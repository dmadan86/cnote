package worker

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/mailer"
)

// MailHook lets a module observe the outbox mails it enqueued with Mail.Notify "kind:ref". Before runs in the job's
// transaction right before the send: false cancels the send (the job completes). After runs in the same transaction
// once the send succeeded, so the module's record and the job's done mark commit together.
type MailHook struct {
	Before func(c *command.Ctx, ref string) (send bool, err error)
	After  func(c *command.Ctx, ref, messageID string) error
}

// MailHooks is filled by New (wire.go).
var MailHooks = map[string]MailHook{}

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
	kind, ref, _ := strings.Cut(m.Notify, ":")
	hook, hooked := MailHooks[kind]
	if hooked && hook.Before != nil {
		if ok, err := hook.Before(c, ref); err != nil || !ok {
			return err
		}
	}
	id, err := mailer.Send(c.Context, m)
	if err != nil {
		slog.Warn("mail.send failed", "error", err)
		return err
	}
	if jid, ok := jobs.JobID(c.Context); ok {
		_, err = c.Tx.Exec(c.Context, `update core.jobs set payload = payload || jsonb_build_object('sent_message_id', $2::text) where id = $1`, jid, id)
	}
	if err != nil {
		return err
	}
	if hooked && hook.After != nil {
		return hook.After(c, ref, id)
	}
	return nil
}
