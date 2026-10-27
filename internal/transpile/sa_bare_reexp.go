// Bare-spec value re-export chain (2b_reexport).
//
// A value re-export through a bare spec (e.g.
// `export { resolveCliSpawn as resolveCodexSpawn } from "@cxu/sdk-backend"`)
// fails the driver's base-resolver edge: there is no node_modules tree and
// the base resolver carries no tsconfig "paths", so no reexpOf edge exists
// and hook C2 refuses the statement with "re-exports link in a later stage".
//
// This pass chases the chain the way the xfile type harvest does — tsconfig
// "paths" / package.json "file:" resolution via saResolveBareTypeFile — but
// for VALUES. It follows the barrel's export-from edges on disk to the file
// that actually defines each re-exported name, adopts ONLY that defining
// file into the program graph (never the whole barrel closure), and records
// synthetic reexpOf edges plus graph edges, so hook C2's saProgChase and
// downstream importer binding resolve exactly like a relative re-export.
//
// A name whose chain fails (unresolvable spec, missing export, cycle, depth
// cap, workspace-escape guard) records nothing and keeps refusing loudly at
// C2, exactly as today. Bare value IMPORTS are out of scope (Phase 3
// warnings unchanged); type-only re-exports are out of scope (the
// type-forward path is untouched); namespace re-exports (`export * as ns`)
// are out of scope (they are not star re-exports).
//
// JEV (jev-1.14.0): driver_chain_like_xfile_type 0.60 vs
// bind_time_from_reexporter_claims 0.37 (confidence 0.40, a modest
// plurality — adopted as the plurality choice, not a strong verdict),
// bare_reexport_needed 0.84, scope bare_value_only 1.0.

package transpile

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// saBareReexpDef is one adopted defining file for a star re-export.
type saBareReexpDef struct {
	key string
	abs string
}

// saBareReexpDotDots counts the leading "../" segments of a program key.
// The workspace writer joins outDir/packages/<mod>/src with the key, so a
// key with more than 3 leading ".." segments would escape the output dir;
// such targets are left unlinked (loud refusal preserved).
func saBareReexpDotDots(key string) int {
	n := 0
	for _, seg := range strings.Split(key, "/") {
		if seg == ".." {
			n++
			continue
		}
		break
	}
	return n
}

// saBareReexpParse parses one on-disk file, cached by absolute path. Files
// already in the program graph are never re-parsed (the caller prefers the
// graph's parsed AST); this cache covers barrels and defining files that
// live outside the walked package dir.
func saBareReexpParse(parseCache map[string]*ast.SourceFile, abs string) *ast.SourceFile {
	if sf, ok := parseCache[abs]; ok {
		return sf
	}
	text, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	kind := core.ScriptKindTS
	if strings.HasSuffix(abs, ".tsx") {
		kind = core.ScriptKindTSX
	} else if saIsJSPath(abs) {
		kind = core.ScriptKindJS
	}
	sf := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: abs, Path: tspath.ToPath(abs, "/", true)}, string(text), kind)
	if sf == nil {
		return nil
	}
	parseCache[abs] = sf
	return sf
}

// saBareReexpCurrent resolves (importerKey, spec) to the target file's
// program key, absolute path, and AST. Relative specs resolve against the
// importer key's directory on disk (.js falls back to .ts, index files
// included, mirroring saBareTypeFirstFile); bare specs resolve through the
// xfile resolver (tsconfig paths / package.json file: deps).
func saBareReexpCurrent(dirAbs, importerKey, spec string, parsed map[string]*ast.SourceFile, parseCache map[string]*ast.SourceFile) (curKey, curAbs string, sf *ast.SourceFile, ok bool) {
	importerAbsDir := filepath.Join(dirAbs, filepath.Dir(filepath.FromSlash(importerKey)))
	var abs string
	if strings.HasPrefix(spec, ".") {
		abs = saBareTypeFirstFile(filepath.Join(importerAbsDir, filepath.FromSlash(spec)))
	} else {
		abs = saResolveBareTypeFile(importerAbsDir, spec)
	}
	if abs == "" {
		return "", "", nil, false
	}
	rel, err := filepath.Rel(dirAbs, abs)
	if err != nil {
		return "", "", nil, false
	}
	curKey = path.Clean(filepath.ToSlash(rel))
	curAbs = abs
	if existing, inGraph := parsed[curKey]; inGraph && existing != nil {
		return curKey, curAbs, existing, true
	}
	sf = saBareReexpParse(parseCache, curAbs)
	if sf == nil {
		return "", "", nil, false
	}
	return curKey, curAbs, sf, true
}

// saBareReexpTargetAbs resolves a specifier found inside the file at
// parentAbs to an absolute target path ("" when unresolvable).
func saBareReexpTargetAbs(parentAbs, spec string) string {
	if strings.HasPrefix(spec, ".") {
		return saBareTypeFirstFile(filepath.Join(filepath.Dir(parentAbs), spec))
	}
	if strings.HasPrefix(spec, "#") {
		return ""
	}
	return saResolveBareTypeFile(filepath.Dir(parentAbs), spec)
}

// saBareReexpChase follows a bare re-export chain to the file that defines
// the wanted value name. It returns the defining file's program key, the
// harvest name importers must look up there, and its absolute path.
// Direct exported definitions win over local-list aliases, which win over
// import-then-export shims, which win over from-form edges in file order
// (named edges for the wanted name, then star edges first-match) — the same
// precedence the type chase and TS star semantics use. seen guards cycles;
// depth caps long barrel chains.
func saBareReexpChase(dirAbs string, parsed map[string]*ast.SourceFile, parseCache map[string]*ast.SourceFile, importerKey, spec, want string, depth int, seen map[string]bool) (defKey, defName, defAbs string, ok bool) {
	if depth > 16 || want == "" {
		return "", "", "", false
	}
	curKey, curAbs, sf, ok2 := saBareReexpCurrent(dirAbs, importerKey, spec, parsed, parseCache)
	if !ok2 || sf == nil {
		return "", "", "", false
	}
	seenKey := curKey + "\x00" + want
	if seen[seenKey] {
		return "", "", "", false
	}
	seen[seenKey] = true
	stmts := sf.AsSourceFile().Statements.Nodes
	localEdges := map[string]string{}
	type impEdge struct{ spec, remote string }
	importEdges := map[string]impEdge{}
	defined := map[string]bool{}
	definedDefault := false
	for _, st := range stmts {
		if st == nil {
			continue
		}
		switch st.Kind {
		case ast.KindExportDeclaration:
			ed := st.AsExportDeclaration()
			if ed == nil || ed.ModuleSpecifier != nil {
				continue
			}
			if edges, star := saProgLocalEdges(st); !star {
				for exp, loc := range edges {
					localEdges[exp] = loc
				}
			}
		case ast.KindImportDeclaration:
			imp := st.AsImportDeclaration()
			if imp == nil || imp.ImportClause == nil || imp.ModuleSpecifier == nil || imp.ModuleSpecifier.Kind != ast.KindStringLiteral {
				continue
			}
			sp := imp.ModuleSpecifier.Text()
			cl := imp.ImportClause.AsImportClause()
			if cl == nil {
				continue
			}
			if nm := cl.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				importEdges[nm.Text()] = impEdge{sp, "default"}
			}
			if nb := cl.NamedBindings; nb != nil && nb.Kind == ast.KindNamedImports {
				if ni := nb.AsNamedImports(); ni != nil && ni.Elements != nil {
					for _, n := range ni.Elements.Nodes {
						if n == nil || n.Kind != ast.KindImportSpecifier {
							continue
						}
						isp := n.AsImportSpecifier()
						nm := n.Name()
						if nm == nil || nm.Kind != ast.KindIdentifier {
							continue
						}
						local := nm.Text()
						remote := local
						if isp != nil && isp.PropertyName != nil {
							remote = isp.PropertyName.Text()
						}
						importEdges[local] = impEdge{sp, remote}
					}
				}
			}
		case ast.KindFunctionDeclaration, ast.KindClassDeclaration:
			nm := st.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			if ast.HasModifier(st, ast.ModifierFlagsExport) {
				defined[nm.Text()] = true
			}
			if ast.HasModifier(st, ast.ModifierFlagsDefault) {
				definedDefault = true
			}
		case ast.KindVariableStatement:
			if !ast.HasModifier(st, ast.ModifierFlagsExport) {
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
			for _, d := range vdl.Declarations.Nodes {
				vd := d.AsVariableDeclaration()
				if vd == nil {
					continue
				}
				nm := vd.Name()
				if nm != nil && nm.Kind == ast.KindIdentifier {
					defined[nm.Text()] = true
				}
			}
		}
	}
	if want == "default" {
		if definedDefault {
			return curKey, "default", curAbs, true
		}
	} else if defined[want] {
		return curKey, want, curAbs, true
	} else if loc, ok3 := localEdges[want]; ok3 {
		if defined[loc] {
			return curKey, want, curAbs, true
		}
		if ie, ok4 := importEdges[loc]; ok4 {
			return saBareReexpChase(dirAbs, parsed, parseCache, curKey, ie.spec, ie.remote, depth+1, seen)
		}
	}
	for _, st := range stmts {
		if st == nil || st.Kind != ast.KindExportDeclaration {
			continue
		}
		ed := st.AsExportDeclaration()
		if ed == nil {
			continue
		}
		if ed.ExportClause != nil && ed.ExportClause.Kind == ast.KindNamespaceExport {
			continue
		}
		spec2, edges2, star2 := saProgReexpEdges(st)
		if spec2 == "" {
			continue
		}
		if star2 {
			if dk, dn, da, ok5 := saBareReexpChase(dirAbs, parsed, parseCache, curKey, spec2, want, depth+1, seen); ok5 {
				return dk, dn, da, true
			}
			continue
		}
		if remote, ok6 := edges2[want]; ok6 {
			if dk, dn, da, ok5 := saBareReexpChase(dirAbs, parsed, parseCache, curKey, spec2, remote, depth+1, seen); ok5 {
				return dk, dn, da, true
			}
		}
	}
	return "", "", "", false
}

// saBareReexpStarDefs collects the defining files of a star re-export's
// barrel closure: every file reachable through the barrel's export-from
// edges (named and star alike — `export *` re-exports all named exports of
// its target, however the target acquired them) that directly defines at
// least one exported value (function, class, exported const, or local-list
// export name). Type-only files are traversed but contribute no defs.
// Bounded: 64 files, depth 8.
func saBareReexpStarDefs(dirAbs string, parsed map[string]*ast.SourceFile, parseCache map[string]*ast.SourceFile, importerKey, spec string) []saBareReexpDef {
	curKey, curAbs, sf, ok := saBareReexpCurrent(dirAbs, importerKey, spec, parsed, parseCache)
	if !ok || sf == nil {
		return nil
	}
	type work struct {
		key   string
		abs   string
		sf    *ast.SourceFile
		depth int
	}
	queue := []work{{curKey, curAbs, sf, 0}}
	visited := map[string]bool{curAbs: true}
	seenDef := map[string]bool{}
	var defs []saBareReexpDef
	for len(queue) > 0 && len(defs) < 64 {
		w := queue[0]
		queue = queue[1:]
		definesValue := false
		for _, st := range w.sf.AsSourceFile().Statements.Nodes {
			if st == nil {
				continue
			}
			switch st.Kind {
			case ast.KindFunctionDeclaration, ast.KindClassDeclaration:
				if nm := st.Name(); nm != nil && nm.Kind == ast.KindIdentifier && ast.HasModifier(st, ast.ModifierFlagsExport) {
					definesValue = true
				}
			case ast.KindVariableStatement:
				if ast.HasModifier(st, ast.ModifierFlagsExport) {
					definesValue = true
				}
			case ast.KindExportDeclaration:
				ed := st.AsExportDeclaration()
				if ed == nil || ed.ModuleSpecifier != nil {
					continue
				}
				if edges, star := saProgLocalEdges(st); !star && len(edges) > 0 {
					definesValue = true
				}
			}
			if definesValue {
				break
			}
		}
		if definesValue && !seenDef[w.key] {
			seenDef[w.key] = true
			defs = append(defs, saBareReexpDef{key: w.key, abs: w.abs})
		}
		if w.depth >= 8 {
			continue
		}
		for _, st := range w.sf.AsSourceFile().Statements.Nodes {
			if st == nil || st.Kind != ast.KindExportDeclaration {
				continue
			}
			ed := st.AsExportDeclaration()
			if ed == nil {
				continue
			}
			if ed.ExportClause != nil && ed.ExportClause.Kind == ast.KindNamespaceExport {
				continue
			}
			spec2, _, _ := saProgReexpEdges(st)
			if spec2 == "" {
				continue
			}
			tgtAbs := saBareReexpTargetAbs(w.abs, spec2)
			if tgtAbs == "" || visited[tgtAbs] {
				continue
			}
			visited[tgtAbs] = true
			rel, err := filepath.Rel(dirAbs, tgtAbs)
			if err != nil {
				continue
			}
			tgtKey := path.Clean(filepath.ToSlash(rel))
			var tgtSf *ast.SourceFile
			if existing, inGraph := parsed[tgtKey]; inGraph && existing != nil {
				tgtSf = existing
			} else {
				tgtSf = saBareReexpParse(parseCache, tgtAbs)
			}
			if tgtSf == nil {
				continue
			}
			queue = append(queue, work{tgtKey, tgtAbs, tgtSf, w.depth + 1})
		}
	}
	return defs
}

// saBareReexpLink is the driver pre-pass for bare-spec value re-exports.
// It runs after saProgIncludeBare and before the edge loop, over a snapshot
// of the program keys (adopted files are not re-scanned; their chains were
// already followed on disk). It writes synthetic reexpOf/starOf edges and
// graph edges for exactly the chains it resolves; everything else is left
// for the edge loop's existing behavior (unlinked, loud at C2).
func saBareReexpLink(dirAbs string, files map[string]string, parsed map[string]*ast.SourceFile, rs *saProgResolvers, graph map[string][]string, reexpOf map[string]map[string]string, starOf map[string][]string) {
	parseCache := map[string]*ast.SourceFile{}
	adopted := 0
	adopt := func(defKey, defAbs string) bool {
		if _, ok := files[defKey]; ok {
			return true
		}
		if adopted >= 64 || saBareReexpDotDots(defKey) > 3 {
			return false
		}
		text, err := os.ReadFile(defAbs)
		if err != nil {
			return false
		}
		kind := core.ScriptKindTS
		if strings.HasSuffix(defAbs, ".tsx") {
			kind = core.ScriptKindTSX
		} else if saIsJSPath(defAbs) {
			kind = core.ScriptKindJS
		}
		sf := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: defAbs, Path: tspath.ToPath(defAbs, "/", true)}, string(text), kind)
		if sf == nil {
			return false
		}
		files[defKey] = string(text)
		parsed[defKey] = sf
		adopted++
		return true
	}
	addGraph := func(p, tgt string) {
		for _, q := range graph[p] {
			if q == tgt {
				return
			}
		}
		graph[p] = append(graph[p], tgt)
	}
	keys := make([]string, 0, len(parsed))
	for p := range parsed {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		sf := parsed[p]
		if sf == nil {
			continue
		}
		for _, st := range sf.AsSourceFile().Statements.Nodes {
			if st == nil || st.Kind != ast.KindExportDeclaration {
				continue
			}
			ed := st.AsExportDeclaration()
			if ed == nil || ed.IsTypeOnly {
				continue
			}
			if ed.ExportClause != nil && ed.ExportClause.Kind == ast.KindNamespaceExport {
				continue
			}
			spec, edges, star := saProgReexpEdges(st)
			if spec == "" || strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "#") || saProgBuiltinMod(spec) {
				continue
			}
			if rs != nil && saProgResolveBare(p, spec, files, dirAbs, rs, false) != "" {
				continue
			}
			if star {
				for _, d := range saBareReexpStarDefs(dirAbs, parsed, parseCache, p, spec) {
					if !adopt(d.key, d.abs) {
						continue
					}
					addGraph(p, d.key)
					dup := false
					for _, q := range starOf[p] {
						if q == d.key {
							dup = true
							break
						}
					}
					if !dup {
						starOf[p] = append(starOf[p], d.key)
					}
				}
				continue
			}
			names := make([]string, 0, len(edges))
			for exp := range edges {
				names = append(names, exp)
			}
			sort.Strings(names)
			for _, exp := range names {
				dk, dn, da, ok := saBareReexpChase(dirAbs, parsed, parseCache, p, spec, edges[exp], 0, map[string]bool{})
				if !ok {
					continue
				}
				if !adopt(dk, da) {
					continue
				}
				if reexpOf[p] == nil {
					reexpOf[p] = map[string]string{}
				}
				reexpOf[p][exp] = dk + "\x00" + dn
				addGraph(p, dk)
			}
		}
	}
}
