package browser

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeHeadedOptionsDefaults(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	opts, err := normalizeHeadedOptions(HeadedOptions{})
	if err != nil {
		t.Fatalf("normalizeHeadedOptions() error = %v", err)
	}

	wantUserDataDir := filepath.Join(home, ".browse/chrome-profile")
	if opts.UserDataDir != wantUserDataDir {
		t.Fatalf("UserDataDir = %q, want %q", opts.UserDataDir, wantUserDataDir)
	}
	if !reflect.DeepEqual(opts.WindowSize, [2]int{1280, 900}) {
		t.Fatalf("WindowSize = %v, want [1280 900]", opts.WindowSize)
	}
	if len(opts.ExtensionPaths) != 0 {
		t.Fatalf("ExtensionPaths = %v, want empty", opts.ExtensionPaths)
	}
}

func TestNormalizeHeadedOptionsExtensionsAndWindowSize(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}

	opts, err := normalizeHeadedOptions(HeadedOptions{
		UserDataDir:    "~/chrome-profile",
		ExtensionPaths: []string{" ~/ext-a ", "", "/tmp/ext-b "},
		WindowSize:     [2]int{1440, 960},
	})
	if err != nil {
		t.Fatalf("normalizeHeadedOptions() error = %v", err)
	}

	if opts.UserDataDir != filepath.Join(home, "chrome-profile") {
		t.Fatalf("UserDataDir = %q, want %q", opts.UserDataDir, filepath.Join(home, "chrome-profile"))
	}
	if !reflect.DeepEqual(opts.ExtensionPaths, []string{filepath.Join(home, "ext-a"), "/tmp/ext-b"}) {
		t.Fatalf("ExtensionPaths = %#v, want expanded/trimmed paths", opts.ExtensionPaths)
	}
	if !reflect.DeepEqual(opts.WindowSize, [2]int{1440, 960}) {
		t.Fatalf("WindowSize = %v, want [1440 960]", opts.WindowSize)
	}
}

func TestNormalizeHeadedOptionsRejectsInvalidWindowSize(t *testing.T) {
	_, err := normalizeHeadedOptions(HeadedOptions{WindowSize: [2]int{0, 700}})
	if err == nil {
		t.Fatal("normalizeHeadedOptions() error = nil, want non-nil")
	}
}
