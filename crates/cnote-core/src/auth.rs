//! Human sessions, API keys and agent principals.

use crate::error::{CmdError, CmdResult, ErrorCode};
use crate::principal::{Principal, PrincipalKind, Role};
use base64::Engine;
use rand::RngCore;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use uuid::Uuid;

const SESSION_DAYS: i64 = 30;

/// HMAC-SHA256 (RFC 2104) on top of sha2, to avoid another dependency for twelve lines.
pub fn hmac_sha256(key: &[u8], msg: &[u8]) -> [u8; 32] {
    let mut k = [0u8; 64];
    if key.len() > 64 {
        k[..32].copy_from_slice(&Sha256::digest(key));
    } else {
        k[..key.len()].copy_from_slice(key);
    }
    let pad = |b: u8| k.iter().map(|x| x ^ b).collect::<Vec<u8>>();
    let inner = Sha256::new()
        .chain_update(pad(0x36))
        .chain_update(msg)
        .finalize();
    Sha256::new()
        .chain_update(pad(0x5c))
        .chain_update(inner)
        .finalize()
        .into()
}

pub fn sha256_hex(s: &str) -> String {
    hex::encode(Sha256::digest(s.as_bytes()))
}

pub fn random_token(bytes: usize) -> String {
    let mut buf = vec![0u8; bytes];
    rand::rng().fill_bytes(&mut buf);
    base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(buf)
}

pub fn hash_password(pw: &str) -> CmdResult<String> {
    use argon2::PasswordHasher;
    argon2::Argon2::default()
        .hash_password(pw.as_bytes())
        .map(|h| h.to_string())
        .map_err(|e| CmdError::internal(format!("hash: {e}")))
}

pub fn verify_password(pw: &str, stored: &str) -> bool {
    use argon2::{PasswordHash, PasswordVerifier};
    PasswordHash::new(stored)
        .map(|h| {
            argon2::Argon2::default()
                .verify_password(pw.as_bytes(), &h)
                .is_ok()
        })
        .unwrap_or(false)
}

pub async fn sign_up(pool: &PgPool, email: &str, name: &str, password: &str) -> CmdResult<Uuid> {
    let email = email.trim().to_lowercase();
    if !email.contains('@') {
        return Err(CmdError::invalid("a valid email is required"));
    }
    if password.len() < 8 {
        return Err(CmdError::invalid("password must be at least 8 characters"));
    }
    let hash = hash_password(password)?;
    let id: Option<Uuid> = sqlx::query_scalar(
        "insert into core.users (email, name, password_hash) values ($1,$2,$3) on conflict (email) do nothing returning id",
    )
    .bind(&email)
    .bind(name.trim())
    .bind(hash)
    .fetch_optional(pool)
    .await?;
    id.ok_or_else(|| CmdError::conflict("an account with this email already exists"))
}

/// Returns (user id, session token).
pub async fn sign_in(pool: &PgPool, email: &str, password: &str) -> CmdResult<(Uuid, String)> {
    let row: Option<(Uuid, Option<String>)> =
        sqlx::query_as("select id, password_hash from core.users where email = $1")
            .bind(email.trim().to_lowercase())
            .fetch_optional(pool)
            .await?;
    match row {
        Some((id, Some(hash))) if verify_password(password, &hash) => {
            Ok((id, create_session(pool, id).await?))
        }
        _ => Err(CmdError::new(
            ErrorCode::Unauthenticated,
            "invalid email or password",
        )),
    }
}

pub async fn create_session(pool: &PgPool, user_id: Uuid) -> CmdResult<String> {
    let token = random_token(32);
    sqlx::query("insert into core.sessions (id, user_id, expires_at) values ($1, $2, now() + make_interval(days => $3))")
        .bind(sha256_hex(&token))
        .bind(user_id)
        .bind(SESSION_DAYS as i32)
        .execute(pool)
        .await?;
    Ok(token)
}

pub async fn destroy_session(pool: &PgPool, token: &str) -> CmdResult<()> {
    sqlx::query("delete from core.sessions where id = $1")
        .bind(sha256_hex(token))
        .execute(pool)
        .await?;
    Ok(())
}

#[derive(Debug, Clone, serde::Serialize, sqlx::FromRow)]
pub struct SessionUser {
    pub id: Uuid,
    pub email: String,
    pub name: String,
}

pub async fn user_from_session(pool: &PgPool, token: &str) -> CmdResult<Option<SessionUser>> {
    Ok(sqlx::query_as(
        "select u.id, u.email, u.name from core.sessions s join core.users u on u.id = s.user_id
         where s.id = $1 and s.expires_at > now()",
    )
    .bind(sha256_hex(token))
    .fetch_optional(pool)
    .await?)
}

#[derive(Debug, Clone, serde::Serialize, sqlx::FromRow)]
pub struct WorkspaceRef {
    pub id: Uuid,
    pub name: String,
    pub slug: String,
    pub role: String,
}

pub async fn user_workspaces(pool: &PgPool, user_id: Uuid) -> CmdResult<Vec<WorkspaceRef>> {
    Ok(sqlx::query_as("select * from core.user_workspaces($1)")
        .bind(user_id)
        .fetch_all(pool)
        .await?)
}

/// Principal for a human in a workspace; Forbidden if not a member.
pub async fn user_principal(
    pool: &PgPool,
    user_id: Uuid,
    workspace_id: Uuid,
) -> CmdResult<Principal> {
    let mut tx = cnote_db::begin_tenant(pool, workspace_id).await?;
    let role: Option<String> =
        sqlx::query_scalar("select role from core.memberships where user_id = $1")
            .bind(user_id)
            .fetch_optional(&mut *tx)
            .await?;
    tx.commit().await?;
    let role = role.ok_or_else(|| CmdError::forbidden("not a member of this workspace"))?;
    Ok(Principal::user(user_id, workspace_id, Role::parse(&role)))
}

/// Create a key `cn_<prefix>_<secret>`. Only the hash is stored; the plaintext is returned once.
pub async fn create_api_key(
    pool: &PgPool,
    p: &Principal,
    name: &str,
    scopes: &[String],
    agent_id: Option<Uuid>,
) -> CmdResult<(Uuid, String)> {
    let prefix = &hex::encode(Uuid::new_v4().as_bytes())[..8];
    let key = format!("cn_{prefix}_{}", random_token(24));
    let mut tx = cnote_db::begin_tenant(pool, p.workspace_id).await?;
    let id: Uuid = sqlx::query_scalar(
        "insert into core.api_keys (workspace_id, user_id, agent_id, name, prefix, key_hash, scopes)
         values ($1,$2,$3,$4,$5,$6,$7) returning id",
    )
    .bind(p.workspace_id)
    .bind(p.user_id)
    .bind(agent_id)
    .bind(name)
    .bind(prefix)
    .bind(sha256_hex(&key))
    .bind(scopes)
    .fetch_one(&mut *tx)
    .await?;
    tx.commit().await?;
    Ok((id, key))
}

/// Resolve a bearer API key. Agents are capped by their owner's role and never act as owner.
pub async fn principal_from_api_key(pool: &PgPool, key: &str) -> CmdResult<Option<Principal>> {
    if !key.starts_with("cn_") {
        return Ok(None);
    }
    let row: Option<(Uuid, Uuid, Option<Uuid>, Vec<String>)> = sqlx::query_as(
        "select workspace_id, user_id, agent_id, scopes from core.resolve_api_key($1)",
    )
    .bind(sha256_hex(key))
    .fetch_optional(pool)
    .await?;
    let Some((ws, user, agent, scopes)) = row else {
        return Ok(None);
    };
    let Ok(owner) = user_principal(pool, user, ws).await else {
        return Ok(None);
    };
    Ok(Some(Principal {
        kind: if agent.is_some() {
            PrincipalKind::Agent
        } else {
            PrincipalKind::ApiKey
        },
        user_id: Some(user),
        agent_id: agent,
        workspace_id: ws,
        role: owner.role.min(Role::Admin),
        scopes: if scopes.is_empty() {
            None
        } else {
            Some(scopes)
        },
    }))
}

/// Prefix of OAuth access tokens issued to MCP clients (see cnote-server oauth.rs).
pub const OAUTH_ACCESS_PREFIX: &str = "cno_";

/// Resolve an OAuth access token (issued for `resource`, the MCP endpoint URL) to an agent principal on the workspace the user approved.
/// Same cap as API keys: the user's current role, never above admin. Revoked grants, expired tokens
/// and users who left the workspace all resolve to None.
pub async fn principal_from_oauth_token(
    pool: &PgPool,
    token: &str,
    resource: &str,
) -> CmdResult<Option<Principal>> {
    if !token.starts_with(OAUTH_ACCESS_PREFIX) {
        return Ok(None);
    }
    let row: Option<(Uuid, Uuid, Uuid, bool)> = sqlx::query_as(
        "select g.id, g.user_id, g.workspace_id,
                (g.last_used_at is null or g.last_used_at < now() - interval '1 minute')
         from core.oauth_tokens t join core.oauth_grants g on g.id = t.grant_id
         where t.token_hash = $1 and t.kind = 'access' and t.expires_at > now() and g.revoked_at is null
           and g.resource = $2",
    )
    .bind(sha256_hex(token))
    .bind(resource)
    .fetch_optional(pool)
    .await?;
    let Some((grant, user, ws, stale)) = row else {
        return Ok(None);
    };
    let Ok(owner) = user_principal(pool, user, ws).await else {
        return Ok(None);
    };
    if stale {
        sqlx::query("update core.oauth_grants set last_used_at = now() where id = $1")
            .bind(grant)
            .execute(pool)
            .await?;
    }
    Ok(Some(Principal {
        kind: PrincipalKind::Agent,
        user_id: Some(user),
        agent_id: None,
        workspace_id: ws,
        role: owner.role.min(Role::Admin),
        scopes: None,
    }))
}

/// Any bearer credential an agent may present: `cn_` API key or `cno_` OAuth access token.
pub async fn principal_from_bearer(
    pool: &PgPool,
    token: &str,
    oauth_resource: &str,
) -> CmdResult<Option<Principal>> {
    if token.starts_with(OAUTH_ACCESS_PREFIX) {
        principal_from_oauth_token(pool, token, oauth_resource).await
    } else {
        principal_from_api_key(pool, token).await
    }
}

pub fn slugify(s: &str) -> String {
    let mut out = String::new();
    for c in s.to_lowercase().chars() {
        if c.is_ascii_alphanumeric() {
            out.push(c)
        } else if !out.ends_with('-') {
            out.push('-')
        }
    }
    let out = out.trim_matches('-').chars().take(40).collect::<String>();
    if out.is_empty() {
        "workspace".into()
    } else {
        out
    }
}

/// Create a workspace owned by `user_id`, then run module bootstrap hooks inside the tenant.
pub async fn create_workspace(
    pool: &PgPool,
    registry: &crate::command::Registry,
    user_id: Uuid,
    name: &str,
) -> CmdResult<Uuid> {
    if name.trim().is_empty() {
        return Err(CmdError::invalid("name is required"));
    }
    let id: Uuid = sqlx::query_scalar("select core.create_workspace($1, $2, $3)")
        .bind(name.trim())
        .bind(slugify(name))
        .bind(user_id)
        .fetch_one(pool)
        .await?;
    let mut tx = cnote_db::begin_tenant(pool, id).await?;
    for hook in &registry.workspace_hooks {
        hook(&mut tx, id, user_id).await?;
    }
    tx.commit().await?;
    Ok(id)
}
