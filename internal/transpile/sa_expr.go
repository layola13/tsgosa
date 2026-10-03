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
		// 实例返回：具名同种句柄直传（`return p`；封存 lowerReturn 值位同形；
		// 种错配沿既有 i32 门拒，fresh temp 非调用源沿旧门拒）。
		if strings.HasPrefix(retKind, "inst:") {
			if k, ok := scope.types[e.Text()]; ok && k == retKind {
				return e.Text(), ""
			}
			return "", "struct return needs matching struct value"
		}
	}
	if retKind == "boolean" {
		return saEvalBool(w, e, scope, pos, refusals, nextTemp)
	}
	if retKind == "string" {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	// f64 bindings pass through (i32-annotated functions returning float bits; cf loose returns).
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "f64" {
			return e.Text(), ""
		}
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

func saLowerTernaryValue(w printer.EmitTextWriter, ce *ast.ConditionalExpression, where *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, bool, string) {
	condOp, msg := saCondOperand(w, ce.Condition, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
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
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "ternary arms must be string operands"})
			return "", false, "ternary arms must be string operands"
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
			return saLowerTernaryF64Join(w, condOp, ak, at, bk, bt, scope, nextLabel, nextTemp), false, ""
		}
	}
	a, msgA := saEvalI32(w, ce.WhenTrue, scope, pos, refusals, nextTemp)
	b, msgB := saEvalI32(w, ce.WhenFalse, scope, pos, refusals, nextTemp)
	if msgA != "" || msgB != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "ternary arms must be i32 operands"})
		return "", false, "ternary arms must be i32 operands"
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
			return nil, fmt.Sprintf("omitted default argument %d of %s has no recorded default (short calls need a literal default)", i+1, fname)
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
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return "", false
	}
	name := ce.Expression.Text()
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
	return "", false
}

func saEvalNamedCall(w printer.EmitTextWriter, name string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	callName := name
	if name == "main" && scope.mainRenamed {
		// 入口合成抢 `@main`（定义改名处同步；签名表仍以原名建）。
		callName = "main__user"
	}
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
	if !ok {
		// 异步定时器裸全局专用拒因（先于 unknown；事件循环回调分发
		// Phase 2，无同步 JS 形；形状证据：封存 node_timers.go:16-33）。
		// 方法形（`x.setTimeout`）不触此门，走各自表面。
		if saIsTimerName(name) {
			return "", false, name + " needs an event loop with callback dispatch (async timers are Phase 2)"
		}
		return "", false, "unknown function " + name
	}
	return saEvalFuncCall(w, name, callName, sig, ce, scope, pos, refusals, nextTemp)
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
		if len(sig.paramKinds) == total && len(sig.paramKinds[i]) > 5 && sig.paramKinds[i][:5] == "inst:" {
			if a != nil && a.Kind == ast.KindIdentifier {
				if k, ok := scope.types[a.Text()]; ok && k == sig.paramKinds[i] {
					return a.Text(), ""
				}
			}
			return "", "instance argument needs matching class"
		}
		if a != nil && a.Kind == ast.KindIdentifier {
			if k, ok := scope.types[a.Text()]; ok && (k == "arr" || k == "str") {
				return a.Text(), ""
			}
		}
		return saEvalBool(w, a, scope, pos, refusals, nextTemp)
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
	// captureSig 实参拼接 :1336-1343 同序）。
	args = append(args, sig.arrowCaps...)
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
	w.Write(fmt.Sprintf("  %s = %s\n", old, target))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
	saStoreLocal(w, target, t, scope, nextTemp)
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
	case ast.KindIdentifier:
		if k, ok := scope.types[e.Text()]; ok && k == "f64" {
			return e.Text(), ""
		}
		return "", e.Text() + " is not a float"
	case ast.KindParenthesizedExpression:
		return saEvalF64Strict(w, e.AsParenthesizedExpression().Expression, scope, pos, refusals, nextTemp)
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
				return "", "assignment to unknown/non-i32 variable"
			}
			op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			w.Write(fmt.Sprintf("  %s = %s\n", target, op))
			return target, ""
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindInKeyword {
			// `in` 静态折叠须先于一切求值门（左为串字面量键）。
			return saLowerInFold(w, be, scope, pos, refusals, nextTemp)
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindQuestionQuestionToken {
			// `??` 空合槽须先于串门（i32 位，右惰性）。
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
	case ast.KindDeleteExpression:
		return "", "delete operator is not lowerable (static layouts cannot drop fields; Maps/Sets use .delete())"
	case ast.KindAwaitExpression:
		// await 值透传（悬挂在内层调用门大声拒；形状证据：封存 lowerExpr:2863-2871）。
		return saEvalI32(w, e.AsAwaitExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindObjectLiteralExpression:
		return "", "object literal needs a declaration binding (const p: Iface = {...})"
	default:
		return "", fmt.Sprintf("unsupported expression kind %d", int(e.Kind))
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
			if k, ok := scope.types[be.Right.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
				if def, ok := scope.classes[k[5:]]; ok {
					// 存取器无槽但有名（`in` 判存在；形状证据同上）。
					if _, ok := def.offsets[be.Left.Text()]; ok {
						verdict = "1"
					} else if _, ok := def.getters[be.Left.Text()]; ok {
						verdict = "1"
					} else if _, ok := def.setters[be.Left.Text()]; ok {
						verdict = "1"
					} else {
						verdict = "0"
					}
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
