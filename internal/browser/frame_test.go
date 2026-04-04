package browser

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestFramePathStackHelpers(t *testing.T) {
	mgr := &Manager{}

	if mgr.hasActiveFrame() {
		t.Fatalf("expected no active frame by default")
	}

	mgr.pushFrame(cdp.FrameID("frame-1"), runtime.ExecutionContextID(11))
	mgr.pushFrame(cdp.FrameID("frame-2"), runtime.ExecutionContextID(22))

	if !mgr.hasActiveFrame() {
		t.Fatalf("expected active frame after push")
	}
	if got := len(mgr.currentFramePath()); got != 2 {
		t.Fatalf("frame path len = %d, want %d", got, 2)
	}

	frameID, ctxID := mgr.currentActiveFrameState()
	if frameID != cdp.FrameID("frame-2") {
		t.Fatalf("active frame id = %q, want %q", frameID, "frame-2")
	}
	if ctxID != runtime.ExecutionContextID(22) {
		t.Fatalf("active frame context = %d, want %d", ctxID, 22)
	}

	mgr.clearActiveFrame()
	if mgr.hasActiveFrame() {
		t.Fatalf("expected no active frame after clear")
	}
	if got := len(mgr.currentFramePath()); got != 0 {
		t.Fatalf("frame path len after clear = %d, want %d", got, 0)
	}
}

func TestParseFrameCommand(t *testing.T) {
	t.Run("main", func(t *testing.T) {
		cmd, err := parseFrameCommand([]string{"main"})
		if err != nil {
			t.Fatalf("parse frame main: %v", err)
		}
		if cmd.mode != frameCommandMain {
			t.Fatalf("mode = %v, want %v", cmd.mode, frameCommandMain)
		}
	})

	t.Run("single_selector", func(t *testing.T) {
		cmd, err := parseFrameCommand([]string{"#frame"})
		if err != nil {
			t.Fatalf("parse single selector: %v", err)
		}
		if cmd.mode != frameCommandSingleTarget {
			t.Fatalf("mode = %v, want %v", cmd.mode, frameCommandSingleTarget)
		}
		if cmd.target != "#frame" {
			t.Fatalf("target = %q, want %q", cmd.target, "#frame")
		}
	})

	t.Run("multi_selectors", func(t *testing.T) {
		cmd, err := parseFrameCommand([]string{"#outer", "#inner"})
		if err != nil {
			t.Fatalf("parse multi selectors: %v", err)
		}
		if cmd.mode != frameCommandNestedTargets {
			t.Fatalf("mode = %v, want %v", cmd.mode, frameCommandNestedTargets)
		}
		if !reflect.DeepEqual(cmd.targets, []string{"#outer", "#inner"}) {
			t.Fatalf("targets = %#v, want %#v", cmd.targets, []string{"#outer", "#inner"})
		}
	})
}

func TestFrame(t *testing.T) {
	mgr := sharedTestManager(t)

	t.Run("switch_text_fill_and_main", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("iframe.html"))

		mainTitle := mustExecCommand(t, mgr, "js", `document.getElementById("main-title").textContent`)
		if mainTitle != "Main Page" {
			t.Fatalf("main title = %q, want %q", mainTitle, "Main Page")
		}

		switchOut := mustExecCommand(t, mgr, "frame", "#test-frame")
		assertContains(t, switchOut, "Switched to iframe")

		frameText := mustExecCommand(t, mgr, "text")
		assertContains(t, frameText, "Inside Frame")

		frameInputRef := registerActiveFrameRef(t, mgr, "#frame-input", "frame_input")
		fillOut := mustExecCommand(t, mgr, "fill", frameInputRef, "hello from frame")
		assertContains(t, fillOut, "Filled")

		filledValue := mustExecCommand(t, mgr, "js", `document.getElementById("frame-input").value`)
		if filledValue != "hello from frame" {
			t.Fatalf("frame input value = %q, want %q", filledValue, "hello from frame")
		}

		mainOut := mustExecCommand(t, mgr, "frame", "main")
		if mainOut != "Switched to main frame" {
			t.Fatalf("frame main output = %q, want %q", mainOut, "Switched to main frame")
		}

		mainTitleAgain := mustExecCommand(t, mgr, "js", `document.getElementById("main-title").textContent`)
		if mainTitleAgain != "Main Page" {
			t.Fatalf("main title after frame main = %q, want %q", mainTitleAgain, "Main Page")
		}
	})

	t.Run("goto_blocked_inside_iframe", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("iframe.html"))
		_ = mustExecCommand(t, mgr, "frame", "#test-frame")

		_, err := mgr.Execute("goto", []string{fixtureURL("basic.html")})
		if err == nil {
			t.Fatalf("goto inside iframe: expected error")
		}

		errText := strings.ToLower(err.Error())
		if !strings.Contains(errText, "disabled in iframe context") {
			t.Fatalf("goto iframe error = %q, want to contain %q", err, "disabled in iframe context")
		}
		if !strings.Contains(errText, "frame main") {
			t.Fatalf("goto iframe error = %q, want to contain %q", err, "frame main")
		}
	})

	t.Run("snapshot_shows_iframe_context_header", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("iframe.html"))
		_ = mustExecCommand(t, mgr, "frame", "#test-frame")

		snapshotOut := mustExecCommand(t, mgr, "snapshot")
		assertContains(t, snapshotOut, `[Context: frame path `)
	})

	t.Run("single_selector_enters_nested_iframe_from_current_frame", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("nested-iframe.html"))
		_ = mustExecCommand(t, mgr, "frame", "#outer-frame")
		_ = mustExecCommand(t, mgr, "frame", "#inner-frame")

		if got := len(mgr.currentFramePath()); got != 2 {
			t.Fatalf("frame path len = %d, want %d", got, 2)
		}

		innerTitle := mustExecCommand(t, mgr, "js", `document.getElementById("inner-title").textContent`)
		if innerTitle != "Inner Frame" {
			t.Fatalf("inner title = %q, want %q", innerTitle, "Inner Frame")
		}
	})

	t.Run("multi_selector_enters_nested_iframe_from_main", func(t *testing.T) {
		resetFrameContext(mgr)
		defer resetFrameContext(mgr)

		_ = mustExecCommand(t, mgr, "goto", fixtureURL("nested-iframe.html"))
		switchOut := mustExecCommand(t, mgr, "frame", "#outer-frame", "#inner-frame")
		assertContains(t, switchOut, "Switched to iframe")

		if got := len(mgr.currentFramePath()); got != 2 {
			t.Fatalf("frame path len = %d, want %d", got, 2)
		}

		innerTitle := mustExecCommand(t, mgr, "js", `document.getElementById("inner-title").textContent`)
		if innerTitle != "Inner Frame" {
			t.Fatalf("inner title = %q, want %q", innerTitle, "Inner Frame")
		}
	})
}

func resetFrameContext(mgr *Manager) {
	_, _ = mgr.Execute("frame", []string{"main"})
}

func registerActiveFrameRef(t *testing.T, mgr *Manager, selector string, key string) string {
	t.Helper()

	var backendID cdp.BackendNodeID
	err := chromedp.Run(mgr.Ctx(), chromedp.ActionFunc(func(ctx context.Context) error {
		frameCtxID, err := mgr.ensureFrameExecutionContext(ctx)
		if err != nil {
			return err
		}
		if frameCtxID == 0 {
			return fmt.Errorf("active frame context not set")
		}

		obj, exception, err := runtime.Evaluate(fmt.Sprintf(`document.querySelector(%q)`, selector)).
			WithContextID(frameCtxID).
			Do(ctx)
		if err != nil {
			return err
		}
		if exception != nil {
			return fmt.Errorf("evaluate selector %q: %s", selector, exception.Text)
		}
		if obj == nil || obj.ObjectID == "" {
			return fmt.Errorf("selector %q not found in active frame", selector)
		}

		node, err := dom.DescribeNode().WithObjectID(obj.ObjectID).Do(ctx)
		if err != nil {
			return err
		}
		if node.BackendNodeID == 0 {
			return fmt.Errorf("selector %q has no backend node id", selector)
		}
		backendID = node.BackendNodeID
		return nil
	}))
	if err != nil {
		t.Fatalf("resolve selector %q in active frame: %v", selector, err)
	}

	refs := mgr.GetRefs()
	refs[key] = RefEntry{
		Role:             "textbox",
		Name:             selector,
		BackendDOMNodeID: backendID,
	}
	mgr.SetRefs(refs)
	return "@" + key
}
