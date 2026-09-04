package hooks

import (
	"fmt"
	"strings"
)

// A small dependency-free line diff used by ChangePlan. It is kept separate
// so callers can rely on the plan's diff without coupling configuration
// surgery to a command-line renderer.

type diffKind byte

const (
	diffKeep   diffKind = ' '
	diffDelete diffKind = '-'
	diffInsert diffKind = '+'
)

type diffOp struct {
	Kind  diffKind
	Text  string
	aLine int
	bLine int
}

// diffLines produces the shortest edit script between a and b using an LCS
// dynamic program. Settings files are small enough that the straightforward
// O(len(a)*len(b)) table is appropriate.
func diffLines(a, b []string) []diffOp {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
				continue
			}
			if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var ops []diffOp
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{Kind: diffKeep, Text: a[i], aLine: i + 1, bLine: j + 1})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{Kind: diffDelete, Text: a[i], aLine: i + 1})
			i++
		default:
			ops = append(ops, diffOp{Kind: diffInsert, Text: b[j], bLine: j + 1})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{Kind: diffDelete, Text: a[i], aLine: i + 1})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{Kind: diffInsert, Text: b[j], bLine: j + 1})
	}
	return ops
}

func diffStat(ops []diffOp) (added, deleted int) {
	for _, op := range ops {
		switch op.Kind {
		case diffInsert:
			added++
		case diffDelete:
			deleted++
		}
	}
	return added, deleted
}

// unifiedDiff renders a unified diff with ctx lines of context. Identical
// inputs produce an empty string.
func unifiedDiff(a, b []string, aLabel, bLabel string, ctx int) string {
	if ctx < 0 {
		ctx = 0
	}
	ops := diffLines(a, b)
	if added, deleted := diffStat(ops); added == 0 && deleted == 0 {
		return ""
	}

	show := make([]bool, len(ops))
	for i, op := range ops {
		if op.Kind == diffKeep {
			continue
		}
		lo, hi := max(0, i-ctx), min(len(ops)-1, i+ctx)
		for index := lo; index <= hi; index++ {
			show[index] = true
		}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n", aLabel)
	fmt.Fprintf(&out, "+++ %s\n", bLabel)
	for i := 0; i < len(ops); {
		if !show[i] {
			i++
			continue
		}
		end := i
		for end < len(ops) && show[end] {
			end++
		}
		hunk := ops[i:end]
		aStart, aCount, bStart, bCount := 0, 0, 0, 0
		for _, op := range hunk {
			if op.Kind != diffInsert {
				if aCount == 0 {
					aStart = op.aLine
				}
				aCount++
			}
			if op.Kind != diffDelete {
				if bCount == 0 {
					bStart = op.bLine
				}
				bCount++
			}
		}
		if aCount == 0 {
			aStart = countBefore(ops[:i], diffInsert)
		}
		if bCount == 0 {
			bStart = countBefore(ops[:i], diffDelete)
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
		for _, op := range hunk {
			fmt.Fprintf(&out, "%c%s\n", byte(op.Kind), op.Text)
		}
		i = end
	}
	return out.String()
}

func countBefore(ops []diffOp, skip diffKind) int {
	n := 0
	for _, op := range ops {
		if op.Kind != skip {
			n++
		}
	}
	return n
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
