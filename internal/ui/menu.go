package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"inks/internal/macmenu"
	"inks/internal/settings"
)

// menuItem describes one entry of the native menu bar.
type menuItem struct {
	title    string
	cmd      string
	args     map[string]any
	sel      string // standard Cocoa action instead of a command
	key      string // fixed shortcut for sel items, in key binding form
	role     string
	disabled bool
	sub      []menuItem
	sep      bool
}

var sep = menuItem{sep: true}

func cmdItem(title, cmd string, args ...any) menuItem {
	it := menuItem{title: title, cmd: cmd}
	if len(args) > 0 {
		it.args = map[string]any{}
		for i := 0; i+1 < len(args); i += 2 {
			it.args[args[i].(string)] = args[i+1]
		}
	}
	return it
}

func subMenu(title string, items ...menuItem) menuItem {
	return menuItem{title: title, sub: items}
}

func (a *App) menuSpec() []menuItem {
	var recent []menuItem
	for _, p := range a.recent {
		recent = append(recent, cmdItem(tildify(p), "open_file", "file", p))
	}
	if len(recent) > 0 {
		recent = append(recent, sep, cmdItem("Clear Items", "clear_recent_files"))
	} else {
		recent = append(recent, menuItem{title: "No Recent Files", disabled: true})
	}
	var textTools []menuItem
	for _, name := range []string{
		"sort_lines", "sort_lines_numeric", "sort_lines_reverse", "uniq_lines", "uniq_count", "",
		"sort_blocks", "sort_blocks_by_key", "sort_block_body", "align_equals", "",
		"cut_fields", "translate_chars", "sed_substitute", "keep_lines_matching", "remove_lines_matching", "",
		"join_lines_with", "remove_blank_lines", "number_lines",
	} {
		if name == "" {
			textTools = append(textTools, sep)
			continue
		}
		textTools = append(textTools, cmdItem(a.captionFor(name), name))
	}

	return []menuItem{
		subMenu("inks",
			menuItem{title: "About inks", sel: "orderFrontStandardAboutPanel:"},
			sep,
			cmdItem("Settings…", "edit_settings"),
			cmdItem("Key Bindings", "edit_keybindings"),
			cmdItem("Color Scheme…", "select_color_scheme"),
			sep,
			subMenu("Packages",
				cmdItem("Install Package…", "install_package"),
				cmdItem("Remove Package…", "remove_package"),
				cmdItem("Upgrade All Packages", "upgrade_all_packages"),
				cmdItem("List Packages", "list_packages"),
				sep,
				cmdItem("Browse Packages", "open_packages_folder"),
				cmdItem("New Plugin…", "new_plugin"),
				cmdItem("Restart Plugin Host", "restart_plugin_host"),
			),
			sep,
			menuItem{title: "Services", role: "services"},
			sep,
			menuItem{title: "Hide inks", sel: "hide:", key: "super+h"},
			menuItem{title: "Hide Others", sel: "hideOtherApplications:", key: "alt+super+h"},
			menuItem{title: "Show All", sel: "unhideAllApplications:"},
			sep,
			menuItem{title: "Quit inks", sel: "terminate:", key: "super+q"},
		),
		subMenu("File",
			cmdItem("New File", "new_file"),
			cmdItem("Open…", "prompt_open"),
			cmdItem("Open Folder…", "prompt_open_folder"),
			subMenu("Open Recent", recent...),
			cmdItem("Reopen Closed File", "reopen_last_file"),
			sep,
			cmdItem("Save", "save"),
			cmdItem("Save As…", "prompt_save_as"),
			cmdItem("Save All", "save_all"),
			sep,
			cmdItem("Reveal in Finder", "reveal_in_finder"),
			cmdItem("Revert File", "revert_file"),
			sep,
			cmdItem("Close File", "close"),
			cmdItem("Close All Files", "close_all"),
		),
		subMenu("Edit",
			cmdItem("Undo", "undo"),
			cmdItem("Redo", "redo"),
			subMenu("Undo Selection",
				cmdItem("Soft Undo", "soft_undo"),
				cmdItem("Soft Redo", "soft_redo"),
			),
			sep,
			cmdItem("Cut", "cut"),
			cmdItem("Copy", "copy"),
			cmdItem("Paste", "paste"),
			sep,
			subMenu("Line",
				cmdItem("Indent", "indent"),
				cmdItem("Unindent", "unindent"),
				sep,
				cmdItem("Swap Line Up", "swap_line_up"),
				cmdItem("Swap Line Down", "swap_line_down"),
				cmdItem("Duplicate Line", "duplicate_line"),
				cmdItem("Delete Line", "delete_line"),
				cmdItem("Join Lines", "join_lines"),
				sep,
				cmdItem("Insert Line After", "insert_line_after"),
				cmdItem("Insert Line Before", "insert_line_before"),
			),
			subMenu("Comment",
				cmdItem("Toggle Comment", "toggle_comment"),
			),
			subMenu("Text",
				cmdItem("Delete to End of Line", "delete_to_end"),
				cmdItem("Delete to Beginning of Line", "delete_to_beginning"),
			),
			subMenu("Convert Case",
				cmdItem("Upper Case", "upper_case"),
				cmdItem("Lower Case", "lower_case"),
				cmdItem("Title Case", "title_case"),
			),
			sep,
			subMenu("Code Folding",
				cmdItem("Fold", "fold"),
				cmdItem("Unfold", "unfold"),
				cmdItem("Unfold All", "unfold_all"),
				sep,
				cmdItem("Fold Level 1", "fold_by_level", "level", 1.0),
				cmdItem("Fold Level 2", "fold_by_level", "level", 2.0),
				cmdItem("Fold Level 3", "fold_by_level", "level", 3.0),
			),
			subMenu("Text Tools", textTools...),
			cmdItem("Filter Through Command…", "filter_through_command"),
		),
		subMenu("Selection",
			cmdItem("Split into Lines", "split_selection_into_lines"),
			cmdItem("Add Previous Line", "select_lines", "forward", false),
			cmdItem("Add Next Line", "select_lines", "forward", true),
			cmdItem("Single Selection", "single_selection"),
			sep,
			cmdItem("Select All", "select_all"),
			cmdItem("Expand Selection to Line", "expand_selection_line"),
			cmdItem("Expand Selection to Word", "find_under_expand"),
		),
		subMenu("Find",
			cmdItem("Find…", "show_find_panel"),
			cmdItem("Find Next", "find_next"),
			cmdItem("Find Previous", "find_prev"),
			cmdItem("Replace…", "show_replace_panel"),
			sep,
			cmdItem("Quick Add Next", "find_under_expand"),
			cmdItem("Quick Skip Next", "find_under_expand_skip"),
			cmdItem("Quick Find All", "find_all_under"),
			cmdItem("Use Selection for Find", "slurp_find_string"),
			sep,
			cmdItem("Find in Files…", "show_find_in_files"),
			cmdItem("Next Result", "next_result"),
			cmdItem("Previous Result", "prev_result"),
			cmdItem("Undo Replace in Files", "undo_replace_in_files"),
		),
		subMenu("View",
			subMenu("Side Bar",
				cmdItem("Toggle Side Bar", "toggle_side_bar"),
				cmdItem("Show Folders", "show_folders"),
				cmdItem("Show Search", "show_find_in_files"),
			),
			sep,
			subMenu("Layout",
				cmdItem("Single", "set_layout", "layout", "single"),
				cmdItem("Columns: 2", "set_layout", "layout", "columns_2"),
				cmdItem("Columns: 3", "set_layout", "layout", "columns_3"),
				cmdItem("Rows: 2", "set_layout", "layout", "rows_2"),
				cmdItem("Grid: 4", "set_layout", "layout", "grid_4"),
			),
			subMenu("Focus Group",
				cmdItem("Group 1", "focus_group", "group", 0.0),
				cmdItem("Group 2", "focus_group", "group", 1.0),
				cmdItem("Group 3", "focus_group", "group", 2.0),
				cmdItem("Group 4", "focus_group", "group", 3.0),
			),
			subMenu("Move File to Group",
				cmdItem("Group 1", "move_to_group", "group", 0.0),
				cmdItem("Group 2", "move_to_group", "group", 1.0),
				cmdItem("Group 3", "move_to_group", "group", 2.0),
				cmdItem("Group 4", "move_to_group", "group", 3.0),
			),
			sep,
			cmdItem("Word Wrap", "toggle_word_wrap"),
			cmdItem("Minimap", "toggle_minimap"),
			cmdItem("Indent Guides", "toggle_indent_guides"),
			sep,
			cmdItem("Syntax…", "pick_syntax"),
			cmdItem("Indentation…", "pick_indentation"),
			sep,
			subMenu("Font",
				cmdItem("Larger", "increase_font_size"),
				cmdItem("Smaller", "decrease_font_size"),
			),
		),
		subMenu("Goto",
			cmdItem("Goto Anything…", "show_overlay_goto"),
			cmdItem("Goto Symbol…", "show_overlay_goto", "text", "@"),
			cmdItem("Goto Line…", "show_overlay_goto", "text", ":"),
			sep,
			cmdItem("Next File", "next_view"),
			cmdItem("Previous File", "prev_view"),
			sep,
			cmdItem("Jump Back", "jump_back"),
			cmdItem("Jump Forward", "jump_forward"),
			sep,
			subMenu("Bookmarks",
				cmdItem("Toggle Bookmark", "toggle_bookmark"),
				cmdItem("Next Bookmark", "next_bookmark"),
				cmdItem("Previous Bookmark", "prev_bookmark"),
				cmdItem("Clear Bookmarks", "clear_bookmarks"),
				cmdItem("Select All Bookmarks", "select_all_bookmarks"),
			),
		),
		subMenu("Tools",
			cmdItem("Command Palette…", "show_overlay_command_palette"),
			cmdItem("Console", "show_panel", "panel", "console", "toggle", true),
			sep,
			cmdItem("Build", "build"),
			cmdItem("Build With…", "build_with"),
			cmdItem("Cancel Build", "cancel_build"),
			cmdItem("Show Build Output", "toggle_output_panel"),
			sep,
			cmdItem("Record Macro", "toggle_record_macro"),
			cmdItem("Playback Macro", "run_macro"),
			sep,
			cmdItem("Next Result", "next_result"),
			cmdItem("Previous Result", "prev_result"),
		),
		{title: "Window", role: "window", sub: []menuItem{
			{title: "Minimize", sel: "performMiniaturize:", key: "super+m"},
			{title: "Zoom", sel: "performZoom:"},
			sep,
			{title: "Bring All to Front", sel: "arrangeInFront:"},
		}},
	}
}

// captionFor returns the palette caption of a command without its category.
func (a *App) captionFor(cmd string) string {
	for _, e := range a.palette {
		if e.Command == cmd && e.Args == nil {
			c := e.Caption
			if i := strings.Index(c, ": "); i >= 0 {
				c = c[i+2:]
			}
			return c
		}
	}
	return cmd
}

// installMenu (re)builds the native menu bar from the spec and key bindings.
func (a *App) installMenu() {
	var actions []menuItem
	var conv func(items []menuItem) []macmenu.Item
	conv = func(items []menuItem) []macmenu.Item {
		var out []macmenu.Item
		for _, it := range items {
			if it.sep {
				out = append(out, macmenu.Item{Sep: true})
				continue
			}
			m := macmenu.Item{Title: it.title, Role: it.role, Selector: it.sel, Disabled: it.disabled}
			k := it.key
			if it.cmd != "" {
				actions = append(actions, it)
				m.ID = len(actions)
				k = a.keymap.SingleKeyFor(it.cmd, it.args)
			}
			m.Key, m.Mods = menuKey(k)
			if it.sub != nil {
				m.Sub = conv(it.sub)
				if m.Sub == nil {
					m.Sub = []macmenu.Item{}
				}
			}
			out = append(out, m)
		}
		return out
	}
	spec := conv(a.menuSpec())
	macmenu.Set(spec, func(id int) {
		if id <= 0 || id > len(actions) {
			return
		}
		it := actions[id-1]
		if os.Getenv("INKS_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "[menu] fire %d → %s %v\n", id, it.cmd, it.args)
		}
		a.Do(func() {
			a.focusEditor = !strings.HasPrefix(it.cmd, "show_")
			a.run(it.cmd, it.args)
		})
	})
}

// menuKey converts "shift+super+f" to a Cocoa key equivalent and modifiers.
func menuKey(k string) (string, int) {
	if k == "" {
		return "", 0
	}
	parts := strings.Split(k, "+")
	name := parts[len(parts)-1]
	mods := 0
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "super":
			mods |= macmenu.Command
		case "shift":
			mods |= macmenu.Shift
		case "alt":
			mods |= macmenu.Option
		case "ctrl":
			mods |= macmenu.Control
		}
	}
	special := map[string]rune{
		"up": 0xF700, "down": 0xF701, "left": 0xF702, "right": 0xF703,
		"home": 0xF729, "end": 0xF72B, "pageup": 0xF72C, "pagedown": 0xF72D, "delete": 0xF728,
		"enter": '\r', "backspace": 0x08, "tab": '\t', "escape": 0x1b, "space": ' ',
		"minus": '-', "equals": '=',
	}
	if len(name) >= 2 && name[0] == 'f' {
		n := 0
		for _, c := range name[1:] {
			if c < '0' || c > '9' {
				n = -1
				break
			}
			n = n*10 + int(c-'0')
		}
		if n >= 1 && n <= 20 {
			return string(rune(0xF704 + n - 1)), mods
		}
	}
	// Plain keys without ⌘ or ⌃ would steal typing from the editor.
	if mods&(macmenu.Command|macmenu.Control) == 0 {
		return "", 0
	}
	if r, ok := special[name]; ok {
		return string(r), mods
	}
	if len([]rune(name)) != 1 {
		return "", 0
	}
	return name, mods
}

// noteRecent records a file in the Open Recent menu.
func (a *App) noteRecent(p string) {
	if p == "" || strings.HasPrefix(p, settings.DataDir()+string(filepath.Separator)) {
		return
	}
	out := []string{p}
	for _, r := range a.recent {
		if r != p {
			out = append(out, r)
		}
	}
	if len(out) > 15 {
		out = out[:15]
	}
	a.recent = out
	a.menuStale = true
}

const macmenuCmdShift = macmenu.Command | macmenu.Shift
