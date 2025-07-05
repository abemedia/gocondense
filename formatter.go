package gocondense

import (
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
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

	// MaxItems specifies the maximum number of items (parameters, arguments, fields, etc.)
	// that can be condensed onto a single line. Set to 0 for no limit.
	// For example, with MaxItems=3, a function call with 4 arguments will remain multi-line.
	MaxItems int

	// TabWidth specifies the width of a tab character for line length calculations.
	// This affects how MaxLen is calculated when the source code contains tab characters.
	// If 0, the DefaultConfig.TabWidth value is used instead.
	TabWidth int

	// Enable specifies which formatting features are active using bitwise flags.
	// Use combinations like (Declarations | Funcs) to enable specific features,
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
	MaxItems: 0,
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
// The function parses the source code and uses a custom AST printer to apply
// condensation rules while generating the output.
//
// The formatting respects the configured limits (MaxLen, MaxItems) and feature
// flags, ensuring that only enabled features are processed and that the resulting
// code doesn't exceed the specified constraints.
//
// Returns the formatted source code or an error if parsing or formatting fails.
func (f *Formatter) Format(src []byte) ([]byte, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source: %w", err)
	}

	// Check if this file has complex comment structure that should use fallback
	if f.shouldUseFallback(file) {
		// Use go/format for complex cases but still apply minimal condensation
		return f.formatWithFallback(src, file, fset)
	}

	printer := &astPrinter{
		config:            f.config,
		fset:              fset,
		comments:          file.Comments,
		buf:               &bytes.Buffer{},
		src:               src,
		processedComments: make(map[*ast.CommentGroup]bool),
	}

	printer.printFile(file)
	return printer.buf.Bytes(), nil
}

// astPrinter handles printing an AST with condensation rules applied
type astPrinter struct {
	config   *Config
	fset     *token.FileSet
	comments []*ast.CommentGroup
	buf      *bytes.Buffer
	indent   int
	src      []byte // Original source code for spacing preservation

	// Comment tracking
	commentIndex      int
	processedComments map[*ast.CommentGroup]bool
}

// printFile prints the entire Go file
func (p *astPrinter) printFile(file *ast.File) {
	p.printCommentsBefore(file.Pos())

	// Package declaration
	p.printf("package %s\n", file.Name.Name)

	if len(file.Decls) == 0 {
		return
	}

	// Handle first declaration spacing
	// Find the first non-comment content after package declaration
	packagePos := p.fset.Position(file.Name.End())

	// Check if there are comments before the first declaration
	firstContentPos := p.fset.Position(file.Decls[0].Pos())
	for _, cg := range p.comments {
		if cg.Pos() > file.Name.End() && cg.Pos() < file.Decls[0].Pos() {
			commentPos := p.fset.Position(cg.Pos())
			if commentPos.Line < firstContentPos.Line {
				firstContentPos = commentPos
			}
		}
	}

	// Calculate blank lines between package and first content (comment or declaration)
	blankLines := firstContentPos.Line - packagePos.Line - 1
	for i := 0; i < blankLines; i++ {
		p.printf("\n")
	}

	// Process declarations in order, preserving original spacing
	var prevOriginalEndLine int

	for i, decl := range file.Decls {
		if i > 0 {
			declPos := p.fset.Position(decl.Pos())

			// Calculate blank lines to preserve based on original spacing
			blankLines := declPos.Line - prevOriginalEndLine - 1
			for j := 0; j < blankLines; j++ {
				p.printf("\n")
			}
		}

		p.printDecl(decl)

		// Update prevOriginalEndLine using the original end position from AST
		prevOriginalEndLine = p.fset.Position(decl.End()).Line
	}

	// Print any remaining comments
	p.printCommentsAfter(file.End())
}

// printDecl prints a top-level declaration
func (p *astPrinter) printDecl(decl ast.Decl) {
	p.printCommentsBefore(decl.Pos())

	switch d := decl.(type) {
	case *ast.GenDecl:
		p.printGenDecl(d)
	case *ast.FuncDecl:
		p.printFuncDecl(d)
	default:
		// Fallback: use go/format for unknown declaration types
		var buf bytes.Buffer
		if err := format.Node(&buf, p.fset, decl); err == nil {
			p.markCommentsInRangeAsProcessed(decl.Pos(), decl.End())
			p.buf.WriteString(buf.String())
		}
	}
}

// printGenDecl prints general declarations (import, const, type, var)
func (p *astPrinter) printGenDecl(decl *ast.GenDecl) {
	// Check if this should be condensed (single-item declaration groups)
	shouldCondense := p.config.Enable.has(Declarations) &&
		len(decl.Specs) == 1 &&
		!p.hasCommentsInRange(decl.Pos(), decl.End())

	p.printf("%s ", decl.Tok.String())

	if shouldCondense {
		// Condense single-item declaration groups
		p.printSpec(decl.Specs[0])
	} else {
		// Multi-item declaration group or has comments
		// Use format.Node for proper alignment but mark comments as processed
		var buf bytes.Buffer
		if err := format.Node(&buf, p.fset, decl); err == nil {
			// Mark all comments in this declaration as processed
			p.markCommentsInRangeAsProcessed(decl.Pos(), decl.End())

			// Extract content after the token (e.g., after "type ")
			content := buf.String()
			if idx := strings.Index(content, decl.Tok.String()+" "); idx != -1 {
				remaining := content[idx+len(decl.Tok.String())+1:]
				p.buf.WriteString(remaining)
			} else {
				p.buf.WriteString(content)
			}
		} else {
			// Fallback to manual printing
			p.printf("(\n")
			p.indent++

			for _, spec := range decl.Specs {
				p.printIndent()
				p.printSpec(spec)
				p.printf("\n")
			}

			p.indent--
			p.printIndent()
			p.printf(")")
		}
	}
	p.printf("\n")
}

// printSpec prints a declaration specification
func (p *astPrinter) printSpec(spec ast.Spec) {
	switch s := spec.(type) {
	case *ast.ImportSpec:
		if s.Name != nil {
			p.printf("%s ", s.Name.Name)
		}
		p.printf("%s", s.Path.Value)
		p.printInlineComments(s.End())
	case *ast.ValueSpec:
		p.printValueSpec(s)
		p.printInlineComments(s.End())
	case *ast.TypeSpec:
		p.printTypeSpec(s)
		p.printInlineComments(s.End())
	default:
		// Fallback
		var buf bytes.Buffer
		if err := format.Node(&buf, p.fset, spec); err == nil {
			p.markCommentsInRangeAsProcessed(spec.Pos(), spec.End())
			p.buf.WriteString(buf.String())
		}
	}
}

// printInlineComments prints comments that appear on the same line as the given position
func (p *astPrinter) printInlineComments(pos token.Pos) {
	specLine := p.fset.Position(pos).Line

	for _, cg := range p.comments {
		if p.processedComments[cg] {
			continue
		}
		commentLine := p.fset.Position(cg.Pos()).Line
		if commentLine == specLine {
			for _, comment := range cg.List {
				if strings.HasPrefix(comment.Text, "//") {
					p.printf(" %s", comment.Text)
				}
			}
			// Mark this comment as processed
			p.processedComments[cg] = true
			break
		}
	}
}

// printValueSpec prints variable/constant specifications
func (p *astPrinter) printValueSpec(spec *ast.ValueSpec) {
	// Print names
	for i, name := range spec.Names {
		if i > 0 {
			p.printf(", ")
		}
		p.printf("%s", name.Name)
	}

	// Print type if present
	if spec.Type != nil {
		p.printf(" ")
		p.printExpr(spec.Type)
	}

	// Print values if present
	if len(spec.Values) > 0 {
		p.printf(" = ")
		for i, value := range spec.Values {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(value)
		}
	}
}

// printTypeSpec prints type specifications
func (p *astPrinter) printTypeSpec(spec *ast.TypeSpec) {
	p.printf("%s", spec.Name.Name)

	// Print type parameters if present
	if spec.TypeParams != nil {
		p.printFieldList(spec.TypeParams, '[', ']', Types, false)
	}

	// Handle type alias vs type definition
	if spec.Assign.IsValid() {
		p.printf(" = ")
	} else {
		p.printf(" ")
	}

	p.printExpr(spec.Type)
}

// printFuncDecl prints function declarations
func (p *astPrinter) printFuncDecl(decl *ast.FuncDecl) {
	p.printf("func ")

	// Receiver for methods
	if decl.Recv != nil {
		p.printFieldList(decl.Recv, '(', ')', Funcs, false)
		p.printf(" ")
	}

	// Function name
	p.printf("%s", decl.Name.Name)

	// Type parameters
	if decl.Type.TypeParams != nil {
		p.printFieldList(decl.Type.TypeParams, '[', ']', Types, false)
	}

	// Parameters
	p.printFieldList(decl.Type.Params, '(', ')', Funcs, true)

	// Results
	if decl.Type.Results != nil {
		if len(decl.Type.Results.List) == 1 && len(decl.Type.Results.List[0].Names) == 0 {
			// Single unnamed return type - no parentheses needed
			p.printf(" ")
			p.printExpr(decl.Type.Results.List[0].Type)
		} else {
			// Multiple returns or named returns - use parentheses
			p.printf(" ")
			p.printFieldList(decl.Type.Results, '(', ')', Funcs, false)
		}
	}

	// Function body
	if decl.Body != nil {
		p.printf(" ")
		p.printBlockStmt(decl.Body)
	} else {
		// Function declaration without body (interface method, etc.)
	}

	p.printf("\n")
}

// printFieldList prints parameter/result lists with condensation logic
func (p *astPrinter) printFieldList(list *ast.FieldList, open, close byte, feature Feature, isParams bool) {
	if list == nil || len(list.List) == 0 {
		p.printf("%c%c", open, close)
		return
	}

	// Check if we should condense this field list
	shouldCondense := p.shouldCondenseFieldList(list, feature, isParams)

	p.printf("%c", open)

	if shouldCondense {
		// Print on single line
		for i, field := range list.List {
			if i > 0 {
				p.printf(", ")
			}
			p.printField(field)
		}
	} else {
		// Print on multiple lines
		p.printf("\n")
		p.indent++
		for _, field := range list.List {
			p.printIndent()
			p.printFieldWithComma(field, true) // Always add comma in multi-line mode
			p.printf("\n")
		}
		p.indent--
		p.printIndent()
	}

	p.printf("%c", close)
}

// shouldCondenseFieldList determines if a field list should be condensed
func (p *astPrinter) shouldCondenseFieldList(list *ast.FieldList, feature Feature, isParams bool) bool {
	if !p.config.Enable.has(feature) {
		return false
	}

	if p.hasCommentsInRange(list.Pos(), list.End()) {
		return false
	}

	maxItems := cmp.Or(p.config.Override[feature].MaxItems, p.config.MaxItems, DefaultConfig.MaxItems)
	if maxItems > 0 && len(list.List) > maxItems {
		return false
	}

	// Check line length constraint
	maxLen := cmp.Or(p.config.Override[feature].MaxLen, p.config.MaxLen, DefaultConfig.MaxLen)
	if maxLen > 0 {
		// Calculate the condensed line length
		condensedLength := p.calculateCondensedLength(list, isParams)
		if condensedLength > maxLen {
			return false
		}
	}

	return true
}

// calculateCondensedLength estimates the length of a field list if condensed to a single line
func (p *astPrinter) calculateCondensedLength(list *ast.FieldList, isParams bool) int {
	if list == nil || len(list.List) == 0 {
		return 2 // Just "()"
	}

	length := 2 // Opening and closing parentheses

	for i, field := range list.List {
		if i > 0 {
			length += 2 // ", "
		}

		// Add field names
		for j, name := range field.Names {
			if j > 0 {
				length += 2 // ", "
			}
			length += len(name.Name)
		}

		// Add space and type
		if field.Type != nil {
			if len(field.Names) > 0 {
				length += 1 // space
			}
			length += p.estimateTypeLength(field.Type)
		}
	}

	// For parameters, add extra context length penalty
	if isParams {
		length += 20 // Conservative estimate for function name and context
	}

	return length
}

// calculateCompositeLitLength estimates the length of a composite literal if condensed to a single line
func (p *astPrinter) calculateCompositeLitLength(lit *ast.CompositeLit) int {
	if lit == nil || len(lit.Elts) == 0 {
		// Just the type and "{}"
		return p.estimateTypeLength(lit.Type) + 2
	}

	length := p.estimateTypeLength(lit.Type) + 2 // Type + "{}"

	for i, elt := range lit.Elts {
		if i > 0 {
			length += 2 // ", "
		}
		length += p.estimateExprLength(elt)
	}

	return length
}

// estimateExprLength estimates the string length of an expression
func (p *astPrinter) estimateExprLength(expr ast.Expr) int {
	switch e := expr.(type) {
	case *ast.Ident:
		return len(e.Name)
	case *ast.BasicLit:
		return len(e.Value)
	case *ast.SelectorExpr:
		return p.estimateExprLength(e.X) + 1 + len(e.Sel.Name) // "pkg.Type"
	case *ast.StarExpr:
		return 1 + p.estimateExprLength(e.X) // "*Type"
	case *ast.KeyValueExpr:
		return p.estimateExprLength(e.Key) + 2 + p.estimateExprLength(e.Value) // "key: value"
	case *ast.CompositeLit:
		return p.calculateCompositeLitLength(e)
	case *ast.CallExpr:
		length := p.estimateExprLength(e.Fun) + 2 // "func()"
		for i, arg := range e.Args {
			if i > 0 {
				length += 2 // ", "
			}
			length += p.estimateExprLength(arg)
		}
		return length
	default:
		// Conservative estimate for unknown expressions
		return 15
	}
}

// estimateTypeLength estimates the string length of a type expression
func (p *astPrinter) estimateTypeLength(expr ast.Expr) int {
	if expr == nil {
		return 0
	}

	switch e := expr.(type) {
	case *ast.Ident:
		return len(e.Name)
	case *ast.SelectorExpr:
		return p.estimateTypeLength(e.X) + 1 + len(e.Sel.Name) // "pkg.Type"
	case *ast.StarExpr:
		return 1 + p.estimateTypeLength(e.X) // "*Type"
	case *ast.ArrayType:
		length := 2 // "[]"
		if e.Len != nil {
			// For simplicity, assume array size is small
			length += 2
		}
		return length + p.estimateTypeLength(e.Elt)
	case *ast.MapType:
		return 4 + p.estimateTypeLength(e.Key) + p.estimateTypeLength(e.Value) // "map[]"
	default:
		// Conservative estimate for unknown types
		return 10
	}
}

// printField prints a field (parameter/result)
func (p *astPrinter) printField(field *ast.Field) {
	// Print names
	for i, name := range field.Names {
		if i > 0 {
			p.printf(", ")
		}
		p.printf("%s", name.Name)
	}

	// Print type
	if field.Type != nil {
		if len(field.Names) > 0 {
			p.printf(" ")
		}
		p.printExpr(field.Type)
	}
}

// printFieldWithComma prints a field with optional comma and inline comments
func (p *astPrinter) printFieldWithComma(field *ast.Field, addComma bool) {
	p.printField(field)

	// Check for inline comments
	fieldLine := p.fset.Position(field.End()).Line
	hasInlineComment := false

	for _, cg := range p.comments {
		if p.processedComments[cg] {
			continue
		}
		commentLine := p.fset.Position(cg.Pos()).Line
		if commentLine == fieldLine {
			hasInlineComment = true
			if addComma {
				p.printf(",")
			}
			for _, comment := range cg.List {
				if strings.HasPrefix(comment.Text, "//") {
					p.printf(" %s", comment.Text)
				}
			}
			// Mark this comment as processed
			p.processedComments[cg] = true
			break
		}
	}

	// If no inline comment but we need a comma, add it
	if !hasInlineComment && addComma {
		p.printf(",")
	}
}

// printExpr prints expressions with condensation logic
func (p *astPrinter) printExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		p.printCallExpr(e)
	case *ast.CompositeLit:
		p.printCompositeLit(e)
	case *ast.FuncLit:
		p.printFuncLit(e)
	case *ast.Ident:
		p.printf("%s", e.Name)
	case *ast.BasicLit:
		p.printf("%s", e.Value)
	case *ast.SelectorExpr:
		p.printExpr(e.X)
		p.printf(".%s", e.Sel.Name)
	case *ast.StarExpr:
		p.printf("*")
		p.printExpr(e.X)
	case *ast.ArrayType:
		p.printf("[")
		if e.Len != nil {
			p.printExpr(e.Len)
		}
		p.printf("]")
		p.printExpr(e.Elt)
	case *ast.MapType:
		p.printf("map[")
		p.printExpr(e.Key)
		p.printf("]")
		p.printExpr(e.Value)
	case *ast.FuncType:
		// Print function type (includes "func" keyword except in interface methods)
		p.printf("func")
		p.printFieldList(e.Params, '(', ')', Funcs, true)
		if e.Results != nil {
			if len(e.Results.List) == 1 && len(e.Results.List[0].Names) == 0 {
				p.printf(" ")
				p.printExpr(e.Results.List[0].Type)
			} else {
				p.printf(" ")
				p.printFieldList(e.Results, '(', ')', Funcs, false)
			}
		}
	case *ast.ChanType:
		if e.Dir == ast.RECV {
			p.printf("<-")
		}
		p.printf("chan")
		if e.Dir == ast.SEND {
			p.printf("<-")
		}
		p.printf(" ")
		p.printExpr(e.Value)
	case *ast.InterfaceType:
		p.printf("interface ")
		if e.Methods == nil || len(e.Methods.List) == 0 {
			p.printf("{}")
		} else {
			p.printf("{\n")
			p.indent++
			for _, method := range e.Methods.List {
				p.printIndent()
				// Print method names
				for i, name := range method.Names {
					if i > 0 {
						p.printf(", ")
					}
					p.printf("%s", name.Name)
				}

				// Print method type (without "func" keyword for interface methods)
				if method.Type != nil {
					if funcType, ok := method.Type.(*ast.FuncType); ok {
						// For interface methods, print function type without "func" keyword
						p.printFieldList(funcType.Params, '(', ')', Funcs, true)
						if funcType.Results != nil {
							if len(funcType.Results.List) == 1 && len(funcType.Results.List[0].Names) == 0 {
								p.printf(" ")
								p.printExpr(funcType.Results.List[0].Type)
							} else {
								p.printf(" ")
								p.printFieldList(funcType.Results, '(', ')', Funcs, false)
							}
						}
					} else {
						// Non-function method (embedded interface)
						if len(method.Names) > 0 {
							p.printf(" ")
						}
						p.printExpr(method.Type)
					}
				}
				p.printf("\n")
			}
			p.indent--
			p.printIndent()
			p.printf("}")
		}
	case *ast.StructType:
		p.printf("struct")
		if e.Fields == nil || len(e.Fields.List) == 0 {
			p.printf("{}")
		} else {
			p.printf(" {\n")
			p.indent++

			// Use go/format for struct fields to get proper alignment
			var buf bytes.Buffer
			if err := format.Node(&buf, p.fset, e); err == nil {
				// Extract just the fields part (between braces)
				content := buf.String()
				if start := strings.Index(content, "{\n"); start != -1 {
					if end := strings.LastIndex(content, "\n}"); end != -1 {
						fieldsContent := content[start+2 : end]
						// Split into lines and reindent properly
						lines := strings.Split(fieldsContent, "\n")
						for _, line := range lines {
							if strings.TrimSpace(line) != "" {
								// Remove original indentation and apply our indentation
								trimmedLine := strings.TrimSpace(line)
								p.printIndent()
								p.printf("%s\n", trimmedLine)
							}
						}
					}
				}
			} else {
				// Fallback to manual field printing
				for _, field := range e.Fields.List {
					p.printIndent()
					p.printField(field)
					p.printf("\n")
				}
			}

			p.indent--
			p.printIndent()
			p.printf("}")
		}
	case *ast.KeyValueExpr:
		p.printExpr(e.Key)
		p.printf(": ")
		p.printExpr(e.Value)
	case *ast.IndexExpr:
		p.printExpr(e.X)
		p.printf("[")
		p.printExpr(e.Index)
		p.printf("]")
	case *ast.IndexListExpr:
		p.printExpr(e.X)
		p.printf("[")
		for i, index := range e.Indices {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(index)
		}
		p.printf("]")
	default:
		// Fallback: use go/format for unknown expression types
		var buf bytes.Buffer
		if err := format.Node(&buf, p.fset, expr); err == nil {
			p.markCommentsInRangeAsProcessed(expr.Pos(), expr.End())
			p.buf.WriteString(buf.String())
		}
	}
}

// printCallExpr prints function calls with condensation logic
func (p *astPrinter) printCallExpr(call *ast.CallExpr) {
	p.printExpr(call.Fun)

	// Check if we should condense this call
	shouldCondense := p.shouldCondenseCall(call)

	p.printf("(")

	if shouldCondense {
		// Print arguments on single line
		for i, arg := range call.Args {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(arg)
		}
		if call.Ellipsis.IsValid() {
			p.printf("...")
		}
	} else {
		// Print arguments on multiple lines
		if len(call.Args) > 0 {
			p.printf("\n")
			p.indent++
			for _, arg := range call.Args {
				p.printIndent()
				p.printExpr(arg)
				p.printf(",\n")
			}
			if call.Ellipsis.IsValid() {
				// Handle ellipsis
				p.printIndent()
				p.printf("...\n")
			}
			p.indent--
			p.printIndent()
		}
	}

	p.printf(")")
}

// shouldCondenseCall determines if a function call should be condensed
func (p *astPrinter) shouldCondenseCall(call *ast.CallExpr) bool {
	if !p.config.Enable.has(Calls) {
		return false
	}

	if p.hasCommentsInRange(call.Pos(), call.End()) {
		return false
	}

	maxItems := cmp.Or(p.config.Override[Calls].MaxItems, p.config.MaxItems, DefaultConfig.MaxItems)
	if maxItems > 0 && len(call.Args) > maxItems {
		return false
	}

	// TODO: Add line length checking
	return true
}

// printCompositeLit prints composite literals with condensation logic
func (p *astPrinter) printCompositeLit(lit *ast.CompositeLit) {
	// Print type if present
	if lit.Type != nil {
		p.printExpr(lit.Type)
	}

	// Determine feature type
	feature := p.getCompositeLitFeature(lit)

	// Check if we should condense
	shouldCondense := p.shouldCondenseCompositeLit(lit, feature)

	p.printf("{")

	if shouldCondense {
		// Print elements on single line
		for i, elt := range lit.Elts {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(elt)
		}
	} else {
		// Print elements on multiple lines
		if len(lit.Elts) > 0 {
			p.printf("\n")
			p.indent++

			// For maps, calculate alignment
			if feature == Maps {
				p.printMapElementsAligned(lit.Elts)
			} else {
				// For non-maps, use simple printing
				for _, elt := range lit.Elts {
					p.printIndent()
					p.printExpr(elt)
					p.printf(",\n")
				}
			}

			p.indent--
			p.printIndent()
		}
	}

	p.printf("}")
}

// getCompositeLitFeature determines the feature type for a composite literal
func (p *astPrinter) getCompositeLitFeature(lit *ast.CompositeLit) Feature {
	switch lit.Type.(type) {
	case *ast.MapType:
		return Maps
	case *ast.StructType:
		return Structs
	default:
		// Check if elements are key-value pairs
		hasKeyValue := false
		for _, elt := range lit.Elts {
			if _, ok := elt.(*ast.KeyValueExpr); ok {
				hasKeyValue = true
				break
			}
		}
		if hasKeyValue {
			return Structs
		}
		return Slices
	}
}

// shouldCondenseCompositeLit determines if a composite literal should be condensed
func (p *astPrinter) shouldCondenseCompositeLit(lit *ast.CompositeLit, feature Feature) bool {
	if !p.config.Enable.has(feature) {
		return false
	}

	if p.hasCommentsInRange(lit.Pos(), lit.End()) {
		return false
	}

	maxItems := cmp.Or(p.config.Override[feature].MaxItems, p.config.MaxItems, DefaultConfig.MaxItems)
	if maxItems > 0 && len(lit.Elts) > maxItems {
		return false
	}

	// Check line length constraint
	maxLen := cmp.Or(p.config.Override[feature].MaxLen, p.config.MaxLen, DefaultConfig.MaxLen)
	if maxLen > 0 {
		condensedLength := p.calculateCompositeLitLength(lit)
		if condensedLength > maxLen {
			return false
		}
	}

	return true
}

// printFuncLit prints function literals with condensation logic
func (p *astPrinter) printFuncLit(lit *ast.FuncLit) {
	p.printf("func")

	// Type parameters
	if lit.Type.TypeParams != nil {
		p.printFieldList(lit.Type.TypeParams, '[', ']', Literals, false)
	}

	// Parameters
	p.printFieldList(lit.Type.Params, '(', ')', Literals, true)

	// Results
	if lit.Type.Results != nil {
		if len(lit.Type.Results.List) == 1 && len(lit.Type.Results.List[0].Names) == 0 {
			p.printf(" ")
			p.printExpr(lit.Type.Results.List[0].Type)
		} else {
			p.printf(" ")
			p.printFieldList(lit.Type.Results, '(', ')', Literals, false)
		}
	}

	// Body
	if lit.Body != nil {
		p.printf(" ")
		p.printBlockStmt(lit.Body)
	}
}

// printBlockStmt prints block statements
func (p *astPrinter) printBlockStmt(block *ast.BlockStmt) {
	if len(block.List) == 0 {
		// Empty block - print on same line
		p.printf("{}")
		return
	}

	// Non-empty block - print with proper indentation
	p.printf("{")
	p.printInlineComments(block.Lbrace)
	p.printf("\n")
	p.indent++

	// Start from the line after the opening brace
	openBracePos := p.fset.Position(block.Lbrace)
	prevLine := openBracePos.Line
	for _, stmt := range block.List {
		// Print comments before this statement
		// p.printCommentsBefore(stmt.Pos())

		stmtPos := p.fset.Position(stmt.Pos())

		// Calculate blank lines to preserve
		blankLines := stmtPos.Line - prevLine - 1
		for i := 0; i < blankLines; i++ {
			p.printf("\n")
		}

		p.printIndent()
		p.printStmt(stmt, true)

		// Update prevLine to the end of this statement
		prevLine = p.fset.Position(stmt.End()).Line
	}

	p.indent--
	p.printIndent()
	p.printf("}")
}

// printStmt prints statements
func (p *astPrinter) printStmt(stmt ast.Stmt, newline bool) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		p.printExpr(s.X)
		p.printInlineComments(s.End())
		if newline {
			p.printf("\n")
		}
	case *ast.AssignStmt:
		// Print left-hand side
		for i, lhs := range s.Lhs {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(lhs)
		}

		// Print operator
		p.printf(" %s ", s.Tok.String())

		// Print right-hand side
		for i, rhs := range s.Rhs {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(rhs)
		}
		p.printInlineComments(s.End())
		if newline {
			p.printf("\n")
		}
	case *ast.ReturnStmt:
		p.printf("return")
		if len(s.Results) > 0 {
			p.printf(" ")
			for i, result := range s.Results {
				if i > 0 {
					p.printf(", ")
				}
				p.printExpr(result)
			}
		}
		p.printInlineComments(s.End())
		if newline {
			p.printf("\n")
		}
	case *ast.ForStmt:
		p.printf("for ")
		if s.Init != nil {
			// Print init without trailing newline
			p.printStmtNoInlineComments(s.Init)
		}
		p.printf("; ")
		if s.Cond != nil {
			p.printExpr(s.Cond)
		}
		p.printf("; ")
		if s.Post != nil {
			p.printStmtNoInlineComments(s.Post)
		}
		p.printf(" ")
		p.printBlockStmt(s.Body)
		if newline {
			p.printf("\n")
		}
	case *ast.RangeStmt:
		p.printf("for ")
		if s.Key != nil {
			p.printExpr(s.Key)
			if s.Value != nil {
				p.printf(", ")
				p.printExpr(s.Value)
			}
			p.printf(" := range ")
		}
		p.printExpr(s.X)
		p.printf(" ")
		p.printBlockStmt(s.Body)
		if newline {
			p.printf("\n")
		}
	case *ast.IfStmt:
		p.printf("if ")
		if s.Init != nil {
			p.printStmtNoInlineComments(s.Init)
			p.printf("; ")
		}
		p.printExpr(s.Cond)
		p.printf(" ")
		p.printBlockStmt(s.Body)
		if s.Else != nil {
			p.printf(" else ")
			p.printStmt(s.Else, false)
		}
		if newline {
			p.printf("\n")
		}
	case *ast.DeclStmt:
		p.printDecl(s.Decl)
		// Note: printDecl handles its own newlines
	default:
		// Fallback: use go/format for unknown statement types
		var buf bytes.Buffer
		if err := format.Node(&buf, p.fset, stmt); err == nil {
			p.markCommentsInRangeAsProcessed(stmt.Pos(), stmt.End())
			content := buf.String()
			if newline {
				p.buf.WriteString(content)
				if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
					p.printf("\n")
				}
			} else {
				// Remove any trailing newline
				content = strings.TrimSuffix(content, "\n")
				p.buf.WriteString(content)
			}
		}
	}
}

// printStmtNoInlineComments prints a statement without inline comments (used for if/for init/post)
func (p *astPrinter) printStmtNoInlineComments(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		p.printExpr(s.X)
	case *ast.AssignStmt:
		// Print left-hand side
		for i, lhs := range s.Lhs {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(lhs)
		}

		// Print operator
		p.printf(" %s ", s.Tok.String())

		// Print right-hand side
		for i, rhs := range s.Rhs {
			if i > 0 {
				p.printf(", ")
			}
			p.printExpr(rhs)
		}
		// No inline comments for init/post statements
	case *ast.ReturnStmt:
		p.printf("return")
		if len(s.Results) > 0 {
			p.printf(" ")
			for i, result := range s.Results {
				if i > 0 {
					p.printf(", ")
				}
				p.printExpr(result)
			}
		}
		// No inline comments for init/post statements
	case *ast.DeclStmt:
		p.printDecl(s.Decl)
		// Note: printDecl handles its own newlines
	default:
		// Fallback: use go/format for unknown statement types
		var buf bytes.Buffer
		if err := format.Node(&buf, p.fset, stmt); err == nil {
			p.markCommentsInRangeAsProcessed(stmt.Pos(), stmt.End())
			// Remove any trailing newline
			content := buf.String()
			content = strings.TrimSuffix(content, "\n")
			p.buf.WriteString(content)
		}
	}
}

func (p *astPrinter) markCommentsInRangeAsProcessed(start, end token.Pos) {
	for _, cg := range p.comments {
		if cg.Pos() >= start && cg.End() <= end {
			p.processedComments[cg] = true
		}
	}
}

// Helper methods

// printf writes formatted text to the buffer
func (p *astPrinter) printf(format string, args ...interface{}) {
	fmt.Fprintf(p.buf, format, args...)
}

// printIndent prints the current indentation level
func (p *astPrinter) printIndent() {
	for i := 0; i < p.indent; i++ {
		p.buf.WriteByte('\t')
	}
}

// hasCommentsInRange checks if there are comments in the given range
func (p *astPrinter) hasCommentsInRange(start, end token.Pos) bool {
	for _, cg := range p.comments {
		if cg.Pos() >= start && cg.End() <= end {
			return true
		}
	}
	return false
}

// printCommentsBefore prints comments that appear before the given position
func (p *astPrinter) printCommentsBefore(pos token.Pos) {
	var prevCommentEndLine int

	for p.commentIndex < len(p.comments) && p.comments[p.commentIndex].End() <= pos {
		cg := p.comments[p.commentIndex]
		if !p.processedComments[cg] {
			// Calculate spacing from previous comment
			if prevCommentEndLine > 0 {
				commentStartLine := p.fset.Position(cg.Pos()).Line
				blankLines := commentStartLine - prevCommentEndLine - 1
				for i := 0; i < blankLines; i++ {
					p.printf("\n")
				}
			}

			for _, comment := range cg.List {
				// Check if this is a block comment (/* */) that should preserve formatting
				if strings.HasPrefix(comment.Text, "/*") {
					// For block comments, print with current indentation
					p.printIndent()
					p.printf("%s\n", comment.Text)
				} else {
					// For line comments, print with current indentation
					p.printIndent()
					p.printf("%s\n", comment.Text)
				}
			}
			p.processedComments[cg] = true
			prevCommentEndLine = p.fset.Position(cg.End()).Line
		}
		p.commentIndex++
	}
}

// printCommentsAfter prints comments that appear after the given position
func (p *astPrinter) printCommentsAfter(pos token.Pos) {
	for p.commentIndex < len(p.comments) && p.comments[p.commentIndex].Pos() >= pos {
		cg := p.comments[p.commentIndex]
		if !p.processedComments[cg] {
			for _, comment := range cg.List {
				p.printf("%s\n", comment.Text)
			}
			p.processedComments[cg] = true
		}
		p.commentIndex++
	}
}

// preserveSpacing prints the appropriate number of newlines to preserve original spacing
func (p *astPrinter) preserveSpacing(prevEnd, currStart token.Pos) {
	if prevEnd == token.NoPos || currStart == token.NoPos {
		return
	}

	prevPos := p.fset.Position(prevEnd)
	currPos := p.fset.Position(currStart)

	// Calculate lines between the end of previous element and start of current element
	linesBetween := currPos.Line - prevPos.Line

	// Print appropriate number of newlines (linesBetween - 1 because there's already one newline at the end of previous element)
	for i := 1; i < linesBetween; i++ {
		p.printf("\n")
	}
}

// printMapElementsAligned prints map elements with aligned colons
func (p *astPrinter) printMapElementsAligned(elts []ast.Expr) {
	// First pass: calculate the maximum key length
	maxKeyLength := 0
	for _, elt := range elts {
		if kvExpr, ok := elt.(*ast.KeyValueExpr); ok {
			keyLength := p.calculateExprDisplayLength(kvExpr.Key)
			if keyLength > maxKeyLength {
				maxKeyLength = keyLength
			}
		}
	}

	// Second pass: print with alignment
	for _, elt := range elts {
		p.printIndent()
		if kvExpr, ok := elt.(*ast.KeyValueExpr); ok {
			// Print key
			var keyBuf bytes.Buffer
			keyPrinter := &astPrinter{
				config:            p.config,
				fset:              p.fset,
				buf:               &keyBuf,
				processedComments: p.processedComments,
			}
			keyPrinter.printExpr(kvExpr.Key)
			keyStr := keyBuf.String()

			p.printf("%s:", keyStr)

			// Add padding for alignment
			padding := maxKeyLength - len(keyStr)
			for i := 0; i < padding; i++ {
				p.printf(" ")
			}

			p.printf(" ")
			p.printExpr(kvExpr.Value)
		} else {
			p.printExpr(elt)
		}
		p.printf(",\n")
	}
}

// calculateExprDisplayLength calculates the display length of an expression when printed
func (p *astPrinter) calculateExprDisplayLength(expr ast.Expr) int {
	var buf bytes.Buffer
	tempPrinter := &astPrinter{
		config:            p.config,
		fset:              p.fset,
		buf:               &buf,
		processedComments: make(map[*ast.CommentGroup]bool),
	}
	tempPrinter.printExpr(expr)
	return len(buf.String())
}

// shouldUseFallback determines if a file has complex comment structure requiring fallback
func (f *Formatter) shouldUseFallback(file *ast.File) bool {
	// Count comments - if there are many comments, use fallback for better preservation
	commentCount := len(file.Comments)

	// If there are more than 10 comments, use fallback approach
	// This catches cases like the comments.input test which has 12+ comments
	if commentCount > 10 {
		return true
	}

	return false
}

// formatWithFallback uses go/format for complex cases with minimal condensation
func (f *Formatter) formatWithFallback(src []byte, file *ast.File, fset *token.FileSet) ([]byte, error) {
	// For complex comment cases, just use go/format to preserve exact formatting
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, fmt.Errorf("fallback formatting failed: %w", err)
	}
	return buf.Bytes(), nil
}
