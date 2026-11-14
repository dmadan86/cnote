package importers

import (
	"context"
	"testing"
)

type fakeJira struct{}

func (fakeJira) Search(_ context.Context, _, _, _, _ string, t *string) (map[string]any, error) {
	if t == nil {
		return map[string]any{"issues": []any{map[string]any{"key": "A-1"}}, "nextPageToken": "p2"}, nil
	}
	return map[string]any{"issues": []any{map[string]any{"key": "A-2"}}, "isLast": true}, nil
}

// Ports of fetch_issues / base_url_must_be_jira_cloud in jira.rs.
func TestFetchIssuesPagesAndRejectsOtherHosts(t *testing.T) {
	for _, bad := range []string{"http://acme.atlassian.net", "https://localhost", "https://169.254.169.254", "https://10.0.0.5", "https://evil.com",
		"https://user:pw@x.atlassian.net", "https://x.atlassian.net.evil.com", "https://.atlassian.net"} {
		if _, err := FetchIssues(context.Background(), fakeJira{}, bad, "a@example.com", "t", "x"); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	for _, ok := range []string{"https://acme.atlassian.net", "https://ACME.atlassian.net./"} {
		if err := ValidateBaseURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	got, err := FetchIssues(context.Background(), fakeJira{}, "https://acme.atlassian.net", "a@example.com", "t", "x")
	if err != nil || got != `{"issues":[{"key":"A-1"},{"key":"A-2"}]}` {
		t.Fatalf("%q %v", got, err)
	}
}
