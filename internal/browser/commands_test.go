package browser

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	testTabIDPattern = regexp.MustCompile(`Opened new tab ([^ ]+)`)
	testRefPattern   = regexp.MustCompile(`@(e[0-9]+|c[0-9]+)`)
)

func TestCommands(t *testing.T) {
	mgr := sharedTestManager(t)

	t.Run("Navigation", func(t *testing.T) {
		navMgr := newTestManager(t)

		out := mustExecCommand(t, navMgr, "goto", fixtureURL("basic.html"))
		assertContains(t, out, "Navigated to")

		urlOut := mustExecCommand(t, navMgr, "url")
		assertContains(t, urlOut, "/basic.html")

		_ = mustExecCommand(t, navMgr, "goto", fixtureURL("forms.html"))
		backOut, backErr := navMgr.Execute("back", nil)
		if backErr != nil {
			t.Logf("back failed (known Chrome issue with file:// history): %v", backErr)
		} else {
			assertContains(t, backOut, "Back")
			urlOut = mustExecCommand(t, navMgr, "url")
			if !strings.Contains(urlOut, "/basic.html") {
				t.Logf("back did not navigate to basic.html (got %q); skipping forward test (Chrome-specific)", urlOut)
				_ = mustExecCommand(t, navMgr, "goto", fixtureURL("forms.html"))
			} else {
				forwardOut := mustExecCommand(t, navMgr, "forward")
				assertContains(t, forwardOut, "Forward")
				urlOut = mustExecCommand(t, navMgr, "url")
				assertContains(t, urlOut, "/forms.html")
			}
		}

		reloadOut := mustExecCommand(t, navMgr, "reload")
		assertContains(t, reloadOut, "Reloaded")
	})

	t.Run("Content", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("basic.html"))

		textOut := mustExecCommand(t, mgr, "text")
		assertContains(t, textOut, "Hello World")
		assertContains(t, textOut, "Item one")
		if strings.Contains(textOut, "<h1>") {
			t.Fatalf("text output should not contain raw HTML tags, got %q", textOut)
		}

		htmlOut := mustExecCommand(t, mgr, "html")
		assertContains(t, htmlOut, "<html")
		assertContains(t, htmlOut, `<h1 id="title">Hello World</h1>`)

		linksOut := mustExecCommand(t, mgr, "links")
		assertContains(t, linksOut, "Page 1")
		assertContains(t, linksOut, "Page 2")
		assertContains(t, linksOut, "https://external.com/link")

		jsOut := mustExecCommand(t, mgr, "js", "document.title")
		if jsOut != "Test Page - Basic" {
			t.Fatalf("js document.title = %q, want %q", jsOut, "Test Page - Basic")
		}
	})

	t.Run("Interaction", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("forms.html"))

		fillOut := mustExecCommand(t, mgr, "fill", "#email", "test@example.com")
		assertContains(t, fillOut, "Filled")

		fillOut = mustExecCommand(t, mgr, "fill", "#password", "secret123")
		assertContains(t, fillOut, "Filled")

		typeOut := mustExecCommand(t, mgr, "type", "#name", "Jane Doe")
		assertContains(t, typeOut, "Typed")

		emailVal := mustExecCommand(t, mgr, "js", `document.querySelector("#email").value`)
		if emailVal != "test@example.com" {
			t.Fatalf("email value = %q, want %q", emailVal, "test@example.com")
		}

		nameVal := mustExecCommand(t, mgr, "js", `document.querySelector("#name").value`)
		if nameVal != "Jane Doe" {
			t.Fatalf("name value = %q, want %q", nameVal, "Jane Doe")
		}

		selectOut := mustExecCommand(t, mgr, "select", "#role", "admin")
		assertContains(t, strings.ToLower(selectOut), "selected")

		roleVal := mustExecCommand(t, mgr, "js", `document.querySelector("#role").value`)
		if roleVal != "admin" {
			t.Fatalf("role value = %q, want %q", roleVal, "admin")
		}

		clickOut := mustExecCommand(t, mgr, "click", "#profile-btn")
		assertContains(t, clickOut, "Clicked")

		waitOut := mustExecCommand(t, mgr, "wait", "#result")
		assertContains(t, waitOut, "is visible")

		resultText := mustExecCommand(t, mgr, "js", `document.querySelector("#result").textContent`)
		assertContains(t, resultText, "Form submitted!")

		_ = mustExecCommand(t, mgr, "wait", "25")

		snapshotOut := mustExecCommand(t, mgr, "snapshot", "-i")
		loginButtonRef := findRefInSnapshot(t, snapshotOut, "button", "Log In")

		hoverOut := mustExecCommand(t, mgr, "hover", loginButtonRef)
		assertContains(t, hoverOut, "Hovered")

		hoverSelectorOut := mustExecCommand(t, mgr, "hover", "#profile-btn")
		assertContains(t, hoverSelectorOut, "Hovered")

		scrollOut := mustExecCommand(t, mgr, "scroll", "down", "200")
		assertContains(t, scrollOut, "Scrolled down 200px")

		_ = mustExecCommand(t, mgr, "click", "#email")
		pressOut := mustExecCommand(t, mgr, "press", "Tab")
		assertContains(t, pressOut, "Pressed Tab")

		focusID := mustExecCommand(t, mgr, "js", "document.activeElement.id")
		if focusID != "password" {
			t.Fatalf("active element id = %q, want %q after Tab", focusID, "password")
		}
	})

	t.Run("Tabs", func(t *testing.T) {
		tabsOut := mustExecCommand(t, mgr, "tabs")
		assertContains(t, tabsOut, "[")

		firstNewTabOut := mustExecCommand(t, mgr, "newtab", fixtureURL("forms.html"))
		assertContains(t, firstNewTabOut, "Opened new tab")
		firstTargetID := extractTargetID(t, firstNewTabOut)

		secondNewTabOut := mustExecCommand(t, mgr, "newtab", fixtureURL("basic.html"))
		assertContains(t, secondNewTabOut, "Opened new tab")
		secondTargetID := extractTargetID(t, secondNewTabOut)

		cleanupTargets := []string{firstTargetID, secondTargetID}
		t.Cleanup(func() {
			for _, targetID := range cleanupTargets {
				if targetID == "" {
					continue
				}
				_, _ = mgr.Execute("closetab", []string{targetID})
			}
		})

		tabsOut = mustExecCommand(t, mgr, "tabs")
		assertContains(t, tabsOut, "/forms.html")
		assertContains(t, tabsOut, "/basic.html")

		tabOut := mustExecCommand(t, mgr, "tab", "0")
		assertContains(t, tabOut, "Switched to tab [0]")

		closeOut, closeErr := mgr.Execute("closetab", []string{firstTargetID})
		if closeErr == nil {
			assertContains(t, closeOut, "Closed tab")
		} else {
			assertContains(t, closeErr.Error(), "to close the target")
		}
	})

	t.Run("Screenshot", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("basic.html"))

		outPath := filepath.Join(t.TempDir(), "commands-screenshot.png")
		ssOut := mustExecCommand(t, mgr, "screenshot", outPath)
		assertContains(t, ssOut, "Screenshot saved to")

		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read screenshot %q: %v", outPath, err)
		}
		if len(data) == 0 {
			t.Fatalf("screenshot %q is empty", outPath)
		}

		pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
		if len(data) < len(pngHeader) || !bytes.Equal(data[:len(pngHeader)], pngHeader) {
			t.Fatalf("screenshot %q is not a PNG file", outPath)
		}
	})

	t.Run("EmptyPage", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("empty.html"))

		textOut := mustExecCommand(t, mgr, "text")
		if strings.TrimSpace(textOut) != "" {
			t.Fatalf("text on empty page = %q, want empty", textOut)
		}

		linksOut := mustExecCommand(t, mgr, "links")
		if linksOut != "(no links)" {
			t.Fatalf("links on empty page = %q, want %q", linksOut, "(no links)")
		}
	})

	t.Run("Errors", func(t *testing.T) {
		t.Run("MissingArgs", func(t *testing.T) {
			testCases := []struct {
				command string
				args    []string
			}{
				{command: "goto"},
				{command: "click"},
				{command: "fill", args: []string{"#email"}},
				{command: "select", args: []string{"#role"}},
				{command: "hover"},
				{command: "type", args: []string{"#name"}},
				{command: "wait"},
				{command: "press"},
			}

			for _, tc := range testCases {
				tc := tc
				t.Run(tc.command, func(t *testing.T) {
					_, err := mgr.Execute(tc.command, tc.args)
					if err == nil {
						t.Fatalf("%s %v: expected error", tc.command, tc.args)
					}
					assertContains(t, strings.ToLower(err.Error()), "usage:")
				})
			}
		})

		t.Run("InvalidSelectors", func(t *testing.T) {
			if _, err := mgr.Execute("click", []string{"#[invalid"}); err == nil {
				t.Fatalf("click with invalid selector: expected error")
			}

			if _, err := mgr.Execute("fill", []string{"#[invalid", "value"}); err == nil {
				t.Fatalf("fill with invalid selector: expected error")
			}
		})

		t.Run("UnknownCommand", func(t *testing.T) {
			_, err := mgr.Execute("not-a-real-command", nil)
			if err == nil {
				t.Fatalf("unknown command: expected error")
			}
			assertContains(t, err.Error(), "unknown command")
		})
	})
}

func mustExecCommand(t *testing.T, mgr *Manager, command string, args ...string) string {
	t.Helper()
	out, err := mgr.Execute(command, args)
	if err != nil {
		t.Fatalf("%s %v: %v", command, args, err)
	}
	return out
}

func execCommandWithTimeout(mgr *Manager, timeout time.Duration, command string, args ...string) (string, error) {
	baseCtx := mgr.Ctx()
	timeoutCtx, timeoutCancel := context.WithTimeout(baseCtx, timeout)
	defer timeoutCancel()

	origCtx := mgr.ctx
	mgr.ctx = timeoutCtx
	defer func() {
		mgr.ctx = origCtx
	}()

	return mgr.Execute(command, args)
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output %q does not contain %q", got, want)
	}
}

func findRefInSnapshot(t *testing.T, snapshot string, role string, name string) string {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, role) || !strings.Contains(line, `"`+name+`"`) {
			continue
		}
		matches := testRefPattern.FindStringSubmatch(line)
		if len(matches) == 2 {
			return "@" + matches[1]
		}
	}
	t.Fatalf("failed to find %s %q ref in snapshot:\n%s", role, name, snapshot)
	return ""
}

func extractTargetID(t *testing.T, output string) string {
	t.Helper()
	matches := testTabIDPattern.FindStringSubmatch(output)
	if len(matches) != 2 {
		t.Fatalf("failed to parse tab id from output %q", output)
	}
	return matches[1]
}

func TestParseScreenshotClip(t *testing.T) {
	t.Run("ValidClip", func(t *testing.T) {
		clip, err := parseScreenshotClip("0,0,500,300")
		if err != nil {
			t.Fatalf("parseScreenshotClip returned error: %v", err)
		}
		if clip.X != 0 || clip.Y != 0 || clip.Width != 500 || clip.Height != 300 || clip.Scale != 1 {
			t.Fatalf("unexpected clip parsed: %+v", clip)
		}
	})

	t.Run("InvalidFormat", func(t *testing.T) {
		_, err := parseScreenshotClip("0,0,500")
		if err == nil {
			t.Fatal("expected error for malformed clip string")
		}
		assertContains(t, err.Error(), "expected x,y,w,h")
	})

	t.Run("NonPositiveDimensions", func(t *testing.T) {
		testCases := []string{
			"0,0,0,300",
			"0,0,500,-1",
		}

		for _, raw := range testCases {
			raw := raw
			t.Run(raw, func(t *testing.T) {
				_, err := parseScreenshotClip(raw)
				if err == nil {
					t.Fatalf("expected error for clip %q", raw)
				}
				assertContains(t, err.Error(), "greater than 0")
			})
		}
	})
}

func TestParseOptionalWaitTimeout(t *testing.T) {
	t.Run("NoArgsUsesDefault", func(t *testing.T) {
		defaultTimeout := 3 * time.Second
		got, err := parseOptionalWaitTimeout(nil, defaultTimeout)
		if err != nil {
			t.Fatalf("parseOptionalWaitTimeout returned error: %v", err)
		}
		if got != defaultTimeout {
			t.Fatalf("timeout = %s, want %s", got, defaultTimeout)
		}
	})

	t.Run("ValidMilliseconds", func(t *testing.T) {
		got, err := parseOptionalWaitTimeout([]string{"1500"}, 3*time.Second)
		if err != nil {
			t.Fatalf("parseOptionalWaitTimeout returned error: %v", err)
		}
		if got != 1500*time.Millisecond {
			t.Fatalf("timeout = %s, want %s", got, 1500*time.Millisecond)
		}
	})

	t.Run("InvalidValue", func(t *testing.T) {
		_, err := parseOptionalWaitTimeout([]string{"soon"}, 3*time.Second)
		if err == nil {
			t.Fatal("expected error for non-numeric timeout")
		}
		assertContains(t, err.Error(), "invalid timeout")
	})

	t.Run("NegativeValue", func(t *testing.T) {
		_, err := parseOptionalWaitTimeout([]string{"-250"}, 3*time.Second)
		if err == nil {
			t.Fatal("expected error for negative timeout")
		}
		assertContains(t, err.Error(), "invalid timeout")
	})
}

func TestIsTargetNavigatedErr(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Nil",
			err:  nil,
			want: false,
		},
		{
			name: "InspectedTargetNavigated",
			err:  errors.New("Inspected target navigated or closed"),
			want: true,
		},
		{
			name: "TargetClosed",
			err:  errors.New("Target closed while navigating"),
			want: true,
		},
		{
			name: "ExecutionContextDestroyed",
			err:  errors.New("Execution context was destroyed, most likely because of a navigation."),
			want: true,
		},
		{
			name: "OrdinaryError",
			err:  errors.New("some unrelated failure"),
			want: false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isTargetNavigatedErr(tc.err)
			if got != tc.want {
				t.Fatalf("isTargetNavigatedErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestNeedsBlockWrapper(t *testing.T) {
	testCases := []struct {
		name string
		code string
		want bool
	}{
		{
			name: "Multiline",
			code: "foo()\nbar()",
			want: true,
		},
		{
			name: "Declaration",
			code: "const value = 1",
			want: true,
		},
		{
			name: "Semicolon",
			code: "doWork();",
			want: true,
		},
		{
			name: "SingleExpression",
			code: "user.name",
			want: false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := needsBlockWrapper(tc.code)
			if got != tc.want {
				t.Fatalf("needsBlockWrapper(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

func TestWrapForEvaluate(t *testing.T) {
	t.Run("NoAwaitReturnsOriginal", func(t *testing.T) {
		code := "document.title"
		got := wrapForEvaluate(code)
		if got != code {
			t.Fatalf("wrapForEvaluate(%q) = %q, want unchanged", code, got)
		}
	})

	t.Run("AwaitExpressionUsesParenWrapper", func(t *testing.T) {
		code := "await fetch('/api/status')"
		got := wrapForEvaluate(code)
		want := "(async()=>(await fetch('/api/status')))()"
		if got != want {
			t.Fatalf("wrapForEvaluate(%q) = %q, want %q", code, got, want)
		}
	})

	t.Run("AwaitMultilineUsesBlockWrapper", func(t *testing.T) {
		code := "const res = await fetch('/api/status');\nreturn res.status"
		got := wrapForEvaluate(code)
		want := "(async()=>{\nconst res = await fetch('/api/status');\nreturn res.status\n})()"
		if got != want {
			t.Fatalf("wrapForEvaluate multiline = %q, want %q", got, want)
		}
	})
}
