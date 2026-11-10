//! Status from evidence (ADR-014): signals from outside the app attach to items and fire the transitions whose
//! `evidence` guard matches. Nobody has to drag a card when the PR merged.

use crate::items::{ItemRef, load_item, transition_item};
use cnote_core::access::assert_space_access;
use cnote_core::{CmdError, CmdResult, Command, Ctx, ToolGroup, handler};
use schemars::JsonSchema;
use serde::Deserialize;
use serde_json::{Value, json};

#[derive(Deserialize, JsonSchema)]
pub struct RecordEvidence {
    /// Where the signal came from: github, stripe, ci...
    pub source: String,
    /// pr_opened | pr_merged | deploy_succeeded | payment_received | ... (lowercase snake_case)
    pub kind: String,
    /// Keys of the items the evidence is about, e.g. ["ENG-12"].
    pub item_keys: Vec<String>,
    #[serde(default)]
    pub url: Option<String>,
}

async fn evidence_record(ctx: &mut Ctx, i: RecordEvidence) -> CmdResult<Value> {
    let kind = i.kind.trim().to_string();
    if kind.is_empty()
        || !kind
            .chars()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '_')
    {
        return Err(CmdError::invalid(
            "kind must be lowercase snake_case, e.g. pr_merged",
        ));
    }
    if i.source.trim().is_empty() || i.item_keys.len() > 50 {
        return Err(CmdError::invalid(
            "source is required and at most 50 item keys are allowed",
        ));
    }
    let (mut recorded, mut transitions, mut skipped, mut unknown) = (0, vec![], vec![], vec![]);
    for key in &i.item_keys {
        let item = match load_item(
            ctx,
            &ItemRef {
                id: None,
                key: Some(key.clone()),
            },
            true,
        )
        .await
        {
            Ok(item) => item,
            Err(e)
                if matches!(
                    e.code,
                    cnote_core::ErrorCode::NotFound | cnote_core::ErrorCode::Forbidden
                ) =>
            {
                unknown.push(key.clone());
                continue;
            }
            Err(e) => return Err(e),
        };
        assert_space_access(ctx, item.space_id, true).await?;
        sqlx::query("insert into projects.evidence (workspace_id, note_id, source, kind, url) values ($1,$2,$3,$4,$5)")
            .bind(ctx.workspace_id())
            .bind(item.id)
            .bind(i.source.trim())
            .bind(&kind)
            .bind(&i.url)
            .execute(&mut *ctx.tx)
            .await?;
        recorded += 1;
        ctx.emit(
            "evidence.recorded",
            Some(item.id),
            json!({"key": item.key, "kind": kind, "source": i.source, "url": i.url}),
        )
        .await?;
        let wf = crate::wf::space_wf(ctx, item.space_id).await?;
        let def = crate::wf::definition_for(ctx, item.space_id, &wf, item.workflow_version).await?;
        let mut why = None;
        for t in def.evidence_driven(&item.status, &kind) {
            let cur = load_item(
                ctx,
                &ItemRef {
                    id: Some(item.id),
                    key: None,
                },
                true,
            )
            .await?;
            match transition_item(ctx, cur, &t.name).await {
                Ok(after) => {
                    transitions.push(json!({"key": item.key, "from": item.status, "to": after.status, "transition": t.name}));
                    why = None;
                    break;
                }
                Err(e) => why = Some(e.message),
            }
        }
        if let Some(reason) = why {
            skipped.push(json!({"key": item.key, "reason": reason}));
        }
    }
    Ok(
        json!({"recorded": recorded, "transitions": transitions, "skipped": skipped, "unknown": unknown}),
    )
}

pub fn commands() -> Vec<Command> {
    vec![Command::new(
        "evidence.record",
        "Record external evidence (PR opened/merged, deploy succeeded, payment received) on items; transitions guarded by that evidence fire automatically.",
        ToolGroup::WriteItems,
        handler!(evidence_record),
    )]
}
