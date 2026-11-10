//! cnote-projects: items, workflow engine (ADR-007), CNQL, sprints, automations and evidence-driven status (ADR-014).

pub mod automation;
pub mod cnql;
pub mod evidence;
pub mod github;
pub mod items;
pub mod planning;
pub mod wf;
pub mod workflow;

pub use github::handle_github_webhook;

pub fn module() -> cnote_core::Module {
    let mut commands = items::commands();
    commands.extend(planning::commands());
    commands.extend(wf::commands());
    commands.extend(evidence::commands());
    commands.extend(automation::commands());
    cnote_core::Module {
        commands,
        event_handlers: vec![("", automation::on_event)],
        sweeps: vec![("projects.automations", 60, automation::sweep)],
        ..Default::default()
    }
}
