package gocondense

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"

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

	editor := &astEditor{
		config:    f.config,
		fset:      fset,
		file:      file,
		tokenFile: fset.File(file.Pos()),
		replaced:  map[ast.Node]ast.Node{},
	}

	astutil.Apply(file, editor.applyPre, editor.applyPost)

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, fmt.Errorf("failed to format AST: %w", err)
	}

	return buf.Bytes(), nil
}

// astEditor handles editing AST nodes in-place for condensation.
type astEditor struct {
	config    *Config
	fset      *token.FileSet
	file      *ast.File
	tokenFile *token.File
	replaced  map[ast.Node]ast.Node
}

// applyPre is called before visiting children nodes.
func (e *astEditor) applyPre(c *astutil.Cursor) bool {
	node := c.Node()
	if node == nil {
		return true
	}

	if e.isSingleLine(node) {
		return false
	}

	var newNode ast.Node
	var removeLines bool

	switch n := node.(type) {
	case *ast.GenDecl:
		newNode = e.replaceGenDecl(n)
		removeLines = true
	case *ast.TypeSpec:
		newNode = e.replaceTypeSpec(n)
	case *ast.FuncDecl:
		newNode = e.replaceFuncDecl(n)
	case *ast.CallExpr:
		newNode = e.replaceCallExpr(n)
		removeLines = !slices.ContainsFunc(n.Args, isComplexExpr)
	case *ast.CompositeLit:
		newNode = e.replaceCompositeLit(n)
		removeLines = !slices.ContainsFunc(n.Elts, isComplexExpr)
	case *ast.FuncLit:
		newNode = e.replaceFuncLit(n)
		removeLines = true
	default:
		return true
	}

	if newNode != nil && newNode != node && e.canCondense(newNode) {
		e.replaced[newNode] = node
		c.Replace(newNode)
		if removeLines {
			e.removeNewLines(node, newNode)
		}
	}

	return true
}

func (e *astEditor) applyPost(c *astutil.Cursor) bool {
	node := c.Node()
	if node == nil || !node.Pos().IsValid() {
		return true
	}

	var exprs []ast.Expr

	switch n := node.(type) {
	case *ast.CallExpr:
		exprs = n.Args
	case *ast.CompositeLit:
		exprs = n.Elts
	default:
		return true
	}

	if slices.ContainsFunc(exprs, func(expr ast.Expr) bool { return !expr.Pos().IsValid() }) {
		base := node.Pos()
		end := node.End()
		diff := (end - base - 2) / token.Pos(len(exprs))

		for i, expr := range exprs {
			pos := base + (token.Pos(i+1) * diff)
			switch ex := expr.(type) {
			case *ast.CompositeLit:
				ex.Lbrace = pos
			case *ast.FuncLit:
				ex.Type.Func = pos
			case *ast.CallExpr:
				ex.Lparen = pos
			case *ast.InterfaceType:
				ex.Interface = pos
			}
		}
	}

	return true
}

// replaceGenDecl replaces a GenDecl with a condensed version.
func (e *astEditor) replaceGenDecl(decl *ast.GenDecl) *ast.GenDecl {
	if !e.config.Enable.has(Declarations) {
		return decl
	}

	if e.isSingleLine(decl) {
		return decl
	}

	if len(decl.Specs) > 1 || e.hasComments(decl) {
		return decl
	}

	return &ast.GenDecl{
		Doc:    decl.Doc,
		Tok:    decl.Tok,
		TokPos: decl.TokPos,
		Specs:  decl.Specs,
	}
}

// replaceTypeSpec replaces a TypeSpec with a condensed version.
func (e *astEditor) replaceTypeSpec(spec *ast.TypeSpec) *ast.TypeSpec {
	if !e.config.Enable.has(Types) || spec.TypeParams == nil {
		return spec
	}

	if e.hasComments(spec.TypeParams) {
		return spec
	}

	newTypeParams := e.replaceFieldList(spec.TypeParams, Types)
	if newTypeParams == spec.TypeParams {
		return spec
	}

	return &ast.TypeSpec{
		Doc:        spec.Doc,
		Name:       spec.Name,
		TypeParams: e.replaceFieldList(spec.TypeParams, Types),
		Assign:     spec.Assign,
		Type:       spec.Type,
		Comment:    spec.Comment,
	}
}

// replaceFuncDecl replaces a FuncDecl with a condensed version.
func (e *astEditor) replaceFuncDecl(decl *ast.FuncDecl) *ast.FuncDecl {
	if e.isSingleLine(decl) || !e.config.Enable.has(Funcs) {
		return decl
	}

	newType := e.replaceFuncType(decl.Type, Funcs)
	newRecv := e.replaceFieldList(decl.Recv, Funcs)

	if newType == decl.Type && newRecv == decl.Recv {
		return decl
	}

	return &ast.FuncDecl{
		Doc:  decl.Doc,
		Recv: newRecv,
		Name: decl.Name,
		Type: newType,
		Body: decl.Body,
	}
}

// replaceFuncLit replaces a FuncLit with a condensed version.
func (e *astEditor) replaceFuncLit(lit *ast.FuncLit) *ast.FuncLit {
	if !e.config.Enable.has(Literals) || e.isSingleLine(lit) {
		return lit
	}

	newType := e.replaceFuncType(lit.Type, Literals)
	if newType == lit.Type {
		return lit
	}

	return &ast.FuncLit{
		Type: newType,
		Body: lit.Body,
	}
}

// replaceFuncType replaces a FuncType with a condensed version.
func (e *astEditor) replaceFuncType(funcType *ast.FuncType, feature Feature) *ast.FuncType {
	results := e.replaceFieldList(funcType.Results, feature)
	typeParams := e.replaceFieldList(funcType.TypeParams, feature)
	params := e.replaceFieldList(funcType.Params, feature)

	if results == funcType.Results && typeParams == funcType.TypeParams && params == funcType.Params {
		return funcType
	}

	return &ast.FuncType{
		Func:       funcType.Func,
		TypeParams: typeParams,
		Params:     params,
		Results:    results,
	}
}

// replaceCallExpr replaces a CallExpr with a condensed version.
func (e *astEditor) replaceCallExpr(call *ast.CallExpr) *ast.CallExpr {
	if !e.config.Enable.has(Calls) {
		return call
	}

	if e.isSingleLine(call) || e.hasComments(call) {
		return call
	}

	newArgs := make([]ast.Expr, len(call.Args))
	for i, arg := range call.Args {
		newArgs[i] = e.replaceExpr(arg)
	}

	return &ast.CallExpr{
		Fun:      call.Fun,
		Args:     newArgs,
		Ellipsis: call.Ellipsis,
	}
}

// replaceCompositeLit replaces a CompositeLit with a condensed version.
func (e *astEditor) replaceCompositeLit(lit *ast.CompositeLit) *ast.CompositeLit {
	if e.isSingleLine(lit) || e.hasComments(lit) || slices.ContainsFunc(lit.Elts, isComplexExpr) {
		return lit
	}

	var feature Feature

	switch lit.Type.(type) {
	case *ast.MapType:
		feature = Maps
	case *ast.StructType:
		feature = Structs
	default:
		// Check if elements are key-value pairs (struct-like)
		hasKeyValue := false
		for _, elt := range lit.Elts {
			if _, ok := elt.(*ast.KeyValueExpr); ok {
				hasKeyValue = true
				break
			}
		}
		if hasKeyValue {
			feature = Structs
		} else {
			feature = Slices
		}
	}

	if !e.config.Enable.has(feature) {
		return lit
	}

	if (feature == Structs || feature == Maps) && len(lit.Elts) > e.config.MaxKeyValue {
		return lit
	}

	newElts := make([]ast.Expr, len(lit.Elts))
	for i, elt := range lit.Elts {
		newElts[i] = e.replaceExpr(elt)
	}

	return &ast.CompositeLit{
		Type: lit.Type,
		Elts: newElts,
	}
}

// replaceFieldList replaces a FieldList with a condensed version.
func (e *astEditor) replaceFieldList(list *ast.FieldList, feature Feature) *ast.FieldList {
	if list == nil || len(list.List) == 0 || !e.config.Enable.has(feature) {
		return list
	}

	if e.isSingleLine(list) || e.hasComments(list) {
		return list
	}

	hasComplexFields := slices.ContainsFunc(list.List, func(f *ast.Field) bool { return isComplexExpr(f.Type) })
	if hasComplexFields {
		return list
	}

	newFields := make([]*ast.Field, len(list.List))
	for i, field := range list.List {
		var newNames []*ast.Ident
		for _, name := range field.Names {
			newNames = append(newNames, &ast.Ident{Name: name.Name})
		}

		newFields[i] = &ast.Field{
			Doc:     field.Doc,
			Names:   newNames,
			Type:    e.replaceExpr(field.Type),
			Tag:     field.Tag,
			Comment: field.Comment,
		}
	}

	return &ast.FieldList{List: newFields}
}

// replaceExpr creates a deep copy of an expression with all positions set to NoPos.
func (e *astEditor) replaceExpr(expr ast.Expr) ast.Expr {
	switch ex := expr.(type) {
	case *ast.Ident:
		return &ast.Ident{Name: ex.Name}
	case *ast.BasicLit:
		return &ast.BasicLit{
			Kind:  ex.Kind,
			Value: ex.Value,
		}
	case *ast.KeyValueExpr:
		key := e.replaceExpr(ex.Key)
		value := e.replaceExpr(ex.Value)
		if key == ex.Key && value == ex.Value || isComplexExpr(key) || isComplexExpr(value) {
			return ex
		}
		return &ast.KeyValueExpr{Key: key, Value: value}
	case *ast.CallExpr:
		return e.replaceCallExpr(ex)
	case *ast.SelectorExpr:
		return &ast.SelectorExpr{
			X:   e.replaceExpr(ex.X),
			Sel: e.replaceExpr(ex.Sel).(*ast.Ident),
		}
	case *ast.CompositeLit:
		return e.replaceCompositeLit(ex)
	case *ast.FuncLit:
		return e.replaceFuncLit(ex)
	case *ast.StarExpr:
		return &ast.StarExpr{X: e.replaceExpr(ex.X)}
	case *ast.IndexListExpr:
		newIndices := make([]ast.Expr, len(ex.Indices))
		for i, idx := range ex.Indices {
			newIndices[i] = e.replaceExpr(idx)
		}
		return &ast.IndexListExpr{
			X:       ex.X,
			Indices: newIndices,
		}
	case *ast.IndexExpr:
		return &ast.IndexExpr{
			X:     ex.X,
			Index: e.replaceExpr(ex.Index),
		}
	}

	return expr
}

// hasCommentsInRange checks if there are any comments between the given positions.
func (e *astEditor) hasCommentsInRange(start, end token.Pos) bool {
	for _, cg := range e.file.Comments {
		if cg.Pos() >= start && cg.End() <= end {
			return true
		}
	}
	return false
}

// hasComments checks if there are any comments within the node's position range.
func (e *astEditor) hasComments(node ast.Node) bool {
	return e.hasCommentsInRange(node.Pos(), node.End())
}

// isSingleLine checks if a node is already on a single line.
func (e *astEditor) isSingleLine(node ast.Node) bool {
	return e.line(node.Pos()) == e.line(node.End())
}

// line returns the line number for a position.
func (e *astEditor) line(pos token.Pos) int {
	return e.fset.Position(pos).Line
}

// removeLines removes all newlines between two line numbers, so that they end
// up on the same line.
func (e *astEditor) removeLines(fromLine, toLine int) {
	for fromLine < toLine {
		e.tokenFile.MergeLine(fromLine)
		toLine--
	}
}

// removeNewLines removes blank lines that may be left when condensing a multi-line construct.
func (e *astEditor) removeNewLines(oldNode, newNode ast.Node) {
	if oldNode == nil || newNode == nil || oldNode == newNode {
		return
	}

	start := e.line(oldNode.Pos())
	end := e.line(oldNode.End())
	oldLines := end - start
	if oldLines < 1 {
		return
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, e.fset, newNode); err != nil {
		panic(fmt.Sprintf("failed to format new node: %v", err))
	}

	newLines := bytes.Count(buf.Bytes(), []byte{'\n'})
	linesToRemove := oldLines - newLines
	if linesToRemove > 0 {
		e.removeLines(end-linesToRemove, end)
	}
}

// calculateLineLength calculates the length of a node when formatted as a single line.
func (e *astEditor) calculateLineLength(node ast.Node) int {
	var buf bytes.Buffer
	if err := format.Node(&buf, e.fset, node); err != nil {
		return 0
	}
	lines := buf.Bytes()

	line, _, ok := bytes.Cut(lines, []byte{'\n'})
	if !ok {
		line = lines
	}

	length := 0
	tabWidth := e.config.TabWidth
	if tabWidth == 0 {
		tabWidth = DefaultConfig.TabWidth
	}

	length = len(line) + bytes.Count(line, []byte{'\n'})*tabWidth - 1

	return length
}

// canCondense checks if a node can be condensed without exceeding MaxLen.
func (e *astEditor) canCondense(node ast.Node) bool {
	maxLen := e.config.MaxLen
	if maxLen == 0 {
		maxLen = DefaultConfig.MaxLen
	}

	return e.calculateLineLength(node) <= maxLen
}

func isComplexExpr(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.CompositeLit, *ast.FuncLit, *ast.CallExpr, *ast.InterfaceType:
		return true
	default:
		return false
	}
}
