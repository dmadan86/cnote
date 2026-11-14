package worker

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/hooks"
	"github.com/cnote/api/internal/jobs"
)

// aiIndexOnNoteEvent is cnote-ai's on_note_event: queue an embedding refresh. The job itself (ai.index_note) calls
// the embedding provider and stays with the Rust worker until the AI port lands.
func aiIndexOnNoteEvent(c *command.Ctx, e jobs.Event) error {
	if e.NoteID == nil {
		return nil
	}
	return enqueue(c, "ai.index_note", map[string]any{"note_id": *e.NoteID}, nil)
}

// New builds the worker with every handler of the Rust modules, in Rust's registration order (core, docs,
// finance, projects, ai, importers).
//
// Left to the Rust worker (they call an AI model provider): job kinds ai.index_note and docs.propose_edit. The Go
// worker never claims them, so while Rust runs they are processed there; with Rust stopped they stay queued.
func New(pool *pgxpool.Pool, reg *command.Registry) *Worker {
	w := newWorker(pool, reg)
	w.OnEvent("mention.created", mentionEmail)
	w.OnEvent("note.", syncHandler)
	w.OnEvent("", freshnessOnEvent)
	w.OnEvent("", automationOnEvent)
	w.OnEvent("note.created", aiIndexOnNoteEvent)
	w.OnEvent("note.updated", aiIndexOnNoteEvent)
	w.OnEvent("", hooks.OnEvent)
	w.OnJob("hook.deliver", hooks.DeliverJob)
	day := 24 * time.Hour
	w.AddSweep(Sweep{"docs.freshness_sweep", day, FreshnessSweep})
	w.AddSweep(Sweep{"finance.irn_window", day, func(ctx context.Context, w *Worker) error {
		_, err := IRNWindowSweep(ctx, w, todayIST())
		return err
	}})
	w.AddSweep(Sweep{"projects.automations", time.Minute, AutomationsSweep})
	w.AddSweep(Sweep{"hooks.schedule", time.Minute, func(ctx context.Context, w *Worker) error { return hooks.Sweep(ctx, w.Pool) }})
	return w
}

func todayIST() time.Time {
	t := time.Now().UTC().Add(330 * time.Minute)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
