// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"fmt"
	"sort"
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
// fsub 为嵌套字段的子布局名（fkinds inst 时有效）；
// fopt 记可选字段（`b?: i32` 缺省零填位；必备缺省沿旧门）；
// tparams/fdefs 为泛型接口模板（具化前只存不用，单态实例另行派生）；
// isMono 为单态派生布局（与用户声明名区分，防键碰撞静默复用）；
// isSynth 为字面量合成布局（不参与键集匹配，保多命中拒不变）。
type saClassDef struct {
	name          string
	fields        []saClassField
	offsets       map[string]int
	fkinds        map[string]string
	fsub          map[string]string
	fopt          map[string]bool
	fpos          map[string]int
	abstracts     map[string]int
	abstractKinds map[string]string
	tparams       []string
	fdefs         map[string]*ast.TypeNode
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
	finits        map[string]*ast.Node
	finitOrder    []string
	parent        string
	implements    []string
	isIface       bool
	isAbstract    bool
	isMono        bool
	isSynth       bool
	isStdBase     bool
}

// saIsBareTypeParamField 报告字段声明类型是否为裸类型参引用（未决泛型擦除
// 回退，如 `Box<T>.value`；具化/真数组声明非裸参，返回 false）。
func saIsBareTypeParamField(def *saClassDef, fname string) bool {
	if def == nil || def.fdefs == nil {
		return false
	}
	tn, ok := def.fdefs[fname]
	if !ok || tn == nil || tn.Kind != ast.KindTypeReference {
		return false
	}
	ref := tn.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return false
	}
	for _, tp := range def.tparams {
		if tp == ref.TypeName.Text() {
			return true
		}
	}
	return false
}

// saFieldWidth 返回字段槽宽与对齐（i32 系 4/4，str/arr/inst/f64 句柄与
// 双精度 8/8；形状证据：封存 widthOf:268-279 + alignTo:281-289）。
func saFieldWidth(kind string) (int, int) {
	if kind == "str" || kind == "arr" || kind == "inst" || kind == "f64" || kind == "jsval" {
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

// saStdBaseDef synthesizes a web std base class layout (JEV par_std
// seed_synth_bases): Error carries message:str + name:str, DOMException
// carries name:str + message:str, EventTarget carries no fields. The def
// has no ctor AST, so super(...) in a subclass hits the existing nil-ctor
// arm (args evaluated in order, then discarded); subclass-owned fields and
// methods lower exactly like any other inheritance. A user-declared class
// of the same name recorded BEFORE the subclass wins (lookup hits first);
// a later user declaration still hits the existing duplicate-class refusal.
func saStdBaseDef(name string) *saClassDef {
	mk := func(fields []saClassField, fkinds map[string]string, size int) *saClassDef {
		offsets := map[string]int{}
		for _, f := range fields {
			offsets[f.name] = f.offset
		}
		return &saClassDef{name: name, fields: fields, offsets: offsets, fkinds: fkinds,
			methods: map[string]*ast.Node{}, staticMethods: map[string]*ast.Node{},
			getters: map[string]*ast.Node{}, setters: map[string]*ast.Node{},
			staticGetters: map[string]*ast.Node{}, staticSetters: map[string]*ast.Node{},
			statics: map[string]saStaticVal{}, fopt: map[string]bool{},
			fdefs: map[string]*ast.TypeNode{}, size: size, isStdBase: true}
	}
	switch name {
	case "Error":
		return mk([]saClassField{{name: "message", offset: 0}, {name: "name", offset: 8}},
			map[string]string{"message": "str", "name": "str"}, 16)
	case "DOMException":
		return mk([]saClassField{{name: "name", offset: 0}, {name: "message", offset: 8}},
			map[string]string{"name": "str", "message": "str"}, 16)
	case "EventTarget":
		return mk(nil, map[string]string{}, 0)
	}
	return nil
}

// saSeedStdBase records the synthetic std base in classes if absent.
func saSeedStdBase(classes map[string]*saClassDef, base string) bool {
	if _, ok := classes[base]; ok {
		return false
	}
	def := saStdBaseDef(base)
	if def == nil {
		return false
	}
	classes[base] = def
	return true
}

// saIsStdOpaqueClass reports whether name is a web std class lowered as an
// opaque jsval box (JEV par_std jsval_opaque): the value is never observed
// field-by-field by tsgosa, only passed along (fetch options/bodies) or
// touched through the small method table below. Promise is NOT in this set
// (JEV keep_refusing); Response is NOT either (fetch slice: synthetic inst).
func saIsStdOpaqueClass(name string) bool {
	switch name {
	case "AbortController", "Headers", "FormData", "Blob", "DataView",
		"CompressionStream", "DecompressionStream", "TextEncoder", "URL":
		return true
	}
	return false
}

// saStdOpaqueMethod maps (class, method) to a result kind: "void" for
// fire-and-forget mutators, "jsval" for opaque value results. Anything else
// keeps refusing loudly (URL protocol/hostname/pathname reads, Headers.get,
// URLSearchParams-style accessors, stream readable/writable chains).
func saStdOpaqueMethod(class, method string) (string, bool) {
	switch class {
	case "AbortController":
		if method == "abort" {
			return "void", true
		}
	case "FormData":
		if method == "append" || method == "set" || method == "delete" {
			return "void", true
		}
	case "TextEncoder":
		if method == "encode" {
			return "jsval", true
		}
	}
	return "", false
}

// saStdOpaquePure reports whether a ctor/method argument node is side-effect
// free (scalar literals, bound identifiers, literals composed of pure nodes).
// Pure args are discarded unevaluated: the opaque value never observes them.
// Impure args go through saStdOpaqueEvalArg instead.
func saStdOpaquePure(a *ast.Node, scope *saScope) bool {
	if a == nil {
		return true
	}
	switch a.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral,
		ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindNullKeyword, ast.KindUndefinedKeyword:
		return true
	case ast.KindIdentifier:
		if _, ok := scope.types[a.Text()]; ok {
			return true
		}
		if _, ok := scope.topConsts[a.Text()]; ok {
			return true
		}
		if scope.topStr[a.Text()] {
			return true
		}
		return false
	case ast.KindArrayLiteralExpression:
		al := a.AsArrayLiteralExpression()
		if al == nil {
			return false
		}
		for _, el := range al.Elements.Nodes {
			if !saStdOpaquePure(el, scope) {
				return false
			}
		}
		return true
	case ast.KindObjectLiteralExpression:
		ol := a.AsObjectLiteralExpression()
		if ol == nil {
			return false
		}
		for _, m := range ol.Properties.Nodes {
			if m == nil || m.Kind != ast.KindPropertyAssignment {
				return false
			}
			if pa := m.AsPropertyAssignment(); pa == nil || !saStdOpaquePure(pa.Initializer, scope) {
				return false
			}
		}
		return true
	case ast.KindParenthesizedExpression:
		if pe := a.AsParenthesizedExpression(); pe != nil {
			return saStdOpaquePure(pe.Expression, scope)
		}
		return false
	}
	return false
}

// saStdOpaqueEvalArg evaluates one opaque ctor/method argument for its side
// effects, discarding the value. Pure args need no emission. Call args are
// evaluated (a refused call refuses the site); templates are evaluated only
// when they contain calls (saEvalStr + release-if-temp). Anything else that
// is not already lowerable refuses loudly (JEV: evaluate where lowerable,
// else refuse) instead of silently dropping an effect.
func saStdOpaqueEvalArg(w printer.EmitTextWriter, a *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	if a == nil || saStdOpaquePure(a, scope) {
		return ""
	}
	// Transparent wrappers peel to the inner node (parens/as/satisfies/non-null).
	inner := a
	for inner != nil {
		switch inner.Kind {
		case ast.KindParenthesizedExpression:
			if pe := inner.AsParenthesizedExpression(); pe != nil {
				inner = pe.Expression
				continue
			}
		case ast.KindAsExpression:
			if ae := inner.AsAsExpression(); ae != nil {
				inner = ae.Expression
				continue
			}
		case ast.KindSatisfiesExpression:
			if se := inner.AsSatisfiesExpression(); se != nil {
				inner = se.Expression
				continue
			}
		case ast.KindNonNullExpression:
			if nn := inner.AsNonNullExpression(); nn != nil {
				inner = nn.Expression
				continue
			}
		case ast.KindAwaitExpression:
			if aw := inner.AsAwaitExpression(); aw != nil {
				inner = aw.Expression
				continue
			}
		}
		break
	}
	if inner == nil {
		return ""
	}
	if saStdOpaquePure(inner, scope) {
		return ""
	}
	switch inner.Kind {
	case ast.KindCallExpression:
		op, _, msg := saEvalCall(w, inner.AsCallExpression(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		if saIsTempOp(op) {
			saReleaseStmtTemp(w, scope, op)
		}
		return ""
	case ast.KindTemplateExpression:
		h, msg := saEvalStr(w, inner, scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		if saIsTempOp(h) {
			saReleaseStmtTemp(w, scope, h)
		}
		return ""
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		// A field/index read on a bound base is a pure read (no emission
		// needed); on an unbound base it is not lowerable, so refuse.
		var base *ast.Node
		if inner.Kind == ast.KindPropertyAccessExpression {
			if pa := inner.AsPropertyAccessExpression(); pa != nil {
				base = pa.Expression
			}
		} else if ea := inner.AsElementAccessExpression(); ea != nil {
			base = ea.Expression
		}
		if base != nil && base.Kind == ast.KindIdentifier {
			if _, ok := scope.types[base.Text()]; ok {
				return ""
			}
		}
		return "std opaque constructor argument is not lowerable"
	}
	return "std opaque constructor argument is not lowerable"
}

// saLowerURLSearchParamsMethod lowers calls on a URLSearchParams accumulator
// binding (a str handle). set/append evaluate both args as strings, join a
// runtime "&" separator when the accumulator is non-empty (slot join, the
// ternary-arm pattern: SA has no phi), concatenate key + "=" + value, and
// rebind the variable with the str-assignment sequence (release old handle,
// consume new temp, heap + rebound marks). toString returns the binding
// itself (a borrow, like any str identifier read). Anything else (get, has,
// delete, sort, ...) is unhandled and falls through to the existing refusal.
func saLowerURLSearchParamsMethod(w printer.EmitTextWriter, recv, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string, bool) {
	if method == "toString" {
		var argNodes []*ast.Node
		if ce.Arguments != nil {
			argNodes = ce.Arguments.Nodes
		}
		if len(argNodes) != 0 {
			return "", false, "URLSearchParams.toString takes no arguments", true
		}
		return recv, false, "", true
	}
	if method != "set" && method != "append" {
		return "", false, "", false
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 2 {
		return "", false, "URLSearchParams." + method + " takes 2 arguments", true
	}
	kh, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg, true
	}
	vh, msg := saEvalStr(w, argNodes[1], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg, true
	}
	// Runtime "&" separator: present iff the accumulator is non-empty.
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	_, oldLen := saExpandStr(w, recv, nextTemp)
	nz := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", nz, oldLen))
	sepL := fmt.Sprintf("L_urlsp_sep_%d", *scope.nextLabel)
	*scope.nextLabel++
	nosepL := fmt.Sprintf("L_urlsp_nosep_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_urlsp_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", nz, sepL, nosepL))
	w.Write(fmt.Sprintf("%s:\n", sepL))
	amp := saLowerStringLiteral(w, "&", scope, nextTemp)
	withSep := saConcatSlices(w, recv, amp, scope, nextTemp)
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, withSep))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", nosepL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, recv))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	acc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", acc, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	kv := saConcatSlices(w, acc, kh, scope, nextTemp)
	eq := saLowerStringLiteral(w, "=", scope, nextTemp)
	kv2 := saConcatSlices(w, kv, eq, scope, nextTemp)
	final := saConcatSlices(w, kv2, vh, scope, nextTemp)
	saRebindRelease(w, scope, recv)
	w.Write(fmt.Sprintf("  %s = %s\n", recv, final))
	saConsumeOwn(scope, final)
	if b := saOwnOf(scope, recv); b != nil {
		b.heap = true
	}
	saMarkRebound(scope, recv)
	saClearNullConst(scope, recv)
	return "", true, "", true
}

// saSeedStatsLayout records the synthetic emitter Stats layout (patch 4)
// if absent; a user-declared class of the same name wins (lookup hits
// first). Fields are named exactly like the TS Stats surface so generic
// inst field loads serve .mode/.size/.mtimeMs (and .isFile/.isDirectory as
// fields); .isFile()/.isDirectory() calls go through saLowerStatsMethod.
func saSeedStatsLayout(classes map[string]*saClassDef) {
	if _, ok := classes["Stats"]; ok {
		return
	}
	classes["Stats"] = &saClassDef{
		name: "Stats",
		fields: []saClassField{
			{name: "isFile", offset: 0},
			{name: "isDirectory", offset: 4},
			{name: "mode", offset: 8},
			{name: "size", offset: 12},
			{name: "mtimeMs", offset: 16},
		},
		offsets: map[string]int{"isFile": 0, "isDirectory": 4, "mode": 8, "size": 12, "mtimeMs": 16},
		fkinds:  map[string]string{"isFile": "i32", "isDirectory": "i32", "mode": "i32", "size": "i32", "mtimeMs": "i32"},
		fsub:    map[string]string{},
		fopt:    map[string]bool{},
		fpos:    map[string]int{},
		methods: map[string]*ast.Node{},
		getters: map[string]*ast.Node{},
		setters: map[string]*ast.Node{},
		statics: map[string]saStaticVal{},
		size:    20,
	}
}

// saIsStatsCall reports whether ce is a projected `statSync(...)` call
// (named import from node:fs, recorded as mod "fs").
func saIsStatsCall(ce *ast.CallExpression, scope *saScope) bool {
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return false
	}
	if ce.Expression.Text() != "statSync" {
		return false
	}
	mod, ok := scope.imports["statSync"]
	return ok && mod == "fs"
}

// saLowerStatsMethod lowers isFile()/isDirectory() on a Stats handle value
// (an identifier name or a temp holding the struct). Anything else is
// unhandled and falls through to the existing refusal arms.
func saLowerStatsMethod(w printer.EmitTextWriter, recv, method string, ce *ast.CallExpression, scope *saScope, nextTemp *int) (string, bool, string, bool) {
	off, ok := map[string]int{"isFile": 0, "isDirectory": 4}[method]
	if !ok {
		return "", false, "", false
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 0 {
		return "", false, "Stats." + method + " takes no arguments", true
	}
	v := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", v, recv, off))
	return v, false, "", true
}

// saSeedResponseLayout records the synthetic emitter Response layout
// (patch 5) if absent; a user-declared class of the same name wins.
// status/ok are real i32 fields (generic inst field loads serve them);
// __bodyPtr/__bodyLen are internal slots read only by saLowerResponseText.
func saSeedResponseLayout(classes map[string]*saClassDef) {
	if _, ok := classes["Response"]; ok {
		return
	}
	classes["Response"] = &saClassDef{
		name: "Response",
		fields: []saClassField{
			{name: "status", offset: 0},
			{name: "ok", offset: 4},
			{name: "__bodyPtr", offset: 8},
			{name: "__bodyLen", offset: 16},
		},
		offsets: map[string]int{"status": 0, "ok": 4, "__bodyPtr": 8, "__bodyLen": 16},
		fkinds:  map[string]string{"status": "i32", "ok": "i32", "__bodyPtr": "f64", "__bodyLen": "i32"},
		fsub:    map[string]string{},
		fopt:    map[string]bool{},
		fpos:    map[string]int{},
		methods: map[string]*ast.Node{},
		getters: map[string]*ast.Node{},
		setters: map[string]*ast.Node{},
		statics: map[string]saStaticVal{},
		size:    20,
	}
}

// saIsFetchCall reports whether ce is a bare-global `fetch(...)` call (a
// user-declared or imported fetch wins and is not hijacked).
func saIsFetchCall(ce *ast.CallExpression, scope *saScope) bool {
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return false
	}
	if ce.Expression.Text() != "fetch" {
		return false
	}
	if _, ok := scope.funcs["fetch"]; ok {
		return false
	}
	if _, ok := scope.imports["fetch"]; ok {
		return false
	}
	return true
}

// saFetchCallOf peels transparent wrappers + await layers around e and
// returns the inner fetch call, if any (async-erasure: await is transparent).
func saFetchCallOf(e *ast.Node, scope *saScope) *ast.CallExpression {
	e = saUnwrapTransparent(e)
	for e != nil && e.Kind == ast.KindAwaitExpression {
		e = saUnwrapTransparent(e.AsAwaitExpression().Expression)
	}
	if e != nil && e.Kind == ast.KindCallExpression && saIsFetchCall(e.AsCallExpression(), scope) {
		return e.AsCallExpression()
	}
	return nil
}

// saPeelAwaitCall peels transparent wrappers + await layers around e and
// returns the inner call, if any (nil otherwise). It returns nil when no
// await layer was peeled (patch 6c): plain calls must fall through to the
// CallExpression block, which binds special str-call kinds (e.g. the
// .match() result array) that the awaited-str arm would misbind as str.
func saPeelAwaitCall(e *ast.Node, scope *saScope) *ast.CallExpression {
	e = saUnwrapTransparent(e)
	sawAwait := false
	for e != nil && e.Kind == ast.KindAwaitExpression {
		sawAwait = true
		e = saUnwrapTransparent(e.AsAwaitExpression().Expression)
	}
	if sawAwait && e != nil && e.Kind == ast.KindCallExpression {
		return e.AsCallExpression()
	}
	return nil
}

// saPanicIfErr emits: br <err> != 0 -> panicL (panic(2501) terminator),
// else okL. The caller writes the panic label, then continues at okL.
func saPanicIfErr(w printer.EmitTextWriter, err string, scope *saScope, nextTemp *int) (panicL, okL string) {
	nz := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", nz, err))
	panicL = fmt.Sprintf("L_fetch_panic_%d", *scope.nextLabel)
	*scope.nextLabel++
	okL = fmt.Sprintf("L_fetch_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", nz, panicL, okL))
	w.Write(fmt.Sprintf("%s:\n", panicL))
	w.Write("  panic(2501)\n")
	w.Write(fmt.Sprintf("%s:\n", okL))
	return panicL, okL
}

// saLowerFetch lowers `fetch(url, opts?)` to a synchronous http_client
// exchange. Returns (Response struct temp, false, "") or a refusal message.
// The struct temp is saOwnTemp'd; the caller (decl/assign/statement) claims
// or releases it. The http response buffer is intentionally NOT freed: the
// struct's __bodyPtr points into it (one resp leak per fetch, documented).
func saLowerFetch(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) < 1 || len(argNodes) > 2 {
		return "", false, "fetch takes 1 or 2 arguments"
	}
	urlH, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	useTLS := 0
	if lit := saUnwrapTransparent(argNodes[0]); lit != nil && lit.Kind == ast.KindStringLiteral {
		if strings.HasPrefix(lit.Text(), "https://") {
			useTLS = 1
		}
	}
	methodCode := 1
	type hdrEnt struct{ kbp, kbl, vbp, vbl string }
	var headers []hdrEnt
	var bodyBp, bodyBl string
	hasBody := false
	if len(argNodes) == 2 {
		opts := saUnwrapTransparent(argNodes[1])
		if opts == nil || opts.Kind != ast.KindObjectLiteralExpression {
			return "", false, "fetch options must be an object literal"
		}
		for _, prop := range opts.AsObjectLiteralExpression().Properties.Nodes {
			if prop == nil || prop.Kind != ast.KindPropertyAssignment {
				continue
			}
			pa := prop.AsPropertyAssignment()
			if pa.Name() == nil {
				continue
			}
			var pname string
			switch pa.Name().Kind {
			case ast.KindIdentifier:
				pname = pa.Name().Text()
			case ast.KindStringLiteral:
				pname = pa.Name().Text()
			default:
				continue
			}
			switch pname {
			case "method":
				mLit := saUnwrapTransparent(pa.Initializer)
				if mLit == nil || mLit.Kind != ast.KindStringLiteral {
					return "", false, "fetch method must be a string literal"
				}
				switch strings.ToLower(mLit.Text()) {
				case "get":
					methodCode = 1
				case "post":
					methodCode = 2
				case "put":
					methodCode = 3
				case "delete":
					methodCode = 4
				default:
					return "", false, "fetch method " + mLit.Text() + " is not mappable (GET/POST/PUT/DELETE only)"
				}
			case "headers":
				hLit := saUnwrapTransparent(pa.Initializer)
				if hLit == nil || hLit.Kind != ast.KindObjectLiteralExpression {
					return "", false, "fetch headers must be an object literal"
				}
				for _, hp := range hLit.AsObjectLiteralExpression().Properties.Nodes {
					if hp == nil || hp.Kind != ast.KindPropertyAssignment {
						continue
					}
					hpa := hp.AsPropertyAssignment()
					if hpa.Name() == nil {
						continue
					}
					var hname string
					switch hpa.Name().Kind {
					case ast.KindIdentifier:
						hname = hpa.Name().Text()
					case ast.KindStringLiteral:
						hname = hpa.Name().Text()
					default:
						continue
					}
					kh := saLowerStringLiteral(w, hname, scope, nextTemp)
					kbp, kbl := saExpandStr(w, kh, nextTemp)
					vh, vmsg := saEvalStr(w, hpa.Initializer, scope, pos, refusals, nextTemp)
					if vmsg != "" {
						return "", false, vmsg
					}
					vbp, vbl := saExpandStr(w, vh, nextTemp)
					headers = append(headers, hdrEnt{kbp, kbl, vbp, vbl})
				}
			case "body":
				bh, bmsg := saEvalStr(w, pa.Initializer, scope, pos, refusals, nextTemp)
				if bmsg != "" {
					return "", false, bmsg
				}
				bodyBp, bodyBl = saExpandStr(w, bh, nextTemp)
				hasBody = true
			default:
				// signal, cache, credentials, ...: ignored without
				// evaluation (async-erasure stance, documented).
			}
		}
	}
	scope.addImport("sa_std/http_client.sai")
	saSeedResponseLayout(scope.classes)
	bp, bl := saExpandStr(w, urlH, nextTemp)
	clientSlot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", clientSlot))
	cerr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_http_client_new(%d, &%s)\n", cerr, useTLS, clientSlot))
	saPanicIfErr(w, cerr, scope, nextTemp)
	client := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", client, clientSlot))
	reqSlot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", reqSlot))
	rerr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_http_client_req_new(%s, %d, &%s, %s, &%s)\n", rerr, client, methodCode, bp, bl, reqSlot))
	saPanicIfErr(w, rerr, scope, nextTemp)
	req := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", req, reqSlot))
	for _, he := range headers {
		herr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_http_client_req_add_header(%s, &%s, %s, &%s, %s)\n", herr, req, he.kbp, he.kbl, he.vbp, he.vbl))
		saPanicIfErr(w, herr, scope, nextTemp)
	}
	if hasBody {
		berr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_http_client_req_set_body(%s, &%s, %s)\n", berr, req, bodyBp, bodyBl))
		saPanicIfErr(w, berr, scope, nextTemp)
	}
	respSlot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", respSlot))
	serr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_http_client_req_send(%s, &%s)\n", serr, req, respSlot))
	saPanicIfErr(w, serr, scope, nextTemp)
	resp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", resp, respSlot))
	st16 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_http_client_resp_status(%s)\n", st16, resp))
	status := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = zext %s as i32\n", status, st16))
	ge200 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sge %s, 200\n", ge200, status))
	lt300 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, 300\n", lt300, status))
	ok := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, %s\n", ok, ge200, lt300))
	bPtrSlot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", bPtrSlot))
	bLenSlot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", bLenSlot))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", bPtrSlot))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as u64\n", bLenSlot))
	bsl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_http_client_resp_body_slice(%s, &%s, &%s)\n", bsl, resp, bPtrSlot, bLenSlot))
	saReleaseStmtTemp(w, scope, bsl)
	bodyPtr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", bodyPtr, bPtrSlot))
	bodyLen64 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", bodyLen64, bLenSlot))
	bodyLen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = trunc %s as i32\n", bodyLen, bodyLen64))
	rst := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 20\n", rst))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", rst, status))
	w.Write(fmt.Sprintf("  store %s + 4, %s as i32\n", rst, ok))
	w.Write(fmt.Sprintf("  store %s + 8, %s as ptr\n", rst, bodyPtr))
	w.Write(fmt.Sprintf("  store %s + 16, %s as i32\n", rst, bodyLen))
	rf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_http_client_req_free(%s)\n", rf, req))
	saReleaseStmtTemp(w, scope, rf)
	cf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_http_client_free(%s)\n", cf, client))
	saReleaseStmtTemp(w, scope, cf)
	w.Write(fmt.Sprintf("  !%s\n", clientSlot))
	w.Write(fmt.Sprintf("  !%s\n", reqSlot))
	w.Write(fmt.Sprintf("  !%s\n", respSlot))
	w.Write(fmt.Sprintf("  !%s\n", bPtrSlot))
	w.Write(fmt.Sprintf("  !%s\n", bLenSlot))
	saOwnTemp(scope, rst)
	return rst, false, ""
}

// saLowerResponseText lowers response.text() (0 args): a fresh str handle
// {bodyPtr, bodyLen-as-u64} over the response buffer (which the Response
// struct keeps alive by not freeing it).
func saLowerResponseText(w printer.EmitTextWriter, recv string, ce *ast.CallExpression, scope *saScope, nextTemp *int) (string, bool, string, bool) {
	if ce == nil || ce.Expression == nil {
		return "", false, "", false
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 0 {
		return "", false, "Response.text takes no arguments", true
	}
	bp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as ptr\n", bp, recv))
	bl32 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 16 as i32\n", bl32, recv))
	bl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = zext %s as u64\n", bl, bl32))
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", h, bp))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", h, bl))
	saOwnTemp(scope, h)
	return h, false, "", true
}

// saIsJsonCall reports whether ce is `response.json()` on an
// inst:Response identifier receiver (non-optional, 0 args checked by callers).
func saIsJsonCall(ce *ast.CallExpression, scope *saScope) bool {
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Text() != "json" || pa.QuestionDotToken != nil || ce.QuestionDotToken != nil {
		return false
	}
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
		return false
	}
	return scope.types[pa.Expression.Text()] == "inst:Response"
}

// saJsonCatchMsg validates a .catch() call on a json() result: exactly 1
// argument, a zero-parameter arrow/function (callbacks with parameters
// keep refusing loudly). Returns "" or the refusal message.
func saJsonCatchMsg(ce *ast.CallExpression) string {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	if len(argNodes) != 1 {
		return "json catch takes 1 argument"
	}
	cb := argNodes[0]
	nparams := -1
	if cb != nil && cb.Kind == ast.KindArrowFunction {
		if af := cb.AsArrowFunction(); af != nil {
			if af.Parameters == nil {
				nparams = 0
			} else {
				nparams = len(af.Parameters.Nodes)
			}
		}
	} else if cb != nil && cb.Kind == ast.KindFunctionExpression {
		if fe := cb.AsFunctionExpression(); fe != nil {
			if fe.Parameters == nil {
				nparams = 0
			} else {
				nparams = len(fe.Parameters.Nodes)
			}
		}
	}
	if nparams != 0 {
		return "json catch callbacks with parameters are not lowerable (zero-parameter fallbacks only)"
	}
	return ""
}

// saJsonValueOf peels transparent wrappers + await layers around e and
// reports the inner json value: (jsonCall, hasCatch, msg). msg != "" means
// a loud refusal (catch arity / callback shape). (nil, false, "") means e
// is not a json value at all (caller falls through).
func saJsonValueOf(e *ast.Node, scope *saScope) (*ast.CallExpression, bool, string) {
	e = saUnwrapTransparent(e)
	for e != nil && e.Kind == ast.KindAwaitExpression {
		e = saUnwrapTransparent(e.AsAwaitExpression().Expression)
	}
	if e == nil || e.Kind != ast.KindCallExpression {
		return nil, false, ""
	}
	ce := e.AsCallExpression()
	if saIsJsonCall(ce, scope) {
		return ce, false, ""
	}
	if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa != nil && pa.Name() != nil && pa.Name().Text() == "catch" && pa.QuestionDotToken == nil && ce.QuestionDotToken == nil &&
			pa.Expression != nil && pa.Expression.Kind == ast.KindCallExpression &&
			saIsJsonCall(pa.Expression.AsCallExpression(), scope) {
			var argNodes []*ast.Node
			if ce.Arguments != nil {
				argNodes = ce.Arguments.Nodes
			}
			if len(argNodes) != 1 {
				return nil, false, "json catch takes 1 argument"
			}
			cb := argNodes[0]
			nparams := -1
			if cb != nil && cb.Kind == ast.KindArrowFunction {
				if af := cb.AsArrowFunction(); af != nil {
					if af.Parameters == nil {
						nparams = 0
					} else {
						nparams = len(af.Parameters.Nodes)
					}
				}
			} else if cb != nil && cb.Kind == ast.KindFunctionExpression {
				if fe := cb.AsFunctionExpression(); fe != nil {
					if fe.Parameters == nil {
						nparams = 0
					} else {
						nparams = len(fe.Parameters.Nodes)
					}
				}
			}
			if nparams != 0 {
				return nil, false, "json catch callbacks with parameters are not lowerable (zero-parameter fallbacks only)"
			}
			return pa.Expression.AsCallExpression(), true, ""
		}
	}
	return nil, false, ""
}

// saLowerJsonNode parses a Response body's slice into a u64 node handle
// (0 on parse failure). The node is a plain value, never owned.
func saLowerJsonNode(w printer.EmitTextWriter, jsonCall *ast.CallExpression, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/encoding/json.sai")
	pa := jsonCall.Expression.AsPropertyAccessExpression()
	recv := pa.Expression.Text()
	bp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as ptr\n", bp, recv))
	bl32 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 16 as i32\n", bl32, recv))
	bl := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = zext %s as u64\n", bl, bl32))
	node := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_json_parse(&%s, %s)\n", node, bp, bl))
	return node
}

// saLowerJsonCatchValue lowers `response.json().catch(cb)`: parse, then a
// slot join on node != 0 (success -> node, failure -> 0; the zero-param
// fallback body is not evaluated).
func saLowerJsonCatchValue(w printer.EmitTextWriter, jsonCall *ast.CallExpression, scope *saScope, nextTemp *int) string {
	node := saLowerJsonNode(w, jsonCall, scope, nextTemp)
	nz := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", nz, node))
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	okL := fmt.Sprintf("L_json_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	failL := fmt.Sprintf("L_json_fail_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_json_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", nz, okL, failL))
	w.Write(fmt.Sprintf("%s:\n", okL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as u64\n", slot, node))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", failL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as u64\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", res, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return res
}

type saJsonMember struct {
	name string
	kind string
}

// saJsonCastMembers extracts the scalar members of a cast type: (members,
// hasLayout). TypeLiteral casts always build a layout (bool/string/number
// members; number -> f64); TypeReference casts use a claimed interface
// layout's field kinds (str/bool/i32/f64 only); anything else falls through
// (hasLayout=false, caller keeps the transparent as-cast behavior).
func saJsonCastMembers(tn *ast.TypeNode, scope *saScope) ([]saJsonMember, bool) {
	if tn == nil {
		return nil, false
	}
	if tn.Kind == ast.KindTypeLiteral {
		var members []saJsonMember
		tl := tn.AsTypeLiteralNode()
		if tl == nil || tl.Members == nil {
			return members, true
		}
		for _, m := range tl.Members.Nodes {
			if m == nil || m.Kind != ast.KindPropertySignature {
				continue
			}
			fn := m.Name()
			if fn == nil || fn.Kind != ast.KindIdentifier {
				continue
			}
			pd := m.AsPropertySignatureDeclaration()
			if pd == nil || pd.Type == nil {
				continue
			}
			switch pd.Type.Kind {
			case ast.KindBooleanKeyword:
				members = append(members, saJsonMember{fn.Text(), "bool"})
			case ast.KindStringKeyword:
				members = append(members, saJsonMember{fn.Text(), "str"})
			case ast.KindNumberKeyword:
				members = append(members, saJsonMember{fn.Text(), "f64"})
			}
		}
		return members, true
	}
	if tn.Kind == ast.KindTypeReference {
		ref := tn.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil {
			return nil, false
		}
		def, ok := scope.classes[ref.TypeName.Text()]
		if !ok || def == nil {
			return nil, false
		}
		var members []saJsonMember
		for _, f := range def.fields {
			switch def.fkinds[f.name] {
			case "str", "bool", "i32", "f64":
				members = append(members, saJsonMember{f.name, def.fkinds[f.name]})
			}
		}
		return members, true
	}
	return nil, false
}

// saLowerJsonCast extracts a json node's scalar members into a fresh
// synthetic inst layout and returns (layoutName, structTemp). The struct
// temp is saOwnTemp'd.
func saLowerJsonCast(w printer.EmitTextWriter, node string, members []saJsonMember, scope *saScope, nextTemp *int) (string, string) {
	scope.addImport("sa_std/encoding/json.sai")
	lname := fmt.Sprintf("__JsonCast_%d", *scope.nextLabel)
	*scope.nextLabel++
	offsets := map[string]int{}
	fkinds := map[string]string{}
	off := 0
	for _, m := range members {
		off = saAlignOff(off, m.kind)
		offsets[m.name] = off
		fkinds[m.name] = m.kind
		width, _ := saFieldWidth(m.kind)
		off += width
	}
	fields := make([]saClassField, 0, len(members))
	for _, m := range members {
		fields = append(fields, saClassField{name: m.name, offset: offsets[m.name]})
	}
	scope.classes[lname] = &saClassDef{
		name: lname, fields: fields, offsets: offsets, fkinds: fkinds,
		fsub: map[string]string{}, fopt: map[string]bool{}, fpos: map[string]int{},
		methods: map[string]*ast.Node{}, getters: map[string]*ast.Node{},
		setters: map[string]*ast.Node{}, statics: map[string]saStaticVal{},
		size: off, isSynth: true,
	}
	st := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc %d\n", st, off))
	for _, m := range members {
		kh := saLowerStringLiteral(w, m.name, scope, nextTemp)
		kbp, kbl := saExpandStr(w, kh, nextTemp)
		switch m.kind {
		case "bool":
			slot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
			gr := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_json_object_get_bool(%s, &%s, %s, &%s)\n", gr, node, kbp, kbl, slot))
			saReleaseStmtTemp(w, scope, gr)
			v8 := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as u8\n", v8, slot))
			v := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = zext %s as i32\n", v, v8))
			w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", st, offsets[m.name], v))
			w.Write(fmt.Sprintf("  !%s\n", slot))
		case "str":
			pslot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", pslot))
			lslot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", lslot))
			gr := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_json_object_get_string(%s, &%s, %s, &%s, &%s)\n", gr, node, kbp, kbl, pslot, lslot))
			saReleaseStmtTemp(w, scope, gr)
			p := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", p, pslot))
			l := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", l, lslot))
			h := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", h, p))
			w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", h, l))
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", st, offsets[m.name], h))
			w.Write(fmt.Sprintf("  !%s\n", pslot))
			w.Write(fmt.Sprintf("  !%s\n", lslot))
		case "f64":
			slot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
			gr := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_json_object_get_f64(%s, &%s, %s, &%s)\n", gr, node, kbp, kbl, slot))
			saReleaseStmtTemp(w, scope, gr)
			v := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as f64\n", v, slot))
			w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", st, offsets[m.name], v))
			w.Write(fmt.Sprintf("  !%s\n", slot))
		case "i32":
			slot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
			gr := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_json_object_get_i64(%s, &%s, %s, &%s)\n", gr, node, kbp, kbl, slot))
			saReleaseStmtTemp(w, scope, gr)
			v64 := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as i64\n", v64, slot))
			v := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = trunc %s as i32\n", v, v64))
			w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", st, offsets[m.name], v))
			w.Write(fmt.Sprintf("  !%s\n", slot))
		}
	}
	saOwnTemp(scope, st)
	return lname, st
}

// saLowerStdOpaqueNew lowers `new X(...)` for an opaque std class to a fresh
// jsval box (kind 4 object, null payload) via the existing @ts_jsval_box_ptr
// helper (no new inline helper). Constructor args are side-effect evaluated
// per saStdOpaqueEvalArg; the box itself carries no observable state.
func saLowerStdOpaqueNew(w printer.EmitTextWriter, name string, ce *ast.NewExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	for _, a := range argNodes {
		if msg := saStdOpaqueEvalArg(w, a, scope, pos, refusals, nextTemp); msg != "" {
			return "", msg
		}
	}
	h := saBoxJsValStd(w, scope, "ptr", 4, "0", nextTemp)
	saOwnTemp(scope, h)
	scope.types[h] = "jsval"
	return h, ""
}

// saLowerStdOpaqueMethod lowers a method call on an identifier bound by an
// opaque ctor. Returns (op, voidCall, msg, handled): unhandled methods fall
// through to the existing refusal arms; handled methods with bad args refuse
// with their own message (no fall-through to a confusing second refusal).
func saLowerStdOpaqueMethod(w printer.EmitTextWriter, class, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string, bool) {
	res, ok := saStdOpaqueMethod(class, method)
	if !ok {
		return "", false, "", false
	}
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	want := -1
	switch method {
	case "abort":
		want = 0
	case "delete":
		want = 1
	case "append", "set":
		want = 2
	case "encode":
		want = 1
	}
	if want >= 0 && len(argNodes) != want {
		return "", false, class + "." + method + " takes " + fmt.Sprintf("%d", want) + " arguments", true
	}
	if res == "jsval" {
		// TextEncoder.encode(str): evaluate the string arg, discard it, and
		// return a fresh opaque jsval box (the bytes are unobserved here).
		h, msg := saEvalStr(w, argNodes[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg, true
		}
		if saIsTempOp(h) {
			saReleaseStmtTemp(w, scope, h)
		}
		box := saBoxJsValStd(w, scope, "ptr", 4, "0", nextTemp)
		saOwnTemp(scope, box)
		scope.types[box] = "jsval"
		return box, false, "", true
	}
	for _, a := range argNodes {
		if msg := saStdOpaqueEvalArg(w, a, scope, pos, refusals, nextTemp); msg != "" {
			return "", false, msg, true
		}
	}
	return "", true, "", true
}

// saRecordClass 记录类定义（布局 + 构造 + 方法；无码。重复类名/非法成员拒）。
func saRecordClass(st *ast.Node, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) bool {
	return saRecordClassNamed(st, "", false, "", classes, pos, refusals)
}

// saRecordStaticAssign 记录期执行静态块单句（`C.n = <declit>`；本类已声明
// i32 字段折叠更新；他类/前向/非字面量/非赋值沿旧门大声拒）。
func saRecordStaticAssign(def *saClassDef, owner string, s *ast.Node, pos func(int) (int, int), refusals *[]SARefusal) bool {
	refuse := func(msg string) bool {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false
	}
	if s == nil || s.Kind != ast.KindExpressionStatement {
		return refuse("class static blocks lower only C.field = decimal integer literal assignments")
	}
	ex := s.AsExpressionStatement().Expression
	if ex == nil || ex.Kind != ast.KindBinaryExpression {
		return refuse("class static blocks lower only C.field = decimal integer literal assignments")
	}
	bin := ex.AsBinaryExpression()
	if bin.OperatorToken == nil || bin.OperatorToken.Kind != ast.KindEqualsToken ||
		bin.Left == nil || bin.Left.Kind != ast.KindPropertyAccessExpression ||
		bin.Right == nil || bin.Right.Kind != ast.KindNumericLiteral || !saIsDecIntLit(bin.Right.Text()) {
		return refuse("class static blocks lower only C.field = decimal integer literal assignments")
	}
	pa := bin.Left.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier ||
		pa.Expression.Text() != owner || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return refuse("static block writes must target own class fields (C.f = ...)")
	}
	fname := pa.Name().Text()
	sv, ok := def.statics[fname]
	if !ok || sv.kind != "i32" {
		return refuse("static block writes need a declared i32 static field above the block")
	}
	sv.text = bin.Right.Text()
	def.statics[fname] = sv
	return true
}

// saDottedBaseName 把 `N.C` / `A.B.C` 限定基展平为下划线路径（与 `N_C`
// 布局键同形；非标识段一律 false）。
// 形状证据：封存 dottedBaseName:76-92（段收集 + Join 下划线）。
func saDottedBaseName(n *ast.Node) (string, bool) {
	var segs []string
	cur := n
	for cur != nil && cur.Kind == ast.KindPropertyAccessExpression {
		pa := cur.AsPropertyAccessExpression()
		if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			return "", false
		}
		segs = append([]string{pa.Name().Text()}, segs...)
		cur = pa.Expression
	}
	if cur == nil || cur.Kind != ast.KindIdentifier {
		return "", false
	}
	segs = append([]string{cur.Text()}, segs...)
	return strings.Join(segs, "_"), true
}

// saRecordClassNamed 记录类定义（声明与表达式同形；forceName 供
// `const C = class...` 绑定名；aliasOwn 为真时自身具名（`class E`）另记同体
// 别名，为假时仅记 forceName（命名空间成员限定名，不得泄漏成员名到外层域；
// 封存 inner 名泄漏 gap 仅适用于同域别名）；nsScope 非空时 heritage 基先查
// 本名、次查 `nsScope_基`（同 ns 内继承；parent 存限定名，下游表查贯通）。
// 字段初值表达式忽略（布局只记槽位；封存 recordClassNamed:9691-9743
// 不读 Initializer，初值不求值）。
func saRecordClassNamed(st *ast.Node, forceName string, aliasOwn bool, nsScope string, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) bool {
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
	// 泛型形参模板直存（接口 saRecordIface 同形；参数属性 `v: T` 擦除查表用）。
	for _, tp := range st.TypeParameters() {
		if tp == nil || tp.Kind != ast.KindTypeParameter {
			continue
		}
		if nm := tp.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			def.tparams = append(def.tparams, nm.Text())
		}
	}
	ownFields := map[string]bool{}
	ownMethods := map[string]bool{}
	// 单继承：基布局字段追加在下（父偏移守恒），方法按名拷贝（子类覆写）；
	// implements 子句整体擦除（纯类型零运行时，永不拒因；形状证据：封存
	// parseHeritage:42-83 + inheritClass:88-215 + class_heritage.go:16）。
	if hc := heritage; hc != nil {
		for _, h := range hc.Nodes {
			if h.Kind != ast.KindHeritageClause {
				continue
			}
			if h.AsHeritageClause().Token != ast.KindExtendsKeyword {
				// 4_if_flow (JEV record_implements_args 0.95): implements
				// clauses are recorded, not erased, so that an instance
				// argument can be accepted where an interface parameter is
				// expected. Only the name is kept (type arguments stripped,
				// dotted names flattened like the extends base); unknown or
				// unrecorded interfaces simply never match, so recording
				// alone changes no acceptance. Field/method layout is
				// untouched: implements still contributes no fields.
				if h.AsHeritageClause().Token == ast.KindImplementsKeyword {
					if types := h.AsHeritageClause().Types; types != nil {
						for _, el := range types.Nodes {
							inm := ""
							if el.Kind == ast.KindExpressionWithTypeArguments {
								if ex := el.AsExpressionWithTypeArguments().Expression; ex != nil {
									if ex.Kind == ast.KindIdentifier {
										inm = ex.Text()
									} else if q, ok := saDottedBaseName(ex); ok {
										inm = q
									}
								}
							} else if el.Kind == ast.KindIdentifier {
								inm = el.Text()
							} else if q, ok := saDottedBaseName(el); ok {
								inm = q
							}
							if inm != "" {
								def.implements = append(def.implements, inm)
							}
						}
					}
				}
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
				if ex := el.AsExpressionWithTypeArguments().Expression; ex != nil {
					if ex.Kind == ast.KindIdentifier {
						base = ex.Text()
					} else if q, ok := saDottedBaseName(ex); ok {
						// 限定基 `extends N.C` 展平为命名空间路径（与 `N_C`
						// 布局键同形；形状证据：封存 dottedBaseName:76-92）。
						base = q
					}
				}
			} else if el.Kind == ast.KindIdentifier {
				base = el.Text()
			} else if q, ok := saDottedBaseName(el); ok {
				base = q
			}
			if base == "" {
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class extends needs a plain base class name (mixins are not lowerable)"})
				return false
			}
			if def.parent != "" {
				// 多基系整句问题（子句节点位错位，见多基探针；指类声明位）。
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class extends needs exactly one base class"})
				return false
			}
			bdef, ok := classes[base]
			if !ok {
				// Synthetic std base (Error/EventTarget/DOMException): seed on
				// first extends use; user declarations recorded earlier win.
				if saSeedStdBase(classes, base) {
					bdef, ok = classes[base]
				}
			}
			qbase := base
			if (!ok || bdef.isIface) && nsScope != "" {
				if qb, ok2 := classes[nsScope+"_"+base]; ok2 && !qb.isIface {
					bdef, ok, qbase = qb, true, nsScope+"_"+base
				}
			}
			if !ok || bdef.isIface {
				// 未知基拒因指 extends 子句基位（与 implements 未知接口子句位同形）。
				ln, col := pos(el.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class " + name + " extends unknown base " + base + " (declare the base class first)"})
				return false
			}
			for p := qbase; p != ""; {
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
			def.parent = qbase
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
		if m.Kind == ast.KindConstructor {
			// 参数装饰器在定义时执行任意代码，静默丢弃即改行为，大声拒
			//（封存 saemit.go:9616-9629 全形；拒因逐字对齐上游）。
			for _, p := range m.Parameters() {
				if p == nil || p.AsNode() == nil {
					continue
				}
				if len(p.AsNode().Decorators()) > 0 {
					ln, col := pos(p.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "parameter decorators are not lowerable (definition-time effects have no SA-ASM form)"})
					return false
				}
			}
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
			// 抽象属性记名（派生须声明同名域；占槽照常，基不实例化无害；
			// static+abstract 非法直接拒，与抽象存取器同形）。
			if ast.HasModifier(m, ast.ModifierFlagsAbstract) {
				if ast.HasModifier(m, ast.ModifierFlagsStatic) {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "abstract static fields are not lowerable"})
					return false
				}
				if def.abstracts == nil {
					def.abstracts = map[string]int{}
				}
				if def.abstractKinds == nil {
					def.abstractKinds = map[string]string{}
				}
				def.abstracts[fkey] = m.Pos()
				def.abstractKinds[fkey] = "field"
			}
			if ast.HasModifier(m, ast.ModifierFlagsStatic) {
				// 静态字面量折叠记表（不占实例槽；封存 recordClassNamed:9703-9711）；
				// 非字面静态初值记表期大声拒（旧路落实例槽但无 finit 登记，实例读静默
				// 得零，如 `static y = C.x+10` 读零 vs node 11；类名读已拒，实例径同
				// 立场；无初值静态沿旧路实例槽，与无初值实例域同形）。
				if text, kind, ok := saStaticLiteral(pd.Initializer); ok {
					if def.statics == nil {
						def.statics = map[string]saStaticVal{}
					}
					def.statics[fkey] = saStaticVal{text: text, kind: kind}
					continue
				}
				if pd.Initializer != nil {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "static field " + fkey + " initializer must be a literal (non-literal statics are not lowerable)"})
					return false
				}
			}
			// 字段初值表达式忽略（布局只记槽位，不求值；封存 recordClassNamed
			// 9691-9743 不读 Initializer；初值语义随 alloc，见 AGENTS step47）。
			// 字段种：i32/bool 恒 4 字节槽，string/arr（含函数类型与未记录用户类型）
			// 为头指针 8 字节槽，f64 为双精度 8 字节槽（封存 widthOf；余下无槽，拒）。
			fkind := "i32"
			if pd.Type != nil {
				if k, ok := saAnnotKind(pd.Type); ok && (k == "i32" || k == "bool" || k == "str" || k == "arr" || k == "f64") {
					if k == "str" {
						fkind = "str"
					} else if k == "arr" {
						fkind = "arr"
					} else if k == "f64" {
						fkind = "f64"
					}
				} else if pd.Type.Kind == ast.KindTypeReference && pd.Type.AsTypeReferenceNode() != nil && pd.Type.AsTypeReferenceNode().TypeName != nil && pd.Type.AsTypeReferenceNode().TypeName.Kind == ast.KindIdentifier {
					if sub, ok := classes[pd.Type.AsTypeReferenceNode().TypeName.Text()]; ok {
						fkind = "inst"
						if def.fsub == nil {
							def.fsub = map[string]string{}
						}
						def.fsub[fkey] = sub.name
					} else if pd.Type.AsTypeReferenceNode().TypeName.Text() == name {
						// 自引用字段（链表 `next: Node`）：类表尚未落子，按实例布局记种；
						// 宽与 arr 柄槽同为 8,8，布局零位移。
						fkind = "inst"
						if def.fsub == nil {
							def.fsub = map[string]string{}
						}
						def.fsub[fkey] = name
					} else {
						// 未记录用户类型（含泛型形参 `T`）落 ptr 句柄槽（8B；封存
						// saNameOfType:197-199 用户类型皆 ptr 句柄 + recordClassNamed:9713-9716
						// 无注解/未知皆槽位 + widthOf 默认 8,8；本仓句柄种为 arr）。
						fkind = "arr"
					}
				} else if pd.Type.Kind == ast.KindFunctionType {
					// 函数类型字段落 ptr 句柄槽（构造捕获箭头逐实例记表，去虚化回放；
					// 封存 recordClassNamed:9713-9716 + wireCtorFieldStore:10022-10033）。
					fkind = "arr"
				} else if qn, ok := saQualifiedTypeName(pd.Type); ok {
					// 限定引用字段 `f: N.C`（与 `N_C` 布局键同形；未记录沿旧门）。
					if sub, ok := classes[qn]; ok {
						fkind = "inst"
						if def.fsub == nil {
							def.fsub = map[string]string{}
						}
						def.fsub[fkey] = sub.name
					} else {
						ln, col := pos(m.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32, string, array or recorded layout"})
						return false
					}
				} else if pd.Type.Kind == ast.KindTypeLiteral {
					// 内联对象类型字段（`f: {a: number}`）：按类型字面量合成布局
					// （复用 saSynthAnonLayout 纪律）。
					if lname, ok := saSynthAnonLayout(pd.Type, classes); ok {
						fkind = "inst"
						if def.fsub == nil {
							def.fsub = map[string]string{}
						}
						def.fsub[fkey] = lname
					} else {
						ln, col := pos(m.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32, string, array or recorded layout"})
						return false
					}
				} else if pd.Type.Kind == ast.KindUnionType {
					// union-typed fields lower as ptr handle slots (cf saNameOfType union default).
					// 单类+空联合（`next: Node | null`）按实例布局记种（与 TypeReference
					// 同律：fkind 置 inst + fsub 登记，读位经布局消解；maybe-null 直读
					// 与调用返回值同口径（假设非空，空即运行时 trap，与 JS TypeError
					// 同形；确定空仍由 const-null 门大声拒）；`?.` 守卫读位凭此解析布局。
					if inst, ok := saUnionInstKind(pd.Type.AsUnionTypeNode(), classes); ok {
						fkind = "inst"
						if def.fsub == nil {
							def.fsub = map[string]string{}
						}
						def.fsub[fkey] = inst[5:]
					} else if self, ok := saUnionSelfKind(pd.Type.AsUnionTypeNode(), name); ok {
						// 自引用可空联合（`next: Node | null`）：类表尚未落子，按实例布局记种；
						// 宽与 arr 柄槽同为 8,8，布局零位移；maybe-null 直读与调用
						// 返回值同口径（假设非空），`?.` 守卫读位凭此解析布局。
						fkind = "inst"
						if def.fsub == nil {
							def.fsub = map[string]string{}
						}
						def.fsub[fkey] = self
					} else {
						fkind = "arr"
					}
				} else if pd.Type.Kind == ast.KindAnyKeyword || pd.Type.Kind == ast.KindUnknownKeyword {
					// `any` fields lower as ptr handle slots (cf saNameOfType unknown default).
					fkind = "arr"
				} else {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32, string, array or recorded layout"})
					return false
				}
			}
			if pd.Type == nil {
				// 2b_tmpl: an unannotated field whose initializer is a
				// string literal (duck ToolSimulationStream.pending =
				// "") is a str field, not the i32 default; finit
				// lowering already has a str case (saLowerFieldInits).
				// Other initializers keep the i32 default.
				if init := saUnwrapTransparent(pd.Initializer); init != nil && (init.Kind == ast.KindStringLiteral || init.Kind == ast.KindNoSubstitutionTemplateLiteral) {
					fkind = "str"
				}
			}
			if pd.Initializer != nil && !ast.HasModifier(m, ast.ModifierFlagsStatic) {
				// 实例字段初值登记（构造时基先派后、先于构造体落槽；ES2022
				// class fields 语义，见 internal/transformers/estransforms/classfields.go）。
				if def.finits == nil {
					def.finits = map[string]*ast.Node{}
				}
				def.finits[fkey] = pd.Initializer
				def.finitOrder = append(def.finitOrder, fkey)
			}
			if _, dup := def.offsets[fkey]; dup {
				// 继承字段重声明：守基偏移（同宽同种恒成立）。
				// 形状证据：封存 recordClassNamed:9717-9733。
				if def.parent == "" || ownFields[fkey] {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate field " + fkey})
					return false
				}
				// 继承字段重声明即实现意图（抽象属性实现判定用）。
				ownFields[fkey] = true
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
			// 重载签名擦除：无体构造声明不注册（实现体唯一定义；方法重载
			// 554-569 同例；双实现体仍沿旧门拒）。
			if m.BodyData() == nil || m.BodyData().Body == nil {
				continue
			}
			if def.ctor != nil {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate constructor"})
				return false
			}
			def.ctor = m
		case ast.KindMethodDeclaration:
			mn := m.Name()
			if mn == nil || mn.Kind != ast.KindIdentifier {
				// 计算/私有方法名跳过（类其余成员照常；调用点沿未知方法门
				// 大声拒；形状证据：封存上游计算名擦除 + 私有方法跳过同形）。
				continue
			}
			// 重载签名擦除：无体声明不注册（实现体唯一定义；顶层同例见 step41；
			// 形状证据：封存 recordClassNamed:9746-9771 无重复检查直接覆盖（签名被实现覆盖）+ saemit.go:983 无体拒止于调用点）。
			// 抽象方法记名不占实现（派生非抽象类须实现；与重载擦除同位，abstract 优先）。
			if m.BodyData() == nil || m.BodyData().Body == nil {
				if ast.HasModifier(m, ast.ModifierFlagsAbstract) {
					if def.abstracts == nil {
						def.abstracts = map[string]int{}
					}
					if def.abstractKinds == nil {
						def.abstractKinds = map[string]string{}
					}
					def.abstracts[mn.Text()] = m.Pos()
					def.abstractKinds[mn.Text()] = "method"
				}
				continue
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
			// 抽象存取器记名不占实现（派生须同形实现；static+abstract 非法直接拒）。
			if ast.HasModifier(m, ast.ModifierFlagsAbstract) {
				if ast.HasModifier(m, ast.ModifierFlagsStatic) {
					ln, col := pos(m.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "abstract static accessors are not lowerable"})
					return false
				}
				if def.abstracts == nil {
					def.abstracts = map[string]int{}
				}
				if def.abstractKinds == nil {
					def.abstractKinds = map[string]string{}
				}
				def.abstracts[an.Text()] = m.Pos()
				if m.Kind == ast.KindGetAccessor {
					def.abstractKinds[an.Text()] = "get"
				} else {
					def.abstractKinds[an.Text()] = "set"
				}
				continue
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
			continue
		case ast.KindClassStaticBlockDeclaration:
			// 静态块记录期执行（定义点顺序语义；记录先于一切 lowering，
			// 后续读折叠即新值；仅收已声明本类 i32 字段十进制字面量写；
			// 前向/他类/余形沿旧门大声拒）。
			sb := m.AsClassStaticBlockDeclaration()
			if sb == nil || sb.Body == nil {
				ln, col := pos(m.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class static blocks lower only C.field = decimal integer literal assignments"})
				return false
			}
			for _, s := range sb.Body.Statements() {
				if !saRecordStaticAssign(def, name, s, pos, refusals) {
					return false
				}
			}
			continue
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
		if !saRecordParamPropFields(def, def.ctor, &off, classes, pos, refusals) {
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
	// implements 子句整体擦除（纯类型零运行时，永不拒因；未知/后置/限定名/缺成员一律放行，使用位拒词；封存 class_heritage.go:16 + saemit.go:9537/9670）。
	// 抽象方法实现校验（非抽象派生类须实现祖先链全部抽象方法；自身抽象跳过；
	// 拒因指抽象声明位；继承展平已含中间实现，只查自身 methods）。
	if !def.isAbstract {
		need := map[string]int{}
		needKind := map[string]string{}
		for p := def.parent; p != ""; {
			pb := classes[p]
			if pb == nil {
				break
			}
			for n, pp := range pb.abstracts {
				if _, ok := need[n]; !ok {
					need[n] = pp
					if kk, ok := pb.abstractKinds[n]; ok {
						needKind[n] = kk
					} else {
						needKind[n] = "method"
					}
				}
			}
			p = pb.parent
		}
		for n, pp := range need {
			kk := needKind[n]
			satisfied := false
			switch kk {
			case "get":
				_, satisfied = def.getters[n]
			case "set":
				_, satisfied = def.setters[n]
			case "field":
				// 抽象属性须自有声明（继承展平使 offsets 恒真，查 ownFields）。
				_, satisfied = ownFields[n]
			default:
				_, satisfied = def.methods[n]
			}
			if !satisfied {
				ln, col := pos(pp)
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class " + name + " must implement abstract " + n + "()"})
				return false
			}
		}
	}
	classes[name] = def
	// 自身具名（`const D = class E`）记同体别名（值对；封存 inner 名泄漏 gap
	// 即此语义；别名冲突诚实拒）。命名空间成员（aliasOwn 假）不记外层别名。
	if aliasOwn && forceName != "" {
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
func saRecordParamPropFields(def *saClassDef, ctor *ast.Node, off *int, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal) bool {
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
		if pd == nil {
			ln, col := pos(p.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "parameter property " + fname + " needs a type annotation (slot width)"})
			return false
		}
		if pd.Type == nil {
			// (par_notexp) 无标注参数属性：槽宽由字面量初值推断
			// （整数 i32、串 str、布尔 bool；余形仍大声拒）。
			init := saUnwrapTransparent(pd.Initializer)
			fkind := ""
			if init != nil {
				switch init.Kind {
				case ast.KindNumericLiteral:
					if !saIsFloatLit(init.Text()) {
						fkind = "i32"
					}
				case ast.KindPrefixUnaryExpression:
					if un := init.AsPrefixUnaryExpression(); un != nil && un.Operand != nil && un.Operand.Kind == ast.KindNumericLiteral && !saIsFloatLit(un.Operand.Text()) && (un.Operator == ast.KindMinusToken || un.Operator == ast.KindPlusToken) {
						fkind = "i32"
					}
				case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
					fkind = "str"
				case ast.KindTrueKeyword, ast.KindFalseKeyword:
					fkind = "bool"
				}
			}
			if fkind == "" {
				ln, col := pos(p.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "parameter property " + fname + " needs a type annotation (slot width)"})
				return false
			}
			*off = saAlignOff(*off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: *off})
			def.offsets[fname] = *off
			def.fkinds[fname] = fkind
			sz, _ := saFieldWidth(fkind)
			*off += sz
			continue
		}
		k, ok := saAnnotKind(pd.Type)
		if ok && (k == "i32" || k == "bool" || k == "str" || k == "arr" || k == "f64") {
			fkind := "i32"
			if k == "str" {
				fkind = "str"
			} else if k == "arr" {
				fkind = "arr"
			} else if k == "f64" {
				fkind = "f64"
			}
			*off = saAlignOff(*off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: *off})
			def.offsets[fname] = *off
			def.fkinds[fname] = fkind
			sz, _ := saFieldWidth(fkind)
			*off += sz
			continue
		}
		// (par_notexp) 函数类型参数属性落指针句柄槽（与类字段门同形）。
		if pd.Type.Kind == ast.KindFunctionType {
			*off = saAlignOff(*off, "arr")
			def.fields = append(def.fields, saClassField{name: fname, offset: *off})
			def.offsets[fname] = *off
			def.fkinds[fname] = "arr"
			sz, _ := saFieldWidth("arr")
			*off += sz
			continue
		}
		// 布局表内类/接口名嵌套句柄（与接口门/类字段门同形）。
		if pd.Type.Kind == ast.KindTypeReference && pd.Type.AsTypeReferenceNode() != nil && pd.Type.AsTypeReferenceNode().TypeName != nil && pd.Type.AsTypeReferenceNode().TypeName.Kind == ast.KindIdentifier {
			if sub, ok := classes[pd.Type.AsTypeReferenceNode().TypeName.Text()]; ok {
				if def.fsub == nil {
					def.fsub = map[string]string{}
				}
				def.fsub[fname] = sub.name
				*off = saAlignOff(*off, "inst")
				def.fields = append(def.fields, saClassField{name: fname, offset: *off})
				def.offsets[fname] = *off
				def.fkinds[fname] = "inst"
				sz, _ := saFieldWidth("inst")
				*off += sz
				continue
			}
			// 泛型形参擦除为 ptr 句柄槽（`v: T`；上游具化值直存字面量，
			// 本仓 wiring 经 i32 求值后按槽存；非形参未知名沿旧门）。
			erased := false
			for _, tp := range def.tparams {
				if tp == pd.Type.AsTypeReferenceNode().TypeName.Text() {
					erased = true
					break
				}
			}
			if erased {
				*off = saAlignOff(*off, "arr")
				def.fields = append(def.fields, saClassField{name: fname, offset: *off})
				def.offsets[fname] = *off
				def.fkinds[fname] = "arr"
				sz, _ := saFieldWidth("arr")
				*off += sz
				continue
			}
		}
		ln, col := pos(p.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "class fields must be i32, string, array or recorded layout"})
		return false
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

// saProgFindClass 在全部已收割文件里按名找类布局（文件名排序，确定性
// first-hit；多文件同名异布局属病态输入，用点门照常大声）。
func saProgFindClass(link *saFileLink, name string) *saClassDef {
	if link == nil || link.classHarvests == nil {
		return nil
	}
	files := make([]string, 0, len(link.classHarvests))
	for f := range link.classHarvests {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if ch, ok := link.classHarvests[f][name]; ok && ch.exported && ch.def != nil {
			return ch.def
		}
	}
	return nil
}

// saPreseedImportedAliases 在类型预扫前把具名导入的类型别名播进本文件注解表
// （普通与纯类型导入均播；类型无运行时，本地同名别名优先；未导出不播）。
func saPreseedImportedAliases(sf *ast.SourceFile, aliasOf map[string]*ast.TypeNode, link *saFileLink) {
	if sf == nil || link == nil || link.specOf == nil || link.aliasHarvests == nil {
		return
	}
	local := map[string]bool{}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st != nil && st.Kind == ast.KindTypeAliasDeclaration {
			if nm := st.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				local[nm.Text()] = true
			}
		}
	}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st == nil || st.Kind != ast.KindImportDeclaration {
			continue
		}
		imp := st.AsImportDeclaration()
		if imp == nil || imp.ImportClause == nil {
			continue
		}
		ms := imp.ModuleSpecifier
		if ms == nil || ms.Kind != ast.KindStringLiteral {
			continue
		}
		tgt, ok := link.specOf[ms.Text()]
		if !ok || tgt == "" {
			// Bare workspace-package type harvest (JEV type_harvest_only):
			// the spec has no program edge, but its defining file may
			// still yield the named alias node.
			saBareImportLoop(link, imp, ms.Text(), sf, func(local, kind string, def *saClassDef, node *ast.TypeNode) {
				if kind == "alias" && node != nil {
					if _, dup := aliasOf[local]; !dup {
						aliasOf[local] = node
					}
				}
			})
			continue
		}
		clause := imp.ImportClause.AsImportClause()
		if clause == nil {
			continue
		}
		nb := clause.NamedBindings
		if nb == nil || nb.Kind != ast.KindNamedImports {
			continue
		}
		ni := nb.AsNamedImports()
		if ni == nil || ni.Elements == nil {
			continue
		}
		for _, n := range ni.Elements.Nodes {
			if n == nil || n.Kind != ast.KindImportSpecifier {
				continue
			}
			sp := n.AsImportSpecifier()
			nm := n.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			name := nm.Text()
			remote := name
			if sp.PropertyName != nil {
				remote = sp.PropertyName.Text()
			}
			if local[name] {
				continue
			}
			if _, dup := aliasOf[name]; dup {
				continue
			}
			patgt := tgt
			if _, ok := link.aliasHarvests[patgt][remote]; !ok {
				if st, ok := saProgStarTarget(link, tgt, remote, map[string]bool{}, func(t, r string) bool {
					a, ok := link.aliasHarvests[t][r]
					return ok && a.exported && a.node != nil
				}); ok {
					patgt = st
				}
			}
			if ah, ok := link.aliasHarvests[patgt][remote]; ok && ah.exported && ah.node != nil {
				aliasOf[name] = ah.node
			}
			if _, ok := aliasOf[name]; !ok && !saBareLocalDecl(sf, name) {
				if kind, _, node, ok := saBareHarvestLookup(link, ms.Text(), remote); ok && kind == "alias" && node != nil {
					aliasOf[name] = node
				}
			}
		}
	}
}

// saPreseedImportedClasses 在类型预扫前把具名导入的类布局播进本文件表
// （跨文件 heritage 记录期须见基布局，祖先链传递播种；本地同名定义优先，
// 调用方沿既有 duplicate 门；默认/命名空间成员类沿旧门后阶段）。
// 形状证据：封存 program.go:460-500 leaves-first 共享 classDefs 预扫 +
// TransitiveHeritage 三文件链（基布局先行，派生复用）。
func saPreseedImportedClasses(sf *ast.SourceFile, classes map[string]*saClassDef, link *saFileLink) {
	if sf == nil || link == nil || link.specOf == nil || link.classHarvests == nil {
		return
	}
	local := map[string]bool{}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st != nil && st.Kind == ast.KindClassDeclaration {
			if nm := st.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				local[nm.Text()] = true
			}
		}
	}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st == nil || st.Kind != ast.KindImportDeclaration {
			continue
		}
		imp := st.AsImportDeclaration()
		if imp == nil || imp.ImportClause == nil {
			continue
		}
		ms := imp.ModuleSpecifier
		if ms == nil || ms.Kind != ast.KindStringLiteral {
			continue
		}
		tgt, ok := link.specOf[ms.Text()]
		if !ok || tgt == "" {
			// Bare workspace-package type harvest (JEV type_harvest_only):
			// the spec has no program edge, but its defining file may
			// still yield the named interface layout.
			saBareImportLoop(link, imp, ms.Text(), sf, func(local, kind string, def *saClassDef, node *ast.TypeNode) {
				if kind == "class" && def != nil {
					if _, dup := classes[local]; !dup {
						classes[local] = def
					}
				}
			})
			continue
		}
		clause := imp.ImportClause.AsImportClause()
		if clause == nil {
			continue
		}
		nb := clause.NamedBindings
		if nb == nil || nb.Kind != ast.KindNamedImports {
			continue
		}
		ni := nb.AsNamedImports()
		if ni == nil || ni.Elements == nil {
			continue
		}
		for _, n := range ni.Elements.Nodes {
			if n == nil || n.Kind != ast.KindImportSpecifier {
				continue
			}
			sp := n.AsImportSpecifier()
			nm := n.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			name := nm.Text()
			remote := name
			if sp.PropertyName != nil {
				remote = sp.PropertyName.Text()
			}
			if local[name] {
				continue
			}
			if _, dup := classes[name]; dup {
				continue
			}
			ptgt := tgt
			if _, ok := link.classHarvests[ptgt][remote]; !ok {
				if st, ok := saProgStarTarget(link, tgt, remote, map[string]bool{}, func(t, r string) bool {
					c, ok := link.classHarvests[t][r]
					return ok && c.exported && c.def != nil
				}); ok {
					ptgt = st
				}
			}
			if ch, ok := link.classHarvests[ptgt][remote]; ok && ch.exported && ch.def != nil {
				classes[name] = ch.def
			}
			if _, ok := classes[name]; !ok && !saBareLocalDecl(sf, name) {
				if kind, def, _, ok := saBareHarvestLookup(link, ms.Text(), remote); ok && kind == "class" && def != nil {
					classes[name] = def
				}
			}
			// 命名空间成员布局预播种（`import { N }` + `extends N.C` 记录期
			// 须见 `N_C`；与 hook B 整件绑定同键同序；已记名跳过）。
			if members, ok := link.classHarvests[tgt]; ok {
				var keys []string
				for key := range members {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					member, ok := strings.CutPrefix(key, remote+".")
					if !ok || member == "" {
						continue
					}
					ch := members[key]
					if !ch.exported || ch.def == nil {
						continue
					}
					if _, dup := classes[name+"_"+member]; dup {
						continue
					}
					classes[name+"_"+member] = ch.def
				}
			}
		}
	}
	// 祖先链传递播种（派生内联 super-wiring 须见全部祖先；visited 防环；
	// 真未知基留空，记录期沿既有 unknown-base 门大声拒）。
	visited := map[string]bool{}
	queue := make([]string, 0, len(classes))
	for name := range classes {
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true
		def := classes[name]
		if def == nil || def.parent == "" {
			continue
		}
		if _, ok := classes[def.parent]; ok {
			continue
		}
		if base := saProgFindClass(link, def.parent); base != nil {
			classes[def.parent] = base
			queue = append(queue, def.parent)
		}
	}
}

// saRefuseNsBareCapture 扫描 ns 类成员体内的裸兄弟纯量读（内联发生在外层
// 域，窗口不可达；用限定 `N.K` 形则经永久键直通）。声明名位/属性名/heritage
// 子句跳过（各有专属门）；余下一律按读处理（宁可误拒，不可静默错位）。
// 形状证据：上游 defNS 常量子集（兄弟读经域内折叠，方法内联无域）。
func saRefuseNsBareCapture(m *ast.Node, names map[string]bool, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if m == nil || len(names) == 0 {
		return false
	}
	var hit *ast.Node
	var walk func(n *ast.Node)
	kids := func(n *ast.Node) {
		n.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk = func(n *ast.Node) {
		if n == nil || hit != nil {
			return
		}
		switch n.Kind {
		case ast.KindIdentifier:
			if names[n.Text()] {
				hit = n
			}
			return
		case ast.KindPropertyAccessExpression:
			if pa := n.AsPropertyAccessExpression(); pa != nil {
				walk(pa.Expression.AsNode())
			}
			return
		case ast.KindHeritageClause:
			// heritage 基走专属门（未知基/混入大声拒），此处跳过。
			return
		case ast.KindVariableDeclaration:
			if vd := n.AsVariableDeclaration(); vd != nil {
				if vd.Type != nil {
					walk(vd.Type.AsNode())
				}
				if vd.Initializer != nil {
					walk(vd.Initializer.AsNode())
				}
			}
			return
		case ast.KindFunctionDeclaration, ast.KindClassDeclaration,
			ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor,
			ast.KindPropertyDeclaration:
			// 声明名跳过（各有专属门），余下子树照走。
			skipName := n.Name()
			n.ForEachChild(func(c *ast.Node) bool {
				if c == skipName {
					return false
				}
				walk(c)
				return false
			})
			return
		case ast.KindParameter:
			if pd := n.AsParameterDeclaration(); pd != nil {
				if pd.Type != nil {
					walk(pd.Type.AsNode())
				}
				if pd.Initializer != nil {
					walk(pd.Initializer.AsNode())
				}
			}
			return
		case ast.KindPropertyAssignment:
			if pa := n.AsPropertyAssignment(); pa != nil && pa.Initializer != nil {
				walk(pa.Initializer.AsNode())
			}
			return
		case ast.KindShorthandPropertyAssignment:
			// `{K}` 简写既读又绑，读侧按裸引用处理。
			if nm := n.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				if names[nm.Text()] {
					hit = nm
				}
			}
			return
		case ast.KindBindingElement:
			if be := n.AsBindingElement(); be != nil {
				if be.PropertyName != nil {
					walk(be.PropertyName.AsNode())
				}
				if be.Initializer != nil {
					walk(be.Initializer.AsNode())
				}
				if bnm := be.Name(); bnm != nil && (bnm.Kind == ast.KindArrayBindingPattern || bnm.Kind == ast.KindObjectBindingPattern) {
					walk(bnm)
				}
			}
			return
		}
		kids(n)
	}
	walk(m)
	if hit == nil {
		return false
	}
	ln, col := pos(hit.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "bare reference to namespace const \"" + hit.Text() + "\" inside a class body is not lowerable (qualify it)"})
	return true
}

// saCouldBeInst 判定表达式是否可能为实例基（绑定实例名或方法内 this）。
func saCouldBeInst(e *ast.Node, scope *saScope) bool {
	// 括号接收者透明（`(c).m()` 即 `c.m()`；与 saInstBase 同步解包，
	// 双门同律；481 被调解包同例 thin-lead。
	for e != nil && e.Kind == ast.KindParenthesizedExpression {
		pe := e.AsParenthesizedExpression()
		if pe == nil || pe.Expression == nil {
			break
		}
		e = pe.Expression.AsNode()
	}
	if e != nil && e.Kind == ast.KindThisKeyword {
		return scope.thisSelf != ""
	}
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok {
			return len(k) > 5 && k[:5] == "inst:"
		}
	}
	if e != nil && e.Kind == ast.KindElementAccessExpression {
		// map 索引基（值种 inst 即实例；封存 layoutOfNode 调用位同形）。
		if ea := e.AsElementAccessExpression(); ea != nil && ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
			if k, ok := scope.types[ea.Expression.Text()]; ok && k == "map" {
				if vk, ok := scope.mapVals[ea.Expression.Text()]; ok {
					return len(vk) > 5 && vk[:5] == "inst:"
				}
			}
		}
		// struct 数组取元基（`b.items[i]` 经布局字段；`?.` 沿读位旧门）。
		// Top object array element base: TOP[i] resolves fields through the
		// synthesized layout recorded on the snapshot.
		if ea := e.AsElementAccessExpression(); ea != nil && ea.QuestionDotToken == nil &&
			ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
			if arr, ok := saTopArrLookup(scope, ea.Expression); ok && arr.layout != "" {
				return true
			}
		}
		// 顶层对象数组域元基（`GROUPS.typecheck[i]` 经域合成布局消解）。
		if ea := e.AsElementAccessExpression(); ea != nil && ea.QuestionDotToken == nil {
			if _, _, _, ok := saTopObjArrField(scope, ea.Expression); ok {
				return true
			}
		}
		if ea := e.AsElementAccessExpression(); ea != nil && ea.QuestionDotToken == nil {
			if _, ok := saStructArrElemLayout(ea.Expression, scope); ok {
				return true
			}
		}
		// Plain array-index element base (xs[i]): the element layout is
		// recorded in arrElemLayout for params, literals, and
		// filter/slice/map results bound to names. saInstBaseElem's
		// index-load branch then tags the loaded handle inst:layout,
		// so field reads on xs[i] resolve exactly like bound elements.
		if ea := e.AsElementAccessExpression(); ea != nil && ea.QuestionDotToken == nil &&
			ea.Expression != nil && ea.Expression.Kind == ast.KindIdentifier {
			if _, ok := scope.arrElemLayout[ea.Expression.Text()]; ok {
				return true
			}
		}
	}
	if e != nil && e.Kind == ast.KindNewExpression {
		// `new C()` 临时实例基（`new C().m()`；类已记布局才放行，构造与
		// 归属复用 saLowerNewStmt，与 step419 实参 new 形同核）。
		if cname, msg := saNewStmtClassName(e.AsNewExpression()); msg == "" {
			def, ok := scope.classes[cname]
			return ok && !def.isIface && !def.isAbstract
		}
	}
	if e != nil && e.Kind == ast.KindCallExpression {
		// 调用结果基（`M.get(k)` 实例映射取；具实例签名的同文件函数调用；
		// 种以求值后记种为准，此处只放行语法形；`?.` 沿旧门）。
		if ce := e.AsCallExpression(); ce != nil && ce.QuestionDotToken == nil && ce.Expression != nil {
			if ce.Expression.Kind == ast.KindPropertyAccessExpression {
				pa := ce.Expression.AsPropertyAccessExpression()
				if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil &&
					pa.Name().Text() == "get" && pa.QuestionDotToken == nil {
					if k, ok := scope.types[pa.Expression.Text()]; ok && k == "map" {
						if vk, ok := scope.mapVals[pa.Expression.Text()]; ok {
							return len(vk) > 5 && vk[:5] == "inst:"
						}
					}
				}
			}
			if ce.Expression.Kind == ast.KindIdentifier {
				if sig, ok := scope.funcs[ce.Expression.Text()]; ok {
					return !sig.isVoid && len(sig.retKind) > 5 && sig.retKind[:5] == "inst:"
				}
				// Cross-file call-result base (JEV direct_call_field_load 0.98):
				// the callee lives under a qualified name in scope.funcs and
				// is reachable only via the link table (saIsStrFieldRead already
				// resolves it this way); without this, an inferred string
				// declaration of a call-result str field in an importing
				// file falls through to the i32 evaluator and refuses with
				// "only .length member access lowerable".
				if q, linked := saLinkCallee(scope, ce.Expression.Text()); linked {
					if sig, ok := scope.funcs[q]; ok {
						return !sig.isVoid && len(sig.retKind) > 5 && sig.retKind[:5] == "inst:"
					}
				}
			}
			if ce.Expression.Kind == ast.KindPropertyAccessExpression && saStaticInstClass(e, scope) != "" {
				return true
			}
			// Equality-find call result (JEV inline_loop_eq 0.98): the
			// scan types its result temp inst:<element layout>, so a
			// find call over a known-layout array is an instance operand
			// for ?? / return / condition dispatch; unknown layouts stay
			// refused by the scan itself, never silently typed.
			if ce.Expression.Kind == ast.KindPropertyAccessExpression {
				pa := ce.Expression.AsPropertyAccessExpression()
				if pa != nil && pa.Name() != nil && pa.QuestionDotToken == nil &&
					(pa.Name().Text() == "find" || pa.Name().Text() == "findLast") &&
					ce.Arguments != nil && len(ce.Arguments.Nodes) >= 1 {
					if _, _, _, _, _, ok := saFindEqGuard(ce.Arguments.Nodes[0]); ok {
						if _, ok := saFindReceiverLayout(pa.Expression, scope); ok {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// saMethodInstReturn 方法返回注解的实例类（`this` 类型即接收者类；具名类
// 引用无类型实参；接口/未知/余形为空）。
func saMethodInstReturn(mn *ast.Node, className string, scope *saScope) string {
	if mn == nil || mn.Kind != ast.KindMethodDeclaration {
		return ""
	}
	typ := mn.AsMethodDeclaration().Type
	if typ == nil {
		return ""
	}
	if typ.Kind == ast.KindThisType {
		return className
	}
	if typ.Kind == ast.KindTypeReference {
		ref := typ.AsTypeReferenceNode()
		if ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && ref.TypeArguments == nil {
			if d, ok := scope.classes[ref.TypeName.Text()]; ok {
				// Interface return types count as instance returns too
				// (JEV iface_objectliteral 1.0): a recorded interface
				// layout is a valid instance layout, same as a class.
				return d.name
			}
		}
	}
	return ""
}

// saStaticInstClass 语法级消解实例表达式的类（this/实例绑定/new/实例返回方法
// 调用链；零发射，供判定位用；不可消解为空）。
func saStaticInstClass(e *ast.Node, scope *saScope) string {
	e = saUnwrapTransparent(e)
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ast.KindThisKeyword:
		if scope.thisSelf != "" {
			return scope.thisClass
		}
	case ast.KindIdentifier:
		if k, ok := scope.types[e.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
			return k[5:]
		}
	case ast.KindNewExpression:
		if cname, msg := saNewStmtClassName(e.AsNewExpression()); msg == "" {
			if d, ok := scope.classes[cname]; ok && !d.isIface && !d.isAbstract {
				return cname
			}
		}
	case ast.KindCallExpression:
		ce := e.AsCallExpression()
		if ce.QuestionDotToken != nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
			return ""
		}
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa.QuestionDotToken != nil || pa.Name() == nil {
			return ""
		}
		base := saStaticInstClass(pa.Expression, scope)
		if base == "" {
			return ""
		}
		if d, ok := scope.classes[base]; ok {
			return saMethodInstReturn(d.methods[pa.Name().Text()], base, scope)
		}
	}
	return ""
}

// saLowerInlineInstReturn 内联方法体 `return <实例>` 存槽（this/实例绑定为别名，
// new/内联体新生归属为移交；同一方法别名与移交混返即归属不定大声拒；类须为
// 声明返回类或其派生）。
func saLowerInlineInstReturn(w printer.EmitTextWriter, expr *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	ir := scope.inlineRet
	want := ir.kind[5:]
	e := saUnwrapTransparent(expr)
	var h, have string
	switch {
	case e != nil && e.Kind == ast.KindThisKeyword && scope.thisSelf != "":
		h, have = scope.thisSelf, scope.thisClass
	case e != nil && e.Kind == ast.KindIdentifier:
		k, ok := scope.types[e.Text()]
		if !ok || len(k) <= 5 || k[:5] != "inst:" {
			return "method instance return must be an instance"
		}
		h, have = e.Text(), k[5:]
	case e != nil && saCouldBeInst(e, scope) && (e.Kind == ast.KindNewExpression || e.Kind == ast.KindCallExpression):
		th, def, msg := saInstBaseElem(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		h, have = th, def.name
	case e != nil && e.Kind == ast.KindObjectLiteralExpression:
		ol := e.AsObjectLiteralExpression()
		if ol == nil {
			return "method instance return must be an instance"
		}
		if def, ok := scope.classes[want]; ok && def != nil && def.isIface {
			// Interface method returns lower the literal via
			// saLowerObjectLiteral, the free-function return path
			// (JEV iface_objectliteral 1.0, scope iface_all 0.98):
			// topMap fields, empty-Record fields and optional-field
			// zero-fill all already work there. Class returns keep
			// saLowerMapObjLit below.
			th, _, msg := saLowerObjectLiteral(w, e, want, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
			h, have = th, want
		} else {
			th, msg := saLowerMapObjLit(w, ol, want, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
			h, have = th, want
		}
	case e != nil && e.Kind == ast.KindObjectLiteralExpression:
		ol := e.AsObjectLiteralExpression()
		if ol == nil {
			return "method instance return must be an instance"
		}
		th, msg := saLowerMapObjLit(w, ol, want, scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		h, have = th, want
	default:
		return "method instance return must be this, a bound instance, new or an instance call"
	}
	if !saIsDerivedFrom(scope.classes, have, want) {
		return "method instance return class mismatch (want " + want + ")"
	}
	mode := 2
	if b := saOwnOf(scope, h); b != nil && b.heap && !b.consumed && !b.released {
		for i := ir.scopeBase; i < len(scope.ownOrder); i++ {
			if scope.ownOrder[i] == h {
				mode = 1
				break
			}
		}
	}
	if ir.instMode != 0 && ir.instMode != mode {
		return "method returns both fresh and existing instances (ownership ambiguous)"
	}
	ir.instMode = mode
	if mode == 1 {
		w.Write(fmt.Sprintf("  %s = %s\n", ir.slot, h))
		saConsumeOwn(scope, h)
	} else {
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", ir.slot, h))
	}
	saReleaseDeeperThan(w, scope, ir.scopeBase)
	w.Write(fmt.Sprintf("  jmp %s\n", ir.end))
	return ""
}

// saRecordIface 记录接口布局（i32 字段；无码。形状证据：封存
// lowerTypeDecl 的布局记录一半；方法/索引签名大声拒）。
func saRecordIface(st *ast.Node, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, pos func(int) (int, int), refusals *[]SARefusal) bool {
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
	// 泛型形参模板直存（具化前只存不用；封存 recordTypeAlias tparams 同形）。
	if decl.TypeParameters != nil {
		for _, tp := range decl.TypeParameters.Nodes {
			if tp == nil || tp.Kind != ast.KindTypeParameter {
				continue
			}
			if nm := tp.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				def.tparams = append(def.tparams, nm.Text())
			}
		}
	}
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
					if tn := el.AsTypeReferenceNode(); tn != nil && tn.TypeName != nil && tn.TypeName.Kind == ast.KindIdentifier {
						iname = tn.TypeName.Text()
						// 泛型基具化（`extends Box<i32>` 按单态展平；不可具化
						// 沿旧擦除；封存 instantiateLayout 继承位同形）。
						if tn.TypeArguments != nil && len(tn.TypeArguments.Nodes) > 0 {
							if lname, ok := saInstantiateIface(iname, tn.TypeArguments.Nodes, classes); ok {
								iname = lname
							}
						}
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
					// 可选位随继承透传（缺省零填门凭基表放行）。
					if base.fopt[f.name] {
						if def.fopt == nil {
							def.fopt = map[string]bool{}
						}
						def.fopt[f.name] = true
					}
					// 模板节点透传（单态基除外：其 fdefs 为代入前原形，
					// 缺节点时具化回退模板已算种，见 saInstantiateIface）。
					if !base.isMono && base.fdefs != nil {
						if fd, ok := base.fdefs[f.name]; ok && fd != nil {
							if def.fdefs == nil {
								def.fdefs = map[string]*ast.TypeNode{}
							}
							def.fdefs[f.name] = fd
						}
					}
					sz, _ := saFieldWidth(fk)
					off += sz
				}
			}
		}
	}
	for _, m := range decl.Members.Nodes {
		if m.Kind != ast.KindPropertySignature {
			// 方法签名记名不占槽（供 implements 存在性校验；无发射位，
			// 禁静默缺方法；形状证据：封存 recordLayout 方法跳过同形）。
			if m.Kind == ast.KindMethodSignature {
				if fn := m.Name(); fn != nil && fn.Kind == ast.KindIdentifier {
					if def.methods == nil {
						def.methods = map[string]*ast.Node{}
					}
					def.methods[fn.Text()] = m
					// 方法签名声明位直存（缺方法拒因指接口方法位；只存不用）。
					if def.fpos == nil {
						def.fpos = map[string]int{}
					}
					def.fpos[fn.Text()] = m.Pos()
				}
			}
			// non-field members declare no layout slot (methods/indexers/signatures are type-only;
			// cf recordLayout method-signature skip).
			continue
		}
		fn := m.Name()
		if fn == nil || fn.Kind != ast.KindIdentifier {
			// 非标识字段名（引号串名/计算名）跳过该域（整接口不拒；形状证据：
			// 封存 recordLayout:9374-9377 `bindingNameText` 不成即 `continue`；
			// 失配字面量键集在构造匹配处仍大声拒，无静默错码）。
			continue
		}
		pd := m.AsPropertySignatureDeclaration()
		fkind := saIfaceFieldKind(pd.Type, fn.Text(), def, classes)
		// 别名字段种修正（1612）：接口域为未记录别名引用时，saIfaceFieldKind
		// 落 arr 句柄槽；若该别名消解为标量（串字面量联合等），按标量种
		// 重记（与 saRecordTypeAlias 的 saResolveAliasKind 门同律；inst
		// 域不走此支，因其 fkind 已为 inst 而非 arr）。
		if fkind == "arr" {
			if ak, ok := saResolveAliasKind(pd.Type, aliasOf); ok && (ak == "str" || ak == "i32" || ak == "bool" || ak == "f64") {
				fkind = ak
			}
		}
		// 嵌套匿名布局别名修正（1147）：内联对象字段经 saIfaceFieldKind
		// 合成的旧布局不带别名表，其别名-字面量联合域落 arr 槽；
		// 以别名表重合成一次，域种按 saMergedFieldKind 修正（串字面量
		// 联合即 str，JEV plain_str 1.0），并改指 fsub；同形时返回同名，
		// 异种时按 _2 后缀另建，旧布局留给无别名上下文复用，不误删。
		if pd.Type != nil && pd.Type.Kind == ast.KindTypeLiteral {
			if lname, ok := saSynthAnonLayoutAlias(pd.Type, classes, aliasOf); ok {
				fkind = "inst"
				if def.fsub == nil {
					def.fsub = map[string]string{}
				}
				def.fsub[fn.Text()] = lname
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
		// 可选字段记位（缺省零填门凭此放行；必备缺省沿旧门）。
		if m.QuestionToken() != nil {
			if def.fopt == nil {
				def.fopt = map[string]bool{}
			}
			def.fopt[fn.Text()] = true
		}
		// 字段声明位直存（implements 缺字段拒因指接口字段位；只存不用，零行为变）。
		if def.fpos == nil {
			def.fpos = map[string]int{}
		}
		def.fpos[fn.Text()] = m.Pos()
		// 字段类型节点直存（具化期代入用；只存不用，零行为变）。
		if pd.Type != nil {
			if def.fdefs == nil {
				def.fdefs = map[string]*ast.TypeNode{}
			}
			def.fdefs[fn.Text()] = pd.Type
		}
		sz, _ := saFieldWidth(fkind)
		off += sz
	}
	def.size = off
	classes[name] = def
	return true
}

// saIfaceFieldKind 解接口域种（纯搬运原记录循环体，零语义变；新增一支：
// 字面量目标递归合成匿名布局，合成失败沿旧门 arr）。
// 形状证据：封存 saNameOfType:197-199 + recordLayout:9375 + widthOf 默认 8,8。
func saIfaceFieldKind(ftn *ast.TypeNode, fname string, def *saClassDef, classes map[string]*saClassDef) string {
	fkind := "i32"
	if ftn == nil {
		return fkind
	}
	// i32/bool 恒 4 字节槽（封存 saNameOfType boolean→i32 同形）；
	// string/arr 为句柄 8 字节槽；f64 为双精度 8 字节槽；已记录接口名（TypeReference）为嵌套
	// 句柄 8 字节槽（与上游 layoutOfCheckerName 同形）；未记录用户类型
	// （含泛型形参）落 arr 句柄槽；余下无槽，拒。
	if k, ok := saAnnotKind(ftn); ok && (k == "i32" || k == "bool" || k == "str" || k == "arr" || k == "f64") {
		if k == "str" {
			fkind = "str"
		} else if k == "arr" {
			fkind = "arr"
		} else if k == "f64" {
			fkind = "f64"
		}
	} else if ftn.Kind == ast.KindTypeReference && ftn.AsTypeReferenceNode() != nil && ftn.AsTypeReferenceNode().TypeName != nil && ftn.AsTypeReferenceNode().TypeName.Kind == ast.KindIdentifier {
		// 类/接口类型嵌套字段皆 8B 句柄（布局表同形；实例与接口对象皆句柄）。
		if sub, ok := classes[ftn.AsTypeReferenceNode().TypeName.Text()]; ok {
			fkind = "inst"
			if def.fsub == nil {
				def.fsub = map[string]string{}
			}
			def.fsub[fname] = sub.name
		} else {
			// 未记录用户类型（含泛型形参 `T`）落 ptr 句柄槽（8B；封存
			// saNameOfType:197-199 用户类型皆 ptr 句柄 + recordLayout:9375
			// `saname := saNameOfType(ftn)` 直映 + widthOf 默认 8,8；本仓句柄种为 arr）。
			fkind = "arr"
		}
	} else if ftn.Kind == ast.KindTypeLiteral {
		// 嵌套字面量目标递归合成匿名布局（Z1；合成失败沿旧门 arr）。
		if sub, ok := saSynthAnonLayout(ftn, classes); ok {
			fkind = "inst"
			if def.fsub == nil {
				def.fsub = map[string]string{}
			}
			def.fsub[fname] = sub
		} else {
			fkind = "arr"
		}
	} else if ftn.Kind == ast.KindUnionType {
		// 字面量联合按标量归种（`"m"|"f"` 即 str，数字联合即 i32；经
		// `saUnionScalarKind` 与注解核同形；余形落 ptr 句柄槽；形状证据：
		// 封存 saNameOfType:235-264 default 恒 ptr + 本仓 saLitFieldKind）。
		if uk, ok := saUnionScalarKind(ftn.AsNode()); ok {
			switch uk {
			case "str":
				fkind = "str"
			case "arr":
				fkind = "arr"
			default:
				fkind = "i32"
			}
		} else {
			fkind = "arr"
		}
	} else if ftn.Kind == ast.KindAnyKeyword || ftn.Kind == ast.KindUnknownKeyword {
		// `any` fields lower as ptr handle slots (cf class record).
		fkind = "arr"
	} else {
		// 余下类型（字面量/对象字面量/函数/元组等）皆落 ptr 句柄槽（8B；
		// 形状证据：封存 saNameOfType:235-264 default 分支恒 "ptr" +
		// widthOf:268-278 default 8,8；本仓句柄种为 arr，与未知引用同例）。
		fkind = "arr"
	}
	return fkind
}

// saSynthAnonLayout 由字面量目标合成匿名接口布局（名按域集派生 `Anon_a_b`，
// 同名异种 `_2` 递加，全程确定性；同形复用，空域/坏形 false；合成布局记
// isSynth，不参与键集匹配（保既有多命中拒不变），仅注解直命中可用）。
func saSynthAnonLayout(ftn *ast.TypeNode, classes map[string]*saClassDef) (string, bool) {
	return saSynthAnonLayoutAlias(ftn, classes, nil)
}

// saAnonLayoutEqual 判定既有合成布局与给定域集同形（域数/域名/种全等；
// 种按当前表重算后比对，表变化致异种即不复用，后缀另建）。
func saAnonLayoutEqual(def *saClassDef, fields []string, fdefs map[string]*ast.TypeNode, classes map[string]*saClassDef) bool {
	if def == nil || !def.isSynth || len(def.fields) != len(fields) {
		return false
	}
	probe := &saClassDef{fsub: map[string]string{}}
	for i, fname := range fields {
		if i >= len(def.fields) || def.fields[i].name != fname {
			return false
		}
		if saIfaceFieldKind(fdefs[fname], fname, probe, classes) != def.fkinds[fname] {
			return false
		}
	}
	return true
}

// saAnonFieldKindAlias is the alias-aware field-kind computation for
// anonymous layouts: inline object fields recurse through the alias-aware
// synthesis (so doubly nested anonymous objects keep the alias table),
// other fields go through saMergedFieldKind, which resolves alias-to-scalar
// unions (string-literal unions -> str) and otherwise falls back to
// saIfaceFieldKind. With a nil alias table it matches the old behaviour.
func saAnonFieldKindAlias(ftn *ast.TypeNode, fname string, def *saClassDef, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) string {
	if ftn != nil && ftn.Kind == ast.KindTypeLiteral {
		if lname, ok := saSynthAnonLayoutAlias(ftn, classes, aliasOf); ok {
			if def.fsub == nil {
				def.fsub = map[string]string{}
			}
			def.fsub[fname] = lname
			return "inst"
		}
		return "arr"
	}
	return saMergedFieldKind(ftn, fname, def, classes, aliasOf)
}

// saAnonLayoutEqualAlias is saAnonLayoutEqual with alias-aware kind
// recomputation: an existing layout whose field kinds differ under the
// alias table (e.g. delete: arr vs str) is not reused; synthesis then
// creates a suffixed layout instead of silently reusing the wrong kind.
func saAnonLayoutEqualAlias(def *saClassDef, fields []string, fdefs map[string]*ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) bool {
	if def == nil || !def.isSynth || len(def.fields) != len(fields) {
		return false
	}
	probe := &saClassDef{fsub: map[string]string{}}
	for i, fname := range fields {
		if i >= len(def.fields) || def.fields[i].name != fname {
			return false
		}
		if saAnonFieldKindAlias(fdefs[fname], fname, probe, classes, aliasOf) != def.fkinds[fname] {
			return false
		}
	}
	return true
}

// saSynthAnonLayoutAlias is saSynthAnonLayout with the alias table threaded
// through (JEV thread_alias_into_synth_anon 0.99): nested alias-union
// fields resolve to their scalar kind instead of the generic arr slot.
// The plain saSynthAnonLayout wrapper below keeps the nil-alias behaviour
// for call sites without an alias table.
func saSynthAnonLayoutAlias(ftn *ast.TypeNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if ftn == nil || ftn.Kind != ast.KindTypeLiteral {
		return "", false
	}
	lit := ftn.AsTypeLiteralNode()
	if lit == nil || lit.Members == nil {
		return "", false
	}
	var fields []string
	fdefs := map[string]*ast.TypeNode{}
	fopt := map[string]bool{}
	for _, m := range lit.Members.Nodes {
		if m == nil || m.Kind != ast.KindPropertySignature {
			continue
		}
		fn := m.Name()
		if fn == nil || fn.Kind != ast.KindIdentifier {
			continue
		}
		if _, dup := fdefs[fn.Text()]; dup {
			continue
		}
		pd := m.AsPropertySignatureDeclaration()
		if pd == nil || pd.Type == nil {
			continue
		}
		fields = append(fields, fn.Text())
		fdefs[fn.Text()] = pd.Type
		if m.QuestionToken() != nil {
			fopt[fn.Text()] = true
		}
	}
	if len(fields) == 0 {
		return "", false
	}
	base := "Anon_" + saMonoName(strings.Join(fields, "_"))
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		if def, ok := classes[name]; ok {
			if saAnonLayoutEqualAlias(def, fields, fdefs, classes, aliasOf) {
				return def.name, true
			}
			continue
		}
		def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fopt: map[string]bool{}}
		off := 0
		for _, fname := range fields {
			fkind := saAnonFieldKindAlias(fdefs[fname], fname, def, classes, aliasOf)
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: off})
			def.offsets[fname] = off
			def.fkinds[fname] = fkind
			def.fdefs[fname] = fdefs[fname]
			if fopt[fname] {
				def.fopt[fname] = true
			}
			sz, _ := saFieldWidth(fkind)
			off += sz
		}
		def.size = off
		classes[name] = def
		return def.name, true
	}
}

// saFixupForwardIfaceFields corrects forward-referenced interface fields
// (1147 follow-up, JEV harvest_fixup_pass 0.97): interfaces are recorded
// in a single file-order pass, so a field referencing a layout declared
// later in the file is first recorded as arr (generic handle slot). Once
// the table is complete, any such field whose stored type node is a plain
// reference to a now-recorded interface/synth layout is upgraded to inst
// with its sub-layout registered. Unknown references stay arr, exactly as
// the single-pass fallback. arr and inst slots are both 8 bytes, so
// offsets and layout sizes do not move. Scope is interface and synthesized
// layouts only (isIface); class layouts are excluded. Inherited fields
// carry copied fdefs (except mono bases), so they are fixed up too.
func saFixupForwardIfaceFields(classes map[string]*saClassDef) {
	for _, def := range classes {
		if def == nil || !def.isIface {
			continue
		}
		for _, fld := range def.fields {
			if def.fkinds[fld.name] != "arr" {
				continue
			}
			ftn, ok := def.fdefs[fld.name]
			if !ok || ftn == nil || ftn.Kind != ast.KindTypeReference {
				continue
			}
			ref := ftn.AsTypeReferenceNode()
			if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil {
				continue
			}
			tgt, ok := classes[ref.TypeName.Text()]
			if !ok || tgt == nil || !tgt.isIface {
				continue
			}
			def.fkinds[fld.name] = "inst"
			if def.fsub == nil {
				def.fsub = map[string]string{}
			}
			def.fsub[fld.name] = tgt.name
		}
	}
}

// saAliasUnionScalarKind resolves a type (following alias chains) to a
// scalar union kind when the endpoint is a union of literals/keywords
// (e.g. TokenUsageSource = "openai" | "anthropic" -> str). Alias cycles
// and non-union endpoints return false; callers fall back to
// saIfaceFieldKind.
func saAliasUnionScalarKind(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode) (string, bool) {
	cur := t
	seen := map[string]bool{}
	for i := 0; i <= len(aliasOf)+2 && cur != nil; i++ {
		switch cur.Kind {
		case ast.KindUnionType:
			if k, ok := saUnionScalarKind(cur.AsNode()); ok {
				return k, true
			}
			// 3_theme2 (JEV alias_str_union, additive fallback): unions
			// the raw scalar check rejects only because members go through
			// aliases or the string&{} idiom still resolve to str here.
			// Existing successful resolutions never change.
			if ut := cur.AsNode().AsUnionTypeNode(); ut != nil && saAliasStrUnionKind(ut, aliasOf, map[string]bool{}) {
				return "str", true
			}
			return "", false
		case ast.KindParenthesizedType:
			pt := cur.AsParenthesizedTypeNode()
			if pt == nil || pt.Type == nil {
				return "", false
			}
			cur = pt.Type
		case ast.KindTypeOperator:
			// keyof 值恒为串（JEV keyof_to_str 1.0；keyof5 的域解析同律）；
			// 非 keyof 操作符沿旧门（循环内无 readonly 解包，keyof 直解）。
			to := cur.AsTypeOperatorNode()
			if to == nil || to.Operator != ast.KindKeyOfKeyword || to.Type == nil {
				return "", false
			}
			return "str", true
		case ast.KindTypeReference:
			ref := cur.AsTypeReferenceNode()
			if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil || len(aliasOf) == 0 {
				return "", false
			}
			nm := ref.TypeName.Text()
			if seen[nm] {
				return "", false
			}
			seen[nm] = true
			nxt, ok := aliasOf[nm]
			if !ok || nxt == nil {
				return "", false
			}
			cur = nxt
		default:
			return "", false
		}
	}
	return "", false
}

// saAliasStrMemberKind reports whether a single union member denotes only
// string values, resolving TypeReference members through aliasOf with a
// chain-scoped seen set (a copy per chain, so repeated members do not
// falsely trip the cycle guard). The string&{} widening idiom counts as
// string only when every other intersection member is an empty type
// literal; anything else (interfaces, arrays, non-string members) fails.
func saAliasStrMemberKind(m *ast.Node, aliasOf map[string]*ast.TypeNode, seen map[string]bool) bool {
	if m == nil {
		return false
	}
	cur := m
	for cur != nil && cur.Kind == ast.KindParenthesizedType {
		pt := cur.AsParenthesizedTypeNode()
		if pt == nil || pt.Type == nil {
			return false
		}
		cur = pt.Type.AsNode()
	}
	if cur == nil {
		return false
	}
	switch cur.Kind {
	case ast.KindStringKeyword:
		return true
	case ast.KindLiteralType:
		lit := cur.AsLiteralTypeNode().Literal
		return lit != nil && (lit.Kind == ast.KindStringLiteral || lit.Kind == ast.KindNoSubstitutionTemplateLiteral)
	case ast.KindTypeReference:
		ref := cur.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil {
			return false
		}
		nm := ref.TypeName.Text()
		if seen[nm] {
			return false
		}
		ns := map[string]bool{}
		for k, v := range seen {
			ns[k] = v
		}
		ns[nm] = true
		tgt, ok := aliasOf[nm]
		if !ok || tgt == nil {
			return false
		}
		return saAliasStrMemberKind(tgt.AsNode(), aliasOf, ns)
	case ast.KindUnionType:
		ut := cur.AsUnionTypeNode()
		return ut != nil && saAliasStrUnionKind(ut, aliasOf, seen)
	case ast.KindIntersectionType:
		it := cur.AsIntersectionTypeNode()
		if it == nil || it.Types == nil {
			return false
		}
		sawStr := false
		for _, im := range it.Types.Nodes {
			if im == nil {
				return false
			}
			ic := im
			for ic != nil && ic.Kind == ast.KindParenthesizedType {
				pt := ic.AsParenthesizedTypeNode()
				if pt == nil || pt.Type == nil {
					return false
				}
				ic = pt.Type.AsNode()
			}
			if ic == nil {
				return false
			}
			switch ic.Kind {
			case ast.KindStringKeyword:
				sawStr = true
			case ast.KindTypeLiteral:
				lt := ic.AsTypeLiteralNode()
				if lt == nil {
					return false
				}
				if lt.Members != nil && len(lt.Members.Nodes) != 0 {
					return false
				}
			default:
				return false
			}
		}
		return sawStr
	default:
		return false
	}
}

// saAliasStrUnionKind reports whether every non-null member of a union
// denotes only string values (3_theme2, JEV alias_str_union). Null and
// undefined members are absorbed, matching saUnionScalarKind; an empty
// union (all null) is not a string union.
func saAliasStrUnionKind(ut *ast.UnionTypeNode, aliasOf map[string]*ast.TypeNode, seen map[string]bool) bool {
	if ut == nil || ut.Types == nil {
		return false
	}
	saw := false
	for _, m := range ut.Types.Nodes {
		if m == nil {
			return false
		}
		if m.Kind == ast.KindNullKeyword || m.Kind == ast.KindUndefinedKeyword {
			continue
		}
		if m.Kind == ast.KindLiteralType {
			if lit := m.AsLiteralTypeNode().Literal; lit != nil && lit.Kind == ast.KindNullKeyword {
				continue
			}
		}
		if !saAliasStrMemberKind(m, aliasOf, seen) {
			return false
		}
		saw = true
	}
	return saw
}

// saMergedFieldKind is saIfaceFieldKind with one correction: a field whose
// type is an alias to a scalar union takes the union kind (a string-literal
// union field is a str handle, not the arr slot saIfaceFieldKind assigns to
// an unrecorded TypeReference). Bool unions land in i32 slots, matching
// saIfaceFieldKind's bool handling.
func saMergedFieldKind(ftn *ast.TypeNode, fname string, def *saClassDef, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) string {
	if uk, ok := saAliasUnionScalarKind(ftn, aliasOf); ok {
		switch uk {
		case "str":
			return "str"
		case "arr":
			return "arr"
		case "f64":
			return "f64"
		default:
			return "i32"
		}
	}
	return saIfaceFieldKind(ftn, fname, def, classes)
}

// saCollectMergedFields unions the fields of an intersection's constituents
// (in order, first wins on duplicates): type literals contribute their
// property signatures, named interfaces contribute their recorded fields,
// and named aliases are followed (cycle-guarded). Anything else fails.
func saCollectMergedFields(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode, classes map[string]*saClassDef, fields *[]string, fdefs map[string]*ast.TypeNode, fopt map[string]bool, fkinds map[string]string, fsub map[string]string, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ast.KindParenthesizedType:
		pt := t.AsParenthesizedTypeNode()
		if pt == nil || pt.Type == nil {
			return false
		}
		return saCollectMergedFields(pt.Type, aliasOf, classes, fields, fdefs, fopt, fkinds, fsub, seen)
	case ast.KindTypeLiteral:
		lit := t.AsTypeLiteralNode()
		if lit == nil || lit.Members == nil {
			return false
		}
		// A literal member merges successfully when at least one member
		// is well-formed, even if every field duplicates an earlier member
		// (JEV pick inst_synth: Pick<...> & {permission, ...} where the
		// literal fields are already in the base must not veto the merge).
		// A duplicate re-declared without `?` clears the optional flag
		// (intersection required-wins); the interface/Pick branches skip
		// duplicates before copying fopt, so this is order-independent.
		valid := false
		for _, m := range lit.Members.Nodes {
			if m == nil || m.Kind != ast.KindPropertySignature {
				continue
			}
			nm := m.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			pd := m.AsPropertySignatureDeclaration()
			if pd == nil || pd.Type == nil {
				continue
			}
			valid = true
			if _, dup := fdefs[nm.Text()]; dup {
				if m.QuestionToken() == nil {
					delete(fopt, nm.Text())
				}
				continue
			}
			*fields = append(*fields, nm.Text())
			fdefs[nm.Text()] = pd.Type
			if m.QuestionToken() != nil {
				fopt[nm.Text()] = true
			}
		}
		return valid
	case ast.KindIntersectionType:
		it := t.AsIntersectionTypeNode()
		if it == nil || it.Types == nil {
			return false
		}
		any := false
		for _, m := range it.Types.Nodes {
			if m == nil || !saCollectMergedFields(m, aliasOf, classes, fields, fdefs, fopt, fkinds, fsub, seen) {
				return false
			}
			any = true
		}
		return any
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return false
		}
		nm := ref.TypeName.Text()
		// Generic instantiation member (Partial<T>, Pick<T,..>, ...): resolve
		// it to its synthesized layout and merge that layout's fields, so
		// `TopicTranscriptDiagnostics & Partial<TopicUsageDiagnostics>` merges
		// instead of refusing. Non-inst resolutions (jsval/map/...) still fail
		// the merge loudly, as before.
		if ref.TypeArguments != nil {
			if k, ok := saReturnKindRef(t, classes, aliasOf); ok && len(k) > 5 && k[:5] == "inst:" {
				if def, ok := classes[k[5:]]; ok {
					for _, f := range def.fields {
						if _, dup := fdefs[f.name]; dup {
							continue
						}
						*fields = append(*fields, f.name)
						if def.fdefs != nil {
							fdefs[f.name] = def.fdefs[f.name]
						}
						if def.fkinds != nil {
							fkinds[f.name] = def.fkinds[f.name]
						}
						if def.fsub != nil {
							if sub, ok := def.fsub[f.name]; ok {
								fsub[f.name] = sub
							}
						}
						if def.fopt != nil && def.fopt[f.name] {
							fopt[f.name] = true
						}
					}
					return len(def.fields) > 0
				}
			}
			return false
		}
		if def, ok := classes[nm]; ok && def.isIface {
			for _, f := range def.fields {
				if _, dup := fdefs[f.name]; dup {
					continue
				}
				*fields = append(*fields, f.name)
				if def.fdefs != nil {
					fdefs[f.name] = def.fdefs[f.name]
				}
				if def.fkinds != nil {
					fkinds[f.name] = def.fkinds[f.name]
				}
				if def.fsub != nil {
					if sub, ok := def.fsub[f.name]; ok {
						fsub[f.name] = sub
					}
				}
				if def.fopt != nil && def.fopt[f.name] {
					fopt[f.name] = true
				}
			}
			return len(def.fields) > 0
		}
		if seen[nm] {
			return false
		}
		seen[nm] = true
		tgt, ok := aliasOf[nm]
		if !ok || tgt == nil {
			return false
		}
		return saCollectMergedFields(tgt, aliasOf, classes, fields, fdefs, fopt, fkinds, fsub, seen)
	default:
		return false
	}
}

// saMergedLayoutEqual mirrors saAnonLayoutEqual for merged layouts, using
// the alias-aware field kind (a stored str field for a string-literal-union
// alias must compare equal, or every lookup would mint a new layout).
func saMergedLayoutEqual(def *saClassDef, fields []string, fdefs map[string]*ast.TypeNode, fkinds map[string]string, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) bool {
	if def == nil || !def.isSynth || len(def.fields) != len(fields) {
		return false
	}
	probe := &saClassDef{fsub: map[string]string{}}
	for i, fname := range fields {
		if i >= len(def.fields) || def.fields[i].name != fname {
			return false
		}
		var k string
		if v, ok := fkinds[fname]; ok {
			k = v
		} else {
			k = saMergedFieldKind(fdefs[fname], fname, probe, classes, aliasOf)
		}
		if k != def.fkinds[fname] {
			return false
		}
	}
	return true
}

// saSynthMergedLayout synthesizes an instance layout for an intersection
// type (direct, or reached through a type-alias reference), named
// Inter_<fields> with _2 suffixes on kind mismatch, following the
// saSynthAnonLayout discipline. A plain alias to a type literal is also
// accepted (same merged shape, one constituent).
func saSynthMergedLayout(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode, classes map[string]*saClassDef) (string, bool) {
	var fields []string
	fdefs := map[string]*ast.TypeNode{}
	fopt := map[string]bool{}
	fkinds := map[string]string{}
	fsub := map[string]string{}
	if !saCollectMergedFields(t, aliasOf, classes, &fields, fdefs, fopt, fkinds, fsub, map[string]bool{}) {
		return "", false
	}
	if len(fields) == 0 {
		return "", false
	}
	base := "Inter_" + saMonoName(strings.Join(fields, "_"))
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		if def, ok := classes[name]; ok {
			if saMergedLayoutEqual(def, fields, fdefs, fkinds, classes, aliasOf) {
				return def.name, true
			}
			continue
		}
		def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fopt: map[string]bool{}, fsub: fsub}
		off := 0
		for _, fname := range fields {
			var fkind string
			if k, ok := fkinds[fname]; ok {
				fkind = k
			} else {
				fkind = saMergedFieldKind(fdefs[fname], fname, def, classes, aliasOf)
			}
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: off})
			def.offsets[fname] = off
			def.fkinds[fname] = fkind
			def.fdefs[fname] = fdefs[fname]
			if fopt[fname] {
				def.fopt[fname] = true
			}
			sz, _ := saFieldWidth(fkind)
			off += sz
		}
		def.size = off
		classes[name] = def
		return def.name, true
	}
}

// saUnionVariantField is one field of one union variant for merged-layout
// synthesis. kind is the recorded kind for interface members and "" for
// literal members (kind computed from fdef during unification); sub is the
// recorded nested layout for interface inst fields.
type saUnionVariantField struct {
	name   string
	fdef   *ast.TypeNode
	opt    bool
	kind   string
	sub    string
	hasSub bool
}

type saUnionVariant struct {
	fields []saUnionVariantField
}

// saResolveUnionNode follows aliases (cycle-guarded) and parens to the
// underlying union node, or nil when the type is not a union.
func saResolveUnionNode(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode) *ast.UnionTypeNode {
	seen := map[string]bool{}
	cur := t
	for cur != nil {
		switch cur.Kind {
		case ast.KindUnionType:
			return cur.AsUnionTypeNode()
		case ast.KindParenthesizedType:
			pt := cur.AsParenthesizedTypeNode()
			if pt == nil {
				return nil
			}
			cur = pt.Type
		case ast.KindTypeReference:
			ref := cur.AsTypeReferenceNode()
			if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil {
				return nil
			}
			nm := ref.TypeName.Text()
			if seen[nm] {
				return nil
			}
			seen[nm] = true
			nxt, ok := aliasOf[nm]
			if !ok || nxt == nil {
				return nil
			}
			cur = nxt
		default:
			return nil
		}
	}
	return nil
}

// saIsUnionNullMember reports null/undefined union members (absorbed, like
// saUnionEraseKind/saUnionInstKind).
func saIsUnionNullMember(m *ast.TypeNode) bool {
	if m == nil {
		return true
	}
	if m.Kind == ast.KindNullKeyword || m.Kind == ast.KindUndefinedKeyword {
		return true
	}
	if m.Kind == ast.KindLiteralType {
		if lit := m.AsLiteralTypeNode().Literal; lit != nil && lit.Kind == ast.KindNullKeyword {
			return true
		}
	}
	return false
}

// saCollectUnionVariant appends one object-shaped variant's fields to out.
// Nested unions are flattened one level per recursion (cycle-guarded via
// seen on alias names). Scalar members fail loudly: a mixed scalar/object
// union is not a merged layout (that shape belongs to jsval).
func saCollectUnionVariant(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode, classes map[string]*saClassDef, seen map[string]bool, out *[]saUnionVariant) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ast.KindParenthesizedType:
		pt := t.AsParenthesizedTypeNode()
		if pt == nil || pt.Type == nil {
			return false
		}
		return saCollectUnionVariant(pt.Type, aliasOf, classes, seen, out)
	case ast.KindUnionType:
		ut := t.AsUnionTypeNode()
		if ut == nil || ut.Types == nil {
			return false
		}
		for _, m := range ut.Types.Nodes {
			if saIsUnionNullMember(m) {
				continue
			}
			if !saCollectUnionVariant(m, aliasOf, classes, seen, out) {
				return false
			}
		}
		return true
	case ast.KindTypeLiteral:
		lit := t.AsTypeLiteralNode()
		if lit == nil || lit.Members == nil {
			return false
		}
		v := saUnionVariant{}
		for _, m := range lit.Members.Nodes {
			if m == nil || m.Kind != ast.KindPropertySignature {
				continue
			}
			fn := m.Name()
			if fn == nil || fn.Kind != ast.KindIdentifier {
				continue
			}
			pd := m.AsPropertySignatureDeclaration()
			if pd == nil || pd.Type == nil {
				continue
			}
			f := saUnionVariantField{name: fn.Text(), fdef: pd.Type}
			if m.QuestionToken() != nil {
				f.opt = true
			}
			v.fields = append(v.fields, f)
		}
		*out = append(*out, v)
		return true
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil {
			return false
		}
		nm := ref.TypeName.Text()
		if def, ok := classes[nm]; ok && def != nil && def.isIface {
			v := saUnionVariant{}
			for _, fld := range def.fields {
				f := saUnionVariantField{name: fld.name, opt: def.fopt[fld.name], kind: def.fkinds[fld.name]}
				if def.fdefs != nil {
					f.fdef = def.fdefs[fld.name]
				}
				if sub, ok := def.fsub[fld.name]; ok {
					f.sub = sub
					f.hasSub = true
				}
				v.fields = append(v.fields, f)
			}
			*out = append(*out, v)
			return true
		}
		if seen[nm] {
			return false
		}
		seen[nm] = true
		nxt, ok := aliasOf[nm]
		if !ok || nxt == nil {
			return false
		}
		return saCollectUnionVariant(nxt, aliasOf, classes, seen, out)
	default:
		return false
	}
}

// saUnionLayoutEqual reports whether an existing synth layout matches the
// computed union fields (order, kinds, optionality, nested layouts all
// equal); mismatches get a suffixed layout instead of silent reuse.
func saUnionLayoutEqual(def *saClassDef, fields []string, fkinds map[string]string, fopt map[string]bool, fsub map[string]string) bool {
	if def == nil || !def.isSynth || len(def.fields) != len(fields) {
		return false
	}
	for i, fname := range fields {
		if i >= len(def.fields) || def.fields[i].name != fname {
			return false
		}
		if def.fkinds[fname] != fkinds[fname] {
			return false
		}
		if def.fopt[fname] != fopt[fname] {
			return false
		}
		sub, ok := fsub[fname]
		dsub, dok := def.fsub[fname]
		if ok != dok || (ok && sub != dsub) {
			return false
		}
	}
	return true
}

// saUnionScalarJsValKind reports whether a union's non-null members are all
// direct scalars (number/boolean/string keywords, string/numeric/bool
// literals, parenthesized forms). Such heterogeneous scalar unions (e.g.
// number | string | null) cannot erase to one base kind, so they resolve
// to jsval with per-arm boxing at return sites.
func saUnionScalarJsValKind(ut *ast.UnionTypeNode) bool {
	if ut == nil || ut.Types == nil {
		return false
	}
	saw := false
	for _, m := range ut.Types.Nodes {
		if saIsUnionNullMember(m) {
			continue
		}
		if m == nil {
			return false
		}
		cur := m
		if cur.Kind == ast.KindParenthesizedType {
			pt := cur.AsParenthesizedTypeNode()
			if pt == nil || pt.Type == nil {
				return false
			}
			cur = pt.Type
		}
		switch cur.Kind {
		case ast.KindNumberKeyword, ast.KindBooleanKeyword, ast.KindStringKeyword:
			saw = true
		case ast.KindLiteralType:
			lit := cur.AsLiteralTypeNode().Literal
			if lit == nil {
				return false
			}
			switch lit.Kind {
			case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword:
				saw = true
			default:
				return false
			}
		default:
			return false
		}
	}
	return saw
}

// saSynthUnionLayout synthesizes a merged inst layout for a union of object
// literals (JEV merged_layout 0.98): fields unioned in first-seen order,
// variant-specific fields optional and zero-filled on match, kinds unified
// across variants. Returns the layout name (caller prefixes "inst:").
func saSynthUnionLayout(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode, classes map[string]*saClassDef) (string, bool) {
	ut := saResolveUnionNode(t, aliasOf)
	if ut == nil || ut.Types == nil {
		return "", false
	}
	var variants []saUnionVariant
	for _, m := range ut.Types.Nodes {
		if saIsUnionNullMember(m) {
			continue
		}
		if !saCollectUnionVariant(m, aliasOf, classes, map[string]bool{}, &variants) {
			return "", false
		}
	}
	if len(variants) == 0 {
		return "", false
	}
	var fields []string
	fdefs := map[string]*ast.TypeNode{}
	fopt := map[string]bool{}
	fkinds := map[string]string{}
	fsub := map[string]string{}
	seenField := map[string]bool{}
	for _, v := range variants {
		for _, f := range v.fields {
			if !seenField[f.name] {
				seenField[f.name] = true
				fields = append(fields, f.name)
				fdefs[f.name] = f.fdef
			}
		}
	}
	if len(fields) == 0 {
		return "", false
	}
	for _, fname := range fields {
		presentInAll := true
		anyOpt := false
		var kind string
		var sub string
		var hasSub bool
		for _, v := range variants {
			var vf *saUnionVariantField
			for i := range v.fields {
				if v.fields[i].name == fname {
					vf = &v.fields[i]
					break
				}
			}
			if vf == nil {
				presentInAll = false
				continue
			}
			if vf.opt {
				anyOpt = true
			}
			var k string
			var ksub string
			var khasSub bool
			if vf.kind != "" {
				k = vf.kind
				ksub = vf.sub
				khasSub = vf.hasSub
			} else if vf.fdef != nil && vf.fdef.Kind == ast.KindLiteralType {
				lit := vf.fdef.AsLiteralTypeNode().Literal
				if lit == nil {
					return "", false
				}
				switch lit.Kind {
				case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
					k = "str"
				case ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword:
					k = "i32"
				default:
					return "", false
				}
			} else if vf.fdef != nil {
				probe := &saClassDef{fsub: map[string]string{}}
				k = saAnonFieldKindAlias(vf.fdef, fname, probe, classes, aliasOf)
				if s, ok := probe.fsub[fname]; ok {
					ksub = s
					khasSub = true
				}
			} else {
				return "", false
			}
			if kind == "" {
				kind = k
				sub = ksub
				hasSub = khasSub
			} else if kind != k {
				return "", false
			} else if khasSub && hasSub && sub != ksub {
				return "", false
			} else if khasSub && !hasSub {
				sub = ksub
				hasSub = true
			}
		}
		if kind == "" {
			return "", false
		}
		fkinds[fname] = kind
		if hasSub {
			fsub[fname] = sub
		}
		if !presentInAll || anyOpt {
			fopt[fname] = true
		}
	}
	base := "Union_" + saMonoName(strings.Join(fields, "_"))
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		if def, ok := classes[name]; ok {
			if saUnionLayoutEqual(def, fields, fkinds, fopt, fsub) {
				return def.name, true
			}
			continue
		}
		def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fopt: map[string]bool{}, fsub: map[string]string{}}
		off := 0
		for _, fname := range fields {
			fkind := fkinds[fname]
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: off})
			def.offsets[fname] = off
			def.fkinds[fname] = fkind
			def.fdefs[fname] = fdefs[fname]
			if fopt[fname] {
				def.fopt[fname] = true
			}
			if sub, ok := fsub[fname]; ok {
				def.fsub[fname] = sub
			}
			sz, _ := saFieldWidth(fkind)
			off += sz
		}
		def.size = off
		classes[name] = def
		return def.name, true
	}
}

// saSynthUnionRetLayout synthesizes an inst layout for a single-variant
// union return literal (JEV cfg296 jsval_field 0.92, lit_to_str 1.0,
// scope union_return_literal_only 0.48): the single non-null member must
// be a TypeLiteral. String literal fields resolve to str (mirroring
// saSynthUnionLayout's literal branch, which the nil-alias Anon path
// lacks), and fields declared with an unresolvable named type (plain
// TypeReference, no type args, in neither classes nor aliasOf) resolve
// to a jsval field kind (ptr slot holding a tagged jsval box; the
// object-literal jsval arm boxes at the store and saLowerClassFieldLoad
// marks reads jsval). The synthesis ONLY fires when at least one field
// changes kind versus saAnonFieldKindAlias (exactly the
// previously-refusing shapes); otherwise it returns false and the
// existing saUnionInstKind/saSynthUnionLayout paths run unchanged, so
// all currently-passing union returns are unaffected. Same-file
// interfaces are recorded in the harvest pass before function lowering,
// so an unrecorded name at return-kind time is genuinely external
// (e.g. JsonValue imported from another package), not a forward ref.
func saSynthUnionRetLayout(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode, classes map[string]*saClassDef) (string, bool) {
	ut := saResolveUnionNode(t, aliasOf)
	if ut == nil || ut.Types == nil {
		return "", false
	}
	var litMember *ast.TypeNode
	for _, m := range ut.Types.Nodes {
		if saIsUnionNullMember(m) {
			continue
		}
		if litMember != nil {
			return "", false
		}
		litMember = m
	}
	if litMember == nil || litMember.Kind != ast.KindTypeLiteral {
		return "", false
	}
	lit := litMember.AsTypeLiteralNode()
	if lit == nil || lit.Members == nil {
		return "", false
	}
	var fields []string
	fdefs := map[string]*ast.TypeNode{}
	fopt := map[string]bool{}
	for _, mm := range lit.Members.Nodes {
		if mm == nil || mm.Kind != ast.KindPropertySignature {
			continue
		}
		fn := mm.Name()
		if fn == nil || fn.Kind != ast.KindIdentifier {
			continue
		}
		if _, dup := fdefs[fn.Text()]; dup {
			continue
		}
		pd := mm.AsPropertySignatureDeclaration()
		if pd == nil || pd.Type == nil {
			continue
		}
		fields = append(fields, fn.Text())
		fdefs[fn.Text()] = pd.Type
		if mm.QuestionToken() != nil {
			fopt[fn.Text()] = true
		}
	}
	if len(fields) == 0 {
		return "", false
	}
	fkinds := map[string]string{}
	fsub := map[string]string{}
	changed := false
	for _, fname := range fields {
		fdef := fdefs[fname]
		var kind string
		if fdef != nil && fdef.Kind == ast.KindLiteralType {
			litn := fdef.AsLiteralTypeNode()
			if litn == nil || litn.Literal == nil {
				return "", false
			}
			switch litn.Literal.Kind {
			case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
				kind = "str"
			case ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword:
				kind = "i32"
			default:
				return "", false
			}
		} else {
			probe := &saClassDef{fsub: map[string]string{}}
			kind = saAnonFieldKindAlias(fdef, fname, probe, classes, aliasOf)
			if sub, ok := probe.fsub[fname]; ok {
				fsub[fname] = sub
			}
			if kind == "arr" && fdef != nil && fdef.Kind == ast.KindTypeReference {
				ref := fdef.AsTypeReferenceNode()
				if ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier && ref.TypeArguments == nil {
					nm := ref.TypeName.Text()
					if _, inClasses := classes[nm]; !inClasses {
						if _, inAlias := aliasOf[nm]; !inAlias {
							kind = "jsval"
						}
					}
				}
			}
			// JEV jvunion jsval_box 0.97, scope union_return_and_record 0.64:
			// a field whose type is a named alias resolving to a JSON union
			// (e.g. shared JsonValue) takes a jsval slot, restoring the
			// pre-harvest behaviour where the unresolvable name took jsval
			// via the rule above. The kind == "arr" guard keeps scalar-union
			// fields (str/i32 via saAliasUnionScalarKind) on their existing
			// kinds; direct union field types are untouched.
			if kind == "arr" && saIsJSONUnionAlias(fdef, aliasOf) {
				kind = "jsval"
			}
		}
		if kind == "" {
			return "", false
		}
		oldProbe := &saClassDef{fsub: map[string]string{}}
		if oldKind := saAnonFieldKindAlias(fdef, fname, oldProbe, classes, aliasOf); oldKind != kind {
			changed = true
		}
		fkinds[fname] = kind
	}
	if !changed {
		return "", false
	}
	base := "UnionRet_" + saMonoName(strings.Join(fields, "_"))
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		if def, ok := classes[name]; ok {
			if saUnionLayoutEqual(def, fields, fkinds, fopt, fsub) {
				return def.name, true
			}
			continue
		}
		def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fopt: map[string]bool{}, fsub: map[string]string{}}
		off := 0
		for _, fname := range fields {
			fkind := fkinds[fname]
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: off})
			def.offsets[fname] = off
			def.fkinds[fname] = fkind
			def.fdefs[fname] = fdefs[fname]
			if fopt[fname] {
				def.fopt[fname] = true
			}
			if sub, ok := fsub[fname]; ok {
				def.fsub[fname] = sub
			}
			sz, _ := saFieldWidth(fkind)
			off += sz
		}
		def.size = off
		classes[name] = def
		return def.name, true
	}
}

// saRestLayoutEqual 判定既有剩余布局与给定源域集同形（域序/种全等；
// 子布局名随种走，不等即不复用，后缀另建，与匿名合成同律）。
func saRestLayoutEqual(def, src *saClassDef, fields []string) bool {
	if def == nil || !def.isSynth || len(def.fields) != len(fields) {
		return false
	}
	for i, fname := range fields {
		if i >= len(def.fields) || def.fields[i].name != fname {
			return false
		}
		sk := src.fkinds[fname]
		if sk == "" {
			sk = "i32"
		}
		if def.fkinds[fname] != sk {
			return false
		}
	}
	return true
}

// saRestLayout 由源布局派生剩余字段子布局（对象剩余解构 `...rest` 用；
// 排除集外字段保源序，偏移按种重算，fdefs/fsub/fopt 透传；名按源名 +
// 剩余域名确定性派生，同形复用异形后缀；isSynth 故不参与键集匹配，
// 与字面量合成同律；空剩余即零域布局，alloc 侧走既有零尺寸口径）。
func saRestLayout(src *saClassDef, excluded map[string]bool, classes map[string]*saClassDef) *saClassDef {
	if src == nil {
		return nil
	}
	var fields []string
	for _, f := range src.fields {
		if !excluded[f.name] {
			fields = append(fields, f.name)
		}
	}
	base := "Rest_" + saMonoName(src.name)
	if len(fields) > 0 {
		base += "_" + saMonoName(strings.Join(fields, "_"))
	}
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		if def, ok := classes[name]; ok {
			if saRestLayoutEqual(def, src, fields) {
				return def
			}
			continue
		}
		def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}}
		off := 0
		for _, fname := range fields {
			fkind := src.fkinds[fname]
			if fkind == "" {
				fkind = "i32"
			}
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: off})
			def.offsets[fname] = off
			def.fkinds[fname] = fkind
			if src.fdefs != nil {
				def.fdefs[fname] = src.fdefs[fname]
			}
			if sub, ok := src.fsub[fname]; ok {
				if def.fsub == nil {
					def.fsub = map[string]string{}
				}
				def.fsub[fname] = sub
			}
			if src.fopt[fname] {
				if def.fopt == nil {
					def.fopt = map[string]bool{}
				}
				def.fopt[fname] = true
			}
			sz, _ := saFieldWidth(fkind)
			off += sz
		}
		def.size = off
		classes[name] = def
		return def
	}
}

// saRecordTypeAlias 记录对象字面量类型别名布局（`type X = {a: T, ...}`；
// 非字面量目标（联合/基元/引用）不记，含泛型形参别名不记（l5/l6 史诗级另立），
// 方法签名不占槽，与接口同形；嵌套具名域经已记录表解子布局（声明序依赖，
// 未记录落 arr 句柄，与接口未知引用同例）。形状证据：封存 recordTypeAlias
// 全形 + planck TransformValue/RotValue/Vec2Value 形。
func saRecordTypeAlias(st *ast.Node, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, pos func(int) (int, int), refusals *[]SARefusal) bool {
	ta := st.AsTypeAliasDeclaration()
	nm := st.Name()
	if ta == nil || ta.Type == nil || nm == nil || nm.Kind != ast.KindIdentifier {
		return true
	}
	name := nm.Text()
	if _, dup := classes[name]; dup {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate type " + name})
		return false
	}
	tgt := ta.Type
	if tgt == nil || tgt.Kind != ast.KindTypeLiteral {
		return true
	}
	if ta.TypeParameters != nil && len(ta.TypeParameters.Nodes) > 0 {
		return true
	}
	lit := tgt.AsTypeLiteralNode()
	if lit == nil || lit.Members == nil {
		return true
	}
	def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true}
	off := 0
	for _, m := range lit.Members.Nodes {
		if m.Kind != ast.KindPropertySignature {
			continue
		}
		fn := m.Name()
		if fn == nil || fn.Kind != ast.KindIdentifier {
			continue
		}
		pd := m.AsPropertySignatureDeclaration()
		fkind := "i32"
		if pd.Type != nil {
			if k, ok := saAnnotKind(pd.Type); ok && (k == "i32" || k == "bool" || k == "str" || k == "arr" || k == "f64") {
				if k == "str" {
					fkind = "str"
				} else if k == "arr" {
					fkind = "arr"
				} else if k == "f64" {
					fkind = "f64"
				}
			} else if ak, ok := saResolveAliasKind(pd.Type, aliasOf); ok && (ak == "i32" || ak == "bool" || ak == "str" || ak == "arr" || ak == "f64") {
				// Type-alias field (e.g. verbosity?: ModelVerbosity where
				// ModelVerbosity = "low" | "medium" | "high"): without this,
				// the unknown TypeReference fell through to "arr", so a
				// string field was later evaluated as i32/arr and refused.
				// saResolveAliasKind follows alias chains and folds literal
				// unions via saUnionScalarKind (str for string literals).
				if ak == "str" {
					fkind = "str"
				} else if ak == "arr" {
					fkind = "arr"
				} else if ak == "f64" {
					fkind = "f64"
				}
			} else if pd.Type.Kind == ast.KindTypeReference && pd.Type.AsTypeReferenceNode() != nil && pd.Type.AsTypeReferenceNode().TypeName != nil && pd.Type.AsTypeReferenceNode().TypeName.Kind == ast.KindIdentifier {
				if sub, ok := classes[pd.Type.AsTypeReferenceNode().TypeName.Text()]; ok {
					fkind = "inst"
					if def.fsub == nil {
						def.fsub = map[string]string{}
					}
					def.fsub[fn.Text()] = sub.name
				} else {
					fkind = "arr"
				}
			} else {
				fkind = "arr"
			}
		}
		if _, dup := def.offsets[fn.Text()]; dup {
			continue
		}
		off = saAlignOff(off, fkind)
		def.fields = append(def.fields, saClassField{name: fn.Text(), offset: off})
		def.offsets[fn.Text()] = off
		def.fkinds[fn.Text()] = fkind
		// par_k244: record the field type node (mirroring
		// saSynthAnonLayoutAlias). Without fdefs, the forward-fixup pass
		// cannot upgrade fields referencing imported interfaces, and the
		// empty-Record gate/materializer arms cannot see the Record type.
		if pd.Type != nil {
			if def.fdefs == nil {
				def.fdefs = map[string]*ast.TypeNode{}
			}
			def.fdefs[fn.Text()] = pd.Type
		}
		if m.QuestionToken() != nil {
			if def.fopt == nil {
				def.fopt = map[string]bool{}
			}
			def.fopt[fn.Text()] = true
		}
		sz, _ := saFieldWidth(fkind)
		off += sz
	}
	if len(def.fields) == 0 {
		return true
	}
	def.size = off
	classes[name] = def
	return true
}

// saAliasReady 报告别名是否可一次记（字面量目标的具名引用域须已入表，
// 否则推迟到表齐后；非字面量/泛型/标量可定种域恒真，沿既有语义一次记）。
func saAliasReady(st *ast.Node, classes map[string]*saClassDef) bool {
	ta := st.AsTypeAliasDeclaration()
	if ta == nil || ta.Type == nil {
		return true
	}
	tgt := ta.Type
	if tgt == nil || tgt.Kind != ast.KindTypeLiteral {
		return true
	}
	if ta.TypeParameters != nil && len(ta.TypeParameters.Nodes) > 0 {
		return true
	}
	lit := tgt.AsTypeLiteralNode()
	if lit == nil || lit.Members == nil {
		return true
	}
	for _, m := range lit.Members.Nodes {
		if m == nil || m.Kind != ast.KindPropertySignature {
			continue
		}
		fn := m.Name()
		if fn == nil || fn.Kind != ast.KindIdentifier {
			continue
		}
		pd := m.AsPropertySignatureDeclaration()
		if pd == nil || pd.Type == nil {
			continue
		}
		if k, ok := saAnnotKind(pd.Type); ok && (k == "i32" || k == "bool" || k == "str" || k == "arr" || k == "f64") {
			continue
		}
		if pd.Type.Kind != ast.KindTypeReference {
			continue
		}
		ref := pd.Type.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			continue
		}
		if _, ok := classes[ref.TypeName.Text()]; !ok {
			return false
		}
	}
	return true
}

// saMonoKey 渲染具化键（`Box<i32>`；裸名直返；封存 monoKey 同形）。
func saMonoKey(t *ast.TypeNode) string {
	if t == nil {
		return "i32"
	}
	if t.Kind != ast.KindTypeReference {
		if k, ok := saAnnotKind(t); ok {
			switch k {
			case "bool":
				return "i32"
			case "i32", "str", "arr":
				return k
			}
		}
		if t.Kind == ast.KindStringKeyword {
			return "string"
		}
		return "unknown"
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "unknown"
	}
	name := ref.TypeName.Text()
	if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) == 0 {
		return name
	}
	parts := make([]string, 0, len(ref.TypeArguments.Nodes))
	for _, a := range ref.TypeArguments.Nodes {
		parts = append(parts, saMonoKey(a))
	}
	return name + "<" + strings.Join(parts, ",") + ">"
}

// saMonoName 渲染 SA 安全的单态布局名（`Box<i32>` → `Box_i32`；尖括号逗号
// 非 SA 标识符，投影下划线；封存 instantiateLayout cacheKey 的本仓投影）。
func saMonoName(key string) string {
	var b strings.Builder
	for _, r := range key {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// saSubstArg 代入单实参（持参泛型引用非闭合；封存 substFieldType 闭合门同形）。
func saSubstArg(a *ast.TypeNode, pmap map[string]*ast.TypeNode) (*ast.TypeNode, bool) {
	if a != nil && a.Kind == ast.KindTypeReference {
		ref := a.AsTypeReferenceNode()
		if ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			if parg, ok := pmap[ref.TypeName.Text()]; ok {
				if parg == nil {
					return nil, false
				}
				if parg.Kind == ast.KindTypeReference {
					pr := parg.AsTypeReferenceNode()
					if pr != nil && pr.TypeArguments != nil && len(pr.TypeArguments.Nodes) > 0 {
						return nil, false
					}
				}
				return parg, true
			}
		}
	}
	return a, true
}

// saSubstFieldKind 代入求字段种（形参名经 pmap 取实参；闭引用递归具化；
// 参数化嵌套未闭合沿旧门 arr；封存 substFieldType 同形，唯本仓句柄种为 arr）。
func saSubstFieldKind(ftn *ast.TypeNode, pmap map[string]*ast.TypeNode, classes map[string]*saClassDef) (fkind, sub string) {
	if ftn != nil && ftn.Kind == ast.KindTypeReference {
		ref := ftn.AsTypeReferenceNode()
		if ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			name := ref.TypeName.Text()
			if arg, ok := pmap[name]; ok && arg != nil {
				if k, ok := saAnnotKind(arg); ok {
					switch k {
					case "i32", "bool":
						return "i32", ""
					case "str":
						return "str", ""
					case "arr":
						return "arr", ""
					case "f64":
						return "f64", ""
					}
				}
				if arg.Kind == ast.KindTypeReference {
					aref := arg.AsTypeReferenceNode()
					if aref != nil && aref.TypeName != nil && aref.TypeName.Kind == ast.KindIdentifier {
						var args []*ast.TypeNode
						if aref.TypeArguments != nil {
							args = aref.TypeArguments.Nodes
						}
						if lname, ok := saInstantiateIface(aref.TypeName.Text(), args, classes); ok {
							return "inst", lname
						}
					}
				}
				return "arr", ""
			}
			if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
				sub := make([]*ast.TypeNode, 0, len(ref.TypeArguments.Nodes))
				for _, a := range ref.TypeArguments.Nodes {
					sa, closed := saSubstArg(a, pmap)
					if !closed {
						return "arr", ""
					}
					sub = append(sub, sa)
				}
				if lname, ok := saInstantiateIface(name, sub, classes); ok {
					return "inst", lname
				}
				return "arr", ""
			}
		}
	}
	if k, ok := saAnnotKind(ftn); ok && (k == "i32" || k == "bool" || k == "str" || k == "arr" || k == "f64") {
		if k == "str" {
			return "str", ""
		}
		if k == "arr" {
			return "arr", ""
		}
		if k == "f64" {
			return "f64", ""
		}
		return "i32", ""
	}
	if ftn != nil && ftn.Kind == ast.KindTypeReference {
		if ref := ftn.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			if sub, ok := classes[ref.TypeName.Text()]; ok {
				return "inst", sub.name
			}
		}
	}
	return "arr", ""
}

// saInstantiateIface 单态具化泛型接口（模板 tparams/args 等长；字段代入重算
// 种与偏移；shell 先行保递归终止；结果缓存布局表；未知模板/元数错/键碰撞
// 沿旧门 false。封存 instantiateLayout 全形）。
func saInstantiateIface(name string, args []*ast.TypeNode, classes map[string]*saClassDef) (string, bool) {
	tmpl, ok := classes[name]
	if !ok || tmpl == nil {
		return "", false
	}
	if len(tmpl.tparams) == 0 {
		if len(args) != 0 {
			return "", false
		}
		return tmpl.name, true
	}
	if len(args) != len(tmpl.tparams) {
		return "", false
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, saMonoKey(a))
	}
	mangled := saMonoName(name + "<" + strings.Join(parts, ",") + ">")
	if def, ok := classes[mangled]; ok {
		if def.isMono {
			return def.name, true
		}
		return "", false
	}
	pmap := map[string]*ast.TypeNode{}
	for i, p := range tmpl.tparams {
		pmap[p] = args[i]
	}
	def := &saClassDef{name: mangled, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isMono: true}
	classes[mangled] = def
	off := 0
	for _, f := range tmpl.fields {
		// 继承展平域无模板节点：沿模板已算种直用（fsub 同步）。
		fkind, sub := tmpl.fkinds[f.name], tmpl.fsub[f.name]
		if fkind == "" {
			fkind = "i32"
			sub = ""
		}
		if fd, ok := tmpl.fdefs[f.name]; ok && fd != nil {
			fkind, sub = saSubstFieldKind(fd, pmap, classes)
		}
		off = saAlignOff(off, fkind)
		def.fields = append(def.fields, saClassField{name: f.name, offset: off})
		def.offsets[f.name] = off
		def.fkinds[f.name] = fkind
		if sub != "" {
			if def.fsub == nil {
				def.fsub = map[string]string{}
			}
			def.fsub[f.name] = sub
		}
		sz, _ := saFieldWidth(fkind)
		off += sz
	}
	def.size = off
	return def.name, true
}

// saMatchIface 按键集匹配唯一接口布局（0 或 2+ 匹配皆大声拒，确定性优先；
// 形状证据：封存 layoutOfLiteral:8953-8975 + matchLayout 名集匹配）。
func saMatchIface(keys []string, classes map[string]*saClassDef) (*saClassDef, string) {
	hits := saMatchIfaceList(keys, classes)
	if len(hits) > 1 {
		return nil, "object literal matches no unique recorded interface layout (declare the interface first)"
	}
	if len(hits) == 0 {
		return nil, "object literal matches no recorded interface layout (declare the interface first)"
	}
	return hits[0], ""
}

// saMappedAliasLayout 消解同构映射别名布局（`Partial/Required/Readonly<B>`
// 及用户同构 `{[K in keyof Y]: Y[K]}`；布局恒等于基接口 B，槽位不变；
// 别名链跟随（`P<T> = Partial<T>` 经占位实参透传，上限 4 跳防环）；
// 键过滤形（Pick/Omit/非常值 `as`）沿旧门；Y 须为实参名或未记录泛型占位）。
func saMappedAliasLayout(ref *ast.TypeReferenceNode, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 1 || ref.TypeArguments.Nodes[0] == nil {
		return "", false
	}
	arg := ref.TypeArguments.Nodes[0]
	if arg.Kind != ast.KindTypeReference {
		return "", false
	}
	an := arg.AsTypeReferenceNode()
	if an == nil || an.TypeName == nil || an.TypeName.Kind != ast.KindIdentifier || an.TypeArguments != nil {
		return "", false
	}
	base := an.TypeName.Text()
	bdef, ok := classes[base]
	if !ok || !bdef.isIface {
		return "", false
	}
	name := ref.TypeName.Text()
	for i := 0; i < 4; i++ {
		if name == "Partial" || name == "Required" || name == "Readonly" {
			return base, true
		}
		rhs, ok := aliasOf[name]
		if !ok || rhs == nil {
			return "", false
		}
		if rhs.Kind == ast.KindMappedType {
			if saHomomorphicMapped(rhs, base, classes) {
				return base, true
			}
			return "", false
		}
		// 别名链：`P<T> = Partial<T>` 占位透传（实参须为裸标识符，
		// 具象不透传防错位）。
		if rhs.Kind != ast.KindTypeReference {
			return "", false
		}
		rn := rhs.AsTypeReferenceNode()
		if rn == nil || rn.TypeName == nil || rn.TypeName.Kind != ast.KindIdentifier {
			return "", false
		}
		if rn.TypeArguments == nil || len(rn.TypeArguments.Nodes) != 1 || rn.TypeArguments.Nodes[0] == nil {
			return "", false
		}
		inner := rn.TypeArguments.Nodes[0]
		if inner.Kind != ast.KindTypeReference {
			return "", false
		}
		in := inner.AsTypeReferenceNode()
		if in == nil || in.TypeName == nil || in.TypeName.Kind != ast.KindIdentifier || in.TypeArguments != nil {
			return "", false
		}
		if _, recorded := classes[in.TypeName.Text()]; recorded {
			return "", false
		}
		name = rn.TypeName.Text()
	}
	return "", false
}

// saPickLayout synthesizes a layout for Pick<Base, keys> (JEV
// expand_utilities): the layout is the base layout with identical offsets
// and size, but every non-picked field marked optional (fopt), so a literal
// built with only the picked fields zero-fills the rest, and readers of
// picked fields use the base offsets. Base must be a recorded interface;
// unresolvable bases fall through to the caller's jsval fallback. The
// layout name embeds the picked keys, so different picks do not collide.
func saPickLayout(ref *ast.TypeReferenceNode, classes map[string]*saClassDef) (string, bool) {
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeName.Text() != "Pick" {
		return "", false
	}
	if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 2 || ref.TypeArguments.Nodes[0] == nil || ref.TypeArguments.Nodes[1] == nil {
		return "", false
	}
	baseRef := ref.TypeArguments.Nodes[0].AsTypeReferenceNode()
	if baseRef == nil || baseRef.TypeName == nil || baseRef.TypeName.Kind != ast.KindIdentifier || baseRef.TypeArguments != nil {
		return "", false
	}
	bdef, ok := classes[baseRef.TypeName.Text()]
	if !ok || !bdef.isIface {
		return "", false
	}
	keys, ok := saPickKeys(ref.TypeArguments.Nodes[1])
	if !ok || len(keys) == 0 {
		return "", false
	}
	picked := map[string]bool{}
	for _, k := range keys {
		picked[k] = true
	}
	for _, k := range keys {
		if _, ok := bdef.offsets[k]; !ok {
			return "", false
		}
	}
	name := "Pick_" + bdef.name + "_" + saMonoName(strings.Join(keys, "_"))
	if def, ok := classes[name]; ok {
		return def.name, true
	}
	def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fopt: map[string]bool{}, fsub: map[string]string{}}
	def.size = bdef.size
	for _, f := range bdef.fields {
		def.fields = append(def.fields, f)
		def.offsets[f.name] = f.offset
		def.fkinds[f.name] = bdef.fkinds[f.name]
		def.fdefs[f.name] = bdef.fdefs[f.name]
		if bdef.fsub != nil {
			def.fsub[f.name] = bdef.fsub[f.name]
		}
		if bdef.fopt[f.name] || !picked[f.name] {
			def.fopt[f.name] = true
		}
	}
	classes[name] = def
	return def.name, true
}

// saPartialLayout synthesizes a layout for Partial<Base> (JEV
// expand_utilities): identical offsets and size to the base layout, but
// every field marked optional (fopt), so a literal with only a subset of
// fields zero-fills the rest instead of failing the want-key match. This
// differs from saMappedAliasLayout, which returns the base layout itself
// (correct for Required/Readonly, wrong for Partial literals).
func saPartialLayout(ref *ast.TypeReferenceNode, classes map[string]*saClassDef) (string, bool) {
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeName.Text() != "Partial" {
		return "", false
	}
	if ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 1 || ref.TypeArguments.Nodes[0] == nil {
		return "", false
	}
	baseRef := ref.TypeArguments.Nodes[0].AsTypeReferenceNode()
	if baseRef == nil || baseRef.TypeName == nil || baseRef.TypeName.Kind != ast.KindIdentifier || baseRef.TypeArguments != nil {
		return "", false
	}
	bdef, ok := classes[baseRef.TypeName.Text()]
	if !ok || !bdef.isIface {
		return "", false
	}
	name := "Partial_" + bdef.name
	if def, ok := classes[name]; ok {
		return def.name, true
	}
	def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fopt: map[string]bool{}, fsub: map[string]string{}}
	def.size = bdef.size
	for _, f := range bdef.fields {
		def.fields = append(def.fields, f)
		def.offsets[f.name] = f.offset
		def.fkinds[f.name] = bdef.fkinds[f.name]
		def.fdefs[f.name] = bdef.fdefs[f.name]
		if bdef.fsub != nil {
			def.fsub[f.name] = bdef.fsub[f.name]
		}
		def.fopt[f.name] = true
	}
	classes[name] = def
	return def.name, true
}

// saPickKeys extracts string-literal keys from a Pick key type (a single
// string literal or a union of string literals, parenthesized tolerated).
func saPickKeys(t *ast.TypeNode) ([]string, bool) {
	if t == nil {
		return nil, false
	}
	if t.Kind == ast.KindParenthesizedType {
		if pt := t.AsParenthesizedTypeNode(); pt != nil {
			return saPickKeys(pt.Type)
		}
		return nil, false
	}
	if t.Kind == ast.KindLiteralType {
		if lit := t.AsLiteralTypeNode().Literal; lit != nil && lit.Kind == ast.KindStringLiteral {
			return []string{lit.Text()}, true
		}
		return nil, false
	}
	if t.Kind == ast.KindUnionType {
		ut := t.AsUnionTypeNode()
		if ut == nil || ut.Types == nil {
			return nil, false
		}
		var keys []string
		for _, m := range ut.Types.Nodes {
			ks, ok := saPickKeys(m)
			if !ok {
				return nil, false
			}
			keys = append(keys, ks...)
		}
		return keys, true
	}
	return nil, false
}

// saHomomorphicMapped 验证用户同构映射（`{[K in keyof Y]: Y[K]}`；`?`/只读
// 饰词容忍（槽位不变）；`as` 仅容无子句与 `as string`；Y 须为实参名或
// 未记录泛型占位，定目标异名拒）。
func saHomomorphicMapped(rhs *ast.TypeNode, base string, classes map[string]*saClassDef) bool {
	m := rhs.AsMappedTypeNode()
	if m == nil || m.TypeParameter == nil || m.Type == nil {
		return false
	}
	kn := m.TypeParameter.Name()
	if kn == nil || kn.Kind != ast.KindIdentifier {
		return false
	}
	kname := kn.Text()
	tpd := m.TypeParameter.AsTypeParameterDeclaration()
	if tpd == nil || tpd.Constraint == nil || tpd.Constraint.Kind != ast.KindTypeOperator {
		return false
	}
	top := tpd.Constraint.AsTypeOperatorNode()
	if top == nil || top.Operator != ast.KindKeyOfKeyword || top.Type == nil ||
		top.Type.Kind != ast.KindTypeReference {
		return false
	}
	kt := top.Type.AsTypeReferenceNode()
	if kt == nil || kt.TypeName == nil || kt.TypeName.Kind != ast.KindIdentifier {
		return false
	}
	yname := kt.TypeName.Text()
	if yname != base {
		// 泛型占位（未记录名，视为别名自身形参；已记录异名系定目标映射，拒）。
		if _, recorded := classes[yname]; recorded {
			return false
		}
	}
	vt := m.Type
	if vt == nil || vt.Kind != ast.KindIndexedAccessType {
		return false
	}
	ia := vt.AsIndexedAccessTypeNode()
	if ia == nil || ia.ObjectType == nil || ia.ObjectType.Kind != ast.KindTypeReference ||
		ia.IndexType == nil || ia.IndexType.Kind != ast.KindTypeReference {
		return false
	}
	obj := ia.ObjectType.AsTypeReferenceNode()
	idx := ia.IndexType.AsTypeReferenceNode()
	if obj == nil || obj.TypeName == nil || obj.TypeName.Kind != ast.KindIdentifier ||
		obj.TypeName.Text() != yname ||
		idx == nil || idx.TypeName == nil || idx.TypeName.Kind != ast.KindIdentifier ||
		idx.TypeName.Text() != kname {
		return false
	}
	// `as` 重映射仅容无子句与 `as string`（键集不变；余形拒）。
	if m.NameType != nil {
		if m.NameType.Kind != ast.KindStringKeyword {
			return false
		}
	}
	return true
}

// saIfaceKeyMatch 报告字面量键集是否命中布局（全键在表 + 缺键全可选；
// 余键/缺必备/合成布局沿旧门；多命中决胜由调用方既有门保障）。
// saWantKeyMatch 注解直命中的键集核对（允许合成布局：注解显式点名该布局，
// 即用户自己写出的形状，匹配它正确；键集命中的通用路径仍排除合成布局，
// 禁误吞多命中门）。
func saWantKeyMatch(def *saClassDef, keys []string) bool {
	if def == nil || !def.isIface {
		return false
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if _, has := def.offsets[k]; !has {
			return false
		}
		seen[k] = true
	}
	for _, f := range def.fields {
		if !seen[f.name] && !def.fopt[f.name] {
			return false
		}
	}
	return true
}

func saIfaceKeyMatch(def *saClassDef, keys []string) bool {
	if def == nil || !def.isIface || def.isSynth {
		return false
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if _, has := def.offsets[k]; !has {
			return false
		}
		seen[k] = true
	}
	for _, f := range def.fields {
		if !seen[f.name] && !def.fopt[f.name] {
			return false
		}
	}
	return true
}

// saMatchIfaceList 列出键集全命中的接口布局（saMatchIface 的无拒因版，
// 供值种决胜复用；拒因文案由调用方沿既有用语）。
func saMatchIfaceList(keys []string, classes map[string]*saClassDef) []*saClassDef {
	var hits []*saClassDef
	for _, def := range classes {
		if !saIfaceKeyMatch(def, keys) {
			continue
		}
		hits = append(hits, def)
	}
	return hits
}

// saLitFieldKind 初判字面量域值种（i32/str；余下空；封存 checker 值种直觉的
// 语法子集：字面量与已定种绑定名，不做求值）。
func saLitFieldKind(init *ast.Node, scope *saScope) string {
	if init == nil {
		return ""
	}
	switch init.Kind {
	case ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword:
		return "i32"
	case ast.KindIdentifier:
		if k, ok := scope.types[init.Text()]; ok && (k == "i32" || k == "bool") {
			return "i32"
		}
		return ""
	}
	if saIsStrExpr(init, scope) {
		return "str"
	}
	return ""
}

// saMatchWantIface 注解优先直命中（名在表、键集命中（缺键全可选）即用；
// 其余一律nil 交既有匹配门，拒因文案零变）。
// saWantKeySubset reports whether every literal key exists in the layout.
// Unlike saWantKeyMatch it does not require non-optional fields to be
// present: the materializer zero-fills missing fields, so a partial
// literal (e.g. engine: { phase: "idle" } with extra Engine fields) still
// lowers correctly. Extra keys still fail.
func saWantKeySubset(def *saClassDef, keys []string) bool {
	if def == nil || !def.isIface {
		return false
	}
	for _, k := range keys {
		if _, has := def.offsets[k]; !has {
			return false
		}
	}
	return true
}

func saMatchWantIface(want string, keys []string, classes map[string]*saClassDef) (*saClassDef, string) {
	if want == "" {
		return nil, ""
	}
	def, ok := classes[want]
	if !ok || def == nil || !def.isIface || !saWantKeySubset(def, keys) {
		return nil, ""
	}
	return def, ""
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
	case ast.KindNumericLiteral:
		// (2b_unannot2, JEV numeric_key) bare numeric keys (`50: ...`)
		// are string keys in JS; the text is the field name.
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

// saTopObj 快照（接口布局名 + 字面量初值节点；用点经 saLowerObjectLiteral
// 现场具化，快照语义）。
type saTopObj struct {
	layout string
	init   *ast.Node
	// arrObjs/arrLayouts 为内联类型对象 + 数组对象域形（JEV
	// synth_layout_arr_fields 0.84）：每域为一对象数组快照，值
	// 为该域诸元解析，布局为该域元合成布局；标量形此二域为空。
	arrObjs    map[string][]saTopObjElem
	arrLayouts map[string]string
	// arrStrFields/objFieldClaims (par_notexp, JEV idfield_snapshot
	// 0.96): top-level identifier fields resolved at defining-file
	// prescan time: string-array field value copies (arr slots) and
	// nested object claims (inst slots). They travel with the claim
	// struct through seeding, so importer materialization never looks
	// the defining-file name up (same-named importer locals cannot
	// silently supply wrong values); empty means no identifier fields.
	arrStrFields   map[string][]string
	objFieldClaims map[string]saTopObj
}

// saTopObjLitLayout 为不可解析注解的顶层对象字面量合成布局（JEV
// topobj_partial_synth 的第二半）：注解为无类型实参的命名引用
// （文件内未记录的接口名，或 React.CSSProperties 式限定名），初值
// 为全 i32/串字面量域的对象字面量时，复用既有 saSynthTopObjLayout
// 按域排序合成布局（isSynth，不参与键集匹配）；泛型注解
// （Record<...> 等）、非字面量域、重名域、空字面量一律不收，
// 沿旧 kind-244 门。合成布局写入 prescan 共用的 classes 表，
// 用点字段读与 saLowerObjectLiteral 同形；合成布局无 fopt，
// 故字面量域须齐全。
// spread 形（JEV merge_at_prescan 0.94）：SpreadAssignment 源须为同文件
// 已认领顶层对象快照（prev，即 prescan 已累积 out），其布局域须全为
// i32/str；展开域先并入，显式域后写覆盖（JS 语义），同名显式域仍
// 按重名拒；未认领/跨文件/含非 i32/str 域的源沿旧门大声拒。
func saTopObjLitLayout(vd *ast.VariableDeclaration, classes map[string]*saClassDef, prev map[string]saTopObj) (string, bool) {
	if vd == nil || vd.Type == nil || vd.Initializer == nil {
		return "", false
	}
	if vd.Type.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := vd.Type.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil {
		return "", false
	}
	if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
		return "", false
	}
	if ref.TypeName.Kind != ast.KindIdentifier && ref.TypeName.Kind != ast.KindQualifiedName {
		return "", false
	}
	if ref.TypeName.Kind == ast.KindIdentifier {
		if _, ok := classes[ref.TypeName.Text()]; ok {
			return "", false
		}
	}
	init := saUnwrapTransparent(vd.Initializer)
	if init == nil || init.Kind != ast.KindObjectLiteralExpression {
		return "", false
	}
	ol := init.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil || len(ol.Properties.Nodes) == 0 {
		return "", false
	}
	var fields []string
	fkinds := map[string]string{}
	ownSeen := map[string]bool{}
	for _, p := range ol.Properties.Nodes {
		if p == nil {
			return "", false
		}
		if p.Kind == ast.KindSpreadAssignment {
			se := saUnwrapTransparent(p.AsSpreadAssignment().Expression.AsNode())
			if se == nil || se.Kind != ast.KindIdentifier {
				return "", false
			}
			srcTo, ok := prev[se.Text()]
			if !ok {
				return "", false
			}
			sdef, ok := classes[srcTo.layout]
			if !ok || sdef == nil {
				return "", false
			}
			for _, f := range sdef.fields {
				if fk := sdef.fkinds[f.name]; fk != "i32" && fk != "str" && fk != "f64" && fk != "arr" {
					return "", false
				}
				if _, seen := fkinds[f.name]; !seen {
					fields = append(fields, f.name)
				}
				fkinds[f.name] = sdef.fkinds[f.name]
			}
			continue
		}
		if p.Kind != ast.KindPropertyAssignment {
			return "", false
		}
		fname, ok := saObjPropName(p)
		if !ok || fname == "" {
			return "", false
		}
		if ownSeen[fname] {
			return "", false
		}
		ownSeen[fname] = true
		finit := p.AsPropertyAssignment().Initializer
		_, wasSpread := fkinds[fname]
		if _, iok := saTopArrElemText(finit); iok {
			fkinds[fname] = "i32"
		} else if _, sok := saTopArrStrText(finit); sok {
			fkinds[fname] = "str"
		} else {
			return "", false
		}
		if !wasSpread {
			fields = append(fields, fname)
		}
	}
	sort.Strings(fields)
	return saSynthTopObjLayout(fields, fkinds, map[string]bool{}, map[string]string{}, classes), true
}

// saTopObjUnannotLayoutOf synthesizes a layout for one unannotated object
// literal under the lit rule (JEV unannot_flat lit_synth 0.64, scope
// flat_nested 0.67): string literals -> str, integer literals (and
// true/false, +/- integer prefixes) -> i32 via the existing
// saTopArrElemText/saTopArrStrText helpers, nested object literals ->
// inst slots behind recursively synthesized sub-layouts. Floats, arrays,
// identifiers, nulls, methods, shorthand, computed keys, spread from a
// layout with non-i32/str fields, and empty literals all return false,
// so the statement keeps refusing kind-244 loudly. Depth-guarded.
func saTopObjUnannotLayoutOf(ol *ast.ObjectLiteralExpression, classes map[string]*saClassDef, prev map[string]saTopObj, topArrs map[string]saTopArr, depth int, arrStr map[string][]string, objClaims map[string]saTopObj) (string, bool) {
	if ol == nil || ol.Properties == nil || len(ol.Properties.Nodes) == 0 || depth > 8 {
		return "", false
	}
	var fields []string
	fkinds := map[string]string{}
	subs := map[string]string{}
	ownSeen := map[string]bool{}
	for _, p := range ol.Properties.Nodes {
		if p == nil {
			return "", false
		}
		if p.Kind == ast.KindSpreadAssignment {
			se := saUnwrapTransparent(p.AsSpreadAssignment().Expression.AsNode())
			if se == nil || se.Kind != ast.KindIdentifier {
				return "", false
			}
			srcTo, ok := prev[se.Text()]
			if !ok {
				return "", false
			}
			sdef, ok := classes[srcTo.layout]
			if !ok || sdef == nil {
				return "", false
			}
			for _, f := range sdef.fields {
				if fk := sdef.fkinds[f.name]; fk != "i32" && fk != "str" && fk != "f64" && fk != "arr" {
					return "", false
				}
				if _, seen := fkinds[f.name]; !seen {
					fields = append(fields, f.name)
				}
				fkinds[f.name] = sdef.fkinds[f.name]
			}
			continue
		}
		if p.Kind != ast.KindPropertyAssignment {
			return "", false
		}
		fname, ok := saObjPropName(p)
		if !ok || fname == "" {
			return "", false
		}
		if ownSeen[fname] {
			return "", false
		}
		ownSeen[fname] = true
		finit := p.AsPropertyAssignment().Initializer
		if finit == nil {
			return "", false
		}
		if _, iok := saTopArrElemText(finit); iok {
			fkinds[fname] = "i32"
		} else if _, sok := saTopArrStrText(finit); sok {
			fkinds[fname] = "str"
		} else if _, fok := saTopFloatText(finit); fok {
			// (par_notexp, JEV float_f64) 浮点字面量域 f64 化；抽取值
			// 已由 saTopObjElemParse 进 numFields，物化按 fkind 发 f64 存。
			fkinds[fname] = "f64"
		} else if finit.Kind == ast.KindIdentifier && depth == 0 && arrStr != nil && objClaims != nil {
			// (par_notexp, JEV idfield_snapshot) 顶层标识符域：解
			// 同文件已认领快照——对象快照落 inst+子布局，串数组
			// 快照落 arr 槽并值拷贝进收集表（供跨文件物化）；嵌套
			// 字面量内标识符域（depth>0）仍沿旧门；未解出大声拒。
			nm := finit.Text()
			if prevTo, ok := prev[nm]; ok {
				if sdef, ok2 := classes[prevTo.layout]; ok2 && sdef != nil {
					fkinds[fname] = "inst"
					subs[fname] = prevTo.layout
					objClaims[fname] = prevTo
				} else {
					return "", false
				}
			} else if arr, ok := topArrs[nm]; ok && arr.str {
				fkinds[fname] = "arr"
				cp := append([]string(nil), arr.elems...)
				arrStr[fname] = cp
			} else {
				return "", false
			}
		} else if finit.Kind == ast.KindObjectLiteralExpression {
			subOl := finit.AsObjectLiteralExpression()
			subLay, ok := saTopObjUnannotLayoutOf(subOl, classes, prev, topArrs, depth+1, nil, nil)
			if !ok {
				return "", false
			}
			fkinds[fname] = "inst"
			subs[fname] = subLay
		} else {
			return "", false
		}
		found := false
		for _, f := range fields {
			if f == fname {
				found = true
				break
			}
		}
		if !found {
			fields = append(fields, fname)
		}
	}
	sort.Strings(fields)
	return saSynthTopObjLayout(fields, fkinds, map[string]bool{}, subs, classes), true
}

// saTopObjUnannotLayout claims an unannotated top-level const object
// (vd.Type == nil) by synthesizing its layout from literal field kinds.
// Annotated declarations never reach it: the prescan tries it only when
// vd.Type == nil, so annotated behavior is byte-identical.
func saTopObjUnannotLayout(vd *ast.VariableDeclaration, classes map[string]*saClassDef, prev map[string]saTopObj, topArrs map[string]saTopArr) (string, map[string][]string, map[string]saTopObj, bool) {
	if vd == nil || vd.Type != nil || vd.Initializer == nil {
		return "", nil, nil, false
	}
	init := saUnwrapTransparent(vd.Initializer)
	if init == nil || init.Kind != ast.KindObjectLiteralExpression {
		return "", nil, nil, false
	}
	ol := init.AsObjectLiteralExpression()
	if ol == nil {
		return "", nil, nil, false
	}
	arrStr := map[string][]string{}
	objClaims := map[string]saTopObj{}
	lay, ok := saTopObjUnannotLayoutOf(ol, classes, prev, topArrs, 0, arrStr, objClaims)
	if !ok {
		return "", nil, nil, false
	}
	if len(arrStr) == 0 {
		arrStr = nil
	}
	if len(objClaims) == 0 {
		objClaims = nil
	}
	return lay, arrStr, objClaims, true
}

// saNullFieldTypeNode resolves one field's annotation TypeNode by walking an
// annotation through aliasOf/intersection/literal constituents (first wins,
// cycle-guarded). Recorded interfaces carry no fdefs (saRecordTypeAlias
// never sets them), so their fields are recovered from the alias literal
// when one exists; otherwise it returns nil.
func saNullFieldTypeNode(t *ast.TypeNode, fname string, aliasOf map[string]*ast.TypeNode, classes map[string]*saClassDef, seen map[string]bool, depth int) *ast.TypeNode {
	if t == nil || depth > 8 {
		return nil
	}
	switch t.Kind {
	case ast.KindParenthesizedType:
		pt := t.AsParenthesizedTypeNode()
		if pt == nil {
			return nil
		}
		return saNullFieldTypeNode(pt.Type, fname, aliasOf, classes, seen, depth+1)
	case ast.KindTypeLiteral:
		lit := t.AsTypeLiteralNode()
		if lit == nil || lit.Members == nil {
			return nil
		}
		for _, m := range lit.Members.Nodes {
			if m == nil || m.Kind != ast.KindPropertySignature {
				continue
			}
			nm := m.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() != fname {
				continue
			}
			pd := m.AsPropertySignatureDeclaration()
			if pd == nil {
				return nil
			}
			return pd.Type
		}
		return nil
	case ast.KindIntersectionType:
		it := t.AsIntersectionTypeNode()
		if it == nil || it.Types == nil {
			return nil
		}
		for _, m := range it.Types.Nodes {
			if m == nil {
				continue
			}
			if got := saNullFieldTypeNode(m, fname, aliasOf, classes, seen, depth+1); got != nil {
				return got
			}
		}
		return nil
	case ast.KindTypeReference:
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
			return nil
		}
		if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
			return nil
		}
		nm := ref.TypeName.Text()
		if seen[nm] {
			return nil
		}
		seen[nm] = true
		tgt, ok := aliasOf[nm]
		if !ok || tgt == nil {
			return nil
		}
		return saNullFieldTypeNode(tgt, fname, aliasOf, classes, seen, depth+1)
	default:
		return nil
	}
}

// saTopObjNullLayout claims top-level const objects whose annotation is an
// unrecorded alias resolving (through aliasOf) to an intersection or type
// literal, and whose literal fields are nulls (kind from the merged
// annotation layout), i32/str literals (cross-checked like the interface
// path), or empty object literals for Record-typed fields (materialized as
// empty maps by the object-literal map arm). JEV kind244_empty:
// null_annotated_str 0.91, scope lit_intersection_only 0.83. Anything else
// returns false and the statement keeps refusing kind-244 loudly.
func saTopObjNullLayout(vd *ast.VariableDeclaration, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, bool) {
	if vd == nil || vd.Type == nil || vd.Initializer == nil {
		return "", false
	}
	if vd.Type.Kind != ast.KindTypeReference {
		return "", false
	}
	ref := vd.Type.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil {
		return "", false
	}
	if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
		return "", false
	}
	if ref.TypeName.Kind != ast.KindIdentifier {
		return "", false
	}
	if _, ok := classes[ref.TypeName.Text()]; ok {
		return "", false
	}
	init := saUnwrapTransparent(vd.Initializer)
	if init == nil || init.Kind != ast.KindObjectLiteralExpression {
		return "", false
	}
	ol := init.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil || len(ol.Properties.Nodes) == 0 {
		return "", false
	}
	lay, ok := saSynthMergedLayout(vd.Type, aliasOf, classes)
	if !ok {
		return "", false
	}
	def, ok := classes[lay]
	if !ok || def == nil || !def.isIface {
		return "", false
	}
	var keys []string
	seen := map[string]bool{}
	for _, p := range ol.Properties.Nodes {
		if p == nil || p.Kind != ast.KindPropertyAssignment {
			return "", false
		}
		fname, ok := saObjPropName(p)
		if !ok || fname == "" || seen[fname] {
			return "", false
		}
		seen[fname] = true
		keys = append(keys, fname)
		fk, ok := def.fkinds[fname]
		if !ok {
			return "", false
		}
		finit := p.AsPropertyAssignment().Initializer
		switch {
		case finit != nil && (finit.Kind == ast.KindNullKeyword || finit.Kind == ast.KindUndefinedKeyword):
			if fk != "str" && fk != "i32" && fk != "bool" {
				return "", false
			}
		case finit != nil && finit.Kind == ast.KindObjectLiteralExpression:
			sub := finit.AsObjectLiteralExpression()
			if sub == nil || sub.Properties == nil || len(sub.Properties.Nodes) != 0 {
				return "", false
			}
			if fk != "arr" {
				return "", false
			}
			fnode := saNullFieldTypeNode(vd.Type, fname, aliasOf, classes, map[string]bool{}, 0)
			if fnode == nil {
				return "", false
			}
			if _, ok := saRecordValueKind(fnode, classes, aliasOf); !ok {
				return "", false
			}
			if _, ok := def.fdefs[fname]; !ok || def.fdefs[fname] == nil {
				if def.fdefs == nil {
					def.fdefs = map[string]*ast.TypeNode{}
				}
				def.fdefs[fname] = fnode
			}
		default:
			litKind := ""
			if _, iok := saTopArrElemText(finit); iok {
				litKind = "i32"
			} else if _, sok := saTopArrStrText(finit); sok {
				litKind = "str"
			} else {
				return "", false
			}
			if litKind == "i32" {
				if fk != "i32" && fk != "bool" {
					return "", false
				}
			} else if fk != "str" {
				return "", false
			}
		}
	}
	if !saWantKeyMatch(def, keys) {
		return "", false
	}
	return lay, true
}

// saTopObjPrescan 收顶层 `const O: P = {...}` 对象快照（名→快照；接口先行：// saTopObjPrescan 收顶层 `const O: P = {...}` 对象快照（名→快照；接口先行：
// 注解须为已记录接口；键集须与布局偏移键集精确相等；字段初值须全为 i32/串
// 直接量且与布局种交叉一致（注错种 TS 本错，此处亦拒）；简写/计算键/spread/
// 方法余形整句不收，沿旧 kind244 门；788）。
// saTopObjAnnotPropOK reports whether one annotated-interface field value is
// claimable as a topObj snapshot field (JEV extend_annotated_obj 0.97):
// scalar literals keep the old i32/str rules; null/undefined stores as the 0
// handle in any ptr/i32 slot (str/i32 arms already emit 0, the inst arm gains
// a null case below, jsval boxes null); a nested object literal is allowed
// only for an inst field with a recorded sub layout whose key set matches and
// whose own fields pass this same check (depth-capped); an array literal is
// allowed only for an arr field with scalar-or-empty elements; an empty object
// literal is allowed only for a Record-typed arr field (the empty-map arm in
// saLowerObjectLiteral). Float literals stay refused: interface number fields
// are i32 slots. Anything else (identifiers, calls, spreads, shorthand) keeps
// the old refusal, so a claim here always has a materialization path.
func saTopObjAnnotPropOK(finit *ast.Node, fk, fname string, def *saClassDef, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, depth int) bool {
	if finit == nil || def == nil {
		return false
	}
	if _, iok := saTopArrElemText(finit); iok {
		return fk == "i32" || fk == "bool"
	}
	if _, sok := saTopArrStrText(finit); sok {
		return fk == "str"
	}
	if finit.Kind == ast.KindNullKeyword || finit.Kind == ast.KindUndefinedKeyword {
		switch fk {
		case "str", "i32", "bool", "inst", "arr", "jsval":
			return true
		}
		return false
	}
	if finit.Kind == ast.KindArrayLiteralExpression {
		if fk != "arr" {
			return false
		}
		al := finit.AsArrayLiteralExpression()
		if al == nil || al.Elements == nil {
			return true
		}
		for _, el := range al.Elements.Nodes {
			if el == nil {
				return false
			}
			if _, ok := saTopArrElemText(el); ok {
				continue
			}
			if _, ok := saTopArrStrText(el); ok {
				continue
			}
			if el.Kind == ast.KindTrueKeyword || el.Kind == ast.KindFalseKeyword {
				continue
			}
			return false
		}
		return true
	}
	if finit.Kind == ast.KindObjectLiteralExpression {
		if fk == "arr" {
			ol := finit.AsObjectLiteralExpression()
			if ol == nil || ol.Properties == nil || len(ol.Properties.Nodes) != 0 {
				return false
			}
			if fdef, ok := def.fdefs[fname]; ok && fdef != nil {
				if _, ok := saRecordValueKind(fdef, classes, aliasOf); ok {
					return true
				}
				// JEV synth_alias_layout: an empty {} claims as an empty
				// map even when the value kind is unparseable (no value
				// kind is needed to materialize an empty map). The
				// materializer arm mirrors this check.
				if fdef.Kind == ast.KindTypeReference {
					if ref := fdef.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier &&
						(ref.TypeName.Text() == "Record" || ref.TypeName.Text() == "Map") {
						return true
					}
				}
			}
			return false
		}
		if fk != "inst" || depth >= 8 {
			return false
		}
		subName, ok := def.fsub[fname]
		if !ok || subName == "" {
			return false
		}
		sub, ok := classes[subName]
		if !ok || sub == nil {
			return false
		}
		ol := finit.AsObjectLiteralExpression()
		if ol == nil || ol.Properties == nil {
			return false
		}
		var keys []string
		for _, p := range ol.Properties.Nodes {
			if p == nil || p.Kind != ast.KindPropertyAssignment {
				return false
			}
			fn, ok := saObjPropName(p)
			if !ok {
				return false
			}
			sfk, ok := sub.fkinds[fn]
			if !ok {
				return false
			}
			if !saTopObjAnnotPropOK(p.AsPropertyAssignment().Initializer, sfk, fn, sub, classes, aliasOf, depth+1) {
				return false
			}
			keys = append(keys, fn)
		}
		// Subset acceptance: every present key was validated above, and
		// missing sub-fields zero-fill on materialization (saLowerObjectLiteral
		// defaults), matching the top-level gate, which iterates literal
		// properties only. saWantKeyMatch would additionally require every
		// non-optional sub-field to be present, which the materializer does
		// not require (e.g. engine: { phase: "idle" } with extra Engine fields).
		_ = keys
		return true
	}
	return false
}

func saTopObjPrescan(stmts []*ast.Node, funcs map[string]saFuncSig, classes map[string]*saClassDef, assigned map[string]bool, aliasOf map[string]*ast.TypeNode, topArrs map[string]saTopArr) (map[string]saTopObj, map[*ast.Node]bool) {
	out := map[string]saTopObj{}
	claimed := map[*ast.Node]bool{}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindVariableStatement {
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
		// const 永收；let/var 须全 declarator 未被赋值（等价 const 快照；818）。
		isConst := vs.DeclarationList.AsNode().Flags&ast.NodeFlagsConst != 0
		if vs.DeclarationList.AsNode().Flags&ast.NodeFlagsUsing != 0 {
			continue
		}
		type pend struct {
			name string
			obj  saTopObj
		}
		var pends []pend
		okAll := true
		for _, d := range vdl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil || vd.Initializer == nil {
				okAll = false
				break
			}
			nm := vd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				okAll = false
				break
			}
			name := nm.Text()
			if _, dup := out[name]; dup {
				okAll = false
				break
			}
			if _, dup := funcs[name]; dup {
				okAll = false
				break
			}
			// 非 const 声明须未被赋值（等价 const 快照；818）。
			if !isConst && assigned[name] {
				okAll = false
				break
			}
			// 无注解径（JEV unannot_flat）：注解缺省时试字面量合成布局；
			// 成则本声明直接认领，败则沿旧门。
			if vd.Type == nil {
				if lay, arrStr, objClaims, okU := saTopObjUnannotLayout(vd, classes, out, topArrs); okU {
					initU := saUnwrapTransparent(vd.Initializer)
					pends = append(pends, pend{name: name, obj: saTopObj{layout: lay, init: initU, arrStrFields: arrStr, objFieldClaims: objClaims}})
					continue
				}
				okAll = false
				break
			}
			layout := ""
			if vd.Type.Kind == ast.KindTypeReference {
				if ref := vd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil &&
					ref.TypeName.Kind == ast.KindIdentifier {
					layout = ref.TypeName.Text()
				}
			}
			def, ok := classes[layout]
			if !ok || def == nil || !def.isIface {
				// 内联类型对象 + 数组对象域形（JEV
				// synth_layout_arr_fields 0.84）：标量接口径不收
				// 时，试数组域径；成则本声明直接认领，败则沿旧门。
				if to2, ok2 := saTopObjArrFieldsParse(vd, classes); ok2 {
					pends = append(pends, pend{name: name, obj: to2})
					continue
				}
				if lay, ok3 := saTopObjLitLayout(vd, classes, out); ok3 {
					init3 := saUnwrapTransparent(vd.Initializer)
					pends = append(pends, pend{name: name, obj: saTopObj{layout: lay, init: init3}})
					continue
				}
				if lay, ok4 := saTopObjNullLayout(vd, classes, aliasOf); ok4 {
					init4 := saUnwrapTransparent(vd.Initializer)
					pends = append(pends, pend{name: name, obj: saTopObj{layout: lay, init: init4}})
					continue
				}
				// (par_notexp) 别名-字面量注解：经 aliasOf 解出合成布局后
				// 落入下方的接口域检查（域须全为字面量且键集匹配）。
				if lay, ok5 := saAliasLiteralLayout(vd.Type, classes, aliasOf); ok5 {
					if d2, ok6 := classes[lay]; ok6 && d2 != nil && d2.isIface {
						layout, def = lay, d2
					}
				}
				if def == nil || !def.isIface {
					okAll = false
					break
				}
			}
			init := saUnwrapTransparent(vd.Initializer)
			if init == nil || init.Kind != ast.KindObjectLiteralExpression {
				okAll = false
				break
			}
			ol := init.AsObjectLiteralExpression()
			if ol == nil || ol.Properties == nil {
				okAll = false
				break
			}
			seen := map[string]bool{}
			propOk := true
			floatMismatch := false
			for _, p := range ol.Properties.Nodes {
				if p == nil || p.Kind != ast.KindPropertyAssignment {
					propOk = false
					break
				}
				fname, ok := saObjPropName(p)
				if !ok {
					propOk = false
					break
				}
				finit := p.AsPropertyAssignment().Initializer
				litKind := ""
				if _, iok := saTopArrElemText(finit); iok {
					litKind = "i32"
				} else if _, sok := saTopArrStrText(finit); sok {
					litKind = "str"
				} else if _, fok := saTopFloatText(finit); fok {
					litKind = "f64"
				}
				fk, ok := def.fkinds[fname]
				if !ok {
					propOk = false
					break
				}
				if litKind != "" {
					if litKind == "i32" {
						if fk != "i32" && fk != "bool" {
							propOk = false
							break
						}
					} else if litKind == "f64" {
						// (par_notexp, JEV float_f64) 注解 number 落 i32
						// 但字面量为浮点：不截断，记失配走下文字面量合成
						// 径（共享布局不动）；注解已为 f64 则直认。
						if fk == "f64" {
						} else if fk == "i32" {
							floatMismatch = true
						} else {
							propOk = false
							break
						}
					} else if fk != "str" {
						propOk = false
						break
					}
				} else if !saTopObjAnnotPropOK(finit, fk, fname, def, classes, aliasOf, 0) {
					propOk = false
					break
				}
				seen[fname] = true
			}
			var wantKeys []string
			for k := range seen {
				wantKeys = append(wantKeys, k)
			}
			// 缺省域与 saWantKeyMatch 同口径：必备域缺省沿旧门，
			// 可选域缺省由物化零填（JEV topobj_partial_synth）。
			if !propOk || !saWantKeyMatch(def, wantKeys) {
				okAll = false
				break
			}
			if floatMismatch {
				// (par_notexp, JEV float_f64) 注解布局不动：字面量合
				// 成布局认领本声明；合成败则沿旧门。
				arrStrF := map[string][]string{}
				objClaimsF := map[string]saTopObj{}
				if synthLay, okS := saTopObjUnannotLayoutOf(ol, classes, out, topArrs, 0, arrStrF, objClaimsF); okS {
					if len(arrStrF) == 0 {
						arrStrF = nil
					}
					if len(objClaimsF) == 0 {
						objClaimsF = nil
					}
					pends = append(pends, pend{name: name, obj: saTopObj{layout: synthLay, init: init, arrStrFields: arrStrF, objFieldClaims: objClaimsF}})
					continue
				}
				okAll = false
				break
			}
			pends = append(pends, pend{name: name, obj: saTopObj{layout: layout, init: init}})
		}
		if !okAll {
			continue
		}
		for _, p := range pends {
			out[p.name] = p.obj
		}
		claimed[st] = true
	}
	return out, claimed
}

// saTopObjLookup 查顶层对象常量快照（标识符直指、无局部遮蔽；不落字）。
func saTopObjLookup(scope *saScope, e *ast.Node) (saTopObj, bool) {
	if e == nil || e.Kind != ast.KindIdentifier {
		return saTopObj{}, false
	}
	name := e.Text()
	if _, shadowed := scope.types[name]; shadowed {
		return saTopObj{}, false
	}
	to, ok := scope.topObjs[name]
	if !ok {
		return saTopObj{}, false
	}
	return to, true
}

// saIsStrFieldRead 纯判定属性读是否为 str 位（静态串折叠/实例 str 域/
// str getter；不落字，供串位语法门；求值见 saEvalStr 属性分支）。
func saIsStrFieldRead(pa *ast.PropertyAccessExpression, scope *saScope) bool {
	if pa == nil || pa.Name() == nil {
		return false
	}
	// Top-level object-array .find field: `TOP.find(... )?.field` is a string
	// value (miss is undefined, handled as 0 in lowering).
	if _, _, _, ok := saTopObjArrFind(pa, scope); ok {
		return true
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
		// 命名空间可变串槽（`N.S` 活值；求值见 saEvalStr 属性分支同键）。
		if ms, ok := scope.modVars[nm+"."+field]; ok && ms.w == "str" {
			return true
		}
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
		// 顶层对象常量串域（快照；求值见 saEvalStr 顶层对象臂；788）。
		if to, ok := saTopObjLookup(scope, base); ok {
			if d, ok := scope.classes[to.layout]; ok {
				return atClass(d)
			}
		}
		// 顶层 Record 快照串域点读 `T.k`（vkind str；求值见 saEvalStr
		// 顶层 Record 点读臂；`?.` 与 map 方法同名键沿旧门）。
		if pa.QuestionDotToken == nil && !saIsMapMethod(field) {
			if tm, ok := saTopMapLookup(scope, base); ok && tm.vkind == "str" {
				return true
			}
		}
		return false
	}
	if base != nil && base.Kind == ast.KindThisKeyword && scope.thisSelf != "" {
		if d, ok := scope.classes[scope.thisClass]; ok {
			return atClass(d)
		}
	}
	// 嵌套链基（`q.p.s` 内节纯查表；不落字；私名嵌套沿旧门）。
	if base != nil && base.Kind == ast.KindPropertyAccessExpression {
		if sub, ok := saChainSubDef(base.AsPropertyAccessExpression(), scope); ok {
			return atClass(sub)
		}
	}
	// 调用结果基（`strip(model).baseModel`：被调函数返回实例布局时，字段种
	// 按该布局查表；求值侧 saInstBaseElem 已支持调用基，此处只补判定门，
	// 否则串方法链（.trim()/.replace() 等）在 saEvalCall 分发时因接收者
	// 不是串表达式而落“only direct function calls lowerable”）。
	if base != nil && base.Kind == ast.KindCallExpression {
		ce := base.AsCallExpression()
		if ce != nil && ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier {
			callee := ce.Expression.Text()
			if sig, ok := scope.funcs[callee]; ok && !sig.isVoid && len(sig.retKind) > 5 && sig.retKind[:5] == "inst:" {
				if d, ok := scope.classes[sig.retKind[5:]]; ok {
					return atClass(d)
				}
			}
			if q, linked := saLinkCallee(scope, callee); linked {
				if sig, ok := scope.funcs[q]; ok && !sig.isVoid && len(sig.retKind) > 5 && sig.retKind[:5] == "inst:" {
					if d, ok := scope.classes[sig.retKind[5:]]; ok {
						return atClass(d)
					}
				}
			}
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
	} else if base != nil && base.Kind == ast.KindSuperKeyword {
		// `super.m()` 基即基类布局（与调用分发 `saSuperBase` 同源；构造内
		// super() 调用形不经此口，沿旧门）。
		bdef, _, msg := saSuperBase(scope)
		if msg != "" {
			return nil, false
		}
		def = bdef
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
// / 供串位调用识别，`c.get(): string` 即串值）。
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
// saSynthLiteralLayout 按对象字面量的键集与值种合成布局（TS 的对象字面量
// 类型即其自身形状；无匹配接口时的兜底）：域值种 i32/str 直用；嵌套对象
// 字面量递归合成（inst + fsub 子布局）；余形（数组/调用/f64/实例/方法/spread）
// 未知即拒（禁猜）。形状证据：封存 lowerObjectLiteral 键集匹配 9066-9076
// 无命中时的自然延伸 + saSynthAnonLayout 合成纪律（isSynth 标记，键集匹配
// 不参与命中，禁误吞多命中门）。
// saLitArmKind 推断字面量域/三元臂的静态种（JEV par_retann sub agree_kind 0.98）：
// 透明包装解包；嵌套三元须两臂同种（inst 臂须同子布局）；对象字面量递归合成；
// 数字/布尔 -> i32，串表达式 -> str，浮点字面量 -> f64；绑定名按已定种；
// 调用按被调返回种；null/undefined 臂不给种（禁猜可空域），异种臂拒。
func saLitArmKind(e *ast.Node, scope *saScope) (string, string, bool) {
	if e == nil {
		return "", "", false
	}
	u := saUnwrapTransparent(e)
	if u == nil {
		return "", "", false
	}
	if u.Kind == ast.KindConditionalExpression {
		ce := u.AsConditionalExpression()
		if ce == nil || ce.WhenTrue == nil || ce.WhenFalse == nil {
			return "", "", false
		}
		tk, tsub, tok := saLitArmKind(ce.WhenTrue, scope)
		fk, fsub, fok := saLitArmKind(ce.WhenFalse, scope)
		if !tok || !fok || tk == "" || tk != fk {
			return "", "", false
		}
		if tk == "inst" && tsub != fsub {
			return "", "", false
		}
		return tk, tsub, true
	}
	if u.Kind == ast.KindObjectLiteralExpression {
		sub, ok := saSynthLiteralLayout(u.AsObjectLiteralExpression(), scope)
		if !ok {
			return "", "", false
		}
		return "inst", sub.name, true
	}
	if k := saLitFieldKind(u, scope); k != "" {
		return k, "", true
	}
	if u.Kind == ast.KindNumericLiteral && saIsF64Operand(u, scope) {
		return "f64", "", true
	}
	if u.Kind == ast.KindIdentifier {
		if bk, ok := scope.types[u.Text()]; ok {
			switch {
			case bk == "i32" || bk == "bool":
				return "i32", "", true
			case bk == "str":
				return "str", "", true
			case bk == "arr" || bk == "arrStr":
				return "arr", "", true
			case bk == "f64":
				return "f64", "", true
			case len(bk) > 5 && bk[:5] == "inst:":
				return "inst", bk[5:], true
			}
		}
		return "", "", false
	}
	if u.Kind == ast.KindCallExpression {
		ck, ok := saCallRetKind(u.AsCallExpression(), scope)
		if !ok {
			return "", "", false
		}
		switch {
		case ck == "i32" || ck == "bool":
			return "i32", "", true
		case ck == "str":
			return "str", "", true
		case ck == "arr" || ck == "arrStr":
			return "arr", "", true
		case ck == "f64":
			return "f64", "", true
		case len(ck) > 5 && ck[:5] == "inst:":
			return "inst", ck[5:], true
		}
		return "", "", false
	}
	return "", "", false
}

func saSynthLiteralLayout(ol *ast.ObjectLiteralExpression, scope *saScope) (*saClassDef, bool) {
	if ol == nil || ol.Properties == nil {
		return nil, false
	}
	names := []string{}
	kinds := map[string]string{}
	subs := map[string]string{}
	addField := func(fname, kind, sub string) {
		if _, ok := kinds[fname]; !ok {
			names = append(names, fname)
		}
		kinds[fname] = kind
		if sub != "" {
			subs[fname] = sub
		} else {
			delete(subs, fname)
		}
	}
	for _, p := range ol.Properties.Nodes {
		if p == nil {
			return nil, false
		}
		if p.Kind == ast.KindSpreadAssignment {
			// spread 源须已记布局（无命中时不可合成；jsval/map/调用展开
			// 沿旧门大声拒）。显式域后写覆盖展开域（JS 语义：后者胜，
			// 但域序保持展开在先）。
			se := saUnwrapTransparent(p.AsSpreadAssignment().Expression.AsNode())
			if se == nil {
				return nil, false
			}
			if se.Kind == ast.KindIdentifier {
				k, ok := scope.types[se.Text()]
				if !ok || len(k) <= 5 || k[:5] != "inst:" {
					return nil, false
				}
				sdef, ok := scope.classes[k[5:]]
				if !ok {
					return nil, false
				}
				for _, f := range sdef.fields {
					sk := sdef.fkinds[f.name]
					if sk == "" {
						sk = "i32"
					}
					addField(f.name, sk, sdef.fsub[f.name])
				}
				continue
			}
			if se.Kind == ast.KindObjectLiteralExpression {
				sub, ok := saSynthLiteralLayout(se.AsObjectLiteralExpression(), scope)
				if !ok {
					return nil, false
				}
				for _, f := range sub.fields {
					addField(f.name, sub.fkinds[f.name], sub.fsub[f.name])
				}
				continue
			}
			return nil, false
		}
		var fname string
		var init *ast.Node
		switch p.Kind {
		case ast.KindPropertyAssignment:
			pa := p.AsPropertyAssignment()
			if pa == nil || pa.Name() == nil {
				return nil, false
			}
			fname = pa.Name().Text()
			init = pa.Initializer
		case ast.KindShorthandPropertyAssignment:
			sp := p.AsShorthandPropertyAssignment()
			if sp == nil || sp.Name() == nil {
				return nil, false
			}
			fname = sp.Name().Text()
			init = p.Name()
		default:
			return nil, false
		}
		k := saLitFieldKind(init, scope)
		if k == "" && init != nil && init.Kind == ast.KindNumericLiteral && saIsF64Operand(init, scope) {
			// 浮点字面量初值 → f64 域（禁截断存 i32）。
			k = "f64"
		}
		if k == "" && init != nil && init.Kind == ast.KindCallExpression {
			// 调用初值：按被调返回种取（i32/str/arr/inst；未知即拒，禁猜）。
			if ck, ok := saCallRetKind(init.AsCallExpression(), scope); ok {
				switch {
				case ck == "i32" || ck == "bool":
					k = "i32"
				case ck == "str":
					k = "str"
				case ck == "arr" || ck == "arrStr":
					k = "arr"
				case ck == "f64":
					k = "f64"
				case len(ck) > 5 && ck[:5] == "inst:":
					k = "inst"
					subs[fname] = ck[5:]
				}
			}
		}
		if k == "" && init != nil && init.Kind == ast.KindIdentifier {
			// 绑定名初值：按已定种取域种（str/arr/f64/inst；i32/bool 已由
			// saLitFieldKind 给出；禁猜未知名）。
			if bk, ok := scope.types[init.Text()]; ok {
				switch {
				case bk == "str":
					k = "str"
				case bk == "arr" || bk == "arrStr":
					k = "arr"
				case bk == "f64":
					k = "f64"
				case len(bk) > 5 && bk[:5] == "inst:":
					k = "inst"
					subs[fname] = bk[5:]
				}
			}
		}
		if k == "" && init != nil {
			// 三元域初值：两臂同种即取该种（JEV par_retann sub agree_kind 0.98；
			// 异种/null/undefined 臂沿旧门拒，禁猜）。
			if u := saUnwrapTransparent(init); u != nil && u.Kind == ast.KindConditionalExpression {
				ce := u.AsConditionalExpression()
				if ce != nil && ce.WhenTrue != nil && ce.WhenFalse != nil {
					tk, tsub, tok := saLitArmKind(ce.WhenTrue, scope)
					fk, fsub, fok := saLitArmKind(ce.WhenFalse, scope)
					if tok && fok && tk != "" && tk == fk && (tk != "inst" || tsub == fsub) {
						k = tk
						if tsub != "" {
							subs[fname] = tsub
						}
					}
				}
			}
		}
		if k == "" {
			if init == nil || init.Kind != ast.KindObjectLiteralExpression {
				return nil, false
			}
			sub, ok := saSynthLiteralLayout(init.AsObjectLiteralExpression(), scope)
			if !ok {
				return nil, false
			}
			k = "inst"
			subs[fname] = sub.name
		}
		addField(fname, k, subs[fname])
	}
	if len(names) == 0 {
		return nil, false
	}
	base := "Lit_" + saMonoName(strings.Join(names, "_"))
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		if def, ok := scope.classes[name]; ok {
			// 同形复用：域名序与种全等（异形另建，禁静默改布局）。
			same := len(def.fields) == len(names)
			if same {
				for i, fn := range names {
					if def.fields[i].name != fn || def.fkinds[fn] != kinds[fn] {
						same = false
						break
					}
				}
			}
			if same {
				return def, true
			}
			continue
		}
		def := &saClassDef{name: name, offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fsub: map[string]string{}}
		off := 0
		for _, fname := range names {
			fkind := kinds[fname]
			off = saAlignOff(off, fkind)
			def.fields = append(def.fields, saClassField{name: fname, offset: off})
			def.offsets[fname] = off
			def.fkinds[fname] = fkind
			if s, ok := subs[fname]; ok {
				def.fsub[fname] = s
			}
			sz, _ := saFieldWidth(fkind)
			off += sz
		}
		def.size = off
		scope.classes[name] = def
		return def, true
	}
}

// saCondObjArmFields collects field names/kinds for a conditional-spread arm:
// an object literal (PropertyAssignment + Shorthand) or a nested conditional
// (recursively unioned). Kinds are static: numeric/bool -> i32, str expr -> str,
// arr value -> arr, inst value -> inst, identifier by its recorded kind.
func saCondObjArmFields(arm *ast.Node, scope *saScope) ([]string, map[string]string, string) {
	if arm == nil {
		return nil, nil, "conditional spread arm is missing"
	}
	if arm.Kind == ast.KindConditionalExpression {
		ce := arm.AsConditionalExpression()
		if ce == nil || ce.WhenTrue == nil || ce.WhenFalse == nil {
			return nil, nil, "conditional spread needs both arms"
		}
		tNames, tKinds, tMsg := saCondObjArmFields(ce.WhenTrue, scope)
		if tMsg != "" {
			return nil, nil, tMsg
		}
		fNames, fKinds, fMsg := saCondObjArmFields(ce.WhenFalse, scope)
		if fMsg != "" {
			return nil, nil, fMsg
		}
		var names []string
		kinds := map[string]string{}
		for _, fn := range tNames {
			names = append(names, fn)
			kinds[fn] = tKinds[fn]
		}
		for _, fn := range fNames {
			if prev, ok := kinds[fn]; ok {
				if prev != fKinds[fn] {
					return nil, nil, "conditional spread field " + fn + " has conflicting kinds across arms"
				}
				continue
			}
			names = append(names, fn)
			kinds[fn] = fKinds[fn]
		}
		return names, kinds, ""
	}
	if arm.Kind != ast.KindObjectLiteralExpression {
		return nil, nil, "conditional spread arms must be object literals"
	}
	ol := arm.AsObjectLiteralExpression()
	var names []string
	kinds := map[string]string{}
	for _, pr := range ol.Properties.Nodes {
		var fname string
		var init *ast.Node
		var ok bool
		switch pr.Kind {
		case ast.KindPropertyAssignment:
			fname, ok = saObjPropName(pr)
			if !ok {
				return nil, nil, "conditional spread arm property name must be a literal"
			}
			init = pr.AsPropertyAssignment().Initializer
		case ast.KindShorthandPropertyAssignment:
			fname, ok = saObjPropName(pr)
			if !ok {
				return nil, nil, "conditional spread arm property name must be a literal"
			}
			init = pr.Name()
		default:
			return nil, nil, "conditional spread arm must contain only property assignments"
		}
		k := saLitFieldKind(init, scope)
		if k == "" {
			if saIsArrValue(init, scope) {
				k = "arr"
			} else if saCouldBeInst(init, scope) {
				k = "inst"
			} else if init != nil && init.Kind == ast.KindIdentifier {
				if kk, ok := scope.types[init.Text()]; ok {
					if kk == "str" {
						k = "str"
					} else if kk == "arr" {
						k = "arr"
					} else if len(kk) > 5 && kk[:5] == "inst:" {
						k = "inst"
					}
				}
			}
		}
		if k == "" {
			return nil, nil, "conditional spread arm field " + fname + " has an unknown kind"
		}
		if prev, ok := kinds[fname]; ok && prev != k {
			return nil, nil, "conditional spread arm field " + fname + " has conflicting kinds"
		}
		if _, ok := kinds[fname]; !ok {
			names = append(names, fname)
		}
		kinds[fname] = k
	}
	return names, kinds, ""
}

// saLowerCondObj evaluates `cond ? {...} : {...}` (nested ternaries allowed)
// into a zero-filled temp object with the union layout, branching on the
// condition and copying only the taken arm's fields. Absent fields stay zero
// (undefined for optional fields), matching JS spread semantics.
func saLowerCondObj(w printer.EmitTextWriter, ce *ast.ConditionalExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, *saClassDef, string) {
	tNames, tKinds, tMsg := saCondObjArmFields(ce.WhenTrue, scope)
	if tMsg != "" {
		return "", nil, tMsg
	}
	fNames, fKinds, fMsg := saCondObjArmFields(ce.WhenFalse, scope)
	if fMsg != "" {
		return "", nil, fMsg
	}
	var uniNames []string
	uniKinds := map[string]string{}
	for _, fn := range tNames {
		uniNames = append(uniNames, fn)
		uniKinds[fn] = tKinds[fn]
	}
	for _, fn := range fNames {
		if prev, ok := uniKinds[fn]; ok {
			if prev != fKinds[fn] {
				return "", nil, "conditional spread field " + fn + " has conflicting kinds across arms"
			}
			continue
		}
		uniNames = append(uniNames, fn)
		uniKinds[fn] = fKinds[fn]
	}
	uniDef := &saClassDef{name: "LitCondSpread", offsets: map[string]int{}, fkinds: map[string]string{}, methods: map[string]*ast.Node{}, isIface: true, isSynth: true, fdefs: map[string]*ast.TypeNode{}, fsub: map[string]string{}}
	off := 0
	for _, fn := range uniNames {
		fk := uniKinds[fn]
		off = saAlignOff(off, fk)
		uniDef.fields = append(uniDef.fields, saClassField{name: fn, offset: off})
		uniDef.offsets[fn] = off
		uniDef.fkinds[fn] = fk
		sz, _ := saFieldWidth(fk)
		off += sz
	}
	uniDef.size = off
	cond, cmsg := saCondOperand(w, ce.Condition, scope, pos, refusals, nextTemp)
	if cmsg != "" {
		return "", nil, cmsg
	}
	tmp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if uniDef.size == 0 {
		w.Write(fmt.Sprintf("  %s = alloc 4\n", tmp))
	} else {
		w.Write(fmt.Sprintf("  %s = alloc %d\n", tmp, uniDef.size))
	}
	saOwnTemp(scope, tmp)
	for _, fn := range uniNames {
		fk := uniKinds[fn]
		if fk == "str" || fk == "arr" || fk == "inst" || fk == "f64" {
			w.Write(fmt.Sprintf("  store %s + %d, 0 as ptr\n", tmp, uniDef.offsets[fn]))
		} else {
			w.Write(fmt.Sprintf("  store %s + %d, 0 as i32\n", tmp, uniDef.offsets[fn]))
		}
	}
	trueL := fmt.Sprintf("L_cspread_true_%d", *scope.nextLabel)
	*scope.nextLabel++
	falseL := fmt.Sprintf("L_cspread_false_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_cspread_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cond, trueL, falseL))
	copyFrom := func(h string, def *saClassDef) {
		for _, f := range def.fields {
			sk := def.fkinds[f.name]
			if sk == "" {
				sk = "i32"
			}
			tt := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			if sk == "str" || sk == "arr" || sk == "inst" {
				w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", tt, h, def.offsets[f.name]))
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", tmp, uniDef.offsets[f.name], tt))
			} else if sk == "f64" {
				w.Write(fmt.Sprintf("  %s = load %s + %d as f64\n", tt, h, def.offsets[f.name]))
				w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", tmp, uniDef.offsets[f.name], tt))
			} else {
				w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", tt, h, def.offsets[f.name]))
				w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", tmp, uniDef.offsets[f.name], tt))
			}
		}
	}
	copyArm := func(arm *ast.Node) string {
		if arm == nil {
			return "conditional spread arm is missing"
		}
		if arm.Kind == ast.KindConditionalExpression {
			nce := arm.AsConditionalExpression()
			if nce == nil {
				return "conditional spread arm is missing"
			}
			h, def, msg := saLowerCondObj(w, nce, scope, pos, refusals, nextTemp)
			if msg != "" {
				return msg
			}
			copyFrom(h, def)
			// Nested temp is branch-local: release it here, not at return drain,
			// where the verifier reports UnknownRegister for branch-allocated temps.
			saReleaseOwnedTemp(w, scope, h)
			return ""
		}
		if arm.Kind != ast.KindObjectLiteralExpression {
			return "conditional spread arms must be object literals"
		}
		ol := arm.AsObjectLiteralExpression()
		if len(ol.Properties.Nodes) == 0 {
			return ""
		}
		h, lname, msg := saLowerObjectLiteral(w, arm, "", scope, pos, refusals, nextTemp)
		if msg != "" {
			return msg
		}
		adef, ok := scope.classes[lname]
		if !ok {
			return "conditional spread arm layout not recorded"
		}
		copyFrom(h, adef)
		// Arm handle is branch-local: release it here, not at return drain,
		// where the verifier reports UnknownRegister for branch-allocated temps.
		saReleaseOwnedTemp(w, scope, h)
		return ""
	}
	w.Write(fmt.Sprintf("%s:\n", trueL))
	if msg := copyArm(ce.WhenTrue); msg != "" {
		return "", nil, msg
	}
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", falseL))
	if msg := copyArm(ce.WhenFalse); msg != "" {
		return "", nil, msg
	}
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return tmp, uniDef, ""
}

// saFESpread is a collected Object.fromEntries spread: the source array
// handle (materialized during collection), the map callback parameter
// name, and the pair key/value expressions. The pair loop itself is
// emitted later, once the target layout is known, by
// saEmitFromEntriesSpread, which calls the sa_std helper
// @ts_obj_set_field_by_name per pair instead of re-emitting a
// field-match chain at every spread site (JEV sa_std_pair_setter).
type saFESpread struct {
	src   string
	param string
	key   *ast.Node
	val   *ast.Node
}

// saIsFromEntriesCall reports whether se is Object.fromEntries(x) with
// exactly one argument (light predicate; strict shape validation lives
// in saCollectFromEntriesSpread).
func saIsFromEntriesCall(se *ast.Node) bool {
	if se == nil || se.Kind != ast.KindCallExpression {
		return false
	}
	ce := se.AsCallExpression()
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Text() != "fromEntries" {
		return false
	}
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Expression.Text() != "Object" {
		return false
	}
	if ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 {
		return false
	}
	return true
}

// saCollectFromEntriesSpread validates the strict lowerable shape
// Object.fromEntries(SRC.map((k) => [k, v])) and materializes the
// source array handle. The map itself is never evaluated: the emitter
// loop binds k per element and evaluates the pair expressions inline,
// which matches JS evaluation order (callback per element, pairs in
// order). Anything else refuses loudly here, never silently.
func saCollectFromEntriesSpread(se *ast.Node, scope *saScope, w printer.EmitTextWriter, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (*saFESpread, string) {
	ce := se.AsCallExpression()
	if ce == nil || ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 {
		return nil, "fromEntries spread needs exactly one argument"
	}
	arg := saUnwrapTransparent(ce.Arguments.Nodes[0])
	if arg == nil || arg.Kind != ast.KindCallExpression {
		return nil, "fromEntries spread needs a .map call argument (only SRC.map((k) => [k, v]) is lowerable)"
	}
	mce := arg.AsCallExpression()
	if mce == nil || mce.Expression == nil || mce.Expression.Kind != ast.KindPropertyAccessExpression {
		return nil, "fromEntries spread needs a .map call argument (only SRC.map((k) => [k, v]) is lowerable)"
	}
	mpa := mce.Expression.AsPropertyAccessExpression()
	if mpa == nil || mpa.Name() == nil || mpa.Name().Text() != "map" {
		return nil, "fromEntries spread needs a .map call argument (only SRC.map((k) => [k, v]) is lowerable)"
	}
	if mce.Arguments == nil || len(mce.Arguments.Nodes) != 1 {
		return nil, "fromEntries spread needs a .map call with exactly one callback argument"
	}
	cb := mce.Arguments.Nodes[0]
	if cb == nil || (cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression) {
		return nil, "fromEntries map callback must be an inline arrow or function expression"
	}
	params := cb.Parameters()
	if len(params) != 1 {
		return nil, "fromEntries map callback takes exactly one parameter"
	}
	pd := params[0].AsParameterDeclaration()
	if pd == nil || pd.Name() == nil || pd.Name().Kind != ast.KindIdentifier {
		return nil, "fromEntries map callback parameter must be a single identifier"
	}
	paramName := pd.Name().Text()
	if _, shadowed := scope.types[paramName]; shadowed {
		return nil, "fromEntries map parameter shadows a live binding (rename it)"
	}
	body := cb.Body()
	if body == nil {
		return nil, "fromEntries map callback has no body"
	}
	if body.Kind == ast.KindBlock {
		bd := body.AsBlock()
		if bd == nil || bd.Statements == nil || len(bd.Statements.Nodes) != 1 {
			return nil, "fromEntries map callback block must have exactly one return"
		}
		rs := bd.Statements.Nodes[0].AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return nil, "fromEntries map callback block must have exactly one return"
		}
		body = rs.Expression
	}
	body = saUnwrapTransparent(body)
	if body == nil || body.Kind != ast.KindArrayLiteralExpression {
		return nil, "fromEntries map callback must return a [key, value] pair literal"
	}
	al := body.AsArrayLiteralExpression()
	if al == nil || al.Elements == nil || len(al.Elements.Nodes) != 2 {
		return nil, "fromEntries map callback must return a [key, value] pair literal"
	}
	for _, el := range al.Elements.Nodes {
		if el == nil || el.Kind == ast.KindSpreadElement || el.Kind == ast.KindOmittedExpression {
			return nil, "fromEntries map callback must return a [key, value] pair literal"
		}
	}
	recvExpr := mpa.Expression
	var src string
	if recvExpr != nil && recvExpr.Kind == ast.KindIdentifier {
		if arr, ok := saTopArrLookup(scope, recvExpr); ok {
			src = saMaterializeTopArr(w, arr, scope, nextTemp)
		}
	}
	if src == "" {
		h, msg := saArrValueOf(w, recvExpr, scope, pos, refusals, nextTemp)
		if msg != "" {
			return nil, msg
		}
		src = h
	}
	return &saFESpread{src: src, param: paramName, key: al.Elements.Nodes[0], val: al.Elements.Nodes[1]}, ""
}

// saEmitFromEntriesSpread lowers one collected fromEntries spread into
// the target object h with layout def: a loop over the source array
// binds the map parameter per element, evaluates the pair key (string)
// and value (i32), and calls @ts_obj_set_field_by_name (sa_std) to
// store into the matching field. The loop itself is site-specific
// control flow (it inlines the map callback); the reusable
// match-and-store step is the library call. Only i32/bool target
// fields are lowerable: an i32 pair value cannot soundly fill a
// str/arr/inst/f64 slot, so such layouts refuse loudly. Unmatched
// pairs are dropped by the helper (they have no static slot); the
// caller zero-fills the target up front, so dropped pairs read as
// missing optional fields, never garbage.
func saEmitFromEntriesSpread(w printer.EmitTextWriter, h string, def *saClassDef, fe *saFESpread, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	for _, f := range def.fields {
		fk := def.fkinds[f.name]
		if fk == "" {
			fk = "i32"
		}
		if fk != "i32" && fk != "bool" {
			return "fromEntries spread over non-i32 field " + f.name + " is not lowerable (i32 pair values only)"
		}
	}
	var nameHs []string
	for _, f := range def.fields {
		nameHs = append(nameHs, saLowerStringLiteral(w, f.name, scope, nextTemp))
	}
	namesArr := saAssembleHandles(w, nameHs, scope, nextTemp)
	var offTexts []string
	for _, f := range def.fields {
		offTexts = append(offTexts, strconv.Itoa(def.offsets[f.name]))
	}
	offsetsArr := saBuildI32Array(w, offTexts, scope, nextTemp)
	scope.addImport("sa_std/ts_object.sa")
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, fe.src))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, fe.src))
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_fe_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_fe_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_fe_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	el := saArrElemAt(w, data, i, nextTemp)
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", fe.param, el))
	scope.types[fe.param] = "str"
	saDeclarePlain(scope, fe.param)
	keyH, msg := saEvalStr(w, fe.key, scope, pos, refusals, nextTemp)
	if msg != "" {
		return msg
	}
	kp, kl := saExpandStr(w, keyH, nextTemp)
	v, msg := saEvalI32(w, fe.val, scope, pos, refusals, nextTemp)
	if msg != "" {
		return msg
	}
	w.Write(fmt.Sprintf("  call @ts_obj_set_field_by_name(%s, %s, %s, %s, %s, %s, %d)\n", h, kp, kl, v, namesArr, offsetsArr, len(def.fields)))
	if keyH != fe.param {
		saReleaseOwnedTemp(w, scope, keyH)
	}
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	delete(scope.types, fe.param)
	saReleaseOwnedTemp(w, scope, namesArr)
	saReleaseOwnedTemp(w, scope, offsetsArr)
	saReleaseOwnedTemp(w, scope, fe.src)
	return ""
}

// saLowerTopObjClaim lowers a top-object claim's init with the claim
// pointer set (par_notexp, JEV idfield_snapshot): identifier fields
// resolved at prescan time lower from claim data, never from importer
// scope names. Save/restored, so nested claim recursions nest safely.
func saLowerTopObjClaim(w printer.EmitTextWriter, to saTopObj, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	claim := to
	prev := scope.topObjClaim
	scope.topObjClaim = &claim
	h, lname, msg := saLowerObjectLiteral(w, to.init, to.layout, scope, pos, refusals, nextTemp)
	scope.topObjClaim = prev
	return h, lname, msg
}

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
		fe     *saFESpread // non-nil for a fromEntries spread
	}
	var spreads []spreadSrc
	var ops []op
	var keys []string
	fieldVals := map[string]string{}
	for _, p := range ol.Properties.Nodes {
		if p.Kind == ast.KindSpreadAssignment {
			se := p.AsSpreadAssignment().Expression.AsNode()
			se = saUnwrapTransparent(se)
			var sh string
			var sdef *saClassDef
			switch {
			case se.Kind == ast.KindIdentifier:
				nm := se.Text()
				if to, ok := scope.topObjs[nm]; ok {
					sd, ok := scope.classes[to.layout]
					if !ok || sd == nil {
						return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
					}
					sh2, _, msg2 := saLowerTopObjClaim(w, to, scope, pos, refusals, nextTemp)
					if msg2 != "" {
						return "", "", msg2
					}
					sh, sdef = sh2, sd
					break
				}
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
			case se.Kind == ast.KindConditionalExpression:
				ce := se.AsConditionalExpression()
				if ce == nil {
					return "", "", "conditional spread needs a conditional expression"
				}
				h, sdef, msg := saLowerCondObj(w, ce, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				spreads = append(spreads, spreadSrc{h: h, def: sdef})
				ops = append(ops, op{spread: len(spreads) - 1})
				for _, f := range sdef.fields {
					keys = append(keys, f.name)
				}
				continue
			case saIsFromEntriesCall(se):
				fe, msg := saCollectFromEntriesSpread(se, scope, w, pos, refusals, nextTemp)
				if msg != "" {
					return "", "", msg
				}
				ops = append(ops, op{spread: -1, fe: fe})
				continue
			case se.Kind == ast.KindCallExpression:
				// 工厂调用展开（`{...mk()}` 经签名返回种得布局；封存
				// checker_layout s1 + layoutOfNode 调用位同形；非 inst
				// 返回/未知被调沿旧门大声拒）。
				ce := se.AsCallExpression()
				got, ok := saCallRetKind(ce, scope)
				if !ok || len(got) <= 5 || got[:5] != "inst:" {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				d, ok := scope.classes[got[5:]]
				if !ok {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				op, voidCall, msg := saEvalCall(w, ce, scope, pos, refusals, nextTemp)
				if msg != "" || voidCall {
					return "", "", "spread source has no recorded interface layout (spread an interface-typed object)"
				}
				sh, sdef = op, d
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
			fieldVals[fname] = saLitFieldKind(p.AsPropertyAssignment().Initializer, scope)
		case ast.KindShorthandPropertyAssignment:
			fname, ok := saObjPropName(p)
			if !ok {
				return "", "", "computed property names must be literals (dynamic keys have no static layout)"
			}
			ops = append(ops, op{spread: -1, fname: fname, init: p.Name()})
			keys = append(keys, fname)
			fieldVals[fname] = saLitFieldKind(p.Name(), scope)
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
	// 注解优先直命中（单态具化名精确消歧：模板与实例键集相同时按键集
	// 匹配必多命中；名/键集不符下探既有匹配门）。
	def, msg := saMatchWantIface(want, uniq, scope.classes)
	if msg != "" {
		return "", "", msg
	}
	if def == nil {
		hits := saMatchIfaceList(uniq, scope.classes)
		if len(hits) == 0 {
			// 无匹配接口：按字面量自身键集+值种合成布局（TS 的对象字面量
			// 类型即其自身形状；未知值种仍拒，禁猜）。
			sdef, sok := saSynthLiteralLayout(ol, scope)
			if !sok {
				return "", "", "object literal matches no recorded interface layout (declare the interface first)"
			}
			def = sdef
		} else {
			def = hits[0]
		}
		if len(hits) > 1 {
			// 值种决胜（模板与单态键集并存时按字面量值种计分；唯一最高
			// 分即用，平分沿旧多命中门；单命中零行为变）。
			best, bestScore, tied := hits[0], -1, false
			for _, h := range hits {
				s := 0
				for _, f := range h.fields {
					if vk, ok := fieldVals[f.name]; ok && vk != "" && h.fkinds[f.name] == vk {
						s++
					}
				}
				if s > bestScore {
					best, bestScore, tied = h, s, false
				} else if s == bestScore {
					tied = true
				}
			}
			if tied {
				return "", "", "object literal matches no unique recorded interface layout (declare the interface first)"
			}
			def = best
		}
	}
	if want != "" && def.name != want {
		return "", "", "object literal does not match interface " + want
	}
	// 2b_tmpl (JEV plus_lit_erasure): a statically-known string
	// literal value for a string-literal-typed field must equal the
	// literal type; other values (identifiers, templates, calls)
	// keep erasure semantics and are checked by kind only.
	// Merged layouts are exempt: union/intersection synthesis keeps
	// only the first variant's literal (first-wins fdefs), so a
	// value matching a later variant (shared CodexSandboxPolicy
	// "workspaceWrite", server CodexRetryParsedLine "ignored") would
	// falsely refuse against the first variant's literal.
	if def.fdefs != nil && !strings.HasPrefix(def.name, "Union_") && !strings.HasPrefix(def.name, "Inter_") {
		for _, o := range ops {
			if o.fname == "" || o.init == nil {
				continue
			}
			tn, ok := def.fdefs[o.fname]
			if !ok || tn == nil || tn.Kind != ast.KindLiteralType {
				continue
			}
			lit := tn.AsLiteralTypeNode()
			if lit == nil || lit.Literal == nil || (lit.Literal.Kind != ast.KindStringLiteral && lit.Literal.Kind != ast.KindNoSubstitutionTemplateLiteral) {
				continue
			}
			if v := saUnwrapTransparent(o.init); v != nil && (v.Kind == ast.KindStringLiteral || v.Kind == ast.KindNoSubstitutionTemplateLiteral) && v.Text() != lit.Literal.Text() {
				return "", "", "string literal field " + o.fname + " must be \"" + lit.Literal.Text() + "\""
			}
		}
	}
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if def.size == 0 {
		w.Write(fmt.Sprintf("  %s = alloc 4\n", h))
	} else {
		w.Write(fmt.Sprintf("  %s = alloc %d\n", h, def.size))
	}
	// 头槽归属（嵌套递归同记；具名绑定时消费，余下返前释放；
	// 封存 lowerObjectLiteral 尾 declareOwned(h) 同形）。
	saOwnTemp(scope, h)
	// fromEntries spreads need an upfront zero-fill: their fields are
	// set dynamically (pairs may cover any subset), and the trailing
	// zero-fill below is skipped for fe-covered fields, so defaults
	// must be zero before any op replays (later props still win, as
	// they overwrite zeros; earlier props survive unless a pair key
	// matches them, exactly as JS spread order requires).
	hasFE := false
	for _, o := range ops {
		if o.fe != nil {
			hasFE = true
			break
		}
	}
	if hasFE {
		for _, f := range def.fields {
			if fk := def.fkinds[f.name]; fk == "i32" || fk == "" || fk == "bool" {
				w.Write(fmt.Sprintf("  store %s + %d, 0 as i32\n", h, def.offsets[f.name]))
			} else {
				w.Write(fmt.Sprintf("  store %s + %d, 0 as ptr\n", h, def.offsets[f.name]))
			}
		}
	}
	for oi, o := range ops {
		if o.fe != nil {
			if msg := saEmitFromEntriesSpread(w, h, def, o.fe, scope, pos, refusals, nextTemp); msg != "" {
				return "", "", msg
			}
			continue
		}
		if o.spread >= 0 {
			// spread 逐域复制（源序；后者覆盖前者；种错配拒，封存 9095 原文）。
			// 后静态 op（显式域/后 spread）覆盖的域不复制：其值必被后写
			// 覆盖，复制是死代码，且覆盖改种时旧种复制会误报错配。
			// fromEntries 是动态写域，不计入后写集合。
			skip := map[string]bool{}
			for _, later := range ops[oi+1:] {
				if later.fe != nil {
					continue
				}
				if later.spread >= 0 {
					for _, f := range spreads[later.spread].def.fields {
						skip[f.name] = true
					}
				} else if later.fname != "" {
					skip[later.fname] = true
				}
			}
			src := spreads[o.spread]
			for _, f := range src.def.fields {
				if skip[f.name] {
					continue
				}
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
				if sk == "str" || sk == "arr" || sk == "inst" || sk == "jsval" {
					w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, src.h, src.def.offsets[f.name]))
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[f.name], t))
					continue
				}
				if sk == "f64" {
					w.Write(fmt.Sprintf("  %s = load %s + %d as f64\n", t, src.h, src.def.offsets[f.name]))
					w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, def.offsets[f.name], t))
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
		if fk := def.fkinds[o.fname]; fk == "jsval" {
			// jsval field (JEV cfg296 jsval_field 0.92): box the supplied
			// value through the existing jsval call-arg ladder (reuses
			// @ts_jsval_box_* sa_std helpers; no new inline helper) and
			// store the box handle as ptr.
			fakeSig := saFuncSig{paramKinds: []string{"jsval"}}
			v, msg := saEvalCallArg(w, fakeSig, 0, o.init, 1, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], v))
			continue
		}
		if fk := def.fkinds[o.fname]; fk == "arr" {
			// Record-typed field with an empty object literal (JEV
			// kind244_empty null_annotated_str): the field is a map and
			// {} is the empty map. Non-Record arr fields and non-empty
			// literals fall through to the old arr arm below, so this
			// arm only flips previously-refusing shapes.
			if o.init != nil && o.init.Kind == ast.KindObjectLiteralExpression {
				if sub := o.init.AsObjectLiteralExpression(); sub != nil && sub.Properties != nil && len(sub.Properties.Nodes) == 0 {
					if fdef, ok := def.fdefs[o.fname]; ok && fdef != nil {
						if _, ok := saRecordValueKind(fdef, scope.classes, scope.aliasOf); !ok {
							// Mirror the gate relaxation: an empty {} for a
							// direct Record/Map field materializes as an empty
							// map even without a parseable value kind.
							if fdef.Kind != ast.KindTypeReference {
								goto notEmptyMap
							}
							ref := fdef.AsTypeReferenceNode()
							if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier ||
								(ref.TypeName.Text() != "Record" && ref.TypeName.Text() != "Map") {
								goto notEmptyMap
							}
						}
						mh, msg := saLowerMapNew(w, "Map", nil, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", "", msg
						}
						saOwnTemp(scope, mh)
						w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], mh))
						continue
					}
				notEmptyMap:
				}
			}
		}
		if fk := def.fkinds[o.fname]; fk == "arr" {
			// (par_notexp, JEV idfield_snapshot) 认领内标识符数组域：
			// prescan 值拷贝直供句柄数组，禁 importer 名查找。
			if o.init != nil && o.init.Kind == ast.KindIdentifier && scope.topObjClaim != nil {
				if vals, ok := scope.topObjClaim.arrStrFields[o.fname]; ok {
					var hs []string
					for _, v := range vals {
						hs = append(hs, saLowerStringLiteral(w, v, scope, nextTemp))
					}
					h2 := saAssembleHandles(w, hs, scope, nextTemp)
					saMarkArrStr(scope, h2)
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], h2))
					continue
				}
			}
			// arr 域存句柄（字面量递归/绑定直传经句柄总线；泛型形参域等标量初值
			// 按 i32 求值存入 ptr 槽；封存 lowerObjectLiteral:9105-9109 按域种无区分落字）。
			// 字面量直供记元种（逐元 `saLitFieldKind`，全同才记，供实例深相等分流）。
			if al := o.init; al != nil && al.Kind == ast.KindArrayLiteralExpression {
				if lit := al.AsArrayLiteralExpression(); lit != nil && lit.Elements != nil {
					ek := ""
					for _, el := range lit.Elements.Nodes {
						k := saLitFieldKind(el, scope)
						if k != "i32" && k != "str" {
							ek = ""
							break
						}
						if ek == "" {
							ek = k
						} else if ek != k {
							ek = ""
							break
						}
					}
					if ek != "" {
						if scope.arrFieldElem == nil {
							scope.arrFieldElem = map[string]string{}
						}
						scope.arrFieldElem[def.name+"."+o.fname] = ek
					}
				}
			}
			v, msg := saArrValueOf(w, o.init, scope, pos, refusals, nextTemp)
			if msg != "" {
				// jsval ternary fallback (JEV arr_fallback 1.0): JsonValue
				// fields are recorded as arr, so their ternary inits never
				// reach the jsval ladder; a literal arm would die in the
				// i32 fallback below. The pure pre-check gates the divert,
				// so currently-passing shapes (e.g. undefined-arm ternaries
				// via SELECT) keep the existing fallback byte-identical.
				if o.init != nil && o.init.Kind == ast.KindConditionalExpression {
					if ce := o.init.AsConditionalExpression(); ce != nil && saJsValTernaryShapeOk(o.init, scope, 0) && saJsValTernaryHasLiteral(o.init, scope, 0) {
						if hv, msg2 := saLowerTernaryJsVal(w, ce, o.init, scope, pos, refusals, scope.nextLabel, nextTemp); msg2 == "" {
							v, msg = hv, ""
						}
					}
				}
				if msg != "" {
					v, msg = saEvalI32(w, o.init, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", "", msg
					}
				}
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
			if o.init != nil && (o.init.Kind == ast.KindNullKeyword || o.init.Kind == ast.KindUndefinedKeyword) {
				w.Write(fmt.Sprintf("  store %s + %d, 0 as ptr\n", h, def.offsets[o.fname]))
				continue
			}
			if o.init != nil && o.init.Kind == ast.KindObjectLiteralExpression {
				// 嵌套字面量自有域：清认领指针（外层认领数据仅属
				// 顶层域，禁同名嵌套域误取外层值）。
				prevClaim := scope.topObjClaim
				scope.topObjClaim = nil
				v, _, msg := saLowerObjectLiteral(w, o.init, sub, scope, pos, refusals, nextTemp)
				scope.topObjClaim = prevClaim
				if msg != "" {
					return "", "", msg
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], v))
				continue
			}
			// 实例直存（已绑定实例句柄值；种门：须为同布局实例）。
			if o.init != nil && o.init.Kind == ast.KindIdentifier {
				// (par_notexp, JEV idfield_snapshot) 认领内标识符
				// 对象域：嵌套认领递归物化（指针切至嵌套认领）。
				if scope.topObjClaim != nil {
					if nested, ok := scope.topObjClaim.objFieldClaims[o.fname]; ok {
						v, _, msg := saLowerTopObjClaim(w, nested, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", "", msg
						}
						w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], v))
						continue
					}
				}
				k, ok := scope.types[o.init.Text()]
				if !ok || k != "inst:"+sub {
					return "", "", "nested field " + o.fname + " needs an object literal or matching instance"
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], o.init.Text()))
				continue
			}
			// 调用返回实例（具实例返回种；句柄直存，源 temp 移交字段）。
			if o.init != nil && o.init.Kind == ast.KindCallExpression {
				if ck, ok := saCallRetKind(o.init.AsCallExpression(), scope); ok && ck == "inst:"+sub {
					v, _, msg := saEvalCall(w, o.init.AsCallExpression(), scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", "", msg
					}
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, def.offsets[o.fname], v))
					continue
				}
			}
			return "", "", "nested field " + o.fname + " needs an object literal"
		}
		if fk := def.fkinds[o.fname]; fk == "f64" {
			// f64 域存双精度（字面量/绑定/调用经严格求值；与类 finit 臂同形）。
			v, msg := saEvalF64(w, o.init, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", "", msg
			}
			w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, def.offsets[o.fname], v))
			continue
		}
		v, msg := saEvalI32(w, o.init, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, def.offsets[o.fname], v))
	}
	// 缺省可选域零填（i32/bool 域填 0 as i32；串/数组/实例/f64 域为 8 字节
	// 指针/双精度，填 0 as ptr 即全零（f64 零位即 0.0，缺省即 undefined 的
	// 空柄表示；匹配门已保缺键全可选）。
	filled := map[string]bool{}
	for _, o := range ops {
		if o.fe != nil {
			for _, f := range def.fields {
				filled[f.name] = true
			}
			continue
		}
		if o.spread >= 0 {
			for _, f := range spreads[o.spread].def.fields {
				filled[f.name] = true
			}
			continue
		}
		filled[o.fname] = true
	}
	for _, f := range def.fields {
		if filled[f.name] {
			continue
		}
		if fk := def.fkinds[f.name]; fk == "i32" || fk == "" || fk == "bool" {
			w.Write(fmt.Sprintf("  store %s + %d, 0 as i32\n", h, def.offsets[f.name]))
			continue
		}
		w.Write(fmt.Sprintf("  store %s + %d, 0 as ptr\n", h, def.offsets[f.name]))
	}
	return h, def.name, ""
}

// saInstBaseElem 解析实例基发射位（map 索引 `m["a"].x` 经 get 脱糖，结果
// 按记种消解；调用结果 `M.get(k).f`/实例签名函数调用求值后按记种消解；
// 上游 lowerExpr 通用基同形）。
// temp 按建表值种记 `inst:T`；非 map 索引/错种沿旧门大声拒，永不空-def-无-msg）。
func saInstBaseElem(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, *saClassDef, string) {
	if e != nil && e.Kind == ast.KindNewExpression {
		// `new C()` 临时实例基：构造即 saOwnTemp 归属，域尾/返前释放口回收。
		cname, msg := saNewStmtClassName(e.AsNewExpression())
		if msg != "" {
			return "", nil, msg
		}
		h, msg := saLowerNewStmt(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", nil, msg
		}
		return h, scope.classes[cname], ""
	}
	if e != nil && e.Kind == ast.KindCallExpression {
		// 调用结果基（`M.get(k).f`；求值后按记种消解布局；上游 lowerExpr 通用基同形）。
		ce := e.AsCallExpression()
		op, voidCall, msg := saEvalCall(w, ce, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", nil, msg
		}
		if voidCall {
			return "", nil, "void call cannot be an instance base"
		}
		if k, ok := scope.types[op]; ok && len(k) > 5 && k[:5] == "inst:" {
			if def, ok := scope.classes[k[5:]]; ok {
				return op, def, ""
			}
			return "", nil, "unknown class " + k[5:]
		}
		return "", nil, "call did not yield an instance"
	}
	if e == nil || e.Kind != ast.KindElementAccessExpression {
		return "", nil, "not a map index base"
	}
	op, msg := saLowerIndexLoadExpr(w, e.AsElementAccessExpression(), scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", nil, msg
	}
	if k, ok := scope.types[op]; ok && len(k) > 5 && k[:5] == "inst:" {
		if def, ok := scope.classes[k[5:]]; ok {
			return op, def, ""
		}
		return "", nil, "unknown class " + k[5:]
	}
	return "", nil, "map index did not yield an instance"
}

// saEvalInstOperand 求实例操作数句柄（标识符/下标/调用/字段链读；返回柄+种）：
// 复用既有读位（saInstBaseElem/saEvalCall/saEvalI32 字段链），只收 inst 记种，
// 非实例沿旧门大声拒；供通用空合槽与条件位同行分支共用（封存 lowerBinary:3182-3206 槽形）。
func saEvalInstOperand(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string, string) {
	u := saUnwrapTransparent(e)
	if u != nil && u.Kind == ast.KindIdentifier {
		if k, ok := scope.types[u.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
			if _, ok := scope.classes[k[5:]]; !ok {
				return "", "", "unknown class " + k[5:]
			}
			return u.Text(), k, ""
		}
		return "", "", "not an instance operand"
	}
	if saCouldBeInst(u, scope) {
		h, def, msg := saInstBaseElem(w, u, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		if def == nil {
			return "", "", "instance base did not resolve to a recorded layout"
		}
		if k, ok := scope.types[h]; ok && len(k) > 5 && k[:5] == "inst:" {
			return h, k, ""
		}
		return "", "", "map index did not yield an instance"
	}
	if u != nil && u.Kind == ast.KindPropertyAccessExpression {
		op, msg := saEvalI32(w, u, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", "", msg
		}
		if k, ok := scope.types[op]; ok && len(k) > 5 && k[:5] == "inst:" {
			return op, k, ""
		}
		return "", "", "not an instance operand"
	}
	return "", "", "not an instance operand"
}

// saLowerNullishInst 通用实例空合槽（左右皆同布局实例则右惰性，或右为空字面量；
// 槽形镜像 saLowerNullish/saLowerNullishInstNull，值宽按 ptr；异布局/非实例大声拒）。
func saLowerNullishInst(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	l, lkind, msg := saEvalInstOperand(w, be.Left, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	isNullRight := false
	if be.Right != nil {
		switch be.Right.Kind {
		case ast.KindNullKeyword, ast.KindUndefinedKeyword:
			isNullRight = true
		}
		if !isNullRight && be.Right.Kind == ast.KindIdentifier && be.Right.Text() == "undefined" {
			if _, ok := scope.types["undefined"]; !ok {
				isNullRight = true
			}
		}
	}
	var r, rkind string
	if isNullRight {
		r = "0"
		rkind = lkind
	} else {
		var m string
		r, rkind, m = saEvalInstOperand(w, be.Right, scope, pos, refusals, nextTemp)
		if m != "" {
			return "", m
		}
		if rkind != lkind {
			return "", "instance nullish arms must share a layout (or use null fallback)"
		}
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	tL := fmt.Sprintf("L_null_t_%d", *scope.nextLabel)
	*scope.nextLabel++
	fL := fmt.Sprintf("L_null_f_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_null_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = ne %s, 0\n", c, l))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, tL, fL))
	w.Write(fmt.Sprintf("%s:\n", tL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, l))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", fL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, r))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	scope.types[out] = lkind
	saOwnTemp(scope, out)
	return out, ""
}

// saIsNullLit 报告空字面量（null/undefined（含未绑定标识符）；子集皆 0 句柄）。
func saIsNullLit(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindNullKeyword, ast.KindUndefinedKeyword:
		return true
	case ast.KindIdentifier:
		if e.Text() == "undefined" {
			if _, ok := scope.types["undefined"]; !ok {
				return true
			}
		}
	}
	return false
}

// saIsInstOperandSyntax 无发射预判实例操作数形状（标识符 inst 种/下标·调用 saCouldBeInst/
// 字段链已知 inst 域；供 ?? 分发先行选路，禁误吞 i32 串 sound 形）。
func saIsInstOperandSyntax(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	u := saUnwrapTransparent(e)
	if u == nil {
		return false
	}
	if u.Kind == ast.KindIdentifier {
		if k, ok := scope.types[u.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
			return true
		}
		return false
	}
	if saCouldBeInst(u, scope) {
		return true
	}
	if u.Kind == ast.KindPropertyAccessExpression {
		pa := u.AsPropertyAccessExpression()
		if pa == nil || pa.Name() == nil {
			return false
		}
		fname := pa.Name().Text()
		// 直接基为具名实例：查布局字段种。
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			if k, ok := scope.types[pa.Expression.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
				if def, ok := scope.classes[k[5:]]; ok {
					if fk, ok := def.fkinds[fname]; ok && fk == "inst" {
						return true
					}
				}
			}
		}
		if pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword && scope.thisSelf != "" {
			if def, ok := scope.classes[scope.thisClass]; ok {
				if fk, ok := def.fkinds[fname]; ok && fk == "inst" {
					return true
				}
			}
		}
	}
	return false
}

// saChainHasOptional 报告成员链基里是否含 `?.`（`o?.inner.v` 的中间步：null
// 短路后基为空柄，末读须空守卫 join，否则读零址 SIGSEGV——禁静默错码）。
func saChainHasOptional(e *ast.Node) bool {
	for e != nil {
		switch e.Kind {
		case ast.KindPropertyAccessExpression:
			pa := e.AsPropertyAccessExpression()
			if pa == nil {
				return false
			}
			if pa.QuestionDotToken != nil {
				return true
			}
			e = pa.Expression
		case ast.KindElementAccessExpression:
			ea := e.AsElementAccessExpression()
			if ea == nil {
				return false
			}
			e = ea.Expression
		case ast.KindCallExpression:
			ce := e.AsCallExpression()
			if ce == nil {
				return false
			}
			if ce.QuestionDotToken != nil {
				return true
			}
			e = ce.Expression
		default:
			return false
		}
	}
	return false
}

func saInstBase(e *ast.Node, scope *saScope) (string, *saClassDef, string) {
	// 括号接收者透明（`(c).m()` 即 `c.m()`；与被调/注解/表达式侧括号
	// 解包同律；上游同位拒收系过严，本仓 thin-lead（481 同例）。
	for e != nil && e.Kind == ast.KindParenthesizedExpression {
		pe := e.AsParenthesizedExpression()
		if pe == nil || pe.Expression == nil {
			break
		}
		e = pe.Expression.AsNode()
	}
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
// saIsDerivedFrom 报告sub 是否为 base 本身或其传统派生（跟 parent 链，防环；形状证据：抽象校验祖先链同形）。
// saClassSatisfies reports whether an instance of class `have` may be
// passed where class/interface `want` is expected: exact match, the
// existing parent-chain derivation, or a directly implemented interface
// on `have` or on any class on `have`'s parent chain (4_if_flow, JEV
// record_implements_args 0.95). Interface-extends transitivity is NOT
// included: interface extends is flattened into fields, not a parent
// link, so a class implementing ChildIface does not yet satisfy a
// ParentIface parameter. Cycle-guarded (parent cycles are refused at
// recording, but this helper must never loop on a corrupt map).
func saClassSatisfies(classes map[string]*saClassDef, have, want string) bool {
	if have == want {
		return true
	}
	if saIsDerivedFrom(classes, have, want) {
		return true
	}
	seen := map[string]bool{}
	for c := have; c != "" && !seen[c]; {
		seen[c] = true
		d, ok := classes[c]
		if d == nil || !ok {
			break
		}
		for _, im := range d.implements {
			if im == want {
				return true
			}
		}
		c = d.parent
	}
	return false
}

func saIsDerivedFrom(classes map[string]*saClassDef, sub, base string) bool {
	for p := sub; p != ""; {
		if p == base {
			return true
		}
		pb := classes[p]
		if pb == nil {
			return false
		}
		p = pb.parent
	}
	return false
}

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
// saNewStmtClassName 消解裸 `new` 语句的类名（标识符 + `N.C` 限定即 `N_C`；
// 与声明绑定 sa_decl.go:295-304 同形；Date/Map/Set/Array 构造沿旧门）。
func saNewStmtClassName(ne *ast.NewExpression) (string, string) {
	if ne == nil || ne.Expression == nil {
		return "", "new expression is not lowerable"
	}
	// 括号被 new 透明（`new (C)()` 即 `new C()`；483 同例）。
	ex := saUnwrapTransparent(ne.Expression)
	if ex.Kind == ast.KindIdentifier {
		name := ex.Text()
		switch name {
		case "Date", "Map", "Set", "Array":
			return "", "new " + name + " needs a binding (statement discard not lowerable yet)"
		}
		return name, ""
	}
	if ex.Kind == ast.KindPropertyAccessExpression {
		pa := ex.AsPropertyAccessExpression()
		if pa != nil && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Name() != nil && pa.Name().Kind == ast.KindIdentifier {
			return pa.Expression.Text() + "_" + pa.Name().Text(), ""
		}
	}
	return "", "new expression is not lowerable"
}

// saLowerNewStmt lowering 裸 `new C();` 语句的构造半程（返回待释句柄，
// 释放由调用方经既有释放口；构造副作用保留；上游 jsDocPrivateConstructor.sai
// 同形 `alloc` + `!`；封存声明绑定 saLowerNewClass 同核）。
func saLowerNewStmt(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	ne := e.AsNewExpression()
	cname, msg := saNewStmtClassName(ne)
	if msg != "" {
		return "", msg
	}
	return saLowerNewClass(w, cname, ne, scope, pos, refusals, nextTemp)
}

func saLowerNewClass(w printer.EmitTextWriter, name string, ce *ast.NewExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	def, ok := scope.classes[name]
	if !ok {
		if saIsStdOpaqueClass(name) {
			return saLowerStdOpaqueNew(w, name, ce, scope, pos, refusals, nextTemp)
		}
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
	// 头槽归属（嵌套递归同记；具名绑定时消费，余下返前释放；
	// 封存 lowerObjectLiteral 尾 declareOwned(h) 同形）。
	saOwnTemp(scope, h)
	if msg := saLowerFieldInits(w, h, def, scope, pos, refusals, nextTemp); msg != "" {
		return "", msg
	}
	if def.ctor == nil {
		// 无显式构造：多余实参直接丢弃（P-newargs：上游同形忽略，
		// 无求值无副作用；有参构造沿下 arity 门）。
		return h, ""
	}
	params := def.ctor.Parameters()
	// (par_notexp, JEV paramprop_wire 0.99) 缺省实参：仅参数属性且
	// 字面量初值者以初值补齐（TS 缺省语义）；余形沿旧 arity 门。
	if len(argNodes) > len(params) {
		return "", fmt.Sprintf("new %s takes %d arguments (%d given)", name, len(params), len(argNodes))
	}
	if len(argNodes) < len(params) {
		for i := len(argNodes); i < len(params); i++ {
			pd := params[i].AsParameterDeclaration()
			if pd == nil || !ast.IsParameterPropertyDeclaration(params[i], def.ctor) || pd.Initializer == nil {
				return "", fmt.Sprintf("new %s takes %d arguments (%d given)", name, len(params), len(argNodes))
			}
			if !saIsPureLiteral(saUnwrapTransparent(pd.Initializer)) {
				return "", "constructor parameter shape is not lowerable"
			}
			argNodes = append(argNodes, pd.Initializer)
		}
	}
	owner := name
	if def.ctorOwner != "" {
		owner = def.ctorOwner
	}
	// wiring 目标种预扫（`this.f = param` 的 f 种决定实参求值器；str 域走串求值）。
	wantStr := saCtorWiringKinds(def.ctor, owner, scope)
	// f64 目标预扫（`this.r = p`/`p * 2` 的 p 走浮求值；str 臂优先，见下）。
	wantF64 := saCtorWiringKindsF64(def.ctor, owner, scope)
	paramVal := map[string]string{}
	fnArgs := map[string]*ast.Node{} // 构造箭头实参（`this.f = k` 捕获位消费）
	for i, p := range params {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			return "", "destructured constructor parameters are not lowerable"
		}
		// (par_notexp, JEV paramprop_wire) 初值门放宽：实参已给
		// 时初值被忽略（TS 语义）；缺省位已在上由参数属性字面
		// 量初值补齐，余缺省形在 arity 门已拒；仅余 rest/? 拒。
		if pd.DotDotDotToken != nil || pd.QuestionToken != nil {
			return "", "constructor parameter shape is not lowerable"
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			return "", "destructured constructor parameters are not lowerable"
		}
		a := argNodes[i]
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			// 函数值实参：仅构造 wiring 捕获位消费（`this.f = k` 记表 + 零槽；参数属性配函数值大声拒；
			// 封存 wireCtorFieldStore:10022-10033）。
			if ast.IsParameterPropertyDeclaration(p, def.ctor) {
				return "", "parameter property " + nm.Text() + " cannot be a function value"
			}
			if fnArgs == nil {
				fnArgs = map[string]*ast.Node{}
			}
			fnArgs[nm.Text()] = a
			continue
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
			// 嵌套布局形参（`p: P`）：对象字面按子布局构造，实例直传须同布局。
			if pd.Type.Kind == ast.KindTypeReference && pd.Type.AsTypeReferenceNode() != nil && pd.Type.AsTypeReferenceNode().TypeName != nil && pd.Type.AsTypeReferenceNode().TypeName.Kind == ast.KindIdentifier {
				if sub, ok := scope.classes[pd.Type.AsTypeReferenceNode().TypeName.Text()]; ok {
					if a != nil && a.Kind == ast.KindObjectLiteralExpression {
						v, _, msg := saLowerObjectLiteral(w, a, sub.name, scope, pos, refusals, nextTemp)
						if msg != "" {
							return "", msg
						}
						paramVal[nm.Text()] = v
						continue
					}
					if a != nil && a.Kind == ast.KindIdentifier {
						if k, ok := scope.types[a.Text()]; ok && k == "inst:"+sub.name {
							paramVal[nm.Text()] = a.Text()
							continue
						}
					}
				}
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
		// 可空实例联合形参（`next: Node|null`）：同布局实例直传，空字面量
		// 吸收为 0 句柄；与方法内联/函数形参位同源；错布局沿旧门。
		if pd.Type != nil && pd.Type.Kind == ast.KindUnionType {
			if inst, ok := saUnionInstKind(pd.Type.AsUnionTypeNode(), scope.classes); ok && len(inst) > 5 {
				lname := inst[5:]
				if a != nil && a.Kind == ast.KindIdentifier {
					if k, ok := scope.types[a.Text()]; ok && k == "inst:"+lname {
						paramVal[nm.Text()] = a.Text()
						continue
					}
				}
				if saIsNullLit(a, scope) {
					paramVal[nm.Text()] = "0"
					continue
				}
			}
		}
		if wantF64[nm.Text()] {
			v, msg := saEvalF64(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			paramVal[nm.Text()] = v
			continue
		}
		if pd.Type != nil {
			if k, ok := saAnnotKind(pd.Type); ok && k == "f64" {
				v, msg := saEvalF64(w, a, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				paramVal[nm.Text()] = v
				continue
			}
			// 串注解直路由（`this.n = t.length` 等读位用串形参；
			// 名单/计算位 want 扫描已先行，此处只补读位漏网）。
			if k, ok := saAnnotKind(pd.Type); ok && k == "str" {
				v, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				paramVal[nm.Text()] = v
				continue
			}
		}
		v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		// 构造器 i32 形参记种检查（串/实例句柄禁入；与声明/实参位同形；铁律 4）。
		if msg := saCheckI32Value(scope, v); msg != "" {
			return "", msg
		}
		paramVal[nm.Text()] = v
	}
	// (par_notexp, JEV paramprop_wire 0.99) 参数属性槽写回由
	// saWireCtorBody 的 inject() 承接（已按记录槽存 paramVal）；
	// 此处不另存，禁重复写；缺省位已在上由字面量初值补齐进
	// paramVal，故 inject() 对缺省参数属性亦能写回。
	body := def.ctor.Body()
	if body == nil {
		return h, ""
	}
	if !saWireCtorBody(w, h, owner, def.ctor, paramVal, fnArgs, scope, pos, refusals, nextTemp) {
		return "", "unwirable"
	}
	return h, ""
}

// saLowerFieldInits 落实例字段初值（继承链基先派后，先于构造 wiring；
// 只收字面量初值——数/负数/布尔/串，求值与调用方作用域无关；余形及
// 「派生重声明且祖先有构造」之序依赖形大声拒，禁旧路静默丢初值）。
func saLowerFieldInits(w printer.EmitTextWriter, h string, def *saClassDef, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	var chain []*saClassDef
	for c, n := def, 0; c != nil && n < 32; n++ {
		chain = append([]*saClassDef{c}, chain...)
		c = scope.classes[c.parent]
	}
	for i, c := range chain {
		for _, fk := range c.finitOrder {
			init := saUnwrapTransparent(c.finits[fk])
			for _, a := range chain[:i] {
				if _, ok := a.offsets[fk]; ok && a.ctor != nil {
					return "field " + fk + " initializer overrides a base constructor field (not lowerable)"
				}
			}
			if !saIsPureLiteral(init) {
				return "field " + fk + " initializer must be a literal"
			}
			off := def.offsets[fk]
			switch def.fkinds[fk] {
			case "str":
				v, msg := saEvalStr(w, init, scope, pos, refusals, nextTemp)
				if msg != "" {
					return msg
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
			case "i32", "":
				v, msg := saEvalI32(w, init, scope, pos, refusals, nextTemp)
				if msg != "" {
					return msg
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
			case "f64":
				v, msg := saEvalF64(w, init, scope, pos, refusals, nextTemp)
				if msg != "" {
					return msg
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, off, v))
			case "arr":
				if init.Kind != ast.KindArrayLiteralExpression {
					return "field " + fk + " initializer must be an array literal"
				}
				v, msg := saArrValueOf(w, init, scope, pos, refusals, nextTemp)
				if msg != "" {
					return msg
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
			case "inst":
				sub, ok := def.fsub[fk]
				if !ok || init.Kind != ast.KindObjectLiteralExpression {
					return "field " + fk + " initializer must be an object literal of a recorded layout"
				}
				v, _, msg := saLowerObjectLiteral(w, init, sub, scope, pos, refusals, nextTemp)
				if msg != "" {
					return msg
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
			default:
				return "field " + fk + " initializer kind is not lowerable"
			}
		}
	}
	return ""
}

// saIsPureLiteral 报告字面量树（数/负数/布尔/串/null，及其数组与对象字面量
// 嵌套；无标识符引用，故求值与调用方作用域无关）。
func saIsPureLiteral(e *ast.Node) bool {
	e = saUnwrapTransparent(e)
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return true
	case ast.KindPrefixUnaryExpression:
		pu := e.AsPrefixUnaryExpression()
		return (pu.Operator == ast.KindMinusToken || pu.Operator == ast.KindPlusToken) &&
			pu.Operand != nil && pu.Operand.Kind == ast.KindNumericLiteral
	case ast.KindArrayLiteralExpression:
		for _, el := range e.AsArrayLiteralExpression().Elements.Nodes {
			if !saIsPureLiteral(el) {
				return false
			}
		}
		return true
	case ast.KindObjectLiteralExpression:
		for _, p := range e.AsObjectLiteralExpression().Properties.Nodes {
			if p == nil || p.Kind != ast.KindPropertyAssignment || !saIsPureLiteral(p.AsPropertyAssignment().Initializer) {
				return false
			}
		}
		return true
	}
	return false
}

// saCtorWiringKinds 预扫构造体 `this.f = param` 的 str 目标（返回 param 名集；
// 非 wiring 语句由 wire 主路拒，此处只收形状完整的；`super(...)` 转发按基构造
// 同例递归解（环由记录期继承圈门保证无环，另加深度守卫）。
func saCtorWiringKinds(ctor *ast.Node, owner string, scope *saScope) map[string]bool {
	return saCtorWiringKindsDepth(ctor, owner, scope, 0)
}

// saWiringCollectIdents 收右值纯形中缀名（括号/前缀/算术位二元下钻；调用/成员/
// 新建等一律止步（接线门同形拒）；供 str 目标 want 扫描。）
func saWiringCollectIdents(e *ast.Node, out map[string]bool) {
	if e == nil {
		return
	}
	if e.Kind == ast.KindIdentifier {
		out[e.Text()] = true
		return
	}
	switch e.Kind {
	case ast.KindParenthesizedExpression:
		if pe := e.AsParenthesizedExpression(); pe != nil {
			saWiringCollectIdents(pe.Expression, out)
		}
	case ast.KindPrefixUnaryExpression:
		if un := e.AsPrefixUnaryExpression(); un != nil && un.Operand != nil {
			switch un.Operator {
			case ast.KindMinusToken, ast.KindPlusToken, ast.KindTildeToken, ast.KindExclamationToken:
				saWiringCollectIdents(un.Operand, out)
			}
		}
	case ast.KindBinaryExpression:
		if be := e.AsBinaryExpression(); be != nil && be.OperatorToken != nil {
			switch be.OperatorToken.Kind {
			case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindSlashToken,
				ast.KindPercentToken, ast.KindAsteriskAsteriskToken,
				ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken,
				ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken,
				ast.KindGreaterThanGreaterThanGreaterThanToken:
				saWiringCollectIdents(be.Left, out)
				saWiringCollectIdents(be.Right, out)
			}
		}
	}
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
		if bin.Right == nil {
			continue
		}
		fname := pa.Name().Text()
		if strings.HasPrefix(fname, "#") {
			fname = saPrivKey(owner, fname)
		}
		if bin.Right.Kind == ast.KindIdentifier {
			if def.fkinds[fname] == "str" {
				want[bin.Right.Text()] = true
			}
			continue
		}
		// str 目标计算右值中缀名亦按串求值（`this.s = t + "!"`；中缀收集器
		// 只下钻纯形，调用等不贡献名（接线门同形拒）；i32/f64 目标沿旧门）。
		if def.fkinds[fname] == "str" {
			saWiringCollectIdents(bin.Right, want)
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

// saCtorWiringKindsF64 预扫 f64 目标（`this.r = p` 名形与 `this.r = p * 2` 计算形；
// super 转发经 f64 位递归；str 版镜像同形；i32 目标沿默认臂）。
func saCtorWiringKindsF64(ctor *ast.Node, owner string, scope *saScope) map[string]bool {
	return saCtorWiringKindsF64Depth(ctor, owner, scope, 0)
}

func saCtorWiringKindsF64Depth(ctor *ast.Node, owner string, scope *saScope, depth int) map[string]bool {
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
		// super 转发：基形参 f64 位透传给外层同名实参。
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
		bwant := saCtorWiringKindsF64Depth(bdef.ctor, def.parent, scope, depth+1)
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
		if bin.Right == nil {
			continue
		}
		fname := pa.Name().Text()
		if strings.HasPrefix(fname, "#") {
			fname = saPrivKey(owner, fname)
		}
		if def.fkinds[fname] != "f64" {
			continue
		}
		if bin.Right.Kind == ast.KindIdentifier {
			want[bin.Right.Text()] = true
			continue
		}
		// f64 目标计算右值中缀名（`this.r = p * 2`；收集器只下钻纯形）。
		saWiringCollectIdents(bin.Right, want)
	}
	// 参数属性隐式 wiring 的 f64 位。
	for _, p := range ctor.Parameters() {
		if !ast.IsParameterPropertyDeclaration(p, ctor) {
			continue
		}
		nm := p.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		if def.fkinds[nm.Text()] == "f64" {
			want[nm.Text()] = true
		}
	}
	return want
}

// saWireCtorBody 解释构造体语句（`super(...)` 委托基 wiring + `this.f = param` +
// 参数属性隐式 `this.p = p` 注在顶层 super() 后（无 super 置顶），显式 wiring 优先；
// 形状证据：封存 wireCtorBody + wireCtorFieldStore + visitConstructorBody/Worker）。
// saWiringPureLit 报告接线右值为纯字面量形（数值/串/布尔/null 字面量、
// 括号透明、前缀一元（-!+~）、算术位二元；名/调用/成员/新建/数组/对象/模板
// 一律否（含名即 caller 影子误值风险，沿旧门）。
func saWiringPureLit(e *ast.Node) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return true
	case ast.KindParenthesizedExpression:
		if pe := e.AsParenthesizedExpression(); pe != nil {
			return saWiringPureLit(pe.Expression)
		}
		return false
	case ast.KindPrefixUnaryExpression:
		if un := e.AsPrefixUnaryExpression(); un != nil && un.Operand != nil {
			switch un.Operator {
			case ast.KindMinusToken, ast.KindPlusToken, ast.KindTildeToken, ast.KindExclamationToken:
				return saWiringPureLit(un.Operand)
			}
		}
		return false
	case ast.KindBinaryExpression:
		if be := e.AsBinaryExpression(); be != nil && be.OperatorToken != nil {
			switch be.OperatorToken.Kind {
			case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindSlashToken,
				ast.KindPercentToken, ast.KindAsteriskAsteriskToken,
				ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken,
				ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken,
				ast.KindGreaterThanGreaterThanGreaterThanToken:
				return saWiringPureLit(be.Left) && saWiringPureLit(be.Right)
			}
		}
		return false
	}
	return false
}

// saWiringNamedLit 报告接线右值除名外纯字面量形（名须全在 allow 集（已绑形参）；
// 名外规则与 saWiringPureLit 同形；另收已绑 arr/inst 形参的下标/取长/字段读
// （`a[0]`/`a.length`/`b.f`，`?.`/链式沿旧门；错种由求值位大声拒）与逻辑值/
// 三元形（`f && true`/`f ? 10 : 20`，操作数递归同门）；
// 调用/new/模板一律否）。
// saIsIdentText reports identifier-shaped text.
func saIsIdentText(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// saOperandTextKind classifies operand text (literal shapes direct; temps/names
// strictly via scope table; unmarked yields "" (callers fail loud).
func saOperandTextKind(op string, scope *saScope) string {
	if saIsTempOp(op) || saIsIdentText(op) {
		if k, ok := scope.types[op]; ok && (k == "i32" || k == "f64" || k == "str") {
			return k
		}
		return ""
	}
	if saIsDecIntLit(op) {
		return "i32"
	}
	if saIsFloatLit(op) {
		return "f64"
	}
	return ""
}

func saWiringNamedLit(e *ast.Node, allow map[string]bool, scope *saScope) bool {
	if e == nil {
		return false
	}
	if e.Kind == ast.KindIdentifier {
		return allow[e.Text()]
	}
	switch e.Kind {
	case ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return true
	case ast.KindParenthesizedExpression:
		if pe := e.AsParenthesizedExpression(); pe != nil {
			return saWiringNamedLit(pe.Expression, allow, scope)
		}
		return false
	case ast.KindPrefixUnaryExpression:
		if un := e.AsPrefixUnaryExpression(); un != nil && un.Operand != nil {
			switch un.Operator {
			case ast.KindMinusToken, ast.KindPlusToken, ast.KindTildeToken, ast.KindExclamationToken:
				return saWiringNamedLit(un.Operand, allow, scope)
			}
		}
		return false
	case ast.KindBinaryExpression:
		if be := e.AsBinaryExpression(); be != nil && be.OperatorToken != nil {
			switch be.OperatorToken.Kind {
			case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindSlashToken,
				ast.KindPercentToken, ast.KindAsteriskAsteriskToken,
				ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken,
				ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken,
				ast.KindGreaterThanGreaterThanGreaterThanToken,
				ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken,
				ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken,
				ast.KindLessThanToken, ast.KindLessThanEqualsToken,
				ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken:
				return saWiringNamedLit(be.Left, allow, scope) && saWiringNamedLit(be.Right, allow, scope)
			case ast.KindAmpersandAmpersandToken, ast.KindBarBarToken:
				// 逻辑值语义（`f && true`；操作数递归同门；求值经既有逻辑值位短路）。
				return saWiringNamedLit(be.Left, allow, scope) && saWiringNamedLit(be.Right, allow, scope)
			case ast.KindQuestionQuestionToken:
				// 空合（`t?.length ?? -1`；左右操作数递归同门；求值经既有空合核）。
				return saWiringNamedLit(be.Left, allow, scope) && saWiringNamedLit(be.Right, allow, scope)
			}
		}
		return false
	case ast.KindConditionalExpression:
		// 三元（`f ? 10 : 20`；条件与双臂递归同门；求值经既有三元位）。
		if ce := e.AsConditionalExpression(); ce != nil {
			return saWiringNamedLit(ce.Condition, allow, scope) &&
				saWiringNamedLit(ce.WhenTrue, allow, scope) &&
				saWiringNamedLit(ce.WhenFalse, allow, scope)
		}
		return false
	case ast.KindElementAccessExpression:
		// 数组下标读（`a[0]`；基须已绑 arr 形参；索引递归同门）与
		// 数组可选下标（`a?.[i]`；基须已绑 arr 形参，空基归零；串元柄禁入，
		// 求值经 `saLowerOptionalIndex` 空守卫）与
		// 串可选下标（`t?.[i]`；基须已绑 str 形参；索引递归同门；求值经串位守卫）。
		ea := e.AsElementAccessExpression()
		if ea == nil || ea.Expression == nil ||
			ea.Expression.Kind != ast.KindIdentifier || !allow[ea.Expression.Text()] {
			return false
		}
		if ea.QuestionDotToken != nil {
			if k, ok := scope.types[ea.Expression.Text()]; !ok {
				return false
			} else if k == "str" {
				return saWiringNamedLit(ea.ArgumentExpression, allow, scope)
			} else if k != "arr" {
				return false
			}
			if scope.arrStr != nil && scope.arrStr[ea.Expression.Text()] {
				return false
			}
			return saWiringNamedLit(ea.ArgumentExpression, allow, scope)
		}
		if k, ok := scope.types[ea.Expression.Text()]; !ok || k != "arr" {
			return false
		}
		// 串元柄禁入共享门（句柄位与整数槽同宽，误读即指针当整数；
		// 串域由串元臂另收，此处一律拒）。
		if scope.arrStr != nil && scope.arrStr[ea.Expression.Text()] {
			return false
		}
		return saWiringNamedLit(ea.ArgumentExpression, allow, scope)
	case ast.KindPropertyAccessExpression:
		// 取长/实例字段读（`a.length`/`b.f`；基须已绑 arr/inst 形参；
		// 错种由求值位大声拒）与嵌套链读（`oo.p.v` 两级链；
		// 根须已绑形参，布局由 saChainLeafDef 纯判定；深层链/`?.`/元素基沿旧门）与
		// 串可选取长（`t?.length`；基须已绑 str 形参；其余 `?.` 成员沿旧门）。
		pa := e.AsPropertyAccessExpression()
		if pa == nil || pa.Expression == nil || pa.Name() == nil {
			return false
		}
		if pa.Expression.Kind == ast.KindThisKeyword {
			// this 读（`this.m`；属主布局数据字段且有 finit 初值——JS 字段初始化
			// 先于构造体，无初值读 undefined，本仓无值沿旧门；仅收 i32/str 种，
			// arr/inst/f64 柄入值位系静默错码（正常位同洞既有，本门禁扩散），
			// 错种由求值位大声拒；求值经实例通道）。
			if pa.QuestionDotToken != nil || pa.Name().Kind != ast.KindIdentifier {
				return false
			}
			nm := pa.Name().Text()
			if strings.HasPrefix(nm, "#") {
				return false
			}
			def, ok := scope.classes[scope.thisClass]
			if !ok || def == nil {
				return false
			}
			if _, ok := def.offsets[nm]; !ok {
				return false
			}
			if fk := def.fkinds[nm]; fk != "i32" && fk != "str" && fk != "" {
				return false
			}
			if def.finits == nil {
				return false
			}
			if _, ok := def.finits[nm]; !ok {
				return false
			}
			return true
		}
		if pa.QuestionDotToken != nil {
			if pa.Name().Text() != "length" || pa.Expression.Kind != ast.KindIdentifier ||
				!allow[pa.Expression.Text()] {
				return false
			}
			if k, ok := scope.types[pa.Expression.Text()]; !ok || k != "str" {
				return false
			}
			return true
		}
		if pa.Expression.Kind == ast.KindPropertyAccessExpression {
			inner := pa.Expression.AsPropertyAccessExpression()
			if inner == nil || inner.QuestionDotToken != nil || inner.Expression == nil ||
				inner.Expression.Kind != ast.KindIdentifier || inner.Name() == nil ||
				!allow[inner.Expression.Text()] {
				return false
			}
			if _, _, ok := saChainLeafDef(e, scope); !ok {
				return false
			}
			return true
		}
		if pa.Expression.Kind != ast.KindIdentifier ||
			!allow[pa.Expression.Text()] {
			return false
		}
		k, ok := scope.types[pa.Expression.Text()]
		if !ok {
			return false
		}
		if pa.Name().Text() == "length" && (k == "arr" || k == "str") {
			return true
		}
		if len(k) > 5 && k[:5] == "inst:" {
			return true
		}
		return false
	}
	return false
}

// saWiringStrRead 报告串接线读形（串字面量/已绑串元下标读/串下标取字/`+` 拼接递归；
// `?.`/链式沿旧门；调用/模板/new 一律否，求值位大声拒）。
func saWiringStrRead(e *ast.Node, allow map[string]bool, scope *saScope) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return true
	case ast.KindParenthesizedExpression:
		if pe := e.AsParenthesizedExpression(); pe != nil {
			return saWiringStrRead(pe.Expression, allow, scope)
		}
		return false
	case ast.KindBinaryExpression:
		if be := e.AsBinaryExpression(); be != nil && be.OperatorToken != nil &&
			be.OperatorToken.Kind == ast.KindPlusToken {
			return saWiringStrRead(be.Left, allow, scope) && saWiringStrRead(be.Right, allow, scope)
		}
		return false
	case ast.KindElementAccessExpression:
		ea := e.AsElementAccessExpression()
		if ea == nil || ea.QuestionDotToken != nil || ea.Expression == nil ||
			ea.Expression.Kind != ast.KindIdentifier ||
			!allow[ea.Expression.Text()] {
			return false
		}
		// 串下标读（`t[i]` 取单字柄；基为 str 种，与串元臂同门）。
		if k, ok := scope.types[ea.Expression.Text()]; ok && k == "str" {
			return saWiringNamedLit(ea.ArgumentExpression, allow, scope)
		}
		if scope.arrStr == nil || !scope.arrStr[ea.Expression.Text()] {
			return false
		}
		return saWiringNamedLit(ea.ArgumentExpression, allow, scope)
	}
	return false
}

func saWireCtorBody(w printer.EmitTextWriter, h, owner string, ctor *ast.Node, paramVal map[string]string, fnArgs map[string]*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	// 接线期 this 读（`this.m` 经实例通道直读在建柄；finit 先行故初值 sound；
	// super/注入/快照环无 this 求值，不受影响；退出恢复。thisClass 同步为
	// 属主（门经布局验证；外层值恢复）。
	savedSelf, savedClass := scope.thisSelf, scope.thisClass
	scope.thisSelf, scope.thisClass = h, owner
	defer func() { scope.thisSelf, scope.thisClass = savedSelf, savedClass }()
	// 名单接线串实参集合（arg 环已按串求值；串柄入 i32/f64 域即错，见下守卫；
	// 与 new 侧 wantStr 同源重算（纯扫，零发射）。
	// 名单接线串/浮实参集合（arg 环已按串/浮求值；错域入即错，见下守卫；
	// 与 new 侧同源重算（纯扫，零发射）；绑定定种同序优先（串>浮>注解>字面）。
	wireWantStr := saCtorWiringKinds(ctor, owner, scope)
	wireWantF64 := saCtorWiringKindsF64(ctor, owner, scope)
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
			if def.fkinds[pname] == "str" || def.fkinds[pname] == "arr" || def.fkinds[pname] == "inst" {
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
				continue
			}
			if def.fkinds[pname] == "f64" {
				w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, off, v))
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
	// 计算右值预扫（无 `this.f = <非参名右值含名>` 即零发射，存量逐字不动；
	// 有名右值才进绑定；纯字面量右值沿旧径无绑定）。
	needWireBind := false
	for _, ws := range stmts {
		if ws == nil || ws.Kind != ast.KindExpressionStatement {
			continue
		}
		wex := ws.AsExpressionStatement().Expression
		if wex == nil || wex.Kind != ast.KindBinaryExpression {
			continue
		}
		wbin := wex.AsBinaryExpression()
		if wbin.OperatorToken == nil || wbin.Right == nil {
			continue
		}
		if wbin.OperatorToken.Kind != ast.KindEqualsToken {
			// 复合赋值接线预扫（`this.f op= RHS` 读参须绑定；纯字面量沿旧径无绑定）。
			if !saWiringPureLit(wbin.Right) {
				needWireBind = true
				break
			}
			continue
		}
		if wbin.Right.Kind == ast.KindIdentifier {
			continue
		}
		if saWiringPureLit(wbin.Right) {
			continue
		}
		needWireBind = true
		break
	}
	boundParams := map[string]bool{}
	type wireSaved struct {
		typ     string
		tok     bool
		alias   string
		aok     bool
		own     *saOwn
		ownOk   bool
		mv      string
		mvok    bool
		smv     bool
		smvok   bool
		save    string
		hasSave bool
	}
	wireKeep := map[string]*wireSaved{}
	restoreWireTypes := func() {
		for name, s := range wireKeep {
			if s.tok {
				scope.types[name] = s.typ
			} else {
				delete(scope.types, name)
			}
			if s.aok {
				scope.mathAlias[name] = s.alias
			} else {
				delete(scope.mathAlias, name)
			}
			if s.ownOk {
				scope.ownState[name] = s.own
			} else {
				delete(scope.ownState, name)
			}
			if s.mvok {
				if scope.mapVals == nil {
					scope.mapVals = map[string]string{}
				}
				scope.mapVals[name] = s.mv
			} else if scope.mapVals != nil {
				delete(scope.mapVals, name)
			}
			if s.smvok {
				if s.smv {
					if scope.arrStr == nil {
						scope.arrStr = map[string]bool{}
					}
					scope.arrStr[name] = true
				} else if scope.arrStr != nil {
					delete(scope.arrStr, name)
				}
			}
		}
	}
	defer restoreWireTypes()
	if needWireBind {
		// 具名计算接线形参绑定（`this.n = k + 1`；paramVal 操作数快照拷贝；种按注解，
		// 无注解按操作数形推断（柄须显式记种，否则不绑）；arr 记 arr、类引用记
		// inst（名单接线经 paramVal 旧径）；caller 同名 plain 值快照恢复（live 归属沿方法内联同门拒）；
		// 恢复放 loop 尾成功径（失败品废弃），记种放 defer 全径）。
		for _, cp := range ctor.Parameters() {
			pd := cp.AsParameterDeclaration()
			if pd == nil {
				continue
			}
			if pd.DotDotDotToken != nil || pd.QuestionToken != nil || pd.Initializer != nil {
				continue
			}
			nm := pd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			pname := nm.Text()
			op, ok := paramVal[pname]
			if !ok {
				continue
			}
			// 定种序与 arg 环求值序一致（串>浮>注解>arr/inst>字面；余种不绑）：
			// 到此之操作数必与其求值器同种（否则 arg 环已拒，无 paramVal）。
			kind := ""
			switch {
			case wireWantStr[pname]:
				kind = "str"
			case wireWantF64[pname]:
				kind = "f64"
			case pd.Type != nil:
				if ak, ok := saAnnotKind(pd.Type); ok {
					switch ak {
					case "i32":
						kind = "i32"
					case "f64":
						kind = "f64"
					case "str":
						kind = "str"
					}
				}
			}
			if kind == "" && pd.Type != nil {
				// arr/inst 读接线绑定（`this.n = a[0]/a.length/b.n`；快照拷贝
				// 复标量 `add …, 0` 同形，读位经句柄总线/实例通道自动贯通）。
				if ak, ok := saAnnotKind(pd.Type); ok && ak == "arr" {
					kind = "arr"
				} else if pd.Type.Kind == ast.KindTypeReference && pd.Type.AsTypeReferenceNode() != nil &&
					pd.Type.AsTypeReferenceNode().TypeName != nil &&
					pd.Type.AsTypeReferenceNode().TypeName.Kind == ast.KindIdentifier {
					if _, ok := scope.classes[pd.Type.AsTypeReferenceNode().TypeName.Text()]; ok {
						kind = "inst:" + pd.Type.AsTypeReferenceNode().TypeName.Text()
					}
				}
			}
			if kind == "" {
				// 无注解非路由：字面量形直断（名/柄不推，沿旧门）。
				if saIsDecIntLit(op) {
					kind = "i32"
				} else if saIsFloatLit(op) && !saIsIdentText(op) && !saIsTempOp(op) {
					kind = "f64"
				}
			}
			ws := &wireSaved{}
			if oldTyp, ok := scope.types[pname]; ok {
				if oo, ook := scope.ownState[pname]; ook && oo != nil && oo.heap && !oo.consumed && !oo.released {
					ln, col := pos(cp.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor parameter " + pname + " shadows a live binding (rename it)"})
					return false
				}
				ws.typ, ws.tok = oldTyp, true
				if oo, ook := scope.ownState[pname]; ook && oo != nil {
					oc := *oo
					ws.own, ws.ownOk = &oc, true
				}
				if oa, aok := scope.mathAlias[pname]; aok {
					ws.alias, ws.aok = oa, true
				}
				if scope.mapVals != nil {
					if mv, mvok := scope.mapVals[pname]; mvok {
						ws.mv, ws.mvok = mv, true
					}
				}
				sv := fmt.Sprintf("__wired_save_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = %s\n", sv, pname))
				ws.save, ws.hasSave = sv, true
			}
			wireKeep[pname] = ws
			switch kind {
			case "i32":
				w.Write(fmt.Sprintf("  %s = add %s, 0\n", pname, op))
			case "f64":
				w.Write(fmt.Sprintf("  %s = fadd %s, 0.0\n", pname, op))
			case "str":
				w.Write(fmt.Sprintf("  %s = add %s, 0\n", pname, op))
			case "arr":
				w.Write(fmt.Sprintf("  %s = add %s, 0\n", pname, op))
				if pd.Type != nil && saIsStringArrayAnnot(pd.Type) {
					if kept, ok := wireKeep[pname]; ok {
						kept.smv = scope.arrStr[pname]
						kept.smvok = true
					}
					saMarkArrStr(scope, pname)
				}
			default:
				if strings.HasPrefix(kind, "inst:") {
					w.Write(fmt.Sprintf("  %s = add %s, 0\n", pname, op))
				}
			}
			scope.types[pname] = kind
			saDeclarePlain(scope, pname)
			boundParams[pname] = true
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
		if bin.OperatorToken == nil ||
			bin.Left == nil || bin.Left.Kind != ast.KindPropertyAccessExpression {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
			return false
		}
		if bin.OperatorToken.Kind != ast.KindEqualsToken {
			// 复合赋值接线（`this.n += k`；读-改-写回经既有实例字段复合臂；
			// thisSelf 已注入；finit 在场门（无初值读 undefined，本仓无值）与
			// 私有域沿旧门；失败沿复合臂拒因，不另附门）。
			cpa := bin.Left.AsPropertyAccessExpression()
			if cpa == nil || cpa.Expression == nil || cpa.Expression.Kind != ast.KindThisKeyword || cpa.Name() == nil {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
				return false
			}
			cnm := cpa.Name().Text()
			if cdef, ok := scope.classes[owner]; !ok || cdef == nil {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown class " + owner})
				return false
			} else if _, ok := cdef.offsets[cnm]; !ok {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + cnm + " is not in the " + owner + " layout"})
				return false
			} else if cdef.finits == nil {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
				return false
			} else if _, ok := cdef.finits[cnm]; !ok {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor of " + owner + " supports only this.f = param wirings"})
				return false
			}
			if !saLowerCompound(w, bin, scope, pos, refusals, nextTemp, s) {
				return false
			}
			continue
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
			// i32 字面量直存（`this.x = 3`；字段须 i32 种；串/句柄/浮点/
			// 十六进制/负数字面沿旧门大声拒）。
			if br := bin.Right; br != nil && br.Kind == ast.KindNumericLiteral &&
				def.fkinds[fname] == "i32" && !saIsFloatLit(br.Text()) && saIsDecIntLit(br.Text()) {
				w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, br.Text()))
				continue
			}
			// f64 字面量直存（`this.r = 2.5`；字段须 f64 种；整字面补 .0
			// 同 finit 臂；负数/十六进制沿旧门）。
			if br := bin.Right; br != nil && br.Kind == ast.KindNumericLiteral &&
				def.fkinds[fname] == "f64" && (saIsFloatLit(br.Text()) || saIsDecIntLit(br.Text())) {
				fv := br.Text()
				if saIsDecIntLit(fv) {
					fv += ".0"
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, off, fv))
				continue
			}
			// 串字面量直存（`this.s = "hi"`；字段须 str 种；存柄移交实例持有，
			// 与 finit 串臂同形，不释）。
			if br := bin.Right; br != nil && br.Kind == ast.KindStringLiteral &&
				def.fkinds[fname] == "str" {
				sh := saLowerStringLiteral(w, br.Text(), scope, nextTemp)
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, sh))
				continue
			}
			// 布尔字面量直存（`this.ok = true`；字段为 i32 槽（bool 恒 i32 种）；1/0）。
			if br := bin.Right; br != nil && (br.Kind == ast.KindTrueKeyword || br.Kind == ast.KindFalseKeyword) &&
				(def.fkinds[fname] == "i32" || def.fkinds[fname] == "") {
				bv := "1"
				if br.Kind == ast.KindFalseKeyword {
					bv = "0"
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, bv))
				continue
			}
			// 串元下标读（`this.s = a[i]/a[i]+"!"`；a 须已绑 string[] 形参；
			// 读形见 saWiringStrRead；i32 位永不经此口，禁句柄当整数；`?.` 沿旧门）。
			if def.fkinds[fname] == "str" && bin.Right != nil &&
				saWiringStrRead(bin.Right, boundParams, scope) {
				v, msg := saEvalStr(w, bin.Right, scope, pos, refusals, nextTemp)
				if msg != "" {
					ln, col := pos(s.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
					return false
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
				continue
			}
			// 纯字面量表达式右值（`this.n = 2 + 3`/`-7`/`"a" + "b"`；无名绑定零作用域
			// 风险；含名/调用/成员一律沿旧门，禁 caller 影子误值）：i32/f64/串按域种
			// 求值落存（bool 经 saEvalBool 取 1/0 入 i32 槽）；求值失败透传其声。
			if saWiringPureLit(bin.Right) || saWiringNamedLit(bin.Right, boundParams, scope) {
				fk := def.fkinds[fname]
				if fk == "i32" || fk == "" {
					if br := bin.Right; br != nil && (br.Kind == ast.KindTrueKeyword || br.Kind == ast.KindFalseKeyword) {
						bv := "1"
						if br.Kind == ast.KindFalseKeyword {
							bv = "0"
						}
						w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, bv))
						continue
					}
					if bo, bom := saEvalBool(w, bin.Right, scope, pos, refusals, nextTemp); bom == "" {
						if bo == "0" || bo == "1" {
							w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, bo))
							continue
						}
					}
					v, msg := saEvalI32(w, bin.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						ln, col := pos(s.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
						return false
					}
					if msg := saCheckI32Value(scope, v); msg != "" {
						ln, col := pos(s.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
						return false
					}
					// f64 temp 永不沉入 i32 域（与声明位同门，大声）。
					if k, ok := scope.types[v]; ok && k == "f64" {
						ln, col := pos(s.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "float value needs float annotation"})
						return false
					}
					w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
					continue
				}
				if fk == "f64" {
					v, msg := saEvalF64(w, bin.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						ln, col := pos(s.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
						return false
					}
					w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, off, v))
					continue
				}
				if fk == "str" {
					v, msg := saEvalStr(w, bin.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						ln, col := pos(s.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
						return false
					}
					w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
					continue
				}
			}
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor wiring right side must be a parameter name"})
			return false
		}
		v, ok := paramVal[bin.Right.Text()]
		if !ok {
			// 函数值 wiring（`this.f = k` 且 k 为箭头/函数表达式实参）：捕获记表 + 零槽
			// （封存 wireCtorFieldStore:10022-10033 `store h+off, 0 as <saname>` 同形）；其余沿旧门。
			if anode, isFn := fnArgs[bin.Right.Text()]; isFn && anode != nil &&
				(anode.Kind == ast.KindArrowFunction || anode.Kind == ast.KindFunctionExpression) {
				z := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = 0\n", z))
				askind := "i32"
				if fk := def.fkinds[fname]; fk == "str" || fk == "arr" || fk == "inst" {
					askind = "ptr"
				}
				w.Write(fmt.Sprintf("  store %s + %d, %s as %s\n", h, off, z, askind))
				saRecordInstFn(scope, h, fname, anode)
				continue
			}
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "constructor parameter " + bin.Right.Text() + " has no value"})
			return false
		}
		// 名单接线操作数种形检查（右值已按种求值；确定错位大声拒：float 文本/串记种
		// 禁入 i32 域（d1 458277600 实证），串记种禁入 f64 域；未记种柄沿旧门）。
		if fk := def.fkinds[fname]; fk == "i32" || fk == "" {
			if wireWantStr[bin.Right.Text()] {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string operand into i32 field is not lowerable"})
				return false
			}
			if saOperandTextKind(v, scope) == "f64" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "float operand into i32 field is not lowerable"})
				return false
			}
			if k, ok := scope.types[v]; ok && k == "str" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string operand into i32 field is not lowerable"})
				return false
			}
		}
		if def.fkinds[fname] == "f64" {
			if wireWantStr[bin.Right.Text()] {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string operand into f64 field is not lowerable"})
				return false
			}
			if k, ok := scope.types[v]; ok && k == "str" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string operand into f64 field is not lowerable"})
				return false
			}
		}
		// str/arr/inst 域存句柄（右值已按种求值）；f64 域存双精度。
		if def.fkinds[fname] == "str" || def.fkinds[fname] == "arr" || def.fkinds[fname] == "inst" {
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
			continue
		}
		if def.fkinds[fname] == "f64" {
			w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, off, v))
			continue
		}
		w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
	}
	// 形参影子值恢复（成功径内联发射；失败品废弃；记种由 defer 全径恢复）。
	if len(boundParams) > 0 {
		for name, s := range wireKeep {
			if s.hasSave {
				w.Write(fmt.Sprintf("  %s = %s\n", name, s.save))
			}
		}
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
		if pd.DotDotDotToken != nil || pd.QuestionToken != nil {
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
	return saWireCtorBody(w, h, base, bdef.ctor, paramVal, nil, scope, pos, refusals, nextTemp)
}

// saInstArg 求实例实参句柄（绑定标识符/`this`/`new C()`；子类实例可传基形参，
// 展平布局前缀一致，读基域安全；`new` 形经 saLowerNewStmt 求临时句柄直传，
// 无命名绑定故无双重释放，上游 q4b 形同形）。
func saInstArg(w printer.EmitTextWriter, a *ast.Node, want string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
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
	case a != nil && a.Kind == ast.KindNewExpression:
		ne := a.AsNewExpression()
		cname, msg := saNewStmtClassName(ne)
		if msg != "" {
			return "", msg
		}
		have = cname
		h, msg = saLowerNewStmt(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
	default:
		return "", "method instance argument must be a bound instance"
	}
	if saClassSatisfies(scope.classes, have, want) {
		return h, ""
	}
	return "", "method instance argument class mismatch (want " + want + ")"
}

// saRecordInstFn 登记实例函数字段捕获（`new C(arrow)` 经构造 wiring `this.f = k` 落位；
// 去虚化点按 (handle, field) 回放捕获箭头；形状证据：封存 wireCtorFieldStore:10022-10033
// instFnFields + trackBinding:1533-1537 别名透传）。
func saRecordInstFn(scope *saScope, h, field string, arrow *ast.Node) {
	if scope.instFn == nil {
		scope.instFn = map[string]map[string]*ast.Node{}
	}
	if scope.instFn[h] == nil {
		scope.instFn[h] = map[string]*ast.Node{}
	}
	scope.instFn[h][field] = arrow
}

// saCopyInstFn 透传实例函数字段捕获（具名绑定/句柄直传沿用捕获表；无表即无操作）。
func saCopyInstFn(scope *saScope, from, to string) {
	fields, ok := scope.instFn[from]
	if !ok {
		return
	}
	if scope.instFn[to] == nil {
		scope.instFn[to] = map[string]*ast.Node{}
	}
	for f, n := range fields {
		scope.instFn[to][f] = n
	}
}

// saInlineInstanceCallback 回放实例函数字段捕获（`this.pick(e)` 按调用实参内联捕获箭头；
// 句柄实参直传（种由实参绑定查表），标量经 i32 求值；箭头/函数值实参大声拒；
// 形状证据：封存 inlineInstanceCallback:4585-4601 经 callbackValue 同形）。
func saInlineInstanceCallback(w printer.EmitTextWriter, anode *ast.Node, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	params := anode.Parameters()
	if len(argNodes) != len(params) {
		return "", "instance callback takes exact arguments"
	}
	argVals := make([]string, 0, len(argNodes))
	kinds := make([]string, 0, len(argNodes))
	for _, a := range argNodes {
		if a != nil && (a.Kind == ast.KindArrowFunction || a.Kind == ast.KindFunctionExpression) {
			return "", "function arguments to instance callbacks are not lowerable"
		}
		if a != nil && a.Kind == ast.KindIdentifier {
			if k, ok := scope.types[a.Text()]; ok && len(k) > 5 && k[:5] == "inst:" {
				argVals = append(argVals, a.Text())
				kinds = append(kinds, k)
				continue
			}
		}
		v, msg := saEvalI32(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		argVals = append(argVals, v)
		kinds = append(kinds, "")
	}
	v, msg := saCallbackValue(w, anode, argVals, true, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, false, kinds)
	if msg != "" {
		return "", msg
	}
	return v, ""
}

// saInlineMethod 内联 `obj.m(args)`（形参快照 + this 指向 + 槽汇合；
// 形状证据：封存 inlineClassMethod:10188-10280；体复用 saCallbackValue）。
func saInlineMethod(w printer.EmitTextWriter, recv string, def *saClassDef, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (string, string) {
	mn, ok := def.methods[method]
	if !ok {
		return "", "unknown method " + method
	}
	if mn.Body() == nil {
		if mn.Kind == ast.KindMethodSignature {
			return "", def.name + "." + method + " is an interface method with no body (interface dispatch is not lowerable)"
		}
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
				if k == "f64" {
					kinds[i] = "f64"
					continue
				}
				// 串形参（调用方串求值，体经串快照绑定；封存 inlineClassMethod
				// 串别名分支；上游同形）。
				if k == "str" {
					kinds[i] = "str"
					continue
				}
				if k != "i32" && k != "bool" {
					return "", "method parameters must be i32"
				}
				continue
			}
			if pd.Type.Kind == ast.KindTypeReference {
				if ref := pd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
					// `Record<string,T>` 形参记 map 种（值种播种与函数序同形）。
					if vkind, ok := saRecordValueKind(pd.Type, scope.classes, scope.aliasOf); ok {
						// JEV jvunion jsval_box 0.97, scope union_return_and_record 0.64:
						// Record<string, T> where T is a named alias resolving to a JSON
						// union takes a jsval param, not a map param (same rule as the
						// free-function param path; Record<string, unknown/any> keeps
						// map kind per map_with_jsval_values).
						if vkind == "jsval" && saRecordValueIsJSONUnion(pd.Type, scope.aliasOf) {
							kinds[i] = "jsval"
							continue
						}
						kinds[i] = "map"
						if nm := pd.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
							saSetMapVal(scope, nm.Text(), vkind)
						}
						continue
					}
					if _, ok := scope.classes[ref.TypeName.Text()]; ok {
						kinds[i] = "inst:" + ref.TypeName.Text()
						continue
					}
				}
			}
			// 可擦除注解按实参形绑定（泛型 `T`、any、联合等无布局名：实例实参继承布局，
			// 标量走快照；arr/str 句柄实参无布局绑定，沿旧门；封存 inlineClassMethod 按实参布局别名）。
			if pd.Type != nil && pd.Type.Kind == ast.KindUnionType {
				// 可空实例联合形参（`next: Node|null` 即 Node 布局，空吸收为 0
				// 句柄；与函数形参位 transpile.go:2647 同源；其余联合沿旧门）。
				if inst, ok := saUnionInstKind(pd.Type.AsUnionTypeNode(), scope.classes); ok {
					kinds[i] = inst
					continue
				}
			}
			if i < len(argNodes) && argNodes[i] != nil {
				if argNodes[i].Kind == ast.KindIdentifier {
					if k, ok := scope.types[argNodes[i].Text()]; ok {
						if len(k) > 5 && k[:5] == "inst:" {
							kinds[i] = k
							continue
						}
						if k == "arr" || k == "str" {
							return "", "method parameters must be i32"
						}
					}
				}
				kinds[i] = ""
				continue
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
			h, msg := saInstArg(w, a, kinds[i][5:], scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			argVals = append(argVals, h)
			continue
		}
		// f64 params evaluate as floats (snapshot copies float bits like scalars).
		if i < len(kinds) && kinds[i] == "f64" {
			v, msg := saEvalF64(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			argVals = append(argVals, v)
			continue
		}
		// map 形参实参直传句柄（`m` 须为已绑 map；与实例直传同形）。
		if i < len(kinds) && kinds[i] == "map" {
			if a != nil && a.Kind == ast.KindIdentifier {
				if k, ok := scope.types[a.Text()]; ok && k == "map" {
					argVals = append(argVals, a.Text())
					continue
				}
			}
			return "", "method map argument must be a bound map"
		}
		// 串形参实参串求值（`text: string`；绑定核快照串句柄；上游串别名同形）。
		if i < len(kinds) && kinds[i] == "str" {
			v, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			argVals = append(argVals, v)
			continue
		}
		// jsval 形参实参走自由函数同款 jsval 梯（JEV 2b_jsvalargs
		// reuse_freefn_paramkind 0.74, scope iface_method_only 0.96：
		// 方法签名种表已按 jvunion 规则把 Record<string, JSON-union>
		// 形参记为 jsval，但本循环此前无 jsval 臂，字面量实参落
		// saEvalI32 拒“object literal needs a declaration binding”，
		// 标识符实参拒“jsval params in i32 expression”；与自由函数
		// 调用核同律改走 saEvalCallArg 的 jsval 臂。非 jsval 种不受
		// 影响；无体接口方法此前已在 saInlineMethod 拒，沿旧门。）
		if i < len(kinds) && kinds[i] == "jsval" {
			sig := saFuncSig{params: len(kinds), paramKinds: kinds}
			v, msg := saEvalCallArg(w, sig, i, a, len(kinds), scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			argVals = append(argVals, v)
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
	// 返回种按声明注解（str 走串槽，f64 走双精度槽；余下走 i32 槽）。
	wantKind := "i32"
	if k, ok := saMethodReturnKind(mn); ok && k == "str" {
		wantKind = "str"
	} else if k, ok := saMethodReturnKind(mn); ok && k == "f64" {
		wantKind = "f64"
	} else if ic := saMethodInstReturn(mn, className, scope); ic != "" {
		wantKind = "inst:" + ic
	}
	v, msg := saCallbackValue(w, mn, argVals, true, wantKind, scope, pos, refusals, needImport, nextLabel, nextTemp, false, kinds)
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
	} else if k, ok := saMethodReturnKind(gn); ok && k == "f64" {
		wantKind = "f64"
	}
	v, msg := saCallbackValue(w, gn, nil, true, wantKind, scope, pos, refusals, needImport, nextLabel, nextTemp, false)
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
	} else if k, ok := saMethodReturnKind(gn); ok && k == "f64" {
		wantKind = "f64"
	}
	v, msg := saCallbackValue(w, gn, nil, true, wantKind, scope, pos, refusals, needImport, nextLabel, nextTemp, false)
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
	_, msg := saCallbackValue(w, sn, []string{val}, false, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, false)
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
	_, msg := saCallbackValue(w, sn, []string{val}, false, "i32", scope, pos, refusals, needImport, nextLabel, nextTemp, false)
	scope.thisSelf, scope.thisClass = savedSelf, savedClass
	return msg
}

// saLowerObjectAssign `Object.assign(target, ...sources)` 已知布局逐域拷贝（源序；
// 后源覆盖前源，同 JS 左到右覆盖语义；源为绑定标识符或对象字面量；
// 标量域 i32/f64 直拷；str/arr/inst 句柄域按对象展开同形 load as ptr +
// store as ptr 共享句柄（JEV 2b_assign assign_handle allow 0.74，置信 0.48，
// 与展开纪律一致）；jsval 域沿旧门大声拒（jsval 域赋值本身未 lowerable）；
// 未知布局/异布局/未知字面量键沿旧门；返回值即目标，JS 同义。形状证据：
// JEV extend_narg 1.0 / materialize_literal 0.90；字面量子集匹配
// saWantKeySubset + 缺省零填见 saLowerObjectLiteral；展开句柄拷贝同形见
// saLowerObjectLiteral spread 臂。
func saLowerObjectAssign(w printer.EmitTextWriter, tnode *ast.Node, sources []*ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if tnode == nil || tnode.Kind != ast.KindIdentifier {
		return "", "Object.assign needs bound objects"
	}
	tk, ok := scope.types[tnode.Text()]
	if !ok || len(tk) <= 5 || tk[:5] != "inst:" {
		return "", "Object.assign needs a known-layout target"
	}
	tdef, ok := scope.classes[tk[5:]]
	if !ok || tdef == nil {
		return "", "Object.assign needs a known-layout target"
	}
	for _, snode := range sources {
		if snode == nil {
			return "", "Object.assign needs bound objects"
		}
		if snode.Kind == ast.KindObjectLiteralExpression {
			if msg := saLowerObjectAssignLiteral(w, tnode.Text(), tdef, snode, scope, pos, refusals, nextTemp); msg != "" {
				return "", msg
			}
			continue
		}
		if snode.Kind != ast.KindIdentifier {
			return "", "Object.assign needs bound objects or object literals"
		}
		sk, ok := scope.types[snode.Text()]
		if !ok || len(sk) <= 5 || sk[:5] != "inst:" {
			return "", "Object.assign needs a known-layout source"
		}
		sdef, ok := scope.classes[sk[5:]]
		if !ok || sdef == nil {
			return "", "Object.assign needs a known-layout source"
		}
		if msg := saCopyInstFields(w, tnode.Text(), tdef, snode.Text(), sdef, nil, scope, nextTemp); msg != "" {
			return "", msg
		}
	}
	return tnode.Text(), ""
}

// saCopyInstFields 逐域拷贝（源域序；后写覆盖前写由调用方源序保证）。
// keyFilter 非空时仅拷贝所列键（字面量源：仅字面量自有键，零填缺省域不碰
// 目标）。i32/f64 经 load/store 助手；str/arr/inst 句柄按展开同形直拷 ptr；
// jsval 与其他种大声拒。
func saCopyInstFields(w printer.EmitTextWriter, tname string, tdef *saClassDef, sname string, sdef *saClassDef, keyFilter []string, scope *saScope, nextTemp *int) string {
	fields := sdef.fields
	if keyFilter != nil {
		fields = nil
		for _, k := range keyFilter {
			found := false
			for _, f := range sdef.fields {
				if f.name == k {
					fields = append(fields, f)
					found = true
					break
				}
			}
			if !found {
				return "Object.assign literal field " + k + " has no target field"
			}
		}
	}
	for _, f := range fields {
		sk := sdef.fkinds[f.name]
		if sk == "" {
			sk = "i32"
		}
		dk := tdef.fkinds[f.name]
		if dk == "" {
			dk = "i32"
		}
		if _, ok := tdef.offsets[f.name]; !ok || dk != sk {
			return "Object.assign needs matching field layouts"
		}
		kind := sk
		if kind == "jsval" {
			return "Object.assign cannot copy jsval fields (jsval field assignment is not lowerable yet)"
		}
		if kind != "i32" && kind != "f64" && kind != "str" && kind != "arr" && kind != "inst" {
			return "Object.assign cannot copy " + kind + " fields"
		}
		v, msg := saLowerClassFieldLoad(w, sname, sdef, f.name, scope, nextTemp)
		if msg != "" {
			return msg
		}
		if kind == "str" || kind == "arr" || kind == "inst" {
			// 展开同形：句柄直拷（saLowerClassFieldStore 对 inst 会落 i32，
			// 故此处直接按 ptr 存，与字面量展开臂一致）。
			w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", tname, tdef.offsets[f.name], v))
			continue
		}
		if msg := saLowerClassFieldStore(w, tname, tdef, f.name, v); msg != "" {
			return msg
		}
	}
	return ""
}

// saLowerObjectAssignLiteral 字面量源：先经 saLowerObjectLiteral 按目标布局
// 具化临时实例（want=目标布局；子集键已由 saWantKeySubset 接受，缺省可选域
// 零填），再仅按字面量自有键序拷入目标——零填域不得覆盖目标既有值。
// 字面量内含展开时拒（展开键无法从自有键集区分零填域）。
func saLowerObjectAssignLiteral(w printer.EmitTextWriter, tname string, tdef *saClassDef, lit *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) string {
	ol := lit.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil {
		return "Object.assign literal source is not lowerable"
	}
	var keys []string
	for _, p := range ol.Properties.Nodes {
		if p.Kind == ast.KindSpreadAssignment {
			return "Object.assign literal source with spread is not lowerable"
		}
		fname, ok := saObjPropName(p)
		if !ok {
			return "computed property names must be literals (dynamic keys have no static layout)"
		}
		keys = append(keys, fname)
	}
	h, lname, msg := saLowerObjectLiteral(w, lit, tdef.name, scope, pos, refusals, nextTemp)
	if msg != "" {
		return msg
	}
	ldef, ok := scope.classes[lname]
	if !ok || ldef == nil {
		return "Object.assign literal source has no recorded interface layout"
	}
	return saCopyInstFields(w, tname, tdef, h, ldef, keys, scope, nextTemp)
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
		scope.types[t] = "arr"
		return t, ""
	}
	if def.fkinds[field] == "inst" {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
		if sub, ok := def.fsub[field]; ok {
			scope.types[t] = "inst:" + sub
		}
		return t, ""
	}
	if def.fkinds[field] == "jsval" {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
		scope.types[t] = "jsval"
		return t, ""
	}
	if def.fkinds[field] == "f64" {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as f64\n", t, h, off))
		scope.types[t] = "f64"
		return t, ""
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", t, h, off))
	return t, ""
}

// saLowerGuardedFieldLoad lowering `b?.v`（inst 字段空守卫 join；2b_tail：
// 全字段种（i32/bool/str/arr/inst/jsval/f64），槽按种存取（指针种 as ptr，
// f64 as f64，i32/bool 沿旧 as ptr 存/as i32 取）；形状证据：satsgo
// lowerGuardedProperty:saemit.go + 本仓 saLowerOptionalIndex:sa_arr.go:231。
// 基经 saInstBase 仅 This/Identifier 即纯基（封存 isPureBase 同形）；
// 未知字段种（map/set/date 等）沿旧门大声拒，禁静默错种。
func saLowerGuardedFieldLoad(w printer.EmitTextWriter, h string, def *saClassDef, field string, scope *saScope, nextTemp *int) (string, string) {
	fk := def.fkinds[field]
	switch fk {
	case "", "i32", "bool", "str", "arr", "inst", "jsval", "f64":
	default:
		return "", "optional field access on non-i32 field is not lowerable"
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	isnull := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	nullL := fmt.Sprintf("L_prop_null_%d", *scope.nextLabel)
	*scope.nextLabel++
	okL := fmt.Sprintf("L_prop_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_prop_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isnull, h))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isnull, nullL, okL))
	w.Write(fmt.Sprintf("%s:\n", nullL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", okL))
	v, msg := saLowerClassFieldLoad(w, h, def, field, scope, nextTemp)
	if msg != "" {
		return "", msg
	}
	if fk == "f64" {
		w.Write(fmt.Sprintf("  store %s + 0, %s as f64\n", slot, v))
	} else {
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	}
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	switch fk {
	case "f64":
		w.Write(fmt.Sprintf("  %s = load %s + 0 as f64\n", dest, slot))
		scope.types[dest] = "f64"
	case "str":
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dest, slot))
		scope.types[dest] = "str"
	case "arr":
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dest, slot))
		scope.types[dest] = "arr"
	case "inst":
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dest, slot))
		if sub, ok := def.fsub[field]; ok {
			scope.types[dest] = "inst:" + sub
		}
	case "jsval":
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", dest, slot))
		scope.types[dest] = "jsval"
	default:
		w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	}
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest, ""
}

// saLowerGuardedMethodCall lowering `b.m?.()`/`b?.m()`（inst i32 方法空守卫
// join；空基即 0，否则既有方法内联直调。封存 lowerGuardedCall:4108 槽形）。
func saLowerGuardedMethodCall(w printer.EmitTextWriter, h string, def *saClassDef, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	isnull := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	nullL := fmt.Sprintf("L_call_null_%d", *scope.nextLabel)
	*scope.nextLabel++
	okL := fmt.Sprintf("L_call_ok_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_call_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isnull, h))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isnull, nullL, okL))
	w.Write(fmt.Sprintf("%s:\n", nullL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", okL))
	v, msg := saInlineMethod(w, h, def, method, ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
	if msg != "" {
		return "", msg
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return dest, ""
}

// saChainBase 解对象链基（`q.p`→内层句柄；递归支持多层；
// 叶子由调用方按表读/存；非 inst 链节一律失败，调用方沿旧门）。
// saChainLeafDef 纯判定链式成员叶布局（`o.inner.v` 经 inst 域逐级下钻；
// 不落字（谓词位无发射），发射见 saChainBase；根基限 this/实例绑定/类名
// （与 saChainBase 前三臂同形；super/调用/元素基沿旧门由发射位拒）。
func saChainLeafDef(e *ast.Node, scope *saScope) (*saClassDef, string, bool) {
	pa := e.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return nil, "", false
	}
	if pa.Expression == nil || pa.Expression.Kind != ast.KindPropertyAccessExpression {
		return nil, "", false
	}
	var segs []string
	cur := e
	for cur != nil && cur.Kind == ast.KindPropertyAccessExpression {
		cpa := cur.AsPropertyAccessExpression()
		if cpa.Name() == nil || cpa.Name().Kind != ast.KindIdentifier {
			return nil, "", false
		}
		segs = append([]string{cpa.Name().Text()}, segs...)
		cur = cpa.Expression
	}
	var def *saClassDef
	switch {
	case cur != nil && cur.Kind == ast.KindThisKeyword:
		if scope.thisSelf == "" {
			return nil, "", false
		}
		d, ok := scope.classes[scope.thisClass]
		if !ok {
			return nil, "", false
		}
		def = d
	case cur != nil && cur.Kind == ast.KindIdentifier:
		nm := cur.Text()
		if k, ok := scope.types[nm]; ok && len(k) > 5 && k[:5] == "inst:" {
			d, ok := scope.classes[k[5:]]
			if !ok {
				return nil, "", false
			}
			def = d
		} else if d, ok := scope.classes[nm]; ok {
			if _, shadowed := scope.types[nm]; shadowed {
				return nil, "", false
			}
			if _, shadowed := scope.funcs[nm]; shadowed {
				return nil, "", false
			}
			def = d
		} else {
			return nil, "", false
		}
	default:
		return nil, "", false
	}
	for _, s := range segs[:len(segs)-1] {
		if _, ok := def.offsets[s]; !ok || def.fkinds[s] != "inst" {
			return nil, "", false
		}
		sub, ok := scope.classes[def.fsub[s]]
		if !ok {
			return nil, "", false
		}
		def = sub
	}
	leaf := segs[len(segs)-1]
	if _, ok := def.offsets[leaf]; !ok {
		return nil, "", false
	}
	return def, leaf, true
}

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
		if k, ok := scope.types[pa.Expression.Text()]; ok {
			if len(k) <= 5 || k[:5] != "inst:" {
				return "", nil, "chained base is not a bound instance"
			}
			d, ok := scope.classes[k[5:]]
			if !ok {
				return "", nil, "unknown class " + k[5:]
			}
			h, def = pa.Expression.Text(), d
		} else if _, ok := scope.funcs[pa.Expression.Text()]; ok {
			return "", nil, "chained base is not a bound instance"
		} else if d, ok := scope.classes[pa.Expression.Text()]; ok {
			// 类名即基址（`C.p.a` 经类布局逐级解；形状证据：封存 lowerMemberChain:8243-8308
			// segs 收集中缀 + layoutOfNode 解析基布局 + lowerExpr 基回裸名:2819
			// `return n.Text()` + `load base + off as saname` + ftypes 下沉嵌套）。
			h, def = pa.Expression.Text(), d
		} else if to, ok := saTopObjLookup(scope, pa.Expression); ok {
			// 顶层 const 对象根（JEV chain materialize_sub）：快照按需物化，
			// 既有尾部续读嵌套域句柄；深链经 PropertyAccess 递归自达。
			// 无初值种子沿旧门大声拒，禁空初值入物化。
			d, ok := scope.classes[to.layout]
			if !ok || d == nil {
				return "", nil, "unknown class " + to.layout
			}
			if to.init == nil {
				return "", nil, "top object has no recorded initializer"
			}
			// Pre-check the chain step before emitting: a failed probe must
			// not leak a dead snapshot into a fall-through arm (NEST.a.length
			// went 20 -> 36 instr without this). Mirrors the tail checks.
			if _, ok := d.offsets[leaf]; !ok {
				return "", nil, "unknown field " + leaf
			}
			if d.fkinds[leaf] != "inst" {
				return "", nil, "chained field " + leaf + " is not a nested object"
			}
			if _, ok := scope.classes[d.fsub[leaf]]; !ok {
				return "", nil, "nested field " + leaf + " has no recorded sub layout"
			}
			h2, _, msg := saLowerTopObjClaim(w, to, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", nil, msg
			}
			h, def = h2, d
		} else {
			return "", nil, "chained base is not a bound instance"
		}
	case pa.Expression != nil && pa.Expression.Kind == ast.KindPropertyAccessExpression:
		ih, idef, msg := saChainBase(w, pa.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", nil, msg
		}
		h, def = ih, idef
	case pa.Expression != nil && pa.Expression.Kind == ast.KindSuperKeyword:
		// super 链基与同接收者共享展平偏移（形状证据：封存 lowerMemberChain:8254-8260
		// super 锚定查后改写 thisSelf + checkSuperAccess；写侧同接收者见 lowerFieldStore:8325-8330）。
		bdef, hh, msg := saSuperBase(scope)
		if msg != "" {
			return "", nil, msg
		}
		h, def = hh, bdef
	case pa.Expression != nil && (pa.Expression.Kind == ast.KindElementAccessExpression || pa.Expression.Kind == ast.KindCallExpression || pa.Expression.Kind == ast.KindNewExpression):
		// Index/call/new base (JEV eval_chain 0.97): resolve the base
		// instance through the existing element/call/new evaluator,
		// which materializes top arrays and releases the array handle
		// after element extraction, so probing this arm never leaks a
		// materialized receiver into a fall-through arm.
		eh, edef, msg := saInstBaseElem(w, pa.Expression, scope, pos, refusals, nextTemp)
		if msg != "" || edef == nil {
			if msg == "" {
				msg = "chained base did not resolve to a recorded layout"
			}
			return "", nil, msg
		}
		h, def = eh, edef
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
	if pa.QuestionDotToken != nil {
		// `o?.inner`：空守卫（null → 空句柄 0），否则读嵌套对象句柄；直读会
		// 在 null 基上读零址 SIGSEGV（禁静默错码：TS 语义是整链短路）。
		slot := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
		isnull := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		nullL := fmt.Sprintf("L_chain_null_%d", *scope.nextLabel)
		*scope.nextLabel++
		okL := fmt.Sprintf("L_chain_ok_%d", *scope.nextLabel)
		*scope.nextLabel++
		endL := fmt.Sprintf("L_chain_end_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", isnull, h))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", isnull, nullL, okL))
		w.Write(fmt.Sprintf("%s:\n", nullL))
		w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", okL))
		v := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", v, h, off))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
		w.Write(fmt.Sprintf("%s:\n", endL))
		w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", t, slot))
		w.Write(fmt.Sprintf("  !%s\n", slot))
	} else {
		w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", t, h, off))
	}
	sub, ok := scope.classes[def.fsub[leaf]]
	if !ok {
		return "", nil, "nested field " + leaf + " has no recorded sub layout"
	}
	return t, sub, ""
}

// saLowerFieldIncDec 实例 i32 域自增 `o.f++`/`this.f--`（读-改-写回，前缀返新值、
// 后缀返旧值，与模块槽自增同序；基限 this/实例绑定；非 i32 域与存取器大声拒）。
func saLowerFieldIncDec(w printer.EmitTextWriter, operand *ast.Node, up, prefix bool, scope *saScope, nextTemp *int) (string, string, bool) {
	if operand == nil || operand.Kind != ast.KindPropertyAccessExpression {
		return "", "", false
	}
	pa := operand.AsPropertyAccessExpression()
	if pa.QuestionDotToken != nil || pa.Name() == nil || !saCouldBeInst(pa.Expression, scope) {
		return "", "", false
	}
	h, def, msg := saInstBase(pa.Expression, scope)
	if msg != "" || def == nil {
		return "", "", false
	}
	field, msg := saPrivResolve(def, pa.Name().Text(), scope.thisClass)
	if msg != "" {
		return "", msg, true
	}
	if _, ok := def.offsets[field]; !ok {
		return "", "", false
	}
	// f64 域自增（读-改-写回；`fadd/fsub 1.0`；前缀返新值、后缀返旧值
	// 与 i32 臂同序；新值记 f64 种供值位）。
	if def.fkinds[field] == "f64" {
		cur, msg := saLowerClassFieldLoad(w, h, def, field, scope, nextTemp)
		if msg != "" {
			return "", msg, true
		}
		fop := "fadd"
		if !up {
			fop = "fsub"
		}
		nw := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, 1.0\n", nw, fop, cur))
		if msg := saLowerClassFieldStore(w, h, def, field, nw); msg != "" {
			return "", msg, true
		}
		scope.types[nw] = "f64"
		if prefix {
			return nw, "", true
		}
		return cur, "", true
	}
	if k := def.fkinds[field]; k != "" && k != "i32" {
		return "", "++/-- on non-i32 field " + field + " is not lowerable", true
	}
	cur, msg := saLowerClassFieldLoad(w, h, def, field, scope, nextTemp)
	if msg != "" {
		return "", msg, true
	}
	op := "add"
	if !up {
		op = "sub"
	}
	nw := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s %s, 1\n", nw, op, cur))
	if msg := saLowerClassFieldStore(w, h, def, field, nw); msg != "" {
		return "", msg, true
	}
	if prefix {
		return nw, "", true
	}
	return cur, "", true
}

// saLowerClassFieldStore 写 `o.f = v`（偏移 store；i32 存值，str/arr 存句柄，f64 存双精度）。
func saLowerClassFieldStore(w printer.EmitTextWriter, h string, def *saClassDef, field, v string) string {
	off, ok := def.offsets[field]
	if !ok {
		return "unknown field " + field
	}
	if def.fkinds[field] == "str" || def.fkinds[field] == "arr" {
		w.Write(fmt.Sprintf("  store %s + %d, %s as ptr\n", h, off, v))
		return ""
	}
	if def.fkinds[field] == "f64" {
		w.Write(fmt.Sprintf("  store %s + %d, %s as f64\n", h, off, v))
		return ""
	}
	if def.fkinds[field] == "jsval" {
		return "jsval field assignment is not lowerable yet"
	}
	w.Write(fmt.Sprintf("  store %s + %d, %s as i32\n", h, off, v))
	return ""
}
