# Changelog

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
