#!/bin/sh
# install.sh -- portable installer for the WezTerm + tmux + zsh + nvim dotfiles.
#
# Model: COPY, not symlink. For each managed source subtree this installer
# copies tracked files, file-by-file, into REAL directories under ~/.config
# (creating real dirs, never symlinks). This is deliberate:
#
#   * Worktrees work. A directory symlink pins the live config to ONE checkout
#     (the symlink target), so you could not test config changes from a git
#     worktree. Copying deploys from whatever checkout you run install.sh in.
#   * Generated files can coexist. The theme generator (themes/render.sh) emits
#     build artifacts into config dirs. If a config dir WERE the repo (a symlink),
#     writing a generated file into it would write into the repo. With real
#     dirs, tracked and generated files live side by side, and the installer
#     manages only the tracked ones (see the EXCLUDE list).
#
# What it copies (see the MANIFEST table below):
#   * wezterm/  -> ~/.config/wezterm/        (excludes render.sh-generated colors/)
#   * tmux/     -> ~/.config/tmux/           (incl. scripts/, themes/spaceflight/)
#   * nvim/     -> ~/.config/nvim/           (incl. lua/, lsp/; excludes colors/)
#   * zsh/      -> ~/.config/zsh/
#   * themes/   -> ~/.config/themes/         (render.sh + public palettes/)
#   * tmux/plugins/tmux-agent-state.ts -> ~/.config/opencode/plugins/... (single)
#   * tmux/tmux.conf                   -> ~/.tmux.conf   (compat shim, single)
#
# The recursive subtree copy mirrors structure, so nested dirs (nvim's lua/ and
# lsp/, tmux's scripts/ and themes/spaceflight/) deploy automatically and new
# tracked files need no manifest edit. It also preserves file mode, so tmux
# scripts and the opencode plugin stay executable.
#
# Two entries below are duplicate deploys and that is intended: the opencode
# plugin (tmux-agent-state.ts) and the ~/.tmux.conf shim both also live under
# the tmux/ subtree. The tmux/ subtree copy places them under ~/.config/tmux/
# (where tmux scripts reference them); the two extra single-file entries place
# copies at the additional paths those consumers require (opencode's plugin dir,
# and the classic ~/.tmux.conf path for tmux < 3.1).
#
# After copying, the theme generator (themes/render.sh --all) runs so the PUBLIC
# palettes (nebula, rosepine) are emitted as native theme files into each tool's
# own theme dir (~/.config/{wezterm/colors,nvim/colors,opencode/themes,
# tmux/themes}) for the tools to discover/source (needs jq). Local-only palettes
# (e.g. a brand palette kept OUTSIDE this repo) are NOT rendered by the installer;
# generate those yourself: `sh render.sh /path/to/palette.json`.
#
# The zsh directory is copied whole so future fragments alongside interactive.zsh
# deploy automatically. This installer does NOT edit your ~/.zshrc. To activate
# the fragment, add exactly one line to your own ~/.zshrc (see dotfiles/README.md):
#   source "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/interactive.zsh"
#
# Conflict handling: when a destination file already exists and DIFFERS from the
# repo source, the installer treats it as a CONFLICT. Interactively (a TTY is
# available) it prompts per file: overwrite / skip / diff / all-overwrite / quit.
# Every overwrite first backs the existing file up to <dest>.bak.<epoch>.
# Non-interactively it SKIPS and warns (never clobbers silently) unless --force.
#
# Uninstall is SAFE: because copies are real files you may have edited, a target
# is removed ONLY if it is byte-identical to the repo's current source for it.
# A modified target is KEPT. Files not in the manifest (generated themes, your
# own files) are never touched.
#
# Safety:
#   * Idempotent: re-running with no repo changes is all-OK -- no prompts, no
#     backups, no writes.
#   * Only ever writes individual files (and mkdir -p their parents). Never
#     deletes or wipes a directory; uninstall removes only files it recognizes
#     as unchanged copies, and prunes only dirs left empty.
#
# POSIX sh; no bashisms. Works on macOS, Linux, and WSL.

set -eu

# ---------------------------------------------------------------------------
# Temp-file cleanup trap
# ---------------------------------------------------------------------------
# The copy and uninstall loops each stage a `find` listing into a temp file
# (they cannot use `find | while`, which would run the loop body in a subshell
# and discard the counter mutations -- see deploy_subtree). A trailing `rm -f`
# is not enough: the interactive `q` (quit) branch `exit`s from inside the
# `while ... done < "$_tmplist"` loop, and a `set -e` abort mid-loop also skips
# the trailing cleanup. So we register an EXIT/INT/TERM trap over a single
# script-scoped temp path, reused by both loops, and initialize it empty so the
# trap is safe under `set -u` even before any temp file is created.
_tmplist=""
# shellcheck disable=SC2064  # expand $_tmplist at trap-fire time, not now.
trap 'rm -f "${_tmplist:-}"' EXIT INT TERM

# ---------------------------------------------------------------------------
# Paths -- derived from THIS script's location, never hard-coded.
# ---------------------------------------------------------------------------
# Resolve the directory containing this script, following one level of symlink
# if the script itself was invoked via a symlink. We avoid `readlink -f`
# (missing on stock macOS) and use a portable cd/pwd approach.
script_path=$0
# If invoked through a symlink, resolve it (single level is sufficient here).
while [ -h "$script_path" ]; do
  link_target=$(readlink "$script_path")
  case $link_target in
    /*) script_path=$link_target ;;
    *) script_path=$(dirname "$script_path")/$link_target ;;
  esac
done
SCRIPT_DIR=$(cd "$(dirname "$script_path")" && pwd)

# Destination root.
: "${XDG_CONFIG_HOME:=$HOME/.config}"

# The theme source (also its own managed subtree, below) drives the post-copy
# render step.
THEMES_SRC="$SCRIPT_DIR/themes"

# ---------------------------------------------------------------------------
# MANIFEST -- source subtree (or single file) -> target.
# ---------------------------------------------------------------------------
# Each managed entry is one line in $MANIFEST, "<src>|<dest>", where paths are
# absolute. A trailing form is not needed: whether an entry is a directory
# subtree or a single file is decided at deploy time by testing -d "$src".
#
# For subtree entries, files whose path matches any glob in $EXCLUDES (matched
# against the path RELATIVE to the subtree source root) are pruned -- they are
# generated build output the installer must neither deploy nor, on uninstall,
# remove.
#
#   Source (under $SCRIPT_DIR)              Target                                   Notes
#   wezterm/                                ~/.config/wezterm/                       exclude colors/ (render.sh-generated schemes)
#   tmux/                                   ~/.config/tmux/                          incl. scripts/ + themes/spaceflight/ (tracked)
#   nvim/                                   ~/.config/nvim/                          incl. lua/ + lsp/; exclude colors/
#   zsh/                                    ~/.config/zsh/                           --
#   themes/                                 ~/.config/themes/                        render.sh + public palettes/ (source of truth)
#   tmux/plugins/tmux-agent-state.ts        ~/.config/opencode/plugins/...           single file (opencode plugin)
#   tmux/tmux.conf                          ~/.tmux.conf                             single-file compat shim for tmux < 3.1
#
# Newlines separate entries; a leading blank line is harmless (skipped).
MANIFEST="\
$SCRIPT_DIR/wezterm|$XDG_CONFIG_HOME/wezterm
$SCRIPT_DIR/tmux|$XDG_CONFIG_HOME/tmux
$SCRIPT_DIR/nvim|$XDG_CONFIG_HOME/nvim
$SCRIPT_DIR/zsh|$XDG_CONFIG_HOME/zsh
$SCRIPT_DIR/themes|$XDG_CONFIG_HOME/themes
$SCRIPT_DIR/tmux/plugins/tmux-agent-state.ts|$XDG_CONFIG_HOME/opencode/plugins/tmux-agent-state.ts
$SCRIPT_DIR/tmux/tmux.conf|$HOME/.tmux.conf"

# EXCLUDES -- shell globs matched (via `case`) against each subtree file's path
# RELATIVE to its source root. Pruned files are never deployed and never removed
# on uninstall: these are generated theme files that must coexist in the real
# config dirs without the installer managing or wiping them.
#
# The theming model is "native tool theme dirs": themes/render.sh emits native
# theme files DIRECTLY into each tool's config dir at ~/.config:
#   wezterm  -> ~/.config/wezterm/colors/<name>-<mode>.toml
#   nvim     -> ~/.config/nvim/colors/<name>-<mode>.lua
#   opencode -> ~/.config/opencode/themes/<name>.json
#   tmux     -> ~/.config/tmux/themes/<name>-<mode>.conf
#
#   */colors/*   wezterm + nvim generated color schemes. The repo sources carry
#   colors/*     no colors/ dir, so these are purely defensive: they guarantee
#                the installer never manages a generated scheme even if one ever
#                appears under a source tree.
#
# NOTE: the generated tmux theme confs (~/.config/tmux/themes/*.conf) and the
# generated opencode theme (~/.config/opencode/themes/*.json) live only at the
# DEPLOY target, not in the repo source, so the installer's source-tree scan
# never sees them -- no exclude is needed. Do NOT exclude tmux/themes/spaceflight/*
# -- that is legit tracked config that should deploy.
EXCLUDES="\
*/colors/*
colors/*"

# ---------------------------------------------------------------------------
# Options
# ---------------------------------------------------------------------------
DRY_RUN=0
UNINSTALL=0
FORCE=0
# Set to 1 the first time the user answers "a" (all-overwrite) at a prompt.
OVERWRITE_ALL=0

usage() {
  cat <<EOF
Usage: install.sh [--dry-run] [--force] [--uninstall] [-h|--help]

  (no options)  Copy the managed dotfiles into real dirs under ~/.config
                (and the ~/.tmux.conf shim). Existing, differing files are
                treated as conflicts: prompted interactively, skipped when
                non-interactive.
  --dry-run     Print what WOULD happen (NEW/UPDATE/OK/CONFLICT); change nothing
                and never prompt.
  --force       Overwrite conflicting files without prompting. Each overwrite is
                still backed up first to <dest>.bak.<epoch>.
  --uninstall   Remove ONLY managed files that are byte-identical to the current
                repo source (unchanged since deploy); KEEP modified ones. Never
                touches files outside the manifest. Prunes empty dirs it created.
  -h, --help    Show this help.
EOF
}

for arg in "$@"; do
  case $arg in
    --dry-run) DRY_RUN=1 ;;
    --force) FORCE=1 ;;
    --uninstall) UNINSTALL=1 ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'error: unknown option: %s\n\n' "$arg" >&2
      usage >&2
      exit 2
      ;;
  esac
done

# ---------------------------------------------------------------------------
# Interactive-terminal detection
# ---------------------------------------------------------------------------
# Whether we can PROMPT the user. Testing `[ -r /dev/tty ]` is NOT sufficient:
# on a detached session the device node exists and tests readable, yet actually
# OPENING it fails ("Device not configured"), which under `set -e` would abort
# the whole run. So we probe by truly opening /dev/tty for reading, in a
# subshell, with all output discarded. HAVE_TTY=1 only if that open succeeds.
if [ "${DRY_RUN:-0}" -eq 1 ]; then
  HAVE_TTY=0
elif (exec </dev/tty) 2>/dev/null; then
  HAVE_TTY=1
else
  HAVE_TTY=0
fi

# ---------------------------------------------------------------------------
# Color / output helpers (POSIX sh + ANSI)
# ---------------------------------------------------------------------------
# Respect NO_COLOR, and disable color when stdout is not a TTY.
if [ -n "${NO_COLOR:-}" ] || [ ! -t 1 ]; then
  C_RESET='' C_DIM='' C_GREEN='' C_YELLOW='' C_RED='' C_CYAN=''
else
  C_RESET=$(printf '\033[0m')
  C_DIM=$(printf '\033[2m')
  C_GREEN=$(printf '\033[32m')
  C_YELLOW=$(printf '\033[33m')
  C_RED=$(printf '\033[31m')
  C_CYAN=$(printf '\033[36m')
fi

# tag <color> <TAG> <message>  -- aligned, colorized status line.
tag() {
  _c=$1
  _t=$2
  shift 2
  printf '%s%-9s%s%s\n' "$_c" "$_t" "$C_RESET" "$*"
}
report_new() { tag "$C_GREEN" NEW "$1"; }
report_update() { tag "$C_YELLOW" UPDATE "$1"; }
report_ok() { tag "$C_DIM" OK "$1"; }
report_skip() { tag "$C_YELLOW" SKIP "$1"; }
report_conflict() { tag "$C_RED" CONFLICT "$1"; }
report_backup() { tag "$C_DIM" BACKUP "$1"; }
report_removed() { tag "$C_GREEN" REMOVED "$1"; }
report_kept() { tag "$C_YELLOW" KEPT "$1"; }
report_would() { tag "$C_CYAN" WOULD "$1"; }

info() { printf '  %s\n' "$1"; }
ok() { tag "$C_GREEN" OK "$1"; }
warn() { tag "$C_RED" WARN "$1" >&2; }

# ---------------------------------------------------------------------------
# Summary counters. These are plain variables mutated in the MAIN shell only.
# The copy loop deliberately avoids `find | while` (which would run the loop
# body in a SUBSHELL, discarding counter mutations); it redirects a temp file
# into the loop instead (see deploy_subtree).
# ---------------------------------------------------------------------------
N_NEW=0
N_UPDATE=0
N_OK=0
N_SKIP=0
N_CONFLICT=0
N_BACKUP=0
N_REMOVED=0
N_KEPT=0

# ---------------------------------------------------------------------------
# Platform detection (for dependency suggestions)
# ---------------------------------------------------------------------------
# Returns one of: macos, wsl, linux, other
detect_platform() {
  uname_s=$(uname -s 2>/dev/null || echo unknown)
  case $uname_s in
    Darwin) echo macos ;;
    Linux)
      if grep -qi microsoft /proc/version 2>/dev/null; then
        echo wsl
      else
        echo linux
      fi
      ;;
    *) echo other ;;
  esac
}
PLATFORM=$(detect_platform)

# ---------------------------------------------------------------------------
# Copy core
# ---------------------------------------------------------------------------
# Symlink safety (C1) ---------------------------------------------------------
# This installer supersedes an OLDER one that created whole-directory symlinks
# (e.g. ~/.config/tmux -> <repo>/dotfiles/tmux). On a machine that ran the old
# installer those stale symlinks still exist, and they are DANGEROUS to a
# copy/uninstall model: a $dest under such a symlink resolves THROUGH it back
# into the repo, so cmp would compare a repo file to ITSELF ("identical") and a
# subsequent `rm` would delete a tracked file out of the git working tree; a
# copy would write INTO the repo. We therefore never compare/copy/remove
# through a symlink. We refuse and instruct the user to remove the legacy
# symlink and re-run (approach (a): refuse + instruct, never auto-migrate).
#
# symlink_on_path <path>  -- return 0 if <path> itself is a symlink, OR if any
# ANCESTOR directory of it (walking up toward, but not past, the config roots
# $XDG_CONFIG_HOME / $HOME / /) is a symlink. This catches both a symlinked
# leaf file and the legacy whole-dir symlink case (an ancestor like
# ~/.config/tmux being a symlink when the target is ~/.config/tmux/scripts/x).
symlink_on_path() {
  _p=$1
  # Test the path itself first (leaf may be a symlinked file).
  if [ -L "$_p" ]; then
    return 0
  fi
  # Walk ancestors upward, stopping at a config root or the filesystem root.
  _cur=$(dirname "$_p")
  while :; do
    case $_cur in
      "$XDG_CONFIG_HOME" | "$HOME" | / | .) return 1 ;;
    esac
    if [ -L "$_cur" ]; then
      return 0
    fi
    _next=$(dirname "$_cur")
    # Guard against a stuck walk (dirname of "/" is "/").
    [ "$_next" = "$_cur" ] && return 1
    _cur=$_next
  done
}

# warned-roots ledger: newline-separated list of destination roots we have
# already emitted the legacy-symlink warning for, so we nag exactly ONCE per
# affected root (not once per file under it).
WARNED_SYMLINK_ROOTS=""

# warn_legacy_symlink <dest>  -- find the offending symlink component on <dest>
# (the path itself or the nearest symlinked ancestor) and print a single clear,
# actionable migration instruction the FIRST time we see that component.
warn_legacy_symlink() {
  _dest=$1
  # Identify the actual symlink component to name in the message.
  _link=""
  if [ -L "$_dest" ]; then
    _link=$_dest
  else
    _cur=$(dirname "$_dest")
    while :; do
      case $_cur in
        "$XDG_CONFIG_HOME" | "$HOME" | / | .) break ;;
      esac
      if [ -L "$_cur" ]; then
        _link=$_cur
        break
      fi
      _next=$(dirname "$_cur")
      [ "$_next" = "$_cur" ] && break
      _cur=$_next
    done
  fi
  [ -n "$_link" ] || _link=$_dest

  # Emit once per offending component.
  _oldifs=$IFS
  IFS='
'
  for _seen in $WARNED_SYMLINK_ROOTS; do
    if [ "$_seen" = "$_link" ]; then
      IFS=$_oldifs
      return 0
    fi
  done
  IFS=$_oldifs
  WARNED_SYMLINK_ROOTS="$WARNED_SYMLINK_ROOTS
$_link"

  if [ "$UNINSTALL" -eq 1 ]; then
    report_kept "$_link (symlink -- legacy install; skipped)"
    N_KEPT=$((N_KEPT + 1))
  else
    report_skip "$_link (symlink -- legacy install; skipped)"
    N_SKIP=$((N_SKIP + 1))
  fi
  warn "$_link is a symlink (legacy directory-symlink install)."
  warn "  Refusing to read/write/remove through it (that could damage the repo)."
  warn "  To migrate, remove the old symlink and re-run this installer:"
  warn "      rm '$_link'"
}

# is_excluded <relpath>  -- return 0 if <relpath> matches any EXCLUDES glob.
is_excluded() {
  _rel=$1
  # Iterate the newline-separated globs. `case` does glob matching.
  _oldifs=$IFS
  IFS='
'
  for _glob in $EXCLUDES; do
    [ -n "$_glob" ] || continue
    # shellcheck disable=SC2254  # $_glob is intentionally a glob pattern here.
    case $_rel in
      $_glob)
        IFS=$_oldifs
        return 0
        ;;
    esac
  done
  IFS=$_oldifs
  return 1
}

# backup_dest <dest>  -- copy an existing dest aside to dest.bak.<epoch>.
# Never clobbers a prior backup (epoch-timestamped). Honors DRY_RUN.
backup_dest() {
  _dest=$1
  _bak="$_dest.bak.$(date +%s)"
  # In the extremely unlikely event of a same-second collision, bump until free.
  while [ -e "$_bak" ]; do
    _bak="$_bak.1"
  done
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  cp -p "$_dest" "$_bak"
  N_BACKUP=$((N_BACKUP + 1))
  report_backup "$_dest -> $_bak"
}

# do_copy <src> <dest>  -- mkdir -p parent, copy preserving mode. Honors DRY_RUN.
# M2: the copy is ATOMIC. We copy to a temp file in the SAME directory as $dest,
# then `mv` it into place. `mv` within a directory is a rename -- atomic on
# POSIX filesystems -- so an interrupt can never leave a truncated $dest (even
# for a NEW file, where no backup exists yet). `-p` preserves mode/timestamps
# so executable scripts stay executable across the mv. The temp file is cleaned
# on any failure so a partial write never lingers.
do_copy() {
  _src=$1
  _dest=$2
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  _parent=$(dirname "$_dest")
  [ -d "$_parent" ] || mkdir -p "$_parent"
  _tmp="$_dest.tmp.$$"
  # Copy to the sibling temp, then atomically rename. On any failure, remove the
  # partial temp and propagate failure (return 1) rather than leave debris.
  if cp -p "$_src" "$_tmp" && mv "$_tmp" "$_dest"; then
    return 0
  else
    rm -f "$_tmp"
    warn "failed to copy $_src -> $_dest"
    return 1
  fi
}

# prompt_conflict <src> <dest>
# Prompts the user (reading from /dev/tty) how to resolve a differing file.
# Echoes one of: overwrite | overwrite-all | skip | quit  on stdout.
# Re-prompts after showing a diff ("d").
#
# NOTE (H2): this ALWAYS runs in a command-substitution subshell
# (`_decision=$(prompt_conflict ...)`), so it must NOT try to set OVERWRITE_ALL
# itself -- that assignment would die with the subshell. Instead the "a" answer
# is communicated back to the caller as the `overwrite-all` token on stdout;
# the caller (copy_one, in the main shell) sets OVERWRITE_ALL=1.
prompt_conflict() {
  _src=$1
  _dest=$2
  while :; do
    # Prompt to /dev/tty so it is visible even if stdout is redirected; read the
    # answer from /dev/tty so it works even when stdin is piped.
    printf '%sfile %s differs. [o]verwrite / [s]kip / [d]iff / [a]ll-overwrite / [q]uit ?%s ' \
      "$C_YELLOW" "$_dest" "$C_RESET" >/dev/tty
    if ! IFS= read -r _ans </dev/tty; then
      # EOF on the tty -- treat as skip to avoid clobbering.
      echo skip
      return 0
    fi
    case $_ans in
      o | O) echo overwrite; return 0 ;;
      s | S | '') echo skip; return 0 ;;
      q | Q) echo quit; return 0 ;;
      a | A) echo overwrite-all; return 0 ;;
      d | D)
        printf '%s--- diff (%s vs %s): ---%s\n' "$C_DIM" "$_dest" "$_src" "$C_RESET" >/dev/tty
        # Unified diff, existing dest vs repo source. Never fatal under set -e.
        diff -u "$_dest" "$_src" >/dev/tty 2>&1 || true
        # loop and re-prompt the same file.
        ;;
      *)
        printf 'please answer o, s, d, a, or q.\n' >/dev/tty
        ;;
    esac
  done
}

# copy_one <src> <dest>  -- the heart. Deploys one file with full conflict,
# backup, idempotency, dry-run, force, and interactive handling. Mutates the
# summary counters in the current shell (callers must NOT pipe into this).
copy_one() {
  src=$1
  dest=$2

  if [ ! -e "$src" ]; then
    warn "source missing, cannot copy: $src"
    return 0
  fi

  # Symlink guard (C1). Never copy through a symlinked dest or a dest under a
  # legacy directory symlink -- that would write into the repo. Refuse + warn
  # (once per offending symlink component) and skip. `[ -e ]` above follows
  # symlinks; a dangling dest symlink reads as "not exists" and would fall into
  # the NEW branch, so we test symlinks explicitly BEFORE the existence checks.
  if symlink_on_path "$dest"; then
    warn_legacy_symlink "$dest"
    return 0
  fi

  # New file.
  if [ ! -e "$dest" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
      report_new "$dest (would create)"
    else
      do_copy "$src" "$dest"
      report_new "$dest"
    fi
    N_NEW=$((N_NEW + 1))
    return 0
  fi

  # Exists and identical -> nothing to do.
  if cmp -s "$src" "$dest"; then
    report_ok "$dest"
    N_OK=$((N_OK + 1))
    return 0
  fi

  # Exists and DIFFERS -> conflict.
  N_CONFLICT=$((N_CONFLICT + 1))

  # Dry run: report the intended outcome, make no changes, do not prompt.
  # M3: honor --force (or a prior all-overwrite) in the report so
  # `--dry-run --force` says "would update", not "would prompt". This branch
  # must precede the real force branch (which mutates); do_copy/backup_dest are
  # already no-ops under DRY_RUN, but reporting here keeps dry-run prompt-free.
  if [ "$DRY_RUN" -eq 1 ]; then
    if [ "$FORCE" -eq 1 ] || [ "$OVERWRITE_ALL" -eq 1 ]; then
      report_update "$dest (would update)"
    else
      report_conflict "$dest differs (would prompt)"
    fi
    return 0
  fi

  # Force, or a prior "all-overwrite": overwrite (with backup), no prompt.
  if [ "$FORCE" -eq 1 ] || [ "$OVERWRITE_ALL" -eq 1 ]; then
    report_conflict "$dest differs"
    backup_dest "$dest"
    do_copy "$src" "$dest"
    report_update "$dest"
    N_UPDATE=$((N_UPDATE + 1))
    return 0
  fi

  # Interactive path: only if we could actually open a controlling terminal.
  if [ "$HAVE_TTY" -eq 1 ]; then
    report_conflict "$dest differs"
    _decision=$(prompt_conflict "$src" "$dest")
    case $_decision in
      overwrite-all)
        # H2: persist "all-overwrite" in THIS (main) shell so every subsequent
        # conflict is auto-overwritten without re-prompting. Then fall through
        # to the same backup+copy as a one-off overwrite.
        OVERWRITE_ALL=1
        backup_dest "$dest"
        do_copy "$src" "$dest"
        report_update "$dest"
        N_UPDATE=$((N_UPDATE + 1))
        ;;
      overwrite)
        backup_dest "$dest"
        do_copy "$src" "$dest"
        report_update "$dest"
        N_UPDATE=$((N_UPDATE + 1))
        ;;
      skip)
        report_skip "$dest (kept your version)"
        N_SKIP=$((N_SKIP + 1))
        ;;
      quit)
        printf '\n%saborted by user.%s\n' "$C_YELLOW" "$C_RESET" >&2
        print_summary
        exit 130
        ;;
    esac
    return 0
  fi

  # Non-interactive, not forced: never clobber silently -- skip + warn.
  report_conflict "$dest differs"
  warn "not a TTY and no --force: leaving $dest untouched (use --force to overwrite)."
  N_SKIP=$((N_SKIP + 1))
  return 0
}

# deploy_subtree <src-root> <dest-root>
# Recursively copies every file under <src-root> (minus EXCLUDES) to the
# mirrored path under <dest-root>. Uses a temp file, NOT a pipe, so copy_one's
# counter mutations survive in this shell (a `find | while` loop runs in a
# subshell and would discard them).
deploy_subtree() {
  _srcroot=$1
  _destroot=$2

  # Legacy whole-dir symlink guard: if the destination root (or an ancestor of
  # it up to the config root) is itself a symlink, this is an OLD directory-
  # symlink install. Do NOT descend through it -- warn once and skip the root.
  if symlink_on_path "$_destroot"; then
    warn_legacy_symlink "$_destroot"
    return 0
  fi

  # Reuse the script-scoped temp path (cleaned by the EXIT/INT/TERM trap).
  _tmplist=$(mktemp "${TMPDIR:-/tmp}/dotfiles-install.XXXXXX") || {
    warn "could not create temp file; skipping $_srcroot"
    return 0
  }
  # Enumerate regular files. `|| true` so an empty/odd tree never aborts set -e.
  find "$_srcroot" -type f >"$_tmplist" 2>/dev/null || true

  while IFS= read -r _srcfile; do
    [ -n "$_srcfile" ] || continue
    # Path relative to the subtree root (strip "<root>/").
    _rel=${_srcfile#"$_srcroot"/}
    if is_excluded "$_rel"; then
      continue
    fi
    copy_one "$_srcfile" "$_destroot/$_rel"
  done <"$_tmplist"

  rm -f "$_tmplist"
  _tmplist=""
}

# deploy_entry <src> <dest>  -- dispatch a manifest entry: subtree if src is a
# directory, else a single file.
deploy_entry() {
  _src=$1
  _dest=$2
  if [ -d "$_src" ]; then
    deploy_subtree "$_src" "$_dest"
  else
    copy_one "$_src" "$_dest"
  fi
}

# ---------------------------------------------------------------------------
# Uninstall core
# ---------------------------------------------------------------------------
# remove_one <src> <dest>
# Removes dest ONLY if it is byte-identical to src (unchanged since deploy).
# A differing file is KEPT. A missing dest is a silent no-op. Honors DRY_RUN.
# Records the parent dir of any removal in $PRUNE_DIRS for later empty-dir prune.
PRUNE_DIRS=""
remove_one() {
  src=$1
  dest=$2

  # Symlink guard (C1). This is the data-loss case that motivated the fix: if
  # $dest resolves through a legacy directory symlink into the repo, `cmp -s`
  # would compare the repo source to itself ("identical") and `rm` would delete
  # a TRACKED file from the git working tree. Never compare/remove through a
  # symlink -- report KEPT (legacy symlink) and skip. Tested BEFORE `[ -e ]`
  # (which follows symlinks) so a legacy symlink is caught, not followed.
  if symlink_on_path "$dest"; then
    warn_legacy_symlink "$dest"
    return 0
  fi

  if [ ! -e "$dest" ]; then
    return 0
  fi

  if [ ! -e "$src" ]; then
    # No source to compare against -> cannot prove it is an unchanged copy.
    report_kept "$dest (no repo source to compare; leaving it)"
    N_KEPT=$((N_KEPT + 1))
    return 0
  fi

  if cmp -s "$src" "$dest"; then
    if [ "$DRY_RUN" -eq 1 ]; then
      report_removed "$dest (would remove)"
    else
      rm -f "$dest"
      report_removed "$dest"
      PRUNE_DIRS="$PRUNE_DIRS
$(dirname "$dest")"
    fi
    N_REMOVED=$((N_REMOVED + 1))
  else
    report_kept "$dest (modified)"
    N_KEPT=$((N_KEPT + 1))
  fi
}

# uninstall_subtree <src-root> <dest-root>  -- mirror of deploy_subtree.
uninstall_subtree() {
  _srcroot=$1
  _destroot=$2

  # Legacy whole-dir symlink guard (mirror of deploy_subtree): never descend
  # through, compare across, or remove a legacy directory symlink. On a machine
  # still running the OLD symlink install, $_destroot resolves THROUGH the
  # symlink into the repo, so a byte-compare would match the repo file to itself
  # and delete tracked files out of the working tree. Refuse and instruct.
  if symlink_on_path "$_destroot"; then
    warn_legacy_symlink "$_destroot"
    return 0
  fi

  # Reuse the script-scoped temp path (cleaned by the EXIT/INT/TERM trap).
  _tmplist=$(mktemp "${TMPDIR:-/tmp}/dotfiles-uninstall.XXXXXX") || {
    warn "could not create temp file; skipping $_srcroot"
    return 0
  }
  find "$_srcroot" -type f >"$_tmplist" 2>/dev/null || true

  while IFS= read -r _srcfile; do
    [ -n "$_srcfile" ] || continue
    _rel=${_srcfile#"$_srcroot"/}
    if is_excluded "$_rel"; then
      continue
    fi
    remove_one "$_srcfile" "$_destroot/$_rel"
  done <"$_tmplist"

  rm -f "$_tmplist"
  _tmplist=""
}

uninstall_entry() {
  _src=$1
  _dest=$2
  if [ -d "$_src" ]; then
    uninstall_subtree "$_src" "$_dest"
  else
    remove_one "$_src" "$_dest"
  fi
}

# prune_empty_dirs  -- rmdir (only-if-empty) the dirs that held removed files,
# walking upward toward the config roots. Never recursive, never forced; a
# non-empty dir (holds generated/user files) simply stops the walk. Skips the
# top-level config roots themselves.
prune_empty_dirs() {
  [ "$DRY_RUN" -eq 1 ] && return 0
  # Roots we must never rmdir even if momentarily empty.
  _roots="$XDG_CONFIG_HOME
$HOME"
  # De-dup and process each recorded dir, walking up.
  _oldifs=$IFS
  IFS='
'
  for _d in $PRUNE_DIRS; do
    [ -n "$_d" ] || continue
    _cur=$_d
    while [ -d "$_cur" ]; do
      # Stop at a protected root.
      _is_root=0
      for _r in $_roots; do
        [ "$_cur" = "$_r" ] && _is_root=1
      done
      [ "$_is_root" -eq 1 ] && break
      # Only remove if empty. rmdir fails (non-fatally) on a non-empty dir.
      if rmdir "$_cur" 2>/dev/null; then
        _cur=$(dirname "$_cur")
      else
        break
      fi
    done
  done
  IFS=$_oldifs
}

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
print_summary() {
  printf '\n%sSummary:%s\n' "$C_CYAN" "$C_RESET"
  if [ "$UNINSTALL" -eq 1 ]; then
    printf '  removed: %s   kept(modified): %s\n' "$N_REMOVED" "$N_KEPT"
  else
    printf '  new: %s   updated: %s   unchanged: %s   skipped: %s   conflicts: %s   backups: %s\n' \
      "$N_NEW" "$N_UPDATE" "$N_OK" "$N_SKIP" "$N_CONFLICT" "$N_BACKUP"
  fi
}

# ---------------------------------------------------------------------------
# Dependency checks (advisory only -- never blocks or mutates anything)
# ---------------------------------------------------------------------------
suggest_install() {
  what=$1
  case $what in
    wezterm)
      case $PLATFORM in
        macos) info "install with: brew install --cask wezterm" ;;
        linux | wsl) info "see https://wezterm.org/install/linux.html for your distro" ;;
        *) info "see https://wezterm.org/installation.html" ;;
      esac
      ;;
    tmux)
      case $PLATFORM in
        macos) info "install with: brew install tmux" ;;
        linux | wsl) info "install with your package manager, e.g. 'sudo apt install tmux' or 'sudo dnf install tmux'" ;;
        *) info "install tmux via your package manager" ;;
      esac
      ;;
    jq)
      case $PLATFORM in
        macos) info "install with: brew install jq" ;;
        linux | wsl) info "install with your package manager, e.g. 'sudo apt install jq' or 'sudo dnf install jq'" ;;
        *) info "install jq via your package manager (https://jqlang.github.io/jq/)" ;;
      esac
      ;;
    font)
      case $PLATFORM in
        macos) info "install with: brew install --cask font-hack-nerd-font" ;;
        linux | wsl) info "download from https://www.nerdfonts.com/font-downloads (package names vary by distro)" ;;
        *) info "download from https://www.nerdfonts.com/font-downloads" ;;
      esac
      ;;
    zsh_plugins)
      case $PLATFORM in
        macos)
          info "install with: brew install fzf zsh-autosuggestions zsh-syntax-highlighting"
          ;;
        linux | wsl)
          info "package names vary by distro. Common examples:"
          info "  Debian/Ubuntu: sudo apt install fzf zsh-autosuggestions zsh-syntax-highlighting"
          info "  Arch:          sudo pacman -S fzf zsh-autosuggestions zsh-syntax-highlighting"
          info "  Fedora:        sudo dnf install fzf zsh-autosuggestions zsh-syntax-highlighting"
          info "or install upstream manually:"
          info "  https://github.com/zsh-users/zsh-autosuggestions"
          info "  https://github.com/zsh-users/zsh-syntax-highlighting"
          info "(package availability/names are not guaranteed; verify for your distro.)"
          ;;
        *)
          info "install fzf, zsh-autosuggestions, zsh-syntax-highlighting via your package manager,"
          info "or from https://github.com/zsh-users/zsh-autosuggestions and"
          info "https://github.com/zsh-users/zsh-syntax-highlighting"
          ;;
      esac
      ;;
  esac
}

check_deps() {
  printf '\nDependency check (advisory):\n'

  if command -v wezterm >/dev/null 2>&1; then
    ok "wezterm found: $(wezterm --version 2>/dev/null || echo present)"
  else
    warn "wezterm not found on PATH."
    suggest_install wezterm
  fi

  if command -v tmux >/dev/null 2>&1; then
    ok "tmux found: $(tmux -V 2>/dev/null || echo present)"
  else
    warn "tmux not found on PATH."
    suggest_install tmux
  fi

  # jq drives the theme generator (render.sh). Without it, the native theme
  # files can't be produced and wezterm/tmux/nvim/opencode fall back to built-ins.
  if command -v jq >/dev/null 2>&1; then
    ok "jq found: $(jq --version 2>/dev/null || echo present)"
  else
    warn "jq not found on PATH (required to generate themes via render.sh)."
    suggest_install jq
  fi

  # Font detection is best-effort and platform dependent. We only run a check
  # we can trust; where we cannot, we say so rather than guess.
  if command -v wezterm >/dev/null 2>&1; then
    if wezterm ls-fonts --list-system 2>/dev/null | grep -qi "Hack Nerd Font"; then
      ok "Hack Nerd Font detected (via wezterm ls-fonts)."
    else
      warn "Hack Nerd Font not detected by 'wezterm ls-fonts'."
      warn "  The config still requests it; WezTerm falls back to a system font if absent."
      suggest_install font
    fi
  elif [ "$PLATFORM" = macos ] && command -v fc-list >/dev/null 2>&1; then
    if fc-list 2>/dev/null | grep -qi "Hack Nerd Font"; then
      ok "Hack Nerd Font detected (via fc-list)."
    else
      warn "Hack Nerd Font not detected (via fc-list)."
      suggest_install font
    fi
  elif command -v fc-list >/dev/null 2>&1; then
    if fc-list 2>/dev/null | grep -qi "Hack Nerd Font"; then
      ok "Hack Nerd Font detected (via fc-list)."
    else
      warn "Hack Nerd Font not detected (via fc-list)."
      suggest_install font
    fi
  else
    warn "Cannot reliably check for Hack Nerd Font on this platform (no wezterm/fc-list)."
    info "If glyphs render as boxes, install Hack Nerd Font manually."
    suggest_install font
  fi

  check_zsh
}

# ---------------------------------------------------------------------------
# zsh dependency checks (advisory only).
# ---------------------------------------------------------------------------
# Mirrors the discovery logic in dotfiles/zsh/interactive.zsh: an override
# directory, then `brew --prefix <formula>` (whose printed path is verified to
# actually contain the file, because brew prints a path and exits 0 even for an
# UNINSTALLED formula), then common distro locations.
# Args: <override-dir> <brew-formula> <relative-file> [extra dirs...]
# Prints the found path on stdout and returns 0, or returns 1 if not found.
find_zsh_plugin() {
  override_dir=$1
  formula=$2
  relfile=$3
  shift 3

  if [ -n "$override_dir" ] && [ -r "$override_dir/$relfile" ]; then
    printf '%s\n' "$override_dir/$relfile"
    return 0
  fi

  if command -v brew >/dev/null 2>&1; then
    brew_prefix=$(brew --prefix "$formula" 2>/dev/null || true)
    if [ -n "$brew_prefix" ]; then
      if [ -r "$brew_prefix/share/$formula/$relfile" ]; then
        printf '%s\n' "$brew_prefix/share/$formula/$relfile"
        return 0
      elif [ -r "$brew_prefix/$relfile" ]; then
        printf '%s\n' "$brew_prefix/$relfile"
        return 0
      fi
    fi
  fi

  for cand in "$@"; do
    if [ -r "$cand/$relfile" ]; then
      printf '%s\n' "$cand/$relfile"
      return 0
    fi
  done

  return 1
}

check_zsh() {
  printf '\nzsh interactive fragment (advisory):\n'

  if command -v zsh >/dev/null 2>&1; then
    ok "zsh found: $(zsh --version 2>/dev/null || echo present)"
  else
    warn "zsh not found on PATH. The interactive.zsh fragment requires zsh."
    case $PLATFORM in
      macos) info "macOS ships zsh by default; if missing: brew install zsh" ;;
      linux | wsl) info "install with your package manager, e.g. 'sudo apt install zsh'" ;;
      *) info "install zsh via your package manager" ;;
    esac
  fi

  # fzf (optional): the fragment uses `fzf --zsh` when supported.
  if command -v fzf >/dev/null 2>&1; then
    if fzf --zsh >/dev/null 2>&1; then
      ok "fzf found with 'fzf --zsh' support: $(fzf --version 2>/dev/null || echo present)"
    else
      warn "fzf found but too old for 'fzf --zsh': $(fzf --version 2>/dev/null || echo present)"
      info "the fragment skips fzf integration on versions without 'fzf --zsh'."
    fi
  else
    warn "fzf not found (optional; enables fuzzy history/completion)."
  fi

  # zsh-autosuggestions (optional).
  as_file=$(find_zsh_plugin \
    "${ZSH_AUTOSUGGEST_DIR:-}" \
    zsh-autosuggestions \
    zsh-autosuggestions.zsh \
    /usr/share/zsh-autosuggestions \
    /usr/share/zsh/plugins/zsh-autosuggestions \
    /usr/local/share/zsh-autosuggestions) || as_file=""
  if [ -n "$as_file" ]; then
    ok "zsh-autosuggestions found: $as_file"
  else
    warn "zsh-autosuggestions not found (optional; enables inline suggestions + Ctrl-f accept)."
  fi

  # zsh-syntax-highlighting (optional).
  sh_file=$(find_zsh_plugin \
    "${ZSH_SYNTAX_HIGHLIGHTING_DIR:-}" \
    zsh-syntax-highlighting \
    zsh-syntax-highlighting.zsh \
    /usr/share/zsh-syntax-highlighting \
    /usr/share/zsh/plugins/zsh-syntax-highlighting \
    /usr/local/share/zsh-syntax-highlighting) || sh_file=""
  if [ -n "$sh_file" ]; then
    ok "zsh-syntax-highlighting found: $sh_file"
  else
    warn "zsh-syntax-highlighting not found (optional; enables command highlighting)."
  fi

  if [ -z "$as_file" ] || [ -z "$sh_file" ] || ! command -v fzf >/dev/null 2>&1; then
    suggest_install zsh_plugins
  fi
  info "override discovery with ZSH_AUTOSUGGEST_DIR / ZSH_SYNTAX_HIGHLIGHTING_DIR if installed elsewhere."
}

# ---------------------------------------------------------------------------
# Neovim LSP server checks (advisory only).
# ---------------------------------------------------------------------------
# The Neovim config (dotfiles/nvim) uses the native 0.11 LSP client: each
# server in lsp/<name>.lua is enabled via vim.lsp.enable(), and the server
# BINARY must be on your $PATH. There is no plugin manager and no Mason to
# auto-install them, so this advisory check names what's missing and how to get
# it. Like every other check here it NEVER installs or mutates anything.
#
# check_lsp_server <binary> <macos-hint> <generic-hint>
check_lsp_server() {
  bin=$1
  macos_hint=$2
  generic_hint=$3
  if command -v "$bin" >/dev/null 2>&1; then
    ok "$bin found."
  else
    warn "$bin not found on PATH."
    case $PLATFORM in
      macos) info "$macos_hint" ;;
      *) info "$generic_hint" ;;
    esac
  fi
}

check_lsp_servers() {
  printf '\nNeovim LSP servers (advisory):\n'
  check_lsp_server lua-language-server \
    "install with: brew install lua-language-server" \
    "install lua-language-server via your package manager, or see https://luals.github.io/#install"
  check_lsp_server pyright-langserver \
    "install with: brew install pyright  (or: npm i -g pyright)" \
    "install with: npm i -g pyright"
  check_lsp_server bash-language-server \
    "install with: npm i -g bash-language-server" \
    "install with: npm i -g bash-language-server"
  check_lsp_server yaml-language-server \
    "install with: npm i -g yaml-language-server" \
    "install with: npm i -g yaml-language-server"
  check_lsp_server terraform-ls \
    "install with: brew install terraform-ls" \
    "see https://github.com/hashicorp/terraform-ls/releases (or your package manager)"
  check_lsp_server rust-analyzer \
    "install with: brew install rust-analyzer" \
    "install with: rustup component add rust-analyzer"
  check_lsp_server gopls \
    "install with: go install golang.org/x/tools/gopls@latest" \
    "install with: go install golang.org/x/tools/gopls@latest"
  check_lsp_server typescript-language-server \
    "install with: npm i -g typescript-language-server typescript" \
    "install with: npm i -g typescript-language-server typescript"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '(dry run -- no changes will be made)\n'
  fi

  if [ "$UNINSTALL" -eq 1 ]; then
    printf 'Uninstalling dotfiles (removing only unchanged copies):\n'
    info "repo dotfiles dir: $SCRIPT_DIR"
    # Iterate the manifest.
    _oldifs=$IFS
    IFS='
'
    for _entry in $MANIFEST; do
      [ -n "$_entry" ] || continue
      _src=${_entry%%|*}
      _dest=${_entry#*|}
      uninstall_entry "$_src" "$_dest"
    done
    IFS=$_oldifs
    prune_empty_dirs
    print_summary
    exit 0
  fi

  printf 'Installing dotfiles (copy model):\n'
  info "repo dotfiles dir: $SCRIPT_DIR"
  if [ "$FORCE" -eq 1 ]; then
    info "--force: conflicts will be overwritten (after backup)."
  fi

  # Deploy every manifest entry.
  _oldifs=$IFS
  IFS='
'
  for _entry in $MANIFEST; do
    [ -n "$_entry" ] || continue
    _src=${_entry%%|*}
    _dest=${_entry#*|}
    deploy_entry "$_src" "$_dest"
  done
  IFS=$_oldifs

  # Generate the PUBLIC themes (nebula, rosepine) as native theme files into
  # each tool's own theme dir (~/.config/{wezterm/colors,nvim/colors,
  # opencode/themes,tmux/themes}) so wezterm/nvim/opencode/tmux can discover and
  # source them. `render.sh --all` renders only the baked-in public palettes;
  # local-only palettes (kept outside this repo) are intentionally NOT rendered
  # here -- generate those yourself with `sh render.sh /path/to/palette.json`.
  # render.sh honors $XDG_CONFIG_HOME (and per-tool *_DIR overrides), so we
  # export it here to keep the render targets in lockstep with the copy targets.
  # Advisory + non-fatal: requires jq, and a failure here must never abort an
  # otherwise-successful install under `set -e`.
  export XDG_CONFIG_HOME
  if [ "$DRY_RUN" -eq 1 ]; then
    report_would "run render.sh --all (generate native theme files into ~/.config/{wezterm,nvim,opencode,tmux})"
  elif command -v jq >/dev/null 2>&1; then
    if (cd "$THEMES_SRC" && sh render.sh --all >/dev/null 2>&1); then
      ok "rendered public themes (nebula, rosepine) into the tool theme dirs"
    else
      warn "render.sh failed; themes will use built-in fallbacks until it succeeds."
    fi
  else
    warn "jq not found; skipping theme render. wezterm/tmux/nvim/opencode use built-in fallbacks."
    info "install jq, then run: sh $THEMES_SRC/render.sh --all"
  fi

  # Dependency checks are advisory. Never let a future probe that returns
  # non-zero abort an otherwise successful install under `set -e`.
  check_deps || true
  check_lsp_servers || true

  print_summary

  printf '\n%sDone.%s\n' "$C_GREEN" "$C_RESET"
  printf '\nTo activate the zsh fragment, add this ONE line to your ~/.zshrc\n'
  printf '(this installer does NOT edit ~/.zshrc for you):\n'
  # The parameter expansion below is printed LITERALLY for the user to paste
  # into their own ~/.zshrc; it must not be expanded here.
  # shellcheck disable=SC2016
  printf '  source "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/interactive.zsh"\n'
  printf 'Then reload with: exec zsh\n'
}

main
