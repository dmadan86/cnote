package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/sites"
)

// Phase 3b languages (docs/design/site-builder-phase3.md section 18.1 to 18.7).

// hiModel translates every entry it is sent into "हि:<source>" and remembers the keys of each call.
func hiModel(t *testing.T, calls *[][]string) *scriptedModel {
	return newScriptedModel(t, func(r modelReq) []map[string]any {
		if r.Turn > 0 {
			return []map[string]any{mtext("Done.")}
		}
		i, j := strings.Index(r.First, "<entries>\n"), strings.Index(r.First, "\n</entries>")
		var enc string
		var list []struct{ Key, Text string }
		if i < 0 || j < i || json.Unmarshal([]byte(r.First[i+10:j]), &enc) != nil || json.Unmarshal([]byte(enc), &list) != nil {
			t.Errorf("unreadable request: %q", r.First)
			return []map[string]any{mtext("?")}
		}
		out, keys := []map[string]any{}, []string{}
		for _, e := range list {
			out = append(out, map[string]any{"key": e.Key, "text": "हि:" + e.Text})
			keys = append(keys, e.Key)
		}
		*calls = append(*calls, keys)
		return []map[string]any{muse("t", "set_translations", map[string]any{"entries": append(out, map[string]any{"key": "Hero-ffffffff.title", "text": "ignored"})})}
	})
}

func (p *pubEnv) langs(w who, langs []string, extra map[string]any) resp {
	in := map[string]any{"id": p.site, "expected_version_id": p.draft(), "langs": langs}
	for k, v := range extra {
		in[k] = v
	}
	return p.call(w, "site.set_langs", in)
}

func (p *pubEnv) translations(lang, page string) []map[string]any {
	p.t.Helper()
	in := map[string]any{"id": p.site, "lang": lang}
	if page != "" {
		in["page"] = page
	}
	var out []map[string]any
	for _, pg := range p.ok(p.mia, "site.translations", in)["pages"].([]any) {
		for _, e := range pg.(map[string]any)["entries"].([]any) {
			out = append(out, e.(map[string]any))
		}
	}
	return out
}

func entryOfKey(list []map[string]any, key string) map[string]any {
	for _, e := range list {
		if e["key"] == key {
			return e
		}
	}
	return nil
}

func TestSiteLanguagesSettings(t *testing.T) {
	p := newPubEnv(t)
	eq(t, p.langs(p.mia, []string{"en", "hi"}, nil).status, 400) // SITES_LANGUAGES is off
	t.Setenv("SITES_LANGUAGES", "1")
	eq(t, p.langs(p.mia, []string{"en", "pa"}, nil).status, 400) // pa and or need SITES_EXTRA_LANGS
	eq(t, p.langs(p.mia, []string{"en", "fr"}, nil).status, 400)
	eq(t, p.langs(p.mia, []string{"en", "hi", "ta"}, nil).status, 402) // one extra language on the included plan
	r := adminOK(t, p.langs(p.mia, []string{"en", "hi"}, nil))
	l := p.ok(p.mia, "site.languages", map[string]any{"id": p.site})
	eq(t, l["enabled"], true)
	langs := l["langs"].([]any)
	eq(t, len(langs), 2)
	eq(t, langs[1].(map[string]any)["hidden"], true) // a new language starts hidden until checked
	if e := langs[1].(map[string]any)["entries"].(map[string]any); e["missing"].(float64) < 5 || e["ok"] != float64(0) {
		t.Fatalf("entries: %v", e)
	}
	eq(t, r["site"].(map[string]any)["rev"], float64(0)+r["site"].(map[string]any)["rev"].(float64))
	// Pro allows three extra languages; the main language is fixed once there are others
	adminOK(t, p.call(p.owner, "site.platform_set", map[string]any{"workspace_slug": p.sql("select slug from core.workspaces"), "action": "pro_on", "reason": "language test"}))
	adminOK(t, p.langs(p.mia, []string{"en", "hi", "ta", "te"}, map[string]any{"hidden": []string{"ta"}}))
	eq(t, p.langs(p.mia, []string{"hi", "en", "ta", "te"}, nil).status, 400)
	eq(t, p.langs(p.mia, []string{"en", "hi", "ta", "te", "kn"}, nil).status, 400)
	t.Setenv("SITES_EXTRA_LANGS", "pa")
	adminOK(t, p.langs(p.mia, []string{"en", "pa"}, nil))
	// removing a language drops it from the document
	adminOK(t, p.langs(p.mia, []string{"en"}, nil))
	if strings.Contains(string(jsonOf(p.get(p.mia, p.site)["data"])), "langs") {
		t.Fatal("langs stayed in the document")
	}
}

func TestSiteTranslateTurnAndReview(t *testing.T) {
	t.Setenv("SITES_LANGUAGES", "1")
	p := newPubEnv(t)
	adminOK(t, p.editOps(p.owner, p.site, p.draft(), map[string]any{"tool": "add_page", "input": map[string]any{"title": "About us"}}))
	adminOK(t, p.langs(p.mia, []string{"en", "hi"}, nil))
	var calls [][]string
	p.useModel(p.owner, hiModel(t, &calls))
	start := func(extra map[string]any) string {
		in := map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi"}
		for k, v := range extra {
			in[k] = v
		}
		return p.ok(p.mia, "site.translate", in)["turn_id"].(string)
	}
	// English is the main language, and an agent may not drive the model
	eq(t, p.call(p.mia, "site.translate", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "en"}).status, 400)

	before := p.draft()
	turn := start(nil)
	// the one-active-turn rule holds while it runs
	done := p.wait(p.mia, turn)["turn"].(map[string]any)
	eq(t, done["status"], "done")
	eq(t, done["kind"], "translate")
	if done["version_id"] == nil || done["version_id"] == before {
		t.Fatalf("no version: %v", done)
	}
	eq(t, p.draft(), done["version_id"])
	eq(t, len(calls), 2) // Home and About: one model call per page
	eq(t, p.sql("select (source = 'chat' and parent_id = $1::uuid)::text from sites.site_versions where id = $2::uuid", before, done["version_id"]), "true")
	eq(t, p.sql("select (kind = 'translate' and calls = 2)::text from sites.site_turns where id = $1::uuid", turn), "true")
	hero := entryOfKey(p.translations("hi", "@home"), blockOf(p.get(p.mia, p.site)["data"], "Hero")+".title")
	eq(t, hero["status"], "ok")
	eq(t, hero["checked"], false)
	if !strings.HasPrefix(hero["translation"].(string), "हि:") {
		t.Fatalf("hero: %v", hero)
	}
	if e := entryOfKey(p.translations("hi", "about-us"), "@meta.title"); e == nil || e["translation"] != "हि:About us" {
		t.Fatalf("page title: %v", e)
	}
	// nothing left to do: free, no version, no model call
	n, v := len(calls), p.versions()
	t2 := p.wait(p.mia, start(nil))["turn"].(map[string]any)
	eq(t, t2["status"], "done")
	eq(t, t2["version_id"], nil)
	eq(t, len(calls), n)
	eq(t, p.versions(), v)

	// a source edit makes one entry stale; a re-run translates only that one
	heroID := blockOf(p.get(p.mia, p.site)["data"], "Hero")
	adminOK(t, p.editOps(p.mia, p.site, p.draft(), map[string]any{"tool": "update_block_props", "input": map[string]any{"id": heroID, "props": map[string]any{"title": "Fresh title"}}}))
	eq(t, entryOfKey(p.translations("hi", "@home"), heroID+".title")["status"], "needs-update")
	p.wait(p.mia, start(nil))
	eq(t, len(calls), n+1)
	eq(t, len(calls[n]), 1)
	eq(t, calls[n][0], heroID+".title")
	eq(t, entryOfKey(p.translations("hi", "@home"), heroID+".title")["translation"], "हि:Fresh title")

	// the owner edits one entry (it counts as checked); "Looks right" checks the rest
	adminOK(t, p.call(p.mia, "site.set_translation", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi",
		"entries": []map[string]any{{"key": heroID + ".title", "text": "मेरा शीर्षक"}}}))
	e := entryOfKey(p.translations("hi", "@home"), heroID+".title")
	eq(t, e["checked"], true)
	eq(t, e["status"], "ok")
	eq(t, p.call(p.mia, "site.set_translation", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi",
		"entries": []map[string]any{{"key": heroID + ".cta_href", "text": "x"}}}).status, 400) // not a translatable prop
	eq(t, p.call(p.mia, "site.set_translation", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi",
		"entries": []map[string]any{{"key": heroID + ".title", "text": strings.Repeat("अ", 81)}}}).status, 400)
	rv := adminOK(t, p.call(p.mia, "site.review_translation", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi"}))
	if rv["checked"].(float64) < 3 {
		t.Fatalf("checked %v", rv["checked"])
	}
	l := p.ok(p.mia, "site.languages", map[string]any{"id": p.site})["langs"].([]any)[1].(map[string]any)["entries"].(map[string]any)
	eq(t, l["unchecked"], float64(0))
	eq(t, l["missing"], float64(0))
	eq(t, p.call(p.mia, "site.review_translation", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi"}).json()["checked"], float64(0))
}

// The web's command names wrap the same operations on the current draft.
func TestSiteLangWebCommands(t *testing.T) {
	t.Setenv("SITES_LANGUAGES", "1")
	p := newPubEnv(t)
	l := p.ok(p.mia, "site.lang_add", map[string]any{"id": p.site, "lang": "hi"})["langs"].([]any)
	eq(t, len(l), 1)
	eq(t, l[0].(map[string]any)["hidden"], true)
	eq(t, l[0].(map[string]any)["checked"], false)
	svc := blockOf(p.get(p.mia, p.site)["data"], "Services")
	adminOK(t, p.editOps(p.mia, p.site, p.draft(), map[string]any{"tool": "update_block_props", "input": map[string]any{"id": svc, "props": map[string]any{"items": []any{map[string]any{"name": "Cleaning"}}}}}))
	rv := p.ok(p.mia, "site.lang_review", map[string]any{"id": p.site, "lang": "hi"})
	var item map[string]any
	for _, pg := range rv["pages"].([]any) {
		for _, e := range pg.(map[string]any)["entries"].([]any) {
			if m := e.(map[string]any); m["prop"] == "items.name" {
				item = m
			}
		}
	}
	if item == nil || item["status"] != "missing" || item["source"] == "" || item["index"] != float64(0) {
		t.Fatalf("list entry: %v", item)
	}
	set := p.ok(p.mia, "site.lang_entry_set", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi", "block_id": item["block_id"], "prop": "items.name", "index": 0, "text": "सेवा"})
	eq(t, set["entry"].(map[string]any)["status"], "ok")
	p.ok(p.mia, "site.lang_set_hidden", map[string]any{"id": p.site, "lang": "hi", "hidden": false})
	sub := uniq()
	p.publishOK(sub)
	st, m := p.publicGet(sub)
	eq(t, st, 200)
	eq(t, m["langs"].([]any)[0], "hi")
	raw := string(jsonOf(m["data"]))
	if !strings.Contains(raw, `"text":"सेवा"`) || !strings.Contains(raw, `"fp":`) {
		t.Fatalf("overlay: %s", raw)
	}
	p.ok(p.mia, "site.lang_mark_checked", map[string]any{"id": p.site, "lang": "hi"})
	p.ok(p.mia, "site.lang_remove", map[string]any{"id": p.site, "lang": "hi"})
}

func TestSiteLanguagesPublicAndPosts(t *testing.T) {
	t.Setenv("SITES_LANGUAGES", "1")
	p := newPubEnv(t)
	adminOK(t, p.call(p.owner, "site.platform_set", map[string]any{"workspace_slug": p.sql("select slug from core.workspaces"), "action": "pro_on", "reason": "language test"}))
	adminOK(t, p.langs(p.mia, []string{"en", "hi", "ta"}, map[string]any{"hidden": []string{"hi", "ta"}}))
	// a draft post in Hindi pins the language
	post := adminOK(t, p.call(p.mia, "site.post_save", map[string]any{"id": p.site, "title": "नमस्ते", "body": docOf(para("पहला")), "lang": "hi"}))
	eq(t, post["lang"], "hi")
	eq(t, p.call(p.mia, "site.post_save", map[string]any{"id": p.site, "title": "x", "body": docOf(para("x")), "lang": "kn"}).status, 400)
	eq(t, p.langs(p.mia, []string{"en", "ta"}, nil).status, 409)
	sub := uniq()
	p.publishOK(sub)
	adminOK(t, p.call(p.owner, "site.post_publish", map[string]any{"id": p.site, "post_id": post["id"], "expected_rev": post["rev"], "publish": true}))
	eq(t, p.call(p.mia, "site.post_save", map[string]any{"id": p.site, "post_id": post["id"], "expected_rev": p.ok(p.mia, "site.post_get", map[string]any{"id": p.site, "post_id": post["id"]})["rev"], "title": "नमस्ते", "body": docOf(para("पहला")), "lang": "en"}).status, 400) // a published post keeps its language
	if r := p.langs(p.mia, []string{"en", "ta"}, nil); r.status != 409 || !strings.Contains(string(r.body), "published post") {
		t.Fatalf("%d %s", r.status, r.body)
	}

	// hidden languages are not served: no overlay, not in the languages list, no posts
	ctx := context.Background()
	host := sub + "." + siteDomain
	pub, err := sites.LookupHost(ctx, p.pool, host)
	if err != nil {
		t.Fatal(err)
	}
	st, m := p.publicGet(sub)
	eq(t, st, 200)
	eq(t, m["languages"].(map[string]any)["main"], "en")
	eq(t, len(m["languages"].(map[string]any)["shown"].([]any)), 1)
	if _, err := sites.PublicPostsLang(ctx, p.pool, pub, "hi", 1, 10); err == nil {
		t.Fatal("a hidden language served its posts")
	}
	if _, err := sites.PublicPostIn(ctx, p.pool, pub, "hi", "namaste"); err == nil {
		t.Fatal("a hidden language served a post")
	}
	// show Hindi (with one translated title) and publish: the overlay, the language and the posts appear
	heroID := blockOf(p.get(p.mia, p.site)["data"], "Hero")
	adminOK(t, p.call(p.mia, "site.set_translation", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "hi",
		"entries": []map[string]any{{"key": heroID + ".title", "text": "हिंदी शीर्षक"}}}))
	adminOK(t, p.call(p.mia, "site.set_translation", map[string]any{"id": p.site, "expected_version_id": p.draft(), "lang": "ta",
		"entries": []map[string]any{{"key": heroID + ".title", "text": "தமிழ் தலைப்பு"}}}))
	adminOK(t, p.langs(p.mia, []string{"en", "hi", "ta"}, map[string]any{"hidden": []string{"ta"}}))
	p.publishOK(sub)
	st, m = p.publicGet(sub)
	eq(t, st, 200)
	eq(t, len(m["languages"].(map[string]any)["shown"].([]any)), 2)
	raw := string(jsonOf(m["data"]))
	if !strings.Contains(raw, "हिंदी शीर्षक") || strings.Contains(raw, "தமிழ் தலைப்பு") || strings.Contains(raw, "langs_hidden") {
		t.Fatalf("public data: %s", raw)
	}
	pub, _ = sites.LookupHost(ctx, p.pool, host)
	list, err := sites.PublicPostsLang(ctx, p.pool, pub, "hi", 1, 10)
	if err != nil || len(list["posts"].([]sites.PostSummary)) != 1 {
		t.Fatalf("hindi posts: %v %v", list, err)
	}
	if _, err := sites.PublicPostsLang(ctx, p.pool, pub, "en", 1, 10); err == nil {
		t.Fatal("an English index listed a Hindi post")
	}
	if got, err := sites.PublicPostIn(ctx, p.pool, pub, "hi", post["slug"].(string)); err != nil || got["lang"] != "hi" {
		t.Fatalf("hindi post: %v %v", got, err)
	}
	if _, err := sites.PublicPostIn(ctx, p.pool, pub, "en", post["slug"].(string)); err == nil {
		t.Fatal("a Hindi post answered under /en")
	}

	// the form: the language must be shown on the published version; the consent version carries it
	reg := command.NewRegistry(modules.Commands()...)
	in := sites.Submission{Name: "Asha", Phone: "+919876543210", Message: "hello", ConsentText: "t", Lang: "ta"}
	if err := sites.Submit(ctx, p.pool, reg, pub, in); err == nil {
		t.Fatal("a hidden language took a form")
	}
	text, ver, _ := sites.ConsentForLang("", "hi", pub.Name)
	in.Lang, in.ConsentText, in.ConsentVersion = "hi", text, ver
	if err := sites.Submit(ctx, p.pool, reg, pub, in); err != nil {
		t.Fatal(err)
	}
}
