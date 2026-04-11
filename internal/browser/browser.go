package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// RefEntry stores a mapped accessibility ref.
type RefEntry struct {
	Role             string            `json:"role"`
	Name             string            `json:"name"`
	BackendDOMNodeID cdp.BackendNodeID `json:"-"`
}

type dialogAutoMode struct {
	enabled    bool
	accept     bool
	promptText string
}

type frameLevel struct {
	frameID   cdp.FrameID
	contextID runtime.ExecutionContextID
}

// HeadedOptions configures local Chrome launch in headed mode.
type HeadedOptions struct {
	UserDataDir    string
	ExtensionPaths []string
	WindowSize     [2]int
	ProxyServer    string
}

const (
	defaultHeadedUserDataDir = "~/.browse/chrome-profile"
	defaultWindowWidth       = 1280
	defaultWindowHeight      = 900
)

var (
	// ErrStopRequested is returned by the stop command so the server can shut down.
	ErrStopRequested = errors.New("browse stop requested")
	// ErrRestartRequested is returned by the restart command so the server can restart.
	ErrRestartRequested = errors.New("browse restart requested")
)

// Manager manages the Chrome connection and ref map.
type Manager struct {
	chromeURL   string
	allocCtx    context.Context
	allocCancel context.CancelFunc
	ctx         context.Context
	ctxCancel   context.CancelFunc
	startedAt   time.Time
	headedMode  bool
	proxyServer string

	mu             sync.RWMutex
	refMap         map[string]RefEntry
	framePath      []frameLevel
	consoleBuffer  *RingBuffer[ConsoleEntry]
	networkBuffer  *RingBuffer[NetworkEntry]
	dialogBuffer   *RingBuffer[DialogEntry]
	dialogMode     dialogAutoMode
	lastSnapshot   string
	watching       bool
	watchSnapshots []string
	watchStartTime time.Time
}

// New creates a Manager connected to Chrome at the given CDP URL.
func New(chromeURL string) (*Manager, error) {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(
		context.Background(), chromeURL,
	)

	ctx, ctxCancel := chromedp.NewContext(allocCtx)

	// Verify connection by running a no-op
	if err := chromedp.Run(ctx); err != nil {
		ctxCancel()
		allocCancel()
		return nil, fmt.Errorf("connect to %s: %w", chromeURL, err)
	}

	mgr := &Manager{
		chromeURL:     chromeURL,
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

	if err := mgr.startEventListeners(); err != nil {
		mgr.Close()
		return nil, err
	}

	return mgr, nil
}

// NewHeaded creates a Manager by launching a local headed Chrome process.
func NewHeaded(opts HeadedOptions) (*Manager, error) {
	normalized, err := normalizeHeadedOptions(opts)
	if err != nil {
		return nil, err
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.UserDataDir(normalized.UserDataDir),
		chromedp.WindowSize(normalized.WindowSize[0], normalized.WindowSize[1]),
	)

	if len(normalized.ExtensionPaths) > 0 {
		extensions := strings.Join(normalized.ExtensionPaths, ",")
		allocOpts = append(allocOpts,
			chromedp.Flag("disable-extensions", false),
			chromedp.Flag("disable-extensions-except", extensions),
			chromedp.Flag("load-extension", extensions),
		)
	}

	if normalized.ProxyServer != "" {
		allocOpts = append(allocOpts, chromedp.Flag("proxy-server", normalized.ProxyServer))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)

	// Verify launch by running a no-op.
	if err := chromedp.Run(ctx); err != nil {
		ctxCancel()
		allocCancel()
		return nil, wrapHeadedStartError(err)
	}

	mgr := &Manager{
		chromeURL:     "exec://local",
		allocCtx:      allocCtx,
		allocCancel:   allocCancel,
		ctx:           ctx,
		ctxCancel:     ctxCancel,
		startedAt:     time.Now(),
		headedMode:    true,
		proxyServer:   normalized.ProxyServer,
		refMap:        make(map[string]RefEntry),
		consoleBuffer: NewRingBuffer[ConsoleEntry](50000),
		networkBuffer: NewRingBuffer[NetworkEntry](50000),
		dialogBuffer:  NewRingBuffer[DialogEntry](50000),
	}

	if err := mgr.startEventListeners(); err != nil {
		mgr.Close()
		return nil, err
	}

	return mgr, nil
}

func (m *Manager) Close() {
	m.ctxCancel()
	m.allocCancel()
}

func (m *Manager) ChromeURL() string    { return m.chromeURL }
func (m *Manager) StartedAt() time.Time { return m.startedAt }
func (m *Manager) IsHeaded() bool       { return m.headedMode }
func (m *Manager) ProxyServer() string  { return m.proxyServer }

// Ctx returns the chromedp context for running actions.
func (m *Manager) Ctx() context.Context { return m.ctx }

// CurrentURL returns the current page URL.
func (m *Manager) CurrentURL() string {
	var url string
	if err := chromedp.Run(m.ctx, chromedp.Location(&url)); err != nil {
		return "(unknown)"
	}
	return url
}

// SetRefs replaces the ref map.
func (m *Manager) SetRefs(refs map[string]RefEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refMap = refs
}

// ClearRefs clears all cached accessibility refs.
func (m *Manager) ClearRefs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refMap = make(map[string]RefEntry)
}

// GetRefs returns a copy of the ref map.
func (m *Manager) GetRefs() map[string]RefEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]RefEntry, len(m.refMap))
	for k, v := range m.refMap {
		result[k] = v
	}
	return result
}

// SetLastSnapshot stores a snapshot text for future diff comparison.
func (m *Manager) SetLastSnapshot(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSnapshot = text
}

// GetLastSnapshot returns the last stored snapshot text.
func (m *Manager) GetLastSnapshot() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastSnapshot
}

func (m *Manager) IsWatching() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.watching
}

func (m *Manager) startWatch() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.watching = true
	m.watchSnapshots = nil
	m.watchStartTime = time.Now()
}

func (m *Manager) stopWatch() ([]string, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshots := append([]string(nil), m.watchSnapshots...)
	var duration time.Duration
	if !m.watchStartTime.IsZero() {
		duration = time.Since(m.watchStartTime)
	}

	m.watching = false
	m.watchSnapshots = nil
	m.watchStartTime = time.Time{}

	return snapshots, duration
}

func (m *Manager) addWatchSnapshot(snapshot string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.watching {
		return
	}
	m.watchSnapshots = append(m.watchSnapshots, snapshot)
}

func (m *Manager) SetDialogAutoMode(accept bool, promptText string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dialogMode = dialogAutoMode{
		enabled:    true,
		accept:     accept,
		promptText: promptText,
	}
}

func (m *Manager) GetDialogAutoMode() dialogAutoMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dialogMode
}

// ResolveRef looks up a ref (@e1) and returns the backend node ID.
func (m *Manager) ResolveRef(ref string) (RefEntry, error) {
	// Strip @ prefix
	key := ref
	if len(key) > 0 && key[0] == '@' {
		key = key[1:]
	}

	m.mu.RLock()
	entry, ok := m.refMap[key]
	m.mu.RUnlock()
	if !ok {
		return RefEntry{}, fmt.Errorf("ref %s not found. Run 'browse snapshot' to refresh refs", ref)
	}

	err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := dom.DescribeNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
		return err
	}))
	if err != nil {
		if isStaleBackendNodeErr(err) {
			m.mu.Lock()
			delete(m.refMap, key)
			m.mu.Unlock()
			return RefEntry{}, fmt.Errorf("ref %s is stale. Run 'browse snapshot' to refresh refs", ref)
		}
		return RefEntry{}, fmt.Errorf("resolve ref %s: %w", ref, err)
	}

	return entry, nil
}

func isStaleBackendNodeErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot find node with given id") ||
		strings.Contains(msg, "could not find node with given id") ||
		strings.Contains(msg, "no node found for given backend id")
}

func (m *Manager) executeWriteCommand(
	run func() (string, error),
) (string, error) {
	result, err := run()
	if err != nil {
		return "", err
	}

	// Best-effort wait to reduce races where subsequent reads run before
	// network activity triggered by a write command has settled.
	_ = m.waitNetworkIdle(1500 * time.Millisecond)
	return result, nil
}

func normalizeHeadedOptions(opts HeadedOptions) (HeadedOptions, error) {
	if opts.UserDataDir == "" {
		opts.UserDataDir = defaultHeadedUserDataDir
	}

	userDataDir, err := expandHomePath(opts.UserDataDir)
	if err != nil {
		return HeadedOptions{}, fmt.Errorf("resolve user data dir: %w", err)
	}
	opts.UserDataDir = userDataDir

	normalizedExtensions := make([]string, 0, len(opts.ExtensionPaths))
	for _, ext := range opts.ExtensionPaths {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		path, err := expandHomePath(ext)
		if err != nil {
			return HeadedOptions{}, fmt.Errorf("resolve extension path %q: %w", ext, err)
		}
		normalizedExtensions = append(normalizedExtensions, path)
	}
	opts.ExtensionPaths = normalizedExtensions

	width := opts.WindowSize[0]
	height := opts.WindowSize[1]
	if width == 0 && height == 0 {
		opts.WindowSize = [2]int{defaultWindowWidth, defaultWindowHeight}
	} else if width <= 0 || height <= 0 {
		return HeadedOptions{}, fmt.Errorf("window size must be positive, got %dx%d", width, height)
	}

	if err := ValidateProxy(opts.ProxyServer); err != nil {
		return HeadedOptions{}, err
	}

	return opts, nil
}

func expandHomePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	switch {
	case path == "":
		return "", nil
	case path == "~":
		return os.UserHomeDir()
	case strings.HasPrefix(path, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	default:
		return path, nil
	}
}

func wrapHeadedStartError(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "executable file not found") {
		return fmt.Errorf("start headed Chrome: Chrome/Chromium executable not found in PATH")
	}
	return fmt.Errorf("start headed Chrome: %w", err)
}

// Execute dispatches a command to the appropriate handler.
func (m *Manager) Execute(command string, args []string) (string, error) {
	switch command {
	// Navigation
	case "goto":
		return m.cmdGoto(args)
	case "back":
		return m.cmdBack()
	case "forward":
		return m.cmdForward()
	case "reload":
		return m.cmdReload()
	case "frame":
		return m.cmdFrame(args)
	case "url":
		return m.CurrentURL(), nil

	// Snapshot
	case "snapshot":
		return m.cmdSnapshot(args)

	// Interaction
	case "click":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdClick(args)
		})
	case "fill":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdFill(args)
		})
	case "type":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdType(args)
		})
	case "hover":
		return m.cmdHover(args)
	case "select":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdSelect(args)
		})
	case "scroll":
		return m.cmdScroll(args)
	case "wait":
		return m.cmdWait(args)

	// Read (extended)
	case "forms":
		return m.cmdForms(args)
	case "css":
		return m.cmdCSS(args)
	case "attrs":
		return m.cmdAttrs(args)
	case "is":
		return m.cmdIs(args)
	case "cookies":
		return m.cmdCookies(args)
	case "storage":
		return m.cmdStorage(args)
	case "perf":
		return m.cmdPerf(args)
	case "eval":
		return m.cmdEval(args)

	// Read
	case "text":
		return m.cmdText(args)
	case "html":
		return m.cmdHTML(args)
	case "links":
		return m.cmdLinks()
	case "js":
		return m.cmdJS(args)

	// Visual
	case "screenshot":
		return m.cmdScreenshot(args)

	// Tabs
	case "tabs":
		return m.cmdTabs()
	case "tab":
		return m.cmdTab(args)
	case "console":
		return m.cmdConsole(args)
	case "network":
		return m.cmdNetwork(args)
	case "dialog":
		return m.cmdDialog(args)

	// Write (extended)
	case "press":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdPress(args)
		})
	case "useragent":
		return m.cmdUserAgent(args)
	case "viewport":
		return m.cmdViewport(args)
	case "cookie":
		return m.cmdCookie(args)
	case "cookie-import":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdCookieImport(args)
		})
	case "cookie-import-browser":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdCookieImportBrowser(args)
		})
	case "header":
		return m.cmdHeader(args)
	case "upload":
		return m.executeWriteCommand(func() (string, error) {
			return m.cmdUpload(args)
		})
	case "dialog-accept":
		return m.cmdDialogAccept(args)
	case "dialog-dismiss":
		return m.cmdDialogDismiss(args)

	// Meta
	case "newtab":
		return m.cmdNewTab(args)
	case "closetab":
		return m.cmdCloseTab(args)
	case "status":
		return m.cmdStatus(args)
	case "chain":
		return m.cmdChain(args)
	case "diff":
		return m.cmdDiff(args)
	case "pdf":
		return m.cmdPDF(args)
	case "responsive":
		return m.cmdResponsive(args)
	case "handoff":
		return m.cmdHandoff(args)
	case "resume":
		return m.cmdResume(args)
	case "state":
		return m.cmdState(args)
	case "watch":
		return m.cmdWatch(args)
	case "restart":
		return m.cmdRestart(args)
	case "stop":
		return m.cmdStop(args)

	default:
		return "", fmt.Errorf("unknown command: %s", command)
	}
}
