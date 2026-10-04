// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/debug"
	"github.com/microsoft/typescript-go/internal/module"
	"github.com/microsoft/typescript-go/internal/packagejson"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/printer"
	"github.com/microsoft/typescript-go/internal/transformers"
	"github.com/microsoft/typescript-go/internal/transformers/jsxtransforms"
	"github.com/microsoft/typescript-go/internal/transformers/tstransforms"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// Options configures single-file transpilation.
type Options struct {
	// CompilerOptions are the base compiler options to use for the transpilation.
	// If nil, a default set of compiler options is used. Regardless of what is
	// provided, a number of options are unconditionally overridden; see
	// [TranspileModule] and [TranspileDeclaration].
	CompilerOptions *core.CompilerOptions

	// FileName is the name given to the synthesized input file. It only needs to
	// be provided if the source text relies on characteristics implied by the
	// file's extension or path, e.g. its extension controls whether the file is
	// parsed as a script or module, whether JSX syntax is allowed, etc.
	// Defaults to "module.ts", or "module.tsx" if CompilerOptions.Jsx is set.
	FileName string

	// ReportDiagnostics indicates whether syntactic and compiler option
	// diagnostics should be included in the result. Regardless of this setting,
	// diagnostics produced while emitting (including declaration emit errors
	// such as those produced by isolated declarations) are always included.
	ReportDiagnostics bool
}

// Output contains the emitted text and any requested diagnostics.
type Output struct {
	OutputText    string
	Diagnostics   []*ast.Diagnostic
	SourceMapText string
}

// inputDirectory is the synthetic current directory used to root the
// single input file created for transpilation.
const inputDirectory = "/"

// libDirectory is the synthetic directory that the barebones default library
// file is placed in for declaration transpilation. See [barebonesLibContent].
const libDirectory = "/lib"

// Declaration emit works without a `lib`, but some local inferences you'd
// expect to work won't without at least a minimal `lib` available, since the
// checker will type inferred declarations as `any` without these defined.
// Late bound symbol names, in particular, are impossible to define without
// `Symbol` at least partially defined.
// TODO: This should *probably* just load the full, real `lib` for the target.
const barebonesLibContent = `interface Boolean {}
interface Function {}
interface CallableFunction {}
interface NewableFunction {}
interface IArguments {}
interface Number {}
interface Object {}
interface RegExp {}
interface String {}
interface Array<T> { length: number; [n: number]: T; }
interface SymbolConstructor {
    (desc?: string | number): symbol;
    for(name: string): symbol;
    readonly toStringTag: symbol;
}
declare var Symbol: SymbolConstructor;
interface Symbol {
    readonly [Symbol.toStringTag]: string;
}`

// TranspileModule transpiles a single file of source text to JavaScript
// using the specified options. If no compiler options are provided, a
// default set of compiler options is used. It returns nil if the context is
// canceled before emission completes.
//
// Extra compiler options that are unconditionally used by this function are:
//   - IsolatedModules = true (unless VerbatimModuleSyntax is set, which makes
//     this option redundant)
//   - NoCheck = true
//   - NoResolve = true
//   - NoLib = true
//   - Declaration = false
//   - DeclarationMap = false
func TranspileModule(ctx context.Context, input string, options Options) *Output {
	return transpileWorker(ctx, input, options, false /*declaration*/)
}

// TranspileDeclaration creates a declaration (.d.ts) file from a single file
// of source text using the specified options. If no compiler options are
// provided, a default set of compiler options is used.
//
// Note that, because only the single input file is available, the resulting
// declaration file may differ from the one a full program type-check and
// emit would produce.
//
// Extra compiler options that are unconditionally used by this function are:
//   - IsolatedModules = true (unless VerbatimModuleSyntax is set, which makes
//     this option redundant)
//   - NoCheck = true
//   - NoResolve = true
//   - NoLib = false
//   - Declaration = true
//   - EmitDeclarationOnly = true
//   - IsolatedDeclarations = true
func TranspileDeclaration(ctx context.Context, input string, options Options) *Output {
	return transpileWorker(ctx, input, options, true /*declaration*/)
}

func transpileWorker(ctx context.Context, input string, options Options, declaration bool) *Output {
	var opts *core.CompilerOptions
	if options.CompilerOptions != nil {
		opts = options.CompilerOptions.Clone()
	} else {
		opts = &core.CompilerOptions{}
	}

	// Clear options that do not apply to single-file transpilation.
	opts.Incremental = core.TSUnknown
	opts.Declaration = core.TSUnknown
	opts.EmitDeclarationOnly = core.TSUnknown
	opts.NoEmit = core.TSUnknown
	opts.Lib = nil
	opts.OutFile = ""
	opts.Composite = core.TSUnknown
	opts.TsBuildInfoFile = ""
	opts.Paths = nil
	opts.RootDirs = nil
	opts.Types = nil
	opts.AllowImportingTsExtensions = core.TSUnknown
	opts.NoEmitOnError = core.TSUnknown
	opts.DeclarationDir = ""

	// Do not set `isolatedModules` if `verbatimModuleSyntax` was supplied, since
	// it would be redundant.
	if !opts.VerbatimModuleSyntax.IsTrue() {
		opts.IsolatedModules = core.TSTrue
	}
	opts.NoCheck = core.TSTrue
	opts.NoResolve = core.TSTrue

	// transpileModule/transpileDeclaration do not write anything to disk, so
	// there's no need to verify there are no conflicts between input and
	// output paths.
	opts.SuppressOutputPathCheck = core.TSTrue

	// FileName can be a non-ts file.
	opts.AllowNonTsExtensions = core.TSTrue

	if declaration {
		opts.Declaration = core.TSTrue
		opts.EmitDeclarationOnly = core.TSTrue
		opts.IsolatedDeclarations = core.TSTrue
	} else {
		opts.Declaration = core.TSFalse
		opts.DeclarationMap = core.TSFalse
	}

	// When transpiling declarations, we need a lib. GetDefaultLibFileName will
	// cause the barebones lib below to be used instead of a real lib.
	if declaration {
		opts.NoLib = core.TSFalse
	} else {
		opts.NoLib = core.TSTrue
	}

	// If jsx is specified, then treat the file as .tsx.
	fileName := options.FileName
	if fileName == "" {
		if opts.Jsx != core.JsxEmitNone {
			fileName = "module.tsx"
		} else {
			fileName = "module.ts"
		}
	}
	inputFileName := tspath.GetNormalizedAbsolutePath(fileName, inputDirectory)

	files := map[string]string{
		inputFileName: input,
	}

	// Declaration emit needs a default lib to resolve global types (e.g.
	// `Array`, `Symbol`); plain transpilation sets NoLib so none is read.
	// The default lib name depends on the configured target.
	if declaration {
		libFileName := tsoptions.GetDefaultLibFileName(opts)
		files[tspath.CombinePaths(libDirectory, libFileName)] = barebonesLibContent
	}

	fs := vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/)
	host := compiler.NewCompilerHost(inputDirectory, fs, libDirectory, nil, nil, nil)

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config: &tsoptions.ParsedCommandLine{
			ParsedConfig: &tsoptions.ParsedOptions{
				FileNames:       []string{inputFileName},
				CompilerOptions: opts,
			},
		},
		Host: host,
	})

	var allDiagnostics []*ast.Diagnostic
	if options.ReportDiagnostics {
		sourceFile := program.GetSourceFile(inputFileName)
		allDiagnostics = append(allDiagnostics, program.GetSyntacticDiagnostics(ctx, sourceFile)...)
		allDiagnostics = append(allDiagnostics, program.GetConfigFileParsingDiagnostics()...)
		allDiagnostics = append(allDiagnostics, program.GetProgramDiagnostics()...)
	}

	emitOnly := compiler.EmitAll
	if declaration {
		emitOnly = compiler.EmitOnlyDts
	}

	var outputText, sourceMapText string
	var hasOutputText, hasSourceMapText bool
	result := program.Emit(ctx, compiler.EmitOptions{
		EmitOnly:  emitOnly,
		ForceEmit: declaration,
		WriteFile: func(fileName string, text string, data *compiler.WriteFileData) error {
			if strings.HasSuffix(fileName, ".map") {
				debug.Assert(!hasSourceMapText, "Unexpected multiple source map outputs, file: "+fileName)
				sourceMapText = text
				hasSourceMapText = true
			} else {
				debug.Assert(!hasOutputText, "Unexpected multiple outputs, file: "+fileName)
				outputText = text
				hasOutputText = true
			}
			return nil
		},
	})
	if result == nil {
		return nil
	}

	// Diagnostics produced during emit (e.g. isolated declaration errors) are
	// always included, regardless of ReportDiagnostics.
	allDiagnostics = append(allDiagnostics, result.Diagnostics...)

	debug.Assert(hasOutputText, "Output generation failed")

	return &Output{
		OutputText:    outputText,
		Diagnostics:   allDiagnostics,
		SourceMapText: sourceMapText,
	}
}

// ── ts→sa: 复用本文件单文件口径，在已有架构内替换 JS 落字为 SA 落字 ──
// 上游对齐：建 Program 同 transpileWorker（IsolatedModules/NoResolve/NoLib）；
// 中转同 compiler/emitter.go#getScriptTransformers 前三段
// （TypeEraser→RuntimeSyntax→JSX，未做 ES 降级/模块重写，SA 需保留模块形态）；
// 落字将 printer JS 文本换成 printer.NewTextWriter SA 文本 + sci 宏
// （textwriter.go:218 NewTextWriter；reuse 思想见 satsgo 封存说明）。
// 铁律：本文件为已有文件，禁止再新建 internal/saemit；main.go 只做薄分发。

const saStepHeader = "// Generated by tsgo-sa step2: analyze(native program) -> middle(native transformers) -> emit(SA text + sci macros)\n"

// SARefusal 是定位拒绝（永不静默错码）。
type SARefusal struct {
	Line int
	Col  int
	Msg  string
}

// SAOutput 是单文件 ts→sa 结果。
type SAOutput struct {
	SAI         string
	Refusals    []SARefusal
	Warnings    []SARefusal
	Diagnostics []*ast.Diagnostic
}

// TranspileSA 将单文件 TS 转为 sci/sa 可装配的 .sai（step1+step2 子集）。
// step1：顶层 function f(): void {}/return; → @f(): ret；
// number/boolean 字面返回 → @f() -> i32: ret lit；其余定位拒绝。
// step2：if/else → EXPAND IF_ELSE/IF_TRUE + @import "sa_std/control.sal"；
// return c?a:b（i32 字面臂）→ EXPAND SELECT；false 恒假消死臂；嵌套 if 带 jmp。
func TranspileSA(ctx context.Context, input string, options Options) *SAOutput {
	return transpileSAInner(ctx, input, options, nil)
}

// transpileSAInner is TranspileSA with an optional program-link environment
// (nil = single-file). Program builds lower reachable files through this
// in dependency order with per-file link env.
func transpileSAInner(ctx context.Context, input string, options Options, link *saFileLink) *SAOutput {
	var opts *core.CompilerOptions
	if options.CompilerOptions != nil {
		opts = options.CompilerOptions.Clone()
	} else {
		opts = &core.CompilerOptions{}
	}
	opts.Incremental = core.TSUnknown
	opts.Declaration = core.TSUnknown
	opts.EmitDeclarationOnly = core.TSUnknown
	opts.NoEmit = core.TSUnknown
	opts.Lib = nil
	opts.OutFile = ""
	opts.Composite = core.TSUnknown
	opts.TsBuildInfoFile = ""
	opts.Paths = nil
	opts.RootDirs = nil
	opts.Types = nil
	opts.AllowImportingTsExtensions = core.TSUnknown
	opts.NoEmitOnError = core.TSUnknown
	opts.DeclarationDir = ""
	if !opts.VerbatimModuleSyntax.IsTrue() {
		opts.IsolatedModules = core.TSTrue
	}
	opts.NoCheck = core.TSTrue
	opts.NoResolve = core.TSTrue
	opts.SuppressOutputPathCheck = core.TSTrue
	opts.AllowNonTsExtensions = core.TSTrue
	opts.Declaration = core.TSFalse
	opts.DeclarationMap = core.TSFalse
	opts.NoLib = core.TSTrue

	fileName := options.FileName
	if fileName == "" {
		if opts.Jsx != core.JsxEmitNone {
			fileName = "module.tsx"
		} else {
			fileName = "module.ts"
		}
	}
	inputFileName := tspath.GetNormalizedAbsolutePath(fileName, inputDirectory)
	files := map[string]string{inputFileName: input}
	fs := vfstest.FromMap(files, true)
	host := compiler.NewCompilerHost(inputDirectory, fs, libDirectory, nil, nil, nil)
	program := compiler.NewProgram(compiler.ProgramOptions{
		Config: &tsoptions.ParsedCommandLine{
			ParsedConfig: &tsoptions.ParsedOptions{
				FileNames:       []string{inputFileName},
				CompilerOptions: opts,
			},
		},
		Host: host,
	})
	sf := program.GetSourceFile(inputFileName)
	if sf == nil {
		return &SAOutput{Refusals: []SARefusal{{Line: 1, Col: 1, Msg: "no source file"}}}
	}
	// 中转：原生 TypeEraser→RuntimeSyntax→JSX（影子模式，失败回退原文件）。
	cur := sf
	func() {
		defer func() { _ = recover() }()
		emitCtx := printer.NewEmitContext()
		topts := &transformers.TransformOptions{
			Context:         emitCtx,
			CompilerOptions: program.Options(),
			Resolver:        nil,
			EmitResolver:    nil,
			GetEmitModuleFormatOfFile: func(f ast.HasFileName) core.ModuleKind {
				return program.GetEmitModuleFormatOfFile(f)
			},
		}
		cur = tstransforms.NewTypeEraserTransformer(topts).TransformSourceFile(cur)
		cur = tstransforms.NewRuntimeSyntaxTransformer(topts).TransformSourceFile(cur)
		if sf.LanguageVariant == core.LanguageVariantJSX {
			cur = jsxtransforms.NewJSXTransformer(topts).TransformSourceFile(cur)
		}
		if cur == nil {
			cur = sf
		}
	}()
	_ = cur
	// 返回类型 checker 推断 ctx（复用本 program 的 checker，与 sf 节点同源；
	// 失败回退 void 门；形状证据：封存 newTypeCtx + prescanRet）。
	var tcx *saTypeCtx
	func() {
		defer func() { _ = recover() }()
		if c, done := program.GetTypeChecker(ctx); c != nil {
			tcx = &saTypeCtx{check: c, done: done}
			return
		}
	}()
	if tcx != nil {
		defer tcx.close()
	}
	sai, refusals, warnings := saLowerSourceFile(sf, input, tcx, link)
	return &SAOutput{SAI: sai, Refusals: refusals, Warnings: warnings}
}

func saLineOffsets(src string) []int {
	offs := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			offs = append(offs, i+1)
		}
	}
	return offs
}

func saPos(offs []int, p int) (int, int) {
	line, start := 1, 0
	for i, off := range offs {
		if off > p {
			break
		}
		line, start = i+1, off
	}
	return line, p - start + 1
}

// saFileLink carries per-file program-link environment (nil = single-file
// lowering, all behavior unchanged). Shape evidence: upstream LowerProgram
// prefixOf + links[p].resolved + seeded funcSigs + PerFile harvest.
type saFileLink struct {
	defPrefix     string                            // definition prefix ("" entry; "util__" libs)
	isEntry       bool                              // entry synthesizes @main; libs refuse top-level execution
	self          string                            // this file key (for own re-export edges)
	specOf        map[string]string                 // this file: import spec -> target file
	prefixOf      map[string]string                 // all files: target -> prefix
	harvests      map[string]map[string]saProgFunc  // all files: target -> name -> harvested (driver fills)
	reexps        map[string]map[string]string      // all files: target -> exported -> "tgt\x00remote" (driver fills; upstream reexp edge 同形）
	stars         map[string][]string               // all files: target -> star-from targets in order (upstream starFrom 同形）
	resolve       map[string]string                 // out/in: imported local name -> qualified callee
	seed          map[string]saFuncSig              // out/in: qualified callee -> defining file signature
	harvest       map[string]saProgFunc             // out: own top-level functions for dependents
	classHarvest  map[string]saProgClass            // out: own top-level classes for dependents
	classHarvests map[string]map[string]saProgClass // all files: target -> name -> harvested class (driver fills)
	classSeed     map[string]*saClassDef            // out/in: imported local class name -> defining layout
}

// saProgFunc is one harvested top-level function for cross-file linking.
// Default exports harvest under key "default" with defLocal naming the
// defining declaration (upstream fileExports.defLocal 同形；qualified 后缀
// 用定义名，见 program.go:26/defQualified:98）。
type saProgFunc struct {
	sig      saFuncSig
	exported bool
	isArrow  bool
	defLocal string // defining name for "default" entries ("" otherwise)
}

// saProgClass is one harvested top-level class for cross-file linking
// (upstream program.go sharedClassDefs + expOf exports 同形；布局指针跨文件
// 共享只读消费，方法内联即定义体 AST；heritage 跨文件另步，默认/命名空间
// 成员类沿旧门后阶段）。
type saProgClass struct {
	def      *saClassDef
	exported bool
}

// saLowerSourceFile 发射 SA 文本（后端为 printer.NewTextWriter，替换 JS 落字）。
// saLinkDefPrefix returns the definition prefix for link mode ("" when nil).
func saLinkDefPrefix(link *saFileLink) string {
	if link == nil {
		return ""
	}
	return link.defPrefix
}

// saScopeLinkFill copies program-link environment into a fresh scope;
// saScopeLinkCopy inherits it from a parent scope. Single-file lowering
// leaves both empty (nil link / nil parent fields).
func saScopeLinkFill(scope *saScope, link *saFileLink) {
	if scope == nil || link == nil {
		return
	}
	scope.defPrefix = link.defPrefix
	scope.linkResolve = link.resolve
	scope.linkHarvests = link.harvests
}

func saScopeLinkCopy(dst, src *saScope) {
	if dst == nil || src == nil {
		return
	}
	dst.defPrefix = src.defPrefix
	dst.linkResolve = src.linkResolve
	dst.linkHarvests = src.linkHarvests
}

// saLinkResolveMap returns the link resolve map for link mode (nil when nil).
func saLinkResolveMap(link *saFileLink) map[string]string {
	if link == nil {
		return nil
	}
	return link.resolve
}

// saLinkHarvestsMap returns all harvested program functions for link mode
// (nil when nil; driver fills progressively in dependency order).
func saLinkHarvestsMap(link *saFileLink) map[string]map[string]saProgFunc {
	if link == nil {
		return nil
	}
	return link.harvests
}

// saProgChase resolves (tgt, remote) to a qualified callee through re-export
// edges (upstream resolveReExports:1317 同形；cycle 经 seen 守卫，未导出/
// 箭头/断链一律 !ok 由调用方按形拒因）。remote=="default" 用 defLocal 后缀。
func saProgChase(link *saFileLink, tgt, remote string, seen map[string]bool) (string, saFuncSig, bool) {
	key := tgt + "\x00" + remote
	if seen[key] {
		return "", saFuncSig{}, false
	}
	seen[key] = true
	if hv, ok := link.harvests[tgt][remote]; ok {
		if !hv.exported || hv.isArrow {
			return "", saFuncSig{}, false
		}
		name := remote
		if remote == "default" {
			if hv.defLocal == "" {
				return "", saFuncSig{}, false
			}
			name = hv.defLocal
		}
		return link.prefixOf[tgt] + name, hv.sig, true
	}
	if edge, ok := link.reexps[tgt][remote]; ok {
		parts := strings.SplitN(edge, "\x00", 2)
		if len(parts) == 2 {
			return saProgChase(link, parts[0], parts[1], seen)
		}
	}
	// Star re-exports (first match wins, locals shadowed above; upstream
	// resolveReExports star 分支同形）。
	for _, st := range link.stars[tgt] {
		if q, sig, ok := saProgChase(link, st, remote, seen); ok {
			return q, sig, true
		}
	}
	return "", saFuncSig{}, false
}

// saProgReexpEdges parses `export {a [, b as c]} from "spec"` into the
// spec plus exported->remote edges (star forms report star=true, S1 后阶段；
// 形状证据：externalmoduleinfo.go:186-200 Name/PropertyNameOrName 读法）。
func saProgReexpEdges(st *ast.Node) (string, map[string]string, bool) {
	ed := st.AsExportDeclaration()
	if ed == nil || ed.IsTypeOnly {
		return "", nil, false
	}
	ms := ed.ModuleSpecifier
	if ms == nil || ms.Kind != ast.KindStringLiteral {
		return "", nil, false
	}
	spec := ms.Text()
	if ed.ExportClause == nil {
		return spec, nil, true
	}
	if ed.ExportClause.Kind == ast.KindNamespaceExport {
		return spec, nil, true
	}
	if ed.ExportClause.Kind != ast.KindNamedExports {
		return spec, nil, true
	}
	ne := ed.ExportClause.AsNamedExports()
	if ne == nil || ne.Elements == nil {
		return spec, nil, false
	}
	edges := map[string]string{}
	for _, n := range ne.Elements.Nodes {
		if n == nil || n.Kind != ast.KindExportSpecifier {
			continue
		}
		sp := n.AsExportSpecifier()
		if sp == nil || sp.IsTypeOnly {
			continue
		}
		nm := n.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		remote := nm.Text()
		if pn := sp.PropertyNameOrName(); pn != nil && pn.Kind == ast.KindIdentifier {
			remote = pn.Text()
		}
		edges[nm.Text()] = remote
	}
	return spec, edges, false
}

// saProgLocalEdges parses `export {a [, b as c]}` (no from) into
// exported->local edges (star/empty/absent yield nil, true).
func saProgLocalEdges(st *ast.Node) (map[string]string, bool) {
	ed := st.AsExportDeclaration()
	if ed == nil || ed.IsTypeOnly || ed.ModuleSpecifier != nil {
		return nil, true
	}
	if ed.ExportClause == nil || ed.ExportClause.Kind != ast.KindNamedExports {
		return nil, true
	}
	ne := ed.ExportClause.AsNamedExports()
	if ne == nil || ne.Elements == nil {
		return nil, true
	}
	edges := map[string]string{}
	for _, n := range ne.Elements.Nodes {
		if n == nil || n.Kind != ast.KindExportSpecifier {
			continue
		}
		sp := n.AsExportSpecifier()
		if sp == nil || sp.IsTypeOnly {
			continue
		}
		nm := n.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		local := nm.Text()
		if pn := sp.PropertyNameOrName(); pn != nil && pn.Kind == ast.KindIdentifier {
			local = pn.Text()
		}
		edges[nm.Text()] = local
	}
	if len(edges) == 0 {
		return nil, true
	}
	return edges, false
}

// saBindProgNsMembers 绑定具名命名空间导入（`import { N }`）：把收割到的
// `N.<成员>` 逐个记入 link.resolve（调用点经既有点式 linkResolve 路由；
// 未导出/箭头/无发射名成员永不绑定；零绑定返回 false 下探旧门）。
// 形状证据：上游 link_namespace.go bindNSMembers 点键同形。
func saBindProgNsMembers(link *saFileLink, tgt, remote, local string) bool {
	if link == nil {
		return false
	}
	members, ok := link.harvests[tgt]
	if !ok || len(members) == 0 {
		return false
	}
	prefix, ok := link.prefixOf[tgt]
	if !ok {
		return false
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	bound := false
	for _, name := range names {
		hv := members[name]
		member, ok := strings.CutPrefix(name, remote+".")
		if !ok || member == "" || !hv.exported || hv.isArrow || hv.defLocal == "" {
			continue
		}
		if link.resolve == nil {
			link.resolve = map[string]string{}
		}
		if link.seed == nil {
			link.seed = map[string]saFuncSig{}
		}
		q := prefix + hv.defLocal
		link.resolve[local+"."+member] = q
		link.seed[q] = hv.sig
		bound = true
	}
	return bound
}

// saBindProgImports binds one relative named import to qualified callees
// (signatures seeded from the defining file, lowered earlier in dependency
// order). Returns true when claimed (emission skips via handledTop).
// Namespace/default forms refuse loudly (later stage); unresolvable targets
// continue silently (use sites refuse); bare third-party specs warn here
// (single-file step157 同形：未使用即警告过，使用处自然拒；driver 侧聚合
// Unresolved). Shape evidence: upstream LowerProgram
// links[p].resolved + recordNamedImports + linkRoute advisories.
func saBindProgImports(st *ast.Node, link *saFileLink, pos func(int) (int, int), refusals *[]SARefusal, warnings *[]SARefusal) bool {
	imp := st.AsImportDeclaration()
	if imp == nil || imp.ImportClause == nil {
		return false
	}
	if cl := imp.ImportClause; cl != nil && cl.IsTypeOnly() {
		return false
	}
	ms := imp.ModuleSpecifier
	if ms == nil || ms.Kind != ast.KindStringLiteral {
		return false
	}
	spec := ms.Text()
	if len(spec) > 0 && spec[0] != '.' {
		// 已链接 bare（node_modules 按需纳入的真实目标）：下探正常绑定；
		// 其余 bare 警告（driver 聚合 Unresolved；用点自然拒）。
		if _, ok := link.specOf[spec]; !ok {
			ln, col := pos(st.Pos())
			*warnings = append(*warnings, SARefusal{Line: ln, Col: col, Msg: "import " + spec + " is not resolvable (bare third-party imports are Phase 3; see todo/03_npm.md)"})
			return true
		}
	}
	clause := imp.ImportClause.AsImportClause()
	if clause == nil {
		return false
	}
	if nm := clause.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
		// 默认导入直链定义文件 `export default function Name`（上游 program
		// build 同形过，本仓 S1 前拒；其余默认形/箭头/未导出沿旧门后阶段；
		// 重导出链经 saProgChase 透传）。混合 clause（`import d, {x}`）
		// 默认部处理完落through命名分支（上游同语句双绑）。
		if tgt, ok := link.specOf[spec]; ok && tgt != "" {
			if q, sig, ok := saProgChase(link, tgt, "default", map[string]bool{}); ok {
				link.resolve[nm.Text()] = q
				link.seed[q] = sig
			} else {
				ln, col := pos(st.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "default imports link in a later stage"})
			}
		}
		if nb := clause.NamedBindings; nb == nil || (nb.Kind != ast.KindNamedImports && nb.Kind != ast.KindNamespaceImport) {
			return true
		}
	}
	nb := clause.NamedBindings
	if nb == nil {
		return false
	}
	if nb.Kind == ast.KindNamespaceImport {
		// 命名空间逐成员直链（namespace 本身无值；每个可链成员绑
		// `本地.成员` qualified；上游 link_namespace.go bindNSMembers 同形；
		// 不可解目标/零可链成员沿旧门后阶段）。
		if tgt, ok := link.specOf[spec]; ok && tgt != "" {
			bound := false
			for member, hv := range link.harvests[tgt] {
				if member == "default" || !hv.exported || hv.isArrow {
					continue
				}
				local := nb.AsNamespaceImport().Name().Text()
				if local == "" {
					continue
				}
				q := link.prefixOf[tgt] + member
				link.resolve[local+"."+member] = q
				link.seed[q] = hv.sig
				bound = true
			}
			if bound {
				return true
			}
		} else {
			// Unresolvable relative target: no edge, no binding (use sites
			// refuse); bare specs never reach here (warned above).
			return true
		}
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "namespace imports link in a later stage"})
		return true
	}
	if nb.Kind != ast.KindNamedImports {
		return false
	}
	ni := nb.AsNamedImports()
	if ni == nil || ni.Elements == nil {
		return false
	}
	tgt, ok := link.specOf[spec]
	if !ok || tgt == "" {
		// Unresolvable target: no edge, no binding (use sites refuse).
		return true
	}
	prefix := link.prefixOf[tgt]
	for _, n := range ni.Elements.Nodes {
		if n == nil || n.Kind != ast.KindImportSpecifier {
			continue
		}
		sp := n.AsImportSpecifier()
		nm := n.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		local := nm.Text()
		remote := local
		if sp.PropertyName != nil {
			remote = sp.PropertyName.Text()
		}
		hv, ok := link.harvests[tgt][remote]
		if !ok {
			// 类直链：具名导入类名命中定义文件收割即播种布局（本地名直挂
			// 调用点 `new`/方法内联复用实例通道；未导出沿"未导出"门；
			// 默认/命名空间成员类/reexp 透传沿旧门后阶段）。
			if ch, ok := link.classHarvests[tgt][remote]; ok {
				if !ch.exported || ch.def == nil {
					ln, col := pos(n.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: remote + " is not exported by " + spec})
					continue
				}
				if link.classSeed == nil {
					link.classSeed = map[string]*saClassDef{}
				}
				link.classSeed[local] = ch.def
				continue
			}
			// 命名空间整件直链（`import { N }` + `N.f()` 经成员点键绑定；
			// 零可链成员下探重导出透传/未导出门；上游 bindNSMembers 同形）。
			if saBindProgNsMembers(link, tgt, remote, local) {
				continue
			}
			// 重导出透传（`export {a} from` 链；cycle/断链下探"未导出"门）。
			if q, sig, ok := saProgChase(link, tgt, remote, map[string]bool{}); ok {
				link.resolve[local] = q
				link.seed[q] = sig
				continue
			}
			ln, col := pos(n.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: remote + " is not exported by " + spec})
			continue
		}
		if !hv.exported {
			ln, col := pos(n.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: remote + " is not exported by " + spec})
			continue
		}
		if hv.isArrow {
			ln, col := pos(n.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: remote + " is not a linkable function (arrow consts link in a later stage)"})
			continue
		}
		q := prefix + remote
		link.resolve[local] = q
		link.seed[q] = hv.sig
	}
	return true
}

func saLowerSourceFile(sf *ast.SourceFile, src string, tcx *saTypeCtx, link *saFileLink) (string, []SARefusal, []SARefusal) {
	w := printer.NewTextWriter("\n", 2)
	var refusals []SARefusal
	var warnings []SARefusal
	var importOrder []string
	importSeen := map[string]bool{}
	needImport := func(path string) {
		if !importSeen[path] {
			importSeen[path] = true
			importOrder = append(importOrder, path)
		}
	}
	offs := saLineOffsets(src)
	pos := func(p int) (int, int) { return saPos(offs, p) }
	nextLabel := 1
	nextTemp := 1
	// 文件级 out-of-line 局部箭头缓冲（封存 pendingFuncs:336，末尾 :576-579 排空）。
	var pendingFns []string
	arrowSeq := 0
	// 文件级类型别名表（`type X = …`；注解消解用，无码；封存 link_erasure
	// 纯类型擦除 + lowerVarDeclList:1462 注解丢弃/初值生效——本仓沿既有注解
	// enforcement，仅对非泛型标量别名做等价消解，其余沿旧门大声拒）。
	aliasOf := saCollectTypeAliases(sf)
	// 预扫顶层函数签名（调用核：被调函数须同文件定义，元数精确匹配；
	// 证据：封存 program.go:435/512 按定义收集 rets/arity）。
	funcs := map[string]saFuncSig{}
	imports := map[string]string{}
	importRemote := map[string]string{}
	enums := map[string]map[string]int64{}
	enumNonInt := map[string]map[string]bool{}
	classes := map[string]*saClassDef{}
	// 跨文件 heritage 预播种（导入类布局须在本地记录期前可见；单文件 link
	// 空零行为变；形状证据见 saPreseedImportedClasses）。
	saPreseedImportedClasses(sf, classes, link)
	// 预扫一：类型表（类/接口/枚举；函数签名引用须先行）。
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind == ast.KindClassDeclaration {
			// 类定义预扫成表（布局记录、无码；方法随调用内联）。
			saRecordClass(st, classes, pos, &refusals)
			continue
		}
		// 命名空间成员类预扫成表（`N_C` 限定布局；同 ns 内 heritage 基另步；
		// 形状证据：封存 recordClassNamed:9586-9611 具名记录全形）。
		if ns, members, ok := saNsLowerableMembers(st); ok {
			for _, m := range members {
				if m == nil || m.Kind != ast.KindClassDeclaration {
					continue
				}
				mn := m.Name()
				if mn == nil || mn.Kind != ast.KindIdentifier {
					continue
				}
				saRecordClassNamed(m, ns+"_"+mn.Text(), classes, pos, &refusals)
			}
			continue
		}
		// 顶层类表达式预扫成表（`const C = class...` 绑定名记录，
		// 自身具名记别名；无码；形状证据：封存 recordClassNamed:9586-9611）。
		if bound, ce, ok := saTopLevelClassExpr(st); ok {
			saRecordClassNamed(ce, bound, classes, pos, &refusals)
			continue
		}
		if st.Kind == ast.KindInterfaceDeclaration {
			// 接口布局预扫成表（对象字面量匹配用；无码）。
			saRecordIface(st, classes, pos, &refusals)
			continue
		}
		if st.Kind == ast.KindEnumDeclaration {
			// 整数枚举预扫成表（布局记录、无码；形状证据：封存 lowerTypeDecl:9242-9253）。
			nm := st.AsEnumDeclaration().Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				continue
			}
			members, nonInt, msg := saEnumMembers(st)
			if msg != "" {
				ln, col := pos(st.Pos())
				refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				continue
			}
			enums[nm.Text()] = members
			if len(nonInt) > 0 {
				enumNonInt[nm.Text()] = nonInt
			}
			continue
		}
	}
	// 预扫二：函数签名（形参种含实例注解，须类型表先行）。
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind == ast.KindClassDeclaration || st.Kind == ast.KindInterfaceDeclaration ||
			st.Kind == ast.KindEnumDeclaration {
			continue
		}
		if st.Kind != ast.KindFunctionDeclaration {
			continue
		}
		fn := st.AsFunctionDeclaration()
		nm := fn.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			continue
		}
		// 重载签名擦除：无体声明不注册签名，实现体唯一注册/发射；
		// 孤签名定义无名，调用点按未知函数诚实拒。
		// 证据：satsgo saemit.go:642（预扫跳过）+ :758（发射跳过）+ :983（无体拒）；
		// 上游 JS 管线同形擦除（printer 落字仅实现体）。
		if fn.Body == nil {
			continue
		}
		saPrescanFuncSig(fn, st, nm.Text(), funcs, tcx, classes, aliasOf, enums, pos, &refusals)
	}
	// 预扫二b：命名空间成员函数签名（`N_f` 发射键 + `N.f` 调用键双记；无体
	// 跳过；非函数/类/类型成员整块不在此（发射侧同门拒）；形状证据见 saNsLowerableMembers）。
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		ns, members, ok := saNsLowerableMembers(st)
		if !ok {
			continue
		}
		for _, m := range members {
			if m == nil || m.Kind != ast.KindFunctionDeclaration {
				continue
			}
			fn := m.AsFunctionDeclaration()
			if fn == nil || fn.Body == nil {
				continue
			}
			mn := fn.Name()
			if mn == nil || mn.Kind != ast.KindIdentifier {
				continue
			}
			if saPrescanFuncSig(fn, m, ns+"_"+mn.Text(), funcs, tcx, classes, aliasOf, enums, pos, &refusals) {
				saPrescanFuncSig(fn, m, ns+"."+mn.Text(), funcs, tcx, classes, aliasOf, enums, pos, &refusals)
			}
		}
	}
	// 预扫二b：顶层箭头/函数表达式 `const f = (...)=>...` 记调用签名
	// （与函数声明同表；形状证据：封存 tryTopLevelArrow:1015-1032 +
	// lowerArrowBinding:1058-1098）。重名（函数/先行箭头）同门大声拒。
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		name, arrow, ok := saIsTopLevelArrowConst(st)
		if !ok {
			continue
		}
		if _, dup := funcs[name]; dup {
			ln, col := pos(st.Pos())
			refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate function " + name})
			continue
		}
		var nodes []*ast.Node
		if pl := arrow.ParameterList(); pl != nil {
			nodes = pl.Nodes
		}
		nparams := len(nodes)
		isVoid := false
		retKind := ""
		if k, v, ok := saPrescanRet(saArrowReturnNode(arrow), arrow, tcx, classes, aliasOf); ok {
			retKind = k
			isVoid = v
		}
		var pk []string
		if _, kinds, _, ok := saSynthArrowParams(arrow, classes, aliasOf, enums); ok {
			if names, ok := saArrowParamNames(arrow); ok {
				for _, n := range names {
					pk = append(pk, kinds[n])
				}
			}
		}
		defs, dexprs := saFuncDefaultTables(nodes)
		funcs[name] = saFuncSig{params: nparams, isVoid: isVoid, retKind: retKind, paramKinds: pk, defaults: defs, defaultExprs: dexprs}
	}
	// Program hook A: harvest own top-level functions (+export flags) for
	// dependents; seed qualified signatures from defining files. Leaves
	// lower first (dependency order) so harvests exist when needed.
	if link != nil {
		if link.harvest == nil {
			link.harvest = map[string]saProgFunc{}
		}
		if link.resolve == nil {
			link.resolve = map[string]string{}
		}
		if link.seed == nil {
			link.seed = map[string]saFuncSig{}
		}
		for _, st := range sf.AsSourceFile().Statements.Nodes {
			if st == nil {
				continue
			}
			if st.Kind == ast.KindFunctionDeclaration {
				fn := st.AsFunctionDeclaration()
				if fn == nil || fn.Body == nil {
					continue
				}
				if nm := fn.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
					if sig, ok := funcs[nm.Text()]; ok {
						link.harvest[nm.Text()] = saProgFunc{sig: sig, exported: ast.HasModifier(st, ast.ModifierFlagsExport)}
						// 默认导出另记 "default" 键（定义名守 qualified 后缀；
						// 匿名默认无名可守，沿旧门）。
						if ast.HasModifier(st, ast.ModifierFlagsDefault) {
							link.harvest["default"] = saProgFunc{sig: sig, exported: ast.HasModifier(st, ast.ModifierFlagsExport), defLocal: nm.Text()}
						}
					}
				}
				continue
			}
			if name, _, ok := saIsTopLevelArrowConst(st); ok {
				if sig, ok := funcs[name]; ok {
					link.harvest[name] = saProgFunc{sig: sig, exported: ast.HasModifier(st, ast.ModifierFlagsExport), isArrow: true}
				}
			}
			// 类收割：具名类声明记布局指针 + export 旗（上游 program.go
			// sharedClassDefs + expOf 同形；类表达式/默认导出类/heritage 基
			// 跨文件另步，沿旧门）。
			if st.Kind == ast.KindClassDeclaration {
				if nm := st.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
					if def, ok := classes[nm.Text()]; ok && def != nil {
						if link.classHarvest == nil {
							link.classHarvest = map[string]saProgClass{}
						}
						link.classHarvest[nm.Text()] = saProgClass{def: def, exported: ast.HasModifier(st, ast.ModifierFlagsExport)}
					}
				}
			}
			// 命名空间成员函数收割（`N.f` 点键 + 发射名记 defLocal；定义侧
			// step194 发射 `@N_f`；上游 bindNSMembers 点键同形）。
			if ns, members, ok := saNsLowerableMembers(st); ok {
				for _, m := range members {
					if m == nil || m.Kind != ast.KindFunctionDeclaration {
						continue
					}
					fn := m.AsFunctionDeclaration()
					if fn == nil || fn.Body == nil {
						continue
					}
					mn := fn.Name()
					if mn == nil || mn.Kind != ast.KindIdentifier {
						continue
					}
					if sig, ok := funcs[ns+"_"+mn.Text()]; ok {
						link.harvest[ns+"."+mn.Text()] = saProgFunc{sig: sig, exported: ast.HasModifier(m, ast.ModifierFlagsExport), defLocal: ns + "_" + mn.Text()}
					}
				}
			}
		}
	}
	// 预扫二c：顶层可变槽登记（`let x = 1` 被赋值即入槽，声明无码；`const` 永不入槽；
	// 形状证据：封存 preRegisterModStates:593-616）+ 纯量折叠（未被认领者；被赋值名永不折叠，
	// 封存 modstate.go:26-28）。
	assigned := saAssignedNames(sf.AsSourceFile().Statements.Nodes)
	modVars := saRecordModStates(sf.AsSourceFile().Statements.Nodes, assigned, funcs, classes, pos, &refusals)
	topConsts := map[string]string{}
	topStr := map[string]bool{}
	topMaths := map[string]string{}
	handledTop := map[*ast.Node]bool{}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		// 槽绑定声明无码（封存 tryModState:618-635；注册期拒因已落袋，此处只消费）。
		if saTryModState(st, assigned) {
			handledTop[st] = true
			continue
		}
		if saFoldTopLevelConst(st, topConsts, topStr, topMaths, pos, &refusals) {
			handledTop[st] = true
			continue
		}
		// 单层运行时 namespace 纯量拍扁（`N.K` 键；函数/嵌套沿旧拒）。
		if saFoldNamespaceConsts(st, topConsts, topStr, pos, &refusals) {
			handledTop[st] = true
		}
	}

	// prescan zero: builtin projection imports (fs/net direct calls need no linking).
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st == nil || st.Kind != ast.KindImportDeclaration {
			continue
		}
		if saRecordProjImports(st, imports, importRemote) {
			handledTop[st] = true
			continue
		}
		// Program hook B: relative named imports bind qualified callees.
		if link != nil && saBindProgImports(st, link, pos, &refusals, &warnings) {
			handledTop[st] = true
		}
	}
	// Program hook C: seed qualified signatures into this file's table.
	if link != nil {
		for q, sig := range link.seed {
			funcs[q] = sig
		}
		// Program hook C-class: seed imported class layouts (local
		// definitions win; defining-file pointer shared read-only).
		for local, def := range link.classSeed {
			if def == nil {
				continue
			}
			if _, dup := classes[local]; !dup {
				classes[local] = def
			}
		}
		// Program hook C2: value export lists stay unlinked in S1
		// (named functions link via export modifier; star/default/lists
		// refuse loudly for a later stage). Resolvable from-form re-exports
		// emit nothing (edges chase at use sites).
		for _, st := range sf.AsSourceFile().Statements.Nodes {
			if st == nil || st.Kind != ast.KindExportDeclaration {
				continue
			}
			ed := st.AsExportDeclaration()
			if ed == nil || ed.IsTypeOnly {
				continue
			}
			if ed.ModuleSpecifier != nil {
				_, edges, star := saProgReexpEdges(st)
				if star {
					// star 目标已 lower（harvest 就绪）即认领无码；成员级
					// miss 在用点拒。
					ready := false
					for _, tgt := range link.stars[link.self] {
						if _, ok := link.harvests[tgt]; ok {
							ready = true
							break
						}
					}
					if ready {
						handledTop[st] = true
						continue
					}
				} else if len(edges) > 0 {
					okAll := true
					for exported := range edges {
						if _, _, ok := saProgChase(link, link.self, exported, map[string]bool{}); !ok {
							okAll = false
							break
						}
					}
					if okAll {
						handledTop[st] = true
						continue
					}
				}
				ln, col := pos(st.Pos())
				refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "re-exports link in a later stage"})
				continue
			}
			if ed.ExportClause != nil {
				// 本地名单：全员可 chase（自有 harvest 或进口边）即无码认领，
				// 否则沿旧门（命名空间值/断链无单值）。
				if edges, star := saProgLocalEdges(st); !star {
					okAll := true
					for exported := range edges {
						if _, _, ok := saProgChase(link, link.self, exported, map[string]bool{}); !ok {
							okAll = false
							break
						}
					}
					if okAll {
						handledTop[st] = true
						continue
					}
				}
				ln, col := pos(st.Pos())
				refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "export lists link in a later stage"})
			}
		}
	}
	strPool := &saStrPool{seen: map[string]string{}, prefix: saLinkDefPrefix(link)}
	emitted := map[string]bool{}
	// 入口合成规划：顶层执行语句聚入生成的 `@main() -> i32`（定义之后落字）；
	// 用户 `main` 遇合成改名 `main__user`（定义 + 调用点；`main__user` 已有则拒）。
	// 形状证据：封存 entry_top.go:1-152。
	var entryStmts []*ast.Node
	hasUserMain := false
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind == ast.KindFunctionDeclaration {
			if fn := st.AsFunctionDeclaration(); fn != nil {
				if fn.Body == nil {
					// 重载签名无体：不参与入口改名/碰撞（仅实现体定义）。
					continue
				}
			}
			if nm := st.AsFunctionDeclaration().Name(); nm != nil && nm.Kind == ast.KindIdentifier {
				if nm.Text() == "main" {
					hasUserMain = true
				}
				if nm.Text() == "main__user" {
					ln, col := pos(st.Pos())
					refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "entry synthesis collides with existing definition main__user (rename it)"})
				}
			}
			continue
		}
		// 顶层箭头 `const main = ...` 与函数声明同例参与入口改名/碰撞。
		if name, _, ok := saIsTopLevelArrowConst(st); ok {
			if name == "main" {
				hasUserMain = true
			}
			if name == "main__user" {
				ln, col := pos(st.Pos())
				refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "entry synthesis collides with existing definition main__user (rename it)"})
			}
			continue
		}
		if saIsEntryStmt(st) {
			// Program libs refuse top-level execution (entry synthesis is
			// entry-only; shape evidence: upstream progprobe "top-level
			// executable statements are not lowerable in non-entry
			// program files (move them into functions)").
			if link != nil && !link.isEntry {
				ln, col := pos(st.Pos())
				refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "top-level executable statements are not lowerable in non-entry program files (move them into functions)"})
				continue
			}
			entryStmts = append(entryStmts, st)
		}
	}
	mainRenamed := hasUserMain && len(entryStmts) > 0
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		// 类型声明擦除（记录、无码；形状证据：封存 lowerTypeDecl:9242-9253）。
		// export 修饰随声明擦除（单文件无模块边；`export default function`
		// 同形；`export {}`/`export =` 无码，镜像 lowerModuleDecl:10329-10336）。
		// 入口语句跳过定义流（聚入合成 `@main`）。
		if saIsEntryStmt(st) {
			continue
		}
		switch st.Kind {
		case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindEnumDeclaration,
			ast.KindExportDeclaration, ast.KindExportAssignment, ast.KindNamespaceExportDeclaration,
			ast.KindClassDeclaration:
			continue
		case ast.KindModuleDeclaration:
			// 环境模块块擦除（无运行时码）；已拍扁的运行时 namespace 纯量无码。
			if saIsAmbientModule(st) {
				continue
			}
			if handledTop[st] {
				continue
			}
			// 纯类型 namespace 整块擦除（成员皆接口/别名/枚举，无码；含值沿旧拒）。
			if saIsTypeOnlyNamespace(st) {
				continue
			}
			// 成员函数直落（`@N_f`；program 前缀由 defPrefix 续接；类型成员
			// 跳过；含值成员沿旧 kind-268 拒）。
			if ns, members, ok := saNsLowerableMembers(st); ok {
				for _, m := range members {
					if m == nil || m.Kind != ast.KindFunctionDeclaration {
						continue
					}
					if fn := m.AsFunctionDeclaration(); fn == nil || fn.Body == nil {
						continue
					}
					mn := m.Name()
					if mn == nil || mn.Kind != ast.KindIdentifier {
						continue
					}
					if emitted[ns+"_"+mn.Text()] {
						continue
					}
					emitted[ns+"_"+mn.Text()] = true
					saLowerFunction(w, m, funcs, enums, enumNonInt, classes, topConsts, topStr, topMaths, modVars, src, mainRenamed, pos, &refusals, needImport, &nextLabel, &nextTemp, strPool, tcx, &pendingFns, &arrowSeq, aliasOf, imports, importRemote, saLinkDefPrefix(link), saLinkResolveMap(link), saLinkHarvestsMap(link), ns+"_"+mn.Text())
				}
				continue
			}
		case ast.KindImportDeclaration:
			// builtin projection imports recorded in prescan emit nothing.
			if handledTop[st] {
				continue
			}
			imp := st.AsImportDeclaration()
			if cl := imp.ImportClause; cl != nil && cl.IsTypeOnly() {
				continue
			}
			// Non-builtin imports warn and continue (usage-erased imports
			// never reach resolution; use sites refuse naturally when the
			// names are actually referenced; shape evidence: upstream
			// recordImports "local module import %s recorded" warning).
			spec := ""
			if ms := imp.ModuleSpecifier; ms != nil && ms.Kind == ast.KindStringLiteral {
				spec = ms.Text()
			}
			ln, col := pos(st.Pos())
			warnings = append(warnings, SARefusal{Line: ln, Col: col, Msg: "local module import " + spec + " recorded; single-file lowering continues (multi-file link is Phase 2)"})
			continue
		}
		if st.Kind != ast.KindFunctionDeclaration {
			// 顶层类表达式纯记录（布局表），无码；与 ClassDeclaration 同例。
			if _, _, ok := saTopLevelClassExpr(st); ok {
				continue
			}
			// 顶层箭头 `const f = (...)=>...` 走函数同形发射（out-of-line；
			// 封存 tryTopLevelArrow + lowerArrowBinding）。其余变量声明仍拒。
			if name, arrow, ok := saIsTopLevelArrowConst(st); ok {
				if emitted[name] {
					continue
				}
				emitted[name] = true
				saLowerArrowConst(w, name, arrow, funcs, enums, enumNonInt, classes, topConsts, topStr, topMaths, modVars, src, mainRenamed, pos, &refusals, needImport, &nextLabel, &nextTemp, strPool, tcx, aliasOf, imports, importRemote, &pendingFns, &arrowSeq, link)
				continue
			}
			// 顶层纯量已在预扫折叠（无码；部分纯洁落下拒）。
			if handledTop[st] {
				continue
			}
			ln, col := pos(st.Pos())
			refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("step2 refuses kind %d (only top-level functions)", int(st.Kind))})
			continue
		}
		// 重载签名擦除：无体声明直接跳过（实现体唯一发射；孤签名零定义）。
		if fn0 := st.AsFunctionDeclaration(); fn0 == nil || fn0.Body == nil {
			continue
		}
		if nm := st.AsFunctionDeclaration().Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			if emitted[nm.Text()] {
				continue
			}
			emitted[nm.Text()] = true
		}
		saLowerFunction(w, st, funcs, enums, enumNonInt, classes, topConsts, topStr, topMaths, modVars, src, mainRenamed, pos, &refusals, needImport, &nextLabel, &nextTemp, strPool, tcx, &pendingFns, &arrowSeq, aliasOf, imports, importRemote, saLinkDefPrefix(link), saLinkResolveMap(link), saLinkHarvestsMap(link), "")
	}
	if len(entryStmts) > 0 {
		// 入口 `@main`（空作用域帧，i32 出口；缺尾返补 `ret 0`）。
		w.Write("@main() -> i32:\n")
		escope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, enumNonInt: enumNonInt, classes: classes, topConsts: topConsts, topStr: topStr, modVars: modVars, mainRenamed: mainRenamed, nextLabel: &nextLabel, retKind: "i32", strPool: strPool, src: src, addImport: needImport, tcx: tcx, pendingFns: &pendingFns, arrowSeq: &arrowSeq, aliasOf: aliasOf, imports: imports, importRemote: importRemote}
		saScopeLinkFill(escope, link)
		saSeedTopMaths(escope, topMaths)
		terminated := false
		for _, s := range entryStmts {
			if terminated {
				// 死码静默抑制（封存 lowerBlockStatement:820-823 同形；抑制非
				// 错译，活码不落此路）。
				continue
			}
			if done, failed := saLowerStmt(w, s, false, escope, pos, &refusals, needImport, &nextLabel, &nextTemp); failed {
				break
			} else if done {
				terminated = true
			}
		}
		if !terminated {
			saReleaseAllOwnedExcept(w, escope, "")
			w.Write("  ret 0\n")
		} else if saEndsWithBareSwitchLabel(entryStmts) {
			saReleaseAllOwnedExcept(w, escope, "")
			w.Write("  ret 0\n")
		}
	}
	var head strings.Builder
	head.WriteString(saStepHeader)
	for _, imp := range importOrder {
		head.WriteString("@import \"" + imp + "\"\n")
	}
	head.WriteString(strPool.buf.String())
	out := head.String() + w.String()
	// out-of-line 局部箭头排在全部定义之后（封存 :576-579 排空 pendingFuncs 同序）。
	for _, fn := range pendingFns {
		out += fn
	}
	return out, refusals, warnings
}

func saFuncName(fn *ast.FunctionDeclaration) (string, bool) {
	nm := fn.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", false
	}
	return nm.Text(), true
}

// saDestructurePending 记录一个绑定模式形参：隐藏句柄形参 + 体顶展开的模式。
// 形状证据：封存 destructurePending:5335-5341 + hiddenDestructuredParam:5347-5371
// + drainDestructuredParams:5376-5410（体顶 field-wise，与声明解构同形同拒）。
type saDestructurePending struct {
	hid   string
	pat   *ast.Node
	annot *ast.TypeNode
}

// saSynthParams 合成形参表（标识符直通；`{x,y}`/`[a,b]` 模式合成隐藏句柄形参；
// 尾 `...rest: T[]` 记 arr 收集句柄）。
// default/optional 形参仍大声拒（封存 :5349）。隐藏名对兄弟形参名唯一
// （`__darg` 冲突追 `_`，封存 :5359-5368）。模式种：数组注解 `i32[]` 记 arr，
// 类/接口注解记 `inst:Name`（调用点句柄直传，复用 arr/inst 求值位）；无注解数组
// 模式记 arr，无注解对象模式无布局可查、大声拒。
func saSynthParams(fn *ast.FunctionDeclaration, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, enums map[string]map[string]int64) ([]string, map[string]string, []saDestructurePending, bool) {
	var nodes []*ast.Node
	if fn.Parameters != nil {
		nodes = fn.Parameters.Nodes
	}
	return saSynthParamNodes(nodes, classes, aliasOf, enums, saTypeParamSet(fn.TypeParameters))
}

// saTypeParamSet collects own unconstrained type parameter names (generic erasure
// targets; constrained or defaulted params stay loud downstream).
// saIsBareTypeParam reports a bare reference to an own erased type parameter.
// saUnwrapPromise strips Promise<> layers (sync-subset erasure per JEV T1 verdict:
// unwrap to T, cf await-sync-unwrap; non-Promise passes through).
func saUnwrapPromise(t *ast.TypeNode) *ast.TypeNode {
	for t != nil && t.Kind == ast.KindTypeReference {
		ref := t.AsTypeReferenceNode()
		if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier ||
			ref.TypeName.Text() != "Promise" || ref.TypeArguments == nil || len(ref.TypeArguments.Nodes) != 1 ||
			ref.TypeArguments.Nodes[0] == nil {
			break
		}
		t = ref.TypeArguments.Nodes[0]
	}
	return t
}

func saIsBareTypeParam(t *ast.TypeNode, tparams map[string]bool) bool {
	if t == nil || t.Kind != ast.KindTypeReference {
		return false
	}
	ref := t.AsTypeReferenceNode()
	if ref == nil || ref.TypeName == nil || ref.TypeName.Kind != ast.KindIdentifier {
		return false
	}
	if ref.TypeArguments != nil {
		return false
	}
	return tparams[ref.TypeName.Text()]
}

func saTypeParamSet(list *ast.TypeParameterList) map[string]bool {
	out := map[string]bool{}
	if list == nil {
		return out
	}
	for _, tp := range list.Nodes {
		if tp == nil || tp.Kind != ast.KindTypeParameter {
			continue
		}
		pd := tp.AsTypeParameterDeclaration()
		if pd == nil {
			continue
		}
		if nm := tp.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			out[nm.Text()] = true
		}
	}
	return out
}

func saSynthParamNodes(paramNodes []*ast.Node, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, enums map[string]map[string]int64, tparams map[string]bool) ([]string, map[string]string, []saDestructurePending, bool) {
	kinds := map[string]string{}
	var pendings []saDestructurePending
	taken := map[string]bool{}
	for idx, p := range paramNodes {
		pd := p.AsParameterDeclaration()
		if pd == nil || pd.QuestionToken != nil {
			return nil, nil, nil, false
		}
		if pd.DotDotDotToken != nil {
			// trailing rest collects the packed slice (`...rest: number[]` records arr; last position plus
			// identifier plus array annotation or no annotation; other rest shapes refuse loudly).
			if idx != len(paramNodes)-1 {
				return nil, nil, nil, false
			}
			nm := pd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				return nil, nil, nil, false
			}
			if pd.Type != nil {
				k, ok := saAnnotKind(pd.Type)
				if !ok || k != "arr" {
					return nil, nil, nil, false
				}
			}
			taken[nm.Text()] = true
			kinds[nm.Text()] = "arr"
			continue
		}
		nm := pd.Name()
		if nm == nil {
			return nil, nil, nil, false
		}
		if nm.Kind != ast.KindIdentifier && pd.Initializer != nil {
			return nil, nil, nil, false
		}
		if nm.Kind == ast.KindIdentifier {
			name := nm.Text()
			taken[name] = true
			if pd.Type == nil {
				kinds[name] = "i32"
				continue
			}
			if pd.Type.Kind == ast.KindAnyKeyword || pd.Type.Kind == ast.KindUnknownKeyword {
				// `any` params default to i32 (cf tUnknown signature default).
				kinds[name] = "i32"
				continue
			}
			k, ok := saAnnotKind(pd.Type)
			if !ok {
				if pd.Type.Kind == ast.KindTypeReference {
					if ref := pd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
						if _, ok := classes[ref.TypeName.Text()]; ok {
							kinds[name] = "inst:" + ref.TypeName.Text()
							continue
						}
						// enum annotations lower as ptr handles (integer-enum equality folds to eq).
						if _, ok := enums[ref.TypeName.Text()]; ok {
							kinds[name] = "arr"
							continue
						}
						// erased own type parameters default to i32 (cf unannotated params).
						if tparams[ref.TypeName.Text()] {
							kinds[name] = "i32"
							continue
						}
					}
				}
				if pd.Type.Kind == ast.KindUnionType {
					// 非折叠联合形参缺省 i32（cf tUnknown signature default；可折叠已由 saAnnotKind 办）。
					kinds[name] = "i32"
					continue
				}
				// 标量别名经顶层别名表消解（`c: Count`；封存 annotationType
				// 别名词消解同形；inst 别名/未知沿旧门拒）。
				if ak, aok := saResolveAliasKind(pd.Type, aliasOf); aok {
					k, ok = ak, true
				} else {
					return nil, nil, nil, false
				}
			}
			if k != "i32" && k != "bool" && k != "arr" && k != "str" && k != "f64" {
				return nil, nil, nil, false
			}
			kinds[name] = k
			continue
		}
		if nm.Kind != ast.KindObjectBindingPattern && nm.Kind != ast.KindArrayBindingPattern {
			return nil, nil, nil, false
		}
		hid := "__darg"
		for taken[hid] {
			hid += "_"
		}
		taken[hid] = true
		kind := ""
		if pd.Type == nil {
			if nm.Kind == ast.KindArrayBindingPattern {
				kind = "arr"
			} else {
				return nil, nil, nil, false
			}
		} else if k, ok := saAnnotKind(pd.Type); ok && k == "arr" {
			kind = "arr"
		} else if pd.Type.Kind == ast.KindTypeReference {
			if ref := pd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
				if _, ok := classes[ref.TypeName.Text()]; ok {
					kind = "inst:" + ref.TypeName.Text()
				}
			}
			if kind == "" {
				return nil, nil, nil, false
			}
		} else {
			return nil, nil, nil, false
		}
		kinds[hid] = kind
		pendings = append(pendings, saDestructurePending{hid: hid, pat: nm.AsNode(), annot: pd.Type})
	}
	var out []string
	if paramNodes != nil {
		// 按形参顺序重建名表（隐藏名已占位，保证签名/预扫/调用元数一致）。
		taken2 := map[string]bool{}
		for _, p := range paramNodes {
			pd := p.AsParameterDeclaration()
			nm := pd.Name()
			if nm.Kind == ast.KindIdentifier {
				out = append(out, nm.Text())
				taken2[nm.Text()] = true
				continue
			}
			hid := "__darg"
			for taken2[hid] {
				hid += "_"
			}
			// 同 saSynthParams 上半的 taken 推进保持同序同名：兄弟标识符先占位。
			// 标识符已在 taken2 占位，隐藏名按序追 `_` 即与上半一致。
			taken2[hid] = true
			out = append(out, hid)
		}
	}
	return out, kinds, pendings, true
}

func saParamNames(fn *ast.FunctionDeclaration) ([]string, bool) {
	if fn.Parameters == nil {
		return nil, true
	}
	var out []string
	taken := map[string]bool{}
	for idx, p := range fn.Parameters.Nodes {
		pd := p.AsParameterDeclaration()
		if pd == nil || pd.QuestionToken != nil {
			return nil, false
		}
		if pd.DotDotDotToken != nil {
			// trailing rest passes through here; kind gate lives in saSynthParamNodes.
			if idx != len(fn.Parameters.Nodes)-1 {
				return nil, false
			}
			nm := pd.Name()
			if nm == nil || nm.Kind != ast.KindIdentifier {
				return nil, false
			}
			out = append(out, nm.Text())
			taken[nm.Text()] = true
			continue
		}
		nm := pd.Name()
		if nm == nil {
			return nil, false
		}
		if nm.Kind != ast.KindIdentifier && pd.Initializer != nil {
			return nil, false
		}
		if nm.Kind == ast.KindIdentifier {
			out = append(out, nm.Text())
			taken[nm.Text()] = true
			continue
		}
		if nm.Kind != ast.KindObjectBindingPattern && nm.Kind != ast.KindArrayBindingPattern {
			return nil, false
		}
		hid := "__darg"
		for taken[hid] {
			hid += "_"
		}
		taken[hid] = true
		out = append(out, hid)
	}
	return out, true
}

// saParamKinds 与 saParamNames 同步校验参数，返回名->种（"i32"|"bool"）。
// 标注依据封存 saemit.go:162 annotationType（number->i32；i32 TypeReference->i32）；
// 无注解缺省 i32（形状证据：封存 lowerFunction:946 `ptype := tI32`）。
// 类/接口注解（`p: Pt`）记 `inst:Pt`（实例句柄直传）。
// 绑定模式形参走隐藏句柄（`__darg`，数组记 arr、对象记 `inst:Name`；封存 :5347-5371）。
func saParamKinds(fn *ast.FunctionDeclaration, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, enums map[string]map[string]int64) (map[string]string, bool) {
	_, kinds, _, ok := saSynthParams(fn, classes, aliasOf, enums)
	if !ok {
		return nil, false
	}
	for _, v := range kinds {
		if v != "i32" && v != "bool" && v != "arr" && v != "str" && v != "f64" && !(len(v) > 5 && v[:5] == "inst:") {
			return nil, false
		}
	}
	return kinds, true
}

// saDrainDestructuredParams 在体顶按域展开模式形参（与声明解构同形同拒）。
// 数组位逐元越界归零 join 绑 i32（封存 destructureArray:5309-5333）；对象位按
// 接口/类布局偏移 `load hid + off as i32` 绑 i32（封存 destructureObject:5465-5509）。
// rest/嵌套/缺省/未知域/无布局一律大声拒。
func saDrainDestructuredParams(w printer.EmitTextWriter, pendings []saDestructurePending, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextLabel, nextTemp *int) bool {
	for _, q := range pendings {
		if q.pat.Kind == ast.KindArrayBindingPattern {
			idx := 0
			for _, el := range q.pat.AsBindingPattern().Elements.Nodes {
				if el.Kind != ast.KindBindingElement {
					idx++
					continue
				}
				be := el.AsBindingElement()
				if be.DotDotDotToken != nil {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest elements in destructuring are not lowerable"})
					return false
				}
				if be.Initializer != nil {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring defaults are not lowerable"})
					return false
				}
				nm := be.Name()
				if nm == nil {
					idx++
					continue
				}
				if nm.Kind != ast.KindIdentifier {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested destructuring shape is not lowerable"})
					return false
				}
				name := nm.Text()
				if _, dup := scope.types[name]; dup {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
					return false
				}
				v := saLowerCheckedIndex(w, q.hid, fmt.Sprintf("%d", idx), scope.nextLabel, nextTemp)
				w.Write(fmt.Sprintf("  %s = %s\n", name, v))
				scope.types[name] = "i32"
				saDeclareInitOwn(scope, name, v)
				idx++
			}
			continue
		}
		if q.pat.Kind == ast.KindObjectBindingPattern {
			var def *saClassDef
			if q.annot != nil && q.annot.Kind == ast.KindTypeReference {
				if ref := q.annot.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
					def, _ = scope.classes[ref.TypeName.Text()]
				}
			}
			if def == nil {
				ln, col := pos(q.pat.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object destructuring needs a recorded struct layout"})
				return false
			}
			for _, el := range q.pat.AsBindingPattern().Elements.Nodes {
				if el.Kind != ast.KindBindingElement {
					continue
				}
				be := el.AsBindingElement()
				if be.DotDotDotToken != nil {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "rest elements in destructuring are not lowerable"})
					return false
				}
				if be.Initializer != nil {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring defaults are not lowerable"})
					return false
				}
				nm := be.Name()
				if nm == nil || nm.Kind != ast.KindIdentifier {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "nested destructuring shape is not lowerable"})
					return false
				}
				field := nm.Text()
				if be.PropertyName != nil {
					pn := be.PropertyName.AsNode()
					if pn.Kind != ast.KindIdentifier && pn.Kind != ast.KindStringLiteral {
						ln, col := pos(el.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "computed destructuring keys are not lowerable"})
						return false
					}
					field = pn.Text()
				}
				off, ok := def.offsets[field]
				if !ok {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "field " + field + " is not in the " + def.name + " layout"})
					return false
				}
				// str 域经头指针读回串值（读形见字段读位）。
				if def.fkinds[field] == "str" {
					name := nm.Text()
					if _, dup := scope.types[name]; dup {
						ln, col := pos(el.Pos())
						*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
						return false
					}
					w.Write(fmt.Sprintf("  %s = load %s + %d as ptr\n", name, q.hid, off))
					scope.types[name] = "str"
					continue
				}
				name := nm.Text()
				if _, dup := scope.types[name]; dup {
					ln, col := pos(el.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
					return false
				}
				w.Write(fmt.Sprintf("  %s = load %s + %d as i32\n", name, q.hid, off))
				scope.types[name] = "i32"
			}
			continue
		}
		ln, col := pos(q.pat.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("binding pattern %d is not lowerable", int(q.pat.Kind))})
		return false
	}
	return true
}

// saUnionScalarKind 取联合中唯一的非空标量种（null/undefined 吸收为 0；
// 多非空/全空/类接口成员一律拒。封存 annotationType:166-206 联合落
// tUnknown(i32) 是 checker 缺席时的粗 fallback；薄口无 checker，语法定种：
// null 吸收后单一种直通（`i32|null`→i32 与封存测试一致，`string|null`→str）。
func saUnionScalarKind(n *ast.Node) (string, bool) {
	ut := n.AsUnionTypeNode()
	if ut == nil || ut.Types == nil {
		return "", false
	}
	kind := ""
	for _, m := range ut.Types.Nodes {
		if m == nil {
			return "", false
		}
		if m.Kind == ast.KindNullKeyword || m.Kind == ast.KindUndefinedKeyword {
			continue
		}
		if m.Kind == ast.KindLiteralType {
			// `null` 类型即空字面类型（LiteralType 包 NullKeyword）。
			if lit := m.AsLiteralTypeNode().Literal; lit != nil && lit.Kind == ast.KindNullKeyword {
				continue
			}
			return "", false
		}
		var k string
		switch m.Kind {
		case ast.KindNumberKeyword:
			k = "i32"
		case ast.KindBooleanKeyword:
			k = "bool"
		case ast.KindStringKeyword:
			k = "str"
		case ast.KindArrayType:
			at := m.AsArrayTypeNode()
			if at == nil || at.ElementType == nil {
				return "", false
			}
			if ek, ok := saAnnotKind(at.ElementType); !ok || ek != "i32" {
				return "", false
			}
			k = "arr"
		case ast.KindTypeReference:
			if ref := m.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
				if ref.TypeName.Kind != ast.KindIdentifier {
					return "", false
				}
				switch ref.TypeName.Text() {
				case "i32":
					k = "i32"
				case "boolean":
					k = "bool"
				default:
					return "", false
				}
			} else {
				return "", false
			}
		default:
			return "", false
		}
		if kind != "" {
			return "", false
		}
		kind = k
	}
	if kind == "" {
		return "", false
	}
	return kind, true
}

// saAnnotKind 映射类型注解到子集种类（证据：封存 annotationType:166-210）。
// number/i32 标量；number[]/i32[] 为 i32 数组（16 字节头 + 4 字节槽，见 lowerArrayLiteral）。
func saAnnotKind(t *ast.TypeNode) (string, bool) {
	if t == nil {
		return "", false
	}
	switch t.Kind {
	case ast.KindNumberKeyword:
		return "i32", true
	case ast.KindBooleanKeyword:
		return "bool", true
	case ast.KindStringKeyword:
		// 字符串为 16 字节 {ptr,len} 句柄（证据：封存 tString:141）。
		return "str", true
	case ast.KindUnionType:
		return saUnionScalarKind(t.AsNode())
	case ast.KindArrayType:
		el := t.AsArrayTypeNode().ElementType
		if el != nil && el.Kind == ast.KindNumberKeyword {
			return "arr", true
		}
		if el != nil && (el.Kind == ast.KindAnyKeyword || el.Kind == ast.KindUnknownKeyword) {
			return "arr", true
		}
		// 串元数组（`string[]` 具化 16 字节切片头数组；封存 annotationType
		// ArrayType 即 tArray 不分元种 + lowerArrayLiteral 逐元 lowerExpr 同形）。
		if k, ok := saAnnotKind(el); ok && (k == "i32" || k == "str" || k == "arr") {
			return "arr", true
		}
		return "", false
	case ast.KindTupleType:
		// Tuples lower as fixed arrays (all members must slot as i32/str/arr).
		tt := t.AsTupleTypeNode()
		if tt == nil || tt.Elements == nil || len(tt.Elements.Nodes) == 0 {
			return "", false
		}
		for _, m := range tt.Elements.Nodes {
			if k, ok := saTupleElemKind(m); !ok || (k != "i32" && k != "str" && k != "arr") {
				return "", false
			}
		}
		return "arr", true
	case ast.KindTypeOperator:
		// `readonly T[]` unwraps (mutable copy semantics; other operators refuse).
		to := t.AsTypeOperatorNode()
		if to != nil && to.Operator == ast.KindReadonlyKeyword && to.Type != nil {
			return saAnnotKind(to.Type)
		}
		return "", false
	case ast.KindTypeReference:
		if ref := t.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
			if ref.TypeName.Kind != ast.KindIdentifier {
				return "", false
			}
			switch ref.TypeName.Text() {
			case "i32":
				return "i32", true
			case "boolean":
				return "bool", true
			case "f64", "f32":
				// f64 values lower directly (integer/float literal text, fadd/fcmp temps).
				return "f64", true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// saTupleElemKind resolves one tuple member (scalars map directly; arrays and nested
// tuples recurse; named/optional members refuse loudly).
func saTupleElemKind(m *ast.Node) (string, bool) {
	if m == nil {
		return "", false
	}
	switch m.Kind {
	case ast.KindNumberKeyword, ast.KindBooleanKeyword:
		return "i32", true
	case ast.KindStringKeyword:
		return "str", true
	case ast.KindArrayType:
		at := m.AsArrayTypeNode()
		if at == nil || at.ElementType == nil {
			return "", false
		}
		if k, ok := saAnnotKind(at.ElementType); ok && (k == "i32" || k == "str" || k == "arr") {
			return "arr", true
		}
		return "", false
	case ast.KindTupleType:
		tt := m.AsTupleTypeNode()
		if tt == nil || tt.Elements == nil || len(tt.Elements.Nodes) == 0 {
			return "", false
		}
		for _, e := range tt.Elements.Nodes {
			if k, ok := saTupleElemKind(e); !ok || (k != "i32" && k != "str" && k != "arr") {
				return "", false
			}
		}
		return "arr", true
	default:
		return "", false
	}
}

// saReturnKind: "void", "number", "boolean", "string"; 其他一律拒绝。
// i32 返回注解按封存 annotationType:181-186 视为 number。
// saEnumMembers 枚举成员编号（显式 =N 优先，余下 next++；串/计算初值
// 成员仍占序数槽并记 nonInt 集（读位拒，整数成员照折）；形状证据：封存
// integerInit:9258-9277 + enumMemberTable:9284-9305 + recordEnum:9308-9342）。
func saEnumMembers(st *ast.Node) (map[string]int64, map[string]bool, string) {
	m := map[string]int64{}
	nonInt := map[string]bool{}
	var next int64
	for _, mem := range st.AsEnumDeclaration().Members.Nodes {
		nm := mem.Name()
		if nm == nil || (nm.Kind != ast.KindIdentifier && nm.Kind != ast.KindStringLiteral) {
			return nil, nil, "enum member shape is not lowerable"
		}
		if init := mem.AsEnumMember().Initializer; init != nil {
			if v, ok := saEnumInit(init.AsNode()); ok {
				next = v
			} else {
				nonInt[nm.Text()] = true
			}
		}
		m[nm.Text()] = next
		next++
	}
	return m, nonInt, ""
}

// saEnumNonIntMsg 串/计算枚举成员读拒因（封存 7962/8000 原文），命中返回消息。
func saEnumNonIntMsg(enumName, member string, scope *saScope) (string, bool) {
	if set, ok := scope.enumNonInt[enumName]; ok && set[member] {
		return fmt.Sprintf("string enum member %s.%s is not lowerable (only all-integer enums fold ordinals)", enumName, member), true
	}
	return "", false
}

// saEnumInit 折叠枚举初值（整数 Natal 字面量与一元 -/+；镜像 integerInit）。
func saEnumInit(e *ast.Node) (int64, bool) {
	if e.Kind == ast.KindNumericLiteral && !saIsFloatLit(e.Text()) {
		var v int64
		if _, err := fmt.Sscanf(e.Text(), "%d", &v); err != nil {
			return 0, false
		}
		return v, true
	}
	if e.Kind == ast.KindPrefixUnaryExpression {
		un := e.AsPrefixUnaryExpression()
		if (un.Operator == ast.KindMinusToken || un.Operator == ast.KindPlusToken) &&
			un.Operand.Kind == ast.KindNumericLiteral && !saIsFloatLit(un.Operand.Text()) {
			var v int64
			if _, err := fmt.Sscanf(un.Operand.Text(), "%d", &v); err != nil {
				return 0, false
			}
			if un.Operator == ast.KindMinusToken {
				v = -v
			}
			return v, true
		}
	}
	return 0, false
}

func saReturnKind(t *ast.TypeNode) (string, bool) {
	if t == nil {
		// 缺注解即 void（形状证据：封存 lowerFunction:919-923）。
		return "void", true
	}
	switch t.Kind {
	case ast.KindVoidKeyword:
		return "void", true
	case ast.KindNumberKeyword:
		return "number", true
	case ast.KindBooleanKeyword:
		return "boolean", true
	case ast.KindStringKeyword:
		return "string", true
	case ast.KindAnyKeyword, ast.KindUnknownKeyword:
		// `any` renders as number (cf tUnknown signature default).
		return "number", true
	case ast.KindTypeReference:
		if k, ok := saAnnotKind(t); ok && k == "i32" {
			return "number", true
		}
		return "", false
	case ast.KindUnionType:
		if k, ok := saUnionScalarKind(t.AsNode()); ok {
			switch k {
			case "i32":
				return "number", true
			case "bool":
				return "boolean", true
			case "str":
				return "string", true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// saFuncSig 是调用核的签名表项（名->形参数/是否 void/返回种/形参种/缺省表）。
// defaults 与 defaultExprs 与形参同长：有 Initializer 即 true（字面量短调回放，
// 非字面量短调大声拒；形状证据：封存 funcDefaults/funcDefaultExpr + padDefaultArgs）。
type saFuncSig struct {
	params       int
	isVoid       bool
	retKind      string
	paramKinds   []string
	defaults     []bool
	defaultExprs []*ast.Node
	arrowCaps    []string // 局部箭头尾随捕获名（有序；调用点原样追加实参）
	hasRest      bool     // trailing ...rest param; calls pack into one slice (cf funcHasRest)
}

// saLoop 是 break/continue 的跳转栈帧（unlabeled 经栈顶；labeled 经 scope.labels
// 查表：loops/switch 绑定 break+cont，block/switch 绑 break-only（cont 为空）。
// cont 为 continue 落点：while 即 top；for 落增量前（证据：封存 lowerFor:2072-2084
// 跳 top 会跳过增量导致死循环，故增量存在且体用 continue 时另立 cont 标号）。
type saLoop struct {
	top   string
	cont  string
	end   string
	depth int // 入栈时 ownOrder 长度（break/continue 跳前释深于此的归属绑定）
}

// saScope 是单函数子集作用域：名->种 + 循环栈（扁平单作用域，无遮蔽；重声明拒）
// + 文件级函数签名表（调用核只认同文件顶层函数）+ 标号表（loops/switch/block
// 绑定时落子，语句终结随语句消亡；串行复用合法，同名嵌套拒；形状证据：封存
// labels.go:1-58）。
type saScope struct {
	types       map[string]string
	loops       []saLoop
	labels      map[string]saLoop
	pending     []string
	mathAlias   map[string]string
	funcs       map[string]saFuncSig
	enums       map[string]map[string]int64
	enumNonInt  map[string]map[string]bool
	classes     map[string]*saClassDef
	topConsts   map[string]string
	topStr      map[string]bool
	modVars     map[string]*saModState
	thisSelf    string
	thisClass   string
	mainRenamed bool
	nextLabel   *int
	retKind     string
	strPool     *saStrPool
	src         string
	addImport   func(string)
	inlineRet   *saInlineRet
	tcx         *saTypeCtx                      // checker 推断上下文（局部箭头返回种；封存 e.tcx 同形）
	aliasOf     map[string]*ast.TypeNode        // 顶层 `type X` 表（注解别名消解；无码）
	ownOrder    []string                        // 具名绑定声明序（封存 e.owned；return/块出口逆序释放）
	ownState    map[string]*saOwn               // 名→归属记录（堆/已消费/已释放）
	pendingFns  *[]string                       // 文件级 out-of-line 箭头缓冲（封存 pendingFuncs:336 + :576-579 末尾排空）
	arrowSeq    *int                            // 文件级局部箭头序号（封存 e.arrowSeq）
	instFn      map[string]map[string]*ast.Node // 实例函数字段捕获（handle/绑定名→字段→箭头节点；`new C(arrow)` 经构造 wiring 落位，`this.f(e)` 去虚化回放；封存 instFnFields:355-388）
	arrNest     map[string]bool                 // array handle holds slice handles (deep clone recurses; flat by default)
	// arrStr marks array handles whose elements are string handles
	// (callback/for-of params bind str; flat/i32 arrays stay unmarked).
	arrStr       map[string]bool
	imports      map[string]string // builtin-module named imports (local -> module; single-file direct calls)
	importRemote map[string]string // import alias remote names (local -> remote; cf importedRemote)
	// Program-link environment (nil-equivalent when empty; single-file lowering
	// leaves all three zero; shape evidence: upstream LowerProgram prefixOf +
	// links[p].resolved + seeded funcSigs).
	defPrefix   string            // definition prefix for this file ("", entry; "util__", libs)
	linkResolve map[string]string // imported local name -> qualified callee
	// linkHarvests carries all harvested program functions for import-first
	// advisories (read-only; driver fills progressively in dependency order,
	// so reverse-order uses keep the old message — both still refuse).
	linkHarvests map[string]map[string]saProgFunc
}

// saInlineRet 是高阶回调体 return 拦截态（封存 inlineRetState 的薄口子集）：
// 回调体内 return 存槽+jmp end，不写函数 ret；块作用域随内联消亡；
// kind 为槽种（i32/bool 存值，str 存头指针；封存回调槽同形）。
type saInlineRet struct {
	slot string
	end  string
	kind string
}

// saStrPool 是文件级字符串常量池（`@const str_const_N = utf8:"...\\0"` 行在
// @import 之后、函数之前集中落字；同文本去重。形状证据：封存
// lowerStringLiteral:2974-2990）。prefix 为 program-link 定义前缀（成员
// 常量跨文件 `@import` 时防重名；单文件/入口空前缀零行为变）。
type saStrPool struct {
	buf    strings.Builder
	seen   map[string]string
	next   int
	prefix string
}

func saBlockStmts(body *ast.Node) ([]*ast.Node, bool) {
	if body == nil || body.Kind != ast.KindBlock {
		return nil, false
	}
	return body.AsBlock().Statements.Nodes, true
}

// saIsEntryStmt 报告顶层模块加载执行语句（声明/导入导出无码；其余执行。
// 形状证据：封存 isEntryStmt:32-50）。
// saIsProjModule reports builtin projection modules (fs/net/os/path/crypto/
// process/querystring/url/util/punycode and their node: forms; single-file
// direct calls need no linking; other modules refuse loudly as before).
// Module set mirrors upstream import prescan (saemit.go recordImports gate).
func saIsProjModule(mod string) bool {
	switch mod {
	case "fs", "net", "path", "os", "crypto", "process", "querystring", "url", "util", "punycode",
		"node:fs", "node:net", "node:path", "node:os", "node:crypto", "node:process",
		"node:querystring", "node:url", "node:util", "node:punycode":
		return true
	}
	return false
}

// saRecordProjImports records builtin-module named imports (local name -> module and
// remote name; default/namespace forms stay unrecorded and refuse loudly downstream).
// Returns true when recorded (emission skips via handledTop).
func saRecordProjImports(st *ast.Node, imports, importRemote map[string]string) bool {
	imp := st.AsImportDeclaration()
	if imp == nil || imp.ImportClause == nil {
		return false
	}
	if cl := imp.ImportClause; cl != nil && cl.IsTypeOnly() {
		return false
	}
	ms := imp.ModuleSpecifier
	if ms == nil || ms.Kind != ast.KindStringLiteral {
		return false
	}
	mod := ms.Text()
	if !saIsProjModule(mod) {
		return false
	}
	base := mod
	if len(base) > 5 && base[:5] == "node:" {
		base = base[5:]
	}
	clause := imp.ImportClause.AsImportClause()
	if clause == nil {
		return false
	}
	nb := clause.NamedBindings
	if nb == nil || nb.Kind != ast.KindNamedImports {
		return false
	}
	ni := nb.AsNamedImports()
	if ni == nil || ni.Elements == nil {
		return false
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
		local := nm.Text()
		imports[local] = base
		remote := local
		if sp.PropertyName != nil {
			remote = sp.PropertyName.Text()
		}
		importRemote[local] = remote
	}
	return true
}

func saIsEntryStmt(st *ast.Node) bool {
	switch st.Kind {
	case ast.KindFunctionDeclaration,
		ast.KindClassDeclaration,
		ast.KindInterfaceDeclaration,
		ast.KindTypeAliasDeclaration,
		ast.KindEnumDeclaration,
		ast.KindImportDeclaration,
		ast.KindExportDeclaration,
		ast.KindExportAssignment,
		ast.KindNamespaceExportDeclaration,
		ast.KindImportEqualsDeclaration,
		ast.KindVariableStatement,
		ast.KindModuleDeclaration:
		return false
	default:
		return true
	}
}

func saLowerFunction(w printer.EmitTextWriter, st *ast.Node, funcs map[string]saFuncSig, enums map[string]map[string]int64, enumNonInt map[string]map[string]bool, classes map[string]*saClassDef, topConsts map[string]string, topStr map[string]bool, topMaths map[string]string, modVars map[string]*saModState, src string, mainRenamed bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool, tcx *saTypeCtx, pendingFns *[]string, arrowSeq *int, aliasOf map[string]*ast.TypeNode, imports, importRemote map[string]string, defPrefix string, linkResolve map[string]string, linkHarvests map[string]map[string]saProgFunc, forceName string) {
	fn := st.AsFunctionDeclaration()
	name, ok := saFuncName(fn)
	if !ok {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "anonymous function refused"})
		return
	}
	params, ok := saParamNames(fn)
	if !ok {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported parameters"})
		return
	}
	// 返回签名与预扫同源（注解 > checker 推断 > void；非法注解沿既有拒）。
	retKind, isVoid := "void", true
	// erased own type parameter defaults to number.
	rtype := saUnwrapPromise(fn.Type)
	if saIsBareTypeParam(rtype, saTypeParamSet(fn.TypeParameters)) {
		retKind, isVoid = "number", false
	} else if fn.Type != nil {
		k, ok := saReturnKindRef(rtype, classes, aliasOf)
		if !ok {
			ln, col := pos(st.Pos())
			msg := "unsupported return annotation"
			if fn.Type != nil && rtype.Kind == ast.KindUnionType {
				msg = "unsupported return annotation: union"
			}
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return
		}
		retKind, isVoid = k, k == "void"
	} else if k, v, ok := saPrescanRet(nil, st, tcx, classes, aliasOf); ok {
		retKind, isVoid = k, v
	}
	emitName := name
	if forceName != "" {
		// 命名空间成员以限定名发射（`@N_f`；program 前缀续接，见上游
		// lowerPendingNamespaces 限定发射）。
		emitName = forceName
	} else if emitName == "main" && mainRenamed {
		// 入口合成抢 `@main`，用户定义改名（形状证据：封存 planEntry:99-101）。
		emitName = "main__user"
	}
	// 防御：无体直达即孤签名（发射环已擦除，此处按封存 :983 诚实拒）。
	if fn.Body == nil {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function " + name + " has no body (overload signatures are not lowerable)"})
		return
	}
	paramKinds, ok := saParamKinds(fn, classes, aliasOf, enums)
	if !ok {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported parameter annotation (i32/bool/arr/str/inst only)"})
		return
	}
	sig := "@" + defPrefix + emitName + "(" + saSigParamList(paramKinds, params) + ")"
	if !isVoid {
		// 串/实例返回为句柄（证据：封存 return_infer `@greet(n: i32) -> ptr:` 与
		// `@make(x: i32, y: i32) -> ptr:`）。
		sig += saSigRetSuffix(retKind)
	}
	sig += ":\n"
	w.Write(sig)
	stmts, ok := saBlockStmts(fn.Body)
	if !ok {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported body"})
		return
	}
	if len(stmts) == 0 {
		if !isVoid {
			ln, col := pos(st.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "missing return"})
			return
		}
		w.Write("  ret\n")
		return
	}
	scope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, enumNonInt: enumNonInt, classes: classes, topConsts: topConsts, topStr: topStr, modVars: modVars, mainRenamed: mainRenamed, nextLabel: nextLabel, retKind: retKind, strPool: strPool, src: src, addImport: needImport, tcx: tcx, pendingFns: pendingFns, arrowSeq: arrowSeq, aliasOf: aliasOf, imports: imports, importRemote: importRemote}
	scope.defPrefix = defPrefix
	scope.linkResolve = linkResolve
	scope.linkHarvests = linkHarvests
	saSeedTopMaths(scope, topMaths)
	for _, p := range params {
		scope.types[p] = paramKinds[p]
		// 形参归属（封存 declareOwned；release 逆序依赖声明序）。
		saDeclareOwned(scope, p)
	}
	// 模式形参体顶展开（封存 drainDestructuredParams:5376-5410；声明解构同形同拒）。
	if _, _, pendings, ok := saSynthParams(fn, scope.classes, scope.aliasOf, scope.enums); ok && len(pendings) > 0 {
		if !saDrainDestructuredParams(w, pendings, scope, pos, refusals, nextLabel, nextTemp) {
			return
		}
	}
	terminated := false
	for _, s := range stmts {
		if terminated {
			// 终结后的后继是死码，静默抑制（形状证据：封存
			// lowerBlockStatement:820-823 `if e.terminated { return }`，与三处
			// 循环同形）。抑制非错译：saStmtTerminates 只在 return/break/
			// continue/throw、两臂皆终结的 if-else、穷尽 switch 时为真，
			// 活码永不落此路（此前本仓在此大声拒，与上游同码反而不齐）。
			continue
		}
		if done, failed := saLowerStmt(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp); failed {
			return
		} else if done {
			terminated = true
		}
	}
	if !terminated {
		if !isVoid {
			ln, col := pos(st.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "missing return"})
			return
		}
		saReleaseAllOwnedExcept(w, scope, "")
		w.Write("  ret\n")
	} else if saEndsWithBareSwitchLabel(stmts) {
		// 穷尽 switch 收尾：endswitch 标号悬空，补死结构终结（不可达，
		// 缺 return 判定不受影响；封存上游函数尾恒补 return 同形）。
		saReleaseAllOwnedExcept(w, scope, "")
		if isVoid {
			w.Write("  ret\n")
		} else {
			w.Write("  ret 0\n")
		}
	}
}

// saLowerStmt lowering 单条语句（函数体/臂/循环体共用）：
// 返回 (terminated, failed)。形状锁：return/if 原有形状不变。
func saLowerStmt(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	switch s.Kind {
	case ast.KindReturnStatement:
		return saLowerReturn(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	case ast.KindIfStatement:
		if !saLowerIf(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return saStmtTerminates(s), false
	case ast.KindWhileStatement:
		if !saLowerWhile(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return false, false
	case ast.KindForStatement:
		if !saLowerFor(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return false, false
	case ast.KindForOfStatement:
		if !saLowerForOf(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return false, false
	case ast.KindForInStatement:
		if !saLowerForIn(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return false, false
	case ast.KindDoStatement:
		if !saLowerDoWhile(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return false, false
	case ast.KindSwitchStatement:
		if !saLowerSwitch(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return saStmtTerminates(s), false
	case ast.KindTryStatement:
		done, failed := saLowerTry(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if failed {
			return false, true
		}
		return done, false
	case ast.KindVariableStatement:
		if !saLowerVarDecl(w, s, scope, pos, refusals, nextTemp) {
			return false, true
		}
		return false, false
	case ast.KindExpressionStatement:
		if !saLowerExprStmt(w, s, scope, pos, refusals, nextTemp) {
			return false, true
		}
		return false, false
	case ast.KindBreakStatement, ast.KindContinueStatement:
		if !saLowerBreakContinue(w, s, scope, pos, refusals) {
			return false, true
		}
		return true, false
	case ast.KindLabeledStatement:
		done, failed := saLowerLabeled(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if failed {
			return false, true
		}
		return done, false
	case ast.KindThrowStatement:
		// throw 即 panic(2501)，不可恢复（形状证据：封存 saemit.go:153
		// panicThrow 常量 + :882-884 `panic(%d)` 落字；try 内顶层 throw
		// 走 throwing 切片绑 catch 形参，见 saLowerThrowingTry）。
		w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		return true, false
	case ast.KindEmptyStatement:
		// 空语句 no-op（形状证据：封存 :886-887）。
		return false, false
	case ast.KindBlock:
		// 裸块作语句（switch 臂 `{...}` / 独立 `{...}`）：块域 + 共享语句
		// 全集；终结态经 saLowerArm 回传（封存 :847-852 pushScope + 逐语句
		// lowerBlockStatement + popScope 同形）。
		bd := s.AsBlock()
		if bd == nil || bd.Statements == nil {
			return false, false
		}
		if !saLowerArm(w, bd.Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		return saArmTerminates(bd.Statements.Nodes), false
	default:
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported statement kind %d", int(s.Kind))})
		return false, true
	}
}

// saLowerReturn lowering return（含 void 裸 return、三元 SELECT、i32 操作数）。
func saLowerReturn(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	rs := s.AsReturnStatement()
	if scope.inlineRet != nil {
		// 回调体内 return：存槽+jmp end（裸 return 只跳过余下回调体）。
		// 形状证据：封存 callbackValue:5222-5249。
		if rs.Expression == nil {
			w.Write(fmt.Sprintf("  jmp %s\n", scope.inlineRet.end))
			return true, false
		}
		if scope.inlineRet.kind == "str" {
			sop, msg := saEvalStr(w, rs.Expression, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				return false, true
			}
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", scope.inlineRet.slot, sop))
			w.Write(fmt.Sprintf("  jmp %s\n", scope.inlineRet.end))
			return true, false
		}
		op, msg := saEvalI32(w, rs.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
			return false, true
		}
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", scope.inlineRet.slot, op))
		w.Write(fmt.Sprintf("  jmp %s\n", scope.inlineRet.end))
		return true, false
	}
	if isVoid {
		if rs.Expression != nil {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "return value in void function refused"})
			return false, true
		}
		saReleaseAllOwnedExcept(w, scope, "")
		w.Write("  ret\n")
		return true, false
	}
	if rs.Expression != nil && rs.Expression.Kind == ast.KindConditionalExpression {
		ce := rs.Expression.AsConditionalExpression()
		t, _, msg := saLowerTernaryValue(w, ce, s, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if msg != "" {
			return false, true
		}
		saReleaseExceptOp(w, scope, t)
		w.Write(fmt.Sprintf("  ret %s\n", t))
		return true, false
	}
	op, msg := saEvalReturnOperand(w, rs.Expression, scope.retKind, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		// 求值错误透传具体信息（调用核/一元/未知变量等定位关键）。
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return false, true
	}
	saReleaseExceptOp(w, scope, op)
	w.Write(fmt.Sprintf("  ret %s\n", op))
	return true, false
}

func saLowerArm(w printer.EmitTextWriter, stmts []*ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	saved := saScopeEnter(scope)
	defer saScopeExit(scope, saved)
	terminated := false
	for _, s := range stmts {
		if terminated {
			// 死码静默抑制（封存 lowerBlockStatement:820-823 同形；见函数体
			// 同款注释：抑制非错译，活码不落此路）。
			continue
		}
		done, failed := saLowerStmt(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if failed {
			return false
		}
		if done {
			terminated = true
		}
	}
	return true
}

// ── ts→sa 端到端薄壳：复用 TranspileSA，落盘 .sai + subset-report.txt ──
// 移植自 satsgo/cmd/tsgo-sa/main.go（CLI 壳：--out/用法/exit 码；其 lowerFile
// 核心即本文件 TranspileSA）。上游对齐：输入输出走 os/flag 标准库，与
// execute.CommandLine 无耦合；调用方（cmd/tsgo/main.go）只做 --sa 薄分发。
// Exit 码：2 用法/IO 错误，1 存在定位拒绝，0 全量通过。
func RunSA(args []string) int {
	fs := flag.NewFlagSet("sa", flag.ContinueOnError)
	out := fs.String("out", "", "output directory for .sai files (default: <first-input-base>_sa)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	inputs := fs.Args()
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tsgo --sa [--out <dir>] <file.ts> [...]")
		return 2
	}
	outDir := *out
	if outDir == "" {
		base := strings.TrimSuffix(filepath.Base(inputs[0]), filepath.Ext(inputs[0]))
		outDir = base + "_sa"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: mkdir %s: %v\n", outDir, err)
		return 2
	}
	refused := false
	var report strings.Builder
	for _, f := range inputs {
		text, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", f, err)
			return 2
		}
		res := TranspileSA(context.Background(), string(text), Options{FileName: f})
		base := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		if err := os.WriteFile(filepath.Join(outDir, base+".sai"), []byte(res.SAI), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: write %s: %v\n", f, err)
			return 2
		}
		for _, r := range res.Refusals {
			fmt.Fprintf(&report, "%s:%d:%d: %s\n", f, r.Line, r.Col, r.Msg)
		}
		for _, wr := range res.Warnings {
			fmt.Fprintf(&report, "%s:%d:%d: warning: %s\n", f, wr.Line, wr.Col, wr.Msg)
		}
		if len(res.Refusals) > 0 {
			refused = true
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "subset-report.txt"), []byte(report.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write report: %v\n", err)
		return 2
	}
	fmt.Printf("wrote SA project to %s\n", outDir)
	if refused {
		return 1
	}
	return 0
}

// ── program build（tsgo build <dir>）：本地多文件链接 + sci workspace 脚手架 ──
// S1 范围：命名函数导入链接（相对路径，扩展名省略/.ts）；命名空间/默认/
// 重导出/star/环/非函数值拒因大声（后阶段）；bare 第三方聚合 Unresolved；
// npm 依赖记 sa.mod 注释 + 报告（sa pkg 手动解决）。
// 形状证据：封存 LowerProgram:202-1255 + ScaffoldProgram + programReport +
// build.sh/sa.mod 文本；输出布局按 sci workspace（根 sa.mod + packages/<mod>）。

// saNpmDep is one package.json runtime dependency (versions verbatim).
type saNpmDep struct {
	Name    string
	Version string
}

// saReadNpmDeps returns sorted runtime dependencies of dir/package.json
// (nil when absent or unparsable; never refuses). Shape evidence: base
// packagejson.Parse + DependencyFields (Expected 值语义，畸形值安全）。
func saReadNpmDeps(dir string) []saNpmDep {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	fields, err := packagejson.Parse(data)
	if err != nil {
		return nil
	}
	deps, ok := fields.Dependencies.GetValue()
	if !ok || len(deps) == 0 {
		return nil
	}
	out := make([]saNpmDep, 0, len(deps))
	for name, ver := range deps {
		out = append(out, saNpmDep{Name: name, Version: ver})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RunBuild implements `tsgo build [--out O] [--entry E] [--mod M] <dir>`.
// Exit codes mirror the build gate: 2 usage/IO, 1 refused, 0 linked.
func RunBuild(args []string) int {
	// Hoist flags wherever they appear so positional <dir> never cuts
	// flag parsing short (mirrors upstream prescanBuildFlags).
	var rest []string
	outFlag, entryFlag, modFlag := "", "", ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--out" && i+1 < len(args):
			outFlag = take()
		case strings.HasPrefix(a, "--out="):
			outFlag = strings.TrimPrefix(a, "--out=")
		case a == "--entry" && i+1 < len(args):
			entryFlag = take()
		case strings.HasPrefix(a, "--entry="):
			entryFlag = strings.TrimPrefix(a, "--entry=")
		case a == "--mod" && i+1 < len(args):
			modFlag = take()
		case strings.HasPrefix(a, "--mod="):
			modFlag = strings.TrimPrefix(a, "--mod=")
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tsgo build [--out <dir>] [--entry <file>] [--mod <name>] <dir>")
		return 2
	}
	dir := rest[0]
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "error: build takes a directory (for single files, drop `build`): %s\n", dir)
		return 2
	}
	out := outFlag
	if out == "" {
		out = dir
	}
	files := map[string]string{}
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(p)
			if base == "node_modules" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".d.ts") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		if strings.HasSuffix(rel, ".sai") {
			return nil
		}
		text, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		files[filepath.ToSlash(rel)] = string(text)
		return nil
	})
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "error: no .ts sources under %s\n", dir)
		return 2
	}
	entry := entryFlag
	if entry == "" {
		entry = saDetectEntry(dir, files)
	}
	if _, ok := files[entry]; !ok {
		fmt.Fprintf(os.Stderr, "error: entry %s not found under %s\n", entry, dir)
		return 2
	}
	modName := modFlag
	if modName == "" {
		modName = saDetectModName(dir)
	}
	npmDeps := saReadNpmDeps(dir)
	res := saLowerProgram(entry, files, dir)
	report := saProgramReport(entry, modName, res, npmDeps)
	if err := saWriteWorkspace(out, modName, entry, files, res, npmDeps, report); err != nil {
		fmt.Fprintf(os.Stderr, "error: scaffold: %v\n", err)
		return 2
	}
	for _, d := range res.diags {
		fmt.Fprintln(os.Stderr, "diag:", d)
	}
	fmt.Printf("wrote linked SA workspace to %s (entry %s, %d files)\n", out, entry, len(res.files))
	if res.refused {
		fmt.Fprintln(os.Stderr, "refused: resolve subset-report.txt before running sh build.sh")
		return 1
	}
	return 0
}

// saDetectEntry resolves the program entry: package.json "saEntry",
// tsconfig "files"[0], then src/main.ts, main.ts, src/index.ts, index.ts,
// fallback lexicographically first file (deterministic).
func saDetectEntry(dir string, files map[string]string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg map[string]any
		if json.Unmarshal(data, &pkg) == nil {
			if se, ok := pkg["saEntry"].(string); ok {
				if _, ok := files[se]; ok {
					return se
				}
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "tsconfig.json")); err == nil {
		var ts map[string]any
		if json.Unmarshal(data, &ts) == nil {
			if fl, ok := ts["files"].([]any); ok && len(fl) > 0 {
				if f0, ok := fl[0].(string); ok {
					if _, ok := files[f0]; ok {
						return f0
					}
				}
			}
		}
	}
	for _, c := range []string{"src/main.ts", "main.ts", "src/index.ts", "index.ts"} {
		if _, ok := files[c]; ok {
			return c
		}
	}
	names := []string{}
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names[0]
}

// saDetectModName resolves the package name: --mod, package.json name,
// else dir base (dashes to underscores). Shape evidence: base
// packagejson.Parse + HeaderFields.Name.
func saDetectModName(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		if fields, err := packagejson.Parse(data); err == nil {
			if name, ok := fields.Name.GetValue(); ok && name != "" {
				return strings.ReplaceAll(name, "-", "_")
			}
		}
	}
	return strings.ReplaceAll(filepath.Base(dir), "-", "_")
}

// saProgResult is the linked program outcome (mirrors ProgramResult).
type saProgResult struct {
	files      []string
	perFile    map[string]string
	deps       map[string][]string // direct link edges per file (for member @imports)
	diags      []string
	unresolved []string
	refused    bool
}

// saProgBuiltinMod reports builtin/asset modules skipped by linking.
func saProgBuiltinMod(spec string) bool {
	switch spec {
	case "fs", "net", "path", "os",
		"node:fs", "node:net", "node:path", "node:os",
		"node:process", "node:buffer":
		return true
	}
	return strings.HasSuffix(spec, ".wasm") || strings.HasSuffix(spec, ".wit")
}

// saProgResolveRelative resolves a spec against the file set. Primary is the
// base module resolver (internal/module, Bundler 口径：含 node_modules、
// package.json exports、`.js`→`.ts` 回退；形状证据：ata.go:189/419、
// contentmappers.go:18)；仅底座落到 files 集内才认领（node_modules/.d.ts
// 目标沿旧 Unresolved 聚合），底座不认再走旧 5 候选回退（保旧行为）。
func saProgResolveRelative(importer, spec string, files map[string]string, dir string, resolver *module.Resolver) string {
	if resolver != nil && dir != "" {
		containing := filepath.Join(dir, filepath.FromSlash(importer))
		if resolved, _ := resolver.ResolveModuleName(spec, containing, core.ModuleKindESNext, nil); resolved.IsResolved() {
			if rel, err := filepath.Rel(dir, resolved.ResolvedFileName); err == nil {
				if c := path.Clean(filepath.ToSlash(rel)); c != "" {
					if _, ok := files[c]; ok {
						return c
					}
				}
			}
		}
	}
	dir2 := path.Dir(importer)
	if dir2 == "." {
		dir2 = ""
	}
	join := func(base string) string {
		if dir2 == "" {
			return path.Clean(base)
		}
		return path.Clean(dir2 + "/" + base)
	}
	for _, c := range []string{join(spec) + ".ts", join(spec) + ".js", join(spec), join(spec) + "/index.ts", join(spec) + "/index.js"} {
		if _, ok := files[c]; ok {
			return c
		}
	}
	return ""
}

// saProgPrefix sanitizes a path to a definition prefix (entry keeps "").
func saProgPrefix(p, entry string) string {
	if p == entry {
		return ""
	}
	base := p
	// node_modules 特判：`node_modules/<pkg>/<rest>` 按包名展平（sla 成员
	// 风格短前缀；scope `@` 吃掉），避免整条路径前缀又臭又长。
	if i := strings.Index(base, "node_modules/"); i >= 0 {
		rest := base[i+len("node_modules/"):]
		segs := strings.Split(rest, "/")
		npkg := 1
		if strings.HasPrefix(rest, "@") && len(segs) >= 2 {
			npkg = 2
		}
		if len(segs) > npkg {
			base = strings.Join(segs[:npkg], "_") + "_" + strings.Join(segs[npkg:], "_")
		} else {
			base = strings.Join(segs, "_")
		}
	}
	base = strings.TrimSuffix(base, ".ts")
	base = strings.TrimSuffix(base, ".js")
	base = strings.TrimSuffix(base, ".d.ts")
	var b strings.Builder
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String() + "__"
}

// saProgResolveBare resolves a bare spec through the base resolver, gated
// to files-set `.ts`/`.js` members (node_modules 按需纳入后的真实目标；
// 包外一律 ""，沿旧 Unresolved 聚合）。`.js` 直转 `.sa`（用户令：落 js
// 即转 sa；JS 无注解按既有缺省 i32/void，超子集处大声拒）。
func saProgResolveBare(importer, spec string, files map[string]string, dir string, resolver *module.Resolver) string {
	if resolver == nil || dir == "" {
		return ""
	}
	containing := filepath.Join(dir, filepath.FromSlash(importer))
	resolved, _ := resolver.ResolveModuleName(spec, containing, core.ModuleKindESNext, nil)
	if !resolved.IsResolved() {
		return ""
	}
	name := resolved.ResolvedFileName
	if strings.HasSuffix(name, ".d.ts") {
		return ""
	}
	if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".js") {
		return ""
	}
	if c, ok := saProgKeyFor(dir, name); ok {
		if _, ok := files[c]; ok {
			return c
		}
	}
	return ""
}

// saProgIncludeBare pulls reachable node_modules `.ts`/`.js` files into
// the file set (Walk 整目录跳过系设计，防整库爆炸；此处只纳入引用可达者，
// 递归跟随其相对依赖，bare 再解；总量 cap，超量沿旧 Unresolved）。
// `.js` 按 JS 种解析直转（用户令；zod dist 即此形态）。import 与
// export-from 同跟随（barrel 链）；相对目标落盘存在即纳入。
// Caveat：symlinked node_modules 底座解析不穿透（osvfs Realpath 未接入
// scope 发现），真实 `npm install` 目录正常。
// saProgKeyFor maps an absolute-disk target to a file-set key: inside the
// project by dir-relative path, otherwise (symlinked/external installs,
// pnpm forests) by its `node_modules/` suffix so link and real installs
// share keys; outside both it refuses.
func saProgKeyFor(dir, name string) (string, bool) {
	if i := strings.LastIndex(filepath.ToSlash(name), "node_modules/"); i >= 0 {
		if c := path.Clean("node_modules/" + filepath.ToSlash(name)[i+len("node_modules/"):]); c != "" && c != "node_modules" {
			return c, true
		}
		return "", false
	}
	rel, err := filepath.Rel(dir, name)
	if err != nil {
		return "", false
	}
	if c := path.Clean(filepath.ToSlash(rel)); c != "" && !strings.HasPrefix(c, "..") {
		return c, true
	}
	return "", false
}

// saProgAdoptFile reads an absolute-disk `.ts`/`.js` file into the set
// (keyed by saProgKeyFor; parsed with its kind).
func saProgAdoptFile(files map[string]string, parsed map[string]*ast.SourceFile, dir, name string, kind core.ScriptKind) bool {
	c, ok := saProgKeyFor(dir, name)
	if !ok {
		return false
	}
	if _, ok := files[c]; ok {
		return false
	}
	text, err := os.ReadFile(name)
	if err != nil {
		return false
	}
	files[c] = string(text)
	abs := "/" + strings.TrimPrefix(c, "/")
	parsed[path.Clean(c)] = parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: abs, Path: tspath.ToPath(abs, "/", true)}, string(text), kind)
	return true
}

func saProgIncludeBare(files map[string]string, dir string, resolver *module.Resolver, parsed map[string]*ast.SourceFile) {
	const maxFiles = 64
	tried := map[string]bool{}
	adoptRel := func(p, spec string) bool {
		base := filepath.Join(filepath.Dir(filepath.Join(dir, filepath.FromSlash(p))), filepath.FromSlash(spec))
		cands := []string{base, base + ".ts", base + ".js", filepath.Join(base, "index.ts"), filepath.Join(base, "index.js")}
		// TS 语义 `./x.js` 回退 `x.ts`（底座 resolver 同形）。
		if strings.HasSuffix(base, ".js") {
			cands = append(cands, strings.TrimSuffix(base, ".js")+".ts")
		}
		for _, cand := range cands {
			kind := core.ScriptKindTS
			if strings.HasSuffix(cand, ".js") {
				kind = core.ScriptKindJS
			}
			if saProgAdoptFile(files, parsed, dir, cand, kind) {
				return true
			}
		}
		return false
	}
	for len(files) < maxFiles {
		progress := false
		for p, sf := range parsed {
			if sf == nil {
				continue
			}
			for _, st := range sf.AsSourceFile().Statements.Nodes {
				if st == nil {
					continue
				}
				// export-from 与 import 同跟随（barrel 链）。
				var spec string
				switch st.Kind {
				case ast.KindImportDeclaration:
					if cl := st.AsImportDeclaration().ImportClause; cl != nil && cl.IsTypeOnly() {
						continue
					}
					spec = saProgModuleSpec(st)
				case ast.KindExportDeclaration:
					s, _, _ := saProgReexpEdges(st)
					spec = s
				default:
					continue
				}
				if spec == "" || saProgBuiltinMod(spec) {
					continue
				}
				key := p + "\x00" + spec
				if tried[key] {
					continue
				}
				tried[key] = true
				// 相对目标：files 集外但落盘存在即纳入。
				if strings.HasPrefix(spec, ".") {
					if _, ok := files[saProgResolveRelative(p, spec, files, dir, resolver)]; !ok {
						if adoptRel(p, spec) {
							progress = true
						}
					}
					continue
				}
				containing := filepath.Join(dir, filepath.FromSlash(p))
				resolved, _ := resolver.ResolveModuleName(spec, containing, core.ModuleKindESNext, nil)
				if !resolved.IsResolved() {
					continue
				}
				name := resolved.ResolvedFileName
				if strings.HasSuffix(name, ".d.ts") {
					continue
				}
				kind := core.ScriptKindTS
				if strings.HasSuffix(name, ".js") {
					kind = core.ScriptKindJS
				} else if !strings.HasSuffix(name, ".ts") {
					continue
				}
				if saProgAdoptFile(files, parsed, dir, name, kind) {
					progress = true
				}
			}
		}
		if !progress {
			return
		}
	}
}

// saProgModuleSpec extracts the literal module string of an import.
func saProgModuleSpec(st *ast.Node) string {
	if st == nil || st.Kind != ast.KindImportDeclaration {
		return ""
	}
	imp := st.AsImportDeclaration()
	if imp == nil || imp.ModuleSpecifier == nil || imp.ModuleSpecifier.Kind != ast.KindStringLiteral {
		return ""
	}
	return imp.ModuleSpecifier.Text()
}

// saLowerProgram lowers entry plus reachable relative .ts modules. dir is the
// project root (absolute or not; absolutized for the base resolver).
func saLowerProgram(entry string, files map[string]string, dir string) *saProgResult {
	res := &saProgResult{perFile: map[string]string{}}
	entry = path.Clean(entry)
	if _, ok := files[entry]; !ok {
		res.refused = true
		res.diags = append(res.diags, fmt.Sprintf("entry %s not in file set", entry))
		return res
	}
	// 底座模块解析器（internal/module，磁盘 host + Bundler 口径；单例复用，
	// 内带缓存；失败 nil 即全走旧候选，行为不变）。CustomConditions 含
	// `@zod/source`：包方为 TS 源码加载发布的官方条件（zod package.json
	// exports "." 首键；底座 GetConditions:1943 原生支持，tsx/strip-types
	// 同形 honoring；无此条件的包零影响）。
	var resolver *module.Resolver
	if absDir, err := filepath.Abs(dir); err == nil {
		func() {
			defer func() { _ = recover() }()
			host := compiler.NewCompilerHost(absDir, osvfs.FS(), libDirectory, nil, nil, nil)
			resolver = module.NewResolver(host, &core.CompilerOptions{ModuleResolution: core.ModuleResolutionKindBundler, CustomConditions: []string{"@zod/source"}}, "", "", nil)
			dir = absDir
		}()
	}
	parsed := map[string]*ast.SourceFile{}
	for p, text := range files {
		abs := "/" + strings.TrimPrefix(p, "/")
		sf := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: abs, Path: tspath.ToPath(abs, "/", true)}, text, core.ScriptKindTS)
		parsed[path.Clean(p)] = sf
	}
	// bare 第三方按需纳入（resolver 解 node_modules `.ts` 才进集；`.js`/
	// 包外沿旧 Unresolved；上游 todo/01 node_modules 跟随设计）。
	if resolver != nil {
		func() {
			defer func() { _ = recover() }()
			saProgIncludeBare(files, dir, resolver, parsed)
		}()
	}
	graph := map[string][]string{}
	specOf := map[string]map[string]string{}
	reexpOf := map[string]map[string]string{}
	starOf := map[string][]string{}
	impOf := map[string]map[string]string{}
	unresolved := map[string]bool{}
	addEdge := func(p, spec string) string {
		if !strings.HasPrefix(spec, ".") {
			// linked bare：已纳入集的真实目标建边（后续 hook B 经 specOf
			// 绑定）；其余沿旧 Unresolved 聚合。
			if !saProgBuiltinMod(spec) {
				if tgt := saProgResolveBare(p, spec, files, dir, resolver); tgt != "" {
					graph[p] = append(graph[p], tgt)
					return tgt
				}
				unresolved[spec] = true
			}
			return ""
		}
		tgt := saProgResolveRelative(p, spec, files, dir, resolver)
		if tgt == "" {
			return ""
		}
		graph[p] = append(graph[p], tgt)
		return tgt
	}
	for p, sf := range parsed {
		for _, st := range sf.AsSourceFile().Statements.Nodes {
			if st == nil {
				continue
			}
			// 重导出 from 形建边 + 记边（上游 collectReExport:1501 同形；
			// star 形记序（first-match），C2 目标可达即认领）。
			if st.Kind == ast.KindExportDeclaration {
				spec, edges, star := saProgReexpEdges(st)
				if spec == "" {
					continue
				}
				tgt := addEdge(p, spec)
				if tgt == "" {
					continue
				}
				if star {
					starOf[p] = append(starOf[p], tgt)
					continue
				}
				if len(edges) == 0 {
					continue
				}
				if reexpOf[p] == nil {
					reexpOf[p] = map[string]string{}
				}
				for exported, remote := range edges {
					reexpOf[p][exported] = tgt + "\x00" + remote
				}
				continue
			}
			if st.Kind != ast.KindImportDeclaration {
				continue
			}
			if cl := st.AsImportDeclaration().ImportClause; cl != nil && cl.IsTypeOnly() {
				continue
			}
			spec := saProgModuleSpec(st)
			if spec == "" {
				continue
			}
			if tgt := addEdge(p, spec); tgt != "" {
				if specOf[p] == nil {
					specOf[p] = map[string]string{}
				}
				specOf[p][spec] = tgt
				// import provenance（本地名单转出口用；命名 + 默认，
				// 命名空间无单值跳过；副作用导入无 ImportClause 即无 provenance）。
				if ic := st.AsImportDeclaration().ImportClause; ic != nil {
					if cl := ic.AsImportClause(); cl != nil {
						if nm := cl.Name(); nm != nil && nm.Kind == ast.KindIdentifier {
							if impOf[p] == nil {
								impOf[p] = map[string]string{}
							}
							impOf[p][nm.Text()] = tgt + "\x00default"
						}
						if nb := cl.NamedBindings; nb != nil && nb.Kind == ast.KindNamedImports {
							if ni := nb.AsNamedImports(); ni != nil && ni.Elements != nil {
								for _, n := range ni.Elements.Nodes {
									if n == nil || n.Kind != ast.KindImportSpecifier {
										continue
									}
									sp := n.AsImportSpecifier()
									inm := n.Name()
									if inm == nil || inm.Kind != ast.KindIdentifier {
										continue
									}
									local := inm.Text()
									remote := local
									if sp != nil && sp.PropertyName != nil {
										remote = sp.PropertyName.Text()
									}
									if impOf[p] == nil {
										impOf[p] = map[string]string{}
									}
									impOf[p][local] = tgt + "\x00" + remote
								}
							}
						}
					}
				}
			}
		}
	}
	// 本地名单转出口（`import {a}; export {a [as b]}`；自有函数经 harvest
	// 直解，此处只记进口名边；命名空间值无单值沿旧门）。
	for p, sf := range parsed {
		if sf == nil {
			continue
		}
		for _, st := range sf.AsSourceFile().Statements.Nodes {
			if st == nil || st.Kind != ast.KindExportDeclaration {
				continue
			}
			edges, star := saProgLocalEdges(st)
			if star {
				continue
			}
			for exported, local := range edges {
				if edge, ok := impOf[p][local]; ok {
					if reexpOf[p] == nil {
						reexpOf[p] = map[string]string{}
					}
					reexpOf[p][exported] = edge
				}
			}
		}
	}
	reachable := []string{}
	visited := map[string]bool{}
	onStack := map[string]bool{}
	var stack []string
	var cycle []string
	var dfs func(p string)
	dfs = func(p string) {
		visited[p] = true
		onStack[p] = true
		stack = append(stack, p)
		for _, q := range graph[p] {
			if cycle != nil {
				break
			}
			if onStack[q] {
				i := 0
				for i < len(stack) && stack[i] != q {
					i++
				}
				cycle = append(append([]string{}, stack[i:]...), q)
				break
			}
			if !visited[q] {
				dfs(q)
			}
		}
		stack = stack[:len(stack)-1]
		onStack[p] = false
		reachable = append(reachable, p)
	}
	dfs(entry)
	if cycle != nil {
		res.refused = true
		res.diags = append(res.diags, fmt.Sprintf("import cycle: %s", strings.Join(cycle, " -> ")))
		return res
	}
	prefixOf := map[string]string{}
	for _, p := range reachable {
		prefixOf[p] = saProgPrefix(p, entry)
	}
	harvests := map[string]map[string]saProgFunc{}
	classHarvests := map[string]map[string]saProgClass{}
	for _, p := range reachable {
		lk := &saFileLink{
			defPrefix:     prefixOf[p],
			isEntry:       p == entry,
			self:          p,
			specOf:        specOf[p],
			harvests:      harvests,
			prefixOf:      prefixOf,
			reexps:        reexpOf,
			stars:         starOf,
			resolve:       map[string]string{},
			seed:          map[string]saFuncSig{},
			harvest:       map[string]saProgFunc{},
			classHarvest:  map[string]saProgClass{},
			classHarvests: classHarvests,
			classSeed:     map[string]*saClassDef{},
		}
		out := transpileSAInner(context.Background(), files[p], Options{FileName: p}, lk)
		harvests[p] = lk.harvest
		classHarvests[p] = lk.classHarvest
		for _, r := range out.Refusals {
			res.diags = append(res.diags, fmt.Sprintf("%s:%d:%d: %s", p, r.Line, r.Col, r.Msg))
		}
		for _, wr := range out.Warnings {
			res.diags = append(res.diags, fmt.Sprintf("%s:%d:%d: warning: %s", p, wr.Line, wr.Col, wr.Msg))
		}
		if len(out.Refusals) > 0 {
			res.refused = true
		}
		res.perFile[p] = out.SAI
	}
	res.deps = graph
	res.files = append([]string{}, reachable...)
	for spec := range unresolved {
		// Only specs reachable from the entry program surface.
		res.unresolved = append(res.unresolved, spec)
	}
	sort.Strings(res.unresolved)
	sort.Strings(res.files)
	return res
}

// saProgramReport renders the build report (mirrors programReport).
func saProgramReport(entry, mod string, res *saProgResult, npmDeps []saNpmDep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "== program %s: refused=%v ==\n", entry, res.refused)
	fmt.Fprintf(&b, "files: %s\n", strings.Join(res.files, ", "))
	for _, d := range res.diags {
		fmt.Fprintf(&b, "%s\n", d)
	}
	if len(res.unresolved) > 0 {
		fmt.Fprintf(&b, "== unresolved third-party deps (%d) ==\n", len(res.unresolved))
		for _, u := range res.unresolved {
			fmt.Fprintf(&b, "package %s: no SA backend yet (see todo/03_npm.md)\n", u)
		}
	}
	if len(npmDeps) > 0 {
		fmt.Fprintf(&b, "== package.json dependencies (%d) ==\n", len(npmDeps))
		for _, d := range npmDeps {
			fmt.Fprintf(&b, "npm %s@%s: record in sa.mod require after sa pkg resolution\n", d.Name, d.Version)
		}
	}
	_ = mod
	return b.String()
}

// saWriteWorkspace scaffolds the sci workspace output.
func saWriteWorkspace(outDir, mod, entry string, files map[string]string, res *saProgResult, npmDeps []saNpmDep, report string) error {
	pkgDir := filepath.Join(outDir, "packages", mod)
	srcDir := filepath.Join(pkgDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return err
	}
	for p, text := range files {
		rel := strings.TrimPrefix(filepath.FromSlash(p), "src"+string(filepath.Separator))
		dst := filepath.Join(srcDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			return err
		}
	}
	// 分裂布局（sla workspace 真形态：一成员多 `.sa`，`@import "./x.sa"`
	// 跨文件引用；用户原则纠正：禁合并单文件）。单元名由 prefix 派生（入口
	// 恒 `main`，node_modules 按包展平；`saProgPrefix` 唯一，冲突大声拒）；
	// 逐文件 `.sai` inspection 件退役（单元即 `.sa`）。
	saName := map[string]string{}
	seenBase := map[string]bool{}
	for _, p := range res.files {
		base := strings.TrimSuffix(saProgPrefix(p, entry), "__")
		if base == "" {
			base = "main"
		}
		if seenBase[base] {
			return fmt.Errorf("duplicate member unit name %q (from %s)", base+".sa", p)
		}
		seenBase[base] = true
		saName[p] = base
	}
	for p, sai := range res.perFile {
		name, ok := saName[p]
		if !ok {
			continue
		}
		var b strings.Builder
		for _, q := range res.deps[p] {
			dn, ok := saName[q]
			if !ok || dn == name {
				continue
			}
			fmt.Fprintf(&b, "@import \"./%s.sa\"\n", dn)
		}
		b.WriteString(sai)
		dst := filepath.Join(srcDir, name+".sa")
		if err := os.WriteFile(dst, []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "subset-report.txt"), []byte(report), 0o644); err != nil {
		return err
	}
	var nm strings.Builder
	nm.WriteString(fmt.Sprintf("package \"%s\"\n", mod))
	if len(npmDeps) > 0 {
		nm.WriteString("# npm dependencies (no sa hash yet; resolve via sa pkg before uncommenting):\n")
		for _, d := range npmDeps {
			nm.WriteString("# require npm:" + d.Name + "@" + d.Version + " <sha256 pending>\n")
		}
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "sa.mod"), []byte(nm.String()), 0o644); err != nil {
		return err
	}
	ws := fmt.Sprintf("workspace {\n  members [\"packages/%s\"]\n  default_member \"%s\"\n}\n", mod, mod)
	if err := os.WriteFile(filepath.Join(outDir, "sa.mod"), []byte(ws), 0o644); err != nil {
		return err
	}
	readme := "# " + mod + " (tsgo -> SA workspace)\n\nGenerated by tsgo build: TypeScript linked to SA-ASM for the sci/sa toolchain.\n\nEntry: packages/" + mod + "/src/main.sa (member \"" + mod + "\").\n"
	if err := os.WriteFile(filepath.Join(outDir, "README.md"), []byte(readme), 0o644); err != nil {
		return err
	}
	buildsh := "#!/usr/bin/env sh\n# Build the workspace member with the sci toolchain.\nset -eu\ncd \"$(dirname \"$0\")\"\nif grep -q \"refused=true\" subset-report.txt 2>/dev/null; then\n  echo \"refused: resolve subset-report.txt before building\" >&2\n  exit 1\nfi\nSA_BIN=\"${SA_BIN:-sa}\"\n\"$SA_BIN\" build-workspace -p " + mod + " -o main\n"
	if err := os.WriteFile(filepath.Join(outDir, "build.sh"), []byte(buildsh), 0o755); err != nil {
		return err
	}
	return nil
}
