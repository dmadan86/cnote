package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/modules/admin"
	"github.com/cnote/api/internal/secretbox"
)

// Invitation mail (docs/design/onboarding.md 6.6). The outbox entry holds only the invitation id and a Message-ID. This job
// rebuilds the message just before SMTP from the invitation as it is then: a revoked, accepted, expired or resent invitation, or
// an inviter who is no longer an admin, sends nothing. The mail holds the workspace name, the inviter's name and the link.
// The token is read from token_enc (sealed with the master key), is never logged, and token_enc is cleared once the mail is out.
// Delivery is at least once: a retry after a lost commit sends the same link again, which is harmless.

type inviteMailRef struct {
	InvitationID string `json:"invitation_id"`
	MessageID    string `json:"message_id"`
}

func inviteMailJob(c *command.Ctx, payload json.RawMessage) error {
	var ref inviteMailRef
	if json.Unmarshal(payload, &ref) != nil || !isUUID(ref.InvitationID) {
		return command.ErrInvalid("invite mail payload")
	}
	var email, role, inviterName, wsName string
	var enc []byte
	err := c.Tx.QueryRow(c.Context, `select i.email, i.role, i.token_enc, coalesce(u.name, ''), w.name
		from core.invitations i join core.workspaces w on w.id = i.workspace_id
		left join core.memberships m on m.workspace_id = i.workspace_id and m.user_id = i.invited_by and m.deactivated_at is null and m.role in ('admin','owner')
		left join core.users u on u.id = m.user_id
		where i.id = $1::uuid and i.workspace_id = $2::uuid and i.accepted_at is null and i.revoked_at is null and i.expires_at > now() and i.token_enc is not null
		for update of i`, ref.InvitationID, c.WorkspaceID()).Scan(&email, &role, &enc, &inviterName, &wsName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // revoked, accepted, expired or already sent
	}
	if err != nil {
		return err
	}
	clear := func() error {
		_, err := c.Tx.Exec(c.Context, "update core.invitations set token_enc = null where id = $1::uuid", ref.InvitationID)
		return err
	}
	if inviterName == "" || !mailer.Configured() { // the inviter left or lost their role, or mail was switched off: send nothing
		slog.Debug("invitation mail dropped", "invitation", ref.InvitationID)
		return clear()
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		return clear()
	}
	tok, err := secretbox.Decrypt(key, c.WorkspaceID(), enc)
	if err != nil {
		return clear()
	}
	m := buildInviteMail(wsName, inviterName, email, webBase()+"/invite/"+tok, ref.MessageID)
	sctx, cancel := context.WithTimeout(c.Context, smtpTimeout())
	_, err = mailer.Send(sctx, m)
	cancel()
	if err != nil {
		slog.Warn("invite.mail send failed", "invitation", ref.InvitationID, "error", err)
		return err // the queue retries with backoff
	}
	return clear()
}

func buildInviteMail(wsName, inviter, to, link, messageID string) mailer.Mail {
	one := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	ws, who := one(wsName), one(inviter)
	days := admin.InviteDays
	text := fmt.Sprintf("%s invited you to join %s on C Note.\n\nAccept the invitation:\n%s\n\nThe link works for %d days and only for this email address.\n", who, ws, link, days)
	htm := fmt.Sprintf(`<p>%s invited you to join <strong>%s</strong> on C Note.</p>`+"\n"+`<p><a href="%s">Accept the invitation</a></p>`+"\n"+
		`<p style="color:#666;font-size:12px">The link works for %d days and only for this email address.</p>`+"\n",
		html.EscapeString(who), html.EscapeString(ws), html.EscapeString(link), days)
	return mailer.Mail{To: to, Subject: who + " invited you to " + ws + " on C Note", Text: text, HTML: htm, MessageID: messageID}
}
