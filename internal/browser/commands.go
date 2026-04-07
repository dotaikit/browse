package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"golang.org/x/image/draw"
)

// --- Navigation ---

var blockedMetadataHosts = map[string]struct{}{
	"169.254.169.254":          {},
	"fd00::":                   {},
	"metadata.google.internal": {},
	"metadata.azure.internal":  {},
}

var lookupHost = net.LookupHost

func normalizeNavigationURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("usage: goto <url>")
	}

	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		return normalizeParsedNavigationURL(parsed, raw)
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	return normalizeParsedNavigationURL(parsed, raw)
}

func normalizeParsedNavigationURL(parsed *url.URL, raw string) (string, error) {
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsafe URL scheme %q (only http/https allowed)", scheme)
	}
	hostname := normalizeHostname(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("invalid URL: %s", raw)
	}

	if isBlockedMetadataHost(hostname) {
		return "", fmt.Errorf("blocked URL host %q (cloud metadata endpoints are not allowed)", parsed.Hostname())
	}
	if shouldResolveNavigationHost(hostname) && resolvesToBlockedMetadataHost(hostname) {
		return "", fmt.Errorf("blocked URL host %q (DNS resolves to cloud metadata endpoint)", parsed.Hostname())
	}

	parsed.Host = joinNormalizedHostPort(hostname, parsed.Port())
	return parsed.String(), nil
}

func normalizeHostname(hostname string) string {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	hostname = strings.TrimRight(hostname, ".")
	if strings.HasPrefix(hostname, "[") && strings.HasSuffix(hostname, "]") {
		hostname = strings.TrimPrefix(strings.TrimSuffix(hostname, "]"), "[")
	}
	return hostname
}

func isBlockedMetadataHost(hostname string) bool {
	hostname = normalizeHostname(hostname)
	if hostname == "" {
		return false
	}
	if _, blocked := blockedMetadataHosts[hostname]; blocked {
		return true
	}
	ip := parseNavigationIP(hostname)
	return isBlockedMetadataIP(ip)
}

func shouldResolveNavigationHost(hostname string) bool {
	hostname = normalizeHostname(hostname)
	if hostname == "" || hostname == "localhost" {
		return false
	}
	return parseNavigationIP(hostname) == nil
}

func resolvesToBlockedMetadataHost(hostname string) bool {
	addrs, err := lookupHost(hostname)
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if isBlockedMetadataHost(addr) {
			return true
		}
	}
	return false
}

func parseNavigationIP(hostname string) net.IP {
	if hostname == "" {
		return nil
	}

	if ip := net.ParseIP(hostname); ip != nil {
		return ip
	}

	normalizedIPv4, ok := normalizeNumericIPv4(hostname)
	if !ok {
		return nil
	}
	return net.ParseIP(normalizedIPv4)
}

func normalizeNumericIPv4(hostname string) (string, bool) {
	const maxIPv4 = uint64(1<<32 - 1)

	if hostname == "" || strings.Contains(hostname, ":") {
		return "", false
	}

	if strings.Contains(hostname, ".") {
		parts := strings.Split(hostname, ".")
		if len(parts) != 4 {
			return "", false
		}
		octets := make([]byte, 4)
		for i, part := range parts {
			value, ok := parseNumericIPv4Component(part)
			if !ok || value > 255 {
				return "", false
			}
			octets[i] = byte(value)
		}
		return net.IPv4(octets[0], octets[1], octets[2], octets[3]).String(), true
	}

	value, ok := parseNumericIPv4Component(hostname)
	if !ok || value > maxIPv4 {
		return "", false
	}

	return net.IPv4(
		byte(value>>24),
		byte(value>>16),
		byte(value>>8),
		byte(value),
	).String(), true
}

func parseNumericIPv4Component(component string) (uint64, bool) {
	if component == "" || strings.HasPrefix(component, "+") || strings.HasPrefix(component, "-") {
		return 0, false
	}

	base := 10
	digits := component

	switch {
	case strings.HasPrefix(component, "0x") || strings.HasPrefix(component, "0X"):
		base = 16
		digits = component[2:]
	case len(component) > 1 && strings.HasPrefix(component, "0"):
		base = 8
		digits = component[1:]
	}

	if digits == "" {
		return 0, false
	}

	value, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func isBlockedMetadataIP(ip net.IP) bool {
	if ip == nil {
		return false
	}

	if v4 := ip.To4(); v4 != nil {
		_, blocked := blockedMetadataHosts[net.IP(v4).String()]
		return blocked
	}

	_, blocked := blockedMetadataHosts[strings.ToLower(ip.String())]
	return blocked
}

func joinNormalizedHostPort(hostname, port string) string {
	if port != "" {
		return net.JoinHostPort(hostname, port)
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}

func validateOutputPath(filePath string) (string, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return "", fmt.Errorf("path is required")
	}

	resolvedPath, err := resolvePathWithSymlinks(filePath)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	tempDir, err := resolvePathWithSymlinks(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("resolve temp dir: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	resolvedCWD, err := resolvePathWithSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}

	if pathWithinRoot(resolvedPath, tempDir) || pathWithinRoot(resolvedPath, resolvedCWD) {
		return resolvedPath, nil
	}

	return "", fmt.Errorf("path must be within %s or %s", tempDir, resolvedCWD)
}

func resolvePathWithSymlinks(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	current := filepath.Clean(absPath)
	var suffix []string

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func pathWithinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)

	if path == root {
		return true
	}

	rootWithSep := root
	if !strings.HasSuffix(rootWithSep, string(os.PathSeparator)) {
		rootWithSep += string(os.PathSeparator)
	}
	return strings.HasPrefix(path, rootWithSep)
}

func (m *Manager) cmdGoto(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: goto <url>")
	}
	if err := m.ensureMainFrameNavigation("goto"); err != nil {
		return "", err
	}

	targetURL, err := normalizeNavigationURL(args[0])
	if err != nil {
		return "", err
	}
	if err := chromedp.Run(m.ctx, chromedp.Navigate(targetURL)); err != nil {
		return "", fmt.Errorf("navigate: %w", err)
	}
	m.clearActiveFrame()
	m.ClearRefs()
	return fmt.Sprintf("Navigated to %s", m.CurrentURL()), nil
}

func (m *Manager) cmdBack() (string, error) {
	if err := m.ensureMainFrameNavigation("back"); err != nil {
		return "", err
	}
	if err := m.navigateHistoryWithTimeoutAndFallback("back", chromedp.NavigateBack(), `window.history.back();`); err != nil {
		return "", err
	}
	m.clearActiveFrame()
	m.ClearRefs()
	return fmt.Sprintf("Back → %s", m.CurrentURL()), nil
}

func (m *Manager) cmdForward() (string, error) {
	if err := m.ensureMainFrameNavigation("forward"); err != nil {
		return "", err
	}
	if err := m.navigateHistoryWithTimeoutAndFallback("forward", chromedp.NavigateForward(), `window.history.forward();`); err != nil {
		return "", err
	}
	m.clearActiveFrame()
	m.ClearRefs()
	return fmt.Sprintf("Forward → %s", m.CurrentURL()), nil
}

func (m *Manager) cmdReload() (string, error) {
	if err := m.ensureMainFrameNavigation("reload"); err != nil {
		return "", err
	}
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return page.Reload().Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("reload: %w", err)
	}
	m.clearActiveFrame()
	m.ClearRefs()
	return fmt.Sprintf("Reloaded %s", m.CurrentURL()), nil
}

func (m *Manager) navigateHistoryWithTimeoutAndFallback(command string, action chromedp.Action, fallbackJS string) error {
	const timeout = 15 * time.Second

	navCtx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	if err := chromedp.Run(navCtx, action); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%s: %w", command, err)
		}

		// CDP timed out — try JS fallback.
		if evalErr := chromedp.Run(m.ctx, m.evaluate(fallbackJS, nil)); evalErr != nil {
			// "Inspected target navigated or closed" means the original
			// CDP navigation DID complete after our timeout — the page
			// moved, which invalidated the execution context.  This is
			// a success, not a failure.
			if isTargetNavigatedErr(evalErr) {
				_ = m.waitDocumentReadyState(5*time.Second, false)
				return nil
			}
			return fmt.Errorf("%s: timed out after %s and JS fallback failed: %w", command, timeout, evalErr)
		}
		if waitErr := m.waitDocumentReadyState(timeout, false); waitErr != nil {
			return fmt.Errorf("%s: JS fallback navigation did not reach domcontentloaded: %w", command, waitErr)
		}
	}

	return nil
}

func isTargetNavigatedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "inspected target navigated or closed") ||
		strings.Contains(msg, "target closed") ||
		strings.Contains(msg, "execution context was destroyed")
}

func (m *Manager) waitNetworkIdle(timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}

	const (
		pollEvery   = 50 * time.Millisecond
		quietWindow = 250 * time.Millisecond
	)

	deadline := time.Now().Add(timeout)
	lastTotal := m.networkBuffer.TotalAdded()
	lastActivity := time.Now()

	for time.Now().Before(deadline) {
		currentTotal := m.networkBuffer.TotalAdded()
		if currentTotal != lastTotal {
			lastTotal = currentTotal
			lastActivity = time.Now()
		}

		if time.Since(lastActivity) >= quietWindow && !m.hasPendingNetworkRequests() {
			return nil
		}
		time.Sleep(pollEvery)
	}

	return fmt.Errorf("network idle timeout after %s", timeout)
}

func (m *Manager) hasPendingNetworkRequests() bool {
	now := time.Now().UnixMilli()
	const pendingCutoffMs = int64((2 * time.Second) / time.Millisecond)

	entries := m.networkBuffer.Last(512)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Status == 0 && now-entries[i].Timestamp <= pendingCutoffMs {
			return true
		}
	}
	return false
}

// --- Interaction ---

func (m *Manager) cmdClick(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: click <@ref|selector>")
	}
	sel := args[0]

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		if err := m.clickByNodeID(entry.BackendDOMNodeID); err != nil {
			return "", fmt.Errorf("click %s (%s %q): %w", sel, entry.Role, entry.Name, err)
		}
		return fmt.Sprintf("Clicked %s (%s %q) → %s", sel, entry.Role, entry.Name, m.CurrentURL()), nil
	}

	if m.hasActiveFrame() {
		js := fmt.Sprintf(`(function() {
			var el = document.querySelector(%q);
			if (!el) return false;
			el.click();
			return true;
		})()`, sel)
		var clicked bool
		if err := chromedp.Run(m.ctx, m.evaluate(js, &clicked)); err != nil {
			return "", fmt.Errorf("click %q: %w", sel, err)
		}
		if !clicked {
			return "", fmt.Errorf("click %q: element not found", sel)
		}
		return fmt.Sprintf("Clicked %q → %s", sel, m.CurrentURL()), nil
	}

	// CSS selector
	if err := chromedp.Run(m.ctx, chromedp.Click(sel, chromedp.ByQuery)); err != nil {
		return "", fmt.Errorf("click %q: %w", sel, err)
	}
	return fmt.Sprintf("Clicked %q → %s", sel, m.CurrentURL()), nil
}

func (m *Manager) clickByNodeID(id cdp.BackendNodeID) error {
	return chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := dom.ScrollIntoViewIfNeeded().WithBackendNodeID(id).Do(ctx); err != nil {
			return fmt.Errorf("scroll into view: %w", err)
		}
		quads, err := dom.GetContentQuads().WithBackendNodeID(id).Do(ctx)
		if err != nil {
			return fmt.Errorf("get quads: %w", err)
		}
		if len(quads) == 0 {
			return fmt.Errorf("element has no visible area")
		}
		q := quads[0]
		x := (q[0] + q[2] + q[4] + q[6]) / 4
		y := (q[1] + q[3] + q[5] + q[7]) / 4
		return chromedp.MouseClickXY(x, y).Do(ctx)
	}))
}

func (m *Manager) cmdFill(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: fill <@ref|selector> <text>")
	}
	sel := args[0]
	text := strings.Join(args[1:], " ")

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		if err := m.fillByNodeID(entry.BackendDOMNodeID, text); err != nil {
			return "", fmt.Errorf("fill %s: %w", sel, err)
		}
		return fmt.Sprintf("Filled %s (%s %q) with %q", sel, entry.Role, entry.Name, text), nil
	}

	if err := chromedp.Run(m.ctx,
		chromedp.Clear(sel, chromedp.ByQuery),
		chromedp.SendKeys(sel, text, chromedp.ByQuery),
	); err != nil {
		return "", fmt.Errorf("fill %q: %w", sel, err)
	}
	return fmt.Sprintf("Filled %q with %q", sel, text), nil
}

func (m *Manager) fillByNodeID(id cdp.BackendNodeID, text string) error {
	return chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := dom.ScrollIntoViewIfNeeded().WithBackendNodeID(id).Do(ctx); err != nil {
			return err
		}
		if err := dom.Focus().WithBackendNodeID(id).Do(ctx); err != nil {
			return fmt.Errorf("focus: %w", err)
		}
		// Select all + delete to clear existing value
		if err := input.DispatchKeyEvent(input.KeyDown).WithKey("a").WithModifiers(2).Do(ctx); err != nil { // Ctrl+A
			return err
		}
		if err := input.DispatchKeyEvent(input.KeyUp).WithKey("a").WithModifiers(2).Do(ctx); err != nil {
			return err
		}
		if err := input.DispatchKeyEvent(input.KeyDown).WithKey("Backspace").Do(ctx); err != nil {
			return err
		}
		if err := input.DispatchKeyEvent(input.KeyUp).WithKey("Backspace").Do(ctx); err != nil {
			return err
		}
		// Insert text
		return input.InsertText(text).Do(ctx)
	}))
}

func (m *Manager) cmdType(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: type <@ref|selector> <text>")
	}
	sel := args[0]
	text := strings.Join(args[1:], " ")

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			if err := dom.Focus().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx); err != nil {
				return err
			}
			for _, ch := range text {
				if err := input.DispatchKeyEvent(input.KeyDown).WithKey(string(ch)).WithText(string(ch)).Do(ctx); err != nil {
					return err
				}
				if err := input.DispatchKeyEvent(input.KeyUp).WithKey(string(ch)).Do(ctx); err != nil {
					return err
				}
			}
			return nil
		})); err != nil {
			return "", fmt.Errorf("type %s: %w", sel, err)
		}
		return fmt.Sprintf("Typed %q into %s", text, sel), nil
	}

	if err := chromedp.Run(m.ctx, chromedp.SendKeys(sel, text, chromedp.ByQuery)); err != nil {
		return "", fmt.Errorf("type %q: %w", sel, err)
	}
	return fmt.Sprintf("Typed %q into %q", text, sel), nil
}

func (m *Manager) cmdHover(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: hover <@ref|selector>")
	}
	sel := args[0]

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		if err := m.hoverByBackendNodeID(entry.BackendDOMNodeID); err != nil {
			return "", fmt.Errorf("hover %s: %w", sel, err)
		}
		return fmt.Sprintf("Hovered %s (%s %q)", sel, entry.Role, entry.Name), nil
	}

	if m.hasActiveFrame() {
		if err := m.hoverBySelectorInActiveFrame(sel); err != nil {
			return "", fmt.Errorf("hover %q: %w", sel, err)
		}
		return fmt.Sprintf("Hovered %q", sel), nil
	}

	if err := m.hoverBySelector(sel); err != nil {
		return "", fmt.Errorf("hover %q: %w", sel, err)
	}
	return fmt.Sprintf("Hovered %q", sel), nil
}

func (m *Manager) hoverByBackendNodeID(id cdp.BackendNodeID) error {
	return chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := dom.ScrollIntoViewIfNeeded().WithBackendNodeID(id).Do(ctx); err != nil {
			return err
		}
		quads, err := dom.GetContentQuads().WithBackendNodeID(id).Do(ctx)
		if err != nil {
			return err
		}
		if len(quads) == 0 {
			return fmt.Errorf("element has no visible area")
		}
		q := quads[0]
		x := (q[0] + q[2] + q[4] + q[6]) / 4
		y := (q[1] + q[3] + q[5] + q[7]) / 4
		return input.DispatchMouseEvent(input.MouseMoved, x, y).Do(ctx)
	}))
}

func (m *Manager) hoverBySelector(selector string) error {
	return chromedp.Run(m.ctx, chromedp.QueryAfter(selector, func(ctx context.Context, _ runtime.ExecutionContextID, nodes ...*cdp.Node) error {
		if len(nodes) == 0 || nodes[0] == nil {
			return fmt.Errorf("element not found")
		}

		node := nodes[0]
		if err := dom.ScrollIntoViewIfNeeded().WithNodeID(node.NodeID).Do(ctx); err != nil {
			return err
		}

		quads, err := dom.GetContentQuads().WithNodeID(node.NodeID).Do(ctx)
		if err != nil {
			return err
		}
		if len(quads) == 0 {
			return fmt.Errorf("element has no visible area")
		}

		q := quads[0]
		x := (q[0] + q[2] + q[4] + q[6]) / 4
		y := (q[1] + q[3] + q[5] + q[7]) / 4
		return input.DispatchMouseEvent(input.MouseMoved, x, y).Do(ctx)
	}, chromedp.ByQuery))
}

func (m *Manager) hoverBySelectorInActiveFrame(selector string) error {
	var point struct {
		Found  bool    `json:"found"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	}

	js := fmt.Sprintf(`(function() {
		var el = document.querySelector(%q);
		if (!el) return { found: false };
		el.scrollIntoView({ block: "center", inline: "center" });
		var rect = el.getBoundingClientRect();
		return {
			found: true,
			x: rect.left + rect.width / 2,
			y: rect.top + rect.height / 2,
			width: rect.width,
			height: rect.height
		};
	})()`, selector)

	if err := chromedp.Run(m.ctx, m.evaluate(js, &point)); err != nil {
		return err
	}
	if !point.Found {
		return fmt.Errorf("element not found")
	}
	if point.Width <= 0 || point.Height <= 0 {
		return fmt.Errorf("element has no visible area")
	}

	return chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(input.MouseMoved, point.X, point.Y).Do(ctx)
	}))
}

func (m *Manager) cmdSelect(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: select <@ref|selector> <value>")
	}
	sel := args[0]
	value := args[1]

	js := fmt.Sprintf(`(function() {
		var el = document.querySelector('[data-browse-ref="%s"]') || document.querySelector('%s');
		if (!el) return 'element not found';
		el.value = %q;
		el.dispatchEvent(new Event('change', {bubbles: true}));
		return 'selected ' + %q;
	})()`, sel, sel, value, value)

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		// Use runtime to call on the node
		var result string
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			obj, err := dom.ResolveNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return err
			}
			jsExpr := fmt.Sprintf(`function() { this.value = %q; this.dispatchEvent(new Event('change', {bubbles: true})); return 'selected ' + %q; }`, value, value)
			res, _, err := runtime.CallFunctionOn(jsExpr).WithObjectID(obj.ObjectID).Do(ctx)
			if err != nil {
				return err
			}
			if res.Value != nil {
				result = strings.Trim(string(res.Value), `"`)
			}
			return nil
		})); err != nil {
			return "", fmt.Errorf("select %s: %w", sel, err)
		}
		return result, nil
	}

	var result string
	if err := chromedp.Run(m.ctx, m.evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("select: %w", err)
	}
	return result, nil
}

func (m *Manager) cmdScroll(args []string) (string, error) {
	direction := "down"
	amount := 500
	if len(args) > 0 {
		direction = args[0]
	}
	if len(args) > 1 {
		if a, err := strconv.Atoi(args[1]); err == nil {
			amount = a
		}
	}

	if strings.HasPrefix(direction, "@") {
		entry, err := m.ResolveRef(direction)
		if err != nil {
			return "", err
		}
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return dom.ScrollIntoViewIfNeeded().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
		})); err != nil {
			return "", fmt.Errorf("scroll to %s: %w", direction, err)
		}
		return fmt.Sprintf("Scrolled to %s (%s %q)", direction, entry.Role, entry.Name), nil
	}

	dy := amount
	if direction == "up" {
		dy = -amount
	}
	js := fmt.Sprintf("window.scrollBy(0, %d)", dy)
	if err := chromedp.Run(m.ctx, m.evaluate(js, nil)); err != nil {
		return "", fmt.Errorf("scroll: %w", err)
	}
	return fmt.Sprintf("Scrolled %s %dpx", direction, amount), nil
}

func (m *Manager) cmdWait(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: wait <ms|selector|--networkidle [ms]|--load [ms]|--domcontentloaded [ms]>")
	}
	arg := args[0]

	if strings.HasPrefix(arg, "--") {
		timeout, err := parseOptionalWaitTimeout(args[1:], 15*time.Second)
		if err != nil {
			return "", err
		}

		switch arg {
		case "--networkidle":
			if err := m.waitNetworkIdle(timeout); err != nil {
				return "", err
			}
			return fmt.Sprintf("Network is idle (timeout %s)", timeout), nil
		case "--load":
			if err := m.waitDocumentReadyState(timeout, true); err != nil {
				return "", err
			}
			return "Load event completed", nil
		case "--domcontentloaded":
			if err := m.waitDocumentReadyState(timeout, false); err != nil {
				return "", err
			}
			return "DOMContentLoaded completed", nil
		default:
			return "", fmt.Errorf("unknown wait mode: %s", arg)
		}
	}

	// Try as milliseconds
	if ms, err := strconv.Atoi(arg); err == nil {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return fmt.Sprintf("Waited %dms", ms), nil
	}

	// CSS selector
	if err := chromedp.Run(m.ctx, chromedp.WaitVisible(arg, chromedp.ByQuery)); err != nil {
		return "", fmt.Errorf("wait for %q: %w", arg, err)
	}
	return fmt.Sprintf("Element %q is visible", arg), nil
}

func parseOptionalWaitTimeout(args []string, defaultTimeout time.Duration) (time.Duration, error) {
	if len(args) == 0 {
		return defaultTimeout, nil
	}
	if len(args) > 1 {
		return 0, fmt.Errorf("too many arguments for wait mode")
	}

	ms, err := strconv.Atoi(args[0])
	if err != nil || ms <= 0 {
		return 0, fmt.Errorf("invalid timeout %q (expected positive milliseconds)", args[0])
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (m *Manager) waitDocumentReadyState(timeout time.Duration, requireLoad bool) error {
	if timeout <= 0 {
		return fmt.Errorf("timeout must be greater than 0")
	}

	deadline := time.Now().Add(timeout)
	const pollEvery = 50 * time.Millisecond

	for time.Now().Before(deadline) {
		var readyState string
		if err := chromedp.Run(m.ctx, m.evaluate(`document.readyState`, &readyState)); err != nil {
			return fmt.Errorf("document.readyState: %w", err)
		}

		if requireLoad {
			if readyState == "complete" {
				return nil
			}
		} else if readyState == "interactive" || readyState == "complete" {
			return nil
		}

		time.Sleep(pollEvery)
	}

	if requireLoad {
		return fmt.Errorf("load timeout after %s", timeout)
	}
	return fmt.Errorf("domcontentloaded timeout after %s", timeout)
}

// --- Read ---

func (m *Manager) cmdText(args []string) (string, error) {
	if len(args) > 0 && strings.HasPrefix(args[0], "@") {
		entry, err := m.ResolveRef(args[0])
		if err != nil {
			return "", err
		}
		var text string
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			obj, err := dom.ResolveNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return err
			}
			res, _, err := runtime.CallFunctionOn("function() { return this.innerText || this.textContent || ''; }").WithObjectID(obj.ObjectID).Do(ctx)
			if err != nil {
				return err
			}
			if res.Value != nil {
				text = strings.Trim(string(res.Value), `"`)
			}
			return nil
		})); err != nil {
			return "", err
		}
		return text, nil
	}

	var text string
	if err := chromedp.Run(m.ctx, m.evaluate(`document.body.innerText`, &text)); err != nil {
		return "", fmt.Errorf("text: %w", err)
	}
	return text, nil
}

func (m *Manager) cmdHTML(args []string) (string, error) {
	if len(args) > 0 && strings.HasPrefix(args[0], "@") {
		entry, err := m.ResolveRef(args[0])
		if err != nil {
			return "", err
		}
		var html string
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			h, err := dom.GetOuterHTML().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return err
			}
			html = h
			return nil
		})); err != nil {
			return "", err
		}
		return html, nil
	}

	var html string
	if err := chromedp.Run(m.ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery)); err != nil {
		return "", fmt.Errorf("html: %w", err)
	}
	return html, nil
}

func (m *Manager) cmdLinks() (string, error) {
	var result string
	js := `(function() {
		var links = document.querySelectorAll('a[href]');
		var out = [];
		links.forEach(function(a) {
			var text = (a.innerText || '').trim().replace(/\n/g, ' ').substring(0, 80);
			out.push(text + ' → ' + a.href);
		});
		return out.join('\n');
	})()`
	if err := chromedp.Run(m.ctx, m.evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("links: %w", err)
	}
	if result == "" {
		return "(no links)", nil
	}
	return result, nil
}

func (m *Manager) cmdJS(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: js <expression>")
	}
	expr := strings.Join(args, " ")

	var result any
	if err := chromedp.Run(m.ctx, m.evaluate(expr, &result)); err != nil {
		return "", fmt.Errorf("js: %w", err)
	}
	return fmt.Sprintf("%v", result), nil
}

// --- Visual ---

func (m *Manager) cmdScreenshot(args []string) (string, error) {
	outPath := ""
	viewportOnly := false
	var clip *page.Viewport
	var scale float64
	var width int

	usage := "usage: screenshot [--viewport] [--clip x,y,w,h] [--scale N] [--width N] [path]"

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--viewport":
			viewportOnly = true
		case arg == "--clip":
			if i+1 >= len(args) {
				return "", errors.New(usage)
			}
			parsedClip, err := parseScreenshotClip(args[i+1])
			if err != nil {
				return "", err
			}
			clip = parsedClip
			i++
		case strings.HasPrefix(arg, "--clip="):
			parsedClip, err := parseScreenshotClip(strings.TrimPrefix(arg, "--clip="))
			if err != nil {
				return "", err
			}
			clip = parsedClip
		case arg == "--scale":
			if i+1 >= len(args) {
				return "", errors.New(usage)
			}
			v, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil || v <= 0 {
				return "", fmt.Errorf("--scale must be a positive number, got %q", args[i+1])
			}
			scale = v
			i++
		case strings.HasPrefix(arg, "--scale="):
			v, err := strconv.ParseFloat(strings.TrimPrefix(arg, "--scale="), 64)
			if err != nil || v <= 0 {
				return "", fmt.Errorf("--scale must be a positive number, got %q", strings.TrimPrefix(arg, "--scale="))
			}
			scale = v
		case arg == "--width":
			if i+1 >= len(args) {
				return "", errors.New(usage)
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v <= 0 {
				return "", fmt.Errorf("--width must be a positive integer, got %q", args[i+1])
			}
			width = v
			i++
		case strings.HasPrefix(arg, "--width="):
			v, err := strconv.Atoi(strings.TrimPrefix(arg, "--width="))
			if err != nil || v <= 0 {
				return "", fmt.Errorf("--width must be a positive integer, got %q", strings.TrimPrefix(arg, "--width="))
			}
			width = v
		case strings.HasPrefix(arg, "-"):
			return "", fmt.Errorf("unknown screenshot flag: %s", arg)
		default:
			if outPath != "" {
				return "", errors.New(usage)
			}
			outPath = arg
		}
	}

	if viewportOnly && clip != nil {
		return "", fmt.Errorf("--viewport and --clip cannot be used together")
	}

	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), fmt.Sprintf("browse-screenshot-%d.png", time.Now().UnixMilli()))
	}
	resolvedPath, err := validateOutputPath(outPath)
	if err != nil {
		return "", fmt.Errorf("screenshot: %w", err)
	}
	outPath = resolvedPath

	// Apply --scale: override DPR via EmulateViewport, restore after capture.
	if scale > 0 {
		// Read current viewport dimensions
		var metricsJSON string
		if err := chromedp.Run(m.ctx, m.evaluate(
			`JSON.stringify({w: window.innerWidth, h: window.innerHeight})`,
			&metricsJSON,
		)); err != nil {
			return "", fmt.Errorf("screenshot: get viewport metrics: %w", err)
		}
		var metrics struct {
			W int64 `json:"w"`
			H int64 `json:"h"`
		}
		if err := json.Unmarshal([]byte(metricsJSON), &metrics); err != nil {
			return "", fmt.Errorf("screenshot: parse viewport metrics: %w", err)
		}
		if metrics.W == 0 {
			metrics.W = 1280
		}
		if metrics.H == 0 {
			metrics.H = 720
		}

		if err := chromedp.Run(m.ctx, chromedp.EmulateViewport(metrics.W, metrics.H, chromedp.EmulateScale(scale))); err != nil {
			return "", fmt.Errorf("screenshot: set scale: %w", err)
		}
		defer chromedp.Run(m.ctx, chromedp.EmulateViewport(metrics.W, metrics.H))
	}

	var buf []byte
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		capture := page.CaptureScreenshot().
			WithFormat(page.CaptureScreenshotFormatPng).
			WithFromSurface(true)

		if clip != nil {
			capture = capture.WithCaptureBeyondViewport(false).WithClip(clip)
		} else if viewportOnly {
			capture = capture.WithCaptureBeyondViewport(false)
		} else {
			capture = capture.WithCaptureBeyondViewport(true)
		}

		data, err := capture.Do(ctx)
		if err != nil {
			return err
		}
		buf = data
		return nil
	})); err != nil {
		return "", fmt.Errorf("screenshot: %w", err)
	}

	// Decode base64 if needed (chromedp returns raw bytes)
	if len(buf) > 0 && buf[0] != 0x89 { // not PNG header
		decoded, err := base64.StdEncoding.DecodeString(string(buf))
		if err == nil {
			buf = decoded
		}
	}

	// Apply --width: resize PNG to target width, preserving aspect ratio.
	if width > 0 {
		resized, err := resizePNG(buf, width)
		if err != nil {
			return "", fmt.Errorf("screenshot: resize: %w", err)
		}
		buf = resized
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, buf, 0644); err != nil {
		return "", fmt.Errorf("write screenshot: %w", err)
	}
	return fmt.Sprintf("Screenshot saved to %s", outPath), nil
}

// resizePNG decodes PNG data, resizes to targetWidth preserving aspect ratio,
// and re-encodes as PNG.
func resizePNG(data []byte, targetWidth int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	srcBounds := src.Bounds()
	origW := srcBounds.Dx()
	origH := srcBounds.Dy()
	if origW == 0 {
		return nil, fmt.Errorf("source image has zero width")
	}
	newH := origH * targetWidth / origW
	if newH == 0 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, newH))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, srcBounds, draw.Over, nil)

	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return out.Bytes(), nil
}

func parseScreenshotClip(raw string) (*page.Viewport, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid clip %q (expected x,y,w,h)", raw)
	}

	values := make([]float64, 4)
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid clip %q (expected x,y,w,h)", raw)
		}
		values[i] = v
	}

	if values[2] <= 0 || values[3] <= 0 {
		return nil, fmt.Errorf("clip width and height must be greater than 0")
	}

	return &page.Viewport{
		X:      values[0],
		Y:      values[1],
		Width:  values[2],
		Height: values[3],
		Scale:  1,
	}, nil
}

// --- Tabs ---

func (m *Manager) cmdTabs() (string, error) {
	var result string
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		targets, err := target.GetTargets().Do(ctx)
		if err != nil {
			return err
		}
		var sb strings.Builder
		for i, t := range targets {
			if t.Type != "page" {
				continue
			}
			fmt.Fprintf(&sb, "[%d] %s\n    %s\n", i, t.Title, t.URL)
		}
		result = sb.String()
		return nil
	})); err != nil {
		return "", fmt.Errorf("tabs: %w", err)
	}
	if result == "" {
		return "(no tabs)", nil
	}
	return result, nil
}

func (m *Manager) cmdTab(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: tab <index>")
	}
	idx, err := strconv.Atoi(args[0])
	if err != nil {
		return "", fmt.Errorf("invalid tab index: %s", args[0])
	}

	var activated string
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
		t := pages[idx]
		if err := target.ActivateTarget(t.TargetID).Do(ctx); err != nil {
			return err
		}
		activated = fmt.Sprintf("Switched to tab [%d] %s\n%s", idx, t.Title, t.URL)
		return nil
	})); err != nil {
		return "", err
	}
	m.clearActiveFrame()
	return activated, nil
}
