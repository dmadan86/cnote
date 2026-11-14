package auth

// MFA: TOTP enrolment, step-up challenges, recovery codes. The TOTP secret is encrypted with CNOTE_MASTER_KEY
// (secretbox, bound to the user id); recovery codes are stored as sha256 of a 50-bit random value, single use.

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/secretbox"
)

const (
	challengeTTLMinutes = 5
	challengeAttempts   = 5
	recoveryCount       = 10
	freeMFA             = 5
)

var errBadCode = command.NewError(command.Unauthenticated, "invalid code")

func (a *Auth) MFAEnabled(ctx context.Context, userID string) (bool, error) {
	var on bool
	err := a.Pool.QueryRow(ctx, "select exists (select 1 from core.user_mfa where user_id = $1::uuid and confirmed_at is not null)", userID).Scan(&on)
	return on, command.DB(err)
}

func (a *Auth) secret(ctx context.Context, userID string, confirmed bool) ([]byte, int64, error) {
	var blob []byte
	var last int64
	err := a.Pool.QueryRow(ctx, "select secret_enc, last_step from core.user_mfa where user_id = $1::uuid and (confirmed_at is not null) = $2", userID, confirmed).Scan(&blob, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errBadCode
	}
	if err != nil {
		return nil, 0, command.DB(err)
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		return nil, 0, err
	}
	s, err := secretbox.Decrypt(key, userID, blob)
	if err != nil {
		return nil, 0, err
	}
	raw, err := b32.DecodeString(s)
	return raw, last, err
}

// MFAStart creates (or replaces) a pending secret and returns it with the otpauth URI for the QR code.
func (a *Auth) MFAStart(ctx context.Context, userID, email string) (string, string, error) {
	if on, err := a.MFAEnabled(ctx, userID); err != nil || on {
		if err != nil {
			return "", "", err
		}
		return "", "", command.ErrConflict("multi-factor authentication is already on; disable it first")
	}
	key, err := secretbox.MasterKey()
	if err != nil {
		return "", "", err
	}
	sec, err := newTOTPSecret()
	if err != nil {
		return "", "", command.NewError(command.Internal, "random: "+err.Error())
	}
	blob, err := secretbox.Encrypt(key, userID, b32.EncodeToString(sec))
	if err != nil {
		return "", "", err
	}
	_, err = a.Pool.Exec(ctx, `insert into core.user_mfa (user_id, secret_enc) values ($1::uuid, $2)
		on conflict (user_id) do update set secret_enc = excluded.secret_enc, last_step = 0, created_at = now() where core.user_mfa.confirmed_at is null`, userID, blob)
	return b32.EncodeToString(sec), OTPAuthURI(sec, email), command.DB(err)
}

// useTOTP accepts a code once: the matched step must be newer than the last accepted one (atomic, so two parallel
// requests with the same code cannot both pass).
func (a *Auth) useTOTP(ctx context.Context, userID, code string, confirmed bool) (bool, error) {
	sec, last, err := a.secret(ctx, userID, confirmed)
	if err != nil {
		return false, err
	}
	step, ok := totpMatch(sec, code, time.Now())
	if !ok || step <= last {
		return false, nil
	}
	tag, err := a.Pool.Exec(ctx, "update core.user_mfa set last_step = $2 where user_id = $1::uuid and last_step < $2", userID, step)
	return tag.RowsAffected() == 1, command.DB(err)
}

func newRecoveryCode() string {
	b := make([]byte, 7) // 56 bits, 10 base32 characters used
	_, _ = rand.Read(b)
	s := strings.ToLower(b32.EncodeToString(b))[:10]
	return s[:5] + "-" + s[5:]
}

// MFAConfirm checks the first code from the app, switches MFA on and returns the recovery codes (shown once).
func (a *Auth) MFAConfirm(ctx context.Context, userID, code string) ([]string, error) {
	if ok, err := a.useTOTP(ctx, userID, normCode(code), false); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, errBadCode
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return nil, command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "update core.user_mfa set confirmed_at = now() where user_id = $1::uuid", userID); err != nil {
		return nil, command.DB(err)
	}
	codes, err := replaceRecoveryCodes(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	return codes, command.DB(tx.Commit(ctx))
}

// replaceRecoveryCodes deletes the person's codes, used or not, and stores a fresh set; the old set stops working.
func replaceRecoveryCodes(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	if _, err := tx.Exec(ctx, "delete from core.mfa_recovery_codes where user_id = $1::uuid", userID); err != nil {
		return nil, command.DB(err)
	}
	codes := make([]string, recoveryCount)
	for i := range codes {
		codes[i] = newRecoveryCode()
		if _, err := tx.Exec(ctx, "insert into core.mfa_recovery_codes (user_id, code_hash) values ($1::uuid, $2)", userID, sha256Hex(normCode(codes[i]))); err != nil {
			return nil, command.DB(err)
		}
	}
	return codes, nil
}

// RegenerateRecovery needs the password and a current authenticator code (a recovery code is not enough: it would let a
// stolen code mint more), then replaces the whole set.
func (a *Auth) RegenerateRecovery(ctx context.Context, userID, password, code string) ([]string, error) {
	var hash *string
	if err := a.Pool.QueryRow(ctx, "select password_hash from core.users where id = $1::uuid", userID).Scan(&hash); err != nil {
		return nil, command.DB(err)
	}
	bad := command.NewError(command.Unauthenticated, "invalid password or code")
	if hash == nil || !VerifyPassword(password, *hash) || len(normCode(code)) != 6 {
		return nil, bad
	}
	if err := a.checkSecondFactor(ctx, userID, code); err != nil {
		if e, ok := err.(*command.Error); ok && e.Code == command.Unauthenticated {
			return nil, bad
		}
		return nil, err
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return nil, command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	codes, err := replaceRecoveryCodes(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	return codes, command.DB(tx.Commit(ctx))
}

// checkSecondFactor verifies a TOTP code or a recovery code for an enrolled person, throttled per person.
func (a *Auth) checkSecondFactor(ctx context.Context, userID, code string) error {
	key := "mfa:" + userID
	if d, err := a.locked(ctx, key); err != nil || d > 0 {
		if err != nil {
			return err
		}
		return throttled(d)
	}
	code = normCode(code)
	ok, err := false, error(nil)
	if len(code) == 6 {
		ok, err = a.useTOTP(ctx, userID, code, true)
	} else if len(code) == 10 {
		var tag pgconn.CommandTag
		tag, err = a.Pool.Exec(ctx, "update core.mfa_recovery_codes set used_at = now() where user_id = $1::uuid and code_hash = $2 and used_at is null", userID, sha256Hex(code))
		ok = err == nil && tag.RowsAffected() == 1
	}
	if err != nil {
		return err
	}
	if ok {
		a.forget(ctx, key)
		return nil
	}
	if d, err := a.fail(ctx, key, freeMFA); err == nil && d > 0 {
		a.auditLockout(ctx, userID, "mfa", d)
	}
	return errBadCode
}

// NewChallenge is the step-up token handed out after a correct password: five minutes, five tries.
func (a *Auth) NewChallenge(ctx context.Context, userID string) (string, error) {
	tok := RandomToken(32)
	_, err := a.Pool.Exec(ctx, "insert into core.mfa_challenges (id, user_id, expires_at) values ($1, $2::uuid, now() + make_interval(mins => $3))", sha256Hex(tok), userID, challengeTTLMinutes)
	return tok, command.DB(err)
}

// CompleteChallenge turns a challenge token plus a code into a session (amr mfa).
func (a *Auth) CompleteChallenge(ctx context.Context, token, code string) (string, string, error) {
	var uid string
	err := a.Pool.QueryRow(ctx, `update core.mfa_challenges set attempts = attempts + 1 where id = $1 and expires_at > now() and attempts < $2
		returning user_id::text`, sha256Hex(token), challengeAttempts).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", command.NewError(command.Unauthenticated, "this sign-in expired; start again")
	}
	if err != nil {
		return "", "", command.DB(err)
	}
	if err := a.checkSecondFactor(ctx, uid, code); err != nil {
		return "", "", err
	}
	_, _ = a.Pool.Exec(ctx, "delete from core.mfa_challenges where id = $1", sha256Hex(token))
	tok, err := a.createSession(ctx, uid, "mfa", "")
	return uid, tok, err
}

// UpgradeSession marks a session as MFA-proven (right after enrolment, so a required-MFA admin can carry on).
func (a *Auth) UpgradeSession(ctx context.Context, token string) error {
	_, err := a.Pool.Exec(ctx, "update core.sessions set amr = 'mfa' where id = $1 and amr = 'pwd'", sha256Hex(token))
	return command.DB(err)
}

// MFADisable needs the password and a current code; refused while a workspace requires admins to have MFA.
func (a *Auth) MFADisable(ctx context.Context, userID, password, code string) error {
	var hash *string
	if err := a.Pool.QueryRow(ctx, "select password_hash from core.users where id = $1::uuid", userID).Scan(&hash); err != nil {
		return command.DB(err)
	}
	if hash == nil || !VerifyPassword(password, *hash) {
		return command.NewError(command.Unauthenticated, "invalid password or code")
	}
	if err := a.checkSecondFactor(ctx, userID, code); err != nil {
		if e, ok := err.(*command.Error); ok && e.Code == command.Unauthenticated {
			return command.NewError(command.Unauthenticated, "invalid password or code")
		}
		return err
	}
	var req bool
	if err := a.Pool.QueryRow(ctx, "select core.mfa_required_for($1::uuid)", userID).Scan(&req); err != nil {
		return command.DB(err)
	}
	if req {
		return command.ErrForbidden("a workspace you administer requires multi-factor authentication")
	}
	if _, err := a.Pool.Exec(ctx, "delete from core.user_mfa where user_id = $1::uuid", userID); err != nil {
		return command.DB(err)
	}
	_, err := a.Pool.Exec(ctx, "delete from core.mfa_recovery_codes where user_id = $1::uuid", userID)
	return command.DB(err)
}

// RecoveryRemaining counts unused recovery codes.
func (a *Auth) RecoveryRemaining(ctx context.Context, userID string) (int, error) {
	var n int
	err := a.Pool.QueryRow(ctx, "select count(*) from core.mfa_recovery_codes where user_id = $1::uuid and used_at is null", userID).Scan(&n)
	return n, command.DB(err)
}
