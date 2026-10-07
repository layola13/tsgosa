// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
	"strings"
)

// sa_str.go — str 种全集（step25；只调 sa_std 现货，见文件内形状总纲）。
// ---- string（str 种：16 字节 {ptr,len} 切片句柄，与 arr 同模型）----
// 形状证据总纲：封存 tString:141（ptr 句柄）+ lowerStringLiteral:2974-2990
// （@const utf8 + 16 字节头）+ lowerStringMethod:7156-7388（sa_std/string.sai
// 现货直调）+ concatSlices:8909-8930（concat 经 sa_fmt_buffer_data/len 读回）+
// renderInterpValue:8854-8904（串直通；i32/bool 经 sext + sa_fmt_i64_into；
// f64 经 sa_fmt_f64_into）+ lowerTemplate:8823-8847 +
// lowerConsoleLog:7862-7887（print.sai，两操作数间空格 + 末尾换行）+
// stringContentEq:9146-9166（等长 + 零偏 indexOf 命中）+ 投影表
// stdlib.go:80-106（concat/string 方法→string.sai，console.log→io/print.sai，
// 模板/i32 插值→fmt.sai；i32 Math 系全 @inline 故不投）。
// sa_std 现货（string.sai/fmt.sai）：concat/from_char_code/from_code_point/
// index_of/last_index_of/starts_with/ends_with/to_lower-upper_ascii/repeat/
// pad_start-end/replace/code_point_at + i64_into/buffer_data-len。
// 本薄口只调以上现货；split（串元数组超 i32 槽模型）、tagged模板、
// console.error（node 插件后端）一律大声拒（Number.parseFloat 见 step373 已映射）。

// saStrIntern 字符串常量池录入（同文本去重；转义镜像封存）。
func saStrIntern(pool *saStrPool, text string) string {
	if n, ok := pool.seen[text]; ok {
		return n
	}
	n := fmt.Sprintf("%sstr_const_%d", pool.prefix, pool.next)
	pool.next++
	esc := strings.ReplaceAll(text, "\\", "\\\\")
	esc = strings.ReplaceAll(esc, "\"", "\\\"")
	esc = strings.ReplaceAll(esc, "\n", "\\n")
	esc = strings.ReplaceAll(esc, "\r", "\\r")
	esc = strings.ReplaceAll(esc, "\t", "\\t")
	fmt.Fprintf(&pool.buf, "@const %s = utf8:\"%s\\0\"\n", n, esc)
	pool.seen[text] = n
	return n
}

// saLowerStringLiteral 字符串字面量具化（@const utf8 + 16 字节头）。
func saLowerStringLiteral(w printer.EmitTextWriter, text string, scope *saScope, nextTemp *int) string {
	cname := saStrIntern(scope.strPool, text)
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  store %s + 0, &%s as ptr\n", h, cname))
	w.Write(fmt.Sprintf("  store %s + 8, %d as u64\n", h, len(text)))
	saOwnTemp(scope, h)
	return h
}

// saExpandStr 展开句柄为 (ptr, len)（形状证据：封存 expandSlice:6126-6132）。
func saExpandStr(w printer.EmitTextWriter, h string, nextTemp *int) (string, string) {
	p := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	l := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", p, h))
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", l, h))
	return p, l
}

// saIsStrExpr 语法级判定表达式是否为串位（不落字；供 + 拼接与 i32 拒用）。
func saIsStrExpr(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateExpression:
		return true
	case ast.KindIdentifier:
		k, ok := scope.types[e.Text()]
		if ok && k == "str" {
			return true
		}
		// 槽名须无局部遮蔽（任一绑定优先；封存 modStrRecv:656-660）。
		if _, bound := scope.types[e.Text()]; !bound {
			if ms, ok := scope.modVars[e.Text()]; ok && ms.w == "str" {
				return true
			}
		}
		return ok && k == "str"
	case ast.KindCallExpression:
		return saCallIsStr(e.AsCallExpression(), scope)
	case ast.KindElementAccessExpression:
		// 串 Map 下标读即串值（值种按建表记；`?.` 沿旧门）。
		if ea := e.AsElementAccessExpression(); ea != nil && ea.Expression != nil &&
			ea.Expression.Kind == ast.KindIdentifier && ea.QuestionDotToken == nil {
			if k, ok := scope.types[ea.Expression.Text()]; ok && k == "map" {
				return scope.mapVals[ea.Expression.Text()] == "str"
			}
		}
		// 串下标读即串值（`s[i]` 与 charAt 同串位；`?.` 沿旧门）。
		if ea := e.AsElementAccessExpression(); ea != nil && ea.QuestionDotToken == nil &&
			saIsStrExpr(ea.Expression, scope) {
			return true
		}
		// 串元数组元素读即串值（arrStr 标记；split 读回链同形；`?.` 沿旧门）。
		if ea := e.AsElementAccessExpression(); ea != nil && ea.QuestionDotToken == nil &&
			ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier &&
			scope.arrStr != nil && scope.arrStr[ea.Expression.Text()] {
			return true
		}
		return false
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindQuestionQuestionToken {
			// `??` 空合串位（任一臂串值即串；求值走 saEvalStr 空合串槽）。
			return saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindPlusToken {
			// A + chain is string only if some operand is string-VALUED (i32-returning string
			// calls like charCodeAt do not count; otherwise nested arithmetic misroutes to concat).
			return saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)
		}
		return false
	case ast.KindParenthesizedExpression:
		return saIsStrExpr(e.AsParenthesizedExpression().Expression, scope)
	case ast.KindTypeOfExpression:
		// typeof 恒为种类串（值位具化见 saEvalStr；未知名沿求值门拒）。
		return true
	case ast.KindAsExpression:
		return saIsStrExpr(e.AsAsExpression().Expression, scope)
	case ast.KindPropertyAccessExpression:
		// str 域读即串值（saEvalStr 属性分支具化；静态串折叠同）。
		return saIsStrFieldRead(e.AsPropertyAccessExpression(), scope)
	case ast.KindSatisfiesExpression:
		return saIsStrExpr(e.AsSatisfiesExpression().Expression, scope)
	case ast.KindNonNullExpression:
		return saIsStrExpr(e.AsNonNullExpression().Expression, scope)
	case ast.KindTypeAssertionExpression:
		return saIsStrExpr(e.AsTypeAssertion().Expression, scope)
	case ast.KindTaggedTemplateExpression:
		// 仅 `String.raw` 为串值（余下标签求值拒；判定先行）。
		tt := e.AsTaggedTemplateExpression()
		if tt.Tag != nil && tt.Tag.Kind == ast.KindPropertyAccessExpression {
			pa := tt.Tag.AsPropertyAccessExpression()
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" &&
				pa.Name() != nil && pa.Name().Text() == "raw" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// saIsStrValue 串值判定（串位扣除 i32 返回的串方法调用；供 i32 位门禁，
// 免得 `&&`/`+` 等 numerical 上下文被方法名误拦）。
func saIsStrValue(e *ast.Node, scope *saScope) bool {
	if e != nil && e.Kind == ast.KindCallExpression && saStrCallIsI32(e.AsCallExpression(), scope) {
		return false
	}
	return saIsStrExpr(e, scope)
}

// saCallIsStr 判定调用是否为串返回（String()/String.from*/串方法/同文件 string 函数）。
func saCallIsStr(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil {
		return false
	}
	if ce.Expression.Kind == ast.KindIdentifier {
		nm := ce.Expression.Text()
		if nm == "String" {
			return true
		}
		// btoa/atob bare globals return strings (deno.sai, no import).
		if nm == "btoa" || nm == "atob" {
			return true
		}
		if sig, ok := scope.funcs[nm]; ok {
			return !sig.isVoid && sig.retKind == "string"
		}
		// Program link: 源级名经链接表查种子签名（`greet(..)` → `util__greet`；
		// saCallRetKind:647 同形，串判定此前漏链）。
		if q, linked := saLinkCallee(scope, nm); linked {
			if sig, ok := scope.funcs[q]; ok {
				return !sig.isVoid && sig.retKind == "string"
			}
		}
		// fs/net projected string surfaces (readFile returns a slice).
		if mod, ok := scope.imports[nm]; ok {
			remote := nm
			if r, ok := scope.importRemote[nm]; ok {
				remote = r
			}
			if mod == "fs" && remote == "readFile" {
				return true
			}
			// node.sai-backed string projections (os/path/…; kinds mirror
			// upstream Ret tString; boolout/fire stay i32/void).
			if saNodeIsStr(mod + "." + remote) {
				return true
			}
			// crypto Hash 暂存即串缓冲（声明收养为 str 柄；方法位另行分发）。
			if mod == "crypto" && remote == "createHash" {
				return true
			}
		}
		return false
	}
	if ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
		recv := pa.Expression.Text()
		// node 插件串返回（process/crypto 裸全局零参；console/Buffer 走各自的分发）。
		if recv == "process" || recv == "crypto" {
			if saNodeIsStr(recv + "." + pa.Name().Text()) {
				return true
			}
		}
		if recv == "Buffer" && pa.Name().Text() == "concat" {
			return true
		}
		// Deno 插件串返回（直接成员；env 链走两级分发，种判定同表）。
		if recv == "Deno" && saNodeIsStr("Deno."+pa.Name().Text()) {
			return true
		}
	}
	// Deno.env.get 两级串返回（`Deno.env` 为 PropertyAccess 基）。
	if pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression && pa.Name() != nil {
		inner := pa.Expression.AsPropertyAccessExpression()
		if inner.Expression != nil && inner.Expression.Kind == ast.KindIdentifier &&
			inner.Expression.Text() == "Deno" && inner.Name() != nil && inner.Name().Text() == "env" &&
			saNodeIsStr("Deno.env."+pa.Name().Text()) {
			return true
		}
	}
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" &&
		pa.Name() != nil {
		switch pa.Name().Text() {
		case "fromCharCode", "fromCodePoint":
			return true
		}
		return false
	}
	if pa.Name() == nil || !saIsStrMethod(pa.Name().Text()) {
		// crypto Hash 终结即串值（`h.digest()` hex 串柄；update 值位另行拒）。
		if pa.Name() != nil && pa.Name().Text() == "digest" {
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
				if k, ok := scope.types[pa.Expression.Text()]; ok && k == "str" {
					if _, ok := scope.hashAcc[pa.Expression.Text()]; ok {
						return true
					}
				}
			}
		}
		// Map 串值读即串值（`M.get(k)`；值种按建表记，与下标读同形）。
		if pa.Name() != nil && pa.Name().Text() == "get" {
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
				if k, ok := scope.types[pa.Expression.Text()]; ok && k == "map" {
					if scope.mapVals[pa.Expression.Text()] == "str" {
						return true
					}
				}
			}
		}
		// 类方法串返回（`c.get(): string`；声明种为准，体求值走内联）。
		if pa.Name() != nil {
			if mn, ok := saLookupMethod(pa.Expression, pa.Name().Text(), scope); ok {
				if k, ok := saMethodReturnKind(mn); ok && k == "str" {
					return true
				}
			}
		}
		return false
	}
	// i32 `toString()` 即串返回（`String(x)` interp 同形；可证 i32 门内聚）。
	if pa.Name() != nil && pa.Name().Text() == "toString" && pa.QuestionDotToken == nil &&
		saIsToStringableI32(pa.Expression, scope) {
		return true
	}
	return saIsStrExpr(pa.Expression, scope)
}

// saStrCallIsI32 判定串调用是否为 i32 返回（indexOf 系/startsWith 系/
// charCodeAt 系/includes；其余串调用皆为串返回）。
func saStrCallIsI32(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return false
	}
	switch pa.Name().Text() {
	case "charCodeAt", "codePointAt", "indexOf", "lastIndexOf", "startsWith", "endsWith", "includes":
		return saIsStrExpr(pa.Expression, scope)
	}
	return false
}

// split 另行大声拒，不在此列为“未知方法”拒）。
func saIsStrMethod(m string) bool {
	switch m {
	case "charCodeAt", "codePointAt", "indexOf", "lastIndexOf", "startsWith", "endsWith",
		"toLowerCase", "toUpperCase", "repeat", "padStart", "padEnd", "replace", "replaceAll",
		"includes", "charAt", "at", "trim", "trimStart", "trimEnd", "concat",
		"slice", "substring", "substr", "toString":
		return true
	}
	return false
}

// saEvalStr 求串操作数（返回 16 字节句柄）。
func saEvalStr(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil {
		return "", "missing expression"
	}
	switch e.Kind {
	case ast.KindConditionalExpression:
		// 三元串臂（与 return/声明位同核；i32 臂在此拒）。
		t, isStr, msg := saLowerTernaryValue(w, e.AsConditionalExpression(), e, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
		if msg != "" {
			return "", msg
		}
		if !isStr {
			return "", "not a string expression"
		}
		return t, ""
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return saLowerStringLiteral(w, e.Text(), scope, nextTemp), ""
	case ast.KindTemplateExpression:
		return saLowerTemplate(w, e.AsTemplateExpression(), scope, pos, refusals, nextTemp)
	case ast.KindTypeOfExpression:
		// 值位 typeof 具化为种类串（形状证据：封存 lowerTypeof:9171-9239）。
		kind, msg := saTypeofKind(e, scope)
		if msg != "" {
			return "", msg
		}
		return saLowerStringLiteral(w, kind, scope, nextTemp), ""
	case ast.KindTaggedTemplateExpression:
		return saLowerTaggedTemplate(w, e, scope, pos, refusals, nextTemp)
	case ast.KindIdentifier:
		nm := e.Text()
		if k, ok := scope.types[nm]; ok {
			if k == "str" {
				return nm, ""
			}
			return "", nm + " is not a string"
		}
		// 顶层串常量折叠读（具化；非串顶层量沿串门拒；封存 lowerExpr:2775）。
		if text, ok := scope.topConsts[nm]; ok {
			if scope.topStr[nm] {
				return saLowerStringLiteral(w, text, scope, nextTemp), ""
			}
			return "", "not a string expression"
		}
		// 顶层可变串槽读（具化 16 字节头；方法/`.length` 经此自动通；
		// 形状证据：封存 modStrRecv:653-666 + emitModLoadString:941-962）。
		if ms, ok := scope.modVars[nm]; ok && ms.w == "str" {
			return saModLoadStr(w, ms, scope, nextTemp), ""
		}
		if nm == "undefined" {
			return "", "not a string expression"
		}
		return "", "unknown variable " + nm
	case ast.KindCallExpression:
		op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if voidCall {
			return "", "void function call in string position"
		}
		if !saCallIsStr(e.AsCallExpression(), scope) && !saIsArrJoinCall(e.AsCallExpression(), scope) && !saIsDateStrCall(e.AsCallExpression(), scope) {
			return "", "non-string call in string position"
		}
		return op, ""
	case ast.KindElementAccessExpression:
		// 串 Map 下标读（值种按建表记，非串值沿串门拒；`?.` 沿旧门；与 i32 位读同形）。
		ea := e.AsElementAccessExpression()
		if ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
			if k, ok := scope.types[ea.Expression.Text()]; ok && k == "map" {
				if ea.QuestionDotToken != nil {
					return "", "optional map index reads are not lowerable"
				}
				if scope.mapVals[ea.Expression.Text()] == "str" {
					t, msg := saLowerMapIndexLoad(w, ea.Expression.Text(), ea.ArgumentExpression, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					return t, ""
				}
				return "", "map value is not a string"
			}
		}
		// 串元数组元素读（arrStr 标记；槽值即串柄直传，OOB 归零柄；
		// 上游同位把柄按十进制打印系静默错译，本仓正确优先；`?.` 沿旧门）。
		if ea.QuestionDotToken == nil && ea.Expression != nil &&
			ea.Expression.Kind == ast.KindIdentifier && scope.arrStr != nil &&
			scope.arrStr[ea.Expression.Text()] {
			idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if msg := saCheckIntIndex(scope, idx); msg != "" {
				return "", msg
			}
			return saLowerCheckedIndex(w, ea.Expression.Text(), idx, scope.nextLabel, nextTemp), ""
		}
		if ea.QuestionDotToken == nil && saIsStrExpr(ea.Expression, scope) {
			h, msg := saEvalStr(w, ea.Expression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			bp, _ := saExpandStr(w, h, nextTemp)
			return saLowerStrIndexChar(w, bp, idx, scope, nextTemp), ""
		}
		return "", "not a string expression"
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindQuestionQuestionToken &&
			(saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)) {
			// `??` 空合串位（与 i32 槽同形，宽按 ptr；封存 lowerBinary:3182-3206）。
			return saLowerNullishStr(w, be, scope, pos, refusals, nextTemp)
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindPlusToken &&
			(saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)) {
			return saConcatStr(w, be.Left, be.Right, scope, pos, refusals, nextTemp)
		}
		return "", "only + concatenates strings"
	case ast.KindParenthesizedExpression:
		return saEvalStr(w, e.AsParenthesizedExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindAsExpression:
		return saEvalStr(w, e.AsAsExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindSatisfiesExpression:
		return saEvalStr(w, e.AsSatisfiesExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindNonNullExpression:
		return saEvalStr(w, e.AsNonNullExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindTypeAssertionExpression:
		return saEvalStr(w, e.AsTypeAssertion().Expression, scope, pos, refusals, nextTemp)
	case ast.KindPropertyAccessExpression:
		// 类静态串字面量折叠（`C.TYPE` 具化；非串静态沿标识符口径拒；
		// 封存 lowerExpr:8013-8041）。
		pa := e.AsPropertyAccessExpression()
		if pa.Name() != nil {
			// 私有静态串折叠（`C.#S`；非串沿标识符口径拒）。
			if def, key, msg, ok := saPrivStaticKey(pa.Expression, pa.Name().Text(), scope); msg != "" {
				return "", msg
			} else if ok {
				sv := def.statics[key]
				if sv.kind == "str" {
					return saLowerStringLiteral(w, sv.text, scope, nextTemp), ""
				}
				return "", pa.Name().Text() + " is not a string"
			}
			if op, kind, ok := saStaticFold(w, pa.Expression, pa.Name().Text(), scope, nextTemp); ok {
				if kind == "str" {
					return op, ""
				}
				return "", pa.Name().Text() + " is not a string"
			}
			// 类名基静态 str getter 读（`C.gs` 空 this 内联；声明返回种为准；
			// 遮蔽门与 i32 读位同形；形状证据：封存 lowerClassStaticCall 存取器位）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
				if def, ok := scope.classes[pa.Expression.Text()]; ok {
					if _, shadowed := scope.types[pa.Expression.Text()]; !shadowed {
						if _, shadowed := scope.funcs[pa.Expression.Text()]; !shadowed {
							if gn, ok := def.staticGetters[pa.Name().Text()]; ok {
								if k, ok := saMethodReturnKind(gn); ok && k == "str" {
									v, msg := saInlineStaticGetter(w, pa.Expression.Text(), def, pa.Name().Text(), scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
									if msg == "" {
										return v, ""
									}
									return "", msg
								}
							}
						}
					}
				}
			}
			// 命名空间拍扁串读（`N.S` 具化；非串沿串门拒）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil {
				if text, ok := scope.topConsts[pa.Expression.Text()+"."+pa.Name().Text()]; ok {
					if scope.topStr[pa.Expression.Text()+"."+pa.Name().Text()] {
						return saLowerStringLiteral(w, text, scope, nextTemp), ""
					}
					return "", "not a string expression"
				}
				// 命名空间可变串槽读（`N.S` 活值具化；与顶层串槽同门）。
				if ms, ok := scope.modVars[pa.Expression.Text()+"."+pa.Name().Text()]; ok && ms.w == "str" {
					return saModLoadStr(w, ms, scope, nextTemp), ""
				}
			}
			// 串/计算枚举成员读拒（整数成员串位沿既有串门拒）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
				if _, ok := scope.enums[pa.Expression.Text()]; ok {
					if msg, bad := saEnumNonIntMsg(pa.Expression.Text(), pa.Name().Text(), scope); bad {
						return "", msg
					}
				}
			}
			// 实例 str 域读（头指针即串值，临时量已记 str）。
			if saCouldBeInst(pa.Expression, scope) {
				h, def, msg := saInstBase(pa.Expression, scope)
				// map 索引实例基（`m[k].f`；saInstBase 只认标识符/this；
				// 与 i32 读位 2450-2459 同形；否则 nil 解引用崩溃）。
				if msg == "" && def == nil && pa.Expression != nil && (pa.Expression.Kind == ast.KindElementAccessExpression || pa.Expression.Kind == ast.KindCallExpression) {
					h, def, msg = saInstBaseElem(w, pa.Expression, scope, pos, refusals, nextTemp)
				}
				if msg == "" {
					if def == nil {
						return "", "instance base did not resolve to a recorded layout"
					}
					fname := pa.Name().Text()
					if strings.HasPrefix(fname, "#") {
						key, msg := saPrivResolve(def, fname, scope.thisClass)
						if msg != "" {
							return "", msg
						}
						fname = key
					}
					if _, ok := def.offsets[fname]; ok && def.fkinds[fname] == "str" {
						t, msg := saLowerClassFieldLoad(w, h, def, fname, scope, nextTemp)
						if msg == "" {
							return t, ""
						}
						return "", msg
					}
					// str getter 内联（声明返回种为准；求值走内联体）。
					if gn, ok := def.getters[pa.Name().Text()]; ok {
						if k, ok := saMethodReturnKind(gn); ok && k == "str" {
							v, msg := saInlineGetter(w, h, def, pa.Name().Text(), scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
							if msg == "" {
								return v, ""
							}
							return "", msg
						}
					}
				}
			}
			// 嵌套串链读（`q.p.s` 经内层句柄；叶子须 str，非串沿旧门；
			// 与 i32 链读同形；形状证据：封存 saChainBase）。
			if pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression && pa.Name() != nil {
				if ch, cdef, msg := saChainBase(w, pa.Expression, scope, pos, refusals, nextTemp); msg == "" {
					if _, ok := cdef.offsets[pa.Name().Text()]; ok && cdef.fkinds[pa.Name().Text()] == "str" {
						t, msg := saLowerClassFieldLoad(w, ch, cdef, pa.Name().Text(), scope, nextTemp)
						if msg == "" {
							return t, ""
						}
						return "", msg
					}
				}
			}
		}
		return "", "not a string expression"
	default:
		return "", "not a string expression"
	}
}

// saToSlice 任一可文本化操作数转切片（串直通；i32/bool 经 interp；其余拒）。
// 供模板/console/String() 共用（renderInterpValue 哲学：同 sa_fmt 现货）。
func saToSlice(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if saIsStrValue(e, scope) {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	if saCouldBeInst(e, scope) {
		// 实例句柄禁文本化（指针误作十进制打印；模板/console/String() 共用此口；铁律 4）。
		return "", "instance value in string position"
	}
	// date 串方法直通串位（toISOString/toString 系）。
	if e != nil && e.Kind == ast.KindCallExpression && saIsDateStrCall(e.AsCallExpression(), scope) {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	// 数组 join 回串（串位；形状证据同 saEvalStr 调用位）。
	if e != nil && e.Kind == ast.KindCallExpression && saIsArrJoinCall(e.AsCallExpression(), scope) {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	// f64 函数调用结果经 @sa_fmt_f64_into 落文本切片（精度 6；与上游
	// renderInterpValue f64 分支同形；非 f64 调用沿既有各门）。
	if e != nil && e.Kind == ast.KindCallExpression {
		if k, ok := saCallRetKind(e.AsCallExpression(), scope); ok && k == "f64" {
			op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if voidCall {
				return "", "void call in string position"
			}
			return saRenderInterpF64(w, op, scope, nextTemp), ""
		}
	}
	// date millis 经 i64 直插值（无 sext；窄化不发生，millis 原样入 fmt）。
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "date" {
			return saRenderInterp64(w, e.Text(), scope, nextTemp), ""
		}
	}
	if e != nil && e.Kind == ast.KindCallExpression {
		if k, ok := saDateCallKind(e.AsCallExpression(), scope); ok && k == "date" {
			op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			if voidCall {
				return "", "void call in string position"
			}
			return saRenderInterp64(w, op, scope, nextTemp), ""
		}
	}
	var op string
	if saIsF64Operand(e, scope) {
		// f64 经 @sa_fmt_f64_into 落文本切片（精度 6；上游 console.log(f64)
		// 同形；i32/bool/串位不动）。
		var msg string
		op, msg = saEvalF64(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		return saRenderInterpF64(w, op, scope, nextTemp), ""
	}
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "bool" {
			var msg string
			op, msg = saEvalBool(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		} else {
			var msg string
			op, msg = saEvalI32(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		}
	} else {
		var msg string
		op, msg = saEvalI32(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			// bool 字面走 saEvalBool 兜底（saEvalI32 未必收 true/false）。
			op, msg = saEvalBool(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		}
	}
	return saRenderInterp(w, op, scope, nextTemp), ""
}

// saRenderInterp64 i64 操作数经 @sa_fmt_i64_into 落文本切片（date millis
// 直用，无 sext；形状证据同 saRenderInterp）。
func saRenderInterp64(w printer.EmitTextWriter, v string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/fmt.sai")
	numbuf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 64\n", numbuf))
	numlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", numlen))
	rc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_i64_into(%s, 10, %s, 64, &%s)\n", rc, v, numbuf, numlen))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", nlen, numlen))
	vslice := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", vslice))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", vslice, numbuf))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", vslice, nlen))
	saOwnTemp(scope, numbuf)
	saOwnTemp(scope, numlen)
	saOwnTemp(scope, rc)
	saOwnTemp(scope, vslice)
	saReleaseOwnedTemp(w, scope, rc)
	saReleaseOwnedTemp(w, scope, nlen)
	return vslice
}

// saRenderInterp 整数操作数经 sext + @sa_fmt_i64_into 落文本切片
// （形状证据：封存 renderInterpValue:8879-8900；bool 到此已是 0/1）。
func saRenderInterp(w printer.EmitTextWriter, v string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/fmt.sai")
	wide := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sext %s as i64\n", wide, v))
	numbuf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 64\n", numbuf))
	numlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", numlen))
	rc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_i64_into(%s, 10, %s, 64, &%s)\n", rc, wide, numbuf, numlen))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", nlen, numlen))
	vslice := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", vslice))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", vslice, numbuf))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", vslice, nlen))
	saOwnTemp(scope, numbuf)
	saOwnTemp(scope, numlen)
	saOwnTemp(scope, rc)
	saOwnTemp(scope, vslice)
	saReleaseOwnedTemp(w, scope, rc)
	saReleaseOwnedTemp(w, scope, nlen)
	return vslice
}

// saRenderInterpF64 f64 操作数经 @sa_fmt_f64_into（精度 6）落文本切片
// （形状证据：封存 renderInterpValue:8852-8877 f64 分支逐行同形）。
func saRenderInterpF64(w printer.EmitTextWriter, v string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/fmt.sai")
	numbuf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 64\n", numbuf))
	numlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", numlen))
	rc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_f64_into(%s, 6, %s, 64, &%s)\n", rc, v, numbuf, numlen))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", nlen, numlen))
	vslice := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", vslice))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", vslice, numbuf))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", vslice, nlen))
	saOwnTemp(scope, numbuf)
	saOwnTemp(scope, numlen)
	saOwnTemp(scope, rc)
	saOwnTemp(scope, vslice)
	saReleaseOwnedTemp(w, scope, rc)
	saReleaseOwnedTemp(w, scope, nlen)
	return vslice
}

// saConcatSlices 两切片经 @sa_string_concat 合并后读回新切片
// （形状证据：封存 concatSlices:8909-8930；需 string.sai + fmt.sai）。
func saConcatSlices(w printer.EmitTextWriter, left, right string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/string.sai")
	scope.addImport("sa_std/fmt.sai")
	lptr, llen := saExpandStr(w, left, nextTemp)
	rptr, rlen := saExpandStr(w, right, nextTemp)
	obuf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_string_concat(%s, %s, %s, %s)\n", obuf, lptr, llen, rptr, rlen))
	optr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_buffer_data(%s)\n", optr, obuf))
	olen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_buffer_len(%s)\n", olen, obuf))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, optr))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, olen))
	saOwnTemp(scope, obuf)
	saOwnTemp(scope, optr)
	saOwnTemp(scope, olen)
	saOwnTemp(scope, out)
	for _, t := range []string{lptr, llen, rptr, rlen, optr, olen, obuf} {
		saReleaseOwnedTemp(w, scope, t)
	}
	saReleaseOwnedTemp(w, scope, left)
	saReleaseOwnedTemp(w, scope, right)
	return out
}

// saConcatStr `+` 拼接（两侧须皆为串位；混合数值须显式 String()，
// 子集门，大声拒——封存 lowerBinary 无数值隐式强制证据）。
func saConcatStr(w printer.EmitTextWriter, l, r *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if !saIsStrValue(l, scope) || !saIsStrValue(r, scope) {
		return "", "string + needs string operands on both sides (use String(x))"
	}
	lh, msgL := saEvalStr(w, l, scope, pos, refusals, nextTemp)
	if msgL != "" {
		return "", msgL
	}
	rh, msgR := saEvalStr(w, r, scope, pos, refusals, nextTemp)
	if msgR != "" {
		return "", msgR
	}
	return saConcatSlices(w, lh, rh, scope, nextTemp), ""
}

// saIsIntWord reports int-valued operands (integer literals, bools,
// i32/bool bindings and module slots; float literals and handles stay out).
func saIsIntWord(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		return !saIsFloatLit(e.Text())
	case ast.KindTrueKeyword, ast.KindFalseKeyword:
		return true
	case ast.KindIdentifier:
		if k, ok := scope.types[e.Text()]; ok {
			return k == "i32" || k == "bool"
		}
		if ms, ok := scope.modVars[e.Text()]; ok {
			return ms.w == "i32"
		}
		if text, ok := scope.topConsts[e.Text()]; ok && !scope.topStr[e.Text()] {
			return saIsIntLitText(text)
		}
		return false
	default:
		return false
	}
}

// saLowerFloatConvert lowers parseFloat(s)/Number(x) to f64 (string args via
// the sa_parse_float wheel; integer/boolean args via sitofp; shape evidence:
// upstream sa_parse_float call sites; empty/multi args and float literals refuse).
func saLowerFloatConvert(w printer.EmitTextWriter, name string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 1 || argNodes[0] == nil {
		return "", name + " takes exactly 1 argument"
	}
	a := argNodes[0]
	if name == "Number" && saIsIntWord(a, scope) {
		dd, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sitofp %s\n", t, dd))
		scope.types[t] = "f64"
		return t, ""
	}
	if !saIsStrValue(a, scope) {
		return "", name + " takes a string or integer argument"
	}
	h, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	bp, bl := saExpandStr(w, h, nextTemp)
	scope.addImport("sa_std/string.sai")
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_parse_float(&%s, %s)\n", t, bp, bl))
	scope.types[t] = "f64"
	return t, ""
}

// saLowerParseIntArgs parseInt/Number.parseInt 实参（串求值 + 基数门；缺省/
// 字面量 10 即十进制扫描，字面量 2-36 即通用扫描；非法/变量基数大声拒。
// R2 回迁映射：扫描语义由 `sci/sa_std/ts_string.sa` `@ts_str_parse_int`
// 实现（十进制传 radix=10、start=0，与旧十进制扫描逐指令同形；16 进制
// 0x 剥离经 `@ts_str_hex_i0` 折叠为起始位），本侧只做 import + 调用 + 归属。
func saLowerParseIntArgs(w printer.EmitTextWriter, argNodes []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if len(argNodes) < 1 || len(argNodes) > 2 {
		return "", "parseInt takes 1 argument (plus optional radix 10)"
	}
	s, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	radix := 10
	if len(argNodes) == 2 {
		r := argNodes[1]
		if r == nil || r.Kind != ast.KindNumericLiteral {
			return "", "parseInt radix must be a literal 2-36"
		}
		var v int
		if _, err := fmt.Sscanf(r.Text(), "%d", &v); err != nil || v < 2 || v > 36 {
			return "", "parseInt radix must be a literal 2-36"
		}
		radix = v
	}
	bp, bl := saExpandStr(w, s, nextTemp)
	scope.addImport("sa_std/ts_string.sa")
	start := "0"
	stOwned := ""
	if radix == 16 {
		st := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_str_hex_i0(%s, %s)\n", st, bp, bl))
		saOwnTemp(scope, st)
		start = st
		stOwned = st
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_str_parse_int(%s, %s, %d, %s)\n", t, bp, bl, radix, start))
	saOwnTemp(scope, t)
	if stOwned != "" {
		saReleaseOwnedTemp(w, scope, stOwned)
	}
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, t))
	saReleaseOwnedTemp(w, scope, t)
	return out, ""
}

// saLowerNullishStr `??` 空合串槽（左非零句柄直通，否则右惰性求值；子集 null 即 0 句柄；
// 串位；形状证据：封存 lowerBinary:3182-3206 + 本仓 saLowerNullish i32 槽同形，槽宽同 8，值宽按 ptr）。
func saLowerNullishStr(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	l, msg := saEvalStr(w, be.Left, scope, pos, refusals, nextTemp)
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
	// 右臂快照点（臂内惰性求值新建的归属临时量一律随槽消费，否则返前释放
	// 在直通臂上引用未定义寄存器；具名绑定非臂内定义，留归属；左值无条件
	// 求值，无此问题）。
	mark := len(scope.ownOrder)
	r, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, r))
	for _, nm := range scope.ownOrder[mark:] {
		if saIsTempOp(nm) {
			saConsumeOwn(scope, nm)
		}
	}
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	saOwnTemp(scope, out)
	return out, ""
}

// saStringContentEq 串内容相等（等长 + 零偏 indexOf 命中；negate 取反。
// 形状证据：封存 stringContentEq:9146-9166）。
func saStringContentEq(w printer.EmitTextWriter, l, r string, negate bool, scope *saScope, nextTemp *int) string {
	// R2 回迁映射：内容判等语义由 `sci/sa_std/ts_string.sa` `@ts_str_equals`
	// 实现（等长 + 零偏命中；negate 由调用点折叠），本侧只做 import + 调用。
	scope.addImport("sa_std/ts_string.sa")
	lp, ll := saExpandStr(w, l, nextTemp)
	rp, rl := saExpandStr(w, r, nextTemp)
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_str_equals(%s, %s, %s, %s)\n", t, lp, ll, rp, rl))
	// 调用结果归属(返前释放；上游 ownTemp 同形).
	saOwnTemp(scope, t)
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if negate {
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", out, t))
	} else {
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, t))
	}
	saReleaseOwnedTemp(w, scope, t)
	return out
}

// saLowerStrDecl lowering 字符串声明（字面量具化；同类句柄拷贝；
// 缺 init 的 let 绑空句柄零值（`s = 0`，后赋重绑；形状证据：封存
// lowerVarDeclList:1405-1434）；const 缺 init/非串初值皆大声拒）。
func saLowerStrDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, name string, isConst bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer == nil {
		ln, col := pos(d.Pos())
		if isConst {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
			return false
		}
		w.Write(fmt.Sprintf("  %s = 0\n", name))
		scope.types[name] = "str"
		saDeclarePlain(scope, name)
		return true
	}
	h, msg := saEvalStr(w, vd.Initializer, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported string initializer: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, h))
	scope.types[name] = "str"
	saConsumeOwn(scope, h)
	saDeclareOwned(scope, name)
	saAdoptHash(scope, name, vd.Initializer)
	return true
}

// saIsToStringableI32 报告表达式是否可证 i32（数值字面量/i32 标识（局部
// 遮蔽优先）/i32 模块槽/算术位运算一元递归；bool 字面量/标识/比较逻辑一律否；
// 不落字，`toString` 数值门专用）。
func saIsToStringableI32(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		return true
	case ast.KindTrueKeyword, ast.KindFalseKeyword:
		return false
	case ast.KindIdentifier:
		nm := e.Text()
		if k, ok := scope.types[nm]; ok {
			return k == "i32"
		}
		if ms, ok := scope.modVars[nm]; ok {
			return ms.w == "i32"
		}
		// 顶层数值折叠（未被赋值名恒折叠；纯整数字面文本方收，表达式文本沿旧门）。
		if text, ok := scope.topConsts[nm]; ok && !scope.topStr[nm] {
			return saIsIntLitText(text)
		}
		return false
	case ast.KindParenthesizedExpression:
		return saIsToStringableI32(e.AsParenthesizedExpression().Expression, scope)
	case ast.KindPrefixUnaryExpression:
		ue := e.AsPrefixUnaryExpression()
		if ue == nil {
			return false
		}
		switch ue.Operator {
		case ast.KindMinusToken, ast.KindPlusToken, ast.KindTildeToken:
			return saIsToStringableI32(ue.Operand, scope)
		}
		return false
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken == nil {
			return false
		}
		switch be.OperatorToken.Kind {
		case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken,
			ast.KindSlashToken, ast.KindPercentToken, ast.KindAsteriskAsteriskToken,
			ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken,
			ast.KindGreaterThanGreaterThanGreaterThanToken, ast.KindAmpersandToken,
			ast.KindBarToken, ast.KindCaretToken:
			return saIsToStringableI32(be.Left, scope) && saIsToStringableI32(be.Right, scope)
		}
		return false
	}
	return false
}

// saIsIntLitText 报告文本是否为纯整数字面量（可选前导负号；`toString`
// 折叠门专用，不作数值语义）。
func saIsIntLitText(text string) bool {
	if text == "" {
		return false
	}
	i := 0
	if text[0] == '-' {
		i = 1
	}
	if i >= len(text) {
		return false
	}
	for ; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// saLowerStrCall lowering 串调用：String(x)/String.from*/串方法/同文件 string 函数。
func saLowerStrCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	if ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "String" {
		args := []*ast.Node{}
		if ce.Arguments != nil {
			args = ce.Arguments.Nodes
		}
		if len(args) != 1 {
			return "", false, "String(x) needs 1 argument"
		}
		// String(x)：串直通，数值经 interp（形状证据：封存 lowerCall:3885-3900）。
		h, msg := saToSlice(w, args[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		return h, false, ""
	}
	if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" &&
			pa.Name() != nil {
			switch pa.Name().Text() {
			case "fromCharCode", "fromCodePoint":
				args := []*ast.Node{}
				if ce.Arguments != nil {
					args = ce.Arguments.Nodes
				}
				if len(args) < 1 {
					// 零参即空串（上游同过；JS `""`）。
					return saLowerStringLiteral(w, "", scope, nextTemp), false, ""
				}
				// 原语回 BUFFER 句柄（u64，现货签名 `-> u64`），经 data/len
				// unwrap 成 16 字节串句柄；缓冲作头读（u64 作片读会段错；
				// 形状证据：封存 lowerCall:3811-3836；现货 sa_std/string.sai
				// from_char_code/from_code_point + sa_std/fmt.sai buffer_data/len）。
				// 多参逐字具化后 concat（轮子一元，上游多参直调系静默截断，
				// 本仓组装值正确；node 镜算）。
				sym := "sa_string_from_char_code"
				if pa.Name().Text() == "fromCodePoint" {
					sym = "sa_string_from_code_point"
				}
				scope.addImport("sa_std/string.sai")
				scope.addImport("sa_std/fmt.sai")
				acc := ""
				for _, an := range args {
					if an == nil {
						return "", false, "String." + pa.Name().Text() + " needs 1 argument"
					}
					v, msg := saEvalI32(w, an, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", false, msg
					}
					hbuf := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", hbuf, sym, v))
					hptr := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = call @sa_fmt_buffer_data(%s)\n", hptr, hbuf))
					hlen := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = call @sa_fmt_buffer_len(%s)\n", hlen, hbuf))
					t := fmt.Sprintf("t_%d", *nextTemp)
					*nextTemp++
					w.Write(fmt.Sprintf("  %s = alloc 16\n", t))
					w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", t, hptr))
					w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", t, hlen))
					acc = saConcatSlicesOpt(w, acc, t, scope, nextTemp)
				}
				return acc, false, ""
			}
			return "", false, "unsupported String method " + pa.Name().Text()
		}
		if pa.QuestionDotToken != nil {
			return "", false, "optional member call not lowerable"
		}
		// i32 `toString()`（`String(x)` interp 同形；bool 拼写 `true/false`
		// 与数值 `0/1` 殊形，只收可证 i32 形，余下沿旧门）。
		if pa.Name() != nil && pa.Name().Text() == "toString" {
			args := []*ast.Node{}
			if ce.Arguments != nil {
				args = ce.Arguments.Nodes
			}
			if len(args) != 0 {
				return "", false, "toString takes 0 arguments"
			}
			if !saIsToStringableI32(pa.Expression, scope) {
				return "", false, "toString receiver must be a number (booleans need true/false spelling)"
			}
			v, msg := saEvalI32(w, pa.Expression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			if smsg := saCheckI32Value(scope, v); smsg != "" {
				return "", false, smsg
			}
			return saRenderInterp(w, v, scope, nextTemp), false, ""
		}
		recv, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		if pa.Name() == nil {
			return "", false, "missing method name"
		}
		return saLowerStrMethod(w, recv, pa.Name().Text(), ce, scope, pos, refusals, nextTemp)
	}
	// 同文件 string 函数走通用调用核（saEvalCall 标识符分支按签名表落字）；
	// 此处仅处理 String()/String.from*/串方法。
	return "", false, "not a string call"
}

// saLowerStringSplit lowering `s.split(sep[, limit])`（串元数组；封存 lowerStringSplit:7469-7527
// 全形：indexOf 扫描 + 切片装配 + 逐段 push + 尾段；空头 16 字节零柄起，串元标记）。
// limit 按 lib.es5.d.ts `split(separator, limit?)` 截断（0 即空；负数经 ToUint32 视为不限，
// 以 i32 上确界归一；封存上游忽略 limit 系静默错码，本仓正确优先，step109 同例有意分歧）。
func saLowerStringSplit(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/string.sai")
	pa := ce.Expression.AsPropertyAccessExpression()
	recv, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) < 1 {
		return "", "split needs 1 argument"
	}
	sep, msg := saEvalStr(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	lim := ""
	if len(args) >= 2 {
		limRaw, msg := saEvalI32(w, args[1], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// 负 limit 经 ToUint32 即大数（node 实证 -1 不限长），归一为 i32 上确界；
		// EXPAND SELECT 系 control.sal 宏（三元值核同形，用时方进口，单参零漂移）。
		scope.addImport("sa_std/control.sal")
		neg := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, 0\n", neg, limRaw))
		lim = fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  EXPAND SELECT %s, %s, 2147483647, %s\n", lim, neg, limRaw))
	}
	bp, bl := saExpandStr(w, recv, nextTemp)
	sp, sl := saExpandStr(w, sep, nextTemp)
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", h))
	w.Write(fmt.Sprintf("  store %s + 8, 0 as u64\n", h))
	saOwnTemp(scope, h)
	saMarkArrStr(scope, h)
	start := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", start))
	topL := fmt.Sprintf("L_sp_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_sp_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_sp_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	// 有 limit 时循环顶按已收段数截断（满即弃尾直返），无 limit 保持原三标号零漂移。
	workL, tailL, doneL := "", "", ""
	if lim != "" {
		workL = fmt.Sprintf("L_sp_work_%d", *scope.nextLabel)
		*scope.nextLabel++
		tailL = fmt.Sprintf("L_sp_tail_%d", *scope.nextLabel)
		*scope.nextLabel++
		doneL = fmt.Sprintf("L_sp_done_%d", *scope.nextLabel)
		*scope.nextLabel++
	}
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", topL))
	if lim != "" {
		cnt := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as i32\n", cnt, h))
		room := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", room, cnt, lim))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", room, workL, doneL))
		w.Write(fmt.Sprintf("%s:\n", workL))
	}
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_string_index_of(%s, %s, %s, %s, %s)\n", idx, bp, bl, sp, sl, start))
	// 循环内调用结果不登记不释放（dedup 体内 scan 同形； draining 只释直线头）。
	found := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, -1\n", found, idx))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", found, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	pp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", pp, bp, start))
	pl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", pl, idx, start))
	part := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", part))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", part, pp))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", part, pl))
	// 段柄入槽即数组载荷（字面量串元同形：只头登记，段不单独释放）。
	saLowerArrayPush(w, h, part, scope, nextTemp)
	nstart := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", nstart, idx, sl))
	w.Write(fmt.Sprintf("  %s = %s\n", start, nstart))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	if lim != "" {
		cnt2 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as i32\n", cnt2, h))
		room2 := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", room2, cnt2, lim))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", room2, tailL, doneL))
		w.Write(fmt.Sprintf("%s:\n", tailL))
	}
	pp2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", pp2, bp, start))
	pl2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", pl2, bl, start))
	tail := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", tail))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", tail, pp2))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", tail, pl2))
	saLowerArrayPush(w, h, tail, scope, nextTemp)
	if lim != "" {
		w.Write(fmt.Sprintf("%s:\n", doneL))
	}
	return h, ""
}

// saLowerStrMethod lowering 串方法全集（投影表 stdlib.go:95-105 + 封存
// lowerStringMethod:7173-7386；split 需串元数组，超 i32 槽模型，大声拒）。
// saLowerStrIndexChar 取单字柄（`charAt`/`s[i]` 同形；无界检查与既有
// charAt 一字之差无：越界未定义，调用方禁另行加塞语义）。
// R2-4 回迁映射：拼柄语义由 `sci/sa_std/ts_string.sa` `@ts_str_char_at`
// 实现，本侧只做 import + 单 call + 归属登记；形状证据见 ts_string.sa 头注
// + 封存 lowerStringCharAt（saemit.go:7270-7280）。
func saLowerStrIndexChar(w printer.EmitTextWriter, bp, sel string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/ts_string.sa")
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_str_char_at(%s, %s)\n", out, bp, sel))
	// 取字柄归属(返前释放；上游同形).
	saOwnTemp(scope, out)
	return out
}

func saLowerStrMethod(w printer.EmitTextWriter, recv, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	scope.addImport("sa_std/string.sai")
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	strArg := func(i int) (string, string) {
		if i >= len(args) {
			return "", "missing argument"
		}
		return saEvalStr(w, args[i], scope, pos, refusals, nextTemp)
	}
	intArg := func(i int) (string, string) {
		if i >= len(args) {
			return "", "missing argument"
		}
		return saEvalI32(w, args[i], scope, pos, refusals, nextTemp)
	}
	bp, bl := saExpandStr(w, recv, nextTemp)
	call1 := func(sym string, extra ...string) string {
		all := append([]string{bp, bl}, extra...)
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", t, sym, strings.Join(all, ", ")))
		// extern 调用结果归属(用后/返前释放；上游 callStr ownTemp 同形).
		saOwnTemp(scope, t)
		return t
	}
	switch method {
	case "charCodeAt":
		if len(args) != 1 {
			return "", false, "charCodeAt needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_string_code_point_at(%s, %s, %s)\n", t, bp, bl, a))
		// 码点结果归属(返前释放；上游同形).
		saOwnTemp(scope, t)
		return t, false, ""
	case "codePointAt":
		if len(args) != 1 {
			return "", false, "codePointAt needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		return call1("sa_string_code_point_at", a), false, ""
	case "indexOf", "lastIndexOf":
		if len(args) < 1 {
			return "", false, method + " needs 1 argument"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		from := "0"
		if len(args) > 1 {
			var msg string
			from, msg = intArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		sym := "sa_string_index_of"
		if method == "lastIndexOf" {
			sym = "sa_string_last_index_of"
		}
		return call1(sym, np, nl, from), false, ""
	case "startsWith", "endsWith":
		if len(args) != 1 {
			return "", false, method + " needs 1 argument"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		sym := "sa_string_starts_with"
		if method == "endsWith" {
			sym = "sa_string_ends_with"
		}
		return call1(sym, np, nl), false, ""
	case "toLowerCase":
		return call1("sa_string_to_lower_ascii"), false, ""
	case "toUpperCase":
		return call1("sa_string_to_upper_ascii"), false, ""
	case "repeat":
		if len(args) != 1 {
			return "", false, "repeat needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		return call1("sa_string_repeat", a), false, ""
	case "padStart", "padEnd":
		if len(args) < 1 {
			return "", false, method + " needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		padArg := saLowerStringLiteral(w, " ", scope, nextTemp)
		if len(args) > 1 {
			var msg string
			padArg, msg = strArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		pp, pl := saExpandStr(w, padArg, nextTemp)
		sym := "sa_string_pad_start"
		if method == "padEnd" {
			sym = "sa_string_pad_end"
		}
		return call1(sym, a, pp, pl), false, ""
	case "replace", "replaceAll":
		need := 2
		if method == "replace" {
			need = -1
		}
		if (need == -1 && len(args) != 2 && len(args) != 3) || (need == 2 && len(args) != 2) {
			return "", false, method + " needs 2 arguments"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		rp_, msg := strArg(1)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		rp, rl := saExpandStr(w, rp_, nextTemp)
		all := "0"
		if method == "replaceAll" {
			all = "1"
		} else if len(args) == 3 {
			var msg string
			all, msg = intArg(2)
			if msg != "" {
				return "", false, msg
			}
		}
		return call1("sa_string_replace", np, nl, rp, rl, all), false, ""
	case "includes":
		if len(args) < 1 {
			return "", false, "includes needs 1 argument"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		from := "0"
		if len(args) > 1 {
			var msg string
			from, msg = intArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		idx := call1("sa_string_index_of", np, nl, from)
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = ne %s, -1\n", out, idx))
		return out, false, ""
	case "charAt", "at":
		if len(args) != 1 {
			return "", false, method + " needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		sel := a
		if method == "at" {
			// 负下标自末端起（无分支形；形状证据：封存 lowerStringMethod:7288-7295）。
			isneg := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, 0\n", isneg, a))
			adj := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = mul %s, %s\n", adj, bl, isneg))
			sel = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", sel, a, adj))
		}
		return saLowerStrIndexChar(w, bp, sel, scope, nextTemp), false, ""
	case "trim", "trimStart", "trimEnd":
		// ascii 三件套合成（形状证据：封存 lowerStringMethod:7304-7335；
		// H14 实锤：旧形 `end_len(orig) - start` 全空串下溢；
		// 改 sci 自家 STR_TRIM_ASCII 两步合成：先 trimStart 得中片，
		// 再对中片 trimEnd；trimStart 本形已对（`bl - start`），分支保留）。
		if method == "trimEnd" {
			endlen := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_str_trim_ascii_end_len(%s, %s)\n", endlen, bp, bl))
			saOwnTemp(scope, endlen)
			out := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, bp))
			w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, endlen))
			saOwnTemp(scope, out)
			return out, false, ""
		}
		start := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_str_trim_ascii_start_index(%s, %s)\n", start, bp, bl))
		saOwnTemp(scope, start)
		midlen := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sub %s, %s\n", midlen, bl, start))
		midptr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", midptr, bp, start))
		nptr, nlen := midptr, midlen
		if method != "trimStart" {
			endlen := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_str_trim_ascii_end_len(%s, %s)\n", endlen, midptr, midlen))
			saOwnTemp(scope, endlen)
			nlen = endlen
		}
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, nptr))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, nlen))
		// 修剪柄归属(返前释放；上游同位缺失，上游另有空操作数错).
		saOwnTemp(scope, out)
		return out, false, ""
	case "concat":
		// 逐片折叠 @sa_string_concat（形状证据：封存 lowerStringMethod:7336-7347；
		// 注意此处直接折叠缓冲柄，与 + 拼接的读回形不同，各守其源）。
		acc := recv
		for i := range args {
			n, msg := strArg(i)
			if msg != "" {
				return "", false, msg
			}
			np, nl := saExpandStr(w, n, nextTemp)
			abp, abl := saExpandStr(w, acc, nextTemp)
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_string_concat(%s, %s, %s, %s)\n", t, abp, abl, np, nl))
			acc = t
		}
		return acc, false, ""
	case "slice", "substring", "substr":
		// R2 回迁映射：钳位子切片语义由 `sci/sa_std/ts_string.sa`
		// `@ts_str_slice` 实现（缺省 end/ substr 加长由调用点折叠），
		// 本侧只做 import + 调用 + 归属。
		if len(args) < 1 {
			return "", false, method + " needs 1 argument"
		}
		a0, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		end := bl
		if len(args) > 1 {
			var msg string
			end, msg = intArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		if method == "substr" {
			nend := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", nend, a0, end))
			end = nend
		}
		sub := "0"
		if method == "substring" {
			sub = "1"
		}
		scope.addImport("sa_std/ts_string.sa")
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @ts_str_slice(%s, %s, %s, %s, %s)\n", out, bp, bl, a0, end, sub))
		// 切片柄归属(返前释放；extern 结果同口径).
		saOwnTemp(scope, out)
		return out, false, ""
	case "split":
		return "", false, "split needs string arrays (beyond i32 slots)"
	case "toString":
		return recv, false, ""
	}
	return "", false, "unsupported string method " + method
}

// saRawTemplateText 取模板片 raw 文本（转义不煮；NoSub 由源码切片，
// 解析器不填其 RawText；形状证据：封存 rawTemplateText:8742-8753）。
func saRawTemplateText(n *ast.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case ast.KindTemplateHead:
		return n.AsTemplateHead().RawText
	case ast.KindTemplateMiddle:
		return n.AsTemplateMiddle().RawText
	case ast.KindTemplateTail:
		return n.AsTemplateTail().RawText
	default:
		return n.Text()
	}
}

// saLowerTaggedTemplate lowering 标签模板（`String.raw` 不煮：raw 片 +
// 常规渲染插值逐片拼接；其余标签大声拒；形状证据：封存 lowerTaggedTemplate:8772-8783）。
func saLowerTaggedTemplate(w printer.EmitTextWriter, n *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	tt := n.AsTaggedTemplateExpression()
	if tt.Tag != nil && tt.Tag.Kind == ast.KindPropertyAccessExpression {
		pa := tt.Tag.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" &&
			pa.Name() != nil && pa.Name().Text() == "raw" {
			return saLowerRawTemplate(w, tt.Template, scope, pos, refusals, nextTemp)
		}
	}
	ln, col := pos(n.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "tagged templates are not lowerable (tag functions have no first-class value; String.raw is the only supported tag)"})
	return "", "tagged templates are not lowerable (tag functions have no first-class value; String.raw is the only supported tag)"
}

// saLowerRawTemplate lowering `String.raw` 模板（raw 头尾 + 插值渲染逐片拼接；
// NoSub 经源码切片（位字节精确）；形状证据：封存 lowerRawTemplate:8787-8821 +
// rawNoSubText:8759-8765）。
func saLowerRawTemplate(w printer.EmitTextWriter, tpl *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if tpl != nil && tpl.Kind == ast.KindNoSubstitutionTemplateLiteral {
		p, en := tpl.Pos(), tpl.End()
		if p < 0 || en > len(scope.src) || en-p < 2 || scope.src[p] != '`' {
			ln, col := pos(tpl.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "String.raw literal has no recoverable source text"})
			return "", "String.raw literal has no recoverable source text"
		}
		return saLowerStringLiteral(w, scope.src[p+1:en-1], scope, nextTemp), ""
	}
	if tpl == nil || tpl.Kind != ast.KindTemplateExpression {
		ln, col := pos(tpl.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "tagged template shape is not lowerable"})
		return "", "tagged template shape is not lowerable"
	}
	tp := tpl.AsTemplateExpression()
	scope.addImport("sa_std/string.sai")
	scope.addImport("sa_std/fmt.sai")
	var acc string
	if tp.Head != nil {
		acc = saLowerStringLiteral(w, saRawTemplateText(tp.Head), scope, nextTemp)
	} else {
		acc = saLowerStringLiteral(w, "", scope, nextTemp)
	}
	if tp.TemplateSpans != nil {
		for _, sp := range tp.TemplateSpans.Nodes {
			span := sp.AsTemplateSpan()
			part, msg := saToSlice(w, span.Expression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			acc = saConcatSlices(w, acc, part, scope, nextTemp)
			tail := ""
			if span.Literal != nil {
				tail = saRawTemplateText(span.Literal)
			}
			if tail != "" {
				tailH := saLowerStringLiteral(w, tail, scope, nextTemp)
				acc = saConcatSlices(w, acc, tailH, scope, nextTemp)
			}
		}
	}
	return acc, ""
}

// saLowerTemplate 模板字面量（头 +  spans 插值 + tails 逐片拼接；
// 形状证据：封存 lowerTemplate:8823-8847；插值仅 i32/bool/串，f64 拒）。
func saLowerTemplate(w printer.EmitTextWriter, tp *ast.TemplateExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/string.sai")
	scope.addImport("sa_std/fmt.sai")
	head := ""
	if tp.Head != nil {
		head = tp.Head.Text()
	}
	acc := saLowerStringLiteral(w, head, scope, nextTemp)
	if tp.TemplateSpans != nil {
		for _, sp := range tp.TemplateSpans.Nodes {
			span := sp.AsTemplateSpan()
			part, msg := saToSlice(w, span.Expression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			acc = saConcatSlices(w, acc, part, scope, nextTemp)
			tail := ""
			if span.Literal != nil {
				tail = span.Literal.Text()
			}
			if tail != "" {
				tailH := saLowerStringLiteral(w, tail, scope, nextTemp)
				acc = saConcatSlices(w, acc, tailH, scope, nextTemp)
			}
		}
	}
	return acc, ""
}

// saLowerConsoleLog `console.log(...)`（操作数经 interp 转切片，空格分隔，
// 末尾换行；形状证据：封存 lowerConsoleLog:7862-7887；console.error 走
// node 插件后端，本薄口大声拒）。
func saLowerConsoleLog(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (bool, string) {
	scope.addImport("sa_std/io/print.sai")
	scope.addImport("sa_std/fmt.sai")
	scope.addImport("sa_std/string.sai")
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	for i, a := range argNodes {
		if i > 0 {
			seg := saLowerStringLiteral(w, " ", scope, nextTemp)
			bp, bl := saExpandStr(w, seg, nextTemp)
			w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
			// 段头即释（字面量头为归属临时量；具名句柄非 temp 天然跳过；
			// 臂域 exit 截断归属表，尾释覆盖不到，此处即释为唯一释放点；
			// 封存上游臂内实发 call 后 `!t` 同形）。
			saReleaseOwnedTemp(w, scope, seg)
		}
		seg, msg := saToSlice(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return false, msg
		}
		bp, bl := saExpandStr(w, seg, nextTemp)
		w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
		saReleaseOwnedTemp(w, scope, seg)
	}
	seg := saLowerStringLiteral(w, "\n", scope, nextTemp)
	bp, bl := saExpandStr(w, seg, nextTemp)
	w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
	saReleaseOwnedTemp(w, scope, seg)
	return true, ""
}

// saIsConsoleLog 识别 `console.log(...)`。
func saIsConsoleLog(ce *ast.CallExpression) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	return pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "console" &&
		pa.Name() != nil && pa.Name().Text() == "log"
}

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
