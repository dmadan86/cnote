//! Database access: pool, tenant-scoped transactions (ADR-003) and embedded migrations.

use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
use sqlx::{PgPool, Postgres, Transaction};
use std::str::FromStr;
use uuid::Uuid;

pub use sqlx;
pub type Tx = Transaction<'static, Postgres>;

pub static MIGRATOR: sqlx::migrate::Migrator = sqlx::migrate!("../../migrations");

/// App pool. Must connect as `cnote_app` (not the table owner) so row-level security applies.
pub async fn connect(url: &str, max: u32) -> anyhow::Result<PgPool> {
    let opts = PgConnectOptions::from_str(url)?;
    Ok(PgPoolOptions::new()
        .max_connections(max)
        .connect_with(opts)
        .await?)
}

/// Run migrations with the owner role (DATABASE_ADMIN_URL).
pub async fn migrate(admin_url: &str, reset: bool) -> anyhow::Result<()> {
    let pool = PgPoolOptions::new()
        .max_connections(1)
        .connect(admin_url)
        .await?;
    if reset {
        sqlx::raw_sql(
            "DROP SCHEMA IF EXISTS core, projects, docs, crm, billing, ai, importers CASCADE; \
             DROP TABLE IF EXISTS public._sqlx_migrations;",
        )
        .execute(&pool)
        .await?;
    }
    MIGRATOR.run(&pool).await?;
    pool.close().await;
    Ok(())
}

/// Begin a transaction bound to one tenant. Every tenant query goes through here, background jobs included.
/// `set_config(..., true)` is transaction-local, so pooled connections never leak the tenant.
pub async fn begin_tenant(pool: &PgPool, workspace_id: Uuid) -> sqlx::Result<Tx> {
    let mut tx = pool.begin().await?;
    sqlx::query("select set_config('app.workspace_id', $1, true)")
        .bind(workspace_id.to_string())
        .execute(&mut *tx)
        .await?;
    Ok(tx)
}

/// Transaction for global tables (users, sessions) and SECURITY DEFINER helpers.
pub async fn begin_global(pool: &PgPool) -> sqlx::Result<Tx> {
    pool.begin().await
}

/// Test helper: a fresh user + workspace. Integration tests share one database; ids keep them apart.
pub async fn create_test_tenant(pool: &PgPool, name: &str) -> anyhow::Result<(Uuid, Uuid)> {
    let suffix = &Uuid::new_v4().to_string()[..8];
    let mut tx = pool.begin().await?;
    let user: Uuid =
        sqlx::query_scalar("insert into core.users(email, name) values ($1, $2) returning id")
            .bind(format!("{suffix}@test.local"))
            .bind(name)
            .fetch_one(&mut *tx)
            .await?;
    let ws: Uuid = sqlx::query_scalar("select core.create_workspace($1, $2, $3)")
        .bind(format!("{name} {suffix}"))
        .bind(format!("t-{suffix}"))
        .bind(user)
        .fetch_one(&mut *tx)
        .await?;
    tx.commit().await?;
    Ok((user, ws))
}
