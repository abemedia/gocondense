package gocondense

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"slices"

	"golang.org/x/tools/go/ast/astutil"
)

// condenser handles editing AST nodes in-place for condensation.
type condenser struct {
	config    *Config
	fset      *token.FileSet
	file      *ast.File
	tokenFile *token.File
	replaced  map[ast.Node]ast.Node
}

// applyPre is called before visiting children nodes.
func (e *condenser) applyPre(c *astutil.Cursor) bool {
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

// replaceGenDecl replaces a GenDecl with a condensed version.
func (e *condenser) replaceGenDecl(decl *ast.GenDecl) *ast.GenDecl {
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
func (e *condenser) replaceTypeSpec(spec *ast.TypeSpec) *ast.TypeSpec {
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
func (e *condenser) replaceFuncDecl(decl *ast.FuncDecl) *ast.FuncDecl {
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
func (e *condenser) replaceFuncLit(lit *ast.FuncLit) *ast.FuncLit {
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
func (e *condenser) replaceFuncType(funcType *ast.FuncType, feature Feature) *ast.FuncType {
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
func (e *condenser) replaceCallExpr(call *ast.CallExpr) *ast.CallExpr {
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
func (e *condenser) replaceCompositeLit(lit *ast.CompositeLit) *ast.CompositeLit {
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
func (e *condenser) replaceFieldList(list *ast.FieldList, feature Feature) *ast.FieldList {
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
func (e *condenser) replaceExpr(expr ast.Expr) ast.Expr {
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

// removeNewLines removes blank lines that may be left when condensing a multi-line construct.
func (e *condenser) removeNewLines(oldNode, newNode ast.Node) {
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
func (e *condenser) calculateLineLength(node ast.Node) int {
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
func (e *condenser) canCondense(node ast.Node) bool {
	maxLen := e.config.MaxLen
	if maxLen == 0 {
		maxLen = DefaultConfig.MaxLen
	}

	return e.calculateLineLength(node) <= maxLen
}
