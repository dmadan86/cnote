package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

var errStepNotImplemented = errors.New("tool calling not implemented")

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
func (s *Session) ModelFor(tier string) string { return "" }

// Step makes one model call. It touches no database and never records anything.
func (s *Session) Step(ctx context.Context, r StepRequest) (StepResult, error) {
	return StepResult{}, fmt.Errorf("session step: %w", errStepNotImplemented)
}

// Record writes the ai.audit row and, unless the workspace brings its own key, the core.usage rows for one step.
func (s *Session) Record(c *command.Ctx, ref any, res StepResult) error {
	return record(c, s.r, res.Provider, res.Model, "agent.step", ref, nil, res.TokensIn, res.TokensOut)
}

// Complete runs a one-shot completion with the workspace's key, redaction, audit and metering.
func Complete(c *command.Ctx, purpose string, msgs []Message, maxTokens uint32) (Completion, error) {
	return complete(c, purpose, msgs, maxTokens)
}

// RedactJSON redacts string leaves only, so numbers and UUID strings survive. Wave 0 stub: returns raw unchanged.
func RedactJSON(raw []byte) []byte { return raw }

func (Local) Step(context.Context, StepRequest) (StepResult, error) {
	return StepResult{}, errStepNotImplemented
}
func (*Anthropic) Step(context.Context, StepRequest) (StepResult, error) {
	return StepResult{}, errStepNotImplemented
}
func (*OpenAI) Step(context.Context, StepRequest) (StepResult, error) {
	return StepResult{}, errStepNotImplemented
}
func (c Composite) Step(ctx context.Context, r StepRequest) (StepResult, error) {
	return c.Chat.Step(ctx, r)
}
