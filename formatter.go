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

// Feature represents formatting capabilities that can be enabled or disabled.
// Multiple features can be combined using bitwise OR operations.
type Feature uint

// has reports whether a specific feature is enabled.
func (f Feature) has(flag Feature) bool { return f&flag != 0 }

const (
	// Declarations enables condensing of single-item declaration groups into simple declarations.
	// This converts declaration groups like:
	//   import (
	//       "fmt"
	//   )
	//
	//   var (
	//       x = 1
	//   )
	//
	//   const (
	//       Name = "value"
	//   )
	//
	//   type (
	//       ID int
	//   )
	// into:
	//   import "fmt"
	//
	//   var x = 1
	//
	//   const Name = "value"
	//
	//   type ID int
	Declarations Feature = 1 << iota

	// Types enables condensing of multi-line type declarations, including generic type parameters.
	// This converts type parameter lists like:
	//   func MyFunc[
	//       T any,
	//       U comparable,
	//   ]() {}
	// into:
	//   func MyFunc[T any, U comparable]() {}
	Types

	// Funcs enables condensing of multi-line function declarations, including parameters and return values.
	// This converts function signatures like:
	//   func Add(
	//       a int,
	//       b int,
	//   ) (
	//       result int,
	//   ) {
	//       return a + b
	//   }
	// into:
	//   func Add(a int, b int) (result int) {
	//       return a + b
	//   }
	Funcs

	// Literals enables condensing of multi-line function literals (anonymous functions).
	// This converts function literals like:
	//   add := func(
	//       x int,
	//       y int,
	//   ) int {
	//       return x + y
	//   }
	// into:
	//   add := func(x int, y int) int {
	//       return x + y
	//   }
	Literals

	// Calls enables condensing of multi-line function call expressions.
	// This converts function calls like:
	//   myFunction(
	//       arg1,
	//       arg2,
	//       arg3,
	//   )
	// into:
	//   myFunction(arg1, arg2, arg3)
	Calls

	// Structs enables condensing of multi-line struct literals with named fields.
	// This converts struct literals like:
	//   Person{
	//       Name: "John",
	//       Age:  30,
	//   }
	// into:
	//   Person{Name: "John", Age: 30}
	Structs

	// Slices enables condensing of multi-line slice and array literals.
	// This converts slice literals like:
	//   []string{
	//       "apple",
	//       "banana",
	//       "cherry",
	//   }
	// into:
	//   []string{"apple", "banana", "cherry"}
	Slices

	// Maps enables condensing of multi-line map literals.
	// This converts map literals like:
	//   map[string]int{
	//       "apple":  1,
	//       "banana": 2,
	//       "cherry": 3,
	//   }
	// into:
	//   map[string]int{"apple": 1, "banana": 2, "cherry": 3}
	Maps

	// All enables condensing of all supported constructs.
	All = Declarations | Types | Funcs | Literals | Calls | Structs | Slices | Maps
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

	// MaxKeyValue is the maximum number of key-value pairs allowed in
	// struct and map literals before keeping them multi-line.
	// If 0, defaults to 3 pairs.
	MaxKeyValue int

	// Enable specifies which formatting features are active.
	// Use combinations like (Declarations | Funcs) to enable specific features,
	// or All to enable everything.
	// If 0, all features are enabled by default.
	Enable Feature
}

// DefaultConfig provides a sensible default configuration.
// It enables all features with a maximum line length of 80 characters,
// tab width of 4 spaces, and up to 3 key-value pairs per line.
var DefaultConfig = &Config{
	MaxLen:      80,
	TabWidth:    4,
	MaxKeyValue: 3,
	Enable:      All,
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
// The formatter respects the configured limits and feature flags,
// only condensing constructs that fit within the specified constraints.
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

	editor := &condenser{
		config:    f.config,
		fset:      fset,
		file:      file,
		tokenFile: fset.File(file.Pos()),
		addLines:  map[ast.Node][2]int{},
	}

	astutil.Apply(file, editor.applyPre, editor.applyPost)

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, fmt.Errorf("failed to format AST: %w", err)
	}

	return buf.Bytes(), nil
}

func isComplexExpr(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.CompositeLit, *ast.FuncLit, *ast.CallExpr, *ast.InterfaceType:
		return true
	default:
		return false
	}
}
