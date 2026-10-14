// Package settings loads layered preferences: built-in defaults, then the
// user's Preferences.inks-settings (JSON with comments and trailing commas).
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Defaults are documented here and shown by "Preferences: Settings".
const DefaultText = `// inks default settings. Put your changes in the User settings file.
{
	// Color scheme: Mariner, Monokai, Sixteen Ocean, Solarized Dark,
	// Solarized Light, Breakwater, Celestine
	"color_scheme": "Mariner",
	"font_face": "Menlo",
	"font_size": 13,
	"ui_font_size": 13,
	"line_height": 1.45,
	"tab_size": 4,
	"translate_tabs_to_spaces": false,
	"detect_indentation": true,
	"auto_indent": true,
	"auto_match_enabled": true,
	"line_numbers": true,
	"highlight_line": false,
	"rulers": [],
	"caret_blink": true,
	"scroll_past_end": true,
	// Word wrap: true, false, or "auto" (Markdown and plain text).
	"word_wrap": "auto",
	"wrap_width": 0,
	"draw_minimap": true,
	"draw_indent_guides": true,
	// Git: change marks in the gutter, file colors in the side bar, branch
	// in the status bar.
	"git_gutter": true,
	"show_git_status": true,
	// Suggest words and plugin completions while typing.
	"auto_complete": true,
	"auto_complete_min_chars": 2,
	// Python plugins in Packages/<name>/*.py (Python 3 is found
	// automatically; set its path here if needed).
	"plugins_enabled": true,
	"python_path": "",
	"trim_trailing_white_space_on_save": false,
	"ensure_newline_at_eof_on_save": false,
	"hot_exit": true,
	"show_side_bar": true,
	"folder_exclude_patterns": [".git", ".svn", ".hg", "CVS", ".Trash", ".terraform", "node_modules", "__pycache__", ".venv"],
	"file_exclude_patterns": ["*.pyc", "*.o", "*.so", "*.dylib", "*.class", ".DS_Store", "*.tfstate", "*.tfstate.backup"],
	"binary_file_patterns": ["*.jpg", "*.jpeg", "*.png", "*.gif", "*.ico", "*.pdf", "*.zip", "*.gz", "*.ttf", "*.woff", "*.woff2"]
}
`

// Settings is the merged view.
type Settings struct {
	defaults map[string]any
	user     map[string]any
	Path     string // user settings file
}

// DataDir is where inks keeps settings, sessions and packages.
func DataDir() string {
	if d := os.Getenv("INKS_USER_DATA"); d != "" {
		return d
	}
	base, err := os.UserConfigDir() // ~/Library/Application Support on macOS
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "inks")
}

// UserDir is Packages/User inside the data dir.
func UserDir() string { return filepath.Join(DataDir(), "Packages", "User") }

// Load reads defaults and the user file (creating it if missing).
func Load() *Settings {
	s := &Settings{Path: filepath.Join(UserDir(), "Preferences.inks-settings")}
	s.defaults, _ = ParseJSONC(DefaultText)
	if _, err := os.Stat(s.Path); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(s.Path), 0o755)
		_ = os.WriteFile(s.Path, []byte("// Your settings override the defaults (Preferences: Settings shows them).\n{\n}\n"), 0o644)
	}
	s.Reload()
	return s
}

// Reload re-reads the user file; it returns a parse error if any.
func (s *Settings) Reload() error {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		s.user = map[string]any{}
		return nil
	}
	user, err := ParseJSONC(string(data))
	if err != nil {
		return err
	}
	s.user = user
	return nil
}

// Get returns a raw value (user first, then defaults).
func (s *Settings) Get(key string) any {
	if v, ok := s.user[key]; ok {
		return v
	}
	return s.defaults[key]
}

func (s *Settings) Bool(key string) bool {
	b, _ := s.Get(key).(bool)
	return b
}

func (s *Settings) Int(key string) int {
	switch v := s.Get(key).(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func (s *Settings) Float(key string) float64 {
	f, _ := s.Get(key).(float64)
	return f
}

func (s *Settings) String(key string) string {
	v, _ := s.Get(key).(string)
	return v
}

func (s *Settings) Strings(key string) []string {
	arr, _ := s.Get(key).([]any)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func (s *Settings) Ints(key string) []int {
	arr, _ := s.Get(key).([]any)
	var out []int
	for _, v := range arr {
		if f, ok := v.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

// Set writes a key into the user file, preserving comments elsewhere.
func (s *Settings) Set(key string, value any) error {
	data, _ := os.ReadFile(s.Path)
	text := string(data)
	if strings.TrimSpace(text) == "" {
		text = "{\n}\n"
	}
	enc, _ := json.Marshal(value)
	re := regexp.MustCompile(`(?m)^([ \t]*)"` + regexp.QuoteMeta(key) + `"\s*:\s*(?:"(?:[^"\\]|\\.)*"|\[[^\]]*\]|\{[^}]*\}|[^,\n}]+)(\s*,?)`)
	if re.MatchString(text) {
		text = re.ReplaceAllString(text, `${1}"`+key+`": `+strings.ReplaceAll(string(enc), "$", "$$")+`${2}`)
	} else {
		i := strings.LastIndex(text, "}")
		if i < 0 {
			i = len(text)
			text += "}"
		}
		before := strings.TrimRight(text[:i], " \t\n")
		comma := ","
		if strings.HasSuffix(before, "{") || strings.HasSuffix(before, ",") {
			comma = ""
		}
		text = before + comma + "\n\t\"" + key + "\": " + string(enc) + "\n}\n"
	}
	if err := os.WriteFile(s.Path, []byte(text), 0o644); err != nil {
		return err
	}
	return s.Reload()
}

// ParseJSONC parses JSON that may contain // and /* */ comments and
// trailing commas.
func ParseJSONC(text string) (map[string]any, error) {
	var sb strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(text) && text[j] != '"' {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(text) {
				j = len(text) - 1
			}
			sb.WriteString(text[i : j+1])
			i = j
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
			sb.WriteByte('\n')
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				i = len(text)
			} else {
				i += end + 3
			}
		default:
			sb.WriteByte(c)
		}
	}
	clean := regexp.MustCompile(`,(\s*[}\]])`).ReplaceAllString(sb.String(), "$1")
	out := map[string]any{}
	if strings.TrimSpace(clean) == "" {
		return out, nil
	}
	err := json.Unmarshal([]byte(clean), &out)
	return out, err
}
