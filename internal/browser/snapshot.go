package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

var interactiveRoles = map[string]bool{
	"button":           true,
	"link":             true,
	"textbox":          true,
	"checkbox":         true,
	"radio":            true,
	"combobox":         true,
	"slider":           true,
	"spinbutton":       true,
	"searchbox":        true,
	"switch":           true,
	"tab":              true,
	"menuitem":         true,
	"menuitemcheckbox": true,
	"menuitemradio":    true,
	"option":           true,
	"treeitem":         true,
}

type snapshotOpts struct {
	interactive       bool
	maxDepth          int    // -1 = unlimited
	compact           bool   // -c: skip non-interactive nodes without name/text
	selector          string // -s: scope to CSS selector subtree
	diff              bool   // -D: diff against previous snapshot
	annotate          bool   // -a: annotated screenshot with overlay boxes
	outputPath        string // -o: custom output path for annotated screenshot
	cursorInteractive bool   // -C: scan cursor:pointer/onclick/tabindex elements
}

func parseSnapshotArgs(args []string) snapshotOpts {
	opts := snapshotOpts{maxDepth: -1}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-i", "--interactive":
			opts.interactive = true
		case "-d", "--depth":
			if i+1 < len(args) {
				i++
				if d, err := strconv.Atoi(args[i]); err == nil {
					opts.maxDepth = d
				}
			}
		case "-c", "--compact":
			opts.compact = true
		case "-s", "--selector":
			if i+1 < len(args) {
				i++
				opts.selector = args[i]
			}
		case "-D", "--diff":
			opts.diff = true
		case "-a", "--annotate":
			opts.annotate = true
		case "-o", "--output":
			if i+1 < len(args) {
				i++
				opts.outputPath = args[i]
			}
		case "-C", "--cursor-interactive":
			opts.cursorInteractive = true
		}
	}
	return opts
}

func (m *Manager) cmdSnapshot(args []string) (string, error) {
	opts := parseSnapshotArgs(args)

	// Determine scope: if -s is set, find the target node first
	var scopeNodeID cdp.BackendNodeID
	if opts.selector != "" {
		var err error
		scopeNodeID, err = m.findNodeBySelector(opts.selector)
		if err != nil {
			return "", fmt.Errorf("selector %q: %w", opts.selector, err)
		}
	}

	var nodes []*accessibility.Node
	if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		nodes, err = accessibility.GetFullAXTree().Do(ctx)
		return err
	})); err != nil {
		return "", fmt.Errorf("get accessibility tree: %w", err)
	}

	// Build parent-children map
	childrenOf := make(map[accessibility.NodeID][]accessibility.NodeID)
	nodeByID := make(map[accessibility.NodeID]*accessibility.Node)
	var rootID accessibility.NodeID

	for _, n := range nodes {
		nodeByID[n.NodeID] = n
		if n.ParentID == "" {
			rootID = n.NodeID
		}
		if n.ParentID != "" {
			childrenOf[n.ParentID] = append(childrenOf[n.ParentID], n.NodeID)
		}
	}

	// If selector is set, find the AX node whose BackendDOMNodeID matches
	walkRoot := rootID
	if scopeNodeID != 0 {
		found := false
		for _, n := range nodes {
			if n.BackendDOMNodeID == scopeNodeID {
				walkRoot = n.NodeID
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("selector %q: matching element has no accessibility node", opts.selector)
		}
	}

	// Walk tree and build output
	refs := make(map[string]RefEntry)
	refCounter := 0
	var sb strings.Builder
	if header := m.activeFrameSnapshotHeader(); header != "" {
		sb.WriteString(header)
		sb.WriteByte('\n')
	}

	var walk func(id accessibility.NodeID, depth int)
	walk = func(id accessibility.NodeID, depth int) {
		if opts.maxDepth >= 0 && depth > opts.maxDepth {
			return
		}
		node := nodeByID[id]
		if node == nil || node.Ignored {
			for _, childID := range childrenOf[id] {
				walk(childID, depth)
			}
			return
		}

		role := axValueStr(node.Role)
		name := axValueStr(node.Name)
		value := axValueStr(node.Value)

		// Skip generic/uninteresting nodes
		if role == "none" || role == "generic" || role == "GenericContainer" {
			for _, childID := range childrenOf[id] {
				walk(childID, depth)
			}
			return
		}

		isInteractive := interactiveRoles[role]

		// Interactive filter
		if opts.interactive && !isInteractive {
			for _, childID := range childrenOf[id] {
				walk(childID, depth)
			}
			return
		}

		// Compact filter: skip non-interactive nodes that have no name and no text content
		if opts.compact && !isInteractive && name == "" && value == "" {
			for _, childID := range childrenOf[id] {
				walk(childID, depth)
			}
			return
		}

		// Skip nodes without DOM mapping (can't interact)
		if node.BackendDOMNodeID == 0 {
			for _, childID := range childrenOf[id] {
				walk(childID, depth)
			}
			return
		}

		// Assign ref
		refCounter++
		refKey := fmt.Sprintf("e%d", refCounter)
		refs[refKey] = RefEntry{
			Role:             role,
			Name:             name,
			BackendDOMNodeID: node.BackendDOMNodeID,
		}

		// Format line
		indent := strings.Repeat("  ", depth)
		if name != "" {
			fmt.Fprintf(&sb, "%s- %s %q @%s\n", indent, role, name, refKey)
		} else {
			fmt.Fprintf(&sb, "%s- %s @%s\n", indent, role, refKey)
		}

		// Walk children
		for _, childID := range childrenOf[id] {
			walk(childID, depth+1)
		}
	}

	walk(walkRoot, 0)

	// --- Cursor-interactive scan (-C) ---
	if opts.cursorInteractive {
		cRefs, cOutput, err := m.scanCursorInteractive()
		if err != nil {
			sb.WriteString("\n(cursor scan failed: " + err.Error() + ")\n")
		} else if len(cRefs) > 0 {
			sb.WriteString("\n-- cursor-interactive (not in ARIA tree) --\n")
			for key, entry := range cRefs {
				refs[key] = entry
			}
			sb.WriteString(cOutput)
		}
	}

	// Store refs (merge @e and @c refs)
	m.SetRefs(refs)

	result := sb.String()
	if result == "" {
		return "(empty accessibility tree)", nil
	}

	// --- Annotated screenshot (-a) ---
	if opts.annotate {
		annotateMsg, err := m.annotateSnapshot(refs, opts.outputPath)
		if err != nil {
			result += "\n(annotate failed: " + err.Error() + ")"
		} else {
			result += "\n" + annotateMsg
		}
	}

	// --- Diff mode (-D) ---
	if opts.diff {
		lastSnapshot := m.GetLastSnapshot()
		if lastSnapshot == "" {
			m.SetLastSnapshot(result)
			return result + "\n\n(no previous snapshot to diff against -- this snapshot stored as baseline)", nil
		}

		diffOutput := computeUnifiedDiff(lastSnapshot, result)
		m.SetLastSnapshot(result)
		return diffOutput, nil
	}

	// Store for future diffs
	m.SetLastSnapshot(result)

	return result, nil
}

// findNodeBySelector uses CDP to find a DOM node matching the CSS selector
// and returns its BackendNodeID.
func (m *Manager) findNodeBySelector(selector string) (cdp.BackendNodeID, error) {
	var backendID cdp.BackendNodeID
	err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		// Get the document root
		docNode, err := dom.GetDocument().WithDepth(0).Do(ctx)
		if err != nil {
			return fmt.Errorf("get document: %w", err)
		}
		// Query selector
		nodeID, err := dom.QuerySelector(docNode.NodeID, selector).Do(ctx)
		if err != nil {
			return fmt.Errorf("query selector: %w", err)
		}
		if nodeID == 0 {
			return fmt.Errorf("no element found")
		}
		// Describe node to get BackendNodeID
		node, err := dom.DescribeNode().WithNodeID(nodeID).Do(ctx)
		if err != nil {
			return fmt.Errorf("describe node: %w", err)
		}
		backendID = node.BackendNodeID
		return nil
	}))
	return backendID, err
}

// scanCursorInteractive finds elements with cursor:pointer, onclick, or tabindex>=0
// that don't have ARIA roles and aren't standard interactive elements.
// Returns @c refs and formatted output text.
func (m *Manager) scanCursorInteractive() (map[string]RefEntry, string, error) {
	type cursorElement struct {
		Selector string `json:"selector"`
		Text     string `json:"text"`
		Reason   string `json:"reason"`
	}

	var elementsJSON string
	js := `(function() {
		var STANDARD_INTERACTIVE = ['A', 'BUTTON', 'INPUT', 'SELECT', 'TEXTAREA', 'SUMMARY', 'DETAILS'];
		var results = [];
		var allElements = document.querySelectorAll('*');
		for (var i = 0; i < allElements.length; i++) {
			var el = allElements[i];
			if (STANDARD_INTERACTIVE.indexOf(el.tagName) >= 0) continue;
			if (!el.offsetParent && el.tagName !== 'BODY') continue;
			var style = getComputedStyle(el);
			var hasCursorPointer = style.cursor === 'pointer';
			var hasOnclick = el.hasAttribute('onclick');
			var hasTabindex = el.hasAttribute('tabindex') && parseInt(el.getAttribute('tabindex'), 10) >= 0;
			var hasRole = el.hasAttribute('role');
			if (!hasCursorPointer && !hasOnclick && !hasTabindex) continue;
			if (hasRole) continue;
			var parts = [];
			var current = el;
			while (current && current !== document.documentElement) {
				var parent = current.parentElement;
				if (!parent) break;
				var siblings = parent.children;
				var index = 0;
				for (var j = 0; j < siblings.length; j++) {
					if (siblings[j] === current) { index = j + 1; break; }
				}
				parts.unshift(current.tagName.toLowerCase() + ':nth-child(' + index + ')');
				current = parent;
			}
			var selector = parts.join(' > ');
			var text = (el.innerText || '').trim().substring(0, 80) || el.tagName.toLowerCase();
			var reasons = [];
			if (hasCursorPointer) reasons.push('cursor:pointer');
			if (hasOnclick) reasons.push('onclick');
			if (hasTabindex) reasons.push('tabindex=' + el.getAttribute('tabindex'));
			results.push({selector: selector, text: text, reason: reasons.join(', ')});
		}
		return JSON.stringify(results);
	})()`

	if err := chromedp.Run(m.ctx, m.evaluate(js, &elementsJSON)); err != nil {
		return nil, "", fmt.Errorf("cursor scan: %w", err)
	}

	var elements []cursorElement
	if err := json.Unmarshal([]byte(elementsJSON), &elements); err != nil {
		return nil, "", fmt.Errorf("parse cursor results: %w", err)
	}

	if len(elements) == 0 {
		return nil, "", nil
	}

	refs := make(map[string]RefEntry)
	var sb strings.Builder

	for i, elem := range elements {
		refKey := fmt.Sprintf("c%d", i+1)

		// Resolve the CSS selector to a BackendNodeID for interaction
		var backendID cdp.BackendNodeID
		if err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			docNode, err := dom.GetDocument().WithDepth(0).Do(ctx)
			if err != nil {
				return err
			}
			nodeID, err := dom.QuerySelector(docNode.NodeID, elem.Selector).Do(ctx)
			if err != nil {
				return err
			}
			if nodeID == 0 {
				return fmt.Errorf("node not found")
			}
			node, err := dom.DescribeNode().WithNodeID(nodeID).Do(ctx)
			if err != nil {
				return err
			}
			backendID = node.BackendNodeID
			return nil
		})); err != nil {
			// Skip elements we can't resolve
			continue
		}

		refs[refKey] = RefEntry{
			Role:             "cursor-interactive",
			Name:             elem.Text,
			BackendDOMNodeID: backendID,
		}
		fmt.Fprintf(&sb, "@%s [%s] %q\n", refKey, elem.Reason, elem.Text)
	}

	return refs, sb.String(), nil
}

// annotateSnapshot injects overlay divs at each ref's bounding box, takes a screenshot,
// then removes the overlays.
func (m *Manager) annotateSnapshot(refs map[string]RefEntry, outputPath string) (string, error) {
	if outputPath == "" {
		outputPath = filepath.Join(os.TempDir(), "browse-annotated.png")
	}

	// Collect bounding boxes for all refs
	type refBox struct {
		Ref string  `json:"ref"`
		X   float64 `json:"x"`
		Y   float64 `json:"y"`
		W   float64 `json:"w"`
		H   float64 `json:"h"`
	}

	var boxes []refBox
	for key, entry := range refs {
		var x, y, w, h float64
		err := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			quads, err := dom.GetContentQuads().WithBackendNodeID(entry.BackendDOMNodeID).Do(ctx)
			if err != nil {
				return err
			}
			if len(quads) == 0 {
				return fmt.Errorf("no quads")
			}
			q := quads[0]
			if len(q) < 8 {
				return fmt.Errorf("invalid quad")
			}
			// quad corners: top-left, top-right, bottom-right, bottom-left
			minX := q[0]
			maxX := q[0]
			minY := q[1]
			maxY := q[1]
			for i := 2; i < 8; i += 2 {
				if q[i] < minX {
					minX = q[i]
				}
				if q[i] > maxX {
					maxX = q[i]
				}
				if q[i+1] < minY {
					minY = q[i+1]
				}
				if q[i+1] > maxY {
					maxY = q[i+1]
				}
			}
			x = minX
			y = minY
			w = maxX - minX
			h = maxY - minY
			return nil
		}))
		if err != nil {
			// Element may be offscreen or hidden -- skip
			continue
		}
		boxes = append(boxes, refBox{Ref: "@" + key, X: x, Y: y, W: w, H: h})
	}

	// Inject overlay divs via JS
	boxesJSON, _ := json.Marshal(boxes)
	injectJS := fmt.Sprintf(`(function() {
		var boxes = %s;
		for (var i = 0; i < boxes.length; i++) {
			var b = boxes[i];
			var overlay = document.createElement('div');
			overlay.className = '__browse_annotation__';
			overlay.style.cssText = 'position:absolute;top:'+b.y+'px;left:'+b.x+'px;width:'+b.w+'px;height:'+b.h+'px;border:2px solid red;background:rgba(255,0,0,0.1);pointer-events:none;z-index:99999;font-size:10px;color:red;font-weight:bold;';
			var label = document.createElement('span');
			label.textContent = b.ref;
			label.style.cssText = 'position:absolute;top:-14px;left:0;background:red;color:white;padding:0 3px;font-size:10px;';
			overlay.appendChild(label);
			document.body.appendChild(overlay);
		}
	})()`, string(boxesJSON))

	if err := chromedp.Run(m.ctx, m.evaluate(injectJS, nil)); err != nil {
		return "", fmt.Errorf("inject overlays: %w", err)
	}

	// Take screenshot
	var buf []byte
	screenshotErr := chromedp.Run(m.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
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
	}))

	// Always remove overlays
	removeJS := `(function() { var els = document.querySelectorAll('.__browse_annotation__'); for (var i = 0; i < els.length; i++) { els[i].remove(); } })()`
	chromedp.Run(m.ctx, m.evaluate(removeJS, nil))

	if screenshotErr != nil {
		return "", fmt.Errorf("screenshot: %w", screenshotErr)
	}

	// Decode base64 if needed (chromedp returns raw bytes)
	if len(buf) > 0 && buf[0] != 0x89 { // not PNG header
		decoded, err := base64.StdEncoding.DecodeString(string(buf))
		if err == nil {
			buf = decoded
		}
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	if err := os.WriteFile(outputPath, buf, 0644); err != nil {
		return "", fmt.Errorf("write annotated screenshot: %w", err)
	}

	return fmt.Sprintf("[annotated screenshot: %s]", outputPath), nil
}

// computeUnifiedDiff produces a simple unified-style diff between two texts.
func computeUnifiedDiff(oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	var result strings.Builder
	result.WriteString("--- previous snapshot\n")
	result.WriteString("+++ current snapshot\n\n")

	// Build a simple line-by-line diff using longest common subsequence approach.
	// For practical purposes, use a straightforward O(n*m) LCS approach.
	m := len(oldLines)
	n := len(newLines)

	// Build LCS table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] > lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}

	// Backtrack to produce diff
	type diffLine struct {
		prefix string
		text   string
	}
	var diffs []diffLine

	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			diffs = append(diffs, diffLine{" ", oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			diffs = append(diffs, diffLine{"+", newLines[j-1]})
			j--
		} else {
			diffs = append(diffs, diffLine{"-", oldLines[i-1]})
			i--
		}
	}

	// Reverse the diffs (we built them backwards)
	for left, right := 0, len(diffs)-1; left < right; left, right = left+1, right-1 {
		diffs[left], diffs[right] = diffs[right], diffs[left]
	}

	for _, d := range diffs {
		fmt.Fprintf(&result, "%s %s\n", d.prefix, d.text)
	}

	return result.String()
}

func axValueStr(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	if len(v.Value) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(v.Value), &s); err == nil {
		return s
	}
	return string(v.Value)
}
