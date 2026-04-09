package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

type edit struct {
	op   byte // '=', '-', '+'
	oldI int
	newI int
}

// unifiedDiff returns a unified diff between old and new content for filename.
func unifiedDiff(filename string, old, cur []byte) string {
	oldLines := splitLines(old)
	newLines := splitLines(cur)
	edits := myersDiff(oldLines, newLines)
	if len(edits) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", filepath.Join("a", filename), filepath.Join("b", filename))
	writeHunks(&b, edits, oldLines, newLines)
	return b.String()
}

const diffContextLines = 3

// writeHunks groups edits into unified-diff hunks and writes them to b.
func writeHunks(b *strings.Builder, edits []edit, oldLines, newLines []string) {
	i := 0
	for i < len(edits) {
		for i < len(edits) && edits[i].op == '=' {
			i++
		}
		if i >= len(edits) {
			break
		}

		start := i
		for start > 0 && edits[start-1].op == '=' && i-start+1 <= diffContextLines {
			start--
		}

		j := hunkEnd(edits, i)
		writeHunk(b, edits[start:j], oldLines, newLines)
		i = j
	}
}

// hunkEnd returns the end index of a hunk starting at the first change at pos i.
func hunkEnd(edits []edit, i int) int {
	j := i
	for {
		for j < len(edits) && edits[j].op != '=' {
			j++
		}
		end := j
		for end < len(edits) && edits[end].op == '=' && end-j < diffContextLines {
			end++
		}
		if end < len(edits) && edits[end].op != '=' {
			j = end
			continue
		}
		return end
	}
}

// writeHunk writes a single hunk header and lines to b.
func writeHunk(b *strings.Builder, hunkEdits []edit, oldLines, newLines []string) {
	var oldStart, oldCount, newStart, newCount int
	var lines []string
	for _, e := range hunkEdits {
		switch e.op {
		case '=':
			lines = append(lines, " "+oldLines[e.oldI])
			oldCount++
			newCount++
		case '-':
			lines = append(lines, "-"+oldLines[e.oldI])
			oldCount++
		case '+':
			lines = append(lines, "+"+newLines[e.newI])
			newCount++
		}
	}

	for _, e := range hunkEdits {
		if e.op != '+' {
			oldStart = e.oldI + 1
			break
		}
	}
	for _, e := range hunkEdits {
		if e.op != '-' {
			newStart = e.newI + 1
			break
		}
	}

	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
}

// myersDiff computes a minimal edit script between two line slices.
func myersDiff(oldLines, newLines []string) []edit {
	n, m := len(oldLines), len(newLines)
	total := n + m
	if total == 0 {
		return nil
	}

	vs := myersForward(oldLines, newLines, n, m, total)
	return myersBacktrack(vs, n, m)
}

// myersForward runs the forward phase of the Myers diff algorithm,
// returning the saved v-states for backtracking.
func myersForward(oldLines, newLines []string, n, m, total int) [][]int {
	v := make([]int, 2*total+1)
	vs := make([][]int, 0, total)

	for d := 0; d <= total; d++ {
		vc := make([]int, len(v))
		copy(vc, v)
		vs = append(vs, vc)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+total] < v[k+1+total]) {
				x = v[k+1+total]
			} else {
				x = v[k-1+total] + 1
			}
			y := x - k
			for x < n && y < m && oldLines[x] == newLines[y] {
				x++
				y++
			}
			v[k+total] = x
			if x >= n && y >= m {
				return vs
			}
		}
	}
	return vs
}

// myersBacktrack recovers the edit script from the saved v-states.
func myersBacktrack(vs [][]int, n, m int) []edit {
	total := n + m
	x, y := n, m
	var edits []edit
	for d := len(vs) - 1; d >= 0; d-- {
		vd := vs[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vd[k-1+total] < vd[k+1+total]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vd[prevK+total]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			edits = append(edits, edit{'=', x, y})
		}
		if d > 0 {
			if x > prevX {
				x--
				edits = append(edits, edit{'-', x, 0})
			} else {
				y--
				edits = append(edits, edit{'+', 0, y})
			}
		}
	}
	for i, j := 0, len(edits)-1; i < j; i, j = i+1, j-1 {
		edits[i], edits[j] = edits[j], edits[i]
	}
	return edits
}

// splitLines splits data into lines, stripping the trailing newline from each.
func splitLines(data []byte) []string {
	s := string(data)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
