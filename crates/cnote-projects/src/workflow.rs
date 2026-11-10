//! Workflow engine (ADR-007): a pure state machine over a JSON definition. No I/O here; commands load the
//! definition, build a `GuardInput` from the item, and apply the chosen transition.

use cnote_core::Role;
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::HashSet;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Category {
    Todo,
    InProgress,
    Done,
}

impl Category {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Todo => "todo",
            Self::InProgress => "in_progress",
            Self::Done => "done",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
pub struct Status {
    pub name: String,
    pub category: Category,
}

/// `"*"` (any status) or a list of status names.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(untagged)]
pub enum FromSpec {
    Any(String),
    Statuses(Vec<String>),
}

impl FromSpec {
    fn matches(&self, status: &str) -> bool {
        match self {
            Self::Any(_) => true,
            Self::Statuses(v) => v.iter().any(|s| s.eq_ignore_ascii_case(status)),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Guard {
    /// Item fields that must be filled: assignee, estimate, due_date, labels, sprint, fix_version, component,
    /// priority, description, parent, or a custom field key.
    RequiredFields { fields: Vec<String> },
    /// owner | admin | member | guest
    MinRole { role: String },
    /// An evidence record of this kind (pr_opened, pr_merged, deploy_succeeded, ...) must exist on the item.
    Evidence { event: String },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum PostAction {
    SetField {
        field: String,
        value: Value,
    },
    /// Assign the item to its reporter.
    AssignReporter,
    /// Emit an extra event on the item.
    Emit {
        event: String,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
pub struct Transition {
    pub name: String,
    pub from: FromSpec,
    pub to: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub guards: Vec<Guard>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub post_actions: Vec<PostAction>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
pub struct Definition {
    pub statuses: Vec<Status>,
    pub transitions: Vec<Transition>,
    /// Allowed item types; empty = any.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub types: Vec<String>,
}

/// What the guards can see about the item and the caller.
pub struct GuardInput<'a> {
    pub role: Role,
    pub filled: &'a HashSet<String>,
    pub evidence: &'a [String],
}

impl Transition {
    pub fn evidence_events(&self) -> impl Iterator<Item = &str> {
        self.guards.iter().filter_map(|g| match g {
            Guard::Evidence { event } => Some(event.as_str()),
            _ => None,
        })
    }

    pub fn check(&self, inp: &GuardInput) -> Result<(), String> {
        for g in &self.guards {
            match g {
                Guard::RequiredFields { fields } => {
                    let missing: Vec<&str> = fields
                        .iter()
                        .map(String::as_str)
                        .filter(|f| !inp.filled.contains(*f))
                        .collect();
                    if !missing.is_empty() {
                        return Err(format!(
                            "transition '{}' requires fields: {}",
                            self.name,
                            missing.join(", ")
                        ));
                    }
                }
                Guard::MinRole { role } => {
                    if inp.role < Role::parse(role) {
                        return Err(format!(
                            "transition '{}' requires role {role} or higher",
                            self.name
                        ));
                    }
                }
                Guard::Evidence { event } => {
                    if !inp.evidence.iter().any(|e| e == event) {
                        return Err(format!(
                            "transition '{}' requires evidence '{event}' (none recorded)",
                            self.name
                        ));
                    }
                }
            }
        }
        Ok(())
    }
}

impl Definition {
    pub fn template(name: &str) -> Self {
        let v = match name {
            "kanban" => json!({
                "types": ["task", "bug"],
                "statuses": [
                    {"name": "To Do", "category": "todo"},
                    {"name": "Doing", "category": "in_progress"},
                    {"name": "Done", "category": "done"}
                ],
                "transitions": [
                    {"name": "Start", "from": ["To Do"], "to": "Doing"},
                    {"name": "Back", "from": ["Doing"], "to": "To Do"},
                    {"name": "Finish", "from": ["To Do", "Doing"], "to": "Done"},
                    {"name": "PR merged", "from": ["To Do", "Doing"], "to": "Done",
                     "guards": [{"type": "evidence", "event": "pr_merged"}]},
                    {"name": "Reopen", "from": ["Done"], "to": "To Do"}
                ]
            }),
            _ => json!({
                "types": ["epic", "story", "task", "bug", "subtask"],
                "statuses": [
                    {"name": "Backlog", "category": "todo"},
                    {"name": "Todo", "category": "todo"},
                    {"name": "In Progress", "category": "in_progress"},
                    {"name": "In Review", "category": "in_progress"},
                    {"name": "Done", "category": "done"},
                    {"name": "Cancelled", "category": "done"}
                ],
                "transitions": [
                    {"name": "Plan", "from": ["Backlog"], "to": "Todo"},
                    {"name": "Back to Backlog", "from": ["Todo"], "to": "Backlog"},
                    {"name": "Start", "from": ["Backlog", "Todo"], "to": "In Progress"},
                    {"name": "Back to Todo", "from": ["In Progress", "In Review"], "to": "Todo"},
                    {"name": "Request review", "from": ["In Progress"], "to": "In Review"},
                    {"name": "Back to progress", "from": ["In Review"], "to": "In Progress"},
                    {"name": "PR opened", "from": ["Backlog", "Todo", "In Progress"], "to": "In Review",
                     "guards": [{"type": "evidence", "event": "pr_opened"}]},
                    {"name": "Complete", "from": ["Todo", "In Progress", "In Review"], "to": "Done"},
                    {"name": "PR merged", "from": ["Backlog", "Todo", "In Progress", "In Review"], "to": "Done",
                     "guards": [{"type": "evidence", "event": "pr_merged"}]},
                    {"name": "Reopen", "from": ["Done", "Cancelled"], "to": "Todo"},
                    {"name": "Cancel", "from": "*", "to": "Cancelled"}
                ]
            }),
        };
        serde_json::from_value(v).expect("built-in template is valid")
    }

    pub fn validate(&self) -> Result<(), String> {
        if self.statuses.is_empty() {
            return Err("a workflow needs at least one status".into());
        }
        let mut seen = HashSet::new();
        for s in &self.statuses {
            if s.name.trim().is_empty() || !seen.insert(s.name.to_lowercase()) {
                return Err(format!("status '{}' is empty or duplicated", s.name));
            }
        }
        let mut names = HashSet::new();
        for t in &self.transitions {
            if !names.insert(t.name.to_lowercase()) {
                return Err(format!("transition name '{}' is duplicated", t.name));
            }
            if self.status(&t.to).is_none() {
                return Err(format!(
                    "transition '{}' targets unknown status '{}'",
                    t.name, t.to
                ));
            }
            match &t.from {
                FromSpec::Any(s) if s != "*" => {
                    return Err(format!(
                        "transition '{}': from must be \"*\" or a list of statuses",
                        t.name
                    ));
                }
                FromSpec::Statuses(v) => {
                    if let Some(bad) = v.iter().find(|s| self.status(s).is_none()) {
                        return Err(format!(
                            "transition '{}' starts from unknown status '{bad}'",
                            t.name
                        ));
                    }
                }
                _ => {}
            }
            for g in &t.guards {
                match g {
                    Guard::RequiredFields { fields } if fields.is_empty() => {
                        return Err(format!("transition '{}': required_fields is empty", t.name));
                    }
                    Guard::MinRole { role }
                        if !matches!(role.as_str(), "owner" | "admin" | "member" | "guest") =>
                    {
                        return Err(format!("transition '{}': unknown role '{role}'", t.name));
                    }
                    Guard::Evidence { event } if event.trim().is_empty() => {
                        return Err(format!("transition '{}': evidence event is empty", t.name));
                    }
                    _ => {}
                }
            }
            for a in &t.post_actions {
                if let PostAction::Emit { event } = a
                    && event.trim().is_empty()
                {
                    return Err(format!("transition '{}': emit event is empty", t.name));
                }
            }
        }
        Ok(())
    }

    pub fn status(&self, name: &str) -> Option<&Status> {
        self.statuses
            .iter()
            .find(|s| s.name.eq_ignore_ascii_case(name))
    }

    pub fn initial(&self) -> &Status {
        &self.statuses[0]
    }

    /// Transitions out of `from` whose name or target status equals `target` (case-insensitive).
    pub fn candidates(&self, from: &str, target: &str) -> Vec<&Transition> {
        self.transitions
            .iter()
            .filter(|t| {
                t.from.matches(from)
                    && !t.to.eq_ignore_ascii_case(from)
                    && (t.name.eq_ignore_ascii_case(target) || t.to.eq_ignore_ascii_case(target))
            })
            .collect()
    }

    /// Transitions out of `from` that are fired by evidence of `kind`.
    pub fn evidence_driven(&self, from: &str, kind: &str) -> Vec<&Transition> {
        self.transitions
            .iter()
            .filter(|t| {
                t.from.matches(from)
                    && !t.to.eq_ignore_ascii_case(from)
                    && t.evidence_events().any(|e| e == kind)
            })
            .collect()
    }

    pub fn allowed_from(&self, from: &str) -> Vec<&Transition> {
        self.transitions
            .iter()
            .filter(|t| t.from.matches(from) && !t.to.eq_ignore_ascii_case(from))
            .collect()
    }

    /// The first candidate whose guards pass; otherwise the first candidate's failure.
    pub fn pick(&self, from: &str, target: &str, inp: &GuardInput) -> Result<&Transition, String> {
        let cands = self.candidates(from, target);
        if cands.is_empty() {
            let allowed: Vec<&str> = self
                .allowed_from(from)
                .iter()
                .map(|t| t.to.as_str())
                .collect();
            return Err(format!(
                "no transition from '{from}' to '{target}'; allowed targets: {}",
                if allowed.is_empty() {
                    "none".to_string()
                } else {
                    allowed.join(", ")
                }
            ));
        }
        let mut first_err = None;
        for t in cands {
            match t.check(inp) {
                Ok(()) => return Ok(t),
                Err(e) => {
                    first_err.get_or_insert(e);
                }
            }
        }
        Err(first_err.unwrap_or_default())
    }

    /// Human-readable differences from `template` (what `space.drift` reports).
    pub fn diff(&self, template: &Definition) -> Vec<String> {
        let mut out = vec![];
        for s in &self.statuses {
            match template.status(&s.name) {
                None => out.push(format!("status '{}' is not in the template", s.name)),
                Some(t) if t.category != s.category => out.push(format!(
                    "status '{}' has category {} (template: {})",
                    s.name,
                    s.category.as_str(),
                    t.category.as_str()
                )),
                _ => {}
            }
        }
        for t in &template.statuses {
            if self.status(&t.name).is_none() {
                out.push(format!("status '{}' from the template is missing", t.name));
            }
        }
        for t in &self.transitions {
            match template
                .transitions
                .iter()
                .find(|x| x.name.eq_ignore_ascii_case(&t.name))
            {
                None => out.push(format!("transition '{}' is not in the template", t.name)),
                Some(x) if x != t => {
                    out.push(format!("transition '{}' differs from the template", t.name))
                }
                _ => {}
            }
        }
        for x in &template.transitions {
            if !self
                .transitions
                .iter()
                .any(|t| t.name.eq_ignore_ascii_case(&x.name))
            {
                out.push(format!(
                    "transition '{}' from the template is missing",
                    x.name
                ));
            }
        }
        if self.types != template.types {
            out.push(format!(
                "item types are [{}] (template: [{}])",
                self.types.join(", "),
                template.types.join(", ")
            ));
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn inp<'a>(role: Role, filled: &'a HashSet<String>, evidence: &'a [String]) -> GuardInput<'a> {
        GuardInput {
            role,
            filled,
            evidence,
        }
    }

    #[test]
    fn templates_are_valid() {
        for t in ["software", "kanban", "anything-else"] {
            Definition::template(t).validate().unwrap();
        }
        assert_eq!(Definition::template("software").statuses.len(), 6);
        assert_eq!(Definition::template("kanban").initial().name, "To Do");
    }

    #[test]
    fn parses_the_documented_json_shape() {
        let d: Definition = serde_json::from_value(json!({
            "statuses": [{"name": "A", "category": "todo"}, {"name": "B", "category": "done"}],
            "transitions": [{
                "name": "Go", "from": "*", "to": "B",
                "guards": [{"type": "required_fields", "fields": ["assignee"]},
                           {"type": "min_role", "role": "admin"},
                           {"type": "evidence", "event": "pr_merged"}],
                "post_actions": [{"type": "set_field", "field": "priority", "value": "low"},
                                 {"type": "assign_reporter"}, {"type": "emit", "event": "x.y"}]
            }]
        }))
        .unwrap();
        d.validate().unwrap();
        assert_eq!(d.transitions[0].guards.len(), 3);
        assert_eq!(d.transitions[0].post_actions.len(), 3);
    }

    #[test]
    fn validate_rejects_bad_definitions() {
        let mut d = Definition::template("kanban");
        d.transitions[0].to = "Nowhere".into();
        assert!(d.validate().unwrap_err().contains("unknown status"));
        let mut d = Definition::template("kanban");
        d.statuses.push(Status {
            name: "done".into(),
            category: Category::Done,
        });
        assert!(d.validate().unwrap_err().contains("duplicated"));
        let mut d = Definition::template("kanban");
        d.transitions[0].from = FromSpec::Any("all".into());
        assert!(d.validate().is_err());
        let mut d = Definition::template("kanban");
        d.transitions[0].guards = vec![Guard::MinRole {
            role: "boss".into(),
        }];
        assert!(d.validate().unwrap_err().contains("unknown role"));
    }

    #[test]
    fn pick_by_status_or_transition_name() {
        let d = Definition::template("software");
        let f = HashSet::new();
        let i = inp(Role::Member, &f, &[]);
        assert_eq!(d.pick("Backlog", "Todo", &i).unwrap().name, "Plan");
        assert_eq!(d.pick("todo", "in progress", &i).unwrap().name, "Start");
        assert_eq!(d.pick("Todo", "Start", &i).unwrap().to, "In Progress");
        assert_eq!(
            d.pick("In Progress", "Cancelled", &i).unwrap().name,
            "Cancel"
        );
        let e = d.pick("Backlog", "Done", &i).unwrap_err();
        assert!(e.contains("PR merged"), "{e}"); // only the evidence route is open from Backlog
        let e = d.pick("Done", "In Review", &i).unwrap_err();
        assert!(e.contains("allowed targets: Todo, Cancelled"), "{e}");
        assert!(d.pick("Todo", "Todo", &i).is_err(), "no self transition");
    }

    #[test]
    fn guards() {
        let mut d = Definition::template("kanban");
        d.transitions[0].guards = vec![
            Guard::RequiredFields {
                fields: vec!["assignee".into(), "estimate".into()],
            },
            Guard::MinRole {
                role: "admin".into(),
            },
        ];
        let mut f: HashSet<String> = ["assignee".to_string()].into();
        let e = d
            .pick("To Do", "Doing", &inp(Role::Admin, &f, &[]))
            .unwrap_err();
        assert!(e.contains("estimate") && !e.contains("assignee,"), "{e}");
        f.insert("estimate".into());
        let e = d
            .pick("To Do", "Doing", &inp(Role::Member, &f, &[]))
            .unwrap_err();
        assert!(e.contains("requires role admin"), "{e}");
        assert!(d.pick("To Do", "Doing", &inp(Role::Owner, &f, &[])).is_ok());
    }

    #[test]
    fn evidence_guard_and_auto_transitions() {
        let d = Definition::template("software");
        let f = HashSet::new();
        // Manual "Done" works through the plain Complete transition, evidence routes are separate.
        let t = d
            .pick("In Review", "Done", &inp(Role::Member, &f, &[]))
            .unwrap();
        assert_eq!(t.name, "Complete");
        let auto = d.evidence_driven("In Review", "pr_merged");
        assert_eq!(auto.len(), 1);
        assert_eq!(auto[0].to, "Done");
        assert!(auto[0].check(&inp(Role::Member, &f, &[])).is_err());
        assert!(
            auto[0]
                .check(&inp(Role::Member, &f, &["pr_merged".to_string()]))
                .is_ok()
        );
        assert!(d.evidence_driven("Done", "pr_merged").is_empty());
        assert!(d.evidence_driven("Todo", "nothing").is_empty());
    }

    #[test]
    fn diff_reports_drift() {
        let tpl = Definition::template("software");
        assert!(tpl.diff(&tpl).is_empty());
        let mut d = tpl.clone();
        d.statuses.push(Status {
            name: "QA".into(),
            category: Category::InProgress,
        });
        d.statuses.retain(|s| s.name != "Cancelled");
        d.transitions[0].to = "Done".into();
        d.transitions.retain(|t| t.name != "Cancel");
        let diff = d.diff(&tpl);
        assert!(
            diff.iter()
                .any(|x| x.contains("'QA' is not in the template"))
        );
        assert!(
            diff.iter()
                .any(|x| x.contains("'Cancelled' from the template is missing"))
        );
        assert!(diff.iter().any(|x| x.contains("'Plan' differs")));
        assert!(
            diff.iter()
                .any(|x| x.contains("'Cancel' from the template is missing"))
        );
    }
}
