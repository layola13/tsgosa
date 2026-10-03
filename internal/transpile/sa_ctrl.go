// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"strings"

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
		ast.KindPercentEqualsToken:          "srem",
		ast.KindLessThanLessThanEqualsToken: "shl", ast.KindGreaterThanGreaterThanEqualsToken: "ashr",
		ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: "lshr",
		ast.KindAmpersandEqualsToken:                         "and", ast.KindBarEqualsToken: "or", ast.KindCaretEqualsToken: "xor",
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
	base, msg := saArrStoreBase(w, ea.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
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
		base, msg := saArrStoreBase(w, ea.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
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
		// f64 compound (fadd/fsub/fmul/fdiv; direct plain rebind like f64 assign).
		if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
			if k, bound := scope.types[be.Left.Text()]; bound && k == "f64" {
				r, msg := saEvalF64(w, be.Right, scope, pos, refusals, nextTemp)
				if msg != "" {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
					return false
				}
				var fop string
				switch saBinaryOpKind(be) {
				case ast.KindPlusEqualsToken:
					fop = "fadd"
				case ast.KindMinusEqualsToken:
					fop = "fsub"
				case ast.KindAsteriskEqualsToken:
					fop = "fmul"
				case ast.KindSlashEqualsToken:
					fop = "fdiv"
				default:
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "float compound operator is not lowerable"})
					return false
				}
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, fop, be.Left.Text(), r))
				w.Write(fmt.Sprintf("  %s = %s\n", be.Left.Text(), t))
				return true
			}
		}
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
		// 顶层可变槽复合赋值（读-改-写回；先读后右值，与上游同序；i32 独占；
		// 形状证据：封存 lowerCompoundAssign:3373-3437 标识符分支）。
		if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
			if ms, ok := scope.modVars[be.Left.Text()]; ok && ms.w == "i32" {
				if _, shadowed := scope.types[be.Left.Text()]; !shadowed {
					cur := saModLoadI32(w, ms, scope, nextTemp)
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
					saModStoreI32(w, ms, t, scope, nextTemp)
					return true
				}
			}
		}
		// 串槽复合即计算串存储，沿字面存储门大声拒（封存 emitModStoreStringDispatch:808-820）。
		if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
			if ms, ok := scope.modVars[be.Left.Text()]; ok && ms.w == "str" {
				if _, shadowed := scope.types[be.Left.Text()]; !shadowed {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("module state %s stores string literals and string constants only (computed strings are not lowerable yet)", ms.qual)})
					return false
				}
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
	saStoreLocal(w, target, t, scope, nextTemp)
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
		// 顶层可变槽语句写（i32/串按宽分发；形状证据同值位）。
		if ms, ok := scope.modVars[name]; ok {
			if ms.w == "str" {
				text, ok := saModStrText(be.Right, scope)
				if !ok {
					ln, col := pos(s.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("module state %s stores string literals and string constants only (computed strings are not lowerable yet)", ms.qual)})
					return false
				}
				saModStoreStr(w, ms, text, scope, nextTemp)
				return true
			}
			op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported assignment rhs: " + msg})
				return false
			}
			saModStoreI32(w, ms, op, scope, nextTemp)
			return true
		}
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to unknown variable " + name})
		return false
	}
	if k != "i32" && k != "bool" && k != "arr" && k != "str" && k != "f64" && !strings.HasPrefix(k, "inst:") {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to non-i32 variable " + name})
		return false
	}
	if strings.HasPrefix(k, "inst:") {
		// 实例重绑定（字面量现场构造 + 旧值先释；封存 lowerCompoundAssign
		// 标识符分支读-改-写回同序；句柄对拷无显式 clone 语义，沿上游拒）。
		if be.Right == nil || be.Right.Kind != ast.KindObjectLiteralExpression {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "struct reassignment needs an object literal"})
			return false
		}
		defname := strings.TrimPrefix(k, "inst:")
		h, _, msg := saLowerObjectLiteral(w, be.Right, defname, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return false
		}
		saRebindRelease(w, scope, name)
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		saConsumeOwn(scope, h)
		// 新鲜句柄入位：置堆 + 复位（saStoreLocal live+temp 分支同形）。
		if b := saOwnOf(scope, name); b != nil {
			b.heap = true
		}
		saMarkRebound(scope, name)
		return true
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
	if k == "f64" {
		// f64 plain rebind (direct write like i32-plain; no release: plain never owned).
		op, msg := saEvalF64(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported assignment rhs: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
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
	saStoreLocal(w, name, op, scope, nextTemp)
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
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL, depth: len(scope.ownOrder)})
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

// saModSlotTarget 取未遮蔽 i32 槽目标（for 头专用；局部遮蔽优先；
// 形状证据：封存 lowerCompoundAssign:3373-3437 标识符分支同形）。
func saModSlotTarget(e *ast.Node, scope *saScope) (*saModState, bool) {
	if e != nil && e.Kind == ast.KindIdentifier {
		if ms, ok := scope.modVars[e.Text()]; ok && ms.w == "i32" {
			if _, shadowed := scope.types[e.Text()]; !shadowed {
				return ms, true
			}
		}
	}
	return nil, false
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
		// 槽计数器（不绑定局部，直存槽；封存 lowerCompoundAssign 标识符分支同形）。
		if ms, ok := saModSlotTarget(be.Left, scope); ok {
			op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(init.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer: " + msg})
				return false
			}
			saModStoreI32(w, ms, op, scope, nextTemp)
			return true
		}
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
		saStoreLocal(w, name, op, scope, nextTemp)
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
		saStoreLocal(w, target, t, scope, nextTemp)
	}
	switch incr.Kind {
	case ast.KindPostfixUnaryExpression:
		un := incr.AsPostfixUnaryExpression()
		if un.Operator != ast.KindPlusPlusToken && un.Operator != ast.KindMinusMinusToken {
			break
		}
		target, ok := saBoundI32(scope, un.Operand)
		if !ok {
			// 槽增量（读-改-写回，无旧值临时量；与增量位同形）。
			if ms, ok := saModSlotTarget(un.Operand, scope); ok {
				op := "add"
				if un.Operator == ast.KindMinusMinusToken {
					op = "sub"
				}
				cur := saModLoadI32(w, ms, scope, nextTemp)
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, cur))
				saModStoreI32(w, ms, t, scope, nextTemp)
				return true
			}
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
			// 槽增量（读-改-写回，无旧值临时量；与增量位同形）。
			if ms, ok := saModSlotTarget(un.Operand, scope); ok {
				op := "add"
				if un.Operator == ast.KindMinusMinusToken {
					op = "sub"
				}
				cur := saModLoadI32(w, ms, scope, nextTemp)
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, cur))
				saModStoreI32(w, ms, t, scope, nextTemp)
				return true
			}
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
				// 槽赋值形增量（直存槽；与初始化位同形）。
				if ms, ok := saModSlotTarget(be.Left, scope); ok {
					r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						ln, col := pos(incr.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
						return false
					}
					saModStoreI32(w, ms, r, scope, nextTemp)
					return true
				}
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
			// 槽复合增量（读-改-写回；与语句位同形）。
			if ms, ok := saModSlotTarget(be.Left, scope); ok {
				cur := saModLoadI32(w, ms, scope, nextTemp)
				r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
				if msg != "" {
					ln, col := pos(incr.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
					return false
				}
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, cur, r))
				saModStoreI32(w, ms, t, scope, nextTemp)
				return true
			}
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
	// 计数变量与整个 for 句同域（封存 lowerFor:2046 pushScope 覆盖 init+体，
	// tryLowerForMacro:1968 同形）：两个 `for (let i…)` 各自成域，同名可复用。
	saved := saScopeEnter(scope)
	defer saScopeExit(scope, saved)
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
	scope.loops = append(scope.loops, saLoop{top: topL, cont: contL, end: endL, depth: len(scope.ownOrder)})
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
	scope.loops = append(scope.loops, saLoop{top: topL, cont: contL, end: endL, depth: len(scope.ownOrder)})
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
	scope.loops = append(scope.loops, saLoop{top: loopL, cont: condL, end: endL, depth: len(scope.ownOrder)})
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
	// discriminant i32 优先、败则串（串句柄 eq 分发，与上游 lowerExpr 通用同形）。
	disc, msg := saEvalI32(w, sw.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		if h, smsg := saEvalStr(w, sw.Expression, scope, pos, refusals, nextTemp); smsg == "" {
			disc = h
		} else {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported switch discriminant: " + msg})
			return false
		}
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
	scope.loops = append(scope.loops, saLoop{end: endL, depth: len(scope.ownOrder)})
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
			// case 值败则串（与 discriminant 同门；混合臂 eq 恒假落 default）。
			if h, smsg := saEvalStr(w, p.node.AsCaseOrDefaultClause().Expression, scope, pos, refusals, nextTemp); smsg == "" {
				val = h
			} else {
				ln, col := pos(p.node.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported case value: " + vmsg})
				lowered = false
				break
			}
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
			if h, smsg := saEvalStr(w, p.node.AsCaseOrDefaultClause().Expression, scope, pos, refusals, nextTemp); smsg == "" {
				val = h
			} else {
				ln, col := pos(p.node.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported case value: " + vmsg})
				return false
			}
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
	scope.loops = append(scope.loops, saLoop{end: endL, depth: len(scope.ownOrder)})
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
		scope.types[nm.Text()] = "i32"
		saStoreLocal(w, nm.Text(), val, scope, nextTemp)
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
	ld := saLoop{end: fr.end, depth: fr.depth}
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
		scope.loops = append(scope.loops, saLoop{end: endL, depth: len(scope.ownOrder)})
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
			saReleaseDeeperThan(w, scope, ld.depth)
			w.Write(fmt.Sprintf("  jmp %s\n", ld.end))
			return true
		}
		if ld.cont == "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "continue to non-loop label " + nm + " is not lowerable"})
			return false
		}
		saReleaseDeeperThan(w, scope, ld.depth)
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
		saReleaseDeeperThan(w, scope, fr.depth)
		w.Write(fmt.Sprintf("  jmp %s\n", fr.end))
		return true
	}
	if fr.cont == "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "continue to non-loop target"})
		return false
	}
	saReleaseDeeperThan(w, scope, fr.depth)
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
		// then 臂落空直入 else 即错臂执行（fizzbuzz 5,3,1 实证）；非终结臂须跳 end（上游同形）。
		// 无跳则不落标：双终结臂后裸 endif 标号触 FallthroughForbidden。
		needEnd := !saArmTerminates(thenStmts)
		endifLabel := ""
		if needEnd {
			endifLabel = fmt.Sprintf("L_endif_%d", *nextLabel)
			*nextLabel++
			w.Write(fmt.Sprintf("  jmp %s\n", endifLabel))
		}
		w.Write(elseLabel + ":\n")
		if !saLowerArm(w, elseStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		if needEnd {
			w.Write(endifLabel + ":\n")
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
	// 臂终结（return/jmp 收尾）则免 join 跳转——落在终结符后即不可达陷阱，
	// 真机 `FallthroughForbidden`；封存上游同位：终结臂无 `jmp L_endif`。
	if saContainsIf(thenStmts) && !saArmTerminates(thenStmts) {
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
// saEndsWithBareSwitchLabel 报告语句列是否以穷尽 switch 收尾（臂全终结而
// endswitch 标号后无终结符，函数尾即悬空标号，真机 `FallthroughForbidden`；
// 封存上游函数 epilogue `if !e.terminated` 恒补 return 同形——本仓穷尽 switch
// 判终结（上游恒不断），故仅在此形补结构终结 `ret`，缺 return 拒因不受影响）。
func saEndsWithBareSwitchLabel(stmts []*ast.Node) bool {
	if len(stmts) == 0 {
		return false
	}
	last := stmts[len(stmts)-1]
	return last != nil && last.Kind == ast.KindSwitchStatement && saStmtTerminates(last)
}

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
	case ast.KindBlock:
		// 裸块终结态同其末句（switch 臂 `{ … break; }` 由此免去多余落空跳；
		// 与 KindBlock 语句位 saLowerStmt 的回传同形）。
		bd := s.AsBlock()
		if bd == nil || bd.Statements == nil {
			return false
		}
		return saArmTerminates(bd.Statements.Nodes)
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

// ── 归属记录与 `!` 释放（所有权模型 v3 端口）──
// 形状证据总纲：封存 Scope/ownership:11004-11023（Owned：形参、调用结果、
// alloc 结果、具名串/数组/结构体句柄；Non-owned：立即数、纯临时量、普通标量
// 绑定；`!` 对非归属仍合法）+ declareOwned/declareAlias/declarePlain +
// setHeap/isOwnedTemp/consume/markRebound/rebindRelease:11048-11102 +
// releaseAllOwnedExcept:11338-11360（return 释放除返回值外一切，绑定留位标
// 已释放）+ releaseScope:11306-11336（块出口释顶层域，终结块静默）。
// 本仓 types 扁平，故归属表亦随快照-回滚进出（与种表同命）。
type saOwn struct {
	heap     bool // 归属：须释放/移动/返回（形参、temp 源绑定、句柄）
	consumed bool // 已移动走
	released bool // 已发 `!`
}

// saDeclareOwned 登记归属绑定（形参、temp 源初值、句柄；复登即新值新命——
// 置堆并复位旗标，封存 markRebound:11100-11106 新值待未来释放同形）。
func saDeclareOwned(scope *saScope, name string) {
	if scope.ownState == nil {
		scope.ownState = map[string]*saOwn{}
	}
	if b, ok := scope.ownState[name]; ok {
		b.heap = true
		b.released = false
		b.consumed = false
		return
	}
	scope.ownState[name] = &saOwn{heap: true}
	scope.ownOrder = append(scope.ownOrder, name)
}

// saDeclarePlain 登记非归属标量绑定（立即数/具名源初值；封存 declarePlain:11079-11091）。
// 复登即新值：清堆并复位旗标（回调遮蔽恢复由调用方快照归属表，见 saCallbackValue）。
func saDeclarePlain(scope *saScope, name string) {
	if scope.ownState == nil {
		scope.ownState = map[string]*saOwn{}
	}
	if b, ok := scope.ownState[name]; ok {
		b.heap = false
		b.released = false
		b.consumed = false
		return
	}
	scope.ownState[name] = &saOwn{}
	scope.ownOrder = append(scope.ownOrder, name)
}

// saOwnOf 查归属记录（temps 永不登记，封存 isTempName 跳过同形）。
func saOwnOf(scope *saScope, name string) *saOwn {
	if scope.ownState == nil {
		return nil
	}
	return scope.ownState[name]
}

// saConsumeOwn 标记移动走（归属且未释放者；封存 consume:11099-11106）。
func saConsumeOwn(scope *saScope, name string) {
	if b := saOwnOf(scope, name); b != nil && b.heap && !b.released {
		b.consumed = true
	}
}

// saReleaseAllOwnedExcept 返前释放：逆声明序释全部归属 live 者，返回值除外
// （封存 releaseAllOwnedExcept:11338-11360；绑定留位，防早返后外层名失联）。
func saReleaseAllOwnedExcept(w printer.EmitTextWriter, scope *saScope, except string) {
	done := map[string]bool{}
	for i := len(scope.ownOrder) - 1; i >= 0; i-- {
		name := scope.ownOrder[i]
		if name == except || done[name] {
			continue
		}
		done[name] = true
		if b := saOwnOf(scope, name); b != nil && b.heap && !b.consumed && !b.released {
			w.Write(fmt.Sprintf("  !%s\n", name))
			b.released = true
		}
	}
}

// saRebindRelease 重绑定前释 live 具名寄存器（temps 为循环携带 SSA 值，
// 直接重绑；封存 rebindRelease:11089-11097）。
func saRebindRelease(w printer.EmitTextWriter, scope *saScope, dst string) {
	if saIsTempOp(dst) {
		return
	}
	if b := saOwnOf(scope, dst); b != nil && b.heap && !b.consumed && !b.released {
		w.Write(fmt.Sprintf("  !%s\n", dst))
		b.released = true
	}
}

// saMarkRebound 标 fresh 绑定（新值待未来释放；封存 markRebound:11100-11106）。
func saMarkRebound(scope *saScope, dst string) {
	if b := saOwnOf(scope, dst); b != nil {
		b.released = false
		b.consumed = false
	}
}

// saStoreLocal 按赋值纪律落标量 `dst = src`（封存 assignLocal:11108-11171）：
// fresh+temp 直搬+归属；fresh+named 快照+普通；fresh+imm 直赋+普通；
// live 先 rebindRelease，再 named 双 temp 快照、temp/imm 直赋，并置堆位
// （temp 源置堆+复位，named/imm 源清堆；imm 源不复位，沿上原文）。
func saStoreLocal(w printer.EmitTextWriter, dst, src string, scope *saScope, nextTemp *int) {
	b := saOwnOf(scope, dst)
	fresh := b == nil
	srcIsTemp := saIsTempOp(src)
	_, srcIsNamed := scope.types[src]
	if fresh {
		if srcIsTemp {
			w.Write(fmt.Sprintf("  %s = %s\n", dst, src))
			saConsumeOwn(scope, src)
			saDeclareOwned(scope, dst)
			return
		}
		if srcIsNamed && !srcIsTemp {
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", dst, src))
			saDeclarePlain(scope, dst)
			return
		}
		w.Write(fmt.Sprintf("  %s = %s\n", dst, src))
		saDeclarePlain(scope, dst)
		return
	}
	saRebindRelease(w, scope, dst)
	if srcIsNamed && !srcIsTemp {
		c1 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		c2 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", c1, src))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", c2, c1))
		w.Write(fmt.Sprintf("  %s = %s\n", dst, c2))
		b.heap = false
		saMarkRebound(scope, dst)
		return
	}
	w.Write(fmt.Sprintf("  %s = %s\n", dst, src))
	if srcIsTemp {
		saConsumeOwn(scope, src)
		b.heap = true
		saMarkRebound(scope, dst)
		return
	}
	b.heap = false
}

// saOwnTemp 登记归属临时量（封存 ownTemp:11280-11282：即 declareOwned；
// temps 亦入 ownOrder，返前释放同覆盖）。
func saOwnTemp(scope *saScope, t string) {
	saDeclareOwned(scope, t)
}

// saReleaseOwnedTemp 释归属临时量（具名绑定永不经此口；封存
// releaseIfOwnedTemp:11286-11293）。
func saReleaseOwnedTemp(w printer.EmitTextWriter, scope *saScope, t string) {
	if !saIsTempOp(t) {
		return
	}
	if b := saOwnOf(scope, t); b != nil && b.heap && !b.consumed && !b.released {
		w.Write(fmt.Sprintf("  !%s\n", t))
		b.released = true
	}
}

// saReleaseDeeperThan 跳前释放：释下标深于 depth 的归属 live 绑定
// （逆声明序；封存 releaseForJump:11366-11385：只释被抛弃的臂/块域，
// 外层域存活以保 join 点状态一致）。
func saReleaseDeeperThan(w printer.EmitTextWriter, scope *saScope, depth int) {
	if depth < 0 {
		depth = 0
	}
	if depth > len(scope.ownOrder) {
		return
	}
	done := map[string]bool{}
	for i := len(scope.ownOrder) - 1; i >= depth; i-- {
		name := scope.ownOrder[i]
		if done[name] {
			continue
		}
		done[name] = true
		if b := saOwnOf(scope, name); b != nil && b.heap && !b.consumed && !b.released {
			w.Write(fmt.Sprintf("  !%s\n", name))
			b.released = true
		}
	}
}

// saReleaseExceptOp 返前释放便捷口：返回操作数（具名或临时量）即除外，
// 否则全释（封存 releaseAllOwnedExcept(except=v) 按名精确除外，不分具名/temp）。
func saReleaseExceptOp(w printer.EmitTextWriter, scope *saScope, op string) {
	saReleaseAllOwnedExcept(w, scope, op)
}

// saIsTempOp 报告操作数是否为编译临时量 `t_N`（封存 isTempName 同形）。
func saIsTempOp(op string) bool {
	if len(op) < 3 || op[0] != 't' || op[1] != '_' {
		return false
	}
	for i := 2; i < len(op); i++ {
		if op[i] < '0' || op[i] > '9' {
			return false
		}
	}
	return true
}

// saScopeEnter 开块域（上游各 arm/loop 体各自 pushScope 同形：封存 lowerIf:1758 /
// lowerWhile:1817 / lowerDoWhile:1968 / lowerFor:2046（for 另在 2004 为体再开一层）/
// lowerForOf:2137 / lowerSwitch 臂:2642 与 tryLowerSwitchMacro 臂:2563 / KindBlock:847-848）。
// 本仓 types 是扁平种表，故以「快照-回滚」等价实现逐层进出：块内新登记的
// 名字出块即不可见，同名兄弟块可复用（顺序复用同寄存器，语义等价）。
// saScopeSaved 块域快照（种表 + 归属表截断点；归属旗标沿上游持久化，
// 不回滚——封存 releaseScope 释后标 released 跨早返仍有效 :11352-11356）。
type saScopeSaved struct {
	types map[string]string
	owned int
}

func saScopeEnter(scope *saScope) saScopeSaved {
	saved := make(map[string]string, len(scope.types))
	for k, v := range scope.types {
		saved[k] = v
	}
	return saScopeSaved{types: saved, owned: len(scope.ownOrder)}
}

// saScopeExit 闭块域：丢弃块内新登记的名字，还原被遮蔽名的外层种（封存
// popScope:11031-11035 + lookupBinding:11037-11044 逐层回查同形）。
// 仍可见名的块内重名一律先被 `duplicate local` 大声拒（封存无此门且
// 会静默复用同寄存器致外层读错值——铁律 4「禁静默错码」高于逐字同形，
// 见 AGENTS.md §4 差分门禁）。
func saScopeExit(scope *saScope, saved saScopeSaved) {
	for k := range scope.types {
		if _, ok := saved.types[k]; !ok {
			delete(scope.types, k)
		}
	}
	for k, v := range saved.types {
		scope.types[k] = v
	}
	// 归属表截断：块内新登记名出块即除名（种表同命；旗标不回滚）。
	for _, name := range scope.ownOrder[saved.owned:] {
		delete(scope.ownState, name)
	}
	scope.ownOrder = scope.ownOrder[:saved.owned]
}

// saLowerArm 处理臂/循环体语句（经 saLowerStmt 与函数体共用全语句集）。
// 臂内终结后仍有语句同样大声拒（与函数体同门）。
// 块域自此入口统一开关（上游各 arm/loop 体各自 pushScope 同形，见
// saScopeEnter 证据表）：臂内局部名不外泄，兄弟臂同名可复用。
