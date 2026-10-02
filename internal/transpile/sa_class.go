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
// 方法/构造恒空，不可 new）。
type saClassDef struct {
	name    string
	fields  []saClassField
	offsets map[string]int
	size    int
	methods map[string]*ast.Node
	ctor    *ast.Node
	isIface bool
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
	if hc := cd.HeritageClauses; hc != nil {
		for _, h := range hc.Nodes {
			if h.Kind == ast.KindHeritageClause && h.AsHeritageClause().Token == ast.KindExtendsKeyword {
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class heritage is not lowerable"})
				return false
			}
		}
	}
	if ast.HasModifier(st, ast.ModifierFlagsAbstract) {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "abstract class " + name + " cannot be instantiated"})
		return false
	}
	def := &saClassDef{name: name, offsets: map[string]int{}, methods: map[string]*ast.Node{}}
	off := 0
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
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate field " + fn.Text()})
				return false
			}
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
			if _, dup := def.methods[mn.Text()]; dup {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate method " + mn.Text()})
				return false
			}
			def.methods[mn.Text()] = m
		case ast.KindSemicolonClassElement:
		default:
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class member is not lowerable (getters/setters/indexers refused)"})
			return false
		}
	}
	def.size = off
	classes[name] = def
	return true
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
			ln, col := pos(m.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate field " + fn.Text()})
			return false
		}
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
	for _, s := range body.Statements() {
		// 仅 this.f = param  wiring（余下语句大声拒，镜像 wireCtorStatement）。
		if s.Kind != ast.KindExpressionStatement {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + name + " supports only this.f = param wirings"})
			return "", "unwirable"
		}
		ex := s.AsExpressionStatement().Expression
		if ex == nil || ex.Kind != ast.KindBinaryExpression {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + name + " supports only this.f = param wirings"})
			return "", "unwirable"
		}
		bin := ex.AsBinaryExpression()
		if bin.OperatorToken == nil || bin.OperatorToken.Kind != ast.KindEqualsToken ||
			bin.Left == nil || bin.Left.Kind != ast.KindPropertyAccessExpression {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + name + " supports only this.f = param wirings"})
			return "", "unwirable"
		}
		pa := bin.Left.AsPropertyAccessExpression()
		if pa.Expression == nil || pa.Expression.Kind != ast.KindThisKeyword || pa.Name() == nil {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + name + " supports only this.f = param wirings"})
			return "", "unwirable"
		}
		off, ok := def.offsets[pa.Name().Text()]
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + pa.Name().Text() + " is not in the " + name + " layout"})
			return "", "unwirable"
		}
		if bin.Right == nil || bin.Right.Kind != ast.KindIdentifier {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor wiring right side must be a parameter name"})
			return "", "unwirable"
		}
		v, ok := paramVal[bin.Right.Text()]
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor parameter " + bin.Right.Text() + " has no value"})
			return "", "unwirable"
		}
		w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
	}
	return h, ""
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

// saLowerClassFieldLoad 读 `o.f`（偏移 load；形状证据：字段偏移布局）。
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
