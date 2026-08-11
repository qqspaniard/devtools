# Dotfiles

Portable, source-controlled development configuration. This directory is the
shared home for terminal, editor, shell, Git, and other user-level tooling as
those configurations are added.

The first included setup pairs [WezTerm](https://wezterm.org) as the GPU
terminal emulator with [tmux](https://github.com/tmux/tmux) as the multiplexer.
It targets macOS, Linux, and WSL, avoids plugins, and installs by copying the
files in this repo into real directories under `~/.config`.

Alongside it is an optional, framework-free **zsh interactive fragment**
(`zsh/interactive.zsh`) that layers native completion, fzf key-bindings, and two
optional zsh plugins onto your existing shell — without taking over your
`.zshrc`. See [zsh interactive fragment](#zsh-interactive-fragment).

## Current scope: WezTerm + tmux

- **Layers stay independent.** WezTerm draws the window; tmux manages panes,
  windows, and the status bar. The installer does **not** auto-launch tmux and
  does **not** force a shell, so you can learn, use, and debug each layer on its
  own.
- **tmux owns the multiplexing UI.** WezTerm's tab bar is disabled so tmux's
  status line is the only one you see. WezTerm keeps its default keybindings;
  it does not bind `Ctrl-Space`, so the tmux prefix reaches tmux naturally.
- **No plugins, no theme files, no TPM.** Everything uses built-in WezTerm and
  tmux features. See [Why no plugins](#why-plugins-are-deferred).
- **Portable and non-destructive.** Paths are derived from the repo location;
  the installer copies files into real directories and never clobbers a file
  you have edited without asking (or backing it up).

## Layout

```
dotfiles/
├── README.md            # this file
├── install.sh           # POSIX-sh copy installer (conflict-aware)
├── wezterm/
│   ├── wezterm.lua      # entry point (config_builder + module composition)
│   ├── appearance.lua   # scheme, font, opacity, macOS blur, tab bar
│   └── keybindings.lua  # intentionally minimal (keeps Ctrl-Space free)
├── tmux/
│   └── tmux.conf        # prefix, splits, navigation, clipboard, status bar
├── nvim/                # portable Neovim config (options, lsp, statusline, …)
├── themes/              # color themes (see "Themes" below)
│   ├── palettes/       # <name>.json — the source of truth (light + dark)
│   └── render.sh       # generates native theme files into each tool's own dir
└── zsh/
    └── interactive.zsh  # optional: completion + fzf + autosuggestions +
                         # syntax-highlighting (you add one source line)
```

## Themes

WezTerm, tmux, Neovim, and opencode share color palettes. The model is **native
tool theme dirs**: `render.sh` transpiles a palette into each tool's *own*
native theme format and drops it in that tool's *own* theme-discovery directory,
so each tool switches themes natively — no central controller, no "active" file,
no indirection. Two public palettes ship, each with a **light** and **dark**
mode:

| Theme | Look |
|-------|------|
| `nebula` | deep-space blue-violet with nebula-cyan / plasma-violet accents |
| `rosepine` | Rose Pine (Moon dark / Dawn light) |

### Generating

```sh
~/.config/themes/render.sh <name>        # a baked-in palette: nebula, rosepine
~/.config/themes/render.sh /path/pal.json # any palette .json (local/third-party)
~/.config/themes/render.sh --all         # every baked-in palette
```

`render.sh` writes native theme files into each tool's own directory:

- WezTerm → `~/.config/wezterm/colors/<name>-<mode>.toml` (auto-scanned; each
  registered by its `[metadata] name`)
- Neovim → `~/.config/nvim/colors/<name>-<mode>.lua` (loadable via
  `:colorscheme <name>-<mode>`)
- opencode → `~/.config/opencode/themes/<name>.json` (one file, both modes)
- tmux → `~/.config/tmux/themes/<name>-<mode>.conf` (status-bar styling)

`install.sh` runs `render.sh --all` after copying, so the public themes exist in
place (needs `jq`).

### Switching

Each tool switches natively — no shared switcher:

- **WezTerm**: set `config.color_scheme = '<name>-<mode>'` in
  `wezterm/appearance.lua`, or use WezTerm's built-in scheme picker.
- **Neovim**: `:colorscheme <name>-<mode>` (the committed default is set in
  `nvim/lua/colorscheme.lua`).
- **opencode**: set the theme in opencode's config; it follows its own
  light/dark setting from the one JSON.
- **tmux**: change the sourced filename in `tmux.conf` (tmux has no native theme
  registry) and reload (`prefix + r`).

The committed default across the tools is `nebula-dark`.

### Palettes

The **palette** (`themes/palettes/<name>.json`) is the single source of truth:
semantic roles (bg, fg, accent, …) plus a 16-color ANSI set and agent-state
colors, defined for both modes. Edit a palette and re-run `render.sh <name>` (or
`--all`) to regenerate. If a tool's generated file is missing (fresh checkout
before `install.sh` runs `render.sh`), each tool falls back to a sane built-in
so nothing breaks.

Local or third-party palettes (e.g. a private brand palette you do NOT want in
this repo) live outside the repo — drop them in `~/.config/themes/palettes/` and
generate with `render.sh ~/.config/themes/palettes/<name>.json`. The installer
renders only the baked-in public palettes, never local ones.

| Tool | Purpose | Notes |
|------|---------|-------|
| WezTerm | terminal emulator | required to use the WezTerm config |
| tmux | multiplexer | 3.0+ recommended; the config is written to work on older versions too |
| Hack Nerd Font | font | optional but recommended; WezTerm falls back to a system font if absent |
| jq | JSON processor | required to (re)generate themes via `render.sh`; without it the tools use built-in color fallbacks |
| zsh | shell | required only for the optional `zsh/interactive.zsh` fragment |
| fzf | fuzzy finder | optional; needs `fzf --zsh` support (fzf ≥ 0.48) for the fragment's key-bindings |
| zsh-autosuggestions | inline suggestions | optional plugin; enables suggestions + Ctrl-f accept |
| zsh-syntax-highlighting | command highlighting | optional plugin; loaded last |

The installer checks for these and prints platform-specific install
suggestions for anything missing. It never installs anything itself.

## Installation

From anywhere:

```sh
sh /path/to/dotfiles/install.sh
```

This **copies** the tracked files into real directories under `~/.config`
(creating the directories, not symlinks):

- `~/.config/wezterm/` ← `<repo>/dotfiles/wezterm/`
- `~/.config/tmux/` ← `<repo>/dotfiles/tmux/` (incl. `scripts/`,
  `themes/spaceflight/`)
- `~/.config/nvim/` ← `<repo>/dotfiles/nvim/` (incl. `lua/`, `lsp/`)
- `~/.config/zsh/` ← `<repo>/dotfiles/zsh/`
- `~/.config/themes/` ← `<repo>/dotfiles/themes/` (`render.sh` + public
  `palettes/`; the generated theme files land in each tool's own dir)
- `~/.config/opencode/plugins/tmux-agent-state.ts` (the opencode-side half of
  the tmux agent-status feature)
- `~/.tmux.conf` (a single-file compat shim for tmux older than 3.1, which
  only reads the classic path)

Copying is deliberate. A directory **symlink** would pin your live config to a
single checkout, so you could not deploy or test from a git worktree, and a
generated theme file written into a tool's config dir would land inside the
repo. With real directories, you can install from any checkout, and tracked
files coexist with generated/untracked ones in the same dirs — the installer
manages only the tracked files.

Copies mirror the source structure, so nested modules (`nvim/lua/`, `nvim/lsp/`,
`tmux/scripts/`) deploy automatically and adding a new tracked file needs no
installer edit. File modes are preserved, so scripts and the opencode plugin
stay executable.

**The installer does NOT edit your `~/.zshrc`.** Copying `~/.config/zsh/` only
puts the fragment in place; you activate it by adding one `source` line yourself
(see [zsh interactive fragment](#zsh-interactive-fragment)). Your `~/.zshrc`
stays entirely yours and untracked.

Because files are **copied**, editing a file back in the repo does not take
effect until you re-run the installer (which will detect the change as a
conflict — see below). Conversely, you can now edit a deployed file in place
to experiment without dirtying the repo.

### Options

```sh
sh install.sh --dry-run     # show what would happen (NEW/UPDATE/OK/CONFLICT); change nothing
sh install.sh --force       # overwrite conflicts without prompting (still backs up first)
sh install.sh --uninstall   # remove ONLY unchanged copies; keep anything you edited
sh install.sh --help        # usage
```

## Conflict handling

Each file is compared to its repo source:

- **NEW** — the destination does not exist; it is created.
- **OK** — the destination is byte-identical to the source; nothing to do. This
  makes re-running **idempotent**: no prompts, no backups, no writes.
- **CONFLICT** — the destination exists but **differs** from the source (you, or
  something else, changed it). The installer never clobbers it silently:
  - **Interactively** (a terminal is available) it prompts per file:
    `[o]verwrite / [s]kip / [d]iff / [a]ll-overwrite / [q]uit`. `d` shows a
    unified diff and re-asks; `a` overwrites this and all remaining conflicts;
    `q` aborts cleanly.
  - **Non-interactively** (piped, cron, CI) it **skips** the file and warns,
    unless you pass `--force`.

Before **any** overwrite, the existing file is backed up to
`<dest>.bak.<epoch>` (timestamped, never clobbering a prior backup). So `--force`
is safe: it overwrites, but your previous version is always recoverable.

## Uninstall

```sh
sh install.sh --uninstall
```

Because the deployed files are **real files you may have edited**, uninstall is
conservative. For each managed file it removes the destination **only if** it is
byte-identical to the current repo source (i.e. unchanged since deploy) —
reported `REMOVED`. A file you have modified is left in place and reported
`KEPT (modified)`. Files that are not in the manifest (the generated theme files
under each tool's own dir, your own files) are **never** touched, and only
directories left empty by removals are pruned — it never `rm -rf`s a config dir.

To remove things manually, delete the specific files or directories yourself;
they are ordinary files, so `rm` behaves as expected.

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
- **tmux** does not reload automatically. Press **`prefix r`** (Ctrl-Space then r);
  you should see `tmux.conf reloaded` in the status line. To reload from a
  shell: `tmux source-file ~/.tmux.conf`.

### True color
tmux advertises true color through `terminal-overrides
",xterm-256color:Tc"`, with `default-terminal "tmux-256color"` **inside**
tmux. WezTerm uses `xterm-256color` by default. The additional `wezterm:Tc`
override supports users who explicitly install WezTerm's terminfo and opt into
`TERM=wezterm`. This uses the widely supported `Tc` capability rather than the
newer `terminal-features` option, so it works on older tmux too. If colors look
wrong:
- Confirm your system has the `tmux-256color` terminfo entry
  (`infocmp tmux-256color >/dev/null`). If not, change `default-terminal` to
  `screen-256color` in `tmux/tmux.conf`.
- Verify inside tmux: `tmux info | grep -i Tc` and run a truecolor test such as
  `printf '\033[38;2;255;100;0mTRUECOLOR\033[0m\n'`.

### Clipboard
Copying in tmux copy mode uses tmux's `set-clipboard on`, which emits **OSC 52**
escape sequences. WezTerm honors OSC 52, so local tmux yanks reach your system
clipboard without a `pbcopy`/`xclip`/`wl-copy` helper. Over SSH, the remote tmux
must also support and enable `set-clipboard` (tmux 2.6+). If copy does not reach
the clipboard:
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

## zsh interactive fragment

`zsh/interactive.zsh` is an **optional**, framework-free fragment that adds
interactive niceties to zsh without owning your shell config. It is a
**sourceable fragment, not a `.zshrc`**: your prompt, aliases, `PATH`,
environment, and anything machine-specific or sensitive stay in your own local,
untracked `~/.zshrc`. This repo tracks only the shared, portable fragment.

### What it does (in order)

1. **Native completion.** Runs `autoload -Uz compinit && compinit`, but only if
   completion is not already initialized in the shell (it checks for the
   `compdef` function). If your framework or a prior line already set completion
   up, it is not redone.
2. **fzf integration.** If `fzf` is on `PATH` and supports `fzf --zsh`, it
   sources that integration for fuzzy history (Ctrl-r) and completion. Older fzf
   without `fzf --zsh` is skipped silently — no startup error.
3. **zsh-autosuggestions** (optional). Loads it if found, and binds **Ctrl-f**
   (`^F`) to `autosuggest-accept` — but only when that widget actually exists.
4. **zsh-syntax-highlighting** (optional). Loaded **last**, which is an upstream
   requirement so it can wrap all other widgets.

Missing optional tools simply mean the corresponding feature is absent. Normal
startup stays silent. The fragment does **not** install packages, git-clone
plugins, change your prompt, set aliases/environment, or load secrets. It also
returns immediately if sourced from a non-interactive shell, so it is safe in
scripts.

### One-time setup

1. Install the fragment (once): `sh dotfiles/install.sh` copies
   `<repo>/dotfiles/zsh/` into `~/.config/zsh/`. **This does not touch
   `~/.zshrc`.**
2. Add exactly **one line** to your own `~/.zshrc` (you do this manually):

   ```sh
   source "${XDG_CONFIG_HOME:-$HOME/.config}/zsh/interactive.zsh"
   ```

3. Reload your shell:

   ```sh
   exec zsh
   ```

### Install the optional packages

**macOS (Homebrew):**

```sh
brew install fzf zsh-autosuggestions zsh-syntax-highlighting
```

**Linux:** package names vary by distro; there is no guarantee a given name
exists on yours. Common examples:

```sh
# Debian/Ubuntu
sudo apt install fzf zsh-autosuggestions zsh-syntax-highlighting
# Arch
sudo pacman -S fzf zsh-autosuggestions zsh-syntax-highlighting
# Fedora
sudo dnf install fzf zsh-autosuggestions zsh-syntax-highlighting
```

Or install upstream manually:
<https://github.com/zsh-users/zsh-autosuggestions> and
<https://github.com/zsh-users/zsh-syntax-highlighting>.

### How the fragment finds the plugins

It does **not** hard-code any Homebrew prefix. For each plugin it looks, in
order, at:

1. An **override directory** you set (the escape hatch for unusual layouts):
   - `ZSH_AUTOSUGGEST_DIR` — dir containing `zsh-autosuggestions.zsh`
   - `ZSH_SYNTAX_HIGHLIGHTING_DIR` — dir containing
     `zsh-syntax-highlighting.zsh`
2. **Homebrew**, via `brew --prefix <formula>` — this resolves correctly on
   Apple Silicon (`/opt/homebrew`), Intel macOS (`/usr/local`), and Linuxbrew
   without assuming a prefix. (Note: `brew --prefix` prints a path even for an
   *uninstalled* formula, so the fragment verifies the file is actually readable
   before sourcing it.)
3. **Common distro locations**, e.g. `/usr/share/zsh-autosuggestions`,
   `/usr/share/zsh/plugins/zsh-autosuggestions`, and the
   `zsh-syntax-highlighting` equivalents.

If none match, the plugin is skipped and startup stays silent.

### Load order

`zsh-syntax-highlighting` **must be sourced last**, after every other
widget-defining plugin, or it will not highlight correctly. The fragment
enforces this ordering internally: completion → fzf → autosuggestions →
**syntax highlighting (last)**. Keep the single `source ".../interactive.zsh"`
line at or near the end of your `~/.zshrc`, after anything else that defines zle
widgets.

### If you already source fzf yourself

If your `~/.zshrc` already contains `source <(fzf --zsh)` (or the legacy
`~/.fzf.zsh`), **remove or comment that line** before sourcing this fragment, so
fzf is not initialized twice. The fragment makes a best effort to detect an
already-loaded fzf (it checks for fzf's `fzf-history-widget`) and skip
re-sourcing, but removing the duplicate line is the reliable fix.

### Verifying it works

- Reload: `exec zsh`.
- Type a previously used command; if `zsh-autosuggestions` is installed you
  should see a dimmed inline suggestion. Press **Ctrl-f** to accept it.
- Type a command name; with `zsh-syntax-highlighting` installed, valid commands
  are colored differently from unknown ones.
- Press **Ctrl-r** for fzf history search (if fzf is installed).
- To see exactly what loaded and what was skipped, turn on debug output:

  ```sh
  ZSH_INTERACTIVE_DEBUG=1 exec zsh
  ```

  This prints advisory details for sections that run; normal startup is silent.

### Why your `~/.zshrc` stays untracked

Your personal `~/.zshrc` typically holds machine-specific paths and sensitive
settings that must **not** live in a shared repo. This design deliberately keeps
that file yours: the repo tracks only the portable, non-sensitive
`interactive.zsh` fragment, and you opt in with a single `source` line. The
installer never reads, edits, or copies your `~/.zshrc`.

## Configuration summary

**WezTerm** (`wezterm/`)
- `wezterm.config_builder()`
- Built-in `rose-pine-moon` color scheme (+ a narrow selection-color fix)
- `Hack Nerd Font`, size 15
- Window opacity `0.9`; macOS-only background blur `30`
- Tab bar disabled (tmux is the multiplexer UI)
- Default keybindings preserved; `Ctrl-Space` reaches tmux

**tmux** (`tmux/tmux.conf`)
- `Ctrl-Space` prefix
- `default-terminal tmux-256color` + `Tc` true-color overrides for WezTerm
- Mouse on; vi copy mode; OSC 52 system clipboard
- `base-index 1`, `pane-base-index 1`, `escape-time 10`, `history-limit 10000`
- Splits inherit the current path: `|` side-by-side, `-` top/bottom
- `h/j/k/l` navigate panes; `H/J/K/L` resize; `r` reloads with confirmation
- Rose Pine Moon-inspired, text-first status bar (session · windows · host ·
  time), readable even without Nerd Font glyphs

**zsh** (`zsh/interactive.zsh`, optional — you add one `source` line)
- Native completion via `compinit`, skipped if already initialized
- `fzf --zsh` key-bindings/completion when fzf supports it (else skipped)
- `zsh-autosuggestions` if found; **Ctrl-f** accepts a suggestion
- `zsh-syntax-highlighting` loaded **last** (upstream ordering requirement)
- No prompt, aliases, env, secrets, or plugin manager; silent when optional
  tools are absent; `ZSH_INTERACTIVE_DEBUG=1` for advisory output

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
# press:  Ctrl-Space  then  d  to detach (session keeps running)
tmux attach          # come back to it later
tmux ls              # list running sessions
tmux new -s work     # start a named session
tmux attach -t work  # attach to a named session
```

### Cheat sheet

The prefix is **`Ctrl-Space`** (written `prefix` below). Press the prefix, release,
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
  works identically the moment the files are installed, offline, on a fresh box.
- **Debuggability.** With only built-in features, unexpected behavior has a
  small, inspectable surface — you learn the tools, not a plugin stack.
- **Stability.** Nothing here can break from an upstream plugin change.

Plugins can be layered on later once the base is understood; the modular
WezTerm layout (`appearance.lua`, `keybindings.lua`) leaves clear seams for
extension without rewrites.
