package browser

import (
	"strings"
	"testing"
)

func TestSnapshot(t *testing.T) {
	mgr := sharedTestManager(t)

	t.Run("basic_snapshot_has_roles_and_refs", func(t *testing.T) {
		gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")

		out := mustExecCommand(t, mgr, "snapshot")
		assertContains(t, out, "@e")
		assertContains(t, out, `heading "Snapshot Test"`)
		assertContains(t, out, `link "Internal Link"`)
		assertContains(t, out, `button "Submit"`)
	})

	t.Run("flags_interactive_compact_depth_selector", func(t *testing.T) {
		t.Run("interactive_only", func(t *testing.T) {
			gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
			out := mustExecCommand(t, mgr, "snapshot", "-i")

			assertContains(t, out, `textbox "Username"`)
			assertContains(t, out, `button "Submit"`)
			assertContains(t, out, `link "Internal Link"`)
			if strings.Contains(out, `heading "Snapshot Test"`) {
				t.Fatalf("interactive snapshot should not include heading, got:\n%s", out)
			}
		})

		t.Run("compact_reduces_or_keeps_lines", func(t *testing.T) {
			gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
			full := mustExecCommand(t, mgr, "snapshot")
			compact := mustExecCommand(t, mgr, "snapshot", "-c")

			fullLines := snapshotNonEmptyLines(full)
			compactLines := snapshotNonEmptyLines(compact)
			if compactLines > fullLines {
				t.Fatalf("compact snapshot lines = %d, want <= full lines %d", compactLines, fullLines)
			}
		})

		t.Run("depth_limit_reduces_or_keeps_lines", func(t *testing.T) {
			gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
			deep := mustExecCommand(t, mgr, "snapshot")
			shallow := mustExecCommand(t, mgr, "snapshot", "-d", "2")

			deepLines := snapshotNonEmptyLines(deep)
			shallowLines := snapshotNonEmptyLines(shallow)
			if shallowLines > deepLines {
				t.Fatalf("depth-limited snapshot lines = %d, want <= full lines %d", shallowLines, deepLines)
			}
		})

		t.Run("selector_scope", func(t *testing.T) {
			gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
			out := mustExecCommand(t, mgr, "snapshot", "-s", "#main")

			assertContains(t, out, `textbox "Username"`)
			assertContains(t, out, `button "Submit"`)
			if strings.Contains(out, "Internal Link") {
				t.Fatalf("selector-scoped snapshot should not include nav links, got:\n%s", out)
			}
		})
	})

	t.Run("diff_mode_baseline_and_changed_diff", func(t *testing.T) {
		t.Run("first_diff_creates_baseline", func(t *testing.T) {
			mgr.SetLastSnapshot("")
			gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")

			out := mustExecCommand(t, mgr, "snapshot", "-D")
			assertContains(t, out, "no previous snapshot to diff against")
			assertContains(t, out, "baseline")
		})

		t.Run("diff_after_dom_change", func(t *testing.T) {
			mgr.SetLastSnapshot("")
			gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
			_ = mustExecCommand(t, mgr, "snapshot")
			_ = mustExecCommand(t, mgr, "js", `document.querySelector("h1").textContent = "Changed Title"`)

			diff := mustExecCommand(t, mgr, "snapshot", "-D")
			assertContains(t, diff, "--- previous snapshot")
			assertContains(t, diff, "+++ current snapshot")
			assertContains(t, diff, "Changed Title")
		})
	})

	t.Run("cursor_interactive", func(t *testing.T) {
		gotoFixtureAndWaitLoad(t, mgr, "cursor-interactive.html")

		out := mustExecCommand(t, mgr, "snapshot", "-C")
		assertContains(t, out, "cursor-interactive")
		assertContains(t, out, "@c")
		assertContains(t, out, "cursor:pointer")
		assertContains(t, out, "onclick")
		assertContains(t, out, "tabindex")

		cursorRef := findCursorRefInSnapshot(t, out)
		clickOut := mustExecCommand(t, mgr, "click", cursorRef)
		assertContains(t, clickOut, "Clicked")
	})

	t.Run("ref_based_interaction_commands", func(t *testing.T) {
		gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
		interactive := mustExecCommand(t, mgr, "snapshot", "-i")

		usernameRef := findRefInSnapshot(t, interactive, "textbox", "Username")
		submitRef := findRefInSnapshot(t, interactive, "button", "Submit")
		linkRef := findRefInSnapshot(t, interactive, "link", "Internal Link")

		fillOut := mustExecCommand(t, mgr, "fill", usernameRef, "testuser")
		assertContains(t, fillOut, "Filled")

		usernameVal := mustExecCommand(t, mgr, "js", `document.querySelector("#username").value`)
		if usernameVal != "testuser" {
			t.Fatalf("username value = %q, want %q", usernameVal, "testuser")
		}

		clickOut := mustExecCommand(t, mgr, "click", submitRef)
		assertContains(t, clickOut, "Clicked")

		hoverOut := mustExecCommand(t, mgr, "hover", linkRef)
		assertContains(t, hoverOut, "Hovered")

		attrsOut := mustExecCommand(t, mgr, "attrs", usernameRef)
		assertContains(t, attrsOut, `"id": "username"`)

		full := mustExecCommand(t, mgr, "snapshot")
		headingRef := findRefInSnapshot(t, full, "heading", "Snapshot Test")

		htmlOut := mustExecCommand(t, mgr, "html", headingRef)
		assertContains(t, htmlOut, "Snapshot Test")

		cssOut := mustExecCommand(t, mgr, "css", headingRef, "font-family")
		if strings.TrimSpace(cssOut) == "" {
			t.Fatalf("css font-family should not be empty")
		}
	})

	t.Run("ref_invalidation_after_navigation", func(t *testing.T) {
		gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
		interactive := mustExecCommand(t, mgr, "snapshot", "-i")
		submitRef := findRefInSnapshot(t, interactive, "button", "Submit")

		if refCount := len(mgr.GetRefs()); refCount == 0 {
			t.Fatalf("expected refs before navigation, got %d", refCount)
		}

		gotoFixtureAndWaitLoad(t, mgr, "basic.html")
		if refCount := len(mgr.GetRefs()); refCount != 0 {
			t.Fatalf("refs should be cleared after navigation, got %d", refCount)
		}

		_, err := mgr.Execute("click", []string{submitRef})
		if err == nil {
			t.Fatalf("click stale ref after navigation: expected error")
		}
		assertContains(t, strings.ToLower(err.Error()), "not found")
		assertContains(t, strings.ToLower(err.Error()), "snapshot")
	})

	t.Run("ref_invalidation_after_dom_removal", func(t *testing.T) {
		gotoFixtureAndWaitLoad(t, mgr, "snapshot.html")
		interactive := mustExecCommand(t, mgr, "snapshot", "-i")
		submitRef := findRefInSnapshot(t, interactive, "button", "Submit")

		_ = mustExecCommand(t, mgr, "js", `document.getElementById("submit-btn").remove()`)

		_, err := mgr.Execute("click", []string{submitRef})
		if err == nil {
			t.Fatalf("click removed-node ref: expected stale error")
		}
		errLower := strings.ToLower(err.Error())
		if !strings.Contains(errLower, "stale") &&
			!strings.Contains(errLower, "detached") &&
			!strings.Contains(errLower, "not found") {
			t.Fatalf("removed-node ref should fail with stale/detached/not-found error, got %q", err)
		}
	})
}

func TestComputeUnifiedDiff(t *testing.T) {
	t.Run("identical_text_has_no_added_or_removed_lines", func(t *testing.T) {
		text := "line one\nline two"
		diff := computeUnifiedDiff(text, text)

		assertContains(t, diff, "--- previous snapshot")
		assertContains(t, diff, "+++ current snapshot")
		if strings.Contains(diff, "\n+ ") || strings.Contains(diff, "\n- ") {
			t.Fatalf("computeUnifiedDiff() should not include +/- lines for identical text, got:\n%s", diff)
		}
	})

	t.Run("added_line_is_marked_with_plus", func(t *testing.T) {
		diff := computeUnifiedDiff("a\nb", "a\nb\nc")
		if !strings.Contains(diff, "+ c") {
			t.Fatalf("computeUnifiedDiff() should mark added lines with '+', got:\n%s", diff)
		}
	})

	t.Run("removed_line_is_marked_with_minus", func(t *testing.T) {
		diff := computeUnifiedDiff("a\nb\nc", "a\nc")
		if !strings.Contains(diff, "- b") {
			t.Fatalf("computeUnifiedDiff() should mark removed lines with '-', got:\n%s", diff)
		}
	})

	t.Run("mixed_changes_include_add_and_remove_lines", func(t *testing.T) {
		oldText := "title\nold value\nkeep"
		newText := "title\nnew value\nkeep\ntail"
		diff := computeUnifiedDiff(oldText, newText)

		if !strings.Contains(diff, "- old value") {
			t.Fatalf("computeUnifiedDiff() missing removed line, got:\n%s", diff)
		}
		if !strings.Contains(diff, "+ new value") {
			t.Fatalf("computeUnifiedDiff() missing replaced added line, got:\n%s", diff)
		}
		if !strings.Contains(diff, "+ tail") {
			t.Fatalf("computeUnifiedDiff() missing appended added line, got:\n%s", diff)
		}
	})
}

func snapshotNonEmptyLines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func findCursorRefInSnapshot(t *testing.T, snapshot string) string {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, "@c") {
			continue
		}
		matches := testRefPattern.FindStringSubmatch(line)
		if len(matches) == 2 {
			return "@" + matches[1]
		}
	}
	t.Fatalf("failed to find cursor ref in snapshot:\n%s", snapshot)
	return ""
}

func gotoFixtureAndWaitLoad(t *testing.T, mgr *Manager, fixture string) {
	t.Helper()
	_ = mustExecCommand(t, mgr, "goto", fixtureURL(fixture))
	_ = mustExecCommand(t, mgr, "wait", "--load", "5000")
}
