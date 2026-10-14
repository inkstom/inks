// Package syntax detects languages and produces per-line highlight spans
// using chroma lexers.
package syntax

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// PlainText is the name used when no lexer applies.
const PlainText = "Plain Text"

// Span colors bytes [Start, End) of a line with a theme role.
type Span struct {
	Start, End int
	Role       string
}

// Names lists every available language.
func Names() []string {
	names := []string{PlainText}
	for _, l := range lexers.GlobalLexerRegistry.Lexers {
		names = append(names, l.Config().Name)
	}
	sort.Slice(names[1:], func(i, j int) bool { return strings.ToLower(names[i+1]) < strings.ToLower(names[j+1]) })
	return names
}

var extra = map[string]string{
	".tf": "Terraform", ".tfvars": "Terraform", ".hcl": "Terraform", ".inks-settings": "JSON", ".inks-keymap": "JSON",
	".inks-build": "JSON", ".inks-project": "JSON", ".inks-commands": "JSON", ".mjs": "JavaScript", ".cjs": "JavaScript",
}

// Detect picks a language for a file name and its first line.
func Detect(path, firstLine string) string {
	if path != "" {
		if name, ok := extra[strings.ToLower(filepath.Ext(path))]; ok && lexers.Get(name) != nil {
			return name
		}
		if l := lexers.Match(filepath.Base(path)); l != nil && !isPlain(l.Config().Name) {
			return l.Config().Name
		}
	}
	if strings.HasPrefix(firstLine, "#!") {
		if l := lexers.Analyse(firstLine + "\n"); l != nil && !isPlain(l.Config().Name) {
			return l.Config().Name
		}
		for key, name := range map[string]string{"python": "Python", "node": "JavaScript", "bash": "Bash", "sh": "Bash", "zsh": "Bash", "ruby": "Ruby", "perl": "Perl"} {
			if strings.Contains(firstLine, key) {
				return name
			}
		}
	}
	return PlainText
}

func isPlain(name string) bool {
	n := strings.ToLower(name)
	return n == "plaintext" || n == "plain text" || n == "text"
}

// Comments returns the line and block comment tokens for a language.
func Comments(lang string) (line string, block [2]string) {
	l := strings.ToLower(lang)
	switch {
	case l == "python" || l == "ruby" || l == "bash" || l == "yaml" || l == "toml" || l == "perl" || l == "r" || l == "makefile" || l == "docker" || l == "nginx configuration file" || strings.Contains(l, "shell") || l == "ini" || l == "cmake":
		return "#", [2]string{}
	case l == "terraform" || l == "hcl":
		return "#", [2]string{"/*", "*/"}
	case l == "sql" || l == "lua" || l == "haskell":
		return "--", [2]string{}
	case l == "html" || l == "xml" || l == "markdown" || l == "vue" || l == "svelte":
		return "", [2]string{"<!--", "-->"}
	case l == "css":
		return "", [2]string{"/*", "*/"}
	case l == "lisp" || l == "clojure" || l == "scheme" || l == "emacslisp":
		return ";", [2]string{}
	case l == "plain text" || l == "json":
		return "", [2]string{}
	}
	return "//", [2]string{"/*", "*/"}
}

// roleFor maps a chroma token type to a theme role.
func roleFor(t chroma.TokenType) string {
	switch {
	case t.InCategory(chroma.Comment):
		return "comment"
	case t == chroma.KeywordType:
		return "type"
	case t == chroma.KeywordConstant:
		return "constant"
	case t == chroma.KeywordDeclaration || t == chroma.KeywordNamespace:
		return "storage"
	case t.InCategory(chroma.Keyword):
		return "keyword"
	case t == chroma.LiteralStringEscape || t == chroma.LiteralStringInterpol:
		return "escape"
	case t == chroma.LiteralStringRegex:
		return "escape"
	case t.InSubCategory(chroma.LiteralString):
		return "string"
	case t.InSubCategory(chroma.LiteralNumber):
		return "number"
	case t == chroma.NameFunction || t == chroma.NameFunctionMagic:
		return "function"
	case t == chroma.NameClass || t == chroma.NameNamespace || t == chroma.KeywordReserved:
		return "type"
	case t == chroma.NameBuiltin || t == chroma.NameBuiltinPseudo:
		return "builtin"
	case t == chroma.NameTag:
		return "tag"
	case t == chroma.NameAttribute || t == chroma.NameDecorator:
		return "attribute"
	case t == chroma.NameConstant || t == chroma.NameEntity:
		return "constant"
	case t.InCategory(chroma.Operator):
		return "operator"
	case t == chroma.Punctuation:
		return "punctuation"
	case t == chroma.GenericHeading || t == chroma.GenericSubheading:
		return "heading"
	case t == chroma.GenericInserted:
		return "string"
	case t == chroma.GenericDeleted || t == chroma.Error:
		return "invalid"
	case t == chroma.NameVariable || t == chroma.NameVariableInstance:
		return "variable"
	}
	return ""
}

// Highlight lexes text and returns spans per line.
func Highlight(lang, text string) [][]Span {
	lines := strings.Count(text, "\n") + 1
	out := make([][]Span, lines)
	if lang == PlainText || lang == "" {
		return out
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return out
	}
	lexer = chroma.Coalesce(lexer)
	it, err := lexer.Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: true}, text)
	if err != nil {
		return out
	}
	line, col := 0, 0
	for tok := it(); tok != chroma.EOF; tok = it() {
		role := roleFor(tok.Type)
		v := tok.Value
		for len(v) > 0 && line < lines {
			nl := strings.IndexByte(v, '\n')
			seg := v
			if nl >= 0 {
				seg = v[:nl]
			}
			if role != "" && len(seg) > 0 {
				out[line] = append(out[line], Span{Start: col, End: col + len(seg), Role: role})
			}
			if nl < 0 {
				col += len(seg)
				break
			}
			line++
			col = 0
			v = v[nl+1:]
		}
	}
	return out
}

// Highlighter re-lexes a document in the background and publishes the
// latest spans; readers get the most recent result (possibly stale by a
// keystroke, which is invisible in practice).
type Highlighter struct {
	mu      sync.Mutex
	spans   [][]Span
	version int
	pending chan job
	notify  func()
}

type job struct {
	lang    string
	text    string
	version int
}

// NewHighlighter starts a background lexer; notify runs after each update.
func NewHighlighter(notify func()) *Highlighter {
	h := &Highlighter{pending: make(chan job, 1), notify: notify}
	go h.loop()
	return h
}

func (h *Highlighter) loop() {
	for j := range h.pending {
		spans := Highlight(j.lang, j.text)
		h.mu.Lock()
		if j.version >= h.version {
			h.spans, h.version = spans, j.version
		}
		h.mu.Unlock()
		if h.notify != nil {
			h.notify()
		}
	}
}

// Update schedules re-highlighting (the newest request replaces any queued one).
func (h *Highlighter) Update(lang, text string, version int) {
	j := job{lang, text, version}
	select {
	case h.pending <- j:
	default:
		select {
		case <-h.pending:
		default:
		}
		h.pending <- j
	}
}

// Line returns spans for a line, if known.
func (h *Highlighter) Line(i int) []Span {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i < len(h.spans) {
		return h.spans[i]
	}
	return nil
}

// Version of the published spans.
func (h *Highlighter) Version() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

// Close stops the background goroutine.
func (h *Highlighter) Close() { close(h.pending) }
