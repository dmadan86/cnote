package mailer

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// JobKind is the core.jobs kind of an outbox mail. Enqueue it in the transaction that decides to send, so a rolled-back
// decision sends nothing; the worker's mail.send handler delivers it. A mail service can later consume the same rows.
const JobKind = "mail.send"

// Enqueue stores m for delivery in tx. The job's Message-ID is fixed here (unless m has one), so a retry after a
// crash between send and completion repeats a recognisable message instead of a fresh one.
func Enqueue(ctx context.Context, tx pgx.Tx, workspaceID string, m Mail) error {
	if m.MessageID == "" {
		var id string
		if err := tx.QueryRow(ctx, "select gen_random_uuid()::text").Scan(&id); err != nil {
			return err
		}
		m.MessageID = "<" + id + "@cnote>"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "insert into core.jobs (workspace_id, kind, payload) values ($1::uuid,$2,$3::jsonb)", workspaceID, JobKind, b)
	return err
}
