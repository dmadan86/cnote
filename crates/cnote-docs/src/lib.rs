//! cnote-docs: the living-page model (ADR-013). Pages are Notes without types that have owners, verify-by dates,
//! bindings to what they describe, and a freshness engine that proposes (never applies) edits.

pub mod bindings;
mod commands;
pub mod freshness;

use cnote_core::Module;

pub fn module() -> Module {
    Module {
        commands: commands::commands(),
        event_handlers: vec![
            ("note.", |ctx, ev| Box::pin(bindings::sync_handler(ctx, ev))),
            ("", |ctx, ev| Box::pin(freshness::on_event(ctx, ev))),
        ],
        job_handlers: vec![("docs.propose_edit", freshness::propose_edit_job)],
        sweeps: vec![("docs.freshness_sweep", 24 * 3600, freshness::sweep)],
        ..Default::default()
    }
}
