// Package search finds and replaces text: literal, regular expression and
// structural (balanced bracket) templates.
//
// Structural templates:
//
//	:[name]    any balanced text (brackets and strings are matched), lazily
//	:[[name]]  an identifier
//	:[name.]   a run of non-whitespace
//	:[name\n]  the rest of the line
//	...        anonymous balanced hole
//
// Whitespace in a template matches any whitespace. Replacement templates
// insert holes with the same :[name] syntax.
package search

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
)

// Options select the kind of search.
type Options struct {
	Regex         bool
	CaseSensitive bool
	WholeWord     bool
	Structural    bool
	MaxMatches    int
}

// Hole is a captured structural hole.
type Hole struct {
	Text     string
	From, To int
}

// Match is one result; offsets are bytes into the searched text.
type Match struct {
	From, To int
	Text     string
	Groups   []string          // regex groups (0 = whole match)
	Named    map[string]string // named regex groups
	Holes    map[string]Hole   // structural holes
}

// Matcher compiles a query once and finds it in many texts.
type Matcher struct {
	Query string
	Opts  Options
	Err   error
	re    *regexp2.Regexp
	tmpl  []token
}

// New compiles query. Check m.Valid() before use.
func New(query string, opts Options) *Matcher {
	if opts.MaxMatches == 0 {
		opts.MaxMatches = 50000
	}
	m := &Matcher{Query: query, Opts: opts}
	if query == "" {
		m.Err = fmt.Errorf("empty query")
		return m
	}
	if opts.Structural {
		m.tmpl, m.Err = compileTemplate(query, opts.CaseSensitive)
		return m
	}
	src := query
	if !opts.Regex {
		src = regexp2.Escape(query)
		src = strings.ReplaceAll(src, "\\n", "\\r?\\n")
	}
	if opts.WholeWord {
		src = `(?<![\p{L}\p{N}_])(?:` + src + `)(?![\p{L}\p{N}_])`
	}
	ro := regexp2.Multiline
	if !opts.CaseSensitive {
		ro |= regexp2.IgnoreCase
	}
	m.re, m.Err = regexp2.Compile(src, ro)
	return m
}

// Valid reports whether the query compiled.
func (m *Matcher) Valid() bool { return m.Err == nil }

// FindAll returns every match in text[from:to].
func (m *Matcher) FindAll(text string, from, to int) []Match {
	if !m.Valid() {
		return nil
	}
	if to <= 0 || to > len(text) {
		to = len(text)
	}
	if m.Opts.Structural {
		return structuralFindAll(m.tmpl, text, from, to, m.Opts.MaxMatches)
	}
	runeToByte := runeOffsets(text)
	var out []Match
	startRune := byteToRune(text, from)
	mt, err := m.re.FindStringMatchStartingAt(text, startRune)
	for err == nil && mt != nil {
		b0 := runeToByte(mt.RuneIndex)
		b1 := runeToByte(mt.RuneIndex + mt.RuneLength)
		if b0 >= to || b1 > to {
			break
		}
		if b1 > b0 {
			res := Match{From: b0, To: b1, Text: text[b0:b1]}
			for _, g := range mt.Groups() {
				res.Groups = append(res.Groups, g.String())
				if g.Name != "" && !isDigits(g.Name) {
					if res.Named == nil {
						res.Named = map[string]string{}
					}
					res.Named[g.Name] = g.String()
				}
			}
			out = append(out, res)
			if len(out) >= m.Opts.MaxMatches {
				break
			}
		}
		mt, err = m.re.FindNextMatch(mt)
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// runeOffsets maps rune indexes to byte offsets (fast path for ASCII).
func runeOffsets(text string) func(int) int {
	ascii := true
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return func(i int) int { return i }
	}
	offs := make([]int, 0, len(text)+1)
	for i := range text {
		offs = append(offs, i)
	}
	offs = append(offs, len(text))
	return func(i int) int {
		if i < 0 {
			return 0
		}
		if i >= len(offs) {
			return len(text)
		}
		return offs[i]
	}
}

func byteToRune(text string, b int) int {
	if b <= 0 {
		return 0
	}
	if b > len(text) {
		b = len(text)
	}
	return utf8.RuneCountInString(text[:b])
}

// Replacement returns the text that replaces match given template.
func (m *Matcher) Replacement(match Match, template string, preserveCase bool) string {
	var out string
	switch {
	case m.Opts.Structural:
		out = expandStructural(template, match.Holes)
	case m.Opts.Regex:
		out = ExpandRegex(template, match)
	default:
		out = template
	}
	if preserveCase {
		out = ApplyCase(match.Text, out)
	}
	return out
}

// ExpandRegex expands $1 $& $<name> ${n} \1 \n \t and the case modifiers
// \U \L \E \u \l in a replacement template.
func ExpandRegex(template string, m Match) string {
	g := m.Groups
	if len(g) == 0 {
		g = []string{m.Text}
	}
	group := func(i int) string {
		if i < len(g) {
			return g[i]
		}
		return ""
	}
	var sb strings.Builder
	mode := byte(0)
	once := byte(0)
	push := func(s string) {
		if s == "" {
			return
		}
		if once != 0 {
			r, n := utf8.DecodeRuneInString(s)
			if once == 'u' {
				s = string(unicode.ToUpper(r)) + s[n:]
			} else {
				s = string(unicode.ToLower(r)) + s[n:]
			}
			once = 0
		}
		switch mode {
		case 'U':
			s = strings.ToUpper(s)
		case 'L':
			s = strings.ToLower(s)
		}
		sb.WriteString(s)
	}
	for i := 0; i < len(template); i++ {
		c := template[i]
		if i+1 >= len(template) {
			push(string(c))
			break
		}
		n := template[i+1]
		if c == '\\' {
			switch {
			case n >= '0' && n <= '9':
				push(group(int(n - '0')))
			case n == 'n':
				push("\n")
			case n == 't':
				push("\t")
			case n == 'r':
				push("\r")
			case n == 'U' || n == 'L':
				mode = n
			case n == 'E':
				mode = 0
			case n == 'u' || n == 'l':
				once = n
			default:
				push(string(n))
			}
			i++
			continue
		}
		if c == '$' {
			switch {
			case n == '$':
				push("$")
				i++
				continue
			case n == '&' || n == '0':
				push(group(0))
				i++
				continue
			case n >= '1' && n <= '9':
				if i+2 < len(template) && template[i+2] >= '0' && template[i+2] <= '9' {
					two := int(n-'0')*10 + int(template[i+2]-'0')
					if two < len(g) {
						push(group(two))
						i += 2
						continue
					}
				}
				push(group(int(n - '0')))
				i++
				continue
			case n == '<' || n == '{':
				close := byte('>')
				if n == '{' {
					close = '}'
				}
				if end := strings.IndexByte(template[i:], close); end > 0 {
					key := template[i+2 : i+end]
					if isDigits(key) {
						var idx int
						fmt.Sscan(key, &idx)
						push(group(idx))
					} else if m.Named != nil {
						push(m.Named[key])
					}
					i += end
					continue
				}
			}
		}
		push(string(c))
	}
	return sb.String()
}

// ApplyCase keeps the capitalisation style of original in replacement.
func ApplyCase(original, replacement string) string {
	if original == "" || replacement == "" {
		return replacement
	}
	var letters []rune
	for _, r := range original {
		if unicode.IsLetter(r) {
			letters = append(letters, r)
		}
	}
	if len(letters) == 0 {
		return replacement
	}
	s := string(letters)
	switch {
	case len(letters) > 1 && s == strings.ToUpper(s):
		return strings.ToUpper(replacement)
	case s == strings.ToLower(s):
		return strings.ToLower(replacement)
	case unicode.IsUpper(letters[0]) && string(letters[1:]) == strings.ToLower(string(letters[1:])):
		r, n := utf8.DecodeRuneInString(replacement)
		return string(unicode.ToUpper(r)) + strings.ToLower(replacement[n:])
	case unicode.IsUpper(letters[0]):
		r, n := utf8.DecodeRuneInString(replacement)
		return string(unicode.ToUpper(r)) + replacement[n:]
	}
	return replacement
}

// ---------------------------------------------------------------------------
// Structural templates

type token struct {
	lit  bool
	text string
	re   *regexp.Regexp // anchored literal
	find *regexp.Regexp // unanchored literal
	name string
	kind string // balanced, word, nonspace, line
}

var holeRe = regexp.MustCompile(`:\[\[(\w+)\]\]|:\[(\w+)(\.|\\n)?\]|\.\.\.`)

func compileTemplate(src string, caseSensitive bool) ([]token, error) {
	var toks []token
	flags := "(?s)"
	if !caseSensitive {
		flags = "(?si)"
	}
	// holeBefore/holeAfter: whether a hole sits on that side of the literal;
	// whitespace touching a hole must match at least one character.
	literal := func(text string, holeBefore, holeAfter bool) error {
		if text == "" {
			return nil
		}
		var sb strings.Builder
		parts := splitKeepSpace(text)
		for i, p := range parts {
			if strings.TrimSpace(p) == "" {
				prev, next := "", ""
				if i > 0 {
					prev = parts[i-1]
				}
				if i+1 < len(parts) {
					next = parts[i+1]
				}
				wordBefore := endsWord(prev) || (i == 0 && holeBefore)
				wordAfter := startsWord(next) || (i == len(parts)-1 && holeAfter)
				if wordBefore && wordAfter {
					sb.WriteString(`\s+`)
				} else {
					sb.WriteString(`\s*`)
				}
			} else {
				sb.WriteString(regexp.QuoteMeta(p))
			}
		}
		anchored, err := regexp.Compile(flags + `^(?:` + sb.String() + `)`)
		if err != nil {
			return err
		}
		find, err := regexp.Compile(flags + sb.String())
		if err != nil {
			return err
		}
		toks = append(toks, token{lit: true, text: text, re: anchored, find: find})
		return nil
	}
	last := 0
	for _, loc := range holeRe.FindAllStringSubmatchIndex(src, -1) {
		if err := literal(src[last:loc[0]], len(toks) > 0 && !toks[len(toks)-1].lit, true); err != nil {
			return nil, err
		}
		whole := src[loc[0]:loc[1]]
		switch {
		case whole == "...":
			toks = append(toks, token{name: "_", kind: "balanced"})
		case loc[2] >= 0:
			toks = append(toks, token{name: src[loc[2]:loc[3]], kind: "word"})
		default:
			kind := "balanced"
			if loc[6] >= 0 {
				if src[loc[6]:loc[7]] == "." {
					kind = "nonspace"
				} else {
					kind = "line"
				}
			}
			toks = append(toks, token{name: src[loc[4]:loc[5]], kind: kind})
		}
		last = loc[1]
	}
	if err := literal(src[last:], len(toks) > 0 && !toks[len(toks)-1].lit, false); err != nil {
		return nil, err
	}
	for len(toks) > 0 && toks[0].lit && strings.TrimSpace(toks[0].text) == "" {
		toks = toks[1:]
	}
	for len(toks) > 0 && toks[len(toks)-1].lit && strings.TrimSpace(toks[len(toks)-1].text) == "" {
		toks = toks[:len(toks)-1]
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("empty template")
	}
	hasLit := false
	for _, t := range toks {
		if t.lit {
			hasLit = true
		}
	}
	if !hasLit {
		return nil, fmt.Errorf("a structural template needs some literal text")
	}
	return toks, nil
}

func splitKeepSpace(s string) []string {
	var out []string
	start := 0
	inSpace := false
	for i, r := range s {
		sp := unicode.IsSpace(r)
		if i == 0 {
			inSpace = sp
			continue
		}
		if sp != inSpace {
			out = append(out, s[start:i])
			start = i
			inSpace = sp
		}
	}
	return append(out, s[start:])
}

func endsWord(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
func startsWord(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

var openers = map[byte]byte{'{': '}', '[': ']', '(': ')'}
var closers = map[byte]byte{'}': '{', ']': '[', ')': '('}

// scanBalanced calls visit at each position from pos where brackets are
// balanced; it stops at an unmatched closer or when visit returns true.
func scanBalanced(text string, pos, limit int, visit func(int) bool) bool {
	var stack []byte
	end := pos + limit
	if end > len(text) {
		end = len(text)
	}
	i := pos
	for i <= end {
		if len(stack) == 0 && visit(i) {
			return true
		}
		if i >= end {
			break
		}
		c := text[i]
		if c == '"' || c == '\'' || c == '`' {
			j := i + 1
			for j < len(text) && text[j] != c {
				if text[j] == '\\' {
					j++
				} else if text[j] == '\n' && c != '`' {
					break
				}
				j++
			}
			if j >= len(text) || text[j] != c {
				i++
				continue
			}
			i = j + 1
			continue
		}
		if _, ok := openers[c]; ok {
			stack = append(stack, c)
		} else if o, ok := closers[c]; ok {
			if len(stack) == 0 || stack[len(stack)-1] != o {
				return false
			}
			stack = stack[:len(stack)-1]
		}
		i++
	}
	return false
}

var (
	wordRe     = regexp.MustCompile(`^[\p{L}\p{N}_]+`)
	nonspaceRe = regexp.MustCompile(`^\S+`)
	lineRe     = regexp.MustCompile(`^[^\n]*`)
)

func matchTokens(toks []token, k int, text string, pos int, holes map[string]Hole, limitEnd int) int {
	if k == len(toks) {
		return pos
	}
	t := toks[k]
	if t.lit {
		loc := t.re.FindStringIndex(text[pos:])
		if loc == nil {
			return -1
		}
		return matchTokens(toks, k+1, text, pos+loc[1], holes, limitEnd)
	}
	bind := func(from, to int) bool {
		if t.name == "_" {
			return true
		}
		val := text[from:to]
		if h, ok := holes[t.name]; ok && h.Text != val {
			return false
		}
		holes[t.name] = Hole{Text: val, From: from, To: to}
		return true
	}
	unbind := func() {
		if t.name != "_" {
			delete(holes, t.name)
		}
	}
	switch t.kind {
	case "word", "nonspace", "line":
		re := map[string]*regexp.Regexp{"word": wordRe, "nonspace": nonspaceRe, "line": lineRe}[t.kind]
		loc := re.FindStringIndex(text[pos:])
		if loc == nil {
			return -1
		}
		if !bind(pos, pos+loc[1]) {
			return -1
		}
		r := matchTokens(toks, k+1, text, pos+loc[1], holes, limitEnd)
		if r < 0 {
			unbind()
		}
		return r
	}
	result := -1
	var next *token
	if k+1 < len(toks) {
		next = &toks[k+1]
	}
	limit := limitEnd - pos
	if limit > 200000 {
		limit = 200000
	}
	scanBalanced(text, pos, limit, func(i int) bool {
		if next != nil && next.lit && next.re.FindStringIndex(text[i:]) == nil {
			return false
		}
		if !bind(pos, i) {
			return false
		}
		if r := matchTokens(toks, k+1, text, i, holes, limitEnd); r >= 0 {
			result = r
			return true
		}
		unbind()
		return false
	})
	return result
}

func structuralFindAll(toks []token, text string, from, to, max int) []Match {
	var out []Match
	firstLit := -1
	for i, t := range toks {
		if t.lit {
			firstLit = i
			break
		}
	}
	pos := from
	for pos < to && len(out) < max {
		var start int
		if firstLit == 0 {
			loc := toks[0].find.FindStringIndex(text[pos:to])
			if loc == nil {
				break
			}
			start = pos + loc[0]
		} else {
			// Templates that begin with a hole anchor at word starts and openers.
			start = -1
			for i := pos; i < to; i++ {
				c := text[i]
				prevWord := i > 0 && (isWordByte(text[i-1]))
				if (isWordByte(c) && !prevWord) || c == '{' || c == '[' || c == '(' {
					start = i
					break
				}
			}
			if start < 0 {
				break
			}
		}
		holes := map[string]Hole{}
		end := matchTokens(toks, 0, text, start, holes, to)
		if end > start {
			out = append(out, Match{From: start, To: end, Text: text[start:end], Holes: holes})
			pos = end
		} else {
			_, n := utf8.DecodeRuneInString(text[start:])
			if n == 0 {
				n = 1
			}
			pos = start + n
		}
	}
	return out
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

var replHoleRe = regexp.MustCompile(`:\[\[(\w+)\]\]|:\[(\w+)(?:\.|\\n)?\]`)

func expandStructural(template string, holes map[string]Hole) string {
	out := replHoleRe.ReplaceAllStringFunc(template, func(m string) string {
		sub := replHoleRe.FindStringSubmatch(m)
		name := sub[1]
		if name == "" {
			name = sub[2]
		}
		return holes[name].Text
	})
	out = strings.ReplaceAll(out, `\n`, "\n")
	return strings.ReplaceAll(out, `\t`, "\t")
}

// ---------------------------------------------------------------------------
// Path filters for "files to include / exclude" ("*.tf, modules/**, -x")

// GlobRegexp converts a glob into a regular expression over slash paths.
func GlobRegexp(glob string) *regexp.Regexp {
	g := strings.TrimSpace(strings.ReplaceAll(glob, `\`, "/"))
	if g == "" {
		return nil
	}
	anchored := strings.HasPrefix(g, "/")
	g = strings.TrimPrefix(g, "/")
	var sb strings.Builder
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				sb.WriteString(".*")
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
				}
			} else {
				sb.WriteString("[^/]*")
			}
		case '?':
			sb.WriteString("[^/]")
		case '{':
			if end := strings.IndexByte(g[i:], '}'); end > 0 {
				opts := strings.Split(g[i+1:i+end], ",")
				for k := range opts {
					opts[k] = regexp.QuoteMeta(opts[k])
				}
				sb.WriteString("(?:" + strings.Join(opts, "|") + ")")
				i += end
			} else {
				sb.WriteString(`\{`)
			}
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	pre := "(^|/)"
	if anchored {
		pre = "^"
	}
	re, err := regexp.Compile("(?i)" + pre + sb.String() + "(/|$)")
	if err != nil {
		return nil
	}
	return re
}

// PathFilter returns a predicate over relative paths.
func PathFilter(include, exclude string) func(rel string) bool {
	var inc, exc []*regexp.Regexp
	for _, g := range strings.Split(include, ",") {
		if re := GlobRegexp(g); re != nil {
			inc = append(inc, re)
		}
	}
	for _, g := range strings.Split(exclude, ",") {
		if re := GlobRegexp(g); re != nil {
			exc = append(exc, re)
		}
	}
	return func(rel string) bool {
		p := strings.ReplaceAll(rel, `\`, "/")
		if len(inc) > 0 {
			ok := false
			for _, r := range inc {
				if r.MatchString(p) {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
		for _, r := range exc {
			if r.MatchString(p) {
				return false
			}
		}
		return true
	}
}
