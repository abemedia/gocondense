package gocondense

import (
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
)

// Feature represents a bitmask flag for enabling or disabling specific formatting features.
// Multiple features can be combined using bitwise OR operations.
type Feature uint

// has checks if a specific feature flag is enabled in this Feature set.
func (f Feature) has(flag Feature) bool { return f&flag != 0 }

const (
	// Imports enables condensing of multi-line import declarations into single-line imports.
	// This converts import blocks like:
	//   import (
	//       "fmt"
	//   )
	// into:
	//   import "fmt"
	Imports Feature = 1 << iota

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
	//   ) {}
	// into:
	//   func Add(a int, b int) (result int) {}
	Funcs

	// Literals enables condensing of multi-line function literals (anonymous functions).
	// This converts function literals like:
	//   func(
	//       x int,
	//       y int,
	//   ) int {
	//       return x + y
	//   }
	// into:
	//   func(x int, y int) int { return x + y }
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
	All = Imports | Types | Funcs | Literals | Calls | Structs | Slices | Maps
)

// Config holds the configuration settings for the Go code formatter.
// It controls when and how multi-line constructs should be condensed into single lines.
type Config struct {
	// MaxLen specifies the maximum line length (in characters) before keeping constructs multi-line.
	// Lines longer than this limit will not be condensed. Tab characters are expanded according
	// to TabWidth for length calculation.
	// If 0, the DefaultConfig.MaxLen value is used instead.
	MaxLen int

	// MaxItems specifies the maximum number of items (parameters, arguments, fields, etc.)
	// that can be condensed onto a single line. Set to 0 for no limit.
	// For example, with MaxItems=3, a function call with 4 arguments will remain multi-line.
	MaxItems int

	// TabWidth specifies the width of a tab character for line length calculations.
	// This affects how MaxLen is calculated when the source code contains tab characters.
	// If 0, the DefaultConfig.TabWidth value is used instead.
	TabWidth int

	// Enable specifies which formatting features are active using bitwise flags.
	// Use combinations like (Imports | Funcs) to enable specific features,
	// or All to enable everything.
	// If 0, the DefaultConfig.Enable value is used instead.
	Enable Feature

	// Override provides feature-specific configuration overrides that take precedence
	// over the global MaxLen and MaxItems settings. This allows fine-grained control,
	// such as allowing more items for function calls than for struct literals.
	Override map[Feature]ConfigOverride
}

// ConfigOverride allows specifying feature-specific configuration overrides.
// When a feature has an override defined, these values take precedence over
// the global Config settings for that specific feature type.
type ConfigOverride struct {
	// MaxLen overrides the global maximum line length for this specific feature.
	// If 0, the global Config.MaxLen value is used instead.
	MaxLen int

	// MaxItems overrides the global maximum item count for this specific feature.
	// If 0, the global Config.MaxItems value is used instead.
	MaxItems int
}

// DefaultConfig provides a sensible default configuration for the formatter.
// It enables all features with a maximum line length of 80 characters,
// no item limit, and tab width of 4 spaces.
var DefaultConfig = &Config{
	MaxLen:   80,
	MaxItems: 3,
	TabWidth: 4,
	Enable:   All,
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
// It maintains internal state for parsing and processing source code transformations.
type Formatter struct {
	config   *Config
	fset     *token.FileSet
	comments []*ast.CommentGroup
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
// The function parses the source code, identifies multi-line constructs that can
// be safely condensed based on the formatter's configuration, and applies the
// transformations. It runs multiple passes until no more changes can be made.
//
// The formatting respects the configured limits (MaxLen, MaxItems) and feature
// flags, ensuring that only enabled features are processed and that the resulting
// code doesn't exceed the specified constraints.
//
// Returns the formatted source code or an error if parsing or formatting fails.
func (f *Formatter) Format(src []byte) ([]byte, error) {
	f.fset = token.NewFileSet()

	maxPasses := 10 // Prevent infinite loops

	// Run multiple passes until no more changes are made.
	for range maxPasses {
		file, err := parser.ParseFile(f.fset, "", src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("failed to parse source: %w", err)
		}

		f.comments = file.Comments

		result := f.processFile(src, file)

		if bytes.Equal(src, result) {
			break // If no changes were made, we're done.
		}

		src = result
	}

	return format.Source(src)
}

// processFile walks the AST and applies transformations using string replacement.
//
//nolint:cyclop,gocognit
func (f *Formatter) processFile(src []byte, file *ast.File) []byte {
	var replacements []replacement

	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			return true
		}

		if f.fset.Position(node.Pos()).Line == f.fset.Position(node.End()).Line {
			return false // Skip nodes that are already single-line.
		}

		switch n := node.(type) {
		case *ast.GenDecl:
			switch n.Tok {
			case token.IMPORT:
				if f.config.Enable.has(Imports) {
					replacements = append(replacements, f.analyzeImportDecl(n)...)
				}
			case token.TYPE:
				if f.config.Enable.has(Types) {
					replacements = append(replacements, f.analyzeTypeDecl(n)...)
				}
			}
		case *ast.IndexListExpr:
			if f.config.Enable.has(Types) {
				replacements = append(replacements, f.analyzeIndexListExpr(n)...)
			}
		case *ast.FuncDecl:
			if f.config.Enable.has(Funcs) {
				replacements = append(replacements, f.analyzeFuncType(n.Type, Funcs)...)
			}
		case *ast.FuncLit:
			if f.config.Enable.has(Literals) {
				replacements = append(replacements, f.analyzeFuncType(n.Type, Literals)...)
			}
		case *ast.CallExpr:
			if f.config.Enable.has(Calls) {
				replacements = append(replacements, f.analyzeCallExpr(n)...)
			}
		case *ast.CompositeLit:
			if f.config.Enable.has(Structs) || f.config.Enable.has(Slices) || f.config.Enable.has(Maps) {
				replacements = append(replacements, f.analyzeCompositeLit(n)...)
			}
		}

		return true
	})

	replacements = f.processReplacements(src, replacements)

	// Sort by start position (descending for reverse application).
	slices.SortFunc(replacements, func(a, b replacement) int { return cmp.Compare(b.start, a.start) })

	for _, r := range replacements {
		if r.start >= 0 && r.end <= len(src) && r.start <= r.end {
			src = slices.Replace(src, r.start, r.end, r.text...)
		}
	}

	return src
}

// processReplacements validates, deduplicates and sorts replacement
// candidates. It ensures replacements meet configured constraints and resolves
// conflicts by preferring more specific replacements over broader ones.
//
//nolint:gocognit
func (f *Formatter) processReplacements(src []byte, replacements []replacement) []replacement {
	if len(replacements) == 0 {
		return replacements
	}

	tabWidth := cmp.Or(f.config.TabWidth, DefaultConfig.TabWidth)

	// visualLength calculates the visual length of a byte slice, accounting for tabs.
	visualLength := func(b []byte) int {
		return len(b) + (tabWidth-1)*bytes.Count(b, []byte{'\t'})
	}

	result := make([]replacement, 0, len(replacements))

Loop:
	for _, r := range replacements {
		maxLen := cmp.Or(f.config.Override[r.feature].MaxLen, f.config.MaxLen, DefaultConfig.MaxLen)
		maxItems := cmp.Or(f.config.Override[r.feature].MaxItems, f.config.MaxItems, DefaultConfig.MaxItems)

		if maxItems > 0 && r.items > maxItems {
			continue // Skip if too many items.
		}

		// Check line length constraints
		lineStart := bytes.LastIndexByte(src[:r.start], '\n') + 1
		lineEnd := r.end + bytes.IndexByte(src[r.end:], '\n')
		if lineEnd < r.end {
			lineEnd = len(src)
		}

		prefix := visualLength(src[lineStart:r.start])
		suffix := visualLength(src[r.end:lineEnd])
		lines := bytes.Split(r.text, []byte("\n"))

		for i, line := range lines {
			var currentLen int
			if i == 0 {
				currentLen += prefix
			}
			currentLen += visualLength(line)
			if i == len(lines)-1 {
				currentLen += suffix
			}
			if currentLen > maxLen {
				continue Loop // Skip if any line exceeds max length.
			}
		}

		// Check for overlaps with existing replacements
		for j, prev := range result {
			if (r.start >= prev.start && r.start < prev.end) ||
				(r.end > prev.start && r.end <= prev.end) ||
				(r.start <= prev.start && r.end >= prev.end) {
				// Keep the smaller (more specific) replacement
				if (r.end - r.start) < (prev.end - prev.start) {
					result[j] = r
				}
				continue Loop // Skip if overlaps with an existing replacement.
			}
		}

		result = append(result, r)
	}

	return result
}

type replacement struct {
	start, end int
	text       []byte
	feature    Feature
	items      int
}

// analyzeImportDecl analyzes import declarations for condensing.
func (f *Formatter) analyzeImportDecl(decl *ast.GenDecl) []replacement {
	if len(decl.Specs) != 1 {
		return nil // Only condense single imports
	}

	if f.hasCommentsInRange(decl.Pos(), decl.End()) {
		return nil
	}

	spec := decl.Specs[0].(*ast.ImportSpec)
	var buf bytes.Buffer
	buf.WriteString("import ")

	if spec.Name != nil {
		buf.WriteString(spec.Name.Name)
		buf.WriteString(" ")
	}

	buf.WriteString(spec.Path.Value)

	return []replacement{
		{
			start:   f.fset.Position(decl.Pos()).Offset,
			end:     f.fset.Position(decl.End()).Offset,
			text:    buf.Bytes(),
			feature: Imports,
			items:   1,
		},
	}
}

// analyzeTypeDecl analyzes type declarations for condensing.
func (f *Formatter) analyzeTypeDecl(decl *ast.GenDecl) []replacement {
	var replacements []replacement

	for _, spec := range decl.Specs {
		if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.TypeParams != nil {
			if r := f.analyzeFieldList(typeSpec.TypeParams, '[', ']', Types); r != nil {
				replacements = append(replacements, *r)
			}
		}
	}

	return replacements
}

// analyzeCompositeLit analyzes composite literals for condensing.
func (f *Formatter) analyzeCompositeLit(lit *ast.CompositeLit) []replacement {
	if f.hasCommentsInRange(lit.Pos(), lit.End()) {
		return nil
	}

	var feature Feature

	switch lit.Type.(type) {
	case *ast.MapType:
		feature = Maps
	case *ast.StructType: // Only catches anonymous structs.
		feature = Structs
	default:
		// Fall back to checking key-value elements.
		hasKeyValue := slices.ContainsFunc(lit.Elts, func(elt ast.Expr) bool {
			_, ok := elt.(*ast.KeyValueExpr)
			return ok
		})
		if hasKeyValue {
			feature = Structs
		} else {
			feature = Slices
		}
	}

	if !f.config.Enable.has(feature) {
		return nil
	}

	var buf bytes.Buffer

	if lit.Type != nil {
		buf.WriteString(f.exprToString(lit.Type))
	}

	buf.WriteString("{")
	for i, elt := range lit.Elts {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(f.exprToString(elt))
	}
	buf.WriteString("}")

	return []replacement{{
		start:   f.fset.Position(lit.Pos()).Offset,
		end:     f.fset.Position(lit.End()).Offset,
		text:    buf.Bytes(),
		feature: feature,
		items:   len(lit.Elts),
	}}
}

// analyzeFuncType analyzes function types for condensing.
func (f *Formatter) analyzeFuncType(funcType *ast.FuncType, feature Feature) []replacement {
	var replacements []replacement

	if funcType.TypeParams != nil && len(funcType.TypeParams.List) > 0 {
		if r := f.analyzeFieldList(funcType.TypeParams, '[', ']', feature); r != nil {
			replacements = append(replacements, *r)
		}
	}

	if funcType.Params != nil && len(funcType.Params.List) > 0 {
		if r := f.analyzeFieldList(funcType.Params, '(', ')', feature); r != nil {
			replacements = append(replacements, *r)
		}
	}

	if funcType.Results != nil && len(funcType.Results.List) > 0 {
		if r := f.analyzeFieldList(funcType.Results, '(', ')', feature); r != nil {
			replacements = append(replacements, *r)
		}
	}

	return replacements
}

// analyzeIndexListExpr analyzes index list expressions for condensing.
func (f *Formatter) analyzeIndexListExpr(expr *ast.IndexListExpr) []replacement {
	if f.hasCommentsInRange(expr.Pos(), expr.End()) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString(f.exprToString(expr.X))
	buf.WriteByte('[')
	for i, index := range expr.Indices {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(f.exprToString(index))
	}
	buf.WriteByte(']')

	return []replacement{{
		start:   f.fset.Position(expr.Pos()).Offset,
		end:     f.fset.Position(expr.End()).Offset,
		text:    buf.Bytes(),
		feature: Types, // Generic type instantiations are considered part of types.
		items:   len(expr.Indices),
	}}
}

// analyzeCallExpr analyzes call expressions for condensing.
func (f *Formatter) analyzeCallExpr(call *ast.CallExpr) []replacement {
	if f.hasCommentsInRange(call.Pos(), call.End()) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString(f.exprToString(call.Fun))
	buf.WriteString("(")

	for i, arg := range call.Args {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(f.exprToString(arg))
	}

	if call.Ellipsis.IsValid() {
		buf.WriteString("...")
	}

	buf.WriteString(")")

	return []replacement{{
		start:   f.fset.Position(call.Pos()).Offset,
		end:     f.fset.Position(call.End()).Offset,
		text:    buf.Bytes(),
		feature: Calls,
		items:   len(call.Args),
	}}
}

// analyzeFieldList provides generic analysis for any *ast.FieldList.
func (f *Formatter) analyzeFieldList(fieldList *ast.FieldList, opening, closing byte, feature Feature) *replacement {
	if fieldList == nil || len(fieldList.List) == 0 {
		return nil
	}

	start := f.fset.Position(fieldList.Pos())
	end := f.fset.Position(fieldList.End())

	if start.Line == end.Line {
		return nil // Check if already single-line.
	}

	if f.hasCommentsInRange(fieldList.Pos(), fieldList.End()) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteByte(opening)

	for i, field := range fieldList.List {
		if i > 0 {
			buf.WriteString(", ")
		}

		for j, name := range field.Names {
			if j > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(name.Name)
		}

		if field.Type != nil {
			if len(field.Names) > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString(f.exprToString(field.Type))
		}
	}

	buf.WriteByte(closing)

	return &replacement{
		start: start.Offset,
		end:   end.Offset, text: buf.Bytes(),
		feature: feature,
		items:   len(fieldList.List),
	}
}

// hasCommentsInRange checks if there are any comments within the given range.
func (f *Formatter) hasCommentsInRange(start, end token.Pos) bool {
	for _, cg := range f.comments {
		if cg.Pos() >= start && cg.End() <= end {
			return true
		}
	}
	return false
}

// exprToString converts an AST expression to its string representation.
func (f *Formatter) exprToString(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, f.fset, expr); err != nil {
		return "" // Return empty string on error
	}
	return buf.String()
}
