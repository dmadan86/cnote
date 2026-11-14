package command

import (
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Code and Error mirror crates/cnote-core/src/error.rs: same codes, statuses and JSON shape.
type Code string

const (
	Unauthenticated Code = "unauthenticated"
	Forbidden       Code = "forbidden"
	NotFound        Code = "not_found"
	Invalid         Code = "invalid"
	Conflict        Code = "conflict"
	RateLimited     Code = "rate_limited"
	PlanLimit       Code = "plan_limit"
	Internal        Code = "internal"
)

func (c Code) Status() int {
	switch c {
	case Unauthenticated:
		return 401
	case Forbidden:
		return 403
	case NotFound:
		return 404
	case Invalid:
		return 400
	case Conflict:
		return 409
	case RateLimited:
		return 429
	case PlanLimit:
		return 402
	}
	return 500
}

type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func NewError(c Code, msg string) *Error { return &Error{Code: c, Message: msg} }
func ErrNotFound(what string) *Error     { return NewError(NotFound, what+" not found") }
func ErrForbidden(msg string) *Error     { return NewError(Forbidden, msg) }
func ErrInvalid(msg string) *Error       { return NewError(Invalid, msg) }
func ErrConflict(msg string) *Error      { return NewError(Conflict, msg) }

// ErrFallback tells the router to hand the request to the Rust server unchanged. Handlers return it
// for inputs whose Rust error text they do not reproduce (bad JSON shape, odd UUID spellings), so
// those paths stay byte-identical by construction.
var ErrFallback = errors.New("fallback to upstream")

// DB maps a database error like `impl From<sqlx::Error> for CmdError`.
func DB(err error) error {
	if err == nil {
		return nil
	}
	var ce *Error
	if errors.As(err, &ce) || errors.Is(err, ErrFallback) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return NewError(NotFound, "not found")
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return ErrConflict(pg.Message)
		case "23503", "23514", "22P02", "P0001":
			return ErrInvalid(pg.Message)
		case "42501":
			return ErrForbidden("row-level security denied the operation")
		}
	}
	slog.Error("database error", "error", err)
	return NewError(Internal, "database error")
}
