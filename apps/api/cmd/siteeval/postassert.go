package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PostSnap is a post as the eval sees it, before or after the turn.
type PostSnap struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	Body        json.RawMessage `json:"body"`
	Changed     bool            `json:"-"`
	Plan        json.RawMessage `json:"-"`
	Reply       string          `json:"-"`
}

// parsePostFixture reads fixture_blog.json: {"post": {...}} or the post itself; a bare TipTap doc is the body.
func parsePostFixture(raw []byte) (PostSnap, error) {
	var top struct {
		Post *PostSnap `json:"post"`
		PostSnap
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return PostSnap{}, err
	}
	switch {
	case top.Post != nil:
		return *top.Post, nil
	case top.Type == "doc":
		return PostSnap{Title: "Untitled", Body: raw}, nil
	}
	return top.PostSnap, nil
}

func postNodes(body json.RawMessage) []map[string]any {
	var doc struct {
		Content []map[string]any `json:"content"`
	}
	_ = json.Unmarshal(body, &doc)
	return doc.Content
}

func nodeText(n map[string]any) string {
	switch n["type"] {
	case "text":
		s, _ := n["text"].(string)
		return s
	case "hardBreak":
		return "\n"
	}
	list, _ := n["content"].([]any)
	var parts []string
	for _, c := range list {
		m, _ := c.(map[string]any)
		parts = append(parts, nodeText(m))
	}
	if n["type"] == "paragraph" || n["type"] == "heading" {
		return strings.Join(parts, "")
	}
	return strings.Join(parts, "\n")
}

func nodeKind(n map[string]any) string {
	switch n["type"] {
	case "paragraph":
		return "p"
	case "heading":
		if a, _ := n["attrs"].(map[string]any); fmt.Sprint(a["level"]) == "3" {
			return "h3"
		}
		return "h2"
	case "bulletList":
		return "ul"
	case "orderedList":
		return "ol"
	case "blockquote":
		return "quote"
	}
	return "other"
}

func countWalk(v any, f func(map[string]any) int) int {
	switch t := v.(type) {
	case map[string]any:
		n := f(t)
		for _, c := range t {
			n += countWalk(c, f)
		}
		return n
	case []any:
		n := 0
		for _, c := range t {
			n += countWalk(c, f)
		}
		return n
	}
	return 0
}

// resolvePostPath reads a "post/..." path:
//
//	post/changed | title | description | reply | text (all text, blocks joined by newlines)
//	post/blocks (list) | blocks.N.type (p h2 h3 ul ol quote other) | blocks.N.text | blocks.N.json (the node, for byte-identity)
//	post/links, post/images (counts) | post/plan.title | post/plan.points (list)
func resolvePostPath(s PostSnap, path string) (any, bool) {
	path = strings.TrimPrefix(path, "post/")
	nodes := postNodes(s.Body)
	switch path {
	case "changed":
		return strconv.FormatBool(s.Changed), true
	case "title":
		return s.Title, true
	case "description":
		return s.Description, true
	case "reply":
		return s.Reply, true
	case "text":
		var parts []string
		for _, n := range nodes {
			parts = append(parts, nodeText(n))
		}
		return strings.Join(parts, "\n"), true
	case "blocks":
		list := make([]any, len(nodes))
		for i, n := range nodes {
			list[i] = n
		}
		return list, true
	case "links":
		return countWalk(nodes2any(nodes), func(m map[string]any) int {
			if m["type"] == "link" {
				return 1
			}
			return 0
		}), true
	case "images":
		return countWalk(nodes2any(nodes), func(m map[string]any) int {
			if t := m["type"]; t == "image" || t == "siteImage" {
				return 1
			}
			return 0
		}), true
	}
	if rest, ok := strings.CutPrefix(path, "plan."); ok {
		var p map[string]any
		if json.Unmarshal(s.Plan, &p) != nil || p == nil {
			return nil, false
		}
		v, ok := p[rest]
		return v, ok
	}
	if rest, ok := strings.CutPrefix(path, "blocks."); ok {
		idx, field, _ := strings.Cut(rest, ".")
		i, err := strconv.Atoi(idx)
		if err != nil || i < 0 || i >= len(nodes) {
			return nil, false
		}
		switch field {
		case "type":
			return nodeKind(nodes[i]), true
		case "text":
			return nodeText(nodes[i]), true
		case "json":
			b, _ := json.Marshal(nodes[i])
			return string(b), true
		}
	}
	return nil, false
}

func nodes2any(n []map[string]any) any {
	out := make([]any, len(n))
	for i := range n {
		out[i] = n[i]
	}
	return out
}

// EvalPostAssertions runs the assertions whose path starts with "post/" against the post after the turn.
func EvalPostAssertions(caseID string, after PostSnap, assertions []Assertion) []*AssertionError {
	return evalAssertions(caseID, assertions, func(p string) (any, bool) { return resolvePostPath(after, p) })
}

// splitAssertions separates post/ assertions from page assertions.
func splitAssertions(all []Assertion) (page, post []Assertion) {
	for _, a := range all {
		if strings.HasPrefix(a.Path, "post/") {
			post = append(post, a)
		} else {
			page = append(page, a)
		}
	}
	return
}

// CheckPostCollateral flags post changes outside may_change. Tokens: post/title, post/description, post/body (any
// block) and post/block.N (that block only, including blocks added at the end).
func CheckPostCollateral(caseID string, before, after PostSnap, mayChange []string) []string {
	allowed := map[string]bool{}
	for _, t := range mayChange {
		allowed[t] = true
	}
	var diffs []string
	if before.Title != after.Title && !allowed["post/title"] {
		diffs = append(diffs, caseID+": collateral: title changed outside may_change")
	}
	if before.Description != after.Description && !allowed["post/description"] {
		diffs = append(diffs, caseID+": collateral: description changed outside may_change")
	}
	if allowed["post/body"] {
		return diffs
	}
	bn, an := postNodes(before.Body), postNodes(after.Body)
	for i := 0; i < len(bn) || i < len(an); i++ {
		if allowed["post/block."+strconv.Itoa(i)] {
			continue
		}
		switch {
		case i >= len(an):
			diffs = append(diffs, fmt.Sprintf("%s: collateral: block %d disappeared", caseID, i))
		case i >= len(bn):
			diffs = append(diffs, fmt.Sprintf("%s: collateral: block %d was added", caseID, i))
		default:
			b, _ := json.Marshal(bn[i])
			a, _ := json.Marshal(an[i])
			if string(b) != string(a) {
				diffs = append(diffs, fmt.Sprintf("%s: collateral: block %d changed outside may_change", caseID, i))
			}
		}
	}
	return diffs
}
