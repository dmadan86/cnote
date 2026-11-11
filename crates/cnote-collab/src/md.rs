//! Markdown <-> the Tiptap/ProseMirror Y.XmlFragment named "default".
//! Blocks: paragraph, heading(level), bulletList/orderedList/listItem, codeBlock(language), blockquote,
//! table/tableRow/tableHeader/tableCell (GFM) and taskList/taskItem(checked). Output matches @tiptap/markdown
//! up to blank-line runs (see `same`), so lib.rs keeps the Yjs doc for pages edited in the browser.
//! shortcut: seeding keeps inline Markdown (**bold**, links) as literal text; marks written by real editors are
//! rendered back to Markdown when snapshotting. Upgrade when seeded pages must show formatting before first edit.

use yrs::types::text::YChange;
use yrs::{
    Any, Doc, Number, Out, ReadTxn, Text, Transact, WriteTxn, Xml, XmlElementPrelim, XmlFragment,
    XmlOut, XmlTextPrelim,
};

pub const FRAGMENT: &str = "default";

#[derive(Debug, PartialEq)]
enum Block {
    Para(String),
    Heading(u8, String),
    Code(Option<String>, String),
    Quote(Vec<String>),
    List(bool, Vec<Item>),
    /// Rows of cells; `true` when the first row is a header row.
    Table(bool, Vec<Vec<String>>),
    Tasks(Vec<(bool, String)>),
}

#[derive(Debug, PartialEq)]
struct Item {
    text: String,
    nested: Vec<Block>,
}

fn list_marker(line: &str) -> Option<(usize, bool, &str)> {
    let indent = line.len() - line.trim_start().len();
    let t = line.trim_start();
    for m in ["- ", "* ", "+ "] {
        if let Some(r) = t.strip_prefix(m) {
            return Some((indent, false, r));
        }
    }
    let digits = t.chars().take_while(char::is_ascii_digit).count();
    if digits > 0 && digits < 10 {
        let rest = &t[digits..];
        if let Some(r) = rest.strip_prefix(". ").or_else(|| rest.strip_prefix(") ")) {
            return Some((indent, true, r));
        }
    }
    None
}

fn heading(line: &str) -> Option<(u8, &str)> {
    let n = line.chars().take_while(|c| *c == '#').count();
    (1..=6).contains(&n).then_some(())?;
    line[n..].strip_prefix(' ').map(|t| (n as u8, t.trim()))
}

fn task(line: &str) -> Option<(bool, &str)> {
    let (indent, ordered, rest) = list_marker(line)?;
    if indent > 0 || ordered {
        return None;
    }
    rest.strip_prefix("[ ] ").map(|t| (false, t)).or_else(|| {
        rest.strip_prefix("[x] ")
            .or_else(|| rest.strip_prefix("[X] "))
            .map(|t| (true, t))
    })
}

fn is_table_sep(line: &str) -> bool {
    let t = line.trim();
    t.contains('|')
        && t.contains('-')
        && t.chars().all(|c| matches!(c, ' ' | '\t' | '|' | ':' | '-'))
}

/// Split a `| a | b \| c |` row into unescaped cell texts.
fn table_cells(line: &str) -> Vec<String> {
    let t = line.trim();
    let t = t.strip_prefix('|').unwrap_or(t);
    let t = t
        .strip_suffix('|')
        .filter(|r| !r.ends_with('\\'))
        .unwrap_or(t);
    let (mut cells, mut cur, mut esc) = (vec![], String::new(), false);
    for ch in t.chars() {
        match ch {
            '\\' if !esc => esc = true,
            '|' if !esc => cells.push(std::mem::take(&mut cur).trim().to_string()),
            _ => {
                if esc && ch != '|' {
                    cur.push('\\');
                }
                esc = false;
                cur.push(ch);
            }
        }
    }
    cells.push(cur.trim().to_string());
    cells
}

fn starts_block(line: &str) -> bool {
    line.trim_start().starts_with('|')
        || line.starts_with("```")
        || line.starts_with('>')
        || heading(line).is_some()
        || list_marker(line).is_some()
}

/// Parse consecutive list lines starting at `*i` whose indent is at least `base`.
fn parse_list(lines: &[&str], i: &mut usize, base: usize) -> Block {
    let (_, ordered, _) = list_marker(lines[*i]).unwrap();
    let mut items = vec![];
    while *i < lines.len() {
        let Some((indent, ord, text)) = list_marker(lines[*i]) else {
            break;
        };
        if indent < base || ord != ordered {
            break;
        }
        if indent > base {
            // Deeper than this list's items: belongs to the previous item.
            if let Some(last) = items.last_mut() {
                let Item { nested, .. }: &mut Item = last;
                nested.push(parse_list(lines, i, indent));
                continue;
            }
        }
        *i += 1;
        items.push(Item {
            text: text.trim().to_string(),
            nested: vec![],
        });
    }
    Block::List(ordered, items)
}

fn parse(md: &str) -> Vec<Block> {
    let lines: Vec<&str> = md.lines().collect();
    let (mut blocks, mut i) = (vec![], 0);
    while i < lines.len() {
        let line = lines[i];
        if line.trim().is_empty() {
            i += 1;
        } else if let Some(lang) = line.trim_start().strip_prefix("```") {
            let lang = Some(lang.trim().to_string()).filter(|l| !l.is_empty());
            let mut code = vec![];
            i += 1;
            while i < lines.len() && !lines[i].trim_start().starts_with("```") {
                code.push(lines[i]);
                i += 1;
            }
            i += 1;
            blocks.push(Block::Code(lang, code.join("\n")));
        } else if let Some((level, text)) = heading(line) {
            blocks.push(Block::Heading(level, text.to_string()));
            i += 1;
        } else if line.starts_with('>') {
            let mut paras = vec![String::new()];
            while i < lines.len() && lines[i].starts_with('>') {
                let t = lines[i].trim_start_matches('>').trim();
                if t.is_empty() {
                    paras.push(String::new());
                } else {
                    let p = paras.last_mut().unwrap();
                    if !p.is_empty() {
                        p.push(' ');
                    }
                    p.push_str(t);
                }
                i += 1;
            }
            paras.retain(|p| !p.is_empty());
            blocks.push(Block::Quote(paras));
        } else if line.trim_start().starts_with('|')
            && lines.get(i + 1).is_some_and(|l| is_table_sep(l))
        {
            let mut rows = vec![table_cells(line)];
            i += 2;
            while i < lines.len() && lines[i].trim_start().starts_with('|') {
                rows.push(table_cells(lines[i]));
                i += 1;
            }
            let header = rows[0].iter().any(|c| !c.is_empty());
            if !header {
                rows.remove(0);
            }
            blocks.push(Block::Table(header, rows));
        } else if task(line).is_some() {
            let mut items = vec![];
            while let Some((done, t)) = lines.get(i).and_then(|l| task(l)) {
                items.push((done, t.trim().to_string()));
                i += 1;
            }
            blocks.push(Block::Tasks(items));
        } else if list_marker(line).is_some() {
            let base = list_marker(line).unwrap().0;
            blocks.push(parse_list(&lines, &mut i, base));
        } else {
            let mut text = line.trim().to_string();
            i += 1;
            while i < lines.len() && !lines[i].trim().is_empty() && !starts_block(lines[i]) {
                text.push(' ');
                text.push_str(lines[i].trim());
                i += 1;
            }
            blocks.push(Block::Para(text));
        }
    }
    blocks
}

fn write_block<P: XmlFragment>(txn: &mut yrs::TransactionMut, parent: &P, b: &Block) {
    let text_el = |txn: &mut yrs::TransactionMut, tag: &str, text: &str| {
        let el = parent.push_back(txn, XmlElementPrelim::empty(tag));
        if !text.is_empty() {
            el.push_back(txn, XmlTextPrelim::new(text));
        }
        el
    };
    match b {
        Block::Para(t) => {
            text_el(txn, "paragraph", t);
        }
        Block::Heading(level, t) => {
            let el = text_el(txn, "heading", t);
            el.insert_attribute(txn, "level", Any::Number(Number::Int(*level as i64)));
        }
        Block::Code(lang, code) => {
            let el = text_el(txn, "codeBlock", code);
            if let Some(l) = lang {
                el.insert_attribute(txn, "language", Any::String(l.as_str().into()));
            }
        }
        Block::Quote(paras) => {
            let q = parent.push_back(txn, XmlElementPrelim::empty("blockquote"));
            for p in paras {
                let el = q.push_back(txn, XmlElementPrelim::empty("paragraph"));
                el.push_back(txn, XmlTextPrelim::new(p.as_str()));
            }
        }
        Block::Table(header, rows) => {
            let table = parent.push_back(txn, XmlElementPrelim::empty("table"));
            let width = rows.iter().map(Vec::len).max().unwrap_or(0);
            for (r, row) in rows.iter().enumerate() {
                let tr = table.push_back(txn, XmlElementPrelim::empty("tableRow"));
                for c in 0..width {
                    let tag = if *header && r == 0 {
                        "tableHeader"
                    } else {
                        "tableCell"
                    };
                    let cell = tr.push_back(txn, XmlElementPrelim::empty(tag));
                    let p = cell.push_back(txn, XmlElementPrelim::empty("paragraph"));
                    if let Some(t) = row.get(c).filter(|t| !t.is_empty()) {
                        p.push_back(txn, XmlTextPrelim::new(t.as_str()));
                    }
                }
            }
        }
        Block::Tasks(items) => {
            let list = parent.push_back(txn, XmlElementPrelim::empty("taskList"));
            for (done, t) in items {
                let li = list.push_back(txn, XmlElementPrelim::empty("taskItem"));
                li.insert_attribute(txn, "checked", Any::Bool(*done));
                let p = li.push_back(txn, XmlElementPrelim::empty("paragraph"));
                if !t.is_empty() {
                    p.push_back(txn, XmlTextPrelim::new(t.as_str()));
                }
            }
        }
        Block::List(ordered, items) => {
            let list = parent.push_back(
                txn,
                XmlElementPrelim::empty(if *ordered {
                    "orderedList"
                } else {
                    "bulletList"
                }),
            );
            for it in items {
                let li = list.push_back(txn, XmlElementPrelim::empty("listItem"));
                let p = li.push_back(txn, XmlElementPrelim::empty("paragraph"));
                if !it.text.is_empty() {
                    p.push_back(txn, XmlTextPrelim::new(it.text.as_str()));
                }
                for n in &it.nested {
                    write_block(txn, &li, n);
                }
            }
        }
    }
}

/// Fill the "default" fragment of a fresh doc from Markdown.
pub fn seed(doc: &Doc, md: &str) {
    let mut txn = doc.transact_mut();
    let frag = txn.get_or_insert_xml_fragment(FRAGMENT);
    for b in parse(md) {
        write_block(&mut txn, &frag, &b);
    }
}

fn mark(text: &str, attrs: Option<&yrs::types::Attrs>) -> String {
    let Some(a) = attrs else {
        return text.to_string();
    };
    let inner = text.trim();
    if inner.is_empty() {
        return text.to_string();
    }
    let (lead, trail) = (
        &text[..text.len() - text.trim_start().len()],
        &text[text.trim_end().len()..],
    );
    let has = |k: &str| a.contains_key(k);
    let mut s = inner.to_string();
    if has("code") {
        s = format!("`{s}`");
    }
    if has("italic") || has("em") {
        s = format!("*{s}*");
    }
    if has("bold") || has("strong") {
        s = format!("**{s}**");
    }
    if has("underline") {
        s = format!("<u>{s}</u>");
    }
    if has("strike") {
        s = format!("~~{s}~~");
    }
    if let Some(Any::Map(m)) = a.get("link")
        && let Some(Any::String(href)) = m.get("href")
    {
        s = format!("[{s}]({href})");
    }
    format!("{lead}{s}{trail}")
}

fn inline<T: ReadTxn>(txn: &T, el: &impl XmlFragment) -> String {
    let mut out = String::new();
    for c in el.children(txn) {
        match c {
            XmlOut::Text(t) => {
                for d in t.diff(txn, YChange::identity) {
                    if let Out::Any(Any::String(s)) = &d.insert {
                        out.push_str(&mark(s, d.attributes.as_deref()));
                    }
                }
            }
            XmlOut::Element(e) if &**e.tag() == "hardBreak" => out.push_str("  \n"),
            XmlOut::Element(e) => out.push_str(&inline(txn, &e)),
            XmlOut::Fragment(_) => {}
        }
    }
    out
}

fn indent_rest(s: &str, pad: &str) -> String {
    s.lines()
        .enumerate()
        .map(|(i, l)| {
            if i == 0 || l.is_empty() {
                l.to_string()
            } else {
                format!("{pad}{l}")
            }
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn render<T: ReadTxn>(txn: &T, node: XmlOut) -> Option<String> {
    let XmlOut::Element(el) = node else {
        return None;
    };
    let tag = el.tag().to_string();
    Some(match tag.as_str() {
        "heading" => {
            let level = match el.get_attribute(txn, "level") {
                Some(Out::Any(Any::Number(Number::Int(n)))) => n as usize,
                Some(Out::Any(Any::Number(Number::Float(n)))) => n as usize,
                Some(Out::Any(Any::String(s))) => s.parse().unwrap_or(1),
                _ => 1,
            };
            format!(
                "{} {}",
                "#".repeat(level.clamp(1, 6)),
                inline(txn, &el).trim()
            )
        }
        "codeBlock" => {
            let lang = match el.get_attribute(txn, "language") {
                Some(Out::Any(Any::String(s))) => s.to_string(),
                _ => String::new(),
            };
            format!("```{lang}\n{}\n```", inline(txn, &el))
        }
        "blockquote" => {
            let inner = blocks(txn, &el);
            inner
                .lines()
                .map(|l| {
                    if l.is_empty() {
                        ">".to_string()
                    } else {
                        format!("> {l}")
                    }
                })
                .collect::<Vec<_>>()
                .join("\n")
        }
        "bulletList" | "orderedList" => {
            let mut lines = vec![];
            for (n, li) in el.children(txn).enumerate() {
                let XmlOut::Element(li) = li else { continue };
                let marker = if tag == "bulletList" {
                    "- ".to_string()
                } else {
                    format!("{}. ", n + 1)
                };
                let parts: Vec<String> = li.children(txn).filter_map(|c| render(txn, c)).collect();
                lines.push(format!(
                    "{marker}{}",
                    indent_rest(&parts.join("\n"), &" ".repeat(marker.len()))
                ));
            }
            lines.join("\n")
        }
        "taskList" => {
            let mut lines = vec![];
            for li in el.children(txn) {
                let XmlOut::Element(li) = li else { continue };
                let done = matches!(
                    li.get_attribute(txn, "checked"),
                    Some(Out::Any(Any::Bool(true)))
                );
                let marker = if done { "- [x] " } else { "- [ ] " };
                let parts: Vec<String> = li.children(txn).filter_map(|c| render(txn, c)).collect();
                lines.push(format!(
                    "{marker}{}",
                    indent_rest(&parts.join("\n"), &" ".repeat(marker.len()))
                ));
            }
            lines.join("\n")
        }
        "table" => table_md(txn, &el),
        "horizontalRule" => "---".to_string(),
        _ => inline(txn, &el).trim().to_string(),
    })
}

/// GFM table exactly as @tiptap/extension-table renders it: padded columns (min width 3), `<br>` for line
/// breaks inside a cell, escaped pipes, and an empty header row when the table has none.
fn table_md<T: ReadTxn>(txn: &T, table: &yrs::XmlElementRef) -> String {
    let mut rows: Vec<Vec<(String, bool)>> = vec![];
    for tr in table.children(txn) {
        let XmlOut::Element(tr) = tr else { continue };
        let mut cells = vec![];
        for cell in tr.children(txn) {
            let XmlOut::Element(cell) = cell else {
                continue;
            };
            let raw = cell
                .children(txn)
                .filter_map(|c| render(txn, c))
                .collect::<Vec<_>>()
                .join("\n");
            let lines: Vec<&str> = raw.split('\n').map(str::trim).collect();
            let text = lines
                .join("<br>")
                .split_whitespace()
                .collect::<Vec<_>>()
                .join(" ");
            let mut esc = String::new();
            let mut it = text.chars().peekable();
            while let Some(ch) = it.next() {
                match ch {
                    '\\' => {
                        esc.push(ch);
                        if let Some(n) = it.next() {
                            esc.push(n);
                        }
                    }
                    '|' => esc.push_str("\\|"),
                    _ => esc.push(ch),
                }
            }
            cells.push((esc, &**cell.tag() == "tableHeader"));
        }
        rows.push(cells);
    }
    let cols = rows.iter().map(Vec::len).max().unwrap_or(0);
    if cols == 0 {
        return String::new();
    }
    let len = |s: &str| s.encode_utf16().count();
    let widths: Vec<usize> = (0..cols)
        .map(|i| {
            rows.iter()
                .filter_map(|r| r.get(i))
                .map(|c| len(&c.0))
                .max()
                .unwrap_or(0)
                .max(3)
        })
        .collect();
    let line = |texts: Vec<&str>| {
        let cells: Vec<String> = texts
            .iter()
            .zip(&widths)
            .map(|(t, w)| format!("{t}{}", " ".repeat(w.saturating_sub(len(t)))))
            .collect();
        format!("| {} |", cells.join(" | "))
    };
    let has_header = rows[0].iter().any(|c| c.1);
    let mut out = vec![line(
        (0..cols)
            .map(|i| {
                if has_header {
                    cell_text(&rows[0], i)
                } else {
                    ""
                }
            })
            .collect(),
    )];
    out.push(format!(
        "| {} |",
        widths
            .iter()
            .map(|w| "-".repeat(*w))
            .collect::<Vec<_>>()
            .join(" | ")
    ));
    for r in rows.iter().skip(usize::from(has_header)) {
        out.push(line((0..cols).map(|i| cell_text(r, i)).collect()));
    }
    out.join("\n")
}

fn cell_text(r: &[(String, bool)], i: usize) -> &str {
    r.get(i).map(|c| c.0.as_str()).unwrap_or("")
}

fn blocks<T: ReadTxn>(txn: &T, parent: &impl XmlFragment) -> String {
    parent
        .children(txn)
        .filter_map(|c| render(txn, c))
        .collect::<Vec<_>>()
        .join("\n\n")
}

/// Equal up to surrounding whitespace and blank-line runs (@tiptap/markdown pads tables with extra blank lines).
pub fn same(a: &str, b: &str) -> bool {
    let norm = |s: &str| {
        let mut out = String::new();
        let mut blank = 0;
        for l in s.trim().lines() {
            if l.trim().is_empty() {
                blank += 1;
                continue;
            }
            if !out.is_empty() {
                out.push_str(if blank > 0 { "\n\n" } else { "\n" });
            }
            blank = 0;
            out.push_str(l.trim_end());
        }
        out
    };
    norm(a) == norm(b)
}

/// Markdown snapshot of the "default" fragment (empty when there is none).
pub fn to_markdown(doc: &Doc) -> String {
    let txn = doc.transact();
    match txn.get_xml_fragment(FRAGMENT) {
        Some(f) => blocks(&txn, &f),
        None => String::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use yrs::XmlOut;

    const DOC: &str = "# Runbook\n\nRestart the pods, then wait.\n\n## Steps\n\n- check logs\n  - tail the api\n  - tail the worker\n- restart\n\n1. one\n2. two\n\n```sh\nkubectl get pods\n```\n\n> careful\n>\n> really careful";

    #[test]
    fn markdown_round_trips_through_the_fragment() {
        let doc = Doc::new();
        seed(&doc, DOC);
        assert_eq!(to_markdown(&doc), DOC);
    }

    #[test]
    fn fragment_has_the_tiptap_shape() {
        let doc = Doc::new();
        seed(&doc, "## Title\n\n- a\n- b");
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let tags: Vec<String> = f
            .children(&txn)
            .filter_map(|c| match c {
                XmlOut::Element(e) => Some(e.tag().to_string()),
                _ => None,
            })
            .collect();
        assert_eq!(tags, ["heading", "bulletList"]);
        let Some(XmlOut::Element(h)) = f.get(&txn, 0) else {
            panic!()
        };
        assert!(matches!(
            h.get_attribute(&txn, "level"),
            Some(Out::Any(Any::Number(Number::Int(2))))
        ));
        let Some(XmlOut::Element(l)) = f.get(&txn, 1) else {
            panic!()
        };
        let Some(XmlOut::Element(li)) = l.get(&txn, 0) else {
            panic!()
        };
        assert_eq!(&**li.tag(), "listItem");
        assert_eq!(
            &*match li.get(&txn, 0) {
                Some(XmlOut::Element(p)) => p.tag().clone(),
                _ => panic!(),
            },
            "paragraph"
        );
    }

    #[test]
    fn editor_marks_become_markdown() {
        let doc = Doc::new();
        {
            let mut txn = doc.transact_mut();
            let f = txn.get_or_insert_xml_fragment(FRAGMENT);
            let p = f.push_back(&mut txn, XmlElementPrelim::empty("paragraph"));
            let t = p.push_back(&mut txn, XmlTextPrelim::new(""));
            t.insert(&mut txn, 0, "plain ");
            let bold = yrs::types::Attrs::from([("bold".into(), Any::from(true))]);
            t.insert_with_attributes(&mut txn, 6, "loud", bold);
            let link = yrs::types::Attrs::from([(
                "link".into(),
                Any::from(std::collections::HashMap::from([(
                    "href".to_string(),
                    Any::from("https://example.com"),
                )])),
            )]);
            t.insert_with_attributes(&mut txn, 10, " docs", link);
        }
        assert_eq!(
            to_markdown(&doc),
            "plain **loud** [docs](https://example.com)"
        );
    }

    #[test]
    fn tables_and_tasks_round_trip_like_tiptap() {
        // Exactly what @tiptap/markdown produces for a 3x3 table with a header row and a to-do list.
        let md = "Intro\n\n| Name  | Qty | Note      |\n| ----- | --- | --------- |\n| Pens  | 10  | a \\| b    |\n| Paper |     | line<br>2 |\n\n- [ ] call Priya\n- [x] send quote";
        let doc = Doc::new();
        seed(&doc, md);
        assert_eq!(to_markdown(&doc), md);
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let Some(XmlOut::Element(t)) = f.get(&txn, 1) else {
            panic!()
        };
        assert_eq!(&**t.tag(), "table");
        let Some(XmlOut::Element(tl)) = f.get(&txn, 2) else {
            panic!()
        };
        assert_eq!(&**tl.tag(), "taskList");
    }

    #[test]
    fn table_without_header_renders_an_empty_header_row() {
        let doc = Doc::new();
        seed(&doc, "|     |     |\n| --- | --- |\n| a   | b   |");
        assert_eq!(
            to_markdown(&doc),
            "|     |     |\n| --- | --- |\n| a   | b   |"
        );
    }

    #[test]
    fn browser_and_server_snapshots_compare_equal() {
        let browser = "## T\n\n\n| h   |\n| --- |\n| v   |\n\n\nend\n";
        let doc = Doc::new();
        seed(&doc, browser);
        assert!(same(&to_markdown(&doc), browser));
        assert!(!same("a\n\nb", "a\nb"));
    }

    #[test]
    fn empty_input_gives_empty_doc() {
        let doc = Doc::new();
        seed(&doc, "");
        assert_eq!(to_markdown(&doc), "");
    }
}
