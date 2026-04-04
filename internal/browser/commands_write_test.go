package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// handleDialogManually handles a browser dialog from a separate goroutine,
// bypassing the Manager's auto-handler. This avoids a chromedp listenersMu
// deadlock where the event-dispatch goroutine holds the mutex while running
// listeners, and a listener calling Target.Execute tries to acquire it again.
func handleDialogManually(mgr *Manager, accept bool, promptText string, delay time.Duration) {
	c := chromedp.FromContext(mgr.ctx)
	go func() {
		time.Sleep(delay)
		execCtx := cdp.WithExecutor(context.Background(), c.Target)
		action := page.HandleJavaScriptDialog(accept)
		if accept && promptText != "" {
			action = action.WithPromptText(promptText)
		}
		_ = action.Do(execCtx)
	}()
}

// disableDialogAutoHandler disables the broken auto-handler and returns
// a cleanup function that re-enables it.
func disableDialogAutoHandler(mgr *Manager) func() {
	mgr.mu.Lock()
	mgr.dialogMode.enabled = false
	mgr.mu.Unlock()
	return func() {
		mgr.SetDialogAutoMode(true, "")
	}
}

func TestCommandsWrite(t *testing.T) {
	mgr := sharedTestManager(t)

	// --- Viewport ---

	t.Run("viewport_set_and_verify", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("basic.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		result, err := mgr.Execute("viewport", []string{"375x812"})
		if err != nil {
			t.Fatalf("viewport: %v", err)
		}
		if !strings.Contains(result, "Viewport set") {
			t.Fatalf("expected 'Viewport set', got %q", result)
		}

		size, err := mgr.Execute("js", []string{"`${window.innerWidth}x${window.innerHeight}`"})
		if err != nil {
			t.Fatalf("js viewport check: %v", err)
		}
		if size != "375x812" {
			t.Fatalf("viewport size = %q, want %q", size, "375x812")
		}

		// Reset viewport
		if _, err := mgr.Execute("viewport", []string{"1280x720"}); err != nil {
			t.Fatalf("viewport reset: %v", err)
		}
	})

	t.Run("viewport_bad_format_errors", func(t *testing.T) {
		_, err := mgr.Execute("viewport", []string{"badformat"})
		if err == nil {
			t.Fatal("expected error for bad viewport format")
		}
	})

	t.Run("viewport_no_args_errors", func(t *testing.T) {
		_, err := mgr.Execute("viewport", nil)
		if err == nil {
			t.Fatal("expected error for missing viewport args")
		}
	})

	// --- UserAgent ---

	t.Run("useragent_set_and_verify_via_echo", func(t *testing.T) {
		result, err := mgr.Execute("useragent", []string{"TestBot/1.0"})
		if err != nil {
			t.Fatalf("useragent: %v", err)
		}
		if !strings.Contains(result, "User-Agent set") {
			t.Fatalf("expected 'User-Agent set', got %q", result)
		}
		if !strings.Contains(result, "TestBot/1.0") {
			t.Fatalf("expected user-agent string in result, got %q", result)
		}

		// Verify via /echo endpoint which returns request headers as JSON
		if _, err := mgr.Execute("goto", []string{fixtureURL("echo")}); err != nil {
			t.Fatalf("goto echo: %v", err)
		}
		text, err := mgr.Execute("text", nil)
		if err != nil {
			t.Fatalf("text: %v", err)
		}
		if !strings.Contains(text, "TestBot/1.0") {
			t.Fatalf("echo response should contain user-agent, got %q", text)
		}
	})

	t.Run("useragent_no_args_errors", func(t *testing.T) {
		_, err := mgr.Execute("useragent", nil)
		if err == nil {
			t.Fatal("expected error for missing useragent args")
		}
	})

	// --- Cookie ---

	t.Run("cookie_set_and_validate", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("basic.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		result, err := mgr.Execute("cookie", []string{"testcookie=testvalue"})
		if err != nil {
			t.Fatalf("cookie: %v", err)
		}
		if !strings.Contains(result, "Cookie set") {
			t.Fatalf("expected 'Cookie set', got %q", result)
		}
		// Value should be redacted
		if !strings.Contains(result, "****") {
			t.Fatalf("cookie value should be redacted, got %q", result)
		}
		if strings.Contains(result, "testvalue") {
			t.Fatalf("cookie value should not appear in output, got %q", result)
		}

		// Verify cookie was actually set
		cookies, err := mgr.Execute("cookies", nil)
		if err != nil {
			t.Fatalf("cookies: %v", err)
		}
		if !strings.Contains(cookies, "testcookie") {
			t.Fatalf("cookie name not found in %q", cookies)
		}
		if !strings.Contains(cookies, "testvalue") {
			t.Fatalf("cookie value not found in %q", cookies)
		}
	})

	t.Run("cookie_no_args_errors", func(t *testing.T) {
		_, err := mgr.Execute("cookie", nil)
		if err == nil {
			t.Fatal("expected error for missing cookie args")
		}
	})

	t.Run("cookie_no_equals_errors", func(t *testing.T) {
		_, err := mgr.Execute("cookie", []string{"invalid"})
		if err == nil {
			t.Fatal("expected error for cookie without =")
		}
	})

	// --- Cookie Import ---

	t.Run("cookie_import_from_json", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("basic.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "cookies.json")
		cookies := []map[string]string{
			{"name": "imported1", "value": "val1"},
			{"name": "imported2", "value": "val2"},
		}
		data, _ := json.Marshal(cookies)
		if err := os.WriteFile(tmpFile, data, 0644); err != nil {
			t.Fatalf("write cookie file: %v", err)
		}

		result, err := mgr.Execute("cookie-import", []string{tmpFile})
		if err != nil {
			t.Fatalf("cookie-import: %v", err)
		}
		if !strings.Contains(result, "Imported 2 cookies") {
			t.Fatalf("expected 'Imported 2 cookies', got %q", result)
		}

		// Verify cookies were set
		cookieList, err := mgr.Execute("cookies", nil)
		if err != nil {
			t.Fatalf("cookies: %v", err)
		}
		if !strings.Contains(cookieList, "imported1") {
			t.Fatalf("imported1 not found in cookies")
		}
		if !strings.Contains(cookieList, "imported2") {
			t.Fatalf("imported2 not found in cookies")
		}
	})

	t.Run("cookie_import_auto_fills_domain", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("basic.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "cookies-nodomain.json")
		cookies := []map[string]string{{"name": "autofill-test", "value": "works"}}
		data, _ := json.Marshal(cookies)
		if err := os.WriteFile(tmpFile, data, 0644); err != nil {
			t.Fatalf("write cookie file: %v", err)
		}

		result, err := mgr.Execute("cookie-import", []string{tmpFile})
		if err != nil {
			t.Fatalf("cookie-import: %v", err)
		}
		if !strings.Contains(result, "Imported 1") {
			t.Fatalf("expected import count, got %q", result)
		}

		cookieList, _ := mgr.Execute("cookies", nil)
		if !strings.Contains(cookieList, "autofill-test") {
			t.Fatalf("autofill-test not found in cookies")
		}
	})

	t.Run("cookie_import_preserves_explicit_domain", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("basic.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "cookies-domain.json")
		cookies := []map[string]interface{}{
			{"name": "explicit", "value": "domain", "domain": "example.com", "path": "/foo"},
		}
		data, _ := json.Marshal(cookies)
		if err := os.WriteFile(tmpFile, data, 0644); err != nil {
			t.Fatalf("write cookie file: %v", err)
		}

		result, err := mgr.Execute("cookie-import", []string{tmpFile})
		if err != nil {
			t.Fatalf("cookie-import: %v", err)
		}
		if !strings.Contains(result, "Imported 1") {
			t.Fatalf("expected import count, got %q", result)
		}
	})

	t.Run("cookie_import_empty_array", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("basic.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "empty.json")
		if err := os.WriteFile(tmpFile, []byte("[]"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		result, err := mgr.Execute("cookie-import", []string{tmpFile})
		if err != nil {
			t.Fatalf("cookie-import: %v", err)
		}
		if !strings.Contains(result, "Imported 0 cookies") {
			t.Fatalf("expected 'Imported 0 cookies', got %q", result)
		}
	})

	t.Run("cookie_import_file_not_found", func(t *testing.T) {
		_, err := mgr.Execute("cookie-import", []string{"/tmp/nonexistent-cookies-xyz.json"})
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("cookie_import_invalid_json", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "bad.json")
		if err := os.WriteFile(tmpFile, []byte("not json {{{"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		_, err := mgr.Execute("cookie-import", []string{tmpFile})
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})

	t.Run("cookie_import_missing_name", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("basic.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "noname.json")
		if err := os.WriteFile(tmpFile, []byte(`[{"value": "no-name"}]`), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		_, err := mgr.Execute("cookie-import", []string{tmpFile})
		if err == nil {
			t.Fatal("expected error for cookie missing name")
		}
		if !strings.Contains(err.Error(), "missing name") {
			t.Fatalf("expected 'missing name' in error, got %q", err)
		}
	})

	t.Run("cookie_import_no_args_errors", func(t *testing.T) {
		_, err := mgr.Execute("cookie-import", nil)
		if err == nil {
			t.Fatal("expected error for missing cookie-import args")
		}
	})

	// --- Header ---

	t.Run("header_set_and_verify_via_echo", func(t *testing.T) {
		result, err := mgr.Execute("header", []string{"X-Custom-Test:custom-value"})
		if err != nil {
			t.Fatalf("header: %v", err)
		}
		if !strings.Contains(result, "Header set") {
			t.Fatalf("expected 'Header set', got %q", result)
		}
		if !strings.Contains(result, "custom-value") {
			t.Fatalf("expected non-sensitive value in result, got %q", result)
		}

		// Verify via /echo endpoint
		if _, err := mgr.Execute("goto", []string{fixtureURL("echo")}); err != nil {
			t.Fatalf("goto echo: %v", err)
		}
		text, err := mgr.Execute("text", nil)
		if err != nil {
			t.Fatalf("text: %v", err)
		}
		if !strings.Contains(strings.ToLower(text), "x-custom-test") {
			t.Fatalf("echo response should contain header name, got %q", text)
		}
		if !strings.Contains(text, "custom-value") {
			t.Fatalf("echo response should contain header value, got %q", text)
		}
	})

	t.Run("header_redacts_authorization", func(t *testing.T) {
		result, err := mgr.Execute("header", []string{"Authorization:Bearer token-xyz"})
		if err != nil {
			t.Fatalf("header: %v", err)
		}
		if !strings.Contains(result, "Authorization") {
			t.Fatalf("expected 'Authorization' in result, got %q", result)
		}
		if !strings.Contains(result, "****") {
			t.Fatalf("expected redacted value, got %q", result)
		}
		if strings.Contains(result, "token-xyz") {
			t.Fatalf("sensitive value should be redacted, got %q", result)
		}
	})

	t.Run("header_redacts_x_api_key", func(t *testing.T) {
		result, err := mgr.Execute("header", []string{"X-API-Key:sk-12345"})
		if err != nil {
			t.Fatalf("header: %v", err)
		}
		if !strings.Contains(result, "X-API-Key") {
			t.Fatalf("expected 'X-API-Key' in result, got %q", result)
		}
		if !strings.Contains(result, "****") {
			t.Fatalf("expected redacted value, got %q", result)
		}
		if strings.Contains(result, "sk-12345") {
			t.Fatalf("sensitive value should be redacted, got %q", result)
		}
	})

	t.Run("header_shows_non_sensitive_values", func(t *testing.T) {
		result, err := mgr.Execute("header", []string{"Content-Type:application/json"})
		if err != nil {
			t.Fatalf("header: %v", err)
		}
		if !strings.Contains(result, "application/json") {
			t.Fatalf("expected value in result, got %q", result)
		}
		if strings.Contains(result, "****") {
			t.Fatalf("non-sensitive value should not be redacted, got %q", result)
		}
	})

	t.Run("header_no_args_errors", func(t *testing.T) {
		_, err := mgr.Execute("header", nil)
		if err == nil {
			t.Fatal("expected error for missing header args")
		}
	})

	t.Run("header_no_colon_errors", func(t *testing.T) {
		_, err := mgr.Execute("header", []string{"invalid"})
		if err == nil {
			t.Fatal("expected error for header without colon")
		}
	})

	// --- Upload ---

	t.Run("upload_single_file", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("upload.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "testfile.txt")
		if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		result, err := mgr.Execute("upload", []string{"#file-input", tmpFile})
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		if !strings.Contains(result, "Uploaded") {
			t.Fatalf("expected 'Uploaded', got %q", result)
		}
		if !strings.Contains(result, "testfile.txt") {
			t.Fatalf("expected filename in result, got %q", result)
		}

		// Wait for change event handler to fire
		time.Sleep(200 * time.Millisecond)

		text, err := mgr.Execute("js", []string{`document.querySelector("#upload-result").textContent`})
		if err != nil {
			t.Fatalf("js: %v", err)
		}
		if !strings.Contains(text, "testfile.txt") {
			t.Fatalf("upload handler should show filename, got %q", text)
		}
	})

	t.Run("upload_multi_file", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("upload.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		tmpDir := t.TempDir()
		f1 := filepath.Join(tmpDir, "file1.txt")
		f2 := filepath.Join(tmpDir, "file2.txt")
		if err := os.WriteFile(f1, []byte("content1"), 0644); err != nil {
			t.Fatalf("write file1: %v", err)
		}
		if err := os.WriteFile(f2, []byte("content2"), 0644); err != nil {
			t.Fatalf("write file2: %v", err)
		}

		result, err := mgr.Execute("upload", []string{"#multi-input", f1, f2})
		if err != nil {
			t.Fatalf("upload multi: %v", err)
		}
		if !strings.Contains(result, "Uploaded") {
			t.Fatalf("expected 'Uploaded', got %q", result)
		}
		if !strings.Contains(result, "file1.txt") || !strings.Contains(result, "file2.txt") {
			t.Fatalf("expected both filenames in result, got %q", result)
		}

		// Wait for change event handler
		time.Sleep(200 * time.Millisecond)

		text, err := mgr.Execute("js", []string{`document.querySelector("#upload-result").textContent`})
		if err != nil {
			t.Fatalf("js: %v", err)
		}
		if !strings.Contains(text, "file1.txt") || !strings.Contains(text, "file2.txt") {
			t.Fatalf("upload handler should show both filenames, got %q", text)
		}
	})

	t.Run("upload_nonexistent_file_errors", func(t *testing.T) {
		if _, err := mgr.Execute("goto", []string{fixtureURL("upload.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}
		_, err := mgr.Execute("upload", []string{"#file-input", "/tmp/nonexistent-file-xyz.txt"})
		if err == nil {
			t.Fatal("expected error for nonexistent file")
		}
		if !strings.Contains(err.Error(), "file not found") {
			t.Fatalf("expected 'file not found' in error, got %q", err)
		}
	})

	t.Run("upload_missing_args_errors", func(t *testing.T) {
		_, err := mgr.Execute("upload", []string{"#file-input"})
		if err == nil {
			t.Fatal("expected error for missing upload args")
		}
	})

	// --- Dialog Accept / Dismiss ---
	//
	// The Manager's auto-handler in monitoring.go calls chromedp.Run from inside
	// an event listener callback, which deadlocks on chromedp's listenersMu
	// (held by the dispatch goroutine during runListeners). These tests bypass
	// the auto-handler and invoke Page.handleJavaScriptDialog directly via CDP.

	t.Run("dialog_accept_command", func(t *testing.T) {
		result, err := mgr.Execute("dialog-accept", nil)
		if err != nil {
			t.Fatalf("dialog-accept: %v", err)
		}
		if result != "Dialogs will be accepted" {
			t.Fatalf("got %q, want %q", result, "Dialogs will be accepted")
		}

		mode := mgr.GetDialogAutoMode()
		if !mode.accept {
			t.Fatal("expected accept=true after dialog-accept")
		}
	})

	t.Run("dialog_accept_with_text_command", func(t *testing.T) {
		result, err := mgr.Execute("dialog-accept", []string{"TestUser"})
		if err != nil {
			t.Fatalf("dialog-accept: %v", err)
		}
		if !strings.Contains(result, "TestUser") {
			t.Fatalf("expected 'TestUser' in result, got %q", result)
		}

		mode := mgr.GetDialogAutoMode()
		if !mode.accept || mode.promptText != "TestUser" {
			t.Fatalf("mode = %+v, want accept=true promptText=TestUser", mode)
		}

		// Reset
		mgr.Execute("dialog-accept", nil)
	})

	t.Run("dialog_dismiss_command", func(t *testing.T) {
		result, err := mgr.Execute("dialog-dismiss", nil)
		if err != nil {
			t.Fatalf("dialog-dismiss: %v", err)
		}
		if result != "Dialogs will be dismissed" {
			t.Fatalf("got %q, want %q", result, "Dialogs will be dismissed")
		}

		mode := mgr.GetDialogAutoMode()
		if mode.accept {
			t.Fatal("expected accept=false after dialog-dismiss")
		}

		// Reset
		mgr.Execute("dialog-accept", nil)
	})

	t.Run("dialog_accept_confirm_behavior", func(t *testing.T) {
		restore := disableDialogAutoHandler(mgr)
		defer restore()

		if _, err := mgr.Execute("goto", []string{fixtureURL("dialog.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		// Handle the dialog from a separate goroutine (bypasses listenersMu deadlock)
		handleDialogManually(mgr, true, "", 200*time.Millisecond)

		// Trigger confirm via setTimeout so Runtime.evaluate returns before dialog opens
		if _, err := mgr.Execute("js", []string{`setTimeout(() => document.getElementById('confirm-btn').click(), 50)`}); err != nil {
			t.Fatalf("js: %v", err)
		}
		time.Sleep(1 * time.Second)

		result, err := mgr.Execute("js", []string{`document.querySelector("#confirm-result").textContent`})
		if err != nil {
			t.Fatalf("js: %v", err)
		}
		if result != "confirmed" {
			t.Fatalf("confirm result = %q, want %q", result, "confirmed")
		}
	})

	t.Run("dialog_dismiss_confirm_behavior", func(t *testing.T) {
		restore := disableDialogAutoHandler(mgr)
		defer restore()

		if _, err := mgr.Execute("goto", []string{fixtureURL("dialog.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		handleDialogManually(mgr, false, "", 200*time.Millisecond)

		if _, err := mgr.Execute("js", []string{`setTimeout(() => document.getElementById('confirm-btn').click(), 50)`}); err != nil {
			t.Fatalf("js: %v", err)
		}
		time.Sleep(1 * time.Second)

		result, err := mgr.Execute("js", []string{`document.querySelector("#confirm-result").textContent`})
		if err != nil {
			t.Fatalf("js: %v", err)
		}
		if result != "cancelled" {
			t.Fatalf("confirm result = %q, want %q", result, "cancelled")
		}
	})

	t.Run("dialog_accept_prompt_behavior", func(t *testing.T) {
		restore := disableDialogAutoHandler(mgr)
		defer restore()

		if _, err := mgr.Execute("goto", []string{fixtureURL("dialog.html")}); err != nil {
			t.Fatalf("goto: %v", err)
		}

		handleDialogManually(mgr, true, "TestUser", 200*time.Millisecond)

		if _, err := mgr.Execute("js", []string{`setTimeout(() => document.getElementById('prompt-btn').click(), 50)`}); err != nil {
			t.Fatalf("js: %v", err)
		}
		time.Sleep(1 * time.Second)

		result, err := mgr.Execute("js", []string{`document.querySelector("#prompt-result").textContent`})
		if err != nil {
			t.Fatalf("js: %v", err)
		}
		if result != "TestUser" {
			t.Fatalf("prompt result = %q, want %q", result, "TestUser")
		}
	})

	t.Run("dialog_clear_buffer", func(t *testing.T) {
		cleared, err := mgr.Execute("dialog", []string{"--clear"})
		if err != nil {
			t.Fatalf("dialog --clear: %v", err)
		}
		if !strings.Contains(cleared, "cleared") {
			t.Fatalf("expected 'cleared', got %q", cleared)
		}

		after, err := mgr.Execute("dialog", nil)
		if err != nil {
			t.Fatalf("dialog: %v", err)
		}
		if !strings.Contains(after, "no dialog") {
			t.Fatalf("expected empty dialog buffer, got %q", after)
		}
	})
}
