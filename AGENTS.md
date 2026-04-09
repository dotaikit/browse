# browse · Agent Spec

> Read `~/dotai/AGENTS.md` (global workflow) first, then this file (project constraints), then the role file specified in the prompt.

## Project Overview

Browser automation CLI for AI agents — text commands in, text results out. Single Go binary, Chrome DevTools Protocol, ~100ms per command.

Architecture: `browse <command>` → HTTP → `browser.Manager` → Chrome CDP

- **browse serve**: HTTP server with bearer token auth, idle auto-shutdown
- **browse \<cmd\>**: CLI client, auto-discovers server via `.browse/state.json`, auto-starts if needed
- **Scope**: navigation, snapshot, interaction, read/write, monitoring, visual capture, tabs, state management

## Project Structure

| Path | What it does |
|------|-------------|
| `cmd/browse/main.go` | Entrypoint: `serve` (server) or forward command to server |
| `internal/server/` | HTTP server, bearer token auth, command dispatch |
| `internal/browser/` | CDP manager, 50+ command handlers, snapshot, monitoring, frame context |
| `internal/cli/` | Client: auto-discovers server via `.browse/state.json`, auto-starts if needed |
| `internal/state/` | State file read/write (pid, port, token) |

## Rules

- Go 1.26, minimal external dependencies (stdlib first)
- Single binary distribution, cross-compiled for linux/darwin × amd64/arm64
- Module: `github.com/dotaikit/browse`
- Dependencies: `chromedp v0.15.1`, `cdproto`, `uuid`
- Tests with `go test ./...` (stdlib only, no external test frameworks)
- Tests require Chrome with `--remote-debugging-port=9222`
- Build: `go build -o browse ./cmd/browse/`
- Version build: `go build -ldflags="-s -w -X main.version=v0.1.0" -o browse ./cmd/browse/`
- Git branching: see **Git Workflow** section below

## Git Workflow

### Branching

- `main` — 稳定分支，只接受 squash merge
- `dev` — 开发分支，从 `main` 切出，合并后删除
- `archive/<version>` — 版本归档，保留开发完整历史供 debug 追溯
- `archive/fix-<slug>` — fix 归档，统一在 archive/ namespace 下

### Commit 规范

- 每个任务的 plan 文件（status: done）必须和代码在**同一个 commit** 里提交
- 不要把 plan 更新攒到最后集中处理

### Squash Merge 流程

按顺序执行，每步完成后再进入下一步：

1. **确认 dev 上所有工作完成** — 代码、测试、plan 文件都已提交，所有 plan status: done
2. **`dotai snapshot`** — 吸收所有已完成 plan 到 snapshot.md，删除 plan 文件
3. **验证 dev 干净** — 无残留 plan 文件，snapshot 内容正确
4. **提交 snapshot** — `chore: snapshot — absorb all completed plans`
5. **归档开发分支** — `git branch archive/<version> dev`
6. **Squash merge** — `git checkout main && git merge --squash dev && git commit`
7. **删除 dev** — `git branch -D dev`
8. **用户 push** — Orchestrator 不 push

### 注意

- 不要在 squash merge 后再 amend — 所有收尾在 merge 前完成
- 后续新开发从 main 切出新的 dev 分支
- 任何修改都要切分支，不直接在 main 上改

## Toolchain

- Go 1.26
- chromedp / cdproto (CDP protocol)
- go test (testing, stdlib only)
- GitHub Actions (cross-compile + release)

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
