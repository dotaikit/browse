package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const stateFileVersion = 1
const stateTTL = 7 * 24 * time.Hour

var stateNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type browserState struct {
	Version int               `json:"version"`
	SavedAt string            `json:"savedAt"`
	Cookies []*network.Cookie `json:"cookies"`
	Pages   []statePage       `json:"pages"`
}

type statePage struct {
	URL      string `json:"url"`
	IsActive bool   `json:"isActive"`
}

// --- Meta Commands ---

// cmdNewTab opens a new browser tab, optionally navigating to a URL.
// Usage: newtab [url]
func (m *Manager) cmdNewTab(args []string) (string, error) {
	urlStr := ""
	if len(args) > 0 {
		normalized, err := normalizeNavigationURL(args[0])
		if err != nil {
			return "", fmt.Errorf("newtab: %w", err)
		}
		urlStr = normalized
	}
	if urlStr == "" {
		urlStr = "about:blank"
	}

	var targetID target.ID
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		targetID, err = target.CreateTarget(urlStr).Do(ctx)
		return err
	})); err != nil {
		return "", fmt.Errorf("newtab: %w", err)
	}

	if urlStr == "about:blank" {
		return fmt.Sprintf("Opened new tab %s", targetID), nil
	}
	return fmt.Sprintf("Opened new tab %s → %s", targetID, urlStr), nil
}

// cmdCloseTab closes a browser tab by target ID.
// Usage: closetab [target-id]
func (m *Manager) cmdCloseTab(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: closetab <target-id|index>")
	}

	idOrIndex := args[0]

	// Try as numeric index first
	if idx, err := strconv.Atoi(idOrIndex); err == nil {
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			targets, err := target.GetTargets().Do(ctx)
			if err != nil {
				return err
			}
			var pages []*target.Info
			for _, t := range targets {
				if t.Type == "page" {
					pages = append(pages, t)
				}
			}
			if idx < 0 || idx >= len(pages) {
				return fmt.Errorf("tab index %d out of range (0-%d)", idx, len(pages)-1)
			}
			return target.CloseTarget(pages[idx].TargetID).Do(ctx)
		})); err != nil {
			return "", fmt.Errorf("closetab: %w", err)
		}
		return fmt.Sprintf("Closed tab %d", idx), nil
	}

	// Treat as target ID
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return target.CloseTarget(target.ID(idOrIndex)).Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("closetab: %w", err)
	}
	return fmt.Sprintf("Closed tab %s", idOrIndex), nil
}

// cmdStatus returns server/browser status information.
func (m *Manager) cmdStatus(args []string) (string, error) {
	currentURL := m.CurrentURL()
	uptime := time.Since(m.startedAt).Round(time.Second)

	// Count tabs
	tabCount := 0
	_ = chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		targets, err := target.GetTargets().Do(ctx)
		if err != nil {
			return err
		}
		for _, t := range targets {
			if t.Type == "page" {
				tabCount++
			}
		}
		return nil
	}))

	pid := os.Getpid()

	return fmt.Sprintf(
		"Status: healthy\nURL: %s\nTabs: %d\nUptime: %s\nPID: %d",
		currentURL, tabCount, uptime, pid,
	), nil
}

// cmdHandoff prints current browser state and asks user to continue manually.
// Usage: handoff [message]
func (m *Manager) cmdHandoff(args []string) (string, error) {
	message := strings.TrimSpace(strings.Join(args, " "))
	currentURL := m.CurrentURL()

	tabCount := 0
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		targets, err := target.GetTargets().Do(ctx)
		if err != nil {
			return err
		}
		for _, t := range targets {
			if t.Type == "page" {
				tabCount++
			}
		}
		return nil
	})); err != nil {
		return "", fmt.Errorf("handoff: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("Handoff mode: AI control paused.\n")
	fmt.Fprintf(&sb, "URL: %s\n", currentURL)
	fmt.Fprintf(&sb, "Tabs: %d\n", tabCount)
	if message != "" {
		fmt.Fprintf(&sb, "Message: %s\n", message)
	}
	sb.WriteString("You can now operate the browser manually.\n")
	sb.WriteString("Run 'browse resume' when ready.")

	return sb.String(), nil
}

// cmdResume clears stale refs/frame context and refreshes interactive snapshot.
// Usage: resume
func (m *Manager) cmdResume(args []string) (string, error) {
	if len(args) != 0 {
		return "", fmt.Errorf("usage: resume")
	}

	m.ClearRefs()
	m.clearActiveFrame()
	return m.cmdSnapshot([]string{"-i"})
}

// cmdRestart requests the server process to restart.
// Usage: restart
func (m *Manager) cmdRestart(args []string) (string, error) {
	if len(args) != 0 {
		return "", fmt.Errorf("usage: restart")
	}
	return "", ErrRestartRequested
}

// cmdStop requests the server process to stop.
// Usage: stop
func (m *Manager) cmdStop(args []string) (string, error) {
	if len(args) != 0 {
		return "", fmt.Errorf("usage: stop")
	}
	return "", ErrStopRequested
}

// cmdWatch controls manual watch mode snapshots for CLI polling workflows.
// Usage: watch <start|stop|add>
func (m *Manager) cmdWatch(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("usage: watch <start|stop|add>")
	}

	switch args[0] {
	case "start":
		if m.IsWatching() {
			return "Already watching. Run 'watch stop' to stop.", nil
		}
		m.startWatch()
		return "Watching started. Run 'watch add' to capture snapshots.", nil

	case "add":
		if !m.IsWatching() {
			return "", fmt.Errorf("watch add: not currently watching. Run 'watch start' first")
		}

		snapshot, err := m.cmdSnapshot([]string{"-i"})
		if err != nil {
			return "", fmt.Errorf("watch add: %w", err)
		}
		m.addWatchSnapshot(snapshot)
		return "Watch snapshot captured.", nil

	case "stop":
		if !m.IsWatching() {
			return "Not currently watching.", nil
		}

		snapshots, duration := m.stopWatch()
		lastSnapshot := "(none)"
		if len(snapshots) > 0 {
			lastSnapshot = snapshots[len(snapshots)-1]
		}
		return fmt.Sprintf(
			"WATCH STOPPED (%s, %d snapshots)\n\nLast snapshot:\n%s",
			duration.Round(time.Second),
			len(snapshots),
			lastSnapshot,
		), nil

	default:
		return "", fmt.Errorf("usage: watch <start|stop|add>")
	}
}

// cmdState saves or restores cookies + tab URLs to ~/.browse/states/<name>.json.
// Usage: state <save|load> <name>
func (m *Manager) cmdState(args []string) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("usage: state <save|load> <name>")
	}

	switch args[0] {
	case "save":
		return m.cmdStateSave(args[1])
	case "load":
		return m.cmdStateLoad(args[1])
	default:
		return "", fmt.Errorf("usage: state <save|load> <name>")
	}
}

func (m *Manager) cmdStateSave(name string) (string, error) {
	statePath, err := resolveStatePath(name)
	if err != nil {
		return "", err
	}

	snapshot := browserState{
		Version: stateFileVersion,
		SavedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		targets, err := target.GetTargets().Do(ctx)
		if err != nil {
			return fmt.Errorf("list tabs: %w", err)
		}

		activeTargetID := target.ID("")
		if current := chromedp.FromContext(ctx); current != nil && current.Target != nil {
			activeTargetID = current.Target.TargetID
		}

		cookieURLs := make([]string, 0, len(targets))
		seenCookieURLs := make(map[string]struct{}, len(targets))

		for _, info := range targets {
			if info.Type != "page" {
				continue
			}

			pageURL := strings.TrimSpace(info.URL)
			if pageURL == "" {
				pageURL = "about:blank"
			}

			snapshot.Pages = append(snapshot.Pages, statePage{
				URL:      pageURL,
				IsActive: info.TargetID == activeTargetID,
			})

			if !isCookieURL(pageURL) {
				continue
			}
			if _, exists := seenCookieURLs[pageURL]; exists {
				continue
			}
			seenCookieURLs[pageURL] = struct{}{}
			cookieURLs = append(cookieURLs, pageURL)
		}

		if len(snapshot.Pages) == 0 {
			snapshot.Pages = append(snapshot.Pages, statePage{
				URL:      "about:blank",
				IsActive: true,
			})
		} else {
			hasActive := false
			for _, page := range snapshot.Pages {
				if page.IsActive {
					hasActive = true
					break
				}
			}
			if !hasActive {
				snapshot.Pages[0].IsActive = true
			}
		}

		getCookies := network.GetCookies()
		if len(cookieURLs) > 0 {
			getCookies = getCookies.WithURLs(cookieURLs)
		}

		cookies, err := getCookies.Do(ctx)
		if err != nil {
			return fmt.Errorf("get cookies: %w", err)
		}
		snapshot.Cookies = cookies
		return nil
	})); err != nil {
		return "", fmt.Errorf("state save: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return "", fmt.Errorf("state save: create directory: %w", err)
	}

	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", fmt.Errorf("state save: marshal: %w", err)
	}

	if err := writeStateFile(statePath, payload); err != nil {
		return "", fmt.Errorf("state save: %w", err)
	}

	return fmt.Sprintf("State saved: %s", statePath), nil
}

func (m *Manager) cmdStateLoad(name string) (string, error) {
	statePath, err := resolveStatePath(name)
	if err != nil {
		return "", err
	}

	payload, err := os.ReadFile(statePath)
	if err != nil {
		return "", fmt.Errorf("state load: read %s: %w", statePath, err)
	}

	var snapshot browserState
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return "", fmt.Errorf("state load: parse %s: %w", statePath, err)
	}
	if snapshot.Version != stateFileVersion {
		return "", fmt.Errorf("state load: unsupported state version %d", snapshot.Version)
	}

	pages := normalizeStatePages(snapshot.Pages)
	cookies := cookiesToParams(snapshot.Cookies)

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if len(cookies) > 0 {
			if err := network.SetCookies(cookies).Do(ctx); err != nil {
				return fmt.Errorf("set cookies: %w", err)
			}
		}

		currentTargetID := target.ID("")
		if current := chromedp.FromContext(ctx); current != nil && current.Target != nil {
			currentTargetID = current.Target.TargetID
		}
		if currentTargetID == "" {
			return fmt.Errorf("cannot resolve active tab target")
		}

		targets, err := target.GetTargets().Do(ctx)
		if err != nil {
			return fmt.Errorf("list tabs: %w", err)
		}

		for _, info := range targets {
			if info.Type != "page" || info.TargetID == currentTargetID {
				continue
			}
			if err := target.CloseTarget(info.TargetID).Do(ctx); err != nil {
				return fmt.Errorf("close tab %s: %w", info.TargetID, err)
			}
		}

		if err := chromedp.Navigate(pages[0].URL).Do(ctx); err != nil {
			return fmt.Errorf("open %s: %w", pages[0].URL, err)
		}

		openedTargets := make([]target.ID, 0, len(pages))
		openedTargets = append(openedTargets, currentTargetID)

		for _, page := range pages[1:] {
			targetID, err := target.CreateTarget(page.URL).Do(ctx)
			if err != nil {
				return fmt.Errorf("open %s: %w", page.URL, err)
			}
			openedTargets = append(openedTargets, targetID)
		}

		activeIndex := 0
		for i, page := range pages {
			if page.IsActive {
				activeIndex = i
				break
			}
		}
		if activeIndex < 0 || activeIndex >= len(openedTargets) {
			activeIndex = 0
		}

		if err := target.ActivateTarget(openedTargets[activeIndex]).Do(ctx); err != nil {
			return fmt.Errorf("activate tab: %w", err)
		}
		return nil
	})); err != nil {
		return "", fmt.Errorf("state load: %w", err)
	}

	result := fmt.Sprintf("State loaded: %s", statePath)
	if warning := stateTTLWarning(snapshot.SavedAt, time.Now()); warning != "" {
		result = warning + "\n" + result
	}

	return result, nil
}

func resolveStatePath(name string) (string, error) {
	if !stateNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid state name %q: must match [a-zA-Z0-9_-]+", name)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, ".browse", "states", name+".json"), nil
}

func normalizeStatePages(raw []statePage) []statePage {
	pages := make([]statePage, 0, len(raw))
	hasActive := false

	for _, page := range raw {
		pageURL := strings.TrimSpace(page.URL)
		if pageURL == "" {
			continue
		}

		isActive := page.IsActive && !hasActive
		if isActive {
			hasActive = true
		}

		pages = append(pages, statePage{
			URL:      pageURL,
			IsActive: isActive,
		})
	}

	if len(pages) == 0 {
		return []statePage{{URL: "about:blank", IsActive: true}}
	}
	if !hasActive {
		pages[0].IsActive = true
	}
	return pages
}

func stateTTLWarning(savedAt string, now time.Time) string {
	if strings.TrimSpace(savedAt) == "" {
		return ""
	}

	savedTime, err := time.Parse(time.RFC3339, savedAt)
	if err != nil {
		return ""
	}

	age := now.Sub(savedTime)
	if age <= stateTTL {
		return ""
	}

	daysOld := int(age.Hours() / 24)
	if daysOld < 1 {
		daysOld = 1
	}
	return fmt.Sprintf("Warning: state file is %d days old. Consider re-saving.", daysOld)
}

func isCookieURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func cookiesToParams(cookies []*network.Cookie) []*network.CookieParam {
	params := make([]*network.CookieParam, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}

		param := &network.CookieParam{
			Name:         cookie.Name,
			Value:        cookie.Value,
			Domain:       cookie.Domain,
			Path:         cookie.Path,
			Secure:       cookie.Secure,
			HTTPOnly:     cookie.HTTPOnly,
			SameSite:     cookie.SameSite,
			Priority:     cookie.Priority,
			SourceScheme: cookie.SourceScheme,
			SourcePort:   cookie.SourcePort,
			PartitionKey: cookie.PartitionKey,
		}

		if !cookie.Session && cookie.Expires >= 0 {
			expiresAt := time.Unix(0, int64(cookie.Expires*float64(time.Second)))
			expires := cdp.TimeSinceEpoch(expiresAt)
			param.Expires = &expires
		}

		params = append(params, param)
	}
	return params
}

func writeStateFile(path string, payload []byte) error {
	tempPath := path + ".tmp"

	if err := os.WriteFile(tempPath, payload, 0600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Chmod(tempPath, 0600); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("set temp permissions: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("set permissions: %w", err)
	}
	return nil
}

// cmdChain executes multiple commands sequentially.
// Usage: chain '<json-array>' or chain 'cmd1 arg | cmd2 arg | ...'
func (m *Manager) cmdChain(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: chain '<json-array>' or chain 'cmd1 arg | cmd2 arg'")
	}

	input := strings.Join(args, " ")

	var commands [][]string

	// Try JSON parse first
	if err := json.Unmarshal([]byte(input), &commands); err != nil {
		// Fallback: pipe-delimited format
		segments := strings.Split(input, " | ")
		commands = make([][]string, 0, len(segments))
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			parts := tokenizeChainSegment(seg)
			if len(parts) > 0 {
				commands = append(commands, parts)
			}
		}
	}

	if len(commands) == 0 {
		return "", fmt.Errorf("no commands to execute")
	}

	var results []string
	for _, cmd := range commands {
		if len(cmd) == 0 {
			continue
		}
		name := cmd[0]
		cmdArgs := cmd[1:]

		result, err := m.Execute(name, cmdArgs)
		if err != nil {
			results = append(results, fmt.Sprintf("[%s] ERROR: %s", name, err.Error()))
		} else {
			results = append(results, fmt.Sprintf("[%s] %s", name, result))
		}
	}

	return strings.Join(results, "\n\n"), nil
}

// tokenizeChainSegment splits a command segment respecting double-quoted strings.
func tokenizeChainSegment(segment string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false

	for _, ch := range segment {
		switch {
		case ch == '"':
			inQuote = !inQuote
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// cmdDiff navigates to two URLs and produces a unified diff of their text content.
// Usage: diff <url1> <url2>
func (m *Manager) cmdDiff(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: diff <url1> <url2>")
	}

	url1, err := normalizeNavigationURL(args[0])
	if err != nil {
		return "", fmt.Errorf("diff url1: %w", err)
	}
	url2, err := normalizeNavigationURL(args[1])
	if err != nil {
		return "", fmt.Errorf("diff url2: %w", err)
	}

	// Get text from url1
	if err := chromedp.Run(m.ctx, chromedp.Navigate(url1)); err != nil {
		return "", fmt.Errorf("diff: navigate to %s: %w", url1, err)
	}
	// Wait briefly for page load
	time.Sleep(500 * time.Millisecond)

	var text1 string
	if err := chromedp.Run(m.ctx, m.evaluate(cleanTextJS, &text1)); err != nil {
		return "", fmt.Errorf("diff: get text from %s: %w", url1, err)
	}

	// Get text from url2
	if err := chromedp.Run(m.ctx, chromedp.Navigate(url2)); err != nil {
		return "", fmt.Errorf("diff: navigate to %s: %w", url2, err)
	}
	time.Sleep(500 * time.Millisecond)

	var text2 string
	if err := chromedp.Run(m.ctx, m.evaluate(cleanTextJS, &text2)); err != nil {
		return "", fmt.Errorf("diff: get text from %s: %w", url2, err)
	}

	// Compute simple unified diff
	lines1 := strings.Split(text1, "\n")
	lines2 := strings.Split(text2, "\n")

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", url1)
	fmt.Fprintf(&sb, "+++ %s\n", url2)
	sb.WriteString("\n")

	// Simple line-by-line diff (not a full Myers diff, but functional)
	i, j := 0, 0
	for i < len(lines1) || j < len(lines2) {
		if i < len(lines1) && j < len(lines2) {
			if lines1[i] == lines2[j] {
				fmt.Fprintf(&sb, "  %s\n", lines1[i])
				i++
				j++
			} else {
				// Look ahead to find matching line
				found := false
				for k := j + 1; k < len(lines2) && k-j < 5; k++ {
					if lines1[i] == lines2[k] {
						// Lines added in text2
						for ; j < k; j++ {
							fmt.Fprintf(&sb, "+ %s\n", lines2[j])
						}
						found = true
						break
					}
				}
				if !found {
					for k := i + 1; k < len(lines1) && k-i < 5; k++ {
						if lines2[j] == lines1[k] {
							// Lines removed from text1
							for ; i < k; i++ {
								fmt.Fprintf(&sb, "- %s\n", lines1[i])
							}
							found = true
							break
						}
					}
				}
				if !found {
					fmt.Fprintf(&sb, "- %s\n", lines1[i])
					fmt.Fprintf(&sb, "+ %s\n", lines2[j])
					i++
					j++
				}
			}
		} else if i < len(lines1) {
			fmt.Fprintf(&sb, "- %s\n", lines1[i])
			i++
		} else {
			fmt.Fprintf(&sb, "+ %s\n", lines2[j])
			j++
		}
	}

	return sb.String(), nil
}

// cleanTextJS is the JavaScript to extract clean text from a page.
const cleanTextJS = `(function() {
	var body = document.body;
	if (!body) return '';
	var clone = body.cloneNode(true);
	clone.querySelectorAll('script, style, noscript, svg').forEach(function(el) { el.remove(); });
	return clone.innerText
		.split('\n')
		.map(function(line) { return line.trim(); })
		.filter(function(line) { return line.length > 0; })
		.join('\n');
})()`

// cmdPDF exports the current page to PDF.
// Usage: pdf [path]
func (m *Manager) cmdPDF(args []string) (string, error) {
	outPath := filepath.Join(os.TempDir(), "browse-page.pdf")
	if len(args) > 0 {
		outPath = args[0]
	}
	resolvedPath, err := validateOutputPath(outPath)
	if err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}
	outPath = resolvedPath

	var pdfData []byte
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		pdfData, _, err = page.PrintToPDF().
			WithPrintBackground(true).
			WithPreferCSSPageSize(true).
			Do(ctx)
		return err
	})); err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}

	// PrintToPDF returns base64-encoded data in some versions
	if len(pdfData) > 0 && pdfData[0] != '%' { // PDF files start with %PDF
		decoded, err := base64.StdEncoding.DecodeString(string(pdfData))
		if err == nil && len(decoded) > 0 && decoded[0] == '%' {
			pdfData = decoded
		}
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return "", fmt.Errorf("pdf: create dir: %w", err)
	}
	if err := os.WriteFile(outPath, pdfData, 0644); err != nil {
		return "", fmt.Errorf("pdf: write: %w", err)
	}
	return fmt.Sprintf("PDF saved: %s", outPath), nil
}

// cmdResponsive takes screenshots at three standard viewports.
// Usage: responsive [prefix]
func (m *Manager) cmdResponsive(args []string) (string, error) {
	prefix := filepath.Join(os.TempDir(), "browse-responsive")
	if len(args) > 0 {
		prefix = args[0]
	}

	viewports := []struct {
		name   string
		width  int64
		height int64
	}{
		{"mobile", 375, 812},
		{"tablet", 768, 1024},
		{"desktop", 1280, 720},
	}

	// Save original viewport to restore later
	var origWidth, origHeight int64
	chromedp.Run(m.ctx, m.evaluate(`JSON.stringify({w: window.innerWidth, h: window.innerHeight})`, nil))

	var results []string

	for _, vp := range viewports {
		// Set viewport
		if err := chromedp.Run(m.ctx, chromedp.EmulateViewport(vp.width, vp.height)); err != nil {
			results = append(results, fmt.Sprintf("%s (%dx%d): ERROR: %s", vp.name, vp.width, vp.height, err))
			continue
		}

		// Wait for layout to settle
		time.Sleep(200 * time.Millisecond)

		// Take screenshot
		outPath := fmt.Sprintf("%s-%s.png", prefix, vp.name)
		resolvedPath, err := validateOutputPath(outPath)
		if err != nil {
			results = append(results, fmt.Sprintf("%s (%dx%d): ERROR: %s", vp.name, vp.width, vp.height, err))
			continue
		}
		outPath = resolvedPath

		var buf []byte
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			data, err := page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				WithCaptureBeyondViewport(true).
				WithFromSurface(true).
				Do(ctx)
			if err != nil {
				return err
			}
			buf = data
			return nil
		})); err != nil {
			results = append(results, fmt.Sprintf("%s (%dx%d): ERROR: %s", vp.name, vp.width, vp.height, err))
			continue
		}

		// Decode base64 if needed
		if len(buf) > 0 && buf[0] != 0x89 {
			decoded, err := base64.StdEncoding.DecodeString(string(buf))
			if err == nil {
				buf = decoded
			}
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			results = append(results, fmt.Sprintf("%s (%dx%d): ERROR: %s", vp.name, vp.width, vp.height, err))
			continue
		}
		if err := os.WriteFile(outPath, buf, 0644); err != nil {
			results = append(results, fmt.Sprintf("%s (%dx%d): ERROR: %s", vp.name, vp.width, vp.height, err))
			continue
		}

		results = append(results, fmt.Sprintf("%s (%dx%d): %s", vp.name, vp.width, vp.height, outPath))
		origWidth = vp.width
		origHeight = vp.height
	}

	// Restore to desktop viewport (last one, or 1280x720 if all failed)
	if origWidth == 0 {
		origWidth = 1280
		origHeight = 720
	}
	chromedp.Run(m.ctx, chromedp.EmulateViewport(origWidth, origHeight))

	return strings.Join(results, "\n"), nil
}
