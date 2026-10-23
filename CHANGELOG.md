# Changelog

## Unreleased

- **Side bar right-click menus** for files, folders and the empty space: open (to the side, in a new window, in the default app), reveal in Finder, **open a terminal, Claude Code or Codex in that folder** (iTerm2, or Terminal), new file and folder, rename, delete to the Trash, cut/copy/paste and duplicate files, copy path / relative path / name, find in folder, add or remove workspace folders, collapse all and refresh. The terminal commands are also in Tools and the Command Palette.
- **The `inks` command**, like VS Code's `code`: `inks` opens the current folder in a window, `inks <folder>` opens a folder in its own window, and `inks <files>` opens files in the window whose folder holds them. Options: `-n` (new window), `-a` (add to the active window), `-g file:line:col`, `-w` (wait until closed, for `core.editor`) and `-` (standard input). inks starts if it isn't running. Install it with **Tools › Install 'inks' Command in PATH**.
- **Expand All and Collapse All** for the side bar: buttons beside FOLDERS, right-click menu items and Command Palette commands.
- Side bar icons sit closer to the names, centered in a fixed slot so names line up.
- Nested files and folders are indented half as far.

## 1.1.0 — 2026-10-15

- **File icons** in the side bar and in Search results: the icon set of the File & Folder Icons extension for VS Code (file-icons), with about 2,200 extensions and 770 file names. Turn it off with `"sidebar_icons": false`.
- **The side bar stays current:** files and folders added, removed or renamed on disk show up within a couple of seconds, and Goto Anything lists them too. **View: Refresh Folders** forces a re-read.
- **Multiple windows:** File › New Window (⇧⌘N), Close Window (⇧⌘W), Add Folder to Workspace, Duplicate Workspace, Close Folder, and recent folders in Open Recent. Open Folder uses a new window when the current one already has a folder.
- **Tabs:** drag to reorder or to move to another split group, and drag out of the window to open the file in a new window under the pointer. Right-click a tab for close actions, Open in New Window, splitting, moving to another window, copying its path and Reveal in Finder.
- Opening a file that's already open in another window switches to it there.
- Closing a window asks about unsaved files. Quitting restores every window next time, and clicking the Dock icon with no windows open opens one.
- The first click in an inactive window acts right away, so you can drag a tab or place the caret without activating the window first.
- **Unsaved work survives crashes:** the session is saved within about three seconds of a change, written atomically, and saved on kill and logout signals. Each launch keeps the previous session as `Session.previous.json`.
- **Open and Save dialogs** start in the folder you last used, remembered across launches.
- **More classic key bindings:** goto definition (F12), goto symbol in project (⇧⌘R), goto word (⌘;), switch folder (⌃⌘P), bracket jump and selection (⌃M, ⇧⌘M), expand to indentation (⇧⌘J) and scope (⇧⌘Space), paste and indent (⇧⌘V), paste from history (⌘K ⌘V), block comments (⌥⌘/), transpose (⌃T), marks (⌘K ⌘Space/A/X), hard wrap (⌥⌘Q), find under caret (⌥⌘G), replace next (⌥⌘E), center caret (⌘K ⌘C), scroll lines (⌃⌥↑↓), header/source switch (⌥⌘↑), 4 columns and 3 rows, and distraction-free mode (⌃⇧⌘F).
- Fixed: after a document was restored, the first keystroke could move the caret to the start of the line.
- Fixed: on Macs set to prefer tabs (or with View › Show Tab Bar on), macOS added its own title-only tab bar under the title bar and merged new windows into it as tabs. inks now opts out of macOS window tabbing, since it has its own tabs.

## 1.0.0 — 2026-10-13

First release of the native inks: one Go binary drawing with [Gio](https://gioui.org), with no web view. The app is about 18 MB.

- **Editing:** multiple cursors (⌘D, ⌃⌘G, ⇧⌘L, ⌥-drag, ⌘-click), line and comment commands, auto-indent and bracket pairing, accent keys and input methods, word wrap, indent guides, minimap, code folding, bookmarks, jump back/forward, macros, autocomplete and snippets.
- **Layout:** tabs with preview tabs, split layouts (2 or 3 columns, 2 rows, a 2×2 grid), a resizable side bar, and a native macOS menu bar built from your key bindings.
- **Navigation:** Goto Anything (`@` symbols, `:` lines, `#` words) and the Command Palette.
- **Find and replace:** regex, case, whole word, preserve case, and structural search with `:[holes]` for balanced code blocks such as Terraform resources.
- **Search across files** in the side bar: live results, include/exclude globs, per-match and per-file checkboxes, previews, Replace All with confirmation, and Undo Replace in Files.
- **Text tools:** sort Terraform/HCL, JSON and CSS blocks, align `=`, sort/uniq/cut/tr/sed/grep, join lines, and Filter Through Command.
- **Git:** change marks in the gutter, file status in the side bar, and the branch in the status bar.
- **Builds (⌘B):** Go, Terraform, Python, Node, TypeScript, shell, Ruby, Rust and Make, plus your own in settings; streaming output with clickable errors.
- **Python plugins**, a console, and packages from the public package index, including their commands, key bindings, snippets and color schemes.
- Files reload when changed on disk, and unsaved work survives quitting (hot exit).
