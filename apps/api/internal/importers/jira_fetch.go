package importers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
)

// Live Jira Cloud fetch (jira.rs fetch_issues). Credentials are used for this call only and never stored or logged.

// JiraClient fetches one page of /rest/api/3/search/jql (tests replace it).
type JiraClient interface {
	Search(ctx context.Context, baseURL, email, token, jql string, pageToken *string) (map[string]any, error)
}

type httpJira struct{}

var jiraHTTP = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// DefaultJira is the real client.
var DefaultJira JiraClient = httpJira{}

func (httpJira) Search(ctx context.Context, baseURL, email, token, jql string, pageToken *string) (map[string]any, error) {
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("fields", "*all")
	q.Set("expand", "changelog")
	q.Set("maxResults", "100")
	if pageToken != nil {
		q.Set("nextPageToken", *pageToken)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/rest/api/3/search/jql?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("error sending request")
	}
	req.SetBasicAuth(email, token)
	res, err := jiraHTTP.Do(req)
	if err != nil {
		return nil, errors.New("error sending request for url") // the URL and cause stay out of errors
	}
	defer res.Body.Close()
	// Status only: response bodies and URLs stay out of errors.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("Jira returned HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	var page map[string]any
	dec := json.NewDecoder(io.LimitReader(res.Body, 256<<20))
	dec.UseNumber() // keep big integers and float spelling intact
	if err := dec.Decode(&page); err != nil {
		return nil, errors.New("error decoding response body")
	}
	return page, nil
}

// ValidateBaseURL: Jira Cloud only: https, no credentials in the URL, host under atlassian.net. Anything else
// could point the server at internal addresses.
func ValidateBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" && u.Scheme == "" {
		return errors.New("base_url is not a valid URL")
	}
	host := strings.TrimRight(strings.ToLower(u.Hostname()), ".")
	if u.Scheme != "https" || u.User != nil || !strings.HasSuffix(host, ".atlassian.net") || host == ".atlassian.net" {
		return errors.New("base_url must be an https URL on a *.atlassian.net host")
	}
	return nil
}

const maxIssues = 10_000

// FetchIssues pages through the search results and returns {"issues": [...]} as JSON text.
func FetchIssues(ctx context.Context, c JiraClient, baseURL, email, token, jql string) (string, error) {
	if err := ValidateBaseURL(baseURL); err != nil {
		return "", err
	}
	issues := []any{}
	var next *string
	for {
		page, err := c.Search(ctx, baseURL, email, token, jql, next)
		if err != nil {
			return "", err
		}
		if is, ok := page["issues"].([]any); ok {
			issues = append(issues, is...)
		}
		next = nil
		if s, ok := page["nextPageToken"].(string); ok {
			next = &s
		}
		if last, _ := page["isLast"].(bool); next == nil || last || len(issues) >= maxIssues {
			break
		}
	}
	if len(issues) > maxIssues {
		issues = issues[:maxIssues]
	}
	b, err := command.Marshal(map[string]any{"issues": issues})
	return string(b), err
}

// CountRecords parses an import's stored input exactly like the import.run job does and returns the record
// count; the error text is the user-facing message import.start answers with.
func CountRecords(source string, storedInput []byte) (int, error) {
	in, err := decodeJSON(string(storedInput))
	if err != nil {
		return 0, err
	}
	p, err := parseInput(source, in)
	if err != nil {
		return 0, err
	}
	return len(p.Recs), nil
}
