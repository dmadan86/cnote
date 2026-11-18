package worker

import (
	"encoding/json"
	"log/slog"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/mailer"
)

// mailSendJob delivers one outbox mail (mailer.Enqueue). A failed send fails the job (the queue retries with backoff,
// reusing the payload's Message-ID); the sent Message-ID is written back so a job that already sent never sends again.
func mailSendJob(c *command.Ctx, payload json.RawMessage) error {
	var m mailer.Mail
	var done struct {
		Sent string `json:"sent_message_id"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return command.ErrInvalid("mail payload")
	}
	_ = json.Unmarshal(payload, &done)
	if done.Sent != "" {
		return nil
	}
	id, err := mailer.Send(c.Context, m)
	if err != nil {
		slog.Warn("mail.send failed", "error", err)
		return err
	}
	if jid, ok := jobs.JobID(c.Context); ok {
		_, err = c.Tx.Exec(c.Context, `update core.jobs set payload = payload || jsonb_build_object('sent_message_id', $2::text) where id = $1`, jid, id)
	}
	return err
}
