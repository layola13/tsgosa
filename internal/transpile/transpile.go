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
	inlineRet *saInlineRet
}

// saInlineRet 是高阶回调体 return 拦截态（封存 inlineRetState 的薄口子集）：
// 回调体内 return 存槽+jmp end，不写函数 ret；块作用域随内联消亡。
type saInlineRet struct {
	slot string
	end  string
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
	if scope.inlineRet != nil {
		// 回调体内 return：存槽+jmp end（裸 return 只跳过余下回调体）。
		// 形状证据：封存 callbackValue:5222-5249。
		if rs.Expression == nil {
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
// （bool 标识符直用，其余 0/1 操作数），number 函数走 saEvalI32；数组无返回位。
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
		// 数组成员调用与 Array.from（基为数组位；其余成员拒）。
		// 管线经 scope 内取（addImport/nextLabel 已随 scope 走，无需改签名）。
		if pa.Name() != nil {
			m := pa.Name().Text()
			if saIsArrMethod(m) && saIsArrValue(pa.Expression, scope) {
				op, kind, msg := saLowerArrCall(w, ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				_ = kind
				return op, false, ""
			}
			if m == "from" && pa.Expression != nil && pa.Expression.Kind == ast.KindIdentifier && pa.Expression.Text() == "Array" {
				op, kind, msg := saLowerArrCall(w, ce, scope, pos, refusals, scope.addImport, scope.nextLabel, nextTemp)
				if msg != "" {
					return "", false, msg
				}
				_ = kind
				return op, false, ""
			}
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
			if len(sig.paramKinds) == len(ce.Arguments.Nodes) && sig.paramKinds[i] == "arr" {
				h, msg := saArrValueOf(w, a, scope, pos, refusals, nextTemp)
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
			ast.KindPercentToken:                           "srem",
			ast.KindLessThanLessThanToken:                  "shl",
			ast.KindGreaterThanGreaterThanToken:            "ashr",
			ast.KindGreaterThanGreaterThanGreaterThanToken: "lshr",
			ast.KindAmpersandToken:                         "and", ast.KindBarToken: "or",
			ast.KindCaretToken:        "xor",
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
		if k, ok := saArrCallRet(e.AsCallExpression(), scope); ok && k != "i32" {
			return "", "array value in i32 expression"
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
	if vd.Initializer.Kind == ast.KindCallExpression {
		// 数组/串返回调用按返回种建种（slice/concat/map、join/String() 等）。
		if k, ok := saArrCallRet(vd.Initializer.AsCallExpression(), scope); ok && k == "arr" {
			return saLowerArrDecl(w, d, vd, name, isConst, scope, pos, refusals, nextTemp)
		}
		if saCallIsStr(vd.Initializer.AsCallExpression(), scope) && !saStrCallIsI32(vd.Initializer.AsCallExpression(), scope) {
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
		// 数组句柄拷贝（绑定直传；数组返回调用亦直传）。
		if src, msg := saArrValueOf(w, be.Right, scope, pos, refusals, nextTemp); msg == "" {
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
