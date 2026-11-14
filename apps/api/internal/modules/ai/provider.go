package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Model-agnostic provider interface (crates/cnote-core/src/ai.rs) and the adapters of crates/cnote-ai/src/providers.rs.

const (
	EmbeddingDims         = 1536
	defaultAnthropicModel = "claude-sonnet-5-5"
	defaultOpenAIModel    = "gpt-4o-mini"
	defaultEmbeddingModel = "text-embedding-3-small"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Completion struct {
	Text, Provider, Model string
	TokensIn, TokensOut   uint32
}

type Provider interface {
	Name() string
	Complete(ctx context.Context, purpose string, msgs []Message, maxTokens uint32) (Completion, error)
	EmbeddingModel() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ---------- local (deterministic, offline) ----------

type Local struct{}

func (Local) Name() string           { return "local" }
func (Local) EmbeddingModel() string { return "local-hash-1536" }

func (Local) Complete(_ context.Context, _ string, msgs []Message, maxTokens uint32) (Completion, error) {
	last := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			last = msgs[i].Content
			break
		}
	}
	n := min(int(maxTokens)*4, 2000)
	text := last
	if r := []rune(last); len(r) > n {
		text = string(r[:n])
	}
	return Completion{Text: text, Provider: "local", Model: "local-extractive", TokensIn: uint32(len(last) / 4), TokensOut: uint32(len(text) / 4)}, nil
}

func (Local) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = HashEmbed(t)
	}
	return out, nil
}

func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Other_Alphabetic, r)
}

// HashEmbed is hashed bag-of-words, L2-normalised. shortcut: strings.ToLower has no final-sigma rule (Rust's
// to_lowercase does), so a word ending in a capital sigma hashes differently; offline dev provider only.
func HashEmbed(text string) []float32 {
	v := make([]float32, EmbeddingDims)
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !isAlnum(r) }) {
		if len(w) <= 2 {
			continue
		}
		h := sha256.Sum256([]byte(w))
		idx := int(binary.LittleEndian.Uint16(h[:2])) % EmbeddingDims
		if h[2]&1 == 0 {
			v[idx]++
		} else {
			v[idx]--
		}
	}
	var sum float32
	for _, x := range v {
		sum += float32(x * x) // explicit conversion: no fused multiply-add, matching Rust
	}
	if norm := float32(math.Sqrt(float64(sum))); norm > 0 {
		for i := range v {
			v[i] /= norm
		}
	}
	return v
}

// VecLiteral is Rust's vec_literal: f32 Display (shortest round-trip, never an exponent).
func VecLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(float64(x), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// ---------- HTTP ----------

var statusText = map[int]string{413: "Payload Too Large", 414: "URI Too Long", 416: "Range Not Satisfiable"}

func statusLine(code int) string { // like http::StatusCode Display
	t := http.StatusText(code)
	if s, ok := statusText[code]; ok {
		t = s
	}
	if t == "" {
		t = "<unknown status code>"
	}
	return fmt.Sprintf("%d %s", code, t)
}

func postJSON(ctx context.Context, url string, headers [][2]string, body any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("model request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	res, err := httpClient().Do(req)
	if err != nil {
		return nil, errors.New("model request failed")
	}
	defer res.Body.Close()
	text, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, errors.New("reading model response")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		// Upstream bodies stay out of responses; callers only learn the status.
		return nil, fmt.Errorf("model API returned %s", statusLine(res.StatusCode))
	}
	var v map[string]any
	d := json.NewDecoder(bytes.NewReader(text))
	if err := d.Decode(&v); err != nil || v == nil {
		return nil, errors.New("model API returned non-JSON")
	}
	return v, nil
}

func idx(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func str(v any) string { s, _ := v.(string); return s }

// u32 is `as_u64().unwrap_or(0) as u32`.
func u32(v any) uint32 {
	f, ok := v.(float64)
	if !ok || f < 0 || f != math.Trunc(f) {
		return 0
	}
	return uint32(uint64(f))
}

// ---------- Anthropic ----------

type Anthropic struct{ baseURL, apiKey, model string }

func NewAnthropic(apiKey string, baseURL, model *string) *Anthropic {
	a := &Anthropic{baseURL: "https://api.anthropic.com", apiKey: apiKey, model: defaultAnthropicModel}
	if baseURL != nil {
		a.baseURL = *baseURL
	}
	a.baseURL = strings.TrimRight(a.baseURL, "/")
	if model != nil {
		a.model = *model
	}
	return a
}

func (*Anthropic) Name() string           { return "anthropic" }
func (*Anthropic) EmbeddingModel() string { return "local-hash-1536" } // no embeddings API: hashed locally
func (*Anthropic) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return Local{}.Embed(ctx, texts)
}

func (a *Anthropic) Complete(ctx context.Context, _ string, msgs []Message, maxTokens uint32) (Completion, error) {
	var system []string
	rest := []Message{}
	for _, m := range msgs {
		if m.Role == "system" {
			system = append(system, m.Content)
		} else {
			rest = append(rest, m)
		}
	}
	if len(rest) == 0 {
		return Completion{}, errors.New("no user message to send")
	}
	body := map[string]any{"model": a.model, "max_tokens": maxTokens, "messages": rest}
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	v, err := postJSON(ctx, a.baseURL+"/v1/messages", [][2]string{{"x-api-key", a.apiKey}, {"anthropic-version", "2023-06-01"}}, body)
	if err != nil {
		return Completion{}, err
	}
	blocks, ok := v["content"].([]any)
	if !ok {
		return Completion{}, errors.New("Anthropic response has no content")
	}
	var text strings.Builder
	for _, b := range blocks {
		if idx(b, "type") == "text" {
			text.WriteString(str(idx(b, "text")))
		}
	}
	return Completion{Text: text.String(), Provider: "anthropic", Model: str(v["model"]),
		TokensIn: u32(idx(v, "usage", "input_tokens")), TokensOut: u32(idx(v, "usage", "output_tokens"))}, nil
}

// ---------- OpenAI-compatible ----------

type OpenAI struct{ baseURL, apiKey, model, embModel string }

func NewOpenAI(apiKey, baseURL, model, embModel *string) *OpenAI {
	o := &OpenAI{baseURL: "https://api.openai.com/v1", model: defaultOpenAIModel, embModel: defaultEmbeddingModel}
	if baseURL != nil {
		o.baseURL = *baseURL
	}
	o.baseURL = strings.TrimRight(o.baseURL, "/")
	if apiKey != nil {
		o.apiKey = *apiKey
	}
	if model != nil {
		o.model = *model
	}
	if embModel != nil {
		o.embModel = *embModel
	}
	return o
}

func (*OpenAI) Name() string             { return "openai" }
func (o *OpenAI) EmbeddingModel() string { return o.embModel }

func (o *OpenAI) auth() [][2]string {
	if o.apiKey == "" {
		return nil
	}
	return [][2]string{{"authorization", "Bearer " + o.apiKey}}
}

func (o *OpenAI) Complete(ctx context.Context, _ string, msgs []Message, maxTokens uint32) (Completion, error) {
	v, err := postJSON(ctx, o.baseURL+"/chat/completions", o.auth(), map[string]any{"model": o.model, "max_tokens": maxTokens, "messages": msgs})
	if err != nil {
		return Completion{}, err
	}
	text, ok := idx(v, "choices", 0, "message", "content").(string)
	if !ok {
		return Completion{}, errors.New("OpenAI-compatible response has no message content")
	}
	return Completion{Text: text, Provider: "openai", Model: str(v["model"]),
		TokensIn: u32(idx(v, "usage", "prompt_tokens")), TokensOut: u32(idx(v, "usage", "completion_tokens"))}, nil
}

func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]any{"model": o.embModel, "input": texts}
	if strings.HasPrefix(o.embModel, "text-embedding-3") { // Matryoshka models: pin to the column width
		body["dimensions"] = EmbeddingDims
	}
	v, err := postJSON(ctx, o.baseURL+"/embeddings", o.auth(), body)
	if err != nil {
		return nil, err
	}
	data, ok := v["data"].([]any)
	if !ok {
		return nil, errors.New("embeddings response has no data")
	}
	type row struct {
		i uint64
		e []float32
	}
	rows := make([]row, 0, len(data))
	for _, d := range data {
		arr, ok := idx(d, "embedding").([]any)
		if !ok {
			return nil, errors.New("embedding missing")
		}
		e := make([]float32, len(arr))
		for j, x := range arr {
			f, ok := x.(float64)
			if !ok {
				return nil, errors.New("bad float")
			}
			e[j] = float32(f)
		}
		var i uint64
		if f, ok := idx(d, "index").(float64); ok && f >= 0 && f == math.Trunc(f) {
			i = uint64(f)
		}
		rows = append(rows, row{i, e})
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].i < rows[b].i })
	if len(rows) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(rows))
	}
	out := make([][]float32, len(rows))
	for i, r := range rows {
		if len(r.e) != EmbeddingDims {
			return nil, fmt.Errorf("embedding has %d dimensions, the index needs %d", len(r.e), EmbeddingDims)
		}
		out[i] = r.e
	}
	return out, nil
}

// ---------- composition ----------

// Composite chats with one provider and embeds with another (Anthropic chat + OpenAI-compatible embeddings).
type Composite struct{ Chat, Embedder Provider }

func (c Composite) Name() string           { return c.Chat.Name() }
func (c Composite) EmbeddingModel() string { return c.Embedder.EmbeddingModel() }
func (c Composite) Complete(ctx context.Context, p string, m []Message, n uint32) (Completion, error) {
	return c.Chat.Complete(ctx, p, m, n)
}
func (c Composite) Embed(ctx context.Context, t []string) ([][]float32, error) {
	return c.Embedder.Embed(ctx, t)
}

func envVar(k string) *string {
	if v := os.Getenv(k); strings.TrimSpace(v) != "" {
		return &v
	}
	return nil
}

// FromEnv is the process-wide provider; with no keys it is the deterministic Local provider.
func FromEnv() Provider {
	model := envVar("AI_MODEL")
	var openai Provider
	if envVar("OPENAI_API_KEY") != nil || envVar("OPENAI_BASE_URL") != nil {
		openai = NewOpenAI(envVar("OPENAI_API_KEY"), envVar("OPENAI_BASE_URL"), model, envVar("OPENAI_EMBEDDING_MODEL"))
	}
	if key := envVar("ANTHROPIC_API_KEY"); key != nil {
		chat := NewAnthropic(*key, envVar("ANTHROPIC_BASE_URL"), model)
		if openai != nil {
			return Composite{chat, openai}
		}
		return chat
	}
	if openai != nil {
		return openai
	}
	return Local{}
}

var (
	processOnce sync.Once
	process     Provider
)

// Process is the provider of the registry (Rust `ctx.registry.ai`), built once from the environment.
func Process() Provider {
	processOnce.Do(func() { process = FromEnv() })
	return process
}

// For is a workspace's own endpoint (BYO-LLM).
func For(provider string, apiKey, baseURL, model, embModel *string) Provider {
	if provider == "anthropic" {
		k := ""
		if apiKey != nil {
			k = *apiKey
		}
		return NewAnthropic(k, baseURL, model)
	}
	return NewOpenAI(apiKey, baseURL, model, embModel)
}
