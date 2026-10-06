// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"sort"
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
		// Map 种基走 set 脱糖（`m[k] = v` ≡ `m.set(k, v)`；Set 无键值拒）。
		if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
			if k, ok := scope.types[ea.Expression.Text()]; ok && (k == "map" || k == "set") {
				if k == "set" {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "Set index stores need .add (no keyed values)"})
					return false
				}
				if msg := saLowerMapIndexStore(w, ea.Expression.Text(), ea.ArgumentExpression, be.Right, scope, pos, refusals, nextTemp); msg != "" {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
				return true
			}
		}
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
	// 元素右值记种检查（串/实例句柄禁入 i32 槽；与上同形；铁律 4）。
	if msg := saCheckI32Value(scope, rhs); msg != "" {
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
			// Map/Set 种基复合写须读改写回，另步；此处大声拒（禁数组错码）。
			if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
				if k, ok := scope.types[ea.Expression.Text()]; ok && (k == "map" || k == "set") {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "map compound stores need read-modify-write (not lowerable)"})
					return false
				}
			}
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
		// 复合右值记种检查（串/实例句柄禁入 i32 累加；与标识符目标同形；铁律 4）。
		if msg := saCheckI32Value(scope, r); msg != "" {
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
				// 串重绑先释旧柄（H13：与上游 `!s` 先释同形；saConcatSlices
				// 已释输入 temps，具名旧值在此释；新柄 consume+复位防双释）。
				saRebindRelease(w, scope, be.Left.Text())
				w.Write(fmt.Sprintf("  %s = %s\n", be.Left.Text(), out))
				saConsumeOwn(scope, out)
				if b := saOwnOf(scope, be.Left.Text()); b != nil {
					b.heap = true
				}
				saMarkRebound(scope, be.Left.Text())
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
					// 复合右值记种检查（与局部目标同形；铁律 4）。
					if msg := saCheckI32Value(scope, r); msg != "" {
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
		// 命名空间可变槽复合赋值（`N.K op= v`；读-改-写回同序；i32 独占）。
		if be.Left != nil && be.Left.Kind == ast.KindPropertyAccessExpression {
			lpa := be.Left.AsPropertyAccessExpression()
			if lpa.Expression != nil && lpa.Expression.Kind == ast.KindIdentifier && lpa.Name() != nil && lpa.Name().Kind == ast.KindIdentifier {
				if ms, ok := scope.modVars[lpa.Expression.Text()+"."+lpa.Name().Text()]; ok && ms.w == "i32" {
					cur := saModLoadI32(w, ms, scope, nextTemp)
					r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						ln, col := pos(where.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
						return false
					}
					// 复合右值记种检查（与局部目标同形；铁律 4）。
					if msg := saCheckI32Value(scope, r); msg != "" {
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
	// 复合右值记种检查（串/实例句柄禁入 i32 累加；与声明/实参位同形；铁律 4）。
	if msg := saCheckI32Value(scope, r); msg != "" {
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

// saLowerPowAssign lowering 语句位 `x **= e`（P-ppow：目标限本地 i32 标识，
// 经 pow 核 + saStoreLocal 纪律；元素/modvar/f64 目标沿旧门拒）。
func saLowerPowAssign(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node) bool {
	if be.Left == nil || be.Left.Kind != ast.KindIdentifier {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "compound **= needs a plain identifier target"})
		return false
	}
	target, ok := saBoundI32(scope, be.Left)
	if !ok {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "compound **= needs a plain identifier target"})
		return false
	}
	r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
		return false
	}
	if msg := saCheckI32Value(scope, r); msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
		return false
	}
	res := saLowerPowOps(w, target, r, scope.nextLabel, nextTemp)
	saStoreLocal(w, target, res, scope, nextTemp)
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

// saLowerModLogicAssign lowering 模块槽 `m &&= b`/`m ||= b`/`m ??= b`
// （i32 槽；读-测-写经既有槽口 saModLoadI32/saModStoreI32，真短路两臂经槽
// 汇合同 saLowerLogicAssign 槽形；`??=` 与局部同口径视 0 为空；str 槽沿旧门）。
func saLowerModLogicAssign(w printer.EmitTextWriter, be *ast.BinaryExpression, ms *saModState, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	op := saBinaryOpKind(be)
	cur := saModLoadI32(w, ms, scope, nextTemp)
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	test := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if op == ast.KindAmpersandAmpersandEqualsToken {
		w.Write(fmt.Sprintf("  %s = ne %s, 0\n", test, cur))
	} else {
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", test, cur))
	}
	assignL := fmt.Sprintf("L_modlogas_assign_%d", *scope.nextLabel)
	*scope.nextLabel++
	skipL := fmt.Sprintf("L_modlogas_skip_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_modlogas_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", test, assignL, skipL))
	w.Write(fmt.Sprintf("%s:\n", assignL))
	rhs, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	if msg := saCheckI32Value(scope, rhs); msg != "" {
		return "", msg
	}
	rv := saSnapImm(w, rhs, nextTemp)
	saModStoreI32(w, ms, rv, scope, nextTemp)
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, rv))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", skipL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, cur))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return out, ""
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
	// 模块槽目标（i32 宽经槽口；str 槽与未知沿旧门）。
	if ms, ok := scope.modVars[name]; ok {
		if ms.w == "i32" {
			return saLowerModLogicAssign(w, be, ms, scope, pos, refusals, nextTemp)
		}
		return "", "logical assignment target is not lowerable"
	}
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
	// 逻辑赋值右值记种检查（i32/bool 目标禁串/实例句柄；str 目标沿串求值；铁律 4）。
	if kind != "str" {
		if msg := saCheckI32Value(scope, rhs); msg != "" {
			return "", msg
		}
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

// saContainsReturn 报告子树是否含 return（函数边界重置；内联进调用方后
// return 会逃出外层函数，故注册回调体一律禁 return；镜像 saContainsThrow）。
func saContainsReturn(n *ast.Node) bool {
	found := false
	var walk func(x *ast.Node)
	walk = func(x *ast.Node) {
		if x == nil || found {
			return
		}
		if x.Kind == ast.KindReturnStatement {
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

// saIsTestRegistrationName 报告未解析的测试注册名（test/describe/it；已解析
// 的同名函数/别名/导入沿旧路，禁劫持）。
func saIsTestRegistrationName(scope *saScope, name string) bool {
	if name != "test" && name != "describe" && name != "it" {
		return false
	}
	return saIsUnresolvedTestName(scope, name)
}

// saIsTestHookName 报告未解析的测试钩子名（beforeEach/afterEach/beforeAll
// 直做；afterAll 需整域缓冲未做，见主流程拒因；劫持规则同注册名）。
func saIsTestHookName(scope *saScope, name string) bool {
	if name != "beforeEach" && name != "afterEach" && name != "beforeAll" && name != "afterAll" {
		return false
	}
	return saIsUnresolvedTestName(scope, name)
}

// saIsUnresolvedTestName 报告名未被任何已解析绑定占用（函数/箭头别名/
// math 别名/链接/导入；命中任一即沿旧路，禁劫持用户定义）。
func saIsUnresolvedTestName(scope *saScope, name string) bool {
	if scope == nil {
		return true
	}
	if _, ok := scope.funcs[name]; ok {
		return false
	}
	if k, ok := scope.types[name]; ok && len(k) > 3 && k[:3] == "fn:" {
		return false
	}
	if _, ok := scope.mathAlias[name]; ok {
		return false
	}
	if _, ok := scope.linkResolve[name]; ok {
		return false
	}
	if _, ok := scope.imports[name]; ok {
		return false
	}
	return true
}

// saCheckTestCallback 校验测试回调（零参箭头/函数表达式；体非空、无 return；
// 返回回调体或定位拒因）。
func saCheckTestCallback(fn *ast.Node, name string, s *ast.Node, pos func(int) (int, int), refusals *[]SARefusal) (*ast.Node, bool) {
	fail := func(msg string) (*ast.Node, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return nil, false
	}
	if fn == nil || (fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression) {
		return fail(name + " callback must be an arrow or function expression")
	}
	if len(fn.Parameters()) != 0 {
		return fail(name + " callback takes no parameters")
	}
	body := fn.Body()
	if body == nil {
		return fail(name + " callback has no body")
	}
	if saContainsReturn(body) {
		return fail(name + " body must not return (use assertions)")
	}
	return body, true
}

// saInlineTestUnit 内联一个测试单元体（块体直驱语句循环，自带块域，出口扫尾；
// 表达式体求值丢弃；与注册主流程共用）。
func saInlineTestUnit(w printer.EmitTextWriter, body *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	if body.Kind != ast.KindBlock {
		if body.Kind == ast.KindCallExpression {
			_, _, msg := saEvalCall(w, body.AsCallExpression(), scope, pos, refusals, nextTemp)
			return msg == ""
		}
		_, msg := saEvalI32(w, body, scope, pos, refusals, nextTemp)
		return msg == ""
	}
	stmts, ok := saBlockStmts(body)
	if !ok {
		return false
	}
	// 测试缓冲委托（回调体内 afterAll/.only 直接子命中才走缓冲核）。
	if saScopeNeedsTestBuffer(stmts, scope) {
		ok, _ := saLowerBufferedScope(w, stmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		return ok
	}
	// 回调体自带块域（箭头内局部名不外泄；直驱语句循环以保本域记账可见，
	// saLowerArm 内置 exit 会提前裁掉记账致出口扫尾看不见；出口扫尾释本域归属，
	// 否则顶层合成 @main 无函数尾声扫尾即 MemoryLeak）。
	saved := saScopeEnter(scope)
	terminated := false
	armOK := true
	savedTarget := scope.expectTarget
	savedTargetSet := scope.expectTargetSet
	savedSlot := scope.expectCountSlot
	savedInTest := scope.expectInTest
	scope.expectTargetSet = false
	scope.expectCountSlot = ""
	scope.expectInTest = true
	for _, st := range stmts {
		if terminated {
			continue
		}
		done, failed := saLowerStmt(w, st, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if failed {
			armOK = false
			break
		}
		if done {
			terminated = true
		}
	}
	if armOK && scope.expectTargetSet {
		// 回调尾断言计数检查（assertions 须等，has 须 >0；fail-fast
		// panic(2501) 同形；无 expect 落字则计数视 0）。
		c := "0"
		if scope.expectCountSlot != "" {
			c = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", c, scope.expectCountSlot))
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		if scope.expectTarget == -2 {
			w.Write(fmt.Sprintf("  %s = eq %s, 0\n", t, c))
		} else {
			w.Write(fmt.Sprintf("  %s = ne %s, %d\n", t, c, scope.expectTarget))
		}
		failL := fmt.Sprintf("L_expassert_fail_%d", *nextLabel)
		*nextLabel++
		okL := fmt.Sprintf("L_expassert_ok_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", t, failL, okL))
		w.Write(fmt.Sprintf("%s:\n", failL))
		w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		w.Write(fmt.Sprintf("%s:\n", okL))
	}
	scope.expectTarget = savedTarget
	scope.expectTargetSet = savedTargetSet
	scope.expectCountSlot = savedSlot
	scope.expectInTest = savedInTest
	saReleaseDeeperThan(w, scope, saved.owned)
	saScopeExit(scope, saved)
	return armOK
}

// saLowerTestRegistration lowering 测试注册调用（`test/describe/it("name", () => {...})`
// 箭头体就地内联顺序执行；fail-fast，无隔离；用户 mandate 全量测试框架 track 2）。
// 钩子：beforeEach/afterEach 注册体按序贴到后续 test 内联前/后（同域顺序语义，
// 无套件提升——vitest 会提升，此处以文本序为准）；beforeAll 就地跑一次；afterAll
// 需整域缓冲未做，大声拒。describe 自身透明（只分组），其内 hook 进出按栈存取
// saEmitExpectCount 断言计数+1（槽回调域内建，归属登记随 `saReleaseDeeperThan`
// 自动收；assertions 语义本即当前 test，跨回调不累计）。
func saEmitExpectCount(w printer.EmitTextWriter, scope *saScope, nextTemp *int) {
	if scope.expectCountSlot == "" {
		slot := fmt.Sprintf("__expect_count_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		w.Write(fmt.Sprintf("  store %s + 0, 0 as i32\n", slot))
		saDeclareOwned(scope, slot)
		scope.expectCountSlot = slot
	}
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", c, scope.expectCountSlot))
	n := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", n, c))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", scope.expectCountSlot, n))
}

// saLowerExpectCount `expect.assertions(n)`/`expect.hasAssertions()` 登记
// （只记 target 不落字；n 须非负字面量；expect 基劫持沿旧路，禁静默错位）。
func saLowerExpectCount(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (bool, bool) {
	fail := func(msg string) (bool, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return true, false
	}
	_ = isVoid
	e := s.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return false, false
	}
	ce := e.AsCallExpression()
	callee := ce.Expression
	if callee == nil || callee.Kind != ast.KindPropertyAccessExpression {
		return false, false
	}
	pa := callee.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Name() == nil {
		return false, false
	}
	m := pa.Name().Text()
	if m != "assertions" && m != "hasAssertions" {
		return false, false
	}
	base := pa.Expression
	if base == nil || base.Kind != ast.KindIdentifier || base.Text() != "expect" {
		return false, false
	}
	if !saIsUnresolvedTestName(scope, "expect") {
		return false, false
	}
	if !scope.expectInTest {
		return fail("expect.assertions needs a test callback")
	}
	var margs []*ast.Node
	if ce.Arguments != nil {
		margs = ce.Arguments.Nodes
	}
	if m == "hasAssertions" {
		if len(margs) != 0 {
			return fail("expect.hasAssertions takes no arguments")
		}
		scope.expectTarget = -2
		scope.expectTargetSet = true
		return true, true
	}
	if len(margs) != 1 {
		return fail("expect.assertions takes one argument")
	}
	a := margs[0]
	if a == nil || a.Kind != ast.KindNumericLiteral {
		return fail("expect.assertions needs a literal count")
	}
	var n int
	if _, err := fmt.Sscanf(a.Text(), "%d", &n); err != nil || n < 0 {
		return fail("expect.assertions needs a literal count")
	}
	scope.expectTarget = n
	scope.expectTargetSet = true
	return true, true
}

// saStrContentEq 串内容相等值式（both=内容等且等长；idx 自清洁；ah/bh
// 调用方自理；与 `saLowerExpectStrEq` 同指令同序，禁另立口径）。
func saStrContentEq(w printer.EmitTextWriter, ah, bh string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/string.sai")
	ap, al := saExpandStr(w, ah, nextTemp)
	bp, bl := saExpandStr(w, bh, nextTemp)
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_string_index_of(%s, %s, %s, %s, 0)\n", idx, ap, al, bp, bl))
	saOwnTemp(scope, idx)
	at0 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", at0, idx))
	samelen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, %s\n", samelen, al, bl))
	both := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, %s\n", both, at0, samelen))
	saReleaseOwnedTemp(w, scope, idx)
	return both
}

// saDeepArrEq 数组域深相等值式（等长 + 逐元；i32 元 ne/eq 链，str 元内容链；
// 肯定形返失败条件（or 累），否定形返失败条件（and 累）；调用方 br 断言）。
func saDeepArrEq(w printer.EmitTextWriter, ah, bh, elemKind string, neg bool, scope *saScope, nextTemp, nextLabel *int) string {
	la := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", la, ah))
	lb := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lb, bh))
	da := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", da, ah))
	db := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", db, bh))
	acc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if !neg {
		w.Write(fmt.Sprintf("  %s = ne %s, %s\n", acc, la, lb))
	} else {
		w.Write(fmt.Sprintf("  %s = eq %s, %s\n", acc, la, lb))
	}
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_deq_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_deq_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_deq_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, la))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	eoff := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", eoff, i))
	eaddr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", eaddr, da, eoff))
	faddr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", faddr, db, eoff))
	var dd string
	if elemKind == "str" {
		ea := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", ea, eaddr))
		eb := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", eb, faddr))
		both := saStrContentEq(w, ea, eb, scope, nextTemp)
		dd = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		if !neg {
			w.Write(fmt.Sprintf("  %s = eq %s, 0\n", dd, both))
		} else {
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", dd, both))
		}
	} else {
		ea := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", ea, eaddr))
		eb := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", eb, faddr))
		dd = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		if !neg {
			w.Write(fmt.Sprintf("  %s = ne %s, %s\n", dd, ea, eb))
		} else {
			w.Write(fmt.Sprintf("  %s = eq %s, %s\n", dd, ea, eb))
		}
	}
	cc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if !neg {
		w.Write(fmt.Sprintf("  %s = or %s, %s\n", cc, acc, dd))
	} else {
		w.Write(fmt.Sprintf("  %s = and %s, %s\n", cc, acc, dd))
	}
	w.Write(fmt.Sprintf("  %s = %s\n", acc, cc))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return acc
}

// saLowerExpectInstEq 实例深相等（同布局双实例逐 i32 域 eq 链；含非 i32 域/
// 布局不同大声拒；`.not` 翻转；任一臂非实例沿旧路，禁抢 i32/串门）。
func saLowerExpectInstEq(w printer.EmitTextWriter, neg bool, iarg *ast.Node, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int) (bool, bool) {
	var margs []*ast.Node
	if ce.Arguments != nil {
		margs = ce.Arguments.Nodes
	}
	if len(margs) != 1 {
		return false, false
	}
	if iarg == nil || margs[0] == nil {
		return false, false
	}
	iarg = saUnwrapTransparent(iarg)
	marg := saUnwrapTransparent(margs[0])
	if iarg.Kind != ast.KindIdentifier || marg.Kind != ast.KindIdentifier {
		return false, false
	}
	ha, defA, msgA := saInstBase(iarg, scope)
	hb, _, msgB := saInstBase(marg, scope)
	if msgA != "" || msgB != "" {
		return false, false
	}
	if ha == "" || hb == "" {
		return false, false
	}
	ka, oka := scope.types[iarg.Text()]
	kb, okb := scope.types[marg.Text()]
	if !oka || !okb || ka != kb {
		ln, col := pos(iarg.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "cannot compare instances of different layouts"})
		return true, false
	}
	visited := map[string]bool{defA.name: true}
	acc, ok := saDeepInstEq(w, ha, hb, defA, neg, scope, pos, refusals, nextLabel, nextTemp, visited, iarg.Pos())
	if !ok {
		return true, false
	}
	failL := fmt.Sprintf("L_expinst_fail_%d", *nextLabel)
	*nextLabel++
	okL := fmt.Sprintf("L_expinst_ok_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", acc, failL, okL))
	w.Write(failL + ":\n")
	w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
	w.Write(okL + ":\n")
	return true, true
}

// saDeepInstEq 递归深相等值式（返失败条件 bool；同布局逐域：
// i32 eq/str 内容/arr 按元种表/inst 递归子布局；布局不同/未知域/混合元种/
// 递归环大声拒；空布局恒等；调用方 br 断言）。
func saDeepInstEq(w printer.EmitTextWriter, ha, hb string, def *saClassDef, neg bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int, visited map[string]bool, atPos int) (string, bool) {
	refuse := func(msg string) (string, bool) {
		ln, col := pos(atPos)
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return "", false
	}
	keys := make([]string, 0, len(def.offsets))
	for k := range def.offsets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	elemKinds := map[string]string{}
	for _, k := range keys {
		if fk, ok := def.fkinds[k]; ok && (fk == "i32" || fk == "str") {
			continue
		}
		if fk, ok := def.fkinds[k]; ok && fk == "arr" {
			if ek, ok := scope.arrFieldElem[def.name+"."+k]; ok && (ek == "i32" || ek == "str") {
				elemKinds[k] = ek
				continue
			}
		}
		if fk, ok := def.fkinds[k]; ok && fk == "inst" {
			sub, ok := def.fsub[k]
			if !ok {
				return refuse("deep equality needs a recorded sub layout")
			}
			if visited[sub] {
				return refuse("deep equality does not support recursive layouts yet")
			}
			if _, ok := scope.classes[sub]; !ok {
				return refuse("deep equality needs a recorded sub layout")
			}
			continue
		}
		return refuse("deep equality only supports i32/str fields yet")
	}
	acc := ""
	for _, k := range keys {
		var dd string
		if fk, _ := def.fkinds[k]; fk == "inst" {
			ah := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", ah, ha, def.offsets[k]))
			bh := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", bh, hb, def.offsets[k]))
			sub := def.fsub[k]
			subDef := scope.classes[sub]
			visited[sub] = true
			subAcc, ok := saDeepInstEq(w, ah, bh, subDef, neg, scope, pos, refusals, nextLabel, nextTemp, visited, atPos)
			delete(visited, sub)
			if !ok {
				return "", false
			}
			dd = subAcc
		} else if fk, _ := def.fkinds[k]; fk == "arr" {
			ah := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", ah, ha, def.offsets[k]))
			bh := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", bh, hb, def.offsets[k]))
			dd = saDeepArrEq(w, ah, bh, elemKinds[k], neg, scope, nextTemp, nextLabel)
		} else if fk, _ := def.fkinds[k]; fk == "str" {
			ah := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", ah, ha, def.offsets[k]))
			bh := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", bh, hb, def.offsets[k]))
			both := saStrContentEq(w, ah, bh, scope, nextTemp)
			dd = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			if !neg {
				w.Write(fmt.Sprintf("  %s = eq %s, 0\n", dd, both))
			} else {
				w.Write(fmt.Sprintf("  %s = add %s, 0\n", dd, both))
			}
		} else {
			aa := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", aa, ha, def.offsets[k]))
			bb := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", bb, hb, def.offsets[k]))
			dd = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			if !neg {
				w.Write(fmt.Sprintf("  %s = ne %s, %s\n", dd, aa, bb))
			} else {
				w.Write(fmt.Sprintf("  %s = eq %s, %s\n", dd, aa, bb))
			}
		}
		if acc == "" {
			acc = dd
		} else {
			cc := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			if !neg {
				w.Write(fmt.Sprintf("  %s = or %s, %s\n", cc, acc, dd))
			} else {
				w.Write(fmt.Sprintf("  %s = and %s, %s\n", cc, acc, dd))
			}
			acc = cc
		}
	}
	if acc == "" {
		acc = "0"
	}
	return acc, true
}

// saLowerExpectAssertion lowering `expect(actual).toBe(expected)` 语句断言
// （i32 两侧既有求值 + `ne` + 不等即 `panic(2501)`，与 `throw` 终结同形，
// 测试 fail-fast 口径一致；`expect` 被用户绑定时沿旧路，禁劫持）。
// 匹配器：toBe 系（i32）/零元系（i32 零判）/比较系（i32）/串系（`not` 取反
// 同门；其余匹配器/异种臂另步大声拒。
// 返回（接管，成功）。
func saLowerExpectAssertion(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	fail := func(msg string) (bool, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return true, false
	}
	e := s.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return false, false
	}
	ce := e.AsCallExpression()
	outer := ce.Expression
	if outer == nil || outer.Kind != ast.KindPropertyAccessExpression {
		return false, false
	}
	opa := outer.AsPropertyAccessExpression()
	if opa == nil || opa.Expression == nil || opa.Name() == nil {
		return false, false
	}
	matcher := opa.Name().Text()
	base := opa.Expression
	// `.not` 修饰形（`expect(x).not.toBe(y)`）解包取反（相等即败走 `eq`，
	// 与肯定形同槽同终结；非 `not` 属性基沿旧路，禁静默错位）。
	neg := false
	if base.Kind == ast.KindPropertyAccessExpression {
		bpa := base.AsPropertyAccessExpression()
		if bpa == nil || bpa.Name() == nil || bpa.Name().Text() != "not" || bpa.Expression == nil {
			return false, false
		}
		neg = true
		base = bpa.Expression
	}
	if base.Kind != ast.KindCallExpression {
		return false, false
	}
	inner := base.AsCallExpression()
	ib := inner.Expression
	if ib == nil || ib.Kind != ast.KindIdentifier || ib.Text() != "expect" {
		return false, false
	}
	if !saIsUnresolvedTestName(scope, "expect") {
		return false, false
	}
	isZero := matcher == "toBeNull" || matcher == "toBeUndefined" || matcher == "toBeDefined" ||
		matcher == "toBeTruthy" || matcher == "toBeFalsy"
	cmpName := ""
	switch matcher {
	case "toBeGreaterThan":
		cmpName = "sgt"
	case "toBeGreaterThanOrEqual":
		cmpName = "sge"
	case "toBeLessThan":
		cmpName = "slt"
	case "toBeLessThanOrEqual":
		cmpName = "sle"
	}
	if matcher != "toBe" && matcher != "toEqual" && matcher != "toStrictEqual" && !isZero && cmpName == "" &&
		matcher != "toContain" && matcher != "toStartsWith" && matcher != "toEndsWith" && matcher != "toHaveLength" && matcher != "toThrow" && matcher != "toMatch" && matcher != "toBeNaN" {
		return fail("expect()." + matcher + " is not lowerable yet (only toBe/toEqual/toBeNull/toBeDefined/toBeTruthy/toBeFalsy/toBeNaN/toBeGreaterThan/toBeLessThan/toContain/toStartsWith/toEndsWith/toHaveLength/toThrow/toMatch)")
	}
	var iargs []*ast.Node
	if inner.Arguments != nil {
		iargs = inner.Arguments.Nodes
	}
	if len(iargs) != 1 {
		return fail("expect takes one value")
	}
	var margs []*ast.Node
	if ce.Arguments != nil {
		margs = ce.Arguments.Nodes
	}
	if matcher == "toBeNaN" {
		// `toBeNaN` 恒判定（i32 子集无 NaN：浮点字面量早拒，f64 另门；
		// 肯定恒败，`.not` 恒过；实参照常求值保副作用）。
		if len(margs) != 0 {
			return fail("expect().toBeNaN takes no arguments")
		}
		aop, msg := saEvalI32(w, iargs[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return fail(msg)
		}
		_ = aop
		if !neg {
			w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		}
		return true, true
	}
	if matcher == "toBe" || matcher == "toEqual" || matcher == "toStrictEqual" {
		// 实例深相等（同布局双实例逐 i32 域 eq 链；与 `in` 布局门同源）。
		if handled, ok := saLowerExpectInstEq(w, neg, iargs[0], ce, scope, pos, refusals, nextLabel, nextTemp); handled {
			if ok {
				saEmitExpectCount(w, scope, nextTemp)
			}
			return handled, ok
		}
	}
	if isZero {
		// 零元匹配器（`toBeNull/toBeUndefined` 即柄零判，`toBeDefined`/`toBeTruthy` 即
		// 非零判，`toBeFalsy` 即零判；i32/bool 通道恒 0/1，空吸收同门）。
		if len(margs) != 0 {
			return fail("expect()." + matcher + " takes no arguments")
		}
		aop, msg := saEvalI32(w, iargs[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return fail(msg)
		}
		cmp := "ne"
		if matcher == "toBeTruthy" || matcher == "toBeDefined" {
			cmp = "eq"
		}
		if neg {
			if cmp == "eq" {
				cmp = "ne"
			} else {
				cmp = "eq"
			}
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, 0\n", t, cmp, aop))
		failL := fmt.Sprintf("L_exp_fail_%d", *nextLabel)
		*nextLabel++
		okL := fmt.Sprintf("L_exp_ok_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", t, failL, okL))
		w.Write(failL + ":\n")
		w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		w.Write(okL + ":\n")
		return true, true
	}
	if matcher != "toThrow" && len(margs) != 1 {
		return fail("expect()." + matcher + " takes one expected value")
	}
	if matcher == "toContain" || matcher == "toStartsWith" || matcher == "toEndsWith" || matcher == "toMatch" {
		// 串匹配器（`includes/startsWith/endsWith` 既有原语同指令同序：
		// 封存 sa_str.go 对应分支；布尔化后进败臂，`.not` 翻转比较符；
		// 自清洁释所创归属临时量）。
		// booleanize 布尔化失败条件并发射败臂（`neg` 翻转已由调用方选定
		// 比较符；发射后自清洁释 pend 句柄）。
		booleanize := func(cmp, a, b string, pend []string) (bool, bool) {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, cmp, a, b))
			for _, h := range pend {
				saReleaseOwnedTemp(w, scope, h)
			}
			failL := fmt.Sprintf("L_exp_fail_%d", *nextLabel)
			*nextLabel++
			okL := fmt.Sprintf("L_exp_ok_%d", *nextLabel)
			*nextLabel++
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", t, failL, okL))
			w.Write(failL + ":\n")
			w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
			w.Write(okL + ":\n")
			return true, true
		}
		failCmp := "eq"
		if neg {
			failCmp = "ne"
		}
		// `toMatch` 正则形（行内编译 + test，封存 InlineBase/Test 同形；
		// actual 只求值一次，recv 自清洁）。
		if matcher == "toMatch" && len(margs) == 1 && saRegexBaseKind(margs[0], scope) {
			recv, msg := saLowerRegexInlineBase(w, margs[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return fail(msg)
			}
			hit, msg := saLowerRegexTest(w, recv, iargs[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return fail(msg)
			}
			saReleaseOwnedTemp(w, scope, recv)
			return booleanize(failCmp, hit, "0", nil)
		}
		// `toMatch` 串形即子串（含 `toContain` 同形）。
		scope.addImport("sa_std/string.sai")
		ah, msg := saEvalStr(w, iargs[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return fail(msg)
		}
		nh, msg := saEvalStr(w, margs[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return fail(msg)
		}
		ap, al := saExpandStr(w, ah, nextTemp)
		np, nl := saExpandStr(w, nh, nextTemp)
		if matcher == "toContain" || matcher == "toMatch" {
			idx := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_string_index_of(%s, %s, %s, %s, 0)\n", idx, ap, al, np, nl))
			saOwnTemp(scope, idx)
			return booleanize(failCmp, idx, "-1", []string{idx, ah, nh})
		}
		sym := "sa_string_starts_with"
		if matcher == "toEndsWith" {
			sym = "sa_string_ends_with"
		}
		o := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @%s(%s, %s, %s, %s)\n", o, sym, ap, al, np, nl))
		saOwnTemp(scope, o)
		return booleanize(failCmp, o, "0", []string{o, ah, nh})
	}
	if matcher == "toHaveLength" {
		// 数组/串长度断言（句柄总线求柄：绑定/字面量/链式经 saArrValueOf，
		// 串经 saEvalStr；头 +8 u64 即长，与 `.length` 落字同形；`.not` 翻转）。
		if len(margs) != 1 {
			return fail("expect().toHaveLength takes one expected value")
		}
		bop, msg := saEvalI32(w, margs[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return fail(msg)
		}
		h, hmsg := saArrValueOf(w, iargs[0], scope, pos, refusals, nextTemp)
		if hmsg != "" {
			var smsg string
			h, smsg = saEvalStr(w, iargs[0], scope, pos, refusals, nextTemp)
			if smsg != "" {
				return fail(hmsg)
			}
		}
		lt := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lt, h))
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		cmp := "ne"
		if neg {
			cmp = "eq"
		}
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, cmp, lt, bop))
		saReleaseOwnedTemp(w, scope, h)
		failL := fmt.Sprintf("L_exp_fail_%d", *nextLabel)
		*nextLabel++
		okL := fmt.Sprintf("L_exp_ok_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", t, failL, okL))
		w.Write(failL + ":\n")
		w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		w.Write(okL + ":\n")
		return true, true
	}
	if matcher == "toThrow" {
		// 抛断言静态判定（SA panic 不可恢复，catch 不能 resume，无动态捕获；
		// 只认内联箭头/函数表达式体顶层无条件 throw：前缀直行照跑副作用，
		// throw 及其后 erased；条件/调用/标识符引用/带参匹配另步大声拒）。
		if len(margs) != 0 {
			return fail("expect().toThrow with expected error is not lowerable yet")
		}
		target := iargs[0]
		if target == nil || (target.Kind != ast.KindArrowFunction && target.Kind != ast.KindFunctionExpression) {
			return fail("expect().toThrow needs an inline arrow or function expression")
		}
		if target.Body() == nil || target.Body().Kind != ast.KindBlock {
			return fail("expect().toThrow body must be a block")
		}
		tstmts, bok := saBlockStmts(target.Body())
		if !bok {
			return fail("expect().toThrow body must be a block")
		}
		idx := saThrowPrefixEnd(tstmts)
		if idx == -2 {
			return fail("expect().toThrow body must be straight-line with an unconditional throw")
		}
		saved := saScopeEnter(scope)
		throwOK := true
		end := len(tstmts)
		if idx >= 0 {
			end = idx
		}
		for _, st := range tstmts[:end] {
			done, failed := saLowerStmt(w, st, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
			if failed {
				throwOK = false
				break
			}
			if done {
				break
			}
		}
		saReleaseDeeperThan(w, scope, saved.owned)
		saScopeExit(scope, saved)
		if !throwOK {
			return true, false
		}
		willThrow := idx >= 0
		if neg {
			willThrow = !willThrow
		}
		if !willThrow {
			// 未抛（或 `.not` 下有抛）即败：直發 panic。
			w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		}
		return true, true
	}
	// 任一臂串值即串相等（先语法分流零发射；与 `==` 内容相等同形；
	// 混种沿串门大声拒，禁静默强转；比较系匹配器无字典序底座，
	// 串臂到此一律大声拒，禁误走相等断言）。
	if saIsStrValue(iargs[0], scope) || saIsStrValue(margs[0], scope) {
		if matcher != "toBe" && matcher != "toEqual" && matcher != "toStrictEqual" {
			return fail("expect()." + matcher + " does not support string values (only toBe/toEqual/toStrictEqual lower string equality)")
		}
		return saLowerExpectStrEq(w, s, neg, iargs[0], margs[0], scope, pos, refusals, nextLabel, nextTemp)
	}
	aop, msg := saEvalI32(w, iargs[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return fail(msg)
	}
	bop, msg := saEvalI32(w, margs[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return fail(msg)
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	cmp := "ne"
	if neg {
		cmp = "eq"
	}
	if cmpName != "" {
		// 大小比较（i32 有符号直比；落字为失败条件即逆命题，
		// `.not` 取原命题，恒满足排中律）。
		switch cmpName {
		case "sgt":
			cmp = "sle"
		case "sge":
			cmp = "slt"
		case "slt":
			cmp = "sge"
		case "sle":
			cmp = "sgt"
		}
		if neg {
			cmp = cmpName
		}
	}
	w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, cmp, aop, bop))
	failL := fmt.Sprintf("L_exp_fail_%d", *nextLabel)
	*nextLabel++
	okL := fmt.Sprintf("L_exp_ok_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", t, failL, okL))
	w.Write(failL + ":\n")
	w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
	w.Write(okL + ":\n")
	return true, true
}

// saLowerExpectStrEq lowering 串 `toBe/toEqual`（内容等 + 等长，与 `==`
// 内容相等同指令同序，idx 自清洁；`.not` 翻转比较符；混种沿串门大声拒）。
func saLowerExpectStrEq(w printer.EmitTextWriter, s *ast.Node, neg bool, actual, expected *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int) (bool, bool) {
	fail := func(msg string) (bool, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return true, false
	}
	scope.addImport("sa_std/string.sai")
	ah, msg := saEvalStr(w, actual, scope, pos, refusals, nextTemp)
	if msg != "" {
		return fail(msg)
	}
	nh, msg := saEvalStr(w, expected, scope, pos, refusals, nextTemp)
	if msg != "" {
		return fail(msg)
	}
	ap, al := saExpandStr(w, ah, nextTemp)
	np, nl := saExpandStr(w, nh, nextTemp)
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_string_index_of(%s, %s, %s, %s, 0)\n", idx, ap, al, np, nl))
	saOwnTemp(scope, idx)
	at0 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", at0, idx))
	samelen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, %s\n", samelen, al, nl))
	both := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, %s\n", both, at0, samelen))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	cmp := "eq"
	if neg {
		cmp = "ne"
	}
	w.Write(fmt.Sprintf("  %s = %s %s, 0\n", t, cmp, both))
	saReleaseOwnedTemp(w, scope, idx)
	saReleaseOwnedTemp(w, scope, ah)
	saReleaseOwnedTemp(w, scope, nh)
	failL := fmt.Sprintf("L_exp_fail_%d", *nextLabel)
	*nextLabel++
	okL := fmt.Sprintf("L_exp_ok_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", t, failL, okL))
	w.Write(failL + ":\n")
	w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
	w.Write(okL + ":\n")
	return true, true
}

// saCheckEachCallback 校验 each 回调（箭头/函数表达式；形参数 == 行宽且
// 皆裸标识符；注解须与列种一致；体非空、无 return，与 test 回调同门）。
func saCheckEachCallback(fn *ast.Node, width int, kinds []string, name string, s *ast.Node, pos func(int) (int, int), refusals *[]SARefusal) (body *ast.Node, pnames []string, ok bool) {
	fail := func(msg string) (*ast.Node, []string, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return nil, nil, false
	}
	if fn == nil || (fn.Kind != ast.KindArrowFunction && fn.Kind != ast.KindFunctionExpression) {
		return fail(name + ".each callback must be an arrow or function expression")
	}
	params := fn.Parameters()
	if len(params) != width {
		return fail(fmt.Sprintf("%s callback takes %d parameters (%d given)", name+".each", width, len(params)))
	}
	for i, p := range params {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			return fail(name + ".each parameters must be plain identifiers")
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			return fail(name + ".each parameters must be plain identifiers")
		}
		if pd.DotDotDotToken != nil || pd.QuestionToken != nil || pd.Initializer != nil {
			return fail(name + ".each parameters must be plain identifiers")
		}
		if pd.Type != nil {
			if k, kok := saAnnotKind(pd.Type); !kok || k != kinds[i] {
				return fail(name + ".each parameter type must match the table")
			}
		}
		pnames = append(pnames, nm.Text())
	}
	body = fn.Body()
	if body == nil {
		return fail(name + ".each callback has no body")
	}
	if saContainsReturn(body) {
		return fail(name + ".each body must not return (use assertions)")
	}
	return body, pnames, true
}

// saEachCellKind 报告 each 表元的绑定种（数字字面量即 i32，串字面量即
// str；无发射纯判定；其余沿旧门）。
func saEachCellKind(v *ast.Node) (string, bool) {
	if v == nil {
		return "", false
	}
	switch v.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return "str", true
	case ast.KindNumericLiteral:
		return "i32", true
	}
	return "", false
}

// saLowerTestEach lowering `test.each(table)(title, fn)`（`it.each` 同；
// 行字面量表逐行内联顺序执行，fail-fast，与注册体同口径；标题仅验形不落字）。
// 行：数组字面量行（N 元）或标量行（回调单参）；元须 i32/str 字面量且列种
// 一致；回调形参裸标识符，注解须与列种一致；空表零例通过。
// saEachRow 为 each 一行（字面量值 + 绑定种）。
type saEachRow struct {
	vals  []*ast.Node
	kinds []string
}

// saParseEachTable 解析 each 表与标题（字面量表 + 串标题 + 等宽 + 列种一致；
// 纯判定零发射；空表零行宽 0）。
func saParseEachTable(s *ast.Node, base string, table *ast.Node, title *ast.Node, pos func(int) (int, int), refusals *[]SARefusal) (rows []saEachRow, width int, ok bool) {
	fail := func(msg string) ([]saEachRow, int, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return nil, 0, false
	}
	if table == nil || table.Kind != ast.KindArrayLiteralExpression {
		return fail(base + ".each table must be an array literal")
	}
	if title == nil || (title.Kind != ast.KindStringLiteral && title.Kind != ast.KindNoSubstitutionTemplateLiteral) {
		return fail(base + ".each title must be a string literal")
	}
	elems := []*ast.Node{}
	if al := table.AsArrayLiteralExpression(); al != nil && al.Elements != nil {
		elems = al.Elements.Nodes
	}
	width = -1
	for _, r := range elems {
		var vals []*ast.Node
		if r != nil && r.Kind == ast.KindArrayLiteralExpression {
			if rl := r.AsArrayLiteralExpression(); rl != nil && rl.Elements != nil {
				vals = rl.Elements.Nodes
			}
		} else {
			vals = []*ast.Node{r}
		}
		if width < 0 {
			width = len(vals)
		}
		if len(vals) != width {
			return fail(base + ".each rows must all have the same width")
		}
		kinds := make([]string, len(vals))
		for i, v := range vals {
			k, ok := saEachCellKind(v)
			if !ok {
				return fail(base + ".each cells must be number or string literals")
			}
			kinds[i] = k
		}
		rows = append(rows, saEachRow{vals: vals, kinds: kinds})
	}
	if width < 0 {
		width = 0
	}
	for c := 0; c < width; c++ {
		for _, r := range rows[1:] {
			if r.kinds[c] != rows[0].kinds[c] {
				return fail(base + ".each column kinds must be consistent")
			}
		}
	}
	return rows, width, true
}

// saLowerEachRows 行绑定 + 逐行体内联（fail-fast；体 lowering 由调用方注入：
// test 族经 hooks，describe 经 describe 内联； bindings 逐行域隔离）。
func saLowerEachRows(w printer.EmitTextWriter, s *ast.Node, rows []saEachRow, body *ast.Node, pnames []string, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, lowerBody func(body *ast.Node) bool) (bool, bool) {
	for _, r := range rows {
		saved := saScopeEnter(scope)
		caseOK := true
		for c := range r.vals {
			var op string
			var msg string
			if r.kinds[c] == "str" {
				op, msg = saEvalStr(w, r.vals[c], scope, pos, refusals, nextTemp)
			} else {
				op, msg = saEvalI32(w, r.vals[c], scope, pos, refusals, nextTemp)
			}
			if msg != "" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				caseOK = false
				break
			}
			// 形参种预置（saStoreLocal 只管归属不管种表，声明点同形）。
			scope.types[pnames[c]] = r.kinds[c]
			saStoreLocal(w, pnames[c], op, scope, nextTemp)
		}
		if caseOK {
			if !lowerBody(body) {
				caseOK = false
			}
		}
		saReleaseDeeperThan(w, scope, saved.owned)
		saScopeExit(scope, saved)
		if !caseOK {
			return true, false
		}
	}
	return true, true
}

func saLowerTestEach(w printer.EmitTextWriter, s *ast.Node, base string, table *ast.Node, title *ast.Node, fn *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	rows, width, ok := saParseEachTable(s, base, table, title, pos, refusals)
	if !ok {
		return true, false
	}
	var kinds []string
	if len(rows) > 0 {
		kinds = rows[0].kinds
	}
	body, pnames, ok := saCheckEachCallback(fn, width, kinds, base, s, pos, refusals)
	if !ok {
		return true, false
	}
	return saLowerEachRows(w, s, rows, body, pnames, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp,
		func(b *ast.Node) bool {
			return saInlineTestWithHooks(w, b, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		})
}

// saLowerDescribeEach lowering `describe.each(table)(title, fn)`（行复用
// test.each 核，体经 describe 内联；嵌套 test 照常注册）。
func saLowerDescribeEach(w printer.EmitTextWriter, s *ast.Node, table *ast.Node, title *ast.Node, fn *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	rows, width, ok := saParseEachTable(s, "describe", table, title, pos, refusals)
	if !ok {
		return true, false
	}
	var kinds []string
	if len(rows) > 0 {
		kinds = rows[0].kinds
	}
	body, pnames, ok := saCheckEachCallback(fn, width, kinds, "describe", s, pos, refusals)
	if !ok {
		return true, false
	}
	return saLowerEachRows(w, s, rows, body, pnames, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp,
		func(b *ast.Node) bool {
			return saInlineDescribeBody(w, b, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		})
}

// saLowerEachOuter 分发外层 `test.each(table)(title, fn)`（`it.each` 同；
// 形不合沿旧路；劫持沿旧路）。
func saLowerEachOuter(w printer.EmitTextWriter, s *ast.Node, ce *ast.CallExpression, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	fail := func(msg string) (bool, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return true, false
	}
	callee := ce.Expression
	if callee == nil || callee.Kind != ast.KindCallExpression {
		return false, false
	}
	inner := callee.AsCallExpression()
	if inner == nil || inner.Expression == nil || inner.Expression.Kind != ast.KindPropertyAccessExpression {
		return false, false
	}
	pa := inner.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Name() == nil {
		return false, false
	}
	base, prop := pa.Expression.Text(), pa.Name().Text()
	if prop != "each" || (base != "test" && base != "it" && base != "describe") {
		return false, false
	}
	if !saIsUnresolvedTestName(scope, base) {
		return false, false
	}
	var targs []*ast.Node
	if inner.Arguments != nil {
		targs = inner.Arguments.Nodes
	}
	var oargs []*ast.Node
	if ce.Arguments != nil {
		oargs = ce.Arguments.Nodes
	}
	if len(targs) != 1 || len(oargs) != 2 {
		return fail(base + ".each takes a table and (title, callback)")
	}
	if base == "describe" {
		return saLowerDescribeEach(w, s, targs[0], oargs[0], oargs[1], isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	}
	return saLowerTestEach(w, s, base, targs[0], oargs[0], oargs[1], isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
}

// saSubtreeHasCall 报告子树是否含调用/构造（throw 静态判定用；
// 函数边界内不计——内联体外函数不执行）。
func saSubtreeHasCall(n *ast.Node) bool {
	found := false
	var walk func(x *ast.Node)
	walk = func(x *ast.Node) {
		if x == nil || found {
			return
		}
		switch x.Kind {
		case ast.KindCallExpression, ast.KindNewExpression:
			// `console.log` 可证不抛（落字无 panic；参数照查，
			// `console.log(f())` 的 f 可抛）。
			if x.Kind == ast.KindCallExpression {
				if ce := x.AsCallExpression(); ce != nil && saIsConsoleLog(ce) {
					if ce.Arguments != nil {
						for _, a := range ce.Arguments.Nodes {
							walk(a)
							if found {
								return
							}
						}
					}
					return
				}
			}
			found = true
			return
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression,
			ast.KindArrowFunction, ast.KindClassDeclaration:
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

// saIsThrowPrefixPure 报告语句能否作 toThrow 前缀（直行纯语句：
// 无调用/控制流/return/throw/声明；var 初值与表达式须同纯）。
func saIsThrowPrefixPure(st *ast.Node) bool {
	if st == nil {
		return true
	}
	switch st.Kind {
	case ast.KindVariableStatement, ast.KindExpressionStatement,
		ast.KindEmptyStatement, ast.KindDebuggerStatement:
		return !saSubtreeHasCall(st)
	default:
		return false
	}
}

// saThrowPrefixEnd 报告体顶层首个无条件 throw 下标（-1 即无线索；
// -2 即含调用/控制流等复杂形，动态捕获另步）。
func saThrowPrefixEnd(stmts []*ast.Node) int {
	for i, st := range stmts {
		if st == nil {
			continue
		}
		if st.Kind == ast.KindThrowStatement {
			return i
		}
		if !saIsThrowPrefixPure(st) {
			return -2
		}
	}
	return -1
}

// 防外泄。返回（接管，成功）。
func saLowerTestRegistration(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	fail := func(msg string) (bool, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return true, false
	}
	e := s.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return false, false
	}
	ce := e.AsCallExpression()
	callee := ce.Expression
	if callee == nil {
		return false, false
	}
	// `expect.assertions(n)`/`expect.hasAssertions()` 计数登记（回调尾检查；
	// 未命中沿旧路）。
	if handled, ok := saLowerExpectCount(w, s, isVoid, scope, pos, refusals, nextTemp); handled {
		return handled, ok
	}
	// `expect(actual).toBe(expected)` 语句断言就地内联（测试域分发；未命中沿旧路）。
	if handled, ok := saLowerExpectAssertion(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp); handled {
		if ok {
			saEmitExpectCount(w, scope, nextTemp)
		}
		return handled, ok
	}
	// `test.each(table)(title, fn)` 行展开（`it.each` 同；劫持规则同注册名；
	// 未命中沿旧路）。
	if callee.Kind == ast.KindCallExpression {
		if handled, ok := saLowerEachOuter(w, s, ce, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp); handled {
			return handled, ok
		}
		return false, false
	}
	// 成员式 `test.only/skip/todo`（`describe/it` 同）：skip 跳发射（体仅验形）、
	// todo 空过（1 串参，或附体忽略）、only 整文件缓冲未做大声拒；基名劫持规则同。
	if callee.Kind == ast.KindPropertyAccessExpression {
		pa := callee.AsPropertyAccessExpression()
		if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Name() == nil {
			return false, false
		}
		base, prop := pa.Expression.Text(), pa.Name().Text()
		if base != "test" && base != "describe" && base != "it" {
			return false, false
		}
		if !saIsUnresolvedTestName(scope, base) {
			return false, false
		}
		// 内层 `.each(table)` 单独成句无意义（须续接 `(title, fn)`）。
		if prop == "each" {
			return fail(base + ".each(table) needs (title, callback)")
		}
		// 重试/预期失败须 panic 捕获（SA panic 不可恢复），并发须事件循环；
		// 皆无底座，定位拒收禁静默忽略。
		if prop == "retry" || prop == "fails" {
			return fail(base + "." + prop + " needs failure capture (not lowerable)")
		}
		if prop == "concurrent" {
			return fail(base + "." + prop + " needs an event loop (not lowerable)")
		}
		if prop != "only" && prop != "skip" && prop != "todo" {
			return false, false
		}
		if prop == "only" {
			return fail(base + "." + prop + " is supported inside describe() only (function/top-level buffering not yet)")
		}
		var margs []*ast.Node
		if ce.Arguments != nil {
			margs = ce.Arguments.Nodes
		}
		if len(margs) < 1 || len(margs) > 2 {
			return fail(base + "." + prop + " takes a name and an optional callback")
		}
		if margs[0] == nil || (margs[0].Kind != ast.KindStringLiteral && margs[0].Kind != ast.KindNoSubstitutionTemplateLiteral) {
			return fail(base + "." + prop + " name must be a string literal")
		}
		if len(margs) == 2 {
			if _, ok := saCheckTestCallback(margs[1], base+"."+prop, s, pos, refusals); !ok {
				return true, false
			}
		}
		return true, true
	}
	if callee.Kind != ast.KindIdentifier {
		return false, false
	}
	name := callee.Text()
	isHook := saIsTestHookName(scope, name)
	if !saIsTestRegistrationName(scope, name) && !isHook {
		return false, false
	}
	if name == "afterAll" {
		return fail("afterAll is supported inside describe() only (function/top-level buffering not yet)")
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	// 钩子一参（回调）或双参（静态串名 + 回调，名忽略）；注册固定双参；
	// 单参 test/it("name") 即 todo 空过（vitest 无回调即 pending 同形）。
	fnIdx := 1
	if isHook && len(argNodes) == 1 {
		fnIdx = 0
	} else {
		if len(argNodes) == 1 && (name == "test" || name == "it") &&
			argNodes[0] != nil && (argNodes[0].Kind == ast.KindStringLiteral || argNodes[0].Kind == ast.KindNoSubstitutionTemplateLiteral) {
			return true, true
		}
		if len(argNodes) != 2 {
			return fail(name + " takes a name and a callback (2 arguments)")
		}
		if argNodes[0] == nil || (argNodes[0].Kind != ast.KindStringLiteral && argNodes[0].Kind != ast.KindNoSubstitutionTemplateLiteral) {
			return fail(name + " name must be a string literal")
		}
	}
	body, ok := saCheckTestCallback(argNodes[fnIdx], name, s, pos, refusals)
	if !ok {
		return true, false
	}
	if isHook {
		if name == "beforeAll" {
			if !saInlineTestUnit(w, body, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
				return true, false
			}
			return true, true
		}
		if name == "beforeEach" {
			scope.testBefore = append(scope.testBefore, body)
		} else {
			scope.testAfter = append(scope.testAfter, body)
		}
		return true, true
	}
	if name == "describe" {
		// 分组透明：hook 进出按栈存取，体内 test/afterAll 经分区消化递归。
		if !saRunDescribe(w, body, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return true, false
		}
		return true, true
	}
	if !saInlineTestWithHooks(w, body, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
		return true, false
	}
	return true, true
}

// saInlineTestWithHooks 按“前钩+本体+后钩”内联一个测试体（各单元自带块域）。
func saInlineTestWithHooks(w printer.EmitTextWriter, body *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	for _, hb := range scope.testBefore {
		if !saInlineTestUnit(w, hb, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
	}
	if !saInlineTestUnit(w, body, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
		return false
	}
	for _, hb := range scope.testAfter {
		if !saInlineTestUnit(w, hb, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
	}
	return true
}

// saRunDescribe 跑一个 describe 体（hook 进出按栈存取防外泄）。
func saRunDescribe(w printer.EmitTextWriter, body *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	savedBefore, savedAfter := scope.testBefore, scope.testAfter
	ok := saInlineDescribeBody(w, body, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.testBefore, scope.testAfter = savedBefore, savedAfter
	return ok
}

// saOnlyCallBody 解析成员式 `.only` 调用（`test|it|describe.only("name", cb)`；
// 名静态 + 回调验形，不发射；畸形记拒因返 ok=false）。
func saOnlyCallBody(st *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) (body *ast.Node, kind string, ok bool) {
	if st == nil || st.Kind != ast.KindExpressionStatement {
		return nil, "", true
	}
	fail := func(msg string) (*ast.Node, string, bool) {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return nil, "", false
	}
	e := st.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return nil, "", true
	}
	ce := e.AsCallExpression()
	callee := ce.Expression
	if callee == nil || callee.Kind != ast.KindPropertyAccessExpression {
		return nil, "", true
	}
	pa := callee.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Name() == nil {
		return nil, "", true
	}
	base, prop := pa.Expression.Text(), pa.Name().Text()
	if prop != "only" || (base != "test" && base != "it" && base != "describe") {
		return nil, "", true
	}
	if !saIsUnresolvedTestName(scope, base) {
		return nil, "", true
	}
	kind = "test"
	if base == "describe" {
		kind = "describe"
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 2 {
		return fail(base + ".only takes a name and a callback (2 arguments)")
	}
	if argNodes[0] == nil || (argNodes[0].Kind != ast.KindStringLiteral && argNodes[0].Kind != ast.KindNoSubstitutionTemplateLiteral) {
		return fail(base + ".only name must be a string literal")
	}
	bd, good := saCheckTestCallback(argNodes[1], base+".only", st, pos, refusals)
	if !good {
		return nil, "", false
	}
	return bd, kind, true
}

// saAfterAllBody 识别 afterAll 调用语句并取回调体（1 参回调 / 2 参静态名 +
// 回调；非 afterAll 调用返 found=false；畸形记拒因返 found=true 体 nil）。
func saAfterAllBody(s *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) (body *ast.Node, found bool) {
	if s == nil || s.Kind != ast.KindExpressionStatement {
		return nil, false
	}
	e := s.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return nil, false
	}
	ce := e.AsCallExpression()
	callee := ce.Expression
	if callee == nil || callee.Kind != ast.KindIdentifier || callee.Text() != "afterAll" {
		return nil, false
	}
	if !saIsTestHookName(scope, "afterAll") {
		return nil, false
	}
	fail := func(msg string) (*ast.Node, bool) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return nil, true
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	fnIdx := 0
	if len(argNodes) == 2 {
		if argNodes[0] == nil || (argNodes[0].Kind != ast.KindStringLiteral && argNodes[0].Kind != ast.KindNoSubstitutionTemplateLiteral) {
			return fail("afterAll name must be a string literal")
		}
		fnIdx = 1
	} else if len(argNodes) != 1 {
		return fail("afterAll takes a callback (1 argument)")
	}
	bd, ok := saCheckTestCallback(argNodes[fnIdx], "afterAll", s, pos, refusals)
	if !ok {
		return nil, true
	}
	return bd, true
}

// saInlineDescribeBody 内联 describe 体并将其 afterAll 延后（两阶段分区：先体
// 后钩；嵌套 describe/test 经语句分发递归，嵌套 afterAll 由内层分区就地消化）。
// saSkipTestCall 仅验形不发射（only 模式下跳过普通 test/describe 注册：
// 名静态 + 回调验形；体语句一律不降）。
func saSkipTestCall(st *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if st == nil || st.Kind != ast.KindExpressionStatement {
		return true
	}
	e := st.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return true
	}
	ce := e.AsCallExpression()
	callee := ce.Expression
	if callee == nil || callee.Kind != ast.KindIdentifier {
		return true
	}
	name := callee.Text()
	if name != "test" && name != "it" && name != "describe" {
		return true
	}
	if !saIsUnresolvedTestName(scope, name) {
		return true
	}
	fail := func(msg string) bool {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 2 {
		return fail(name + " takes a name and a callback (2 arguments)")
	}
	if argNodes[0] == nil || (argNodes[0].Kind != ast.KindStringLiteral && argNodes[0].Kind != ast.KindNoSubstitutionTemplateLiteral) {
		return fail(name + " name must be a string literal")
	}
	if _, ok := saCheckTestCallback(argNodes[1], name, st, pos, refusals); !ok {
		return false
	}
	return true
}

func saInlineDescribeBody(w printer.EmitTextWriter, body *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	if body.Kind != ast.KindBlock {
		return saInlineTestUnit(w, body, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	}
	stmts, ok := saBlockStmts(body)
	if !ok {
		return false
	}
	ok, _ = saLowerBufferedScope(w, stmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	return ok
}

// saOnlyMark 识别直接子语句中的 `test|it|describe.only("name", cb)`（基名须
// 未解析；返回族名 test/describe，it 归 test；其余返空；畸形由 lowering 侧按
// 同形大声拒，不在此判）。
func saOnlyMark(st *ast.Node, scope *saScope) string {
	if st == nil || st.Kind != ast.KindExpressionStatement {
		return ""
	}
	e := st.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return ""
	}
	ce := e.AsCallExpression()
	callee := ce.Expression
	if callee == nil || callee.Kind != ast.KindPropertyAccessExpression {
		return ""
	}
	pa := callee.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Name() == nil {
		return ""
	}
	base, prop := pa.Expression.Text(), pa.Name().Text()
	if prop != "only" {
		return ""
	}
	if base != "test" && base != "it" && base != "describe" {
		return ""
	}
	if !saIsUnresolvedTestName(scope, base) {
		return ""
	}
	if base == "describe" {
		return "describe"
	}
	return "test"
}

// saIsAfterAllCall 纯判定直接子语句是否为未解析 afterAll 调用（无发射无记拒；
// 畸形亦命中，交 lowering 侧同形大声拒）。
func saIsAfterAllCall(st *ast.Node, scope *saScope) bool {
	if st == nil || st.Kind != ast.KindExpressionStatement {
		return false
	}
	e := st.AsExpressionStatement().Expression
	if e == nil || e.Kind != ast.KindCallExpression {
		return false
	}
	ce := e.AsCallExpression()
	callee := ce.Expression
	if callee == nil || callee.Kind != ast.KindIdentifier || callee.Text() != "afterAll" {
		return false
	}
	return saIsTestHookName(scope, "afterAll")
}

// saScopeNeedsTestBuffer 预扫语句表是否需测试缓冲（直接子含未解析 afterAll
// 或 .only 注册；纯判定无发射；命中者走缓冲核，否则沿旧路字节一致）。
func saScopeNeedsTestBuffer(stmts []*ast.Node, scope *saScope) bool {
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindExpressionStatement {
			continue
		}
		if saIsAfterAllCall(st, scope) {
			return true
		}
		if saOnlyMark(st, scope) != "" {
			return true
		}
	}
	return false
}

// saLowerBufferedScope 带测试缓冲的语句表 lowering（afterAll 延后 + .only
// 分区；无触发时与直驱循环字节一致；调用方（arm/合成/回调内联）预扫命中才委托）。
// 返回（成功，终结）：终结供合成循环补 ret 判定。
func saLowerBufferedScope(w printer.EmitTextWriter, stmts []*ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	onlyMode := false
	for _, st := range stmts {
		if st != nil && saOnlyMark(st, scope) != "" {
			onlyMode = true
			break
		}
	}
	var afters []*ast.Node
	saved := saScopeEnter(scope)
	terminated, armOK := false, true
	lowerOne := func(st *ast.Node) {
		if terminated || !armOK {
			return
		}
		if ab, found := saAfterAllBody(st, scope, pos, refusals); found {
			if ab == nil {
				armOK = false
				return
			}
			afters = append(afters, ab)
			return
		}
		if onlyMode {
			if saOnlyMark(st, scope) != "" {
				ob, kind, good := saOnlyCallBody(st, scope, pos, refusals)
				if !good {
					armOK = false
					return
				}
				if kind == "describe" {
					if !saRunDescribe(w, ob, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
						armOK = false
					}
				} else if !saInlineTestWithHooks(w, ob, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
					armOK = false
				}
				return
			}
			if st != nil && st.Kind == ast.KindExpressionStatement {
				if e := st.AsExpressionStatement().Expression; e != nil && e.Kind == ast.KindCallExpression {
					if ce := e.AsCallExpression(); ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier {
						if nm := ce.Expression.Text(); nm == "test" || nm == "it" || nm == "describe" {
							if !saSkipTestCall(st, scope, pos, refusals) {
								armOK = false
							}
							return
						}
					}
				}
			}
		}
		done, failed := saLowerStmt(w, st, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if failed {
			armOK = false
			return
		}
		if done {
			terminated = true
		}
	}
	for _, st := range stmts {
		if st == nil {
			continue
		}
		lowerOne(st)
		if !armOK {
			break
		}
	}
	saReleaseDeeperThan(w, scope, saved.owned)
	saScopeExit(scope, saved)
	if !armOK {
		return false, terminated
	}
	for _, ab := range afters {
		if !saInlineTestUnit(w, ab, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, terminated
		}
	}
	return true, terminated
}

// saIsPureValueOp 报告二元操作符是否为纯值运算（语句位求值丢弃安全集：
// 算术/位运算/比较/`&& ||` 指令形/`**`；`??`/逗号/`in`/赋值族沿旧门大声拒）。
func saIsPureValueOp(op ast.Kind) bool {
	switch op {
	case ast.KindPlusToken, ast.KindMinusToken,
		ast.KindAsteriskToken, ast.KindSlashToken,
		ast.KindPercentToken, ast.KindAsteriskAsteriskToken,
		ast.KindLessThanLessThanToken,
		ast.KindGreaterThanGreaterThanToken,
		ast.KindGreaterThanGreaterThanGreaterThanToken,
		ast.KindAmpersandToken, ast.KindBarToken,
		ast.KindCaretToken,
		ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken,
		ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken,
		ast.KindLessThanToken, ast.KindLessThanEqualsToken,
		ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken,
		ast.KindAmpersandAmpersandToken, ast.KindBarBarToken:
		return true
	}
	return false
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
		op, _, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported call statement: " + msg})
			return false
		}
		// 语句位丢弃结果即释（H-try：try 体调用结果此前漏释，@main 尾 MemoryLeak；
		// 上游语句位统一 `!t` 同形。数组/串/date 沿既有三门，余下 i32/用户调用
		// 收归一口；saReleaseStmtTemp 记名走归属口、已释/已耗跳过，未记名直释）。
		saReleaseStmtTemp(w, scope, op)
		return true
	}
	if e.Kind != ast.KindBinaryExpression {
		// 裸 `new C();` 语句：构造照常（副作用保留）+ 句柄即释丢弃
		// （上游 jsDocPrivateConstructor.sai 同形 `alloc` + `!`；构造核
		// 复用声明绑定 saLowerNewClass，释放走既有归属口）。
		if e.Kind == ast.KindNewExpression {
			if h, msg := saLowerNewStmt(w, e, scope, pos, refusals, nextTemp); msg != "" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported new statement: " + msg})
				return false
			} else {
				saReleaseOwnedTemp(w, scope, h)
				return true
			}
		}
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
	if saBinaryOpKind(be) == ast.KindAsteriskAsteriskEqualsToken {
		return saLowerPowAssign(w, be, scope, pos, refusals, nextTemp, s)
	}
	if be.OperatorToken == nil || (be.OperatorToken.Kind != ast.KindEqualsToken && !saIsPureValueOp(saBinaryOpKind(be))) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement (plain x = i32 only)"})
		return false
	}
	if be.OperatorToken.Kind != ast.KindEqualsToken {
		// 纯值二元表达式语句求值后丢弃（`1 + 2;`；与非二元臂同形，封存 lowerExprStatement:2712-2715；
		// 上游 simpleTest.sai 同形 `t_1 = add 1, 2` 直接丢弃）。
		if _, msg := saEvalI32(w, e, scope, pos, refusals, nextTemp); msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement: " + msg})
			return false
		}
		return true
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
		// 数组句柄重绑（H16：直行/循环重绑缺先释皆陷 RegisterRedefinition；
		// 与串分支同律：temp 源先释+consume/复位，具名源上游原句拒）。
		if src, msg := saArrValueOf(w, be.Right, scope, pos, refusals, nextTemp); msg == "" {
			if _, named := scope.types[src]; named && !saIsTempOp(src) {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "handle copies need an explicit clone (pass the handle directly)"})
				return false
			}
			saRebindRelease(w, scope, name)
			w.Write(fmt.Sprintf("  %s = %s\n", name, src))
			saConsumeOwn(scope, src)
			if b := saOwnOf(scope, name); b != nil {
				b.heap = true
			}
			saMarkRebound(scope, name)
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
		if _, named := scope.types[h]; named && !saIsTempOp(h) {
			// 具名串柄直授即别名，上游同形大声拒（H13 同源；禁别名双释）。
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "handle copies need an explicit clone (pass the handle directly)"})
			return false
		}
		// 串重绑先释旧柄（H13；新柄 consume+复位防双释）。
		saRebindRelease(w, scope, name)
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		saConsumeOwn(scope, h)
		if b := saOwnOf(scope, name); b != nil {
			b.heap = true
		}
		saMarkRebound(scope, name)
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
	// 语句赋值右值记种检查（串/实例句柄禁入 i32/bool 位；str/实例/数组目标沿上分支；铁律 4）。
	if msg := saCheckI32Value(scope, op); msg != "" {
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
	condOp, msg := saCondOperandMat(w, ws.Expression, scope, pos, refusals, nextTemp)
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
		} else if scope.types[name] != "i32" && scope.types[name] != "f64" {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to non-i32 variable " + name})
			return false
		}
		if scope.types[name] == "f64" {
			// f64 计数器初值直写（语句位 f64 分支同形；整数字面量文本直绑，
			// 上游 `i = 0` 同形；plain 永不归属，无释放）。
			op, msg := saEvalF64(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(init.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			return true
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
			// f64 计数器增量直写（语句位 f64 分支同形；`i = i + 1.0` 经
			// saEvalF64 得 fadd，整/小数量化皆 f64 世界自洽，条件位既有
			// fcmp 通路配套；plain 永不归属，无释放）。
			if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
				if k, ok := scope.types[be.Left.Text()]; ok && k == "f64" {
					r, msg := saEvalF64(w, be.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						ln, col := pos(incr.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
						return false
					}
					w.Write(fmt.Sprintf("  %s = %s\n", be.Left.Text(), r))
					return true
				}
			}
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
			saStoreLocal(w, target, r, scope, nextTemp)
			return true
		}
		if saBinaryOpKind(be) == ast.KindAsteriskAsteriskEqualsToken {
			// 增量位 `x **= e`（P-ppow 增量位：目标限本地 i32 绑定，
			// 经 pow 核 + saStoreLocal 纪律；余下沿旧门）。
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
			if msg := saCheckI32Value(scope, r); msg != "" {
				ln, col := pos(incr.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
				return false
			}
			res := saLowerPowOps(w, target, r, scope.nextLabel, nextTemp)
			saStoreLocal(w, target, res, scope, nextTemp)
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
		saStoreLocal(w, target, t, scope, nextTemp)
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
		condOp, msg := saCondOperandMat(w, fs.Condition, scope, pos, refusals, nextTemp)
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
	condOp, msg := saCondOperandMat(w, ds.Expression, scope, pos, refusals, nextTemp)
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

// saEvalSwitchVal 求 switch 判别/case 值（i32 优先、败则串沿旧门、再败则
// bool 标识直读/字面量折 1/0；bool 通道恒 0/1，eq 分发 sound，混合臂恒假落
// default 与既有串/i32 混合 rule 同形；调用返 bool 等非直读形沿旧门）。
func saEvalSwitchVal(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	v, imsg := saEvalI32(w, e, scope, pos, refusals, nextTemp)
	if imsg == "" {
		return v, ""
	}
	if h, smsg := saEvalStr(w, e, scope, pos, refusals, nextTemp); smsg == "" {
		return h, ""
	}
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "bool" {
			return e.Text(), ""
		}
	}
	if e != nil && e.Kind == ast.KindTrueKeyword {
		return "1", ""
	}
	if e != nil && e.Kind == ast.KindFalseKeyword {
		return "0", ""
	}
	return "", imsg
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
	// discriminant i32 优先、败则串、再败则 bool（与 case 值同门 saEvalSwitchVal）。
	disc, msg := saEvalSwitchVal(w, sw.Expression, scope, pos, refusals, nextTemp)
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
	dispatch := saSwitchDispatch(s, parts, bodyLabels, testLabels[len(parts)], endL)
	for i, p := range parts {
		w.Write(fmt.Sprintf("%s:\n", testLabels[i]))
		val, vmsg := saEvalSwitchVal(w, p.node.AsCaseOrDefaultClause().Expression, scope, pos, refusals, nextTemp)
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
		stmts := p.node.AsCaseOrDefaultClause().Statements.Nodes
		w.Write(fmt.Sprintf("%s:\n", bodyLabels[i]))
		if len(stmts) == 0 {
			// 空臂直通源码序下子句（dispatch 预解链目标；宏路同形）。
			w.Write(fmt.Sprintf("  jmp %s\n", dispatch[i]))
			continue
		}
		savedArmRelease := scope.armRelease
		scope.armRelease = true
		armOK := saLowerArm(w, stmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedArmRelease
		if !armOK {
			lowered = false
			break
		}
		if !saArmTerminates(stmts) {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		}
	}
	if !lowered {
		scope.loops = scope.loops[:len(scope.loops)-1]
		return false
	}
	w.Write(fmt.Sprintf("%s:\n", testLabels[len(parts)]))
	if defaultNode != nil {
		savedArmRelease := scope.armRelease
		scope.armRelease = true
		armOK := saLowerArm(w, defaultNode.AsCaseOrDefaultClause().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedArmRelease
		if !armOK {
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

// saSwitchDispatch 解空臂源码序链目标（堆叠 case 标签 JS fallthrough：
// 空臂→紧随下子句体标（default 即 defaultL），末空臂无 default 即 endL；
// 非空臂恒自体标。调用方空体发 `jmp` 链（宏按标号展开，标复用即重定义，
// 故须独立标号+jmp）；上游空臂落 endswitch 致 f(1)=0 错译，本仓正确优先。
func saSwitchDispatch(s *ast.Node, parts []saCasePart, bodyLabels []string, defaultL, endL string) []string {
	dispatch := make([]string, len(parts))
	for i := range parts {
		dispatch[i] = bodyLabels[i]
	}
	if s == nil {
		return dispatch
	}
	sw := s.AsSwitchStatement()
	if sw == nil || sw.CaseBlock == nil {
		return dispatch
	}
	clauses := sw.CaseBlock.AsCaseBlock().Clauses.Nodes
	partIndex := make(map[*ast.Node]int, len(parts))
	for i, p := range parts {
		partIndex[p.node] = i
	}
	next := endL
	for k := len(clauses) - 1; k >= 0; k-- {
		cl := clauses[k]
		if cl == nil {
			continue
		}
		if cl.Kind == ast.KindDefaultClause {
			next = defaultL
			continue
		}
		j, ok := partIndex[cl]
		if !ok {
			continue
		}
		if len(cl.AsCaseOrDefaultClause().Statements.Nodes) == 0 {
			dispatch[j] = next
		} else {
			next = bodyLabels[j]
		}
	}
	return dispatch
}

// saLowerSwitchMacro lowering 2/3 臂 switch（上游 SWITCH_2/3 分发宏；
// 体/break/default/域/终结纪律镜 legacy，唯 test 链（eq+br）入宏；
// 无 default 时宏 default 臂落空到 end；形状证据：封存 tryLowerSwitchMacro:2502-2588）。
func saLowerSwitchMacro(w printer.EmitTextWriter, s *ast.Node, disc string, parts []saCasePart, defaultNode *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = s
	// case 值前置求值（legacy 与体交错，运行时序由标号固定；两形各求值一次）。
	vals := make([]string, len(parts))
	for i, p := range parts {
		val, vmsg := saEvalSwitchVal(w, p.node.AsCaseOrDefaultClause().Expression, scope, pos, refusals, nextTemp)
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
	scope.loops = append(scope.loops, saLoop{end: endL, depth: len(scope.ownOrder)})
	saBindPendingLabels(scope, true)
	needImport("sa_std/control.sal")
	dispatch := saSwitchDispatch(s, parts, bodyLabels, defaultL, endL)
	if len(parts) == 2 {
		w.Write(fmt.Sprintf("  EXPAND SWITCH_2 %s, %s, %s, %s, %s, %s\n", disc, vals[0], bodyLabels[0], vals[1], bodyLabels[1], defaultL))
	} else {
		w.Write(fmt.Sprintf("  EXPAND SWITCH_3 %s, %s, %s, %s, %s, %s, %s, %s\n", disc, vals[0], bodyLabels[0], vals[1], bodyLabels[1], vals[2], bodyLabels[2], defaultL))
	}
	lowerBody := func(stmts []*ast.Node) bool {
		savedArmRelease := scope.armRelease
		scope.armRelease = true
		armOK := saLowerArm(w, stmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedArmRelease
		if !armOK {
			return false
		}
		if !saArmTerminates(stmts) {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		}
		return true
	}
	lowered := true
	for i, p := range parts {
		stmts := p.node.AsCaseOrDefaultClause().Statements.Nodes
		w.Write(fmt.Sprintf("%s:\n", bodyLabels[i]))
		if len(stmts) == 0 {
			// 空臂直通源码序下子句（dispatch 预解链目标；宏按标号展
			// 开故须独立标号，复用即重定义；上游空臂落 endswitch 致
			// f(1)=0 错译，本仓正确优先）。
			w.Write(fmt.Sprintf("  jmp %s\n", dispatch[i]))
			continue
		}
		if !lowerBody(stmts) {
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
			savedArmRelease := scope.armRelease
			scope.armRelease = true
			armOK := saLowerArm(w, ts.FinallyBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
			scope.armRelease = savedArmRelease
			if !armOK {
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
	savedArmRelease := scope.armRelease
	scope.armRelease = true
	catchOK := saLowerArm(w, catchStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.armRelease = savedArmRelease
	if !catchOK {
		return false, true
	}
	catchTerm := saArmTerminates(catchStmts)
	if ts.FinallyBlock != nil {
		finStmts := ts.FinallyBlock.AsBlock().Statements.Nodes
		savedFinArmRelease := scope.armRelease
		scope.armRelease = true
		finOK := saLowerArm(w, finStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedFinArmRelease
		if !finOK {
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
	if ts.TryBlock != nil && saContainsThrow(ts.TryBlock) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "throw inside try is not lowerable (catch cannot resume after panic)"})
		return false, true
	}
	if ts.TryBlock != nil {
		savedArmRelease := scope.armRelease
		scope.armRelease = true
		tryOK := saLowerArm(w, ts.TryBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedArmRelease
		if !tryOK {
			return false, true
		}
	}
	// catch 永不可达（无 throw）：整块跳过，不绑定。
	if ts.FinallyBlock != nil {
		savedFinArmRelease := scope.armRelease
		scope.armRelease = true
		finOK := saLowerArm(w, ts.FinallyBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedFinArmRelease
		if !finOK {
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
		// 变量/表达式语句标签恒无用（break/continue 只能进循环/块/标号块，
		// 此处标签不可被引用）：擦标签直降内句。
		if inner.Kind == ast.KindVariableStatement || inner.Kind == ast.KindExpressionStatement {
			done, failed := saLowerStmt(w, inner, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
			if failed {
				return false, true
			}
			return done, false
		}
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
	condOp, msg := saCondOperandMat(w, iv.Expression, scope, pos, refusals, nextTemp)
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
		savedArmRelease := scope.armRelease
		scope.armRelease = true
		thenOK := saLowerArm(w, thenStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedArmRelease
		if !thenOK {
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
		savedElseArmRelease := scope.armRelease
		scope.armRelease = true
		elseOK := saLowerArm(w, elseStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.armRelease = savedElseArmRelease
		if !elseOK {
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
	savedArmRelease := scope.armRelease
	scope.armRelease = true
	thenOK := saLowerArm(w, thenStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.armRelease = savedArmRelease
	if !thenOK {
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
		// 臂皆终结则整体终结；无 default 可落空。空臂（堆叠 case 标签）
		// 直通源码序下子句，其终结态即链目标终结态（发射侧
		// saSwitchDispatch 同形）；末空臂无 default 即落空 end。
		sw := s.AsSwitchStatement()
		if sw.CaseBlock == nil {
			return false
		}
		clauses := sw.CaseBlock.AsCaseBlock().Clauses.Nodes
		hasDefault := false
		for _, cl := range clauses {
			if cl.Kind == ast.KindDefaultClause {
				hasDefault = true
			} else if cl.Kind != ast.KindCaseClause {
				return false
			}
		}
		if !hasDefault {
			return false
		}
		// 自后向前源码序链：空臂终结态 = 紧随下子句终结态；末空臂无
		// default 即落空 end（nextTerms 初 false）；default 体须自终结。
		nextTerms := false
		for i := len(clauses) - 1; i >= 0; i-- {
			cl := clauses[i]
			if cl.Kind == ast.KindDefaultClause {
				nextTerms = saArmTerminates(cl.AsCaseOrDefaultClause().Statements.Nodes)
				if !nextTerms {
					return false
				}
				continue
			}
			stmts := cl.AsCaseOrDefaultClause().Statements.Nodes
			if len(stmts) == 0 {
				if !nextTerms {
					return false
				}
				continue
			}
			nextTerms = saArmTerminates(stmts)
			if !nextTerms {
				return false
			}
		}
		return true
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
	// 终结后皆不可达（落字侧 saLowerArm 同律抑制死码）：任一句终结即臂终结；
	// 只看末句会误判 `[return; dead]` 致终结符后多发 jmp 触 verifier。
	for _, s := range stmts {
		if saStmtTerminates(s) {
			return true
		}
	}
	return false
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
	if b := saOwnOf(scope, dst); b != nil && !b.consumed && !b.released {
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

// saConsumeTemp 记录 temp 源 move（H-try 深修：裸 `dst = t` 即 move，
// 未登记 temp 的 move 在 Go 侧本无记录，致语句位释放误释已 move 值
// （168 `d = t_2` 后 `!t_2` 陷 UseAfterMove）。已登记走归属口；未登记
// 记 consumed（heap 置 false：纯标量 move，无堆可释；drain/释放口径
// 全跳过，与 verifier 一致）。temps 永不复用，无复登记冲掉之虞。
func saConsumeTemp(scope *saScope, name string) {
	if !saIsTempOp(name) {
		return
	}
	if b := saOwnOf(scope, name); b != nil {
		if b.heap && !b.released {
			b.consumed = true
		}
		return
	}
	if scope.ownState == nil {
		scope.ownState = map[string]*saOwn{}
	}
	scope.ownState[name] = &saOwn{consumed: true}
	scope.ownOrder = append(scope.ownOrder, name)
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
			saConsumeTemp(scope, src)
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
		saConsumeTemp(scope, src)
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

// saReleaseStmtTemp 语句位丢弃新柄即释（记名走归属口，已释跳过防双释；
// 未记名 temp 直释；具名不动）。
func saReleaseStmtTemp(w printer.EmitTextWriter, scope *saScope, op string) {
	if !saIsTempOp(op) {
		return
	}
	if saOwnOf(scope, op) != nil {
		saReleaseOwnedTemp(w, scope, op)
		return
	}
	w.Write(fmt.Sprintf("  !%s\n", op))
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
