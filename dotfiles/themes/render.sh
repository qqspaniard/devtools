#!/bin/sh
# render.sh -- generate native theme files from a base24 color scheme.
#
# The theming model is "native tool theme dirs": for a given scheme we emit one
# theme file per tool, in that tool's OWN theme-discovery directory, in that
# tool's native format. Each tool then switches themes natively -- there is no
# central controller, no "active" file, no current.* indirection.
#
# PALETTE FORMAT: base24 (https://github.com/tinted-theming/home). A scheme is a
# single variant (dark OR light) with a 24-slot semantic palette base00..base17:
#
#   base00 bg        base01 lighter bg   base02 selection bg  base03 comments/muted
#   base04 dark fg   base05 default fg   base06 light fg      base07 lightest fg
#   base08 red       base09 orange       base0A yellow        base0B green
#   base0C cyan      base0D blue         base0E magenta       base0F dark-red/brown
#   base10 darker bg base11 darkest bg   base12-17 bright ANSI (red/yel/grn/cyn/blu/mag)
#
# A scheme file (schemes/<slug>.json) looks like:
#   { "system":"base24", "name":"Rose Pine Moon", "slug":"rose-pine-moon",
#     "variant":"dark", "palette": { "base00":"#232136", ... "base17":"#ea9a97" } }
#
# A light+dark theme is TWO scheme files (e.g. rose-pine-moon [dark] +
# rose-pine-dawn [light]) that the renderer pairs for opencode's single
# dark+light theme file (see the opencode emitter below).
#
# A scheme is resolved as either:
#   * a NAME  -> schemes/<name>.json  (baked-in public schemes), or
#   * a PATH  -> any *.json file       (local / third-party schemes, e.g. a
#                                        brand scheme kept OUTSIDE this repo).
#
# For the resolved scheme it emits:
#   wezterm  -> $WEZTERM_COLORS_DIR/<slug>.toml   (slug already encodes variant)
#   nvim     -> $NVIM_COLORS_DIR/<slug>.lua        (slug already encodes variant)
#   opencode -> $OPENCODE_THEMES_DIR/<dark-slug>.json  (dark+light in one file)
#
# NAMING: each base24 scheme is a single variant, so wezterm/nvim output files
# are named by SLUG with no extra -dark/-light suffix (the slug carries it:
# rose-pine-moon = dark, rose-pine-dawn = light). This matches base24 convention.
#
# OPENCODE PAIRING: opencode themes carry dark AND light together. We pair a dark
# scheme with its light sibling and name the opencode theme by the DARK slug:
#   * if --paired-light <scheme> is given, that scheme provides the light values;
#   * else we look for a sibling scheme whose slug is <base>-dawn or <base>-light
#     (e.g. rose-pine-moon -> rose-pine-dawn; nebula -> nebula-dawn);
#   * else (no sibling) the dark scheme's own values are used for both modes.
# A LIGHT scheme rendered on its own emits an opencode theme named by ITS slug
# using its values for both modes (a standalone light theme). Under --all we
# dedupe so a dark+light pair yields exactly one opencode theme (the dark slug).
#
# (tmux is intentionally NOT emitted: the tmux status bar is styled natively and
# inline in tmux.conf -- it dissolves into the terminal via `bg=default` -- so
# there is no generated tmux theme file. tmux styling is owned by tmux.conf.)
#
# Switching, per tool:
#   wezterm  set config.color_scheme = '<slug>' (e.g. 'rose-pine-moon'); wezterm
#            scans $WEZTERM_COLORS_DIR IF appearance.lua sets color_scheme_dirs.
#   nvim     :colorscheme <slug>
#   opencode set the theme in opencode's config; it follows its own light/dark.
#
# Output dirs default to the tools' real ~/.config locations but are overridable
# via the env vars named above (for testing against scratch dirs). Everything is
# idempotent: re-running overwrites outputs in place.
#
# Usage:
#   render.sh <name>                       render schemes/<name>.json
#   render.sh <path>                       render an arbitrary scheme .json
#   render.sh <name> --paired-light <sch>  pair a light sibling for opencode
#   render.sh --all                        render every scheme in schemes/
#
# POSIX sh; needs `jq`.

set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
SCHEMES_DIR="$SCRIPT_DIR/schemes"

# ---------------------------------------------------------------------------
# Native output dirs (overridable via env for testing against scratch dirs).
# ---------------------------------------------------------------------------
: "${XDG_CONFIG_HOME:=$HOME/.config}"
: "${WEZTERM_COLORS_DIR:=$XDG_CONFIG_HOME/wezterm/colors}"
: "${NVIM_COLORS_DIR:=$XDG_CONFIG_HOME/nvim/colors}"
: "${OPENCODE_THEMES_DIR:=$XDG_CONFIG_HOME/opencode/themes}"

if ! command -v jq >/dev/null 2>&1; then
  printf 'render.sh: error: jq is required but not found on PATH.\n' >&2
  exit 1
fi

# p <scheme-file> <slot>  -> prints palette.<slot> hex (e.g. p file base0D).
p() { jq -r --arg k "$1" '.palette[$k]' "$2"; }

# slug_of <scheme-file>  -> prints the scheme's slug (falls back to filename stem).
slug_of() {
  s=$(jq -r '.slug // empty' "$1")
  [ -n "$s" ] || s=$(basename "$1" .json)
  printf '%s' "$s"
}

# valid_scheme <scheme-file>  -> 0 if it is a base24 scheme with a 24-slot palette.
valid_scheme() {
  jq -e '(.palette|type=="object") and (.palette|has("base00")) and (.palette|has("base17"))' \
    "$1" >/dev/null 2>&1
}

# light_sibling <dark-scheme-file>  -> prints the path to a light sibling scheme
# (<base>-dawn.json or <base>-light.json in the SAME directory as the dark file),
# or nothing if none exists. Used to auto-pair opencode dark+light.
light_sibling() {
  df=$1
  dir=$(dirname "$df")
  dslug=$(slug_of "$df")
  # Derive the base stem: strip a trailing variant token if present, else use as-is.
  case "$dslug" in
    *-moon)  base=${dslug%-moon} ;;
    *-night) base=${dslug%-night} ;;
    *-dark)  base=${dslug%-dark} ;;
    *)       base=$dslug ;;
  esac
  for cand in "$dir/$base-dawn.json" "$dir/$dslug-dawn.json" \
              "$dir/$base-light.json" "$dir/$dslug-light.json"; do
    if [ -f "$cand" ] && valid_scheme "$cand"; then
      printf '%s' "$cand"
      return 0
    fi
  done
  return 1
}

# -----------------------------------------------------------------------------
# wezterm: one TOML color file per scheme (slug-named). Discovered by wezterm
# from color_scheme_dirs (appearance.lua must set it -- the nightly build does
# NOT auto-scan ~/.config/wezterm/colors). The [metadata] name equals the
# filename stem so `config.color_scheme = '<slug>'` selects it.
#
# base24 -> ANSI is base24's native purpose. ANSI index order is
# black,red,green,yellow,blue,magenta,cyan,white:
#   ansi    = base00, base08, base0B, base0A, base0D, base0E, base0C, base05
#   brights = base03, base12, base14, base13, base16, base17, base15, base07
# -----------------------------------------------------------------------------
render_wezterm() {
  sf=$1; slug=$2
  b00=$(p base00 "$sf"); b03=$(p base03 "$sf"); b05=$(p base05 "$sf")
  b07=$(p base07 "$sf"); b02=$(p base02 "$sf"); b0D=$(p base0D "$sf")
  b08=$(p base08 "$sf"); b0B=$(p base0B "$sf"); b0A=$(p base0A "$sf")
  b0E=$(p base0E "$sf"); b0C=$(p base0C "$sf")
  b12=$(p base12 "$sf"); b13=$(p base13 "$sf"); b14=$(p base14 "$sf")
  b15=$(p base15 "$sf"); b16=$(p base16 "$sf"); b17=$(p base17 "$sf")

  mkdir -p "$WEZTERM_COLORS_DIR"
  cat >"$WEZTERM_COLORS_DIR/$slug.toml" <<EOF
# GENERATED by dotfiles/themes/render.sh -- do not edit.
# base24 scheme: $slug
# A wezterm color scheme. wezterm discovers this only if appearance.lua sets
#   config.color_scheme_dirs = { wezterm.home_dir .. '/.config/wezterm/colors' }
# (the nightly build does not auto-scan that dir). Select with:
#   config.color_scheme = '$slug'   (or the built-in scheme picker).
[metadata]
name = "$slug"

[colors]
foreground = "$b05"
background = "$b00"
cursor_bg = "$b0D"
cursor_border = "$b0D"
cursor_fg = "$b00"
selection_bg = "$b02"
selection_fg = "$b05"
ansi    = ["$b00", "$b08", "$b0B", "$b0A", "$b0D", "$b0E", "$b0C", "$b05"]
brights = ["$b03", "$b12", "$b14", "$b13", "$b16", "$b17", "$b15", "$b07"]
EOF
}

# -----------------------------------------------------------------------------
# nvim: a colorscheme file per scheme (slug-named), loadable via
# `:colorscheme <slug>`. Sets termguicolors + colors_name + base highlight groups
# mapped from base24 slots. statusline.lua reads PmenuSel/Directory/Visual after
# the colorscheme loads, so those are defined here. Treesitter @-groups link to
# the base groups for richer highlighting.
# -----------------------------------------------------------------------------
render_nvim() {
  sf=$1; slug=$2
  b00=$(p base00 "$sf"); b01=$(p base01 "$sf"); b02=$(p base02 "$sf")
  b03=$(p base03 "$sf"); b05=$(p base05 "$sf")
  b08=$(p base08 "$sf"); b09=$(p base09 "$sf"); b0A=$(p base0A "$sf")
  b0B=$(p base0B "$sf"); b0C=$(p base0C "$sf"); b0D=$(p base0D "$sf")
  b0E=$(p base0E "$sf")

  mkdir -p "$NVIM_COLORS_DIR"
  cat >"$NVIM_COLORS_DIR/$slug.lua" <<EOF
-- GENERATED by dotfiles/themes/render.sh -- do not edit.
-- base24 scheme: $slug
-- A Neovim colorscheme. Load with :colorscheme $slug. Colors come from the
-- base24 palette slots. statusline.lua reads PmenuSel/Directory/Visual.
vim.o.termguicolors = true
vim.cmd('highlight clear')
if vim.fn.exists('syntax_on') == 1 then
  vim.cmd('syntax reset')
end
vim.g.colors_name = '$slug'
local set = vim.api.nvim_set_hl
-- Base UI
set(0, 'Normal',       { fg = '$b05', bg = '$b00' })
set(0, 'NormalFloat',  { fg = '$b05', bg = '$b01' })
set(0, 'Visual',       { bg = '$b02' })
set(0, 'Pmenu',        { fg = '$b05', bg = '$b01' })
set(0, 'PmenuSel',     { fg = '$b00', bg = '$b0D' })
set(0, 'Directory',    { fg = '$b0D' })
set(0, 'LineNr',       { fg = '$b03' })
set(0, 'CursorLineNr', { fg = '$b0A', bold = true })
set(0, 'StatusLine',   { fg = '$b05', bg = '$b01' })
-- Syntax (legacy groups)
set(0, 'Comment',    { fg = '$b03', italic = true })
set(0, 'Constant',   { fg = '$b09' })
set(0, 'Number',     { fg = '$b09' })
set(0, 'String',     { fg = '$b0B' })
set(0, 'Statement',  { fg = '$b0E' })
set(0, 'Keyword',    { fg = '$b0E' })
set(0, 'Identifier', { fg = '$b08' })
set(0, 'Function',   { fg = '$b0D' })
set(0, 'Type',       { fg = '$b0A' })
set(0, 'Operator',   { fg = '$b05' })
-- Treesitter (@-groups) -> link to the legacy groups above
set(0, '@comment',  { link = 'Comment' })
set(0, '@keyword',  { link = 'Keyword' })
set(0, '@function', { link = 'Function' })
set(0, '@string',   { link = 'String' })
set(0, '@variable', { link = 'Identifier' })
set(0, '@type',     { link = 'Type' })
set(0, '@number',   { link = 'Number' })
set(0, '@constant', { link = 'Constant' })
set(0, '@operator', { link = 'Operator' })
-- Diagnostics
set(0, 'DiagnosticError', { fg = '$b08' })
set(0, 'DiagnosticWarn',  { fg = '$b0A' })
set(0, 'DiagnosticInfo',  { fg = '$b0C' })
set(0, 'DiagnosticHint',  { fg = '$b03' })
EOF
}

# -----------------------------------------------------------------------------
# opencode: one theme JSON carrying BOTH modes, built with jq so hex values are
# injected safely and the output is valid JSON.
#
# LAYERED model (validated live, build 1.18.16): the BASE background INHERITS the
# terminal (value "none"), while panel/element background LAYERS use base01/base02
# hex, and every foreground/accent role is base24 hex. This mirrors the look of
# opencode's built-in `system` theme but with exact palette colors.
#
# CRITICAL: opencode CRASHES on `null` and on the per-role value "system" (tested,
# build 1.18.16). The ONLY inheritance token we emit is the string "none"; every
# other value is inline hex. Never emit null or "system".
#
# Role -> base24 slot mapping (validated in the base24 prototype). For a paired
# theme, the dark mode reads the DARK scheme's slot and the light mode reads the
# LIGHT scheme's same slot. Args: <dark-scheme-file> <light-scheme-file> <name>.
# (Pass the dark file as the light file too for a dark-only / light-only theme.)
# -----------------------------------------------------------------------------
render_opencode() {
  df=$1; lf=$2; name=$3

  mkdir -p "$OPENCODE_THEMES_DIR"
  jq -n \
    --slurpfile d "$df" \
    --slurpfile l "$lf" '
    ($d[0].palette) as $D |
    ($l[0].palette) as $L |
    # r <- a theme role that reads slot $k from dark and light palettes.
    def r($k): { dark: $D[$k], light: $L[$k] };
    # none <- the terminal-inherit sentinel (literal string, never null/system).
    def none: { dark: "none", light: "none" };
    {
      "$schema": "https://opencode.ai/theme.json",
      "theme": {
        "primary":   r("base0D"),
        "secondary": r("base0C"),
        "accent":    r("base0E"),
        "error":     r("base08"),
        "warning":   r("base0A"),
        "success":   r("base0B"),
        "info":      r("base0C"),
        "text":      r("base05"),
        "textMuted": r("base03"),

        "background":        none,
        "backgroundPanel":   r("base01"),
        "backgroundElement": r("base02"),

        "border":       r("base02"),
        "borderActive": r("base0D"),
        "borderSubtle": r("base01"),

        "diffAdded":            r("base0B"),
        "diffRemoved":          r("base08"),
        "diffContext":          r("base03"),
        "diffHunkHeader":       r("base0D"),
        "diffHighlightAdded":   r("base14"),
        "diffHighlightRemoved": r("base12"),
        "diffAddedBg":          r("base10"),
        "diffRemovedBg":        r("base11"),
        "diffContextBg":        none,
        "diffLineNumber":       r("base03"),
        "diffAddedLineNumberBg":   r("base10"),
        "diffRemovedLineNumberBg": r("base11"),

        "markdownText":           r("base05"),
        "markdownHeading":        r("base0D"),
        "markdownLink":           r("base0C"),
        "markdownLinkText":       r("base08"),
        "markdownCode":           r("base0B"),
        "markdownBlockQuote":     r("base0C"),
        "markdownEmph":           r("base0E"),
        "markdownStrong":         r("base0A"),
        "markdownHorizontalRule": r("base03"),
        "markdownListItem":       r("base0D"),
        "markdownListEnumeration":r("base09"),
        "markdownImage":          r("base0C"),
        "markdownImageText":      r("base08"),
        "markdownCodeBlock":      r("base05"),

        "syntaxComment":     r("base03"),
        "syntaxKeyword":     r("base0E"),
        "syntaxFunction":    r("base0D"),
        "syntaxVariable":    r("base08"),
        "syntaxString":      r("base0B"),
        "syntaxNumber":      r("base09"),
        "syntaxType":        r("base0A"),
        "syntaxOperator":    r("base05"),
        "syntaxPunctuation": r("base05")
      }
    }
  ' >"$OPENCODE_THEMES_DIR/$name.json"
}

# render_scheme <scheme-file> [<paired-light-file>]
# Emits wezterm + nvim for THIS scheme (by its slug), and one opencode theme
# (dark+light). The opencode theme is named/paired per the rules in the header:
#   * explicit paired-light wins;
#   * else a light sibling is auto-detected for a dark scheme;
#   * else the scheme's own values are used for both opencode modes.
render_scheme() {
  sf=$1
  paired_light=${2:-}
  if [ ! -f "$sf" ]; then
    printf 'render.sh: error: no scheme file: %s\n' "$sf" >&2
    return 1
  fi
  if ! valid_scheme "$sf"; then
    printf 'render.sh: error: %s is not a base24 scheme (need palette.base00..base17)\n' "$sf" >&2
    return 1
  fi

  slug=$(slug_of "$sf")
  variant=$(jq -r '.variant // "dark"' "$sf")

  # Per-tool native files are always emitted for this scheme (slug-named).
  render_wezterm "$sf" "$slug"
  render_nvim    "$sf" "$slug"

  # opencode pairing.
  if [ -n "$paired_light" ]; then
    if [ ! -f "$paired_light" ] || ! valid_scheme "$paired_light"; then
      printf 'render.sh: error: --paired-light %s is not a valid base24 scheme\n' "$paired_light" >&2
      return 1
    fi
    render_opencode "$sf" "$paired_light" "$slug"
    printf '  rendered %s (opencode paired with %s) -> wezterm/nvim/opencode\n' \
      "$slug" "$(slug_of "$paired_light")"
    return 0
  fi

  if [ "$variant" = "light" ]; then
    # A light scheme on its own: standalone opencode theme, light values both modes.
    render_opencode "$sf" "$sf" "$slug"
    printf '  rendered %s (light) -> wezterm/nvim/opencode\n' "$slug"
    return 0
  fi

  # Dark scheme: try to auto-pair a light sibling for opencode.
  if lsib=$(light_sibling "$sf"); then
    render_opencode "$sf" "$lsib" "$slug"
    printf '  rendered %s (opencode paired with %s) -> wezterm/nvim/opencode\n' \
      "$slug" "$(slug_of "$lsib")"
  else
    render_opencode "$sf" "$sf" "$slug"
    printf '  rendered %s -> wezterm/nvim/opencode\n' "$slug"
  fi
}

# resolve_and_render <arg> [<paired-light-arg>]  -- arg is a NAME or a PATH.
resolve_and_render() {
  arg=$1
  plight=${2:-}
  case "$arg" in
    */* | *.json)
      if [ ! -f "$arg" ]; then
        printf 'render.sh: error: no such scheme file: %s\n' "$arg" >&2
        return 1
      fi
      sf=$arg
      ;;
    *)
      sf="$SCHEMES_DIR/$arg.json"
      if [ ! -f "$sf" ]; then
        printf 'render.sh: error: no baked-in scheme named %s (looked for %s)\n' "$arg" "$sf" >&2
        printf '  available:\n' >&2
        for s in "$SCHEMES_DIR"/*.json; do
          [ -e "$s" ] || continue
          printf '    %s\n' "$(basename "$s" .json)" >&2
        done
        printf '  (or pass a path to a .json base24 scheme file)\n' >&2
        return 1
      fi
      ;;
  esac

  # Resolve an explicit paired-light NAME or PATH the same way.
  if [ -n "$plight" ]; then
    case "$plight" in
      */* | *.json) pf=$plight ;;
      *)            pf="$SCHEMES_DIR/$plight.json" ;;
    esac
    render_scheme "$sf" "$pf"
  else
    render_scheme "$sf"
  fi
}

# render_all -- render every scheme in schemes/, deduping opencode output so a
# dark+light pair yields ONE opencode theme (named by the dark slug). Light
# schemes that ARE the sibling of a rendered dark scheme still emit their own
# wezterm/nvim files, but do not additionally emit a standalone opencode theme.
render_all() {
  # First pass: collect the set of light siblings that will be paired by a dark
  # scheme, so we can skip their standalone opencode emission.
  paired_lights=""
  for sf in "$SCHEMES_DIR"/*.json; do
    [ -e "$sf" ] || continue
    valid_scheme "$sf" || continue
    [ "$(jq -r '.variant // "dark"' "$sf")" = "light" ] && continue
    if lsib=$(light_sibling "$sf"); then
      paired_lights="$paired_lights $lsib"
    fi
  done

  for sf in "$SCHEMES_DIR"/*.json; do
    [ -e "$sf" ] || continue
    if ! valid_scheme "$sf"; then
      printf 'render.sh: warning: skipping non-base24 scheme %s\n' "$sf" >&2
      continue
    fi
    slug=$(slug_of "$sf")
    variant=$(jq -r '.variant // "dark"' "$sf")
    if [ "$variant" = "light" ]; then
      # Is this light scheme already paired by a dark scheme's opencode theme?
      skip_oc=0
      for pl in $paired_lights; do
        [ "$pl" = "$sf" ] && skip_oc=1 && break
      done
      render_wezterm "$sf" "$slug"
      render_nvim    "$sf" "$slug"
      if [ "$skip_oc" -eq 1 ]; then
        printf '  rendered %s (light; opencode paired from its dark partner) -> wezterm/nvim\n' "$slug"
      else
        render_opencode "$sf" "$sf" "$slug"
        printf '  rendered %s (light) -> wezterm/nvim/opencode\n' "$slug"
      fi
    else
      render_scheme "$sf"
    fi
  done
}

main() {
  if [ "$#" -eq 0 ]; then
    printf 'render.sh: error: a scheme name or path is required (or --all).\n' >&2
    printf 'Usage: render.sh <name|path.json> [--paired-light <name|path>] | --all\n' >&2
    exit 2
  fi
  case "$1" in
    --all)
      render_all
      ;;
    -h | --help)
      printf 'Usage: render.sh <name|path.json> [--paired-light <name|path>] | --all\n'
      printf '  <name>                     render schemes/<name>.json (baked-in scheme)\n'
      printf '  <path.json>                render an arbitrary base24 scheme file\n'
      printf '  <name> --paired-light <s>  pair a light sibling for the opencode theme\n'
      printf '  --all                      render every scheme in schemes/\n'
      ;;
    *)
      scheme=$1
      shift
      plight=""
      if [ "$#" -ge 2 ] && [ "$1" = "--paired-light" ]; then
        plight=$2
      elif [ "$#" -ge 1 ] && [ "$1" != "--paired-light" ]; then
        printf 'render.sh: error: unexpected argument: %s\n' "$1" >&2
        exit 2
      elif [ "$#" -eq 1 ] && [ "$1" = "--paired-light" ]; then
        printf 'render.sh: error: --paired-light requires a scheme argument\n' >&2
        exit 2
      fi
      resolve_and_render "$scheme" "$plight"
      ;;
  esac
}

main "$@"
