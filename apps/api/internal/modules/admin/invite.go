package admin

// Email invites (docs/design/onboarding.md 6.6). An invitation never creates a user or a membership: those are made when the
// link is accepted (server/invite.go). Only the SHA-256 of the 32-byte token is stored for lookup. While a mail is waiting to
// go out the token also sits sealed (secretbox, bound to the workspace) in token_enc, so the outbox job can hold references
// only and still rebuild the link. The token is never in an event, an error or a log line; it is returned once, as a path, only
// when the server cannot send mail.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/secretbox"
)

// InviteMailJob is the core.jobs kind of one invitation mail; its payload is {invitation_id, message_id} and nothing else.
const InviteMailJob = "invite.mail"

// InviteDays is how long a link works.
const InviteDays = 7

const maxInvitesPerCall = 20

func inviteLimit(env string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(env)); err == nil && n > 0 {
		return n
	}
	return def
}

// NewInviteToken returns the URL token (43 characters of base64url) and the hex SHA-256 of its 32 random bytes.
func NewInviteToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	h := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(b), hex.EncodeToString(h[:]), nil
}

// InviteHash is the lookup hash of a token from a link; false for anything that is not exactly 32 canonical base64url bytes.
func InviteHash(token string) (string, bool) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(b) != 32 {
		return "", false
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), true
}

func inviteCommands() []command.Command {
	mk := func(name string, g command.Group, h command.Handler, secret bool) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = command.Admin
		c.ReturnsSecret = secret
		return c
	}
	inv := mk("member.invite", command.GroupAdmin, memberInvite, true)
	resend := mk("member.invite_resend", command.GroupAdmin, memberInviteResend, true)
	ex := command.Exists("setup.team_exists", `select 1 from core.invitations i
		join core.memberships m on m.workspace_id = i.workspace_id and m.user_id = i.accepted_by
		where i.workspace_id = $1::uuid and i.accepted_at is not null and m.deactivated_at is null and m.role <> 'owner'`)
	return []command.Command{inv, resend,
		mk("member.invite_revoke", command.GroupAdmin, memberInviteRevoke, false),
		mk("member.invite_list", command.GroupRead, memberInviteList, false), ex}
}

type inviteIn struct {
	Emails []string `json:"emails"`
	Role   string   `json:"role"`
	ID     string   `json:"id"`
}

func parseInvite(b json.RawMessage) (inviteIn, error) {
	var in inviteIn
	if json.Unmarshal(b, &in) != nil {
		return in, command.ErrInvalid("invalid input")
	}
	return in, nil
}

// issued is one link the caller may copy (only when the server cannot send mail).
type issued struct {
	Email string `json:"email"`
	Path  string `json:"path"`
}

func inviter(c *command.Ctx) (string, error) {
	if c.Principal.UserID == nil || c.Principal.IsMachine() {
		return "", command.ErrForbidden("only a person can send invitations")
	}
	return *c.Principal.UserID, nil
}

// rate counts one issue against the inviter (per hour) and the workspace (per day) in the caller's transaction.
func rate(c *command.Ctx, uid string) error {
	for _, l := range []struct {
		key   string
		day   bool
		limit int
	}{{"invite:u:" + uid, false, inviteLimit("INVITE_PER_INVITER_HOUR", 20)}, {"invite:w:" + c.WorkspaceID(), true, inviteLimit("INVITE_PER_WORKSPACE_DAY", 100)}} {
		n, err := db.Bump(c.Context, c.Tx, l.key, l.day)
		if err != nil {
			return command.DB(err)
		}
		if n > l.limit {
			e := command.NewError(command.RateLimited, "too many invitations sent, try again later")
			w := time.Hour
			if l.day {
				w = 24 * time.Hour
			}
			e.Details = map[string]int{"retry_after": int(time.Until(db.BucketStart(time.Now(), l.day).Add(w)).Seconds()) + 1}
			return e
		}
	}
	return nil
}

// arm gives an invitation a fresh token and expiry and either queues the mail or, when the server cannot send, returns the link.
func arm(c *command.Ctx, id, email string) (*issued, error) {
	tok, hash, err := NewInviteToken()
	if err != nil {
		return nil, err
	}
	var enc []byte
	mail := false
	if mailer.Configured() {
		if key, kerr := secretbox.MasterKey(); kerr == nil {
			if enc, err = secretbox.Encrypt(key, c.WorkspaceID(), tok); err != nil {
				return nil, err
			}
			mail = true
		}
	}
	if _, err := c.Tx.Exec(c.Context, `update core.invitations set token_hash = $2, token_enc = $3, expires_at = now() + make_interval(days => $4), updated_at = now() where id = $1::uuid`,
		id, hash, enc, InviteDays); err != nil {
		return nil, command.DB(err)
	}
	if !mail {
		return &issued{Email: email, Path: "/invite/" + tok}, nil
	}
	var mid string
	if err := c.Tx.QueryRow(c.Context, "select gen_random_uuid()::text").Scan(&mid); err != nil {
		return nil, command.DB(err)
	}
	p, _ := json.Marshal(map[string]string{"invitation_id": id, "message_id": "<" + mid + "@cnote>"})
	_, err = c.Tx.Exec(c.Context, "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,$3::jsonb, now())", c.WorkspaceID(), InviteMailJob, p)
	return nil, command.DB(err)
}

func roleGate(c *command.Ctx, role string) error {
	switch role {
	case "owner", "admin", "member", "guest":
	default:
		return command.ErrInvalid("role must be owner, admin, member or guest")
	}
	if command.ParseRole(role) > c.Principal.Role { // only owners invite owners; admins at most admin
		return command.ErrForbidden("cannot grant a role above your own")
	}
	return nil
}

func result(links []issued) map[string]any {
	if links == nil {
		links = []issued{}
	}
	// The same words whatever happened to each address: an existing member, a deactivated one or a new person look alike.
	return map[string]any{"message": "If these people can join, invitations have been sent.", "mailed": mailer.Configured(), "links": links}
}

func memberInvite(c *command.Ctx, b json.RawMessage) (any, error) {
	in, err := parseInvite(b)
	if err != nil {
		return nil, err
	}
	uid, err := inviter(c)
	if err != nil {
		return nil, err
	}
	if len(in.Emails) == 0 || len(in.Emails) > maxInvitesPerCall {
		return nil, command.ErrInvalid("give between 1 and 20 email addresses")
	}
	if err := roleGate(c, in.Role); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var links []issued
	for _, raw := range in.Emails {
		email := lowerRust(strings.TrimSpace(raw))
		if at := strings.Index(email, "@"); at < 1 || at == len(email)-1 || len(email) > 254 || strings.ContainsAny(email, " \t\r\n<>,;\"") {
			return nil, command.ErrInvalid("not a valid email address")
		}
		if seen[email] {
			continue
		}
		seen[email] = true
		if _, err := c.Tx.Exec(c.Context, "select pg_advisory_xact_lock(hashtextextended($1, 0))", c.WorkspaceID()+"|"+email); err != nil {
			return nil, command.DB(err)
		}
		var member bool
		if err := c.Tx.QueryRow(c.Context, `select exists(select 1 from core.users u join core.memberships m on m.user_id = u.id and m.workspace_id = $1::uuid where u.email = $2)`,
			c.WorkspaceID(), email).Scan(&member); err != nil {
			return nil, command.DB(err)
		}
		if member { // already here or deactivated: no invitation, and no different answer
			continue
		}
		if err := rate(c, uid); err != nil {
			return nil, err
		}
		var id, oldRole string
		switch err := c.Tx.QueryRow(c.Context, `select id::text, role from core.invitations where workspace_id = $1::uuid and email = $2 and accepted_at is null and revoked_at is null for update`,
			c.WorkspaceID(), email).Scan(&id, &oldRole); {
		case errors.Is(err, pgx.ErrNoRows):
			err = c.Tx.QueryRow(c.Context, `insert into core.invitations (workspace_id, email, role, invited_by, token_hash, expires_at)
				values ($1::uuid, $2, $3, $4::uuid, encode(sha256(gen_random_bytes(32)), 'hex'), now()) returning id::text`, c.WorkspaceID(), email, in.Role, uid).Scan(&id)
			if err != nil {
				return nil, command.DB(err)
			}
		case err != nil:
			return nil, command.DB(err)
		default: // already invited: the same row gets a new token (and the new role, unless it is above the caller's)
			if command.ParseRole(oldRole) > c.Principal.Role {
				return nil, command.ErrForbidden("cannot change an invitation above your own role")
			}
			if _, err := c.Tx.Exec(c.Context, "update core.invitations set role = $2, invited_by = $3::uuid where id = $1::uuid", id, in.Role, uid); err != nil {
				return nil, command.DB(err)
			}
		}
		l, err := arm(c, id, email)
		if err != nil {
			return nil, err
		}
		if l != nil {
			links = append(links, *l)
		}
		if _, err := c.Emit("member.invited", nil, map[string]any{"invitation_id": id, "email": email, "role": in.Role}); err != nil {
			return nil, err
		}
	}
	return result(links), nil
}

// open locks one open invitation of this workspace and checks the caller may touch it.
func open(c *command.Ctx, id string) (email, role string, err error) {
	if !uuidRE.MatchString(id) {
		return "", "", command.ErrInvalid("invalid id")
	}
	err = c.Tx.QueryRow(c.Context, `select email, role from core.invitations where id = $1::uuid and workspace_id = $2::uuid and accepted_at is null and revoked_at is null for update`,
		id, c.WorkspaceID()).Scan(&email, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", command.ErrNotFound("invitation")
	}
	if err != nil {
		return "", "", command.DB(err)
	}
	if command.ParseRole(role) > c.Principal.Role {
		return "", "", command.ErrForbidden("cannot change an invitation above your own role")
	}
	return email, role, nil
}

func memberInviteResend(c *command.Ctx, b json.RawMessage) (any, error) {
	in, err := parseInvite(b)
	if err != nil {
		return nil, err
	}
	uid, err := inviter(c)
	if err != nil {
		return nil, err
	}
	email, role, err := open(c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := rate(c, uid); err != nil {
		return nil, err
	}
	l, err := arm(c, in.ID, email) // a new token: the old link stops working
	if err != nil {
		return nil, err
	}
	if _, err := c.Emit("member.invite_resent", nil, map[string]any{"invitation_id": in.ID, "email": email, "role": role}); err != nil {
		return nil, err
	}
	var links []issued
	if l != nil {
		links = append(links, *l)
	}
	return result(links), nil
}

func memberInviteRevoke(c *command.Ctx, b json.RawMessage) (any, error) {
	in, err := parseInvite(b)
	if err != nil {
		return nil, err
	}
	email, role, err := open(c, in.ID)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update core.invitations set revoked_at = now(), token_enc = null, updated_at = now() where id = $1::uuid", in.ID); err != nil {
		return nil, command.DB(err)
	}
	_, err = c.Emit("member.invite_revoked", nil, map[string]any{"invitation_id": in.ID, "email": email, "role": role})
	return map[string]any{"revoked": 1}, err
}

func memberInviteList(c *command.Ctx, b json.RawMessage) (any, error) {
	return query(c, `select i.id::text, i.email, i.role, coalesce(u.name, '') as invited_by_name, i.created_at, i.expires_at, i.expires_at <= now() as expired
		from core.invitations i left join core.users u on u.id = i.invited_by
		where i.workspace_id = $1::uuid and i.accepted_at is null and i.revoked_at is null order by i.created_at desc, i.id`, c.WorkspaceID())
}
