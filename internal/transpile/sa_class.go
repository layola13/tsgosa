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
// 本薄口子集：单类、i32/str 字段（number/i32 注解 4 字节槽，string 8 字节头指针）、
// 构造 wiring（含参数属性 `constructor(private x: i32)` 按上游 RuntimeSyntax
// 合成字段 + super() 后注入 `this.p = p`，显式 wiring 优先）、
// 方法内联（i32 形参/体）；继承/抽象/静态/访问器/装饰器/私有名/计算名/
// 字段初值仍忽略（布局只记槽位）。

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
// 静态字面量折叠表，不占实例槽；staticMethods 为静态方法内联体
// （`C.m()` 类名分发，this 置空；实例项永不持有，见 saRecordClassNamed）；
// staticGetters/staticSetters 为静态存取器内联体（`C.g`/`C.s = v` 类名分发，
// 空 this 内联，镜像 staticMethods；实例项永不持有）；
// fkinds 为字段种表，i32/str/arr/inst，句柄种 8 字节对齐；
// fsub 为嵌套字段的子布局名（fkinds inst 时有效）。
type saClassDef struct {
	name          string
	fields        []saClassField
	offsets       map[string]int
	fkinds        map[string]string
	fsub          map[string]string
	size          int
	methods       map[string]*ast.Node
	staticMethods map[string]*ast.Node
	getters       map[string]*ast.Node
	setters       map[string]*ast.Node
	staticGetters map[string]*ast.Node
	staticSetters map[string]*ast.Node
	statics       map[string]saStaticVal
	ctor          *ast.Node
	ctorOwner     string
	parent        string
	isIface       bool
	isAbstract    bool
}

// saFieldWidth 返回字段槽宽与对齐（i32 系 4/4，str/arr/inst 句柄 8/8；
// 形状证据：封存 widthOf:268-279 + alignTo:281-289）。
func saFieldWidth(kind string) (int, int) {
	if kind == "str" || kind == "arr" || kind == "inst" {
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
	// 类装饰器无一等值语义，大声拒（成员装饰器另门；封存 TestLoudDecoratorUsing 同形）。
	if len(st.Decorators()) > 0 {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class decorators are not lowerable"})
		return false
	}
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
	def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, getters: map[string]*ast.Node{}, setters: map[string]*ast.Node{}, staticGetters: map[string]*ast.Node{}, staticSetters: map[string]*ast.Node{}}
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
				if sub, ok := bdef.fsub[f.name]; ok {
					if def.fsub == nil {
						def.fsub = map[string]string{}
					}
					def.fsub[f.name] = sub
				}
			}
			for k, v := range bdef.methods {
				if _, ok := def.methods[k]; !ok {
					def.methods[k] = v
				}
			}
			// 静态方法同例继承（子类覆写；形状证据：封存 inheritClass staticMethods 拷贝）。
			if def.staticMethods == nil {
				def.staticMethods = map[string]*ast.Node{}
			}
			for k, v := range bdef.staticMethods {
				if _, ok := def.staticMethods[k]; !ok {
					def.staticMethods[k] = v
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
			// 静态存取器随静态方法同例继承（子类覆写；封存 inheritClass staticGetters/Setters 拷贝）。
			if def.staticGetters == nil {
				def.staticGetters = map[string]*ast.Node{}
			}
			for k, v := range bdef.staticGetters {
				if _, ok := def.staticGetters[k]; !ok {
					def.staticGetters[k] = v
				}
			}
			if def.staticSetters == nil {
				def.staticSetters = map[string]*ast.Node{}
			}
			for k, v := range bdef.staticSetters {
				if _, ok := def.staticSetters[k]; !ok {
					def.staticSetters[k] = v
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
			if fn == nil || (fn.Kind != ast.KindIdentifier && fn.Kind != ast.KindPrivateIdentifier) {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed/private field names are not lowerable"})
				return false
			}
			// 私有字段按属主 mangle（`#x` → `#C#x`，遮蔽属主各占槽；封存 8208）。
			fkey := fn.Text()
			if fn.Kind == ast.KindPrivateIdentifier {
				fkey = saPrivKey(name, fn.Text())
			}
			if ast.HasModifier(m, ast.ModifierFlagsStatic) {
				// 静态字面量折叠记表（不占实例槽；封存 recordClassNamed:9703-9711）；
				// 非字面静态走 legacy 实例槽（封存 s2 形；i32 恒 4 字节，见 step48）。
				if text, kind, ok := saStaticLiteral(pd.Initializer); ok {
					if def.statics == nil {
						def.statics = map[string]saStaticVal{}
					}
					def.statics[fkey] = saStaticVal{text: text, kind: kind}
					continue
				}
			}
			// 字段初值表达式忽略（布局只记槽位，不求值；封存 recordClassNamed
			// 9691-9743 不读 Initializer；初值语义随 alloc，见 AGENTS step47）。
			// 字段种：i32/bool 恒 4 字节槽，string 为头指针 8 字节槽
			// （封存 widthOf；其余宽度无槽）。
			fkind := "i32"
			if pd.Type != nil {
				if k, ok := saAnnotKind(pd.Type); !ok || (k != "i32" && k != "bool" && k != "str" && k != "arr") {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32, string or array"})
					return false
				} else if k == "str" {
					fkind = "str"
				} else if k == "arr" {
					fkind = "arr"
				}
			}
			if _, dup := def.offsets[fkey]; dup {
				// 继承字段重声明：守基偏移（同宽同种恒成立）。
				// 形状证据：封存 recordClassNamed:9717-9733。
				if def.parent == "" || ownFields[fkey] {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate field " + fkey})
					return false
				}
				continue
			}
			ownFields[fkey] = true
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fkey, offset: off})
			def.offsets[fkey] = off
			def.fkinds[fkey] = fkind
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
				// 静态方法另表记录，`C.m()` 类名分发内联（实例项永不持有同名，
				// 防遮蔽/元数错位；静态调用走 saInlineStaticMethod；形状证据：
				// 封存 recordClassNamed 静态另表 + lowerClassStaticCall）。
				if def.staticMethods == nil {
					def.staticMethods = map[string]*ast.Node{}
				}
				if _, dup := def.staticMethods[mn.Text()]; dup && ownMethods[mn.Text()] {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate method " + mn.Text()})
					return false
				}
				// 覆写语义：子类同名直接覆盖继承静态（形状证据：封存 inheritClass 同例）。
				ownMethods[mn.Text()] = true
				def.staticMethods[mn.Text()] = m
				continue
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
				// 静态存取器另表记录，`C.g`/`C.s = v` 类名分发内联（实例项永不持有，
				// 镜像 staticMethods；形状证据：封存 recordClassNamed 静态存取器拆分）。
				if m.Kind == ast.KindGetAccessor {
					if def.staticGetters == nil {
						def.staticGetters = map[string]*ast.Node{}
					}
					if _, dup := def.staticGetters[an.Text()]; dup && ownMethods[an.Text()] {
						ln, col := pos(m.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate getter " + an.Text()})
						return false
					}
					ownMethods[an.Text()] = true
					def.staticGetters[an.Text()] = m
				} else {
					if def.staticSetters == nil {
						def.staticSetters = map[string]*ast.Node{}
					}
					if _, dup := def.staticSetters[an.Text()]; dup && ownMethods[an.Text()] {
						ln, col := pos(m.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate setter " + an.Text()})
						return false
					}
					ownMethods[an.Text()] = true
					def.staticSetters[an.Text()] = m
				}
				continue
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
	// 参数属性按上游 RuntimeSyntax 合成字段追加（显式/继承槽位优先，重复跳过；
	// 可访问性抹平为普通槽；无标注/非标识大声拒；形状证据：封存
	// recordParamPropFields + visitClassDeclaration 合成 PropertyDeclaration）。
	if def.ctor != nil {
		if !saRecordParamPropFields(def, def.ctor, &off, pos, refusals) {
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

// saParamPropNames 列出构造器中标识符参数属性名（上游 getParameterProperties
// 标识符子集；非标识形状在记录期已拒，此处跳过；形状证据：封存 paramPropNames）。
func saParamPropNames(ctor *ast.Node) []string {
	var out []string
	for _, p := range ctor.Parameters() {
		if !ast.IsParameterPropertyDeclaration(p, ctor) {
			continue
		}
		if nm := p.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			out = append(out, nm.Text())
		}
	}
	return out
}

// saRecordParamPropFields 将构造器参数属性记为实例字段（上游
// RuntimeSyntaxTransformer.visitClassDeclaration 按标识符参数属性合成
// PropertyDeclaration；检测谓词同源 ast.IsParameterPropertyDeclaration）。
// 可访问性抹平为普通槽；已存在槽（显式成员或继承）跳过（重复为 checker 错，
// 现有槽位获胜）；无标注/非标识大声拒；形状证据：封存 recordParamPropFields。
func saRecordParamPropFields(def *saClassDef, ctor *ast.Node, off *int, pos func(int) (int, int), refusals *[]SARefusal) bool {
	for _, p := range ctor.Parameters() {
		if !ast.IsParameterPropertyDeclaration(p, ctor) {
			continue
		}
		nm := p.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			ln, col := pos(p.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "parameter property names must be identifiers"})
			return false
		}
		fname := nm.Text()
		if _, dup := def.offsets[fname]; dup {
			continue
		}
		pd := p.AsParameterDeclaration()
		if pd == nil || pd.Type == nil {
			ln, col := pos(p.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "parameter property " + fname + " needs a type annotation (slot width)"})
			return false
		}
		k, ok := saAnnotKind(pd.Type)
		if !ok || (k != "i32" && k != "bool" && k != "str" && k != "arr") {
			ln, col := pos(p.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32, string or array"})
			return false
		}
		fkind := "i32"
		if k == "str" {
			fkind = "str"
		} else if k == "arr" {
			fkind = "arr"
		}
		*off = saAlignOff(*off, fkind)
		def.fields = append(def.fields, saClassField{name: fname, offset: *off})
		def.offsets[fname] = *off
		def.fkinds[fname] = fkind
		sz, _ := saFieldWidth(fkind)
		*off += sz
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
					if sub, ok := base.fsub[f.name]; ok {
						if def.fsub == nil {
							def.fsub = map[string]string{}
						}
						def.fsub[f.name] = sub
					}
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
			// string/arr 为句柄 8 字节槽；已记录接口名（TypeReference）为嵌套
			// 句柄 8 字节槽（与上游 layoutOfCheckerName 同形）；余下无槽，拒。
			if k, ok := saAnnotKind(pd.Type); ok && (k == "i32" || k == "bool" || k == "str" || k == "arr") {
				if k == "str" {
					fkind = "str"
				} else if k == "arr" {
					fkind = "arr"
				}
			} else if pd.Type.Kind == ast.KindTypeReference && pd.Type.AsTypeReferenceNode() != nil && pd.Type.AsTypeReferenceNode().TypeName != nil {
				if sub, ok := classes[pd.Type.AsTypeReferenceNode().TypeName.Text()]; ok && sub.isIface {
					fkind = "inst"
					if def.fsub == nil {
						def.fsub = map[string]string{}
					}
					def.fsub[fn.Text()] = sub.name
				} else {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "interface fields must be i32, string, array or recorded interface"})
					return false
				}
			} else {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "interface fields must be i32, string, array or recorded interface"})
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

// saStaticLiteral 折叠静态字面量初值（上游 ast.SkipOuterExpressions +
// OEKAssertions 精确四 kind 解包，括号保持不透明与上游调用形一致；数字面→i32 文本，
// 串/反引号字面→原文（具化路径与字面量同字节），true/false→1/0；
// 形状证据：封存 staticLiteralText 去孤岛 p1）。
func saStaticLiteral(n *ast.Node) (string, string, bool) {
	if n == nil {
		return "", "", false
	}
	n = ast.SkipOuterExpressions(n, ast.OEKAssertions)
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
		key := field
		if strings.HasPrefix(field, "#") {
			if scope.thisClass == "" {
				return false
			}
			key = saPrivKey(scope.thisClass, field)
			if _, ok := d.offsets[key]; !ok {
				return false
			}
		}
		if d.fkinds[key] == "str" {
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
			// 私有静态串判定（类名基 + 属主一致）。
			if _, key, msg, ok := saPrivStaticKey(base, field, scope); ok && msg == "" {
				if sv, ok := d.statics[key]; ok {
					return sv.kind == "str"
				}
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

// saPrivKey 映射私有字段到属主（`#x` 在 C 中 → `#C#x`，遮蔽属主各占槽；
// 公共名直通；形状证据：封存 privFieldKey:8208-8213）。
func saPrivKey(owner, fname string) string {
	if !strings.HasPrefix(fname, "#") {
		return fname
	}
	return "#" + owner + fname
}

// saPrivResolve 按词法属主解 `#` 成员（scope.thisClass；布局缺席/域外大声拒；
// 公共名直通。形状证据：封存 privResolveOwned:8218-8228 + privResolve:8230-8241）。
// 返回（布局键，拒因）。
func saPrivResolve(def *saClassDef, raw, owner string) (string, string) {
	if !strings.HasPrefix(raw, "#") {
		return raw, ""
	}
	if owner == "" {
		return "", fmt.Sprintf("private field %s is not accessible outside a class method", raw)
	}
	key := saPrivKey(owner, raw)
	if _, ok := def.offsets[key]; !ok {
		// 静态私名走 statics 表。
		if _, ok := def.statics[key]; !ok {
			return "", fmt.Sprintf("private field %s is not declared in class %s", raw, owner)
		}
	}
	return key, ""
}

// saPrivStaticKey 解析私有静态读（`C.#K`，类名基 + 属主一致；
// 封存 8029-8064）。返回（定义，布局键，拒因，命中）。
func saPrivStaticKey(base *ast.Node, field string, scope *saScope) (*saClassDef, string, string, bool) {
	if field == "" || !strings.HasPrefix(field, "#") {
		return nil, "", "", false
	}
	if base == nil || base.Kind != ast.KindIdentifier {
		return nil, "", "", false
	}
	nm := base.Text()
	def, ok := scope.classes[nm]
	if !ok {
		return nil, "", "", false
	}
	owner := scope.thisClass
	if owner == "" {
		return nil, "", fmt.Sprintf("private static %s is not accessible outside a class method", field), false
	}
	if owner != nm {
		return nil, "", fmt.Sprintf("private static %s is not declared in class %s", field, owner), false
	}
	key := saPrivKey(nm, field)
	if _, ok := def.statics[key]; !ok {
		// 非字面 legacy 私有静态走实例槽，类名基不可读（与公共 legacy 同形，下探）。
		return nil, "", "", false
	}
	return def, key, "", true
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
				if sk == "str" || sk == "arr" || sk == "inst" {
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
		if fk := def.fkinds[o.fname]; fk == "arr" {
			// arr 域存句柄（字面量递归/绑定直传经句柄总线）。
			v, msg := saArrValueOf(w, o.init, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], v))
			continue
		}
		if fk := def.fkinds[o.fname]; fk == "inst" {
			// 嵌套接口域：内层字面按子布局构造存句柄（与上游子布局内联同形）。
			sub, ok := def.fsub[o.fname]
			if !ok {
				return "", "", "nested field " + o.fname + " has no recorded sub layout"
			}
			if o.init == nil || o.init.Kind != ast.KindObjectLiteralExpression {
				return "", "", "nested field " + o.fname + " needs an object literal"
			}
			v, _, msg := saLowerObjectLiteral(w, o.init, sub, scope, pos, refusals, nextTemp)
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
		if pd.Type != nil {
			if k, ok := saAnnotKind(pd.Type); ok && k == "arr" {
				v, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				paramVal[nm.Text()] = v
				continue
			}
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
		fname := pa.Name().Text()
		if strings.HasPrefix(fname, "#") {
			fname = saPrivKey(owner, fname)
		}
		if def.fkinds[fname] == "str" {
			want[bin.Right.Text()] = true
		}
	}
	// 参数属性隐式 wiring 的 str 位（无显式 `this.p = p` 语句，但 new 侧实参须按串求值；
	// 形状证据：封存 recordParamPropFields 种表 + wireCtorBody 注入）。
	for _, p := range ctor.Parameters() {
		if !ast.IsParameterPropertyDeclaration(p, ctor) {
			continue
		}
		nm := p.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		if def.fkinds[nm.Text()] == "str" {
			want[nm.Text()] = true
		}
	}
	return want
}

// saWireCtorBody 解释构造体语句（`super(...)` 委托基 wiring + `this.f = param` +
// 参数属性隐式 `this.p = p` 注在顶层 super() 后（无 super 置顶），显式 wiring 优先；
// 形状证据：封存 wireCtorBody + wireCtorFieldStore + visitConstructorBody/Worker）。
func saWireCtorBody(w printer.EmitTextWriter, h, owner string, ctor *ast.Node, paramVal map[string]string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	body := ctor.Body()
	if body == nil {
		return true
	}
	stmts := body.Statements()
	wired := map[string]bool{}
	for _, s := range stmts {
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
		wired[pa.Name().Text()] = true
	}
	inject := func() bool {
		def, ok := scope.classes[owner]
		if !ok {
			ln, col := pos(ctor.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + owner})
			return false
		}
		for _, pname := range saParamPropNames(ctor) {
			if wired[pname] {
				continue
			}
			v, ok := paramVal[pname]
			if !ok {
				ln, col := pos(ctor.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "parameter property " + pname + " has no argument"})
				return false
			}
			off, ok := def.offsets[pname]
			if !ok {
				ln, col := pos(ctor.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + pname + " is not in the " + owner + " layout"})
				return false
			}
			if def.fkinds[pname] == "str" || def.fkinds[pname] == "arr" {
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
				continue
			}
			w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
		}
		return true
	}
	superIdx := -1
	for i, s := range stmts {
		if saIsSuperCall(s) {
			superIdx = i
			break
		}
	}
	if superIdx < 0 {
		if !inject() {
			return false
		}
	}
	for i, s := range stmts {
		if saIsSuperCall(s) {
			if !saWireSuperCtor(w, h, owner, s, paramVal, scope, pos, refusals, nextTemp) {
				return false
			}
			if i == superIdx {
				if !inject() {
					return false
				}
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
		// 构造体内 this 即属主类本身（私有域按属主解）。
		fname := pa.Name().Text()
		if strings.HasPrefix(fname, "#") {
			key, msg := saPrivResolve(def, fname, owner)
			if msg != "" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false
			}
			fname = key
		}
		off, ok := def.offsets[fname]
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + fname + " is not in the " + owner + " layout"})
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
		// str/arr 域存句柄（右值已按种求值）。
		if def.fkinds[fname] == "str" || def.fkinds[fname] == "arr" {
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
			case ast.KindArrayLiteralExpression:
				if _, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp); msg != "" {
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
		case ast.KindArrayLiteralExpression:
			// arr 字面量超参（基 arr 形参位；值位门在基 wire）。
			v, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp)
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

// saInstArg 求实例实参句柄（绑定标识符/`this`；子类实例可传基形参，
// 展平布局前缀一致，读基域安全）。
func saInstArg(a *ast.Node, want string, scope *saScope) (string, string) {
	var have, h string
	switch {
	case a != nil && a.Kind == ast.KindIdentifier:
		nm := a.Text()
		k, ok := scope.types[nm]
		if !ok || len(k) <= 5 || k[:5] != "inst:" {
			return "", "method instance argument must be a bound instance"
		}
		have, h = k[5:], nm
	case a != nil && a.Kind == ast.KindThisKeyword && scope.thisSelf != "":
		have, h = scope.thisClass, scope.thisSelf
	default:
		return "", "method instance argument must be a bound instance"
	}
	for c := have; c != ""; {
		if c == want {
			return h, ""
		}
		d, ok := scope.classes[c]
		if !ok {
			break
		}
		c = d.parent
	}
	return "", "method instance argument class mismatch (want " + want + ")"
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
	return saInlineMethodCore(w, recv, def.name, def, mn, ce, scope, pos, refusals, needImport, nextLabel, nextTemp)
}

// saInlineStaticMethod 内联 `C.m(args)`（无实例，this 置空使实例态诚实拒，
// 其余静态走同一分发；形状证据：封存 lowerClassStaticCall）。
func saInlineStaticMethod(w printer.EmitTextWriter, className string, def *saClassDef, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	mn, ok := def.staticMethods[method]
	if !ok {
		return "", "unknown static method " + method
	}
	if mn.Body() == nil {
		return "", method + " has no body (overload signatures do not inline)"
	}
	return saInlineMethodCore(w, "", def.name, def, mn, ce, scope, pos, refusals, needImport, nextLabel, nextTemp)
}

// saInlineMethodCore 是实例/静态共享内联核：形参绑定（通法同例），
// thisSelf 别接收者（静态置空），体经值槽汇合；形状证据：封存
// inlineClassMethod:10188-10280。
func saInlineMethodCore(w printer.EmitTextWriter, thisSelf, className string, def *saClassDef, mn *ast.Node, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	params := mn.Parameters()
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != len(params) {
		return "", "method takes exact arguments"
	}
	// 方法形参恒 i32（回调快照绑定只存标量；句柄形参无改写机制，大声拒）。
	// 实例注解直传绑定（`add(o: C)` 跨实例同属主读；封存方法实例形参位）。
	kinds := make([]string, len(params))
	for i, p := range params {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			return "", "method parameter shape is not lowerable"
		}
		if pd.Type != nil {
			if k, ok := saAnnotKind(pd.Type); ok {
				if k != "i32" {
					return "", "method parameters must be i32"
				}
				continue
			}
			if pd.Type.Kind == ast.KindTypeReference {
				if ref := pd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
					if _, ok := scope.classes[ref.TypeName.Text()]; ok {
						kinds[i] = "inst:" + ref.TypeName.Text()
						continue
					}
				}
			}
			return "", "method parameters must be i32"
		}
	}
	var argVals []string
	for i, a := range argNodes {
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			return "", "method arguments must be values"
		}
		// 实例形参实参直传句柄（子类实例可传基形参，展平前缀一致）。
		if i < len(kinds) && len(kinds[i]) > 5 && kinds[i][:5] == "inst:" {
			h, msg := saInstArg(a, kinds[i][5:], scope)
			if msg != "" {
				return "", msg
			}
			argVals = append(argVals, h)
			continue
		}
		v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		argVals = append(argVals, v)
	}
	savedSelf, savedClass := scope.thisSelf, scope.thisClass
	scope.thisSelf, scope.thisClass = thisSelf, className
	// 返回种按声明注解（str 走串槽；余下走 i32 槽）。
	wantKind := "i32"
	if k, ok := saMethodReturnKind(mn); ok && k == "str" {
		wantKind = "str"
	}
	v, msg := saCallbackValue(w, mn, argVals, true, wantKind, scope, pos, refusals, needImport, nextLabel, nextTemp, kinds)
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
	if gn.Body() == nil {
		return "", def.name + "." + name + " has no body (overload signatures do not inline)"
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

// saInlineStaticGetter 内联 `C.g` 读（空 this，实例态经既有门诚实拒；
// 镜像 saInlineStaticMethod；形状证据：封存 lowerClassStaticCall 存取器位）。
func saInlineStaticGetter(w printer.EmitTextWriter, className string, def *saClassDef, name string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	gn, ok := def.staticGetters[name]
	if !ok {
		if _, ok := def.staticSetters[name]; ok {
			return "", name + " is write-only (setter has no getter)"
		}
		return "", "unknown static member " + name
	}
	if gn.Body() == nil {
		return "", def.name + "." + name + " has no body (overload signatures do not inline)"
	}
	if len(gn.Parameters()) != 0 {
		return "", "getter " + name + " takes 0 parameters"
	}
	savedSelf, savedClass := scope.thisSelf, scope.thisClass
	scope.thisSelf, scope.thisClass = "", def.name
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
	if sn.Body() == nil {
		return def.name + "." + name + " has no body (overload signatures do not inline)"
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

// saInlineStaticSetter 内联 `C.s = v` 写（空 this；镜像 saInlineStaticGetter）。
func saInlineStaticSetter(w printer.EmitTextWriter, className string, def *saClassDef, name, val string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) string {
	sn, ok := def.staticSetters[name]
	if !ok {
		return "unknown static member " + name
	}
	if sn.Body() == nil {
		return def.name + "." + name + " has no body (overload signatures do not inline)"
	}
	if len(sn.Parameters()) != 1 {
		return "setter " + name + " takes 1 parameter"
	}
	savedSelf, savedClass := scope.thisSelf, scope.thisClass
	scope.thisSelf, scope.thisClass = "", def.name
	_, msg := saCallbackValue(w, sn, []string{val}, false, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.thisSelf, scope.thisClass = savedSelf, savedClass
	return msg
}
func saLowerClassFieldLoad(w printer.EmitTextWriter, h string, def *saClassDef, field string, scope *saScope, nextTemp *int) (string, string) {
	off, ok := def.offsets[field]
	if !ok {
		return "", "unknown field " + field
	}
	// str/arr 域读回句柄（16 字节头在堆上，值即其址；临时量记 str
	// 供下游串位；封存 lowerMemberChain:8294-8297 `load as <type>` 同形）。
	if def.fkinds[field] == "str" {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
		scope.types[t] = "str"
		return t, ""
	}
	if def.fkinds[field] == "arr" {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
		return t, ""
	}
	if def.fkinds[field] == "inst" {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
		return t, ""
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", t, h, off))
	return t, ""
}

// saChainBase 解对象链基（`q.p`→内层句柄；递归支持多层；
// 叶子由调用方按表读/存；非 inst 链节一律失败，调用方沿旧门）。
func saChainBase(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, *saClassDef, string) {
	pa := e.AsPropertyAccessExpression()
	if pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return "", nil, "chained base must be an identifier field"
	}
	leaf := pa.Name().Text()
	var h string
	var def *saClassDef
	switch {
	case pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword:
		if scope.thisSelf == "" {
			return "", nil, "this outside a class method is not lowerable"
		}
		d, ok := scope.classes[scope.thisClass]
		if !ok {
			return "", nil, "unknown class " + scope.thisClass
		}
		h, def = scope.thisSelf, d
	case pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier:
		k, ok := scope.types[pa.Expression.Text()]
		if !ok || len(k) <= 5 || k[:5] != "inst:" {
			return "", nil, "chained base is not a bound instance"
		}
		d, ok := scope.classes[k[5:]]
		if !ok {
			return "", nil, "unknown class " + k[5:]
		}
		h, def = pa.Expression.Text(), d
	case pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression:
		ih, idef, msg := saChainBase(w, pa.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", nil, msg
		}
		h, def = ih, idef
	default:
		return "", nil, "chained base is not a bound instance"
	}
	off, ok := def.offsets[leaf]
	if !ok {
		return "", nil, "unknown field " + leaf
	}
	if def.fkinds[leaf] != "inst" {
		return "", nil, "chained field " + leaf + " is not a nested object"
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
	sub, ok := scope.classes[def.fsub[leaf]]
	if !ok {
		return "", nil, "nested field " + leaf + " has no recorded sub layout"
	}
	return t, sub, ""
}

// saLowerClassFieldStore 写 `o.f = v`（偏移 store；i32 存值，str/arr 存句柄）。
func saLowerClassFieldStore(w printer.EmitTextWriter, h string, def *saClassDef, field, v string) string {
	off, ok := def.offsets[field]
	if !ok {
		return "unknown field " + field
	}
	if def.fkinds[field] == "str" || def.fkinds[field] == "arr" {
		w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
		return ""
	}
	w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
	return ""
}
