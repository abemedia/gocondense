package gocondense_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/abemedia/gocondense"
)

var update = flag.Bool("update", false, "update .golden files")

func TestFormat(t *testing.T) {
	matches, err := filepath.Glob("testdata/*.input")
	if err != nil {
		t.Fatal(err)
	}

	for _, inputFile := range matches {
		base := strings.TrimSuffix(inputFile, ".input")
		goldenFile := base + ".golden"

		name := filepath.Base(base)
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile(inputFile)
			if err != nil {
				t.Fatalf("failed to read input file %s: %v", inputFile, err)
			}

			got, err := gocondense.Format(input)
			if err != nil {
				t.Fatalf("failed to format %s: %v", inputFile, err)
			}

			if *update { // Update golden file
				if err := os.WriteFile(goldenFile, got, 0o600); err != nil {
					t.Fatalf("failed to update golden file %s: %v", goldenFile, err)
				}
				return
			}

			want, err := os.ReadFile(goldenFile)
			if err != nil {
				t.Fatalf("failed to read golden file %s: %v", goldenFile, err)
			}

			if diff := cmp.Diff(got, want); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func TestFormatWithConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *gocondense.Config
		input  string
		want   string
	}{
		{
			name: "disabled_imports",
			config: &gocondense.Config{
				MaxLen:   120,
				TabWidth: 4,
				Enable:   gocondense.All &^ gocondense.Imports,
			},
			input: `package main

import (
	"fmt"
)

func add(
	a, b int,
) int {
	return a + b
}`,
			want: `package main

import (
	"fmt"
)

func add(a, b int) int {
	return a + b
}
`,
		},
		{
			name: "disabled_functions",
			config: &gocondense.Config{
				MaxLen:   120,
				TabWidth: 4,
				Enable:   gocondense.All &^ gocondense.Funcs,
			},
			input: `package main

import (
	"fmt"
)

func add(
	a, b int,
) int {
	return a + b
}`,
			want: `package main

import "fmt"

func add(
	a, b int,
) int {
	return a + b
}
`,
		},
		{
			name: "max_length_constraint",
			config: &gocondense.Config{
				MaxLen:   20, // very short
				TabWidth: 4,
				Enable:   gocondense.All,
			},
			input: `package main

func add(
	a, b int,
) int {
	return a + b
}`,
			want: `package main

func add(
	a, b int,
) int {
	return a + b
}
`,
		},
		{
			name: "max_items_constraint_global",
			config: &gocondense.Config{
				MaxLen:   80,
				MaxItems: 2,
				Enable:   gocondense.All,
			},
			input: `package main

func main() {
	myFunc(
		"a",
		"b",
		"c",
	)
}
`,
			want: `package main

func main() {
	myFunc(
		"a",
		"b",
		"c",
	)
}
`,
		},
		{
			name: "max_items_override_allows",
			config: &gocondense.Config{
				MaxLen:   80,
				MaxItems: 2,
				Enable:   gocondense.All,
				Override: map[gocondense.Feature]gocondense.ConfigOverride{
					gocondense.Calls: {MaxItems: 3},
				},
			},
			input: `package main

func main() {
	myFunc(
		"a",
		"b",
		"c",
	)
}
`,
			want: `package main

func main() {
	myFunc("a", "b", "c")
}
`,
		},
		{
			name: "maps_condensing",
			config: &gocondense.Config{
				MaxLen:   80,
				MaxItems: 0,
				Enable:   gocondense.Maps,
			},
			input: `package main

func main() {
	data := map[string]int{
		"apple":  1,
		"banana": 2,
		"cherry": 3,
	}
}
`,
			want: `package main

func main() {
	data := map[string]int{"apple": 1, "banana": 2, "cherry": 3}
}
`,
		},
		{
			name: "maps_disabled",
			config: &gocondense.Config{
				MaxLen:   80,
				MaxItems: 0,
				Enable:   gocondense.All &^ gocondense.Maps,
			},
			input: `package main

func main() {
	data := map[string]int{
		"apple":  1,
		"banana": 2,
		"cherry": 3,
	}
}
`,
			want: `package main

func main() {
	data := map[string]int{
		"apple":  1,
		"banana": 2,
		"cherry": 3,
	}
}
`,
		},
		{
			name: "max_items_override_prevents",
			config: &gocondense.Config{
				MaxLen:   80,
				MaxItems: 4,
				Enable:   gocondense.All,
				Override: map[gocondense.Feature]gocondense.ConfigOverride{
					gocondense.Calls: {MaxItems: 2},
				},
			},
			input: `package main

func main() {
	myFunc(
		"a",
		"b",
		"c",
	)
}
`,
			want: `package main

func main() {
	myFunc(
		"a",
		"b",
		"c",
	)
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := gocondense.New(tt.config)
			got, err := formatter.Format([]byte(tt.input))
			if err != nil {
				t.Fatalf("failed to format: %v", err)
			}
			if diff := cmp.Diff(string(got), tt.want); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func TestErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "invalid_syntax",
			input:   "package main\n\nfunc main() {\n\treturn\n", // missing closing brace
			wantErr: true,
		},
		{
			name:  "empty_input",
			input: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gocondense.Format([]byte(tt.input))
			if err == nil && tt.wantErr {
				t.Error("expected error for invalid syntax")
			}
		})
	}
}
