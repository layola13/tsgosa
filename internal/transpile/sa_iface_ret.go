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
func saAnalyzeReturnIfaceProv(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) {
	type bodyInfo struct {
		body     *ast.Node
		params   []string
		paramsOK bool
	}
	bodies := map[string]bodyInfo{}
	note := func(key string, body *ast.Node, paramNodes []*ast.Node) {
		if body == nil {
			return
		}
		params, ok := saRetParamNames(paramNodes)
		bodies[key] = bodyInfo{body: body, params: params, paramsOK: ok}
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
			if sig.isVoid || sig.retIsPromise || len(sig.retKind) <= 5 || sig.retKind[:5] != "inst:" {
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
			prov, litFn, has := saAnalyzeFuncReturns(bi.body, bi.params, sig.retKind, funcs, classes, aliasOf)
			if !has {
				continue
			}
			if prov != "" {
				sig.retIfaceProv = prov
			} else {
				sig.retIfaceLitFn = litFn
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

// saAnalyzeFuncReturns classifies every return path of one function body.
// body is a Block node (declarations) or an arrow body (Block or a single
// expression, which is one synthetic return path). Returns the agreed
// concrete class (prov) or literal-method table (litFn), or has=false.
func saAnalyzeFuncReturns(body *ast.Node, paramNames []string, retKind string, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode) (string, map[string]*ast.Node, bool) {
	retIface := retKind[5:]
	if body == nil {
		return "", nil, false
	}
	// Arrow expression bodies are a single synthetic return path with no
	// bindings: classify the expression directly.
	if body.Kind != ast.KindBlock {
		p := saRetClassifyReturn(body, nil, nil, retKind, retIface, funcs, classes)
		if p.unknown || p.isLit {
			// A literal-method expression body has no binding for the
			// call-site arms to attach to; v1 records class prov only
			// from expression bodies (litFn needs a const binding).
			return "", nil, false
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
	w := &saRetWalker{
		taint:    t,
		entries:  map[string]saIfaceRetEntry{},
		ok:       true,
		funcs:    funcs,
		classes:  classes,
		aliasOf:  aliasOf,
		retKind:  retKind,
		retIface: retIface,
		litSeq:   0,
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
	taint    *saRetTaint
	entries  map[string]saIfaceRetEntry
	paths    []saIfaceRetPath
	ok       bool
	funcs    map[string]saFuncSig
	classes  map[string]*saClassDef
	aliasOf  map[string]*ast.TypeNode
	retKind  string
	retIface string
	litSeq   int
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
				w.paths = append(w.paths, saRetClassifyReturn(rs.Expression, w.entries, shadowed, w.retKind, w.retIface, w.funcs, w.classes))
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
		if e, ok := saRetClassifyInitWithEntries(vd.Initializer, vd.Type, w.entries, nil, w.funcs, w.classes, w.aliasOf, &w.litSeq); ok {
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
		if !ok || sig.isVoid || sig.retIsPromise {
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
func saRetClassifyInitWithEntries(init *ast.Node, annot *ast.TypeNode, entries map[string]saIfaceRetEntry, shadowed map[string]bool, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, litSeq *int) (saIfaceRetEntry, bool) {
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
func saRetClassifyReturn(expr *ast.Node, entries map[string]saIfaceRetEntry, shadowed map[string]bool, retKind, retIface string, funcs map[string]saFuncSig, classes map[string]*saClassDef) saIfaceRetPath {
	unknown := saIfaceRetPath{unknown: true}
	if expr == nil {
		return unknown
	}
	expr = saRetPeelParenNonNull(expr)
	if expr == nil {
		return unknown
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
	case ast.KindCallExpression:
		ce := expr.AsCallExpression()
		if ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
			return unknown
		}
		sig, ok := funcs[ce.Expression.Text()]
		if !ok || sig.isVoid || sig.retIsPromise {
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
// kind as the bound kind, sync, non-promise. The existing dispatch site
// then handles method calls on the binding unchanged.
func saAttachIfaceRetProv(scope *saScope, ce *ast.CallExpression, name, vkind string, isConst bool) {
	if !isConst || ce == nil || ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return
	}
	sig, ok := scope.funcs[ce.Expression.Text()]
	if !ok || sig.isVoid || sig.retIsPromise || sig.retKind != vkind {
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
