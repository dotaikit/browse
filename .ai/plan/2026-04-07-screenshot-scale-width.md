# screenshot --scale & --width
Date: 2026-04-07
Status: done

## Summary
Added `--scale N` (DPR override) and `--width N` (post-capture resize) flags to `screenshot` command. All 8 AC met, 8 new tests pass.

---

## Goal
Add `--scale N` and `--width N` flags to the `screenshot` command.

- `--scale N` — override device pixel ratio before capture (e.g. `--scale 1` forces 1× on HiDPI)
- `--width N` — post-capture resize to N pixels wide, preserving aspect ratio

## Acceptance Criteria

1. `browse screenshot --scale 1 /tmp/out.png` produces 1× DPR screenshot regardless of display
2. `browse screenshot --scale 2 /tmp/out.png` produces 2× DPR screenshot
3. `browse screenshot --width 1200 /tmp/out.png` resizes output PNG to 1200px wide (aspect ratio preserved)
4. `--scale` and `--width` can be combined: `--scale 1 --width 800`
5. `--scale` and `--width` work with existing `--viewport` and `--clip` flags
6. Invalid values (<=0, non-numeric) return clear error messages
7. Tests for both flags: happy path + error cases
8. SKILL.md updated with new flags

## Approach

### `--scale N` (float64)
- Before capture: save current viewport metrics, call `chromedp.EmulateViewport(w, h, chromedp.EmulateScale(N))`
- Capture screenshot as usual
- After capture: restore original viewport metrics
- Reference pattern: `responsive` command in `commands_meta.go:823-913`

### `--width N` (int)
- After capture + file write, decode PNG → resize with `golang.org/x/image/draw` (BiLinear) → re-encode
- Add dependency: `golang.org/x/image`
- Aspect ratio: `newHeight = origHeight * N / origWidth`

### Files to modify
- `internal/browser/commands.go` — flag parsing + scale emulation + width resize logic
- `internal/browser/commands_test.go` — tests for both flags
- `go.mod` / `go.sum` — add `golang.org/x/image`
- `SKILL.md` — document new flags

## Key Decisions
- `--scale` via `chromedp.EmulateViewport` + `EmulateScale` — uses existing CDP API, defer restore
- `--width` via `golang.org/x/image/draw.BiLinear.Scale` — post-capture resize, not viewport change
- Added `golang.org/x/image v0.38.0` as sole new dependency

## Changes
- `internal/browser/commands.go` — flag parsing, scale emulation with defer restore, `resizePNG()` helper
- `internal/browser/commands_test.go` — 5 integration subtests + 3 `resizePNG` unit tests
- `go.mod` / `go.sum` — `golang.org/x/image v0.38.0`
- `SKILL.md` — updated screenshot signature

## Notes
- `--scale` restores original DPR after capture to avoid side effects on subsequent commands
- `--width` operates on the final PNG bytes, so it composes correctly with `--clip` and `--viewport`
