package mailer

import (
	"context"
	"errors"
)

// send is replaced by agent B's implementation (delete this file then).
func send(context.Context, Mail) (string, error) { return "", errors.New("mailer: not implemented") }
