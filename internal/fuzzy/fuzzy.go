// Package fuzzy scores subsequence matches for Goto Anything and the
// Command Palette, rewarding consecutive runs and word boundaries.
package fuzzy

import (
	"path"
	"strings"
	"unicode"
)

// Result of a match: score (higher is better) and matched rune indexes.
type Result struct {
	Score   float64
	Indexes []int
}

func boundary(t []rune, i int) bool {
	if i == 0 {
		return true
	}
	p, c := t[i-1], t[i]
	if strings.ContainsRune("/\\_-. :()[]", p) {
		return true
	}
	return unicode.IsLower(p) && unicode.IsUpper(c)
}

// Match returns how well query matches target, or ok=false.
func Match(query, target string) (Result, bool) {
	q := []rune(strings.ToLower(strings.ReplaceAll(query, " ", "")))
	if len(q) == 0 {
		return Result{}, true
	}
	orig := []rune(target)
	t := []rune(strings.ToLower(target))
	qo := []rune(strings.ReplaceAll(query, " ", ""))
	m, n := len(q), len(t)
	if m > n {
		return Result{}, false
	}
	k := 0
	for i := 0; i < n && k < m; i++ {
		if t[i] == q[k] {
			k++
		}
	}
	if k < m {
		return Result{}, false
	}
	const neg = -1e18
	score := make([][]float64, m)
	from := make([][]int, m)
	for i := range score {
		score[i] = make([]float64, n)
		from[i] = make([]int, n)
		for j := range score[i] {
			score[i][j] = neg
			from[i][j] = -1
		}
	}
	for i := 0; i < m; i++ {
		bestPrev, bestIdx := neg, -1
		for j := 0; j < n; j++ {
			if i > 0 && j >= 2 && score[i-1][j-2] > bestPrev {
				bestPrev, bestIdx = score[i-1][j-2], j-2
			}
			if t[j] != q[i] {
				continue
			}
			bonus := 1.0
			if boundary(orig, j) {
				bonus += 8
			}
			if i < len(qo) && orig[j] == qo[i] {
				bonus += 0.5
			}
			if i == 0 {
				score[i][j] = bonus - float64(j)*0.05
				continue
			}
			best, bi := neg, -1
			if j >= 1 && score[i-1][j-1] > neg {
				best, bi = score[i-1][j-1]+bonus+6, j-1
			}
			if bestPrev > neg && bestPrev+bonus-1 > best {
				best, bi = bestPrev+bonus-1, bestIdx
			}
			score[i][j], from[i][j] = best, bi
		}
	}
	best, bj := neg, -1
	for j := 0; j < n; j++ {
		if score[m-1][j] > best {
			best, bj = score[m-1][j], j
		}
	}
	if bj < 0 {
		return Result{}, false
	}
	idx := make([]int, m)
	for i, j := m-1, bj; i >= 0; i-- {
		idx[i] = j
		j = from[i][j]
	}
	return Result{Score: best - float64(n)*0.01, Indexes: idx}, true
}

// MatchPath prefers matches inside the file name of a slash path.
func MatchPath(query, rel string) (Result, bool) {
	full, ok := Match(query, rel)
	if !ok {
		return full, false
	}
	base := path.Base(rel)
	offset := len([]rune(rel)) - len([]rune(base))
	if b, ok := Match(query, base); ok && b.Score+12 > full.Score {
		for i := range b.Indexes {
			b.Indexes[i] += offset
		}
		b.Score += 12
		return b, true
	}
	return full, true
}
