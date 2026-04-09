---
name: browse
description: >
  Browser automation CLI for AI agents via Chrome DevTools Protocol.
  Use when you need to navigate websites, read page content, fill forms,
  click elements, take screenshots, or manage browser sessions.
  Single binary, ~100ms per command, text in/text out.
license: MIT
compatibility: Requires Chrome/Chromium. Linux or macOS.
metadata:
  author: dotaikit
  version: "0.1"
---

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/dotaikit/browse/main/install.sh | sh
```

## Quick Start

```bash
browse serve --headed              # or: browse serve (connect to existing Chrome)
browse goto https://example.com
browse snapshot -i                 # interactive elements with @e refs
browse fill @e3 "hello"
browse click @e5
browse wait --networkidle
browse snapshot -i                 # verify result
```

Server auto-starts on first command. 30-minute idle auto-shutdown.

## Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--chrome-url`, `-c` | `http://127.0.0.1:9222` | Chrome CDP URL (headless remote) |
| `--headed` | off | Launch local Chrome (mutually exclusive with `--chrome-url`) |
| `--user-data-dir` | `~/.browse/chrome-profile` | Headed mode profile directory |
| `--extension` | — | Comma-separated extension paths (headed only) |
| `--window-size` | `1280x900` | Headed mode window size |
| `--port`, `-p` | random | Server port |

## Commands

### Navigation

- `goto <url>` — navigate to URL
- `back` / `forward` / `reload` — history navigation
- `url` — print current URL
- `frame <sel|@ref|--name n|main>` — switch iframe context

### Snapshot

Primary way to understand page state. Returns accessibility tree with `@e` refs.

- `snapshot` — full tree
- `snapshot -i` — interactive elements only (use this most)
- `snapshot -c` — compact (no empty structural nodes)
- `snapshot -C` — compact with content (includes text)
- `snapshot -d N` — limit depth
- `snapshot -s "CSS"` — scope to selector
- `snapshot -D` — diff against previous
- `snapshot -a -o path` — annotated screenshot

### Interaction

- `click <@ref|sel>` — click element
- `fill <@ref|sel> <value>` — fill input
- `type <text>` — type into focused element
- `hover <@ref|sel>` — hover element
- `select <@ref|sel> <value>` — select dropdown option
- `scroll [sel]` — scroll into view or to bottom
- `press <key>` — press key (Enter, Tab, Escape, Arrow*, etc.)
- `upload <sel> <file...>` — upload files
- `wait <ms|selector|--networkidle [ms]|--load [ms]|--domcontentloaded [ms]>` — wait

### Read

- `text` — cleaned page text
- `html [sel]` — innerHTML
- `links` — all links as "text -> href"
- `forms` — form fields as JSON
- `js <expr>` — run JavaScript
- `eval <js-file>` — run JavaScript from file
- `css <sel> <prop>` — computed CSS value
- `attrs <@ref|sel>` — element attributes
- `is <visible|hidden|enabled|disabled|checked|focused|editable> <@ref|sel>` — state check
- `perf` — performance metrics
- `cookies` — read cookies
- `storage [set <key> <value>]` — localStorage + sessionStorage

### Write

- `cookie <name>=<value>` — set cookie
- `cookie-import <json-file>` — import cookies from JSON
- `cookie-import-browser <browser> --domain <domain> [--profile <name>]` — import from installed browser
- `viewport <WxH>` — set viewport size
- `useragent <string>` — set user agent
- `header <name>:<value>` — set custom header
- `dialog-accept [text]` — accept next dialog
- `dialog-dismiss` — dismiss next dialog

### Monitor

- `console [--clear|--errors]` — console log
- `network [--clear|--errors]` — network log
- `dialog [--clear|--errors]` — dialog log

### Visual

- `screenshot [--viewport] [--clip x,y,w,h] [--scale N] [--width N] [path]` — save screenshot
- `pdf [path]` — save as PDF
- `responsive [prefix]` — screenshots at mobile/tablet/desktop

### Tabs

- `tabs` — list open tabs
- `tab <index>` — switch tab
- `newtab [url]` — open new tab
- `closetab <target-id|index>` — close tab

### Meta

- `status` — health check
- `version` — show CLI version
- `chain` — chain multiple commands
- `diff` — diff current vs previous snapshot
- `handoff [msg]` — pause for human takeover
- `resume` — re-snapshot after human takeover
- `state save|load <name>` — save/load browser state
- `restart` — restart server
- `stop` — shutdown server

## Typical Workflow

```bash
browse goto https://example.com/login
browse snapshot -i                    # see interactive elements
browse fill @e3 "user@example.com"
browse fill @e5 "password"
browse click @e7                     # click login
browse wait --networkidle
browse snapshot -i                    # verify logged in
browse screenshot /tmp/result.png
```

## Key Behaviors

- Element refs (`@e1`, `@e2`...) are stable within a page, reset on navigation
- `snapshot -i` is the primary command for understanding page state
- `handoff` / `resume` enables human-AI shared control
- Chrome 136+ requires `--user-data-dir` flag
