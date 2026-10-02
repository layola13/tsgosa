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
	// 预扫顶层函数签名（调用核：被调函数须同文件定义，元数精确匹配；
	// 证据：封存 program.go:435/512 按定义收集 rets/arity）。
	funcs := map[string]saFuncSig{}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind != ast.KindFunctionDeclaration {
			continue
		}
		fn := st.AsFunctionDeclaration()
		nm := fn.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
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
		if k, ok := saReturnKind(fn.Type); ok {
			retKind = k
			isVoid = k == "void"
		}
		var pk []string
		if kinds, ok := saParamKinds(fn); ok {
			if fn.Parameters != nil {
				for _, p := range fn.Parameters.Nodes {
					pk = append(pk, kinds[p.AsParameterDeclaration().Name().Text()])
				}
			}
		}
		funcs[name] = saFuncSig{params: nparams, isVoid: isVoid, retKind: retKind, paramKinds: pk}
	}
	strPool := &saStrPool{seen: map[string]string{}}
	emitted := map[string]bool{}
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind != ast.KindFunctionDeclaration {
			ln, col := pos(st.Pos())
			refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("step2 refuses kind %d (only top-level functions)", int(st.Kind))})
			continue
		}
		if nm := st.AsFunctionDeclaration().Name(); nm != nil && nm.Kind == ast.KindIdentifier {
			if emitted[nm.Text()] {
				continue
			}
			emitted[nm.Text()] = true
		}
		saLowerFunction(w, st, funcs, pos, &refusals, needImport, &nextLabel, &nextTemp, strPool)
	}
	var head strings.Builder
	head.WriteString(saStepHeader)
	for _, imp := range importOrder {
		head.WriteString("@import \"" + imp + "\"\n")
	}
	head.WriteString(strPool.buf.String())
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

// saParamKinds 与 saParamNames 同步校验参数，返回名->种（"i32"|"bool"）。
// 标注依据封存 saemit.go:162 annotationType（number->i32；i32 TypeReference->i32）；
// 无注解缺省 i32（形状证据：封存 lowerFunction:946 `ptype := tI32`）。
func saParamKinds(fn *ast.FunctionDeclaration) (map[string]string, bool) {
	kinds := map[string]string{}
	if fn.Parameters == nil {
		return kinds, true
	}
	for _, p := range fn.Parameters.Nodes {
		pd := p.AsParameterDeclaration()
		if pd == nil || pd.DotDotDotToken != nil || pd.Initializer != nil || pd.QuestionToken != nil {
			return nil, false
		}
		nm := pd.Name()
		if nm == nil || nm.Kind != ast.KindIdentifier {
			return nil, false
		}
		if pd.Type == nil {
			kinds[nm.Text()] = "i32"
			continue
		}
		k, ok := saAnnotKind(pd.Type)
		if !ok {
			return nil, false
		}
		// 参数仅允许 i32/bool/str/arr 四种（其余大声拒，子集门）。
		if k != "i32" && k != "bool" && k != "arr" && k != "str" {
			return nil, false
		}
		kinds[nm.Text()] = k
	}
	return kinds, true
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
	case ast.KindArrayType:
		el := t.AsArrayTypeNode().ElementType
		if el != nil && el.Kind == ast.KindNumberKeyword {
			return "arr", true
		}
		if k, ok := saAnnotKind(el); ok && k == "i32" {
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
	default:
		return "", false
	}
}

// saFuncSig 是调用核的签名表项（名->形参数/是否 void/返回种/形参种）。
type saFuncSig struct {
	params     int
	isVoid     bool
	retKind    string
	paramKinds []string
}
// saLoop 是 break/continue 的跳转栈帧（unlabeled 经栈顶；labeled 经 scope.labels
// 查表：loops/switch 绑定 break+cont，block/switch 绑 break-only（cont 为空）。
// cont 为 continue 落点：while 即 top；for 落增量前（证据：封存 lowerFor:2072-2084
// 跳 top 会跳过增量导致死循环，故增量存在且体用 continue 时另立 cont 标号）。
type saLoop struct {
	top  string
	cont string
	end  string
}

// saScope 是单函数子集作用域：名->种 + 循环栈（扁平单作用域，无遮蔽；重声明拒）
// + 文件级函数签名表（调用核只认同文件顶层函数）+ 标号表（loops/switch/block
// 绑定时落子，语句终结随语句消亡；串行复用合法，同名嵌套拒；形状证据：封存
// labels.go:1-58）。
type saScope struct {
	types     map[string]string
	loops     []saLoop
	labels    map[string]saLoop
	pending   []string
	mathAlias map[string]string
	funcs     map[string]saFuncSig
	nextLabel *int
	retKind   string
	strPool   *saStrPool
	addImport func(string)
}

// saStrPool 是文件级字符串常量池（`@const str_const_N = utf8:"...\\0"` 行在
// @import 之后、函数之前集中落字；同文本去重。形状证据：封存
// lowerStringLiteral:2974-2990）。
type saStrPool struct {
	buf  strings.Builder
	seen map[string]string
	next int
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

func saLowerFunction(w printer.EmitTextWriter, st *ast.Node, funcs map[string]saFuncSig, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool) {
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
		if retKind == "string" {
			// 字符串返回为句柄（证据：封存 return_infer `@greet(n: i32) -> ptr:`）。
			sig += " -> ptr"
		} else {
			sig += " -> i32"
		}
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
	scope := &saScope{types: map[string]string{}, funcs: funcs, nextLabel: nextLabel, retKind: retKind, strPool: strPool, addImport: needImport}
	paramKinds, ok := saParamKinds(fn)
	if !ok {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported parameter annotation (i32/bool/str only)"})
		return
	}
	for k, v := range paramKinds {
		scope.types[k] = v
	}
	terminated := false
	for _, s := range stmts {
		if terminated {
			// 终结后仍有语句：此前静默丢弃，现大声拒（拒则大声）。
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unreachable code after terminating statement"})
			return
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
		w.Write("  ret\n")
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
		return false, false
	case ast.KindTryStatement:
		if !saLowerTry(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false, true
		}
		tryTerm, finTerm := saTryTerms(s)
		return tryTerm || finTerm, false
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
		// panicThrow 常量 + :882-884 `panic(%d)` 落字；try 内含 throw
		// 仍由 saLowerTry 前门大声拒，见 lowerTry 前的 containsThrow 门）。
		w.Write(fmt.Sprintf("  panic(%d)\n", 2501))
		return true, false
	case ast.KindEmptyStatement:
		// 空语句 no-op（形状证据：封存 :886-887）。
		return false, false
	default:
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported statement kind %d", int(s.Kind))})
		return false, true
	}
}

// saLowerReturn lowering return（含 void 裸 return、三元 SELECT、i32 操作数）。
func saLowerReturn(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	rs := s.AsReturnStatement()
	if isVoid {
		if rs.Expression != nil {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "return value in void function refused"})
			return false, true
		}
		w.Write("  ret\n")
		return true, false
	}
	if rs.Expression != nil && rs.Expression.Kind == ast.KindConditionalExpression {
		ce := rs.Expression.AsConditionalExpression()
		condOp, msg := saCondOperand(w, ce.Condition, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported ternary condition: " + msg})
			return false, true
		}
		if saIsStrValue(ce.WhenTrue, scope) || saIsStrValue(ce.WhenFalse, scope) {
			// 串臂三元走槽汇合（两侧须皆串位；形状证据：封存 lowerTernary:8498-8515）。
			if !saIsStrValue(ce.WhenTrue, scope) || !saIsStrValue(ce.WhenFalse, scope) {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "ternary arms disagree (string vs non-string)"})
				return false, true
			}
			tv, msgA := saEvalStr(w, ce.WhenTrue, scope, pos, refusals, nextTemp)
			fv, msgB := saEvalStr(w, ce.WhenFalse, scope, pos, refusals, nextTemp)
			if msgA != "" || msgB != "" {
				ln, col := pos(s.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "ternary arms must be string operands"})
				return false, true
			}
			slot := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
			tL := fmt.Sprintf("L_tern_t_%d", *nextLabel)
			*nextLabel++
			fL := fmt.Sprintf("L_tern_f_%d", *nextLabel)
			*nextLabel++
			endL := fmt.Sprintf("L_tern_end_%d", *nextLabel)
			*nextLabel++
			w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, tL, fL))
			w.Write(fmt.Sprintf("%s:\n", tL))
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, tv))
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
			w.Write(fmt.Sprintf("%s:\n", fL))
			w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, fv))
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
			w.Write(fmt.Sprintf("%s:\n", endL))
			res := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", res, slot))
			w.Write(fmt.Sprintf("  ret %s\n", res))
			return true, false
		}
		a, msgA := saEvalI32(w, ce.WhenTrue, scope, pos, refusals, nextTemp)
		b, msgB := saEvalI32(w, ce.WhenFalse, scope, pos, refusals, nextTemp)
		if msgA != "" || msgB != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "ternary arms must be i32 operands"})
			return false, true
		}
		needImport("sa_std/control.sal")
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  EXPAND SELECT %s, %s, %s, %s\n", t, condOp, a, b))
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
	w.Write(fmt.Sprintf("  ret %s\n", op))
	return true, false
}

// saBoolSideCond 报告二元条件中布尔类型一侧的变量名（无则 ""）。
// 门禁 TestSATSGoPortNonParamCondRefused 要求此类条件以 condition kind 拒绝。
func saBoolSideCond(cond *ast.Node, scope *saScope) string {
	if cond == nil || cond.Kind != ast.KindBinaryExpression {
		return ""
	}
	be := cond.AsBinaryExpression()
	for _, side := range []*ast.Node{be.Left, be.Right} {
		if side != nil && side.Kind == ast.KindIdentifier && scope.types[side.Text()] == "bool" {
			return side.Text()
		}
	}
	return ""
}

// saCondOperand 求条件操作数：绑定标识符直接用（形状锁）；真/假折 1/0；
// 其余走 saEvalI32（比较等先行发射临时量）。失败返回定位信息。
func saCondOperand(w printer.EmitTextWriter, cond *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if cond == nil {
		return "", "missing condition"
	}
	switch cond.Kind {
	case ast.KindIdentifier:
		nm := cond.Text()
		if k, ok := scope.types[nm]; ok {
			if k == "arr" {
				return "", "array " + nm + " in condition"
			}
			if k == "str" {
				return "", "string " + nm + " in condition"
			}
			return nm, ""
		}
		return "", "unknown condition variable " + nm
	case ast.KindTrueKeyword:
		return "1", ""
	case ast.KindFalseKeyword:
		return "0", ""
	default:
		return saEvalI32(w, cond, scope, pos, refusals, nextTemp)
	}
}

// saBinaryOpKind 空安全取二元操作符（parser 常保非空，防御备用）。
func saBinaryOpKind(be *ast.BinaryExpression) ast.Kind {
	if be == nil || be.OperatorToken == nil {
		return ast.KindUnknown
	}
	return be.OperatorToken.Kind
}

// saEvalReturnOperand 按函数返回种求 return 操作数：boolean 函数走 saEvalBool
//（bool 标识符直用，其余 0/1 操作数），number 函数走 saEvalI32；数组无返回位。
func saEvalReturnOperand(w printer.EmitTextWriter, e *ast.Node, retKind string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "arr" {
			return "", "array return not supported"
		}
		if k, ok := scope.types[e.Text()]; ok && k == "str" && retKind != "string" {
			return "", "string return needs string annotation"
		}
	}
	if retKind == "boolean" {
		return saEvalBool(w, e, scope, pos, refusals, nextTemp)
	}
	if retKind == "string" {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	return saEvalI32(w, e, scope, pos, refusals, nextTemp)
}

// saEvalBool 求布尔操作数（0/1 表示与 i32 统一）：绑定 bool 标识符直用，
// 其余走 saEvalI32（字面/比较/`!`/调 bool 函数皆产 0/1 操作数）。
func saEvalBool(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e != nil && e.Kind == ast.KindIdentifier {
		nm := e.Text()
		if k, ok := scope.types[nm]; ok && k == "bool" {
			return nm, ""
		}
	}
	return saEvalI32(w, e, scope, pos, refusals, nextTemp)
}

// saIsFloatLit 粗判浮点数字面（封存 lowerExpr:2725 按 isFloatLiteral 分 f64/i32）。
func saIsFloatLit(text string) bool {
	for i := 0; i < len(text); i++ {
		if c := text[i]; c == '.' || c == 'e' || c == 'E' {
			return true
		}
	}
	return false
}

// saEvalCall 求函数调用（形状证据：封存 `%s = call @%s(%s)` / `call @%s(%s)`）。
// 被调者须为同文件顶层函数（预扫签名表；元数精确匹配）；局部同名遮蔽则拒
// （无一等函数）。返回 (operand, isVoidCall, errMsg)。
// saEvalMathAbs 求 `Math.abs(x)`（形状证据：封存 lowerMathInline abs:5754-5780
// 分支汇合原样：alloc 8 槽 + `sge x, 0` + br + 两臂 store + end load + 释放。
// 其余 Math.* 本薄口大声拒；`Math.abs` 别名调用不认（无 mathAliases 表，拒）。
func saEvalMathAbs(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math.abs needs 1 argument"
	}
	v, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	tL := fmt.Sprintf("L_abs_t_%d", *scope.nextLabel)
	*scope.nextLabel++
	fL := fmt.Sprintf("L_abs_f_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_abs_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  %s = sge %s, 0\n", c, v))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, tL, fL))
	w.Write(fmt.Sprintf("%s:\n", tL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", fL))
	nv := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub 0, %s\n", nv, v))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, nv))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return out, false, ""
}

// saEvalMathPow 求 `Math.pow(base, expo)`（形状证据：封存 lowerMathInline
// pow:5781-5806，即 lowerPowLoop 循环形；本薄口 saLowerPow:1130-1168 同形，
// 此处复用同发射，仅标号前缀取 L_mpow_ 以区分表达式位）。
func saEvalMathPow(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 2 {
		return "", false, "Math.pow needs 2 arguments"
	}
	base, msgB := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
	if msgB != "" {
		return "", false, msgB
	}
	expo, msgE := saEvalI32(w, args[1], scope, pos, refusals, nextTemp)
	if msgE != "" {
		return "", false, msgE
	}
	nextLabel := scope.nextLabel
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 1\n", res))
	topL := fmt.Sprintf("L_mpow_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_mpow_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_mpow_end_%d", *nextLabel)
	*nextLabel++
	ctr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", ctr, expo))
	w.Write(fmt.Sprintf("%s:\n", topL))
	cc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sgt %s, 0\n", cc, ctr))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cc, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	nr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", nr, res, base))
	w.Write(fmt.Sprintf("  %s = %s\n", res, nr))
	nc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", nc, ctr))
	w.Write(fmt.Sprintf("  %s = %s\n", ctr, nc))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return res, false, ""
}

// saEvalMathRounding 求 `Math.floor/ceil/round/trunc(x)`（形状证据：封存
// lowerMathRounding:5941-5945：整数操作数恒等 `out = add v, 0`；浮点转换分支
// 在本薄口不存在——i32 子集内浮点字面早由 saEvalI32 大声拒，故恒等即全量）。
func saEvalMathRounding(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math rounding needs 1 argument"
	}
	v, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, v))
	return out, false, ""
}

// saEvalMathMinMax 求 `Math.min(a,b)`/`Math.max(a,b)`（形状证据：封存
// lowerMathMinMax:5650-5687 两元折叠原样：slt/sgt + alloc 8 槽 + br + 两臂
// store + end load + 释放；spread 切片归约在本薄口大声拒）。
func saEvalMathMinMax(w printer.EmitTextWriter, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	for _, a := range args {
		if a != nil && a.Kind == ast.KindSpreadElement {
			return saEvalMathSpreadMinMax(w, method, a, scope, pos, refusals, nextTemp)
		}
	}
	if len(args) != 2 {
		return "", false, "Math." + method + " takes two scalars or one spread slice"
	}
	a0, msg0 := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
	if msg0 != "" {
		return "", false, msg0
	}
	a1, msg1 := saEvalI32(w, args[1], scope, pos, refusals, nextTemp)
	if msg1 != "" {
		return "", false, msg1
	}
	cmp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if method == "max" {
		w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", cmp, a0, a1))
	} else {
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cmp, a0, a1))
	}
	slot := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	tL := fmt.Sprintf("L_mm_t_%d", *scope.nextLabel)
	*scope.nextLabel++
	fL := fmt.Sprintf("L_mm_f_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_mm_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cmp, tL, fL))
	w.Write(fmt.Sprintf("%s:\n", tL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, a0))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", fL))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, a1))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", out, slot))
	w.Write(fmt.Sprintf("  !%s\n", slot))
	return out, false, ""
}

// saEvalMathSqrt 求 `Math.sqrt(x)`（形状证据：封存 lowerMathSqrt:5841-5891
// 整数二分原样：acc=0、lo=1、hi=x，`mid<=x/mid` 取最佳；具名绑定先快照。
// 浮点在本薄口不可达——浮点字面早由 saEvalI32 大声拒）。
func saEvalMathSqrt(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math.sqrt needs 1 argument"
	}
	x, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	fx := x
	if len(x) < 2 || x[:2] != "t_" {
		if _, ok := scope.types[x]; ok {
			cp := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, 0\n", cp, x))
			fx = cp
		}
	}
	acc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", acc))
	lo := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 1\n", lo))
	hi := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", hi, fx))
	topL := fmt.Sprintf("L_sqrt_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_sqrt_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	takeL := fmt.Sprintf("L_sqrt_take_%d", *scope.nextLabel)
	*scope.nextLabel++
	skipL := fmt.Sprintf("L_sqrt_skip_%d", *scope.nextLabel)
	*scope.nextLabel++
	nextL := fmt.Sprintf("L_sqrt_next_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_sqrt_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sle %s, %s\n", c, lo, hi))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	d := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", d, hi, lo))
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = div %s, 2\n", h, d))
	mid := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", mid, lo, h))
	q := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = div %s, %s\n", q, fx, mid))
	ok := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sle %s, %s\n", ok, mid, q))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", ok, takeL, skipL))
	w.Write(fmt.Sprintf("%s:\n", takeL))
	w.Write(fmt.Sprintf("  %s = %s\n", acc, mid))
	loN := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", loN, mid))
	w.Write(fmt.Sprintf("  %s = %s\n", lo, loN))
	w.Write(fmt.Sprintf("  jmp %s\n", nextL))
	w.Write(fmt.Sprintf("%s:\n", skipL))
	hiN := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", hiN, mid))
	w.Write(fmt.Sprintf("  %s = %s\n", hi, hiN))
	w.Write(fmt.Sprintf("  jmp %s\n", nextL))
	w.Write(fmt.Sprintf("%s:\n", nextL))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return acc, false, ""
}

// saEvalMathLog10 求 `Math.log10(x)`（形状证据：封存 lowerMathLog10:5894-5916
// 位数循环原样：`>=10` 则 `/=10` 且计数++）。
func saEvalMathLog10(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	if len(args) != 1 {
		return "", false, "Math.log10 needs 1 argument"
	}
	x, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", false, msg
	}
	lacc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", lacc))
	ltmp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", ltmp, x))
	topL := fmt.Sprintf("L_l10_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_l10_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_l10_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sge %s, 10\n", c, ltmp))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	q := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = div %s, 10\n", q, ltmp))
	w.Write(fmt.Sprintf("  %s = %s\n", ltmp, q))
	a := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", a, lacc))
	w.Write(fmt.Sprintf("  %s = %s\n", lacc, a))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return lacc, false, ""
}

// saEvalMathRandom 求 `Math.random()`（形状证据：封存 lowerMathRandom:5920-5936
// 确定性 LCG 原样：`__ts_rand_seed` 首用播 12345，`seed*1103515245+12345`，
// `ashr 16` 取低 15 位；序列非密码学，与 Node 不同已文档化）。
func saEvalMathRandom(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	const seed = "__ts_rand_seed"
	if ce.Arguments != nil && len(ce.Arguments.Nodes) != 0 {
		return "", false, "Math.random needs 0 arguments"
	}
	if _, ok := scope.types[seed]; !ok {
		w.Write(fmt.Sprintf("  %s = 12345\n", seed))
		scope.types[seed] = "i32"
	}
	rs := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 1103515245\n", rs, seed))
	rs2 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 12345\n", rs2, rs))
	w.Write(fmt.Sprintf("  %s = %s\n", seed, rs2))
	ro := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = ashr %s, 16\n", ro, seed))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, 32767\n", out, ro))
	return out, false, ""
}

// saEvalMathSpreadMinMax 求 `Math.min/max(...slice)`（形状证据：封存
// lowerMathSpreadMinMax:5690-5747 原样：取 len/data，best 初值为
// ∓INT 极值，索引巡回 take/skip 归约；源须为绑定数组或字面量）。
func saEvalMathSpreadMinMax(w printer.EmitTextWriter, method string, spread *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	se := spread.AsSpreadElement()
	var arr string
	if se.Expression != nil && se.Expression.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, se.Expression.AsNode(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, "unsupported spread slice: " + msg
		}
		arr = h
	} else if base, ok := saArrBase(scope, se.Expression.AsNode()); ok {
		arr = base
	} else {
		return "", false, "spread min/max needs a slice operand"
	}
	isMax := method == "max"
	ln := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, arr))
	data := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, arr))
	best := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if isMax {
		w.Write(fmt.Sprintf("  %s = -2147483648\n", best))
	} else {
		w.Write(fmt.Sprintf("  %s = 2147483647\n", best))
	}
	i := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", i))
	topL := fmt.Sprintf("L_mm_top_%d", *scope.nextLabel)
	*scope.nextLabel++
	bodyL := fmt.Sprintf("L_mm_body_%d", *scope.nextLabel)
	*scope.nextLabel++
	endL := fmt.Sprintf("L_mm_end_%d", *scope.nextLabel)
	*scope.nextLabel++
	takeL := fmt.Sprintf("L_mm_take_%d", *scope.nextLabel)
	*scope.nextLabel++
	skipL := fmt.Sprintf("L_mm_skip_%d", *scope.nextLabel)
	*scope.nextLabel++
	w.Write(fmt.Sprintf("%s:\n", topL))
	c := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", c, i, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	off := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, i))
	addr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	elem := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", elem, addr))
	cmp := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if isMax {
		w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", cmp, elem, best))
	} else {
		w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cmp, elem, best))
	}
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cmp, takeL, skipL))
	w.Write(fmt.Sprintf("%s:\n", takeL))
	nb := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", nb, elem))
	w.Write(fmt.Sprintf("  %s = %s\n", best, nb))
	w.Write(fmt.Sprintf("  jmp %s\n", skipL))
	w.Write(fmt.Sprintf("%s:\n", skipL))
	inext := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 1\n", inext, i))
	w.Write(fmt.Sprintf("  %s = %s\n", i, inext))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return best, false, ""
}

// saMathMethodName 识别 `Math.<m>`（m 为已支持方法集；其余 Math.* 本薄口拒）。
func saMathMethodName(n *ast.Node) (string, bool) {
	if n == nil || n.Kind != ast.KindPropertyAccessExpression {
		return "", false
	}
	pa := n.AsPropertyAccessExpression()
	if pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier || pa.Expression.Text() != "Math" ||
		pa.Name() == nil {
		return "", false
	}
	switch pa.Name().Text() {
	case "abs", "pow", "floor", "ceil", "round", "trunc", "min", "max", "sqrt", "log10", "random":
		return pa.Name().Text(), true
	}
	return "", false
}

// saEvalMathMethod 按方法名分发 Math 调用（含别名调用位；形状证据同各 saEvalMath*）。
func saEvalMathMethod(w printer.EmitTextWriter, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	switch method {
	case "abs":
		return saEvalMathAbs(w, ce, scope, pos, refusals, nextTemp)
	case "pow":
		return saEvalMathPow(w, ce, scope, pos, refusals, nextTemp)
	case "floor", "ceil", "round", "trunc":
		return saEvalMathRounding(w, ce, scope, pos, refusals, nextTemp)
	case "min", "max":
		return saEvalMathMinMax(w, method, ce, scope, pos, refusals, nextTemp)
	case "sqrt":
		return saEvalMathSqrt(w, ce, scope, pos, refusals, nextTemp)
	case "log10":
		return saEvalMathLog10(w, ce, scope, pos, refusals, nextTemp)
	case "random":
		return saEvalMathRandom(w, ce, scope, pos, refusals, nextTemp)
	}
	return "", false, "unsupported Math method " + method
}

// saTryMathAliasDecl 记录 `const f = Math.<m>` 及链式 `const g = f`（只记表，
// 不落字；形状证据：封存 lowerVarDeclList 前的 mathAliases:2957-2964 + 别名调用
// lowerMathCall:3795-3804）。返回 true 表示已认领。
func saTryMathAliasDecl(d *ast.Node, vd *ast.VariableDeclaration, name string, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) bool {
	if vd.Initializer == nil {
		return false
	}
	if m, ok := saMathMethodName(vd.Initializer); ok {
		if scope.mathAlias == nil {
			scope.mathAlias = map[string]string{}
		}
		scope.mathAlias[name] = m
		return true
	}
	if vd.Initializer.Kind == ast.KindIdentifier {
		if m, ok := scope.mathAlias[vd.Initializer.Text()]; ok {
			if scope.mathAlias == nil {
				scope.mathAlias = map[string]string{}
			}
			scope.mathAlias[name] = m
			return true
		}
	}
	_ = d
	_ = pos
	_ = refusals
	return false
}

func saEvalCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	if m, ok := saMathMethodName(ce.Expression); ok {
		return saEvalMathMethod(w, m, ce, scope, pos, refusals, nextTemp)
	}
	if saIsConsoleLog(ce) {
		ok, msg := saLowerConsoleLog(w, ce, scope, pos, refusals, nextTemp)
		if !ok {
			return "", false, msg
		}
		return "", true, ""
	}
	if ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "String" {
		return saLowerStrCall(w, ce, scope, pos, refusals, nextTemp)
	}
	if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" {
			return saLowerStrCall(w, ce, scope, pos, refusals, nextTemp)
		}
		if pa.Name() != nil && saIsStrMethod(pa.Name().Text()) && saIsStrExpr(pa.Expression, scope) {
			return saLowerStrCall(w, ce, scope, pos, refusals, nextTemp)
		}
		return "", false, "only direct function calls lowerable"
	}
	if ce.Expression == nil || ce.Expression.Kind != ast.KindIdentifier {
		return "", false, "only direct function calls lowerable"
	}
	name := ce.Expression.Text()
	if _, shadowed := scope.types[name]; shadowed {
		return "", false, name + " is not a function"
	}
	if m, ok := scope.mathAlias[name]; ok {
		return saEvalMathMethod(w, m, ce, scope, pos, refusals, nextTemp)
	}
	sig, ok := scope.funcs[name]
	if !ok {
		return "", false, "unknown function " + name
	}
	var args []string
	if ce.Arguments != nil {
		for i, a := range ce.Arguments.Nodes {
			// 形参种导向求值：str 形参走串求值（字面量/调用/拼接皆可），
			// arr/str 句柄标识符直传；其余走 bool 兼容求值。
			if len(sig.paramKinds) == len(ce.Arguments.Nodes) && sig.paramKinds[i] == "str" {
				h, msg := saEvalStr(w, a, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				args = append(args, h)
				continue
			}
			// 数组/字符串句柄直传（0/1 统一之外唯一的引用语义）；其余走 bool 兼容求值。
			if a != nil && a.Kind == ast.KindIdentifier {
				if k, ok := scope.types[a.Text()]; ok && (k == "arr" || k == "str") {
					args = append(args, a.Text())
					continue
				}
			}
			// 实参 bool 兼容求值。
			op, msg := saEvalBool(w, a, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", false, msg
			}
			args = append(args, op)
		}
	}
	if len(args) != sig.params {
		return "", false, fmt.Sprintf("arity mismatch for %s: want %d, got %d", name, sig.params, len(args))
	}
	call := fmt.Sprintf("call @%s(%s)", name, strings.Join(args, ", "))
	if sig.isVoid {
		w.Write(fmt.Sprintf("  %s\n", call))
		return "", true, ""
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", t, call))
	return t, false, ""
}

// saEvalI32 求 i32 操作数并按需发射临时量（形状证据：封存 lowerBinary:3214-3324
// saLowerPrefixUnary lowering 前缀一元（证据：封存 lowerPrefixUnary:3595-3619：
// 数字面正负折叠；`-x` 为 `sub 0, x`；`!x` 为 `eq x, 0`；其余大声拒）。
func saLowerPrefixUnary(w printer.EmitTextWriter, un *ast.PrefixUnaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if un.Operand != nil && un.Operand.Kind == ast.KindNumericLiteral &&
		(un.Operator == ast.KindMinusToken || un.Operator == ast.KindPlusToken) {
		t := un.Operand.Text()
		if saIsFloatLit(t) {
			return "", "float literal not in i32 subset"
		}
		if un.Operator == ast.KindMinusToken {
			return "-" + t, ""
		}
		return t, ""
	}
	switch un.Operator {
	case ast.KindPlusPlusToken, ast.KindMinusMinusToken:
		// 前缀返回新值（证据：封存 lowerIncDec:3632 + lowerPrefixUnary:3598-3601）。
		return saLowerIncDec(w, un.Operand, un.Operator == ast.KindPlusPlusToken, true, scope, pos, refusals, nextTemp)
	case ast.KindMinusToken:
		arg, msg := saEvalI32(w, un.Operand, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = sub 0, %s\n", t, arg))
		return t, ""
	case ast.KindExclamationToken:
		var arg string
		if un.Operand != nil && un.Operand.Kind == ast.KindIdentifier {
			nm := un.Operand.Text()
			if _, ok := scope.types[nm]; !ok {
				return "", "unknown variable " + nm
			}
			arg = nm
		} else {
			var msg string
			arg, msg = saEvalI32(w, un.Operand, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", t, arg))
		return t, ""
	default:
		return "", fmt.Sprintf("prefix operator %s not in subset", un.Operator.String())
	}
}

// saLowerPostfixUnary lowering 后缀一元（证据：封存 lowerPostfixUnary:3621-3630
// + lowerIncDec:3632：`++`/`--` 皆可；后缀返回旧值）。
func saLowerPostfixUnary(w printer.EmitTextWriter, un *ast.PostfixUnaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if un.Operator != ast.KindPlusPlusToken && un.Operator != ast.KindMinusMinusToken {
		return "", fmt.Sprintf("postfix operator %s not in subset", un.Operator.String())
	}
	return saLowerIncDec(w, un.Operand, un.Operator == ast.KindPlusPlusToken, false, scope, pos, refusals, nextTemp)
}

// saLowerIncDec lowering 自增（prefix=true 返回新值，false 返回旧值；仅 i32 绑定）。
func saLowerIncDec(w printer.EmitTextWriter, operand *ast.Node, up, prefix bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	target, ok := saBoundI32(scope, operand)
	if !ok {
		return "", "incdec target must be bound i32 variable"
	}
	op := "add"
	if !up {
		op = "sub"
	}
	if prefix {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
		w.Write(fmt.Sprintf("  %s = %s\n", target, t))
		return t, ""
	}
	old := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s\n", old, target))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
	w.Write(fmt.Sprintf("  %s = %s\n", target, t))
	return old, ""
}

// saLowerPow lowering 整数 `**`（形状证据：封存 lowerPowLoop:3326-3350：
// r=1；ctr=expo；top: cc=sgt ctr,0；br body/end；body: r*=base, ctr--；jmp top）。
func saLowerPow(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	base, msgB := saEvalI32(w, be.Left, scope, pos, refusals, nextTemp)
	if msgB != "" {
		return "", msgB
	}
	expo, msgE := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msgE != "" {
		return "", msgE
	}
	nextLabel := scope.nextLabel
	res := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 1\n", res))
	topL := fmt.Sprintf("L_pow_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_pow_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_pow_end_%d", *nextLabel)
	*nextLabel++
	ctr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, 0\n", ctr, expo))
	w.Write(fmt.Sprintf("%s:\n", topL))
	cc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sgt %s, 0\n", cc, ctr))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cc, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	nr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = mul %s, %s\n", nr, res, base))
	w.Write(fmt.Sprintf("  %s = %s\n", res, nr))
	nc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, 1\n", nc, ctr))
	w.Write(fmt.Sprintf("  %s = %s\n", ctr, nc))
	w.Write(fmt.Sprintf("  jmp %s\n", topL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	return res, ""
}

// saEvalI32 求 i32 操作数并按需发射临时量（形状证据：封存 lowerBinary:3214-3324
// add/sub/mul/div/srem/shl/ashr/lshr/and/or/xor、eq/ne/slt/sle/sgt/sge；`**`
// 走 lowerPowLoop:3326-3350；一元见 lowerPrefixUnary:3595-3619）。
// 返回 (operand, errMsg)，errMsg 非空即失败（调用方按上下文包装定位拒绝）。
func saEvalI32(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil {
		return "", "missing expression"
	}
	switch e.Kind {
	case ast.KindNumericLiteral:
		t := e.Text()
		if saIsFloatLit(t) {
			return "", "float literal " + t + " not in i32 subset"
		}
		return t, ""
	case ast.KindTrueKeyword:
		return "1", ""
	case ast.KindFalseKeyword:
		return "0", ""
	case ast.KindIdentifier:
		nm := e.Text()
		if k, ok := scope.types[nm]; ok {
			if k == "arr" {
				return "", "array " + nm + " in i32 expression"
			}
			if k == "str" {
				return "", "string " + nm + " in i32 expression"
			}
			if k != "i32" {
				return "", "boolean " + nm + " in i32 expression"
			}
			return nm, ""
		}
		return "", "unknown variable " + nm
	case ast.KindElementAccessExpression:
		return saLowerIndexLoadExpr(w, e.AsElementAccessExpression(), scope, pos, refusals, nextTemp)
	case ast.KindPropertyAccessExpression:
		pa := e.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Math" &&
			pa.Name() != nil {
			// `Math.PI`/`Math.E` 折叠为 3/2（形状证据：封存 stdlib.go:126-127
			// `@const:3`/`@const:2` integer subset）。
			switch pa.Name().Text() {
			case "PI":
				return "3", ""
			case "E":
				return "2", ""
			}
		}
		return saLowerLengthExpr(w, e.AsPropertyAccessExpression(), scope, pos, refusals, nextTemp)
	case ast.KindParenthesizedExpression:
		return saEvalI32(w, e.AsParenthesizedExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindAsExpression:
		// 纯类型级（TypeEraser 已擦类型，值层直通）。
		return saEvalI32(w, e.AsAsExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindSatisfiesExpression:
		return saEvalI32(w, e.AsSatisfiesExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindTypeAssertionExpression:
		return saEvalI32(w, e.AsTypeAssertion().Expression, scope, pos, refusals, nextTemp)
	case ast.KindNonNullExpression:
		return saEvalI32(w, e.AsNonNullExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindVoidExpression:
		// `void expr` 求值为 0，副作用保留（证据：封存 lowerExpr:2739-2743）。
		if _, msg := saEvalI32(w, e.AsVoidExpression().Expression, scope, pos, refusals, nextTemp); msg != "" {
			return "", msg
		}
		return "0", ""
	case ast.KindPrefixUnaryExpression:
		return saLowerPrefixUnary(w, e.AsPrefixUnaryExpression(), scope, pos, refusals, nextTemp)
	case ast.KindPostfixUnaryExpression:
		return saLowerPostfixUnary(w, e.AsPostfixUnaryExpression(), scope, pos, refusals, nextTemp)
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindEqualsToken {
			// 值位赋值折成寄存器拷贝（证据：封存 lowerBinary:3009-3010）。
			// 串目标走串求值（同形拷贝）。
			if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
				if k, ok := scope.types[be.Left.Text()]; ok && k == "str" {
					op, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
					if msg != "" {
						return "", msg
					}
					w.Write(fmt.Sprintf("  %s = %s\n", be.Left.Text(), op))
					return be.Left.Text(), ""
				}
			}
			target, ok := saBoundI32(scope, be.Left)
			if !ok {
				return "", "assignment to unknown/non-i32 variable"
			}
			op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			w.Write(fmt.Sprintf("  %s = %s\n", target, op))
			return target, ""
		}
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindPlusToken &&
			(saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)) {
			// `+` 遇串位即拼接，串句柄只可由串位取用（saEvalStr）；i32 位拒收。
			// 此处不落字（落字只走 saEvalStr 串路径），直接定位拒绝。
			return "", "string value in i32 expression"
		}
		if saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope) {
			// 串位仅 `+`（拼接）与 `==/!=`（内容相等）可走；其余算符大声拒。
			if be.OperatorToken != nil && (be.OperatorToken.Kind == ast.KindEqualsEqualsToken ||
				be.OperatorToken.Kind == ast.KindEqualsEqualsEqualsToken ||
				be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
				be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken) &&
				saIsStrValue(be.Left, scope) && saIsStrValue(be.Right, scope) {
				lh, msgL := saEvalStr(w, be.Left, scope, pos, refusals, nextTemp)
				if msgL != "" {
					return "", msgL
				}
				rh, msgR := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
				if msgR != "" {
					return "", msgR
				}
				neg := be.OperatorToken.Kind == ast.KindExclamationEqualsToken ||
					be.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken
				return saStringContentEq(w, lh, rh, neg, scope, nextTemp), ""
			}
			return "", "only +/==/!= operate on strings"
		}
		op, ok := map[ast.Kind]string{
			ast.KindPlusToken: "add", ast.KindMinusToken: "sub",
			ast.KindAsteriskToken: "mul", ast.KindSlashToken: "div",
			ast.KindPercentToken: "srem",
			ast.KindLessThanLessThanToken: "shl",
			ast.KindGreaterThanGreaterThanToken: "ashr",
			ast.KindGreaterThanGreaterThanGreaterThanToken: "lshr",
			ast.KindAmpersandToken: "and", ast.KindBarToken: "or",
			ast.KindCaretToken: "xor",
			ast.KindEqualsEqualsToken: "eq", ast.KindEqualsEqualsEqualsToken: "eq",
			ast.KindExclamationEqualsToken: "ne", ast.KindExclamationEqualsEqualsToken: "ne",
			ast.KindLessThanToken: "slt", ast.KindLessThanEqualsToken: "sle",
			ast.KindGreaterThanToken: "sgt", ast.KindGreaterThanEqualsToken: "sge",
			ast.KindAmpersandAmpersandToken: "and", ast.KindBarBarToken: "or",
		}[saBinaryOpKind(be)]
		if !ok {
			if saBinaryOpKind(be) == ast.KindAsteriskAsteriskToken {
				return saLowerPow(w, be, scope, pos, refusals, nextTemp)
			}
			return "", fmt.Sprintf("binary operator %s not in subset", saBinaryOpKind(be).String())
		}
		l, msgL := saEvalI32(w, be.Left, scope, pos, refusals, nextTemp)
		if msgL != "" {
			return "", msgL
		}
		r, msgR := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msgR != "" {
			return "", msgR
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, l, r))
		return t, ""
	case ast.KindCallExpression:
		if saCallIsStr(e.AsCallExpression(), scope) {
			if !saStrCallIsI32(e.AsCallExpression(), scope) {
				return "", "string value in i32 expression"
			}
		}
		op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if voidCall {
			return "", "void function call in value position"
		}
		return op, ""
	default:
		return "", fmt.Sprintf("unsupported expression kind %d", int(e.Kind))
	}
}

// saLowerVarDecl lowering 变量声明（`let/const x: number|i32 = <i32>`）。
// 形状证据：封存 lowerVarDeclList:1395-1470（using 拒、无 init const 拒、
// 解构拒、缺 init 绑零值、名按 bindingNameText 取标识符）。
func saLowerVarDecl(w printer.EmitTextWriter, s *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	vs := s.AsVariableStatement()
	return saLowerVarDeclList(w, s, vs.DeclarationList.AsVariableDeclarationList(), scope, pos, refusals, nextTemp)
}

// saLowerVarDeclList lowering 声明表（语句位与 for 初始化位共用）。
// saLowerArrayLiteral lowering i32 数组字面量（形状证据：封存
// lowerArrayLiteral:8684-8740：`alloc 16` 头 + `alloc len*4` 缓冲 + 逐槽
// `store … as i32` + 头部 ptr/len + `!buf`；spread/非 i32 元大声拒）。
func saLowerArrayLiteral(w printer.EmitTextWriter, n *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	al := n.AsArrayLiteralExpression()
	var elems []string
	if al.Elements != nil {
		for _, el := range al.Elements.Nodes {
			if el.Kind == ast.KindSpreadElement {
				return "", "spread elements are not lowerable"
			}
			v, msg := saEvalI32(w, el, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			elems = append(elems, v)
		}
	}
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	buf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  %s = alloc %d\n", buf, len(elems)*4))
	for i, v := range elems {
		p := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %d\n", p, buf, i*4))
		w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", p, v))
	}
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", h, buf))
	w.Write(fmt.Sprintf("  store %s + 8, %d as u64\n", h, len(elems)))
	w.Write(fmt.Sprintf("  !%s\n", buf))
	return h, ""
}

// saLowerCheckedIndex lowering 越界归零下标读（形状证据：封存
// lowerCheckedIndex:8522-8567：alloc 8 join 槽 + len/ult 检查 + data/mul/add
// 取址 + i32 读回；OOB 得 0；release 为空操作故略）。
func saLowerCheckedIndex(w printer.EmitTextWriter, base, idx string, nextLabel, nextTemp *int) string {
	freshT := func() string {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		return t
	}
	freshL := func(p string) string {
		l := fmt.Sprintf("L_%s_%d", p, *nextLabel)
		*nextLabel++
		return l
	}
	slot := freshT()
	endL := freshL("idx_end")
	oobL := freshL("idx_oob")
	loadL := freshL("idx_ok")
	w.Write(fmt.Sprintf("  %s = alloc 8\n", slot))
	ln := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", ln, base))
	ok := freshT()
	w.Write(fmt.Sprintf("  %s = ult %s, %s\n", ok, idx, ln))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", ok, loadL, oobL))
	w.Write(fmt.Sprintf("%s:\n", loadL))
	data := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", data, base))
	off := freshT()
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", off, idx))
	addr := freshT()
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, data, off))
	v := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", v, addr))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", slot, v))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", oobL))
	w.Write(fmt.Sprintf("  store %s + 0, 0 as ptr\n", slot))
	w.Write(fmt.Sprintf("  jmp %s\n", endL))
	w.Write(fmt.Sprintf("%s:\n", endL))
	dest := freshT()
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", dest, slot))
	return dest
}

// saLowerElementStore lowering `a[i] = v`（形状证据：封存 lowerElementStore:8437-8448）。
func saLowerElementStore(w printer.EmitTextWriter, base, idx, rhs string, nextTemp *int) {
	baseT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	offT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	ptrT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", baseT, base))
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", offT, idx))
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", ptrT, baseT, offT))
	w.Write(fmt.Sprintf("  store %s + 0, %s as i32\n", ptrT, rhs))
}

// saLowerIndexLoadExpr lowering 下标读表达式 `a[i]`（基须为绑定数组；
// `?.[]` 拒；下标走 i32 求值，读回走越界归零 join）。
func saLowerIndexLoadExpr(w printer.EmitTextWriter, ea *ast.ElementAccessExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if ea.QuestionDotToken != nil {
		return "", "optional index access not lowerable"
	}
	base, ok := saArrBase(scope, ea.Expression)
	if !ok {
		return "", "index base must be bound array"
	}
	idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	return saLowerCheckedIndex(w, base, idx, scope.nextLabel, nextTemp), ""
}

// saLowerLengthExpr lowering `.length`（数组/字符串头 +8 u64；其余成员拒）。
func saLowerLengthExpr(w printer.EmitTextWriter, pa *ast.PropertyAccessExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	_ = refusals
	if pa.QuestionDotToken != nil {
		return "", "optional member access not lowerable"
	}
	if nm := pa.Name(); nm == nil || nm.Kind != ast.KindIdentifier || nm.Text() != "length" {
		return "", "only .length member access lowerable"
	}
	if base, ok := saArrBase(scope, pa.Expression); ok {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, base))
		return t, ""
	}
	// 字符串 `.length`：字面量/调用结果等非常驻基先具化为句柄。
	h, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", ".length base must be bound array or string"
	}
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", t, h))
	return t, ""
}

// saArrBase 报告绑定数组变量的句柄名（未绑定/非数组即失败）。
func saArrBase(scope *saScope, n *ast.Node) (string, bool) {
	if n == nil || n.Kind != ast.KindIdentifier {
		return "", false
	}
	nm := n.Text()
	if k, ok := scope.types[nm]; !ok || k != "arr" {
		return "", false
	}
	return nm, true
}

func saLowerVarDeclList(w printer.EmitTextWriter, anchor *ast.Node, dl *ast.VariableDeclarationList, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if dl.AsNode().Flags&ast.NodeFlagsUsing != 0 {
		ln, col := pos(anchor.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "using declarations are not lowerable"})
		return false
	}
	isConst := dl.AsNode().Flags&ast.NodeFlagsConst != 0
	for _, d := range dl.Declarations.Nodes {
		vd := d.AsVariableDeclaration()
		nm := vd.Name()
		if nm == nil {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring declarations are not in subset"})
			return false
		}
		if nm.Kind != ast.KindIdentifier {
			if !saLowerDestructuringDecl(w, d, vd, nm.AsNode(), scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		name := nm.Text()
		if _, dup := scope.types[name]; dup {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		if _, dup := scope.mathAlias[name]; dup {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + name})
			return false
		}
		if saTryMathAliasDecl(d, vd, name, scope, pos, refusals) {
			continue
		}
		if vd.Type == nil {
			// 无注解推断（形状证据：封存 lowerVarDeclList:1415-1418/1463-1464
			// 未知注解缺省 i32 + 按初值类型绑定）：数组字面量/数组句柄走 arr 通道，
			// true/false 走 bool，其余 i32 求值；缺 init 绑 i32 零值（const 缺 init 拒）。
			if !saLowerInferredDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		vkind, ok := saAnnotKind(vd.Type)
		if !ok || (vkind != "i32" && vkind != "bool" && vkind != "arr" && vkind != "str") {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported annotation (i32/bool/arr/str locals only)"})
			return false
		}
		if vkind == "arr" {
			if !saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		if vkind == "str" {
			if !saLowerStrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp) {
				return false
			}
			continue
		}
		if vd.Initializer == nil {
			if isConst {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
				return false
			}
			w.Write(fmt.Sprintf("  %s = 0\n", name))
			scope.types[name] = vkind
			continue
		}
		if vd.Initializer.Kind == ast.KindArrowFunction || vd.Initializer.Kind == ast.KindFunctionExpression {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function initializer not lowerable"})
			return false
		}
		var op string
		var msg string
		if vkind == "bool" {
			op, msg = saEvalBool(w, vd.Initializer, scope, pos, refusals, nextTemp)
		} else {
			op, msg = saEvalI32(w, vd.Initializer, scope, pos, refusals, nextTemp)
		}
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		scope.types[name] = vkind
	}
	return true
}

// saLowerDestructuringDecl lowering 解构声明（形状证据：封存
// lowerDestructuringDecl:5414-5461 + destructureArray:5309-5333 +
// bindPatternName:5513-5524：数组位逐元 lowerCheckedIndex（越界归零 join）绑定 i32；
// 空穴跳过，rest 大声拒，嵌套位大声拒；对象位需结构体布局，本薄口大声拒）。
// 源须为数组句柄（已绑定数组直传；字面量现场构造）；函数值不可解构。
func saLowerDestructuringDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, pat *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer != nil && (vd.Initializer.Kind == ast.KindArrowFunction || vd.Initializer.Kind == ast.KindFunctionExpression) {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function values do not destructure"})
		return false
	}
	if pat.Kind != ast.KindArrayBindingPattern {
		ln, col := pos(d.Pos())
		if pat.Kind == ast.KindObjectBindingPattern {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object destructuring needs a recorded struct layout"})
		} else {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("binding pattern %d is not lowerable", int(pat.Kind))})
		}
		return false
	}
	if vd.Initializer == nil {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring declaration needs initializer"})
		return false
	}
	var arr string
	if vd.Initializer.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported array literal: " + msg})
			return false
		}
		arr = h
	} else if base, ok := saArrBase(scope, vd.Initializer); ok {
		arr = base
	} else {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "destructuring source must be bound array"})
		return false
	}
	idx := 0
	for _, el := range pat.AsBindingPattern().Elements.Nodes {
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
		v := saLowerCheckedIndex(w, arr, fmt.Sprintf("%d", idx), scope.nextLabel, nextTemp)
		w.Write(fmt.Sprintf("  %s = %s\n", name, v))
		scope.types[name] = "i32"
		idx++
	}
	return true
}

// saLowerInferredDecl lowering 无注解声明的类型推断（见上注释）。
// 数组/bool/i32 三通道复用已有求值与落字，不自造语义。
func saLowerInferredDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, name string, isConst bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer == nil {
		if isConst {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
			return false
		}
		w.Write(fmt.Sprintf("  %s = 0\n", name))
		scope.types[name] = "i32"
		return true
	}
	if vd.Initializer.Kind == ast.KindArrowFunction || vd.Initializer.Kind == ast.KindFunctionExpression {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "function initializer not lowerable"})
		return false
	}
	if vd.Initializer.Kind == ast.KindArrayLiteralExpression {
		return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
	}
	if vd.Initializer.Kind == ast.KindStringLiteral || vd.Initializer.Kind == ast.KindNoSubstitutionTemplateLiteral ||
		vd.Initializer.Kind == ast.KindTemplateExpression {
		// 无注解串推断（字面量/模板皆串位）。
		h, msg := saEvalStr(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		scope.types[name] = "str"
		return true
	}
	if _, ok := saArrBase(scope, vd.Initializer); ok {
		return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
	}
	if vd.Initializer.Kind == ast.KindTrueKeyword || vd.Initializer.Kind == ast.KindFalseKeyword {
		op, msg := saEvalBool(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		scope.types[name] = "bool"
		return true
	}
	if vd.Initializer.Kind == ast.KindIdentifier {
		if k, ok := scope.types[vd.Initializer.Text()]; ok && k == "bool" {
			op, msg := saEvalBool(w, vd.Initializer, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(d.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", name, op))
			scope.types[name] = "bool"
			return true
		}
	}
	op, msg := saEvalI32(w, vd.Initializer, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported initializer: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, op))
	scope.types[name] = "i32"
	return true
}

// ---- string（str 种：16 字节 {ptr,len} 切片句柄，与 arr 同模型）----
// 形状证据总纲：封存 tString:141（ptr 句柄）+ lowerStringLiteral:2974-2990
// （@const utf8 + 16 字节头）+ lowerStringMethod:7156-7388（sa_std/string.sai
// 现货直调）+ concatSlices:8909-8930（concat 经 sa_fmt_buffer_data/len 读回）+
// renderInterpValue:8854-8904（串直通；i32/bool 经 sext + sa_fmt_i64_into；
// f64 经 sa_fmt_f64_into）+ lowerTemplate:8823-8847 +
// lowerConsoleLog:7862-7887（print.sai，两操作数间空格 + 末尾换行）+
// stringContentEq:9146-9166（等长 + 零偏 indexOf 命中）+ 投影表
// stdlib.go:80-106（concat/string 方法→string.sai，console.log→io/print.sai，
// 模板/i32 插值→fmt.sai；i32 Math 系全 @inline 故不投）。
// sa_std 现货（string.sai/fmt.sai）：concat/from_char_code/from_code_point/
// index_of/last_index_of/starts_with/ends_with/to_lower-upper_ascii/repeat/
// pad_start-end/replace/code_point_at + i64_into/buffer_data-len。
// 本薄口只调以上现货；split（串元数组超 i32 槽模型）、tagged模板、
// Number.parseFloat（f64）、console.error（node 插件后端）一律大声拒。

// saStrIntern 字符串常量池录入（同文本去重；转义镜像封存）。
func saStrIntern(pool *saStrPool, text string) string {
	if n, ok := pool.seen[text]; ok {
		return n
	}
	n := fmt.Sprintf("str_const_%d", pool.next)
	pool.next++
	esc := strings.ReplaceAll(text, "\\", "\\\\")
	esc = strings.ReplaceAll(esc, "\"", "\\\"")
	esc = strings.ReplaceAll(esc, "\n", "\\n")
	esc = strings.ReplaceAll(esc, "\r", "\\r")
	esc = strings.ReplaceAll(esc, "\t", "\\t")
	fmt.Fprintf(&pool.buf, "@const %s = utf8:\"%s\\0\"\n", n, esc)
	pool.seen[text] = n
	return n
}

// saLowerStringLiteral 字符串字面量具化（@const utf8 + 16 字节头）。
func saLowerStringLiteral(w printer.EmitTextWriter, text string, scope *saScope, nextTemp *int) string {
	cname := saStrIntern(scope.strPool, text)
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", h))
	w.Write(fmt.Sprintf("  store %s + 0, &%s as ptr\n", h, cname))
	w.Write(fmt.Sprintf("  store %s + 8, %d as u64\n", h, len(text)))
	return h
}

// saExpandStr 展开句柄为 (ptr, len)（形状证据：封存 expandSlice:6126-6132）。
func saExpandStr(w printer.EmitTextWriter, h string, nextTemp *int) (string, string) {
	p := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	l := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", p, h))
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", l, h))
	return p, l
}

// saIsStrExpr 语法级判定表达式是否为串位（不落字；供 + 拼接与 i32 拒用）。
func saIsStrExpr(e *ast.Node, scope *saScope) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateExpression:
		return true
	case ast.KindIdentifier:
		k, ok := scope.types[e.Text()]
		return ok && k == "str"
	case ast.KindCallExpression:
		return saCallIsStr(e.AsCallExpression(), scope)
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindPlusToken {
			return saIsStrExpr(be.Left, scope) || saIsStrExpr(be.Right, scope)
		}
		return false
	case ast.KindParenthesizedExpression:
		return saIsStrExpr(e.AsParenthesizedExpression().Expression, scope)
	case ast.KindAsExpression:
		return saIsStrExpr(e.AsAsExpression().Expression, scope)
	case ast.KindSatisfiesExpression:
		return saIsStrExpr(e.AsSatisfiesExpression().Expression, scope)
	case ast.KindNonNullExpression:
		return saIsStrExpr(e.AsNonNullExpression().Expression, scope)
	case ast.KindTypeAssertionExpression:
		return saIsStrExpr(e.AsTypeAssertion().Expression, scope)
	default:
		return false
	}
}

// saIsStrValue 串值判定（串位扣除 i32 返回的串方法调用；供 i32 位门禁，
// 免得 `&&`/`+` 等 numerical 上下文被方法名误拦）。
func saIsStrValue(e *ast.Node, scope *saScope) bool {
	if e != nil && e.Kind == ast.KindCallExpression && saStrCallIsI32(e.AsCallExpression(), scope) {
		return false
	}
	return saIsStrExpr(e, scope)
}

// saCallIsStr 判定调用是否为串返回（String()/String.from*/串方法/同文件 string 函数）。
func saCallIsStr(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil {
		return false
	}
	if ce.Expression.Kind == ast.KindIdentifier {
		nm := ce.Expression.Text()
		if nm == "String" {
			return true
		}
		if sig, ok := scope.funcs[nm]; ok {
			return !sig.isVoid && sig.retKind == "string"
		}
		return false
	}
	if ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" &&
		pa.Name() != nil {
		switch pa.Name().Text() {
		case "fromCharCode", "fromCodePoint":
			return true
		}
		return false
	}
	if pa.Name() == nil || !saIsStrMethod(pa.Name().Text()) {
		return false
	}
	return saIsStrExpr(pa.Expression, scope)
}

// saStrCallIsI32 判定串调用是否为 i32 返回（indexOf 系/startsWith 系/
// charCodeAt 系/includes；其余串调用皆为串返回）。
func saStrCallIsI32(ce *ast.CallExpression, scope *saScope) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	if pa.Name() == nil {
		return false
	}
	switch pa.Name().Text() {
	case "charCodeAt", "codePointAt", "indexOf", "lastIndexOf", "startsWith", "endsWith", "includes":
		return saIsStrExpr(pa.Expression, scope)
	}
	return false
}
// split 另行大声拒，不在此列为“未知方法”拒）。
func saIsStrMethod(m string) bool {
	switch m {
	case "charCodeAt", "codePointAt", "indexOf", "lastIndexOf", "startsWith", "endsWith",
		"toLowerCase", "toUpperCase", "repeat", "padStart", "padEnd", "replace", "replaceAll",
		"includes", "charAt", "at", "trim", "trimStart", "trimEnd", "concat",
		"slice", "substring", "substr", "toString":
		return true
	}
	return false
}

// saEvalStr 求串操作数（返回 16 字节句柄）。
func saEvalStr(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil {
		return "", "missing expression"
	}
	switch e.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return saLowerStringLiteral(w, e.Text(), scope, nextTemp), ""
	case ast.KindTemplateExpression:
		return saLowerTemplate(w, e.AsTemplateExpression(), scope, pos, refusals, nextTemp)
	case ast.KindTaggedTemplateExpression:
		return "", "tagged templates are not lowerable (String.raw needs raw source text)"
	case ast.KindIdentifier:
		nm := e.Text()
		if k, ok := scope.types[nm]; ok {
			if k == "str" {
				return nm, ""
			}
			return "", nm + " is not a string"
		}
		return "", "unknown variable " + nm
	case ast.KindCallExpression:
		op, voidCall, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", msg
		}
		if voidCall {
			return "", "void function call in string position"
		}
		if !saCallIsStr(e.AsCallExpression(), scope) {
			return "", "non-string call in string position"
		}
		return op, ""
	case ast.KindBinaryExpression:
		be := e.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindPlusToken &&
			(saIsStrValue(be.Left, scope) || saIsStrValue(be.Right, scope)) {
			return saConcatStr(w, be.Left, be.Right, scope, pos, refusals, nextTemp)
		}
		return "", "only + concatenates strings"
	case ast.KindParenthesizedExpression:
		return saEvalStr(w, e.AsParenthesizedExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindAsExpression:
		return saEvalStr(w, e.AsAsExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindSatisfiesExpression:
		return saEvalStr(w, e.AsSatisfiesExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindNonNullExpression:
		return saEvalStr(w, e.AsNonNullExpression().Expression, scope, pos, refusals, nextTemp)
	case ast.KindTypeAssertionExpression:
		return saEvalStr(w, e.AsTypeAssertion().Expression, scope, pos, refusals, nextTemp)
	default:
		return "", "not a string expression"
	}
}

// saToSlice 任一可文本化操作数转切片（串直通；i32/bool 经 interp；其余拒）。
// 供模板/console/String() 共用（renderInterpValue 哲学：同 sa_fmt 现货）。
func saToSlice(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if saIsStrExpr(e, scope) {
		return saEvalStr(w, e, scope, pos, refusals, nextTemp)
	}
	var op string
	if e != nil && e.Kind == ast.KindIdentifier {
		if k, ok := scope.types[e.Text()]; ok && k == "bool" {
			var msg string
			op, msg = saEvalBool(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		} else {
			var msg string
			op, msg = saEvalI32(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		}
	} else {
		var msg string
		op, msg = saEvalI32(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			// bool 字面走 saEvalBool 兜底（saEvalI32 未必收 true/false）。
			op, msg = saEvalBool(w, e, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
		}
	}
	return saRenderInterp(w, op, scope, nextTemp), ""
}

// saRenderInterp 整数操作数经 sext + @sa_fmt_i64_into 落文本切片
// （形状证据：封存 renderInterpValue:8879-8900；bool 到此已是 0/1）。
func saRenderInterp(w printer.EmitTextWriter, v string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/fmt.sai")
	wide := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sext %s as i64\n", wide, v))
	numbuf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 64\n", numbuf))
	numlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 8\n", numlen))
	rc := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_i64_into(%s, 10, %s, 64, &%s)\n", rc, wide, numbuf, numlen))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as u64\n", nlen, numlen))
	vslice := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", vslice))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", vslice, numbuf))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", vslice, nlen))
	return vslice
}

// saConcatSlices 两切片经 @sa_string_concat 合并后读回新切片
// （形状证据：封存 concatSlices:8909-8930；需 string.sai + fmt.sai）。
func saConcatSlices(w printer.EmitTextWriter, left, right string, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/string.sai")
	scope.addImport("sa_std/fmt.sai")
	lptr, llen := saExpandStr(w, left, nextTemp)
	rptr, rlen := saExpandStr(w, right, nextTemp)
	obuf := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_string_concat(%s, %s, %s, %s)\n", obuf, lptr, llen, rptr, rlen))
	optr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_buffer_data(%s)\n", optr, obuf))
	olen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_fmt_buffer_len(%s)\n", olen, obuf))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
	w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, optr))
	w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, olen))
	return out
}

// saConcatStr `+` 拼接（两侧须皆为串位；混合数值须显式 String()，
// 子集门，大声拒——封存 lowerBinary 无数值隐式强制证据）。
func saConcatStr(w printer.EmitTextWriter, l, r *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if !saIsStrValue(l, scope) || !saIsStrValue(r, scope) {
		return "", "string + needs string operands on both sides (use String(x))"
	}
	lh, msgL := saEvalStr(w, l, scope, pos, refusals, nextTemp)
	if msgL != "" {
		return "", msgL
	}
	rh, msgR := saEvalStr(w, r, scope, pos, refusals, nextTemp)
	if msgR != "" {
		return "", msgR
	}
	return saConcatSlices(w, lh, rh, scope, nextTemp), ""
}

// saStringContentEq 串内容相等（等长 + 零偏 indexOf 命中；negate 取反。
// 形状证据：封存 stringContentEq:9146-9166）。
func saStringContentEq(w printer.EmitTextWriter, l, r string, negate bool, scope *saScope, nextTemp *int) string {
	scope.addImport("sa_std/string.sai")
	lp, ll := saExpandStr(w, l, nextTemp)
	rp, rl := saExpandStr(w, r, nextTemp)
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_string_index_of(%s, %s, %s, %s, 0)\n", idx, lp, ll, rp, rl))
	at0 := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, 0\n", at0, idx))
	samelen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = eq %s, %s\n", samelen, ll, rl))
	both := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = and %s, %s\n", both, at0, samelen))
	out := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	if negate {
		w.Write(fmt.Sprintf("  %s = eq %s, 0\n", out, both))
	} else {
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, both))
	}
	return out
}

// saLowerStrDecl lowering 字符串声明（字面量具化；同类句柄拷贝；
// 缺 init/const 缺 init/非串初值皆大声拒）。
func saLowerStrDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, name string, isConst bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer == nil {
		ln, col := pos(d.Pos())
		if isConst {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
		} else {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string declaration needs initializer"})
		}
		return false
	}
	h, msg := saEvalStr(w, vd.Initializer, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(d.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported string initializer: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, h))
	scope.types[name] = "str"
	return true
}

// saLowerStrCall lowering 串调用：String(x)/String.from*/串方法/同文件 string 函数。
func saLowerStrCall(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	if ce.Expression != nil && ce.Expression.Kind == ast.KindIdentifier && ce.Expression.Text() == "String" {
		args := []*ast.Node{}
		if ce.Arguments != nil {
			args = ce.Arguments.Nodes
		}
		if len(args) != 1 {
			return "", false, "String(x) needs 1 argument"
		}
		// String(x)：串直通，数值经 interp（形状证据：封存 lowerCall:3885-3900）。
		h, msg := saToSlice(w, args[0], scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		return h, false, ""
	}
	if ce.Expression != nil && ce.Expression.Kind == ast.KindPropertyAccessExpression {
		pa := ce.Expression.AsPropertyAccessExpression()
		if pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "String" &&
			pa.Name() != nil {
			switch pa.Name().Text() {
			case "fromCharCode", "fromCodePoint":
				args := []*ast.Node{}
				if ce.Arguments != nil {
					args = ce.Arguments.Nodes
				}
				if len(args) != 1 {
					return "", false, "String." + pa.Name().Text() + " needs 1 argument"
				}
				v, msg := saEvalI32(w, args[0], scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				sym := "sa_string_from_char_code"
				if pa.Name().Text() == "fromCodePoint" {
					sym = "sa_string_from_code_point"
				}
				scope.addImport("sa_std/string.sai")
				t := fmt.Sprintf("t_%d", *nextTemp)
				*nextTemp++
				w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", t, sym, v))
				return t, false, ""
			}
			return "", false, "unsupported String method " + pa.Name().Text()
		}
		if pa.QuestionDotToken != nil {
			return "", false, "optional member call not lowerable"
		}
		recv, msg := saEvalStr(w, pa.Expression, scope, pos, refusals, nextTemp)
		if msg != "" {
			return "", false, msg
		}
		if pa.Name() == nil {
			return "", false, "missing method name"
		}
		return saLowerStrMethod(w, recv, pa.Name().Text(), ce, scope, pos, refusals, nextTemp)
	}
	// 同文件 string 函数走通用调用核（saEvalCall 标识符分支按签名表落字）；
	// 此处仅处理 String()/String.from*/串方法。
	return "", false, "not a string call"
}

// saLowerStrMethod lowering 串方法全集（投影表 stdlib.go:95-105 + 封存
// lowerStringMethod:7173-7386；split 需串元数组，超 i32 槽模型，大声拒）。
func saLowerStrMethod(w printer.EmitTextWriter, recv, method string, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, bool, string) {
	scope.addImport("sa_std/string.sai")
	args := []*ast.Node{}
	if ce.Arguments != nil {
		args = ce.Arguments.Nodes
	}
	strArg := func(i int) (string, string) {
		if i >= len(args) {
			return "", "missing argument"
		}
		return saEvalStr(w, args[i], scope, pos, refusals, nextTemp)
	}
	intArg := func(i int) (string, string) {
		if i >= len(args) {
			return "", "missing argument"
		}
		return saEvalI32(w, args[i], scope, pos, refusals, nextTemp)
	}
	bp, bl := saExpandStr(w, recv, nextTemp)
	call1 := func(sym string, extra ...string) string {
		all := append([]string{bp, bl}, extra...)
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @%s(%s)\n", t, sym, strings.Join(all, ", ")))
		return t
	}
	switch method {
	case "charCodeAt":
		if len(args) != 1 {
			return "", false, "charCodeAt needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_string_code_point_at(%s, %s, %s)\n", t, bp, bl, a))
		return t, false, ""
	case "codePointAt":
		if len(args) != 1 {
			return "", false, "codePointAt needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		return call1("sa_string_code_point_at", a), false, ""
	case "indexOf", "lastIndexOf":
		if len(args) < 1 {
			return "", false, method + " needs 1 argument"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		from := "0"
		if len(args) > 1 {
			var msg string
			from, msg = intArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		sym := "sa_string_index_of"
		if method == "lastIndexOf" {
			sym = "sa_string_last_index_of"
		}
		return call1(sym, np, nl, from), false, ""
	case "startsWith", "endsWith":
		if len(args) != 1 {
			return "", false, method + " needs 1 argument"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		sym := "sa_string_starts_with"
		if method == "endsWith" {
			sym = "sa_string_ends_with"
		}
		return call1(sym, np, nl), false, ""
	case "toLowerCase":
		return call1("sa_string_to_lower_ascii"), false, ""
	case "toUpperCase":
		return call1("sa_string_to_upper_ascii"), false, ""
	case "repeat":
		if len(args) != 1 {
			return "", false, "repeat needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		return call1("sa_string_repeat", a), false, ""
	case "padStart", "padEnd":
		if len(args) < 1 {
			return "", false, method + " needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		padArg := saLowerStringLiteral(w, " ", scope, nextTemp)
		if len(args) > 1 {
			var msg string
			padArg, msg = strArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		pp, pl := saExpandStr(w, padArg, nextTemp)
		sym := "sa_string_pad_start"
		if method == "padEnd" {
			sym = "sa_string_pad_end"
		}
		return call1(sym, a, pp, pl), false, ""
	case "replace", "replaceAll":
		need := 2
		if method == "replace" {
			need = -1
		}
		if (need == -1 && len(args) != 2 && len(args) != 3) || (need == 2 && len(args) != 2) {
			return "", false, method + " needs 2 arguments"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		rp_, msg := strArg(1)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		rp, rl := saExpandStr(w, rp_, nextTemp)
		all := "0"
		if method == "replaceAll" {
			all = "1"
		} else if len(args) == 3 {
			var msg string
			all, msg = intArg(2)
			if msg != "" {
				return "", false, msg
			}
		}
		return call1("sa_string_replace", np, nl, rp, rl, all), false, ""
	case "includes":
		if len(args) < 1 {
			return "", false, "includes needs 1 argument"
		}
		n, msg := strArg(0)
		if msg != "" {
			return "", false, msg
		}
		np, nl := saExpandStr(w, n, nextTemp)
		from := "0"
		if len(args) > 1 {
			var msg string
			from, msg = intArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		idx := call1("sa_string_index_of", np, nl, from)
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = ne %s, -1\n", out, idx))
		return out, false, ""
	case "charAt", "at":
		if len(args) != 1 {
			return "", false, method + " needs 1 argument"
		}
		a, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		sel := a
		if method == "at" {
			// 负下标自末端起（无分支形；形状证据：封存 lowerStringMethod:7288-7295）。
			isneg := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = slt %s, 0\n", isneg, a))
			adj := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = mul %s, %s\n", adj, bl, isneg))
			sel = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", sel, a, adj))
		}
		addr := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, %s\n", addr, bp, sel))
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, addr))
		w.Write(fmt.Sprintf("  store %s + 8, 1 as u64\n", out))
		return out, false, ""
	case "trim", "trimStart", "trimEnd":
		// ascii 三件套合成（形状证据：封存 lowerStringMethod:7304-7335）。
		start := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_str_trim_ascii_start_index(%s, %s)\n", start, bp, bl))
		full := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = call @sa_str_trim_ascii_end_len(%s, %s)\n", full, bp, bl))
		s, l := start, full
		if method == "trimStart" {
			rest := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sub %s, %s\n", rest, bl, start))
			l = rest
		} else if method == "trimEnd" {
			s = "0"
		} else {
			rest := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = sub %s, %s\n", rest, full, start))
			l = rest
		}
		nptr := bp
		if s != "0" {
			nptr = fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", nptr, bp, s))
		}
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, nptr))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, l))
		return out, false, ""
	case "concat":
		// 逐片折叠 @sa_string_concat（形状证据：封存 lowerStringMethod:7336-7347；
		// 注意此处直接折叠缓冲柄，与 + 拼接的读回形不同，各守其源）。
		acc := recv
		for i := range args {
			n, msg := strArg(i)
			if msg != "" {
				return "", false, msg
			}
			np, nl := saExpandStr(w, n, nextTemp)
			abp, abl := saExpandStr(w, acc, nextTemp)
			t := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = call @sa_string_concat(%s, %s, %s, %s)\n", t, abp, abl, np, nl))
			acc = t
		}
		return acc, false, ""
	case "slice", "substring", "substr":
		// 钳位子切片（形状证据：封存 lowerStringMethod:7348-7369 + clampRange:7393-7465）。
		if len(args) < 1 {
			return "", false, method + " needs 1 argument"
		}
		a0, msg := intArg(0)
		if msg != "" {
			return "", false, msg
		}
		end := bl
		if len(args) > 1 {
			var msg string
			end, msg = intArg(1)
			if msg != "" {
				return "", false, msg
			}
		}
		if method == "substr" {
			nend := fmt.Sprintf("t_%d", *nextTemp)
			*nextTemp++
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", nend, a0, end))
			end = nend
		}
		s, l := saClampRange(w, bp, bl, a0, end, method == "substring", scope, nextTemp)
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = alloc 16\n", out))
		w.Write(fmt.Sprintf("  store %s + 0, %s as ptr\n", out, s))
		w.Write(fmt.Sprintf("  store %s + 8, %s as u64\n", out, l))
		return out, false, ""
	case "split":
		return "", false, "split needs string arrays (beyond i32 slots)"
	case "toString":
		return recv, false, ""
	}
	return "", false, "unsupported string method " + method
}

// saClampRange 钳位 [start, end) 到 [0, len]（负值自末端起钳；substring 另
// 交换逆序界并将负值记 0。形状证据：封存 clampRange:7393-7465）。
func saClampRange(w printer.EmitTextWriter, bp, bl, start, end string, substring bool, scope *saScope, nextTemp *int) (string, string) {
	norm := func(v string) string {
		neg := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		adj := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		out := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		nL := fmt.Sprintf("L_cl_neg_%d", *scope.nextLabel)
		*scope.nextLabel++
		nN := fmt.Sprintf("L_cl_nneg_%d", *scope.nextLabel)
		*scope.nextLabel++
		nE := fmt.Sprintf("L_cl_end_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = slt %s, 0\n", neg, v))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", neg, nL, nN))
		w.Write(fmt.Sprintf("%s:\n", nL))
		if substring {
			w.Write(fmt.Sprintf("  %s = 0\n", adj))
		} else {
			w.Write(fmt.Sprintf("  %s = add %s, %s\n", adj, bl, v))
		}
		w.Write(fmt.Sprintf("  jmp %s\n", nE))
		w.Write(fmt.Sprintf("%s:\n", nN))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", adj, v))
		w.Write(fmt.Sprintf("  jmp %s\n", nE))
		w.Write(fmt.Sprintf("%s:\n", nE))
		lo := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		loT := fmt.Sprintf("L_cl_lot_%d", *scope.nextLabel)
		*scope.nextLabel++
		loF := fmt.Sprintf("L_cl_lof_%d", *scope.nextLabel)
		*scope.nextLabel++
		loE := fmt.Sprintf("L_cl_loe_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = slt %s, 0\n", lo, adj))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", lo, loT, loF))
		w.Write(fmt.Sprintf("%s:\n", loT))
		w.Write(fmt.Sprintf("  %s = 0\n", out))
		w.Write(fmt.Sprintf("  jmp %s\n", loE))
		w.Write(fmt.Sprintf("%s:\n", loF))
		hi := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		hiT := fmt.Sprintf("L_cl_hit_%d", *scope.nextLabel)
		*scope.nextLabel++
		hiF := fmt.Sprintf("L_cl_hif_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", hi, adj, bl))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", hi, hiT, hiF))
		w.Write(fmt.Sprintf("%s:\n", hiT))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, bl))
		w.Write(fmt.Sprintf("  jmp %s\n", loE))
		w.Write(fmt.Sprintf("%s:\n", hiF))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", out, adj))
		w.Write(fmt.Sprintf("  jmp %s\n", loE))
		w.Write(fmt.Sprintf("%s:\n", loE))
		return out
	}
	s := norm(start)
	f := norm(end)
	if substring {
		sw := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		c := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		tL := fmt.Sprintf("L_cl_swap_%d", *scope.nextLabel)
		*scope.nextLabel++
		kL := fmt.Sprintf("L_cl_keep_%d", *scope.nextLabel)
		*scope.nextLabel++
		dL := fmt.Sprintf("L_cl_done_%d", *scope.nextLabel)
		*scope.nextLabel++
		w.Write(fmt.Sprintf("  %s = sgt %s, %s\n", c, s, f))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", c, tL, kL))
		w.Write(fmt.Sprintf("%s:\n", tL))
		w.Write(fmt.Sprintf("  %s = add %s, 0\n", sw, s))
		w.Write(fmt.Sprintf("  %s = %s\n", s, f))
		w.Write(fmt.Sprintf("  %s = %s\n", f, sw))
		w.Write(fmt.Sprintf("  jmp %s\n", dL))
		w.Write(fmt.Sprintf("%s:\n", kL))
		w.Write(fmt.Sprintf("  jmp %s\n", dL))
		w.Write(fmt.Sprintf("%s:\n", dL))
	}
	nptr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", nptr, bp, s))
	nlen := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = sub %s, %s\n", nlen, f, s))
	return nptr, nlen
}

// saLowerTemplate 模板字面量（头 +  spans 插值 + tails 逐片拼接；
// 形状证据：封存 lowerTemplate:8823-8847；插值仅 i32/bool/串，f64 拒）。
func saLowerTemplate(w printer.EmitTextWriter, tp *ast.TemplateExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/string.sai")
	scope.addImport("sa_std/fmt.sai")
	head := ""
	if tp.Head != nil {
		head = tp.Head.Text()
	}
	acc := saLowerStringLiteral(w, head, scope, nextTemp)
	if tp.TemplateSpans != nil {
		for _, sp := range tp.TemplateSpans.Nodes {
			span := sp.AsTemplateSpan()
			part, msg := saToSlice(w, span.Expression, scope, pos, refusals, nextTemp)
			if msg != "" {
				return "", msg
			}
			acc = saConcatSlices(w, acc, part, scope, nextTemp)
			tail := ""
			if span.Literal != nil {
				tail = span.Literal.Text()
			}
			if tail != "" {
				tailH := saLowerStringLiteral(w, tail, scope, nextTemp)
				acc = saConcatSlices(w, acc, tailH, scope, nextTemp)
			}
		}
	}
	return acc, ""
}

// saLowerConsoleLog `console.log(...)`（操作数经 interp 转切片，空格分隔，
// 末尾换行；形状证据：封存 lowerConsoleLog:7862-7887；console.error 走
// node 插件后端，本薄口大声拒）。
func saLowerConsoleLog(w printer.EmitTextWriter, ce *ast.CallExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (bool, string) {
	scope.addImport("sa_std/io/print.sai")
	scope.addImport("sa_std/fmt.sai")
	scope.addImport("sa_std/string.sai")
	var argNodes []*ast.Node
	if ce.Arguments != nil {
		argNodes = ce.Arguments.Nodes
	}
	for i, a := range argNodes {
		if i > 0 {
			seg := saLowerStringLiteral(w, " ", scope, nextTemp)
			bp, bl := saExpandStr(w, seg, nextTemp)
			w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
		}
		seg, msg := saToSlice(w, a, scope, pos, refusals, nextTemp)
		if msg != "" {
			return false, msg
		}
		bp, bl := saExpandStr(w, seg, nextTemp)
		w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
	}
	seg := saLowerStringLiteral(w, "\n", scope, nextTemp)
	bp, bl := saExpandStr(w, seg, nextTemp)
	w.Write(fmt.Sprintf("  call @sa_print_bytes(&%s, %s)\n", bp, bl))
	return true, ""
}

// saIsConsoleLog 识别 `console.log(...)`。
func saIsConsoleLog(ce *ast.CallExpression) bool {
	if ce.Expression == nil || ce.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := ce.Expression.AsPropertyAccessExpression()
	return pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "console" &&
		pa.Name() != nil && pa.Name().Text() == "log"
}

// saLowerArrDecl lowering 数组声明（`let a: number[] = […]` 字面构造；
// 同类句柄拷贝 `= b`；缺 init/const 缺 init/非字面皆大声拒）。
func saLowerArrDecl(w printer.EmitTextWriter, d *ast.Node, vd *ast.VariableDeclaration, name string, isConst bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if vd.Initializer == nil {
		ln, col := pos(d.Pos())
		if isConst {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "const declarations must be initialized"})
		} else {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array declaration needs initializer"})
		}
		return false
	}
	if vd.Initializer.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, vd.Initializer, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(d.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported array literal: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		scope.types[name] = "arr"
		return true
	}
	if src, ok := saArrBase(scope, vd.Initializer); ok {
		w.Write(fmt.Sprintf("  %s = %s\n", name, src))
		scope.types[name] = "arr"
		return true
	}
	ln, col := pos(d.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array initializer must be literal or array"})
	return false
}

// saBoundI32 报告绑定 i32 变量名（未绑定或非 i32 即失败）。
func saBoundI32(scope *saScope, n *ast.Node) (string, bool) {
	if n == nil || n.Kind != ast.KindIdentifier {
		return "", false
	}
	nm := n.Text()
	if k, ok := scope.types[nm]; !ok || k != "i32" {
		return "", false
	}
	return nm, true
}

// saCompoundOp 映射复合赋值到 SA 算符（证据：封存 lowerCompoundAssign:3394-3417）。
func saCompoundOp(op ast.Kind) (string, bool) {
	mapped, ok := map[ast.Kind]string{
		ast.KindPlusEqualsToken: "add", ast.KindMinusEqualsToken: "sub",
		ast.KindAsteriskEqualsToken: "mul", ast.KindSlashEqualsToken: "div",
		ast.KindPercentEqualsToken: "srem",
	}[op]
	return mapped, ok
}

// saLowerElementAssign lowering `a[i] = v`（仅 plain `=`；下标/右值走 i32 求值）。
func saLowerElementAssign(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node) bool {
	ea := be.Left.AsElementAccessExpression()
	if ea.QuestionDotToken != nil {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "optional index store not lowerable"})
		return false
	}
	base, ok := saArrBase(scope, ea.Expression)
	if !ok {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "index store base must be bound array"})
		return false
	}
	idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported index: " + msg})
		return false
	}
	rhs, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported element rhs: " + msg})
		return false
	}
	saLowerElementStore(w, base, idx, rhs, nextTemp)
	return true
}

// saLowerCompound lowering x <op>= e（语句位与增量位共用；元素目标读改写回）。
func saLowerCompound(w printer.EmitTextWriter, be *ast.BinaryExpression, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node) bool {
	if be.Left != nil && be.Left.Kind == ast.KindElementAccessExpression {
		ea := be.Left.AsElementAccessExpression()
		if ea.QuestionDotToken != nil {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "optional index store not lowerable"})
			return false
		}
		base, ok := saArrBase(scope, ea.Expression)
		if !ok {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "index store base must be bound array"})
			return false
		}
		idx, msg := saEvalI32(w, ea.ArgumentExpression, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported index: " + msg})
			return false
		}
		// 读-改-写回：join 读回当前值，算符作用后存回同址。
		cur := saLowerCheckedIndex(w, base, idx, scope.nextLabel, nextTemp)
		r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
			return false
		}
		op, _ := saCompoundOp(saBinaryOpKind(be))
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, cur, r))
		saLowerElementStore(w, base, idx, t, nextTemp)
		return true
	}
	target, ok := saBoundI32(scope, be.Left)
	if !ok {
		// 串目标仅 `+=` 拼接（两侧串位；混合数值须显式 String()）。
		if be.Left != nil && be.Left.Kind == ast.KindIdentifier {
			if k, bound := scope.types[be.Left.Text()]; bound && k == "str" &&
				saBinaryOpKind(be) == ast.KindPlusEqualsToken {
				h, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
				if msg != "" {
					ln, col := pos(where.Pos())
					*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported string += rhs: " + msg})
					return false
				}
				out := saConcatSlices(w, be.Left.Text(), h, scope, nextTemp)
				w.Write(fmt.Sprintf("  %s = %s\n", be.Left.Text(), out))
				return true
			}
		}
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "compound assignment to unknown/non-i32 variable"})
		return false
	}
	r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(where.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported compound rhs: " + msg})
		return false
	}
	op, _ := saCompoundOp(saBinaryOpKind(be))
	t := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, target, r))
	w.Write(fmt.Sprintf("  %s = %s\n", target, t))
	return true
}
// saLowerExprStmt lowering 表达式语句：调用（值/void 皆可，结果丢弃）与赋值
// （`x = <i32>`，x 须已绑定；复合赋分流）。其余一律大声拒。
func saLowerExprStmt(w printer.EmitTextWriter, s *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	e := s.AsExpressionStatement().Expression
	if e == nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement (calls and assignments only)"})
		return false
	}
	if e.Kind == ast.KindCallExpression {
		if _, _, msg := saEvalCall(w, e.AsCallExpression(), scope, pos, refusals, nextTemp); msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported call statement: " + msg})
			return false
		}
		return true
	}
	if e.Kind != ast.KindBinaryExpression {
		// 其余表达式语句求值后丢弃（证据：封存 lowerExprStatement:2712-2715
		// 只 lower 表达式：`i++` 等副作用保留，无副作用的纯表达式亦然）。
		if _, msg := saEvalI32(w, e, scope, pos, refusals, nextTemp); msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement: " + msg})
			return false
		}
		return true
	}
	be := e.AsBinaryExpression()
	if _, ok := saCompoundOp(saBinaryOpKind(be)); ok {
		return saLowerCompound(w, be, scope, pos, refusals, nextTemp, s)
	}
	if be.OperatorToken == nil || be.OperatorToken.Kind != ast.KindEqualsToken {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement (plain x = i32 only)"})
		return false
	}
	// 元素目标 `a[i] = v`（仅 plain `=`；复合走 saLowerCompound 的元素分支）。
	if be.Left != nil && be.Left.Kind == ast.KindElementAccessExpression {
		return saLowerElementAssign(w, be, scope, pos, refusals, nextTemp, s)
	}
	if be.Left == nil || be.Left.Kind != ast.KindIdentifier {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported expression statement (plain x = i32 only)"})
		return false
	}
	name := be.Left.Text()
	k, ok := scope.types[name]
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to unknown variable " + name})
		return false
	}
	if k != "i32" && k != "bool" && k != "arr" && k != "str" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to non-i32 variable " + name})
		return false
	}
	if k == "arr" {
		// 数组句柄拷贝（同类相授）。
		if src, ok := saArrBase(scope, be.Right); ok {
			w.Write(fmt.Sprintf("  %s = %s\n", name, src))
			return true
		}
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array assignment needs array handle"})
		return false
	}
	if k == "str" {
		// 字符串句柄拷贝（同类相授）。
		h, msg := saEvalStr(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "string assignment needs string value: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, h))
		return true
	}
	var op string
	var msg string
	if k == "bool" {
		op, msg = saEvalBool(w, be.Right, scope, pos, refusals, nextTemp)
	} else {
		op, msg = saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
	}
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported assignment rhs: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  %s = %s\n", name, op))
	return true
}

// saLowerWhile lowering while（形状证据：封存 lowerWhile:1805-1835
// top/body/end + br + 体 + jmp top + end；false 恒假消死臂）。
func saLowerWhile(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	ws := s.AsWhileStatement()
	if ws.Expression != nil && ws.Expression.Kind == ast.KindFalseKeyword {
		return true
	}
	if be := saBoolSideCond(ws.Expression, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
		return false
	}
	bodyStmts, ok := saEmbeddedBlock(ws.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported while body"})
		return false
	}
	topL := fmt.Sprintf("L_while_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_while_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_while_end_%d", *nextLabel)
	*nextLabel++
	// 条件求值落在顶标号之后（每轮重算），先落顶再求条件。
	w.Write(fmt.Sprintf("%s:\n", topL))
	condOp, msg := saCondOperand(w, ws.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported while condition: " + msg})
		return false
	}
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL})
	saBindPendingLabels(scope, false)
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if !saArmTerminates(bodyStmts) {
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saBodyHasContinue 报告循环体是否可能执行 continue（证据：封存 labels.go:100-127；
// 函数边界重置目标，其余过近似——多出的 cont 标号无害）。
func saBodyHasContinue(n *ast.Node) bool {
	found := false
	var walk func(x *ast.Node)
	walk = func(x *ast.Node) {
		if x == nil || found {
			return
		}
		if x.Kind == ast.KindContinueStatement {
			found = true
			return
		}
		switch x.Kind {
		case ast.KindFunctionDeclaration, ast.KindArrowFunction,
			ast.KindFunctionExpression, ast.KindClassDeclaration:
			return
		}
		x.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk(n)
	return found
}

// saLowerForInit lowering for 初始化位（变量声明表走声明路径；表达式须为赋值形）。
func saLowerForInit(w printer.EmitTextWriter, init *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	if init == nil {
		return true
	}
	switch init.Kind {
	case ast.KindVariableDeclarationList:
		dl := init.AsVariableDeclarationList()
		return saLowerVarDeclList(w, init, dl, scope, pos, refusals, nextTemp)
	case ast.KindVariableStatement:
		return saLowerVarDecl(w, init, scope, pos, refusals, nextTemp)
	default:
		// 表达式初始化位：仅接受 x = <i32> 赋值形（与语句位同门）。
		if init.Kind != ast.KindBinaryExpression {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer"})
			return false
		}
		be := init.AsBinaryExpression()
		if be.OperatorToken == nil || be.OperatorToken.Kind != ast.KindEqualsToken ||
			be.Left == nil || be.Left.Kind != ast.KindIdentifier {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer"})
			return false
		}
		name := be.Left.Text()
		if _, ok := scope.types[name]; !ok {
			// 初始化位允许首次绑定（`for (i = 0;;)`），视同 let 隐式声明。
			scope.types[name] = "i32"
		} else if scope.types[name] != "i32" {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "assignment to non-i32 variable " + name})
			return false
		}
		op, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(init.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for initializer: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  %s = %s\n", name, op))
		return true
	}
}

// saLowerIncr lowering for 增量位：`x++`/`++x`/`x += K` 等（证据：封存
// canonicalForStep:1855-1900 只认 ++ 系；复合赋值的 op 映射见 lowerCompoundAssign:3394-3417）。
// 其余一律大声拒（遗留 legacy 接受任意表达式，本子集收紧为门）。
func saLowerIncr(w printer.EmitTextWriter, incr *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) bool {
	// 增量位只用写回副作用，不发旧值临时量（表达式位经 saLowerIncDec 保留旧值语义）。
	emitBump := func(target, op string) {
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, 1\n", t, op, target))
		w.Write(fmt.Sprintf("  %s = %s\n", target, t))
	}
	switch incr.Kind {
	case ast.KindPostfixUnaryExpression:
		un := incr.AsPostfixUnaryExpression()
		if un.Operator != ast.KindPlusPlusToken && un.Operator != ast.KindMinusMinusToken {
			break
		}
		target, ok := saBoundI32(scope, un.Operand)
		if !ok {
			break
		}
		op := "add"
		if un.Operator == ast.KindMinusMinusToken {
			op = "sub"
		}
		emitBump(target, op)
		return true
	case ast.KindPrefixUnaryExpression:
		un := incr.AsPrefixUnaryExpression()
		if un.Operator != ast.KindPlusPlusToken && un.Operator != ast.KindMinusMinusToken {
			break
		}
		target, ok := saBoundI32(scope, un.Operand)
		if !ok {
			break
		}
		op := "add"
		if un.Operator == ast.KindMinusMinusToken {
			op = "sub"
		}
		emitBump(target, op)
		return true
	case ast.KindBinaryExpression:
		be := incr.AsBinaryExpression()
		if be.OperatorToken != nil && be.OperatorToken.Kind == ast.KindEqualsToken {
			// `x = <i32>` 赋值形增量（与语句位同门）。
			target, okT := saBoundI32(scope, be.Left)
			if !okT {
				break
			}
			r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
			if msg != "" {
				ln, col := pos(incr.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
				return false
			}
			w.Write(fmt.Sprintf("  %s = %s\n", target, r))
			return true
		}
		op, ok := saCompoundOp(saBinaryOpKind(be))
		if !ok {
			break
		}
		target, okT := saBoundI32(scope, be.Left)
		if !okT {
			break
		}
		r, msg := saEvalI32(w, be.Right, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(incr.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor: " + msg})
			return false
		}
		t := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = %s %s, %s\n", t, op, target, r))
		w.Write(fmt.Sprintf("  %s = %s\n", target, t))
		return true
	default:
		ln, col := pos(incr.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor (x++/--x/x=<i32>/x+=K only)"})
		return false
	}
	ln, col := pos(incr.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for incrementor (x++/--x/x=<i32>/x+=K only)"})
	return false
}

// saLowerFor lowering for（形状证据：封存 lowerFor:2043-2122 legacy 形；
// canonical 宏形 FOR_INIT/FOR_CHECK/FOR_NEXT 暂不采用，统一 legacy br 形）。
func saLowerFor(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	fs := s.AsForStatement()
	if !saLowerForInit(w, fs.Initializer, scope, pos, refusals, nextTemp) {
		return false
	}
	// Never-taken C 循环只发射 init（证据：封存 lowerFor:2061-2068）。
	if fs.Condition != nil && fs.Condition.Kind == ast.KindFalseKeyword {
		return true
	}
	if be := saBoolSideCond(fs.Condition, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
		return false
	}
	bodyNode := fs.Statement
	var bodyStmts []*ast.Node
	if bodyNode != nil {
		var ok bool
		bodyStmts, ok = saEmbeddedBlock(bodyNode)
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for body"})
			return false
		}
	}
	topL := fmt.Sprintf("L_for_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_for_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_for_end_%d", *nextLabel)
	*nextLabel++
	needCont := fs.Incrementor != nil && bodyNode != nil && saBodyHasContinue(bodyNode)
	contL := topL
	if needCont {
		contL = fmt.Sprintf("L_for_cont_%d", *nextLabel)
		*nextLabel++
	}
	w.Write(fmt.Sprintf("%s:\n", topL))
	if fs.Condition != nil {
		condOp, msg := saCondOperand(w, fs.Condition, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for condition: " + msg})
			return false
		}
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, bodyL, endL))
	} else {
		w.Write(fmt.Sprintf("  jmp %s\n", bodyL))
	}
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: contL, end: endL})
	saBindPendingLabels(scope, false)
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if needCont {
		w.Write(fmt.Sprintf("%s:\n", contL))
	}
	doIncr := fs.Incrementor != nil && (!saArmTerminates(bodyStmts) || needCont)
	if doIncr {
		if !saLowerIncr(w, fs.Incrementor, scope, pos, refusals, nextTemp) {
			return false
		}
	}
	if !saArmTerminates(bodyStmts) {
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saForBindingName 取 for-of/for-in 循环变量名（形状证据：封存
// foBindingName:11440-11450 + foBindingPattern:11428-11438：
// 初始化位须为 VariableDeclarationList 单声明；标识符取名单，
// 非标识符 Name 即 pattern 位）。
// 返回 (name, pattern, ok)：pattern 非空时为解构位（调用方大声拒），
// ok=false 时形状不支持（多声明/缺声明/非声明位）。
func saForBindingName(init *ast.Node) (string, *ast.Node, bool) {
	if init == nil || init.Kind != ast.KindVariableDeclarationList {
		return "", nil, false
	}
	decls := init.AsVariableDeclarationList().Declarations.Nodes
	if len(decls) != 1 {
		return "", nil, false
	}
	nm := decls[0].Name()
	if nm == nil {
		return "", nil, false
	}
	if nm.Kind == ast.KindIdentifier {
		return nm.Text(), nil, true
	}
	return "", nm.AsNode(), true
}

// saForArrHandle 取 for-of/for-in 被巡数组句柄（复用 step12 底座）：
// 已绑定数组直传句柄（引用语义直传）；数组字面量走 saLowerArrayLiteral
// 现场构造（alloc 16 头 + 缓冲 + 逐槽 store）；其余一律大声拒。
// 形状证据：封存 lowerForOf:2123/lowerForIn:2191 的 lowerExpr(fo.Expression)
// 位（本薄口仅支持句柄/字面量子集）。
func saForArrHandle(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int, where *ast.Node, what string) (string, bool) {
	if e != nil && e.Kind == ast.KindArrayLiteralExpression {
		h, msg := saLowerArrayLiteral(w, e, scope, pos, refusals, nextTemp)
		if msg != "" {
			ln, col := pos(where.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported %s base: %s", what, msg)})
			return "", false
		}
		return h, true
	}
	if base, ok := saArrBase(scope, e); ok {
		return base, true
	}
	ln, col := pos(where.Pos())
	*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("%s base must be bound array", what)})
	return "", false
}

// saLowerForOf lowering for-of（形状证据：封存 lowerForOf:2120-2184 索引巡回
// 原样镜像：idx=0 + 头+8 len + top:slt/br + body:base/mul/add/i32读回 +
// 绑定 + 体 + 增量 + jmp top + end；continue→top（非增量前，与 for 的
// cont 分支不对称，文档化原样）；break 落 end。
// sci for 宏（control.sal FOR_INIT/FOR_CHECK/FOR_NEXT、core/loop.sa
// ARRAY_FOR_EACH）为计数/Slice 形，与本 16 字节头 + ptr/len 形状不同，
// 故沿用 legacy br 形，不套宏——禁止原创调用惯例）。
func saLowerForOf(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	fo := s.AsForInOrOfStatement()
	if fo.AwaitModifier != nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "for-await not lowerable"})
		return false
	}
	binding, pat, ok := saForBindingName(fo.Initializer)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-of initializer (single identifier declaration only)"})
		return false
	}
	if pat != nil {
		ln, col := pos(s.Pos())
		if pat.Kind == ast.KindArrayBindingPattern {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "array patterns in for-of need nested array handles"})
		} else {
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "object patterns in for-of need static element layouts"})
		}
		return false
	}
	if _, dup := scope.types[binding]; dup {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + binding})
		return false
	}
	arrVal, ok := saForArrHandle(w, fo.Expression, scope, pos, refusals, nextTemp, s, "for-of")
	if !ok {
		return false
	}
	bodyStmts, ok := saEmbeddedBlock(fo.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-of body"})
		return false
	}
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", idx))
	topL := fmt.Sprintf("L_forof_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_forof_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_forof_end_%d", *nextLabel)
	*nextLabel++
	lenT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lenT, arrVal))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL})
	saBindPendingLabels(scope, false)
	w.Write(fmt.Sprintf("%s:\n", topL))
	cT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cT, idx, lenT))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	baseT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	offT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	elemPtr := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	elemT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 0 as ptr\n", baseT, arrVal))
	w.Write(fmt.Sprintf("  %s = mul %s, 4\n", offT, idx))
	w.Write(fmt.Sprintf("  %s = add %s, %s\n", elemPtr, baseT, offT))
	w.Write(fmt.Sprintf("  %s = load %s + 0 as i32\n", elemT, elemPtr))
	w.Write(fmt.Sprintf("  %s = %s\n", binding, elemT))
	scope.types[binding] = "i32"
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if !saArmTerminates(bodyStmts) {
		incT := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", incT, idx))
		w.Write(fmt.Sprintf("  %s = %s\n", idx, incT))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saLowerForIn lowering for-in（形状证据：封存 lowerForIn:2189-2226：
// 与 for-of 同索引巡回，唯绑定位为下标本身 `binding = idx`（+ declarePlain
// 记 i32）；对象巡回大声拒（须为绑定数组）；continue→top 原样）。
func saLowerForIn(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	fo := s.AsForInOrOfStatement()
	if fo.AwaitModifier != nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "for-await not lowerable"})
		return false
	}
	binding, pat, ok := saForBindingName(fo.Initializer)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-in initializer (single identifier declaration only)"})
		return false
	}
	if pat != nil {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "patterns in for-in need static element layouts"})
		return false
	}
	if _, dup := scope.types[binding]; dup {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate local " + binding})
		return false
	}
	arrVal, ok := saForArrHandle(w, fo.Expression, scope, pos, refusals, nextTemp, s, "for-in")
	if !ok {
		return false
	}
	bodyStmts, ok := saEmbeddedBlock(fo.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported for-in body"})
		return false
	}
	idx := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = 0\n", idx))
	topL := fmt.Sprintf("L_forin_top_%d", *nextLabel)
	*nextLabel++
	bodyL := fmt.Sprintf("L_forin_body_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_forin_end_%d", *nextLabel)
	*nextLabel++
	lenT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = load %s + 8 as u64\n", lenT, arrVal))
	scope.loops = append(scope.loops, saLoop{top: topL, cont: topL, end: endL})
	saBindPendingLabels(scope, false)
	w.Write(fmt.Sprintf("%s:\n", topL))
	cT := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = slt %s, %s\n", cT, idx, lenT))
	w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cT, bodyL, endL))
	w.Write(fmt.Sprintf("%s:\n", bodyL))
	w.Write(fmt.Sprintf("  %s = %s\n", binding, idx))
	scope.types[binding] = "i32"
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	if !saArmTerminates(bodyStmts) {
		incT := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = add %s, 1\n", incT, idx))
		w.Write(fmt.Sprintf("  %s = %s\n", idx, incT))
		w.Write(fmt.Sprintf("  jmp %s\n", topL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saLowerDoWhile lowering do-while（形状证据：封存 lowerDoWhile:2232-2263：
// 体跑一次 + `jmp cond`（体终结则省）+ 条件 `jmp loop`/`br` + end；
// continue 落条件（非顶），break 落 end）。
func saLowerDoWhile(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	ds := s.AsDoStatement()
	bodyStmts, ok := saEmbeddedBlock(ds.Statement)
	if !ok {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported do body"})
		return false
	}
	if be := saBoolSideCond(ds.Expression, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
		return false
	}
	loopL := fmt.Sprintf("L_do_%d", *nextLabel)
	*nextLabel++
	condL := fmt.Sprintf("L_do_cond_%d", *nextLabel)
	*nextLabel++
	endL := fmt.Sprintf("L_do_end_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("%s:\n", loopL))
	scope.loops = append(scope.loops, saLoop{top: loopL, cont: condL, end: endL})
	saBindPendingLabels(scope, false)
	armOK := saLowerArm(w, bodyStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
	scope.loops = scope.loops[:len(scope.loops)-1]
	if !armOK {
		return false
	}
	bodyTerm := saArmTerminates(bodyStmts)
	if !bodyTerm {
		w.Write(fmt.Sprintf("  jmp %s\n", condL))
	}
	w.Write(fmt.Sprintf("%s:\n", condL))
	condOp, msg := saCondOperand(w, ds.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported do condition: " + msg})
		return false
	}
	switch condOp {
	case "1", "true":
		w.Write(fmt.Sprintf("  jmp %s\n", loopL))
	case "0", "false":
		// 落空直达 end。
	default:
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", condOp, loopL, endL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	return true
}

// saLowerSwitch lowering switch（形状证据：封存 lowerSwitch:2599-2675 legacy 链：
// 每 case 一 test 标号（eq 比较 -> body/下一 test）+ body 标号；
// 体终结则省尾 jmp；default 落空点；break 经栈到 end（无 continue 目标）。
// 2/3 臂宏形 SWITCH_2/3 暂不采用，统一 legacy 链）。
func saLowerSwitch(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	_ = needImport
	sw := s.AsSwitchStatement()
	disc, msg := saEvalI32(w, sw.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported switch discriminant: " + msg})
		return false
	}
	clauses := sw.CaseBlock.AsCaseBlock().Clauses.Nodes
	type casePart struct {
		node *ast.Node
	}
	var parts []casePart
	var defaultNode *ast.Node
	for _, cl := range clauses {
		switch cl.Kind {
		case ast.KindCaseClause:
			parts = append(parts, casePart{node: cl})
		case ast.KindDefaultClause:
			if defaultNode != nil {
				ln, col := pos(cl.Pos())
				*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "multiple default clauses are not lowerable"})
				return false
			}
			defaultNode = cl
		default:
			ln, col := pos(cl.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("switch clause kind %d is not lowerable", int(cl.Kind))})
			return false
		}
	}
	endL := fmt.Sprintf("L_endswitch_%d", *nextLabel)
	*nextLabel++
	scope.loops = append(scope.loops, saLoop{end: endL})
	saBindPendingLabels(scope, true)
	testLabels := make([]string, len(parts)+1)
	bodyLabels := make([]string, len(parts))
	for i := range parts {
		testLabels[i] = fmt.Sprintf("L_case_t_%d", *nextLabel)
		*nextLabel++
		bodyLabels[i] = fmt.Sprintf("L_case_b_%d", *nextLabel)
		*nextLabel++
	}
	testLabels[len(parts)] = fmt.Sprintf("L_case_default_%d", *nextLabel)
	*nextLabel++
	lowered := true
	for i, p := range parts {
		w.Write(fmt.Sprintf("%s:\n", testLabels[i]))
		val, vmsg := saEvalI32(w, p.node.AsCaseOrDefaultClause().Expression, scope, pos, refusals, nextTemp)
		if vmsg != "" {
			ln, col := pos(p.node.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported case value: " + vmsg})
			lowered = false
			break
		}
		cmp := fmt.Sprintf("t_%d", *nextTemp)
		*nextTemp++
		w.Write(fmt.Sprintf("  %s = eq %s, %s\n", cmp, disc, val))
		w.Write(fmt.Sprintf("  br %s -> %s, %s\n", cmp, bodyLabels[i], testLabels[i+1]))
		w.Write(fmt.Sprintf("%s:\n", bodyLabels[i]))
		if !saLowerArm(w, p.node.AsCaseOrDefaultClause().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			lowered = false
			break
		}
		if !saArmTerminates(p.node.AsCaseOrDefaultClause().Statements.Nodes) {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		}
	}
	if !lowered {
		scope.loops = scope.loops[:len(scope.loops)-1]
		return false
	}
	w.Write(fmt.Sprintf("%s:\n", testLabels[len(parts)]))
	if defaultNode != nil {
		if !saLowerArm(w, defaultNode.AsCaseOrDefaultClause().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			scope.loops = scope.loops[:len(scope.loops)-1]
			return false
		}
		if !saArmTerminates(defaultNode.AsCaseOrDefaultClause().Statements.Nodes) {
			w.Write(fmt.Sprintf("  jmp %s\n", endL))
		}
	} else {
		w.Write(fmt.Sprintf("  jmp %s\n", endL))
	}
	w.Write(fmt.Sprintf("%s:\n", endL))
	scope.loops = scope.loops[:len(scope.loops)-1]
	return true
}

// saContainsThrow 报告子树是否含 throw（函数边界重置；证据：封存 lowerTry 前的
// containsThrow 门——SA-ASM 无异常边，throw 即 panic 不可恢复）。
func saContainsThrow(n *ast.Node) bool {
	found := false
	var walk func(x *ast.Node)
	walk = func(x *ast.Node) {
		if x == nil || found {
			return
		}
		if x.Kind == ast.KindThrowStatement {
			found = true
			return
		}
		switch x.Kind {
		case ast.KindFunctionDeclaration, ast.KindArrowFunction,
			ast.KindFunctionExpression, ast.KindClassDeclaration:
			return
		}
		x.ForEachChild(func(c *ast.Node) bool {
			walk(c)
			return false
		})
	}
	walk(n)
	return found
}

// saTryTerms 报告 try/finally 的终结性（try 终结或 finally 终结即终结；
// 缺省块视为空）。
func saTryTerms(s *ast.Node) (bool, bool) {
	ts := s.AsTryStatement()
	tryTerm := false
	if ts.TryBlock != nil {
		tryTerm = saArmTerminates(ts.TryBlock.AsBlock().Statements.Nodes)
	}
	finTerm := false
	if ts.FinallyBlock != nil {
		finTerm = saArmTerminates(ts.FinallyBlock.AsBlock().Statements.Nodes)
	}
	return tryTerm, finTerm
}

// saLowerTry lowering try（形状证据：封存 lowerTry:2271-2300：无 throw 时
// try 体直跑、catch 死代码跳过、finally 必跑；含 throw 大声拒。
// 局限（与封存一致）：try 体内 abrupt 退出（return/break）跳过后随 finally
// 代码，finally 仅直落路径精确）。
func saLowerTry(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	ts := s.AsTryStatement()
	if saContainsThrow(s) {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "throw inside try is not lowerable (catch cannot resume after panic)"})
		return false
	}
	if ts.TryBlock != nil {
		if !saLowerArm(w, ts.TryBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
	}
	// catch 永不可达（无 throw）：整块跳过，不绑定。
	if ts.FinallyBlock != nil {
		if !saLowerArm(w, ts.FinallyBlock.AsBlock().Statements.Nodes, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
	}
	return true
}

// saBindPendingLabels 将待绑标号附到刚压栈的目标上（`a: b: for` 双绑同环；
// block/switch 为 break-only；形状证据：封存 labels.go:38-58）。
// 调用点：每个 loops/breaks 压栈之后（while/for/for-of/for-in/do/switch/标号块）。
func saBindPendingLabels(scope *saScope, breakOnly bool) {
	if len(scope.pending) == 0 {
		return
	}
	if scope.labels == nil {
		scope.labels = map[string]saLoop{}
	}
	fr := scope.loops[len(scope.loops)-1]
	ld := saLoop{end: fr.end}
	if !breakOnly {
		ld.top = fr.top
		ld.cont = fr.cont
	}
	for _, nm := range scope.pending {
		scope.labels[nm] = ld
	}
	scope.pending = nil
}

// saLowerLabeled lowering `lbl: stmt`（仅 loops/switch/block；形状证据：封存
// lowerLabeled:63-98：标号随内层语句绑定、随语句消亡；串行复用合法，同名嵌套拒；
// 非三者大声拒。标号块为 break-only（continue 落此拒），体经 saLowerArm 直跑，
// end 落空点；break 到自标号不视为语句终结（落空继续），return 终结则透传）。
func saLowerLabeled(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) (bool, bool) {
	ls := s.AsLabeledStatement()
	lbl := ls.Label.Text()
	if _, dup := scope.labels[lbl]; dup {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "duplicate label " + lbl + " is not lowerable (labels share the function scope)"})
		return false, true
	}
	inner := ls.Statement
	if inner.Kind == ast.KindBlock {
		stmts, ok := saEmbeddedBlock(inner)
		if !ok {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported labeled block"})
			return false, true
		}
		endL := fmt.Sprintf("L_lbl_end_%d", *nextLabel)
		*nextLabel++
		scope.pending = append(scope.pending, lbl)
		scope.loops = append(scope.loops, saLoop{end: endL})
		saBindPendingLabels(scope, true)
		armOK := saLowerArm(w, stmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		scope.loops = scope.loops[:len(scope.loops)-1]
		delete(scope.labels, lbl)
		if !armOK {
			return false, true
		}
		w.Write(fmt.Sprintf("%s:\n", endL))
		// 终结性：仅 return/throw 透传；break/continue 落空（不终结）。
		if len(stmts) > 0 {
			if last := stmts[len(stmts)-1]; last != nil && (last.Kind == ast.KindReturnStatement || last.Kind == ast.KindThrowStatement) {
				return true, false
			}
		}
		return false, false
	}
	switch inner.Kind {
	case ast.KindForStatement, ast.KindWhileStatement, ast.KindForOfStatement,
		ast.KindForInStatement, ast.KindDoStatement, ast.KindSwitchStatement,
		ast.KindLabeledStatement:
		scope.pending = append(scope.pending, lbl)
		done, failed := saLowerStmt(w, inner, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		delete(scope.labels, lbl)
		if failed {
			return false, true
		}
		if len(scope.pending) > 0 {
			scope.pending = nil
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("labeled %d did not bind (internal invariant)", int(inner.Kind))})
			return false, true
		}
		return done, false
	default:
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("labeled %d is not lowerable (loops, switch and blocks only)", int(inner.Kind))})
		return false, true
	}
}

// saLowerBreakContinue lowering break/continue（无标号走栈顶；标号形查标号表：
// break 落 end，continue 落 cont（block/switch 无 cont 拒，未定义标号拒）；
// 栈空/表空拒。形状证据：封存 labels.go:129-158）。
func saLowerBreakContinue(w printer.EmitTextWriter, s *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal) bool {
	isBreak := s.Kind == ast.KindBreakStatement
	var label *ast.IdentifierNode
	if isBreak {
		label = s.AsBreakStatement().Label
	} else {
		label = s.AsContinueStatement().Label
	}
	if label != nil {
		nm := label.Text()
		ld, ok := scope.labels[nm]
		if !ok {
			ln, col := pos(s.Pos())
			kind := "break"
			if !isBreak {
				kind = "continue"
			}
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: kind + " to undefined label " + nm + " is not lowerable"})
			return false
		}
		if isBreak {
			w.Write(fmt.Sprintf("  jmp %s\n", ld.end))
			return true
		}
		if ld.cont == "" {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "continue to non-loop label " + nm + " is not lowerable"})
			return false
		}
		w.Write(fmt.Sprintf("  jmp %s\n", ld.cont))
		return true
	}
	if len(scope.loops) == 0 {
		ln, col := pos(s.Pos())
		kind := "break"
		if !isBreak {
			kind = "continue"
		}
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: kind + " outside loop"})
		return false
	}
	fr := scope.loops[len(scope.loops)-1]
	if isBreak {
		w.Write(fmt.Sprintf("  jmp %s\n", fr.end))
		return true
	}
	if fr.cont == "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "continue to non-loop target"})
		return false
	}
	w.Write(fmt.Sprintf("  jmp %s\n", fr.cont))
	return true
}

// saLowerIf 处理 if/else（void 与 i32 值两形，支持嵌套；嵌套走同一函数递归）。
// 条件：绑定标识符直接用（形状锁），其余 i32 操作数先求值到临时量。
func saLowerIf(w printer.EmitTextWriter, s *ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	iv := s.AsIfStatement()
	// false 恒假消死臂。
	if iv.Expression != nil && iv.Expression.Kind == ast.KindFalseKeyword {
		return true
	}
	if be := saBoolSideCond(iv.Expression, scope); be != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: fmt.Sprintf("unsupported condition kind (boolean %s in comparison)", be)})
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
	condOp, msg := saCondOperand(w, iv.Expression, scope, pos, refusals, nextTemp)
	if msg != "" {
		ln, col := pos(s.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported condition kind: " + msg})
		return false
	}
	needImport("sa_std/control.sal")
	if hasElse {
		thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
		*nextLabel++
		elseLabel := fmt.Sprintf("L_else_%d", *nextLabel)
		*nextLabel++
		w.Write(fmt.Sprintf("  EXPAND IF_ELSE %s, %s, %s\n", condOp, thenLabel, elseLabel))
		w.Write(thenLabel + ":\n")
		if !saLowerArm(w, thenStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		w.Write(elseLabel + ":\n")
		if !saLowerArm(w, elseStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
			return false
		}
		return true
	}
	thenLabel := fmt.Sprintf("L_then_%d", *nextLabel)
	*nextLabel++
	*nextLabel++ // 预留 else 槽位，与 satsgo 门禁形状对齐（1→3，4→6）
	endifLabel := fmt.Sprintf("L_endif_%d", *nextLabel)
	*nextLabel++
	w.Write(fmt.Sprintf("  EXPAND IF_TRUE %s, %s, %s\n", condOp, thenLabel, endifLabel))
	w.Write(thenLabel + ":\n")
	if !saLowerArm(w, thenStmts, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp) {
		return false
	}
	if saContainsIf(thenStmts) {
		w.Write(fmt.Sprintf("  jmp %s\n", endifLabel))
	}
	w.Write(endifLabel + ":\n")
	// 无 else 是否缺 return 由函数尾 epilogue 经 saStmtTerminates 统一判定，
	// 此处不拒（循环体/臂内同形亦然）。
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

// saStmtTerminates 判定单条语句是否终结控制流（return、break/continue，
// 或两臂皆终结的 if/else）。while/声明/赋值落空（须后继收尾）。
func saStmtTerminates(s *ast.Node) bool {
	if s == nil {
		return false
	}
	switch s.Kind {
	case ast.KindReturnStatement, ast.KindBreakStatement, ast.KindContinueStatement,
		ast.KindThrowStatement:
		return true
	case ast.KindTryStatement:
		tryTerm, finTerm := saTryTerms(s)
		return tryTerm || finTerm
	case ast.KindIfStatement:
		iv := s.AsIfStatement()
		if iv.ElseStatement == nil {
			return false
		}
		thenStmts, ok1 := saEmbeddedBlock(iv.ThenStatement)
		elseStmts, ok2 := saEmbeddedBlock(iv.ElseStatement)
		if !ok1 || !ok2 {
			return false
		}
		return saArmTerminates(thenStmts) && saArmTerminates(elseStmts)
	default:
		return false
	}
}

func saArmTerminates(stmts []*ast.Node) bool {
	if len(stmts) == 0 {
		return false
	}
	return saStmtTerminates(stmts[len(stmts)-1])
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

// saLowerArm 处理臂/循环体语句（经 saLowerStmt 与函数体共用全语句集）。
// 臂内终结后仍有语句同样大声拒（与函数体同门）。
func saLowerArm(w printer.EmitTextWriter, stmts []*ast.Node, isVoid bool, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int) bool {
	terminated := false
	for _, s := range stmts {
		if terminated {
			ln, col := pos(s.Pos())
			*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unreachable code after terminating statement"})
			return false
		}
		done, failed := saLowerStmt(w, s, isVoid, scope, pos, refusals, needImport, nextLabel, nextTemp)
		if failed {
			return false
		}
		terminated = terminated || done
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
