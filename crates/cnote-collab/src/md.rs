//! Markdown <-> the Tiptap/ProseMirror Y.XmlFragment named "default".
//! Blocks: paragraph/heading (optional `textAlign`), bulletList/orderedList/listItem, codeBlock(language),
//! blockquote, image, table (GFM, or one-line HTML when a cell has colspan/rowspan/colwidth) and taskList/taskItem.
//! Inline marks (bold, italic, strike, code, link, `textStyle` colour, hardBreak) are parsed from Markdown into Yjs
//! text attributes exactly as y-prosemirror stores them, and rendered back. Output matches @tiptap/markdown up to
//! blank-line runs (see `same`), so lib.rs keeps the Yjs doc for pages edited in the browser.
//! The wire format (including the small HTML subset) is the storage contract in the repo docs: plain Markdown
//! wherever Markdown can say it, HTML only where it cannot. HTML input is parsed leniently, always written canonically.
//! shortcut: to_markdown does not backslash-escape literal `*`/`_` (the browser does not either), so `\*` seeded
//! as `*` comes back unescaped; a link spanning differently-marked runs renders one link per run.

use std::collections::HashMap;
use yrs::types::Attrs;
use yrs::types::text::YChange;
use yrs::{
    Any, Doc, Number, Out, ReadTxn, Text, Transact, TransactionMut, WriteTxn, Xml,
    XmlElementPrelim, XmlElementRef, XmlFragment, XmlOut, XmlTextPrelim,
};

pub const FRAGMENT: &str = "default";

#[derive(Debug, PartialEq, Clone, Default)]
struct Marks {
    bold: bool,
    italic: bool,
    strike: bool,
    code: bool,
    link: Option<String>,
    color: Option<String>,
}

#[derive(Debug, PartialEq)]
enum Node {
    Text(String, Marks),
    Break,
}

type Inl = Vec<Node>;

#[derive(Debug, PartialEq)]
struct Cell {
    header: bool,
    colspan: i64,
    rowspan: i64,
    colwidth: Option<Vec<i64>>,
    paras: Vec<Inl>,
}

#[derive(Debug, PartialEq)]
enum Block {
    Para(Inl, Option<String>),
    Heading(u8, Inl, Option<String>),
    Code(Option<String>, String),
    Quote(Vec<Inl>),
    List(bool, Vec<Item>),
    Table(Vec<Vec<Cell>>),
    Tasks(Vec<(bool, Inl)>),
    Image(String, String, Option<String>),
}

#[derive(Debug, PartialEq)]
struct Item {
    text: Inl,
    nested: Vec<Block>,
}

// ---------- HTML helpers (the fixed subset of the storage contract) ----------

fn esc(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}

fn unesc(s: &str) -> String {
    s.replace("&lt;", "<")
        .replace("&gt;", ">")
        .replace("&quot;", "\"")
        .replace("&#39;", "'")
        .replace("&apos;", "'")
        .replace("&amp;", "&")
}

struct Tag {
    name: String,
    closing: bool,
    attrs: Vec<(String, String)>,
    len: usize,
}

impl Tag {
    fn attr(&self, k: &str) -> Option<&str> {
        self.attrs.iter().find(|a| a.0 == k).map(|a| a.1.as_str())
    }
}

/// Parse an HTML tag at the start of `s` (any attribute order, quoted or bare values, optional `/`).
fn tag_at(s: &str) -> Option<Tag> {
    let b = s.as_bytes();
    if b.first() != Some(&b'<') {
        return None;
    }
    let mut i = 1;
    let closing = b.get(i) == Some(&b'/');
    i += usize::from(closing);
    let st = i;
    while i < b.len() && b[i].is_ascii_alphanumeric() {
        i += 1;
    }
    if i == st {
        return None;
    }
    let name = s[st..i].to_ascii_lowercase();
    let mut attrs = vec![];
    loop {
        while i < b.len() && (b[i].is_ascii_whitespace() || b[i] == b'/') {
            i += 1;
        }
        if *b.get(i)? == b'>' {
            i += 1;
            break;
        }
        let ks = i;
        while i < b.len() && !matches!(b[i], b'=' | b'>' | b'/') && !b[i].is_ascii_whitespace() {
            i += 1;
        }
        if i == ks {
            return None;
        }
        let key = s[ks..i].to_ascii_lowercase();
        let mut val = String::new();
        if b.get(i) == Some(&b'=') {
            i += 1;
            match *b.get(i)? {
                q @ (b'"' | b'\'') => {
                    let e = s[i + 1..].find(q as char)? + i + 1;
                    val = unesc(&s[i + 1..e]);
                    i = e + 1;
                }
                _ => {
                    let vs = i;
                    while i < b.len() && !b[i].is_ascii_whitespace() && b[i] != b'>' {
                        i += 1;
                    }
                    val = unesc(&s[vs..i]);
                }
            }
        }
        attrs.push((key, val));
    }
    Some(Tag {
        name,
        closing,
        attrs,
        len: i,
    })
}

fn style_prop(style: &str, key: &str) -> Option<String> {
    style.split(';').find_map(|d| {
        let (k, v) = d.split_once(':')?;
        (k.trim().eq_ignore_ascii_case(key)).then(|| v.trim().to_ascii_lowercase())
    })
}

/// Normalise a CSS colour to `#rrggbb`: `#rgb`, `#rrggbb`, `rgb()` / `rgba()`; anything else is rejected.
fn norm_color(c: &str) -> Option<String> {
    let c = c.trim().to_ascii_lowercase();
    if let Some(h) = c.strip_prefix('#') {
        if !h.chars().all(|x| x.is_ascii_hexdigit()) {
            return None;
        }
        return match h.len() {
            6 => Some(c),
            3 => Some(format!(
                "#{}",
                h.chars().flat_map(|x| [x, x]).collect::<String>()
            )),
            _ => None,
        };
    }
    let inner = c
        .strip_prefix("rgba(")
        .or_else(|| c.strip_prefix("rgb("))?
        .strip_suffix(')')?;
    let v: Vec<&str> = inner
        .split([',', ' ', '/'])
        .filter(|x| !x.is_empty())
        .collect();
    if v.len() < 3 || v.len() > 4 {
        return None;
    }
    let mut out = String::from("#");
    for x in &v[..3] {
        out.push_str(&format!("{:02x}", x.parse::<u8>().ok()?));
    }
    Some(out)
}

/// `#rrggbb` from a style attribute, if its colour is valid.
fn style_color(style: &str) -> Option<String> {
    norm_color(&style_prop(style, "color")?)
}

/// http(s), mailto or scheme-less (relative) links only.
fn safe_href(h: &str) -> bool {
    let h = h.trim().to_ascii_lowercase();
    if h.chars().any(char::is_control) {
        return false;
    }
    let scheme_end = h
        .find([':', '/', '?', '#'])
        .filter(|i| h.as_bytes()[*i] == b':');
    match scheme_end {
        Some(i) => matches!(&h[..i], "http" | "https" | "mailto"),
        None => true,
    }
}

fn safe_src(s: &str) -> bool {
    let s = s.trim().to_ascii_lowercase();
    s.starts_with("http://") || s.starts_with("https://")
}

fn push_text(out: &mut Inl, s: String, m: Marks) {
    if let Some(Node::Text(t, lm)) = out.last_mut()
        && *lm == m
    {
        t.push_str(&s);
        return;
    }
    out.push(Node::Text(s, m));
}

/// Inline HTML (contract §2) -> nodes; `None` when it holds anything outside the subset.
fn html_inline(s: &str) -> Option<Inl> {
    let (mut out, mut stack, mut m, mut i) =
        (vec![], Vec::<(String, Marks)>::new(), Marks::default(), 0);
    while i < s.len() {
        if s[i..].starts_with('<') {
            let t = tag_at(&s[i..])?;
            i += t.len;
            if t.name == "br" && !t.closing {
                out.push(Node::Break);
            } else if t.closing {
                let (name, prev) = stack.pop()?;
                if name != t.name {
                    return None;
                }
                m = prev;
            } else {
                stack.push((t.name.clone(), m.clone()));
                match t.name.as_str() {
                    "a" => m.link = Some(t.attr("href")?.to_string()).filter(|h| safe_href(h)),
                    "strong" | "b" => m.bold = true,
                    "em" | "i" => m.italic = true,
                    "s" | "del" | "strike" => m.strike = true,
                    "code" => m.code = true,
                    "span" => m.color = t.attr("style").and_then(style_color),
                    _ => return None,
                }
            }
        } else {
            let e = s[i..].find('<').map_or(s.len(), |p| p + i);
            push_text(&mut out, unesc(&s[i..e]), m.clone());
            i = e;
        }
    }
    stack.is_empty().then_some(out)
}

// ---------- Markdown inline parser ----------

/// Index of the `close` matching the `open` that starts `s` (backslash escapes skipped).
fn matching(s: &str, open: char, close: char) -> Option<usize> {
    let (mut d, mut esc) = (0, false);
    for (i, c) in s.char_indices() {
        if esc {
            esc = false;
        } else if c == '\\' {
            esc = true;
        } else if c == open {
            d += 1;
        } else if c == close {
            d -= 1;
            if d == 0 {
                return Some(i);
            }
        }
    }
    None
}

/// Start of the closing run for an `n`-char delimiter `c` opened at the start of `rest`.
fn closer(rest: &str, c: char, n: usize) -> Option<usize> {
    let b = rest.as_bytes();
    let mut j = n;
    while j < b.len() {
        match b[j] {
            b'\\' => j += 2,
            b'`' => j += rest[j + 1..].find('`').map_or(1, |e| e + 2),
            x if x == c as u8 => {
                let run = b[j..].iter().take_while(|y| **y == x).count();
                // a single-char delimiter skips longer runs (nested bold); a double one closes on the run's last two
                let k = if n == 1 {
                    (run == 1).then_some(j)
                } else {
                    (run >= 2).then(|| j + run - 2)
                };
                if let Some(k) = k
                    && k > n
                    && !rest[..k].ends_with(char::is_whitespace)
                {
                    return Some(k);
                }
                j += run;
            }
            _ => j += 1,
        }
    }
    None
}

/// Try one inline construct at the start of `rest`; returns bytes consumed and the nodes it makes.
fn construct(rest: &str, prev: Option<char>, m: &Marks, br: bool) -> Option<(usize, Inl)> {
    let c = rest.chars().next()?;
    match c {
        '`' => {
            let end = rest[1..].find('`')? + 1;
            let code = &rest[1..end];
            if code.is_empty() {
                return None;
            }
            let mm = Marks {
                code: true,
                color: None,
                ..m.clone()
            };
            Some((end + 1, vec![Node::Text(code.to_string(), mm)]))
        }
        '*' | '_' | '~' => {
            let n = if rest[1..].starts_with(c) { 2 } else { 1 };
            if c == '~' && n == 1 || c == '_' && prev.is_some_and(char::is_alphanumeric) {
                return None;
            }
            if rest[n..].chars().next().is_none_or(char::is_whitespace) {
                return None;
            }
            let k = closer(rest, c, n)?;
            if c == '_'
                && rest[k + n..]
                    .chars()
                    .next()
                    .is_some_and(char::is_alphanumeric)
            {
                return None;
            }
            let mut mm = m.clone();
            match (c, n) {
                ('~', _) => mm.strike = true,
                (_, 2) => mm.bold = true,
                _ => mm.italic = true,
            }
            Some((k + n, md_inline(&rest[n..k], &mm, br)))
        }
        '[' if m.link.is_none() => {
            let te = matching(rest, '[', ']')?;
            let after = &rest[te + 1..];
            if !after.starts_with('(') {
                return None;
            }
            let ue = matching(after, '(', ')')?;
            let url = after[1..ue].trim().split(" \"").next()?.trim();
            if url.is_empty() {
                return None;
            }
            let mm = Marks {
                link: Some(url.to_string()).filter(|u| safe_href(u)),
                ..m.clone()
            };
            Some((te + ue + 2, md_inline(&rest[1..te], &mm, br)))
        }
        '<' => {
            let t = tag_at(rest)?;
            if t.name == "br" && br && !t.closing {
                return Some((t.len, vec![Node::Break]));
            }
            if t.name == "span" && !t.closing {
                let color = t.attr("style").and_then(style_color);
                let e = rest[t.len..].to_ascii_lowercase().find("</span>")? + t.len;
                let mm = Marks { color, ..m.clone() };
                return Some((
                    e + 7,
                    vec![Node::Text(unesc(&md_unescape(&rest[t.len..e])), mm)],
                ));
            }
            None
        }
        _ => None,
    }
}

/// Inline Markdown -> nodes. `br`: also read `<br>` as a hard break (table cells).
fn md_inline(s: &str, m: &Marks, br: bool) -> Inl {
    let (mut out, mut lit, mut i) = (vec![], String::new(), 0);
    while i < s.len() {
        let rest = &s[i..];
        let c = rest.chars().next().unwrap();
        if c == '\\'
            && let Some(n) = rest[1..].chars().next().filter(char::is_ascii_punctuation)
        {
            lit.push(n);
            i += 2;
            continue;
        }
        if let Some((used, nodes)) = construct(rest, s[..i].chars().next_back(), m, br) {
            if !lit.is_empty() {
                push_text(&mut out, std::mem::take(&mut lit), m.clone());
            }
            for n in nodes {
                match n {
                    Node::Text(t, mm) => push_text(&mut out, t, mm),
                    b => out.push(b),
                }
            }
            i += used;
        } else {
            lit.push(c);
            i += c.len_utf8();
        }
    }
    if !lit.is_empty() {
        push_text(&mut out, lit, m.clone());
    }
    out
}

fn md(s: &str) -> Inl {
    md_inline(s, &Marks::default(), false)
}

// ---------- block parser ----------

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
            text: md(text.trim()),
            nested: vec![],
        });
    }
    Block::List(ordered, items)
}

fn image(line: &str) -> Option<Block> {
    let t = line.trim().strip_prefix("![")?;
    let alt_end = t.find("](")?;
    let inner = t[alt_end + 2..].strip_suffix(')')?;
    let (src, title) = match inner.split_once(' ') {
        Some((s, r)) => (s, Some(r.trim().strip_prefix('"')?.strip_suffix('"')?)),
        None => (inner, None),
    };
    safe_src(src).then(|| {
        Block::Image(
            src.to_string(),
            t[..alt_end].to_string(),
            title.map(str::to_string),
        )
    })
}

fn parse_cell(t: &Tag, body: &str) -> Option<Cell> {
    let n = |k| {
        t.attr(k)
            .and_then(|v| v.trim().parse::<i64>().ok())
            .filter(|n| *n >= 1)
            .unwrap_or(1)
    };
    let colwidth = t
        .attr("colwidth")
        .and_then(|v| {
            v.split(',')
                .map(|x| x.trim().parse::<i64>().ok())
                .collect::<Option<Vec<_>>>()
        })
        .filter(|v| !v.is_empty() && v.iter().all(|n| *n > 0));
    let (mut paras, mut rest) = (vec![], body.trim());
    while !rest.is_empty() {
        match tag_at(rest) {
            Some(p) if p.name == "p" && !p.closing => {
                let e = rest.to_ascii_lowercase().find("</p>")?;
                paras.push(html_inline(rest.get(p.len..e)?)?);
                rest = rest[e + 4..].trim();
            }
            _ => {
                paras.push(html_inline(rest)?);
                break;
            }
        }
    }
    Some(Cell {
        header: t.name == "th",
        colspan: n("colspan"),
        rowspan: n("rowspan"),
        colwidth,
        paras,
    })
}

/// `s` is a whole `<table>...</table>`; `pos` is just past its opening tag.
fn table_block(s: &str, mut pos: usize) -> Option<Block> {
    let lc = s.to_ascii_lowercase();
    let (mut rows, mut cur): (Vec<Vec<Cell>>, Option<Vec<Cell>>) = (vec![], None);
    loop {
        pos += s[pos..].len() - s[pos..].trim_start().len();
        let t = tag_at(&s[pos..])?;
        pos += t.len;
        match (t.name.as_str(), t.closing) {
            ("tbody" | "thead", _) => {}
            ("tr", false) if cur.is_none() => cur = Some(vec![]),
            ("tr", true) => rows.push(cur.take()?),
            ("th" | "td", false) => {
                let close = format!("</{}>", t.name);
                let e = lc[pos..].find(&close)? + pos;
                cur.as_mut()?.push(parse_cell(&t, &s[pos..e])?);
                pos = e + close.len();
            }
            ("table", true) => break,
            _ => return None,
        }
    }
    (s[pos..].trim().is_empty() && !rows.is_empty() && cur.is_none()).then_some(Block::Table(rows))
}

/// One HTML block (contract §3/§4) starting at `lines[i]`; returns it and the lines used.
fn html_at(lines: &[&str], i: usize) -> Option<(Block, usize)> {
    let first = lines[i].trim();
    let lower = first.to_ascii_lowercase();
    let (s, used) = if lower.starts_with("<table") && !lower.contains("</table>") {
        let end =
            (i + 1..lines.len()).find(|j| lines[*j].to_ascii_lowercase().contains("</table>"))?;
        (lines[i..=end].join(" "), end - i + 1)
    } else {
        (first.to_string(), 1)
    };
    let t = tag_at(&s)?;
    let lc = s.to_ascii_lowercase();
    if t.closing {
        return None;
    }
    if t.name == "table" {
        return Some((table_block(&s, t.len)?, used));
    }
    let level = match t.name.as_bytes() {
        [b'p'] => 0,
        [b'h', d @ b'1'..=b'6'] => d - b'0',
        _ => return None,
    };
    let close = format!("</{}>", t.name);
    let body = lc
        .ends_with(&close)
        .then(|| s.get(t.len..s.len() - close.len()))??;
    let inl = html_inline(body)?;
    let align = t
        .attr("style")
        .and_then(|st| style_prop(st, "text-align"))
        .filter(|a| matches!(a.as_str(), "center" | "right" | "justify"));
    let block = if level == 0 {
        Block::Para(inl, align)
    } else {
        Block::Heading(level, inl, align)
    };
    Some((block, used))
}

fn parse(md_text: &str) -> Vec<Block> {
    let lines: Vec<&str> = md_text.lines().collect();
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
            blocks.push(Block::Heading(level, md(text), None));
            i += 1;
        } else if let Some((b, used)) = html_at(&lines, i) {
            blocks.push(b);
            i += used;
        } else if let Some(b) = image(line) {
            blocks.push(b);
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
            blocks.push(Block::Quote(
                paras
                    .iter()
                    .filter(|p| !p.is_empty())
                    .map(|p| md(p))
                    .collect(),
            ));
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
            let width = rows.iter().map(Vec::len).max().unwrap_or(0);
            blocks.push(Block::Table(
                rows.iter()
                    .enumerate()
                    .map(|(r, row)| {
                        (0..width)
                            .map(|c| Cell {
                                header: header && r == 0,
                                colspan: 1,
                                rowspan: 1,
                                colwidth: None,
                                paras: vec![md_inline(
                                    row.get(c).map_or("", String::as_str),
                                    &Marks::default(),
                                    true,
                                )],
                            })
                            .collect()
                    })
                    .collect(),
            ));
        } else if task(line).is_some() {
            let mut items = vec![];
            while let Some((done, t)) = lines.get(i).and_then(|l| task(l)) {
                items.push((done, md(t.trim())));
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
            blocks.push(Block::Para(md(&text), None));
        }
    }
    blocks
}

// ---------- Yjs writer ----------

fn str_attr(s: &str) -> Any {
    Any::String(s.into())
}

fn int_attr(n: i64) -> Any {
    Any::Number(Number::Int(n))
}

/// Marks as y-prosemirror stores them: one XmlText attribute per mark name, valued with the mark's attrs.
fn mark_attrs(m: &Marks) -> Attrs {
    let obj = |kv: &[(&str, &str)]| {
        Any::from(
            kv.iter()
                .map(|(k, v)| (k.to_string(), str_attr(v)))
                .collect::<HashMap<_, _>>(),
        )
    };
    let mut a = Attrs::new();
    for (on, name) in [
        (m.bold, "bold"),
        (m.italic, "italic"),
        (m.strike, "strike"),
        (m.code, "code"),
    ] {
        if on {
            a.insert(name.into(), obj(&[]));
        }
    }
    if let Some(h) = &m.link {
        a.insert("link".into(), obj(&[("href", h)]));
    }
    if let Some(c) = &m.color {
        a.insert("textStyle".into(), obj(&[("color", c)]));
    }
    a
}

fn write_inline<P: XmlFragment>(txn: &mut TransactionMut, parent: &P, inl: &Inl) {
    let mut cur: Option<yrs::XmlTextRef> = None;
    for n in inl {
        match n {
            Node::Break => {
                parent.push_back(txn, XmlElementPrelim::empty("hardBreak"));
                cur = None;
            }
            Node::Text(s, m) => {
                let t = match &cur {
                    Some(t) => t.clone(),
                    None => parent.push_back(txn, XmlTextPrelim::new("")),
                };
                // always explicit attrs: a plain insert would inherit the neighbouring run's marks
                let at = t.len(txn);
                t.insert_with_attributes(txn, at, s, mark_attrs(m));
                cur = Some(t);
            }
        }
    }
}

fn text_el<P: XmlFragment>(
    txn: &mut TransactionMut,
    parent: &P,
    tag: &str,
    inl: &Inl,
) -> XmlElementRef {
    let el = parent.push_back(txn, XmlElementPrelim::empty(tag));
    write_inline(txn, &el, inl);
    el
}

fn set_align(txn: &mut TransactionMut, el: &XmlElementRef, align: &Option<String>) {
    if let Some(a) = align {
        el.insert_attribute(txn, "textAlign", str_attr(a));
    }
}

fn write_block<P: XmlFragment>(txn: &mut TransactionMut, parent: &P, b: &Block) {
    match b {
        Block::Para(t, align) => {
            let el = text_el(txn, parent, "paragraph", t);
            set_align(txn, &el, align);
        }
        Block::Heading(level, t, align) => {
            let el = text_el(txn, parent, "heading", t);
            el.insert_attribute(txn, "level", int_attr(*level as i64));
            set_align(txn, &el, align);
        }
        Block::Code(lang, code) => {
            let el = parent.push_back(txn, XmlElementPrelim::empty("codeBlock"));
            if !code.is_empty() {
                el.push_back(txn, XmlTextPrelim::new(code.as_str()));
            }
            if let Some(l) = lang {
                el.insert_attribute(txn, "language", str_attr(l));
            }
        }
        Block::Quote(paras) => {
            let q = parent.push_back(txn, XmlElementPrelim::empty("blockquote"));
            for p in paras {
                text_el(txn, &q, "paragraph", p);
            }
        }
        Block::Image(src, alt, title) => {
            let el = parent.push_back(txn, XmlElementPrelim::empty("image"));
            el.insert_attribute(txn, "src", str_attr(src));
            el.insert_attribute(txn, "alt", str_attr(alt));
            if let Some(t) = title {
                el.insert_attribute(txn, "title", str_attr(t));
            }
        }
        Block::Table(rows) => {
            let table = parent.push_back(txn, XmlElementPrelim::empty("table"));
            for row in rows {
                let tr = table.push_back(txn, XmlElementPrelim::empty("tableRow"));
                for c in row {
                    let tag = if c.header { "tableHeader" } else { "tableCell" };
                    let cell = tr.push_back(txn, XmlElementPrelim::empty(tag));
                    if c.colspan > 1 {
                        cell.insert_attribute(txn, "colspan", int_attr(c.colspan));
                    }
                    if c.rowspan > 1 {
                        cell.insert_attribute(txn, "rowspan", int_attr(c.rowspan));
                    }
                    if let Some(w) = &c.colwidth {
                        let w: Vec<Any> = w.iter().map(|n| int_attr(*n)).collect();
                        cell.insert_attribute(txn, "colwidth", Any::Array(w.into()));
                    }
                    for p in &c.paras {
                        text_el(txn, &cell, "paragraph", p);
                    }
                    if c.paras.is_empty() {
                        text_el(txn, &cell, "paragraph", &vec![]);
                    }
                }
            }
        }
        Block::Tasks(items) => {
            let list = parent.push_back(txn, XmlElementPrelim::empty("taskList"));
            for (done, t) in items {
                let li = list.push_back(txn, XmlElementPrelim::empty("taskItem"));
                li.insert_attribute(txn, "checked", Any::Bool(*done));
                text_el(txn, &li, "paragraph", t);
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
                text_el(txn, &li, "paragraph", &it.text);
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

// ---------- Markdown / HTML renderer ----------

#[derive(Clone, Copy, PartialEq)]
enum Mode {
    /// Markdown context: marks as Markdown, colour as an HTML span.
    Md,
    /// Inside an HTML block (contract §2).
    Html,
    /// Text only, HTML-escaped.
    Plain,
}

fn color(a: &Attrs) -> Option<String> {
    if let Some(Any::Map(m)) = a.get("textStyle")
        && let Some(Any::String(c)) = m.get("color")
    {
        return norm_color(c);
    }
    None
}

fn link_href(a: &Attrs) -> Option<String> {
    if let Some(Any::Map(m)) = a.get("link")
        && let Some(Any::String(href)) = m.get("href")
    {
        return Some(href.to_string()).filter(|h| safe_href(h));
    }
    None
}

/// Markdown-escape text the way @tiptap/markdown does before a mark wraps it (colour spans only).
fn md_escape(s: &str) -> String {
    let mut o = String::new();
    for c in s.chars() {
        if matches!(c, '\\' | '*' | '_' | '`' | '~' | '[' | ']') {
            o.push('\\');
        }
        o.push(c);
    }
    o
}

fn md_unescape(s: &str) -> String {
    let mut o = String::new();
    let mut it = s.chars().peekable();
    while let Some(c) = it.next() {
        match it.peek() {
            Some(n) if c == '\\' && n.is_ascii_punctuation() => o.push(it.next().unwrap()),
            _ => o.push(c),
        }
    }
    o
}

fn span(c: &str, inner: &str) -> String {
    format!("<span style=\"color: {c}\">{inner}</span>")
}

fn mark(text: &str, attrs: Option<&Attrs>, mode: Mode) -> String {
    let Some(a) = attrs else {
        return if mode == Mode::Md {
            text.to_string()
        } else {
            esc(text)
        };
    };
    let has = |k: &str| a.contains_key(k);
    let (code, col) = (has("code"), color(a));
    let col = col.filter(|_| !code);
    match mode {
        Mode::Plain => esc(text),
        Mode::Html => {
            let mut s = esc(text);
            if let Some(c) = &col {
                s = span(c, &s);
            }
            for (on, tag) in [
                (code, "code"),
                (has("strike"), "s"),
                (has("italic") || has("em"), "em"),
                (has("bold") || has("strong"), "strong"),
            ] {
                if on {
                    s = format!("<{tag}>{s}</{tag}>");
                }
            }
            match link_href(a) {
                Some(h) => format!("<a href=\"{}\">{s}</a>", esc(&h)),
                None => s,
            }
        }
        Mode::Md => {
            let inner = text.trim();
            if inner.is_empty() {
                return text.to_string();
            }
            let (lead, trail) = (
                &text[..text.len() - text.trim_start().len()],
                &text[text.trim_end().len()..],
            );
            let mut s = match &col {
                Some(c) => span(c, &esc(&md_escape(inner))),
                None => inner.to_string(),
            };
            if code {
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
            if let Some(h) = link_href(a) {
                s = format!("[{s}]({h})");
            }
            format!("{lead}{s}{trail}")
        }
    }
}

fn inline<T: ReadTxn>(txn: &T, el: &impl XmlFragment, mode: Mode) -> String {
    let mut out = String::new();
    for c in el.children(txn) {
        match c {
            XmlOut::Text(t) => {
                for d in t.diff(txn, YChange::identity) {
                    if let Out::Any(Any::String(s)) = &d.insert {
                        out.push_str(&mark(s, d.attributes.as_deref(), mode));
                    }
                }
            }
            XmlOut::Element(e) if &**e.tag() == "hardBreak" => out.push_str(match mode {
                Mode::Md => "  \n",
                Mode::Html => "<br>",
                Mode::Plain => " ",
            }),
            XmlOut::Element(e) => out.push_str(&inline(txn, &e, mode)),
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

fn str_of(o: Option<Out>) -> Option<String> {
    match o {
        Some(Out::Any(Any::String(s))) => Some(s.to_string()),
        _ => None,
    }
}

fn int_of(o: Option<Out>) -> Option<i64> {
    match o {
        Some(Out::Any(Any::Number(Number::Int(n)))) => Some(n),
        Some(Out::Any(Any::Number(Number::Float(n)))) => Some(n as i64),
        Some(Out::Any(Any::String(s))) => s.parse().ok(),
        _ => None,
    }
}

fn align_of<T: ReadTxn>(txn: &T, el: &XmlElementRef) -> Option<String> {
    str_of(el.get_attribute(txn, "textAlign"))
        .filter(|a| matches!(a.as_str(), "center" | "right" | "justify"))
}

/// `top`: the block sits directly in the document, where alignment is allowed.
fn render<T: ReadTxn>(txn: &T, node: XmlOut, top: bool) -> Option<String> {
    let XmlOut::Element(el) = node else {
        return None;
    };
    let tag = el.tag().to_string();
    let align = align_of(txn, &el).filter(|_| top);
    Some(match tag.as_str() {
        "heading" => {
            let level = int_of(el.get_attribute(txn, "level"))
                .unwrap_or(1)
                .clamp(1, 6) as usize;
            match align {
                Some(a) => format!(
                    "<h{level} style=\"text-align: {a}\">{}</h{level}>",
                    inline(txn, &el, Mode::Html)
                ),
                None => format!(
                    "{} {}",
                    "#".repeat(level),
                    inline(txn, &el, Mode::Md).trim()
                ),
            }
        }
        "paragraph" if align.is_some() => format!(
            "<p style=\"text-align: {}\">{}</p>",
            align.unwrap(),
            inline(txn, &el, Mode::Html)
        ),
        "image" => {
            let get = |k| str_of(el.get_attribute(txn, k));
            let (src, alt) = (
                get("src").unwrap_or_default(),
                get("alt").unwrap_or_default(),
            );
            if !safe_src(&src) {
                return None;
            }
            match get("title").filter(|t| !t.is_empty()) {
                Some(t) => format!("![{alt}]({src} \"{t}\")"),
                None => format!("![{alt}]({src})"),
            }
        }
        "codeBlock" => {
            let lang = str_of(el.get_attribute(txn, "language")).unwrap_or_default();
            format!("```{lang}\n{}\n```", inline(txn, &el, Mode::Md))
        }
        "blockquote" => {
            let inner = blocks(txn, &el, false);
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
                let parts: Vec<String> = li
                    .children(txn)
                    .filter_map(|c| render(txn, c, false))
                    .collect();
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
                let parts: Vec<String> = li
                    .children(txn)
                    .filter_map(|c| render(txn, c, false))
                    .collect();
                lines.push(format!(
                    "{marker}{}",
                    indent_rest(&parts.join("\n"), &" ".repeat(marker.len()))
                ));
            }
            lines.join("\n")
        }
        "table" => {
            if cells(txn, &el)
                .iter()
                .flatten()
                .any(|c| c.2 != (1, 1, None))
            {
                table_html(txn, &el)
            } else {
                table_md(txn, &el)
            }
        }
        "horizontalRule" => "---".to_string(),
        _ => inline(txn, &el, Mode::Md).trim().to_string(),
    })
}

type CellAttrs = (i64, i64, Option<Vec<i64>>);

/// Every cell as (element, is-header, (colspan, rowspan, colwidth)), row by row.
fn cells<T: ReadTxn>(txn: &T, table: &XmlElementRef) -> Vec<Vec<(XmlElementRef, bool, CellAttrs)>> {
    let mut rows = vec![];
    for tr in table.children(txn) {
        let XmlOut::Element(tr) = tr else { continue };
        let mut row = vec![];
        for cell in tr.children(txn) {
            let XmlOut::Element(cell) = cell else {
                continue;
            };
            let span = |k| {
                int_of(cell.get_attribute(txn, k))
                    .filter(|n| *n >= 1)
                    .unwrap_or(1)
            };
            let width = match cell.get_attribute(txn, "colwidth") {
                Some(Out::Any(Any::Array(a))) => a
                    .iter()
                    .map(|x| int_of(Some(Out::Any(x.clone()))).filter(|n| *n > 0))
                    .collect::<Option<Vec<i64>>>()
                    .filter(|w| !w.is_empty()),
                _ => None,
            };
            let attrs = (span("colspan"), span("rowspan"), width);
            let header = &**cell.tag() == "tableHeader";
            row.push((cell, header, attrs));
        }
        rows.push(row);
    }
    rows
}

/// One-line HTML table (contract §4).
fn table_html<T: ReadTxn>(txn: &T, table: &XmlElementRef) -> String {
    let mut out = String::from("<table>");
    for row in cells(txn, table) {
        out.push_str("<tr>");
        for (cell, header, (cs, rs, w)) in row {
            let tag = if header { "th" } else { "td" };
            out.push_str(&format!("<{tag}"));
            if cs > 1 {
                out.push_str(&format!(" colspan=\"{cs}\""));
            }
            if rs > 1 {
                out.push_str(&format!(" rowspan=\"{rs}\""));
            }
            if let Some(w) = w {
                let w: Vec<String> = w.iter().map(i64::to_string).collect();
                out.push_str(&format!(" colwidth=\"{}\"", w.join(",")));
            }
            out.push('>');
            let mut any = false;
            for c in cell.children(txn) {
                let XmlOut::Element(p) = c else { continue };
                let mode = if &**p.tag() == "paragraph" {
                    Mode::Html
                } else {
                    Mode::Plain
                };
                out.push_str(&format!("<p>{}</p>", inline(txn, &p, mode)));
                any = true;
            }
            if !any {
                out.push_str("<p></p>");
            }
            out.push_str(&format!("</{tag}>"));
        }
        out.push_str("</tr>");
    }
    out.push_str("</table>");
    out
}

/// GFM table exactly as @tiptap/extension-table renders it: padded columns (min width 3), `<br>` for line
/// breaks inside a cell, escaped pipes, and an empty header row when the table has none.
fn table_md<T: ReadTxn>(txn: &T, table: &XmlElementRef) -> String {
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
                .filter_map(|c| render(txn, c, false))
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

fn blocks<T: ReadTxn>(txn: &T, parent: &impl XmlFragment, top: bool) -> String {
    parent
        .children(txn)
        .filter_map(|c| render(txn, c, top))
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
        Some(f) => blocks(&txn, &f, true),
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

    fn rt(md: &str) -> String {
        let doc = Doc::new();
        seed(&doc, md);
        to_markdown(&doc)
    }

    /// Attribute names present on the first text run whose text is `needle`.
    fn marks_of(md: &str, needle: &str) -> Vec<String> {
        let doc = Doc::new();
        seed(&doc, md);
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let Some(XmlOut::Element(p)) = f.get(&txn, 0) else {
            panic!()
        };
        for c in p.children(&txn) {
            if let XmlOut::Text(t) = c {
                for d in t.diff(&txn, YChange::identity) {
                    if matches!(&d.insert, Out::Any(Any::String(s)) if &**s == needle) {
                        let mut k: Vec<String> = d
                            .attributes
                            .iter()
                            .flat_map(|a| a.keys().map(|k| k.to_string()))
                            .collect();
                        k.sort();
                        return k;
                    }
                }
            }
        }
        panic!("run {needle:?} not found")
    }

    #[test]
    fn each_inline_mark_becomes_a_yjs_attribute_and_back() {
        for (md, run, mark) in [
            ("a **b** c", "b", "bold"),
            ("a __b__ c", "b", "bold"),
            ("a *b* c", "b", "italic"),
            ("a _b_ c", "b", "italic"),
            ("a ~~b~~ c", "b", "strike"),
            ("a `b` c", "b", "code"),
            ("a [b](https://x.y) c", "b", "link"),
        ] {
            assert_eq!(marks_of(md, run), [mark], "{md}");
        }
        for md in [
            "a **b** c",
            "a *b* c",
            "a ~~b~~ c",
            "a `b` c",
            "a [b](https://x.y) c",
        ] {
            assert_eq!(rt(md), md);
        }
        assert_eq!(rt("a __b__ c _d_"), "a **b** c *d*");
        assert_eq!(rt("a [b](https://x.y \"t\") c"), "a [b](https://x.y) c");
    }

    #[test]
    fn link_mark_carries_href() {
        let doc = Doc::new();
        seed(&doc, "[docs](https://example.com/a)");
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let Some(XmlOut::Element(p)) = f.get(&txn, 0) else {
            panic!()
        };
        let Some(XmlOut::Text(t)) = p.get(&txn, 0) else {
            panic!()
        };
        let d = t.diff(&txn, YChange::identity);
        let Some(Any::Map(m)) = d[0].attributes.as_ref().unwrap().get("link") else {
            panic!()
        };
        assert!(matches!(m.get("href"), Some(Any::String(h)) if &**h == "https://example.com/a"));
    }

    #[test]
    fn nesting_escapes_and_unclosed_markers() {
        assert_eq!(marks_of("**bold *both***", "both"), ["bold", "italic"]);
        assert_eq!(marks_of("**bold *both***", "bold "), ["bold"]);
        assert_eq!(rt("**bold *both***"), "**bold** ***both***");
        assert_eq!(marks_of("[**x**](https://u.v)", "x"), ["bold", "link"]);
        assert_eq!(rt("[**x**](https://u.v)"), "[**x**](https://u.v)");
        assert_eq!(rt("\\*not italic\\* and \\[x\\]"), "*not italic* and [x]");
        assert_eq!(marks_of("\\*lit\\*", "*lit*"), Vec::<String>::new());
        for lit in [
            "**open",
            "a * b * c",
            "snake_case_name",
            "[no](",
            "~one~",
            "`open",
            "2 ** 3",
        ] {
            assert_eq!(rt(lit), lit);
        }
    }

    #[test]
    fn colour_span_in_markdown() {
        let c = "<span style=\"color: #dc2626\">hot</span>";
        assert_eq!(rt(c), c);
        assert_eq!(marks_of(c, "hot"), ["textStyle"]);
        let bold = format!("**{c}**");
        assert_eq!(rt(&bold), bold);
        assert_eq!(marks_of(&bold, "hot"), ["bold", "textStyle"]);
        let link = "[<span style=\"color: #dc2626\">x</span>](https://a.b)";
        assert_eq!(rt(link), link);
        assert_eq!(marks_of(link, "x"), ["link", "textStyle"]);
        // lenient input: shorthand, rgb(), trailing semicolon, upper case; text is HTML-escaped on write
        assert_eq!(
            rt("<span style=\"color:#F00;\">a &amp; b</span>"),
            "<span style=\"color: #ff0000\">a &amp; b</span>"
        );
        assert_eq!(
            rt("<span style='color: rgb(37, 99, 235)'>x</span>"),
            "<span style=\"color: #2563eb\">x</span>"
        );
    }

    #[test]
    fn aligned_paragraph_and_heading_with_inline_html() {
        let p = "<p style=\"text-align: center\"><a href=\"https://x.y\"><strong>Hi</strong></a> &amp; <span style=\"color: #2563eb\">blue</span><br>next <em>it</em> <s>no</s> <code>c</code></p>";
        assert_eq!(rt(p), p);
        let h = "<h2 style=\"text-align: right\">T <strong>b</strong></h2>";
        assert_eq!(rt(h), h);
        let doc = Doc::new();
        seed(&doc, h);
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let Some(XmlOut::Element(e)) = f.get(&txn, 0) else {
            panic!()
        };
        assert_eq!(&**e.tag(), "heading");
        assert_eq!(
            str_of(e.get_attribute(&txn, "textAlign")).as_deref(),
            Some("right")
        );
        assert_eq!(int_of(e.get_attribute(&txn, "level")), Some(2));
        // leniency: aliases, attribute noise, justify; left / unknown tags
        assert_eq!(
            rt(
                "<p class=\"x\" style=\"text-align:justify;\"><b>a</b><i>b</i><del>c</del><br/></p>"
            ),
            "<p style=\"text-align: justify\"><strong>a</strong><em>b</em><s>c</s><br></p>"
        );
        assert_eq!(rt("<p style=\"text-align: left\">x</p>"), "x");
        assert_eq!(rt("<p><blink>x</blink></p>"), "<p><blink>x</blink></p>");
    }

    #[test]
    fn aligned_blocks_inside_lists_lose_alignment() {
        let doc = Doc::new();
        seed(&doc, "<p style=\"text-align: center\">x</p>");
        {
            let mut txn = doc.transact_mut();
            let f = txn.get_or_insert_xml_fragment(FRAGMENT);
            let q = f.push_back(&mut txn, XmlElementPrelim::empty("blockquote"));
            let p = q.push_back(&mut txn, XmlElementPrelim::empty("paragraph"));
            p.insert_attribute(&mut txn, "textAlign", str_attr("center"));
            p.push_back(&mut txn, XmlTextPrelim::new("in quote"));
        }
        assert_eq!(
            to_markdown(&doc),
            "<p style=\"text-align: center\">x</p>\n\n> in quote"
        );
    }

    const HTML_TABLE: &str = "<table><tr><th colspan=\"2\" colwidth=\"120,80\"><p>Head <strong>A</strong></p></th><th><p>B</p></th></tr><tr><td rowspan=\"2\"><p>x</p><p>y</p></td><td><p></p></td><td><p>a<br>b</p></td></tr><tr><td colwidth=\"50\"><p>z &amp; w</p></td><td><p></p></td></tr></table>";

    #[test]
    fn html_table_round_trips_and_has_the_tiptap_shape() {
        assert_eq!(rt(HTML_TABLE), HTML_TABLE);
        let doc = Doc::new();
        seed(&doc, HTML_TABLE);
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let Some(XmlOut::Element(t)) = f.get(&txn, 0) else {
            panic!()
        };
        let rows = cells(&txn, &t);
        assert_eq!(rows.len(), 3);
        assert_eq!(rows[0][0].2, (2, 1, Some(vec![120, 80])));
        assert!(rows[0][0].1 && !rows[1][0].1);
        assert_eq!(rows[1][0].2, (1, 2, None));
        assert_eq!(rows[2][0].2, (1, 1, Some(vec![50])));
    }

    #[test]
    fn html_table_input_is_parsed_leniently() {
        let loose = "<table>\n<tbody>\n <tr> <th colspan='2'> <p>A</p> </th> </tr>\n <tr><td colspan=2>x</td><td rowspan=\"1\" colspan=\"1\">y</td></tr>\n</tbody>\n</table>";
        assert_eq!(
            rt(loose),
            "<table><tr><th colspan=\"2\"><p>A</p></th></tr><tr><td colspan=\"2\"><p>x</p></td><td><p>y</p></td></tr></table>"
        );
        assert_eq!(
            rt("<table><tr><td>x</td></tr><div></table>"),
            "<table><tr><td>x</td></tr><div></table>"
        );
    }

    #[test]
    fn gfm_table_cells_carry_inline_marks() {
        let md = "| Name      | Note           |\n| --------- | -------------- |\n| **bold**  | [a](https://x) |\n| `c` ~~s~~ | *i*<br>line 2  |";
        let doc = Doc::new();
        seed(&doc, md);
        let out = to_markdown(&doc);
        assert_eq!(out, md);
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let Some(XmlOut::Element(t)) = f.get(&txn, 0) else {
            panic!()
        };
        assert_eq!(cells(&txn, &t).len(), 3);
        assert!(!out.contains("<table"));
    }

    #[test]
    fn images_with_and_without_title() {
        for md in [
            "![a cat](https://x.y/c.png)",
            "![a cat](https://x.y/c.png \"The Cat\")",
            "text\n\n![](https://x.y/c.png)\n\nmore",
        ] {
            assert_eq!(rt(md), md);
        }
        let doc = Doc::new();
        seed(&doc, "![a cat](https://x.y/c.png \"The Cat\")");
        let txn = doc.transact();
        let f = txn.get_xml_fragment(FRAGMENT).unwrap();
        let Some(XmlOut::Element(e)) = f.get(&txn, 0) else {
            panic!()
        };
        assert_eq!(&**e.tag(), "image");
        for (k, v) in [
            ("src", "https://x.y/c.png"),
            ("alt", "a cat"),
            ("title", "The Cat"),
        ] {
            assert_eq!(str_of(e.get_attribute(&txn, k)).as_deref(), Some(v));
        }
    }

    #[test]
    fn unsafe_values_are_dropped() {
        // colour: quote + onclick never reaches output; text survives
        assert_eq!(
            rt("<span style=\"color: red&quot; onclick=&quot;x\">hi</span>"),
            "hi"
        );
        assert_eq!(rt("<span style=\"color: #12\">hi</span>"), "hi");
        assert_eq!(rt("<p><span style=\"color: #zzz\">hi</span></p>"), "hi");
        // links
        assert_eq!(rt("[x](javascript:alert(1))"), "x");
        assert_eq!(rt("[x](  JaVaScRiPt:alert(1))"), "x");
        assert_eq!(rt("<p><a href=\"javascript:alert(1)\">x</a></p>"), "x");
        assert_eq!(rt("[x](data:text/html,hi)"), "x");
        for ok in [
            "https://a.b/?q=1&r=2",
            "mailto:a@b.c",
            "/rel/path",
            "#frag",
            "./x",
            "page.html",
        ] {
            assert_eq!(rt(&format!("[x]({ok})")), format!("[x]({ok})"));
        }
        // writer side: a hostile doc never produces a javascript: link, bad colour or data: image
        let doc = Doc::new();
        {
            let mut txn = doc.transact_mut();
            let f = txn.get_or_insert_xml_fragment(FRAGMENT);
            let p = f.push_back(&mut txn, XmlElementPrelim::empty("paragraph"));
            let t = p.push_back(&mut txn, XmlTextPrelim::new(""));
            let obj = |k: &str, v: &str| Any::from(HashMap::from([(k.to_string(), Any::from(v))]));
            let attrs = Attrs::from([
                ("link".into(), obj("href", "javascript:alert(1)")),
                ("textStyle".into(), obj("color", "red\" onclick=\"x")),
            ]);
            t.insert_with_attributes(&mut txn, 0, "evil", attrs);
            let img = f.push_back(&mut txn, XmlElementPrelim::empty("image"));
            img.insert_attribute(&mut txn, "src", str_attr("data:image/png;base64,AAAA"));
        }
        assert_eq!(to_markdown(&doc), "evil");
        // colwidth / span
        let bad = "<table><tr><td colwidth=\"a,2\" colspan=\"-1\"><p>x</p></td></tr></table>";
        assert_eq!(rt(bad), "|     |\n| --- |\n| x   |");
        assert_eq!(
            rt("<table><tr><td colwidth=\"0,5\"><p>x</p></td></tr></table>"),
            "|     |\n| --- |\n| x   |"
        );
        // image
        // not an image block any more; the link rule then strips the unsafe link
        assert_eq!(rt("![x](data:image/png;base64,AAAA)"), "!x");
        assert_eq!(rt("![x](javascript:alert(1))"), "!x");
    }

    #[test]
    fn editor_fixtures_round_trip() {
        let fx = "<h2 style=\"text-align: center\">Head</h2>\n\nship 🚀\n\n**<span style=\"color: #dc2626\">hot</span>**\n\n![cat](https://example.com/cat.png)\n\n<table><tr><th colspan=\"2\"><p>a</p><p>b</p></th><th colwidth=\"244\"><p>c</p></th></tr><tr><td><p></p></td><td><p></p></td><td colwidth=\"244\"><p></p></td></tr></table>";
        assert_eq!(rt(fx), fx);
        let esc = "<span style=\"color: #dc2626\">a \\*b\\* \\_c &amp; &lt;d&gt;</span>";
        assert_eq!(rt(esc), esc);
        assert_eq!(marks_of(esc, "a *b* _c & <d>"), ["textStyle"]);
        // inside HTML blocks only HTML escaping applies
        let h =
            "<p style=\"text-align: right\"><span style=\"color: #dc2626\">a *b* \\ _c</span></p>";
        assert_eq!(rt(h), h);
    }
}
