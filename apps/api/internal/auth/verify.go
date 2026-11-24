package auth

// Email verification (docs/design/site-builder-phase1.md 17.8). A 6-digit code from crypto/rand is mailed to the
// address on the account and stored only as an HMAC (master key) bound to the user and that address. It lives 10
// minutes, allows 5 wrong tries, is consumed atomically and at most 5 are sent per person per hour. Without SMTP
// nothing can be verified: the request fails closed. An OIDC sign-in with email_verified=true counts too (SAML does not).

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/mailer"
	"github.com/cnote/api/internal/secretbox"
)

const (
	codeTTL         = 10 * time.Minute
	codeTries       = 5
	codeSendsPerHr  = 5
	badCodeMessage  = "That code is not right or has expired. Ask for a new one."
	notConfiguredMs = "Email verification is not set up on this server."
)

func codeHash(userID, email, code string) (string, error) {
	key, err := secretbox.MasterKey()
	if err != nil {
		return "", err
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte("email-code\x00" + userID + "\x00" + strings.ToLower(email) + "\x00" + code))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RequestEmailCode mails a fresh code to the person's account address (a verified person gets nothing and no error).
func (a *Auth) RequestEmailCode(ctx context.Context, userID string) error {
	if !mailer.Configured() {
		return command.ErrInvalid(notConfiguredMs)
	}
	var email string
	var verified bool
	if err := a.Pool.QueryRow(ctx, "select email, email_verified_at is not null from core.users where id = $1::uuid", userID).Scan(&email, &verified); err != nil {
		return command.DB(err)
	}
	if verified {
		return nil
	}
	n, err := db.Bump(ctx, a.Pool, "verify:"+userID, false)
	if err != nil {
		return command.DB(err)
	}
	if n > codeSendsPerHr {
		e := command.NewError(command.RateLimited, "too many codes requested, try again later")
		e.Details = map[string]int{"retry_after": int(time.Until(db.BucketStart(time.Now(), false).Add(time.Hour)).Seconds()) + 1}
		return e
	}
	v, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", v.Int64())
	hash, err := codeHash(userID, email, code)
	if err != nil {
		return err
	}
	if _, err := a.Pool.Exec(ctx, `insert into core.email_codes (user_id, email, code_hash, expires_at, attempts) values ($1::uuid, $2, $3, now() + $4 * interval '1 second', 0)
		on conflict (user_id) do update set email = excluded.email, code_hash = excluded.code_hash, expires_at = excluded.expires_at, attempts = 0`,
		userID, email, hash, codeTTL.Seconds()); err != nil {
		return command.DB(err)
	}
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err = mailer.Send(sctx, mailer.Mail{To: email, Subject: "Your C Note verification code",
		Text: "Your C Note verification code is " + code + ".\n\nIt works for 10 minutes. If you did not ask for it, ignore this email.\n"})
	if err != nil {
		deleteFailedCode(ctx, a.Pool, userID, hash)
		return fmt.Errorf("verification email: %w", err)
	}
	return nil
}

// ConfirmEmailCode checks the code and marks the address verified. Each call spends one try; a right code is consumed
// by a DELETE ... RETURNING so two requests cannot both use it.
func (a *Auth) ConfirmEmailCode(ctx context.Context, userID, code string) error {
	bad := command.ErrInvalid(badCodeMessage)
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return bad
	}
	var email, stored string
	err := a.Pool.QueryRow(ctx, `update core.email_codes set attempts = attempts + 1
		where user_id = $1::uuid and expires_at > now() and attempts < $2 returning email, code_hash`, userID, codeTries).Scan(&email, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return bad
	}
	if err != nil {
		return command.DB(err)
	}
	want, err := codeHash(userID, email, code)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(stored)) != 1 {
		return bad
	}
	var used string
	if err := a.Pool.QueryRow(ctx, "delete from core.email_codes where user_id = $1::uuid and code_hash = $2 returning email", userID, stored).Scan(&used); err != nil {
		return bad // someone else consumed it first
	}
	_, err = a.Pool.Exec(ctx, "update core.users set email_verified_at = coalesce(email_verified_at, now()) where id = $1::uuid and lower(email) = lower($2)", userID, used)
	return command.DB(err)
}

// markVerified records an address the identity provider vouched for.
func (a *Auth) markVerified(ctx context.Context, userID, email string) {
	_, _ = a.Pool.Exec(ctx, "update core.users set email_verified_at = coalesce(email_verified_at, now()) where id = $1::uuid and lower(email) = lower($2)", userID, email)
}

func deleteFailedCode(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, userID, hash string) {
	_, _ = q.Exec(ctx, "delete from core.email_codes where user_id = $1::uuid and code_hash = $2", userID, hash)
}
