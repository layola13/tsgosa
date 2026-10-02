# tsgosa Agents.md — ts→sa 底座复用铁律（用户立项，2026-10-02）

> 目标：在 `tsgosa`（typescript-go 浅克隆）内做 `ts → sa`，输出 `.sai` 给 `sci/sa` 消费。
> 知识底座：`sala`（SA/SLA 帮助文档站，只读知识库，不参与工具链）、`sci`（Zig 核心编译器 `.sa→Flattener→Referee→LLVM/WASM` + `sa_std` + `sa` CLI，后端消费者）、`satsgo`（已有 `tsgo-sa` step1/step2 转换与 286 扫测门禁，移植源）。
> 验收标准：将 `satsgo` 已完成的转换（`satsgo/cmd/tsgo-sa/main_test.go` step1 void/number/boolean/return + step2 IF_ELSE/IF_TRUE/SELECT，拒则大声）全部移植到本工程，且全量单测通过。

## 1. 五条铁律（违反即回滚）

1. **必须在已有架构做**：只改 `tsgosa` 已有文件（`internal/compiler/emitter.go` 管线、`internal/transformers/*` 中转、`internal/printer/*` 落字、`internal/transpile/transpile.go` 单文件口、`cmd/tsgo/main.go` 薄 CLI）。不另起平行 lowering 体系。
2. **必须替换已有生成的 JS 逻辑**：SA 发射位即 JS 管线末端（`emitter.go:getScriptTransformers` 跑同一变换链 → 末端 printer 由 JS 文本换成 SA 文本 + `sci` 宏）。`saemit.go` 式 11k 行独立发射、绕过 `transformers/printer` 的做法已封存（见 `satsgo/_archive_saemit_20261002/README.md`），不得重犯。之前 0 复用已有文件、浪费底座，本次必须用底座代码，直接替换掉已有的 JS 逻辑。
3. **禁止新建文件**：`internal/saemit/*`、`cmd/tsgo-sa/*`、任何新 `.go`/`.py` 一律不建。确需新模块时，先问 JEV，获批后拆分已有超长文件（单文件超约 500 行或职责超两项才拆，且拆分即独立 commit + 单测）。
4. **禁止原创逻辑**：语义以三处上游为准——`binder`（控制流/绑定）→ `checker`（类型/窄化）→ `printer`（JS 发射形状即语义基准），注释必须写明 `internal/` 文件行；`sci` 侧以 `sala/content/03_sa_asm` + `sci/sa_std` 契约 + `sa_plugin_ts` 279 demos 为准；`@import` 只指向 `sci/sa_std`（或 node/deno/bun 插件 `.sai` 白名单），`StdProjectionTable` 式逐字核对，无符号一律定位拒绝，永不静默错码、永不自造 helper/调用惯例。
5. **禁止在 MAIN.GO 里堆逻辑**：`cmd/tsgo/main.go` 只留 `runMain` 薄分发（flag 解析 + 调已有 internal 入口）。分析（`compiler.Program`）、中转（`tstransforms TypeEraser/RuntimeSyntax + jsxtransforms`）、发射（`printer.EmitTextWriter` 后端 + `sci` 宏）一律进已有 `internal/*` 文件；`main.go` 新增超 20 行即违规。

## 2. 管线（与 satsgo/SA_TS_TO_SA_DESIGN.md 一致，路径已换成本仓）

```
.ts --[parser+binder+checker: compiler.Program]--> 分析
    --[middle: tstransforms TypeEraser + RuntimeSyntaxTransformer (+ JSX)]--> 中转（原生 JS 形）
    --[emit: printer.EmitTextWriter 后端 + sci 宏]--> .sai + subset-report.txt
    --[sci sa build]--> .exe/.wasm
```

- 对齐 `transpile.TranspileModule`（`IsolatedModules/NoResolve/NoLib`）单文件口径。
- 变换采用 = 独立 commit + 零回退门禁，不批量切；`transformForSA` 影子模式（失败回退原文件，见 `satsgo/_archive_saemit_20261002/saemit/reuse_pipeline.go` 思想）。

## 3. 当前移植清单（satsgo → tsgosa，不新建文件）

- step1：顶层 `function f(): void {}`/`return;` → `@f(): ret`；`(): number/boolean {return lit;}` → `@f() -> i32: ret lit`；值空体缺 return、大声拒；非函数、void 回值、联合注解、string 返回一律拒（`main_test.go:22-126`）。
- step2：`if/else` → `EXPAND IF_ELSE/IF_TRUE` + `@import "sa_std/control.sal"`；`false` 恒假消死臂；`return c?a:b`（i32 字面臂）→ `EXPAND SELECT`（`main_test.go:128-284`）。
- 报告：`subset-report.txt` 逐行 `file:line:col: msg`，有拒绝则 exit 1。

## 4. 工具纪律

- 读/查/改优先 `sa_vm_run`（read_file/read_lines/count_lines/grep_search/edit_file），禁终端 `cat/grep/ls/head/tail` 看代码；`git/diagnostics` 走宿主。
- 有问题问 JEV（`jev_diagnose/jev_choose`），每步完问 `jev_next` 找下个任务，全程由 JEV 控制；禁停下来总结、禁 `askquestions` 式反问。
