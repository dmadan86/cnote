package agents

import (
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mcp"
	"github.com/cnote/api/internal/modules/ai"
	"github.com/cnote/api/internal/serdecheck"
)

const (
	maxTools       = 40
	maxResultChars = 24_000
	heldSuffix     = " Held for a person's approval before it takes effect."
)

// toolSet is the per-run offer: what the model sees and how a tool name maps back to a command (underscores are
// ambiguous, so the map is the only way back).
type toolSet struct {
	tools []ai.Tool
	cmds  map[string]string // tool name -> command
}

// buildTools offers the template allowlist, narrowed by the agent's own `tools`, to the model (16.3, section 4). A
// command is offered only with a description and schema, outside the admin and destructive groups, returning no
// secret, never an agent.* command, and only when the run's principal passes the gate. c carries that principal.
func buildTools(c *command.Ctx, reg *command.Registry, template string, narrow []string) toolSet {
	ts := toolSet{cmds: map[string]string{}}
	allow := ToolAllowlist[template]
	cat := map[string]mcp.Entry{}
	for _, e := range mcp.Catalog(reg) {
		cat[e.Name] = e
	}
	names := slices.Clone(allow)
	slices.Sort(names)
	for _, name := range slices.Compact(names) {
		if narrow != nil && !slices.Contains(narrow, name) {
			continue
		}
		e, ok := cat[name]
		cmd, known := reg.Get(name)
		if !ok || !known || e.Description == "" || len(e.Schema) == 0 || cmd.ReturnsSecret || strings.HasPrefix(name, "agent.") ||
			cmd.Group == command.GroupAdmin || cmd.Group == command.GroupDestructive || c.Allowed(name) != nil {
			continue
		}
		tn := mcp.ToolName(name)
		if _, dup := ts.cmds[tn]; dup || len(ts.tools) >= maxTools {
			continue
		}
		desc := e.Description
		if cmd.Confirm || e.Confirm {
			desc += heldSuffix
		}
		ts.tools = append(ts.tools, ai.Tool{Name: tn, Description: desc, Schema: e.Schema})
		ts.cmds[tn] = name
	}
	return ts
}

// subjectFree commands read workspace-wide and so may carry ids that are not the run's subject (16.3).
func subjectFree(name string) bool {
	return strings.HasPrefix(name, "report.") || name == "home.summary" || name == "stock_item.list" || name == "quote.list"
}

var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// idsIn collects the uuid strings held under `id`, `*_id`, `ids` and `*_ids` keys, at any depth.
func idsIn(raw []byte) []string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				walk(k, e)
			}
		case []any:
			for _, e := range x {
				walk(key, e)
			}
		case string:
			if (key == "id" || key == "ids" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_ids")) && uuidRe.MatchString(x) {
				out = append(out, strings.ToLower(x))
			}
		}
	}
	walk("", v)
	return out
}

// allUUIDs collects every uuid string of a JSON value (the run context names the subject records).
func allUUIDs(raw []byte) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			if uuidRe.MatchString(x) {
				out = append(out, strings.ToLower(x))
			}
		}
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		walk(v)
	}
	return out
}

// subjectViolation names the first id in input that is not one of the run's subject records ("" when fine). Only
// event runs are bound: their subject is the triggering note plus whatever the trigger put in the run context.
func subjectViolation(trigger, command string, input []byte, subject map[string]bool) string {
	if trigger != "event" || subjectFree(command) {
		return ""
	}
	for _, id := range idsIn(input) {
		if !subject[id] {
			return id
		}
	}
	return ""
}

// untrusted wraps customer-supplied text so the model treats it as data.
func untrusted(s string) string {
	s = strings.NewReplacer("<untrusted_data>", "", "</untrusted_data>", "").Replace(s)
	return "<untrusted_data>\n" + s + "\n</untrusted_data>"
}

func errJSON(code command.Code, msg string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": string(code), "message": msg}})
	return string(b)
}

// clip cuts a tool result at maxResultChars with a note to narrow the query.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= maxResultChars {
		return s
	}
	r := []rune(s)
	return string(r[:maxResultChars]) + "\n[result cut: narrow the query with filters or a limit]"
}

// callOutcome shapes a command result for the model: its text, whether it is an error, the step outcome
// (ok | error | denied | held) and the confirmation id when held.
func callOutcome(name string, schema, input json.RawMessage, out []byte, err error) (text, outcome, confirmationID string, isErr bool) {
	var ce *command.Error
	switch {
	case err == nil:
		var v struct {
			Confirmation bool   `json:"confirmation_required"`
			Approval     bool   `json:"approval_required"`
			ID           string `json:"confirmation_id"`
		}
		if json.Unmarshal(out, &v) == nil && (v.Confirmation || v.Approval) && v.ID != "" {
			b, _ := json.Marshal(map[string]any{"status": "held_for_approval", "confirmation_id": v.ID,
				"message": "A person must approve this in C Note before it takes effect. Do not retry."})
			return string(b), "held", v.ID, false
		}
		return clip(string(out)), "ok", "", false
	case errors.Is(err, command.ErrFallback):
		return errJSON(command.Invalid, "invalid input: "+serdecheck.Describe(name, schema, input)), "error", "", true
	case errors.As(err, &ce):
		o := "error"
		if ce.Code == command.Forbidden {
			o = "denied"
		}
		return errJSON(ce.Code, ce.Message), o, "", true
	}
	slog.Error("agent tool failed", "command", name, "error", err)
	return errJSON(command.Internal, "internal error"), "error", "", true
}

// prepareInput normalises a model's tool arguments like mcp.exec does. ok is false for input that is not a JSON object.
func prepareInput(reg *command.Registry, name string, raw json.RawMessage) (json.RawMessage, bool) {
	if len(strings.TrimSpace(string(raw))) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil && obj != nil { // meta-arguments belong to the runtime, not the model
		delete(obj, "idempotency_key")
		delete(obj, "confirmation_id")
		raw, _ = command.Marshal(obj)
	}
	return serdecheck.NormalizeUUIDs(mcp.InputSchema(reg, name), serdecheck.EmptySequence(name, raw)), true
}
