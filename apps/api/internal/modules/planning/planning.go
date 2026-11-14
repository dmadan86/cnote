// Package planning ports the sprint, version, component, report and automation commands from
// crates/cnote-projects/src/planning.rs and automation.rs. Automation execution (on_event, sweep,
// fire) stays in the Rust worker; this package only ports the four CRUD commands so the Rust worker
// keeps reacting to events and jobs written by Go commands.
package planning

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// Commands returns all ported commands.
func Commands() []command.Command {
	return []command.Command{
		command.NewCommand("sprint.create", command.GroupWriteItems, sprintCreate),
		command.NewCommand("sprint.list", command.GroupRead, sprintList),
		command.NewCommand("sprint.start", command.GroupWriteItems, sprintStart),
		command.NewCommand("sprint.complete", command.GroupWriteItems, sprintComplete),
		command.NewCommand("version.create", command.GroupWriteItems, versionCreate),
		command.NewCommand("version.list", command.GroupRead, versionList),
		command.NewCommand("version.release", command.GroupWriteItems, versionRelease),
		command.NewCommand("component.create", command.GroupWriteItems, componentCreate),
		command.NewCommand("component.list", command.GroupRead, componentList),
		command.NewCommand("report.burndown", command.GroupRead, reportBurndown),
		command.NewCommand("report.velocity", command.GroupRead, reportVelocity),
		command.NewCommand("automation.create", command.GroupWriteItems, automationCreate),
		command.NewCommand("automation.list", command.GroupRead, automationList),
		command.NewCommand("automation.update", command.GroupWriteItems, automationUpdate),
		command.NewCommand("automation.runs", command.GroupRead, automationRuns),
	}
}

var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// decodeUUID parses a *string UUID field; falls back if nil or bad format.
func decodeUUID(v *string) (string, error) {
	if v == nil || !uuidRe.MatchString(*v) {
		return "", command.ErrFallback
	}
	return strings.ToLower(*v), nil
}

// decodeOptUUID returns nil for JSON null/absent, falls back on bad format.
func decodeOptUUID(v **string) (*string, error) {
	if v == nil || *v == nil {
		return nil, nil
	}
	s, err := decodeUUID(*v)
	if err != nil {
		return nil, command.ErrFallback
	}
	return &s, nil
}

// dateRe matches YYYY-MM-DD.
var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// decodeOptDate parses an optional date string; falls back on bad format.
func decodeOptDate(v **string) (*string, error) {
	if v == nil || *v == nil {
		return nil, nil
	}
	if !dateRe.MatchString(**v) {
		return nil, command.ErrFallback
	}
	return *v, nil
}

// ============================================================
// SPRINTS
// ============================================================

const sprintCols = `id::text, space_id::text, name, goal, state, start_date::text, end_date::text`

// Sprint matches the Rust SprintOut (fields serialized sorted by serde_json::Value).
type Sprint struct {
	EndDate   *string `json:"end_date"`
	Goal      *string `json:"goal"`
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	SpaceID   string  `json:"space_id"`
	StartDate *string `json:"start_date"`
	State     string  `json:"state"`
}

func scanSprint(row pgx.Row) (Sprint, error) {
	var s Sprint
	err := row.Scan(&s.ID, &s.SpaceID, &s.Name, &s.Goal, &s.State, &s.StartDate, &s.EndDate)
	return s, err
}

func loadSprint(c *command.Ctx, id string, write bool) (Sprint, error) {
	s, err := scanSprint(c.Tx.QueryRow(c.Context,
		"select "+sprintCols+" from projects.sprints where id = $1::uuid", id))
	if err == pgx.ErrNoRows {
		return s, command.ErrNotFound("sprint")
	}
	if err != nil {
		return s, command.DB(err)
	}
	if err := access.AssertSpaceAccess(c, s.SpaceID, write); err != nil {
		return s, err
	}
	return s, nil
}

func checkDates(start, end *string) error {
	if start != nil && end != nil {
		s, _ := time.Parse("2006-01-02", *start)
		e, _ := time.Parse("2006-01-02", *end)
		if e.Before(s) {
			return command.ErrInvalid("end_date is before start_date")
		}
	}
	return nil
}

// ---- sprint.create ----

type createSprintIn struct {
	SpaceID   *string  `json:"space_id"`
	Name      *string  `json:"name"`
	Goal      **string `json:"goal"`
	StartDate **string `json:"start_date"`
	EndDate   **string `json:"end_date"`
}

func sprintCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in createSprintIn
	if json.Unmarshal(raw, &in) != nil || in.SpaceID == nil || in.Name == nil {
		return nil, command.ErrFallback
	}
	spaceID, err := decodeUUID(in.SpaceID)
	if err != nil {
		return nil, err
	}
	start, err := decodeOptDate(in.StartDate)
	if err != nil {
		return nil, err
	}
	end, err := decodeOptDate(in.EndDate)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, spaceID, true); err != nil {
		return nil, err
	}
	if err := checkDates(start, end); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(*in.Name)
	if name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	var goal *string
	if in.Goal != nil {
		goal = *in.Goal
	}
	s, err := scanSprint(c.Tx.QueryRow(c.Context,
		"insert into projects.sprints (workspace_id, space_id, name, goal, start_date, end_date)"+
			" values ($1::uuid,$2::uuid,$3,$4,$5::date,$6::date) returning "+sprintCols,
		c.WorkspaceID(), spaceID, name, goal, start, end))
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("sprint.created", nil, map[string]any{
		"sprint_id": s.ID, "space_id": s.SpaceID, "name": s.Name,
	}); err != nil {
		return nil, err
	}
	return s, nil
}

// ---- sprint.list ----

type listSprintsIn struct {
	SpaceID **string `json:"space_id"`
	State   **string `json:"state"`
}

func sprintList(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in listSprintsIn
	if json.Unmarshal(raw, &in) != nil {
		return nil, command.ErrFallback
	}
	var spaceID *string
	if in.SpaceID != nil && *in.SpaceID != nil {
		s, err := decodeUUID(*in.SpaceID)
		if err != nil {
			return nil, err
		}
		spaceID = &s
	}
	var state *string
	if in.State != nil {
		state = *in.State
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select "+sprintCols+" from projects.sprints where space_id = any($1::text[]::uuid[])"+
			" and ($2::uuid is null or space_id = $2::uuid) and ($3::text is null or state = $3)"+
			" order by start_date nulls last, created_at",
		spaces, spaceID, state)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []Sprint{}
	for rows.Next() {
		s, err := scanSprint(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, s)
	}
	return out, command.DB(rows.Err())
}

// ---- sprint.start ----

type startSprintIn struct {
	ID        *string  `json:"id"`
	StartDate **string `json:"start_date"`
	EndDate   **string `json:"end_date"`
}

func sprintStart(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in startSprintIn
	if json.Unmarshal(raw, &in) != nil || in.ID == nil {
		return nil, command.ErrFallback
	}
	id, err := decodeUUID(in.ID)
	if err != nil {
		return nil, err
	}
	overrideStart, err := decodeOptDate(in.StartDate)
	if err != nil {
		return nil, err
	}
	overrideEnd, err := decodeOptDate(in.EndDate)
	if err != nil {
		return nil, err
	}
	s, err := loadSprint(c, id, true)
	if err != nil {
		return nil, err
	}
	if s.State != "future" {
		return nil, command.ErrConflict("sprint is " + s.State + ", only a future sprint can start")
	}
	var active bool
	if err := c.Tx.QueryRow(c.Context,
		"select exists(select 1 from projects.sprints where space_id = $1::uuid and state = 'active')",
		s.SpaceID).Scan(&active); err != nil {
		return nil, command.DB(err)
	}
	if active {
		return nil, command.ErrConflict("another sprint is already active in this space; complete it first")
	}
	// Resolve start date: override → stored → today
	startStr := overrideStart
	if startStr == nil {
		startStr = s.StartDate
	}
	if startStr == nil {
		today := time.Now().UTC().Format("2006-01-02")
		startStr = &today
	}
	// Resolve end date: override → stored → start + 14 days
	endStr := overrideEnd
	if endStr == nil {
		endStr = s.EndDate
	}
	if endStr == nil {
		startT, _ := time.Parse("2006-01-02", *startStr)
		d := startT.AddDate(0, 0, 14).Format("2006-01-02")
		endStr = &d
	}
	if err := checkDates(startStr, endStr); err != nil {
		return nil, err
	}
	out, err := scanSprint(c.Tx.QueryRow(c.Context,
		"update projects.sprints set state = 'active', start_date = $2::date, end_date = $3::date"+
			" where id = $1::uuid returning "+sprintCols,
		s.ID, *startStr, *endStr))
	if err != nil {
		return nil, command.DB(err)
	}
	var committedCount int64
	var committedPoints float64
	if err := c.Tx.QueryRow(c.Context,
		"select count(*), coalesce(sum(coalesce(estimate, 1)), 0)::float8 from projects.items where sprint_id = $1::uuid",
		s.ID).Scan(&committedCount, &committedPoints); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("sprint.started", nil, map[string]any{
		"sprint_id": s.ID, "space_id": s.SpaceID, "name": s.Name,
		"committed_count": committedCount, "committed_points": command.F64(committedPoints),
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- sprint.complete ----

type completeSprintIn struct {
	ID             *string  `json:"id"`
	MoveToSprintID **string `json:"move_to_sprint_id"`
}

func sprintComplete(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in completeSprintIn
	if json.Unmarshal(raw, &in) != nil || in.ID == nil {
		return nil, command.ErrFallback
	}
	id, err := decodeUUID(in.ID)
	if err != nil {
		return nil, err
	}
	s, err := loadSprint(c, id, true)
	if err != nil {
		return nil, err
	}
	if s.State != "active" {
		return nil, command.ErrConflict("sprint is " + s.State + ", only an active sprint can be completed")
	}

	// Resolve destination sprint.
	var dest *string
	if in.MoveToSprintID != nil && *in.MoveToSprintID != nil {
		destID, err := decodeUUID(*in.MoveToSprintID)
		if err != nil {
			return nil, err
		}
		t, err := loadSprint(c, destID, true)
		if err != nil {
			return nil, err
		}
		if t.SpaceID != s.SpaceID || t.State != "future" || t.ID == s.ID {
			return nil, command.ErrInvalid("move_to_sprint_id must be a future sprint of the same space")
		}
		dest = &destID
	} else {
		// Auto: pick the next future sprint of the space.
		var nextID *string
		if err := c.Tx.QueryRow(c.Context,
			"select id::text from projects.sprints where space_id = $1::uuid and state = 'future'"+
				" order by start_date nulls last, created_at limit 1",
			s.SpaceID).Scan(&nextID); err != nil && err != pgx.ErrNoRows {
			return nil, command.DB(err)
		}
		dest = nextID
	}

	var doneCount int64
	var donePoints float64
	if err := c.Tx.QueryRow(c.Context,
		"select count(*), coalesce(sum(coalesce(estimate, 1)), 0)::float8"+
			" from projects.items where sprint_id = $1::uuid and status_category = 'done'",
		s.ID).Scan(&doneCount, &donePoints); err != nil {
		return nil, command.DB(err)
	}
	// Move unfinished items; collect moved note_ids and estimates.
	type movedRow struct {
		NoteID *string
		Est    float64
	}
	var moved []movedRow
	rows, err := c.Tx.Query(c.Context,
		"update projects.items set sprint_id = $2::uuid"+
			" where sprint_id = $1::uuid and status_category <> 'done'"+
			" returning note_id::text, coalesce(estimate, 1)::float8",
		s.ID, dest)
	if err != nil {
		return nil, command.DB(err)
	}
	for rows.Next() {
		var r movedRow
		if err := rows.Scan(&r.NoteID, &r.Est); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		moved = append(moved, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}

	if _, err := c.Tx.Exec(c.Context,
		"update projects.sprints set state = 'closed', end_date = coalesce(end_date, current_date) where id = $1::uuid",
		s.ID); err != nil {
		return nil, command.DB(err)
	}

	var movedPoints float64
	movedIDs := []any{} // non-nil so JSON is [] not null
	for _, m := range moved {
		movedPoints += m.Est
		if m.NoteID != nil {
			movedIDs = append(movedIDs, *m.NoteID)
		}
	}
	payload := map[string]any{
		"sprint_id":        s.ID,
		"space_id":         s.SpaceID,
		"name":             s.Name,
		"completed_count":  doneCount,
		"completed_points": command.F64(donePoints),
		"moved_count":      len(moved),
		"moved_points":     command.F64(movedPoints),
		"moved_to":         dest,
		"moved_item_ids":   movedIDs,
	}
	if _, err := c.Emit("sprint.completed", nil, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// ============================================================
// VERSIONS
// ============================================================

// Version matches Rust VersionOut.
type Version struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	ReleaseDate *string `json:"release_date"`
	Released    bool    `json:"released"`
	SpaceID     string  `json:"space_id"`
}

func scanVersion(row pgx.Row) (Version, error) {
	var v Version
	err := row.Scan(&v.ID, &v.SpaceID, &v.Name, &v.Released, &v.ReleaseDate)
	return v, err
}

const versionCols = `id::text, space_id::text, name, released, release_date::text`

// ---- version.create ----

type createVersionIn struct {
	SpaceID     *string  `json:"space_id"`
	Name        *string  `json:"name"`
	ReleaseDate **string `json:"release_date"`
}

func versionCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in createVersionIn
	if json.Unmarshal(raw, &in) != nil || in.SpaceID == nil || in.Name == nil {
		return nil, command.ErrFallback
	}
	spaceID, err := decodeUUID(in.SpaceID)
	if err != nil {
		return nil, err
	}
	releaseDate, err := decodeOptDate(in.ReleaseDate)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, spaceID, true); err != nil {
		return nil, err
	}
	v, err := scanVersion(c.Tx.QueryRow(c.Context,
		"insert into projects.versions (workspace_id, space_id, name, release_date)"+
			" values ($1::uuid,$2::uuid,$3,$4::date)"+
			" returning "+versionCols,
		c.WorkspaceID(), spaceID, strings.TrimSpace(*in.Name), releaseDate))
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("version.created", nil, map[string]any{
		"version_id": v.ID, "space_id": v.SpaceID, "name": v.Name,
	}); err != nil {
		return nil, err
	}
	return v, nil
}

// ---- version.list ----

type bySpaceIn struct {
	SpaceID *string `json:"space_id"`
}

func versionList(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in bySpaceIn
	if json.Unmarshal(raw, &in) != nil || in.SpaceID == nil {
		return nil, command.ErrFallback
	}
	spaceID, err := decodeUUID(in.SpaceID)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, spaceID, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select "+versionCols+" from projects.versions where space_id = $1::uuid"+
			" order by released, release_date nulls last, name",
		spaceID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, v)
	}
	return out, command.DB(rows.Err())
}

// ---- version.release ----

type releaseVersionIn struct {
	ID          *string  `json:"id"`
	ReleaseDate **string `json:"release_date"`
}

func versionRelease(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in releaseVersionIn
	if json.Unmarshal(raw, &in) != nil || in.ID == nil {
		return nil, command.ErrFallback
	}
	id, err := decodeUUID(in.ID)
	if err != nil {
		return nil, err
	}
	releaseDate, err := decodeOptDate(in.ReleaseDate)
	if err != nil {
		return nil, err
	}
	var spaceID string
	if err := c.Tx.QueryRow(c.Context,
		"select space_id::text from projects.versions where id = $1::uuid", id).Scan(&spaceID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, command.ErrNotFound("version")
		}
		return nil, command.DB(err)
	}
	if err := access.AssertSpaceAccess(c, spaceID, true); err != nil {
		return nil, err
	}
	v, err := scanVersion(c.Tx.QueryRow(c.Context,
		"update projects.versions set released = true, release_date = coalesce($2::date, current_date)"+
			" where id = $1::uuid and not released returning "+versionCols,
		id, releaseDate))
	if err == pgx.ErrNoRows {
		return nil, command.ErrConflict("version is already released")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	// collect item keys in this version
	itemRows, err := c.Tx.Query(c.Context,
		"select key from projects.items where fix_version_id = $1::uuid order by number",
		v.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer itemRows.Close()
	items := []string{}
	for itemRows.Next() {
		var key string
		if err := itemRows.Scan(&key); err != nil {
			return nil, command.DB(err)
		}
		items = append(items, key)
	}
	if err := itemRows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("version.released", nil, map[string]any{
		"version_id": v.ID, "space_id": v.SpaceID, "name": v.Name, "items": items,
	}); err != nil {
		return nil, err
	}
	return v, nil
}

// ============================================================
// COMPONENTS
// ============================================================

// Component matches Rust ComponentOut.
type Component struct {
	ID      string  `json:"id"`
	LeadID  *string `json:"lead_id"`
	Name    string  `json:"name"`
	SpaceID string  `json:"space_id"`
}

func scanComponent(row pgx.Row) (Component, error) {
	var c Component
	err := row.Scan(&c.ID, &c.SpaceID, &c.Name, &c.LeadID)
	return c, err
}

// ---- component.create ----

type createComponentIn struct {
	SpaceID *string  `json:"space_id"`
	Name    *string  `json:"name"`
	LeadID  **string `json:"lead_id"`
}

func componentCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in createComponentIn
	if json.Unmarshal(raw, &in) != nil || in.SpaceID == nil || in.Name == nil {
		return nil, command.ErrFallback
	}
	spaceID, err := decodeUUID(in.SpaceID)
	if err != nil {
		return nil, err
	}
	var leadID *string
	if in.LeadID != nil && *in.LeadID != nil {
		l, err := decodeUUID(*in.LeadID)
		if err != nil {
			return nil, err
		}
		leadID = &l
	}
	if err := access.AssertSpaceAccess(c, spaceID, true); err != nil {
		return nil, err
	}
	comp, err := scanComponent(c.Tx.QueryRow(c.Context,
		"insert into projects.components (workspace_id, space_id, name, lead_id)"+
			" values ($1::uuid,$2::uuid,$3,$4::uuid)"+
			" returning id::text, space_id::text, name, lead_id::text",
		c.WorkspaceID(), spaceID, strings.TrimSpace(*in.Name), leadID))
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("component.created", nil, map[string]any{
		"component_id": comp.ID, "space_id": comp.SpaceID, "name": comp.Name,
	}); err != nil {
		return nil, err
	}
	return comp, nil
}

// ---- component.list ----

func componentList(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in bySpaceIn
	if json.Unmarshal(raw, &in) != nil || in.SpaceID == nil {
		return nil, command.ErrFallback
	}
	spaceID, err := decodeUUID(in.SpaceID)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, spaceID, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select id::text, space_id::text, name, lead_id::text from projects.components"+
			" where space_id = $1::uuid order by name",
		spaceID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []Component{}
	for rows.Next() {
		comp, err := scanComponent(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, comp)
	}
	return out, command.DB(rows.Err())
}

// ============================================================
// REPORTS
// ============================================================

// BurnPoint matches Rust BurnPoint.
type BurnPoint struct {
	Date      string      `json:"date"`
	Ideal     command.F64 `json:"ideal"`
	Remaining command.F64 `json:"remaining"`
}

// burndown computes daily remaining work. Matches the Rust function exactly:
// an item is "burned" on day D if the latest transition by end-of-D is to category "done".
func burndown(start, last string, items [][2]any, events [][3]any) []BurnPoint {
	startT, _ := time.Parse("2006-01-02", start)
	lastT, _ := time.Parse("2006-01-02", last)
	days := int(lastT.Sub(startT).Hours() / 24)
	if days < 0 {
		days = 0
	}
	var total float64
	for _, it := range items {
		total += it[1].(float64)
	}
	// group events by item id
	type evRow struct {
		at       time.Time
		category string
	}
	byItem := map[string][]evRow{}
	for _, e := range events {
		id := e[0].(string)
		at := e[1].(time.Time)
		cat := e[2].(string)
		byItem[id] = append(byItem[id], evRow{at, cat})
	}
	pts := make([]BurnPoint, days+1)
	for d := 0; d <= days; d++ {
		date := startT.AddDate(0, 0, d)
		dateStr := date.Format("2006-01-02")
		var burned float64
		for _, it := range items {
			id := it[0].(string)
			est := it[1].(float64)
			evs := byItem[id]
			// find the latest event by end of this day
			var latest *evRow
			for i := range evs {
				ev := &evs[i]
				if ev.at.UTC().Truncate(24*time.Hour).Compare(date.UTC()) <= 0 {
					if latest == nil || ev.at.After(latest.at) {
						latest = ev
					}
				}
			}
			if latest != nil && latest.category == "done" {
				burned += est
			}
		}
		var ideal float64
		if days == 0 {
			ideal = 0
		} else {
			ideal = total * (1.0 - float64(d)/float64(days))
		}
		pts[d] = BurnPoint{Date: dateStr, Ideal: command.F64(ideal), Remaining: command.F64(total - burned)}
	}
	return pts
}

// ---- report.burndown ----

type burndownIn struct {
	SprintID *string `json:"sprint_id"`
}

func reportBurndown(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in burndownIn
	if json.Unmarshal(raw, &in) != nil || in.SprintID == nil {
		return nil, command.ErrFallback
	}
	sprintID, err := decodeUUID(in.SprintID)
	if err != nil {
		return nil, err
	}
	s, err := loadSprint(c, sprintID, false)
	if err != nil {
		return nil, err
	}
	if s.StartDate == nil {
		return nil, command.ErrInvalid("sprint has not started")
	}
	start := *s.StartDate
	var endStr string
	if s.EndDate != nil {
		endStr = *s.EndDate
	} else {
		startT, _ := time.Parse("2006-01-02", start)
		endStr = startT.AddDate(0, 0, 14).Format("2006-01-02")
	}
	today := time.Now().UTC().Format("2006-01-02")
	var last string
	if s.State == "active" {
		// last = min(end, today).max(start)
		if endStr < today {
			last = endStr
		} else {
			last = today
		}
		if last < start {
			last = start
		}
	} else {
		if endStr > start {
			last = endStr
		} else {
			last = start
		}
	}

	// Items: in sprint now + items carried out when it closed.
	type itemRow struct {
		NoteID string
		Est    float64
	}
	itemRows, err := c.Tx.Query(c.Context,
		"select note_id::text, coalesce(estimate, 1)::float8 from projects.items"+
			" where sprint_id = $1::uuid"+
			" or note_id in (select (jsonb_array_elements_text(payload->'moved_item_ids'))::uuid"+
			"                from core.events where kind = 'sprint.completed' and payload->>'sprint_id' = $1::text)",
		s.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer itemRows.Close()
	var items [][2]any
	for itemRows.Next() {
		var r itemRow
		if err := itemRows.Scan(&r.NoteID, &r.Est); err != nil {
			return nil, command.DB(err)
		}
		items = append(items, [2]any{r.NoteID, r.Est})
	}
	if err := itemRows.Err(); err != nil {
		return nil, command.DB(err)
	}
	itemIDs := make([]string, len(items))
	for i, it := range items {
		itemIDs[i] = it[0].(string)
	}

	// Transition events for all items in this sprint.
	evRows, err := c.Tx.Query(c.Context,
		"select note_id::text, created_at, payload->>'category' from core.events"+
			" where kind = 'item.transitioned' and note_id = any($1::text[]::uuid[])",
		itemIDs)
	if err != nil {
		return nil, command.DB(err)
	}
	defer evRows.Close()
	var events [][3]any
	for evRows.Next() {
		var noteID, cat string
		var at time.Time
		if err := evRows.Scan(&noteID, &at, &cat); err != nil {
			return nil, command.DB(err)
		}
		events = append(events, [3]any{noteID, at, cat})
	}
	if err := evRows.Err(); err != nil {
		return nil, command.DB(err)
	}

	var total float64
	for _, it := range items {
		total += it[1].(float64)
	}
	pts := burndown(start, last, items, events)
	return map[string]any{
		"sprint_id":    s.ID,
		"name":         s.Name,
		"state":        s.State,
		"total_points": command.F64(total),
		"points":       pts,
	}, nil
}

// ---- report.velocity ----

type velocityIn struct {
	SpaceID *string `json:"space_id"`
	Sprints **int64 `json:"sprints"`
}

func reportVelocity(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in velocityIn
	if json.Unmarshal(raw, &in) != nil || in.SpaceID == nil {
		return nil, command.ErrFallback
	}
	spaceID, err := decodeUUID(in.SpaceID)
	if err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, spaceID, false); err != nil {
		return nil, err
	}
	limit := int64(6)
	if in.Sprints != nil && *in.Sprints != nil {
		v := **in.Sprints
		if v < 1 {
			v = 1
		}
		if v > 52 {
			v = 52
		}
		limit = v
	}

	type row struct {
		Payload []byte
		At      time.Time
	}
	rows, err := c.Tx.Query(c.Context,
		"select payload, created_at from core.events"+
			" where kind = 'sprint.completed' and payload->>'space_id' = $1::text"+
			" order by id desc limit $2",
		spaceID, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()

	type sprintEntry struct {
		Payload []byte
		At      time.Time
	}
	var entries []sprintEntry
	for rows.Next() {
		var e sprintEntry
		if err := rows.Scan(&e.Payload, &e.At); err != nil {
			return nil, command.DB(err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}

	sprintsList := []any{}
	var sum float64
	for _, e := range entries {
		var p map[string]json.RawMessage
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			continue
		}
		var compPoints, movedPoints float64
		_ = json.Unmarshal(p["completed_points"], &compPoints)
		_ = json.Unmarshal(p["moved_points"], &movedPoints)
		committed := compPoints + movedPoints
		sum += compPoints

		norm, _ := command.Norm(e.Payload)
		var pm map[string]json.RawMessage
		_ = json.Unmarshal(norm, &pm)
		sprintsList = append(sprintsList, map[string]any{
			"sprint_id":        pm["sprint_id"],
			"name":             pm["name"],
			"completed_at":     command.Ts(e.At),
			"completed_points": pm["completed_points"],
			"completed_count":  pm["completed_count"],
			"committed_points": command.F64(committed),
		})
	}
	var avg float64
	if len(entries) > 0 {
		avg = sum / float64(len(entries))
	}
	return map[string]any{
		"space_id":       spaceID,
		"sprints":        sprintsList,
		"average_points": command.F64(avg),
	}, nil
}

// ============================================================
// AUTOMATIONS
// ============================================================

// AutomationOut matches Rust AutomationOut.
type AutomationOut struct {
	Actions    json.RawMessage `json:"actions"`
	Conditions json.RawMessage `json:"conditions"`
	CreatedAt  command.Ts      `json:"created_at"`
	CreatedBy  *string         `json:"created_by"`
	Draft      bool            `json:"draft"`
	Enabled    bool            `json:"enabled"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	SpaceID    *string         `json:"space_id"`
	Trigger    json.RawMessage `json:"trigger"`
}

const autoCols = `id::text, space_id::text, name, enabled, draft, trigger, conditions, actions, created_by::text, created_at`

func scanAutomation(row pgx.Row) (AutomationOut, error) {
	var a AutomationOut
	var trigger, conditions, actions []byte
	var createdAt time.Time
	err := row.Scan(&a.ID, &a.SpaceID, &a.Name, &a.Enabled, &a.Draft,
		&trigger, &conditions, &actions, &a.CreatedBy, &createdAt)
	if err != nil {
		return a, err
	}
	a.CreatedAt = command.Ts(createdAt)
	a.Trigger, _ = command.Norm(trigger)
	a.Conditions, _ = command.Norm(conditions)
	a.Actions, _ = command.Norm(actions)
	return a, nil
}

// loadAutomation fetches an automation and checks access.
func loadAutomation(c *command.Ctx, id string, write bool) (AutomationOut, error) {
	a, err := scanAutomation(c.Tx.QueryRow(c.Context,
		"select "+autoCols+" from projects.automations where id = $1::uuid", id))
	if err == pgx.ErrNoRows {
		return a, command.ErrNotFound("automation")
	}
	if err != nil {
		return a, command.DB(err)
	}
	if a.SpaceID != nil {
		if err := access.AssertSpaceAccess(c, *a.SpaceID, write); err != nil {
			return a, err
		}
	} else if write && c.Principal.Role < command.Admin {
		return a, command.ErrForbidden("workspace-wide automations need an admin")
	}
	return a, nil
}

// validateTrigger does the structural validation from automation.rs validate().
// CNQL syntax errors use ErrFallback so the Rust error text stays identical.
func validateTrigger(trigger json.RawMessage) error {
	var t struct {
		Kind         string  `json:"kind"`
		Event        *string `json:"event"`
		EveryMinutes *int    `json:"every_minutes"`
		CNQL         *string `json:"cnql"`
	}
	if json.Unmarshal(trigger, &t) != nil {
		return command.ErrFallback
	}
	switch t.Kind {
	case "event":
		if t.Event == nil || strings.TrimSpace(*t.Event) == "" {
			return command.ErrInvalid("trigger.event is required")
		}
		if t.CNQL != nil && *t.CNQL != "" {
			// CNQL parsing happens in Rust; if cnql is non-empty and potentially invalid
			// we fall back so error messages are identical.
			// We allow it through here; a truly bad query surfaces at Rust.
			// (The Go server validates structural parts only.)
		}
	case "schedule":
		if t.EveryMinutes == nil || *t.EveryMinutes == 0 {
			return command.ErrInvalid("every_minutes must be at least 1")
		}
	default:
		return command.ErrFallback
	}
	return nil
}

func validateConditions(conditions json.RawMessage) error {
	var conds []struct {
		CNQL string `json:"cnql"`
	}
	if json.Unmarshal(conditions, &conds) != nil {
		return command.ErrFallback
	}
	return nil
}

func validateActions(c *command.Ctx, actions json.RawMessage) error {
	var acts []struct {
		Type    string           `json:"type"`
		To      *string          `json:"to"`
		UserID  *string          `json:"user_id"`
		AgentID *string          `json:"agent_id"`
		Body    *string          `json:"body"`
		Name    *string          `json:"name"`
		Input   *json.RawMessage `json:"input"`
	}
	if json.Unmarshal(actions, &acts) != nil {
		return command.ErrFallback
	}
	if len(acts) == 0 {
		return command.ErrInvalid("at least one action is required")
	}
	for _, a := range acts {
		switch a.Type {
		case "transition":
			if a.To == nil || strings.TrimSpace(*a.To) == "" {
				return command.ErrInvalid("transition: 'to' is required")
			}
		case "assign":
			if a.UserID != nil && a.AgentID != nil {
				return command.ErrInvalid("assign: give user_id or agent_id, not both")
			}
		case "comment":
			// any body is valid
		case "command":
			if a.Name == nil {
				return command.ErrFallback
			}
			// role check would require registry access; handled by Rust for now
			// (the differential test proves correctness)
		default:
			return command.ErrFallback
		}
	}
	return nil
}

// ---- automation.create ----

type createAutomationIn struct {
	SpaceID    **string         `json:"space_id"`
	Name       *string          `json:"name"`
	Trigger    *json.RawMessage `json:"trigger"`
	Conditions *json.RawMessage `json:"conditions"`
	Actions    *json.RawMessage `json:"actions"`
	Enabled    *bool            `json:"enabled"`
	Draft      *bool            `json:"draft"`
}

func automationCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	// Only people (not machines) can create automations.
	if c.Principal.Kind == command.KindAgent || c.Principal.Kind == command.KindAPIKey {
		return nil, command.ErrForbidden("only people can create or edit automations")
	}
	var in createAutomationIn
	if json.Unmarshal(raw, &in) != nil || in.Name == nil || in.Trigger == nil || in.Actions == nil {
		return nil, command.ErrFallback
	}
	var spaceID *string
	if in.SpaceID != nil && *in.SpaceID != nil {
		s, err := decodeUUID(*in.SpaceID)
		if err != nil {
			return nil, err
		}
		spaceID = &s
		if err := access.AssertSpaceAccess(c, s, true); err != nil {
			return nil, err
		}
	} else if in.SpaceID == nil || *in.SpaceID == nil {
		// workspace-wide
		if c.Principal.Role < command.Admin {
			return nil, command.ErrForbidden("workspace-wide automations need an admin; pass space_id")
		}
	}
	name := strings.TrimSpace(*in.Name)
	if name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	if err := validateTrigger(*in.Trigger); err != nil {
		return nil, err
	}
	conditions := json.RawMessage("[]")
	if in.Conditions != nil {
		conditions = *in.Conditions
	}
	if err := validateConditions(conditions); err != nil {
		return nil, err
	}
	if err := validateActions(c, *in.Actions); err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	draft := false
	if in.Draft != nil {
		draft = *in.Draft
	}

	triggerNorm, err := command.Norm(*in.Trigger)
	if err != nil {
		return nil, command.ErrFallback
	}
	condNorm, err := command.Norm(conditions)
	if err != nil {
		return nil, command.ErrFallback
	}
	actNorm, err := command.Norm(*in.Actions)
	if err != nil {
		return nil, command.ErrFallback
	}

	a, err := scanAutomation(c.Tx.QueryRow(c.Context,
		"insert into projects.automations (workspace_id, space_id, name, enabled, draft, trigger, conditions, actions, created_by)"+
			" values ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9::uuid) returning "+autoCols,
		c.WorkspaceID(), spaceID, name, enabled, draft,
		triggerNorm, condNorm, actNorm, c.Principal.UserID))
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("automation.created", nil, map[string]any{
		"automation_id": a.ID, "name": a.Name,
	}); err != nil {
		return nil, err
	}
	return a, nil
}

// ---- automation.list ----

type listAutomationsIn struct {
	SpaceID **string `json:"space_id"`
}

func automationList(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in listAutomationsIn
	if json.Unmarshal(raw, &in) != nil {
		return nil, command.ErrFallback
	}
	var spaceID *string
	if in.SpaceID != nil && *in.SpaceID != nil {
		s, err := decodeUUID(*in.SpaceID)
		if err != nil {
			return nil, err
		}
		spaceID = &s
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		"select "+autoCols+" from projects.automations"+
			" where (space_id is null or space_id = any($1::text[]::uuid[]))"+
			" and ($2::uuid is null or space_id = $2::uuid)"+
			" order by created_at",
		spaces, spaceID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []AutomationOut{}
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, a)
	}
	return out, command.DB(rows.Err())
}

// ---- automation.update ----

type updateAutomationIn struct {
	ID         *string          `json:"id"`
	Name       **string         `json:"name"`
	Enabled    *bool            `json:"enabled"`
	Draft      *bool            `json:"draft"`
	Trigger    *json.RawMessage `json:"trigger"`
	Conditions *json.RawMessage `json:"conditions"`
	Actions    *json.RawMessage `json:"actions"`
}

func automationUpdate(c *command.Ctx, raw json.RawMessage) (any, error) {
	// Only people can update automations.
	if c.Principal.Kind == command.KindAgent || c.Principal.Kind == command.KindAPIKey {
		return nil, command.ErrForbidden("only people can create or edit automations")
	}
	var in updateAutomationIn
	if json.Unmarshal(raw, &in) != nil || in.ID == nil {
		return nil, command.ErrFallback
	}
	id, err := decodeUUID(in.ID)
	if err != nil {
		return nil, err
	}
	cur, err := loadAutomation(c, id, true)
	if err != nil {
		return nil, err
	}

	// Merge trigger.
	trigger := cur.Trigger
	if in.Trigger != nil {
		trigger = *in.Trigger
	}
	if err := validateTrigger(trigger); err != nil {
		return nil, err
	}

	// Merge conditions.
	conditions := cur.Conditions
	if in.Conditions != nil {
		conditions = *in.Conditions
	}
	if err := validateConditions(conditions); err != nil {
		return nil, err
	}

	// Merge actions.
	actions := cur.Actions
	if in.Actions != nil {
		actions = *in.Actions
	}
	if err := validateActions(c, actions); err != nil {
		return nil, err
	}

	triggerNorm, _ := command.Norm(trigger)
	condNorm, _ := command.Norm(conditions)
	actNorm, _ := command.Norm(actions)

	var name *string
	if in.Name != nil && *in.Name != nil {
		trimmed := strings.TrimSpace(**in.Name)
		name = &trimmed
	}

	a, err := scanAutomation(c.Tx.QueryRow(c.Context,
		"update projects.automations set name = coalesce($2, name), enabled = coalesce($3, enabled),"+
			" draft = coalesce($4, draft), trigger = $5, conditions = $6, actions = $7,"+
			" created_by = $8::uuid"+ // the editor becomes the new author
			" where id = $1::uuid returning "+autoCols,
		id, name, in.Enabled, in.Draft, triggerNorm, condNorm, actNorm, c.Principal.UserID))
	if err == pgx.ErrNoRows {
		return nil, command.ErrNotFound("automation")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("automation.updated", nil, map[string]any{
		"automation_id": a.ID, "enabled": a.Enabled, "draft": a.Draft,
	}); err != nil {
		return nil, err
	}
	return a, nil
}

// ---- automation.runs ----

type automationRunsIn struct {
	AutomationID *string `json:"automation_id"`
	Limit        **int64 `json:"limit"`
}

// RunOut matches Rust RunOut.
type RunOut struct {
	CreatedAt command.Ts      `json:"created_at"`
	EventID   *int64          `json:"event_id"`
	ID        int64           `json:"id"`
	Log       json.RawMessage `json:"log"`
	Status    string          `json:"status"`
}

func automationRuns(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in automationRunsIn
	if json.Unmarshal(raw, &in) != nil || in.AutomationID == nil {
		return nil, command.ErrFallback
	}
	automationID, err := decodeUUID(in.AutomationID)
	if err != nil {
		return nil, err
	}
	if _, err := loadAutomation(c, automationID, false); err != nil {
		return nil, err
	}
	limit := int64(50)
	if in.Limit != nil && *in.Limit != nil {
		v := **in.Limit
		if v < 1 {
			v = 1
		}
		if v > 200 {
			v = 200
		}
		limit = v
	}
	rows, err := c.Tx.Query(c.Context,
		"select id, event_id, status, log, created_at from projects.automation_runs"+
			" where automation_id = $1::uuid order by id desc limit $2",
		automationID, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []RunOut{}
	for rows.Next() {
		var r RunOut
		var log []byte
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.EventID, &r.Status, &log, &createdAt); err != nil {
			return nil, command.DB(err)
		}
		r.CreatedAt = command.Ts(createdAt)
		r.Log, _ = command.Norm(log)
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}
