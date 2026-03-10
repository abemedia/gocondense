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
	maxLen      int
	tabWidth    int
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
		if e.simplifyGenDecl(n) {
			c.Delete()
		}
	case *ast.ParenExpr:
		if e.canRemoveParens(n) {
			c.Replace(n.X)
		}
	case *ast.FieldList:
		e.condenseFieldList(n)
	case *ast.BlockStmt:
		trim(e, n.Lbrace, n.Rbrace, n.List)
	case *ast.CaseClause:
		trimTop(e, n.Colon, n.End(), n.Body)
	case *ast.CommClause:
		trimTop(e, n.Colon, n.End(), n.Body)
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
	case *ast.AssignStmt:
		if !e.hasCommentsInRange(n.TokPos, n.Rhs[0].Pos()) {
			e.removeLines(e.line(n.TokPos), e.line(n.Rhs[0].Pos()))
		} else {
			trimTop(e, n.TokPos, n.End(), n.Rhs)
		}
	case *ast.ValueSpec:
		if len(n.Values) > 0 {
			start := n.Pos()
			if n.Type != nil {
				start = n.Type.End()
			}
			trimTop(e, start, n.End(), n.Values)
		}
	}

	e.parents = e.parents[:len(e.parents)-1] // Pop parent stack.

	return true
}

// simplifyGenDecl simplifies grouped declarations. It trims blank lines in
// multi-spec or commented groups, removes parens from single-spec groups, and
// reports whether the declaration is empty and should be deleted.
func (e *condenser) simplifyGenDecl(decl *ast.GenDecl) bool {
	switch {
	case !decl.Lparen.IsValid():
	case len(decl.Specs) > 1, e.hasComments(decl):
		trim(e, decl.Lparen, decl.Rparen, decl.Specs)
	case decl.Specs != nil:
		start, end := e.line(decl.Lparen), e.line(decl.Rparen)
		decl.Lparen, decl.Rparen = token.NoPos, token.NoPos
		e.removeLines(e.line(decl.Specs[0].End()), end)
		e.removeLines(start, e.line(decl.Specs[0].Pos()))
	case decl.Doc != nil:
	default:
		return true
	}
	return false
}

// canRemoveParens reports whether the parentheses can be safely removed.
// Binary/unary parens are only stripped in unambiguous single-value contexts.
// Parens around channel/func types, pointer derefs before postfix operators,
// and composite literals in control flow headers are always kept.
func (e *condenser) canRemoveParens(paren *ast.ParenExpr) bool { //nolint:cyclop
	switch paren.X.(type) {
	case *ast.ChanType, *ast.FuncType:
		return false
	case *ast.StarExpr:
		switch e.parent(1).(type) {
		case *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr, *ast.SliceExpr, *ast.CallExpr, *ast.TypeAssertExpr:
			return false
		}
	case *ast.BinaryExpr, *ast.UnaryExpr:
		switch p := e.parent(1).(type) {
		case *ast.AssignStmt:
			return len(p.Rhs) == 1
		case *ast.ValueSpec:
			return len(p.Values) == 1
		case *ast.ReturnStmt:
			return len(p.Results) == 1
		case *ast.CaseClause:
			return len(p.List) == 1
		case *ast.CompositeLit:
			return len(p.Elts) == 1
		case *ast.KeyValueExpr:
			return p.Key != paren
		case *ast.ExprStmt, *ast.ParenExpr:
			return true
		default:
			return false
		}
	}

	for i := len(e.parents) - 2; i >= 0; i-- {
		switch e.parents[i].(type) {
		case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause, *ast.FuncDecl, *ast.FuncLit, *ast.ParenExpr:
			return true
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
			for expr := paren.X; ; {
				switch e := expr.(type) {
				case *ast.SelectorExpr:
					expr = e.X
				case *ast.CallExpr:
					expr = e.Fun
				case *ast.IndexExpr:
					expr = e.X
				case *ast.SliceExpr:
					expr = e.X
				case *ast.TypeAssertExpr:
					expr = e.X
				case *ast.StarExpr:
					expr = e.X
				case *ast.CompositeLit:
					return false
				default:
					return true
				}
			}
		}
	}

	return true
}

// condenseFieldList trims blank lines in a field list and, for type params
// and function params/results/receivers, attempts to collapse it onto a
// single line, merging adjacent fields with the same type.
func (e *condenser) condenseFieldList(list *ast.FieldList) {
	if !list.Opening.IsValid() {
		return
	}

	trim(e, list.Opening, list.Closing, list.List)

	switch e.parent(1).(type) {
	case *ast.StructType, *ast.InterfaceType:
		// Struct fields may have tags, and interface methods are unnamed,
		// so neither can be merged or condensed onto a single line.
		return
	}

	if e.hasComments(list) {
		return
	}

	startLine, endLine := e.line(list.Pos()), e.line(list.End())
	if startLine == endLine {
		mergeFields(list)
		return
	}

	for _, field := range list.List {
		if !e.isSingleLine(field.Type) {
			return
		}
	}

	// Save line table and field names so both can be reverted atomically.
	savedLines := slices.Clone(e.tokenFile.Lines())
	savedFields := slices.Clone(list.List)
	savedNames := make([][]*ast.Ident, len(list.List))
	for i, f := range list.List {
		savedNames[i] = f.Names
	}

	e.removeLines(startLine, endLine)
	mergeFields(list)

	// format.Node can't render a standalone FieldList, so verify against
	// the parent node which IS renderable.
	if !e.canCondense(e.parent(1)) {
		e.tokenFile.SetLines(savedLines)
		list.List = savedFields
		for i, f := range savedFields {
			f.Names = savedNames[i]
		}
	}
}

// condenseCompositeLit trims blank lines and attempts to collapse multi-line
// composite literals onto a single line. Literals with multi-line types are
// not collapsed. Key-value literals (structs/maps) are only condensed when
// the first element shares a line with the opening brace.
func (e *condenser) condenseCompositeLit(lit *ast.CompositeLit) {
	trim(e, lit.Lbrace, lit.Rbrace, lit.Elts)
	if len(lit.Elts) == 0 || e.isSingleLine(lit) || e.hasComments(lit) {
		return
	}

	// Skip key-value literals whose first element is not on the same line as the opening brace.
	if _, kv := lit.Elts[0].(*ast.KeyValueExpr); kv && e.line(lit.Lbrace) != e.line(lit.Elts[0].Pos()) {
		return
	}

	// Skip composite literals where the type spans multiple lines.
	if !e.isSingleLine(lit.Type) {
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

// trim removes blank lines between the delimiters and their nearest children,
// stopping at comments. Empty regions are collapsed.
//
// TODO: convert to a method once https://github.com/golang/go/issues/77273 lands.
func trim[T ast.Node](e *condenser, start, end token.Pos, children []T) {
	if len(children) == 0 && !e.hasCommentsInRange(start, end) {
		e.removeLines(e.line(start), e.line(end))
		return
	}

	trimTop(e, start, end, children)

	// Trim blank lines between the closing delimiter and the last child.
	last := start
	if len(children) > 0 {
		last = children[len(children)-1].End()
	}
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

// trimTop removes blank lines between the opening delimiter and the first
// child, stopping at comments.
//
// TODO: convert to a method once https://github.com/golang/go/issues/77273 lands.
func trimTop[T ast.Node](e *condenser, start, end token.Pos, children []T) {
	first := end
	if len(children) > 0 {
		first = children[0].Pos()
	}
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
	return e.hasCommentsInRange(node.Pos(), node.End())
}

// isSingleLine checks if a node is already on a single line.
func (e *condenser) isSingleLine(node ast.Node) bool {
	return node == nil || e.line(node.Pos()) == e.line(node.End())
}

// parent returns the nth ancestor from the parent stack (0 = self, 1 = parent, 2 = grandparent).
//
//nolint:unparam
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
		length := len(line) + bytes.Count(line, []byte{'\t'})*(e.tabWidth-1)
		if first {
			length += startCol
			first = false
		}
		if length > e.maxLen {
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
	line := e.line(pos)
	var ancestor token.Pos
	for i := len(e.parents) - 1; i >= 0; i-- {
		p := e.parents[i]
		if e.line(p.Pos()) != line {
			break
		}
		ancestor = p.Pos()
	}

	col := e.indentLevel * e.tabWidth
	if ancestor.IsValid() {
		col += int(pos - ancestor)
	}
	return col
}

// condenseNode attempts to condense a node by removing lines between its positions.
// If the condensed result would exceed MaxLen, the line table is restored.
func (e *condenser) condenseNode(node ast.Node) {
	lines := slices.Clone(e.tokenFile.Lines())
	e.removeLines(e.line(node.Pos()), e.line(node.End()))

	if !e.canCondense(node) {
		e.tokenFile.SetLines(lines)
	}
}

// mergeFields merges adjacent fields with the same type (e.g. `a T, b T` → `a, b T`).
func mergeFields(list *ast.FieldList) {
	for i := len(list.List) - 1; i > 0; i-- {
		a, b := list.List[i-1], list.List[i]
		if len(a.Names) > 0 && len(b.Names) > 0 && equalExpr(a.Type, b.Type) {
			a.Names = append(a.Names, b.Names...)
			list.List = slices.Delete(list.List, i, i+1)
		}
	}
}

// equalExpr reports whether two AST type expressions are structurally equal.
func equalExpr(a, b ast.Expr) bool { //nolint:cyclop
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
		return ok && x.Sel.Name == y.Sel.Name && equalExpr(x.X, y.X)
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
		return ok && equalFieldList(x.Methods, y.Methods)
	case *ast.FuncType:
		y, ok := b.(*ast.FuncType)
		return ok && equalFieldList(x.Params, y.Params) && equalFieldList(x.Results, y.Results)
	case *ast.StructType:
		y, ok := b.(*ast.StructType)
		return ok && equalFieldList(x.Fields, y.Fields)
	default:
		return false
	}
}

// equalFieldList reports whether two field lists are structurally equal.
func equalFieldList(a, b *ast.FieldList) bool {
	if a == nil || b == nil {
		return a == b
	}
	return slices.EqualFunc(a.List, b.List, func(x, y *ast.Field) bool {
		return slices.EqualFunc(x.Names, y.Names, func(xi, yi *ast.Ident) bool {
			return xi.Name == yi.Name
		}) && equalExpr(x.Type, y.Type) &&
			(x.Tag == y.Tag || x.Tag != nil && y.Tag != nil && x.Tag.Value == y.Tag.Value)
	})
}
