// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
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
			if k == "map" || k == "set" || k == "date" {
				return "", k + " " + nm + " in condition"
			}
			if k != "i32" && k != "bool" {
				return "", k + " " + nm + " in condition"
			}
			return nm, ""
		}
		return "", "unknown condition variable " + nm
	case ast.KindTrueKeyword:
		return "1", ""
	case ast.KindFalseKeyword:
		return "0", ""
	default:
		return saEvalI32(w, cond, scope, pos, refusals, nextTemp)
	}
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
			return "", "array return not supported"
		}
		if k, ok := scope.types[e.Text()]; ok && k == "str" && retKind != "string" {
			return "", "string return needs string annotation"
		}
	}
	if retKind == "boolean" {
		return saEvalBool(w, e, scope, pos, refusals, nextTemp)
	}
	if retKind == "string" {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	return saEvalI32(w, e, scope, pos, refusals, nextTemp)
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

func saEvalCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	if m, ok := saMathMethodName(ce.Expression); ok {
		return saEvalMathMethod(w, m, ce, scope, pos, refusals, nextTemp)
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
		if pa.Name() != nil && saIsStrMethod(pa.Name().Text()) && saIsStrExpr(pa.Expression, scope) {
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
			op, msg := saInlineMethod(w, h, def, pa.Name().Text(), ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			return op, false, ""
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
			if _, ok := scope.classes[pa.Expression.Text()]; ok {
				return "", false, "static class members are not lowerable"
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
		// Number.isInteger(x)：i32 操作数恒整（求值保留副作用后折 "1"；
		// 形状证据：封存 lowerCall:3837-3848）。
		if saIsNumberIsInteger(ce) {
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			if len(argNodes) != 1 {
				return "", false, "Number.isInteger takes one argument"
			}
			if _, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp); msg != "" {
				return "", false, msg
			}
			return "1", false, ""
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Number" {
			// parseFloat 回 f64（薄口无 f64 种）；其余 Number.* 未知。
			if pa.Name() != nil && pa.Name().Text() == "parseFloat" {
				return "", false, "Number.parseFloat needs f64 (beyond i32 subset)"
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
		return "", false, "only direct function calls lowerable"
	}
	if ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		if ce.Expression != nil && ce.Expression.Kind == ast.KindSuperKeyword {
			return "", false, "super() is only lowerable inside a subclass constructor"
		}
		return "", false, "only direct function calls lowerable"
	}
	name := ce.Expression.Text()
	if name == "Number" {
		// Number(x) 回 f64（薄口无 f64 种，大声拒）。
		return "", false, "Number(x) needs f64 (beyond i32 subset)"
	}
	if name == "Array" {
		// 数组构造式具化（调用式；`new Array` 另走声明位）。
		h, msg := saLowerArrayCtor(w, ce.AsNode(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		return h, false, ""
	}
	if _, shadowed := scope.types[name]; shadowed {
		return "", false, name + " is not a function"
	}
	if m, ok := scope.mathAlias[name]; ok {
		return saEvalMathMethod(w, m, ce, scope, pos, refusals, nextTemp)
	}
	sig, ok := scope.funcs[name]
	if !ok {
		return "", false, "unknown function " + name
	}
	var args []string
	if ce.Arguments != nil {
		for i, a := range ce.Arguments.Nodes {
			// 形参种导向求值：str 形参走串求值（字面量/调用/拼接皆可），
			// arr/str 句柄标识符直传；其余走 bool 兼容求值。
			if len(sig.paramKinds) == len(ce.Arguments.Nodes) && sig.paramKinds[i] == "str" {
				h, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				args = append(args, h)
				continue
			}
			if len(sig.paramKinds) == len(ce.Arguments.Nodes) && sig.paramKinds[i] == "arr" {
				h, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				args = append(args, h)
				continue
			}
			// 实例句柄须形参同类相授（`inst:Pt` 对 `inst:Pt`）。
			if len(sig.paramKinds) == len(ce.Arguments.Nodes) && len(sig.paramKinds[i]) > 5 && sig.paramKinds[i][:5] == "inst:" {
				if a != nil && a.Kind == ast.KindIdentifier {
					if k, ok := scope.types[a.Text()]; ok && k == sig.paramKinds[i] {
						args = append(args, a.Text())
						continue
					}
				}
				return "", false, "instance argument needs matching class"
			}
			// 数组/字符串句柄直传（0/1 统一之外唯一的引用语义）；其余走 bool 兼容求值。
			if a != nil && a.Kind == ast.KindIdentifier {
				if k, ok := scope.types[a.Text()]; ok && (k == "arr" || k == "str") {
					args = append(args, a.Text())
					continue
				}
			}
			// 实参 bool 兼容求值。
			op, msg := saEvalBool(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			args = append(args, op)
		}
	}
	if len(args) != sig.params {
		return "", false, fmt.Sprintf("arity mismatch for %s: want %d, got %d", name, sig.params, len(args))
	}
	call := fmt.Sprintf("call @%s(%s)", name, strings.Join(args, ", "))
	if sig.isVoid {
		w.Write(fmt.Sprintf("  %s\n", call))
		return "", true, ""
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", t, call))
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
		w.Write(fmt.Sprintf("  %s = %s\n", target, t))
		return t, ""
	}
	old := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", old, target))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
	w.Write(fmt.Sprintf("  %s = %s\n", target, t))
	return old, ""
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
	nextLabel := scope.nextLabel
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 1\n", res))
	topL := fmt.Sprintf("L_pow_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_pow_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_pow_end_%d", *nextLabel)
	*nextLabel++
	ctr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", ctr, expo))
	w.Write(fmt.Sprintf("%s:\n", topL))
	cc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sgt %s, 0\n", cc, ctr))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cc, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	nr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", nr, res, base))
	w.Write(fmt.Sprintf("  %s = %s\n", res, nr))
	nc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", nc, ctr))
	w.Write(fmt.Sprintf("  %s = %s\n", ctr, nc))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return res, ""
}

// saEvalI32 求 i32 操作数并按需发射临时量（形状证据：封存 lowerBinary:3214-3324
// add/sub/mul/div/srem/shl/ashr/lshr/and/or/xor、eq/ne/slt/sle/sgt/sge；`**`
// 走 lowerPowLoop:3326-3350；一元见 lowerPrefixUnary:3595-3619）。
// 返回 (operand, errMsg)，errMsg 非空即失败（调用方按上下文包装定位拒绝）。
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
	case ast.KindIdentifier:
		nm := e.Text()
		if k, ok := scope.types[nm]; ok {
			if k == "arr" {
				return "", "array " + nm + " in i32 expression"
			}
			if k == "str" {
				return "", "string " + nm + " in i32 expression"
			}
			if k != "i32" && k != "bool" {
				return "", k + " " + nm + " in i32 expression"
			}
			if k != "i32" {
				return "", "boolean " + nm + " in i32 expression"
			}
			return nm, ""
		}
		return "", "unknown variable " + nm
	case ast.KindThisKeyword:
		if scope.thisSelf == "" {
			return "", "this outside a class method is not lowerable"
		}
		return scope.thisSelf, ""
	case ast.KindNewExpression:
		// 实例只可经声明绑定（`const o = new C()`）；值位大声拒。
		ne := e.AsNewExpression()
		if ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier {
			if ne.Expression.Text() == "Date" {
				return "", "new Date(x) is not lowerable (only arg-less now-shape binds)"
			}
			if ne.Expression.Text() == "RegExp" {
				return "", "regular expressions are not lowerable (no base lowering; regex.sai is unprojected stock)"
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
		// super.f 读基布局（形状证据：封存 checkSuperAccess:253-278）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindSuperKeyword && pa.Name() != nil {
			bdef, h, msg := saSuperBase(scope)
			if msg != "" {
				return "", msg
			}
			t, msg := saLowerClassFieldLoad(w, h, bdef, pa.Name().Text(), nextTemp)
			if msg != "" {
				return "", msg
			}
			return t, ""
		}
		// Number 整形常量折叠（`MAX_VALUE` 等；形状证据：封存 stdlib.go:128-130）。
		if v, ok := saNumberConst(pa); ok {
			return v, ""
		}
		// 实例字段读（`o.f`/`this.f`；静态成员大声拒）。
		if pa.Name() != nil && saCouldBeInst(pa.Expression, scope) {
			h, def, msg := saInstBase(pa.Expression, scope)
			if msg != "" {
				return "", msg
			}
			t, msg := saLowerClassFieldLoad(w, h, def, pa.Name().Text(), nextTemp)
			if msg != "" {
				return "", msg
			}
			return t, ""
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
			if _, ok := scope.classes[pa.Expression.Text()]; ok {
				return "", "static class members are not lowerable"
			}
		}
		// 整数枚举成员折叠（`E.A` → 字面量；未知成员大声拒）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
			if members, ok := scope.enums[pa.Expression.Text()]; ok {
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
				// super.f 写基布局（形状证据同读位）。
				if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
					lpa := be.Left.AsPropertyAccessExpression()
					if lpa.Expression != nil && lpa.Expression.Kind == ast.KindSuperKeyword && lpa.Name() != nil {
						bdef, h, msg := saSuperBase(scope)
						if msg != "" {
							return "", msg
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
				}
				// 实例字段写（`o.f = v`；返回右值，镜像赋值折值语义）。
				if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
					lpa := be.Left.AsPropertyAccessExpression()
					if lpa.Name() != nil && saCouldBeInst(lpa.Expression, scope) {
						h, def, msg := saInstBase(lpa.Expression, scope)
						if msg != "" {
							return "", msg
						}
						op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", msg
						}
						if msg := saLowerClassFieldStore(w, h, def, lpa.Name().Text(), op); msg != "" {
							return "", msg
						}
						return op, ""
					}
				}
				return "", "assignment to unknown/non-i32 variable"
			}
			op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			w.Write(fmt.Sprintf("  %s = %s\n", target, op))
			return target, ""
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
		if k, ok := saArrCallRet(e.AsCallExpression(), scope); ok && k != "i32" {
			return "", "array value in i32 expression"
		}
		if k, ok := saDateCallKind(e.AsCallExpression(), scope); ok && k != "i32" {
			// 未知成员（种 ""）落调用核取精确定位；millis/串位在此拒。
			if k == "str" || saIsDateStrCall(e.AsCallExpression(), scope) {
				return "", "string value in i32 expression"
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
		return "", "regular expressions are not lowerable (no base lowering; regex.sai is unprojected stock)"
	case ast.KindObjectLiteralExpression:
		return "", "object literal needs a declaration binding (const p: Iface = {...})"
	default:
		return "", fmt.Sprintf("unsupported expression kind %d", int(e.Kind))
	}
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
