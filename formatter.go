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

var (
	defaultConfig    = Config{MaxLen: 80, TabWidth: 4}
	defaultFormatter = New(defaultConfig)
)

// Source formats Go source code using the default configuration.
// Returns the formatted source code or an error if parsing fails.
func Source(src []byte) ([]byte, error) {
	return defaultFormatter.Source(src)
}

// Formatter condenses Go code according to the specified configuration.
type Formatter struct {
	config Config
}

// New creates a new formatter with the given configuration.
// Zero fields are replaced with their [Config] defaults.
func New(config Config) *Formatter {
	if config.MaxLen < 0 || config.TabWidth < 0 {
		panic("gocondense: MaxLen and TabWidth must not be negative")
	}
	if config.MaxLen == 0 {
		config.MaxLen = defaultConfig.MaxLen
	}
	if config.TabWidth == 0 {
		config.TabWidth = defaultConfig.TabWidth
	}
	return &Formatter{config: config}
}

// Source processes Go source code and returns a condensed version.
// The formatter respects the configured limits, only condensing constructs
// that fit within the specified constraints.
// Returns the formatted source code or an error if parsing or formatting fails.
func (f *Formatter) Source(src []byte) ([]byte, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source: %w", err)
	}

	f.File(fset, file)

	buf := bytes.NewBuffer(make([]byte, 0, len(src)))
	if err := format.Node(buf, fset, file); err != nil {
		return nil, fmt.Errorf("failed to format AST: %w", err)
	}

	return buf.Bytes(), nil
}

// File condenses the given AST file in-place. The caller is responsible for
// parsing and for rendering the result (e.g. via format.Node).
func (f *Formatter) File(fset *token.FileSet, file *ast.File) {
	c := &condenser{
		maxLen:    f.config.MaxLen,
		tabWidth:  f.config.TabWidth,
		fset:      fset,
		file:      file,
		tokenFile: fset.File(file.Pos()),
		buf:       bytes.NewBuffer(make([]byte, 0, 4096)),
		parents:   make([]ast.Node, 0, 32),
	}

	astutil.Apply(file, c.applyPre, c.applyPost)
}
