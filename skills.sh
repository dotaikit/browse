#!/usr/bin/env bash
set -euo pipefail

SKILL_NAME="browse"
REPO_URL="https://github.com/dotaikit/browse"
SKILL_FILES=("SKILL.md")
DEFAULT_TARGET="$HOME/dotai/skills/$SKILL_NAME"

FORCE=false

info()  { printf '\033[0;34m→\033[0m %s\n' "$*"; }
ok()    { printf '\033[0;32m✓\033[0m %s\n' "$*"; }
err()   { printf '\033[0;31m✗\033[0m %s\n' "$*" >&2; }
die()   { err "$@"; exit 1; }

setup_source() {
    if [ -f "SKILL.md" ]; then
        SOURCE_DIR="$(pwd)"
        NEED_CLEANUP=false
    else
        info "Downloading from ${REPO_URL}..."
        SOURCE_DIR="$(mktemp -d)"
        NEED_CLEANUP=true
        command -v git &>/dev/null || die "git is required for remote installation"
        git clone --depth 1 "$REPO_URL" "$SOURCE_DIR" 2>/dev/null
        [ -f "$SOURCE_DIR/SKILL.md" ] || die "SKILL.md not found in repo"
    fi
}

cleanup_source() {
    if [ "${NEED_CLEANUP:-false}" = true ] && [ -n "${SOURCE_DIR:-}" ]; then
        rm -rf "$SOURCE_DIR"
    fi
}
trap cleanup_source EXIT

build_skill() {
    BUILD_DIR="$(mktemp -d)"
    local skill_dir="$BUILD_DIR/$SKILL_NAME"
    mkdir -p "$skill_dir"
    for f in "${SKILL_FILES[@]}"; do
        local dest="$skill_dir/$f"
        mkdir -p "$(dirname "$dest")"
        cp "$SOURCE_DIR/$f" "$dest"
    done
    ok "Built skill → $skill_dir"
}

install_skill() {
    local target="$1"

    if [ -d "$target" ]; then
        if diff -rq "$BUILD_DIR/$SKILL_NAME" "$target" &>/dev/null; then
            ok "Skill already up to date at $target"
            return
        fi

        if [ "$FORCE" = true ]; then
            info "Overwriting existing skill at $target (--force)"
        else
            info "Skill already exists at $target — changes detected:"
            diff -ru "$target" "$BUILD_DIR/$SKILL_NAME" || true
            printf '\n%s' "Overwrite? [y/N] "
            read -r answer
            case "$answer" in
                [yY]*) ;;
                *) die "Aborted." ;;
            esac
        fi
        rm -rf "$target"
    fi

    mkdir -p "$(dirname "$target")"
    cp -r "$BUILD_DIR/$SKILL_NAME" "$target"
    ok "Installed skill → $target"
}

install_cli() {
    if command -v browse &>/dev/null; then
        ok "CLI already available: $(command -v browse)"
        return
    fi

    if [ -x "$SOURCE_DIR/browse" ]; then
        ok "Found local browse binary: $SOURCE_DIR/browse"
        info "Add it to PATH (example):"
        echo "  install -m 755 \"$SOURCE_DIR/browse\" \"\$HOME/.local/bin/browse\""
        return
    fi

    info "browse CLI is not installed yet."
    info "Install with one of these commands:"
    echo "  curl -fsSL https://raw.githubusercontent.com/dotaikit/browse/main/install.sh | sh"
    echo "  go install github.com/dotaikit/browse/cmd/browse@latest"
}

main() {
    local target="$DEFAULT_TARGET"

    while [ $# -gt 0 ]; do
        case "$1" in
            --force) FORCE=true; shift ;;
            *) target="$1"; shift ;;
        esac
    done

    echo ""
    echo "  $SKILL_NAME installer"
    echo ""
    setup_source
    build_skill
    install_skill "$target"
    install_cli
    echo ""
    ok "Done! Restart your AI agent to load the skill."
}

main "$@"
