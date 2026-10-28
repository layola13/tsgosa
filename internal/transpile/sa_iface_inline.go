// Inline-scope swap (iface_inline, JEV link_install_swap 0.97/0.96).
//
// Method bodies are lexically scoped to their defining file, but
// saInlineMethodCore lowers them in the CALLER's scope object: only
// thisSelf/thisClass were swapped, every other namespace map stayed
// caller-side (dynamic scoping). Same-file programs cannot tell;
// cross-file programs can, and the difference was silent (s_rebind:
// lib's body resolved Real to the caller's imported Real, 99 instead
// of 7) or loud-refusing (x_method_direct*, s_cross_topconst)
// depending on whether the caller happened to import a colliding name.
//
// This file implements the defining-namespace install: at
// saInlineMethodCore entry, AFTER arg evaluation (args are caller
// expressions) and BEFORE body lowering, the defining file's
// harvested namespace replaces the caller maps, with save/restore
// around saCallbackValue on ALL paths. Strictness is structural:
// caller maps are swapped OUT, so there is no fallback path for free
// names. A body referencing a name its defining file itself imported
// from a third file refuses loudly (JEV inline_third_import strict
// 0.95): per-file import resolutions are not retained after lowering,
// so defining-file imports are not installable.
//
// RETAINED caller-side (untouched by the swap): scope.types (locals +
// params; body locals are declared during lowering and cleaned by
// saScopeExit; params are snapshot-bound by saCallbackValue),
// ownState/ownOrder (ownership/drain), ifaceProv/ifaceLitFn (body-local
// prov created during lowering), thisSelf/thisClass (already swapped),
// linkHarvests (program-wide; the import-first advisory stays available
// as a diagnostic), topConstSeed (only read inside a linkResolve-guarded
// branch, which cannot fire while linkResolve is swapped out),
// mathAlias, and all lowering state (strPool, src, tcx, pendingFns,
// arrowSeq, keyElems, instFn, futureVals, ...).
//
// The swap publishes NO prov (JEV inline_file_scope same_file_only
// 0.86): it is name resolution, not prov publication. Getters get NO
// swap (saInlineGetter untouched). Emitter-only (JEV inline_sci_split
// 0.99/0.98): no sci work.
//
// Origin determination (JEV inline_inherited origin_stamp 0.75/0.50
// MODERATE, narrowed per tension T1): v1 uses pointer-search origin
// for NON-INHERITED shapes (saClassDef carries no file field; seeded
// defs are shared pointers, so pointer-searching classHarvests finds
// the defining file; same-file is a no-op). Detected
// inherited-cross-file shapes get NO swap (today's behavior stands).
// FULL origin stamping (method->file map filled at inherited-copy
// time) lands WITH the inherited-cross-file follow-up slice, not v1.
// If build evidence shows the narrowing unworkable, the build STOPS
// for a re-ask; it does not silently install the stamp in v1.
//
// NAMESPACE-MEMBER RECEIVERS (JEV inline_ns_receiver fail_closed_ns
// 0.56 / conf 0.34 WEAK, adopted + flagged; live alternative
// receiver_alias 0.39 NOT adopted): install NO swap when the receiver
// is namespaced. A namespace-member def is harvested under a DOTTED
// key (N.C) while its caller-side name is the under key (N_C), so the
// installed classes map cannot contain scope.thisClass and saInstBase
// fails on `this` (247_zod_combo: V.Strict, inherited-same-file
// install, classesHasThisClass=N). The dotted harvest key is the
// discriminator: plain class names cannot contain dots, so a plain
// `My_Class` is unaffected, unlike any underscore heuristic. With no
// swap the body lowers in caller scope, exactly as the pre-swap
// binary did (zod returns to refused=false, value 111, sha
// aed506c8...). Namespaced inline scope is a later slice, not a v1
// regression.

package transpile

import (
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// saInlineFindDefFile pointer-searches link.classHarvests for the file
// whose harvested class def pointer equals def. Exactly one match is
// required; zero (unharvested def: single-file mode, synthesized or
// namespace-member defs) or multiple (ambiguous) both fail closed.
func saInlineFindDefFile(link *saFileLink, def *saClassDef) (string, bool) {
	if link == nil || def == nil || len(link.classHarvests) == 0 {
		return "", false
	}
	found := ""
	files := make([]string, 0, len(link.classHarvests))
	for f := range link.classHarvests {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		for _, pc := range link.classHarvests[f] {
			if pc.def == def {
				if found != "" {
					return "", false
				}
				found = f
			}
		}
	}
	if found == "" {
		return "", false
	}
	return found, true
}

// saInlineResolveParentDef resolves a parent class name to its def:
// first the caller-side classes map (own classes + imported seeds),
// then a by-name search of classHarvests (exactly one file defining
// that class name; ambiguous or absent fails).
func saInlineResolveParentDef(scope *saScope, link *saFileLink, parent string) (*saClassDef, bool) {
	if d, ok := scope.classes[parent]; ok && d != nil {
		return d, true
	}
	if link == nil || len(link.classHarvests) == 0 {
		return nil, false
	}
	var found *saClassDef
	files := make([]string, 0, len(link.classHarvests))
	for f := range link.classHarvests {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if pc, ok := link.classHarvests[f][parent]; ok && pc.def != nil {
			if found != nil {
				return nil, false
			}
			found = pc.def
		}
	}
	if found == nil {
		return nil, false
	}
	return found, true
}

// saInlineDefHarvestKey returns the classHarvests key under which def
// is harvested, requiring exactly one match program-wide (mirroring
// saInlineFindDefFile's uniqueness rule); zero or multiple matches
// fail closed. Namespace-member defs harvest under DOTTED keys
// (N.C); plain classes under plain names. A dotted key therefore
// proves the receiver is namespaced, without any underscore
// heuristic (a plain `My_Class` harvests under `My_Class`, dot-free).
func saInlineDefHarvestKey(link *saFileLink, def *saClassDef) (string, bool) {
	if link == nil || def == nil || len(link.classHarvests) == 0 {
		return "", false
	}
	found := ""
	files := make([]string, 0, len(link.classHarvests))
	for f := range link.classHarvests {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		for key, pc := range link.classHarvests[f] {
			if pc.def == def {
				if found != "" {
					return "", false
				}
				found = key
			}
		}
	}
	if found == "" {
		return "", false
	}
	return found, true
}

// saInlineOrigin determines the defining file for a method inline.
// It returns (origin, swap, reason): swap is true only for cross-file
// non-inherited shapes, where the swap must be installed; every other
// outcome (same-file no-op, undetermined origin, detected
// inherited-cross-file) leaves today's behavior untouched.
func saInlineOrigin(scope *saScope, def *saClassDef, mn *ast.Node, method string) (string, bool, string) {
	if scope == nil || scope.link == nil || def == nil || mn == nil {
		return "", false, "no-link-or-def"
	}
	// The core is also reached for litFn methods (sa_expr.go:3573),
	// whose node comes from a literal table, not from the receiver
	// def: swapping those to the interface's file would resolve the
	// literal body's names in the WRONG file. Only nodes actually
	// held by the def (own or inherited-copied) are eligible.
	if node, ok := def.methods[method]; !ok || node != mn {
		if node, ok := def.staticMethods[method]; !ok || node != mn {
			return "", false, "node-not-in-def"
		}
	}
	link := scope.link
	cur := link.self
	if cur == "" {
		return "", false, "no-self"
	}
	recvFile, ok := saInlineFindDefFile(link, def)
	if !ok {
		return "", false, "receiver-not-harvested"
	}
	if recvFile == cur {
		return recvFile, false, "same-file-noop"
	}
	// Inherited detection: walk the parent chain from the receiver
	// def. The nearest ancestor whose methods/staticMethods map holds
	// the SAME method node pointer is where the method was written
	// (overrides shadow: the receiver's own node never equals an
	// ancestor's entry). A different-file ancestor means
	// inherited-cross-file: no swap (T1 narrowing).
	seen := map[*saClassDef]bool{def: true}
	pdef := def
	for pdef.parent != "" {
		parent, ok := saInlineResolveParentDef(scope, link, pdef.parent)
		if !ok || parent == nil || seen[parent] {
			break
		}
		seen[parent] = true
		var hit bool
		if node, ok := parent.methods[method]; ok && node == mn {
			hit = true
		}
		if node, ok := parent.staticMethods[method]; ok && node == mn {
			hit = true
		}
		if hit {
			pfile, ok := saInlineFindDefFile(link, parent)
			if !ok {
				return recvFile, false, "inherited-parent-unharvested"
			}
			if pfile != recvFile {
				return recvFile, false, "inherited-cross-file"
			}
			// Namespaced-receiver fail-closed (JEV fail_closed_ns):
			// a dotted harvest key proves the receiver def is a
			// namespace member, whose installed classes map cannot
			// contain the caller-side under name for `this`.
			if key, ok := saInlineDefHarvestKey(link, def); ok && strings.Contains(key, ".") {
				return recvFile, false, "namespaced-receiver"
			}
			return recvFile, true, "inherited-same-file"
		}
		pdef = parent
	}
	// Namespaced-receiver fail-closed (JEV fail_closed_ns), same
	// reason as above: no swap when the receiver is namespaced.
	if key, ok := saInlineDefHarvestKey(link, def); ok && strings.Contains(key, ".") {
		return recvFile, false, "namespaced-receiver"
	}
	return recvFile, true, "cross-file"
}

// saInlineScopeSaved holds the caller maps displaced by the defining
// namespace install, for exact restore after body lowering.
type saInlineScopeSaved struct {
	funcs        map[string]saFuncSig
	enums        map[string]map[string]int64
	enumNonInt   map[string]map[string]bool
	classes      map[string]*saClassDef
	topConsts    map[string]string
	topStr       map[string]bool
	aliasOf      map[string]*ast.TypeNode
	topArrs      map[string]saTopArr
	topObjs      map[string]saTopObj
	topRegex     map[string]saTopRegex
	topSets      map[string]saTopSet
	topMaps      map[string]saTopMap
	topJSONs     map[string]saTopJSON
	modVars      map[string]*saModState
	imports      map[string]string
	importRemote map[string]string
	defPrefix    string
	linkResolve  map[string]string
}

// saInlineInstallDefiningScope installs the defining file's harvested
// namespace into the scope maps (fresh maps built from harvests, so
// body-lowering writes never touch the shared harvests or the caller
// maps) and returns the saved caller maps for restore. It returns nil
// when no swap is to be installed (same-file, undetermined origin, or
// missing prefix entry: without F's prefix, helper calls could not be
// emitted qualified, so fail closed).
//
// Install set (JEV link_install_swap): harvested funcs + class defs +
// folded consts (+ string marks) + enums (+ non-int marks) + aliases
// + top snapshots (arr/obj/regex/map) from the defining file's link
// harvests. Uninstalled snapshot classes (topSets, topJSONs) and
// namespace/module maps (modVars, imports, importRemote) are CLEARED,
// not left caller-side: §7 forbids an uninstalled class falling back
// to caller scope, and defining-file imports (including ns/builtin
// imports) are third-file imports under strict, which refuse loudly.
// defPrefix becomes F's prefix so helper calls emit qualified
// (saLinkCallee: linkResolve first, else defPrefix+name); linkResolve
// becomes a fresh empty map so caller imports can neither shadow
// defining names nor resolve defining-file imports.
func saInlineInstallDefiningScope(scope *saScope, origin string) *saInlineScopeSaved {
	link := scope.link
	if link == nil {
		return nil
	}
	pfx, ok := link.prefixOf[origin]
	if !ok {
		return nil
	}
	saved := &saInlineScopeSaved{
		funcs: scope.funcs, enums: scope.enums, enumNonInt: scope.enumNonInt,
		classes: scope.classes, topConsts: scope.topConsts, topStr: scope.topStr,
		aliasOf: scope.aliasOf, topArrs: scope.topArrs, topObjs: scope.topObjs,
		topRegex: scope.topRegex, topSets: scope.topSets, topMaps: scope.topMaps,
		topJSONs: scope.topJSONs, modVars: scope.modVars, imports: scope.imports,
		importRemote: scope.importRemote, defPrefix: scope.defPrefix,
		linkResolve: scope.linkResolve,
	}
	funcs := map[string]saFuncSig{}
	for name, pf := range link.harvests[origin] {
		funcs[name] = pf.sig
	}
	scope.funcs = funcs
	classes := map[string]*saClassDef{}
	for name, pc := range link.classHarvests[origin] {
		classes[name] = pc.def
	}
	scope.classes = classes
	topConsts := map[string]string{}
	topStr := map[string]bool{}
	for name, pc := range link.constHarvests[origin] {
		topConsts[name] = pc.text
		if pc.isStr {
			topStr[name] = true
		}
	}
	scope.topConsts = topConsts
	scope.topStr = topStr
	enums := map[string]map[string]int64{}
	enumNonInt := map[string]map[string]bool{}
	for name, pe := range link.enumHarvests[origin] {
		enums[name] = pe.members
		enumNonInt[name] = pe.nonInt
	}
	scope.enums = enums
	scope.enumNonInt = enumNonInt
	aliasOf := map[string]*ast.TypeNode{}
	for name, pa := range link.aliasHarvests[origin] {
		aliasOf[name] = pa.node
	}
	scope.aliasOf = aliasOf
	topArrs := map[string]saTopArr{}
	for name, v := range link.topArrHarvests[origin] {
		topArrs[name] = v
	}
	scope.topArrs = topArrs
	topObjs := map[string]saTopObj{}
	for name, v := range link.topObjHarvests[origin] {
		topObjs[name] = v
	}
	scope.topObjs = topObjs
	topRegex := map[string]saTopRegex{}
	for name, v := range link.topRegexHarvests[origin] {
		topRegex[name] = v
	}
	scope.topRegex = topRegex
	topMaps := map[string]saTopMap{}
	for name, v := range link.topMapHarvests[origin] {
		topMaps[name] = v
	}
	scope.topMaps = topMaps
	// No harvests exist for these classes: clear, do not fall back.
	scope.topSets = map[string]saTopSet{}
	scope.topJSONs = map[string]saTopJSON{}
	scope.modVars = map[string]*saModState{}
	scope.imports = map[string]string{}
	scope.importRemote = map[string]string{}
	scope.defPrefix = pfx
	scope.linkResolve = map[string]string{}
	return saved
}

// restore puts the saved caller maps back, unconditionally: body
// lowering may have written into the installed maps, and those writes
// must never survive into subsequent caller statements.
func (s *saInlineScopeSaved) restore(scope *saScope) {
	if s == nil || scope == nil {
		return
	}
	scope.funcs = s.funcs
	scope.enums = s.enums
	scope.enumNonInt = s.enumNonInt
	scope.classes = s.classes
	scope.topConsts = s.topConsts
	scope.topStr = s.topStr
	scope.aliasOf = s.aliasOf
	scope.topArrs = s.topArrs
	scope.topObjs = s.topObjs
	scope.topRegex = s.topRegex
	scope.topSets = s.topSets
	scope.topMaps = s.topMaps
	scope.topJSONs = s.topJSONs
	scope.modVars = s.modVars
	scope.imports = s.imports
	scope.importRemote = s.importRemote
	scope.defPrefix = s.defPrefix
	scope.linkResolve = s.linkResolve
}
