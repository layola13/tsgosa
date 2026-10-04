// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_date.go — Date 最小子集 + 正则拒绝门（step29；JEV 落件 a 置信度 97%）。
// 形状证据：封存 lowerMethodCall Date 段:4338-4407（now/parse/i64 恒等/
// getTimezoneOffset 常 0/投影直调）+ 收敛证据 saemit_test.go:739-830 +
// 投影表 stdlib.go:176-217（time.sai 现货直调）。
// 本薄口无 i64 种：一切 i64 位（Date.now/parse/getTime/valueOf/getters/
// setters）大声拒；仅支持 `new Date()` 无参绑定（millis 不透明存种 "date"）
// 与纯串方法（toISOString/toString/toDateString/toTimeString/toUTCString）
// 及 getTimezoneOffset 常 0。
// 正则：satsgo/sa_plugin_ts 均无 lowering 证据（后者 REQUIREMENTS 明确记
// replace(/./g) 无支持），regex.sai 为无人调用的现货；铁律 4 禁止原创，
// 故字面量/new RegExp/.test/.exec 一律大声拒。

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
		return t, "i32", ""
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
