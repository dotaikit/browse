package main

import (
	"reflect"
	"testing"
)

func TestParseServeConfigDefaults(t *testing.T) {
	cfg, err := parseServeConfig(nil)
	if err != nil {
		t.Fatalf("parseServeConfig() error = %v", err)
	}

	if cfg.chromeURL != defaultChromeURL {
		t.Fatalf("chromeURL = %q, want %q", cfg.chromeURL, defaultChromeURL)
	}
	if cfg.headed {
		t.Fatal("headed = true, want false")
	}
	if cfg.port != 0 {
		t.Fatalf("port = %d, want 0", cfg.port)
	}
	if cfg.userDataDir != defaultHeadedUserDataDir {
		t.Fatalf("userDataDir = %q, want %q", cfg.userDataDir, defaultHeadedUserDataDir)
	}
	if !reflect.DeepEqual(cfg.windowSize, [2]int{1280, 900}) {
		t.Fatalf("windowSize = %v, want [1280 900]", cfg.windowSize)
	}
}

func TestParseServeConfigHeaded(t *testing.T) {
	args := []string{
		"--headed",
		"--user-data-dir", "/tmp/profile",
		"--extension", " /tmp/ext-a ,/tmp/ext-b ",
		"--window-size", "1440x960",
		"--port", "7777",
	}
	cfg, err := parseServeConfig(args)
	if err != nil {
		t.Fatalf("parseServeConfig() error = %v", err)
	}

	if !cfg.headed {
		t.Fatal("headed = false, want true")
	}
	if cfg.userDataDir != "/tmp/profile" {
		t.Fatalf("userDataDir = %q, want %q", cfg.userDataDir, "/tmp/profile")
	}
	if !reflect.DeepEqual(cfg.extensionPaths, []string{"/tmp/ext-a", "/tmp/ext-b"}) {
		t.Fatalf("extensionPaths = %#v, want []string{\"/tmp/ext-a\", \"/tmp/ext-b\"}", cfg.extensionPaths)
	}
	if !reflect.DeepEqual(cfg.windowSize, [2]int{1440, 960}) {
		t.Fatalf("windowSize = %v, want [1440 960]", cfg.windowSize)
	}
	if cfg.port != 7777 {
		t.Fatalf("port = %d, want 7777", cfg.port)
	}
}

func TestParseServeConfigMutuallyExclusive(t *testing.T) {
	_, err := parseServeConfig([]string{"--headed", "--chrome-url", "http://127.0.0.1:9222"})
	if err == nil {
		t.Fatal("parseServeConfig() error = nil, want non-nil")
	}
}

func TestParseServeConfigHeadedOnlyFlagsRequireHeaded(t *testing.T) {
	_, err := parseServeConfig([]string{"--user-data-dir", "/tmp/profile"})
	if err == nil {
		t.Fatal("parseServeConfig() error = nil, want non-nil")
	}
}

func TestParseWindowSize(t *testing.T) {
	size, err := parseWindowSize("1920X1080")
	if err != nil {
		t.Fatalf("parseWindowSize() error = %v", err)
	}
	if !reflect.DeepEqual(size, [2]int{1920, 1080}) {
		t.Fatalf("size = %v, want [1920 1080]", size)
	}
}

func TestParseWindowSizeInvalid(t *testing.T) {
	_, err := parseWindowSize("1920")
	if err == nil {
		t.Fatal("parseWindowSize() error = nil, want non-nil")
	}
}
