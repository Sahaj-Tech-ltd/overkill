#!/bin/bash
# git-nexus.sh — manage all inspiration repos in one command
# Usage: ./scripts/git-nexus.sh [clone|pull|status|deepen]
#
# clone   — shallow clone all repos (default depth 1, fast)
# pull    — update all repos to latest
# status  — show branch + commit hash + dirty state for each repo
# deepen  — unshallow all repos for full history (when deep analysis needed)

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSPIRATION_DIR="$(dirname "$SCRIPT_DIR")/inspiration"

# All inspiration repos with their clone URLs
declare -A REPOS=(
    ["openclaw"]="https://github.com/openclaw/openclaw.git"
    ["openclaude"]="https://github.com/Gitlawb/openclaude.git"
    ["hermes-agent"]="https://github.com/NousResearch/hermes-agent.git"
    ["zeroclaw"]="https://github.com/zeroclaw-labs/zeroclaw.git"
    ["opencode"]="https://github.com/anomalyco/opencode.git"
    ["picoclaw"]="https://github.com/sipeed/picoclaw.git"
    ["claude-mem"]="https://github.com/thedotmack/claude-mem.git"
    ["mattpocock-skills"]="https://github.com/mattpocock/skills.git"
    ["models.dev"]="https://github.com/anomalyco/models.dev.git"
    ["opentui"]="https://github.com/anomalyco/opentui.git"
    ["rtk"]="https://github.com/rtk-ai/rtk.git"
    ["dev-browser"]="https://github.com/SawyerHood/dev-browser.git"
    ["understand-anything"]="https://github.com/Lum1104/Understand-Anything.git"
    ["dive-into-claude-code"]="https://github.com/VILA-Lab/Dive-into-Claude-Code.git"
)

cmd="${1:-status}"

case "$cmd" in
    clone)
        echo "=== Cloning all inspiration repos (shallow, depth 1) ==="
        mkdir -p "$INSPIRATION_DIR"
        for name in "${!REPOS[@]}"; do
            url="${REPOS[$name]}"
            target="$INSPIRATION_DIR/$name"
            if [ -d "$target/.git" ]; then
                echo "[SKIP] $name — already exists"
            else
                echo "[CLONE] $name <- $url"
                git clone --depth 1 "$url" "$target"
            fi
        done
        echo "=== Done ==="
        ;;
    pull)
        echo "=== Pulling all inspiration repos ==="
        for name in "${!REPOS[@]}"; do
            target="$INSPIRATION_DIR/$name"
            if [ -d "$target/.git" ]; then
                echo "[PULL] $name"
                git -C "$target" pull --depth 1 --ff-only 2>&1 | sed 's/^/  /'
            else
                echo "[MISS] $name — not cloned"
            fi
        done
        echo "=== Done ==="
        ;;
    status)
        echo "=== Inspiration Repo Status ==="
        printf "%-25s %-12s %-10s %s\n" "REPO" "BRANCH" "HASH" "DIRTY"
        printf "%-25s %-12s %-10s %s\n" "----" "------" "----" "-----"
        for name in "${!REPOS[@]}"; do
            target="$INSPIRATION_DIR/$name"
            if [ -d "$target/.git" ]; then
                branch=$(git -C "$target" rev-parse --abbrev-ref HEAD)
                hash=$(git -C "$target" rev-parse --short HEAD)
                dirty=$(git -C "$target" status --porcelain | head -1)
                if [ -n "$dirty" ]; then
                    flag="DIRTY"
                else
                    flag="clean"
                fi
                printf "%-25s %-12s %-10s %s\n" "$name" "$branch" "$hash" "$flag"
            else
                printf "%-25s %-12s %-10s %s\n" "$name" "MISSING" "-" "-"
            fi
        done
        ;;
    deepen)
        echo "=== Unshallowing all inspiration repos for full history ==="
        for name in "${!REPOS[@]}"; do
            target="$INSPIRATION_DIR/$name"
            if [ -d "$target/.git" ]; then
                if git -C "$target" rev-parse --is-shallow-repository 2>/dev/null | grep -q true; then
                    echo "[DEEPEN] $name — fetching full history"
                    git -C "$target" fetch --unshallow 2>&1 | tail -1
                else
                    echo "[SKIP] $name — already full depth"
                fi
            fi
        done
        echo "=== Done ==="
        ;;
    *)
        echo "Usage: git-nexus.sh [clone|pull|status|deepen]"
        exit 1
        ;;
esac
