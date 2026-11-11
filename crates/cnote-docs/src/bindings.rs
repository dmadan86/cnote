//! Bindings (ADR-013): what a page claims to describe. Pure parser plus the handler that keeps `binds` relations in sync.

use cnote_core::events::Event;
use cnote_core::{CmdResult, Ctx};
use regex::Regex;
use serde_json::json;
use std::collections::{HashMap, HashSet};
use std::sync::OnceLock;
use uuid::Uuid;

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub enum Binding {
    ItemKey(String),
    NoteLink(Uuid),
    WikiLink(String),
    Github(String),
    Repo(String),
    Url(String),
}

fn re(cell: &'static OnceLock<Regex>, pat: &str) -> &'static Regex {
    cell.get_or_init(|| Regex::new(pat).expect("static regex"))
}

/// Blank out every match of `r` in `rest`, collecting (position, binding) for those `f` accepts.
fn take(
    r: &Regex,
    rest: &mut String,
    found: &mut Vec<(usize, Binding)>,
    f: impl Fn(&regex::Captures) -> Option<Binding>,
) {
    let mut out = String::with_capacity(rest.len());
    let mut last = 0;
    for c in r.captures_iter(rest) {
        let m = c.get(0).unwrap();
        out.push_str(&rest[last..m.start()]);
        out.extend(std::iter::repeat_n(' ', m.len()));
        last = m.end();
        if let Some(b) = f(&c) {
            found.push((m.start(), b));
        }
    }
    out.push_str(&rest[last..]);
    *rest = out;
}

/// Extract bindings in document order, deduplicated. Stricter constructs are consumed first so that
/// an item key inside a URL is part of the URL, not a separate binding.
pub fn parse(md: &str) -> Vec<Binding> {
    static NOTE: OnceLock<Regex> = OnceLock::new();
    static URL: OnceLock<Regex> = OnceLock::new();
    static REPO: OnceLock<Regex> = OnceLock::new();
    static WIKI: OnceLock<Regex> = OnceLock::new();
    static KEY: OnceLock<Regex> = OnceLock::new();
    let mut found = vec![];
    let mut rest = md.to_string();
    take(
        re(
            &NOTE,
            r"cnote://[^\s)>\]]*?/note/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})",
        ),
        &mut rest,
        &mut found,
        |c| c[1].parse().ok().map(Binding::NoteLink),
    );
    take(
        re(&URL, r"https?://[^\s)>\]]+"),
        &mut rest,
        &mut found,
        |c| {
            let u = c[0].trim_end_matches(['.', ',', ';', ':', '!', '?', '"', '\'']);
            let host = u.split('/').nth(2).unwrap_or("").to_lowercase();
            Some(if host == "github.com" || host == "www.github.com" {
                Binding::Github(u.into())
            } else {
                Binding::Url(u.into())
            })
        },
    );
    take(
        re(
            &REPO,
            r"\brepo:([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[^\s)>\],;]*)?)",
        ),
        &mut rest,
        &mut found,
        |c| Some(Binding::Repo(c[1].trim_end_matches('.').into())),
    );
    take(
        re(&WIKI, r"\[\[([^\[\]|]+)(?:\|[^\]]*)?\]\]"),
        &mut rest,
        &mut found,
        |c| (!c[1].trim().is_empty()).then(|| Binding::WikiLink(c[1].trim().into())),
    );
    take(
        re(&KEY, r"\b[A-Z][A-Z0-9]{1,9}-[1-9][0-9]*\b"),
        &mut rest,
        &mut found,
        |c| Some(Binding::ItemKey(c[0].into())),
    );
    found.sort_by_key(|(p, _)| *p);
    let mut seen = HashSet::new();
    found
        .into_iter()
        .map(|(_, b)| b)
        .filter(|b| seen.insert(b.clone()))
        .collect()
}

/// note.created / note.updated on a page: rewrite its `binds` relations and external bindings from the Markdown.
pub async fn sync_handler(ctx: &mut Ctx, ev: &Event) -> CmdResult<()> {
    let changed = ev.payload["changed"]
        .as_array()
        .is_some_and(|a| a.iter().any(|c| c == "content"));
    if !(ev.kind == "note.created" || (ev.kind == "note.updated" && changed)) {
        return Ok(());
    }
    let Some(page) = ev.note_id else {
        return Ok(());
    };
    let row: Option<(String, bool)> = sqlx::query_as(
        "select content_md, cardinality(types) = 0 from core.notes where id = $1 and deleted_at is null",
    )
    .bind(page)
    .fetch_optional(&mut *ctx.tx)
    .await?;
    match row {
        Some((md, true)) => sync(ctx, page, &md).await,
        _ => Ok(()),
    }
}

pub async fn sync(ctx: &mut Ctx, page: Uuid, md: &str) -> CmdResult<()> {
    let (mut keys, mut titles, mut ids) = (vec![], vec![], vec![]);
    let mut external: Vec<(&str, String)> = vec![];
    for b in parse(md) {
        match b {
            Binding::ItemKey(k) => keys.push(k),
            Binding::WikiLink(t) => titles.push(t.to_lowercase()),
            Binding::NoteLink(id) => ids.push(id),
            Binding::Github(u) => external.push(("github", u)),
            Binding::Repo(r) => external.push(("repo", r)),
            Binding::Url(u) => external.push(("url", u)),
        }
    }
    // Only bind to notes the author can read; otherwise a mention could pull private content into
    // freshness proposals. No known author (system edit) means no note bindings.
    let author = ctx.principal.user_id;
    let readable = match author {
        Some(u) => readable_space_ids_for(ctx, u).await?,
        None => vec![],
    };
    let admin = match author {
        Some(u) => is_admin_user(ctx, u).await?,
        None => false,
    };
    // target note id -> the reference text that produced it (first wins)
    let mut targets: HashMap<Uuid, String> = HashMap::new();
    let by_key: Vec<(Uuid, String)> = sqlx::query_as(
        "select i.note_id, i.key from projects.items i join core.notes n on n.id = i.note_id
         where i.key = any($1) and n.deleted_at is null and n.space_id = any($2)
           and core.note_access(n.id, $3, $4, $2) <> 'none'",
    )
    .bind(&keys)
    .bind(&readable)
    .bind(author)
    .bind(admin)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let by_title: Vec<(Uuid, String)> = sqlx::query_as(
        "select id, '[[' || title || ']]' from core.notes
         where lower(title) = any($1) and deleted_at is null and space_id = any($2)
           and core.note_access(id, $3, $4, $2) <> 'none'",
    )
    .bind(&titles)
    .bind(&readable)
    .bind(author)
    .bind(admin)
    .fetch_all(&mut *ctx.tx)
    .await?;
    let by_id: Vec<(Uuid,)> = sqlx::query_as(
        "select id from core.notes where id = any($1) and deleted_at is null and space_id = any($2)
           and core.note_access(id, $3, $4, $2) <> 'none'",
    )
    .bind(&ids)
    .bind(&readable)
    .bind(author)
    .bind(admin)
    .fetch_all(&mut *ctx.tx)
    .await?;
    for (id, r) in by_key.into_iter().chain(by_title) {
        targets.entry(id).or_insert(r);
    }
    for (id,) in by_id {
        targets
            .entry(id)
            .or_insert_with(|| format!("cnote://note/{id}"));
    }
    targets.remove(&page);

    sqlx::query("delete from core.relations where from_id = $1 and kind = 'binds'")
        .bind(page)
        .execute(&mut *ctx.tx)
        .await?;
    for (to, r) in &targets {
        sqlx::query("insert into core.relations (workspace_id, from_id, to_id, kind, external_ref) values ($1,$2,$3,'binds',$4)")
            .bind(ctx.workspace_id())
            .bind(page)
            .bind(to)
            .bind(r)
            .execute(&mut *ctx.tx)
            .await?;
    }
    sqlx::query("delete from docs.external_bindings where note_id = $1")
        .bind(page)
        .execute(&mut *ctx.tx)
        .await?;
    for (kind, r) in external {
        sqlx::query("insert into docs.external_bindings (workspace_id, note_id, kind, ref) values ($1,$2,$3,$4) on conflict do nothing")
            .bind(ctx.workspace_id())
            .bind(page)
            .bind(kind)
            .bind(r)
            .execute(&mut *ctx.tx)
            .await?;
    }
    ctx.emit(
        "doc.bindings_synced",
        Some(page),
        json!({"notes": targets.len()}),
    )
    .await?;
    Ok(())
}

pub async fn is_admin_user(ctx: &mut Ctx, user: Uuid) -> CmdResult<bool> {
    Ok(sqlx::query_scalar(
        "select exists(select 1 from core.memberships where user_id = $1 and role in ('owner','admin'))",
    )
    .bind(user)
    .fetch_one(&mut *ctx.tx)
    .await?)
}

/// Spaces a specific user can read (same rules as cnote_core::access::readable_space_ids, but for a user other
/// than the caller: event handlers run as the system principal).
pub async fn readable_space_ids_for(ctx: &mut Ctx, user: Uuid) -> CmdResult<Vec<Uuid>> {
    Ok(sqlx::query_scalar(
        "select s.id from core.spaces s
         left join core.memberships m on m.user_id = $1
         left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1
         where m.role in ('owner','admin') or sm.user_id is not null
            or (s.visibility = 'open' and m.role = 'member')",
    )
    .bind(user)
    .fetch_all(&mut *ctx.tx)
    .await?)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_every_binding_kind() {
        let id = Uuid::new_v4();
        let md = format!(
            "See ENG-12 and [[Deploy Guide]] ([[Deploy Guide|again]]).\n\
             Link: cnote://acme/space/x/note/{id}\n\
             Code: repo:acme/api/src/lib.rs, PR https://github.com/acme/api/pull/9.\n\
             Docs at https://example.com/a?b=1, ENG-12 again."
        );
        assert_eq!(
            parse(&md),
            vec![
                Binding::ItemKey("ENG-12".into()),
                Binding::WikiLink("Deploy Guide".into()),
                Binding::NoteLink(id),
                Binding::Repo("acme/api/src/lib.rs".into()),
                Binding::Github("https://github.com/acme/api/pull/9".into()),
                Binding::Url("https://example.com/a?b=1".into()),
            ]
        );
    }

    #[test]
    fn keys_inside_urls_and_lowercase_are_ignored() {
        assert_eq!(
            parse("https://jira.example.com/browse/ENG-1 eng-3 A-1 ENG-0"),
            vec![Binding::Url("https://jira.example.com/browse/ENG-1".into())]
        );
    }
}
