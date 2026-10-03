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
	case "add", "has", "delete", "clear", "size":
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
		return "i32", true
	}
	if !saIsSetMethod(m) {
		return "", true
	}
	return "i32", true
}

// saMapKeySlice 键编码（串键直通；i32 键经单元切片；形状证据：mapKeySlice）。
func saMapKeySlice(w printer.EmitTextWriter, a *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if saIsStrExpr(a, scope) {
		return saEvalStr(w, a, scope, pos, refusals, nextTemp)
	}
	v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
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
	return slice, ""
}

// saLowerMapNew `new Map()`/`new Set()`（零参；有参形大声拒）。
func saLowerMapNew(w printer.EmitTextWriter, name string, ce *ast.NewExpression, scope *saScope, nextTemp *int) (string, string) {
	if ce.Arguments != nil && len(ce.Arguments.Nodes) != 0 {
		return "", "new " + name + "() takes 0 arguments"
	}
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

// saLowerMapCall Map/Set 调用总线（返回 operand/种/errMsg；种恒 i32
// （keys 系另行拒）；形状证据：lowerMapMethod/lowerSetMethod）。
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
			ks, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			v, msg := saEvalI32(w, argNodes[1], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", recv, ks, v))
			return "0", "i32", ""
		case "get":
			if len(argNodes) != 1 {
				return "", "", "Map.get needs 1 argument"
			}
			ks, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_get(&%s, &%s)\n", t, recv, ks))
			// 调用结果归属(返前释放；上游 ownTemp 同形).
			saOwnTemp(scope, t)
			return t, "i32", ""
		case "has":
			if len(argNodes) != 1 {
				return "", "", "Map.has needs 1 argument"
			}
			ks, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_btree_map_contains_key(&%s, &%s)\n", t, recv, ks))
			// 调用结果归属(返前释放；上游 ownTemp 同形).
			saOwnTemp(scope, t)
			return t, "i32", ""
		case "delete":
			if len(argNodes) != 1 {
				return "", "", "Map.delete needs 1 argument"
			}
			ks, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
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
			return "", "", "Map." + method + " needs vec/set models (beyond i32 slots)"
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
		ks, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		w.Write(fmt.Sprintf("  call @sa_btree_set_insert(&%s, &%s)\n", recv, ks))
		return "0", "i32", ""
	case "has":
		if len(argNodes) != 1 {
			return "", "", "Set.has needs 1 argument"
		}
		ks, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_btree_set_contains(&%s, &%s)\n", t, recv, ks))
		// 调用结果归属(返前释放；上游 ownTemp 同形).
		saOwnTemp(scope, t)
		return t, "i32", ""
	case "delete":
		if len(argNodes) != 1 {
			return "", "", "Set.delete needs 1 argument"
		}
		ks, msg := saMapKeySlice(w, argNodes[0], scope, pos, refusals, nextTemp)
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
		// 调用结果归属(返前释放；上游 ownTemp 同形).
		saOwnTemp(scope, drop)
		return t, "i32", ""
	case "clear":
		if len(argNodes) != 0 {
			return "", "", "Set.clear needs 0 arguments"
		}
		w.Write(fmt.Sprintf("  call @sa_btree_set_clear(&%s)\n", recv))
		return "0", "i32", ""
	case "size":
		if len(argNodes) != 0 {
			return "", "", "Set.size needs 0 arguments"
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
