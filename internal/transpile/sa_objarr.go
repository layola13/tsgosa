package transpile

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// saTopObjElemParse parses one object literal element of a top-level const
// object array: exactly one RegExp field and any number of string-literal
// fields, no other property kinds, no spread, no methods, no shorthand.
func saTopObjElemParse(el *ast.Node) (saTopObjElem, bool) {
	if el == nil || el.Kind != ast.KindObjectLiteralExpression {
		return saTopObjElem{}, false
	}
	ol := el.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil {
		return saTopObjElem{}, false
	}
	out := saTopObjElem{strFields: map[string]string{}}
	for _, pr := range ol.Properties.Nodes {
		if pr == nil || pr.Kind != ast.KindPropertyAssignment {
			return saTopObjElem{}, false
		}
		fname, ok := saObjPropName(pr)
		if !ok {
			return saTopObjElem{}, false
		}
		init := pr.AsPropertyAssignment().Initializer
		if init == nil {
			return saTopObjElem{}, false
		}
		if init.Kind == ast.KindRegularExpressionLiteral {
			if out.regexField != "" {
				return saTopObjElem{}, false
			}
			pat, flags, ok := saRegexSplitLiteral(init.Text())
			if !ok {
				return saTopObjElem{}, false
			}
			out.regexField = fname
			out.regexPat = pat
			out.regexFlags = flags
			continue
		}
		if init.Kind == ast.KindStringLiteral {
			out.strFields[fname] = init.Text()
			continue
		}
		return saTopObjElem{}, false
	}
	if out.regexField == "" || len(out.strFields) == 0 {
		return saTopObjElem{}, false
	}
	return out, true
}

// saTopObjArrFind parses `TOP.find(cb)?.field` where TOP is a top-level const
// object array snapshot, cb is an inline arrow/function with one parameter,
// and its body is `entry.<regexField>.test(arg)`. It returns the snapshot,
// the requested string field, and the test argument node. It is a pure
// syntactic predicate and emits no SA.
func saTopObjArrFind(pa *ast.PropertyAccessExpression, scope *saScope) (saTopArr, string, *ast.Node, bool) {
	if pa == nil || pa.Name() == nil || pa.QuestionDotToken == nil {
		return saTopArr{}, "", nil, false
	}
	field := pa.Name().Text()
	if pa.Expression == nil || pa.Expression.Kind != ast.KindCallExpression {
		return saTopArr{}, "", nil, false
	}
	ce := pa.Expression.AsCallExpression()
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return saTopArr{}, "", nil, false
	}
	fpa := ce.Expression.AsPropertyAccessExpression()
	if fpa == nil || fpa.Name() == nil || fpa.Name().Text() != "find" || fpa.QuestionDotToken != nil {
		return saTopArr{}, "", nil, false
	}
	if fpa.Expression == nil || fpa.Expression.Kind != ast.KindIdentifier {
		return saTopArr{}, "", nil, false
	}
	arrName := fpa.Expression.Text()
	if _, shadowed := scope.types[arrName]; shadowed {
		return saTopArr{}, "", nil, false
	}
	arr, ok := scope.topArrs[arrName]
	if !ok || len(arr.objs) == 0 {
		return saTopArr{}, "", nil, false
	}
	for _, o := range arr.objs {
		if _, ok := o.strFields[field]; !ok {
			return saTopArr{}, "", nil, false
		}
	}
	if ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 {
		return saTopArr{}, "", nil, false
	}
	cb := saUnwrapTransparent(ce.Arguments.Nodes[0])
	if cb == nil || (cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression) {
		return saTopArr{}, "", nil, false
	}
	params := cb.Parameters()
	if len(params) != 1 {
		return saTopArr{}, "", nil, false
	}
	pd := params[0].AsParameterDeclaration()
	if pd == nil {
		return saTopArr{}, "", nil, false
	}
	pnm := pd.Name()
	if pnm == nil || pnm.Kind != ast.KindIdentifier {
		return saTopArr{}, "", nil, false
	}
	entry := pnm.Text()
	body := cb.Body()
	if body == nil {
		return saTopArr{}, "", nil, false
	}
	var testExpr *ast.Node
	if body.Kind == ast.KindBlock {
		stmts, ok := saBlockStmts(body)
		if !ok || len(stmts) != 1 || stmts[0] == nil || stmts[0].Kind != ast.KindReturnStatement {
			return saTopArr{}, "", nil, false
		}
		rs := stmts[0].AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return saTopArr{}, "", nil, false
		}
		testExpr = rs.Expression
	} else {
		testExpr = body
	}
	testExpr = saUnwrapTransparent(testExpr)
	if testExpr == nil || testExpr.Kind != ast.KindCallExpression {
		return saTopArr{}, "", nil, false
	}
	tce := testExpr.AsCallExpression()
	if tce == nil || tce.Expression == nil || tce.Expression.Kind != ast.KindPropertyAccessExpression {
		return saTopArr{}, "", nil, false
	}
	tpa := tce.Expression.AsPropertyAccessExpression()
	if tpa == nil || tpa.Name() == nil || tpa.Name().Text() != "test" || tpa.QuestionDotToken != nil {
		return saTopArr{}, "", nil, false
	}
	if tpa.Expression == nil || tpa.Expression.Kind != ast.KindPropertyAccessExpression {
		return saTopArr{}, "", nil, false
	}
	epa := tpa.Expression.AsPropertyAccessExpression()
	if epa == nil || epa.Name() == nil || epa.Name().Text() != arr.objs[0].regexField || epa.QuestionDotToken != nil {
		return saTopArr{}, "", nil, false
	}
	if epa.Expression == nil || epa.Expression.Kind != ast.KindIdentifier || epa.Expression.Text() != entry {
		return saTopArr{}, "", nil, false
	}
	if tce.Arguments == nil || len(tce.Arguments.Nodes) != 1 {
		return saTopArr{}, "", nil, false
	}
	arg := tce.Arguments.Nodes[0]
	if arg == nil || !saIsStrExpr(arg, scope) {
		return saTopArr{}, "", nil, false
	}
	return arr, field, arg, true
}
