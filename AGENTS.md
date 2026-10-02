# tsgosa Agents.md — ts→sa 底座复用铁律（用户立项，2026-10-02）

> 目标：在 `tsgosa`（typescript-go 浅克隆）内做 `ts → sa`，输出 `.sai` 给 `sci/sa` 消费。
> 知识底座：`sala`（SA/SLA 帮助文档站，只读知识库，不参与工具链）、`sci`（Zig 核心编译器 `.sa→Flattener→Referee→LLVM/WASM` + `sa_std` + `sa` CLI，后端消费者）、`satsgo`（已有 `tsgo-sa` step1/step2 转换与 286 扫测门禁，移植源）。
> 验收标准：
> 1. `satsgo` step1/step2（`satsgo/cmd/tsgo-sa/main_test.go`）已移植为 `TranspileSA`（本仓单文件薄口）。
> 2. 用户令（2026-10-02，重申）：`satsgo` saemit **全部代码**必须重构进本仓，且**禁止新建文件**——所有 SA 逻辑只允许以**编辑已有文件**方式落入（`internal/transpile/transpile.go` 的 SA 区、`internal/compiler/emitter.go`、`internal/printer/*`、`cmd/tsgo/main.go` 薄分发），把已有 JS 生成逻辑替换为 SA；封存树只做逻辑移植源，不整体搬迁。
>
> 禁止以“整体搬迁封存树”（`internal/saemit/*`、`cmd/tsgo-sa/*` 新文件）替代重构——该方案经用户否决。

## 1. 五条铁律（违反即回滚）

1. **必须在已有架构做**：只改 `tsgosa` 已有文件（`internal/compiler/emitter.go` 管线、`internal/transformers/*` 中转、`internal/printer/*` 落字、`internal/transpile/transpile.go` 单文件口、`cmd/tsgo/main.go` 薄 CLI）。不另起平行 lowering 体系。
2. **必须替换已有生成的 JS 逻辑**：SA 发射位即 JS 管线末端（`emitter.go:getScriptTransformers` 跑同一变换链 → 末端 printer 由 JS 文本换成 SA 文本 + `sci` 宏）。`saemit.go` 式 11k 行独立发射、绕过 `transformers/printer` 的做法已封存（见 `satsgo/_archive_saemit_20261002/README.md`），不得重犯。之前 0 复用已有文件、浪费底座，本次必须用底座代码，直接替换掉已有的 JS 逻辑。
3. **禁止新建文件**：`internal/saemit/*`、`cmd/tsgo-sa/*`、任何新 `.go`/`.py` 一律不建（整体搬迁封存树亦属新建，同样禁止）。satsgo saemit 全部逻辑只允许以编辑已有文件方式并入；确需拆分超长文件（单文件超约 500 行或职责超两项）时，先问 JEV，获批后拆分，且拆分即独立 commit + 单测。
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
- step3-11：i32/bool 表达式核（算术/比较/逻辑/`!`/`++/--`/三元值形/调用传参）、`while`/`for`（legacy br 形，cont 落增量前）/`do-while`/`switch`（legacy 链）/`try-finally`（无 throw 时直跑，含 throw 拒）+ 不可达门。
- step12：i32 数组（字面量 alloc 16 头 + 缓冲逐槽 store、越界归零下标读 join、元素存、`.length` 头+8、`let a=b` 句柄拷贝/传参直传；spread/非 i32 元/缺 init/数组条件·返回位一律拒）。
- step13：`for-of`/`for-in` 索引巡回（绑定数组直传/字面量现场构造，`idx=0`+头+8 len+`slt/br`+`base/mul/add/i32` 读回；for-in 绑下标；`continue→top` 原样不对称；`await`/多声明/pattern/非数组基拒；sci FOR/ARRAY_FOR_EACH 宏形不用，沿 legacy）。
- step14：标号语句（loops/switch/block 绑定，标号 break/continue 查表；块为 break-only；串行复用合法、同名嵌套/未定义/块上 continue/标非常规语句拒；`labels.go:1-158`）。
- step15：`throw` → `panic(2501)`（终结；try 内含 throw 仍拒）+ 空语句 no-op（`saemit.go:153,882-887`）。
- step16：数组解构声明（`const [a,b] = p|[...]` 逐元越界归零 join 绑 i32；空穴跳过；rest/嵌套/对象布局/非数组源/函数值/缺 init/重复名拒；`lowerDestructuringDecl:5414-5461/destructureArray:5309-5333/bindPatternName:5513-5524`）。
- step17：无注解局部推断（数组字面量/句柄走 arr 通道，`true/false`/bool 句柄走 bool，其余 i32 求值；缺 init 绑 i32 零值，const 缺 init/函数值拒；`lowerVarDeclList:1415-1433` 缺省 i32 + 按初值绑定）。
- step18：无注解参数缺省 i32（`lowerFunction:946 ptype := tI32`）。
- step19：无注解返回缺省 void（`lowerFunction:919-923`；有值返回仍拒）。
- 报告：`subset-report.txt` 逐行 `file:line:col: msg`，有拒绝则 exit 1。

## 4. 工具纪律

- 读/查/改优先 `sa_vm_run`（read_file/read_lines/count_lines/grep_search/edit_file），禁终端 `cat/grep/ls/head/tail` 看代码；`git/diagnostics` 走宿主。
- 有问题问 JEV（`jev_diagnose/jev_choose`），每步完问 `jev_next` 找下个任务，全程由 JEV 控制；禁停下来总结、禁 `askquestions` 式反问。
