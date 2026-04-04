package browser

import (
	"strings"
	"testing"
	"time"
)

func TestMonitoring(t *testing.T) {
	mgr := sharedTestManager(t)

	t.Run("console_capture_clear_and_errors", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "console", "--clear")
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("spa.html"))
		_ = mustExecCommand(t, mgr, "wait", ".loaded")

		consoleOut := mustExecCommand(t, mgr, "console")
		assertContains(t, consoleOut, "[SPA] Starting render")
		assertContains(t, consoleOut, "[SPA] Render complete")

		errorsOut := mustExecCommand(t, mgr, "console", "--errors")
		assertContains(t, errorsOut, "[SPA] This is a warning")
		assertContains(t, errorsOut, "[SPA] This is an error")
		if strings.Contains(errorsOut, "[SPA] Starting render") {
			t.Fatalf("console --errors should filter log level output, got %q", errorsOut)
		}

		clearOut := mustExecCommand(t, mgr, "console", "--clear")
		assertContains(t, strings.ToLower(clearOut), "cleared")

		emptyOut := mustExecCommand(t, mgr, "console")
		if !strings.Contains(strings.ToLower(emptyOut), "no console") {
			t.Fatalf("console after clear = %q, want no-console message", emptyOut)
		}
	})

	t.Run("network_capture_clear_and_errors", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "network", "--clear")
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("network-idle.html"))
		_ = mustExecCommand(t, mgr, "wait", "--load", "5000")

		networkOut := mustExecCommand(t, mgr, "network")
		assertContains(t, networkOut, "GET")
		assertContains(t, networkOut, "/network-idle.html")

		_ = mustExecCommand(t, mgr, "click", "#fetch-btn")
		echoResult := mustExecCommand(t, mgr, "js", `document.getElementById("result").textContent`)
		assertContains(t, echoResult, "Data loaded")

		networkOut = mustExecCommand(t, mgr, "network")
		assertContains(t, networkOut, "/echo")

		clearOut := mustExecCommand(t, mgr, "network", "--clear")
		assertContains(t, strings.ToLower(clearOut), "cleared")

		emptyOut := mustExecCommand(t, mgr, "network")
		if !strings.Contains(strings.ToLower(emptyOut), "no network") {
			t.Fatalf("network after clear = %q, want no-network message", emptyOut)
		}

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("network-idle.html"))
		_ = mustExecCommand(t, mgr, "network", "--clear")
		_ = mustExecCommand(t, mgr, "js", `(function() { fetch('/missing-network-resource'); return 'requested'; })()`)
		_ = mustExecCommand(t, mgr, "wait", "--networkidle", "5000")

		errorOut := mustExecCommand(t, mgr, "network", "--errors")
		assertContains(t, errorOut, "404")
		assertContains(t, errorOut, "missing-network-resource")
	})

	t.Run("dialog_capture_and_clear", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "dialog", "--clear")

		restore := disableDialogAutoHandler(mgr)
		defer restore()

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("dialog.html"))
		handleDialogManually(mgr, true, "", 200*time.Millisecond)
		_ = mustExecCommand(t, mgr, "js", `setTimeout(() => document.getElementById('alert-btn').click(), 50)`)
		time.Sleep(1 * time.Second)

		dialogOut := mustExecCommand(t, mgr, "dialog")
		assertContains(t, dialogOut, "alert")
		assertContains(t, dialogOut, "Hello from alert")

		clearOut := mustExecCommand(t, mgr, "dialog", "--clear")
		assertContains(t, strings.ToLower(clearOut), "cleared")

		emptyOut := mustExecCommand(t, mgr, "dialog")
		if !strings.Contains(strings.ToLower(emptyOut), "no dialog") {
			t.Fatalf("dialog after clear = %q, want no-dialog message", emptyOut)
		}
	})
}

func TestFormatConsoleEntry(t *testing.T) {
	timestamp := int64(1700000000123)
	ts := time.UnixMilli(timestamp).Format(time.RFC3339)

	t.Run("severity_levels_are_formatted", func(t *testing.T) {
		testCases := []struct {
			level string
			text  string
		}{
			{level: "log", text: "hello"},
			{level: "warning", text: "careful"},
			{level: "error", text: "boom"},
		}

		for _, tc := range testCases {
			tc := tc
			t.Run(tc.level, func(t *testing.T) {
				got := formatConsoleEntry(ConsoleEntry{
					Timestamp: timestamp,
					Level:     tc.level,
					Text:      tc.text,
				})
				want := "[" + ts + "] " + tc.level + ": " + tc.text
				if got != want {
					t.Fatalf("formatConsoleEntry() = %q, want %q", got, want)
				}
			})
		}
	})

	t.Run("multi_arg_text_is_preserved", func(t *testing.T) {
		joined := strings.Join([]string{"first", "42", "true"}, " ")
		got := formatConsoleEntry(ConsoleEntry{
			Timestamp: timestamp,
			Level:     "info",
			Text:      joined,
		})

		if !strings.Contains(got, ": "+joined) {
			t.Fatalf("formatConsoleEntry() should keep joined text %q, got %q", joined, got)
		}
	})
}

func TestFormatNetworkEntry(t *testing.T) {
	timestamp := int64(1700000000456)
	ts := time.UnixMilli(timestamp).Format(time.RFC3339)

	t.Run("completed_request_includes_status_size_and_duration", func(t *testing.T) {
		got := formatNetworkEntry(NetworkEntry{
			Timestamp: timestamp,
			Method:    "GET",
			URL:       "https://example.com/api",
			Status:    201,
			Duration:  45,
			Size:      1234,
		})

		if !strings.Contains(got, "["+ts+"] GET https://example.com/api") {
			t.Fatalf("formatNetworkEntry() missing prefix details, got %q", got)
		}
		if !strings.Contains(got, " 201 (45ms 1234B)") {
			t.Fatalf("formatNetworkEntry() should include status/duration/size, got %q", got)
		}
		if strings.Contains(got, "(pending)") {
			t.Fatalf("formatNetworkEntry() should not mark completed request as pending, got %q", got)
		}
	})

	t.Run("pending_request_has_no_status", func(t *testing.T) {
		got := formatNetworkEntry(NetworkEntry{
			Timestamp: timestamp,
			Method:    "POST",
			URL:       "https://example.com/upload",
		})
		want := "[" + ts + "] POST https://example.com/upload (pending)"
		if got != want {
			t.Fatalf("formatNetworkEntry() = %q, want %q", got, want)
		}
		if strings.Contains(got, "ms") || strings.Contains(got, "B)") {
			t.Fatalf("formatNetworkEntry() pending output should not contain duration/size, got %q", got)
		}
	})
}

func TestFormatDialogEntry(t *testing.T) {
	timestamp := int64(1700000000789)
	ts := time.UnixMilli(timestamp).Format(time.RFC3339)

	testCases := []struct {
		name        string
		entry       DialogEntry
		wantContain []string
	}{
		{
			name: "alert_accepted",
			entry: DialogEntry{
				Timestamp: timestamp,
				Type:      "alert",
				Message:   "Heads up",
				Action:    "accepted",
			},
			wantContain: []string{
				"[" + ts + "] alert: \"Heads up\"",
				"accepted",
			},
		},
		{
			name: "confirm_dismissed",
			entry: DialogEntry{
				Timestamp: timestamp,
				Type:      "confirm",
				Message:   "Delete item?",
				Action:    "dismissed",
			},
			wantContain: []string{
				"[" + ts + "] confirm: \"Delete item?\"",
				"dismissed",
			},
		},
		{
			name: "prompt_with_response",
			entry: DialogEntry{
				Timestamp: timestamp,
				Type:      "prompt",
				Message:   "Name",
				Action:    "accepted",
				Response:  "alice",
			},
			wantContain: []string{
				"[" + ts + "] prompt: \"Name\"",
				"accepted",
				"(response: \"alice\")",
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := formatDialogEntry(tc.entry)
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Fatalf("formatDialogEntry() = %q, want to contain %q", got, want)
				}
			}
		})
	}
}
