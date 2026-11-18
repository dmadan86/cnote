package agents

import (
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// Hooks other files of this package fill in (A3). Kept as functions so loop.go does not depend on their files.
var (
	// instructionsFor returns the built-in instructions of a template (used when agents.instructions is null).
	instructionsFor = func(template string) string { return "" }
	// deliver posts a finished or held run's output (chat reply, channel post). It runs as the agent in the
	// finishing transaction inside a savepoint: an error is logged and does not fail the run.
	deliver = func(c *command.Ctx, r DoneRun) error { return nil }
)

// RegisterInstructions sets the source of built-in template instructions (call from an init in templates.go).
func RegisterInstructions(f func(template string) string) { instructionsFor = f }

// RegisterDeliver sets the delivery of a run's output (call from an init in triggers.go).
func RegisterDeliver(f func(c *command.Ctx, r DoneRun) error) { deliver = f }

// DoneRun is what delivery needs about a run that just ended (status completed or held).
type DoneRun struct {
	ID, AgentID, OnBehalfOf, Trigger, Status, Output string
	ConversationID, NoteID, DeliverChannelID         *string
	Held                                             int
}

func agentJob(trigger string) string {
	if trigger == "ask" || trigger == "chat" {
		return "agent.ask"
	}
	return "agent.run"
}

// createRun is CreateRun's body (section 6 guards, 16.5 ownership). c.Tx is the caller's tenant transaction.
func createRun(c *command.Ctx, n NewRun) (string, bool, error) {
	ws := c.WorkspaceID()
	var enabled, runtime bool
	var daily int
	err := c.Tx.QueryRow(c.Context,
		"select enabled, template is not null or instructions is not null, daily_runs from core.agents where id = $1::uuid and workspace_id = $2::uuid",
		n.AgentID, ws).Scan(&enabled, &runtime, &daily)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, command.ErrNotFound("agent")
	}
	if err != nil {
		return "", false, command.DB(err)
	}
	if !runtime {
		return "", false, command.ErrInvalid("this agent is an outside client and does not run in C Note")
	}
	if !enabled {
		return "", false, command.ErrForbidden("this AI employee is paused")
	}
	if n.Trigger == "event" || n.Trigger == "schedule" {
		if n.DedupeKey == nil {
			return "", false, command.ErrInvalid("event and schedule runs need a dedupe key")
		}
	}
	if n.DedupeKey != nil {
		var id string
		err := c.Tx.QueryRow(c.Context, "select id::text from core.agent_runs where agent_id = $1::uuid and dedupe_key = $2", n.AgentID, *n.DedupeKey).Scan(&id)
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", false, command.DB(err)
		}
	}
	// 16.5: a conversation belongs to the person who started it; a note must be one the caller can open.
	if n.ConversationID != nil {
		var other bool
		if err := c.Tx.QueryRow(c.Context,
			"select exists (select 1 from core.agent_runs where conversation_id = $1::uuid and on_behalf_of <> $2::uuid)",
			*n.ConversationID, n.OnBehalfOf).Scan(&other); err != nil {
			return "", false, command.DB(err)
		}
		if other {
			return "", false, command.ErrForbidden("that conversation belongs to someone else")
		}
	}
	if n.NoteID != nil && n.Trigger != "event" && n.Trigger != "schedule" {
		if _, err := c.Call("note.get", map[string]string{"id": *n.NoteID}); err != nil {
			return "", false, err
		}
	}
	if n.Trigger == "event" && n.NoteID != nil { // a run still queued for the same agent and note is reused
		var id string
		err := c.Tx.QueryRow(c.Context, "select id::text from core.agent_runs where agent_id = $1::uuid and note_id = $2::uuid and status = 'queued' limit 1",
			n.AgentID, *n.NoteID).Scan(&id)
		if err == nil {
			return id, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", false, command.DB(err)
		}
	}
	var ran, skipped int
	if err := c.Tx.QueryRow(c.Context,
		`select count(*) filter (where status <> 'skipped'), count(*) filter (where status = 'skipped')
		   from core.agent_runs where agent_id = $1::uuid and created_at > now() - interval '24 hours'`, n.AgentID).Scan(&ran, &skipped); err != nil {
		return "", false, command.DB(err)
	}
	if ran >= daily { // one skipped run says so; the rest are dropped
		if skipped == 0 {
			if _, err := c.Tx.Exec(c.Context,
				`insert into core.agent_runs (workspace_id, agent_id, on_behalf_of, trigger, status, error, finished_at)
				 values ($1::uuid,$2::uuid,$3::uuid,$4,'skipped','daily run limit reached',now())`, ws, n.AgentID, n.OnBehalfOf, n.Trigger); err != nil {
				return "", false, command.DB(err)
			}
		}
		return "", false, nil
	}
	ctxJSON := json.RawMessage(`{}`)
	if n.Context != nil {
		b, err := command.Marshal(n.Context)
		if err != nil {
			return "", false, command.ErrInvalid(err.Error())
		}
		ctxJSON = b
	}
	if n.Trigger == "event" && n.NoteID != nil { // the new run supersedes this agent's older pending drafts on the note
		if _, err := c.Tx.Exec(c.Context,
			`update core.confirmations c set status = 'cancelled', reason = 'superseded', decided_at = now()
			   from core.agent_run_steps s join core.agent_runs r on r.id = s.run_id
			  where s.confirmation_id = c.id and c.status = 'pending' and r.agent_id = $1::uuid and r.note_id = $2::uuid`,
			n.AgentID, *n.NoteID); err != nil {
			return "", false, command.DB(err)
		}
	}
	var id string
	err = c.Tx.QueryRow(c.Context,
		`insert into core.agent_runs (workspace_id, agent_id, on_behalf_of, trigger, dedupe_key, conversation_id, note_id, input, context)
		 values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid,$7::uuid,$8,$9)
		 on conflict (agent_id, dedupe_key) where dedupe_key is not null do nothing returning id::text`,
		ws, n.AgentID, n.OnBehalfOf, n.Trigger, n.DedupeKey, n.ConversationID, n.NoteID, n.Input, ctxJSON).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) { // lost a race on the dedupe key
		err = c.Tx.QueryRow(c.Context, "select id::text from core.agent_runs where agent_id = $1::uuid and dedupe_key = $2", n.AgentID, *n.DedupeKey).Scan(&id)
		return id, false, command.DB(err)
	}
	if err != nil {
		return "", false, command.DB(err)
	}
	payload, _ := command.Marshal(map[string]string{"run_id": id})
	max := 5
	if n.Trigger == "ask" {
		max = 2
	}
	_, err = c.Tx.Exec(c.Context, "insert into core.jobs (workspace_id, kind, payload, max_attempts) values ($1::uuid,$2,$3,$4)",
		ws, agentJob(n.Trigger), json.RawMessage(payload), max)
	return id, true, command.DB(err)
}
