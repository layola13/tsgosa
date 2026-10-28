package transpile

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// Phase P0 interface-parameter provenance (branch tsgosa/parallel/iface_params,
// JEV params_* decisions in ~/workspace/iface_params_design_probe.md).
//
// Problem: a free function taking an interface parameter has no provenance
// for that parameter, so every method call on it refuses with "X is an
// interface method with no body" even when every program-internal caller
// passes the same concrete class. This file adds a same-file, sync,
// emitter-only call-graph analysis: for each free function (top-level,
// namespace member, top-level arrow const) whose parameter kind is an
// interface, if EVERY collected call passes the same concrete class (or
// the same literal-method table) at that position, that provenance is
// recorded on the signature (paramIfaceProv / paramIfaceLitFn, parallel
// to paramKinds, mirroring the R0 retIfaceProv precedent). The body-entry
// installation in saLowerFunction / saLowerArrowConst then installs it on
// the parameter binding, and the existing dispatch site inlines the
// concrete method. No dispatch-site change, no sci change.
//
// Soundness argument (complete-or-poison caller enumeration, JEV
// params_edge_rule exactly_one UNANIMOUS, params_escape_rule
// poison_on_escape 0.98): exactly-one is sound only if every possible
// caller is enumerated as an edge or poisons prov. Collected call
// positions are top-level function bodies, namespace member bodies,
// top-level arrow const bodies, and top-level class method bodies
// (methods contribute edges INTO free functions but never receive prov
// themselves, JEV params_caller_contexts plus_methods). Every other
// position poisons: entry statements, local arrow / nested function /
// object-literal method bodies, class field initializers, alias and
// callback/value uses of a candidate name, calls with unresolvable
// callees, and calls omitting the parameter (defaulted params). Spurious
// edges from unexecuted functions are safe (fail-closed direction, same
// argument as R0's q_ret_stateful_id diag-drop).
//
// Cross-file eligibility (JEV params_xfile_eligibility
// import_graph_eligibility 0.84, params_file_scope same_file_only 0.64):
// a same-file pass cannot see cross-file callers, so a function is
// eligible only if no other file imports it (direct import, namespace
// import, or re-export chain, computed by saProgComputeImportedBy from
// the program graph before lowering). Entry-file exports are eligible
// under the same rule (JEV gate0 entry_eligible_same_rule 0.86): the
// host is outside the threat model, exactly as for R0.
//
// Fail-closed throughout: zero edges, multiple concretes, any unknown
// edge, any poison, imported callees, async callees, generic functions
// with type parameters in scope, methods/constructors as callees,
// rest/destructured/defaulted-omitted positions -> NO prov, loud refusal.
// This pass mutates only the funcs map (struct copies) and adds no
// refusals itself.

// saParamProv is one resolved parameter provenance value.
type saParamProv struct {
	class    string // concrete class ("" when lit)
	isLit    bool   // literal-method table (mutually exclusive with class)
	litFn    map[string]*ast.Node
	litID    int    // agreement id (pass-global fresh ids for local tables; saRetCallLitID for call results)
	litIface string // interface the litFn table was built/filtered for ("" when unknown)
}

// saParamEdgeKind classifies one collected call argument.
const (
	saParamEdgeUnknown = iota // unresolvable argument: blocks the parameter
	saParamEdgeClass          // concrete class value
	saParamEdgeLit            // literal-method table value
	saParamEdgeVar            // the caller's own parameter (fixpoint variable)
)

// saParamEdge is one collected argument at one interface parameter position.
type saParamEdge struct {
	kind     int
	class    string
	litFn    map[string]*ast.Node
	litID    int
	litIface string
	varKey   string // caller func key (saParamEdgeVar only)
	varIdx   int    // caller param index (saParamEdgeVar only)
}

// saParamInfo is one caller-body parameter position.
type saParamInfo struct {
	name       string // identifier name; "" for destructured/rest (no single name)
	isRest     bool
	hasDefault bool
	kind       string // lowering kind ("inst:X", "i32", ...)
	isIface    bool   // kind is an interface instance kind
	iface      string // interface name when isIface
}

// saParamCaller is one collected caller body.
type saParamCaller struct {
	key      string // funcs key ("" for method bodies, which never receive prov)
	body     *ast.Node
	params   []saParamInfo
	isMethod bool
}

// saParamPass is the whole-file P0 analysis state.
type saParamPass struct {
	funcs    map[string]saFuncSig
	classes  map[string]*saClassDef
	aliasOf  map[string]*ast.TypeNode
	link     *saFileLink
	litSeq   int // pass-global fresh lit ids (agreement is cross-body, unlike R0)
	edges    map[string]map[int][]saParamEdge
	poisoned map[string]map[int]bool
	// nsDottedToUnder maps a namespace member dotted call key ("N.M.f")
	// to its canonical funcs key ("N_M_f", the installation key).
	nsDottedToUnder map[string]string
	// candidates maps canonical funcs keys that may record prov: free
	// functions / ns members / top-level arrows with at least one
	// interface parameter, non-async, no type parameters in scope, with
	// a body. Callers need not be candidates (async/typeparam bodies
	// still contribute concrete edges; their param-var edges never
	// resolve, which blocks correctly).
	candidates map[string]bool
	// exportNames maps canonical funcs keys to the names under which the
	// function is importable ("" set = not importable = always eligible).
	// Namespace members use nsOuter instead (single outermost name).
	exportNames map[string][]string
	nsOuter     map[string]string
}

// saParamIfacePositions returns the interface parameter positions of a
// signature: kind "inst:I" where I is a recorded interface.
func (p *saParamPass) saParamIfacePositions(sig saFuncSig) []int {
	var out []int
	for i, k := range sig.paramKinds {
		if len(k) > 5 && k[:5] == "inst:" {
			if def, ok := p.classes[k[5:]]; ok && def != nil && def.isIface {
				out = append(out, i)
			}
		}
	}
	return out
}

// saParamPoisonAll poisons every interface position of a canonical key.
func (p *saParamPass) saParamPoisonAll(key string) {
	sig, ok := p.funcs[key]
	if !ok {
		return
	}
	for _, idx := range p.saParamIfacePositions(sig) {
		p.saParamPoisonOne(key, idx)
	}
}

// saParamPoisonOne poisons one interface position of a canonical key.
func (p *saParamPass) saParamPoisonOne(key string, idx int) {
	if p.poisoned[key] == nil {
		p.poisoned[key] = map[int]bool{}
	}
	p.poisoned[key][idx] = true
}

// saParamCanon maps a callee key to its canonical key (ns dotted -> under).
func (p *saParamPass) saParamCanon(key string) string {
	if under, ok := p.nsDottedToUnder[key]; ok {
		return under
	}
	return key
}

// saAnalyzeParamsIfaceProv is the P0 hook, called from saLowerSourceFile
// immediately AFTER R0's analysis (call-result args need retIfaceProv
// first) and before the main lowering loop. It mutates only the funcs
// map (param prov fields) and adds no refusals.
func saAnalyzeParamsIfaceProv(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, link *saFileLink) {
	if sf == nil || funcs == nil {
		return
	}
	p := &saParamPass{
		funcs:           funcs,
		classes:         classes,
		aliasOf:         aliasOf,
		link:            link,
		edges:           map[string]map[int][]saParamEdge{},
		poisoned:        map[string]map[int]bool{},
		nsDottedToUnder: map[string]string{},
		candidates:      map[string]bool{},
		exportNames:     map[string][]string{},
		nsOuter:         map[string]string{},
	}
	p.collectCallers(sf)
	p.fixpointAndRecord()
}

// saParamInfos builds per-position caller info from parameter nodes and
// the signature's parallel paramKinds. Length mismatch fails closed
// (caller treated as unanalyzable by the caller of this helper).
func (p *saParamPass) saParamInfos(nodes []*ast.Node, kinds []string) ([]saParamInfo, bool) {
	if len(nodes) != len(kinds) {
		return nil, false
	}
	out := make([]saParamInfo, len(nodes))
	for i, n := range nodes {
		if n == nil {
			return nil, false
		}
		pd := n.AsParameterDeclaration()
		if pd == nil {
			return nil, false
		}
		info := saParamInfo{kind: kinds[i]}
		if pd.DotDotDotToken != nil {
			info.isRest = true
		}
		if pd.Initializer != nil {
			info.hasDefault = true
		}
		if nm := pd.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			info.name = nm.Text()
		}
		if len(kinds[i]) > 5 && kinds[i][:5] == "inst:" {
			if def, ok := p.classes[kinds[i][5:]]; ok && def != nil && def.isIface {
				info.isIface = true
				info.iface = kinds[i][5:]
			}
		}
		out[i] = info
	}
	return out, true
}

// saParamMethodInfos builds name-only infos for method parameters
// (methods never receive prov, so kinds are irrelevant; names matter
// only for the local-reference check).
func saParamMethodInfos(nodes []*ast.Node) []saParamInfo {
	out := make([]saParamInfo, len(nodes))
	for i, n := range nodes {
		if n == nil {
			continue
		}
		pd := n.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		if nm := pd.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			out[i].name = nm.Text()
		}
	}
	return out
}

// saParamHasTypeParams reports whether a function-like node declares type
// parameters (generic functions with type parameters in scope get no
// prov, fail-closed).
func saParamHasTypeParams(fn *ast.Node) bool {
	if fn == nil {
		return false
	}
	switch fn.Kind {
	case ast.KindFunctionDeclaration:
		if fd := fn.AsFunctionDeclaration(); fd != nil && fd.TypeParameters != nil {
			return true
		}
	case ast.KindArrowFunction:
		if af := fn.AsArrowFunction(); af != nil && af.TypeParameters != nil {
			return true
		}
	case ast.KindFunctionExpression:
		if fe := fn.AsFunctionExpression(); fe != nil && fe.TypeParameters != nil {
			return true
		}
	case ast.KindMethodDeclaration:
		if md := fn.AsMethodDeclaration(); md != nil && md.TypeParameters != nil {
			return true
		}
	}
	return false
}

// saParamFnAsync reports whether a function-like declaration carries the
// async modifier (async callees get no prov, sync_only).
func saParamFnAsync(node *ast.Node) bool {
	if node == nil {
		return false
	}
	return ast.HasModifier(node, ast.ModifierFlagsAsync)
}

// saParamExportNames computes the importable names of a top-level
// function/arrow const: the export modifier name, "default" for default
// exports, export-list names mapping to the local, and CJS pair names.
// Empty means not importable (always eligible).
func saParamExportNames(stmts []*ast.Node, local string, st *ast.Node) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if st != nil && ast.HasModifier(st, ast.ModifierFlagsExport) {
		add(local)
	}
	if st != nil && ast.HasModifier(st, ast.ModifierFlagsDefault) {
		add("default")
	}
	for _, st2 := range stmts {
		if st2 == nil || st2.Kind != ast.KindExportDeclaration {
			continue
		}
		ed := st2.AsExportDeclaration()
		if ed == nil || ed.ModuleSpecifier != nil {
			continue
		}
		if edges, star := saProgLocalEdges(st2); !star {
			for exported, loc := range edges {
				if loc == local {
					add(exported)
				}
			}
		}
	}
	for _, st2 := range stmts {
		if pairs := saCJSExportPairs(st2); len(pairs) > 0 {
			for exported, loc := range pairs {
				if loc == local {
					add(exported)
				}
			}
		}
	}
	return out
}

// saParamCollectMethodCallers records method bodies of one class node as
// edge-contributing callers (methods never receive prov).
func (p *saParamPass) saParamCollectMethodCallers(classNode *ast.Node, callers *[]*saParamCaller) {
	if classNode == nil {
		return
	}
	var members []*ast.Node
	if cd := classNode.AsClassDeclaration(); cd != nil && cd.Members != nil {
		members = cd.Members.Nodes
	} else if ce := classNode.AsClassExpression(); ce != nil && ce.Members != nil {
		members = ce.Members.Nodes
	}
	if members == nil {
		return
	}
	for _, m := range members {
		if m == nil || m.Kind != ast.KindMethodDeclaration {
			continue
		}
		body := m.Body()
		if body == nil {
			continue
		}
		var nodes []*ast.Node
		if pl := m.ParameterList(); pl != nil {
			nodes = pl.Nodes
		}
		*callers = append(*callers, &saParamCaller{
			key:      "",
			body:     body,
			params:   saParamMethodInfos(nodes),
			isMethod: true,
		})
	}
}

// collectCallers walks the file's top-level statements, registering
// caller bodies and poison-scanning everything uncollected. It also
// fills the candidate set and eligibility keys.
func (p *saParamPass) collectCallers(sf *ast.SourceFile) {
	stmts := sf.AsSourceFile().Statements.Nodes
	var callers []*saParamCaller
	for _, st := range stmts {
		if st == nil {
			continue
		}
		switch st.Kind {
		case ast.KindFunctionDeclaration:
			fn := st.AsFunctionDeclaration()
			if fn == nil || st.Body() == nil {
				continue
			}
			name := ""
			if nm := fn.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				name = nm.Text()
			} else if ast.HasModifier(st, ast.ModifierFlagsDefault) {
				name = "default"
			} else {
				continue
			}
			sig, ok := p.funcs[name]
			if !ok {
				continue
			}
			var nodes []*ast.Node
			if fn.Parameters != nil {
				nodes = fn.Parameters.Nodes
			}
			infos, ok := p.saParamInfos(nodes, sig.paramKinds)
			if !ok {
				// Kind/name mismatch: fail closed, poison-scan the body.
				p.poisonNode(st.Body())
				continue
			}
			callers = append(callers, &saParamCaller{key: name, body: st.Body(), params: infos})
			if !saParamFnAsync(st) && !saParamHasTypeParams(st) && len(p.saParamIfacePositions(sig)) > 0 {
				p.candidates[name] = true
			}
			p.exportNames[name] = saParamExportNames(stmts, name, st)
		case ast.KindModuleDeclaration:
			members, ok := saFlattenNsMembers(st)
			if !ok {
				p.poisonNode(st)
				continue
			}
			for _, mb := range members {
				if mb.isConst {
					// Namespace const initializers are value positions:
					// a candidate reference there is an escape.
					if mb.decl != nil {
						if vd := mb.decl.AsVariableDeclaration(); vd != nil && vd.Initializer != nil {
							p.poisonNode(vd.Initializer)
						}
					}
					continue
				}
				if mb.isFunc {
					fn := mb.node.AsFunctionDeclaration()
					if fn == nil || mb.node.Body() == nil {
						continue
					}
					p.nsDottedToUnder[mb.dotted] = mb.under
					sig, ok := p.funcs[mb.under]
					if !ok {
						continue
					}
					var nodes []*ast.Node
					if fn.Parameters != nil {
						nodes = fn.Parameters.Nodes
					}
					infos, ok := p.saParamInfos(nodes, sig.paramKinds)
					if !ok {
						p.poisonNode(mb.node.Body())
						continue
					}
					callers = append(callers, &saParamCaller{key: mb.under, body: mb.node.Body(), params: infos})
					if !saParamFnAsync(mb.node) && !saParamHasTypeParams(mb.node) && len(p.saParamIfacePositions(sig)) > 0 {
						p.candidates[mb.under] = true
					}
					// Eligibility key: the outermost namespace name
					// (importing the namespace may call any member).
					outer := mb.dotted
					if i := strings.IndexByte(outer, '.'); i >= 0 {
						outer = outer[:i]
					}
					p.nsOuter[mb.under] = outer
					continue
				}
				// Namespace classes: their methods contribute edges.
				p.saParamCollectMethodCallers(mb.node, &callers)
			}
		case ast.KindVariableStatement:
			if name, arrow, ok := saIsTopLevelArrowConst(st); ok {
				sig, ok := p.funcs[name]
				if !ok || arrow == nil || arrow.Body() == nil {
					continue
				}
				var nodes []*ast.Node
				if pl := arrow.ParameterList(); pl != nil {
					nodes = pl.Nodes
				}
				infos, ok := p.saParamInfos(nodes, sig.paramKinds)
				if !ok {
					p.poisonNode(arrow.Body())
					continue
				}
				callers = append(callers, &saParamCaller{key: name, body: arrow.Body(), params: infos})
				if !saParamFnAsync(arrow) && !saParamHasTypeParams(arrow) && len(p.saParamIfacePositions(sig)) > 0 {
					p.candidates[name] = true
				}
				p.exportNames[name] = saParamExportNames(stmts, name, st)
				continue
			}
			// Any other top-level const: initializers are module-level
			// value positions (alias escapes poison).
			if vs := st.AsVariableStatement(); vs != nil && vs.DeclarationList != nil {
				if dl := vs.DeclarationList.AsVariableDeclarationList(); dl != nil {
					for _, d := range dl.Declarations.Nodes {
						if vd := d.AsVariableDeclaration(); vd != nil && vd.Initializer != nil {
							p.poisonNode(vd.Initializer)
						}
					}
				}
			}
		case ast.KindClassDeclaration:
			p.saParamCollectMethodCallers(st, &callers)
			// Field initializers, heritage, static blocks: uncollected.
			if cd := st.AsClassDeclaration(); cd != nil && cd.Members != nil {
				for _, m := range cd.Members.Nodes {
					if m == nil {
						continue
					}
					switch m.Kind {
					case ast.KindPropertyDeclaration:
						if pd := m.AsPropertyDeclaration(); pd != nil {
							if nm := m.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
								p.poisonNode(nm)
							}
							if pd.Initializer != nil {
								p.poisonNode(pd.Initializer)
							}
						}
					case ast.KindMethodDeclaration:
						// Collected above; skip (its body is a caller).
					default:
						// Static blocks and other members: uncollected.
						p.poisonNode(m)
					}
				}
			}
			if cd := st.AsClassDeclaration(); cd != nil {
				// Heritage expressions (extends clauses are values).
				if cd.HeritageClauses != nil {
					for _, h := range cd.HeritageClauses.Nodes {
						p.poisonNode(h)
					}
				}
			}
		case ast.KindExportAssignment:
			// `export = <expr>`: the expression is a value escape.
			if ea := st.AsExportAssignment(); ea != nil && ea.Expression != nil {
				p.poisonNode(ea.Expression)
			}
		default:
			if saIsEntryStmt(st) {
				p.poisonNode(st)
			}
			// Imports, exports, types, enums, interfaces: no value
			// positions (export lists are eligibility, not poison).
		}
	}
	// Walk every collected caller body.
	for _, c := range callers {
		p.walkCaller(c)
	}
}

// saParamBodyWalk is the ordered per-caller walk (mirrors R0's
// saRetWalker statement recursion, plus call/identifier scanning).
type saParamBodyWalk struct {
	p       *saParamPass
	caller  *saParamCaller
	taint   *saRetTaint
	entries map[string]saIfaceRetEntry
	scan    *saParamScan
	// shadowed is the current statement list's shadow set, maintained
	// by walkList (saved/restored across nested lists) so expression
	// scanning resolves names against the right scope.
	shadowed map[string]bool
}

// walkCaller walks one collected caller body. Unanalyzable bodies
// (taint unknown) poison-scan wholesale instead of contributing edges.
func (p *saParamPass) walkCaller(c *saParamCaller) {
	if c.body == nil {
		return
	}
	// Arrow expression bodies: a single expression, no bindings.
	if c.body.Kind != ast.KindBlock {
		t := &saRetTaint{counts: map[string]int{}, assigned: map[string]bool{}}
		for _, info := range c.params {
			if info.name != "" {
				t.counts[info.name]++
			}
		}
		saRetScanAssign(c.body, t.assigned)
		w := &saParamBodyWalk{p: p, caller: c, taint: t, entries: map[string]saIfaceRetEntry{}}
		w.scan = &saParamScan{p: p, w: w}
		w.scan.expr(c.body)
		return
	}
	bd := c.body.AsBlock()
	if bd == nil || bd.Statements == nil {
		return
	}
	stmts := bd.Statements.Nodes
	t := &saRetTaint{counts: map[string]int{}, assigned: map[string]bool{}}
	for _, info := range c.params {
		if info.name != "" {
			t.counts[info.name]++
		}
	}
	saRetTaintStmts(stmts, t)
	if t.unknown {
		// Unwhitelisted statements (for-in/of, switch, ...): the whole
		// body is uncollected; every candidate call/ref inside poisons.
		p.poisonNode(c.body)
		return
	}
	w := &saParamBodyWalk{p: p, caller: c, taint: t, entries: map[string]saIfaceRetEntry{}}
	w.scan = &saParamScan{p: p, w: w}
	w.walkList(stmts, 0, nil)
}

// isLocal reports whether a name in this caller body refers to a local
// binding (parameter, depth-0 const entry, or shadowed nested name)
// rather than a top-level candidate.
func (w *saParamBodyWalk) isLocal(name string, shadowed map[string]bool) bool {
	if shadowed != nil && shadowed[name] {
		return true
	}
	if _, ok := w.entries[name]; ok {
		return true
	}
	for _, info := range w.caller.params {
		if info.name == name {
			return true
		}
	}
	return false
}

// walkList mirrors R0's saRetWalker.walkList statement recursion, with
// expression scanning at every statement for calls and identifier refs.
func (w *saParamBodyWalk) walkList(stmts []*ast.Node, depth int, shadowed map[string]bool) {
	if depth > 0 {
		shadowed = saRetCopyShadow(shadowed)
		for n := range saRetDirectDeclNames(stmts) {
			shadowed[n] = true
		}
	}
	// The scanner resolves names against this list's shadow set.
	prev := w.shadowed
	w.shadowed = shadowed
	defer func() { w.shadowed = prev }()
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
			vs := st.AsVariableStatement()
			if vs == nil || vs.DeclarationList == nil {
				continue
			}
			dl := vs.DeclarationList.AsVariableDeclarationList()
			if dl == nil {
				continue
			}
			isConst := vs.DeclarationList.AsNode() != nil && vs.DeclarationList.AsNode().Flags&ast.NodeFlagsConst != 0
			for _, d := range dl.Declarations.Nodes {
				vd := d.AsVariableDeclaration()
				if vd == nil {
					continue
				}
				// Scan the initializer for calls BEFORE binding
				// (TDZ: the binding is not visible inside its own
				// initializer).
				if vd.Initializer != nil {
					w.scan.expr(vd.Initializer)
				}
				if depth != 0 || !isConst {
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
				if e, ok := saRetClassifyInitWithEntries(vd.Initializer, vd.Type, w.entries, nil, w.p.funcs, w.p.classes, w.p.aliasOf, &w.p.litSeq); ok {
					w.entries[name] = e
				}
			}
		case ast.KindReturnStatement:
			if rs := st.AsReturnStatement(); rs != nil && rs.Expression != nil {
				w.scan.expr(rs.Expression)
			}
		case ast.KindExpressionStatement:
			if es := st.AsExpressionStatement(); es != nil && es.Expression != nil {
				w.scan.expr(es.Expression)
			}
		case ast.KindThrowStatement:
			if ts := st.AsThrowStatement(); ts != nil && ts.Expression != nil {
				w.scan.expr(ts.Expression)
			}
		case ast.KindIfStatement:
			if is := st.AsIfStatement(); is != nil {
				if is.Expression != nil {
					w.scan.expr(is.Expression)
				}
				if thenStmts, ok := saEmbeddedBlock(is.ThenStatement); ok {
					w.walkList(thenStmts, depth+1, shadowed)
				}
				if elseStmts, ok := saEmbeddedBlock(is.ElseStatement); ok {
					w.walkList(elseStmts, depth+1, shadowed)
				}
			}
		case ast.KindWhileStatement:
			if ws := st.AsWhileStatement(); ws != nil {
				if ws.Expression != nil {
					w.scan.expr(ws.Expression)
				}
				if bodyStmts, ok := saEmbeddedBlock(ws.Statement); ok {
					w.walkList(bodyStmts, depth+1, shadowed)
				}
			}
		case ast.KindDoStatement:
			if ds := st.AsDoStatement(); ds != nil {
				if bodyStmts, ok := saEmbeddedBlock(ds.Statement); ok {
					w.walkList(bodyStmts, depth+1, shadowed)
				}
				if ds.Expression != nil {
					w.scan.expr(ds.Expression)
				}
			}
		case ast.KindForStatement:
			if fs := st.AsForStatement(); fs != nil {
				sub := saRetCopyShadow(shadowed)
				if fs.Initializer != nil {
					if fs.Initializer.Kind == ast.KindVariableDeclarationList {
						if dl := fs.Initializer.AsVariableDeclarationList(); dl != nil {
							for _, d := range dl.Declarations.Nodes {
								vd := d.AsVariableDeclaration()
								if vd == nil {
									continue
								}
								if vd.Initializer != nil {
									w.scan.expr(vd.Initializer)
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
					} else {
						w.scan.expr(fs.Initializer)
					}
				}
				if fs.Condition != nil {
					w.scan.expr(fs.Condition)
				}
				if fs.Incrementor != nil {
					w.scan.expr(fs.Incrementor)
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
							if vd := cc.VariableDeclaration.AsVariableDeclaration(); vd != nil {
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
		case ast.KindFunctionDeclaration, ast.KindClassDeclaration:
			// Nested function/class boundary: uncollected, poison-scan.
			w.p.poisonNode(st)
		case ast.KindBreakStatement, ast.KindContinueStatement,
			ast.KindEmptyStatement, ast.KindDebuggerStatement:
			// No expressions.
		default:
			// Unreachable in analyzable bodies (taint flags these),
			// but stay fail-closed: poison-scan.
			w.p.poisonNode(st)
		}
	}
}

// saParamScan is the shared expression scanner. In edge mode (w != nil)
// calls are classified into edges and identifier refs are checked
// against locals; in poison mode (w == nil) every candidate call/ref
// poisons.
type saParamScan struct {
	p *saParamPass
	w *saParamBodyWalk // nil in poison mode
}

// expr scans one expression node for calls and identifier refs.
func (s *saParamScan) expr(e *ast.Node) {
	if e == nil {
		return
	}
	switch e.Kind {
	case ast.KindIdentifier:
		s.ident(e)
		return
	case ast.KindCallExpression:
		s.call(e)
		return
	case ast.KindPropertyAccessExpression:
		// Receiver only; the Name is structural, never a reference.
		if pa := e.AsPropertyAccessExpression(); pa != nil && pa.Expression != nil {
			s.expr(pa.Expression)
		}
		return
	case ast.KindPropertyAssignment:
		if pa := e.AsPropertyAssignment(); pa != nil {
			if nm := e.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
				s.expr(nm)
			}
			if pa.Initializer != nil {
				s.expr(pa.Initializer)
			}
		}
		return
	case ast.KindShorthandPropertyAssignment:
		// The name IS a value reference.
		if nm := e.Name(); nm != nil {
			s.ident(nm)
		}
		if sa := e.AsShorthandPropertyAssignment(); sa != nil && sa.ObjectAssignmentInitializer != nil {
			s.expr(sa.ObjectAssignmentInitializer)
		}
		return
	case ast.KindArrowFunction, ast.KindFunctionExpression,
		ast.KindFunctionDeclaration, ast.KindClassDeclaration,
		ast.KindClassExpression, ast.KindMethodDeclaration,
		ast.KindGetAccessor, ast.KindSetAccessor:
		// Boundary: interiors are uncollected in both modes.
		s.p.poisonNode(e)
		return
	case ast.KindJsxAttribute:
		// Attribute names are not references; initializers are.
		if ja := e.AsJsxAttribute(); ja != nil && ja.Initializer != nil {
			s.expr(ja.Initializer)
		}
		return
	default:
		e.ForEachChild(func(c *ast.Node) bool {
			s.expr(c)
			return false
		})
	}
}

// ident handles one identifier in expression position.
func (s *saParamScan) ident(id *ast.Node) {
	if id == nil {
		return
	}
	name := id.Text()
	if s.w == nil {
		// Poison mode: any candidate name poisons (over-poisoning
		// shadowed locals is sound).
		if _, ok := s.p.funcs[name]; ok {
			s.p.saParamPoisonAll(s.p.saParamCanon(name))
		}
		return
	}
	// Edge mode: locals (parameters, entries, shadowed names) are not
	// candidate references. Tainted names are ambiguous: poison if
	// they match a candidate (fail-closed).
	if s.w.isLocal(name, s.w.shadowed) {
		return
	}
	if s.w.taint.tainted(name) {
		if _, ok := s.p.funcs[name]; ok {
			s.p.saParamPoisonAll(s.p.saParamCanon(name))
		}
		return
	}
	if _, ok := s.p.funcs[name]; ok {
		s.p.saParamPoisonAll(s.p.saParamCanon(name))
	}
}

// resolveCallee maps a call's callee to a canonical funcs key, or ""
// when the call is not a same-file free-function/ns call. The caller
// (edge mode) applies local-shadowing checks before calling this.
func (p *saParamPass) resolveCallee(ce *ast.CallExpression) string {
	if ce == nil || ce.Expression == nil {
		return ""
	}
	callee := saRetPeelParenNonNull(ce.Expression)
	if callee == nil {
		return ""
	}
	switch callee.Kind {
	case ast.KindIdentifier:
		name := callee.Text()
		if _, ok := p.funcs[name]; ok {
			return p.saParamCanon(name)
		}
		return ""
	case ast.KindPropertyAccessExpression:
		pa := callee.AsPropertyAccessExpression()
		if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			return ""
		}
		var dotted string
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			dotted = pa.Expression.Text() + "." + pa.Name().Text()
		} else if d, _, _, ok := saNsCallChain(callee); ok {
			dotted = d
		} else {
			return ""
		}
		if _, ok := p.funcs[dotted]; ok {
			return p.saParamCanon(dotted)
		}
		return ""
	default:
		return ""
	}
}

// call handles one call expression.
func (s *saParamScan) call(e *ast.Node) {
	ce := e.AsCallExpression()
	if ce == nil {
		return
	}
	var args []*ast.Node
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if s.w == nil {
		// Poison mode: candidate calls poison all their parameters.
		if key := s.p.resolveCallee(ce); key != "" {
			s.p.saParamPoisonAll(key)
		}
	} else {
		// Edge mode: local shadowing disqualifies the edge (the call
		// goes to the local, not the candidate).
		if key := s.edgeCalleeKey(ce); key != "" {
			s.recordCall(key, args)
		}
	}
	// Recurse into callee and arguments. A bare-identifier callee is
	// the call itself, never a reference; a property-access callee
	// contributes only its receiver expression.
	callee := saRetPeelParenNonNull(ce.Expression)
	if callee != nil {
		switch callee.Kind {
		case ast.KindIdentifier:
			// Skip: the call, not a reference.
		case ast.KindPropertyAccessExpression:
			if pa := callee.AsPropertyAccessExpression(); pa != nil && pa.Expression != nil {
				s.expr(pa.Expression)
			}
		default:
			s.expr(callee)
		}
	}
	for _, a := range args {
		s.expr(a)
	}
}

// edgeCalleeKey resolves a call's callee for edge collection, applying
// the walker's local-shadowing checks. "" means "not an edge".
func (s *saParamScan) edgeCalleeKey(ce *ast.CallExpression) string {
	if ce == nil || ce.Expression == nil {
		return ""
	}
	callee := saRetPeelParenNonNull(ce.Expression)
	if callee == nil {
		return ""
	}
	// The walker's current shadowed set (maintained by walkList)
	// disqualifies the edge: the call goes to the local, not the
	// candidate.
	switch callee.Kind {
	case ast.KindIdentifier:
		name := callee.Text()
		if s.w.isLocal(name, s.w.shadowed) || s.w.taint.tainted(name) {
			return ""
		}
		if _, ok := s.p.funcs[name]; ok {
			return s.p.saParamCanon(name)
		}
		return ""
	case ast.KindPropertyAccessExpression:
		pa := callee.AsPropertyAccessExpression()
		if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			return ""
		}
		// Root identifier locally declared => method call on a local.
		var dotted string
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			root := pa.Expression.Text()
			if s.w.isLocal(root, s.w.shadowed) || s.w.taint.tainted(root) {
				return ""
			}
			dotted = root + "." + pa.Name().Text()
		} else if d, _, _, ok := saNsCallChain(callee); ok {
			dotted = d
		} else {
			return ""
		}
		if _, ok := s.p.funcs[dotted]; ok {
			return s.p.saParamCanon(dotted)
		}
		return ""
	default:
		return ""
	}
}

// recordCall classifies one collected call's arguments into edges (or
// poison) for the callee's interface parameter positions.
func (s *saParamScan) recordCall(key string, args []*ast.Node) {
	sig, ok := s.p.funcs[key]
	if !ok {
		return
	}
	ifacePos := s.p.saParamIfacePositions(sig)
	if len(ifacePos) == 0 {
		return
	}
	// First spread position: arguments at or after it are unknowable.
	firstSpread := -1
	for i, a := range args {
		if a != nil && a.Kind == ast.KindSpreadElement {
			firstSpread = i
			break
		}
	}
	for _, idx := range ifacePos {
		if firstSpread >= 0 && idx >= firstSpread {
			s.addEdge(key, idx, saParamEdge{kind: saParamEdgeUnknown})
			continue
		}
		if idx >= len(args) {
			// Omitted argument: poisons this parameter (v1 rule for
			// defaulted/optional positions).
			s.p.saParamPoisonOne(key, idx)
			continue
		}
		s.addEdge(key, idx, s.w.classifyArg(args[idx], sig.paramKinds[idx]))
	}
}

// addEdge appends one edge for a callee parameter position.
func (s *saParamScan) addEdge(key string, idx int, e saParamEdge) {
	if s.p.edges[key] == nil {
		s.p.edges[key] = map[int][]saParamEdge{}
	}
	s.p.edges[key][idx] = append(s.p.edges[key][idx], e)
}

// classifyArg classifies one argument expression against one interface
// parameter kind. Anything unlisted is unknown (blocks the parameter).
func (w *saParamBodyWalk) classifyArg(arg *ast.Node, paramKind string) saParamEdge {
	unknown := saParamEdge{kind: saParamEdgeUnknown}
	if arg == nil {
		return unknown
	}
	arg = saRetPeelParenNonNull(arg)
	if arg == nil {
		return unknown
	}
	switch arg.Kind {
	case ast.KindNewExpression:
		ne := arg.AsNewExpression()
		if ne == nil || ne.Expression == nil || ne.Expression.Kind != ast.KindIdentifier {
			return unknown
		}
		return saParamEdge{kind: saParamEdgeClass, class: ne.Expression.Text()}
	case ast.KindIdentifier:
		name := arg.Text()
		if w.taint.tainted(name) {
			return unknown
		}
		// Shadowed names refer to nested bindings, not outer entries.
		if w.shadowed != nil && w.shadowed[name] {
			return unknown
		}
		if entry, ok := w.entries[name]; ok {
			if entry.isLit {
				litIface := ""
				if len(entry.kind) > 5 && entry.kind[:5] == "inst:" {
					litIface = entry.kind[5:]
				}
				return saParamEdge{kind: saParamEdgeLit, litFn: entry.litFn, litID: entry.litID, litIface: litIface}
			}
			return saParamEdge{kind: saParamEdgeClass, class: entry.class}
		}
		if w.caller.isMethod {
			// Method parameters never carry prov (design-literal).
			return unknown
		}
		for i, info := range w.caller.params {
			if info.name == name {
				if info.isIface {
					return saParamEdge{kind: saParamEdgeVar, varKey: w.caller.key, varIdx: i}
				}
				if len(info.kind) > 5 && info.kind[:5] == "inst:" {
					if def, ok := w.p.classes[info.kind[5:]]; ok && def != nil && !def.isIface {
						return saParamEdge{kind: saParamEdgeClass, class: info.kind[5:]}
					}
				}
				return unknown
			}
		}
		return unknown
	case ast.KindCallExpression:
		ce := arg.AsCallExpression()
		if ce == nil {
			return unknown
		}
		calleeKey := w.scan.edgeCalleeKey(ce)
		if calleeKey == "" {
			return unknown
		}
		sig, ok := w.p.funcs[calleeKey]
		if !ok || sig.retIsPromise {
			return unknown
		}
		if sig.retKind == paramKind {
			if sig.retIfaceProv != "" {
				return saParamEdge{kind: saParamEdgeClass, class: sig.retIfaceProv}
			}
			if len(sig.retIfaceLitFn) > 0 {
				iface := ""
				if len(paramKind) > 5 && paramKind[:5] == "inst:" {
					iface = paramKind[5:]
				}
				return saParamEdge{kind: saParamEdgeLit, litFn: sig.retIfaceLitFn, litID: saRetCallLitID(calleeKey), litIface: iface}
			}
			return unknown
		}
		if len(sig.retKind) > 5 && sig.retKind[:5] == "inst:" {
			if def, ok := w.p.classes[sig.retKind[5:]]; ok && def != nil && !def.isIface {
				return saParamEdge{kind: saParamEdgeClass, class: sig.retKind[5:]}
			}
		}
		return unknown
	default:
		// Object literals never contribute prov (data literals carry
		// no dispatch; method-bearing literals refuse at the call
		// site). Await, property/element access, and everything else
		// is unresolvable here.
		return unknown
	}
}

// poisonNode scans one node as an uncollected context: every candidate
// call or candidate-name reference poisons all of that candidate's
// interface parameters. Declaration names, property names/keys, and
// type positions are structural and never fire.
func (p *saParamPass) poisonNode(n *ast.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindIdentifier:
		if _, ok := p.funcs[n.Text()]; ok {
			p.saParamPoisonAll(p.saParamCanon(n.Text()))
		}
		return
	case ast.KindCallExpression:
		ce := n.AsCallExpression()
		if ce != nil {
			if key := p.resolveCallee(ce); key != "" {
				p.saParamPoisonAll(key)
			}
			// Recurse into callee (bare identifier skipped: it is
			// the call, not a reference) and arguments.
			callee := saRetPeelParenNonNull(ce.Expression)
			if callee != nil {
				switch callee.Kind {
				case ast.KindIdentifier:
				case ast.KindPropertyAccessExpression:
					if pa := callee.AsPropertyAccessExpression(); pa != nil && pa.Expression != nil {
						p.poisonNode(pa.Expression)
					}
				default:
					p.poisonNode(callee)
				}
			}
			if ce.Arguments != nil {
				for _, a := range ce.Arguments.Nodes {
					p.poisonNode(a)
				}
			}
		}
		return
	case ast.KindPropertyAccessExpression:
		if pa := n.AsPropertyAccessExpression(); pa != nil && pa.Expression != nil {
			p.poisonNode(pa.Expression)
		}
		return
	case ast.KindPropertyAssignment:
		if pa := n.AsPropertyAssignment(); pa != nil {
			if nm := n.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
				p.poisonNode(nm)
			}
			if pa.Initializer != nil {
				p.poisonNode(pa.Initializer)
			}
		}
		return
	case ast.KindShorthandPropertyAssignment:
		if nm := n.Name(); nm != nil {
			p.poisonNode(nm)
		}
		if sa := n.AsShorthandPropertyAssignment(); sa != nil && sa.ObjectAssignmentInitializer != nil {
			p.poisonNode(sa.ObjectAssignmentInitializer)
		}
		return
	case ast.KindVariableDeclaration:
		if vd := n.AsVariableDeclaration(); vd != nil && vd.Initializer != nil {
			p.poisonNode(vd.Initializer)
		}
		return
	case ast.KindParameter:
		if pd := n.AsParameterDeclaration(); pd != nil && pd.Initializer != nil {
			p.poisonNode(pd.Initializer)
		}
		return
	case ast.KindFunctionDeclaration, ast.KindArrowFunction, ast.KindFunctionExpression:
		if pl := n.ParameterList(); pl != nil {
			for _, prm := range pl.Nodes {
				p.poisonNode(prm)
			}
		}
		if body := n.Body(); body != nil {
			p.poisonNode(body)
		}
		return
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
			p.poisonNode(nm)
		}
		if pl := n.ParameterList(); pl != nil {
			for _, prm := range pl.Nodes {
				p.poisonNode(prm)
			}
		}
		if body := n.Body(); body != nil {
			p.poisonNode(body)
		}
		return
	case ast.KindPropertyDeclaration:
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
			p.poisonNode(nm)
		}
		if pd := n.AsPropertyDeclaration(); pd != nil && pd.Initializer != nil {
			p.poisonNode(pd.Initializer)
		}
		return
	case ast.KindBreakStatement, ast.KindContinueStatement:
		// Labels are not value references.
		return
	case ast.KindImportDeclaration, ast.KindExportDeclaration,
		ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration,
		ast.KindEnumDeclaration:
		// No value positions (export lists are eligibility, not poison).
		return
	case ast.KindJsxAttribute:
		if ja := n.AsJsxAttribute(); ja != nil && ja.Initializer != nil {
			p.poisonNode(ja.Initializer)
		}
		return
	case ast.KindLabeledStatement:
		if ls := n.AsLabeledStatement(); ls != nil && ls.Statement != nil {
			p.poisonNode(ls.Statement)
		}
		return
	default:
		n.ForEachChild(func(c *ast.Node) bool {
			p.poisonNode(c)
			return false
		})
	}
}

// saParamEligible reports whether a canonical funcs key may record prov
// under the import-graph eligibility rule: no other file imports it
// (direct, namespace, or re-export chain). A nil link or nil importedBy
// map (single-file lowering) means no importers.
func (p *saParamPass) saParamEligible(key string) bool {
	if p.link == nil || p.link.importedBy == nil {
		return true
	}
	self := p.link.self
	byFile, ok := p.link.importedBy[self]
	if !ok {
		return true
	}
	importedByOther := func(name string) bool {
		for importer := range byFile[name] {
			if importer != self {
				return true
			}
		}
		return false
	}
	if outer, isNs := p.nsOuter[key]; isNs {
		// Namespace members: importing the outermost namespace (or a
		// star import of the file) may call any member.
		return !importedByOther(outer) && !importedByOther("*")
	}
	names := p.exportNames[key]
	if len(names) == 0 {
		// Not importable: no importer can call it.
		return true
	}
	for _, name := range names {
		if importedByOther(name) {
			return false
		}
	}
	return !importedByOther("*")
}

// saParamState is one fixpoint state per (function, parameter position).
const (
	saParamStateUnresolved = iota
	saParamStateResolved
	saParamStateBlocked
)

type saParamFixState struct {
	state int
	prov  saParamProv
}

// fixpointAndRecord resolves param-var edges to a fixpoint, then records
// prov on eligible, unpoisoned, exactly-one-agreeing parameters.
func (p *saParamPass) fixpointAndRecord() {
	type pair struct {
		key string
		idx int
	}
	states := map[pair]*saParamFixState{}
	var pairs []pair
	for key := range p.candidates {
		sig := p.funcs[key]
		for _, idx := range p.saParamIfacePositions(sig) {
			pr := pair{key, idx}
			states[pr] = &saParamFixState{state: saParamStateUnresolved}
			pairs = append(pairs, pr)
		}
	}
	// Zero-edge parameters and poisoned parameters block immediately.
	for _, pr := range pairs {
		if p.poisoned[pr.key] != nil && p.poisoned[pr.key][pr.idx] {
			states[pr].state = saParamStateBlocked
			continue
		}
		if len(p.edges[pr.key][pr.idx]) == 0 {
			states[pr].state = saParamStateBlocked
		}
	}
	maxRounds := len(pairs) + 1
	for round := 0; round < maxRounds; round++ {
		changed := false
		for _, pr := range pairs {
			st := states[pr]
			if st.state != saParamStateUnresolved {
				continue
			}
			edges := p.edges[pr.key][pr.idx]
			blocked := false
			unresolved := false
			var agreed *saParamProv
			for _, e := range edges {
				var cur saParamProv
				switch e.kind {
				case saParamEdgeUnknown:
					blocked = true
				case saParamEdgeClass:
					cur = saParamProv{class: e.class}
				case saParamEdgeLit:
					cur = saParamProv{isLit: true, litFn: e.litFn, litID: e.litID, litIface: e.litIface}
				case saParamEdgeVar:
					ref := states[pair{e.varKey, e.varIdx}]
					if ref == nil || ref.state == saParamStateBlocked {
						blocked = true
					} else if ref.state == saParamStateUnresolved {
						unresolved = true
					} else {
						cur = ref.prov
					}
				}
				if blocked {
					break
				}
				if e.kind == saParamEdgeVar {
					ref := states[pair{e.varKey, e.varIdx}]
					if ref == nil || ref.state != saParamStateResolved {
						continue
					}
				}
				if agreed == nil {
					cpy := cur
					agreed = &cpy
				} else if agreed.isLit != cur.isLit {
					blocked = true
					break
				} else if agreed.isLit {
					if agreed.litID != cur.litID {
						blocked = true
						break
					}
					// Same lit id: same table lineage. Keep the
					// first edge's table and litIface.
				} else if agreed.class != cur.class {
					blocked = true
					break
				}
			}
			if blocked {
				st.state = saParamStateBlocked
				changed = true
			} else if !unresolved && agreed != nil {
				st.state = saParamStateResolved
				st.prov = *agreed
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for _, pr := range pairs {
		st := states[pr]
		if st.state == saParamStateUnresolved {
			// Dependency cycle: fail closed.
			st.state = saParamStateBlocked
		}
	}
	// Record.
	for _, pr := range pairs {
		st := states[pr]
		if st.state != saParamStateResolved {
			continue
		}
		if !p.saParamEligible(pr.key) {
			continue
		}
		sig := p.funcs[pr.key]
		if pr.idx >= len(sig.paramKinds) {
			continue
		}
		paramKind := sig.paramKinds[pr.idx]
		iface := ""
		if len(paramKind) > 5 && paramKind[:5] == "inst:" {
			iface = paramKind[5:]
		}
		if iface == "" {
			continue
		}
		prov := st.prov
		if prov.isLit {
			// Filter the table to this parameter interface's methods
			// (mirror R0's copy filter); an empty filter records
			// nothing. A filter-interface change mints a fresh
			// agreement id (a filtered table is not the same table).
			idef, ok := p.classes[iface]
			if !ok || idef == nil || !idef.isIface {
				continue
			}
			filtered := map[string]*ast.Node{}
			for m, anode := range prov.litFn {
				if _, isMeth := idef.methods[m]; isMeth {
					filtered[m] = anode
				}
			}
			if len(filtered) == 0 {
				continue
			}
			prov.litFn = filtered
			if prov.litIface != iface {
				p.litSeq++
				prov.litID = p.litSeq
				prov.litIface = iface
			}
		} else {
			// The agreed class must satisfy the parameter interface
			// (explicitly or structurally); otherwise the call itself
			// refuses at argument matching, and no prov is recorded.
			if !saClassSatisfies(p.classes, prov.class, iface) &&
				!saClassStructurallySatisfies(p.classes, prov.class, iface) {
				continue
			}
		}
		// Write back to every funcs key for this canonical function
		// (namespace members live under both under and dotted keys).
		keys := []string{pr.key}
		for dotted, under := range p.nsDottedToUnder {
			if under == pr.key {
				keys = append(keys, dotted)
			}
		}
		for _, key := range keys {
			sig2, ok := p.funcs[key]
			if !ok {
				continue
			}
			if len(sig2.paramIfaceProv) < len(sig2.paramKinds) {
				np := make([]string, len(sig2.paramKinds))
				copy(np, sig2.paramIfaceProv)
				sig2.paramIfaceProv = np
			}
			if len(sig2.paramIfaceLitFn) < len(sig2.paramKinds) {
				nl := make([]map[string]*ast.Node, len(sig2.paramKinds))
				copy(nl, sig2.paramIfaceLitFn)
				sig2.paramIfaceLitFn = nl
			}
			if len(sig2.paramIfaceLitID) < len(sig2.paramKinds) {
				ni := make([]int, len(sig2.paramKinds))
				copy(ni, sig2.paramIfaceLitID)
				sig2.paramIfaceLitID = ni
			}
			if prov.isLit {
				sig2.paramIfaceLitFn[pr.idx] = prov.litFn
				sig2.paramIfaceLitID[pr.idx] = prov.litID
				sig2.paramIfaceProv[pr.idx] = ""
			} else {
				sig2.paramIfaceProv[pr.idx] = prov.class
				sig2.paramIfaceLitFn[pr.idx] = nil
				sig2.paramIfaceLitID[pr.idx] = 0
			}
			p.funcs[key] = sig2
		}
	}
}

// saInstallParamIfaceProv installs recorded parameter provenance on a
// body-entry scope. params parallels the signature's paramKinds; length
// mismatch fails closed (no installation).
func saInstallParamIfaceProv(scope *saScope, sig saFuncSig, params []string) {
	if scope == nil || len(params) == 0 {
		return
	}
	if len(sig.paramIfaceProv) < len(params) && len(sig.paramIfaceLitFn) < len(params) {
		return
	}
	for i, name := range params {
		if name == "" {
			continue
		}
		if i < len(sig.paramIfaceProv) && sig.paramIfaceProv[i] != "" {
			saSetIfaceProv(scope, name, sig.paramIfaceProv[i])
			continue
		}
		if i < len(sig.paramIfaceLitFn) && sig.paramIfaceLitFn[i] != nil {
			// Filter to the parameter interface's methods (mirror
			// the R0 attachment filter; recording already filters,
			// so this is idempotent defense in depth).
			if i < len(sig.paramKinds) && len(sig.paramKinds[i]) > 5 && sig.paramKinds[i][:5] == "inst:" {
				if idef, ok := scope.classes[sig.paramKinds[i][5:]]; ok && idef != nil && idef.isIface {
					for m, anode := range sig.paramIfaceLitFn[i] {
						if _, isMeth := idef.methods[m]; isMeth {
							saSetIfaceLitFn(scope, name, m, anode)
						}
					}
				}
			}
		}
	}
}

// saProgComputeImportedBy builds the import-graph eligibility map from
// the program graph: target file -> export name -> importing files.
// Direct imports, namespace imports (wildcard "*"), and re-export
// chains are all included; type-only imports and re-exports never enter
// impOf/reexpOf, so they are naturally excluded. Over-approximation is
// sound (fail-closed): a star re-export chain marks the re-exported
// file's names imported even when the middle file defines its own.
func saProgComputeImportedBy(impOf map[string]map[string]string, starImpOf map[string]map[string]bool, reexpOf map[string]map[string]string, starOf map[string][]string) map[string]map[string]map[string]bool {
	// eff[Y] = set of "file\x00name" pairs Y effectively imports
	// (name "*" = wildcard via a namespace import or star re-export).
	eff := map[string]map[string]bool{}
	add := func(file, key string) bool {
		if eff[file] == nil {
			eff[file] = map[string]bool{}
		}
		if eff[file][key] {
			return false
		}
		eff[file][key] = true
		return true
	}
	for importer, m := range impOf {
		for _, edge := range m {
			add(importer, edge)
		}
	}
	for importer, tgts := range starImpOf {
		for tgt := range tgts {
			add(importer, tgt+"\x00*")
		}
	}
	// Fixpoint over re-export chains (set growth only, so it terminates).
	for {
		changed := false
		for importer, keys := range eff {
			for key := range keys {
				parts := strings.SplitN(key, "\x00", 2)
				if len(parts) != 2 {
					continue
				}
				mid, exported := parts[0], parts[1]
				if exported == "*" {
					for name, edge := range reexpOf[mid] {
						if add(importer, edge) {
							changed = true
						}
						_ = name
					}
					for _, tgt := range starOf[mid] {
						if add(importer, tgt+"\x00*") {
							changed = true
						}
					}
				} else {
					if edge, ok := reexpOf[mid][exported]; ok {
						if add(importer, edge) {
							changed = true
						}
					}
					for _, tgt := range starOf[mid] {
						if add(importer, tgt+"\x00"+exported) {
							changed = true
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	out := map[string]map[string]map[string]bool{}
	for importer, keys := range eff {
		for key := range keys {
			parts := strings.SplitN(key, "\x00", 2)
			if len(parts) != 2 {
				continue
			}
			target, exported := parts[0], parts[1]
			if out[target] == nil {
				out[target] = map[string]map[string]bool{}
			}
			if out[target][exported] == nil {
				out[target][exported] = map[string]bool{}
			}
			out[target][exported][importer] = true
		}
	}
	return out
}
