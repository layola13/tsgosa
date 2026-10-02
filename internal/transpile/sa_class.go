// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"strconv"

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

// saClassDef 是类定义（字段表 + 构造 + 方法表；接口以 isIface 记，
// 方法/构造恒空，不可 new；parent 为单继承父名，空即无）。
type saClassDef struct {
	name       string
	fields     []saClassField
	offsets    map[string]int
	size       int
	methods    map[string]*ast.Node
	getters    map[string]*ast.Node
	setters    map[string]*ast.Node
	ctor       *ast.Node
	ctorOwner  string
	parent     string
	isIface    bool
	isAbstract bool
}

// saRecordClass 记录类定义（布局 + 构造 + 方法；无码。重复类名/非法成员拒）。
func saRecordClass(st *ast.Node, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if st.Kind != ast.KindClassDeclaration {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "only class declarations lowerable"})
		return false
	}
	cd := st.AsClassDeclaration()
	nm := st.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "anonymous classes are not lowerable"})
		return false
	}
	name := nm.Text()
	if _, dup := classes[name]; dup {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate class " + name})
		return false
	}
	def := &saClassDef{name: name, offsets: map[string]int{}, methods: map[string]*ast.Node{}, getters: map[string]*ast.Node{}, setters: map[string]*ast.Node{}}
	ownFields := map[string]bool{}
	ownMethods := map[string]bool{}
	// 单继承：基布局字段追加在下（父偏移守恒），方法按名拷贝（子类覆写），
	// implements 擦除；多 extends/动态基/未知基/环一律拒。
	// 形状证据：封存 parseHeritage:42-83 + inheritClass:88-215。
	if hc := cd.HeritageClauses; hc != nil {
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
			def.parent = base
		}
	}
	if ast.HasModifier(st, ast.ModifierFlagsAbstract) {
		def.isAbstract = true
	}
	off := len(def.fields) * 4
	for _, m := range cd.Members.Nodes {
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
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "static class members are not lowerable"})
				return false
			}
			if pd.Initializer != nil {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field initializers are not lowerable (wire in constructor)"})
				return false
			}
			// 字段恒 i32（number/i32 注解；其余宽度无槽）。
			if pd.Type != nil {
				if k, ok := saAnnotKind(pd.Type); !ok || k != "i32" {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32"})
					return false
				}
			}
			if _, dup := def.offsets[fn.Text()]; dup {
				// 继承字段重声明：守基偏移（同宽恒成立，i32 薄口）。
				// 形状证据：封存 recordClassNamed:9717-9733。
				if def.parent == "" || ownFields[fn.Text()] {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate field " + fn.Text()})
					return false
				}
				continue
			}
			ownFields[fn.Text()] = true
			def.fields = append(def.fields, saClassField{name: fn.Text(), offset: off})
			def.offsets[fn.Text()] = off
			off += 4
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
	def := &saClassDef{name: name, offsets: map[string]int{}, methods: map[string]*ast.Node{}, isIface: true}
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
					def.fields = append(def.fields, saClassField{name: f.name, offset: off})
					def.offsets[f.name] = off
					off += 4
				}
			}
		}
	}
	for _, m := range decl.Members.Nodes {
		if m.Kind != ast.KindPropertySignature {
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "interface member is not lowerable (i32 props only)"})
			return false
		}
		fn := m.Name()
		if fn == nil || fn.Kind != ast.KindIdentifier {
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed interface field names are not lowerable"})
			return false
		}
		pd := m.AsPropertySignatureDeclaration()
		if pd.Type != nil {
			if k, ok := saAnnotKind(pd.Type); !ok || k != "i32" {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "interface fields must be i32"})
				return false
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
		def.fields = append(def.fields, saClassField{name: fn.Text(), offset: off})
		def.offsets[fn.Text()] = off
		off += 4
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

// saLowerObjectLiteral 具化结构体（alloc 布局 + 逐域 store；i32 值；
// 简写读绑定；spread/方法/动态键拒；want 非空时须命中该接口。
// 形状证据：封存 lowerObjectLiteral:9009+）。
func saLowerObjectLiteral(w printer.EmitTextWriter, n *ast.Node, want string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	ol := n.AsObjectLiteralExpression()
	type op struct {
		fname string
		init  *ast.Node
	}
	var ops []op
	var keys []string
	for _, p := range ol.Properties.Nodes {
		switch p.Kind {
		case ast.KindPropertyAssignment:
			fname, ok := saObjPropName(p)
			if !ok {
				return "", "", "computed property names must be literals (dynamic keys have no static layout)"
			}
			ops = append(ops, op{fname: fname, init: p.AsPropertyAssignment().Initializer})
			keys = append(keys, fname)
		case ast.KindShorthandPropertyAssignment:
			fname, ok := saObjPropName(p)
			if !ok {
				return "", "", "computed property names must be literals (dynamic keys have no static layout)"
			}
			ops = append(ops, op{fname: fname, init: p.Name()})
			keys = append(keys, fname)
		default:
			return "", "", "object literal property is not lowerable (spread/methods refused)"
		}
	}
	def, msg := saMatchIface(keys, scope.classes)
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
	owner := name
	if def.ctorOwner != "" {
		owner = def.ctorOwner
	}
	if !saWireCtorBody(w, h, owner, def.ctor, paramVal, scope, pos, refusals, nextTemp) {
		return "", "unwirable"
	}
	return h, ""
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
	v, msg := saCallbackValue(w, mn, argVals, true, scope, pos, refusals, needImport, nextLabel, nextTemp)
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
	v, msg := saCallbackValue(w, gn, nil, true, scope, pos, refusals, needImport, nextLabel, nextTemp)
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
	_, msg := saCallbackValue(w, sn, []string{val}, false, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.thisSelf, scope.thisClass = savedSelf, savedClass
	return msg
}
func saLowerClassFieldLoad(w printer.EmitTextWriter, h string, def *saClassDef, field string, nextTemp *int) (string, string) {
	off, ok := def.offsets[field]
	if !ok {
		return "", "unknown field " + field
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", t, h, off))
	return t, ""
}

// saLowerClassFieldStore 写 `o.f = v`（偏移 store）。
func saLowerClassFieldStore(w printer.EmitTextWriter, h string, def *saClassDef, field, v string) string {
	off, ok := def.offsets[field]
	if !ok {
		return "unknown field " + field
	}
	w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
	return ""
}
