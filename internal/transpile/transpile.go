// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/debug"
	"github.com/microsoft/typescript-go/internal/printer"
	"github.com/microsoft/typescript-go/internal/transformers"
	"github.com/microsoft/typescript-go/internal/transformers/jsxtransforms"
	"github.com/microsoft/typescript-go/internal/transformers/tstransforms"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
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
	Diagnostics []*ast.Diagnostic
}

// TranspileSA 将单文件 TS 转为 sci/sa 可装配的 .sai（step1+step2 子集）。
// step1：顶层 function f(): void {}/return; → @f(): ret；
// number/boolean 字面返回 → @f() -> i32: ret lit；其余定位拒绝。
// step2：if/else → EXPAND IF_ELSE/IF_TRUE + @import "sa_std/control.sal"；
// return c?a:b（i32 字面臂）→ EXPAND SELECT；false 恒假消死臂；嵌套 if 带 jmp。
func TranspileSA(ctx context.Context, input string, options Options) *SAOutput {
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
	sai, refusals := saLowerSourceFile(sf, input, tcx)
	return &SAOutput{SAI: sai, Refusals: refusals}
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

// saLowerSourceFile 发射 SA 文本（后端为 printer.NewTextWriter，替换 JS 落字）。
func saLowerSourceFile(sf *ast.SourceFile, src string, tcx *saTypeCtx) (string, []SARefusal) {
	w := printer.NewTextWriter("\n", 2)
	var refusals []SARefusal
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
	enums := map[string]map[string]int64{}
	enumNonInt := map[string]map[string]bool{}
	classes := map[string]*saClassDef{}
	// 预扫一：类型表（类/接口/枚举；函数签名引用须先行）。
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind == ast.KindClassDeclaration {
			// 类定义预扫成表（布局记录、无码；方法随调用内联）。
			saRecordClass(st, classes, pos, &refusals)
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
		name := nm.Text()
		if _, dup := funcs[name]; dup {
			ln, col := pos(st.Pos())
			refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate function " + name})
			continue
		}
		nparams := 0
		if fn.Parameters != nil {
			nparams = len(fn.Parameters.Nodes)
		}
		isVoid := false
		retKind := ""
		if k, v, ok := saPrescanRet(fn.Type, st, tcx, classes, aliasOf); ok {
			retKind = k
			isVoid = v
		}
		var pk []string
		if kinds, ok := saParamKinds(fn, classes, aliasOf, enums); ok {
			if names, ok := saParamNames(fn); ok {
				for _, n := range names {
					pk = append(pk, kinds[n])
				}
			}
		}
		var defs []bool
		var dexprs []*ast.Node
		if fn.Parameters != nil {
			defs, dexprs = saFuncDefaultTables(fn.Parameters.Nodes)
		}
		hasRest := false
		if fn.Parameters != nil && len(fn.Parameters.Nodes) > 0 {
			if last := fn.Parameters.Nodes[len(fn.Parameters.Nodes)-1]; last != nil {
				if pd := last.AsParameterDeclaration(); pd != nil && pd.DotDotDotToken != nil {
					hasRest = true
				}
			}
		}
		funcs[name] = saFuncSig{params: nparams, isVoid: isVoid, retKind: retKind, paramKinds: pk, defaults: defs, defaultExprs: dexprs, hasRest: hasRest}
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
	strPool := &saStrPool{seen: map[string]string{}}
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
		case ast.KindImportDeclaration:
			imp := st.AsImportDeclaration()
			if cl := imp.ImportClause; cl != nil && cl.IsTypeOnly() {
				continue
			}
			ln, col := pos(st.Pos())
			refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: "value imports are not lowerable (single file)"})
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
				saLowerArrowConst(w, name, arrow, funcs, enums, enumNonInt, classes, topConsts, topStr, topMaths, modVars, src, mainRenamed, pos, &refusals, needImport, &nextLabel, &nextTemp, strPool, tcx, aliasOf)
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
		saLowerFunction(w, st, funcs, enums, enumNonInt, classes, topConsts, topStr, topMaths, modVars, src, mainRenamed, pos, &refusals, needImport, &nextLabel, &nextTemp, strPool, tcx, &pendingFns, &arrowSeq, aliasOf)
	}
	if len(entryStmts) > 0 {
		// 入口 `@main`（空作用域帧，i32 出口；缺尾返补 `ret 0`）。
		w.Write("@main() -> i32:\n")
		escope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, enumNonInt: enumNonInt, classes: classes, topConsts: topConsts, topStr: topStr, modVars: modVars, mainRenamed: mainRenamed, nextLabel: &nextLabel, retKind: "i32", strPool: strPool, src: src, addImport: needImport, tcx: tcx, pendingFns: &pendingFns, arrowSeq: &arrowSeq, aliasOf: aliasOf}
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
	return out, refusals
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
	return saSynthParamNodes(nodes, classes, aliasOf, enums)
}

func saSynthParamNodes(paramNodes []*ast.Node, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, enums map[string]map[string]int64) ([]string, map[string]string, []saDestructurePending, bool) {
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
			k, ok := saAnnotKind(pd.Type)
			if !ok {
				if pd.Type.Kind == ast.KindTypeReference {
					if ref := pd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
						if _, ok := classes[ref.TypeName.Text()]; ok {
							kinds[name] = "inst:" + ref.TypeName.Text()
							continue
						}
						// enum annotations lower as ptr handles (integer-enum equality folds to eq).
						if _, ok := enums[ref.TypeName.Text()]; ok {
							kinds[name] = "arr"
							continue
						}
					}
				}
				// 标量别名经顶层别名表消解（`c: Count`；封存 annotationType
				// 别名词消解同形；inst 别名/未知沿旧门拒）。
				if ak, aok := saResolveAliasKind(pd.Type, aliasOf); aok {
					k, ok = ak, true
				} else {
					return nil, nil, nil, false
				}
			}
			if k != "i32" && k != "bool" && k != "arr" && k != "str" {
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
			if ref := pd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
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
		if v != "i32" && v != "bool" && v != "arr" && v != "str" && !(len(v) > 5 && v[:5] == "inst:") {
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
		// 串元数组（`string[]` 具化 16 字节切片头数组；封存 annotationType
		// ArrayType 即 tArray 不分元种 + lowerArrayLiteral 逐元 lowerExpr 同形）。
		if k, ok := saAnnotKind(el); ok && (k == "i32" || k == "str") {
			return "arr", true
		}
		return "", false
	case ast.KindTypeReference:
		if ref := t.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
			switch ref.TypeName.Text() {
			case "i32":
				return "i32", true
			case "boolean":
				return "bool", true
			}
		}
		return "", false
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
	tcx         *saTypeCtx   // checker 推断上下文（局部箭头返回种；封存 e.tcx 同形）
	aliasOf       map[string]*ast.TypeNode // 顶层 `type X` 表（注解别名消解；无码）
	ownOrder    []string              // 具名绑定声明序（封存 e.owned；return/块出口逆序释放）
	ownState    map[string]*saOwn     // 名→归属记录（堆/已消费/已释放）
	pendingFns  *[]string    // 文件级 out-of-line 箭头缓冲（封存 pendingFuncs:336 + :576-579 末尾排空）
	arrowSeq    *int         // 文件级局部箭头序号（封存 e.arrowSeq）
	instFn      map[string]map[string]*ast.Node // 实例函数字段捕获（handle/绑定名→字段→箭头节点；`new C(arrow)` 经构造 wiring 落位，`this.f(e)` 去虚化回放；封存 instFnFields:355-388）
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
// lowerStringLiteral:2974-2990）。
type saStrPool struct {
	buf  strings.Builder
	seen map[string]string
	next int
}

func saBlockStmts(body *ast.Node) ([]*ast.Node, bool) {
	if body == nil || body.Kind != ast.KindBlock {
		return nil, false
	}
	return body.AsBlock().Statements.Nodes, true
}

// saIsEntryStmt 报告顶层模块加载执行语句（声明/导入导出无码；其余执行。
// 形状证据：封存 isEntryStmt:32-50）。
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

func saLowerFunction(w printer.EmitTextWriter, st *ast.Node, funcs map[string]saFuncSig, enums map[string]map[string]int64, enumNonInt map[string]map[string]bool, classes map[string]*saClassDef, topConsts map[string]string, topStr map[string]bool, topMaths map[string]string, modVars map[string]*saModState, src string, mainRenamed bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool, tcx *saTypeCtx, pendingFns *[]string, arrowSeq *int, aliasOf map[string]*ast.TypeNode) {
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
	if fn.Type != nil {
		k, ok := saReturnKindRef(fn.Type, classes, aliasOf)
		if !ok {
			ln, col := pos(st.Pos())
			msg := "unsupported return annotation"
			if fn.Type != nil && fn.Type.Kind == ast.KindUnionType {
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
	if emitName == "main" && mainRenamed {
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
	sig := "@" + emitName + "(" + saSigParamList(paramKinds, params) + ")"
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
	scope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, enumNonInt: enumNonInt, classes: classes, topConsts: topConsts, topStr: topStr, modVars: modVars, mainRenamed: mainRenamed, nextLabel: nextLabel, retKind: retKind, strPool: strPool, src: src, addImport: needImport, tcx: tcx, pendingFns: pendingFns, arrowSeq: arrowSeq, aliasOf: aliasOf}
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
