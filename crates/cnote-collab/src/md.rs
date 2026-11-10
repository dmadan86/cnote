//! Markdown <-> the Tiptap/ProseMirror Y.XmlFragment named "default".
//! Blocks: paragraph, heading(level), bulletList/orderedList/listItem, codeBlock(language), blockquote.
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

fn starts_block(line: &str) -> bool {
    line.starts_with("```")
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
        "horizontalRule" => "---".to_string(),
        _ => inline(txn, &el).trim().to_string(),
    })
}

fn blocks<T: ReadTxn>(txn: &T, parent: &impl XmlFragment) -> String {
    parent
        .children(txn)
        .filter_map(|c| render(txn, c))
        .collect::<Vec<_>>()
        .join("\n\n")
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
    fn empty_input_gives_empty_doc() {
        let doc = Doc::new();
        seed(&doc, "");
        assert_eq!(to_markdown(&doc), "");
    }
}
