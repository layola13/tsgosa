// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_arr.go — arr 种全集：字面量/读写/length/声明/解构/for-of-in/方法与高阶内联（step12/13/16/26；esz 恒 4）。
// saLowerVarDeclList lowering 声明表（语句位与 for 初始化位共用）。
// saLowerArrayLiteral lowering 数组字面量（i32/串元，形状证据：封存
// lowerArrayLiteral:8684-8740：`alloc 16` 头 + `alloc len*4` 缓冲 + 逐槽
// `store … as i32` + 头部 ptr/len + `!buf`；spread 经空数组 + push/整片合并
// （封存 lowerArrayLiteral:8680-8708）。
// 元求值与上游 lowerExpr 同形：串元走串位（具化切片 temp 归属，具名直存），
// 嵌套字面量递归（内层头归属，外层存后返前释放）；i32 元沿既有纯临时量口径。
// 头槽一律归属（封存尾 declareOwned(h)；具名绑定消费，余下返前释放）。
// 嵌套数组字面量元递归构造内层 slice 句柄存句柄值（外层 esz 恒 4，与上游
// lowerExpr 递归同形；串句柄混存截断风险由注解门守，见调用方）。
// saArrayLiteralElem 求数组字面量单个普通元（嵌套字面量递归/串位/其余 i32；
// spread 元不在此（调用方走合并通道）；形状证据：封存 lowerArrayLiteral:8709-8735
// 普通元逐元 lowerExpr 同形）。
// saPropArrNest propagates nested-slice marking across handle bindings.
func saPropArrNest(scope *saScope, from, to string) {
	if scope.arrNest == nil || to == "" || from == to {
		return
	}
	if scope.arrNest[from] {
		scope.arrNest[to] = true
	}
}

// saPropArrStr propagates string-element marking across handle bindings
// (mirror of saPropArrNest; element kind only, never ownership).
func saPropArrStr(scope *saScope, from, to string) {
	if scope.arrStr == nil || to == "" || from == to {
		return
	}
	if scope.arrStr[from] {
		scope.arrStr[to] = true
	}
}

// saPropArrElemLayout propagates element instance-layout marking across
// handle bindings (mirror of saPropArrNest; element kind only, never
// ownership). Filter/slice results previously dropped the layout, so
// element field reads and chained higher-order calls on the result
// refused even though the source array carried it.
func saPropArrElemLayout(scope *saScope, from, to string) {
	if scope.arrElemLayout == nil || to == "" || from == to {
		return
	}
	if layout, ok := scope.arrElemLayout[from]; ok {
		scope.arrElemLayout[to] = layout
	}
}

// saMarkArrInnerStr records a nested string-array handle (map init inline).
func saMarkArrInnerStr(scope *saScope, name string) {
	if name == "" {
		return
	}
	if scope.arrInnerStr == nil {
		scope.arrInnerStr = map[string]bool{}
	}
	scope.arrInnerStr[name] = true
}

// saPropArrInnerStr propagates nested-string-element marking across handle
// bindings (mirror of saPropArrStr; element kind only, never ownership).
func saPropArrInnerStr(scope *saScope, from, to string) {
	if scope.arrInnerStr == nil || to == "" || from == to {
		return
	}
	if scope.arrInnerStr[from] {
		scope.arrInnerStr[to] = true
	}
}

// saSeedParamArrNest 播形参嵌套数组标记（`aa: T[][]` 按注解记 arrNest
// （元确为数组），内叶串另记 arrInnerStr；无注解/扁平数组不记，读侧沿旧门；
// 函数/箭头序同形共用，见 saSeedParamArrStr）。
func saSeedParamArrNest(paramNodes []*ast.Node, kinds map[string]string, scope *saScope) {
	for _, pn := range paramNodes {
		if pn == nil {
			continue
		}
		pd := pn.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier || kinds[nm.Text()] != "arr" {
			continue
		}
		if pd.Type == nil {
			continue
		}
		if saIsNestedArrayAnnot(pd.Type) {
			if scope.arrNest == nil {
				scope.arrNest = map[string]bool{}
			}
			scope.arrNest[nm.Text()] = true
		}
		if saIsNestedStrArrayAnnot(pd.Type) {
			saMarkArrInnerStr(scope, nm.Text())
		}
	}
}

// saIsNestedArrayAnnot reports `T[][]` / `Array<T[]>` / `readonly T[][]`
// annotations (elements are arrays; inner kind erased, same granularity as
// upstream; aliases/other generics stay unmarked).
func saIsNestedArrayAnnot(tn *ast.TypeNode) bool {
	if tn == nil {
		return false
	}
	if tn.Kind == ast.KindTypeOperator {
		if to := tn.AsTypeOperatorNode(); to != nil && to.Operator == ast.KindReadonlyKeyword && to.Type != nil {
			return saIsNestedArrayAnnot(to.Type)
		}
		return false
	}
	if tn.Kind == ast.KindArrayType {
		el := tn.AsArrayTypeNode().ElementType
		if el == nil {
			return false
		}
		if el.Kind == ast.KindArrayType {
			return true
		}
		if el.Kind == ast.KindTypeReference {
			if ref := el.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil &&
				ref.TypeName.Kind == ast.KindIdentifier && ref.TypeName.Text() == "Array" {
				return true
			}
		}
		return false
	}
	if tn.Kind == ast.KindTypeReference {
		ref := tn.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeName.Text() != "Array" {
			return false
		}
		if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 1 {
			return false
		}
		arg := ref.TypeArguments.Nodes[0]
		if arg == nil {
			return false
		}
		if arg.Kind == ast.KindArrayType {
			return true
		}
		if arg.Kind == ast.KindTypeReference {
			if aref := arg.AsTypeReferenceNode(); aref != nil && aref.TypeName != nil &&
				aref.TypeName.Kind == ast.KindIdentifier && aref.TypeName.Text() == "Array" {
				return true
			}
		}
		return false
	}
	return false
}

// saSeedParamArrStr 播形参串元标记（`a: string[]` 按注解记 arrStr；
// 无注解/i32 数组不记，读侧沿旧门；函数/箭头序同形共用，见 saSeedParamMapVals）。
func saSeedParamArrStr(paramNodes []*ast.Node, kinds map[string]string, scope *saScope) {
	for _, pn := range paramNodes {
		if pn == nil {
			continue
		}
		pd := pn.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier || kinds[nm.Text()] != "arr" {
			continue
		}
		if pd.Type == nil {
			continue
		}
		if saIsStringArrayAnnot(pd.Type) {
			saMarkArrStr(scope, nm.Text())
		}
	}
}

// saMarkArrStr records a string-element array handle (map init inline).
func saMarkArrStr(scope *saScope, name string) {
	if name == "" {
		return
	}
	if scope.arrStr == nil {
		scope.arrStr = map[string]bool{}
	}
	scope.arrStr[name] = true
}

// saArrElemLayoutOfType resolves an array annotation to a recorded element
// instance layout name: T[] / readonly T[] / Array<T> / ReadonlyArray<T>
// where T is a recorded class/interface (including a type-alias literal,
// which saRecordTypeAlias stores under the alias name), or an inline type
// literal (synthesized anon layout, seed time only). Anything else is not
// an instance-element array. JEV inline_loop_eq 0.98 / reconcile_layout 1.0.
func saArrElemLayoutOfType(t *ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if t == nil {
		return "", false
	}
	switch t.Kind {
	case ast.KindParenthesizedType:
		if pt := t.AsParenthesizedTypeNode(); pt != nil {
			return saArrElemLayoutOfType(pt.Type, classes, aliasOf)
		}
	case ast.KindTypeOperator:
		if to := t.AsTypeOperatorNode(); to != nil && to.Operator == ast.KindReadonlyKeyword && to.Type != nil {
			return saArrElemLayoutOfType(to.Type, classes, aliasOf)
		}
	case ast.KindArrayType:
		if at := t.AsArrayTypeNode(); at != nil {
			return saArrElemLayoutOfElem(at.ElementType, classes, aliasOf)
		}
	case ast.KindTypeReference:
		if ref := t.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			if ref.TypeName.Text() == "Array" || ref.TypeName.Text() == "ReadonlyArray" {
				if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) == 1 {
					return saArrElemLayoutOfElem(ref.TypeArguments.Nodes[0], classes, aliasOf)
				}
			}
		}
	}
	return "", false
}

func saArrElemLayoutOfElem(el *ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if el == nil {
		return "", false
	}
	switch el.Kind {
	case ast.KindParenthesizedType:
		if pt := el.AsParenthesizedTypeNode(); pt != nil {
			return saArrElemLayoutOfElem(pt.Type, classes, aliasOf)
		}
	case ast.KindTypeOperator:
		if to := el.AsTypeOperatorNode(); to != nil && to.Operator == ast.KindReadonlyKeyword && to.Type != nil {
			return saArrElemLayoutOfElem(to.Type, classes, aliasOf)
		}
	case ast.KindTypeReference:
		if ref := el.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			name := ref.TypeName.Text()
			if _, ok := classes[name]; ok {
				return name, true
			}
			if lname, ok := saSynthMergedLayout(el, aliasOf, classes); ok {
				return lname, true
			}
		}
	case ast.KindTypeLiteral:
		return saSynthAnonLayout(el, classes)
	}
	return "", false
}

// saSeedParamArrElemLayout records param array element layouts (mirror of
// saSeedParamArrStr); called at function entry alongside the other seeds.
func saSeedParamArrElemLayout(paramNodes []*ast.Node, kinds map[string]string, scope *saScope) {
	for _, pn := range paramNodes {
		if pn == nil {
			continue
		}
		pd := pn.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier || kinds[nm.Text()] != "arr" {
			continue
		}
		if pd.Type == nil {
			continue
		}
		if layout, ok := saArrElemLayoutOfType(pd.Type, scope.classes, scope.aliasOf); ok {
			if scope.arrElemLayout == nil {
				scope.arrElemLayout = map[string]string{}
			}
			scope.arrElemLayout[nm.Text()] = layout
		}
	}
}

// saFindEqRhsHoistable reports whether the equality operand can be
// evaluated once before the find loop: it must not mention the callback
// parameter (element-dependent) and must contain no call or assignment
// (side effects that JS would repeat per element). Identifiers, literals
// and outer field reads are loop-invariant: the loop assigns nothing but
// its own index/slot temporaries.
func saFindEqRhsHoistable(n *ast.Node, paramName string) bool {
	if n == nil {
		return false
	}
	if n.Kind == ast.KindIdentifier && n.Text() == paramName {
		return false
	}
	if n.Kind == ast.KindCallExpression {
		return false
	}
	if n.Kind == ast.KindBinaryExpression {
		if be := n.AsBinaryExpression(); be != nil && be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindEqualsToken {
			return false
		}
	}
	ok := true
	n.ForEachChild(func(c *ast.Node) bool {
		if !saFindEqRhsHoistable(c, paramName) {
			ok = false
			return true
		}
		return false
	})
	return ok
}

// saFindReceiverLayout resolves the element instance layout for a .find
// receiver expression, purely (no emission): annotated array bindings via
// scope.arrElemLayout, top-level const object arrays via their snapshot
// layout, struct array fields via saStructArrElemLayout. Anything else has
// no known element layout and the equality find refuses loudly.
func saFindReceiverLayout(base *ast.Node, scope *saScope) (string, bool) {
	if base == nil {
		return "", false
	}
	u := saUnwrapTransparent(base)
	if u == nil {
		return "", false
	}
	if u.Kind == ast.KindIdentifier {
		nm := u.Text()
		if layout, ok := scope.arrElemLayout[nm]; ok {
			return layout, true
		}
		if arr, ok := saTopArrLookup(scope, u); ok && arr.layout != "" {
			return arr.layout, true
		}
		return "", false
	}
	if u.Kind == ast.KindPropertyAccessExpression {
		if _, _, layout, ok := saTopObjArrField(scope, u); ok {
			return layout, true
		}
		return saStructArrElemLayout(u, scope)
	}
	return "", false
}

// saFindEqGuard detects a find callback whose body is exactly one equality
// (===/==/!==/!=) between a field of its first parameter and another
// expression: (x) => x.field === other, or a block with a single return of
// that. It returns the parameter name, field name, negation flag, the other
// operand node, and true. The other operand must not be the parameter
// itself (an element-handle comparison is not an equality find).
func saFindEqGuard(cb *ast.Node) (string, string, bool, *ast.Node, bool) {
	if cb == nil {
		return "", "", false, nil, false
	}
	if cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression {
		return "", "", false, nil, false
	}
	params := cb.Parameters()
	if len(params) < 1 {
		return "", "", false, nil, false
	}
	pd := params[0].AsParameterDeclaration()
	if pd == nil {
		return "", "", false, nil, false
	}
	nm := pd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", "", false, nil, false
	}
	paramName := nm.Text()
	body := cb.Body()
	if body == nil {
		return "", "", false, nil, false
	}
	expr := body
	if body.Kind == ast.KindBlock {
		bd := body.AsBlock()
		if bd == nil || bd.Statements == nil || len(bd.Statements.Nodes) != 1 {
			return "", "", false, nil, false
		}
		if bd.Statements.Nodes[0].Kind != ast.KindReturnStatement {
			return "", "", false, nil, false
		}
		rs := bd.Statements.Nodes[0].AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return "", "", false, nil, false
		}
		expr = rs.Expression
	}
	expr = saUnwrapTransparent(expr)
	if expr == nil || expr.Kind != ast.KindBinaryExpression {
		return "", "", false, nil, false
	}
	be := expr.AsBinaryExpression()
	if be == nil {
		return "", "", false, nil, false
	}
	opKind := saBinaryOpKind(be)
	neg := false
	switch opKind {
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
		neg = false
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
		neg = true
	default:
		return "", "", false, nil, false
	}
	var fieldName string
	var other *ast.Node
	for _, side := range []*ast.Node{be.Left, be.Right} {
		if side != nil && side.Kind == ast.KindPropertyAccessExpression {
			pa := side.AsPropertyAccessExpression()
			if pa != nil && pa.QuestionDotToken == nil && pa.Name() != nil && pa.Name().Kind == ast.KindIdentifier &&
				pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == paramName {
				if fieldName != "" {
					return "", "", false, nil, false
				}
				fieldName = pa.Name().Text()
				continue
			}
		}
		if other != nil {
			return "", "", false, nil, false
		}
		other = side
	}
	if fieldName == "" || other == nil {
		return "", "", false, nil, false
	}
	if other.Kind == ast.KindIdentifier && other.Text() == paramName {
		return "", "", false, nil, false
	}
	return paramName, fieldName, neg, other, true
}

// saOrDisjunct is one arm of a .find OR predicate (JEV inline_loop_or
// 0.98): either a field equality (isIncludes false) or an optional-chain
// string-array includes check (isIncludes true). other is the equality
// right-hand side or the includes argument; neg applies to equalities only.
type saOrDisjunct struct {
	isIncludes bool
	field      string
	neg        bool
	other      *ast.Node
}

// saParseEqDisjunct parses one equality between a field of paramName and
// another expression (either side may hold the field), mirroring
// saFindEqGuard's single-equality shape.
func saParseEqDisjunct(expr *ast.Node, paramName string) (string, bool, *ast.Node, bool) {
	expr = saUnwrapTransparent(expr)
	if expr == nil || expr.Kind != ast.KindBinaryExpression {
		return "", false, nil, false
	}
	be := expr.AsBinaryExpression()
	if be == nil {
		return "", false, nil, false
	}
	neg := false
	switch saBinaryOpKind(be) {
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
		neg = true
	default:
		return "", false, nil, false
	}
	var fieldName string
	var other *ast.Node
	for _, side := range []*ast.Node{be.Left, be.Right} {
		if side != nil && side.Kind == ast.KindPropertyAccessExpression {
			pa := side.AsPropertyAccessExpression()
			if pa != nil && pa.QuestionDotToken == nil && pa.Name() != nil && pa.Name().Kind == ast.KindIdentifier &&
				pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == paramName {
				if fieldName != "" {
					return "", false, nil, false
				}
				fieldName = pa.Name().Text()
				continue
			}
		}
		if other != nil {
			return "", false, nil, false
		}
		other = side
	}
	if fieldName == "" || other == nil {
		return "", false, nil, false
	}
	if other.Kind == ast.KindIdentifier && other.Text() == paramName {
		return "", false, nil, false
	}
	return fieldName, neg, other, true
}

// saParseIncludesDisjunct parses `param.field?.includes(arg)`: an optional
// chain over a field of paramName, exactly one argument, not the parameter
// itself. The ?. token may sit on the method property access or on the
// call node depending on the parser's chain representation; either counts.
func saParseIncludesDisjunct(expr *ast.Node, paramName string) (string, *ast.Node, bool) {
	expr = saUnwrapTransparent(expr)
	if expr == nil || expr.Kind != ast.KindCallExpression {
		return "", nil, false
	}
	ce := expr.AsCallExpression()
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", nil, false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Text() != "includes" {
		return "", nil, false
	}
	if pa.QuestionDotToken == nil && ce.QuestionDotToken == nil {
		return "", nil, false
	}
	if pa.Expression == nil || pa.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", nil, false
	}
	base := pa.Expression.AsPropertyAccessExpression()
	if base == nil || base.QuestionDotToken != nil || base.Name() == nil || base.Name().Kind != ast.KindIdentifier ||
		base.Expression == nil || base.Expression.Kind != ast.KindIdentifier || base.Expression.Text() != paramName {
		return "", nil, false
	}
	if ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 {
		return "", nil, false
	}
	arg := ce.Arguments.Nodes[0]
	if arg == nil || (arg.Kind == ast.KindIdentifier && arg.Text() == paramName) {
		return "", nil, false
	}
	return base.Name().Text(), arg, true
}

// saFindOrGuard detects a .find callback whose body is a || chain (or a
// single includes check) of equality disjuncts and optional-chain includes
// disjuncts over the first parameter. A single equality is left to
// saFindEqGuard's branch, so this guard declines it.
func saFindOrGuard(cb *ast.Node) (string, []saOrDisjunct, bool) {
	if cb == nil {
		return "", nil, false
	}
	if cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression {
		return "", nil, false
	}
	params := cb.Parameters()
	if len(params) < 1 {
		return "", nil, false
	}
	pd := params[0].AsParameterDeclaration()
	if pd == nil {
		return "", nil, false
	}
	nm := pd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", nil, false
	}
	paramName := nm.Text()
	body := cb.Body()
	if body == nil {
		return "", nil, false
	}
	expr := body
	if body.Kind == ast.KindBlock {
		bd := body.AsBlock()
		if bd == nil || bd.Statements == nil || len(bd.Statements.Nodes) != 1 {
			return "", nil, false
		}
		if bd.Statements.Nodes[0].Kind != ast.KindReturnStatement {
			return "", nil, false
		}
		rs := bd.Statements.Nodes[0].AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return "", nil, false
		}
		expr = rs.Expression
	}
	var parts []*ast.Node
	var flatten func(n *ast.Node) bool
	flatten = func(n *ast.Node) bool {
		n = saUnwrapTransparent(n)
		if n != nil && n.Kind == ast.KindBinaryExpression {
			if be := n.AsBinaryExpression(); be != nil && saBinaryOpKind(be) == ast.KindBarBarToken {
				return flatten(be.Left) && flatten(be.Right)
			}
		}
		parts = append(parts, n)
		return true
	}
	if !flatten(expr) || len(parts) == 0 {
		return "", nil, false
	}
	if len(parts) == 1 {
		if _, _, _, ok := saParseEqDisjunct(parts[0], paramName); ok {
			return "", nil, false
		}
	}
	var out []saOrDisjunct
	for _, part := range parts {
		if f, neg, other, ok := saParseEqDisjunct(part, paramName); ok {
			out = append(out, saOrDisjunct{field: f, neg: neg, other: other})
			continue
		}
		if f, arg, ok := saParseIncludesDisjunct(part, paramName); ok {
			out = append(out, saOrDisjunct{isIncludes: true, field: f, other: arg})
			continue
		}
		return "", nil, false
	}
	return paramName, out, true
}

// saEmitEqDisjunct emits one equality disjunct's evaluation for element
// handle el, branching to trueL on match and falseL otherwise. It mirrors
// the equality-find loop's arms exactly, including the zero-handle arm for
// missing str fields (a zero field equals only a zero right-hand side, so
// ts_str_equals never sees a null handle) and the both-arms release rule
// for per-iteration owned right-hand sides (hoisted handles, keyed by
// hoistKey in hoisted, are released once after the loop by the caller).
func saEmitEqDisjunct(w printer.EmitTextWriter, el string, def *saClassDef, d saOrDisjunct, hoisted map[string]string, hoistKey, trueL, falseL string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int) string {
	fkind, ok := def.fkinds[d.field]
	if !ok {
		return "find OR equality field not in element layout"
	}
	off, ok := def.offsets[d.field]
	if !ok {
		return "find OR equality field has no layout offset"
	}
	if fkind == "str" {
		var rhs string
		rhsPerIter := false
		if h, ok := hoisted[hoistKey]; ok {
			rhs = h
		} else {
			h, msg := saEvalStr(w, d.other, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
			rhs, rhsPerIter = h, true
		}
		rhsOwned := false
		if rhsPerIter {
			if b := saOwnOf(scope, rhs); b != nil && b.heap && !b.consumed && !b.released {
				rhsOwned = true
			}
		}
		releaseRhs := func() {
			if rhsOwned {
				w.Write(fmt.Sprintf("  !%s\n", rhs))
			}
		}
		fh := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", fh, el, off))
		fz := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", fz, fh))
		fzL := fmt.Sprintf("L_feq_fz_%d", *nextLabel)
		*nextLabel++
		fnzL := fmt.Sprintf("L_feq_fnz_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", fz, fzL, fnzL))
		w.Write(fmt.Sprintf("%s:\n", fzL))
		eqv := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", eqv, rhs))
		if d.neg {
			pz := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = eq %s, 0\n", pz, eqv))
			releaseRhs()
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pz, trueL, falseL))
		} else {
			releaseRhs()
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", eqv, trueL, falseL))
		}
		w.Write(fmt.Sprintf("%s:\n", fnzL))
		pred := saStringContentEq(w, fh, rhs, d.neg, scope, nextTemp)
		releaseRhs()
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pred, trueL, falseL))
		if rhsOwned {
			if b := saOwnOf(scope, rhs); b != nil {
				b.released = true
			}
		}
		return ""
	}
	if fkind == "i32" || fkind == "bool" {
		fv := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", fv, el, off))
		rv, msg := saEvalI32(w, d.other, scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		op := "eq"
		if d.neg {
			op = "ne"
		}
		pred := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", pred, op, fv, rv))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pred, trueL, falseL))
		return ""
	}
	return "find OR equality field kind not supported: " + fkind
}

// saEmitIncludesDisjunct emits one optional-chain includes disjunct's
// evaluation for element handle el, branching to trueL when the element's
// string-array field contains the argument and to falseL otherwise. A
// missing (zero) field handle means false, never a fault. The inner scan
// reuses the string-array header layout (+0 data ptr, +8 u64 length) and
// 4-byte element slots, exactly as saArrElemAt reads them.
func saEmitIncludesDisjunct(w printer.EmitTextWriter, el string, def *saClassDef, d saOrDisjunct, hoisted map[string]string, hoistKey, trueL, falseL string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int) string {
	fkind, ok := def.fkinds[d.field]
	if !ok {
		return "find OR includes field not in element layout"
	}
	if fkind != "arr" {
		return "find OR includes field is not a string array field"
	}
	off, ok := def.offsets[d.field]
	if !ok {
		return "find OR includes field has no layout offset"
	}
	var rhs string
	rhsPerIter := false
	if h, ok := hoisted[hoistKey]; ok {
		rhs = h
	} else {
		h, msg := saEvalStr(w, d.other, scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		rhs, rhsPerIter = h, true
	}
	rhsOwned := false
	if rhsPerIter {
		if b := saOwnOf(scope, rhs); b != nil && b.heap && !b.consumed && !b.released {
			rhsOwned = true
		}
	}
	releaseRhs := func() {
		if rhsOwned {
			w.Write(fmt.Sprintf("  !%s\n", rhs))
		}
	}
	arrH := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", arrH, el, off))
	z := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", z, arrH))
	zL := fmt.Sprintf("L_inc_z_%d", *nextLabel)
	*nextLabel++
	nzL := fmt.Sprintf("L_inc_nz_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", z, zL, nzL))
	w.Write(fmt.Sprintf("%s:\n", zL))
	releaseRhs()
	w.Write(fmt.Sprintf("  jmp %s\n", falseL))
	w.Write(fmt.Sprintf("%s:\n", nzL))
	ln2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln2, arrH))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, arrH))
	j := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", j))
	topL := fmt.Sprintf("L_inc_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_inc_body_%d", *nextLabel)
	*nextLabel++
	hitL := fmt.Sprintf("L_inc_hit_%d", *nextLabel)
	*nextLabel++
	nextJL := fmt.Sprintf("L_inc_next_%d", *nextLabel)
	*nextLabel++
	missL := fmt.Sprintf("L_inc_miss_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, j, ln2))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, missL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	elemH := saArrElemAt(w, data, j, nextTemp)
	pred := saStringContentEq(w, elemH, rhs, false, scope, nextTemp)
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pred, hitL, nextJL))
	w.Write(fmt.Sprintf("%s:\n", hitL))
	releaseRhs()
	w.Write(fmt.Sprintf("  jmp %s\n", trueL))
	w.Write(fmt.Sprintf("%s:\n", nextJL))
	jnext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", jnext, j))
	w.Write(fmt.Sprintf("  %s = %s\n", j, jnext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", missL))
	releaseRhs()
	w.Write(fmt.Sprintf("  jmp %s\n", falseL))
	if rhsOwned {
		if b := saOwnOf(scope, rhs); b != nil {
			b.released = true
		}
	}
	return ""
}

// saBoolPredTrimLeaf extracts a trim-call leaf over a parameter-rooted
// field chain (JEV inline_loop_method_calls 0.98): returns the receiver
// chain node and the trim mode (0/1/2). Other call shapes return ok=false.
func saBoolPredTrimLeaf(n *ast.Node, paramName string) (*ast.Node, string, bool) {
	if n == nil || n.Kind != ast.KindCallExpression {
		return nil, "", false
	}
	ce := n.AsCallExpression()
	if ce == nil || ce.QuestionDotToken != nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return nil, "", false
	}
	if ce.Arguments != nil && len(ce.Arguments.Nodes) != 0 {
		return nil, "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.QuestionDotToken != nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return nil, "", false
	}
	mode := ""
	switch pa.Name().Text() {
	case "trim":
		mode = "0"
	case "trimStart":
		mode = "1"
	case "trimEnd":
		mode = "2"
	default:
		return nil, "", false
	}
	if _, msg := saBoolPredChainFields(pa.Expression, paramName); msg != "" {
		return nil, "", false
	}
	return pa.Expression, mode, true
}

// saBoolPredShape reports whether n is a boolean predicate over paramName:
// a field-chain read rooted at the parameter, a ! negation, or a &&/||
// combination of such predicates. Pure reads only, so eager evaluation of
// both sides of &&/|| is semantically identical to short-circuiting.
func saBoolPredShape(n *ast.Node, paramName string) bool {
	n = saUnwrapTransparent(n)
	if n == nil {
		return false
	}
	switch n.Kind {
	case ast.KindPrefixUnaryExpression:
		un := n.AsPrefixUnaryExpression()
		if un == nil || un.Operator != ast.KindExclamationToken {
			return false
		}
		return saBoolPredShape(un.Operand, paramName)
	case ast.KindBinaryExpression:
		be := n.AsBinaryExpression()
		if be == nil {
			return false
		}
		op := saBinaryOpKind(be)
		if op != ast.KindBarBarToken && op != ast.KindAmpersandAmpersandToken {
			return false
		}
		return saBoolPredShape(be.Left, paramName) && saBoolPredShape(be.Right, paramName)
	case ast.KindCallExpression:
		_, _, ok := saBoolPredTrimLeaf(n, paramName)
		return ok
	case ast.KindPropertyAccessExpression:
		_, msg := saBoolPredChainFields(n, paramName)
		return msg == ""
	}
	return false
}

// saBoolPredChainFields collects the field names of a property-access chain
// rooted at paramName, outermost last (e.g. capabilities, chatRuntime).
func saBoolPredChainFields(n *ast.Node, paramName string) ([]string, string) {
	var fields []string
	cur := n
	for cur != nil && cur.Kind == ast.KindPropertyAccessExpression {
		pa := cur.AsPropertyAccessExpression()
		if pa == nil || pa.QuestionDotToken != nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			return nil, "filter bool predicate has an unsupported field access"
		}
		fields = append([]string{pa.Name().Text()}, fields...)
		cur = saUnwrapTransparent(pa.Expression)
	}
	if cur == nil || cur.Kind != ast.KindIdentifier || cur.Text() != paramName {
		return nil, "filter bool predicate is not rooted at the callback parameter"
	}
	if len(fields) == 0 {
		return nil, "filter bool predicate has no field"
	}
	return fields, ""
}

// saBoolPredFieldLeaf walks a field chain through layout defs and returns
// the leaf field's kind ("" plus a message on failure). Intermediate fields
// must be inst fields with a recorded sub-layout.
func saBoolPredFieldLeaf(n *ast.Node, paramName string, def *saClassDef, scope *saScope) (string, string) {
	fields, msg := saBoolPredChainFields(n, paramName)
	if msg != "" {
		return "", msg
	}
	curDef := def
	for i, fname := range fields {
		fk, ok := curDef.fkinds[fname]
		if !ok {
			return "", "filter bool predicate field not in element layout"
		}
		if i < len(fields)-1 {
			if fk != "inst" {
				return "", "filter bool predicate field is not a nested instance"
			}
			sub, ok := curDef.fsub[fname]
			if !ok {
				return "", "filter bool predicate nested field has no sub-layout"
			}
			next, ok := scope.classes[sub]
			if !ok || next == nil {
				return "", "filter bool predicate nested layout not recorded"
			}
			curDef = next
			continue
		}
		return fk, ""
	}
	return "", "filter bool predicate has no field"
}

// saCheckBoolPred validates a filter boolean predicate against the element
// layout: every leaf must be a bool or i32 field (i32 truthiness is
// nonzero, matching JS number truthiness for 0/1-valued fields).
func saCheckBoolPred(n *ast.Node, paramName string, def *saClassDef, scope *saScope) string {
	n = saUnwrapTransparent(n)
	if n == nil {
		return "filter bool predicate is empty"
	}
	switch n.Kind {
	case ast.KindPrefixUnaryExpression:
		un := n.AsPrefixUnaryExpression()
		if un == nil || un.Operator != ast.KindExclamationToken {
			return "filter bool predicate has unsupported unary operator"
		}
		return saCheckBoolPred(un.Operand, paramName, def, scope)
	case ast.KindBinaryExpression:
		be := n.AsBinaryExpression()
		if be == nil {
			return "filter bool predicate has no operator"
		}
		op := saBinaryOpKind(be)
		if op != ast.KindBarBarToken && op != ast.KindAmpersandAmpersandToken {
			return "filter bool predicate has unsupported binary operator"
		}
		if msg := saCheckBoolPred(be.Left, paramName, def, scope); msg != "" {
			return msg
		}
		return saCheckBoolPred(be.Right, paramName, def, scope)
	case ast.KindCallExpression:
		recv, _, ok := saBoolPredTrimLeaf(n, paramName)
		if !ok {
			return "filter bool predicate shape not supported"
		}
		kind, msg := saBoolPredFieldLeaf(recv, paramName, def, scope)
		if msg != "" {
			return msg
		}
		if kind != "str" {
			return "filter bool predicate trim field kind not supported: " + kind
		}
		return ""
	case ast.KindPropertyAccessExpression:
		kind, msg := saBoolPredFieldLeaf(n, paramName, def, scope)
		if msg != "" {
			return msg
		}
		if kind != "bool" && kind != "i32" {
			return "filter bool predicate field kind not supported: " + kind
		}
		return ""
	}
	return "filter bool predicate shape not supported"
}

// saEvalBoolPred evaluates a validated filter boolean predicate for element
// handle el to an i32 temp (0/1). Field chains load through nested instance
// ptr slots; ! inverts; &&/|| combine eagerly (pure reads, no side effects).
func saEvalBoolPred(w printer.EmitTextWriter, n *ast.Node, paramName string, el string, def *saClassDef, scope *saScope, nextTemp *int) (string, string) {
	n = saUnwrapTransparent(n)
	if n == nil {
		return "", "filter bool predicate is empty"
	}
	switch n.Kind {
	case ast.KindPrefixUnaryExpression:
		un := n.AsPrefixUnaryExpression()
		if un == nil || un.Operator != ast.KindExclamationToken {
			return "", "filter bool predicate has unsupported unary operator"
		}
		v, msg := saEvalBoolPred(w, un.Operand, paramName, el, def, scope, nextTemp)
		if msg != "" {
			return "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", t, v))
		return t, ""
	case ast.KindBinaryExpression:
		be := n.AsBinaryExpression()
		if be == nil {
			return "", "filter bool predicate has no operator"
		}
		op := saBinaryOpKind(be)
		if op != ast.KindBarBarToken && op != ast.KindAmpersandAmpersandToken {
			return "", "filter bool predicate has unsupported binary operator"
		}
		l, msg := saEvalBoolPred(w, be.Left, paramName, el, def, scope, nextTemp)
		if msg != "" {
			return "", msg
		}
		r, msg := saEvalBoolPred(w, be.Right, paramName, el, def, scope, nextTemp)
		if msg != "" {
			return "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		sop := "or"
		if op == ast.KindAmpersandAmpersandToken {
			sop = "and"
		}
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, sop, l, r))
		return t, ""
	case ast.KindCallExpression:
		recv, mode, ok := saBoolPredTrimLeaf(n, paramName)
		if !ok {
			return "", "filter bool predicate shape not supported"
		}
		fields, msg := saBoolPredChainFields(recv, paramName)
		if msg != "" {
			return "", msg
		}
		handle := el
		curDef := def
		for i, fname := range fields {
			off, ok := curDef.offsets[fname]
			if !ok {
				return "", "filter bool predicate field has no layout offset"
			}
			if i < len(fields)-1 {
				nh := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", nh, handle, off))
				next, ok := scope.classes[curDef.fsub[fname]]
				if !ok || next == nil {
					return "", "filter bool predicate nested layout not recorded"
				}
				handle = nh
				curDef = next
				continue
			}
			fh := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", fh, handle, off))
			// Trimmed-string truthiness: a 0 (missing) field handle is
			// falsy; otherwise trim exactly via @ts_str_trim and test
			// the trimmed length at header +8 (string/array header
			// layout). The trimmed handle is released on the single
			// straight-line trim path before the join, mirroring the
			// checked-index slot discipline for per-iteration temps.
			slot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
			saOwnTemp(scope, slot)
			w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
			fz := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = eq %s, 0\n", fz, fh))
			zeroL := fmt.Sprintf("L_trimf_zero_%d", *scope.nextLabel)
			*scope.nextLabel++
			trimL := fmt.Sprintf("L_trimf_trim_%d", *scope.nextLabel)
			*scope.nextLabel++
			endL := fmt.Sprintf("L_trimf_end_%d", *scope.nextLabel)
			*scope.nextLabel++
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", fz, zeroL, trimL))
			w.Write(fmt.Sprintf("%s:\n", zeroL))
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
			w.Write(fmt.Sprintf("%s:\n", trimL))
			scope.addImport("sa_std/ts_string.sa")
			bp := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", bp, fh))
			bl := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", bl, fh))
			out := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @ts_str_trim(%s, %s, %s)\n", out, bp, bl, mode))
			saOwnTemp(scope, out)
			tl := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", tl, out))
			tv := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = ne %s, 0\n", tv, tl))
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, tv))
			saReleaseOwnedTemp(w, scope, out)
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
			w.Write(fmt.Sprintf("%s:\n", endL))
			res := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", res, slot))
			saReleaseOwnedTemp(w, scope, slot)
			return res, ""
		}
		return "", "filter bool predicate has no field"
	case ast.KindPropertyAccessExpression:
		fields, msg := saBoolPredChainFields(n, paramName)
		if msg != "" {
			return "", msg
		}
		handle := el
		curDef := def
		for i, fname := range fields {
			off, ok := curDef.offsets[fname]
			if !ok {
				return "", "filter bool predicate field has no layout offset"
			}
			if i < len(fields)-1 {
				nh := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", nh, handle, off))
				next, ok := scope.classes[curDef.fsub[fname]]
				if !ok || next == nil {
					return "", "filter bool predicate nested layout not recorded"
				}
				handle = nh
				curDef = next
				continue
			}
			v := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", v, handle, off))
			return v, ""
		}
		return "", "filter bool predicate has no field"
	}
	return "", "filter bool predicate shape not supported"
}

// saFilterBoolGuard extracts a .filter callback's boolean predicate: the
// first parameter name and the body expression (direct or single-return
// block), claimed only when the body is a boolean-predicate shape over the
// parameter (field chains, !, &&, ||, trim-call leaves). Other callbacks fall through to the
// generic filter path unchanged.
func saFilterBoolGuard(cb *ast.Node) (string, *ast.Node, bool) {
	if cb == nil {
		return "", nil, false
	}
	if cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression {
		return "", nil, false
	}
	params := cb.Parameters()
	if len(params) < 1 {
		return "", nil, false
	}
	pd := params[0].AsParameterDeclaration()
	if pd == nil {
		return "", nil, false
	}
	nm := pd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", nil, false
	}
	paramName := nm.Text()
	body := cb.Body()
	if body == nil {
		return "", nil, false
	}
	expr := body
	if body.Kind == ast.KindBlock {
		bd := body.AsBlock()
		if bd == nil || bd.Statements == nil || len(bd.Statements.Nodes) != 1 {
			return "", nil, false
		}
		if bd.Statements.Nodes[0].Kind != ast.KindReturnStatement {
			return "", nil, false
		}
		rs := bd.Statements.Nodes[0].AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return "", nil, false
		}
		expr = rs.Expression
	}
	expr = saUnwrapTransparent(expr)
	if !saBoolPredShape(expr, paramName) {
		return "", nil, false
	}
	return paramName, expr, true
}

// saMarkArrJsVal records a jsval-element array handle (mirror of saMarkArrStr).
func saMarkArrJsVal(scope *saScope, name string) {
	if name == "" {
		return
	}
	if scope.arrJsVal == nil {
		scope.arrJsVal = map[string]bool{}
	}
	scope.arrJsVal[name] = true
}

// saPropArrJsVal propagates jsval-element marking across handle bindings.
func saPropArrJsVal(scope *saScope, from, to string) {
	if scope.arrJsVal == nil || to == "" || from == to {
		return
	}
	if scope.arrJsVal[from] {
		scope.arrJsVal[to] = true
	}
}

// saSeedParamArrJsVal seeds jsval-element marking for unknown[]/any[] params.
func saSeedParamArrJsVal(paramNodes []*ast.Node, kinds map[string]string, scope *saScope) {
	for _, pn := range paramNodes {
		if pn == nil {
			continue
		}
		pd := pn.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier || kinds[nm.Text()] != "arr" {
			continue
		}
		if pd.Type == nil {
			continue
		}
		if saIsJsValArrayAnnot(pd.Type) {
			saMarkArrJsVal(scope, nm.Text())
		}
	}
}

// saIsJsValArrayAnnot reports unknown[]/any[]/Array<unknown>/Array<any> annotations.
func saIsJsValArrayAnnot(tn *ast.TypeNode) bool {
	if tn == nil {
		return false
	}
	if tn.Kind == ast.KindArrayType {
		el := tn.AsArrayTypeNode().ElementType
		return el != nil && (el.Kind == ast.KindUnknownKeyword || el.Kind == ast.KindAnyKeyword)
	}
	if tn.Kind == ast.KindTypeReference {
		ref := tn.AsTypeReferenceNode()
		if ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier &&
			(ref.TypeName.Text() == "Array" || ref.TypeName.Text() == "ReadonlyArray") &&
			ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) == 1 && ref.TypeArguments.Nodes[0] != nil {
			el := ref.TypeArguments.Nodes[0]
			return el.Kind == ast.KindUnknownKeyword || el.Kind == ast.KindAnyKeyword
		}
	}
	return false
}

// saIsStringArrayAnnot reports `string[]` / `readonly string[]` /
// `Array<string>` annotations (element-kind source for the arrStr mark;
// mirrors upstream saNameOfType string->ptr element mapping at
// trackBinding:1638-1641; aliases/other generics stay unmarked).
func saIsStringArrayAnnot(tn *ast.TypeNode) bool {
	if tn == nil {
		return false
	}
	if tn.Kind == ast.KindTypeOperator {
		if to := tn.AsTypeOperatorNode(); to != nil && to.Operator == ast.KindReadonlyKeyword && to.Type != nil {
			return saIsStringArrayAnnot(to.Type)
		}
		return false
	}
	if tn.Kind == ast.KindArrayType {
		if el := tn.AsArrayTypeNode().ElementType; el != nil && el.Kind == ast.KindStringKeyword {
			return true
		}
		return false
	}
	if tn.Kind == ast.KindTypeReference {
		ref := tn.AsTypeReferenceNode()
		// 限定名先守（`NS.Array<string>` TypeName.Text 会 panic，0 崩溃铁律；见 step376）。
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeName.Text() != "Array" {
			return false
		}
		if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 1 {
			return false
		}
		return ref.TypeArguments.Nodes[0].Kind == ast.KindStringKeyword
	}
	return false
}

// saIsNestedStrArrayAnnot reports `string[][]` / `readonly string[][]` /
// `Array<string[]>` annotations (inner-leaf-kind source for the arrInnerStr
// mark; mirror of saIsStringArrayAnnot one level down; deeper nesting and
// other generics stay unmarked).
func saIsNestedStrArrayAnnot(tn *ast.TypeNode) bool {
	if tn == nil {
		return false
	}
	if tn.Kind == ast.KindTypeOperator {
		if to := tn.AsTypeOperatorNode(); to != nil && to.Operator == ast.KindReadonlyKeyword && to.Type != nil {
			return saIsNestedStrArrayAnnot(to.Type)
		}
		return false
	}
	if tn.Kind == ast.KindArrayType {
		if el := tn.AsArrayTypeNode().ElementType; el != nil {
			return saIsStringArrayAnnot(el)
		}
		return false
	}
	if tn.Kind == ast.KindTypeReference {
		ref := tn.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeName.Text() != "Array" {
			return false
		}
		if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 1 {
			return false
		}
		return saIsStringArrayAnnot(ref.TypeArguments.Nodes[0])
	}
	return false
}

// saLiteralIsNestedStrArray pre-scans array literals for all-str inner
// arrays (each element a non-empty all-string literal; empty/nested-deeper/
// spread/omitted never mark; bindings propagate at declaration).
func saLiteralIsNestedStrArray(n *ast.Node, scope *saScope) bool {
	if n == nil || n.Kind != ast.KindArrayLiteralExpression {
		return false
	}
	al := n.AsArrayLiteralExpression()
	if al == nil || al.Elements == nil || len(al.Elements.Nodes) == 0 {
		return false
	}
	for _, el := range al.Elements.Nodes {
		if el == nil || el.Kind != ast.KindArrayLiteralExpression {
			return false
		}
		if !saLiteralIsStrArray(el, scope) {
			return false
		}
	}
	return true
}

// saLiteralIsStrArray pre-scans array literals for all-string elements
// (empty/spread/nested/omitted never mark; bindings propagate at declaration).
func saLiteralIsStrArray(n *ast.Node, scope *saScope) bool {
	if n == nil || n.Kind != ast.KindArrayLiteralExpression {
		return false
	}
	al := n.AsArrayLiteralExpression()
	if al == nil || al.Elements == nil || len(al.Elements.Nodes) == 0 {
		return false
	}
	for _, el := range al.Elements.Nodes {
		if el == nil {
			return false
		}
		if el.Kind == ast.KindSpreadElement || el.Kind == ast.KindArrayLiteralExpression || el.Kind == ast.KindOmittedExpression {
			return false
		}
		if !saIsStrExpr(el, scope) {
			return false
		}
	}
	return true
}

func saArrayLiteralElem(w printer.EmitTextWriter, el *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if el.Kind == ast.KindOmittedExpression {
		// 空穴按 0 入槽（`[1,,3]` 中洞读 0；与 `new Array(n)` 未初始化读、
		// 缺键/缺省归零同律；`length` 照计，尾逗号无洞节点天然正确）。
		return "0", ""
	}
	if el.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, el, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		return h, ""
	}
	if saIsStrExpr(el, scope) {
		// 串元（封存 lowerExpr 串分支同形；字面量具化 temp 已归属，
		// 具名直存不碰归属——名下值仍由名释放）。
		h, msg := saEvalStr(w, el, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if saIsTempOp(h) {
			saOwnTemp(scope, h)
		}
		return h, ""
	}
	if saIsF64Operand(el, scope) {
		// 浮元（字面量文本/f64 绑定名/`-x` 经严格求值落 `fneg`；`store X
		// as i32` 与上游实发逐字同形；读位经既有 i32 门；`+` 形双边同拒）。
		// 已知缺口：小数元截断存槽（`[1.5,2.5]` 真机读回 1/2，上游同错；
		// f64 数组 8 字节布局 Phase 2 另立；曾试 choke 点全拒，误伤只读
		// length 的 315，已回退）。
		v, msg := saEvalF64Strict(w, el, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		return v, ""
	}
	if el.Kind == ast.KindIdentifier {
		if k, ok := scope.types[el.Text()]; ok && k == "jsval" {
			return el.Text(), ""
		}
	}
	if el.Kind == ast.KindObjectLiteralExpression {
		// Object-literal elements (JEV inline_materialize 1.0):
		// materialize each element with the object-literal materializer
		// (recorded interface on unique key-set match, else a synthesized
		// layout from the literal keys) and store the handle as a word,
		// exactly like string elements. The element layout is recorded
		// for the enclosing array by saLowerArrayLiteral's tail.
		h, lname, msg := saLowerObjectLiteral(w, el, "", scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		scope.types[h] = "inst:" + lname
		return h, ""
	}
	if el.Kind == ast.KindSatisfiesExpression || el.Kind == ast.KindAsExpression {
		var inner *ast.Node
		var tnode *ast.TypeNode
		if el.Kind == ast.KindSatisfiesExpression {
			se := el.AsSatisfiesExpression()
			inner, tnode = se.Expression, se.Type
		} else {
			ae := el.AsAsExpression()
			inner, tnode = ae.Expression, ae.Type
		}
		if inner != nil {
			inner = saPeelParens(inner)
		}
		if inner != nil && inner.Kind == ast.KindObjectLiteralExpression {
			want := ""
			if tnode != nil && tnode.Kind == ast.KindTypeReference {
				if ref := tnode.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && ref.TypeArguments == nil {
					if def, ok := scope.classes[ref.TypeName.Text()]; ok && def.isIface {
						want = ref.TypeName.Text()
					}
				}
			}
			h, lname, msg := saLowerObjectLiteral(w, inner, want, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			scope.types[h] = "inst:" + lname
			return h, ""
		}
	}
	v, msg := saEvalI32(w, el, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	return v, ""
}

// saSetSpreadSrc detects `new Set(array)` as a spread source, returning
// the source array node. Other Set constructions (literal seeds, etc.)
// return nil and follow the existing Set path.
func saSetSpreadSrc(e *ast.Node) *ast.Node {
	if e == nil {
		return nil
	}
	// Unwrap parens transparently.
	for e != nil && e.Kind == ast.KindParenthesizedExpression {
		pe := e.AsParenthesizedExpression()
		if pe == nil || pe.Expression == nil {
			break
		}
		e = pe.Expression.AsNode()
	}
	if e == nil || e.Kind != ast.KindNewExpression {
		return nil
	}
	ne := e.AsNewExpression()
	if ne == nil || ne.Expression == nil || ne.Expression.Kind != ast.KindIdentifier {
		return nil
	}
	if ne.Expression.Text() != "Set" {
		return nil
	}
	if ne.Arguments == nil || len(ne.Arguments.Nodes) != 1 || ne.Arguments.Nodes[0] == nil {
		return nil
	}
	return ne.Arguments.Nodes[0].AsNode()
}

// saLowerSetSpreadDedup emits a dedup loop for `[...new Set(src)]` (JEV
// dedup_loop, 0.92): iterate src, push each element into h only if h
// does not already contain it (string content equality via
// @ts_str_arr_scan, i32 equality via @ts_arr_scan). First-occurrence
// order matches JS Set. srcIsStr reports whether src elements are strings.
func saLowerSetSpreadDedup(w printer.EmitTextWriter, h, src string, srcIsStr bool, scope *saScope, nextTemp *int) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, src))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, src))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_dedup_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_dedup_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	pushL := fmt.Sprintf("L_dedup_push_%d", *scope.nextLabel)
	*scope.nextLabel++
	nextL := fmt.Sprintf("L_dedup_next_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_dedup_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	el := saArrElemAt(w, data, i, nextTemp)
	found := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if srcIsStr {
		scope.addImport("sa_std/ts_string.sa")
		np, nl := saExpandStr(w, el, nextTemp)
		w.Write(fmt.Sprintf("  %s = call @ts_str_arr_scan(%s, %s, %s, 0, 0, 0, 0)\n", found, h, np, nl))
	} else {
		scope.addImport("sa_std/ts_array.sa")
		w.Write(fmt.Sprintf("  %s = call @ts_arr_scan(%s, %s, 0, 0, 0, 0)\n", found, h, el))
	}
	// found is an i32 scan result, not a heap handle: no release here.
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", found, nextL, pushL))
	w.Write(fmt.Sprintf("%s:\n", pushL))
	saLowerArrayPush(w, h, el, scope, nextTemp)
	w.Write(fmt.Sprintf("  jmp %s\n", nextL))
	w.Write(fmt.Sprintf("%s:\n", nextL))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
}

func saLowerArrayLiteral(w printer.EmitTextWriter, n *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	al := n.AsArrayLiteralExpression()
	// spread 字面量（`[...a, 3]`）经空数组 + 逐元 push/整片合并
	// （封存 lowerArrayLiteral:8680-8708 newEmptyArray + appendSlice + lowerArrayPush 同形）。
	hasSpread := false
	if al.Elements != nil {
		for _, el := range al.Elements.Nodes {
			if el.Kind == ast.KindSpreadElement {
				hasSpread = true
				break
			}
		}
	}
	if hasSpread {
		h := saNewEmptyArray(w, nextTemp)
		isStr := false
		for _, el := range al.Elements.Nodes {
			if el.Kind == ast.KindSpreadElement {
				// `[...new Set(src)]` dedup loop (JEV dedup_loop): no Set is
				// constructed; src elements are deduplicated by content.
				if setSrc := saSetSpreadSrc(el.AsSpreadElement().Expression.AsNode()); setSrc != nil {
					src, msg := saArrValueOf(w, setSrc, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					srcIsStr := scope.arrStr != nil && scope.arrStr[src]
					if srcIsStr {
						isStr = true
					}
					saLowerSetSpreadDedup(w, h, src, srcIsStr, scope, nextTemp)
					continue
				}
				sv, msg := saArrValueOf(w, el.AsSpreadElement().Expression.AsNode(), scope, pos, refusals, nextTemp)
				if msg != "" {
					// 顶层 const 数组展开：i32 与串元快照均可物化后整片合并
					// （串元物化已在 saMaterializeTopArr 中记 arrStr；结果数组
					// 须在末尾记 arrStr，否则后续读元按 i32 走）。
					arr, ok := saTopArrLookup(scope, el.AsSpreadElement().Expression.AsNode())
					if !ok {
						return "", msg
					}
					if arr.str {
						isStr = true
					}
					sv = saMaterializeTopArr(w, arr, scope, nextTemp)
				} else if scope.arrStr != nil && scope.arrStr[sv] {
					isStr = true
				}
				saAppendSlice(w, h, sv, scope, nextTemp)
				continue
			}
			if saIsStrExpr(el, scope) {
				isStr = true
			}
			v, msg := saArrayLiteralElem(w, el, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			saLowerArrayPush(w, h, v, scope, nextTemp)
		}
		saOwnTemp(scope, h)
		if isStr {
			saMarkArrStr(scope, h)
		}
		return h, ""
	}
	var elems []string
	// nested-slice marking (elements that are themselves literals hold handles).
	nested := false
	if al.Elements != nil {
		for _, el := range al.Elements.Nodes {
			if el != nil && el.Kind == ast.KindArrayLiteralExpression {
				nested = true
				break
			}
		}
	}
	if al.Elements != nil {
		for _, el := range al.Elements.Nodes {
			v, msg := saArrayLiteralElem(w, el, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			elems = append(elems, v)
		}
	}
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	// 独占借用（SA Rust 式默认不可变）：`&mut` 是唯一写权限来源，直写不可变
	// 绑定会 ConstMutation；借用视图与源同址，写后即释（借用须先于源释放）。
	hm := saMutBorrow(w, nextTemp, h)
	buf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %d\n", buf, len(elems)*4))
	for i, v := range elems {
		p := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %d\n", p, buf, i*4))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", p, v))
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", hm, buf))
	w.Write(fmt.Sprintf("  store %s + 8, %d as u64\n", hm, len(elems)))
	w.Write(fmt.Sprintf("  !%s\n", hm))
	w.Write(fmt.Sprintf("  !%s\n", buf))
	saOwnTemp(scope, h)
	if len(elems) > 0 {
		// Uniform instance elements: record the layout so index reads
		// (xs[0].field) resolve through arrElemLayout. Mixed layouts
		// stay unmarked and refuse loudly at use sites, as before.
		elemLayout := ""
		uniform := true
		for _, ev := range elems {
			if k, ok := scope.types[ev]; ok && len(k) > 5 && k[:5] == "inst:" {
				if elemLayout == "" {
					elemLayout = k[5:]
				} else if elemLayout != k[5:] {
					uniform = false
					break
				}
			} else {
				uniform = false
				break
			}
		}
		if uniform && elemLayout != "" {
			if scope.arrElemLayout == nil {
				scope.arrElemLayout = map[string]string{}
			}
			scope.arrElemLayout[h] = elemLayout
		}
	}
	if len(elems) > 0 {
		allJsVal := true
		for _, ev := range elems {
			if k, ok := scope.types[ev]; !ok || k != "jsval" {
				allJsVal = false
				break
			}
		}
		if allJsVal {
			saMarkArrJsVal(scope, h)
		}
	}
	if nested {
		if scope.arrNest == nil {
			scope.arrNest = map[string]bool{}
		}
		scope.arrNest[h] = true
	}
	if saLiteralIsStrArray(n, scope) {
		saMarkArrStr(scope, h)
	}
	return h, ""
}

// saMutBorrow 独占借用（SA Rust 式默认不可变）：`&mut` 是唯一写权限来源。
// sci 的 `--default-immutable` 模式下 `alloc` 得到不可变绑定，直写会
// ConstMutation；须先取独占借用，写入经借用视图（与源同址），写后即释
// （借用须先于源释放，否则释放源时借用仍活跃会 BorrowConflict）。
// 返回借用视图寄存器名。
func saMutBorrow(w printer.EmitTextWriter, nextTemp *int, target string) string {
	view := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = &mut %s\n", view, target))
	return view
}

// saTplNumText 报告文本是否为模板孔安全数字形（可选 ± 号 + 1~15 位十进制
// 整数（2^53 内精确；小数/指数/十六进制/下划线沿旧门，JS 串化另有指数律）；828）。
func saTplNumText(t string) bool {
	if t == "" {
		return false
	}
	i := 0
	if t[0] == '+' || t[0] == '-' {
		i = 1
	}
	digits := 0
	for ; i < len(t); i++ {
		c := t[i]
		if c < '0' || c > '9' {
			return false
		}
		digits++
	}
	if digits == 0 || digits > 15 {
		return false
	}
	return true
}

// saTplHoleText 煮模板孔文本（十进制整/浮字面量原样、`-`/`+` 号折叠、true/false
// 即文本、串字面量煮后文本、已折串量煮后文本、已折数值量（"0"/"1" 与 bool 折叠
// 歧义故拒；十六进制等非十进制沿旧门）；方法别名/mod 槽/快照沿旧门；余形 false。
func saTplHoleText(e *ast.Node, consts map[string]string, strs map[string]bool) (string, bool) {
	if e == nil {
		return "", false
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		if saTplNumText(e.Text()) {
			return e.Text(), true
		}
		return "", false
	case ast.KindTrueKeyword:
		return "true", true
	case ast.KindFalseKeyword:
		return "false", true
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return e.Text(), true
	case ast.KindPrefixUnaryExpression:
		un := e.AsPrefixUnaryExpression()
		if un == nil || un.Operand == nil || un.Operand.Kind != ast.KindNumericLiteral {
			return "", false
		}
		if !saTplNumText(un.Operand.Text()) {
			return "", false
		}
		if un.Operator == ast.KindMinusToken {
			return "-" + un.Operand.Text(), true
		}
		if un.Operator == ast.KindPlusToken {
			return un.Operand.Text(), true
		}
		return "", false
	case ast.KindIdentifier:
		nm := e.Text()
		if t, ok := consts[nm]; ok {
			if strs[nm] {
				return t, true
			}
			if t == "0" || t == "1" {
				return "", false
			}
			if saTplNumText(t) {
				return t, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// saTopArrElemText 取顶层数组元直接量文本（整字面直通、`-`/`+` 号折叠、
// true/false 化 1/0、空穴归 0；与 saEvalI32 字面量臂 + saLowerPrefixUnary
// 正负折叠 + saArrayLiteralElem 空穴口径同律；浮/串/余形 false 交旧门）。
func saTopArrElemText(el *ast.Node) (string, bool) {
	if el == nil {
		return "", false
	}
	switch el.Kind {
	case ast.KindOmittedExpression:
		return "0", true
	case ast.KindNumericLiteral:
		if saIsFloatLit(el.Text()) {
			return "", false
		}
		return el.Text(), true
	case ast.KindTrueKeyword:
		return "1", true
	case ast.KindFalseKeyword:
		return "0", true
	case ast.KindPrefixUnaryExpression:
		un := el.AsPrefixUnaryExpression()
		if un == nil || un.Operand == nil || un.Operand.Kind != ast.KindNumericLiteral {
			return "", false
		}
		if saIsFloatLit(un.Operand.Text()) {
			return "", false
		}
		if un.Operator == ast.KindMinusToken {
			return "-" + un.Operand.Text(), true
		}
		if un.Operator == ast.KindPlusToken {
			return un.Operand.Text(), true
		}
		return "", false
	default:
		return "", false
	}
}

// saTopArrStrText 取顶层数组串元源文本（串字面量/无替换模板原样；余形 false）。
func saTopArrStrText(el *ast.Node) (string, bool) {
	if el == nil {
		return "", false
	}
	if el.Kind == ast.KindStringLiteral || el.Kind == ast.KindNoSubstitutionTemplateLiteral {
		return el.Text(), true
	}
	return "", false
}

// saTopArr 快照（i32 直接量文本或串字面量源文本；str 为真即全串元；
// nested 非空即嵌套快照（每元须同为 i32 子树，深度一致；串内层/异构不收）。
// 三者互斥；深度 = 1 + 子树深（平为 1）。
type saTopArr struct {
	elems  []string
	str    bool
	nested []saTopArr
	objs   []saTopObjElem
	// layout is the synthesized instance layout for numeric-shape object
	// elements (saSynthTopObjLayout); empty for the regex snapshot shape,
	// which is never materialized.
	layout string
}

// saTopObjElem is one object literal in a top-level const object array:
// one RegExp field (pattern) plus string-literal fields (e.g. verbosity).
// It is a transpiler-time snapshot only, never a runtime SA array of objects.
type saTopObjElem struct {
	regexField string
	regexPat   string
	regexFlags string
	strFields  map[string]string
	// numFields holds numeric-literal texts for numeric-shape elements
	// (sign folded in for unary minus/plus); empty for the regex shape.
	numFields map[string]string
	// boolFields holds boolean-literal values (JEV synth_inst_nested).
	boolFields map[string]bool
	// arrFields holds string-array literals (JEV synth_inst_nested).
	arrFields map[string][]string
	// instFields holds nested object literals and identifier-expanded const
	// object literals (JEV synth_inst_nested); each gets its own synthesized
	// layout behind a ptr slot.
	instFields map[string]*saTopObjElem
	// objArrFields holds arrays of object literals (JEV META slice:
	// options arrays); the field itself is an assembled-handle array
	// behind a ptr slot, like arrFields but with instance elements of
	// one synthesized option layout (recorded in fsub) instead of
	// string literals.
	objArrFields map[string][]saTopObjElem
}

// saTopArrDepth 快照深度（平/i32 空为 1；嵌套为子树深 + 1，同构故取 max 即确值）。
func saTopArrDepth(a saTopArr) int {
	if a.nested == nil {
		return 1
	}
	d := 0
	for _, k := range a.nested {
		if dd := saTopArrDepth(k); dd > d {
			d = dd
		}
	}
	return d + 1
}

// saTopNestKid 分类嵌套元（平 i32 叶或 i32 子树；串元/异构 false；758 臂同律）。
func saTopNestKid(el *ast.Node) (saTopArr, bool) {
	if el == nil || el.Kind != ast.KindArrayLiteralExpression {
		return saTopArr{}, false
	}
	nl := el.AsArrayLiteralExpression()
	if nl == nil {
		return saTopArr{}, false
	}
	if nl.Elements == nil {
		return saTopArr{}, true
	}
	var flat []string
	var subs []saTopArr
	for _, nel := range nl.Elements.Nodes {
		if t, ok := saTopArrElemText(nel); ok {
			flat = append(flat, t)
			continue
		}
		sub, ok := saTopNestKid(nel)
		if !ok || sub.str {
			return saTopArr{}, false
		}
		subs = append(subs, sub)
	}
	if len(flat) > 0 && len(subs) > 0 {
		return saTopArr{}, false
	}
	if len(subs) > 0 {
		d := -1
		for _, s := range subs {
			if dd := saTopArrDepth(s); d >= 0 && dd != d {
				return saTopArr{}, false
			} else {
				d = dd
			}
		}
		return saTopArr{nested: subs}, true
	}
	return saTopArr{elems: flat}, true
}

// saTopArrPrescan 收顶层 `const A = [...]` 直接量数组（名→快照；全 declarator
// 须同为 const 数组字面量（混合语句整句不收，沿旧 kind244 门，零行为变）；
// 注解须 arr 种；重名/函数类重名不收（用点沿旧门大声拒）；元须全 i32 直接量或
// 全串字面量（混元不收），空数组凭 `string[]` 注解记串元；用点按需物化本地副本
// （快照语义；变异位无臂）；与 saFoldTopLevelConst 纯量折叠同律；698/758）。
func saTopArrPrescan(stmts []*ast.Node, funcs map[string]saFuncSig, classes map[string]*saClassDef, assigned map[string]bool) (map[string]saTopArr, map[*ast.Node]bool) {
	out := map[string]saTopArr{}
	claimed := map[*ast.Node]bool{}
	// Top-level const object literals (name -> parsed element), for
	// identifier-valued nested fields in top object arrays (JEV
	// synth_inst_nested): CAP_CODEX-style consts expanded at prescan time.
	// Only non-regex object literals are collected; anything else in the
	// field position refuses loudly at the array claim below.
	objConsts := map[string]saTopObjElem{}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindVariableStatement {
			continue
		}
		vs := st.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		vdl := vs.DeclarationList.AsVariableDeclarationList()
		if vdl == nil {
			continue
		}
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil || vd.Initializer == nil {
				continue
			}
			init := saUnwrapTransparent(vd.Initializer)
			if init == nil || init.Kind != ast.KindObjectLiteralExpression {
				continue
			}
			nm := vd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			if o, ok := saTopObjElemParse(init, objConsts); ok && o.regexField == "" {
				objConsts[nm.Text()] = o
			}
		}
	}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindVariableStatement {
			continue
		}
		vs := st.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		vdl := vs.DeclarationList.AsVariableDeclarationList()
		if vdl == nil {
			continue
		}
		// const 永收；let/var 须全 declarator 未被赋值（等价 const 快照；
		// 被赋值者由 modstate 槽认领在先，此处不碰；`using` 显式释放沿旧门；818）。
		isConst := vs.DeclarationList.AsNode().Flags&ast.NodeFlagsConst != 0
		if vs.DeclarationList.AsNode().Flags&ast.NodeFlagsUsing != 0 {
			continue
		}
		type pend struct {
			name string
			arr  saTopArr
		}
		var pends []pend
		okAll := true
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil || vd.Initializer == nil {
				okAll = false
				break
			}
			// 纯类型包装透明（`as const`/satisfies/括号，非值语义；698 下标臂既有
			// 解包同律；718）。
			init := saUnwrapTransparent(vd.Initializer)
			if init == nil || init.Kind != ast.KindArrayLiteralExpression {
				okAll = false
				break
			}
			nm := vd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				okAll = false
				break
			}
			name := nm.Text()
			if _, dup := out[name]; dup {
				okAll = false
				break
			}
			if _, dup := funcs[name]; dup {
				okAll = false
				break
			}
			if _, dup := classes[name]; dup {
				okAll = false
				break
			}
			// 非 const 声明须未被赋值（等价 const 快照；818）。
			if !isConst && assigned[name] {
				okAll = false
				break
			}
			strAnnot := false
			if vd.Type != nil {
				if k, ok := saAnnotKind(vd.Type); !ok || k != "arr" {
					okAll = false
					break
				}
				strAnnot = saIsStringArrayAnnot(vd.Type)
			}
			al := init.AsArrayLiteralExpression()
			if al == nil {
				okAll = false
				break
			}
			var i32s, strs []string
			var subs []saTopArr
			var objs []saTopObjElem
			eleOk := true
			if al.Elements != nil {
				for _, el := range al.Elements.Nodes {
					if t, elok := saTopArrElemText(el); elok {
						i32s = append(i32s, t)
						continue
					}
					if t, elok := saTopArrStrText(el); elok {
						strs = append(strs, t)
						continue
					}
					// 嵌套子树（i32 同构；串内层/异构/深浅不一整句不收；808）。
					// Object element (regex field + string-literal fields; same field set; .find inline at use site, not materialised as a runtime object array).
					if obj, objok := saTopObjElemParse(el, objConsts); objok {
						objs = append(objs, obj)
						continue
					}
					if sub, subok := saTopNestKid(el); subok && !sub.str {
						subs = append(subs, sub)
						continue
					}
					eleOk = false
					break
				}
			}
			nKinds := 0
			if len(i32s) > 0 {
				nKinds++
			}
			if len(strs) > 0 {
				nKinds++
			}
			if len(subs) > 0 {
				nKinds++
			}
			if len(objs) > 0 {
				nKinds++
			}
			if !eleOk || nKinds > 1 {
				okAll = false
				break
			}
			// 嵌套子树深度一致（808）。
			if len(subs) > 0 {
				d := -1
				for _, s := range subs {
					if dd := saTopArrDepth(s); d >= 0 && dd != d {
						eleOk = false
						break
					} else {
						d = dd
					}
				}
				if !eleOk {
					okAll = false
					break
				}
			}
			arr := saTopArr{elems: i32s}
			if len(strs) > 0 || (len(i32s) == 0 && len(subs) == 0 && len(objs) == 0 && strAnnot) {
				arr = saTopArr{elems: strs, str: true}
			}
			if len(subs) > 0 {
				arr = saTopArr{nested: subs}
			}
			if len(objs) > 0 {
				first := objs[0]
				if first.regexField != "" {
					for _, o := range objs[1:] {
						if o.regexField != first.regexField || len(o.strFields) != len(first.strFields) || len(o.numFields) != 0 {
							eleOk = false
							break
						}
						for k := range first.strFields {
							if _, ok := o.strFields[k]; !ok {
								eleOk = false
								break
							}
						}
					}
					if !eleOk {
						okAll = false
						break
					}
					arr = saTopArr{objs: objs}
				} else {
					// Numeric shape: union field set across elements; a field
					// missing from any element is optional (fopt discipline).
					// A field must keep one kind across elements, and numeric
					// fields take f64 slots when any value is fractional.
					kindOf := map[string]string{}
					present := map[string]int{}
					instSub := map[string]string{}
					// arrSeen/objArrSeen keep string-array and
					// object-array shapes apart: both synthesize to
					// kind "arr", so kindOf alone cannot tell a field
					// that mixes them across elements (loud refuse).
					arrSeen := map[string]bool{}
					objArrSeen := map[string]bool{}
					objArrElems := map[string][]saTopObjElem{}
					for _, o := range objs {
						if o.regexField != "" {
							eleOk = false
							break
						}
						for k := range o.strFields {
							if prev, dup := kindOf[k]; dup && prev != "str" {
								eleOk = false
								break
							}
							kindOf[k] = "str"
							present[k]++
						}
						if !eleOk {
							break
						}
						for k, v := range o.numFields {
							if prev, dup := kindOf[k]; dup && prev != "i32" && prev != "f64" {
								eleOk = false
								break
							}
							want := "i32"
							if saIsFloatLit(v) {
								want = "f64"
							}
							if kindOf[k] == "f64" || want == "f64" {
								kindOf[k] = "f64"
							} else {
								kindOf[k] = "i32"
							}
							present[k]++
						}
						if !eleOk {
							break
						}
						for k := range o.boolFields {
							// Bool fields are stored as i32 slots (see kindOf[k] = "i32" below and
							// saTopObjElemKinds bool -> "i32"); the consistency check must compare
							// against the stored kind "i32", not "bool", or the second element
							// of any bool-bearing top object array fails the prescan (kind-244).
							if prev, dup := kindOf[k]; dup && prev != "i32" {
								eleOk = false
								break
							}
							kindOf[k] = "i32"
							present[k]++
						}
						if !eleOk {
							break
						}
						for k := range o.arrFields {
							if objArrSeen[k] {
								eleOk = false
								break
							}
							if prev, dup := kindOf[k]; dup && prev != "arr" {
								eleOk = false
								break
							}
							kindOf[k] = "arr"
							arrSeen[k] = true
							present[k]++
						}
						if !eleOk {
							break
						}
						for k, subElems := range o.objArrFields {
							if arrSeen[k] {
								eleOk = false
								break
							}
							if prev, dup := kindOf[k]; dup && prev != "arr" {
								eleOk = false
								break
							}
							kindOf[k] = "arr"
							objArrSeen[k] = true
							objArrElems[k] = append(objArrElems[k], subElems...)
							present[k]++
						}
						if !eleOk {
							break
						}
						for k, sub := range o.instFields {
							if prev, dup := kindOf[k]; dup && prev != "inst" {
								eleOk = false
								break
							}
							if sub == nil {
								eleOk = false
								break
							}
							lname, lok := saTopObjLayoutOfElem(*sub, classes)
							if !lok {
								eleOk = false
								break
							}
							if prev, dup := instSub[k]; dup && prev != lname {
								eleOk = false
								break
							}
							instSub[k] = lname
							kindOf[k] = "inst"
							present[k]++
						}
						if !eleOk {
							break
						}
					}
					for k, subElems := range objArrElems {
						lname, lok := saTopObjArrLayout(subElems, classes)
						if !lok {
							eleOk = false
							break
						}
						if lname != "" {
							instSub[k] = lname
						}
					}
					if !eleOk {
						okAll = false
						break
					}
					var fields []string
					for k := range kindOf {
						fields = append(fields, k)
					}
					sort.Strings(fields)
					fopt := map[string]bool{}
					for _, f := range fields {
						if present[f] != len(objs) {
							fopt[f] = true
						}
					}
					arr = saTopArr{objs: objs, layout: saSynthTopObjLayout(fields, kindOf, fopt, instSub, classes)}
				}
			}
			pends = append(pends, pend{name: name, arr: arr})
		}
		if !okAll {
			continue
		}
		for _, p := range pends {
			out[p.name] = p.arr
		}
		claimed[st] = true
	}
	return out, claimed
}

// saMaterializeTopArr 按快照物化顶层 const 数组为本地新柄（i32 元：alloc 16 头
// + 4n 缓冲逐元直存 + 头/长回填，与 saLowerArrayLiteral 非 spread 臂同形；串元：
// 16 字节头逐元具化 + 4 字节槽存柄 + arrStr 标记，与本地串字面量数组同形；嵌套：
// 内层逐个具化 + 外层 4 字节槽存内层柄 + arrNest 标记；新柄归属登记，调用方读后
// 即释；698/758/798）。
// saMaterializeTopObjElem materializes one materialized-shape top object
// element as an owned instance handle of def's layout (JEV
// synth_inst_nested): string fields via string literals, numeric fields by
// kind, boolean fields as 0/1 i32, string-array fields as a materialized
// string array handle, nested instance fields as recursively materialized
// instance handles, all stored as ptr slots; missing optional fields are
// zero-filled (0 handle for ptr slots, 0 for i32 slots).
func saMaterializeTopObjElem(w printer.EmitTextWriter, o saTopObjElem, def *saClassDef, scope *saScope, nextTemp *int) string {
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if def.size == 0 {
		w.Write(fmt.Sprintf("  %s = alloc 4\n", h))
	} else {
		w.Write(fmt.Sprintf("  %s = alloc %d\n", h, def.size))
	}
	saOwnTemp(scope, h)
	for _, f := range def.fields {
		if v, ok := o.strFields[f.name]; ok {
			sh := saLowerStringLiteral(w, v, scope, nextTemp)
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[f.name], sh))
			saReleaseOwnedTemp(w, scope, sh)
			continue
		}
		if v, ok := o.numFields[f.name]; ok {
			if def.fkinds[f.name] == "f64" {
				w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, def.offsets[f.name], v))
			} else {
				w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, def.offsets[f.name], v))
			}
			continue
		}
		if v, ok := o.boolFields[f.name]; ok {
			bv := "0"
			if v {
				bv = "1"
			}
			w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, def.offsets[f.name], bv))
			continue
		}
		if vals, ok := o.arrFields[f.name]; ok {
			var hs []string
			for _, sv := range vals {
				hs = append(hs, saLowerStringLiteral(w, sv, scope, nextTemp))
			}
			ah := saAssembleHandles(w, hs, scope, nextTemp)
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[f.name], ah))
			saReleaseOwnedTemp(w, scope, ah)
			continue
		}
		if elems, ok := o.objArrFields[f.name]; ok {
			if subName, ok2 := def.fsub[f.name]; ok2 {
				if subDef, ok3 := scope.classes[subName]; ok3 && subDef != nil {
					var hs []string
					for _, eo := range elems {
						hs = append(hs, saMaterializeTopObjElem(w, eo, subDef, scope, nextTemp))
					}
					ah := saAssembleHandles(w, hs, scope, nextTemp)
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[f.name], ah))
					saReleaseOwnedTemp(w, scope, ah)
					continue
				}
			}
			// No option layout (every element's array was empty):
			// materialize an empty handle array.
			ah := saAssembleHandles(w, nil, scope, nextTemp)
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[f.name], ah))
			saReleaseOwnedTemp(w, scope, ah)
			continue
		}
		if sub, ok := o.instFields[f.name]; ok && sub != nil {
			if subName, ok2 := def.fsub[f.name]; ok2 {
				if subDef, ok3 := scope.classes[subName]; ok3 && subDef != nil {
					nh := saMaterializeTopObjElem(w, *sub, subDef, scope, nextTemp)
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[f.name], nh))
					saReleaseOwnedTemp(w, scope, nh)
					continue
				}
			}
			w.Write(fmt.Sprintf("  store %s + %d, 0 as ptr\n", h, def.offsets[f.name]))
			continue
		}
		if def.fkinds[f.name] == "str" || def.fkinds[f.name] == "f64" || def.fkinds[f.name] == "arr" || def.fkinds[f.name] == "inst" {
			w.Write(fmt.Sprintf("  store %s + %d, 0 as ptr\n", h, def.offsets[f.name]))
		} else {
			w.Write(fmt.Sprintf("  store %s + %d, 0 as i32\n", h, def.offsets[f.name]))
		}
	}
	return h
}

func saMaterializeTopArr(w printer.EmitTextWriter, arr saTopArr, scope *saScope, nextTemp *int) string {
	if arr.objs != nil && arr.layout != "" {
		def, ok := scope.classes[arr.layout]
		if !ok || def == nil {
			return saAssembleHandles(w, nil, scope, nextTemp)
		}
		var hs []string
		for _, o := range arr.objs {
			hs = append(hs, saMaterializeTopObjElem(w, o, def, scope, nextTemp))
		}
		return saAssembleHandles(w, hs, scope, nextTemp)
	}
	if arr.str {
		var hs []string
		for _, t := range arr.elems {
			hs = append(hs, saLowerStringLiteral(w, t, scope, nextTemp))
		}
		h := saAssembleHandles(w, hs, scope, nextTemp)
		saMarkArrStr(scope, h)
		return h
	}
	if arr.nested != nil {
		var hs []string
		for _, sub := range arr.nested {
			hs = append(hs, saBuildNestArray(w, sub, scope, nextTemp))
		}
		h := saAssembleHandles(w, hs, scope, nextTemp)
		if scope.arrNest == nil {
			scope.arrNest = map[string]bool{}
		}
		scope.arrNest[h] = true
		return h
	}
	return saBuildI32Array(w, arr.elems, scope, nextTemp)
}

func saBuildNestArray(w printer.EmitTextWriter, sub saTopArr, scope *saScope, nextTemp *int) string {
	if sub.nested == nil {
		return saBuildI32Array(w, sub.elems, scope, nextTemp)
	}
	var hs []string
	for _, k := range sub.nested {
		hs = append(hs, saBuildNestArray(w, k, scope, nextTemp))
	}
	h := saAssembleHandles(w, hs, scope, nextTemp)
	if scope.arrNest == nil {
		scope.arrNest = map[string]bool{}
	}
	scope.arrNest[h] = true
	return h
}

// saAssembleHandles 组装句柄数组头（alloc 16 头 + 4n 缓冲存柄 + 头/长回填；
// 归属登记；串/嵌套物化共用；808）。
func saAssembleHandles(w printer.EmitTextWriter, hs []string, scope *saScope, nextTemp *int) string {
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	// 独占借用（SA Rust 式默认不可变）：`&mut` 是唯一写权限来源，直写不可变
	// 绑定会 ConstMutation；借用视图与源同址，写后即释（借用须先于源释放）。
	hm := saMutBorrow(w, nextTemp, h)
	buf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %d\n", buf, len(hs)*4))
	for i, v := range hs {
		p := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %d\n", p, buf, i*4))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", p, v))
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", hm, buf))
	w.Write(fmt.Sprintf("  store %s + 8, %d as u64\n", hm, len(hs)))
	w.Write(fmt.Sprintf("  !%s\n", hm))
	w.Write(fmt.Sprintf("  !%s\n", buf))
	saOwnTemp(scope, h)
	return h
}

// saBuildI32Array 按直接量文本建平数组柄（alloc 16 头 + 4n 缓冲逐元直存 +
// 头/长回填；归属登记；698）。
func saBuildI32Array(w printer.EmitTextWriter, elems []string, scope *saScope, nextTemp *int) string {
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	buf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  %s = alloc %d\n", buf, len(elems)*4))
	for i, v := range elems {
		p := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %d\n", p, buf, i*4))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", p, v))
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", h, buf))
	w.Write(fmt.Sprintf("  store %s + 8, %d as u64\n", h, len(elems)))
	w.Write(fmt.Sprintf("  !%s\n", buf))
	saOwnTemp(scope, h)
	return h
}

// saTopArrLookup 查顶层 const 数组快照（标识符直指、无局部/mod 槽遮蔽；不落字）。
func saTopArrLookup(scope *saScope, e *ast.Node) (saTopArr, bool) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return saTopArr{}, false
	}
	name := e.Text()
	if _, shadowed := scope.types[name]; shadowed {
		return saTopArr{}, false
	}
	if _, isMod := scope.modVars[name]; isMod {
		return saTopArr{}, false
	}
	arr, ok := scope.topArrs[name]
	if !ok {
		return saTopArr{}, false
	}
	return arr, true
}

// saLowerCheckedIndex lowering 越界归零下标读（形状证据：封存
// lowerCheckedIndex:8522-8567：alloc 8 join 槽 + len/ult 检查 + data/mul/add
// 取址 + i32 读回；OOB 得 0；槽 ownTemp + 读后 releaseIfOwnedTemp 同形）。
// saLowerCheckedIndex lowering 越界归零下标读（形状证据：封存
// lowerCheckedIndex:8522-8567：alloc 8 join 槽 + len/ult 检查 + data/mul/add
// 取址 + i32 读回；OOB 得 0；槽 ownTemp + 读后 releaseIfOwnedTemp 同形）。
// saTopArrPureMethod 报告顶层 const 数组快照接收器安全方法（slice/indexOf/
// includes/join/concat 纯读 + map/filter/find/findIndex/some/every/reduce/
// reduceRight/forEach 内联回调高阶；快照物化后走既有方法径；回调副作用为用户
// 语义与本地同律，具名回调沿既有内联门大声拒；变异方法与未知方法不在此列；708/738）。
func saTopArrPureMethod(m string) bool {
	switch m {
	case "slice", "indexOf", "includes", "join", "concat",
		"map", "filter", "find", "findIndex", "some", "every",
		"reduce", "reduceRight", "forEach",
		"lastIndexOf", "at", "toReversed", "toSorted", "with", "toSpliced",
		"flat", "flatMap", "findLast", "findLastIndex":
		return true
	}
	return false
}

// saTopArrPureCall 纯查表判定是否为顶层 const 数组快照方法调用（不落字；
// 门控位调用，局部/mod 槽遮蔽优先；`?.` 调用沿旧门；串快照仅 indexOf/includes
// （余下串方法本地同形破碎或未探，沿旧门）；708/758）。
func saTopArrPureCall(pa *ast.PropertyAccessExpression, scope *saScope, m string) bool {
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.QuestionDotToken != nil {
		return false
	}
	arr, ok := saTopArrLookup(scope, pa.Expression)
	if !ok {
		return false
	}
	// Str snapshots also allow some/every (JEV inline_loop_indexed_read
	// 0.98): the materialized handle is arrStr-marked, so the callback
	// parameter binds as str and indexed interface reads in the predicate
	// lower exactly as in for-of loops. Other str methods stay gated.
	if arr.str && m != "indexOf" && m != "includes" && m != "some" && m != "every" {
		return false
	}
	return saTopArrPureMethod(m)
}

// saTopArrPureRecv 物化顶层 const 数组快照方法接收器（纯读性由调用点门控，
// 此处复判防未来新调用方；变异/未知方法返回 false 沿旧门；708）。
func saTopArrPureRecv(w printer.EmitTextWriter, pa *ast.PropertyAccessExpression, scope *saScope, nextTemp *int) (string, bool) {
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
		return "", false
	}
	arr, ok := saTopArrLookup(scope, pa.Expression)
	if !ok {
		return "", false
	}
	if pa.Name() == nil || !saTopArrPureMethod(pa.Name().Text()) {
		return "", false
	}
	if arr.str && pa.Name().Text() != "indexOf" && pa.Name().Text() != "includes" && pa.Name().Text() != "some" && pa.Name().Text() != "every" {
		return "", false
	}
	return saMaterializeTopArr(w, arr, scope, nextTemp), true
}

// saTopArrSnapshot 物化顶层 const 数组快照（用点：for-of/for-in 巡回、spread
// 字面量展开；i32 与串元皆可（串巡回绑定另臂，spread 串形调用方另判）；调用实参/
// 返回/别名/存储位禁用（身份语义），沿旧门；728）。
func saTopArrSnapshot(w printer.EmitTextWriter, e *ast.Node, scope *saScope, nextTemp *int) (string, bool) {
	arr, ok := saTopArrLookup(scope, e)
	if !ok {
		return "", false
	}
	return saMaterializeTopArr(w, arr, scope, nextTemp), true
}

// saLowerOptionalIndex lowers `a?.[i]` (null base reads 0, otherwise the checked-index
// join; null is the zero handle).
func saLowerOptionalIndex(w printer.EmitTextWriter, base, idx string, nextLabel, nextTemp *int) string {
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	nullL := fmt.Sprintf("L_idx_null_%d", *nextLabel)
	*nextLabel++
	chkL := fmt.Sprintf("L_idx_chk_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_idx_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	isnull := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isnull, base))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isnull, nullL, chkL))
	w.Write(fmt.Sprintf("%s:\n", nullL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", chkL))
	v := saLowerCheckedIndex(w, base, idx, nextLabel, nextTemp)
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest
}

// saStructArrElemLayout 解析 struct 数组基布局名（`b.items` 经实例字段
// fdefs 元查布局表；裸标识符 struct 数组另步，返 false）。
func saStructArrElemLayout(base *ast.Node, scope *saScope) (string, bool) {
	if base == nil || base.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	pa := base.AsPropertyAccessExpression()
	if pa == nil || pa.QuestionDotToken != nil || pa.Name() == nil ||
		pa.Name().Kind != ast.KindIdentifier {
		return "", false
	}
	var def *saClassDef
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		k, ok := scope.types[pa.Expression.Text()]
		if !ok || len(k) <= 5 || k[:5] != "inst:" {
			return "", false
		}
		def, ok = scope.classes[k[5:]]
		if !ok || def == nil {
			return "", false
		}
	} else if pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword {
		var ok bool
		def, ok = scope.classes[scope.thisClass]
		if !ok || def == nil {
			return "", false
		}
	} else {
		return "", false
	}
	fname := pa.Name().Text()
	if def.fkinds[fname] != "arr" {
		return "", false
	}
	return saJSONArrInstSub(def, fname, scope)
}

// saLowerStructArrElem 越界归零 struct 元读（8 字节柄槽；OOB 得 0 柄；
// 与 saLowerCheckedIndex 同骨架，值宽按 ptr，元步进 8；布局名由调用方
// 持证绑定，发射侧只搬句柄）。
func saLowerStructArrElem(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	base, msg := saArrValueOf(w, ea.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", "index base must be bound array"
	}
	idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	if msg := saCheckIntIndex(scope, idx); msg != "" {
		return "", msg
	}
	freshT := func() string {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		return t
	}
	freshL := func(p string) string {
		l := fmt.Sprintf("L_%s_%d", p, *scope.nextLabel)
		*scope.nextLabel++
		return l
	}
	slot := freshT()
	endL := freshL("sidx_end")
	oobL := freshL("sidx_oob")
	loadL := freshL("sidx_ok")
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	ln := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, base))
	ok := freshT()
	w.Write(fmt.Sprintf("  %s = ult %s, %s\n", ok, idx, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", ok, loadL, oobL))
	w.Write(fmt.Sprintf("%s:\n", loadL))
	data := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, base))
	off := freshT()
	w.Write(fmt.Sprintf("  %s = mul %s, 8\n", off, idx))
	addr := freshT()
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	v := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", v, addr))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", oobL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dest, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest, ""
}

func saLowerCheckedIndex(w printer.EmitTextWriter, base, idx string, nextLabel, nextTemp *int) string {
	freshT := func() string {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		return t
	}
	freshL := func(p string) string {
		l := fmt.Sprintf("L_%s_%d", p, *nextLabel)
		*nextLabel++
		return l
	}
	slot := freshT()
	endL := freshL("idx_end")
	oobL := freshL("idx_oob")
	loadL := freshL("idx_ok")
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	ln := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, base))
	ok := freshT()
	w.Write(fmt.Sprintf("  %s = ult %s, %s\n", ok, idx, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", ok, loadL, oobL))
	w.Write(fmt.Sprintf("%s:\n", loadL))
	data := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, base))
	off := freshT()
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, idx))
	addr := freshT()
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	v := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", v, addr))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", oobL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	// join 槽读后即死，就地释放（ownTemp+releaseIfOwnedTemp 同效；槽为本函数
	// 内新鲜临时量，无外部分支能消费/释放，故无条件释放 sound）。
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest
}

// saLowerCheckedIndexDefault lowering 带缺省的守卫下标读（绑定数组解构缺省
// 用；守卫/装载/汇合与 saLowerCheckedIndex 同形，OOB 臂改求缺省值存槽。
// 惰性：界内缺省恒不求值，副作用缺省只在越界触发（`??`/三元惰性同律）。
// 上游同形 OOB 恒存 0（p8b 实证 bun 5 vs 上游 0，静默错值），薄口按 JS
// 求值缺省式（字面量越界臂同律），正确优先（铁律 4）；缺省求值失败沿
// saEvalI32 原拒因上抛（调用方定位）。
func saLowerCheckedIndexDefault(w printer.EmitTextWriter, base, idx string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int, init *ast.Node, isStr bool) (string, string) {
	freshT := func() string {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		return t
	}
	freshL := func(p string) string {
		l := fmt.Sprintf("L_%s_%d", p, *nextLabel)
		*nextLabel++
		return l
	}
	slot := freshT()
	endL := freshL("idx_end")
	oobL := freshL("idx_oob")
	loadL := freshL("idx_ok")
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	ln := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, base))
	ok := freshT()
	w.Write(fmt.Sprintf("  %s = ult %s, %s\n", ok, idx, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", ok, loadL, oobL))
	w.Write(fmt.Sprintf("%s:\n", loadL))
	data := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, base))
	off := freshT()
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, idx))
	addr := freshT()
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	v := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", v, addr))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", oobL))
	var op, msg string
	if isStr {
		op, msg = saEvalStr(w, init, scope, pos, refusals, nextTemp)
	} else {
		op, msg = saEvalI32(w, init, scope, pos, refusals, nextTemp)
	}
	if msg != "" {
		return "", msg
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, op))
	saReleaseOwnedTemp(w, scope, op)
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest, ""
}

// saLowerCheckedIndexDefaultArr lowering 数组缺省的守卫下标读（嵌套数组解构
// 缺省用；守卫/装载/汇合与 saLowerCheckedIndexDefault 同形，OOB 臂现场构造数组
// 字面量缺省（saLowerArrayLiteral 自带归属登记与 arrNest/arrStr 标记，槽存后即释，
// 与标量臂同律）；界内读元柄直传（嵌套元即内层柄，4 字节槽同宽）。
func saLowerCheckedIndexDefaultArr(w printer.EmitTextWriter, base, idx string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int, init *ast.Node) (string, string) {
	freshT := func() string {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		return t
	}
	freshL := func(p string) string {
		l := fmt.Sprintf("L_%s_%d", p, *nextLabel)
		*nextLabel++
		return l
	}
	slot := freshT()
	endL := freshL("idx_end")
	oobL := freshL("idx_oob")
	loadL := freshL("idx_ok")
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	ln := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, base))
	ok := freshT()
	w.Write(fmt.Sprintf("  %s = ult %s, %s\n", ok, idx, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", ok, loadL, oobL))
	w.Write(fmt.Sprintf("%s:\n", loadL))
	data := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, base))
	off := freshT()
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, idx))
	addr := freshT()
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	v := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", v, addr))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", oobL))
	mark := len(scope.ownOrder)
	op, msg := saLowerArrayLiteral(w, init, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, op))
	// 臂内新生归属逆序释放（分支内定义跨 join 沿用即 UnknownRegister，h1 实证；
	// 释后记 released，域尾 sweep 跳过禁双释；与 p0 域尾序同律）。
	for i := len(scope.ownOrder) - 1; i >= mark; i-- {
		saReleaseOwnedTemp(w, scope, scope.ownOrder[i])
	}
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest, ""
}

// saLowerElementStore lowering `a[i] = v`（形状证据：封存 lowerElementStore:8437-8448）。
// saLowerElementStore lowering `a[i] = v`（形状证据：封存 lowerElementStore:8437-8448）。
func saLowerElementStore(w printer.EmitTextWriter, base, idx, rhs string, nextTemp *int) {
	baseT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	offT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	ptrT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", baseT, base))
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", offT, idx))
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", ptrT, baseT, offT))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", ptrT, rhs))
}

// saCheckIntIndex 守下标整数种（串/实例句柄禁作槽位下标；元素求值只建种不验种；
// 下标读/存/链三处共用；铁律 4 高于同形）。
func saCheckIntIndex(scope *saScope, idx string) string {
	if k, ok := scope.types[idx]; ok && (k == "str" || (len(k) > 5 && k[:5] == "inst:")) {
		return "array index must be an integer"
	}
	return ""
}

// saKeyofBase 解析 keyof 基布局（标识符 inst 绑定/this；透明包装见
// saKeyofUnwrap；余形 false）。
func saKeyofBase(base *ast.Node, scope *saScope) (string, *saClassDef, bool) {
	if base == nil {
		return "", nil, false
	}
	if base = saKeyofUnwrap(base, scope); base == nil {
		return "", nil, false
	}
	if base.Kind == ast.KindIdentifier {
		if k, ok := scope.types[base.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
			if def, ok := scope.classes[k[5:]]; ok && def != nil {
				return k[5:], def, true
			}
		}
		return "", nil, false
	}
	if base.Kind == ast.KindThisKeyword && scope.thisSelf != "" {
		if def, ok := scope.classes[scope.thisClass]; ok && def != nil {
			return scope.thisClass, def, true
		}
	}
	return "", nil, false
}

// saKeyofUnwrap 剥离 keyof 基透明包装（括号/非空/`<>`恒透明；as/satisfies
// 仅 any/unknown/never 或同名布局透传，跨布局断言返 nil 调用方沿旧门大声拒，
// 禁静默错布局；铁律 4）。
func saKeyofUnwrap(base *ast.Node, scope *saScope) *ast.Node {
	for base != nil {
		switch base.Kind {
		case ast.KindParenthesizedExpression:
			base = base.AsParenthesizedExpression().Expression
		case ast.KindNonNullExpression:
			base = base.AsNonNullExpression().Expression
		case ast.KindTypeAssertionExpression:
			base = base.AsTypeAssertion().Expression
		case ast.KindAsExpression, ast.KindSatisfiesExpression:
			var inner *ast.Node
			var tnode *ast.TypeNode
			if base.Kind == ast.KindAsExpression {
				ax := base.AsAsExpression()
				inner, tnode = ax.Expression, ax.Type
			} else {
				sx := base.AsSatisfiesExpression()
				inner, tnode = sx.Expression, sx.Type
			}
			if tnode != nil && (tnode.Kind == ast.KindAnyKeyword || tnode.Kind == ast.KindUnknownKeyword || tnode.Kind == ast.KindNeverKeyword) {
				base = inner
				continue
			}
			// 同名布局透传（inner 布局 L 且目标为同名/别名链/泛型具化同布局；
			// 别名/泛型目标另见 saTypeNamesLayout）。
			layout, _, ok := saKeyofBase(inner, scope)
			if !ok || !saTypeNamesLayout(tnode, layout, scope) {
				return nil
			}
			base = inner
		default:
			return base
		}
	}
	return base
}

// saTypeNamesLayout 断言目标是否为布局直名（`as O`；别名链跟随至接口名
// 再比（`type O2 = O`）；泛型具化经 saInstantiateIface 求布局名再比
// （`as Box<number>`）；余形沿旧门）。
func saTypeNamesLayout(tnode *ast.TypeNode, layout string, scope *saScope) bool {
	if tnode == nil || tnode.Kind != ast.KindTypeReference {
		return false
	}
	ref := tnode.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return false
	}
	name := ref.TypeName.Text()
	if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
		if lname, ok := saInstantiateIface(name, ref.TypeArguments.Nodes, scope.classes); ok {
			return lname == layout
		}
		return false
	}
	if name == layout {
		return true
	}
	// 别名链跟随（环/断链止步；终点须为布局名本身）。
	seen := map[string]bool{}
	cur := name
	for i := 0; i < len(scope.aliasOf)+1; i++ {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		tgt, ok := scope.aliasOf[cur]
		if !ok || tgt == nil || tgt.Kind != ast.KindTypeReference {
			return false
		}
		nr := tgt.AsTypeReferenceNode()
		if nr == nil || nr.TypeName == nil || nr.TypeName.Kind != ast.KindIdentifier {
			return false
		}
		if nr.TypeArguments != nil && len(nr.TypeArguments.Nodes) > 0 {
			return false
		}
		cur = nr.TypeName.Text()
		if cur == layout {
			return true
		}
	}
	return false
}

// saLowerKeyofIndex lowering `p[k]` 动态键读（布局须全 i32；字面量键静态
// 折叠直读；动态键 strcmp 链命中直读、未知键归零（缺键同律）；`?.` 另步；
// 非 i32 布局大声拒；上游句柄当下标错译，本仓 correct）。
func saLowerKeyofIndex(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, layout string, def *saClassDef, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if ea.QuestionDotToken != nil {
		return "", "optional keyof index reads are not lowerable yet"
	}
	key := ea.ArgumentExpression
	if key != nil && (key.Kind == ast.KindStringLiteral || key.Kind == ast.KindNoSubstitutionTemplateLiteral) {
		// 字面量键静态折叠（缺键归零；非 i32 字段 loud）。
		s, ok := saKeyofLitText(key)
		if !ok {
			return "", "keyof literal key is not lowerable"
		}
		off, ok := def.offsets[s]
		if !ok {
			return "0", ""
		}
		if def.fkinds[s] != "i32" {
			return "", "keyof field is not i32"
		}
		base, msg := saArrValueOf(w, ea.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			// 实例基经句柄直读（saArrValueOf 只识数组；实例柄即基；as/括号
			// 等透明包装经 saKeyofUnwrap 剥离，跨布局断言返 nil 沿旧门拒）。
			if h, _, msg2 := saInstBase(saKeyofUnwrap(ea.Expression, scope), scope); msg2 == "" && h != "" {
				base = h
			} else {
				return "", "keyof base must be a bound instance"
			}
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", t, base, off))
		return t, ""
	}
	for _, f := range def.fields {
		if def.fkinds[f.name] != "i32" {
			return "", "keyof index needs all-i32 layout"
		}
	}
	kh, msg := saEvalStr(w, key, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	base, msg := saArrValueOf(w, ea.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		if h, _, msg2 := saInstBase(saKeyofUnwrap(ea.Expression, scope), scope); msg2 == "" && h != "" {
			base = h
		} else {
			return "", "keyof base must be a bound instance"
		}
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	endL := fmt.Sprintf("L_ki_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	for _, f := range def.fields {
		fh := saLowerStringLiteral(w, f.name, scope, nextTemp)
		eq := saStringContentEq(w, kh, fh, false, scope, nextTemp)
		// 字段头分支内定义，用后（比较后、跳转前）即释；尾 drain 看不见分支域，
		// 释后置于终结符后即断块（FallthroughForbidden），故先释后跳。
		saReleaseOwnedTemp(w, scope, fh)
		hitL := fmt.Sprintf("L_ki_hit_%d", *scope.nextLabel)
		*scope.nextLabel++
		nextL := fmt.Sprintf("L_ki_next_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", eq, hitL, nextL))
		w.Write(fmt.Sprintf("%s:\n", hitL))
		v := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", v, base, def.offsets[f.name]))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", slot, v))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", nextL))
	}
	w.Write(fmt.Sprintf("  store %s + 0, 0 as i32\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return out, ""
}

// saLowerOptKeyofNullish 融合 `base?.[k] ?? D`（i32 布局专用）：空基与缺键
// 合流 miss 臂（D 单点惰性求值一次；命中即值，与上游 `undefined ?? D` 逐情形
// 一致：空基→D、命中→值、缺键→D）；基址须解包至标识符/this（调用基址另见
// saLowerOptKeyofCallBase，字面量沿旧门）（非 i32 布局/`?.` 缺配对沿旧门大声拒）。
func saLowerOptKeyofNullish(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, dflt *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	_, def, ok := saKeyofBase(ea.Expression, scope)
	if !ok {
		return "", "optional keyof index reads are not lowerable yet"
	}
	for _, f := range def.fields {
		if def.fkinds[f.name] != "i32" {
			return "", "keyof index needs all-i32 layout"
		}
	}
	ub := saKeyofUnwrap(ea.Expression, scope)
	var base string
	if ub != nil && ub.Kind == ast.KindIdentifier {
		base = ub.Text()
	} else if ub != nil && ub.Kind == ast.KindThisKeyword {
		if scope.thisSelf == "" {
			return "", "this outside a class method is not lowerable"
		}
		base = scope.thisSelf
	} else {
		return "", "optional keyof index reads are not lowerable yet"
	}
	return saLowerOptKeyofEmit(w, ea, dflt, base, def, scope, pos, refusals, nextTemp)
}

// saLowerOptKeyofCallBase 融合调用基址 `getObj()?.[k] ?? D`（for-in 调用臂
// 同形：返回布局已知 + 全 i32 才求值入柄；调用恰求值一次，巡后释放；成员
// 调用/void/非 inst 返回沿旧门）。
func saLowerOptKeyofCallBase(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, dflt *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, bool) {
	ce := saForInCallBase(ea.Expression)
	if ce == nil {
		return "", "", false
	}
	rk, ok := saCallRetKind(ce, scope)
	if !ok || len(rk) <= 5 || rk[:5] != "inst:" {
		return "", "", false
	}
	def, ok := scope.classes[rk[5:]]
	if !ok || def == nil {
		return "", "", false
	}
	for _, f := range def.fields {
		if def.fkinds[f.name] != "i32" {
			return "", "keyof index needs all-i32 layout", true
		}
	}
	op, voidCall, msg := saEvalCall(w, ce, scope, pos, refusals, nextTemp)
	if msg != "" || voidCall {
		return "", "", false
	}
	fh := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", fh, op))
	scope.types[fh] = rk
	if saIsTempOp(op) {
		saConsumeOwn(scope, op)
	}
	fhDepth := len(scope.ownOrder)
	saDeclareOwned(scope, fh)
	out, msg := saLowerOptKeyofEmit(w, ea, dflt, fh, def, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg, true
	}
	saReleaseDeeperThan(w, scope, fhDepth)
	return out, "", true
}

// saLowerOptKeyofEmit 融合发射核（基柄 + 布局既定）：空基与缺键合流 miss 臂。
func saLowerOptKeyofEmit(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, dflt *ast.Node, base string, def *saClassDef, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	isnull := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	missL := fmt.Sprintf("L_koq_miss_%d", *scope.nextLabel)
	*scope.nextLabel++
	chkL := fmt.Sprintf("L_koq_chk_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_koq_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	// 空基（子集 null 即 0 句柄）直入 miss 臂。
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isnull, base))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isnull, missL, chkL))
	w.Write(fmt.Sprintf("%s:\n", chkL))
	key := ea.ArgumentExpression
	if key != nil && (key.Kind == ast.KindStringLiteral || key.Kind == ast.KindNoSubstitutionTemplateLiteral) {
		s, ok := saKeyofLitText(key)
		if !ok {
			return "", "keyof literal key is not lowerable"
		}
		if off, ok := def.offsets[s]; ok {
			v := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", v, base, off))
			w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", slot, v))
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		} else {
			w.Write(fmt.Sprintf("  jmp %s\n", missL))
		}
	} else {
		kh, msg := saEvalStr(w, key, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		for _, f := range def.fields {
			fh := saLowerStringLiteral(w, f.name, scope, nextTemp)
			eq := saStringContentEq(w, kh, fh, false, scope, nextTemp)
			saReleaseOwnedTemp(w, scope, fh)
			hitL := fmt.Sprintf("L_koq_hit_%d", *scope.nextLabel)
			*scope.nextLabel++
			nextL := fmt.Sprintf("L_koq_next_%d", *scope.nextLabel)
			*scope.nextLabel++
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", eq, hitL, nextL))
			w.Write(fmt.Sprintf("%s:\n", hitL))
			v := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", v, base, def.offsets[f.name]))
			w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", slot, v))
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
			w.Write(fmt.Sprintf("%s:\n", nextL))
		}
		w.Write(fmt.Sprintf("  jmp %s\n", missL))
	}
	w.Write(fmt.Sprintf("%s:\n", missL))
	// 右臂快照点（D 惰性求值新建的归属临时量随槽消费，否则返前释放在直通
	// 臂上引用未定义寄存器；串空合核同律）。
	mark := len(scope.ownOrder)
	d, msg := saEvalI32(w, dflt, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", slot, d))
	for _, nm := range scope.ownOrder[mark:] {
		if saIsTempOp(nm) {
			saConsumeOwn(scope, nm)
		}
	}
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return out, ""
}

// saKeyofLitText 取字面量键文本（串字面量/无替换模板，取煮后文本）。
func saKeyofLitText(key *ast.Node) (string, bool) {
	if key == nil {
		return "", false
	}
	if key.Kind != ast.KindStringLiteral && key.Kind != ast.KindNoSubstitutionTemplateLiteral {
		return "", false
	}
	return key.Text(), true
}

// saLowerIndexLoadExpr lowering 下标读表达式 `a[i]`/`a?.[i]`（基为绑定数组或数组值调用；
// `?.` 空基归零；下标走 i32 求值，读回走越界归零 join）。
func saLowerIndexLoadExpr(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	isOpt := ea.QuestionDotToken != nil
	// Map 种基走 get 脱糖（`m[k]` ≡ `m.get(k)`；Set 无键值，大声拒）。
	if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
		if tm, ok := saTopMapLookup(scope, ea.Expression); ok {
			if isOpt {
				return "", "optional map index reads are not lowerable"
			}
			slot, msg := saLowerTopMap(w, tm, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			return saLowerMapIndexLoad(w, slot, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
		}
		if k, ok := scope.types[ea.Expression.Text()]; ok && (k == "map" || k == "set") {
			if k == "set" {
				return "", "Set index reads need .has (no keyed values)"
			}
			if isOpt {
				return "", "optional map index reads are not lowerable"
			}
			return saLowerMapIndexLoad(w, ea.Expression.Text(), ea.ArgumentExpression, scope, pos, refusals, nextTemp)
		}
	}
	// keyof 动态键读先行（`p[k]`；实例基非数组，saArrValueOf 不识；字面量
	// 键静态折叠，动态键 strcmp 链；上游句柄当下标错译，本仓 correct）。
	if layout, def, ok := saKeyofBase(ea.Expression, scope); ok {
		return saLowerKeyofIndex(w, ea, layout, def, scope, pos, refusals, nextTemp)
	}
	// Object.entries 的 pair 数组：位次读走专用径（saPairSlot 按静态位次
	// 分种）；此处直读位次不明 → 大声拒（禁把键的句柄地址当整数绑出）。
	if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier &&
		scope.arrPairEl != nil && scope.arrPairEl[ea.Expression.Text()] {
		return "", "Object.entries pair elements must be read as es[i][0] (string key) or es[i][1] (i32 value)"
	}
	// Object.entries pair 位 1（i32 值）：先取外层 pair 句柄，再读位 1。
	if lit, ok := saPairSlot(ea.AsNode(), scope); ok {
		if lit != 1 {
			return "", "Object.entries pair slot 0 is the string key (read it as a string)"
		}
		base := ea.Expression.AsElementAccessExpression()
		pairH, msg := saLowerIndexLoadExpr(w, base, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		out := saLowerCheckedIndex(w, pairH, "1", scope.nextLabel, nextTemp)
		saReleaseOwnedTemp(w, scope, pairH)
		return out, ""
	}
	// 顶层 const 数组下标读（i32 元快照物化后走既有越界归零径；串元走串位，
	// 嵌套走双下标臂，此处 i32 位大声拒；局部/mod 槽遮蔽优先；写位/方法位/别名位
	// 无臂沿旧门大声拒；698/758/798）。
	if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
		if arr, ok := saTopArrLookup(scope, ea.Expression); ok && arr.objs != nil && arr.layout != "" {
			base := saMaterializeTopArr(w, arr, scope, nextTemp)
			idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if msg := saCheckIntIndex(scope, idx); msg != "" {
				return "", msg
			}
			var out string
			if isOpt {
				out = saLowerOptionalIndex(w, base, idx, scope.nextLabel, nextTemp)
			} else {
				out = saLowerCheckedIndex(w, base, idx, scope.nextLabel, nextTemp)
			}
			saReleaseOwnedTemp(w, scope, base)
			// Element handle kind drives downstream field loads: the
			// instance branch of the str/i32/f64 evaluators resolves the
			// layout from this recorded kind (saInstBaseElem).
			scope.types[out] = "inst:" + arr.layout
			return out, ""
		}
		if arr, ok := saTopArrLookup(scope, ea.Expression); ok && !arr.str && arr.nested == nil {
			base := saMaterializeTopArr(w, arr, scope, nextTemp)
			idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if msg := saCheckIntIndex(scope, idx); msg != "" {
				return "", msg
			}
			if isOpt {
				out := saLowerOptionalIndex(w, base, idx, scope.nextLabel, nextTemp)
				saReleaseOwnedTemp(w, scope, base)
				return out, ""
			}
			out := saLowerCheckedIndex(w, base, idx, scope.nextLabel, nextTemp)
			saReleaseOwnedTemp(w, scope, base)
			return out, ""
		}
	}
	// 顶层嵌套数组双下标读（深度 2 快照物化 + 两级越界归零 join；次级经空守卫读，
	// 外层 OOB 空柄归零不崩（本地链式同形崩溃实锤，本仓不扩散）；单下标值位沿旧门
	// 大声拒（禁句柄当整数）；`?.` 任一级沿旧门；798/808）。
	if ea.Expression != nil && ea.Expression.Kind == ast.KindElementAccessExpression && ea.QuestionDotToken == nil {
		if inner := ea.Expression.AsElementAccessExpression(); inner != nil && inner.QuestionDotToken == nil &&
			inner.Expression != nil && inner.Expression.Kind == ast.KindIdentifier {
			if arr, ok := saTopArrLookup(scope, inner.Expression); ok && saTopArrDepth(arr) == 2 {
				base := saMaterializeTopArr(w, arr, scope, nextTemp)
				ii, msg := saEvalI32(w, inner.ArgumentExpression, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				if msg := saCheckIntIndex(scope, ii); msg != "" {
					return "", msg
				}
				h1 := saLowerCheckedIndex(w, base, ii, scope.nextLabel, nextTemp)
				saReleaseOwnedTemp(w, scope, base)
				oi, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				if msg := saCheckIntIndex(scope, oi); msg != "" {
					return "", msg
				}
				out := saLowerOptionalIndex(w, h1, oi, scope.nextLabel, nextTemp)
				return out, ""
			}
		}
	}
	// 顶层嵌套数组三下标读（深度 3 快照物化 + 三级越界归零 join；后两级经空守卫读；
	// 链长须与快照深度相等（短链值位沿旧门，禁句柄当整数）；`?.` 任一级沿旧门；808）。
	if ea.Expression != nil && ea.Expression.Kind == ast.KindElementAccessExpression && ea.QuestionDotToken == nil {
		if mid := ea.Expression.AsElementAccessExpression(); mid != nil && mid.QuestionDotToken == nil &&
			mid.Expression != nil && mid.Expression.Kind == ast.KindElementAccessExpression {
			if inner := mid.Expression.AsElementAccessExpression(); inner != nil && inner.QuestionDotToken == nil &&
				inner.Expression != nil && inner.Expression.Kind == ast.KindIdentifier {
				if arr, ok := saTopArrLookup(scope, inner.Expression); ok && saTopArrDepth(arr) == 3 {
					base := saMaterializeTopArr(w, arr, scope, nextTemp)
					i0, msg := saEvalI32(w, inner.ArgumentExpression, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					if msg := saCheckIntIndex(scope, i0); msg != "" {
						return "", msg
					}
					h1 := saLowerCheckedIndex(w, base, i0, scope.nextLabel, nextTemp)
					saReleaseOwnedTemp(w, scope, base)
					i1, msg := saEvalI32(w, mid.ArgumentExpression, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					if msg := saCheckIntIndex(scope, i1); msg != "" {
						return "", msg
					}
					h2 := saLowerOptionalIndex(w, h1, i1, scope.nextLabel, nextTemp)
					i2, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					if msg := saCheckIntIndex(scope, i2); msg != "" {
						return "", msg
					}
					out := saLowerOptionalIndex(w, h2, i2, scope.nextLabel, nextTemp)
					return out, ""
				}
			}
		}
	}
	base, msg := saArrValueOf(w, ea.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", "index base must be bound array"
	}
	// struct 数组取元具化（`b.items[i]` 8 字节柄槽；越界归零柄；记 inst 种，
	// 链读/绑定经种分发；`?.` 沿旧门；裸标识符 struct 数组另步）。
	if layout, ok := saStructArrElemLayout(ea.Expression, scope); ok {
		if isOpt {
			return "", "optional struct index reads are not lowerable"
		}
		h, msg := saLowerStructArrElem(w, ea, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		scope.types[h] = "inst:" + layout
		return h, ""
	}
	idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	if msg := saCheckIntIndex(scope, idx); msg != "" {
		return "", msg
	}
	if isOpt {
		// `a?.[i]` (null base reads 0, otherwise checked-index join).
		out := saLowerOptionalIndex(w, base, idx, scope.nextLabel, nextTemp)
		saReleaseOwnedTemp(w, scope, base)
		if layout, ok := scope.arrElemLayout[base]; ok {
			scope.types[out] = "inst:" + layout
		}
		return out, ""
	}
	out := saLowerCheckedIndex(w, base, idx, scope.nextLabel, nextTemp)
	// 下标读后链式基即释（具名/借用基 no-op；值已物化为 i32）。
	saReleaseOwnedTemp(w, scope, base)
	if layout, ok := scope.arrElemLayout[base]; ok {
		scope.types[out] = "inst:" + layout
	}
	return out, ""
}

// saTernaryArrArm reports an array-ternary arm as (str, known): literals by
// element scan, identifiers by bound marking, wrappers/nested ternaries by
// recursion; calls and exotic shapes are unknown (loud refusal downstream—no
// silent handle-kind guess; cf h4/h5 pointer-as-int, UP same-wrong).
// saIsEmptyArrayLiteral reports a (possibly parenthesized/as-wrapped) empty
// array literal. An empty arm carries no element kind, so it is a wildcard
// in array-ternary str agreement: `cond ? [str] : []` is a string array,
// not a mixed-kind ternary.
func saIsEmptyArrayLiteral(e *ast.Node) bool {
	for e != nil {
		switch e.Kind {
		case ast.KindParenthesizedExpression:
			e = e.AsParenthesizedExpression().Expression
		case ast.KindAsExpression:
			e = e.AsAsExpression().Expression
		case ast.KindSatisfiesExpression:
			e = e.AsSatisfiesExpression().Expression
		case ast.KindNonNullExpression:
			e = e.AsNonNullExpression().Expression
		case ast.KindArrayLiteralExpression:
			al := e.AsArrayLiteralExpression()
			return al != nil && (al.Elements == nil || len(al.Elements.Nodes) == 0)
		default:
			return false
		}
	}
	return false
}

func saTernaryArrArm(e *ast.Node, scope *saScope) (bool, bool) {
	for e != nil {
		switch e.Kind {
		case ast.KindParenthesizedExpression:
			e = e.AsParenthesizedExpression().Expression
		case ast.KindAsExpression:
			e = e.AsAsExpression().Expression
		case ast.KindSatisfiesExpression:
			e = e.AsSatisfiesExpression().Expression
		case ast.KindNonNullExpression:
			e = e.AsNonNullExpression().Expression
		case ast.KindTypeAssertionExpression:
			e = e.AsTypeAssertion().Expression
		case ast.KindConditionalExpression:
			nce := e.AsConditionalExpression()
			if nce == nil {
				return false, false
			}
			ts, tok := saTernaryArrArm(nce.WhenTrue, scope)
			fs, fok := saTernaryArrArm(nce.WhenFalse, scope)
			return ts || fs, tok && fok
		case ast.KindArrayLiteralExpression:
			return saLiteralIsStrArray(e, scope), true
		case ast.KindIdentifier:
			return scope.arrStr[e.Text()], true
		case ast.KindCallExpression:
			// 调用臂唯签名种可信（用户函数 `arr`/`arrStr`；内建方法种
			// `saArrCallRet` 元模糊如 slice，沿未知拒；u1 实证）。
			if k, ok := saCallRetKind(e.AsCallExpression(), scope); ok {
				if k == "arr" {
					return false, true
				}
				if k == "arrStr" {
					return true, true
				}
			}
			return false, false
		default:
			return false, false
		}
	}
	return false, false
}

// saLowerOptionalLength lowers `a?.length`（空基读 0，否则头 +8；
// 与 `a?.[i]` 守卫槽（saLowerOptionalIndex）、`b?.v` 守卫槽同形；
// 空即 undefined≡0 子集口径；上游同形（eq+槽+load）实证）。
func saLowerOptionalLength(w printer.EmitTextWriter, base string, scope *saScope, nextTemp *int) string {
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	nullL := fmt.Sprintf("L_len_null_%d", *scope.nextLabel)
	*scope.nextLabel++
	okL := fmt.Sprintf("L_len_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_len_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	isnull := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isnull, base))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isnull, nullL, okL))
	w.Write(fmt.Sprintf("%s:\n", nullL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", okL))
	v := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", v, base))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest
}

// saLowerObjectKeys `Object.keys(o)` 已知布局字段名序具化串元数组（字段声明序；
// 存取器/未知布局沿旧门；与 values 同口径，keys 计数折叠同源；串元数组形状复
// split 分段数组：空数组 + 逐元 push + 串元标记）。
func saLowerObjectKeys(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return "", "Object.keys needs a bound object"
	}
	k, ok := scope.types[e.Text()]
	if !ok || len(k) <= 5 || k[:5] != "inst:" {
		return "", "Object.keys needs a known-layout object"
	}
	def, ok := scope.classes[k[5:]]
	if !ok || def == nil {
		return "", "Object.keys needs a known-layout object"
	}
	if len(def.getters) > 0 || len(def.setters) > 0 {
		return "", "Object.keys needs data slots"
	}
	names := []string{}
	for _, f := range def.fields {
		names = append(names, f.name)
	}
	h := saNewEmptyArray(w, nextTemp)
	saOwnTemp(scope, h)
	saMarkArrStr(scope, h)
	for _, n := range names {
		seg := saLowerStringLiteral(w, n, scope, nextTemp)
		saLowerArrayPush(w, h, seg, scope, nextTemp)
	}
	return h, ""
}

// saLiteralIndex 取字面量下标（非字面量返 false）：Object.entries 的 pair
// 位次按静态下标分种（位 0 串键 / 位 1 i32 值），动态下标拒（禁运行时猜位次）。
func saLiteralIndex(e *ast.Node) (int, bool) {
	if e == nil || e.Kind != ast.KindNumericLiteral {
		return 0, false
	}
	var v int
	if _, err := fmt.Sscanf(e.Text(), "%d", &v); err != nil {
		return 0, false
	}
	return v, true
}

// saPairSlot 判断元素访问是否落在 Object.entries 的 pair 上：内层读的基须是
// 「entries 数组的字面量下标访问」，内外下标皆字面量 → 返回位次（0=串键、
// 1=i32 值）。链式读 es[0][0] 的内层基是元素访问（非标识符），故按「基是
// entries 数组的元素访问」判定（标记打在 entries 数组上）。
func saPairSlot(e *ast.Node, scope *saScope) (int, bool) {
	ea := e.AsElementAccessExpression()
	if ea == nil || ea.QuestionDotToken != nil {
		return 0, false
	}
	if ea.Expression == nil || ea.Expression.Kind != ast.KindElementAccessExpression {
		return 0, false
	}
	base := ea.Expression.AsElementAccessExpression()
	if base == nil || base.QuestionDotToken != nil {
		return 0, false
	}
	if base.Expression == nil || base.Expression.Kind != ast.KindIdentifier {
		return 0, false
	}
	if scope.arrPair == nil || !scope.arrPair[base.Expression.Text()] {
		return 0, false
	}
	if _, ok := saLiteralIndex(base.ArgumentExpression); !ok {
		return 0, false
	}
	lit, ok := saLiteralIndex(ea.ArgumentExpression)
	if !ok {
		return 0, false
	}
	return lit, true
}

// saMarkArrPair 标记 Object.entries 结果数组（其元素为 pair 数组）。
func saMarkArrPair(scope *saScope, name string) {
	if scope.arrPair == nil {
		scope.arrPair = map[string]bool{}
	}
	scope.arrPair[name] = true
}

// saCallReturnsPairArray 判定调用是否为 `Object.entries(o)`（其结果为 pair
// 数组；供声明位播种标记与解构位拒因共用）。
func saCallReturnsPairArray(ce *ast.CallExpression) bool {
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier ||
		pa.Expression.Text() != "Object" || pa.Name() == nil || pa.Name().Text() != "entries" {
		return false
	}
	return ce.Arguments != nil && len(ce.Arguments.Nodes) == 1
}

// saLowerObjectEntries `Object.entries(o)` 已知布局 → 键值对数组（每元一个
// 2 元数组 {key, value}：位 0 存键的串句柄地址、位 1 存 i32 值；外层数组存
// 各 pair 的地址，与嵌套数组模型同形）。键序 = 字段声明序。句柄域字段沿旧门
// 拒（pair 值槽为 i32 字）。逐字段展开落字（无循环——循环体内建的句柄其域尾释
// 在体外 → verifier UnknownRegister 实锤，step2223 教训）。
func saLowerObjectEntries(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return "", "Object.entries needs a bound object"
	}
	k, ok := scope.types[e.Text()]
	if !ok || len(k) <= 5 || k[:5] != "inst:" {
		return "", "Object.entries needs a known-layout object"
	}
	def, ok := scope.classes[k[5:]]
	if !ok || def == nil {
		return "", "Object.entries needs a known-layout object"
	}
	if len(def.getters) > 0 || len(def.setters) > 0 {
		return "", "Object.entries needs data slots"
	}
	out := saNewEmptyArray(w, nextTemp)
	saOwnTemp(scope, out)
	if scope.arrNest == nil {
		scope.arrNest = map[string]bool{}
	}
	scope.arrNest[out] = true
	saMarkArrPair(scope, out)
	for _, f := range def.fields {
		if def.fkinds[f.name] != "i32" {
			return "", "Object.entries needs i32 fields (handle fields are not lowerable)"
		}
		key := saLowerStringLiteral(w, f.name, scope, nextTemp)
		v, msg := saLowerClassFieldLoad(w, e.Text(), def, f.name, scope, nextTemp)
		if msg != "" {
			return "", msg
		}
		buf := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", buf))
		saOwnTemp(scope, buf)
		b0 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", b0, buf))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", b0, key))
		b1 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 4\n", b1, buf))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", b1, v))
		pair := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", pair))
		saOwnTemp(scope, pair)
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", pair, buf))
		w.Write(fmt.Sprintf("  store %s + 8, 2 as u64\n", pair))
		if scope.arrPairEl == nil {
			scope.arrPairEl = map[string]bool{}
		}
		scope.arrPairEl[pair] = true
		saLowerArrayPush(w, out, pair, scope, nextTemp)
	}
	return out, ""
}

// saLowerObjectValues `Object.values(o)` 已知布局 i32 数据槽具化新数组（字段声明序；存取器/异种槽/未知布局沿旧门；577）。
func saLowerObjectValues(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return "", "Object.values needs a bound object"
	}
	k, ok := scope.types[e.Text()]
	if !ok || len(k) <= 5 || k[:5] != "inst:" {
		return "", "Object.values needs a known-layout object"
	}
	def, ok := scope.classes[k[5:]]
	if !ok || def == nil {
		return "", "Object.values needs a known-layout object"
	}
	if len(def.getters) > 0 || len(def.setters) > 0 {
		return "", "Object.values needs data slots"
	}
	offs := []int{}
	for _, f := range def.fields {
		off, ok := def.offsets[f.name]
		if !ok || def.fkinds[f.name] != "i32" {
			return "", "Object.values needs i32 data slots"
		}
		offs = append(offs, off)
	}
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %d\n", data, len(offs)*4))
	for i, off := range offs {
		v := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", v, e.Text(), off))
		w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", data, i*4, v))
	}
	head := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", head))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", head, data))
	w.Write(fmt.Sprintf("  store %s + 8, %d as u64\n", head, len(offs)))
	w.Write(fmt.Sprintf("  !%s\n", data))
	saOwnTemp(scope, head)
	return head, ""
}

// saLowerLengthExpr lowering `.length`（数组/字符串头 +8 u64；其余成员拒）。
func saLowerLengthExpr(w printer.EmitTextWriter, pa *ast.PropertyAccessExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	_ = refusals
	if pa.QuestionDotToken != nil {
		// 可选链仅 ?.length 走空守卫；其余成员大声拒（879 补丁：曾误将任意 ?.member 折为取长）。
		if nm := pa.Name(); nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() != "length" {
			return "", "optional member access not lowerable"
		}
		// `a?.length` 具名数组基走空守卫 join；括号基同步解包；其余基沿旧门。
		if base, ok := saArrBase(scope, saUnwrapTransparent(pa.Expression)); ok {
			return saLowerOptionalLength(w, base, scope, nextTemp), ""
		}
		// 可空串 `s?.length` 具名串基走同形空守卫（`string|null` 零句柄；
		// 空读 0，非空读头 +8；与数组 480 同形；上游同形实证 parity；491）。
		if e := saUnwrapTransparent(pa.Expression); e != nil && e.Kind == ast.KindIdentifier {
			if k, ok := scope.types[e.Text()]; ok && k == "str" {
				return saLowerOptionalLength(w, e.Text(), scope, nextTemp), ""
			}
			// 顶层折叠串基（具化后走同形守卫，具化柄用后即释；589 同形）。
			if _, ok := scope.types[e.Text()]; !ok {
				if text, ok := scope.topConsts[e.Text()]; ok && scope.topStr[e.Text()] {
					h := saLowerStringLiteral(w, text, scope, nextTemp)
					ln := saLowerOptionalLength(w, h, scope, nextTemp)
					saReleaseOwnedTemp(w, scope, h)
					return ln, ""
				}
			}
			// 顶层 const 数组 `?.length`（快照具化后走同形空守卫；i32/串元皆可，头 +8；
			// 局部/mod 槽遮蔽优先；698/758）。
			if arr, ok := saTopArrLookup(scope, e); ok {
				h := saMaterializeTopArr(w, arr, scope, nextTemp)
				ln := saLowerOptionalLength(w, h, scope, nextTemp)
				saReleaseOwnedTemp(w, scope, h)
				return ln, ""
			}
		}
		// 链式基 `o.f?.length`（inst 字段 arr/str 柄；空柄读 0，否则头 +8；
		// 与 plain .length 链式基 saArrValueOf 同形，只是取长换空守卫 join）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression {
			if h, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
				return saLowerOptionalLength(w, h, scope, nextTemp), ""
			}
			if sh, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
				return saLowerOptionalLength(w, sh, scope, nextTemp), ""
			}
		}
		// 调用结果 `?.length`（新鲜非空柄，空臂不可达，等价直读；plain 调用基 868 同形；586）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindCallExpression {
			if h, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
				saReleaseOwnedTemp(w, scope, h)
				return t, ""
			}
			// 串调用 `?.length`（串求值柄经空守卫，空读 0；与具名串基同形；878）。
			if sh, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
				ln := saLowerOptionalLength(w, sh, scope, nextTemp)
				saReleaseOwnedTemp(w, scope, sh)
				return ln, ""
			}
		}
		return "", "optional member access not lowerable"
	}
	// Map/Set 同柄链 `.size` 属性（`m.set(k,v).size`；内层原位变异弃值，
	// 外层在绑定上读长；与调用链同形；1318）。
	if nm := pa.Name(); nm != nil && nm.Kind == ast.KindIdentifier && nm.Text() == "size" &&
		pa.Expression != nil && pa.Expression.Kind == ast.KindCallExpression {
		if im, recv, k, ok := saMapChainBase(pa.Expression.AsCallExpression(), scope); ok {
			if _, _, msg := saLowerMapCall(w, recv, k, im, pa.Expression.AsCallExpression(), scope, pos, refusals, nextTemp); msg != "" {
				return "", msg
			}
			if k == "map" {
				scope.addImport("sa_std/btree_map.sa")
			} else {
				scope.addImport("sa_std/btree_set.sa")
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_%s_len(&%s)\n", t, k, recv))
			saOwnTemp(scope, t)
			return t, ""
		}
	}
	if nm := pa.Name(); nm != nil && nm.Kind == ast.KindIdentifier && nm.Text() == "size" &&
		pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		// lib.es2015.collection.d.ts `readonly size: number` 属性形：复用
		// sa_map.go `size()` 臂同符号（`sci/sa_std/btree_map.sa`/`btree_set.sa`
		// `sa_btree_{map,set}_len`），本侧只 @import + 调用，零手写。
		if k, ok := scope.types[pa.Expression.Text()]; ok && (k == "map" || k == "set") {
			recv := pa.Expression.Text()
			if k == "map" {
				scope.addImport("sa_std/btree_map.sa")
			} else {
				scope.addImport("sa_std/btree_set.sa")
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_%s_len(&%s)\n", t, k, recv))
			saOwnTemp(scope, t)
			return t, ""
		}
	}
	// 顶层 Record 快照点读 `T.k`：用点物化后按 get 同义（链式值种透传）。
	if nm := pa.Name(); nm != nil && nm.Kind == ast.KindIdentifier && nm.Text() != "length" &&
		pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && !saIsMapMethod(nm.Text()) {
		if tm, ok := saTopMapLookup(scope, pa.Expression); ok {
			if pa.QuestionDotToken != nil {
				return "", "optional map index reads are not lowerable"
			}
			slot, msg := saLowerTopMap(w, tm, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			scope.addImport("sa_std/btree_map.sa")
			kh := saLowerStringLiteral(w, nm.Text(), scope, nextTemp)
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_get(&%s, &%s)\n", t, slot, kh))
			saReleaseOwnedTemp(w, scope, kh)
			saOwnTemp(scope, t)
			if strings.HasPrefix(tm.vkind, "map:") {
				scope.types[t] = "map"
				saSetMapVal(scope, t, tm.vkind[4:])
			} else {
				scope.types[t] = tm.vkind
			}
			return t, ""
		}
		if k, ok := scope.types[pa.Expression.Text()]; ok && k == "map" {
			recv := pa.Expression.Text()
			scope.addImport("sa_std/btree_map.sa")
			kh := saLowerStringLiteral(w, nm.Text(), scope, nextTemp)
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_get(&%s, &%s)\n", t, recv, kh))
			saReleaseOwnedTemp(w, scope, kh)
			saOwnTemp(scope, t)
			vkind := scope.mapVals[recv]
			if vkind == "" {
				vkind = "i32"
			}
			scope.types[t] = vkind
			return t, ""
		}
	}
	// `match.index` for a variable bound to String.match: the array struct has
	// no index field, so the match lowering carries group-0 start in a side
	// channel (scope.matchIndex). Miss value is -1; guarded code (if (!match))
	// never reads it. Byte offset == JS char index for ASCII inputs only.
	if nm := pa.Name(); nm != nil && nm.Kind == ast.KindIdentifier && nm.Text() == "index" &&
		pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		if idx, ok := scope.matchIndex[pa.Expression.Text()]; ok {
			return idx, ""
		}
	}
	if nm := pa.Name(); nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() != "length" {
		return "", "only .length member access lowerable"
	}
	// const 空柄具名直读头必崩（`const t: string|null = null; t.length`
	// 落 `load t+8` 读零址，真机 SIGSEGV 实证；上游同错 parity-in-wrong，
	// 薄口大声拒；`?.` 守卫径在上已分流不受影响；491）。
	if e := saUnwrapTransparent(pa.Expression); e != nil && e.Kind == ast.KindIdentifier && saIsNullConst(scope, e.Text()) {
		if k, ok := scope.types[e.Text()]; ok && (k == "str" || k == "arr") {
			return "", "const null handle has no .length (definite null dereference)"
		}
	}
	// 括号数组基透明（`(a).length` 即 `a.length`；483 实例双门同例）。
	if base, ok := saArrBase(scope, saUnwrapTransparent(pa.Expression)); ok {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, base))
		return t, ""
	}
	// 顶层 const 数组取长（快照物化读头 +8，读后即释；i32/串元皆可；
	// 局部/mod 槽遮蔽优先；589 折叠串臂同形；698/758）。
	if e := saUnwrapTransparent(pa.Expression); e != nil && e.Kind == ast.KindIdentifier {
		if arr, ok := saTopArrLookup(scope, e); ok {
			h := saMaterializeTopArr(w, arr, scope, nextTemp)
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
			saReleaseOwnedTemp(w, scope, h)
			return t, ""
		}
	}
	// 链式下标基（`pairs[0].length` 经句柄总线递归求内层句柄；与读位同形）。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindElementAccessExpression {
		if h, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
			return t, ""
		}
		// 顶层嵌套数组内层取长（`N[i].length` 快照物化 + 内层头 +8；798）。
		if inner := pa.Expression.AsElementAccessExpression(); inner != nil && inner.QuestionDotToken == nil &&
			inner.Expression != nil && inner.Expression.Kind == ast.KindIdentifier {
			if arr, ok := saTopArrLookup(scope, inner.Expression); ok && arr.nested != nil {
				base := saMaterializeTopArr(w, arr, scope, nextTemp)
				ii, msg := saEvalI32(w, inner.ArgumentExpression, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				if msg := saCheckIntIndex(scope, ii); msg != "" {
					return "", msg
				}
				h1 := saLowerCheckedIndex(w, base, ii, scope.nextLabel, nextTemp)
				saReleaseOwnedTemp(w, scope, base)
				ln := saLowerOptionalLength(w, h1, scope, nextTemp)
				return ln, ""
			}
		}
		// 顶层嵌套数组深层内层取长（`D[i][j].length`，深度 3；各级 OOB 归零；808）。
		if mid := pa.Expression.AsElementAccessExpression(); mid != nil && mid.QuestionDotToken == nil &&
			mid.Expression != nil && mid.Expression.Kind == ast.KindElementAccessExpression {
			if inner := mid.Expression.AsElementAccessExpression(); inner != nil && inner.QuestionDotToken == nil &&
				inner.Expression != nil && inner.Expression.Kind == ast.KindIdentifier {
				if arr, ok := saTopArrLookup(scope, inner.Expression); ok && saTopArrDepth(arr) == 3 {
					base := saMaterializeTopArr(w, arr, scope, nextTemp)
					i0, msg := saEvalI32(w, inner.ArgumentExpression, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					if msg := saCheckIntIndex(scope, i0); msg != "" {
						return "", msg
					}
					h1 := saLowerCheckedIndex(w, base, i0, scope.nextLabel, nextTemp)
					saReleaseOwnedTemp(w, scope, base)
					i1, msg := saEvalI32(w, mid.ArgumentExpression, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					if msg := saCheckIntIndex(scope, i1); msg != "" {
						return "", msg
					}
					h2 := saLowerOptionalIndex(w, h1, i1, scope.nextLabel, nextTemp)
					ln := saLowerOptionalLength(w, h2, scope, nextTemp)
					return ln, ""
				}
			}
		}
	}
	// 嵌套链基（`q.r.a.length` 经句柄总线；与下标基同形）。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression {
		if h, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
			return t, ""
		}
	}
	// 调用结果基（`s.split(",").length` 经调用求句柄；saArrValueOf 已识
	// split/数组返回调用，失败沿旧路落串门；见 step375）。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindCallExpression {
		// `Object.keys(o).length` 已知布局字段数静态折叠（与 hasOwn 同门；
		// 仅数据槽计数，存取器在场保守拒；未知布局沿旧门）。
		if ce := pa.Expression.AsCallExpression(); ce != nil {
			if n, ok := saLayoutKeyCount(ce, scope); ok {
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %d\n", t, n))
				return t, ""
			}
		}
		if h, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
			// 调用新柄读后即释（具名/借用基 no-op）。
			saReleaseOwnedTemp(w, scope, h)
			return t, ""
		}
		// join 调用基走串位（R3-13b 已开门；saEvalStr 经 saIsArrJoinCall
		// 识串，结果读后即释；失败沿旧路落串门）。
		if ce := pa.Expression.AsCallExpression(); ce != nil && saIsArrJoinCall(ce, scope) {
			if sh, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, sh))
				saReleaseOwnedTemp(w, scope, sh)
				return t, ""
			}
		}
	}
	// 新鲜构造取长（`new Array(n).length` 经构造求句柄；读后即释；
	// 调用式 `Array(n).length` 经调用臂已通；1298）。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindNewExpression {
		if h, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
			saReleaseOwnedTemp(w, scope, h)
			return t, ""
		}
	}
	// Map/Set 用 `.size()` 方法（属性形大声拒）。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		if k, ok := scope.types[pa.Expression.Text()]; ok && (k == "map" || k == "set") {
			return "", "use .size() method on " + k
		}
	}
	// 字符串 `.length`：字面量/调用结果等非常驻基先具化为句柄。
	h, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", ".length base must be bound array or string"
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
	// 调用新柄读后即释（具名/借用基 no-op；join 径 705 同形）。
	saReleaseOwnedTemp(w, scope, h)
	return t, ""
}

// saArrStoreBase 求下标存基（绑定直传；链式下标基递归读回内层句柄；
// 字面量/调用基仍拒——写临时无意义，沿既有 loud 门）。
func saArrStoreBase(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if base, ok := saArrBase(scope, e); ok {
		return base, ""
	}
	if e != nil && e.Kind == ast.KindElementAccessExpression {
		ea := e.AsElementAccessExpression()
		if ea.QuestionDotToken != nil {
			return "", "optional index access not lowerable"
		}
		inner, msg := saArrValueOf(w, ea.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if msg := saCheckIntIndex(scope, idx); msg != "" {
			return "", msg
		}
		return saLowerCheckedIndex(w, inner, idx, scope.nextLabel, nextTemp), ""
	}
	return "", "not a bound array"
}

// saArrBase 报告绑定数组变量的句柄名（未绑定/非数组即失败）。
func saArrBase(scope *saScope, n *ast.Node) (string, bool) {
	if n == nil || n.Kind != ast.KindIdentifier {
		return "", false
	}
	nm := n.Text()
	if k, ok := scope.types[nm]; !ok || k != "arr" {
		return "", false
	}
	return nm, true
}

// saLowerDestructuringDecl lowering 解构声明（形状证据：封存
// lowerDestructuringDecl:5414-5461 + destructureArray:5309-5333 +
// bindPatternName:5513-5524 + destructureObject:5465-5509：数组位逐元
// lowerCheckedIndex（越界归零 join）绑定 i32；空穴跳过，rest 大声拒，嵌套位
// 大声拒；对象位按布局偏移直读（注解优先，次之源绑定 `inst:` 布局；串域记
// str 其余 i32，与形参排空同门）。
// 数组源须为数组句柄（已绑定数组直传；字面量现场构造）；函数值不可解构。
func saLowerDestructuringDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, pat *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer != nil && (vd.Initializer.Kind == ast.KindArrowFunction || vd.Initializer.Kind == ast.KindFunctionExpression) {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function values do not destructure"})
		return false
	}
	if pat.Kind == ast.KindObjectBindingPattern {
		return saLowerObjDestructuringDecl(w, d, vd, pat, scope, pos, refusals, nextTemp)
	}
	if pat.Kind == ast.KindArrayBindingPattern && vd.Initializer != nil &&
		vd.Initializer.Kind == ast.KindCallExpression &&
		saCallReturnsPairArray(vd.Initializer.AsCallExpression()) {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "Object.entries pair destructuring is not lowerable (the key is a string; read es[i][0] and es[i][1] instead)"})
		return false
	}
	if pat.Kind != ast.KindArrayBindingPattern {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("binding pattern %d is not lowerable", int(pat.Kind))})
		return false
	}
	if vd.Initializer == nil {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring declaration needs initializer"})
		return false
	}
	var arr string
	litLen := -1
	// 纯类型包装透明（`[] as i32[]` 与字面量同形；483 `(a).length` 同例；1358）。
	srcInit := saUnwrapTransparent(vd.Initializer)
	if srcInit.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, srcInit, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported array literal: " + msg})
			return false
		}
		if srcInit.AsArrayLiteralExpression().Elements != nil {
			litLen = len(srcInit.AsArrayLiteralExpression().Elements.Nodes)
		}
		arr = h
		// 空字面量断言透传（`[] as string[]`：无元可判种，按断言记串；
		// 包裹剥离 + 断言取类型名与对象位 1851 同例；1878 残留）。
		if outer := vd.Initializer; outer != nil {
			for outer != nil && (outer.Kind == ast.KindParenthesizedExpression || outer.Kind == ast.KindNonNullExpression) {
				if outer.Kind == ast.KindParenthesizedExpression {
					outer = outer.AsParenthesizedExpression().Expression
				} else {
					outer = outer.AsNonNullExpression().Expression
				}
			}
			var tnode *ast.TypeNode
			if outer != nil && outer.Kind == ast.KindAsExpression {
				if ae := outer.AsAsExpression(); ae != nil {
					tnode = ae.Type
				}
			} else if outer != nil && outer.Kind == ast.KindSatisfiesExpression {
				if se := outer.AsSatisfiesExpression(); se != nil {
					tnode = se.Type
				}
			}
			if tnode != nil && saIsStringArrayAnnot(tnode) {
				saMarkArrStr(scope, h)
			}
		}
	} else if base, ok := saArrBase(scope, srcInit); ok {
		arr = base
	} else if saIsArrayCtor(srcInit) {
		// 新鲜构造解构源（`const [x,y] = new Array(9,8)`；现场具化 + 归属登记，
		// 与字面量源同律；空穴形缺省按未知长大声拒（空穴读 undefined 才走缺省，
		// 0 口径不可代；无缺省空穴读 0 沿 480/501 口径）；1308）。
		h, msg := saLowerArrayCtor(w, srcInit, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported array constructor: " + msg})
			return false
		}
		saOwnTemp(scope, h)
		arr = h
		if !saIsHoleArrayCtor(srcInit) {
			litLen = saCtorElemCount(srcInit)
		}
	} else {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring source must be bound array"})
		return false
	}
	var srcElems []*ast.Node
	if srcInit.Kind == ast.KindArrayLiteralExpression {
		srcElems = []*ast.Node{}
		if elems := srcInit.AsArrayLiteralExpression().Elements; elems != nil {
			for _, n := range elems.Nodes {
				srcElems = append(srcElems, n)
			}
		}
	}
	elemIsStr := scope.arrStr != nil && scope.arrStr[arr]
	return saLowerArrayPatternElems(w, d, arr, litLen, srcElems, elemIsStr, pat, scope, pos, refusals, nextTemp)
}

// saLowerArrayPatternElems lowering 数组模式逐元（saLowerDestructuringDecl
// 数组位本体；抽取供嵌套递归复用，零语义差）。
func saLowerArrayPatternElems(w printer.EmitTextWriter, d *ast.Node, arr string, litLen int, srcElems []*ast.Node, elemIsStr bool, pat *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	idx := 0
	// 逐元判种（字面量源按 idx 位源元精确：内层字面量即 arr，
	// 串求值元即串（与构造 `saArrayLiteralElem` 判向同谓词），其余
	// i32；混合字面量各元记各的种（`[[1], 2]` 的 b 恒 i32，1882
	// 实证）；缺位/绑定源沿模式级标记（串/嵌套/其余），p8g 同例）。
	kindOf := func() string {
		if srcElems != nil {
			if idx < len(srcElems) && srcElems[idx] != nil {
				se := srcElems[idx]
				if se.Kind == ast.KindArrayLiteralExpression {
					return "arr"
				}
				if saIsStrExpr(se, scope) {
					return "str"
				}
			} else if elemIsStr {
				return "str"
			}
			return "i32"
		}
		if elemIsStr {
			return "str"
		}
		if scope.arrNest != nil && scope.arrNest[arr] {
			return "arr"
		}
		return "i32"
	}
	// rowIsStrArray 报告当前元是否为串元数组（两步串透传标记源：
	// 字面量行内叶全串直判，绑定行凭外层内叶串标记）。
	rowIsStrArray := func() bool {
		if srcElems != nil {
			if idx < len(srcElems) && srcElems[idx] != nil &&
				srcElems[idx].Kind == ast.KindArrayLiteralExpression {
				return saLiteralIsStrArray(srcElems[idx], scope)
			}
			return false
		}
		return scope.arrInnerStr != nil && scope.arrInnerStr[arr]
	}
	sawRest := false
	for _, el := range pat.AsBindingPattern().Elements.Nodes {
		if sawRest {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest element must be last"})
			return false
		}
		if el.Kind != ast.KindBindingElement {
			idx++
			continue
		}
		be := el.AsBindingElement()
		if be.DotDotDotToken != nil {
			// 剩余解构（`const [x, ...rest] = a`；尾段切片现场具化（R1
			// `@ts_arr_slice_copy` 现货，标记透传与归属同切片径）；rest 须处末
			// （TS 本错）；嵌套 rest 沿旧门；越界起位钳空与切片同律。
			sawRest = true
			nm := be.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest element needs a plain name"})
				return false
			}
			name := nm.Text()
			if _, dup := scope.types[name]; dup {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
				return false
			}
			h := saLowerArraySlice(w, arr, fmt.Sprintf("%d", idx), "", scope, nextTemp)
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "arr"
			saPropArrNest(scope, arr, name)
			saPropArrStr(scope, arr, name)
			saPropArrInnerStr(scope, arr, name)
			saDeclareInitOwn(scope, name, h)
			idx++
			continue
		}
		nm := be.Name()
		if nm == nil {
			idx++
			continue
		}
		if nm.Kind != ast.KindIdentifier {
			if nm.Kind != ast.KindArrayBindingPattern {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested destructuring shape is not lowerable"})
				return false
			}
			// 嵌套数组模式（`const [[a]] = [[1]]`）：外层元读回内层句柄
			// 再递归逐元（上游 m2 同形双守卫；对象内嵌双边同拒沿旧门；
			// 串元作数组解构系病态，TS 本错，大声拒；绑定源凭 `arrNest`
			// 存在证明放行（元确为数组），内叶种凭 `arrInnerStr` 标记，
			// 无标记一律 i32，与扁平未知元同律）。
			if elemIsStr {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested array pattern over string elements is not lowerable"})
				return false
			}
			var innerElems []*ast.Node
			innerLitLen := -1
			innerIsStr := false
			if srcElems != nil {
				if idx >= len(srcElems) || srcElems[idx] == nil || srcElems[idx].Kind != ast.KindArrayLiteralExpression {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested array pattern needs a literal inner array"})
					return false
				}
				innerElems = []*ast.Node{}
				innerLitLen = 0
				if elems := srcElems[idx].AsArrayLiteralExpression().Elements; elems != nil {
					for _, n := range elems.Nodes {
						innerElems = append(innerElems, n)
					}
					innerLitLen = len(elems.Nodes)
				}
				innerIsStr = saLiteralIsStrArray(srcElems[idx], scope)
			} else {
				if scope.arrNest == nil || !scope.arrNest[arr] {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested array pattern over runtime array is not lowerable yet"})
					return false
				}
				if scope.arrInnerStr != nil && scope.arrInnerStr[arr] {
					innerIsStr = true
				}
			}
			inner := saLowerCheckedIndex(w, arr, fmt.Sprintf("%d", idx), scope.nextLabel, nextTemp)
			if !saLowerArrayPatternElems(w, d, inner, innerLitLen, innerElems, innerIsStr, nm.AsNode(), scope, pos, refusals, nextTemp) {
				return false
			}
			idx++
			continue
		}
		name := nm.Text()
		if _, dup := scope.types[name]; dup {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		// 缺省折叠（字面量源下标编译期已知：界内缺省恒死走元，
		// 越界按元种求缺省式（i32 经 `saEvalI32`，串经 `saEvalStr`）；
		// 绑定源运行时守卫：界内走元，越界臂内惰性求缺省值（JS 语义；
		// 上游 OOB 恒存 0 系静默错值，p8b 实证 bun 5 vs 上游 0，正确优先）。
		if be.Initializer != nil && (litLen < 0 || idx >= litLen) {
			if litLen >= 0 {
				var op, msg string
				if elemIsStr {
					op, msg = saEvalStr(w, be.Initializer, scope, pos, refusals, nextTemp)
				} else {
					op, msg = saEvalI32(w, be.Initializer, scope, pos, refusals, nextTemp)
				}
				if msg != "" {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
				w.Write(fmt.Sprintf("  %s = %s\n", name, op))
				scope.types[name] = kindOf()
				saDeclareInitOwn(scope, name, op)
				idx++
				continue
			}
			// 数组元缺省 OOB 现场构造（缺省须数组字面量，OOB 臂具化绑定；
			// 标量缺省系 TS 本错沿旧门大声拒；源内叶串种须与缺省字面串种一致
			// （空字面通配），错配大声拒禁静默；臂内新生归属逆序释放（分支内
			// 定义跨 join 沿用即 UnknownRegister，h1/k6 实证）；上游丢缺省恒存
			// 0 系静默错值，p8b 同族，正确优先）。
			if kindOf() == "arr" {
				if be.Initializer == nil || be.Initializer.Kind != ast.KindArrayLiteralExpression {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array element defaults over bindings are not lowerable yet"})
					return false
				}
				defStr := saLiteralIsStrArray(be.Initializer, scope)
				srcStr := scope.arrInnerStr != nil && scope.arrInnerStr[arr]
				defEmpty := true
				if ale := be.Initializer.AsArrayLiteralExpression(); ale != nil && ale.Elements != nil {
					if len(ale.Elements.Nodes) > 0 {
						defEmpty = false
					}
				}
				if !defEmpty && defStr != srcStr {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array element default kind does not match nested source"})
					return false
				}
				v, msg := saLowerCheckedIndexDefaultArr(w, arr, fmt.Sprintf("%d", idx), scope, pos, refusals, scope.nextLabel, nextTemp, be.Initializer)
				if msg != "" {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
				w.Write(fmt.Sprintf("  %s = %s\n", name, v))
				scope.types[name] = kindOf()
				if defStr || (defEmpty && srcStr) {
					saMarkArrStr(scope, name)
				}
				saDeclareInitOwn(scope, name, v)
				idx++
				continue
			}
			v, msg := saLowerCheckedIndexDefault(w, arr, fmt.Sprintf("%d", idx), scope, pos, refusals, scope.nextLabel, nextTemp, be.Initializer, elemIsStr)
			if msg != "" {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, v))
			scope.types[name] = kindOf()
			saDeclareInitOwn(scope, name, v)
			idx++
			continue
		}
		v := saLowerCheckedIndex(w, arr, fmt.Sprintf("%d", idx), scope.nextLabel, nextTemp)
		w.Write(fmt.Sprintf("  %s = %s\n", name, v))
		scope.types[name] = kindOf()
		// 行串标记（字面量行内叶全串直判；绑定行凭外层内叶串标记；
		// 两步串透传 o14 同例）。
		if rowIsStrArray() {
			saMarkArrStr(scope, name)
		}
		saDeclareInitOwn(scope, name, v)
		idx++
	}
	return true
}

// saIsNsAliasSrc 判定标识符是否为命名空间别名源（单文件 `N.f` 预扫双键或
// program `u.add` 链接点键在场，且源名未被值绑定/类占用；与 step194 调用
// 守卫同形）。
func saIsNsAliasSrc(scope *saScope, src string) bool {
	if scope == nil || src == "" {
		return false
	}
	if _, bound := scope.types[src]; bound {
		return false
	}
	if _, isClass := scope.classes[src]; isClass {
		return false
	}
	prefix := src + "."
	for k := range scope.funcs {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range scope.linkResolve {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// saLowerNsDestructuringDecl lowering 命名空间别名解构（`const {f} = N` /
// `const {add} = u` / `const {d: delta} = T`）：逐元直绑限定被调（无码；
// 调用点经既有具名/链接分发）。program 点键优先（前缀已定），单文件走
// `N_f` 发射（defPrefix 续接，自文件签名透传）。
// 拒因逐字对齐封存 link_nsobject.go:152-198（模式/元素/缺省/名/键/嵌套/
// 未导出）；重名沿既有 `duplicate local` 门。
func saLowerNsDestructuringDecl(d *ast.Node, pat *ast.Node, src string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) bool {
	for _, el := range pat.AsBindingPattern().Elements.Nodes {
		if el.Kind != ast.KindBindingElement {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace destructuring needs plain elements"})
			return false
		}
		be := el.AsBindingElement()
		if be.DotDotDotToken != nil {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace destructuring needs plain elements"})
			return false
		}
		if be.Initializer != nil {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace destructuring defaults are not in the subset"})
			return false
		}
		nm := be.Name()
		if nm != nil && (nm.Kind == ast.KindArrayBindingPattern || nm.Kind == ast.KindObjectBindingPattern) {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace destructuring needs plain local names (no nesting)"})
			return false
		}
		if nm == nil || nm.Kind != ast.KindIdentifier {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace destructuring needs plain names"})
			return false
		}
		field := nm.Text()
		if be.PropertyName != nil {
			pn := be.PropertyName.AsNode()
			if pn.Kind != ast.KindIdentifier && pn.Kind != ast.KindStringLiteral {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace destructuring keys must be identifiers or strings"})
				return false
			}
			field = pn.Text()
		}
		local := nm.Text()
		if _, dup := scope.types[local]; dup {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + local})
			return false
		}
		// 函数名保留（共享 funcs 表无作用域恢复钩子，遮蔽必静默错位；
		// 与 `let` 遮蔽函数名既有严格性同形；禁静默错码高于同形）。
		if _, dup := scope.funcs[local]; dup {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + local})
			return false
		}
		if q, ok := scope.linkResolve[src+"."+field]; ok {
			if scope.linkResolve == nil {
				scope.linkResolve = map[string]string{}
			}
			scope.linkResolve[local] = q
			continue
		}
		if _, ok := scope.funcs[src+"."+field]; ok {
			emit := scope.defPrefix + src + "_" + field
			if _, ok := scope.funcs[emit]; !ok {
				if sig, ok := scope.funcs[src+"_"+field]; ok {
					scope.funcs[emit] = sig
				}
			}
			if scope.linkResolve == nil {
				scope.linkResolve = map[string]string{}
			}
			scope.linkResolve[local] = emit
			continue
		}
		ln, col := pos(el.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: field + " is not exported by " + src})
		return false
	}
	return true
}

// saLiteralKeySet 取对象字面量静态键集（解构缺省折叠用；方法/展开跳过；
// 原始与去引号双记，调用点字段取法不一，按任一命中）。
func saLiteralKeySet(lit *ast.Node) map[string]bool {
	out := map[string]bool{}
	if lit == nil || lit.Kind != ast.KindObjectLiteralExpression {
		return out
	}
	for _, p := range lit.AsObjectLiteralExpression().Properties.Nodes {
		if p == nil {
			continue
		}
		if p.Kind != ast.KindPropertyAssignment && p.Kind != ast.KindShorthandPropertyAssignment {
			continue
		}
		if nm := p.Name(); nm != nil {
			out[nm.Text()] = true
		}
		if fn, ok := saObjPropName(p); ok {
			out[fn] = true
		}
	}
	return out
}

// saLowerObjDestructuringDecl lowering 对象解构声明（`const {x, y: z} = src`；
// 布局源：声明注解 TypeReference 优先，次之源标识符绑定的 `inst:` 布局；
// 串域头指针读记 str，其余 i32；rest/缺省/嵌套/计算键/未知域/重名一律大声拒。
// 形状证据：封存 destructureObject:5465-5509（源绑定布局 + 串名字面键 +
// 按域种读回）+ 本仓形参排空对象位（串/i32 双臂 + 同文拒因）；字面量源另步）。
func saLowerObjDestructuringDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, pat *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	var def *saClassDef
	if vd.Type != nil && vd.Type.Kind == ast.KindTypeReference {
		if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			def, _ = scope.classes[ref.TypeName.Text()]
		}
	}
	src := ""
	if vd.Initializer != nil && vd.Initializer.Kind == ast.KindIdentifier {
		src = vd.Initializer.Text()
		if def == nil {
			if k, ok := scope.types[src]; ok && len(k) > 5 && k[:5] == "inst:" {
				def, _ = scope.classes[k[5:]]
			}
		}
	}
	// `this` 源：布局取接收者类（`scope.thisClass`，与 `this.v` 读位同源；
	// 句柄取 `scope.thisSelf`，直读 `load hid + off` 与上游 r2 形同形；
	// 静态方法 thisSelf 为空沿旧门；注解优先既有）。
	if vd.Initializer != nil && vd.Initializer.Kind == ast.KindThisKeyword && scope.thisSelf != "" {
		src = scope.thisSelf
		if def == nil && scope.thisClass != "" {
			def, _ = scope.classes[scope.thisClass]
		}
	}
	effInit := vd.Initializer
	outerInit := vd.Initializer
	for outerInit != nil && (outerInit.Kind == ast.KindParenthesizedExpression || outerInit.Kind == ast.KindNonNullExpression) {
		if outerInit.Kind == ast.KindParenthesizedExpression {
			outerInit = outerInit.AsParenthesizedExpression().Expression
		} else {
			outerInit = outerInit.AsNonNullExpression().Expression
		}
	}
	if outerInit != nil && (outerInit.Kind == ast.KindAsExpression || outerInit.Kind == ast.KindSatisfiesExpression) &&
		def == nil && src == "" {
		// 断言包装字面量源（`const {x} = {x:5} as Pt`；声明位 sa_decl.go:142
		// 同例 + 1358 数组透明包装同例；布局取断言类型名；`as const`/泛型/
		// 错配沿旧门；成功时 effInit 供下缺省折叠键集）。
		var inner *ast.Node
		var tnode *ast.TypeNode
		if outerInit.Kind == ast.KindSatisfiesExpression {
			if se := outerInit.AsSatisfiesExpression(); se != nil {
				inner, tnode = se.Expression, se.Type
			}
		} else {
			if ae := outerInit.AsAsExpression(); ae != nil {
				inner, tnode = ae.Expression, ae.Type
			}
		}
		for inner != nil && (inner.Kind == ast.KindParenthesizedExpression || inner.Kind == ast.KindNonNullExpression) {
			if inner.Kind == ast.KindParenthesizedExpression {
				inner = inner.AsParenthesizedExpression().Expression
			} else {
				inner = inner.AsNonNullExpression().Expression
			}
		}
		if inner != nil && inner.Kind == ast.KindObjectLiteralExpression && tnode != nil && tnode.Kind == ast.KindTypeReference {
			if ref := tnode.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && ref.TypeArguments == nil {
				if _, ok := scope.classes[ref.TypeName.Text()]; ok {
					h, defname, msg := saLowerObjectLiteral(w, inner, ref.TypeName.Text(), scope, pos, refusals, nextTemp)
					if msg != "" {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
						return false
					}
					if d2, ok := scope.classes[defname]; ok {
						def = d2
					}
					src = h
					effInit = inner
				}
			}
		}
	}
	if vd.Initializer != nil && vd.Initializer.Kind == ast.KindObjectLiteralExpression &&
		(def == nil || src == "") {
		// 字面量源现场具化（注解供布局时亦须物化——旧 `def == nil` 条件跳过
		// 注解+字面量形致 src 悬空；无注解按键集匹配；具名注解须同名，泛型优先
		// 具化；封存 destructureObject:5465-5509 + layoutOfLiteral 键集匹配）。
		want := ""
		if vd.Type != nil {
			if vd.Type.Kind != ast.KindTypeReference {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object destructuring needs a recorded struct layout"})
				return false
			}
			if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
				want = ref.TypeName.Text()
				if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
					if lname, ok := saInstantiateIface(want, ref.TypeArguments.Nodes, scope.classes); ok {
						want = lname
					}
				}
			}
		}
		h, defname, msg := saLowerObjectLiteral(w, vd.Initializer, want, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return false
		}
		if d2, ok := scope.classes[defname]; ok {
			def = d2
		}
		src = h
	}
	if def == nil && src == "" && vd.Initializer != nil && saCouldBeInst(vd.Initializer, scope) &&
		(vd.Initializer.Kind == ast.KindElementAccessExpression || vd.Initializer.Kind == ast.KindCallExpression) {
		// 下标/调用源现场求值（`const {x} = M[k]`；布局按读回记种消解；
		// 上游先求值后布局同形；非实例源沿消解门大声拒）。
		h, d2, msg := saInstBaseElem(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return false
		}
		if d2 == nil {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object destructuring needs a recorded struct layout"})
			return false
		}
		src, def = h, d2
	}
	if def != nil && src == "" && vd.Initializer != nil && saCouldBeInst(vd.Initializer, scope) &&
		(vd.Initializer.Kind == ast.KindElementAccessExpression || vd.Initializer.Kind == ast.KindCallExpression) {
		// 注解+下标/调用源（`const {x}: T = M[k]`；注解定布局，源须同布局；
		// 与上分支同形；布局错配沿本函数旧文拒）。
		h, d2, msg := saInstBaseElem(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return false
		}
		if d2 == nil || d2.name != def.name {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object destructuring needs a recorded struct layout"})
			return false
		}
		src = h
	}
	if def == nil {
		// 命名空间别名源（`const {f} = N` / `const {add} = u`）：成员直绑
		// 限定被调；布局源另走上门。形状证据：封存 link_nsobject.go
		// lowerNsDestructure（p3 单层静态；spread/动态沿旧门）。
		if src != "" && saIsNsAliasSrc(scope, src) {
			return saLowerNsDestructuringDecl(d, pat, src, scope, pos, refusals)
		}
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object destructuring needs a recorded struct layout"})
		return false
	}
	if src == "" {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object destructuring needs a recorded struct layout"})
		return false
	}
	hid := src
	// 字面量源键集（缺省折叠用；标识源存在性未知不折，litKeys 为空即全拒；
	// 断言包装源以解包字面量为准）。
	isLitSrc := effInit != nil && effInit.Kind == ast.KindObjectLiteralExpression
	litKeys := saLiteralKeySet(effInit)
	return saLowerObjPatternElems(w, d, def, hid, isLitSrc, litKeys, pat, scope, pos, refusals, nextTemp)
}

// saLowerObjPatternElems lowering 对象模式逐元（saLowerObjDestructuringDecl
// 对象位本体；抽取供嵌套递归复用，零语义差）。
func saLowerObjPatternElems(w printer.EmitTextWriter, d *ast.Node, def *saClassDef, hid string, isLitSrc bool, litKeys map[string]bool, pat *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	excluded := map[string]bool{}
	sawRest := false
	for _, el := range pat.AsBindingPattern().Elements.Nodes {
		if sawRest {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest element must be last"})
			return false
		}
		if el.Kind != ast.KindBindingElement {
			continue
		}
		be := el.AsBindingElement()
		if be.DotDotDotToken != nil {
			// 对象剩余解构（`const {x, ...rest} = o`；须居末 + 纯标识名；
			// 剩余域合成子布局后 alloc + 逐域复制（种分发与对象 spread
			// 复制臂同形）；上游 destructureObject 无 rest 臂，此处为
			// spread 形复合，非自造语义）。
			sawRest = true
			nm := be.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier || be.PropertyName != nil || be.Initializer != nil {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest element needs a plain name"})
				return false
			}
			name := nm.Text()
			if _, dup := scope.types[name]; dup {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
				return false
			}
			rdef := saRestLayout(def, excluded, scope.classes)
			h := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			if rdef.size == 0 {
				w.Write(fmt.Sprintf("  %s = alloc 4\n", h))
			} else {
				w.Write(fmt.Sprintf("  %s = alloc %d\n", h, rdef.size))
			}
			saOwnTemp(scope, h)
			for _, f := range rdef.fields {
				fk := rdef.fkinds[f.name]
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				if fk == "str" || fk == "arr" || fk == "inst" {
					w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, hid, def.offsets[f.name]))
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, f.offset, t))
					continue
				}
				if fk == "f64" {
					w.Write(fmt.Sprintf("  %s = load %s + %d as f64\n", t, hid, def.offsets[f.name]))
					w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, f.offset, t))
					continue
				}
				w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", t, hid, def.offsets[f.name]))
				w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, f.offset, t))
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "inst:" + rdef.name
			saDeclareInitOwn(scope, name, h)
			continue
		}
		nm := be.Name()
		if nm == nil || (nm.Kind != ast.KindIdentifier && nm.Kind != ast.KindObjectBindingPattern) {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested destructuring shape is not lowerable"})
			return false
		}
		field := ""
		if nm.Kind == ast.KindIdentifier {
			field = nm.Text()
		}
		if be.PropertyName != nil {
			pn := be.PropertyName.AsNode()
			if pn.Kind != ast.KindIdentifier && pn.Kind != ast.KindStringLiteral {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed destructuring keys are not lowerable"})
				return false
			}
			field = pn.Text()
		}
		if nm.Kind != ast.KindIdentifier && field == "" {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested destructuring shape is not lowerable"})
			return false
		}
		off, ok := def.offsets[field]
		if !ok {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + field + " is not in the " + def.name + " layout"})
			return false
		}
		excluded[field] = true
		if nm.Kind == ast.KindObjectBindingPattern {
			// 嵌套对象模式（`const {p: {v}} = b`）：外层域读回内层句柄
			// 再递归逐域（实例布局固定，内外偏移皆静态，读链恒 sound；
			// 内层布局凭 `fsub` 子布局名消解，非 inst 域作对象解构系
			// 病态，TS 运行时 TypeError，大声拒；对象内嵌上游拒，
			// 此处为双 verified-lowering 复合，非自造语义）。
			sub, ok := def.fsub[field]
			innerDef, ok2 := scope.classes[sub]
			if !ok || !ok2 || def.fkinds[field] != "inst" {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested object pattern needs a recorded nested layout"})
				return false
			}
			inner := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", inner, hid, off))
			if !saLowerObjPatternElems(w, d, innerDef, inner, false, map[string]bool{}, nm.AsNode(), scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		name := nm.Text()
		if _, dup := scope.types[name]; dup {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		// 缺省折叠（字面量源键集编译期已知：键在则缺省恒死走字段，
		// 缺则按域种求值缺省式；绑定源以记录布局为准：键在布局即恒在
		// （实例布局固定，缺省字段构造期置零，417 同律），缺省恒死走下
		// 字段读；上游 p1/p2/p7/p9 同形（`load o + off` 直读，缺省丢弃）；
		// 布局外键已由上门大声拒）。
		if be.Initializer != nil && isLitSrc && !litKeys[field] {
			var op, msg string
			kind := "i32"
			if def.fkinds[field] == "str" {
				kind = "str"
				op, msg = saEvalStr(w, be.Initializer, scope, pos, refusals, nextTemp)
			} else {
				op, msg = saEvalI32(w, be.Initializer, scope, pos, refusals, nextTemp)
			}
			if msg != "" {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = kind
			continue
		}
		if def.fkinds[field] == "str" {
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", name, hid, off))
			scope.types[name] = "str"
			continue
		}
		// inst 域绑布局种（两步解构透传：`const {p} = b` 后 `const {v} = p`
		// 经既有 `inst:` 源消解布局；与成员读 step92 同律；子布局未记录
		// 沿旧 i32 口径，零行为变）。
		if def.fkinds[field] == "inst" {
			if sub, ok := def.fsub[field]; ok {
				if _, ok2 := scope.classes[sub]; ok2 {
					w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", name, hid, off))
					scope.types[name] = "inst:" + sub
					continue
				}
			}
		}
		w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", name, hid, off))
		scope.types[name] = "i32"
	}
	return true
}

// saLowerInferredDecl lowering 无注解声明的类型推断（见上注释）。
// 数组/bool/i32 三通道复用已有求值与落字，不自造语义。
// saLowerArrDecl lowering 数组声明（`let a: number[] = […]` 字面构造；
// 同类句柄拷贝 `= b`；缺 init/const 缺 init/非字面皆大声拒）。
func saLowerArrDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, name string, isConst bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer == nil {
		ln, col := pos(d.Pos())
		if isConst {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
			return false
		}
		// 无初值数组/元组零柄（`let a: T[]`/`let t: [...]`；后继赋值重绑；
		// 读未赋值即空错，上游 `a = 0` 同形）。
		w.Write(fmt.Sprintf("  %s = 0\n", name))
		scope.types[name] = "arr"
		saDeclarePlain(scope, name)
		return true
	}
	if vd.Initializer.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported array literal: " + msg})
			return false
		}
		// 显式 i32 元注解收串元拒（4 字节槽截断句柄；串注解/无注解沿既有口径；
		// 按语法种判定；铁律 4）。元组同门（`[i32, string]` 混元同崩；全串元组沿 arrStr 口径豁免；537）。
		if vd.Type != nil && !saIsStringArrayAnnot(vd.Type) &&
			!(vd.Type.Kind == ast.KindTupleType && saLiteralIsStrArray(vd.Initializer, scope)) &&
			(vd.Type.Kind == ast.KindArrayType || vd.Type.Kind == ast.KindTupleType || (vd.Type.Kind == ast.KindTypeReference && vd.Type.AsTypeReferenceNode() != nil)) {
			if al := vd.Initializer.AsArrayLiteralExpression(); al != nil && al.Elements != nil {
				for _, el := range al.Elements.Nodes {
					if el != nil && el.Kind != ast.KindSpreadElement && el.Kind != ast.KindOmittedExpression &&
						(saIsStrValue(el, scope) || saCouldBeInst(el, scope)) {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array element kind mismatch (non-string array takes i32 values)"})
						return false
					}
				}
			}
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		scope.types[name] = "arr"
		saConsumeOwn(scope, h)
		saDeclareOwned(scope, name)
		saPropArrNest(scope, h, name)
		saPropArrStr(scope, h, name)
		saPropArrInnerStr(scope, h, name)
		saPropArrElemLayout(scope, h, name)
		// 嵌套串字面量内叶直记（`const ss = [["x"]]` 无注解亦定种；
		// 标注形由声明位注解门记，此处只看字面量）。
		if saLiteralIsNestedStrArray(vd.Initializer, scope) {
			saMarkArrInnerStr(scope, name)
		}
		return true
	}
	// 数组构造式（`Array(n)`/`Array(a, b)`；`new Array(n)` 由声明位直办）。
	if saIsArrayCtor(vd.Initializer) && vd.Initializer.Kind == ast.KindCallExpression {
		h, msg := saLowerArrayCtor(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		scope.types[name] = "arr"
		saConsumeOwn(scope, h)
		saDeclareOwned(scope, name)
		return true
	}
	// 绑定句柄与数组返回调用皆直传（slice/concat/map 等新鲜句柄）。
	if src, msg := saArrValueOf(w, vd.Initializer, scope, pos, refusals, nextTemp); msg == "" {
		// 具名柄直传即别名（赋值位 H13 同门；`const r = a` 既往放行致 UseAfterMove 实证；注释既有偏离上游自认；513）。
		if _, named := scope.types[src]; named && !saIsTempOp(src) {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: handle copies need an explicit clone (pass the handle directly)"})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, src))
		scope.types[name] = "arr"
		// 新鲜 temp 句柄消费（具名直传不碰：名下值仍由名释放，上游同形拒拷贝，
		// 本仓沿既有直传口径，禁静默 move）。
		if saIsTempOp(src) {
			saConsumeOwn(scope, src)
		}
		saDeclareOwned(scope, name)
		saPropArrNest(scope, src, name)
		saPropArrStr(scope, src, name)
		saPropArrInnerStr(scope, src, name)
		saPropArrElemLayout(scope, src, name)
		if vd.Initializer != nil && vd.Initializer.Kind == ast.KindCallExpression {
			if ce := vd.Initializer.AsCallExpression(); ce != nil && ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
				if pa := ce.Expression.AsPropertyAccessExpression(); pa != nil && pa.Name() != nil && pa.Name().Text() == "match" {
					if scope.matchArr == nil {
						scope.matchArr = map[string]bool{}
					}
					scope.matchArr[name] = true
					if idx, ok := scope.matchIndexTemp[src]; ok {
						if scope.matchIndex == nil {
							scope.matchIndex = map[string]string{}
						}
						scope.matchIndex[name] = idx
					}
				}
			}
		}
		// 函数串数组返回绑定记串元（`const sv = get()`；调用柄新鲜无标记，
		// 签名种为准，与右值下标分支同源）。
		if vd.Initializer != nil && vd.Initializer.Kind == ast.KindCallExpression {
			if saFuncReturnsStrArray(vd.Initializer.AsCallExpression(), scope) {
				saMarkArrStr(scope, name)
			}
			// Object.entries 结果数组标记（其元素为 pair：位 0 串键 / 位 1 i32 值）。
			if saCallReturnsPairArray(vd.Initializer.AsCallExpression()) {
				saMarkArrPair(scope, name)
			}
		}
		return true
	}
	// 顶层 const 数组别名大声拒（注解形；快照无共享柄；698）。
	if vd.Initializer != nil && vd.Initializer.Kind == ast.KindIdentifier {
		if _, shadowed := scope.types[vd.Initializer.Text()]; !shadowed {
			if _, isMod := scope.modVars[vd.Initializer.Text()]; !isMod {
				if _, ok := scope.topArrs[vd.Initializer.Text()]; ok {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "top-level const array alias is not lowerable (snapshots have no shared handle; read elements directly)"})
					return false
				}
			}
		}
	}
	ln, col := pos(d.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array initializer must be literal or array"})
	return false
}

// saBoundI32 报告绑定 i32 变量名（未绑定或非 i32 即失败）。
// foBindingName:11440-11450 + foBindingPattern:11428-11438：
// 初始化位须为 VariableDeclarationList 单声明；标识符取名单，
// 非标识符 Name 即 pattern 位）。
// 返回 (name, pattern, ok)：pattern 非空时为解构位（调用方大声拒），
// ok=false 时形状不支持（多声明/缺声明/非声明位）。
func saForBindingName(init *ast.Node) (string, *ast.Node, bool) {
	if init == nil || init.Kind != ast.KindVariableDeclarationList {
		return "", nil, false
	}
	// `using` 头大声拒（随声明位同门；调用方以通用初始化位信息拒出）。
	if init.Flags&ast.NodeFlagsUsing != 0 {
		return "", nil, false
	}
	decls := init.AsVariableDeclarationList().Declarations.Nodes
	if len(decls) != 1 {
		return "", nil, false
	}
	nm := decls[0].Name()
	if nm == nil {
		return "", nil, false
	}
	if nm.Kind == ast.KindIdentifier {
		return nm.Text(), nil, true
	}
	return "", nm.AsNode(), true
}

// saForArrHandle 取 for-of/for-in 被巡数组句柄（复用 step12 底座）：
// 已绑定数组直传句柄（引用语义直传）；数组字面量走 saLowerArrayLiteral
// 现场构造（alloc 16 头 + 缓冲 + 逐槽 store）；其余一律大声拒。
// 形状证据：封存 lowerForOf:2123/lowerForIn:2191 的 lowerExpr(fo.Expression)
// 位（本薄口仅支持句柄/字面量子集）。
// saForStrOrArrHandle 取 for-of 被巡句柄（数组经 saForArrHandle 原样；
// 串经 saEvalStr 求柄，调用方按串形取字；其余沿数组门大声拒。串判定先行，
// 免数组门误记拒因（求柄各臂自带 import/归属）。
func saForStrOrArrHandle(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node) (string, string, bool) {
	if saIsStrExpr(e, scope) {
		sh, msg := saEvalStr(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported for-of base: %s", msg)})
			return "", "", false
		}
		return sh, sh, true
	}
	h, ok := saForArrHandle(w, e, scope, pos, refusals, nextTemp, where, "for-of")
	return h, "", ok
}

// saIsHoleArrayCtor 识别空穴构造式（单长 `new Array(n)`/`Array(n)`；
// 空穴在 for-in 被跳过（计数分叉）、for-of 读 undefined（值分叉），
// 巡回位须大声拒；直接下标读 0 沿 480/501 既定口径不管；1298）。
func saIsHoleArrayCtor(e *ast.Node) bool {
	if e == nil || !saIsArrayCtor(e) {
		return false
	}
	var argNodes []*ast.Node
	isOf := false
	if e.Kind == ast.KindCallExpression {
		ce := e.AsCallExpression()
		if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
			pa := ce.Expression.AsPropertyAccessExpression()
			isOf = pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" &&
				pa.Name() != nil && pa.Name().Text() == "of"
		}
		if isOf {
			return false
		}
		if ce.Arguments != nil {
			argNodes = ce.Arguments.Nodes
		}
		return len(argNodes) == 1
	}
	if e.Kind == ast.KindNewExpression {
		if ne := e.AsNewExpression(); ne.Arguments != nil {
			argNodes = ne.Arguments.Nodes
		}
		return len(argNodes) == 1
	}
	return false
}

func saForArrHandle(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node, what string) (string, bool) {
	// 纯类型包装透明（`(a)`/`a as T` 与裸形同门；1358 解构源同例；
	// 先解包再进空穴门，免 `(new Array(n))` 绕过空穴设防；1368）。
	e = saUnwrapTransparent(e)
	// 空穴构造巡回大声拒（for-in 跳过空穴计数分叉、for-of 空穴值 undefined
	// 与 0 分叉；直接下标读 0 沿 480/501 口径不管；1298）。
	if saIsHoleArrayCtor(e) {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("%s base over hole array is not lowerable (holes skip for-in and read undefined for-of)", what)})
		return "", false
	}
	if e != nil && e.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported %s base: %s", what, msg)})
			return "", false
		}
		return h, true
	}
	if base, ok := saArrBase(scope, e); ok {
		return base, true
	}
	// 新鲜数组构造巡回（`for (v of new Array(n))`；现场具化 + 归属登记，
	// 巡后释与字面量柄同律；1298）。
	if e != nil && e.Kind == ast.KindNewExpression && saIsArrayCtor(e) {
		h, msg := saLowerArrayCtor(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported %s base: %s", what, msg)})
			return "", false
		}
		saOwnTemp(scope, h)
		return h, true
	}
	// 顶层 const 数组巡回（快照物化后走既有索引巡回；巡后释与字面量柄同律；
	// 体内变异沿既有存储/方法门大声拒；728）。
	if h, ok := saTopArrSnapshot(w, e, scope, nextTemp); ok {
		return h, true
	}
	// 链基（`pairs[0]`/`q.r.a` 经句柄总线；失败静默下探旧门）。
	if e != nil && (e.Kind == ast.KindElementAccessExpression || e.Kind == ast.KindPropertyAccessExpression || e.Kind == ast.KindCallExpression) {
		if h, msg := saArrValueOf(w, e, scope, pos, refusals, nextTemp); msg == "" {
			return h, true
		}
		// `.entries()` 迭代器非扁平数组：Map 侧三 u64 一组误巡回 3 倍并解构
		// 越界崩（1098 t5 实证 segfault；数组侧沿旧门已拒）；此处点名大声拒，
		// 其余链形仍落通用门（1067 for-in 等已锁指纹不动）。
		if e.Kind == ast.KindCallExpression {
			if ce := e.AsCallExpression(); ce != nil && ce.Expression != nil &&
				ce.Expression.Kind == ast.KindPropertyAccessExpression {
				if pa := ce.Expression.AsPropertyAccessExpression(); pa != nil && pa.Name() != nil &&
					pa.Name().Text() == "entries" && pa.QuestionDotToken == nil {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("%s base over .entries() iterator needs forEach (entry groups are not flat arrays)", what)})
					return "", false
				}
			}
		}
	}
	ln, col := pos(where.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("%s base must be bound array", what)})
	return "", false
}

// saLowerForOf lowering for-of（形状证据：封存 lowerForOf:2120-2184 索引巡回
// 原样镜像：idx=0 + 头+8 len + top:slt/br + body:base/mul/add/i32读回 +
// 绑定 + 体 + 增量 + jmp top + end；continue→top（非增量前，与 for 的
// cont 分支不对称，文档化原样）；break 落 end。
// sci for 宏（control.sal FOR_INIT/FOR_CHECK/FOR_NEXT、core/loop.sa
// ARRAY_FOR_EACH）为计数/Slice 形，与本 16 字节头 + ptr/len 形状不同，
// 故沿用 legacy br 形，不套宏——禁止原创调用惯例）。
// saLowerForOf lowering for-of（形状证据：封存 lowerForOf:2120-2184 索引巡回
// 原样镜像：idx=0 + 头+8 len + top:slt/br + body:base/mul/add/i32读回 +
// 绑定 + 体 + 增量 + jmp top + end；continue→top（非增量前，与 for 的
// cont 分支不对称，文档化原样）；break 落 end。
// sci for 宏（control.sal FOR_INIT/FOR_CHECK/FOR_NEXT、core/loop.sa
// ARRAY_FOR_EACH）为计数/Slice 形，与本 16 字节头 + ptr/len 形状不同，
// 故沿用 legacy br 形，不套宏——禁止原创调用惯例）。
func saLowerForOf(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	fo := s.AsForInOrOfStatement()
	// `for await...of` 脱糖为同步 for-of（上游同形：空数组直接索引巡回；
	// 子集无 thenable（Promise 值双边同拒在先），await 元素恒等，脱糖可靠；
	// 非数组/串源仍由既有门大声拒，体内 await 沿既有门拒）。
	binding, pat, ok := saForBindingName(fo.Initializer)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-of initializer (single identifier declaration only)"})
		return false
	}
	if pat != nil && pat.Kind != ast.KindArrayBindingPattern {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object patterns in for-of need static element layouts"})
		return false
	}
	// 元素绑定与循环体同域（封存 lowerForOf:2137 pushScope 覆盖绑定+体）。
	saved := saScopeEnter(scope)
	defer saScopeExit(scope, saved)
	if pat == nil {
		if _, dup := scope.types[binding]; dup {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + binding})
			return false
		}
	}
	setVec := ""
	// Set 具名巡回（i32 元经 `sa_btree_set_iter_vec` 物化快照向量后索引巡回，
	// 与 Map.forEach 三字组巡回同形；串元/未知元大声拒，禁静默错读；
	// 快照长口径与数组环预读同形；上游 n2 扁平直读已烂（`+8` 取长，
	// 现头 `+16`），禁照抄；先于数组门判定，免旧拒因残留）。
	if be := fo.Expression; be != nil && be.Kind == ast.KindIdentifier {
		if k, kok := scope.types[be.Text()]; kok && k == "set" {
			if scope.setVals[be.Text()] != "i32" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "for-of over Set needs i32 elements (string-element sets are not flat-iterable)"})
				return false
			}
			scope.addImport("sa_std/btree_set.sa")
			vec := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_set_iter_vec(&%s)\n", vec, be.Text()))
			saOwnTemp(scope, vec)
			setVec = vec
		}
	}
	arrVal, strBase, ok := setVec, "", setVec != ""
	if setVec == "" {
		arrVal, strBase, ok = saForStrOrArrHandle(w, fo.Expression, scope, pos, refusals, nextTemp, s)
	}
	if !ok {
		return false
	}
	bodyStmts, ok := saEmbeddedBlock(fo.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-of body"})
		return false
	}
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", idx))
	topL := fmt.Sprintf("L_forof_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_forof_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_forof_end_%d", *nextLabel)
	*nextLabel++
	lenT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lenT, arrVal))
	// Set 快照向量长为字数（元为键指针/键长双字），计数折半。
	boundT := lenT
	if setVec != "" {
		cntT := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = div %s, 2\n", cntT, lenT))
		boundT = cntT
	}
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL, depth: len(scope.ownOrder)})
	saBindPendingLabels(scope, false)
	w.Write(fmt.Sprintf("%s:\n", topL))
	cT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cT, idx, boundT))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	baseT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", baseT, arrVal))
	elemT := ""
	if setVec != "" {
		// Set 快照元为键指针/键长双字（16 字节步进），i32 元经键单元读回
		//（8 字节 cell 首字；与 saMapKeySlice 槽形同源）。
		offT := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		pairPtr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		keyPtr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		elemT = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 16\n", offT, idx))
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", pairPtr, baseT, offT))
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", keyPtr, pairPtr))
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", elemT, keyPtr))
	} else if strBase != "" {
		// 串元取字（字节精确 1 字柄，复用 saLowerStrIndexChar，与 s[i] 同形）。
		elemT = saLowerStrIndexChar(w, baseT, idx, scope, nextTemp)
	} else {
		offT := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		elemPtr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		elemT = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", offT, idx))
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", elemPtr, baseT, offT))
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", elemT, elemPtr))
	}
	if pat != nil {
		if strBase != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array patterns in for-of need array bases"})
			scope.loops = scope.loops[:len(scope.loops)-1]
			return false
		}
		// Object.entries 的 pair 元素解构：键是串，端口解构位恒 i32 → 大声拒
		// （禁把键的句柄地址当整数绑出，静默错码）。
		if fo.Expression != nil && fo.Expression.Kind == ast.KindCallExpression &&
			saCallReturnsPairArray(fo.Expression.AsCallExpression()) {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "Object.entries pair destructuring is not lowerable (the key is a string; read es[i][0] and es[i][1] instead)"})
			scope.loops = scope.loops[:len(scope.loops)-1]
			return false
		}
		// 数组模式解构（元为内层 slice 句柄，逐元越界归零 join 绑 i32；
		// 空穴跳过，rest/嵌套名大声拒；形状证据：封存 destructureArray +
		// lowerDestructuringDecl 数组位）。
		idx := 0
		for _, el := range pat.AsBindingPattern().Elements.Nodes {
			if el.Kind != ast.KindBindingElement {
				idx++
				continue
			}
			be := el.AsBindingElement()
			if be.DotDotDotToken != nil {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest elements in for-of pattern are not lowerable"})
				scope.loops = scope.loops[:len(scope.loops)-1]
				return false
			}
			nm := be.Name()
			if nm == nil {
				idx++
				continue
			}
			if nm.Kind != ast.KindIdentifier {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested patterns in for-of are not lowerable"})
				scope.loops = scope.loops[:len(scope.loops)-1]
				return false
			}
			if _, dup := scope.types[nm.Text()]; dup {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + nm.Text()})
				scope.loops = scope.loops[:len(scope.loops)-1]
				return false
			}
			v := saLowerCheckedIndex(w, elemT, fmt.Sprintf("%d", idx), scope.nextLabel, nextTemp)
			w.Write(fmt.Sprintf("  %s = %s\n", nm.Text(), v))
			scope.types[nm.Text()] = "i32"
			saDeclareInitOwn(scope, nm.Text(), v)
			idx++
		}
	} else {
		w.Write(fmt.Sprintf("  %s = %s\n", binding, elemT))
		// 每轮重绑复位归属旗标(上游 assignLocal markRebound 同形).
		saMarkRebound(scope, binding)
		saDeclareInitOwn(scope, binding, elemT)
		// 嵌套字面量直巡的行绑定记 arr（行即内层句柄，`row[0]`/`row.length`
		// 可用；扁平直巡仍记 i32；变量被巡元素种未知，沿旧 i32 门）。
		bindKind := "i32"
		if strBase != "" {
			// 串元绑定记 str（1 字柄；求值/拼接经串通道）。
			bindKind = "str"
		}
		if strBase != "" {
			// for-of 串元素记名（单轮新鲜柄，直授 move 安全；540）。
			if scope.forOfStr == nil {
				scope.forOfStr = map[string]bool{}
			}
			scope.forOfStr[binding] = true
		}
		if be := fo.Expression; be != nil && be.Kind == ast.KindArrayLiteralExpression {
			if al := be.AsArrayLiteralExpression(); al.Elements != nil {
				for _, el := range al.Elements.Nodes {
					if el.Kind == ast.KindArrayLiteralExpression {
						bindKind = "arr"
						break
					}
				}
			}
		}
		// string-element receivers bind str (literal pre-scan or binding
		// mark; chains/temps stay i32, mirroring nested-literal handling above).
		if bindKind == "i32" {
			if be := fo.Expression; be != nil && be.Kind == ast.KindArrayLiteralExpression {
				if saLiteralIsStrArray(be, scope) {
					bindKind = "str"
				}
			} else if rbase, ok := saArrBase(scope, fo.Expression); ok && scope.arrStr[rbase] {
				bindKind = "str"
			} else if rbase, ok := saArrBase(scope, fo.Expression); ok && scope.arrNest != nil && scope.arrNest[rbase] {
				// 嵌套数组标识符巡回：元为内层句柄，行绑 arr（字面量直巡同形；
				// 形状证据：封存 for-of 体 `row = t_19` 后 `load row + 8`）。
				bindKind = "arr"
			} else if arr, ok := saTopArrLookup(scope, fo.Expression); ok && arr.str {
				// 顶层串元数组巡回：元为串柄，行绑 str（本地串数组同形；758）。
				bindKind = "str"
			} else if arr, ok := saTopArrLookup(scope, fo.Expression); ok && arr.nested != nil {
				// 顶层嵌套数组巡回：元为内层柄，行绑 arr（本地嵌套同形；798）。
				bindKind = "arr"
			}
		}
		scope.types[binding] = bindKind
	}
	// 体临时量每轮尾释放（行绑定走既有具名释放；体临时量具名不动外全释；同上）。
	savedArmTemps := scope.armReleaseTemps
	scope.armReleaseTemps = true
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.armReleaseTemps = savedArmTemps
	// 行绑定每轮尾释放(堆存活才落字;break/continue 经深释放,返前经释放全部).
	if b := saOwnOf(scope, binding); b != nil && b.heap && !b.consumed && !b.released {
		w.Write(fmt.Sprintf("  !%s\n", binding))
		b.released = true
	}
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if !saArmTerminates(bodyStmts) {
		incT := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", incT, idx))
		w.Write(fmt.Sprintf("  %s = %s\n", idx, incT))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	// 字面量接收 temp 巡后释放(绑定名/链 temp 为 no-op；上游同形).
	// 巡后释本域归属(具名+临时,逆序;已消费/已释放/非堆跳过；上游 releaseScope 同形).
	// 外层绑定早于本域基址,不在此列；体臂条目已随臂退出截断.
	{
		done := map[string]bool{}
		for i := len(scope.ownOrder) - 1; i >= saved.owned; i-- {
			name := scope.ownOrder[i]
			if done[name] {
				continue
			}
			done[name] = true
			if b := saOwnOf(scope, name); saIsTempOp(name) && b != nil && b.heap && !b.consumed && !b.released {
				w.Write(fmt.Sprintf("  !%s\n", name))
				b.released = true
			}
		}
	}
	return true
}

// saLowerForIn lowering for-in（形状证据：封存 lowerForIn:2189-2226：
// 与 for-of 同索引巡回，唯绑定位为下标本身 `binding = idx`（+ declarePlain
// 记 i32）；对象巡回大声拒（须为绑定数组）；continue→top 原样）。
// saLowerForIn lowering for-in（形状证据：封存 lowerForIn:2189-2226：
// 与 for-of 同索引巡回，唯绑定位为下标本身 `binding = idx`（+ declarePlain
// 记 i32）；对象巡回大声拒（须为绑定数组）；continue→top 原样）。
//
// saLowerForInInstFields 对象 for-in 布局字段静态展开（标识基址与调用
// 基址共用：逐份独立域，键名串常量 inline 体，份尾释本份归属；体终结则
// 后续份不可达截断防死码；空布局零次展开）。
func saLowerForInInstFields(w printer.EmitTextWriter, binding string, bodyStmts []*ast.Node, def *saClassDef, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	for _, f := range def.fields {
		// 逐份独立域（名隔离；份尾释本份归属（含键绑），防静态重定义；
		// 体终结则后续份不可达，截断防死码）。
		savedF := saScopeEnter(scope)
		copyDepth := len(scope.ownOrder)
		kh := saLowerStringLiteral(w, f.name, scope, nextTemp)
		w.Write(fmt.Sprintf("  %s = %s\n", binding, kh))
		scope.types[binding] = "str"
		// for-in 键每份新鲜具化，直授 move 安全（for-of 串元素同形；540 律；541）。
		if scope.forOfStr == nil {
			scope.forOfStr = map[string]bool{}
		}
		scope.forOfStr[binding] = true
		saConsumeOwn(scope, kh)
		saDeclareOwned(scope, binding)
		armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if armOK {
			saReleaseDeeperThan(w, scope, copyDepth)
		}
		saScopeExit(scope, savedF)
		if !armOK {
			return false
		}
		if saArmTerminates(bodyStmts) {
			break
		}
	}
	return true
}

// saForInCallBase 取 for-in 调用基址（括号透明；成员调用/箭头沿旧门）。
func saForInCallBase(e *ast.Node) *ast.CallExpression {
	e = saUnwrapTransparent(e)
	if e != nil && e.Kind == ast.KindCallExpression {
		return e.AsCallExpression()
	}
	return nil
}

// saForInThisField 取 for-in this 字段基址名（`this.o`；`?.`/调用/元素基沿旧门）。
func saForInThisField(e *ast.Node) string {
	if e == nil || e.Kind != ast.KindPropertyAccessExpression {
		return ""
	}
	pa := e.AsPropertyAccessExpression()
	if pa == nil || pa.QuestionDotToken != nil || pa.Name() == nil ||
		pa.Name().Kind != ast.KindIdentifier {
		return ""
	}
	if pa.Expression == nil || pa.Expression.Kind != ast.KindThisKeyword {
		return ""
	}
	return pa.Name().Text()
}
func saLowerForIn(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	fo := s.AsForInOrOfStatement()
	if fo.AwaitModifier != nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "for-await not lowerable"})
		return false
	}
	binding, pat, ok := saForBindingName(fo.Initializer)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-in initializer (single identifier declaration only)"})
		return false
	}
	if pat != nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "patterns in for-in need static element layouts"})
		return false
	}
	// 下标/键绑定与循环体同域（封存 lowerForIn 的 body pushScope 与 for-of 同形）。
	saved := saScopeEnter(scope)
	defer saScopeExit(scope, saved)
	if _, dup := scope.types[binding]; dup {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + binding})
		return false
	}
	// 对象 for-in 静态展开（布局字段逐个绑键名串常量 inline 体；`o[k]`
	// 动态读沿旧门；break/continue 沿无循环栈门拒；空布局零次展开）。
	// 调用基址（`for (k in getObj())`）：返回布局已知的调用先求值入临时
	// 柄，再与绑定标识形态共用字段展开（调用恰求值一次，与上游同语义；
	// 非 inst 返回/未知布局/求值失败皆大声拒，不下探数组门）。
	if ce := saForInCallBase(fo.Expression); ce != nil {
		if rk, ok := saCallRetKind(ce, scope); ok && len(rk) > 5 && rk[:5] == "inst:" {
			def, ok := scope.classes[rk[5:]]
			if !ok {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + rk[5:]})
				return false
			}
			op, voidCall, msg := saEvalCall(w, ce, scope, pos, refusals, nextTemp)
			if msg != "" || voidCall {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported for-in call base: %s", msg)})
				return false
			}
			fh := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = %s\n", fh, op))
			scope.types[fh] = rk
			if saIsTempOp(op) {
				saConsumeOwn(scope, op)
			}
			fhDepth := len(scope.ownOrder)
			saDeclareOwned(scope, fh)
			bodyStmts, ok := saEmbeddedBlock(fo.Statement)
			if !ok {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-in body"})
				return false
			}
			if !saLowerForInInstFields(w, binding, bodyStmts, def, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
				return false
			}
			// 调用柄巡后释放（展开只读布局，柄未消费；份内释放以份深为准，
			// 此柄在份外，按声明深统一收）。
			saReleaseDeeperThan(w, scope, fhDepth)
			return true
		}
	}
	if ub := saKeyofUnwrap(fo.Expression, scope); ub != nil && ub.Kind == ast.KindIdentifier {
		if k, ok := scope.types[ub.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
			def, ok := scope.classes[k[5:]]
			if !ok {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + k[5:]})
				return false
			}
			bodyStmts, ok := saEmbeddedBlock(fo.Statement)
			if !ok {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-in body"})
				return false
			}
			return saLowerForInInstFields(w, binding, bodyStmts, def, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		}
	}
	// this 字段基址（`for (k in this.o)`）：属主布局字段具 inst 子布局即静态
	// 展开（与标识形态共用；数组/标量/方法字段沿旧门）。
	if fname := saForInThisField(fo.Expression); fname != "" {
		if scope.thisSelf == "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "this outside a class method is not lowerable"})
			return false
		}
		if ownerDef, ok := scope.classes[scope.thisClass]; ok && ownerDef != nil {
			if sub, ok := ownerDef.fsub[fname]; ok {
				if def, ok := scope.classes[sub]; ok && def != nil {
					bodyStmts, ok := saEmbeddedBlock(fo.Statement)
					if !ok {
						ln, col := pos(s.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-in body"})
						return false
					}
					return saLowerForInInstFields(w, binding, bodyStmts, def, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
				}
			}
		}
	}
	// 串 for-in（索引巡回 0..len-1，绑定 i32；16 字节 {ptr,len} 头与数组同形，
	// 下同数组骨架；串判定先行，免数组门误记拒因；上游 `k = add idx, 0` 同形）。
	// 求柄各臂自带归属，巡后块统一释放（具名直传 no-op）。
	var arrVal string
	if saIsStrExpr(fo.Expression, scope) {
		h, msg := saEvalStr(w, fo.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported for-in base: %s", msg)})
			return false
		}
		arrVal = h
	} else {
		var ok bool
		arrVal, ok = saForArrHandle(w, fo.Expression, scope, pos, refusals, nextTemp, s, "for-in")
		if !ok {
			return false
		}
	}
	bodyStmts, ok := saEmbeddedBlock(fo.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-in body"})
		return false
	}
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", idx))
	topL := fmt.Sprintf("L_forin_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_forin_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_forin_end_%d", *nextLabel)
	*nextLabel++
	lenT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lenT, arrVal))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL, depth: len(scope.ownOrder)})
	saBindPendingLabels(scope, false)
	w.Write(fmt.Sprintf("%s:\n", topL))
	cT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cT, idx, lenT))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	// 下标绑定快照（`binding = add idx, 0` + declarePlain；move 会消费循环携带的
	// idx 致增量位 UseAfterMove；形状证据：封存 for-in 体 `i = add t_6, 0`）。
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", binding, idx))
	scope.types[binding] = "i32"
	saDeclarePlain(scope, binding)
	// 体临时量每轮尾释放（下标绑定为快照具名不动；体临时量全释；同上）。
	savedArmTemps := scope.armReleaseTemps
	scope.armReleaseTemps = true
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.armReleaseTemps = savedArmTemps
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if !saArmTerminates(bodyStmts) {
		incT := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", incT, idx))
		w.Write(fmt.Sprintf("  %s = %s\n", idx, incT))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	// 字面量接收 temp 巡后释放(绑定名/链 temp 为 no-op；上游同形).
	// 巡后释本域归属(具名+临时,逆序;已消费/已释放/非堆跳过；上游 releaseScope 同形).
	// 外层绑定早于本域基址,不在此列；体臂条目已随臂退出截断.
	{
		done := map[string]bool{}
		for i := len(scope.ownOrder) - 1; i >= saved.owned; i-- {
			name := scope.ownOrder[i]
			if done[name] {
				continue
			}
			done[name] = true
			if b := saOwnOf(scope, name); saIsTempOp(name) && b != nil && b.heap && !b.consumed && !b.released {
				w.Write(fmt.Sprintf("  !%s\n", name))
				b.released = true
			}
		}
	}
	return true
}

// saLowerDoWhile lowering do-while（形状证据：封存 lowerDoWhile:2232-2263：
// 体跑一次 + `jmp cond`（体终结则省）+ 条件 `jmp loop`/`br` + end；
// continue 落条件（非顶），break 落 end）。
// ---- 数组方法（arr 种上的成员调用；i32 槽模型，esz 固定 4）----
// 形状证据总纲：封存 lowerArrayMethod:6137-6397 + lowerArrayPush:5999-6054
// （扩容拷贝）+ lowerInsertionSort:6057-6123 + lowerArrayScan:6444-6513 +
// lowerArrayReverse:6516-6556 + lowerArraySlice:6559-6638 +
// lowerArrayAt:6643-6654 + lowerArrayJoin:6658-6712 +
// lowerCopyWithin:6717-6798 + lowerToReversed:6820-6879 +
// lowerArrayWith:6883-6919 + lowerToSpliced:6923-6975 + spliceCopy:6978-7023 +
// copyRange:7026-7057 + lowerArrayConcat:7061-7090 + lowerArrayFrom:7094-7153 +
// newEmptyArray:7802-7817 + appendSlice:7820-7847 + arrayClampLen:6401-6440 +
// 高阶 lowerHigherOrder:4875-5188 + callbackValue:5194-5250 +
// lowerSortWithCmp:5539-5622。
// 本薄口数组恒为 i32 元（elem/es乙固定）；高阶回调恒为 i32 位
// （串回调值大声拒）；具名函数回调须内联书写（箭头别名无值，见 step17 门）。

// saIsArrMethod 数组方法名集合（含高阶；未知成员另行大声拒）。
func saIsArrMethod(m string) bool {
	switch m {
	case "push", "pop", "shift", "unshift", "fill", "sort", "indexOf", "lastIndexOf",
		"includes", "reverse", "slice", "at", "join", "copyWithin", "toReversed",
		"toSorted", "with", "toSpliced", "splice", "flat", "flatMap", "concat",
		"forEach", "map", "filter", "find", "findIndex", "findLast", "findLastIndex",
		"some", "every", "reduce", "reduceRight", "entries", "keys", "values":
		return true
	}
	return false
}

// saIsHigherOrderMethod 回调形数组方法（封存 isHigherOrderMethod:4863-4870）。
func saIsHigherOrderMethod(m string) bool {
	switch m {
	case "forEach", "map", "filter", "find", "findIndex", "findLast", "findLastIndex",
		"some", "every", "reduce", "reduceRight", "sort", "toSorted", "flatMap":
		return true
	}
	return false
}

// saIsChainArrValue 纯查表判定属性链是否为数组位（`c.a`/`q.r.a`；
// 不落字；叶子须 arr，内节须 inst；实例/链基皆可）。
func saIsChainArrValue(e *ast.Node, scope *saScope) bool {
	pa := e.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return false
	}
	var hdef *saClassDef
	switch {
	case pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword:
		if scope.thisSelf == "" {
			return false
		}
		d, ok := scope.classes[scope.thisClass]
		if !ok {
			return false
		}
		hdef = d
	case pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier:
		k, ok := scope.types[pa.Expression.Text()]
		if !ok || len(k) <= 5 || k[:5] != "inst:" {
			return false
		}
		d, ok := scope.classes[k[5:]]
		if !ok {
			return false
		}
		hdef = d
	case pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression:
		inner := pa.Expression.AsPropertyAccessExpression()
		if inner.Name() == nil {
			return false
		}
		sub, ok := saChainSubDef(inner, scope)
		if !ok {
			return false
		}
		hdef = sub
	default:
		return false
	}
	off, ok := hdef.offsets[pa.Name().Text()]
	if !ok || hdef.fkinds[pa.Name().Text()] != "arr" {
		_ = off
		return false
	}
	return true
}

// saChainSubDef 纯查表解属性链内节布局（`q.r`→R；不落字）。
func saChainSubDef(pa *ast.PropertyAccessExpression, scope *saScope) (*saClassDef, bool) {
	if pa.Name() == nil {
		return nil, false
	}
	var hdef *saClassDef
	switch {
	case pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword:
		if scope.thisSelf == "" {
			return nil, false
		}
		d, ok := scope.classes[scope.thisClass]
		if !ok {
			return nil, false
		}
		hdef = d
	case pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier:
		k, ok := scope.types[pa.Expression.Text()]
		if !ok || len(k) <= 5 || k[:5] != "inst:" {
			return nil, false
		}
		d, ok := scope.classes[k[5:]]
		if !ok {
			return nil, false
		}
		hdef = d
	case pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression:
		inner := pa.Expression.AsPropertyAccessExpression()
		sub, ok := saChainSubDef(inner, scope)
		if !ok {
			return nil, false
		}
		hdef = sub
	default:
		return nil, false
	}
	fname := pa.Name().Text()
	if _, ok := hdef.offsets[fname]; !ok || hdef.fkinds[fname] != "inst" {
		return nil, false
	}
	sub, ok := scope.classes[hdef.fsub[fname]]
	if !ok {
		return nil, false
	}
	return sub, true
}

// saIsArrValue 语法级判定表达式是否为数组位（不落字）。
func saIsArrValue(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindArrayLiteralExpression:
		return true
	case ast.KindNewExpression:
		// 新鲜数组构造即数组值（`new Array(n)` 链式接收者/下标基；1288）。
		return saIsArrayCtor(e)
	case ast.KindIdentifier:
		k, ok := scope.types[e.Text()]
		return ok && k == "arr"
	case ast.KindCallExpression:
		k, ok := saArrCallRet(e.AsCallExpression(), scope)
		if ok && k == "arr" {
			return true
		}
		// 同文件/链接数组函数返回（求值侧 `saArrValueOf` 同源分支既有；
		// 门与求值不对称致 expect 调用位误拒，此处补齐，零新语义）。
		if k, ok := saCallRetKind(e.AsCallExpression(), scope); ok && (k == "arr" || k == "arrStr") {
			return true
		}
		return false
	case ast.KindParenthesizedExpression:
		return saIsArrValue(e.AsParenthesizedExpression().Expression, scope)
	case ast.KindAsExpression:
		return saIsArrValue(e.AsAsExpression().Expression, scope)
	case ast.KindSatisfiesExpression:
		return saIsArrValue(e.AsSatisfiesExpression().Expression, scope)
	case ast.KindNonNullExpression:
		return saIsArrValue(e.AsNonNullExpression().Expression, scope)
	case ast.KindTypeAssertionExpression:
		return saIsArrValue(e.AsTypeAssertion().Expression, scope)
	case ast.KindElementAccessExpression:
		// 嵌套字面量直供基（`[[1,2]][0]` 内层为句柄；变量基元素种未知，沿旧门）。
		ea := e.AsElementAccessExpression()
		if ea.Expression != nil && ea.Expression.Kind == ast.KindArrayLiteralExpression {
			if al := ea.Expression.AsArrayLiteralExpression(); al.Elements != nil {
				for _, el := range al.Elements.Nodes {
					if el.Kind == ast.KindArrayLiteralExpression {
						return true
					}
				}
			}
		}
		// 嵌套变量基（`arrNest` 标记内层即句柄；与 for-of 行绑定/派生透传同源）。
		if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
			if scope.arrNest != nil && scope.arrNest[ea.Expression.Text()] {
				return true
			}
		}
		return false
	case ast.KindPropertyAccessExpression:
		// 实例/链 arr 字段（`c.a`/`q.r.a` 纯查表；私名/静态沿旧门）。
		return saIsChainArrValue(e, scope)
	default:
		return false
	}
}

// saArrCallRet 数组调用的返回种（"i32"/"arr"/"str"；非数组调用即失败）。
// 高阶回调形恒走内联（具名函数回调须内联书写）。
func saArrCallRet(ce *ast.CallExpression, scope *saScope) (string, bool) {
	if ce.Expression == nil {
		return "", false
	}
	if ce.Expression.Kind == ast.KindIdentifier {
		return "", false
	}
	if ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return "", false
	}
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" {
		if pa.Name().Text() == "from" {
			return "arr", true
		}
		return "", false
	}
	m := pa.Name().Text()
	if m == "split" && saIsStrExpr(pa.Expression, scope) {
		// 串 `split` 回串元数组（`saLowerStringSplit`；串基纯语法判定零落字）。
		return "arr", true
	}
	// `Map.get` 数组值回 arr 句柄（`saMapCallKind` 建表记种；串 split 同形）。
	if k, ok := saMapCallKind(ce, scope); ok && k == "arr" {
		return "arr", true
	}
	if m == "exec" {
		// 正则 `.exec` 回整体单元素串数组（`saLowerRegexMatchArray` 共享）。
		if k, ok := saRegexCallKind(ce, scope); ok && k == "arr" {
			return "arr", true
		}
	}
	if m == "match" && saIsStrExpr(pa.Expression, scope) {
		// 串 `match`（无 g）回串元数组/miss 即 null（`saLowerStrMatch`；种记 arr）。
		return "arr", true
	}
	// Map.keys/values/entries 回 arr 句柄（移植封存 lowerMapMethod:4785-4798；
	// 种判定 saMapCallKind:67-68 同 "arr"，数组位/for-of 凭此直传句柄）。
	if k, ok := saMapBaseKind(pa.Expression, scope); ok && k == "map" {
		switch m {
		case "keys", "values", "entries":
			return "arr", true
		}
	}
	// `Object.values(o)` 回 i32 数组柄、`Object.keys(o)` 回串元数组柄
	// （布局门在求值侧；种判定零落字；577）。
	if (m == "values" || m == "keys" || m == "entries") && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Object" {
		return "arr", true
	}
	if !saIsArrMethod(m) {
		return "", false
	}
	// 顶层 const 数组纯读方法调用种（快照接收器；变异/未知方法沿旧门；708）。
	// jsval receiver (unknown array, e.g. values.flatMap(...).map(...)):
	// saEvalCall already dispatches array methods on jsval identifiers
	// (unbox_at_call), but saArrCallRet refused them here, so a chained
	// .map/.filter on the flatMap result was not recognised as an array
	// and fell through to "only direct function calls lowerable".
	isJsValRecv := pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && scope.types[pa.Expression.Text()] == "jsval"
	// 顶层对象数组域接收器（`GROUPS.typecheck.find`：域柄经
	// saArrValueOf 顶层对象径，元布局经 saFindReceiverLayout 同径）。
	isTopObjArrRecv := false
	if _, _, _, ok := saTopObjArrField(scope, pa.Expression); ok {
		isTopObjArrRecv = true
	}
	if !saIsArrValue(pa.Expression, scope) && !saTopArrPureCall(pa, scope, m) && !isJsValRecv && !isTopObjArrRecv {
		return "", false
	}
	if (m == "find" || m == "findLast") && ce.Arguments != nil && len(ce.Arguments.Nodes) >= 1 {
		if _, _, _, _, ok := saFindEqGuard(ce.Arguments.Nodes[0]); ok {
			if layout, ok := saFindReceiverLayout(pa.Expression, scope); ok {
				return "inst:" + layout, true
			}
		}
		if _, typeofName, _, ok := saFindTypeofGuard(ce.Arguments.Nodes[0]); ok && typeofName == "string" {
			return "str", true
		}
	}
	if (m == "findIndex" || m == "findLastIndex") && ce.Arguments != nil && len(ce.Arguments.Nodes) >= 1 {
		if _, _, _, _, ok := saFindEqGuard(ce.Arguments.Nodes[0]); ok {
			if _, ok := saFindReceiverLayout(pa.Expression, scope); ok {
				return "i32", true
			}
		}
	}
	switch m {
	case "push", "pop", "shift", "unshift", "indexOf", "lastIndexOf", "includes", "at",
		"forEach", "find", "findIndex", "findLast", "findLastIndex", "some", "every",
		"reduce", "reduceRight":
		return "i32", true
	case "join":
		return "str", true
	default:
		return "arr", true
	}
}

// saIsArrJoinCall 判定是否为数组 join 调用（串位）。
func saIsArrJoinCall(ce *ast.CallExpression, scope *saScope) bool {
	k, ok := saArrCallRet(ce, scope)
	return ok && k == "str"
}

// saArrValueOf 求数组句柄（绑定直传；字面量构造；数组返回调用；括号类直通）。
// 链式下标基（`pairs[0]`）递归读回内层句柄值（与上游 lowerExpr 递归同形）。
func saArrValueOf(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil {
		return "", "missing expression"
	}
	if base, ok := saArrBase(scope, e); ok {
		return base, ""
	}
	switch e.Kind {
	case ast.KindArrayLiteralExpression:
		return saLowerArrayLiteral(w, e, scope, pos, refusals, nextTemp)
	case ast.KindPropertyAccessExpression:
		// 实例 arr 字段基（`this.a`/`c.a` 读句柄；私名/静态/存取器沿既有门）。
		pa := e.AsPropertyAccessExpression()
		if pa.Name() != nil && saCouldBeInst(pa.Expression, scope) {
			// const 空实例数组域读必崩（`q.a` 读零址；t29f SIGSEGV 实证；`?.` 沿既有门；493）。
			if pa.QuestionDotToken == nil && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && saIsNullConst(scope, pa.Expression.Text()) {
				return "", "const null instance member is not lowerable (definite null dereference)"
			}
			h, def, msg := saInstBase(pa.Expression, scope)
			// map 索引实例基（`m[k].a`；saInstBase 只认标识符/this；
			// 与 i32 读位 2450-2459 同形；否则 nil 解引用崩溃）。
			if msg == "" && def == nil && pa.Expression != nil && (pa.Expression.Kind == ast.KindElementAccessExpression || pa.Expression.Kind == ast.KindCallExpression || pa.Expression.Kind == ast.KindNewExpression) {
				h, def, msg = saInstBaseElem(w, pa.Expression, scope, pos, refusals, nextTemp)
			}
			if msg == "" {
				if def == nil {
					return "", "instance base did not resolve to a recorded layout"
				}
				fname := pa.Name().Text()
				if off, ok := def.offsets[fname]; ok && def.fkinds[fname] == "arr" {
					t := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
					return t, ""
				}
			}
		}
		// 顶层对象数组域基（`GROUPS.typecheck`：外层物化后读 arr 域；
		// 元布局记入 arrElemLayout 供下标元读消解；外层柄归属沿
		// 域外释放口径，域柄为借用读）。
		if pa.Name() != nil && pa.QuestionDotToken == nil {
			if to, field, layout, ok := saTopObjArrField(scope, e); ok {
				def, ok2 := scope.classes[to.layout]
				if !ok2 || def == nil {
					return "", "unknown top object layout " + to.layout
				}
				h, msg := saMaterializeTopObjArrFields(w, to, scope, nextTemp)
				if msg != "" {
					return "", msg
				}
				t, msg := saLowerClassFieldLoad(w, h, def, field, scope, nextTemp)
				if msg != "" {
					return "", msg
				}
				if scope.arrElemLayout == nil {
					scope.arrElemLayout = map[string]string{}
				}
				scope.arrElemLayout[t] = layout
				return t, ""
			}
		}
		// 嵌套链 arr 基（`q.r.a` 经内层句柄；叶子须 arr；与链读同形）。
		if pa.Name() != nil && pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression {
			if ch, cdef, msg := saChainBase(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
				if off, ok := cdef.offsets[pa.Name().Text()]; ok && cdef.fkinds[pa.Name().Text()] == "arr" {
					t := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, ch, off))
					return t, ""
				}
			}
		}
		return "", "not an array expression"
	case ast.KindElementAccessExpression:
		ea := e.AsElementAccessExpression()
		if ea.QuestionDotToken != nil {
			return "", "optional index access not lowerable"
		}
		inner, msg := saArrValueOf(w, ea.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if msg := saCheckIntIndex(scope, idx); msg != "" {
			return "", msg
		}
		return saLowerCheckedIndex(w, inner, idx, scope.nextLabel, nextTemp), ""
	case ast.KindCallExpression:
		ce := e.AsCallExpression()
		if k, ok := saArrCallRet(ce, scope); ok && k == "arr" {
			op, voidCall, msg := saEvalCall(w, ce, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if voidCall {
				return "", "void call in array position"
			}
			return op, ""
		}
		// 同文件数组函数返回句柄直传（`const a = get()`；签名预扫记 arr/arrStr）。
		if k, ok := saCallRetKind(ce, scope); ok && (k == "arr" || k == "arrStr") {
			op, voidCall, msg := saEvalCall(w, ce, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if voidCall {
				return "", "void call in array position"
			}
			return op, ""
		}
		if ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "alloc" {
			// alloc 原语即新鲜句柄（`const b: Box<i32> = alloc(4)` 经声明 arr 位；
			// 与求值核共用 helper；用户遮蔽沿旧门；形状证据：封存 lowerCall:3861-3868）。
			if _, shadowed := scope.funcs["alloc"]; !shadowed {
				op, _, msg := saLowerAllocCall(w, ce, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				return op, ""
			}
		}
		// 调用式数组构造具化（`Array(n)`/`Array.of(..)` 下标基；归属登记、
		// 消费位即释，与 New 式同律；1298）。
		if saIsArrayCtor(e) {
			h, msg := saLowerArrayCtor(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			saOwnTemp(scope, h)
			return h, ""
		}
		return "", "not an array expression"
	case ast.KindParenthesizedExpression:
		return saArrValueOf(w, e.AsParenthesizedExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindAsExpression:
		return saArrValueOf(w, e.AsAsExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindSatisfiesExpression:
		return saArrValueOf(w, e.AsSatisfiesExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindNonNullExpression:
		return saArrValueOf(w, e.AsNonNullExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindTypeAssertionExpression:
		return saArrValueOf(w, e.AsTypeAssertion().Expression, scope, pos, refusals, nextTemp)
	case ast.KindConditionalExpression:
		// 数组三元（双臂句柄经槽选柄；与串三元槽汇合 `L_tern_*` 同形；
		// 条件核与 `saLowerTernaryValue` 同源；上游同形实证 check-clean）。
		tce := e.AsConditionalExpression()
		if tce == nil {
			return "", "not an array expression"
		}
		// 臂种门：未知臂大声拒；串元臂可降当双臂同串（句柄皆 ptr，槽选柄
		// 与 i32 元同形，结果记 arrStr；混串/非串臂仍大声拒，元种歧义）。
		tstr, tok := saTernaryArrArm(tce.WhenTrue, scope)
		fstr, fok := saTernaryArrArm(tce.WhenFalse, scope)
		if !tok || !fok {
			return "", "array ternary arm shape is not lowerable"
		}
		if tstr != fstr && !saIsEmptyArrayLiteral(tce.WhenTrue) && !saIsEmptyArrayLiteral(tce.WhenFalse) {
			return "", "string-element array ternary arms must agree"
		}
		condOp, msg := saCondOperandMat(w, tce.Condition, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		tv, msg := saArrValueOf(w, tce.WhenTrue, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		fv, msg := saArrValueOf(w, tce.WhenFalse, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		slot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		tL := fmt.Sprintf("L_tern_t_%d", *scope.nextLabel)
		*scope.nextLabel++
		fL := fmt.Sprintf("L_tern_f_%d", *scope.nextLabel)
		*scope.nextLabel++
		endL := fmt.Sprintf("L_tern_end_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, tL, fL))
		w.Write(fmt.Sprintf("%s:\n", tL))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, tv))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", fL))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, fv))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		res := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", res, slot))
		w.Write(fmt.Sprintf("  !%s\n", slot))
		scope.types[res] = "arr"
		if tstr || fstr {
			saMarkArrStr(scope, res)
		}
		return res, ""
	case ast.KindNewExpression:
		// 新鲜数组构造具化（链式接收者/下标基；归属登记、消费位即释，与字面量同律；1288）。
		if saIsArrayCtor(e) {
			h, msg := saLowerArrayCtor(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			saOwnTemp(scope, h)
			return h, ""
		}
		return "", "not an array expression"
	default:
		return "", "not an array expression"
	}
}

// saNewSizedArray 零缓冲定长数组（形状证据：封存 newSizedArray:7710-7730）。
func saNewSizedArray(w printer.EmitTextWriter, lenOp string, nextTemp *int) string {
	bytes := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", bytes, lenOp))
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	buf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  %s = alloc %s\n", buf, bytes))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", h, buf))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", h, lenOp))
	w.Write(fmt.Sprintf("  !%s\n", buf))
	return h
}

// saIsArrayCtor 识别数组构造式（`Array(...)` 调用与 `new Array(n)` 单长形；
// 后者多参落通用拒绝，镜像封存 lowerNew:8603）。
func saIsArrayCtor(e *ast.Node) bool {
	if e == nil {
		return false
	}
	if e.Kind == ast.KindCallExpression {
		ce := e.AsCallExpression()
		if ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "Array" {
			return true
		}
		// `Array.of(...)` 元素式（单数值参亦为元）。
		if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
			pa := ce.Expression.AsPropertyAccessExpression()
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" &&
				pa.Name() != nil && pa.Name().Text() == "of" && pa.QuestionDotToken == nil {
				return true
			}
		}
		return false
	}
	if e.Kind == ast.KindNewExpression {
		ne := e.AsNewExpression()
		return ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "Array"
	}
	return false
}

// saCtorElemCount 元素式构造元数（多元/`of` 即参量；调用方保证非空穴；
// 单长空穴沿 litLen=-1 未知长门；1308）。
func saCtorElemCount(e *ast.Node) int {
	if e == nil {
		return -1
	}
	switch e.Kind {
	case ast.KindCallExpression:
		if ce := e.AsCallExpression(); ce != nil && ce.Arguments != nil {
			return len(ce.Arguments.Nodes)
		}
	case ast.KindNewExpression:
		if ne := e.AsNewExpression(); ne != nil && ne.Arguments != nil {
			return len(ne.Arguments.Nodes)
		}
	}
	return -1
}

// saLowerArrayCtor 数组构造式具化（单长分配；多元逐元 push；i32  plain 值；
// 形状证据：封存 lowerCall Array:3871-3884 + newSizedArray）。
func saLowerArrayCtor(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	var argNodes []*ast.Node
	if e.Kind == ast.KindCallExpression {
		if ce := e.AsCallExpression(); ce.Arguments != nil {
			argNodes = ce.Arguments.Nodes
		}
	} else {
		if ne := e.AsNewExpression(); ne.Arguments != nil {
			argNodes = ne.Arguments.Nodes
		}
	}
	// `new Array(n)` 单参即长；零参/多参即元素式（与 `Array.of` 同，
	// 与 `Array(...)` 多元臂共享；spread 元沿既有门拒）。
	// `Array.of` 元素式：单参亦为元（与 `Array(n)` 为长相别；零参即空数组）。
	of := false
	if e.Kind == ast.KindCallExpression {
		if ce := e.AsCallExpression(); ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
			pa := ce.Expression.AsPropertyAccessExpression()
			of = pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" &&
				pa.Name() != nil && pa.Name().Text() == "of"
		}
	}
	if len(argNodes) == 1 && !of {
		// 单参恒为长（`Array(5)` 即长 5；元素式请用字面量；
		// 形状证据：封存调用式 newSizedArray 分支 + lowerNew:8603）。
		v, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// 长度记种检查（串/实例句柄禁作分配长；指针×4 即爆内存；铁律 4）。
		if msg := saCheckI32Value(scope, v); msg != "" {
			return "", msg
		}
		return saNewSizedArray(w, v, nextTemp), ""
	}
	h := saNewEmptyArray(w, nextTemp)
	for _, a := range argNodes {
		if a != nil && a.Kind == ast.KindSpreadElement {
			return "", "Array(...) elements must be plain values"
		}
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			return "", "Array(...) elements must be plain values"
		}
		v, msg := saArrayLiteralElem(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		saLowerArrayPush(w, h, v, scope, nextTemp)
	}
	return h, ""
}

// saNewEmptyArray 空数组（alloc 16 头 + 空柄；形状证据：封存 newEmptyArray:7802-7817）。
func saNewEmptyArray(w printer.EmitTextWriter, nextTemp *int) string {
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	hm := saMutBorrow(w, nextTemp, h)
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", hm))
	w.Write(fmt.Sprintf("  store %s + 8, 0 as u64\n", hm))
	w.Write(fmt.Sprintf("  !%s\n", hm))
	return h
}

// saAppendSlice 整片拷贝入目标（`sci/sa_std/ts_array.sa` `@ts_arr_append_slice`
// 实现，逐元 push；R3-5 回迁；形状证据：封存 appendSlice:7820-7847）。
func saAppendSlice(w printer.EmitTextWriter, dst, src string, scope *saScope, nextTemp *int) {
	scope.addImport("sa_std/ts_array.sa")
	w.Write(fmt.Sprintf("  call @ts_arr_append_slice(%s, %s)\n", dst, src))
}

// saCopyRange 拷贝 src[s0,s1) 到 dst[d0,.]（`sci/sa_std/ts_array.sa`
// `@ts_arr_copy_range` 实现，调用方保证目标已分配；R3-6 回迁；形状证据：
// 封存 copyRange:7026-7057）。
func saCopyRange(w printer.EmitTextWriter, sdata, ddata, s0, s1, d0 string, scope *saScope, nextTemp *int) {
	scope.addImport("sa_std/ts_array.sa")
	w.Write(fmt.Sprintf("  call @ts_arr_copy_range(%s, %s, %s, %s, %s)\n", sdata, ddata, s0, s1, d0))
}

// saLowerArrayPush 扩容拷贝压栈（返回新长；形状证据：封存 lowerArrayPush:5999-6054）。
// saLowerDeepClone lowers `structuredClone(v)` (element-wise deep copy for array handles,
// recursing one level into nested slices; scalars snapshot; cf lowerDeepClone).
// saLowerDeepClone lowers `structuredClone(v)` array copying (flat or nested by mark).
func saLowerDeepClone(w printer.EmitTextWriter, src string, nested bool, scope *saScope, nextTemp *int) string {
	if nested {
		return saLowerDeepCloneInner(w, src, "deep", true, scope, nextTemp)
	}
	return saLowerDeepCloneInner(w, src, "flat", true, scope, nextTemp)
}

// saLowerDeepCloneInner ports lowerDeepCloneInner (kind snap/flat/deep; takeOwn marks only
// the top result owned; loop-scoped inner headers move into the outer array).
func saLowerDeepCloneInner(w printer.EmitTextWriter, src, kind string, takeOwn bool, scope *saScope, nextTemp *int) string {
	if kind == "flat" {
		// R3-7 回迁映射：单层拷贝语义由 `sci/sa_std/ts_array.sa` `@ts_arr_clone_flat`
		// 实现；归属由外层统一登记（drain 去重同形）；deep/snap 形保留。
		scope.addImport("sa_std/ts_array.sa")
		dest := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_clone_flat(%s)\n", dest, src))
		return dest
	}
	if kind == "snap" {
		cp := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", cp, src))
		return cp
	}
	// R3-14 回迁映射：嵌套深拷贝整体由 `sci/sa_std/ts_array.sa`
	// `@ts_arr_clone_deep` 单调用实现（外层循环 + 内层 flat 一级，
	// 封存 lowerDeepCloneInner 两层形；更深嵌套拷内层片头，上游同形）。
	// 调用方旧内联循环逐元调运行时会致双重遍历（运行时再循环），
	// 首元叶整数即野指针解引用，真机 SIGSEGV 实锤，故整包下沉。
	scope.addImport("sa_std/ts_array.sa")
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_clone_deep(%s)\n", dest, src))
	if takeOwn {
		saOwnTemp(scope, dest)
	}
	if scope.arrNest == nil {
		scope.arrNest = map[string]bool{}
	}
	scope.arrNest[dest] = true
	return dest
}

func saLowerStructuredClone(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 1 {
		return "", false, "structuredClone takes 1 argument"
	}
	if h, msg := saArrValueOf(w, argNodes[0], scope, pos, refusals, nextTemp); msg == "" {
		nested := scope.arrNest != nil && scope.arrNest[h]
		dest := saLowerDeepClone(w, h, nested, scope, nextTemp)
		saOwnTemp(scope, dest)
		scope.types[dest] = "arr"
		if nested {
			if scope.arrNest == nil {
				scope.arrNest = map[string]bool{}
			}
			scope.arrNest[dest] = true
		}
		return dest, false, ""
	}
	v, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	return saLowerDeepCloneInner(w, v, "snap", false, scope, nextTemp), false, ""
}

func saLowerArrayPush(w printer.EmitTextWriter, arr, val string, scope *saScope, nextTemp *int) string {
	// R2-7 回迁映射：扩容拷贝压栈语义由 `sci/sa_std/ts_array.sa`
	// `@ts_arr_push_word` 实现（返同柄，归属移交；旧柄泄漏纪律原样保留），
	// 本侧只做 import + 调用 + 新长读回；形状证据：封存 lowerArrayPush:5999-6054。
	scope.addImport("sa_std/ts_array.sa")
	w.Write(fmt.Sprintf("  call @ts_arr_push_word(%s, %s)\n", arr, val))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", nlen, arr))
	return nlen
}

// saLowerPushSpread push 展开形（`a.push(...src)`；源长循环逐元原位压栈，
// 空源零次；返压后新长（直读 recv 头 +8）；源柄读后即释（具名 no-op）；
// 元种门：i32 绑定数组收串/嵌套源沿标量臂同律拒；自展（`a.push(...a)`）长预读
// 语义与 JS 先求值一致；1388）。
func saLowerPushSpread(w printer.EmitTextWriter, recv string, pa *ast.PropertyAccessExpression, se *ast.SpreadElement, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	src, msg := saArrValueOf(w, se.Expression.AsNode(), scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		if k, ok := scope.types[pa.Expression.Text()]; ok && k == "arr" {
			if scope.arrStr == nil || !scope.arrStr[pa.Expression.Text()] {
				if (scope.arrStr != nil && scope.arrStr[src]) || (scope.arrNest != nil && scope.arrNest[src]) {
					return "", "array element kind mismatch (non-string array takes i32 values)"
				}
			}
		}
	}
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", idx))
	topL := fmt.Sprintf("L_pushsp_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_pushsp_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_pushsp_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	lenT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lenT, src))
	w.Write(fmt.Sprintf("%s:\n", topL))
	cT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cT, idx, lenT))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	v := saLowerCheckedIndex(w, src, idx, scope.nextLabel, nextTemp)
	_ = saLowerArrayPush(w, recv, v, scope, nextTemp)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, idx))
	w.Write(fmt.Sprintf("  %s = %s\n", idx, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	saReleaseOwnedTemp(w, scope, src)
	last := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", last, recv))
	return last, ""
}

// saLowerUnshiftSpread unshift 展开形（`a.unshift(x, ...b, y)`；全参按序中转
// 新鲜暂存（标量逐元压栈、展开整片合并），再逆序单步前插（push + rotr1）；
// 暂存读后无条件释放（新鲜内柄，checked-index 槽同律）；返压后新长；
// 元种门：i32 绑定数组收串/嵌套源沿 push 同律拒；1398）。
func saLowerUnshiftSpread(w printer.EmitTextWriter, recv string, pa *ast.PropertyAccessExpression, args []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	i32Recv := false
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		if k, ok := scope.types[pa.Expression.Text()]; ok && k == "arr" {
			i32Recv = scope.arrStr == nil || !scope.arrStr[pa.Expression.Text()]
		}
	}
	tmp := saNewEmptyArray(w, nextTemp)
	for _, a := range args {
		if a != nil && a.Kind == ast.KindSpreadElement {
			src, msg := saArrValueOf(w, a.AsSpreadElement().Expression.AsNode(), scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if i32Recv && ((scope.arrStr != nil && scope.arrStr[src]) || (scope.arrNest != nil && scope.arrNest[src])) {
				return "", "array element kind mismatch (non-string array takes i32 values)"
			}
			saAppendSlice(w, tmp, src, scope, nextTemp)
			saReleaseOwnedTemp(w, scope, src)
			continue
		}
		v, msg := saArrayLiteralElem(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if i32Recv && (saIsStrValue(a, scope) || saCouldBeInst(a, scope)) {
			return "", "array element kind mismatch (non-string array takes i32 values)"
		}
		saLowerArrayPush(w, tmp, v, scope, nextTemp)
	}
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	lenT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lenT, tmp))
	w.Write(fmt.Sprintf("  %s = %s\n", idx, lenT))
	topL := fmt.Sprintf("L_unshsp_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_unshsp_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_unshsp_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	cT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sgt %s, 0\n", cT, idx))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", inext, idx))
	w.Write(fmt.Sprintf("  %s = %s\n", idx, inext))
	v := saLowerCheckedIndex(w, tmp, idx, scope.nextLabel, nextTemp)
	_ = saLowerArrayPush(w, recv, v, scope, nextTemp)
	scope.addImport("sa_std/ts_array.sa")
	w.Write(fmt.Sprintf("  call @ts_arr_rotr1(%s)\n", recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", data, v))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	w.Write(fmt.Sprintf("  !%s\n", tmp))
	last := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", last, recv))
	return last, ""
}

// saLowerArraySlice 拷贝 [start, end) 到新数组（钳位；空域单路径分配；
// 形状证据：封存 lowerArraySlice:6559-6638）。
func saLowerArraySlice(w printer.EmitTextWriter, recv, start, end string, scope *saScope, nextTemp *int) string {
	// R1 回迁映射：切片语义（负起/双端钳位/空段）由 `sci/sa_std/ts_array.sa`
	// `@ts_arr_slice_copy` 实现，本侧只做种门禁（调用点 saCheckIntIndex）+
	// import + 归属/标记透传；形状证据见 ts_array.sa 头注。
	scope.addImport("sa_std/ts_array.sa")
	f := end
	if f == "" {
		f = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as i32\n", f, recv))
	}
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_slice_copy(%s, %s, %s)\n", dest, recv, start, f))
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	saPropArrInnerStr(scope, recv, dest)
	saPropArrElemLayout(scope, recv, dest)
	// 切片柄归属(返前释放；上游同形).
	saOwnTemp(scope, dest)
	return dest
}

// saLowerArrayAt 负下标归一后走越界归零 join（形状证据：封存 lowerArrayAt:6643-6654）。
func saLowerArrayAt(w printer.EmitTextWriter, recv, idx string, scope *saScope, nextTemp *int) string {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	isneg := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, 0\n", isneg, idx))
	adj := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", adj, ln, isneg))
	sel := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", sel, idx, adj))
	out := saLowerCheckedIndex(w, recv, sel, scope.nextLabel, nextTemp)
	saReleaseOwnedTemp(w, scope, recv)
	return out
}

// saLowerToReversed 逆序拷贝到新数组（形状证据：封存 lowerToReversed:6820-6879）。
func saLowerToReversed(w printer.EmitTextWriter, recv string, scope *saScope, nextTemp *int) string {
	// R1 回迁映射：逆序语义由 `sci/sa_std/ts_array.sa` `@ts_arr_toreversed`
	// 实现，本侧只做 import + 归属/标记透传。
	scope.addImport("sa_std/ts_array.sa")
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_toreversed(%s)\n", dest, recv))
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	saPropArrInnerStr(scope, recv, dest)
	saOwnTemp(scope, dest)
	return dest
}

// saLowerToSorted 拷贝并数值插入排序返新数组（R1 回迁映射：语义由
// `sci/sa_std/ts_array.sa` `@ts_arr_tosorted` 实现，本侧只做 import +
// 归属/标记透传；比较器形沿既有内联不动）。
func saLowerToSorted(w printer.EmitTextWriter, recv string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/ts_array.sa")
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_tosorted(%s)\n", dest, recv))
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	saPropArrInnerStr(scope, recv, dest)
	saOwnTemp(scope, dest)
	return dest
}

// saLowerArrayWith 拷贝并定点替换（越界透传拷贝；形状证据：封存 lowerArrayWith:6883-6919）。
func saLowerArrayWith(w printer.EmitTextWriter, recv, idx, val string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	// R1 回迁映射：单点替换语义（负规范/越界原样）由 `sci/sa_std/ts_array.sa`
	// `@ts_arr_with` 实现，本侧只做 import + 归属/标记透传。
	_ = pos
	_ = refusals
	scope.addImport("sa_std/ts_array.sa")
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_with(%s, %s, %s)\n", dest, recv, idx, val))
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	saPropArrInnerStr(scope, recv, dest)
	saOwnTemp(scope, dest)
	return dest, ""
}

// saLowerToSpliced 新数组删段插项（R1 回迁映射：语义由
// `sci/sa_std/ts_array.sa` `@ts_arr_tospliced` 实现，本侧只做种门禁
// （start/del 求值 + 插入项 plain 门）+ items 临时数组 + import +
// 归属/标记透传；形状证据：封存 lowerToSpliced:6923-6975）。
func saLowerToSpliced(w printer.EmitTextWriter, recv string, args []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/ts_array.sa")
	start := "0"
	if len(args) > 0 {
		v, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		start = v
	}
	var delOp string
	if len(args) > 1 {
		v, msg := saEvalI32(w, args[1], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		delOp = v
	} else {
		delOp = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as i32\n", delOp, recv))
	}
	items := saNewEmptyArray(w, nextTemp)
	if len(args) > 2 {
		for _, it := range args[2:] {
			if it == nil {
				return "", "toSpliced items must be plain values"
			}
			if it.Kind == ast.KindSpreadElement {
				return "", "toSpliced items must be plain values"
			}
			if it.Kind == ast.KindArrowFunction || it.Kind == ast.KindFunctionExpression {
				return "", "toSpliced items must be plain values"
			}
			v, msg := saEvalI32(w, it, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			saLowerArrayPush(w, items, v, scope, nextTemp)
		}
	}
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_tospliced(%s, %s, %s, %s)\n", dest, recv, start, delOp, items))
	w.Write(fmt.Sprintf("  !%s\n", items))
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	saPropArrInnerStr(scope, recv, dest)
	saOwnTemp(scope, dest)
	return dest, ""
}

// recvIsStrArray 报告成员调用接收者是否为串元数组（绑定标记；字面量
// 接收另由调用方具化，此处只认绑定）。
func recvIsStrArray(scope *saScope, pa *ast.PropertyAccessExpression) bool {
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
		return false
	}
	if scope.arrStr != nil && scope.arrStr[pa.Expression.Text()] {
		return true
	}
	// 顶层串元数组快照（758）。
	if arr, ok := saTopArrLookup(scope, pa.Expression); ok && arr.str {
		return true
	}
	return false
}

// saLowerArrayScanStr 串元数组内容扫描门面（includes/indexOf 正向 +
// lastIndexOf 逆向；针求值 + 起位种门禁留本侧，扫描语义内聚
// `@ts_str_arr_scan`；includes 终值 1/0、index 系返位/-1 皆由符号 wantidx 位决定）。
func saLowerArrayScanStr(w printer.EmitTextWriter, recv, method string, argNodes []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int) (string, string, string) {
	nh, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", "", msg
	}
	reverse := method == "lastIndexOf"
	// R3 回迁映射：串元内容扫描语义由 `sci/sa_std/ts_string.sa`
	// `@ts_str_arr_scan` 实现（起位钳位/逆向/内容判等内聚符号内；记种门禁留调用点）。
	fromVal, hasfrom := "0", "0"
	if len(argNodes) > 1 {
		fv, msg := saEvalI32(w, argNodes[1], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		if msg := saCheckIntIndex(scope, fv); msg != "" {
			return "", "", msg
		}
		fromVal, hasfrom = fv, "1"
	}
	rev, wi := "0", "1"
	if reverse {
		rev = "1"
	}
	if method == "includes" {
		wi = "0"
	}
	scope.addImport("sa_std/ts_string.sa")
	np, nl := saExpandStr(w, nh, nextTemp)
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_str_arr_scan(%s, %s, %s, %s, %s, %s, %s)\n", dest, recv, np, nl, fromVal, hasfrom, rev, wi))
	saOwnTemp(scope, dest)
	saReleaseOwnedTemp(w, scope, nh)
	return dest, "i32", ""
}

// 嵌套路两遍：遍 1 外槽累内长，遍 2 新柄逐内拷片；元静态全 arr 才展
// （arrNest 标记），混合/未知大声拒；结果不再记嵌套（保守，另步传标记）。
func saLowerArrayFlat(w printer.EmitTextWriter, recv string, depth int, scope *saScope, nextTemp *int) string {
	// R3-8 回迁映射：一层拍扁执行语义由 `sci/sa_std/ts_array.sa` `@ts_arr_flat`
	//（+ `@ts_arr_mkcopy` 预分配）实现；isnest 由调用点折叠（depth==0/非嵌套
	// 即浅拷）；arrNest 标记透传与归属登记保留本侧。
	scope.addImport("sa_std/ts_array.sa")
	isnest := "0"
	if depth != 0 && scope.arrNest != nil && scope.arrNest[recv] {
		isnest = "1"
	}
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_flat(%s, %s)\n", dest, recv, isnest))
	if depth == 0 {
		saPropArrNest(scope, recv, dest)
	}
	saOwnTemp(scope, dest)
	return dest
}

// 钳位段与 saLowerToSpliced 同形，删除段 alloc 同形；无插入走原地前移，
// 有插入走新缓冲三段拷后换柄；插入项 plain i32（与 toSpliced 同门）；
// 形状证据：封存 lowerToSpliced:6923-6975）。
// saLowerArraySplice 原地删段返删除段（R1 回迁映射：语义由
// `sci/sa_std/ts_array.sa` `@ts_arr_splice` 实现，本侧只做种门禁
// （start/del 求值 + 插入项 plain 门）+ del 缺省物化 + items 临时数组 +
// import + 归属/标记透传；形状证据：封存 lowerSplice 全形）。
func saLowerArraySplice(w printer.EmitTextWriter, recv string, pa *ast.PropertyAccessExpression, args []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	// 空参即空删（`a.splice()` 删零元返空数组，原数组不动，JS 同义；
	// 标记透传与常径一致；1478）。
	if len(args) < 1 {
		h := saNewEmptyArray(w, nextTemp)
		saPropArrNest(scope, recv, h)
		saPropArrStr(scope, recv, h)
		saPropArrInnerStr(scope, recv, h)
		saOwnTemp(scope, h)
		return h, ""
	}
	scope.addImport("sa_std/ts_array.sa")
	start, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	var delOp string
	if len(args) > 1 {
		v, msg := saEvalI32(w, args[1], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		delOp = v
	} else {
		delOp = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as i32\n", delOp, recv))
	}
	items := saNewEmptyArray(w, nextTemp)
	if len(args) > 2 {
		for _, it := range args[2:] {
			if it == nil {
				return "", "splice items must be plain values"
			}
			// 展开项（`splice(1, 0, ...b)`；中转 items 保序，元种门沿 push 同律；
			// 非 i32 绑定数组标量亦不可插串，沿旧门；1478）。
			if it.Kind == ast.KindSpreadElement {
				if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
					return "", "splice spread into non-i32 array is not lowerable yet"
				}
				if k, ok := scope.types[pa.Expression.Text()]; !ok || k != "arr" ||
					(scope.arrStr != nil && scope.arrStr[pa.Expression.Text()]) {
					return "", "splice spread into non-i32 array is not lowerable yet"
				}
				if _, msg := saLowerPushSpread(w, items, pa, it.AsSpreadElement(), scope, pos, refusals, nextTemp); msg != "" {
					return "", msg
				}
				continue
			}
			if it.Kind == ast.KindArrowFunction || it.Kind == ast.KindFunctionExpression {
				return "", "splice items must be plain values"
			}
			v, msg := saEvalI32(w, it, scope, pos, refusals, nextTemp)
			if msg != "" {
				// 串数组插串值（`s.splice(1, 0, "b")`；i32 门先行保既有拒因，
				// 串值沿此臂（串基以标记判定）；1498）。
				if scope.arrStr == nil || !scope.arrStr[recv] {
					return "", msg
				}
				var smsg string
				v, smsg = saEvalStr(w, it, scope, pos, refusals, nextTemp)
				if smsg != "" {
					return "", msg
				}
			}
			saLowerArrayPush(w, items, v, scope, nextTemp)
		}
	}
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_arr_splice(%s, %s, %s, %s)\n", dest, recv, start, delOp, items))
	w.Write(fmt.Sprintf("  !%s\n", items))
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	saPropArrInnerStr(scope, recv, dest)
	saOwnTemp(scope, dest)
	return dest, ""
}

// saLowerArrayConcat 首尾相接（R1 回迁映射：双片合并由
// `sci/sa_std/ts_array.sa` `@ts_arr_concat2` 实现，本侧 fold 调度 +
// 种门禁/标量 push/标记透传；形状证据：封存 lowerArrayConcat:7061-7090）。
func saLowerArrayConcat(w printer.EmitTextWriter, recv string, args []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/ts_array.sa")
	fold := func(cur, src string) string {
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_concat2(%s, %s)\n", out, cur, src))
		w.Write(fmt.Sprintf("  !%s\n", cur))
		return out
	}
	h := saNewEmptyArray(w, nextTemp)
	h = fold(h, recv)
	saPropArrNest(scope, recv, h)
	// strOK tracks all-string concatenation (recv marked and every
	// array arg str-marked; scalar args are i32 and break it).
	strOK := scope.arrStr != nil && scope.arrStr[recv]
	for _, a := range args {
		if a != nil && a.Kind == ast.KindSpreadElement {
			se := a.AsSpreadElement()
			src, msg := saArrValueOf(w, se.Expression.AsNode(), scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			h = fold(h, src)
			saPropArrNest(scope, src, h)
			strOK = strOK && scope.arrStr[src]
			continue
		}
		if a != nil && saIsArrValue(a, scope) {
			src, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			h = fold(h, src)
			saPropArrNest(scope, src, h)
			strOK = strOK && scope.arrStr[src]
			continue
		}
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			return "", "callbacks are not concat values"
		}
		v, msg := saArrayLiteralElem(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		saLowerArrayPush(w, h, v, scope, nextTemp)
		strOK = strOK && saIsStrExpr(a, scope)
	}
	if strOK {
		saMarkArrStr(scope, h)
	}
	saOwnTemp(scope, h)
	return h, ""
}

// saLowerArrayFrom `Array.from`（`{length:n}` 零数组；切片克隆；mapper 内联 map。
// 形状证据：封存 lowerArrayFrom:7094-7153）。
func saLowerArrayFrom(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) < 1 {
		return "", "Array.from needs 1 argument"
	}
	var mapper *ast.Node
	for _, a := range argNodes[1:] {
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			mapper = a
			break
		}
		if a != nil && a.Kind == ast.KindIdentifier {
			return "", "pass the mapper inline at Array.from (named callbacks do not inline)"
		}
	}
	if len(argNodes) > 1 && mapper == nil {
		for _, a := range argNodes[1:] {
			if a == nil {
				continue
			}
			if a.Kind == ast.KindSpreadElement {
				return "", "Array.from mapper must be an inline arrow"
			}
		}
		return "", "Array.from mapper must be an inline arrow"
	}
	var base string
	if argNodes[0] != nil && argNodes[0].Kind == ast.KindObjectLiteralExpression {
		n := "0"
		for _, p := range argNodes[0].AsObjectLiteralExpression().Properties.Nodes {
			if p.Kind == ast.KindPropertyAssignment {
				pa := p.AsPropertyAssignment()
				if pa.Name() != nil && pa.Name().Kind == ast.KindIdentifier && pa.Name().Text() == "length" {
					v, msg := saEvalI32(w, pa.Initializer, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					n = v
				}
			}
		}
		h := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		// R1-17 回迁映射：零数组预分配由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_mkcopy` 实现（多 1 槽位旧形一致，返柄调用方持有）。
		scope.addImport("sa_std/ts_array.sa")
		w.Write(fmt.Sprintf("  %s = call @ts_arr_mkcopy(%s)\n", h, n))
		base = h
	} else if saIsStrExpr(argNodes[0], scope) {
		// 串源：逐字成串元数组（复用 `sci/sa_std/ts_string.sa` `@ts_str_split`
		// 的零长分隔符扫描（sci 侧已修：原 `nstart = idx + sl` 在 sl=0 时
		// 游标不动 → 死循环）；段句柄由 sci 侧分配（调用方持数组柄），
		// 端口侧不建字句柄（循环体内建的字句柄其域尾释在体外 → verifier
		// UnknownRegister 实锤，禁半落地）。
		// 语义差：split("") 按码元切，Array.from 按码点切（代理对边缘差；
		// 端口域为 ASCII，两者一致）。
		sh, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		empty := saLowerStringLiteral(w, "", scope, nextTemp)
		bp, bl := saExpandStr(w, sh, nextTemp)
		sp, sl := saExpandStr(w, empty, nextTemp)
		scope.addImport("sa_std/ts_string.sa")
		h := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_str_split(%s, %s, %s, %s, 0, 0)\n", h, bp, bl, sp, sl))
		saOwnTemp(scope, h)
		saMarkArrStr(scope, h)
		base = h
	} else {
		src, msg := saArrValueOf(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// R1-18 回迁映射：切片克隆由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_clone_flat` 实现（新柄新缓冲、同槽宽浅拷；标记透传保留本侧）。
		scope.addImport("sa_std/ts_array.sa")
		h := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_clone_flat(%s)\n", h, src))
		saPropArrNest(scope, src, h)
		saPropArrStr(scope, src, h)
		saPropArrInnerStr(scope, src, h)
		base = h
	}
	if mapper == nil {
		return base, ""
	}
	// 克隆/预分配新柄入 mapper 前登记归属（mapper 内链式释放新柄前释基；
	// 无 mapper 径调用方收基；具名借用基不受影响（本处恒新柄）；R1 同纪律）。
	saOwnTemp(scope, base)
	return saHigherOrderMap(w, base, mapper, scope, pos, refusals, needImport, nextLabel, nextTemp, "")
}

// saCallbackValue 内联一次回调应用（形参绑循环值，体落值或槽汇合；
// 形状证据：封存 callbackValue:5194-5250 + bindCallbackParam:5255-5305。
// 本薄口回调恒 i32 位（串回调值大声拒）；具名遮蔽存取恢复；return 拦截经
// scope.inlineRet，嵌套回调栈式保存恢复）。
// saDestructureCallbackPattern展开回调数组模式形参（`([a, b]) =>`；实参须为数组句柄，
// 逐元越界归零join绑i32；空穴跳过，rest/嵌套大声拒；名登记由调用方快照先行，体后恢复。
// (evidences: bindCallbackParam + destructureArray + bindPatternName in archive.)
// returns msg (empty means success).
func saDestructureCallbackPattern(w printer.EmitTextWriter, pat *ast.Node, arr string, scope *saScope, pos func(int) (int, int), nextLabel, nextTemp *int) string {
	idx := 0
	for _, el := range pat.AsBindingPattern().Elements.Nodes {
		if el.Kind != ast.KindBindingElement {
			idx++
			continue
		}
		be := el.AsBindingElement()
		if be.DotDotDotToken != nil {
			return "rest elements in destructuring are not lowerable"
		}
		nm := be.Name()
		if nm == nil {
			idx++
			continue
		}
		if nm.Kind != ast.KindIdentifier {
			return "nested destructuring shape is not lowerable"
		}
		name := nm.Text()
		v := saLowerCheckedIndex(w, arr, fmt.Sprintf("%d", idx), nextLabel, nextTemp)
		w.Write(fmt.Sprintf("  %s = %s\n", name, v))
		scope.types[name] = "i32"
		saDeclareInitOwn(scope, name, v)
		idx++
	}
	return ""
}

// saCallbackScalarKind resolves the scalar snapshot kind (f64 stays float, else i32).
func saCallbackScalarKind(kinds []string, i int) string {
	if i < len(kinds) && kinds[i] == "f64" {
		return "f64"
	}
	return "i32"
}

// saNestedElemKinds reports callback element kinds for nested-slice or
// string-element receivers (element binds handle/str directly).
// saSortElemKinds reports comparator kinds for nested-slice receivers (both compare
// handles directly).
func saSortElemKinds(scope *saScope, recv string) []string {
	if scope.arrNest == nil || !scope.arrNest[recv] {
		return nil
	}
	return []string{"arr", "arr"}
}

func saNestedElemKinds(scope *saScope, recv string, idx, n int) []string {
	if scope.arrNest != nil && scope.arrNest[recv] {
		kinds := make([]string, n)
		if idx >= 0 && idx < n {
			kinds[idx] = "arr"
		}
		return kinds
	}
	// string-element receivers bind the element as str (mirror of nested
	// handles; `.length`/methods route via str; snapshot stays a copy).
	if scope.arrStr != nil && scope.arrStr[recv] {
		kinds := make([]string, n)
		if idx >= 0 && idx < n {
			kinds[idx] = "str"
		}
		return kinds
	}
	if scope.arrJsVal != nil && scope.arrJsVal[recv] {
		kinds := make([]string, n)
		if idx >= 0 && idx < n {
			kinds[idx] = "jsval"
		}
		return kinds
	}
	return nil
}

func saCallbackValue(w printer.EmitTextWriter, cb *ast.Node, argVals []string, wantValue bool, wantKind string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, inLoop bool, paramKinds ...[]string) (string, string) {
	params := cb.Parameters()
	if len(params) > len(argVals) {
		return "", "callback declares too many parameters"
	}
	type saved struct {
		kind  string
		ok    bool
		alias string
		aok   bool
		own   *saOwn
		ownOk bool
		mv    string
		mvok  bool
	}
	keep := map[string]saved{}
	bind := func(name, kind string) {
		if _, done := keep[name]; !done {
			old, ok := scope.types[name]
			oa, aok := scope.mathAlias[name]
			// 归属记录同快照（值拷贝：回调体内经同一指针改旗标不得污染快照，
			// 恢复时原样贴回；封存 bindCallbackParam 遮蔽同形）。
			var oc saOwn
			oo, ook := scope.ownState[name]
			if ook && oo != nil {
				oc = *oo
				oo = &oc
			}
			mv, mvok := scope.mapVals[name]
			keep[name] = saved{kind: old, ok: ok, alias: oa, aok: aok, own: oo, ownOk: ook, mv: mv, mvok: mvok}
		}
		scope.types[name] = kind
	}
	// releaseInstAlias 释 temp 源实例形参（`o = t` move 后堆归 o，t 已标
	// consumed；命名实参不碰（维持既有用例行为，move 别名化另立史诗）；
	// 自别名跳过；体不能释 o（bind 只记种不记归属），由成功路径在体后收；
	// 失败路径产物作废不收）。
	releaseInstAlias := func() {
		for i, p := range params {
			if len(paramKinds) == 0 || i >= len(paramKinds[0]) || len(paramKinds[0][i]) <= 5 || paramKinds[0][i][:5] != "inst:" || i >= len(argVals) {
				continue
			}
			pd := p.AsParameterDeclaration()
			if pd == nil {
				continue
			}
			nm := pd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() == argVals[i] || !saIsTempOp(argVals[i]) {
				continue
			}
			w.Write(fmt.Sprintf("  !%s\n", nm.Text()))
		}
	}
	done := func() {
		for name, s := range keep {
			if s.ok {
				scope.types[name] = s.kind
			} else {
				delete(scope.types, name)
			}
			if s.aok {
				scope.mathAlias[name] = s.alias
			} else {
				delete(scope.mathAlias, name)
			}
			if s.ownOk {
				scope.ownState[name] = s.own
			} else {
				delete(scope.ownState, name)
			}
			if s.mvok {
				if scope.mapVals == nil {
					scope.mapVals = map[string]string{}
				}
				scope.mapVals[name] = s.mv
			} else if scope.mapVals != nil {
				delete(scope.mapVals, name)
			}
		}
	}
	// 变参 kinds 为方法实例形参并行表（nil 种恒 i32，供数组回调）。
	kinds := []string(nil)
	if len(paramKinds) > 0 {
		kinds = paramKinds[0]
	}
	for i, p := range params {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			done()
			return "", "callback parameter shape is not lowerable"
		}
		if pd.DotDotDotToken != nil || pd.QuestionToken != nil || pd.Initializer != nil {
			done()
			return "", "callback parameter shape is not lowerable"
		}
		nm := pd.Name()
		if nm == nil {
			done()
			return "", "callback parameter shape is not lowerable"
		}
		if nm.Kind == ast.KindArrayBindingPattern {
			// array pattern params destructure the handle element-wise (cf declaration destructure).
			if i >= len(argVals) {
				done()
				return "", "callback declares too many parameters"
			}
			for _, el := range nm.AsNode().AsBindingPattern().Elements.Nodes {
				if el.Kind != ast.KindBindingElement {
					continue
				}
				if bn := el.AsBindingElement().Name(); bn != nil && bn.Kind == ast.KindIdentifier {
					bind(bn.Text(), "i32")
				}
			}
			if msg := saDestructureCallbackPattern(w, nm.AsNode(), argVals[i], scope, pos, nextLabel, nextTemp); msg != "" {
				done()
				return "", msg
			}
			continue
		}
		if nm.Kind != ast.KindIdentifier {
			done()
			return "", "callback parameter shape is not lowerable"
		}
		// 形参名禁影存活归属（`name = arg` 直写同名寄存器：调用方同名绑定若有
		// 未释放归属（堆句柄），即被冲掉（`e = add 1, 0` 冲实例柄，下游
		// verifier 报重定义甚或静默错码）；捕获回放等 stale 记种（无归属）
		// 重绑无害，放行。标量沿旧口径不动。
		// 形状证据：方法内联快照绑定同形（上游同位静默错译，本仓大声拒）。
		{
			pname := nm.Text()
			if _, ok := scope.types[pname]; ok {
				liveOwn := false
				if oo, ook := scope.ownState[pname]; ook && oo != nil && oo.heap && !oo.consumed && !oo.released {
					liveOwn = true
				}
				if liveOwn {
					ln, col := pos(p.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "callback parameter " + pname + " shadows a live binding (rename it)"})
					done()
					return "", "callback parameter " + pname + " shadows a live binding (rename it)"
				}
			}
		}
		// 标量快照拷贝（形状证据：封存 bindCallbackParam:5289-5294）。
		// 实例句柄直传绑定（方法实例形参经变参 kinds 表）。
		name := nm.Text()
		if i < len(kinds) && len(kinds[i]) > 5 && kinds[i][:5] == "inst:" {
			// 命名实参快照（`o = add p, 0`，saStoreLocal 命名源同形）：直写即 move，
			// 调用方绑定（含同为接收者 `p.f(p)`）随后读即 UseAfterMove。
			if saIsTempOp(argVals[i]) {
				w.Write(fmt.Sprintf("  %s = %s\n", name, argVals[i]))
			} else {
				w.Write(fmt.Sprintf("  %s = add %s, 0\n", name, argVals[i]))
			}
			// temp 实参 move 即标 consumed（返前 draining 跳过，防 `!t` 双释；
			// 命名实参不标（读位不查 consumed，标后悬垂读更隐蔽，维持既有）；
			// saConsumeTemp 对命名 no-op，天然区分；上游 new-实参同形无别名）。
			saConsumeTemp(scope, argVals[i])
			bind(name, kinds[i])
			continue
		}
		// handle elements (nested slices) bind directly, never owned.
		if i < len(kinds) && kinds[i] == "arr" {
			w.Write(fmt.Sprintf("  %s = %s\n", name, argVals[i]))
			bind(name, "arr")
			continue
		}
		// map 柄直传绑定（值种随调用点表，缺表恒 i32；快照恢复同 types；
		// 同名实参跳过自拷，禁重定义）。
		if i < len(kinds) && kinds[i] == "map" {
			if argVals[i] != name {
				w.Write(fmt.Sprintf("  %s = %s\n", name, argVals[i]))
			}
			bind(name, "map")
			if mv, ok := scope.mapVals[argVals[i]]; ok {
				saSetMapVal(scope, name, mv)
			}
			continue
		}
		// string elements snapshot the handle word and bind str (upstream
		// `s = add t, 0` + no release; `.length`/methods route via str).
		if i < len(kinds) && kinds[i] == "str" {
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", name, argVals[i]))
			bind(name, "str")
			saDeclarePlain(scope, name)
			continue
		}
		// jsval 形参借用绑定（元素句柄仍归数组所有，回调体不释；直写即
		// temp move，具名源用 add 0 拷贝；与 inst 借用同律，唯种为 jsval）。
		// Explicit number annotation overrides jsval element binding (prior
		// g1 test: number callback on a jsval receiver lowers as i32; an
		// unknown/no annotation stays jsval).
		if i < len(kinds) && kinds[i] == "jsval" && !(pd.Type != nil && (pd.Type.Kind == ast.KindNumberKeyword || (pd.Type.Kind == ast.KindTypeReference && pd.Type.AsTypeReferenceNode() != nil && pd.Type.AsTypeReferenceNode().TypeName != nil && pd.Type.AsTypeReferenceNode().TypeName.Text() == "i32"))) {
			if saIsTempOp(argVals[i]) {
				w.Write(fmt.Sprintf("  %s = %s\n", name, argVals[i]))
			} else {
				w.Write(fmt.Sprintf("  %s = add %s, 0\n", name, argVals[i]))
			}
			bind(name, "jsval")
			saDeclarePlain(scope, name)
			continue
		}
		// f64 形参快照绑定（`fadd x, 0.0` 拷贝，无 add 截断；直 `=` 即别名，
		// 具名实参随后释放即 UseAfterMove；与 i32 `add` 快照同形）。
		if i < len(kinds) && kinds[i] == "f64" {
			w.Write(fmt.Sprintf("  %s = fadd %s, 0.0\n", name, argVals[i]))
			bind(name, "f64")
			saDeclarePlain(scope, name)
			continue
		}
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", name, argVals[i]))
		bind(name, saCallbackScalarKind(kinds, i))
		saDeclarePlain(scope, name)
	}
	// 体归属基点（高阶循环体 arm 式出口截断用；直内联体由 drain 回收，不截）。
	ownBase := len(scope.ownOrder)
	body := cb.Body()
	if body == nil {
		done()
		return "", "callback has no body"
	}
	if body.Kind != ast.KindBlock {
		var v string
		if body.Kind == ast.KindCallExpression {
			op, voidCall, msg := saEvalCall(w, body.AsCallExpression(), scope, pos, refusals, nextTemp)
			if msg != "" {
				done()
				return "", msg
			}
			if voidCall {
				if wantValue {
					done()
					return "", "void callback value"
				}
				done()
				// void 体亦截断（`forEach(print)` 即此路；与值路同纪律）。
				if inLoop {
					saTruncateBodyOwned(scope, ownBase)
				}
				return "0", ""
			}
			v = op
		} else if wantKind == "arr" {
			// flatMap 回调位：数组字面量直构 / 数组返回调用 / 数组绑定直传；
			// 嵌套结果大声拒；串元结果放行（数组皆 4 字节词数组，append_slice
			// 按词拷贝与串元同形；结果 arrStr 由 flatMap 循环向 h 传播）。
			// 形状证据：字面量走 saLowerArrayLiteral（嵌套标记同 186-226），
			// 调用种走 saArrCallRet（与 1476-1478 同门），具名走 types 表。
			var av string
			switch {
			case body.Kind == ast.KindArrayLiteralExpression:
				h, msg := saLowerArrayLiteral(w, body, scope, pos, refusals, nextTemp)
				if msg != "" {
					done()
					return "", msg
				}
				if scope.arrNest[h] {
					done()
					return "", "flatMap callback must return a flat number array"
				}
				av = h
			case body.Kind == ast.KindCallExpression:
				k, ok := saArrCallRet(body.AsCallExpression(), scope)
				if !ok || k != "arr" {
					done()
					return "", "flatMap callback must return an array"
				}
				aop, avoidCall, amsg := saEvalCall(w, body.AsCallExpression(), scope, pos, refusals, nextTemp)
				if amsg != "" {
					done()
					return "", amsg
				}
				if avoidCall {
					done()
					return "", "void callback value"
				}
				if scope.arrNest[aop] {
					done()
					return "", "flatMap callback must return a flat number array"
				}
				av = aop
			case body.Kind == ast.KindIdentifier:
				if k, ok := scope.types[body.Text()]; !ok || k != "arr" {
					done()
					return "", "flatMap callback must return an array"
				}
				if scope.arrNest[body.Text()] {
					done()
					return "", "flatMap callback must return a flat number array"
				}
				av = body.Text()
			default:
				done()
				return "", "flatMap callback must return an array"
			}
			done()
			releaseInstAlias()
			if inLoop {
				saTruncateBodyOwned(scope, ownBase)
			}
			// 循环域截断吞掉内层 temp 归属记录；具名直传非 temp 不重登记。
			if saIsTempOp(av) {
				saOwnTemp(scope, av)
			}
			return av, ""
		} else if wantKind == "str" {
			sop, msg := saEvalStr(w, body, scope, pos, refusals, nextTemp)
			if msg != "" {
				done()
				return "", msg
			}
			v = sop
		} else if body != nil && body.Kind == ast.KindIdentifier {
			if k, ok := scope.types[body.Text()]; ok && k == "arr" {
				// bare handle bodies pass the word through (filter predicates, identity maps).
				v = body.Text()
			} else if wantKind == "i32" {
				// bool-position bare predicate: str identifiers coerce
				// by truthiness (saEvalBool); all other identifier
				// kinds delegate to saEvalI32 inside saEvalBool,
				// so this is a pure widening for str predicates.
				op, msg := saEvalBool(w, body, scope, pos, refusals, nextTemp)
				if msg != "" {
					done()
					return "", msg
				}
				v = op
			} else {
				op, msg := saEvalI32(w, body, scope, pos, refusals, nextTemp)
				if msg != "" {
					done()
					return "", msg
				}
				v = op
			}
		} else {
			op, msg := saEvalI32(w, body, scope, pos, refusals, nextTemp)
			if msg != "" {
				done()
				return "", msg
			}
			v = op
		}
		done()
		releaseInstAlias()
		// 高阶循环体出口截断（for-of 体臂同纪律：体临时量落循环域内，
		// 不可外泄至函数尾 drain，否则域外 `!` 判 UnknownRegister；
		// 高阶回调恒 i32 位，结果非堆柄，无 except 必要）。
		if inLoop {
			saTruncateBodyOwned(scope, ownBase)
		}
		return v, ""
	}

	kind := "i32"
	if wantKind == "str" || wantKind == "arr" || wantKind == "f64" || strings.HasPrefix(wantKind, "inst:") {
		kind = wantKind
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if !strings.HasPrefix(kind, "inst:") {
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
		saOwnTemp(scope, slot)
	}
	endL := fmt.Sprintf("L_cb_end_%d", *nextLabel)
	*nextLabel++
	savedRet := scope.inlineRet
	ir := &saInlineRet{slot: slot, end: endL, kind: kind, scopeBase: len(scope.ownOrder)}
	scope.inlineRet = ir
	stmts, ok := saBlockStmts(body)
	if !ok {
		scope.inlineRet = savedRet
		done()
		return "", "unsupported callback body"
	}
	armOK := saLowerArm(w, stmts, false, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.inlineRet = savedRet
	done()
	if !armOK {
		return "", "unsupported callback body"
	}
	// 高阶循环体出口截断（slot 注册于基点前保留，结果 out 于截断后具化）。
	if inLoop {
		saTruncateBodyOwned(scope, ownBase)
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	releaseInstAlias()
	if strings.HasPrefix(kind, "inst:") {
		// 实例返回：slot 即汇合寄存器（各 return 位 move/快照落同名，无内存槽）；
		// 移交（new/体内新生）即调用方归属，别名（this/绑定）快照不登记堆。
		if ir.instMode == 0 {
			return "", "method returning an instance has no instance return"
		}
		scope.types[slot] = kind
		if ir.instMode == 1 {
			saOwnTemp(scope, slot)
		} else {
			saDeclarePlain(scope, slot)
		}
		return slot, ""
	}
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if kind == "str" {
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", out, slot))
		saReleaseOwnedTemp(w, scope, slot)
		return out, ""
	}
	if kind == "arr" {
		// 块体数组返回读回（镜像 str 路径；out 归属由调用方释放，见 flatMap 循环）。
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", out, slot))
		saReleaseOwnedTemp(w, scope, slot)
		saOwnTemp(scope, out)
		if scope.arrStr != nil && scope.arrStr[slot] {
			saMarkArrStr(scope, out)
		}
		return out, ""
	}
	if kind == "f64" {
		// 块体双精度返回读回（镜像 i32 路径；记 f64 种供下游 f64 位）。
		w.Write(fmt.Sprintf("  %s = load %s + 0 as f64\n", out, slot))
		saReleaseOwnedTemp(w, scope, slot)
		scope.types[out] = "f64"
		return out, ""
	}
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	saReleaseOwnedTemp(w, scope, slot)
	return out, ""
}

// saLowerInlineArrReturn 块体数组返回（inlineRet arr 种）：存槽（ptr 槽）
// +jmp end（镜像 str 分支；嵌套/串元大声拒，外层 esz 恒 4 i32 槽；求值走
// saArrValueOf 数组位）。由 saLowerReturn 分发（transpile.go 零领域逻辑）。
func saLowerInlineArrReturn(w printer.EmitTextWriter, expr *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, stmtPos int) (bool, bool) {
	hop, msg := saArrValueOf(w, expr, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(stmtPos)
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false, true
	}
	if scope.arrNest[hop] {
		ln, col := pos(stmtPos)
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "block-body array return must be a flat number array"})
		return false, true
	}
	// 串元返回向汇合槽传播 arrStr（单调：任一返回臂为串元即记串元，
	// 空数组臂不清除；调用方再由槽向 out 传播，flatMap 再向 h 传播）。
	if scope.arrStr != nil && scope.arrStr[hop] {
		saMarkArrStr(scope, scope.inlineRet.slot)
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", scope.inlineRet.slot, hop))
	saReleaseDeeperThan(w, scope, scope.inlineRet.scopeBase)
	w.Write(fmt.Sprintf("  jmp %s\n", scope.inlineRet.end))
	return true, false
}

// saTruncateBodyOwned 截断基点后新增归属（无发射；for-of 体臂
// saScopeExit 同形：块内新登记名出体即除名）。
func saTruncateBodyOwned(scope *saScope, base int) {
	if base < 0 {
		base = 0
	}
	if base > len(scope.ownOrder) {
		return
	}
	for _, name := range scope.ownOrder[base:] {
		delete(scope.ownState, name)
	}
	scope.ownOrder = scope.ownOrder[:base]
}

// saHigherOrderMap `map` 内联（新数组逐元回调值压栈；形状证据：封存 lowerHigherOrder:4945-4965）。
func saHigherOrderMap(w printer.EmitTextWriter, recv string, cb *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, elemLayout string) (string, string) {
	h := saNewEmptyArray(w, nextTemp)
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_mp_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_mp_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_mp_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	// Map 结果串元判定（纯查表，无发射）：输入为串元数组时，回调首参
	// 临时绑 str，回调体经 saIsStrExpr 判串（trim/toLowerCase/恒等/字面量
	// 等），则结果记 arrStr，使链式 .filter/.map 续按串元绑定；i32
	// 结果（length 等）不记，避免误标。
	// Map result string-element detection (pure lookup, no emission):
	// string-element receivers bind the parameter as str, and
	// instance-element receivers bind it as inst:layout, then the
	// callback result expression (a block body unwraps to its single
	// return) is tested as a string expression. This marks str results
	// (field reads, trim calls) arrStr so chained reads treat elements
	// as strings.
	mapStr := false
	if params := cb.Parameters(); len(params) > 0 {
		if pd := params[0].AsParameterDeclaration(); pd != nil {
			if nm := pd.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				bindKind := ""
				if scope.arrStr != nil && scope.arrStr[recv] {
					bindKind = "str"
				} else if elemLayout != "" {
					bindKind = "inst:" + elemLayout
				}
				if bindKind != "" {
					bodyExpr := cb.Body()
					// Multi-statement blocks (JEV extend_mapstr_unwrap 0.82):
					// a block with exactly one return statement unwraps to
					// that return's expression for the string-result test,
					// so `const x = ...; return "<str>"` maps route through
					// the str slot instead of being evaluated as i32.
					if bodyExpr != nil && bodyExpr.Kind == ast.KindBlock {
						if bd := bodyExpr.AsBlock(); bd != nil && bd.Statements != nil {
							var retExpr *ast.Node
							retCount := 0
							for _, st := range bd.Statements.Nodes {
								if st == nil {
									continue
								}
								if st.Kind == ast.KindReturnStatement {
									if rs := st.AsReturnStatement(); rs != nil {
										retCount++
										retExpr = rs.Expression
									}
								}
							}
							if retCount == 1 && retExpr != nil {
								bodyExpr = retExpr
							}
							// Resolve a bare-identifier return through body locals
							// (up to 8 steps): `const s = [...].join(); return s;`
							// and `const n = role.name.trim(); return n;` detect
							// as string results via their initializers.
							for step := 0; step < 8 && bodyExpr != nil && bodyExpr.Kind == ast.KindIdentifier; step++ {
								found := false
								for _, st := range bd.Statements.Nodes {
									if st == nil || st.Kind != ast.KindVariableStatement {
										continue
									}
									vs := st.AsVariableStatement()
									if vs == nil || vs.DeclarationList == nil {
										continue
									}
									dl := vs.DeclarationList.AsVariableDeclarationList()
									if dl == nil {
										continue
									}
									for _, d := range dl.Declarations.Nodes {
										if d == nil || d.Kind != ast.KindVariableDeclaration {
											continue
										}
										vd := d.AsVariableDeclaration()
										if vd == nil || vd.Initializer == nil {
											continue
										}
										nm := vd.Name()
										if nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() != bodyExpr.Text() {
											continue
										}
										bodyExpr = vd.Initializer
										found = true
									}
								}
								if !found {
									break
								}
							}
						}
					}
					oldKind, hadOld := scope.types[nm.Text()]
					scope.types[nm.Text()] = bindKind
					mapStr = saIsStrExpr(bodyExpr, scope)
					if hadOld {
						scope.types[nm.Text()] = oldKind
					} else {
						delete(scope.types, nm.Text())
					}
				}
			}
		}
	}
	el := saArrElemAt(w, data, i, nextTemp)
	paramKinds := saNestedElemKinds(scope, recv, 0, 2)
	if elemLayout != "" {
		// Instance-element receivers bind the callback parameter as
		// inst:layout (mirror of the find/filter inline loops, which
		// never route through the i32 callback binding); the
		// per-iteration release discipline is saCallbackValue's own
		// inst-param path, the same shape as for-of row release.
		paramKinds = []string{"inst:" + elemLayout, "i32"}
	}
	cbKind := "i32"
	if mapStr {
		// String-result callbacks (expression or block body) evaluate
		// through the str path so block returns land in the ptr slot;
		// i32 results keep the existing i32 path.
		cbKind = "str"
	}
	v, msg := saCallbackValue(w, cb, []string{el, i}, true, cbKind, scope, pos, refusals, needImport, nextLabel, nextTemp, true, paramKinds)
	if msg != "" {
		return "", msg
	}
	if mapStr {
		saMarkArrStr(scope, h)
	}
	saLowerArrayPush(w, h, v, scope, nextTemp)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	// 新柄归属 + 链式接收者回收（R1 slice/concat 同纪律；具名/借用基 no-op）。
	saOwnTemp(scope, h)
	saReleaseOwnedTemp(w, scope, recv)
	return h, ""
}

// saHigherOrderFlatMap `flatMap` 内联（map 展开 + 一层拼片 = JS 语义；
// 回调须返扁平 i32 数组，分支拒因见 saCallbackValue "arr" 位；
// 拼片复 `saAppendSlice`（R3-5 `@ts_arr_append_slice`），内层 temp 拼后即释，
// 具名直传为借用不释；循环骨架与 saHigherOrderMap 同形）。
func saHigherOrderFlatMap(w printer.EmitTextWriter, recv string, cb *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	h := saNewEmptyArray(w, nextTemp)
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_fm_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_fm_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_fm_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	el := saArrElemAt(w, data, i, nextTemp)
	v, msg := saCallbackValue(w, cb, []string{el, i}, true, "arr", scope, pos, refusals, needImport, nextLabel, nextTemp, true, saNestedElemKinds(scope, recv, 0, 2))
	if msg != "" {
		return "", msg
	}
	if scope.arrStr != nil && scope.arrStr[v] {
		saMarkArrStr(scope, h)
	}
	saAppendSlice(w, h, v, scope, nextTemp)
	saReleaseOwnedTemp(w, scope, v)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	saOwnTemp(scope, h)
	saReleaseOwnedTemp(w, scope, recv)
	return h, ""
}

// saArrElemAt 读 data[idx] 为 i32 临时量（循环体元加载共用）。
func saArrElemAt(w printer.EmitTextWriter, data, idx string, nextTemp *int) string {
	off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, idx))
	addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	v := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", v, addr))
	return v
}

// saHigherOrder взвод通用扫描骨架：forEach/filter/find 系/some/every 共用
// （各按命中语义收尾；形状证据：封存 lowerHigherOrder:4926-5119）。
// saFindTypeofGuard detects a find callback whose body is exactly a typeof
// guard on its first parameter: typeof entry === "string" (or !==, or a block
// with a single return of that). JEV find_predicate_as_typeof_guard 0.99:
// do not evaluate the guard via saCallbackValue (which binds the param by
// element kind and then hits the tcx any/unknown guard in saTypeofKind);
// lower it directly from element kind in saHigherOrderScan.
func saFindTypeofGuard(cb *ast.Node) (string, string, bool, bool) {
	if cb == nil {
		return "", "", false, false
	}
	if cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression {
		return "", "", false, false
	}
	params := cb.Parameters()
	if len(params) < 1 {
		return "", "", false, false
	}
	pd := params[0].AsParameterDeclaration()
	if pd == nil {
		return "", "", false, false
	}
	nm := pd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", "", false, false
	}
	paramName := nm.Text()
	body := cb.Body()
	if body == nil {
		return "", "", false, false
	}
	expr := body
	if body.Kind == ast.KindBlock {
		bd := body.AsBlock()
		if bd == nil || bd.Statements == nil || len(bd.Statements.Nodes) != 1 {
			return "", "", false, false
		}
		if bd.Statements.Nodes[0].Kind != ast.KindReturnStatement {
			return "", "", false, false
		}
		rs := bd.Statements.Nodes[0].AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return "", "", false, false
		}
		expr = rs.Expression
	}
	if expr.Kind != ast.KindBinaryExpression {
		return "", "", false, false
	}
	be := expr.AsBinaryExpression()
	if be == nil {
		return "", "", false, false
	}
	opKind := saBinaryOpKind(be)
	neg := false
	switch opKind {
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
		neg = false
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
		neg = true
	default:
		return "", "", false, false
	}
	var typeOp, litNode *ast.Node
	for _, side := range []*ast.Node{be.Left, be.Right} {
		if side != nil && side.Kind == ast.KindTypeOfExpression {
			typeOp = side
		}
		if side != nil && side.Kind == ast.KindStringLiteral {
			litNode = side
		}
	}
	if typeOp == nil || litNode == nil {
		return "", "", false, false
	}
	inner := typeOp.AsTypeOfExpression().Expression
	if inner == nil || inner.Kind != ast.KindIdentifier || inner.Text() != paramName {
		return "", "", false, false
	}
	return paramName, litNode.Text(), neg, true
}

func saHigherOrderScan(w printer.EmitTextWriter, recv, method string, cb *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, elemLayout string) (string, string, string) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	emitElem := func(idx string) string { return saArrElemAt(w, data, idx, nextTemp) }
	switch method {
	case "forEach":
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = 0\n", i))
		topL := fmt.Sprintf("L_fe_top_%d", *nextLabel)
		*nextLabel++
		bodyL := fmt.Sprintf("L_fe_body_%d", *nextLabel)
		*nextLabel++
		endL := fmt.Sprintf("L_fe_end_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("%s:\n", topL))
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
		w.Write(fmt.Sprintf("%s:\n", bodyL))
		el := emitElem(i)
		if _, msg := saCallbackValue(w, cb, []string{el, i}, false, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, true, saNestedElemKinds(scope, recv, 0, 2)); msg != "" {
			return "", "", msg
		}
		inext := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		// 链式接收者回收（具名/借用基 no-op；标量返回不带柄）。
		saReleaseOwnedTemp(w, scope, recv)
		return "0", "i32", ""
	case "filter":
		if bpParam, bpPred, bpOk := saFilterBoolGuard(cb); bpOk {
			// Bool-predicate filter (JEV inline_loop_bool_pred 0.98):
			// iterate the array handle buffer, evaluate the predicate
			// per element by layout-offset field loads (chaining through
			// nested instance ptr slots), and push matching element
			// handles into a new array. The generic callback path binds
			// the parameter as i32, so nested bool field reads refuse
			// there; this branch never routes through saCallbackValue.
			if elemLayout == "" {
				return "", "", "filter bool predicate needs a known instance element layout"
			}
			bdef, ok := scope.classes[elemLayout]
			if !ok || bdef == nil {
				return "", "", "filter bool predicate element layout not recorded"
			}
			if msg := saCheckBoolPred(bpPred, bpParam, bdef, scope); msg != "" {
				return "", "", msg
			}
			bh := saNewEmptyArray(w, nextTemp)
			bi := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = 0\n", bi))
			btopL := fmt.Sprintf("L_fbp_top_%d", *nextLabel)
			*nextLabel++
			bbodyL := fmt.Sprintf("L_fbp_body_%d", *nextLabel)
			*nextLabel++
			bendL := fmt.Sprintf("L_fbp_end_%d", *nextLabel)
			*nextLabel++
			btakeL := fmt.Sprintf("L_fbp_take_%d", *nextLabel)
			*nextLabel++
			bskipL := fmt.Sprintf("L_fbp_skip_%d", *nextLabel)
			*nextLabel++
			w.Write(fmt.Sprintf("%s:\n", btopL))
			bc := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, %s\n", bc, bi, ln))
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", bc, bbodyL, bendL))
			w.Write(fmt.Sprintf("%s:\n", bbodyL))
			bel := emitElem(bi)
			bv, bmsg := saEvalBoolPred(w, bpPred, bpParam, bel, bdef, scope, nextTemp)
			if bmsg != "" {
				return "", "", bmsg
			}
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", bv, btakeL, bskipL))
			w.Write(fmt.Sprintf("%s:\n", btakeL))
			saLowerArrayPush(w, bh, bel, scope, nextTemp)
			w.Write(fmt.Sprintf("  jmp %s\n", bskipL))
			w.Write(fmt.Sprintf("%s:\n", bskipL))
			binext := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, 1\n", binext, bi))
			w.Write(fmt.Sprintf("  %s = %s\n", bi, binext))
			w.Write(fmt.Sprintf("  jmp %s\n", btopL))
			w.Write(fmt.Sprintf("%s:\n", bendL))
			saOwnTemp(scope, bh)
			saReleaseOwnedTemp(w, scope, recv)
			return bh, "arr", ""
		}
		h := saNewEmptyArray(w, nextTemp)
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = 0\n", i))
		topL := fmt.Sprintf("L_fi_top_%d", *nextLabel)
		*nextLabel++
		bodyL := fmt.Sprintf("L_fi_body_%d", *scope.nextLabel)
		*scope.nextLabel++
		endL := fmt.Sprintf("L_fi_end_%d", *nextLabel)
		*nextLabel++
		takeL := fmt.Sprintf("L_fi_take_%d", *nextLabel)
		*nextLabel++
		skipL := fmt.Sprintf("L_fi_skip_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("%s:\n", topL))
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
		w.Write(fmt.Sprintf("%s:\n", bodyL))
		el := emitElem(i)
		v, msg := saCallbackValue(w, cb, []string{el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, true, saNestedElemKinds(scope, recv, 0, 2))
		if msg != "" {
			return "", "", msg
		}
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", v, takeL, skipL))
		w.Write(fmt.Sprintf("%s:\n", takeL))
		saLowerArrayPush(w, h, el, scope, nextTemp)
		w.Write(fmt.Sprintf("  jmp %s\n", skipL))
		w.Write(fmt.Sprintf("%s:\n", skipL))
		inext := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		// 新柄归属 + 链式接收者回收（与 map 臂同纪律）。
		saOwnTemp(scope, h)
		saReleaseOwnedTemp(w, scope, recv)
		return h, "arr", ""
	case "find", "findIndex", "findLast", "findLastIndex":
		if eqParam, eqField, eqNeg, eqOther, eqOk := saFindEqGuard(cb); eqOk {
			// Equality find (JEV inline_loop_eq 0.98): inline loop over the
			// element handles, loading the compared field from each element
			// instance by layout offset. The generic callback path binds the
			// parameter as i32, so a string field read there refuses with
			// "only +/==/!= operate on strings"; this branch never routes
			// through saCallbackValue. Miss is the 0 handle (undefined);
			// findIndex variants store the index (miss -1) instead.
			if elemLayout == "" {
				return "", "", "find equality needs a known instance element layout"
			}
			def, ok := scope.classes[elemLayout]
			if !ok || def == nil {
				return "", "", "find equality element layout not recorded"
			}
			fkind, ok := def.fkinds[eqField]
			if !ok {
				return "", "", "find equality field not in element layout"
			}
			if fkind != "str" && fkind != "i32" && fkind != "bool" {
				return "", "", "find equality field kind not supported: " + fkind
			}
			off, ok := def.offsets[eqField]
			if !ok {
				return "", "", "find equality field has no layout offset"
			}
			isIndex := method == "findIndex" || method == "findLastIndex"
			last := method == "findLast" || method == "findLastIndex"
			// Hoisted string operand (evaluated once before the loop when
			// loop-invariant; released once after the loop on the single
			// straight-line path, so join states agree).
			var rhsHoisted string
			rhsHoistedOk := false
			if fkind == "str" && saFindEqRhsHoistable(eqOther, eqParam) {
				h, msg := saEvalStr(w, eqOther, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				rhsHoisted, rhsHoistedOk = h, true
			}
			slot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
			saOwnTemp(scope, slot)
			init := "0"
			if isIndex {
				init = "-1"
			}
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, init))
			i := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = 0\n", i))
			topL := fmt.Sprintf("L_feq_top_%d", *nextLabel)
			*nextLabel++
			bodyL := fmt.Sprintf("L_feq_body_%d", *scope.nextLabel)
			*scope.nextLabel++
			endL := fmt.Sprintf("L_feq_end_%d", *nextLabel)
			*nextLabel++
			hitL := fmt.Sprintf("L_feq_hit_%d", *nextLabel)
			*nextLabel++
			nextL := fmt.Sprintf("L_feq_next_%d", *nextLabel)
			*nextLabel++
			w.Write(fmt.Sprintf("%s:\n", topL))
			c := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
			w.Write(fmt.Sprintf("%s:\n", bodyL))
			el := emitElem(i)
			if fkind == "str" {
				var rhs string
				rhsPerIter := false
				if rhsHoistedOk {
					rhs = rhsHoisted
				} else {
					h, msg := saEvalStr(w, eqOther, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", "", msg
					}
					rhs, rhsPerIter = h, true
				}
				// Per-iteration owned temporaries must be released on BOTH
				// arms: saReleaseOwnedTemp would suppress the second arm
				// statically while only one arm runs dynamically, which
				// the verifier reports as PhiStateConflict at the join.
				// Hoisted handles are released once after the loop instead.
				rhsOwned := false
				if rhsPerIter {
					if b := saOwnOf(scope, rhs); b != nil && b.heap && !b.consumed && !b.released {
						rhsOwned = true
					}
				}
				releaseRhs := func() {
					if rhsOwned {
						w.Write(fmt.Sprintf("  !%s\n", rhs))
					}
				}
				fh := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", fh, el, off))
				fz := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = eq %s, 0\n", fz, fh))
				fzL := fmt.Sprintf("L_feq_fz_%d", *nextLabel)
				*nextLabel++
				fnzL := fmt.Sprintf("L_feq_fnz_%d", *nextLabel)
				*nextLabel++
				w.Write(fmt.Sprintf("  br %s -> %s, %s\n", fz, fzL, fnzL))
				// Missing (zero-filled) field handle: it equals only a
				// zero right-hand side, never a live string; this also
				// keeps ts_str_equals off a null handle.
				w.Write(fmt.Sprintf("%s:\n", fzL))
				eqv := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = eq %s, 0\n", eqv, rhs))
				if eqNeg {
					pz := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = eq %s, 0\n", pz, eqv))
					releaseRhs()
					w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pz, hitL, nextL))
				} else {
					releaseRhs()
					w.Write(fmt.Sprintf("  br %s -> %s, %s\n", eqv, hitL, nextL))
				}
				w.Write(fmt.Sprintf("%s:\n", fnzL))
				pred := saStringContentEq(w, fh, rhs, eqNeg, scope, nextTemp)
				releaseRhs()
				w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pred, hitL, nextL))
				if rhsOwned {
					if b := saOwnOf(scope, rhs); b != nil {
						b.released = true
					}
				}
			} else {
				fv := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", fv, el, off))
				rv, msg := saEvalI32(w, eqOther, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				op := "eq"
				if eqNeg {
					op = "ne"
				}
				pred := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, %s\n", pred, op, fv, rv))
				w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pred, hitL, nextL))
			}
			w.Write(fmt.Sprintf("%s:\n", hitL))
			if isIndex {
				w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, i))
			} else {
				w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, el))
			}
			if !last {
				w.Write(fmt.Sprintf("  jmp %s\n", endL))
			} else {
				w.Write(fmt.Sprintf("  jmp %s\n", nextL))
			}
			w.Write(fmt.Sprintf("%s:\n", nextL))
			inext := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
			w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
			w.Write(fmt.Sprintf("  jmp %s\n", topL))
			w.Write(fmt.Sprintf("%s:\n", endL))
			if rhsHoistedOk {
				saReleaseOwnedTemp(w, scope, rhsHoisted)
			}
			out := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			if isIndex {
				w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
			} else {
				w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", out, slot))
				scope.types[out] = "inst:" + elemLayout
			}
			saReleaseOwnedTemp(w, scope, slot)
			saReleaseOwnedTemp(w, scope, recv)
			if isIndex {
				return out, "i32", ""
			}
			return out, "inst:" + elemLayout, ""
		}
		if orParam, orDisj, orOk := saFindOrGuard(cb); orOk {
			// OR find (JEV inline_loop_or 0.98): per-element disjunction
			// over equality arms and optional-chain includes arms. Each
			// arm branches to the shared hit label or to the next arm's
			// label; the last arm's false branch is the next-iteration
			// label. Loop-invariant string right-hand sides are hoisted
			// once before the loop and released once after it, exactly
			// as in the single-equality find.
			if elemLayout == "" {
				return "", "", "find OR predicate needs a known instance element layout"
			}
			odef, ok := scope.classes[elemLayout]
			if !ok || odef == nil {
				return "", "", "find OR element layout not recorded"
			}
			for _, d := range orDisj {
				fkind, ok := odef.fkinds[d.field]
				if !ok {
					return "", "", "find OR field not in element layout"
				}
				if d.isIncludes {
					if fkind != "arr" {
						return "", "", "find OR includes field is not a string array field"
					}
				} else if fkind != "str" && fkind != "i32" && fkind != "bool" {
					return "", "", "find OR equality field kind not supported: " + fkind
				}
				if _, ok := odef.offsets[d.field]; !ok {
					return "", "", "find OR field has no layout offset"
				}
			}
			ohoisted := map[string]string{}
			for di, d := range orDisj {
				if saFindEqRhsHoistable(d.other, orParam) {
					h, msg := saEvalStr(w, d.other, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", "", msg
					}
					ohoisted[fmt.Sprintf("d%d", di)] = h
				}
			}
			oisIndex := method == "findIndex" || method == "findLastIndex"
			olast := method == "findLast" || method == "findLastIndex"
			oslot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", oslot))
			saOwnTemp(scope, oslot)
			oinit := "0"
			if oisIndex {
				oinit = "-1"
			}
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", oslot, oinit))
			oi := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = 0\n", oi))
			otopL := fmt.Sprintf("L_for_top_%d", *nextLabel)
			*nextLabel++
			obodyL := fmt.Sprintf("L_for_body_%d", *nextLabel)
			*nextLabel++
			oendL := fmt.Sprintf("L_for_end_%d", *nextLabel)
			*nextLabel++
			ohitL := fmt.Sprintf("L_for_hit_%d", *nextLabel)
			*nextLabel++
			onextL := fmt.Sprintf("L_for_next_%d", *nextLabel)
			*nextLabel++
			odLabels := make([]string, len(orDisj))
			for di := range orDisj {
				odLabels[di] = fmt.Sprintf("L_for_d%d_%d", di, *nextLabel)
				*nextLabel++
			}
			w.Write(fmt.Sprintf("%s:\n", otopL))
			oc := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, %s\n", oc, oi, ln))
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", oc, obodyL, oendL))
			w.Write(fmt.Sprintf("%s:\n", obodyL))
			oel := emitElem(oi)
			for di, d := range orDisj {
				if di > 0 {
					w.Write(fmt.Sprintf("%s:\n", odLabels[di]))
				}
				ofalseL := onextL
				if di+1 < len(orDisj) {
					ofalseL = odLabels[di+1]
				}
				var omsg string
				if d.isIncludes {
					omsg = saEmitIncludesDisjunct(w, oel, odef, d, ohoisted, fmt.Sprintf("d%d", di), ohitL, ofalseL, scope, pos, refusals, nextLabel, nextTemp)
				} else {
					omsg = saEmitEqDisjunct(w, oel, odef, d, ohoisted, fmt.Sprintf("d%d", di), ohitL, ofalseL, scope, pos, refusals, nextLabel, nextTemp)
				}
				if omsg != "" {
					return "", "", omsg
				}
			}
			w.Write(fmt.Sprintf("%s:\n", ohitL))
			if oisIndex {
				w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", oslot, oi))
			} else {
				w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", oslot, oel))
			}
			if !olast {
				w.Write(fmt.Sprintf("  jmp %s\n", oendL))
			} else {
				w.Write(fmt.Sprintf("  jmp %s\n", onextL))
			}
			w.Write(fmt.Sprintf("%s:\n", onextL))
			oinext := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, 1\n", oinext, oi))
			w.Write(fmt.Sprintf("  %s = %s\n", oi, oinext))
			w.Write(fmt.Sprintf("  jmp %s\n", otopL))
			w.Write(fmt.Sprintf("%s:\n", oendL))
			for _, h := range ohoisted {
				saReleaseOwnedTemp(w, scope, h)
			}
			oout := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			if oisIndex {
				w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", oout, oslot))
			} else {
				w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", oout, oslot))
				scope.types[oout] = "inst:" + elemLayout
			}
			saReleaseOwnedTemp(w, scope, oslot)
			saReleaseOwnedTemp(w, scope, recv)
			if oisIndex {
				return oout, "i32", ""
			}
			return oout, "inst:" + elemLayout, ""
		}
		if method == "find" || method == "findLast" {
			if _, typeofName, neg, ok := saFindTypeofGuard(cb); ok {
				if typeofName != "string" && typeofName != "number" {
					return "", "", "find typeof guard only supports string/number in this step"
				}
				elemKind := "i32"
				if scope.arrStr != nil && scope.arrStr[recv] {
					elemKind = "str"
				} else if scope.arrJsVal != nil && scope.arrJsVal[recv] {
					elemKind = "jsval"
				}
				resultKind := "i32"
				if typeofName == "string" {
					resultKind = "str"
				}
				last := method == "findLast"
				slot := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
				saOwnTemp(scope, slot)
				w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
				i := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = 0\n", i))
				topL := fmt.Sprintf("L_fd_top_%d", *nextLabel)
				*nextLabel++
				bodyL := fmt.Sprintf("L_fd_body_%d", *scope.nextLabel)
				*scope.nextLabel++
				endL := fmt.Sprintf("L_fd_end_%d", *nextLabel)
				*nextLabel++
				hitL := fmt.Sprintf("L_fd_hit_%d", *nextLabel)
				*nextLabel++
				nextL := fmt.Sprintf("L_fd_next_%d", *nextLabel)
				*nextLabel++
				w.Write(fmt.Sprintf("%s:\n", topL))
				c := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
				w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
				w.Write(fmt.Sprintf("%s:\n", bodyL))
				el := emitElem(i)
				var pred string
				if elemKind == "jsval" {
					tag := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", tag, el))
					code, _ := saJsValKindCode(typeofName)
					base := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = eq %s, %d\n", base, tag, code))
					if neg {
						inv := fmt.Sprintf("t_%d", *nextTemp)
						*nextTemp++
						w.Write(fmt.Sprintf("  %s = eq %s, 0\n", inv, base))
						pred = inv
					} else {
						pred = base
					}
				} else {
					baseIs := false
					if elemKind == "str" && typeofName == "string" {
						baseIs = true
					}
					if elemKind == "i32" && typeofName == "number" {
						baseIs = true
					}
					if neg {
						baseIs = !baseIs
					}
					if baseIs {
						pred = "1"
					} else {
						pred = "0"
					}
				}
				if pred == "0" || pred == "1" {
					pt := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = %s\n", pt, pred))
					pred = pt
				}
				w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pred, hitL, nextL))
				w.Write(fmt.Sprintf("%s:\n", hitL))
				storeVal := el
				if elemKind == "jsval" {
					if typeofName == "string" {
						ub := fmt.Sprintf("t_%d", *nextTemp)
						*nextTemp++
						w.Write(fmt.Sprintf("  %s = load %s + 8 as ptr\n", ub, el))
						storeVal = ub
					} else {
						ub := fmt.Sprintf("t_%d", *nextTemp)
						*nextTemp++
						w.Write(fmt.Sprintf("  %s = load %s + 16 as i32\n", ub, el))
						storeVal = ub
					}
				}
				w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, storeVal))
				if !last {
					w.Write(fmt.Sprintf("  jmp %s\n", endL))
				} else {
					w.Write(fmt.Sprintf("  jmp %s\n", nextL))
				}
				w.Write(fmt.Sprintf("%s:\n", nextL))
				inext := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
				w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
				w.Write(fmt.Sprintf("  jmp %s\n", topL))
				w.Write(fmt.Sprintf("%s:\n", endL))
				out := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
				saReleaseOwnedTemp(w, scope, slot)
				saReleaseOwnedTemp(w, scope, recv)
				return out, resultKind, ""
			}
		}
		last := method == "findLast" || method == "findLastIndex"
		slot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		saOwnTemp(scope, slot)
		init := "-1"
		if method == "find" || method == "findLast" {
			init = "0"
		}
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, init))
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = 0\n", i))
		topL := fmt.Sprintf("L_fd_top_%d", *nextLabel)
		*nextLabel++
		bodyL := fmt.Sprintf("L_fd_body_%d", *scope.nextLabel)
		*scope.nextLabel++
		endL := fmt.Sprintf("L_fd_end_%d", *nextLabel)
		*nextLabel++
		hitL := fmt.Sprintf("L_fd_hit_%d", *nextLabel)
		*nextLabel++
		nextL := fmt.Sprintf("L_fd_next_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("%s:\n", topL))
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
		w.Write(fmt.Sprintf("%s:\n", bodyL))
		el := emitElem(i)
		v, msg := saCallbackValue(w, cb, []string{el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, true, saNestedElemKinds(scope, recv, 0, 2))
		if msg != "" {
			return "", "", msg
		}
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", v, hitL, nextL))
		w.Write(fmt.Sprintf("%s:\n", hitL))
		if method == "find" || method == "findLast" {
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, el))
		} else {
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, i))
		}
		if !last {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		} else {
			w.Write(fmt.Sprintf("  jmp %s\n", nextL))
		}
		w.Write(fmt.Sprintf("%s:\n", nextL))
		inext := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
		saReleaseOwnedTemp(w, scope, slot)
		saReleaseOwnedTemp(w, scope, recv)
		return out, "i32", ""
	case "some", "every":
		if eqParam, eqField, eqNeg, eqOther, eqOk := saFindEqGuard(cb); eqOk {
			// Equality some/every (JEV inline_loop_eq_some 1.0): the
			// single-equality loop from the find branch, with boolean
			// result semantics (some: true on first match; every: false
			// on first non-match). The generic callback path binds the
			// parameter as i32, so a string field read refuses there;
			// this branch reuses saEmitEqDisjunct and never routes
			// through saCallbackValue.
			if elemLayout == "" {
				return "", "", "some/every equality needs a known instance element layout"
			}
			sdef, ok := scope.classes[elemLayout]
			if !ok || sdef == nil {
				return "", "", "some/every equality element layout not recorded"
			}
			sd := saOrDisjunct{field: eqField, neg: eqNeg, other: eqOther}
			sfkind, ok := sdef.fkinds[eqField]
			if !ok {
				return "", "", "some/every equality field not in element layout"
			}
			if sfkind != "str" && sfkind != "i32" && sfkind != "bool" {
				return "", "", "some/every equality field kind not supported: " + sfkind
			}
			if _, ok := sdef.offsets[eqField]; !ok {
				return "", "", "some/every equality field has no layout offset"
			}
			shoisted := map[string]string{}
			if sfkind == "str" && saFindEqRhsHoistable(eqOther, eqParam) {
				h, msg := saEvalStr(w, eqOther, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				shoisted["d0"] = h
			}
			_ = sfkind
			sslot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", sslot))
			saOwnTemp(scope, sslot)
			sinit := "0"
			if method == "every" {
				sinit = "1"
			}
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", sslot, sinit))
			si := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = 0\n", si))
			stopL := fmt.Sprintf("L_seq_top_%d", *nextLabel)
			*nextLabel++
			sbodyL := fmt.Sprintf("L_seq_body_%d", *nextLabel)
			*nextLabel++
			sendL := fmt.Sprintf("L_seq_end_%d", *nextLabel)
			*nextLabel++
			shitL := fmt.Sprintf("L_seq_hit_%d", *nextLabel)
			*nextLabel++
			snextL := fmt.Sprintf("L_seq_next_%d", *nextLabel)
			*nextLabel++
			smissL := fmt.Sprintf("L_seq_miss_%d", *nextLabel)
			*nextLabel++
			w.Write(fmt.Sprintf("%s:\n", stopL))
			sc := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, %s\n", sc, si, ln))
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", sc, sbodyL, sendL))
			w.Write(fmt.Sprintf("%s:\n", sbodyL))
			sel := emitElem(si)
			var smsg string
			if method == "some" {
				smsg = saEmitEqDisjunct(w, sel, sdef, sd, shoisted, "d0", shitL, snextL, scope, pos, refusals, nextLabel, nextTemp)
			} else {
				smsg = saEmitEqDisjunct(w, sel, sdef, sd, shoisted, "d0", snextL, smissL, scope, pos, refusals, nextLabel, nextTemp)
			}
			if smsg != "" {
				return "", "", smsg
			}
			if method == "some" {
				w.Write(fmt.Sprintf("%s:\n", shitL))
				w.Write(fmt.Sprintf("  store %s + 0, 1 as ptr\n", sslot))
				w.Write(fmt.Sprintf("  jmp %s\n", sendL))
			}
			if method == "every" {
				w.Write(fmt.Sprintf("%s:\n", smissL))
				w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", sslot))
				w.Write(fmt.Sprintf("  jmp %s\n", sendL))
			}
			w.Write(fmt.Sprintf("%s:\n", snextL))
			sinext := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, 1\n", sinext, si))
			w.Write(fmt.Sprintf("  %s = %s\n", si, sinext))
			w.Write(fmt.Sprintf("  jmp %s\n", stopL))
			w.Write(fmt.Sprintf("%s:\n", sendL))
			for _, h := range shoisted {
				saReleaseOwnedTemp(w, scope, h)
			}
			sout := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", sout, sslot))
			saReleaseOwnedTemp(w, scope, sslot)
			saReleaseOwnedTemp(w, scope, recv)
			return sout, "i32", ""
		}
		slot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		saOwnTemp(scope, slot)
		init, stop := "0", "1"
		if method == "every" {
			init, stop = "1", "0"
		}
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, init))
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = 0\n", i))
		topL := fmt.Sprintf("L_se_top_%d", *nextLabel)
		*nextLabel++
		bodyL := fmt.Sprintf("L_se_body_%d", *scope.nextLabel)
		*scope.nextLabel++
		endL := fmt.Sprintf("L_se_end_%d", *nextLabel)
		*nextLabel++
		hitL := fmt.Sprintf("L_se_hit_%d", *nextLabel)
		*nextLabel++
		nextL := fmt.Sprintf("L_se_next_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("%s:\n", topL))
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
		w.Write(fmt.Sprintf("%s:\n", bodyL))
		el := emitElem(i)
		v, msg := saCallbackValue(w, cb, []string{el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, true, saNestedElemKinds(scope, recv, 0, 2))
		if msg != "" {
			return "", "", msg
		}
		cmp := v
		if method == "every" {
			nv := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = eq %s, 0\n", nv, v))
			cmp = nv
		}
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cmp, hitL, nextL))
		w.Write(fmt.Sprintf("%s:\n", hitL))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, stop))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", nextL))
		inext := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
		saReleaseOwnedTemp(w, scope, slot)
		saReleaseOwnedTemp(w, scope, recv)
		return out, "i32", ""
	}
	return "", "", "unreachable"
}

// saHigherOrderReduce reduce/reduceRight 内联（形状证据：封存 lowerHigherOrder:5120-5180）。
func saHigherOrderReduce(w printer.EmitTextWriter, recv string, right bool, cb *ast.Node, initVal string, hasInit bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string, string) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	acc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if hasInit {
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", acc, initVal))
		if !right {
			w.Write(fmt.Sprintf("  %s = 0\n", i))
		} else {
			w.Write(fmt.Sprintf("  %s = sub %s, 1\n", i, ln))
		}
	} else {
		// 无初值以边缘元为种（空数组种 0 跳过循环；undefined→0 子集规则）。
		if !right {
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", acc, saLowerCheckedIndex(w, recv, "0", scope.nextLabel, nextTemp)))
			w.Write(fmt.Sprintf("  %s = 1\n", i))
		} else {
			last := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sub %s, 1\n", last, ln))
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", acc, saLowerCheckedIndex(w, recv, last, scope.nextLabel, nextTemp)))
			w.Write(fmt.Sprintf("  %s = sub %s, 2\n", i, ln))
		}
	}
	topL := fmt.Sprintf("L_rd_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_rd_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_rd_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if !right {
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	} else {
		w.Write(fmt.Sprintf("  %s = sge %s, 0\n", c, i))
	}
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	el := saArrElemAt(w, data, i, nextTemp)
	v, msg := saCallbackValue(w, cb, []string{acc, el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, true, saNestedElemKinds(scope, recv, 1, 3))
	if msg != "" {
		return "", "", msg
	}
	w.Write(fmt.Sprintf("  %s = %s\n", acc, v))
	step := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if !right {
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", step, i))
	} else {
		w.Write(fmt.Sprintf("  %s = sub %s, 1\n", step, i))
	}
	w.Write(fmt.Sprintf("  %s = %s\n", i, step))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	saReleaseOwnedTemp(w, scope, recv)
	return acc, "i32", ""
}

// saLowerSortWithCmp 比较器内联插入排序（形状证据：封存 lowerSortWithCmp:5539-5622）。
func saLowerSortWithCmp(w printer.EmitTextWriter, recv string, cb *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string, string) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	loadAt := func(idx string) (string, string) {
		off := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, idx))
		addr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
		v := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", v, addr))
		return v, addr
	}
	storeAt := func(addr, v string) {
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", addr, v))
	}
	cmpGt := func(x, y string) (string, string) {
		v, msg := saCallbackValue(w, cb, []string{x, y}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, true, saSortElemKinds(scope, recv))
		if msg != "" {
			return "", msg
		}
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sgt %s, 0\n", c, v))
		return c, ""
	}
	topL := fmt.Sprintf("L_cs_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_cs_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_cs_end_%d", *nextLabel)
	*nextLabel++
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 1\n", i))
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	key, _ := loadAt(i)
	j := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", j, i))
	inTop := fmt.Sprintf("L_cs_in_top_%d", *nextLabel)
	*nextLabel++
	inChk := fmt.Sprintf("L_cs_in_chk_%d", *nextLabel)
	*nextLabel++
	inBody := fmt.Sprintf("L_cs_in_body_%d", *nextLabel)
	*nextLabel++
	inEnd := fmt.Sprintf("L_cs_in_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("%s:\n", inTop))
	c1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sge %s, 0\n", c1, j))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c1, inChk, inEnd))
	w.Write(fmt.Sprintf("%s:\n", inChk))
	aj, _ := loadAt(j)
	gt, msg := cmpGt(aj, key)
	if msg != "" {
		return "", "", msg
	}
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", gt, inBody, inEnd))
	w.Write(fmt.Sprintf("%s:\n", inBody))
	j1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", j1, j))
	_, j1addr := loadAt(j1)
	storeAt(j1addr, aj)
	jm := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", jm, j))
	w.Write(fmt.Sprintf("  %s = %s\n", j, jm))
	w.Write(fmt.Sprintf("  jmp %s\n", inTop))
	w.Write(fmt.Sprintf("%s:\n", inEnd))
	k1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", k1, j))
	_, k1addr := loadAt(k1)
	storeAt(k1addr, key)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return recv, "arr", ""
}

// saArrCallbackNode 取调用实参中的首个内联回调（箭头/函数表达式；
// 具名标识符不内联，大声拒。形状证据：封存 lowerHigherOrder:4887-4902）。
func saArrCallbackNode(args []*ast.Node) (*ast.Node, int, string) {
	for i, a := range args {
		if a == nil {
			continue
		}
		// 括号包裹解包（`((x) => ...)` 与裸箭头同形；具名仍不内联）。
		n := a
		for n.Kind == ast.KindParenthesizedExpression {
			pe := n.AsParenthesizedExpression()
			if pe == nil || pe.Expression == nil {
				break
			}
			n = pe.Expression
		}
		if n.Kind == ast.KindArrowFunction || n.Kind == ast.KindFunctionExpression {
			return n, i, ""
		}
		if n.Kind == ast.KindIdentifier || a.Kind == ast.KindIdentifier {
			return nil, -1, "pass the arrow inline (named callbacks do not inline)"
		}
	}
	return nil, -1, ""
}

// saLowerArrCall 数组调用总线（成员 + Array.from；返回 (operand, 种, errMsg)。
// 种 ∈ {"i32","arr","str"}；形状证据：封存 lowerArrayMethod:6137-6397）。
func saLowerArrCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string, string) {
	if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" &&
			pa.Name() != nil && pa.Name().Text() == "from" {
			h, msg := saLowerArrayFrom(w, ce, scope, pos, refusals, needImport, nextLabel, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			return h, "arr", ""
		}
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.QuestionDotToken != nil {
		return "", "", "optional member call not lowerable"
	}
	recv, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		// jsval 接收器（JEV unbox_at_call, 0.83）：调用点才拆箱，标识符种
		// 不变；载荷 ptr 即数组句柄（kind 5；非数组运行时 JS 本会抛错，
		// 此处与既有未检 cast 同律，不另做 kind 校验）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && scope.types[pa.Expression.Text()] == "jsval" {
			recv = saUnboxJsValPtr(w, pa.Expression.Text(), nextTemp)
			// 拆箱后数组元即 jsval 句柄（convert_at_box 保证；此标记使
			// 回调参绑 jsval、元加载走 ptr，与 unknown[] 形参同律）。
			saMarkArrJsVal(scope, recv)
			msg = ""
		} else if h, ok := saTopArrPureRecv(w, pa, scope, nextTemp); ok {
			recv = h
		} else {
			return "", "", msg
		}
	}
	method := ""
	if pa.Name() != nil {
		method = pa.Name().Text()
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	i32arg := func(i int) (string, string) {
		if i >= len(argNodes) {
			return "", "missing argument"
		}
		return saEvalI32(w, argNodes[i], scope, pos, refusals, nextTemp)
	}
	switch method {
	case "push":
		// 空参即原长（`a.push()` 无操作返长，JS 同义；1468）。
		if len(argNodes) < 1 {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, recv))
			return t, "i32", ""
		}
		// 多参逐元压栈（返末次新长；JS 同义；584）。
		last := ""
		for _, an := range argNodes {
			// 展开形（`a.push(...src)`；逐元原位压栈，与多参同律；1388）。
			if an != nil && an.Kind == ast.KindSpreadElement {
				var msg string
				last, msg = saLowerPushSpread(w, recv, pa, an.AsSpreadElement(), scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				continue
			}
			v, msg := saArrayLiteralElem(w, an, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			// 已知 i32 数组收串/实例值拒（4 字节槽截断句柄；串标数组沿既有串元口径；
			// 按语法种判定（字面量未必记种）；铁律 4）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
				if k, ok := scope.types[pa.Expression.Text()]; ok && k == "arr" {
					if scope.arrStr == nil || !scope.arrStr[pa.Expression.Text()] {
						if saIsStrValue(an, scope) || saCouldBeInst(an, scope) {
							return "", "", "array element kind mismatch (non-string array takes i32 values)"
						}
					}
				}
			}
			last = saLowerArrayPush(w, recv, v, scope, nextTemp)
		}
		return last, "i32", ""
	case "pop":
		if len(argNodes) != 0 {
			return "", "", "pop needs 0 arguments"
		}
		// R1 回迁映射：末端取值缩长语义由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_pop` 实现（i32 结果须 saOwnTemp 登记，R1-12 同例）。
		// 0.17 空守卫：sci 0.17 线尚无 main 的 wasm-port 空守卫
		// （空弹返 0），调用点自守卫（`saLowerGuardedMethodCall`
		// L_call_null 槽形；空即 0，非空直调；JS 空弹 undefined→0
		// 子集口径与旧守卫同值）。
		scope.addImport("sa_std/ts_array.sa")
		slot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		plen := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", plen, recv))
		isempty := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		nullL := fmt.Sprintf("L_pop_empty_%d", *scope.nextLabel)
		*scope.nextLabel++
		hasL := fmt.Sprintf("L_pop_has_%d", *scope.nextLabel)
		*scope.nextLabel++
		endL := fmt.Sprintf("L_pop_end_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isempty, plen))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isempty, nullL, hasL))
		w.Write(fmt.Sprintf("%s:\n", nullL))
		w.Write(fmt.Sprintf("  store %s + 0, 0 as i32\n", slot))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", hasL))
		pv := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_pop(%s)\n", pv, recv))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", slot, pv))
		w.Write(fmt.Sprintf("  !%s\n", pv))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
		w.Write(fmt.Sprintf("  !%s\n", slot))
		saOwnTemp(scope, out)
		return out, "i32", ""
	case "shift":
		if len(argNodes) != 0 {
			return "", "", "shift needs 0 arguments"
		}
		// R1 回迁映射：首端取值前移语义由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_shift` 实现（i32 结果须 saOwnTemp 登记，R1-12 同例）。
		// 0.17 空守卫：与 pop 同形（空即 0；shift 空取在双线皆越界，
		// 此处一并收敛，verdict 中性）。
		scope.addImport("sa_std/ts_array.sa")
		sslot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", sslot))
		slen := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", slen, recv))
		sempty := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		snullL := fmt.Sprintf("L_shift_empty_%d", *scope.nextLabel)
		*scope.nextLabel++
		shasL := fmt.Sprintf("L_shift_has_%d", *scope.nextLabel)
		*scope.nextLabel++
		sendL := fmt.Sprintf("L_shift_end_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", sempty, slen))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", sempty, snullL, shasL))
		w.Write(fmt.Sprintf("%s:\n", snullL))
		w.Write(fmt.Sprintf("  store %s + 0, 0 as i32\n", sslot))
		w.Write(fmt.Sprintf("  jmp %s\n", sendL))
		w.Write(fmt.Sprintf("%s:\n", shasL))
		sv := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_shift(%s)\n", sv, recv))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", sslot, sv))
		w.Write(fmt.Sprintf("  !%s\n", sv))
		w.Write(fmt.Sprintf("  jmp %s\n", sendL))
		w.Write(fmt.Sprintf("%s:\n", sendL))
		sout := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", sout, sslot))
		w.Write(fmt.Sprintf("  !%s\n", sslot))
		saOwnTemp(scope, sout)
		return sout, "i32", ""
	case "unshift":
		// 展开形（`a.unshift(0, ...b)`；中转暂存保序 + 逆序单步前插；1398）。
		for _, an := range argNodes {
			if an != nil && an.Kind == ast.KindSpreadElement {
				nlen, msg := saLowerUnshiftSpread(w, recv, pa, argNodes, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				return nlen, "i32", ""
			}
		}
		// 空参即原长（`a.unshift()` 无操作返长，JS 同义；1468）。
		if len(argNodes) < 1 {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, recv))
			return t, "i32", ""
		}
		if len(argNodes) != 1 {
			return "", "", "unshift needs 1 argument"
		}
		v, msg := saArrayLiteralElem(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		nlen := saLowerArrayPush(w, recv, v, scope, nextTemp)
		// R1 回迁映射：右旋一步由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_rotr1` 实现（扩展仍走共享 push；首元回填留调用点）。
		scope.addImport("sa_std/ts_array.sa")
		w.Write(fmt.Sprintf("  call @ts_arr_rotr1(%s)\n", recv))
		data := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", data, v))
		return nlen, "i32", ""
	case "fill":
		if len(argNodes) < 1 || len(argNodes) > 3 {
			return "", "", "fill takes 1-3 arguments"
		}
		v, msg := saArrayLiteralElem(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		if len(argNodes) == 1 {
			// R1 回迁映射：全域值填充语义由 `sci/sa_std/ts_array.sa`
			// `@ts_arr_fill` 实现，本侧只做实参门 + import + 直接调用（无新柄）。
			scope.addImport("sa_std/ts_array.sa")
			w.Write(fmt.Sprintf("  call @ts_arr_fill(%s, %s)\n", recv, v))
			return recv, "arr", ""
		}
		// 起止填充（缺省 0/len；负值 len 起钳 0；超长空扫天然；与 satsgo
		// lowerArrayFill:6257 全域循环同骨架；归属 v 值复用不释）。
		s, msg := saEvalI32(w, argNodes[1], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		if msg := saCheckIntIndex(scope, s); msg != "" {
			return "", "", msg
		}
		e := ""
		if len(argNodes) == 3 {
			var msg string
			e, msg = saEvalI32(w, argNodes[2], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			if msg := saCheckIntIndex(scope, e); msg != "" {
				return "", "", msg
			}
		}
		ln := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
		data := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
		// 全 i32 域（文本长恒域内；`trunc` 窄化唯一，出入显式）。
		slen := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = trunc %s as i32\n", slen, ln))
		// 起位钳位（负 len 起、仍负即 0；超长循环空扫天然）。
		sb := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", sb))
		sneg := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, 0\n", sneg, s))
		snegL := fmt.Sprintf("L_fl_neg_%d", *nextTemp)
		*nextTemp++
		sposL := fmt.Sprintf("L_fl_pos_%d", *nextTemp)
		*nextTemp++
		sjoinL := fmt.Sprintf("L_fl_join_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", sneg, snegL, sposL))
		w.Write(fmt.Sprintf("%s:\n", snegL))
		sadj := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", sadj, s, slen))
		snn := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, 0\n", snn, sadj))
		snnL := fmt.Sprintf("L_fl_nn_%d", *nextTemp)
		*nextTemp++
		szL := fmt.Sprintf("L_fl_z_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", snn, snnL, szL))
		w.Write(fmt.Sprintf("%s:\n", snnL))
		w.Write(fmt.Sprintf("  store %s + 0, 0 as i32\n", sb))
		w.Write(fmt.Sprintf("  jmp %s\n", sjoinL))
		w.Write(fmt.Sprintf("%s:\n", szL))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", sb, sadj))
		w.Write(fmt.Sprintf("  jmp %s\n", sjoinL))
		w.Write(fmt.Sprintf("%s:\n", sposL))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", sb, s))
		w.Write(fmt.Sprintf("  jmp %s\n", sjoinL))
		w.Write(fmt.Sprintf("%s:\n", sjoinL))
		beg := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", beg, sb))
		w.Write(fmt.Sprintf("  !%s\n", sb))
		// 止位钳位（缺省 len；负 len 起、仍负即 0；超长循环空扫天然）。
		fb := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", fb))
		if e == "" {
			w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", fb, slen))
		} else {
			eneg := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, 0\n", eneg, e))
			enegL := fmt.Sprintf("L_fl_eneg_%d", *nextTemp)
			*nextTemp++
			eposL := fmt.Sprintf("L_fl_epos_%d", *nextTemp)
			*nextTemp++
			ejoinL := fmt.Sprintf("L_fl_ejoin_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", eneg, enegL, eposL))
			w.Write(fmt.Sprintf("%s:\n", enegL))
			eadj := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", eadj, e, slen))
			enn := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, 0\n", enn, eadj))
			ennL := fmt.Sprintf("L_fl_enn_%d", *nextTemp)
			*nextTemp++
			ezL := fmt.Sprintf("L_fl_ez_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", enn, ennL, ezL))
			w.Write(fmt.Sprintf("%s:\n", ennL))
			w.Write(fmt.Sprintf("  store %s + 0, 0 as i32\n", fb))
			w.Write(fmt.Sprintf("  jmp %s\n", ejoinL))
			w.Write(fmt.Sprintf("%s:\n", ezL))
			w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", fb, eadj))
			w.Write(fmt.Sprintf("  jmp %s\n", ejoinL))
			w.Write(fmt.Sprintf("%s:\n", eposL))
			w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", fb, e))
			w.Write(fmt.Sprintf("  jmp %s\n", ejoinL))
			w.Write(fmt.Sprintf("%s:\n", ejoinL))
		}
		fin := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", fin, fb))
		w.Write(fmt.Sprintf("  !%s\n", fb))
		// 填充循环（与 satsgo lowerArrayFill:6257 全域循环同骨架；v 值复用不释）。
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", i, beg))
		topL := fmt.Sprintf("L_fl_top_%d", *nextTemp)
		*nextTemp++
		bodyL := fmt.Sprintf("L_fl_body_%d", *nextTemp)
		*nextTemp++
		endL := fmt.Sprintf("L_fl_end_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("%s:\n", topL))
		c1 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c1, i, fin))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c1, bodyL, endL))
		w.Write(fmt.Sprintf("%s:\n", bodyL))
		off := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, i))
		addr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", addr, v))
		inext := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
		inext2 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", inext2, inext))
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext2))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		return recv, "arr", ""
	case "sort", "toSorted":
		if cb, _, msg := saArrCallbackNode(argNodes); msg != "" {
			return "", "", msg
		} else if cb != nil {
			target := recv
			if method == "toSorted" {
				target = saLowerArraySlice(w, recv, "0", "", scope, nextTemp)
			}
			out, kind, msg := saLowerSortWithCmp(w, target, cb, scope, pos, refusals, needImport, nextLabel, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			return out, kind, ""
		}
		if len(argNodes) != 0 {
			return "", "", method + " without a comparator takes 0 arguments"
		}
		// 串元数组字典序原地/拷贝排（`@ts_arr_sort_str`，比较环内联、
		// ts_array.sa 零对外依赖；toSorted 先克隆后排；标记透传/归属同数值径；
		// 数值插入按指针数序错排永禁）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier &&
			scope.arrStr != nil && scope.arrStr[pa.Expression.Text()] {
			scope.addImport("sa_std/ts_array.sa")
			target := recv
			if method == "toSorted" {
				target = fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = call @ts_arr_clone_flat(%s)\n", target, recv))
				saPropArrNest(scope, recv, target)
				saPropArrStr(scope, recv, target)
				saPropArrInnerStr(scope, recv, target)
				saOwnTemp(scope, target)
			}
			w.Write(fmt.Sprintf("  call @ts_arr_sort_str(%s)\n", target))
			return target, "arr", ""
		}
		if method == "sort" {
			// R1 回迁映射：原地数值插入排序语义由 `sci/sa_std/ts_array.sa`
			// `@ts_arr_sort` 实现，本侧只做 import + 直接调用（无新柄）。
			scope.addImport("sa_std/ts_array.sa")
			w.Write(fmt.Sprintf("  call @ts_arr_sort(%s)\n", recv))
			return recv, "arr", ""
		}
		return saLowerToSorted(w, recv, scope, nextTemp), "arr", ""
	case "indexOf", "lastIndexOf", "includes":
		if len(argNodes) < 1 {
			return "", "", method + " needs 1 argument"
		}
		if recvIsStrArray(scope, pa) && saIsStrExpr(argNodes[0], scope) {
			// 串元内容扫描（含 lastIndexOf 逆向；`@ts_str_equals` 内容判等，
			// 地址直比永禁；includes 返 1/0，index 系返位/-1）。
			return saLowerArrayScanStr(w, recv, method, argNodes, scope, pos, refusals, nextLabel, nextTemp)
		}
		want, msg := i32arg(0)
		if msg != "" {
			return "", "", msg
		}
		// 针值记种检查（串/实例句柄禁与 i32 元比较；字面量沿求值门；铁律 4）。
		if msg := saCheckI32Value(scope, want); msg != "" {
			return "", "", msg
		}
		from := ""
		if len(argNodes) > 1 {
			var msg string
			from, msg = i32arg(1)
			if msg != "" {
				return "", "", msg
			}
			// 起始下标记种检查（与 at 同形；铁律 4）。
			if msg := saCheckIntIndex(scope, from); msg != "" {
				return "", "", msg
			}
		}
		reverse := method == "lastIndexOf"
		wantIndex := method != "includes"
		// R1 回迁映射：相等扫描语义由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_scan` 实现（缺省起位/钳位内聚符号内；记种门禁留调用点）。
		fromVal, hasfrom := "0", "0"
		if from != "" {
			fromVal, hasfrom = from, "1"
		}
		rev, wi := "0", "1"
		if reverse {
			rev = "1"
		}
		if !wantIndex {
			wi = "0"
		}
		scope.addImport("sa_std/ts_array.sa")
		dest := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_scan(%s, %s, %s, %s, %s, %s)\n", dest, recv, want, fromVal, hasfrom, rev, wi))
		saOwnTemp(scope, dest)
		return dest, "i32", ""
	case "reverse":
		if len(argNodes) != 0 {
			return "", "", "reverse needs 0 arguments"
		}
		// R1 回迁映射：原地对称交换语义由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_reverse` 实现，本侧只做 import + 直接调用（无新柄）。
		scope.addImport("sa_std/ts_array.sa")
		w.Write(fmt.Sprintf("  call @ts_arr_reverse(%s)\n", recv))
		return recv, "arr", ""
	case "slice":
		start, end := "0", ""
		if len(argNodes) > 0 {
			var msg string
			start, msg = i32arg(0)
			if msg != "" {
				return "", "", msg
			}
			// 切片下标记种检查（与 at 同形；铁律 4）。
			if msg := saCheckIntIndex(scope, start); msg != "" {
				return "", "", msg
			}
		}
		if len(argNodes) > 1 {
			var msg string
			end, msg = i32arg(1)
			if msg != "" {
				return "", "", msg
			}
			// 切片下标记种检查（与 at 同形；铁律 4）。
			if msg := saCheckIntIndex(scope, end); msg != "" {
				return "", "", msg
			}
		}
		if len(argNodes) > 2 {
			return "", "", "slice takes at most 2 arguments"
		}
		return saLowerArraySlice(w, recv, start, end, scope, nextTemp), "arr", ""
	case "flat":
		if len(argNodes) > 1 {
			return "", "", "flat takes at most 1 argument"
		}
		depth := 1
		if len(argNodes) == 1 {
			dn := argNodes[0]
			if dn == nil || dn.Kind != ast.KindNumericLiteral {
				return "", "", "flat depth must be a literal 0 or 1"
			}
			var dv int
			if _, err := fmt.Sscanf(dn.Text(), "%d", &dv); err != nil || (dv != 0 && dv != 1) {
				return "", "", "flat depth must be a literal 0 or 1"
			}
			depth = dv
		}
		return saLowerArrayFlat(w, recv, depth, scope, nextTemp), "arr", ""
	case "keys":
		// `arr.keys()` 下标数组（sci `sa_std/ts_array.sa` `@ts_arr_keys`：
		// 0..len-1 的 i32 下标；新句柄，调用方持有）。
		if len(argNodes) != 0 {
			return "", "", "keys takes no arguments"
		}
		scope.addImport("sa_std/ts_array.sa")
		h := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_keys(%s)\n", h, recv))
		saOwnTemp(scope, h)
		return h, "arr", ""
	case "values":
		// `arr.values()` 值数组（i32 元即原数组浅拷；sci `@ts_arr_clone_flat`
		// 现货，新句柄，串元/嵌套标记透传）。
		if len(argNodes) != 0 {
			return "", "", "values takes no arguments"
		}
		scope.addImport("sa_std/ts_array.sa")
		h := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_clone_flat(%s)\n", h, recv))
		saOwnTemp(scope, h)
		saPropArrNest(scope, recv, h)
		saPropArrStr(scope, recv, h)
		return h, "arr", ""
	case "entries":
		// `arr.entries()` 下标-值对数组（sci `sa_std/ts_array.sa`
		// `@ts_arr_entries`：每元一个 2 元 i32 数组 {i, v}；外层数组存对句柄
		// 地址，与嵌套数组模型同形；对数组由 sci 侧分配（端口无逐对归属问题））。
		if len(argNodes) != 0 {
			return "", "", "entries takes no arguments"
		}
		if (scope.arrStr != nil && scope.arrStr[recv]) || (scope.arrNest != nil && scope.arrNest[recv]) {
			return "", "", "entries needs an i32 array (string or nested elements are not lowerable)"
		}
		scope.addImport("sa_std/ts_array.sa")
		h := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_entries(%s)\n", h, recv))
		saOwnTemp(scope, h)
		if scope.arrNest == nil {
			scope.arrNest = map[string]bool{}
		}
		scope.arrNest[h] = true
		return h, "arr", ""
	case "at":
		if len(argNodes) != 1 {
			return "", "", "at needs 1 argument"
		}
		v, msg := i32arg(0)
		if msg != "" {
			return "", "", msg
		}
		// 下标记种检查（串/实例句柄禁作槽位；与下标读同形；铁律 4）。
		if msg := saCheckIntIndex(scope, v); msg != "" {
			return "", "", msg
		}
		return saLowerArrayAt(w, recv, v, scope, nextTemp), "i32", ""
	case "join":
		// R3-13b 回迁映射：拼接语义由 `sci/sa_std/ts_string.sa`
		// `@ts_arr_join_vals` 实现（分支合并经槽中转 + 自赋值对齐，H-join
		// 旧陷阱已解；缺省分隔符 ","）；本侧只做参数门 + import + 调用 +
		// 归属（分隔符用后释放，结果登记）。
		if len(argNodes) > 1 {
			return "", "", "join takes at most 1 argument"
		}
		sep := saLowerStringLiteral(w, ",", scope, nextTemp)
		if len(argNodes) == 1 {
			ns, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			sep = ns
		}
		sp, sl := saExpandStr(w, sep, nextTemp)
		// 串元数组 join 经 sa_std 分隔 helper（JEV sa_std_sep_helper 1.0）：
		// `@ts_arr_join_str_sep` 循元拼段、元间插分隔符；i32 join 仍沿旧路。
		if _, ok := scope.arrStr[recv]; ok {
			scope.addImport("sa_std/ts_string.sa")
			out := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @ts_arr_join_str_sep(%s, %s, %s)\n", out, recv, sp, sl))
			saReleaseOwnedTemp(w, scope, sep)
			saOwnTemp(scope, out)
			return out, "str", ""
		}
		// 嵌套数组 join 大声拒（内层柄误作 i32 即拼地址：`[[1,2]].join()` 真机
		// 印句柄（1040470688 vs "1,2"）实证；与串元门同律；1418）。
		if scope.arrNest != nil && scope.arrNest[recv] {
			return "", "", "join on nested arrays is not lowerable (join formats flat i32 elements)"
		}
		scope.addImport("sa_std/ts_string.sa")
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_arr_join_vals(%s, %s, %s)\n", out, recv, sp, sl))
		saReleaseOwnedTemp(w, scope, sep)
		saOwnTemp(scope, out)
		return out, "str", ""
	case "copyWithin":
		if len(argNodes) < 1 {
			return "", "", "copyWithin needs 1 argument"
		}
		target, msg := i32arg(0)
		if msg != "" {
			return "", "", msg
		}
		start, end := "0", ""
		if len(argNodes) > 1 {
			var msg string
			start, msg = i32arg(1)
			if msg != "" {
				return "", "", msg
			}
		}
		if len(argNodes) > 2 {
			var msg string
			end, msg = i32arg(2)
			if msg != "" {
				return "", "", msg
			}
		}
		if len(argNodes) > 3 {
			return "", "", "copyWithin takes at most 3 arguments"
		}
		// R1 回迁映射：交叠安全方向拷贝语义由 `sci/sa_std/ts_array.sa`
		// `@ts_arr_copywithin` 实现（钳位内聚符号内；实参门禁留调用点）。
		scope.addImport("sa_std/ts_array.sa")
		endVal, hasend := "0", "0"
		if end != "" {
			endVal, hasend = end, "1"
		}
		w.Write(fmt.Sprintf("  call @ts_arr_copywithin(%s, %s, %s, %s, %s)\n", recv, target, start, endVal, hasend))
		return recv, "arr", ""
	case "toReversed":
		if len(argNodes) != 0 {
			return "", "", "toReversed needs 0 arguments"
		}
		return saLowerToReversed(w, recv, scope, nextTemp), "arr", ""
	case "with":
		if len(argNodes) != 2 {
			return "", "", "with needs 2 arguments"
		}
		idx, msg := i32arg(0)
		if msg != "" {
			return "", "", msg
		}
		val, msg := saArrayLiteralElem(w, argNodes[1], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		cp, msg := saLowerArrayWith(w, recv, idx, val, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		return cp, "arr", ""
	case "toSpliced":
		h, msg := saLowerToSpliced(w, recv, argNodes, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		return h, "arr", ""
	case "splice":
		h, msg := saLowerArraySplice(w, recv, pa, argNodes, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		return h, "arr", ""
	case "concat":
		h, msg := saLowerArrayConcat(w, recv, argNodes, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		return h, "arr", ""
	case "forEach", "map", "filter", "flatMap", "find", "findIndex", "findLast", "findLastIndex", "some", "every":
		cb, _, msg := saArrCallbackNode(argNodes)
		if msg != "" {
			return "", "", msg
		}
		if cb == nil {
			return "", "", method + " needs an inline arrow callback"
		}
		if len(argNodes) != 1 {
			return "", "", method + " takes only a callback"
		}
		if method == "flatMap" {
			h, msg := saHigherOrderFlatMap(w, recv, cb, scope, pos, refusals, needImport, nextLabel, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			return h, "arr", ""
		}
		if method == "map" {
			mapLayout := ""
			if layout, ok := saFindReceiverLayout(pa.Expression, scope); ok {
				mapLayout = layout
			}
			h, msg := saHigherOrderMap(w, recv, cb, scope, pos, refusals, needImport, nextLabel, nextTemp, mapLayout)
			if msg != "" {
				return "", "", msg
			}
			return h, "arr", ""
		}
		out, kind, msg := saHigherOrderScan(w, recv, method, cb, scope, pos, refusals, needImport, nextLabel, nextTemp, func() string {
			if layout, ok := saFindReceiverLayout(pa.Expression, scope); ok {
				return layout
			}
			return ""
		}())
		if msg != "" {
			return "", "", msg
		}
		if method == "filter" {
			saPropArrNest(scope, recv, out)
			saPropArrStr(scope, recv, out)
			saPropArrInnerStr(scope, recv, out)
			saPropArrJsVal(scope, recv, out)
			saPropArrElemLayout(scope, recv, out)
		}
		return out, kind, ""
	case "reduce", "reduceRight":
		right := method == "reduceRight"
		cb, cbIdx, msg := saArrCallbackNode(argNodes)
		if msg != "" {
			return "", "", msg
		}
		if cb == nil {
			return "", "", method + " needs an inline arrow callback"
		}
		if cbIdx != 0 {
			return "", "", method + " needs (callback, init)"
		}
		var initVal string
		hasInit := false
		if len(argNodes) > 1 {
			if argNodes[1] != nil && (argNodes[1].Kind == ast.KindArrowFunction || argNodes[1].Kind == ast.KindFunctionExpression) {
				return "", "", method + " needs (callback, init)"
			}
			v, msg := saEvalI32(w, argNodes[1], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			initVal, hasInit = v, true
		}
		if len(argNodes) > 2 {
			return "", "", method + " takes at most 2 arguments"
		}
		out, kind, msg := saHigherOrderReduce(w, recv, right, cb, initVal, hasInit, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		return out, kind, ""
	default:
		return "", "", "unsupported array method " + method
	}
}

// saLowerArrIn 数组下标 `in`（`k in arr` ⟺ 0<=k<len；稠密子集恒成立：
// 空穴字面量拒收、非零 `length=` 截断沿旧门，故无洞可言；`?.` 沿旧门）。
// 键守卫：null/undefined/布尔/串键/void 在 JS 下标语义下恒 false 或另义，
// 子集 i32 求值会误折为 0/1，一律大声拒；具名须 i32 种（bool/f64/句柄禁入）。
// 发射与 checked-index 同形（`load +8 as u64` 长槽 + `slt`/`sge` + `and`）。
func saLowerArrIn(w printer.EmitTextWriter, be *ast.BinaryExpression, base string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	left := be.Left
	if left == nil {
		return "", "in operator needs a literal key and a known-layout object"
	}
	switch left.Kind {
	case ast.KindNullKeyword, ast.KindUndefinedKeyword,
		ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindVoidExpression,
		ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral,
		ast.KindTemplateExpression, ast.KindTaggedTemplateExpression:
		return "", "array 'in' needs an integer index (string keys and non-index values are not lowerable)"
	case ast.KindIdentifier:
		if k, ok := scope.types[left.Text()]; !ok || k != "i32" {
			return "", "array 'in' needs an integer index (string keys and non-index values are not lowerable)"
		}
	}
	kop, msg := saEvalI32(w, left, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	if msg := saCheckI32Value(scope, kop); msg != "" {
		return "", msg
	}
	lnT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lnT, base))
	c1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sge %s, 0\n", c1, kop))
	c2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c2, kop, lnT))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, %s\n", t, c1, c2))
	return t, ""
}
