// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_decl.go — 变量声明（step16/17；解构在 sa_arr.go）。
// saLowerVarDecl lowering 变量声明（`let/const x: number|i32 = <i32>`）。
// 形状证据：封存 lowerVarDeclList:1395-1470（using 拒、无 init const 拒、
// 解构拒、缺 init 绑零值、名按 bindingNameText 取标识符）。
func saLowerVarDecl(w printer.EmitTextWriter, s *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	vs := s.AsVariableStatement()
	return saLowerVarDeclList(w, s, vs.DeclarationList.AsVariableDeclarationList(), scope, pos, refusals, nextTemp)
}

func saLowerVarDeclList(w printer.EmitTextWriter, anchor *ast.Node, dl *ast.VariableDeclarationList, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if dl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
		ln, col := pos(anchor.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "using declarations are not lowerable"})
		return false
	}
	isConst := dl.AsNode().Flags&ast.NodeFlagsConst != 0
	for _, d := range dl.Declarations.Nodes {
		vd := d.AsVariableDeclaration()
		nm := vd.Name()
		if nm == nil {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring declarations are not in subset"})
			return false
		}
		if nm.Kind != ast.KindIdentifier {
			if !saLowerDestructuringDecl(w, d, vd, nm.AsNode(), scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		name := nm.Text()
		if _, dup := scope.types[name]; dup {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		if _, dup := scope.mathAlias[name]; dup {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		if saTryMathAliasDecl(d, vd, name, scope, pos, refusals) {
			continue
		}
		if vd.Initializer != nil && vd.Initializer.Kind == ast.KindObjectLiteralExpression {
			// 对象字面量声明（注解须为同名接口；无注解按键集匹配）。
			want := ""
			if vd.Type != nil {
				tn := vd.Type
				ref := tn.AsTypeReferenceNode()
				if tn.Kind != ast.KindTypeReference || ref == nil || ref.TypeName == nil {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object annotation must name an interface"})
					return false
				}
				want = ref.TypeName.Text()
				if def, ok := scope.classes[want]; !ok || !def.isIface {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object annotation must name an interface"})
					return false
				}
			}
			h, defname, msg := saLowerObjectLiteral(w, vd.Initializer, want, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "inst:" + defname
			continue
		}
		if vd.Initializer != nil && vd.Initializer.Kind == ast.KindNewExpression {
			// `new Array(n)` 定长零数组（`new Array(a, b)` 落通用拒绝）。
			if saIsArrayCtor(vd.Initializer) {
				ne := vd.Initializer.AsNewExpression()
				if ne.Arguments == nil || len(ne.Arguments.Nodes) != 1 {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "new Array takes 1 length argument"})
					return false
				}
				if vd.Type != nil {
					if k, ok := saAnnotKind(vd.Type); !ok || k != "arr" {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array annotation must be an array type"})
						return false
					}
				}
				h, msg := saLowerArrayCtor(w, vd.Initializer, scope, pos, refusals, nextTemp)
				if msg != "" {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
				w.Write(fmt.Sprintf("  %s = %s\n", name, h))
				scope.types[name] = "arr"
				continue
			}
			// `new Map()`/`new Set()` 绑定为 map/set 种（零参；有参形大声拒）。
			if ne := vd.Initializer.AsNewExpression(); ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier &&
				(ne.Expression.Text() == "Map" || ne.Expression.Text() == "Set") {
				if vd.Type != nil {
					tn := vd.Type
					ref := tn.AsTypeReferenceNode()
					if tn.Kind != ast.KindTypeReference || ref == nil || ref.TypeName == nil ||
						(ref.TypeName.Text() != "Map" && ref.TypeName.Text() != "Set") {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "collection annotation must be Map or Set"})
						return false
					}
				}
				h, msg := saLowerMapNew(w, ne.Expression.Text(), ne, scope, nextTemp)
				if msg != "" {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
				kind := "map"
				if ne.Expression.Text() == "Set" {
					kind = "set"
				}
				w.Write(fmt.Sprintf("  %s = %s\n", name, h))
				scope.types[name] = kind
				continue
			}
			// `new Date()` 绑定为 date 种（millis 不透明；有参形大声拒）。
			if ne := vd.Initializer.AsNewExpression(); ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "Date" {
				if !saIsDateNew(vd.Initializer) {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "new Date(x) is not lowerable (only arg-less now-shape binds)"})
					return false
				}
				if vd.Type != nil {
					tn := vd.Type
					ref := tn.AsTypeReferenceNode()
					if tn.Kind != ast.KindTypeReference || ref == nil || ref.TypeName == nil ||
						ref.TypeName.Text() != "Date" {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "date annotation must be Date"})
						return false
					}
				}
				h := saLowerDateNew(w, scope, nextTemp)
				w.Write(fmt.Sprintf("  %s = %s\n", name, h))
				scope.types[name] = "date"
				continue
			}
			// 实例声明（`const o: C = new C(...)` 注解须同名；`let o = new C()` 推断）。
			ne := vd.Initializer.AsNewExpression()
			cname := ""
			if ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier {
				cname = ne.Expression.Text()
			}
			if _, ok := scope.classes[cname]; !ok {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + cname})
				return false
			}
			if vd.Type != nil {
				tn := vd.Type
				ref := tn.AsTypeReferenceNode()
				if tn.Kind != ast.KindTypeReference || ref == nil || ref.TypeName == nil ||
					ref.TypeName.Text() != cname {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "instance annotation must name its class"})
					return false
				}
			}
			h, msg := saLowerNewClass(w, cname, ne, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "inst:" + cname
			continue
		}
		if vd.Type == nil {
			// 无注解推断（形状证据：封存 lowerVarDeclList:1415-1418/1463-1464
			// 未知注解缺省 i32 + 按初值类型绑定）：数组字面量/数组句柄走 arr 通道，
			// true/false 走 bool，其余 i32 求值；缺 init 绑 i32 零值（const 缺 init 拒）。
			if !saLowerInferredDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		if vd.Type.Kind == ast.KindTypeReference {
			if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Text() == "Date" {
				// `: Date` 注解须配 date 种初值（new/now/parse/getTime/setter 链）。
				if vd.Initializer != nil && vd.Initializer.Kind == ast.KindCallExpression {
					if k, ok := saDateCallKind(vd.Initializer.AsCallExpression(), scope); ok && k == "date" {
						op, voidCall, msg := saEvalCall(w, vd.Initializer.AsCallExpression(), scope, pos, refusals, nextTemp)
						if msg != "" || voidCall {
							ln, col := pos(d.Pos())
							*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported date initializer: " + msg})
							return false
						}
						w.Write(fmt.Sprintf("  %s = %s\n", name, op))
						scope.types[name] = "date"
						continue
					}
				}
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "Date annotation needs a date value"})
				return false
			}
		}
		vkind, ok := saAnnotKind(vd.Type)
		if !ok || (vkind != "i32" && vkind != "bool" && vkind != "arr" && vkind != "str") {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported annotation (i32/bool/arr/str locals only)"})
			return false
		}
		if vkind == "arr" {
			if !saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		if vkind == "str" {
			if !saLowerStrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		if vd.Initializer == nil {
			if isConst {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
				return false
			}
			w.Write(fmt.Sprintf("  %s = 0\n", name))
			scope.types[name] = vkind
			continue
		}
		if vd.Initializer.Kind == ast.KindArrowFunction || vd.Initializer.Kind == ast.KindFunctionExpression {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function initializer not lowerable"})
			return false
		}
		var op string
		var msg string
		if vkind == "bool" {
			op, msg = saEvalBool(w, vd.Initializer, scope, pos, refusals, nextTemp)
		} else {
			op, msg = saEvalI32(w, vd.Initializer, scope, pos, refusals, nextTemp)
		}
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		scope.types[name] = vkind
	}
	return true
}

func saLowerInferredDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, name string, isConst bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer == nil {
		if isConst {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
			return false
		}
		w.Write(fmt.Sprintf("  %s = 0\n", name))
		scope.types[name] = "i32"
		return true
	}
	if vd.Initializer.Kind == ast.KindArrowFunction || vd.Initializer.Kind == ast.KindFunctionExpression {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function initializer not lowerable"})
		return false
	}
	if vd.Initializer.Kind == ast.KindArrayLiteralExpression {
		return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
	}
	if vd.Initializer.Kind == ast.KindStringLiteral || vd.Initializer.Kind == ast.KindNoSubstitutionTemplateLiteral ||
		vd.Initializer.Kind == ast.KindTemplateExpression {
		// 无注解串推断（字面量/模板皆串位）。
		h, msg := saEvalStr(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		scope.types[name] = "str"
		return true
	}
	if _, ok := saArrBase(scope, vd.Initializer); ok {
		return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
	}
	if vd.Initializer.Kind == ast.KindCallExpression {
		// 数组/串/date 返回调用按返回种建种。
		if saIsArrayCtor(vd.Initializer) {
			h, msg := saLowerArrayCtor(w, vd.Initializer, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "arr"
			return true
		}
		if k, ok := saArrCallRet(vd.Initializer.AsCallExpression(), scope); ok && k == "arr" {
			return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
		}
		if saCallIsStr(vd.Initializer.AsCallExpression(), scope) && !saStrCallIsI32(vd.Initializer.AsCallExpression(), scope) {
			h, msg := saEvalStr(w, vd.Initializer, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "str"
			return true
		}
		if k, ok := saDateCallKind(vd.Initializer.AsCallExpression(), scope); ok && k == "date" {
			op, voidCall, msg := saEvalCall(w, vd.Initializer.AsCallExpression(), scope, pos, refusals, nextTemp)
			if msg != "" || voidCall {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = "date"
			return true
		}
	}
	if vd.Initializer.Kind == ast.KindTrueKeyword || vd.Initializer.Kind == ast.KindFalseKeyword {
		op, msg := saEvalBool(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		scope.types[name] = "bool"
		return true
	}
	if vd.Initializer.Kind == ast.KindIdentifier {
		if k, ok := scope.types[vd.Initializer.Text()]; ok && k == "bool" {
			op, msg := saEvalBool(w, vd.Initializer, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = "bool"
			return true
		}
		if k, ok := scope.types[vd.Initializer.Text()]; ok && k == "date" {
			w.Write(fmt.Sprintf("  %s = %s\n", name, vd.Initializer.Text()))
			scope.types[name] = "date"
			return true
		}
	}
	op, msg := saEvalI32(w, vd.Initializer, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, op))
	scope.types[name] = "i32"
	return true
}
