package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// --- Write Commands ---

// keyMap maps friendly key names to CDP key definitions.
var keyMap = map[string]struct {
	key          string
	code         string
	keyCode      int
	nativeVKCode int
}{
	"enter":      {"Enter", "Enter", 13, 13},
	"tab":        {"Tab", "Tab", 9, 9},
	"escape":     {"Escape", "Escape", 27, 27},
	"backspace":  {"Backspace", "Backspace", 8, 8},
	"delete":     {"Delete", "Delete", 46, 46},
	"space":      {" ", "Space", 32, 32},
	"arrowleft":  {"ArrowLeft", "ArrowLeft", 37, 37},
	"arrowright": {"ArrowRight", "ArrowRight", 39, 39},
	"arrowup":    {"ArrowUp", "ArrowUp", 38, 38},
	"arrowdown":  {"ArrowDown", "ArrowDown", 40, 40},
	"home":       {"Home", "Home", 36, 36},
	"end":        {"End", "End", 35, 35},
	"pageup":     {"PageUp", "PageUp", 33, 33},
	"pagedown":   {"PageDown", "PageDown", 34, 34},
	"f1":         {"F1", "F1", 112, 112},
	"f2":         {"F2", "F2", 113, 113},
	"f3":         {"F3", "F3", 114, 114},
	"f4":         {"F4", "F4", 115, 115},
	"f5":         {"F5", "F5", 116, 116},
	"f6":         {"F6", "F6", 117, 117},
	"f7":         {"F7", "F7", 118, 118},
	"f8":         {"F8", "F8", 119, 119},
	"f9":         {"F9", "F9", 120, 120},
	"f10":        {"F10", "F10", 121, 121},
	"f11":        {"F11", "F11", 122, 122},
	"f12":        {"F12", "F12", 123, 123},
}

type importedCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// cmdPress dispatches a key press (KeyDown + KeyUp).
// Usage: press <key>
func (m *Manager) cmdPress(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: press <key> (e.g., Enter, Tab, Escape, ArrowLeft)")
	}
	keyName := args[0]
	lower := strings.ToLower(keyName)

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if info, ok := keyMap[lower]; ok {
			if err := input.DispatchKeyEvent(input.KeyDown).
				WithKey(info.key).
				WithCode(info.code).
				WithWindowsVirtualKeyCode(int64(info.keyCode)).
				WithNativeVirtualKeyCode(int64(info.nativeVKCode)).
				Do(ctx); err != nil {
				return err
			}
			return input.DispatchKeyEvent(input.KeyUp).
				WithKey(info.key).
				WithCode(info.code).
				WithWindowsVirtualKeyCode(int64(info.keyCode)).
				WithNativeVirtualKeyCode(int64(info.nativeVKCode)).
				Do(ctx)
		}
		// Single character key
		if err := input.DispatchKeyEvent(input.KeyDown).
			WithKey(keyName).
			WithText(keyName).
			Do(ctx); err != nil {
			return err
		}
		return input.DispatchKeyEvent(input.KeyUp).
			WithKey(keyName).
			Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("press %s: %w", keyName, err)
	}
	return fmt.Sprintf("Pressed %s", keyName), nil
}

// cmdViewport sets the browser viewport dimensions.
// Usage: viewport <WxH> (e.g., 375x812)
func (m *Manager) cmdViewport(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: viewport <WxH> (e.g., 375x812)")
	}
	size := args[0]
	if !strings.Contains(size, "x") {
		return "", fmt.Errorf("usage: viewport <WxH> (e.g., 375x812)")
	}

	parts := strings.SplitN(size, "x", 2)
	w, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid width: %s", parts[0])
	}
	h, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid height: %s", parts[1])
	}

	if err := chromedp.Run(m.ctx, chromedp.EmulateViewport(w, h)); err != nil {
		return "", fmt.Errorf("viewport: %w", err)
	}
	return fmt.Sprintf("Viewport set to %dx%d", w, h), nil
}

// cmdUserAgent sets a custom User-Agent for subsequent requests.
// Usage: useragent <string>
func (m *Manager) cmdUserAgent(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: useragent <string>")
	}
	ua := strings.TrimSpace(strings.Join(args, " "))
	if ua == "" {
		return "", fmt.Errorf("usage: useragent <string>")
	}

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetUserAgentOverride(ua).Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("useragent: %w", err)
	}
	return fmt.Sprintf("User-Agent set: %s", ua), nil
}

// cmdCookie sets a cookie on the current page domain.
// Usage: cookie <name>=<value>
func (m *Manager) cmdCookie(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: cookie <name>=<value>")
	}
	cookieStr := args[0]
	eqIdx := strings.Index(cookieStr, "=")
	if eqIdx < 0 {
		return "", fmt.Errorf("usage: cookie <name>=<value>")
	}
	name := cookieStr[:eqIdx]
	value := cookieStr[eqIdx+1:]

	currentURL := m.CurrentURL()
	parsed, err := url.Parse(currentURL)
	if err != nil {
		return "", fmt.Errorf("parse current URL: %w", err)
	}
	domain := parsed.Hostname()

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return network.SetCookie(name, value).
			WithDomain(domain).
			WithPath("/").
			Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("cookie: %w", err)
	}
	return fmt.Sprintf("Cookie set: %s=****", name), nil
}

// cmdCookieImport imports cookies from a JSON file.
// Usage: cookie-import <json-file>
func (m *Manager) cmdCookieImport(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: cookie-import <json-file>")
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		return "", fmt.Errorf("cookie-import: read %s: %w", args[0], err)
	}

	var cookies []importedCookie
	if err := json.Unmarshal(data, &cookies); err != nil {
		return "", fmt.Errorf("cookie-import: parse %s: %w", args[0], err)
	}

	defaultDomain := ""
	if parsed, err := url.Parse(m.CurrentURL()); err == nil {
		defaultDomain = parsed.Hostname()
	}

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		for i, c := range cookies {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				return fmt.Errorf("cookie %d missing name", i+1)
			}

			domain := strings.TrimSpace(c.Domain)
			if domain == "" {
				domain = defaultDomain
			}
			if domain == "" {
				return fmt.Errorf("cookie %d (%s) missing domain and current page has no hostname", i+1, name)
			}

			path := strings.TrimSpace(c.Path)
			if path == "" {
				path = "/"
			}

			if err := network.SetCookie(name, c.Value).
				WithDomain(domain).
				WithPath(path).
				Do(ctx); err != nil {
				return fmt.Errorf("set cookie %d (%s): %w", i+1, name, err)
			}
		}
		return nil
	})); err != nil {
		return "", fmt.Errorf("cookie-import: %w", err)
	}

	return fmt.Sprintf("Imported %d cookies from %s", len(cookies), args[0]), nil
}

// cmdHeader sets a custom HTTP header for subsequent requests.
// Usage: header <name>:<value>
func (m *Manager) cmdHeader(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: header <name>:<value>")
	}
	headerStr := args[0]
	sepIdx := strings.Index(headerStr, ":")
	if sepIdx < 0 {
		return "", fmt.Errorf("usage: header <name>:<value>")
	}
	name := strings.TrimSpace(headerStr[:sepIdx])
	value := strings.TrimSpace(headerStr[sepIdx+1:])

	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		headers := network.Headers{name: value}
		return network.SetExtraHTTPHeaders(headers).Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("header: %w", err)
	}

	// Redact sensitive header values
	sensitiveHeaders := map[string]bool{
		"authorization": true, "cookie": true, "set-cookie": true,
		"x-api-key": true, "x-auth-token": true,
	}
	redactedValue := value
	if sensitiveHeaders[strings.ToLower(name)] {
		redactedValue = "****"
	}
	return fmt.Sprintf("Header set: %s: %s", name, redactedValue), nil
}

// cmdUpload sets files on a file input element.
// Usage: upload <@ref|selector> <file1> [file2...]
func (m *Manager) cmdUpload(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: upload <@ref|selector> <file1> [file2...]")
	}
	sel := args[0]
	filePaths := args[1:]

	// Validate all files exist
	for _, fp := range filePaths {
		if _, err := os.Stat(fp); err != nil {
			return "", fmt.Errorf("file not found: %s", fp)
		}
	}

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			// Resolve BackendNodeID to NodeID
			node, err := dom.DescribeNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return fmt.Errorf("describe node: %w", err)
			}
			return dom.SetFileInputFiles(filePaths).WithNodeID(node.NodeID).Do(ctx)
		})); err != nil {
			return "", fmt.Errorf("upload %s: %w", sel, err)
		}
	} else {
		// CSS selector — get node IDs first
		var nodeIDs []cdp.NodeID
		if err := chromedp.Run(m.ctx, chromedp.NodeIDs(sel, &nodeIDs, chromedp.ByQuery)); err != nil {
			return "", fmt.Errorf("upload: find element %q: %w", sel, err)
		}
		if len(nodeIDs) == 0 {
			return "", fmt.Errorf("upload: element not found: %s", sel)
		}
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return dom.SetFileInputFiles(filePaths).WithNodeID(nodeIDs[0]).Do(ctx)
		})); err != nil {
			return "", fmt.Errorf("upload: %w", err)
		}
	}

	// Build file info
	var infos []string
	for _, fp := range filePaths {
		stat, _ := os.Stat(fp)
		infos = append(infos, fmt.Sprintf("%s (%dB)", filepath.Base(fp), stat.Size()))
	}
	return fmt.Sprintf("Uploaded: %s", strings.Join(infos, ", ")), nil
}

// cmdDialogAccept sets the dialog auto-accept mode.
// Usage: dialog-accept [text]
func (m *Manager) cmdDialogAccept(args []string) (string, error) {
	if len(args) > 0 {
		text := strings.Join(args, " ")
		m.SetDialogAutoMode(true, text)
		return fmt.Sprintf("Dialogs will be accepted with text: %q", text), nil
	}
	m.SetDialogAutoMode(true, "")
	return "Dialogs will be accepted", nil
}

// cmdDialogDismiss sets the dialog auto-dismiss mode.
func (m *Manager) cmdDialogDismiss(args []string) (string, error) {
	m.SetDialogAutoMode(false, "")
	return "Dialogs will be dismissed", nil
}
