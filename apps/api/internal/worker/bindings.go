package worker

import (
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
)

// Port of crates/cnote-docs/src/bindings.rs: what a page claims to describe, and the handler that keeps its
// `binds` relations and docs.external_bindings in sync.

type bindingKind int

const (
	bItemKey bindingKind = iota
	bNoteLink
	bWikiLink
	bGithub
	bRepo
	bURL
)

type binding struct {
	kind bindingKind
	val  string
}

const uuidPat = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

var (
	reNote = regexp.MustCompile(`cnote://[^` + wsClass + `)>\]]*?/note/(` + uuidPat + `)`)
	reURL  = regexp.MustCompile(`https?://[^` + wsClass + `)>\]]+`)
	// Rust wraps these in \b; boundaries are checked by hand because Go's \b is ASCII only.
	reRepo = regexp.MustCompile(`repo:([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[^` + wsClass + `)>\],;]*)?)`)
	reWiki = regexp.MustCompile(`\[\[([^\[\]|]+)(?:\|[^\]]*)?\]\]`)
	reKey  = regexp.MustCompile(`[A-Z][A-Z0-9]{1,9}-[1-9][0-9]*`)
)

type found struct {
	pos int
	b   binding
}

// take blanks every match of r in rest (same byte length) and collects the bindings f accepts.
func take(r *regexp.Regexp, rest *string, out *[]found, f func(m []string) (binding, bool), startBoundary, endBoundary bool) {
	s := *rest
	var sb strings.Builder
	last := 0
	for _, loc := range r.FindAllStringSubmatchIndex(s, -1) {
		if (startBoundary && !boundaryBefore(s, loc[0])) || (endBoundary && !boundaryAfter(s, loc[1])) {
			continue
		}
		m := make([]string, len(loc)/2)
		for i := range m {
			if loc[2*i] >= 0 {
				m[i] = s[loc[2*i]:loc[2*i+1]]
			}
		}
		sb.WriteString(s[last:loc[0]])
		sb.WriteString(strings.Repeat(" ", loc[1]-loc[0]))
		last = loc[1]
		if b, ok := f(m); ok {
			*out = append(*out, found{loc[0], b})
		}
	}
	sb.WriteString(s[last:])
	*rest = sb.String()
}

// parseBindings extracts bindings in document order, deduplicated; stricter constructs are consumed first.
func parseBindings(md string) []binding {
	var fs []found
	rest := md
	take(reNote, &rest, &fs, func(m []string) (binding, bool) { return binding{bNoteLink, strings.ToLower(m[1])}, true }, false, false)
	take(reURL, &rest, &fs, func(m []string) (binding, bool) {
		u := strings.TrimRight(m[0], ".,;:!?\"'")
		host := ""
		if parts := strings.Split(u, "/"); len(parts) > 2 {
			host = rustLower(parts[2])
		}
		if host == "github.com" || host == "www.github.com" {
			return binding{bGithub, u}, true
		}
		return binding{bURL, u}, true
	}, false, false)
	take(reRepo, &rest, &fs, func(m []string) (binding, bool) { return binding{bRepo, strings.TrimRight(m[1], ".")}, true }, true, false)
	take(reWiki, &rest, &fs, func(m []string) (binding, bool) {
		t := strings.TrimFunc(m[1], isRustSpace)
		return binding{bWikiLink, t}, t != ""
	}, false, false)
	take(reKey, &rest, &fs, func(m []string) (binding, bool) { return binding{bItemKey, m[0]}, true }, true, true)
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].pos < fs[j].pos })
	seen := map[binding]bool{}
	var out []binding
	for _, f := range fs {
		if !seen[f.b] {
			seen[f.b] = true
			out = append(out, f.b)
		}
	}
	return out
}

func isRustSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// syncHandler: note.created / note.updated on a page rewrites its `binds` relations and external bindings.
func syncHandler(c *command.Ctx, ev jobs.Event) error {
	var p struct {
		Changed []any `json:"changed"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	changed := slices.Contains(p.Changed, any("content"))
	if !(ev.Kind == "note.created" || (ev.Kind == "note.updated" && changed)) || ev.NoteID == nil {
		return nil
	}
	var md string
	var plain bool
	err := c.Tx.QueryRow(c.Context,
		"select content_md, cardinality(types) = 0 from core.notes where id = $1::uuid and deleted_at is null", *ev.NoteID).Scan(&md, &plain)
	if err == pgx.ErrNoRows || (err == nil && !plain) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncBindings(c, *ev.NoteID, md)
}

func syncBindings(c *command.Ctx, page, md string) error {
	var keys, titles, ids []string
	type ext struct{ kind, ref string }
	var external []ext
	for _, b := range parseBindings(md) {
		switch b.kind {
		case bItemKey:
			keys = append(keys, b.val)
		case bWikiLink:
			titles = append(titles, rustLower(b.val))
		case bNoteLink:
			ids = append(ids, b.val)
		case bGithub:
			external = append(external, ext{"github", b.val})
		case bRepo:
			external = append(external, ext{"repo", b.val})
		case bURL:
			external = append(external, ext{"url", b.val})
		}
	}
	// Only bind to notes the author can read; no known author (system edit) means no note bindings.
	author := c.Principal.UserID
	readable := []string{}
	admin := false
	if author != nil {
		if err := queryStrings(c, &readable, `select s.id::text from core.spaces s
			 left join core.memberships m on m.user_id = $1::uuid
			 left join core.space_members sm on sm.space_id = s.id and sm.user_id = $1::uuid
			 where m.role in ('owner','admin') or sm.user_id is not null
			    or (s.visibility = 'open' and m.role = 'member')`, *author); err != nil {
			return err
		}
		if err := c.Tx.QueryRow(c.Context,
			"select exists(select 1 from core.memberships where user_id = $1::uuid and role in ('owner','admin'))", *author).Scan(&admin); err != nil {
			return err
		}
	}
	type target struct{ id, ref string }
	var targets []target
	have := map[string]bool{}
	add := func(id, ref string) {
		if !have[id] {
			have[id] = true
			targets = append(targets, target{id, ref})
		}
	}
	pairs := func(sql string, arg any) ([]target, error) {
		rows, err := c.Tx.Query(c.Context, sql, arg, readable, author, admin)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []target
		for rows.Next() {
			var t target
			if err := rows.Scan(&t.id, &t.ref); err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return out, rows.Err()
	}
	byKey, err := pairs(`select i.note_id::text, i.key from projects.items i join core.notes n on n.id = i.note_id
		 where i.key = any($1::text[]) and n.deleted_at is null and n.space_id = any($2::text[]::uuid[])
		   and core.note_access(n.id, $3::uuid, $4, $2::text[]::uuid[]) <> 'none'`, nz(keys))
	if err != nil {
		return err
	}
	byTitle, err := pairs(`select id::text, '[[' || title || ']]' from core.notes
		 where lower(title) = any($1::text[]) and deleted_at is null and space_id = any($2::text[]::uuid[])
		   and core.note_access(id, $3::uuid, $4, $2::text[]::uuid[]) <> 'none'`, nz(titles))
	if err != nil {
		return err
	}
	byID, err := pairs(`select id::text, '' from core.notes where id = any($1::text[]::uuid[]) and deleted_at is null and space_id = any($2::text[]::uuid[])
		   and core.note_access(id, $3::uuid, $4, $2::text[]::uuid[]) <> 'none'`, nz(ids))
	if err != nil {
		return err
	}
	for _, t := range append(byKey, byTitle...) {
		add(t.id, t.ref)
	}
	for _, t := range byID {
		add(t.id, "cnote://note/"+t.id)
	}
	targetsF := targets[:0:0]
	for _, t := range targets {
		if t.id != page {
			targetsF = append(targetsF, t)
		}
	}
	if _, err := c.Tx.Exec(c.Context, "delete from core.relations where from_id = $1::uuid and kind = 'binds'", page); err != nil {
		return err
	}
	for _, t := range targetsF {
		if _, err := c.Tx.Exec(c.Context,
			"insert into core.relations (workspace_id, from_id, to_id, kind, external_ref) values ($1::uuid,$2::uuid,$3::uuid,'binds',$4)",
			c.WorkspaceID(), page, t.id, t.ref); err != nil {
			return err
		}
	}
	if _, err := c.Tx.Exec(c.Context, "delete from docs.external_bindings where note_id = $1::uuid", page); err != nil {
		return err
	}
	for _, e := range external {
		if _, err := c.Tx.Exec(c.Context,
			"insert into docs.external_bindings (workspace_id, note_id, kind, ref) values ($1::uuid,$2::uuid,$3,$4) on conflict do nothing",
			c.WorkspaceID(), page, e.kind, e.ref); err != nil {
			return err
		}
	}
	_, err = c.Emit("doc.bindings_synced", &page, map[string]any{"notes": len(targetsF)})
	return err
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func queryStrings(c *command.Ctx, dst *[]string, sql string, args ...any) error {
	rows, err := c.Tx.Query(c.Context, sql, args...)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	*dst = nz(out)
	return nil
}
