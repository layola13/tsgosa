package transpile

import (
	"sort"
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
// widening). Phase MC adds the method-call arm, but m_call2method
// flipping still means the build smuggled a param-receiver arm and
// must stop (mc_param_recv excludes params); one-shot placement is
// replaced by the Phase MC fixpoint (after_joint placement stays —
// the fixpoint wraps the method pass, it does not move it into the
// joint loop).
//
// Phase MC (branch tsgosa/parallel/iface_methodcall) adds the
// classifier method-call arm: a method-call return/init resolves
// through the inner method's recorded methodProv (this/new/
// identifier receivers; Call receivers deferred as chains; param
// receivers excluded — a declared-type receiver is unsound under
// subclass override, JEV mc_param_recv). Record iff inner prov
// exists AND inner declared iface == outer retIface AND the outer
// iface is method-only (method-call handles are true handles via the
// ladder's alias discipline, M0 probe §2.2). The method pass runs a
// fixpoint (JEV mc_depth, WEAK near-tie — guard G-depth: m_chain_method
// MUST flip, no silent single_level fallback): each round analyzes
// only methods NOT yet recorded (recorded entries are final, never
// revisited), bound 1 + #methods, per-round merge visibility.
// m_call2method STAYS the control: its receiver is a param, excluded
// by mc_param_recv, so it keeps the exact 31:9 interface message;
// m_chain_return stays refused (param receiver AND R0 placement,
// doubly deferred).
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

// saHarvestClassDef resolves a class name through link.classHarvests:
// first the anchor def's defining file (pointer-search, exactly-one-file
// rule, mirroring saInlineFindDefFile), then program-wide exactly-one
// file defining the name (mirroring saInlineResolveParentDef's by-name
// search). Both steps require exported=true on the harvest entry.
// Plain names only: a name containing "." never resolves, so
// namespace-member defs (dotted harvest keys) stay out of v1.
// Returns nil when ambiguous, absent, non-exported, or dotted.
// (dispatch-defs slice, JEV defining_first_unique_fallback +
// exported_only; the shared harvest-lookup helper for the attach and
// dispatch fallback sites — it changes no existing call site.)
func saHarvestClassDef(scope *saScope, name string, anchor *saClassDef) *saClassDef {
	if scope == nil || scope.link == nil || name == "" || strings.Contains(name, ".") {
		return nil
	}
	harvests := scope.link.classHarvests
	if len(harvests) == 0 {
		return nil
	}
	// Step 1: the anchor def's defining file.
	if anchor != nil {
		if file, ok := saInlineFindDefFile(scope.link, anchor); ok {
			if pc, ok := harvests[file][name]; ok && pc.def != nil && pc.exported {
				return pc.def
			}
		}
	}
	// Step 2: program-wide exactly-one file defining the name.
	files := make([]string, 0, len(harvests))
	for f := range harvests {
		files = append(files, f)
	}
	sort.Strings(files)
	var found *saClassDef
	for _, f := range files {
		if pc, ok := harvests[f][name]; ok && pc.def != nil && pc.exported {
			if found != nil {
				return nil
			}
			found = pc.def
		}
	}
	return found
}

// saScopeOrHarvestClassDef resolves a class name caller-scope-first,
// then via saHarvestClassDef (anchor file, then program-wide unique,
// exported-only). Caller-scope defs are returned as-is (imported or
// same-file defs already passed their own visibility gates); only a
// missing caller-scope def consults harvests.
func saScopeOrHarvestClassDef(scope *saScope, name string, anchor *saClassDef) *saClassDef {
	if scope == nil {
		return nil
	}
	if d, ok := scope.classes[name]; ok && d != nil {
		return d
	}
	return saHarvestClassDef(scope, name, anchor)
}

// saAttachIfaceDeclaredHarvest checks a method node's declared
// interface return against link.classHarvests: the walk-def's defining
// file first, then program-wide exactly-one file, both requiring
// exported=true. It mirrors saMethodDeclaredIface's extraction (plain
// TypeReference identifier, no type arguments) and kind comparison,
// and is consulted ONLY after the caller-scope check fails.
func saAttachIfaceDeclaredHarvest(scope *saScope, walkDef *saClassDef, mn *ast.Node, vkind string) bool {
	if mn == nil || mn.Kind != ast.KindMethodDeclaration {
		return false
	}
	md := mn.AsMethodDeclaration()
	if md == nil || md.Type == nil || md.Type.Kind != ast.KindTypeReference {
		return false
	}
	ref := md.Type.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier || ref.TypeArguments != nil {
		return false
	}
	d := saHarvestClassDef(scope, ref.TypeName.Text(), walkDef)
	if d == nil || !d.isIface {
		return false
	}
	return "inst:"+d.name == vkind
}

// saDispatchIfaceFallback resolves the interface def for a dispatch
// whose saInstBase call failed with "unknown class <Iface>". It fires
// ONLY when the (paren-peeled) receiver is a bare identifier, the
// binding's kind is inst:<Iface> matching the failure message, and a
// prov entry exists for the receiver (the prov_required bound —
// without prov, harvestability alone never dispatches). The interface
// def resolves caller-scope-first (vacuous here, since saInstBase
// already failed, kept for symmetry), then via saHarvestClassDef
// anchored on the caller-scope prov def when visible, else
// program-wide unique; harvest entries must be exported. The returned
// handle is the receiver binding name, exactly what saInstBase's
// identifier arm returns on success. The def must be an interface;
// the branch's own signature-presence check still applies downstream.
// Otherwise the original refusal stands unchanged.
func saDispatchIfaceFallback(scope *saScope, recv *ast.Node, msg string) (string, *saClassDef, bool) {
	if scope == nil || recv == nil {
		return "", nil, false
	}
	// Peel parens exactly like saInstBase.
	for recv != nil && recv.Kind == ast.KindParenthesizedExpression {
		pe := recv.AsParenthesizedExpression()
		if pe == nil || pe.Expression == nil {
			break
		}
		recv = pe.Expression.AsNode()
	}
	if recv == nil || recv.Kind != ast.KindIdentifier {
		return "", nil, false
	}
	recvName := recv.Text()
	prov, ok := scope.ifaceProv[recvName]
	if !ok || prov == "" {
		return "", nil, false
	}
	k, ok := scope.types[recvName]
	if !ok || len(k) <= 5 || k[:5] != "inst:" {
		return "", nil, false
	}
	ifaceName := k[5:]
	if msg != "unknown class "+ifaceName {
		return "", nil, false
	}
	// Anchor: the caller-scope prov def's defining file when visible.
	var anchor *saClassDef
	if pd, ok := scope.classes[prov]; ok && pd != nil {
		anchor = pd
	}
	d := saScopeOrHarvestClassDef(scope, ifaceName, anchor)
	if d == nil || !d.isIface {
		return "", nil, false
	}
	return recvName, d, true
}

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
//
// Phase MC fixpoint: the method-call arm's inputs are methodProv
// itself, so one pass is source-order-dependent (same-class inners
// invisible — merge at end of class; later-source-order cross-class
// inners invisible). The pass therefore iterates until quiet: each
// round analyzes only methods NOT yet recorded, merges per round so
// the next round sees earlier recordings, and stops on a round that
// adds nothing. Bound 1 + #methods (each non-quiet round adds >=1
// entry). Recorded entries are final, never revisited — a round-1
// recording read only stable entries, so later growth cannot
// invalidate it; a method unknown in round N (missing inner) may
// resolve in round N+1. Monotone add-only; agreement preserved.
func saAnalyzeMethodIfaceProv(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) {
	if sf == nil || funcs == nil || classes == nil {
		return
	}
	total := 0
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st == nil || st.Kind != ast.KindClassDeclaration {
			continue
		}
		nm := st.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		def, ok := classes[nm.Text()]
		if !ok || def == nil {
			continue
		}
		total += len(def.methods) + len(def.staticMethods)
	}
	for round := 0; round <= total; round++ {
		if saAnalyzeMethodIfaceProvRound(sf, funcs, classes, aliasOf) == 0 {
			return
		}
	}
}

// saAnalyzeMethodIfaceProvRound runs one pass of the method analysis
// over the file's classes, analyzing only methods NOT yet recorded in
// def.methodProv, and merging per-class results into def.methodProv.
// Returns the number of NEW entries added. The per-class prov map +
// collision rule are unchanged from the one-shot pass; skip-by-name
// makes the static/instance collision-across-rounds edge first-wins
// (a name recorded in an earlier round is never revisited, so a
// later-resolving same-name method of the other kind cannot
// invalidate it).
func saAnalyzeMethodIfaceProvRound(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) int {
	added := 0
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
				// Skip-recorded: recorded entries are final.
				if _, recorded := def.methodProv[mname]; recorded {
					continue
				}
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
				if _, ok := def.methodProv[mname]; !ok {
					def.methodProv[mname] = class
					added++
				}
			}
		}
	}
	return added
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

// saMethodCallProv resolves a method-call expression to the inner
// method's recorded provenance (Phase MC, JEV mc_*). It mirrors
// attach's walk and the direct-call resolver with analysis-time
// inputs: this → analyzing class (instance only); new → AST class
// name (concrete, non-abstract, in classes); identifier → walker
// entries map (entry class, absent → unknown; no static case —
// class-name identifiers yield unknown like params, GATE-B deferred);
// Call → two budgeted peels (chain depth-3, JEV
// uc_depth cap_2_peels: the wrapper passes peels=2, the
// core recurses through itself so guard checks re-apply, the inner
// kind is discarded and the outer walk returns the outer method's
// declared kind); guarded → fallthrough.
// Parent-chain walk override-shadows, missing methodProv entry stops
// the walk. Returns the inner prov class and the inner method's
// declared return kind ("inst:"+iface), or ok=false. The walk reads
// def.methods (instance) only; the method name must be a plain
// identifier (never `#`).
func saMethodCallProv(ce *ast.CallExpression, entries map[string]saIfaceRetEntry, classes map[string]*saClassDef, analyzingClass string, isStatic bool) (string, string, bool) {
	return saMethodCallProvBudget(ce, entries, classes, analyzingClass, isStatic, 2)
}

func saMethodCallProvBudget(ce *ast.CallExpression, entries map[string]saIfaceRetEntry, classes map[string]*saClassDef, analyzingClass string, isStatic bool, peels int) (string, string, bool) {
	if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return "", "", false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return "", "", false
	}
	mname := pa.Name().Text()
	if strings.HasPrefix(mname, "#") {
		return "", "", false
	}
	// Guarded shapes fall through (analysis-side guarded handling is
	// a separate question; the arm does not touch it).
	if pa.QuestionDotToken != nil || ce.QuestionDotToken != nil {
		return "", "", false
	}
	recv := saRetPeelParenNonNull(pa.Expression)
	if recv == nil {
		return "", "", false
	}
	var startClass string
	switch recv.Kind {
	case ast.KindThisKeyword:
		if isStatic || analyzingClass == "" {
			return "", "", false
		}
		def, ok := classes[analyzingClass]
		if !ok || def == nil || def.isIface || def.isAbstract {
			return "", "", false
		}
		startClass = analyzingClass
	case ast.KindNewExpression:
		ne := recv.AsNewExpression()
		if ne == nil || ne.Expression == nil || ne.Expression.Kind != ast.KindIdentifier {
			return "", "", false
		}
		cname := ne.Expression.Text()
		def, ok := classes[cname]
		if !ok || def == nil || def.isIface || def.isAbstract {
			return "", "", false
		}
		startClass = cname
	case ast.KindIdentifier:
		e, ok := entries[recv.Text()]
		if !ok || e.class == "" {
			return "", "", false
		}
		startClass = e.class
	case ast.KindCallExpression:
		// Chain receivers: one budgeted peel. Recursing through the
		// core itself re-applies the guard checks (unguarded CE + PA,
		// plain identifier method name, never `#`) to the inner call.
		// The inner kind is discarded: the outer walk below returns
		// the outer method's declared kind.
		if peels <= 0 {
			return "", "", false
		}
		recvCE := recv.AsCallExpression()
		if recvCE == nil {
			return "", "", false
		}
		innerProv, _, ok := saMethodCallProvBudget(recvCE, entries, classes, analyzingClass, isStatic, peels-1)
		if !ok || innerProv == "" {
			return "", "", false
		}
		startClass = innerProv
	default:
		// Everything else falls through.
		return "", "", false
	}
	visited := map[string]bool{}
	for cls := startClass; cls != ""; {
		if visited[cls] {
			return "", "", false
		}
		visited[cls] = true
		def, ok := classes[cls]
		if !ok || def == nil {
			return "", "", false
		}
		mn, ok := def.methods[mname]
		if ok && mn != nil {
			prov, ok := def.methodProv[mname]
			if !ok || prov == "" {
				return "", "", false
			}
			innerKind := saMethodDeclaredIface(mn, classes)
			if innerKind == "" {
				return "", "", false
			}
			return prov, innerKind, true
		}
		cls = def.parent
	}
	return "", "", false
}

// saMethodClassifyInit classifies a depth-0 const binding initializer
// into an entry, or reports no entry (unknown provenance, never an
// error). Free-function call results (bare-identifier callee with
// recorded class prov) and identifier copies of entries classify; in
// Phase C a NewExpression init also classifies (verbatim R0's new
// arm: annotation discarded, kind concrete, ctor args ignored — prov
// is about the class, not the construction). In Phase MC a
// method-call init also classifies via saMethodCallProv: entry =
// {class: inner prov class, kind: "inst:" + inner declared iface}
// (JEV mc_init_kind, declared_kind), guarded at record time by
// same-interface match (inner declared kind == the enclosing
// method's retKind) AND methodOnly — the guard must live here
// because the identifier passthrough enforces kind == retKind but has
// no methodOnly check, so a fieldful init-mediated path would
// otherwise leak. Field reads, object literals, and everything else
// yield no entry (field/litFn origins are all deferred in v1).
// analyzingClass/isStatic/retIface are the enclosing method's (the
// walker carries them; the sole caller is the methodMode branch).
func saMethodClassifyInit(init *ast.Node, annot *ast.TypeNode, entries map[string]saIfaceRetEntry, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, analyzingClass string, isStatic bool, retIface string) (saIfaceRetEntry, bool) {
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
		if ce == nil || ce.Expression == nil {
			return saIfaceRetEntry{}, false
		}
		// Phase MC method-call arm (see saMethodCallProv): the
		// entry kind is the inner DECLARED kind, and the
		// same-interface + methodOnly guards are enforced here at
		// record time (see the function comment).
		if ce.Expression.Kind == ast.KindPropertyAccessExpression {
			prov, innerKind, ok := saMethodCallProv(ce, entries, classes, analyzingClass, isStatic)
			if !ok || innerKind == "" {
				return saIfaceRetEntry{}, false
			}
			if innerKind != "inst:"+retIface || !saIfaceMethodOnly(classes, retIface) {
				return saIfaceRetEntry{}, false
			}
			return saIfaceRetEntry{class: prov, kind: innerKind}, true
		}
		if ce.Expression.Kind != ast.KindIdentifier {
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
// also classify. In Phase MC, method-call returns also classify via
// saMethodCallProv: inner prov exists AND inner declared iface ==
// outer retIface AND outer iface method-only (method-call handles
// are true handles via the ladder's alias discipline, M0 probe
// §2.2). Param, field, and litFn origins all yield unknown (each
// deferred in v1); the isLit rejection stays.
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
		if ce == nil || ce.Expression == nil {
			return unknown
		}
		// Phase MC method-call arm (see saMethodCallProv): inner
		// prov exists AND inner declared iface == outer retIface
		// AND outer iface method-only; otherwise unknown.
		if ce.Expression.Kind == ast.KindPropertyAccessExpression {
			prov, innerKind, ok := saMethodCallProv(ce, entries, classes, analyzingClass, isStatic)
			if !ok || innerKind != retKind {
				return unknown
			}
			if !saIfaceMethodOnly(classes, retIface) {
				return unknown
			}
			return saIfaceRetPath{class: prov}
		}
		if ce.Expression.Kind != ast.KindIdentifier {
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
	case recv.Kind == ast.KindCallExpression:
		// Bind-chain attach (iface_bindchain, bc_mechanism
		// core_reuse, iface_unbounded_chain uc_depth cap_2_peels): the initializer
		// is itself a chain call whose result the core
		// resolves. Outer guards first (the DC-1 branch's
		// pre-checks — the core does not check the outer
		// PA's guard token), then the receiver-call guards,
		// then one budgeted core resolution (peels=2,
		// bound=false per bc_xfile inherit_existing).
		// Anything else returns with no prov installed.
		if pa.QuestionDotToken != nil || ce.QuestionDotToken != nil {
			return
		}
		recvCE := recv.AsCallExpression()
		if recvCE == nil || recvCE.QuestionDotToken != nil || recvCE.Expression == nil || recvCE.Expression.Kind != ast.KindPropertyAccessExpression {
			return
		}
		linkPA := recvCE.Expression.AsPropertyAccessExpression()
		if linkPA == nil || linkPA.QuestionDotToken != nil || linkPA.Name() == nil || strings.HasPrefix(linkPA.Name().Text(), "#") {
			return
		}
		prov, calleeDef, ok := saResolveDirectCallProvDef(scope, pa)
		if !ok || prov == "" || calleeDef == nil {
			return
		}
		// Re-walk calleeDef's parent chain for the method
		// node, with the core's walk discipline exactly
		// (visited guard; first def holding the node wins;
		// a def holding the node but lacking the methodProv
		// entry STOPS the walk). The re-walk finds the same
		// def the core did, and recovers the node the core
		// does not return, for the existing kind check +
		// harvest fallback below.
		visited := map[string]bool{}
		for cls := calleeDef.name; cls != ""; {
			if visited[cls] {
				return
			}
			visited[cls] = true
			def, ok := scope.classes[cls]
			if !ok || def == nil {
				return
			}
			mn, ok := def.methods[mname]
			if ok && mn != nil {
				p, ok := def.methodProv[mname]
				if !ok || p == "" {
					return
				}
				if saMethodDeclaredIface(mn, scope.classes) != vkind {
					if !saAttachIfaceDeclaredHarvest(scope, def, mn, vkind) {
						return
					}
				}
				saSetIfaceProv(scope, name, prov)
				return
			}
			cls = def.parent
		}
		return
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
				// Dispatch-defs fallback (dd): caller-scope check
				// failed — consult the walk-def's defining-file
				// harvest, then program-wide unique, exported-only.
				// No prov is installed unless the declared name
				// resolves to an interface there.
				if !saAttachIfaceDeclaredHarvest(scope, def, mn, vkind) {
					return
				}
			}
			saSetIfaceProv(scope, name, prov)
			return
		}
		cls = def.parent
	}
}

// saResolveDirectCallProv is the direct-call dispatch resolver (DC-1):
// a pure lookup over scope + AST, mirroring saAttachIfaceMethodProv's
// walk exactly, minus the bind/kind checks (no binding, no vkind —
// the outer call's kind comes from saInstBaseElem). It resolves the
// inner method call's recorded methodProv entry from the inner
// call's receiver base:
//
//   - ThisKeyword (scope.thisSelf != "") -> scope.thisClass;
//   - Identifier with inst:<Concrete> kind -> that concrete class;
//   - Identifier with inst:<Iface> kind + scope.ifaceProv entry ->
//     the prov concrete class (bind-prov-mediated inner calls);
//   - NewExpression -> saNewStmtClassName, concrete non-abstract
//     def in caller scope.
//   - CallExpression -> one chain link (iface_chain, ch_depth
//     one_step_only): peel one Call receiver, re-applying the
//     DC-1 guards per link (unguarded CE + PA, non-#, PA callee),
//     recurse for the inner prov, then walk the inner prov
//     class's parent chain for the current method's entry.
//
// Class-name (static) receivers fall through (dc_static deferred:
// the resolver never consults staticMethods in v1), as do
// free-fn (bare-Identifier callee) links, guarded links, and
// everything else. The parent-chain walk is override-shadows:
// the first def holding the method node wins, and a missing
// methodProv entry there STOPS the walk. The same-file bound
// (dc_xfile same_file_only) applies to the single-level path
// only: when the callee def pointer-searches
// (saInlineFindDefFile precedent) to exactly one file that is
// NOT the current lowering file (scope.link.self), the branch
// is skipped; pointer-search failure (live same-file defs are
// not harvest pointers) or a current-file resolution engages.
// Chain links carry NO pointer-search bound (iface_chain,
// ch_xfile caller_scope_only): the only file control for a
// chain is the DC-1 branch's caller-scope prov-def lookup.
// The resolver writes nothing to scope (read-only table
// lookups); the inner call's emission already happened
// upstream in saInstBaseElem. Any failure returns ("", false)
// and the caller falls through unchanged to the interface
// message. Chain budget is cap_2 (iface_unbounded_chain, JEV
// uc_depth): depth-3 chains resolve, depth-4 stays refused
// (boundary at length 4); the single-level same-file bound is
// untouched.
func saResolveDirectCallProv(scope *saScope, innerPA *ast.PropertyAccessExpression) (string, bool) {
	prov, _, ok := saResolveDirectCallProvBudget(scope, innerPA, 2, true)
	return prov, ok
}

// saResolveDirectCallProvDef is the def-returning wrapper for
// attach (iface_bindchain, bc_mechanism core_reuse): same budget
// as the attach Call case (two peels, unbounded — uc_depth
// cap_2_peels, bc_xfile inherit_existing). The returned def is
// the def holding the resolved method node (from the core's own
// walk), so attach can re-walk for the method node and run the
// existing kind check + harvest fallback.
func saResolveDirectCallProvDef(scope *saScope, innerPA *ast.PropertyAccessExpression) (string, *saClassDef, bool) {
	return saResolveDirectCallProvBudget(scope, innerPA, 2, false)
}

// saResolveDirectCallProvBudget is the budgeted core behind
// saResolveDirectCallProv: peels is the remaining Call-receiver
// peel budget (2 for the call sites — cap_2 per JEV uc_depth), and
// bound selects the same-file pointer-search bound (true for
// the single-level path; chain links always resolve unbounded).
// It also returns the def holding the resolved method node
// (nil on failure); the recursive call's def is discarded — the
// outer walk's def is the result, since attach re-walks from it
// for the outer method node.
func saResolveDirectCallProvBudget(scope *saScope, innerPA *ast.PropertyAccessExpression, peels int, bound bool) (string, *saClassDef, bool) {
	if scope == nil || innerPA == nil || innerPA.Name() == nil {
		return "", nil, false
	}
	innerMethod := innerPA.Name().Text()
	if strings.HasPrefix(innerMethod, "#") {
		return "", nil, false
	}
	recv := saRetPeelParenNonNull(innerPA.Expression)
	if recv == nil {
		return "", nil, false
	}
	var startClass string
	var chainProv string
	var chainLink bool
	switch {
	case recv.Kind == ast.KindThisKeyword:
		if scope.thisSelf == "" || scope.thisClass == "" {
			return "", nil, false
		}
		startClass = scope.thisClass
	case recv.Kind == ast.KindIdentifier:
		rn := recv.Text()
		// Static receivers are deferred (dc_static): a class name
		// unshadowed in types and funcs must NOT resolve here.
		if def, ok := scope.classes[rn]; ok && def != nil {
			if _, shadowed := scope.types[rn]; !shadowed {
				if _, shadowed := scope.funcs[rn]; !shadowed {
					return "", nil, false
				}
			}
		}
		k, ok := scope.types[rn]
		if !ok || len(k) <= 5 || k[:5] != "inst:" {
			return "", nil, false
		}
		kind := k[5:]
		d, ok := scope.classes[kind]
		if !ok || d == nil {
			return "", nil, false
		}
		if d.isIface {
			prov, ok := scope.ifaceProv[rn]
			if !ok || prov == "" {
				return "", nil, false
			}
			pd, ok := scope.classes[prov]
			if !ok || pd == nil || pd.isIface || pd.isAbstract {
				return "", nil, false
			}
			startClass = prov
		} else {
			if d.isAbstract {
				return "", nil, false
			}
			startClass = kind
		}
	case recv.Kind == ast.KindNewExpression:
		cname, msg := saNewStmtClassName(recv.AsNewExpression())
		if msg != "" || cname == "" {
			return "", nil, false
		}
		d, ok := scope.classes[cname]
		if !ok || d == nil || d.isIface || d.isAbstract {
			return "", nil, false
		}
		startClass = cname
	case recv.Kind == ast.KindCallExpression:
		// One chain link: peel exactly one Call receiver, with
		// the DC-1 guards re-applied to the peeled link.
		if peels <= 0 {
			return "", nil, false
		}
		ce := recv.AsCallExpression()
		if ce == nil || ce.QuestionDotToken != nil || ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
			return "", nil, false
		}
		linkPA := ce.Expression.AsPropertyAccessExpression()
		if linkPA == nil || linkPA.QuestionDotToken != nil || linkPA.Name() == nil || strings.HasPrefix(linkPA.Name().Text(), "#") {
			return "", nil, false
		}
		innerProv, _, ok := saResolveDirectCallProvBudget(scope, linkPA, peels-1, false)
		if !ok || innerProv == "" {
			return "", nil, false
		}
		chainProv = innerProv
		chainLink = true
	default:
		return "", nil, false
	}
	walkFrom := startClass
	if chainLink {
		walkFrom = chainProv
	}
	visited := map[string]bool{}
	var prov string
	var calleeDef *saClassDef
	for cls := walkFrom; cls != ""; {
		if visited[cls] {
			return "", nil, false
		}
		visited[cls] = true
		def, ok := scope.classes[cls]
		if !ok || def == nil {
			return "", nil, false
		}
		mn, ok := def.methods[innerMethod]
		if ok && mn != nil {
			p, ok := def.methodProv[innerMethod]
			if !ok || p == "" {
				return "", nil, false
			}
			prov = p
			calleeDef = def
			break
		}
		cls = def.parent
	}
	if calleeDef == nil || prov == "" {
		return "", nil, false
	}
	// Same-file bound (dc_xfile same_file_only), single-level
	// path only: skip when the callee def resolves to exactly
	// one file that is not the current lowering file; engage on
	// pointer-search failure or a current-file resolution
	// (mirrors saInlineOrigin's file-identity comparison,
	// including the no-self guard). Chain links are unbounded
	// (ch_xfile caller_scope_only) — the branch's caller-scope
	// prov-def lookup is their only file control.
	if bound && !chainLink && scope.link != nil && scope.link.self != "" {
		if f, ok := saInlineFindDefFile(scope.link, calleeDef); ok && f != scope.link.self {
			return "", nil, false
		}
	}
	return prov, calleeDef, true
}
