// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"strings"

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

// sa_decl.go 后半 — 顶层 const 函数值（`const f = (...)=>...` / `= function...`）。
// 归声明域（transpile.go 禁巨无霸：仅留 saLowerSourceFile 编排 + 函数签名核）。
// 形状证据：封存 tryTopLevelArrow:1015-1032 + lowerArrowBinding:1058-1098。

// saSynthArrowParams 合成箭头/函数表达式形参表（与 saSynthParams 同核；
// 形状证据：封存 lowerArrowBinding:1074-1098，模式走同 hiddenDestructuredParam）。
func saSynthArrowParams(arrow *ast.Node, classes map[string]*saClassDef) ([]string, map[string]string, []saDestructurePending, bool) {
	var nodes []*ast.Node
	if pl := arrow.ParameterList(); pl != nil {
		nodes = pl.Nodes
	}
	return saSynthParamNodes(nodes, classes)
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
		if pd == nil || pd.DotDotDotToken != nil || pd.Initializer != nil || pd.QuestionToken != nil {
			return nil, false
		}
		nm := pd.Name()
		if nm == nil {
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
	type fold struct {
		name  string
		text  string
		str   bool
		math  string
		alias bool
	}
	var folds []fold
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
			folds = append(folds, fold{name: nm.Text(), text: init.Text()})
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
			folds = append(folds, fold{name: nm.Text(), text: init.Text(), str: true})
		case ast.KindTrueKeyword:
			folds = append(folds, fold{name: nm.Text(), text: "1"})
		case ast.KindFalseKeyword:
			folds = append(folds, fold{name: nm.Text(), text: "0"})
		case ast.KindPropertyAccessExpression:
			m, ok := saMathMethodName(init)
			if !ok {
				return false
			}
			folds = append(folds, fold{name: nm.Text(), math: m, alias: true})
		case ast.KindIdentifier:
			m, ok := maths[init.Text()]
			if !ok {
				return false
			}
			folds = append(folds, fold{name: nm.Text(), math: m, alias: true})
		default:
			return false
		}
	}
	for _, f := range folds {
		if f.alias {
			maths[f.name] = f.math
			continue
		}
		consts[f.name] = f.text
		if f.str {
			strs[f.name] = true
		}
	}
	return true
}

// saLowerArrowConst lowering 顶层 `const f = (...)=>...`/`= function...`
// （out-of-line 被调，与函数声明同形；形状证据：封存 tryTopLevelArrow:1015-1032
// + lowerArrowBinding:1058-1098）。仅顶层无捕获口径：体引用未知名走既有求值
// 大声拒；生成器/async 形大声拒；表达式体单值返回，无注解值体仍按函数同例拒。
func saLowerArrowConst(w printer.EmitTextWriter, name string, arrow *ast.Node, funcs map[string]saFuncSig, enums map[string]map[string]int64, classes map[string]*saClassDef, topConsts map[string]string, topStr map[string]bool, topMaths map[string]string, mainRenamed bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool) {
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
	retKind, ok := saReturnKind(saArrowReturnNode(arrow))
	if !ok {
		ln, col := pos(arrow.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported return annotation"})
		return
	}
	isVoid := retKind == "void"
	emitName := name
	if emitName == "main" && mainRenamed {
		emitName = "main__user"
	}
	sig := "@" + emitName + "(" + strings.Join(params, ", ") + ")"
	if !isVoid {
		if retKind == "string" {
			sig += " -> ptr"
		} else {
			sig += " -> i32"
		}
	}
	sig += ":\n"
	w.Write(sig)
	body := arrow.Body()
	if body == nil {
		ln, col := pos(arrow.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function value " + name + " has no body"})
		return
	}
	scope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, classes: classes, topConsts: topConsts, topStr: topStr, mainRenamed: mainRenamed, nextLabel: nextLabel, retKind: retKind, strPool: strPool, addImport: needImport}
	saSeedTopMaths(scope, topMaths)
	if _, kinds, _, ok := saSynthArrowParams(arrow, scope.classes); ok {
		for k, v := range kinds {
			scope.types[k] = v
		}
	} else {
		ln, col := pos(arrow.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported parameter annotation (i32/bool/arr/str/inst only)"})
		return
	}
	if _, _, pendings, ok := saSynthArrowParams(arrow, scope.classes); ok && len(pendings) > 0 {
		if !saDrainDestructuredParams(w, pendings, scope, pos, refusals, nextLabel, nextTemp) {
			return
		}
	}
	if body.Kind == ast.KindBlock {
		stmts, ok := saBlockStmts(body)
		if !ok {
			ln, col := pos(arrow.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported body"})
			return
		}
		if len(stmts) == 0 {
			if !isVoid {
				ln, col := pos(arrow.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "missing return"})
				return
			}
			w.Write("  ret\n")
			return
		}
		terminated := false
		for _, s := range stmts {
			if terminated {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unreachable code after terminating statement"})
				return
			}
			if done, failed := saLowerStmt(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp); failed {
				return
			} else if done {
				terminated = true
			}
		}
		if !terminated {
			if !isVoid {
				ln, col := pos(arrow.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "missing return"})
				return
			}
			w.Write("  ret\n")
		}
		return
	}
	if isVoid {
		ln, col := pos(body.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported void arrow expression body"})
		return
	}
	op, msg := saEvalReturnOperand(w, body, retKind, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(body.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return
	}
	w.Write(fmt.Sprintf("  ret %s\n", op))
}
