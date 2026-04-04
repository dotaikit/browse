package browser

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const frameUsage = "usage: frame <main|@ref|selector|selector ...|--name <name>|--url <pattern>>"

type frameCommandMode int

const (
	frameCommandMain frameCommandMode = iota
	frameCommandByName
	frameCommandByURL
	frameCommandSingleTarget
	frameCommandNestedTargets
)

type frameCommand struct {
	mode    frameCommandMode
	target  string
	targets []string
}

func parseFrameCommand(args []string) (frameCommand, error) {
	if len(args) == 0 {
		return frameCommand{}, fmt.Errorf(frameUsage)
	}

	switch args[0] {
	case "main":
		if len(args) != 1 {
			return frameCommand{}, fmt.Errorf(frameUsage)
		}
		return frameCommand{mode: frameCommandMain}, nil
	case "--name":
		if len(args) != 2 {
			return frameCommand{}, fmt.Errorf(frameUsage)
		}
		return frameCommand{mode: frameCommandByName, target: args[1]}, nil
	case "--url":
		if len(args) != 2 {
			return frameCommand{}, fmt.Errorf(frameUsage)
		}
		return frameCommand{mode: frameCommandByURL, target: args[1]}, nil
	default:
		if len(args) == 1 {
			return frameCommand{mode: frameCommandSingleTarget, target: args[0]}, nil
		}
		return frameCommand{mode: frameCommandNestedTargets, targets: append([]string(nil), args...)}, nil
	}
}

func (m *Manager) cmdFrame(args []string) (string, error) {
	cmd, err := parseFrameCommand(args)
	if err != nil {
		return "", err
	}

	switch cmd.mode {
	case frameCommandMain:
		m.clearActiveFrame()
		m.ClearRefs()
		return "Switched to main frame", nil
	case frameCommandByName:
		frame, err := m.findFrameByName(cmd.target)
		if err != nil {
			return "", err
		}
		return m.switchToFrame(frame)
	case frameCommandByURL:
		pattern, err := regexp.Compile(cmd.target)
		if err != nil {
			return "", fmt.Errorf("invalid --url pattern %q: %w", cmd.target, err)
		}
		frame, err := m.findFrameByURL(pattern)
		if err != nil {
			return "", err
		}
		return m.switchToFrame(frame)
	case frameCommandSingleTarget:
		frame, err := m.enterFrameTarget(cmd.target)
		if err != nil {
			return "", err
		}
		m.ClearRefs()
		return formatFrameSwitchMessage(frame), nil
	case frameCommandNestedTargets:
		originalPath := m.currentFramePath()
		m.clearActiveFrame()

		var lastFrame *cdp.Frame
		for _, target := range cmd.targets {
			frame, err := m.enterFrameTarget(target)
			if err != nil {
				m.setFramePath(originalPath)
				return "", err
			}
			lastFrame = frame
		}

		m.ClearRefs()
		return formatFrameSwitchMessage(lastFrame), nil
	default:
		return "", fmt.Errorf(frameUsage)
	}
}

func (m *Manager) enterFrameTarget(target string) (*cdp.Frame, error) {
	frame, err := m.findFrameBySelectorOrRef(target)
	if err != nil {
		return nil, err
	}
	if err := m.appendActiveFrame(frame.ID); err != nil {
		return nil, err
	}
	return frame, nil
}

func formatFrameSwitchMessage(frame *cdp.Frame) string {
	if frame == nil {
		return "Switched to iframe"
	}
	if frame.Name != "" {
		return fmt.Sprintf("Switched to iframe %q (src=%q)", frame.Name, frame.URL)
	}
	return fmt.Sprintf("Switched to iframe src=%q", frame.URL)
}

func (m *Manager) findFrameBySelectorOrRef(target string) (*cdp.Frame, error) {
	frameID, err := m.resolveFrameID(target)
	if err != nil {
		return nil, err
	}
	frame, err := m.getFrameByID(frameID)
	if err != nil {
		return nil, err
	}
	if frame.ParentID == "" {
		return nil, fmt.Errorf("target %q resolves to the main frame. Use 'frame main' instead", target)
	}
	return frame, nil
}

func (m *Manager) resolveFrameID(target string) (cdp.FrameID, error) {
	if strings.HasPrefix(target, "@") {
		entry, err := m.ResolveRef(target)
		if err != nil {
			return "", err
		}
		var frameID cdp.FrameID
		err = chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			node, err := dom.DescribeNode().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return fmt.Errorf("describe %s: %w", target, err)
			}
			frameID = node.FrameID
			return nil
		}))
		if err != nil {
			return "", err
		}
		if frameID == "" {
			return "", fmt.Errorf("ref %s does not point to an iframe", target)
		}
		return frameID, nil
	}

	return m.resolveFrameIDInCurrentContext(target)
}

func (m *Manager) resolveFrameIDInCurrentContext(selector string) (cdp.FrameID, error) {
	var frameID cdp.FrameID
	err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		frameCtxID, err := m.ensureFrameExecutionContext(ctx)
		if err != nil {
			return err
		}

		eval := runtime.Evaluate(fmt.Sprintf("document.querySelector(%q)", selector))
		if frameCtxID != 0 {
			eval = eval.WithContextID(frameCtxID)
		}

		obj, exception, err := eval.Do(ctx)
		if err != nil {
			return fmt.Errorf("query selector: %w", err)
		}
		if exception != nil {
			return fmt.Errorf("query selector %q: %s", selector, exception.Text)
		}
		if obj == nil || obj.ObjectID == "" {
			return fmt.Errorf("no element matched %q", selector)
		}
		defer func() {
			_ = runtime.ReleaseObject(obj.ObjectID).Do(ctx)
		}()

		node, err := dom.DescribeNode().WithObjectID(obj.ObjectID).Do(ctx)
		if err != nil {
			return fmt.Errorf("describe selector %q: %w", selector, err)
		}
		if node.ContentDocument != nil && node.ContentDocument.FrameID != "" {
			frameID = node.ContentDocument.FrameID
			return nil
		}

		docObj, docException, err := runtime.CallFunctionOn(`function() { return this.contentDocument; }`).
			WithObjectID(obj.ObjectID).
			Do(ctx)
		if err != nil {
			return fmt.Errorf("resolve iframe document for %q: %w", selector, err)
		}
		if docException != nil {
			return fmt.Errorf("resolve iframe document for %q: %s", selector, docException.Text)
		}
		if docObj != nil && docObj.ObjectID != "" {
			defer func() {
				_ = runtime.ReleaseObject(docObj.ObjectID).Do(ctx)
			}()

			docNode, err := dom.DescribeNode().WithObjectID(docObj.ObjectID).Do(ctx)
			if err == nil && docNode.FrameID != "" {
				frameID = docNode.FrameID
				return nil
			}
		}
		frameID = node.FrameID
		return nil
	}))
	if err != nil {
		return "", err
	}
	if frameID == "" {
		return "", fmt.Errorf("selector %q does not point to an iframe", selector)
	}
	return frameID, nil
}

func (m *Manager) switchToFrame(frame *cdp.Frame) (string, error) {
	if frame == nil {
		return "", fmt.Errorf("frame not found")
	}
	if frame.ParentID == "" {
		m.clearActiveFrame()
		m.ClearRefs()
		return "Switched to main frame", nil
	}

	framePath, err := m.buildFramePath(frame.ID)
	if err != nil {
		return "", err
	}

	originalPath := m.currentFramePath()
	m.setFramePath(framePath)
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := m.ensureFrameExecutionContext(ctx)
		return err
	})); err != nil {
		m.setFramePath(originalPath)
		return "", fmt.Errorf("switch frame %s: %w", frame.ID, err)
	}

	m.ClearRefs()
	return formatFrameSwitchMessage(frame), nil
}

func (m *Manager) appendActiveFrame(frameID cdp.FrameID) error {
	if frameID == "" {
		return fmt.Errorf("frame id is empty")
	}

	originalPath := m.currentFramePath()
	m.pushFrame(frameID, 0)
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := m.ensureFrameExecutionContext(ctx)
		return err
	})); err != nil {
		m.setFramePath(originalPath)
		return fmt.Errorf("switch frame %s: %w", frameID, err)
	}
	return nil
}

func (m *Manager) buildFramePath(targetFrameID cdp.FrameID) ([]frameLevel, error) {
	if targetFrameID == "" {
		return nil, fmt.Errorf("frame id is empty")
	}

	var levels []frameLevel
	err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		frameTree, err := page.GetFrameTree().Do(ctx)
		if err != nil {
			return fmt.Errorf("get frame tree: %w", err)
		}

		chain, ok := matchFrameChain(frameTree, targetFrameID)
		if !ok {
			return fmt.Errorf("frame %s not found", targetFrameID)
		}

		if len(chain) <= 1 {
			levels = nil
			return nil
		}

		levels = make([]frameLevel, 0, len(chain)-1)
		for _, frame := range chain[1:] {
			levels = append(levels, frameLevel{frameID: frame.ID})
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	return levels, nil
}

func (m *Manager) findFrameByName(name string) (*cdp.Frame, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("usage: frame --name <name>")
	}

	frame, err := m.findFrameInTree(func(f *cdp.Frame) bool {
		return f.ParentID != "" && f.Name == name
	})
	if err != nil {
		return nil, err
	}
	if frame == nil {
		return nil, fmt.Errorf("iframe with name %q not found", name)
	}
	return frame, nil
}

func (m *Manager) findFrameByURL(pattern *regexp.Regexp) (*cdp.Frame, error) {
	frame, err := m.findFrameInTree(func(f *cdp.Frame) bool {
		return f.ParentID != "" && pattern.MatchString(f.URL)
	})
	if err != nil {
		return nil, err
	}
	if frame == nil {
		return nil, fmt.Errorf("iframe URL matching %q not found", pattern.String())
	}
	return frame, nil
}

func (m *Manager) getFrameByID(frameID cdp.FrameID) (*cdp.Frame, error) {
	if frameID == "" {
		return nil, fmt.Errorf("frame id is empty")
	}
	frame, err := m.findFrameInTree(func(f *cdp.Frame) bool {
		return f.ID == frameID
	})
	if err != nil {
		return nil, err
	}
	if frame == nil {
		return nil, fmt.Errorf("frame %s not found", frameID)
	}
	return frame, nil
}

func (m *Manager) findFrameInTree(predicate func(*cdp.Frame) bool) (*cdp.Frame, error) {
	var found *cdp.Frame
	err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		frameTree, err := page.GetFrameTree().Do(ctx)
		if err != nil {
			return fmt.Errorf("get frame tree: %w", err)
		}
		found = matchFrame(frameTree, predicate)
		return nil
	}))
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, nil
	}
	return found, nil
}

func matchFrame(tree *page.FrameTree, predicate func(*cdp.Frame) bool) *cdp.Frame {
	if tree == nil || tree.Frame == nil {
		return nil
	}
	if predicate(tree.Frame) {
		return tree.Frame
	}
	for _, child := range tree.ChildFrames {
		if matched := matchFrame(child, predicate); matched != nil {
			return matched
		}
	}
	return nil
}

func matchFrameChain(tree *page.FrameTree, target cdp.FrameID) ([]*cdp.Frame, bool) {
	if tree == nil || tree.Frame == nil {
		return nil, false
	}
	if tree.Frame.ID == target {
		return []*cdp.Frame{tree.Frame}, true
	}
	for _, child := range tree.ChildFrames {
		chain, ok := matchFrameChain(child, target)
		if !ok {
			continue
		}
		return append([]*cdp.Frame{tree.Frame}, chain...), true
	}
	return nil, false
}

func (m *Manager) currentFramePath() []frameLevel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.framePath) == 0 {
		return nil
	}
	path := make([]frameLevel, len(m.framePath))
	copy(path, m.framePath)
	return path
}

func (m *Manager) currentActiveFrameState() (cdp.FrameID, runtime.ExecutionContextID) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.framePath) == 0 {
		return "", 0
	}
	level := m.framePath[len(m.framePath)-1]
	return level.frameID, level.contextID
}

func (m *Manager) currentActiveFrameID() cdp.FrameID {
	frameID, _ := m.currentActiveFrameState()
	return frameID
}

func (m *Manager) setFramePath(path []frameLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(path) == 0 {
		m.framePath = nil
		return
	}
	m.framePath = make([]frameLevel, len(path))
	copy(m.framePath, path)
}

func (m *Manager) pushFrame(frameID cdp.FrameID, contextID runtime.ExecutionContextID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.framePath = append(m.framePath, frameLevel{
		frameID:   frameID,
		contextID: contextID,
	})
}

func (m *Manager) setActiveFrame(frameID cdp.FrameID, contextID runtime.ExecutionContextID) {
	if frameID == "" {
		m.clearActiveFrame()
		return
	}
	m.setFramePath([]frameLevel{{
		frameID:   frameID,
		contextID: contextID,
	}})
}

func (m *Manager) clearActiveFrame() {
	m.setFramePath(nil)
}

func (m *Manager) hasActiveFrame() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.framePath) > 0
}

func (m *Manager) getFrameExecutionContext() runtime.ExecutionContextID {
	_, contextID := m.currentActiveFrameState()
	return contextID
}

func (m *Manager) ensureFrameExecutionContext(ctx context.Context) (runtime.ExecutionContextID, error) {
	frameID, contextID := m.currentActiveFrameState()
	if frameID == "" {
		return 0, nil
	}
	if contextID != 0 {
		return contextID, nil
	}

	contextID, err := page.CreateIsolatedWorld(frameID).
		WithWorldName("browse-frame").
		Do(ctx)
	if err != nil {
		m.clearActiveFrame()
		return 0, fmt.Errorf("active iframe is unavailable; run 'frame' again: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.framePath) == 0 {
		return 0, fmt.Errorf("active iframe changed; run 'frame' again")
	}
	last := len(m.framePath) - 1
	if m.framePath[last].frameID != frameID {
		return 0, fmt.Errorf("active iframe changed; run 'frame' again")
	}
	m.framePath[last].contextID = contextID
	return contextID, nil
}

func (m *Manager) evaluate(expression string, res any, opts ...chromedp.EvaluateOption) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		frameCtxID, err := m.ensureFrameExecutionContext(ctx)
		if err != nil {
			return err
		}

		evalOpts := make([]chromedp.EvaluateOption, 0, len(opts)+1)
		evalOpts = append(evalOpts, opts...)
		if frameCtxID != 0 {
			evalOpts = append(evalOpts, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithContextID(frameCtxID)
			})
		}

		return chromedp.Evaluate(expression, res, evalOpts...).Do(ctx)
	})
}

func (m *Manager) ensureMainFrameNavigation(command string) error {
	if !m.hasActiveFrame() {
		return nil
	}
	return fmt.Errorf("%s is disabled in iframe context; run 'frame main' first", command)
}

func (m *Manager) activeFrameSnapshotHeader() string {
	path := m.currentFramePath()
	if len(path) == 0 {
		return ""
	}

	parts := make([]string, 0, len(path))
	for _, level := range path {
		frame, err := m.getFrameByID(level.frameID)
		if err != nil {
			m.clearActiveFrame()
			return ""
		}

		part := fmt.Sprintf("src=%q", frame.URL)
		if frame.Name != "" {
			part = fmt.Sprintf("name=%q %s", frame.Name, part)
		}
		parts = append(parts, part)
	}

	return fmt.Sprintf("[Context: frame path %s]", strings.Join(parts, " -> "))
}
