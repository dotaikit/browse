package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// pendingRequest tracks an in-flight network request for duration calculation.
type pendingRequest struct {
	index     int   // position in the networkBuffer
	timestamp int64 // request start time (unix ms)
}

func (m *Manager) startEventListeners() error {
	// pendingRequests maps CDP request ID to buffer index + start time.
	// Protected by its own mutex to avoid contention with buffer locks.
	var pendingMu sync.Mutex
	pendingRequests := make(map[network.RequestID]pendingRequest)

	chromedp.ListenTarget(m.ctx, func(ev any) {
		switch e := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			m.consoleBuffer.Add(consoleEntryFromEvent(e))

		case *network.EventRequestWillBeSent:
			entry := NetworkEntry{
				Timestamp: time.Now().UnixMilli(),
				Method:    e.Request.Method,
				URL:       e.Request.URL,
			}
			m.networkBuffer.Add(entry)

			pendingMu.Lock()
			pendingRequests[e.RequestID] = pendingRequest{
				index:     m.networkBuffer.Len() - 1,
				timestamp: entry.Timestamp,
			}
			pendingMu.Unlock()

		case *network.EventResponseReceived:
			pendingMu.Lock()
			pending, ok := pendingRequests[e.RequestID]
			if ok {
				delete(pendingRequests, e.RequestID)
			}
			pendingMu.Unlock()

			if ok && e.Response != nil {
				status := int(e.Response.Status)
				size := int(e.Response.EncodedDataLength)
				duration := time.Now().UnixMilli() - pending.timestamp

				updated, found := m.networkBuffer.Get(pending.index)
				if found && updated.URL == e.Response.URL {
					updated.Status = status
					updated.Duration = duration
					updated.Size = size
					m.networkBuffer.Set(pending.index, updated)
				}
			}

		case *page.EventJavascriptDialogOpening:
			m.dialogBuffer.Add(DialogEntry{
				Timestamp:    time.Now().UnixMilli(),
				Type:         string(e.Type),
				Message:      e.Message,
				DefaultValue: e.DefaultPrompt,
			})

			mode := m.GetDialogAutoMode()
			if mode.enabled {
				// Handle dialog in a goroutine to avoid deadlocking on
				// chromedp's listenersMu (we are inside a listener callback).
				// Use cdp.WithExecutor to bypass chromedp.Run.
				go func() {
					action := page.HandleJavaScriptDialog(mode.accept)
					if mode.accept && mode.promptText != "" {
						action = action.WithPromptText(mode.promptText)
					}
					_ = action.Do(cdp.WithExecutor(m.ctx, chromedp.FromContext(m.ctx).Target))
				}()
			}

		case *page.EventJavascriptDialogClosed:
			// Update the most recent dialog entry with the action result.
			n := m.dialogBuffer.Len()
			if n > 0 {
				entry, ok := m.dialogBuffer.Get(n - 1)
				if ok {
					if e.Result {
						entry.Action = "accepted"
					} else {
						entry.Action = "dismissed"
					}
					entry.Response = e.UserInput
					m.dialogBuffer.Set(n-1, entry)
				}
			}

		case *page.EventFrameNavigated:
			if e.Frame == nil {
				return
			}
			// Main-frame navigation and active iframe navigation both invalidate
			// the frame execution context and cached refs.
			if e.Frame.ParentID == "" || e.Frame.ID == m.currentActiveFrameID() {
				m.clearActiveFrame()
				m.ClearRefs()
			}
		}
	})

	return chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := runtime.Enable().Do(ctx); err != nil {
			return fmt.Errorf("enable runtime monitoring: %w", err)
		}
		if err := network.Enable().Do(ctx); err != nil {
			return fmt.Errorf("enable network monitoring: %w", err)
		}
		if err := page.Enable().Do(ctx); err != nil {
			return fmt.Errorf("enable page monitoring: %w", err)
		}
		return nil
	}))
}

// --- Console command ---

func (m *Manager) cmdConsole(args []string) (string, error) {
	for _, arg := range args {
		if arg == "--clear" {
			m.consoleBuffer.Clear()
			return "console buffer cleared", nil
		}
	}

	entries := m.consoleBuffer.Snapshot()
	if len(entries) == 0 {
		return "(no console events yet)", nil
	}

	errorsOnly := false
	for _, arg := range args {
		if arg == "--errors" {
			errorsOnly = true
			break
		}
	}

	var sb strings.Builder
	for _, e := range entries {
		if errorsOnly {
			lvl := strings.ToLower(e.Level)
			if lvl != "error" && lvl != "warning" {
				continue
			}
		}
		sb.WriteString(formatConsoleEntry(e))
		sb.WriteByte('\n')
	}

	result := strings.TrimRight(sb.String(), "\n")
	if result == "" {
		if errorsOnly {
			return "(no console errors/warnings)", nil
		}
		return "(no console events yet)", nil
	}
	return result, nil
}

// --- Network command ---

func (m *Manager) cmdNetwork(args []string) (string, error) {
	for _, arg := range args {
		if arg == "--clear" {
			m.networkBuffer.Clear()
			return "network buffer cleared", nil
		}
	}

	errorsOnly := false
	for _, arg := range args {
		if arg == "--errors" {
			errorsOnly = true
			break
		}
	}

	entries := m.networkBuffer.Snapshot()
	if len(entries) == 0 {
		return "(no network events yet)", nil
	}

	var sb strings.Builder
	for _, e := range entries {
		if errorsOnly && (e.Status == 0 || (e.Status >= 200 && e.Status < 400)) {
			continue
		}
		sb.WriteString(formatNetworkEntry(e))
		sb.WriteByte('\n')
	}

	result := strings.TrimRight(sb.String(), "\n")
	if result == "" {
		if errorsOnly {
			return "(no network errors)", nil
		}
		return "(no network events yet)", nil
	}
	return result, nil
}

// --- Dialog command ---

func (m *Manager) cmdDialog(args []string) (string, error) {
	for _, arg := range args {
		if arg == "--clear" {
			m.dialogBuffer.Clear()
			return "dialog buffer cleared", nil
		}
	}

	errorsOnly := false
	for _, arg := range args {
		if arg == "--errors" {
			errorsOnly = true
			break
		}
	}

	entries := m.dialogBuffer.Snapshot()
	if len(entries) == 0 {
		return "(no dialog events yet)", nil
	}

	var sb strings.Builder
	for _, e := range entries {
		if errorsOnly && e.Type != "beforeunload" {
			continue
		}
		sb.WriteString(formatDialogEntry(e))
		sb.WriteByte('\n')
	}

	result := strings.TrimRight(sb.String(), "\n")
	if result == "" {
		if errorsOnly {
			return "(no dialog errors)", nil
		}
		return "(no dialog events yet)", nil
	}
	return result, nil
}

// --- Formatters ---

func consoleEntryFromEvent(ev *runtime.EventConsoleAPICalled) ConsoleEntry {
	parts := make([]string, 0, len(ev.Args))
	for _, arg := range ev.Args {
		parts = append(parts, formatConsoleArg(arg))
	}
	return ConsoleEntry{
		Timestamp: time.Now().UnixMilli(),
		Level:     string(ev.Type),
		Text:      strings.Join(parts, " "),
	}
}

func formatConsoleArg(obj *runtime.RemoteObject) string {
	if obj == nil {
		return "null"
	}
	if len(obj.Value) > 0 {
		var asString string
		if err := json.Unmarshal(obj.Value, &asString); err == nil {
			return asString
		}
		return string(obj.Value)
	}
	if obj.UnserializableValue != "" {
		return string(obj.UnserializableValue)
	}
	if obj.Description != "" {
		return obj.Description
	}
	if obj.ClassName != "" {
		return obj.ClassName
	}
	return string(obj.Type)
}

func formatConsoleEntry(e ConsoleEntry) string {
	ts := time.UnixMilli(e.Timestamp).Format(time.RFC3339)
	return fmt.Sprintf("[%s] %s: %s", ts, e.Level, e.Text)
}

func formatNetworkEntry(e NetworkEntry) string {
	ts := time.UnixMilli(e.Timestamp).Format(time.RFC3339)
	if e.Status > 0 {
		sizeStr := ""
		if e.Size > 0 {
			sizeStr = fmt.Sprintf(" %dB", e.Size)
		}
		return fmt.Sprintf("[%s] %s %s → %d (%dms%s)",
			ts, e.Method, e.URL, e.Status, e.Duration, sizeStr)
	}
	return fmt.Sprintf("[%s] %s %s (pending)", ts, e.Method, e.URL)
}

func formatDialogEntry(e DialogEntry) string {
	ts := time.UnixMilli(e.Timestamp).Format(time.RFC3339)
	action := e.Action
	if action == "" {
		action = "pending"
	}
	result := fmt.Sprintf("[%s] %s: %q → %s", ts, e.Type, e.Message, action)
	if e.Response != "" {
		result += fmt.Sprintf(" (response: %q)", e.Response)
	}
	return result
}
