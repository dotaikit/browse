# Project Snapshot

Last updated: 2026-04-05

## Overview

`browse` is a single-binary Go browser automation CLI distributed via GitHub Releases and installable via `curl | sh`. It exposes browser control commands through a local HTTP server authenticated with bearer tokens, executing them via Chrome DevTools Protocol (CDP). The runtime flow is `cmd/browse` (CLI serve/client entry with version injection) -> `internal/cli` (auto-discovery/auto-start) -> `internal/server` (auth + command endpoint) -> `internal/browser.Manager` (CDP command execution and shared state), with `.browse/state.json` used for server discovery. The module is `github.com/dotaikit/browse` (renamed from gqk/browse) and builds to a single statically-compiled binary with version injection via ldflags.

## Release & Distribution Infrastructure

Version injection occurs at build time: `cmd/browse/main.go` declares `var version = "dev"`, with a `version` / `--version` command handler that outputs `browse <version>`. The `.github/workflows/release.yml` workflow triggers on `v*` tags, cross-compiles for 4 platform/arch combinations (linux/darwin × amd64/arm64), archives each binary as `browse-{os}-{arch}.tar.gz`, generates a SHA256 checksums file, and publishes both archives and the install script to GitHub Releases. The `install.sh` script detects OS and architecture at runtime, downloads the corresponding archive from GitHub Releases, validates SHA256 checksums (best-effort: sha256sum/shasum when available, warning if missing), extracts the binary to `~/.local/bin` (or `$INSTALL_DIR`), and prints a PATH setup reminder if needed. This enables `curl -fsSL https://raw.githubusercontent.com/dotaikit/browse/main/install.sh | sh` one-liner installation without requiring Go tools.

## Documentation

`README.md` (40 lines) provides a concise human-facing introduction: one-line description, install instructions (curl one-liner + go install), quick-start examples, pointer to SKILL.md for the complete reference, and a note that the tool is designed for AI agent use. `SKILL.md` (164 lines) is the authoritative command reference organized by category (Navigation, Snapshot, Interaction, Read, Write, Monitor, Visual, Tabs, Meta), including server flags, typical workflows, and implementation notes. Both documents reference `https://raw.githubusercontent.com/dotaikit/browse/main/...` install URLs and the Go install path `github.com/dotaikit/browse/cmd/browse@latest`, pointing users to the dotaikit organization.

## Source File Structure

| File | Responsibility |
|------|---------------|
| `cmd/browse/main.go` | CLI entry point, `serve` subcommand with flag parsing (`--chrome-url`, `--headed`, `--user-data-dir`, `--extension`, `--window-size`, `--port`), version injection via ldflags, client mode forwards commands to server |
| `internal/cli/cli.go` | Client-side auto-discovery (reads `.browse/state.json` for port/token), auto-start server if not running, command forwarding via HTTP |
| `internal/server/server.go` | HTTP server with auth middleware, `/health`, `/command`, `/refs` endpoints, idle timeout, sentinel error handling for restart/stop |
| `internal/browser/browser.go` | `Manager` struct (Chrome connection, refs, frame path, watch, dialog mode), `New()` (remote CDP), `NewHeaded()` (local Chrome launch), `Execute()` command dispatcher |
| `internal/browser/commands.go` | Navigation (goto/back/forward/reload), interaction (click/fill/type/hover/select/scroll/wait), URL validation, path validation, back/forward timeout with JS fallback |
| `internal/browser/commands_read.go` | Read commands: forms, css, attrs, is, cookies, storage, perf, eval; JS wrapping helpers (hasAwait, wrapForEvaluate) |
| `internal/browser/commands_write.go` | Write commands: press, viewport, useragent, cookie, cookie-import, header, upload, dialog-accept, dialog-dismiss |
| `internal/browser/commands_meta.go` | Meta commands: newtab, closetab, status, chain, diff, pdf, responsive, state save/load, watch, handoff, resume, restart, stop |
| `internal/browser/snapshot.go` | Accessibility tree snapshot with flags (-i/-c/-d/-s/-D/-a/-o/-C), annotate overlay, cursor-interactive scan, unified diff |
| `internal/browser/frame.go` | Frame stack model (framePath), frame command parsing, nested iframe entry, frame-aware evaluate wrapper, frame tree traversal |
| `internal/browser/monitoring.go` | Event listeners for console/network/dialog, request-response correlation, formatting, --clear/--errors filters |
| `internal/browser/buffer.go` | Generic `RingBuffer[T]` with Add/Snapshot/Last/Get/Set/Clear/TotalAdded |
| `internal/browser/cookie_import.go` | Chromium cookie DB discovery, AES-128-CBC decryption, PBKDF2 key derivation, OS keychain integration |
| `.github/workflows/release.yml` | GitHub Actions workflow: tag push → cross-compile 4 platforms → sha256 checksums → GitHub Release |
| `install.sh` | Installation script: OS/ARCH detection → download → checksum verify → install to ~/.local/bin |

## Command Reference

**Navigation** (6): `goto <url>`, `back`, `forward`, `reload`, `url`, `frame <main|selector...|--name|--url>`

**Snapshot** (1): `snapshot [-i] [-d N] [-c] [-s CSS] [-D] [-a] [-o path] [-C]`

**Interaction** (8): `click <@ref|sel>`, `fill <@ref|sel> <text>`, `type <@ref|sel> <text>`, `hover <@ref|sel>`, `select <@ref|sel> <value>`, `scroll [dir] [px]`, `press <key>`, `upload <sel> <file>`

**Wait** (1): `wait <ms|selector|--networkidle [ms]|--load [ms]|--domcontentloaded [ms]>`

**Read** (11): `text`, `html`, `links`, `js <expr>`, `forms`, `css <sel> <prop>`, `attrs <sel>`, `is <prop> <sel>`, `cookies`, `storage [set <k> <v>]`, `perf`, `eval <js-file>`

**Write** (7): `viewport <WxH>`, `useragent <string>`, `cookie <name>=<value>`, `cookie-import <json>`, `cookie-import-browser <browser> --domain <d> [--profile <p>]`, `header <name>:<value>`, `dialog-accept [text]`, `dialog-dismiss`

**Monitor** (3): `console [--clear|--errors]`, `network [--clear|--errors]`, `dialog [--clear|--errors]`

**Visual** (3): `screenshot [--viewport] [--clip x,y,w,h] [path]`, `pdf [path]`, `responsive [prefix]`

**Tabs** (4): `tabs`, `tab <index>`, `newtab [url]`, `closetab <id|index>`

**Meta** (8): `status`, `chain <cmds>`, `diff <url1> <url2>`, `state <save|load> <name>`, `watch <start|stop|add>`, `handoff [msg]`, `resume`, `restart`, `stop`

**Version** (1): `version`, `--version`

## Shared Runtime Mechanisms

`Manager.Execute` is the central dispatcher for navigation, interaction, read/write, monitoring, and meta commands; mutating actions are wrapped with a best-effort `waitNetworkIdle` to reduce post-write races. A single frame-aware `evaluate` wrapper routes JavaScript either to the main frame or an isolated iframe world, and ref handling is centralized through `refMap` plus stale detection (`dom.DescribeNode`), so invalid refs are dropped and require refresh via `snapshot`.

## Server Lifecycle And Auth

`browse serve` starts a localhost server, generates a bearer token, persists process/port/token metadata to `.browse/state.json`, and installs a 30-minute idle shutdown timer. `/health` is intentionally unauthenticated for liveness checks, while `/command` and `/refs` require `Authorization: Bearer <token>`; control commands (`restart`, `stop`) propagate sentinel errors from browser layer to server/CLI for process restart/shutdown flow. `server.NewWithManager` accepts a pre-created Manager, enabling headed mode to construct the browser before the server. CLI client mode (`browse <cmd>`) reads `.browse/state.json` for port/token, auto-starts a server if none is running, and forwards commands via HTTP POST.

## Snapshot And Interaction Surface

`snapshot` uses typed CDP accessibility APIs (`accessibility.GetFullAXTree`) and supports depth filtering, interactive-only mode, compact mode, selector scoping, baseline diffing, annotated screenshots, and cursor-interactive fallback scanning (`@c` refs) for non-ARIA clickable elements. Interaction commands (`click/fill/type/hover/select/scroll/press/upload`) support both selectors and `@ref` targets, and visual capture supports full page, viewport-only, or clipped screenshots.

## Frame Context Model

Iframe control is path-based rather than single-frame: `framePath` stores nested frame IDs plus execution context IDs, enabling `frame <selector...>` nested entry, `frame --name`, `frame --url`, and `frame main`. Navigation commands are blocked in iframe context by design, and both main-frame navigation and active iframe navigation clear frame/ref state to avoid stale execution contexts; snapshot output includes a frame-context header when inside iframe paths.

## Monitoring And Buffers

Console/network/dialog capture uses generic `RingBuffer[T]` instances (capacity 50,000 each) with snapshot, tail, indexed update, clear, and total-added counters. Monitoring listeners record request and response pairs (status/duration/size), keep pending request correlation by request ID, and support `--clear` and `--errors` filters (`dialog --errors` maps to `beforeunload` events).

## Security Boundaries

URL normalization is strict: only `http/https` are allowed, cloud metadata endpoints are blocked, numeric-IP variants are normalized, and DNS rebinding checks reject hosts resolving to blocked metadata targets. Output-path validation resolves symlinks and enforces writes only under `os.TempDir()` or current project root; this guard is applied to screenshot/pdf/responsive/eval file paths. Profile/state inputs are constrained (`validateProfile`, regex-based state names) to prevent traversal and malformed control input.

## State, Watch, And Handoff Workflows

Session state can be saved/loaded as versioned JSON in `~/.browse/states/<name>.json` (atomic write, `0600` permissions), containing cookies and tab URLs with active-tab metadata; load warns (non-blocking) when snapshot age exceeds 7 days. Human-in-the-loop commands (`handoff`/`resume`) pause operator flow and re-anchor automation with a fresh interactive snapshot, while `watch start|add|stop` provides manual snapshot collection for polling-style workflows.

## Cookie Import Integrations

`cookie-import` loads JSON cookie arrays and sets them through CDP with domain/path defaults. `cookie-import-browser` supports Chromium-family local profile imports (`chrome/chromium/brave/edge`) by copying the Cookies SQLite DB, querying domain rows via `sqlite3` CLI, deriving platform keys (macOS keychain, Linux `secret-tool`, Linux v10 fallback), decrypting AES-128-CBC cookie payloads, and replaying them through `network.SetCookie`.

## Startup Modes And Toolchain

The server supports remote-CDP mode (`--chrome-url`) and local headed mode (`--headed`, `--user-data-dir`, `--extension`, `--window-size`) with explicit mutual exclusion. The module is `github.com/dotaikit/browse` targeting Go `1.26` and depends primarily on `chromedp`/`cdproto` plus `uuid`; runtime artifacts (`.browse/`, `.ai/tmp/`, build outputs) are gitignored. Releases are automated: version is injected via ldflags at build time, GitHub Actions cross-compiles for 4 platforms on tag push, and the install script enables one-line installation from GitHub Releases.

## Test Architecture

`internal/browser` uses a mixed strategy: shared headless-Chrome integration tests with `httptest` fixtures for command behavior, plus pure unit tests for parsing, formatting, diffing, validation, and crypto helpers. `TestMain` manages a shared Manager + fixture HTTP server; `newTestManager(t)` provides isolated instances; Chrome unavailability triggers `t.Skip` not `t.Fatal`. Coverage: 235 sub-tests across command groups (navigation/read/write/meta/snapshot/frame/monitoring), security validation (URL/path/profile), watch/state TTL, headed option parsing, CLI flag parsing, and server auth/security-audit. Test files: 13 in `internal/browser/`, 1 in `cmd/browse/`, 1 in `internal/server/`; 12 HTML fixtures in `testdata/fixtures/`.

## Known Limitations

- `back`/`forward` use a 15s CDP timeout with JS `window.history` fallback; if the fallback encounters "Inspected target navigated or closed", it is treated as success. On some Chrome versions with `file://` URLs, back still times out (test tolerates this).
- `hover` CSS selector in iframe context uses JS `getBoundingClientRect` + `DispatchMouseEvent` (chromedp.QueryAfter does not support frame execution contexts).
- Headed mode (`NewHeaded`) is implemented and unit-tested but not GUI-verified in CI/sandbox environments.
- `cookie-import-browser` depends on `sqlite3` CLI binary at runtime (not a Go dependency).
- State V1 does not persist localStorage (cookies + tab URLs only).
- Sidebar UI is explicitly out of scope — human-AI collaboration uses `handoff`/`resume` via terminal.
