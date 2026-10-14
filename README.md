# inks

A fast, keyboard-driven text editor for code and prose. inks is a native macOS app written in Go. It draws its own UI with [Gio](https://gioui.org), so there's no web view; the app is about 18 MB.

## Install

Download `inks_<version>_arm64.dmg` from the [releases](https://gitea.tinks.us/inkstom/inks-go/releases), open it and drag **inks** to Applications.

inks is signed ad hoc, not by an Apple developer account, so the first launch is blocked. Right-click inks in Applications and choose **Open**, or allow it in **System Settings › Privacy & Security**. To skip the check for a copy you built or trust:

```bash
xattr -dr com.apple.quarantine /Applications/inks.app
```

To open files from a terminal, link the launcher onto your PATH:

```bash
ln -s "$PWD/bin/inks" /usr/local/bin/inks
inks main.tf modules/       # files open in tabs, folders in the side bar
```

## Build from source

Requires macOS 12 or later, Go 1.27+, and the Xcode Command Line Tools (`xcode-select --install`). Python 3 is only needed for plugins.

```bash
make            # build/inks
make run        # build and run
make test       # go vet + unit tests
make selftest   # end-to-end checks in a real window
make app        # build/inks.app
make dmg        # dist/inks_<version>_<arch>.dmg
```

## Essentials

| Keys | Does |
| --- | --- |
| ⌘P | Goto Anything: fuzzy-open files. Add `@` for symbols, `:` for a line, `#` for a word (`main.tf:40`, `app@render`) |
| ⇧⌘P | Command Palette: every command, by name |
| ⌘D / ⌃⌘G | Add the next occurrence / every occurrence as a cursor (⌘K ⌘D skips one) |
| ⇧⌘L | Split a selection into one cursor per line |
| ⌥-drag, ⌘-click | Column selection, extra cursor |
| ⌘L / ⇧⌘D / ⌃⇧K | Select line / duplicate line / delete line |
| ⌃⌘↑ ⌃⌘↓ | Move lines up / down |
| ⌘/ | Toggle comment |
| ⇧⌘N / ⇧⌘W | New window / close window (asks about unsaved files) |
| ⌥⌘2 / ⌥⌘3 / ⌥⇧⌘2 / ⌥⌘5 / ⌥⌘1 | Two columns / three columns / two rows / grid / single layout |
| ⌃1–4, ⌃⇧1–4 | Focus a group, move the file to a group |
| ⌘K ⌘B | Toggle the side bar |
| ⌘R / ⌃G | Goto symbol / goto line |
| ⌥⌘[ / ⌥⌘] | Fold / unfold (⌘K ⌘1–5 folds by level, ⌘K ⌘J unfolds all) |
| ⌘F2 / F2 | Toggle a bookmark / next bookmark |
| ⌃- / ⌃⇧- | Jump back / forward |
| ⌃Q / ⌃⇧Q | Record a macro / play it back |
| ⌃Space | Show completions (they also appear as you type) |
| ⌘B / ⇧⌘B | Build / choose a build |
| ⌃\` | Console |
| ⎋ | Close the current panel or collapse cursors |

Every command is also in the menu bar and the Command Palette. Hover a button for a description.

## Windows and tabs

- **File › New Window** (⇧⌘N) opens another window. **Open Folder…** uses the current window if it has no folder yet, and a new window otherwise. **Add Folder to Workspace…** adds to the current one, and **Duplicate Workspace** opens a new window with the same folders.
- **Drag a tab** to reorder it, onto another split group to move it there, or **out of the window** to open it in a new window where you drop it.
- **Right-click a tab** for Close, Close Others, Close Tabs to the Right, Close All, **Open in New Window**, Split Right or Move to Next Group, Move to another window, Copy Path, Copy Relative Path and Reveal in Finder.
- Opening a file that's already open in another window switches to it there instead of opening a second copy.
- Closing a window (⇧⌘W or its close button) asks about unsaved files first. Quitting keeps everything, every window included, for next time.

## Find and replace

**⌘F** finds in the current file, and **⌥⌘F** replaces. ↩ finds the next match, ⇧↩ the previous one, and ⌥↩ selects every match as a cursor. In the Replace field, ↩ replaces one match and ⌥↩ replaces all of them.

Toggles: regex `.*` (⌥⌘R), case `Aa` (⌥⌘C), whole word `ab` (⌥⌘W), structural `{…}` (⌥⌘B), and preserve case `AB` (⌥⌘P).

- **Regex:** find `(\w+)_id`, replace `\U$1\E_ID` turns `user_id` into `USER_ID`.
- **Preserve case:** replacing `foo` with `bar` also turns `Foo` into `Bar` and `FOO` into `BAR`.

**Structural search** matches code by shape. `:[x]` matches anything with balanced `{}` `[]` `()` and quotes.

| Find | Replace | Effect |
| --- | --- | --- |
| `resource "aws_s3_bucket" :[name] {:[body]}` | `module :[name] {:[body]}` | Rewrites whole Terraform blocks, nested braces included |
| `foo(:[args])` | `bar(:[args])` | Renames calls, whatever the arguments |
| `instance_type = :[v\n]` | `instance_type = "t3.small"` | Replaces a value to the end of the line |

Other holes: `:[[x]]` is an identifier, `:[x.]` is non-space text, and `...` is anything unnamed.

## Search across files

**⇧⌘F** opens Search in the side bar. Results update as you type.

- **files to include:** `*.tf, modules/**`. **files to exclude:** `*.lock, vendor/**`. The `⋯` button shows or hides these fields.
- ⇧↩ adds a new line to the search, for multi-line matches.
- Each match and file has a checkbox. Replace All replaces only the checked ones, after asking.
- Hover a row to replace just that match or file (↻) or dismiss it (×). Click a match to open it.
- Open files change in the editor and stay unsaved. Other files are written directly. **Find › Undo Replace in Files** puts them back.
- F4 / ⇧F4 step through the results.

## Text tools

They're in **Edit › Text Tools**, or type `Text:` in the Command Palette. They act on the selection, or on the whole file when nothing is selected.

- **Sort Blocks:** orders Terraform/HCL, JSON or CSS blocks. Comments stay with their block.
- **Sort Blocks by Key…:** e.g. `^\w+\s+"[^"]*"\s+"([^"]*)"` sorts resources by name.
- **Sort Block Contents** sorts the attributes of the block under the cursor. **Align "="** lines up `=` like `terraform fmt`.
- Line tools: sort (natural, numeric, reverse), unique, `uniq -c`, `cut`, `tr`, `sed s/a/b/g`, keep or remove lines matching, join lines with a separator, remove blank lines, number lines.
- **Filter Through Command… (⌥⌘X):** pipes the selection through any shell command, for example `sort | uniq -c | sort -rn`, `awk -F, '{print $2}'`, `jq .` or `column -t`.

## Git

In a git repository, the gutter marks lines added, changed or deleted since the last commit. The side bar colors files by status (M, A, U, D), and the status bar shows the branch.

## Builds

**⌘B** runs the build for the current file, and **⇧⌘B** picks another one. inks saves first, then streams the output into a panel. Click a `file:line` reference to jump to it, or use F4 / ⇧F4. ⌃C cancels.

Built in: Go (build, run, test), Terraform (validate, fmt check, plan), Python, Node, TypeScript, shell, Ruby, Rust (cargo) and Make. Builds run in a login shell, so your PATH applies. Add your own in Settings:

```jsonc
"build_systems": [
  { "name": "Terraform: Plan (dev)", "cmd": "terraform plan -var-file=dev.tfvars", "ext": ["tf"] },
  { "name": "Lint", "cmd": "make lint", "needs": "Makefile" }
]
```

`cmd` can use `$file`, `$file_path`, `$file_name`, `$file_base_name` and `$folder`. `syntax` or `ext` limits a build to some files. `needs` names a file that must exist in the folder or a parent folder, and the build runs there.

## Settings and key bindings

**inks › Settings (⌘,)** opens the defaults as a read-only reference beside your `Preferences.inks-settings`:

```jsonc
{
  "font_size": 14,
  "tab_size": 2,
  "translate_tabs_to_spaces": true,
  "rulers": [80],
  "color_scheme": "Monokai",   // or any scheme from a package
  "word_wrap": "auto",         // true, false, or "auto" for Markdown and plain text
  "draw_minimap": true,
  "trim_trailing_white_space_on_save": true
}
```

Built-in schemes: Mariner, Monokai, Sixteen Ocean, Solarized Dark, Solarized Light, Breakwater and Celestine. **inks › Color Scheme…** previews them as you move through the list.

Key bindings live in **inks › Key Bindings**:

```jsonc
[
  { "keys": ["super+shift+k"], "command": "delete_line" },
  { "keys": ["super+k", "super+s"], "command": "sort_blocks" },
  { "keys": ["ctrl+t"], "command": "filter_through_command" }
]
```

Command names are in the Command Palette entries and in `internal/ui/keymap.go`. Settings, key bindings, packages and the session are kept in `~/Library/Application Support/inks`.

## Plugins and packages

Plugins are Python 3 files in `Packages/<name>/` (**inks › Packages › Browse Packages**). They load when inks starts and reload when you save them. **Packages › New Plugin…** starts one:

```python
import datetime
import inks_plugin

class InsertDateCommand(inks_plugin.TextCommand):
    def run(self, edit):
        for r in self.view.sel():
            self.view.replace(edit, r, datetime.date.today().isoformat())
```

Run it from the console with `view.run_command('insert_date')`, or bind `insert_date` to a key. The console (⌃\`) runs Python, with `view`, `window` and `inks` predefined.

**inks › Packages › Install Package…** installs packages from the public package index, along with their dependencies. Packages can add Python commands and event listeners, palette entries (`.inks-commands`), key bindings (`.inks-keymap`, without `context` rules), snippets (`.inks-snippet`) and color schemes (`.inks-color-scheme` or `.tmTheme`). Packages written for the legacy `*.sublime-*` resource names work too.

## Project layout

```
cmd/inks/             entry point: window, file-open events
internal/ui/          window, editor view, panels, side bar, commands, menus,
                      builds, git, plugin host bridge, packages
internal/editor/      selections, motions and editing commands
internal/buffer/      text storage, edits and undo
internal/search/      literal, regex and structural matching
internal/texttools/   block sorting, alignment, line tools
internal/syntax/      language detection and highlighting (chroma)
internal/theme/       color schemes, including package schemes
internal/plugins/     the embedded Python plugin host and inks modules
internal/pkgctl/      package index, install, upgrade
internal/linediff/    line diffs for the git gutter
internal/macmenu/     native macOS menu bar (Objective-C)
internal/settings/    defaults and the user settings file
```

## Testing

`make test` runs `go vet` and the unit tests. `make selftest` opens a window and drives the real app through 53 scripted checks: editing, search and replace, the menu bar, git, builds, layouts, folding, macros, plugins and more. It uses a throwaway profile and exits non-zero on failure.

Useful environment variables:

- `INKS_USER_DATA=<dir>` uses another profile folder.
- `INKS_NO_SESSION=1` starts without restoring or saving the session.
- `INKS_SNAPSHOT=<prefix>` makes `make selftest` also save PNG renders of a few screens.

## Releasing

1. Update `VERSION` and add a section to `CHANGELOG.md`.
2. Commit, then tag and push: `git tag -a v1.0.1 -m "inks 1.0.1" && git push origin main v1.0.1`.
3. The release workflow tests, builds the disk image and publishes a Gitea release with the changelog section as its notes.

To publish from your own Mac instead: `make release`, then
`GITEA_URL=https://gitea.tinks.us GITEA_REPO=inkstom/inks-go GITEA_TOKEN=… sh scripts/gitea-release.sh v1.0.1 dist/*.dmg`.

## Continuous integration

`.gitea/workflows/` holds two Gitea Actions workflows:

- **CI** (every push to `main` and every pull request): formatting, `go vet`, unit tests and an app build, plus `make selftest` when the repository variable `INKS_GUI_TESTS` is `true`.
- **Release** (tags `v*`, which must match `VERSION`): tests, then builds and publishes the disk image.

Both need a macOS [act_runner](https://docs.gitea.com/usage/actions/act-runner) registered with the label `macos` and running in host mode (not Docker), with the Xcode Command Line Tools installed. For the self-test, run the runner inside a logged-in user session (for example as a LaunchAgent), since it opens a window. If the automatic token can't create releases, add a personal access token with repository write access as the `RELEASE_TOKEN` secret.

## License

MIT, see [LICENSE](LICENSE).
