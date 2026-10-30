package builderread

import "strings"

const maxDiffLines = 3000

// unifiedDiff returns a unified diff (3 lines of context) of two texts, or "" when they are equal or too large
// to diff cheaply. Inputs are already masked snapshot content.
func unifiedDiff(name, a, b string) string {
	if a == b {
		return ""
	}
	x, y := strings.Split(strings.TrimSuffix(a, "\n"), "\n"), strings.Split(strings.TrimSuffix(b, "\n"), "\n")
	if len(x) > maxDiffLines || len(y) > maxDiffLines {
		return ""
	}
	// LCS table over lines.
	l := make([][]int, len(x)+1)
	for i := range l {
		l[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else {
				l[i][j] = max(l[i+1][j], l[i][j+1])
			}
		}
	}
	type op struct {
		c    byte // ' ', '-', '+'
		text string
	}
	var ops []op
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			ops = append(ops, op{' ', x[i]})
			i++
			j++
		case j < len(y) && (i == len(x) || l[i][j+1] > l[i+1][j]):
			ops = append(ops, op{'+', y[j]})
			j++
		default:
			ops = append(ops, op{'-', x[i]})
			i++
		}
	}
	// Group changes into hunks with 3 lines of context.
	const ctx = 3
	var out strings.Builder
	out.WriteString("--- a/" + name + "\n+++ b/" + name + "\n")
	n := len(ops)
	aLine, bLine := make([]int, n+1), make([]int, n+1) // 1-based line of ops[k] in a and b
	ai, bi := 1, 1
	for k, o := range ops {
		aLine[k], bLine[k] = ai, bi
		if o.c != '+' {
			ai++
		}
		if o.c != '-' {
			bi++
		}
	}
	for k := 0; k < n; {
		if ops[k].c == ' ' {
			k++
			continue
		}
		start := max(k-ctx, 0)
		end, last := k, k
		for end < n {
			if ops[end].c != ' ' {
				last = end
			} else if end-last > 2*ctx {
				break
			}
			end++
		}
		stop := min(last+ctx+1, n)
		var as, bs int
		for _, o := range ops[start:stop] {
			if o.c != '+' {
				as++
			}
			if o.c != '-' {
				bs++
			}
		}
		out.WriteString("@@ -" + itoa(aLine[start]) + "," + itoa(as) + " +" + itoa(bLine[start]) + "," + itoa(bs) + " @@\n")
		for _, o := range ops[start:stop] {
			out.WriteByte(o.c)
			out.WriteString(o.text + "\n")
		}
		k = stop
	}
	return out.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// UnifiedDiff is the exported form for the change service, which diffs masked file content.
func UnifiedDiff(name, a, b string) string { return unifiedDiff(name, a, b) }
