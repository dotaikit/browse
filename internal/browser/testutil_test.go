package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

var (
	testMgr         *Manager
	testMgrCleanup  func()
	testServer      *httptest.Server
	testBaseURL     string
	testChromeError string
)

// TestMain boots shared integration-test dependencies once for the package.
func TestMain(m *testing.M) {
	server, baseURL, err := startSharedTestServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "browser tests: start fixture server: %v\n", err)
		os.Exit(1)
	}
	testServer = server
	testBaseURL = baseURL

	mgr, cleanup, err := createTestManager()
	if err != nil {
		testChromeError = err.Error()
		fmt.Fprintf(os.Stderr, "browser tests: chrome unavailable, integration tests will be skipped: %v\n", err)
	} else {
		testMgr = mgr
		testMgrCleanup = cleanup
	}

	code := m.Run()

	if testMgrCleanup != nil {
		testMgrCleanup()
	}
	if testServer != nil {
		testServer.Close()
	}

	os.Exit(code)
}

// newTestManager starts an isolated Manager for tests that need fresh state.
func newTestManager(t *testing.T) *Manager {
	t.Helper()

	mgr, cleanup, err := createTestManager()
	if err != nil {
		t.Skipf("skip: chrome unavailable: %v", err)
	}
	t.Cleanup(cleanup)
	return mgr
}

// sharedTestManager returns the TestMain-managed manager or skips when Chrome is unavailable.
func sharedTestManager(t *testing.T) *Manager {
	t.Helper()
	if testMgr == nil {
		if testChromeError != "" {
			t.Skipf("skip: %s", testChromeError)
		}
		t.Skip("skip: shared chrome manager not initialized")
	}
	return testMgr
}

// startTestServer starts an isolated fixture server for tests that need custom server state.
func startTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	server, baseURL, err := startSharedTestServer()
	if err != nil {
		t.Fatalf("start test server: %v", err)
	}
	t.Cleanup(server.Close)
	return server, baseURL
}

func startSharedTestServer() (*httptest.Server, string, error) {
	fixturesDir, err := fixtureDir()
	if err != nil {
		return nil, "", err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		headers := make(map[string]string, len(r.Header))
		for key, values := range r.Header {
			headers[strings.ToLower(key)] = strings.Join(values, ",")
		}

		body, err := json.MarshalIndent(headers, "", "  ")
		if err != nil {
			http.Error(w, "failed to marshal headers", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fileName := strings.TrimPrefix(r.URL.Path, "/")
		if fileName == "" {
			fileName = "basic.html"
		}

		cleanPath := filepath.Clean(fileName)
		if cleanPath == "." {
			cleanPath = "basic.html"
		}
		if strings.HasPrefix(cleanPath, "..") {
			http.NotFound(w, r)
			return
		}

		fullPath := filepath.Join(fixturesDir, cleanPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if filepath.Ext(cleanPath) == ".html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		_, _ = w.Write(content)
	})

	server := httptest.NewServer(mux)
	return server, server.URL, nil
}

func fixtureURL(path string) string {
	if path == "" || path == "/" {
		return testBaseURL + "/"
	}
	return testBaseURL + "/" + strings.TrimPrefix(path, "/")
}

func fixtureDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate testutil_test.go")
	}

	dir := filepath.Join(filepath.Dir(thisFile), "testdata", "fixtures")
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("fixtures directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("fixtures path %q is not a directory", dir)
	}
	return dir, nil
}

func createTestManager() (*Manager, func(), error) {
	chromePath, err := findChromeBinary()
	if err != nil {
		return nil, nil, err
	}

	userDataDir, err := os.MkdirTemp("", "browse-test-chrome-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create chrome user-data-dir: %w", err)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(userDataDir),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)

	if err := chromedp.Run(ctx); err != nil {
		ctxCancel()
		allocCancel()
		_ = os.RemoveAll(userDataDir)
		return nil, nil, fmt.Errorf("start chrome: %w", err)
	}

	mgr := &Manager{
		chromeURL:     "exec://" + filepath.Base(chromePath),
		allocCtx:      allocCtx,
		allocCancel:   allocCancel,
		ctx:           ctx,
		ctxCancel:     ctxCancel,
		startedAt:     time.Now(),
		refMap:        make(map[string]RefEntry),
		consoleBuffer: NewRingBuffer[ConsoleEntry](50000),
		networkBuffer: NewRingBuffer[NetworkEntry](50000),
		dialogBuffer:  NewRingBuffer[DialogEntry](50000),
	}

	// Match test expectations from the prior implementation: dialogs auto-accept by default.
	mgr.SetDialogAutoMode(true, "")

	if err := mgr.startEventListeners(); err != nil {
		mgr.Close()
		_ = os.RemoveAll(userDataDir)
		return nil, nil, err
	}

	cleanup := func() {
		mgr.Close()
		_ = os.RemoveAll(userDataDir)
	}
	return mgr, cleanup, nil
}

func findChromeBinary() (string, error) {
	candidates := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium-browser",
		"chromium",
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("chrome not found (tried %s)", strings.Join(candidates, ", "))
}
