package browser

import (
	"strings"
	"testing"
	"time"
)

func TestWatchLifecycle(t *testing.T) {
	mgr := &Manager{}

	if mgr.IsWatching() {
		t.Fatalf("new manager should not be watching")
	}

	mgr.startWatch()
	if !mgr.IsWatching() {
		t.Fatalf("manager should be watching after startWatch")
	}
	if mgr.watchStartTime.IsZero() {
		t.Fatalf("watchStartTime should be set after startWatch")
	}

	mgr.watchStartTime = time.Now().Add(-3 * time.Second)
	mgr.addWatchSnapshot("snapshot-1")
	mgr.addWatchSnapshot("snapshot-2")

	snapshots, duration := mgr.stopWatch()
	if mgr.IsWatching() {
		t.Fatalf("manager should not be watching after stopWatch")
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(snapshots))
	}
	if snapshots[0] != "snapshot-1" || snapshots[1] != "snapshot-2" {
		t.Fatalf("snapshots = %#v, want [snapshot-1 snapshot-2]", snapshots)
	}
	if duration < 2*time.Second {
		t.Fatalf("duration = %s, want >= 2s", duration)
	}
	if !mgr.watchStartTime.IsZero() {
		t.Fatalf("watchStartTime should be reset after stopWatch")
	}
	if len(mgr.watchSnapshots) != 0 {
		t.Fatalf("watchSnapshots should be cleared after stopWatch")
	}

	mgr.addWatchSnapshot("ignored-when-not-watching")
	mgr.startWatch()
	mgr.addWatchSnapshot("fresh-snapshot")
	freshSnapshots, _ := mgr.stopWatch()
	if len(freshSnapshots) != 1 || freshSnapshots[0] != "fresh-snapshot" {
		t.Fatalf("fresh snapshots = %#v, want [fresh-snapshot]", freshSnapshots)
	}
}

func TestStateTTLWarning(t *testing.T) {
	now := time.Date(2026, time.April, 4, 12, 0, 0, 0, time.UTC)

	t.Run("stale_state_shows_warning", func(t *testing.T) {
		savedAt := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
		warning := stateTTLWarning(savedAt, now)
		if !strings.Contains(warning, "8 days old") {
			t.Fatalf("warning = %q, want to contain %q", warning, "8 days old")
		}
	})

	t.Run("fresh_state_has_no_warning", func(t *testing.T) {
		savedAt := now.Add(-6 * 24 * time.Hour).Format(time.RFC3339)
		if warning := stateTTLWarning(savedAt, now); warning != "" {
			t.Fatalf("warning = %q, want empty", warning)
		}
	})

	t.Run("invalid_timestamp_has_no_warning", func(t *testing.T) {
		if warning := stateTTLWarning("not-a-time", now); warning != "" {
			t.Fatalf("warning = %q, want empty", warning)
		}
	})
}
