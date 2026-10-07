// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_date.go — Date 最小子集 + 正则 POSIX-ERE 投影（step29 门，step183 起投影）。
// 形状证据：封存 lowerMethodCall Date 段:4338-4407（now/parse/i64 恒等/
// getTimezoneOffset 常 0/投影直调）+ 收敛证据 saemit_test.go:739-830 +
// 投影表 stdlib.go:176-217（time.sai 现货直调）。
// 本薄口无 i64 种：i64 以 date 种不透明流转（Date.now/parse/getTime/
// valueOf/getters/setters/setTime），永不截断；仅支持 `new Date()` 无参绑定
// （millis 不透明存种 "date"，setTime 直换）与纯串方法（toISOString/toString/toDateString/toTimeString/toUTCString）
// 及 getTimezoneOffset 常 0。
// 正则（step183）：sci 底座 `sa_std/text/regex.sai` 现货直投（`@import
// "sa_std/text/regex.sa"`，用法见 `sci/tests/unit_framework/support/json_regex.sa`
// + sal 常量 `SA_REGEX_EXTENDED/ICASE/NEWLINE`）：字面量/new RegExp(串字面量)
// 编译为 regex 柄（ptr 种 "regex"，归属纪律同 map/set 句柄）；`.test(串)` 经
// `sa_regex_match` 判空（`ne match, 0`）+ `sa_regex_match_free(^match)` 即释；
// 默认 cflags 取 `SA_REGEX_EXTENDED`(1)，`i` 加 ICASE(2)、`m` 加 NEWLINE(4)；
// `g/y/d/s/u/v` 无底座位一律大声拒；JS 特有写法（`(?` 前瞻/命名组、`\d\s\w\b` 等
// 转义类、`\p \u \x`、`\1` 反向引用、NUL）超 POSIX-ERE 即大声拒，永不静默错码；
// `.exec/replace/split/match` 另步，沿旧门。

// saIsDateNew 识别无参 `new Date()`。
func saIsDateNew(e *ast.Node) bool {
	if e == nil || e.Kind != ast.KindNewExpression {
		return false
	}
	ne := e.AsNewExpression()
	if ne.Expression == nil || ne.Expression.Kind != ast.KindIdentifier || ne.Expression.Text() != "Date" {
		return false
	}
	return ne.Arguments == nil || len(ne.Arguments.Nodes) == 0
}

// saLowerDateNew `new Date()`（零参 now 形；返回 millis 不透明临时量。
// 形状证据：封存 lowerNew:8609-8613 + new Date 测试 d1/d3）。
func saLowerDateNew(w printer.EmitTextWriter, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/time.sai")
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_time_unix_ms()\n", t))
	return t
}

// saDateStrMethod 串位 Date 方法表（toISOString + format_utc 0..3）。
func saDateStrMethod(m string) (string, string, bool) {
	switch m {
	case "toISOString":
		return "sa_time_iso_from_unix_ms", "", true
	case "toString", "toDateString", "toTimeString", "toUTCString":
		fmts := map[string]string{"toString": "0", "toDateString": "1", "toTimeString": "2", "toUTCString": "3"}
		return "sa_time_format_utc", fmts[m], true
	}
	return "", "", false
}

// saDateGetter 日历分量读表（i64 ABI → i32 窄化：分量恒 < 2^31，
// 窄化点唯一，出入皆显式；millis 本体永不窄化，仍以 date 种流转）。
func saDateGetter(m string) (string, bool) {
	switch m {
	case "getFullYear", "getMonth", "getDate", "getHours", "getMinutes", "getSeconds",
		"getMilliseconds", "getDay":
		return "sa_time_get_" + map[string]string{
			"getFullYear": "full_year", "getMonth": "month", "getDate": "date",
			"getHours": "hours", "getMinutes": "minutes", "getSeconds": "seconds",
			"getMilliseconds": "milliseconds", "getDay": "day",
		}[m], true
	}
	return "", false
}

// saDateSetterField 写字段 id 表（形状证据：封存 lowerMethodCall:4389-4404）。
func saDateSetterField(m string) (string, bool) {
	switch m {
	case "setFullYear":
		return "0", true
	case "setMonth":
		return "1", true
	case "setDate":
		return "2", true
	case "setHours":
		return "3", true
	case "setMinutes":
		return "4", true
	case "setSeconds":
		return "5", true
	case "setMilliseconds":
		return "6", true
	}
	return "", false
}

// saLowerDateCall Date 调用总线（返回 operand/种/errMsg；
// 种 ∈ {"i32","str","date"}；i64 以 date 种不透明流转，永不截断）。
func saLowerDateCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", "", "not a date call"
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return "", "", "missing method name"
	}
	method := pa.Name().Text()
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	// 静态位。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Date" {
		switch method {
		case "now":
			if len(argNodes) != 0 {
				return "", "", "Date.now needs 0 arguments"
			}
			scope.addImport("sa_std/time.sai")
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_time_unix_ms()\n", t))
			return t, "date", ""
		case "parse":
			if len(argNodes) != 1 {
				return "", "", "Date.parse needs 1 argument"
			}
			h, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			ip, il := saExpandStr(w, h, nextTemp)
			scope.addImport("sa_std/time.sai")
			ms := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", ms))
			st := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_time_parse_iso(&%s, %s, &%s)\n", st, ip, il, ms))
			badL := fmt.Sprintf("L_parse_bad_%d", *scope.nextLabel)
			*scope.nextLabel++
			okL := fmt.Sprintf("L_parse_ok_%d", *scope.nextLabel)
			*scope.nextLabel++
			bad := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = ne %s, 0\n", bad, st))
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", bad, badL, okL))
			w.Write(fmt.Sprintf("%s:\n", badL))
			w.Write(fmt.Sprintf("  panic(%d)\n", 2503))
			w.Write(fmt.Sprintf("%s:\n", okL))
			w.Write(fmt.Sprintf("  !%s\n", st))
			out := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as i64\n", out, ms))
			w.Write(fmt.Sprintf("  !%s\n", ms))
			return out, "date", ""
		}
		return "", "", "unknown Date member " + method
	}
	// 实例位：基须为 date 绑定或行内 `new Date()`。
	var ms string
	if saIsDateNew(pa.Expression) {
		ms = saLowerDateNew(w, scope, nextTemp)
	} else if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		k, ok := scope.types[pa.Expression.Text()]
		if !ok || k != "date" {
			return "", "", "not a date call"
		}
		ms = pa.Expression.Text()
	}
	if sym, extra, ok := saDateStrMethod(method); ok {
		if len(argNodes) != 0 {
			return "", "", "Date." + method + " takes 0 arguments"
		}
		scope.addImport("sa_std/time.sai")
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		if extra == "" {
			w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", t, sym, ms))
		} else {
			w.Write(fmt.Sprintf("  %s = call @%s(%s, %s)\n", t, sym, ms, extra))
		}
		// 外部调用结果登记归属（P0-4：@main 尾泄漏；R1-12 同律，drain 释）。
		saOwnTemp(scope, t)
		return t, "str", ""
	}
	if method == "getTimezoneOffset" && len(argNodes) == 0 {
		// UTC-only 子集恒 0（形状证据：封存 lowerMethodCall:4369-4371）。
		return "0", "i32", ""
	}
	if (method == "getTime" || method == "valueOf") && len(argNodes) == 0 {
		// millis 恒等（形状证据：封存 lowerMethodCall:4366-4368）。
		return ms, "date", ""
	}
	if sym, ok := saDateGetter(method); ok {
		if len(argNodes) != 0 {
			return "", "", "Date." + method + " takes 0 arguments"
		}
		scope.addImport("sa_std/time.sai")
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", t, sym, ms))
		// 外部调用结果登记归属（P0-4：@main 尾泄漏；R1-12 同律，drain 释）。
		saOwnTemp(scope, t)
		return t, "i32", ""
	}
	if method == "setTime" {
		// millis 直换（JS 返新值；i64 不透明流转：date 种直传（绑定/日期调用），
		// i32 小值 sext 提升；大 i64 字面量沿 i32 门拒，禁静默截断；零底座调用）。
		if len(argNodes) != 1 {
			return "", "", "Date.setTime takes 1 argument"
		}
		if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
			return "", "", "Date.setTime mutates a binding (no inline-new target)"
		}
		var v string
		a0 := argNodes[0]
		if a0 != nil && a0.Kind == ast.KindIdentifier {
			if k, ok := scope.types[a0.Text()]; ok && k == "date" {
				v = a0.Text()
			}
		}
		if v == "" && a0 != nil && a0.Kind == ast.KindCallExpression {
			if k, ok := saCallRetKind(a0.AsCallExpression(), scope); ok && k == "date" {
				op, voidCall, msg := saEvalCall(w, a0.AsCallExpression(), scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				if voidCall {
					return "", "", "void call in date position"
				}
				v = op
			}
		}
		if v == "" {
			i32v, msg := saEvalI32(w, a0, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			v = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sext %s as i64\n", v, i32v))
		}
		saStoreLocal(w, pa.Expression.Text(), v, scope, nextTemp)
		return v, "date", ""
	}
	if fid, ok := saDateSetterField(method); ok {
		if len(argNodes) != 1 {
			return "", "", "Date." + method + " takes 1 argument"
		}
		if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
			return "", "", "Date." + method + " mutates a binding (no inline-new target)"
		}
		v, msg := saEvalI32(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		scope.addImport("sa_std/time.sai")
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_time_set_field(%s, %s, %s)\n", t, ms, fid, v))
		saStoreLocal(w, pa.Expression.Text(), t, scope, nextTemp)
		return t, "date", ""
	}
	switch method {
	case "toLocaleString", "toLocaleDateString", "toLocaleTimeString":
		return "", "", "Date." + method + " is not in the subset (no projection)"
	}
	return "", "", "Date." + method + " is not in the subset (see the time projection list)"
}

// saDateBaseKind 基种判定（date 绑定或行内 new Date()）。
func saDateBaseKind(e *ast.Node, scope *saScope) bool {
	if saIsDateNew(e) {
		return true
	}
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "date" {
			return true
		}
	}
	return false
}

// saIsDateSetterCall 判定是否为变异重绑 setter 调用（返回柄经 `d = t`
// move 给绑定，语句位禁释，禁双释）。
func saIsDateSetterCall(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return false
	}
	if _, ok := saDateSetterField(pa.Name().Text()); !ok {
		return false
	}
	return saDateBaseKind(pa.Expression, scope)
}

// saDateCallKind Date 调用的返回种（语法级判定，不落字；供各求值位门禁）。
func saDateCallKind(ce *ast.CallExpression, scope *saScope) (string, bool) {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return "", false
	}
	method := pa.Name().Text()
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Date" {
		switch method {
		case "now", "parse":
			return "date", true
		}
		return "", false
	}
	if !saDateBaseKind(pa.Expression, scope) {
		return "", false
	}
	if _, _, ok := saDateStrMethod(method); ok {
		return "str", true
	}
	if method == "getTimezoneOffset" {
		return "i32", true
	}
	if method == "getTime" || method == "valueOf" {
		return "date", true
	}
	if _, ok := saDateGetter(method); ok {
		return "i32", true
	}
	if _, ok := saDateSetterField(method); ok {
		return "date", true
	}
	return "", true
}

// saIsDateStrCall 判定是否为串返回 Date 调用（供串位）。
func saIsDateStrCall(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return false
	}
	if _, _, ok := saDateStrMethod(pa.Name().Text()); !ok {
		return false
	}
	return saDateBaseKind(pa.Expression, scope)
}

// saIsDateI32Call 判定是否为 i32 返回 Date 调用（仅 getTimezoneOffset）。
func saIsDateI32Call(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil || pa.Name().Text() != "getTimezoneOffset" {
		return false
	}
	return saDateBaseKind(pa.Expression, scope)
}

// ---- 正则 POSIX-ERE 投影（step183；底座 `sa_std/text/regex.sai` 现货直调）----

// saRegexSplitLiteral 拆字面量 `/pat/flags`（Text 全形；`\/` 即 `/`）。
func saRegexSplitLiteral(text string) (string, string, bool) {
	if len(text) < 2 || text[0] != '/' {
		return "", "", false
	}
	sep := strings.LastIndex(text, "/")
	if sep <= 0 {
		return "", "", false
	}
	pat := strings.ReplaceAll(text[1:sep], "\\/", "/")
	return pat, text[sep+1:], true
}

// saRegexCflags 映 JS flags 为 sal cflags 字面量（EXTENDED=1/ICASE=2/NEWLINE=4；
// `g` 无底座状态位（全局性由调用方循环实现），编译期忽略，调用方按语义分流）。
func saRegexCflags(flags string) (string, string) {
	c := 1
	for _, f := range flags {
		switch f {
		case 'i':
			c |= 2
		case 'm':
			c |= 4
		case 'g':
		default:
			return "", "RegExp flag " + string(f) + " has no sa_std/text/regex projection (only i/m/g)"
		}
	}
	return fmt.Sprintf("%d", c), ""
}

// saRegexGatePattern 查 JS 特有写法（超 POSIX-ERE 即拒因；"" 为过）。
func saRegexGatePattern(pat string) string {
	if strings.Contains(pat, "(?") {
		return "RegExp (? groups need POSIX ERE (no lookahead/named/capture-less groups)"
	}
	for i := 0; i < len(pat); i++ {
		if pat[i] == 0 {
			return "RegExp NUL byte has no sa_std/text/regex projection"
		}
		if pat[i] != '\\' || i+1 >= len(pat) {
			continue
		}
		n := pat[i+1]
		switch n {
		case 'd', 'D', 's', 'S', 'w', 'W', 'b', 'B', 'p', 'P', 'u', 'x', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			return string("RegExp \\") + string([]byte{n}) + " needs POSIX ERE (no JS escape classes/backrefs)"
		}
		i++
	}
	return ""
}

// saLowerRegexCompile 编译柄（常量池 + `sa_regex_compile`；归属 temp）。
func saLowerRegexCompile(w printer.EmitTextWriter, pat, flags string, scope *saScope, nextTemp *int) (string, string) {
	if msg := saRegexGatePattern(pat); msg != "" {
		return "", msg
	}
	cf, msg := saRegexCflags(flags)
	if msg != "" {
		return "", msg
	}
	scope.addImport("sa_std/text/regex.sa")
	cname := saStrIntern(scope.strPool, pat)
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_compile(&%s, %d, %s)\n", t, cname, len(pat), cf))
	saOwnTemp(scope, t)
	return t, ""
}

// saLowerRegexNew `new RegExp("pat", "flags?")`（串字面量元のみ）。
func saLowerRegexNew(w printer.EmitTextWriter, ne *ast.NewExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	var args []*ast.Node
	if ne.Arguments != nil {
		args = ne.Arguments.Nodes
	}
	if len(args) < 1 || len(args) > 2 {
		return "", "new RegExp takes 1 pattern and 1 optional flags argument"
	}
	if args[0] == nil || args[0].Kind != ast.KindStringLiteral {
		return "", "new RegExp pattern must be a string literal"
	}
	flags := ""
	if len(args) == 2 {
		if args[1] == nil || args[1].Kind != ast.KindStringLiteral {
			return "", "new RegExp flags must be a string literal"
		}
		flags = args[1].Text()
	}
	return saLowerRegexCompile(w, args[0].Text(), flags, scope, nextTemp)
}

// saRegexBaseKind 基种判定（regex 绑定或行内字面量/new）。
func saRegexBaseKind(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	if e.Kind == ast.KindRegularExpressionLiteral {
		return true
	}
	if e.Kind == ast.KindNewExpression {
		ne := e.AsNewExpression()
		return ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "RegExp"
	}
	if e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "regex" {
			return true
		}
	}
	return false
}

// saLowerRegexTest `.test(串)`（match 判空 + 即释；回 i32；无分支直形——
// miss 时 match 柄为 0，`sa_regex_match_free(0)` 经 takeResourceLocked 取空
// 回句柄错码，无陷阱（`sci/src/runtime/sa_std.zig:dynamicIndex/sa_std_close`），
// 故免 br 保线性态一收敛，见 PhiStateConflict）。
func saLowerRegexTest(w printer.EmitTextWriter, recv string, arg *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if !saIsStrExpr(arg, scope) {
		return "", "RegExp.test takes a string argument"
	}
	h, msg := saEvalStr(w, arg, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	ip, il := saExpandStr(w, h, nextTemp)
	scope.addImport("sa_std/text/regex.sa")
	m := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_match(%s, &%s, %s)\n", m, recv, ip, il))
	hit := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", hit, m))
	fr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_match_free(^%s)\n", fr, m))
	fst := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", fst, fr))
	w.Write(fmt.Sprintf("  !%s\n", fr))
	w.Write(fmt.Sprintf("  !%s\n", fst))
	return hit, ""
}

// saLowerRegexMatchArray match 整体单元素串数组（miss 即 null 0 柄；
// `String.match` 无 g 与 `RegExp.exec` 共享；整体经 group0 ptr/len 具化新头
// push 入新串元数组；`saMarkArrStr` 记串元；分支内 alloc 分支内释放）。
func saLowerRegexMatchArray(w printer.EmitTextWriter, rh, tp, tl string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/text/regex.sa")
	m := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_match(%s, &%s, %s)\n", m, rh, tp, tl))
	hit := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", hit, m))
	hitL := fmt.Sprintf("L_mt_hit_%d", *nextTemp)
	*nextTemp++
	missL := fmt.Sprintf("L_mt_miss_%d", *nextTemp)
	*nextTemp++
	endL := fmt.Sprintf("L_mt_end_%d", *nextTemp)
	*nextTemp++
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", slot))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", hit, hitL, missL))
	w.Write(fmt.Sprintf("%s:\n", missL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  store %s + 8, 0 as u64\n", slot))
	mfr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_match_free(^%s)\n", mfr, m))
	w.Write(fmt.Sprintf("  !%s\n", mfr))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", hitL))
	gp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_group_ptr(%s, 0)\n", gp, m))
	gl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_group_len(%s, 0)\n", gl, m))
	fr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_regex_match_free(^%s)\n", fr, m))
	w.Write(fmt.Sprintf("  !%s\n", fr))
	gh := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", gh))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", gh, gp))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", gh, gl))
	saOwnTemp(scope, gh)
	h := saNewEmptyArray(w, nextTemp)
	saOwnTemp(scope, h)
	saLowerArrayPush(w, h, gh, scope, nextTemp)
	// gh 分支内具化分支内释放（drain 不可见分支内 alloc）。
	saReleaseOwnedTemp(w, scope, gh)
	hp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", hp, h))
	hl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", hl, h))
	saReleaseOwnedTemp(w, scope, h)
	w.Write(fmt.Sprintf("  !%s\n", gp))
	w.Write(fmt.Sprintf("  !%s\n", gl))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, hp))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", slot, hl))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	oh := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", oh, slot))
	ol := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", ol, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, oh))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, ol))
	saOwnTemp(scope, out)
	saMarkArrStr(scope, out)
	return out
}

// saLowerRegexCall 正则调用总线（`.test`→i32；`.exec`→整体单元素串数组
// （与 `String.match` 无 g 同形，共享 `saLowerRegexMatchArray`）；余下拒）。
func saLowerRegexCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	pa := ce.Expression.AsPropertyAccessExpression()
	method := pa.Name().Text()
	if method != "test" && method != "exec" {
		return "", "", "RegExp." + method + " is not in the subset (only .test/.exec)"
	}
	var args []*ast.Node
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", "", "RegExp." + method + " takes 1 argument"
	}
	recv := ""
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
		recv = pa.Expression.Text()
	} else {
		var msg string
		recv, msg = saLowerRegexInlineBase(w, pa.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
	}
	if method == "exec" {
		th, msg := saEvalStr(w, args[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		tp, tl := saExpandStr(w, th, nextTemp)
		return saLowerRegexMatchArray(w, recv, tp, tl, scope, nextTemp), "arr", ""
	}
	op, msg := saLowerRegexTest(w, recv, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", "", msg
	}
	return op, "i32", ""
}

// saLowerRegexInlineBase 行内基编译（字面量/new 直编；绑定名直传）。
func saLowerRegexInlineBase(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "regex" {
			return e.Text(), ""
		}
		return "", "not a regex call"
	}
	if e != nil && e.Kind == ast.KindRegularExpressionLiteral {
		pat, flags, ok := saRegexSplitLiteral(e.Text())
		if !ok {
			return "", "bad regular expression literal"
		}
		return saLowerRegexCompile(w, pat, flags, scope, nextTemp)
	}
	if e != nil && e.Kind == ast.KindNewExpression {
		ne := e.AsNewExpression()
		if ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "RegExp" {
			return saLowerRegexNew(w, ne, scope, pos, refusals, nextTemp)
		}
	}
	return "", "not a regex call"
}

// saRegexCallKind 正则调用返回种（`.test`→i32；`.exec`→arr，整体单元素
// 串数组，与 `String.match` 无 g 同形；余下非正则）。
func saRegexCallKind(ce *ast.CallExpression, scope *saScope) (string, bool) {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil || !saRegexBaseKind(pa.Expression, scope) {
		return "", false
	}
	if pa.Name().Text() == "test" {
		return "i32", true
	}
	if pa.Name().Text() == "exec" {
		return "arr", true
	}
	return "", true
}
