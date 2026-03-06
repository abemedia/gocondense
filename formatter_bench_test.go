package gocondense_test

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/abemedia/gocondense"
)

func BenchmarkFormat(b *testing.B) {
	for _, n := range []int{100, 1000} {
		src, lines := generateSrc(n)
		b.Run(fmt.Sprintf("%d_LOC", lines), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))
			for b.Loop() {
				out, err := gocondense.Format(src)
				if err != nil {
					b.Fatal(err)
				}
				_ = out
			}
		})
	}
}

// generateSrc produces approximately n lines of uncondensed code.
func generateSrc(n int) ([]byte, int) {
	var b strings.Builder

	b.WriteString("package example\n\n")
	b.WriteString("import (\n\t\"fmt\"\n)\n\n")

	lines := strings.Count(b.String(), "\n")
	i := 0

	chunks := []string{
		// Single-item var group → condensable declaration.
		"var (\n\tv%d = 0\n)\n\n",
		// Single-item const group → condensable declaration.
		"const (\n\tC%d = \"label\"\n)\n\n",
		// Multi-line func params and return → condensable func signature.
		"func fn%d[\n\tT any,\n](\n\tx T,\n\ty string,\n) (\n\tstring,\n\terror,\n) {\n\treturn fmt.Sprint(x), nil\n}\n\n",
		// Multi-line call expression → condensable call.
		"var call%d = fmt.Sprintf(\n\t\"prefix\",\n\t\"arg\",\n)\n\n",
		// Multi-line slice literal → condensable slice.
		"var slice%d = []string{\n\t\"alpha\",\n\t\"beta\",\n\t\"gamma\",\n}\n\n",
		// Multi-line func literal → condensable func literal.
		"var lit%d = func(\n\tv string,\n) (\n\tstring,\n\terror,\n) {\n\treturn fmt.Sprint(v), nil\n}\n\n",
		// Unnecessary parentheses → condensable parens.
		"var paren%d = (v0)\n\n",
	}

	chunkLines := make([]int, len(chunks))
	for j, c := range chunks {
		chunkLines[j] = strings.Count(c, "\n")
	}

	for lines < n {
		ci, remaining := i%len(chunks), n-lines
		if chunkLines[ci] > remaining {
			ci = slices.Index(chunkLines, slices.MinFunc(chunkLines, func(a, b int) int {
				return cmp.Compare(max(a-remaining, remaining-a), max(b-remaining, remaining-b))
			}))
		}
		fmt.Fprintf(&b, chunks[ci], i)
		lines += chunkLines[ci]
		i++
	}

	return []byte(b.String()), lines
}
