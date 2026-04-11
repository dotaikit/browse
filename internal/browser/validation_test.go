package browser

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidationNormalizeNavigationURL(t *testing.T) {
	t.Run("allows_normal_urls", func(t *testing.T) {
		stubLookupHost(t, func(string) ([]string, error) {
			return nil, errors.New("dns disabled for test")
		})

		normalized, err := normalizeNavigationURL("EXAMPLE.com./path")
		if err != nil {
			t.Fatalf("normalizeNavigationURL returned error: %v", err)
		}
		if normalized != "https://example.com/path" {
			t.Fatalf("normalized URL = %q, want %q", normalized, "https://example.com/path")
		}
	})

	t.Run("rejects_unsafe_scheme", func(t *testing.T) {
		cases := []string{
			"file:///etc/passwd",
			"javascript:alert(1)",
			"data:text/plain;base64,SGVsbG8=",
		}

		for _, raw := range cases {
			raw := raw
			t.Run(raw, func(t *testing.T) {
				_, err := normalizeNavigationURL(raw)
				if err == nil {
					t.Fatalf("normalizeNavigationURL(%q): expected error", raw)
				}
				if !strings.Contains(err.Error(), "unsafe URL scheme") {
					t.Fatalf("normalizeNavigationURL(%q): error = %q, want unsafe scheme", raw, err)
				}
			})
		}
	})

	t.Run("rejects_blocked_metadata_hosts", func(t *testing.T) {
		cases := []string{
			"http://169.254.169.254/latest/meta-data",
			"http://metadata.google.internal./computeMetadata/v1",
			"http://[fd00::]/",
		}

		for _, raw := range cases {
			raw := raw
			t.Run(raw, func(t *testing.T) {
				_, err := normalizeNavigationURL(raw)
				if err == nil {
					t.Fatalf("normalizeNavigationURL(%q): expected error", raw)
				}
				if !strings.Contains(err.Error(), "cloud metadata") {
					t.Fatalf("normalizeNavigationURL(%q): error = %q, want metadata block", raw, err)
				}
			})
		}
	})

	t.Run("rejects_numeric_metadata_ip_forms", func(t *testing.T) {
		cases := []string{
			"http://0xA9FEA9FE/",
			"http://2852039166/",
			"http://0251.0376.0251.0376/",
		}

		for _, raw := range cases {
			raw := raw
			t.Run(raw, func(t *testing.T) {
				_, err := normalizeNavigationURL(raw)
				if err == nil {
					t.Fatalf("normalizeNavigationURL(%q): expected error", raw)
				}
				if !strings.Contains(err.Error(), "cloud metadata") {
					t.Fatalf("normalizeNavigationURL(%q): error = %q, want metadata block", raw, err)
				}
			})
		}
	})

	t.Run("rejects_dns_rebinding_to_metadata_ip", func(t *testing.T) {
		stubLookupHost(t, func(host string) ([]string, error) {
			if host == "rebind.test" {
				return []string{"169.254.169.254"}, nil
			}
			return nil, errors.New("not found")
		})

		_, err := normalizeNavigationURL("https://rebind.test/path")
		if err == nil {
			t.Fatalf("normalizeNavigationURL(%q): expected error", "https://rebind.test/path")
		}
		if !strings.Contains(err.Error(), "DNS resolves") {
			t.Fatalf("error = %q, want DNS rebinding block", err)
		}
	})

	t.Run("skips_dns_lookup_for_localhost", func(t *testing.T) {
		calls := 0
		stubLookupHost(t, func(string) ([]string, error) {
			calls++
			return nil, errors.New("should not be called")
		})

		_, err := normalizeNavigationURL("http://localhost:8080")
		if err != nil {
			t.Fatalf("normalizeNavigationURL returned error: %v", err)
		}
		if calls != 0 {
			t.Fatalf("lookupHost called %d times, want 0", calls)
		}
	})
}

func TestValidationOutputPath(t *testing.T) {
	t.Run("allows_temp_dir_output", func(t *testing.T) {
		path := filepath.Join(os.TempDir(), "browse-validation", "shot.png")
		resolved, err := validateOutputPath(path)
		if err != nil {
			t.Fatalf("validateOutputPath returned error: %v", err)
		}

		tempRoot, err := resolvePathWithSymlinks(os.TempDir())
		if err != nil {
			t.Fatalf("resolvePathWithSymlinks(temp dir): %v", err)
		}
		if !pathWithinRoot(resolved, tempRoot) {
			t.Fatalf("resolved path %q is not within temp root %q", resolved, tempRoot)
		}
	})

	t.Run("allows_cwd_output", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		path := filepath.Join(cwd, ".ai", "tmp", "validation-out.txt")

		resolved, err := validateOutputPath(path)
		if err != nil {
			t.Fatalf("validateOutputPath returned error: %v", err)
		}

		cwdRoot, err := resolvePathWithSymlinks(cwd)
		if err != nil {
			t.Fatalf("resolvePathWithSymlinks(cwd): %v", err)
		}
		if !pathWithinRoot(resolved, cwdRoot) {
			t.Fatalf("resolved path %q is not within cwd root %q", resolved, cwdRoot)
		}
	})

	t.Run("rejects_outside_path", func(t *testing.T) {
		outside := filepath.Join(string(os.PathSeparator), "etc", "browse-validation-outside.txt")
		if _, err := validateOutputPath(outside); err == nil {
			t.Fatalf("validateOutputPath(%q): expected error", outside)
		}
	})

	t.Run("rejects_parent_traversal_escape", func(t *testing.T) {
		candidate := filepath.Join("..", "browse-validation-outside.txt")
		resolved, err := resolvePathWithSymlinks(candidate)
		if err != nil {
			t.Fatalf("resolvePathWithSymlinks(%q): %v", candidate, err)
		}

		tempRoot, err := resolvePathWithSymlinks(os.TempDir())
		if err != nil {
			t.Fatalf("resolvePathWithSymlinks(temp dir): %v", err)
		}

		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		cwdRoot, err := resolvePathWithSymlinks(cwd)
		if err != nil {
			t.Fatalf("resolvePathWithSymlinks(cwd): %v", err)
		}

		if pathWithinRoot(resolved, tempRoot) || pathWithinRoot(resolved, cwdRoot) {
			t.Skipf("candidate traversal path %q resolves to allowed root; skip in this environment", resolved)
		}

		if _, err := validateOutputPath(candidate); err == nil {
			t.Fatalf("validateOutputPath(%q): expected error", candidate)
		}
	})

	t.Run("rejects_symlink_escape", func(t *testing.T) {
		base := t.TempDir()
		linkPath := filepath.Join(base, "escape")
		if err := os.Symlink(filepath.Join(string(os.PathSeparator), "etc"), linkPath); err != nil {
			if errors.Is(err, os.ErrPermission) {
				t.Skipf("symlink not permitted: %v", err)
			}
			t.Fatalf("create symlink: %v", err)
		}

		candidate := filepath.Join(linkPath, "passwd-copy")
		if _, err := validateOutputPath(candidate); err == nil {
			t.Fatalf("validateOutputPath(%q): expected error", candidate)
		}
	})
}

func TestValidateProxyValid(t *testing.T) {
	cases := []string{
		"",
		"http://127.0.0.1:8080",
		"https://proxy.example.com:443",
		"socks5://127.0.0.1:1080",
		"http://user:pass@proxy.example.com:8080",
	}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if err := ValidateProxy(raw); err != nil {
				t.Fatalf("ValidateProxy(%q) = %v, want nil", raw, err)
			}
		})
	}
}

func TestValidateProxyInvalid(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr string
	}{
		{"ftp://proxy.example.com:21", "scheme must be"},
		{"://missing-scheme", "invalid proxy URL"},
		{"socks5://", "missing host"},
		{"not-a-url", "scheme must be"},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			err := ValidateProxy(tc.raw)
			if err == nil {
				t.Fatalf("ValidateProxy(%q) = nil, want error containing %q", tc.raw, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateProxy(%q) = %q, want error containing %q", tc.raw, err, tc.wantErr)
			}
		})
	}
}

func stubLookupHost(t *testing.T, stub func(string) ([]string, error)) {
	t.Helper()
	orig := lookupHost
	lookupHost = stub
	t.Cleanup(func() {
		lookupHost = orig
	})
}
