//! Source-neutral record that every parser produces and the runner upserts.

use chrono::{DateTime, NaiveDate, Utc};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(Debug, Clone, Default)]
pub struct Rec {
    pub source_id: String,
    pub source_url: Option<String>,
    pub title: String,
    pub body_md: String,
    pub created_at: Option<DateTime<Utc>>,
    pub author: Option<String>,
    /// Some = an item in projects.items; None = a page.
    pub item: Option<ItemData>,
    /// source_id of the parent record (epic, parent page).
    pub parent: Option<String>,
    pub links: Vec<Link>,
    pub history: Vec<Hist>,
    pub comments: Vec<Comment>,
    pub attachments: Vec<Attach>,
    pub warnings: Vec<String>,
}

#[derive(Debug, Clone, Default)]
pub struct ItemData {
    pub item_type: String,
    pub status: String,
    pub status_category: &'static str,
    pub priority: &'static str,
    pub assignee_email: Option<String>,
    pub reporter_email: Option<String>,
    pub labels: Vec<String>,
    pub component: Option<String>,
    pub fix_version: Option<Version>,
    pub sprint: Option<Sprint>,
    pub estimate: Option<f64>,
    pub due_date: Option<NaiveDate>,
    pub resolved_at: Option<DateTime<Utc>>,
    /// Anything without a column (source assignee name, unmapped CSV columns).
    pub custom: serde_json::Map<String, serde_json::Value>,
}

#[derive(Debug, Clone)]
pub struct Version {
    pub name: String,
    pub released: bool,
    pub date: Option<NaiveDate>,
}

#[derive(Debug, Clone)]
pub struct Sprint {
    pub name: String,
    pub state: &'static str,
    pub start: Option<NaiveDate>,
    pub end: Option<NaiveDate>,
}

#[derive(Debug, Clone)]
pub struct Link {
    /// blocks | relates
    pub kind: &'static str,
    /// source_id of the other record.
    pub other: String,
    /// true: this record is the `from` side.
    pub outward: bool,
}

#[derive(Debug, Clone)]
pub struct Hist {
    pub id: String,
    pub at: Option<DateTime<Utc>>,
    pub author: Option<String>,
    pub field: String,
    pub from: Option<String>,
    pub to: Option<String>,
}

#[derive(Debug, Clone)]
pub struct Comment {
    pub id: String,
    pub author: Option<String>,
    pub author_email: Option<String>,
    pub at: Option<DateTime<Utc>>,
    pub body: String,
}

#[derive(Debug, Clone)]
pub struct Attach {
    pub filename: String,
    pub content_type: String,
    pub size: i64,
    pub url: String,
}

#[derive(Debug, Clone)]
pub struct Filter {
    pub id: String,
    pub name: String,
    pub jql: String,
}

#[derive(Debug, Clone)]
pub struct Automation {
    pub name: String,
    pub trigger: serde_json::Value,
    pub conditions: serde_json::Value,
    pub actions: serde_json::Value,
}

#[derive(Debug, Default)]
pub struct Parsed {
    pub recs: Vec<Rec>,
    /// Import-wide warnings, added once.
    pub warnings: Vec<String>,
    pub filters: Vec<Filter>,
    pub automations: Vec<Automation>,
}

/// The fidelity report (ADR-012): what arrived, what changed, what could not be carried over.
#[derive(Debug, Default, Serialize, Deserialize)]
pub struct Report {
    pub imported: u32,
    pub updated: u32,
    pub skipped: u32,
    pub comments: u32,
    pub history_events: u32,
    pub attachments: u32,
    pub relations: u32,
    pub views: u32,
    pub automations: u32,
    /// message -> number of occurrences
    pub warnings: BTreeMap<String, u32>,
}

impl Report {
    pub fn warn(&mut self, msg: impl Into<String>) {
        *self.warnings.entry(msg.into()).or_default() += 1;
    }
}

pub fn parse_ts(s: &str) -> Option<DateTime<Utc>> {
    for f in ["%Y-%m-%dT%H:%M:%S%.f%z", "%Y-%m-%dT%H:%M:%S%.f%:z"] {
        if let Ok(d) = DateTime::parse_from_str(s, f) {
            return Some(d.with_timezone(&Utc));
        }
    }
    DateTime::parse_from_rfc3339(s)
        .ok()
        .map(|d| d.with_timezone(&Utc))
}

pub fn parse_date(s: &str) -> Option<NaiveDate> {
    NaiveDate::parse_from_str(s.get(..10)?, "%Y-%m-%d").ok()
}

/// Status name -> category, for sources that only have list/column names.
pub fn category_from_name(name: &str) -> &'static str {
    let n = name.to_lowercase();
    if [
        "done", "complete", "closed", "resolved", "shipped", "released",
    ]
    .iter()
    .any(|w| n.contains(w))
    {
        "done"
    } else if [
        "progress", "doing", "review", "testing", "active", "started",
    ]
    .iter()
    .any(|w| n.contains(w))
    {
        "in_progress"
    } else {
        "todo"
    }
}

pub fn map_priority(name: &str) -> Option<&'static str> {
    Some(match name.trim().to_lowercase().as_str() {
        "highest" | "blocker" | "critical" | "urgent" | "p0" => "urgent",
        "high" | "major" | "p1" => "high",
        "medium" | "normal" | "p2" => "medium",
        "low" | "lowest" | "minor" | "trivial" | "p3" => "low",
        _ => return None,
    })
}

pub fn slug(s: &str) -> String {
    let mut out = String::new();
    for c in s.to_lowercase().chars() {
        if c.is_alphanumeric() {
            out.push(c);
        } else if !out.ends_with('-') {
            out.push('-');
        }
    }
    out.trim_matches('-').to_string()
}
