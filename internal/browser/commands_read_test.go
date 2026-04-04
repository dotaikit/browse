package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type readTestForm struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Fields []readTestField `json:"fields"`
}

type readTestField struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Value    string `json:"value"`
}

type readTestCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type readTestStorage struct {
	LocalStorage   map[string]string `json:"localStorage"`
	SessionStorage map[string]string `json:"sessionStorage"`
}

func TestCommandsRead(t *testing.T) {
	mgr := sharedTestManager(t)

	t.Run("forms_discovers_fields_and_redacts_password", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("forms.html"))
		_ = mustExecCommand(t, mgr, "fill", "#email", "reader@example.com")
		_ = mustExecCommand(t, mgr, "fill", "#password", "super-secret")

		out := mustExecCommand(t, mgr, "forms")
		if strings.Contains(out, "super-secret") {
			t.Fatalf("forms output leaked password value: %q", out)
		}

		var forms []readTestForm
		if err := json.Unmarshal([]byte(out), &forms); err != nil {
			t.Fatalf("unmarshal forms output: %v\noutput:\n%s", err, out)
		}
		if len(forms) != 2 {
			t.Fatalf("forms length = %d, want 2", len(forms))
		}

		loginForm := findFormByID(t, forms, "login-form")
		if loginForm.Method != "post" {
			t.Fatalf("login form method = %q, want %q", loginForm.Method, "post")
		}

		emailField := findFormFieldByName(t, loginForm.Fields, "email")
		if emailField.Type != "email" {
			t.Fatalf("email field type = %q, want %q", emailField.Type, "email")
		}
		if !emailField.Required {
			t.Fatalf("email field required = %v, want true", emailField.Required)
		}
		if emailField.Value != "reader@example.com" {
			t.Fatalf("email field value = %q, want %q", emailField.Value, "reader@example.com")
		}

		passwordField := findFormFieldByName(t, loginForm.Fields, "password")
		if passwordField.Value != "[redacted]" {
			t.Fatalf("password field value = %q, want %q", passwordField.Value, "[redacted]")
		}
	})

	t.Run("css_and_attrs_support_selector_and_ref", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("basic.html"))

		color := mustExecCommand(t, mgr, "css", "h1", "color")
		assertContains(t, color, "0, 0, 128")

		attrsOut := mustExecCommand(t, mgr, "attrs", "#content")
		var attrs map[string]string
		if err := json.Unmarshal([]byte(attrsOut), &attrs); err != nil {
			t.Fatalf("unmarshal attrs output: %v\noutput:\n%s", err, attrsOut)
		}
		if attrs["id"] != "content" {
			t.Fatalf("attrs[id] = %q, want %q", attrs["id"], "content")
		}
		if attrs["data-testid"] != "main-content" {
			t.Fatalf("attrs[data-testid] = %q, want %q", attrs["data-testid"], "main-content")
		}
		if attrs["data-version"] != "1.0" {
			t.Fatalf("attrs[data-version] = %q, want %q", attrs["data-version"], "1.0")
		}

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("forms.html"))
		snapshot := mustExecCommand(t, mgr, "snapshot", "-i")
		loginButtonRef := findRefInSnapshot(t, snapshot, "button", "Log In")

		display := mustExecCommand(t, mgr, "css", loginButtonRef, "display")
		if strings.TrimSpace(display) == "" {
			t.Fatalf("css display for %s is empty", loginButtonRef)
		}

		refAttrsOut := mustExecCommand(t, mgr, "attrs", loginButtonRef)
		var refAttrs map[string]string
		if err := json.Unmarshal([]byte(refAttrsOut), &refAttrs); err != nil {
			t.Fatalf("unmarshal ref attrs output: %v\noutput:\n%s", err, refAttrsOut)
		}
		if refAttrs["id"] != "login-btn" {
			t.Fatalf("ref attrs[id] = %q, want %q", refAttrs["id"], "login-btn")
		}
	})

	t.Run("is_checks_all_supported_states", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("states.html"))

		testCases := []struct {
			property string
			selector string
			want     string
		}{
			{property: "visible", selector: "#visible-div", want: "true"},
			{property: "hidden", selector: "#hidden-div", want: "true"},
			{property: "enabled", selector: "#enabled-input", want: "true"},
			{property: "disabled", selector: "#disabled-input", want: "true"},
			{property: "checked", selector: "#checked-box", want: "true"},
			{property: "editable", selector: "#enabled-input", want: "true"},
			{property: "editable", selector: "#readonly-input", want: "false"},
		}

		for _, tc := range testCases {
			got := mustExecCommand(t, mgr, "is", tc.property, tc.selector)
			if got != tc.want {
				t.Fatalf("is %s %s = %q, want %q", tc.property, tc.selector, got, tc.want)
			}
		}

		_ = mustExecCommand(t, mgr, "click", "#enabled-input")
		focused := mustExecCommand(t, mgr, "is", "focused", "#enabled-input")
		if focused != "true" {
			t.Fatalf("is focused #enabled-input = %q, want %q", focused, "true")
		}

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("forms.html"))
		snapshot := mustExecCommand(t, mgr, "snapshot", "-i")
		loginButtonRef := findRefInSnapshot(t, snapshot, "button", "Log In")
		refVisible := mustExecCommand(t, mgr, "is", "visible", loginButtonRef)
		if refVisible != "true" {
			t.Fatalf("is visible %s = %q, want %q", loginButtonRef, refVisible, "true")
		}
	})

	t.Run("cookies_lists_current_page_cookie_values", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("basic.html"))

		cookieName := fmt.Sprintf("read_cookie_%d", time.Now().UnixNano())
		cookieValue := "cookie-value"
		_ = mustExecCommand(t, mgr, "cookie", cookieName+"="+cookieValue)

		out := mustExecCommand(t, mgr, "cookies")
		if out == "(no cookies)" {
			t.Fatalf("cookies output unexpectedly empty after setting cookie %q", cookieName)
		}

		var cookies []readTestCookie
		if err := json.Unmarshal([]byte(out), &cookies); err != nil {
			t.Fatalf("unmarshal cookies output: %v\noutput:\n%s", err, out)
		}

		found := false
		for _, c := range cookies {
			if c.Name != cookieName {
				continue
			}
			found = true
			if c.Value != cookieValue {
				t.Fatalf("cookie %q value = %q, want %q", cookieName, c.Value, cookieValue)
			}
			break
		}
		if !found {
			t.Fatalf("cookie %q not found in cookies output: %s", cookieName, out)
		}
	})

	t.Run("storage_set_get_and_redaction", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("basic.html"))
		_ = mustExecCommand(t, mgr, "js", `localStorage.clear(); sessionStorage.clear();`)

		setOut := mustExecCommand(t, mgr, "storage", "set", "session_token", "abc123")
		assertContains(t, setOut, "session_token")
		if strings.Contains(setOut, "abc123") {
			t.Fatalf("storage set output leaked value: %q", setOut)
		}

		_ = mustExecCommand(t, mgr, "storage", "set", "displayName", "normalValue")
		_ = mustExecCommand(t, mgr, "storage", "set", "userData", "eyJhbGciOiJIUzI1NiJ9.payload.sig")
		_ = mustExecCommand(t, mgr, "js", `sessionStorage.setItem("tempFlag", "ok")`)

		out := mustExecCommand(t, mgr, "storage")
		if strings.Contains(out, "abc123") || strings.Contains(out, "eyJhbGciOiJIUzI1NiJ9") {
			t.Fatalf("storage output leaked sensitive values: %s", out)
		}

		var storage readTestStorage
		if err := json.Unmarshal([]byte(out), &storage); err != nil {
			t.Fatalf("unmarshal storage output: %v\noutput:\n%s", err, out)
		}

		if storage.LocalStorage["displayName"] != "normalValue" {
			t.Fatalf("localStorage[displayName] = %q, want %q", storage.LocalStorage["displayName"], "normalValue")
		}
		if storage.LocalStorage["session_token"] != "[REDACTED — 6 chars]" {
			t.Fatalf("localStorage[session_token] = %q, want %q", storage.LocalStorage["session_token"], "[REDACTED — 6 chars]")
		}
		if !strings.Contains(storage.LocalStorage["userData"], "[REDACTED") {
			t.Fatalf("localStorage[userData] should be redacted, got %q", storage.LocalStorage["userData"])
		}
		if storage.SessionStorage["tempFlag"] != "ok" {
			t.Fatalf("sessionStorage[tempFlag] = %q, want %q", storage.SessionStorage["tempFlag"], "ok")
		}
	})

	t.Run("perf_returns_timing_metrics", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("basic.html"))

		out := mustExecCommand(t, mgr, "perf")
		assertContains(t, out, "dns")
		assertContains(t, out, "ttfb")
		assertContains(t, out, "load")
		assertContains(t, out, "ms")
	})

	t.Run("eval_file_exec_object_return_and_await", func(t *testing.T) {
		_ = mustExecCommand(t, mgr, "goto", fixtureURL("basic.html"))

		tmpDir := t.TempDir()

		execFile := filepath.Join(tmpDir, "eval-exec.js")
		if err := os.WriteFile(execFile, []byte(`document.title + " - evaluated"`), 0644); err != nil {
			t.Fatalf("write eval exec file: %v", err)
		}
		execOut := mustExecCommand(t, mgr, "eval", execFile)
		if execOut != "Test Page - Basic - evaluated" {
			t.Fatalf("eval exec output = %q, want %q", execOut, "Test Page - Basic - evaluated")
		}

		objectFile := filepath.Join(tmpDir, "eval-object.js")
		if err := os.WriteFile(objectFile, []byte(`({title: document.title, listCount: document.querySelectorAll("li").length})`), 0644); err != nil {
			t.Fatalf("write eval object file: %v", err)
		}
		objectOut := mustExecCommand(t, mgr, "eval", objectFile)
		var objectResult map[string]any
		if err := json.Unmarshal([]byte(objectOut), &objectResult); err != nil {
			t.Fatalf("unmarshal eval object output: %v\noutput:\n%s", err, objectOut)
		}
		if objectResult["title"] != "Test Page - Basic" {
			t.Fatalf("eval object title = %v, want %q", objectResult["title"], "Test Page - Basic")
		}
		if objectResult["listCount"] != float64(3) {
			t.Fatalf("eval object listCount = %v, want 3", objectResult["listCount"])
		}

		awaitFile := filepath.Join(tmpDir, "eval-await.js")
		awaitCode := "await Promise.resolve(\"await-ok\")"
		if !hasAwait(awaitCode) {
			t.Fatalf("hasAwait(%q) = false, want true", awaitCode)
		}
		if wrapped := wrapForEvaluate(awaitCode); !strings.HasPrefix(wrapped, "(async()=>(") {
			t.Fatalf("wrapForEvaluate(%q) = %q, want async wrapper", awaitCode, wrapped)
		}
		if err := os.WriteFile(awaitFile, []byte(awaitCode), 0644); err != nil {
			t.Fatalf("write eval await file: %v", err)
		}
		awaitOut := mustExecCommand(t, mgr, "eval", awaitFile)
		if strings.TrimSpace(awaitOut) == "" {
			t.Fatalf("eval await output is empty")
		}
	})
}

func findFormByID(t *testing.T, forms []readTestForm, id string) readTestForm {
	t.Helper()
	for _, form := range forms {
		if form.ID == id {
			return form
		}
	}
	t.Fatalf("form with id %q not found", id)
	return readTestForm{}
}

func findFormFieldByName(t *testing.T, fields []readTestField, name string) readTestField {
	t.Helper()
	for _, field := range fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("field with name %q not found", name)
	return readTestField{}
}
