package transpile

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// saTopObjElemParse parses one object literal element of a top-level const
// object array. Two shapes are accepted, never mixed in one element:
//   - regex shape: exactly one RegExp field and any number of string-literal
//     fields (snapshot-only, lowered at regex-test .find use sites);
//   - numeric shape (JEV synth_inst_array 0.95): at least one string-literal
//     field plus any number of numeric-literal fields (a leading sign folds
//     into the text). No other property kinds, no spread, no methods, no
//     shorthand. Numeric-shape elements are materialized as instances of a
//     synthesized layout, so fractional values keep f64 slots instead of
//     being truncated into i32 slots.
func saTopObjElemParse(el *ast.Node) (saTopObjElem, bool) {
	if el == nil || el.Kind != ast.KindObjectLiteralExpression {
		return saTopObjElem{}, false
	}
	ol := el.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil {
		return saTopObjElem{}, false
	}
	out := saTopObjElem{strFields: map[string]string{}, numFields: map[string]string{}}
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
		if init.Kind == ast.KindNumericLiteral {
			if out.regexField != "" {
				return saTopObjElem{}, false
			}
			out.numFields[fname] = init.Text()
			continue
		}
		if init.Kind == ast.KindPrefixUnaryExpression {
			if un := init.AsPrefixUnaryExpression(); un != nil &&
				(un.Operator == ast.KindMinusToken || un.Operator == ast.KindPlusToken) &&
				un.Operand != nil && un.Operand.Kind == ast.KindNumericLiteral {
				if out.regexField != "" {
					return saTopObjElem{}, false
				}
				sign := "+"
				if un.Operator == ast.KindMinusToken {
					sign = "-"
				}
				out.numFields[fname] = sign + un.Operand.Text()
				continue
			}
		}
		return saTopObjElem{}, false
	}
	if out.regexField != "" {
		if len(out.strFields) == 0 || len(out.numFields) != 0 {
			return saTopObjElem{}, false
		}
		return out, true
	}
	if len(out.strFields) == 0 {
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

// saSynthTopObjLayout builds (or reuses) a synthesized instance layout for a
// numeric-shape top-level object array. Fields arrive in sorted order for a
// deterministic layout name; kinds are str for string fields, f64 for numeric
// fields with any fractional value, i32 otherwise; fields missing from any
// element are optional and zero-filled at materialization. A same-named
// layout with different fields, kinds, or optional flags gets a numeric
// suffix, mirroring the anonymous-layout discipline.
func saSynthTopObjLayout(fields []string, kinds map[string]string, fopt map[string]bool, classes map[string]*saClassDef) string {
	base := "TopObj_" + saMonoName(strings.Join(fields, "_"))
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		if def, ok := classes[name]; ok {
			if len(def.fields) != len(fields) {
				continue
			}
			match := true
			for i, fname := range fields {
				if def.fields[i].name != fname || def.fkinds[fname] != kinds[fname] || def.fopt[fname] != fopt[fname] {
					match = false
					break
				}
			}
			if match {
				return def.name
			}
			continue
		}
		def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fopt: map[string]bool{}}
		off := 0
		for _, fname := range fields {
			fkind := kinds[fname]
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: off})
			def.offsets[fname] = off
			def.fkinds[fname] = fkind
			if fopt[fname] {
				def.fopt[fname] = true
			}
			sz, _ := saFieldWidth(fkind)
			off += sz
		}
		def.size = off
		classes[name] = def
		return def.name
	}
}
