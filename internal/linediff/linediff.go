// Package linediff compares two texts line by line (Myers' O(ND) algorithm)
// and classifies the lines of the new text for a gutter: added, modified, or
// next to a deletion.
package linediff

// Mark is the gutter state of one line of the new text.
type Mark byte

const (
	None     Mark = iota
	Added         // line is new
	Modified      // line replaced an old one
	Deleted       // old lines were removed just above this line
)

// Op is an edit script step: Equal, Insert (from b) or Delete (from a).
type Op struct {
	Kind byte // '=', '+', '-'
	A, B int  // line index in a (for '=' and '-') and b (for '=' and '+')
}

// Diff returns an edit script turning a into b. maxCost bounds the work
// (edit distance); beyond it the remainder is reported as replaced.
func Diff(a, b []string, maxCost int) []Op {
	// Trim the common prefix and suffix: most edits are local.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var ops []Op
	for i := 0; i < pre; i++ {
		ops = append(ops, Op{'=', i, i})
	}
	ops = append(ops, myers(a[pre:len(a)-suf], b[pre:len(b)-suf], pre, pre, maxCost)...)
	for i := 0; i < suf; i++ {
		ops = append(ops, Op{'=', len(a) - suf + i, len(b) - suf + i})
	}
	return ops
}

func myers(a, b []string, offA, offB, maxCost int) []Op {
	n, m := len(a), len(b)
	if n == 0 || m == 0 || (maxCost > 0 && n+m > 2*maxCost && n*m > 4_000_000) {
		return replaceAll(n, m, offA, offB)
	}
	// Intern lines so comparisons are integer compares.
	ids := map[string]int{}
	ia := make([]int, n)
	ib := make([]int, m)
	for i, s := range a {
		id, ok := ids[s]
		if !ok {
			id = len(ids)
			ids[s] = id
		}
		ia[i] = id
	}
	for i, s := range b {
		id, ok := ids[s]
		if !ok {
			id = len(ids)
			ids[s] = id
		}
		ib[i] = id
	}
	max := n + m
	if maxCost > 0 && maxCost < max {
		max = maxCost
	}
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	found := false
	for d := 0; d <= max && !found; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && ia[x] == ib[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		return replaceAll(n, m, offA, offB)
	}
	// Backtrack.
	var rev []Op
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		vv := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && vv[off+k-1] < vv[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := vv[off+pk]
		py := px - pk
		for x > px && y > py {
			x--
			y--
			rev = append(rev, Op{'=', offA + x, offB + y})
		}
		if d > 0 {
			if x == px {
				y--
				rev = append(rev, Op{'+', offA + x, offB + y})
			} else {
				x--
				rev = append(rev, Op{'-', offA + x, offB + y})
			}
		}
	}
	ops := make([]Op, len(rev))
	for i := range rev {
		ops[i] = rev[len(rev)-1-i]
	}
	return ops
}

func replaceAll(n, m, offA, offB int) []Op {
	var ops []Op
	for i := 0; i < n; i++ {
		ops = append(ops, Op{'-', offA + i, offB})
	}
	for j := 0; j < m; j++ {
		ops = append(ops, Op{'+', offA + n, offB + j})
	}
	return ops
}

// Marks classifies each line of b against a.
func Marks(a, b []string) []Mark {
	marks := make([]Mark, len(b))
	ops := Diff(a, b, 4000)
	for i := 0; i < len(ops); {
		if ops[i].Kind == '=' {
			i++
			continue
		}
		// A run of deletions and insertions.
		dels, ins := 0, []int{}
		at := ops[i].B
		for i < len(ops) && ops[i].Kind != '=' {
			if ops[i].Kind == '-' {
				dels++
			} else {
				ins = append(ins, ops[i].B)
			}
			i++
		}
		for k, line := range ins {
			if k < dels {
				marks[line] = Modified
			} else {
				marks[line] = Added
			}
		}
		if len(ins) == 0 && dels > 0 {
			if at < len(marks) {
				if marks[at] == None {
					marks[at] = Deleted
				}
			} else if len(marks) > 0 && marks[len(marks)-1] == None {
				marks[len(marks)-1] = Deleted
			}
		}
	}
	return marks
}
