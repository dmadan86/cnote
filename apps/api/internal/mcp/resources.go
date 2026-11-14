package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// listResources: the 50 most recently updated notes the caller may read (page restrictions applied in SQL).
func (s *Server) listResources(w http.ResponseWriter, r *http.Request, p command.Principal, m message) {
	list := []resource{}
	err := s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) error {
		spaces, err := access.ReadableSpaceIDs(c)
		if err != nil {
			return err
		}
		uid, admin := access.Viewer(c)
		rows, err := c.Tx.Query(c.Context, `select id::text, title, freshness from core.notes
			where deleted_at is null and freshness <> 'archived' and space_id = any($1::text[]::uuid[])
			  and core.note_access(id, $3::uuid, $4, $1::text[]::uuid[]) <> 'none'
			order by updated_at desc limit $2`, spaces, int64(recentNotes), uid, admin)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, freshness string
			if err := rows.Scan(&id, &title, &freshness); err != nil {
				return err
			}
			list = append(list, resource{URI: "cnote://ws/" + p.WorkspaceID + "/note/" + id, Name: title, Description: "Note (" + freshness + ")", MimeType: "text/markdown"})
		}
		return rows.Err()
	})
	if err != nil {
		rpcErr(w, m.id, err)
		return
	}
	respond(w, m.id, map[string]any{"resources": list})
}

// parseURI is `cnote://ws/{ws}/note/{id}` or `.../view/{id}`. exotic=true: a UUID spelling Go cannot judge.
func parseURI(uri string) (ws, kind, id string, ok, exotic bool) {
	rest, has := strings.CutPrefix(uri, "cnote://ws/")
	if !has {
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || (parts[1] != "note" && parts[1] != "view") {
		return
	}
	for _, u := range []string{parts[0], parts[2]} {
		if (len(u) == 38 && u[0] == '{') || (len(u) == 45 && strings.EqualFold(u[:9], "urn:uuid:")) {
			return "", "", "", false, true
		}
	}
	w, i := parseUUID(parts[0]), parseUUID(parts[2])
	if w == nil || i == nil {
		return
	}
	return *w, parts[1], *i, true, false
}

func (s *Server) readResource(w http.ResponseWriter, r *http.Request, p command.Principal, m message, proxy func()) {
	var uri string
	_ = json.Unmarshal(m.params["uri"], &uri)
	ws, kind, id, ok, exotic := parseURI(uri)
	if exotic {
		proxy()
		return
	}
	if !ok {
		respondErr(w, m.id, codeInvalidParams, "bad resource uri "+uri)
		return
	}
	// A key is bound to one workspace; another workspace's resource does not exist as far as it can tell.
	if ws != p.WorkspaceID {
		respondErr(w, m.id, codeResourceNotFound, "resource not found")
		return
	}
	var body string
	err := s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) (err error) {
		if kind == "note" {
			body, err = readNote(c, id)
		} else {
			body, err = readView(c, id)
		}
		return err
	})
	switch {
	case errors.Is(err, command.ErrFallback):
		proxy()
	case err != nil:
		rpcErr(w, m.id, err)
	default:
		respond(w, m.id, map[string]any{"contents": []content{{URI: uri, MimeType: "text/markdown", Text: body}}})
	}
}

// frontMatter: JSON strings are valid YAML scalars, so values are JSON-encoded; nulls are omitted.
func frontMatter(keys []string, vals map[string]json.RawMessage) string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		if v := vals[k]; len(v) > 0 && string(v) != "null" {
			b.WriteString(k + ": " + string(v) + "\n")
		}
	}
	b.WriteString("---\n")
	return b.String()
}

func readNote(c *command.Ctx, id string) (string, error) {
	raw, err := c.Call("note.get", map[string]any{"id": id})
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var note map[string]any
	if err := dec.Decode(&note); err != nil {
		return "", err
	}
	enc := func(v any) json.RawMessage { b, _ := command.Marshal(v); return b }
	vals := map[string]json.RawMessage{}
	for _, k := range []string{"title", "freshness", "verify_by", "verified_at", "types", "id", "updated_at", "owner_id"} {
		vals[k] = enc(note[k])
	}
	vals["owner"] = vals["owner_id"]
	if oid, _ := note["owner_id"].(string); parseUUID(oid) != nil {
		var name string
		err := c.Tx.QueryRow(c.Context, "select name from core.users where id = $1::uuid", oid).Scan(&name)
		switch {
		case err == nil:
			vals["owner"] = enc(name)
		case !errors.Is(err, pgx.ErrNoRows):
			return "", command.DB(err)
		}
	}
	md, _ := note["content_md"].(string)
	return frontMatter([]string{"title", "owner", "freshness", "verify_by", "verified_at", "types", "id", "updated_at"}, vals) + "\n" + md, nil
}

func readView(c *command.Ctx, id string) (string, error) {
	var name, layout, query string
	var space *string
	err := c.Tx.QueryRow(c.Context, "select name, layout, query, space_id::text from core.views where id = $1::uuid", id).Scan(&name, &layout, &query, &space)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("view")
	}
	if err != nil {
		return "", command.DB(err)
	}
	if space != nil {
		if err := access.AssertSpaceAccess(c, *space, false); err != nil {
			return "", err
		}
	}
	enc := func(v any) json.RawMessage { b, _ := command.Marshal(v); return b }
	out := frontMatter([]string{"name", "layout", "query"}, map[string]json.RawMessage{"name": enc(name), "layout": enc(layout), "query": enc(query)})
	rows, err := c.Call("item.list", map[string]any{"cnql": query})
	if err != nil {
		return "", err
	}
	return out + "\n```json\n" + pretty(rows) + "\n```\n", nil
}
