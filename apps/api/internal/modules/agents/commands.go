package agents

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

var idRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// defaultGroups mirrors command.defaultAgentGroups: the tool groups a workspace has before it sets agent_tool_groups.
var defaultGroups = []string{"read", "write_notes", "write_items", "manage_crm"}

type props = map[string]any

func sch(required []string, p props) json.RawMessage {
	o := props{"type": "object", "properties": p}
	if len(required) > 0 {
		o["required"] = required
	}
	b, _ := json.Marshal(o)
	return b
}

var (
	tStr  = props{"type": "string"}
	tInt  = props{"type": "integer"}
	tBool = props{"type": "boolean"}
	tID   = props{"type": "string", "description": "UUID"}
	tStrs = props{"type": "array", "items": tStr}
)

// Commands are the agent.* commands of section 2.3.
func Commands() []command.Command {
	mk := func(name string, g command.Group, role command.Role, person bool, desc string, schema json.RawMessage, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole, c.Description, c.Schema = role, desc, schema
		if person { // an agent can never start, change or cancel an agent
			inner := h
			c.Handler = func(c *command.Ctx, raw json.RawMessage) (any, error) {
				if c.Principal.Kind != command.KindUser {
					return nil, command.ErrForbidden("this is for people only; agents and API keys are refused")
				}
				return inner(c, raw)
			}
		}
		return c
	}
	R, W, A, M := command.GroupRead, command.GroupWriteNotes, command.GroupAdmin, command.Member
	return []command.Command{
		budgetCommand("agent.budget_reserve", budgetReserve),
		budgetCommand("agent.budget_settle", budgetSettle),
		mk("agent.templates", R, M, false, "List the AI employee templates (ask, briefing, lead_responder, support) with their tools, triggers, the workspace switches each needs, and the installed agent id.", sch(nil, props{}), templatesCmd),
		mk("agent.install", A, command.Admin, true, "Install an AI employee template as an agent owned by the caller. at is the daily time (IST) for the briefing; channel_id is where the briefing posts; quotes lets the lead responder draft quotations.",
			sch([]string{"template"}, props{"template": tStr, "at": tStr, "channel_id": tID, "quotes": tBool}), install),
		mk("agent.get", R, M, false, "Read one AI employee: instructions, tools, scopes, limits, triggers and schedule.", sch([]string{"id"}, props{"id": tID}), getCmd),
		mk("agent.update", A, command.Admin, true, "Change an AI employee. null resets instructions or tools to the template. Tools and scopes can only narrow the template's.",
			sch([]string{"id"}, props{"id": tID, "name": tStr, "instructions": tStr, "tools": tStrs, "scopes": tStrs, "model_tier": props{"type": "string", "enum": []string{"reasoning", "routine"}},
				"max_steps": tInt, "token_budget": tInt, "daily_runs": tInt, "enabled": tBool, "events": tStrs, "every_minutes": tInt, "at": tStr, "deliver_channel_id": tID}), update),
		mk("agent.ask", W, M, true, "Ask the workspace's Ask agent a question. Returns a run_id to poll with agent.run_get; conversation_id continues an earlier conversation.",
			sch([]string{"question"}, props{"question": tStr, "conversation_id": tID}), ask),
		mk("agent.run", A, command.Admin, true, "Start an AI employee now, optionally with an instruction.", sch([]string{"agent_id"}, props{"agent_id": tID, "input": tStr}), run),
		mk("agent.run_get", R, M, false, "Read one run with its steps (model text, tool calls, outcomes and held approvals). Visible to the person it ran for, the agent's owner and admins.", sch([]string{"id"}, props{"id": tID}), runGet),
		mk("agent.run_list", R, M, false, "List runs you may see, newest first. before is a created_at timestamp for the next page.",
			sch(nil, props{"agent_id": tID, "note_id": tID, "conversation_id": tID, "status": tStr, "limit": tInt, "before": tStr}), runList),
		mk("agent.run_cancel", W, M, true, "Cancel a queued or running run.", sch([]string{"id"}, props{"id": tID}), runCancel),
	}
}

func obj(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrInvalid("input must be a JSON object")
	}
	return m, nil
}

func (m objM) str(k string, max int) (string, bool, error) {
	v, ok := m[k]
	if !ok || string(v) == "null" {
		return "", false, nil
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", false, command.ErrInvalid(k + " must be a string")
	}
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > max {
		return "", false, command.ErrInvalid(k + " must be 1 to " + itoa(max) + " characters")
	}
	return s, true, nil
}

type objM map[string]json.RawMessage

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (m objM) id(k string, required bool) (string, bool, error) {
	s, ok, err := m.str(k, 36)
	if err != nil {
		return "", false, err
	}
	if required && !ok {
		return "", false, command.ErrInvalid(k + " is required")
	}
	if ok && !idRe.MatchString(s) {
		return "", false, command.ErrInvalid(k + " must be a UUID")
	}
	return s, ok, nil
}

func in(raw json.RawMessage) (objM, error) { m, err := obj(raw); return objM(m), err }

func me(c *command.Ctx) string { return *c.Principal.UserID }

// ---------- workspace state ----------

// wsState is the workspace's enabled agent tool groups and whether agents may join chat.
func wsState(c *command.Ctx) (groups []string, chatAgents bool, err error) {
	var g []string
	var chat bool
	err = c.Tx.QueryRow(c.Context, `select case when jsonb_typeof(settings->'agent_tool_groups') = 'array'
		then array(select jsonb_array_elements_text(settings->'agent_tool_groups')) end, coalesce(settings->>'chat_agents' = 'true', false)
		from core.workspaces where id = $1::uuid`, c.WorkspaceID()).Scan(&g, &chat)
	if err != nil {
		return nil, false, command.DB(err)
	}
	if g == nil {
		g = slices.Clone(defaultGroups)
	}
	return g, chat, nil
}

func missing(have, need []string) []string {
	out := []string{}
	for _, n := range need {
		if !slices.Contains(have, n) {
			out = append(out, n)
		}
	}
	return out
}

func baseTools(d Template) []string {
	out := []string{}
	for _, t := range ToolAllowlist[d.Key] {
		if !slices.Contains(d.OptionalTools, t) {
			out = append(out, t)
		}
	}
	return out
}

// ---------- agent.templates / agent.get ----------

func templatesCmd(c *command.Ctx, _ json.RawMessage) (any, error) {
	groups, chat, err := wsState(c)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, d := range Defs {
		var id *string
		if err := c.Tx.QueryRow(c.Context, `select id::text from core.agents where template = $1`, d.Key).Scan(&id); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, command.DB(err)
		}
		ev, every := d.Events, any(nil)
		if ev == nil {
			ev = []string{}
		}
		if d.EveryMinutes > 0 {
			every = d.EveryMinutes
		}
		out = append(out, props{"key": d.Key, "name": d.Name, "description": d.Description, "tier": d.Tier, "scopes": d.Scopes, "tools": baseTools(d),
			"events": ev, "every_minutes": every, "needs": props{"groups_off": missing(groups, d.Scopes), "chat_agents": d.ChatAgents && !chat}, "agent_id": id})
	}
	return out, nil
}

type agentOut struct {
	ID                  string      `json:"id"`
	Name                string      `json:"name"`
	Description         string      `json:"description"`
	Kind                string      `json:"kind"`
	Template            *string     `json:"template"`
	OwnerUserID         string      `json:"owner_user_id"`
	OwnerName           string      `json:"owner_name"`
	Enabled             bool        `json:"enabled"`
	Runtime             bool        `json:"runtime"`
	Instructions        *string     `json:"instructions"`
	InstructionsDefault *string     `json:"instructions_default"`
	Tools               []string    `json:"tools"`
	Scopes              []string    `json:"scopes"`
	ModelTier           string      `json:"model_tier"`
	MaxSteps            int         `json:"max_steps"`
	TokenBudget         int         `json:"token_budget"`
	DailyRuns           int         `json:"daily_runs"`
	Events              []string    `json:"events"`
	EveryMinutes        *int        `json:"every_minutes"`
	NextRunAt           *command.Ts `json:"next_run_at"`
	DeliverChannelID    *string     `json:"deliver_channel_id"`
	CreatedAt           command.Ts  `json:"created_at"`
}

func loadAgent(c *command.Ctx, id string) (agentOut, error) {
	var a agentOut
	var tools []string
	var next *time.Time
	var created time.Time
	err := c.Tx.QueryRow(c.Context, `select a.id::text, a.name, a.description, a.kind, a.template, a.owner_user_id::text, coalesce(u.name,''), a.enabled,
		a.instructions, a.tools, a.scopes, a.model_tier, a.max_steps, a.token_budget, a.daily_runs, a.events, a.every_minutes, a.next_run_at,
		a.deliver_channel_id::text, a.created_at from core.agents a left join core.users u on u.id = a.owner_user_id where a.id = $1::uuid`, id).
		Scan(&a.ID, &a.Name, &a.Description, &a.Kind, &a.Template, &a.OwnerUserID, &a.OwnerName, &a.Enabled, &a.Instructions, &tools, &a.Scopes,
			&a.ModelTier, &a.MaxSteps, &a.TokenBudget, &a.DailyRuns, &a.Events, &a.EveryMinutes, &next, &a.DeliverChannelID, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, command.ErrNotFound("agent")
	}
	if err != nil {
		return a, command.DB(err)
	}
	a.Runtime = a.Template != nil || a.Instructions != nil
	a.NextRunAt, a.CreatedAt = command.TsPtr(next), command.Ts(created)
	a.Tools = tools
	if a.Template != nil {
		if d, ok := Def(*a.Template); ok {
			a.InstructionsDefault = &d.Instructions
			if tools == nil {
				a.Tools = slices.Clone(ToolAllowlist[d.Key])
			}
		}
	}
	for _, s := range []*[]string{&a.Tools, &a.Scopes, &a.Events} {
		if *s == nil {
			*s = []string{}
		}
	}
	return a, nil
}

func getCmd(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	return loadAgent(c, id)
}

// ---------- agent.install ----------

func install(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	key, _, err := m.str("template", 40)
	if err != nil {
		return nil, err
	}
	d, ok := Def(key)
	if !ok {
		return nil, command.ErrInvalid("template must be one of " + strings.Join(Templates, ", "))
	}
	at, hasAt, err := m.str("at", 5)
	if err != nil {
		return nil, err
	}
	channel, hasChannel, err := m.id("channel_id", false)
	if err != nil {
		return nil, err
	}
	var quotes bool
	if v, ok := m["quotes"]; ok && json.Unmarshal(v, &quotes) != nil {
		return nil, command.ErrInvalid("quotes must be true or false")
	}
	if (hasAt || hasChannel) && !d.ChatAgents || hasChannel && d.Key == TemplateCollections || quotes && d.Key != TemplateLeadResponder {
		return nil, command.ErrInvalid("at and channel_id are for the briefing, quotes is for the lead responder")
	}
	var exists bool
	if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from core.agents where template = $1)`, d.Key).Scan(&exists); err != nil {
		return nil, command.DB(err)
	}
	if exists {
		return nil, command.ErrConflict(d.Name + " is already installed")
	}
	scopes, tools := slices.Clone(d.Scopes), []string(nil)
	if quotes {
		scopes = append(scopes, d.OptionalScopes...)
	} else if len(d.OptionalTools) > 0 {
		tools = baseTools(d) // narrowed: no quote tools without the quotes switch
	}
	var every *int
	var next *time.Time
	if d.EveryMinutes > 0 {
		e := d.EveryMinutes
		if !hasAt {
			at = "08:30"
			if d.Weekly {
				at = "09:30"
			}
		}
		n, err := nextAt(at, time.Now())
		if err != nil {
			return nil, err
		}
		for d.Weekly && n.In(ist).Weekday() != time.Monday {
			n = n.AddDate(0, 0, 1)
		}
		every, next = &e, &n
	}
	if hasChannel {
		if err := channelUsable(c, channel); err != nil {
			return nil, err
		}
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, `insert into core.agents (workspace_id, name, kind, owner_user_id, scopes, description, template, tools, model_tier, events, every_minutes, next_run_at)
		values ($1::uuid, $2, 'internal', $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11) returning id::text`,
		c.WorkspaceID(), d.Name, me(c), scopes, d.Description, d.Key, tools, d.Tier, nonNil(d.Events), every, next).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	groups, chat, err := wsState(c)
	if err != nil {
		return nil, err
	}
	settings := props{}
	if miss := missing(groups, scopes); len(miss) > 0 {
		settings["agent_tool_groups"] = append(slices.Clone(groups), miss...)
	}
	if d.ChatAgents && !chat {
		settings["chat_agents"] = true
	}
	if len(settings) > 0 {
		if _, err := c.Call("workspace.update", props{"settings": settings}); err != nil {
			return nil, err
		}
	}
	if d.ChatAgents && d.Key != TemplateCollections { // the collections brief goes by direct message, never to a channel
		if !hasChannel {
			if channel, err = briefingChannel(c); err != nil {
				return nil, err
			}
		}
		if _, err := c.Call("chat.agent_add", props{"channel_id": channel, "agent_id": id}); err != nil {
			return nil, err
		}
		if _, err := c.Tx.Exec(c.Context, `update core.agents set deliver_channel_id = $2::uuid where id = $1::uuid`, id, channel); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Emit("agent.installed", nil, props{"agent_id": id, "template": d.Key}); err != nil {
		return nil, err
	}
	return loadAgent(c, id)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type chanRow struct{ ID, Kind, Name string }

func channels(c *command.Ctx) ([]chanRow, error) {
	b, err := c.Call("chat.channel_list", props{})
	if err != nil {
		return nil, err
	}
	var rows []chanRow
	if json.Unmarshal(b, &rows) != nil {
		return nil, command.ErrInvalid("could not read the channel list")
	}
	return rows, nil
}

// channelUsable: a channel the caller can see that is not a direct message (chat itself enforces who may post).
func channelUsable(c *command.Ctx, id string) error {
	rows, err := channels(c)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.ID == id && r.Kind != "dm" {
			return nil
		}
	}
	return command.ErrNotFound("channel")
}

// briefingChannel finds or creates the #daily-briefing channel.
func briefingChannel(c *command.Ctx) (string, error) {
	rows, err := channels(c)
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.Kind != "dm" && r.Name == "daily-briefing" {
			return r.ID, nil
		}
	}
	b, err := c.Call("chat.channel_create", props{"name": "daily-briefing"})
	if err != nil {
		return "", err
	}
	var out struct{ ID string }
	if json.Unmarshal(b, &out) != nil || out.ID == "" {
		return "", command.ErrInvalid("could not create the briefing channel")
	}
	return out.ID, nil
}

// ---------- agent.update ----------

func update(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	cur, err := loadAgent(c, id)
	if err != nil {
		return nil, err
	}
	if cur.Kind != "internal" {
		return nil, command.ErrInvalid("only AI employees can be changed here")
	}
	var def Template
	if cur.Template != nil {
		def, _ = Def(*cur.Template)
	}
	var sets []string
	var args []any
	var keys []string
	set := func(col, cast string, v any) {
		args = append(args, v)
		sets = append(sets, col+" = $"+itoa(len(args)+1)+cast)
	}
	isNull := func(k string) bool { return string(m[k]) == "null" }
	for k := range m {
		if k != "id" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, command.ErrInvalid("give at least one field to change")
	}
	for _, k := range keys {
		v := m[k]
		switch k {
		case "name":
			s, _, err := m.str(k, 80)
			if err != nil {
				return nil, err
			}
			set("name", "", s)
		case "instructions":
			if isNull(k) {
				set("instructions", "", nil)
				break
			}
			s, _, err := m.str(k, 8000)
			if err != nil {
				return nil, err
			}
			set("instructions", "", s)
		case "tools":
			if isNull(k) {
				set("tools", "", nil)
				break
			}
			var l []string
			if json.Unmarshal(v, &l) != nil || len(l) == 0 || len(l) > 40 {
				return nil, command.ErrInvalid("tools must be a list of 1 to 40 command names, or null for the template's")
			}
			if cur.Template == nil {
				return nil, command.ErrInvalid("tools can only be narrowed on a template agent")
			}
			for _, t := range l {
				if !slices.Contains(ToolAllowlist[def.Key], t) {
					return nil, command.ErrInvalid(t + " is not in this template's tool list")
				}
			}
			set("tools", "", l)
		case "scopes":
			var l []string
			if json.Unmarshal(v, &l) != nil || cur.Template == nil {
				return nil, command.ErrInvalid("scopes must be a list, on a template agent")
			}
			for _, s := range l {
				if !slices.Contains(def.Scopes, s) && !slices.Contains(def.OptionalScopes, s) {
					return nil, command.ErrInvalid(s + " is not a scope this template may have")
				}
			}
			set("scopes", "", nonNil(l))
		case "model_tier":
			var s string
			if json.Unmarshal(v, &s) != nil || s != "reasoning" && s != "routine" {
				return nil, command.ErrInvalid("model_tier must be reasoning or routine")
			}
			set("model_tier", "", s)
		case "max_steps", "token_budget", "daily_runs":
			lim := map[string][2]int{"max_steps": {1, 40}, "token_budget": {5000, 2000000}, "daily_runs": {1, 5000}}[k]
			var n int
			if json.Unmarshal(v, &n) != nil || n < lim[0] || n > lim[1] {
				return nil, command.ErrInvalid(k + " must be " + itoa(lim[0]) + " to " + itoa(lim[1]))
			}
			set(k, "", n)
		case "enabled":
			var b bool
			if json.Unmarshal(v, &b) != nil {
				return nil, command.ErrInvalid("enabled must be true or false")
			}
			set("enabled", "", b)
		case "events":
			var l []string
			if json.Unmarshal(v, &l) != nil || len(l) > 20 {
				return nil, command.ErrInvalid("events must be a list of at most 20 event kind prefixes")
			}
			for _, e := range l {
				if e = strings.TrimSpace(e); e == "" || e == "*" || len(e) > 100 || strings.HasPrefix(e, "agent.") {
					return nil, command.ErrInvalid("events must be specific event kind prefixes (not * and not agent.*)")
				}
			}
			set("events", "", nonNil(l))
		case "every_minutes", "at":
			// handled together below
		case "deliver_channel_id":
			if isNull(k) {
				set(k, "::uuid", nil)
				break
			}
			ch, _, err := m.id(k, true)
			if err != nil {
				return nil, err
			}
			if err := channelUsable(c, ch); err != nil {
				return nil, err
			}
			if _, err := c.Call("chat.agent_add", props{"channel_id": ch, "agent_id": id}); err != nil {
				return nil, err
			}
			set(k, "::uuid", ch)
		default:
			return nil, command.ErrInvalid("unknown field " + k)
		}
	}
	if _, ok := m["every_minutes"]; ok || m["at"] != nil {
		every := cur.EveryMinutes
		if ok {
			if isNull("every_minutes") {
				every = nil
			} else {
				var n int
				if json.Unmarshal(m["every_minutes"], &n) != nil || n < 5 || n > 525600 {
					return nil, command.ErrInvalid("every_minutes must be 5 to 525600, or null to stop the schedule")
				}
				every = &n
			}
			set("every_minutes", "", every)
		}
		var next *time.Time
		if every != nil {
			n := time.Now().Add(time.Duration(*every) * time.Minute)
			if at, hasAt, err := m.str("at", 5); err != nil {
				return nil, err
			} else if hasAt {
				if n, err = nextAt(at, time.Now()); err != nil {
					return nil, err
				}
			} else if !ok {
				return nil, command.ErrInvalid("at is the time of day for a schedule")
			}
			next = &n
		} else if m["at"] != nil {
			return nil, command.ErrInvalid("at needs a schedule: set every_minutes first")
		}
		set("next_run_at", "", next)
	}
	if len(sets) == 0 {
		return nil, command.ErrInvalid("nothing to change")
	}
	if _, err := c.Tx.Exec(c.Context, "update core.agents set "+strings.Join(sets, ", ")+" where id = $1::uuid", append([]any{id}, args...)...); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("agent.updated", nil, props{"agent_id": id, "keys": keys}); err != nil {
		return nil, err
	}
	return loadAgent(c, id)
}

// ---------- agent.ask / agent.run ----------

func ask(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	q, _, err := m.str("question", 4000)
	if err != nil {
		return nil, err
	}
	if _, ok := m["question"]; !ok {
		return nil, command.ErrInvalid("question is required")
	}
	conv, hasConv, err := m.id("conversation_id", false)
	if err != nil {
		return nil, err
	}
	var agent string
	err = c.Tx.QueryRow(c.Context, `select id::text from core.agents where template = $1 and enabled`, TemplateAsk).Scan(&agent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrInvalid("the Ask agent is not installed or is paused; an admin can install it in settings")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if hasConv { // a conversation id is only ever one the caller already has
		var ok bool
		if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from core.agent_runs where conversation_id = $1::uuid and on_behalf_of = $2::uuid and trigger = 'ask')`, conv, me(c)).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if !ok {
			return nil, command.ErrNotFound("conversation")
		}
	} else if err := c.Tx.QueryRow(c.Context, `select gen_random_uuid()::text`).Scan(&conv); err != nil {
		return nil, command.DB(err)
	}
	rid, _, err := CreateRun(c, NewRun{AgentID: agent, OnBehalfOf: me(c), Trigger: "ask", Input: q, ConversationID: &conv})
	if err != nil {
		return nil, err
	}
	return props{"run_id": rid, "conversation_id": conv}, nil
}

func run(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("agent_id", true)
	if err != nil {
		return nil, err
	}
	input, _, err := m.str("input", 4000)
	if err != nil {
		return nil, err
	}
	a, err := loadAgent(c, id)
	if err != nil {
		return nil, err
	}
	if !a.Runtime || !a.Enabled {
		return nil, command.ErrInvalid("this agent cannot run here: it is paused or not an AI employee")
	}
	rid, _, err := CreateRun(c, NewRun{AgentID: id, OnBehalfOf: me(c), Trigger: "manual", Input: input})
	if err != nil {
		return nil, err
	}
	return props{"run_id": rid}, nil
}

// ---------- runs ----------

const runSelect = `select r.id::text, r.agent_id::text, a.name, r.trigger, r.status, r.on_behalf_of::text, coalesce(u.name,''), r.conversation_id::text, r.note_id::text,
	r.input, r.output, r.error, r.steps, r.held,
	(select count(*) from core.agent_run_steps s join core.confirmations cf on cf.id = s.confirmation_id where s.run_id = r.id and cf.status = 'pending'),
	r.tokens_in, r.tokens_out, r.provider, r.model, r.created_at, r.started_at, r.finished_at
	from core.agent_runs r join core.agents a on a.id = r.agent_id left join core.users u on u.id = r.on_behalf_of`

// runVisible is the per-object rule: the person it ran for, the agent's owner, and admins ($1 admin, $2 caller).
const runVisible = ` ($1 or r.on_behalf_of = $2::uuid or a.owner_user_id = $2::uuid)`

func viewer(c *command.Ctx) (bool, *string) {
	return c.Principal.Role >= command.Admin, c.Principal.UserID
}

func scanRuns(rows pgx.Rows) ([]props, error) {
	defer rows.Close()
	out := []props{}
	for rows.Next() {
		var id, agent, agentName, trig, status, behalf, behalfName, input string
		var conv, note, output, errMsg, prov, model *string
		var steps, held, pending, tin, tout int64
		var created time.Time
		var started, finished *time.Time
		if err := rows.Scan(&id, &agent, &agentName, &trig, &status, &behalf, &behalfName, &conv, &note, &input, &output, &errMsg, &steps, &held, &pending,
			&tin, &tout, &prov, &model, &created, &started, &finished); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, props{"id": id, "agent_id": agent, "agent_name": agentName, "trigger": trig, "status": status, "on_behalf_of": behalf, "on_behalf_name": behalfName,
			"conversation_id": conv, "note_id": note, "input": input, "output": output, "error": errMsg, "steps": steps, "held": held, "pending": pending,
			"tokens_in": tin, "tokens_out": tout, "provider": prov, "model": model, "created_at": command.Ts(created), "started_at": command.TsPtr(started), "finished_at": command.TsPtr(finished)})
	}
	return out, command.DB(rows.Err())
}

func oneRun(c *command.Ctx, id string) (props, error) {
	admin, me := viewer(c)
	rows, err := c.Tx.Query(c.Context, runSelect+` where r.id = $3::uuid and`+runVisible, admin, me, id)
	if err != nil {
		return nil, command.DB(err)
	}
	l, err := scanRuns(rows)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, command.ErrNotFound("run") // also for a run you may not see
	}
	return l[0], nil
}

func runGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	r, err := oneRun(c, id)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select s.seq, s.kind, s.text, s.stop, s.tokens_in, s.tokens_out, s.command, s.input, s.result, s.outcome, s.ms, s.created_at,
		s.confirmation_id::text, cf.kind, cf.status from core.agent_run_steps s left join core.confirmations cf on cf.id = s.confirmation_id where s.run_id = $1::uuid order by s.seq`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	steps := []props{}
	for rows.Next() {
		var seq int
		var kind string
		var text, stop, cmd, result, outcome, cid, ckind, cstatus *string
		var tin, tout, ms *int
		var inp json.RawMessage
		var at time.Time
		if err := rows.Scan(&seq, &kind, &text, &stop, &tin, &tout, &cmd, &inp, &result, &outcome, &ms, &at, &cid, &ckind, &cstatus); err != nil {
			return nil, command.DB(err)
		}
		if kind == "model" {
			steps = append(steps, props{"seq": seq, "kind": kind, "text": text, "stop": stop, "tokens_in": tin, "tokens_out": tout, "created_at": command.Ts(at)})
			continue
		}
		var conf any
		if cid != nil {
			conf = props{"id": *cid, "kind": ckind, "status": cstatus}
		}
		steps = append(steps, props{"seq": seq, "kind": kind, "command": cmd, "input": inp, "result": result, "outcome": outcome, "confirmation": conf, "ms": ms, "created_at": command.Ts(at)})
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	return props{"run": r, "steps": steps}, nil
}

func runList(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	var ids [3]*string
	for i, k := range []string{"agent_id", "note_id", "conversation_id"} {
		s, ok, err := m.id(k, false)
		if err != nil {
			return nil, err
		}
		if ok {
			ids[i] = &s
		}
	}
	var status *string
	if s, ok, err := m.str("status", 20); err != nil {
		return nil, err
	} else if ok {
		status = &s
	}
	limit := 50
	if v, ok := m["limit"]; ok && string(v) != "null" && (json.Unmarshal(v, &limit) != nil || limit < 1 || limit > 200) {
		return nil, command.ErrInvalid("limit must be 1 to 200")
	}
	var before *time.Time
	if s, ok, err := m.str("before", 40); err != nil {
		return nil, err
	} else if ok {
		t, e := time.Parse(time.RFC3339Nano, s)
		if e != nil {
			return nil, command.ErrInvalid("before must be a created_at timestamp")
		}
		before = &t
	}
	admin, who := viewer(c)
	rows, err := c.Tx.Query(c.Context, runSelect+` where`+runVisible+` and ($3::uuid is null or r.agent_id = $3::uuid) and ($4::uuid is null or r.note_id = $4::uuid)
		and ($5::uuid is null or r.conversation_id = $5::uuid) and ($6::text is null or r.status = $6) and ($7::timestamptz is null or r.created_at < $7)
		order by r.created_at desc, r.id limit $8`, admin, who, ids[0], ids[1], ids[2], status, before, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	return scanRuns(rows)
}

func runCancel(c *command.Ctx, raw json.RawMessage) (any, error) {
	m, err := in(raw)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	r, err := oneRun(c, id)
	if err != nil {
		return nil, err
	}
	var status string
	err = c.Tx.QueryRow(c.Context, `update core.agent_runs set status = 'cancelled', error = 'cancelled', finished_at = now()
		where id = $1::uuid and status in ('queued','running') returning status`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return props{"status": r["status"]}, nil // already finished: nothing to cancel
	}
	return props{"status": status}, command.DB(err)
}

func budgetCommand(name string, handler command.Handler) command.Command {
	c := command.NewCommand(name, command.GroupRead, handler)
	c.Internal = true
	return c
}
func budgetReserve(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		Tokens int64 `json:"tokens"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Tokens <= 0 {
		return nil, command.ErrInvalid("tokens must be positive")
	}
	day := time.Now().UTC().Format("2006-01-02")
	if err := reserve(c, day, in.Tokens); err != nil {
		return nil, err
	}
	return map[string]string{"day": day}, nil
}
func budgetSettle(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		Day      string `json:"day"`
		Reserved int64  `json:"reserved"`
		Used     int64  `json:"used"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Reserved < 0 || in.Used < 0 {
		return nil, command.ErrInvalid("invalid budget settlement")
	}
	if _, err := time.Parse("2006-01-02", in.Day); err != nil {
		return nil, command.ErrInvalid("invalid budget day")
	}
	if err := settle(c, in.Day, in.Reserved, in.Used); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}
