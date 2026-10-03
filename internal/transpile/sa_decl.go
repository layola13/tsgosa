// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
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
		vd.Initializer.Kind == ast.KindTemplateExpression || vd.Initializer.Kind == ast.KindTaggedTemplateExpression {
		// 无注解串推断（字面量/模板/tagged 皆串位；tag 门在求值内）。
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
	if vd.Initializer.Kind == ast.KindConditionalExpression {
		// 无注解三元推断（i32/串臂与 return 位同核；分歧沿核拒）。
		ce := vd.Initializer.AsConditionalExpression()
		t, isStr, msg := saLowerTernaryValue(w, ce, d, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, t))
		if isStr {
			scope.types[name] = "str"
		} else {
			scope.types[name] = "i32"
		}
		return true
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
func saLowerArrowConst(w printer.EmitTextWriter, name string, arrow *ast.Node, funcs map[string]saFuncSig, enums map[string]map[string]int64, enumNonInt map[string]map[string]bool, classes map[string]*saClassDef, topConsts map[string]string, topStr map[string]bool, topMaths map[string]string, src string, mainRenamed bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool, tcx *saTypeCtx) {
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
	retKind, isVoid := "void", true
	if rt := saArrowReturnNode(arrow); rt != nil {
		k, ok := saReturnKind(rt)
		if !ok {
			ln, col := pos(arrow.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported return annotation"})
			return
		}
		retKind, isVoid = k, k == "void"
	} else if k, v, ok := saPrescanRet(nil, arrow, tcx); ok {
		retKind, isVoid = k, v
	}
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
	scope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, enumNonInt: enumNonInt, classes: classes, topConsts: topConsts, topStr: topStr, mainRenamed: mainRenamed, nextLabel: nextLabel, retKind: retKind, strPool: strPool, src: src, addImport: needImport}
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
func saPrescanRet(typeNode *ast.TypeNode, fnNode *ast.Node, tcx *saTypeCtx) (retKind string, isVoid, ok bool) {
	if typeNode != nil {
		if k, good := saReturnKind(typeNode); good {
			return k, k == "void", true
		}
		return "", false, false
	}
	if k, good := saInferredReturnKind(fnNode, tcx); good {
		return k, k == "void", true
	}
	return "void", true, true
}
