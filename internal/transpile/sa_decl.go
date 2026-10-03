// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
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
		// `using`/`await using` 同旗（后者 NodeFlagsAwaitUsing 含 Using 位）；
		// 形状证据：封存 lowerVarDeclList:1395-1398。
		ln, col := pos(anchor.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "using declarations are not lowerable (explicit resource disposal has no SA-ASM scope-exit hook)"})
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
				ln, col := pos(vd.Initializer.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "inst:" + defname
			saConsumeOwn(scope, h)
			saDeclareOwned(scope, name)
			continue
		}
		if vd.Initializer != nil && vd.Initializer.Kind == ast.KindNewExpression {
			// `new Array(n)` 定长零数组（`new Array(a, b)` 落通用拒绝）。
			if saIsArrayCtor(vd.Initializer) {
				ne := vd.Initializer.AsNewExpression()
				if ne.Arguments == nil || len(ne.Arguments.Nodes) != 1 {
					ln, col := pos(vd.Initializer.Pos())
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
				saConsumeOwn(scope, h)
				saDeclareOwned(scope, name)
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
				saConsumeOwn(scope, h)
				saDeclareOwned(scope, name)
				continue
			}
			// `new Date()` 绑定为 date 种（millis 不透明；有参形大声拒）。
			if ne := vd.Initializer.AsNewExpression(); ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "Date" {
				if !saIsDateNew(vd.Initializer) {
					ln, col := pos(vd.Initializer.Pos())
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
				saConsumeOwn(scope, h)
				saDeclareOwned(scope, name)
				continue
			}
			// 实例声明（`const o: C = new C(...)` 注解须同名；`let o = new C()` 推断）。
			ne := vd.Initializer.AsNewExpression()
			cname := ""
			if ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier {
				cname = ne.Expression.Text()
			}
			if _, ok := scope.classes[cname]; !ok {
				ln, col := pos(vd.Initializer.Pos())
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
			saConsumeOwn(scope, h)
			saDeclareOwned(scope, name)
			// 实例函数字段捕获随具名绑定透传（`const b = new C(arrow)` 后去虚化；封存 trackBinding:1533-1537）。
			saCopyInstFn(scope, h, name)
			continue
		}
		if vd.Type == nil || saIsEraseAnnotation(vd.Type) {
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
							ln, col := pos(vd.Initializer.Pos())
							*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported date initializer: " + msg})
							return false
						}
						w.Write(fmt.Sprintf("  %s = %s\n", name, op))
						scope.types[name] = "date"
						saDeclareInitOwn(scope, name, op)
						continue
					}
				}
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "Date annotation needs a date value"})
				return false
			}
		}
		vkind, ok := saAnnotKind(vd.Type)
		if !ok {
			// 单标识符非泛型类型别名经顶层别名表消解（`type Count = i32`；
			// 封存 lowerVarDeclList:1462 注解语义消解同形；失败沿旧门）。
			vkind, ok = saResolveAliasKind(vd.Type, scope.aliasOf)
		}
		if !ok {
			// 具名接口/类注解记 inst（调用返回与字面量初值核对布局；
			// 形参与 saSynthParamNodes 类/接口分支同形）。
			vkind, ok = saAnnotInstKind(vd.Type, scope.classes)
		}
		if !ok {
			// 泛型具化/未知用户类型注解落 ptr 句柄（`Box<i32>` 无声明时；已记录
			// 类/接口名、别名、标量名一律不认（沿既有门），具化实例另步；形状证据：
			// 封存 saNameOfType:197-199 用户类型皆 ptr 句柄 + instantiateLayout:227-230
			// 未知模板回退裸布局；本仓句柄种为 arr，宽 8 对齐 8 与 widthOf 默认 8,8 同形）。
			vkind, ok = saGenericHandleKind(vd.Type, scope)
		}
		// 非折叠联合与 typeof 声明按初值种绑定（cf any 擦除；可折叠已由 saAnnotKind 办）。
		if vd.Type != nil && (vd.Type.Kind == ast.KindUnionType || vd.Type.Kind == ast.KindTypeQuery) {
			return saLowerInferredDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
		}
		if !ok || (vkind != "i32" && vkind != "bool" && vkind != "arr" && vkind != "str" && vkind != "f64" && !strings.HasPrefix(vkind, "inst:")) {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported annotation (i32/bool/arr/str locals only)"})
			return false
		}
		if vkind == "arr" {
			if !saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			if saIsStringArrayAnnot(vd.Type) {
				saMarkArrStr(scope, name)
			}
			continue
		}
		if vkind == "str" {
			if !saLowerStrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		if vkind == "f64" {
			// f64 binding (plain scalar words, never owned/released).
			if vd.Initializer == nil {
				if isConst {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
					return false
				}
				w.Write(fmt.Sprintf("  %s = 0\n", name))
				scope.types[name] = "f64"
				saDeclarePlain(scope, name)
				continue
			}
			op, msg := saEvalF64(w, vd.Initializer, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(vd.Initializer.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = "f64"
			saDeclarePlain(scope, name)
			continue
		}
		if strings.HasPrefix(vkind, "inst:") {
			// 实例注解配调用初值（被调返回种须同名；`p = make(…)` 封存
			// lowerCall 值返回同形；字面量/`new` 初值已在前分支办）。
			if vd.Initializer == nil || vd.Initializer.Kind != ast.KindCallExpression {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "struct annotation needs a struct call result"})
				return false
			}
			got, ok := saCallRetKind(vd.Initializer.AsCallExpression(), scope)
			if !ok || got != vkind {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "struct call return mismatch for " + name})
				return false
			}
			op, voidCall, msg := saEvalCall(w, vd.Initializer.AsCallExpression(), scope, pos, refusals, nextTemp)
			if msg != "" || voidCall {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported struct call: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = vkind
			if saIsTempOp(op) {
				saConsumeOwn(scope, op)
			}
			saDeclareOwned(scope, name)
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
			saDeclarePlain(scope, name)
			continue
		}
		if vd.Initializer.Kind == ast.KindArrowFunction || vd.Initializer.Kind == ast.KindFunctionExpression {
			// 局部箭头落 out-of-line 被调 + 调用别名（封存 lowerArrowBinding
			// 局部分支 :1058-1254）。
			if !saLowerLocalArrow(w, name, vd.Initializer, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp) {
				return false
			}
			continue
		}
		var op string
		var msg string
		if vkind == "bool" {
			op, msg = saEvalBool(w, vd.Initializer, scope, pos, refusals, nextTemp)
		} else {
			op, msg = saEvalI32(w, vd.Initializer, scope, pos, refusals, nextTemp)
		}
		if msg != "" {
			ln, col := pos(vd.Initializer.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		// f64 temps never coerce into i32/bool locals (stay loud).
		if k, ok := scope.types[op]; ok && k == "f64" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "float value needs float annotation"})
			return false
		}
		saEmitScalarInit(w, name, op, scope)
		scope.types[name] = vkind
		saDeclareInitOwn(scope, name, op)
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
		saDeclarePlain(scope, name)
		return true
	}
	// transparent wrappers peel before kind dispatch (type-level only).
	init := saUnwrapTransparent(vd.Initializer)
	if init.Kind == ast.KindArrowFunction || init.Kind == ast.KindFunctionExpression {
		// 局部箭头落 out-of-line 被调 + 调用别名（封存 lowerArrowBinding
		// 局部分支 :1058-1254）。
		return saLowerLocalArrow(w, name, init, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
	}
	if init.Kind == ast.KindArrayLiteralExpression {
		return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
	}
	if init.Kind == ast.KindStringLiteral || init.Kind == ast.KindNoSubstitutionTemplateLiteral ||
		init.Kind == ast.KindTemplateExpression || init.Kind == ast.KindTaggedTemplateExpression {
		// 无注解串推断（字面量/模板/tagged 皆串位；tag 门在求值内）。
		h, msg := saEvalStr(w, init, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		scope.types[name] = "str"
		saConsumeOwn(scope, h)
		saDeclareOwned(scope, name)
		return true
	}
	if _, ok := saArrBase(scope, init); ok {
		return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
	}
	if init.Kind == ast.KindCallExpression {
		// 数组/串/date 返回调用按返回种建种。
		if saIsArrayCtor(init) {
			h, msg := saLowerArrayCtor(w, init, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(init.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "arr"
			saConsumeOwn(scope, h)
			saDeclareOwned(scope, name)
			return true
		}
		if k, ok := saArrCallRet(init.AsCallExpression(), scope); ok && k == "arr" {
			return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
		}
		if saCallIsStr(init.AsCallExpression(), scope) && !saStrCallIsI32(init.AsCallExpression(), scope) {
			h, msg := saEvalStr(w, init, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(init.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "str"
			saConsumeOwn(scope, h)
			saDeclareOwned(scope, name)
			return true
		}
		if k, ok := saDateCallKind(init.AsCallExpression(), scope); ok && k == "date" {
			op, voidCall, msg := saEvalCall(w, init.AsCallExpression(), scope, pos, refusals, nextTemp)
			if msg != "" || voidCall {
				ln, col := pos(init.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = "date"
			saDeclareInitOwn(scope, name, op)
			return true
		}
	}
	if init.Kind == ast.KindTrueKeyword || init.Kind == ast.KindFalseKeyword {
		op, msg := saEvalBool(w, init, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		saEmitScalarInit(w, name, op, scope)
		scope.types[name] = "bool"
		saDeclareInitOwn(scope, name, op)
		return true
	}
	if init.Kind == ast.KindIdentifier {
		if k, ok := scope.types[init.Text()]; ok && k == "bool" {
			op, msg := saEvalBool(w, init, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			saEmitScalarInit(w, name, op, scope)
			scope.types[name] = "bool"
			saDeclarePlain(scope, name)
			return true
		}
		if k, ok := scope.types[init.Text()]; ok && k == "date" {
			saEmitScalarInit(w, name, init.Text(), scope)
			scope.types[name] = "date"
			saDeclarePlain(scope, name)
			return true
		}
	}
	if init.Kind == ast.KindConditionalExpression {
		// 无注解三元推断（i32/串臂与 return 位同核；分歧沿核拒）。
		ce := init.AsConditionalExpression()
		t, isStr, msg := saLowerTernaryValue(w, ce, init, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
		if msg != "" {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, t))
		if isStr {
			scope.types[name] = "str"
			saConsumeOwn(scope, t)
			saDeclareOwned(scope, name)
		} else if k, ok := scope.types[t]; ok && k == "f64" {
			scope.types[name] = "f64"
			saDeclarePlain(scope, name)
		} else {
			// i32 三元值记种(str/f64 臂已记；缺此行下游报 unknown).
			scope.types[name] = "i32"
			saDeclareInitOwn(scope, name, t)
		}
		return true
	}
	op, msg := saEvalI32(w, init, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(init.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
		return false
	}
	// 句柄读回种传递（字段读已记临时量种；字面量/绑定名沿既有 i32）。
	// 句柄直传（上游此处拒，本仓沿既有直传口径；快照只对标量，`add` 不可作用 ptr）。
	if k, ok := scope.types[op]; ok && k == "f64" {
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		scope.types[name] = "f64"
		saDeclarePlain(scope, name)
	} else if k, ok := scope.types[op]; ok && (k == "arr" || k == "str" || (len(k) > 5 && k[:5] == "inst:")) {
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		scope.types[name] = k
		saConsumeOwn(scope, op)
		saDeclareOwned(scope, name)
		saCopyInstFn(scope, op, name)
		saPropArrNest(scope, op, name)
	} else {
		saEmitScalarInit(w, name, op, scope)
		scope.types[name] = "i32"
		saDeclareInitOwn(scope, name, op)
	}
	return true
}

// sa_decl.go 后半 — 顶层 const 函数值（`const f = (...)=>...` / `= function...`）。
// 归声明域（transpile.go 禁巨无霸：仅留 saLowerSourceFile 编排 + 函数签名核）。
// 形状证据：封存 tryTopLevelArrow:1015-1032 + lowerArrowBinding:1058-1098。

// saSynthArrowParams 合成箭头/函数表达式形参表（与 saSynthParams 同核；
// 形状证据：封存 lowerArrowBinding:1074-1098，模式走同 hiddenDestructuredParam）。
// saNodeTypeParams extracts own type parameters from function-like nodes (nil-safe).
func saNodeTypeParams(n *ast.Node) *ast.TypeParameterList {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case ast.KindArrowFunction:
		if af := n.AsArrowFunction(); af != nil {
			return af.TypeParameters
		}
	case ast.KindFunctionExpression:
		if fe := n.AsFunctionExpression(); fe != nil {
			return fe.TypeParameters
		}
	}
	return nil
}

func saSynthArrowParams(arrow *ast.Node, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, enums map[string]map[string]int64) ([]string, map[string]string, []saDestructurePending, bool) {
	var nodes []*ast.Node
	if pl := arrow.ParameterList(); pl != nil {
		nodes = pl.Nodes
	}
	return saSynthParamNodes(nodes, classes, aliasOf, enums, saTypeParamSet(saNodeTypeParams(arrow)))
}

// saArrowParamNames 合成箭头形参名表（标识符直通；模式取隐藏名，与 saParamNames 同序）。
func saArrowParamNames(arrow *ast.Node) ([]string, bool) {
	var nodes []*ast.Node
	if pl := arrow.ParameterList(); pl != nil {
		nodes = pl.Nodes
	}
	var out []string
	taken := map[string]bool{}
	for _, p := range nodes {
		pd := p.AsParameterDeclaration()
		if pd == nil || pd.DotDotDotToken != nil || pd.QuestionToken != nil {
			return nil, false
		}
		nm := pd.Name()
		if nm == nil {
			return nil, false
		}
		if nm.Kind != ast.KindIdentifier && pd.Initializer != nil {
			return nil, false
		}
		if nm.Kind == ast.KindIdentifier {
			out = append(out, nm.Text())
			taken[nm.Text()] = true
			continue
		}
		if nm.Kind != ast.KindObjectBindingPattern && nm.Kind != ast.KindArrayBindingPattern {
			return nil, false
		}
		hid := "__darg"
		for taken[hid] {
			hid += "_"
		}
		taken[hid] = true
		out = append(out, hid)
	}
	return out, true
}

// saFuncDefaultTables 记录每形参缺省（与形参同长；有 Initializer 即 true，
// 缺省表达式原节点供短调字面量回放；非字面量短调大声拒；形状证据：封存
// funcDefaults/funcDefaultExpr + padDefaultArgs）。
func saFuncDefaultTables(nodes []*ast.Node) ([]bool, []*ast.Node) {
	defs := make([]bool, len(nodes))
	dexprs := make([]*ast.Node, len(nodes))
	for i, p := range nodes {
		pd := p.AsParameterDeclaration()
		if pd != nil && pd.Initializer != nil {
			defs[i] = true
			dexprs[i] = pd.Initializer
		}
	}
	return defs, dexprs
}

// saArrowReturnNode 取箭头/函数表达式的返回注解（无注解 nil；形状证据：
// 封存 lowerArrowBinding:1059-1068）。
func saArrowReturnNode(arrow *ast.Node) *ast.TypeNode {
	switch arrow.Kind {
	case ast.KindArrowFunction:
		return arrow.AsArrowFunction().Type
	case ast.KindFunctionExpression:
		return arrow.AsFunctionExpression().Type
	default:
		return nil
	}
}

// saCollectTypeAliases 收集顶层 `type X = …`（名→目标类型节点；只记表，无码）。
// 封存 link_erasure 纯类型擦除同形（类型别名无运行时形）。
func saCollectTypeAliases(sf *ast.SourceFile) map[string]*ast.TypeNode {
	out := map[string]*ast.TypeNode{}
	for _, st := range sf.Statements.Nodes {
		if st.Kind != ast.KindTypeAliasDeclaration {
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
		out[nm.Text()] = ta.Type
	}
	return out
}

// saResolveAliasKind 消解单标识符非泛型类型别名到标量种（链式跟随、防环）。
// 仅标量拼写经既有 `saAnnotKind` 复用（arr/str 走各自声明路径，无新语义）；
// 泛型实例化/未知目标一律 false，调用方沿旧门大声拒。
func saResolveAliasKind(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if t == nil || t.Kind != ast.KindTypeReference || len(aliasOf) == 0 {
		return "", false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	if ref.TypeArguments != nil {
		return "", false
	}
	seen := map[string]bool{}
	cur := ref.TypeName.Text()
	for i := 0; i < len(aliasOf)+1; i++ {
		if seen[cur] {
			return "", false
		}
		seen[cur] = true
		tgt, ok := aliasOf[cur]
		if !ok || tgt == nil {
			return "", false
		}
		if k, ok := saAnnotKind(tgt); ok {
			return k, true
		}
		// 目标仍为单标识符引用则继续跟随，否则（如接口/字面量）止步拒。
		nr := tgt.AsTypeReferenceNode()
		if tgt.Kind != ast.KindTypeReference || nr == nil || nr.TypeName == nil ||
			nr.TypeName.Kind != ast.KindIdentifier || nr.TypeArguments != nil {
			return "", false
		}
		cur = nr.TypeName.Text()
	}
	return "", false
}

// saReturnKindRef 消解返回注解（saReturnKind 标量集之外）：单标识符别名经
// 别名表映回 number/boolean/string（`type Count = i32`；arr 别名在返回位仍拒，
// 无用例）；具名接口/类经 classes 表记 `inst:Name`（`-> ptr`，封存上游实发
// `@make(x: i32, y: i32) -> ptr:`）。泛型实例化/未知名沿旧门 false。
// 注意 160 分歧：上游把 i32 别名参数/返回标 `ptr`（疑似未消解回退），本仓按
// 别名语义消解为 i32——值流一致且更忠实，禁静默错码高于逐字同形。
func saReturnKindRef(t *ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if k, ok := saReturnKind(t); ok {
		return k, true
	}
	if t == nil || t.Kind != ast.KindTypeReference {
		return "", false
	}
	if k, ok := saResolveAliasKind(t, aliasOf); ok {
		switch k {
		case "i32":
			return "number", true
		case "bool":
			return "boolean", true
		case "str":
			return "string", true
		}
		return "", false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	if ref.TypeArguments != nil {
		return "", false
	}
	if _, ok := classes[ref.TypeName.Text()]; ok {
		return "inst:" + ref.TypeName.Text(), true
	}
	return "", false
}

// saGenericHandleKind 认未记录泛型/用户类型注解为句柄种（`Box<i32>`、裸 `Box`
// 无声明时；已记录类/接口名、别名、标量名一律不认（沿既有门）；调用方按初值
// 通道求值绑定（alloc/字面量/句柄直传），具化实例（`Box<i32>` 有声明时）另步。
func saGenericHandleKind(t *ast.TypeNode, scope *saScope) (string, bool) {
	if t == nil || t.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	base := ref.TypeName.Text()
	switch base {
	case "i32", "u32", "i64", "u64", "f64", "f32", "i8", "u8", "i16", "u16",
		"number", "boolean", "string", "void", "ptr", "bool":
		return "", false
	}
	if _, ok := scope.classes[base]; ok {
		return "", false
	}
	if _, ok := scope.aliasOf[base]; ok {
		return "", false
	}
	return "arr", true
}

// saIsEraseAnnotation reports an erased annotation (`any`/`unknown`/`never`: binds by
// initializer kind).
func saIsEraseAnnotation(t *ast.TypeNode) bool {
	if t == nil {
		return false
	}
	return t.Kind == ast.KindAnyKeyword || t.Kind == ast.KindUnknownKeyword || t.Kind == ast.KindNeverKeyword
}

// saAnnotInstKind 消解具名接口/类注解为 `inst:Name`（单标识符、无泛型实参、
// 经 classes 表；形参/返回/声明三处注解同源）。
func saAnnotInstKind(t *ast.TypeNode, classes map[string]*saClassDef) (string, bool) {
	if t == nil || t.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	if ref.TypeArguments != nil {
		return "", false
	}
	if _, ok := classes[ref.TypeName.Text()]; ok {
		return "inst:" + ref.TypeName.Text(), true
	}
	return "", false
}

// saDeclareInitOwn 按初值操作数登记归属（temp 源归属并消费源，具名/
// 立即数源普通；封存 assignLocal fresh 分支：temp→declareOwned，
// named/imm→declarePlain）。
// saEmitScalarInit 按初值源落声明存：具名源快照（`y = add x, 0`，读后源
// 仍 live，后续 `!x` 合法），temp/imm 直赋；封存 assignLocal fresh+named
// 分支快照同形（:11130-11140）。
func saEmitScalarInit(w printer.EmitTextWriter, name, op string, scope *saScope) {
	if !saIsTempOp(op) {
		if _, ok := scope.types[op]; ok {
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", name, op))
			return
		}
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, op))
}

func saDeclareInitOwn(scope *saScope, name, op string) {
	if saIsTempOp(op) {
		saConsumeOwn(scope, op)
		saDeclareOwned(scope, name)
		return
	}
	saDeclarePlain(scope, name)
}

// saIsTopLevelArrowConst 识别顶层 `const f = (...)=>...`/`= function...`
// （单声明、标识符名、箭头/函数表达式初值；形状证据：封存 tryTopLevelArrow:1015-1032）。
// 其余顶层变量声明不在此口径（调用方落既有拒绝）。
func saIsTopLevelArrowConst(st *ast.Node) (string, *ast.Node, bool) {
	if st.Kind != ast.KindVariableStatement {
		return "", nil, false
	}
	vs := st.AsVariableStatement()
	if vs == nil || vs.DeclarationList == nil {
		return "", nil, false
	}
	dl := vs.DeclarationList.AsVariableDeclarationList()
	if dl == nil || len(dl.Declarations.Nodes) != 1 {
		return "", nil, false
	}
	d := dl.Declarations.Nodes[0]
	vd := d.AsVariableDeclaration()
	if vd == nil || vd.Initializer == nil {
		return "", nil, false
	}
	nm := vd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", nil, false
	}
	init := vd.Initializer
	if init.Kind != ast.KindArrowFunction && init.Kind != ast.KindFunctionExpression {
		return "", nil, false
	}
	return nm.Text(), init, true
}

// saFoldTopLevelConst 折叠顶层纯量声明（`var K = 42` 内联文本、
// `var S = "hi"` 串池化、`var f = Math.g` 别名；两遍：验全纯再记，
// 部分纯洁不记半吊子；非纯（require 等）返回 false 留发射环拒。
// 可变顶层（函数内赋值）无槽，另域 modstate。
// 形状证据：封存 tryTopLevelConst:2894-2972。
func saFoldTopLevelConst(st *ast.Node, consts map[string]string, strs map[string]bool, maths map[string]string, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if st.Kind != ast.KindVariableStatement {
		return false
	}
	vs := st.AsVariableStatement()
	if vs == nil || vs.DeclarationList == nil {
		return false
	}
	dl := vs.DeclarationList.AsVariableDeclarationList()
	if dl == nil || len(dl.Declarations.Nodes) == 0 {
		return false
	}
	// `using`/`await using` 同旗（后者含 Using 位），显式释放无 SA 域退出钩子，
	// 顶层亦大声拒（不可静默吞掉；封存 lowerVarDeclList:1395-1398 同门）。
	if dl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "using declarations are not lowerable (explicit resource disposal has no SA-ASM scope-exit hook)"})
		return true
	}
	// 逐 declarator 即收即写（后 declarator 可前向读同句/前句已折纯量；
	// 静默错译止血，形状证据：封存 multi-const 逐 declarator + 按名折叠）。
	for _, d := range dl.Declarations.Nodes {
		vd := d.AsVariableDeclaration()
		if vd == nil {
			return false
		}
		nm := vd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			return false
		}
		init := vd.Initializer
		if init == nil {
			return false
		}
		switch init.Kind {
		case ast.KindNumericLiteral:
			if saIsFloatLit(init.Text()) {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "float top-level const is beyond the i32 subset"})
				return true
			}
			consts[nm.Text()] = init.Text()
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
			consts[nm.Text()] = init.Text()
			strs[nm.Text()] = true
		case ast.KindTrueKeyword:
			consts[nm.Text()] = "1"
		case ast.KindFalseKeyword:
			consts[nm.Text()] = "0"
		case ast.KindPropertyAccessExpression:
			m, ok := saMathMethodName(init)
			if !ok {
				return false
			}
			maths[nm.Text()] = m
		case ast.KindIdentifier:
			if m, ok := maths[init.Text()]; ok {
				maths[nm.Text()] = m
				continue
			}
			if t, ok := consts[init.Text()]; ok {
				consts[nm.Text()] = t
				if strs[init.Text()] {
					strs[nm.Text()] = true
				}
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

// saIsAmbientModule 报告类型-only 模块块（`declare` 修饰、串名
// （`declare module "./x"`）、`declare global`；皆擦除；形状证据：封存
// isAmbientModule:94-105）。
func saIsAmbientModule(st *ast.Node) bool {
	if st == nil || st.Kind != ast.KindModuleDeclaration {
		return false
	}
	if ast.HasModifier(st, ast.ModifierFlagsAmbient) {
		return true
	}
	md := st.AsModuleDeclaration()
	if nm := md.Name(); nm != nil && nm.Kind == ast.KindStringLiteral {
		return true
	}
	return false
}

// saFoldNamespaceConsts 折叠单层 `namespace N { export const K = <纯字面> }`
// 为 `N.K` 拍扁纯量（数字/串/true/false；复用顶层折叠值域，不含 Math 别名与
// 标识符链）。非 export/非纯量/函数/类/嵌套 namespace 成员一律整块不折，
// 调用方沿旧拒（loud；命名空间函数/跨文件链接另立大项）。
// 形状证据：封存 nsPreScan qualified 注册 + lowerPendingNamespaces 常量子集。
func saFoldNamespaceConsts(st *ast.Node, consts map[string]string, strs map[string]bool, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if st == nil || st.Kind != ast.KindModuleDeclaration {
		return false
	}
	if saIsAmbientModule(st) {
		return false
	}
	md := st.AsModuleDeclaration()
	nm := md.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return false
	}
	if md.Body == nil || md.Body.Kind != ast.KindModuleBlock {
		return false
	}
	ns := nm.Text()
	seen := map[string]bool{}
	folded := 0
	for _, m := range md.Body.AsModuleBlock().Statements.Nodes {
		if m == nil || m.Kind != ast.KindVariableStatement {
			return false
		}
		if !ast.HasModifier(m, ast.ModifierFlagsExport) {
			return false
		}
		vs := m.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			return false
		}
		dl := vs.DeclarationList.AsVariableDeclarationList()
		if dl == nil || len(dl.Declarations.Nodes) == 0 {
			return false
		}
		if dl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "using declarations are not lowerable (explicit resource disposal has no SA-ASM scope-exit hook)"})
			return true
		}
		for _, d := range dl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil {
				return false
			}
			vnm := vd.Name()
			if vnm == nil || vnm.Kind != ast.KindIdentifier {
				return false
			}
			if seen[vnm.Text()] {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate const " + ns + "." + vnm.Text()})
				return true
			}
			seen[vnm.Text()] = true
			init := vd.Initializer
			if init == nil {
				return false
			}
			switch init.Kind {
			case ast.KindNumericLiteral:
				if saIsFloatLit(init.Text()) {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "float top-level const is beyond the i32 subset"})
					return true
				}
				consts[ns+"."+vnm.Text()] = init.Text()
				folded++
			case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
				consts[ns+"."+vnm.Text()] = init.Text()
				strs[ns+"."+vnm.Text()] = true
				folded++
			case ast.KindTrueKeyword:
				consts[ns+"."+vnm.Text()] = "1"
				folded++
			case ast.KindFalseKeyword:
				consts[ns+"."+vnm.Text()] = "0"
				folded++
			case ast.KindIdentifier:
				// 同 NS 前向读（按名折叠；串性透传；未定义沿旧拒）。
				if t, ok := consts[ns+"."+init.Text()]; ok {
					consts[ns+"."+vnm.Text()] = t
					if strs[ns+"."+init.Text()] {
						strs[ns+"."+vnm.Text()] = true
					}
					folded++
					continue
				}
				return false
			default:
				return false
			}
		}
	}
	if folded == 0 {
		return false
	}
	return true
}

// saLowerArrowConst lowering 顶层 `const f = (...)=>...`/`= function...`
// （out-of-line 被调，与函数声明同形；形状证据：封存 tryTopLevelArrow:1015-1032
// + lowerArrowBinding:1058-1098）。仅顶层无捕获口径：体引用未知名走既有求值
// 大声拒；生成器/async 形大声拒；表达式体单值返回，无注解值体仍按函数同例拒。
func saLowerArrowConst(w printer.EmitTextWriter, name string, arrow *ast.Node, funcs map[string]saFuncSig, enums map[string]map[string]int64, enumNonInt map[string]map[string]bool, classes map[string]*saClassDef, topConsts map[string]string, topStr map[string]bool, topMaths map[string]string, modVars map[string]*saModState, src string, mainRenamed bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool, tcx *saTypeCtx, aliasOf map[string]*ast.TypeNode, imports, importRemote map[string]string) {
	if arrow.Kind == ast.KindFunctionExpression {
		if fe := arrow.AsFunctionExpression(); fe != nil && fe.AsteriskToken != nil {
			ln, col := pos(arrow.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "generators are not lowerable"})
			return
		}
	}
	if ast.HasModifier(arrow, ast.ModifierFlagsAsync) {
		ln, col := pos(arrow.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "async functions are not lowerable"})
		return
	}
	params, ok := saArrowParamNames(arrow)
	if !ok {
		ln, col := pos(arrow.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported parameters"})
		return
	}
	// 形参种先行（签名注解与归属登记同源；封存 lowerArrowBinding 先合成形参同形）。
	_, kinds, arrowPendings, ok := saSynthArrowParams(arrow, classes, aliasOf, enums)
	if !ok {
		ln, col := pos(arrow.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported parameter annotation (i32/bool/arr/str/inst only)"})
		return
	}
	retKind, isVoid := "void", true
	if rt := saArrowReturnNode(arrow); rt != nil {
		rtype := saUnwrapPromise(rt)
		if saIsBareTypeParam(rtype, saTypeParamSet(saNodeTypeParams(arrow))) {
			// erased own type parameter defaults to number.
			retKind, isVoid = "number", false
		} else if k, ok := saReturnKindRef(rtype, classes, aliasOf); !ok {
			ln, col := pos(arrow.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported return annotation"})
			return
		} else {
			retKind, isVoid = k, k == "void"
		}
	} else if k, v, ok := saPrescanRet(nil, arrow, tcx, classes, aliasOf); ok {
		retKind, isVoid = k, v
	}
	emitName := name
	if emitName == "main" && mainRenamed {
		emitName = "main__user"
	}
	sig := "@" + emitName + "(" + saSigParamList(kinds, params) + ")"
	if !isVoid {
		sig += saSigRetSuffix(retKind)
	}
	sig += ":\n"
	w.Write(sig)
	body := arrow.Body()
	if body == nil {
		ln, col := pos(arrow.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function value " + name + " has no body"})
		return
	}
	scope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, enumNonInt: enumNonInt, classes: classes, topConsts: topConsts, topStr: topStr, modVars: modVars, mainRenamed: mainRenamed, nextLabel: nextLabel, retKind: retKind, strPool: strPool, src: src, addImport: needImport, aliasOf: aliasOf, imports: imports, importRemote: importRemote}
	saSeedTopMaths(scope, topMaths)
	for _, p := range params {
		scope.types[p] = kinds[p]
		saDeclareOwned(scope, p)
	}
	if len(arrowPendings) > 0 {
		if !saDrainDestructuredParams(w, arrowPendings, scope, pos, refusals, nextLabel, nextTemp) {
			return
		}
	}
	if !saLowerArrowBody(w, arrow, body, isVoid, retKind, scope, pos, refusals, needImport, nextLabel, nextTemp) {
		return
	}
}

// saLowerArrowBody lowering 箭头体（块体逐语句/终结判 + 缺尾 ret 补；表达式体单值
// ret）。顶层箭头与局部箭头共用同一条体路径。
// 形状证据：封存 lowerArrowBinding 体内 lowering（:1174-1253 体作用域 save/restore
// 后逐语句）与表达式体 `ret <expr>`（:1224-1232）。
func saLowerArrowBody(w printer.EmitTextWriter, arrow *ast.Node, body *ast.Node, isVoid bool, retKind string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	refuse := func(n *ast.Node, msg string) bool {
		ln, col := pos(n.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false
	}
	if body.Kind == ast.KindBlock {
		stmts, ok := saBlockStmts(body)
		if !ok {
			return refuse(arrow, "unsupported body")
		}
		if len(stmts) == 0 {
			if !isVoid {
				return refuse(arrow, "missing return")
			}
			saReleaseAllOwnedExcept(w, scope, "")
			w.Write("  ret\n")
			return true
		}
		terminated := false
		for _, s := range stmts {
			if terminated {
				return refuse(s, "unreachable code after terminating statement")
			}
			if done, failed := saLowerStmt(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp); failed {
				return false
			} else if done {
				terminated = true
			}
		}
		if !terminated {
			if !isVoid {
				return refuse(arrow, "missing return")
			}
			saReleaseAllOwnedExcept(w, scope, "")
			w.Write("  ret\n")
		} else if saEndsWithBareSwitchLabel(stmts) {
			saReleaseAllOwnedExcept(w, scope, "")
			if isVoid {
				w.Write("  ret\n")
			} else {
				w.Write("  ret 0\n")
			}
		}
		return true
	}
	if isVoid {
		return refuse(body, "unsupported void arrow expression body")
	}
	op, msg := saEvalReturnOperand(w, body, retKind, scope, pos, refusals, nextTemp)
	if msg != "" {
		return refuse(body, msg)
	}
	saReleaseExceptOp(w, scope, op)
	w.Write(fmt.Sprintf("  ret %s\n", op))
	return true
}

// ── 局部箭头值（out-of-line 被调 + 尾随捕获形参）──
// 形状证据：封存 lowerArrowBinding:1058-1254（局部 topLevel=false 分支：
// 生成 `@prefix__arrow_N(params..., captures...)` 落 pendingFuncs 末尾排空
// :576-579，局部名记调用别名 arrowAliases:588 + declarePlain:1256-1258），
// 捕获集取自由标识符减形参/体内声明/全局/被调名，命中外层绑定者按排序
// 追加为尾参（:1120-1141 + captureSig:1336-1343）。
// 本仓以 `scope.types[name] = "fn:<gen>"` 承载别名（块域随 saScopeExit
// 一起回滚，且重复声明照旧 `duplicate local`）；非调用位读到 "fn:" 种沿
// i32/串读位既有异种门大声拒，绝不当值用。

// saPureTypeKinds 纯类型子树（值使用集永不入；镜像封存 pureTypeKinds:20-46）。
var saPureTypeKinds = map[ast.Kind]bool{
	ast.KindTypeReference: true, ast.KindTypeQuery: true, ast.KindTypeLiteral: true,
	ast.KindTupleType: true, ast.KindArrayType: true, ast.KindUnionType: true,
	ast.KindIntersectionType: true, ast.KindFunctionType: true, ast.KindConstructorType: true,
	ast.KindTypeOperator: true, ast.KindIndexedAccessType: true, ast.KindMappedType: true,
	ast.KindLiteralType: true, ast.KindOptionalType: true, ast.KindRestType: true,
	ast.KindTypeParameter: true, ast.KindTypePredicate: true, ast.KindThisType: true,
	ast.KindTemplateLiteralType: true, ast.KindParenthesizedType: true,
	ast.KindMethodSignature: true, ast.KindPropertySignature: true,
	ast.KindCallSignature: true, ast.KindConstructSignature: true,
	ast.KindIndexSignature: true,
}

// saBindingNameKinds 绑定名（非使用）子树（镜像封存 bindingNameKinds:52-70）。
var saBindingNameKinds = map[ast.Kind]bool{
	ast.KindVariableDeclaration: true, ast.KindParameter: true, ast.KindBindingElement: true,
	ast.KindFunctionDeclaration: true, ast.KindFunctionExpression: true,
	ast.KindClassDeclaration: true, ast.KindClassExpression: true,
	ast.KindEnumDeclaration: true, ast.KindEnumMember: true,
	ast.KindInterfaceDeclaration: true, ast.KindTypeAliasDeclaration: true,
	ast.KindMethodDeclaration: true, ast.KindGetAccessor: true, ast.KindSetAccessor: true,
	ast.KindPropertyAssignment: true, ast.KindPropertyAccessExpression: true,
}

// saValueUsedNames 收集值位标识符（镜像封存 valueUsedNames:80-131：绑定名跳过、
// 纯类型子树不入、heritage 保留）。捕获集与别域堵漏共用。
func saValueUsedNames(stmts []*ast.Node) map[string]bool {
	used := map[string]bool{}
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil {
			return
		}
		if n.Kind == ast.KindIdentifier {
			used[n.Text()] = true
			return
		}
		var skip *ast.Node
		if saBindingNameKinds[n.Kind] {
			if nm := n.Name(); nm != nil {
				skip = nm
			}
		}
		n.ForEachChild(func(c *ast.Node) bool {
			if skip != nil && c == skip {
				return false
			}
			if saPureTypeKinds[c.Kind] {
				return false
			}
			walk(c)
			return false
		})
	}
	for _, st := range stmts {
		walk(st)
	}
	return used
}

// saArrowDeclaredNames 收集箭头体内的声明名（镜像封存 collectDeclaredNames:1352-1370）。
func saArrowDeclaredNames(n *ast.Node, out map[string]bool) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindVariableDeclaration, ast.KindParameter, ast.KindBindingElement:
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			out[nm.Text()] = true
		}
	case ast.KindFunctionDeclaration, ast.KindClassDeclaration,
		ast.KindInterfaceDeclaration, ast.KindEnumDeclaration:
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			out[nm.Text()] = true
		}
	}
	n.ForEachChild(func(c *ast.Node) bool {
		saArrowDeclaredNames(c, out)
		return false
	})
}

// saArrowCaptures 取箭头体自由标识符中命中外层绑定者（排序；封存 :1120-1141）。
func saArrowCaptures(body *ast.Node, name string, paramNames []string, scope *saScope) []string {
	var stmts []*ast.Node
	if body != nil {
		if body.Kind == ast.KindBlock {
			if b := body.AsBlock(); b != nil && b.Statements != nil {
				stmts = b.Statements.Nodes
			}
		} else {
			stmts = []*ast.Node{body}
		}
	}
	uses := saValueUsedNames(stmts)
	decls := map[string]bool{name: true}
	for _, p := range paramNames {
		decls[p] = true
	}
	saArrowDeclaredNames(body, decls)
	for fn := range scope.funcs {
		decls[fn] = true
	}
	for _, g := range []string{"console", "Math", "String", "Number", "Array", "Map", "Set",
		"undefined", "null", "true", "false", "Object", "JSON", "Date", "RegExp", "Promise"} {
		decls[g] = true
	}
	caps := []string{}
	for id := range uses {
		if decls[id] {
			continue
		}
		if _, ok := scope.types[id]; ok {
			caps = append(caps, id)
			continue
		}
		if _, ok := scope.mathAlias[id]; ok {
			caps = append(caps, id)
		}
	}
	sort.Strings(caps)
	return caps
}

// saLowerLocalArrow lowering 局部 `let f = (…) => …` / `= function …`：
// 生成 out-of-line `@__arrow_N` 并把局部名记为调用别名（不落字）。
// 返回 false 即已落拒因。形状证据：封存 lowerArrowBinding:1058-1254。
func saLowerLocalArrow(w printer.EmitTextWriter, name string, arrow *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	refuse := func(n *ast.Node, msg string) bool {
		ln, col := pos(n.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false
	}
	if arrow.Kind == ast.KindFunctionExpression {
		if fe := arrow.AsFunctionExpression(); fe != nil && fe.AsteriskToken != nil {
			return refuse(arrow, "generators are not lowerable")
		}
	}
	if ast.HasModifier(arrow, ast.ModifierFlagsAsync) {
		return refuse(arrow, "async functions are not lowerable")
	}
	body := arrow.Body()
	if body == nil {
		return refuse(arrow, "function value "+name+" has no body")
	}
	params, ok := saArrowParamNames(arrow)
	if !ok {
		return refuse(arrow, "unsupported parameters")
	}
	paramKinds, synthOK := map[string]string(nil), false
	var pendings []saDestructurePending
	var captured []string
	if params, paramKinds, pendings, synthOK = saSynthArrowParams(arrow, scope.classes, scope.aliasOf, scope.enums); !synthOK {
		return refuse(arrow, "unsupported parameter annotation (i32/bool/arr/str/inst only)")
	}
	// 返回种（严格上游序，封存 :1099-1112）：显注解 > 表达式体/有形参即值
	// 函数 i32 > checker 推断 > void。checker 回退 void 不得吞掉 value_fn 规则。
	retKind, isVoid := "void", true
	if rt := saArrowReturnNode(arrow); rt != nil {
		rtype := saUnwrapPromise(rt)
		if saIsBareTypeParam(rtype, saTypeParamSet(saNodeTypeParams(arrow))) {
			// erased own type parameter defaults to number.
			retKind, isVoid = "number", false
		} else if k, kok := saReturnKindRef(rtype, scope.classes, scope.aliasOf); !kok {
			return refuse(arrow, "unsupported return annotation")
		} else {
			retKind, isVoid = k, k == "void"
		}
	} else if body.Kind != ast.KindBlock || len(params) > 0 {
		retKind, isVoid = "i32", false
	} else if k, v, pok := saPrescanRet(nil, arrow, scope.tcx, scope.classes, scope.aliasOf); pok {
		retKind, isVoid = k, v
	}
	captured = saArrowCaptures(body, name, params, scope)
	// 函数值捕获无值形可传，大声拒（体读到 "fn:" 种在既有读门亦拒；此处先定位）。
	for _, cp := range captured {
		if strings.HasPrefix(scope.types[cp], "fn:") {
			return refuse(arrow, "function value capture "+cp+" is not lowerable")
		}
	}
	// out-of-line 发射：换新 builder 与作用域状态（封存 :1174-1253 save/restore）。
	buf := printer.NewTextWriter("\n", 2)
	*scope.arrowSeq++
	gen := fmt.Sprintf("__arrow_%d", *scope.arrowSeq)
	sigNames := append(append([]string{}, params...), captured...)
	allKinds := map[string]string{}
	for k, v := range paramKinds {
		allKinds[k] = v
	}
	for _, cp := range captured {
		allKinds[cp] = scope.types[cp]
	}
	buf.Write("@" + gen + "(" + saSigParamList(allKinds, sigNames) + ")")
	if !isVoid {
		buf.Write(saSigRetSuffix(retKind))
	}
	buf.Write(":\n")
	inner := &saScope{types: map[string]string{}, funcs: scope.funcs, enums: scope.enums,
		enumNonInt: scope.enumNonInt, classes: scope.classes, topConsts: scope.topConsts,
		topStr: scope.topStr, modVars: scope.modVars, mainRenamed: scope.mainRenamed,
		nextLabel: scope.nextLabel, retKind: retKind, strPool: scope.strPool, src: scope.src,
		addImport: needImport, tcx: scope.tcx, pendingFns: scope.pendingFns, arrowSeq: scope.arrowSeq, aliasOf: scope.aliasOf, imports: scope.imports, importRemote: scope.importRemote}
	// 内层继承外层 math 别名（Math.* 方法体内可用；顶层 maths 已在外层种入）。
	saSeedTopMaths(inner, scope.mathAlias)
	for k, v := range paramKinds {
		inner.types[k] = v
	}
	// 捕获以同名尾参入内层作用域（体读名即读形参；封存 declareOwned 补登）。
	for _, cp := range captured {
		inner.types[cp] = scope.types[cp]
	}
	// 形参+捕获按签名序归属登记（逆序释放依赖此序）。
	for _, p := range sigNames {
		saDeclareOwned(inner, p)
	}
	if len(pendings) > 0 {
		if !saDrainDestructuredParams(buf, pendings, inner, pos, refusals, nextLabel, nextTemp) {
			return false
		}
	}
	if !saLowerArrowBody(buf, arrow, body, isVoid, retKind, inner, pos, refusals, needImport, nextLabel, nextTemp) {
		return false
	}
	*scope.pendingFns = append(*scope.pendingFns, buf.String())
	// 签名入表（调用核按名分发；arrowCaps 有序记尾随捕获实参）+ 局部名记别名。
	scope.funcs[gen] = saFuncSig{params: len(params), isVoid: isVoid, retKind: retKind, paramKinds: saSigKinds(paramKinds, params), arrowCaps: captured}
	scope.types[name] = "fn:" + gen
	return true
}

// saSigRetSuffix 返回签名后缀（string/inst 句柄即 ptr，其余 i32；
 // 封存上游实发 `-> ptr`（串）与 `@make(…) -> ptr:`（实例））。
func saSigRetSuffix(retKind string) string {
	if retKind == "string" || strings.HasPrefix(retKind, "inst:") {
		return " -> ptr"
	}
	return " -> i32"
}

// saSigKinds 按形参序摊平种表（调用核按位定向求值用；捕获实参由
// arrowCaps 另行追加，不占形参种位）。
func saSigKinds(paramKinds map[string]string, params []string) []string {
	out := make([]string, 0, len(params))
	for _, p := range params {
		out = append(out, paramKinds[p])
	}
	return out
}

// saSigParamType 把形参种映成签名注解（i32/bool 即 i32，其余 ptr 句柄；
// 形状证据：封存上游实发 `@__arrow_1(x: i32, base: i32)`、`@g(p: ptr, s: ptr)`、
// `@h(f: i32)`——真机 `sa check` 拒无注解签名 `InvalidFunctionSig`）。
func saSigParamType(kind string) string {
	if kind == "i32" || kind == "bool" {
		return "i32"
	}
	if kind == "f64" {
		return "f64"
	}
	return "ptr"
}

// saSigParamList 按形参序拼 `名: 种` 签名段（与 saSigKinds 同序）。
func saSigParamList(paramKinds map[string]string, params []string) string {
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, p+": "+saSigParamType(paramKinds[p]))
	}
	return strings.Join(parts, ", ")
}

// ── 返回类型 checker 推断（satsgo typecheck.go 同款，单文件特化） ──
// typeCtx 在 transpileWorker 同口径 program 上绑定 + 取 checker（NoCheck 仅关
// 诊断，不关 checker 构造；IsolatedModules 单文件等价独立绑定；形状证据：
// 封存 newTypeCtx + inferredReturnType + scalarReturnKind + prescanRet）。
type saTypeCtx struct {
	check *checker.Checker
	done  func()
}

func (t *saTypeCtx) close() {
	if t != nil && t.done != nil {
		t.done()
	}
}

// saScalarReturnKind 映射单个 checker 类型到 SA 返回种（注解表镜像：
// number→"number"，string→"string"，boolean→"boolean"，void/undefined→"void"；
// any/unknown/异形一律 false，调用方沿既有 loud 拒）。
func saScalarReturnKind(ty *checker.Type) (string, bool) {
	if ty == nil {
		return "", false
	}
	f := ty.Flags()
	switch {
	case f&checker.TypeFlagsAnyOrUnknown != 0:
		return "", false
	case f&checker.TypeFlagsStringLike != 0:
		return "string", true
	case f&checker.TypeFlagsNumberLike != 0:
		return "number", true
	case f&checker.TypeFlagsBooleanLike != 0:
		return "boolean", true
	case f&checker.TypeFlagsVoid != 0 || f&checker.TypeFlagsUndefined != 0:
		return "void", true
	}
	return "", false
}

// saInferredReturnKind 取函数节点的 checker 签名返回种（联合须全体一致，
// checker 拼 `boolean` 为 `true|false` 与注解 `boolean` 同形；分歧/any/异形
// false；无 ctx 或 checker 异常一律 false，调用方回退既有 void 门）。
func saInferredReturnKind(fnNode *ast.Node, tcx *saTypeCtx) (string, bool) {
	if tcx == nil || tcx.check == nil || fnNode == nil {
		return "", false
	}
	var out string
	ok := false
	func() {
		defer func() { _ = recover() }()
		ty := tcx.check.GetTypeAtLocation(fnNode)
		if ty == nil {
			return
		}
		sigs := tcx.check.GetSignaturesOfType(ty, checker.SignatureKindCall)
		if len(sigs) == 0 {
			return
		}
		rt := tcx.check.GetReturnTypeOfSignature(sigs[0])
		if rt == nil {
			return
		}
		var flats []*checker.Type
		if rt.Flags()&checker.TypeFlagsUnionOrIntersection != 0 {
			flats = rt.Types()
		} else {
			flats = []*checker.Type{rt}
		}
		if len(flats) == 0 {
			return
		}
		got := ""
		have := false
		for _, m := range flats {
			s, good := saScalarReturnKind(m)
			if !good {
				return
			}
			if !have {
				got, have = s, true
			} else if got != s {
				return
			}
		}
		out, ok = got, have
	}()
	if !ok {
		return "", false
	}
	return out, true
}

// saPrescanRet 定单个函数/箭头的 SA 返回签名（显式注解 > checker 推断 > void；
// 三处签名表——函数预扫/箭头预扫/定义发射——必须同源，否则调用点与定义错位丢值）。
func saPrescanRet(typeNode *ast.TypeNode, fnNode *ast.Node, tcx *saTypeCtx, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (retKind string, isVoid, ok bool) {
	if typeNode != nil {
		if k, good := saReturnKindRef(typeNode, classes, aliasOf); good {
			return k, k == "void", true
		}
		return "", false, false
	}
	if k, good := saInferredReturnKind(fnNode, tcx); good {
		return k, k == "void", true
	}
	return "void", true, true
}

// ── 顶层可变模块状态（step106；i32 标量 `let`/`var` 切片；str/obj/i64/f64/复合赋值仍沿旧门）──
// 形状证据总纲：封存 modstate.go:1-28（无全局/AOT 只读→注册表槽位/惰性一次/键域隔离/+
// 被赋值名永不折叠 26-28）+ assignedNames:150-214 + modClaim:560-591 +
// preRegisterModStates:593-616 + tryModState:618-635 + registerModState:343-385 +
// emitModSetRaw:668-687 + emitModEnsure:689-724 + emitModLoad:901-938（i32 分支 933-937）+
// modWiden:822-882（同宽 trunc 839-851）。
// 单文件无前缀：键域前缀为空（多文件前缀见 modKeyOf:107-110，单文件投影）。
type saModState struct {
	qual string // 饰名（拒因定位；封存 modState.qual:39-55）
	w    string // "i32" | "str"（i64/u64/f64/obj 后步；封存 modWidthOf:70-82 子集）
	key  uint64
	key2 uint64 // len 槽（串独有；封存 modStrKeyOf:112-117）
	flag uint64 // 0 即零快道（注册表零填，无分支；封存 modIsZero:321-341；串恒置位）
	init string // 非零字面文本（flag != 0 时有效；零初值/无初值 flag 恒 0；串恒有初值）
}

// saModFnv1a64 即 FNV-1a 64（封存 fnv1a64:84-97）。
func saModFnv1a64(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// saModKeyOf 派生值/标志槽键（同域字符串；63 位掩码；封存 modKeyOf:105-110 +
// modKeyMask:99-103；单文件前缀为空）。
func saModKeyOf(qual string) (uint64, uint64) {
	base := "satsgo modstate v1\x00\x00" + qual
	const mask = uint64(0x7FFFFFFFFFFFFFFF)
	return saModFnv1a64("val\x00"+base) & mask, saModFnv1a64("flag\x00"+base) & mask
}

// saModStrKeyOf 派生串 ptr/len/flag 三槽键（各异域；封存 modStrKeyOf:112-117）。
func saModStrKeyOf(qual string) (ptr, ln, flag uint64) {
	base := "satsgo modstate v1\x00\x00" + qual
	const mask = uint64(0x7FFFFFFFFFFFFFFF)
	return saModFnv1a64("strptr\x00"+base) & mask, saModFnv1a64("strlen\x00"+base) & mask, saModFnv1a64("strflag\x00"+base) & mask
}

// saIsModAssignOp 报告赋值类操作符（`=`/复合/逻辑赋值；`==` 系比较除外）。
func saIsModAssignOp(op ast.Kind) bool {
	switch op {
	case ast.KindEqualsToken,
		ast.KindPlusEqualsToken, ast.KindMinusEqualsToken,
		ast.KindAsteriskEqualsToken, ast.KindSlashEqualsToken, ast.KindPercentEqualsToken,
		ast.KindLessThanLessThanEqualsToken, ast.KindGreaterThanGreaterThanEqualsToken,
		ast.KindGreaterThanGreaterThanGreaterThanEqualsToken,
		ast.KindAmpersandEqualsToken, ast.KindBarEqualsToken, ast.KindCaretEqualsToken:
		return true
	}
	return saIsLogicAssignOp(op)
}

// saAssignedNames 全文件收集赋值目标裸名（`=`/复合/`++`/`--`；跨作用域过近似仅多建槽，
// 局部遮蔽仍优先，sound；封存 assignedNames:150-157）。
func saAssignedNames(stmts []*ast.Node) map[string]bool {
	out := map[string]bool{}
	mark := func(n *ast.Node) {
		if n != nil && n.Kind == ast.KindIdentifier {
			out[n.Text()] = true
		}
	}
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case ast.KindBinaryExpression:
			if be := n.AsBinaryExpression(); be != nil && be.OperatorToken != nil && saIsModAssignOp(be.OperatorToken.Kind) {
				mark(be.Left)
			}
		case ast.KindPrefixUnaryExpression:
			if un := n.AsPrefixUnaryExpression(); un != nil && (un.Operator == ast.KindPlusPlusToken || un.Operator == ast.KindMinusMinusToken) {
				mark(un.Operand)
			}
		case ast.KindPostfixUnaryExpression:
			if un := n.AsPostfixUnaryExpression(); un != nil && (un.Operator == ast.KindPlusPlusToken || un.Operator == ast.KindMinusMinusToken) {
				mark(un.Operand)
			}
		}
		n.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	for _, st := range stmts {
		walk(st)
	}
	return out
}

// saModInitI32 分类 i32 槽初值（nil/缺省零快道；整字面/布尔/`-`整；其余交旧路；
// 封存 modInitOf:281-319 子集 + modIsZero:321-341 子集）。
func saModInitI32(init *ast.Node) (imm string, zero, ok bool) {
	if init == nil {
		return "", true, true
	}
	switch init.Kind {
	case ast.KindNumericLiteral:
		t := init.Text()
		if saIsFloatLit(t) {
			return "", false, false
		}
		i := t
		if len(i) > 0 && i[0] == '-' {
			i = i[1:]
		}
		return t, i == "0", true
	case ast.KindTrueKeyword:
		return "1", false, true
	case ast.KindFalseKeyword:
		return "0", true, true
	case ast.KindPrefixUnaryExpression:
		un := init.AsPrefixUnaryExpression()
		if un != nil && un.Operator == ast.KindMinusToken && un.Operand != nil && un.Operand.Kind == ast.KindNumericLiteral && !saIsFloatLit(un.Operand.Text()) {
			return "-" + un.Operand.Text(), un.Operand.Text() == "0", true
		}
		return "", false, false
	default:
		return "", false, false
	}
}

// saModSlotInit 分类槽初值（i32：沿 saModInitI32；串：字面/纯模板，文本直喂落字；
// 注解须同宽（bool 熨平 i32）；串无初值交旧门；封存 modInitOf:281-319 子集）。
func saModSlotInit(vd *ast.VariableDeclaration) (w, lit string, zero, ok bool) {
	if vd.Initializer != nil {
		switch vd.Initializer.Kind {
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
			w, lit = "str", vd.Initializer.Text()
		default:
			imm, z, ok2 := saModInitI32(vd.Initializer)
			if !ok2 {
				return "", "", false, false
			}
			w, lit, zero = "i32", imm, z
		}
	} else {
		w, zero = "i32", true
	}
	if vd.Type != nil {
		k, ok2 := saAnnotKind(vd.Type)
		if !ok2 {
			return "", "", false, false
		}
		switch {
		case w == "i32" && (k == "i32" || k == "bool"):
		case w == "str" && k == "str":
		default:
			return "", "", false, false
		}
	}
	if w == "str" && vd.Initializer == nil {
		return "", "", false, false
	}
	return w, lit, zero, true
}

// saModClaimName 判定单 declarator 是否归槽（具名 + 文件内被赋值 + `let`/`var` +
// i32/串初值；`const`/箭头/异形交旧路；封存 modClaim:560-591 子集）。
func saModClaimName(d *ast.Node, vd *ast.VariableDeclaration, assigned map[string]bool) (string, string, bool) {
	if vd == nil {
		return "", "", false
	}
	nm := vd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", "", false
	}
	name := nm.Text()
	if !assigned[name] {
		return "", "", false
	}
	if vd.Initializer != nil && vd.Initializer.Kind == ast.KindArrowFunction {
		return "", "", false
	}
	w, _, _, ok := saModSlotInit(vd)
	if !ok {
		return "", "", false
	}
	_ = d
	return name, w, true
}

// saRecordModStates 预注册顶层 i32 槽（`const` 永不入槽；重名/碰撞大声拒；
// 声明无码；封存 preRegisterModStates:593-616 + registerModState:343-385 子集）。
func saRecordModStates(stmts []*ast.Node, assigned map[string]bool, funcs map[string]saFuncSig, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) map[string]*saModState {
	out := map[string]*saModState{}
	for _, st := range stmts {
		if st.Kind != ast.KindVariableStatement {
			continue
		}
		vs := st.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		vdl := vs.DeclarationList.AsVariableDeclarationList()
		if vdl == nil {
			continue
		}
		if vs.DeclarationList.AsNode().Flags&ast.NodeFlagsConst != 0 {
			continue
		}
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			name, w, ok := saModClaimName(d, vd, assigned)
			if !ok {
				continue
			}
			if _, dup := out[name]; dup {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "module variable " + name + " is already declared (redefinition is not lowerable)"})
				continue
			}
			if _, dup := funcs[name]; dup {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "module variable " + name + " collides with an existing definition"})
				continue
			}
			if _, dup := classes[name]; dup {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "module variable " + name + " collides with an existing definition"})
				continue
			}
			_, lit, zero, _ := saModSlotInit(vd)
			if w == "str" {
				// 串双槽（ptr+len 独立键域；标志恒置位，空串亦物化；封存
				// registerModState:360-367 + modIsZero 串分支）。
				ptr, ln, flag := saModStrKeyOf(name)
				out[name] = &saModState{qual: name, w: "str", key: ptr, key2: ln, flag: flag, init: lit}
				continue
			}
			key, flag := saModKeyOf(name)
			ms := &saModState{qual: name, w: "i32", key: key}
			if !zero {
				ms.flag = flag
				ms.init = lit
			}
			out[name] = ms
		}
	}
	return out
}

// saTryModState 消费槽绑定声明（无码；任一 declarator 被认领即整句消费；
// 封存 tryModState:618-635 子集）。
func saTryModState(st *ast.Node, assigned map[string]bool) bool {
	if st.Kind != ast.KindVariableStatement {
		return false
	}
	vs := st.AsVariableStatement()
	if vs == nil || vs.DeclarationList == nil || vs.DeclarationList.AsVariableDeclarationList() == nil {
		return false
	}
	if vs.DeclarationList.AsNode().Flags&ast.NodeFlagsConst != 0 {
		return false
	}
	for _, d := range vs.DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
		if _, _, ok := saModClaimName(d, d.AsVariableDeclaration(), assigned); ok {
			return true
		}
	}
	return false
}

// saEmitModSetRaw 原始 u64 槽存 + house 状态检查（非零 panic；封存 emitModSetRaw:668-687）。
func saEmitModSetRaw(w printer.EmitTextWriter, key uint64, val string, scope *saScope, nextTemp *int) {
	st := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_modstate_set_u64(%d, %s)\n", st, key, val))
	bad := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	badL := fmt.Sprintf("L_ms_bad_%d", *scope.nextLabel)
	*scope.nextLabel++
	okL := fmt.Sprintf("L_ms_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", bad, st))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", bad, badL, okL))
	w.Write(fmt.Sprintf("%s:\n", badL))
	w.Write(fmt.Sprintf("  panic(%d)\n", 1403))
	w.Write(fmt.Sprintf("%s:\n", okL))
	scope.addImport("sa_std/modstate.sai")
}

// saEmitModEnsure 惰性一次守卫（零快道无字；封存 emitModEnsure:689-724 子集）。
func saEmitModEnsure(w printer.EmitTextWriter, ms *saModState, scope *saScope, nextTemp *int) {
	if ms.flag == 0 {
		return
	}
	f := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_modstate_get_u64(%d)\n", f, ms.flag))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	doneL := fmt.Sprintf("L_ms_done_%d", *scope.nextLabel)
	*scope.nextLabel++
	initL := fmt.Sprintf("L_ms_init_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", c, f))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, doneL, initL))
	w.Write(fmt.Sprintf("%s:\n", initL))
	iv := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = trunc %s as u64\n", iv, ms.init))
	saEmitModSetRaw(w, ms.key, iv, scope, nextTemp)
	saEmitModSetRaw(w, ms.flag, "1", scope, nextTemp)
	w.Write(fmt.Sprintf("  jmp %s\n", doneL))
	w.Write(fmt.Sprintf("%s:\n", doneL))
	scope.addImport("sa_std/modstate.sai")
}

// saModLoadI32 槽读（守卫 + 取 u64 + 窄化；i32 分支封存 emitModLoad:933-937）。
func saModLoadI32(w printer.EmitTextWriter, ms *saModState, scope *saScope, nextTemp *int) string {
	saEmitModEnsure(w, ms, scope, nextTemp)
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_modstate_get_u64(%d)\n", t, ms.key))
	n := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = trunc %s as i32\n", n, t))
	scope.addImport("sa_std/modstate.sai")
	return n
}

// saModStoreI32 槽写（先加宽、后守卫、再落存；加宽暂存即赋值值，封存
// emitModStore:1157-1189 + modWiden:839-851 同宽 trunc 子集）。
func saModStoreI32(w printer.EmitTextWriter, ms *saModState, val string, scope *saScope, nextTemp *int) string {
	u := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = trunc %s as u64\n", u, val))
	saEmitModEnsure(w, ms, scope, nextTemp)
	saEmitModSetRaw(w, ms.key, u, scope, nextTemp)
	return u
}

// saModStrText 取字面串存储文本（字面/纯模板/已折叠串常量；计算串交旧门大声拒；
// 封存 modStringText:765-787）。
func saModStrText(rhs *ast.Node, scope *saScope) (string, bool) {
	if rhs == nil {
		return "", false
	}
	switch rhs.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return rhs.Text(), true
	case ast.KindIdentifier:
		if text, ok := scope.topConsts[rhs.Text()]; ok && scope.topStr[rhs.Text()] {
			return text, true
		}
		return "", false
	default:
		return "", false
	}
}

// saModMaterialize 具化字面串为 16 字节头并回读 (ptr, len)（init 与存共用；
// 封存 emitModInitString:730-742）。
func saModMaterialize(w printer.EmitTextWriter, text string, scope *saScope, nextTemp *int) (pv, ln string) {
	h := saLowerStringLiteral(w, text, scope, nextTemp)
	pv = fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", pv, h))
	ln = fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, h))
	return pv, ln
}

// saEmitModEnsureStr 串惰性一次守卫（标志恒置位，空串亦物化；封存
// emitModEnsure:689-724 + emitModInitString:730-742）。
func saEmitModEnsureStr(w printer.EmitTextWriter, ms *saModState, scope *saScope, nextTemp *int) {
	f := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_modstate_get_u64(%d)\n", f, ms.flag))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	doneL := fmt.Sprintf("L_ms_done_%d", *scope.nextLabel)
	*scope.nextLabel++
	initL := fmt.Sprintf("L_ms_init_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", c, f))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, doneL, initL))
	w.Write(fmt.Sprintf("%s:\n", initL))
	pv, ln := saModMaterialize(w, ms.init, scope, nextTemp)
	pu := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = trunc %s as u64\n", pu, pv))
	saEmitModSetRaw(w, ms.key, pu, scope, nextTemp)
	saEmitModSetRaw(w, ms.key2, ln, scope, nextTemp)
	saEmitModSetRaw(w, ms.flag, "1", scope, nextTemp)
	w.Write(fmt.Sprintf("  jmp %s\n", doneL))
	w.Write(fmt.Sprintf("%s:\n", doneL))
	scope.addImport("sa_std/modstate.sai")
}

// saModLoadStr 串槽读（守卫 + 双槽取 + 16 字节头；封存 emitModLoadString:941-962）。
func saModLoadStr(w printer.EmitTextWriter, ms *saModState, scope *saScope, nextTemp *int) string {
	saEmitModEnsureStr(w, ms, scope, nextTemp)
	tp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_modstate_get_u64(%d)\n", tp, ms.key))
	tl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_modstate_get_u64(%d)\n", tl, ms.key2))
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", h, tp))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", h, tl))
	scope.addImport("sa_std/modstate.sai")
	return h
}

// saModStoreStr 串槽存（字面直存；调用方已取文本；返回具化头即赋值值；
// 封存 emitModStoreString:789-806）。
func saModStoreStr(w printer.EmitTextWriter, ms *saModState, text string, scope *saScope, nextTemp *int) string {
	saEmitModEnsureStr(w, ms, scope, nextTemp)
	h := saLowerStringLiteral(w, text, scope, nextTemp)
	pv := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", pv, h))
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, h))
	pu := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = trunc %s as u64\n", pu, pv))
	saEmitModSetRaw(w, ms.key, pu, scope, nextTemp)
	saEmitModSetRaw(w, ms.key2, ln, scope, nextTemp)
	return h
}
