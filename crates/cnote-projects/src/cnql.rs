//! CNQL: a JQL-compatible query language for items. `parse` builds an AST, `compile` turns it into a SQL
//! `WHERE` fragment plus an ORDER BY with numbered placeholders. Values are never interpolated: every literal
//! becomes a bind, so the generated SQL only ever contains static text and `$n` placeholders.

use chrono::{DateTime, Datelike, Duration, Months, NaiveDate, NaiveDateTime, TimeZone, Utc};
use sqlx::postgres::{PgArguments, Postgres};
use sqlx::query::QueryAs;
use std::fmt;
use uuid::Uuid;

/// The compiled fragments reference these aliases; callers must select from this.
pub const FROM_SQL: &str = "projects.items i join core.notes n on n.id = i.note_id join core.spaces sp on sp.id = i.space_id";
pub const DEFAULT_ORDER: &str = "i.rank collate \"C\" asc, i.number asc";

#[derive(Debug, Clone, PartialEq)]
pub struct CnqlError {
    /// 1-based column in the query text.
    pub pos: usize,
    pub msg: String,
}

impl fmt::Display for CnqlError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} (at column {})", self.msg, self.pos)
    }
}

fn err<T>(pos: usize, msg: impl Into<String>) -> Result<T, CnqlError> {
    Err(CnqlError {
        pos,
        msg: msg.into(),
    })
}

// ---------- lexer ----------

#[derive(Debug, Clone, PartialEq)]
enum Tok {
    Word(String),
    Str(String),
    Eq,
    Ne,
    Gt,
    Ge,
    Lt,
    Le,
    Tilde,
    NotTilde,
    LParen,
    RParen,
    Comma,
    Eof,
}

impl Tok {
    fn describe(&self) -> String {
        match self {
            Tok::Word(w) => format!("'{w}'"),
            Tok::Str(s) => format!("\"{s}\""),
            Tok::Eq => "'='".into(),
            Tok::Ne => "'!='".into(),
            Tok::Gt => "'>'".into(),
            Tok::Ge => "'>='".into(),
            Tok::Lt => "'<'".into(),
            Tok::Le => "'<='".into(),
            Tok::Tilde => "'~'".into(),
            Tok::NotTilde => "'!~'".into(),
            Tok::LParen => "'('".into(),
            Tok::RParen => "')'".into(),
            Tok::Comma => "','".into(),
            Tok::Eof => "end of query".into(),
        }
    }
}

fn is_word_char(c: char) -> bool {
    c.is_alphanumeric() || matches!(c, '_' | '-' | '.' | '@' | '+' | ':' | '/')
}

fn lex(input: &str) -> Result<Vec<(Tok, usize)>, CnqlError> {
    let chars: Vec<char> = input.chars().collect();
    let mut out = vec![];
    let mut i = 0;
    while i < chars.len() {
        let c = chars[i];
        let pos = i + 1;
        let next = chars.get(i + 1).copied();
        match c {
            c if c.is_whitespace() => i += 1,
            '(' => {
                out.push((Tok::LParen, pos));
                i += 1
            }
            ')' => {
                out.push((Tok::RParen, pos));
                i += 1
            }
            ',' => {
                out.push((Tok::Comma, pos));
                i += 1
            }
            '=' => {
                out.push((Tok::Eq, pos));
                i += 1
            }
            '~' => {
                out.push((Tok::Tilde, pos));
                i += 1
            }
            '!' if next == Some('=') => {
                out.push((Tok::Ne, pos));
                i += 2
            }
            '!' if next == Some('~') => {
                out.push((Tok::NotTilde, pos));
                i += 2
            }
            '>' | '<' => {
                let eq = next == Some('=');
                out.push((
                    match (c, eq) {
                        ('>', true) => Tok::Ge,
                        ('>', false) => Tok::Gt,
                        ('<', true) => Tok::Le,
                        _ => Tok::Lt,
                    },
                    pos,
                ));
                i += if eq { 2 } else { 1 }
            }
            '"' | '\'' => {
                let mut s = String::new();
                let mut j = i + 1;
                loop {
                    match chars.get(j) {
                        None => return err(pos, "unterminated string"),
                        Some('\\') if j + 1 < chars.len() => {
                            s.push(chars[j + 1]);
                            j += 2
                        }
                        Some(&q) if q == c => break,
                        Some(&ch) => {
                            s.push(ch);
                            j += 1
                        }
                    }
                }
                out.push((Tok::Str(s), pos));
                i = j + 1
            }
            c if is_word_char(c) => {
                let mut j = i;
                while j < chars.len() && is_word_char(chars[j]) {
                    j += 1
                }
                out.push((Tok::Word(chars[i..j].iter().collect()), pos));
                i = j
            }
            '!' => return err(pos, "'!' must be followed by '=' or '~'"),
            c => return err(pos, format!("unexpected character '{c}'")),
        }
    }
    out.push((Tok::Eof, chars.len() + 1));
    Ok(out)
}

// ---------- AST ----------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Field {
    Project,
    Key,
    Status,
    StatusCategory,
    Assignee,
    Reporter,
    Priority,
    Type,
    Labels,
    Sprint,
    FixVersion,
    Component,
    Created,
    Updated,
    Due,
    Resolved,
    Text,
    /// ORDER BY only.
    Rank,
}

const FIELD_NAMES: &str = "project, key, status, statusCategory, assignee, reporter, priority, type, labels, sprint, fixVersion, component, created, updated, due, resolved, text";

impl Field {
    fn parse(s: &str) -> Option<Self> {
        Some(match s.to_ascii_lowercase().as_str() {
            "project" => Self::Project,
            "key" | "issuekey" => Self::Key,
            "status" => Self::Status,
            "statuscategory" => Self::StatusCategory,
            "assignee" => Self::Assignee,
            "reporter" => Self::Reporter,
            "priority" => Self::Priority,
            "type" | "issuetype" => Self::Type,
            "labels" | "label" => Self::Labels,
            "sprint" => Self::Sprint,
            "fixversion" => Self::FixVersion,
            "component" => Self::Component,
            "created" => Self::Created,
            "updated" => Self::Updated,
            "due" | "duedate" => Self::Due,
            "resolved" | "resolutiondate" => Self::Resolved,
            "text" => Self::Text,
            "rank" => Self::Rank,
            _ => return None,
        })
    }
    fn name(self) -> &'static str {
        match self {
            Self::Project => "project",
            Self::Key => "key",
            Self::Status => "status",
            Self::StatusCategory => "statusCategory",
            Self::Assignee => "assignee",
            Self::Reporter => "reporter",
            Self::Priority => "priority",
            Self::Type => "type",
            Self::Labels => "labels",
            Self::Sprint => "sprint",
            Self::FixVersion => "fixVersion",
            Self::Component => "component",
            Self::Created => "created",
            Self::Updated => "updated",
            Self::Due => "due",
            Self::Resolved => "resolved",
            Self::Text => "text",
            Self::Rank => "rank",
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub enum Val {
    Lit(String),
    Func { name: String, args: Vec<String> },
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Op {
    Eq,
    Ne,
    Gt,
    Ge,
    Lt,
    Le,
    Like,
    NotLike,
}

#[derive(Debug, Clone, PartialEq)]
pub enum Expr {
    And(Vec<Expr>),
    Or(Vec<Expr>),
    Not(Box<Expr>),
    Cmp {
        field: Field,
        op: Op,
        val: (Val, usize),
        pos: usize,
    },
    In {
        field: Field,
        vals: Vec<(Val, usize)>,
        negated: bool,
        pos: usize,
    },
    Empty {
        field: Field,
        negated: bool,
        pos: usize,
    },
}

#[derive(Debug, Clone, PartialEq)]
pub struct Query {
    pub expr: Option<Expr>,
    pub order: Vec<(Field, bool, usize)>, // (field, descending, pos)
}

// ---------- parser ----------

struct Parser {
    toks: Vec<(Tok, usize)>,
    i: usize,
}

impl Parser {
    fn peek(&self) -> &Tok {
        &self.toks[self.i].0
    }
    fn pos(&self) -> usize {
        self.toks[self.i].1
    }
    fn bump(&mut self) -> (Tok, usize) {
        let t = self.toks[self.i].clone();
        if self.i + 1 < self.toks.len() {
            self.i += 1
        }
        t
    }
    fn is_kw(&self, kw: &str) -> bool {
        matches!(self.peek(), Tok::Word(w) if w.eq_ignore_ascii_case(kw))
    }
    fn peek_kw_at(&self, off: usize, kw: &str) -> bool {
        matches!(self.toks.get(self.i + off), Some((Tok::Word(w), _)) if w.eq_ignore_ascii_case(kw))
    }

    fn or_expr(&mut self) -> Result<Expr, CnqlError> {
        let mut parts = vec![self.and_expr()?];
        while self.is_kw("OR") {
            self.bump();
            parts.push(self.and_expr()?);
        }
        Ok(if parts.len() == 1 {
            parts.remove(0)
        } else {
            Expr::Or(parts)
        })
    }

    fn and_expr(&mut self) -> Result<Expr, CnqlError> {
        let mut parts = vec![self.not_expr()?];
        while self.is_kw("AND") {
            self.bump();
            parts.push(self.not_expr()?);
        }
        Ok(if parts.len() == 1 {
            parts.remove(0)
        } else {
            Expr::And(parts)
        })
    }

    fn not_expr(&mut self) -> Result<Expr, CnqlError> {
        if self.is_kw("NOT") {
            self.bump();
            return Ok(Expr::Not(Box::new(self.not_expr()?)));
        }
        self.primary()
    }

    fn primary(&mut self) -> Result<Expr, CnqlError> {
        if *self.peek() == Tok::LParen {
            let open = self.pos();
            self.bump();
            let e = self.or_expr()?;
            if *self.peek() != Tok::RParen {
                return err(
                    self.pos(),
                    format!("expected ')' to close '(' at column {open}"),
                );
            }
            self.bump();
            return Ok(e);
        }
        let (tok, pos) = self.bump();
        let Tok::Word(name) = tok else {
            return err(
                pos,
                format!("expected a field name, found {}", tok.describe()),
            );
        };
        let Some(field) = Field::parse(&name) else {
            return err(
                pos,
                format!("unknown field '{name}'; valid fields: {FIELD_NAMES}"),
            );
        };
        let op_pos = self.pos();
        let op = match self.peek() {
            Tok::Eq => Some(Op::Eq),
            Tok::Ne => Some(Op::Ne),
            Tok::Gt => Some(Op::Gt),
            Tok::Ge => Some(Op::Ge),
            Tok::Lt => Some(Op::Lt),
            Tok::Le => Some(Op::Le),
            Tok::Tilde => Some(Op::Like),
            Tok::NotTilde => Some(Op::NotLike),
            _ => None,
        };
        if let Some(op) = op {
            self.bump();
            let val = self.value()?;
            return Ok(Expr::Cmp {
                field,
                op,
                val,
                pos: op_pos,
            });
        }
        if self.is_kw("IN") || (self.is_kw("NOT") && self.peek_kw_at(1, "IN")) {
            let negated = self.is_kw("NOT");
            if negated {
                self.bump();
            }
            self.bump();
            // JQL allows a bare function: `sprint in openSprints()`.
            if matches!(self.peek(), Tok::Word(_))
                && matches!(self.toks.get(self.i + 1), Some((Tok::LParen, _)))
            {
                let v = self.value()?;
                return Ok(Expr::In {
                    field,
                    vals: vec![v],
                    negated,
                    pos: op_pos,
                });
            }
            if *self.peek() != Tok::LParen {
                return err(
                    self.pos(),
                    format!("expected '(' after IN, found {}", self.peek().describe()),
                );
            }
            self.bump();
            let mut vals = vec![self.value()?];
            while *self.peek() == Tok::Comma {
                self.bump();
                vals.push(self.value()?);
            }
            if *self.peek() != Tok::RParen {
                return err(
                    self.pos(),
                    format!("expected ',' or ')', found {}", self.peek().describe()),
                );
            }
            self.bump();
            return Ok(Expr::In {
                field,
                vals,
                negated,
                pos: op_pos,
            });
        }
        if self.is_kw("IS") {
            self.bump();
            let negated = self.is_kw("NOT");
            if negated {
                self.bump();
            }
            if self.is_kw("EMPTY") || self.is_kw("NULL") {
                self.bump();
                return Ok(Expr::Empty {
                    field,
                    negated,
                    pos: op_pos,
                });
            }
            return err(
                self.pos(),
                format!("expected EMPTY after IS, found {}", self.peek().describe()),
            );
        }
        err(
            op_pos,
            format!(
                "expected an operator (=, !=, >, >=, <, <=, ~, !~, IN, NOT IN, IS EMPTY, IS NOT EMPTY) after '{name}', found {}",
                self.peek().describe()
            ),
        )
    }

    fn value(&mut self) -> Result<(Val, usize), CnqlError> {
        let (tok, pos) = self.bump();
        match tok {
            Tok::Str(s) => Ok((Val::Lit(s), pos)),
            Tok::Word(w) => {
                if *self.peek() == Tok::LParen {
                    self.bump();
                    let mut args = vec![];
                    if *self.peek() != Tok::RParen {
                        loop {
                            let (t, p) = self.bump();
                            match t {
                                Tok::Str(s) | Tok::Word(s) => args.push(s),
                                t => {
                                    return err(
                                        p,
                                        format!(
                                            "expected a function argument, found {}",
                                            t.describe()
                                        ),
                                    );
                                }
                            }
                            if *self.peek() == Tok::Comma {
                                self.bump();
                            } else {
                                break;
                            }
                        }
                    }
                    if *self.peek() != Tok::RParen {
                        return err(self.pos(), format!("expected ')' after arguments of {w}()"));
                    }
                    self.bump();
                    Ok((Val::Func { name: w, args }, pos))
                } else {
                    Ok((Val::Lit(w), pos))
                }
            }
            t => err(pos, format!("expected a value, found {}", t.describe())),
        }
    }
}

pub fn parse(input: &str) -> Result<Query, CnqlError> {
    let mut p = Parser {
        toks: lex(input)?,
        i: 0,
    };
    let expr = if *p.peek() == Tok::Eof || p.is_kw("ORDER") {
        None
    } else {
        Some(p.or_expr()?)
    };
    let mut order = vec![];
    if p.is_kw("ORDER") {
        p.bump();
        if !p.is_kw("BY") {
            return err(p.pos(), "expected BY after ORDER");
        }
        p.bump();
        loop {
            let (tok, pos) = p.bump();
            let Tok::Word(name) = tok else {
                return err(
                    pos,
                    format!("expected a field name, found {}", tok.describe()),
                );
            };
            let Some(f) = Field::parse(&name) else {
                return err(
                    pos,
                    format!("unknown field '{name}'; valid fields: {FIELD_NAMES}, rank"),
                );
            };
            let desc = if p.is_kw("DESC") {
                p.bump();
                true
            } else {
                if p.is_kw("ASC") {
                    p.bump();
                }
                false
            };
            order.push((f, desc, pos));
            if *p.peek() == Tok::Comma {
                p.bump();
            } else {
                break;
            }
        }
    }
    if *p.peek() != Tok::Eof {
        return err(
            p.pos(),
            format!(
                "unexpected {}; expected AND, OR or ORDER BY",
                p.peek().describe()
            ),
        );
    }
    Ok(Query { expr, order })
}

// ---------- compiler ----------

#[derive(Debug, Clone, PartialEq)]
pub enum Bind {
    Text(String),
    TextArr(Vec<String>),
    Uuid(Uuid),
    UuidArr(Vec<Uuid>),
    Ts(DateTime<Utc>),
    Date(NaiveDate),
    Int(i32),
}

impl Bind {
    pub fn apply<'q, O>(
        self,
        q: QueryAs<'q, Postgres, O, PgArguments>,
    ) -> QueryAs<'q, Postgres, O, PgArguments> {
        match self {
            Bind::Text(v) => q.bind(v),
            Bind::TextArr(v) => q.bind(v),
            Bind::Uuid(v) => q.bind(v),
            Bind::UuidArr(v) => q.bind(v),
            Bind::Ts(v) => q.bind(v),
            Bind::Date(v) => q.bind(v),
            Bind::Int(v) => q.bind(v),
        }
    }
}

/// Evaluation context for functions like currentUser() and now().
pub struct Cx {
    pub now: DateTime<Utc>,
    pub user: Option<Uuid>,
}

#[derive(Debug)]
pub struct Compiled {
    /// Boolean SQL expression (`true` for an empty query).
    pub where_sql: String,
    /// Comma-separated ORDER BY terms.
    pub order_sql: String,
    pub binds: Vec<Bind>,
}

struct Gen<'a> {
    cx: &'a Cx,
    first: usize,
    binds: Vec<Bind>,
}

const PRIORITY_RANK: &str = "(case i.priority when 'urgent' then 4 when 'high' then 3 when 'medium' then 2 when 'low' then 1 else 0 end)";

pub fn compile(q: &Query, cx: &Cx, first_param: usize) -> Result<Compiled, CnqlError> {
    let mut g = Gen {
        cx,
        first: first_param,
        binds: vec![],
    };
    let where_sql = match &q.expr {
        Some(e) => g.expr(e)?,
        None => "true".into(),
    };
    let mut terms = vec![];
    for (f, desc, pos) in &q.order {
        let dir = if *desc { "desc" } else { "asc" };
        let col = match f {
            Field::Key => format!("sp.key {dir}, i.number"),
            Field::Created => "n.created_at".into(),
            Field::Updated => "n.updated_at".into(),
            Field::Due => "i.due_date".into(),
            Field::Resolved => "i.resolved_at".into(),
            Field::Priority => PRIORITY_RANK.into(),
            Field::Status => "i.status".into(),
            Field::Rank => "i.rank collate \"C\"".into(),
            f => return err(*pos, format!("cannot ORDER BY '{}'", f.name())),
        };
        terms.push(format!("{col} {dir} nulls last"));
    }
    terms.push("i.number asc".into());
    Ok(Compiled {
        where_sql,
        order_sql: if q.order.is_empty() {
            DEFAULT_ORDER.into()
        } else {
            terms.join(", ")
        },
        binds: g.binds,
    })
}

enum Pred<'a> {
    Eq(Vec<&'a (Val, usize)>),
    Cmp(Op, &'a (Val, usize)),
    Like(&'a (Val, usize)),
    Empty,
}

impl Gen<'_> {
    fn push(&mut self, b: Bind, cast: &str) -> String {
        self.binds.push(b);
        format!("${}::{cast}", self.first + self.binds.len() - 1)
    }

    fn expr(&mut self, e: &Expr) -> Result<String, CnqlError> {
        Ok(match e {
            Expr::And(v) => format!(
                "({})",
                v.iter()
                    .map(|x| self.expr(x))
                    .collect::<Result<Vec<_>, _>>()?
                    .join(" and ")
            ),
            Expr::Or(v) => format!(
                "({})",
                v.iter()
                    .map(|x| self.expr(x))
                    .collect::<Result<Vec<_>, _>>()?
                    .join(" or ")
            ),
            Expr::Not(x) => format!("(not coalesce({}, false))", self.expr(x)?),
            Expr::Cmp {
                field,
                op,
                val,
                pos,
            } => match op {
                Op::Eq => self.pred(*field, Pred::Eq(vec![val]), *pos)?,
                Op::Ne => neg(self.pred(*field, Pred::Eq(vec![val]), *pos)?),
                Op::Like => self.pred(*field, Pred::Like(val), *pos)?,
                Op::NotLike => neg(self.pred(*field, Pred::Like(val), *pos)?),
                op => self.pred(*field, Pred::Cmp(*op, val), *pos)?,
            },
            Expr::In {
                field,
                vals,
                negated,
                pos,
            } => {
                let p = self.pred(*field, Pred::Eq(vals.iter().collect()), *pos)?;
                if *negated { neg(p) } else { p }
            }
            Expr::Empty {
                field,
                negated,
                pos,
            } => {
                let p = self.pred(*field, Pred::Empty, *pos)?;
                if *negated { format!("(not ({p}))") } else { p }
            }
        })
    }

    fn pred(&mut self, f: Field, p: Pred, pos: usize) -> Result<String, CnqlError> {
        let unsupported = |what: &str| {
            err(
                pos,
                format!("operator {what} is not supported for field '{}'", f.name()),
            )
        };
        match f {
            Field::Project | Field::Key | Field::Status | Field::Type => {
                let col = match f {
                    Field::Project => "sp.key",
                    Field::Key => "i.key",
                    Field::Status => "i.status",
                    _ => "i.item_type",
                };
                match p {
                    Pred::Eq(vs) => {
                        let vals = vs
                            .iter()
                            .map(|v| self.plain(f, v, |s| Ok(s.to_lowercase())))
                            .collect::<Result<Vec<_>, _>>()?;
                        let b = self.push(Bind::TextArr(vals), "text[]");
                        Ok(format!("(lower({col}) = any({b}))"))
                    }
                    Pred::Empty => Ok(format!("({col} is null)")),
                    Pred::Cmp(..) => unsupported("<, <=, >, >="),
                    Pred::Like(_) => unsupported("~"),
                }
            }
            Field::StatusCategory => match p {
                Pred::Eq(vs) => {
                    let vals = vs
                        .iter()
                        .map(|v| {
                            self.plain(f, v, |s| {
                                match s.to_lowercase().replace([' ', '-'], "_").as_str() {
                                    "todo" | "to_do" | "open" => Ok("todo".into()),
                                    "in_progress" | "inprogress" => Ok("in_progress".into()),
                                    "done" | "closed" | "complete" => Ok("done".into()),
                                    _ => Err("expected To Do, In Progress or Done".to_string()),
                                }
                            })
                        })
                        .collect::<Result<Vec<_>, _>>()?;
                    let b = self.push(Bind::TextArr(vals), "text[]");
                    Ok(format!("(i.status_category = any({b}))"))
                }
                Pred::Empty => Ok("(i.status_category is null)".into()),
                Pred::Cmp(..) => unsupported("<, <=, >, >="),
                Pred::Like(_) => unsupported("~"),
            },
            Field::Priority => {
                let norm = |s: &str| -> Result<String, String> {
                    match s.to_lowercase().as_str() {
                        "urgent" | "highest" => Ok("urgent".into()),
                        "high" => Ok("high".into()),
                        "medium" => Ok("medium".into()),
                        "low" => Ok("low".into()),
                        "none" | "lowest" => Ok("none".into()),
                        _ => Err("expected Urgent, High, Medium, Low or None".into()),
                    }
                };
                match p {
                    Pred::Eq(vs) => {
                        let vals = vs
                            .iter()
                            .map(|v| self.plain(f, v, norm))
                            .collect::<Result<Vec<_>, _>>()?;
                        let b = self.push(Bind::TextArr(vals), "text[]");
                        Ok(format!("(i.priority = any({b}))"))
                    }
                    Pred::Cmp(op, v) => {
                        let rank = match self.plain(f, v, norm)?.as_str() {
                            "urgent" => 4,
                            "high" => 3,
                            "medium" => 2,
                            "low" => 1,
                            _ => 0,
                        };
                        let b = self.push(Bind::Int(rank), "int");
                        Ok(format!("({PRIORITY_RANK} {} {b})", sql_op(op)))
                    }
                    Pred::Empty => Ok("(i.priority is null)".into()),
                    Pred::Like(_) => unsupported("~"),
                }
            }
            Field::Assignee | Field::Reporter => {
                let assignee = f == Field::Assignee;
                match p {
                    Pred::Eq(vs) => {
                        let (mut ids, mut names) = (vec![], vec![]);
                        for (v, vpos) in vs {
                            match v {
                                Val::Func { name, .. }
                                    if name.eq_ignore_ascii_case("currentUser") =>
                                {
                                    match self.cx.user {
                                        Some(u) => ids.push(u),
                                        None => {
                                            return err(
                                                *vpos,
                                                "currentUser() needs a signed-in user",
                                            );
                                        }
                                    }
                                }
                                Val::Func { name, .. } => {
                                    return err(
                                        *vpos,
                                        format!(
                                            "function {name}() cannot be used with '{}'",
                                            f.name()
                                        ),
                                    );
                                }
                                Val::Lit(s) => match Uuid::parse_str(s) {
                                    Ok(u) => ids.push(u),
                                    Err(_) => names.push(s.to_lowercase()),
                                },
                            }
                        }
                        let mut parts = vec![];
                        let user_col = if assignee {
                            "i.assignee_id"
                        } else {
                            "i.reporter_id"
                        };
                        if !ids.is_empty() {
                            let b = self.push(Bind::UuidArr(ids), "uuid[]");
                            parts.push(format!("{user_col} = any({b})"));
                            if assignee {
                                parts.push(format!("i.assignee_agent_id = any({b})"));
                            }
                        }
                        if !names.is_empty() {
                            let b = self.push(Bind::TextArr(names), "text[]");
                            parts.push(format!(
                                "{user_col} in (select m.user_id from core.memberships m join core.users u on u.id = m.user_id where lower(u.email) = any({b}) or lower(u.name) = any({b}))"
                            ));
                            if assignee {
                                parts.push(format!(
                                    "i.assignee_agent_id in (select a.id from core.agents a where lower(a.name) = any({b}))"
                                ));
                            }
                        }
                        Ok(format!("({})", parts.join(" or ")))
                    }
                    Pred::Empty if assignee => {
                        Ok("(i.assignee_id is null and i.assignee_agent_id is null)".into())
                    }
                    Pred::Empty => Ok("(i.reporter_id is null)".into()),
                    Pred::Cmp(..) => unsupported("<, <=, >, >="),
                    Pred::Like(_) => unsupported("~"),
                }
            }
            Field::Labels => match p {
                Pred::Eq(vs) => {
                    let vals = vs
                        .iter()
                        .map(|v| self.plain(f, v, |s| Ok(s.to_lowercase())))
                        .collect::<Result<Vec<_>, _>>()?;
                    let b = self.push(Bind::TextArr(vals), "text[]");
                    Ok(format!(
                        "(exists (select 1 from unnest(i.labels) l where lower(l) = any({b})))"
                    ))
                }
                Pred::Empty => Ok("(cardinality(i.labels) = 0)".into()),
                Pred::Cmp(..) => unsupported("<, <=, >, >="),
                Pred::Like(_) => unsupported("~"),
            },
            Field::Sprint => self.reference(
                f,
                p,
                pos,
                "i.sprint_id",
                "projects.sprints",
                &[
                    ("openSprints", "state = 'active'"),
                    ("closedSprints", "state = 'closed'"),
                    ("futureSprints", "state = 'future'"),
                ],
            ),
            Field::FixVersion => self.reference(
                f,
                p,
                pos,
                "i.fix_version_id",
                "projects.versions",
                &[
                    ("releasedVersions", "released"),
                    ("unreleasedVersions", "not released"),
                ],
            ),
            Field::Component => {
                self.reference(f, p, pos, "i.component_id", "projects.components", &[])
            }
            Field::Created | Field::Updated | Field::Resolved | Field::Due => {
                let (col, is_date) = match f {
                    Field::Created => ("n.created_at", false),
                    Field::Updated => ("n.updated_at", false),
                    Field::Resolved => ("i.resolved_at", false),
                    _ => ("i.due_date", true),
                };
                match p {
                    Pred::Empty => Ok(format!("({col} is null)")),
                    Pred::Like(_) => unsupported("~"),
                    Pred::Cmp(op, v) => {
                        let (ts, _) = self.date(f, v)?;
                        let b = self.bind_date(ts, is_date);
                        Ok(format!("({col} {} {b})", sql_op(op)))
                    }
                    Pred::Eq(vs) => {
                        let mut parts = vec![];
                        for v in vs {
                            let (ts, day_only) = self.date(f, v)?;
                            if day_only && !is_date {
                                let a = self.bind_date(ts, false);
                                let b = self.bind_date(ts + Duration::days(1), false);
                                parts.push(format!("({col} >= {a} and {col} < {b})"));
                            } else {
                                let b = self.bind_date(ts, is_date);
                                parts.push(format!("{col} = {b}"));
                            }
                        }
                        Ok(format!("({})", parts.join(" or ")))
                    }
                }
            }
            Field::Text => match p {
                Pred::Like(v) => {
                    let s = self.plain(f, v, |s| Ok(s.to_string()))?;
                    let esc = s
                        .replace('\\', "\\\\")
                        .replace('%', "\\%")
                        .replace('_', "\\_");
                    let b = self.push(Bind::Text(format!("%{esc}%")), "text");
                    Ok(format!(
                        "(n.title ilike {b} or n.content_md ilike {b} or i.key ilike {b})"
                    ))
                }
                Pred::Empty => Ok("(n.title = '' and n.content_md = '')".into()),
                _ => unsupported("=, !=, <, >, IN (use ~ for text search)"),
            },
            Field::Rank => err(pos, "'rank' can only be used in ORDER BY"),
        }
    }

    /// A literal value (no functions) run through `norm`.
    fn plain(
        &mut self,
        f: Field,
        v: &(Val, usize),
        norm: impl Fn(&str) -> Result<String, String>,
    ) -> Result<String, CnqlError> {
        match &v.0 {
            Val::Lit(s) => norm(s)
                .or_else(|m| err(v.1, format!("invalid value '{s}' for '{}': {m}", f.name()))),
            Val::Func { name, .. } => err(
                v.1,
                format!("function {name}() cannot be used with '{}'", f.name()),
            ),
        }
    }

    /// sprint / fixVersion / component: match by id, by name, or by a named function.
    fn reference(
        &mut self,
        f: Field,
        p: Pred,
        pos: usize,
        col: &str,
        table: &str,
        funcs: &[(&str, &str)],
    ) -> Result<String, CnqlError> {
        match p {
            Pred::Eq(vs) => {
                let (mut ids, mut names, mut parts) = (vec![], vec![], vec![]);
                for (v, vpos) in vs {
                    match v {
                        Val::Func { name, .. } => match funcs
                            .iter()
                            .find(|(n, _)| n.eq_ignore_ascii_case(name))
                        {
                            Some((_, cond)) => parts
                                .push(format!("{col} in (select id from {table} where {cond})")),
                            None => {
                                return err(
                                    *vpos,
                                    format!("function {name}() cannot be used with '{}'", f.name()),
                                );
                            }
                        },
                        Val::Lit(s) => match Uuid::parse_str(s) {
                            Ok(u) => ids.push(u),
                            Err(_) => names.push(s.to_lowercase()),
                        },
                    }
                }
                if !ids.is_empty() {
                    let b = self.push(Bind::UuidArr(ids), "uuid[]");
                    parts.push(format!("{col} = any({b})"));
                }
                if !names.is_empty() {
                    let b = self.push(Bind::TextArr(names), "text[]");
                    parts.push(format!(
                        "{col} in (select id from {table} where lower(name) = any({b}))"
                    ));
                }
                Ok(format!("({})", parts.join(" or ")))
            }
            Pred::Empty => Ok(format!("({col} is null)")),
            Pred::Cmp(..) => err(
                pos,
                format!(
                    "operator <, <=, >, >= is not supported for field '{}'",
                    f.name()
                ),
            ),
            Pred::Like(_) => err(
                pos,
                format!("operator ~ is not supported for field '{}'", f.name()),
            ),
        }
    }

    fn date(&self, f: Field, v: &(Val, usize)) -> Result<(DateTime<Utc>, bool), CnqlError> {
        let now = self.cx.now;
        let bad = |m: String| err(v.1, format!("invalid date for '{}': {m}", f.name()));
        match &v.0 {
            Val::Func { name, args } => {
                let base = match name.to_ascii_lowercase().as_str() {
                    "now" => now,
                    "startofday" => start_of_day(now),
                    "startofweek" => {
                        start_of_day(now)
                            - Duration::days(now.weekday().num_days_from_monday() as i64)
                    }
                    "startofmonth" => start_of_day(now) - Duration::days(now.day0() as i64),
                    _ => {
                        return bad(format!(
                            "unknown function {name}(); use now(), startOfDay(), startOfWeek() or startOfMonth()"
                        ));
                    }
                };
                match args.first() {
                    None => Ok((base, false)),
                    Some(off) => match relative(base, off) {
                        Some(t) => Ok((t, false)),
                        None => bad(format!("bad offset '{off}', expected e.g. -7d, 2w, 3h")),
                    },
                }
            }
            Val::Lit(s) => {
                if let Some(t) = relative(now, s) {
                    return Ok((t, false));
                }
                for fmt in ["%Y-%m-%d", "%Y/%m/%d"] {
                    if let Ok(d) = NaiveDate::parse_from_str(s, fmt) {
                        return Ok((
                            Utc.from_utc_datetime(&d.and_hms_opt(0, 0, 0).unwrap()),
                            true,
                        ));
                    }
                }
                for fmt in [
                    "%Y-%m-%d %H:%M",
                    "%Y/%m/%d %H:%M",
                    "%Y-%m-%dT%H:%M:%S",
                    "%Y-%m-%d %H:%M:%S",
                ] {
                    if let Ok(d) = NaiveDateTime::parse_from_str(s, fmt) {
                        return Ok((Utc.from_utc_datetime(&d), false));
                    }
                }
                if let Ok(d) = DateTime::parse_from_rfc3339(s) {
                    return Ok((d.with_timezone(&Utc), false));
                }
                bad(format!(
                    "'{s}' is not a date; use YYYY-MM-DD, \"YYYY-MM-DD HH:MM\", a relative value like -7d, or now()"
                ))
            }
        }
    }

    fn bind_date(&mut self, ts: DateTime<Utc>, as_date: bool) -> String {
        if as_date {
            self.push(Bind::Date(ts.date_naive()), "date")
        } else {
            self.push(Bind::Ts(ts), "timestamptz")
        }
    }
}

fn neg(p: String) -> String {
    format!("(not coalesce({p}, false))")
}

fn sql_op(op: Op) -> &'static str {
    match op {
        Op::Gt => ">",
        Op::Ge => ">=",
        Op::Lt => "<",
        Op::Le => "<=",
        Op::Ne => "<>",
        _ => "=",
    }
}

fn start_of_day(t: DateTime<Utc>) -> DateTime<Utc> {
    Utc.from_utc_datetime(&t.date_naive().and_hms_opt(0, 0, 0).unwrap())
}

/// `-7d`, `+2w`, `3h`: m minutes, h hours, d days, w weeks, M months, y years.
fn relative(base: DateTime<Utc>, s: &str) -> Option<DateTime<Utc>> {
    let unit = s.chars().last()?;
    let num = s[..s.len() - unit.len_utf8()].parse::<i64>().ok()?;
    match unit {
        'm' => base.checked_add_signed(Duration::minutes(num)),
        'h' => base.checked_add_signed(Duration::hours(num)),
        'd' => base.checked_add_signed(Duration::days(num)),
        'w' => base.checked_add_signed(Duration::weeks(num)),
        'M' | 'y' => {
            let months =
                u32::try_from(num.unsigned_abs() * if unit == 'y' { 12 } else { 1 }).ok()?;
            if num < 0 {
                base.checked_sub_months(Months::new(months))
            } else {
                base.checked_add_months(Months::new(months))
            }
        }
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cx() -> Cx {
        Cx {
            now: Utc.with_ymd_and_hms(2026, 3, 15, 10, 30, 0).unwrap(),
            user: Some(Uuid::nil()),
        }
    }
    fn c(q: &str) -> Compiled {
        compile(&parse(q).unwrap(), &cx(), 3).unwrap()
    }
    fn perr(q: &str) -> CnqlError {
        parse(q).unwrap_err()
    }
    fn cerr(q: &str) -> CnqlError {
        compile(&parse(q).unwrap(), &cx(), 1).unwrap_err()
    }

    #[test]
    fn empty_and_order_only() {
        assert_eq!(c("").where_sql, "true");
        assert_eq!(c("   ").order_sql, DEFAULT_ORDER);
        let o = c("ORDER BY updated DESC, key");
        assert_eq!(o.where_sql, "true");
        assert!(
            o.order_sql
                .starts_with("n.updated_at desc nulls last, sp.key asc, i.number asc nulls last")
        );
    }

    #[test]
    fn simple_equality_is_parameterised() {
        let r = c("project = ENG AND status = \"In Progress\"");
        assert_eq!(
            r.where_sql,
            "((lower(sp.key) = any($3::text[])) and (lower(i.status) = any($4::text[])))"
        );
        assert_eq!(
            r.binds,
            vec![
                Bind::TextArr(vec!["eng".into()]),
                Bind::TextArr(vec!["in progress".into()])
            ]
        );
    }

    #[test]
    fn injection_attempt_stays_a_bind() {
        let r = c("status = \"x'; drop table core.notes; --\"");
        assert!(!r.where_sql.contains("drop"));
        assert_eq!(r.binds.len(), 1);
        let r = c("text ~ \"100%_\\\\\"");
        assert!(!r.where_sql.contains("100"));
        assert_eq!(r.binds, vec![Bind::Text("%100\\%\\_\\\\%".into())]);
    }

    #[test]
    fn precedence_not_and_parens() {
        let r = c("status = a OR status = b AND NOT type = bug");
        assert!(
            r.where_sql
                .starts_with("((lower(i.status) = any($3::text[])) or (")
        );
        assert!(r.where_sql.contains("(not coalesce("));
        let r = c("(status = a OR status = b) AND type = bug");
        assert!(
            r.where_sql
                .starts_with("(((lower(i.status) = any($3::text[])) or")
        );
    }

    #[test]
    fn negations_keep_unassigned_rows() {
        let r = c("assignee != currentUser()");
        assert!(r.where_sql.starts_with("(not coalesce("));
        assert_eq!(r.binds, vec![Bind::UuidArr(vec![Uuid::nil()])]);
        let r = c("status NOT IN (Done, Cancelled)");
        assert!(r.where_sql.starts_with("(not coalesce("));
        assert_eq!(
            r.binds,
            vec![Bind::TextArr(vec!["done".into(), "cancelled".into()])]
        );
    }

    #[test]
    fn empty_operators() {
        assert_eq!(
            c("assignee IS EMPTY").where_sql,
            "(i.assignee_id is null and i.assignee_agent_id is null)"
        );
        assert_eq!(
            c("assignee is not empty").where_sql,
            "(not ((i.assignee_id is null and i.assignee_agent_id is null)))"
        );
        assert_eq!(
            c("labels IS EMPTY").where_sql,
            "(cardinality(i.labels) = 0)"
        );
        assert_eq!(c("due IS NULL").where_sql, "(i.due_date is null)");
    }

    #[test]
    fn assignee_by_name_or_id() {
        let id = Uuid::new_v4();
        let r = c(&format!("assignee IN (\"Tess\", {id})"));
        assert_eq!(r.binds.len(), 2);
        assert_eq!(r.binds[0], Bind::UuidArr(vec![id]));
        assert_eq!(r.binds[1], Bind::TextArr(vec!["tess".into()]));
        assert!(r.where_sql.contains("assignee_agent_id"));
        let r = c("reporter = currentUser()");
        assert!(!r.where_sql.contains("assignee"));
    }

    #[test]
    fn status_category_and_priority() {
        let r = c("statusCategory = \"To Do\"");
        assert_eq!(r.binds, vec![Bind::TextArr(vec!["todo".into()])]);
        let r = c("statusCategory IN (\"In Progress\", Done)");
        assert_eq!(
            r.binds,
            vec![Bind::TextArr(vec!["in_progress".into(), "done".into()])]
        );
        assert!(
            cerr("statusCategory = Weird")
                .msg
                .contains("To Do, In Progress or Done")
        );
        let r = c("priority >= High");
        assert_eq!(r.binds, vec![Bind::Int(3)]);
        assert!(r.where_sql.contains(">= $3::int"));
        let r = c("priority = Highest");
        assert_eq!(r.binds, vec![Bind::TextArr(vec!["urgent".into()])]);
        assert!(cerr("priority = Spicy").msg.contains("Urgent, High"));
    }

    #[test]
    fn sprint_functions_and_names() {
        let r = c("sprint in openSprints()");
        assert!(
            r.where_sql
                .contains("from projects.sprints where state = 'active'")
        );
        assert!(r.binds.is_empty());
        let r = c("sprint IN (closedSprints(), \"Sprint 4\")");
        assert!(r.where_sql.contains("state = 'closed'"));
        assert_eq!(r.binds, vec![Bind::TextArr(vec!["sprint 4".into()])]);
        assert!(
            c("sprint IS EMPTY")
                .where_sql
                .contains("i.sprint_id is null")
        );
        assert!(
            cerr("component = openSprints()")
                .msg
                .contains("cannot be used")
        );
    }

    #[test]
    fn labels_fix_version_component() {
        let r = c("labels = urgent AND fixVersion = \"1.0\" AND component = Api");
        assert_eq!(r.binds.len(), 3);
        assert!(r.where_sql.contains("unnest(i.labels)"));
        assert!(r.where_sql.contains("projects.versions"));
        assert!(r.where_sql.contains("projects.components"));
    }

    #[test]
    fn dates_relative_and_absolute() {
        let r = c("created >= -7d");
        let want = Utc.with_ymd_and_hms(2026, 3, 8, 10, 30, 0).unwrap();
        assert_eq!(r.binds, vec![Bind::Ts(want)]);
        let r = c("updated < -2w");
        assert_eq!(
            r.binds,
            vec![Bind::Ts(
                Utc.with_ymd_and_hms(2026, 3, 1, 10, 30, 0).unwrap()
            )]
        );
        let r = c("created > startOfDay()");
        assert_eq!(
            r.binds,
            vec![Bind::Ts(
                Utc.with_ymd_and_hms(2026, 3, 15, 0, 0, 0).unwrap()
            )]
        );
        let r = c("created > startOfDay(\"-1d\")");
        assert_eq!(
            r.binds,
            vec![Bind::Ts(
                Utc.with_ymd_and_hms(2026, 3, 14, 0, 0, 0).unwrap()
            )]
        );
        let r = c("resolved <= now()");
        assert_eq!(r.binds, vec![Bind::Ts(cx().now)]);
        let r = c("created > startOfWeek() AND created > startOfMonth()");
        assert_eq!(
            r.binds[0],
            Bind::Ts(Utc.with_ymd_and_hms(2026, 3, 9, 0, 0, 0).unwrap())
        );
        assert_eq!(
            r.binds[1],
            Bind::Ts(Utc.with_ymd_and_hms(2026, 3, 1, 0, 0, 0).unwrap())
        );
        let r = c("created > -1M");
        assert_eq!(
            r.binds,
            vec![Bind::Ts(
                Utc.with_ymd_and_hms(2026, 2, 15, 10, 30, 0).unwrap()
            )]
        );
    }

    #[test]
    fn due_is_a_date_and_day_equality_is_a_range() {
        let r = c("due < 2026-04-01");
        assert_eq!(
            r.binds,
            vec![Bind::Date(NaiveDate::from_ymd_opt(2026, 4, 1).unwrap())]
        );
        assert!(r.where_sql.contains("::date"));
        let r = c("created = 2026-04-01");
        assert_eq!(r.binds.len(), 2);
        assert!(
            r.where_sql
                .contains(">= $3::timestamptz and n.created_at < $4::timestamptz")
        );
        let r = c("created = \"2026-04-01 09:00\"");
        assert_eq!(r.binds.len(), 1);
    }

    #[test]
    fn text_search() {
        let r = c("text ~ login");
        assert!(r.where_sql.contains("n.title ilike $3::text"));
        assert_eq!(r.binds, vec![Bind::Text("%login%".into())]);
        assert!(c("text !~ login").where_sql.starts_with("(not coalesce("));
        assert!(cerr("text = login").msg.contains("use ~"));
    }

    #[test]
    fn placeholder_offset() {
        let r = compile(&parse("status = a AND type = b").unwrap(), &cx(), 7).unwrap();
        assert!(r.where_sql.contains("$7::text[]") && r.where_sql.contains("$8::text[]"));
    }

    #[test]
    fn keywords_are_case_insensitive_and_fields_have_aliases() {
        let r = c("Issuetype = bug and Project in (eng, ops) order by Priority desc");
        assert_eq!(r.binds.len(), 2);
        assert!(r.order_sql.contains("desc nulls last"));
    }

    #[test]
    fn order_by_rank_and_rejects() {
        assert!(
            c("project = ENG ORDER BY rank")
                .order_sql
                .contains("i.rank collate \"C\" asc")
        );
        assert!(
            cerr("ORDER BY labels")
                .msg
                .contains("cannot ORDER BY 'labels'")
        );
        assert!(cerr("rank = a").msg.contains("only be used in ORDER BY"));
    }

    #[test]
    fn errors_have_positions() {
        let e = perr("status = ");
        assert_eq!(
            (e.pos, e.msg.as_str()),
            (10, "expected a value, found end of query")
        );
        let e = perr("stauts = Done");
        assert_eq!(e.pos, 1);
        assert!(e.msg.contains("unknown field 'stauts'") && e.msg.contains("statusCategory"));
        let e = perr("status = Done AND");
        assert_eq!(e.pos, 18);
        let e = perr("status Done");
        assert!(e.msg.contains("expected an operator") && e.pos == 8, "{e}");
        let e = perr("(status = Done");
        assert!(
            e.msg.contains("expected ')'") && e.msg.contains("column 1"),
            "{e}"
        );
        let e = perr("status IN Done");
        assert!(e.msg.contains("expected '(' after IN"), "{e}");
        let e = perr("status IN (a, b");
        assert!(e.msg.contains("expected ',' or ')'"), "{e}");
        let e = perr("status = \"abc");
        assert_eq!((e.pos, e.msg.as_str()), (10, "unterminated string"));
        let e = perr("status = Done foo");
        assert!(e.msg.contains("unexpected 'foo'") && e.pos == 15, "{e}");
        let e = perr("status = a # b");
        assert!(e.msg.contains("unexpected character '#'"), "{e}");
        let e = perr("status ! a");
        assert!(e.msg.contains("'!' must be followed"), "{e}");
        let e = perr("assignee IS FULL");
        assert!(e.msg.contains("expected EMPTY"), "{e}");
        let e = perr("ORDER status");
        assert!(e.msg.contains("expected BY"), "{e}");
        let e = perr("status = f(");
        assert!(e.msg.contains("function argument"), "{e}");
        assert!(format!("{}", perr("= a")).contains("at column 1"));
    }

    #[test]
    fn compile_errors() {
        assert!(cerr("priority ~ high").msg.contains("not supported"));
        assert!(cerr("status > a").msg.contains("not supported"));
        assert!(cerr("created = banana").msg.contains("is not a date"));
        assert!(
            cerr("created > nope()")
                .msg
                .contains("unknown function nope()")
        );
        assert!(cerr("created > -7x").msg.contains("is not a date"));
        assert!(cerr("assignee = now()").msg.contains("cannot be used"));
        let no_user = Cx {
            now: cx().now,
            user: None,
        };
        let e = compile(&parse("assignee = currentUser()").unwrap(), &no_user, 1).unwrap_err();
        assert!(e.msg.contains("signed-in user"));
    }
}
