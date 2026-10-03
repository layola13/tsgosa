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
// Number.parseFloat（f64）、console.error（node 插件后端）一律大声拒。

// saStrIntern 字符串常量池录入（同文本去重；转义镜像封存）。
func saStrIntern(pool *saStrPool, text string) string {
	if n, ok := pool.seen[text]; ok {
		return n
	}
	n := fmt.Sprintf("str_const_%d", pool.next)
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
		return ok && k == "str"
	case ast.KindCallExpression:
		return saCallIsStr(e.AsCallExpression(), scope)
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindPlusToken {
			return saIsStrExpr(be.Left, scope) || saIsStrExpr(be.Right, scope)
		}
		return false
	case ast.KindParenthesizedExpression:
		return saIsStrExpr(e.AsParenthesizedExpression().Expression, scope)
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
		if sig, ok := scope.funcs[nm]; ok {
			return !sig.isVoid && sig.retKind == "string"
		}
		return false
	}
	if ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" &&
		pa.Name() != nil {
		switch pa.Name().Text() {
		case "fromCharCode", "fromCodePoint":
			return true
		}
		return false
	}
	if pa.Name() == nil || !saIsStrMethod(pa.Name().Text()) {
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
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
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
				if msg == "" {
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
		}
		return "", "not a string expression"
	default:
		return "", "not a string expression"
	}
}

// saToSlice 任一可文本化操作数转切片（串直通；i32/bool 经 interp；其余拒）。
// 供模板/console/String() 共用（renderInterpValue 哲学：同 sa_fmt 现货）。
func saToSlice(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if saIsStrExpr(e, scope) {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	// date 串方法直通串位（toISOString/toString 系）。
	if e != nil && e.Kind == ast.KindCallExpression && saIsDateStrCall(e.AsCallExpression(), scope) {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	// 数组 join 回串（串位；形状证据同 saEvalStr 调用位）。
	if e != nil && e.Kind == ast.KindCallExpression && saIsArrJoinCall(e.AsCallExpression(), scope) {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
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

// saStringContentEq 串内容相等（等长 + 零偏 indexOf 命中；negate 取反。
// 形状证据：封存 stringContentEq:9146-9166）。
func saStringContentEq(w printer.EmitTextWriter, l, r string, negate bool, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/string.sai")
	lp, ll := saExpandStr(w, l, nextTemp)
	rp, rl := saExpandStr(w, r, nextTemp)
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_string_index_of(%s, %s, %s, %s, 0)\n", idx, lp, ll, rp, rl))
	at0 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", at0, idx))
	samelen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, %s\n", samelen, ll, rl))
	both := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, %s\n", both, at0, samelen))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if negate {
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", out, both))
	} else {
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, both))
	}
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
				if len(args) != 1 {
					return "", false, "String." + pa.Name().Text() + " needs 1 argument"
				}
				v, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				// 原语回 BUFFER 句柄（u64，现货签名 `-> u64`），经 data/len
				// unwrap 成 16 字节串句柄；缓冲作头读（u64 作片读会段错；
				// 形状证据：封存 lowerCall:3811-3836；现货 sa_std/string.sai
				// from_char_code/from_code_point + sa_std/fmt.sai buffer_data/len）。
				sym := "sa_string_from_char_code"
				if pa.Name().Text() == "fromCodePoint" {
					sym = "sa_string_from_code_point"
				}
				scope.addImport("sa_std/string.sai")
				scope.addImport("sa_std/fmt.sai")
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
				return t, false, ""
			}
			return "", false, "unsupported String method " + pa.Name().Text()
		}
		if pa.QuestionDotToken != nil {
			return "", false, "optional member call not lowerable"
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

// saLowerStrMethod lowering 串方法全集（投影表 stdlib.go:95-105 + 封存
// lowerStringMethod:7173-7386；split 需串元数组，超 i32 槽模型，大声拒）。
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
		addr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, bp, sel))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, addr))
		w.Write(fmt.Sprintf("  store %s + 8, 1 as u64\n", out))
		return out, false, ""
	case "trim", "trimStart", "trimEnd":
		// ascii 三件套合成（形状证据：封存 lowerStringMethod:7304-7335）。
		start := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_str_trim_ascii_start_index(%s, %s)\n", start, bp, bl))
		full := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_str_trim_ascii_end_len(%s, %s)\n", full, bp, bl))
		s, l := start, full
		if method == "trimStart" {
			rest := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sub %s, %s\n", rest, bl, start))
			l = rest
		} else if method == "trimEnd" {
			s = "0"
		} else {
			rest := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sub %s, %s\n", rest, full, start))
			l = rest
		}
		nptr := bp
		if s != "0" {
			nptr = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", nptr, bp, s))
		}
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, nptr))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, l))
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
		// 钳位子切片（形状证据：封存 lowerStringMethod:7348-7369 + clampRange:7393-7465）。
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
		s, l := saClampRange(w, bp, bl, a0, end, method == "substring", scope, nextTemp)
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, s))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, l))
		return out, false, ""
	case "split":
		return "", false, "split needs string arrays (beyond i32 slots)"
	case "toString":
		return recv, false, ""
	}
	return "", false, "unsupported string method " + method
}

// saClampRange 钳位 [start, end) 到 [0, len]（负值自末端起钳；substring 另
// 交换逆序界并将负值记 0。形状证据：封存 clampRange:7393-7465）。
func saClampRange(w printer.EmitTextWriter, bp, bl, start, end string, substring bool, scope *saScope, nextTemp *int) (string, string) {
	norm := func(v string) string {
		neg := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		adj := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		nL := fmt.Sprintf("L_cl_neg_%d", *scope.nextLabel)
		*scope.nextLabel++
		nN := fmt.Sprintf("L_cl_nneg_%d", *scope.nextLabel)
		*scope.nextLabel++
		nE := fmt.Sprintf("L_cl_end_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = slt %s, 0\n", neg, v))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", neg, nL, nN))
		w.Write(fmt.Sprintf("%s:\n", nL))
		if substring {
			w.Write(fmt.Sprintf("  %s = 0\n", adj))
		} else {
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", adj, bl, v))
		}
		w.Write(fmt.Sprintf("  jmp %s\n", nE))
		w.Write(fmt.Sprintf("%s:\n", nN))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", adj, v))
		w.Write(fmt.Sprintf("  jmp %s\n", nE))
		w.Write(fmt.Sprintf("%s:\n", nE))
		lo := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		loT := fmt.Sprintf("L_cl_lot_%d", *scope.nextLabel)
		*scope.nextLabel++
		loF := fmt.Sprintf("L_cl_lof_%d", *scope.nextLabel)
		*scope.nextLabel++
		loE := fmt.Sprintf("L_cl_loe_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = slt %s, 0\n", lo, adj))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", lo, loT, loF))
		w.Write(fmt.Sprintf("%s:\n", loT))
		w.Write(fmt.Sprintf("  %s = 0\n", out))
		w.Write(fmt.Sprintf("  jmp %s\n", loE))
		w.Write(fmt.Sprintf("%s:\n", loF))
		hi := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		hiT := fmt.Sprintf("L_cl_hit_%d", *scope.nextLabel)
		*scope.nextLabel++
		hiF := fmt.Sprintf("L_cl_hif_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", hi, adj, bl))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", hi, hiT, hiF))
		w.Write(fmt.Sprintf("%s:\n", hiT))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, bl))
		w.Write(fmt.Sprintf("  jmp %s\n", loE))
		w.Write(fmt.Sprintf("%s:\n", hiF))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, adj))
		w.Write(fmt.Sprintf("  jmp %s\n", loE))
		w.Write(fmt.Sprintf("%s:\n", loE))
		return out
	}
	s := norm(start)
	f := norm(end)
	if substring {
		sw := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		tL := fmt.Sprintf("L_cl_swap_%d", *scope.nextLabel)
		*scope.nextLabel++
		kL := fmt.Sprintf("L_cl_keep_%d", *scope.nextLabel)
		*scope.nextLabel++
		dL := fmt.Sprintf("L_cl_done_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", c, s, f))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, tL, kL))
		w.Write(fmt.Sprintf("%s:\n", tL))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", sw, s))
		w.Write(fmt.Sprintf("  %s = %s\n", s, f))
		w.Write(fmt.Sprintf("  %s = %s\n", f, sw))
		w.Write(fmt.Sprintf("  jmp %s\n", dL))
		w.Write(fmt.Sprintf("%s:\n", kL))
		w.Write(fmt.Sprintf("  jmp %s\n", dL))
		w.Write(fmt.Sprintf("%s:\n", dL))
	}
	nptr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", nptr, bp, s))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", nlen, f, s))
	return nptr, nlen
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
		}
		seg, msg := saToSlice(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return false, msg
		}
		bp, bl := saExpandStr(w, seg, nextTemp)
		w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
	}
	seg := saLowerStringLiteral(w, "\n", scope, nextTemp)
	bp, bl := saExpandStr(w, seg, nextTemp)
	w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
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
