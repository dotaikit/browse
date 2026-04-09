# Project conventions alignment (per ai/stock)
Date: 2026-04-09
Status: done

## Summary
Aligned project scaffolding with ai/stock conventions: restructured AGENTS.md (standard format + Git Workflow), organized .gitignore, added Chinese README, added skill installer with dotai FORMAT.md overwrite policy.

---

## Goal
Align browse project's scaffolding and documentation with the conventions established in ai/stock.

## Acceptance Criteria

### 1. AGENTS.md — restructure to match stock's format
- Header: `> Read ~/dotai/AGENTS.md (global workflow) first, then this file (project constraints), then the role file specified in the prompt.`
- Sections: Project Overview, Rules, Toolchain
- Rules section: Go-specific constraints (stdlib first, single binary, test with `go test`, develop on `dev` + squash merge to `main`, run `dotai snapshot` before squashing)
- Toolchain section: Go 1.26, chromedp, go test, GitHub Actions
- Preserve existing content (architecture, structure table, key behaviors, constraints) but reorganize

### 2. .gitignore — comprehensive, organized by category
Add missing patterns per stock's convention:
- `# dotai` section: `.ai/tmp/`, `CLAUDE.md`, `MEMORY.local.md`
- `# Go` section: binary, test artifacts
- `# IDE` section: `.idea/`, `.vscode/`, `*.swp`, `*.swo`
- `# OS` section: `.DS_Store`
- Keep existing entries, reorganize into sections

### 3. README.zh-CN.md — Chinese translation
- Translate current README.md content to Chinese
- Same structure: title, description, install, how it works, credits

### 4. skills.sh — skill installer script
- Adapted from stock's skills.sh for Go binary distribution
- Copies SKILL.md to `~/dotai/skills/browse/`
- Instead of `uv pip install`, checks for and points to existing binary or offers `go install` / `curl | sh` install
- Same helper functions (info/ok/err/die), same structure (setup_source → build_skill → install_skill → install_cli)

## Key Decisions
- AGENTS.md: Git Workflow section verbatim from stock — branching, commit 规范, squash merge 流程保持跨项目一致
- skills.sh: install_skill 遵循 dotai FORMAT.md 新规范 — diff + confirm / --force / up-to-date skip
- skills.sh: install_cli 不自动安装，提示用户选择 curl|sh 或 go install

## Changes
- `AGENTS.md` — restructured: Header → Overview → Structure → Rules → Git Workflow → Toolchain → Behaviors → Constraints
- `.gitignore` — reorganized into dotai/Go/IDE/OS sections, added MEMORY.local.md + IDE + OS patterns
- `README.zh-CN.md` — new, Chinese translation of README.md
- `skills.sh` — new, skill installer with overwrite-policy per FORMAT.md spec

## Notes
- MEMORY.md: stock doesn't commit one either — skipped
- README.md: current 37-line version adequate — no rewrite
- stock's skills.sh also hasn't adopted the new overwrite policy yet — browse is ahead
