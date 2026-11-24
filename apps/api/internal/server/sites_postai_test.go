package server_test

import (
	"strings"
	"testing"
)

const aiBody = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"old text"}]}]}`

// postTurn queues a "post" turn the way site.chat will: a turn row with kind post, and its job.
func (s *siteEnv) postTurn(site, postID, msg string) string {
	turn := s.sql("select gen_random_uuid()::text")
	adminSQL(s.env, s.owner.ws, `insert into sites.site_turns (id, workspace_id, site_id, created_by, message, base_version_id, kind, post_id)
		select $3::uuid, workspace_id, id, $2::uuid, $4, draft_version_id, 'post', case when $5::text = '' then null else $5::text::uuid end from sites.sites where id = $1::uuid`, site, s.mia.userID, turn, msg, postID)
	adminSQL(s.env, s.owner.ws, `insert into core.jobs (workspace_id, kind, payload, max_attempts) values ($1::uuid, 'site.chat', jsonb_build_object('turn_id', $2::text), 2)`, s.owner.ws, turn)
	return turn
}

func (s *siteEnv) newPost(site, status string, rev int) string {
	id := s.sql("select gen_random_uuid()::text")
	adminSQL(s.env, s.owner.ws, `insert into sites.posts (id, workspace_id, site_id, slug, title, description, body, status, rev, created_by)
		select $6::uuid, workspace_id, id, 'old-' || substr($6::text, 1, 6), 'Old title', 'd', $2::jsonb, $3, $4, $5::uuid from sites.sites where id = $1::uuid`,
		site, aiBody, status, rev, s.mia.userID, id)
	return id
}

func (s *siteEnv) finish(turn string) string {
	settled(s.t, "the turn", func() bool {
		return !strings.Contains("queued running", s.sql("select status from sites.site_turns where id = $1::uuid", turn))
	})
	return s.sql("select status from sites.site_turns where id = $1::uuid", turn)
}

func writeScript(title string) func(modelReq) []map[string]any {
	return func(r modelReq) []map[string]any {
		if r.Turn == 0 {
			return []map[string]any{muse("w", "post_write", map[string]any{"title": title, "blocks": []any{map[string]any{"kind": "p", "text": "new text [add: price]"}}})}
		}
		return []map[string]any{mtext("Drafted.")}
	}
}

// A post turn writes a draft, or the pending copy of a published post, and never touches the page or publishes.
func TestSitePostAITurns(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id := site["id"].(string)
	model := newScriptedModel(t, writeScript("A new post"))
	s.useModel(s.owner, model)

	turn := s.postTurn(id, "", "write a post")
	eq(t, s.finish(turn), "done")
	eq(t, s.sql("select (p.status = 'draft' and p.ai and p.rev = 0 and p.title = 'A new post' and p.slug = 'a-new-post' and p.pending is null and t.post_id = p.id)::text from sites.posts p, sites.site_turns t where t.id = $1::uuid", turn), "true")
	eq(t, s.sql("select (body::text like '%[add: price]%')::text from sites.posts where title = 'A new post'"), "true")
	eq(t, s.sql("select (t.steps->0->>'post_id' = p.id::text)::text from sites.posts p, sites.site_turns t where t.id = $1::uuid", turn), "true")
	eq(t, s.versions(), "1") // the page is untouched
	if got := model.reqs[0].Tools; len(got) != 2 || got[0] != "post_plan" || got[1] != "post_write" {
		t.Fatalf("tools: %v", got)
	}

	// a draft is rewritten in place and its rev moves
	draft := s.newPost(id, "draft", 2)
	eq(t, s.finish(s.postTurn(id, draft, "rewrite")), "done")
	eq(t, s.sql("select (title = 'A new post' and rev = 3 and pending is null and ai)::text from sites.posts where id = $1::uuid", draft), "true")

	// a published post keeps its live copy; the change waits in pending
	live := s.newPost(id, "published", 5)
	eq(t, s.finish(s.postTurn(id, live, "rewrite")), "done")
	eq(t, s.sql("select (title = 'Old title' and body = $2::jsonb and status = 'published' and rev = 6 and pending->>'title' = 'A new post' and pending->'body'::text is not null)::text from sites.posts where id = $1::uuid", live, aiBody), "true")

	// the post is deleted while the model works: the turn fails with a message and creates nothing
	before := s.sql("select count(*)::text from sites.posts")
	gone := s.newPost(id, "draft", 0)
	gm, g := newGate(t, writeScript("Late"))
	s.useModel(s.owner, gm)
	turn = s.postTurn(id, gone, "rewrite")
	g.waitReached(t)
	adminSQL(s.env, s.owner.ws, "delete from sites.posts where id = $1::uuid", gone)
	g.open()
	eq(t, s.finish(turn), "failed")
	eq(t, s.sql("select error from sites.site_turns where id = $1::uuid", turn), "The post changed while I was working. Try again.")
	eq(t, s.sql("select count(*)::text from sites.posts"), before)
}

func TestSitePostAIPlanAndConflict(t *testing.T) {
	s := newSiteEnv(t)
	site := s.create()["site"].(map[string]any)
	id := site["id"].(string)

	plan := newScriptedModel(t, func(r modelReq) []map[string]any {
		if r.Turn == 0 {
			return []map[string]any{muse("p", "post_plan", map[string]any{"title": "Teeth", "points": []string{"a", "b"}})}
		}
		return []map[string]any{mtext("Here is a plan.")}
	})
	s.useModel(s.owner, plan)
	turn := s.postTurn(id, "", "write a post about teeth")
	eq(t, s.finish(turn), "done")
	eq(t, s.sql("select count(*)::text from sites.posts"), "0")
	eq(t, s.sql("select (steps->0->>'tool' = 'post_plan' and reply = 'Here is a plan.' and post_id is null)::text from sites.site_turns where id = $1::uuid", turn), "true")

	// the post changed while the model worked: nothing is overwritten
	post := s.newPost(id, "draft", 1)
	model, g := newGate(t, writeScript("Late"))
	s.useModel(s.owner, model)
	turn = s.postTurn(id, post, "rewrite")
	g.waitReached(t)
	adminSQL(s.env, s.owner.ws, "update sites.posts set rev = rev + 1, title = 'Owner edit' where id = $1::uuid", post)
	g.open()
	eq(t, s.finish(turn), "failed")
	eq(t, s.sql("select error from sites.site_turns where id = $1::uuid", turn), "The post changed while I was working. Try again.")
	eq(t, s.sql("select (title = 'Owner edit' and rev = 2 and not ai)::text from sites.posts where id = $1::uuid", post), "true")
}
