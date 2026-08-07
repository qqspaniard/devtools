# interactive.zsh -- framework-free zsh interactive enhancements.
#
# What this fragment does, in order:
#   1. Native zsh completion (compinit), skipped if already initialized.
#   2. fzf key-bindings + completion, via `fzf --zsh` (if fzf supports it).
#   3. zsh-autosuggestions (if installed), + Ctrl-f -> accept suggestion.
#   4. zsh-syntax-highlighting (if installed) -- MUST be loaded LAST.
#
# It is intentionally NOT a full .zshrc: your prompt, aliases, PATH,
# environment, and any machine-specific or sensitive settings stay in your own
# local, untracked ~/.zshrc. This file only layers on completion and the two
# optional plugins, and does nothing you cannot see here.
#
# Usage: add exactly ONE line to your local ~/.zshrc (the installer does NOT
# edit ~/.zshrc for you):
#
#     source "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/interactive.zsh"
#
# It is safe to source from a non-interactive shell (it returns immediately),
# and safe to source more than once (each section guards against redoing work
# where a reliable marker exists).
#
# No packages are installed and no plugins are git-cloned by this file. Missing
# optional tools simply mean the corresponding feature is absent -- startup
# stays silent. Set ZSH_INTERACTIVE_DEBUG=1 before sourcing to print advisory
# messages about what was and was not loaded.

# ---------------------------------------------------------------------------
# 0. Interactive-only guard.
# ---------------------------------------------------------------------------
# `$-` contains 'i' for interactive shells. Bailing here keeps this fragment
# harmless when sourced by scripts, `zsh -c`, or tooling. `return` at the top
# level of a sourced file is valid in zsh and returns to the caller.
case $- in
  *i*) ;;
  *) return 0 ;;
esac

# Small opt-in debug logger. No output unless ZSH_INTERACTIVE_DEBUG is set.
_zi_debug() {
  [[ -n ${ZSH_INTERACTIVE_DEBUG:-} ]] && print -r -- "interactive.zsh: $*" >&2
  return 0
}

# ---------------------------------------------------------------------------
# 1. Native zsh completion.
# ---------------------------------------------------------------------------
# If completion is already set up (e.g. Oh My Zsh, Prezto, or a prior compinit
# in this same shell), the `compdef` function exists. Re-running compinit then
# is wasted work, so we skip it. Otherwise autoload and initialize.
if (( $+functions[compdef] )); then
  _zi_debug "completion already initialized (compdef present); skipping compinit"
else
  autoload -Uz compinit && compinit
  _zi_debug "ran compinit"
fi

# ---------------------------------------------------------------------------
# 2. fzf integration.
# ---------------------------------------------------------------------------
# Modern fzf (>= 0.48) prints its zsh key-bindings + completion via
# `fzf --zsh`. Older fzf lacks this flag; we detect support and skip silently
# if unavailable, so no startup error is emitted on old versions.
#
# NOTE: if your ~/.zshrc already does `source <(fzf --zsh)` (or the legacy
# ~/.fzf.zsh), remove/comment that line before sourcing this fragment to avoid
# loading fzf twice. As a best effort we skip when fzf's key-binding widget is
# already defined in this shell.
if [[ -z ${ZI_FZF_LOADED:-} ]] && command -v fzf >/dev/null 2>&1; then
  if (( $+functions[fzf-history-widget] )); then
    _zi_debug "fzf widgets already present; skipping fzf --zsh"
    ZI_FZF_LOADED=1
  elif fzf --zsh >/dev/null 2>&1; then
    source <(fzf --zsh)
    ZI_FZF_LOADED=1
    _zi_debug "sourced fzf --zsh"
  else
    _zi_debug "fzf found but 'fzf --zsh' unsupported; skipping"
  fi
fi

# ---------------------------------------------------------------------------
# Plugin path discovery helper.
# ---------------------------------------------------------------------------
# Resolves the source file for an optional plugin without hard-coding a
# Homebrew prefix. Search order:
#   1. An explicit override variable (e.g. $ZSH_AUTOSUGGEST_DIR), if the file
#      is readable there. This is the escape hatch for unusual layouts.
#   2. `brew --prefix <formula>` -- works for Apple Silicon (/opt/homebrew),
#      Intel macOS (/usr/local), and Linuxbrew (/home/linuxbrew/...) without
#      assuming any specific prefix. IMPORTANT: `brew --prefix <formula>` prints
#      a path and exits 0 even when the formula is NOT installed, so we must
#      verify the resulting file is actually readable rather than trust exit
#      status.
#   3. A list of common distro / manual install locations.
# Prints the first readable candidate and returns 0; returns 1 if none found.
#
# Args: <override-dir> <brew-formula> <relative-file> [extra candidate dirs...]
_zi_find_plugin() {
  local override_dir=$1 formula=$2 relfile=$3
  shift 3
  local candidate

  # 1. Explicit override directory.
  if [[ -n $override_dir && -r $override_dir/$relfile ]]; then
    print -r -- "$override_dir/$relfile"
    return 0
  fi

  # 2. Homebrew (prefix discovered dynamically; file existence verified).
  if command -v brew >/dev/null 2>&1; then
    local brew_prefix
    brew_prefix=$(brew --prefix "$formula" 2>/dev/null)
    if [[ -n $brew_prefix && -r $brew_prefix/$relfile ]]; then
      print -r -- "$brew_prefix/$relfile"
      return 0
    fi
  fi

  # 3. Common distro / manual locations passed by the caller.
  for candidate in "$@"; do
    if [[ -r $candidate/$relfile ]]; then
      print -r -- "$candidate/$relfile"
      return 0
    fi
  done

  return 1
}

# ---------------------------------------------------------------------------
# 3. zsh-autosuggestions (optional).
# ---------------------------------------------------------------------------
# Load only if we can find the source file. Override with ZSH_AUTOSUGGEST_DIR
# (point it at the directory containing zsh-autosuggestions.zsh). Common distro
# packages install to /usr/share/zsh-autosuggestions (Debian/Ubuntu/Arch) or
# /usr/share/zsh/plugins/zsh-autosuggestions (some distros).
if (( $+functions[_zsh_autosuggest_start] )); then
  _zi_debug "zsh-autosuggestions already loaded; skipping"
else
  _zi_autosuggest_file=$(
    _zi_find_plugin \
      "${ZSH_AUTOSUGGEST_DIR:-}" \
      zsh-autosuggestions \
      zsh-autosuggestions.zsh \
      /usr/share/zsh-autosuggestions \
      /usr/share/zsh/plugins/zsh-autosuggestions \
      /usr/local/share/zsh-autosuggestions
  )
  if [[ -n $_zi_autosuggest_file ]]; then
    source "$_zi_autosuggest_file"
    _zi_debug "sourced zsh-autosuggestions from $_zi_autosuggest_file"
  else
    _zi_debug "zsh-autosuggestions not found; skipping (no suggestions)"
  fi
  unset _zi_autosuggest_file
fi

# Bind Ctrl-f to accept the current autosuggestion, but only if the widget
# actually exists (i.e. the plugin loaded, now or earlier). Guarding on the
# widget avoids a "no such widget" error when the plugin is absent.
if (( $+widgets[autosuggest-accept] )); then
  bindkey '^F' autosuggest-accept
  _zi_debug "bound ^F -> autosuggest-accept"
fi

# ---------------------------------------------------------------------------
# 4. zsh-syntax-highlighting (optional) -- MUST BE LAST.
# ---------------------------------------------------------------------------
# Upstream requires this be sourced at the very end of interactive config,
# after all other widget-defining plugins, so it can wrap them. Override with
# ZSH_SYNTAX_HIGHLIGHTING_DIR. Common distro packages install to
# /usr/share/zsh-syntax-highlighting or
# /usr/share/zsh/plugins/zsh-syntax-highlighting.
if (( $+functions[_zsh_highlight] )) || [[ -n ${ZSH_HIGHLIGHT_VERSION:-} ]]; then
  _zi_debug "zsh-syntax-highlighting already loaded; skipping"
else
  _zi_highlight_file=$(
    _zi_find_plugin \
      "${ZSH_SYNTAX_HIGHLIGHTING_DIR:-}" \
      zsh-syntax-highlighting \
      zsh-syntax-highlighting.zsh \
      /usr/share/zsh-syntax-highlighting \
      /usr/share/zsh/plugins/zsh-syntax-highlighting \
      /usr/local/share/zsh-syntax-highlighting
  )
  if [[ -n $_zi_highlight_file ]]; then
    source "$_zi_highlight_file"
    _zi_debug "sourced zsh-syntax-highlighting from $_zi_highlight_file"
  else
    _zi_debug "zsh-syntax-highlighting not found; skipping (no highlighting)"
  fi
  unset _zi_highlight_file
fi
