package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules/projects"
)

// Port of the execution half of crates/cnote-projects/src/automation.rs (the automation.* commands are
// ported in modules/projects). Actions run through the command gate with a system principal capped at the role
// of the rule's author.

const (
	maxRunsPerMinute  = 10
	scheduleItemCap   = 100
	automationColumns = "id::text, space_id::text, trigger, conditions, actions, created_by::text"
)

type autoRow struct {
	id, spaceID                  *string
	trigger, conditions, actions json.RawMessage
	createdBy                    *string
}

type trigger struct {
	Kind         string  `json:"kind"`
	Event        string  `json:"event"`
	CNQL         *string `json:"cnql"`
	EveryMinutes *uint32 `json:"every_minutes"`
}

// parseTrigger mirrors serde's internally tagged enum: unknown kinds or missing fields are errors.
func parseTrigger(raw json.RawMessage) (trigger, bool) {
	var t trigger
	if json.Unmarshal(raw, &t) != nil {
		return t, false
	}
	switch t.Kind {
	case "event":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		_, ok := m["event"]
		return t, ok
	case "schedule":
		return t, t.EveryMinutes != nil
	}
	return t, false
}

type action struct {
	Type    string          `json:"type"`
	To      string          `json:"to"`
	UserID  *string         `json:"user_id"`
	AgentID *string         `json:"agent_id"`
	Body    string          `json:"body"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
}

func subst(v any, id *string, key string) any {
	switch x := v.(type) {
	case string:
		r := ""
		if id != nil {
			r = *id
		}
		return strings.ReplaceAll(strings.ReplaceAll(x, "{{item.id}}", r), "{{item.key}}", key)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = subst(e, id, key)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = subst(e, id, key)
		}
		return out
	}
	return v
}

func decodeAny(raw json.RawMessage) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	return v, d.Decode(&v)
}

func errMessage(err error) string {
	var ce *command.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

func runAction(c *command.Ctx, a action, item *string, key string) error {
	needItem := func() (string, error) {
		if item == nil {
			return "", command.ErrInvalid("this action needs an item; the trigger has none")
		}
		return *item, nil
	}
	var err error
	switch a.Type {
	case "transition":
		var id string
		if id, err = needItem(); err == nil {
			_, err = c.Call("item.transition", map[string]any{"id": id, "to": a.To})
		}
	case "assign":
		var id string
		if id, err = needItem(); err == nil {
			_, err = c.Call("item.assign", map[string]any{"id": id, "user_id": a.UserID, "agent_id": a.AgentID})
		}
	case "comment":
		var id string
		if id, err = needItem(); err == nil {
			_, err = c.Call("comment.add", map[string]any{"note_id": id, "body": subst(a.Body, item, key)})
		}
	case "command":
		var in any
		if in, err = decodeAny(a.Input); err == nil {
			_, err = c.Call(a.Name, subst(in, item, key))
		}
	default:
		err = command.ErrInvalid("unknown action")
	}
	return err
}

type outcome struct {
	kind string // skipped | success | failed
	why  string
	log  []any
}

func execTarget(c *command.Ctx, a autoRow, item *string) (outcome, error) {
	var conds []struct {
		CNQL string `json:"cnql"`
	}
	_ = json.Unmarshal(a.conditions, &conds)
	for _, cd := range conds {
		ok := false
		if item != nil {
			var err error
			if ok, err = projects.ItemMatches(c, *item, cd.CNQL); err != nil {
				return outcome{}, err
			}
		}
		if !ok {
			return outcome{kind: "skipped", why: "condition not met: " + cd.CNQL}, nil
		}
	}
	var actions []action
	if err := json.Unmarshal(a.actions, &actions); err != nil {
		return outcome{}, command.ErrInvalid("invalid input: " + err.Error())
	}
	key := ""
	if item != nil {
		err := c.Tx.QueryRow(c.Context, "select key from projects.items where note_id = $1::uuid", *item).Scan(&key)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return outcome{}, err
		}
	}
	if _, err := c.Tx.Exec(c.Context, "savepoint automation_target"); err != nil {
		return outcome{}, err
	}
	var log []any
	for _, act := range actions {
		if err := runAction(c, act, item, key); err != nil {
			if _, e := c.Tx.Exec(c.Context, "rollback to savepoint automation_target"); e != nil {
				return outcome{}, e
			}
			log = append(log, map[string]any{"action": act.Type, "item": key, "ok": false, "error": errMessage(err)})
			return outcome{kind: "failed", log: log}, nil
		}
		log = append(log, map[string]any{"action": act.Type, "item": key, "ok": true})
	}
	if _, err := c.Tx.Exec(c.Context, "release savepoint automation_target"); err != nil {
		return outcome{}, err
	}
	return outcome{kind: "success", log: log}, nil
}

func runPrincipal(c *command.Ctx, createdBy *string) (command.Principal, error) {
	role := command.Member // imported rules have no author
	if createdBy != nil {
		var r string
		err := c.Tx.QueryRow(c.Context, "select role from core.memberships where user_id = $1::uuid", *createdBy).Scan(&r)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			role = command.Guest
		case err != nil:
			return command.Principal{}, err
		default:
			role = command.ParseRole(r)
		}
	}
	return command.Principal{Kind: command.KindSystem, UserID: createdBy, WorkspaceID: c.WorkspaceID(), Role: role}, nil
}

// fire is one firing of an automation over its targets, logged as a single row in automation_runs.
func fire(c *command.Ctx, a autoRow, eventID *int64, targets []*string) (string, error) {
	// Serialise firings per automation so the rate check below cannot be raced.
	if _, err := c.Tx.Exec(c.Context, "select pg_advisory_xact_lock(hashtextextended($1, 0))", *a.id); err != nil {
		return "", err
	}
	var recent int64
	if err := c.Tx.QueryRow(c.Context,
		`select count(*) from projects.automation_runs
		 where automation_id = $1::uuid and status <> 'loop_blocked' and created_at > now() - interval '1 minute'`, *a.id).Scan(&recent); err != nil {
		return "", err
	}
	var status string
	log := []any{}
	if recent >= maxRunsPerMinute {
		status = "loop_blocked"
		log = append(log, map[string]any{"error": fmt.Sprintf("ran %d times in the last minute (limit %d); possible loop", recent, maxRunsPerMinute)})
	} else {
		saved := c.Principal
		p, err := runPrincipal(c, a.createdBy)
		if err != nil {
			return "", err
		}
		c.Principal = p
		failed, ran := false, false
		for _, t := range targets {
			o, err := execTarget(c, a, t)
			switch {
			case err != nil:
				failed = true
				log = append(log, map[string]any{"error": errMessage(err)})
			case o.kind == "skipped":
				log = append(log, map[string]any{"skipped": o.why})
			case o.kind == "success":
				ran = true
				log = append(log, o.log...)
			default:
				failed = true
				log = append(log, o.log...)
			}
		}
		c.Principal = saved
		status = "skipped"
		if failed {
			status = "failed"
		} else if ran {
			status = "success"
		}
	}
	lb, err := command.Marshal(log)
	if err != nil {
		return "", err
	}
	if _, err := c.Tx.Exec(c.Context,
		"insert into projects.automation_runs (workspace_id, automation_id, event_id, status, log) values ($1::uuid,$2::uuid,$3,$4,$5)",
		c.WorkspaceID(), *a.id, eventID, status, json.RawMessage(lb)); err != nil {
		return "", err
	}
	if status != "skipped" && status != "loop_blocked" {
		meta, _ := command.Marshal(map[string]any{"automation_id": *a.id})
		if _, err := c.Tx.Exec(c.Context,
			"insert into core.usage (workspace_id, kind, quantity, meta) values ($1::uuid,$2,$3,$4)",
			c.WorkspaceID(), "automation.run", int64(1), json.RawMessage(meta)); err != nil {
			return "", err
		}
	}
	return status, nil
}

func scanAuto(r pgx.Row) (autoRow, error) {
	var a autoRow
	return a, r.Scan(&a.id, &a.spaceID, &a.trigger, &a.conditions, &a.actions, &a.createdBy)
}

// automationOnEvent runs every enabled, non-draft event automation whose trigger matches. Failures are logged in
// automation_runs, never returned, so one broken rule cannot make the whole event dispatch retry.
func automationOnEvent(c *command.Ctx, e jobs.Event) error {
	rows, err := c.Tx.Query(c.Context,
		`select `+automationColumns+` from projects.automations
		 where enabled and not draft and trigger->>'kind' = 'event' and trigger->>'event' = $1 order by created_at`, e.Kind)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (autoRow, error) { return scanAuto(r) })
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	var item, space *string
	if e.NoteID != nil {
		var s string
		switch err := c.Tx.QueryRow(c.Context, "select note_id::text from projects.items where note_id = $1::uuid", *e.NoteID).Scan(&s); {
		case err == nil:
			item = &s
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		var sp string
		switch err := c.Tx.QueryRow(c.Context, "select space_id::text from core.notes where id = $1::uuid", *e.NoteID).Scan(&sp); {
		case err == nil:
			space = &sp
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
	} else {
		var p struct {
			SpaceID string `json:"space_id"`
		}
		if json.Unmarshal(e.Payload, &p) == nil && isUUID(p.SpaceID) {
			space = &p.SpaceID
		}
	}
	for _, a := range list {
		if a.spaceID != nil && (space == nil || !strings.EqualFold(*a.spaceID, *space)) {
			continue
		}
		t, ok := parseTrigger(a.trigger)
		if !ok || t.Kind != "event" {
			continue
		}
		if t.CNQL != nil {
			match := false
			if item != nil {
				match, _ = projects.ItemMatches(c, *item, *t.CNQL)
			}
			if !match {
				continue
			}
		}
		if _, err := fire(c, a, &e.ID, []*string{item}); err != nil {
			slog.Warn("automation firing failed", "automation", *a.id, "error", err)
		}
	}
	return nil
}

// AutomationsSweepWorkspace runs the due schedule automations of one workspace and returns how many fired.
func AutomationsSweepWorkspace(ctx context.Context, w *Worker, ws string) (int, error) {
	c, err := w.systemCtx(ctx, ws)
	if err != nil {
		return 0, err
	}
	rows, err := c.Tx.Query(ctx, "select id::text from projects.automations where enabled and not draft and trigger->>'kind' = 'schedule'")
	if err != nil {
		_ = c.Tx.Rollback(ctx)
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	_ = c.Tx.Commit(ctx)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, id := range ids {
		ok, err := func() (bool, error) {
			c, err := w.systemCtx(ctx, ws)
			if err != nil {
				return false, err
			}
			defer func() { _ = c.Tx.Rollback(context.WithoutCancel(ctx)) }()
			ok, err := SweepAutomation(c, id)
			if err != nil || !ok {
				return false, err
			}
			return true, c.Tx.Commit(ctx)
		}()
		if err != nil {
			return fired, err
		}
		if ok {
			fired++
		}
	}
	return fired, nil
}

// SweepAutomation fires one schedule automation inside c's transaction when it is due. It reports whether it
// fired; the caller commits.
func SweepAutomation(c *command.Ctx, id string) (bool, error) {
	ctx := c.Context
	// Another worker (Rust included: same key) may be sweeping the same rule; let it.
	var locked bool
	if err := c.Tx.QueryRow(ctx, "select pg_try_advisory_xact_lock(hashtextextended($1, 0))", "sweep:"+id).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	a, err := scanAuto(c.Tx.QueryRow(ctx, "select "+automationColumns+" from projects.automations where id = $1::uuid", id))
	if err != nil {
		return false, err
	}
	t, ok := parseTrigger(a.trigger)
	if !ok || t.Kind != "schedule" {
		return false, nil
	}
	var last *time.Time
	if err := c.Tx.QueryRow(ctx,
		"select max(created_at) from projects.automation_runs where automation_id = $1::uuid and status <> 'loop_blocked'", id).Scan(&last); err != nil {
		return false, err
	}
	if last != nil && time.Since(*last) < time.Duration(*t.EveryMinutes)*time.Minute {
		return false, nil
	}
	targets := []*string{nil}
	if t.CNQL != nil {
		ids, err := projects.MatchingItemIDs(c, a.spaceID, *t.CNQL, scheduleItemCap)
		if err != nil {
			slog.Warn("schedule query failed", "automation", id, "error", err)
			return false, nil
		}
		targets = targets[:0]
		for i := range ids {
			targets = append(targets, &ids[i])
		}
	}
	if len(targets) == 0 {
		return false, nil
	}
	if _, err := fire(c, a, nil, targets); err != nil {
		return false, err
	}
	return true, nil
}

func AutomationsSweep(ctx context.Context, w *Worker) error {
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return err
	}
	for _, ws := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := AutomationsSweepWorkspace(ctx, w, ws); err != nil {
			slog.Warn("automation sweep failed", "workspace", ws, "error", err)
		}
	}
	return nil
}
