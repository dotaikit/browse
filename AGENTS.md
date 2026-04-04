# browse · Agent Spec

## What This Is

CLI that gives you (the AI agent) direct browser control from the terminal. You issue text commands, get text back. No GUI, no MCP overhead.

Architecture: `browse <command>` → HTTP → `browser.Manager` → Chrome CDP

## Quick Reference

```bash
browse serve --headed              # or: browse serve (connect to existing Chrome)
browse goto https://example.com
browse snapshot -i                 # interactive elements with @e refs
browse fill @e3 "query"
browse click @e5
browse wait --networkidle
browse snapshot -i                 # verify result
```

Full command list: see `SKILL.md` in this repo.

## Project Structure

| Path | What it does |
|------|-------------|
| `cmd/browse/main.go` | Entrypoint: `serve` (server) or forward command to server |
| `internal/server/` | HTTP server, bearer token auth, command dispatch |
| `internal/browser/` | CDP manager, 50+ command handlers, snapshot, monitoring, frame context |
| `internal/cli/` | Client: auto-discovers server via `.browse/state.json`, auto-starts if needed |
| `internal/state/` | State file read/write (pid, port, token) |

## Build & Test

- Module: `github.com/dotaikit/browse`, Go 1.26
- Dependencies: `chromedp v0.15.1`, `cdproto`, `uuid`
- Build: `go build -o browse ./cmd/browse/`
- Version build: `go build -ldflags="-s -w -X main.version=v0.1.0" -o browse ./cmd/browse/`
- Test: `go test ./...` (235 tests, requires Chrome with `--remote-debugging-port=9222`)
- No external test frameworks — stdlib only

## Key Behaviors

- Server auto-starts on first command, persists state in `.browse/state.json`
- 30-minute idle auto-shutdown
- Element refs (`@e1`, `@e2`...) stable within a page, reset on navigation
- `snapshot -i` is the primary way to understand page state
- Console/network/dialog events in 50,000-entry ring buffers
- Frame context is path-based: supports nested iframes via `frame <selector>`
- Headed mode: `--headed` launches Chrome with profile, extensions, window size
- Human handoff: `handoff [msg]` pauses for human, `resume` returns control to you
- State save/load: persist cookies + tabs across sessions
- Cookie import from installed browsers (Chrome/Brave/Edge)

## Constraints

- `back`/`forward` may timeout on `file://` URLs; has JS fallback
- `cookie-import-browser` requires `sqlite3` CLI at runtime
- State v1 saves cookies + tab URLs only (no localStorage)
- Chrome 136+ requires `--user-data-dir` flag
- URL validation blocks cloud metadata endpoints and private IPs
- File output paths restricted to `$TMPDIR` or project root
