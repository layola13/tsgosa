package transpile

import (
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// Phase F: interface field provenance (branch tsgosa/parallel/iface_fields,
// JEV fields_* decisions in ~/workspace/iface_fields_design_probe.md).
//
// Problem: a field read bound to a local (`const x = h.svc`) carries the
// field's declared interface kind but no provenance, so dispatch on the
// binding refuses with "X is an interface method with no body" even when
// every `new Holder(...)` site in the file wires the same concrete class
// into that field. This file adds a same-file, sync, emitter-only
// field-store analysis: for each same-file class with an interface-typed
// field wired by its constructor (explicit `this.f = param` statements
// plus parameter properties), if EVERY collected `new Class(...)` site
// wires the same satisfying concrete class at that field's position, the
// provenance is recorded on the class definition (fieldProv, parallel to
// R0's retIfaceProv / P0's paramIfaceProv). Consumption needs no new
// lowering: the unannotated bind install (saAttachIfaceFieldProv, next to
// R0's attach in the inferred-decl arm) installs it on const binds from
// field reads, and the shared field-read entry helper
// (saFieldReadEntry, called from saRetClassifyInitWithEntries) lets R0
// and P0 entry builders classify field-read initializers, so
// bind-mediated field->param and field->return chains work through the
// existing joint loop. The dispatch site is unchanged.
//
// Soundness argument (complete-or-poison, mirroring P0's exactly_one
// UNANIMOUS): exactly-one is sound only if every possible wiring site
// is enumerated as a site or poisons the class. Collected new-sites are
// top-level function bodies, namespace member bodies, top-level arrow
// bodies, and method bodies (P0's caller contexts). Everything else
// poisons: entry statements, nested functions, local arrows,
// object-literal methods, field initializers, static blocks, parameter
// defaults, heritage clauses (a subclass instance flows through super()
// wiring, which is inherited wiring — out of v1), alias and value uses
// of a candidate class name, and `new` sites with unresolvable wired
// args. Spurious sites from unexecuted functions are safe (fail-closed
// direction, same argument as R0's q_ret_stateful_id diag-drop).
//
// Cross-file eligibility (same_file_only 0.98, import-graph rule
// mirroring P0): a same-file pass cannot see cross-file `new` sites, so
// a class's fields gain prov only if no other file imports the class
// (direct import, namespace import, or re-export chain, via
// link.importedBy; a nil link/map means single-file lowering, always
// eligible). Entry-file classes are eligible under the same rule (the
// host is outside the threat model, exactly as for R0/P0).
//
// v1 boundaries, fail-closed throughout (JEV): wiring_only (direct
// stores are the split-out F1 item, not sources here); bind_mediated
// chains only (direct field-read args/returns keep refusing at their
// gates); unannotated binds only (annotated field-read binds are
// REQUIRED NEGATIVES, sa_decl.go:1154 untouched); class_only (no litFn
// in the field table; a literal in ctor-arg position refuses one gate
// earlier); sync_only; same_file_only; emitter_only. Field reads are
// NOT wired-arg sources in v1 (a direct field-read expression as a
// wired arg is Unknown); identifier entries are classified by their
// entry regardless of origin, so a bind-mediated field->field chain
// through a const binding is the same entry mechanism, not a new one.
// Method-body `this` field reads are NOT classified by the analysis
// (walks carry no owner class — the lowering install handles `this`
// via scope.thisClass, which is what the required method-bind flips
// need); method-body field->param/return chains through `this` stay
// blocked in v1, a documented narrowness for a follow-up, not a silent
// gap (they refuse loudly).
//
// This pass mutates only saClassDef.fieldProv (add-only per joint
// round) and adds no refusals itself. It recomputes from scratch each
// joint round (fresh state), so a field unresolved in one round (a
// wired arg whose param prov is not yet recorded, or a field-read entry
// whose table is not yet filled) may resolve in a later round; the
// joint loop's snapshot comparison (prov content + field tables)
// observes quiet, and the bound (1 + funcs + param slots + field
// slots) guarantees termination regardless.

// saFieldCallerParamKinds builds the name -> declared-kind map for one
// caller's parameters (for field-read receiver resolution). Empty names
// (destructured/rest) are skipped; callers with no params get nil.
func saFieldCallerParamKinds(params []saParamInfo) map[string]string {
	if len(params) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, info := range params {
		if info.name != "" {
			out[info.name] = info.kind
		}
	}
	return out
}

// saFieldReadEntry classifies a field-read binding initializer into an
// entry, shared by the R0 and P0 entry builders (via
// saRetClassifyInitWithEntries). The receiver must resolve through the
// walker's entries to a concrete class whose field table records prov
// for the field, or — for parameter receivers, which walks never seed
// into entries — through paramKinds to a concrete class kind (entries
// win on a same-name collision; a param kind that is itself an
// interface never resolves here — interface params resolve only via
// P1-seeded entries carrying their recorded prov class). The entry kind
// is the field's declared interface kind (cross-interface annotations
// stay unknown, mirroring P1's seeded-kind rule), and isNew is false
// (a load is not a new handle, so the classifier's widened-handle arm
// cannot admit it). `this` receivers are unresolvable here (no owner
// class in analysis walks) — fail-closed; the lowering install handles
// `this` separately. Guarded reads (`?.`), private names, chained
// reads, and non-interface fields are never entries.
func saFieldReadEntry(init *ast.Node, annotName string, entries map[string]saIfaceRetEntry, shadowed map[string]bool, classes map[string]*saClassDef, paramKinds map[string]string) (saIfaceRetEntry, bool) {
	if init == nil {
		return saIfaceRetEntry{}, false
	}
	peeled := saUnwrapTransparent(init)
	if peeled == nil || peeled.Kind != ast.KindPropertyAccessExpression {
		return saIfaceRetEntry{}, false
	}
	pa := peeled.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return saIfaceRetEntry{}, false
	}
	if pa.QuestionDotToken != nil {
		return saIfaceRetEntry{}, false
	}
	fname := pa.Name().Text()
	if strings.HasPrefix(fname, "#") {
		return saIfaceRetEntry{}, false
	}
	recv := saUnwrapTransparent(pa.Expression)
	if recv == nil || recv.Kind != ast.KindIdentifier {
		return saIfaceRetEntry{}, false
	}
	rname := recv.Text()
	if shadowed != nil && shadowed[rname] {
		return saIfaceRetEntry{}, false
	}
	recvClass := ""
	if e, ok := entries[rname]; ok && !e.isLit && e.class != "" {
		recvClass = e.class
	} else if k, ok := paramKinds[rname]; ok && len(k) > 5 && k[:5] == "inst:" {
		// Parameter receiver: concrete kinds only. An interface
		// param kind never resolves here (see header).
		recvClass = k[5:]
	}
	if recvClass == "" {
		return saIfaceRetEntry{}, false
	}
	def, ok := classes[recvClass]
	if !ok || def == nil || def.isIface || def.isAbstract {
		return saIfaceRetEntry{}, false
	}
	prov, ok := def.fieldProv[fname]
	if !ok || prov == "" {
		return saIfaceRetEntry{}, false
	}
	declared, ok := def.fsub[fname]
	if !ok || declared == "" {
		return saIfaceRetEntry{}, false
	}
	if def.fkinds[fname] != "inst" {
		return saIfaceRetEntry{}, false
	}
	idef, ok := classes[declared]
	if !ok || idef == nil || !idef.isIface {
		return saIfaceRetEntry{}, false
	}
	if annotName != "" && annotName != declared {
		return saIfaceRetEntry{}, false
	}
	return saIfaceRetEntry{class: prov, kind: "inst:" + declared}, true
}

// saAttachIfaceFieldProv installs recorded field provenance on a const
// binding whose initializer is a field read, in the inferred-decl arm
// next to R0's call-result attach. Prov only, no ownership change.
// The receiver resolves through the lowering scope: `this` via
// scope.thisClass, an identifier via its concrete kind or via recorded
// interface prov (a prov-carrying receiver IS that concrete class, so
// its field table applies — the same resolution the analysis helper
// performs through entries). The recorded kind must match the field's
// declared interface kind; anything else fails closed silently (no
// prov installed, the existing dispatch refusal stands).
func saAttachIfaceFieldProv(scope *saScope, init *ast.Node, name, k string) {
	if scope == nil || init == nil {
		return
	}
	peeled := saUnwrapTransparent(init)
	if peeled == nil || peeled.Kind != ast.KindPropertyAccessExpression {
		return
	}
	pa := peeled.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return
	}
	if pa.QuestionDotToken != nil {
		return
	}
	fname := pa.Name().Text()
	if strings.HasPrefix(fname, "#") {
		return
	}
	recv := saUnwrapTransparent(pa.Expression)
	if recv == nil {
		return
	}
	var def *saClassDef
	switch recv.Kind {
	case ast.KindThisKeyword:
		if scope.thisSelf == "" {
			return
		}
		d, ok := scope.classes[scope.thisClass]
		if !ok || d == nil {
			return
		}
		def = d
	case ast.KindIdentifier:
		rn := recv.Text()
		if rk, ok := scope.types[rn]; ok && len(rk) > 5 && rk[:5] == "inst:" {
			d, ok := scope.classes[rk[5:]]
			if !ok || d == nil {
				return
			}
			def = d
		} else if prov, ok := scope.ifaceProv[rn]; ok && prov != "" {
			d, ok := scope.classes[prov]
			if !ok || d == nil {
				return
			}
			def = d
		} else {
			return
		}
	default:
		return
	}
	if def == nil || def.isIface || def.isAbstract {
		return
	}
	prov, ok := def.fieldProv[fname]
	if !ok || prov == "" {
		return
	}
	declared, ok := def.fsub[fname]
	if !ok || declared == "" {
		return
	}
	if def.fkinds[fname] != "inst" {
		return
	}
	if k != "inst:"+declared {
		return
	}
	idef, ok := scope.classes[declared]
	if !ok || idef == nil || !idef.isIface {
		return
	}
	saSetIfaceProv(scope, name, prov)
}

// saIfaceFieldSnapshot encodes every recorded field table into one
// deterministic string for joint-loop change detection (class names and
// field names sorted; prov strings verbatim).
func saIfaceFieldSnapshot(classes map[string]*saClassDef) string {
	if classes == nil {
		return ""
	}
	names := make([]string, 0, len(classes))
	for n := range classes {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		def := classes[n]
		if def == nil || len(def.fieldProv) == 0 {
			continue
		}
		fields := make([]string, 0, len(def.fieldProv))
		for f := range def.fieldProv {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, f := range fields {
			b.WriteString(n)
			b.WriteByte(0)
			b.WriteString(f)
			b.WriteByte(0)
			b.WriteString(def.fieldProv[f])
			b.WriteByte(1)
		}
	}
	return b.String()
}

// saFieldCandidate is one same-file class whose interface-typed fields
// may gain prov.
type saFieldCandidate struct {
	name     string
	def      *saClassDef
	wiring   map[string]int    // field -> ctor param index
	fields   map[string]string // candidate field -> declared interface
	eligible bool
}

// saFieldSite is one classified wired argument at one new-site.
type saFieldSite struct {
	class string // resolved concrete class ("" when unclassified)
	ok    bool   // classification succeeded
}

// saFieldPass is the whole-file field analysis state. It is rebuilt
// from scratch on every joint-loop round (like P0's saParamPass).
type saFieldPass struct {
	funcs           map[string]saFuncSig
	classes         map[string]*saClassDef
	aliasOf         map[string]*ast.TypeNode
	link            *saFileLink
	stmts           []*ast.Node
	nsDottedToUnder map[string]string
	candidates      map[string]*saFieldCandidate
	poisoned        map[string]bool // class name -> every field blocked
	sites           map[string]map[string][]saFieldSite
}

// saFieldEligible mirrors P0's saParamEligible for class names: a class
// gains prov only if no other file imports it (by any export name, or
// by star import). A nil link/map (single-file lowering) or an
// unexported class means eligible.
func saFieldEligible(exportNames []string, link *saFileLink) bool {
	if link == nil || link.importedBy == nil {
		return true
	}
	self := link.self
	byFile, ok := link.importedBy[self]
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
	if len(exportNames) == 0 {
		return true
	}
	for _, name := range exportNames {
		if importedByOther(name) {
			return false
		}
	}
	return !importedByOther("*")
}

// saAnalyzeFieldIfaceProv is the field hook, called from the joint loop
// after R0's analysis and P0's pass in each round (field prov feeds
// joint results via entries, and joint-derived call args / param prov
// feed field sources, so the field pass must iterate inside the loop).
// It mutates only saClassDef.fieldProv and adds no refusals.
func saAnalyzeFieldIfaceProv(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, link *saFileLink) {
	if sf == nil || funcs == nil {
		return
	}
	f := &saFieldPass{
		funcs:           funcs,
		classes:         classes,
		aliasOf:         aliasOf,
		link:            link,
		stmts:           sf.AsSourceFile().Statements.Nodes,
		nsDottedToUnder: map[string]string{},
		candidates:      map[string]*saFieldCandidate{},
		poisoned:        map[string]bool{},
		sites:           map[string]map[string][]saFieldSite{},
	}
	f.collectCandidates()
	if len(f.candidates) == 0 {
		return
	}
	f.collectCallers()
	f.record()
}

// saFieldWiring builds a class's field<-param wiring map from its ctor
// AST: parameter properties first, then explicit `this.f = param`
// statements (explicit wins, mirroring saWireCtorBody's inject skip).
// Non-param right sides are ignored (lowering refuses them via F0 when
// the class is instantiated, so any prov from other sites would be
// unobservable). A nil map means no usable wiring.
func saFieldWiring(def *saClassDef) map[string]int {
	if def == nil || def.ctor == nil {
		return nil
	}
	ctor := def.ctor
	params := ctor.Parameters()
	if len(params) == 0 {
		return nil
	}
	names := make([]string, len(params))
	byName := map[string]int{}
	for i, p := range params {
		if p == nil {
			continue
		}
		pd := p.AsParameterDeclaration()
		if pd == nil {
			continue
		}
		if nm := pd.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			names[i] = nm.Text()
			byName[nm.Text()] = i
		}
	}
	wiring := map[string]int{}
	for i, p := range params {
		if p == nil || names[i] == "" {
			continue
		}
		if ast.IsParameterPropertyDeclaration(p, ctor) {
			wiring[names[i]] = i
		}
	}
	body := ctor.Body()
	if body == nil {
		if len(wiring) == 0 {
			return nil
		}
		return wiring
	}
	for _, s := range body.Statements() {
		if s == nil || s.Kind != ast.KindExpressionStatement {
			continue
		}
		ex := s.AsExpressionStatement().Expression
		if ex == nil || ex.Kind != ast.KindBinaryExpression {
			continue
		}
		bin := ex.AsBinaryExpression()
		if bin == nil || bin.OperatorToken == nil || bin.OperatorToken.Kind != ast.KindEqualsToken ||
			bin.Left == nil || bin.Left.Kind != ast.KindPropertyAccessExpression || bin.Right == nil {
			continue
		}
		pa := bin.Left.AsPropertyAccessExpression()
		if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindThisKeyword ||
			pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			continue
		}
		right := saRetPeelParenNonNull(bin.Right)
		if right == nil || right.Kind != ast.KindIdentifier {
			continue
		}
		if idx, ok := byName[right.Text()]; ok {
			wiring[pa.Name().Text()] = idx
		}
	}
	if len(wiring) == 0 {
		return nil
	}
	return wiring
}

// collectCandidates registers top-level classes with at least one
// interface-typed wired field. Namespace-member classes are out of v1
// (they must refuse loudly, never silently gain prov — they are simply
// never candidates). Imported classes are never candidates either: only
// declarations in this file's statements are considered, and preseed is
// local-wins, so a same-name lookup yields the file-defined def.
func (f *saFieldPass) collectCandidates() {
	for _, st := range f.stmts {
		if st == nil || st.Kind != ast.KindClassDeclaration {
			continue
		}
		nm := st.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		name := nm.Text()
		def, ok := f.classes[name]
		if !ok || def == nil || def.isIface || def.isAbstract || def.isSynth || def.isMono {
			continue
		}
		wiring := saFieldWiring(def)
		if len(wiring) == 0 {
			continue
		}
		fields := map[string]string{}
		for field := range wiring {
			if def.fkinds[field] != "inst" {
				continue
			}
			declared, ok := def.fsub[field]
			if !ok || declared == "" {
				continue
			}
			idef, ok := f.classes[declared]
			if !ok || idef == nil || !idef.isIface {
				continue
			}
			fields[field] = declared
		}
		if len(fields) == 0 {
			continue
		}
		f.candidates[name] = &saFieldCandidate{
			name:     name,
			def:      def,
			wiring:   wiring,
			fields:   fields,
			eligible: saFieldEligible(saParamExportNames(f.stmts, name, st), f.link),
		}
	}
}

// isCandidate reports whether a class name is a prov-eligible candidate
// (eligibility is part of candidacy for poison purposes too: sites for
// ineligible classes can never yield prov, so they are ignored, not
// poisoned).
func (f *saFieldPass) isCandidate(name string) bool {
	c, ok := f.candidates[name]
	return ok && c != nil && c.eligible
}

// poisonClass blocks every field of one class (poison is per-class: an
// uncollected site cannot be attributed to a single field).
func (f *saFieldPass) poisonClass(name string) {
	if f.isCandidate(name) {
		f.poisoned[name] = true
	}
}

// poisonNode scans one node as an uncollected context: every candidate
// class reference or construction poisons that class's fields.
// Declaration names, property names/keys, and TYPE positions are
// structural and never fire — this is the one place the field pass
// diverges structurally from P0's poisonNode, which can afford generic
// recursion because function names do not appear in type positions,
// while class names appear there constantly (`h: Holder`,
// `x as Holder`, `extends Holder<T>`). Hence the explicit wrapper,
// heritage, and class cases below: only expressions are ever visited,
// never TypeNodes, never implements clauses, never type arguments.
func (f *saFieldPass) poisonNode(n *ast.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindIdentifier:
		f.poisonClass(n.Text())
		return
	case ast.KindCallExpression:
		ce := n.AsCallExpression()
		if ce != nil {
			callee := saRetPeelParenNonNull(ce.Expression)
			if callee != nil && callee.Kind == ast.KindIdentifier {
				f.poisonClass(callee.Text())
			} else if callee != nil {
				f.poisonNode(callee)
			}
			if ce.Arguments != nil {
				for _, a := range ce.Arguments.Nodes {
					f.poisonNode(a)
				}
			}
		}
		return
	case ast.KindNewExpression:
		ne := n.AsNewExpression()
		if ne != nil {
			callee := saRetPeelParenNonNull(ne.Expression)
			if callee != nil && callee.Kind == ast.KindIdentifier {
				f.poisonClass(callee.Text())
			} else if callee != nil {
				f.poisonNode(callee)
			}
			if ne.Arguments != nil {
				for _, a := range ne.Arguments.Nodes {
					f.poisonNode(a)
				}
			}
		}
		return
	case ast.KindPropertyAccessExpression:
		if pa := n.AsPropertyAccessExpression(); pa != nil && pa.Expression != nil {
			f.poisonNode(pa.Expression)
		}
		return
	case ast.KindPropertyAssignment:
		if pa := n.AsPropertyAssignment(); pa != nil {
			if nm := n.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
				f.poisonNode(nm)
			}
			if pa.Initializer != nil {
				f.poisonNode(pa.Initializer)
			}
		}
		return
	case ast.KindShorthandPropertyAssignment:
		if nm := n.Name(); nm != nil {
			f.poisonNode(nm)
		}
		if sa := n.AsShorthandPropertyAssignment(); sa != nil && sa.ObjectAssignmentInitializer != nil {
			f.poisonNode(sa.ObjectAssignmentInitializer)
		}
		return
	case ast.KindVariableDeclaration:
		if vd := n.AsVariableDeclaration(); vd != nil && vd.Initializer != nil {
			f.poisonNode(vd.Initializer)
		}
		return
	case ast.KindParameter:
		if pd := n.AsParameterDeclaration(); pd != nil && pd.Initializer != nil {
			f.poisonNode(pd.Initializer)
		}
		return
	case ast.KindFunctionDeclaration, ast.KindArrowFunction, ast.KindFunctionExpression:
		if pl := n.ParameterList(); pl != nil {
			for _, prm := range pl.Nodes {
				f.poisonNode(prm)
			}
		}
		if body := n.Body(); body != nil {
			f.poisonNode(body)
		}
		return
	case ast.KindMethodDeclaration, ast.KindGetAccessor, ast.KindSetAccessor:
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
			f.poisonNode(nm)
		}
		if pl := n.ParameterList(); pl != nil {
			for _, prm := range pl.Nodes {
				f.poisonNode(prm)
			}
		}
		if body := n.Body(); body != nil {
			f.poisonNode(body)
		}
		return
	case ast.KindConstructor:
		if pl := n.ParameterList(); pl != nil {
			for _, prm := range pl.Nodes {
				f.poisonNode(prm)
			}
		}
		if body := n.Body(); body != nil {
			f.poisonNode(body)
		}
		return
	case ast.KindPropertyDeclaration:
		if nm := n.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
			f.poisonNode(nm)
		}
		if pd := n.AsPropertyDeclaration(); pd != nil && pd.Initializer != nil {
			f.poisonNode(pd.Initializer)
		}
		return
	case ast.KindClassDeclaration, ast.KindClassExpression:
		var heritage *ast.HeritageClauseList
		var members []*ast.Node
		if cd := n.AsClassDeclaration(); cd != nil {
			heritage = cd.HeritageClauses
			if cd.Members != nil {
				members = cd.Members.Nodes
			}
		} else if ce := n.AsClassExpression(); ce != nil {
			heritage = ce.HeritageClauses
			if ce.Members != nil {
				members = ce.Members.Nodes
			}
		}
		if heritage != nil {
			for _, h := range heritage.Nodes {
				f.poisonNode(h)
			}
		}
		for _, m := range members {
			if m == nil {
				continue
			}
			switch m.Kind {
			case ast.KindPropertyDeclaration:
				if nm := m.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
					f.poisonNode(nm)
				}
				if pd := m.AsPropertyDeclaration(); pd != nil && pd.Initializer != nil {
					f.poisonNode(pd.Initializer)
				}
			case ast.KindMethodDeclaration:
				// Collected above when this class is top-level; in
				// poison mode the whole method is uncollected.
				f.poisonNode(m)
			default:
				f.poisonNode(m)
			}
		}
		return
	case ast.KindHeritageClause:
		// Only extends expressions are value positions; implements
		// clauses and all type arguments are types — never visited.
		if hc := n.AsHeritageClause(); hc != nil && hc.Token == ast.KindExtendsKeyword && hc.Types != nil {
			for _, el := range hc.Types.Nodes {
				if el == nil {
					continue
				}
				if el.Kind == ast.KindExpressionWithTypeArguments {
					if ex := el.AsExpressionWithTypeArguments().Expression; ex != nil {
						f.poisonNode(ex)
					}
				} else {
					f.poisonNode(el)
				}
			}
		}
		return
	case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression,
		ast.KindTypeAssertionExpression, ast.KindNonNullExpression:
		// Transparent wrappers: the inner expression only, never the type.
		if u := saUnwrapTransparent(n); u != nil && u != n {
			f.poisonNode(u)
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
			f.poisonNode(ja.Initializer)
		}
		return
	case ast.KindLabeledStatement:
		if ls := n.AsLabeledStatement(); ls != nil && ls.Statement != nil {
			f.poisonNode(ls.Statement)
		}
		return
	default:
		n.ForEachChild(func(c *ast.Node) bool {
			f.poisonNode(c)
			return false
		})
	}
}

// saFieldCaller is one collected new-site context.
type saFieldCaller struct {
	key           string // funcs key ("" for method bodies, which never resolve var edges)
	body          *ast.Node
	params        []saParamInfo
	isMethod      bool
	isAsync       bool
	hasTypeParams bool
}

// saFieldCollectMethodCallers records method bodies of one class node as
// site-contributing callers (methods never resolve var edges, so their
// params are name-only) and poison-scans method parameter defaults
// (defaults run at call time and are invisible sites otherwise).
func (f *saFieldPass) saFieldCollectMethodCallers(classNode *ast.Node, callers *[]*saFieldCaller) {
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
		*callers = append(*callers, &saFieldCaller{
			body:     body,
			params:   saParamMethodInfos(nodes),
			isMethod: true,
		})
		f.poisonParamDefaults(nodes)
	}
}

// poisonParamDefaults poison-scans parameter default expressions: a
// default runs at call time when its argument is omitted, so a `new`
// inside a default is an invisible wiring site unless poisoned. (P0
// needs no equivalent because its omitted-arg rule poisons the call
// site itself; a `new` in a default is not a call to a P0 candidate.)
func (f *saFieldPass) poisonParamDefaults(nodes []*ast.Node) {
	for _, p := range nodes {
		if p == nil {
			continue
		}
		pd := p.AsParameterDeclaration()
		if pd == nil || pd.Initializer == nil {
			continue
		}
		f.poisonNode(pd.Initializer)
	}
}

// collectCallers walks the file's top-level statements, registering
// caller bodies and poison-scanning everything uncollected (mirroring
// P0's collectCallers, plus parameter-default poison scans).
func (f *saFieldPass) collectCallers() {
	pp := &saParamPass{funcs: f.funcs, classes: f.classes}
	var callers []*saFieldCaller
	for _, st := range f.stmts {
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
			sig, ok := f.funcs[name]
			if !ok {
				continue
			}
			var nodes []*ast.Node
			if fn.Parameters != nil {
				nodes = fn.Parameters.Nodes
			}
			infos, ok := pp.saParamInfos(nodes, sig.paramKinds)
			if !ok {
				// Kind/name mismatch: fail closed, poison-scan the body.
				f.poisonNode(st.Body())
				continue
			}
			callers = append(callers, &saFieldCaller{key: name, body: st.Body(), params: infos, isAsync: saParamFnAsync(st), hasTypeParams: saParamHasTypeParams(st)})
			f.poisonParamDefaults(nodes)
		case ast.KindModuleDeclaration:
			members, ok := saFlattenNsMembers(st)
			if !ok {
				f.poisonNode(st)
				continue
			}
			for _, mb := range members {
				if mb.isConst {
					// Namespace const initializers are value positions.
					if mb.decl != nil {
						if vd := mb.decl.AsVariableDeclaration(); vd != nil && vd.Initializer != nil {
							f.poisonNode(vd.Initializer)
						}
					}
					continue
				}
				if mb.isFunc {
					fn := mb.node.AsFunctionDeclaration()
					if fn == nil || mb.node.Body() == nil {
						continue
					}
					f.nsDottedToUnder[mb.dotted] = mb.under
					sig, ok := f.funcs[mb.under]
					if !ok {
						continue
					}
					var nodes []*ast.Node
					if fn.Parameters != nil {
						nodes = fn.Parameters.Nodes
					}
					infos, ok := pp.saParamInfos(nodes, sig.paramKinds)
					if !ok {
						f.poisonNode(mb.node.Body())
						continue
					}
					callers = append(callers, &saFieldCaller{key: mb.under, body: mb.node.Body(), params: infos, isAsync: saParamFnAsync(mb.node), hasTypeParams: saParamHasTypeParams(mb.node)})
					f.poisonParamDefaults(nodes)
					continue
				}
				// Namespace classes: their methods contribute sites,
				// and their extras are poison-scanned like top-level
				// classes (a `new` in an ns class field initializer
				// is an invisible site otherwise).
				f.saFieldCollectMethodCallers(mb.node, &callers)
				f.poisonClassExtras(mb.node)
			}
		case ast.KindVariableStatement:
			if name, arrow, ok := saIsTopLevelArrowConst(st); ok {
				sig, ok := f.funcs[name]
				if !ok || arrow == nil || arrow.Body() == nil {
					continue
				}
				var nodes []*ast.Node
				if pl := arrow.ParameterList(); pl != nil {
					nodes = pl.Nodes
				}
				infos, ok := pp.saParamInfos(nodes, sig.paramKinds)
				if !ok {
					f.poisonNode(arrow.Body())
					continue
				}
				callers = append(callers, &saFieldCaller{key: name, body: arrow.Body(), params: infos, isAsync: saParamFnAsync(arrow), hasTypeParams: saParamHasTypeParams(arrow)})
				f.poisonParamDefaults(nodes)
				continue
			}
			// Any other top-level const: initializers are module-level
			// value positions (a `new` there is an invisible site).
			if vs := st.AsVariableStatement(); vs != nil && vs.DeclarationList != nil {
				if dl := vs.DeclarationList.AsVariableDeclarationList(); dl != nil {
					for _, d := range dl.Declarations.Nodes {
						if vd := d.AsVariableDeclaration(); vd != nil && vd.Initializer != nil {
							f.poisonNode(vd.Initializer)
						}
					}
				}
			}
		case ast.KindClassDeclaration:
			f.saFieldCollectMethodCallers(st, &callers)
			// Ctor param defaults, field initializers, heritage,
			// static blocks: uncollected, poison-scanned.
			f.poisonClassExtras(st)
		case ast.KindExportAssignment:
			// `export = <expr>`: the expression is a value escape.
			if ea := st.AsExportAssignment(); ea != nil && ea.Expression != nil {
				f.poisonNode(ea.Expression)
			}
		default:
			if saIsEntryStmt(st) {
				f.poisonNode(st)
			}
			// Imports, exports, types, enums, interfaces: no value
			// positions (export lists are eligibility, not poison).
		}
	}
	// Walk every collected caller body.
	for _, c := range callers {
		f.walkCaller(c)
	}
}

// poisonClassExtras poison-scans the uncollected parts of one class
// node: heritage clauses (a subclass instance flows through super()
// wiring, which is inherited wiring — out of v1), ctor parameter
// defaults, field initializers, computed names, and static blocks.
// Method bodies are NOT scanned here (they are collected callers).
func (f *saFieldPass) poisonClassExtras(classNode *ast.Node) {
	if classNode == nil {
		return
	}
	var heritage *ast.HeritageClauseList
	var members []*ast.Node
	if cd := classNode.AsClassDeclaration(); cd != nil {
		heritage = cd.HeritageClauses
		if cd.Members != nil {
			members = cd.Members.Nodes
		}
	} else if ce := classNode.AsClassExpression(); ce != nil {
		heritage = ce.HeritageClauses
		if ce.Members != nil {
			members = ce.Members.Nodes
		}
	} else {
		return
	}
	if heritage != nil {
		for _, h := range heritage.Nodes {
			f.poisonNode(h)
		}
	}
	if ctor := f.classCtorNode(classNode); ctor != nil {
		if pl := ctor.ParameterList(); pl != nil {
			f.poisonParamDefaults(pl.Nodes)
		}
	}
	for _, m := range members {
		if m == nil {
			continue
		}
		switch m.Kind {
		case ast.KindPropertyDeclaration:
			if pd := m.AsPropertyDeclaration(); pd != nil {
				if nm := m.Name(); nm != nil && nm.Kind == ast.KindComputedPropertyName {
					f.poisonNode(nm)
				}
				if pd.Initializer != nil {
					f.poisonNode(pd.Initializer)
				}
			}
		case ast.KindMethodDeclaration:
			// Collected as a caller; skip (its body is collected).
		default:
			// Ctors (defaults scanned above; bodies are wiring-only,
			// but scan fully in poison mode for safety), static
			// blocks, others.
			f.poisonNode(m)
		}
	}
}

// classCtorNode returns the constructor member of a class declaration
// or expression node, or nil.
func (f *saFieldPass) classCtorNode(classNode *ast.Node) *ast.Node {
	var members []*ast.Node
	if cd := classNode.AsClassDeclaration(); cd != nil && cd.Members != nil {
		members = cd.Members.Nodes
	} else if ce := classNode.AsClassExpression(); ce != nil && ce.Members != nil {
		members = ce.Members.Nodes
	} else {
		return nil
	}
	for _, m := range members {
		if m != nil && m.Kind == ast.KindConstructor {
			return m
		}
	}
	return nil
}

// saFieldBodyWalk is the ordered per-caller walk (mirrors P0's
// saParamBodyWalk, plus new-site scanning).
type saFieldBodyWalk struct {
	f        *saFieldPass
	caller   *saFieldCaller
	taint    *saRetTaint
	entries  map[string]saIfaceRetEntry
	scan     *saFieldScan
	shadowed map[string]bool
	litSeq   int
}

// isLocal reports whether a name in this caller body refers to a local
// binding (parameter, depth-0 const entry, or shadowed nested name)
// rather than a top-level candidate class.
func (w *saFieldBodyWalk) isLocal(name string, shadowed map[string]bool) bool {
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

// walkCaller walks one collected caller body. Unanalyzable bodies
// (taint unknown) poison-scan wholesale instead of contributing sites.
func (f *saFieldPass) walkCaller(c *saFieldCaller) {
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
		w := &saFieldBodyWalk{f: f, caller: c, taint: t, entries: map[string]saIfaceRetEntry{}}
		w.scan = &saFieldScan{f: f, w: w}
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
		// body is uncollected; every candidate site/ref inside poisons.
		f.poisonNode(c.body)
		return
	}
	w := &saFieldBodyWalk{f: f, caller: c, taint: t, entries: map[string]saIfaceRetEntry{}}
	w.scan = &saFieldScan{f: f, w: w}
	w.walkList(stmts, 0, nil)
}

// walkList mirrors P0's walkList statement recursion, with new-site
// scanning at every statement and depth-0 const entry building (via
// saRetClassifyInitWithEntries, which includes the shared field-read
// arm reading the CURRENT tables — tables from prior joint rounds).
func (w *saFieldBodyWalk) walkList(stmts []*ast.Node, depth int, shadowed map[string]bool) {
	if depth > 0 {
		shadowed = saRetCopyShadow(shadowed)
		for n := range saRetDirectDeclNames(stmts) {
			shadowed[n] = true
		}
	}
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
				// Scan the initializer for sites BEFORE binding
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
				if e, ok := saRetClassifyInitWithEntries(vd.Initializer, vd.Type, w.entries, nil, w.f.funcs, w.f.classes, w.f.aliasOf, &w.litSeq, saFieldCallerParamKinds(w.caller.params)); ok {
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
			w.f.poisonNode(st)
		case ast.KindBreakStatement, ast.KindContinueStatement,
			ast.KindEmptyStatement, ast.KindDebuggerStatement:
			// No expressions.
		default:
			// Unreachable in analyzable bodies (taint flags these),
			// but stay fail-closed: poison-scan.
			w.f.poisonNode(st)
		}
	}
}

// saFieldScan is the shared expression scanner. In site mode (w != nil)
// new-sites are classified and identifier refs are checked against
// locals; in poison mode (w == nil) every candidate site/ref poisons.
type saFieldScan struct {
	f *saFieldPass
	w *saFieldBodyWalk // nil in poison mode
}

// expr scans one expression node for new-sites and identifier refs.
// Only expressions are visited, never TypeNodes: wrapper kinds are
// handled explicitly (inner expression only), and call/new use their
// structured accessors (callee + arguments, never type arguments).
func (s *saFieldScan) expr(e *ast.Node) {
	if e == nil {
		return
	}
	switch e.Kind {
	case ast.KindIdentifier:
		s.ident(e)
		return
	case ast.KindNewExpression:
		s.newExpr(e)
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
		s.f.poisonNode(e)
		return
	case ast.KindJsxAttribute:
		// Attribute names are not references; initializers are.
		if ja := e.AsJsxAttribute(); ja != nil && ja.Initializer != nil {
			s.expr(ja.Initializer)
		}
		return
	case ast.KindParenthesizedExpression, ast.KindAsExpression, ast.KindSatisfiesExpression,
		ast.KindTypeAssertionExpression, ast.KindNonNullExpression:
		// Transparent wrappers: the inner expression only, never the type.
		if u := saUnwrapTransparent(e); u != nil && u != e {
			s.expr(u)
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
func (s *saFieldScan) ident(id *ast.Node) {
	if id == nil {
		return
	}
	name := id.Text()
	if s.w == nil {
		// Poison mode: any candidate name poisons (over-poisoning
		// shadowed locals is sound).
		s.f.poisonClass(name)
		return
	}
	// Site mode: locals (parameters, entries, shadowed names) are not
	// candidate references. Tainted names are ambiguous: poison if
	// they match a candidate (fail-closed, mirroring P0's ident).
	if s.w.isLocal(name, s.w.shadowed) {
		return
	}
	if s.w.taint.tainted(name) {
		s.f.poisonClass(name)
		return
	}
	s.f.poisonClass(name)
}

// call handles one call expression: calls are never field sites, but
// their callee and arguments may contain sites (or, in poison mode,
// candidate uses).
func (s *saFieldScan) call(e *ast.Node) {
	ce := e.AsCallExpression()
	if ce == nil {
		return
	}
	var args []*ast.Node
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	callee := saRetPeelParenNonNull(ce.Expression)
	if s.w == nil {
		// Poison mode: a call to a candidate class value poisons;
		// other callees are scanned for references (a property
		// callee contributes only its receiver).
		if callee != nil && callee.Kind == ast.KindIdentifier {
			s.f.poisonClass(callee.Text())
		} else if callee != nil {
			s.expr(callee)
		}
	} else if callee != nil {
		// Site mode: a bare-identifier callee is the call itself,
		// never a reference; a property-access callee contributes
		// only its receiver expression.
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

// newExpr handles one new expression: in site mode a bare-identifier
// callee naming a candidate class records a site (and its arguments
// are scanned for nested sites); anything else is scanned for
// references. In poison mode every candidate construction poisons.
func (s *saFieldScan) newExpr(e *ast.Node) {
	ne := e.AsNewExpression()
	if ne == nil {
		return
	}
	var args []*ast.Node
	if ne.Arguments != nil {
		args = ne.Arguments.Nodes
	}
	callee := saRetPeelParenNonNull(ne.Expression)
	if s.w == nil {
		// Poison mode: any candidate construction poisons.
		if callee != nil && callee.Kind == ast.KindIdentifier {
			s.f.poisonClass(callee.Text())
		} else if callee != nil {
			s.expr(callee)
		}
		for _, a := range args {
			s.expr(a)
		}
		return
	}
	// Site mode.
	if callee != nil && callee.Kind == ast.KindIdentifier {
		name := callee.Text()
		if s.w.isLocal(name, s.w.shadowed) || s.w.taint.tainted(name) {
			// Shadowed: the new constructs the local, not the class.
			// Tainted: ambiguous — poison if it names a candidate
			// (fail-closed, mirroring P0's ident rule).
			if s.w.taint.tainted(name) {
				s.f.poisonClass(name)
			}
			for _, a := range args {
				s.expr(a)
			}
			return
		}
		if s.f.isCandidate(name) {
			s.recordNewSite(name, args)
		}
		for _, a := range args {
			s.expr(a)
		}
		return
	}
	// Non-identifier callee: scan the callee expression for references
	// (a property-access callee contributes only its receiver) plus args.
	if callee != nil {
		s.expr(callee)
	}
	for _, a := range args {
		s.expr(a)
	}
}

// recordNewSite classifies one collected `new Class(...)` site's wired
// arguments into per-field sites. First spread position: arguments at
// or after it are unknowable. Omitted wired arguments are unknowable
// (the ctor default flows in, and defaults are poison-scanned
// separately — an omitted arg can never silently agree).
func (s *saFieldScan) recordNewSite(className string, args []*ast.Node) {
	cand, ok := s.f.candidates[className]
	if !ok || cand == nil {
		return
	}
	firstSpread := -1
	for i, a := range args {
		if a != nil && a.Kind == ast.KindSpreadElement {
			firstSpread = i
			break
		}
	}
	for field, idx := range cand.wiring {
		if _, isCandidateField := cand.fields[field]; !isCandidateField {
			continue
		}
		var site saFieldSite
		switch {
		case firstSpread >= 0 && idx >= firstSpread:
			site = saFieldSite{ok: false}
		case idx >= len(args):
			site = saFieldSite{ok: false}
		default:
			class, ok := s.w.classifyWiredArg(args[idx], cand.fields[field])
			site = saFieldSite{class: class, ok: ok}
		}
		if s.f.sites[className] == nil {
			s.f.sites[className] = map[string][]saFieldSite{}
		}
		s.f.sites[className][field] = append(s.f.sites[className][field], site)
	}
}

// classifyWiredArg classifies one wired argument against one field's
// declared interface. Anything unlisted is unknowable (blocks the field
// this round; the pass recomputes each joint round, so evidence gained
// later unblocks). Direct field-read expressions are unknowable in v1
// (field reads are consumers, not sources); identifier entries are
// classified by their entry regardless of origin (a bind-mediated
// field->field chain through a const binding is the same entry
// mechanism). Literal-method entries and call-result litFn tables are
// unknowable (class_only).
func (w *saFieldBodyWalk) classifyWiredArg(arg *ast.Node, fieldIface string) (string, bool) {
	if arg == nil {
		return "", false
	}
	arg = saRetPeelParenNonNull(arg)
	if arg == nil {
		return "", false
	}
	switch arg.Kind {
	case ast.KindNewExpression:
		ne := arg.AsNewExpression()
		if ne == nil || ne.Expression == nil || ne.Expression.Kind != ast.KindIdentifier {
			return "", false
		}
		return ne.Expression.Text(), true
	case ast.KindIdentifier:
		name := arg.Text()
		if w.taint.tainted(name) {
			return "", false
		}
		if w.shadowed != nil && w.shadowed[name] {
			return "", false
		}
		if entry, ok := w.entries[name]; ok {
			if entry.isLit || entry.class == "" {
				return "", false
			}
			return entry.class, true
		}
		if w.caller.isMethod {
			// Method parameters never carry prov (design-literal).
			return "", false
		}
		for i, info := range w.caller.params {
			if info.name == name {
				if info.isIface {
					if w.caller.key == "" {
						return "", false
					}
					if sig, ok := w.f.funcs[w.caller.key]; ok && i < len(sig.paramIfaceProv) && sig.paramIfaceProv[i] != "" {
						return sig.paramIfaceProv[i], true
					}
					// Unrecorded param prov: unknowable this round
					// (a later joint round may record it).
					return "", false
				}
				if len(info.kind) > 5 && info.kind[:5] == "inst:" {
					if def, ok := w.f.classes[info.kind[5:]]; ok && def != nil && !def.isIface {
						return info.kind[5:], true
					}
				}
				return "", false
			}
		}
		return "", false
	case ast.KindCallExpression:
		ce := arg.AsCallExpression()
		if ce == nil {
			return "", false
		}
		calleeKey := w.fieldEdgeCalleeKey(ce)
		if calleeKey == "" {
			return "", false
		}
		sig, ok := w.f.funcs[calleeKey]
		if !ok || sig.retIsPromise {
			return "", false
		}
		if sig.retKind == "inst:"+fieldIface {
			if sig.retIfaceProv != "" {
				return sig.retIfaceProv, true
			}
			return "", false
		}
		if len(sig.retKind) > 5 && sig.retKind[:5] == "inst:" {
			if def, ok := w.f.classes[sig.retKind[5:]]; ok && def != nil && !def.isIface {
				return sig.retKind[5:], true
			}
		}
		return "", false
	default:
		// Field reads, object literals, await, and everything else is
		// unresolvable here.
		return "", false
	}
}

// fieldEdgeCalleeKey mirrors P0's edgeCalleeKey for call-result wired
// args: a bare identifier or namespace-dotted callee, not locally
// shadowed or tainted. "" means "not an edge".
func (w *saFieldBodyWalk) fieldEdgeCalleeKey(ce *ast.CallExpression) string {
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
		if w.isLocal(name, w.shadowed) || w.taint.tainted(name) {
			return ""
		}
		if _, ok := w.f.funcs[name]; ok {
			return name
		}
		return ""
	case ast.KindPropertyAccessExpression:
		pa := callee.AsPropertyAccessExpression()
		if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
			return ""
		}
		var dotted string
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier {
			root := pa.Expression.Text()
			if w.isLocal(root, w.shadowed) || w.taint.tainted(root) {
				return ""
			}
			dotted = root + "." + pa.Name().Text()
		} else if d, _, _, ok := saNsCallChain(callee); ok {
			dotted = d
		} else {
			return ""
		}
		if _, ok := w.f.funcs[dotted]; ok {
			if under, ok := w.f.nsDottedToUnder[dotted]; ok {
				return under
			}
			return dotted
		}
		return ""
	default:
		return ""
	}
}

// record writes agreed field prov onto candidate class definitions:
// poisoned classes, ineligible classes, zero-site fields, unclassified
// sites, and disagreements record nothing (fail-closed). The agreed
// class must be a real (non-interface, non-abstract) class satisfying
// the field's declared interface (mirroring P0's record-time
// satisfaction check).
func (f *saFieldPass) record() {
	names := make([]string, 0, len(f.candidates))
	for name := range f.candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cand := f.candidates[name]
		if cand == nil || !cand.eligible || f.poisoned[name] {
			continue
		}
		fields := make([]string, 0, len(cand.fields))
		for field := range cand.fields {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			sites := f.sites[name][field]
			if len(sites) == 0 {
				continue
			}
			agreed := ""
			blocked := false
			for _, s := range sites {
				if !s.ok {
					blocked = true
					break
				}
				if agreed == "" {
					agreed = s.class
				} else if agreed != s.class {
					blocked = true
					break
				}
			}
			if blocked || agreed == "" {
				continue
			}
			adef, ok := f.classes[agreed]
			if !ok || adef == nil || adef.isIface || adef.isAbstract {
				continue
			}
			iface := cand.fields[field]
			if !saClassSatisfies(f.classes, agreed, iface) &&
				!saClassStructurallySatisfies(f.classes, agreed, iface) {
				continue
			}
			if cand.def.fieldProv == nil {
				cand.def.fieldProv = map[string]string{}
			}
			cand.def.fieldProv[field] = agreed
		}
	}
}
