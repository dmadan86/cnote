//! ADR-012 importers: Jira, Trello, Notion, Confluence and CSV into the universal Note, keeping identity and history.

mod html;
pub mod jira;
mod model;
mod others;
mod run;

use cnote_core::access::assert_space_access;
use cnote_core::error::{CmdError, CmdResult};
use cnote_core::events::enqueue;
use cnote_core::{Command, Ctx, Module, Role, ToolGroup, handler};
use futures::future::BoxFuture;
use model::Parsed;
use others::CsvMapping;
use schemars::JsonSchema;
use serde::Deserialize;
use serde_json::{Value, json};
use uuid::Uuid;

#[derive(Deserialize, JsonSchema, Clone, Copy, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum Source {
    Jira,
    Confluence,
    Notion,
    Trello,
    Csv,
}

impl Source {
    fn as_str(self) -> &'static str {
        match self {
            Self::Jira => "jira",
            Self::Confluence => "confluence",
            Self::Notion => "notion",
            Self::Trello => "trello",
            Self::Csv => "csv",
        }
    }
}

/// Live Jira Cloud fetch. The token is used for this call only: it is never stored or logged.
#[derive(Deserialize, JsonSchema)]
pub struct JiraLive {
    /// https://yourcompany.atlassian.net
    pub base_url: String,
    pub email: String,
    /// Jira API token
    pub api_token: String,
    /// Which issues to import, e.g. `project = ENG`
    pub jql: String,
}

#[derive(Deserialize, JsonSchema)]
pub struct StartImport {
    pub source: Source,
    /// Space that receives the imported items or pages.
    pub space_id: Uuid,
    /// The export, as text. jira: search-response JSON `{"issues":[...],"filters":[...],"automation_rules":[...]}`;
    /// trello: board JSON; notion: Markdown or `{"pages":[{id,title,parent_id,markdown}]}`;
    /// confluence: storage/HTML or `{"pages":[{id,title,parent_id,body}]}`; csv: CSV text with a header row.
    #[serde(default)]
    pub content: Option<String>,
    /// jira only, instead of `content`: fetch the issues from Jira Cloud.
    #[serde(default)]
    pub jira: Option<JiraLive>,
    /// csv only: which column holds what.
    #[serde(default)]
    pub mapping: Option<CsvMapping>,
    /// Records per background batch (default 200).
    #[serde(default)]
    pub batch_size: Option<u32>,
}

fn parse_input(source: &str, input: &Value) -> Result<Parsed, String> {
    let content = input["content"].as_str().unwrap_or("");
    match source {
        "jira" => jira::parse(content),
        "trello" => others::trello(content),
        "notion" | "confluence" => others::pages(source, content),
        "csv" => {
            let m: CsvMapping = serde_json::from_value(input["mapping"].clone()).map_err(|_| {
                "csv imports need a `mapping` with at least a title column".to_string()
            })?;
            others::csv(content, &m)
        }
        _ => Err(format!("unknown source {source}")),
    }
}

async fn import_start(ctx: &mut Ctx, i: StartImport) -> CmdResult<Value> {
    assert_space_access(ctx, i.space_id, true).await?;
    let content = match (i.content, i.jira) {
        (_, Some(j)) if i.source == Source::Jira => {
            let v = jira::fetch_issues(
                &jira::ReqwestJira,
                &j.base_url,
                &j.email,
                &j.api_token,
                &j.jql,
            )
            .await
            .map_err(|e| CmdError::invalid(format!("Jira fetch failed: {e}")))?;
            v.to_string()
        }
        (Some(c), _) => c,
        _ => {
            return Err(CmdError::invalid(
                "provide `content` (or `jira` for a live Jira import)",
            ));
        }
    };
    let input = json!({"content": content, "mapping": i.mapping, "batch_size": i.batch_size});
    let records = parse_input(i.source.as_str(), &input)
        .map_err(CmdError::invalid)?
        .recs
        .len();
    if records == 0 {
        return Err(CmdError::invalid(
            "nothing to import: the content has no records",
        ));
    }
    let id: Uuid = sqlx::query_scalar(
        "insert into importers.jobs (workspace_id, source, space_id, input, created_by) values ($1,$2,$3,$4,$5) returning id",
    )
    .bind(ctx.workspace_id())
    .bind(i.source.as_str())
    .bind(i.space_id)
    .bind(&input)
    .bind(ctx.principal.user_id)
    .fetch_one(&mut *ctx.tx)
    .await?;
    let ws = ctx.workspace_id();
    enqueue(
        &mut ctx.tx,
        Some(ws),
        "import.run",
        json!({"import_id": id}),
        None,
    )
    .await?;
    Ok(json!({"id": id, "status": "queued", "records": records}))
}

#[derive(Deserialize, JsonSchema)]
pub struct ImportId {
    pub id: Uuid,
}

async fn import_status(ctx: &mut Ctx, i: ImportId) -> CmdResult<Value> {
    let v: Option<Value> =
        sqlx::query_scalar("select to_jsonb(j) - 'input' from importers.jobs j where id = $1")
            .bind(i.id)
            .fetch_optional(&mut *ctx.tx)
            .await?;
    v.ok_or_else(|| CmdError::not_found("import"))
}

#[derive(Deserialize, JsonSchema, Default)]
pub struct Empty {}

async fn import_list(ctx: &mut Ctx, _: Empty) -> CmdResult<Value> {
    Ok(sqlx::query_scalar(
        "select coalesce(jsonb_agg(to_jsonb(j) - 'input' order by j.created_at desc), '[]'::jsonb)
         from (select * from importers.jobs order by created_at desc limit 50) j",
    )
    .fetch_one(&mut *ctx.tx)
    .await?)
}

fn run_job<'a>(ctx: &'a mut Ctx, payload: Value) -> BoxFuture<'a, CmdResult<()>> {
    Box::pin(run::run(ctx, payload))
}

pub fn module() -> Module {
    Module {
        commands: vec![
            Command::new(
                "import.start",
                "Start a resumable background import from Jira, Trello, Notion, Confluence or CSV. Re-running updates instead of duplicating.",
                ToolGroup::WriteNotes,
                handler!(import_start),
            )
            .min_role(Role::Admin),
            Command::new("import.status", "Progress and fidelity report of an import.", ToolGroup::Read, handler!(import_status))
                .min_role(Role::Admin),
            Command::new("import.list", "Recent imports with their reports.", ToolGroup::Read, handler!(import_list))
                .min_role(Role::Admin),
        ],
        job_handlers: vec![("import.run", run_job)],
        ..Default::default()
    }
}
