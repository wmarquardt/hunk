<div align="center">

<img src=".github/banner.svg" alt="hunk" width="440">

**Your AI's code might be ugly. The diff won't be.**

</div>

A standalone, themeable diff viewer for the terminal. Side-by-side, word-level
highlighting, keyboard-driven — and when you run it inside a git repository, it
turns into a live review pass you can stage from, hunk by hunk, or a read-only
walk back through the commit history.

Built for the diffs coding agents produce: large, spread across many files, and
miserable to read as raw `git diff` output.

<img width="1600" height="1019" alt="image" src="https://github.com/user-attachments/assets/6c94dcc1-c9ce-4dbe-8c36-a2205bab1f8f" />


## Install

```sh
curl -fsSL https://raw.githubusercontent.com/wmarquardt/hunk/main/install.sh | sh
```

That drops the binary in `~/.local/bin`. Set `HUNK_INSTALL_DIR` to put it
somewhere else, or `HUNK_VERSION` to pin a release.

With Go:

```sh
go install github.com/wmarquardt/hunk@latest
```

Or build from a clone:

```sh
git clone https://github.com/wmarquardt/hunk
cd hunk
go build -o hunk .
```

Single static binary, no runtime dependencies. Git is only needed for the
working-tree review and `hunk log` modes; reading a diff and diffing files or
directories work without it.

## Usage

```sh
hunk                     # review the working tree, and stage what you approve
hunk log                 # walk back through the commit history, read only
hunk log internal/ui     # only the commits that touch those paths
git diff | hunk          # read a diff from stdin
git show <sha> | hunk    # or any other diff-producing command
hunk old.txt new.txt     # diff two files, no git required
hunk old/ new/           # diff two directories
hunk version             # print the version (same as --version)
```

Output that is piped or redirected is passed through as plain unified diff, so
`hunk a b > patch.diff` does what you would expect.

### Options

Every flag picks the state hunk opens in; the matching key still toggles it
during the session.

| Flag | Key | Does |
|---|---|---|
| `--theme <ref>` | | theme name, path, or `github.com/user/repo/name` (also `HUNK_THEME`) |
| `--theme-update` | | re-download remote themes instead of using the cache |
| `-u`, `--unified` | `s` | open unified instead of side-by-side |
| `--no-sidebar` | `b` | open with the file sidebar hidden |
| `--sidebar-width <n>` | `shift+←` / `shift+→` | sidebar width in columns, 20–48 (default 28, 36 in `hunk log`) |
| `--show-whitespace` | `W` | render tabs and trailing spaces as visible marks |
| `--no-syntax` | `H` | open with syntax highlighting off |
| `--filter <regex>` | `F` | hide hunks whose every changed line matches |
| `-w`, `--ignore-whitespace` | `i` | hide whitespace-only changes (git modes) |
| `-U`, `--context <n>` | `+` / `-` | unchanged lines around each hunk (default 3) |
| `--no-follow` | `f` | open with live-follow paused (review mode) |
| `-n`, `--max-count <n>` | | commits to read, `hunk log` only (default 50) |
| `--version` | | print the version and exit |

### Keys

| Key | Action |
|---|---|
| `j` / `k`, ↑ / ↓ | scroll a line, or move in the focused sidebar panel |
| `ctrl-d` / `ctrl-u` | scroll half a page |
| `n` / `p` | next / previous hunk |
| `]` / `[` | next / previous file |
| `g` / `G` | top / bottom |
| `/` | search the diff; `n` / `N` repeat, `esc` clears |
| `W` | show / hide whitespace (tabs, trailing spaces) |
| `H` | toggle syntax highlighting |
| `F` | filter out hunks matching a regex; empty clears |
| `h` / `l`, ← / → | scroll sideways |
| `s` | toggle side-by-side / unified |
| `b` | toggle the file sidebar |
| `shift+←` / `shift+→` | narrow / widen the sidebar |
| `ctrl+w` | move focus between the diff and the sidebar |
| `space`, `-` / `+` | on a sidebar folder: toggle, fold / unfold it |
| `?` | help |
| `q` | quit |
| mouse | click a file, a row, or a status-bar option; the wheel scrolls |

In `hunk log` you also get `}` / `{` to move between commits. In working-tree
review mode you get:

| Key | Action |
|---|---|
| `space` | mark this hunk and move to the next one in the file |
| `a` / `d` | mark / unmark every hunk in this file, or every file in a selected folder |
| `w` | stage what is marked |
| `u` | undo the last stage (skips a stuck entry if the index changed underneath) |
| `E` | edit the file in your editor, at the cursor's line |
| `f` | pause / resume following the working tree |
| `i` | ignore / show whitespace-only changes |
| `+` / `-` | more / less context around each hunk |

`E` suspends hunk and opens the file in `$VISUAL`, falling back to `$EDITOR`, at
the line under the cursor. Quit the editor and hunk comes back with the diff
re-read and your marks and place kept. hunk recognizes the line-jump syntax for
Helix, VS Code, Sublime Text and Zed; other editors receive `+line file`. With
both variables unset, hunk says so instead of guessing. When the editor exits,
hunk names editor failures, hints when a GUI editor may need `--wait`, and
confirms which file was reloaded after a successful edit.

Marking is `git add -p` without the one-hunk-at-a-time straitjacket: see the
whole change, jump around, mark as you go, then write it all at once.

The sidebar is the changed files as a directory tree, directories first then
files, each group alphabetical — the same order `]` / `[` walk. A selected
name that does not fit the row scrolls slowly like a marquee (a pause at
each end, one column at a time); every other truncated name still ends in
an ellipsis. `ctrl+w` moves focus to it (its rule lights up),
after which `j` / `k` and ↑ / ↓ move line by line, folders included; every other
key still works on the diff, and `ctrl+w` again or a click in the diff hands
focus back. A folder under the cursor gets a muted bar instead of the accent —
it has no diff of its own — and a `-` or `+`: `space` toggles it, `-` folds it,
`+` unfolds it. `a` / `d` on a folder mark and unmark every file under it, the
same keys as on a file, one level up. A folded folder keeps its `+`, and stands
in for any hidden file
that `]` / `[` land on. `]` / `[` and clicks only ever pick files. Folders turn
green once every file under them is approved — all of its hunks marked, or
fully staged, or staged with the rest marked.

Each file line says where it stands: `·` untouched, `◐` some hunks marked,
`●` all of them, `✓` already staged (gray when only part of it is). Untracked
files are shown as all-additions and staged whole, and a file you stage fully
stays on screen with its check instead of vanishing mid-review.

### History

`hunk log` is `git log -p` you can walk around in. The sidebar splits in two —
the commits on top, the files of the selected commit below — so the keys nest
the way the history does. The selected commit subject and the selected file
name scroll the same way they do in review mode when they do not fit:

| Key | Action |
|---|---|
| `}` / `{` | older / newer commit |
| `]` / `[` | next / previous file in that commit |
| `n` / `p` | next / previous hunk in that file |
| `ctrl+w` | focus the diff, then the commits, then the files |

With the commits or the files focused (their header lights up), `j` / `k` and
↑ / ↓ move through that list.

```sh
hunk log                 # the last 50 commits
hunk log -n 200          # more of them
hunk log README.md       # only the commits that touch a path
```

Everything that only looks at the diff still works — search, the regex filter,
whitespace, syntax highlighting, side-by-side, `+` / `-` context, `i`. Nothing
that writes does: marking, staging, undo and follow are not bound at all, and
`hunk log` has no code path that can reach the index.

Merge commits show their diff against the first parent, so a merge is not a
blank screen.

Every run of changed lines is wrapped in a rounded outline that crosses from one
pane into the other, with an arrow on the seam pointing the way the change goes.
Each pane closes on its own last changed line and the rule turns down into the
taller pane's wall, so one line becoming four draws one shape narrowing rather
than two boxes side by side — the outline itself shows the shape of the change.
Inside a line, the words that actually changed are painted a shade stronger.

hunk follows the working tree while it is open: edits made by you, your editor,
or an agent show up on their own, and marks on untouched hunks survive the
reload. `f` pauses and resumes following. Because of that, opening hunk in a
clean repository is fine — it waits, and fills in with the first change.

Staging runs `git apply --cached`, so it **only ever writes the index** — no
working-tree file is created, modified, or deleted by hunk. If you stage
something you did not mean to, `git restore --staged .` puts it back.

The side-by-side layout falls back to unified on narrow terminals, and the
sidebar hides itself when there is no room for it.

## Theming

hunk ships with two themes: `hunk-dark` (the default) and `paper` (light).

```sh
hunk --theme paper
HUNK_THEME=paper hunk
```

### Writing your own

Themes are TOML files in `~/.config/hunk/themes/` (`$XDG_CONFIG_HOME` is
honoured). A file named `midnight.toml` there is available as `--theme midnight`.

**Every key is optional.** Anything you leave out keeps the default theme's
value, so a two-line theme is a perfectly good theme. One key does a lot of
work: `ui.accent` is every highlight hunk draws — the `@@` line, the current
hunk, a change block's outline and its arrow, the status bar, the selected file
— so recoloring the highlight is a one-line edit:

```toml
# ~/.config/hunk/themes/midnight.toml
name = "midnight"

[ui]
accent = "#7aa2f7"

[diff]
added_fg   = "#7ee787"
removed_fg = "#ff7b72"
```

The full schema, with every key and what it colors, is in
[`internal/theme/builtin/hunk-dark.toml`](internal/theme/builtin/hunk-dark.toml) —
copy it and edit. Values are hex colors (`#rgb` or `#rrggbb`). A typo'd key gets
a warning, a bad color gets an error naming the key, and neither costs you your
diff: hunk falls back to the default theme and carries on.

### Themes from GitHub

Reference a theme the way Go references a module:

```sh
hunk --theme github.com/someone/hunk-themes/dracula
hunk --theme github.com/someone/hunk-themes/dracula@v1.2.0   # pin it
hunk --theme-update                                          # re-fetch
```

It resolves to `themes/<name>.toml` (or `<name>.toml`) in that repo, and is
cached under `~/.config/hunk/themes/github.com/…`. A cache hit never touches the
network, so a theme is downloaded once and hunk keeps working offline.

To publish your own, put `.toml` files in a `themes/` directory in any public
GitHub repo. That is the whole protocol.

## Contributing

Bug reports, themes, and patches all welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT © Will Marquardt. See [LICENSE](LICENSE).
