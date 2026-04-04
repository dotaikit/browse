package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestServerAuth(t *testing.T) {
	serverSource := mustReadSourceFile(t, "server.go")

	t.Run("health_endpoint_has_no_auth_and_no_token_field", func(t *testing.T) {
		requireRegexMatch(
			t,
			serverSource,
			`mux\.HandleFunc\("/health",\s*s\.handleHealth\)`,
			"/health should be registered without auth middleware",
		)
		requireRegexNoMatch(
			t,
			serverSource,
			`mux\.HandleFunc\("/health",\s*s\.authMiddleware\(`,
			"/health should not be protected by auth middleware",
		)

		healthBlock := mustSliceBetween(
			t,
			serverSource,
			"func (s *Server) handleHealth(",
			"func (s *Server) handleCommand(",
		)
		if strings.Contains(strings.ToLower(healthBlock), "token") {
			t.Fatalf("handleHealth should not reference token; block:\n%s", healthBlock)
		}
	})

	t.Run("command_and_refs_require_bearer_auth", func(t *testing.T) {
		requireRegexMatch(
			t,
			serverSource,
			`mux\.HandleFunc\("/command",\s*s\.authMiddleware\(s\.handleCommand\)\)`,
			"/command should be protected by auth middleware",
		)
		requireRegexMatch(
			t,
			serverSource,
			`mux\.HandleFunc\("/refs",\s*s\.authMiddleware\(s\.handleRefs\)\)`,
			"/refs should be protected by auth middleware",
		)
	})

	t.Run("auth_middleware_returns_401_for_missing_or_wrong_token", func(t *testing.T) {
		srv := &Server{token: "secret-token"}

		handler := srv.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

		testCases := []struct {
			name       string
			authHeader string
			wantCode   int
		}{
			{name: "missing_auth_header", wantCode: http.StatusUnauthorized},
			{name: "wrong_scheme", authHeader: "Basic secret-token", wantCode: http.StatusUnauthorized},
			{name: "wrong_token", authHeader: "Bearer not-secret", wantCode: http.StatusUnauthorized},
			{name: "correct_token", authHeader: "Bearer secret-token", wantCode: http.StatusNoContent},
		}

		for _, tc := range testCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/command", nil)
				if tc.authHeader != "" {
					req.Header.Set("Authorization", tc.authHeader)
				}
				rec := httptest.NewRecorder()

				handler.ServeHTTP(rec, req)

				if rec.Code != tc.wantCode {
					t.Fatalf("status code = %d, want %d; body=%q", rec.Code, tc.wantCode, rec.Body.String())
				}
				if tc.wantCode == http.StatusUnauthorized && !strings.Contains(rec.Body.String(), "unauthorized") {
					t.Fatalf("expected unauthorized response body, got %q", rec.Body.String())
				}
			})
		}
	})
}

func TestServerSecurityAudit(t *testing.T) {
	t.Run("normalize_navigation_url_blocks_unsafe_schemes", func(t *testing.T) {
		commandsSource := mustReadSourceFile(t, filepath.Join("..", "browser", "commands.go"))

		requireContains(
			t,
			commandsSource,
			`scheme != "http" && scheme != "https"`,
			"normalizeNavigationURL should only allow http/https schemes",
		)
		requireContains(
			t,
			commandsSource,
			`unsafe URL scheme`,
			"normalizeNavigationURL should return an explicit unsafe scheme error",
		)
	})

	t.Run("validate_output_path_rejects_path_traversal", func(t *testing.T) {
		commandsSource := mustReadSourceFile(t, filepath.Join("..", "browser", "commands.go"))

		requireContains(
			t,
			commandsSource,
			"func validateOutputPath(",
			"validateOutputPath should exist",
		)
		requireContains(
			t,
			commandsSource,
			"os.TempDir()",
			"validateOutputPath should allow temp dir outputs",
		)
		requireContains(
			t,
			commandsSource,
			"os.Getwd()",
			"validateOutputPath should allow cwd outputs",
		)
		requireContains(
			t,
			commandsSource,
			"filepath.EvalSymlinks",
			"validateOutputPath should evaluate symlinks to prevent escape",
		)
		requireContains(
			t,
			commandsSource,
			"pathWithinRoot(",
			"validateOutputPath should enforce directory boundaries",
		)
	})

	t.Run("validate_profile_rejects_dotdot_and_control_chars", func(t *testing.T) {
		cookieImportSource := mustReadSourceFile(t, filepath.Join("..", "browser", "cookie_import.go"))

		requireContains(
			t,
			cookieImportSource,
			`strings.Contains(profile, "..")`,
			"validateProfile should reject path traversal via '..'",
		)
		requireContains(
			t,
			cookieImportSource,
			`r < 0x20 || r == 0x7f`,
			"validateProfile should reject control characters",
		)
	})
}

func mustReadSourceFile(t *testing.T, relativePath string) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	baseDir := filepath.Dir(thisFile)
	fullPath := filepath.Clean(filepath.Join(baseDir, relativePath))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read source file %q: %v", fullPath, err)
	}
	return string(data)
}

func mustSliceBetween(t *testing.T, source, startMarker, endMarker string) string {
	t.Helper()

	start := strings.Index(source, startMarker)
	if start == -1 {
		t.Fatalf("start marker not found: %q", startMarker)
	}
	end := strings.Index(source[start:], endMarker)
	if end == -1 {
		t.Fatalf("end marker not found: %q", endMarker)
	}
	return source[start : start+end]
}

func requireContains(t *testing.T, source, want, message string) {
	t.Helper()
	if !strings.Contains(source, want) {
		t.Fatalf("%s; expected to find %q", message, want)
	}
}

func requireRegexMatch(t *testing.T, source, pattern, message string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(source) {
		t.Fatalf("%s; pattern %q not found", message, pattern)
	}
}

func requireRegexNoMatch(t *testing.T, source, pattern, message string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(source) {
		t.Fatalf("%s; unexpected pattern %q found", message, pattern)
	}
}
