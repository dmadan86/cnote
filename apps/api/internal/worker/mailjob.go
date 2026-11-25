package worker

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

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
	Failed func(c *command.Ctx, ref string) error // the send itself failed (not a crash): undo what Before committed
}

// MailHooks is filled by New (wire.go).
var MailHooks = map[string]MailHook{}
var mailHooksMu sync.RWMutex

func registerMailHook(kind string, hook MailHook) {
	mailHooksMu.Lock()
	defer mailHooksMu.Unlock()
	MailHooks[kind] = hook
}

func getMailHook(kind string) (MailHook, bool) {
	mailHooksMu.RLock()
	defer mailHooksMu.RUnlock()
	hook, ok := MailHooks[kind]
	return hook, ok
}

// mailSendJob delivers one outbox mail (mailer.Enqueue). A failed send fails the job (the queue retries with backoff,
// reusing the payload's Message-ID); the sent Message-ID is written back so a job that already sent never sends again.
//
// Delivery is at-least-once: SMTP and the job's commit are not one transaction, so a crash between them re-sends the
// mail (same Message-ID). A hook can narrow that window: the reminder hook commits a `sending` mark before the send and,
// on a retry that finds it old, treats the mail as possibly sent and does not resend (a missed reminder over a double one).
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
	hook, hooked := getMailHook(kind)
	if hooked && hook.Before != nil {
		if ok, err := hook.Before(c, ref); err != nil || !ok {
			return err
		}
	}
	id, err := mailer.Send(c.Context, m)
	if err != nil {
		slog.Warn("mail.send failed", "error", err)
		if hooked && hook.Failed != nil {
			_ = hook.Failed(c, ref)
		}
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
