package docs

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/ai"
	"github.com/cnote/api/internal/modules/notes"
)

// doc.ask and doc.capture (crates/cnote-docs/src/commands.rs). Both use the process-wide provider
// (`ctx.registry.ai`): no workspace key, no audit row and no metering, exactly like Rust.

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields("the and for are was how what who why when where does did can our you with that this from have has should which") {
		stop[w] = true
	}
}

func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}

func keywords(q string) []string {
	set := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !isAlnum(r) }) {
		if len(w) > 2 && !stop[w] {
			set[w] = true
		}
	}
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out[:min(8, len(out))]
}

func search(c *command.Ctx, q string) ([]map[string]json.RawMessage, error) {
	raw, err := c.Call("search.notes", map[string]any{"q": q, "limit": 5})
	if err != nil {
		return nil, err
	}
	var hits []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &hits)
	return hits, nil
}

func jstr(v json.RawMessage) string {
	var s string
	_ = json.Unmarshal(v, &s)
	return s
}

func nz(v json.RawMessage) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	return v
}

func docAsk(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	question, err := o.ReqStr("question")
	if err != nil {
		return nil, err
	}
	words := keywords(question)
	if len(words) == 0 {
		return nil, command.ErrInvalid("ask a question with at least one meaningful word")
	}
	// Every keyword must match for an answer; any single keyword only makes a "partial" match worth routing to.
	hits, err := search(c, strings.Join(words, " "))
	if err != nil {
		return nil, err
	}
	if len(hits) > 0 {
		citations := make([]map[string]any, len(hits))
		sources := make([]string, len(hits))
		for i, h := range hits {
			citations[i] = map[string]any{"title": nz(h["title"]), "note_id": nz(h["id"]), "freshness": nz(h["freshness"]),
				"verified_at": nz(h["verified_at"]), "snippet": nz(h["snippet"])}
			sources[i] = "[" + jstr(h["title"]) + "] " + jstr(h["freshness"]) + "\n" + jstr(h["snippet"])
		}
		joined := strings.Join(sources, "\n\n")
		answer := joined
		if done, err := ai.Process().Complete(c.Context, "answer", []ai.Message{
			{Role: "system", Content: "Answer the question using only these pages, and say when a page is stale.\nQuestion: " + question},
			{Role: "user", Content: joined},
		}, 512); err == nil {
			answer = done.Text
		}
		return map[string]any{"answered": true, "answer": answer, "citations": citations}, nil
	}

	partial, err := search(c, strings.Join(words, " or "))
	if err != nil {
		return nil, err
	}
	var routed *string
	if len(partial) > 0 {
		if id := jstr(partial[0]["id"]); id != "" {
			if err := c.Tx.QueryRow(c.Context, "select owner_id::text from core.notes where id = $1::uuid", id).Scan(&routed); err != nil {
				return nil, err
			}
		}
	}
	if routed == nil {
		err := c.Tx.QueryRow(c.Context,
			"select user_id::text from core.memberships where role in ('owner','admin') order by (role = 'admin') desc, created_at limit 1").Scan(&routed)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	var id string
	if err := c.Tx.QueryRow(c.Context,
		"insert into docs.capture_requests (workspace_id, question, asked_by, routed_to) values ($1::uuid,$2,$3::uuid,$4::uuid) returning id::text",
		c.WorkspaceID(), strings.TrimSpace(question), c.Principal.UserID, routed).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := c.Emit("doc.capture_requested", nil, map[string]any{"request_id": id, "routed_to": routed}); err != nil {
		return nil, err
	}
	related := make([]map[string]any, len(partial))
	for i, h := range partial {
		related[i] = map[string]any{"title": nz(h["title"]), "note_id": nz(h["id"])}
	}
	return map[string]any{
		"answered":  false,
		"answer":    "No page answers this yet. I asked the likely owner to write it up and will link the answer here.",
		"citations": []any{}, "capture_request_id": id, "routed_to": routed, "related": related,
	}, nil
}

func docCapture(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	source, err := o.ReqStr("source")
	if err != nil {
		return nil, err
	}
	text, err := o.ReqStr("text")
	if err != nil {
		return nil, err
	}
	space, err := o.ReqUUID("space_id")
	if err != nil {
		return nil, err
	}
	title, err := o.OptStr("title")
	if err != nil {
		return nil, err
	}
	if source != "transcript" && source != "chat" && source != "pr" {
		return nil, command.ErrInvalid("source must be transcript, chat or pr")
	}
	if strings.TrimSpace(text) == "" {
		return nil, command.ErrInvalid("text is required")
	}
	if err := access.AssertSpaceAccess(c, space, true); err != nil {
		return nil, err
	}
	done, err := ai.Process().Complete(c.Context, "draft", []ai.Message{
		{Role: "system", Content: "Turn this " + source + " into a concise wiki page in Markdown: decisions, facts, owners, open questions. Invent nothing."},
		{Role: "user", Content: text},
	}, 1500)
	if err != nil {
		return nil, command.NewError(command.Internal, err.Error()) // anyhow's message, unprefixed
	}
	t := ""
	if title != nil && strings.TrimSpace(*title) != "" {
		t = *title
	} else {
		first := ""
		for _, l := range strings.Split(text, "\n") {
			if strings.TrimSpace(l) != "" {
				first = strings.TrimSpace(l)
				break
			}
		}
		if r := []rune(first); len(r) > 80 {
			first = string(r[:80])
		}
		if t = first; t == "" {
			t = "Captured " + source
		}
	}
	note, err := notes.InsertNote(c, space, t, nil, &done.Text, []string{})
	if err != nil {
		return nil, err
	}
	if _, err := c.Emit("doc.captured", &note.ID, map[string]any{"source": source}); err != nil {
		return nil, err
	}
	return note, nil
}
