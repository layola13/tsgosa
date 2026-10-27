// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

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
		// 顶层 Set 快照调用种（.has 等皆 i32；非 Set 成员沿旧门）。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			if _, ok := saTopSetLookup(scope, pa.Expression); ok && saIsSetMethod(pa.Name().Text()) {
				return "i32", true
			}
		}
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
	if strings.HasPrefix(vkind, "map:") {
		// 嵌套 map 值：读回为 map 柄，内值种透传，下标链可继续索引。
		scope.types[t] = "map"
		saSetMapVal(scope, t, vkind[4:])
		return t, ""
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
	if scope.mapVals[recv] == "jsval" {
		fakeSig := saFuncSig{paramKinds: []string{"jsval"}}
		v, msg := saEvalCallArg(w, fakeSig, 0, rhs, 1, scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
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

// saSetRecordView records a Record-view origin jsval handle (nil-safe).
func saSetRecordView(scope *saScope, name, origin string) {
	if scope.recordView == nil {
		scope.recordView = map[string]string{}
	}
	scope.recordView[name] = origin
}

// saRecordCastKind parses `Record<string, T>` as a cast target, returning the
// map value kind: unknown/any -> jsval, string -> str, number -> i32,
// boolean -> bool. Any other shape is not a Record cast.
func saRecordCastKind(tn *ast.TypeNode) (string, bool) {
	if tn == nil || tn.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := tn.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeName.Text() != "Record" {
		return "", false
	}
	if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 2 {
		return "", false
	}
	if ref.TypeArguments.Nodes[0] == nil || ref.TypeArguments.Nodes[0].Kind != ast.KindStringKeyword {
		return "", false
	}
	vt := ref.TypeArguments.Nodes[1]
	if vt == nil {
		return "", false
	}
	switch vt.Kind {
	case ast.KindUnknownKeyword, ast.KindAnyKeyword:
		return "jsval", true
	case ast.KindStringKeyword:
		return "str", true
	case ast.KindNumberKeyword:
		return "i32", true
	case ast.KindBooleanKeyword:
		return "bool", true
	}
	return "", false
}

// saLowerRecordView binds `name` as a borrowed Record view of the jsval
// `origin`: the view map is the jsval +24 sidecar when present, otherwise a
// fresh empty btree_map (external jsval objects have no sidecar; their
// fields then read as missing/undefined, which is loud only in the sense
// that it is a documented approximation, never a crash). The view is NOT
// owned: releasing it would double-free the sidecar while the origin jsval
// still points at it, and the empty-fallback map leak is accepted over a
// double-free. Field reads go through the existing map PropertyAccess path
// (btree_map_get, missing key -> 0).
func saLowerRecordView(w printer.EmitTextWriter, name, origin, vkind string, scope *saScope, nextTemp *int) {
	scope.addImport("sa_std/btree_map.sa")
	side := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 24 as ptr\n", side, origin))
	cond := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", cond, side))
	hasL := fmt.Sprintf("L_rec_has_%d", *scope.nextLabel)
	*scope.nextLabel++
	emptyL := fmt.Sprintf("L_rec_empty_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_rec_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	saOwnTemp(scope, slot)
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cond, hasL, emptyL))
	w.Write(fmt.Sprintf("%s:\n", hasL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, side))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", emptyL))
	nm := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_btree_map_new()\n", nm))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, nm))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	view := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", view, slot))
	saReleaseOwnedTemp(w, scope, slot)
	w.Write(fmt.Sprintf("  %s = %s\n", name, view))
	scope.types[name] = "map"
	saSetMapVal(scope, name, vkind)
	saSetRecordView(scope, name, origin)
	saDeclarePlain(scope, name)
}

// saReturnMapVal derives a map return's value kind: direct Record, or a
// union whose only non-null member is a Record (null absorbed as 0).
func saReturnMapVal(t *ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if t == nil {
		return "", false
	}
	if v, ok := saRecordValueKind(t, classes, aliasOf); ok {
		return v, true
	}
	if t.Kind == ast.KindUnionType {
		ut := t.AsUnionTypeNode()
		if ut == nil || ut.Types == nil {
			return "", false
		}
		found := ""
		for _, m := range ut.Types.Nodes {
			if m == nil {
				return "", false
			}
			if m.Kind == ast.KindNullKeyword || m.Kind == ast.KindUndefinedKeyword {
				continue
			}
			if m.Kind == ast.KindLiteralType {
				if lit := m.AsLiteralTypeNode().Literal; lit != nil && lit.Kind == ast.KindNullKeyword {
					continue
				}
			}
			v, ok := saRecordValueKind(m, classes, aliasOf)
			if !ok {
				return "", false
			}
			if found == "" {
				found = v
			} else if found != v {
				return "", false
			}
		}
		if found != "" {
			return found, true
		}
	}
	return "", false
}

// saSetMapVal records a map handle value kind (lazy table; nil-safe reads).
func saSetMapVal(scope *saScope, name, vkind string) {
	if scope.mapVals == nil {
		scope.mapVals = map[string]string{}
	}
	scope.mapVals[name] = vkind
}

// saSeedSetVal records a Set handle element kind (first add wins; a
// conflicting later add deletes the entry back to unknown; for-of gates
// on "i32", mirroring mapVals policy; nil-safe reads).
func saSeedSetVal(scope *saScope, name, ekind string) {
	if scope.setVals == nil {
		scope.setVals = map[string]string{}
	}
	if old, ok := scope.setVals[name]; !ok {
		scope.setVals[name] = ekind
	} else if old != ekind {
		delete(scope.setVals, name)
	}
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
	// 正则 exec 右值（`re.exec(s)[i]`；具名绑定版正确（362），右值版曾漏认落 i32
	// 下标误打地址（1168 e1 实证：SA 652123728 vs node "123"）；接收者为正则字面量/
	// 绑定（非串位，不套 saIsStrExpr）；求值/标记/释放走既有右值分支；`?.` 沿旧门。
	if pa.Name() != nil && pa.Name().Text() == "exec" && pa.QuestionDotToken == nil {
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
		if vkind, ok := saRecordValueKind(pd.Type, classes, scope.aliasOf); ok {
			saSetMapVal(scope, nm.Text(), vkind)
		}
	}
}

// saRecordValueKind 认 `Record<string, T>` 值种（T 经 `saAnnotKind` i32/bool
// 即 `"i32"`，string 即 `"str"`；具名接口即 `"inst:T"`；余形 false 沿旧门）。
func saRecordValueKind(t *ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if t == nil || t.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	if ref.TypeName.Text() != "Record" && ref.TypeName.Text() != "Map" {
		return "", false
	}
	if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 2 {
		return "", false
	}
	if ref.TypeArguments.Nodes[0] == nil || ref.TypeArguments.Nodes[0].Kind != ast.KindStringKeyword {
		return "", false
	}
	if ref.TypeArguments.Nodes[1] != nil && (ref.TypeArguments.Nodes[1].Kind == ast.KindUnknownKeyword || ref.TypeArguments.Nodes[1].Kind == ast.KindAnyKeyword) {
		// Record<string, unknown/any> values are jsval handles (JEV
		// map_with_jsval_values 0.98); the map handle itself is the
		// return kind, null absorbed as 0 by union erasure.
		return "jsval", true
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
	if ref.TypeArguments.Nodes[1] != nil && saIsJsValAbleType(ref.TypeArguments.Nodes[1], aliasOf, map[string]bool{}) {
		return "jsval", true
	}
	return "", false
}

// saIsJsValAbleType reports whether a type is representable as jsval map
// values: every JSON-like shape. Arrays and index-signature objects are
// containers (their payload is a handle regardless of element type), so
// element recursion is unnecessary there; alias cycles through containers
// resolve to true, and a direct self-alias cycle also resolves to true
// (degenerate aliases carry no value shape to check).
func saIsJsValAbleType(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ast.KindNullKeyword, ast.KindUndefinedKeyword, ast.KindBooleanKeyword, ast.KindNumberKeyword, ast.KindStringKeyword, ast.KindAnyKeyword, ast.KindUnknownKeyword, ast.KindVoidKeyword:
		return true
	case ast.KindParenthesizedType:
		pt := t.AsParenthesizedTypeNode()
		return pt != nil && saIsJsValAbleType(pt.Type, aliasOf, seen)
	case ast.KindArrayType, ast.KindTupleType:
		return true
	case ast.KindLiteralType:
		lit := t.AsLiteralTypeNode()
		if lit == nil || lit.Literal == nil {
			return false
		}
		switch lit.Literal.Kind {
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
			return true
		}
		return false
	case ast.KindTypeLiteral:
		lit := t.AsTypeLiteralNode()
		if lit == nil || lit.Members == nil {
			return false
		}
		for _, m := range lit.Members.Nodes {
			if m == nil {
				continue
			}
			if m.Kind == ast.KindIndexSignature {
				continue
			}
			if m.Kind != ast.KindPropertySignature {
				return false
			}
			pd := m.AsPropertySignatureDeclaration()
			if pd == nil || pd.Type == nil {
				return false
			}
			if !saIsJsValAbleType(pd.Type, aliasOf, seen) {
				return false
			}
		}
		return true
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return false
		}
		if ref.TypeName.Text() == "Array" || ref.TypeName.Text() == "Record" {
			return true
		}
		nm := ref.TypeName.Text()
		if seen[nm] {
			return true
		}
		seen[nm] = true
		nxt, ok := aliasOf[nm]
		if !ok || nxt == nil {
			return false
		}
		return saIsJsValAbleType(nxt, aliasOf, seen)
	case ast.KindUnionType:
		ut := t.AsUnionTypeNode()
		if ut == nil || ut.Types == nil {
			return false
		}
		for _, m := range ut.Types.Nodes {
			if m == nil || !saIsJsValAbleType(m, aliasOf, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
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
		} else if vkind == "jsval" {
			fakeSig := saFuncSig{paramKinds: []string{"jsval"}}
			var msg string
			v, msg = saEvalCallArg(w, fakeSig, 0, init, 1, scope, pos, refusals, nextTemp)
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

// saTopMap 顶层 const Record 快照（名→字面量 + 值种；用点物化；JEV
// topmap_materialize 1.0, chain_maps 0.92）：值种编码 str/i32，或
// map:<内值种>（嵌套 Record 递归）。仅同文件已认领快照可作嵌套值；
// 导出形、变异形（.set/.delete/.clear 或下标写）一律不收；JSON 导入值
// 可作嵌套值（JEV nested_json_maps 0.69，flat_str_only 0.63）：默认导入
// 的 .json 文件须为扁平串对象，解析入 jsonVals，物化时按标识符查表。
type saTopMap struct {
	init     *ast.Node
	vkind    string
	jsonVals map[string]map[string]string // JSON 导入名→扁平串表（nil 表无 JSON 值；物化时标识符臂查）
}

// saTopMapValueKind 解析 Record/Map 注解的值种：串/数/布尔直值，或
// 嵌套 Record/Map 的 map:<内值种>；其余（接口值、jsval 值等）不收。
func saTopMapValueKind(t *ast.TypeNode) (string, bool) {
	return saTopMapValueKindA(t, nil)
}

// saTopMapValueKindA：同 saTopMapValueKind，另以同文件别名表消解键别名
// （Record<Locale, ...> 的 Locale 须经别名表展为索引访问方认；nil 表沿旧门）。
func saTopMapValueKindA(t *ast.TypeNode, aliases map[string]*ast.TypeNode) (string, bool) {
	if t == nil {
		return "", false
	}
	switch t.Kind {
	case ast.KindStringKeyword:
		return "str", true
	case ast.KindNumberKeyword:
		return "i32", true
	case ast.KindBooleanKeyword:
		return "i32", true
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return "", false
		}
		if ref.TypeName.Text() != "Record" && ref.TypeName.Text() != "Map" {
			return "", false
		}
		if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 2 {
			return "", false
		}
		if !saTopMapKeyOK(ref.TypeArguments.Nodes[0], aliases, 0) {
			return "", false
		}
		inner, ok := saTopMapValueKindA(ref.TypeArguments.Nodes[1], aliases)
		if !ok {
			return "", false
		}
		return "map:" + inner, true
	default:
		return "", false
	}
}

// saTopMapKeyOK：map 键恒为串。StringKeyword 直认；串字面量并集直认；
// 索引访问（Locale 形 (typeof X)[...]["value"]）认——其运行时值恒为
// 串字面量并集；数字属性名由字面量校验另行拒（见 saTopMapLitOK）。
func saTopMapKeyOK(t *ast.TypeNode, aliases map[string]*ast.TypeNode, depth int) bool {
	if t == nil || depth > 4 {
		return false
	}
	switch t.Kind {
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return false
		}
		if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
			return false
		}
		if aliased, ok := aliases[ref.TypeName.Text()]; ok {
			return saTopMapKeyOK(aliased, aliases, depth+1)
		}
		return false
	case ast.KindIdentifier:
		if aliased, ok := aliases[t.Text()]; ok {
			return saTopMapKeyOK(aliased, aliases, depth+1)
		}
		return false
	case ast.KindStringKeyword:
		return true
	case ast.KindIndexedAccessType:
		return true
	case ast.KindTypeOperator:
		op := t.AsTypeOperatorNode()
		if op == nil || op.Operator != ast.KindKeyOfKeyword || op.Type == nil {
			return false
		}
		return saKeyofMembersAreStr(op.Type, aliases, depth+1)
	case ast.KindUnionType:
		ut := t.AsUnionTypeNode()
		if ut == nil || ut.Types == nil {
			return false
		}
		for _, m := range ut.Types.Nodes {
			if m == nil || m.Kind != ast.KindLiteralType {
				return false
			}
			lit := m.AsLiteralTypeNode()
			if lit == nil || lit.Literal == nil || lit.Literal.Kind != ast.KindStringLiteral {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// saKeyofMembersAreStr：keyof 操作数经同文件别名解析（深度守卫）后
// 须为 TypeLiteral，且全部成员为标识符名属性/方法签名（keyof 域方为
// 纯串键；索引/调用/构造签名、计算名、未知目标、空字面量、接口目标
// 一律沿旧门大声拒）。
func saKeyofMembersAreStr(t *ast.TypeNode, aliases map[string]*ast.TypeNode, depth int) bool {
	if t == nil || depth > 4 {
		return false
	}
	switch t.Kind {
	case ast.KindTypeLiteral:
		lit := t.AsTypeLiteralNode()
		if lit == nil || lit.Members == nil || len(lit.Members.Nodes) == 0 {
			return false
		}
		for _, m := range lit.Members.Nodes {
			if m == nil || (m.Kind != ast.KindPropertySignature && m.Kind != ast.KindMethodSignature) {
				return false
			}
			fn := m.Name()
			if fn == nil || fn.Kind != ast.KindIdentifier {
				return false
			}
		}
		return true
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return false
		}
		if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
			return false
		}
		aliased, ok := aliases[ref.TypeName.Text()]
		if !ok {
			return false
		}
		return saKeyofMembersAreStr(aliased, aliases, depth+1)
	case ast.KindIdentifier:
		aliased, ok := aliases[t.Text()]
		if !ok {
			return false
		}
		return saKeyofMembersAreStr(aliased, aliases, depth+1)
	default:
		return false
	}
}

// saKeyofDomainNames resolves the field-name domain of a keyof type node
// through same-file aliases (depth-guarded, mirroring saKeyofMembersAreStr):
// a keyof operator unwraps to its operand, a reference/identifier follows
// the alias table, and a TypeLiteral contributes its identifier-named
// property/method members. Anything else (interfaces, unions, index
// signatures, computed names) refuses loudly via ok=false, so callers fall
// back to the strict all-fields gate instead of guessing a domain.
func saKeyofDomainNames(t *ast.TypeNode, aliases map[string]*ast.TypeNode, depth int) (map[string]bool, bool) {
	if t == nil || depth > 8 {
		return nil, false
	}
	switch t.Kind {
	case ast.KindParenthesizedType:
		pt := t.AsParenthesizedTypeNode()
		if pt == nil || pt.Type == nil {
			return nil, false
		}
		return saKeyofDomainNames(pt.Type, aliases, depth+1)
	case ast.KindTypeOperator:
		op := t.AsTypeOperatorNode()
		if op == nil || op.Operator != ast.KindKeyOfKeyword || op.Type == nil {
			return nil, false
		}
		return saKeyofDomainNames(op.Type, aliases, depth+1)
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return nil, false
		}
		if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
			return nil, false
		}
		aliased, ok := aliases[ref.TypeName.Text()]
		if !ok {
			return nil, false
		}
		return saKeyofDomainNames(aliased, aliases, depth+1)
	case ast.KindIdentifier:
		aliased, ok := aliases[t.Text()]
		if !ok {
			return nil, false
		}
		return saKeyofDomainNames(aliased, aliases, depth+1)
	case ast.KindTypeLiteral:
		lit := t.AsTypeLiteralNode()
		if lit == nil || lit.Members == nil || len(lit.Members.Nodes) == 0 {
			return nil, false
		}
		out := map[string]bool{}
		for _, m := range lit.Members.Nodes {
			if m == nil || (m.Kind != ast.KindPropertySignature && m.Kind != ast.KindMethodSignature) {
				return nil, false
			}
			fn := m.Name()
			if fn == nil || fn.Kind != ast.KindIdentifier {
				return nil, false
			}
			out[fn.Text()] = true
		}
		return out, true
	default:
		return nil, false
	}
}

// saTopMapLitOK 校验字面量与值种一致：str 须串字面量，i32 须整/布尔
// 直接量，map 须内嵌字面量（递归）或已认领同值种快照标识符；简写
// 仅 map 值可用（即 {enPack} 形）；方法/spread/计算键一律不收。
func saTopMapLitOK(init *ast.Node, vkind string, out map[string]saTopMap, jsons map[string]map[string]string) bool {
	if init == nil || init.Kind != ast.KindObjectLiteralExpression {
		return false
	}
	ol := init.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil {
		return false
	}
	for _, p := range ol.Properties.Nodes {
		if p == nil {
			return false
		}
		var fname string
		var val *ast.Node
		switch p.Kind {
		case ast.KindPropertyAssignment:
			fn, ok := saObjPropName(p)
			if !ok {
				return false
			}
			fname = fn
			val = p.AsPropertyAssignment().Initializer
		case ast.KindShorthandPropertyAssignment:
			fn, ok := saObjPropName(p)
			if !ok {
				return false
			}
			fname = fn
			val = p.Name()
		default:
			return false
		}
		if fname == "" || val == nil {
			return false
		}
		switch {
		case vkind == "str":
			if _, ok := saTopArrStrText(val); !ok {
				return false
			}
		case vkind == "i32":
			if _, ok := saTopArrElemText(val); !ok {
				return false
			}
		case strings.HasPrefix(vkind, "map:"):
			inner := vkind[4:]
			if val.Kind == ast.KindObjectLiteralExpression {
				if !saTopMapLitOK(val, inner, out, jsons) {
					return false
				}
			} else if val.Kind == ast.KindIdentifier {
				if ref, ok := out[val.Text()]; ok {
					if ref.vkind != inner {
						return false
					}
				} else if _, ok := jsons[val.Text()]; ok {
					// JSON 导入即扁平串表，只可作 str 内值（JEV flat_str_only）。
					if inner != "str" {
						return false
					}
				} else {
					return false
				}
			} else {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// saTopMapMutated：文件内出现 name.set/delete/clear 方法调用，或对
// name[k] 的赋值写，即视为变异，快照不收（saTopSetMutated 同形）。
func saTopMapMutated(stmts []*ast.Node, name string) bool {
	mutated := false
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil || mutated {
			return
		}
		if n.Kind == ast.KindPropertyAccessExpression {
			pa := n.AsPropertyAccessExpression()
			if pa != nil && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == name && pa.Name() != nil {
				switch pa.Name().Text() {
				case "add", "delete", "clear", "set":
					mutated = true
					return
				}
			}
		}
		if n.Kind == ast.KindBinaryExpression {
			be := n.AsBinaryExpression()
			if be != nil && be.Left != nil && be.Left.Kind == ast.KindElementAccessExpression {
				ea := be.Left.AsElementAccessExpression()
				if ea != nil && ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier && ea.Expression.Text() == name {
					switch saBinaryOpKind(be) {
					case ast.KindEqualsToken, ast.KindPlusEqualsToken, ast.KindMinusEqualsToken, ast.KindAsteriskEqualsToken, ast.KindSlashEqualsToken, ast.KindPercentEqualsToken, ast.KindAmpersandEqualsToken, ast.KindBarEqualsToken, ast.KindCaretEqualsToken, ast.KindLessThanLessThanEqualsToken, ast.KindGreaterThanGreaterThanEqualsToken, ast.KindGreaterThanGreaterThanGreaterThanEqualsToken, ast.KindAsteriskAsteriskEqualsToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindBarBarEqualsToken, ast.KindQuestionQuestionEqualsToken:
						mutated = true
						return
					}
				}
			}
		}
		n.ForEachChild(func(child *ast.Node) bool {
			if child != nil {
				walk(child)
			}
			return false
		})
	}
	for _, st := range stmts {
		walk(st)
		if mutated {
			return true
		}
	}
	return false
}

// saTopMapPrescan 收顶层 const Record/Map 字面量快照（名→快照；
// 导出形不收；同语句内先声明者可作后声明者的嵌套值）。
func saTopMapPrescan(stmts []*ast.Node, assigned map[string]bool, link *saFileLink) (map[string]saTopMap, map[*ast.Node]bool) {
	aliases := map[string]*ast.TypeNode{}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindTypeAliasDeclaration {
			continue
		}
		ta := st.AsTypeAliasDeclaration()
		if ta == nil || ta.Type == nil {
			continue
		}
		nm := st.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		aliases[nm.Text()] = ta.Type
	}
	out := map[string]saTopMap{}
	claimed := map[*ast.Node]bool{}
	// JSON 导入表（默认导入名→扁平串表；JEV nested_json_maps）：驱动
	// 层经 link.jsonOf 给出 .json 绝对路径（非程序边，不入 specOf/图）；
	// 非扁平串对象（嵌套/数组/数值/解析失败）不入表，语句沿旧律拒。
	jsons := map[string]map[string]string{}
	if link != nil && link.jsonOf != nil {
		for _, st := range stmts {
			if st == nil || st.Kind != ast.KindImportDeclaration {
				continue
			}
			id := st.AsImportDeclaration()
			if id == nil || id.ImportClause == nil {
				continue
			}
			cl := id.ImportClause.AsImportClause()
			if cl == nil || cl.IsTypeOnly() {
				continue
			}
			nm := cl.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			abs, ok := link.jsonOf[saProgModuleSpec(st)]
			if !ok || abs == "" {
				continue
			}
			if jm, ok := saParseJSONFlatStr(abs); ok {
				jsons[nm.Text()] = jm
			}
		}
	}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindVariableStatement {
			continue
		}
		// 导出形亦认领为文件内快照（JEV claim_exported_local 0.55；
		// unimported 0.41 接近，keep_refusing 0.04）：跨文件可见性不
		// 因此改变（topConstHarvest 只收 arr/obj/regex 快照，不收
		// topMap，故跨文件读仍沿 not-exported 旧门拒）；文件内变异
		// 仍由 saTopMapMutated 守。明示风险：导入方若以属性写变异
		// 该 Record，定义文件守不住，同文件读将见字面量值而非变异
		// 值（快照/引用语义分叉，该情形下静默；Set 导出排除仍保留）。
		vs := st.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		vdl := vs.DeclarationList.AsVariableDeclarationList()
		if vdl == nil || vdl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
			continue
		}
		isConst := vdl.AsNode().Flags&ast.NodeFlagsConst != 0
		type pend struct {
			name string
			tm   saTopMap
		}
		var pends []pend
		okAll := true
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil || vd.Initializer == nil {
				okAll = false
				break
			}
			nm := vd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				okAll = false
				break
			}
			name := nm.Text()
			if _, dup := out[name]; dup {
				okAll = false
				break
			}
			if !isConst && assigned[name] {
				okAll = false
				break
			}
			init := saUnwrapTransparent(vd.Initializer)
			// Empty-ctor arm (JEV empty_ctor, guard_only + empty_only):
			// `new Map()` with no arguments claims regardless of the
			// annotation value kind (the snapshot is empty, so no value is
			// ever materialized from it); when the value kind parses (type
			// arguments on the new-expression, else a Map/Record declaration
			// annotation) it is recorded so reads work as for literal
			// topMaps, otherwise vkind "empty" marks the snapshot and value
			// reads refuse loudly at the read arms (saLowerMapIndexLoad
			// would otherwise default an unknown mapVal to i32 -- a silent
			// miscompile). Type arguments may live on the new-expression
			// (the web EMPTY_* Maps are unannotated) or on the annotation.
			// Materialization reuses saLowerTopMap: saTopMapInsertAll no-ops
			// on a non-object-literal init, leaving a fresh empty btree_map.
			// Non-empty constructor arguments keep refusing (only object
			// literals and empty ctors are claimed).
			if init.Kind == ast.KindNewExpression {
				ce := init.AsNewExpression()
				if ce == nil || ce.Expression == nil {
					okAll = false
					break
				}
				ctor := saUnwrapTransparent(ce.Expression)
				if ctor == nil || ctor.Kind != ast.KindIdentifier || ctor.Text() != "Map" {
					okAll = false
					break
				}
				if ce.Arguments != nil && len(ce.Arguments.Nodes) != 0 {
					okAll = false
					break
				}
				vk := "empty"
				var margs []*ast.TypeNode
				if ce.TypeArguments != nil {
					margs = ce.TypeArguments.Nodes
				} else if vd.Type != nil && vd.Type.Kind == ast.KindTypeReference {
					if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && (ref.TypeName.Text() == "Map" || ref.TypeName.Text() == "Record") && ref.TypeArguments != nil {
						margs = ref.TypeArguments.Nodes
					}
				}
				if len(margs) == 2 && margs[0] != nil && margs[1] != nil && saTopMapKeyOK(margs[0], aliases, 0) {
					if k, ok := saTopMapValueKindA(margs[1], aliases); ok {
						vk = k
					}
				}
				if saTopMapMutated(stmts, name) {
					okAll = false
					break
				}
				pends = append(pends, pend{name: name, tm: saTopMap{init: init, vkind: vk, jsonVals: jsons}})
				continue
			}
			if init == nil || init.Kind != ast.KindObjectLiteralExpression {
				okAll = false
				break
			}
			// 外层须为 Record/Map<键, 值> 本身（值种取自第二类型实参；
			// saTopMapValueKind 才是值种解析器，整注解传入会误作嵌套）。
			// 注解须先验为 TypeReference（内联对象注解如 shared
			// SA_VM_BUN_PROFILE_GROUPS 曾致 AsTypeReferenceNode 崩溃；
			// 旧导出跳过曾偶然遮住它；非 Record/Map 注解沿旧门拒）。
			if vd.Type == nil || vd.Type.Kind != ast.KindTypeReference {
				okAll = false
				break
			}
			ref := vd.Type.AsTypeReferenceNode()
			if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || (ref.TypeName.Text() != "Record" && ref.TypeName.Text() != "Map") {
				okAll = false
				break
			}
			if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 2 {
				okAll = false
				break
			}
			if !saTopMapKeyOK(ref.TypeArguments.Nodes[0], aliases, 0) {
				okAll = false
				break
			}
			vk, ok := saTopMapValueKindA(ref.TypeArguments.Nodes[1], aliases)
			if !ok {
				okAll = false
				break
			}
			view := map[string]saTopMap{}
			for k, v := range out {
				view[k] = v
			}
			for _, p := range pends {
				view[p.name] = p.tm
			}
			if !saTopMapLitOK(init, vk, view, jsons) {
				okAll = false
				break
			}
			if saTopMapMutated(stmts, name) {
				okAll = false
				break
			}
			pends = append(pends, pend{name: name, tm: saTopMap{init: init, vkind: vk, jsonVals: jsons}})
		}
		if !okAll {
			continue
		}
		for _, p := range pends {
			out[p.name] = p.tm
		}
		claimed[st] = true
	}
	return out, claimed
}

// saTopMapLookup 查顶层 Record 快照（标识符直指、无局部遮蔽；不落字）。
func saTopMapLookup(scope *saScope, e *ast.Node) (saTopMap, bool) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return saTopMap{}, false
	}
	name := e.Text()
	if _, shadowed := scope.types[name]; shadowed {
		return saTopMap{}, false
	}
	to, ok := scope.topMaps[name]
	if !ok {
		return saTopMap{}, false
	}
	return to, true
}

// saLowerTopMap 用点物化顶层 Record 快照：新建 btree_map，逐键 insert，
// 返回具名 __topmap 槽（map-get 需槽址，临时量不可取址；归属登记同
// saLowerRecordLiteral 的具名绑定）。嵌套值递归物化。
func saLowerTopMap(w printer.EmitTextWriter, tm saTopMap, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	h, msg := saLowerMapNew(w, "Map", nil, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	slot := fmt.Sprintf("__topmap%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", slot, h))
	saConsumeOwn(scope, h)
	saDeclareOwned(scope, slot)
	scope.types[slot] = "map"
	saSetMapVal(scope, slot, tm.vkind)
	if msg := saTopMapInsertAll(w, slot, tm, scope, pos, refusals, nextTemp); msg != "" {
		return "", msg
	}
	return slot, ""
}

// saLowerTopJSONMap 用点物化 JSON 导入扁平串表：新建 btree_map，键排序
// 后逐键 insert（Go map 遍历随机，排序保输出确定性；串值经
// saLowerStringLiteral，池化去重；槽归属同 saLowerTopMap）。
func saLowerTopJSONMap(w printer.EmitTextWriter, jm map[string]string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	h, msg := saLowerMapNew(w, "Map", nil, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	slot := fmt.Sprintf("__topmap%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", slot, h))
	saConsumeOwn(scope, h)
	saDeclareOwned(scope, slot)
	scope.types[slot] = "map"
	saSetMapVal(scope, slot, "str")
	keys := make([]string, 0, len(jm))
	for k := range jm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kh := saLowerStringLiteral(w, k, scope, nextTemp)
		vh := saLowerStringLiteral(w, jm[k], scope, nextTemp)
		w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", slot, kh, vh))
	}
	return slot, ""
}

// saTopJSON 顶层 cast-JSON-import 快照（名→解析后 JSON 对象树；JEV
// nested_json_maps 0.79：非导出 const，初始化器经透明解包为 .json
// 默认导入标识符，文件顶层须为对象；点读时用点物化为 jsval 值
// btree_map，嵌套对象递归物化后以 kind 4 装箱，数组逐元装箱后组装
// jsval 元数组、以 kind 5 装箱；整体直读与缺键读沿旧门大声拒。
type saTopJSON struct {
	tree map[string]any
}

// saTopJSONLookup 查顶层 JSON 快照（标识符直指、无局部遮蔽；不落字）。
func saTopJSONLookup(scope *saScope, e *ast.Node) (saTopJSON, bool) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return saTopJSON{}, false
	}
	name := e.Text()
	if _, shadowed := scope.types[name]; shadowed {
		return saTopJSON{}, false
	}
	tj, ok := scope.topJSONs[name]
	if !ok {
		return saTopJSON{}, false
	}
	return tj, true
}

// saParseJSONObj 读 JSON 文件为对象树：顶层须为对象，否则不收。
func saParseJSONObj(abs string) (map[string]any, bool) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, false
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	return raw, true
}

// saTopJSONMutated 报告文件内任一语句是否变异给定名（map 方法形
// .add/.delete/.clear/.set；或以该名为根的赋值左部，点读/下标皆算）。
func saTopJSONMutated(stmts []*ast.Node, name string) bool {
	mutated := false
	var rootIs func(n *ast.Node) bool
	rootIs = func(n *ast.Node) bool {
		if n == nil {
			return false
		}
		switch n.Kind {
		case ast.KindIdentifier:
			return n.Text() == name
		case ast.KindPropertyAccessExpression:
			pa := n.AsPropertyAccessExpression()
			return pa != nil && rootIs(pa.Expression)
		case ast.KindElementAccessExpression:
			ea := n.AsElementAccessExpression()
			return ea != nil && rootIs(ea.Expression)
		default:
			return false
		}
	}
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil || mutated {
			return
		}
		if n.Kind == ast.KindPropertyAccessExpression {
			pa := n.AsPropertyAccessExpression()
			if pa != nil && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == name && pa.Name() != nil {
				switch pa.Name().Text() {
				case "add", "delete", "clear", "set":
					mutated = true
					return
				}
			}
		}
		if n.Kind == ast.KindBinaryExpression {
			be := n.AsBinaryExpression()
			if be != nil && be.Left != nil && rootIs(be.Left) {
				switch saBinaryOpKind(be) {
				case ast.KindEqualsToken, ast.KindPlusEqualsToken, ast.KindMinusEqualsToken, ast.KindAsteriskEqualsToken, ast.KindSlashEqualsToken, ast.KindPercentEqualsToken, ast.KindAmpersandEqualsToken, ast.KindBarEqualsToken, ast.KindCaretEqualsToken, ast.KindLessThanLessThanEqualsToken, ast.KindGreaterThanGreaterThanEqualsToken, ast.KindGreaterThanGreaterThanGreaterThanEqualsToken, ast.KindAsteriskAsteriskEqualsToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindBarBarEqualsToken, ast.KindQuestionQuestionEqualsToken:
					mutated = true
					return
				}
			}
		}
		n.ForEachChild(func(child *ast.Node) bool {
			if child != nil {
				walk(child)
			}
			return false
		})
	}
	for _, st := range stmts {
		walk(st)
		if mutated {
			return true
		}
	}
	return false
}

// saTopJSONPrescan 认领顶层 cast-JSON-import const：非导出、const 或
// 未赋值 let/var、初始化器经透明解包为 .json 默认导入标识符、文件
// 解析为对象、文件内无变异；多声明须全认领，否则整句沿旧律拒。
func saTopJSONPrescan(stmts []*ast.Node, assigned map[string]bool, link *saFileLink) (map[string]saTopJSON, map[*ast.Node]bool) {
	out := map[string]saTopJSON{}
	claimed := map[*ast.Node]bool{}
	imports := map[string]string{}
	if link != nil && link.jsonOf != nil {
		for _, st := range stmts {
			if st == nil || st.Kind != ast.KindImportDeclaration {
				continue
			}
			id := st.AsImportDeclaration()
			if id == nil || id.ImportClause == nil {
				continue
			}
			cl := id.ImportClause.AsImportClause()
			if cl == nil || cl.IsTypeOnly() {
				continue
			}
			nm := cl.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			abs, ok := link.jsonOf[saProgModuleSpec(st)]
			if !ok || abs == "" {
				continue
			}
			imports[nm.Text()] = abs
		}
	}
	if len(imports) == 0 {
		return out, claimed
	}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindVariableStatement {
			continue
		}
		if ast.HasModifier(st, ast.ModifierFlagsExport) {
			continue
		}
		vs := st.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		vdl := vs.DeclarationList.AsVariableDeclarationList()
		if vdl == nil || vdl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
			continue
		}
		isConst := vdl.AsNode().Flags&ast.NodeFlagsConst != 0
		type pend struct {
			name string
			tree map[string]any
		}
		var pends []pend
		okAll := true
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil || vd.Initializer == nil {
				okAll = false
				break
			}
			nm := vd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				okAll = false
				break
			}
			name := nm.Text()
			if _, dup := out[name]; dup {
				okAll = false
				break
			}
			if !isConst && assigned[name] {
				okAll = false
				break
			}
			init := saUnwrapTransparent(vd.Initializer)
			if init == nil || init.Kind != ast.KindIdentifier {
				okAll = false
				break
			}
			abs, ok := imports[init.Text()]
			if !ok {
				okAll = false
				break
			}
			tree, ok := saParseJSONObj(abs)
			if !ok {
				okAll = false
				break
			}
			if saTopJSONMutated(stmts, name) {
				okAll = false
				break
			}
			pends = append(pends, pend{name: name, tree: tree})
		}
		if !okAll {
			continue
		}
		for _, p := range pends {
			out[p.name] = saTopJSON{tree: p.tree}
		}
		claimed[st] = true
	}
	return out, claimed
}

// saLowerTopJSONNested 用点物化 JSON 对象树为 jsval 值 btree_map（键
// 排序保输出确定性；失败只返 msg，调用点定位拒，不静默丢值）。
func saLowerTopJSONNested(w printer.EmitTextWriter, obj map[string]any, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	h, msg := saLowerMapNew(w, "Map", nil, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	slot := fmt.Sprintf("__topjson%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", slot, h))
	saConsumeOwn(scope, h)
	saDeclareOwned(scope, slot)
	scope.types[slot] = "map"
	saSetMapVal(scope, slot, "jsval")
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kh := saLowerStringLiteral(w, k, scope, nextTemp)
		vh, msg := saLowerTopJSONValue(w, obj[k], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", slot, kh, vh))
	}
	return slot, ""
}

// saLowerTopJSONValue 装箱单个 JSON 值：串 kind 1、数值 kind 2（f64）、
// 布尔 kind 3、null kind 6、对象递归物化后 kind 4、数组逐元装箱组装后
// kind 5；皆复用 sa_std ts_jsval 装箱 helper（禁内嵌装箱序列）。
func saLowerTopJSONValue(w printer.EmitTextWriter, v any, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	switch t := v.(type) {
	case string:
		sh := saLowerStringLiteral(w, t, scope, nextTemp)
		b := saBoxJsValStd(w, scope, "ptr", 1, sh, nextTemp)
		saOwnTemp(scope, b)
		return b, ""
	case bool:
		iv := "0"
		if t {
			iv = "1"
		}
		b := saBoxJsValStd(w, scope, "i32", 3, iv, nextTemp)
		saOwnTemp(scope, b)
		return b, ""
	case nil:
		b := saBoxJsValStd(w, scope, "ptr", 6, "0", nextTemp)
		saOwnTemp(scope, b)
		return b, ""
	case float64:
		lit := strconv.FormatFloat(t, 'g', -1, 64)
		if !strings.ContainsAny(lit, ".eE") {
			lit += ".0"
		}
		tmp := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s\n", tmp, lit))
		b := saBoxJsValStd(w, scope, "f64", 2, tmp, nextTemp)
		saOwnTemp(scope, b)
		return b, ""
	case map[string]any:
		sub, msg := saLowerTopJSONNested(w, t, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		b := saBoxJsValStd(w, scope, "ptr", 4, sub, nextTemp)
		saOwnTemp(scope, b)
		return b, ""
	case []any:
		var hs []string
		for _, el := range t {
			ev, msg := saLowerTopJSONValue(w, el, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			hs = append(hs, ev)
		}
		arr := saAssembleHandles(w, hs, scope, nextTemp)
		saMarkArrJsVal(scope, arr)
		b := saBoxJsValStd(w, scope, "ptr", 5, arr, nextTemp)
		saOwnTemp(scope, b)
		return b, ""
	default:
		return "", "nested JSON value is not lowerable"
	}
}

// saParseJSONFlatStr 读 JSON 文件为扁平串表：顶层须为对象且值全为串；
// 否则（嵌套/数组/数值/布尔/null/解析失败）返回 not ok，调用方沿旧律拒。
func saParseJSONFlatStr(abs string) (map[string]string, bool) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, false
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out[k] = s
	}
	return out, true
}

func saTopMapInsertAll(w printer.EmitTextWriter, slot string, tm saTopMap, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	ol := tm.init.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil {
		return ""
	}
	for _, p := range ol.Properties.Nodes {
		var fname string
		var val *ast.Node
		if p.Kind == ast.KindPropertyAssignment {
			fn, ok := saObjPropName(p)
			if !ok {
				return "top map property name is not a literal"
			}
			fname = fn
			val = p.AsPropertyAssignment().Initializer
		} else {
			fn, ok := saObjPropName(p)
			if !ok {
				return "top map property name is not a literal"
			}
			fname = fn
			val = p.Name()
		}
		kh := saLowerStringLiteral(w, fname, scope, nextTemp)
		var v string
		switch {
		case tm.vkind == "str":
			var msg string
			v, msg = saEvalStr(w, val, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
		case tm.vkind == "i32":
			var msg string
			v, msg = saEvalI32(w, val, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
		case strings.HasPrefix(tm.vkind, "map:"):
			inner := tm.vkind[4:]
			if val.Kind == ast.KindObjectLiteralExpression {
				sub, msg := saLowerTopMap(w, saTopMap{init: val, vkind: inner, jsonVals: tm.jsonVals}, scope, pos, refusals, nextTemp)
				if msg != "" {
					return msg
				}
				v = sub
			} else if val.Kind == ast.KindIdentifier {
				if ref, ok := scope.topMaps[val.Text()]; ok {
					sub, msg := saLowerTopMap(w, ref, scope, pos, refusals, nextTemp)
					if msg != "" {
						return msg
					}
					v = sub
				} else if jm, ok := tm.jsonVals[val.Text()]; ok {
					sub, msg := saLowerTopJSONMap(w, jm, scope, pos, refusals, nextTemp)
					if msg != "" {
						return msg
					}
					v = sub
				} else {
					return "top map value is not a claimed top map"
				}
			} else {
				return "top map value is not lowerable"
			}
		default:
			return "top map value kind is not lowerable"
		}
		w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", slot, kh, v))
	}
	return ""
}

// saTopSet 顶层 const Set 快照（名→ new 表达式；用点经 btree_set 物化；
// 仅同种字面量元；JEV snapshot_guarded 0.91：导出形禁认领（跨文件
// 变异不可见），文件内 .add/.delete/.clear/.set 任一出现即禁认领，
// 空 Map 变异形不在此列，沿旧 kind-244 门）。
type saTopSet struct {
	init     *ast.Node
	elemKind string
	// folded holds prescan-folded element texts for the Set-from-call
	// arm (new Set(src.map(o => o.field))); nil for the literal arm.
	// Texts are in elemKind: str content (unquoted, as stored in
	// saTopObjElem.strFields) or i32 numeric text (numFields).
	folded []string
}

// saEmptyCtorElemKind maps a Set element type node to a snapshot element
// kind: string -> str, number/boolean -> i32; anything else (interfaces,
// unions, arrays, missing) yields "" so the claim refuses loudly (JEV
// annot_or_typearg: .has key encoding needs a known element kind).
func saEmptyCtorElemKind(t *ast.TypeNode) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ast.KindStringKeyword:
		return "str"
	case ast.KindNumberKeyword, ast.KindBooleanKeyword:
		return "i32"
	}
	return ""
}

// saTopSetLookup 查顶层 Set 快照（标识符直指、无局部遮蔽；不落字）。
func saTopSetLookup(scope *saScope, e *ast.Node) (saTopSet, bool) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return saTopSet{}, false
	}
	if _, shadowed := scope.types[e.Text()]; shadowed {
		return saTopSet{}, false
	}
	ts, ok := scope.topSets[e.Text()]
	if !ok {
		return saTopSet{}, false
	}
	return ts, true
}

// saTopSetMutated 报告文件内任一语句是否以变异成员访问给定名
// （.add/.delete/.clear/.set，调用与否皆算；.has/.size 等读访问
// 不算；递归进函数体/箭头体；ForEachChild 回调恒返 false 以遍历
// 全部子节点，禁 saNodeContainsIdent 式倒置提前停）。
func saTopSetMutated(stmts []*ast.Node, name string) bool {
	mutated := false
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil || mutated {
			return
		}
		if n.Kind == ast.KindPropertyAccessExpression {
			pa := n.AsPropertyAccessExpression()
			if pa != nil && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == name && pa.Name() != nil {
				switch pa.Name().Text() {
				case "add", "delete", "clear", "set":
					mutated = true
					return
				}
			}
		}
		n.ForEachChild(func(child *ast.Node) bool {
			if child != nil {
				walk(child)
			}
			return false
		})
	}
	for _, st := range stmts {
		walk(st)
		if mutated {
			return true
		}
	}
	return false
}

// saFoldTopSetMapCall folds `src.map(o => o.field)` over a claimed top
// object array (JEV fold_map_at_prescan 0.97): the callback must be a
// single-parameter arrow/function whose body is exactly a field read on
// that parameter (expression body, or a block with one return of that
// shape, mirroring saFindEqGuard); every source element must carry the
// field as a string literal (strFields) or a numeric literal
// (numFields), all of one kind; bool/array/instance/object-array fields,
// missing fields, mixed kinds, empty sources, and unclaimed sources all
// refuse (return false) so the statement keeps refusing kind-244 loudly.
func saFoldTopSetMapCall(arg *ast.Node, topArrs map[string]saTopArr) ([]string, string, bool) {
	if arg == nil {
		return nil, "", false
	}
	call := saUnwrapTransparent(arg)
	if call == nil || call.Kind != ast.KindCallExpression {
		return nil, "", false
	}
	ce := call.AsCallExpression()
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return nil, "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Text() != "map" || pa.QuestionDotToken != nil || ce.QuestionDotToken != nil {
		return nil, "", false
	}
	base := saUnwrapTransparent(pa.Expression)
	if base == nil || base.Kind != ast.KindIdentifier {
		return nil, "", false
	}
	src, ok := topArrs[base.Text()]
	if !ok || len(src.objs) == 0 {
		return nil, "", false
	}
	if ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 || ce.Arguments.Nodes[0] == nil {
		return nil, "", false
	}
	cb := saUnwrapTransparent(ce.Arguments.Nodes[0])
	if cb == nil || (cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression) {
		return nil, "", false
	}
	params := cb.Parameters()
	if len(params) < 1 {
		return nil, "", false
	}
	pd := params[0].AsParameterDeclaration()
	if pd == nil || pd.Name() == nil || pd.Name().Kind != ast.KindIdentifier {
		return nil, "", false
	}
	paramName := pd.Name().Text()
	body := cb.Body()
	if body == nil {
		return nil, "", false
	}
	expr := body
	if body.Kind == ast.KindBlock {
		bd := body.AsBlock()
		if bd == nil || bd.Statements == nil || len(bd.Statements.Nodes) != 1 || bd.Statements.Nodes[0].Kind != ast.KindReturnStatement {
			return nil, "", false
		}
		rs := bd.Statements.Nodes[0].AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return nil, "", false
		}
		expr = rs.Expression
	}
	expr = saUnwrapTransparent(expr)
	if expr == nil || expr.Kind != ast.KindPropertyAccessExpression {
		return nil, "", false
	}
	fpa := expr.AsPropertyAccessExpression()
	if fpa == nil || fpa.QuestionDotToken != nil || fpa.Name() == nil || fpa.Name().Kind != ast.KindIdentifier || fpa.Expression == nil || fpa.Expression.Kind != ast.KindIdentifier || fpa.Expression.Text() != paramName {
		return nil, "", false
	}
	field := fpa.Name().Text()
	elemKind := ""
	var folded []string
	for _, o := range src.objs {
		if v, ok := o.strFields[field]; ok {
			if elemKind == "" {
				elemKind = "str"
			} else if elemKind != "str" {
				return nil, "", false
			}
			folded = append(folded, v)
			continue
		}
		if v, ok := o.numFields[field]; ok {
			if elemKind == "" {
				elemKind = "i32"
			} else if elemKind != "i32" {
				return nil, "", false
			}
			folded = append(folded, v)
			continue
		}
		return nil, "", false
	}
	if elemKind == "" {
		return nil, "", false
	}
	return folded, elemKind, true
}

// saLowerTopSetFolded materializes a folded Set-from-call snapshot:
// fresh btree_set + one insert per folded element, mirroring the
// literal seed path in saLowerMapNewSeeds (str keys via
// saLowerStringLiteral, i32 keys via the 8-byte cell + 16-byte slice
// pair, released after the insert). The caller owns and releases the
// returned handle exactly like the literal .has path.
func saLowerTopSetFolded(w printer.EmitTextWriter, ts saTopSet, scope *saScope, nextTemp *int) (string, string) {
	scope.addImport("sa_std/btree_set.sa")
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_btree_set_new()\n", h))
	for _, text := range ts.folded {
		if ts.elemKind == "str" {
			ks := saLowerStringLiteral(w, text, scope, nextTemp)
			w.Write(fmt.Sprintf("  call @sa_btree_set_insert(&%s, &%s)\n", h, ks))
			continue
		}
		cell := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", cell))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", cell, text))
		slice := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", slice))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slice, cell))
		w.Write(fmt.Sprintf("  store %s + 8, 4 as u64\n", slice))
		w.Write(fmt.Sprintf("  call @sa_btree_set_insert(&%s, &%s)\n", h, slice))
		saReleaseKeySlice(w, slice, cell)
	}
	return h, ""
}

// saTopSetPrescan 收顶层 `const S = new Set([...])` 快照（名→快照；
// 构造器须为 Set 标识符（类型实参忽略），实参须恰一数组字面量，
// 元须全串或全 i32 字面量（混元/非字面量整句不收）；const 永收，
// let/var 须未被赋值；导出形与文件内变异形不收，沿旧 kind-244 门。
func saTopSetPrescan(stmts []*ast.Node, funcs map[string]saFuncSig, assigned map[string]bool, topArrs map[string]saTopArr) (map[string]saTopSet, map[*ast.Node]bool) {
	out := map[string]saTopSet{}
	claimed := map[*ast.Node]bool{}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindVariableStatement {
			continue
		}
		if ast.HasModifier(st, ast.ModifierFlagsExport) {
			continue
		}
		vs := st.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		vdl := vs.DeclarationList.AsVariableDeclarationList()
		if vdl == nil || vdl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
			continue
		}
		isConst := vdl.AsNode().Flags&ast.NodeFlagsConst != 0
		type pend struct {
			name string
			set  saTopSet
		}
		var pends []pend
		okAll := true
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil || vd.Initializer == nil {
				okAll = false
				break
			}
			nm := vd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				okAll = false
				break
			}
			name := nm.Text()
			if _, dup := out[name]; dup {
				okAll = false
				break
			}
			if _, dup := funcs[name]; dup {
				okAll = false
				break
			}
			if !isConst && assigned[name] {
				okAll = false
				break
			}
			init := saUnwrapTransparent(vd.Initializer)
			if init == nil || init.Kind != ast.KindNewExpression {
				okAll = false
				break
			}
			ce := init.AsNewExpression()
			if ce == nil || ce.Expression == nil {
				okAll = false
				break
			}
			ctor := saUnwrapTransparent(ce.Expression)
			if ctor == nil || ctor.Kind != ast.KindIdentifier || ctor.Text() != "Set" {
				okAll = false
				break
			}
			// Empty-ctor arm (JEV empty_ctor, guard_only + annot_or_typearg +
			// keep_exclusion): `new Set()` with no arguments claims only when
			// an element kind is recoverable from the new-expression type
			// arguments or from a Set/ReadonlySet declaration annotation
			// (ReadonlySet counts: same element type, and the web flip
			// EMPTY_RUNNING_THREAD_IDS is ReadonlySet<string>-annotated).
			// The export exclusion at the top of this loop already applies,
			// and the file-wide mutation guard below applies like the other
			// arms. Materialization reuses the existing .has path:
			// saLowerMapNew with zero arguments yields a fresh empty btree_set.
			if ce.Arguments == nil || len(ce.Arguments.Nodes) == 0 {
				elemKind := ""
				if ce.TypeArguments != nil && len(ce.TypeArguments.Nodes) == 1 && ce.TypeArguments.Nodes[0] != nil {
					elemKind = saEmptyCtorElemKind(ce.TypeArguments.Nodes[0])
				}
				if elemKind == "" && vd.Type != nil && vd.Type.Kind == ast.KindTypeReference {
					if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && (ref.TypeName.Text() == "Set" || ref.TypeName.Text() == "ReadonlySet") && ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) == 1 && ref.TypeArguments.Nodes[0] != nil {
						elemKind = saEmptyCtorElemKind(ref.TypeArguments.Nodes[0])
					}
				}
				if elemKind == "" {
					okAll = false
					break
				}
				if saTopSetMutated(stmts, name) {
					okAll = false
					break
				}
				pends = append(pends, pend{name: name, set: saTopSet{init: init, elemKind: elemKind}})
				continue
			}
			if ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 || ce.Arguments.Nodes[0] == nil {
				okAll = false
				break
			}
			arg := ce.Arguments.Nodes[0]
			elemKind := ""
			var folded []string
			if arg.Kind != ast.KindArrayLiteralExpression {
				// Set-from-call arm (JEV fold_map_at_prescan 0.97):
				// fold src.map(o => o.field) over a claimed top
				// object array; anything else refuses loudly.
				f, fk, ok := saFoldTopSetMapCall(arg, topArrs)
				if !ok {
					okAll = false
					break
				}
				folded, elemKind = f, fk
				if saTopSetMutated(stmts, name) {
					okAll = false
					break
				}
				pends = append(pends, pend{name: name, set: saTopSet{init: init, elemKind: elemKind, folded: folded}})
				continue
			}
			al := arg.AsArrayLiteralExpression()
			if al == nil || al.Elements == nil {
				okAll = false
				break
			}
			elOk := true
			for _, el := range al.Elements.Nodes {
				if el == nil {
					elOk = false
					break
				}
				if _, sok := saTopArrStrText(el); sok {
					if elemKind == "" {
						elemKind = "str"
					} else if elemKind != "str" {
						elOk = false
						break
					}
				} else if _, iok := saTopArrElemText(el); iok {
					if elemKind == "" {
						elemKind = "i32"
					} else if elemKind != "i32" {
						elOk = false
						break
					}
				} else {
					elOk = false
					break
				}
			}
			if !elOk {
				okAll = false
				break
			}
			if saTopSetMutated(stmts, name) {
				okAll = false
				break
			}
			pends = append(pends, pend{name: name, set: saTopSet{init: init, elemKind: elemKind}})
		}
		if !okAll {
			continue
		}
		for _, p := range pends {
			out[p.name] = p.set
		}
		claimed[st] = true
	}
	return out, claimed
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
// saMapChainBase 同柄链内层判定（`m.set(k,v)`/`s.add(v)`；接收者须绑定
// map/set 标识符（单层，深链沿旧门）；`?.` 沿旧门；仅原位变异返 "0" 字面量
// 的 set/add（弃值零归属），余形沿旧门；1318）。
func saMapChainBase(ce *ast.CallExpression, scope *saScope) (method, recv, kind string, ok bool) {
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", "", "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.QuestionDotToken != nil || ce.QuestionDotToken != nil {
		return "", "", "", false
	}
	m := pa.Name().Text()
	if m != "set" && m != "add" {
		return "", "", "", false
	}
	base := saUnwrapTransparent(pa.Expression)
	if base == nil || base.Kind != ast.KindIdentifier {
		return "", "", "", false
	}
	k, ok := saMapBaseKind(base, scope)
	if !ok {
		return "", "", "", false
	}
	if k == "map" && m != "set" {
		return "", "", "", false
	}
	if k == "set" && m != "add" {
		return "", "", "", false
	}
	return m, base.Text(), k, true
}

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
		// Set 元种播种（串键 cell 为空即串元，否则 i32 元；混元删表回未知，
		// for-of 迭代门凭表，禁静默错读）。
		if kcell == "" {
			saSeedSetVal(scope, recv, "str")
		} else {
			saSeedSetVal(scope, recv, "i32")
		}
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
