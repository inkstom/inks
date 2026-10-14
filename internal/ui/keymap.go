package ui

import (
	"os"
	"path/filepath"
	"strings"

	"gioui.org/io/key"

	"inks/internal/settings"
)

// Binding maps a key sequence to a command with arguments.
type Binding struct {
	Keys    []string       `json:"keys"`
	Command string         `json:"command"`
	Args    map[string]any `json:"args"`
	Context []any          `json:"context"`
	norm    []string
}

func b(keys string, cmd string, args ...any) Binding {
	bd := Binding{Keys: strings.Split(keys, ", "), Command: cmd}
	if len(args) > 0 {
		bd.Args = map[string]any{}
		for i := 0; i+1 < len(args); i += 2 {
			bd.Args[args[i].(string)] = args[i+1]
		}
	}
	return bd
}

// DefaultBindings are the built-in key bindings (macOS).
var DefaultBindings = []Binding{
	b("super+n", "new_file"),
	b("super+o", "prompt_open"),
	b("super+alt+o", "prompt_open_folder"),
	b("super+shift+t", "reopen_last_file"),
	b("super+s", "save"),
	b("super+shift+s", "prompt_save_as"),
	b("super+alt+s", "save_all"),
	b("super+w", "close"),
	b("super+q", "quit"),
	b("super+k, super+w", "close_all"),
	b("super+shift+p", "show_overlay_command_palette"),
	b("super+t", "show_overlay_goto"),
	b("super+p", "show_overlay_goto"),
	b("super+r", "show_overlay_goto", "text", "@"),
	b("ctrl+g", "show_overlay_goto", "text", ":"),
	b("super+f", "show_find_panel"),
	b("super+alt+f", "show_replace_panel"),
	b("super+g", "find_next"),
	b("super+shift+g", "find_prev"),
	b("super+e", "slurp_find_string"),
	b("super+shift+f", "show_find_in_files"),
	b("alt+super+r", "toggle_regex"),
	b("alt+super+c", "toggle_case_sensitive"),
	b("alt+super+w", "toggle_whole_word"),
	b("alt+super+b", "toggle_structural"),
	b("alt+super+p", "toggle_preserve_case"),
	b("f4", "next_result"),
	b("alt+super+[", "fold"),
	b("alt+super+]", "unfold"),
	b("super+k, super+j", "unfold_all"),
	b("super+k, super+0", "unfold_all"),
	b("super+k, super+1", "fold_by_level", "level", 1.0),
	b("super+k, super+2", "fold_by_level", "level", 2.0),
	b("super+k, super+3", "fold_by_level", "level", 3.0),
	b("super+k, super+4", "fold_by_level", "level", 4.0),
	b("super+k, super+5", "fold_by_level", "level", 5.0),
	b("super+f2", "toggle_bookmark"),
	b("f2", "next_bookmark"),
	b("shift+f2", "prev_bookmark"),
	b("shift+super+f2", "clear_bookmarks"),
	b("alt+f2", "select_all_bookmarks"),
	b("ctrl+minus", "jump_back"),
	b("ctrl+shift+minus", "jump_forward"),
	b("ctrl+q", "toggle_record_macro"),
	b("ctrl+shift+q", "run_macro"),
	b("super+b", "build"),
	b("super+shift+b", "build_with"),
	b("ctrl+c", "cancel_build"),
	b("ctrl+`", "show_panel", "panel", "console", "toggle", true),
	b("ctrl+space", "auto_complete"),
	b("shift+f4", "prev_result"),
	b("super+k, super+e", "show_folders"),
	b("super+d", "find_under_expand"),
	b("super+k, super+d", "find_under_expand_skip"),
	b("ctrl+super+g", "find_all_under"),
	b("super+l", "expand_selection_line"),
	b("super+shift+l", "split_selection_into_lines"),
	b("ctrl+shift+up", "select_lines", "forward", false),
	b("ctrl+shift+down", "select_lines", "forward", true),
	b("super+a", "select_all"),
	b("super+z", "undo"),
	b("super+y", "redo"),
	b("super+shift+z", "redo"),
	b("super+u", "soft_undo"),
	b("super+shift+u", "soft_redo"),
	b("super+c", "copy"),
	b("super+x", "cut"),
	b("super+v", "paste"),
	b("super+]", "indent"),
	b("super+[", "unindent"),
	b("ctrl+super+up", "swap_line_up"),
	b("ctrl+super+down", "swap_line_down"),
	b("super+shift+d", "duplicate_line"),
	b("ctrl+shift+k", "delete_line"),
	b("super+j", "join_lines"),
	b("super+/", "toggle_comment"),
	b("super+enter", "insert_line_after"),
	b("super+shift+enter", "insert_line_before"),
	b("ctrl+k", "delete_to_end"),
	b("super+backspace", "delete_to_beginning"),
	b("super+k, super+u", "upper_case"),
	b("super+k, super+l", "lower_case"),
	b("super+k, super+b", "toggle_side_bar"),
	b("super+alt+x", "filter_through_command"),
	b("f5", "sort_lines"),
	b("super+shift+equals", "increase_font_size"),
	b("super+equals", "increase_font_size"),
	b("super+minus", "decrease_font_size"),
	b("ctrl+tab", "next_view"),
	b("ctrl+shift+tab", "prev_view"),
	b("super+shift+]", "next_view"),
	b("super+shift+[", "prev_view"),
	b("super+alt+right", "next_view"),
	b("super+alt+left", "prev_view"),
	b("super+1", "select_by_index", "index", 0.0),
	b("super+2", "select_by_index", "index", 1.0),
	b("super+3", "select_by_index", "index", 2.0),
	b("super+4", "select_by_index", "index", 3.0),
	b("super+5", "select_by_index", "index", 4.0),
	b("super+6", "select_by_index", "index", 5.0),
	b("super+7", "select_by_index", "index", 6.0),
	b("super+8", "select_by_index", "index", 7.0),
	b("super+9", "select_by_index", "index", 8.0),
	b("super+,", "edit_settings"),
	b("alt+super+1", "set_layout", "layout", "single"),
	b("alt+super+2", "set_layout", "layout", "columns_2"),
	b("alt+super+3", "set_layout", "layout", "columns_3"),
	b("alt+shift+super+2", "set_layout", "layout", "rows_2"),
	b("alt+super+5", "set_layout", "layout", "grid_4"),
	b("ctrl+1", "focus_group", "group", 0.0),
	b("ctrl+2", "focus_group", "group", 1.0),
	b("ctrl+3", "focus_group", "group", 2.0),
	b("ctrl+4", "focus_group", "group", 3.0),
	b("ctrl+shift+1", "move_to_group", "group", 0.0),
	b("ctrl+shift+2", "move_to_group", "group", 1.0),
	b("ctrl+shift+3", "move_to_group", "group", 2.0),
	b("ctrl+shift+4", "move_to_group", "group", 3.0),
	b("escape", "escape"),
}

var keyNames = map[key.Name]string{
	key.NameLeftArrow: "left", key.NameRightArrow: "right", key.NameUpArrow: "up", key.NameDownArrow: "down",
	key.NameReturn: "enter", key.NameEnter: "keypad_enter", key.NameEscape: "escape", key.NameHome: "home",
	key.NameEnd: "end", key.NameDeleteBackward: "backspace", key.NameDeleteForward: "delete",
	key.NamePageUp: "pageup", key.NamePageDown: "pagedown", key.NameTab: "tab", key.NameSpace: "space",
	"-": "minus", "=": "equals", "+": "equals",
}

// keyString converts a Gio key event to "ctrl+alt+shift+super+x" form.
func keyString(e key.Event) string {
	name, ok := keyNames[e.Name]
	if !ok {
		name = strings.ToLower(string(e.Name))
	}
	var parts []string
	if e.Modifiers.Contain(key.ModCtrl) {
		parts = append(parts, "ctrl")
	}
	if e.Modifiers.Contain(key.ModAlt) {
		parts = append(parts, "alt")
	}
	if e.Modifiers.Contain(key.ModShift) {
		parts = append(parts, "shift")
	}
	if e.Modifiers.Contain(key.ModCommand) {
		parts = append(parts, "super")
	}
	return strings.Join(append(parts, name), "+")
}

// normalizeKey puts modifiers in canonical order.
func normalizeKey(k string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(k)), "+")
	last := parts[len(parts)-1]
	switch last {
	case "-":
		last = "minus"
	case "=", "plus":
		last = "equals"
	case "return":
		last = "enter"
	}
	mods := map[string]bool{}
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "cmd", "command", "meta", "primary":
			m = "super"
		case "option":
			m = "alt"
		case "control":
			m = "ctrl"
		}
		mods[m] = true
	}
	var out []string
	for _, m := range []string{"ctrl", "alt", "shift", "super"} {
		if mods[m] {
			out = append(out, m)
		}
	}
	return strings.Join(append(out, last), "+")
}

// Keymap resolves key sequences.
type Keymap struct {
	bindings     []Binding
	pending      string
	packageFiles []string // key bindings from installed packages
}

func newKeymap() *Keymap {
	km := &Keymap{}
	km.Load()
	return km
}

// Load (re)reads the user's key bindings file on top of the defaults.
func (km *Keymap) Load() {
	all := append([]Binding(nil), DefaultBindings...)
	for _, f := range km.packageFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var pkg []Binding
		if parseJSONCInto(string(data), &pkg) != nil {
			continue
		}
		for _, bd := range pkg {
			// Context rules are not evaluated; such bindings could steal keys.
			if len(bd.Context) == 0 && len(bd.Keys) > 0 && bd.Command != "" {
				all = append(all, bd)
			}
		}
	}
	path := filepath.Join(settings.UserDir(), "Default (OSX).inks-keymap")
	if data, err := os.ReadFile(path); err == nil {
		var user []Binding
		if err := parseJSONCInto(string(data), &user); err == nil {
			all = append(all, user...)
		}
	} else if os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte("// Your key bindings, e.g.\n// { \"keys\": [\"super+shift+k\"], \"command\": \"delete_line\" }\n[\n]\n"), 0o644)
	}
	for i := range all {
		all[i].norm = make([]string, len(all[i].Keys))
		for j, k := range all[i].Keys {
			all[i].norm[j] = normalizeKey(k)
		}
	}
	km.bindings = all
}

// Feed takes one key and returns the matched binding, whether the key was
// consumed (part of a chord), and a status hint while waiting for a chord.
func (km *Keymap) Feed(k string) (*Binding, bool, string) {
	if km.pending != "" {
		seq := []string{km.pending, k}
		km.pending = ""
		return km.find(seq), true, ""
	}
	if bd := km.find([]string{k}); bd != nil {
		return bd, true, ""
	}
	for _, bd := range km.bindings {
		if len(bd.norm) > 1 && bd.norm[0] == k {
			km.pending = k
			return nil, true, prettyKey(k) + " was pressed, waiting for second key…"
		}
	}
	return nil, false, ""
}

func (km *Keymap) find(seq []string) *Binding {
	for i := len(km.bindings) - 1; i >= 0; i-- {
		bd := &km.bindings[i]
		if len(bd.norm) != len(seq) {
			continue
		}
		match := true
		for j := range seq {
			if bd.norm[j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return bd
		}
	}
	return nil
}

// KeysFor returns the display form of the binding for a command, if any.
func (km *Keymap) KeysFor(cmd string, args map[string]any) string {
	for i := len(km.bindings) - 1; i >= 0; i-- {
		bd := km.bindings[i]
		if bd.Command != cmd || !sameArgs(bd.Args, args) {
			continue
		}
		var parts []string
		for _, k := range bd.norm {
			parts = append(parts, prettyKey(k))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// SingleKeyFor returns the one-key binding for a command ("" for none or chords).
func (km *Keymap) SingleKeyFor(cmd string, args map[string]any) string {
	for i := len(km.bindings) - 1; i >= 0; i-- {
		bd := km.bindings[i]
		if bd.Command == cmd && sameArgs(bd.Args, args) {
			if len(bd.norm) == 1 {
				return bd.norm[0]
			}
			return ""
		}
	}
	return ""
}

func sameArgs(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func prettyKey(k string) string {
	parts := strings.Split(k, "+")
	last := parts[len(parts)-1]
	sym := map[string]string{"up": "↑", "down": "↓", "left": "←", "right": "→", "enter": "↩", "escape": "⎋",
		"backspace": "⌫", "delete": "⌦", "tab": "⇥", "space": "Space", "minus": "-", "equals": "=", "pageup": "⇞", "pagedown": "⇟"}
	if s, ok := sym[last]; ok {
		last = s
	} else {
		last = strings.ToUpper(last)
	}
	var sb strings.Builder
	for _, m := range parts[:len(parts)-1] {
		sb.WriteString(map[string]string{"ctrl": "⌃", "alt": "⌥", "shift": "⇧", "super": "⌘"}[m])
	}
	return sb.String() + last
}

func parseJSONCInto(text string, v any) error {
	// Wrap arrays so the JSONC parser (which expects an object) can read them.
	obj, err := settings.ParseJSONC(`{"v": ` + text + `}`)
	if err != nil {
		return err
	}
	return remarshal(obj["v"], v)
}
