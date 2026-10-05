// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
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
		if ref == nil || ref.TypeName == nil || ref.TypeName.Text() != "Array" {
			return false
		}
		if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 1 {
			return false
		}
		return ref.TypeArguments.Nodes[0].Kind == ast.KindStringKeyword
	}
	return false
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
	v, msg := saEvalI32(w, el, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	return v, ""
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
		for _, el := range al.Elements.Nodes {
			if el.Kind == ast.KindSpreadElement {
				sv, msg := saArrValueOf(w, el.AsSpreadElement().Expression.AsNode(), scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				saAppendSlice(w, h, sv, scope, nextTemp)
				continue
			}
			v, msg := saArrayLiteralElem(w, el, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			saLowerArrayPush(w, h, v, scope, nextTemp)
		}
		saOwnTemp(scope, h)
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

// saLowerCheckedIndex lowering 越界归零下标读（形状证据：封存
// lowerCheckedIndex:8522-8567：alloc 8 join 槽 + len/ult 检查 + data/mul/add
// 取址 + i32 读回；OOB 得 0；槽 ownTemp + 读后 releaseIfOwnedTemp 同形）。
// saLowerCheckedIndex lowering 越界归零下标读（形状证据：封存
// lowerCheckedIndex:8522-8567：alloc 8 join 槽 + len/ult 检查 + data/mul/add
// 取址 + i32 读回；OOB 得 0；槽 ownTemp + 读后 releaseIfOwnedTemp 同形）。
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

// saLowerIndexLoadExpr lowering 下标读表达式 `a[i]`/`a?.[i]`（基为绑定数组或数组值调用；
// `?.` 空基归零；下标走 i32 求值，读回走越界归零 join）。
func saLowerIndexLoadExpr(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	isOpt := ea.QuestionDotToken != nil
	// Map 种基走 get 脱糖（`m[k]` ≡ `m.get(k)`；Set 无键值，大声拒）。
	if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
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
	if isOpt {
		// `a?.[i]` (null base reads 0, otherwise checked-index join).
		return saLowerOptionalIndex(w, base, idx, scope.nextLabel, nextTemp), ""
	}
	return saLowerCheckedIndex(w, base, idx, scope.nextLabel, nextTemp), ""
}

// saLowerLengthExpr lowering `.length`（数组/字符串头 +8 u64；其余成员拒）。
func saLowerLengthExpr(w printer.EmitTextWriter, pa *ast.PropertyAccessExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	_ = refusals
	if pa.QuestionDotToken != nil {
		return "", "optional member access not lowerable"
	}
	if nm := pa.Name(); nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() != "length" {
		return "", "only .length member access lowerable"
	}
	if base, ok := saArrBase(scope, pa.Expression); ok {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, base))
		return t, ""
	}
	// 链式下标基（`pairs[0].length` 经句柄总线递归求内层句柄；与读位同形）。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindElementAccessExpression {
		if h, msg := saArrValueOf(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
			return t, ""
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
	if vd.Initializer.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported array literal: " + msg})
			return false
		}
		arr = h
	} else if base, ok := saArrBase(scope, vd.Initializer); ok {
		arr = base
	} else {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring source must be bound array"})
		return false
	}
	idx := 0
	for _, el := range pat.AsBindingPattern().Elements.Nodes {
		if el.Kind != ast.KindBindingElement {
			idx++
			continue
		}
		be := el.AsBindingElement()
		if be.DotDotDotToken != nil {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest elements in destructuring are not lowerable"})
			return false
		}
		nm := be.Name()
		if nm == nil {
			idx++
			continue
		}
		if nm.Kind != ast.KindIdentifier {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested destructuring shape is not lowerable"})
			return false
		}
		name := nm.Text()
		if _, dup := scope.types[name]; dup {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		v := saLowerCheckedIndex(w, arr, fmt.Sprintf("%d", idx), scope.nextLabel, nextTemp)
		w.Write(fmt.Sprintf("  %s = %s\n", name, v))
		scope.types[name] = "i32"
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
	if def == nil && vd.Initializer != nil && vd.Initializer.Kind == ast.KindObjectLiteralExpression {
		// 字面量源现场具化（无注解按键集匹配；具名注解须同名，泛型优先
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
	for _, el := range pat.AsBindingPattern().Elements.Nodes {
		if el.Kind != ast.KindBindingElement {
			continue
		}
		be := el.AsBindingElement()
		if be.DotDotDotToken != nil {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest elements in destructuring are not lowerable"})
			return false
		}
		if be.Initializer != nil {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring defaults are not lowerable"})
			return false
		}
		nm := be.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested destructuring shape is not lowerable"})
			return false
		}
		field := nm.Text()
		if be.PropertyName != nil {
			pn := be.PropertyName.AsNode()
			if pn.Kind != ast.KindIdentifier && pn.Kind != ast.KindStringLiteral {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed destructuring keys are not lowerable"})
				return false
			}
			field = pn.Text()
		}
		off, ok := def.offsets[field]
		if !ok {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + field + " is not in the " + def.name + " layout"})
			return false
		}
		name := nm.Text()
		if _, dup := scope.types[name]; dup {
			ln, col := pos(el.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		if def.fkinds[field] == "str" {
			w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", name, hid, off))
			scope.types[name] = "str"
			continue
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
		} else {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array declaration needs initializer"})
		}
		return false
	}
	if vd.Initializer.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported array literal: " + msg})
			return false
		}
		// 显式 i32 元注解收串元拒（4 字节槽截断句柄；串注解/无注解沿既有口径；
		// 按语法种判定；铁律 4）。
		if vd.Type != nil && !saIsStringArrayAnnot(vd.Type) &&
			(vd.Type.Kind == ast.KindArrayType || (vd.Type.Kind == ast.KindTypeReference && vd.Type.AsTypeReferenceNode() != nil)) {
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
		return true
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
func saForArrHandle(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node, what string) (string, bool) {
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
	// 链基（`pairs[0]`/`q.r.a` 经句柄总线；失败静默下探旧门）。
	if e != nil && (e.Kind == ast.KindElementAccessExpression || e.Kind == ast.KindPropertyAccessExpression) {
		if h, msg := saArrValueOf(w, e, scope, pos, refusals, nextTemp); msg == "" {
			return h, true
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
	if fo.AwaitModifier != nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "for-await not lowerable"})
		return false
	}
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
	arrVal, ok := saForArrHandle(w, fo.Expression, scope, pos, refusals, nextTemp, s, "for-of")
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
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL, depth: len(scope.ownOrder)})
	saBindPendingLabels(scope, false)
	w.Write(fmt.Sprintf("%s:\n", topL))
	cT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cT, idx, lenT))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	baseT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	offT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	elemPtr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	elemT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", baseT, arrVal))
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", offT, idx))
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", elemPtr, baseT, offT))
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", elemT, elemPtr))
	if pat != nil {
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
			}
		}
		scope.types[binding] = bindKind
	}
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
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
	arrVal, ok := saForArrHandle(w, fo.Expression, scope, pos, refusals, nextTemp, s, "for-in")
	if !ok {
		return false
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
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
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
		"toSorted", "with", "toSpliced", "concat",
		"forEach", "map", "filter", "find", "findIndex", "findLast", "findLastIndex",
		"some", "every", "reduce", "reduceRight":
		return true
	}
	return false
}

// saIsHigherOrderMethod 回调形数组方法（封存 isHigherOrderMethod:4863-4870）。
func saIsHigherOrderMethod(m string) bool {
	switch m {
	case "forEach", "map", "filter", "find", "findIndex", "findLast", "findLastIndex",
		"some", "every", "reduce", "reduceRight", "sort", "toSorted":
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
	case ast.KindIdentifier:
		k, ok := scope.types[e.Text()]
		return ok && k == "arr"
	case ast.KindCallExpression:
		k, ok := saArrCallRet(e.AsCallExpression(), scope)
		return ok && k == "arr"
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
	if !saIsArrMethod(m) {
		return "", false
	}
	if !saIsArrValue(pa.Expression, scope) {
		return "", false
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
			h, def, msg := saInstBase(pa.Expression, scope)
			// map 索引实例基（`m[k].a`；saInstBase 只认标识符/this；
			// 与 i32 读位 2450-2459 同形；否则 nil 解引用崩溃）。
			if msg == "" && def == nil && pa.Expression != nil && (pa.Expression.Kind == ast.KindElementAccessExpression || pa.Expression.Kind == ast.KindCallExpression) {
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
		return ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "Array"
	}
	if e.Kind == ast.KindNewExpression {
		ne := e.AsNewExpression()
		return ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "Array"
	}
	return false
}

// saLowerArrayCtor 数组构造式具化（单长分配；多元逐元 push；i32  plain 值；
// 形状证据：封存 lowerCall Array:3871-3884 + newSizedArray）。
func saLowerArrayCtor(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	var argNodes []*ast.Node
	isNew := false
	if e.Kind == ast.KindCallExpression {
		if ce := e.AsCallExpression(); ce.Arguments != nil {
			argNodes = ce.Arguments.Nodes
		}
	} else {
		isNew = true
		if ne := e.AsNewExpression(); ne.Arguments != nil {
			argNodes = ne.Arguments.Nodes
		}
	}
	if isNew && len(argNodes) != 1 {
		return "", "new Array takes 1 length argument"
	}
	if len(argNodes) == 1 {
		// 单参恒为长（`Array(5)` 即长 5；元素式请用字面量；
		// 形状证据：封存调用式 newSizedArray 分支 + lowerNew:8603）。
		v, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
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
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", h))
	w.Write(fmt.Sprintf("  store %s + 8, 0 as u64\n", h))
	return h
}

// saAppendSlice 整片拷贝入目标（push 循环；形状证据：封存 appendSlice:7820-7847）。
func saAppendSlice(w printer.EmitTextWriter, dst, src string, scope *saScope, nextTemp *int) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, src))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, src))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_ap_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_ap_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_ap_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, i))
	addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	elem := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", elem, addr))
	saLowerArrayPush(w, dst, elem, scope, nextTemp)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
}

// saCopyRange 拷贝 src[s0,s1) 到 dst[d0,.]（形状证据：封存 copyRange:7026-7057）。
func saCopyRange(w printer.EmitTextWriter, sdata, ddata, s0, s1, d0 string, scope *saScope, nextTemp *int) {
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", i, s0))
	topL := fmt.Sprintf("L_cr_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_cr_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_cr_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, s1))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	rel := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", rel, i, s0))
	so := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", so, i))
	sa := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", sa, sdata, so))
	cv := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", cv, sa))
	di := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", di, d0, rel))
	dof := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", dof, di))
	da := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", da, ddata, dof))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", da, cv))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
}

// saArrayClampLen 下标相对 len 钳到 [0, len]（负值自末端起；形状证据：
// 封存 arrayClampLen:6401-6440）。
func saArrayClampLen(w printer.EmitTextWriter, v, ln string, scope *saScope, nextTemp *int) string {
	adj := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	nL := fmt.Sprintf("L_cx_neg_%d", *scope.nextLabel)
	*scope.nextLabel++
	nN := fmt.Sprintf("L_cx_nneg_%d", *scope.nextLabel)
	*scope.nextLabel++
	nE := fmt.Sprintf("L_cx_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	neg := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, 0\n", neg, v))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", neg, nL, nN))
	w.Write(fmt.Sprintf("%s:\n", nL))
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", adj, ln, v))
	w.Write(fmt.Sprintf("  jmp %s\n", nE))
	w.Write(fmt.Sprintf("%s:\n", nN))
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", adj, v))
	w.Write(fmt.Sprintf("  jmp %s\n", nE))
	w.Write(fmt.Sprintf("%s:\n", nE))
	lo := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	loT := fmt.Sprintf("L_cx_lot_%d", *scope.nextLabel)
	*scope.nextLabel++
	loF := fmt.Sprintf("L_cx_lof_%d", *scope.nextLabel)
	*scope.nextLabel++
	loE := fmt.Sprintf("L_cx_loe_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = slt %s, 0\n", lo, adj))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", lo, loT, loF))
	w.Write(fmt.Sprintf("%s:\n", loT))
	w.Write(fmt.Sprintf("  %s = 0\n", out))
	w.Write(fmt.Sprintf("  jmp %s\n", loE))
	w.Write(fmt.Sprintf("%s:\n", loF))
	hi := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	hiT := fmt.Sprintf("L_cx_hit_%d", *scope.nextLabel)
	*scope.nextLabel++
	hiF := fmt.Sprintf("L_cx_hif_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", hi, adj, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", hi, hiT, hiF))
	w.Write(fmt.Sprintf("%s:\n", hiT))
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, ln))
	w.Write(fmt.Sprintf("  jmp %s\n", loE))
	w.Write(fmt.Sprintf("%s:\n", hiF))
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, adj))
	w.Write(fmt.Sprintf("  jmp %s\n", loE))
	w.Write(fmt.Sprintf("%s:\n", loE))
	return out
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
	if kind == "snap" {
		cp := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", cp, src))
		return cp
	}
	fresh := func() string {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		return t
	}
	freshL := func(p string) string {
		l := fmt.Sprintf("L_dc_%s_%d", p, *scope.nextLabel)
		*scope.nextLabel++
		return l
	}
	ln := fresh()
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, src))
	sdata := fresh()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", sdata, src))
	dest := fresh()
	w.Write(fmt.Sprintf("  %s = alloc 16\n", dest))
	ln1 := fresh()
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", ln1, ln))
	nby := fresh()
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", nby, ln1))
	ddata := fresh()
	w.Write(fmt.Sprintf("  %s = alloc %s\n", ddata, nby))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", dest, ddata))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", dest, ln))
	w.Write(fmt.Sprintf("  !%s\n", ddata))
	dloop := fresh()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dloop, dest))
	iv := fresh()
	w.Write(fmt.Sprintf("  %s = 0\n", iv))
	topL, bodyL, endL := freshL("top"), freshL("body"), freshL("end")
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fresh()
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, iv, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	so := fresh()
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", so, iv))
	saddr := fresh()
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", saddr, sdata, so))
	daddr := fresh()
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", daddr, dloop, so))
	if kind == "deep" {
		inner := fresh()
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", inner, saddr))
		inew := saLowerDeepCloneInner(w, inner, "flat", false, scope, nextTemp)
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", daddr, inew))
	} else {
		cv := fresh()
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", cv, saddr))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", daddr, cv))
	}
	inext := fresh()
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, iv))
	w.Write(fmt.Sprintf("  %s = %s\n", iv, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	if takeOwn {
		saOwnTemp(scope, dest)
	}
	if kind == "deep" {
		if scope.arrNest == nil {
			scope.arrNest = map[string]bool{}
		}
		scope.arrNest[dest] = true
	}
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
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, arr))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, arr))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", nlen, ln))
	nbytes := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", nbytes, nlen))
	ndata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	allocOk := fmt.Sprintf("L_push_alloc_%d", *scope.nextLabel)
	*scope.nextLabel++
	allocEmpty := fmt.Sprintf("L_push_empty_%d", *scope.nextLabel)
	*scope.nextLabel++
	allocDone := fmt.Sprintf("L_push_done_%d", *scope.nextLabel)
	*scope.nextLabel++
	isempty := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isempty, nbytes))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isempty, allocEmpty, allocOk))
	w.Write(fmt.Sprintf("%s:\n", allocEmpty))
	w.Write(fmt.Sprintf("  %s = alloc 4\n", ndata))
	w.Write(fmt.Sprintf("  jmp %s\n", allocDone))
	w.Write(fmt.Sprintf("%s:\n", allocOk))
	w.Write(fmt.Sprintf("  %s = alloc %s\n", ndata, nbytes))
	w.Write(fmt.Sprintf("%s:\n", allocDone))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	copyL := fmt.Sprintf("L_push_copy_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_push_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_push_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", copyL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	soff := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", soff, i))
	saddr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", saddr, data, soff))
	tmp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", tmp, saddr))
	daddr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", daddr, ndata, soff))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", daddr, tmp))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", copyL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	voff := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", voff, ln))
	vaddr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", vaddr, ndata, voff))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", vaddr, val))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", arr, ndata))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", arr, nlen))
	w.Write(fmt.Sprintf("  !%s\n", ndata))
	return nlen
}

// saLowerInsertionSort 原地数值插入排序（形状证据：封存 lowerInsertionSort:6057-6123）。
func saLowerInsertionSort(w printer.EmitTextWriter, arr string, scope *saScope, nextTemp *int) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, arr))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, arr))
	topL := fmt.Sprintf("L_sort_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_sort_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_sort_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 1\n", i))
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	ioff := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", ioff, i))
	iaddr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", iaddr, data, ioff))
	key := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", key, iaddr))
	j := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", j, i))
	inTop := fmt.Sprintf("L_sort_in_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	inChk := fmt.Sprintf("L_sort_in_chk_%d", *scope.nextLabel)
	*scope.nextLabel++
	inBody := fmt.Sprintf("L_sort_in_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	inEnd := fmt.Sprintf("L_sort_in_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", inTop))
	c1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sge %s, 0\n", c1, j))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c1, inChk, inEnd))
	w.Write(fmt.Sprintf("%s:\n", inChk))
	joff := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", joff, j))
	jaddr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", jaddr, data, joff))
	aj := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", aj, jaddr))
	c2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", c2, aj, key))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c2, inBody, inEnd))
	w.Write(fmt.Sprintf("%s:\n", inBody))
	j1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", j1, j))
	j1off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", j1off, j1))
	j1addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", j1addr, data, j1off))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", j1addr, aj))
	jm := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", jm, j))
	w.Write(fmt.Sprintf("  %s = %s\n", j, jm))
	w.Write(fmt.Sprintf("  jmp %s\n", inTop))
	w.Write(fmt.Sprintf("%s:\n", inEnd))
	k1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", k1, j))
	k1off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", k1off, k1))
	k1addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", k1addr, data, k1off))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", k1addr, key))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
}

// saLowerArrayScan 相等扫描（index 系回位/-1，includes 回 1/0；
// 形状证据：封存 lowerArrayScan:6444-6513）。
func saLowerArrayScan(w printer.EmitTextWriter, recv, want, from string, reverse, wantIndex bool, scope *saScope, nextTemp *int) string {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	start := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if reverse && from == "" {
		w.Write(fmt.Sprintf("  %s = sub %s, 1\n", start, ln))
	} else if from == "" {
		w.Write(fmt.Sprintf("  %s = 0\n", start))
	} else {
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", start, saArrayClampLen(w, from, ln, scope, nextTemp)))
	}
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if wantIndex {
		w.Write(fmt.Sprintf("  %s = -1\n", res))
	} else {
		w.Write(fmt.Sprintf("  %s = 0\n", res))
	}
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", i, start))
	topL := fmt.Sprintf("L_sc_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_sc_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	nextL := fmt.Sprintf("L_sc_next_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_sc_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	hitL := fmt.Sprintf("L_sc_hit_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if !reverse {
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	} else {
		w.Write(fmt.Sprintf("  %s = sge %s, 0\n", c, i))
	}
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, i))
	addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	cur := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", cur, addr))
	eq := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, %s\n", eq, cur, want))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", eq, hitL, nextL))
	w.Write(fmt.Sprintf("%s:\n", hitL))
	if wantIndex {
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", res, i))
	} else {
		w.Write(fmt.Sprintf("  %s = 1\n", res))
	}
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", nextL))
	step := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if !reverse {
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", step, i))
	} else {
		w.Write(fmt.Sprintf("  %s = sub %s, 1\n", step, i))
	}
	w.Write(fmt.Sprintf("  %s = %s\n", i, step))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return res
}

// saLowerArrayReverse 原地对称交换（形状证据：封存 lowerArrayReverse:6516-6556）。
func saLowerArrayReverse(w printer.EmitTextWriter, recv string, scope *saScope, nextTemp *int) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	half := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = div %s, 2\n", half, ln))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_rv_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_rv_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_rv_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, half))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	j := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", j, ln))
	j2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", j2, j, i))
	ao := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", ao, i))
	aa := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", aa, data, ao))
	bo := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", bo, j2))
	ba := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", ba, data, bo))
	a := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", a, aa))
	b := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", b, ba))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", aa, b))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", ba, a))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
}

// saLowerArraySlice 拷贝 [start, end) 到新数组（钳位；空域单路径分配；
// 形状证据：封存 lowerArraySlice:6559-6638）。
func saLowerArraySlice(w printer.EmitTextWriter, recv, start, end string, scope *saScope, nextTemp *int) string {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	sdata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", sdata, recv))
	s := saArrayClampLen(w, start, ln, scope, nextTemp)
	f := ln
	if end != "" {
		f = saArrayClampLen(w, end, ln, scope, nextTemp)
	}
	n := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", n, f, s))
	nneg := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, 0\n", nneg, n))
	one := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 1\n", one))
	keep := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", keep, one, nneg))
	nn := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", nn, n, keep))
	n = nn
	n1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", n1, n))
	nby := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", nby, n1))
	ddata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %s\n", ddata, nby))
	dh := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", dh))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", dh, ddata))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", dh, n))
	w.Write(fmt.Sprintf("  !%s\n", ddata))
	dloop := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dloop, dh))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_sl_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_sl_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	cendL := fmt.Sprintf("L_sl_cend_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, n))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, cendL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	si := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", si, s, i))
	so := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", so, si))
	sa := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", sa, sdata, so))
	cv := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", cv, sa))
	do := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", do, i))
	da := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", da, dloop, do))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", da, cv))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", cendL))
	saPropArrNest(scope, recv, dh)
	saPropArrStr(scope, recv, dh)
	// 切片柄归属(返前释放；上游同形).
	saOwnTemp(scope, dh)
	return dh
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
	return saLowerCheckedIndex(w, recv, sel, scope.nextLabel, nextTemp)
}

// saLowerArrayJoin 元素经 interp 折叠拼接（分隔符除首元外；形状证据：
// 封存 lowerArrayJoin:6658-6712；本薄口元恒 i32，直走 interp）。
func saLowerArrayJoin(w printer.EmitTextWriter, recv, sep string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/string.sai")
	scope.addImport("sa_std/fmt.sai")
	sepslice := saLowerStringLiteral(w, ",", scope, nextTemp)
	if sep != "" {
		sepslice = sep
	}
	acc := saLowerStringLiteral(w, "", scope, nextTemp)
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_jn_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_jn_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_jn_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	first := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	fl := fmt.Sprintf("L_jn_fl_%d", *scope.nextLabel)
	*scope.nextLabel++
	fe := fmt.Sprintf("L_jn_fe_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", first, i))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", first, fl, fe))
	w.Write(fmt.Sprintf("%s:\n", fl))
	acc = saConcatSlices(w, acc, sepslice, scope, nextTemp)
	w.Write(fmt.Sprintf("  jmp %s\n", fe))
	w.Write(fmt.Sprintf("%s:\n", fe))
	off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, i))
	addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	raw := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", raw, addr))
	part := saRenderInterp(w, raw, scope, nextTemp)
	acc = saConcatSlices(w, acc, part, scope, nextTemp)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return acc, ""
}

// saLowerCopyWithinStep 单步 src[s+k] 到 dst[t+k]（形状证据：封存 lowerCopyWithinStep:6801-6817）。
func saLowerCopyWithinStep(w printer.EmitTextWriter, data, t, s, k string, nextTemp *int) {
	si := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", si, s, k))
	so := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", so, si))
	sa := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", sa, data, so))
	cur := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", cur, sa))
	di := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", di, t, k))
	dof := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", dof, di))
	da := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", da, data, dof))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", da, cur))
}

// saLowerCopyWithin 交叠安全方向拷贝（形状证据：封存 lowerCopyWithin:6717-6798）。
func saLowerCopyWithin(w printer.EmitTextWriter, recv, target, start, end string, scope *saScope, nextTemp *int) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
	t := saArrayClampLen(w, target, ln, scope, nextTemp)
	s := saArrayClampLen(w, start, ln, scope, nextTemp)
	f := ln
	if end != "" {
		f = saArrayClampLen(w, end, ln, scope, nextTemp)
	}
	span := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", span, f, s))
	room := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", room, ln, t))
	count := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	pick := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	minS := fmt.Sprintf("L_cw_minspan_%d", *scope.nextLabel)
	*scope.nextLabel++
	minR := fmt.Sprintf("L_cw_minroom_%d", *scope.nextLabel)
	*scope.nextLabel++
	cntL := fmt.Sprintf("L_cw_cnt_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", pick, span, room))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", pick, minS, minR))
	w.Write(fmt.Sprintf("%s:\n", minS))
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", count, span))
	w.Write(fmt.Sprintf("  jmp %s\n", cntL))
	w.Write(fmt.Sprintf("%s:\n", minR))
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", count, room))
	w.Write(fmt.Sprintf("  jmp %s\n", cntL))
	w.Write(fmt.Sprintf("%s:\n", cntL))
	goT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	runL := fmt.Sprintf("L_cw_run_%d", *scope.nextLabel)
	*scope.nextLabel++
	skipL := fmt.Sprintf("L_cw_skip_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = sgt %s, 0\n", goT, count))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", goT, runL, skipL))
	w.Write(fmt.Sprintf("%s:\n", runL))
	fwd := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	fInit := fmt.Sprintf("L_cw_finit_%d", *scope.nextLabel)
	*scope.nextLabel++
	bTop := fmt.Sprintf("L_cw_btop_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", fwd, t, s))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", fwd, fInit, bTop))
	w.Write(fmt.Sprintf("%s:\n", fInit))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	fTop := fmt.Sprintf("L_cw_ftop_%d", *scope.nextLabel)
	*scope.nextLabel++
	fBody := fmt.Sprintf("L_cw_fbody_%d", *scope.nextLabel)
	*scope.nextLabel++
	fEnd := fmt.Sprintf("L_cw_fend_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  jmp %s\n", fTop))
	w.Write(fmt.Sprintf("%s:\n", fTop))
	fc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", fc, i, count))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", fc, fBody, fEnd))
	w.Write(fmt.Sprintf("%s:\n", fBody))
	saLowerCopyWithinStep(w, data, t, s, i, nextTemp)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", fTop))
	w.Write(fmt.Sprintf("%s:\n", fEnd))
	w.Write(fmt.Sprintf("  jmp %s\n", skipL))
	w.Write(fmt.Sprintf("%s:\n", bTop))
	j := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", j, count))
	bCond := fmt.Sprintf("L_cw_bcond_%d", *scope.nextLabel)
	*scope.nextLabel++
	bBody := fmt.Sprintf("L_cw_bbody_%d", *scope.nextLabel)
	*scope.nextLabel++
	bEnd := fmt.Sprintf("L_cw_bend_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  jmp %s\n", bCond))
	w.Write(fmt.Sprintf("%s:\n", bCond))
	bc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sge %s, 0\n", bc, j))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", bc, bBody, bEnd))
	w.Write(fmt.Sprintf("%s:\n", bBody))
	saLowerCopyWithinStep(w, data, t, s, j, nextTemp)
	jnext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", jnext, j))
	w.Write(fmt.Sprintf("  %s = %s\n", j, jnext))
	w.Write(fmt.Sprintf("  jmp %s\n", bCond))
	w.Write(fmt.Sprintf("%s:\n", bEnd))
	w.Write(fmt.Sprintf("  jmp %s\n", skipL))
	w.Write(fmt.Sprintf("%s:\n", skipL))
}

// saLowerToReversed 逆序拷贝到新数组（形状证据：封存 lowerToReversed:6820-6879）。
func saLowerToReversed(w printer.EmitTextWriter, recv string, scope *saScope, nextTemp *int) string {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	sdata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", sdata, recv))
	ln1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", ln1, ln))
	nby := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", nby, ln1))
	ddata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %s\n", ddata, nby))
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", dest))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", dest, ddata))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", dest, ln))
	w.Write(fmt.Sprintf("  !%s\n", ddata))
	dloop := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dloop, dest))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_tr_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_tr_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_tr_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	si := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", si, ln))
	si2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", si2, si, i))
	so := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", so, si2))
	sa := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", sa, sdata, so))
	cv := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", cv, sa))
	dof := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", dof, i))
	da := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", da, dloop, dof))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", da, cv))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	return dest
}

// saLowerArrayWith 拷贝并定点替换（越界透传拷贝；形状证据：封存 lowerArrayWith:6883-6919）。
func saLowerArrayWith(w printer.EmitTextWriter, recv, idx, val string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	_ = refusals
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	isneg := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, 0\n", isneg, idx))
	adj := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", adj, ln, isneg))
	norm := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", norm, idx, adj))
	cp := saLowerArraySlice(w, recv, "0", "", scope, nextTemp)
	lo := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	hi := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, 0\n", lo, norm))
	w.Write(fmt.Sprintf("  %s = sge %s, %s\n", hi, norm, ln))
	bad := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = or %s, %s\n", bad, lo, hi))
	badL := fmt.Sprintf("L_w_bad_%d", *scope.nextLabel)
	*scope.nextLabel++
	okL := fmt.Sprintf("L_w_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	finL := fmt.Sprintf("L_w_fin_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", bad, badL, okL))
	w.Write(fmt.Sprintf("%s:\n", badL))
	w.Write(fmt.Sprintf("  jmp %s\n", finL))
	w.Write(fmt.Sprintf("%s:\n", okL))
	cdata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", cdata, cp))
	off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, norm))
	addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, cdata, off))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", addr, val))
	w.Write(fmt.Sprintf("  jmp %s\n", finL))
	w.Write(fmt.Sprintf("%s:\n", finL))
	_ = pos
	saPropArrNest(scope, recv, cp)
	saPropArrStr(scope, recv, cp)
	return cp, ""
}

// saLowerToSpliced 新数组删段插项（形状证据：封存 lowerToSpliced:6923-6975）。
func saLowerToSpliced(w printer.EmitTextWriter, recv string, args []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
	sdata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", sdata, recv))
	start, del := "0", ln
	if len(args) > 0 {
		v, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		start = v
	}
	if len(args) > 1 {
		v, msg := saEvalI32(w, args[1], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		del = v
	}
	var itemOps []string
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
			itemOps = append(itemOps, v)
		}
	}
	s := saArrayClampLen(w, start, ln, scope, nextTemp)
	maxdel := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", maxdel, ln, s))
	d := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", d, del))
	neg := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, 0\n", neg, d))
	keepNeg := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub 1, %s\n", keepNeg, neg))
	d0 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", d0, d, keepNeg))
	over := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", over, d0, maxdel))
	gap := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", gap, maxdel, d0))
	fix := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", fix, gap, over))
	d1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", d1, d0, fix))
	d = d1
	kept := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", kept, ln, d))
	ni := fmt.Sprintf("%d", len(itemOps))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", nlen, kept, ni))
	tailStart := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", tailStart, s, d))
	dstOff := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", dstOff, s, ni))
	_ = tailStart
	_ = dstOff
	nlen1 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", nlen1, nlen))
	nby := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", nby, nlen1))
	ddata := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %s\n", ddata, nby))
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", dest))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", dest, ddata))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", dest, nlen))
	w.Write(fmt.Sprintf("  !%s\n", ddata))
	dloop := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dloop, dest))
	saCopyRange(w, sdata, dloop, "0", s, "0", scope, nextTemp)
	for k, it := range itemOps {
		di := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %d\n", di, s, k))
		dof := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", dof, di))
		da := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", da, dloop, dof))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", da, it))
	}
	s2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", s2, s, d))
	dst2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", dst2, s, ni))
	saCopyRange(w, sdata, dloop, s2, ln, dst2, scope, nextTemp)
	saPropArrNest(scope, recv, dest)
	saPropArrStr(scope, recv, dest)
	return dest, ""
}

// saLowerArrayConcat 首尾相接（数组逐元，标量 push；形状证据：封存 lowerArrayConcat:7061-7090）。
func saLowerArrayConcat(w printer.EmitTextWriter, recv string, args []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	h := saNewEmptyArray(w, nextTemp)
	saAppendSlice(w, h, recv, scope, nextTemp)
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
			saAppendSlice(w, h, src, scope, nextTemp)
			saPropArrNest(scope, src, h)
			strOK = strOK && scope.arrStr[src]
			continue
		}
		if a != nil && saIsArrValue(a, scope) {
			src, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			saAppendSlice(w, h, src, scope, nextTemp)
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
		w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
		n1 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", n1, n))
		nby := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", nby, n1))
		buf := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc %s\n", buf, nby))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", h, buf))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", h, n))
		w.Write(fmt.Sprintf("  !%s\n", buf))
		base = h
	} else {
		src, msg := saArrValueOf(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		h := saNewEmptyArray(w, nextTemp)
		saAppendSlice(w, h, src, scope, nextTemp)
		saPropArrNest(scope, src, h)
		saPropArrStr(scope, src, h)
		base = h
	}
	if mapper == nil {
		return base, ""
	}
	return saHigherOrderMap(w, base, mapper, scope, pos, refusals, needImport, nextLabel, nextTemp)
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
	return nil
}

func saCallbackValue(w printer.EmitTextWriter, cb *ast.Node, argVals []string, wantValue bool, wantKind string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, paramKinds ...[]string) (string, string) {
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
		// 标量快照拷贝（形状证据：封存 bindCallbackParam:5289-5294）。
		// 实例句柄直传绑定（方法实例形参经变参 kinds 表）。
		name := nm.Text()
		if i < len(kinds) && len(kinds[i]) > 5 && kinds[i][:5] == "inst:" {
			w.Write(fmt.Sprintf("  %s = %s\n", name, argVals[i]))
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
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", name, argVals[i]))
		bind(name, saCallbackScalarKind(kinds, i))
		saDeclarePlain(scope, name)
	}
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
				return "0", ""
			}
			v = op
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
		return v, ""
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	saOwnTemp(scope, slot)
	endL := fmt.Sprintf("L_cb_end_%d", *nextLabel)
	*nextLabel++
	kind := "i32"
	if wantKind == "str" {
		kind = "str"
	}
	savedRet := scope.inlineRet
	scope.inlineRet = &saInlineRet{slot: slot, end: endL, kind: kind, scopeBase: len(scope.ownOrder)}
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
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if kind == "str" {
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", out, slot))
		saReleaseOwnedTemp(w, scope, slot)
		return out, ""
	}
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	saReleaseOwnedTemp(w, scope, slot)
	return out, ""
}

// saHigherOrderMap `map` 内联（新数组逐元回调值压栈；形状证据：封存 lowerHigherOrder:4945-4965）。
func saHigherOrderMap(w printer.EmitTextWriter, recv string, cb *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
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
	el := saArrElemAt(w, data, i, nextTemp)
	v, msg := saCallbackValue(w, cb, []string{el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, saNestedElemKinds(scope, recv, 0, 2))
	if msg != "" {
		return "", msg
	}
	saLowerArrayPush(w, h, v, scope, nextTemp)
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
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
func saHigherOrderScan(w printer.EmitTextWriter, recv, method string, cb *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string, string) {
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
		if _, msg := saCallbackValue(w, cb, []string{el, i}, false, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, saNestedElemKinds(scope, recv, 0, 2)); msg != "" {
			return "", "", msg
		}
		inext := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		return "0", "i32", ""
	case "filter":
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
		v, msg := saCallbackValue(w, cb, []string{el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, saNestedElemKinds(scope, recv, 0, 2))
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
		return h, "arr", ""
	case "find", "findIndex", "findLast", "findLastIndex":
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
		v, msg := saCallbackValue(w, cb, []string{el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, saNestedElemKinds(scope, recv, 0, 2))
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
		return out, "i32", ""
	case "some", "every":
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
		v, msg := saCallbackValue(w, cb, []string{el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, saNestedElemKinds(scope, recv, 0, 2))
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
	v, msg := saCallbackValue(w, cb, []string{acc, el, i}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, saNestedElemKinds(scope, recv, 1, 3))
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
		v, msg := saCallbackValue(w, cb, []string{x, y}, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, saSortElemKinds(scope, recv))
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
		if a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression {
			return a, i, ""
		}
		if a.Kind == ast.KindIdentifier {
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
		return "", "", msg
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
		if len(argNodes) != 1 {
			return "", "", "push needs 1 argument"
		}
		v, msg := saArrayLiteralElem(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		// 已知 i32 数组收串/实例值拒（4 字节槽截断句柄；串标数组沿既有串元口径；
		// 按语法种判定（字面量未必记种）；铁律 4）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			if k, ok := scope.types[pa.Expression.Text()]; ok && k == "arr" {
				if scope.arrStr == nil || !scope.arrStr[pa.Expression.Text()] {
					if saIsStrValue(argNodes[0], scope) || saCouldBeInst(argNodes[0], scope) {
						return "", "", "array element kind mismatch (non-string array takes i32 values)"
					}
				}
			}
		}
		return saLowerArrayPush(w, recv, v, scope, nextTemp), "i32", ""
	case "pop":
		if len(argNodes) != 0 {
			return "", "", "pop needs 0 arguments"
		}
		ln := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
		last := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sub %s, 1\n", last, ln))
		data := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
		off := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, last))
		addr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, addr))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", recv, last))
		return out, "i32", ""
	case "shift":
		if len(argNodes) != 0 {
			return "", "", "shift needs 0 arguments"
		}
		ln := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
		data := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, data))
		nlen := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sub %s, 1\n", nlen, ln))
		topL := fmt.Sprintf("L_sh_top_%d", *nextLabel)
		*nextLabel++
		bodyL := fmt.Sprintf("L_sh_body_%d", *nextLabel)
		*nextLabel++
		endL := fmt.Sprintf("L_sh_end_%d", *nextLabel)
		*nextLabel++
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = 0\n", i))
		w.Write(fmt.Sprintf("%s:\n", topL))
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, nlen))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
		w.Write(fmt.Sprintf("%s:\n", bodyL))
		src := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", src, i))
		soff := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", soff, src))
		saddr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", saddr, data, soff))
		tmp := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", tmp, saddr))
		doff := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", doff, i))
		daddr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", daddr, data, doff))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", daddr, tmp))
		inext := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", recv, nlen))
		return out, "i32", ""
	case "unshift":
		if len(argNodes) != 1 {
			return "", "", "unshift needs 1 argument"
		}
		v, msg := saArrayLiteralElem(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		nlen := saLowerArrayPush(w, recv, v, scope, nextTemp)
		data := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
		topL := fmt.Sprintf("L_unsh_top_%d", *nextLabel)
		*nextLabel++
		bodyL := fmt.Sprintf("L_unsh_body_%d", *nextLabel)
		*nextLabel++
		endL := fmt.Sprintf("L_unsh_end_%d", *nextLabel)
		*nextLabel++
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sub %s, 1\n", i, nlen))
		w.Write(fmt.Sprintf("%s:\n", topL))
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sgt %s, 0\n", c, i))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
		w.Write(fmt.Sprintf("%s:\n", bodyL))
		prev := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sub %s, 1\n", prev, i))
		soff := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", soff, prev))
		saddr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", saddr, data, soff))
		tmp := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", tmp, saddr))
		doff := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = mul %s, 4\n", doff, i))
		daddr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", daddr, data, doff))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", daddr, tmp))
		w.Write(fmt.Sprintf("  %s = %s\n", i, prev))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", data, v))
		return nlen, "i32", ""
	case "fill":
		if len(argNodes) != 1 {
			return "", "", "fill needs 1 argument"
		}
		v, msg := saArrayLiteralElem(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		ln := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, recv))
		data := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, recv))
		topL := fmt.Sprintf("L_fill_top_%d", *nextLabel)
		*nextLabel++
		bodyL := fmt.Sprintf("L_fill_body_%d", *nextLabel)
		*nextLabel++
		endL := fmt.Sprintf("L_fill_end_%d", *nextLabel)
		*nextLabel++
		i := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = 0\n", i))
		w.Write(fmt.Sprintf("%s:\n", topL))
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
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
		w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
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
		if method == "sort" {
			saLowerInsertionSort(w, recv, scope, nextTemp)
			return recv, "arr", ""
		}
		cp := saLowerArraySlice(w, recv, "0", "", scope, nextTemp)
		saLowerInsertionSort(w, cp, scope, nextTemp)
		return cp, "arr", ""
	case "indexOf", "lastIndexOf", "includes":
		if len(argNodes) < 1 {
			return "", "", method + " needs 1 argument"
		}
		want, msg := i32arg(0)
		if msg != "" {
			return "", "", msg
		}
		from := ""
		if len(argNodes) > 1 {
			var msg string
			from, msg = i32arg(1)
			if msg != "" {
				return "", "", msg
			}
		} else if method != "lastIndexOf" {
			from = "0"
		}
		reverse := method == "lastIndexOf"
		wantIndex := method != "includes"
		return saLowerArrayScan(w, recv, want, from, reverse, wantIndex, scope, nextTemp), "i32", ""
	case "reverse":
		if len(argNodes) != 0 {
			return "", "", "reverse needs 0 arguments"
		}
		saLowerArrayReverse(w, recv, scope, nextTemp)
		return recv, "arr", ""
	case "slice":
		start, end := "0", ""
		if len(argNodes) > 0 {
			var msg string
			start, msg = i32arg(0)
			if msg != "" {
				return "", "", msg
			}
		}
		if len(argNodes) > 1 {
			var msg string
			end, msg = i32arg(1)
			if msg != "" {
				return "", "", msg
			}
		}
		if len(argNodes) > 2 {
			return "", "", "slice takes at most 2 arguments"
		}
		return saLowerArraySlice(w, recv, start, end, scope, nextTemp), "arr", ""
	case "at":
		if len(argNodes) != 1 {
			return "", "", "at needs 1 argument"
		}
		v, msg := i32arg(0)
		if msg != "" {
			return "", "", msg
		}
		return saLowerArrayAt(w, recv, v, scope, nextTemp), "i32", ""
	case "join":
		var sep string
		if len(argNodes) > 0 {
			h, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			sep = h
		}
		if len(argNodes) > 1 {
			return "", "", "join takes at most 1 argument"
		}
		h, msg := saLowerArrayJoin(w, recv, sep, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		return h, "str", ""
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
		saLowerCopyWithin(w, recv, target, start, end, scope, nextTemp)
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
	case "concat":
		h, msg := saLowerArrayConcat(w, recv, argNodes, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		return h, "arr", ""
	case "forEach", "map", "filter", "find", "findIndex", "findLast", "findLastIndex", "some", "every":
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
		if method == "map" {
			h, msg := saHigherOrderMap(w, recv, cb, scope, pos, refusals, needImport, nextLabel, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			return h, "arr", ""
		}
		out, kind, msg := saHigherOrderScan(w, recv, method, cb, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		if method == "filter" {
			saPropArrNest(scope, recv, out)
			saPropArrStr(scope, recv, out)
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
