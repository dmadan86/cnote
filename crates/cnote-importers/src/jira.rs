//! Jira Cloud: parse REST v3 issue JSON into records, and fetch it live through a `JiraClient`.

use crate::model::*;
use async_trait::async_trait;
use serde_json::{Value, json};

/// Credentials arrive per call and are never stored or logged; the fetched JSON is what the job keeps.
#[async_trait]
pub trait JiraClient: Send + Sync {
    /// One page of `/rest/api/3/search/jql`. The response carries `issues`, `isLast` and `nextPageToken`.
    async fn search(
        &self,
        base_url: &str,
        email: &str,
        token: &str,
        jql: &str,
        page_token: Option<&str>,
    ) -> anyhow::Result<Value>;
}

pub struct ReqwestJira;

#[async_trait]
impl JiraClient for ReqwestJira {
    async fn search(
        &self,
        base_url: &str,
        email: &str,
        token: &str,
        jql: &str,
        page_token: Option<&str>,
    ) -> anyhow::Result<Value> {
        let mut q = vec![
            ("jql", jql.to_string()),
            ("fields", "*all".into()),
            ("expand", "changelog".into()),
            ("maxResults", "100".into()),
        ];
        if let Some(t) = page_token {
            q.push(("nextPageToken", t.into()));
        }
        let res = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(std::time::Duration::from_secs(30))
            .build()?
            .get(format!(
                "{}/rest/api/3/search/jql",
                base_url.trim_end_matches('/')
            ))
            .basic_auth(email, Some(token))
            .query(&q)
            .send()
            .await?;
        // Status only: response bodies and URLs stay out of errors.
        let status = res.status();
        if !status.is_success() {
            anyhow::bail!("Jira returned HTTP {status}");
        }
        Ok(res.json().await?)
    }
}

/// Jira Cloud only: https, no credentials in the URL, host under atlassian.net. Anything else could point the
/// server at internal addresses.
pub fn validate_base_url(base_url: &str) -> anyhow::Result<()> {
    let u =
        url::Url::parse(base_url).map_err(|_| anyhow::anyhow!("base_url is not a valid URL"))?;
    let host = u.host_str().unwrap_or("").to_lowercase();
    let host = host.trim_end_matches('.');
    if u.scheme() != "https"
        || !u.username().is_empty()
        || u.password().is_some()
        || !host.ends_with(".atlassian.net")
        || host == ".atlassian.net"
    {
        anyhow::bail!("base_url must be an https URL on a *.atlassian.net host");
    }
    Ok(())
}

const MAX_ISSUES: usize = 10_000;

/// Page through the search results. Only a base_url that is a public https host is accepted.
pub async fn fetch_issues(
    client: &dyn JiraClient,
    base_url: &str,
    email: &str,
    token: &str,
    jql: &str,
) -> anyhow::Result<Value> {
    validate_base_url(base_url)?;
    let mut issues: Vec<Value> = vec![];
    let mut token_next: Option<String> = None;
    loop {
        let page = client
            .search(base_url, email, token, jql, token_next.as_deref())
            .await?;
        issues.extend(page["issues"].as_array().cloned().unwrap_or_default());
        token_next = page["nextPageToken"].as_str().map(String::from);
        if token_next.is_none()
            || page["isLast"].as_bool() == Some(true)
            || issues.len() >= MAX_ISSUES
        {
            break;
        }
    }
    issues.truncate(MAX_ISSUES);
    Ok(json!({ "issues": issues }))
}

/// Atlassian Document Format (or a plain string) -> Markdown-ish text.
fn adf(v: &Value, out: &mut String) {
    match v {
        Value::String(s) => out.push_str(s),
        Value::Object(o) => {
            let kind = o.get("type").and_then(Value::as_str).unwrap_or("");
            let kids = |out: &mut String| {
                for k in o
                    .get("content")
                    .and_then(Value::as_array)
                    .into_iter()
                    .flatten()
                {
                    adf(k, out);
                }
            };
            match kind {
                "text" => out.push_str(o.get("text").and_then(Value::as_str).unwrap_or("")),
                "hardBreak" => out.push('\n'),
                "mention" => out.push_str(
                    v.pointer("/attrs/text")
                        .and_then(Value::as_str)
                        .unwrap_or("@user"),
                ),
                "heading" => {
                    let n = v
                        .pointer("/attrs/level")
                        .and_then(Value::as_u64)
                        .unwrap_or(1) as usize;
                    out.push_str(&"#".repeat(n.min(6)));
                    out.push(' ');
                    kids(out);
                    out.push_str("\n\n");
                }
                "listItem" => {
                    let mut s = String::new();
                    kids(&mut s);
                    out.push_str(&format!("- {}\n", s.trim()));
                }
                "bulletList" | "orderedList" => {
                    kids(out);
                    out.push('\n');
                }
                "codeBlock" => {
                    out.push_str("```\n");
                    kids(out);
                    out.push_str("\n```\n\n");
                }
                "doc" => kids(out),
                _ => {
                    kids(out);
                    out.push_str("\n\n");
                }
            }
        }
        _ => {}
    }
}

fn text_of(v: &Value) -> String {
    let mut s = String::new();
    adf(v, &mut s);
    s.trim().to_string()
}

fn str_at(v: &Value, ptr: &str) -> Option<String> {
    v.pointer(ptr)
        .and_then(Value::as_str)
        .map(String::from)
        .filter(|s| !s.is_empty())
}

fn person(v: &Value) -> (Option<String>, Option<String>) {
    (str_at(v, "/displayName"), str_at(v, "/emailAddress"))
}

fn is_sprint(v: &Value) -> bool {
    v.as_array().is_some_and(|a| {
        !a.is_empty()
            && a.iter().all(|s| {
                s.get("name").is_some()
                    && s.get("state").is_some()
                    && (s.get("boardId").is_some() || s.get("startDate").is_some())
            })
    })
}

/// Fields the importer maps; everything else with a value is reported as unknown.
const KNOWN: [&str; 24] = [
    "summary",
    "description",
    "issuetype",
    "status",
    "priority",
    "assignee",
    "reporter",
    "labels",
    "components",
    "fixVersions",
    "parent",
    "issuelinks",
    "comment",
    "attachment",
    "created",
    "updated",
    "duedate",
    "resolutiondate",
    "customfield_10016",
    "customfield_10014",
    "project",
    "creator",
    "resolution",
    "subtasks",
];
/// Always-present system fields with no meaning here; not worth a warning.
const IGNORED: [&str; 14] = [
    "statuscategorychangedate",
    "lastViewed",
    "watches",
    "votes",
    "worklog",
    "timetracking",
    "aggregateprogress",
    "progress",
    "workratio",
    "security",
    "environment",
    "versions",
    "issuerestriction",
    "aggregatetimespent",
];

fn parse_issue(issue: &Value) -> Result<Rec, String> {
    let key = str_at(issue, "/key").ok_or("issue without key")?;
    let f = issue
        .get("fields")
        .ok_or_else(|| format!("{key}: no fields"))?;
    let mut rec = Rec {
        source_id: key.clone(),
        title: str_at(f, "/summary").ok_or_else(|| format!("{key}: no summary"))?,
        body_md: f.get("description").map(text_of).unwrap_or_default(),
        created_at: str_at(f, "/created").and_then(|s| parse_ts(&s)),
        author: str_at(f, "/creator/displayName").or_else(|| str_at(f, "/reporter/displayName")),
        ..Default::default()
    };
    rec.source_url = str_at(issue, "/self").and_then(|s| {
        s.split_once("/rest/")
            .map(|(origin, _)| format!("{origin}/browse/{key}"))
    });

    let mut item = ItemData {
        item_type: str_at(f, "/issuetype/name").map_or("task".into(), |t| crate::model::slug(&t)),
        status: str_at(f, "/status/name").unwrap_or_else(|| "To Do".into()),
        status_category: match str_at(f, "/status/statusCategory/key").as_deref() {
            Some("done") => "done",
            Some("indeterminate") => "in_progress",
            _ => "todo",
        },
        priority: "medium",
        ..Default::default()
    };
    if let Some(p) = str_at(f, "/priority/name") {
        match map_priority(&p) {
            Some(m) => item.priority = m,
            None => rec
                .warnings
                .push(format!("unknown priority '{p}' mapped to medium")),
        }
    }
    let (aname, aemail) = f.get("assignee").map(person).unwrap_or_default();
    item.assignee_email = aemail;
    if let Some(n) = aname {
        item.custom.insert("source_assignee".into(), json!(n));
    }
    let (rname, remail) = f.get("reporter").map(person).unwrap_or_default();
    item.reporter_email = remail;
    if let Some(n) = rname {
        item.custom.insert("source_reporter".into(), json!(n));
    }
    item.labels = f["labels"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|l| l.as_str().map(String::from))
        .collect();
    let comps: Vec<&str> = f["components"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|c| c["name"].as_str())
        .collect();
    item.component = comps.first().map(|s| s.to_string());
    if comps.len() > 1 {
        rec.warnings
            .push("only the first component is kept per item".into());
    }
    let fixes = f["fixVersions"].as_array().cloned().unwrap_or_default();
    item.fix_version = fixes.first().and_then(|v| {
        Some(Version {
            name: v["name"].as_str()?.to_string(),
            released: v["released"].as_bool().unwrap_or(false),
            date: v["releaseDate"].as_str().and_then(parse_date),
        })
    });
    if fixes.len() > 1 {
        rec.warnings
            .push("only the first fix version is kept per item".into());
    }
    item.estimate = f["customfield_10016"].as_f64();
    item.due_date = str_at(f, "/duedate").and_then(|d| parse_date(&d));
    if item.status_category == "done" {
        item.resolved_at = str_at(f, "/resolutiondate")
            .or_else(|| str_at(f, "/updated"))
            .and_then(|s| parse_ts(&s));
    }
    if let Some(obj) = f.as_object() {
        for (k, v) in obj {
            if v.is_null() || KNOWN.contains(&k.as_str()) || IGNORED.contains(&k.as_str()) {
                continue;
            }
            if k.starts_with("customfield_") && is_sprint(v) {
                let sprints = v.as_array().unwrap();
                // The last entry is the sprint the issue sits in now.
                let s = sprints.last().unwrap();
                item.sprint = Some(Sprint {
                    name: s["name"].as_str().unwrap_or("Sprint").to_string(),
                    state: match s["state"].as_str() {
                        Some("active") => "active",
                        Some("closed") => "closed",
                        _ => "future",
                    },
                    start: s["startDate"].as_str().and_then(parse_date),
                    end: s["endDate"].as_str().and_then(parse_date),
                });
                if sprints.len() > 1 {
                    rec.warnings
                        .push("sprint history flattened to the latest sprint".into());
                }
            } else if v.as_array().is_some_and(|a| a.is_empty()) {
                // empty list: nothing to lose
            } else {
                rec.warnings
                    .push(format!("unknown field '{k}' not imported"));
            }
        }
    }
    rec.item = Some(item);

    rec.parent = str_at(f, "/parent/key").or_else(|| str_at(f, "/customfield_10014"));
    for l in f["issuelinks"].as_array().into_iter().flatten() {
        let blocks = l["type"]["name"]
            .as_str()
            .is_some_and(|n| n.to_lowercase().contains("block"));
        let kind = if blocks { "blocks" } else { "relates" };
        if let Some(k) = str_at(l, "/outwardIssue/key") {
            rec.links.push(Link {
                kind,
                other: k,
                outward: true,
            });
        } else if let Some(k) = str_at(l, "/inwardIssue/key") {
            rec.links.push(Link {
                kind,
                other: k,
                outward: false,
            });
        }
    }

    for h in issue["changelog"]["histories"]
        .as_array()
        .into_iter()
        .flatten()
    {
        let hid = h["id"].as_str().unwrap_or("0");
        for (i, it) in h["items"].as_array().into_iter().flatten().enumerate() {
            rec.history.push(Hist {
                id: format!("{hid}:{i}"),
                at: str_at(h, "/created").and_then(|s| parse_ts(&s)),
                author: str_at(h, "/author/displayName"),
                field: it["field"].as_str().unwrap_or("").to_string(),
                from: str_at(it, "/fromString"),
                to: str_at(it, "/toString"),
            });
        }
    }
    for c in f["comment"]["comments"].as_array().into_iter().flatten() {
        let (name, email) = person(&c["author"]);
        rec.comments.push(Comment {
            id: c["id"].as_str().unwrap_or("").to_string(),
            author: name,
            author_email: email,
            at: str_at(c, "/created").and_then(|s| parse_ts(&s)),
            body: text_of(&c["body"]),
        });
    }
    for a in f["attachment"].as_array().into_iter().flatten() {
        if let Some(url) = str_at(a, "/content") {
            rec.attachments.push(Attach {
                filename: str_at(a, "/filename").unwrap_or_else(|| "attachment".into()),
                content_type: str_at(a, "/mimeType")
                    .unwrap_or_else(|| "application/octet-stream".into()),
                size: a["size"].as_i64().unwrap_or(0),
                url,
            });
        }
    }
    Ok(rec)
}

/// Saved filters need JQL that C Note's query language can run; flag anything beyond simple field clauses.
pub fn jql_supported(jql: &str) -> bool {
    let call = regex::Regex::new(r"(?i)(\w+)\s*\(").unwrap();
    let history = regex::Regex::new(r"(?i)\b(was|changed|during)\b|~|cf\[").unwrap();
    let only_in_lists = call
        .captures_iter(jql)
        .all(|c| matches!(c[1].to_lowercase().as_str(), "in" | "and" | "or" | "not"));
    only_in_lists && !history.is_match(jql)
}

pub fn parse(content: &str) -> Result<Parsed, String> {
    let v: Value = serde_json::from_str(content)
        .map_err(|e| format!("Jira content is not valid JSON: {e}"))?;
    let issues = if v.is_array() {
        v.clone()
    } else {
        v["issues"].clone()
    };
    let issues = issues.as_array().ok_or("expected an `issues` array")?;
    let mut p = Parsed {
        warnings: vec![
            "Jira workflow, permission and notification schemes are flattened: each issue keeps its status name and category only".into(),
        ],
        ..Default::default()
    };
    for i in issues {
        match parse_issue(i) {
            Ok(r) => p.recs.push(r),
            Err(e) => p.warnings.push(format!("skipped: {e}")),
        }
    }
    for f in v["filters"].as_array().into_iter().flatten() {
        if let (Some(name), Some(jql)) = (str_at(f, "/name"), str_at(f, "/jql")) {
            p.filters.push(Filter {
                id: str_at(f, "/id").unwrap_or_else(|| slug(&name)),
                name,
                jql,
            });
        }
    }
    for a in v["automation_rules"].as_array().into_iter().flatten() {
        if let Some(name) = str_at(a, "/name") {
            p.automations.push(Automation {
                name,
                trigger: a.get("trigger").cloned().unwrap_or(Value::Null),
                conditions: a.get("conditions").cloned().unwrap_or(json!([])),
                actions: a.get("actions").cloned().unwrap_or(json!([])),
            });
        }
    }
    Ok(p)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn jql_check_is_conservative() {
        assert!(jql_supported(
            "project = ENG AND status in (Open, \"In Progress\") ORDER BY created"
        ));
        assert!(!jql_supported("assignee = currentUser()"));
        assert!(!jql_supported("sprint in openSprints()"));
        assert!(!jql_supported("status was \"Done\" during (a, b)"));
        assert!(!jql_supported("text ~ \"login\""));
    }

    #[test]
    fn adf_to_text() {
        let d = json!({"type":"doc","content":[
            {"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Steps"}]},
            {"type":"paragraph","content":[{"type":"text","text":"Do it"}]},
            {"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]}]}]});
        assert_eq!(text_of(&d), "## Steps\n\nDo it\n\n- a");
    }

    #[tokio::test]
    async fn fetch_rejects_non_public_hosts_and_pages() {
        struct Fake;
        #[async_trait]
        impl JiraClient for Fake {
            async fn search(
                &self,
                _: &str,
                _: &str,
                _: &str,
                _: &str,
                t: Option<&str>,
            ) -> anyhow::Result<Value> {
                Ok(match t {
                    None => json!({"issues":[{"key":"A-1"}],"nextPageToken":"p2"}),
                    _ => json!({"issues":[{"key":"A-2"}],"isLast":true}),
                })
            }
        }
        assert!(
            fetch_issues(&Fake, "http://x.atlassian.net", "a@example.com", "t", "x")
                .await
                .is_err()
        );
        assert!(
            fetch_issues(&Fake, "https://localhost", "a@example.com", "t", "x")
                .await
                .is_err()
        );
        assert!(
            fetch_issues(&Fake, "https://10.0.0.5", "a@example.com", "t", "x")
                .await
                .is_err()
        );
        let v = fetch_issues(
            &Fake,
            "https://acme.atlassian.net",
            "a@example.com",
            "t",
            "x",
        )
        .await
        .unwrap();
        assert_eq!(v["issues"].as_array().unwrap().len(), 2);
    }

    #[test]
    fn base_url_must_be_jira_cloud() {
        for bad in [
            "http://acme.atlassian.net",
            "https://localhost",
            "https://169.254.169.254",
            "https://evil.com",
            "https://user:pw@x.atlassian.net",
            "https://x.atlassian.net.evil.com",
            "https://.atlassian.net",
        ] {
            assert!(validate_base_url(bad).is_err(), "{bad}");
        }
        assert!(validate_base_url("https://acme.atlassian.net").is_ok());
        assert!(validate_base_url("https://ACME.atlassian.net./").is_ok());
    }
}
