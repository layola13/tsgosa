package transpile

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// Phase R0 interface-return provenance (branch tsgosa/parallel/iface_returns,
// JEV iface_v1_slice returns-first, iface_file_scope same_file_only,
// iface_async_scope sync_only, iface_sci_split emitter_only).
//
// Problem: saEvalReturnOperand never consults S1 provenance, so a function
// returning an interface kind drops the concrete class (or literal-method
// table) its value came from, and the caller-side dispatch then refuses
// with "X is an interface method with no body". This file adds a
// same-file, sync, emitter-only return analysis: for each function whose
// return kind is an interface, if EVERY return path provably yields the
// same concrete class (or the same literal-method table), that provenance
// is recorded on the signature (retIfaceProv / retIfaceLitFn, mirroring
// the retIsPromise precedent). The existing annotated/inferred call-result
// arms then attach it to the bound local, and the existing dispatch site
// handles the call unchanged.
//
// Fail-closed throughout: this analysis ADDS NO REFUSALS. Anything it
// cannot prove (mixed returns, unknown origins, cross-file callees, async
// functions, param/field origins, literal-method returns, shadowed or
// reassigned bindings, unwhitelisted statements) leaves the signature
// untouched, so the program refuses exactly as before.
//
// SOUNDNESS NOTE — the widen hole (empirically confirmed on main):
// `return c` where c has concrete kind inst:Real and the return kind is
// inst:Counter does NOT pass the true instance: lowering emits a widened
// handle (alloc + field copies by name). Provenance is therefore recorded
// for a concrete-kind identifier return ONLY when the interface is
// method-only (no data fields) and the class structurally satisfies it:
// either the class is fieldless (widen succeeds vacuously and the widened
// empty handle dispatches equivalently, since methods read no instance
// state) or it has fields (widen refuses loudly for missing fields, so
// the program refuses and the provenance is unobservable). Data-field
// interfaces never get identifier-return provenance in v1.
// Lowering itself does NOT refuse const reassignment (verified: a const
// reassignment builds clean), so the taint pass below treats ANY
// assignment to a name (including assignments buried inside expressions)
// as disqualifying that binding, and any shadowing declaration in a
// nested block disqualifies reads of that name inside the subtree.
//
// Deliberate v1 cuts (documented, not silent): method return analysis is
// skipped (the call-result arms cannot consume method-call results today:
// saCallRetKind is bare-identifier-only); switch/for-in/for-of bodies,
// destructuring bindings, and trailing if/else-only returns are
// unanalyzable; top-level const statements consuming slots and nested
// function declarations keep their existing behavior.

// saClassStructurallySatisfies reports whether concrete class have can
// stand in for interface want by method-set match alone (no implements
// clause): every method declared by want exists on have. want must be an
// interface and have must be a concrete class; interface-to-interface
// and concrete-to-concrete pairs are NOT structural matches here (the
// former keeps today's extends-only rule, the latter keeps exact/derived
// layout discipline). A method-less interface matches vacuously: the
// callee can call no method through it, so passing any concrete instance
// is sound.
func saClassStructurallySatisfies(classes map[string]*saClassDef, have, want string) bool {
	wdef, ok := classes[want]
	if !ok || wdef == nil || !wdef.isIface {
		return false
	}
	hdef, ok := classes[have]
	if !ok || hdef == nil || hdef.isIface {
		return false
	}
	for m := range wdef.methods {
		if _, ok := hdef.methods[m]; !ok {
			return false
		}
	}
	return true
}

// saIfaceMethodOnly reports whether an interface declares no data fields
// (methods live in the separate methods map; fields is a slice).
func saIfaceMethodOnly(classes map[string]*saClassDef, iface string) bool {
	def, ok := classes[iface]
	if !ok || def == nil || !def.isIface {
		return false
	}
	return len(def.fields) == 0
}

// saIfaceRetEntry is one analyzer binding: the provenance a const binding
// carries for return classification. kind is the lowering-recorded kind
// ("inst:"+name, or "" when the binding is unannotated).
type saIfaceRetEntry struct {
	isLit bool
	class string
	kind  string
	isNew bool // true only for direct new-expression bindings
	litFn map[string]*ast.Node
	litID int
}

// saIfaceRetPath is one classified return path.
type saIfaceRetPath struct {
	unknown bool
	isLit   bool
	class   string
	litID   int
	litFn   map[string]*ast.Node
}

// saAnalyzeReturnIfaceProv records retIfaceProv/retIfaceLitFn on same-file
// function signatures whose every return path provably yields the same
// concrete class or literal-method table for an interface return kind.
// It mutates only the funcs map (struct copies, same discipline as the
// prescan declSigs override) and adds no refusals.
// Phase P1: block-body walks seed entries with the function's CURRENT
// recorded P0 param prov (class only, via saRetParamSeedEntries); the
// joint wrapper (sa_iface_joint.go) re-runs this analysis after each P0
// pass so param prov feeds back into return analysis.
func saAnalyzeReturnIfaceProv(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) {
	type bodyInfo struct {
		body       *ast.Node
		params     []string
		paramsOK   bool
		paramNodes []*ast.Node
	}
	bodies := map[string]bodyInfo{}
	note := func(key string, body *ast.Node, paramNodes []*ast.Node) {
		if body == nil {
			return
		}
		params, ok := saRetParamNames(paramNodes)
		bodies[key] = bodyInfo{body: body, params: params, paramsOK: ok, paramNodes: paramNodes}
	}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		switch st.Kind {
		case ast.KindFunctionDeclaration:
			fn := st.AsFunctionDeclaration()
			if fn == nil || fn.Body == nil {
				continue
			}
			key := ""
			if nm := fn.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				key = nm.Text()
			} else if ast.HasModifier(st, ast.ModifierFlagsDefault) {
				key = "default"
			} else {
				continue
			}
			var params []*ast.Node
			if fn.Parameters != nil {
				params = fn.Parameters.Nodes
			}
			note(key, fn.Body, params)
		case ast.KindModuleDeclaration:
			members, ok := saFlattenNsMembers(st)
			if !ok {
				continue
			}
			for _, mb := range members {
				if !mb.isFunc {
					continue
				}
				fn := mb.node.AsFunctionDeclaration()
				if fn == nil || fn.Body == nil {
					continue
				}
				var params []*ast.Node
				if fn.Parameters != nil {
					params = fn.Parameters.Nodes
				}
				note(mb.under, fn.Body, params)
				note(mb.dotted, fn.Body, params)
			}
		case ast.KindVariableStatement:
			name, arrow, ok := saIsTopLevelArrowConst(st)
			if !ok || arrow.Body() == nil {
				continue
			}
			var params []*ast.Node
			if pl := arrow.ParameterList(); pl != nil {
				params = pl.Nodes
			}
			note(name, arrow.Body(), params)
		}
	}
	// Fixpoint: return->return chains resolve regardless of file order.
	// Each round can only ADD provenance, never remove it, so termination
	// is bounded by the number of signatures (+1 round to observe quiet).
	for round := 0; round <= len(funcs); round++ {
		changed := false
		for key, sig := range funcs {
			if sig.retIfaceProv != "" || len(sig.retIfaceLitFn) > 0 {
				continue
			}
			// Phase A (async v1): retIsPromise no longer excludes
			// same-file fn decls from analysis (JEV include_async,
			// narrowed to returns); the isVoid and inst: gates stand.
			if sig.isVoid || len(sig.retKind) <= 5 || sig.retKind[:5] != "inst:" {
				continue
			}
			iface := sig.retKind[5:]
			idef, ok := classes[iface]
			if !ok || idef == nil || !idef.isIface {
				continue
			}
			bi, ok := bodies[key]
			if !ok || !bi.paramsOK {
				continue
			}
			// Phase P1: seed the walk with this function's CURRENT
			// recorded P0 param prov (class only). Param prov only
			// changes in P0 passes, which run between R0 calls, so the
			// seed is stable across this call's internal rounds.
			seed := saRetParamSeedEntries(sig, bi.paramNodes, classes)
			// Phase F: param kinds for field-read receivers (exact
			// index alignment with bi.params; length mismatch means
			// no paramKinds, mirroring the seed rule).
			var paramKinds map[string]string
			if len(bi.params) == len(sig.paramKinds) {
				paramKinds = map[string]string{}
				for i, name := range bi.params {
					if name != "" {
						paramKinds[name] = sig.paramKinds[i]
					}
				}
			}
			prov, litFn, has := saAnalyzeFuncReturns(bi.body, bi.params, seed, sig.retKind, funcs, classes, aliasOf, paramKinds)
			if !has {
				continue
			}
			if prov != "" {
				sig.retIfaceProv = prov
			} else if !sig.retIsPromise {
				// Phase A (async v1) JEV guard (fixpoint_loop_skip,
				// Q6 defer_async_litfn): async functions never record
				// litFn tables; a_async_litfn must keep refusing.
				sig.retIfaceLitFn = litFn
			} else {
				// Async litFn result discarded: no table recorded,
				// and no change signalled (the sig is unchanged).
				continue
			}
			funcs[key] = sig
			changed = true
		}
		if !changed {
			break
		}
	}
}

// saRetParamNames extracts parameter binding names (identifier or pattern
// names). Failure (unrecognized shape) marks the function unanalyzable.
func saRetParamNames(nodes []*ast.Node) ([]string, bool) {
	var out []string
	for _, p := range nodes {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			return nil, false
		}
		nm := pd.Name()
		if nm == nil {
			return nil, false
		}
		if nm.Kind == ast.KindIdentifier {
			out = append(out, nm.Text())
			continue
		}
		names, ok := saRetPatternNames(nm)
		if !ok {
			return nil, false
		}
		out = append(out, names...)
	}
	return out, true
}

// saRetPatternNames extracts all bound names from an identifier or a
// binding pattern (object/array, nested). Anything else fails.
func saRetPatternNames(nm *ast.Node) ([]string, bool) {
	if nm == nil {
		return nil, false
	}
	switch nm.Kind {
	case ast.KindIdentifier:
		return []string{nm.Text()}, true
	case ast.KindObjectBindingPattern:
		obp := nm.AsBindingPattern()
		if obp == nil || obp.Elements == nil {
			return nil, false
		}
		var out []string
		for _, el := range obp.Elements.Nodes {
			be := el.AsBindingElement()
			if be == nil {
				return nil, false
			}
			bn := be.Name()
			if bn == nil {
				return nil, false
			}
			names, ok := saRetPatternNames(bn)
			if !ok {
				return nil, false
			}
			out = append(out, names...)
		}
		return out, true
	case ast.KindArrayBindingPattern:
		abp := nm.AsBindingPattern()
		if abp == nil || abp.Elements == nil {
			return nil, false
		}
		var out []string
		for _, el := range abp.Elements.Nodes {
			if el == nil || el.Kind == ast.KindOmittedExpression {
				continue
			}
			be := el.AsBindingElement()
			if be == nil {
				return nil, false
			}
			bn := be.Name()
			if bn == nil {
				return nil, false
			}
			names, ok := saRetPatternNames(bn)
			if !ok {
				return nil, false
			}
			out = append(out, names...)
		}
		return out, true
	}
	return nil, false
}

// saRetParamSeedEntries builds param-origin return entries for one function
// from its CURRENT recorded P0 param prov (Phase P1). Class prov only:
// litFn params are never seeded (class_only). Alignment is exact:
// paramNodes[i] pairs with paramKinds[i]/paramIfaceProv[i]; any length
// mismatch skips seeding for the whole function (fail-closed).
// Non-identifier params never seed (P0 records no prov for them; their
// prov, if any, is unreachable by name). The seeded entry kind is the
// param's declared kind, so the classifier's passthrough arm (kind ==
// retKind) admits same-interface param→return flows and refuses
// cross-interface ones (not isNew, so the widened-handle arm cannot
// admit them either).
func saRetParamSeedEntries(sig saFuncSig, paramNodes []*ast.Node, classes map[string]*saClassDef) map[string]saIfaceRetEntry {
	if len(paramNodes) != len(sig.paramKinds) || len(sig.paramIfaceProv) != len(sig.paramKinds) {
		return nil
	}
	var out map[string]saIfaceRetEntry
	for i, n := range paramNodes {
		prov := sig.paramIfaceProv[i]
		if prov == "" {
			continue
		}
		kind := sig.paramKinds[i]
		if len(kind) <= 5 || kind[:5] != "inst:" {
			continue
		}
		idef, ok := classes[kind[5:]]
		if !ok || idef == nil || !idef.isIface {
			continue
		}
		if n == nil {
			continue
		}
		pd := n.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		if out == nil {
			out = map[string]saIfaceRetEntry{}
		}
		out[nm.Text()] = saIfaceRetEntry{class: prov, kind: kind}
	}
	return out
}

// saAnalyzeFuncReturns classifies every return path of one function body.
// body is a Block node (declarations) or an arrow body (Block or a single
// expression, which is one synthetic return path). seed carries Phase P1
// param-origin entries (class only, block bodies only); expression bodies
// classify with nil entries as before (block_only). Returns the agreed
// concrete class (prov) or literal-method table (litFn), or has=false.
func saAnalyzeFuncReturns(body *ast.Node, paramNames []string, seed map[string]saIfaceRetEntry, retKind string, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, paramKinds map[string]string) (string, map[string]*ast.Node, bool) {
	retIface := retKind[5:]
	if body == nil {
		return "", nil, false
	}
	// Arrow expression bodies are a single synthetic return path with no
	// bindings: classify the expression directly.
	if body.Kind != ast.KindBlock {
		litSeq := 0
		p := saRetClassifyReturn(body, nil, nil, retKind, retIface, funcs, classes, &litSeq, paramNameSet(paramNames))
		if p.unknown {
			return "", nil, false
		}
		if p.isLit {
			// Phase R1: only fresh-literal expression bodies record
			// litFn; a call-carried litFn keeps R0's conservative
			// rejection (it needs a const binding in v1's original
			// reasoning). The classifier already ran the closure
			// check for the literal arm.
			if pe := saRetPeelParenNonNull(body); pe == nil || pe.Kind != ast.KindObjectLiteralExpression {
				return "", nil, false
			}
			return "", p.litFn, true
		}
		return p.class, nil, true
	}
	bd := body.AsBlock()
	if bd == nil || bd.Statements == nil {
		return "", nil, false
	}
	stmts := bd.Statements.Nodes
	// Pass 1: taint. Parameters count as declared once, so a body const
	// sharing a parameter name is tainted (lowering refuses duplicates,
	// but the analysis must not trust the name regardless).
	t := &saRetTaint{counts: map[string]int{}, assigned: map[string]bool{}}
	for _, p := range paramNames {
		t.counts[p]++
	}
	saRetTaintStmts(stmts, t)
	if t.unknown {
		return "", nil, false
	}
	// Pass 2: ordered walk, depth-0 const bindings + all return paths.
	// Phase R1: forbidden = defining-function params + declared names
	// (taint keys) for the fresh-literal closure check. Taint keys are
	// a superset of the names actually in scope at any given literal
	// (any depth, any branch) — over-refusal, fail-closed, sound.
	forbidden := map[string]bool{}
	for name := range t.counts {
		forbidden[name] = true
	}
	w := &saRetWalker{
		taint:      t,
		entries:    map[string]saIfaceRetEntry{},
		ok:         true,
		funcs:      funcs,
		classes:    classes,
		aliasOf:    aliasOf,
		retKind:    retKind,
		retIface:   retIface,
		litSeq:     0,
		paramKinds: paramKinds,
		forbidden:  forbidden,
	}
	// Phase P1: seed block-body entries with param-origin prov.
	// Tainted names (assigned, or shared with a same-name const) are
	// never seeded — the same rule bindVarDecl applies to const
	// entries — so a seeded entry can never be overwritten by a
	// same-name const or observed after a reassignment. Shadowed
	// names still yield unknown: the classifier checks shadowed
	// before entries lookup.
	for name, e := range seed {
		if !t.tainted(name) {
			w.entries[name] = e
		}
	}
	w.walkList(stmts, 0, nil)
	if !w.ok || len(w.paths) == 0 {
		return "", nil, false
	}
	for _, p := range w.paths {
		if p.unknown {
			return "", nil, false
		}
	}
	first := w.paths[0]
	for _, p := range w.paths[1:] {
		if p.isLit != first.isLit {
			return "", nil, false
		}
		if first.isLit {
			if p.litID != first.litID {
				return "", nil, false
			}
		} else if p.class != first.class {
			return "", nil, false
		}
	}
	// Trailing-return rule: the body block's last top-level statement
	// must be a return or a throw. Anything else (trailing if/else-only
	// returns, trailing statements after the last return) fails closed.
	if len(stmts) == 0 {
		return "", nil, false
	}
	last := stmts[len(stmts)-1]
	if last == nil || (last.Kind != ast.KindReturnStatement && last.Kind != ast.KindThrowStatement) {
		return "", nil, false
	}
	if first.isLit {
		return "", first.litFn, true
	}
	return first.class, nil, true
}

// saRetTaint is pass-1 state: declaration counts and assigned names.
type saRetTaint struct {
	counts   map[string]int
	assigned map[string]bool
	unknown  bool
}

func (t *saRetTaint) tainted(name string) bool {
	return t.counts[name] >= 2 || t.assigned[name]
}

// saRetTaintStmts is pass 1: collect declared names (any depth, excluding
// nested functions) and assigned names (any expression position), and
// flag unwhitelisted statement kinds.
func saRetTaintStmts(stmts []*ast.Node, t *saRetTaint) {
	for _, st := range stmts {
		if st == nil {
			continue
		}
		switch st.Kind {
		case ast.KindBlock:
			if bd := st.AsBlock(); bd != nil && bd.Statements != nil {
				saRetTaintStmts(bd.Statements.Nodes, t)
			}
		case ast.KindVariableStatement:
			saRetTaintVarDecl(st, t)
		case ast.KindExpressionStatement:
			if es := st.AsExpressionStatement(); es != nil {
				saRetScanAssign(es.Expression, t.assigned)
			}
		case ast.KindReturnStatement:
			if rs := st.AsReturnStatement(); rs != nil {
				saRetScanAssign(rs.Expression, t.assigned)
			}
		case ast.KindThrowStatement:
			if ts := st.AsThrowStatement(); ts != nil {
				saRetScanAssign(ts.Expression, t.assigned)
			}
		case ast.KindIfStatement:
			if is := st.AsIfStatement(); is != nil {
				saRetScanAssign(is.Expression, t.assigned)
				if thenStmts, ok := saEmbeddedBlock(is.ThenStatement); ok {
					saRetTaintStmts(thenStmts, t)
				}
				if elseStmts, ok := saEmbeddedBlock(is.ElseStatement); ok {
					saRetTaintStmts(elseStmts, t)
				}
			}
		case ast.KindWhileStatement:
			if ws := st.AsWhileStatement(); ws != nil {
				saRetScanAssign(ws.Expression, t.assigned)
				if bodyStmts, ok := saEmbeddedBlock(ws.Statement); ok {
					saRetTaintStmts(bodyStmts, t)
				}
			}
		case ast.KindDoStatement:
			if ds := st.AsDoStatement(); ds != nil {
				if bodyStmts, ok := saEmbeddedBlock(ds.Statement); ok {
					saRetTaintStmts(bodyStmts, t)
				}
				saRetScanAssign(ds.Expression, t.assigned)
			}
		case ast.KindForStatement:
			if fs := st.AsForStatement(); fs != nil {
				saRetTaintForInit(fs.Initializer, t)
				saRetScanAssign(fs.Condition, t.assigned)
				saRetScanAssign(fs.Incrementor, t.assigned)
				if bodyStmts, ok := saEmbeddedBlock(fs.Statement); ok {
					saRetTaintStmts(bodyStmts, t)
				}
			}
		case ast.KindTryStatement:
			if ts := st.AsTryStatement(); ts != nil {
				if ts.TryBlock != nil {
					saRetTaintStmts(ts.TryBlock.AsBlock().Statements.Nodes, t)
				}
				if ts.CatchClause != nil {
					if cc := ts.CatchClause.AsCatchClause(); cc != nil {
						if cc.VariableDeclaration != nil {
							vd := cc.VariableDeclaration.AsVariableDeclaration()
							if vd == nil {
								t.unknown = true
							} else if nm := vd.Name(); nm != nil {
								names, ok := saRetPatternNames(nm)
								if !ok {
									t.unknown = true
								} else {
									for _, n := range names {
										t.counts[n]++
									}
								}
							}
						}
						if cc.Block != nil {
							saRetTaintStmts(cc.Block.AsBlock().Statements.Nodes, t)
						}
					}
				}
				if ts.FinallyBlock != nil {
					saRetTaintStmts(ts.FinallyBlock.AsBlock().Statements.Nodes, t)
				}
			}
		case ast.KindFunctionDeclaration, ast.KindClassDeclaration:
			if nm := st.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				t.counts[nm.Text()]++
			}
		case ast.KindBreakStatement, ast.KindContinueStatement, ast.KindEmptyStatement, ast.KindDebuggerStatement:
			// No bindings, no assignments.
		default:
			// ForIn/ForOf, Switch, Labeled, With, and anything else:
			// the function is unanalyzable (fail-closed).
			t.unknown = true
		}
	}
}

// saRetTaintVarDecl collects declarator names and scans initializers.
func saRetTaintVarDecl(st *ast.Node, t *saRetTaint) {
	vs := st.AsVariableStatement()
	if vs == nil || vs.DeclarationList == nil {
		t.unknown = true
		return
	}
	dl := vs.DeclarationList.AsVariableDeclarationList()
	if dl == nil {
		t.unknown = true
		return
	}
	for _, d := range dl.Declarations.Nodes {
		vd := d.AsVariableDeclaration()
		if vd == nil {
			t.unknown = true
			continue
		}
		if nm := vd.Name(); nm != nil {
			names, ok := saRetPatternNames(nm)
			if !ok {
				t.unknown = true
			} else {
				for _, n := range names {
					t.counts[n]++
				}
			}
		}
		saRetScanAssign(vd.Initializer, t.assigned)
	}
}

// saRetTaintForInit handles a for-statement initializer: a declaration
// list contributes names (+ scans initializers), an expression is scanned.
func saRetTaintForInit(init *ast.Node, t *saRetTaint) {
	if init == nil {
		return
	}
	if init.Kind == ast.KindVariableDeclarationList {
		dl := init.AsVariableDeclarationList()
		if dl == nil {
			t.unknown = true
			return
		}
		for _, d := range dl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil {
				t.unknown = true
				continue
			}
			if nm := vd.Name(); nm != nil {
				names, ok := saRetPatternNames(nm)
				if !ok {
					t.unknown = true
				} else {
					for _, n := range names {
						t.counts[n]++
					}
				}
			}
			saRetScanAssign(vd.Initializer, t.assigned)
		}
		return
	}
	saRetScanAssign(init, t.assigned)
}

// saRetDirectDeclNames returns names declared directly by the VariableStatements
// in one statement list (for shadow computation when recursing into a
// nested list). For-statement init declarations are handled by the caller.
func saRetDirectDeclNames(stmts []*ast.Node) map[string]bool {
	out := map[string]bool{}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindVariableStatement {
			continue
		}
		vs := st.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			continue
		}
		dl := vs.DeclarationList.AsVariableDeclarationList()
		if dl == nil {
			continue
		}
		for _, d := range dl.Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			if vd == nil {
				continue
			}
			if nm := vd.Name(); nm != nil {
				if names, ok := saRetPatternNames(nm); ok {
					for _, n := range names {
						out[n] = true
					}
				}
			}
		}
	}
	return out
}

// saRetWalker is pass 2: ordered depth-0 const bindings + return paths,
// with shadowing tracked per subtree.
type saRetWalker struct {
	taint      *saRetTaint
	entries    map[string]saIfaceRetEntry
	paths      []saIfaceRetPath
	ok         bool
	funcs      map[string]saFuncSig
	classes    map[string]*saClassDef
	aliasOf    map[string]*ast.TypeNode
	retKind    string
	retIface   string
	litSeq     int
	paramKinds map[string]string // name -> declared kind, for field-read receivers
	// forbidden is Phase R1 only: defining-function params + declared
	// names (taint keys) for the fresh-literal closure check. The
	// classifier consults it only for its ObjectLiteral arm.
	forbidden map[string]bool
	// methodMode is Phase M only: the walker classifies with the
	// restricted method classifier (free-function call results only,
	// class only) instead of R0's classifier. False for all R0/P0/P1/
	// fields calls, so their behavior is unchanged by construction.
	methodMode bool
	// methodClass/methodStatic are Phase C only: the analyzing class
	// and static-ness for the method-mode this arm (return this
	// yields the analyzing class, instance methods only). Empty/false
	// for all R0/P0/P1/fields calls.
	methodClass  string
	methodStatic bool
}

func saRetCopyShadow(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

// walkList walks one statement list. depth 0 is the body block itself
// (bindings recorded there); deeper lists only contribute return paths,
// and names they declare directly shadow outer entries in their subtree.
func (w *saRetWalker) walkList(stmts []*ast.Node, depth int, shadowed map[string]bool) {
	if depth > 0 {
		shadowed = saRetCopyShadow(shadowed)
		for n := range saRetDirectDeclNames(stmts) {
			shadowed[n] = true
		}
	}
	for _, st := range stmts {
		if st == nil {
			continue
		}
		switch st.Kind {
		case ast.KindBlock:
			if bd := st.AsBlock(); bd != nil && bd.Statements != nil {
				w.walkList(bd.Statements.Nodes, depth+1, shadowed)
			}
		case ast.KindVariableStatement:
			if depth == 0 {
				w.bindVarDecl(st)
			}
		case ast.KindReturnStatement:
			if rs := st.AsReturnStatement(); rs != nil {
				if w.methodMode {
					w.paths = append(w.paths, saMethodClassifyReturn(rs.Expression, w.entries, shadowed, w.retKind, w.retIface, w.funcs, w.classes, w.methodClass, w.methodStatic))
				} else {
					w.paths = append(w.paths, saRetClassifyReturn(rs.Expression, w.entries, shadowed, w.retKind, w.retIface, w.funcs, w.classes, &w.litSeq, w.forbidden))
				}
			} else {
				w.paths = append(w.paths, saIfaceRetPath{unknown: true})
			}
		case ast.KindIfStatement:
			if is := st.AsIfStatement(); is != nil {
				if thenStmts, ok := saEmbeddedBlock(is.ThenStatement); ok {
					w.walkList(thenStmts, depth+1, shadowed)
				}
				if elseStmts, ok := saEmbeddedBlock(is.ElseStatement); ok {
					w.walkList(elseStmts, depth+1, shadowed)
				}
			}
		case ast.KindWhileStatement:
			if ws := st.AsWhileStatement(); ws != nil {
				if bodyStmts, ok := saEmbeddedBlock(ws.Statement); ok {
					w.walkList(bodyStmts, depth+1, shadowed)
				}
			}
		case ast.KindDoStatement:
			if ds := st.AsDoStatement(); ds != nil {
				if bodyStmts, ok := saEmbeddedBlock(ds.Statement); ok {
					w.walkList(bodyStmts, depth+1, shadowed)
				}
			}
		case ast.KindForStatement:
			if fs := st.AsForStatement(); fs != nil {
				sub := saRetCopyShadow(shadowed)
				if fs.Initializer != nil && fs.Initializer.Kind == ast.KindVariableDeclarationList {
					dl := fs.Initializer.AsVariableDeclarationList()
					if dl != nil {
						for _, d := range dl.Declarations.Nodes {
							vd := d.AsVariableDeclaration()
							if vd == nil {
								continue
							}
							if nm := vd.Name(); nm != nil {
								if names, ok := saRetPatternNames(nm); ok {
									for _, n := range names {
										sub[n] = true
									}
								}
							}
						}
					}
				}
				if bodyStmts, ok := saEmbeddedBlock(fs.Statement); ok {
					w.walkList(bodyStmts, depth+1, sub)
				}
			}
		case ast.KindTryStatement:
			if ts := st.AsTryStatement(); ts != nil {
				if ts.TryBlock != nil {
					w.walkList(ts.TryBlock.AsBlock().Statements.Nodes, depth+1, shadowed)
				}
				if ts.CatchClause != nil {
					if cc := ts.CatchClause.AsCatchClause(); cc != nil {
						sub := saRetCopyShadow(shadowed)
						if cc.VariableDeclaration != nil {
							vd := cc.VariableDeclaration.AsVariableDeclaration()
							if vd != nil {
								if nm := vd.Name(); nm != nil {
									if names, ok := saRetPatternNames(nm); ok {
										for _, n := range names {
											sub[n] = true
										}
									}
								}
							}
						}
						if cc.Block != nil {
							w.walkList(cc.Block.AsBlock().Statements.Nodes, depth+1, sub)
						}
					}
				}
				if ts.FinallyBlock != nil {
					w.walkList(ts.FinallyBlock.AsBlock().Statements.Nodes, depth+1, shadowed)
				}
			}
		case ast.KindThrowStatement, ast.KindBreakStatement, ast.KindContinueStatement,
			ast.KindEmptyStatement, ast.KindDebuggerStatement,
			ast.KindExpressionStatement, ast.KindFunctionDeclaration, ast.KindClassDeclaration:
			// No bindings (depth-0 var decls handled above) and no returns.
		default:
			// Unreachable: pass 1 flags these, but stay fail-closed.
			w.ok = false
		}
	}
}

// bindVarDecl records depth-0 const bindings (tainted names skipped).
func (w *saRetWalker) bindVarDecl(st *ast.Node) {
	vs := st.AsVariableStatement()
	if vs == nil || vs.DeclarationList == nil {
		return
	}
	dlNode := vs.DeclarationList.AsNode()
	if dlNode == nil || dlNode.Flags&ast.NodeFlagsConst == 0 {
		return
	}
	dl := vs.DeclarationList.AsVariableDeclarationList()
	if dl == nil {
		return
	}
	for _, d := range dl.Declarations.Nodes {
		vd := d.AsVariableDeclaration()
		if vd == nil {
			continue
		}
		nm := vd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		name := nm.Text()
		if w.taint.tainted(name) {
			continue
		}
		if w.methodMode {
			if e, ok := saMethodClassifyInit(vd.Initializer, vd.Type, w.entries, w.funcs, w.classes, w.aliasOf); ok {
				w.entries[name] = e
			}
		} else if e, ok := saRetClassifyInitWithEntries(vd.Initializer, vd.Type, w.entries, nil, w.funcs, w.classes, w.aliasOf, &w.litSeq, w.paramKinds); ok {
			w.entries[name] = e
		}
	}
}

// saRetPeelParenNonNull peels ONLY parentheses and non-null assertions.
// as/satisfies/type-assertions are deliberately NOT peeled: a cast return
// is an unknown path in v1 (unlike saUnwrapTransparent, which peels them).
func saRetPeelParenNonNull(e *ast.Node) *ast.Node {
	for e != nil {
		switch e.Kind {
		case ast.KindParenthesizedExpression:
			if pe := e.AsParenthesizedExpression(); pe != nil {
				e = pe.Expression
				continue
			}
			return e
		case ast.KindNonNullExpression:
			if ne := e.AsNonNullExpression(); ne != nil {
				e = ne.Expression
				continue
			}
			return e
		}
		return e
	}
	return e
}

// saRetAnnotName resolves a plain (non-generic) TypeReference annotation
// to a class/interface name, chasing aliases (max 8 steps). Anything else
// (unions, qualified names, generics, missing) resolves to "".
func saRetAnnotName(t *ast.TypeNode, aliasOf map[string]*ast.TypeNode) string {
	for i := 0; i < 8 && t != nil; i++ {
		if t.Kind != ast.KindTypeReference {
			return ""
		}
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil {
			return ""
		}
		if ref.TypeArguments != nil {
			return ""
		}
		tn := ref.TypeName
		if tn.Kind != ast.KindIdentifier {
			return ""
		}
		name := tn.Text()
		next, ok := aliasOf[name]
		if !ok || next == nil {
			return name
		}
		t = next
	}
	return ""
}

// saRetBuildLitFn builds the literal-method table for an object literal
// against an interface, mirroring saLowerObjectLiteral exactly: a
// MethodDeclaration with a body whose name is an interface method records
// the declaration node itself; a PropertyAssignment whose name is an
// interface method with an arrow/function-expression value records the
// value node. Anything else (non-interface properties, body-less
// methods, non-function method values, spreads, shorthand, computed
// names) fails: the binding carries no literal provenance in v1.
func saRetBuildLitFn(lit *ast.Node, iface string, classes map[string]*saClassDef) (map[string]*ast.Node, bool) {
	idef, ok := classes[iface]
	if !ok || idef == nil || !idef.isIface {
		return nil, false
	}
	ol := lit.AsObjectLiteralExpression()
	if ol == nil || ol.Properties == nil {
		return nil, false
	}
	table := map[string]*ast.Node{}
	for _, p := range ol.Properties.Nodes {
		switch p.Kind {
		case ast.KindPropertyAssignment:
			fname, ok := saObjPropName(p)
			if !ok {
				return nil, false
			}
			if _, isMeth := idef.methods[fname]; !isMeth {
				return nil, false
			}
			init := p.AsPropertyAssignment().Initializer
			if init == nil || (init.Kind != ast.KindArrowFunction && init.Kind != ast.KindFunctionExpression) {
				return nil, false
			}
			table[fname] = init
		case ast.KindMethodDeclaration:
			fname, ok := saObjPropName(p)
			if !ok || p.Body() == nil {
				return nil, false
			}
			if _, isMeth := idef.methods[fname]; !isMeth {
				return nil, false
			}
			table[fname] = p
		default:
			return nil, false
		}
	}
	return table, true
}

// Phase R1: closure safety for fresh-literal method tables.
//
// A recorded litFn table inlines its method bodies into the CALLER's
// lowering scope (saInlineMethodCore swaps only thisSelf/thisClass).
// A method body referencing a defining-function param/local, `this`,
// `super`, or bare `arguments` would therefore resolve in caller scope
// — loudly refusing when absent, SILENTLY resolving to a different
// caller binding when present (r_closure_shadow: make's n = 5 vs
// main2's n = 99). The analysis must yield unknown for any literal
// whose method bodies reference them (JEV r1_closure,
// exclude_defining_locals, UNANIMOUS 1.00). `this`/`super`/
// `arguments` are receiver/frame-relative references in the same hole
// class and are flagged conservatively (fail-closed; no probe minimal
// uses them, so this cannot change any gate outcome).
//
// The walk is scoped and fail-closed: identifiers bound within the
// method body itself (method params, method-body locals, nested
// function params/locals) are safe, and the walk descends through
// nested functions subtracting each nested function's own params +
// locals (a nested arrow closing over a defining local is the same
// capture). Over-refusal (shadowing, property/type names reached via
// the generic fallback, pattern identifiers) is the fail-closed
// direction and is accepted. Declarations bind only the scope they
// are added to, and are processed in statement order (use-before-decl
// is treated as free, also fail-closed); a use that would resolve to
// a sibling block's declaration is therefore never treated as bound.
//
// saRetLitFnClosureOK reports whether a literal-method table is safe
// to record. forbidden is the set of defining-function param + declared
// names; a free identifier matching one, or any `this`/`super`/bare
// `arguments`, makes the table unsafe.
func saRetLitFnClosureOK(table map[string]*ast.Node, forbidden map[string]bool) bool {
	if len(table) == 0 {
		return true
	}
	w := &saRetClosureWalk{forbidden: forbidden}
	for _, m := range table {
		if m == nil {
			return false
		}
		var params []*ast.Node
		switch m.Kind {
		case ast.KindMethodDeclaration:
			if md := m.AsMethodDeclaration(); md != nil && md.Parameters != nil {
				params = md.Parameters.Nodes
			}
		case ast.KindArrowFunction:
			if af := m.AsArrowFunction(); af != nil && af.Parameters != nil {
				params = af.Parameters.Nodes
			}
		case ast.KindFunctionExpression:
			if fe := m.AsFunctionExpression(); fe != nil && fe.Parameters != nil {
				params = fe.Parameters.Nodes
			}
		default:
			return false
		}
		body := m.Body()
		if body == nil {
			return false
		}
		w.push()
		w.bindFuncParams(params)
		w.walk(body)
		w.pop()
		if w.bad {
			return false
		}
	}
	return true
}

type saRetClosureWalk struct {
	forbidden map[string]bool
	bad       bool
	scopes    []map[string]bool
}

func (w *saRetClosureWalk) bound(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

func (w *saRetClosureWalk) push() {
	w.scopes = append(w.scopes, map[string]bool{})
}

func (w *saRetClosureWalk) pop() {
	w.scopes = w.scopes[:len(w.scopes)-1]
}

func (w *saRetClosureWalk) bind(name string) {
	if len(w.scopes) == 0 {
		w.push()
	}
	w.scopes[len(w.scopes)-1][name] = true
}

func (w *saRetClosureWalk) checkIdent(name string) {
	if w.bad {
		return
	}
	if name == "arguments" && !w.bound(name) {
		// Bare `arguments` resolves to the caller frame's arguments
		// after inlining, not the method's own.
		w.bad = true
		return
	}
	if !w.bound(name) && w.forbidden[name] {
		w.bad = true
	}
}

// bindDecl walks a declaration's reference positions (initializer,
// computed pattern keys) BEFORE binding its names, so no reference can
// hide behind a same-name binding. Pattern identifiers walked before
// binding over-refuse on shadowing (fail-closed, accepted).
func (w *saRetClosureWalk) bindDecl(name *ast.Node, init *ast.Node) {
	if init != nil {
		w.walk(init)
	}
	if name != nil && name.Kind != ast.KindIdentifier {
		w.walk(name)
	}
	if name != nil {
		if names, ok := saRetPatternNames(name); ok {
			for _, n := range names {
				w.bind(n)
			}
		}
	}
}

// bindFuncParams binds a function node's parameters into the current
// (already pushed) function scope, walking defaults and non-identifier
// patterns for references first (same order discipline as bindDecl).
func (w *saRetClosureWalk) bindFuncParams(paramNodes []*ast.Node) {
	for _, p := range paramNodes {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		if pd.Initializer != nil {
			w.walk(pd.Initializer)
		}
		if nm := pd.Name(); nm != nil && nm.Kind != ast.KindIdentifier {
			w.walk(nm)
		}
	}
	for _, p := range paramNodes {
		pd := p.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		if nm := pd.Name(); nm != nil {
			if names, ok := saRetPatternNames(nm); ok {
				for _, n := range names {
					w.bind(n)
				}
			}
		}
	}
}

// walkFuncLike walks a function-like node body in a fresh function
// scope (params bound, name NOT bound — method/function names are not
// bindings, except a named function expression, whose name binds
// inside its own scope).
func (w *saRetClosureWalk) walkFuncLike(n *ast.Node) {
	var params []*ast.Node
	var body *ast.Node
	var selfName string
	switch n.Kind {
	case ast.KindMethodDeclaration:
		if md := n.AsMethodDeclaration(); md != nil && md.Parameters != nil {
			params = md.Parameters.Nodes
		}
	case ast.KindArrowFunction:
		if af := n.AsArrowFunction(); af != nil && af.Parameters != nil {
			params = af.Parameters.Nodes
		}
	case ast.KindFunctionExpression:
		if fe := n.AsFunctionExpression(); fe != nil && fe.Parameters != nil {
			params = fe.Parameters.Nodes
		}
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			selfName = nm.Text()
		}
	case ast.KindFunctionDeclaration:
		if fn := n.AsFunctionDeclaration(); fn != nil && fn.Parameters != nil {
			params = fn.Parameters.Nodes
		}
	case ast.KindConstructor:
		if cd := n.AsConstructorDeclaration(); cd != nil && cd.Parameters != nil {
			params = cd.Parameters.Nodes
		}
	case ast.KindGetAccessor:
		if ga := n.AsGetAccessorDeclaration(); ga != nil && ga.Parameters != nil {
			params = ga.Parameters.Nodes
		}
	case ast.KindSetAccessor:
		if sa := n.AsSetAccessorDeclaration(); sa != nil && sa.Parameters != nil {
			params = sa.Parameters.Nodes
		}
	default:
		return
	}
	body = n.Body()
	if body == nil {
		return
	}
	w.push()
	if selfName != "" {
		w.bind(selfName)
	}
	w.bindFuncParams(params)
	w.walk(body)
	w.pop()
}

func (w *saRetClosureWalk) walk(n *ast.Node) {
	if n == nil || w.bad {
		return
	}
	switch n.Kind {
	case ast.KindIdentifier:
		w.checkIdent(n.Text())
	case ast.KindThisKeyword, ast.KindSuperKeyword:
		w.bad = true
	case ast.KindBlock:
		if bd := n.AsBlock(); bd != nil && bd.Statements != nil {
			w.push()
			for _, st := range bd.Statements.Nodes {
				w.walk(st)
			}
			w.pop()
		}
	case ast.KindVariableStatement:
		if vs := n.AsVariableStatement(); vs != nil && vs.DeclarationList != nil {
			w.walk(vs.DeclarationList.AsNode())
		}
	case ast.KindVariableDeclarationList:
		if dl := n.AsVariableDeclarationList(); dl != nil {
			for _, d := range dl.Declarations.Nodes {
				w.walk(d)
			}
		}
	case ast.KindVariableDeclaration:
		if vd := n.AsVariableDeclaration(); vd != nil {
			w.bindDecl(vd.Name(), vd.Initializer)
		}
	case ast.KindFunctionDeclaration:
		// The declaration name binds in the enclosing scope; the body
		// walks in its own function scope.
		if fn := n.AsFunctionDeclaration(); fn != nil {
			if nm := fn.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				w.bind(nm.Text())
			}
			w.walkFuncLike(n)
		}
	case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindMethodDeclaration,
		ast.KindConstructor, ast.KindGetAccessor, ast.KindSetAccessor:
		w.walkFuncLike(n)
	case ast.KindClassDeclaration, ast.KindClassExpression:
		if cd := n.AsClassDeclaration(); cd != nil {
			if nm := cd.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				w.bind(nm.Text())
			}
		}
		// Heritage expressions are references; members walk via their
		// own cases (methods as function scopes, properties below).
		n.ForEachChild(func(c *ast.Node) bool {
			if c.Kind == ast.KindIdentifier {
				// Class name already bound above; skip re-walking it.
				return !w.bad
			}
			w.walk(c)
			return !w.bad
		})
	case ast.KindPropertyDeclaration:
		if pd := n.AsPropertyDeclaration(); pd != nil {
			if nm := pd.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
				w.walk(nm)
			}
			if pd.Initializer != nil {
				w.walk(pd.Initializer)
			}
		}
	case ast.KindPropertyAccessExpression:
		// The property name is not a reference; only the object is.
		if pa := n.AsPropertyAccessExpression(); pa != nil {
			w.walk(pa.Expression)
		}
	case ast.KindElementAccessExpression:
		if ea := n.AsElementAccessExpression(); ea != nil {
			w.walk(ea.Expression)
			w.walk(ea.ArgumentExpression)
		}
	case ast.KindObjectLiteralExpression:
		if ol := n.AsObjectLiteralExpression(); ol != nil && ol.Properties != nil {
			for _, p := range ol.Properties.Nodes {
				switch p.Kind {
				case ast.KindPropertyAssignment:
					if pa := p.AsPropertyAssignment(); pa != nil {
						if nm := pa.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
							w.walk(nm)
						}
						w.walk(pa.Initializer)
					}
				case ast.KindShorthandPropertyAssignment:
					// Shorthand IS a reference to the same-named value.
					if sa := p.AsShorthandPropertyAssignment(); sa != nil {
						if nm := sa.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
							w.checkIdent(nm.Text())
						}
					}
				case ast.KindSpreadAssignment:
					if sp := p.AsSpreadAssignment(); sp != nil {
						w.walk(sp.Expression)
					}
				default:
					// Methods/getters/setters walk as function scopes.
					w.walk(p)
				}
			}
		}
	case ast.KindPropertyAssignment:
		if pa := n.AsPropertyAssignment(); pa != nil {
			if nm := pa.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
				w.walk(nm)
			}
			w.walk(pa.Initializer)
		}
	case ast.KindShorthandPropertyAssignment:
		if sa := n.AsShorthandPropertyAssignment(); sa != nil {
			if nm := sa.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				w.checkIdent(nm.Text())
			}
		}
	case ast.KindSpreadAssignment:
		if sp := n.AsSpreadAssignment(); sp != nil {
			w.walk(sp.Expression)
		}
	case ast.KindForStatement:
		if fs := n.AsForStatement(); fs != nil {
			w.push()
			w.walk(fs.Initializer)
			w.walk(fs.Condition)
			w.walk(fs.Incrementor)
			w.walk(fs.Statement)
			w.pop()
		}
	case ast.KindForInStatement, ast.KindForOfStatement:
		if fs := n.AsForInOrOfStatement(); fs != nil {
			w.push()
			w.walk(fs.Initializer)
			w.walk(fs.Expression)
			w.walk(fs.Statement)
			w.pop()
		}
	case ast.KindTryStatement:
		if ts := n.AsTryStatement(); ts != nil {
			if ts.TryBlock != nil {
				w.walk(ts.TryBlock)
			}
			if ts.CatchClause != nil {
				w.walk(ts.CatchClause)
			}
			if ts.FinallyBlock != nil {
				w.walk(ts.FinallyBlock)
			}
		}
	case ast.KindCatchClause:
		if cc := n.AsCatchClause(); cc != nil {
			w.push()
			if cc.VariableDeclaration != nil {
				if vd := cc.VariableDeclaration.AsVariableDeclaration(); vd != nil {
					w.bindDecl(vd.Name(), vd.Initializer)
				}
			}
			if cc.Block != nil {
				w.walk(cc.Block)
			}
			w.pop()
		}
	case ast.KindSwitchStatement:
		if ss := n.AsSwitchStatement(); ss != nil {
			// Switch cases share one block scope in JS.
			w.push()
			w.walk(ss.Expression)
			if ss.CaseBlock != nil {
				w.walk(ss.CaseBlock)
			}
			w.pop()
		}
	case ast.KindCaseBlock:
		if cb := n.AsCaseBlock(); cb != nil && cb.Clauses != nil {
			for _, cl := range cb.Clauses.Nodes {
				w.walk(cl)
			}
		}
	case ast.KindCaseClause, ast.KindDefaultClause:
		if cc := n.AsCaseOrDefaultClause(); cc != nil {
			w.walk(cc.Expression)
			if cc.Statements != nil {
				for _, st := range cc.Statements.Nodes {
					w.walk(st)
				}
			}
		}
	default:
		// Generic fallback: walk all children. Identifiers reached
		// here are treated as references (over-refusal, fail-closed);
		// bindings inside unhandled containers are never added, so no
		// reference can hide behind a missed scope.
		n.ForEachChild(func(c *ast.Node) bool {
			w.walk(c)
			return !w.bad
		})
	}
}

// paramNameSet builds a forbidden set from a defining function's
// parameter names (expression-body closure checks have no declared
// names to add).
func paramNameSet(paramNames []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range paramNames {
		if p != "" {
			out[p] = true
		}
	}
	return out
}

// saRetClassifyInit classifies a depth-0 const binding initializer into
// an entry, or reports no entry (unknown provenance, never an error).
// Identifier copies are NOT handled here: they need the walker's live
// entries map, so bindVarDecl routes through
// saRetClassifyInitWithEntries instead.
func saRetClassifyInit(init *ast.Node, annot *ast.TypeNode, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, litSeq *int) (saIfaceRetEntry, bool) {
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
	switch init.Kind {
	case ast.KindNewExpression:
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
	case ast.KindCallExpression:
		ce := init.AsCallExpression()
		if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
			return saIfaceRetEntry{}, false
		}
		sig, ok := funcs[ce.Expression.Text()]
		// Phase A (async v1): retIsPromise lift, coordinated with
		// the analysis lift (recorded prov that cannot be read is
		// worse than unrecorded prov).
		if !ok || sig.isVoid {
			return saIfaceRetEntry{}, false
		}
		if sig.retIfaceProv != "" {
			return saIfaceRetEntry{class: sig.retIfaceProv, kind: annotKindOrEmpty(annotName)}, true
		}
		if len(sig.retIfaceLitFn) > 0 {
			return saIfaceRetEntry{isLit: true, kind: annotKindOrEmpty(annotName), litFn: sig.retIfaceLitFn, litID: saRetCallLitID(ce.Expression.Text())}, true
		}
		return saIfaceRetEntry{}, false
	case ast.KindObjectLiteralExpression:
		if annotName == "" {
			return saIfaceRetEntry{}, false
		}
		table, ok := saRetBuildLitFn(init, annotName, classes)
		if !ok {
			return saIfaceRetEntry{}, false
		}
		*litSeq++
		return saIfaceRetEntry{isLit: true, kind: "inst:" + annotName, litFn: table, litID: *litSeq}, true
	}
	return saIfaceRetEntry{}, false
}

func annotKindOrEmpty(annotName string) string {
	if annotName == "" {
		return ""
	}
	return "inst:" + annotName
}

// saRetClassifyInitWithEntries classifies a binding initializer with
// access to the walker's current entries (needed for identifier copies).
// Copies are verbatim stores, so value identity is preserved: class
// entries copy as-is (isNew preserved: a verbatim copy of a new handle
// IS still the new handle), with the kind adjusted only when annotated;
// literal tables copy filtered to the effective interface's methods
// (the annotation, else the source entry's kind interface), with a fresh
// agreement id whenever the filter interface differs from the source
// entry's kind interface (a filtered table is not the same table).
func saRetClassifyInitWithEntries(init *ast.Node, annot *ast.TypeNode, entries map[string]saIfaceRetEntry, shadowed map[string]bool, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, litSeq *int, paramKinds map[string]string) (saIfaceRetEntry, bool) {
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
		name := init.Text()
		if shadowed != nil && shadowed[name] {
			return saIfaceRetEntry{}, false
		}
		src, ok := entries[name]
		if !ok {
			return saIfaceRetEntry{}, false
		}
		out := src
		if src.isLit {
			effIface := annotName
			srcIface := ""
			if len(src.kind) > 5 && src.kind[:5] == "inst:" {
				srcIface = src.kind[5:]
			}
			if effIface == "" {
				effIface = srcIface
			}
			if effIface == "" {
				return saIfaceRetEntry{}, false
			}
			idef, ok := classes[effIface]
			if !ok || idef == nil || !idef.isIface {
				return saIfaceRetEntry{}, false
			}
			filtered := map[string]*ast.Node{}
			for m, anode := range src.litFn {
				if _, isMeth := idef.methods[m]; isMeth {
					filtered[m] = anode
				}
			}
			out.litFn = filtered
			out.kind = "inst:" + effIface
			if effIface != srcIface {
				*litSeq++
				out.litID = *litSeq
			}
			return out, true
		}
		if annotName != "" {
			out.kind = "inst:" + annotName
		}
		return out, true
	}
	// Phase F: field-read initializers classify through the shared
	// field-read helper (current tables, i.e. prior joint rounds).
	// paramKinds resolves parameter receivers (walks never seed
	// params into entries); nil where the caller has no params.
	if init.Kind == ast.KindPropertyAccessExpression {
		if e, ok := saFieldReadEntry(init, annotName, entries, shadowed, classes, paramKinds); ok {
			return e, true
		}
	}
	return saRetClassifyInit(init, annot, funcs, classes, aliasOf, litSeq)
}

// saRetCallLitID maps a callee name to a stable negative agreement id for
// literal tables carried through call bindings. Negative ids never
// collide with the positive fresh ids of locally built tables.
func saRetCallLitID(callee string) int {
	h := 0
	for i := 0; i < len(callee); i++ {
		h = h*31 + int(callee[i])
	}
	if h < 0 {
		h = -h
	}
	return -(h + 1)
}

// saRetClassifyReturn classifies one return expression into a path.
// entries/shadowed are nil for arrow expression bodies (no bindings).
// forbidden is consulted only by the Phase R1 ObjectLiteral arm.
// litSeq is Phase R1 only: fresh id per literal (nil for callers that
// never classify a literal). All other arms ignore both.
func saRetClassifyReturn(expr *ast.Node, entries map[string]saIfaceRetEntry, shadowed map[string]bool, retKind, retIface string, funcs map[string]saFuncSig, classes map[string]*saClassDef, litSeq *int, forbidden map[string]bool) saIfaceRetPath {
	unknown := saIfaceRetPath{unknown: true}
	if expr == nil {
		return unknown
	}
	expr = saRetPeelParenNonNull(expr)
	if expr == nil {
		return unknown
	}
	// Phase A (async v1): return-site await peel (JEV peel_await).
	// In the sync-erasure subset, `return await X` classifies as
	// `return X`; this is what lets async wraps carrying a sync or
	// async call result record prov. Awaited shapes with no arm below
	// still yield unknown.
	if inner, saw := saPeelAwait(expr); saw && inner != nil {
		expr = saRetPeelParenNonNull(inner)
		if expr == nil {
			return unknown
		}
	}
	switch expr.Kind {
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
		if !saClassSatisfies(classes, cname, retIface) && !saClassStructurallySatisfies(classes, cname, retIface) {
			return unknown
		}
		return saIfaceRetPath{class: cname}
	case ast.KindIdentifier:
		name := expr.Text()
		if shadowed != nil && shadowed[name] {
			return unknown
		}
		e, ok := entries[name]
		if !ok {
			return unknown
		}
		if e.isLit {
			if e.kind == retKind {
				return saIfaceRetPath{isLit: true, litID: e.litID, litFn: e.litFn}
			}
			return unknown
		}
		if e.kind == retKind {
			// Verbatim passthrough: the value flows unchanged.
			return saIfaceRetPath{class: e.class}
		}
		// Widened-handle return: sound ONLY for a direct new binding
		// of a concrete class into a method-only satisfied interface
		// (see the soundness note at the top of this file).
		if e.isNew && e.kind == "inst:"+e.class && e.class != "" &&
			saIfaceMethodOnly(classes, retIface) &&
			(saClassSatisfies(classes, e.class, retIface) || saClassStructurallySatisfies(classes, e.class, retIface)) {
			return saIfaceRetPath{class: e.class}
		}
		return unknown
	case ast.KindObjectLiteralExpression:
		// Phase R1: fresh method-bearing literal in return position.
		// The table is built by the same builder as const-local
		// literals; the ONLY new gate is the closure check (a method
		// body referencing a defining-function param/local/this would
		// inline into caller scope). Methods-only in v1: a literal
		// with data properties builds no table (builder fails) and
		// yields unknown, so mixed shapes gain no prov — the
		// lowering/analysis asymmetry is intentional (a mixed
		// literal passes lowering but refuses at dispatch).
		// Data-only literals (no methods at all) also yield unknown
		// here: no litFn, nothing to record, and the return itself
		// lowers by the existing data path. litSeq gives each literal
		// a fresh id (mirroring the const-local binding name).
		table, litOK := saRetBuildLitFn(expr, retIface, classes)
		if !litOK || len(table) == 0 {
			return unknown
		}
		if !saRetLitFnClosureOK(table, forbidden) {
			return unknown
		}
		id := 0
		if litSeq != nil {
			id = *litSeq
			*litSeq++
		}
		return saIfaceRetPath{isLit: true, litID: id, litFn: table}
	case ast.KindCallExpression:
		ce := expr.AsCallExpression()
		if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
			return unknown
		}
		sig, ok := funcs[ce.Expression.Text()]
		// Phase A (async v1): retIsPromise lift, coordinated with
		// the analysis lift.
		if !ok || sig.isVoid {
			return unknown
		}
		// The call-return arm passes the handle directly only on layout
		// match (same interface); a different interface would take the
		// widen-with-join path and drop provenance.
		if sig.retKind != retKind {
			return unknown
		}
		if sig.retIfaceProv != "" {
			return saIfaceRetPath{class: sig.retIfaceProv}
		}
		if len(sig.retIfaceLitFn) > 0 {
			return saIfaceRetPath{isLit: true, litID: saRetCallLitID(ce.Expression.Text()), litFn: sig.retIfaceLitFn}
		}
		return unknown
	}
	return unknown
}

// saRetScanAssign records every assignment target in an expression into
// out. It recurses through common expression containers; nested function
// bodies are boundaries (their assignments cannot affect this scope's
// bindings), and unhandled expression kinds are opaque (a documented
// residual: assignments buried inside exotic expressions such as JSX are
// not detected, which requires already-invalid const reassignment inside
// an exotic expression to matter).
func saRetScanAssign(e *ast.Node, out map[string]bool) {
	if e == nil {
		return
	}
	switch e.Kind {
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be == nil {
			return
		}
		if saRetIsAssignOp(be.OperatorToken.Kind) {
			saRetAssignTarget(be.Left, out)
		}
		saRetScanAssign(be.Left, out)
		saRetScanAssign(be.Right, out)
	case ast.KindPrefixUnaryExpression:
		un := e.AsPrefixUnaryExpression()
		if un == nil {
			return
		}
		if un.Operator == ast.KindPlusPlusToken || un.Operator == ast.KindMinusMinusToken {
			if un.Operand != nil && un.Operand.Kind == ast.KindIdentifier {
				out[un.Operand.Text()] = true
			}
		}
		saRetScanAssign(un.Operand, out)
	case ast.KindPostfixUnaryExpression:
		un := e.AsPostfixUnaryExpression()
		if un == nil {
			return
		}
		if un.Operator == ast.KindPlusPlusToken || un.Operator == ast.KindMinusMinusToken {
			if un.Operand != nil && un.Operand.Kind == ast.KindIdentifier {
				out[un.Operand.Text()] = true
			}
		}
		saRetScanAssign(un.Operand, out)
	case ast.KindCallExpression:
		ce := e.AsCallExpression()
		if ce == nil {
			return
		}
		saRetScanAssign(ce.Expression, out)
		if ce.Arguments != nil {
			for _, a := range ce.Arguments.Nodes {
				saRetScanAssign(a, out)
			}
		}
	case ast.KindPropertyAccessExpression:
		pa := e.AsPropertyAccessExpression()
		if pa != nil {
			saRetScanAssign(pa.Expression, out)
		}
	case ast.KindElementAccessExpression:
		ea := e.AsElementAccessExpression()
		if ea != nil {
			saRetScanAssign(ea.Expression, out)
			saRetScanAssign(ea.ArgumentExpression, out)
		}
	case ast.KindParenthesizedExpression:
		if pe := e.AsParenthesizedExpression(); pe != nil {
			saRetScanAssign(pe.Expression, out)
		}
	case ast.KindNonNullExpression:
		if ne := e.AsNonNullExpression(); ne != nil {
			saRetScanAssign(ne.Expression, out)
		}
	case ast.KindAsExpression:
		if ae := e.AsAsExpression(); ae != nil {
			saRetScanAssign(ae.Expression, out)
		}
	case ast.KindSatisfiesExpression:
		if se := e.AsSatisfiesExpression(); se != nil {
			saRetScanAssign(se.Expression, out)
		}
	case ast.KindTypeAssertionExpression:
		if te := e.AsTypeAssertion(); te != nil {
			saRetScanAssign(te.Expression, out)
		}
	case ast.KindConditionalExpression:
		ce := e.AsConditionalExpression()
		if ce != nil {
			saRetScanAssign(ce.Condition, out)
			saRetScanAssign(ce.WhenTrue, out)
			saRetScanAssign(ce.WhenFalse, out)
		}
	case ast.KindArrayLiteralExpression:
		al := e.AsArrayLiteralExpression()
		if al != nil && al.Elements != nil {
			for _, el := range al.Elements.Nodes {
				saRetScanAssign(el, out)
			}
		}
	case ast.KindObjectLiteralExpression:
		ol := e.AsObjectLiteralExpression()
		if ol != nil && ol.Properties != nil {
			for _, p := range ol.Properties.Nodes {
				switch p.Kind {
				case ast.KindPropertyAssignment:
					if pa := p.AsPropertyAssignment(); pa != nil {
						saRetScanAssign(pa.Initializer, out)
					}
				case ast.KindSpreadAssignment:
					if sp := p.AsSpreadAssignment(); sp != nil {
						saRetScanAssign(sp.Expression, out)
					}
				case ast.KindShorthandPropertyAssignment:
					// A read, not a write.
				}
			}
		}
	case ast.KindNewExpression:
		ne := e.AsNewExpression()
		if ne != nil {
			saRetScanAssign(ne.Expression, out)
			if ne.Arguments != nil {
				for _, a := range ne.Arguments.Nodes {
					saRetScanAssign(a, out)
				}
			}
		}
	case ast.KindAwaitExpression:
		if ae := e.AsAwaitExpression(); ae != nil {
			saRetScanAssign(ae.Expression, out)
		}
	case ast.KindSpreadElement:
		if se := e.AsSpreadElement(); se != nil {
			saRetScanAssign(se.Expression, out)
		}
	case ast.KindTemplateExpression:
		if te := e.AsTemplateExpression(); te != nil && te.TemplateSpans != nil {
			for _, sp := range te.TemplateSpans.Nodes {
				if ts := sp.AsTemplateSpan(); ts != nil {
					saRetScanAssign(ts.Expression, out)
				}
			}
		}
	case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindClassExpression:
		// Nested function boundary: assignments inside cannot rebind
		// this scope's consts.
	default:
		// Opaque: no assignment targets extracted.
	}
}

// saRetIsAssignOp reports whether a binary operator token assigns.
func saRetIsAssignOp(k ast.Kind) bool {
	switch k {
	case ast.KindEqualsToken, ast.KindPlusEqualsToken, ast.KindMinusEqualsToken,
		ast.KindAsteriskEqualsToken, ast.KindAsteriskAsteriskEqualsToken,
		ast.KindSlashEqualsToken, ast.KindPercentEqualsToken,
		ast.KindAmpersandEqualsToken, ast.KindBarEqualsToken,
		ast.KindCaretEqualsToken, ast.KindLessThanLessThanEqualsToken,
		ast.KindGreaterThanGreaterThanEqualsToken,
		ast.KindGreaterThanGreaterThanGreaterThanEqualsToken,
		ast.KindAmpersandAmpersandEqualsToken, ast.KindBarBarEqualsToken,
		ast.KindQuestionQuestionEqualsToken:
		return true
	}
	return false
}

// saRetAssignTarget records the names a destructuring-or-identifier
// assignment target binds (plain identifier, or object/array pattern
// shorthand names, recursively).
func saRetAssignTarget(t *ast.Node, out map[string]bool) {
	if t == nil {
		return
	}
	switch t.Kind {
	case ast.KindIdentifier:
		out[t.Text()] = true
	case ast.KindObjectLiteralExpression:
		ol := t.AsObjectLiteralExpression()
		if ol == nil || ol.Properties == nil {
			return
		}
		for _, p := range ol.Properties.Nodes {
			switch p.Kind {
			case ast.KindShorthandPropertyAssignment:
				if nm := p.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
					out[nm.Text()] = true
				}
			case ast.KindPropertyAssignment:
				if pa := p.AsPropertyAssignment(); pa != nil {
					saRetAssignTarget(pa.Initializer, out)
				}
			case ast.KindSpreadAssignment:
				if sp := p.AsSpreadAssignment(); sp != nil {
					saRetAssignTarget(sp.Expression, out)
				}
			}
		}
	case ast.KindArrayLiteralExpression:
		al := t.AsArrayLiteralExpression()
		if al == nil || al.Elements == nil {
			return
		}
		for _, el := range al.Elements.Nodes {
			if el == nil || el.Kind == ast.KindOmittedExpression {
				continue
			}
			if el.Kind == ast.KindSpreadElement {
				if se := el.AsSpreadElement(); se != nil {
					saRetAssignTarget(se.Expression, out)
				}
				continue
			}
			saRetAssignTarget(el, out)
		}
	}
}

// saAttachIfaceRetProv attaches a callee's recorded return provenance to
// a const call-result binding. It is a no-op unless every gate holds:
// const binding, bare-identifier callee, same-file signature, same return
// kind as the bound kind. Phase A (async v1): async fn decls are included
// via retKind erasure (their recorded prov attaches like sync prov); the
// future machinery's own retIsPromise uses are untouched.
func saAttachIfaceRetProv(scope *saScope, ce *ast.CallExpression, name, vkind string, isConst bool) {
	if !isConst || ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return
	}
	sig, ok := scope.funcs[ce.Expression.Text()]
	if !ok || sig.isVoid || sig.retKind != vkind {
		return
	}
	if sig.retIfaceProv != "" {
		saSetIfaceProv(scope, name, sig.retIfaceProv)
		return
	}
	if len(sig.retIfaceLitFn) > 0 && len(vkind) > 5 && vkind[:5] == "inst:" {
		if idef, ok := scope.classes[vkind[5:]]; ok && idef.isIface {
			for m, anode := range sig.retIfaceLitFn {
				if _, isMeth := idef.methods[m]; isMeth {
					saSetIfaceLitFn(scope, name, m, anode)
				}
			}
		}
	}
}
