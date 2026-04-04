package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// --- Read Commands ---

// cmdForms scans all <form> elements and lists their fields.
func (m *Manager) cmdForms(args []string) (string, error) {
	js := `(function() {
		return JSON.stringify([...document.querySelectorAll('form')].map(function(form, i) {
			var fields = [...form.querySelectorAll('input, select, textarea')].map(function(el) {
				var input = el;
				var result = {
					tag: el.tagName.toLowerCase(),
					type: input.type || undefined,
					name: input.name || undefined,
					id: input.id || undefined,
					placeholder: input.placeholder || undefined,
					required: input.required || undefined,
					value: input.type === 'password' ? '[redacted]' : (input.value || undefined)
				};
				if (el.tagName === 'SELECT') {
					result.options = [...el.options].map(function(o) {
						return {value: o.value, text: o.text};
					});
				}
				return result;
			});
			return {
				index: i,
				action: form.action || undefined,
				method: form.method || 'get',
				id: form.id || undefined,
				fields: fields
			};
		}));
	})()`

	var result string
	if err := chromedp.Run(m.ctx, m.evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("forms: %w", err)
	}

	// Pretty-print the JSON
	var parsed any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return result, nil
	}
	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return result, nil
	}
	return string(pretty), nil
}

// cmdCSS gets the computed CSS property of an element.
// Usage: css <@ref|selector> <property>
func (m *Manager) cmdCSS(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: css <@ref|selector> <property>")
	}
	sel := args[0]
	property := args[1]

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		var value string
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			obj, err := dom.ResolveNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return err
			}
			jsExpr := fmt.Sprintf(`function() { return getComputedStyle(this).getPropertyValue(%q); }`, property)
			res, _, err := runtime.CallFunctionOn(jsExpr).WithObjectID(obj.ObjectID).Do(ctx)
			if err != nil {
				return err
			}
			if res.Value != nil {
				json.Unmarshal(res.Value, &value)
			}
			return nil
		})); err != nil {
			return "", fmt.Errorf("css %s: %w", sel, err)
		}
		return value, nil
	}

	// CSS selector
	js := fmt.Sprintf(`(function() {
		var el = document.querySelector(%q);
		if (!el) return 'Element not found: %s';
		return getComputedStyle(el).getPropertyValue(%q);
	})()`, sel, sel, property)
	var value string
	if err := chromedp.Run(m.ctx, m.evaluate(js, &value)); err != nil {
		return "", fmt.Errorf("css: %w", err)
	}
	return value, nil
}

// cmdAttrs gets all attributes of an element as JSON.
// Usage: attrs <@ref|selector>
func (m *Manager) cmdAttrs(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: attrs <@ref|selector>")
	}
	sel := args[0]

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		var result string
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			obj, err := dom.ResolveNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return err
			}
			jsExpr := `function() {
				var result = {};
				for (var i = 0; i < this.attributes.length; i++) {
					result[this.attributes[i].name] = this.attributes[i].value;
				}
				return JSON.stringify(result);
			}`
			res, _, err := runtime.CallFunctionOn(jsExpr).WithObjectID(obj.ObjectID).Do(ctx)
			if err != nil {
				return err
			}
			if res.Value != nil {
				json.Unmarshal(res.Value, &result)
			}
			return nil
		})); err != nil {
			return "", fmt.Errorf("attrs %s: %w", sel, err)
		}

		// Pretty-print
		var parsed any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			return result, nil
		}
		pretty, err := json.MarshalIndent(parsed, "", "  ")
		if err != nil {
			return result, nil
		}
		return string(pretty), nil
	}

	// CSS selector
	js := fmt.Sprintf(`(function() {
		var el = document.querySelector(%q);
		if (!el) return JSON.stringify('Element not found: %s');
		var result = {};
		for (var i = 0; i < el.attributes.length; i++) {
			result[el.attributes[i].name] = el.attributes[i].value;
		}
		return JSON.stringify(result);
	})()`, sel, sel)
	var result string
	if err := chromedp.Run(m.ctx, m.evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("attrs: %w", err)
	}

	var parsed any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return result, nil
	}
	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return result, nil
	}
	return string(pretty), nil
}

// cmdIs checks element properties: visible, hidden, enabled, disabled, checked, focused, editable.
// Usage: is <property> <@ref|selector>
func (m *Manager) cmdIs(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("usage: is <property> <@ref|selector>\nProperties: visible, hidden, enabled, disabled, checked, focused, editable")
	}
	property := args[0]
	sel := args[1]

	validProps := map[string]bool{
		"visible": true, "hidden": true, "enabled": true,
		"disabled": true, "checked": true, "focused": true, "editable": true,
	}
	if !validProps[property] {
		return "", fmt.Errorf("unknown property: %s. Use: visible, hidden, enabled, disabled, checked, focused, editable", property)
	}

	var jsCheck string
	switch property {
	case "visible":
		jsCheck = `(function() {
			var rect = this.getBoundingClientRect();
			return this.offsetParent !== null && rect.width > 0 && rect.height > 0;
		})`
	case "hidden":
		jsCheck = `(function() {
			var rect = this.getBoundingClientRect();
			return this.offsetParent === null || rect.width === 0 || rect.height === 0;
		})`
	case "enabled":
		jsCheck = `(function() { return !this.disabled; })`
	case "disabled":
		jsCheck = `(function() { return !!this.disabled; })`
	case "checked":
		jsCheck = `(function() { return !!this.checked; })`
	case "focused":
		jsCheck = `(function() { return this === document.activeElement; })`
	case "editable":
		jsCheck = `(function() {
			return !this.disabled && !this.readOnly &&
				(this.tagName === 'INPUT' || this.tagName === 'TEXTAREA' || this.isContentEditable);
		})`
	}

	if strings.HasPrefix(sel, "@") {
		entry, err := m.ResolveRef(sel)
		if err != nil {
			return "", err
		}
		var result bool
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			obj, err := dom.ResolveNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return err
			}
			res, _, err := runtime.CallFunctionOn(jsCheck).WithObjectID(obj.ObjectID).Do(ctx)
			if err != nil {
				return err
			}
			if res.Value != nil {
				json.Unmarshal(res.Value, &result)
			}
			return nil
		})); err != nil {
			return "", fmt.Errorf("is %s %s: %w", property, sel, err)
		}
		return fmt.Sprintf("%t", result), nil
	}

	// CSS selector
	js := fmt.Sprintf(`(function() {
		var el = document.querySelector(%q);
		if (!el) return 'Element not found: %s';
		return (%s).call(el);
	})()`, sel, sel, jsCheck)
	var result any
	if err := chromedp.Run(m.ctx, m.evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("is: %w", err)
	}
	return fmt.Sprintf("%v", result), nil
}

// cmdCookies lists all cookies for the current page using the CDP network.GetCookies API.
func (m *Manager) cmdCookies(args []string) (string, error) {
	var cookies []*network.Cookie
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().Do(ctx)
		return err
	})); err != nil {
		return "", fmt.Errorf("cookies: %w", err)
	}

	if len(cookies) == 0 {
		return "(no cookies)", nil
	}

	data, err := json.MarshalIndent(cookies, "", "  ")
	if err != nil {
		return "", fmt.Errorf("cookies marshal: %w", err)
	}
	return string(data), nil
}

// Patterns for sensitive key/value redaction in storage.
var (
	sensitiveKeyRe   = regexp.MustCompile(`(?i)(^|[_.\-])(token|secret|key|password|credential|auth|jwt|session|csrf)($|[_.\-])|api.?key`)
	sensitiveValueRe = regexp.MustCompile(`^(eyJ|sk-|sk_live_|sk_test_|pk_live_|pk_test_|rk_live_|sk-ant-|ghp_|gho_|github_pat_|xox[bpsa]-|AKIA[A-Z0-9]{16}|AIza|SG\.|Bearer\s|sbp_)`)
)

// cmdStorage gets localStorage and sessionStorage, redacting sensitive values.
func (m *Manager) cmdStorage(args []string) (string, error) {
	if len(args) > 0 {
		if args[0] != "set" {
			return "", fmt.Errorf("usage: storage [set <key> <value>]")
		}
		if len(args) < 3 {
			return "", fmt.Errorf("usage: storage set <key> <value>")
		}

		key := args[1]
		value := strings.Join(args[2:], " ")
		js := fmt.Sprintf(`(function() {
			localStorage.setItem(%q, %q);
			return true;
		})()`, key, value)
		if err := chromedp.Run(m.ctx, m.evaluate(js, nil)); err != nil {
			return "", fmt.Errorf("storage set: %w", err)
		}
		return fmt.Sprintf("localStorage[%q] set", key), nil
	}

	js := `(function() {
		var ls = {};
		for (var i = 0; i < localStorage.length; i++) {
			var key = localStorage.key(i);
			ls[key] = localStorage.getItem(key);
		}
		var ss = {};
		for (var i = 0; i < sessionStorage.length; i++) {
			var key = sessionStorage.key(i);
			ss[key] = sessionStorage.getItem(key);
		}
		return JSON.stringify({localStorage: ls, sessionStorage: ss});
	})()`

	var result string
	if err := chromedp.Run(m.ctx, m.evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("storage: %w", err)
	}

	// Parse and redact sensitive values
	var storage map[string]map[string]string
	if err := json.Unmarshal([]byte(result), &storage); err != nil {
		return result, nil
	}

	for _, store := range storage {
		for key, value := range store {
			if sensitiveKeyRe.MatchString(key) || sensitiveValueRe.MatchString(value) {
				store[key] = fmt.Sprintf("[REDACTED — %d chars]", len(value))
			}
		}
	}

	pretty, err := json.MarshalIndent(storage, "", "  ")
	if err != nil {
		return result, nil
	}
	return string(pretty), nil
}

// cmdPerf returns navigation performance timing data.
func (m *Manager) cmdPerf(args []string) (string, error) {
	js := `(function() {
		var nav = performance.getEntriesByType('navigation')[0];
		if (!nav) return JSON.stringify('No navigation timing data available.');
		return JSON.stringify({
			dns: Math.round(nav.domainLookupEnd - nav.domainLookupStart),
			tcp: Math.round(nav.connectEnd - nav.connectStart),
			ssl: Math.round(nav.secureConnectionStart > 0 ? nav.connectEnd - nav.secureConnectionStart : 0),
			ttfb: Math.round(nav.responseStart - nav.requestStart),
			download: Math.round(nav.responseEnd - nav.responseStart),
			domParse: Math.round(nav.domInteractive - nav.responseEnd),
			domReady: Math.round(nav.domContentLoadedEventEnd - nav.startTime),
			load: Math.round(nav.loadEventEnd - nav.startTime),
			total: Math.round(nav.loadEventEnd - nav.startTime)
		});
	})()`

	var result string
	if err := chromedp.Run(m.ctx, m.evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("perf: %w", err)
	}

	// Check if it's a plain error string
	var errStr string
	if err := json.Unmarshal([]byte(result), &errStr); err == nil {
		return errStr, nil
	}

	// Parse as timing object and format
	var timings map[string]int
	if err := json.Unmarshal([]byte(result), &timings); err != nil {
		return result, nil
	}

	// Ordered keys for consistent output
	keys := []string{"dns", "tcp", "ssl", "ttfb", "download", "domParse", "domReady", "load", "total"}
	var sb strings.Builder
	for _, k := range keys {
		v := timings[k]
		// Pad key to 12 chars
		padded := k + strings.Repeat(" ", 12-len(k))
		fmt.Fprintf(&sb, "%s %dms\n", padded, v)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// cmdEval executes a JavaScript file.
// Usage: eval <js-file-path>
func (m *Manager) cmdEval(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: eval <js-file>")
	}
	filePath, err := validateOutputPath(args[0])
	if err != nil {
		return "", fmt.Errorf("eval: %w", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("eval: read file: %w", err)
	}
	code := string(data)

	// Wrap in async IIFE if code contains await
	wrapped := wrapForEvaluate(code)

	var result any
	if err := chromedp.Run(m.ctx, m.evaluate(wrapped, &result)); err != nil {
		return "", fmt.Errorf("eval: %w", err)
	}

	if result == nil {
		return "", nil
	}

	switch v := result.(type) {
	case string:
		return v, nil
	case map[string]any, []any:
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v), nil
		}
		return string(data), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// hasAwait detects await keyword in code, ignoring comments.
func hasAwait(code string) bool {
	// Strip single-line comments ((?m) enables multiline mode so $ matches end-of-line)
	stripped := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(code, "")
	// Strip multi-line comments
	stripped = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(stripped, "")
	return regexp.MustCompile(`\bawait\b`).MatchString(stripped)
}

// needsBlockWrapper detects whether code needs a block wrapper inside an async IIFE.
func needsBlockWrapper(code string) bool {
	trimmed := strings.TrimSpace(code)
	if strings.Count(trimmed, "\n") > 0 {
		return true
	}
	if regexp.MustCompile(`\b(const|let|var|function|class|return|throw|if|for|while|switch|try)\b`).MatchString(trimmed) {
		return true
	}
	if strings.Contains(trimmed, ";") {
		return true
	}
	return false
}

// wrapForEvaluate wraps code for chromedp.Evaluate, using async IIFE when await is detected.
func wrapForEvaluate(code string) string {
	if !hasAwait(code) {
		return code
	}
	trimmed := strings.TrimSpace(code)
	if needsBlockWrapper(trimmed) {
		return "(async()=>{\n" + code + "\n})()"
	}
	return "(async()=>(" + trimmed + "))()"
}
