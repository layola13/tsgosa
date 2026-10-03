// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_class.go — class 最小子集（step28；JEV 落件 a 置信度 92%）。
// 形状证据总纲：封存 recordClassNamed:9586-9845（字段偏移布局）+
// lowerNewClass:9899-9962（alloc 布局 + 构造 wiring）+
// wireCtorStatement:9968-10001（仅 this.f = param wiring）+
// inlineClassMethod:10188-10280（recv 别 this + 槽汇合，与回调同构，
// 复用 saCallbackValue）+ lowerClassMethodCall:10152-10163。
// 本薄口子集：单类、i32 字段（number/i32 注解，4 字节槽）、构造 wiring、
// 方法内联（i32 形参/体）；继承/抽象/静态/访问器/装饰器/私有名/计算名/
// 参数属性/字段初值一律大声拒。

// saClassField 是类字段（名 + 字节偏移；i32 恒 4 字节）。
type saClassField struct {
	name   string
	offset int
}

// saStaticVal 是静态字面量折叠值（文本 + 种；i32/bool 直接文本，str 具化）。
type saStaticVal struct {
	text string
	kind string
}

// saClassDef 是类定义（字段表 + 构造 + 方法表；接口以 isIface 记，
// 方法/构造恒空，不可 new；parent 为单继承父名，空即无；statics 为
// 静态字面量折叠表，不占实例槽；fkinds 为字段种表，i32/str，str 域 8 字节对齐）。
type saClassDef struct {
	name       string
	fields     []saClassField
	offsets    map[string]int
	fkinds     map[string]string
	size       int
	methods    map[string]*ast.Node
	getters    map[string]*ast.Node
	setters    map[string]*ast.Node
	statics    map[string]saStaticVal
	ctor       *ast.Node
	ctorOwner  string
	parent     string
	isIface    bool
	isAbstract bool
}

// saFieldWidth 返回字段槽宽与对齐（i32 系 4/4，str 句柄头指针 8/8；
// 形状证据：封存 widthOf:268-279 + alignTo:281-289）。
func saFieldWidth(kind string) (int, int) {
	if kind == "str" {
		return 8, 8
	}
	return 4, 4
}

// saAlignOff 按种对齐推进偏移。
func saAlignOff(off int, kind string) int {
	_, align := saFieldWidth(kind)
	if align <= 1 {
		return off
	}
	if r := off % align; r != 0 {
		return off + (align - r)
	}
	return off
}

// saTopLevelClassExpr 识别顶层 `const C = class...` / `const D = class E...`
// （单声明、标识符名、类表达式初值；形状证据：封存 recordClassNamed:9586-9611
// 声明与表达式同形 + bound/own 双名）。
func saTopLevelClassExpr(st *ast.Node) (string, *ast.Node, bool) {
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
	vd := dl.Declarations.Nodes[0].AsVariableDeclaration()
	if vd == nil || vd.Initializer == nil {
		return "", nil, false
	}
	nm := vd.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", nil, false
	}
	init := vd.Initializer
	if init.Kind != ast.KindClassExpression {
		return "", nil, false
	}
	return nm.Text(), init, true
}

// saRecordClass 记录类定义（布局 + 构造 + 方法；无码。重复类名/非法成员拒）。
func saRecordClass(st *ast.Node, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) bool {
	return saRecordClassNamed(st, "", classes, pos, refusals)
}

// saRecordClassNamed 记录类定义（声明与表达式同形；forceName 供
// `const C = class...` 绑定名，自身具名（`class E`）另记同体别名；
// 字段初值表达式忽略（布局只记槽位；封存 recordClassNamed:9691-9743
// 不读 Initializer，初值不求值）。
func saRecordClassNamed(st *ast.Node, forceName string, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) bool {
	var members []*ast.Node
	var heritage *ast.HeritageClauseList
	switch st.Kind {
	case ast.KindClassDeclaration:
		cd := st.AsClassDeclaration()
		members = cd.Members.Nodes
		heritage = cd.HeritageClauses
	case ast.KindClassExpression:
		ce := st.AsClassExpression()
		members = ce.Members.Nodes
		heritage = ce.HeritageClauses
	default:
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "only class declarations and expressions lowerable"})
		return false
	}
	name := forceName
	if name == "" {
		nm := st.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			ln, col := pos(st.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "anonymous classes are not lowerable"})
			return false
		}
		name = nm.Text()
	}
	if _, dup := classes[name]; dup {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate class " + name})
		return false
	}
	def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, getters: map[string]*ast.Node{}, setters: map[string]*ast.Node{}}
	ownFields := map[string]bool{}
	ownMethods := map[string]bool{}
	// 单继承：基布局字段追加在下（父偏移守恒），方法按名拷贝（子类覆写），
	// implements 擦除；多 extends/动态基/未知基/环一律拒。
	// 形状证据：封存 parseHeritage:42-83 + inheritClass:88-215。
	if hc := heritage; hc != nil {
		for _, h := range hc.Nodes {
			if h.Kind != ast.KindHeritageClause {
				continue
			}
			if h.AsHeritageClause().Token != ast.KindExtendsKeyword {
				continue
			}
			types := h.AsHeritageClause().Types
			if types == nil || len(types.Nodes) != 1 {
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class extends needs exactly one base class"})
				return false
			}
			base := ""
			el := types.Nodes[0]
			if el.Kind == ast.KindExpressionWithTypeArguments {
				if ex := el.AsExpressionWithTypeArguments().Expression; ex != nil && ex.Kind == ast.KindIdentifier {
					base = ex.Text()
				}
			} else if el.Kind == ast.KindIdentifier {
				base = el.Text()
			}
			if base == "" {
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class extends needs a plain base class name (mixins are not lowerable)"})
				return false
			}
			if def.parent != "" {
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class extends needs exactly one base class"})
				return false
			}
			bdef, ok := classes[base]
			if !ok || bdef.isIface {
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class " + name + " extends unknown base " + base + " (declare the base class first)"})
				return false
			}
			for p := base; p != ""; {
				if p == name {
					ln, col := pos(st.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class " + name + " has an inheritance cycle through " + base})
					return false
				}
				pb, ok := classes[p]
				if !ok {
					break
				}
				p = pb.parent
			}
			for _, f := range bdef.fields {
				def.fields = append(def.fields, f)
				def.offsets[f.name] = f.offset
				if fk := bdef.fkinds[f.name]; fk != "" {
					def.fkinds[f.name] = fk
				} else {
					def.fkinds[f.name] = "i32"
				}
			}
			for k, v := range bdef.methods {
				if _, ok := def.methods[k]; !ok {
					def.methods[k] = v
				}
			}
			// 存取器随方法同例继承（子类覆写；形状证据：封存 inheritClass:167-198）。
			if def.getters == nil {
				def.getters = map[string]*ast.Node{}
			}
			for k, v := range bdef.getters {
				if _, ok := def.getters[k]; !ok {
					def.getters[k] = v
				}
			}
			if def.setters == nil {
				def.setters = map[string]*ast.Node{}
			}
			for k, v := range bdef.setters {
				if _, ok := def.setters[k]; !ok {
					def.setters[k] = v
				}
			}
			// 静态字面量随继承下沉（子类未覆写则继承；形状证据：封存 inheritClass statics 拷贝）。
			if def.statics == nil {
				def.statics = map[string]saStaticVal{}
			}
			for k, v := range bdef.statics {
				if _, ok := def.statics[k]; !ok {
					def.statics[k] = v
				}
			}
			def.parent = base
		}
	}
	if ast.HasModifier(st, ast.ModifierFlagsAbstract) {
		def.isAbstract = true
	}
	// 自有域起点为基布局末端（按基域种宽；无基则 0）。
	off := 0
	for _, f := range def.fields {
		fk := def.fkinds[f.name]
		if fk == "" {
			fk = "i32"
		}
		sz, _ := saFieldWidth(fk)
		if end := f.offset + sz; end > off {
			off = end
		}
	}
	for _, m := range members {
		if len(m.Decorators()) > 0 {
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "member decorators are not lowerable"})
			return false
		}
		switch m.Kind {
		case ast.KindPropertyDeclaration:
			pd := m.AsPropertyDeclaration()
			fn := m.Name()
			if fn == nil || fn.Kind != ast.KindIdentifier {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed/private field names are not lowerable"})
				return false
			}
			if ast.HasModifier(m, ast.ModifierFlagsStatic) {
				// 静态字面量折叠记表（不占实例槽；封存 recordClassNamed:9703-9711）；
				// 非字面静态走 legacy 实例槽（封存 s2 形；i32 恒 4 字节，见 step48）。
				if text, kind, ok := saStaticLiteral(pd.Initializer); ok {
					if def.statics == nil {
						def.statics = map[string]saStaticVal{}
					}
					def.statics[fn.Text()] = saStaticVal{text: text, kind: kind}
					continue
				}
			}
			// 字段初值表达式忽略（布局只记槽位，不求值；封存 recordClassNamed
			// 9691-9743 不读 Initializer；初值语义随 alloc，见 AGENTS step47）。
			// 字段种：i32/bool 恒 4 字节槽，string 为头指针 8 字节槽
			// （封存 widthOf；其余宽度无槽）。
			fkind := "i32"
			if pd.Type != nil {
				if k, ok := saAnnotKind(pd.Type); !ok || (k != "i32" && k != "bool" && k != "str") {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32 or string"})
					return false
				} else if k == "str" {
					fkind = "str"
				}
			}
			if _, dup := def.offsets[fn.Text()]; dup {
				// 继承字段重声明：守基偏移（同宽同种恒成立）。
				// 形状证据：封存 recordClassNamed:9717-9733。
				if def.parent == "" || ownFields[fn.Text()] {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate field " + fn.Text()})
					return false
				}
				continue
			}
			ownFields[fn.Text()] = true
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fn.Text(), offset: off})
			def.offsets[fn.Text()] = off
			def.fkinds[fn.Text()] = fkind
			sz, _ := saFieldWidth(fkind)
			off += sz
		case ast.KindConstructor:
			if def.ctor != nil {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate constructor"})
				return false
			}
			def.ctor = m
		case ast.KindMethodDeclaration:
			mn := m.Name()
			if mn == nil || mn.Kind != ast.KindIdentifier {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed/private method names are not lowerable"})
				return false
			}
			if ast.HasModifier(m, ast.ModifierFlagsStatic) {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "static class members are not lowerable"})
				return false
			}
			if _, dup := def.methods[mn.Text()]; dup && ownMethods[mn.Text()] {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate method " + mn.Text()})
				return false
			}
			// 覆写语义：子类同名直接覆盖继承方法（形状证据：封存 inheritClass:139-149）。
			ownMethods[mn.Text()] = true
			def.methods[mn.Text()] = m
		case ast.KindGetAccessor, ast.KindSetAccessor:
			// 存取器以内联体记录（零参 get/一参 set；静态/计算名/字段重名拒；
			// 形状证据：封存 recordClassNamed:9774-9818）。
			an := m.Name()
			if an == nil || an.Kind != ast.KindIdentifier {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed/private accessor names are not lowerable"})
				return false
			}
			if ast.HasModifier(m, ast.ModifierFlagsStatic) {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "static class members are not lowerable"})
				return false
			}
			if _, dup := def.offsets[an.Text()]; dup {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "member name clash " + an.Text()})
				return false
			}
			if m.Kind == ast.KindGetAccessor {
				if _, dup := def.getters[an.Text()]; dup && ownMethods[an.Text()] {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate getter " + an.Text()})
					return false
				}
				ownMethods[an.Text()] = true
				def.getters[an.Text()] = m
			} else {
				if _, dup := def.setters[an.Text()]; dup && ownMethods[an.Text()] {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate setter " + an.Text()})
					return false
				}
				ownMethods[an.Text()] = true
				def.setters[an.Text()] = m
			}
		case ast.KindSemicolonClassElement:
		default:
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class member is not lowerable (indexers refused)"})
			return false
		}
	}
	// 默认派生构造：无 ctor 的子类继承基 ctor 节点（ctorOwner 指向基；
	// 形状证据：封存 recordClassNamed:9819-9828）。
	if def.ctor != nil {
		def.ctorOwner = name
	} else if def.parent != "" {
		if bdef, ok := classes[def.parent]; ok {
			def.ctor = bdef.ctor
			def.ctorOwner = def.parent
		}
	}
	// 派生类自有构造须调 super()（无 super 基域悄零，TS 规则；形状证据：
	// 封存 recordClassNamed:9829-9836）。检查提前到声明期。
	if def.ctor != nil && def.ctorOwner == name && def.parent != "" {
		if _, ok := classes[def.parent]; ok && !saCtorCallsSuper(def.ctor) {
			ln, col := pos(st.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + name + " must call super() (derived constructors delegate to the base)"})
			return false
		}
	}
	def.size = off
	classes[name] = def
	// 自身具名（`const D = class E`）记同体别名（值对；封存 inner 名泄漏 gap
	// 即此语义；别名冲突诚实拒）。
	if forceName != "" {
		if own := st.Name(); own != nil && own.Kind == ast.KindIdentifier && own.Text() != name {
			if _, dup := classes[own.Text()]; dup {
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate class " + own.Text()})
				return false
			}
			classes[own.Text()] = def
		}
	}
	return true
}

// saCtorCallsSuper 报告构造体是否含顶层 `super(...)`（嵌套函数/类边界
// 拥有各自 super，箭头继承外层；形状证据：封存 ctorCallsSuper:280-317）。
func saCtorCallsSuper(ctor *ast.Node) bool {
	body := ctor.Body()
	if body == nil {
		return false
	}
	found := false
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if found || n == nil {
			return
		}
		switch n.Kind {
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression,
			ast.KindClassDeclaration, ast.KindClassExpression:
			return
		case ast.KindCallExpression:
			call := n.AsCallExpression()
			if call.Expression != nil && call.Expression.Kind == ast.KindSuperKeyword {
				found = true
				return
			}
		}
		n.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	for _, s := range body.Statements() {
		walk(s)
		if found {
			break
		}
	}
	return found
}

// saCouldBeInst 判定表达式是否可能为实例基（绑定实例名或方法内 this）。
func saCouldBeInst(e *ast.Node, scope *saScope) bool {
	if e != nil && e.Kind == ast.KindThisKeyword {
		return scope.thisSelf != ""
	}
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok {
			return len(k) > 5 && k[:5] == "inst:"
		}
	}
	return false
}

// saRecordIface 记录接口布局（i32 字段；无码。形状证据：封存
// lowerTypeDecl 的布局记录一半；方法/索引签名大声拒）。
func saRecordIface(st *ast.Node, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) bool {
	decl := st.AsInterfaceDeclaration()
	nm := st.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "anonymous interfaces are not lowerable"})
		return false
	}
	name := nm.Text()
	if _, dup := classes[name]; dup {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate type " + name})
		return false
	}
	def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true}
	off := 0
	ownIface := map[string]bool{}
	// 接口 extends 基展平（类型级；未知基尽力跳过，checker 拥有类型错；
	// 形状证据：封存 inheritInterfaceLayout:319-376）。
	if hc := decl.HeritageClauses; hc != nil {
		for _, h := range hc.Nodes {
			if h.Kind != ast.KindHeritageClause || h.AsHeritageClause().Token != ast.KindExtendsKeyword {
				continue
			}
			for _, el := range h.AsHeritageClause().Types.Nodes {
				iname := ""
				switch el.Kind {
				case ast.KindExpressionWithTypeArguments:
					if ex := el.AsExpressionWithTypeArguments().Expression; ex != nil && ex.Kind == ast.KindIdentifier {
						iname = ex.Text()
					}
				case ast.KindTypeReference:
					if tn := el.AsTypeReferenceNode(); tn != nil && tn.TypeName != nil {
						iname = tn.TypeName.Text()
					}
				case ast.KindIdentifier:
					iname = el.Text()
				}
				if iname == "" {
					continue
				}
				base, ok := classes[iname]
				if !ok || base == nil || !base.isIface {
					continue
				}
				for _, f := range base.fields {
					if _, dup := def.offsets[f.name]; dup {
						continue
					}
					fk := base.fkinds[f.name]
					if fk == "" {
						fk = "i32"
					}
					off = saAlignOff(off, fk)
					def.fields = append(def.fields, saClassField{name: f.name, offset: off})
					def.offsets[f.name] = off
					def.fkinds[f.name] = fk
					sz, _ := saFieldWidth(fk)
					off += sz
				}
			}
		}
	}
	for _, m := range decl.Members.Nodes {
		if m.Kind != ast.KindPropertySignature {
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "interface member is not lowerable (i32/str props only)"})
			return false
		}
		fn := m.Name()
		if fn == nil || fn.Kind != ast.KindIdentifier {
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed interface field names are not lowerable"})
			return false
		}
		pd := m.AsPropertySignatureDeclaration()
		fkind := "i32"
		if pd.Type != nil {
			// i32/bool 恒 4 字节槽（封存 saNameOfType boolean→i32 同形）；
			// string 为头指针 8 字节槽；余下（数组/嵌套/多联合）无槽，拒。
			if k, ok := saAnnotKind(pd.Type); !ok || (k != "i32" && k != "bool" && k != "str") {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "interface fields must be i32 or string"})
				return false
			} else if k == "str" {
				fkind = "str"
			}
		}
		if _, dup := def.offsets[fn.Text()]; dup {
			// 基展平字段重声明：守基偏移（形状证据同类分支）。
			if ownIface[fn.Text()] {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate field " + fn.Text()})
				return false
			}
			continue
		}
		ownIface[fn.Text()] = true
		off = saAlignOff(off, fkind)
		def.fields = append(def.fields, saClassField{name: fn.Text(), offset: off})
		def.offsets[fn.Text()] = off
		def.fkinds[fn.Text()] = fkind
		sz, _ := saFieldWidth(fkind)
		off += sz
	}
	def.size = off
	classes[name] = def
	return true
}

// saMatchIface 按键集匹配唯一接口布局（0 或 2+ 匹配皆大声拒，确定性优先；
// 形状证据：封存 layoutOfLiteral:8953-8975 + matchLayout 名集匹配）。
func saMatchIface(keys []string, classes map[string]*saClassDef) (*saClassDef, string) {
	var hit *saClassDef
	for _, def := range classes {
		if !def.isIface || len(def.fields) != len(keys) {
			continue
		}
		ok := true
		for _, k := range keys {
			if _, has := def.offsets[k]; !has {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if hit != nil {
			return nil, "object literal matches no unique recorded interface layout (declare the interface first)"
		}
		hit = def
	}
	if hit == nil {
		return nil, "object literal matches no recorded interface layout (declare the interface first)"
	}
	return hit, ""
}

// saObjPropName 解析字面量键（标识符/串字面量/字面计算键；简写由调用方展值。
// 形状证据：封存 objPropName:8984-9007）。
func saObjPropName(p *ast.Node) (string, bool) {
	nm := p.Name()
	if nm == nil {
		return "", false
	}
	switch nm.Kind {
	case ast.KindIdentifier:
		return nm.Text(), true
	case ast.KindStringLiteral:
		t := nm.Text()
		if len(t) >= 2 && t[0] == '"' {
			if unq, err := strconv.Unquote(t); err == nil {
				return unq, true
			}
			return "", false
		}
		return t, true
	case ast.KindComputedPropertyName:
		expr := nm.AsComputedPropertyName().Expression
		if expr == nil {
			return "", false
		}
		if expr.Kind == ast.KindStringLiteral {
			t := expr.Text()
			if len(t) >= 2 && t[0] == '"' {
				if unq, err := strconv.Unquote(t); err == nil {
					return unq, true
				}
				return "", false
			}
			return t, true
		}
		if expr.Kind == ast.KindNumericLiteral {
			return expr.Text(), true
		}
	}
	return "", false
}

// saStaticLiteral 折叠静态字面量初值（断言/括号剥离后：数字面→i32 文本，
// 串/反引号字面→原文（具化路径与字面量同字节），true/false→1/0；
// 形状证据：封存 staticLiteralText:9444-9468）。
func saStaticLiteral(n *ast.Node) (string, string, bool) {
	for n != nil {
		switch n.Kind {
		case ast.KindAsExpression:
			n = n.AsAsExpression().Expression
			continue
		case ast.KindSatisfiesExpression:
			n = n.AsSatisfiesExpression().Expression
			continue
		case ast.KindNonNullExpression:
			n = n.AsNonNullExpression().Expression
			continue
		case ast.KindParenthesizedExpression:
			n = n.AsParenthesizedExpression().Expression
			continue
		case ast.KindTypeAssertionExpression:
			n = n.AsTypeAssertion().Expression
			continue
		}
		break
	}
	if n == nil {
		return "", "", false
	}
	switch n.Kind {
	case ast.KindNumericLiteral:
		return n.Text(), "i32", true
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return n.Text(), "str", true
	case ast.KindTrueKeyword:
		return "1", "bool", true
	case ast.KindFalseKeyword:
		return "0", "bool", true
	}
	return "", "", false
}

// saIsStrFieldRead 纯判定属性读是否为 str 位（静态串折叠/实例 str 域/
// str getter；不落字，供串位语法门；求值见 saEvalStr 属性分支）。
func saIsStrFieldRead(pa *ast.PropertyAccessExpression, scope *saScope) bool {
	if pa == nil || pa.Name() == nil {
		return false
	}
	field := pa.Name().Text()
	if strings.HasPrefix(field, "#") {
		return false
	}
	atClass := func(d *saClassDef) bool {
		if d.fkinds[field] == "str" {
			return true
		}
		if gn, ok := d.getters[field]; ok {
			if k, ok := saMethodReturnKind(gn); ok && k == "str" {
				return true
			}
		}
		return false
	}
	base := pa.Expression
	if base != nil && base.Kind == ast.KindIdentifier {
		nm := base.Text()
		if d, ok := scope.classes[nm]; ok {
			if sv, ok := d.statics[field]; ok {
				return sv.kind == "str"
			}
			return false
		}
		if k, ok := scope.types[nm]; ok && len(k) > 5 && k[:5] == "inst:" {
			if d, ok := scope.classes[k[5:]]; ok {
				return atClass(d)
			}
		}
		return false
	}
	if base != nil && base.Kind == ast.KindThisKeyword && scope.thisSelf != "" {
		if d, ok := scope.classes[scope.thisClass]; ok {
			return atClass(d)
		}
	}
	return false
}

// saLookupMethod 按基解方法节点（实例/`this`；含继承展平；静态/未知即失败）。
func saLookupMethod(base *ast.Node, method string, scope *saScope) (*ast.Node, bool) {
	if method == "" || strings.HasPrefix(method, "#") {
		return nil, false
	}
	var def *saClassDef
	if base != nil && base.Kind == ast.KindIdentifier {
		nm := base.Text()
		if _, ok := scope.classes[nm]; ok {
			// 类名基即静态调用，静态方法拒，故无串返回。
			return nil, false
		}
		if k, ok := scope.types[nm]; ok && len(k) > 5 && k[:5] == "inst:" {
			d, ok := scope.classes[k[5:]]
			if !ok {
				return nil, false
			}
			def = d
		} else {
			return nil, false
		}
	} else if base != nil && base.Kind == ast.KindThisKeyword && scope.thisSelf != "" {
		d, ok := scope.classes[scope.thisClass]
		if !ok {
			return nil, false
		}
		def = d
	} else {
		return nil, false
	}
	mn, ok := def.methods[method]
	if !ok || mn == nil {
		return nil, false
	}
	return mn, true
}

// saMethodReturnKind 返回方法/getter 的声明返回种（无注解/非法即失败；
/// 供串位调用识别，`c.get(): string` 即串值）。
func saMethodReturnKind(mn *ast.Node) (string, bool) {
	if mn == nil {
		return "", false
	}
	var typ *ast.TypeNode
	switch mn.Kind {
	case ast.KindMethodDeclaration:
		typ = mn.AsMethodDeclaration().Type
	case ast.KindGetAccessor:
		typ = mn.AsGetAccessorDeclaration().Type
	default:
		return "", false
	}
	if typ == nil {
		return "", false
	}
	return saAnnotKind(typ)
}

// saStaticFold 读静态字面量（类名/实例/`this` 基；私有名不碰；
// 串具化回句柄；i32/bool 直接文本；形状证据：封存 lowerExpr:8013-8041）。
func saStaticFold(w printer.EmitTextWriter, base *ast.Node, field string, scope *saScope, nextTemp *int) (string, string, bool) {
	if field == "" || strings.HasPrefix(field, "#") {
		return "", "", false
	}
	var def *saClassDef
	if base != nil && base.Kind == ast.KindIdentifier {
		nm := base.Text()
		if d, ok := scope.classes[nm]; ok {
			def = d
		} else if k, ok := scope.types[nm]; ok && len(k) > 5 && k[:5] == "inst:" {
			d, ok := scope.classes[k[5:]]
			if !ok {
				return "", "", false
			}
			def = d
		} else {
			return "", "", false
		}
	} else if base != nil && base.Kind == ast.KindThisKeyword && scope.thisSelf != "" {
		d, ok := scope.classes[scope.thisClass]
		if !ok {
			return "", "", false
		}
		def = d
	} else {
		return "", "", false
	}
	sv, ok := def.statics[field]
	if !ok {
		return "", "", false
	}
	if sv.kind == "str" {
		return saLowerStringLiteral(w, sv.text, scope, nextTemp), "str", true
	}
	return sv.text, sv.kind, true
}

// saLowerObjectLiteral 具化结构体（alloc 布局 + 逐域 store；i32 值；
// 简写读绑定；字面计算键折叠；spread 按源序布局复制（后 prop 覆盖先生效）。
// want 非空时须命中该接口。
// 形状证据：封存 lowerObjectLiteral:9009+（spread 相 9026-9044 + 键集去重
// 9066-9076 + 源序重放 9086-9103；零初始化在薄口省略：目标每域必有来源）。
func saLowerObjectLiteral(w printer.EmitTextWriter, n *ast.Node, want string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	ol := n.AsObjectLiteralExpression()
	type spreadSrc struct {
		h   string
		def *saClassDef
	}
	type op struct {
		spread int // spreads 下标，-1 为普通 store
		fname  string
		init   *ast.Node
	}
	var spreads []spreadSrc
	var ops []op
	var keys []string
	for _, p := range ol.Properties.Nodes {
		if p.Kind == ast.KindSpreadAssignment {
			se := p.AsSpreadAssignment().Expression.AsNode()
			var sh string
			var sdef *saClassDef
			switch {
			case se.Kind == ast.KindIdentifier:
				nm := se.Text()
				k, ok := scope.types[nm]
				if !ok {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				if len(k) <= 5 || k[:5] != "inst:" {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				d, ok := scope.classes[k[5:]]
				if !ok {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				sh, sdef = nm, d
			case se.Kind == ast.KindThisKeyword && scope.thisSelf != "":
				d, ok := scope.classes[scope.thisClass]
				if !ok {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				sh, sdef = scope.thisSelf, d
			case se.Kind == ast.KindObjectLiteralExpression:
				h, lname, msg := saLowerObjectLiteral(w, se, "", scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				d, ok := scope.classes[lname]
				if !ok {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				sh, sdef = h, d
			default:
				return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
			}
			spreads = append(spreads, spreadSrc{h: sh, def: sdef})
			ops = append(ops, op{spread: len(spreads) - 1})
			for _, f := range sdef.fields {
				keys = append(keys, f.name)
			}
			continue
		}
		switch p.Kind {
		case ast.KindPropertyAssignment:
			fname, ok := saObjPropName(p)
			if !ok {
				return "", "", "computed property names must be literals (dynamic keys have no static layout)"
			}
			ops = append(ops, op{spread: -1, fname: fname, init: p.AsPropertyAssignment().Initializer})
			keys = append(keys, fname)
		case ast.KindShorthandPropertyAssignment:
			fname, ok := saObjPropName(p)
			if !ok {
				return "", "", "computed property names must be literals (dynamic keys have no static layout)"
			}
			ops = append(ops, op{spread: -1, fname: fname, init: p.Name()})
			keys = append(keys, fname)
		default:
			return "", "", "object literal property is not lowerable (methods refused)"
		}
	}
	// 键集去重后匹配（覆盖不增域；封存 9066-9076）。
	seen := map[string]bool{}
	var uniq []string
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, k)
		}
	}
	def, msg := saMatchIface(uniq, scope.classes)
	if msg != "" {
		return "", "", msg
	}
	if want != "" && def.name != want {
		return "", "", "object literal does not match interface " + want
	}
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if def.size == 0 {
		w.Write(fmt.Sprintf("  %s = alloc 4\n", h))
	} else {
		w.Write(fmt.Sprintf("  %s = alloc %d\n", h, def.size))
	}
	for _, o := range ops {
		if o.spread >= 0 {
			// spread 逐域复制（源序；后者覆盖前者；种错配拒，封存 9095 原文）。
			src := spreads[o.spread]
			for _, f := range src.def.fields {
				sk := src.def.fkinds[f.name]
				if sk == "" {
					sk = "i32"
				}
				dk := def.fkinds[f.name]
				if dk == "" {
					dk = "i32"
				}
				if sk != dk {
					return "", "", fmt.Sprintf("spread field %s type mismatch (%s vs %s)", f.name, sk, dk)
				}
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				if sk == "str" {
					w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, src.h, src.def.offsets[f.name]))
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[f.name], t))
					continue
				}
				w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", t, src.h, src.def.offsets[f.name]))
				w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, def.offsets[f.name], t))
			}
			continue
		}
		if fk := def.fkinds[o.fname]; fk == "str" {
			// str 域具化存头指针（封存 9109 `store ... as <type>` 同形）。
			v, msg := saEvalStr(w, o.init, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], v))
			continue
		}
		v, msg := saEvalI32(w, o.init, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, def.offsets[o.fname], v))
	}
	return h, def.name, ""
}

func saInstBase(e *ast.Node, scope *saScope) (string, *saClassDef, string) {
	if e != nil && e.Kind == ast.KindThisKeyword {
		if scope.thisSelf == "" {
			return "", nil, "this outside a class method is not lowerable"
		}
		def, ok := scope.classes[scope.thisClass]
		if !ok {
			return "", nil, "this outside a class method is not lowerable"
		}
		return scope.thisSelf, def, ""
	}
	if e != nil && e.Kind == ast.KindIdentifier {
		nm := e.Text()
		if k, ok := scope.types[nm]; ok {
			if len(k) > 5 && k[:5] == "inst:" {
				def, ok := scope.classes[k[5:]]
				if !ok {
					return "", nil, "unknown class " + k[5:]
				}
				return nm, def, ""
			}
		}
		return "", nil, ""
	}
	return "", nil, ""
}

// saSuperBase 解析方法内 super 基（同接收者；形状证据：封存
// superBaseForRecv:219-232 + lowerSuperMethodCall:237-250）。
func saSuperBase(scope *saScope) (*saClassDef, string, string) {
	if scope.thisSelf == "" || scope.thisClass == "" {
		return nil, "", "super calls are only lowerable inside a subclass method"
	}
	def, ok := scope.classes[scope.thisClass]
	if !ok || def.parent == "" {
		return nil, "", "super calls are only lowerable inside a subclass method"
	}
	bdef, ok := scope.classes[def.parent]
	if !ok {
		return nil, "", "unknown base class " + def.parent
	}
	return bdef, scope.thisSelf, ""
}

// saLowerNewClass 具化 `new C(...)`（布局 alloc + 构造 wiring；
// 形状证据：封存 lowerNewClass:9899-9962 + wireCtorStatement:9968-10001）。
func saLowerNewClass(w printer.EmitTextWriter, name string, ce *ast.NewExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	def, ok := scope.classes[name]
	if !ok {
		return "", "unknown class " + name
	}
	if def.isIface {
		return "", "interfaces cannot be instantiated (declare a class)"
	}
	if def.isAbstract {
		return "", "abstract class " + name + " cannot be instantiated (declare a concrete subclass)"
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if def.size == 0 {
		w.Write(fmt.Sprintf("  %s = alloc 4\n", h))
	} else {
		w.Write(fmt.Sprintf("  %s = alloc %d\n", h, def.size))
	}
	if def.ctor == nil {
		if len(argNodes) != 0 {
			return "", "new " + name + " takes 0 arguments"
		}
		return h, ""
	}
	params := def.ctor.Parameters()
	if len(argNodes) != len(params) {
		return "", fmt.Sprintf("new %s takes %d arguments (%d given)", name, len(params), len(argNodes))
	}
	owner := name
	if def.ctorOwner != "" {
		owner = def.ctorOwner
	}
	// wiring 目标种预扫（`this.f = param` 的 f 种决定实参求值器；str 域走串求值）。
	wantStr := saCtorWiringKinds(def.ctor, owner, scope)
	paramVal := map[string]string{}
	for i, p := range params {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			return "", "destructured constructor parameters are not lowerable"
		}
		if pd.DotDotDotToken != nil || pd.QuestionToken != nil || pd.Initializer != nil {
			return "", "constructor parameter shape is not lowerable"
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			return "", "destructured constructor parameters are not lowerable"
		}
		a := argNodes[i]
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			return "", "constructor arguments must be values"
		}
		if wantStr[nm.Text()] {
			v, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			paramVal[nm.Text()] = v
			continue
		}
		v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		paramVal[nm.Text()] = v
	}
	body := def.ctor.Body()
	if body == nil {
		return h, ""
	}
	if !saWireCtorBody(w, h, owner, def.ctor, paramVal, scope, pos, refusals, nextTemp) {
		return "", "unwirable"
	}
	return h, ""
}

// saCtorWiringKinds 预扫构造体 `this.f = param` 的 str 目标（返回 param 名集；
// 非 wiring 语句由 wire 主路拒，此处只收形状完整的；`super(...)` 转发按基构造
// 同例递归解（环由记录期继承圈门保证无环，另加深度守卫）。
func saCtorWiringKinds(ctor *ast.Node, owner string, scope *saScope) map[string]bool {
	return saCtorWiringKindsDepth(ctor, owner, scope, 0)
}

func saCtorWiringKindsDepth(ctor *ast.Node, owner string, scope *saScope, depth int) map[string]bool {
	want := map[string]bool{}
	if depth > 8 {
		return want
	}
	body := ctor.Body()
	if body == nil {
		return want
	}
	def, ok := scope.classes[owner]
	if !ok {
		return want
	}
	for _, s := range body.Statements() {
		if !saIsSuperCall(s) {
			continue
		}
		// super 转发：基形参 str 位透传给外层同名实参。
		bdef, ok := scope.classes[def.parent]
		if !ok || def.parent == "" || bdef.ctor == nil {
			continue
		}
		call := s.AsExpressionStatement().Expression.AsCallExpression()
		var argNodes []*ast.Node
		if call.Arguments != nil {
			argNodes = call.Arguments.Nodes
		}
		bparams := bdef.ctor.Parameters()
		if len(argNodes) != len(bparams) {
			continue
		}
		bwant := saCtorWiringKindsDepth(bdef.ctor, def.parent, scope, depth+1)
		for i, p := range bparams {
			pd := p.AsParameterDeclaration()
			if pd == nil {
				continue
			}
			nm := pd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			if !bwant[nm.Text()] {
				continue
			}
			if argNodes[i] != nil && argNodes[i].Kind == ast.KindIdentifier {
				want[argNodes[i].Text()] = true
			}
		}
	}
	for _, s := range body.Statements() {
		if saIsSuperCall(s) {
			continue
		}
		if s == nil || s.Kind != ast.KindExpressionStatement {
			continue
		}
		ex := s.AsExpressionStatement().Expression
		if ex == nil || ex.Kind != ast.KindBinaryExpression {
			continue
		}
		bin := ex.AsBinaryExpression()
		if bin.OperatorToken == nil || bin.OperatorToken.Kind != ast.KindEqualsToken ||
			bin.Left == nil || bin.Left.Kind != ast.KindPropertyAccessExpression {
			continue
		}
		pa := bin.Left.AsPropertyAccessExpression()
		if pa.Expression == nil || pa.Expression.Kind != ast.KindThisKeyword || pa.Name() == nil {
			continue
		}
		if bin.Right == nil || bin.Right.Kind != ast.KindIdentifier {
			continue
		}
		if def.fkinds[pa.Name().Text()] == "str" {
			want[bin.Right.Text()] = true
		}
	}
	return want
}

// saWireCtorBody 解释构造体语句（`super(...)` 委托基 wiring + `this.f = param`；
// 形状证据：封存 wireCtorBody:10075-10147 + wireSuperCtorStatement:383-472）。
func saWireCtorBody(w printer.EmitTextWriter, h, owner string, ctor *ast.Node, paramVal map[string]string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	body := ctor.Body()
	if body == nil {
		return true
	}
	for _, s := range body.Statements() {
		if saIsSuperCall(s) {
			if !saWireSuperCtor(w, h, owner, s, paramVal, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		// 仅 this.f = param  wiring（余下语句大声拒，镜像 wireCtorStatement）。
		if s.Kind != ast.KindExpressionStatement {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
			return false
		}
		ex := s.AsExpressionStatement().Expression
		if ex == nil || ex.Kind != ast.KindBinaryExpression {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
			return false
		}
		bin := ex.AsBinaryExpression()
		if bin.OperatorToken == nil || bin.OperatorToken.Kind != ast.KindEqualsToken ||
			bin.Left == nil || bin.Left.Kind != ast.KindPropertyAccessExpression {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
			return false
		}
		pa := bin.Left.AsPropertyAccessExpression()
		if pa.Expression == nil || pa.Expression.Kind != ast.KindThisKeyword || pa.Name() == nil {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
			return false
		}
		def, ok := scope.classes[owner]
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + owner})
			return false
		}
		off, ok := def.offsets[pa.Name().Text()]
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + pa.Name().Text() + " is not in the " + owner + " layout"})
			return false
		}
		if bin.Right == nil || bin.Right.Kind != ast.KindIdentifier {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor wiring right side must be a parameter name"})
			return false
		}
		v, ok := paramVal[bin.Right.Text()]
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor parameter " + bin.Right.Text() + " has no value"})
			return false
		}
		// str 域存头指针（右值已按种求值）。
		if def.fkinds[pa.Name().Text()] == "str" {
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
			continue
		}
		w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
	}
	return true
}

// saIsSuperCallStatement 识别顶层 `super(...)` 语句（形状证据：封存 isSuperCallStatement）。
func saIsSuperCall(s *ast.Node) bool {
	if s == nil || s.Kind != ast.KindExpressionStatement {
		return false
	}
	ex := s.AsExpressionStatement().Expression
	if ex == nil || ex.Kind != ast.KindCallExpression {
		return false
	}
	call := ex.AsCallExpression()
	return call.Expression != nil && call.Expression.Kind == ast.KindSuperKeyword
}

// saWireSuperCtor 将 `super(a, ...)` 委托给基构造 wiring（同实例柄；
// 实参须为构造参数或字面量；形状证据：封存 wireSuperCtorStatement:383-472）。
func saWireSuperCtor(w printer.EmitTextWriter, h, owner string, s *ast.Node, outerVal map[string]string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	ownerDef, ok := scope.classes[owner]
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + owner})
		return false
	}
	base := ownerDef.parent
	if base == "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() is only lowerable inside a subclass constructor"})
		return false
	}
	bdef, ok := scope.classes[base]
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown base class " + base})
		return false
	}
	call := s.AsExpressionStatement().Expression.AsCallExpression()
	var argNodes []*ast.Node
	if call.Arguments != nil {
		argNodes = call.Arguments.Nodes
	}
	if bdef.ctor == nil {
		// 基无显式构造：逐个求值保序后丢弃（形状证据：封存 wireSuperCtorStatement:402-424）。
		for _, a := range argNodes {
			if a == nil {
				continue
			}
			switch a.Kind {
			case ast.KindArrowFunction, ast.KindFunctionExpression:
			case ast.KindIdentifier:
				if _, ok := outerVal[a.Text()]; !ok {
					ln, col := pos(a.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() arguments must be constructor parameters or literals"})
					return false
				}
			case ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword:
				if _, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp); msg != "" {
					ln, col := pos(a.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
			case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
				if _, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp); msg != "" {
					ln, col := pos(a.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
			default:
				ln, col := pos(a.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() arguments must be constructor parameters or literals"})
				return false
			}
		}
		return true
	}
	params := bdef.ctor.Parameters()
	if len(argNodes) != len(params) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() takes exact base constructor arguments"})
		return false
	}
	paramVal := map[string]string{}
	for i, p := range params {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructured constructor parameters are not lowerable"})
			return false
		}
		if pd.DotDotDotToken != nil || pd.QuestionToken != nil || pd.Initializer != nil {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor parameter shape is not lowerable"})
			return false
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructured constructor parameters are not lowerable"})
			return false
		}
		a := argNodes[i]
		if a == nil {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() arguments must be constructor parameters or literals"})
			return false
		}
		switch a.Kind {
		case ast.KindArrowFunction, ast.KindFunctionExpression:
			ln, col := pos(a.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() arguments must be constructor parameters or literals"})
			return false
		case ast.KindIdentifier:
			v, ok := outerVal[a.Text()]
			if !ok {
				ln, col := pos(a.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() arguments must be constructor parameters or literals"})
				return false
			}
			paramVal[nm.Text()] = v
		case ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword:
			v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(a.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			paramVal[nm.Text()] = v
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
			// str 字面量超参（基 str 形参位；值位门在基 wire）。
			v, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(a.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			paramVal[nm.Text()] = v
		default:
			ln, col := pos(a.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "super() arguments must be constructor parameters or literals"})
			return false
		}
	}
	return saWireCtorBody(w, h, base, bdef.ctor, paramVal, scope, pos, refusals, nextTemp)
}

// saInlineMethod 内联 `obj.m(args)`（形参快照 + this 指向 + 槽汇合；
// 形状证据：封存 inlineClassMethod:10188-10280；体复用 saCallbackValue）。
func saInlineMethod(w printer.EmitTextWriter, recv string, def *saClassDef, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	mn, ok := def.methods[method]
	if !ok {
		return "", "unknown method " + method
	}
	if mn.Body() == nil {
		return "", method + " has no body (overload signatures do not inline)"
	}
	params := mn.Parameters()
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != len(params) {
		return "", "method takes exact arguments"
	}
	// 方法形参恒 i32（回调快照绑定只存标量；句柄形参无改写机制，大声拒）。
	for _, p := range params {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			return "", "method parameter shape is not lowerable"
		}
		if pd.Type != nil {
			if k, ok := saAnnotKind(pd.Type); !ok || k != "i32" {
				return "", "method parameters must be i32"
			}
		}
	}
	var argVals []string
	for _, a := range argNodes {
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			return "", "method arguments must be values"
		}
		v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		argVals = append(argVals, v)
	}
	savedSelf, savedClass := scope.thisSelf, scope.thisClass
	scope.thisSelf, scope.thisClass = recv, def.name
	// 返回种按声明注解（str 走串槽；余下走 i32 槽）。
	wantKind := "i32"
	if k, ok := saMethodReturnKind(mn); ok && k == "str" {
		wantKind = "str"
	}
	v, msg := saCallbackValue(w, mn, argVals, true, wantKind, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.thisSelf, scope.thisClass = savedSelf, savedClass
	if msg != "" {
		return "", msg
	}
	return v, ""
}

// saInlineGetter 内联 `o.g` 读（零参体；形状证据：封存 lowerExpr:8130-8137）。
func saInlineGetter(w printer.EmitTextWriter, recv string, def *saClassDef, name string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	gn, ok := def.getters[name]
	if !ok {
		if _, ok := def.setters[name]; ok {
			return "", name + " is write-only (setter has no getter)"
		}
		return "", "unknown field " + name
	}
	if len(gn.Parameters()) != 0 {
		return "", "getter " + name + " takes 0 parameters"
	}
	savedSelf, savedClass := scope.thisSelf, scope.thisClass
	scope.thisSelf, scope.thisClass = recv, def.name
	wantKind := "i32"
	if k, ok := saMethodReturnKind(gn); ok && k == "str" {
		wantKind = "str"
	}
	v, msg := saCallbackValue(w, gn, nil, true, wantKind, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.thisSelf, scope.thisClass = savedSelf, savedClass
	if msg != "" {
		return "", msg
	}
	return v, ""
}

// saInlineSetter 内联 `o.s = v` 写（一参体；返回右值，镜像赋值折值语义）。
func saInlineSetter(w printer.EmitTextWriter, recv string, def *saClassDef, name, val string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) string {
	sn, ok := def.setters[name]
	if !ok {
		return "unknown field " + name
	}
	if len(sn.Parameters()) != 1 {
		return "setter " + name + " takes 1 parameter"
	}
	savedSelf, savedClass := scope.thisSelf, scope.thisClass
	scope.thisSelf, scope.thisClass = recv, def.name
	_, msg := saCallbackValue(w, sn, []string{val}, false, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.thisSelf, scope.thisClass = savedSelf, savedClass
	return msg
}
func saLowerClassFieldLoad(w printer.EmitTextWriter, h string, def *saClassDef, field string, scope *saScope, nextTemp *int) (string, string) {
	off, ok := def.offsets[field]
	if !ok {
		return "", "unknown field " + field
	}
	// str 域读回头指针即串值（16 字节头在堆上，值即其址；临时量记 str
	// 供下游串位；封存 lowerMemberChain:8294-8297 `load as <type>` 同形）。
	if def.fkinds[field] == "str" {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
		scope.types[t] = "str"
		return t, ""
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", t, h, off))
	return t, ""
}

// saLowerClassFieldStore 写 `o.f = v`（偏移 store；i32 存值，str 存头指针）。
func saLowerClassFieldStore(w printer.EmitTextWriter, h string, def *saClassDef, field, v string) string {
	off, ok := def.offsets[field]
	if !ok {
		return "unknown field " + field
	}
	if def.fkinds[field] == "str" {
		w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
		return ""
	}
	w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
	return ""
}
