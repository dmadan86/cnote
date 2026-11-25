package server_test

import "testing"

// Docs parity B1, P3 (docs-parity-b 4.3): notification preferences. The digest and mail jobs are tested in the worker package.

func TestNotifyPrefsDefaultsAndPartialUpdates(t *testing.T) {
	d, owner := newDG(t)
	m := d.member(owner)
	got := d.ok(m, "notify_prefs.get", map[string]any{})
	if got["email_mode"] != "smart" || got["digest_hour"] != float64(8) || got["direct_off"] != false || got["digest_off"] != false {
		t.Fatalf("defaults: %v", got)
	}
	got = d.ok(m, "notify_prefs.set", map[string]any{"digest_hour": 18})
	if got["digest_hour"] != float64(18) || got["email_mode"] != "smart" {
		t.Fatalf("one field changes, the rest stay: %v", got)
	}
	got = d.ok(m, "notify_prefs.set", map[string]any{"email_mode": "digest", "direct_off": true})
	if got["email_mode"] != "digest" || got["digest_hour"] != float64(18) || got["direct_off"] != true || got["digest_off"] != false {
		t.Fatalf("second update: %v", got)
	}
	got = d.ok(m, "notify_prefs.set", map[string]any{"direct_off": false, "digest_off": true})
	if got["direct_off"] != false || got["digest_off"] != true || got["email_mode"] != "digest" {
		t.Fatalf("flags: %v", got)
	}
	// One person's settings are not another's.
	if o := d.ok(owner, "notify_prefs.get", map[string]any{}); o["email_mode"] != "smart" || o["digest_off"] != false {
		t.Fatalf("owner: %v", o)
	}
	d.code("invalid", m, "notify_prefs.set", map[string]any{"digest_hour": 24})
	d.code("invalid", m, "notify_prefs.set", map[string]any{"email_mode": "weekly"})
}
