#!/bin/sh
set -eu

SKILL_NAME="browse"
REPO_URL="https://github.com/dotaikit/browse"
RAW_BASE="https://raw.githubusercontent.com/dotaikit/browse"
SKILL_FILES="SKILL.md"
DEFAULT_TARGET="$HOME/dotai/skills/$SKILL_NAME"

FORCE=false
VERSION="main"

info()  { printf '\033[0;34m→\033[0m %s\n' "$*"; }
ok()    { printf '\033[0;32m✓\033[0m %s\n' "$*"; }
err()   { printf '\033[0;31m✗\033[0m %s\n' "$*" >&2; }
die()   { err "$@"; exit 1; }

cleanup() {
    if [ -n "${BUILD_DIR:-}" ] && [ -d "${BUILD_DIR:-}" ]; then
        rm -rf "$BUILD_DIR"
    fi
}
trap cleanup EXIT

build_skill() {
    BUILD_DIR="$(mktemp -d)"
    local skill_dir="$BUILD_DIR/$SKILL_NAME"
    mkdir -p "$skill_dir"
    if [ -f "SKILL.md" ]; then
        for f in $SKILL_FILES; do
            local dest="$skill_dir/$f"
            mkdir -p "$(dirname "$dest")"
            cp "$(pwd)/$f" "$dest"
        done
    else
        info "Downloading from ${REPO_URL} (${VERSION})..."
        command -v curl >/dev/null 2>&1 || die "curl is required for remote installation"
        for f in $SKILL_FILES; do
            local dest="$skill_dir/$f"
            mkdir -p "$(dirname "$dest")"
            curl -fsSL "${RAW_BASE}/${VERSION}/${f}" -o "$dest"
        done
    fi
    ok "Built skill → $skill_dir"
}

install_skill() {
    local target="$1"

    if [ -d "$target" ]; then
        if diff -rq "$BUILD_DIR/$SKILL_NAME" "$target" >/dev/null 2>&1; then
            ok "Skill already up to date at $target"
            return
        fi

        if [ "$FORCE" = true ]; then
            info "Overwriting existing skill at $target (--force)"
        else
            info "Skill already exists at $target — changes detected:"
            diff -ru "$target" "$BUILD_DIR/$SKILL_NAME" || true
            printf '\n%s' "Overwrite? [y/N] "
            read -r answer </dev/tty
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
    if command -v browse >/dev/null 2>&1; then
        ok "CLI already available: $(command -v browse)"
        return
    fi

    info "Installing browse CLI..."
    curl -fsSL https://raw.githubusercontent.com/dotaikit/browse/main/install.sh | sh
}

main() {
    local target="$DEFAULT_TARGET"

    while [ $# -gt 0 ]; do
        case "$1" in
            --force) FORCE=true; shift ;;
            --version|-v) VERSION="$2"; shift 2 ;;
            --target|-t) target="$2"; shift 2 ;;
            *) die "Unknown argument: $1" ;;
        esac
    done

    echo ""
    echo "  $SKILL_NAME installer"
    echo ""
    build_skill
    install_skill "$target"
    install_cli
    echo ""
    ok "Done! Restart your AI agent to load the skill."
}

main "$@"
