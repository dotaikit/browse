# browse

Browser automation CLI designed for AI agents. Your agent issues text commands, gets text back — no GUI needed.

Single Go binary, Chrome DevTools Protocol, ~100ms per command.

## Install

```bash
# One-liner (Linux/macOS)
curl -fsSL https://raw.githubusercontent.com/dotaikit/browse/main/install.sh | sh

# Or with Go
go install github.com/dotaikit/browse/cmd/browse@latest
```

## How It Works

`browse` gives AI agents direct browser control through the terminal. The agent reads page state via accessibility snapshots, interacts with elements by reference, and hands control back to you when human judgment is needed.

```bash
browse serve --headed                # start browser
browse goto https://example.com
browse snapshot -i                   # agent sees interactive elements as text
browse fill @e3 "hello"             # agent fills a form field
browse click @e5                    # agent clicks a button
browse handoff "please verify"      # agent hands control to you
browse resume                       # you hand control back to agent
```

Point your AI agent (Claude Code, Codex, Cursor, etc.) to [SKILL.md](SKILL.md) for the complete command reference.

## Credits

Inspired by the `browse` module in [gstack](https://github.com/garrytan/gstack) by Garry Tan.
This project is a clean-room reimplementation in Go with no shared code.
