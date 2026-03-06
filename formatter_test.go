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

			if diff := cmp.Diff(want, got); diff != "" {
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
			name: "max_length_constraint",
			config: &gocondense.Config{
				MaxLen:   10, // very short
				TabWidth: 4,
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
			name: "tab_width_affects_condensing",
			config: &gocondense.Config{
				MaxLen:   50,
				TabWidth: 8,
			},
			// With TabWidth 8: indented call = 8 (tab) + 48 (rest) = 56, stays multi-line.
			input: `package main

import "fmt"

func greet(first, last string) string {
	return fmt.Sprintf(
		"Hello, %s %s!",
		first,
		last,
	)
}`,
			want: `package main

import "fmt"

func greet(first, last string) string {
	return fmt.Sprintf(
		"Hello, %s %s!",
		first,
		last,
	)
}
`,
		},
		{
			name: "tab_width_small_allows_condensing",
			config: &gocondense.Config{
				MaxLen:   50,
				TabWidth: 2,
			},
			// Same input but with TabWidth 2: indented call = 2 (tab) + 48 (rest) = 50, condenses.
			input: `package main

import "fmt"

func greet(first, last string) string {
	return fmt.Sprintf(
		"Hello, %s %s!",
		first,
		last,
	)
}`,
			want: `package main

import "fmt"

func greet(first, last string) string {
	return fmt.Sprintf("Hello, %s %s!", first, last)
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
			if diff := cmp.Diff(tt.want, string(got)); diff != "" {
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

func TestNewPanics(t *testing.T) {
	tests := []struct {
		name   string
		config *gocondense.Config
	}{
		{"negative_max_len", &gocondense.Config{MaxLen: -1}},
		{"negative_tab_width", &gocondense.Config{TabWidth: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected panic")
				}
			}()
			gocondense.New(tt.config)
		})
	}
}
