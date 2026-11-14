package planning

import (
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
)

// TestBurndownCountsLatestStatePerDay ports crates/cnote-projects/src/planning.rs::burndown_counts_latest_state_per_day.
func TestBurndownCountsLatestStatePerDay(t *testing.T) {
	a := "aaaaaaaa-0000-4000-8000-000000000001"
	b := "aaaaaaaa-0000-4000-8000-000000000002"
	c := "aaaaaaaa-0000-4000-8000-000000000003"

	at := func(day int, h int) time.Time {
		return time.Date(2026, 3, day, h, 0, 0, 0, time.UTC)
	}

	items := [][2]any{
		{a, 5.0},
		{b, 3.0},
		{c, 2.0},
	}
	events := [][3]any{
		{a, at(2, 9), "done"},
		{b, at(2, 10), "done"},
		{b, at(3, 8), "in_progress"}, // reopened
		{c, at(4, 8), "done"},
	}
	pts := burndown("2026-03-01", "2026-03-04", items, events)
	if len(pts) != 4 {
		t.Fatalf("expected 4 points, got %d", len(pts))
	}
	want := []float64{10.0, 2.0, 5.0, 3.0}
	for i, p := range pts {
		if float64(p.Remaining) != want[i] {
			t.Errorf("day %d: remaining %.1f, want %.1f", i, float64(p.Remaining), want[i])
		}
	}
	if float64(pts[0].Ideal) != 10.0 {
		t.Errorf("day 0 ideal %.1f, want 10.0", float64(pts[0].Ideal))
	}
	if float64(pts[3].Ideal) != 0.0 {
		t.Errorf("day 3 ideal %.1f, want 0.0", float64(pts[3].Ideal))
	}
	_ = command.F64(0) // keep import
}

// TestBurndownSingleDay ports planning.rs::burndown_single_day.
func TestBurndownSingleDay(t *testing.T) {
	id := "aaaaaaaa-0000-4000-8000-000000000001"
	pts := burndown("2026-03-01", "2026-03-01", [][2]any{{id, 2.0}}, nil)
	if len(pts) != 1 {
		t.Fatalf("expected 1 point, got %d", len(pts))
	}
	if float64(pts[0].Remaining) != 2.0 {
		t.Errorf("remaining %.1f, want 2.0", float64(pts[0].Remaining))
	}
	if float64(pts[0].Ideal) != 0.0 {
		t.Errorf("ideal %.1f, want 0.0 (single day)", float64(pts[0].Ideal))
	}
}

// TestBurndownEmptyItems handles no items.
func TestBurndownEmptyItems(t *testing.T) {
	pts := burndown("2026-03-01", "2026-03-03", nil, nil)
	if len(pts) != 3 {
		t.Fatalf("expected 3 points, got %d", len(pts))
	}
	for i, p := range pts {
		if float64(p.Remaining) != 0.0 || float64(p.Ideal) != 0.0 {
			t.Errorf("day %d: remaining %.1f ideal %.1f, want 0.0", i, float64(p.Remaining), float64(p.Ideal))
		}
	}
}

// TestBurndownLastBeforeStart – should return a single point (day 0) since days < 0 → days=0.
func TestBurndownLastEqualsStart(t *testing.T) {
	id := "aaaaaaaa-0000-4000-8000-000000000001"
	pts := burndown("2026-03-05", "2026-03-03", [][2]any{{id, 3.0}}, nil)
	if len(pts) != 1 {
		t.Fatalf("expected 1 point when last < start, got %d", len(pts))
	}
	// ideal = 0 (days == 0), remaining = total (not done)
	if float64(pts[0].Remaining) != 3.0 {
		t.Errorf("remaining %.1f, want 3.0", float64(pts[0].Remaining))
	}
}
