//! Durable job loop (ADR-001/007): event dispatch to module handlers, module jobs, periodic sweeps.

use cnote_core::events::Event;
use cnote_core::{Ctx, Principal, Registry};
use serde_json::Value;
use sqlx::PgPool;
use std::sync::Arc;
use std::time::Duration;
use uuid::Uuid;

#[derive(sqlx::FromRow)]
struct Job {
    id: i64,
    workspace_id: Option<Uuid>,
    kind: String,
    payload: Value,
    attempts: i32,
    max_attempts: i32,
}

pub async fn run(pool: PgPool, registry: Arc<Registry>) -> anyhow::Result<()> {
    for (name, every, sweep) in registry.sweeps.clone() {
        let (pool, registry) = (pool.clone(), registry.clone());
        tokio::spawn(async move {
            let mut tick = tokio::time::interval(Duration::from_secs(every));
            loop {
                tick.tick().await;
                if let Err(e) = sweep(pool.clone(), registry.clone()).await {
                    tracing::error!(sweep = name, error = %e, "sweep failed");
                }
            }
        });
    }
    tracing::info!("worker started");
    loop {
        match drain_once(&pool, &registry, 20).await {
            Ok(0) => tokio::time::sleep(Duration::from_millis(500)).await,
            Ok(_) => {}
            Err(e) => {
                tracing::error!(error = %e, "worker poll failed");
                tokio::time::sleep(Duration::from_secs(2)).await;
            }
        }
    }
}

/// Claim and run up to `limit` jobs. Returns how many were claimed. Also used by tests.
pub async fn drain_once(
    pool: &PgPool,
    registry: &Arc<Registry>,
    limit: i32,
) -> anyhow::Result<usize> {
    let jobs: Vec<Job> = sqlx::query_as(
        "select id, workspace_id, kind, payload, attempts, max_attempts from core.claim_jobs(null, $1)",
    )
    .bind(limit)
    .fetch_all(pool)
    .await?;
    let n = jobs.len();
    for job in jobs {
        let result = run_job(pool, registry, &job).await;
        let mut tx = match job.workspace_id {
            Some(ws) => cnote_db::begin_tenant(pool, ws).await?,
            None => cnote_db::begin_global(pool).await?,
        };
        match result {
            Ok(()) => {
                sqlx::query(
                    "update core.jobs set status = 'done', updated_at = now() where id = $1",
                )
                .bind(job.id)
                .execute(&mut *tx)
                .await?;
            }
            Err(e) => {
                tracing::warn!(job = job.id, kind = %job.kind, error = %e, "job failed");
                let failed = job.attempts >= job.max_attempts;
                sqlx::query(
                    "update core.jobs set status = $2, last_error = $3, updated_at = now(),
                       run_at = now() + make_interval(secs => power(2, attempts)) where id = $1",
                )
                .bind(job.id)
                .bind(if failed { "failed" } else { "queued" })
                .bind(e.to_string())
                .execute(&mut *tx)
                .await?;
            }
        }
        tx.commit().await?;
    }
    Ok(n)
}

async fn run_job(pool: &PgPool, registry: &Arc<Registry>, job: &Job) -> anyhow::Result<()> {
    let ws = job
        .workspace_id
        .ok_or_else(|| anyhow::anyhow!("job without workspace"))?;
    let tx = cnote_db::begin_tenant(pool, ws).await?;
    let mut ctx = Ctx {
        tx,
        principal: Principal::system(ws),
        registry: registry.clone(),
    };
    if job.kind == "event.dispatch" {
        let id = job.payload["event_id"].as_i64().unwrap_or_default();
        let event: Option<Event> = sqlx::query_as("select * from core.events where id = $1")
            .bind(id)
            .fetch_optional(&mut *ctx.tx)
            .await?;
        let Some(event) = event else { return Ok(()) };
        // Handlers run as the system principal, attributed to the human behind the event when there is one.
        ctx.principal.user_id = event.on_behalf_of.or_else(|| {
            event
                .actor_id
                .as_deref()
                .and_then(|a| a.parse().ok())
                .filter(|_| event.actor_type == "user")
        });
        for (prefix, h) in registry.event_handlers.iter() {
            if event.kind.starts_with(prefix) {
                h(&mut ctx, &event)
                    .await
                    .map_err(|e| anyhow::anyhow!("{prefix}: {e}"))?;
            }
        }
    } else if let Some(h) = registry.job_handlers.get(job.kind.as_str()) {
        h(&mut ctx, job.payload.clone())
            .await
            .map_err(|e| anyhow::anyhow!("{}: {e}", job.kind))?;
    } else {
        tracing::warn!(kind = %job.kind, "no handler for job kind");
    }
    ctx.tx.commit().await?;
    Ok(())
}
