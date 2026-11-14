// Package ai ports crates/cnote-ai: provider resolution (platform provider or the workspace's own, with an
// encrypted key), audit and metering, the SSRF guard, search.hybrid, ai.* commands and outbound hooks.
// Indexing (ai.index_note), event handlers, hook delivery jobs and the hook sweep stay in the Rust worker.
package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

const maxNoteChars = 24_000 // cap on note text sent to a model in one prompt

func Commands() []command.Command {
	adm := func(name string, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupAdmin, h)
		c.MinRole = command.Admin
		return c
	}
	return append([]command.Command{
		command.NewCommand("search.hybrid", command.GroupRead, searchHybrid),
		command.NewCommand("ai.summarize", command.GroupRead, summarize),
		command.NewCommand("ai.extract_tasks", command.GroupRead, extractTasks),
		command.NewCommand("ai.draft", command.GroupRead, draft),
		adm("ai.set_provider", setProvider),
		adm("ai.get_provider", getProvider),
		adm("ai.clear_provider", clearProvider),
	}, hookCommands()...)
}

// ---------- provider resolution, audit, metering ----------

type resolved struct {
	provider Provider
	byo      bool // workspace brings its own key: never metered
	redact   bool
}

type storedConfig struct {
	provider                       string
	baseURL, model, embeddingModel *string
	keyEnc                         []byte
}

func loadConfig(c *command.Ctx) (*storedConfig, error) {
	var s storedConfig
	err := c.Tx.QueryRow(c.Context,
		"select provider, base_url, model, embedding_model, key_enc from ai.provider_config where workspace_id = $1::uuid", c.WorkspaceID()).
		Scan(&s.provider, &s.baseURL, &s.model, &s.embeddingModel, &s.keyEnc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}

func redactEnabled(c *command.Ctx) (bool, error) {
	var b bool
	err := c.Tx.QueryRow(c.Context,
		"select coalesce(settings->'ai_redact_pii' = 'true'::jsonb, false) from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&b)
	return b, err
}

// endpointErr maps a netguard error: unparsable URLs go to Rust for its exact text.
func endpointErr(err error, mk func(string) *command.Error) error {
	if errors.Is(err, errUnparsable) {
		return command.ErrFallback
	}
	return mk(err.Error())
}

func resolve(c *command.Ctx) (*resolved, error) {
	redact, err := redactEnabled(c)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(c)
	if err != nil {
		return nil, err
	}
	var p Provider
	byo := false
	if cfg != nil {
		if cfg.baseURL != nil { // DNS may have changed since the URL was saved
			if err := CheckEndpoint(c.Context, *cfg.baseURL); err != nil {
				return nil, endpointErr(err, command.ErrForbidden)
			}
		}
		var key *string
		if cfg.keyEnc != nil {
			mk, err := MasterKey()
			if err != nil {
				return nil, err
			}
			k, err := Decrypt(mk, c.WorkspaceID(), cfg.keyEnc)
			if err != nil {
				return nil, err
			}
			key = &k
		}
		p, byo = For(cfg.provider, key, cfg.baseURL, cfg.model, cfg.embeddingModel), true
	} else {
		p = Process()
	}
	// the local provider never leaves the process, so there is nothing to redact
	return &resolved{p, byo, redact && p.Name() != "local"}, nil
}

func record(c *command.Ctx, r *resolved, provider, model, purpose string, prompt any, response any, in, out uint32) error {
	var actor *string
	if c.Principal.AgentID != nil {
		actor = c.Principal.AgentID
	} else {
		actor = c.Principal.UserID
	}
	p, err := command.Marshal(prompt)
	if err != nil {
		return command.ErrInvalid(err.Error())
	}
	var resp any
	if response != nil {
		b, _ := command.Marshal(response)
		resp = json.RawMessage(b)
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into ai.audit (workspace_id, actor_id, provider, model, purpose, prompt, response, tokens_in, tokens_out)
		 values ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9)`,
		c.WorkspaceID(), actor, provider, model, purpose, json.RawMessage(p), resp, int32(in), int32(out)); err != nil {
		return command.DB(err)
	}
	if !r.byo {
		meta, _ := command.Marshal(map[string]string{"purpose": purpose, "provider": provider, "model": model})
		for _, u := range []struct {
			kind string
			n    uint32
		}{{"ai.tokens_in", in}, {"ai.tokens_out", out}} {
			if _, err := c.Tx.Exec(c.Context, "insert into core.usage (workspace_id, kind, quantity, meta) values ($1::uuid,$2,$3,$4)",
				c.WorkspaceID(), u.kind, int64(u.n), json.RawMessage(meta)); err != nil {
				return command.DB(err)
			}
		}
	}
	return nil
}

func providerErr(err error) error {
	return command.NewError(command.Internal, "AI provider error: "+err.Error())
}

// complete runs a completion with redaction, audit and metering.
func complete(c *command.Ctx, purpose string, msgs []Message, maxTokens uint32) (Completion, error) {
	r, err := resolve(c)
	if err != nil {
		return Completion{}, err
	}
	if r.redact {
		red := make([]Message, len(msgs))
		for i, m := range msgs {
			red[i] = Message{m.Role, Redact(m.Content)}
		}
		msgs = red
	}
	out, err := r.provider.Complete(c.Context, purpose, msgs, maxTokens)
	if err != nil {
		return Completion{}, providerErr(err)
	}
	return out, record(c, r, out.Provider, out.Model, purpose, msgs, map[string]string{"text": out.Text}, out.TokensIn, out.TokensOut)
}

// embed embeds texts with redaction, audit and metering; returns the embedding model id the vectors belong to.
func embed(c *command.Ctx, purpose string, texts []string) (string, [][]float32, error) {
	r, err := resolve(c)
	if err != nil {
		return "", nil, err
	}
	if r.redact {
		red := make([]string, len(texts))
		for i, t := range texts {
			red[i] = Redact(t)
		}
		texts = red
	}
	chars := 0
	for _, t := range texts {
		chars += len([]rune(t))
	}
	vecs, err := r.provider.Embed(c.Context, texts)
	if err != nil {
		return "", nil, providerErr(err)
	}
	model := r.provider.EmbeddingModel()
	// no per-call token count from embedding APIs: ~4 chars per token is the metering estimate
	err = record(c, r, r.provider.Name(), model, purpose, map[string]int{"texts": len(texts), "chars": chars}, nil, uint32(chars/4), 0)
	return model, vecs, err
}

// ---------- BYO-LLM settings ----------

func setProvider(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	provider, err := o.ReqStr("provider")
	if err != nil {
		return nil, err
	}
	baseURL, err := o.OptStr("base_url")
	if err != nil {
		return nil, err
	}
	model, err := o.OptStr("model")
	if err != nil {
		return nil, err
	}
	embModel, err := o.OptStr("embedding_model")
	if err != nil {
		return nil, err
	}
	apiKey, err := o.OptStr("api_key")
	if err != nil {
		return nil, err
	}
	if provider != "anthropic" && provider != "openai" {
		return nil, command.ErrInvalid(`provider must be "anthropic" or "openai"`)
	}
	if baseURL != nil {
		t := strings.TrimSpace(*baseURL)
		if baseURL = &t; t == "" {
			baseURL = nil
		}
	}
	if baseURL != nil {
		if err := CheckEndpoint(c.Context, *baseURL); err != nil {
			return nil, endpointErr(err, command.ErrInvalid)
		}
	}
	ws := c.WorkspaceID()
	existing, err := loadConfig(c)
	if err != nil {
		return nil, err
	}
	var keyEnc []byte
	if apiKey != nil && strings.TrimSpace(*apiKey) != "" {
		// refuse before anything is stored if the master key is missing or malformed
		mk, err := MasterKey()
		if err != nil {
			return nil, err
		}
		if keyEnc, err = Encrypt(mk, ws, strings.TrimSpace(*apiKey)); err != nil {
			return nil, err
		}
	} else if existing != nil && existing.provider == provider && eqPtr(existing.baseURL, baseURL) {
		keyEnc = existing.keyEnc // a stored key is only reused for the endpoint it was given for
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into ai.provider_config (workspace_id, provider, base_url, model, embedding_model, key_enc, updated_by)
		 values ($1::uuid,$2,$3,$4,$5,$6,$7::uuid)
		 on conflict (workspace_id) do update set provider = $2, base_url = $3, model = $4,
		   embedding_model = $5, key_enc = $6, updated_by = $7::uuid, updated_at = now()`,
		ws, provider, baseURL, model, embModel, keyEnc, c.Principal.UserID); err != nil {
		return nil, err
	}
	if _, err := c.Emit("ai.provider_changed", nil, map[string]any{"provider": provider, "base_url": baseURL, "model": model, "has_key": keyEnc != nil}); err != nil {
		return nil, err
	}
	return providerSettings(c)
}

func eqPtr(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func emptyObj(input json.RawMessage) error { // `Empty {}` accepts any object and ignores fields
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil || m == nil {
		return command.ErrFallback
	}
	return nil
}

func getProvider(c *command.Ctx, input json.RawMessage) (any, error) {
	if err := emptyObj(input); err != nil {
		return nil, err
	}
	return providerSettings(c)
}

func providerSettings(c *command.Ctx) (any, error) {
	redact, err := redactEnabled(c)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(c)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return map[string]any{"configured": false, "platform_provider": Process().Name(), "redact_pii": redact, "metered": true}, nil
	}
	return map[string]any{"configured": true, "provider": cfg.provider, "base_url": cfg.baseURL, "model": cfg.model,
		"embedding_model": cfg.embeddingModel, "has_key": cfg.keyEnc != nil, "redact_pii": redact, "metered": false}, nil
}

func clearProvider(c *command.Ctx, input json.RawMessage) (any, error) {
	if err := emptyObj(input); err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, "delete from ai.provider_config where workspace_id = $1::uuid", c.WorkspaceID())
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() > 0 {
		if _, err := c.Emit("ai.provider_changed", nil, map[string]any{"cleared": true}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"cleared": tag.RowsAffected() > 0}, nil
}

// ---------- search.hybrid ----------

// Reciprocal rank fusion (k = 60) of the full-text and vector rankings. Both CTEs filter by readable spaces and
// page restrictions and exclude archived/deleted notes inside the query, so nothing unreadable leaks through.
const hybridSQL = `
with ft as (
  select n.id, row_number() over (order by ts_rank(n.search, websearch_to_tsquery('english', $1)) desc) as rnk
  from core.notes n
  where n.deleted_at is null and n.freshness <> 'archived' and n.space_id = any($2::text[]::uuid[])
    and core.note_access(n.id, $6::uuid, $7::bool, $2::text[]::uuid[]) <> 'none'
    and n.search @@ websearch_to_tsquery('english', $1)
  order by ts_rank(n.search, websearch_to_tsquery('english', $1)) desc
  limit 50
), near as (
  select c.note_id, min(c.embedding <=> $3::vector) as dist
  from ai.chunks c join core.notes n on n.id = c.note_id
  where c.model = $4 and n.deleted_at is null and n.freshness <> 'archived' and n.space_id = any($2::text[]::uuid[])
    and core.note_access(n.id, $6::uuid, $7::bool, $2::text[]::uuid[]) <> 'none'
  group by c.note_id
  order by dist
  limit 50
), vec as (
  select note_id as id, row_number() over (order by dist) as rnk from near
), fused as (
  select id, sum(1.0 / (60 + rnk)) as score from (select id, rnk from ft union all select id, rnk from vec) u group by id
)
select n.id::text, n.space_id::text, n.title, n.types, n.freshness,
       ts_headline('english', n.content_md, websearch_to_tsquery('english', $1), 'MaxWords=30,MinWords=10') as snippet,
       f.score::real as score
from fused f join core.notes n on n.id = f.id
order by f.score desc, n.title
limit $5::int8`

type hybridHit struct {
	ID        string     `json:"id"`
	SpaceID   string     `json:"space_id"`
	Title     string     `json:"title"`
	Types     []string   `json:"types"`
	Freshness string     `json:"freshness"`
	Snippet   string     `json:"snippet"`
	Score     notes.Rank `json:"score"` // f32 widened to f64, as serde_json does
}

func searchHybrid(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	q, err := o.ReqStr("q")
	if err != nil {
		return nil, err
	}
	lim, err := o.OptInt64("limit")
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, command.ErrInvalid("q is required")
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	model, vecs, err := embed(c, "search", []string{q})
	if err != nil {
		return nil, err
	}
	// let the HNSW scan keep going past ef_search when the permission filter discards candidates (pgvector 0.8+)
	if _, err := c.Tx.Exec(c.Context, "select set_config('hnsw.iterative_scan', 'relaxed_order', true)"); err != nil {
		return nil, err
	}
	limit := int64(10)
	if lim != nil {
		limit = min(max(*lim, 1), 50)
	}
	rows, err := c.Tx.Query(c.Context, hybridSQL, q, spaces, VecLiteral(vecs[0]), model, limit, uid, admin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hybridHit{}
	for rows.Next() {
		var h hybridHit
		if err := rows.Scan(&h.ID, &h.SpaceID, &h.Title, &h.Types, &h.Freshness, &h.Snippet, &h.Score); err != nil {
			return nil, err
		}
		if h.Types == nil {
			h.Types = []string{}
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---------- summarize / extract_tasks / draft ----------

func noteText(c *command.Ctx, id string) (title, content string, err error) {
	if _, err = access.AssertNoteAccess(c, id, false); err != nil {
		return
	}
	if err = c.Tx.QueryRow(c.Context, "select title, content_md from core.notes where id = $1::uuid", id).Scan(&title, &content); err != nil {
		return
	}
	if r := []rune(content); len(r) > maxNoteChars {
		content = string(r[:maxNoteChars])
	}
	return
}

func textOut(c Completion) any {
	return map[string]any{"text": c.Text, "provider": c.Provider, "model": c.Model}
}

func summarize(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	title, content, err := noteText(c, id)
	if err != nil {
		return nil, err
	}
	out, err := complete(c, "summarize", []Message{
		{"system", "Summarize the note in a few sentences, then list key decisions and open questions. Use only the note's content."},
		{"user", fmt.Sprintf("Title: %s\n\n%s", title, content)},
	}, 600)
	if err != nil {
		return nil, err
	}
	return textOut(out), nil
}

type suggestedTask struct {
	Title    string  `json:"title"`
	Assignee *string `json:"assignee"`
	Due      *string `json:"due"`
}

// ParseTasks: models are asked for a JSON array; fall back to bullet lines when they answer in prose.
func parseTasks(text string) []suggestedTask {
	if a, b := strings.Index(text, "["), strings.LastIndex(text, "]"); a >= 0 && a < b {
		if v, ok := decodeTasks(text[a : b+1]); ok && len(v) > 0 {
			return v
		}
	}
	out := []suggestedTask{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		rest, ok := strings.CutPrefix(l, "- ")
		if !ok {
			if rest, ok = strings.CutPrefix(l, "* "); !ok {
				continue
			}
		}
		if r, ok := strings.CutPrefix(rest, "[ ] "); ok {
			rest = r
		} else if r, ok := strings.CutPrefix(rest, "[x] "); ok {
			rest = r
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			out = append(out, suggestedTask{Title: rest})
		}
	}
	return out
}

// decodeTasks is serde's Vec<SuggestedTask>: title required, assignee and due optional strings, case-sensitive keys.
func decodeTasks(s string) ([]suggestedTask, bool) {
	var items []json.RawMessage
	if json.Unmarshal([]byte(s), &items) != nil {
		return nil, false
	}
	out := make([]suggestedTask, 0, len(items))
	for _, it := range items {
		var m map[string]json.RawMessage
		if json.Unmarshal(it, &m) != nil || m == nil {
			return nil, false
		}
		var t suggestedTask
		if string(m["title"]) == "null" || json.Unmarshal(m["title"], &t.Title) != nil {
			return nil, false
		}
		for k, dst := range map[string]**string{"assignee": &t.Assignee, "due": &t.Due} {
			if v, ok := m[k]; ok && json.Unmarshal(v, dst) != nil {
				return nil, false
			}
		}
		out = append(out, t)
	}
	return out, true
}

func extractTasks(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("note_id")
	if err != nil {
		return nil, err
	}
	title, content, err := noteText(c, id)
	if err != nil {
		return nil, err
	}
	out, err := complete(c, "extract_tasks", []Message{
		{"system", `Extract action items from the note. Reply with only a JSON array of objects {"title", "assignee", "due"}; use null for unknown assignee or due date. Do not invent tasks.`},
		{"user", fmt.Sprintf("Title: %s\n\n%s", title, content)},
	}, 800)
	if err != nil {
		return nil, err
	}
	return map[string]any{"suggested_tasks": parseTasks(out.Text),
		"note": "Suggestions only; create items with the project commands after review.", "provider": out.Provider, "model": out.Model}, nil
}

func draft(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	prompt, err := o.ReqStr("prompt")
	if err != nil {
		return nil, err
	}
	noteID, err := o.OptUUID("note_id")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, command.ErrInvalid("prompt is required")
	}
	var user strings.Builder
	if noteID != nil {
		title, content, err := noteText(c, *noteID)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&user, "Context note: %s\n\n%s\n\n---\n\n", title, content)
	}
	user.WriteString(strings.TrimSpace(prompt))
	out, err := complete(c, "draft", []Message{
		{"system", "You draft text for a team workspace. Be concise and direct. Use any provided note as context."},
		{"user", user.String()},
	}, 1200)
	if err != nil {
		return nil, err
	}
	return textOut(out), nil
}
