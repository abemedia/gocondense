package gocondense

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"slices"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

// condenser handles editing AST nodes in-place for condensation.
type condenser struct {
	config    *Config
	fset      *token.FileSet
	file      *ast.File
	tokenFile *token.File
	addLines  map[ast.Node][2]int
}

// applyPre is called before visiting children nodes.
func (e *condenser) applyPre(c *astutil.Cursor) bool {
	node := c.Node()
	if node == nil {
		return true
	}

	if e.isSingleLine(node) {
		return true
	}

	switch n := node.(type) {
	case *ast.GenDecl:
		newNode := e.replaceGenDecl(n)
		if newNode != nil && newNode != node && e.canCondense(newNode) {
			e.removeLines(e.line(n.Pos()), e.line(n.End()))
			c.Replace(newNode)
		}
	case *ast.TypeSpec:
		e.condenseFieldList(n.TypeParams, Types)
	case *ast.FuncDecl:
		e.condenseFuncDecl(n)
	case ast.Expr:
		e.condenseExpr(n)
	}

	return true
}

// applyPost is called after visiting children nodes.
func (e *condenser) applyPost(c *astutil.Cursor) bool {
	node := c.Node()
	if node == nil {
		return true
	}

	lines, ok := e.addLines[node]
	if !ok {
		return true
	}
	delete(e.addLines, node)

	curSize := e.line(node.End()) - e.line(node.Pos())
	prevSize := lines[1] - lines[0]
	for curSize < prevSize {
		e.addNewline(node.Pos() + 1)
		curSize++
	}

	return len(e.addLines) > 0
}

// replaceGenDecl replaces a GenDecl with a condensed version.
func (e *condenser) replaceGenDecl(decl *ast.GenDecl) *ast.GenDecl {
	if !e.config.Enable.has(Declarations) || len(decl.Specs) > 1 || decl.Lparen == token.NoPos ||
		e.isSingleLine(decl) || e.hasComments(decl) {
		return decl
	}

	return &ast.GenDecl{
		Doc:    decl.Doc,
		Tok:    decl.Tok,
		TokPos: decl.TokPos,
		Specs:  decl.Specs,
	}
}

// condenseFieldList attempts to condense a field list by removing lines between elements.
func (e *condenser) condenseFieldList(list *ast.FieldList, feature Feature) bool {
	if list == nil || e.isSingleLine(list) {
		return true
	}
	if !e.config.Enable.has(feature) || e.hasComments(list) {
		return false
	}

	canCondense := !slices.ContainsFunc(list.List, func(f *ast.Field) bool {
		return isComplexExpr(f.Type)
	})

	for _, field := range list.List {
		if ok := e.condenseExpr(field.Type); !ok {
			canCondense = false
		}
	}

	if !canCondense {
		return false
	}

	return e.condenseNode(list)
}

// condenseCompositeLit attempts to condense a composite literal.
func (e *condenser) condenseCompositeLit(lit *ast.CompositeLit) bool {
	if e.hasComments(lit) || slices.ContainsFunc(lit.Elts, isComplexExpr) {
		return false
	}
	if e.isSingleLine(lit) {
		return true
	}

	var feature Feature

	switch lit.Type.(type) {
	case *ast.MapType:
		feature = Maps
	case *ast.StructType:
		feature = Structs
	default:
		// Check if elements are key-value pairs (struct-like).
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
		return false
	}

	if (feature == Structs || feature == Maps) && len(lit.Elts) > e.config.MaxKeyValue {
		return false
	}
	canCondense := true

	for _, elt := range lit.Elts {
		if ok := e.condenseExpr(elt); !ok {
			canCondense = false
		}
	}

	if !canCondense {
		return false
	}

	return e.condenseNode(lit)
}

// condenseExpr recursively condenses expressions.
func (e *condenser) condenseExpr(expr ast.Expr) bool {
	if e.hasComments(expr) {
		return false
	}
	if e.isSingleLine(expr) {
		return true
	}
	switch n := expr.(type) {
	case *ast.BasicLit:
		return e.condenseBasicLit(n)
	case *ast.BinaryExpr:
		return allOK(e.condenseExpr(n.X), e.condenseExpr(n.Y))
	case *ast.CallExpr:
		return e.condenseCallExpr(n)
	case *ast.CompositeLit:
		return e.condenseCompositeLit(n)
	case *ast.FuncLit:
		return e.condenseFuncLit(n)
	case *ast.FuncType:
		return false
	case *ast.InterfaceType:
		return false
	case *ast.KeyValueExpr:
		return allOK(e.condenseExpr(n.Key), e.condenseExpr(n.Value))
	case *ast.MapType:
		return allOK(e.condenseExpr(n.Key), e.condenseExpr(n.Value))
	case *ast.StructType:
		if !e.config.Enable.has(Structs) || len(n.Fields.List) > 0 {
			return false
		}
		return e.condenseNode(expr)
	case *ast.BadExpr:
		return false
	// case *ast.ArrayType:
	// case *ast.ChanType:
	// case *ast.Ellipsis:
	// case *ast.Ident:
	// case *ast.IndexExpr:
	// case *ast.IndexListExpr:
	// case *ast.ParenExpr:
	// case *ast.UnaryExpr:
	// case *ast.SelectorExpr:
	// case *ast.SliceExpr:
	// case *ast.StarExpr:
	// case *ast.TypeAssertExpr:
	default:
		return e.condenseNode(expr)
	}
}

func (e *condenser) condenseBasicLit(lit *ast.BasicLit) bool {
	if lit.Kind != token.STRING || len(lit.Value) < 2 || lit.Value[0] != '`' {
		return e.condenseNode(lit) // If it's not a raw string literal, we can condense it.
	}

	if strings.Contains(lit.Value, "\n") {
		return false // If it contains newlines, we cannot condense it.
	}

	return e.condenseNode(lit)
}

// condenseCallExpr attempts to condense a function call.
func (e *condenser) condenseCallExpr(call *ast.CallExpr) bool {
	if !e.config.Enable.has(Calls) || e.hasComments(call) {
		return false
	}
	if e.isSingleLine(call) {
		return true
	}

	e.condenseExpr(call.Fun)

	canCondense := true

	for _, arg := range call.Args {
		if ok := e.condenseExpr(arg); !ok {
			canCondense = false
		}
	}

	if !canCondense {
		return false
	}

	return e.condenseNode(call)
}

// condenseFuncDecl attempts to condense a function declaration.
func (e *condenser) condenseFuncDecl(decl *ast.FuncDecl) bool {
	if !e.config.Enable.has(Funcs) {
		return false
	}
	if e.isSingleLine(decl) {
		return true
	}

	return allOK(
		e.condenseFieldList(decl.Recv, Funcs),
		e.condenseFuncType(decl.Type, Funcs),
	)
}

// condenseFuncLit attempts to condense a function literal.
func (e *condenser) condenseFuncLit(lit *ast.FuncLit) bool {
	if !e.config.Enable.has(Literals) {
		return false
	}
	if e.isSingleLine(lit) {
		return true
	}

	// Protect the function body from being condensed by parent expressions.
	e.addLines[lit.Body] = [2]int{e.line(lit.Body.Pos()), e.line(lit.Body.End())}

	return e.condenseFuncType(lit.Type, Literals) && len(lit.Body.List) <= 1
}

// condenseFuncType attempts to condense a function type.
func (e *condenser) condenseFuncType(funcType *ast.FuncType, feature Feature) bool {
	if !e.config.Enable.has(feature) {
		return true
	}
	if e.isSingleLine(funcType) || funcType == nil {
		return true
	}

	lines := slices.Clone(e.tokenFile.Lines())

	// Attempt multiple combinations of condensing field lists
	// to find the best fit without exceeding MaxLen.
	combinations := [][]*ast.FieldList{
		{funcType.TypeParams, funcType.Params, funcType.Results},
		{funcType.TypeParams, funcType.Results},
		{funcType.Params, funcType.Results},
		{funcType.Results},
		{funcType.TypeParams, funcType.Params},
		{funcType.TypeParams},
		{funcType.Params},
	}
	success := 0
	first := true
	for _, fields := range combinations {
		if slices.Contains(fields, nil) {
			continue // Skip combinations with nil fields.
		}
		for _, field := range fields {
			if e.condenseFieldList(field, feature) {
				success++
			}
		}
		if e.canCondense(funcType) {
			return first && len(fields) == success // Return true if all fields were condensed successfully.
		}
		e.tokenFile.SetLines(slices.Clone(lines))
		success = 0
		first = false
	}

	return first == true
}

// hasCommentsInRange checks if there are any comments between the given positions.
func (e *condenser) hasCommentsInRange(start, end token.Pos) bool {
	for _, cg := range e.file.Comments {
		if cg.Pos() >= start && cg.End() <= end {
			return true
		}
	}
	return false
}

// hasComments checks if there are any comments within the node's position range.
func (e *condenser) hasComments(node ast.Node) bool {
	if node == nil {
		return false
	}
	return e.hasCommentsInRange(node.Pos(), node.End())
}

// isSingleLine checks if a node is already on a single line.
func (e *condenser) isSingleLine(node ast.Node) bool {
	return e.line(node.Pos()) == e.line(node.End())
}

// line returns the line number for a position.
func (e *condenser) line(pos token.Pos) int {
	return e.fset.Position(pos).Line
}

// removeLines removes all newlines between two line numbers, so that they end
// up on the same line.
func (e *condenser) removeLines(fromLine, toLine int) {
	for fromLine < toLine {
		e.tokenFile.MergeLine(fromLine)
		toLine--
	}
}

func (e *condenser) addNewline(pos token.Pos) {
	offset := e.fset.Position(pos).Offset

	lines := e.tokenFile.Lines()
	i, exists := slices.BinarySearch(lines, offset)
	if exists {
		return
	}
	lines = slices.Insert(lines, i, offset)
	if !e.tokenFile.SetLines(lines) {
		panic(fmt.Sprintf("could not set lines to %v", lines))
	}
}

// canCondense checks if a node can be condensed without exceeding MaxLen.
func (e *condenser) canCondense(node ast.Node) bool {
	maxLen := e.config.MaxLen
	if maxLen == 0 {
		maxLen = DefaultConfig.MaxLen
	}

	tabWidth := e.config.TabWidth
	if tabWidth == 0 {
		tabWidth = DefaultConfig.TabWidth
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, e.fset, node); err != nil {
		return true
	}

	lines := bytes.SplitSeq(buf.Bytes(), []byte{'\n'})
	for line := range lines {
		length := len(line) + bytes.Count(line, []byte{'\t'})*tabWidth - 1
		if length > maxLen {
			return false // If any line exceeds MaxLen, we cannot condense.
		}
	}

	return true
}

// condenseNode attempts to condense a node by removing lines between its positions.
func (e *condenser) condenseNode(node ast.Node) bool {
	if e.isSingleLine(node) || node == nil {
		return true
	}

	lines := slices.Clone(e.tokenFile.Lines())
	e.removeLines(e.line(node.Pos()), e.line(node.End()))

	if !e.canCondense(node) {
		e.tokenFile.SetLines(lines)
		return false
	}

	return e.isSingleLine(node)
}

func allOK(condensers ...bool) bool {
	return !slices.Contains(condensers, false)
}
