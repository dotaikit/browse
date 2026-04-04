package browser

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
)

func TestCommandsMeta(t *testing.T) {
	mgr := sharedTestManager(t)

	t.Run("status_output", func(t *testing.T) {
		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("basic.html"))
		out := mustExecMetaCommand(t, mgr, "status")

		assertContainsMeta(t, out, "Status: healthy")
		assertContainsMeta(t, out, "URL:")
		assertContainsMeta(t, out, "/basic.html")
		assertContainsMeta(t, out, "Tabs:")
		assertContainsMeta(t, out, "Uptime:")
		assertContainsMeta(t, out, "PID:")
	})

	t.Run("chain_json_array", func(t *testing.T) {
		commands := [][]string{
			{"goto", fixtureURL("basic.html")},
			{"js", "document.title"},
		}
		payload, err := json.Marshal(commands)
		if err != nil {
			t.Fatalf("marshal chain commands: %v", err)
		}

		out := mustExecMetaCommand(t, mgr, "chain", string(payload))
		assertContainsMeta(t, out, "[goto]")
		assertContainsMeta(t, out, "[js] Test Page - Basic")
	})

	t.Run("chain_pipe_format", func(t *testing.T) {
		chainInput := "goto " + fixtureURL("forms.html") + " | js document.title"
		out := mustExecMetaCommand(t, mgr, "chain", chainInput)
		assertContainsMeta(t, out, "[goto]")
		assertContainsMeta(t, out, "[js] Test Page - Forms")
	})

	t.Run("chain_error_propagation", func(t *testing.T) {
		commands := [][]string{
			{"goto", fixtureURL("basic.html")},
			{"unknown-meta-command"},
		}
		payload, err := json.Marshal(commands)
		if err != nil {
			t.Fatalf("marshal chain commands: %v", err)
		}

		out := mustExecMetaCommand(t, mgr, "chain", string(payload))
		assertContainsMeta(t, out, "[unknown-meta-command] ERROR:")
		assertContainsMeta(t, out, "unknown command")
	})

	t.Run("diff_compare_two_pages", func(t *testing.T) {
		out := mustExecMetaCommand(t, mgr, "diff", fixtureURL("basic.html"), fixtureURL("forms.html"))
		assertContainsMeta(t, out, "---")
		assertContainsMeta(t, out, "+++")
		assertContainsMeta(t, out, "Hello World")
		assertContainsMeta(t, out, "Form Test Page")
	})

	t.Run("pdf_generate_and_validate", func(t *testing.T) {
		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("basic.html"))

		pdfPath := filepath.Join(t.TempDir(), "meta.pdf")
		out := mustExecMetaCommand(t, mgr, "pdf", pdfPath)
		assertContainsMeta(t, out, "PDF saved:")

		assertPDFFile(t, pdfPath)
	})

	t.Run("responsive_multi_viewport_screenshots", func(t *testing.T) {
		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("responsive.html"))

		prefix := filepath.Join(t.TempDir(), "resp")
		out := mustExecMetaCommand(t, mgr, "responsive", prefix)
		assertContainsMeta(t, out, "mobile (375x812)")
		assertContainsMeta(t, out, "tablet (768x1024)")
		assertContainsMeta(t, out, "desktop (1280x720)")

		assertPNGFile(t, prefix+"-mobile.png")
		assertPNGFile(t, prefix+"-tablet.png")
		assertPNGFile(t, prefix+"-desktop.png")
	})

	t.Run("state_save_load_round_trip", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("basic.html"))
		_ = mustExecMetaCommand(t, mgr, "cookie", "statecookie=initial")

		saveOut := mustExecMetaCommand(t, mgr, "state", "save", "meta_roundtrip")
		assertContainsMeta(t, saveOut, "State saved:")

		statePath := filepath.Join(homeDir, ".browse", "states", "meta_roundtrip.json")
		rawState, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatalf("read saved state %q: %v", statePath, err)
		}
		assertContainsMeta(t, string(rawState), `"version": 1`)

		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("forms.html"))
		loadOut := mustExecMetaCommand(t, mgr, "state", "load", "meta_roundtrip")
		assertContainsMeta(t, loadOut, "State loaded:")

		urlOut := mustExecMetaCommand(t, mgr, "url")
		assertContainsMeta(t, urlOut, "/basic.html")

		cookiesOut := mustExecMetaCommand(t, mgr, "cookies")
		assertContainsMeta(t, cookiesOut, "statecookie")
		assertContainsMeta(t, cookiesOut, "initial")
	})

	t.Run("handoff_output_and_message", func(t *testing.T) {
		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("basic.html"))

		out := mustExecMetaCommand(t, mgr, "handoff", "CAPTCHA", "stuck")
		assertContainsMeta(t, out, "Handoff mode: AI control paused.")
		assertContainsMeta(t, out, "URL:")
		assertContainsMeta(t, out, "/basic.html")
		assertContainsMeta(t, out, "Message: CAPTCHA stuck")
		assertContainsMeta(t, out, "Run 'browse resume' when ready.")
	})

	t.Run("resume_clears_refs_and_returns_snapshot", func(t *testing.T) {
		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("snapshot.html"))
		_ = mustExecMetaCommand(t, mgr, "snapshot", "-i")

		before := mgr.GetRefs()
		if len(before) == 0 {
			t.Fatalf("expected refs after snapshot, got none")
		}

		before["zz_stale"] = RefEntry{Role: "button", Name: "stale-ref"}
		mgr.SetRefs(before)
		mgr.setActiveFrame(cdp.FrameID("fake-frame"), runtime.ExecutionContextID(99))

		resumeOut := mustExecMetaCommand(t, mgr, "resume")
		assertContainsMeta(t, resumeOut, "@e")

		after := mgr.GetRefs()
		if len(after) == 0 {
			t.Fatalf("expected refs after resume snapshot, got none")
		}
		if _, exists := after["zz_stale"]; exists {
			t.Fatalf("stale ref key still present after resume")
		}
		if mgr.hasActiveFrame() {
			t.Fatalf("active frame should be cleared by resume")
		}
	})

	t.Run("path_traversal_style_paths", func(t *testing.T) {
		_ = mustExecMetaCommand(t, mgr, "goto", fixtureURL("basic.html"))

		tmpDir := t.TempDir()
		nestedDir := filepath.Join(tmpDir, "nested")
		if err := os.MkdirAll(nestedDir, 0755); err != nil {
			t.Fatalf("mkdir nested dir: %v", err)
		}

		screenshotArg := nestedDir + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "meta-shot.png"
		screenshotOut := mustExecMetaCommand(t, mgr, "screenshot", screenshotArg)
		assertContainsMeta(t, screenshotOut, "Screenshot saved")
		assertPNGFile(t, filepath.Clean(screenshotArg))

		pdfArg := nestedDir + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "meta.pdf"
		pdfOut := mustExecMetaCommand(t, mgr, "pdf", pdfArg)
		assertContainsMeta(t, pdfOut, "PDF saved:")
		assertPDFFile(t, filepath.Clean(pdfArg))

		prefixArg := nestedDir + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "meta-resp"
		responsiveOut := mustExecMetaCommand(t, mgr, "responsive", prefixArg)
		assertContainsMeta(t, responsiveOut, "mobile")
		assertContainsMeta(t, responsiveOut, "tablet")
		assertContainsMeta(t, responsiveOut, "desktop")
		assertPNGFile(t, filepath.Clean(prefixArg)+"-mobile.png")
		assertPNGFile(t, filepath.Clean(prefixArg)+"-tablet.png")
		assertPNGFile(t, filepath.Clean(prefixArg)+"-desktop.png")

		evalFile := filepath.Join(tmpDir, "eval.js")
		if err := os.WriteFile(evalFile, []byte("document.title"), 0644); err != nil {
			t.Fatalf("write eval file: %v", err)
		}
		evalArg := nestedDir + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "eval.js"
		evalOut := mustExecMetaCommand(t, mgr, "eval", evalArg)
		if evalOut != "Test Page - Basic" {
			t.Fatalf("eval output = %q, want %q", evalOut, "Test Page - Basic")
		}
	})
}

func mustExecMetaCommand(t *testing.T, mgr *Manager, command string, args ...string) string {
	t.Helper()
	out, err := mgr.Execute(command, args)
	if err != nil {
		t.Fatalf("%s %v: %v", command, args, err)
	}
	return out
}

func assertContainsMeta(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output %q does not contain %q", got, want)
	}
}

func assertPNGFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read PNG file %q: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("PNG file %q is empty", path)
	}

	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(data) < len(pngHeader) || !bytes.Equal(data[:len(pngHeader)], pngHeader) {
		t.Fatalf("file %q does not have PNG header", path)
	}
}

func assertPDFFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read PDF file %q: %v", path, err)
	}
	if len(data) < 4 {
		t.Fatalf("PDF file %q too small (%d bytes)", path, len(data))
	}
	if !bytes.Equal(data[:4], []byte("%PDF")) {
		t.Fatalf("file %q does not have PDF header", path)
	}
}

func TestTokenizeChainSegment(t *testing.T) {
	t.Run("basic_tokenization", func(t *testing.T) {
		got := tokenizeChainSegment("goto https://example.com")
		want := []string{"goto", "https://example.com"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tokenizeChainSegment() = %#v, want %#v", got, want)
		}
	})

	t.Run("quoted_spaces_preserved", func(t *testing.T) {
		got := tokenizeChainSegment(`fill "#email" "hello world"`)
		want := []string{"fill", "#email", "hello world"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tokenizeChainSegment() = %#v, want %#v", got, want)
		}
	})

	t.Run("empty_segment", func(t *testing.T) {
		got := tokenizeChainSegment("")
		if len(got) != 0 {
			t.Fatalf("tokenizeChainSegment(empty) length = %d, want 0", len(got))
		}
	})
}

func TestIsCookieURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "http_url", raw: "http://example.com", want: true},
		{name: "https_url", raw: "https://example.com/path", want: true},
		{name: "about_blank", raw: "about:blank", want: false},
		{name: "missing_host", raw: "https:///no-host", want: false},
		{name: "invalid_url", raw: "http://%zz", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isCookieURL(tc.raw)
			if got != tc.want {
				t.Fatalf("isCookieURL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestCookiesToParams(t *testing.T) {
	expiresAt := time.Unix(1893456789, 0).UTC()
	expiresSeconds := float64(expiresAt.Unix())
	partitionKey := &network.CookiePartitionKey{
		TopLevelSite:         "https://top.example",
		HasCrossSiteAncestor: true,
	}

	cookies := []*network.Cookie{
		nil,
		{
			Name: "",
		},
		{
			Name:         "session_cookie",
			Value:        "session_value",
			Domain:       "example.com",
			Path:         "/",
			Secure:       true,
			HTTPOnly:     true,
			Session:      true,
			SameSite:     network.CookieSameSiteLax,
			Priority:     network.CookiePriorityMedium,
			SourceScheme: network.CookieSourceSchemeSecure,
			SourcePort:   443,
			PartitionKey: partitionKey,
		},
		{
			Name:         "persistent_cookie",
			Value:        "persistent_value",
			Domain:       "example.com",
			Path:         "/prefs",
			Session:      false,
			Expires:      expiresSeconds,
			SameSite:     network.CookieSameSiteNone,
			Priority:     network.CookiePriorityHigh,
			SourceScheme: network.CookieSourceSchemeSecure,
			SourcePort:   443,
		},
	}

	got := cookiesToParams(cookies)
	if len(got) != 2 {
		t.Fatalf("cookiesToParams() returned %d params, want 2", len(got))
	}

	session := got[0]
	if session.Name != "session_cookie" {
		t.Fatalf("session cookie name = %q, want %q", session.Name, "session_cookie")
	}
	if session.Expires != nil {
		t.Fatalf("session cookie expires = %v, want nil", session.Expires)
	}
	if session.Domain != "example.com" || session.Path != "/" {
		t.Fatalf("session cookie domain/path = %q %q, want %q %q", session.Domain, session.Path, "example.com", "/")
	}
	if !session.Secure || !session.HTTPOnly {
		t.Fatalf("session cookie secure/httpOnly = %v/%v, want true/true", session.Secure, session.HTTPOnly)
	}
	if session.SameSite != network.CookieSameSiteLax {
		t.Fatalf("session cookie sameSite = %q, want %q", session.SameSite, network.CookieSameSiteLax)
	}
	if session.Priority != network.CookiePriorityMedium {
		t.Fatalf("session cookie priority = %q, want %q", session.Priority, network.CookiePriorityMedium)
	}
	if session.SourceScheme != network.CookieSourceSchemeSecure || session.SourcePort != 443 {
		t.Fatalf("session cookie source = %q/%d, want %q/%d", session.SourceScheme, session.SourcePort, network.CookieSourceSchemeSecure, 443)
	}
	if session.PartitionKey != partitionKey {
		t.Fatalf("session cookie partition key pointer mismatch")
	}

	persistent := got[1]
	if persistent.Name != "persistent_cookie" {
		t.Fatalf("persistent cookie name = %q, want %q", persistent.Name, "persistent_cookie")
	}
	if persistent.Expires == nil {
		t.Fatalf("persistent cookie expires = nil, want non-nil")
	}
	if !persistent.Expires.Time().Equal(expiresAt) {
		t.Fatalf("persistent cookie expires = %s, want %s", persistent.Expires.Time().Format(time.RFC3339), expiresAt.Format(time.RFC3339))
	}
}

func TestNormalizeStatePages(t *testing.T) {
	tests := []struct {
		name string
		raw  []statePage
		want []statePage
	}{
		{
			name: "empty_defaults_to_about_blank",
			raw:  nil,
			want: []statePage{{URL: "about:blank", IsActive: true}},
		},
		{
			name: "multiple_active_only_first_kept_and_empty_urls_filtered",
			raw: []statePage{
				{URL: " https://first.example ", IsActive: true},
				{URL: "", IsActive: false},
				{URL: "https://second.example", IsActive: true},
				{URL: "https://third.example", IsActive: false},
			},
			want: []statePage{
				{URL: "https://first.example", IsActive: true},
				{URL: "https://second.example", IsActive: false},
				{URL: "https://third.example", IsActive: false},
			},
		},
		{
			name: "no_active_marks_first_remaining_page_active",
			raw: []statePage{
				{URL: "", IsActive: false},
				{URL: "https://one.example", IsActive: false},
				{URL: " https://two.example ", IsActive: false},
			},
			want: []statePage{
				{URL: "https://one.example", IsActive: true},
				{URL: "https://two.example", IsActive: false},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeStatePages(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalizeStatePages() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
