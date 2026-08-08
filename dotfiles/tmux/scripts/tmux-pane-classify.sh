#!/usr/bin/env bash
# tmux-pane-classify.sh <pane_current_path> <pane_current_command>
#
# The 0-agent NAMING fallback. When a window has no active agent, its name is
# INFERRED from context. Priority:
#   1. treehouse worktree  -> "repo#N"   (repo + pool number)
#   2. otherwise           -> a contextual process label (nvim, shell, ...)
#
# Prints a bare name string (no styling, no glyph). The window-name script
# decides where/how to place it.
#
# Treehouse layout (verified): ~/.treehouse/<repo>-<hash>/<N>/<repo>/...
# Detached HEAD makes branch names useless, so repo+pool-number is the useful id.

set -euo pipefail

path="${1:-}"
cmd="${2:-}"

# --- 1. treehouse worktree? ------------------------------------------------
# Match .../.treehouse/<repo>-<hash>/<N>/...  and extract <repo> + <N>.
# <repo> may contain hyphens; the trailing -<hash> is the treehouse suffix, and
# <N> is the pool number directory that follows.
if [[ "$path" == *"/.treehouse/"* ]]; then
  # Strip everything through /.treehouse/
  rest="${path##*/.treehouse/}"        # <repo>-<hash>/<N>/<repo>/...
  bucket="${rest%%/*}"                 # <repo>-<hash>
  after="${rest#*/}"                   # <N>/<repo>/...
  pool="${after%%/*}"                  # <N>
  repo="${bucket%-*}"                  # <repo>  (drop trailing -<hash>)
  if [[ -n "$repo" && "$pool" == [0-9]* ]]; then
    printf '%s#%s' "$repo" "$pool"
    exit 0
  fi
fi

# --- 2. contextual process label -------------------------------------------
# Map the pane's foreground command to a short, readable label. Bare name for
# common cases; raw command as the ultimate fallback.
case "$cmd" in
  nvim|vim|nano|micro)         printf 'nvim' ;;
  bash|sh|zsh|fish)            printf 'shell' ;;
  ssh|mosh|telnet)             printf 'ssh' ;;
  python|python3)              printf 'python' ;;
  node|npm|npx|bun|deno)       printf 'node' ;;
  docker|docker-compose|podman) printf 'docker' ;;
  git|lazygit)                 printf 'git' ;;
  less|more|man)               printf 'pager' ;;
  "")                          printf 'shell' ;;
  *)                           printf '%s' "$cmd" ;;
esac
