package inventory

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicListRejectsTooManyIDs(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"ids": make([]string, 201)})
	if _, err := publicList(nil, b); err == nil || !strings.Contains(err.Error(), "200") {
		t.Fatalf("expected cap error, got %v", err)
	}
}
