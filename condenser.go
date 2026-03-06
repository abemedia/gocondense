package gocondense

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"slices"
	"sort"

	"golang.org/x/tools/go/ast/astutil"
)

// condenser implements AST traversal callbacks that simplify and condense nodes.
type condenser struct {
	config      *Config
	fset        *token.FileSet
	file        *ast.File
	tokenFile   *token.File
	buf         *bytes.Buffer
	parents     []ast.Node // stack of ancestor nodes for parent-walk
	indentLevel int        // current nesting depth (blocks, cases)
}

// applyPre tracks parent nodes and indentation level before visiting children.
func (e *condenser) applyPre(c *astutil.Cursor) bool {
	node := c.Node()
	if node == nil {
		return true
	}

	e.parents = append(e.parents, node)

	switch node.(type) {
	case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
		e.indentLevel++
	}

	return true
}

// applyPost performs all condensation work after children have been visited.
func (e *condenser) applyPost(c *astutil.Cursor) bool { //nolint:cyclop,funlen
	node := c.Node()
	if node == nil {
		return true
	}

	switch node.(type) {
	case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
		e.indentLevel--
	}

	switch n := node.(type) {
	case *ast.GenDecl:
		if newNode := e.replaceGenDecl(n); newNode != node && e.canCondense(newNode) {
			e.removeLines(e.line(n.Pos()), e.line(n.End()))
			c.Replace(newNode)
		}
	case *ast.ParenExpr:
		if newNode := e.replaceParenExpr(n); newNode != node {
			c.Replace(newNode)
		}
	case *ast.FieldList:
		e.condenseFieldList(n)
		switch c.Parent().(type) {
		case *ast.FuncType, *ast.TypeSpec:
			e.mergeFields(n)
		case *ast.StructType, *ast.InterfaceType:
			trimBlock(e, n.Opening, n.Closing, n.List)
		}
	case *ast.BlockStmt:
		trimBlock(e, n.Lbrace, n.Rbrace, n.List)
	case *ast.CaseClause:
		trimBlock(e, n.Colon, n.End(), n.Body)
	case *ast.CommClause:
		trimBlock(e, n.Colon, n.End(), n.Body)
	case *ast.CompositeLit:
		e.condenseCompositeLit(n)
	case *ast.CallExpr:
		e.condenseCallExpr(n)
	case *ast.BinaryExpr:
		if !e.isSingleLine(n) && e.isSingleLine(n.X) && e.isSingleLine(n.Y) && !e.hasComments(n) {
			e.condenseNode(n)
		}
	case *ast.SelectorExpr:
		if !e.isSingleLine(n) && e.isSingleLine(n.X) && !e.hasComments(n) {
			e.condenseNode(n)
		}
	case *ast.IndexListExpr:
		if !e.isSingleLine(n) && e.isSingleLine(n.X) && !e.hasComments(n) &&
			!slices.ContainsFunc(n.Indices, func(idx ast.Expr) bool { return !e.isSingleLine(idx) }) {
			e.condenseNode(n)
		}
	}

	e.parents = e.parents[:len(e.parents)-1] // Pop parent stack.

	return true
}

// replaceGenDecl unwraps single-spec declaration groups by returning a copy
// without parentheses. Multi-spec groups, groups without parens, and groups
// containing comments are returned unchanged.
func (e *condenser) replaceGenDecl(decl *ast.GenDecl) *ast.GenDecl {
	if len(decl.Specs) > 1 || decl.Lparen == token.NoPos || e.hasComments(decl) {
		return decl
	}

	return &ast.GenDecl{
		Doc:    decl.Doc,
		Tok:    decl.Tok,
		TokPos: decl.TokPos,
		Specs:  decl.Specs,
	}
}

// replaceParenExpr recursively removes unnecessary parentheses.
// Parentheses are preserved only around binary and unary expressions
// where they may affect precedence or associativity.
func (e *condenser) replaceParenExpr(paren *ast.ParenExpr) ast.Expr {
	switch paren.X.(type) {
	case *ast.BinaryExpr, *ast.UnaryExpr:
		return paren
	default:
		if p, ok := paren.X.(*ast.ParenExpr); ok {
			return e.replaceParenExpr(p)
		}
		return paren.X
	}
}

// condenseFieldList attempts to collapse a multi-line field list (params,
// results, type params, or receivers) onto a single line.
func (e *condenser) condenseFieldList(list *ast.FieldList) {
	if list == nil || e.isSingleLine(list) {
		return
	}

	switch e.parent(1).(type) {
	case *ast.FuncType, *ast.TypeSpec, *ast.FuncDecl:
	default:
		// StructType, InterfaceType - not condensed via this path.
		return
	}

	if e.hasComments(list) {
		return
	}

	// All children are already condensed. Check field types are simple and single-line.
	for _, field := range list.List {
		if !e.isSingleLine(field.Type) {
			return
		}
	}

	// Condense: remove lines to make the FieldList single-line.
	saved := slices.Clone(e.tokenFile.Lines())
	e.removeLines(e.line(list.Pos()), e.line(list.End()))

	// format.Node can't render a standalone FieldList, so verify against
	// the parent node which IS renderable.
	if !e.canCondense(e.parent(1)) {
		e.tokenFile.SetLines(saved)
	}
}

// mergeFields merges adjacent fields with the same type (e.g. `a T, b T` → `a, b T`).
func (e *condenser) mergeFields(list *ast.FieldList) {
	if !e.isSingleLine(list) || e.hasComments(list) {
		return
	}
	for i := len(list.List) - 1; i > 0; i-- {
		a, b := list.List[i-1], list.List[i]
		if len(a.Names) > 0 && len(b.Names) > 0 && equalExpr(a.Type, b.Type) {
			a.Names = append(a.Names, b.Names...)
			list.List = slices.Delete(list.List, i, i+1)
		}
	}
}

// condenseCompositeLit strips blank lines from composite literals and attempts
// to collapse multi-line slice/array literals onto a single line.
// For structs and maps with key-value pairs, the first-element rule applies:
// condense only when the first element shares a line with the opening brace.
func (e *condenser) condenseCompositeLit(lit *ast.CompositeLit) {
	trimBlock(e, lit.Lbrace, lit.Rbrace, lit.Elts)
	if len(lit.Elts) == 0 || e.isSingleLine(lit) || e.hasComments(lit) {
		return
	}

	// First-element rule for key-value literals (structs/maps):
	// only condense when first element shares a line with the opening brace.
	var hasKeyValue bool
	switch lit.Type.(type) {
	case *ast.MapType:
		hasKeyValue = true
	default:
		hasKeyValue = slices.ContainsFunc(lit.Elts, func(e ast.Expr) bool {
			_, ok := e.(*ast.KeyValueExpr)
			return ok
		})
	}
	if hasKeyValue && e.line(lit.Lbrace) != e.line(lit.Elts[0].Pos()) {
		return
	}

	// All children already condensed. Check they're all single-line.
	for _, elt := range lit.Elts {
		if !e.isSingleLine(elt) {
			return
		}
	}

	e.condenseNode(lit)
}

// condenseCallExpr handles condensing of function call expressions.
// If all args are single-line, condenses the entire call.
// If only the last arg is multiline, condenses leading args onto the first line
// and pulls the closing paren up after the trailing arg.
func (e *condenser) condenseCallExpr(call *ast.CallExpr) {
	if e.isSingleLine(call) || !e.isSingleLine(call.Fun) {
		return
	}

	// Find the first multiline arg: -1 means all single-line,
	// len-1 means only the last is multiline, anything else we leave alone.
	i := slices.IndexFunc(call.Args, func(arg ast.Expr) bool { return !e.isSingleLine(arg) })
	if i == -1 {
		if !e.hasComments(call) {
			e.condenseNode(call)
		}
		return
	}
	if i != len(call.Args)-1 {
		return
	}

	// Trailing multiline argument: last arg is multiline, all others are single-line.
	lastArg := call.Args[i]

	// Only check for comments in the leading args and surrounding parens,
	// not the last arg which stays multiline.
	if e.hasCommentsInRange(call.Lparen, lastArg.Pos()-1) || e.hasCommentsInRange(lastArg.End(), call.Rparen) {
		return
	}

	saved := slices.Clone(e.tokenFile.Lines())

	e.removeLines(e.line(call.Lparen), e.line(lastArg.Pos()))
	e.removeLines(e.line(lastArg.End()), e.line(call.Rparen))

	if !e.canCondense(call) {
		e.tokenFile.SetLines(saved)
	}
}

// trimBlock collapses empty block-like nodes and trims leading/trailing
// blank lines from non-empty ones.
//
// TODO: convert to a method once https://github.com/golang/go/issues/77273 lands.
func trimBlock[T ast.Node](e *condenser, start, end token.Pos, children []T) {
	if len(children) == 0 && !e.hasCommentsInRange(start, end) {
		e.removeLines(e.line(start), e.line(end))
		return
	}

	// Determine first child start and last child end positions.
	first, last := end, start
	if len(children) > 0 {
		first, last = children[0].Pos(), children[len(children)-1].End()
	}

	// Trim leading blank lines between the opening delimiter and the first child.
	startLine := e.line(start)
	firstLine := e.line(first)
	if firstLine > startLine+1 {
		leading := 0
		for l := startLine + 1; l < firstLine; l++ {
			if e.hasCommentsInRange(e.tokenFile.LineStart(l), e.tokenFile.LineStart(l+1)-1) {
				break
			}
			leading++
		}
		e.removeLines(startLine, startLine+leading)
	}

	// Trim trailing blank lines between the last child and the closing delimiter.
	endLine := e.line(end)
	lastLine := e.line(last)
	if endLine > lastLine+1 {
		trailing := 0
		for l := endLine - 1; l > lastLine; l-- {
			if e.hasCommentsInRange(e.tokenFile.LineStart(l), e.tokenFile.LineStart(l+1)-1) {
				break
			}
			trailing++
		}
		e.removeLines(endLine-trailing-1, endLine-1)
	}
}

// hasCommentsInRange reports whether any comment group overlaps [start, end].
// It uses binary search on the position-sorted comment list to find the first
// group ending at or after start, then checks if that group begins before end.
func (e *condenser) hasCommentsInRange(start, end token.Pos) bool {
	comments := e.file.Comments
	i := sort.Search(len(comments), func(i int) bool {
		return comments[i].End() >= start
	})
	return i < len(comments) && comments[i].Pos() <= end
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
	return node == nil || e.line(node.Pos()) == e.line(node.End())
}

// parent returns the nth ancestor from the parent stack (0 = self, 1 = parent, 2 = grandparent).
func (e *condenser) parent(n int) ast.Node {
	if i := len(e.parents) - 1 - n; i >= 0 {
		return e.parents[i]
	}
	return nil
}

// line returns the line number for a position.
func (e *condenser) line(pos token.Pos) int {
	return e.tokenFile.Line(pos)
}

// removeLines removes all newlines between two line numbers, so that they end
// up on the same line.
func (e *condenser) removeLines(fromLine, toLine int) {
	for fromLine < toLine {
		e.tokenFile.MergeLine(fromLine)
		toLine--
	}
}

// canCondense checks whether the rendered node fits within MaxLen.
// It formats the node via format.Node and checks every output line against
// the limit, accounting for indentation and tab width.
func (e *condenser) canCondense(node ast.Node) bool {
	maxLen := e.config.MaxLen
	if maxLen == 0 {
		maxLen = DefaultConfig.MaxLen
	}

	tabWidth := e.config.TabWidth
	if tabWidth == 0 {
		tabWidth = DefaultConfig.TabWidth
	}

	e.buf.Reset()
	if err := format.Node(e.buf, e.fset, node); err != nil {
		panic("gocondense: format.Node failed: " + err.Error())
	}

	startCol := e.startColumn(node.Pos())

	first := true
	lines := bytes.SplitSeq(e.buf.Bytes(), []byte{'\n'})
	for line := range lines {
		// Each tab is already counted as 1 byte by len(line), so we add (tabWidth-1)
		// per tab to get the correct visual width without double-counting.
		length := len(line) + bytes.Count(line, []byte{'\t'})*(tabWidth-1)
		if first {
			length += startCol
			first = false
		}
		if length > maxLen {
			return false // If any line exceeds MaxLen, we cannot condense.
		}
	}

	return true
}

// startColumn returns the visual column where pos begins on its line.
// It walks up the parent stack to find the topmost ancestor on the same line,
// then computes: indentLevel * tabWidth + byte distance from ancestor to pos.
// ancestor.Pos() is after leading tabs, so the byte distance is pure non-tab code.
func (e *condenser) startColumn(pos token.Pos) int {
	tabWidth := e.config.TabWidth
	if tabWidth == 0 {
		tabWidth = DefaultConfig.TabWidth
	}

	line := e.line(pos)
	var ancestor token.Pos
	for i := len(e.parents) - 1; i >= 0; i-- {
		p := e.parents[i]
		if e.line(p.Pos()) != line {
			break
		}
		ancestor = p.Pos()
	}

	col := e.indentLevel * tabWidth
	if ancestor.IsValid() {
		col += int(pos - ancestor)
	}
	return col
}

// condenseNode attempts to condense a node by removing lines between its positions.
// If the condensed result would exceed MaxLen, the line table is restored.
func (e *condenser) condenseNode(node ast.Node) {
	if e.isSingleLine(node) {
		return
	}

	lines := slices.Clone(e.tokenFile.Lines())
	e.removeLines(e.line(node.Pos()), e.line(node.End()))

	if !e.canCondense(node) {
		e.tokenFile.SetLines(lines)
	}
}

// equalExpr reports whether two AST type expressions are structurally equal.
func equalExpr(a, b ast.Expr) bool { //nolint:cyclop,funlen
	if a == nil || b == nil {
		return a == b
	}
	switch x := a.(type) {
	case *ast.Ident:
		y, ok := b.(*ast.Ident)
		return ok && x.Name == y.Name
	case *ast.StarExpr:
		y, ok := b.(*ast.StarExpr)
		return ok && equalExpr(x.X, y.X)
	case *ast.SelectorExpr:
		y, ok := b.(*ast.SelectorExpr)
		return ok && equalExpr(x.X, y.X) && x.Sel.Name == y.Sel.Name
	case *ast.ArrayType:
		y, ok := b.(*ast.ArrayType)
		return ok && equalExpr(x.Len, y.Len) && equalExpr(x.Elt, y.Elt)
	case *ast.MapType:
		y, ok := b.(*ast.MapType)
		return ok && equalExpr(x.Key, y.Key) && equalExpr(x.Value, y.Value)
	case *ast.ChanType:
		y, ok := b.(*ast.ChanType)
		return ok && x.Dir == y.Dir && equalExpr(x.Value, y.Value)
	case *ast.IndexExpr:
		y, ok := b.(*ast.IndexExpr)
		return ok && equalExpr(x.X, y.X) && equalExpr(x.Index, y.Index)
	case *ast.IndexListExpr:
		y, ok := b.(*ast.IndexListExpr)
		return ok && equalExpr(x.X, y.X) && slices.EqualFunc(x.Indices, y.Indices, equalExpr)
	case *ast.BasicLit:
		y, ok := b.(*ast.BasicLit)
		return ok && x.Kind == y.Kind && x.Value == y.Value
	case *ast.Ellipsis:
		y, ok := b.(*ast.Ellipsis)
		return ok && equalExpr(x.Elt, y.Elt)
	case *ast.InterfaceType:
		y, ok := b.(*ast.InterfaceType)
		return ok && slices.EqualFunc(x.Methods.List, y.Methods.List, func(xf, yf *ast.Field) bool {
			return slices.EqualFunc(xf.Names, yf.Names, func(xi, yi *ast.Ident) bool {
				return xi.Name == yi.Name
			}) && equalExpr(xf.Type, yf.Type)
		})
	case *ast.FuncType:
		y, ok := b.(*ast.FuncType)
		return ok && equalFieldList(x.Params, y.Params) && equalFieldList(x.Results, y.Results)
	default:
		return false
	}
}

// equalFieldList reports whether two field lists are structurally equal by type.
func equalFieldList(a, b *ast.FieldList) bool {
	if a == nil || b == nil {
		return a == b
	}
	return slices.EqualFunc(a.List, b.List, func(x, y *ast.Field) bool {
		return equalExpr(x.Type, y.Type)
	})
}
