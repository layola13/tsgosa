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
		"keys", "values", "entries", "forEach":
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
		// `get` 回值种按建表记（数组值即 arr，串元数组即 arrStr；无表恒 i32）。
		if m == "get" {
			if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
				if vk, ok := scope.mapVals[pa.Expression.Text()]; ok && (vk == "arr" || vk == "arrStr") {
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
	if vkind == "arrStr" {
		// 串元数组值读回 arr 柄并透传串元标记（下游 `..[i]` 凭标记走串位）。
		scope.types[t] = "arr"
		saMarkArrStr(scope, t)
	} else {
		scope.types[t] = vkind
	}
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

// saMapArrValKind 判 Map 数组值元种（`string[]`/`Array<string>` 即串元 "arrStr"，
// 其余数组即 "arr"；调用方建表记种，读侧凭此透传串元标记，禁静默错码）。
func saMapArrValKind(vt *ast.Node) string {
	if vt != nil && vt.Kind == ast.KindArrayType {
		if el := vt.AsArrayTypeNode().ElementType; el != nil && el.Kind == ast.KindStringKeyword {
			return "arrStr"
		}
	}
	if vt != nil && vt.Kind == ast.KindTypeReference {
		if ref := vt.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil &&
			ref.TypeName.Kind == ast.KindIdentifier && ref.TypeName.Text() == "Array" &&
			ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) == 1 &&
			ref.TypeArguments.Nodes[0] != nil && ref.TypeArguments.Nodes[0].Kind == ast.KindStringKeyword {
			return "arrStr"
		}
	}
	return "arr"
}

// saFuncReturnsStrArray 报告裸调用是否为串数组返回函数（签名预扫 "arrStr"；
// 直接名/`fn:` 别名/link 限定三路同 saCallRetKind 口径；方法调用沿旧门 false）。
func saFuncReturnsStrArray(ce *ast.CallExpression, scope *saScope) bool {
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return false
	}
	nm := ce.Expression.Text()
	if sig, ok := scope.funcs[nm]; ok {
		return !sig.isVoid && sig.retKind == "arrStr"
	}
	if q, linked := saLinkCallee(scope, nm); linked {
		if sig, ok := scope.funcs[q]; ok {
			return !sig.isVoid && sig.retKind == "arrStr"
		}
	}
	if k, ok := scope.types[nm]; ok && len(k) > 3 && k[:3] == "fn:" {
		if sig, ok := scope.funcs[k[3:]]; ok {
			return !sig.isVoid && sig.retKind == "arrStr"
		}
	}
	return false
}

// saIsStrArrRvalue 报告右值是否为串元数组句柄（`m.get(k)` 且建表记 "arrStr"；
// NonNull/括号/as 包装透视；`?.` 由调用方守卫，裸函数返回句柄未记元种仍 false）。
func saIsStrArrRvalue(e *ast.Node, scope *saScope) bool {
	for e != nil && (e.Kind == ast.KindNonNullExpression || e.Kind == ast.KindParenthesizedExpression ||
		e.Kind == ast.KindAsExpression || e.Kind == ast.KindSatisfiesExpression || e.Kind == ast.KindTypeAssertionExpression) {
		switch e.Kind {
		case ast.KindNonNullExpression:
			e = e.AsNonNullExpression().Expression
		case ast.KindParenthesizedExpression:
			e = e.AsParenthesizedExpression().Expression
		case ast.KindAsExpression:
			e = e.AsAsExpression().Expression
		case ast.KindSatisfiesExpression:
			e = e.AsSatisfiesExpression().Expression
		case ast.KindTypeAssertionExpression:
			e = e.AsTypeAssertion().Expression
		}
	}
	if e == nil || e.Kind != ast.KindCallExpression {
		return false
	}
	ce := e.AsCallExpression()
	if ce.Expression == nil {
		return false
	}
	// 裸函数调用串数组返回（`get()[i]`；签名预扫记 "arrStr"，见 R3-37a；
	// 方法调用基沿旧门，link 限定名查定义签名）。
	if ce.Expression.Kind == ast.KindIdentifier {
		return saFuncReturnsStrArray(ce, scope)
	}
	if ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	// 串 split 右值（`s.split(sep)[i]`；具名绑定版正确，右值版曾漏认落 i32
	// 下标误打地址（968 n4 实证：SA 799929152 vs node b）；串基经 saIsStrExpr
	//（字面量/具名/调用），求值/标记/释放走既有右值分支；`?.` 沿旧门。
	if pa.Name() != nil && pa.Name().Text() == "split" && pa.QuestionDotToken == nil &&
		saIsStrExpr(pa.Expression, scope) {
		return true
	}
	// 串 match 右值（`s.match(re)[i]`；具名绑定版正确（362/489），右值版曾漏认
	// 落 i32 下标误打地址（1158 d2/f2 实证：SA "P" vs node "123"）；miss 臂产空柄，
	// 越界沿右值检查归零；`?.` 沿旧门。
	if pa.Name() != nil && pa.Name().Text() == "match" && pa.QuestionDotToken == nil &&
		saIsStrExpr(pa.Expression, scope) {
		return true
	}
	if pa.Name() == nil || pa.Name().Text() != "get" || pa.QuestionDotToken != nil {
		return false
	}
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
		return false
	}
	if k, ok := scope.types[pa.Expression.Text()]; !ok || k != "map" {
		return false
	}
	return scope.mapVals[pa.Expression.Text()] == "arrStr"
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
	h, msg := saLowerMapNew(w, "Map", nil, scope, pos, refusals, nextTemp)
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
func saLowerMapNew(w printer.EmitTextWriter, name string, ce *ast.NewExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
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
	if msg := saLowerMapNewSeeds(w, t, name, ce, scope, pos, refusals, nextTemp); msg != "" {
		return "", msg
	}
	return t, ""
}

// saLowerMapNewSeeds 构造初值批量插入（`new Set([..])` 逐元 add 去重；`new Map([[k,v]])` 双元逐项 set（值串/i32 双门）；余形大声拒（禁静默丢初值）；576）。
func saLowerMapNewSeeds(w printer.EmitTextWriter, t, name string, ce *ast.NewExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	if ce == nil || ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 || ce.Arguments.Nodes[0] == nil {
		return ""
	}
	// 直接断言 panic 改 kind 门（`new Set(a)` 绑定数组实参旧路崩溃；0 崩溃铁律；768）。
	if ce.Arguments.Nodes[0].Kind != ast.KindArrayLiteralExpression {
		return "Map/Set constructor takes an array literal"
	}
	al := ce.Arguments.Nodes[0].AsArrayLiteralExpression()
	if al == nil || al.Elements == nil {
		return "Map/Set constructor takes an array literal"
	}
	for _, el := range al.Elements.Nodes {
		if el == nil {
			return "Map/Set constructor takes an array literal"
		}
		if name == "Set" {
			ks, kcell, msg := saMapKeySlice(w, el, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
			w.Write(fmt.Sprintf("  call @sa_btree_set_insert(&%s, &%s)\n", t, ks))
			saReleaseKeySlice(w, ks, kcell)
		} else {
			// pair 断言 panic 改 kind 门（同上；768）。
			if el.Kind != ast.KindArrayLiteralExpression {
				return "Map constructor takes [[k, v]] entries"
			}
			pair := el.AsArrayLiteralExpression()
			if pair == nil || pair.Elements == nil || len(pair.Elements.Nodes) != 2 || pair.Elements.Nodes[0] == nil || pair.Elements.Nodes[1] == nil {
				return "Map constructor takes [[k, v]] entries"
			}
			ks, kcell, msg := saMapKeySlice(w, pair.Elements.Nodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
			var v string
			if saIsStrExpr(pair.Elements.Nodes[1], scope) {
				if v, msg = saEvalStr(w, pair.Elements.Nodes[1], scope, pos, refusals, nextTemp); msg != "" {
					return msg
				}
			} else {
				if v, msg = saEvalI32(w, pair.Elements.Nodes[1], scope, pos, refusals, nextTemp); msg != "" {
					return msg
				}
				if msg = saCheckI32Value(scope, v); msg != "" {
					return msg
				}
			}
			w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", t, ks, v))
			saReleaseKeySlice(w, ks, kcell)
		}
	}
	return ""
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
			if scope.mapVals[recv] == "arr" || scope.mapVals[recv] == "arrStr" {
				// 数组值存（句柄 word 入槽；i32/串元皆位存，元种凭建表另辨）。
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
			// 串元数组值归一为 arr 柄并透传串元标记（下游 `..[i]` 凭标记走串位）。
			vkind := scope.mapVals[recv]
			if vkind == "" {
				vkind = "i32"
			}
			if vkind == "arrStr" {
				scope.types[t] = "arr"
				saMarkArrStr(scope, t)
				return t, "arr", ""
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
		case "keys", "values":
			// lib.d.ts Map 迭代器具化（移植封存 lowerMapMethod:4785-4798；符号逐字核对 sci/sa_std/btree_map.sa:1027/1062，本侧只做 @import + 符号调用，禁手写轮子）。
			if len(argNodes) != 0 {
				return "", "", "Map." + method + " needs 0 arguments"
			}
			sym := "sa_btree_map_keys_vec"
			if method == "values" {
				sym = "sa_btree_map_values_word_vec"
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @%s(&%s)\n", t, sym, recv))
			// 调用结果归属(返前释放；上游 declareOwned 同形).
			saOwnTemp(scope, t)
			return t, "arr", ""
		case "entries":
			// `m.entries()` 三 u64 一组快照向量非扁平数组：for-of 按单字长误巡回
			// 3 倍并解构越界崩（1098 t5 实证 segfault；t6 计数亦错）；forEach 自有
			// div3 巡回走直调不受影响；此处大声拒，需遍历用 forEach/keys()/values()。
			if len(argNodes) != 0 {
				return "", "", "Map.entries needs 0 arguments"
			}
			return "", "", "Map.entries() snapshot needs forEach (3-word groups are not flat arrays)"
		case "forEach":
			// Map.forEach 快照向量巡回（`sa_btree_map_iter_vec` 三 u64 一组；回调 `(v[, k])`，v 种按建表（i32 缺省/串 head 借用）；k 串 head 每轮具化即释；587）。
			if len(argNodes) != 1 || argNodes[0] == nil {
				return "", "", "Map.forEach needs 1 argument"
			}
			vec := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_iter_vec(&%s)\n", vec, recv))
			saOwnTemp(scope, vec)
			vkind := scope.mapVals[recv]
			if vkind == "" {
				vkind = "i32"
			}
			nlen := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", nlen, vec))
			cnt := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = div %s, 3\n", cnt, nlen))
			base := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", base, vec))
			idx := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = 0\n", idx))
			topL := fmt.Sprintf("L_mfe_top_%d", *nextTemp)
			*nextTemp++
			bodyL := fmt.Sprintf("L_mfe_body_%d", *nextTemp)
			*nextTemp++
			endL := fmt.Sprintf("L_mfe_end_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("%s:\n", topL))
			cT := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cT, idx, cnt))
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
			w.Write(fmt.Sprintf("%s:\n", bodyL))
			voff := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = mul %s, 24\n", voff, idx))
			vptr := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", vptr, base, voff))
			kval := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 16 as u64\n", kval, vptr))
			kh := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 16\n", kh))
			kparr := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", kparr, vptr))
			klen := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", klen, vptr))
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", kh, kparr))
			w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", kh, klen))
			if _, msg := saCallbackValue(w, argNodes[0], []string{kval, kh}, false, vkind, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp, true, []string{vkind, "str"}); msg != "" {
				return "", "", msg
			}
			saReleaseOwnedTemp(w, scope, kh)
			inext := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, idx))
			w.Write(fmt.Sprintf("  %s = %s\n", idx, inext))
			w.Write(fmt.Sprintf("  jmp %s\n", topL))
			w.Write(fmt.Sprintf("%s:\n", endL))
			saReleaseOwnedTemp(w, scope, vec)
			return "0", "i32", ""
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
