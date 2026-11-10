//! One binary, three entrypoints (ADR-001): `serve` (HTTP API + MCP + collab), `worker` (jobs, events, sweeps), `migrate`.

mod api;
mod modules;
mod worker;

use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(name = "cnote", about = "C Note backend")]
struct Cli {
    #[command(subcommand)]
    cmd: Option<Cmd>,
}

#[derive(Subcommand)]
enum Cmd {
    /// HTTP API, MCP endpoint and collaboration gateway (default)
    Serve {
        #[arg(long, env = "PORT", default_value = "4000")]
        port: u16,
        /// Also run the worker loop in-process (handy for dev and single-instance deploys)
        #[arg(long, env = "WITH_WORKER", default_value_t = false)]
        with_worker: bool,
    },
    /// Background jobs: event dispatch, automations, freshness engine, imports
    Worker,
    /// Apply database migrations with DATABASE_ADMIN_URL
    Migrate {
        #[arg(long)]
        reset: bool,
    },
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let _ = dotenvy::dotenv();
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "info,sqlx=warn".into()),
        )
        .init();
    let cli = Cli::parse();
    match cli.cmd.unwrap_or(Cmd::Serve {
        port: 4000,
        with_worker: false,
    }) {
        Cmd::Migrate { reset } => {
            let url = std::env::var("DATABASE_ADMIN_URL")?;
            cnote_db::migrate(&url, reset).await?;
            tracing::info!("migrations applied");
        }
        Cmd::Worker => {
            let state = api::AppState::from_env().await?;
            worker::run(state.pool.clone(), state.registry.clone()).await?;
        }
        Cmd::Serve { port, with_worker } => {
            let state = api::AppState::from_env().await?;
            if with_worker {
                tokio::spawn(worker::run(state.pool.clone(), state.registry.clone()));
            }
            let app = api::router(state);
            let listener = tokio::net::TcpListener::bind(("0.0.0.0", port)).await?;
            tracing::info!("listening on :{port}");
            axum::serve(listener, app).await?;
        }
    }
    Ok(())
}
