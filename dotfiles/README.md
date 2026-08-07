# Dotfiles

Portable, source-controlled development configuration. This directory is the
shared home for terminal, editor, shell, Git, and other user-level tooling as
those configurations are added.

The first included setup pairs [WezTerm](https://wezterm.org) as the GPU
terminal emulator with [tmux](https://github.com/tmux/tmux) as the multiplexer.
It targets macOS, Linux, and WSL, avoids plugins, and installs by symlinking the
files in this repo into place.

## Current scope: WezTerm + tmux

- **Layers stay independent.** WezTerm draws the window; tmux manages panes,
  windows, and the status bar. The installer does **not** auto-launch tmux and
  does **not** force a shell, so you can learn, use, and debug each layer on its
  own.
- **tmux owns the multiplexing UI.** WezTerm's tab bar is disabled so tmux's
  status line is the only one you see. WezTerm keeps its default keybindings;
  it does not bind `Ctrl-b`, so the tmux prefix reaches tmux naturally.
- **No plugins, no theme files, no TPM.** Everything uses built-in WezTerm and
  tmux features. See [Why no plugins](#why-plugins-are-deferred).
- **Portable and non-destructive.** Paths are derived from the repo location;
  the installer refuses to clobber existing files.

## Layout

```
dotfiles/
├── README.md            # this file
├── install.sh           # POSIX-sh symlink installer
├── wezterm/
│   ├── wezterm.lua      # entry point (config_builder + module composition)
│   ├── appearance.lua   # scheme, font, opacity, macOS blur, tab bar
│   └── keybindings.lua  # intentionally minimal (keeps tmux's Ctrl-b free)
└── tmux/
    └── tmux.conf        # prefix, splits, navigation, clipboard, status bar
```

## Prerequisites

| Tool | Purpose | Notes |
|------|---------|-------|
| WezTerm | terminal emulator | required to use the WezTerm config |
| tmux | multiplexer | 3.0+ recommended; the config is written to work on older versions too |
| Hack Nerd Font | font | optional but recommended; WezTerm falls back to a system font if absent |

The installer checks for these and prints platform-specific install
suggestions for anything missing. It never installs anything itself.

## Installation

From anywhere:

```sh
sh /path/to/dotfiles/install.sh
```

This creates two symlinks:

- `~/.config/wezterm` → `<repo>/dotfiles/wezterm` (the **whole directory**)
- `~/.tmux.conf` → `<repo>/dotfiles/tmux/tmux.conf`

**Why link the whole `wezterm` directory?** `wezterm.lua` is modular and
`require`s its siblings (`appearance`, `keybindings`). Linking the entire
directory in one atomic symlink guarantees those modules always resolve, in
every install layout, and keeps a single link to reason about.

**Why `~/.tmux.conf` and not `~/.config/tmux/tmux.conf`?** The classic path is
understood by every tmux version, including releases older than 3.1 that do not
read the XDG path. This maximizes portability.

Because the links point back into the repo, editing files here takes effect
immediately (restart WezTerm or reload tmux with `prefix r`).

### Options

```sh
sh install.sh --dry-run     # show what would happen; change nothing
sh install.sh --uninstall   # remove ONLY the symlinks this installer created
sh install.sh --help        # usage
```

## Safe collision behavior

The installer will **never** overwrite, back up, move, or delete your existing
data. If a destination is already occupied by:

- a **regular file** (e.g. an existing `~/.tmux.conf`),
- a **directory** (e.g. an existing `~/.config/wezterm`), or
- an **unrelated symlink** (pointing somewhere other than this repo),

it prints a `WARN` with an exact remediation command and exits non-zero,
leaving the destination untouched. Re-running after you resolve the collision
is safe.

If a link already points at the correct target, the installer reports `SKIP` —
it is **idempotent**.

## Uninstall / manual unlink

```sh
sh install.sh --uninstall
```

This removes a destination **only if** it is a symlink pointing back into this
repo. Anything else is left alone.

To unlink manually:

```sh
rm ~/.config/wezterm     # only if it is the symlink created above
rm ~/.tmux.conf          # only if it is the symlink created above
```

(`rm` on a symlink removes the link, not the repo files it points to.)

## Platform notes

### macOS
- Suggested font install: `brew install --cask font-hack-nerd-font`.
- Background blur (`macos_window_background_blur = 30`) is enabled here only;
  it is detected via `wezterm.target_triple`.

### Linux
- Install WezTerm per the [Linux instructions](https://wezterm.org/install/linux.html)
  for your distro.
- Nerd Font package names vary by distro; the reliable path is to download from
  <https://www.nerdfonts.com/font-downloads> and refresh the font cache
  (`fc-cache -f`).
- Background blur is not applied (it is macOS-specific).

### WSL (Windows Subsystem for Linux)
- Run WezTerm on the **Windows** side; run tmux inside your **WSL** distro.
- Install the font on Windows (WezTerm reads Windows fonts).
- Everything in this config works in WSL because tmux runs in the Linux
  environment.

### Native Windows limitation
- **tmux does not run natively on Windows.** There is no supported native
  Windows tmux; use **WSL** for the tmux half. The WezTerm config itself is
  cross-platform, but the tmux config is only useful inside WSL/Linux/macOS.

## Verifying WezTerm config and font

- **Check the config parses** (no GUI needed):
  ```sh
  wezterm --config-file ~/.config/wezterm/wezterm.lua ls-fonts >/dev/null
  ```
  A clean exit means the Lua config built successfully.
- **Check the font is found:**
  ```sh
  wezterm ls-fonts --list-system | grep -i "hack nerd font"
  ```
  If there is no match, install the font (above). WezTerm will otherwise fall
  back to a system monospace font and its own bundled symbol font, so the
  terminal still works — glyphs may just differ.
- **See which font WezTerm actually resolved:**
  ```sh
  wezterm ls-fonts
  ```
- **Config file WezTerm is using / debugging:** launch `wezterm` and open the
  debug overlay with the default binding **Ctrl-Shift-L**; it shows a Lua REPL
  and logs, useful for inspecting `wezterm.config_dir`, errors, and warnings.

## Troubleshooting

### Syntax / reload
- **WezTerm** re-reads its config automatically on save. If it looks like a
  change did not apply, run the `--config-file … ls-fonts` check above to see
  the parse error, or open the debug overlay (Ctrl-Shift-L).
- **tmux** does not reload automatically. Press **`prefix r`** (Ctrl-b then r);
  you should see `tmux.conf reloaded` in the status line. To reload from a
  shell: `tmux source-file ~/.tmux.conf`.

### True color
tmux advertises true color to WezTerm via
`terminal-overrides ",xterm-256color:Tc"` and `",wezterm:Tc"`, with
`default-terminal "tmux-256color"` **inside** tmux. This uses the widely
supported `Tc` capability rather than the newer `terminal-features` option, so
it works on older tmux too. If colors look wrong:
- Confirm your system has the `tmux-256color` terminfo entry
  (`infocmp tmux-256color >/dev/null`). If not, change `default-terminal` to
  `screen-256color` in `tmux/tmux.conf`.
- Verify inside tmux: `tmux info | grep -i Tc` and run a truecolor test such as
  `printf '\033[38;2;255;100;0mTRUECOLOR\033[0m\n'`.

### Clipboard
Copying in tmux copy mode uses tmux's `set-clipboard on`, which emits **OSC 52**
escape sequences. WezTerm honors OSC 52, so yanks reach your **local** system
clipboard even over SSH — no `pbcopy`/`xclip`/`wl-copy` helper required. If
copy does not reach the clipboard:
- Ensure you are copying **inside** tmux copy mode (`prefix [`, select with
  `v`, yank with `y`).
- Confirm `set-clipboard` is `on` (`tmux show -g set-clipboard`).
- Very large selections may exceed OSC 52 size limits in some setups; copy less
  or use a mouse selection with WezTerm's own copy.

### Selection highlight is invisible
The built-in `rose-pine-moon` scheme in some WezTerm versions ships a selection
background equal to the window background, making selections hard to see.
`appearance.lua` overrides just the selection colors to fix this. If a future
WezTerm corrects the built-in scheme, that override can be removed.

## Configuration summary

**WezTerm** (`wezterm/`)
- `wezterm.config_builder()`
- Built-in `rose-pine-moon` color scheme (+ a narrow selection-color fix)
- `Hack Nerd Font`, size 15
- Window opacity `0.9`; macOS-only background blur `30`
- Tab bar disabled (tmux is the multiplexer UI)
- Default keybindings preserved; `Ctrl-b` reaches tmux

**tmux** (`tmux/tmux.conf`)
- Standard `Ctrl-b` prefix
- `default-terminal tmux-256color` + `Tc` true-color overrides for WezTerm
- Mouse on; vi copy mode; OSC 52 system clipboard
- `base-index 1`, `pane-base-index 1`, `escape-time 10`, `history-limit 10000`
- Splits inherit the current path: `|` side-by-side, `-` top/bottom
- `h/j/k/l` navigate panes; `H/J/K/L` resize; `r` reloads with confirmation
- Rose Pine Moon-inspired, text-first status bar (session · windows · host ·
  time), readable even without Nerd Font glyphs

---

## tmux learning ladder

Work down this list; each rung is useful on its own.

1. **Start & attach.** `tmux` starts a session. Detach with `prefix d`.
   Re-attach later with `tmux attach`.
2. **Panes.** Split with `prefix |` and `prefix -`. Move with
   `prefix h/j/k/l`. Resize with `prefix H/J/K/L`.
3. **Windows.** `prefix c` new window, `prefix n`/`prefix p` next/previous,
   `prefix <number>` jump to a window.
4. **Copy mode.** `prefix [` to enter, move with vi keys, `v` to start
   selection, `y` to yank (goes to your system clipboard), `q` to leave.
5. **Sessions.** Name and juggle multiple sessions; detach/attach to keep work
   running after you close the terminal or disconnect from SSH.
6. **Command mode & reload.** `prefix :` opens the tmux command prompt;
   `prefix r` reloads this config.

### First-session commands

tmux is **not** launched automatically. Open WezTerm, then:

```sh
tmux                 # start your first session
# ... work, split panes, etc. ...
# press:  Ctrl-b  then  d      to detach (session keeps running)
tmux attach          # come back to it later
tmux ls              # list running sessions
tmux new -s work     # start a named session
tmux attach -t work  # attach to a named session
```

### Cheat sheet

The prefix is **`Ctrl-b`** (written `prefix` below). Press the prefix, release,
then the key.

**Sessions**
| Keys / command | Action |
|----------------|--------|
| `tmux` / `tmux new -s NAME` | start a session (optionally named) |
| `prefix d` | detach (leave it running) |
| `tmux attach` / `tmux attach -t NAME` | re-attach |
| `tmux ls` | list sessions |
| `prefix s` | interactive session switcher |
| `prefix $` | rename current session |

**Windows**
| Keys | Action |
|------|--------|
| `prefix c` | create window |
| `prefix ,` | rename window |
| `prefix n` / `prefix p` | next / previous window |
| `prefix <number>` | jump to window N |
| `prefix w` | window/session tree |
| `prefix &` | kill window |

**Panes**
| Keys | Action |
|------|--------|
| `prefix \|` | split side-by-side (horizontal) |
| `prefix -` | split top/bottom (vertical) |
| `prefix h/j/k/l` | move between panes |
| `prefix H/J/K/L` | resize pane |
| `prefix z` | zoom/unzoom pane |
| `prefix x` | kill pane |

**Copy mode**
| Keys | Action |
|------|--------|
| `prefix [` | enter copy mode |
| `v` | begin selection |
| `y` / `Enter` | copy selection (to system clipboard) |
| `q` | exit copy mode |

**Command / config**
| Keys | Action |
|------|--------|
| `prefix :` | tmux command prompt |
| `prefix r` | reload this config (shows a confirmation) |
| `prefix ?` | list all key bindings |

---

## Why plugins are deferred

This first pass intentionally ships **no plugins** (no TPM, no external theme
files, no focus-dimming machinery):

- **Portability & reproducibility.** Zero external downloads means the setup
  works identically the moment the symlinks exist, offline, on a fresh box.
- **Debuggability.** With only built-in features, unexpected behavior has a
  small, inspectable surface — you learn the tools, not a plugin stack.
- **Stability.** Nothing here can break from an upstream plugin change.

Plugins can be layered on later once the base is understood; the modular
WezTerm layout (`appearance.lua`, `keybindings.lua`) leaves clear seams for
extension without rewrites.
