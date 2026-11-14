package importers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// The `import.run` job: process one batch, save the cursor, re-enqueue until done (ADR-012).
// Phases: records (upsert notes, history, comments, attachments) -> links (parents, relations, which need every
// record to exist) -> extras (Jira saved filters and automation rules).
// shortcut: the source text is re-parsed each batch (kept in importers.jobs.input), like Rust.

type job struct {
	source  string
	spaceID string
	users   map[string]*string
}

func parseInput(source string, input any) (*Parsed, error) {
	content, _ := get(input, "content").(string)
	switch source {
	case "jira":
		return parseJira(content)
	case "trello":
		return parseTrello(content)
	case "notion", "confluence":
		return parsePages(source, content)
	case "csv":
		m, ok := decodeMapping(get(input, "mapping"))
		if !ok {
			return nil, errors.New("csv imports need a `mapping` with at least a title column")
		}
		return parseCSV(content, m)
	}
	return nil, fmt.Errorf("unknown source %s", source)
}

func marshal(v any) string {
	b, err := command.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// RunJob runs one import.run batch inside the caller's tenant transaction (c.Principal is the system principal).
// An error return means the job failed (the worker retries it); per-record failures are skipped and reported.
func RunJob(c *command.Ctx, payload json.RawMessage) error {
	pv, err := decodeJSON(string(payload))
	importID, ok := get(pv, "import_id").(string)
	if err != nil || !ok {
		return command.ErrInvalid("import_id missing")
	}
	var (
		id, source, status, cursorTxt, reportTxt, inputTxt string
		spaceID, createdBy                                 *string
	)
	if err := c.Tx.QueryRow(c.Context,
		`select id::text, source, space_id::text, status, cursor::text, report::text, input::text, created_by::text
		 from importers.jobs where id = $1::uuid for update`, importID).
		Scan(&id, &source, &spaceID, &status, &cursorTxt, &reportTxt, &inputTxt, &createdBy); err != nil {
		return command.DB(err)
	}
	if status == "done" || status == "failed" {
		return nil
	}
	// Writes are attributed to the person who started the import.
	c.Principal.UserID = createdBy
	var report Report
	if json.Unmarshal([]byte(reportTxt), &report) != nil {
		report = Report{}
	}
	input, _ := decodeJSON(inputTxt)

	var parsed *Parsed
	var perr error
	if spaceID != nil {
		parsed, perr = parseInput(source, input)
	}
	if spaceID == nil || perr != nil {
		why := "the target space was deleted"
		if perr != nil {
			why = perr.Error()
		}
		report.warn("import failed: " + why)
		_, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'failed', report = $2::jsonb, finished_at = now() where id = $1::uuid",
			id, marshal(report.norm()))
		return command.DB(err)
	}
	j := &job{source: source, spaceID: *spaceID, users: map[string]*string{}}
	bs := uint64(200)
	if b, ok := asU64(get(input, "batch_size")); ok {
		bs = b
	}
	bs = min(max(bs, 1), 1000)
	cursor, _ := decodeJSON(cursorTxt)
	phase := "records"
	if p, ok := get(cursor, "phase").(string); ok {
		phase = p
	}
	offset, _ := asU64(get(cursor, "offset"))

	if _, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'running' where id = $1::uuid", id); err != nil {
		return command.DB(err)
	}

	total := uint64(len(parsed.Recs))
	end := min(offset+bs, total)
	if offset > end && phase != "extras" {
		return command.NewError(command.Internal, "cursor offset is past the end of the records")
	}
	switch phase {
	case "records":
		if offset == 0 {
			for _, w := range parsed.Warnings {
				report.warn(w)
				if strings.HasPrefix(w, "skipped") || strings.HasPrefix(w, "archived cards") {
					report.Skipped++
				}
			}
		}
		for i := offset; i < end; i++ {
			rec := &parsed.Recs[i]
			if err := runRec(c, &report, rec.SourceID, func() error { return upsert(c, j, rec, &report) }); err != nil {
				return err
			}
		}
	case "links":
		for i := offset; i < end; i++ {
			rec := &parsed.Recs[i]
			if err := runRec(c, &report, rec.SourceID, func() error { return link(c, j, rec, &report) }); err != nil {
				return err
			}
		}
	default:
		if err := extras(c, j, parsed, &report); err != nil {
			return err
		}
	}

	nextPhase, nextOffset := phase, end
	if !(phase != "extras" && end < total) {
		nextOffset = 0
		switch phase {
		case "records":
			nextPhase = "links"
		case "links":
			nextPhase = "extras"
		default:
			nextPhase = "done"
		}
	}
	cur := marshal(map[string]any{"phase": nextPhase, "offset": nextOffset})
	reportJSON := marshal(report.norm())
	if nextPhase == "done" {
		if _, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'done', cursor = $2::jsonb, report = $3::jsonb, finished_at = now() where id = $1::uuid",
			id, cur, reportJSON); err != nil {
			return command.DB(err)
		}
		_, err := c.Emit("import.completed", nil, map[string]any{"import_id": id, "source": source, "report": json.RawMessage(reportJSON)})
		return err
	}
	if _, err := c.Tx.Exec(c.Context, "update importers.jobs set cursor = $2::jsonb, report = $3::jsonb where id = $1::uuid", id, cur, reportJSON); err != nil {
		return command.DB(err)
	}
	_, err = c.Tx.Exec(c.Context, "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,$3::jsonb,now())",
		c.WorkspaceID(), "import.run", marshal(map[string]any{"import_id": id}))
	return command.DB(err)
}

// norm makes the warnings map non-nil so it serializes as {} like Rust's empty BTreeMap.
func (r Report) norm() Report {
	if r.Warnings == nil {
		r.Warnings = map[string]uint32{}
	}
	return r
}

// runRec: one bad record must not sink the batch: each runs in a savepoint and failures are reported as skipped.
func runRec(c *command.Ctx, report *Report, what string, f func() error) error {
	if _, err := c.Tx.Exec(c.Context, "savepoint rec"); err != nil {
		return command.DB(err)
	}
	r := f()
	if r == nil {
		_, err := c.Tx.Exec(c.Context, "release savepoint rec")
		return command.DB(err)
	}
	var pg *pgconn.PgError
	var ce *command.Error
	if !errors.As(r, &pg) && !errors.As(r, &ce) {
		return r // not a statement failure: the transaction is unusable, fail the job
	}
	if _, err := c.Tx.Exec(c.Context, "rollback to savepoint rec"); err != nil {
		return command.DB(err)
	}
	report.Skipped++
	msg := "database error"
	if m, ok := command.DB(r).(*command.Error); ok {
		msg = m.Message
	}
	report.warn(fmt.Sprintf("skipped %s: %s", what, msg))
	return nil
}

func findNote(c *command.Ctx, j *job, sourceID string) (*string, error) {
	var id string
	err := c.Tx.QueryRow(c.Context, "select id::text from core.notes where workspace_id = $1::uuid and source_system = $2 and source_id = $3",
		c.WorkspaceID(), j.source, sourceID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, command.DB(err)
	}
	return &id, nil
}

func userByEmail(c *command.Ctx, j *job, email string, report *Report) (*string, error) {
	k := rustLower(email)
	if id, ok := j.users[k]; ok {
		return id, nil
	}
	var id *string
	rows, err := c.Tx.Query(c.Context,
		"select u.id::text from core.users u join core.memberships m on m.user_id = u.id where lower(u.email) = $1", k)
	if err != nil {
		return nil, command.DB(err)
	}
	if rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		id = &s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if id == nil {
		report.warn(fmt.Sprintf("%s is not a workspace member: person kept as text only", email))
	}
	j.users[k] = id
	return id, nil
}

func insertEvent(c *command.Ctx, j *job, noteID, kind, key string, at *time.Time, payload map[string]any) (bool, error) {
	payload["source_id"] = key
	var seen bool
	if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.events where note_id = $1::uuid and payload->>'source_id' = $2)",
		noteID, key).Scan(&seen); err != nil {
		return false, command.DB(err)
	}
	if seen {
		return false, nil
	}
	_, err := c.Tx.Exec(c.Context,
		`insert into core.events (workspace_id, note_id, kind, actor_type, actor_id, on_behalf_of, payload, created_at)
		 values ($1::uuid,$2::uuid,$3,'import',$4,$5::uuid,$6::jsonb,coalesce($7::timestamptz, now()))`,
		c.WorkspaceID(), noteID, kind, j.source, c.Principal.UserID, marshal(payload), at)
	return err == nil, command.DB(err)
}

func upsert(c *command.Ctx, j *job, rec *Rec, report *Report) error {
	if strings.TrimSpace(rec.Title) == "" || rec.SourceID == "" {
		return command.ErrInvalid("record has no title or id")
	}
	for _, w := range rec.Warnings {
		report.warn(w)
	}
	existing, err := findNote(c, j, rec.SourceID)
	if err != nil {
		return err
	}
	var noteID string
	created := false
	if existing != nil {
		// A re-run applies the source's current state; content_ydoc is dropped so the editor reseeds from content_md.
		if _, err := c.Tx.Exec(c.Context,
			`update core.notes set title = $2, content_md = $3, source_url = $4,
			   content_ydoc = case when content_md is distinct from $3 then null else content_ydoc end,
			   version = version + 1, updated_at = now()
			 where id = $1::uuid and (title is distinct from $2 or content_md is distinct from $3 or source_url is distinct from $4)`,
			*existing, strings.TrimSpace(rec.Title), rec.BodyMD, rec.SourceURL); err != nil {
			return command.DB(err)
		}
		report.Updated++
		noteID = *existing
	} else {
		types := []string{}
		if rec.Item != nil {
			types = []string{rec.Item.ItemType} // same convention as projects item.create: the note's type is the item type
		}
		body := rec.BodyMD
		n, err := notes.InsertNote(c, j.spaceID, rec.Title, nil, &body, types)
		if err != nil {
			return err
		}
		if _, err := c.Tx.Exec(c.Context,
			`update core.notes set source_system = $2, source_id = $3, source_url = $4,
			   created_at = coalesce($5::timestamptz, created_at), freshness = case when $6::bool then 'unverified' else freshness end
			 where id = $1::uuid`, n.ID, j.source, rec.SourceID, rec.SourceURL, rec.CreatedAt, rec.Item == nil); err != nil {
			return command.DB(err)
		}
		if rec.Item == nil {
			// Graveyard mode: unclaimed imported pages get archived by the docs module after 90 days.
			if _, err := c.Tx.Exec(c.Context, "insert into docs.freshness_flags (workspace_id, note_id, reason) values ($1::uuid,$2::uuid,'imported')",
				c.WorkspaceID(), n.ID); err != nil {
				return command.DB(err)
			}
		}
		p := map[string]any{"source_system": j.source, "source_url": rec.SourceURL, "author": rec.Author, "original_created_at": command.TsPtr(rec.CreatedAt)}
		if _, err := insertEvent(c, j, n.ID, "note.imported", rec.SourceID+":imported", rec.CreatedAt, p); err != nil {
			return err
		}
		report.Imported++
		noteID, created = n.ID, true
	}

	if rec.Item != nil {
		if err := upsertItem(c, j, noteID, created, rec.Item, report); err != nil {
			return err
		}
	}

	for _, h := range rec.History {
		kind := "item.updated"
		p := map[string]any{"field": h.Field, "from": h.From, "to": h.To}
		if h.Field == "status" {
			kind = "item.transitioned"
			p = map[string]any{"from": h.From, "to": h.To}
		}
		p["author"] = h.Author
		p["at"] = command.TsPtr(h.At)
		ok, err := insertEvent(c, j, noteID, kind, rec.SourceID+":h"+h.ID, h.At, p)
		if err != nil {
			return err
		}
		if ok {
			report.HistoryEvents++
		}
	}
	for _, cm := range rec.Comments {
		var authorID *string
		if cm.AuthorEmail != nil {
			if authorID, err = userByEmail(c, j, *cm.AuthorEmail, report); err != nil {
				return err
			}
		}
		var isNew bool
		if err := c.Tx.QueryRow(c.Context,
			`insert into core.comments (workspace_id, note_id, author_id, author_name, body, created_at, source_id)
			 values ($1::uuid,$2::uuid,$3::uuid,$4,$5,coalesce($6::timestamptz, now()),$7)
			 on conflict (note_id, source_id) where source_id is not null do update set body = excluded.body
			 returning (xmax = 0)`,
			c.WorkspaceID(), noteID, authorID, cm.Author, cm.Body, cm.At, cm.ID).Scan(&isNew); err != nil {
			return command.DB(err)
		}
		if isNew {
			report.Comments++
			p := map[string]any{"author": cm.Author, "at": command.TsPtr(cm.At)}
			if _, err := insertEvent(c, j, noteID, "comment.added", rec.SourceID+":c"+cm.ID, cm.At, p); err != nil {
				return err
			}
		}
	}
	for _, a := range rec.Attachments {
		// Files are fetched by a later job; storage_key holds the source URL until then.
		tag, err := c.Tx.Exec(c.Context,
			`insert into core.attachments (workspace_id, note_id, filename, content_type, size_bytes, storage_key)
			 select $1::uuid,$2::uuid,$3::text,$4::text,$5::bigint,$6::text
			 where not exists (select 1 from core.attachments where note_id = $2::uuid and storage_key = $6::text)`,
			c.WorkspaceID(), noteID, a.Filename, a.ContentType, a.Size, a.URL)
		if err != nil {
			return command.DB(err)
		}
		report.Attachments += uint32(tag.RowsAffected())
	}
	return nil
}

func upsertItem(c *command.Ctx, j *job, noteID string, created bool, it *ItemData, report *Report) error {
	ws := c.WorkspaceID()
	var assignee, reporter *string
	var err error
	if it.AssigneeEmail != nil {
		if assignee, err = userByEmail(c, j, *it.AssigneeEmail, report); err != nil {
			return err
		}
	}
	if it.ReporterEmail != nil {
		if reporter, err = userByEmail(c, j, *it.ReporterEmail, report); err != nil {
			return err
		}
	}
	var sprint, version, component *string
	if s := it.Sprint; s != nil {
		var found string
		err := c.Tx.QueryRow(c.Context, "select id::text from projects.sprints where space_id = $1::uuid and name = $2", j.spaceID, s.Name).Scan(&found)
		switch {
		case err == nil:
			if _, err := c.Tx.Exec(c.Context, "update projects.sprints set state = $2, start_date = $3::date, end_date = $4::date where id = $1::uuid",
				found, s.State, s.Start, s.End); err != nil {
				return command.DB(err)
			}
		case errors.Is(err, pgx.ErrNoRows):
			if err := c.Tx.QueryRow(c.Context,
				"insert into projects.sprints (workspace_id, space_id, name, state, start_date, end_date) values ($1::uuid,$2::uuid,$3,$4,$5::date,$6::date) returning id::text",
				ws, j.spaceID, s.Name, s.State, s.Start, s.End).Scan(&found); err != nil {
				return command.DB(err)
			}
		default:
			return command.DB(err)
		}
		sprint = &found
	}
	if v := it.FixVersion; v != nil {
		var id string
		if err := c.Tx.QueryRow(c.Context,
			`insert into projects.versions (workspace_id, space_id, name, released, release_date) values ($1::uuid,$2::uuid,$3,$4,$5::date)
			 on conflict (space_id, name) do update set released = excluded.released, release_date = excluded.release_date
			 returning id::text`, ws, j.spaceID, v.Name, v.Released, v.Date).Scan(&id); err != nil {
			return command.DB(err)
		}
		version = &id
	}
	if cn := it.Component; cn != nil {
		var id string
		if err := c.Tx.QueryRow(c.Context,
			`insert into projects.components (workspace_id, space_id, name) values ($1::uuid,$2::uuid,$3)
			 on conflict (space_id, name) do update set name = excluded.name returning id::text`, ws, j.spaceID, *cn).Scan(&id); err != nil {
			return command.DB(err)
		}
		component = &id
	}
	custom := marshal(it.Custom)
	if created {
		// Lock the space row to hand out the next number, then key = space key + number.
		var number int32
		var spaceKey string
		if err := c.Tx.QueryRow(c.Context, "update core.spaces set item_seq = item_seq + 1 where id = $1::uuid returning item_seq, key", j.spaceID).Scan(&number, &spaceKey); err != nil {
			return command.DB(err)
		}
		_, err := c.Tx.Exec(c.Context,
			`insert into projects.items (note_id, workspace_id, space_id, number, key, item_type, status, status_category, priority,
			   assignee_id, reporter_id, sprint_id, fix_version_id, component_id, estimate, due_date, labels, custom, resolved_at)
			 values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid,$11::uuid,$12::uuid,$13::uuid,$14::uuid,$15::float8,$16::date,$17::text[],$18::jsonb,$19::timestamptz)`,
			noteID, ws, j.spaceID, number, fmt.Sprintf("%s-%d", spaceKey, number), it.ItemType, it.Status, it.StatusCategory, it.Priority,
			assignee, reporter, sprint, version, component, it.Estimate, it.DueDate, it.Labels, custom, it.ResolvedAt)
		return command.DB(err)
	}
	_, err = c.Tx.Exec(c.Context,
		`update projects.items set item_type = $2, status = $3, status_category = $4, priority = $5, assignee_id = $6::uuid,
		   reporter_id = $7::uuid, sprint_id = $8::uuid, fix_version_id = $9::uuid, component_id = $10::uuid, estimate = $11::float8,
		   due_date = $12::date, labels = $13::text[], custom = $14::jsonb, resolved_at = $15::timestamptz
		 where note_id = $1::uuid`,
		noteID, it.ItemType, it.Status, it.StatusCategory, it.Priority, assignee, reporter, sprint, version, component,
		it.Estimate, it.DueDate, it.Labels, custom, it.ResolvedAt)
	return command.DB(err)
}

func relate(c *command.Ctx, from, to, kind string, report *Report) error {
	tag, err := c.Tx.Exec(c.Context,
		"insert into core.relations (workspace_id, from_id, to_id, kind) values ($1::uuid,$2::uuid,$3::uuid,$4) on conflict (from_id, to_id, kind) do nothing",
		c.WorkspaceID(), from, to, kind)
	if err != nil {
		return command.DB(err)
	}
	report.Relations += uint32(tag.RowsAffected())
	return nil
}

func link(c *command.Ctx, j *job, rec *Rec, report *Report) error {
	me, err := findNote(c, j, rec.SourceID)
	if err != nil || me == nil {
		return err
	}
	if rec.Parent != nil {
		p, err := findNote(c, j, *rec.Parent)
		if err != nil {
			return err
		}
		switch {
		case p != nil && *p != *me:
			if rec.Item == nil {
				if _, err := c.Tx.Exec(c.Context, "update core.notes set parent_id = $1::uuid where id = $2::uuid and parent_id is distinct from $1::uuid", *p, *me); err != nil {
					return command.DB(err)
				}
			}
			if err := relate(c, *p, *me, "child", report); err != nil {
				return err
			}
		case p == nil:
			report.warn(fmt.Sprintf("parent %s is not part of this import: link skipped", *rec.Parent))
		}
	}
	for _, l := range rec.Links {
		other, err := findNote(c, j, l.Other)
		if err != nil {
			return err
		}
		if other == nil {
			report.warn(fmt.Sprintf("link to %s skipped: not part of this import", l.Other))
			continue
		}
		from, to := *me, *other
		switch {
		case l.Kind == "relates":
			// Symmetric links appear on both issues; a fixed order lets the unique key dedupe them.
			from, to = min(*me, *other), max(*me, *other)
		case !l.Outward:
			from, to = *other, *me
		}
		if err := relate(c, from, to, l.Kind, report); err != nil {
			return err
		}
	}
	return nil
}

func extras(c *command.Ctx, j *job, parsed *Parsed, report *Report) error {
	ws := c.WorkspaceID()
	for _, f := range parsed.Filters {
		supported := jqlSupported(f.JQL)
		if !supported {
			report.warn(fmt.Sprintf("JQL not translatable for filter '%s': stored as text", f.Name))
		}
		options := marshal(map[string]any{"source_system": j.source, "source_id": f.ID, "jql_supported": supported})
		tag, err := c.Tx.Exec(c.Context,
			`update core.views set name = $3, query = $4, options = $5::jsonb
			 where space_id = $1::uuid and options->>'source_system' = $2 and options->>'source_id' = $6`,
			j.spaceID, j.source, f.Name, f.JQL, options, f.ID)
		if err != nil {
			return command.DB(err)
		}
		if tag.RowsAffected() == 0 {
			if _, err := c.Tx.Exec(c.Context,
				"insert into core.views (workspace_id, space_id, name, layout, query, options, created_by) values ($1::uuid,$2::uuid,$3,'table',$4,$5::jsonb,$6::uuid)",
				ws, j.spaceID, f.Name, f.JQL, options, c.Principal.UserID); err != nil {
				return command.DB(err)
			}
		}
		report.Views++
	}
	for _, a := range parsed.Automations {
		trigger, err := serde(map[string]any{"kind": "imported", "source": j.source, "original": a.Trigger})
		if err != nil {
			return err
		}
		conditions, err := serde(a.Conditions)
		if err != nil {
			return err
		}
		actions, err := serde(a.Actions)
		if err != nil {
			return err
		}
		tag, err := c.Tx.Exec(c.Context,
			`update projects.automations set trigger = $3::jsonb, conditions = $4::jsonb, actions = $5::jsonb
			 where space_id = $1::uuid and name = $2 and draft`, j.spaceID, a.Name, trigger, conditions, actions)
		if err != nil {
			return command.DB(err)
		}
		if tag.RowsAffected() == 0 {
			if _, err := c.Tx.Exec(c.Context,
				`insert into projects.automations (workspace_id, space_id, name, enabled, draft, trigger, conditions, actions, created_by)
				 values ($1::uuid,$2::uuid,$3,false,true,$4::jsonb,$5::jsonb,$6::jsonb,$7::uuid)`,
				ws, j.spaceID, a.Name, trigger, conditions, actions, c.Principal.UserID); err != nil {
				return command.DB(err)
			}
		}
		report.Automations++
		report.warn(fmt.Sprintf("automation rule '%s' imported as a disabled draft: review before enabling", a.Name))
	}
	return nil
}

// serde prints source JSON the way serde_json::Value does (sorted keys, f64 numbers re-printed).
func serde(v any) (string, error) {
	raw, err := command.Marshal(v)
	if err != nil {
		return "", command.ErrInvalid(err.Error())
	}
	n, err := notes.NormValue(raw)
	return string(n), err
}
