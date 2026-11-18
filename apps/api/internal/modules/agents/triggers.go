package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
)

const (
	defaultRetentionDays = 30
	maxContextPayload    = 8 << 10
)

// runtimeAgent is the filter for agents this package runs: enabled and with a template or instructions.
const runtimeAgent = `enabled and (template is not null or instructions is not null)`

// OnEvent starts runs for agents whose events prefixes match e (section 6). Events caused by an agent, and every
// agent.* kind, never trigger a run, so an agent cannot start itself or another. The run is for the agent's owner.
func OnEvent(c *command.Ctx, e jobs.Event) error {
	if e.ActorType == "agent" || strings.HasPrefix(e.Kind, "agent.") {
		return nil
	}
	switch e.Kind {
	case "chat.agent_mentioned":
		return onMention(c, e)
	case "ticket.replied":
		if e.ActorType == "user" && e.NoteID != nil {
			return supersedeDrafts(c, *e.NoteID) // a person answered: the agent's pending drafts are stale
		}
	}
	rows, err := c.Tx.Query(c.Context, `select id::text, owner_user_id::text from core.agents
		where `+runtimeAgent+` and exists (select 1 from unnest(events) p where p <> '' and p <> '*' and starts_with($1, p))`, e.Kind)
	if err != nil {
		return command.DB(err)
	}
	type target struct{ id, owner string }
	ts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (t target, err error) { err = r.Scan(&t.id, &t.owner); return })
	if err != nil {
		return command.DB(err)
	}
	ctx := map[string]any{"event_id": e.ID, "event_kind": e.Kind}
	if e.NoteID != nil { // the model has no other way to learn which ticket or deal the event is about
		ctx["note_id"] = *e.NoteID
	}
	if len(e.Payload) > 0 && len(e.Payload) <= maxContextPayload {
		ctx["payload"] = e.Payload
	}
	for _, t := range ts {
		key := fmt.Sprintf("event:%d", e.ID)
		if _, _, err := CreateRun(c, NewRun{AgentID: t.id, OnBehalfOf: t.owner, Trigger: "event", Input: "Event: " + e.Kind,
			DedupeKey: &key, NoteID: e.NoteID, Context: ctx}); err != nil {
			return err
		}
	}
	return nil
}

// onMention handles chat.agent_mentioned (emitted by chat.send for a person's message): the run is for the author.
func onMention(c *command.Ctx, e jobs.Event) error {
	var p struct {
		ChannelID string  `json:"channel_id"`
		MessageID string  `json:"message_id"`
		AgentID   string  `json:"agent_id"`
		ParentID  *string `json:"parent_id"`
		Body      string  `json:"body"`
	}
	if e.ActorType != "user" || e.ActorID == nil || json.Unmarshal(e.Payload, &p) != nil || p.AgentID == "" || p.MessageID == "" {
		return nil
	}
	var ok bool
	if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from core.agents where id = $1::uuid and `+runtimeAgent+`)`, p.AgentID).Scan(&ok); err != nil {
		return command.DB(err)
	}
	if !ok {
		return nil
	}
	key := "chat:" + p.MessageID
	_, _, err := CreateRun(c, NewRun{AgentID: p.AgentID, OnBehalfOf: *e.ActorID, Trigger: "chat", Input: p.Body, DedupeKey: &key,
		Context: map[string]any{"channel_id": p.ChannelID, "message_id": p.MessageID, "parent_id": p.ParentID}})
	return err
}

// supersedeDrafts rejects the agent drafts still waiting on a note once a person has replied there themselves.
func supersedeDrafts(c *command.Ctx, noteID string) error {
	_, err := c.Tx.Exec(c.Context, `update core.confirmations cf set status = 'rejected', decided_at = now()
		where cf.status = 'pending' and exists (select 1 from core.agent_run_steps s join core.agent_runs r on r.id = s.run_id
		  where s.confirmation_id = cf.id and r.note_id = $1::uuid)`, noteID)
	return command.DB(err)
}

// workspaces lists every workspace id.
func workspaces(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// each runs fn in a tenant transaction per workspace and commits it.
func each(ctx context.Context, pool *pgxpool.Pool, fn func(context.Context, pgx.Tx, string) error) error {
	wss, err := workspaces(ctx, pool)
	if err != nil {
		return err
	}
	for _, ws := range wss {
		tx, err := db.BeginTenant(ctx, pool, ws)
		if err != nil {
			return err
		}
		if err := fn(ctx, tx, ws); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Sweep starts due scheduled runs, and on the hour purges old transcripts.
func Sweep(ctx context.Context, pool *pgxpool.Pool) error {
	if err := each(ctx, pool, sweepTx); err != nil {
		return err
	}
	if time.Now().Minute() == 0 { // shortcut: tied to the one-minute tick; a missed tick waits for the next hour
		return Purge(ctx, pool)
	}
	return nil
}

// Purge applies the retention rule to every workspace.
func Purge(ctx context.Context, pool *pgxpool.Pool) error {
	return each(ctx, pool, func(ctx context.Context, tx pgx.Tx, _ string) error { return purgeTx(ctx, tx) })
}

// sweepTx is hooks.sweepTx on core.agents: advance every due schedule past now (missed runs are skipped) and queue one run.
func sweepTx(ctx context.Context, tx pgx.Tx, ws string) error {
	rows, err := tx.Query(ctx, `with due as (select id, next_run_at from core.agents
			where `+runtimeAgent+` and every_minutes is not null and next_run_at <= now() for update skip locked)
		update core.agents a
		   set next_run_at = due.next_run_at + make_interval(mins => a.every_minutes
		         * (floor(extract(epoch from now() - due.next_run_at) / 60 / a.every_minutes)::int + 1))
		from due where a.id = due.id
		returning a.id::text, a.owner_user_id::text, due.next_run_at`)
	if err != nil {
		return err
	}
	type dueRow struct {
		ID, Owner string
		At        time.Time
	}
	due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (d dueRow, err error) { err = r.Scan(&d.ID, &d.Owner, &d.At); return })
	if err != nil {
		return err
	}
	for _, d := range due {
		owner := d.Owner
		c := &command.Ctx{Context: ctx, Tx: tx, Principal: command.Principal{Kind: command.KindSystem, UserID: &owner, WorkspaceID: ws, Role: command.Admin}}
		key := "schedule:" + d.At.UTC().Format(time.RFC3339)
		if _, _, err := CreateRun(c, NewRun{AgentID: d.ID, OnBehalfOf: d.Owner, Trigger: "schedule",
			Input: "Scheduled run for " + d.At.In(ist).Format("2 Jan 2006, 15:04") + " IST", DedupeKey: &key}); err != nil {
			return err
		}
	}
	return nil
}

// purgeTx deletes the steps of finished runs older than the workspace's agent_run_retention_days (default 30) and
// blanks their input and output. Held runs stay: a person may still decide their confirmation.
func purgeTx(ctx context.Context, tx pgx.Tx) error {
	const old = `agent_runs.status in ('completed','failed','cancelled','skipped') and agent_runs.created_at < now() - make_interval(days => (
		select coalesce(nullif((settings->>'agent_run_retention_days'), '')::int, 30) from core.workspaces w where w.id = agent_runs.workspace_id))`
	if _, err := tx.Exec(ctx, `delete from core.agent_run_steps s using core.agent_runs where s.run_id = agent_runs.id and `+old); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `update core.agent_runs set input = '', output = null where (input <> '' or output is not null) and `+old)
	return err
}

var ist = time.FixedZone("IST", 5*3600+1800)

// nextAt is the next HH:MM (IST) after now.
func nextAt(at string, now time.Time) (time.Time, error) {
	t, err := time.Parse("15:04", at)
	if err != nil {
		return time.Time{}, command.ErrInvalid(`at must look like "08:30"`)
	}
	n := now.In(ist)
	d := time.Date(n.Year(), n.Month(), n.Day(), t.Hour(), t.Minute(), 0, 0, ist)
	if !d.After(n) {
		d = d.AddDate(0, 0, 1)
	}
	return d, nil
}

func init() { RegisterDeliver(deliverRun) }

// deliverRun posts a finished run's output through chat.send as the agent (section 7.6). A chat run replies in the
// thread of the message that mentioned it; an event or schedule run posts to the agent's channel, opening with an
// @mention of its owner so the existing chat notification fires. Ask runs are polled, never posted. It is internal chat,
// not an outbound send, so nothing is held.
func deliverRun(c *command.Ctx, r DoneRun) error {
	text := strings.TrimSpace(r.Output)
	if text == "" && r.Held == 0 {
		return nil
	}
	if r.Held > 0 {
		text = strings.TrimSpace(text + fmt.Sprintf("\n\n%d action(s) are waiting for approval.", r.Held))
	}
	in := map[string]any{}
	switch r.Trigger {
	case "chat":
		var ctx struct {
			ChannelID string  `json:"channel_id"`
			MessageID string  `json:"message_id"`
			ParentID  *string `json:"parent_id"`
		}
		var raw []byte
		if err := c.Tx.QueryRow(c.Context, `select context from core.agent_runs where id = $1::uuid`, r.ID).Scan(&raw); err != nil {
			return command.DB(err)
		}
		if json.Unmarshal(raw, &ctx) != nil || ctx.ChannelID == "" {
			return nil
		}
		in["channel_id"], in["parent_id"] = ctx.ChannelID, ctx.MessageID
		if ctx.ParentID != nil { // one level of threads: reply to the top message
			in["parent_id"] = *ctx.ParentID
		}
	case "event", "schedule", "manual":
		ch, err := deliveryChannel(c, r)
		if err != nil || ch == nil {
			return err
		}
		r.DeliverChannelID = ch
		var name string
		if err := c.Tx.QueryRow(c.Context, `select name from core.users where id = $1::uuid`, r.OnBehalfOf).Scan(&name); err != nil {
			return command.DB(err)
		}
		in["channel_id"] = *r.DeliverChannelID
		text = "[@" + strings.NewReplacer("[", "", "]", "").Replace(name) + "](/w/" + c.WorkspaceID() + "/people/" + r.OnBehalfOf + ") " + text
	default:
		return nil
	}
	if rs := []rune(text); len(rs) > 4000 {
		text = string(rs[:4000])
	}
	in["body"] = text
	// Posting is the runtime's own act, not a model tool: the read-only briefing agent still needs the notes scope for it.
	dc := *c
	dc.Principal.Scopes = append(slices.Clone(c.Principal.Scopes), string(command.GroupWriteNotes))
	_, err := dc.Call("chat.send", in)
	return err
}

const collectionsChannel = "collections"

// deliveryChannel is where a run's output goes. Collections briefs carry money: they go to the channel the owner
// chose only if every person who can read it has finance access, else to the owner's own private #collections
// channel; if neither is safe nothing is posted (the run's stored output is the record). Other agents post to their channel.
func deliveryChannel(c *command.Ctx, r DoneRun) (*string, error) {
	var tmpl *string
	if err := c.Tx.QueryRow(c.Context, `select template from core.agents where id = $1::uuid`, r.AgentID).Scan(&tmpl); err != nil {
		return nil, command.DB(err)
	}
	if tmpl == nil || *tmpl != TemplateCollections {
		if r.Trigger == "manual" {
			return nil, nil
		}
		return r.DeliverChannelID, nil
	}
	const safe = `select exists (select 1 from chat.channels ch where ch.id = $1::uuid and ch.kind <> 'dm'
		and not exists (select 1 from core.memberships m where m.workspace_id = ch.workspace_id and m.role in ('member','guest') and not m.finance_access
		  and (ch.kind = 'public' or exists (select 1 from chat.members cm where cm.channel_id = ch.id and cm.user_id = m.user_id))))`
	var ok bool
	if r.DeliverChannelID != nil {
		if err := c.Tx.QueryRow(c.Context, safe, *r.DeliverChannelID).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if ok {
			return r.DeliverChannelID, nil
		}
	}
	var priv *string
	err := c.Tx.QueryRow(c.Context, `select ch.id::text from chat.channels ch join chat.agent_members am on am.channel_id = ch.id
		where am.agent_id = $1::uuid and ch.kind = 'private' and ch.name = $2`, r.AgentID, collectionsChannel).Scan(&priv)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if err := c.Tx.QueryRow(c.Context, safe, *priv).Scan(&ok); err != nil || !ok {
		return nil, command.DB(err)
	}
	return priv, nil
}
