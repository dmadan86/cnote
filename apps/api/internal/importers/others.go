package importers

import (
	"fmt"
	"strings"
	"time"
)

// ---------- Trello ----------

func parseTrello(content string) (*Parsed, error) {
	b, err := decodeJSON(content)
	if err != nil {
		return nil, fmt.Errorf("Trello content is not valid JSON: %v", err)
	}
	lists := map[string]string{}
	for _, l := range asArr(get(b, "lists")) {
		if id, name := sKey(l, "id"), sKey(l, "name"); id != nil && name != nil {
			lists[*id] = *name
		}
	}
	p := &Parsed{}
	p.Warnings = append(p.Warnings, "Trello members have no email in a board export: assignees are not matched")
	checklists := map[string][]string{}
	for _, c := range asArr(get(b, "checklists")) {
		md := fmt.Sprintf("### %s\n", orStr(sKey(c, "name"), "Checklist"))
		for _, it := range asArr(get(c, "checkItems")) {
			mark := " "
			if get(it, "state") == "complete" {
				mark = "x"
			}
			md += fmt.Sprintf("- [%s] %s\n", mark, orStr(sKey(it, "name"), ""))
		}
		if card := sKey(c, "idCard"); card != nil {
			checklists[*card] = append(checklists[*card], md)
		}
	}
	comments := map[string][]Comment{}
	moves := map[string][]Hist{}
	for _, a := range asArr(get(b, "actions")) {
		card := strPtr(a, "/data/card/id")
		if card == nil {
			continue
		}
		var at = parseTSPtr(sKey(a, "date"))
		author := strPtr(a, "/memberCreator/fullName")
		id := orStr(sKey(a, "id"), "")
		switch get(a, "type") {
		case "commentCard":
			comments[*card] = append(comments[*card], Comment{ID: id, Author: author, At: at, Body: orStr(strPtr(a, "/data/text"), "")})
		case "updateCard":
			if _, ok := ptr(a, "/data/listAfter/name"); ok {
				moves[*card] = append(moves[*card], Hist{ID: id, At: at, Author: author, Field: "status",
					From: strPtr(a, "/data/listBefore/name"), To: strPtr(a, "/data/listAfter/name")})
			}
		}
	}
	for _, c := range asArr(get(b, "cards")) {
		id, name := sKey(c, "id"), sKey(c, "name")
		if id == nil || name == nil {
			p.Warnings = append(p.Warnings, "skipped: card without id or name")
			continue
		}
		if closed, _ := asBool(get(c, "closed")); closed {
			p.Warnings = append(p.Warnings, "archived cards are skipped")
			continue
		}
		status := "To Do"
		if l := sKey(c, "idList"); l != nil {
			if n, ok := lists[*l]; ok {
				status = n
			}
		}
		body := orStr(sKey(c, "desc"), "")
		for _, cl := range checklists[*id] {
			body += "\n\n" + cl
		}
		item := &ItemData{ItemType: "task", Status: status, StatusCategory: categoryFromName(status), Priority: "medium",
			Labels: []string{}, Custom: map[string]any{}}
		for _, l := range asArr(get(c, "labels")) {
			if n := sKey(l, "name"); n != nil {
				item.Labels = append(item.Labels, *n)
			}
		}
		if d := sKey(c, "due"); d != nil {
			item.DueDate = parseDate(*d)
		}
		if item.StatusCategory == "done" {
			item.ResolvedAt = parseTSPtr(sKey(c, "dateLastActivity"))
		}
		url := sKey(c, "url")
		if url == nil {
			url = sKey(c, "shortUrl")
		}
		p.Recs = append(p.Recs, Rec{
			SourceURL: url, Title: *name, BodyMD: strings.TrimSpace(body), Item: item,
			History: moves[*id], Comments: comments[*id], SourceID: *id,
		})
		delete(moves, *id)
		delete(comments, *id)
	}
	return p, nil
}

func parseTSPtr(s *string) *time.Time {
	if s == nil {
		return nil
	}
	return parseTS(*s)
}

// ---------- Notion / Confluence ----------

func parsePages(source, content string) (*Parsed, error) {
	p := &Parsed{}
	t := strings.TrimLeftFunc(content, isSpaceRune)
	var pages []any
	switch {
	case strings.HasPrefix(t, "{") || strings.HasPrefix(t, "["):
		v, err := decodeJSON(content)
		if err != nil {
			return nil, fmt.Errorf("pages content is not valid JSON: %v", err)
		}
		arr := v
		if _, ok := v.([]any); !ok {
			arr = get(v, "pages")
		}
		a, ok := arr.([]any)
		if !ok {
			return nil, fmt.Errorf("expected a `pages` array")
		}
		pages = a
	case strings.HasPrefix(t, "<"):
		title := firstText(t, "title")
		if title == nil {
			title = firstText(t, "h1")
		}
		ttl := orStr(title, "Untitled")
		pages = []any{map[string]any{"id": slug(ttl), "title": ttl, "html": t}}
	default:
		title := "Untitled"
		for _, l := range rustLines(t) {
			if r, ok := strings.CutPrefix(l, "# "); ok {
				title = r
				break
			}
		}
		title = strings.TrimSpace(title)
		pages = []any{map[string]any{"id": slug(title), "title": title, "markdown": t}}
	}
	for _, pg := range pages {
		id, title := sKey(pg, "id"), sKey(pg, "title")
		if id == nil || title == nil {
			p.Warnings = append(p.Warnings, "skipped: page without id or title")
			continue
		}
		body := ""
		var warnings []string
		if md := sKey(pg, "markdown"); md != nil {
			body = *md
			if rest, ok := strings.CutPrefix(body, "# "+*title); ok {
				body = strings.TrimLeftFunc(rest, isSpaceRune)
			}
		} else if h := sKey(pg, "html"); h != nil || sKey(pg, "body") != nil {
			if h == nil {
				h = sKey(pg, "body")
			}
			md, dropped := htmlToMD(*h)
			body = md
			if dropped > 0 {
				warnings = append(warnings, fmt.Sprintf("%d unsupported %s macro(s) dropped, their text kept", dropped, source))
			}
		}
		rec := Rec{SourceURL: sKey(pg, "url"), CreatedAt: parseTSPtr(sKey(pg, "created")), Author: sKey(pg, "author"),
			Parent: sKey(pg, "parent_id"), Warnings: warnings, BodyMD: body, Title: *title, SourceID: *id}
		for i, c := range asArr(get(pg, "comments")) {
			rec.Comments = append(rec.Comments, Comment{
				ID:     orStr(sKey(c, "id"), fmt.Sprintf("c%d", i)),
				Author: sKey(c, "author"), At: parseTSPtr(sKey(c, "created")), Body: orStr(sKey(c, "body"), ""),
			})
		}
		p.Recs = append(p.Recs, rec)
	}
	return p, nil
}

// ---------- CSV ----------

type csvMapping struct {
	Target, ID, Title, Description, Status, Priority, ItemType, AssigneeEmail, Labels, Parent, URL *string
}

// decodeMapping is serde's CsvMapping: title is a required string, the rest optional strings (null = absent).
func decodeMapping(v any) (*csvMapping, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	var out csvMapping
	for k, dst := range map[string]**string{"target": &out.Target, "id": &out.ID, "title": &out.Title, "description": &out.Description,
		"status": &out.Status, "priority": &out.Priority, "item_type": &out.ItemType, "assignee_email": &out.AssigneeEmail,
		"labels": &out.Labels, "parent": &out.Parent, "url": &out.URL} {
		switch x := m[k].(type) {
		case nil:
		case string:
			*dst = sp(x)
		default:
			return nil, false
		}
	}
	return &out, out.Title != nil
}

// readCSV mirrors csv-core defaults (flexible, quote ", doubled quotes, \r \n \r\n terminators, blank lines skipped).
func readCSV(s string) [][]string {
	var recs [][]string
	var rec []string
	var field strings.Builder
	const (
		startRecord = iota
		startField
		inField
		inQuoted
		afterQuote
	)
	state := startRecord
	endField := func() { rec = append(rec, field.String()); field.Reset() }
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case startRecord:
			if c == '\n' || c == '\r' {
				continue
			}
			state = startField
			i--
		case startField, inField, afterQuote:
			switch {
			case state == startField && c == '"':
				state = inQuoted
			case state == afterQuote && c == '"':
				field.WriteByte('"')
				state = inQuoted
			case c == ',':
				endField()
				state = startField
			case c == '\n' || c == '\r':
				endField()
				recs = append(recs, rec)
				rec, state = nil, startRecord
			default:
				field.WriteByte(c)
				state = inField
			}
		case inQuoted:
			if c == '"' {
				state = afterQuote
			} else {
				field.WriteByte(c)
			}
		}
	}
	if state != startRecord {
		endField()
		recs = append(recs, rec)
	}
	return recs
}

func parseCSV(content string, m *csvMapping) (*Parsed, error) {
	all := readCSV(strings.TrimPrefix(content, "\ufeff")) // the csv crate drops a leading UTF-8 BOM
	var headers []string
	var rows [][]string
	if len(all) > 0 {
		headers, rows = all[0], all[1:]
	}
	col := func(name *string) (int, bool, error) {
		if name == nil {
			return 0, false, nil
		}
		for i, h := range headers {
			if h == *name {
				return i, true, nil
			}
		}
		return 0, false, fmt.Errorf("mapped column '%s' is not in the CSV header", *name)
	}
	type cc struct {
		i  int
		ok bool
	}
	cols := make([]cc, 0, 10)
	for _, n := range []*string{m.Title, m.ID, m.Description, m.Status, m.Priority, m.ItemType, m.AssigneeEmail, m.Labels, m.Parent, m.URL} {
		i, ok, err := col(n)
		if err != nil {
			return nil, err
		}
		cols = append(cols, cc{i, ok})
	}
	titleCol, idC, descC, statusC, prioC, typeC, asgC, labC, parC, urlC := cols[0], cols[1], cols[2], cols[3], cols[4], cols[5], cols[6], cols[7], cols[8], cols[9]
	mapped := map[int]bool{}
	for _, c := range cols {
		if c.ok {
			mapped[c.i] = true
		}
	}
	pages := false
	switch {
	case m.Target == nil || *m.Target == "items":
	case *m.Target == "pages":
		pages = true
	default:
		return nil, fmt.Errorf("mapping.target must be 'items' or 'pages', got '%s'", *m.Target)
	}
	p := &Parsed{}
	if !idC.ok {
		p.Warnings = append(p.Warnings, "no id column mapped: titles identify rows, so renamed rows import as new")
	}
	var unmapped []string
	for i, h := range headers {
		if !mapped[i] {
			unmapped = append(unmapped, h)
		}
	}
	if len(unmapped) > 0 && !pages {
		p.Warnings = append(p.Warnings, "unmapped columns stored under custom: "+strings.Join(unmapped, ", "))
	}
	for _, row := range rows {
		get := func(c cc) *string {
			if !c.ok || c.i >= len(row) {
				return nil
			}
			if v := strings.TrimSpace(row[c.i]); v != "" {
				return &v
			}
			return nil
		}
		title := get(titleCol)
		if title == nil {
			p.Warnings = append(p.Warnings, "skipped: row without a title")
			continue
		}
		rec := Rec{SourceID: orStr(get(idC), *title), SourceURL: get(urlC), BodyMD: orStr(get(descC), ""), Parent: get(parC), Title: *title}
		if !pages {
			status := orStr(get(statusC), "To Do")
			item := &ItemData{ItemType: "task", Status: status, StatusCategory: categoryFromName(status), Priority: "medium",
				AssigneeEmail: get(asgC), Labels: []string{}, Custom: map[string]any{}}
			if t := get(typeC); t != nil {
				item.ItemType = slug(*t)
			}
			if l := get(labC); l != nil {
				for _, x := range strings.Split(*l, ",") {
					if x = strings.TrimSpace(x); x != "" {
						item.Labels = append(item.Labels, x)
					}
				}
			}
			if pr := get(prioC); pr != nil {
				if x, ok := mapPriority(*pr); ok {
					item.Priority = x
				} else {
					rec.Warnings = append(rec.Warnings, fmt.Sprintf("unknown priority '%s' mapped to medium", *pr))
				}
			}
			for i, h := range headers {
				if !mapped[i] {
					if v := get(cc{i, true}); v != nil {
						item.Custom[h] = *v
					}
				}
			}
			rec.Item = item
		}
		p.Recs = append(p.Recs, rec)
	}
	return p, nil
}
