package ui

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"inks/internal/buffer"
	"inks/internal/theme"
)

// Snippets (*.inks-snippet / *.sublime-snippet) expand from their trigger
// with Tab and appear in autocomplete; package color schemes join the
// built-in ones.

type snippet struct {
	trigger, content, scope, desc string
}

func parseSnippet(data []byte) (snippet, bool) {
	var x struct {
		Content     string `xml:"content"`
		TabTrigger  string `xml:"tabTrigger"`
		Scope       string `xml:"scope"`
		Description string `xml:"description"`
	}
	if xml.Unmarshal(data, &x) != nil || x.TabTrigger == "" {
		return snippet{}, false
	}
	return snippet{trigger: strings.TrimSpace(x.TabTrigger), content: strings.Trim(x.Content, "\n"), scope: strings.TrimSpace(x.Scope), desc: strings.TrimSpace(x.Description)}, true
}

// scopeMatches reports whether a snippet scope selector applies to a syntax.
func scopeMatches(selector, lang string) bool {
	if strings.TrimSpace(selector) == "" {
		return true
	}
	base := baseScope(lang)
	for _, part := range strings.Split(selector, ",") {
		if i := strings.Index(part, " - "); i >= 0 {
			part = part[:i]
		}
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		s := f[0]
		if base == s || strings.HasPrefix(base, s+".") || strings.HasPrefix(s, base) {
			return true
		}
	}
	return false
}

func (a *Shared) loadSnippetsAndSchemes() {
	a.snippets = nil
	for _, pat := range []string{"*.inks-snippet", "*.sublime-snippet"} {
		for _, f := range findResources(pat) {
			if data, err := os.ReadFile(resourcePath(f)); err == nil {
				if sn, ok := parseSnippet(data); ok {
					a.snippets = append(a.snippets, sn)
				}
			}
		}
	}
	theme.ClearExternal()
	for _, pat := range []string{"*.inks-color-scheme", "*.sublime-color-scheme", "*.tmTheme"} {
		for _, f := range findResources(pat) {
			data, err := os.ReadFile(resourcePath(f))
			if err != nil {
				continue
			}
			name := theme.SchemeName(f)
			var s *theme.Scheme
			if strings.HasSuffix(f, ".tmTheme") {
				s, err = theme.ParseTmTheme(name, data)
			} else {
				s, err = theme.ParseColorScheme(name, data)
			}
			if err != nil {
				a.consoleLog("color scheme "+f+": "+err.Error(), true)
				continue
			}
			s.Name = name // pick by file name, like the setting does
			theme.RegisterExternal(s)
		}
	}
	for _, w := range a.windows {
		w.applyTheme()
	}
}

// snippetsFor lists snippets that apply to d.
func (a *App) snippetsFor(d *Doc) []snippet {
	var out []snippet
	for _, s := range a.snippets {
		if scopeMatches(s.scope, d.Lang) {
			out = append(out, s)
		}
	}
	return out
}

var snippetVar = regexp.MustCompile(`\$\{?(TM_FILENAME|TM_FILEPATH|TM_SELECTED_TEXT|SELECTION|TM_CURRENT_LINE|TM_LINE_INDEX|TM_LINE_NUMBER|TM_DIRECTORY|CURRENT_YEAR)\}?`)

// expandSnippetVars fills in the variables snippets commonly use.
func expandSnippetVars(content string, d *Doc) string {
	h := d.M.MainSel()
	return snippetVar.ReplaceAllStringFunc(content, func(m string) string {
		name := strings.Trim(m, "${}")
		switch name {
		case "TM_FILENAME":
			return filepath.Base(d.Path)
		case "TM_FILEPATH":
			return d.Path
		case "TM_DIRECTORY":
			return filepath.Dir(d.Path)
		case "TM_SELECTED_TEXT", "SELECTION":
			return d.M.Buf.Slice(h.Start(), h.End())
		case "TM_CURRENT_LINE":
			return d.M.Buf.Line(h.Head.Line)
		case "TM_LINE_NUMBER":
			return itoa(h.Head.Line + 1)
		case "TM_LINE_INDEX":
			return itoa(h.Head.Col)
		case "CURRENT_YEAR":
			return itoa(time.Now().Year())
		}
		return ""
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// expandSnippetAtCaret replaces a trigger before every caret; false when the
// word before the main caret is not a trigger.
func (a *App) expandSnippetAtCaret(d *Doc) bool {
	prefix := prefixAt(d)
	if prefix == "" {
		return false
	}
	for _, s := range a.snippetsFor(d) {
		if s.trigger == prefix {
			a.insertSnippet(d, s.content, prefix)
			return true
		}
	}
	return false
}

// insertSnippet inserts snippet text at every caret (replacing prefix),
// indenting continuation lines and placing the caret at the first field
// (its placeholder selected).
func (a *App) insertSnippet(d *Doc, content, prefix string) {
	m := d.M
	content = expandSnippetVars(content, d)
	// Fields: ${1:placeholder}, $1, $0. Remember the first field's span.
	var sb strings.Builder
	first, firstLen, last := -1, 0, 0
	firstNum := 1 << 30
	for _, mt := range snippetField.FindAllStringSubmatchIndex(content, -1) {
		sb.WriteString(content[last:mt[0]])
		num := 0
		if mt[2] >= 0 {
			num = atoiSafe(content[mt[2]:mt[3]])
		} else {
			num = atoiSafe(content[mt[6]:mt[7]])
		}
		if num == 0 {
			num = 1 << 29 // $0 is the final position
		}
		ph := ""
		if mt[4] >= 0 {
			ph = content[mt[4]:mt[5]]
		}
		if num < firstNum {
			firstNum, first, firstLen = num, sb.Len(), len(ph)
		}
		sb.WriteString(ph)
		last = mt[1]
	}
	sb.WriteString(content[last:])
	text := strings.ReplaceAll(sb.String(), `\$`, "$")
	if first < 0 {
		first = len(text)
	}
	if m.UseSpaces {
		text = strings.ReplaceAll(text, "\t", strings.Repeat(" ", m.TabSize))
	}
	var edits []buffer.Edit
	var starts []buffer.Range
	var texts []string
	for _, r := range m.Sels {
		h := r.Head
		line := m.Buf.Line(h.Line)
		from := r.Start().Col
		if r.Empty() && prefix != "" && strings.HasSuffix(line[:h.Col], prefix) {
			from = h.Col - len(prefix)
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		t := strings.ReplaceAll(text, "\n", "\n"+indent)
		start := buffer.Pos{Line: r.Start().Line, Col: from}
		edits = append(edits, buffer.Edit{Start: start, End: r.End(), Text: t})
		starts = append(starts, buffer.Cursor(start))
		texts = append(texts, t)
	}
	m.Buf.BreakUndoGroup()
	// Insertion starts stay put (Assoc -1), so each maps to where its text begins.
	mapped := m.Buf.Apply(edits, starts, buffer.ApplyOptions{Kind: "snippet", Assoc: -1})
	var sels []buffer.Range
	for i, st := range mapped {
		off := indentedOffset(text, first, texts[i])
		p := buffer.EndOf(st.Head, texts[i][:off])
		head := p
		if field := texts[i][off:min(len(texts[i]), off+firstLen)]; !strings.Contains(field, "\n") {
			head = buffer.Pos{Line: p.Line, Col: p.Col + len(field)}
		}
		sels = append(sels, buffer.Range{Anchor: p, Head: head, Goal: -1})
	}
	if len(sels) > 0 {
		m.SetSels(sels, len(sels)-1)
	}
	a.afterEdit(d)
}

// indentedOffset maps an offset in the plain snippet text to the offset in
// its indented copy.
func indentedOffset(plain string, off int, indented string) int {
	nl := strings.Count(plain[:off], "\n")
	if nl == 0 {
		return off
	}
	extra := (len(indented) - len(plain)) / max(1, strings.Count(plain, "\n"))
	return off + nl*extra
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
