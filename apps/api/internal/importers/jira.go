package importers

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// adf is Atlassian Document Format (or a plain string) -> Markdown-ish text.
func adf(v any, out *strings.Builder) {
	switch x := v.(type) {
	case string:
		out.WriteString(x)
	case map[string]any:
		kind, _ := x["type"].(string)
		kids := func(out *strings.Builder) {
			for _, k := range asArr(x["content"]) {
				adf(k, out)
			}
		}
		switch kind {
		case "text":
			out.WriteString(orStr(strPtrOf(x["text"]), ""))
		case "hardBreak":
			out.WriteByte('\n')
		case "mention":
			out.WriteString(orStr(strPtr(v, "/attrs/text"), "@user"))
		case "heading":
			n := uint64(1)
			if l, ok := ptr(v, "/attrs/level"); ok {
				if u, ok := asU64(l); ok {
					n = u
				}
			}
			if n > 6 {
				n = 6
			}
			out.WriteString(strings.Repeat("#", int(n)))
			out.WriteByte(' ')
			kids(out)
			out.WriteString("\n\n")
		case "listItem":
			var s strings.Builder
			kids(&s)
			fmt.Fprintf(out, "- %s\n", strings.TrimSpace(s.String()))
		case "bulletList", "orderedList":
			kids(out)
			out.WriteByte('\n')
		case "codeBlock":
			out.WriteString("```\n")
			kids(out)
			out.WriteString("\n```\n\n")
		case "doc":
			kids(out)
		default:
			kids(out)
			out.WriteString("\n\n")
		}
	}
}

func strPtrOf(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func textOf(v any) string {
	var s strings.Builder
	adf(v, &s)
	return strings.TrimSpace(s.String())
}

func person(v any) (*string, *string) { return strAt(v, "/displayName"), strAt(v, "/emailAddress") }

func isSprint(v any) bool {
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return false
	}
	for _, s := range a {
		if !(has(s, "name") && has(s, "state") && (has(s, "boardId") || has(s, "startDate"))) {
			return false
		}
	}
	return true
}

var jiraKnown = map[string]bool{}
var jiraIgnored = map[string]bool{}

func init() {
	for _, k := range []string{"summary", "description", "issuetype", "status", "priority", "assignee", "reporter", "labels",
		"components", "fixVersions", "parent", "issuelinks", "comment", "attachment", "created", "updated", "duedate",
		"resolutiondate", "customfield_10016", "customfield_10014", "project", "creator", "resolution", "subtasks"} {
		jiraKnown[k] = true
	}
	for _, k := range []string{"statuscategorychangedate", "lastViewed", "watches", "votes", "worklog", "timetracking",
		"aggregateprogress", "progress", "workratio", "security", "environment", "versions", "issuerestriction", "aggregatetimespent"} {
		jiraIgnored[k] = true
	}
}

func parseIssue(issue any) (Rec, error) {
	key := strAt(issue, "/key")
	if key == nil {
		return Rec{}, fmt.Errorf("issue without key")
	}
	f, ok := issue.(map[string]any)["fields"]
	if !ok {
		return Rec{}, fmt.Errorf("%s: no fields", *key)
	}
	summary := strAt(f, "/summary")
	if summary == nil {
		return Rec{}, fmt.Errorf("%s: no summary", *key)
	}
	rec := Rec{SourceID: *key, Title: *summary}
	if d, ok := f.(map[string]any)["description"]; ok {
		rec.BodyMD = textOf(d)
	}
	if c := strAt(f, "/created"); c != nil {
		rec.CreatedAt = parseTS(*c)
	}
	rec.Author = strAt(f, "/creator/displayName")
	if rec.Author == nil {
		rec.Author = strAt(f, "/reporter/displayName")
	}
	if self := strAt(issue, "/self"); self != nil {
		if origin, _, ok := strings.Cut(*self, "/rest/"); ok {
			rec.SourceURL = sp(origin + "/browse/" + *key)
		}
	}

	item := &ItemData{ItemType: "task", Status: "To Do", StatusCategory: "todo", Priority: "medium", Custom: map[string]any{}}
	if t := strAt(f, "/issuetype/name"); t != nil {
		item.ItemType = slug(*t)
	}
	if s := strAt(f, "/status/name"); s != nil {
		item.Status = *s
	}
	switch orStr(strAt(f, "/status/statusCategory/key"), "") {
	case "done":
		item.StatusCategory = "done"
	case "indeterminate":
		item.StatusCategory = "in_progress"
	}
	if p := strAt(f, "/priority/name"); p != nil {
		if m, ok := mapPriority(*p); ok {
			item.Priority = m
		} else {
			rec.Warnings = append(rec.Warnings, fmt.Sprintf("unknown priority '%s' mapped to medium", *p))
		}
	}
	if has(f, "assignee") {
		aname, aemail := person(get(f, "assignee"))
		item.AssigneeEmail = aemail
		if aname != nil {
			item.Custom["source_assignee"] = *aname
		}
	}
	if has(f, "reporter") {
		rname, remail := person(get(f, "reporter"))
		item.ReporterEmail = remail
		if rname != nil {
			item.Custom["source_reporter"] = *rname
		}
	}
	item.Labels = []string{}
	for _, l := range asArr(get(f, "labels")) {
		if s, ok := l.(string); ok {
			item.Labels = append(item.Labels, s)
		}
	}
	var comps []string
	for _, c := range asArr(get(f, "components")) {
		if s, ok := get(c, "name").(string); ok {
			comps = append(comps, s)
		}
	}
	if len(comps) > 0 {
		item.Component = sp(comps[0])
	}
	if len(comps) > 1 {
		rec.Warnings = append(rec.Warnings, "only the first component is kept per item")
	}
	fixes := asArr(get(f, "fixVersions"))
	if len(fixes) > 0 {
		if name, ok := get(fixes[0], "name").(string); ok {
			released, _ := asBool(get(fixes[0], "released"))
			v := &Version{Name: name, Released: released}
			if d, ok := get(fixes[0], "releaseDate").(string); ok {
				v.Date = parseDate(d)
			}
			item.FixVersion = v
		}
	}
	if len(fixes) > 1 {
		rec.Warnings = append(rec.Warnings, "only the first fix version is kept per item")
	}
	if e, ok := asF64(get(f, "customfield_10016")); ok {
		item.Estimate = &e
	}
	if d := strAt(f, "/duedate"); d != nil {
		item.DueDate = parseDate(*d)
	}
	if item.StatusCategory == "done" {
		r := strAt(f, "/resolutiondate")
		if r == nil {
			r = strAt(f, "/updated")
		}
		if r != nil {
			item.ResolvedAt = parseTS(*r)
		}
	}
	if obj, ok := f.(map[string]any); ok {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k) // serde_json's Map is a BTreeMap: iterate sorted
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := obj[k]
			if v == nil || jiraKnown[k] || jiraIgnored[k] {
				continue
			}
			if strings.HasPrefix(k, "customfield_") && isSprint(v) {
				sprints := v.([]any)
				s := sprints[len(sprints)-1]
				sprint := &Sprint{Name: "Sprint", State: "future"}
				if n, ok := get(s, "name").(string); ok {
					sprint.Name = n
				}
				switch get(s, "state") {
				case "active":
					sprint.State = "active"
				case "closed":
					sprint.State = "closed"
				}
				if d, ok := get(s, "startDate").(string); ok {
					sprint.Start = parseDate(d)
				}
				if d, ok := get(s, "endDate").(string); ok {
					sprint.End = parseDate(d)
				}
				item.Sprint = sprint
				if len(sprints) > 1 {
					rec.Warnings = append(rec.Warnings, "sprint history flattened to the latest sprint")
				}
			} else if a, ok := v.([]any); ok && len(a) == 0 {
				// empty list: nothing to lose
			} else {
				rec.Warnings = append(rec.Warnings, fmt.Sprintf("unknown field '%s' not imported", k))
			}
		}
	}
	rec.Item = item

	rec.Parent = strAt(f, "/parent/key")
	if rec.Parent == nil {
		rec.Parent = strAt(f, "/customfield_10014")
	}
	for _, l := range asArr(get(f, "issuelinks")) {
		blocks := false
		if n, ok := get(get(l, "type"), "name").(string); ok {
			blocks = strings.Contains(rustLower(n), "block")
		}
		kind := "relates"
		if blocks {
			kind = "blocks"
		}
		if k := strAt(l, "/outwardIssue/key"); k != nil {
			rec.Links = append(rec.Links, Link{Kind: kind, Other: *k, Outward: true})
		} else if k := strAt(l, "/inwardIssue/key"); k != nil {
			rec.Links = append(rec.Links, Link{Kind: kind, Other: *k, Outward: false})
		}
	}

	for _, h := range asArr(get(get(issue, "changelog"), "histories")) {
		hid := orStr(strPtrOf(get(h, "id")), "0")
		for i, it := range asArr(get(h, "items")) {
			hist := Hist{
				ID:     fmt.Sprintf("%s:%d", hid, i),
				Author: strAt(h, "/author/displayName"),
				Field:  orStr(strPtrOf(get(it, "field")), ""),
				From:   strAt(it, "/fromString"),
				To:     strAt(it, "/toString"),
			}
			if c := strAt(h, "/created"); c != nil {
				hist.At = parseTS(*c)
			}
			rec.History = append(rec.History, hist)
		}
	}
	for _, c := range asArr(get(get(f, "comment"), "comments")) {
		name, email := person(get(c, "author"))
		cm := Comment{ID: orStr(strPtrOf(get(c, "id")), ""), Author: name, AuthorEmail: email, Body: textOf(get(c, "body"))}
		if t := strAt(c, "/created"); t != nil {
			cm.At = parseTS(*t)
		}
		rec.Comments = append(rec.Comments, cm)
	}
	for _, a := range asArr(get(f, "attachment")) {
		if url := strAt(a, "/content"); url != nil {
			size, _ := asI64(get(a, "size"))
			rec.Attachments = append(rec.Attachments, Attach{
				Filename:    orStr(strAt(a, "/filename"), "attachment"),
				ContentType: orStr(strAt(a, "/mimeType"), "application/octet-stream"),
				Size:        size,
				URL:         *url,
			})
		}
	}
	return rec, nil
}

// jqlSupported: saved filters need JQL that C Note's query language can run; flag anything beyond simple field clauses.
// Rust: call = (?i)(\w+)\s*\( must only name in/and/or/not; history = (?i)\b(was|changed|during)\b|~|cf\[ must not match.
func jqlSupported(jql string) bool {
	rs := []rune(jql)
	for i := 0; i < len(rs); {
		if !isWordRune(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isWordRune(rs[j]) {
			j++
		}
		k := j
		for k < len(rs) && isSpaceRune(rs[k]) {
			k++
		}
		if k < len(rs) && rs[k] == '(' {
			switch rustLower(string(rs[i:j])) {
			case "in", "and", "or", "not":
			default:
				return false
			}
			i = k + 1
		} else {
			i = j
		}
	}
	for i := range rs {
		if rs[i] == '~' || hasFoldPrefix(rs[i:], "cf[") {
			return false
		}
		for _, w := range []string{"was", "changed", "during"} {
			if hasFoldPrefix(rs[i:], w) && (i == 0 || !isWordRune(rs[i-1])) &&
				(i+len(w) == len(rs) || !isWordRune(rs[i+len(w)])) {
				return false
			}
		}
	}
	return true
}

func isSpaceRune(r rune) bool { return unicode.IsSpace(r) }

// hasFoldPrefix: rs starts with the ASCII word w, ignoring Unicode simple case folding.
func hasFoldPrefix(rs []rune, w string) bool {
	if len(rs) < len(w) {
		return false
	}
	return strings.EqualFold(string(rs[:len(w)]), w)
}

func parseJira(content string) (*Parsed, error) {
	v, err := decodeJSON(content)
	if err != nil {
		return nil, fmt.Errorf("Jira content is not valid JSON: %v", err)
	}
	issuesV := v
	if _, ok := v.([]any); !ok {
		issuesV = get(v, "issues")
	}
	issues, ok := issuesV.([]any)
	if !ok {
		return nil, fmt.Errorf("expected an `issues` array")
	}
	p := &Parsed{Warnings: []string{"Jira workflow, permission and notification schemes are flattened: each issue keeps its status name and category only"}}
	for _, i := range issues {
		r, err := parseIssue(i)
		if err != nil {
			p.Warnings = append(p.Warnings, "skipped: "+err.Error())
			continue
		}
		p.Recs = append(p.Recs, r)
	}
	for _, f := range asArr(get(v, "filters")) {
		name, jql := strAt(f, "/name"), strAt(f, "/jql")
		if name != nil && jql != nil {
			id := strAt(f, "/id")
			p.Filters = append(p.Filters, Filter{ID: orStr(id, slug(*name)), Name: *name, JQL: *jql})
		}
	}
	for _, a := range asArr(get(v, "automation_rules")) {
		if name := strAt(a, "/name"); name != nil {
			au := Automation{Name: *name, Trigger: get(a, "trigger"), Conditions: []any{}, Actions: []any{}}
			if c, ok := a.(map[string]any)["conditions"]; ok {
				au.Conditions = c
			}
			if c, ok := a.(map[string]any)["actions"]; ok {
				au.Actions = c
			}
			p.Automations = append(p.Automations, au)
		}
	}
	return p, nil
}
