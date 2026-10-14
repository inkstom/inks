package theme

import (
	"encoding/xml"
	"fmt"
	"image/color"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"inks/internal/settings"
)

// Color schemes from packages: .inks-color-scheme / .sublime-color-scheme
// (JSON) and .tmTheme (plist). Scope rules are mapped onto the editor's
// token roles by matching each role's representative scope.

var (
	extMu    sync.Mutex
	external = map[string]*Scheme{}
)

// roleScopes are the scopes each highlighting role stands for.
var roleScopes = map[string][]string{
	"comment":     {"comment.line", "comment"},
	"keyword":     {"keyword.control", "keyword"},
	"storage":     {"storage.type", "storage"},
	"string":      {"string.quoted.double", "string"},
	"number":      {"constant.numeric"},
	"constant":    {"constant.language", "constant"},
	"function":    {"entity.name.function"},
	"call":        {"variable.function", "support.function", "meta.function-call"},
	"type":        {"entity.name.type.class", "entity.name.class", "support.type", "storage.type"},
	"tag":         {"entity.name.tag"},
	"attribute":   {"entity.other.attribute-name"},
	"operator":    {"keyword.operator"},
	"builtin":     {"support.function.builtin", "support.function", "variable.language"},
	"punctuation": {"punctuation.separator", "punctuation"},
	"heading":     {"markup.heading", "entity.name.section"},
	"variable":    {"variable.other.readwrite", "variable"},
	"escape":      {"constant.character.escape"},
	"invalid":     {"invalid.illegal", "invalid"},
}

// RegisterExternal adds (or replaces) a scheme from a package.
func RegisterExternal(s *Scheme) {
	extMu.Lock()
	external[s.Name] = s
	extMu.Unlock()
}

// ClearExternal forgets package schemes.
func ClearExternal() {
	extMu.Lock()
	external = map[string]*Scheme{}
	extMu.Unlock()
}

func lookupExternal(name string) *Scheme {
	extMu.Lock()
	defer extMu.Unlock()
	if s, ok := external[name]; ok {
		c := *s
		c.Tokens = map[string]color.NRGBA{}
		for k, v := range s.Tokens {
			c.Tokens[k] = v
		}
		return &c
	}
	return nil
}

func externalNames() []string {
	extMu.Lock()
	defer extMu.Unlock()
	var out []string
	for k := range external {
		out = append(out, k)
	}
	return out
}

// SchemeName normalizes "Packages/X/Monokai Pro.sublime-color-scheme" to
// "Monokai Pro".
func SchemeName(s string) string {
	base := path.Base(strings.ReplaceAll(s, `\`, "/"))
	for _, ext := range []string{".inks-color-scheme", ".sublime-color-scheme", ".tmTheme"} {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// rule is one scope → style rule.
type rule struct {
	scope  string
	fg     string
	italic bool
}

// build assembles a Scheme from globals and rules.
func build(name string, globals map[string]string, rules []rule, resolve func(string) (color.NRGBA, bool)) (*Scheme, error) {
	bg, ok := resolve(globals["background"])
	if !ok {
		return nil, fmt.Errorf("%s: no background color", name)
	}
	fg, ok := resolve(globals["foreground"])
	if !ok {
		fg = Hex("#d8dee9")
	}
	get := func(keys []string, def color.NRGBA) color.NRGBA {
		for _, k := range keys {
			if c, ok := resolve(globals[k]); ok {
				return c
			}
		}
		return def
	}
	dark := luminance(bg) < 0.5
	s := &Scheme{Name: name, Dark: dark, Bg: bg, Fg: fg, Tokens: map[string]color.NRGBA{}}
	s.Caret = get([]string{"caret"}, fg)
	s.Selection = flatten(get([]string{"selection"}, Mix(bg, fg, 0.2)), bg)
	s.LineHighlight = flatten(get([]string{"line_highlight", "lineHighlight"}, Mix(bg, fg, 0.05)), bg)
	s.Gutter = flatten(get([]string{"gutter_foreground", "gutterForeground"}, Mix(fg, bg, 0.5)), bg)
	s.GutterActive = flatten(get([]string{"gutter_foreground_highlight"}, fg), bg)
	s.Guide = flatten(get([]string{"guide", "rulers", "invisibles"}, Mix(bg, fg, 0.12)), bg)
	s.FindBg = flatten(get([]string{"find_highlight", "findHighlight"}, Hex("#f9ae58")), bg)
	s.Added = get([]string{"line_diff_added"}, Hex("#99c794"))
	s.Modified = get([]string{"line_diff_modified"}, Hex("#6699cc"))
	s.Deleted = get([]string{"line_diff_deleted"}, Hex("#ec5f66"))
	for role, scopes := range roleScopes {
		best, bestScore := -1, -1
		for i, r := range rules {
			if r.fg == "" {
				continue
			}
			for _, sc := range scopes {
				if score := selectorScore(r.scope, sc); score >= 0 && score >= bestScore {
					best, bestScore = i, score
				}
			}
		}
		if best >= 0 {
			if c, ok := resolve(rules[best].fg); ok {
				s.Tokens[role] = flatten(c, bg)
				if role == "comment" {
					s.ItalicComments = rules[best].italic
				}
			}
		}
	}
	return s, nil
}

// selectorScore returns how specifically selector matches scope (-1: no).
func selectorScore(selector, scope string) int {
	best := -1
	for _, part := range strings.Split(selector, ",") {
		if i := strings.Index(part, " - "); i >= 0 {
			part = part[:i]
		}
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		last := fields[len(fields)-1]
		if scope == last || strings.HasPrefix(scope, last+".") {
			score := 2 * (strings.Count(last, ".") + 1)
			if len(fields) > 1 {
				score -= 3 // context selectors (source.js …) may not apply here
			}
			best = max(best, score)
		}
	}
	return best
}

func luminance(c color.NRGBA) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

// flatten composites a translucent color over bg.
func flatten(c, bg color.NRGBA) color.NRGBA {
	if c.A == 255 {
		return c
	}
	return Mix(bg, color.NRGBA{c.R, c.G, c.B, 255}, float32(c.A)/255)
}

// ParseColorScheme reads a JSON color scheme.
func ParseColorScheme(name string, data []byte) (*Scheme, error) {
	obj, err := settings.ParseJSONC(string(data))
	if err != nil {
		return nil, err
	}
	str := func(m map[string]any) map[string]string {
		out := map[string]string{}
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	vars := map[string]string{}
	if v, ok := obj["variables"].(map[string]any); ok {
		vars = str(v)
	}
	globals := map[string]string{}
	if g, ok := obj["globals"].(map[string]any); ok {
		globals = str(g)
	}
	var rules []rule
	if list, ok := obj["rules"].([]any); ok {
		for _, x := range list {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			r := rule{}
			r.scope, _ = m["scope"].(string)
			r.fg, _ = m["foreground"].(string)
			fs, _ := m["font_style"].(string)
			r.italic = strings.Contains(fs, "italic")
			rules = append(rules, r)
		}
	}
	if n, ok := obj["name"].(string); ok && n != "" {
		name = n
	}
	var resolve func(string) (color.NRGBA, bool)
	depth := 0
	resolve = func(s string) (color.NRGBA, bool) {
		depth++
		defer func() { depth-- }()
		if depth > 10 {
			return color.NRGBA{}, false
		}
		return parseCSSColor(strings.TrimSpace(s), vars, resolve)
	}
	return build(name, globals, rules, resolve)
}

var (
	varRe   = regexp.MustCompile(`^var\(\s*([\w-]+)\s*\)$`)
	fnRe    = regexp.MustCompile(`^(rgba?|hsla?)\(([^)]*)\)$`)
	colorRe = regexp.MustCompile(`^color\((.*)\)$`)
	modRe   = regexp.MustCompile(`(alpha|a|blend|blenda|l|lightness|s|saturation)\(([^()]*(?:\([^()]*\))?[^()]*)\)`)
)

var named = map[string]string{
	"black": "#000000", "white": "#ffffff", "red": "#ff0000", "green": "#008000", "blue": "#0000ff",
	"yellow": "#ffff00", "orange": "#ffa500", "purple": "#800080", "gray": "#808080", "grey": "#808080",
	"transparent": "#00000000",
}

func parseCSSColor(s string, vars map[string]string, resolve func(string) (color.NRGBA, bool)) (color.NRGBA, bool) {
	switch {
	case s == "":
		return color.NRGBA{}, false
	case strings.HasPrefix(s, "#"):
		if len(s) != 4 && len(s) != 7 && len(s) != 9 && len(s) != 5 {
			return color.NRGBA{}, false
		}
		if len(s) == 5 { // #rgba
			s = "#" + string([]byte{s[1], s[1], s[2], s[2], s[3], s[3], s[4], s[4]})
		}
		return Hex(s), true
	case varRe.MatchString(s):
		v, ok := vars[varRe.FindStringSubmatch(s)[1]]
		if !ok {
			return color.NRGBA{}, false
		}
		return resolve(v)
	case colorRe.MatchString(s):
		inner := colorRe.FindStringSubmatch(s)[1]
		mods := modRe.FindAllStringSubmatchIndex(inner, -1)
		base := inner
		if len(mods) > 0 {
			base = inner[:mods[0][0]]
		}
		c, ok := resolve(strings.TrimSpace(base))
		if !ok {
			return c, false
		}
		for _, m := range mods {
			fn, arg := inner[m[2]:m[3]], strings.TrimSpace(inner[m[4]:m[5]])
			switch fn {
			case "alpha", "a":
				c.A = uint8(parseFrac(arg) * 255)
			case "blend", "blenda":
				parts := strings.Fields(arg)
				if len(parts) >= 2 {
					other, ok := resolve(strings.Join(parts[:len(parts)-1], " "))
					if ok {
						t := parseFrac(parts[len(parts)-1])
						// blend(X p%): p% of c, the rest of X.
						c = Mix(other, c, float32(t))
					}
				}
			}
		}
		return c, true
	case fnRe.MatchString(s):
		m := fnRe.FindStringSubmatch(s)
		args := strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(args) < 3 {
			return color.NRGBA{}, false
		}
		alpha := 1.0
		if len(args) >= 4 {
			alpha = parseFrac(args[3])
		}
		if strings.HasPrefix(m[1], "rgb") {
			ch := func(x string) uint8 {
				if strings.HasSuffix(x, "%") {
					return uint8(parseFrac(x) * 255)
				}
				f, _ := strconv.ParseFloat(x, 64)
				return uint8(math.Max(0, math.Min(255, f)))
			}
			return color.NRGBA{ch(args[0]), ch(args[1]), ch(args[2]), uint8(alpha * 255)}, true
		}
		h, _ := strconv.ParseFloat(strings.TrimSuffix(args[0], "deg"), 64)
		r, g, b := hslToRGB(h/360, parseFrac(args[1]), parseFrac(args[2]))
		return color.NRGBA{r, g, b, uint8(alpha * 255)}, true
	}
	if hex, ok := named[strings.ToLower(s)]; ok {
		return Hex(hex), true
	}
	return color.NRGBA{}, false
}

func parseFrac(s string) float64 {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		f, _ := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		return f / 100
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	hue := func(p, q, t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	var r, g, b float64
	if s == 0 {
		r, g, b = l, l, l
	} else {
		q := l * (1 + s)
		if l >= 0.5 {
			q = l + s - l*s
		}
		p := 2*l - q
		r, g, b = hue(p, q, h+1.0/3), hue(p, q, h), hue(p, q, h-1.0/3)
	}
	return uint8(r * 255), uint8(g * 255), uint8(b * 255)
}

// ParseTmTheme reads a TextMate theme (XML plist).
func ParseTmTheme(name string, data []byte) (*Scheme, error) {
	root, err := parsePlist(data)
	if err != nil {
		return nil, err
	}
	top, _ := root.(map[string]any)
	if n, ok := top["name"].(string); ok && n != "" {
		name = n
	}
	list, _ := top["settings"].([]any)
	globals := map[string]string{}
	var rules []rule
	for i, it := range list {
		m, _ := it.(map[string]any)
		st, _ := m["settings"].(map[string]any)
		scope, _ := m["scope"].(string)
		if i == 0 && scope == "" {
			for k, v := range st {
				if s, ok := v.(string); ok {
					globals[k] = s
				}
			}
			continue
		}
		fg, _ := st["foreground"].(string)
		fs, _ := st["fontStyle"].(string)
		rules = append(rules, rule{scope: scope, fg: fg, italic: strings.Contains(fs, "italic")})
	}
	resolve := func(s string) (color.NRGBA, bool) { return parseCSSColor(strings.TrimSpace(s), nil, nil) }
	return build(name, globals, rules, resolve)
}

// parsePlist decodes the subset of XML plists used by themes.
func parsePlist(data []byte) (any, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	var parse func(start xml.StartElement) (any, error)
	parse = func(start xml.StartElement) (any, error) {
		switch start.Name.Local {
		case "dict":
			out := map[string]any{}
			key := ""
			for {
				tok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				switch t := tok.(type) {
				case xml.StartElement:
					if t.Name.Local == "key" {
						var k string
						if err := dec.DecodeElement(&k, &t); err != nil {
							return nil, err
						}
						key = k
						continue
					}
					v, err := parse(t)
					if err != nil {
						return nil, err
					}
					out[key] = v
				case xml.EndElement:
					return out, nil
				}
			}
		case "array":
			var out []any
			for {
				tok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				switch t := tok.(type) {
				case xml.StartElement:
					v, err := parse(t)
					if err != nil {
						return nil, err
					}
					out = append(out, v)
				case xml.EndElement:
					return out, nil
				}
			}
		case "true", "false":
			_ = dec.Skip()
			return start.Name.Local == "true", nil
		default:
			var s string
			if err := dec.DecodeElement(&s, &start); err != nil {
				return nil, err
			}
			return s, nil
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if t, ok := tok.(xml.StartElement); ok && t.Name.Local != "plist" {
			return parse(t)
		}
	}
}
