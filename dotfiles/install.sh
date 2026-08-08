#!/bin/sh
# install.sh -- portable installer for the WezTerm + tmux + zsh dotfiles.
#
# What it does:
#   * Symlinks ~/.config/wezterm  -> <repo>/dotfiles/wezterm   (whole directory)
#   * Symlinks ~/.config/tmux     -> <repo>/dotfiles/tmux      (whole directory)
#   * Symlinks ~/.tmux.conf       -> ~/.config/tmux/tmux.conf  (compat shim)
#   * Symlinks ~/.config/zsh      -> <repo>/dotfiles/zsh       (whole directory)
#   * Symlinks ~/.config/nvim     -> <repo>/dotfiles/nvim      (whole directory)
#   * Symlinks the tmux agent-status opencode plugin into
#       ~/.config/opencode/plugins/  (see "opencode plugin" note below)
#
# Why a directory symlink for WezTerm and Neovim: linking the entire directory
# guarantees the modular `require`d fragments (wezterm's appearance/keybindings,
# nvim's lua/ and lsp/ modules) always resolve, in one atomic link. The tmux
# directory is linked whole for the same reason: tmux.conf sources sibling
# scripts/ and themes/ (the agent-status feature), which only resolve if the
# whole tmux/ dir is present at a stable path. ~/.tmux.conf is kept as a thin
# symlink INTO that linked dir so tmux older than 3.1 (which only reads the
# classic ~/.tmux.conf path) still works.
#
# opencode plugin: the agent-status feature has an opencode-side half
# (tmux/plugins/tmux-agent-state.ts) that must live in ~/.config/opencode/
# plugins/ to be loaded. It is co-located with the tmux feature it belongs to
# and symlinked into place here. tmux.conf exports TMUX_AGENT_SCRIPT_DIR so the
# plugin and tmux both find the shared scripts/ dir.
#
# The zsh directory is linked whole (same rationale as wezterm) so future
# fragments alongside interactive.zsh resolve in one atomic link. This installer
# does NOT edit your ~/.zshrc. To activate the fragment, add exactly one line to
# your own ~/.zshrc (see dotfiles/README.md):
#   source "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/interactive.zsh"
#
# Safety:
#   * Idempotent: re-running when links already point at the right targets is a
#     no-op.
#   * Never overwrites regular files, directories, or unrelated symlinks. It
#     prints a clear remediation message and leaves your data untouched.
#
# POSIX sh; no bashisms. Works on macOS, Linux, and WSL.

set -eu

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

# The repo's dotfiles source directories.
WEZTERM_SRC="$SCRIPT_DIR/wezterm"
TMUX_DIR_SRC="$SCRIPT_DIR/tmux"
ZSH_SRC="$SCRIPT_DIR/zsh"
NVIM_SRC="$SCRIPT_DIR/nvim"
OC_PLUGIN_SRC="$SCRIPT_DIR/tmux/plugins/tmux-agent-state.ts"

# Destinations.
: "${XDG_CONFIG_HOME:=$HOME/.config}"
WEZTERM_DEST="$XDG_CONFIG_HOME/wezterm"
TMUX_DIR_DEST="$XDG_CONFIG_HOME/tmux"
TMUX_CONF_DEST="$HOME/.tmux.conf"
ZSH_DEST="$XDG_CONFIG_HOME/zsh"
NVIM_DEST="$XDG_CONFIG_HOME/nvim"
OC_PLUGIN_DEST="$XDG_CONFIG_HOME/opencode/plugins/tmux-agent-state.ts"
# ~/.tmux.conf is a compat shim for tmux < 3.1 (which only reads the classic
# path). It points straight at the repo's tmux.conf. Modern tmux reads the same
# file via the ~/.config/tmux dir link, which also makes sibling scripts/ and
# themes/ resolve. The two links are independent by design.
TMUX_CONF_SRC="$TMUX_DIR_SRC/tmux.conf"

# ---------------------------------------------------------------------------
# Options
# ---------------------------------------------------------------------------
DRY_RUN=0
UNINSTALL=0

usage() {
  cat <<EOF
Usage: install.sh [--dry-run] [--uninstall] [-h|--help]

  (no options)  Create the symlinks described above.
  --dry-run     Print what would happen; make no changes.
  --uninstall   Remove ONLY the symlinks this installer created (links that
                point back into this repo). Leaves anything else alone.
  -h, --help    Show this help.
EOF
}

for arg in "$@"; do
  case $arg in
    --dry-run) DRY_RUN=1 ;;
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
# Output helpers
# ---------------------------------------------------------------------------
info() { printf '  %s\n' "$1"; }
ok() { printf 'OK    %s\n' "$1"; }
warn() { printf 'WARN  %s\n' "$1" >&2; }
skip() { printf 'SKIP  %s\n' "$1"; }
act() { printf 'DO    %s\n' "$1"; }

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
# Linking core
# ---------------------------------------------------------------------------
# link_one <source> <dest>
# Creates a symlink dest -> source with full collision safety and idempotency.
link_one() {
  src=$1
  dest=$2

  if [ ! -e "$src" ]; then
    warn "source missing, cannot link: $src"
    return 1
  fi

  # Already a symlink?
  if [ -h "$dest" ]; then
    current=$(readlink "$dest")
    # Normalize the current target to an absolute path for comparison.
    case $current in
      /*) current_abs=$current ;;
      *) current_abs=$(cd "$(dirname "$dest")" && cd "$(dirname "$current")" 2>/dev/null && pwd)/$(basename "$current") ;;
    esac
    if [ "$current_abs" = "$src" ] || [ "$current" = "$src" ]; then
      skip "$dest already links to $src"
      return 0
    fi
    warn "$dest is a symlink to '$current' (not our target)."
    warn "  remediation: inspect it, then remove with: rm '$dest'  and re-run."
    return 1
  fi

  # A regular file or directory occupies the destination.
  if [ -e "$dest" ]; then
    if [ -d "$dest" ]; then
      warn "$dest is an existing directory; refusing to replace it."
    else
      warn "$dest is an existing file; refusing to overwrite it."
    fi
    warn "  remediation: back it up and remove it yourself, then re-run:"
    warn "    mv '$dest' '$dest.bak' && sh install.sh"
    return 1
  fi

  # Clear to create. Ensure the parent directory exists.
  parent=$(dirname "$dest")
  if [ "$DRY_RUN" -eq 1 ]; then
    if [ ! -d "$parent" ]; then
      act "mkdir -p $parent"
    fi
    act "ln -s $src $dest"
    return 0
  fi

  [ -d "$parent" ] || mkdir -p "$parent"
  ln -s "$src" "$dest"
  ok "linked $dest -> $src"
}

# unlink_one <source> <dest>
# Removes dest ONLY if it is a symlink pointing at source. Never deletes files.
unlink_one() {
  src=$1
  dest=$2

  if [ ! -h "$dest" ]; then
    if [ -e "$dest" ]; then
      skip "$dest is not our symlink; leaving it untouched."
    else
      skip "$dest does not exist."
    fi
    return 0
  fi

  current=$(readlink "$dest")
  case $current in
    /*) current_abs=$current ;;
    *) current_abs=$(cd "$(dirname "$dest")" && cd "$(dirname "$current")" 2>/dev/null && pwd)/$(basename "$current") ;;
  esac
  if [ "$current_abs" = "$src" ] || [ "$current" = "$src" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
      act "rm $dest  (symlink -> $src)"
    else
      rm "$dest"
      ok "removed symlink $dest"
    fi
  else
    skip "$dest is a symlink to '$current' (not ours); leaving it untouched."
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
# Main
# ---------------------------------------------------------------------------
main() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '(dry run -- no changes will be made)\n'
  fi

  if [ "$UNINSTALL" -eq 1 ]; then
    printf 'Uninstalling dotfile symlinks:\n'
    rc=0
    unlink_one "$WEZTERM_SRC" "$WEZTERM_DEST" || rc=1
    unlink_one "$TMUX_CONF_SRC" "$TMUX_CONF_DEST" || rc=1
    unlink_one "$TMUX_DIR_SRC" "$TMUX_DIR_DEST" || rc=1
    unlink_one "$OC_PLUGIN_SRC" "$OC_PLUGIN_DEST" || rc=1
    unlink_one "$ZSH_SRC" "$ZSH_DEST" || rc=1
    unlink_one "$NVIM_SRC" "$NVIM_DEST" || rc=1
    exit "$rc"
  fi

  printf 'Installing dotfile symlinks:\n'
  info "repo dotfiles dir: $SCRIPT_DIR"
  rc=0
  link_one "$WEZTERM_SRC" "$WEZTERM_DEST" || rc=1
  # Whole tmux dir (so sibling scripts/ + themes/ resolve) plus the ~/.tmux.conf
  # compat shim pointing straight at the repo file. Independent links.
  link_one "$TMUX_DIR_SRC" "$TMUX_DIR_DEST" || rc=1
  link_one "$TMUX_CONF_SRC" "$TMUX_CONF_DEST" || rc=1
  link_one "$OC_PLUGIN_SRC" "$OC_PLUGIN_DEST" || rc=1
  link_one "$ZSH_SRC" "$ZSH_DEST" || rc=1
  link_one "$NVIM_SRC" "$NVIM_DEST" || rc=1

  # Dependency checks are advisory. Never let a future probe that returns
  # non-zero abort an otherwise successful install under `set -e`.
  check_deps || true

  if [ "$rc" -ne 0 ]; then
    printf '\nFinished with warnings. See messages above for remediation.\n' >&2
  else
    printf '\nDone.\n'
    printf '\nTo activate the zsh fragment, add this ONE line to your ~/.zshrc\n'
    printf '(this installer does NOT edit ~/.zshrc for you):\n'
    # The parameter expansion below is printed LITERALLY for the user to paste
    # into their own ~/.zshrc; it must not be expanded here.
    # shellcheck disable=SC2016
    printf '  source "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/interactive.zsh"\n'
    printf 'Then reload with: exec zsh\n'
  fi
  exit "$rc"
}

main
