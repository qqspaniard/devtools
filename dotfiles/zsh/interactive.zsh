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
# is wasted work, so we skip it. Otherwise use a stable dump and perform the
# security audit at most once per 24 hours. The audit marker is refreshed only
# after a successful normal compinit; `-C` is never used without that proof.
if (( $+functions[compdef] )); then
  _zi_debug "completion already initialized (compdef present); skipping compinit"
else
  autoload -Uz compinit
  zmodload zsh/datetime 2>/dev/null
  zmodload zsh/stat 2>/dev/null

  _zi_cache_dir=${XDG_CACHE_HOME:-$HOME/.cache}/zsh
  _zi_compdump=$_zi_cache_dir/zcompdump
  _zi_compaudit_marker=$_zi_cache_dir/compaudit-ok
  mkdir -p "$_zi_cache_dir"

  _zi_recent_audit=0
  if [[ -r $_zi_compdump && -e $_zi_compaudit_marker ]] \
    && zstat -A _zi_marker_mtime +mtime -- "$_zi_compaudit_marker" 2>/dev/null \
    && (( EPOCHSECONDS >= _zi_marker_mtime[1] \
      && EPOCHSECONDS - _zi_marker_mtime[1] < 86400 )); then
    _zi_recent_audit=1
  fi

  if (( _zi_recent_audit )); then
    compinit -C -d "$_zi_compdump"
    _zi_debug "ran compinit from $_zi_compdump (recent security audit)"
  elif compinit -d "$_zi_compdump"; then
    : >| "$_zi_compaudit_marker"
    _zi_debug "ran compinit with security audit and refreshed marker"
  else
    _zi_debug "compinit failed; security-audit marker was not refreshed"
  fi

  unset _zi_cache_dir _zi_compdump _zi_compaudit_marker _zi_recent_audit
  unset _zi_marker_mtime
fi

# ---------------------------------------------------------------------------
# 2. fzf integration.
# ---------------------------------------------------------------------------
# Modern fzf (>= 0.48) prints its zsh key-bindings + completion via
# `fzf --zsh`. Invoke it exactly once and source the generated integration.
#
# NOTE: if your ~/.zshrc already does `source <(fzf --zsh)` (or the legacy
# ~/.fzf.zsh), remove/comment that line before sourcing this fragment to avoid
# loading fzf twice. As a best effort we skip when fzf's key-binding widget is
# already defined in this shell.
if [[ -z ${ZI_FZF_LOADED:-} ]] && command -v fzf >/dev/null 2>&1; then
  if (( $+functions[fzf-history-widget] )); then
    _zi_debug "fzf widgets already present; skipping fzf --zsh"
    ZI_FZF_LOADED=1
  else
    source <(fzf --zsh 2>/dev/null) 2>/dev/null
    ZI_FZF_LOADED=1
    _zi_debug "sourced fzf --zsh"
  fi
fi

# ---------------------------------------------------------------------------
# Plugin path discovery helper.
# ---------------------------------------------------------------------------
# Resolves the source file for an optional plugin without running Homebrew on
# the normal startup path. Search order:
#   1. An explicit override variable (e.g. $ZSH_AUTOSUGGEST_DIR), if the file
#      is readable there. This is the escape hatch for unusual layouts.
#   2. Stable Homebrew `opt` symlinks under $HOMEBREW_PREFIX and the standard
#      Apple Silicon, Intel macOS, and Linuxbrew prefixes.
#   3. Common distro / manual install locations.
#   4. A cached prior Homebrew result, then `brew --prefix` only on a cache miss
#      or when the cached path is no longer readable.
# Prints the first readable candidate and returns 0; returns 1 if none found.
#
# Args: <override-dir> <brew-formula> <relative-file> [extra candidate dirs...]
_zi_find_plugin() {
  local override_dir=$1 formula=$2 relfile=$3
  shift 3
  local candidate prefix plugin_file

  # 1. Explicit override directory.
  if [[ -n $override_dir && -r $override_dir/$relfile ]]; then
    print -r -- "$override_dir/$relfile"
    return 0
  fi

  # 2. Stable Homebrew opt symlinks (survive formula version upgrades).
  for prefix in \
    "${HOMEBREW_PREFIX:-}" \
    /opt/homebrew \
    /usr/local \
    /home/linuxbrew/.linuxbrew; do
    [[ -n $prefix ]] || continue
    for plugin_file in \
      "$prefix/opt/$formula/share/$formula/$relfile" \
      "$prefix/opt/$formula/$relfile"; do
      if [[ -r $plugin_file ]]; then
        print -r -- "$plugin_file"
        return 0
      fi
    done
  done

  # 3. Common distro / manual locations passed by the caller.
  for candidate in "$@"; do
    if [[ -r $candidate/$relfile ]]; then
      print -r -- "$candidate/$relfile"
      return 0
    fi
  done

  # 4. Portable Homebrew fallback. Cache only a verified source-file path;
  # formula upgrades normally keep it valid through Homebrew's opt symlink.
  local cache_dir=${XDG_CACHE_HOME:-$HOME/.cache}/zsh/plugin-paths
  local cache_file=$cache_dir/$formula
  if [[ -r $cache_file ]]; then
    IFS= read -r plugin_file < "$cache_file"
    if [[ -r $plugin_file ]]; then
      print -r -- "$plugin_file"
      return 0
    fi
  fi

  if command -v brew >/dev/null 2>&1; then
    prefix=$(brew --prefix "$formula" 2>/dev/null)
    for plugin_file in "$prefix/share/$formula/$relfile" "$prefix/$relfile"; do
      if [[ -n $prefix && -r $plugin_file ]]; then
        mkdir -p "$cache_dir"
        print -r -- "$plugin_file" >| "$cache_file"
        print -r -- "$plugin_file"
        return 0
      fi
    done
  fi

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
