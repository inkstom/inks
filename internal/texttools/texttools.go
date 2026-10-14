// Package texttools holds text transformations: bracket-aware block sorting
// (HCL, JSON, CSS…), alignment, and Unix-style line tools.
package texttools

import (
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// Bracket depth, ignoring strings, comments and heredocs.

type scanState struct {
	str     byte
	block   bool
	heredoc string
}

var heredocRe = regexp.MustCompile(`^<<-?\s*["']?(\w+)["']?\s*$`)

func depthDelta(line string, st *scanState) int {
	if st.heredoc != "" {
		if strings.TrimSpace(line) == st.heredoc {
			st.heredoc = ""
		}
		return 0
	}
	d := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if st.block {
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				st.block = false
				i++
			}
			continue
		}
		if st.str != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == st.str {
				st.str = 0
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' || c == '`':
			st.str = c
		case c == '#' || (c == '/' && i+1 < len(line) && line[i+1] == '/'):
			i = len(line)
		case c == '/' && i+1 < len(line) && line[i+1] == '*':
			st.block = true
			i++
		case c == '<' && i+1 < len(line) && line[i+1] == '<':
			if m := heredocRe.FindStringSubmatch(line[i:]); m != nil {
				st.heredoc = m[1]
				i = len(line)
			}
		case c == '{' || c == '[' || c == '(':
			d++
		case c == '}' || c == ']' || c == ')':
			d--
		}
	}
	if st.str != 0 && st.str != '`' {
		st.str = 0
	}
	return d
}

var commentRe = regexp.MustCompile(`^\s*(#|//|/\*|\*|--|;)`)

func isComment(l string) bool { return commentRe.MatchString(l) }
func isBlank(l string) bool   { return strings.TrimSpace(l) == "" }

type item struct {
	lines       []string
	header      string
	commentOnly bool
}

// splitItems groups lines into depth-0 items; comment lines attach to the
// next item; blank lines separate groups.
func splitItems(lines []string) [][]*item {
	groups := [][]*item{{}}
	st := &scanState{}
	var pending []string
	var current *item
	depth := 0
	addGroup := func() {
		if len(groups[len(groups)-1]) > 0 {
			groups = append(groups, []*item{})
		}
	}
	for _, line := range lines {
		if current != nil {
			current.lines = append(current.lines, line)
			depth += depthDelta(line, st)
			if depth <= 0 && st.heredoc == "" && !st.block {
				current = nil
				depth = 0
			}
			continue
		}
		if isBlank(line) {
			if len(pending) > 0 {
				groups[len(groups)-1] = append(groups[len(groups)-1], &item{lines: pending, commentOnly: true})
				pending = nil
			}
			addGroup()
			continue
		}
		if isComment(line) && !st.block {
			pending = append(pending, line)
			depthDelta(line, st)
			continue
		}
		it := &item{lines: append(pending, line), header: line}
		pending = nil
		groups[len(groups)-1] = append(groups[len(groups)-1], it)
		depth = depthDelta(line, st)
		if depth > 0 || st.heredoc != "" || st.block {
			current = it
		}
	}
	if len(pending) > 0 {
		groups[len(groups)-1] = append(groups[len(groups)-1], &item{lines: pending, commentOnly: true})
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// NaturalLess compares case-insensitively with numbers in numeric order.
func NaturalLess(a, b string) bool { return naturalCmp(a, b) < 0 }

func naturalCmp(a, b string) int {
	ar, br := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if unicode.IsDigit(ar[i]) && unicode.IsDigit(br[j]) {
			si := i
			for i < len(ar) && unicode.IsDigit(ar[i]) {
				i++
			}
			sj := j
			for j < len(br) && unicode.IsDigit(br[j]) {
				j++
			}
			na := strings.TrimLeft(string(ar[si:i]), "0")
			nb := strings.TrimLeft(string(br[sj:j]), "0")
			if len(na) != len(nb) {
				if len(na) < len(nb) {
					return -1
				}
				return 1
			}
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			continue
		}
		ca, cb := unicode.ToLower(ar[i]), unicode.ToLower(br[j])
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	return (len(ar) - i) - (len(br) - j)
}

func itemKey(it *item, keyRe *regexp.Regexp) string {
	if keyRe != nil {
		if m := keyRe.FindStringSubmatch(strings.Join(it.lines, "\n")); m != nil {
			if len(m) > 1 {
				return m[1]
			}
			return m[0]
		}
	}
	if it.header != "" {
		return strings.TrimSpace(it.header)
	}
	return strings.TrimSpace(it.lines[0])
}

func sortItems(items []*item, keyRe *regexp.Regexp, reverse bool) []*item {
	var tail []*item
	for len(items) > 0 && items[len(items)-1].commentOnly {
		tail = append([]*item{items[len(items)-1]}, tail...)
		items = items[:len(items)-1]
	}
	sorted := append([]*item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return naturalCmp(itemKey(sorted[i], keyRe), itemKey(sorted[j], keyRe)) < 0 })
	if reverse {
		for i, j := 0, len(sorted)-1; i < j; i, j = i+1, j-1 {
			sorted[i], sorted[j] = sorted[j], sorted[i]
		}
	}
	return append(sorted, tail...)
}

func splitLines(text string) ([]string, bool) {
	eol := strings.HasSuffix(text, "\n")
	if eol {
		text = text[:len(text)-1]
	}
	return strings.Split(text, "\n"), eol
}

func joinLines(lines []string, eol bool) string {
	s := strings.Join(lines, "\n")
	if eol {
		s += "\n"
	}
	return s
}

// SortBlocks sorts top-level blocks (e.g. Terraform resources). key is an
// optional regex whose first group is the sort key.
func SortBlocks(text, key string, reverse bool) (string, error) {
	var keyRe *regexp.Regexp
	if key != "" {
		var err error
		if keyRe, err = regexp.Compile("(?m)" + key); err != nil {
			return text, err
		}
	}
	lines, eol := splitLines(text)
	groups := splitItems(lines)
	var all []*item
	for _, g := range groups {
		all = append(all, g...)
	}
	items := sortItems(all, keyRe, reverse)
	multi := len(groups) > 1
	for _, it := range items {
		if len(it.lines) > 1 {
			multi = true
		}
	}
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = strings.Join(it.lines, "\n")
	}
	sep := "\n"
	if multi {
		sep = "\n\n"
	}
	out := strings.Join(parts, sep)
	if eol {
		out += "\n"
	}
	return out, nil
}

// SortBlockBody sorts the entries of a block body, keeping blank-line groups.
func SortBlockBody(text string) string {
	lines, eol := splitLines(text)
	var groups []string
	for _, g := range splitItems(lines) {
		var parts []string
		for _, it := range sortItems(g, nil, false) {
			parts = append(parts, strings.Join(it.lines, "\n"))
		}
		groups = append(groups, strings.Join(parts, "\n"))
	}
	out := strings.Join(groups, "\n\n")
	if eol {
		out += "\n"
	}
	return out
}

// EnclosingBlock returns the byte range of the lines inside the innermost
// {…} or […] around offset pos.
func EnclosingBlock(text string, pos int) (int, int, bool) {
	depth := 0
	for i := pos - 1; i >= 0; i-- {
		c := text[i]
		if c == '}' || c == ']' {
			depth++
		} else if c == '{' || c == '[' {
			if depth == 0 {
				d := 0
				for j := i + 1; j < len(text); j++ {
					cj := text[j]
					if cj == '{' || cj == '[' {
						d++
					} else if cj == '}' || cj == ']' {
						if d == 0 {
							innerStart := strings.IndexByte(text[i:], '\n')
							if innerStart < 0 {
								return 0, 0, false
							}
							innerStart += i + 1
							innerEnd := strings.LastIndexByte(text[:j], '\n') + 1
							if innerEnd <= innerStart {
								return 0, 0, false
							}
							return innerStart, innerEnd, true
						}
						d--
					}
				}
				return 0, 0, false
			}
			depth--
		}
	}
	return 0, 0, false
}

// AlignOn lines up the first delimiter in consecutive lines at the same
// indentation and depth (like terraform fmt for "=").
func AlignOn(text, delim string) string {
	lines := strings.Split(text, "\n")
	out := append([]string(nil), lines...)
	st := &scanState{}
	depth := 0
	type entry struct {
		i           int
		left, right string
		indent      string
		depth       int
	}
	var run []entry
	flush := func() {
		if len(run) > 1 {
			w := 0
			for _, r := range run {
				if len(r.left) > w {
					w = len(r.left)
				}
			}
			for _, r := range run {
				out[r.i] = r.left + strings.Repeat(" ", w-len(r.left)) + " " + delim + r.right
			}
		}
		run = nil
	}
	for i, line := range lines {
		before := depth
		idx := findDelimiter(line, delim)
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if idx > 0 && !isComment(line) {
			left := strings.TrimRight(line[:idx], " \t")
			right := " " + strings.TrimLeft(line[idx+len(delim):], " \t")
			if len(run) > 0 && (run[0].indent != indent || run[0].depth != before) {
				flush()
			}
			run = append(run, entry{i, left, right, indent, before})
		} else {
			flush()
		}
		depth += depthDelta(line, st)
		if depth != before {
			flush()
		}
	}
	flush()
	return strings.Join(out, "\n")
}

func findDelimiter(line, delim string) int {
	var str byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if str != 0 {
			if c == '\\' {
				i++
			} else if c == str {
				str = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			str = c
			continue
		}
		if strings.HasPrefix(line[i:], delim) {
			if delim == "=" {
				next := byte(0)
				if i+1 < len(line) {
					next = line[i+1]
				}
				prev := byte(0)
				if i > 0 {
					prev = line[i-1]
				}
				if next == '=' || next == '>' || strings.IndexByte("<>!=", prev) >= 0 && prev != 0 {
					continue
				}
			}
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Line tools

// SortLines sorts lines: mode is natural, case, numeric or length. column
// (1-based, 0 = whole line) picks a field split by delim (whitespace if "").
func SortLines(text, mode string, reverse bool, column int, delim string) string {
	lines, eol := splitLines(text)
	field := func(l string) string {
		if column <= 0 {
			return l
		}
		var parts []string
		if delim != "" {
			parts = strings.Split(l, delim)
		} else {
			parts = strings.Fields(l)
		}
		if column-1 < len(parts) {
			return parts[column-1]
		}
		return ""
	}
	num := func(s string) float64 {
		f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return f
	}
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := field(lines[i]), field(lines[j])
		switch mode {
		case "case":
			return a < b
		case "numeric":
			if num(a) != num(b) {
				return num(a) < num(b)
			}
			return naturalCmp(lines[i], lines[j]) < 0
		case "length":
			if len(a) != len(b) {
				return len(a) < len(b)
			}
			return naturalCmp(lines[i], lines[j]) < 0
		}
		return naturalCmp(a, b) < 0
	})
	if reverse {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	return joinLines(lines, eol)
}

// Uniq removes duplicate lines (adjacent only, like uniq, or all).
func Uniq(text string, adjacent, count, onlyDuplicates, ignoreCase bool) string {
	lines, eol := splitLines(text)
	type entry struct {
		line string
		n    int
	}
	norm := func(s string) string {
		if ignoreCase {
			return strings.ToLower(s)
		}
		return s
	}
	var out []*entry
	if adjacent {
		for _, l := range lines {
			if n := len(out); n > 0 && norm(out[n-1].line) == norm(l) {
				out[n-1].n++
			} else {
				out = append(out, &entry{l, 1})
			}
		}
	} else {
		seen := map[string]*entry{}
		for _, l := range lines {
			if e, ok := seen[norm(l)]; ok {
				e.n++
				continue
			}
			e := &entry{l, 1}
			seen[norm(l)] = e
			out = append(out, e)
		}
	}
	width := 1
	for _, e := range out {
		if w := len(strconv.Itoa(e.n)); w > width {
			width = w
		}
	}
	var res []string
	for _, e := range out {
		if onlyDuplicates && e.n < 2 {
			continue
		}
		if count {
			res = append(res, fmt.Sprintf("%*d %s", width+3, e.n, e.line))
		} else {
			res = append(res, e.line)
		}
	}
	return joinLines(res, eol)
}

var (
	cutDelimRe = regexp.MustCompile(`-d\s*(?:'([^']*)'|"([^"]*)"|(\S))`)
	cutFieldRe = regexp.MustCompile(`-f\s*([\d,\- ]+)`)
	cutCharRe  = regexp.MustCompile(`-c\s*([\d,\-]+)`)
)

// Cut selects fields like cut(1): "-d, -f1,3", "2-" (whitespace fields) or "-c1-10".
func Cut(text, spec string) string {
	lines, eol := splitLines(text)
	delim := ""
	hasDelim := false
	fields := strings.TrimSpace(spec)
	if m := cutDelimRe.FindStringSubmatch(fields); m != nil {
		delim = m[1] + m[2] + m[3]
		hasDelim = true
		fields = strings.Replace(fields, m[0], "", 1)
	}
	if m := cutFieldRe.FindStringSubmatch(fields); m != nil {
		fields = m[1]
	}
	chars := false
	if m := cutCharRe.FindStringSubmatch(spec); m != nil {
		fields = m[1]
		chars = true
	}
	type rng struct{ a, b int }
	var ranges []rng
	for _, r := range strings.Split(fields, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		a, b := 1, 0
		if i := strings.Index(r, "-"); i >= 0 {
			if i > 0 {
				a, _ = strconv.Atoi(r[:i])
			}
			if i+1 < len(r) {
				b, _ = strconv.Atoi(r[i+1:])
			} else {
				b = 1 << 30
			}
		} else {
			a, _ = strconv.Atoi(r)
			b = a
		}
		ranges = append(ranges, rng{a, b})
	}
	keep := func(i int) bool {
		for _, r := range ranges {
			if i >= r.a && i <= r.b {
				return true
			}
		}
		return false
	}
	pick := func(parts []string) []string {
		var out []string
		for i, p := range parts {
			if keep(i + 1) {
				out = append(out, p)
			}
		}
		return out
	}
	for i, l := range lines {
		switch {
		case chars:
			var out []rune
			for k, r := range []rune(l) {
				if keep(k + 1) {
					out = append(out, r)
				}
			}
			lines[i] = string(out)
		case hasDelim:
			lines[i] = strings.Join(pick(strings.Split(l, delim)), delim)
		default:
			lines[i] = strings.Join(pick(strings.Fields(l)), " ")
		}
	}
	return joinLines(lines, eol)
}

// Translate maps characters like tr(1): "a-z A-Z", "-d aeiou", "-s ' '".
func Translate(text, spec string) string {
	args := splitArgs(spec)
	expand := func(set string) []rune {
		set = strings.ReplaceAll(strings.ReplaceAll(set, `\n`, "\n"), `\t`, "\t")
		rs := []rune(set)
		var out []rune
		for i := 0; i < len(rs); i++ {
			if i+2 < len(rs) && rs[i+1] == '-' {
				for c := rs[i]; c <= rs[i+2]; c++ {
					out = append(out, c)
				}
				i += 2
			} else {
				out = append(out, rs[i])
			}
		}
		return out
	}
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch arg(0) {
	case "-d":
		del := map[rune]bool{}
		for _, r := range expand(arg(1)) {
			del[r] = true
		}
		return strings.Map(func(r rune) rune {
			if del[r] {
				return -1
			}
			return r
		}, text)
	case "-s":
		sq := map[rune]bool{}
		for _, r := range expand(arg(1)) {
			sq[r] = true
		}
		var sb strings.Builder
		var last rune = -1
		for _, r := range text {
			if sq[r] && r == last {
				continue
			}
			sb.WriteRune(r)
			last = r
		}
		return sb.String()
	}
	from, to := expand(arg(0)), expand(arg(1))
	m := map[rune]rune{}
	for i, r := range from {
		if len(to) == 0 {
			m[r] = -1
		} else if i < len(to) {
			m[r] = to[i]
		} else {
			m[r] = to[len(to)-1]
		}
	}
	return strings.Map(func(r rune) rune {
		if t, ok := m[r]; ok {
			return t
		}
		return r
	}, text)
}

// Grep keeps (or with invert, removes) lines matching pattern.
func Grep(text, pattern string, invert, ignoreCase bool) (string, error) {
	flags := ""
	if ignoreCase {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		re = regexp.MustCompile(flags + regexp.QuoteMeta(pattern))
	}
	lines, eol := splitLines(text)
	var out []string
	for _, l := range lines {
		if re.MatchString(l) != invert {
			out = append(out, l)
		}
	}
	return joinLines(out, eol), nil
}

func unescape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\n`, "\n"), `\t`, "\t")
}

// JoinWith joins lines with a separator.
func JoinWith(text, sep string) string {
	lines, eol := splitLines(text)
	out := strings.Join(lines, unescape(sep))
	if eol {
		out += "\n"
	}
	return out
}

// SplitOn splits text into lines at a delimiter.
func SplitOn(text, sep string) string {
	parts := strings.Split(text, unescape(sep))
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "\n")
}

// NumberLines prefixes line numbers.
func NumberLines(text string) string {
	lines, eol := splitLines(text)
	w := len(strconv.Itoa(len(lines)))
	for i, l := range lines {
		lines[i] = fmt.Sprintf("%*d  %s", w, i+1, l)
	}
	return joinLines(lines, eol)
}

// RemoveBlankLines drops empty lines; SqueezeBlankLines keeps at most one.
func RemoveBlankLines(text string) string {
	lines, eol := splitLines(text)
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return joinLines(out, eol)
}

var squeezeRe = regexp.MustCompile(`\n(?:[ \t]*\n){2,}`)

func SqueezeBlankLines(text string) string { return squeezeRe.ReplaceAllString(text, "\n\n") }

// Zip merges texts line by line (paste).
func Zip(texts []string, sep string) string {
	cols := make([][]string, len(texts))
	n := 0
	for i, t := range texts {
		cols[i] = strings.Split(strings.TrimSuffix(t, "\n"), "\n")
		if len(cols[i]) > n {
			n = len(cols[i])
		}
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		row := make([]string, len(cols))
		for k, c := range cols {
			if i < len(c) {
				row[k] = c[i]
			}
		}
		out[i] = strings.Join(row, unescape(sep))
	}
	return strings.Join(out, "\n")
}

// JoinOnField joins two texts on a key field (join).
func JoinOnField(a, b string, field int, delim string) string {
	split := func(l string) []string {
		if delim != "" {
			return strings.Split(l, delim)
		}
		return strings.Fields(l)
	}
	rest := func(p []string) []string {
		var out []string
		for i, v := range p {
			if i != field-1 {
				out = append(out, v)
			}
		}
		return out
	}
	right := map[string][]string{}
	for _, l := range strings.Split(strings.TrimSuffix(b, "\n"), "\n") {
		p := split(l)
		if field-1 < len(p) {
			if _, ok := right[p[field-1]]; !ok {
				right[p[field-1]] = rest(p)
			}
		}
	}
	sep := delim
	if sep == "" {
		sep = " "
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(a, "\n"), "\n") {
		p := split(l)
		if field-1 >= len(p) {
			continue
		}
		if r, ok := right[p[field-1]]; ok {
			row := append([]string{p[field-1]}, rest(p)...)
			out = append(out, strings.Join(append(row, r...), sep))
		}
	}
	return strings.Join(out, "\n")
}

// Sed applies s/pattern/replacement/flags commands separated by ";".
func Sed(text, script string) (string, error) {
	out := text
	for _, cmd := range splitSed(script) {
		cmd = strings.TrimSpace(cmd)
		if len(cmd) < 2 || cmd[0] != 's' {
			return text, fmt.Errorf("unsupported sed command: %s", cmd)
		}
		d := cmd[1]
		var parts []string
		var cur strings.Builder
		for i := 2; i < len(cmd); i++ {
			c := cmd[i]
			if c == '\\' && i+1 < len(cmd) && cmd[i+1] == d {
				cur.WriteByte(d)
				i++
				continue
			}
			if c == d {
				parts = append(parts, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		}
		parts = append(parts, cur.String())
		for len(parts) < 3 {
			parts = append(parts, "")
		}
		pat := strings.ReplaceAll(strings.ReplaceAll(parts[0], `\(`, "("), `\)`, ")")
		flags := "(?m)"
		if strings.ContainsAny(parts[2], "iI") {
			flags = "(?mi)"
		}
		re, err := regexp.Compile(flags + pat)
		if err != nil {
			return text, err
		}
		rep := regexp.MustCompile(`\\(\d)`).ReplaceAllString(parts[1], `$${$1}`)
		rep = regexp.MustCompile(`(^|[^\\])&`).ReplaceAllString(rep, `$1$${0}`)
		rep = strings.ReplaceAll(rep, `\&`, "&")
		rep = unescape(rep)
		if strings.Contains(parts[2], "g") {
			out = re.ReplaceAllString(out, rep)
		} else {
			done := false
			out = re.ReplaceAllStringFunc(out, func(m string) string {
				if done {
					return m
				}
				done = true
				return re.ReplaceAllString(m, rep)
			})
		}
	}
	return out, nil
}

func splitSed(script string) []string {
	var out []string
	start := 0
	for i := 0; i < len(script); i++ {
		if script[i] == ';' {
			rest := strings.TrimLeft(script[i+1:], " ")
			if len(rest) > 1 && rest[0] == 's' && !unicode.IsLetter(rune(rest[1])) {
				out = append(out, script[start:i])
				start = i + 1
			}
		}
	}
	return append(out, script[start:])
}

func splitArgs(s string) []string {
	re := regexp.MustCompile(`'([^']*)'|"([^"]*)"|(\S+)`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1]+m[2]+m[3])
	}
	return out
}

// Shuffle randomizes line order.
func Shuffle(text string) string {
	lines, eol := splitLines(text)
	rand.Shuffle(len(lines), func(i, j int) { lines[i], lines[j] = lines[j], lines[i] })
	return joinLines(lines, eol)
}

// Reverse reverses line order.
func Reverse(text string) string {
	lines, eol := splitLines(text)
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return joinLines(lines, eol)
}
