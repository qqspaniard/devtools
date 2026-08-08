# palette.sh -- semantic color names for the tmux agent-status theme.
#
# Plain shell file, `source`d by the glyph renderer and any script that needs a
# color. Maps SEMANTIC roles (agent states, chrome) to concrete hex values.
# Theme/glyph logic references these names, never raw hex, so a palette swap
# touches only this file.
#
# Values below are Rose Pine Moon (matching tmux.conf). When the unified theming
# system lands, this becomes a GENERATED adapter emitted from a canonical
# palette.json -- the names stay identical, so nothing sourcing this changes.
#
#   base #232136  surface #2a273f  overlay #393552
#   text #e0def4  muted   #6e6a86  subtle  #908caa
#   iris #c4a7e7  foam    #9ccfd8  gold    #f6c177
#   love #eb6f92  pine    #3e8fb0  rose    #ea9a97

# Agent-state colors (consumed by glyphs.sh render_glyph).
COLOR_WORKING="#9ccfd8"      # foam  -- calm motion, "in flight"
COLOR_NEEDS_INPUT="#f6c177"  # gold  -- attention, "hailing Houston"
COLOR_DONE="#eb6f92"         # love  -- landed, come look (deliberately loud)
COLOR_IDLE="#6e6a86"         # muted -- quiet / non-agent

export COLOR_WORKING COLOR_NEEDS_INPUT COLOR_DONE COLOR_IDLE
