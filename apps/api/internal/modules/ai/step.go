package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
)

// Tool-calling contract for the agent runtime (docs/design/agent-runtime.md section 2.2). Providers implement Step;
// Session wraps provider resolution, redaction and metering the way complete() does for one-shot completions.

type Tool struct {
	Name, Description string
	Schema            json.RawMessage
}

type ToolCall struct {
	ID, Name string
	Input    json.RawMessage
}

type ToolResult struct {
	CallID, Content string
	IsError         bool
}

type Turn struct {
	Role    string // user | assistant | tool
	Text    string
	Calls   []ToolCall      // assistant
	Results []ToolResult    // tool: every result of the previous assistant turn, in one turn
	Raw     json.RawMessage // assistant: provider-native content, replayed unchanged
}

type StepRequest struct {
	Purpose, Model, System string
	Tools                  []Tool
	Turns                  []Turn
	MaxTokens              uint32
}

type StepResult struct {
	Text            string
	Calls           []ToolCall
	Raw             json.RawMessage
	Stop            string // end | tools | max_tokens | refusal
	Provider, Model string
	// TokensIn counts uncached, cache-write and cache-read tokens.
	TokensIn, TokensOut, CacheRead uint32
}

// APIError is a non-2xx answer from a model API; callers use Status to tell retryable (429, 5xx) from fatal.
type APIError struct{ Status int }

func (e *APIError) Error() string { return "model API returned " + statusLine(e.Status) }

const defaultStepTokens = 8000

// IsProviderError reports whether err came from the model provider (not from this server's own checks).
func IsProviderError(err error) bool {
	var ce *command.Error
	return errors.As(err, &ce) && ce.Code == command.Internal && strings.HasPrefix(ce.Message, "AI provider error:")
}

// Session is a resolved provider for one run: BYO config, key and redaction decided once.
type Session struct{ r *resolved }

// Open resolves the workspace's provider (its own endpoint and key, or the platform's).
func Open(c *command.Ctx) (*Session, error) {
	r, err := resolve(c)
	if err != nil {
		return nil, err
	}
	return &Session{r}, nil
}

// ModelFor maps a model tier ("reasoning" | "routine") to a model id; "" means the provider's configured model.
// A workspace with its own key uses its configured model for both tiers; on the platform's Anthropic provider the
// routine tier is AI_MODEL_ROUTINE (default claude-haiku-5-5).
func (s *Session) ModelFor(tier string) string {
	if tier != "routine" || s.r.byo || s.r.provider.Name() != "anthropic" {
		return ""
	}
	if m := envVar("AI_MODEL_ROUTINE"); m != nil {
		return *m
	}
	return "claude-haiku-5-5"
}

// Step makes one model call. It touches no database and never records anything. Errors are returned unwrapped so
// callers can errors.As an *APIError (429 and 5xx are retryable); Turn.Raw of assistant turns is never redacted.
func (s *Session) Step(ctx context.Context, r StepRequest) (StepResult, error) {
	if s.r.redact {
		r = redactStep(r)
	}
	return s.r.provider.Step(ctx, r)
}

func redactStep(r StepRequest) StepRequest {
	r.System = Redact(r.System)
	turns := make([]Turn, len(r.Turns))
	for i, t := range r.Turns {
		if t.Role == "user" {
			t.Text = Redact(t.Text)
		}
		if len(t.Results) > 0 {
			rs := make([]ToolResult, len(t.Results))
			for j, x := range t.Results {
				x.Content = redactContent(x.Content)
				rs[j] = x
			}
			t.Results = rs
		}
		turns[i] = t
	}
	r.Turns = turns
	return r
}

// redactContent redacts a tool result: JSON by string leaf, anything else as text.
func redactContent(s string) string {
	if json.Valid([]byte(s)) {
		return string(RedactJSON([]byte(s)))
	}
	return Redact(s)
}

// Record writes the ai.audit row and, unless the workspace brings its own key, the core.usage rows for one step.
// ref is the audit prompt, e.g. {run_id, seq}; a map with run_id or agent_id also puts them in the usage meta.
func (s *Session) Record(c *command.Ctx, ref any, res StepResult) error {
	meta := map[string]string{}
	if m, ok := ref.(map[string]any); ok {
		for _, k := range []string{"run_id", "agent_id"} {
			if v, ok := m[k].(string); ok {
				meta[k] = v
			}
		}
	}
	return record(c, s.r, res.Provider, res.Model, "agent.step", ref, nil, res.TokensIn, res.TokensOut, meta)
}

// Complete runs a one-shot completion with the workspace's key, redaction, audit and metering.
func Complete(c *command.Ctx, purpose string, msgs []Message, maxTokens uint32) (Completion, error) {
	return complete(c, purpose, msgs, maxTokens)
}

var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// redactLeaf redacts a string but leaves UUIDs alone (their digit runs look like phone and Aadhaar numbers).
func redactLeaf(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range uuidRe.FindAllStringIndex(s, -1) {
		b.WriteString(Redact(s[last:m[0]]))
		b.WriteString(s[m[0]:m[1]])
		last = m[1]
	}
	b.WriteString(Redact(s[last:]))
	return b.String()
}

// RedactJSON redacts string leaves only: numbers, booleans, keys and UUID-shaped text survive, so ids and paise
// totals are not mistaken for phone or Aadhaar numbers. Input that is not JSON is redacted as plain text.
func RedactJSON(raw []byte) []byte {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil || d.More() {
		return []byte(Redact(string(raw)))
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			return redactLeaf(t)
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return x
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(walk(v)) != nil {
		return []byte(Redact(string(raw)))
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func sortedTools(ts []Tool) []Tool {
	out := append([]Tool(nil), ts...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func schemaOrObject(s json.RawMessage) json.RawMessage {
	if len(s) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return s
}

func inputOrEmpty(s json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(s)) == 0 {
		return json.RawMessage("{}")
	}
	return s
}

// ---------- Anthropic ----------

const anthropicDefaultURL = "https://api.anthropic.com"

func (a *Anthropic) Step(ctx context.Context, r StepRequest) (StepResult, error) {
	model := r.Model
	if model == "" {
		model = a.model
	}
	maxTokens := r.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultStepTokens
	}
	var msgs []map[string]any
	for _, t := range r.Turns {
		switch t.Role {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": t.Text})
		case "assistant":
			content := t.Raw
			if len(content) == 0 {
				blocks := []any{}
				if t.Text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": t.Text})
				}
				for _, c := range t.Calls {
					blocks = append(blocks, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": inputOrEmpty(c.Input)})
				}
				b, _ := json.Marshal(blocks)
				content = b
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": content})
		case "tool":
			blocks := make([]any, len(t.Results))
			for i, x := range t.Results {
				blocks[i] = map[string]any{"type": "tool_result", "tool_use_id": x.CallID, "content": x.Content, "is_error": x.IsError}
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": blocks})
		}
	}
	if len(msgs) == 0 {
		return StepResult{}, errors.New("no turns to send")
	}
	body := map[string]any{"model": model, "max_tokens": maxTokens, "messages": msgs}
	if r.System != "" {
		body["system"] = r.System
	}
	if len(r.Tools) > 0 {
		ts := make([]any, 0, len(r.Tools))
		for _, t := range sortedTools(r.Tools) { // stable order keeps the cached prefix stable
			ts = append(ts, map[string]any{"name": t.Name, "description": t.Description, "input_schema": schemaOrObject(t.Schema)})
		}
		body["tools"] = ts
	}
	hdr := [][2]string{{"x-api-key", a.apiKey}, {"anthropic-version", "2023-06-01"}}
	// Never tool_choice, thinking or sampling parameters: forced choice and disabled thinking are 400s on current models.
	if model == "claude-sonnet-5-5" || model == "claude-haiku-5-5" {
		body["output_config"] = map[string]any{"effort": "medium"}
	}
	if a.baseURL == anthropicDefaultURL {
		body["cache_control"] = map[string]any{"type": "ephemeral"}
		if model == "claude-sonnet-5-5" { // Haiku 5.5 has no server-side fallback
			body["fallbacks"] = "default"
			hdr = append(hdr, [2]string{"anthropic-beta", "server-side-fallback-2026-07-01"})
		}
	}
	raw, err := postRaw(ctx, a.baseURL+"/v1/messages", hdr, body)
	if err != nil {
		return StepResult{}, err
	}
	var v struct {
		Model      string          `json:"model"`
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
		U          map[string]any  `json:"usage"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || len(v.Content) == 0 || v.Content[0] != '[' {
		return StepResult{}, errors.New("Anthropic response has no content")
	}
	var blocks []struct {
		Type, Text, ID, Name string
		Input                json.RawMessage
	}
	if err := json.Unmarshal(v.Content, &blocks); err != nil {
		return StepResult{}, errors.New("Anthropic response has no content")
	}
	res := StepResult{Raw: v.Content, Provider: "anthropic", Model: v.Model}
	var text strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			res.Calls = append(res.Calls, ToolCall{ID: b.ID, Name: b.Name, Input: inputOrEmpty(b.Input)})
		}
	}
	res.Text = text.String()
	switch v.StopReason {
	case "tool_use":
		res.Stop = "tools"
	case "max_tokens", "refusal":
		res.Stop = v.StopReason
	default: // end_turn, stop_sequence
		res.Stop = "end"
	}
	res.CacheRead = u32(v.U["cache_read_input_tokens"])
	res.TokensIn = u32(v.U["input_tokens"]) + u32(v.U["cache_creation_input_tokens"]) + res.CacheRead
	res.TokensOut = u32(v.U["output_tokens"])
	return res, nil
}

// ---------- OpenAI-compatible ----------

func (o *OpenAI) Step(ctx context.Context, r StepRequest) (StepResult, error) {
	model := r.Model
	if model == "" {
		model = o.model
	}
	maxTokens := r.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultStepTokens
	}
	msgs := []any{}
	if r.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": r.System})
	}
	for _, t := range r.Turns {
		switch t.Role {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": t.Text})
		case "assistant":
			if len(t.Raw) > 0 {
				msgs = append(msgs, t.Raw)
				continue
			}
			m := map[string]any{"role": "assistant", "content": t.Text}
			if len(t.Calls) > 0 {
				cs := make([]any, len(t.Calls))
				for i, c := range t.Calls {
					cs[i] = map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(inputOrEmpty(c.Input))}}
				}
				m["tool_calls"] = cs
			}
			msgs = append(msgs, m)
		case "tool":
			for _, x := range t.Results {
				msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": x.CallID, "content": x.Content})
			}
		}
	}
	body := map[string]any{"model": model, "max_tokens": maxTokens, "messages": msgs}
	if len(r.Tools) > 0 {
		ts := make([]any, 0, len(r.Tools))
		for _, t := range sortedTools(r.Tools) {
			ts = append(ts, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": schemaOrObject(t.Schema)}})
		}
		body["tools"] = ts
	}
	raw, err := postRaw(ctx, o.baseURL+"/chat/completions", o.auth(), body)
	if err != nil {
		return StepResult{}, err
	}
	var v struct {
		Model   string `json:"model"`
		Choices []struct {
			Message      json.RawMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Prompt  uint32 `json:"prompt_tokens"`
			Compl   uint32 `json:"completion_tokens"`
			Details struct {
				Cached uint32 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || len(v.Choices) == 0 || len(v.Choices[0].Message) == 0 {
		return StepResult{}, errors.New("OpenAI-compatible response has no message")
	}
	var m struct {
		Content   *string `json:"content"`
		ToolCalls []struct {
			ID       string                           `json:"id"`
			Function struct{ Name, Arguments string } `json:"function"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(v.Choices[0].Message, &m); err != nil {
		return StepResult{}, errors.New("OpenAI-compatible response has no message")
	}
	res := StepResult{Raw: v.Choices[0].Message, Provider: "openai", Model: v.Model,
		TokensIn: v.Usage.Prompt, TokensOut: v.Usage.Compl, CacheRead: v.Usage.Details.Cached}
	if m.Content != nil {
		res.Text = *m.Content
	}
	for _, c := range m.ToolCalls {
		in := json.RawMessage(c.Function.Arguments)
		if !json.Valid(in) {
			in = json.RawMessage("{}") // the registry validates input against the schema and refuses what is missing
		}
		res.Calls = append(res.Calls, ToolCall{ID: c.ID, Name: c.Function.Name, Input: in})
	}
	switch v.Choices[0].FinishReason {
	case "tool_calls":
		res.Stop = "tools"
	case "length":
		res.Stop = "max_tokens"
	case "content_filter":
		res.Stop = "refusal"
	default:
		res.Stop = "end"
	}
	if len(res.Calls) > 0 && res.Stop == "end" {
		res.Stop = "tools"
	}
	return res, nil
}

// ---------- Local (the CI fake) ----------

// Step is deterministic and offline. If the last turn is a user turn whose text contains `/tool <name> <json>`
// (rest of that line is the JSON input, default {}), it emits that one call with id local-<n>. After tool results it
// answers "Result of <name>:\n<content>" per result. Anything else is echoed, like Complete.
func (Local) Step(_ context.Context, r StepRequest) (StepResult, error) {
	res := StepResult{Provider: "local", Model: "local-extractive", Stop: "end"}
	if len(r.Turns) == 0 {
		return res, errors.New("no turns to send")
	}
	last := r.Turns[len(r.Turns)-1]
	switch last.Role {
	case "tool":
		names := map[string]string{}
		if len(r.Turns) > 1 {
			for _, c := range r.Turns[len(r.Turns)-2].Calls {
				names[c.ID] = c.Name
			}
		}
		parts := make([]string, len(last.Results))
		for i, x := range last.Results {
			parts[i] = "Result of " + names[x.CallID] + ":\n" + x.Content
		}
		res.Text = strings.Join(parts, "\n")
	default:
		res.Text = last.Text
		if i := strings.Index(last.Text, "/tool "); i >= 0 {
			line, _, _ := strings.Cut(last.Text[i+len("/tool "):], "\n")
			name, in, _ := strings.Cut(strings.TrimSpace(line), " ")
			in = strings.TrimSpace(in)
			if in == "" {
				in = "{}"
			}
			if name != "" && json.Valid([]byte(in)) {
				res.Calls = []ToolCall{{ID: "local-" + strconv.Itoa(len(r.Turns)), Name: name, Input: json.RawMessage(in)}}
				res.Text, res.Stop = "", "tools"
			}
		}
	}
	if len(res.Calls) == 0 {
		n := int(r.MaxTokens) * 4
		if r.MaxTokens == 0 {
			n = defaultStepTokens * 4
		}
		if rs := []rune(res.Text); len(rs) > min(n, 2000) {
			res.Text = string(rs[:min(n, 2000)])
		}
	}
	res.Raw, _ = json.Marshal(map[string]any{"text": res.Text, "calls": res.Calls})
	res.TokensIn, res.TokensOut = uint32(len(last.Text)/4), uint32(len(res.Text)/4)
	return res, nil
}

func (c Composite) Step(ctx context.Context, r StepRequest) (StepResult, error) {
	return c.Chat.Step(ctx, r)
}
