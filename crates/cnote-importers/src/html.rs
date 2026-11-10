//! Small lenient HTML -> Markdown converter for Confluence storage format and HTML exports.
//! Covers headings, paragraphs, lists, links, images, code, quotes, tables, bold/italic. Unknown tags keep their text.

enum Node {
    Text(String),
    El {
        tag: String,
        attrs: Vec<(String, String)>,
        kids: Vec<Node>,
    },
}

const VOID: [&str; 6] = ["br", "hr", "img", "meta", "link", "input"];

fn decode(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut rest = s;
    while let Some(i) = rest.find('&') {
        out.push_str(&rest[..i]);
        rest = &rest[i..];
        let Some(end) = rest.find(';').filter(|e| *e <= 10) else {
            out.push('&');
            rest = &rest[1..];
            continue;
        };
        let ent = &rest[1..end];
        let ch = match ent {
            "amp" => Some('&'),
            "lt" => Some('<'),
            "gt" => Some('>'),
            "quot" => Some('"'),
            "apos" => Some('\''),
            "nbsp" => Some(' '),
            _ => ent
                .strip_prefix('#')
                .and_then(|n| match n.strip_prefix(['x', 'X']) {
                    Some(h) => u32::from_str_radix(h, 16).ok(),
                    None => n.parse().ok(),
                })
                .and_then(char::from_u32),
        };
        match ch {
            Some(c) => {
                out.push(c);
                rest = &rest[end + 1..];
            }
            None => {
                out.push('&');
                rest = &rest[1..];
            }
        }
    }
    out.push_str(rest);
    out
}

fn parse(html: &str) -> Vec<Node> {
    // Stack of open elements; index 0 is a synthetic root.
    let mut stack: Vec<(String, Vec<(String, String)>, Vec<Node>)> =
        vec![("".into(), vec![], vec![])];
    fn close(stack: &mut Vec<(String, Vec<(String, String)>, Vec<Node>)>) {
        let (tag, attrs, kids) = stack.pop().unwrap();
        stack
            .last_mut()
            .unwrap()
            .2
            .push(Node::El { tag, attrs, kids });
    }
    let mut i = 0;
    while i < html.len() {
        let rest = &html[i..];
        if !rest.starts_with('<') {
            let end = rest.find('<').unwrap_or(rest.len());
            stack
                .last_mut()
                .unwrap()
                .2
                .push(Node::Text(decode(&rest[..end])));
            i += end;
        } else if rest.starts_with("<!--") {
            i += rest.find("-->").map_or(rest.len(), |e| e + 3);
        } else if let Some(body) = rest.strip_prefix("<![CDATA[") {
            let end = body.find("]]>").unwrap_or(body.len());
            // CDATA is literal text: keep it from entity decoding.
            stack
                .last_mut()
                .unwrap()
                .2
                .push(Node::Text(body[..end].to_string()));
            i += 9 + end + 3;
        } else if rest.starts_with("<!") || rest.starts_with("<?") {
            i += rest.find('>').map_or(rest.len(), |e| e + 1);
        } else if let Some(r) = rest.strip_prefix("</") {
            let end = r.find('>').unwrap_or(r.len());
            let name = r[..end].trim().to_lowercase();
            if stack.iter().skip(1).any(|(t, _, _)| *t == name) {
                while stack.len() > 1 {
                    let done = stack.last().unwrap().0 == name;
                    close(&mut stack);
                    if done {
                        break;
                    }
                }
            }
            i += 2 + end + 1;
        } else {
            let Some(end) = tag_end(rest) else {
                stack
                    .last_mut()
                    .unwrap()
                    .2
                    .push(Node::Text(rest.to_string()));
                break;
            };
            let inner = &rest[1..end];
            let self_closing = inner.ends_with('/');
            let (tag, attrs) = parse_tag(inner.trim_end_matches('/'));
            i += end + 1;
            if tag.is_empty() {
                stack.last_mut().unwrap().2.push(Node::Text("<".into()));
                continue;
            }
            if tag == "script" || tag == "style" {
                let close_tag = format!("</{tag}");
                let skip = html[i..]
                    .to_lowercase()
                    .find(&close_tag)
                    .and_then(|p| html[i + p..].find('>').map(|g| p + g + 1))
                    .unwrap_or(html.len() - i);
                i += skip;
                continue;
            }
            if self_closing || VOID.contains(&tag.as_str()) {
                stack.last_mut().unwrap().2.push(Node::El {
                    tag,
                    attrs,
                    kids: vec![],
                });
            } else {
                // <li> and <p> implicitly close an open sibling.
                if (tag == "li" || tag == "p") && stack.last().is_some_and(|(t, _, _)| *t == tag) {
                    close(&mut stack);
                }
                stack.push((tag, attrs, vec![]));
            }
        }
    }
    while stack.len() > 1 {
        close(&mut stack);
    }
    stack.pop().unwrap().2
}

/// Index of the `>` that ends the tag at the start of `s`, skipping quoted attribute values.
fn tag_end(s: &str) -> Option<usize> {
    let mut quote = None;
    for (i, c) in s.char_indices().skip(1) {
        match (quote, c) {
            (None, '"' | '\'') => quote = Some(c),
            (Some(q), c) if c == q => quote = None,
            (None, '>') => return Some(i),
            _ => {}
        }
    }
    None
}

fn parse_tag(inner: &str) -> (String, Vec<(String, String)>) {
    let inner = inner.trim();
    let name_end = inner
        .find(|c: char| c.is_whitespace())
        .unwrap_or(inner.len());
    let tag = inner[..name_end].to_lowercase();
    let mut attrs = vec![];
    let mut rest = inner[name_end..].trim_start();
    while !rest.is_empty() {
        let key_end = rest
            .find(|c: char| c == '=' || c.is_whitespace())
            .unwrap_or(rest.len());
        let key = rest[..key_end].to_lowercase();
        rest = rest[key_end..].trim_start();
        let mut val = String::new();
        if let Some(r) = rest.strip_prefix('=') {
            let r = r.trim_start();
            if let Some(q) = r.chars().next().filter(|c| *c == '"' || *c == '\'') {
                let body = &r[1..];
                let e = body.find(q).unwrap_or(body.len());
                val = decode(&body[..e]);
                rest = body.get(e + 1..).unwrap_or("").trim_start();
            } else {
                let e = r.find(char::is_whitespace).unwrap_or(r.len());
                val = decode(&r[..e]);
                rest = r[e..].trim_start();
            }
        }
        if !key.is_empty() {
            attrs.push((key, val));
        }
    }
    (tag, attrs)
}

fn attr<'a>(attrs: &'a [(String, String)], k: &str) -> Option<&'a str> {
    attrs.iter().find(|(n, _)| n == k).map(|(_, v)| v.as_str())
}

fn raw_text(nodes: &[Node], out: &mut String) {
    for n in nodes {
        match n {
            Node::Text(t) => out.push_str(t),
            Node::El { tag, kids, .. } => {
                if tag == "br" {
                    out.push('\n');
                }
                raw_text(kids, out);
            }
        }
    }
}

struct Conv {
    macros_dropped: u32,
}

impl Conv {
    fn inline(&mut self, nodes: &[Node]) -> String {
        let mut out = String::new();
        for n in nodes {
            match n {
                Node::Text(t) => {
                    let mut prev_space = out.ends_with(' ');
                    for c in t.chars() {
                        if c.is_whitespace() {
                            if !prev_space {
                                out.push(' ');
                            }
                            prev_space = true;
                        } else {
                            out.push(c);
                            prev_space = false;
                        }
                    }
                }
                Node::El { tag, attrs, kids } => {
                    let inner = self.inline(kids);
                    let wrap = |m: &str| {
                        if inner.trim().is_empty() {
                            inner.clone()
                        } else {
                            format!("{m}{}{m}", inner.trim())
                        }
                    };
                    match tag.as_str() {
                        "strong" | "b" => out.push_str(&wrap("**")),
                        "em" | "i" => out.push_str(&wrap("*")),
                        "del" | "s" => out.push_str(&wrap("~~")),
                        "code" => out.push_str(&wrap("`")),
                        "br" => out.push_str("  \n"),
                        "a" => match attr(attrs, "href") {
                            Some(h) if !inner.trim().is_empty() => {
                                out.push_str(&format!("[{}]({h})", inner.trim()))
                            }
                            _ => out.push_str(&inner),
                        },
                        "img" => out.push_str(&format!(
                            "![{}]({})",
                            attr(attrs, "alt").unwrap_or(""),
                            attr(attrs, "src").unwrap_or("")
                        )),
                        "ac:structured-macro" => out.push_str(&self.macro_inline(attrs, kids)),
                        "ac:parameter" => {}
                        _ => out.push_str(&inner),
                    }
                }
            }
        }
        out
    }

    fn macro_inline(&mut self, attrs: &[(String, String)], kids: &[Node]) -> String {
        if attr(attrs, "ac:name") != Some("code") {
            self.macros_dropped += 1;
        }
        self.inline(kids)
    }

    fn block(&mut self, nodes: &[Node], out: &mut String, indent: &str) {
        let mut buf = String::new();
        let flush = |buf: &mut String, out: &mut String| {
            let t = buf.trim();
            if !t.is_empty() {
                out.push_str(indent);
                out.push_str(t);
                out.push_str("\n\n");
            }
            buf.clear();
        };
        for n in nodes {
            let Node::El { tag, attrs, kids } = n else {
                buf.push_str(&self.inline(std::slice::from_ref(n)));
                continue;
            };
            match tag.as_str() {
                "h1" | "h2" | "h3" | "h4" | "h5" | "h6" => {
                    flush(&mut buf, out);
                    let level = tag[1..].parse::<usize>().unwrap_or(1);
                    out.push_str(&format!(
                        "{} {}\n\n",
                        "#".repeat(level),
                        self.inline(kids).trim()
                    ));
                }
                "p" => {
                    flush(&mut buf, out);
                    buf = self.inline(kids);
                    flush(&mut buf, out);
                }
                "ul" | "ol" => {
                    flush(&mut buf, out);
                    self.list(tag == "ol", kids, out, indent);
                    out.push('\n');
                }
                "pre" => {
                    flush(&mut buf, out);
                    let lang = kids
                        .iter()
                        .find_map(|k| match k {
                            Node::El { attrs, .. } => attr(attrs, "class").and_then(|c| {
                                c.split_whitespace()
                                    .find_map(|w| w.strip_prefix("language-"))
                            }),
                            _ => None,
                        })
                        .unwrap_or("");
                    let mut code = String::new();
                    raw_text(kids, &mut code);
                    out.push_str(&format!("```{lang}\n{}\n```\n\n", code.trim_matches('\n')));
                }
                "ac:structured-macro" if attr(attrs, "ac:name") == Some("code") => {
                    flush(&mut buf, out);
                    let lang = kids
                        .iter()
                        .find_map(|k| match k {
                            Node::El {
                                tag,
                                attrs: a,
                                kids,
                            } if tag == "ac:parameter"
                                && attr(a, "ac:name") == Some("language") =>
                            {
                                let mut s = String::new();
                                raw_text(kids, &mut s);
                                Some(s)
                            }
                            _ => None,
                        })
                        .unwrap_or_default();
                    let mut code = String::new();
                    for k in kids {
                        if let Node::El { tag, kids, .. } = k
                            && tag == "ac:plain-text-body"
                        {
                            raw_text(kids, &mut code);
                        }
                    }
                    out.push_str(&format!("```{lang}\n{}\n```\n\n", code.trim_matches('\n')));
                }
                "blockquote" => {
                    flush(&mut buf, out);
                    let mut inner = String::new();
                    self.block(kids, &mut inner, "");
                    for l in inner.trim_end().lines() {
                        out.push_str(&format!("> {l}\n").replace("> \n", ">\n"));
                    }
                    out.push('\n');
                }
                "table" => {
                    flush(&mut buf, out);
                    self.table(kids, out);
                }
                "hr" => {
                    flush(&mut buf, out);
                    out.push_str("---\n\n");
                }
                "head" | "title" | "meta" | "link" | "ac:parameter" => {}
                "div" | "section" | "article" | "main" | "body" | "html" | "header" | "footer"
                | "ac:rich-text-body" | "ac:layout" | "ac:layout-section" | "ac:layout-cell"
                | "details" | "figure" => {
                    flush(&mut buf, out);
                    self.block(kids, out, indent);
                }
                "ac:structured-macro" => {
                    flush(&mut buf, out);
                    self.macros_dropped += 1;
                    self.block(kids, out, indent);
                }
                _ => buf.push_str(&self.inline(std::slice::from_ref(n))),
            }
        }
        flush(&mut buf, out);
    }

    fn list(&mut self, ordered: bool, items: &[Node], out: &mut String, indent: &str) {
        let mut n = 0;
        for it in items {
            let Node::El { tag, kids, .. } = it else {
                continue;
            };
            if tag != "li" {
                continue;
            }
            n += 1;
            let (nested, rest): (Vec<&Node>, Vec<&Node>) = kids
                .iter()
                .partition(|k| matches!(k, Node::El { tag, .. } if tag == "ul" || tag == "ol"));
            let text = {
                let mut s = String::new();
                for k in rest {
                    s.push_str(&self.inline(std::slice::from_ref(k)));
                }
                s
            };
            let marker = if ordered {
                format!("{n}. ")
            } else {
                "- ".to_string()
            };
            out.push_str(&format!("{indent}{marker}{}\n", text.trim()));
            for k in nested {
                if let Node::El { tag, kids, .. } = k {
                    self.list(tag == "ol", kids, out, &format!("{indent}  "));
                }
            }
        }
    }

    fn table(&mut self, nodes: &[Node], out: &mut String) {
        fn rows<'a>(nodes: &'a [Node], acc: &mut Vec<&'a [Node]>) {
            for n in nodes {
                if let Node::El { tag, kids, .. } = n {
                    match tag.as_str() {
                        "tr" => acc.push(kids),
                        "thead" | "tbody" | "tfoot" => rows(kids, acc),
                        _ => {}
                    }
                }
            }
        }
        let mut rs = vec![];
        rows(nodes, &mut rs);
        let cells: Vec<Vec<String>> = rs
            .iter()
            .map(|r| {
                r.iter()
                    .filter_map(|c| match c {
                        Node::El { tag, kids, .. } if tag == "td" || tag == "th" => Some(
                            self.inline(kids)
                                .trim()
                                .replace('|', "\\|")
                                .replace('\n', " "),
                        ),
                        _ => None,
                    })
                    .collect()
            })
            .filter(|r: &Vec<String>| !r.is_empty())
            .collect();
        let Some(width) = cells.iter().map(Vec::len).max() else {
            return;
        };
        for (i, r) in cells.iter().enumerate() {
            let mut r = r.clone();
            r.resize(width, String::new());
            out.push_str(&format!("| {} |\n", r.join(" | ")));
            if i == 0 {
                out.push_str(&format!("|{}\n", " --- |".repeat(width)));
            }
        }
        out.push('\n');
    }
}

/// Returns (markdown, number of unsupported macros dropped).
pub fn html_to_md(html: &str) -> (String, u32) {
    let tree = parse(html);
    let mut c = Conv { macros_dropped: 0 };
    let mut out = String::new();
    c.block(&tree, &mut out, "");
    (out.trim().to_string(), c.macros_dropped)
}

/// Text of the first element with this tag (used for page titles).
pub fn first_text(html: &str, tag: &str) -> Option<String> {
    fn find(nodes: &[Node], tag: &str) -> Option<String> {
        for n in nodes {
            if let Node::El { tag: t, kids, .. } = n {
                if t == tag {
                    let mut s = String::new();
                    raw_text(kids, &mut s);
                    return Some(s.trim().to_string());
                }
                if let Some(s) = find(kids, tag) {
                    return Some(s);
                }
            }
        }
        None
    }
    find(&parse(html), tag).filter(|s| !s.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn converts_common_structures() {
        let html = r#"<h1>Runbook</h1><p>Restart the <strong>pods</strong> &amp; <em>wait</em>, see <a href="https://example.com/x">docs</a>.</p>
<ul><li>one<ul><li>nested</li></ul></li><li>two</li></ul><ol><li>a</li><li>b</li></ol>
<pre><code class="language-sh">kubectl get pods
</code></pre><blockquote><p>careful</p></blockquote>
<table><tr><th>Env</th><th>Host</th></tr><tr><td>prod</td><td>a|b</td></tr></table>"#;
        let (md, dropped) = html_to_md(html);
        assert_eq!(dropped, 0);
        assert!(md.starts_with(
            "# Runbook\n\nRestart the **pods** & *wait*, see [docs](https://example.com/x)."
        ));
        assert!(md.contains("- one\n  - nested\n- two"));
        assert!(md.contains("1. a\n2. b"));
        assert!(md.contains("```sh\nkubectl get pods\n```"));
        assert!(md.contains("> careful"));
        assert!(md.contains("| Env | Host |\n| --- | --- |\n| prod | a\\|b |"));
    }

    #[test]
    fn confluence_code_macro_and_unknown_macro() {
        let html = r#"<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">sql</ac:parameter><ac:plain-text-body><![CDATA[select 1 < 2;]]></ac:plain-text-body></ac:structured-macro>
<ac:structured-macro ac:name="toc"/><p>after</p>"#;
        let (md, dropped) = html_to_md(html);
        assert!(md.contains("```sql\nselect 1 < 2;\n```"), "{md}");
        assert!(md.ends_with("after"));
        assert_eq!(dropped, 1);
    }

    #[test]
    fn title_and_script_skipping() {
        let html = "<html><head><title>My Page</title><script>var a = '<b>';</script></head><body><p>hi</p></body></html>";
        assert_eq!(first_text(html, "title").as_deref(), Some("My Page"));
        assert_eq!(html_to_md(html).0, "hi");
    }
}
