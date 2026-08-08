# glyphs.sh -- the SPACEFLIGHT theme's glyph renderer.
#
# This is the swappable "fun" half of the system. The mechanism (window-name
# script) owns WHAT state each agent is in and computes a frame counter from the
# clock; this file owns WHAT EACH STATE LOOKS LIKE.
#
# Contract with the mechanism (do not break these):
#   - source palette.sh (same dir) for COLOR_* names.
#   - define render_glyph <state> <frame> -> prints ONE tmux-styled glyph.
#   - declare the THEME_* metadata below so the mechanism can size/pace the bar.
#
# STEP 1 = STATIC PLACEHOLDERS. Each state renders a single fixed Nerd Font
# glyph, ignoring <frame>. Animation lands in step 3 via the glyph lab; only the
# frame-sequence arrays below change then -- the contract stays identical.

_theme_dir="${BASH_SOURCE[0]%/*}"
# shellcheck source=./palette.sh
. "${_theme_dir}/palette.sh"

# --- Theme metadata (read by the mechanism) --------------------------------
# Milliseconds per animation frame. Step 1 is static, so this is unused until
# step 3; declared now so the contract is stable.
THEME_FRAME_MS=200
# Display cells each glyph occupies. Nerd Font state glyphs are single-width.
# Keep every state the same width to avoid tab jitter as states change.
THEME_GLYPH_WIDTH=1
# When 1, the mechanism may stop redrawing a 'done' agent after it settles
# (done is a terminal, non-animated state once landed). Step 1: already static.
THEME_DONE_STATIC=1
export THEME_FRAME_MS THEME_GLYPH_WIDTH THEME_DONE_STATIC

# --- Frame sequences (Nerd Font, Hack) -------------------------------------
# Step 1: one frame each. Step 3 replaces these with multi-frame animations.
#   working     nf-md-rocket_launch     (climbing rocket)
#   needs-input nf-md-satellite_uplink  (hailing / awaiting you)
#   done        nf-md-flag_checkered / flag  (planted, mission complete)
_FRAMES_WORKING=("󱓞")       # U+F14DE rocket-launch
_FRAMES_NEEDS_INPUT=("󱄙")   # U+F1119 satellite-uplink
_FRAMES_DONE=("")          # U+F00C check (done; placeholder -- lander later)
_FRAMES_IDLE=("")          # U+F0F6C star (quiet) -- rarely rendered

# render_glyph <state> <frame>
#   state ∈ working|needs-input|done|idle|(anything else -> nothing)
#   frame  = integer >= 0 (frame counter from the mechanism's clock)
# Prints a single tmux-#[fg=...] styled glyph, no trailing newline.
render_glyph() {
  local state="$1" frame="${2:-0}" color glyph
  local -a frames
  case "$state" in
    working)     frames=("${_FRAMES_WORKING[@]}");     color="$COLOR_WORKING" ;;
    needs-input) frames=("${_FRAMES_NEEDS_INPUT[@]}");  color="$COLOR_NEEDS_INPUT" ;;
    done)        frames=("${_FRAMES_DONE[@]}");         color="$COLOR_DONE" ;;
    idle)        frames=("${_FRAMES_IDLE[@]}");         color="$COLOR_IDLE" ;;
    *)           return 0 ;;
  esac
  local n="${#frames[@]}"
  (( n == 0 )) && return 0
  glyph="${frames[$(( frame % n ))]}"
  printf '#[fg=%s]%s#[default]' "$color" "$glyph"
}
