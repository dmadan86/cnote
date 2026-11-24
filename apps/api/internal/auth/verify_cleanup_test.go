package auth

import (
	"context"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"testing"
)

type codeStore struct{ hash string }

func (s *codeStore) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(sql, "code_hash = $2") {
		s.hash = ""
	} else if args[1] == s.hash {
		s.hash = ""
	}
	return pgconn.CommandTag{}, nil
}
func TestFailedCodeCleanupPreservesReplacement(t *testing.T) {
	s := &codeStore{hash: "new"}
	deleteFailedCode(context.Background(), s, "user", "old")
	if s.hash != "new" {
		t.Fatal("deleted newer code")
	}
	deleteFailedCode(context.Background(), s, "user", "new")
	if s.hash != "" {
		t.Fatal("failed to delete matching code")
	}
}
