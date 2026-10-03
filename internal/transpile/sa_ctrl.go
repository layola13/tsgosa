// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_ctrl.go — 控制流与语句（step3-11/14/15；循环 cont 语义见内注）。
func saBoundI32(scope *saScope, n *ast.Node) (string, bool) {
	if n == nil || n.Kind != ast.KindIdentifier {
		return "", false
	}
	nm := n.Text()
	if k, ok := scope.types[nm]; !ok || k != "i32" {
		return "", false
	}
	return nm, true
}

// saCompoundOp 映射复合赋值到 SA 算符（证据：封存 lowerCompoundAssign:3394-3417）。
func saCompoundOp(op ast.Kind) (string, bool) {
	mapped, ok := map[ast.Kind]string{
		ast.KindPlusEqualsToken: "add", ast.KindMinusEqualsToken: "sub",
		ast.KindAsteriskEqualsToken: "mul", ast.KindSlashEqualsToken: "div",
		ast.KindPercentEqualsToken: "srem",
	}[op]
	return mapped, ok
}

// saLowerElementAssign lowering `a[i] = v`（仅 plain `=`；下标/右值走 i32 求值）。
func saLowerElementAssign(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node) bool {
	ea := be.Left.AsElementAccessExpression()
	if ea.QuestionDotToken != nil {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "optional index store not lowerable"})
		return false
	}
	base, ok := saArrBase(scope, ea.Expression)
	if !ok {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "index store base must be bound array"})
		return false
	}
	idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported index: " + msg})
		return false
	}
	rhs, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported element rhs: " + msg})
		return false
	}
	saLowerElementStore(w, base, idx, rhs, nextTemp)
	return true
}

// saLowerCompound lowering x <op>= e（语句位与增量位共用；元素目标读改写回）。
func saLowerCompound(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node) bool {
	if be.Left != nil && be.Left.Kind == ast.KindElementAccessExpression {
		ea := be.Left.AsElementAccessExpression()
		if ea.QuestionDotToken != nil {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "optional index store not lowerable"})
			return false
		}
		base, ok := saArrBase(scope, ea.Expression)
		if !ok {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "index store base must be bound array"})
			return false
		}
		idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported index: " + msg})
			return false
		}
		// 读-改-写回：join 读回当前值，算符作用后存回同址。
		cur := saLowerCheckedIndex(w, base, idx, scope.nextLabel, nextTemp)
		r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
			return false
		}
		op, _ := saCompoundOp(saBinaryOpKind(be))
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, cur, r))
		saLowerElementStore(w, base, idx, t, nextTemp)
		return true
	}
	target, ok := saBoundI32(scope, be.Left)
	if !ok {
		// 串目标仅 `+=` 拼接（两侧串位；混合数值须显式 String()）。
		if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
			if k, bound := scope.types[be.Left.Text()]; bound && k == "str" &&
				saBinaryOpKind(be) == ast.KindPlusEqualsToken {
				h, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
				if msg != "" {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported string += rhs: " + msg})
					return false
				}
				out := saConcatSlices(w, be.Left.Text(), h, scope, nextTemp)
				w.Write(fmt.Sprintf("  %s = %s\n", be.Left.Text(), out))
				return true
			}
		}
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "compound assignment to unknown/non-i32 variable"})
		return false
	}
	r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
		return false
	}
	op, _ := saCompoundOp(saBinaryOpKind(be))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, target, r))
	w.Write(fmt.Sprintf("  %s = %s\n", target, t))
	return true
}

// saIsLogicAssignOp 报告短路赋值（`&&=`/`||=`/`??=` 走 join 槽，真短路；
// 与 eager 的 `and`/`or` 值运算不同；形状证据：封存 isLogicAssign:3569-3573）。
func saIsLogicAssignOp(op ast.Kind) bool {
	return op == ast.KindAmpersandAmpersandEqualsToken ||
		op == ast.KindBarBarEqualsToken ||
		op == ast.KindQuestionQuestionEqualsToken
}

// saSnapImm 快照立即数为寄存器（join 槽存须见寄存器；裸名/临时量不定寄存器
// 与立即数不可存；形状证据：封存 snapImm:3575+，调用见 3558）。
func saSnapImm(w printer.EmitTextWriter, op string, nextTemp *int) string {
	if op == "" {
		return op
	}
	imm := true
	for i := 0; i < len(op); i++ {
		c := op[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if i == 0 && c == '-' && len(op) > 1 {
			continue
		}
		imm = false
		break
	}
	if !imm {
		return op
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", t, op))
	return t
}

// saLowerLogicAssign lowering `a &&= b`/`a ||= b`/`a ??= b`（真短路：
// 目标读一次，RHS 只在赋值臂求值，两臂经槽汇合；形状证据：封存
// lowerLogicAssign:3439-3565，`??` 槽形见 lowerBinary:3182-3206）。
// 目标镜像 `=`：裸标识符（i32/bool/str；串以 length 判空、指针判 ??）；
// 成员/元素/未知目标一律大声拒（模块槽另域）。
func saLowerLogicAssign(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	op := saBinaryOpKind(be)
	if be.Left == nil || be.Left.Kind != ast.KindIdentifier {
		return "", "logical assignment target is not lowerable"
	}
	name := be.Left.Text()
	kind, ok := scope.types[name]
	if !ok || (kind != "i32" && kind != "bool" && kind != "str") {
		return "", "logical assignment target is not lowerable"
	}
	// 真值测试：`&&=` 为真赋值，`||=`/`??=` 为假/空赋值，恒进赋值臂优先。
	// 串以 length 判空（空串 falsy，头指针恒真；封存 :3521-3528），
	// `??=` 保指针判空（封存 :3544 注）。
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	test := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	testVal := name
	if kind == "str" && op != ast.KindQuestionQuestionEqualsToken {
		ln := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, name))
		testVal = ln
	}
	if op == ast.KindAmpersandAmpersandEqualsToken {
		w.Write(fmt.Sprintf("  %s = ne %s, 0\n", test, testVal))
	} else {
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", test, testVal))
	}
	assignL := fmt.Sprintf("L_logas_assign_%d", *scope.nextLabel)
	*scope.nextLabel++
	skipL := fmt.Sprintf("L_logas_skip_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_logas_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", test, assignL, skipL))
	w.Write(fmt.Sprintf("%s:\n", assignL))
	var rhs string
	var msg string
	if kind == "str" {
		rhs, msg = saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
	} else if kind == "bool" {
		rhs, msg = saEvalBool(w, be.Right, scope, pos, refusals, nextTemp)
	} else {
		rhs, msg = saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	}
	if msg != "" {
		return "", msg
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, rhs))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, saSnapImm(w, rhs, nextTemp)))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", skipL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, name))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return out, ""
}

// saLowerExprStmt lowering 表达式语句：调用（值/void 皆可，结果丢弃）与赋值
// （`x = <i32>`，x 须已绑定；复合/短路赋分流）。其余一律大声拒。
func saLowerExprStmt(w printer.EmitTextWriter, s *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	e := s.AsExpressionStatement().Expression
	if e == nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement (calls and assignments only)"})
		return false
	}
	if e.Kind == ast.KindCallExpression {
		if _, _, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp); msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported call statement: " + msg})
			return false
		}
		return true
	}
	if e.Kind != ast.KindBinaryExpression {
		// 其余表达式语句求值后丢弃（证据：封存 lowerExprStatement:2712-2715
		// 只 lower 表达式：`i++` 等副作用保留，无副作用的纯表达式亦然）。
		if _, msg := saEvalI32(w, e, scope, pos, refusals, nextTemp); msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement: " + msg})
			return false
		}
		return true
	}
	be := e.AsBinaryExpression()
	if saIsLogicAssignOp(saBinaryOpKind(be)) {
		// 短路赋值语句位（RHS 惰性单求值；结果丢弃；封存 lowerLogicAssign）。
		if _, msg := saLowerLogicAssign(w, be, scope, pos, refusals, nextTemp); msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return false
		}
		return true
	}
	if _, ok := saCompoundOp(saBinaryOpKind(be)); ok {
		return saLowerCompound(w, be, scope, pos, refusals, nextTemp, s)
	}
	if be.OperatorToken == nil || be.OperatorToken.Kind != ast.KindEqualsToken {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement (plain x = i32 only)"})
		return false
	}
	// 元素目标 `a[i] = v`（仅 plain `=`；复合走 saLowerCompound 的元素分支）。
	if be.Left != nil && be.Left.Kind == ast.KindElementAccessExpression {
		return saLowerElementAssign(w, be, scope, pos, refusals, nextTemp, s)
	}
	// 实例字段目标 `o.f = v`（经值位落存，结果丢弃）。
	if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
		if _, msg := saEvalI32(w, e, scope, pos, refusals, nextTemp); msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported field store: " + msg})
			return false
		}
		return true
	}
	if be.Left == nil || be.Left.Kind != ast.KindIdentifier {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement (plain x = i32 only)"})
		return false
	}
	name := be.Left.Text()
	k, ok := scope.types[name]
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to unknown variable " + name})
		return false
	}
	if k != "i32" && k != "bool" && k != "arr" && k != "str" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to non-i32 variable " + name})
		return false
	}
	if k == "arr" {
		// 数组句柄拷贝（绑定直传；数组返回调用亦直传）。
		if src, msg := saArrValueOf(w, be.Right, scope, pos, refusals, nextTemp); msg == "" {
			w.Write(fmt.Sprintf("  %s = %s\n", name, src))
			return true
		}
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array assignment needs array handle"})
		return false
	}
	if k == "str" {
		// 字符串句柄拷贝（同类相授）。
		h, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string assignment needs string value: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		return true
	}
	var op string
	var msg string
	if k == "bool" {
		op, msg = saEvalBool(w, be.Right, scope, pos, refusals, nextTemp)
	} else {
		op, msg = saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	}
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported assignment rhs: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, op))
	return true
}

// saLowerWhile lowering while（形状证据：封存 lowerWhile:1805-1835
// top/body/end + br + 体 + jmp top + end；false 恒假消死臂）。
func saLowerWhile(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	ws := s.AsWhileStatement()
	if ws.Expression != nil && ws.Expression.Kind == ast.KindFalseKeyword {
		return true
	}
	if be := saBoolSideCond(ws.Expression, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
		return false
	}
	bodyStmts, ok := saEmbeddedBlock(ws.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported while body"})
		return false
	}
	topL := fmt.Sprintf("L_while_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_while_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_while_end_%d", *nextLabel)
	*nextLabel++
	// 条件求值落在顶标号之后（每轮重算），先落顶再求条件。
	w.Write(fmt.Sprintf("%s:\n", topL))
	condOp, msg := saCondOperand(w, ws.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported while condition: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL})
	saBindPendingLabels(scope, false)
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if !saArmTerminates(bodyStmts) {
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saBodyHasContinue 报告循环体是否可能执行 continue（证据：封存 labels.go:100-127；
// 函数边界重置目标，其余过近似——多出的 cont 标号无害）。
func saBodyHasContinue(n *ast.Node) bool {
	found := false
	var walk func(x *ast.Node)
	walk = func(x *ast.Node) {
		if x == nil || found {
			return
		}
		if x.Kind == ast.KindContinueStatement {
			found = true
			return
		}
		switch x.Kind {
		case ast.KindFunctionDeclaration, ast.KindArrowFunction,
			ast.KindFunctionExpression, ast.KindClassDeclaration:
			return
		}
		x.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk(n)
	return found
}

// saLowerForInit lowering for 初始化位（变量声明表走声明路径；表达式须为赋值形）。
func saLowerForInit(w printer.EmitTextWriter, init *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if init == nil {
		return true
	}
	switch init.Kind {
	case ast.KindVariableDeclarationList:
		dl := init.AsVariableDeclarationList()
		return saLowerVarDeclList(w, init, dl, scope, pos, refusals, nextTemp)
	case ast.KindVariableStatement:
		return saLowerVarDecl(w, init, scope, pos, refusals, nextTemp)
	default:
		// 表达式初始化位：仅接受 x = <i32> 赋值形（与语句位同门）。
		if init.Kind != ast.KindBinaryExpression {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer"})
			return false
		}
		be := init.AsBinaryExpression()
		if be.OperatorToken == nil || be.OperatorToken.Kind != ast.KindEqualsToken ||
			be.Left == nil || be.Left.Kind != ast.KindIdentifier {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer"})
			return false
		}
		name := be.Left.Text()
		if _, ok := scope.types[name]; !ok {
			// 初始化位允许首次绑定（`for (i = 0;;)`），视同 let 隐式声明。
			scope.types[name] = "i32"
		} else if scope.types[name] != "i32" {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to non-i32 variable " + name})
			return false
		}
		op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		return true
	}
}

// saLowerIncr lowering for 增量位：`x++`/`++x`/`x += K` 等（证据：封存
// canonicalForStep:1855-1900 只认 ++ 系；复合赋值的 op 映射见 lowerCompoundAssign:3394-3417）。
// 其余一律大声拒（遗留 legacy 接受任意表达式，本子集收紧为门）。
func saLowerIncr(w printer.EmitTextWriter, incr *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	// 增量位只用写回副作用，不发旧值临时量（表达式位经 saLowerIncDec 保留旧值语义）。
	emitBump := func(target, op string) {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
		w.Write(fmt.Sprintf("  %s = %s\n", target, t))
	}
	switch incr.Kind {
	case ast.KindPostfixUnaryExpression:
		un := incr.AsPostfixUnaryExpression()
		if un.Operator != ast.KindPlusPlusToken && un.Operator != ast.KindMinusMinusToken {
			break
		}
		target, ok := saBoundI32(scope, un.Operand)
		if !ok {
			break
		}
		op := "add"
		if un.Operator == ast.KindMinusMinusToken {
			op = "sub"
		}
		emitBump(target, op)
		return true
	case ast.KindPrefixUnaryExpression:
		un := incr.AsPrefixUnaryExpression()
		if un.Operator != ast.KindPlusPlusToken && un.Operator != ast.KindMinusMinusToken {
			break
		}
		target, ok := saBoundI32(scope, un.Operand)
		if !ok {
			break
		}
		op := "add"
		if un.Operator == ast.KindMinusMinusToken {
			op = "sub"
		}
		emitBump(target, op)
		return true
	case ast.KindBinaryExpression:
		be := incr.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindEqualsToken {
			// `x = <i32>` 赋值形增量（与语句位同门）。
			target, okT := saBoundI32(scope, be.Left)
			if !okT {
				break
			}
			r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(incr.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", target, r))
			return true
		}
		op, ok := saCompoundOp(saBinaryOpKind(be))
		if !ok {
			break
		}
		target, okT := saBoundI32(scope, be.Left)
		if !okT {
			break
		}
		r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(incr.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
			return false
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, target, r))
		w.Write(fmt.Sprintf("  %s = %s\n", target, t))
		return true
	default:
		ln, col := pos(incr.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor (x++/--x/x=<i32>/x+=K only)"})
		return false
	}
	ln, col := pos(incr.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor (x++/--x/x=<i32>/x+=K only)"})
	return false
}

// saLowerFor lowering for（形状证据：封存 lowerFor:2043-2122 legacy 形；
// canonical 宏形 FOR_INIT/FOR_CHECK/FOR_NEXT 暂不采用，统一 legacy br 形）。
// saNonNegIntLiteral 报告无符号整数字面（纯十进制数字；浮点/十六进制/
// 负数走 legacy；形状证据：封存 nonNegIntLiteral:1833-1849）。
func saNonNegIntLiteral(n *ast.Node) (string, bool) {
	if n == nil || n.Kind != ast.KindNumericLiteral {
		return "", false
	}
	t := n.Text()
	if t == "" || saIsFloatLit(t) {
		return "", false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return "", false
		}
	}
	return t, true
}

// saCanonicalForStep 匹配 `i++`/`++i`/`i += K`/`i = i + K`（字面步进 K>=1，
// 报步进文本；形状证据：封存 canonicalForStep:1851-1900）。
func saCanonicalForStep(ctr string, incr *ast.Node) (string, bool) {
	if incr == nil {
		return "", false
	}
	isCtr := func(n *ast.Node) bool {
		return n != nil && n.Kind == ast.KindIdentifier && n.Text() == ctr
	}
	switch incr.Kind {
	case ast.KindPostfixUnaryExpression:
		un := incr.AsPostfixUnaryExpression()
		if un.Operator == ast.KindPlusPlusToken && isCtr(un.Operand) {
			return "1", true
		}
	case ast.KindPrefixUnaryExpression:
		un := incr.AsPrefixUnaryExpression()
		if un.Operator == ast.KindPlusPlusToken && isCtr(un.Operand) {
			return "1", true
		}
	case ast.KindBinaryExpression:
		bin := incr.AsBinaryExpression()
		if bin.OperatorToken == nil {
			return "", false
		}
		switch bin.OperatorToken.Kind {
		case ast.KindPlusEqualsToken:
			if isCtr(bin.Left) {
				if k, ok := saNonNegIntLiteral(bin.Right); ok && k != "0" {
					return k, true
				}
			}
		case ast.KindEqualsToken:
			if !isCtr(bin.Left) || bin.Right == nil || bin.Right.Kind != ast.KindBinaryExpression {
				return "", false
			}
			add := bin.Right.AsBinaryExpression()
			if add.OperatorToken == nil || add.OperatorToken.Kind != ast.KindPlusToken {
				return "", false
			}
			if isCtr(add.Left) {
				if k, ok := saNonNegIntLiteral(add.Right); ok && k != "0" {
					return k, true
				}
			} else if isCtr(add.Right) {
				if k, ok := saNonNegIntLiteral(add.Left); ok && k != "0" {
					return k, true
				}
			}
		}
	}
	return "", false
}

// saCanonicalForShape 匹配 `for (let i = L0; i < L1; step)`（非负整数字面
// 界 + 正字面步进；余下（<=、调用界、标识符界、浮点、零步进）走 legacy；
// 形状证据：封存 canonicalForShape:1902-1955。FOR_CHECK 作 ult 比较，
// legacy 作 slt；计数器恒非负字面起步+正步进时两形一致，标识符界留 legacy）。
func saCanonicalForShape(fs *ast.ForStatement) (ctr, lo, hi, step string, ok bool) {
	init := fs.Initializer
	if init != nil && init.Kind == ast.KindVariableStatement {
		init = init.AsVariableStatement().DeclarationList
	}
	if init == nil || init.Kind != ast.KindVariableDeclarationList {
		return "", "", "", "", false
	}
	dl := init.AsVariableDeclarationList()
	if len(dl.Declarations.Nodes) != 1 {
		return "", "", "", "", false
	}
	d := dl.Declarations.Nodes[0]
	vd := d.AsVariableDeclaration()
	if vd == nil {
		return "", "", "", "", false
	}
	nm := vd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", "", "", "", false
	}
	lo, good := saNonNegIntLiteral(vd.Initializer)
	if !good {
		return "", "", "", "", false
	}
	cond := fs.Condition
	if cond == nil || cond.Kind != ast.KindBinaryExpression {
		return "", "", "", "", false
	}
	bin := cond.AsBinaryExpression()
	if bin.OperatorToken == nil || bin.OperatorToken.Kind != ast.KindLessThanToken {
		return "", "", "", "", false
	}
	if bin.Left == nil || bin.Left.Kind != ast.KindIdentifier || bin.Left.Text() != nm.Text() {
		return "", "", "", "", false
	}
	hi, good = saNonNegIntLiteral(bin.Right)
	if !good {
		return "", "", "", "", false
	}
	step, good = saCanonicalForStep(nm.Text(), fs.Incrementor)
	if !good {
		return "", "", "", "", false
	}
	return nm.Text(), lo, hi, step, true
}

func saLowerFor(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	fs := s.AsForStatement()
	if !saLowerForInit(w, fs.Initializer, scope, pos, refusals, nextTemp) {
		return false
	}
	// Never-taken C 循环只发射 init（证据：封存 lowerFor:2061-2068）。
	if fs.Condition != nil && fs.Condition.Kind == ast.KindFalseKeyword {
		return true
	}
	if be := saBoolSideCond(fs.Condition, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
		return false
	}
	bodyNode := fs.Statement
	var bodyStmts []*ast.Node
	if bodyNode != nil {
		var ok bool
		bodyStmts, ok = saEmbeddedBlock(bodyNode)
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for body"})
			return false
		}
	}
	// 规范计数循环走上游 control.sal 宏（余下保 legacy 原形；
	// 形状证据：封存 tryLowerForMacro:1962-2037）。
	if ctr, lo, hi, step, ok := saCanonicalForShape(fs); ok {
		return saLowerForMacro(w, s, fs, bodyNode, bodyStmts, ctr, lo, hi, step, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	}
	topL := fmt.Sprintf("L_for_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_for_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_for_end_%d", *nextLabel)
	*nextLabel++
	needCont := fs.Incrementor != nil && bodyNode != nil && saBodyHasContinue(bodyNode)
	contL := topL
	if needCont {
		contL = fmt.Sprintf("L_for_cont_%d", *nextLabel)
		*nextLabel++
	}
	w.Write(fmt.Sprintf("%s:\n", topL))
	if fs.Condition != nil {
		condOp, msg := saCondOperand(w, fs.Condition, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for condition: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, bodyL, endL))
	} else {
		w.Write(fmt.Sprintf("  jmp %s\n", bodyL))
	}
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: contL, end: endL})
	saBindPendingLabels(scope, false)
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if needCont {
		w.Write(fmt.Sprintf("%s:\n", contL))
	}
	doIncr := fs.Incrementor != nil && (!saArmTerminates(bodyStmts) || needCont)
	if doIncr {
		if !saLowerIncr(w, fs.Incrementor, scope, pos, refusals, nextTemp) {
			return false
		}
	}
	if !saArmTerminates(bodyStmts) {
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saLowerForMacro lowering规范计数循环（`for (let i = L0; i < L1; step)` 经
// EXPAND FOR_INIT/FOR_CHECK/FOR_NEXT；域/标号/break-continue/终结纪律镜 legacy；
// continue 落增量前（执行序），无 continue 不留死标号；形状证据：封存
// tryLowerForMacro:1962-2037。doIncr 沿既有规则（可达 continue 恒在臂内，
// 故与封存 contJumps 规则在可接受程序上一致）。
func saLowerForMacro(w printer.EmitTextWriter, s *ast.Node, fs *ast.ForStatement, bodyNode *ast.Node, bodyStmts []*ast.Node, ctr, lo, hi, step string, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = s
	needImport("sa_std/control.sal")
	topL := fmt.Sprintf("L_for_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_for_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_for_end_%d", *nextLabel)
	*nextLabel++
	needCont := fs.Incrementor != nil && bodyNode != nil && saBodyHasContinue(bodyNode)
	contL := topL
	if needCont {
		contL = fmt.Sprintf("L_for_cont_%d", *nextLabel)
		*nextLabel++
	}
	w.Write(fmt.Sprintf("  EXPAND FOR_INIT %s, %s\n", ctr, lo))
	w.Write(fmt.Sprintf("%s:\n", topL))
	w.Write(fmt.Sprintf("  EXPAND FOR_CHECK %s, %s, %s, %s\n", ctr, hi, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: contL, end: endL})
	saBindPendingLabels(scope, false)
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if needCont {
		w.Write(fmt.Sprintf("%s:\n", contL))
	}
	// FOR_NEXT 自带回跳（legacy 增量需显式 jmp；体终结且无 continue 用则落空到 end）。
	if fs.Incrementor != nil && (!saArmTerminates(bodyStmts) || needCont) {
		w.Write(fmt.Sprintf("  EXPAND FOR_NEXT %s, %s, %s\n", ctr, step, topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

func saLowerDoWhile(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	ds := s.AsDoStatement()
	bodyStmts, ok := saEmbeddedBlock(ds.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported do body"})
		return false
	}
	if be := saBoolSideCond(ds.Expression, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
		return false
	}
	loopL := fmt.Sprintf("L_do_%d", *nextLabel)
	*nextLabel++
	condL := fmt.Sprintf("L_do_cond_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_do_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("%s:\n", loopL))
	scope.loops = append(scope.loops, saLoop{top: loopL, cont: condL, end: endL})
	saBindPendingLabels(scope, false)
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	bodyTerm := saArmTerminates(bodyStmts)
	if !bodyTerm {
		w.Write(fmt.Sprintf("  jmp %s\n", condL))
	}
	w.Write(fmt.Sprintf("%s:\n", condL))
	condOp, msg := saCondOperand(w, ds.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported do condition: " + msg})
		return false
	}
	switch condOp {
	case "1", "true":
		w.Write(fmt.Sprintf("  jmp %s\n", loopL))
	case "0", "false":
		// 落空直达 end。
	default:
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, loopL, endL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saCasePart 是 switch 一臂（case 子句节点；default 另记）。
type saCasePart struct {
	node *ast.Node
}

// saLowerSwitch lowering switch（形状证据：封存 lowerSwitch:2599-2675 legacy 链：
// 每 case 一 test 标号（eq 比较 -> body/下一 test）+ body 标号；
// 体终结则省尾 jmp；default 落空点；break 经栈到 end（无 continue 目标）。
// 2/3 臂走上游 SWITCH_2/3 宏（证据：封存 tryLowerSwitchMacro:2502-2588）。
func saLowerSwitch(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	sw := s.AsSwitchStatement()
	disc, msg := saEvalI32(w, sw.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported switch discriminant: " + msg})
		return false
	}
	clauses := sw.CaseBlock.AsCaseBlock().Clauses.Nodes
	var parts []saCasePart
	var defaultNode *ast.Node
	for _, cl := range clauses {
		switch cl.Kind {
		case ast.KindCaseClause:
			parts = append(parts, saCasePart{node: cl})
		case ast.KindDefaultClause:
			if defaultNode != nil {
				ln, col := pos(cl.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "multiple default clauses are not lowerable"})
				return false
			}
			defaultNode = cl
		default:
			ln, col := pos(cl.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("switch clause %s is not lowerable", cl.Kind.String())})
			return false
		}
	}
	// 2/3 臂走宏；余下（1/4+ 臂）保 legacy 链。
	if len(parts) == 2 || len(parts) == 3 {
		return saLowerSwitchMacro(w, s, disc, parts, defaultNode, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	}
	endL := fmt.Sprintf("L_endswitch_%d", *nextLabel)
	*nextLabel++
	scope.loops = append(scope.loops, saLoop{end: endL})
	saBindPendingLabels(scope, true)
	testLabels := make([]string, len(parts)+1)
	bodyLabels := make([]string, len(parts))
	for i := range parts {
		testLabels[i] = fmt.Sprintf("L_case_t_%d", *nextLabel)
		*nextLabel++
		bodyLabels[i] = fmt.Sprintf("L_case_b_%d", *nextLabel)
		*nextLabel++
	}
	testLabels[len(parts)] = fmt.Sprintf("L_case_default_%d", *nextLabel)
	*nextLabel++
	lowered := true
	for i, p := range parts {
		w.Write(fmt.Sprintf("%s:\n", testLabels[i]))
		val, vmsg := saEvalI32(w, p.node.AsCaseOrDefaultClause().Expression, scope, pos, refusals, nextTemp)
		if vmsg != "" {
			ln, col := pos(p.node.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported case value: " + vmsg})
			lowered = false
			break
		}
		cmp := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, %s\n", cmp, disc, val))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cmp, bodyLabels[i], testLabels[i+1]))
		w.Write(fmt.Sprintf("%s:\n", bodyLabels[i]))
		if !saLowerArm(w, p.node.AsCaseOrDefaultClause().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			lowered = false
			break
		}
		if !saArmTerminates(p.node.AsCaseOrDefaultClause().Statements.Nodes) {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		}
	}
	if !lowered {
		scope.loops = scope.loops[:len(scope.loops)-1]
		return false
	}
	w.Write(fmt.Sprintf("%s:\n", testLabels[len(parts)]))
	if defaultNode != nil {
		if !saLowerArm(w, defaultNode.AsCaseOrDefaultClause().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			scope.loops = scope.loops[:len(scope.loops)-1]
			return false
		}
		if !saArmTerminates(defaultNode.AsCaseOrDefaultClause().Statements.Nodes) {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		}
	} else {
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	scope.loops = scope.loops[:len(scope.loops)-1]
	return true
}

// saLowerSwitchMacro lowering 2/3 臂 switch（上游 SWITCH_2/3 分发宏；
// 体/break/default/域/终结纪律镜 legacy，唯 test 链（eq+br）入宏；
// 无 default 时宏 default 臂落空到 end；形状证据：封存 tryLowerSwitchMacro:2502-2588）。
func saLowerSwitchMacro(w printer.EmitTextWriter, s *ast.Node, disc string, parts []saCasePart, defaultNode *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = s
	// case 值前置求值（legacy 与体交错，运行时序由标号固定；两形各求值一次）。
	vals := make([]string, len(parts))
	for i, p := range parts {
		val, vmsg := saEvalI32(w, p.node.AsCaseOrDefaultClause().Expression, scope, pos, refusals, nextTemp)
		if vmsg != "" {
			ln, col := pos(p.node.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported case value: " + vmsg})
			return false
		}
		vals[i] = val
	}
	endL := fmt.Sprintf("L_endswitch_%d", *nextLabel)
	*nextLabel++
	bodyLabels := make([]string, len(parts))
	for i := range parts {
		bodyLabels[i] = fmt.Sprintf("L_case_b_%d", *nextLabel)
		*nextLabel++
	}
	defaultL := fmt.Sprintf("L_case_default_%d", *nextLabel)
	*nextLabel++
	scope.loops = append(scope.loops, saLoop{end: endL})
	saBindPendingLabels(scope, true)
	needImport("sa_std/control.sal")
	if len(parts) == 2 {
		w.Write(fmt.Sprintf("  EXPAND SWITCH_2 %s, %s, %s, %s, %s, %s\n", disc, vals[0], bodyLabels[0], vals[1], bodyLabels[1], defaultL))
	} else {
		w.Write(fmt.Sprintf("  EXPAND SWITCH_3 %s, %s, %s, %s, %s, %s, %s, %s\n", disc, vals[0], bodyLabels[0], vals[1], bodyLabels[1], vals[2], bodyLabels[2], defaultL))
	}
	lowerBody := func(stmts []*ast.Node) bool {
		if !saLowerArm(w, stmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		if !saArmTerminates(stmts) {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		}
		return true
	}
	lowered := true
	for i, p := range parts {
		w.Write(fmt.Sprintf("%s:\n", bodyLabels[i]))
		if !lowerBody(p.node.AsCaseOrDefaultClause().Statements.Nodes) {
			lowered = false
			break
		}
	}
	if !lowered {
		scope.loops = scope.loops[:len(scope.loops)-1]
		return false
	}
	w.Write(fmt.Sprintf("%s:\n", defaultL))
	if defaultNode != nil {
		if !lowerBody(defaultNode.AsCaseOrDefaultClause().Statements.Nodes) {
			scope.loops = scope.loops[:len(scope.loops)-1]
			return false
		}
	} else {
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	scope.loops = scope.loops[:len(scope.loops)-1]
	return true
}

// saTopThrowIndex 取 try 块顶层首个 throw 下标（无则 -1；嵌套 if/循环内
// 的 throw 不在此列，调用方落既有拒；形状证据：封存 tryLowerThrowingTry:2316-2322）。
func saTopThrowIndex(stmts []*ast.Node) int {
	for i, s := range stmts {
		if s != nil && s.Kind == ast.KindThrowStatement {
			return i
		}
	}
	return -1
}

// saHandlerUsesInto 收集子树值使用名（声明名位排除：变量/函数/类/参数/
// catch 形参标识符名、属性访问字段、标号；其余标识符皆记。误报偏向大声拒）。
func saHandlerUsesInto(n *ast.Node, uses map[string]bool) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindIdentifier:
		uses[n.Text()] = true
		return
	case ast.KindVariableDeclaration:
		vd := n.AsVariableDeclaration()
		if vd.Initializer != nil {
			saHandlerUsesInto(vd.Initializer, uses)
		}
		return
	case ast.KindFunctionDeclaration, ast.KindClassDeclaration,
		ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor,
		ast.KindConstructor:
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			// 名位跳过，余部（形参缺省/体）照走。
			n.ForEachChild(func(c *ast.Node) bool {
				if c != nil && c != nm.AsNode() {
					saHandlerUsesInto(c, uses)
				}
				return false
			})
			return
		}
	case ast.KindParameter:
		pd := n.AsParameterDeclaration()
		if pd.Initializer != nil {
			saHandlerUsesInto(pd.Initializer, uses)
		}
		if nm := pd.Name(); nm != nil && nm.Kind != ast.KindIdentifier {
			saHandlerUsesInto(nm.AsNode(), uses)
		}
		return
	case ast.KindPropertyAccessExpression:
		pa := n.AsPropertyAccessExpression()
		saHandlerUsesInto(pa.Expression, uses)
		return
	case ast.KindLabeledStatement:
		ls := n.AsLabeledStatement()
		saHandlerUsesInto(ls.Statement, uses)
		return
	case ast.KindBreakStatement:
		if lbl := n.AsBreakStatement().Label; lbl != nil {
			_ = lbl
			return
		}
	case ast.KindContinueStatement:
		if lbl := n.AsContinueStatement().Label; lbl != nil {
			_ = lbl
			return
		}
	}
	n.ForEachChild(func(c *ast.Node) bool {
		saHandlerUsesInto(c, uses)
		return false
	})
}

// saHandlerUses 收集臂内值使用名集。
func saHandlerUses(stmts []*ast.Node) map[string]bool {
	uses := map[string]bool{}
	for _, s := range stmts {
		saHandlerUsesInto(s, uses)
	}
	return uses
}

// saLowerThrowingTry lowering throwing-try 切片（`try { prefix; throw v; }` 直跑
// prefix 后把值绑 catch 形参跑 handler，再跑 finally；无 catch 则 finally 后
// panic(2501)。throw 后语句死，跳过。形状证据：封存 tryLowerThrowingTry:2301-2470）。
// 返回（终结，失败）。
func saLowerThrowingTry(w printer.EmitTextWriter, s *ast.Node, ts *ast.TryStatement, tryStmts []*ast.Node, throwIdx int, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	legacy := func() (bool, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "throw inside try is not lowerable (catch cannot resume after panic)"})
		return false, true
	}
	// Prefix 须直行（顶层终结/嵌套 throw 落既有拒）。
	for _, p := range tryStmts[:throwIdx] {
		if p == nil {
			continue
		}
		switch p.Kind {
		case ast.KindReturnStatement, ast.KindThrowStatement,
			ast.KindBreakStatement, ast.KindContinueStatement:
			return legacy()
		}
		if saContainsThrow(p) {
			return legacy()
		}
	}
	// try-local 不得泄入 handler（扁平域下读写本无碍，此门与封存对齐；
	// const 按值折叠恒可见，不拦）。
	blocked := map[string]bool{}
	for _, p := range tryStmts[:throwIdx] {
		if p == nil {
			continue
		}
		switch p.Kind {
		case ast.KindVariableStatement:
			dl := p.AsVariableStatement().DeclarationList.AsVariableDeclarationList()
			if dl.AsNode().Flags&ast.NodeFlagsConst != 0 {
				continue
			}
			for _, d := range dl.Declarations.Nodes {
				nm := d.AsVariableDeclaration().Name()
				if nm == nil || nm.Kind != ast.KindIdentifier {
					return legacy()
				}
				blocked[nm.Text()] = true
			}
		case ast.KindFunctionDeclaration, ast.KindClassDeclaration:
			if nm := p.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				blocked[nm.Text()] = true
			}
		}
	}
	if len(blocked) > 0 {
		check := func(stmts []*ast.Node) bool {
			for n := range saHandlerUses(stmts) {
				if blocked[n] {
					return true
				}
			}
			return false
		}
		leak := false
		if ts.CatchClause != nil && ts.CatchClause.AsCatchClause().Block != nil {
			leak = leak || check(ts.CatchClause.AsCatchClause().Block.AsBlock().Statements.Nodes)
		}
		if !leak && ts.FinallyBlock != nil {
			leak = leak || check(ts.FinallyBlock.AsBlock().Statements.Nodes)
		}
		if leak {
			return legacy()
		}
	}
	// 直跑 prefix（终结即 throw 死，落既有拒）。
	for _, p := range tryStmts[:throwIdx] {
		done, failed := saLowerStmt(w, p, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if failed {
			return false, true
		}
		if done {
			return legacy()
		}
	}
	throwSt := tryStmts[throwIdx]
	throwExpr := throwSt.AsThrowStatement().Expression
	if throwExpr != nil {
		if throwExpr.Kind == ast.KindStringLiteral ||
			throwExpr.Kind == ast.KindNoSubstitutionTemplateLiteral ||
			throwExpr.Kind == ast.KindTemplateExpression {
			ln, col := pos(throwSt.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "throw value type is not lowerable (catch params carry i32 only)"})
			return false, true
		}
		if throwExpr.Kind == ast.KindIdentifier {
			if k, ok := scope.types[throwExpr.Text()]; ok && k == "str" {
				ln, col := pos(throwSt.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "throw value type is not lowerable (catch params carry i32 only)"})
				return false, true
			}
		}
	}
	val, msg := saEvalI32(w, throwExpr, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(throwSt.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false, true
	}
	if ts.CatchClause == nil {
		if ts.FinallyBlock != nil {
			if !saLowerArm(w, ts.FinallyBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
				return false, true
			}
		}
		w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		return true, false
	}
	cc := ts.CatchClause.AsCatchClause()
	if cc.VariableDeclaration != nil {
		nm := cc.VariableDeclaration.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			ln, col := pos(cc.VariableDeclaration.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructured catch params are not in the SA-lowerable subset"})
			return false, true
		}
		if _, dup := scope.types[nm.Text()]; dup {
			ln, col := pos(cc.VariableDeclaration.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + nm.Text()})
			return false, true
		}
		w.Write(fmt.Sprintf("  %s = %s\n", nm.Text(), val))
		scope.types[nm.Text()] = "i32"
	}
	if cc.Block == nil {
		return legacy()
	}
	catchStmts := cc.Block.AsBlock().Statements.Nodes
	if !saLowerArm(w, catchStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
		return false, true
	}
	catchTerm := saArmTerminates(catchStmts)
	if ts.FinallyBlock != nil {
		finStmts := ts.FinallyBlock.AsBlock().Statements.Nodes
		if !saLowerArm(w, finStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return catchTerm || saArmTerminates(finStmts), false
	}
	return catchTerm, false
}

// saContainsThrow 报告子树是否含 throw（函数边界重置；证据：封存 lowerTry 前的
// containsThrow 门——SA-ASM 无异常边，throw 即 panic 不可恢复）。
func saContainsThrow(n *ast.Node) bool {
	found := false
	var walk func(x *ast.Node)
	walk = func(x *ast.Node) {
		if x == nil || found {
			return
		}
		if x.Kind == ast.KindThrowStatement {
			found = true
			return
		}
		switch x.Kind {
		case ast.KindFunctionDeclaration, ast.KindArrowFunction,
			ast.KindFunctionExpression, ast.KindClassDeclaration:
			return
		}
		x.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk(n)
	return found
}

// saTryTerms 报告 try/finally 的终结性（try 终结或 finally 终结即终结；
// 缺省块视为空；throwing 切片走 catch/finally 静态终结，与动态一致）。
func saTryTerms(s *ast.Node) (bool, bool) {
	ts := s.AsTryStatement()
	if ts.TryBlock != nil {
		if idx := saTopThrowIndex(ts.TryBlock.AsBlock().Statements.Nodes); idx >= 0 {
			catchTerm := false
			if ts.CatchClause != nil && ts.CatchClause.AsCatchClause().Block != nil {
				catchTerm = saArmTerminates(ts.CatchClause.AsCatchClause().Block.AsBlock().Statements.Nodes)
			}
			finTerm := false
			if ts.FinallyBlock != nil {
				finTerm = saArmTerminates(ts.FinallyBlock.AsBlock().Statements.Nodes)
			}
			if ts.CatchClause == nil {
				return true, finTerm
			}
			return catchTerm, finTerm
		}
	}
	tryTerm := false
	if ts.TryBlock != nil {
		tryTerm = saArmTerminates(ts.TryBlock.AsBlock().Statements.Nodes)
	}
	finTerm := false
	if ts.FinallyBlock != nil {
		finTerm = saArmTerminates(ts.FinallyBlock.AsBlock().Statements.Nodes)
	}
	return tryTerm, finTerm
}

// saLowerTry lowering try（形状证据：封存 lowerTry:2271-2300 +
// tryLowerThrowingTry:2301-2470）：无 throw 时 try 体直跑、catch 死代码跳过、
// finally 必跑；throwing 切片（prefix 直跑 + 顶层 throw 绑 catch 形参跑 handler
// 再跑 finally，无 catch 则 finally 后 panic(2501)）；其余含 throw 大声拒。
// 返回（终结，失败）：终结恒 false——终结后继语句作死码照发（合法 SA，不可达
// 而已；封存 lowerBlockStatement:820-823 静默跳过，薄口照发以保 unreachable
// 门禁一致）；终结证明走静态 saTryTerms（epilogue/if 分析）。
// 局限（与封存一致）：try 体内 abrupt 退出（return/break）跳过后随 finally
// 代码，finally 仅直落路径精确）。
func saLowerTry(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	ts := s.AsTryStatement()
	if ts.TryBlock != nil {
		if idx := saTopThrowIndex(ts.TryBlock.AsBlock().Statements.Nodes); idx >= 0 {
			return saLowerThrowingTry(w, s, ts, ts.TryBlock.AsBlock().Statements.Nodes, idx, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		}
	}
	if saContainsThrow(s) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "throw inside try is not lowerable (catch cannot resume after panic)"})
		return false, true
	}
	if ts.TryBlock != nil {
		if !saLowerArm(w, ts.TryBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
	}
	// catch 永不可达（无 throw）：整块跳过，不绑定。
	if ts.FinallyBlock != nil {
		if !saLowerArm(w, ts.FinallyBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
	}
	tryTerm, finTerm := saTryTerms(s)
	return tryTerm || finTerm, false
}

// saBindPendingLabels 将待绑标号附到刚压栈的目标上（`a: b: for` 双绑同环；
// block/switch 为 break-only；形状证据：封存 labels.go:38-58）。
// 调用点：每个 loops/breaks 压栈之后（while/for/for-of/for-in/do/switch/标号块）。
func saBindPendingLabels(scope *saScope, breakOnly bool) {
	if len(scope.pending) == 0 {
		return
	}
	if scope.labels == nil {
		scope.labels = map[string]saLoop{}
	}
	fr := scope.loops[len(scope.loops)-1]
	ld := saLoop{end: fr.end}
	if !breakOnly {
		ld.top = fr.top
		ld.cont = fr.cont
	}
	for _, nm := range scope.pending {
		scope.labels[nm] = ld
	}
	scope.pending = nil
}

// saLowerLabeled lowering `lbl: stmt`（仅 loops/switch/block；形状证据：封存
// lowerLabeled:63-98：标号随内层语句绑定、随语句消亡；串行复用合法，同名嵌套拒；
// 非三者大声拒。标号块为 break-only（continue 落此拒），体经 saLowerArm 直跑，
// end 落空点；break 到自标号不视为语句终结（落空继续），return 终结则透传）。
func saLowerLabeled(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	ls := s.AsLabeledStatement()
	lbl := ls.Label.Text()
	if _, dup := scope.labels[lbl]; dup {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate label " + lbl + " is not lowerable (labels share the function scope)"})
		return false, true
	}
	inner := ls.Statement
	if inner.Kind == ast.KindBlock {
		stmts, ok := saEmbeddedBlock(inner)
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported labeled block"})
			return false, true
		}
		endL := fmt.Sprintf("L_lbl_end_%d", *nextLabel)
		*nextLabel++
		scope.pending = append(scope.pending, lbl)
		scope.loops = append(scope.loops, saLoop{end: endL})
		saBindPendingLabels(scope, true)
		armOK := saLowerArm(w, stmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.loops = scope.loops[:len(scope.loops)-1]
		delete(scope.labels, lbl)
		if !armOK {
			return false, true
		}
		w.Write(fmt.Sprintf("%s:\n", endL))
		// 终结性：仅 return/throw 透传；break/continue 落空（不终结）。
		if len(stmts) > 0 {
			if last := stmts[len(stmts)-1]; last != nil && (last.Kind == ast.KindReturnStatement || last.Kind == ast.KindThrowStatement) {
				return true, false
			}
		}
		return false, false
	}
	switch inner.Kind {
	case ast.KindForStatement, ast.KindWhileStatement, ast.KindForOfStatement,
		ast.KindForInStatement, ast.KindDoStatement, ast.KindSwitchStatement,
		ast.KindLabeledStatement:
		scope.pending = append(scope.pending, lbl)
		done, failed := saLowerStmt(w, inner, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		delete(scope.labels, lbl)
		if failed {
			return false, true
		}
		if len(scope.pending) > 0 {
			scope.pending = nil
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("labeled %s did not bind (internal invariant)", inner.Kind.String())})
			return false, true
		}
		return done, false
	default:
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("labeled %s is not lowerable (loops, switch and blocks only)", inner.Kind.String())})
		return false, true
	}
}

// saLowerBreakContinue lowering break/continue（无标号走栈顶；标号形查标号表：
// break 落 end，continue 落 cont（block/switch 无 cont 拒，未定义标号拒）；
// 栈空/表空拒。形状证据：封存 labels.go:129-158）。
func saLowerBreakContinue(w printer.EmitTextWriter, s *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) bool {
	isBreak := s.Kind == ast.KindBreakStatement
	var label *ast.IdentifierNode
	if isBreak {
		label = s.AsBreakStatement().Label
	} else {
		label = s.AsContinueStatement().Label
	}
	if label != nil {
		nm := label.Text()
		ld, ok := scope.labels[nm]
		if !ok {
			ln, col := pos(s.Pos())
			kind := "break"
			if !isBreak {
				kind = "continue"
			}
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: kind + " to undefined label " + nm + " is not lowerable"})
			return false
		}
		if isBreak {
			w.Write(fmt.Sprintf("  jmp %s\n", ld.end))
			return true
		}
		if ld.cont == "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "continue to non-loop label " + nm + " is not lowerable"})
			return false
		}
		w.Write(fmt.Sprintf("  jmp %s\n", ld.cont))
		return true
	}
	if len(scope.loops) == 0 {
		ln, col := pos(s.Pos())
		kind := "break"
		if !isBreak {
			kind = "continue"
		}
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: kind + " outside loop"})
		return false
	}
	fr := scope.loops[len(scope.loops)-1]
	if isBreak {
		w.Write(fmt.Sprintf("  jmp %s\n", fr.end))
		return true
	}
	if fr.cont == "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "continue to non-loop target"})
		return false
	}
	w.Write(fmt.Sprintf("  jmp %s\n", fr.cont))
	return true
}

// saLowerIf 处理 if/else（void 与 i32 值两形，支持嵌套；嵌套走同一函数递归）。
// 条件：绑定标识符直接用（形状锁），其余 i32 操作数先求值到临时量。
func saLowerIf(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	iv := s.AsIfStatement()
	// false 恒假消死臂。
	if iv.Expression != nil && iv.Expression.Kind == ast.KindFalseKeyword {
		return true
	}
	if be := saBoolSideCond(iv.Expression, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
		return false
	}
	thenStmts, ok := saEmbeddedBlock(iv.ThenStatement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported then branch"})
		return false
	}
	var elseStmts []*ast.Node
	hasElse := iv.ElseStatement != nil
	if hasElse {
		elseStmts, ok = saEmbeddedBlock(iv.ElseStatement)
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported else branch"})
			return false
		}
	}
	condOp, msg := saCondOperand(w, iv.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported condition kind: " + msg})
		return false
	}
	needImport("sa_std/control.sal")
	if hasElse {
		thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
		*nextLabel++
		elseLabel := fmt.Sprintf("L_else_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  EXPAND IF_ELSE %s, %s, %s\n", condOp, thenLabel, elseLabel))
		w.Write(thenLabel + ":\n")
		if !saLowerArm(w, thenStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		w.Write(elseLabel + ":\n")
		if !saLowerArm(w, elseStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		return true
	}
	thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
	*nextLabel++
	*nextLabel++ // 预留 else 槽位，与 satsgo 门禁形状对齐（1→3，4→6）
	endifLabel := fmt.Sprintf("L_endif_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  EXPAND IF_TRUE %s, %s, %s\n", condOp, thenLabel, endifLabel))
	w.Write(thenLabel + ":\n")
	if !saLowerArm(w, thenStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
		return false
	}
	if saContainsIf(thenStmts) {
		w.Write(fmt.Sprintf("  jmp %s\n", endifLabel))
	}
	w.Write(endifLabel + ":\n")
	// 无 else 是否缺 return 由函数尾 epilogue 经 saStmtTerminates 统一判定，
	// 此处不拒（循环体/臂内同形亦然）。
	return true
}

func saContainsIf(stmts []*ast.Node) bool {
	for _, s := range stmts {
		if s != nil && s.Kind == ast.KindIfStatement {
			return true
		}
	}
	return false
}

// saStmtTerminates 判定单条语句是否终结控制流（return、break/continue，
// 或两臂皆终结的 if/else）。while/声明/赋值落空（须后继收尾）。
func saStmtTerminates(s *ast.Node) bool {
	if s == nil {
		return false
	}
	switch s.Kind {
	case ast.KindReturnStatement, ast.KindBreakStatement, ast.KindContinueStatement,
		ast.KindThrowStatement:
		return true
	case ast.KindTryStatement:
		tryTerm, finTerm := saTryTerms(s)
		return tryTerm || finTerm
	case ast.KindIfStatement:
		iv := s.AsIfStatement()
		if iv.ElseStatement == nil {
			return false
		}
		thenStmts, ok1 := saEmbeddedBlock(iv.ThenStatement)
		elseStmts, ok2 := saEmbeddedBlock(iv.ElseStatement)
		if !ok1 || !ok2 {
			return false
		}
		return saArmTerminates(thenStmts) && saArmTerminates(elseStmts)
	case ast.KindSwitchStatement:
		// 穷尽 switch（有 default 且每臂终结）即终结：必有一臂跑，
		// 臂皆终结则整体终结；无 default 可落空。
		sw := s.AsSwitchStatement()
		if sw.CaseBlock == nil {
			return false
		}
		hasDefault := false
		for _, cl := range sw.CaseBlock.AsCaseBlock().Clauses.Nodes {
			switch cl.Kind {
			case ast.KindDefaultClause:
				hasDefault = true
				if !saArmTerminates(cl.AsCaseOrDefaultClause().Statements.Nodes) {
					return false
				}
			case ast.KindCaseClause:
				if !saArmTerminates(cl.AsCaseOrDefaultClause().Statements.Nodes) {
					return false
				}
			default:
				return false
			}
		}
		return hasDefault
	default:
		return false
	}
}

func saArmTerminates(stmts []*ast.Node) bool {
	if len(stmts) == 0 {
		return false
	}
	return saStmtTerminates(stmts[len(stmts)-1])
}

func saEmbeddedBlock(n *ast.Node) ([]*ast.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.Kind == ast.KindBlock {
		return n.AsBlock().Statements.Nodes, true
	}
	// 单语句臂视为单元素块（return/if）。
	return []*ast.Node{n}, true
}

// saLowerArm 处理臂/循环体语句（经 saLowerStmt 与函数体共用全语句集）。
// 臂内终结后仍有语句同样大声拒（与函数体同门）。
