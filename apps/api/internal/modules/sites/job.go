package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
)

const (
	jobKind      = "site.chat"
	maxCalls     = 4     // model calls per turn, across attempts
	maxTokens    = 60000 // tokens in plus out per turn, across attempts
	stepReserve  = 8000  // the provider's default max_tokens, reserved on top of the request estimate
	sweepAfter   = 5 * time.Minute
	noAccessMsg  = "You no longer have access to this website"
	pageMovedMsg = "The page changed while I was working. Try again."
)

var (
	errFenced      = errors.New("sites: turn finished or taken over by another attempt")
	errNotVisible  = errors.New("site turn not found")
	errTurnLimit   = errors.New("That request was too big for one step. Try a smaller change.")
	errUnavailable = errors.New("The AI service is unavailable. Try again in a minute.")
)

// failErr ends the turn with a message a person can read; anything else is retried (provider trouble, database).
type failErr struct{ msg string }

func (e *failErr) Error() string { return e.msg }

// RunJob is the long-lane handler of "site.chat" jobs. The model is reached through ai.step.
func RunJob(pool *pgxpool.Pool, reg *command.Registry) func(context.Context, jobs.Job) error {
	return func(ctx context.Context, j jobs.Job) error {
		var p struct {
			TurnID string `json:"turn_id"`
		}
		if err := json.Unmarshal(j.Payload, &p); err != nil || p.TurnID == "" || j.WorkspaceID == nil {
			return nil // a malformed job can never succeed
		}
		e := &turnExec{pool: pool, reg: reg, job: j, ws: *j.WorkspaceID, turnID: p.TurnID}
		return e.execute(ctx)
	}
}

type turnExec struct {
	pool   *pgxpool.Pool
	reg    *command.Registry
	job    jobs.Job
	ws     string
	turnID string

	principal command.Principal
	siteID    string
	base      string
	tier      string
	attempt   int32 // the fence: every write below is conditioned on it
	calls     int   // model calls so far, across attempts
	tokens    int64
	retry     error // set when the model step failed for a reason worth another attempt
	fatal     error // set when it failed for a reason that will not change
	fenced    bool
	kind      string // "page" or "post"
	postID    *string
	postIn    PostInput
}

func (e *turnExec) tx(ctx context.Context, fn func(c *command.Ctx) error) error {
	return e.reg.WithTx(ctx, e.pool, e.principal, fn)
}

func (e *turnExec) execute(ctx context.Context) error {
	data, in, done, err := e.start(ctx)
	if err != nil || done {
		return err
	}
	if e.kind == "post" {
		return e.runPost(ctx)
	}
	if e.kind == "translate" {
		return e.runTranslate(ctx, data)
	}
	begun := time.Now()
	res := RunTurn(ctx, e.step, data, in)
	if res.Err == "" {
		err = e.commit(ctx, res)
	} else {
		err = e.failed(ctx, res, begun, in.Tier)
	}
	if errors.Is(err, errFenced) {
		return nil
	}
	return err
}

// start claims the turn (bumping its attempt) and loads what the loop needs. done: nothing left to run.
func (e *turnExec) start(ctx context.Context) (data json.RawMessage, in TurnInput, done bool, err error) {
	sys := command.Principal{Kind: command.KindSystem, WorkspaceID: e.ws}
	err = e.reg.WithTx(ctx, e.pool, sys, func(sc *command.Ctx) error {
		var user, status, message string
		var attached []string
		var block, page *string
		var created time.Time
		err := sc.Tx.QueryRow(ctx, `select site_id::text, created_by::text, message, block_id, tier, status, base_version_id::text, calls, tokens_in + tokens_out, created_at, image_ids::text[], page, kind, post_id::text
			from sites.site_turns where workspace_id = $1::uuid and id = $2::uuid for update`, e.ws, e.turnID).
			Scan(&e.siteID, &user, &message, &block, &e.tier, &status, &e.base, &e.calls, &e.tokens, &created, &attached, &page, &e.kind, &e.postID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotVisible
		}
		if err != nil {
			return command.DB(err)
		}
		if status != "queued" && status != "running" {
			done = true
			return nil
		}
		var role string
		err = sc.Tx.QueryRow(ctx, "select role from core.memberships where workspace_id = $1::uuid and user_id = $2::uuid and deactivated_at is null", e.ws, user).Scan(&role)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return command.DB(err)
		}
		if err != nil || command.ParseRole(role) < command.Member {
			done = true
			_, err = sc.Tx.Exec(ctx, `update sites.site_turns set status = 'failed', error = $3, finished_at = now(), updated_at = now()
				where workspace_id = $1::uuid and id = $2::uuid`, e.ws, e.turnID, noAccessMsg)
			return command.DB(err)
		}
		if err := sc.Tx.QueryRow(ctx, `update sites.site_turns set status = 'running', attempt = attempt + 1, steps = '[]', updated_at = now()
			where workspace_id = $1::uuid and id = $2::uuid returning attempt`, e.ws, e.turnID).Scan(&e.attempt); err != nil {
			return command.DB(err)
		}
		e.principal = command.Principal{Kind: command.KindSystem, UserID: &user, WorkspaceID: e.ws, Role: command.ParseRole(role)}
		c := &command.Ctx{Context: ctx, Tx: sc.Tx, Pool: sc.Pool, Principal: e.principal}
		e.reg.Bind(c)
		var d []byte
		if err := sc.Tx.QueryRow(ctx, "select data from sites.site_versions where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid", e.ws, e.siteID, e.base).Scan(&d); err != nil {
			return command.DB(err)
		}
		data = d
		b, err := business(c)
		if err != nil {
			return err
		}
		in = TurnInput{Message: message, Tier: e.tier, Business: map[string]string{"name": b["name"], "phone": b["phone"], "email": b["email"], "address": b["address"]}}
		if block != nil {
			in.BlockID = *block
		}
		if page != nil {
			in.Page = *page
		}
		imgs, err := c.Call("file.image_list", props{"site_id": e.siteID})
		if err != nil {
			return err
		}
		var list []struct{ ID, Filename string }
		if err := json.Unmarshal(imgs, &list); err != nil {
			return err
		}
		for _, im := range list {
			name := im.Filename
			if slices.Contains(attached, im.ID) {
				name += " (attached now)"
			}
			in.Images = append(in.Images, Image{ID: im.ID, Name: name})
		}
		rows, err := sc.Tx.Query(ctx, `select message, reply from sites.site_turns where workspace_id = $1::uuid and site_id = $2::uuid and status = 'done'
			and coalesce(reply, '') <> '' and created_at < $3 order by created_at desc limit 6`, e.ws, e.siteID, created)
		if err != nil {
			return command.DB(err)
		}
		hist, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (h [2]string, err error) { err = r.Scan(&h[0], &h[1]); return })
		for i := len(hist) - 1; i >= 0; i-- {
			in.History = append(in.History, hist[i])
		}
		if err != nil {
			return command.DB(err)
		}
		if e.kind == "post" {
			gone, err := e.loadPostInput(c, data, in)
			if gone {
				done = true
				_, err = sc.Tx.Exec(ctx, `update sites.site_turns set status = 'failed', error = $3, finished_at = now(), updated_at = now()
					where workspace_id = $1::uuid and id = $2::uuid`, e.ws, e.turnID, postGoneMsg)
				return command.DB(err)
			}
			return err
		}
		return nil
	})
	if errors.Is(err, errNotVisible) {
		return nil, in, false, err // the worker retries with backoff; after max attempts the job fails visibly
	}
	return
}

// step is the Model RunTurn calls: reserve, call and record, settle, each in its own transaction (agents/loop.go).
func (e *turnExec) step(ctx context.Context, req StepReq) (StepRes, error) {
	if e.calls >= e.callCap() || e.tokens >= e.tokenCap() {
		e.fatal = errTurnLimit
		return StepRes{}, e.fatal
	}
	if err := e.progress(ctx, req); err != nil {
		return StepRes{}, err
	}
	req.Purpose = "site.chat"
	b, _ := json.Marshal(req)
	n := int64(len(b)/3) + stepReserve
	var day struct{ Day string }
	err := e.tx(ctx, func(c *command.Ctx) error {
		// Count the call before it is made, against the turn's shared totals (not this attempt's), so an overlapping
		// attempt or a crash after the provider is paid can never buy more than maxCalls calls per turn.
		var calls, tokens int64
		err := c.Tx.QueryRow(ctx, `update sites.site_turns set calls = calls + 1, updated_at = now()
			where workspace_id = $1::uuid and id = $2::uuid and attempt = $3 and status = 'running'
			returning calls, tokens_in + tokens_out`, e.ws, e.turnID, e.attempt).Scan(&calls, &tokens)
		if errors.Is(err, pgx.ErrNoRows) {
			e.fenced = true
			return errFenced
		}
		if err != nil {
			return command.DB(err)
		}
		if calls > int64(e.callCap()) || tokens >= e.tokenCap() {
			return errTurnLimit // rolls the increment back with the reservation
		}
		out, err := c.Call("agent.budget_reserve", props{"tokens": n})
		if err == nil {
			err = json.Unmarshal(out, &day)
		}
		return err
	})
	var ce *command.Error
	switch {
	case errors.Is(err, errFenced):
		return StepRes{}, errFenced
	case errors.Is(err, errTurnLimit):
		e.fatal = errTurnLimit
		return StepRes{}, e.fatal
	case errors.As(err, &ce) && ce.Code == command.PlanLimit:
		e.fatal = &failErr{"The workspace's daily AI budget is used up. Try again tomorrow."}
		return StepRes{}, e.fatal
	case err != nil:
		e.retry = err
		return StepRes{}, err
	}
	release := func(used int64) error {
		return e.reg.WithTx(context.WithoutCancel(ctx), e.pool, e.principal, func(c *command.Ctx) error {
			_, err := c.Call("agent.budget_settle", props{"day": day.Day, "reserved": n, "used": used})
			return err
		})
	}
	var out struct {
		StepRes
		Error  string
		Status int
	}
	err = e.tx(ctx, func(c *command.Ctx) error {
		payload, _ := json.Marshal(props{"Purpose": req.Purpose, "System": req.System, "Tools": req.Tools, "Turns": req.Turns, "MaxTokens": req.MaxTokens,
			"Tier": e.tier, "Ref": props{"site_id": e.siteID, "turn_id": e.turnID}})
		raw, err := c.Call("ai.step", json.RawMessage(payload))
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		return err
	})
	if err != nil {
		_ = release(n) // the provider may have been paid before the transaction failed: charge the whole reservation
		e.retry = err
		return StepRes{}, err
	}
	if out.Error != "" { // the provider failed; the call is audited, nothing was used
		if err := release(0); err != nil {
			e.retry = err
			return StepRes{}, err
		}
		if out.Status == 0 || out.Status == 429 || out.Status >= 500 {
			e.retry = errUnavailable
		} else {
			e.fatal = &failErr{"The AI service refused the request."}
		}
		return StepRes{}, errors.New(out.Error)
	}
	used := int64(out.TokensIn) + int64(out.TokensOut)
	// The provider has been paid: charge it now, so a fenced write below can never turn spent tokens back into budget.
	if err := release(used); err != nil {
		e.retry = err
		return StepRes{}, err
	}
	e.calls++
	e.tokens += used
	err = e.tx(ctx, func(c *command.Ctx) error {
		// Usage is recorded whatever the attempt (it was spent); only then is the fence checked.
		var attempt int32
		var status string
		err := c.Tx.QueryRow(ctx, `update sites.site_turns set tokens_in = tokens_in + $3, tokens_out = tokens_out + $4, cache_read = cache_read + $5,
			provider = $6, model = $7, updated_at = now()
			where workspace_id = $1::uuid and id = $2::uuid returning attempt, status`,
			e.ws, e.turnID, int32(out.TokensIn), int32(out.TokensOut), int32(out.CacheRead), out.Provider, out.Model).Scan(&attempt, &status)
		if err != nil {
			return command.DB(err)
		}
		if attempt != e.attempt || status != "running" {
			e.fenced = true
			return errFenced
		}
		return nil
	})
	if errors.Is(err, errFenced) {
		return StepRes{}, errFenced
	}
	if err != nil {
		e.retry = err
		return StepRes{}, err
	}
	return out.StepRes, nil
}

// progress writes the steps taken so far (read from the conversation) so the web can show them.
func (e *turnExec) progress(ctx context.Context, req StepReq) error {
	steps := []Step{}
	pending := map[string]int{}
	for _, t := range req.Turns {
		for _, call := range t.Calls {
			pending[call.ID] = len(steps)
			steps = append(steps, Step{Tool: call.Name, Input: call.Input, OK: true})
		}
		for _, r := range t.Results {
			if i, ok := pending[r.CallID]; ok && r.IsError {
				steps[i].OK, steps[i].IsError, steps[i].Error = false, true, r.Content
			}
		}
	}
	b, _ := json.Marshal(steps)
	err := e.tx(ctx, func(c *command.Ctx) error {
		tag, err := c.Tx.Exec(ctx, `update sites.site_turns set steps = $4, updated_at = now()
			where workspace_id = $1::uuid and id = $2::uuid and attempt = $3 and status = 'running'`, e.ws, e.turnID, e.attempt, b)
		if err == nil && tag.RowsAffected() == 0 {
			e.fenced = true
			return errFenced
		}
		return command.DB(err)
	})
	if err != nil && !errors.Is(err, errFenced) {
		e.retry = err
	}
	return err
}

// commit saves the result: the version, the draft pointer and the turn move together, or not at all.
func (e *turnExec) commit(ctx context.Context, res TurnResult) error {
	ctx = context.WithoutCancel(ctx)
	steps, _ := json.Marshal(res.Steps)
	reply := strings.TrimSpace(res.Reply)
	if r := []rune(reply); len(r) > 2000 {
		reply = string(r[:2000])
	}
	return e.tx(ctx, func(c *command.Ctx) error {
		var status string
		var attempt int32
		if err := c.Tx.QueryRow(ctx, "select status, attempt from sites.site_turns where workspace_id = $1::uuid and id = $2::uuid for update", e.ws, e.turnID).Scan(&status, &attempt); err != nil {
			return command.DB(err)
		}
		if status != "running" || attempt != e.attempt {
			return errFenced
		}
		var version *string
		if res.Changed {
			var draft string
			if err := c.Tx.QueryRow(ctx, "select draft_version_id::text from sites.sites where workspace_id = $1::uuid and id = $2::uuid for update", e.ws, e.siteID).Scan(&draft); err != nil {
				return command.DB(err)
			}
			if draft != e.base {
				_, err := c.Tx.Exec(ctx, `update sites.site_turns set status = 'failed', error = $3, steps = $4, finished_at = now(), updated_at = now()
					where workspace_id = $1::uuid and id = $2::uuid`, e.ws, e.turnID, pageMovedMsg, steps)
				return command.DB(err)
			}
			v, err := addVersion(c, e.siteID, &e.base, &e.turnID, "chat", summarize(res.Summary), res.Data)
			if err != nil {
				return err
			}
			version = &v
		}
		_, err := c.Tx.Exec(ctx, `update sites.site_turns set status = 'done', reply = $3, version_id = $4::uuid, steps = $5, finished_at = now(), updated_at = now()
			where workspace_id = $1::uuid and id = $2::uuid`, e.ws, e.turnID, reply, version, steps)
		return command.DB(err)
	})
}

var summaryVerb = map[string]string{"update_block_props": "Updated", "add_block": "Added", "remove_block": "Deleted", "move_block": "Moved", "set_theme": "Changed the theme",
	"add_page": "Added page", "remove_page": "Deleted page", "set_page": "Updated page"}

// summarize turns RunTurn's "update_block_props Hero, set_theme theme" into "Updated Hero, Changed the theme".
func summarize(s string) string {
	var out []string
	for _, part := range strings.Split(s, ", ") {
		tool, label, _ := strings.Cut(part, " ")
		if v, ok := summaryVerb[tool]; ok {
			if tool == "set_theme" {
				label = ""
			}
			out = append(out, strings.TrimSpace(v+" "+label))
		}
	}
	if len(out) == 0 {
		return "Changed the page"
	}
	return strings.Join(out, ", ")
}

// failed decides between retrying the job and failing the turn once RunTurn returned an error.
func (e *turnExec) failed(ctx context.Context, res TurnResult, begun time.Time, tier string) error {
	if e.fenced {
		return errFenced
	}
	if ctx.Err() != nil { // the worker is shutting down: the job retries from the base version
		return ctx.Err()
	}
	limit := 60 * time.Second
	if tier == "reasoning" {
		limit = 120 * time.Second
	}
	var fe *failErr
	switch {
	case e.fatal != nil:
		msg := e.fatal.Error()
		if errors.As(e.fatal, &fe) {
			msg = fe.msg
		}
		return e.fail(ctx, res, msg)
	case time.Since(begun) >= limit:
		return e.fail(ctx, res, "That took too long. Try a smaller change.")
	case e.retry != nil:
		if e.job.Attempts >= e.job.MaxAttempts {
			return e.fail(ctx, res, "The AI service is unavailable. Try again in a minute.")
		}
		return e.retry
	}
	return e.fail(ctx, res, friendly(res.Err))
}

func friendly(err string) string {
	switch err {
	case "invalid tool call after retry":
		return "I couldn't make that change. Try rephrasing your request."
	case "refusal":
		return "The AI declined that request."
	case "max_tokens", "token limit exceeded", "model call limit exceeded":
		return "That request was too big for one step. Try a smaller change."
	}
	if r := []rune(err); len(r) > 300 {
		err = string(r[:300])
	}
	return err
}

// fail ends the turn, fenced on this attempt; a turn someone cancelled stays cancelled.
func (e *turnExec) fail(ctx context.Context, res TurnResult, msg string) error {
	steps, _ := json.Marshal(res.Steps)
	if res.Steps == nil {
		steps = []byte("[]")
	}
	return e.tx(context.WithoutCancel(ctx), func(c *command.Ctx) error {
		_, err := c.Tx.Exec(c.Context, `update sites.site_turns set status = 'failed', error = $4, steps = $5, finished_at = now(), updated_at = now()
			where workspace_id = $1::uuid and id = $2::uuid and attempt = $3 and status = 'running'`, e.ws, e.turnID, e.attempt, msg, steps)
		return command.DB(err)
	})
}

// Sweep fails turns that are queued or running with no live job for five minutes (a worker died on the last attempt),
// so a dead job never blocks the site.
func Sweep(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, ws := range wss {
		tx, err := db.BeginTenant(ctx, pool, ws)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update sites.site_turns t set status = 'failed', error = 'The change did not finish. Try again.', finished_at = now(), updated_at = now()
			where t.status in ('queued','running') and t.updated_at < now() - make_interval(secs => $1::float8)
			  and not exists (select 1 from core.jobs j where j.workspace_id = t.workspace_id and j.kind = $2 and j.payload->>'turn_id' = t.id::text and j.status in ('queued','running'))`,
			sweepAfter.Seconds(), jobKind)
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("sites sweep: %w", err)
		}
	}
	return nil
}

const (
	postGoneMsg  = "That post is no longer on the site."
	postMovedMsg = "The post changed while I was working. Try again."
)

// loadPostInput gathers what a post turn shows the model. gone: the post to edit no longer exists.
func (e *turnExec) loadPostInput(c *command.Ctx, data json.RawMessage, in TurnInput) (gone bool, err error) {
	e.postIn = PostInput{Message: in.Message, Tier: in.Tier, Business: in.Business, History: in.History}
	var d map[string]any
	if json.Unmarshal(data, &d) == nil {
		for _, p := range pageMaps(d) {
			e.postIn.Pages = append(e.postIn.Pages, fmt.Sprint(p["title"]))
		}
	}
	if e.postID == nil {
		return false, nil
	}
	p, err := loadPostState(c, e.siteID, *e.postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	e.postIn.Post = p
	return false, err
}

// runPost runs a post turn: the model (through e.step, so budget, fence and audit are shared), then one save.
func (e *turnExec) runPost(ctx context.Context) error {
	begun := time.Now()
	res := RunPostTurn(ctx, e.step, e.postIn)
	var err error
	if res.Err == "" {
		err = e.commitPost(ctx, res)
	} else {
		err = e.failed(ctx, TurnResult{Err: res.Err, Steps: res.Steps}, begun, e.tier)
	}
	if errors.Is(err, errFenced) {
		return nil
	}
	return err
}

// commitPost saves the draft (or pending copy) and finishes the turn in one transaction.
func (e *turnExec) commitPost(ctx context.Context, res PostResult) error {
	ctx = context.WithoutCancel(ctx)
	steps, _ := json.Marshal(res.Steps)
	reply := strings.TrimSpace(res.Reply)
	if r := []rune(reply); len(r) > 2000 {
		reply = string(r[:2000])
	}
	return e.tx(ctx, func(c *command.Ctx) error {
		var status string
		var attempt int32
		if err := c.Tx.QueryRow(ctx, "select status, attempt from sites.site_turns where workspace_id = $1::uuid and id = $2::uuid for update", e.ws, e.turnID).Scan(&status, &attempt); err != nil {
			return command.DB(err)
		}
		if status != "running" || attempt != e.attempt {
			return errFenced
		}
		postID := e.postID
		if res.Changed {
			id, err := storePostDraft(c, e.siteID, e.postIn.Post, res)
			msg := postMovedMsg
			var ce *command.Error
			if errors.As(err, &ce) && (ce.Code == command.PlanLimit || ce.Code == command.Invalid) {
				msg, err = ce.Message, errPostMoved // a refusal that retrying cannot change: the person reads it
			}
			if errors.Is(err, errPostMoved) {
				_, err = c.Tx.Exec(ctx, `update sites.site_turns set status = 'failed', error = $3, steps = $4, finished_at = now(), updated_at = now()
					where workspace_id = $1::uuid and id = $2::uuid`, e.ws, e.turnID, msg, steps)
				return command.DB(err)
			}
			if err != nil {
				return err
			}
			postID = &id
			for i := range res.Steps { // the builder's "Review draft" button opens this post
				if res.Steps[i].Tool == "post_write" && res.Steps[i].OK {
					res.Steps[i].PostID = id
				}
			}
			steps, _ = json.Marshal(res.Steps)
		}
		_, err := c.Tx.Exec(ctx, `update sites.site_turns set status = 'done', reply = $3, post_id = $4::uuid, steps = $5, finished_at = now(), updated_at = now()
			where workspace_id = $1::uuid and id = $2::uuid`, e.ws, e.turnID, reply, postID, steps)
		return command.DB(err)
	})
}
