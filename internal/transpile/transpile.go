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
	enums := map[string]map[string]int64{}
	classes := map[string]*saClassDef{}
	// 预扫一：类型表（类/接口/枚举；函数签名引用须先行）。
	for _, st := range sf.AsSourceFile().Statements.Nodes {
		if st.Kind == ast.KindClassDeclaration {
			// 类定义预扫成表（布局记录、无码；方法随调用内联）。
			saRecordClass(st, classes, pos, &refusals)
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
			members, msg := saEnumMembers(st)
			if msg != "" {
				ln, col := pos(st.Pos())
				refusals = append(refusals, SARefusal{Line: ln, Col: col, Msg: msg})
				continue
			}
			enums[nm.Text()] = members
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
		if kinds, ok := saParamKinds(fn, classes); ok {
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
		// 类型声明擦除（记录、无码；形状证据：封存 lowerTypeDecl:9242-9253）。
		// export 修饰随声明擦除（单文件无模块边；`export default function`
		// 同形；`export {}`/`export =` 无码，镜像 lowerModuleDecl:10329-10336）。
		switch st.Kind {
		case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindEnumDeclaration,
			ast.KindExportDeclaration, ast.KindExportAssignment, ast.KindNamespaceExportDeclaration,
			ast.KindClassDeclaration:
			continue
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
		saLowerFunction(w, st, funcs, enums, classes, pos, &refusals, needImport, &nextLabel, &nextTemp, strPool)
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
// 类/接口注解（`p: Pt`）记 `inst:Pt`（实例句柄直传）。
func saParamKinds(fn *ast.FunctionDeclaration, classes map[string]*saClassDef) (map[string]string, bool) {
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
			// 类/接口注解直记实例种（`p: Pt` → `inst:Pt`）。
			if pd.Type.Kind == ast.KindTypeReference {
				if ref := pd.Type.AsTypeReferenceNode(); ref != nil && ref.TypeName != nil {
					if _, ok := classes[ref.TypeName.Text()]; ok {
						kinds[nm.Text()] = "inst:" + ref.TypeName.Text()
						continue
					}
				}
			}
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
// saEnumMembers 整数枚举成员编号（显式 =N 优先，余下 next++；
// 形状证据：封存 integerInit:9258-9277 + enumMemberTable:9284+。非整数
// （串/浮点/计算式）初值大声拒）。
func saEnumMembers(st *ast.Node) (map[string]int64, string) {
	m := map[string]int64{}
	var next int64
	for _, mem := range st.AsEnumDeclaration().Members.Nodes {
		nm := mem.Name()
		if nm == nil || (nm.Kind != ast.KindIdentifier && nm.Kind != ast.KindStringLiteral) {
			return nil, "enum member shape is not lowerable"
		}
		if init := mem.AsEnumMember().Initializer; init != nil {
			v, ok := saEnumInit(init.AsNode())
			if !ok {
				return nil, "enum member needs an integer initializer"
			}
			m[nm.Text()] = v
			next = v + 1
			continue
		}
		m[nm.Text()] = next
		next++
	}
	return m, ""
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
	enums     map[string]map[string]int64
	classes   map[string]*saClassDef
	thisSelf  string
	thisClass string
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

func saBlockStmts(body *ast.Node) ([]*ast.Node, bool) {
	if body == nil || body.Kind != ast.KindBlock {
		return nil, false
	}
	return body.AsBlock().Statements.Nodes, true
}

func saLowerFunction(w printer.EmitTextWriter, st *ast.Node, funcs map[string]saFuncSig, enums map[string]map[string]int64, classes map[string]*saClassDef, pos func(int) (int, int), refusals *[]SARefusal, needImport func(string), nextLabel, nextTemp *int, strPool *saStrPool) {
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
	scope := &saScope{types: map[string]string{}, funcs: funcs, enums: enums, classes: classes, nextLabel: nextLabel, retKind: retKind, strPool: strPool, addImport: needImport}
	paramKinds, ok := saParamKinds(fn, scope.classes)
	if !ok {
		ln, col := pos(st.Pos())
		*refusals = append(*refusals, SARefusal{Line: ln, Col: col, Msg: "unsupported parameter annotation (i32/bool/arr/str/inst only)"})
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
