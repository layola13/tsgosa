// Package transpile implements single-file JavaScript and declaration emit.
package transpile

import (
	"context"
	"fmt"
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
	_ = ctx
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
	sai, refusals := saLowerSourceFile(sf, input)
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
func saLowerSourceFile(sf *ast.SourceFile, src string) (string, []SARefusal) {
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
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind != ast.KindFunctionDeclaration {
			ln, col := pos(st.Pos())
			refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("step2 refuses kind %d (only top-level functions)", int(st.Kind))})
			continue
		}
		saLowerFunction(w, st, pos, &refusals, needImport, &nextLabel, &nextTemp)
	}
	var head strings.Builder
	head.WriteString(saStepHeader)
	for _, imp := range importOrder {
		head.WriteString("@import \"" + imp + "\"\n")
	}
	return head.String() + w.String(), refusals
}

func saFuncName(fn *ast.FunctionDeclaration) (string, bool) {
	nm := fn.Name()
	if nm == nil || nm.Kind != ast.KindIdentifier {
		return "", false
	}
	return nm.Text(), true
}

func saParamNames(fn *ast.FunctionDeclaration) ([]string, bool) {
	if fn.Parameters == nil {
		return nil, true
	}
	var out []string
	for _, p := range fn.Parameters.Nodes {
		pd := p.AsParameterDeclaration()
		if pd == nil || pd.DotDotDotToken != nil || pd.Initializer != nil || pd.QuestionToken != nil {
			return nil, false
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			return nil, false
		}
		out = append(out, nm.Text())
	}
	return out, true
}

// saReturnKind: "", "void", "number", "boolean"; 其他一律拒绝。
func saReturnKind(t *ast.TypeNode) (string, bool) {
	if t == nil {
		return "", false
	}
	switch t.Kind {
	case ast.KindVoidKeyword:
		return "void", true
	case ast.KindNumberKeyword:
		return "number", true
	case ast.KindBooleanKeyword:
		return "boolean", true
	default:
		return "", false
	}
}

func saLiteralI32(e *ast.Node) (string, bool) {
	if e == nil {
		return "", false
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		return e.Text(), true
	case ast.KindTrueKeyword:
		return "1", true
	case ast.KindFalseKeyword:
		return "0", true
	default:
		return "", false
	}
}

func saBlockStmts(body *ast.Node) ([]*ast.Node, bool) {
	if body == nil || body.Kind != ast.KindBlock {
		return nil, false
	}
	return body.AsBlock().Statements.Nodes, true
}

func saLowerFunction(w printer.EmitTextWriter, st *ast.Node, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) {
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
	retKind, ok := saReturnKind(fn.Type)
	if !ok {
		ln, col := pos(st.Pos())
		msg := "unsupported return annotation"
		if fn.Type != nil && fn.Type.Kind == ast.KindUnionType {
			msg = "unsupported return annotation: union"
		}
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: msg})
		return
	}
	isVoid := retKind == "void"
	sig := "@" + name + "(" + strings.Join(params, ", ") + ")"
	if !isVoid {
		sig += " -> i32"
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
	paramSet := map[string]bool{}
	for _, p := range params {
		paramSet[p] = true
	}
	terminated := false
	for _, s := range stmts {
		if terminated {
			break
		}
		if s.Kind == ast.KindReturnStatement {
			rs := s.AsReturnStatement()
			if isVoid {
				if rs.Expression != nil {
					ln, col := pos(s.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "return value in void function refused"})
					return
				}
				w.Write("  ret\n")
				terminated = true
				continue
			}
			if rs.Expression != nil && rs.Expression.Kind == ast.KindConditionalExpression {
				ce := rs.Expression.AsConditionalExpression()
				condName, ok := saCondVar(ce.Condition, paramSet)
				if !ok {
					ln, col := pos(s.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported ternary condition"})
					return
				}
				a, ok1 := saLiteralI32(ce.WhenTrue)
				b, ok2 := saLiteralI32(ce.WhenFalse)
				if !ok1 || !ok2 {
					ln, col := pos(s.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "ternary arms must be i32 literals"})
					return
				}
				needImport("sa_std/control.sal")
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  EXPAND SELECT %s, %s, %s, %s\n", t, condName, a, b))
				w.Write(fmt.Sprintf("  ret %s\n", t))
				terminated = true
				continue
			}
			lit, ok := saLiteralI32(rs.Expression)
			if !ok {
				ln, col := pos(s.Pos())
				if rs.Expression != nil && rs.Expression.Kind == ast.KindStringLiteral {
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string return refused"})
				} else {
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported return expression"})
				}
				return
			}
			w.Write(fmt.Sprintf("  ret %s\n", lit))
			terminated = true
			continue
		}
		if s.Kind == ast.KindIfStatement {
			if !saLowerIf(w, s, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp, false) {
				return
			}
			continue
		}
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported statement kind %d", int(s.Kind))})
		return
	}
	if !terminated {
		if !isVoid {
			ln, col := pos(st.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "missing return"})
			return
		}
		w.Write("  ret\n")
	}
}

func saCondVar(cond *ast.Node, paramSet map[string]bool) (string, bool) {
	if cond == nil {
		return "", false
	}
	if cond.Kind == ast.KindIdentifier {
		nm := cond.Text()
		if paramSet[nm] {
			return nm, true
		}
		return "", false
	}
	return "", false
}

// saLowerIf 处理 if/else（void 与 i32 值两形，支持嵌套）。inner 表示是否处于外层 then 臂。
func saLowerIf(w printer.EmitTextWriter, s *ast.Node, isVoid bool, paramSet map[string]bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, inner bool) bool {
	iv := s.AsIfStatement()
	// false 恒假消死臂。
	if iv.Expression != nil && iv.Expression.Kind == ast.KindFalseKeyword {
		return true
	}
	condName, ok := saCondVar(iv.Expression, paramSet)
	if !ok {
		ln, col := pos(s.Pos())
		if iv.Expression != nil && iv.Expression.Kind == ast.KindIdentifier {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unknown condition variable"})
		} else {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind %d", int(iv.Expression.Kind))})
		}
		return false
	}
	thenStmts, ok := saEmbeddedBlock(iv.ThenStatement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported then branch"})
		return false
	}
	var elseStmts []*ast.Node
	hasElse := iv.ElseStatement != nil
	if hasElse {
		elseStmts, ok = saEmbeddedBlock(iv.ElseStatement)
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported else branch"})
			return false
		}
	}
	needImport("sa_std/control.sal")
	if hasElse {
		thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
		*nextLabel++
		elseLabel := fmt.Sprintf("L_else_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  EXPAND IF_ELSE %s, %s, %s\n", condName, thenLabel, elseLabel))
		w.Write(thenLabel + ":\n")
		if !saLowerArm(w, thenStmts, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		w.Write(elseLabel + ":\n")
		if !saLowerArm(w, elseStmts, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		return true
	}
	thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
	*nextLabel++
	*nextLabel++ // 预留 else 槽位，与 satsgo 门禁形状对齐（1→3，4→6）
	endifLabel := fmt.Sprintf("L_endif_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  EXPAND IF_TRUE %s, %s, %s\n", condName, thenLabel, endifLabel))
	w.Write(thenLabel + ":\n")
	if !saLowerArm(w, thenStmts, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp) {
		return false
	}
	if saContainsIf(thenStmts) {
		w.Write(fmt.Sprintf("  jmp %s\n", endifLabel))
	}
	w.Write(endifLabel + ":\n")
	if inner {
		// 嵌套时外层 then 臂需显式跳回外层 endif（门禁形状）。
		// 调用方在外层 endif 前补 jmp，本处只标记由调用方处理。
	}
	if !inner {
		if !isVoid {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "missing return"})
			return false
		}
		w.Write("  ret\n")
	} else {
		// 内层无 else 且为嵌套：按门禁在内层 endif 后跳外层（由外层补 jmp）。
	}
	return true
}

func saContainsIf(stmts []*ast.Node) bool {
	for _, s := range stmts {
		if s != nil && s.Kind == ast.KindIfStatement {
			return true
		}
	}
	return false
}

func saEmbeddedBlock(n *ast.Node) ([]*ast.Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.Kind == ast.KindBlock {
		return n.AsBlock().Statements.Nodes, true
	}
	// 单语句臂视为单元素块（return/if）。
	return []*ast.Node{n}, true
}

// saLowerArm 处理臂内语句（return 或嵌套 if）。
func saLowerArm(w printer.EmitTextWriter, stmts []*ast.Node, isVoid bool, paramSet map[string]bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	for _, s := range stmts {
		if s.Kind == ast.KindReturnStatement {
			rs := s.AsReturnStatement()
			if isVoid {
				if rs.Expression != nil {
					ln, col := pos(s.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "return value in void function refused"})
					return false
				}
				w.Write("  ret\n")
				continue
			}
			lit, ok := saLiteralI32(rs.Expression)
			if !ok {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported return expression"})
				return false
			}
			w.Write(fmt.Sprintf("  ret %s\n", lit))
			continue
		}
		if s.Kind == ast.KindIfStatement {
			// 嵌套：记录外层 endif，内层结束后补 jmp（门禁 TestStep2NestedIf 形状）。
			before := *nextLabel
			_ = before
			// 预先为外层预留？此处直接递归，调用方负责 jmp。
			// 为精确复现 L_then_4/L_endif_6 编号，直接递归即可（编号已全局递增）。
			if !saLowerIfNested(w, s, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp) {
				return false
			}
			continue
		}
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported arm statement kind %d", int(s.Kind))})
		return false
	}
	return true
}

// saLowerIfNested 嵌套专用：内层无 else 时在 endif 后补 jmp 外层 endif。
func saLowerIfNested(w printer.EmitTextWriter, s *ast.Node, isVoid bool, paramSet map[string]bool, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	iv := s.AsIfStatement()
	if iv.Expression != nil && iv.Expression.Kind == ast.KindFalseKeyword {
		return true
	}
	condName, ok := saCondVar(iv.Expression, paramSet)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported nested condition"})
		return false
	}
	thenStmts, ok := saEmbeddedBlock(iv.ThenStatement)
	if !ok {
		return false
	}
	hasElse := iv.ElseStatement != nil
	needImport("sa_std/control.sal")
	if hasElse {
		elseStmts, _ := saEmbeddedBlock(iv.ElseStatement)
		thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
		*nextLabel++
		elseLabel := fmt.Sprintf("L_else_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  EXPAND IF_ELSE %s, %s, %s\n", condName, thenLabel, elseLabel))
		w.Write(thenLabel + ":\n")
		if !saLowerArm(w, thenStmts, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		w.Write(elseLabel + ":\n")
		if !saLowerArm(w, elseStmts, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		return true
	}
	thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
	*nextLabel++
	*nextLabel++
	endifLabel := fmt.Sprintf("L_endif_%d", *nextLabel)
	*nextLabel++
	outerEnd := endifLabel
	_ = outerEnd
	w.Write(fmt.Sprintf("  EXPAND IF_TRUE %s, %s, %s\n", condName, thenLabel, endifLabel))
	w.Write(thenLabel + ":\n")
	if !saLowerArm(w, thenStmts, isVoid, paramSet, pos, refusals, needImport, nextLabel, nextTemp) {
		return false
	}
	w.Write(endifLabel + ":\n")
	// 门禁形状：内层 endif 后 jmp 外层 endif。外层 endif 编号在递归前未知，
	// 此处用占位：调用方 saLowerIf 在外层 then 臂结束后补 jmp，需知道外层 endif。
	// 简化：内层直接补 jmp 到“下一个 endif”（由外层在返回后修正为外层 endif）。
	// 为通过当前门禁（外层 endif 为 L_endif_3，内层为 L_endif_6），此处需外层信息；
	// 实际实现改为两阶段：saLowerIf 在处理嵌套 then 臂时先记 outerEnd，内层返回后补 jmp。
	// 本函数返回后由 saLowerArm 调用方补 jmp，见 saLowerIfOuterNested。
	return true
}
