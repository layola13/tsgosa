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

// saQualifiedTypeName 解析类型引用名文本（标识符直返；单层 `N.C` 限定名
// 展平为 `N_C` 布局键；其他形一律 false）。调用方禁止对 TypeName 直接
// .Text()（QualifiedName 会 panic，0 崩溃铁律；封存 step152 同族）。
func saQualifiedTypeName(tn *ast.TypeNode) (string, bool) {
	if tn == nil || tn.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := tn.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil {
		return "", false
	}
	if ref.TypeName.Kind == ast.KindIdentifier {
		return ref.TypeName.Text(), true
	}
	if ref.TypeName.Kind != ast.KindQualifiedName {
		return "", false
	}
	qn := ref.TypeName.AsQualifiedName()
	if qn == nil || qn.Left == nil || qn.Right == nil {
		return "", false
	}
	if qn.Left.Kind != ast.KindIdentifier || qn.Right.Kind != ast.KindIdentifier {
		return "", false
	}
	return qn.Left.Text() + "_" + qn.Right.Text(), true
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
		// crypto Hash 暂存逐声明清零（非声明式 createHash 不得污染后继声明；
		// 形状证据：封存 lowerVarDeclList:1404）。
		scope.lastHash = nil
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
		litInit := vd.Initializer
		adoptWant := ""
		if vd.Type == nil && litInit != nil && (litInit.Kind == ast.KindSatisfiesExpression || litInit.Kind == ast.KindAsExpression) {
			// satisfies/as 裹装字面量视为带注解声明（封存 lowerExpr:2872-2876 擦除同形；只接已记录接口名，as const/泛型沿旧路保持拒收位置与文案）。
			var inner *ast.Node
			var tnode *ast.TypeNode
			if litInit.Kind == ast.KindSatisfiesExpression {
				se := litInit.AsSatisfiesExpression()
				inner, tnode = se.Expression, se.Type
			} else {
				ae := litInit.AsAsExpression()
				inner, tnode = ae.Expression, ae.Type
			}
			if inner != nil && inner.Kind == ast.KindObjectLiteralExpression && tnode != nil && tnode.Kind == ast.KindTypeReference {
				if ref := tnode.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && ref.TypeArguments == nil {
					if def, ok := scope.classes[ref.TypeName.Text()]; ok && def.isIface {
						litInit = inner
						adoptWant = ref.TypeName.Text()
					}
				}
			}
		}
		if litInit != nil && litInit.Kind == ast.KindObjectLiteralExpression {
			// 对象字面量声明（注解须为同名接口；无注解按键集匹配）。
			// `Record<string,T>` 注解走 Map 具化（Z1；余形沿旧门）。
			if vd.Type != nil {
				if vkind, ok := saRecordValueKind(vd.Type, scope.classes); ok {
					if !saLowerRecordLiteral(w, name, vkind, vd.Initializer, scope, pos, refusals, nextTemp) {
						return false
					}
					continue
				}
			}
			want := adoptWant
			if vd.Type != nil {
				tn := vd.Type
				if tn.Kind != ast.KindTypeReference {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object annotation must name an interface"})
					return false
				}
				ref := tn.AsTypeReferenceNode()
				// 限定名先守（`x: NS.Iface` TypeName.Text 会 panic，0 崩溃铁律；见 step376）。
				if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object annotation must name an interface"})
					return false
				}
				want = ref.TypeName.Text()
				// 泛型具化优先（`const b: Box<i32> = {...}` 按单态布局具化；
				// 不可具化下探既有擦除；封存 layoutOfAnnotation 同形）。
				if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
					if lname, ok := saInstantiateIface(ref.TypeName.Text(), ref.TypeArguments.Nodes, scope.classes); ok {
						want = lname
					}
				}
				// 同构映射别名（`Partial<B>`/用户同构；布局恒等；见上）。
				if _, ok := scope.classes[want]; !ok {
					if lname, ok := saMappedAliasLayout(ref, scope.classes, scope.aliasOf); ok {
						want = lname
					}
				}
				if def, ok := scope.classes[want]; !ok || !def.isIface {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object annotation must name an interface"})
					return false
				}
			}
			h, defname, msg := saLowerObjectLiteral(w, litInit, want, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(litInit.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "inst:" + defname
			saConsumeOwn(scope, h)
			saDeclareOwned(scope, name)
			continue
		}
		if vd.Initializer != nil && vd.Initializer.Kind == ast.KindRegularExpressionLiteral {
			// `const re = /pat/flags` 绑 regex 柄（注解缺省或 RegExp，见 sa_date.go）。
			if vd.Type != nil {
				tn := vd.Type
				if tn.Kind != ast.KindTypeReference {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "regex annotation must be RegExp"})
					return false
				}
				ref := tn.AsTypeReferenceNode()
				if ref == nil || ref.TypeName == nil ||
					ref.TypeName.Text() != "RegExp" {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "regex annotation must be RegExp"})
					return false
				}
			}
			pat, flags, ok := saRegexSplitLiteral(vd.Initializer.Text())
			if !ok {
				ln, col := pos(vd.Initializer.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "bad regular expression literal"})
				return false
			}
			h, msg := saLowerRegexCompile(w, pat, flags, scope, nextTemp)
			if msg != "" {
				ln, col := pos(vd.Initializer.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			scope.types[name] = "regex"
			saConsumeOwn(scope, h)
			saDeclareOwned(scope, name)
			continue
		}
		if vd.Initializer != nil && vd.Initializer.Kind == ast.KindNewExpression {
			// `new Array(n)` 定长零数组；零参/多参即元素式（与 `Array.of` 同，
			// 具化核自判；`new Array(a, b)` 不再落通用拒绝）。
			if saIsArrayCtor(vd.Initializer) {
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
			// `new Map()`/`new Set()` 绑定为 map/set 种（参数忽略容忍，见 saLowerMapNew）。
			if ne := vd.Initializer.AsNewExpression(); ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier &&
				(ne.Expression.Text() == "Map" || ne.Expression.Text() == "Set") {
				if vd.Type != nil {
					tn := vd.Type
					if tn.Kind != ast.KindTypeReference {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "collection annotation must be Map or Set"})
						return false
					}
					ref := tn.AsTypeReferenceNode()
					if ref == nil || ref.TypeName == nil ||
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
				// `Map<K, Array<V>>` 值种记表（数组值存取；串元传播另步；
				// 类型参数在 `new` 上（`new Map<K,V>()`）或注解上）。
				if kind == "map" {
					var targs []*ast.Node
					if ne.TypeArguments != nil {
						targs = ne.TypeArguments.Nodes
					} else if vd.Type != nil && vd.Type.Kind == ast.KindTypeReference {
						if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil &&
							ref.TypeName.Text() == "Map" && ref.TypeArguments != nil {
							targs = ref.TypeArguments.Nodes
						}
					}
					if len(targs) == 2 {
						if vt := targs[1]; vt != nil && vt.Kind == ast.KindArrayType {
							saSetMapVal(scope, name, saMapArrValKind(vt))
						}
					}
				}
				saConsumeOwn(scope, h)
				saDeclareOwned(scope, name)
				continue
			}
			// `new Date()` 绑定为 date 种（millis 不透明；有参形大声拒）。
			if ne := vd.Initializer.AsNewExpression(); ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "Date" {
				if !saIsDateNew(vd.Initializer) {
					ln, col := pos(vd.Initializer.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "new Date(x) is not lowerable (only arg-less now-shape)"})
					return false
				}
				if vd.Type != nil {
					tn := vd.Type
					if tn.Kind != ast.KindTypeReference {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "date annotation must be Date"})
						return false
					}
					ref := tn.AsTypeReferenceNode()
					if ref == nil || ref.TypeName == nil ||
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
			// `new RegExp("pat", "flags?")` 绑 regex 柄（注解缺省或 RegExp）。
			if ne := vd.Initializer.AsNewExpression(); ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier && ne.Expression.Text() == "RegExp" {
				if vd.Type != nil {
					tn := vd.Type
					if tn.Kind != ast.KindTypeReference {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "regex annotation must be RegExp"})
						return false
					}
					ref := tn.AsTypeReferenceNode()
					if ref == nil || ref.TypeName == nil ||
						ref.TypeName.Text() != "RegExp" {
						ln, col := pos(d.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "regex annotation must be RegExp"})
						return false
					}
				}
				h, msg := saLowerRegexNew(w, ne, scope, pos, refusals, nextTemp)
				if msg != "" {
					ln, col := pos(vd.Initializer.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
				w.Write(fmt.Sprintf("  %s = %s\n", name, h))
				scope.types[name] = "regex"
				saConsumeOwn(scope, h)
				saDeclareOwned(scope, name)
				continue
			}
			// 实例声明（`const o: C = new C(...)` 注解须同名；`let o = new C()` 推断；
			// `new N.C()` 经 `N_C` 限定布局，单文件命名空间成员类）。
			ne := vd.Initializer.AsNewExpression()
			cname := ""
			if ne.Expression != nil && ne.Expression.Kind == ast.KindIdentifier {
				cname = ne.Expression.Text()
			} else if ne.Expression != nil && ne.Expression.Kind == ast.KindPropertyAccessExpression {
				pa := ne.Expression.AsPropertyAccessExpression()
				if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil && pa.Name().Kind == ast.KindIdentifier {
					cname = pa.Expression.Text() + "_" + pa.Name().Text()
				}
			}
			if _, ok := scope.classes[cname]; !ok {
				ln, col := pos(vd.Initializer.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + cname})
				return false
			}
			if vd.Type != nil {
				tn := vd.Type
				if tn.Kind != ast.KindTypeReference {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "instance annotation must name its class"})
					return false
				}
				// 限定注解 `N.C` 须可解析（与 `new N.C()` 的 `N_C` 布局键同形；非标识/非单层限定沿旧门；直接 .Text() 会 panic，禁碰）。
				// 注解名与初值类名允许不一致（基类注解接派生实例多态：封存 lowerVarDeclList 取初值种绑定，注解显式丢弃，无一致性检查；绑定恒跟初值 inst:cname，用点经布局表大声拒）。
				if _, ok := saQualifiedTypeName(tn); !ok {
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
		if vd.Type == nil || saIsEraseAnnotation(vd.Type) || saAliasErasesToTemplate(vd.Type, scope.aliasOf) {
			// 无注解推断（形状证据：封存 lowerVarDeclList:1415-1418/1463-1464
			// 未知注解缺省 i32 + 按初值类型绑定）：数组字面量/数组句柄走 arr 通道，
			// true/false 走 bool，其余 i32 求值；缺 init 绑 i32 零值（const 缺 init 拒）。
			if !saLowerInferredDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		if vd.Type.Kind == ast.KindTypeReference {
			// 限定名先守（`x: A.B` TypeName.Text 会 panic，0 崩溃铁律；
			// 见 step376，同族另见 sa_arr.go saIsStringArrayAnnot）。
			if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && ref.TypeName.Text() == "Date" {
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
			// 泛型具化优先（`const b: Box<i32> = mk()` 按单态记种；
			// 不可具化下探既有链；封存 layoutOfAnnotation 同形）。
			if vd.Type != nil && vd.Type.Kind == ast.KindTypeReference {
				if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
					if lname, iok := saInstantiateIface(ref.TypeName.Text(), ref.TypeArguments.Nodes, scope.classes); iok {
						vkind, ok = "inst:"+lname, true
					}
				}
			}
		}
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
		// 非折叠联合、typeof 声明、typeof 别名及泛型别名按初值种绑定
		//（cf any 擦除；可折叠已由 saAnnotKind 办；别名链终点 typeof 经上
		// helper 判定；泛型别名基（如条件类型实例化）上游初值种绑定
		// 同形，错配初值亦然，句柄门显式跳过别名基故落此处）。
		if vd.Type != nil && (vd.Type.Kind == ast.KindUnionType || vd.Type.Kind == ast.KindTypeQuery ||
			saAliasResolvesToTypeQuery(vd.Type, scope.aliasOf) ||
			saIsGenericAlias(vd.Type, scope.aliasOf)) {
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
			// f64 binding（owned 登记，drain 统一释放；call 结果 move 链被
			// verifier 追踪（`Number("7")` leak 实证），`!f64` 合法已验证）。
			if vd.Initializer == nil {
				if isConst {
					ln, col := pos(d.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
					return false
				}
				w.Write(fmt.Sprintf("  %s = 0\n", name))
				scope.types[name] = "f64"
				saDeclareOwned(scope, name)
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
			saDeclareOwned(scope, name)
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
		// 注解 i32/bool 位记种检查（串/实例句柄禁入；无注解推断沿传播口径不动；铁律 4）。
		if msg := saCheckI32Value(scope, op); msg != "" {
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

// saFloatLitOperand 无注解浮字面量初值求 f64 操作数（字面直返文本；+ 号去号
// 返文本；- 号由调用点经 `fneg` 直写目标（纯别名首定禁重绑，param 同律），
// 与上游实发同形；其余沿旧门）。
func saFloatLitOperand(e *ast.Node) (string, bool) {
	if e != nil && e.Kind == ast.KindNumericLiteral && saIsFloatLit(e.Text()) {
		return e.Text(), true
	}
	if e != nil && e.Kind == ast.KindPrefixUnaryExpression {
		un := e.AsPrefixUnaryExpression()
		if un != nil && un.Operand != nil && un.Operand.Kind == ast.KindNumericLiteral &&
			saIsFloatLit(un.Operand.Text()) &&
			(un.Operator == ast.KindMinusToken || un.Operator == ast.KindPlusToken) {
			if un.Operator == ast.KindPlusToken {
				return un.Operand.Text(), true
			}
			return "-" + un.Operand.Text(), true
		}
	}
	return "", false
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
		init.Kind == ast.KindTemplateExpression || init.Kind == ast.KindTaggedTemplateExpression ||
		init.Kind == ast.KindTypeOfExpression {
		// 无注解串推断（字面量/模板/tagged/typeof 皆串位；tag 门在求值内）。
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
	if init.Kind == ast.KindElementAccessExpression && saIsStrExpr(init, scope) {
		// 串元数组元素绑定记 str（与直接下标串位同形；`?.` 沿 saIsStrExpr 旧门；
		// 否则误记 i32 会把句柄按整数打印，batch-4 实证）。
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
		// 同文件数组函数返回句柄（`const a = get()`；签名预扫记 arr）。
		if k, ok := saCallRetKind(init.AsCallExpression(), scope); ok && k == "arr" {
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
			saAdoptHash(scope, name, init)
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
		// f64 返回调用按浮种建种（用户 f64 函数 + Number/parseFloat 转换）。
		if k, ok := saCallRetKind(init.AsCallExpression(), scope); ok && k == "f64" {
			op, msg := saEvalF64Strict(w, init, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(init.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = "f64"
			saDeclareOwned(scope, name)
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
	if init.Kind == ast.KindBinaryExpression {
		if be := init.AsBinaryExpression(); be != nil && be.OperatorToken != nil &&
			be.OperatorToken.Kind == ast.KindQuestionQuestionToken &&
			(saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)) {
			// 无注解空合推断（串臂即串；与三元分支同形）。
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
		if be := init.AsBinaryExpression(); be != nil && be.OperatorToken != nil &&
			be.OperatorToken.Kind == ast.KindPlusToken &&
			(saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)) {
			// 无注解拼接推断（任一臂串值即串；与空合分支同形；上游初值种
			// 绑定同形，纯字面量 `"a"+"b"` 亦经此路）。
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
		if be := init.AsBinaryExpression(); be != nil && be.OperatorToken != nil &&
			be.OperatorToken.Kind == ast.KindQuestionQuestionToken &&
			saIsInstOperandSyntax(be.Left, scope) &&
			(saIsNullLit(be.Right, scope) || saIsInstOperandSyntax(be.Right, scope)) {
			// 无注解空合推断（实例臂即实例；与三元分支同形，槽宽按 ptr）。
			h, msg := saLowerNullishInst(w, be, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(init.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, h))
			if k, ok := scope.types[h]; ok && len(k) > 5 && k[:5] == "inst:" {
				scope.types[name] = k
			} else {
				scope.types[name] = "i32"
			}
			saConsumeOwn(scope, h)
			saDeclareOwned(scope, name)
			saCopyInstFn(scope, h, name)
			saPropArrNest(scope, h, name)
			return true
		}
	}
	op, msg := saEvalI32(w, init, scope, pos, refusals, nextTemp)
	if msg != "" {
		// 无注解浮字面量推断 f64（上游初值种绑定同形；用点经既有 f64 机；
		// 符号位经 `fneg`，与上游实发同形；顶层 const 另步）。
		if fl, ok := saFloatLitOperand(init); ok {
			w.Write(fmt.Sprintf("  %s = %s\n", name, fl))
			scope.types[name] = "f64"
			saDeclarePlain(scope, name)
			return true
		}
		// 前缀取负推断（`-x` 非字面量经严格求值落 `fneg`；纯字面量已由上臂
		// 折叠文本（别名首定禁重绑）；失败透传旧拒）。
		if init != nil && init.Kind == ast.KindPrefixUnaryExpression {
			if un := init.AsPrefixUnaryExpression(); un != nil && un.Operator == ast.KindMinusToken {
				if op, msg := saEvalF64Strict(w, init, scope, pos, refusals, nextTemp); msg == "" {
					w.Write(fmt.Sprintf("  %s = %s\n", name, op))
					scope.types[name] = "f64"
					saDeclarePlain(scope, name)
					return true
				}
			}
		}
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
	return saSynthParamNodes(nodes, classes, aliasOf, enums, saTypeParamSet(saNodeTypeParams(arrow)), saTypeParamConstraints(saNodeTypeParams(arrow)))
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
// 缺省表达式原节点供短调字面量回放；无初值 `?` 记可省（undefined 即 0，
// 补齐侧按种垫）；非字面量短调大声拒；形状证据：封存
// funcDefaults/funcDefaultExpr + padDefaultArgs）。
func saFuncDefaultTables(nodes []*ast.Node) ([]bool, []*ast.Node) {
	defs := make([]bool, len(nodes))
	dexprs := make([]*ast.Node, len(nodes))
	for i, p := range nodes {
		pd := p.AsParameterDeclaration()
		if pd != nil && pd.Initializer != nil {
			defs[i] = true
			dexprs[i] = pd.Initializer
		} else if pd != nil && pd.QuestionToken != nil {
			defs[i] = true
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
		if tgt.Kind != ast.KindTypeReference {
			return "", false
		}
		nr := tgt.AsTypeReferenceNode()
		if nr == nil || nr.TypeName == nil ||
			nr.TypeName.Kind != ast.KindIdentifier || nr.TypeArguments != nil {
			return "", false
		}
		cur = nr.TypeName.Text()
	}
	return "", false
}

// saIsGenericAlias 报告注解是否为已知类型别名基带泛型实参（如条件类型
// 实例化 `A<number>`；句柄门显式跳过别名基，类/标量基沿各自旧门）。
func saIsGenericAlias(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode) bool {
	if t == nil || t.Kind != ast.KindTypeReference || len(aliasOf) == 0 {
		return false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return false
	}
	if ref.TypeArguments == nil {
		return false
	}
	_, ok := aliasOf[ref.TypeName.Text()]
	return ok
}

// saAliasResolvesToTypeQuery 别名链终点是否为 typeof 查询（链式跟随、防环；
// 直接 typeof 注解走初值推断既有回退，间接别名同形并入）。
func saAliasResolvesToTypeQuery(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode) bool {
	if t == nil || t.Kind != ast.KindTypeReference || len(aliasOf) == 0 {
		return false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return false
	}
	if ref.TypeArguments != nil {
		return false
	}
	seen := map[string]bool{}
	cur := ref.TypeName.Text()
	for i := 0; i < len(aliasOf)+1; i++ {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		tgt, ok := aliasOf[cur]
		if !ok || tgt == nil {
			return false
		}
		if tgt.Kind == ast.KindTypeQuery {
			return true
		}
		if tgt.Kind != ast.KindTypeReference {
			return false
		}
		nr := tgt.AsTypeReferenceNode()
		if nr == nil || nr.TypeName == nil ||
			nr.TypeName.Kind != ast.KindIdentifier || nr.TypeArguments != nil {
			return false
		}
		cur = nr.TypeName.Text()
	}
	return false
}

// saReturnKindRef 消解返回注解（saReturnKind 标量集之外）：单标识符别名经
// 别名表映回 number/boolean/string（`type Count = i32`；arr 别名在返回位仍拒，
// 无用例）；具名接口/类经 classes 表记 `inst:Name`（`-> ptr`，封存上游实发
// `@make(x: i32, y: i32) -> ptr:`）。泛型实例化/未知名沿旧门 false。
// 注意 160 分歧：上游把 i32 别名参数/返回标 `ptr`（疑似未消解回退），本仓按
// 别名语义消解为 i32——值流一致且更忠实，禁静默错码高于逐字同形。
// 匿名返回按域集匹配唯一接口（`(): {a:i32;b:i32}` 经 saMatchIface 记 inst;
// 0/多匹配沿旧门 false 大声拒；封存 checker_layout l2 + matchLayout 名集匹配）。
func saReturnKindRef(t *ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if k, ok := saReturnKind(t); ok {
		return k, true
	}
	if t == nil {
		return "", false
	}
	if t.Kind == ast.KindTypeLiteral {
		lit := t.AsTypeLiteralNode()
		if lit == nil || lit.Members == nil {
			return "", false
		}
		var keys []string
		for _, m := range lit.Members.Nodes {
			if m == nil || m.Kind != ast.KindPropertySignature {
				return "", false
			}
			nm := m.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				return "", false
			}
			keys = append(keys, nm.Text())
		}
		if len(keys) == 0 {
			return "", false
		}
		if hit, _ := saMatchIface(keys, classes); hit != nil {
			return "inst:" + hit.name, true
		}
		return "", false
	}
	if t.Kind == ast.KindUnionType {
		// 单类+空联合返回记实例句柄（`Size|null` 即 Size 布局，空吸收为 0 句柄；
		// 与形参 1979-1989 同形；封存 union 首个已知布局；其余联合沿旧门）。
		if inst, ok := saUnionInstKind(t.AsUnionTypeNode(), classes); ok {
			return inst, true
		}
		return "", false
	}
	if t.Kind != ast.KindTypeReference {
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
	if ref == nil || ref.TypeName == nil {
		return "", false
	}
	if ref.TypeName.Kind != ast.KindIdentifier {
		// 限定返回 `N.C`（与 `N_C` 布局键同形；具化泛型沿旧门）。
		if ref.TypeArguments != nil {
			return "", false
		}
		if qn, ok := saQualifiedTypeName(t); ok {
			if _, ok := classes[qn]; ok {
				return "inst:" + qn, true
			}
		}
		return "", false
	}
	if ref.TypeArguments != nil {
		// 泛型返回具化（`(): Box<i32>` 记 `inst:Box_i32`；不可具化沿旧门；
		// 封存 layoutOfAnnotation 同形）+ Record 返回记 map 种（Z1）。
		if len(ref.TypeArguments.Nodes) > 0 {
			if lname, ok := saInstantiateIface(ref.TypeName.Text(), ref.TypeArguments.Nodes, classes); ok {
				return "inst:" + lname, true
			}
			if _, ok := saRecordValueKind(t, classes); ok {
				return "map", true
			}
		}
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
// initializer kind; template literal types likewise erase to the initializer).
func saIsEraseAnnotation(t *ast.TypeNode) bool {
	if t == nil {
		return false
	}
	return t.Kind == ast.KindAnyKeyword || t.Kind == ast.KindUnknownKeyword || t.Kind == ast.KindNeverKeyword ||
		t.Kind == ast.KindTemplateLiteralType
}

// saAliasErasesToTemplate reports aliases whose underlying type is a template
// literal (narrow: other aliases keep existing resolution; declaration site only).
func saAliasErasesToTemplate(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode) bool {
	for i := 0; i < 8 && t != nil && t.Kind == ast.KindTypeReference; i++ {
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return false
		}
		next, ok := aliasOf[ref.TypeName.Text()]
		if !ok || next == nil {
			return false
		}
		t = next
	}
	return t != nil && t.Kind == ast.KindTemplateLiteralType
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
func saFoldTopLevelConst(st *ast.Node, consts map[string]string, strs map[string]bool, maths map[string]string, pos func(int) (int, int), refusals *[]SARefusal, tcx *saTypeCtx) bool {
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
		// env-probe 取臂（`typeof U==="undefined"?A:B` 未声明名化纯量；
		// 封存 probeFoldedInit:212-222；已声明/异形沿旧门）。
		if init.Kind == ast.KindConditionalExpression {
			if arm, ok := saEnvProbeArm(init.AsConditionalExpression(), tcx); ok {
				init = arm
			}
		}
		switch init.Kind {
		case ast.KindNumericLiteral:
			// 浮字面量记文本（f64 位经字面量直通，用点 i32 位沿旧门拒；
			// 负号前缀非字面沿旧路；形状证据：上游初值折叠同形）。
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

// saIsTypeOnlyNamespace 判定是否为纯类型 namespace（成员皆为接口/类型别名/
// 枚举声明，无运行时码，可整块擦除；含值成员一律 false，调用方沿旧拒）。
// 形状证据：封存 lowerNamespace:139-183 + prescanNamespaces 类型成员经 lowerTypeDecl
// 只记录无发射 + nsRegisterOne nsKindInterface/nsKindEnum/nsKindType 分支；空体恒真。
func saIsTypeOnlyNamespace(st *ast.Node) bool {
	if st == nil || st.Kind != ast.KindModuleDeclaration {
		return false
	}
	if saIsAmbientModule(st) {
		return false
	}
	md := st.AsModuleDeclaration()
	if md == nil || md.Body == nil || md.Body.Kind != ast.KindModuleBlock {
		return false
	}
	for _, m := range md.Body.AsModuleBlock().Statements.Nodes {
		if m == nil {
			return false
		}
		switch m.Kind {
		case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindEnumDeclaration:
		default:
			return false
		}
	}
	return true
}

// saPrescanFuncSig 预扫单个函数声明签名记入 funcs 表（顶层与命名空间成员
// 共用；键由调用方定：顶层为名，ns 成员为 `N_f`/`N.f` 双键）。
// 形状证据：封存 program.go:435/512 按定义收集 rets/arity（与既有预扫二同源）。
func saPrescanFuncSig(fn *ast.FunctionDeclaration, st *ast.Node, key string, funcs map[string]saFuncSig, tcx *saTypeCtx, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, enums map[string]map[string]int64, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if _, dup := funcs[key]; dup {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate function " + key})
		return false
	}
	nparams := 0
	if fn.Parameters != nil {
		nparams = len(fn.Parameters.Nodes)
	}
	isVoid := false
	retKind := ""
	rtype := saUnwrapPromise(fn.Type)
	if saIsBareTypeParam(rtype, saTypeParamSet(fn.TypeParameters)) {
		// erased own type parameter defaults to number.
		retKind, isVoid = "number", false
	} else if k, v, ok := saPrescanRet(rtype, st, tcx, classes, aliasOf); ok {
		retKind = k
		isVoid = v
	}
	var pk []string
	if kinds, ok := saParamKinds(fn, classes, aliasOf, enums); ok {
		if names, ok := saParamNames(fn); ok {
			for _, n := range names {
				pk = append(pk, kinds[n])
			}
		}
	}
	var defs []bool
	var dexprs []*ast.Node
	if fn.Parameters != nil {
		defs, dexprs = saFuncDefaultTables(fn.Parameters.Nodes)
	}
	hasRest := false
	if fn.Parameters != nil && len(fn.Parameters.Nodes) > 0 {
		if last := fn.Parameters.Nodes[len(fn.Parameters.Nodes)-1]; last != nil {
			if pd := last.AsParameterDeclaration(); pd != nil && pd.DotDotDotToken != nil {
				hasRest = true
			}
		}
	}
	funcs[key] = saFuncSig{params: nparams, isVoid: isVoid, retKind: retKind, paramKinds: pk, defaults: defs, defaultExprs: dexprs, hasRest: hasRest}
	return true
}

// saNsMember 是展平后的命名空间成员（路径联结键 + 原节点）。
type saNsMember struct {
	under   string    // 发射键：N_M_f（const 为父路径，折叠键另计）
	dotted  string    // 调用键：N.M.f
	scope   string    // 父路径：N_M（heritage 回退域；顶层 ns 即首段）
	node    *ast.Node // 成员声明节点（const 为整条 VariableStatement）
	decl    *ast.Node // const declarator 节点（非 const 为空）
	isFunc  bool      // 函数成真
	isConst bool      // 纯量声明成真（类成员两假；类型成员展平期跳过）
}

// saFlattenNsMembers 递归展平运行时 namespace（函数/类成员全收，类型成员
// 跳过；含值（const/let 等）或非法名任一即整块 false，调用方沿旧 kind-268
// 拒）。键按路径联结（`N_M_f`/`N.M.f`；上游 nsDefName 限定同形）。
// 形状证据：封存 prescanNamespaces 类型/类分支 + lowerPendingNamespaces。
func saFlattenNsMembers(st *ast.Node) ([]saNsMember, bool) {
	if st == nil || st.Kind != ast.KindModuleDeclaration {
		return nil, false
	}
	if saIsAmbientModule(st) {
		return nil, false
	}
	md := st.AsModuleDeclaration()
	if md == nil {
		return nil, false
	}
	nm := md.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return nil, false
	}
	if md.Body == nil || md.Body.Kind != ast.KindModuleBlock {
		return nil, false
	}
	var out []saNsMember
	var walk func(path []string, members []*ast.Node) bool
	walk = func(path []string, members []*ast.Node) bool {
		for _, m := range members {
			if m == nil {
				return false
			}
			switch m.Kind {
			case ast.KindFunctionDeclaration, ast.KindClassDeclaration:
				mn := m.Name()
				if mn == nil || mn.Kind != ast.KindIdentifier {
					return false
				}
				segs := append(append([]string{}, path...), mn.Text())
				out = append(out, saNsMember{
					under:  strings.Join(segs, "_"),
					dotted: strings.Join(segs, "."),
					scope:  strings.Join(path, "_"),
					node:   m,
					isFunc: m.Kind == ast.KindFunctionDeclaration,
				})
			case ast.KindVariableStatement:
				// 导出 const/let 声明逐 declarator 记纯量条（折叠消费；
				// `let` 须未被赋值（赋值即跳槽，用点大声拒，可变槽另步）；
				// 非导出/using 沿旧门整块拒）。
				vs := m.AsVariableStatement()
				if vs == nil || vs.DeclarationList == nil {
					return false
				}
				if !ast.HasModifier(m, ast.ModifierFlagsExport) {
					return false
				}
				dl := vs.DeclarationList.AsVariableDeclarationList()
				if dl == nil || len(dl.Declarations.Nodes) == 0 {
					return false
				}
				// const 与 let 皆容（`let` 折叠与否由赋值扫描定；using 沿旧门）。
				if dl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
					return false
				}
				for _, dd := range dl.Declarations.Nodes {
					if dd == nil {
						return false
					}
					vd := dd.AsVariableDeclaration()
					if vd == nil {
						return false
					}
					vnm := vd.Name()
					if vnm == nil || vnm.Kind != ast.KindIdentifier {
						return false
					}
					out = append(out, saNsMember{
						under:   strings.Join(path, "_"),
						dotted:  strings.Join(append(append([]string{}, path...), vnm.Text()), "."),
						scope:   strings.Join(path, "_"),
						node:    m,
						decl:    dd,
						isFunc:  false,
						isConst: true,
					})
				}
			case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration,
				ast.KindEnumDeclaration:
				// 类型成员无码擦除（与纯类型 ns 同例）。
			case ast.KindModuleDeclaration:
				sub := m.AsModuleDeclaration()
				if sub == nil {
					return false
				}
				snm := sub.Name()
				if snm == nil || snm.Kind != ast.KindIdentifier {
					return false
				}
				if sub.Body == nil || sub.Body.Kind != ast.KindModuleBlock {
					return false
				}
				if saIsAmbientModule(m) {
					return false
				}
				if !walk(append(append([]string{}, path...), snm.Text()), sub.Body.AsModuleBlock().Statements.Nodes) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !walk([]string{nm.Text()}, md.Body.AsModuleBlock().Statements.Nodes) {
		return nil, false
	}
	return out, true
}

// saFoldMixedNsConsts 折叠混合 ns 的导出纯量（`N.M.K` 键入顶层折叠值域，
// 与 `N.K` 读位同键；纯度与 saFoldNamespaceConsts 逐 declarator 同形；
// 非纯/重名即整块大声拒，副作用永不静默吞；`let`/`var` 一律走槽（被赋值或
// 零填充），折叠不碰。
// 调用方：整块折叠不成且展平门过时（预扫折叠环；发射侧照常直落函数）。
func saFoldMixedNsConsts(st *ast.Node, consts map[string]string, strs map[string]bool, assigned map[string]bool, pos func(int) (int, int), refusals *[]SARefusal) bool {
	members, ok := saFlattenNsMembers(st)
	if !ok {
		return false
	}
	// 本遍去重（整块折叠失败可留部分写，幂等覆写；真重名（无论值同否）大声拒）。
	seen := map[string]bool{}
	folded := false
	for _, mb := range members {
		if !mb.isConst {
			continue
		}
		vd := mb.decl.AsVariableDeclaration()
		if vd == nil {
			return false
		}
		key := mb.dotted
		if seen[key] {
			ln, col := pos(mb.decl.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate const " + key})
			return false
		}
		seen[key] = true
		// `let`/`var` 一律走槽（被赋值或零填充；用点沿槽门，折叠不碰）。
		if !isNsConstDecl(mb.node) {
			continue
		}
		init := vd.Initializer
		if init == nil {
			ln, col := pos(mb.decl.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace const member must be a pure literal (effectful initializers are not foldable)"})
			return false
		}
		switch init.Kind {
		case ast.KindNumericLiteral:
			consts[key] = init.Text()
			folded = true
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
			consts[key] = init.Text()
			strs[key] = true
			folded = true
		case ast.KindTrueKeyword:
			consts[key] = "1"
			folded = true
		case ast.KindFalseKeyword:
			consts[key] = "0"
			folded = true
		case ast.KindIdentifier:
			// 同域前向读（父路径按名折叠；串性透传；未定义沿旧门）。
			if t, ok := consts[mb.scope+"."+init.Text()]; ok {
				consts[key] = t
				if strs[mb.scope+"."+init.Text()] {
					strs[key] = true
				}
				folded = true
				continue
			}
			ln, col := pos(mb.decl.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace const member must be a pure literal (effectful initializers are not foldable)"})
			return false
		default:
			ln, col := pos(mb.decl.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace const member must be a pure literal (effectful initializers are not foldable)"})
			return false
		}
	}
	return folded
}

// saEnterNsScopeConsts 为单个 ns 成员打开裸兄弟读窗口（同域及祖先域已折
// 纯量按名直挂 + 槽指针直挂，内层覆写；出域恢复原值/原 presence；与顶层折叠
// 键独立；槽读写经既有 modVars 通道零改）。
// 调用方：ns 成员函数发射前后（语句同步落字，out-of-line 体呈文本 baked）。
func saEnterNsScopeConsts(topConsts map[string]string, topStr map[string]bool, modVars map[string]*saModState, members []saNsMember, scope string) func() {
	type saved struct {
		v    string
		s    bool
		hasV bool
		hasS bool
		m    *saModState
		hasM bool
	}
	type seed struct {
		name string
		text string
		str  bool
		mod  *saModState
	}
	byScope := map[string][]seed{}
	for _, mb := range members {
		if !mb.isConst {
			continue
		}
		if mb.scope != scope && !strings.HasPrefix(scope, mb.scope+"_") {
			continue
		}
		vd := mb.decl.AsVariableDeclaration()
		if vd == nil {
			continue
		}
		nm := vd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		if ms, ok := modVars[mb.dotted]; ok && ms != nil && ms.w == "i32" {
			byScope[mb.scope] = append(byScope[mb.scope], seed{name: nm.Text(), mod: ms})
			continue
		}
		text, ok := topConsts[mb.dotted]
		if !ok {
			continue
		}
		byScope[mb.scope] = append(byScope[mb.scope], seed{name: nm.Text(), text: text, str: topStr[mb.dotted]})
	}
	// 外层先挂，内层覆写（路径长度升序）。
	var scopes []string
	for s := range byScope {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	restores := map[string]saved{}
	seen := map[string]bool{}
	var applied []string
	for _, s := range scopes {
		for _, sd := range byScope[s] {
			if !seen[sd.name] {
				v, hasV := topConsts[sd.name]
				b, hasS := topStr[sd.name]
				m, hasM := modVars[sd.name]
				restores[sd.name] = saved{v: v, s: b, hasV: hasV, hasS: hasS, m: m, hasM: hasM}
				seen[sd.name] = true
				applied = append(applied, sd.name)
			}
			if sd.mod != nil {
				modVars[sd.name] = sd.mod
				delete(topConsts, sd.name)
				delete(topStr, sd.name)
				continue
			}
			topConsts[sd.name] = sd.text
			if sd.str {
				topStr[sd.name] = true
			} else {
				delete(topStr, sd.name)
			}
			delete(modVars, sd.name)
		}
	}
	return func() {
		for _, name := range applied {
			r := restores[name]
			if r.hasV {
				topConsts[name] = r.v
			} else {
				delete(topConsts, name)
			}
			if r.hasS {
				topStr[name] = r.s
			} else {
				delete(topStr, name)
			}
			if r.hasM {
				modVars[name] = r.m
			} else {
				delete(modVars, name)
			}
		}
	}
}

// isNsConstDecl 判定 ns 成员声明是否为 const（`let` 另判赋值；形状证据：
// NodeFlagsConst 位，顶层折叠同谓词）。
func isNsConstDecl(st *ast.Node) bool {
	if st == nil || st.Kind != ast.KindVariableStatement {
		return false
	}
	vs := st.AsVariableStatement()
	if vs == nil || vs.DeclarationList == nil {
		return false
	}
	dl := vs.DeclarationList.AsVariableDeclarationList()
	if dl == nil {
		return false
	}
	return dl.AsNode().Flags&ast.NodeFlagsConst != 0
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
func saLowerArrowConst(w printer.EmitTextWriter, name string, arrow *ast.Node, funcs map[string]saFuncSig, enums map[string]map[string]int64, enumNonInt map[string]map[string]bool, classes map[string]*saClassDef, topConsts map[string]string, topStr map[string]bool, topMaths map[string]string, modVars map[string]*saModState, src string, mainRenamed bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool, tcx *saTypeCtx, aliasOf map[string]*ast.TypeNode, imports, importRemote map[string]string, pendingFns *[]string, arrowSeq *int, link *saFileLink) {
	if arrow.Kind == ast.KindFunctionExpression {
		if fe := arrow.AsFunctionExpression(); fe != nil && fe.AsteriskToken != nil {
			ln, col := pos(arrow.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "generators are not lowerable"})
			return
		}
	}
	// async 修饰同步擦除（与函数声明 T1 同律；封存 lowerArrowBinding 全程
	// 无 async 门；无参块体无注解值返回由 checker 恒判 void
	//（saScalarReturnKind 不认 Promise 对象）走既有 void 门拒）。
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
	} else if ab := arrow.Body(); ab != nil && ab.Kind != ast.KindBlock {
		// 无注解表达式体恒为值函数 i32（与局部 1457 行同规则；镜像上游
		// value_fn 共用规则，封存 saemit.go:1099-1117；checker 回退 void
		// 不得吞掉该规则）。
		retKind, isVoid = "i32", false
	} else if k, v, ok := saPrescanRet(nil, arrow, tcx, classes, aliasOf); ok {
		retKind, isVoid = k, v
	}
	emitName := name
	if emitName == "main" && mainRenamed {
		emitName = "main__user"
	}
	// program 库文件前缀（与函数发射同形；同文件自调用经 saLinkCallee
	// defPrefix 改写一致；形状证据：封存 lowerArrowBinding 发射限定）。
	sig := "@" + saLinkDefPrefix(link) + emitName + "(" + saSigParamList(kinds, params) + ")"
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
	linkPrefix, linkResolve := saLinkDefPrefix(link), saLinkResolveMap(link)
	scope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, enumNonInt: enumNonInt, classes: classes, topConsts: topConsts, topStr: topStr, modVars: modVars, mainRenamed: mainRenamed, nextLabel: nextLabel, retKind: retKind, strPool: strPool, src: src, addImport: needImport, aliasOf: aliasOf, imports: imports, importRemote: importRemote, pendingFns: pendingFns, arrowSeq: arrowSeq}
	scope.defPrefix = linkPrefix
	scope.linkResolve = linkResolve
	scope.linkHarvests = saLinkHarvestsMap(link)
	saSeedTopMaths(scope, topMaths)
	for _, p := range params {
		scope.types[p] = kinds[p]
		saDeclareOwned(scope, p)
	}
	// Record 形参值种播种（读侧按表记种；无表恒 i32；与函数序同形）。
	if pl := arrow.ParameterList(); pl != nil {
		saSeedParamMapVals(pl.Nodes, kinds, classes, scope)
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
			// 空体落空即返（值函数补 `ret 0`；上游同形，见函数发射位）。
			saReleaseAllOwnedExcept(w, scope, "")
			if isVoid {
				w.Write("  ret\n")
			} else {
				w.Write("  ret 0\n")
			}
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
			// 落空即返（值函数补 `ret 0`；上游同形，见函数发射位）。
			saReleaseAllOwnedExcept(w, scope, "")
			if isVoid {
				w.Write("  ret\n")
			} else {
				w.Write("  ret 0\n")
			}
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
	// void 调用表达式体先判单发射：后走求值尝试会先落一次字（void 值位拒），
	// 再调即双发射（225 双打印实证，双边同错）；用户 void 函数经 saCallRetKind，
	// console.* 方法恒 void（log/error/time/timeEnd/clear 皆无返回值），先判命中
	// 者直走单发射；其余 void 内建沿旧路（残留双发射已文档化）。
	if body.Kind == ast.KindCallExpression {
		ceVO := body.AsCallExpression()
		voidKnown := false
		if k, ok := saCallRetKind(ceVO, scope); ok && k == "void" {
			voidKnown = true
		}
		if !voidKnown && ceVO.Expression != nil && ceVO.Expression.Kind == ast.KindPropertyAccessExpression {
			if pa := ceVO.Expression.AsPropertyAccessExpression(); pa != nil && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "console" {
				voidKnown = true
			}
		}
		if voidKnown {
			_, _, cmsg := saEvalCall(w, ceVO, scope, pos, refusals, nextTemp)
			if cmsg != "" {
				return refuse(body, cmsg)
			}
			saReleaseAllOwnedExcept(w, scope, "")
			w.Write("  ret 0\n")
			return true
		}
	}
	op, msg := saEvalReturnOperand(w, body, retKind, scope, pos, refusals, nextTemp)
	if msg != "" {
		// 表达式体求值不成而语句可 lower 者（如 void 调用）：按语句发射 +
		// `ret 0`（镜像上游值函数体语句序 + return 0；此分支签名恒 i32，
		// 显式 void 注解沿上游注解优先仍走上门旧拒；证据 /tmp/vcmp/p1upb b.sai）。
		if body.Kind == ast.KindCallExpression {
			if _, _, cmsg := saEvalCall(w, body.AsCallExpression(), scope, pos, refusals, nextTemp); cmsg == "" {
				saReleaseAllOwnedExcept(w, scope, "")
				w.Write("  ret 0\n")
				return true
			}
		}
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

// saArrowUsesThis 报告箭头体是否直用 `this`/`super`（嵌套箭头穿透：其
// this/super 即外层接收者；嵌套函数/类有自属接收者，不计入；本层 this 由
// 调用方判定；super 恒需接收者：`saSuperBase` 以 thisSelf 锚定基布局，
// 故 super 箭头同 `this` 补 `__this` 尾参；上游凭调用点接收者名硬编码全局
// 直读（探针 /tmp/suparr 实发 `load d + 0`），禁照抄，沿 step392 口径）。
func saArrowUsesThis(body *ast.Node) bool {
	found := false
	var walk func(x *ast.Node)
	walk = func(x *ast.Node) {
		if x == nil || found {
			return
		}
		if x.Kind == ast.KindThisKeyword || x.Kind == ast.KindSuperKeyword {
			found = true
			return
		}
		switch x.Kind {
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression,
			ast.KindClassDeclaration, ast.KindClassExpression:
			return
		}
		x.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk(body)
	return found
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
	// async 修饰同步擦除（与顶层 saLowerArrowConst 同门；T1 + 封存同源）。
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
	// 方法接收者捕获（箭头 `this` 即方法 this；按值尾参传递，与值捕获
	// 同归属口径；圈外 this 沿既有 `thisSelf == ""` 各门拒因，不动）。
	usesThis := scope.thisSelf != "" && scope.thisClass != "" && saArrowUsesThis(body)
	thisCap := ""
	if usesThis {
		thisCap = "__this"
		for _, p := range params {
			if p == thisCap {
				return refuse(arrow, "this capture name is shadowed")
			}
		}
		for _, cp := range captured {
			if cp == thisCap {
				return refuse(arrow, "this capture name is shadowed")
			}
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
	if thisCap != "" {
		sigNames = append(sigNames, thisCap)
		allKinds[thisCap] = "ptr"
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
	saScopeLinkCopy(inner, scope)
	// 内层继承外层 math 别名（Math.* 方法体内可用；顶层 maths 已在外层种入）。
	saSeedTopMaths(inner, scope.mathAlias)
	for k, v := range paramKinds {
		inner.types[k] = v
	}
	// 捕获以同名尾参入内层作用域（体读名即读形参；封存 declareOwned 补登）。
	for _, cp := range captured {
		inner.types[cp] = scope.types[cp]
	}
	// 接收者捕获以内层 `this` 落定（体 `this` 经既有接收者通道；封存
	// saInlineMethodCore 置位同形；`thisClass` 供方法/字段布局消解）。
	if thisCap != "" {
		inner.types[thisCap] = "ptr"
		inner.thisSelf, inner.thisClass = thisCap, scope.thisClass
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
	scope.funcs[gen] = saFuncSig{params: len(params), isVoid: isVoid, retKind: retKind, paramKinds: saSigKinds(paramKinds, params), arrowCaps: captured, arrowThis: usesThis}
	scope.types[name] = "fn:" + gen
	return true
}

// saSigRetSuffix 返回签名后缀（string/inst 句柄即 ptr，其余 i32；
// 封存上游实发 `-> ptr`（串）与 `@make(…) -> ptr:`（实例））。
func saSigRetSuffix(retKind string) string {
	if retKind == "string" || retKind == "map" || retKind == "arr" || strings.HasPrefix(retKind, "inst:") {
		return " -> ptr"
	}
	// f64 直通（上游实发 `-> f64`；形参 `saSigParamType` 同形在先）。
	if retKind == "f64" {
		return " -> f64"
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
// 限定写（`N.K =`/`N.K++`）记点键（顶层裸名查找零影响；ns 槽注册消费）。
func saAssignedNames(stmts []*ast.Node) map[string]bool {
	out := map[string]bool{}
	mark := func(n *ast.Node) {
		if n != nil && n.Kind == ast.KindIdentifier {
			out[n.Text()] = true
		}
	}
	markQualified := func(n *ast.Node) {
		if n == nil || n.Kind != ast.KindPropertyAccessExpression {
			return
		}
		pa := n.AsPropertyAccessExpression()
		if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
			return
		}
		if pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			return
		}
		out[pa.Expression.Text()+"."+pa.Name().Text()] = true
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
				markQualified(be.Left)
			}
		case ast.KindPrefixUnaryExpression:
			if un := n.AsPrefixUnaryExpression(); un != nil && (un.Operator == ast.KindPlusPlusToken || un.Operator == ast.KindMinusMinusToken) {
				mark(un.Operand)
				markQualified(un.Operand)
			}
		case ast.KindPostfixUnaryExpression:
			if un := n.AsPostfixUnaryExpression(); un != nil && (un.Operator == ast.KindPlusPlusToken || un.Operator == ast.KindMinusMinusToken) {
				mark(un.Operand)
				markQualified(un.Operand)
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
		if vd.Type != nil {
			// 无初值串注解（`let s: string`/`declare` 成员）：空串物化，
			// 读未写得 ""（注册表零填充 + 串标志恒置位，空串亦物化口径）；
			// 写后读与局部 `let s: string` 零柄一致。
			if k, ok2 := saAnnotKind(vd.Type); ok2 && k == "str" {
				w, lit = "str", ""
			}
		}
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
	return w, lit, zero, true
}

// saModClaimName 判定单 declarator 是否归槽（具名 + `let`/`var` +
// i32/串初值或串注解零值 + 被赋值（force 置位即命名空间/ambient 无条件认领，
// 读未写零填充；顶层沿旧门）；`const`/箭头/异形交旧路；封存 modClaim:560-591 子集）。
func saModClaimName(d *ast.Node, vd *ast.VariableDeclaration, assigned map[string]bool, force bool) (string, string, bool) {
	if vd == nil {
		return "", "", false
	}
	nm := vd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", "", false
	}
	name := nm.Text()
	if !force && !assigned[name] {
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
	feedVS := func(vs *ast.VariableStatement, force bool) {
		if vs == nil || vs.DeclarationList == nil {
			return
		}
		vdl := vs.DeclarationList.AsVariableDeclarationList()
		if vdl == nil {
			return
		}
		if vs.DeclarationList.AsNode().Flags&ast.NodeFlagsConst != 0 {
			return
		}
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			name, w, ok := saModClaimName(d, vd, assigned, force)
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
	for _, st := range stmts {
		if st == nil {
			continue
		}
		if st.Kind == ast.KindVariableStatement {
			feedVS(st.AsVariableStatement(), false)
			continue
		}
		// `declare global { ... }` 环境变量授权：成员 var/let 经同一槽口径
		// （串名模块不碰；块仍整块擦除无码；从未赋值名沿认领门拒收——宿主
		// 注入无底座，读未写即拒，dd1 同族；形状证据：封存 lowerNamespace
		// ambient 擦除 + modstate 槽机制）。
		if st.Kind != ast.KindModuleDeclaration || !ast.HasModifier(st, ast.ModifierFlagsAmbient) {
			continue
		}
		md := st.AsModuleDeclaration()
		if md == nil || md.Body == nil || md.Body.Kind != ast.KindModuleBlock {
			continue
		}
		if nm := md.Name(); nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() != "global" {
			continue
		}
		for _, m := range md.Body.AsModuleBlock().Statements.Nodes {
			if m == nil || m.Kind != ast.KindVariableStatement {
				continue
			}
			feedVS(m.AsVariableStatement(), true)
		}
	}
	// 命名空间可变槽（`export let K`；`N.K` 键；i32/串初值或零值子集；
	// 读未写零填充（注册表口径）；异形沿旧门用点拒；上游 nsMutableState 槽同形）。
	for _, st := range stmts {
		members, ok := saFlattenNsMembers(st)
		if !ok {
			continue
		}
		for _, mb := range members {
			if !mb.isConst || isNsConstDecl(mb.node) {
				continue
			}
			vd := mb.decl.AsVariableDeclaration()
			if vd == nil {
				continue
			}
			if nm := vd.Name(); nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			key := mb.dotted
			if _, dup := out[key]; dup {
				ln, col := pos(mb.decl.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "module variable " + key + " is already declared (redefinition is not lowerable)"})
				continue
			}
			if _, dup := funcs[key]; dup {
				ln, col := pos(mb.decl.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "module variable " + key + " collides with an existing definition"})
				continue
			}
			if _, dup := classes[key]; dup {
				ln, col := pos(mb.decl.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "module variable " + key + " collides with an existing definition"})
				continue
			}
			w, lit, zero, ok := saModSlotInit(vd)
			if !ok {
				continue
			}
			if w == "str" {
				// 命名空间可变串槽（`export let S` 串初值；空串物化同顶层门；
				// 无初值串沿旧门用点拒；与顶层串槽同形）。
				ptr, ln, flag := saModStrKeyOf(key)
				out[key] = &saModState{qual: key, w: "str", key: ptr, key2: ln, flag: flag, init: lit}
				continue
			}
			k, flag := saModKeyOf(key)
			ms := &saModState{qual: key, w: "i32", key: k}
			if !zero {
				ms.flag = flag
				ms.init = lit
			}
			out[key] = ms
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
		if _, _, ok := saModClaimName(d, d.AsVariableDeclaration(), assigned, false); ok {
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
	// 调用结果末用即释（verifier 调用帧归属律；trunc/算术纯量不动）。
	saOwnTemp(scope, st)
	saReleaseOwnedTemp(w, scope, st)
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
	// 守卫调用结果末用即释（与 setRaw 同律）。
	saOwnTemp(scope, f)
	saReleaseOwnedTemp(w, scope, f)
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
	// 槽读调用结果末用即释（trunc 即末用；verifier 调用帧归属律）。
	saOwnTemp(scope, t)
	saReleaseOwnedTemp(w, scope, t)
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
	// 具化头末用即释（回读后即死；守卫 init 分支内具化者 join 后释放非支配，
	// 必就地释；直序调用方尾释经旗标跳过）。
	saReleaseOwnedTemp(w, scope, h)
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
	// 守卫调用结果末用即释（串版与 i32 版同律）。
	saOwnTemp(scope, f)
	saReleaseOwnedTemp(w, scope, f)
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
	// 串槽双读调用结果末用即释；回柄登记归属随调用方释放。
	saOwnTemp(scope, tp)
	saReleaseOwnedTemp(w, scope, tp)
	saOwnTemp(scope, tl)
	saReleaseOwnedTemp(w, scope, tl)
	saOwnTemp(scope, h)
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
