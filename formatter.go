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

// Feature represents a bitmask flag for enabling or disabling specific formatting features.
// Multiple features can be combined using bitwise OR operations.
type Feature uint

// has checks if a specific feature flag is enabled in this Feature set.
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

// Config holds the configuration settings for the Go code formatter.
// It controls when and how multi-line constructs should be condensed into single lines.
type Config struct {
	// MaxLen specifies the maximum line length (in characters) before keeping constructs multi-line.
	// Lines longer than this limit will not be condensed. Tab characters are expanded according
	// to TabWidth for length calculation.
	// If 0, the DefaultConfig.MaxLen value is used instead.
	MaxLen int

	// TabWidth specifies the width of a tab character for line length calculations.
	// This affects how MaxLen is calculated when the source code contains tab characters.
	// If 0, the DefaultConfig.TabWidth value is used instead.
	TabWidth int

	// MaxKeyValue specifies the maximum number of key-value pairs allowed before keeping
	// constructs multi-line. This applies to map literals and struct literals with named fields.
	// Key-value expressions with more pairs than this limit will not be condensed.
	// If 0, the DefaultConfig.MaxKeyValue value is used instead.
	MaxKeyValue int

	// Enable specifies which formatting features are active using bitwise flags.
	// Use combinations like (Declarations | Funcs) to enable specific features,
	// or All to enable everything.
	// If 0, the DefaultConfig.Enable value is used instead.
	Enable Feature
}

// DefaultConfig provides a sensible default configuration for the formatter.
// It enables all features with a maximum line length of 80 characters
// and tab width of 4 spaces.
var DefaultConfig = &Config{
	MaxLen:      80,
	TabWidth:    4,
	MaxKeyValue: 3,
	Enable:      All,
}

// Format is a convenience function that formats the given Go source code
// using the default configuration. This is equivalent to calling:
//
//	New(DefaultConfig).Format(src)
//
// The function returns the formatted source code or an error if the
// source code cannot be parsed or formatted.
func Format(src []byte) ([]byte, error) {
	formatter := New(DefaultConfig)
	return formatter.Format(src)
}

// Formatter handles the Go code condensing process using the specified configuration.
type Formatter struct {
	config *Config
}

// New creates a new formatter instance with the given configuration.
// If config is nil, DefaultConfig will be used instead.
// The returned formatter can be reused for multiple Format calls.
func New(config *Config) *Formatter {
	if config == nil {
		config = DefaultConfig
	}
	return &Formatter{config: config}
}

// Format processes the given Go source code and returns the condensed version.
// The function parses the source code, traverses the AST to edit nodes in-place
// for condensation, then uses format.Node to print the modified AST.
//
// The formatting respects the configured limits (MaxLen, MaxItems) and feature
// flags, ensuring that only enabled features are processed and that the resulting
// code doesn't exceed the specified constraints.
//
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
		replaced:  map[ast.Node]ast.Node{},
	}

	astutil.Apply(file, editor.applyPre, nil)

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
