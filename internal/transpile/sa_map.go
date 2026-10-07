// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_map.go — Map/Set 最小子集（step30；JEV 落件 a 置信度 84%）。
// 形状证据总纲：封存 lowerMapMethod:4726-4803 + lowerSetMethod:4806-4859
// （btree_map.sa/btree_set.sa 现货直调）+ lowerNew:8581-8602（new 柄）+
// mapKeySlice:4605-4618（i32 键经 alloc 8 单元 + 16 字节 len4 切片，
// 串键直通）+ 测试面 saemit_test.go:301-319 + 投影表 stdlib.go:132-134。
// 本薄口：键 i32/串，值 i32（读回窄化点唯一）；keys/values/entries
//（vec/set 元模型超 i32 槽）与 forEach 等回调形一律大声拒。

// saIsMapMethod Map 方法名集合。
func saIsMapMethod(m string) bool {
	switch m {
	case "set", "get", "has", "delete", "clear", "size", "getSize",
		"keys", "values", "entries":
		return true
	}
	return false
}

// saIsSetMethod Set 方法名集合。
func saIsSetMethod(m string) bool {
	switch m {
	case "add", "has", "delete", "clear", "size", "getSize":
		return true
	}
	return false
}

// saMapBaseKind 基种判定（map/set 绑定名）。
func saMapBaseKind(e *ast.Node, scope *saScope) (string, bool) {
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && (k == "map" || k == "set") {
			return k, true
		}
	}
	return "", false
}

// saMapCallKind Map/Set 调用的返回种（语法级判定，不落字）。
func saMapCallKind(ce *ast.CallExpression, scope *saScope) (string, bool) {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return "", false
	}
	kind, ok := saMapBaseKind(pa.Expression, scope)
	if !ok {
		return "", false
	}
	m := pa.Name().Text()
	if kind == "map" {
		if !saIsMapMethod(m) {
			return "", true
		}
		switch m {
		case "keys", "values", "entries":
			return "arr", true
		}
		// `get` 回值种按建表记（数组值即 arr；无表恒 i32）。
		if m == "get" {
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
				if vk, ok := scope.mapVals[pa.Expression.Text()]; ok && vk == "arr" {
					return "arr", true
				}
			}
		}
		return "i32", true
	}
	if !saIsSetMethod(m) {
		return "", true
	}
	return "i32", true
}

// saMapKeySlice 键编码（串键直通；i32 键经单元切片；形状证据：mapKeySlice）。
func saMapKeySlice(w printer.EmitTextWriter, a *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	if saIsStrExpr(a, scope) {
		ks, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
		return ks, "", msg
	}
	v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", "", msg
	}
	cell := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", cell))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", cell, v))
	slice := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", slice))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slice, cell))
	w.Write(fmt.Sprintf("  store %s + 8, 4 as u64\n", slice))
	return slice, cell, ""
}

// saReleaseKeySlice 释键槽（仅 i32 键分支的裸 alloc cell/slice；串键
// cell 为空、串柄走既有归属口，禁双释；调用点 btree call 后、return 前调用）。
func saReleaseKeySlice(w printer.EmitTextWriter, ks, cell string) {
	if cell == "" {
		return
	}
	if saIsTempOp(cell) {
		w.Write(fmt.Sprintf("  !%s\n", cell))
	}
	if saIsTempOp(ks) {
		w.Write(fmt.Sprintf("  !%s\n", ks))
	}
}

// saLowerMapIndexLoad lowering `m[k]` 读（脱糖为 map-get；值种按建表记
// （`Record<string,T>` 具化表，无表恒 i32，与 `.get(k)` 同形同值）；上游
// `m[k]` 系数组地址错码，禁照抄，见铁律 4）。
func saLowerMapIndexLoad(w printer.EmitTextWriter, recv string, key *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/btree_map.sa")
	ks, kcell, msg := saMapKeySlice(w, key, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_btree_map_get(&%s, &%s)\n", t, recv, ks))
	saReleaseKeySlice(w, ks, kcell)
	saOwnTemp(scope, t)
	vkind := scope.mapVals[recv]
	if vkind == "" {
		vkind = "i32"
	}
	scope.types[t] = vkind
	return t, ""
}

// saLowerMapIndexStore lowering `m[k] = v`（脱糖为 map-set；值种按建表记
// （`Record<string,T>` 具化表，无表恒 i32；str 经串求值，inst 经布局现场构造或
// 同布局绑定直传；与 `.set(k, v)` 同形同值；Set 无键值大声拒由调用方守卫）。
func saLowerMapIndexStore(w printer.EmitTextWriter, recv string, key, rhs *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	scope.addImport("sa_std/btree_map.sa")
	ks, kcell, msg := saMapKeySlice(w, key, scope, pos, refusals, nextTemp)
	if msg != "" {
		return msg
	}
	if scope.mapVals[recv] == "str" {
		v, msg := saEvalStr(w, rhs, scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
		saReleaseKeySlice(w, ks, kcell)
		return ""
	}
	if vk := scope.mapVals[recv]; vk != "" && len(vk) > 5 && vk[:5] == "inst:" {
		// 实例值存（字面量经该布局现场构造，同布局绑定直传；错种大声拒，
		// 禁 i32 值混入句柄槽；与字面量构造 260-283 同形同序，值柄留归属）。
		want := vk[5:]
		var v string
		if rhs != nil && rhs.Kind == ast.KindObjectLiteralExpression {
			lh, lname, msg := saLowerObjectLiteral(w, rhs, want, scope, pos, refusals, nextTemp)
			if msg != "" || lname != want {
				return "record value does not match interface " + want
			}
			v = lh
		} else if rhs != nil && rhs.Kind == ast.KindIdentifier {
			if k, ok := scope.types[rhs.Text()]; !ok || k != vk {
				return "record value does not match interface " + want
			}
			v = rhs.Text()
		} else if rhs != nil && (rhs.Kind == ast.KindElementAccessExpression || rhs.Kind == ast.KindCallExpression) {
			// 同布局转存（`M2[k] = M1[k]`；求值后记种须同表；与解构读值同形）。
			t, msg := saEvalI32(w, rhs, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
			if k, ok := scope.types[t]; !ok || k != vk {
				return "record value does not match interface " + want
			}
			v = t
		} else {
			return "record value does not match interface " + want
		}
		w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
		saReleaseKeySlice(w, ks, kcell)
		return ""
	}
	v, msg := saEvalI32(w, rhs, scope, pos, refusals, nextTemp)
	if msg != "" {
		return msg
	}
	// 无表映射值位记种检查（串/实例句柄禁入 i32 槽；有表分支各按表种办；铁律 4）。
	if msg := saCheckI32Value(scope, v); msg != "" {
		return msg
	}
	w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
	saReleaseKeySlice(w, ks, kcell)
	return ""
}

// saSetMapVal records a map handle value kind (lazy table; nil-safe reads).
func saSetMapVal(scope *saScope, name, vkind string) {
	if scope.mapVals == nil {
		scope.mapVals = map[string]string{}
	}
	scope.mapVals[name] = vkind
}

// saSeedParamMapVals 播形参 map 值种（`m: Record<string,T>` 按注解记表；
// 无注解/i32 缺省不记，读侧恒 i32；函数/箭头序同形共用）。
func saSeedParamMapVals(paramNodes []*ast.Node, kinds map[string]string, classes map[string]*saClassDef, scope *saScope) {
	for _, pn := range paramNodes {
		if pn == nil {
			continue
		}
		pd := pn.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier || kinds[nm.Text()] != "map" {
			continue
		}
		if pd.Type == nil {
			continue
		}
		if vkind, ok := saRecordValueKind(pd.Type, classes); ok {
			saSetMapVal(scope, nm.Text(), vkind)
		}
	}
}

// saRecordValueKind 认 `Record<string, T>` 值种（T 经 `saAnnotKind` i32/bool
// 即 `"i32"`，string 即 `"str"`；具名接口即 `"inst:T"`；余形 false 沿旧门）。
func saRecordValueKind(t *ast.TypeNode, classes map[string]*saClassDef) (string, bool) {
	if t == nil || t.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	if ref.TypeName.Text() != "Record" {
		return "", false
	}
	if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 2 {
		return "", false
	}
	if ref.TypeArguments.Nodes[0] == nil || ref.TypeArguments.Nodes[0].Kind != ast.KindStringKeyword {
		return "", false
	}
	if k, ok := saAnnotKind(ref.TypeArguments.Nodes[1]); ok && (k == "i32" || k == "bool") {
		return "i32", true
	}
	if k, ok := saAnnotKind(ref.TypeArguments.Nodes[1]); ok && k == "str" {
		return "str", true
	}
	if ref.TypeArguments.Nodes[1] != nil && ref.TypeArguments.Nodes[1].Kind == ast.KindTypeLiteral {
		// 匿名对象值合成布局（Z1；合成失败沿旧门）。
		if lname, ok := saSynthAnonLayout(ref.TypeArguments.Nodes[1], classes); ok {
			return "inst:" + lname, true
		}
		return "", false
	}
	if ref.TypeArguments.Nodes[1] != nil && ref.TypeArguments.Nodes[1].Kind == ast.KindTypeReference {
		if aref := ref.TypeArguments.Nodes[1].AsTypeReferenceNode(); aref != nil && aref.TypeName != nil && aref.TypeName.Kind == ast.KindIdentifier {
			if def, ok := classes[aref.TypeName.Text()]; ok && def.isIface {
				return "inst:" + def.name, true
			}
		}
	}
	return "", false
}

// saLowerRecordLiteral lowering `const m: Record<string,T> = {a: v}`
// （Map 具化 + 逐键 insert；i32 值经 i32 求值，str 值经串求值，具名接口值经该布局现场构造或
// 同布局绑定直传；方法/spread/计算键/错种值沿旧门大声拒）。
func saLowerRecordLiteral(w printer.EmitTextWriter, name, vkind string, n *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	ol := n.AsObjectLiteralExpression()
	h, msg := saLowerMapNew(w, "Map", nil, scope, nextTemp)
	if msg != "" {
		ln, col := pos(n.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, h))
	scope.types[name] = "map"
	saSetMapVal(scope, name, vkind)
	saConsumeOwn(scope, h)
	saDeclareOwned(scope, name)
	for _, p := range ol.Properties.Nodes {
		var fname string
		var init *ast.Node
		switch p.Kind {
		case ast.KindPropertyAssignment:
			fn, ok := saObjPropName(p)
			if !ok {
				ln, col := pos(p.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed property names must be literals (dynamic keys have no static layout)"})
				return false
			}
			fname = fn
			init = p.AsPropertyAssignment().Initializer
		case ast.KindShorthandPropertyAssignment:
			fn, ok := saObjPropName(p)
			if !ok {
				ln, col := pos(p.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed property names must be literals (dynamic keys have no static layout)"})
				return false
			}
			fname = fn
			init = p.Name()
		default:
			ln, col := pos(p.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object literal property is not lowerable (methods refused)"})
			return false
		}
		kh := saLowerStringLiteral(w, fname, scope, nextTemp)
		var v string
		if vkind == "str" {
			var msg string
			v, msg = saEvalStr(w, init, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(p.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
		} else if vkind == "i32" {
			var msg string
			v, msg = saEvalI32(w, init, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(p.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
		} else {
			want := vkind[5:]
			if init != nil && init.Kind == ast.KindObjectLiteralExpression {
				lh, lname, msg := saLowerObjectLiteral(w, init, want, scope, pos, refusals, nextTemp)
				if msg != "" || lname != want {
					ln, col := pos(p.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "record value does not match interface " + want})
					return false
				}
				// 值柄留归属（尾声释放，序在 map 之前，体后无读，sound；
				// consume 会致 map 仍引用而 verifier 报漏）。
				v = lh
			} else if init != nil && init.Kind == ast.KindIdentifier {
				if k, ok := scope.types[init.Text()]; !ok || k != vkind {
					ln, col := pos(p.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "record value does not match interface " + want})
					return false
				}
				v = init.Text()
			} else {
				ln, col := pos(p.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "record value does not match interface " + want})
				return false
			}
		}
		w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", name, kh, v))
	}
	return true
}

// saLowerMapNew `new Map()`/`new Set()`（参数忽略容忍；形状证据：封存 lowerNew:8579-8592 不看参数）。
func saLowerMapNew(w printer.EmitTextWriter, name string, ce *ast.NewExpression, scope *saScope, nextTemp *int) (string, string) {
	sym, mod := "sa_btree_map_new", "sa_std/btree_map.sa"
	kind := "map"
	if name == "Set" {
		sym, mod, kind = "sa_btree_set_new", "sa_std/btree_set.sa", "set"
	}
	scope.addImport(mod)
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @%s()\n", t, sym))
	_ = kind
	return t, ""
}

// saLowerMapCall Map/Set 调用总线（返回 operand/种/errMsg；Map 值种按建表记
// （`Record<string,T>` 具化表，无表恒 i32；str/inst 经对应求值；keys 系另行拒）；
// 形状证据：lowerMapMethod/lowerSetMethod）。
func saLowerMapCall(w printer.EmitTextWriter, recv, kind, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if kind == "map" {
		scope.addImport("sa_std/btree_map.sa")
		switch method {
		case "set":
			if len(argNodes) != 2 {
				return "", "", "Map.set needs 2 arguments"
			}
			ks, kcell, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			if scope.mapVals[recv] == "str" {
				v, msg := saEvalStr(w, argNodes[1], scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
				saReleaseKeySlice(w, ks, kcell)
				return "0", "i32", ""
			}
			if scope.mapVals[recv] == "arr" {
				// 数组值存（句柄 word 入槽；i32/串元皆位存，元种不记，串元读另步）。
				v, msg := saArrValueOf(w, argNodes[1], scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
				saReleaseKeySlice(w, ks, kcell)
				return "0", "i32", ""
			}
			if vk := scope.mapVals[recv]; vk != "" && len(vk) > 5 && vk[:5] == "inst:" {
				// 实例值存（与下标存同形同序，见上）。
				want := vk[5:]
				rhs := argNodes[1]
				var v string
				if rhs != nil && rhs.Kind == ast.KindObjectLiteralExpression {
					lh, lname, msg := saLowerObjectLiteral(w, rhs, want, scope, pos, refusals, nextTemp)
					if msg != "" || lname != want {
						return "", "", "record value does not match interface " + want
					}
					v = lh
				} else if rhs != nil && rhs.Kind == ast.KindIdentifier {
					if k, ok := scope.types[rhs.Text()]; !ok || k != vk {
						return "", "", "record value does not match interface " + want
					}
					v = rhs.Text()
				} else if rhs != nil && (rhs.Kind == ast.KindElementAccessExpression || rhs.Kind == ast.KindCallExpression) {
					// 同布局转存（与下标存同形）。
					t, msg := saEvalI32(w, rhs, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", "", msg
					}
					if k, ok := scope.types[t]; !ok || k != vk {
						return "", "", "record value does not match interface " + want
					}
					v = t
				} else {
					return "", "", "record value does not match interface " + want
				}
				w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
				saReleaseKeySlice(w, ks, kcell)
				return "0", "i32", ""
			}
			v, msg := saEvalI32(w, argNodes[1], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			// 无表映射值位记种检查（与下标存同形；铁律 4）。
			if msg := saCheckI32Value(scope, v); msg != "" {
				return "", "", msg
			}
			w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
			saReleaseKeySlice(w, ks, kcell)
			return "0", "i32", ""
		case "get":
			if len(argNodes) != 1 {
				return "", "", "Map.get needs 1 argument"
			}
			ks, kcell, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_get(&%s, &%s)\n", t, recv, ks))
			saReleaseKeySlice(w, ks, kcell)
			// 调用结果归属(返前释放；上游 ownTemp 同形).
			saOwnTemp(scope, t)
			// 读回种按建表记临时量（与下标读 112-116 同形；否则下游把句柄当 i32 用）。
			vkind := scope.mapVals[recv]
			if vkind == "" {
				vkind = "i32"
			}
			scope.types[t] = vkind
			return t, vkind, ""
		case "has":
			if len(argNodes) != 1 {
				return "", "", "Map.has needs 1 argument"
			}
			ks, kcell, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_contains_key(&%s, &%s)\n", t, recv, ks))
			saReleaseKeySlice(w, ks, kcell)
			// 调用结果归属(返前释放；上游 ownTemp 同形).
			saOwnTemp(scope, t)
			return t, "i32", ""
		case "delete":
			if len(argNodes) != 1 {
				return "", "", "Map.delete needs 1 argument"
			}
			ks, kcell, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_contains_key(&%s, &%s)\n", t, recv, ks))
			// 调用结果归属(返前释放；上游 ownTemp 同形).
			saOwnTemp(scope, t)
			drop := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_remove(&%s, &%s)\n", drop, recv, ks))
			saReleaseKeySlice(w, ks, kcell)
			// 调用结果归属(返前释放；上游 ownTemp 同形).
			saOwnTemp(scope, drop)
			return t, "i32", ""
		case "clear":
			if len(argNodes) != 0 {
				return "", "", "Map.clear needs 0 arguments"
			}
			w.Write(fmt.Sprintf("  call @sa_btree_map_clear(&%s)\n", recv))
			return "0", "i32", ""
		case "size", "getSize":
			if len(argNodes) != 0 {
				return "", "", "Map." + method + " needs 0 arguments"
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_len(&%s)\n", t, recv))
			// 调用结果归属(返前释放；上游 ownTemp 同形).
			saOwnTemp(scope, t)
			return t, "i32", ""
		case "keys", "values", "entries":
			// lib.d.ts Map 迭代器具化（移植封存 lowerMapMethod:4785-4798；符号逐字核对 sci/sa_std/btree_map.sa:1027/1062/1138，本侧只做 @import + 符号调用，禁手写轮子）。
			if len(argNodes) != 0 {
				return "", "", "Map." + method + " needs 0 arguments"
			}
			sym := "sa_btree_map_keys_set"
			if method == "values" {
				sym = "sa_btree_map_values_vec"
			} else if method == "entries" {
				sym = "sa_btree_map_iter_vec"
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @%s(&%s)\n", t, sym, recv))
			// 调用结果归属(返前释放；上游 declareOwned 同形).
			saOwnTemp(scope, t)
			return t, "arr", ""
		default:
			return "", "", "Map." + method + " is not a projected surface"
		}
	}
	scope.addImport("sa_std/btree_set.sa")
	switch method {
	case "add":
		if len(argNodes) != 1 {
			return "", "", "Set.add needs 1 argument"
		}
		ks, kcell, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		w.Write(fmt.Sprintf("  call @sa_btree_set_insert(&%s, &%s)\n", recv, ks))
		saReleaseKeySlice(w, ks, kcell)
		return "0", "i32", ""
	case "has":
		if len(argNodes) != 1 {
			return "", "", "Set.has needs 1 argument"
		}
		ks, kcell, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_btree_set_contains(&%s, &%s)\n", t, recv, ks))
		saReleaseKeySlice(w, ks, kcell)
		// 调用结果归属(返前释放；上游 ownTemp 同形).
		saOwnTemp(scope, t)
		return t, "i32", ""
	case "delete":
		if len(argNodes) != 1 {
			return "", "", "Set.delete needs 1 argument"
		}
		ks, kcell, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_btree_set_contains(&%s, &%s)\n", t, recv, ks))
		// 调用结果归属(返前释放；上游 ownTemp 同形).
		saOwnTemp(scope, t)
		drop := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_btree_set_remove(&%s, &%s)\n", drop, recv, ks))
		saReleaseKeySlice(w, ks, kcell)
		// 调用结果归属(返前释放；上游 ownTemp 同形).
		saOwnTemp(scope, drop)
		return t, "i32", ""
	case "clear":
		if len(argNodes) != 0 {
			return "", "", "Set.clear needs 0 arguments"
		}
		w.Write(fmt.Sprintf("  call @sa_btree_set_clear(&%s)\n", recv))
		return "0", "i32", ""
	case "size", "getSize":
		if len(argNodes) != 0 {
			return "", "", "Set." + method + " needs 0 arguments"
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_btree_set_len(&%s)\n", t, recv))
		// 调用结果归属(返前释放；上游 ownTemp 同形).
		saOwnTemp(scope, t)
		return t, "i32", ""
	default:
		return "", "", "Set." + method + " is not a projected surface"
	}
}
