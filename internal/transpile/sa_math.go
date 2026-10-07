// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_math.go — Math.* 内联与别名（step18/20-24；禁止原创：形状以封存 lowerMath* 与 mathAliases 为准）。
// saEvalCall 求函数调用（形状证据：封存 `%s = call @%s(%s)` / `call @%s(%s)`）。
// 被调者须为同文件顶层函数（预扫签名表；元数精确匹配）；局部同名遮蔽则拒
// （无一等函数）。返回 (operand, isVoidCall, errMsg)。
// saNumberConst Number 整形常量折叠（形状证据：封存 stdlib.go:128-130
// `@const:2147483647/-2147483648` integer subset）。
func saNumberConst(pa *ast.PropertyAccessExpression) (string, bool) {
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Expression.Text() != "Number" ||
		pa.Name() == nil {
		return "", false
	}
	switch pa.Name().Text() {
	case "MAX_VALUE", "MAX_SAFE_INTEGER":
		return "2147483647", true
	case "MIN_SAFE_INTEGER":
		return "-2147483648", true
	}
	return "", false
}

// saIsNumberIsInteger 识别 `Number.isInteger/isSafeInteger(x)`（后者 i32
// 恒安全（|x|≤2^31-1<2^53-1），求值保留副作用后与前者同折 "1"；f64 位
// 本薄口不可达——浮点字面早拒。形状证据：封存 lowerCall:3837-3848 +
// stdlib.go:404-411；isSafeInteger 上游未路由，本仓按恒等式 thin-lead）。
func saIsNumberIsInteger(ce *ast.CallExpression) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Expression.Text() != "Number" {
		return false
	}
	if pa.Name() == nil {
		return false
	}
	nm := pa.Name().Text()
	return nm == "isInteger" || nm == "isSafeInteger"
}

// saEvalMathAbs 求 `Math.abs(x)`（形状证据：封存 lowerMathInline abs:5754-5780
// 分支汇合原样：alloc 8 槽 + `sge x, 0` + br + 两臂 store + end load + 释放。
// 其余 Math.* 本薄口大声拒；`Math.abs` 别名调用不认（无 mathAliases 表，拒）。
// saMathI32Arg 求 Math 整形参并验记种（串/实例句柄禁入；各方法共用；铁律 4）。
func saMathI32Arg(w printer.EmitTextWriter, a *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	if msg := saCheckI32Value(scope, v); msg != "" {
		return "", msg
	}
	return v, ""
}

func saEvalMathAbs(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math.abs needs 1 argument"
	}
	v, msg := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	// R3-9 回迁映射：语义由 `sci/sa_std/ts_math.sa` `@ts_math_iabs` 实现；
	// 本侧只做种门禁 + import + 调用 + 归属登记（call 结果登记，R3-1 同形）。
	scope.addImport("sa_std/ts_math.sa")
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_iabs(%s)\n", out, v))
	saOwnTemp(scope, out)
	return out, false, ""
}

// saEvalMathPow 求 `Math.pow(base, expo)`（形状证据：封存 lowerMathInline
// pow:5781-5806，即 lowerPowLoop 循环形；本薄口 saLowerPow:1130-1168 同形，
// 此处复用同发射，仅标号前缀取 L_mpow_ 以区分表达式位）。
// saEvalMathPow 求 `Math.pow(base, expo)`（形状证据：封存 lowerMathInline
// pow:5781-5806，即 lowerPowLoop 循环形；本薄口 saLowerPow:1130-1168 同形，
// 此处复用同发射，仅标号前缀取 L_mpow_ 以区分表达式位）。
// saEvalMathSign 求 `Math.sign(x)`（无分支形：`(x>0)-(x<0)`，i32 恒精确
// 含 0；上游拒（`Math.sign is not supported`），本仓 thin-lead，数学恒等式
// 无语义风险；操作数经既有 i32 门）。
func saEvalMathSign(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math.sign needs 1 argument"
	}
	v, msg := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	// R3-9 回迁映射：语义由 `sci/sa_std/ts_math.sa` `@ts_math_isign` 实现。
	scope.addImport("sa_std/ts_math.sa")
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_isign(%s)\n", out, v))
	saOwnTemp(scope, out)
	return out, false, ""
}

// saEvalMathImul 求 `Math.imul(a, b)`（SA mul 即 i32 wrap，与 imul 低 32 位
// 语义一致；双操作数经既有 i32 门）。
func saEvalMathImul(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 2 {
		return "", false, "Math.imul needs 2 arguments"
	}
	a, msg := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	b, msg := saMathI32Arg(w, args[1], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	// R3-9 回迁映射：语义由 `sci/sa_std/ts_math.sa` `@ts_math_imul` 实现。
	scope.addImport("sa_std/ts_math.sa")
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_imul(%s, %s)\n", t, a, b))
	saOwnTemp(scope, t)
	return t, false, ""
}

func saEvalMathPow(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 2 {
		return "", false, "Math.pow needs 2 arguments"
	}
	base, msgB := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msgB != "" {
		return "", false, msgB
	}
	expo, msgE := saMathI32Arg(w, args[1], scope, pos, refusals, nextTemp)
	if msgE != "" {
		return "", false, msgE
	}
	// R3-1 回迁映射：i32 逐乘幂语义由 `sci/sa_std/ts_math.sa` `@ts_math_ipow`
	// 实现（expo<=0 返 1；f64 extern 不可直映）；本侧只做种门禁 + import + 调用。
	scope.addImport("sa_std/ts_math.sa")
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_ipow(%s, %s)\n", res, base, expo))
	// call 结果登记归属（用后/返前释放；extern call1 同形；常量旧形豁免）。
	saOwnTemp(scope, res)
	return res, false, ""
}

// saEvalMathRounding 求 `Math.floor/ceil/round/trunc(x)`（整数操作数恒等 `out = add v, 0`；
// 浮点字面量实参走 fptosi 转换 + 负零碎调整块；形状证据：封存 lowerMathRounding:5941-5988 全形）。
// saEvalMathRounding 求 `Math.floor/ceil/round/trunc(x)`（整数操作数恒等 `out = add v, 0`；
// 浮点字面量实参走 fptosi 转换 + 负零碎调整块；形状证据：封存 lowerMathRounding:5941-5988 全形）。
// saLowerMathRoundingFloat 落浮点字面量实参的 floor/ceil/round/trunc 转换
// （fptosi + 负零碎调整块；ceil 取负、round 先加 0.5、trunc 直转；封存 lowerMathRounding:5947-5988）。
func saLowerMathRoundingFloat(w printer.EmitTextWriter, method, v string, scope *saScope, nextTemp *int) string {
	fresh := func() string {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		return t
	}
	farg := v
	negateOut := false
	if method == "ceil" {
		fn := fresh()
		w.Write(fmt.Sprintf("  %s = fneg %s\n", fn, v))
		farg = fn
		negateOut = true
	} else if method == "round" {
		fh := fresh()
		w.Write(fmt.Sprintf("  %s = fadd %s, 0.5\n", fh, v))
		farg = fh
	} else if method == "trunc" {
		ft := fresh()
		w.Write(fmt.Sprintf("  %s = fptosi %s\n", ft, v))
		return ft
	}
	t := fresh()
	w.Write(fmt.Sprintf("  %s = fptosi %s\n", t, farg))
	isNeg := fresh()
	w.Write(fmt.Sprintf("  %s = fcmp_lt %s, 0.0\n", isNeg, farg))
	back := fresh()
	w.Write(fmt.Sprintf("  %s = sitofp %s\n", back, t))
	isFrac := fresh()
	w.Write(fmt.Sprintf("  %s = fcmp_ne %s, %s\n", isFrac, farg, back))
	need := fresh()
	w.Write(fmt.Sprintf("  %s = and %s, %s\n", need, isNeg, isFrac))
	adjL := fmt.Sprintf("L_fl_adj_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_fl_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", need, adjL, endL))
	w.Write(fmt.Sprintf("%s:\n", adjL))
	dec := fresh()
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", dec, t))
	w.Write(fmt.Sprintf("  %s = %s\n", t, dec))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	if negateOut {
		out := fresh()
		w.Write(fmt.Sprintf("  %s = sub 0, %s\n", out, t))
		return out
	}
	return t
}

func saEvalMathRounding(w printer.EmitTextWriter, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math rounding needs 1 argument"
	}
	if args[0] != nil && args[0].Kind == ast.KindNumericLiteral && saIsFloatLit(args[0].Text()) {
		return saLowerMathRoundingFloat(w, method, args[0].Text(), scope, nextTemp), false, ""
	}
	v, msg := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, v))
	return out, false, ""
}

// saEvalMathMinMax 求 `Math.min(a,b)`/`Math.max(a,b)`（形状证据：封存
// lowerMathMinMax:5650-5687 两元折叠原样：slt/sgt + alloc 8 槽 + br + 两臂
// store + end load + 释放；spread 切片归约在本薄口大声拒）。
// saEvalMathMinMax 求 `Math.min(a,b)`/`Math.max(a,b)`（形状证据：封存
// lowerMathMinMax:5650-5687 两元折叠原样：slt/sgt + alloc 8 槽 + br + 两臂
// store + end load + 释放；spread 切片归约在本薄口大声拒）。
func saEvalMathMinMax(w printer.EmitTextWriter, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	for _, a := range args {
		if a != nil && a.Kind == ast.KindSpreadElement {
			return saEvalMathSpreadMinMax(w, method, a, scope, pos, refusals, nextTemp)
		}
	}
	if len(args) != 2 {
		return "", false, "Math." + method + " takes two scalars or one spread slice"
	}
	a0, msg0 := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msg0 != "" {
		return "", false, msg0
	}
	a1, msg1 := saMathI32Arg(w, args[1], scope, pos, refusals, nextTemp)
	if msg1 != "" {
		return "", false, msg1
	}
	// R3-10 回迁映射：语义由 `sci/sa_std/ts_math.sa` `@ts_math_minmax` 实现
	//（ismax 由调用点折叠；spread 归约走 spread_minmax）；本侧只做种门禁 + 调用 + 归属登记。
	scope.addImport("sa_std/ts_math.sa")
	ismax := "0"
	if method == "max" {
		ismax = "1"
	}
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_minmax(%s, %s, %s)\n", out, a0, a1, ismax))
	saOwnTemp(scope, out)
	return out, false, ""
}

// saEvalMathSqrt 求 `Math.sqrt(x)`（形状证据：封存 lowerMathSqrt:5841-5891
// 整数二分原样：acc=0、lo=1、hi=x，`mid<=x/mid` 取最佳；具名绑定先快照。
// 浮点在本薄口不可达——浮点字面早由 saEvalI32 大声拒）。
// saEvalMathSqrt 求 `Math.sqrt(x)`（形状证据：封存 lowerMathSqrt:5841-5891
// 整数二分原样：acc=0、lo=1、hi=x，`mid<=x/mid` 取最佳；具名绑定先快照。
// 浮点在本薄口不可达——浮点字面早由 saEvalI32 大声拒）。
func saEvalMathSqrt(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math.sqrt needs 1 argument"
	}
	x, msg := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	fx := x
	if len(x) < 2 || x[:2] != "t_" {
		if _, ok := scope.types[x]; ok {
			cp := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", cp, x))
			fx = cp
		}
	}
	// R3-2 回迁映射：整数二分开方语义由 `sci/sa_std/ts_math.sa` `@ts_math_isqrt`
	// 实现（返 floor；循环内 mid 经槽中转，分支汇合自赋值对齐；旧内联产物潜伏
	// UseAfterMove，改调即修）；本侧保留门禁 + 具名快照 + import + 调用 + 归属登记。
	scope.addImport("sa_std/ts_math.sa")
	acc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_isqrt(%s)\n", acc, fx))
	saOwnTemp(scope, acc)
	return acc, false, ""
}

// saEvalMathLog10 求 `Math.log10(x)`（形状证据：封存 lowerMathLog10:5894-5916
// 位数循环原样：`>=10` 则 `/=10` 且计数++）。
// saEvalMathLog10 求 `Math.log10(x)`（形状证据：封存 lowerMathLog10:5894-5916
// 位数循环原样：`>=10` 则 `/=10` 且计数++）。
func saEvalMathLog10(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math.log10 needs 1 argument"
	}
	x, msg := saMathI32Arg(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	// R3-3 回迁映射：十进制位数语义由 `sci/sa_std/ts_math.sa` `@ts_math_ilog10`
	// 实现；本侧只做种门禁 + import + 调用 + 归属登记（call 结果登记，R3-1 同形）。
	scope.addImport("sa_std/ts_math.sa")
	lacc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_ilog10(%s)\n", lacc, x))
	saOwnTemp(scope, lacc)
	return lacc, false, ""
}

// saEvalMathRandom 求 `Math.random()`（形状证据：封存 lowerMathRandom:5920-5936
// 确定性 LCG 原样：`__ts_rand_seed` 首用播 12345，`seed*1103515245+12345`，
// `ashr 16` 取低 15 位；序列非密码学，与 Node 不同已文档化）。
// saEvalMathRandom 求 `Math.random()`（形状证据：封存 lowerMathRandom:5920-5936
// 确定性 LCG 原样：`__ts_rand_seed` 首用播 12345，`seed*1103515245+12345`，
// `ashr 16` 取低 15 位；序列非密码学，与 Node 不同已文档化）。
func saEvalMathRandom(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	const seed = "__ts_rand_seed"
	if ce.Arguments != nil && len(ce.Arguments.Nodes) != 0 {
		return "", false, "Math.random needs 0 arguments"
	}
	if _, ok := scope.types[seed]; !ok {
		w.Write(fmt.Sprintf("  %s = 12345\n", seed))
		scope.types[seed] = "i32"
	}
	rs := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 1103515245\n", rs, seed))
	rs2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 12345\n", rs2, rs))
	w.Write(fmt.Sprintf("  %s = %s\n", seed, rs2))
	ro := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ashr %s, 16\n", ro, seed))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, 32767\n", out, ro))
	return out, false, ""
}

// saEvalMathSpreadMinMax 求 `Math.min/max(...slice)`（形状证据：封存
// lowerMathSpreadMinMax:5690-5747 原样：取 len/data，best 初值为
// ∓INT 极值，索引巡回 take/skip 归约；源须为绑定数组或字面量）。
// saEvalMathSpreadMinMax 求 `Math.min/max(...slice)`（形状证据：封存
// lowerMathSpreadMinMax:5690-5747 原样：取 len/data，best 初值为
// ∓INT 极值，索引巡回 take/skip 归约；源须为绑定数组或字面量）。
func saEvalMathSpreadMinMax(w printer.EmitTextWriter, method string, spread *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	se := spread.AsSpreadElement()
	var arr string
	if se.Expression != nil && se.Expression.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, se.Expression.AsNode(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, "unsupported spread slice: " + msg
		}
		arr = h
	} else if base, ok := saArrBase(scope, se.Expression.AsNode()); ok {
		arr = base
	} else {
		return "", false, "spread min/max needs a slice operand"
	}
	// R3-4 回迁映射：数组极值语义由 `sci/sa_std/ts_math.sa` `@ts_math_spread_minmax`
	// 实现（best 初极值由 ismax 内聚；分支汇合 best 自赋值对齐）；本侧保留源门禁
	//（字面量具化/绑定）+ import + 调用 + 归属登记（call 结果登记，R3-1 同形）。
	isMax := method == "max"
	ismax := "0"
	if isMax {
		ismax = "1"
	}
	scope.addImport("sa_std/ts_math.sa")
	best := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_math_spread_minmax(%s, %s)\n", best, arr, ismax))
	saOwnTemp(scope, best)
	return best, false, ""
}

// saMathMethodName 识别 `Math.<m>`（m 为已支持方法集；其余 Math.* 本薄口拒）。
// saMathMethodName 识别 `Math.<m>`（m 为已支持方法集；其余 Math.* 本薄口拒）。
func saMathMethodName(n *ast.Node) (string, bool) {
	if n == nil || n.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	pa := n.AsPropertyAccessExpression()
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Expression.Text() != "Math" ||
		pa.Name() == nil {
		return "", false
	}
	switch pa.Name().Text() {
	case "abs", "pow", "floor", "ceil", "round", "trunc", "min", "max", "sqrt", "log10", "random", "sign", "imul":
		return pa.Name().Text(), true
	}
	return "", false
}

// saEvalMathMethod 按方法名分发 Math 调用（含别名调用位；形状证据同各 saEvalMath*）。
// saEvalMathMethod 按方法名分发 Math 调用（含别名调用位；形状证据同各 saEvalMath*）。
func saEvalMathMethod(w printer.EmitTextWriter, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	switch method {
	case "abs":
		return saEvalMathAbs(w, ce, scope, pos, refusals, nextTemp)
	case "pow":
		return saEvalMathPow(w, ce, scope, pos, refusals, nextTemp)
	case "floor", "ceil", "round", "trunc":
		return saEvalMathRounding(w, method, ce, scope, pos, refusals, nextTemp)
	case "min", "max":
		return saEvalMathMinMax(w, method, ce, scope, pos, refusals, nextTemp)
	case "sqrt":
		return saEvalMathSqrt(w, ce, scope, pos, refusals, nextTemp)
	case "log10":
		return saEvalMathLog10(w, ce, scope, pos, refusals, nextTemp)
	case "random":
		return saEvalMathRandom(w, ce, scope, pos, refusals, nextTemp)
	case "sign":
		return saEvalMathSign(w, ce, scope, pos, refusals, nextTemp)
	case "imul":
		return saEvalMathImul(w, ce, scope, pos, refusals, nextTemp)
	}
	return "", false, "unsupported Math method " + method
}

// saSeedTopMaths 注入顶层 Math 别名（局部已记遮蔽顶层；封存 mathAliases 全局表位）。
func saSeedTopMaths(scope *saScope, topMaths map[string]string) {
	for k, v := range topMaths {
		if scope.mathAlias == nil {
			scope.mathAlias = map[string]string{}
		}
		if _, ok := scope.mathAlias[k]; !ok {
			scope.mathAlias[k] = v
		}
	}
}

// saTryMathAliasDecl 记录 `const f = Math.<m>` 及链式 `const g = f`（只记表，
// 不落字；形状证据：封存 lowerVarDeclList 前的 mathAliases:2957-2964 + 别名调用
// lowerMathCall:3795-3804）。返回 true 表示已认领。
func saTryMathAliasDecl(d *ast.Node, vd *ast.VariableDeclaration, name string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if vd.Initializer == nil {
		return false
	}
	if m, ok := saMathMethodName(vd.Initializer); ok {
		if scope.mathAlias == nil {
			scope.mathAlias = map[string]string{}
		}
		scope.mathAlias[name] = m
		return true
	}
	if vd.Initializer.Kind == ast.KindIdentifier {
		if m, ok := scope.mathAlias[vd.Initializer.Text()]; ok {
			if scope.mathAlias == nil {
				scope.mathAlias = map[string]string{}
			}
			scope.mathAlias[name] = m
			return true
		}
	}
	_ = d
	_ = pos
	_ = refusals
	return false
}
