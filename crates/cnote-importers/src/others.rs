//! Trello boards, Notion/Confluence pages and CSV files -> records.

use crate::html::{first_text, html_to_md};
use crate::model::*;
use serde::Deserialize;
use serde_json::{Value, json};
use std::collections::HashMap;

fn s(v: &Value, k: &str) -> Option<String> {
    v.get(k)
        .and_then(Value::as_str)
        .map(String::from)
        .filter(|x| !x.is_empty())
}

// ---------- Trello ----------

pub fn trello(content: &str) -> Result<Parsed, String> {
    let b: Value = serde_json::from_str(content)
        .map_err(|e| format!("Trello content is not valid JSON: {e}"))?;
    let lists: HashMap<String, String> = b["lists"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|l| Some((s(l, "id")?, s(l, "name")?)))
        .collect();
    let mut p = Parsed::default();
    p.warnings
        .push("Trello members have no email in a board export: assignees are not matched".into());
    let mut checklists: HashMap<String, Vec<String>> = HashMap::new();
    for c in b["checklists"].as_array().into_iter().flatten() {
        let mut md = format!(
            "### {}\n",
            s(c, "name").unwrap_or_else(|| "Checklist".into())
        );
        for it in c["checkItems"].as_array().into_iter().flatten() {
            let done = it["state"].as_str() == Some("complete");
            md.push_str(&format!(
                "- [{}] {}\n",
                if done { "x" } else { " " },
                s(it, "name").unwrap_or_default()
            ));
        }
        if let Some(card) = s(c, "idCard") {
            checklists.entry(card).or_default().push(md);
        }
    }
    let mut comments: HashMap<String, Vec<Comment>> = HashMap::new();
    let mut moves: HashMap<String, Vec<Hist>> = HashMap::new();
    for a in b["actions"].as_array().into_iter().flatten() {
        let Some(card) = a.pointer("/data/card/id").and_then(Value::as_str) else {
            continue;
        };
        let at = s(a, "date").and_then(|d| parse_ts(&d));
        let author = a
            .pointer("/memberCreator/fullName")
            .and_then(Value::as_str)
            .map(String::from);
        let id = s(a, "id").unwrap_or_default();
        match a["type"].as_str() {
            Some("commentCard") => comments.entry(card.into()).or_default().push(Comment {
                id,
                author,
                author_email: None,
                at,
                body: a
                    .pointer("/data/text")
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .to_string(),
            }),
            Some("updateCard") if a.pointer("/data/listAfter/name").is_some() => {
                moves.entry(card.into()).or_default().push(Hist {
                    id,
                    at,
                    author,
                    field: "status".into(),
                    from: a
                        .pointer("/data/listBefore/name")
                        .and_then(Value::as_str)
                        .map(String::from),
                    to: a
                        .pointer("/data/listAfter/name")
                        .and_then(Value::as_str)
                        .map(String::from),
                })
            }
            _ => {}
        }
    }
    for c in b["cards"].as_array().into_iter().flatten() {
        let (Some(id), Some(name)) = (s(c, "id"), s(c, "name")) else {
            p.warnings.push("skipped: card without id or name".into());
            continue;
        };
        if c["closed"].as_bool() == Some(true) {
            p.warnings.push("archived cards are skipped".into());
            continue;
        }
        let status = s(c, "idList")
            .and_then(|l| lists.get(&l).cloned())
            .unwrap_or_else(|| "To Do".into());
        let mut body = s(c, "desc").unwrap_or_default();
        for cl in checklists.get(&id).into_iter().flatten() {
            body.push_str(&format!("\n\n{cl}"));
        }
        let mut item = ItemData {
            item_type: "task".into(),
            status_category: category_from_name(&status),
            status,
            priority: "medium",
            labels: c["labels"]
                .as_array()
                .into_iter()
                .flatten()
                .filter_map(|l| s(l, "name"))
                .collect(),
            due_date: s(c, "due").and_then(|d| parse_date(&d)),
            ..Default::default()
        };
        if item.status_category == "done" {
            item.resolved_at = s(c, "dateLastActivity").and_then(|d| parse_ts(&d));
        }
        p.recs.push(Rec {
            source_url: s(c, "url").or_else(|| s(c, "shortUrl")),
            title: name,
            body_md: body.trim().to_string(),
            item: Some(item),
            history: moves.remove(&id).unwrap_or_default(),
            comments: comments.remove(&id).unwrap_or_default(),
            source_id: id,
            ..Default::default()
        });
    }
    Ok(p)
}

// ---------- Notion / Confluence ----------

pub fn pages(source: &str, content: &str) -> Result<Parsed, String> {
    let mut p = Parsed::default();
    let t = content.trim_start();
    let pages: Vec<Value> = if t.starts_with('{') || t.starts_with('[') {
        let v: Value = serde_json::from_str(content)
            .map_err(|e| format!("pages content is not valid JSON: {e}"))?;
        let arr = if v.is_array() { v } else { v["pages"].clone() };
        arr.as_array().cloned().ok_or("expected a `pages` array")?
    } else if t.starts_with('<') {
        let title = first_text(t, "title")
            .or_else(|| first_text(t, "h1"))
            .unwrap_or_else(|| "Untitled".into());
        vec![json!({"id": slug(&title), "title": title, "html": t})]
    } else {
        let title = t
            .lines()
            .find_map(|l| l.strip_prefix("# "))
            .unwrap_or("Untitled")
            .trim()
            .to_string();
        vec![json!({"id": slug(&title), "title": title, "markdown": t})]
    };
    for pg in &pages {
        let (Some(id), Some(title)) = (s(pg, "id"), s(pg, "title")) else {
            p.warnings.push("skipped: page without id or title".into());
            continue;
        };
        let (mut body, mut warnings) = (String::new(), vec![]);
        if let Some(md) = s(pg, "markdown") {
            body = md;
            // The H1 duplicates the note title.
            if let Some(rest) = body.strip_prefix(&format!("# {title}")) {
                body = rest.trim_start().to_string();
            }
        } else if let Some(h) = s(pg, "html").or_else(|| s(pg, "body")) {
            let (md, dropped) = html_to_md(&h);
            body = md;
            if dropped > 0 {
                warnings.push(format!(
                    "{dropped} unsupported {source} macro(s) dropped, their text kept"
                ));
            }
        }
        p.recs.push(Rec {
            source_url: s(pg, "url"),
            created_at: s(pg, "created").and_then(|d| parse_ts(&d)),
            author: s(pg, "author"),
            parent: s(pg, "parent_id"),
            comments: pg["comments"]
                .as_array()
                .into_iter()
                .flatten()
                .enumerate()
                .map(|(i, c)| Comment {
                    id: s(c, "id").unwrap_or_else(|| format!("c{i}")),
                    author: s(c, "author"),
                    author_email: None,
                    at: s(c, "created").and_then(|d| parse_ts(&d)),
                    body: s(c, "body").unwrap_or_default(),
                })
                .collect(),
            warnings,
            body_md: body,
            title,
            source_id: id,
            ..Default::default()
        });
    }
    Ok(p)
}

// ---------- CSV ----------

#[derive(Debug, Clone, Default, Deserialize, schemars::JsonSchema, serde::Serialize)]
pub struct CsvMapping {
    /// "items" (default) or "pages".
    pub target: Option<String>,
    /// Column that identifies a row across re-imports. Without it the title is the identity.
    pub id: Option<String>,
    /// Column holding the title (required).
    pub title: String,
    pub description: Option<String>,
    pub status: Option<String>,
    pub priority: Option<String>,
    /// Column with the item type (task, bug, ...); defaults to task.
    pub item_type: Option<String>,
    pub assignee_email: Option<String>,
    /// Comma-separated labels.
    pub labels: Option<String>,
    /// Column holding the id of the parent row.
    pub parent: Option<String>,
    pub url: Option<String>,
}

pub fn csv(content: &str, m: &CsvMapping) -> Result<Parsed, String> {
    let mut rdr = ::csv::ReaderBuilder::new()
        .flexible(true)
        .from_reader(content.as_bytes());
    let headers: Vec<String> = rdr
        .headers()
        .map_err(|e| format!("CSV header: {e}"))?
        .iter()
        .map(String::from)
        .collect();
    let col = |name: &Option<String>| -> Result<Option<usize>, String> {
        match name {
            None => Ok(None),
            Some(n) => headers
                .iter()
                .position(|h| h == n)
                .map(Some)
                .ok_or_else(|| format!("mapped column '{n}' is not in the CSV header")),
        }
    };
    let title_col = col(&Some(m.title.clone()))?.unwrap();
    let (id_c, desc_c, status_c, prio_c, type_c, asg_c, lab_c, par_c, url_c) = (
        col(&m.id)?,
        col(&m.description)?,
        col(&m.status)?,
        col(&m.priority)?,
        col(&m.item_type)?,
        col(&m.assignee_email)?,
        col(&m.labels)?,
        col(&m.parent)?,
        col(&m.url)?,
    );
    let mapped: Vec<usize> = [
        Some(title_col),
        id_c,
        desc_c,
        status_c,
        prio_c,
        type_c,
        asg_c,
        lab_c,
        par_c,
        url_c,
    ]
    .into_iter()
    .flatten()
    .collect();
    let pages = match m.target.as_deref() {
        None | Some("items") => false,
        Some("pages") => true,
        Some(o) => {
            return Err(format!(
                "mapping.target must be 'items' or 'pages', got '{o}'"
            ));
        }
    };
    let mut p = Parsed::default();
    if id_c.is_none() {
        p.warnings.push(
            "no id column mapped: titles identify rows, so renamed rows import as new".into(),
        );
    }
    let unmapped: Vec<&String> = headers
        .iter()
        .enumerate()
        .filter(|(i, _)| !mapped.contains(i))
        .map(|(_, h)| h)
        .collect();
    if !unmapped.is_empty() && !pages {
        p.warnings.push(format!(
            "unmapped columns stored under custom: {}",
            unmapped
                .iter()
                .map(|s| s.as_str())
                .collect::<Vec<_>>()
                .join(", ")
        ));
    }
    for (n, row) in rdr.records().enumerate() {
        let row = row.map_err(|e| format!("CSV row {}: {e}", n + 2))?;
        let get = |c: Option<usize>| {
            c.and_then(|i| row.get(i))
                .map(str::trim)
                .filter(|v| !v.is_empty())
                .map(String::from)
        };
        let Some(title) = get(Some(title_col)) else {
            p.warnings.push("skipped: row without a title".into());
            continue;
        };
        let mut rec = Rec {
            source_id: get(id_c).unwrap_or_else(|| title.clone()),
            source_url: get(url_c),
            body_md: get(desc_c).unwrap_or_default(),
            parent: get(par_c),
            title,
            ..Default::default()
        };
        if !pages {
            let status = get(status_c).unwrap_or_else(|| "To Do".into());
            let mut item = ItemData {
                item_type: get(type_c).map_or("task".into(), |t| slug(&t)),
                status_category: category_from_name(&status),
                status,
                priority: "medium",
                assignee_email: get(asg_c),
                labels: get(lab_c)
                    .map(|l| {
                        l.split(',')
                            .map(|x| x.trim().to_string())
                            .filter(|x| !x.is_empty())
                            .collect()
                    })
                    .unwrap_or_default(),
                ..Default::default()
            };
            if let Some(pr) = get(prio_c) {
                match map_priority(&pr) {
                    Some(x) => item.priority = x,
                    None => rec
                        .warnings
                        .push(format!("unknown priority '{pr}' mapped to medium")),
                }
            }
            for (i, h) in headers.iter().enumerate() {
                if !mapped.contains(&i)
                    && let Some(v) = get(Some(i))
                {
                    item.custom.insert(h.clone(), json!(v));
                }
            }
            rec.item = Some(item);
        }
        p.recs.push(rec);
    }
    Ok(p)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn csv_requires_mapped_columns() {
        let m = CsvMapping {
            title: "Name".into(),
            ..Default::default()
        };
        assert!(csv("a,b\n1,2\n", &m).is_err());
        let p = csv("Name,Extra\nFix login,x\n,y\n", &m).unwrap();
        assert_eq!(p.recs.len(), 1);
        assert_eq!(p.recs[0].item.as_ref().unwrap().custom["Extra"], json!("x"));
    }

    #[test]
    fn single_markdown_page_takes_title_from_h1() {
        let p = pages("notion", "# Onboarding\n\nWelcome").unwrap();
        assert_eq!(p.recs[0].title, "Onboarding");
        assert_eq!(p.recs[0].body_md, "Welcome");
    }
}
