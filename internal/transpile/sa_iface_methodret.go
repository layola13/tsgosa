package transpile

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// Phase M interface-method return provenance (branch
// tsgosa/parallel/iface_methodret, JEV methodret_v1_shapes
// call_result_only, methodret_bind_shapes inferred_only,
// methodret_storage table_classdef, methodret_loop
// after_dispatch_only, methodret_file_scope same_file_only,
// methodret_async_scope sync_only, methodret_sci_split
// emitter_only, methodret_litfn class_only).
//
// Problem: R0's return analysis visits top-level functions, namespace
// members, and top-level arrows only — class declarations are never
// visited, and methods have no signatures at all (they live only as AST
// nodes in saClassDef.methods and lower only by inlining at call
// sites). The call-result arms cannot consume method-call results
// either (saCallRetKind is bare-identifier-only, and
// saAttachIfaceRetProv requires a bare-identifier callee). This file
// adds a same-file, sync, emitter-only method analysis: for each
// interface-typed method of a file-defined class, if EVERY return path
// provably yields the same concrete class via free-function call
// results (direct or local-mediated through entries), that provenance
// is recorded on the class definition (methodProv, next to fieldProv).
// The inferred-decl arm then attaches it to const method-call-result
// bindings, and the existing dispatch site handles the call unchanged.
//
// Fail-closed throughout: this analysis ADDS NO REFUSALS. Anything it
// cannot prove (param/field/method-call/litFn origins, mixed paths,
// async methods, generic methods, cross-file classes, getters,
// shadowed or reassigned bindings, unwhitelisted statements) leaves the
// table untouched, so the program refuses exactly as before.
//
// Phase C (branch tsgosa/parallel/iface_classifier) extends the
// classifier with new/this/identifier origins: a NewExpression init
// yields a new entry (verbatim R0's new arm); a NewExpression return
// yields the class on concrete + (declared-or-structural) satisfies +
// methodOnly guard; a ThisKeyword return yields the ANALYZING class
// (JEV clf_this analyzing_class, moderate — see the discriminator
// minimal m_this_inherit_override) for instance methods only, with the
// same guard; and an identifier return through a new entry lifts the
// deliberate isNew rejection under the same guard (R0's widened-handle
// rule shape with alias soundness: the ladder passes the true handle,
// M0 probe §2.3a). The isLit rejection stays. Placement stays
// one-shot after the joint loop; agreement stays R0's rule
// (all paths same class, mixed → nothing).
//
// v1 cuts, binding on the build (JEV weak adoptions flagged, not
// hidden): annotated method-call binds are REQUIRED NEGATIVES (M1
// stands; the method-call kind function is NOT built — a trivially
// safe annotated arm in build testing is a re-ask, not a silent
// widening); m_call2method flipping means the build smuggled in a
// classifier method-call arm and must stop; one-shot placement only
// (after_dispatch_only plurality) — method prov feeds nothing back,
// so no loop iteration for methods.
//
// The method pass writes ONLY methodProv. It never mutates the joint
// loop's outputs (funcs prov fields, fieldProv): it runs one-shot
// AFTER saAnalyzeIfaceJointProv, consuming final joint-loop retProv
// (a method returning a joint-derived call result resolves in one
// pass). Method->method chains need the classifier method-call arm,
// which does not exist in v1, so re-running cannot resolve new
// methods — there is no fixpoint to iterate.
//
// Inheritance note (correcting the design's "defining class only"
// shorthand): class recording COPIES inherited methods into child defs
// (sa_class.go inherit block: parent methods copied unless the child
// overrides), so a child def's methods map holds the EFFECTIVE method
// for that class — own override or shared inherited node. Analyzing
// each file-defined class's own maps therefore records prov for the
// effective method on each class; inherited nodes are shared, so
// parent and child entries always agree (same node, same analysis).
// The attach walk finds the first def in the receiver's parent chain
// holding the method node, which is exactly the effective method.

// saMethodDeclaredIface returns the declared interface return kind of a
// method node ("inst:"+name), or "" when the method does not declare a
// plain (non-generic) interface return. Concrete returns, `this`
// returns, unions, generics, and missing annotations all yield "".
func saMethodDeclaredIface(mn *ast.Node, classes map[string]*saClassDef) string {
	if mn == nil || mn.Kind != ast.KindMethodDeclaration {
		return ""
	}
	md := mn.AsMethodDeclaration()
	if md == nil || md.Type == nil {
		return ""
	}
	typ := md.Type
	if typ.Kind != ast.KindTypeReference {
		return ""
	}
	ref := typ.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil {
		return ""
	}
	d, ok := classes[ref.TypeName.Text()]
	if !ok || d == nil || !d.isIface {
		return ""
	}
	return "inst:" + d.name
}

// saAnalyzeMethodIfaceProv records methodProv on same-file class
// definitions whose interface-typed methods provably return one
// concrete class via free-function call results. It mutates only
// saClassDef.methodProv and adds no refusals.
func saAnalyzeMethodIfaceProv(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) {
	if sf == nil || funcs == nil || classes == nil {
		return
	}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st == nil || st.Kind != ast.KindClassDeclaration {
			continue
		}
		nm := st.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		name := nm.Text()
		def, ok := classes[name]
		if !ok || def == nil || def.isIface || def.isAbstract || def.isSynth || def.isMono {
			continue
		}
		// Per-class table with collision detection: a static/instance
		// name collision with different prov records nothing.
		prov := map[string]string{}
		collided := map[string]bool{}
		record := func(mname, class string) {
			if collided[mname] {
				return
			}
			if prev, ok := prov[mname]; ok {
				if prev != class {
					delete(prov, mname)
					collided[mname] = true
				}
				return
			}
			prov[mname] = class
		}
		for _, src := range []struct {
			methods  map[string]*ast.Node
			isStatic bool
		}{{def.methods, false}, {def.staticMethods, true}} {
			for mname, mn := range src.methods {
				if mn == nil || strings.HasPrefix(mname, "#") {
					continue
				}
				if mn.Body() == nil {
					continue
				}
				// Getters live in separate maps and are never
				// iterated here (methods_only); async methods are
				// excluded (sync_only); generic methods are
				// deferred (own analysis).
				if saParamFnAsync(mn) {
					continue
				}
				md := mn.AsMethodDeclaration()
				if md == nil {
					continue
				}
				if md.TypeParameters != nil && len(md.TypeParameters.Nodes) > 0 {
					continue
				}
				dk := saMethodDeclaredIface(mn, classes)
				if dk == "" {
					continue
				}
				var paramNodes []*ast.Node
				if md.Parameters != nil {
					paramNodes = md.Parameters.Nodes
				}
				params, paramsOK := saRetParamNames(paramNodes)
				if !paramsOK {
					continue
				}
				body := mn.Body()
				if body == nil || body.Kind != ast.KindBlock {
					continue
				}
				class, ok := saAnalyzeMethodReturns(body, params, dk, funcs, classes, aliasOf, name, src.isStatic)
				if !ok {
					continue
				}
				record(mname, class)
			}
		}
		if len(prov) > 0 {
			if def.methodProv == nil {
				def.methodProv = map[string]string{}
			}
			for mname, class := range prov {
				def.methodProv[mname] = class
			}
		}
	}
}

// saAnalyzeMethodReturns classifies every return path of one method
// body with the restricted classifier (free-function call results
// plus, in Phase C, new/this/identifier origins, class only). It
// mirrors saAnalyzeFuncReturns' discipline — Pass 1 taint, Pass 2
// ordered walk (depth-0 const bindings + return paths), all-paths
// agreement, trailing-return rule — but with the method-mode walker,
// no param seeding (param_origin deferred), no field-read
// classification (field_returns deferred), and no litFn
// (class_only). className/isStatic are the ANALYZING class (the class
// whose map the entry is recorded on — per-class re-analysis, so an
// inherited node's entry for a child class is the child class) and
// whether the method is static (the this arm is instance-only).
// Returns the agreed concrete class, or ok=false.
func saAnalyzeMethodReturns(body *ast.Node, paramNames []string, retKind string, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, className string, isStatic bool) (string, bool) {
	retIface := retKind[5:]
	if body == nil || body.Kind != ast.KindBlock {
		return "", false
	}
	bd := body.AsBlock()
	if bd == nil || bd.Statements == nil {
		return "", false
	}
	stmts := bd.Statements.Nodes
	// Pass 1: taint. Parameters count as declared once, so a body const
	// sharing a parameter name is tainted (same rule as R0).
	t := &saRetTaint{counts: map[string]int{}, assigned: map[string]bool{}}
	for _, p := range paramNames {
		t.counts[p]++
	}
	saRetTaintStmts(stmts, t)
	if t.unknown {
		return "", false
	}
	// Pass 2: ordered walk, depth-0 const bindings + all return paths,
	// with the restricted method classifier.
	w := &saRetWalker{
		taint:        t,
		entries:      map[string]saIfaceRetEntry{},
		ok:           true,
		funcs:        funcs,
		classes:      classes,
		aliasOf:      aliasOf,
		retKind:      retKind,
		retIface:     retIface,
		methodMode:   true,
		methodClass:  className,
		methodStatic: isStatic,
	}
	w.walkList(stmts, 0, nil)
	if !w.ok || len(w.paths) == 0 {
		return "", false
	}
	for _, p := range w.paths {
		if p.unknown {
			return "", false
		}
	}
	first := w.paths[0]
	for _, p := range w.paths[1:] {
		if p.class != first.class {
			return "", false
		}
	}
	// Trailing-return rule (mirroring R0): the body block's last
	// top-level statement must be a return or a throw.
	if len(stmts) == 0 {
		return "", false
	}
	last := stmts[len(stmts)-1]
	if last == nil || (last.Kind != ast.KindReturnStatement && last.Kind != ast.KindThrowStatement) {
		return "", false
	}
	return first.class, true
}

// saMethodClassifyInit classifies a depth-0 const binding initializer
// into an entry, or reports no entry (unknown provenance, never an
// error). Free-function call results (bare-identifier callee with
// recorded class prov) and identifier copies of entries classify; in
// Phase C a NewExpression init also classifies (verbatim R0's new
// arm: annotation discarded, kind concrete, ctor args ignored — prov
// is about the class, not the construction). Field reads, method
// calls, object literals, and everything else yield no entry
// (field/method-call/litFn origins are all deferred in v1).
func saMethodClassifyInit(init *ast.Node, annot *ast.TypeNode, entries map[string]saIfaceRetEntry, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (saIfaceRetEntry, bool) {
	if init == nil {
		return saIfaceRetEntry{}, false
	}
	init = saRetPeelParenNonNull(init)
	if init == nil {
		return saIfaceRetEntry{}, false
	}
	annotName := ""
	if annot != nil {
		annotName = saRetAnnotName(annot, aliasOf)
	}
	if init.Kind == ast.KindIdentifier {
		src, ok := entries[init.Text()]
		if !ok {
			return saIfaceRetEntry{}, false
		}
		out := src
		if annotName != "" {
			out.kind = "inst:" + annotName
		}
		return out, true
	}
	if init.Kind == ast.KindNewExpression {
		ne := init.AsNewExpression()
		if ne == nil || ne.Expression == nil || ne.Expression.Kind != ast.KindIdentifier {
			return saIfaceRetEntry{}, false
		}
		cname := ne.Expression.Text()
		def, ok := classes[cname]
		if !ok || def == nil || def.isIface || def.isAbstract {
			return saIfaceRetEntry{}, false
		}
		// The new arm discards the annotation (binding follows the
		// initial value kind), so the entry kind is always concrete.
		return saIfaceRetEntry{class: cname, kind: "inst:" + cname, isNew: true}, true
	}
	if init.Kind == ast.KindCallExpression {
		ce := init.AsCallExpression()
		if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
			return saIfaceRetEntry{}, false
		}
		sig, ok := funcs[ce.Expression.Text()]
		if !ok || sig.isVoid || sig.retIsPromise {
			return saIfaceRetEntry{}, false
		}
		if sig.retIfaceProv != "" {
			return saIfaceRetEntry{class: sig.retIfaceProv, kind: annotKindOrEmpty(annotName)}, true
		}
		return saIfaceRetEntry{}, false
	}
	return saIfaceRetEntry{}, false
}

// saMethodClassifyReturn classifies one return expression into a path.
// Free-function call results (bare-identifier callee with recorded
// class prov and matching return kind) and identifier returns through
// entries (kind == return kind) classify; in Phase C, NewExpression
// returns (concrete + satisfies + methodOnly guard), ThisKeyword
// returns (analyzing class, instance methods only, same guard), and
// identifier returns through new entries (guarded isNew lift — R0's
// widened-handle rule shape with alias soundness, M0 probe §2.3a)
// also classify. Param, field, method-call, and litFn origins all
// yield unknown (each deferred in v1); the isLit rejection stays.
func saMethodClassifyReturn(expr *ast.Node, entries map[string]saIfaceRetEntry, shadowed map[string]bool, retKind, retIface string, funcs map[string]saFuncSig, classes map[string]*saClassDef, analyzingClass string, isStatic bool) saIfaceRetPath {
	unknown := saIfaceRetPath{unknown: true}
	if expr == nil {
		return unknown
	}
	expr = saRetPeelParenNonNull(expr)
	if expr == nil {
		return unknown
	}
	switch expr.Kind {
	case ast.KindIdentifier:
		name := expr.Text()
		if shadowed != nil && shadowed[name] {
			return unknown
		}
		e, ok := entries[name]
		if !ok {
			return unknown
		}
		// The isLit rejection stays: a literal path must never slip
		// through even if one did (class_only).
		if e.isLit {
			return unknown
		}
		if e.kind == retKind && e.class != "" {
			return saIfaceRetPath{class: e.class}
		}
		// Guarded isNew lift (Phase C): a direct new binding of a
		// concrete class into a method-only satisfied interface.
		// Alias soundness: the ladder passes the true handle, and
		// method-only wants have no interface fields to misread.
		if e.isNew && e.kind == "inst:"+e.class && e.class != "" &&
			saIfaceMethodOnly(classes, retIface) &&
			(saClassSatisfies(classes, e.class, retIface) || saClassStructurallySatisfies(classes, e.class, retIface)) {
			return saIfaceRetPath{class: e.class}
		}
		return unknown
	case ast.KindNewExpression:
		ne := expr.AsNewExpression()
		if ne == nil || ne.Expression == nil || ne.Expression.Kind != ast.KindIdentifier {
			return unknown
		}
		cname := ne.Expression.Text()
		def, ok := classes[cname]
		if !ok || def == nil || def.isIface || def.isAbstract {
			return unknown
		}
		if !saIfaceMethodOnly(classes, retIface) {
			return unknown
		}
		if !saClassSatisfies(classes, cname, retIface) && !saClassStructurallySatisfies(classes, cname, retIface) {
			return unknown
		}
		return saIfaceRetPath{class: cname}
	case ast.KindThisKeyword:
		// Instance methods only: static methods have no `this` at
		// lowering, and the ladder this arm requires thisSelf != "".
		if isStatic {
			return unknown
		}
		def, ok := classes[analyzingClass]
		if !ok || def == nil || def.isIface || def.isAbstract {
			return unknown
		}
		if !saIfaceMethodOnly(classes, retIface) {
			return unknown
		}
		if !saClassSatisfies(classes, analyzingClass, retIface) && !saClassStructurallySatisfies(classes, analyzingClass, retIface) {
			return unknown
		}
		return saIfaceRetPath{class: analyzingClass}
	case ast.KindCallExpression:
		ce := expr.AsCallExpression()
		if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
			return unknown
		}
		sig, ok := funcs[ce.Expression.Text()]
		if !ok || sig.isVoid || sig.retIsPromise {
			return unknown
		}
		// The call-return arm passes the handle directly only on
		// layout match (same interface); a different interface would
		// take the widen-with-join path and drop provenance.
		if sig.retKind != retKind {
			return unknown
		}
		if sig.retIfaceProv != "" {
			return saIfaceRetPath{class: sig.retIfaceProv}
		}
		return unknown
	}
	return unknown
}

// saAttachIfaceMethodProv attaches a method's recorded return
// provenance to a const method-call-result binding. It is a no-op
// unless every gate holds: const binding, PropertyAccess callee with
// an identifier method name (never `#` private names), a resolvable
// receiver class (identifier with a concrete inst kind, `this` via
// scope.thisClass, or an unshadowed class name for static calls), an
// effective method found by walking the receiver's parent chain
// (override shadows: the first def holding the method node wins, and
// a missing table entry there stops the walk), a recorded methodProv
// entry, and a declared interface return kind matching the binding's
// kind. The existing dispatch site then handles method calls on the
// binding unchanged. Prov only, no ownership change.
func saAttachIfaceMethodProv(scope *saScope, ce *ast.CallExpression, name, vkind string, isConst bool) {
	if !isConst || ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return
	}
	mname := pa.Name().Text()
	if strings.HasPrefix(mname, "#") {
		return
	}
	recv := saRetPeelParenNonNull(pa.Expression)
	if recv == nil {
		return
	}
	var startClass string
	var staticRecv bool
	switch {
	case recv.Kind == ast.KindThisKeyword:
		if scope.thisClass == "" {
			return
		}
		startClass = scope.thisClass
	case recv.Kind == ast.KindIdentifier:
		rn := recv.Text()
		if def, ok := scope.classes[rn]; ok && def != nil {
			if _, shadowed := scope.types[rn]; !shadowed {
				if _, shadowed := scope.funcs[rn]; !shadowed {
					startClass = rn
					staticRecv = true
				}
			}
		}
		if startClass == "" {
			k, ok := scope.types[rn]
			if !ok || len(k) <= 5 || k[:5] != "inst:" {
				return
			}
			d, ok := scope.classes[k[5:]]
			if !ok || d == nil || d.isIface || d.isAbstract {
				return
			}
			startClass = k[5:]
		}
	default:
		return
	}
	visited := map[string]bool{}
	for cls := startClass; cls != ""; {
		if visited[cls] {
			return
		}
		visited[cls] = true
		def, ok := scope.classes[cls]
		if !ok || def == nil {
			return
		}
		var mn *ast.Node
		if staticRecv {
			mn, ok = def.staticMethods[mname]
		} else {
			mn, ok = def.methods[mname]
		}
		if ok && mn != nil {
			prov, ok := def.methodProv[mname]
			if !ok || prov == "" {
				return
			}
			if saMethodDeclaredIface(mn, scope.classes) != vkind {
				return
			}
			saSetIfaceProv(scope, name, prov)
			return
		}
		cls = def.parent
	}
}
