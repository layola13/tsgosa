// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/printer"
	"sort"
	"strings"
)

// sa_expr.go — i32/bool 表达式核与调用总线（step3-11/18；调用分发只认同文件函数，见内注）。
func saLiteralI32(e *ast.Node) (string, bool) {
	if e == nil {
		return "", false
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		return e.Text(), true
	case ast.KindTrueKeyword:
		return "1", true
	case ast.KindFalseKeyword:
		return "0", true
	default:
		return "", false
	}
}

// saBoolSideCond 报告二元条件中布尔类型一侧的变量名（无则 ""）。
// 门禁 TestSATSGoPortNonParamCondRefused 要求此类条件以 condition kind 拒绝。
func saBoolSideCond(cond *ast.Node, scope *saScope) string {
	if cond == nil || cond.Kind != ast.KindBinaryExpression {
		return ""
	}
	be := cond.AsBinaryExpression()
	for _, side := range []*ast.Node{be.Left, be.Right} {
		if side != nil && side.Kind == ast.KindIdentifier && scope.types[side.Text()] == "bool" {
			return side.Text()
		}
	}
	return ""
}

// saCondOperand 求条件操作数：绑定标识符直接用（形状锁）；真/假折 1/0；
// 其余走 saEvalI32（比较等先行发射临时量）。失败返回定位信息。
func saCondOperand(w printer.EmitTextWriter, cond *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if cond == nil {
		return "", "missing condition"
	}
	switch cond.Kind {
	case ast.KindIdentifier:
		nm := cond.Text()
		if k, ok := scope.types[nm]; ok {
			if k == "arr" {
				return "", "array " + nm + " in condition"
			}
			if k == "str" {
				return "", "string " + nm + " in condition"
			}
			if k == "map" || k == "set" {
				return "", k + " " + nm + " in condition"
			}
			// date millis 条件直通（i64 寄存器非零即真；上游实发 `br t` 同形；召用位 3747 收窄口径同源）。
			if k == "date" {
				return nm, ""
			}
			// 实例句柄真值即非零（空为 0 句柄；`br` 直吃寄存器，与上游
			// lowerIf 通用 lowerExpr+br 同形；封存 materializeCond 非立即量直通）。
			if strings.HasPrefix(k, "inst:") {
				return nm, ""
			}
			if k != "i32" && k != "bool" {
				return "", k + " " + nm + " in condition"
			}
			return nm, ""
		}
		if text, ok := scope.topConsts[nm]; ok {
			if scope.topStr[nm] {
				return "", "string " + nm + " in condition"
			}
			return text, ""
		}
		if ms, ok := scope.modVars[nm]; ok && ms.w == "i32" {
			return saModLoadI32(w, ms, scope, nextTemp), ""
		}
		if nm == "undefined" {
			return "0", ""
		}
		return "", "unknown condition variable " + nm
	case ast.KindTrueKeyword:
		return "1", ""
	case ast.KindFalseKeyword:
		return "0", ""
	case ast.KindBinaryExpression:
		// 条件位实例空合同行（`if (a ?? b)`/`while (M[k] ?? null)`：柄非零即真；
		// 与值位同槽同形；须与 sound 形（i32/串拒）同行先判，否则误拒）。
		if be := cond.AsBinaryExpression(); be != nil && be.OperatorToken != nil &&
			be.OperatorToken.Kind == ast.KindQuestionQuestionToken {
			if saIsInstOperandSyntax(be.Left, scope) && (saIsNullLit(be.Right, scope) || saIsInstOperandSyntax(be.Right, scope)) {
				op, msg := saLowerNullishInst(w, be, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				return op, ""
			}
		}
		// 条件位实例空比较（`o === null` 即柄零判，`==/!=` 同形；与 ??
		// 空合同行同门同吸收口径；直读标识符形，调用/字段链沿旧门）。
		if be := cond.AsBinaryExpression(); be != nil && be.OperatorToken != nil &&
			(be.OperatorToken.Kind == ast.KindEqualsEqualsToken ||
				be.OperatorToken.Kind == ast.KindEqualsEqualsEqualsToken ||
				be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
				be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken) {
			cmp := "eq"
			if be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
				be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken {
				cmp = "ne"
			}
			var subj *ast.Node
			if be.Left != nil && be.Left.Kind == ast.KindIdentifier &&
				saIsInstOperandSyntax(be.Left, scope) && saIsNullLit(be.Right, scope) {
				subj = be.Left
			} else if be.Right != nil && be.Right.Kind == ast.KindIdentifier &&
				saIsInstOperandSyntax(be.Right, scope) && saIsNullLit(be.Left, scope) {
				subj = be.Right
			}
			if subj != nil {
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, 0\n", t, cmp, subj.Text()))
				return t, ""
			}
		}
		op, msg := saEvalI32(w, cond, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// 串值条件禁入（空串 falsy 而头指针恒真；与串绑定同门；实例沿直通口径）。
		if k, ok := scope.types[op]; ok && k == "str" {
			return "", "string value in condition"
		}
		return op, ""
	default:
		op, msg := saEvalI32(w, cond, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// 串值条件禁入（空串 falsy 而头指针恒真；与串绑定同门；实例沿直通口径）。
		if k, ok := scope.types[op]; ok && k == "str" {
			return "", "string value in condition"
		}
		return op, ""
	}
}

// saCondOperandMat 条件物化：纯整数字面操作数经 ne 0 入临时量再 br
// （形状证据：封存 materializeCond:1686-1704；裸 `br 1` 真机 UnknownRegister，110_while_break 实证；调用点覆盖 while/for/do/if/三元，与封存 5 处同位）。
func saCondOperandMat(w printer.EmitTextWriter, cond *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	op, msg := saCondOperand(w, cond, scope, pos, refusals, nextTemp)
	if msg != "" || op == "" {
		return op, msg
	}
	isInt := true
	for i := 0; i < len(op); i++ {
		c := op[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if i == 0 && c == '-' && len(op) > 1 {
			continue
		}
		isInt = false
		break
	}
	if !isInt {
		return op, ""
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", t, op))
	return t, ""
}

// saBinaryOpKind 空安全取二元操作符（parser 常保非空，防御备用）。
func saBinaryOpKind(be *ast.BinaryExpression) ast.Kind {
	if be == nil || be.OperatorToken == nil {
		return ast.KindUnknown
	}
	return be.OperatorToken.Kind
}

// saEvalReturnOperand 按函数返回种求 return 操作数：boolean 函数走 saEvalBool
// （bool 标识符直用，其余 0/1 操作数），number 函数走 saEvalI32；数组无返回位。
func saEvalReturnOperand(w printer.EmitTextWriter, e *ast.Node, retKind string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "arr" {
			// 数组绑定直传（`return a`；非数组函数沿旧门）。
			if retKind == "arr" {
				return e.Text(), ""
			}
			return "", "array return not supported"
		}
		if k, ok := scope.types[e.Text()]; ok && k == "str" && retKind != "string" {
			return "", "string return needs string annotation"
		}
		// 实例返回：具名同种句柄直传（`return p`；封存 lowerReturn 值位同形；
		// 种错配沿既有 i32 门拒，fresh temp 非调用源沿旧门拒）。
		if strings.HasPrefix(retKind, "inst:") {
			if k, ok := scope.types[e.Text()]; ok && k == retKind {
				return e.Text(), ""
			}
			return "", "struct return needs matching struct value"
		}
		// Map 返回：同种句柄直传（`return m`；字面量仍须声明绑定，沿旧门）。
		if retKind == "map" {
			if k, ok := scope.types[e.Text()]; ok && k == "map" {
				return e.Text(), ""
			}
			return "", "map return needs matching map value"
		}
	}
	// 数组返回：同种句柄直传（`return a` 见上；字面量/数组调用经句柄通道；
	// 注解擦除位：串值具化柄、i32 值直通（上游 `return 5`/`return t_2` 同形）；
	// map/inst 返回同律）。
	if retKind == "arr" {
		if e != nil && e.Kind == ast.KindIdentifier {
			if k, ok := scope.types[e.Text()]; ok && k == "arr" {
				return e.Text(), ""
			}
			return "", "array return needs matching array value"
		}
		if h, msg := saArrValueOf(w, e, scope, pos, refusals, nextTemp); msg == "" {
			return h, ""
		}
		if saIsStrValue(e, scope) {
			return saEvalStr(w, e, scope, pos, refusals, nextTemp)
		}
		return saEvalI32(w, e, scope, pos, refusals, nextTemp)
	}
	// 返回位字面量具化（`return {...}` 配注解接口布局；封存 checker_layout l1）。
	if e != nil && e.Kind == ast.KindObjectLiteralExpression && strings.HasPrefix(retKind, "inst:") {
		if def, ok := scope.classes[retKind[5:]]; ok && def.isIface {
			h, _, msg := saLowerObjectLiteral(w, e, retKind[5:], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			return h, ""
		}
		return "", "struct return needs a recorded interface layout"
	}
	// 实例新建直返（`return new D()` 进 `(): B` 注解位：绑定跟初值句柄，与声明位 step395/实参位 step396 同规；封存 lowerReturn 无检查直返，用点经布局表大声拒；新柄已在 saLowerNewClass 内登记归属，返前释放除外口守住）。
	if strings.HasPrefix(retKind, "inst:") && e != nil && e.Kind == ast.KindNewExpression {
		ne := e.AsNewExpression()
		if ne != nil && ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier {
			if _, ok := scope.classes[ne.Expression.Text()]; ok {
				h, msg := saLowerNewClass(w, ne.Expression.Text(), ne, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				return h, ""
			}
		}
	}
	// 实例空合空回退（`return M[k] ?? null`；左须同布局实例源，右须空字面量；
	// 封存 lowerBinary:3182-3206 通用槽形，本仓只收空右臂，余形由槽内拒）。
	if strings.HasPrefix(retKind, "inst:") && e != nil && e.Kind == ast.KindBinaryExpression {
		if be := e.AsBinaryExpression(); be != nil && be.OperatorToken != nil &&
			be.OperatorToken.Kind == ast.KindQuestionQuestionToken {
			return saLowerNullishInstNull(w, be, retKind, scope, pos, refusals, nextTemp)
		}
	}
	if retKind == "boolean" {
		return saEvalBool(w, e, scope, pos, refusals, nextTemp)
	}
	if retKind == "string" {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	// f64 返回位经严格求值（字面量/绑定/纯浮点算术；余形大声拒）。
	if retKind == "f64" {
		return saEvalF64(w, e, scope, pos, refusals, nextTemp)
	}
	// f64 bindings pass through (i32-annotated functions returning float bits; cf loose returns).
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "f64" {
			return e.Text(), ""
		}
	}
	op, msg := saEvalI32(w, e, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	// 返回 number 位记种检查（串/实例句柄禁入；与声明/实参位同形；铁律 4）。
	if msg := saCheckI32Value(scope, op); msg != "" {
		return "", msg
	}
	return op, ""
}

// saEvalBool 求布尔操作数（0/1 表示与 i32 统一）：绑定 bool 标识符直用，
// 其余走 saEvalI32（字面/比较/`!`/调 bool 函数皆产 0/1 操作数）。
func saEvalBool(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e != nil && e.Kind == ast.KindIdentifier {
		nm := e.Text()
		if k, ok := scope.types[nm]; ok && k == "bool" {
			return nm, ""
		}
	}
	return saEvalI32(w, e, scope, pos, refusals, nextTemp)
}

// saIsFloatLit 粗判浮点数字面（封存 lowerExpr:2725 按 isFloatLiteral 分 f64/i32）。
func saIsFloatLit(text string) bool {
	for i := 0; i < len(text); i++ {
		if c := text[i]; c == '.' || c == 'e' || c == 'E' {
			return true
		}
	}
	return false
}

// saSpreadCallArgs 展开定元 spread 调用（单尾 spread；静态部按位求值；
// 余位经越界归零 join 填齐；形状证据：封存 resolveSpreadCall:7764-7798）。
// 返回 (args, msg, handled)：无 spread 即 handled=false。
func saSpreadCallArgs(w printer.EmitTextWriter, name string, nodes []*ast.Node, sig saFuncSig, evalOne func(int, *ast.Node, int) (string, string), scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) ([]string, string, bool) {
	spreadAt := -1
	for i, a := range nodes {
		if a != nil && a.Kind == ast.KindSpreadElement {
			if spreadAt >= 0 {
				return nil, "spread call supports only one trailing spread", true
			}
			spreadAt = i
		}
	}
	if spreadAt < 0 {
		return nil, "", false
	}
	if spreadAt != len(nodes)-1 {
		return nil, "spread call supports only one trailing spread", true
	}
	if len(sig.paramKinds) != sig.params {
		return nil, "spread call needs a known callee arity", true
	}
	nStatic := spreadAt
	if nStatic > sig.params {
		return nil, "too many arguments in call to " + name, true
	}
	se := nodes[spreadAt].AsSpreadElement()
	arr, msg := saArrValueOf(w, se.Expression.AsNode(), scope, pos, refusals, nextTemp)
	if msg != "" {
		return nil, msg, true
	}
	var args []string
	for i := 0; i < nStatic; i++ {
		op, msg := evalOne(i, nodes[i], sig.params)
		if msg != "" {
			return nil, msg, true
		}
		args = append(args, op)
	}
	for j := nStatic; j < sig.params; j++ {
		if sig.paramKinds[j] != "i32" {
			return nil, "spread fills i32 parameters only", true
		}
		args = append(args, saLowerCheckedIndex(w, arr, fmt.Sprintf("%d", j-nStatic), scope.nextLabel, nextTemp))
	}
	return args, "", true
}

// saLowerAllocCall 落裸 alloc(N) 原语（单参；参经 i32 求值；结果为新鲜归属句柄
// temp（种记 arr，本仓 ptr 句柄种）；用户自定 alloc 遮蔽时调用方走原路；
// 形状证据：封存 lowerCall:3861-3868 alloc 单参直通 + saNameOfType:197-199
// 用户类型落 ptr 句柄 + widthOf 默认 8,8）。
func saLowerAllocCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 1 {
		return "", false, "alloc takes one size argument"
	}
	sz, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %s\n", t, sz))
	saOwnTemp(scope, t)
	scope.types[t] = "arr"
	return t, false, ""
}

// saNsUnknownMemberMsg 对已知命名空间的未知成员报上游逐字拒因
// （`N.bogus is not exported by its module`；封存 NamespaceMemberImport /
// NestedNamespaceImport 坏例同文；前缀无成员沿旧门）。
func saNsUnknownMemberMsg(scope *saScope, pa *ast.PropertyAccessExpression) string {
	if scope == nil || pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return ""
	}
	var segs []string
	cur := pa.Expression
	for cur != nil && cur.Kind == ast.KindPropertyAccessExpression {
		ca := cur.AsPropertyAccessExpression()
		if ca == nil || ca.Name() == nil || ca.Name().Kind != ast.KindIdentifier {
			return ""
		}
		segs = append([]string{ca.Name().Text()}, segs...)
		cur = ca.Expression
	}
	if cur == nil || cur.Kind != ast.KindIdentifier {
		return ""
	}
	root := cur.Text()
	segs = append(append([]string{root}, segs...), pa.Name().Text())
	if len(segs) < 2 {
		return ""
	}
	parent := strings.Join(segs[:len(segs)-1], ".")
	if _, bound := scope.types[root]; bound {
		return ""
	}
	if _, isClass := scope.classes[root]; isClass {
		return ""
	}
	if !saIsNsAliasSrc(scope, parent) {
		return ""
	}
	return strings.Join(segs, ".") + " is not exported by its module"
}

// saNsCallChain 把 `N.M.g` 多级点调用展平为点键/发射键（两级以下 false，
// 单级走既有一点分支；段全标识才展，动态链沿旧门）。
func saNsCallChain(e *ast.Node) (dotted, emit, root string, ok bool) {
	if e == nil || e.Kind != ast.KindPropertyAccessExpression {
		return "", "", "", false
	}
	var segs []string
	cur := e
	for cur != nil && cur.Kind == ast.KindPropertyAccessExpression {
		pa := cur.AsPropertyAccessExpression()
		if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			return "", "", "", false
		}
		segs = append([]string{pa.Name().Text()}, segs...)
		cur = pa.Expression
	}
	if cur == nil || cur.Kind != ast.KindIdentifier || len(segs) < 2 {
		return "", "", "", false
	}
	segs = append([]string{cur.Text()}, segs...)
	return strings.Join(segs, "."), strings.Join(segs, "_"), cur.Text(), true
}

// saLowerIsNaNFinite `isNaN`/`isFinite` 恒判定（`Number.` 成员与裸全局同形，
// lib.es5.d.ts 别名；i32 子集无 NaN/Inf：浮点字面量早拒，除零走 verifier trap；
// 串实参恒 false（JS 语义非 Number 即 false）；先判定后求值，单次求值保副作用）。
func saLowerIsNaNFinite(w printer.EmitTextWriter, label string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	isNaN := len(label) >= 5 && label[len(label)-5:] == "isNaN"
	bare := label == "isNaN" || label == "isFinite"
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 1 {
		return "", false, label + " takes one argument"
	}
	if argNodes[0] != nil && saIsStrExpr(argNodes[0], scope) {
		// 裸全局串参先拒（coerce 经 Number() 回 NaN/f64，薄口无 NaN 种；
		// `Number.isNaN/isFinite` 不 coerce，串恒 false 正确）。
		if bare {
			return "", false, label + " on strings needs numeric coercion (not in the i32 subset; use Number.isNaN/isFinite for strict checks)"
		}
		if _, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp); msg != "" {
			return "", false, msg
		}
		return "0", false, ""
	}
	if _, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp); msg != "" {
		return "", false, msg
	}
	if isNaN {
		return "0", false, ""
	}
	return "1", false, ""
}

// saLowerJSONStringify `JSON.stringify` 标量形（writer 直写后 buffer 拷出
// 具化新串头；i32→i64、bool、串、null；数组/对象另步；status 码沿既有
// extern 惯例忽略，见 test free 形）。
func saLowerJSONStringify(w printer.EmitTextWriter, a *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	if a == nil {
		return "", false, "JSON.stringify takes one argument"
	}
	scope.addImport("sa_std/encoding/json.sai")
	wh := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", wh))
	st := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_json_writer_new(0, 0, 0, 0, 0, &%s)\n", st, wh))
	wr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", wr, wh))
	w.Write(fmt.Sprintf("  !%s\n", wh))
	w.Write(fmt.Sprintf("  !%s\n", st))
	writeV := func(line string) string {
		s := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call %s\n", s, line))
		w.Write(fmt.Sprintf("  !%s\n", s))
		return s
	}
	switch {
	case a.Kind == ast.KindNullKeyword || a.Kind == ast.KindUndefinedKeyword:
		writeV(fmt.Sprintf("@sa_json_writer_write_null(%s)", wr))
	case a.Kind == ast.KindTrueKeyword:
		writeV(fmt.Sprintf("@sa_json_writer_write_bool(%s, 1)", wr))
	case a.Kind == ast.KindFalseKeyword:
		writeV(fmt.Sprintf("@sa_json_writer_write_bool(%s, 0)", wr))
	case saIsStrExpr(a, scope):
		h, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		p, l := saExpandStr(w, h, nextTemp)
		writeV(fmt.Sprintf("@sa_json_writer_write_string(%s, &%s, %s)", wr, p, l))
	default:
		v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, "JSON.stringify takes an i32/string/bool/null value (arrays/objects are not lowerable yet)"
		}
		wide := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sext %s as i64\n", wide, v))
		writeV(fmt.Sprintf("@sa_json_writer_write_i64(%s, %s)", wr, wide))
	}
	oh := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", oh))
	fs := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_json_writer_finish(%s, &%s)\n", fs, wr, oh))
	w.Write(fmt.Sprintf("  !%s\n", fs))
	buf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", buf, oh))
	w.Write(fmt.Sprintf("  !%s\n", oh))
	bd := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_json_buffer_data(%s)\n", bd, buf))
	bl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_json_buffer_len(%s)\n", bl, buf))
	// buffer 内容拷出后释放（串头存悬垂指针永禁）。
	empty := saLowerStringLiteral(w, "", scope, nextTemp)
	bh := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", bh))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", bh, bd))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", bh, bl))
	w.Write(fmt.Sprintf("  !%s\n", bd))
	w.Write(fmt.Sprintf("  !%s\n", bl))
	saOwnTemp(scope, bh)
	out := saConcatSlices(w, empty, bh, scope, nextTemp)
	bfr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_json_buffer_free(^%s)\n", bfr, buf))
	w.Write(fmt.Sprintf("  !%s\n", bfr))
	fr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_json_writer_free(^%s)\n", fr, wr))
	w.Write(fmt.Sprintf("  !%s\n", fr))
	return out, false, ""
}

// saLowerBooleanArg `Boolean(x)` 真值（字面量折叠 + 具名读；调用形返
// done=false 由调用方拒；读无副作用形不落字直接折叠）。
func saLowerBooleanArg(w printer.EmitTextWriter, a *ast.Node, scope *saScope, nextTemp *int) (string, bool, string) {
	if a == nil {
		return "", false, "Boolean takes one argument"
	}
	switch a.Kind {
	case ast.KindNullKeyword:
		return "0", true, ""
	case ast.KindTrueKeyword:
		return "1", true, ""
	case ast.KindFalseKeyword:
		return "0", true, ""
	case ast.KindNumericLiteral:
		if a.Text() == "0" {
			return "0", true, ""
		}
		return "1", true, ""
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		if len(a.Text()) == 0 {
			return "0", true, ""
		}
		return "1", true, ""
	case ast.KindVoidExpression:
		return "0", true, ""
	case ast.KindIdentifier:
		if a.Text() == "undefined" {
			if _, ok := scope.types["undefined"]; !ok {
				return "0", true, ""
			}
		}
		k, ok := scope.types[a.Text()]
		if !ok {
			return "", false, "Boolean of unbound name " + a.Text()
		}
		switch {
		case k == "i32" || k == "bool":
			out := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = ne %s, 0\n", out, a.Text()))
			return out, true, ""
		case k == "str":
			_, l := saExpandStr(w, a.Text(), nextTemp)
			out := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = ne %s, 0\n", out, l))
			return out, true, ""
		case k == "arr" || k == "map" || k == "set" || k == "date" || k == "regex" || strings.HasPrefix(k, "inst:"):
			// 对象/句柄恒真（读无副作用，不落字）。
			return "1", true, ""
		default:
			return "", false, "Boolean of " + k + " is not lowerable yet"
		}
	}
	return "", false, ""
}

func saEvalCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	if m, ok := saMathMethodName(ce.Expression); ok {
		return saEvalMathMethod(w, m, ce, scope, pos, refusals, nextTemp)
	}
	// Object.is(a, b) i32 恒等（子集无 NaN/±0 区分；双求值保副作用）。
	if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Object" &&
			pa.Name() != nil && pa.Name().Text() == "is" {
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			if len(argNodes) != 2 {
				return "", false, "Object.is takes two arguments"
			}
			a, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			b, msg := saEvalI32(w, argNodes[1], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = eq %s, %s\n", t, a, b))
			return t, false, ""
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Object" &&
			pa.Name() != nil && pa.Name().Text() == "hasOwn" {
			// `Object.hasOwn(o, k)` 与 `in` 同门（字面量键 + 已知布局即静态折叠；
			// 变量键/未知布局大声拒，禁运行时臆测）。
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			if len(argNodes) != 2 {
				return "", false, "Object.hasOwn takes two arguments"
			}
			obj := argNodes[0]
			key := argNodes[1]
			if obj == nil || obj.Kind != ast.KindIdentifier || key == nil || key.Kind != ast.KindStringLiteral {
				return "", false, "Object.hasOwn needs a known-layout object and a literal key"
			}
			verdict, ok := saLayoutHasKey(key.Text(), obj.Text(), scope)
			if !ok {
				return "", false, "Object.hasOwn needs a known-layout object and a literal key"
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = %s\n", t, verdict))
			return t, false, ""
		}
	}
	if saIsConsoleLog(ce) {
		ok, msg := saLowerConsoleLog(w, ce, scope, pos, refusals, nextTemp)
		if !ok {
			return "", false, msg
		}
		return "", true, ""
	}
	if ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "String" {
		return saLowerStrCall(w, ce, scope, pos, refusals, nextTemp)
	}
	if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" {
			return saLowerStrCall(w, ce, scope, pos, refusals, nextTemp)
		}
		// 命名空间成员直调（`U.add(..)` 经 hook B 逐成员绑定走既有命名调用；
		// 上游 link_namespace.go importEnv 点键同形；非链接点键下探旧门）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
			if dotted, linked := saLinkCallee(scope, pa.Expression.Text()+"."+pa.Name().Text()); linked {
				return saEvalNamedCall(w, dotted, ce, scope, pos, refusals, nextTemp)
			}
			// 单文件命名空间成员直调（`N.f(..)`；发射名 `N_f`，签名走 `N.f`
			// 双键；基名被值绑定/类占用时让路既有分发；形状证据：封存
			// lowerNamespaceCall + 预扫二b 双键）。
			base, member := pa.Expression.Text(), pa.Name().Text()
			if _, ok := scope.funcs[base+"."+member]; ok {
				if _, bound := scope.types[base]; !bound {
					if _, isClass := scope.classes[base]; !isClass {
						return saEvalNamedCall(w, base+"_"+member, ce, scope, pos, refusals, nextTemp)
					}
				}
			}
		}
		// 多级命名空间直调（`N.M.g(..)`；链展平查 `N.M.g`，发射 `N_M_g`；
		// 根基名守卫同单级；形状证据：封存 nestedNamespace 点键同形）。
		if dotted, emit, root, ok := saNsCallChain(ce.Expression); ok {
			if q, linked := saLinkCallee(scope, dotted); linked {
				return saEvalNamedCall(w, q, ce, scope, pos, refusals, nextTemp)
			}
			if _, ok := scope.funcs[dotted]; ok {
				if _, bound := scope.types[root]; !bound {
					if _, isClass := scope.classes[root]; !isClass {
						return saEvalNamedCall(w, emit, ce, scope, pos, refusals, nextTemp)
					}
				}
			}
		}
		if pa.Name() != nil && pa.Name().Text() == "split" && pa.QuestionDotToken == nil &&
			saIsStrExpr(pa.Expression, scope) {
			// 串 `split(sep)` 回串元数组（`saLowerStringSplit`；封存 lowerStringSplit
			// 全形；split 不在 saIsStrMethod 集，沿旧门会落未知拒）。
			op, msg := saLowerStringSplit(w, ce, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, false, ""
		}
		if pa.Name() != nil && saIsStrMethod(pa.Name().Text()) && saIsStrExpr(pa.Expression, scope) {
			return saLowerStrCall(w, ce, scope, pos, refusals, nextTemp)
		}
		// i32 `toString()`（`String(x)` interp 同形；bool 拼写殊形由
		// saIsToStringableI32 拒；串基沿上门）。
		if pa.Name() != nil && pa.Name().Text() == "toString" && pa.QuestionDotToken == nil &&
			saIsToStringableI32(pa.Expression, scope) {
			return saLowerStrCall(w, ce, scope, pos, refusals, nextTemp)
		}
		// 数组成员调用与 Array.from（基为数组位；其余成员拒）。
		// 管线经 scope 内取（addImport/nextLabel 已随 scope 走，无需改签名）。
		if pa.Name() != nil {
			m := pa.Name().Text()
			if saIsArrMethod(m) && saIsArrValue(pa.Expression, scope) {
				op, kind, msg := saLowerArrCall(w, ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				_ = kind
				return op, false, ""
			}
			if m == "from" && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" {
				op, kind, msg := saLowerArrCall(w, ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				_ = kind
				return op, false, ""
			}
			// Array.isArray(x) 种判定（数组即 1，串/i32 即 0；先判定后求值，
			// 单次求值保副作用；未知种大声拒，禁指针误判）。
			if m == "isArray" && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" {
				var argNodes []*ast.Node
				if ce.Arguments != nil {
					argNodes = ce.Arguments.Nodes
				}
				if len(argNodes) != 1 {
					return "", false, "Array.isArray takes one argument"
				}
				arg := argNodes[0]
				if saIsArrValue(arg, scope) {
					if _, msg := saArrValueOf(w, arg, scope, pos, refusals, nextTemp); msg != "" {
						return "", false, msg
					}
					return "1", false, ""
				}
				if arg != nil && saIsStrExpr(arg, scope) {
					if _, msg := saEvalStr(w, arg, scope, pos, refusals, nextTemp); msg != "" {
						return "", false, msg
					}
					return "0", false, ""
				}
				if arg != nil && arg.Kind == ast.KindNumericLiteral {
					return "0", false, ""
				}
				if arg != nil && arg.Kind == ast.KindIdentifier {
					if k, ok := scope.types[arg.Text()]; ok && (k == "i32" || k == "bool") {
						if _, msg := saEvalI32(w, arg, scope, pos, refusals, nextTemp); msg != "" {
							return "", false, msg
						}
						return "0", false, ""
					}
				}
				// 余形试 i32 求值（成则恒非数组即 0；前序判定皆语法级零落字，
				// 此为首次求值无双副作用；败则透拒因）。
				if _, msg := saEvalI32(w, arg, scope, pos, refusals, nextTemp); msg != "" {
					return "", false, msg
				}
				return "0", false, ""
			}
		}
		// super.m() 内联基方法（同接收者；形状证据：封存 lowerSuperMethodCall:237-250）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindSuperKeyword && pa.Name() != nil {
			bdef, h, msg := saSuperBase(scope)
			if msg != "" {
				return "", false, msg
			}
			op, msg := saInlineMethod(w, h, bdef, pa.Name().Text(), ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, false, ""
		}
		if pa.Name() != nil && saCouldBeInst(pa.Expression, scope) {
			h, def, msg := saInstBase(pa.Expression, scope)
			if msg != "" {
				return "", false, msg
			}
			// map 索引实例基（`m[k].m()`；saInstBase 只认标识符/this；
			// 与 i32 读位 2450-2459 同形；否则 nil 解引用崩溃）。
			if def == nil && pa.Expression != nil && (pa.Expression.Kind == ast.KindElementAccessExpression || pa.Expression.Kind == ast.KindCallExpression || pa.Expression.Kind == ast.KindNewExpression) {
				h, def, msg = saInstBaseElem(w, pa.Expression, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
			}
			if def == nil {
				return "", false, "instance base did not resolve to a recorded layout"
			}
			// this.函数字段去虚化（`this.pick(e)` 回放实例捕获箭头，先于方法分发；
			// 形状证据：封存 lowerPropertyCall:4269-4280）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword && pa.Name() != nil {
				if tbl, ok := scope.instFn[h]; ok {
					if anode, ok := tbl[pa.Name().Text()]; ok {
						op, msg := saInlineInstanceCallback(w, anode, ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
						if msg != "" {
							return "", false, msg
						}
						return op, false, ""
					}
				}
			}
			// `?.` 方法守卫（`b.m?.()`/`b?.m()` 空基即 0；串返回/未知方法沿旧直调门；
			// 回调表已在上分支直通。封存 lowerGuardedCall:4108 槽形）。
			if pa.Name() != nil && (ce.QuestionDotToken != nil || pa.QuestionDotToken != nil) {
				if mn, ok := def.methods[pa.Name().Text()]; ok {
					if k, kok := saMethodReturnKind(mn); !kok || k != "str" {
						op, msg := saLowerGuardedMethodCall(w, h, def, pa.Name().Text(), ce, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", false, msg
						}
						return op, false, ""
					}
				}
			}
			op, msg := saInlineMethod(w, h, def, pa.Name().Text(), ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, false, ""
		}
		// `f.call(thisArg, ...args)` 脱糖为直调（首参即 thisArg，与显式 self
		// 惯例一致；实例自有 `call` 方法已在上分支分发；未知被调继续下探；
		// 形状证据：封存 lowerCallDesugar:4281-4292+4512-4581）。
		if pa.Name() != nil && pa.Name().Text() == "call" && pa.QuestionDotToken == nil &&
			pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			recv := pa.Expression.Text()
			if _, ok := scope.funcs[recv]; ok {
				if _, shadowed := scope.types[recv]; shadowed {
					return "", false, recv + " is not a function"
				}
				return saEvalNamedCall(w, recv, ce, scope, pos, refusals, nextTemp)
			}
		}
		// `C.m()` 内联静态方法（无实例，this 置空使实例态诚实拒；局部/
		// 函数遮蔽类绑定时走原路，未知静态落 loud 拒；`#` 私名走私域门；
		// 形状证据：封存 lowerClassStaticCall 分发位）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil &&
			!strings.HasPrefix(pa.Name().Text(), "#") {
			if def, ok := scope.classes[pa.Expression.Text()]; ok {
				if _, shadowed := scope.types[pa.Expression.Text()]; !shadowed {
					if _, shadowed := scope.funcs[pa.Expression.Text()]; !shadowed {
						op, msg := saInlineStaticMethod(w, pa.Expression.Text(), def, pa.Name().Text(), ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
						if msg != "" {
							return "", false, msg
						}
						return op, false, ""
					}
				}
			}
		}
		// Map/Set 成员调用（基为 map/set 绑定；未知成员由总线定位）。
		if pa.Name() != nil {
			if kind, ok := saMapBaseKind(pa.Expression, scope); ok {
				op, _, msg := saLowerMapCall(w, pa.Expression.Text(), kind, pa.Name().Text(), ce, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				return op, false, ""
			}
		}
		// crypto Hash 累加器调用（`h.update/digest`；声明收养外一律 loud 拒；
		// 块出残留以 types==str 守卫；形状证据：封存调用点 :4466）。
		if pa.Name() != nil && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			if k, ok := scope.types[pa.Expression.Text()]; ok && k == "str" {
				if st, ok := scope.hashAcc[pa.Expression.Text()]; ok {
					op, msg := saLowerHashCall(w, pa.Expression.Text(), st, pa.Name().Text(), ce, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", false, msg
					}
					return op, false, ""
				}
			}
		}
		// RegExp.test(串)→i32（POSIX-ERE 投影，见 sa_date.go；.exec 另步）。
		if pa.Name() != nil {
			if _, ok := saRegexCallKind(ce, scope); ok {
				op, _, msg := saLowerRegexCall(w, ce, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				return op, false, ""
			}
		}
		// Number.isInteger/isSafeInteger(x)：i32 操作数恒整/恒安全（求值保留
		// 副作用后折 "1"；形状证据：封存 lowerCall:3837-3848）。
		if saIsNumberIsInteger(ce) {
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			if len(argNodes) != 1 {
				return "", false, "Number.isInteger/isSafeInteger takes one argument"
			}
			if _, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp); msg != "" {
				return "", false, msg
			}
			return "1", false, ""
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Number" {
			// Number.parseFloat 与裸 parseFloat 同形（lib.es5.d.ts 别名；sa_std/string.sai
			// sa_parse_float 回 f64，投影表 stdlib.go:106 在册；上游发射器未路由，
			// 本仓 thin-lead，见 step373）。
			if pa.Name() != nil && pa.Name().Text() == "parseFloat" {
				op, msg := saLowerFloatConvert(w, "Number.parseFloat", ce, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				return op, false, ""
			}
			// saLowerIsNaNFinite 恒判定 helper（成员/裸全局共享，见下）。
			if pa.Name() != nil && (pa.Name().Text() == "isNaN" || pa.Name().Text() == "isFinite") {
				return saLowerIsNaNFinite(w, "Number."+pa.Name().Text(), ce, scope, pos, refusals, nextTemp)
			}
			if pa.Name() != nil && pa.Name().Text() == "parseInt" {
				var argNodes []*ast.Node
				if ce.Arguments != nil {
					argNodes = ce.Arguments.Nodes
				}
				op, msg := saLowerParseIntArgs(w, argNodes, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				return op, false, ""
			}
			return "", false, "unknown Number member"
		}
		// Date 调用（静态 now/parse 与 date 绑定方法；种由调用方判定）。
		if _, ok := saDateCallKind(ce, scope); ok {
			op, _, msg := saLowerDateCall(w, ce, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, false, ""
		}
		// node/deno 插件裸全局与命名空间方法（复用插件轮子；无 import
		// 亦可（Node 全局暴露）；形状证据：封存 lowerPropertyCall:4294-4310、
		// node_console.go 全文件、node_buffer.go 全文件、node_deno.go 全文件）。
		// Deno.env 两级链的基为 PropertyAccess，整 pa 下探。
		if pa.Name() != nil {
			if op, voidCall, msg, handled := saLowerNodeMethodCall(w, pa, ce, scope, pos, refusals, nextTemp); handled {
				return op, voidCall, msg
			}
		}
		// JSON.stringify 标量形（`sa_json_writer_*` 直写：i32→i64、bool、串、
		// null；数组/对象另步；返新串柄）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "JSON" &&
			pa.Name() != nil && pa.Name().Text() == "stringify" {
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			if len(argNodes) != 1 {
				return "", false, "JSON.stringify takes one argument"
			}
			return saLowerJSONStringify(w, argNodes[0], scope, pos, refusals, nextTemp)
		}
		// 已知命名空间的未知成员逐字拒因（`N.M.bogus is not exported...`）。
		if msg := saNsUnknownMemberMsg(scope, pa); msg != "" {
			return "", false, msg
		}
		return "", false, "only direct function calls lowerable"
	}
	// 裸 alloc(N) 原语单参直通（`t = alloc N` 绑 ptr 句柄；用户自定 alloc
	// 遮蔽时走原命名调用路；形状证据：封存 lowerCall:3861-3868）。
	if ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "alloc" {
		if _, shadowed := scope.funcs["alloc"]; !shadowed {
			return saLowerAllocCall(w, ce, scope, pos, refusals, nextTemp)
		}
	}
	if ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		if ce.Expression != nil && ce.Expression.Kind == ast.KindSuperKeyword {
			return "", false, "super() is only lowerable inside a subclass constructor"
		}
		return "", false, "only direct function calls lowerable"
	}
	return saEvalNamedCall(w, ce.Expression.Text(), ce, scope, pos, refusals, nextTemp)
}

// saLowerTernaryValue 求三元值（i32 臂 SELECT / 串臂槽汇合；
// return 位与无注解声明位共用；形状证据：封存 lowerTernary:8498-8515）。
// 返回 (op, isStr, msg)：串臂 isStr=true。
// saTernaryF64Arm classifies one ternary arm for f64 join (no emission): float literal,
// int literal (sitofp at join), or f64 binding; leading-dot literals normalize with a zero.
func saTernaryF64Arm(e *ast.Node, scope *saScope) (string, string, bool) {
	if e == nil {
		return "", "", false
	}
	if e.Kind == ast.KindNumericLiteral {
		if saIsFloatLit(e.Text()) {
			txt := e.Text()
			if len(txt) > 0 && txt[0] == '.' {
				txt = "0" + txt
			}
			return "f64", txt, true
		}
		return "sitofp", e.Text(), true
	}
	if e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "f64" {
			return "f64", e.Text(), true
		}
	}
	return "", "", false
}

// saLowerTernaryF64Join joins f64 ternary arms through an f64 slot.
func saLowerTernaryF64Join(w printer.EmitTextWriter, condOp, aKind, aText, bKind, bText string, scope *saScope, nextLabel, nextTemp *int) string {
	operand := func(kind, text string) string {
		if kind == "sitofp" {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sitofp %s\n", t, text))
			return t
		}
		return text
	}
	av := operand(aKind, aText)
	bv := operand(bKind, bText)
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	tL := fmt.Sprintf("L_tern_t_%d", *nextLabel)
	*nextLabel++
	fL := fmt.Sprintf("L_tern_f_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_tern_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, tL, fL))
	w.Write(fmt.Sprintf("%s:\n", tL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as f64\n", slot, av))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", fL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as f64\n", slot, bv))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as f64\n", res, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	scope.types[res] = "f64"
	return res
}

// saDeclaredAt 报告 binder 是否在该位置解出名字（值/类型/import 统算可见；
// 无 ctx 一律 false。形状证据：封存 declaredAt:205-219）。
func saDeclaredAt(tcx *saTypeCtx, n *ast.Node) bool {
	if tcx == nil || tcx.check == nil || n == nil {
		return false
	}
	found := false
	func() {
		defer func() { _ = recover() }()
		if sym := tcx.check.GetSymbolAtLocation(n); sym != nil {
			found = true
		}
	}()
	return found
}

// saIsAnyOrUnknown 报告 checker 下该节点是否为 any/unknown（封存
// typeofKindSingle:58-59 全形；无 ctx/异常/nil 一律 false，调用方回退既有
// scope 种逻辑，零行为变）。
func saIsAnyOrUnknown(tcx *saTypeCtx, n *ast.Node) bool {
	if tcx == nil || tcx.check == nil || n == nil {
		return false
	}
	found := false
	func() {
		defer func() { _ = recover() }()
		if ty := tcx.check.GetTypeAtLocation(n); ty != nil {
			found = ty.Flags()&checker.TypeFlagsAnyOrUnknown != 0
		}
	}()
	return found
}

// saEnvProbeArm 折叠 `typeof U ===/!== "undefined" ? A : B`（任一操作数序）
// 为被取臂：U 须为 binder 不可见标识符（SA 无环境全局；tcx 缺席不折，
// 封存 envProbeArm:188-210 + splitTypeofCompare:120-151）。已声明名、
// 非标识/非三元形一律 false，调用方走既有 lowering 与拒因。
func saEnvProbeArm(ce *ast.ConditionalExpression, tcx *saTypeCtx) (*ast.Node, bool) {
	if ce == nil || ce.Condition == nil || ce.Condition.Kind != ast.KindBinaryExpression {
		return nil, false
	}
	be := ce.Condition.AsBinaryExpression()
	neg := false
	switch saBinaryOpKind(be) {
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
		neg = true
	default:
		return nil, false
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
	if typeOp == nil || litNode == nil || litNode.Text() != "undefined" {
		return nil, false
	}
	inner := typeOp.AsTypeOfExpression().Expression
	if inner == nil || inner.Kind != ast.KindIdentifier {
		return nil, false
	}
	// binder 权威：无 ctx 或可解出一律不折（语法 fallback 会发明事实）。
	if tcx == nil || saDeclaredAt(tcx, inner) {
		return nil, false
	}
	if !neg {
		return ce.WhenTrue, true
	}
	return ce.WhenFalse, true
}

func saLowerTernaryValue(w printer.EmitTextWriter, ce *ast.ConditionalExpression, where *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, bool, string) {
	// env-probe 折叠：未声明名 `typeof` 三元取臂直值（i32 经求值核，串经串核；
	// 异形臂下探既有 lowering，原拒因不变）。
	if arm, ok := saEnvProbeArm(ce, scope.tcx); ok {
		if op, msg := saEvalI32(w, arm, scope, pos, refusals, nextTemp); msg == "" {
			return op, false, ""
		}
		if sv, msg := saEvalStr(w, arm, scope, pos, refusals, nextTemp); msg == "" {
			return sv, true, ""
		}
	}
	condOp, msg := saCondOperandMat(w, ce.Condition, scope, pos, refusals, nextTemp)
	if msg != "" {
		// H1b 首批（return 同例）：条件位而非语句位。
		cpos := where.Pos()
		if ce.Condition != nil {
			cpos = ce.Condition.Pos()
		}
		ln, col := pos(cpos)
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported ternary condition: " + msg})
		return "", false, "unsupported ternary condition: " + msg
	}
	if saIsStrValue(ce.WhenTrue, scope) || saIsStrValue(ce.WhenFalse, scope) {
		if !saIsStrValue(ce.WhenTrue, scope) || !saIsStrValue(ce.WhenFalse, scope) {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "ternary arms disagree (string vs non-string)"})
			return "", false, "ternary arms disagree (string vs non-string)"
		}
		tv, msgA := saEvalStr(w, ce.WhenTrue, scope, pos, refusals, nextTemp)
		fv, msgB := saEvalStr(w, ce.WhenFalse, scope, pos, refusals, nextTemp)
		if msgA != "" || msgB != "" {
			// H1c 去重：返首个内层失败（调用方包一层即唯一拒因；
			// 旧门文案与包装复报 2 vs 上游 1）。
			if msgA != "" {
				return "", false, msgA
			}
			return "", false, msgB
		}
		slot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		tL := fmt.Sprintf("L_tern_t_%d", *nextLabel)
		*nextLabel++
		fL := fmt.Sprintf("L_tern_f_%d", *nextLabel)
		*nextLabel++
		endL := fmt.Sprintf("L_tern_end_%d", *nextLabel)
		*nextLabel++
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
		return res, true, ""
	}
	// f64 arms join through an f64 slot (float literals, sitofp ints, f64 bindings).
	if ak, at, aok := saTernaryF64Arm(ce.WhenTrue, scope); aok {
		if bk, bt, bok := saTernaryF64Arm(ce.WhenFalse, scope); bok {
			if ak == "f64" || bk == "f64" {
				return saLowerTernaryF64Join(w, condOp, ak, at, bk, bt, scope, nextLabel, nextTemp), false, ""
			}
		}
	}
	a, msgA := saEvalI32(w, ce.WhenTrue, scope, pos, refusals, nextTemp)
	b, msgB := saEvalI32(w, ce.WhenFalse, scope, pos, refusals, nextTemp)
	if msgA != "" || msgB != "" {
		// H1c 去重：返首个内层失败（调用方包一层即唯一拒因；
		// 旧门文案与包装复报 2 vs 上游 1）。
		if msgA != "" {
			return "", false, msgA
		}
		return "", false, msgB
	}
	needImport("sa_std/control.sal")
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  EXPAND SELECT %s, %s, %s, %s\n", t, condOp, a, b))
	return t, false, ""
}

// saIsTimerName 报告异步定时器裸全局名（setTimeout/clearTimeout/
// setInterval/clearInterval/setImmediate/queueMicrotask；封存 node_timers.go:16-23）。
func saIsTimerName(name string) bool {
	switch name {
	case "setTimeout", "clearTimeout", "setInterval", "clearInterval",
		"setImmediate", "queueMicrotask":
		return true
	}
	return false
}

// saIsLiteralDefault 报告缺省表达式是否可短调回放（纯字面量：数字/串/无替换
// 模板/true/false/null/undefined；标识符/调用/含洞模板重绑 caller 作用域或 duplicat
// 副作用，一律不回放；形状证据：封存 padDefaultArgs）。
func saIsLiteralDefault(n *ast.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind {
	case ast.KindNumericLiteral, ast.KindStringLiteral,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindTrueKeyword,
		ast.KindFalseKeyword, ast.KindNullKeyword, ast.KindUndefinedKeyword:
		return true
	default:
		return false
	}
}

// saPadDefaultArgs 在短调点回放省略的尾部缺省（JS 逐调用求值；调用方作用域求值，
// 种导向经 evalOne；非字面量大声拒；形状证据：封存 padDefaultArgs）。
func saPadDefaultArgs(w printer.EmitTextWriter, fname string, sig saFuncSig, args []string, evalOne func(int, *ast.Node, int) (string, string)) ([]string, string) {
	if len(args) >= sig.params {
		return args, ""
	}
	if len(sig.defaults) != sig.params || len(sig.defaultExprs) != sig.params {
		return args, ""
	}
	out := append([]string{}, args...)
	for i := len(args); i < sig.params; i++ {
		if i >= len(sig.defaults) || !sig.defaults[i] {
			return args, ""
		}
		var init *ast.Node
		if i < len(sig.defaultExprs) {
			init = sig.defaultExprs[i]
		}
		if init == nil {
			// 无初值 `?` 省略（undefined 即 0；i32/bool 垫 0；余下沿旧门）。
			if i < len(sig.paramKinds) && (sig.paramKinds[i] == "i32" || sig.paramKinds[i] == "bool") {
				out = append(out, "0")
				continue
			}
			return nil, fmt.Sprintf("omitted optional argument %d of %s needs a default or explicit passing", i+1, fname)
		}
		if !saIsLiteralDefault(init) {
			return nil, fmt.Sprintf("omitted default argument %d of %s is not a literal (non-literal defaults do not replay at short calls)", i+1, fname)
		}
		v, msg := evalOne(i, init, sig.params)
		if msg != "" {
			return nil, msg
		}
		out = append(out, v)
	}
	return out, ""
}

// saEvalNamedCall lowering具名直调（`f(...)` 与 `f.call(thisArg, ...)` 脱糖共用；
// 形参种导向求值 + spread 展开 + 元数门 + void 形；
// 形状证据：封存 lowerCallDesugar:4512-4581）。
// saCallRetKind 取具名调用的签名返回种（源级名 number/boolean/string/inst:X；
// 局部箭头别名 `fn:<gen>` 透到被调；方法调用/未知被调 false。实例声明与
// 实例赋值核对被调返回用，封存 lowerCall 签名分发同源 funcs 表）。
func saCallRetKind(ce *ast.CallExpression, scope *saScope) (string, bool) {
	// Number.parseFloat 与裸 parseFloat 同种（成员形；sa_std/string.sai
	// sa_parse_float 回 f64，见 step373）。
	if ce != nil && ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		if pa := ce.Expression.AsPropertyAccessExpression(); pa != nil &&
			pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier &&
			pa.Expression.Text() == "Number" && pa.Name() != nil && pa.Name().Text() == "parseFloat" {
			return "f64", true
		}
	}
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return "", false
	}
	name := ce.Expression.Text()
	if name == "Number" || name == "parseFloat" {
		// 数值转换恒回 f64（串经轮子，整数 sitofp；i32 位整数直通另见求值核）。
		return "f64", true
	}
	if k, ok := scope.types[name]; ok && strings.HasPrefix(k, "fn:") {
		gen := strings.TrimPrefix(k, "fn:")
		if sig, ok := scope.funcs[gen]; ok && sig.retKind != "" {
			return sig.retKind, true
		}
		return "", false
	}
	if sig, ok := scope.funcs[name]; ok && sig.retKind != "" {
		return sig.retKind, true
	}
	// Program link: qualified callees carry the defining file's return kind.
	if q, linked := saLinkCallee(scope, name); linked {
		if sig, ok := scope.funcs[q]; ok && sig.retKind != "" {
			return sig.retKind, true
		}
	}
	return "", false
}

// saLowerProjCall lowers builtin-module projected calls (fs.readFile now; other surfaces
// refuse loudly until their step; cf emitProjCall + StdProjectionTable).
// saProjTable maps one fs/net surface to its projection contract (cf StdProjectionTable:
// symbol, module, extra fixed params, string-arg positions, buffer unwrap, fallible u64, arity).
func saProjTable(remote string) (symbol, module, extra string, strArgs []int, unwrap, fallible bool, nargs int, ok bool) {
	switch remote {
	case "readFile":
		return "sa_fs_read_file", "sa_std/fs.sai", "1048576", []int{0}, true, false, 1, true
	case "writeFile":
		return "sa_fs_write_file", "sa_std/fs.sai", "", []int{0, 1}, false, false, 2, true
	case "open":
		return "sa_fs_file_open", "sa_std/fs.sai", "0", []int{0}, false, false, 1, true
	case "create":
		return "sa_fs_file_create", "sa_std/fs.sai", "", []int{0}, false, false, 1, true
	case "close":
		return "sa_fs_file_close", "sa_std/fs.sai", "", nil, false, false, 1, true
	case "read":
		return "sa_fs_file_read", "sa_std/fs.sai", "&buf, 4096", nil, false, false, 1, true
	case "write":
		return "sa_fs_file_write", "sa_std/fs.sai", "&buf, 0", nil, false, false, 1, true
	case "remove":
		return "sa_fs_remove_file", "sa_std/fs.sai", "", []int{0}, false, false, 1, true
	case "mkdir":
		return "sa_fs_make_dir", "sa_std/fs.sai", "", []int{0}, false, false, 1, true
	case "tcpConnect":
		return "sa_net_tcp_connect", "sa_std/net.sai", "0", []int{0}, false, true, 1, true
	case "tcpListen":
		return "sa_net_tcp_listener_bind", "sa_std/net.sai", "0", []int{0}, false, true, 1, true
	case "tcpAccept":
		return "sa_net_tcp_listener_accept", "sa_std/net.sai", "", nil, false, true, 1, true
	case "tcpRead":
		return "sa_net_tcp_stream_read", "sa_std/net.sai", "&buf, 0", nil, false, false, 1, true
	case "tcpWrite":
		return "sa_net_tcp_stream_write", "sa_std/net.sai", "&buf, 0", nil, false, false, 1, true
	case "tcpClose":
		return "sa_net_tcp_stream_close", "sa_std/net.sai", "", nil, false, false, 1, true
	}
	return "", "", "", nil, false, false, 0, false
}

func saLowerProjCall(w printer.EmitTextWriter, mod, remote string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	// crypto Hash 累加器暂存（命名导入裸调用 `createHash("sha256")`；
	// 空串缓冲直至 digest 经 node crypto_hash 一次性路由；声明式收养，
	// 其余用法在方法位大声拒；形状证据：封存 lowerCreateHash:4626-4644）。
	if mod == "crypto" && remote == "createHash" {
		return saStageHash(w, remote, ce, scope, pos, refusals, nextTemp)
	}
	// node.sai-backed surfaces reuse the sa_plugin_node wheel (no new builtins).
	switch mod {
	case "os", "process", "path", "crypto", "querystring", "url", "util", "punycode":
		return saLowerNodeProjCall(w, mod, remote, ce, scope, pos, refusals, nextTemp)
	}
	if mod != "fs" && mod != "net" {
		return "", false, mod + "." + remote + " is not a projected surface"
	}
	symbol, module, extra, strArgs, unwrap, fallible, nargs, ok := saProjTable(remote)
	if !ok {
		return "", false, mod + "." + remote + " is not a projected surface"
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != nargs {
		return "", false, mod + "." + remote + " takes " + fmt.Sprintf("%d", nargs) + " arguments"
	}
	isStr := map[int]bool{}
	for _, k := range strArgs {
		isStr[k] = true
	}
	scope.addImport(module)
	var parts []string
	for idx, a := range argNodes {
		if isStr[idx] {
			h, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			bp, bl := saExpandStr(w, h, nextTemp)
			parts = append(parts, "&"+bp, bl)
			continue
		}
		v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		parts = append(parts, v)
	}
	if extra != "" {
		for _, tok := range strings.Split(extra, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "&buf" {
				buf := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = alloc 4096\n", buf))
				saOwnTemp(scope, buf)
				parts = append(parts, "&"+buf)
				continue
			}
			parts = append(parts, tok)
		}
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", t, symbol, strings.Join(parts, ", ")))
	saOwnTemp(scope, t)
	// fallible u64 handles (field-0 load as i64, handle released).
	if fallible {
		u := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i64\n", u, t))
		saReleaseOwnedTemp(w, scope, t)
		return u, false, ""
	}
	if !unwrap {
		return t, false, ""
	}
	// BUFFER unwrap (cf unwrapFsBuffer: length read, data/length calls, 16-byte slice).
	hb := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", hb, t))
	saReleaseOwnedTemp(w, scope, t)
	dp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fs_read_buffer_data(%s)\n", dp, hb))
	saOwnTemp(scope, dp)
	dl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fs_read_buffer_len(%s)\n", dl, hb))
	saOwnTemp(scope, dl)
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, dp))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, dl))
	saOwnTemp(scope, out)
	saReleaseOwnedTemp(w, scope, dp)
	saReleaseOwnedTemp(w, scope, dl)
	saReleaseOwnedTemp(w, scope, t)
	return out, false, ""
}

// saNodeProjTable maps "mod.remote" to (symbol, nodeOut), reusing the
// sa_plugin_node wheel (no new builtins invented).
// nodeOut mirrors upstream StdProjectionTable NodeOut: "string" (zero-arg),
// "string1/2/3" (one/two/three string slices), "boolout" (bool 0/1 out),
// "argv" (variadic slices packed {ptr,len}), "sized" (u64 size in, out len
// echoes size), "fire" (slices by value, no outs), "fireF64" (fire + f64 out).
// u64out (Buffer.byteLength) has no u64 kind in-subset and stays refused;
// crypto.hash/hmac have no direct TS surface (accumulator-only) and stay refused.
// Shape evidence: upstream stdlib.go node entries + emitProjCall:10618-10906.
func saNodeProjTable(key string) (symbol, nodeOut string, ok bool) {
	switch key {
	case "os.platform":
		return "sa_node_plugin_os_platform", "string", true
	case "os.arch":
		return "sa_node_plugin_os_arch", "string", true
	case "os.homedir":
		return "sa_node_plugin_os_homedir", "string", true
	case "os.tmpdir":
		return "sa_node_plugin_os_tmpdir", "string", true
	case "os.hostname":
		return "sa_node_plugin_os_hostname", "string", true
	case "os.release":
		return "sa_node_plugin_os_release", "string", true
	case "os.type":
		return "sa_node_plugin_os_type", "string", true
	case "os.endianness":
		return "sa_node_plugin_os_endianness", "string", true
	case "os.machine":
		return "sa_node_plugin_os_machine", "string", true
	case "os.cpus":
		return "sa_node_plugin_os_cpus", "string", true
	case "os.version":
		return "sa_node_plugin_os_version", "string", true
	case "os.userInfo":
		return "sa_node_plugin_os_user_info", "string", true
	case "os.networkInterfaces":
		return "sa_node_plugin_os_network_interfaces", "string", true
	case "process.cwd":
		return "sa_node_plugin_process_cwd", "string", true
	case "crypto.randomUUID":
		return "sa_node_plugin_crypto_random_uuid", "string", true
	case "path.normalize":
		return "sa_node_plugin_path_normalize", "string1", true
	case "path.dirname":
		return "sa_node_plugin_path_dirname", "string1", true
	case "path.extname":
		return "sa_node_plugin_path_extname", "string1", true
	case "path.basename":
		return "sa_node_plugin_path_basename", "string2", true
	case "path.isAbsolute":
		return "sa_node_plugin_path_is_absolute", "boolout", true
	case "path.join":
		return "sa_node_plugin_path_join", "argv", true
	case "path.resolve":
		return "sa_node_plugin_path_resolve", "argv", true
	case "crypto.randomBytes":
		return "sa_node_plugin_crypto_random_bytes", "sized", true
	case "punycode.encode":
		return "sa_node_plugin_punycode_encode", "string1", true
	case "punycode.decode":
		return "sa_node_plugin_punycode_decode", "string1", true
	case "querystring.escape":
		return "sa_node_plugin_querystring_escape", "string1", true
	case "querystring.unescape":
		return "sa_node_plugin_querystring_unescape", "string1", true
	case "querystring.parse":
		return "sa_node_plugin_querystring_parse", "string1", true
	case "querystring.stringify":
		return "sa_node_plugin_querystring_stringify", "string1", true
	case "url.parse":
		return "sa_node_plugin_url_parse", "string1", true
	case "url.format":
		return "sa_node_plugin_url_format", "string1", true
	case "url.resolve":
		return "sa_node_plugin_url_resolve", "string2", true
	case "util.stripVTControlCharacters":
		return "sa_node_plugin_util_strip_vt_control_characters", "string1", true
	case "console.error", "console.time", "console.clear":
		return saNodeFireSymbol(key), "fire", true
	case "console.timeEnd":
		return "sa_node_plugin_console_time_end", "fireF64", true
	case "Buffer.concat":
		return "sa_node_plugin_buffer_concat", "argv", true
	case "Deno.hostname":
		return "sa_deno_plugin_hostname", "string", true
	case "Deno.osRelease":
		return "sa_deno_plugin_os_release", "string", true
	case "Deno.cwd":
		return "sa_deno_plugin_cwd", "string", true
	case "Deno.readTextFile":
		return "sa_deno_plugin_read_text_file", "string1", true
	case "Deno.writeTextFile":
		return "sa_deno_plugin_write_text_file", "fire", true
	case "Deno.env.get":
		return "sa_deno_plugin_env_get", "nullable", true
	case "Deno.env.set":
		return "sa_deno_plugin_env_set", "fire", true
	case "Deno.env.delete":
		return "sa_deno_plugin_env_delete", "fire", true
	case "Deno.chdir":
		return "sa_deno_plugin_chdir", "fire", true
	case "Deno.mkdir":
		return "sa_deno_plugin_mkdir", "fire", true
	case "Deno.remove":
		return "sa_deno_plugin_remove", "fire", true
	case "btoa":
		return "sa_deno_plugin_btoa", "string1", true
	case "atob":
		return "sa_deno_plugin_atob", "string1", true
	}
	return "", "", false
}

func saNodeFireSymbol(key string) string {
	switch key {
	case "console.error":
		return "sa_node_plugin_console_error"
	case "console.time":
		return "sa_node_plugin_console_time"
	}
	return "sa_node_plugin_console_clear"
}

// saNodeIsStr reports node projections returning string slices (nullable
// yields a str handle on hit, null "0" on miss).
func saNodeIsStr(key string) bool {
	_, nodeOut, ok := saNodeProjTable(key)
	if !ok {
		return false
	}
	switch nodeOut {
	case "string", "string1", "string2", "string3", "argv", "sized", "nullable":
		return true
	}
	return false
}

// saNodeStatusCheck emits the u32-status check (nonzero panics 2503, loud).
func saNodeStatusCheck(w printer.EmitTextWriter, st string, scope *saScope, nextTemp *int) {
	badL := fmt.Sprintf("L_node_bad_%d", *scope.nextLabel)
	*scope.nextLabel++
	okL := fmt.Sprintf("L_node_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	bad := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", bad, st))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", bad, badL, okL))
	w.Write(fmt.Sprintf("%s:\n", badL))
	w.Write(fmt.Sprintf("  panic(%d)\n", 2503))
	w.Write(fmt.Sprintf("%s:\n", okL))
}

// saNodeWrapStr wraps out ptr/len slots into an owned 16-byte slice handle.
func saNodeWrapStr(w printer.EmitTextWriter, ps, ls string, scope *saScope, nextTemp *int) string {
	ptr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", ptr, ps))
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", ln, ls))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, ptr))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, ln))
	saOwnTemp(scope, out)
	saReleaseOwnedTemp(w, scope, ps)
	saReleaseOwnedTemp(w, scope, ls)
	return out
}

// saNodeStrArg lowers one string argument to (&ptr, len) parts.
func saNodeStrArg(w printer.EmitTextWriter, a *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	h, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", "", msg
	}
	bp, bl := saExpandStr(w, h, nextTemp)
	return "&" + bp, bl, ""
}

// saNodeStrArgRaw lowers one string argument to (ptr, len) value parts
// (fire convention: loaded values, no &slots; cf emitProjCall fire branch).
func saNodeStrArgRaw(w printer.EmitTextWriter, a *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	h, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", "", msg
	}
	bp, bl := saExpandStr(w, h, nextTemp)
	return bp, bl, ""
}

// saLowerNodeProjCall lowers one node.sai-backed call (mod already stripped,
// e.g. mod=os remote=platform). Returns (operand, voidCall, msg); string
// results are str-handle temps (saCallIsStr gates downstream).
// saHashState is one crypto Hash accumulator: acc buffers fed bytes,
// algo holds the lowered algorithm operand; done latches digest()
// (ERR_CRYPTO_HASH_FINALIZED). Shape evidence: upstream hashState:522-526.
type saHashState struct {
	kind string
	acc  string
	algo string
	done bool
}

// saStageHash stages a crypto Hash accumulator (empty string slice until
// digest routes algo+buffer through the node crypto_hash one-shot wheel;
// declaration-form adoption tracks it, other uses refuse loudly at the
// method site via the missing hashAcc entry).
// Shape evidence: upstream lowerCreateHash:4626-4644 (createHash half).
func saStageHash(w printer.EmitTextWriter, fname string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 1 {
		return "", false, fname + " takes exactly 1 argument(s)"
	}
	algo, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	acc := saLowerStringLiteral(w, "", scope, nextTemp)
	scope.lastHash = &saHashState{kind: "Hash", acc: acc, algo: algo}
	return acc, false, ""
}

// saAdoptHash adopts a staged crypto Hash accumulator onto a freshly bound
// declaration name (callee may be an import alias, resolved through the
// remote export name like the call site; staged handle re-points at the
// binding the value moved into).
// Shape evidence: upstream lowerVarDeclList adoption:1472-1491.
func saAdoptHash(scope *saScope, name string, init *ast.Node) {
	st := scope.lastHash
	scope.lastHash = nil
	if st == nil {
		return
	}
	if init == nil || init.Kind != ast.KindCallExpression {
		return
	}
	ce := init.AsCallExpression()
	if ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return
	}
	callee := ce.Expression.Text()
	if r, ok := scope.importRemote[callee]; ok {
		callee = r
	}
	if callee != "createHash" {
		return
	}
	if scope.hashAcc == nil {
		scope.hashAcc = map[string]*saHashState{}
	}
	st.acc = name
	scope.hashAcc[name] = st
}

// saHashUpdateCall reports `h.update(..)` over a tracked Hash binding
// (value-position uses refuse loudly; statement form is the only sound one).
func saHashUpdateCall(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil || pa.Name().Text() != "update" {
		return false
	}
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
		return false
	}
	if k, ok := scope.types[pa.Expression.Text()]; !ok || k != "str" {
		return false
	}
	_, ok := scope.hashAcc[pa.Expression.Text()]
	return ok
}

// saLowerHashCall routes Hash.update/digest over a staged accumulator.
// update folds one string chunk via sa_string_concat and rebinds the receiver
// (串 `+=` 重绑同形：先释旧柄，新柄 consume+复位）；digest emits the node
// crypto_hash one-shot wheel call (hex natively) and latches finalized.
// Non-literal/non-hex encodings, post-finalize use and unknown methods refuse.
// Shape evidence: upstream lowerHashMethod:4651-4714 (Hash half).
func saLowerHashCall(w printer.EmitTextWriter, recv string, st *saHashState, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if st.done {
		return "", "Hash is already digested (ERR_CRYPTO_HASH_FINALIZED)"
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	switch method {
	case "update":
		if len(argNodes) != 1 {
			return "", "Hash.update takes exactly 1 argument"
		}
		if argNodes[0] == nil || !saIsStrValue(argNodes[0], scope) {
			return "", "Hash.update takes a string chunk"
		}
		chunk, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		out := saConcatSlices(w, st.acc, chunk, scope, nextTemp)
		saRebindRelease(w, scope, recv)
		w.Write(fmt.Sprintf("  %s = %s\n", recv, out))
		saConsumeOwn(scope, out)
		if b := saOwnOf(scope, recv); b != nil {
			b.heap = true
		}
		saMarkRebound(scope, recv)
		st.acc = recv
		return recv, ""
	case "digest":
		if len(argNodes) > 1 {
			return "", "Hash.digest takes at most 1 argument (encoding)"
		}
		enc := "hex"
		if len(argNodes) == 1 {
			lit, ok := saDigestEncoding(argNodes[0])
			if !ok {
				return "", "Hash.digest encoding must be a string literal"
			}
			enc = lit
		}
		if enc != "hex" {
			return "", fmt.Sprintf("Hash.digest(%q) is not lowerable (only hex digests are projected)", enc)
		}
		return saLowerHashDigest(w, st, scope, nextTemp)
	default:
		return "", "Hash." + method + " is not a projected surface"
	}
}

// saLowerHashDigest emits the one-shot node crypto_hash wheel call over the
// staged (algo, buffer) slices (hex natively; status-checked, hex digest
// wrapped to a str handle; finalized latched by the caller).
// Shape evidence: upstream digest arm:4693-4709 + string2 emission,
// wheel: sa_plugin_node/node.sai sa_node_plugin_crypto_hash.
func saLowerHashDigest(w printer.EmitTextWriter, st *saHashState, scope *saScope, nextTemp *int) (string, string) {
	scope.addImport("node.sai")
	ap, al := saExpandStr(w, st.algo, nextTemp)
	dp, dl := saExpandStr(w, st.acc, nextTemp)
	ps := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", ps))
	saOwnTemp(scope, ps)
	ls := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", ls))
	saOwnTemp(scope, ls)
	stt := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_node_plugin_crypto_hash(&%s, %s, &%s, %s, &%s, &%s)\n", stt, ap, al, dp, dl, ps, ls))
	saOwnTemp(scope, stt)
	saNodeStatusCheck(w, stt, scope, nextTemp)
	st.done = true
	return saNodeWrapStr(w, ps, ls, scope, nextTemp), ""
}

// saDigestEncoding reads a literal digest encoding from the call's argument
// (lowered operands lose literal text; non-literals refuse).
// Shape evidence: upstream digestEncoding:4716-4723.
func saDigestEncoding(a *ast.Node) (string, bool) {
	if a == nil {
		return "", false
	}
	if a.Kind != ast.KindStringLiteral && a.Kind != ast.KindNoSubstitutionTemplateLiteral {
		return "", false
	}
	return a.Text(), true
}

func saLowerNodeProjCall(w printer.EmitTextWriter, mod, remote string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	key := mod + "." + remote
	if mod == "" {
		key = remote
	}
	symbol, nodeOut, ok := saNodeProjTable(key)
	if !ok {
		return "", false, key + " is not a projected std surface (see StdProjectionTable)"
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	backendModule := "node.sai"
	if len(key) >= 5 && key[:5] == "Deno." || key == "btoa" || key == "atob" {
		backendModule = "deno.sai"
	}
	scope.addImport(backendModule)
	callStatus := func(parts ...string) string {
		st := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", st, symbol, strings.Join(parts, ", ")))
		saOwnTemp(scope, st)
		saNodeStatusCheck(w, st, scope, nextTemp)
		return st
	}
	switch nodeOut {
	case "string":
		if len(argNodes) != 0 {
			return "", false, fmt.Sprintf("%s takes 0 arguments", key)
		}
		ps := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", ps))
		saOwnTemp(scope, ps)
		ls := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", ls))
		saOwnTemp(scope, ls)
		callStatus("&"+ps, "&"+ls)
		return saNodeWrapStr(w, ps, ls, scope, nextTemp), false, ""
	case "string1", "string2", "string3":
		want := 1
		if nodeOut == "string2" {
			want = 2
		}
		if nodeOut == "string3" {
			want = 3
		}
		if len(argNodes) != want {
			return "", false, fmt.Sprintf("%s takes %d argument(s)", key, want)
		}
		parts := []string{}
		for _, a := range argNodes {
			bp, bl, msg := saNodeStrArg(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			parts = append(parts, bp, bl)
		}
		ps := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", ps))
		saOwnTemp(scope, ps)
		ls := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", ls))
		saOwnTemp(scope, ls)
		parts = append(parts, "&"+ps, "&"+ls)
		callStatus(parts...)
		return saNodeWrapStr(w, ps, ls, scope, nextTemp), false, ""
	case "boolout":
		if len(argNodes) != 1 {
			return "", false, fmt.Sprintf("%s takes exactly 1 argument", key)
		}
		bp, bl, msg := saNodeStrArg(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		bslot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", bslot))
		saOwnTemp(scope, bslot)
		callStatus(bp, bl, "&"+bslot)
		bout := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", bout, bslot))
		saReleaseOwnedTemp(w, scope, bslot)
		return bout, false, ""
	case "argv":
		parts := []string{}
		for _, a := range argNodes {
			bp, bl, msg := saNodeStrArgRaw(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			parts = append(parts, bp, bl)
		}
		n := len(argNodes)
		slots := n
		if slots == 0 {
			slots = 1
		}
		argv := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc %d\n", argv, slots*16))
		saOwnTemp(scope, argv)
		for i := 0; i < n; i++ {
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", argv, i*16, parts[i*2]))
			w.Write(fmt.Sprintf("  store %s + %d, %s as u64\n", argv, i*16+8, parts[i*2+1]))
		}
		ps := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", ps))
		saOwnTemp(scope, ps)
		ls := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", ls))
		saOwnTemp(scope, ls)
		callStatus(argv, fmt.Sprintf("%d", n), "&"+ps, "&"+ls)
		out := saNodeWrapStr(w, ps, ls, scope, nextTemp)
		saReleaseOwnedTemp(w, scope, argv)
		return out, false, ""
	case "sized":
		if len(argNodes) != 1 {
			return "", false, fmt.Sprintf("%s takes 1 argument", key)
		}
		sz, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		ps := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", ps))
		saOwnTemp(scope, ps)
		callStatus(sz, "&"+ps)
		ptr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", ptr, ps))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, ptr))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, sz))
		saOwnTemp(scope, out)
		saReleaseOwnedTemp(w, scope, ps)
		return out, false, ""
	case "fire", "fireF64":
		if nodeOut == "fireF64" && len(argNodes) != 1 {
			return "", false, fmt.Sprintf("%s takes exactly 1 argument", key)
		}
		wantFire := -1
		switch key {
		case "Deno.writeTextFile", "Deno.env.set":
			wantFire = 2
		case "Deno.mkdir", "Deno.remove", "Deno.chdir", "Deno.env.delete":
			wantFire = 1
		case "console.clear":
			wantFire = 0
		}
		if wantFire >= 0 && len(argNodes) != wantFire {
			if key == "Deno.mkdir" || key == "Deno.remove" {
				return "", false, fmt.Sprintf("%s takes exactly one path (options objects out of subset)", key)
			}
			return "", false, fmt.Sprintf("%s takes %d argument(s)", key, wantFire)
		}
		ins := []string{}
		for _, a := range argNodes {
			// Capability contract follows each plugin's .sai: node.sai
			// fire takes (ptr, len) values, deno.sai takes &slots.
			// Upstream emits raw values for both; deno fire natively
			// rejects that (CapabilityMismatch), so deno keeps &slots
			// (verified: mkdir/remove run; JEV divergence, evidence kept).
			bp, bl, msg := saNodeStrArgRaw(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			if len(key) >= 5 && key[:5] == "Deno." {
				bp = "&" + bp
			}
			ins = append(ins, bp, bl)
		}
		// Fixed trailing immediates (recursive=0 for Deno mkdir/remove).
		if key == "Deno.mkdir" || key == "Deno.remove" {
			ins = append(ins, "0")
		}
		var fslot string
		if nodeOut == "fireF64" {
			fslot = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", fslot))
			saOwnTemp(scope, fslot)
			ins = append(ins, "&"+fslot)
		}
		callStatus(ins...)
		if nodeOut == "fireF64" {
			fout := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as f64\n", fout, fslot))
			saReleaseOwnedTemp(w, scope, fslot)
			scope.types[fout] = "f64"
			return fout, false, ""
		}
		return "", true, ""
	case "nullable":
		// One slice in, string out; status 1 maps to null "0", other
		// nonzero panics; both arms join on one result temp.
		if len(argNodes) != 1 {
			return "", false, fmt.Sprintf("%s takes exactly 1 argument", key)
		}
		bp, bl, msg := saNodeStrArg(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		nps := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", nps))
		saOwnTemp(scope, nps)
		nls := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", nls))
		saOwnTemp(scope, nls)
		nst := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @%s(&%s, %s, &%s, &%s)\n", nst, symbol, bp, bl, nps, nls))
		saOwnTemp(scope, nst)
		nres := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		zeroL := fmt.Sprintf("L_node_null_%d", *scope.nextLabel)
		*scope.nextLabel++
		chkL := fmt.Sprintf("L_node_chk_%d", *scope.nextLabel)
		*scope.nextLabel++
		wrapL := fmt.Sprintf("L_node_wrap_%d", *scope.nextLabel)
		*scope.nextLabel++
		endL := fmt.Sprintf("L_node_end_%d", *scope.nextLabel)
		*scope.nextLabel++
		badL := fmt.Sprintf("L_node_bad_%d", *scope.nextLabel)
		*scope.nextLabel++
		isnull := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, 1\n", isnull, nst))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isnull, zeroL, chkL))
		w.Write(fmt.Sprintf("%s:\n", zeroL))
		saReleaseOwnedTemp(w, scope, nps)
		saReleaseOwnedTemp(w, scope, nls)
		w.Write(fmt.Sprintf("  %s = 0\n", nres))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", chkL))
		isbad := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = ne %s, 0\n", isbad, nst))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isbad, badL, wrapL))
		w.Write(fmt.Sprintf("%s:\n", badL))
		w.Write(fmt.Sprintf("  panic(%d)\n", 2503))
		w.Write(fmt.Sprintf("%s:\n", wrapL))
		nptr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", nptr, nps))
		nln := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", nln, nls))
		w.Write(fmt.Sprintf("  %s = alloc 16\n", nres))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", nres, nptr))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", nres, nln))
		saOwnTemp(scope, nres)
		saReleaseOwnedTemp(w, scope, nps)
		saReleaseOwnedTemp(w, scope, nls)
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		return nres, false, ""
	}
	return "", false, key + " is not a projected std surface (see StdProjectionTable)"
}

// saLowerNodeMethodCall lowers node-plugin namespace calls (process/crypto
// zero-arg globals without import; console.error/time/timeEnd/clear;
// Buffer.byteLength/concat). Returns (operand, voidCall, msg, handled);
// unhandled receivers fall through to the generic refusal.
func saLowerNodeMethodCall(w printer.EmitTextWriter, pa *ast.PropertyAccessExpression, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string, bool) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	recv, method := "", ""
	if pa.Name() != nil {
		method = pa.Name().Text()
	}
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		recv = pa.Expression.Text()
	}
	if recv == "process" || recv == "crypto" {
		if len(argNodes) != 0 {
			return "", false, "", false
		}
		if _, _, ok := saNodeProjTable(recv + "." + method); !ok {
			return "", false, "", false
		}
		op, voidCall, msg := saLowerNodeProjCall(w, recv, method, ce, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg, true
		}
		return op, voidCall, "", true
	}
	if recv == "console" {
		switch method {
		case "error", "time", "timeEnd", "clear":
		default:
			return "", false, "", false
		}
		return saLowerConsoleNode(w, method, ce, scope, pos, refusals, nextTemp)
	}
	if recv == "Buffer" {
		switch method {
		case "byteLength":
			return "", false, "Buffer.byteLength needs u64 (beyond i32 subset)", true
		case "concat":
			return saLowerBufferConcat(w, ce, scope, pos, refusals, nextTemp)
		}
		return "", false, "", false
	}
	if recv == "Deno" || (pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression) {
		// Two-level env chain (Deno.env.get/set/delete) vs direct members.
		if pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression {
			inner := pa.Expression.AsPropertyAccessExpression()
			if inner.Expression != nil && inner.Expression.Kind == ast.KindIdentifier &&
				inner.Expression.Text() == "Deno" && inner.Name() != nil && inner.Name().Text() == "env" {
				return saLowerDenoEnv(w, method, ce, scope, pos, refusals, nextTemp)
			}
			return "", false, "", false
		}
		if _, _, ok := saNodeProjTable("Deno." + method); !ok {
			return "", false, "", false
		}
		op, voidCall, msg := saLowerNodeProjCall(w, "Deno", method, ce, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg, true
		}
		return op, voidCall, "", true
	}
	return "", false, "", false
}

// saLowerDenoEnv lowers Deno.env.get/set/delete (two-level receiver).
func saLowerDenoEnv(w printer.EmitTextWriter, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string, bool) {
	var key string
	var want int
	switch method {
	case "get":
		key, want = "Deno.env.get", 1
	case "set":
		key, want = "Deno.env.set", 2
	case "delete":
		key, want = "Deno.env.delete", 1
	default:
		return "", false, "Deno.env." + method + " is not projected (get/set/delete only)", true
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != want {
		return "", false, fmt.Sprintf("Deno.env.%s takes exactly %d argument(s)", method, want), true
	}
	op, voidCall, msg := saLowerNodeProjCall(w, "Deno", "env."+method, ce, scope, pos, refusals, nextTemp)
	_ = key
	if msg != "" {
		return "", false, msg, true
	}
	return op, voidCall, "", true
}

// saLowerConsoleNode lowers console.error/time/timeEnd/clear through node.sai.
// error folds multi-arg (spaces + trailing newline) into one slice, like log.
func saLowerConsoleNode(w printer.EmitTextWriter, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string, bool) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	switch method {
	case "error":
		acc := ""
		for i, a := range argNodes {
			if i > 0 {
				seg := saLowerStringLiteral(w, " ", scope, nextTemp)
				acc = saConcatSlicesOpt(w, acc, seg, scope, nextTemp)
			}
			seg, msg := saToSlice(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg, true
			}
			acc = saConcatSlicesOpt(w, acc, seg, scope, nextTemp)
		}
		if acc == "" {
			acc = saLowerStringLiteral(w, "", scope, nextTemp)
		}
		acc = saConcatSlicesOpt(w, acc, saLowerStringLiteral(w, "\n", scope, nextTemp), scope, nextTemp)
		op, voidCall, msg := saLowerNodeFireSlice(w, "console.error", acc, scope, nextTemp)
		if msg != "" {
			return "", false, msg, true
		}
		return op, voidCall, "", true
	case "time", "timeEnd":
		if len(argNodes) > 1 {
			return "", false, "console." + method + " takes at most 1 argument", true
		}
		label := saLowerStringLiteral(w, "default", scope, nextTemp)
		if len(argNodes) == 1 {
			h, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg, true
			}
			label = h
		}
		op, voidCall, msg := saLowerNodeFireSlice(w, "console."+method, label, scope, nextTemp)
		if msg != "" {
			return "", false, msg, true
		}
		return op, voidCall, "", true
	case "clear":
		if len(argNodes) != 0 {
			return "", false, "console.clear takes no arguments", true
		}
		op, voidCall, msg := saLowerNodeProjCall(w, "console", method, ce, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg, true
		}
		return op, voidCall, "", true
	}
	return "", false, "", false
}

// saConcatSlicesOpt concatenates two slice handles (empty left passes through).
func saConcatSlicesOpt(w printer.EmitTextWriter, left, right string, scope *saScope, nextTemp *int) string {
	if left == "" {
		return right
	}
	return saConcatSlices(w, left, right, scope, nextTemp)
}

// saLowerNodeFireSlice fires one pre-folded slice through a fire/fireF64 entry.
func saLowerNodeFireSlice(w printer.EmitTextWriter, key, slice string, scope *saScope, nextTemp *int) (string, bool, string) {
	symbol, nodeOut, ok := saNodeProjTable(key)
	if !ok {
		return "", false, key + " is not a projected std surface (see StdProjectionTable)"
	}
	scope.addImport("node.sai")
	bp, bl := saExpandStr(w, slice, nextTemp)
	ins := []string{bp, bl}
	var fslot string
	if nodeOut == "fireF64" {
		fslot = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", fslot))
		saOwnTemp(scope, fslot)
		ins = append(ins, "&"+fslot)
	}
	st := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", st, symbol, strings.Join(ins, ", ")))
	saOwnTemp(scope, st)
	saNodeStatusCheck(w, st, scope, nextTemp)
	if nodeOut == "fireF64" {
		fout := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as f64\n", fout, fslot))
		saReleaseOwnedTemp(w, scope, fslot)
		scope.types[fout] = "f64"
		return fout, false, ""
	}
	return "", true, ""
}

// saLowerBufferConcat lowers Buffer.concat([a, b, ...]) with a literal element
// list (identifiers and string literals only; dynamic arrays refuse loudly).
func saLowerBufferConcat(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string, bool) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 1 || argNodes[0] == nil || argNodes[0].Kind != ast.KindArrayLiteralExpression {
		return "", false, "Buffer.concat takes exactly 1 argument (an array literal)", true
	}
	symbol, nodeOut, ok := saNodeProjTable("Buffer.concat")
	if !ok || nodeOut != "argv" {
		return "", false, "Buffer.concat is not a projected std surface (see StdProjectionTable)", true
	}
	_ = symbol
	scope.addImport("node.sai")
	parts := []string{}
	for _, el := range argNodes[0].AsArrayLiteralExpression().Elements.Nodes {
		if el == nil || (el.Kind != ast.KindIdentifier && el.Kind != ast.KindStringLiteral && el.Kind != ast.KindNoSubstitutionTemplateLiteral) {
			return "", false, "Buffer.concat elements must be identifiers or string literals", true
		}
		h, msg := saEvalStr(w, el, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg, true
		}
		bp, bl := saExpandStr(w, h, nextTemp)
		parts = append(parts, bp, bl)
	}
	n := len(parts) / 2
	slots := n
	if slots == 0 {
		slots = 1
	}
	argv := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %d\n", argv, slots*16))
	saOwnTemp(scope, argv)
	for i := 0; i < n; i++ {
		w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", argv, i*16, parts[i*2]))
		w.Write(fmt.Sprintf("  store %s + %d, %s as u64\n", argv, i*16+8, parts[i*2+1]))
	}
	// Reuse the argv tail through the table entry.
	ps := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", ps))
	saOwnTemp(scope, ps)
	ls := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", ls))
	saOwnTemp(scope, ls)
	st := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	sym, _, _ := saNodeProjTable("Buffer.concat")
	w.Write(fmt.Sprintf("  %s = call @%s(%s, %d, &%s, &%s)\n", st, sym, argv, n, ps, ls))
	saOwnTemp(scope, st)
	saNodeStatusCheck(w, st, scope, nextTemp)
	out := saNodeWrapStr(w, ps, ls, scope, nextTemp)
	saReleaseOwnedTemp(w, scope, argv)
	return out, false, "", true
}

func saEvalNamedCall(w printer.EmitTextWriter, name string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	callName := name
	if name == "main" && scope.mainRenamed {
		// 入口合成抢 `@main`（定义改名处同步；签名表仍以原名建）。
		callName = "main__user"
	}
	if name == "Number" {
		// Number(x) 数值转换（串经轮子 f64，整数/布尔 sitofp f64；
		// i32 位整数直通见求值核；形状证据：上游 sa_parse_float 实发）。
		// Number() 空参 ≡ Number(0)（ECMA-262 回 +0；i32 子集零即零，
		// 与整数支同形 sitofp 进 f64，无实参故无副作用求值；上游拒收，
		// 本仓 thin-lead，见 step374）。
		if ce.Arguments == nil || len(ce.Arguments.Nodes) == 0 {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sitofp 0\n", t))
			scope.types[t] = "f64"
			return t, false, ""
		}
		op, msg := saLowerFloatConvert(w, name, ce, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		return op, false, ""
	}
	if name == "Array" {
		// 数组构造式具化（调用式；`new Array` 另走声明位）。
		h, msg := saLowerArrayCtor(w, ce.AsNode(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		return h, false, ""
	}
	if k, shadowed := scope.types[name]; shadowed {
		// 局部箭头别名 `fn:<gen>`：转 out-of-line 被调（形状证据：封存
		// arrowAliases:588 + lowerCall 的 arrowAlias 分支）。
		if strings.HasPrefix(k, "fn:") {
			return saEvalFuncCall(w, name, strings.TrimPrefix(k, "fn:"), scope.funcs[strings.TrimPrefix(k, "fn:")], ce, scope, pos, refusals, nextTemp)
		}
		return "", false, name + " is not a function"
	}
	if m, ok := scope.mathAlias[name]; ok {
		return saEvalMathMethod(w, m, ce, scope, pos, refusals, nextTemp)
	}
	sig, ok := scope.funcs[name]
	// Program link first: imported names and own-prefixed callees resolve
	// qualified (recursion included); unlinked names fall through untouched.
	// Shape evidence: upstream LowerProgram links[p]/funcSigs seeding + linkRoute.
	if q, linked := saLinkCallee(scope, name); linked {
		if lsig, ok := scope.funcs[q]; ok {
			return saEvalFuncCall(w, name, q, lsig, ce, scope, pos, refusals, nextTemp)
		}
		// 自文件函数（签名按本名取，发射按限定名；定义侧同前缀；
		// 导入名不触此分支，未播种导入沿旧 unknown 门）。
		if _, isImport := scope.linkResolve[name]; !isImport {
			if sig, ok := scope.funcs[name]; ok {
				return saEvalFuncCall(w, name, q, sig, ce, scope, pos, refusals, nextTemp)
			}
		}
		return "", false, "call to unknown function " + name + " (declare it before use)"
	}
	if !ok {
		// structuredClone builtin fallback (locals, math aliases and user functions win above).
		if name == "structuredClone" {
			return saLowerStructuredClone(w, ce, scope, pos, refusals, nextTemp)
		}
		// parseInt 裸全局（十进制扫描；基数门内收；形状证据：封存 lowerParseIntCall）。
		if name == "parseInt" {
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			op, msg := saLowerParseIntArgs(w, argNodes, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, false, ""
		}
		// parseFloat 裸全局（串经 sa_parse_float 轮子回 f64；上游同形）。
		if name == "parseFloat" {
			op, msg := saLowerFloatConvert(w, name, ce, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, false, ""
		}
		// isNaN/isFinite 裸全局（`Number.` 同形，见 saLowerIsNaNFinite；
		// lib.es5.d.ts 别名）。
		if name == "isNaN" || name == "isFinite" {
			return saLowerIsNaNFinite(w, name, ce, scope, pos, refusals, nextTemp)
		}
		// Boolean(x) 真值转换（lib.es5 全局；v1 接字面量 + 具名：
		// null/undefined/void/0/"" /false 即 0，非零数/true/非空串即 1；
		// i32/bool 具名 `ne 0`，串具名 len 判空，句柄具名恒真（读无副作用）；
		// 调用形另步；`Boolean(-0)` 经 i32 求值恒 0 正确）。
		if name == "Boolean" {
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			if len(argNodes) != 1 {
				return "", false, "Boolean takes one argument"
			}
			if op, done, msg := saLowerBooleanArg(w, argNodes[0], scope, nextTemp); msg != "" || done {
				if msg != "" {
					return "", false, msg
				}
				return op, false, ""
			}
			return "", false, "Boolean takes a literal or a bound value (call results are not lowerable yet)"
		}
		// btoa/atob bare globals lower through deno.sai without import
		// (Web globals; strings only; shape evidence: upstream stdlib
		// "bare global" entries + dn7 test).
		if name == "btoa" || name == "atob" {
			op, voidCall, msg := saLowerNodeProjCall(w, "", name, ce, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, voidCall, ""
		}
		// fs/net builtin-module projection (locals, aliases and user functions win above).
		if mod, ok := scope.imports[name]; ok {
			remote := name
			if r, ok := scope.importRemote[name]; ok {
				remote = r
			}
			return saLowerProjCall(w, mod, remote, ce, scope, pos, refusals, nextTemp)
		}
		// 异步定时器裸全局专用拒因（先于 unknown；事件循环回调分发
		// Phase 2，无同步 JS 形；形状证据：封存 node_timers.go:16-33）。
		// 方法形（`x.setTimeout`）不触此门，走各自表面。
		if saIsTimerName(name) {
			return "", false, name + " needs an event loop with callback dispatch (async timers are Phase 2)"
		}
		// Program 定位拒因：未导入但他处已导出即指认定义文件（上游 linkRoute
		// "import it first" 同形；无命中沿旧 unknown 门）。
		if msg := saImportFirstAdvisory(scope, name); msg != "" {
			return "", false, msg
		}
		return "", false, "call to unknown function " + name + " (declare it before use)"
	}
	return saEvalFuncCall(w, name, callName, sig, ce, scope, pos, refusals, nextTemp)
}

// saImportFirstAdvisory 在 program 模式下为未链接调用指认定义文件
// （`X is defined in f.ts; import it first`；上游 linkRoute 定位拒因同形，
// 形状证据：封存 TestLowerProgramImportFirst/TestLinkRouteMisses；箭头与
// 默认导出永不命中名调用，维持旧拒因；单文件 linkHarvests 空零行为变）。
func saImportFirstAdvisory(scope *saScope, name string) string {
	if scope == nil || len(scope.linkHarvests) == 0 || name == "" {
		return ""
	}
	files := make([]string, 0, len(scope.linkHarvests))
	for f := range scope.linkHarvests {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if hv, ok := scope.linkHarvests[f][name]; ok && hv.exported && !hv.isArrow {
			return name + " is defined in " + f + "; import it first"
		}
	}
	return ""
}

// saLinkCallee resolves a call name through the program link environment:
// imported names rewrite to their qualified callee; own top-level functions
// under a defPrefix rewrite to the prefixed callee (recursion included).
// Single-file lowering leaves both empty and resolves unchanged.
func saLinkCallee(scope *saScope, name string) (string, bool) {
	if scope == nil {
		return name, false
	}
	if q, ok := scope.linkResolve[name]; ok {
		return q, true
	}
	if scope.defPrefix != "" {
		if _, ok := scope.funcs[name]; ok {
			return scope.defPrefix + name, true
		}
	}
	return name, false
}

// saEvalFuncCall 按签名求实参并发射 `call @callee(...)`：普通函数与局部箭头
// 别名（`fn:<gen>` → 转 @gen）共用同一条定向求值/补参/元数门。
// 形状证据：封存 lowerCall:1414-1520（形参种定向、spread、default 补参、
// 元数精确匹配、void 句用值返 nil）。
func saEvalFuncCall(w printer.EmitTextWriter, name, callee string, sig saFuncSig, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	callName := callee
	var args []string
	evalOne := func(i int, a *ast.Node, total int) (string, string) {
		// 形参种导向求值：str 形参走串求值（字面量/调用/拼接皆可），
		// arr 形参走句柄值；实例须同类相授；arr/str 句柄标识符直传；
		// 其余走 bool 兼容求值。
		if len(sig.paramKinds) == total && sig.paramKinds[i] == "str" {
			return saEvalStr(w, a, scope, pos, refusals, nextTemp)
		}
		if len(sig.paramKinds) == total && sig.paramKinds[i] == "arr" {
			// handle position: array values go through the handle bus; scalar values
			// (enum instantiations as i32, etc.) pass through; cf lowerCall handle positions.
			if op, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp); msg == "" {
				return op, ""
			}
			return saEvalI32(w, a, scope, pos, refusals, nextTemp)
		}
		// f64 params evaluate as floats.
		if len(sig.paramKinds) == total && sig.paramKinds[i] == "f64" {
			return saEvalF64(w, a, scope, pos, refusals, nextTemp)
		}
		// map 形参走句柄直传（Record 字典柄；与 arr/str 句柄位同形，
		// 值种由被调注解 `saSeedParamMapVals` 自定，调用方只传柄）。
		if len(sig.paramKinds) == total && sig.paramKinds[i] == "map" {
			if a != nil && a.Kind == ast.KindIdentifier {
				if k, ok := scope.types[a.Text()]; ok && k == "map" {
					return a.Text(), ""
				}
			}
			return "", "map argument needs a bound map"
		}
		if len(sig.paramKinds) == total && len(sig.paramKinds[i]) > 5 && sig.paramKinds[i][:5] == "inst:" {
			if a != nil && a.Kind == ast.KindIdentifier {
				if k, ok := scope.types[a.Text()]; ok && k == sig.paramKinds[i] {
					return a.Text(), ""
				}
				// 基类形参接派生实参多态（实参类为形参类本身或派生即直传句柄；与声明位 step395 同规：绑定跟初值，用点取决；无关/反向沿旧门）。
				if k, ok := scope.types[a.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
					if saIsDerivedFrom(scope.classes, k[5:], sig.paramKinds[i][5:]) {
						return a.Text(), ""
					}
				}
			}
			// 空字面量即 0 句柄（与 `Box|null` 空吸收同形；封存上游实发
			// `call @f(0)`；错类沿旧门）。
			if a != nil && (a.Kind == ast.KindNullKeyword ||
				(a.Kind == ast.KindIdentifier && a.Text() == "undefined")) {
				return "0", ""
			}
			// 接口形参配对象字面量实参：按注解布局现场具化（声明位
			// saLowerObjectLiteral 同核；键集精确匹配，多/缺键沿其旧门；
			// 类形参仍拒——上游字面量直传跳过构造 wiring，禁照抄）。
			if a != nil && a.Kind == ast.KindObjectLiteralExpression {
				if inm := sig.paramKinds[i][5:]; inm != "" {
					if d, ok := scope.classes[inm]; ok && d != nil && d.isIface {
						h, _, msg := saLowerObjectLiteral(w, a, inm, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", msg
						}
						return h, ""
					}
				}
			}
			return "", "instance argument needs matching class"
		}
		if a != nil && a.Kind == ast.KindIdentifier {
			if k, ok := scope.types[a.Text()]; ok && (k == "arr" || k == "str") {
				return a.Text(), ""
			}
		}
		op, msg := saEvalBool(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// 非标识符实参的结果记种检查（串/实例句柄禁入 i32 位；标识符直传
		// 沿上口径；读位只建种不验种，验种在此；铁律 4）。
		if msg := saCheckI32Value(scope, op); msg != "" {
			return "", msg
		}
		return op, ""
	}
	if ce.Arguments != nil {
		nodes := ce.Arguments.Nodes
		if sig.hasRest {
			// rest calls pack into one slice (fixed positions evaluate per signature kind, extras pack:
			// plain values push as i32, spreads append whole; cf resolveSpreadCall rest branch).
			fixed := sig.params - 1
			if fixed < 0 || len(nodes) < fixed {
				return "", false, fmt.Sprintf("arity mismatch for %s: want at least %d, got %d", name, fixed, len(nodes))
			}
			for i := 0; i < fixed; i++ {
				op, msg := evalOne(i, nodes[i], len(sig.paramKinds))
				if msg != "" {
					return "", false, msg
				}
				args = append(args, op)
			}
			h := saNewEmptyArray(w, nextTemp)
			for _, a := range nodes[fixed:] {
				if a != nil && a.Kind == ast.KindSpreadElement {
					sv, msg := saArrValueOf(w, a.AsSpreadElement().Expression.AsNode(), scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", false, msg
					}
					saAppendSlice(w, h, sv, scope, nextTemp)
					continue
				}
				v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				// rest 余元记种检查（串/实例句柄禁入 i32 余槽；展开元沿整片口径；铁律 4）。
				if msg := saCheckI32Value(scope, v); msg != "" {
					return "", false, msg
				}
				saLowerArrayPush(w, h, v, scope, nextTemp)
			}
			saOwnTemp(scope, h)
			args = append(args, h)
		} else if spread, msg, handled := saSpreadCallArgs(w, name, nodes, sig, evalOne, scope, pos, refusals, nextTemp); handled || msg != "" {
			if msg != "" {
				return "", false, msg
			}
			args = spread
		} else {
			evalTotal := len(nodes)
			if len(nodes) <= sig.params {
				evalTotal = sig.params
			}
			for i, a := range nodes {
				op, msg := evalOne(i, a, evalTotal)
				if msg != "" {
					return "", false, msg
				}
				args = append(args, op)
			}
		}
	}
	if len(args) != sig.params {
		if padded, msg := saPadDefaultArgs(w, name, sig, args, evalOne); msg != "" {
			return "", false, msg
		} else if len(padded) == sig.params {
			args = padded
		} else {
			return "", false, fmt.Sprintf("arity mismatch for %s: want %d, got %d", name, sig.params, len(args))
		}
	}
	// 局部箭头捕获：调用点把捕获名按序追加为尾随实参（封存 lowerCall 的
	// captureSig 实参拼接 :1336-1343 同序）；接收者捕获追传定义域 thisSelf
	//（按值同值捕获口径；定义域外调用无绑定即大声拒，禁静默错位）。
	args = append(args, sig.arrowCaps...)
	if sig.arrowThis {
		if scope.thisSelf == "" {
			return "", false, "this capture outside a method is not lowerable"
		}
		args = append(args, scope.thisSelf)
	}
	call := fmt.Sprintf("call @%s(%s)", callName, strings.Join(args, ", "))
	if sig.isVoid {
		w.Write(fmt.Sprintf("  %s\n", call))
		return "", true, ""
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", t, call))
	// 调用结果临时量归属（用后仍须返前释放；封存 lowerCall 各分支 ownTemp）。
	saOwnTemp(scope, t)
	// 工厂句柄种直记（`const v = mk()` 经返回种得布局，调用方按种分发；
	// 封存 checker_layout l1；非 inst 种沿旧路）。
	if strings.HasPrefix(sig.retKind, "inst:") {
		if _, ok := scope.classes[sig.retKind[5:]]; ok {
			scope.types[t] = sig.retKind
		}
	}
	return t, false, ""
}

// saEvalI32 求 i32 操作数并按需发射临时量（形状证据：封存 lowerBinary:3214-3324
// saLowerPrefixUnary lowering 前缀一元（证据：封存 lowerPrefixUnary:3595-3619：
// 数字面正负折叠；`-x` 为 `sub 0, x`；`!x` 为 `eq x, 0`；其余大声拒）。
func saLowerPrefixUnary(w printer.EmitTextWriter, un *ast.PrefixUnaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if un.Operand != nil && un.Operand.Kind == ast.KindNumericLiteral &&
		(un.Operator == ast.KindMinusToken || un.Operator == ast.KindPlusToken) {
		t := un.Operand.Text()
		if saIsFloatLit(t) {
			return "", "float literal not in i32 subset"
		}
		if un.Operator == ast.KindMinusToken {
			return "-" + t, ""
		}
		return t, ""
	}
	switch un.Operator {
	case ast.KindPlusPlusToken, ast.KindMinusMinusToken:
		// 前缀返回新值（证据：封存 lowerIncDec:3632 + lowerPrefixUnary:3598-3601）。
		return saLowerIncDec(w, un.Operand, un.Operator == ast.KindPlusPlusToken, true, scope, pos, refusals, nextTemp)
	case ast.KindMinusToken:
		arg, msg := saEvalI32(w, un.Operand, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// 取反记种检查（串/实例句柄禁作整数；`!` 沿上游同形不动；铁律 4）。
		if msg := saCheckI32Value(scope, arg); msg != "" {
			return "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sub 0, %s\n", t, arg))
		return t, ""
	case ast.KindExclamationToken:
		var arg string
		if un.Operand != nil && un.Operand.Kind == ast.KindIdentifier {
			nm := un.Operand.Text()
			if _, ok := scope.types[nm]; !ok {
				return "", "unknown variable " + nm
			}
			arg = nm
		} else {
			var msg string
			arg, msg = saEvalI32(w, un.Operand, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", t, arg))
		return t, ""
	default:
		return "", fmt.Sprintf("prefix operator %s not in subset", un.Operator.String())
	}
}

// saLowerPostfixUnary lowering 后缀一元（证据：封存 lowerPostfixUnary:3621-3630
// + lowerIncDec:3632：`++`/`--` 皆可；后缀返回旧值）。
func saLowerPostfixUnary(w printer.EmitTextWriter, un *ast.PostfixUnaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if un.Operator != ast.KindPlusPlusToken && un.Operator != ast.KindMinusMinusToken {
		return "", fmt.Sprintf("postfix operator %s not in subset", un.Operator.String())
	}
	return saLowerIncDec(w, un.Operand, un.Operator == ast.KindPlusPlusToken, false, scope, pos, refusals, nextTemp)
}

// saLowerIncDec lowering 自增（prefix=true 返回新值，false 返回旧值；仅 i32 绑定）。
func saLowerIncDec(w printer.EmitTextWriter, operand *ast.Node, up, prefix bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	target, ok := saBoundI32(scope, operand)
	if !ok {
		// 顶层可变槽自增（读-改-写回；旧值/新值语义同本地；i32 独占，串槽大声拒；
		// 形状证据：封存 emitModIncDec:1191-1228）。
		if operand != nil && operand.Kind == ast.KindIdentifier {
			if ms, ok := scope.modVars[operand.Text()]; ok && ms.w == "i32" {
				if _, shadowed := scope.types[operand.Text()]; !shadowed {
					cur := saModLoadI32(w, ms, scope, nextTemp)
					op := "add"
					if !up {
						op = "sub"
					}
					nw := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = %s %s, 1\n", nw, op, cur))
					saModStoreI32(w, ms, nw, scope, nextTemp)
					if prefix {
						return nw, ""
					}
					return cur, ""
				}
			}
			// 串槽自增形状证据：封存 emitModIncDec:1195-1199 串拒因。
			if ms, ok := scope.modVars[operand.Text()]; ok && ms.w == "str" {
				if _, shadowed := scope.types[operand.Text()]; !shadowed {
					return "", fmt.Sprintf("++/-- on string module state %s is not lowerable", ms.qual)
				}
			}
		}
		// 命名空间可变槽自增（`N.K++`；读-改-写回同序；i32 独占）。
		if operand != nil && operand.Kind == ast.KindPropertyAccessExpression {
			lpa := operand.AsPropertyAccessExpression()
			if lpa.Expression != nil && lpa.Expression.Kind == ast.KindIdentifier && lpa.Name() != nil && lpa.Name().Kind == ast.KindIdentifier {
				if ms, ok := scope.modVars[lpa.Expression.Text()+"."+lpa.Name().Text()]; ok && ms.w == "i32" {
					cur := saModLoadI32(w, ms, scope, nextTemp)
					op := "add"
					if !up {
						op = "sub"
					}
					nw := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = %s %s, 1\n", nw, op, cur))
					saModStoreI32(w, ms, nw, scope, nextTemp)
					if prefix {
						return nw, ""
					}
					return cur, ""
				}
			}
		}
		if op, msg, handled := saLowerFieldIncDec(w, operand, up, prefix, scope, nextTemp); handled {
			return op, msg
		}
		return "", "incdec target must be bound i32 variable"
	}
	op := "add"
	if !up {
		op = "sub"
	}
	if prefix {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
		saStoreLocal(w, target, t, scope, nextTemp)
		return t, ""
	}
	old := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	// 后缀快照须非移动拷贝（H17：裸 `old = x` 即 move，后读陷阱；
	// 上游 `add x, 0` 同形）。
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", old, target))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
	saStoreLocal(w, target, t, scope, nextTemp)
	return old, ""
}

// saLowerPowOps 整数幂（R3-12 回迁映射：语义由 `sci/sa_std/ts_math.sa`
// `@ts_math_ipow` 实现，与 Math.pow 同核；形状证据同 saLowerPow）。
func saLowerPowOps(w printer.EmitTextWriter, base, expo string, scope *saScope, nextLabel *int, nextTemp *int) string {
	scope.addImport("sa_std/ts_math.sa")
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_ipow(%s, %s)\n", res, base, expo))
	saOwnTemp(scope, res)
	return res
}

// saLowerPow lowering 整数 `**`（形状证据：封存 lowerPowLoop:3326-3350：
// r=1；ctr=expo；top: cc=sgt ctr,0；br body/end；body: r*=base, ctr--；jmp top）。
func saLowerPow(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	base, msgB := saEvalI32(w, be.Left, scope, pos, refusals, nextTemp)
	if msgB != "" {
		return "", msgB
	}
	expo, msgE := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msgE != "" {
		return "", msgE
	}
	return saLowerPowOps(w, base, expo, scope, scope.nextLabel, nextTemp), ""
}

// saEvalI32 求 i32 操作数并按需发射临时量（形状证据：封存 lowerBinary:3214-3324
// add/sub/mul/div/srem/shl/ashr/lshr/and/or/xor、eq/ne/slt/sle/sgt/sge；`**`
// 走 lowerPowLoop:3326-3350；一元见 lowerPrefixUnary:3595-3619）。
// 返回 (operand, errMsg)，errMsg 非空即失败（调用方按上下文包装定位拒绝）。
// saArrIdentOperand resolves an arr-handle identifier operand for direct equality
// comparison (enum instantiations and other handle bindings against i32 values).
func saArrIdentOperand(e *ast.Node, scope *saScope) (string, bool) {
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "arr" {
			return e.Text(), true
		}
	}
	return "", false
}

// saIsF64Operand reports whether an operand carries f64 (float literal or f64 binding).
func saIsF64Operand(e *ast.Node, scope *saScope) bool {
	for e != nil && e.Kind == ast.KindParenthesizedExpression {
		e = e.AsParenthesizedExpression().Expression
	}
	if e == nil {
		return false
	}
	if e.Kind == ast.KindNumericLiteral && saIsFloatLit(e.Text()) {
		return true
	}
	if e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "f64" {
			return true
		}
	}
	// 前缀取负透传（`-x` 种随操作数：绑定/字面量/嵌套取负递归判定，f64
	// 调用经返回种；`+x` 双边同拒不动；求值见严格位同形臂；消费位皆经
	// 严格求值，无 `Text()` 直通风险）。
	if e.Kind == ast.KindPrefixUnaryExpression {
		un := e.AsPrefixUnaryExpression()
		if un != nil && un.Operator == ast.KindMinusToken {
			if saIsF64Operand(un.Operand, scope) {
				return true
			}
			if un.Operand != nil && un.Operand.Kind == ast.KindCallExpression {
				if k, ok := saCallRetKind(un.Operand.AsCallExpression(), scope); ok && k == "f64" {
					return true
				}
			}
		}
	}
	return false
}

// saEvalF64Strict evaluates a strict f64 operand (float literal, f64 binding, or
// float-only arithmetic; int literals and mixed shapes refuse loudly).
// saF64Side lowers one f64-binary side (float sides pass text through like the
// upstream type-driven emission; other sides evaluate as i32).
func saF64Side(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	orig := e
	for e != nil && e.Kind == ast.KindParenthesizedExpression {
		e = e.AsParenthesizedExpression().Expression
	}
	if e == nil {
		return "", "missing expression"
	}
	// 前缀取负侧经严格求值（操作数为浮即 `-x` 落 `fneg` 临时量；文本快捷
	// 仅字面量/绑定，前缀须先行——`Text()` 无前缀形，误触即 panic；
	// 其余前缀沿下 i32 旧路，行为不变）。
	if e != nil && e.Kind == ast.KindPrefixUnaryExpression {
		if un := e.AsPrefixUnaryExpression(); un != nil && un.Operator == ast.KindMinusToken && saIsF64Operand(un.Operand, scope) {
			op, msg := saEvalF64Strict(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			return op, ""
		}
	}
	if saIsF64Operand(e, scope) {
		return e.Text(), ""
	}
	return saEvalI32(w, orig, scope, pos, refusals, nextTemp)
}

func saEvalF64Strict(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil {
		return "", "missing expression"
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		if saIsFloatLit(e.Text()) {
			return e.Text(), ""
		}
		return "", "integer " + e.Text() + " in float expression"
	case ast.KindCallExpression:
		// f64 种调用直传（用户 f64 函数 + Number/parseFloat 转换经调用核）。
		if k, ok := saCallRetKind(e.AsCallExpression(), scope); ok && k == "f64" {
			op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if voidCall {
				return "", "void function call in value position"
			}
			return op, ""
		}
		return "", "unsupported float expression"
	case ast.KindIdentifier:
		if k, ok := scope.types[e.Text()]; ok && k == "f64" {
			return e.Text(), ""
		}
		return "", e.Text() + " is not a float"
	case ast.KindParenthesizedExpression:
		return saEvalF64Strict(w, e.AsParenthesizedExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindPrefixUnaryExpression:
		// 前缀取负（`-x` 经 `fneg`，与上游实发同形；`+x` 双边同拒沿旧门）。
		if un := e.AsPrefixUnaryExpression(); un != nil && un.Operator == ast.KindMinusToken {
			v, msg := saEvalF64Strict(w, un.Operand, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = fneg %s\n", t, v))
			scope.types[t] = "f64"
			return t, ""
		}
		return "", "unsupported float expression"
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		var fop string
		switch saBinaryOpKind(be) {
		case ast.KindPlusToken:
			fop = "fadd"
		case ast.KindMinusToken:
			fop = "fsub"
		case ast.KindAsteriskToken:
			fop = "fmul"
		case ast.KindSlashToken:
			fop = "fdiv"
		case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
			fop = "fcmp_eq"
		case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
			fop = "fcmp_ne"
		case ast.KindLessThanToken:
			fop = "fcmp_lt"
		case ast.KindLessThanEqualsToken:
			fop = "fcmp_le"
		case ast.KindGreaterThanToken:
			fop = "fcmp_gt"
		case ast.KindGreaterThanEqualsToken:
			fop = "fcmp_ge"
		default:
			return "", "float operator is not lowerable"
		}
		l, msgL := saF64Side(w, be.Left, scope, pos, refusals, nextTemp)
		if msgL != "" {
			return "", msgL
		}
		r, msgR := saF64Side(w, be.Right, scope, pos, refusals, nextTemp)
		if msgR != "" {
			return "", msgR
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, fop, l, r))
		if fop == "fadd" || fop == "fsub" || fop == "fmul" || fop == "fdiv" {
			scope.types[t] = "f64"
		}
		return t, ""
	default:
		return "", "unsupported float expression"
	}
}

// saEvalF64 evaluates an f64 initializer (int/float literal text binds directly,
// otherwise strict float rules apply).
func saEvalF64(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e != nil && e.Kind == ast.KindNumericLiteral {
		return e.Text(), ""
	}
	return saEvalF64Strict(w, e, scope, pos, refusals, nextTemp)
}

// saUnwrapTransparent peels pure type-level wrappers (no value semantics).
func saUnwrapTransparent(e *ast.Node) *ast.Node {
	for e != nil {
		switch e.Kind {
		case ast.KindAsExpression:
			e = e.AsAsExpression().Expression
		case ast.KindSatisfiesExpression:
			e = e.AsSatisfiesExpression().Expression
		case ast.KindNonNullExpression:
			e = e.AsNonNullExpression().Expression
		case ast.KindTypeAssertionExpression:
			e = e.AsTypeAssertion().Expression
		case ast.KindParenthesizedExpression:
			e = e.AsParenthesizedExpression().Expression
		default:
			return e
		}
	}
	return e
}

// saCheckI32Value 守 i32 值位记种（串/实例句柄禁入；求值只建种不验种，验种在
// 各 i32 值位（存/实参/下标）；沿用 string/instance value 文族；铁律 4）。
func saCheckI32Value(scope *saScope, v string) string {
	if k, ok := scope.types[v]; ok && (k == "str" || (len(k) > 5 && k[:5] == "inst:")) {
		if k == "str" {
			return "string value in i32 expression"
		}
		return "instance value in i32 expression"
	}
	return ""
}

func saEvalI32(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil {
		return "", "missing expression"
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		t := e.Text()
		if saIsFloatLit(t) {
			return "", "float literal " + t + " not in i32 subset"
		}
		return t, ""
	case ast.KindTrueKeyword:
		return "1", ""
	case ast.KindFalseKeyword:
		return "0", ""
	case ast.KindNullKeyword, ast.KindUndefinedKeyword:
		// 子集 null/undefined 即 0（越界归零、`== null` 句柄、缺值；
		// 形状证据：封存 lowerExpr:2731-2734）。
		return "0", ""
	case ast.KindIdentifier:
		nm := e.Text()
		if k, ok := scope.types[nm]; ok {
			if k == "arr" {
				return "", "array " + nm + " in i32 expression"
			}
			if k == "str" {
				return "", "string " + nm + " in i32 expression"
			}
			// date millis i32 位直通（i64 寄存器低 32 位截断，调用位 3747 口径同源；上游逐字节同形 `sgt t, 0`；封存无 date 特判）。
			if k == "date" {
				return nm, ""
			}
			if k != "i32" && k != "bool" {
				return "", k + " " + nm + " in i32 expression"
			}
			if k != "i32" {
				return "", "boolean " + nm + " in i32 expression"
			}
			return nm, ""
		}
		// 顶层可变槽读（局部遮蔽优先上；被赋值名永不折叠故与 topConsts 无交；
		// 形状证据：封存 modStateOf:265-279 + emitModLoad:901-938）。
		if ms, ok := scope.modVars[nm]; ok {
			return saModLoadI32(w, ms, scope, nextTemp), ""
		}
		// 顶层纯量折叠读（局部遮蔽优先上；封存 lowerExpr:2775）。
		if text, ok := scope.topConsts[nm]; ok {
			if scope.topStr[nm] {
				return "", "string " + nm + " in i32 expression"
			}
			return text, ""
		}
		// 未绑定 `undefined` 即 0（子集 null 即 0；遮蔽/顶层量优先上）。
		if nm == "undefined" {
			return "0", ""
		}
		return "", "unknown variable " + nm
	case ast.KindThisKeyword:
		if scope.thisSelf == "" {
			return "", "this outside a class method is not lowerable"
		}
		return scope.thisSelf, ""
	case ast.KindConditionalExpression:
		// 三元 i32 臂（与 return/声明位同核；串臂在此拒）。
		t, isStr, msg := saLowerTernaryValue(w, e.AsConditionalExpression(), e, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
		if msg != "" {
			return "", msg
		}
		if isStr {
			return "", "string ternary in i32 expression"
		}
		if k, ok := scope.types[t]; ok && k == "f64" {
			return "", "float ternary in i32 expression"
		}
		return t, ""
	case ast.KindNewExpression:
		// 实例只可经声明绑定（`const o = new C()`）；值位大声拒。
		ne := e.AsNewExpression()
		if ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier {
			if ne.Expression.Text() == "Date" {
				return "", "new Date(x) is not lowerable (only arg-less now-shape)"
			}
			if ne.Expression.Text() == "RegExp" {
				return "", "regex value needs a regex binding (const re = /.../ or new RegExp)"
			}
			if _, ok := scope.classes[ne.Expression.Text()]; ok {
				return "", "instance in i32 expression (bind it first)"
			}
			return "", "unknown class " + ne.Expression.Text()
		}
		return "", "new expression is not lowerable"
	case ast.KindElementAccessExpression:
		return saLowerIndexLoadExpr(w, e.AsElementAccessExpression(), scope, pos, refusals, nextTemp)
	case ast.KindPropertyAccessExpression:
		pa := e.AsPropertyAccessExpression()
		// super.f 读基布局（存取器走基 getter 内联；形状证据：封存 checkSuperAccess:253-278）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindSuperKeyword && pa.Name() != nil {
			// 私有域永不过 super（TS 恒错；封存 8273-8282）。
			if strings.HasPrefix(pa.Name().Text(), "#") {
				return "", fmt.Sprintf("private field %s is not accessible via super", pa.Name().Text())
			}
			bdef, h, msg := saSuperBase(scope)
			if msg != "" {
				return "", msg
			}
			if _, ok := bdef.offsets[pa.Name().Text()]; ok {
				t, msg := saLowerClassFieldLoad(w, h, bdef, pa.Name().Text(), scope, nextTemp)
				if msg != "" {
					return "", msg
				}
				return t, ""
			}
			v, msg := saInlineGetter(w, h, bdef, pa.Name().Text(), scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
			if msg != "" {
				return "", msg
			}
			return v, ""
		}
		// Number 整形常量折叠（`MAX_VALUE` 等；形状证据：封存 stdlib.go:128-130）。
		if v, ok := saNumberConst(pa); ok {
			return v, ""
		}
		// 嵌套对象链读（`q.p.a` 经内层句柄逐级解；叶子按布局读/getter 内联；
		// 非 inst 链节沿旧门；形状证据：封存 lowerMemberChain）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression && pa.Name() != nil {
			ch, cdef, msg := saChainBase(w, pa.Expression, scope, pos, refusals, nextTemp)
			if msg == "" {
				if _, ok := cdef.offsets[pa.Name().Text()]; ok {
					t, msg := saLowerClassFieldLoad(w, ch, cdef, pa.Name().Text(), scope, nextTemp)
					if msg != "" {
						return "", msg
					}
					return t, ""
				}
				v, msg := saInlineGetter(w, ch, cdef, pa.Name().Text(), scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
				if msg != "" {
					return "", msg
				}
				return v, ""
			}
		}
		// 实例字段读（`o.f`/`this.f`；静态成员大声拒；私有域按词法属主解）。
		// this 置空（静态体内）时成员读即越界，沿裸 this 同门拒。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword && scope.thisSelf == "" {
			return "", "this outside a class method is not lowerable"
		}
		if pa.Name() != nil && saCouldBeInst(pa.Expression, scope) {
			h, def, msg := saInstBase(pa.Expression, scope)
			if msg != "" {
				return "", msg
			}
			// map 索引实例基（`m["a"].x`；saInstBase 只认标识符/this）。
			if def == nil && pa.Expression != nil && (pa.Expression.Kind == ast.KindElementAccessExpression || pa.Expression.Kind == ast.KindCallExpression || pa.Expression.Kind == ast.KindNewExpression) {
				h, def, msg = saInstBaseElem(w, pa.Expression, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
			}
			if def == nil {
				return "", "instance base did not resolve to a recorded layout"
			}
			fname := pa.Name().Text()
			if strings.HasPrefix(fname, "#") {
				key, msg := saPrivResolve(def, fname, scope.thisClass)
				if msg != "" {
					return "", msg
				}
				t, msg := saLowerClassFieldLoad(w, h, def, key, scope, nextTemp)
				if msg != "" {
					return "", msg
				}
				return t, ""
			}
			if _, ok := def.offsets[fname]; ok {
				// `b?.v` 空守卫 join（底座复用；封存 lowerGuardedProperty）。
				if pa.QuestionDotToken != nil {
					t, msg := saLowerGuardedFieldLoad(w, h, def, pa.Name().Text(), scope, nextTemp)
					if msg != "" {
						return "", msg
					}
					return t, ""
				}
				t, msg := saLowerClassFieldLoad(w, h, def, pa.Name().Text(), scope, nextTemp)
				if msg != "" {
					return "", msg
				}
				return t, ""
			}
			// 实例基静态字面量折叠（`c.N`；实例槽优先；封存 lowerExpr:8013-8041）。
			if op, kind, ok := saStaticFold(w, pa.Expression, pa.Name().Text(), scope, nextTemp); ok {
				if kind == "str" {
					return "", "string " + pa.Name().Text() + " in i32 expression"
				}
				return op, ""
			}
			// 存取器读内联 getter 体（形状证据：封存 lowerExpr:8130-8137）。
			v, msg := saInlineGetter(w, h, def, pa.Name().Text(), scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
			if msg != "" {
				return "", msg
			}
			return v, ""
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
			// 私有静态读（`C.#K`，类名基 + 属主一致；封存 8029-8064）。
			if def, key, msg, ok := saPrivStaticKey(pa.Expression, pa.Name().Text(), scope); msg != "" {
				return "", msg
			} else if ok {
				sv := def.statics[key]
				if sv.kind == "str" {
					return "", "string " + pa.Name().Text() + " in i32 expression"
				}
				return sv.text, ""
			}
			// 类名基静态字面量折叠（`C.K`；串在 i32 位拒；封存 lowerExpr:8013-8041）。
			if op, kind, ok := saStaticFold(w, pa.Expression, pa.Name().Text(), scope, nextTemp); ok {
				if kind == "str" {
					return "", "string " + pa.Name().Text() + " in i32 expression"
				}
				return op, ""
			}
			// 类名基静态存取器读（`C.g` 空 this 内联；裸类读实例 getter 大声拒；
			// 未知静态下探 loud（枚举/Math/值位门），与 `C.m()` 同形；
			// 镜像 `C.m()` 静态分发，遮蔽门同形；形状证据：封存 lowerClassStaticCall 存取器位）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
				if def, ok := scope.classes[pa.Expression.Text()]; ok {
					if _, shadowed := scope.types[pa.Expression.Text()]; !shadowed {
						if _, shadowed := scope.funcs[pa.Expression.Text()]; !shadowed {
							if _, ok := def.staticGetters[pa.Name().Text()]; ok {
								v, msg := saInlineStaticGetter(w, pa.Expression.Text(), def, pa.Name().Text(), scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
								if msg != "" {
									return "", msg
								}
								return v, ""
							}
							if _, ok := def.getters[pa.Name().Text()]; ok {
								return "", fmt.Sprintf("getter %s.%s is an instance getter (static reads need a static getter)", pa.Expression.Text(), pa.Name().Text())
							}
						}
					}
				}
			}
			// 命名空间拍扁纯量读（`N.K` 键；串在 i32 位沿静态折叠同门拒）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
				// 可变槽优先（活值；折叠与槽互斥，槽即被赋值名；封存 emitModEnsure 系列）。
				if ms, ok := scope.modVars[pa.Expression.Text()+"."+pa.Name().Text()]; ok && ms.w == "i32" {
					return saModLoadI32(w, ms, scope, nextTemp), ""
				}
				if text, ok := scope.topConsts[pa.Expression.Text()+"."+pa.Name().Text()]; ok {
					if scope.topStr[pa.Expression.Text()+"."+pa.Name().Text()] {
						return "", "string " + pa.Name().Text() + " in i32 expression"
					}
					return text, ""
				}
			}
		}
		// 整数枚举成员折叠（`E.A` → 字面量；串/计算成员拒，整数成员照折；
		// 未知成员大声拒；形状证据：封存 recordEnum:9308-9342 + 7961-8000）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
			if members, ok := scope.enums[pa.Expression.Text()]; ok {
				if msg, bad := saEnumNonIntMsg(pa.Expression.Text(), pa.Name().Text(), scope); bad {
					return "", msg
				}
				if v, ok := members[pa.Name().Text()]; ok {
					return fmt.Sprintf("%d", v), ""
				}
				return "", "unknown enum member " + pa.Name().Text()
			}
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Math" &&
			pa.Name() != nil {
			// `Math.PI`/`Math.E` 折叠为 3/2（形状证据：封存 stdlib.go:126-127
			// `@const:3`/`@const:2` integer subset）。
			switch pa.Name().Text() {
			case "PI":
				return "3", ""
			case "E":
				return "2", ""
			}
		}
		return saLowerLengthExpr(w, e.AsPropertyAccessExpression(), scope, pos, refusals, nextTemp)
	case ast.KindParenthesizedExpression:
		return saEvalI32(w, e.AsParenthesizedExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindAsExpression:
		// 纯类型级（TypeEraser 已擦类型，值层直通）。
		return saEvalI32(w, e.AsAsExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindSatisfiesExpression:
		return saEvalI32(w, e.AsSatisfiesExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindTypeAssertionExpression:
		return saEvalI32(w, e.AsTypeAssertion().Expression, scope, pos, refusals, nextTemp)
	case ast.KindNonNullExpression:
		return saEvalI32(w, e.AsNonNullExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindVoidExpression:
		// `void expr` 求值为 0，副作用保留（证据：封存 lowerExpr:2739-2743）。
		if _, msg := saEvalI32(w, e.AsVoidExpression().Expression, scope, pos, refusals, nextTemp); msg != "" {
			return "", msg
		}
		return "0", ""
	case ast.KindPrefixUnaryExpression:
		return saLowerPrefixUnary(w, e.AsPrefixUnaryExpression(), scope, pos, refusals, nextTemp)
	case ast.KindPostfixUnaryExpression:
		return saLowerPostfixUnary(w, e.AsPostfixUnaryExpression(), scope, pos, refusals, nextTemp)
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindEqualsToken {
			// 值位赋值折成寄存器拷贝（证据：封存 lowerBinary:3009-3010）。
			// 串目标走串求值（同形拷贝）。
			if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
				if k, ok := scope.types[be.Left.Text()]; ok && k == "str" {
					op, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					w.Write(fmt.Sprintf("  %s = %s\n", be.Left.Text(), op))
					return be.Left.Text(), ""
				}
			}
			target, ok := saBoundI32(scope, be.Left)
			if !ok {
				// 顶层可变槽写（`x = v`；局部遮蔽优先上；返回右值；
				// i32/串按宽分发，计算串大声拒；
				// 形状证据：封存 emitModStore 系列 + modWiden:822-882 + emitModStoreStringDispatch:808-820）。
				if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
					if ms, ok := scope.modVars[be.Left.Text()]; ok {
						if _, shadowed := scope.types[be.Left.Text()]; !shadowed {
							if ms.w == "str" {
								text, ok := saModStrText(be.Right, scope)
								if !ok {
									return "", fmt.Sprintf("module state %s stores string literals and string constants only (computed strings are not lowerable yet)", ms.qual)
								}
								return saModStoreStr(w, ms, text, scope, nextTemp), ""
							}
							op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
							if msg != "" {
								return "", msg
							}
							return saModStoreI32(w, ms, op, scope, nextTemp), ""
						}
					}
				}
				// super.f 写基布局（存取器走基 setter 内联；形状证据同读位）。
				if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
					lpa := be.Left.AsPropertyAccessExpression()
					if lpa.Expression != nil && lpa.Expression.Kind == ast.KindSuperKeyword && lpa.Name() != nil {
						if strings.HasPrefix(lpa.Name().Text(), "#") {
							return "", fmt.Sprintf("private field %s is not accessible via super", lpa.Name().Text())
						}
						bdef, h, msg := saSuperBase(scope)
						if msg != "" {
							return "", msg
						}
						if _, ok := bdef.offsets[lpa.Name().Text()]; ok {
							// str 域右值走串求值存头指针；i32 域走值求值。
							if bdef.fkinds[lpa.Name().Text()] == "str" {
								sop, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
								if msg != "" {
									return "", msg
								}
								if msg := saLowerClassFieldStore(w, h, bdef, lpa.Name().Text(), sop); msg != "" {
									return "", msg
								}
								return sop, ""
							}
							op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
							if msg != "" {
								return "", msg
							}
							if msg := saLowerClassFieldStore(w, h, bdef, lpa.Name().Text(), op); msg != "" {
								return "", msg
							}
							return op, ""
						}
						op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", msg
						}
						if msg := saInlineSetter(w, h, bdef, lpa.Name().Text(), op, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp); msg != "" {
							return "", msg
						}
						return op, ""
					}
				}
				// 实例字段写（`o.f = v`；setter 走一参体内联；返回右值；str 域右值走串求值）。
				if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
					lpa := be.Left.AsPropertyAccessExpression()
					if lpa.Name() != nil && saCouldBeInst(lpa.Expression, scope) {
						h, def, msg := saInstBase(lpa.Expression, scope)
						if msg != "" {
							return "", msg
						}
						// map 索引实例基（`m[k].f = v`；saInstBase 只认标识符/this；
						// 与 i32 读位 2450-2459 同形；否则 nil 解引用崩溃）。
						if def == nil && lpa.Expression != nil && (lpa.Expression.Kind == ast.KindElementAccessExpression || lpa.Expression.Kind == ast.KindCallExpression || lpa.Expression.Kind == ast.KindNewExpression) {
							h, def, msg = saInstBaseElem(w, lpa.Expression, scope, pos, refusals, nextTemp)
							if msg != "" {
								return "", msg
							}
						}
						if def == nil {
							return "", "instance base did not resolve to a recorded layout"
						}
						fname := lpa.Name().Text()
						if strings.HasPrefix(fname, "#") {
							key, msg := saPrivResolve(def, fname, scope.thisClass)
							if msg != "" {
								return "", msg
							}
							fname = key
						}
						if _, ok := def.offsets[fname]; ok {
							if def.fkinds[fname] == "str" {
								sop, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
								if msg != "" {
									return "", msg
								}
								if msg := saLowerClassFieldStore(w, h, def, fname, sop); msg != "" {
									return "", msg
								}
								return sop, ""
							}
							op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
							if msg != "" {
								return "", msg
							}
							if msg := saLowerClassFieldStore(w, h, def, fname, op); msg != "" {
								return "", msg
							}
							return op, ""
						}
						op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", msg
						}
						if msg := saInlineSetter(w, h, def, lpa.Name().Text(), op, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp); msg != "" {
							return "", msg
						}
						return op, ""
					}
				}
				// 嵌套链写（`q.p.a = v` 经内层句柄；叶子按布局存，inst 叶拒；
				// 链 setter 不 tran，沿旧门；形状证据：封存 saChainBase）。
				// 类名基址只读（写侧沿上游 layoutOfVar 口径拒；形状证据：封存 lowerFieldStore:8366
				// `l := e.layoutOfVar(segs[0])`，读侧 layoutOfNode:408 才含 checker 回退）。
				if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
					lpa := be.Left.AsPropertyAccessExpression()
					if lpa.Expression != nil && lpa.Expression.Kind == ast.KindPropertyAccessExpression && lpa.Name() != nil {
						root := lpa.Expression
						for root != nil && root.Kind == ast.KindPropertyAccessExpression {
							root = root.AsPropertyAccessExpression().Expression
						}
						isBareClass := false
						if root != nil && root.Kind == ast.KindIdentifier {
							if _, ok := scope.classes[root.Text()]; ok {
								if _, shadowed := scope.funcs[root.Text()]; !shadowed {
									if k, ok := scope.types[root.Text()]; !ok || len(k) <= 5 || k[:5] != "inst:" {
										isBareClass = true
									}
								}
							}
						}
						if !isBareClass {
							if ch, cdef, msg := saChainBase(w, lpa.Expression, scope, pos, refusals, nextTemp); msg == "" {
								fname := lpa.Name().Text()
								if _, ok := cdef.offsets[fname]; ok {
									switch cdef.fkinds[fname] {
									case "str":
										sop, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
										if msg != "" {
											return "", msg
										}
										if msg := saLowerClassFieldStore(w, ch, cdef, fname, sop); msg != "" {
											return "", msg
										}
										return sop, ""
									case "arr":
										v, msg := saArrValueOf(w, be.Right, scope, pos, refusals, nextTemp)
										if msg != "" {
											return "", msg
										}
										if msg := saLowerClassFieldStore(w, ch, cdef, fname, v); msg != "" {
											return "", msg
										}
										return v, ""
									case "inst":
										return "", "nested object reassignment needs a constructed handle"
									default:
										op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
										if msg != "" {
											return "", msg
										}
										if msg := saLowerClassFieldStore(w, ch, cdef, fname, op); msg != "" {
											return "", msg
										}
										return op, ""
									}
								}
							}
						}
					}
				}
				// 类名基静态存取器写（`C.s = v` 空 this 内联，返回右值；
				// 裸类写实例 setter 大声拒；遮蔽门与读位同形）。
				if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
					lpa := be.Left.AsPropertyAccessExpression()
					if lpa.Expression != nil && lpa.Expression.Kind == ast.KindIdentifier && lpa.Name() != nil {
						if def, ok := scope.classes[lpa.Expression.Text()]; ok {
							if _, shadowed := scope.types[lpa.Expression.Text()]; !shadowed {
								if _, shadowed := scope.funcs[lpa.Expression.Text()]; !shadowed {
									if _, ok := def.staticSetters[lpa.Name().Text()]; ok {
										op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
										if msg != "" {
											return "", msg
										}
										if msg := saInlineStaticSetter(w, lpa.Expression.Text(), def, lpa.Name().Text(), op, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp); msg != "" {
											return "", msg
										}
										return op, ""
									}
									if _, ok := def.setters[lpa.Name().Text()]; ok {
										return "", fmt.Sprintf("setter %s.%s is an instance setter (static writes need a static setter)", lpa.Expression.Text(), lpa.Name().Text())
									}
								}
							}
						}
					}
				}
				// 命名空间可变槽写（`N.K = v`；读位同键；i32/串按宽分发，计算串
				// 大声拒；封存 emitModStore 系列 + modWiden/emitModStoreStringDispatch）。
				if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
					lpa := be.Left.AsPropertyAccessExpression()
					if lpa.Expression != nil && lpa.Expression.Kind == ast.KindIdentifier && lpa.Name() != nil && lpa.Name().Kind == ast.KindIdentifier {
						if ms, ok := scope.modVars[lpa.Expression.Text()+"."+lpa.Name().Text()]; ok {
							if ms.w == "str" {
								text, ok := saModStrText(be.Right, scope)
								if !ok {
									return "", fmt.Sprintf("module state %s stores string literals and string constants only (computed strings are not lowerable yet)", ms.qual)
								}
								return saModStoreStr(w, ms, text, scope, nextTemp), ""
							}
							if ms.w == "i32" {
								op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
								if msg != "" {
									return "", msg
								}
								return saModStoreI32(w, ms, op, scope, nextTemp), ""
							}
						}
					}
				}
				return "", "assignment to unknown/non-i32 variable"
			}
			op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			// i32 目标记种检查（串/实例句柄禁入；str 目标沿上串分支；铁律 4）。
			if msg := saCheckI32Value(scope, op); msg != "" {
				return "", msg
			}
			saStoreLocal(w, target, op, scope, nextTemp)
			return target, ""
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindInKeyword {
			// `in` 静态折叠须先于一切求值门（左为串字面量键）。
			return saLowerInFold(w, be, scope, pos, refusals, nextTemp)
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindQuestionQuestionToken {
			// `??` 空合槽须先于串门（i32 位，右惰性；串臂走串槽，i32 位大声拒，
			// 禁串句柄误作整数，铁律 4 高于同形）。
			// 实例空合同行：同布局双实例或左实例+空右臂走 ptr 通用槽（与返回位同形）。
			if saIsInstOperandSyntax(be.Left, scope) && (saIsNullLit(be.Right, scope) || saIsInstOperandSyntax(be.Right, scope)) {
				return saLowerNullishInst(w, be, scope, pos, refusals, nextTemp)
			}
			// 混合臂禁入 i32 槽（实例句柄误作整数即静默错码；与串门同形大声拒）。
			if saIsInstOperandSyntax(be.Left, scope) || saIsInstOperandSyntax(be.Right, scope) ||
				saCouldBeInst(be.Left, scope) || saCouldBeInst(be.Right, scope) {
				return "", "instance nullish arms must share a layout (or use null fallback)"
			}
			if saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope) {
				return "", "string value in i32 expression"
			}
			return saLowerNullish(w, be, scope, pos, refusals, nextTemp)
		}
		if be.OperatorToken != nil && saIsLogicAssignOp(be.OperatorToken.Kind) {
			// 短路赋值值位（i32/bool；串 out 为新鲜临时量，薄口无串种跟踪，
			// 串目标只走语句位；封存 lowerLogicAssign 值形另见调用点类型环境）。
			if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
				if k, ok := scope.types[be.Left.Text()]; ok && k == "str" {
					return "", "string logic assignment is statement-only"
				}
			}
			return saLowerLogicAssign(w, be, scope, pos, refusals, nextTemp)
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindPlusToken &&
			(saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)) {
			// `+` 遇串位即拼接，串句柄只可由串位取用（saEvalStr）；i32 位拒收。
			// 此处不落字（落字只走 saEvalStr 串路径），直接定位拒绝。
			return "", "string value in i32 expression"
		}
		if be.OperatorToken != nil && (be.OperatorToken.Kind == ast.KindEqualsEqualsToken ||
			be.OperatorToken.Kind == ast.KindEqualsEqualsEqualsToken ||
			be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
			be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken) &&
			((be.Left != nil && be.Left.Kind == ast.KindTypeOfExpression) ||
				(be.Right != nil && be.Right.Kind == ast.KindTypeOfExpression)) {
			// typeof 比较对（守卫 + 常量折叠；形状证据：封存 typeof_guard.go 全文件）。
			return saLowerTypeofCompare(w, be, scope, pos, refusals, nextTemp)
		}
		if saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope) {
			// 串位仅 `+`（拼接）与 `==/!=`（内容相等）可走；其余算符大声拒。
			if be.OperatorToken != nil && (be.OperatorToken.Kind == ast.KindEqualsEqualsToken ||
				be.OperatorToken.Kind == ast.KindEqualsEqualsEqualsToken ||
				be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
				be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken) &&
				saIsStrValue(be.Left, scope) && saIsStrValue(be.Right, scope) {
				lh, msgL := saEvalStr(w, be.Left, scope, pos, refusals, nextTemp)
				if msgL != "" {
					return "", msgL
				}
				rh, msgR := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
				if msgR != "" {
					return "", msgR
				}
				neg := be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
					be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken
				return saStringContentEq(w, lh, rh, neg, scope, nextTemp), ""
			}
			return "", "only +/==/!= operate on strings"
		}
		if saCouldBeInst(be.Left, scope) || saCouldBeInst(be.Right, scope) {
			// 实例句柄禁入纯算术/位运算与大小比较（指针误作整数；`==/!=` 空比较与
			// `&&`/`||` 沿既有门（上游同形），余算符一律拒；铁律 4）。
			if be.OperatorToken != nil {
				switch be.OperatorToken.Kind {
				case ast.KindPlusToken, ast.KindMinusToken,
					ast.KindAsteriskToken, ast.KindSlashToken,
					ast.KindPercentToken,
					ast.KindLessThanLessThanToken,
					ast.KindGreaterThanGreaterThanToken,
					ast.KindGreaterThanGreaterThanGreaterThanToken,
					ast.KindAmpersandToken, ast.KindBarToken,
					ast.KindCaretToken,
					ast.KindLessThanToken, ast.KindLessThanEqualsToken,
					ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken:
					return "", "instance value in i32 expression"
				}
			}
		}
		op, ok := map[ast.Kind]string{
			ast.KindPlusToken: "add", ast.KindMinusToken: "sub",
			ast.KindAsteriskToken: "mul", ast.KindSlashToken: "div",
			ast.KindPercentToken:                           "srem",
			ast.KindLessThanLessThanToken:                  "shl",
			ast.KindGreaterThanGreaterThanToken:            "ashr",
			ast.KindGreaterThanGreaterThanGreaterThanToken: "lshr",
			ast.KindAmpersandToken:                         "and", ast.KindBarToken: "or",
			ast.KindCaretToken:        "xor",
			ast.KindEqualsEqualsToken: "eq", ast.KindEqualsEqualsEqualsToken: "eq",
			ast.KindExclamationEqualsToken: "ne", ast.KindExclamationEqualsEqualsToken: "ne",
			ast.KindLessThanToken: "slt", ast.KindLessThanEqualsToken: "sle",
			ast.KindGreaterThanToken: "sgt", ast.KindGreaterThanEqualsToken: "sge",
			ast.KindAmpersandAmpersandToken: "and", ast.KindBarBarToken: "or",
		}[saBinaryOpKind(be)]
		if !ok {
			if saBinaryOpKind(be) == ast.KindAsteriskAsteriskToken {
				return saLowerPow(w, be, scope, pos, refusals, nextTemp)
			}
			return "", fmt.Sprintf("binary operator %s not in subset", saBinaryOpKind(be).String())
		}
		if be.OperatorToken != nil && (be.OperatorToken.Kind == ast.KindEqualsEqualsToken ||
			be.OperatorToken.Kind == ast.KindEqualsEqualsEqualsToken ||
			be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
			be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken) {
			// handle equality compares directly (arr bindings from enum instantiations
			// against i32 values; address semantics; string/instance sides keep old gates).
			if ln, ok := saArrIdentOperand(be.Left, scope); ok {
				neg := be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
					be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken
				op2 := "eq"
				if neg {
					op2 = "ne"
				}
				if rn, ok := saArrIdentOperand(be.Right, scope); ok {
					t := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op2, ln, rn))
					return t, ""
				}
				r, msgR := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
				if msgR != "" {
					return "", msgR
				}
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op2, ln, r))
				return t, ""
			}
			if rn, ok := saArrIdentOperand(be.Right, scope); ok {
				l, msgL := saEvalI32(w, be.Left, scope, pos, refusals, nextTemp)
				if msgL != "" {
					return "", msgL
				}
				neg := be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
					be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken
				op2 := "eq"
				if neg {
					op2 = "ne"
				}
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op2, l, rn))
				return t, ""
			}
		}
		// f64 arithmetic/comparison (either side float forces float; mixed shapes refuse).
		if saIsF64Operand(be.Left, scope) || saIsF64Operand(be.Right, scope) {
			t, msg := saEvalF64Strict(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			return t, ""
		}
		l, msgL := saEvalI32(w, be.Left, scope, pos, refusals, nextTemp)
		if msgL != "" {
			return "", msgL
		}
		r, msgR := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msgR != "" {
			return "", msgR
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, l, r))
		return t, ""
	case ast.KindCallExpression:
		if saCallIsStr(e.AsCallExpression(), scope) {
			if !saStrCallIsI32(e.AsCallExpression(), scope) {
				return "", "string value in i32 expression"
			}
		}
		// Number(整数) i32 位直通（值与 node 一致；串形走 f64 轮子，见调用核；
		// 空参与 Number(0) 同值 0，见 step374）。
		if ce := e.AsCallExpression(); ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier &&
			ce.Expression.Text() == "Number" && ce.Arguments != nil {
			if len(ce.Arguments.Nodes) == 0 {
				return "0", ""
			}
			if len(ce.Arguments.Nodes) == 1 && saIsIntWord(ce.Arguments.Nodes[0], scope) {
				return saEvalI32(w, ce.Arguments.Nodes[0], scope, pos, refusals, nextTemp)
			}
		}
		// 数组函数返回句柄禁入 i32 位（与数组方法同门）。
		if k, ok := saCallRetKind(e.AsCallExpression(), scope); ok && k == "arr" {
			return "", "array value in i32 expression"
		}
		// Hash.update 无值返回（语句位专用；值位大声拒，禁句柄误作 i32）。
		if saHashUpdateCall(e.AsCallExpression(), scope) {
			return "", "Hash.update does not return a value (use it as a statement)"
		}
		if k, ok := saArrCallRet(e.AsCallExpression(), scope); ok && k != "i32" {
			return "", "array value in i32 expression"
		}
		if k, ok := saDateCallKind(e.AsCallExpression(), scope); ok && k != "i32" {
			// 未知成员（种 ""）落调用核取精确定位；millis/串位在此拒。
			if k == "str" || saIsDateStrCall(e.AsCallExpression(), scope) {
				return "", "string value in i32 expression"
			}
			// date millis（i64）i32 位收窄：调用求值直传（低 32 位截断，
			// 与 i32 算术回绕同类子集语义；上游逐字节同形，零发明；JEV narrow）。
			if k == "date" {
				op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				if voidCall {
					return "", "void function call in value position"
				}
				return op, ""
			}
			if k != "" {
				return "", "date millis needs i64 (beyond i32 subset)"
			}
		}
		if saIsArrayCtor(e) {
			return "", "array value in i32 expression"
		}
		op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if voidCall {
			return "", "void function call in value position"
		}
		return op, ""
	case ast.KindRegularExpressionLiteral:
		return "", "regex literal needs a regex binding (const re = /.../)"
	case ast.KindDeleteExpression:
		return "", "delete operator is not lowerable (static layouts cannot drop fields; Maps/Sets use .delete())"
	case ast.KindAwaitExpression:
		// await 值透传（悬挂在内层调用门大声拒；形状证据：封存 lowerExpr:2863-2871）。
		return saEvalI32(w, e.AsAwaitExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindObjectLiteralExpression:
		return "", "object literal needs a declaration binding (const p: Iface = {...})"
	default:
		return "", fmt.Sprintf("expression %s is not in the SA-lowerable subset", e.Kind.String())
	}
}

// saLowerInFold `in` 静态折叠（布局固定，字段有无编译期 1/0；
// 形状证据：封存 lowerBinary:3135-3177。品牌检查/动态键一律拒）。
func saLowerInFold(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	verdict := ""
	// 私有品牌检查（`#x in o` 按属主静态折叠；封存 lowerExpr 私有 `in` 相）。
	if be.Left != nil && be.Left.Kind == ast.KindPrivateIdentifier {
		if be.Right != nil && be.Right.Kind == ast.KindIdentifier {
			if k, ok := scope.types[be.Right.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
				if def, ok := scope.classes[k[5:]]; ok {
					key, msg := saPrivResolve(def, be.Left.Text(), scope.thisClass)
					if msg != "" {
						return "", msg
					}
					if _, ok := def.offsets[key]; ok {
						verdict = "1"
					} else {
						verdict = "0"
					}
				}
			}
		}
		if verdict == "" {
			return "", "in operator needs a literal key and a known-layout object"
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s\n", t, verdict))
		return t, ""
	}
	if be.Left != nil && be.Left.Kind == ast.KindStringLiteral {
		if be.Right != nil && be.Right.Kind == ast.KindIdentifier {
			if verdict, ok := saLayoutHasKey(be.Left.Text(), be.Right.Text(), scope); ok {
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s\n", t, verdict))
				return t, ""
			}
		}
	}
	if verdict == "" {
		return "", "in operator needs a literal key and a known-layout object"
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", t, verdict))
	return t, ""
}

// saLayoutHasKey 查已知布局有无键（存取器无槽但有名亦算存在；
// `in` 与 `Object.hasOwn` 共用，禁另立口径）。
func saLayoutHasKey(key, objName string, scope *saScope) (string, bool) {
	k, ok := scope.types[objName]
	if !ok || len(k) <= 5 || k[:5] != "inst:" {
		return "", false
	}
	def, ok := scope.classes[k[5:]]
	if !ok {
		return "", false
	}
	// 存取器无槽但有名（`in` 判存在；形状证据同上）。
	if _, ok := def.offsets[key]; ok {
		return "1", true
	} else if _, ok := def.getters[key]; ok {
		return "1", true
	} else if _, ok := def.setters[key]; ok {
		return "1", true
	}
	return "0", true
}

// saLowerNullish `??` 空合槽（左非零直通，否则右惰性求值；子集 null 即 0；
// i32 位；形状证据：封存 lowerBinary:3182-3206）。
func saLowerNullish(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	l, msg := saEvalI32(w, be.Left, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	tL := fmt.Sprintf("L_null_t_%d", *scope.nextLabel)
	*scope.nextLabel++
	fL := fmt.Sprintf("L_null_f_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_null_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", c, l))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, tL, fL))
	w.Write(fmt.Sprintf("%s:\n", tL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, l))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", fL))
	r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, r))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return out, ""
}

// saLowerNullishInstNull 实例空合空回退（`return M[k] ?? null`；左须同布局实例源
// （具化表下标读/同种标识符），右须空字面量（null/undefined，子集皆 0 句柄）；
// 槽形镜像封存 lowerBinary:3182-3206 + 本仓 saLowerNullish，值宽按 ptr；
// 非空右臂/异种左源沿旧门大声拒）。
func saLowerNullishInstNull(w printer.EmitTextWriter, be *ast.BinaryExpression, retKind string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	isNone := false
	if be.Right != nil {
		switch be.Right.Kind {
		case ast.KindNullKeyword, ast.KindUndefinedKeyword:
			isNone = true
		case ast.KindLiteralType:
			if lit := be.Right.AsLiteralTypeNode().Literal; lit != nil && lit.Kind == ast.KindNullKeyword {
				isNone = true
			}
		}
	}
	if !isNone {
		return "", "only null fallback lowerable for struct nullish return"
	}
	var l string
	switch {
	case be.Left != nil && be.Left.Kind == ast.KindElementAccessExpression:
		ea := be.Left.AsElementAccessExpression()
		if ea.Expression == nil || ea.Expression.Kind != ast.KindIdentifier {
			return "", "index base must be bound array"
		}
		if ea.QuestionDotToken != nil {
			return "", "optional map index reads are not lowerable"
		}
		t, msg := saLowerMapIndexLoad(w, ea.Expression.Text(), ea.ArgumentExpression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if k, ok := scope.types[t]; !ok || k != retKind {
			return "", "struct return needs matching struct value"
		}
		l = t
	case be.Left != nil && be.Left.Kind == ast.KindIdentifier:
		if k, ok := scope.types[be.Left.Text()]; !ok || k != retKind {
			return "", "struct return needs matching struct value"
		}
		l = be.Left.Text()
	default:
		return "", "struct nullish return needs a struct value or index read"
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	tL := fmt.Sprintf("L_null_t_%d", *scope.nextLabel)
	*scope.nextLabel++
	fL := fmt.Sprintf("L_null_f_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_null_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", c, l))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, tL, fL))
	w.Write(fmt.Sprintf("%s:\n", tL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, l))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", fL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	saOwnTemp(scope, out)
	return out, ""
}

// saTypeofKind 静态 typeof 串：字面按语法表；标识符按作用域种
// （math 别名/同文件函数为 function）；未知大声拒（无 checker，
// env-probe 不做——无 binder 权威，折叠即发明事实）。
// 形状证据：封存 lowerTypeof:9171-9239（字面表 + 种映射）+
// lowerTypeofConstFold:236-283（null/undefined 方言映 "undefined"）。
func saTypeofKind(e *ast.Node, scope *saScope) (string, string) {
	if e == nil || e.Kind != ast.KindTypeOfExpression {
		return "", "not a typeof expression"
	}
	op := e.AsTypeOfExpression().Expression
	if op == nil {
		return "", "missing typeof operand"
	}
	if op.Kind == ast.KindIdentifier {
		name := op.Text()
		if k, ok := scope.types[name]; ok {
			// checker 权威：`any`/`unknown` 擦除为 i32 后 typeof 不可折叠
			//（封存 typeofKind:58-59；上游同位拒收；无 tcx 回退既有种逻辑）。
			if saIsAnyOrUnknown(scope.tcx, op) {
				return "", "typeof " + name + " is not statically known"
			}
			switch {
			case k == "i32":
				return "number", ""
			case k == "bool":
				return "boolean", ""
			case k == "str":
				return "string", ""
			case k == "arr" || k == "map" || k == "set" || k == "date":
				return "object", ""
			case len(k) > 5 && k[:5] == "inst:":
				return "object", ""
			default:
				return "", "typeof " + name + " is not statically known"
			}
		}
		if _, ok := scope.mathAlias[name]; ok {
			return "function", ""
		}
		if _, ok := scope.funcs[name]; ok {
			return "function", ""
		}
		return "", "typeof unknown global " + name + " is not lowerable"
	}
	switch op.Kind {
	case ast.KindNumericLiteral:
		return "number", ""
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return "string", ""
	case ast.KindTrueKeyword, ast.KindFalseKeyword:
		return "boolean", ""
	case ast.KindNullKeyword, ast.KindUndefinedKeyword:
		return "undefined", ""
	case ast.KindArrowFunction, ast.KindFunctionExpression:
		return "function", ""
	case ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression:
		return "object", ""
	default:
		return "", "typeof on computed values is not lowerable (bind it first)"
	}
}

// saLowerTypeofCompare `typeof X ==/===/!=/!== "kind"` 任一操作数序
// （形状证据：封存 splitTypeofCompare:120-151 + lowerTypeofGuard:156-178 +
// lowerTypeofConstFold:236-283）：
//   - "undefined" 对 + 标识符 → 空检查（eq/ne v, 0；子集 null 即 0）。
//   - 其余对静态种折叠为 `eq/ne 1, 1` 常量临时量（br 只吃寄存器）。
func saLowerTypeofCompare(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	neg := false
	switch saBinaryOpKind(be) {
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
		neg = false
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
		neg = true
	default:
		return "", "typeof pairs only compare with ==/!="
	}
	var typeOp, litNode *ast.Node
	for _, side := range []*ast.Node{be.Left, be.Right} {
		if side != nil && side.Kind == ast.KindTypeOfExpression {
			typeOp = side
		}
	}
	for _, side := range []*ast.Node{be.Left, be.Right} {
		if side != nil && side.Kind == ast.KindStringLiteral {
			litNode = side
		}
	}
	if typeOp == nil || litNode == nil {
		return "", "typeof pairs need typeof X against a string literal"
	}
	lit := litNode.Text()
	inner := typeOp.AsTypeOfExpression().Expression
	if lit == "undefined" && inner != nil && inner.Kind == ast.KindIdentifier {
		nm := inner.Text()
		if _, ok := scope.types[nm]; !ok {
			return "", "typeof unknown global " + nm + " is not lowerable"
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		if neg {
			w.Write(fmt.Sprintf("  %s = ne %s, 0\n", t, nm))
		} else {
			w.Write(fmt.Sprintf("  %s = eq %s, 0\n", t, nm))
		}
		return t, ""
	}
	kind, msg := saTypeofKind(typeOp, scope)
	if msg != "" {
		return "", msg
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if (kind == lit) != neg {
		w.Write(fmt.Sprintf("  %s = eq 1, 1\n", t))
	} else {
		w.Write(fmt.Sprintf("  %s = ne 1, 1\n", t))
	}
	return t, ""
}

// saLowerVarDecl lowering 变量声明（`let/const x: number|i32 = <i32>`）。
// 形状证据：封存 lowerVarDeclList:1395-1470（using 拒、无 init const 拒、
// 解构拒、缺 init 绑零值、名按 bindingNameText 取标识符）。
