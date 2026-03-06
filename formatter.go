package gocondense

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"

	"golang.org/x/tools/go/ast/astutil"
)

// Config controls the behavior of the Go code formatter.
type Config struct {
	// MaxLen is the maximum line length before keeping constructs multi-line.
	// Lines exceeding this limit will not be condensed.
	// If 0, defaults to 80 characters.
	MaxLen int

	// TabWidth is the number of spaces that represent a tab character
	// when calculating line lengths.
	// If 0, defaults to 4 spaces.
	TabWidth int
}

// DefaultConfig provides a sensible default configuration with a maximum
// line length of 80 characters and a tab width of 4 spaces.
var DefaultConfig = &Config{
	MaxLen:   80,
	TabWidth: 4,
}

// Format formats Go source code using the default configuration.
// Returns the formatted source code or an error if parsing fails.
func Format(src []byte) ([]byte, error) {
	formatter := New(DefaultConfig)
	return formatter.Format(src)
}

// Formatter condenses Go code according to the specified configuration.
type Formatter struct {
	config *Config
}

// New creates a new formatter with the given configuration.
// If config is nil, DefaultConfig is used.
func New(config *Config) *Formatter {
	if config == nil {
		config = DefaultConfig
	}
	return &Formatter{config: config}
}

// Format processes Go source code and returns a condensed version.
// The formatter respects the configured limits, only condensing constructs
// that fit within the specified constraints.
// Returns the formatted source code or an error if parsing or formatting fails.
func (f *Formatter) Format(src []byte) ([]byte, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source: %w", err)
	}

	if ast.IsGenerated(file) {
		return src, nil
	}

	c := &condenser{
		config:    f.config,
		fset:      fset,
		file:      file,
		tokenFile: fset.File(file.Pos()),
		buf:       bytes.NewBuffer(make([]byte, 0, len(src))),
		parents:   make([]ast.Node, 0, 32),
	}

	astutil.Apply(file, c.applyPre, c.applyPost)

	c.buf.Reset()
	if err := format.Node(c.buf, fset, file); err != nil {
		return nil, fmt.Errorf("failed to format AST: %w", err)
	}

	return c.buf.Bytes(), nil
}
