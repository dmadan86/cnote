//! App marketplace: publish, review, install, uninstall. An installed app is an external agent (core.agents) whose
//! scopes are the app's tool groups; it gets `cn_` keys through a one-time install-code exchange (`exchange_token`).
//! Uninstalling deletes the agent, which cascades to its keys. People only: an agent must never install or publish.

use crate::auth::{create_api_key, random_token, sha256_hex};
use crate::command::{Command, Ctx, DEFAULT_AGENT_GROUPS, ToolGroup};
use crate::error::{CmdError, CmdResult};
use crate::handler;
use crate::principal::{Principal, PrincipalKind, Role};
use schemars::JsonSchema;
use serde::Deserialize;
use serde_json::{Value, json};
use sqlx::PgPool;
use uuid::Uuid;

const CATEGORIES: [&str; 6] = [
    "productivity",
    "sales",
    "finance",
    "developer",
    "communication",
    "other",
];
/// 'admin' and 'destructive' are never grantable to an app.
const APP_SCOPES: [&str; 6] = [
    "read",
    "write_notes",
    "write_items",
    "manage_crm",
    "manage_invoices",
    "send_messages",
];
const CODE_TTL_MINUTES: i32 = 10;

fn person(ctx: &Ctx) -> CmdResult<Uuid> {
    match (ctx.principal.kind, ctx.principal.user_id) {
        (PrincipalKind::User, Some(u)) => Ok(u),
        _ => Err(CmdError::forbidden("only people can manage apps")),
    }
}

/// CNOTE_PLATFORM_ADMINS: comma-separated emails; an entry starting with '@' matches an email domain.
async fn platform_admin(ctx: &mut Ctx) -> CmdResult<()> {
    let user = person(ctx)?;
    let email: String = sqlx::query_scalar("select lower(email) from core.users where id = $1")
        .bind(user)
        .fetch_one(&mut *ctx.tx)
        .await?;
    let list = std::env::var("CNOTE_PLATFORM_ADMINS").unwrap_or_default();
    let ok = list
        .split(',')
        .map(|e| e.trim().to_lowercase())
        .filter(|e| !e.is_empty())
        .any(|e| {
            if e.starts_with('@') {
                email.ends_with(&e)
            } else {
                email == e
            }
        });
    if ok {
        Ok(())
    } else {
        Err(CmdError::forbidden("platform admins only"))
    }
}

fn https_url(v: &str, what: &str) -> CmdResult<()> {
    if v.starts_with("https://") && v.len() > 8 && !v.contains(char::is_whitespace) {
        Ok(())
    } else {
        Err(CmdError::invalid(format!("{what} must be an https URL")))
    }
}

/// https, or http only for localhost / 127.0.0.1 (developer testing).
fn redirect_ok(v: &str) -> bool {
    if v.contains(char::is_whitespace) {
        return false;
    }
    if v.starts_with("https://") {
        return v.len() > 8;
    }
    let Some(rest) = v.strip_prefix("http://") else {
        return false;
    };
    let host = rest.split(['/', '?', '#']).next().unwrap_or("");
    let host = host.rsplit_once(':').map_or(host, |(h, _)| h);
    host == "localhost" || host == "127.0.0.1"
}

#[derive(Deserialize, JsonSchema)]
pub struct ListIn {
    #[serde(default)]
    pub q: Option<String>,
}

async fn market_list(ctx: &mut Ctx, i: ListIn) -> CmdResult<Vec<Value>> {
    let q = i.q.map(|q| format!("%{}%", q.trim())).unwrap_or_default();
    Ok(sqlx::query_scalar(
        "select jsonb_build_object('id', a.id, 'slug', a.slug, 'name', a.name, 'tagline', a.tagline,
           'category', a.category, 'icon_url', a.icon_url, 'publisher_name', a.publisher_name, 'scopes', a.scopes,
           'installed', exists(select 1 from core.app_installs i where i.app_id = a.id),
           'installs', core.app_install_count(a.id))
         from core.market_apps a
         where a.status = 'published' and ($1 = '' or a.name ilike $1 or a.tagline ilike $1 or a.slug ilike $1)
         order by core.app_install_count(a.id) desc, a.name",
    )
    .bind(q)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct BySlug {
    pub slug: String,
}

async fn market_get(ctx: &mut Ctx, i: BySlug) -> CmdResult<Value> {
    let ws = ctx.workspace_id();
    let app: Option<Value> = sqlx::query_scalar(
        "select jsonb_build_object('id', a.id, 'slug', a.slug, 'name', a.name, 'tagline', a.tagline,
           'description', a.description, 'category', a.category, 'icon_url', a.icon_url,
           'homepage_url', a.homepage_url, 'publisher_name', a.publisher_name, 'scopes', a.scopes,
           'status', a.status, 'installed', exists(select 1 from core.app_installs i where i.app_id = a.id),
           'installs', core.app_install_count(a.id))
         from core.market_apps a
         where a.slug = $1 and (a.status = 'published' or a.publisher_workspace_id = $2)",
    )
    .bind(&i.slug)
    .bind(ws)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let mut app = app.ok_or_else(|| CmdError::not_found("app"))?;
    let settings: Value = sqlx::query_scalar("select settings from core.workspaces where id = $1")
        .bind(ws)
        .fetch_one(&mut *ctx.tx)
        .await?;
    let enabled: Vec<String> = match settings.get("agent_tool_groups").and_then(|v| v.as_array()) {
        Some(a) => a
            .iter()
            .filter_map(|g| g.as_str().map(String::from))
            .collect(),
        None => DEFAULT_AGENT_GROUPS.iter().map(|s| s.to_string()).collect(),
    };
    let blocked: Vec<&Value> = app["scopes"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|s| !enabled.iter().any(|e| Some(e.as_str()) == s.as_str()))
        .collect();
    app["blocked_scopes"] = json!(blocked);
    Ok(app)
}

#[derive(Deserialize, JsonSchema)]
pub struct AppId {
    pub app_id: Uuid,
}

async fn market_install(ctx: &mut Ctx, i: AppId) -> CmdResult<Value> {
    let user = person(ctx)?;
    let ws = ctx.workspace_id();
    let app: Option<(String, String, Vec<String>)> = sqlx::query_as(
        "select name, redirect_url, scopes from core.market_apps
         where id = $1 and (status = 'published' or publisher_workspace_id = $2)",
    )
    .bind(i.app_id)
    .bind(ws)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    let (name, redirect, scopes) = app.ok_or_else(|| CmdError::not_found("app"))?;
    let exists: bool =
        sqlx::query_scalar("select exists(select 1 from core.app_installs where app_id = $1)")
            .bind(i.app_id)
            .fetch_one(&mut *ctx.tx)
            .await?;
    if exists {
        return Err(CmdError::conflict("app is already installed"));
    }
    let agent: Uuid = sqlx::query_scalar(
        "insert into core.agents (workspace_id, name, kind, owner_user_id, scopes) values ($1,$2,'external',$3,$4) returning id",
    )
    .bind(ws)
    .bind(format!("App: {name}"))
    .bind(user)
    .bind(&scopes)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let install: Uuid = sqlx::query_scalar(
        "insert into core.app_installs (workspace_id, app_id, agent_id, installed_by, scopes) values ($1,$2,$3,$4,$5) returning id",
    )
    .bind(ws)
    .bind(i.app_id)
    .bind(agent)
    .bind(user)
    .bind(&scopes)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let code = format!("cnic_{}", random_token(24));
    sqlx::query(
        "insert into core.app_install_codes (code_hash, install_id, workspace_id, expires_at)
         values ($1,$2,$3, now() + make_interval(mins => $4))",
    )
    .bind(sha256_hex(&code))
    .bind(install)
    .bind(ws)
    .bind(CODE_TTL_MINUTES)
    .execute(&mut *ctx.tx)
    .await?;
    ctx.emit(
        "market.installed",
        None,
        json!({"app_id": i.app_id, "install_id": install}),
    )
    .await?;
    let sep = if redirect.contains('?') { '&' } else { '?' };
    Ok(json!({
        "install_id": install,
        "redirect_to": format!("{redirect}{sep}code={code}&workspace_id={ws}"),
    }))
}

async fn market_uninstall(ctx: &mut Ctx, i: AppId) -> CmdResult<Value> {
    person(ctx)?;
    // Deleting the agent cascades to the install row, its codes and every API key the app holds.
    let n = sqlx::query(
        "delete from core.agents where id = (select agent_id from core.app_installs where app_id = $1)",
    )
    .bind(i.app_id)
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    if n == 1 {
        ctx.emit("market.uninstalled", None, json!({"app_id": i.app_id}))
            .await?;
    }
    Ok(json!({"uninstalled": n == 1}))
}

async fn market_installed(ctx: &mut Ctx, _: crate::notes::Empty) -> CmdResult<Vec<Value>> {
    Ok(sqlx::query_scalar(
        "select jsonb_build_object('app_id', a.id, 'slug', a.slug, 'name', a.name, 'icon_url', a.icon_url,
           'publisher_name', a.publisher_name, 'scopes', i.scopes, 'installed_at', i.created_at,
           'installed_by_name', u.name)
         from core.app_installs i join core.market_apps a on a.id = i.app_id join core.users u on u.id = i.installed_by
         order by a.name",
    )
    .fetch_all(&mut *ctx.tx)
    .await?)
}

/// Everything editable plus status, minus the secret hash.
macro_rules! app_full {
    ($rest:literal) => {
        concat!(
            "select to_jsonb(a) - 'client_secret_hash' || jsonb_build_object('installs', core.app_install_count(a.id)) ",
            $rest
        )
    };
}

async fn dev_apps(ctx: &mut Ctx, _: crate::notes::Empty) -> CmdResult<Vec<Value>> {
    person(ctx)?;
    Ok(sqlx::query_scalar(app_full!(
        "from core.market_apps a where publisher_workspace_id = $1 order by created_at desc"
    ))
    .bind(ctx.workspace_id())
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct SaveApp {
    #[serde(default)]
    pub id: Option<Uuid>,
    pub slug: String,
    pub name: String,
    #[serde(default)]
    pub tagline: String,
    #[serde(default)]
    pub description: String,
    /// productivity | sales | finance | developer | communication | other
    pub category: String,
    #[serde(default)]
    pub icon_url: Option<String>,
    #[serde(default)]
    pub homepage_url: Option<String>,
    /// https (http allowed for localhost / 127.0.0.1). The install code is appended as ?code=...
    pub redirect_url: String,
    /// Subset of read, write_notes, write_items, manage_crm, manage_invoices, send_messages; must include read.
    pub scopes: Vec<String>,
}

fn validate(i: &SaveApp) -> CmdResult<()> {
    let slug_ok = (3..=40).contains(&i.slug.len())
        && i.slug
            .starts_with(|c: char| c.is_ascii_lowercase() || c.is_ascii_digit())
        && i.slug
            .chars()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-');
    if !slug_ok {
        return Err(CmdError::invalid(
            "slug must be 3-40 chars of a-z, 0-9 and '-'",
        ));
    }
    if !(1..=60).contains(&i.name.trim().chars().count()) {
        return Err(CmdError::invalid("name must be 1-60 characters"));
    }
    if i.tagline.chars().count() > 120 || i.description.chars().count() > 4000 {
        return Err(CmdError::invalid(
            "tagline max 120 and description max 4000 characters",
        ));
    }
    if !CATEGORIES.contains(&i.category.as_str()) {
        return Err(CmdError::invalid(format!(
            "category must be one of {}",
            CATEGORIES.join(", ")
        )));
    }
    for (u, what) in [(&i.icon_url, "icon_url"), (&i.homepage_url, "homepage_url")] {
        if let Some(u) = u {
            https_url(u, what)?;
        }
    }
    if !redirect_ok(&i.redirect_url) {
        return Err(CmdError::invalid(
            "redirect_url must be https (http only for localhost)",
        ));
    }
    if !i.scopes.iter().any(|s| s == "read") {
        return Err(CmdError::invalid("scopes must include read"));
    }
    if let Some(s) = i.scopes.iter().find(|s| !APP_SCOPES.contains(&s.as_str())) {
        return Err(CmdError::invalid(format!(
            "scope {s} cannot be granted to apps"
        )));
    }
    Ok(())
}

fn new_secret() -> String {
    format!("cnas_{}", random_token(32))
}

async fn dev_app_save(ctx: &mut Ctx, i: SaveApp) -> CmdResult<Value> {
    person(ctx)?;
    validate(&i)?;
    let ws = ctx.workspace_id();
    let taken: bool = sqlx::query_scalar(
        "select exists(select 1 from core.market_apps where slug = $1 and id is distinct from $2)",
    )
    .bind(&i.slug)
    .bind(i.id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    if taken {
        return Err(CmdError::conflict("slug is already taken"));
    }
    if let Some(id) = i.id {
        // Any edit of a live app goes back through review.
        let n = sqlx::query(
            "update core.market_apps set slug=$3, name=$4, tagline=$5, description=$6, category=$7, icon_url=$8,
               homepage_url=$9, redirect_url=$10, scopes=$11, updated_at=now(),
               status = case when status = 'published' then 'review' else status end
             where id = $1 and publisher_workspace_id = $2",
        )
        .bind(id)
        .bind(ws)
        .bind(&i.slug)
        .bind(i.name.trim())
        .bind(&i.tagline)
        .bind(&i.description)
        .bind(&i.category)
        .bind(&i.icon_url)
        .bind(&i.homepage_url)
        .bind(&i.redirect_url)
        .bind(&i.scopes)
        .execute(&mut *ctx.tx)
        .await?
        .rows_affected();
        if n == 0 {
            return Err(CmdError::not_found("app"));
        }
        return Ok(json!({"app_id": id}));
    }
    let publisher: String = sqlx::query_scalar("select name from core.workspaces where id = $1")
        .bind(ws)
        .fetch_one(&mut *ctx.tx)
        .await?;
    let secret = new_secret();
    let id: Uuid = sqlx::query_scalar(
        "insert into core.market_apps (slug, name, tagline, description, category, icon_url, homepage_url, redirect_url,
           scopes, publisher_workspace_id, publisher_name, client_secret_hash)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) returning id",
    )
    .bind(&i.slug)
    .bind(i.name.trim())
    .bind(&i.tagline)
    .bind(&i.description)
    .bind(&i.category)
    .bind(&i.icon_url)
    .bind(&i.homepage_url)
    .bind(&i.redirect_url)
    .bind(&i.scopes)
    .bind(ws)
    .bind(publisher)
    .bind(sha256_hex(&secret))
    .fetch_one(&mut *ctx.tx)
    .await?;
    Ok(json!({"app_id": id, "client_secret": secret}))
}

#[derive(Deserialize, JsonSchema)]
pub struct ById {
    pub id: Uuid,
}

async fn dev_app_submit(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    person(ctx)?;
    let n = sqlx::query(
        "update core.market_apps set status = 'review', updated_at = now()
         where id = $1 and publisher_workspace_id = $2 and status in ('draft','rejected')",
    )
    .bind(i.id)
    .bind(ctx.workspace_id())
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    if n == 0 {
        return Err(CmdError::conflict(
            "only your draft or rejected apps can be submitted",
        ));
    }
    Ok(json!({"status": "review"}))
}

async fn dev_app_rotate_secret(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    person(ctx)?;
    let secret = new_secret();
    let n = sqlx::query(
        "update core.market_apps set client_secret_hash = $3, updated_at = now() where id = $1 and publisher_workspace_id = $2",
    )
    .bind(i.id)
    .bind(ctx.workspace_id())
    .bind(sha256_hex(&secret))
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    if n == 0 {
        return Err(CmdError::not_found("app"));
    }
    Ok(json!({"client_secret": secret}))
}

async fn dev_app_delete(ctx: &mut Ctx, i: ById) -> CmdResult<Value> {
    person(ctx)?;
    let installs: Option<i64> = sqlx::query_scalar(
        "select core.app_install_count(id) from core.market_apps where id = $1 and publisher_workspace_id = $2",
    )
    .bind(i.id)
    .bind(ctx.workspace_id())
    .fetch_optional(&mut *ctx.tx)
    .await?;
    match installs {
        None => return Err(CmdError::not_found("app")),
        Some(n) if n > 0 => return Err(CmdError::conflict("app still has installs")),
        _ => {}
    }
    sqlx::query("delete from core.market_apps where id = $1")
        .bind(i.id)
        .execute(&mut *ctx.tx)
        .await?;
    Ok(json!({"deleted": true}))
}

async fn review_queue(ctx: &mut Ctx, _: crate::notes::Empty) -> CmdResult<Vec<Value>> {
    platform_admin(ctx).await?;
    Ok(sqlx::query_scalar(app_full!(
        "from core.market_apps a where status = 'review' order by updated_at"
    ))
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[derive(Deserialize, JsonSchema)]
pub struct Review {
    pub id: Uuid,
    pub approve: bool,
    #[serde(default)]
    pub note: Option<String>,
}

async fn market_review(ctx: &mut Ctx, i: Review) -> CmdResult<Value> {
    platform_admin(ctx).await?;
    let status = if i.approve { "published" } else { "rejected" };
    let n = sqlx::query(
        "update core.market_apps set status = $2, review_note = $3, updated_at = now() where id = $1 and status = 'review'",
    )
    .bind(i.id)
    .bind(status)
    .bind(i.note)
    .execute(&mut *ctx.tx)
    .await?
    .rows_affected();
    if n == 0 {
        return Err(CmdError::not_found("app in review"));
    }
    Ok(json!({"status": status}))
}

fn ct_eq(a: &str, b: &str) -> bool {
    a.len() == b.len()
        && a.bytes()
            .zip(b.bytes())
            .fold(0u8, |acc, (x, y)| acc | (x ^ y))
            == 0
}

/// Token endpoint core: app credentials + one-time install code -> a `cn_` key for the app's agent.
/// `None` for any failure (the caller answers `invalid_grant` without detail).
pub async fn exchange_token(
    pool: &PgPool,
    client_id: &str,
    client_secret: &str,
    code: &str,
) -> CmdResult<Option<Value>> {
    let Ok(app_id) = client_id.parse::<Uuid>() else {
        return Ok(None);
    };
    let app: Option<(String, String)> =
        sqlx::query_as("select client_secret_hash, name from core.market_apps where id = $1")
            .bind(app_id)
            .fetch_optional(pool)
            .await?;
    // Hash the presented secret even for unknown apps so both paths cost the same.
    let presented = sha256_hex(client_secret);
    let Some((_, name)) = app.filter(|(h, _)| ct_eq(h, &presented)) else {
        return Ok(None);
    };
    let used: Option<(Uuid, Uuid)> = sqlx::query_as(
        "update core.app_install_codes set used_at = now()
         where code_hash = $1 and used_at is null and expires_at > now() returning install_id, workspace_id",
    )
    .bind(sha256_hex(code))
    .fetch_optional(pool)
    .await?;
    let Some((install, ws)) = used else {
        return Ok(None);
    };
    let mut tx = cnote_db::begin_tenant(pool, ws).await?;
    let row: Option<(Uuid, Uuid, Vec<String>)> = sqlx::query_as(
        "select agent_id, installed_by, scopes from core.app_installs where id = $1 and app_id = $2",
    )
    .bind(install)
    .bind(app_id)
    .fetch_optional(&mut *tx)
    .await?;
    tx.commit().await?;
    let Some((agent, user, scopes)) = row else {
        return Ok(None);
    };
    let p = Principal::user(user, ws, Role::Member);
    let (_, key) = create_api_key(
        pool,
        &p,
        &format!("Marketplace: {name}"),
        &scopes,
        Some(agent),
    )
    .await?;
    Ok(Some(json!({
        "access_token": key,
        "token_type": "bearer",
        "workspace_id": ws,
        "scopes": scopes,
    })))
}

pub fn commands() -> Vec<Command> {
    vec![
        Command::new(
            "market.list",
            "Browse published marketplace apps (optional search text q).",
            ToolGroup::Read,
            handler!(market_list),
        ),
        Command::new(
            "market.get",
            "One marketplace app by slug, with the tool groups this workspace has not enabled for it.",
            ToolGroup::Read,
            handler!(market_get),
        ),
        Command::new(
            "market.install",
            "Install a marketplace app; returns the URL to send the admin to so the app can finish setup.",
            ToolGroup::Admin,
            handler!(market_install),
        )
        .min_role(Role::Admin),
        Command::new(
            "market.uninstall",
            "Uninstall an app: its access keys stop working immediately.",
            ToolGroup::Admin,
            handler!(market_uninstall),
        )
        .min_role(Role::Admin),
        Command::new(
            "market.installed",
            "Apps installed in this workspace.",
            ToolGroup::Read,
            handler!(market_installed),
        ),
        Command::new(
            "dev.apps",
            "Apps your workspace publishes.",
            ToolGroup::Admin,
            handler!(dev_apps),
        )
        .min_role(Role::Admin),
        Command::new(
            "dev.app_save",
            "Create or update an app you publish. Creating returns the client secret once.",
            ToolGroup::Admin,
            handler!(dev_app_save),
        )
        .min_role(Role::Admin)
        .returns_secret(),
        Command::new(
            "dev.app_submit",
            "Submit a draft or rejected app for review.",
            ToolGroup::Admin,
            handler!(dev_app_submit),
        )
        .min_role(Role::Admin),
        Command::new(
            "dev.app_rotate_secret",
            "Issue a new client secret for your app (the old one stops working).",
            ToolGroup::Admin,
            handler!(dev_app_rotate_secret),
        )
        .min_role(Role::Admin)
        .returns_secret(),
        Command::new(
            "dev.app_delete",
            "Delete your app (only when nobody has it installed).",
            ToolGroup::Admin,
            handler!(dev_app_delete),
        )
        .min_role(Role::Admin),
        Command::new(
            "market.review_queue",
            "Apps waiting for review (platform admins only).",
            ToolGroup::Admin,
            handler!(review_queue),
        )
        .min_role(Role::Admin),
        Command::new(
            "market.review",
            "Approve or reject an app in review (platform admins only).",
            ToolGroup::Admin,
            handler!(market_review),
        )
        .min_role(Role::Admin),
    ]
}
