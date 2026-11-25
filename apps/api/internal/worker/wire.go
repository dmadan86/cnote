package worker

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/hooks"
	"github.com/cnote/api/internal/importers"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/modules/agents"
	"github.com/cnote/api/internal/modules/ai"
	"github.com/cnote/api/internal/modules/files"
	"github.com/cnote/api/internal/modules/reminders"
	"github.com/cnote/api/internal/modules/sites"
	"github.com/cnote/api/internal/modules/tables"
)

// aiIndexOnNoteEvent is cnote-ai's on_note_event: queue an embedding refresh (job ai.index_note).
func aiIndexOnNoteEvent(c *command.Ctx, e jobs.Event) error {
	if e.NoteID == nil {
		return nil
	}
	return enqueue(c, "ai.index_note", map[string]any{"note_id": *e.NoteID}, nil)
}

// New builds the worker with every handler of the Rust modules, in Rust's registration order (core, docs,
// finance, projects, ai, importers).
func New(pool *pgxpool.Pool, reg *command.Registry) *Worker {
	w := newWorker(pool, reg)
	w.OnEvent("mention.created", mentionEmail)
	w.OnEvent("note.", syncHandler)
	w.OnEvent("", freshnessOnEvent)
	w.OnEvent("", automationOnEvent)
	w.OnEvent("note.created", aiIndexOnNoteEvent)
	w.OnEvent("note.updated", aiIndexOnNoteEvent)
	w.OnEvent("note.created", pageNotifyOnEvent)
	w.OnEvent("note.updated", pageNotifyOnEvent)
	w.OnEvent("comment.added", pageNotifyOnEvent)
	w.OnEvent("comment.resolved", pageNotifyOnEvent)
	w.OnEvent("comment.reopened", pageNotifyOnEvent)
	w.OnEvent("", hooks.OnEvent)
	w.OnEvent("", agents.OnEvent)
	w.OnEvent("payment.", reminders.OnEvent)
	w.OnEvent("invoice.", reminders.OnEvent)
	w.OnEvent("credit_note.", reminders.OnEvent)
	reminders.Pool = pool
	MailHooks["reminder"] = MailHook{Before: reminders.BeforeSend, After: reminders.AfterSend, Failed: reminders.SendFailed}
	w.OnJob("ai.index_note", ai.IndexJob)
	w.OnJob("docs.propose_edit", proposeEditJob)
	w.OnJob("hook.deliver", hooks.DeliverJob)
	w.OnJob(mailer.JobKind, mailSendJob)
	w.OnJob("import.run", importers.RunJob)
	w.OnJob("data_import.run", importers.RunDataJob)
	w.OnJob("tables.import", tables.ImportJob)
	w.OnLong("agent.ask", 4, agents.RunJob(pool, reg))
	w.OnLong("agent.run", 2, agents.RunJob(pool, reg))
	w.OnLong("site.chat", 2, sites.RunJob(pool, reg))
	w.OnLong(sites.OGJobKind, 1, sites.RunOGJob(pool, reg))
	day := 24 * time.Hour
	w.AddSweep(Sweep{"docs.freshness_sweep", day, FreshnessSweep})
	w.AddSweep(Sweep{"docs.search_plain_backfill", time.Minute, SearchPlainBackfill})
	w.AddSweep(Sweep{"docs.a2_backfill", time.Minute, A2DeletionBackfill})
	w.AddSweep(Sweep{"finance.irn_window", day, func(ctx context.Context, w *Worker) error {
		_, err := IRNWindowSweep(ctx, w, todayIST())
		return err
	}})
	w.AddSweep(Sweep{"jobs.reclaim", time.Minute, ReclaimStale})
	w.AddSweep(Sweep{"projects.automations", time.Minute, AutomationsSweep})
	w.AddSweep(Sweep{"hooks.schedule", time.Minute, func(ctx context.Context, w *Worker) error { return hooks.Sweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"agents.schedule", time.Minute, func(ctx context.Context, w *Worker) error { return agents.Sweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"sites.turns", time.Minute, func(ctx context.Context, w *Worker) error { return sites.Sweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"sites.prune", day, func(ctx context.Context, w *Worker) error { return sites.PruneForm(ctx, w.Pool) }})
	w.AddSweep(Sweep{"sites.prune_versions", day, func(ctx context.Context, w *Worker) error { return sites.PruneVersions(ctx, w.Pool) }})
	w.AddSweep(Sweep{"sites.domains", day, func(ctx context.Context, w *Worker) error { return sites.DomainSweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"sites.stats", day, func(ctx context.Context, w *Worker) error { return sites.StatsSweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"sites.cloudflare", time.Minute, func(ctx context.Context, w *Worker) error { return sites.CloudflareSweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"sites.abuse", day, func(ctx context.Context, w *Worker) error { return sites.AbuseSweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"tables.purge", day, func(ctx context.Context, w *Worker) error { return tables.PurgeSweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"files.record_sweep", time.Hour, func(ctx context.Context, w *Worker) error { return files.RecordSweep(ctx, w.Pool) }})
	w.AddSweep(Sweep{"reminders.send", 5 * time.Minute, func(ctx context.Context, w *Worker) error {
		return reminders.Sweep(ctx, w.Pool, w.Registry, time.Now())
	}})
	return w
}

func todayIST() time.Time {
	t := time.Now().UTC().Add(330 * time.Minute)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
