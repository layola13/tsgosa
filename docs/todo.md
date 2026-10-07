# ts→sa 后端迁移评估：三批探针 + 两批 Demo（2026-10-07）

> 来源：对 `codex-react-ui` 后端（`apps/server/src`，112 个 TS 文件，Bun+Hono+SQLite/WS/PTY）的可转译性评估。
> 方法：按后端真实用量统计构造频率 → 单构造探针 → `tsgo --sa` 转译 + `sa check` 双门禁 → bun 镜算。
> 环境：Ubuntu 24.04 / Go 1.27.1（`/usr/local/go`）/ Zig 0.14.1（`/opt`）/ `tsgo`（`/tmp/tsgo`，41M，由本仓 `go build ./cmd/tsgo`）/ `sci` LLVM 版（`zig build -Dllvm=true`，默认库名 `LLVM-14`；显式 `-Dllvm-lib-name=LLVM-C` 会失败）。
> 探针位置（易失，见 §5）：`/tmp/opencode/ts-sa-probe/{cases,bcases,b2cases}` + 生成器 `gen_probes.py/gen_backend.py/gen_batch2.py`；Demo：`/tmp/opencode/ts-sa-demo/`、`/tmp/opencode/ts-sa-demo2/`。

## 第一批：53 随机探针（`cases/`）

34 通过（含：串数组/嵌套数组/Map/Set/`for-in`/`Array.isArray`/`throw` 裸抛/`try-catch` 无值抛/默认参数/rest/spread-call/重载/`async` 定义/`switch` 串与 fallthrough/float/bitwise/`static/#私有/getter/abstract`/`namespace`/`??`(i32)/正则 `test`/`typeof`/联合/泛型/`as`/`keyof`/`any`）。

19 拒收（拒收原文逐条抄自 `subset-report.txt`）：

| 探针 | 拒收原文 |
|---|---|
| F01 嵌套函数（闭包捕获） | `2:14: statement KindFunctionDeclaration is not in the SA-lowerable subset` |
| F06 `yield` | `1:18: unsupported expression statement: expression KindYieldExpression is not in the SA-lowerable subset` |
| F09 `new Promise` | `2:10: unknown class Promise` |
| F11 顶层 IIFE | `1:1: unsupported call statement: only direct function calls lowerable` |
| K06 `with` 语句 | `2:13: with statements are not lowerable (dynamic scope)` |
| N05 `10n` | `2:10: unsupported initializer: expression KindBigIntLiteral is not in the SA-lowerable subset` |
| T05 串枚举 | `2:23: string enum member Color.R is not lowerable (only all-integer enums fold ordinals)` |
| T07 值 import | `2:23: unknown variable depv` + `warning: local module import ./dep recorded; single-file lowering continues (multi-file link is Phase 2)` |
| V01 对象字面量（无 interface） | `2:10: object literal matches no recorded interface layout (declare the interface first)` |
| V02 `?.`（内联注解） | `3:10: unsupported initializer: optional member access not lowerable` |
| V04 `p!.x` | `2:9: only .length member access lowerable` |
| V05 `JSON.stringify` | `1:23: unsupported call statement: only direct function calls lowerable` |
| V06 `JSON.parse` | `2:10: unsupported initializer: only direct function calls lowerable` |
| V10 `Object.keys(o).length` | `2:10: unsupported initializer: .length base must be bound array or string` |
| V11  computed key | `3:10: computed property names must be literals (dynamic keys have no static layout)` |
| V12 对象 getter | `2:10: object literal property is not lowerable (methods refused)` |
| V13 `delete` | `2:10: object literal matches no recorded interface layout (declare the interface first)` |
| V14 `in` | `2:7: unsupported condition kind: in operator needs a literal key and a known-layout object` |
| V15 `instanceof` | `3:7: unsupported condition kind: binary operator KindInstanceOfKeyword not in subset` |

## 第二批：21 后端专属探针（`bcases/`，全部挂后端真实用量）

后端用量基线：`?.` 2238×、`??` 3391×、`async/await` 2457×、`as` 2499×、`throw/catch` 1156×、`...` 1586×、`for-of/in` 820×、`JSON` 591×、`Map/Set` 253×、`interface` 216×、`import type` 126×、`extends` 33×、反引号模板 10370×。

12 通过：B01（参数直取 `p?.x`）、B03（跨函数 `await`）、B05（基类同文件声明的 `extends`）、B06（`Map.get()!`）、B09（对象 spread）、B10（`for-of Map.keys()`）、B12（interface 先行对象字面量）、B13（`unknown as i32`）、B15（可选参数）、B16（解构）、B17（模板插值）、B21（`implements`）。

9 拒收：

| 探针（后端用量） | 拒收原文 |
|---|---|
| B02 `string\|null ??`（`??`3391×，后端多为串/对象） | `3:10: unsupported initializer: a is not a string`（注：`i32\|null ??` 可用） |
| B04/B24 try 内 `throw "str"` + catch（1156×） | `2:8: throw value type is not lowerable (catch params carry i32 only)` |
| B07 `JSON.stringify(obj)`（591×） | `3:9: only direct function calls lowerable` |
| B08 `Object.keys(o).length`（172×，有 interface 仍拒） | `3:29: .length base must be bound array or string` |
| B11 约束泛型取字段（泛型 436×） | `3:9: only .length member access lowerable` + `inst:P p in i32 expression`（泛型不携带布局；无约束 `id<T>` 可用） |
| B20 `import type`（126×） | `3:6: object annotation must name an interface`（单文件模式；程序模式见追问） |
| B22 `typeof` 收窄联合 | `2:7: unsupported condition kind: typeof v is not statically known` |
| B23 `f?.()`（48×） | `3:10: unsupported initializer: f is not a function` |

追问：B04b（`throw 42` + catch + `console.log(e)`）**通过**（65 指令 check ok）——catch 只运 i32；B26（`tsgo build` 程序模式双文件值 import 链接）**通过**（`wrote linked SA workspace, 2 files`，合并 `main.sa` check ok）——多文件是通路，单文件不是。

## 第三批：19 探针 + 4 追问（`b2cases/`，`toParseInventory`/`buildForwardModelIds` 方向）

7 通过：D03（串数组 push）、D04（`indexOf` 手工多段 split——`splitForwardModelPositional` 可搬）、D07a/b（`String()/toString()`）、D08a/b（`map/filter/find/forEach` 箭头）、D10（`entries()` 解构）。

拒收（含追问全灭）：

| 探针 | 拒收原文 |
|---|---|
| D01/D01b interface 数组 `P[]` 取下标/读字段/`.length`（`toParseInventory` 核心，绑定 `let e = inv[i]` 本身即拒） | `unsupported assignment rhs: .length base must be bound array or string` —— **interface 后门对数组元素无效** |
| D02/D02b `inv[0].providerId` | `only .length member access lowerable` |
| D05/V15 `instanceof`（234×，含自定义类） | `binary operator KindInstanceOfKeyword not in subset` |
| D06 `process.env`（319×） | `only .length member access lowerable` |
| D09b `setTimeout` | `setTimeout needs an event loop with callback dispatch (async timers are Phase 2)` |
| D11/D11b/D11c `p?.x ?? d` 及一切非参数直取 `?.`（2238×） | `optional member access not lowerable` / `inst:P p in i32 expression` —— `?.` 仅 B01 精确形状（函数参数直取）可用；局部变量/空字面量初始化/参数拷贝后全拒 |
| D13 `Promise.all`（47×） | `only direct function calls lowerable` |
| D14 `TextEncoder`（86× 家族） | `unknown class TextEncoder` |
| D15 `class X extends Error` | `class MyErr extends unknown base Error (declare the base class first)` —— bridges 继承 Node `EventEmitter` 落同一坑，需先给 Node 基类建布局声明 |

转译过但 `sa check` 挂（lowering 归属 bug，非语义禁区，修 transpile 可解）：D07c `Number("7")` → `error[MemoryLeak]`；D09a `Date.now()` → `error[MemoryLeak]`；D12 `console.error`（143×）→ `error[ImportResolutionFailed]`（要 node 插件）。

## 第四批：一批 Demo（端到端绿）

- `/tmp/opencode/ts-sa-demo/main.ts`（四则 + `while` + 递归 `fib` + 数组）：转译零拒收 → `sa check`（231 指令 ok）→ LLVM 原生编译运行 `14/45/55/6`，预期一致。
- `/tmp/opencode/ts-sa-demo2/main.ts`（`forwardModel.ts` 的 `{cli}-{provider}-{group}-{model}` 路由解析改写版，含 `codex-team` 含横杠分组最长匹配）：直转译原文件先得 7 条拒收（类继承/顶层数组/成员访问/`for-of` Map 派生等）；改写后（错误码代 `throw`、前置校验 + early return、字符串 single-assignment 三元一次绑定）零拒收 → `sa check`（2295 指令 ok）→ 6 case 全对 → bun 镜算逐字节一致 `MIRROR_MATCH`。
- 附带写法铁律（实证）：分支内复写存活串寄存器必报 `RegisterRedefinition`（concat 形也救不了，必须 single-assignment）；`slice/charCodeAt` 越界 SA 读零，长度守卫不可省；`sa run` 解释器跑不了 `console.log(数字)`（缺 `sa_fmt_i64_into`，要原生链接）；`tsgo --sa` 输出文件名跟输入走。

## 结论：后端迁移分水岭

可搬：纯计算 + 字符串 + 受限控制流 + `interface` 先行单对象 + 程序模式多文件。不可搬：对象数组（`P[]`）、`JSON`（591×）、对象反射（172×）、`Error` 体系、`?.` 组合形态（2238× 绝大部分）、`string ??`、跨文件类型（单文件模式）、Node 运行时（`process.env`/定时器/`Promise.all`/`TextEncoder`）。`index.ts` 级别不行，`forwardModel.ts` 的解析子集行。

## 第四批：第二批 Demo——表驱动最长匹配 + 笛卡尔展开（`/tmp/opencode/ts-sa-demo2/main.ts`）

- 在第一批 Demo（`parseRoute` 硬编码分支）上追加 `parseFull`（providers/groups 扁平表 + offset 范围，循环 longest-match）与 `buildIds`（双重循环笛卡尔打印 `codex-{p}-{g}-m`），对应 `toParseInventory` + `splitForwardModelPositional` + `buildForwardModelIds`（嵌套库存归一化为扁平表 + offset，绕开 `P[]` 不可索引禁区）。
- 转译零拒收 → `sa check`（3287 指令 ok）→ 原生运行 10 case + 4 行展开全对 → bun 镜算逐字节一致 `MIRROR_MATCH`。
- **新发现（soundness bug，高优，已修 R3-33b）**：`let c = a[1]` 绑定串数组元素到局部变量，转译 + 校验全绿，但运行时 `console.log(c)` 打印出裸指针整数（实测 `736707264`，bun 为 `official`）；而直接下标形态（`a[1]` 作实参/`console.log(a[1])`/拼串/`groups[gi]` 循环实参）全部正确。根因：无注解声明种分发把串元判给 i32 位（`saEvalI32`），`sext` 走整数打印；修复：`saLowerInferredDecl` 加串元分支（`saIsStrExpr` 判形→`saEvalStr` 求值→记 `str` 种，与直接形同形；`demos/367_strelem`，`sa check` 168 指令 ok）。纪律更新：绑定串数组元素已可用（i32 数组绑定行为不变）。另：`if (c == "lit")` 对绑定串已通（R3-33b 记种后条件位自动通，`!=`/while/拼串全形已验）。
- 结论：表驱动库存解析可搬（扁平表 + 直接下标 + longest-match 循环），`P[]` 对象数组仍不可。

## 循环迭代（2026-10-07 13:00 UTC）：同步 → 重编 → 重跑矩阵

- 拉取：`tsgosa` 已是最新；`sci` 拉到 `2bb771ea`（`sa_std regex group_start offset`，regex.sai + runtime，编译器本体未变）。
- 重编：`tsgo` 重编（41M）；`sci` `zig build -Dllvm=true`：`libsa_std.a/.so` 已新鲜（12:56），`sa` 主程序仅动态链接系统库（LLVM/libc），编译器未变故未重链——经验：**`sa` 二进制 mtime 不动不代表没同步，要看 `zig-out/lib` 与本次 commit 是否含编译器改动**。后台 `nohup` 构建无日志即不可信，改前台 `--summary all` 确认。
- 重跑 98 探针转译相：唯一真实变化 **V05 `JSON.stringify(42)` 由拒收变通过**（远端 R3-31/32 新增 `sa_std/encoding/json.sai` 投影：`sa_json_writer_*`）。
- **V05 运行时验证挂**：转译 + check 全绿，但原生运行打印随机堆地址（`599151392/984851232/...`，三次三样；bun 为 `42`）——第二例静默误编译（第一例是串数组元素绑定）。`JSON`（后端 591×）仍不可用，之前“拒收”结论升级为“转译放行但结果错”，更危险。
- Demo2 用新工具链重验：`DEMO2_MATCH`（3287 指令，10 case + 笛卡尔，bun 一致）。
- 待办更新：第 2 条追加 V05 writer 输出归属/打印修复；第 4 条 `toParseInventory` longest-match 已由第四批 Demo 覆盖，剩余方向为外部库 FFI 清单。

## 循环迭代 2（2026-10-07 13:30 UTC）：远端修坑验证

- 远端 R3-33b（串数组元素绑定）/R3-33c（绑定串比较）/R3-33（Date f64 归属）正是本评估报的三处，已同步 + 重编 `tsgo` 验证：
- `367_strelem`（`const c = a[1]` + `c == "x"`）：转译/check/原生运行全过，与 `expected.stdout` 一致 —— 第一例静默误编译已修，本评估的“永不绑定”纪律可降级为“以 367 形状为准”（`const` 绑定 + 直接比较）。
- `366_maparr`（`Map<string, string[]>`）：4 行对 3 行，残留 `sm.get("k")[1]` 直接下标打印指针（`925300512` vs `yy`）；而先绑定 `sv = sm.get("k")` 再 `sv[0]` 则正确 —— 与上一轮形态互补（上一轮是绑定错、直接对，这次是直接错、绑定对），说明 map-value 数组的元素加载路径仍有分支未对齐，需再报。
- D09a `console.log(Date.now())`：当前 sci 下仍 `MemoryLeak`，R3-33 修的是另一条 Date 路径，本形态未覆盖，继续挂起。
- 本轮 micro2b（`let` 版绑定 + 比较）：`official/1` 全对，与 367 一致。

## 循环迭代 3（2026-10-07 14:00 UTC）：366 残留最小化

- 366 残留 `sm.get("k")[1]` 不是 Map 特有：`get()[1]`（函数返回串数组直接下标）同样静默误编译（指针 vs `yy`）。位置矩阵（串数组元素）：直接 `log(a[i])`/作实参/拼串/`const c = a[i]` + `c == "lit"` 均已修好（367/micro2b 全对）；`call()[i]` 误编译；`return a[i]` 拒收（`not a string expression`）；`m.set(k, "字面量")` 拒收（`KindStringLiteral is not lowerable`，Map 值只收数组）。
- 含义：串数组元素加载只对“具名局部数组基址”对齐，右值基址（调用/Map.get 链）仍错。修 367 时只对齐了具名路径。
- D09a `Date.now()` 仍 `MemoryLeak`（R3-33 未覆盖本形态）；V05 stringify 误编译仍挂起。

## 待办（给后续轮）

1. 归档探针：`/tmp/opencode/ts-sa-probe` 易失，建议收进本仓 `demos/`（fixture 例外）或另仓；生成器即文档。
2. 修 lowering 归属 bug：`Number()/Date.now()` i32 结果未登记释放（`MemoryLeak`），`console.error` 缺 node 插件 import（`ImportResolutionFailed`）。
3. node 插件本环境 wiring 未通：`203_node_path` 跑分 `PackageNotResolved`；`sa plugin install` 需 TTY 确认特权插件，且曾因硬编码 `/content/sci` 路径失败（已用软链绕过，`http-client` 已装，`node` 本体报 `InvalidPluginPermission`）。`node:path` 前缀形可投影出 `@import "node.sai"`，转译侧通、链接侧待通。
4. 下一步探针方向（二选一）：`Hono` 路由/`better-sqlite3` 等外部库调用形状（预期全拒，产出 FFI 清单）；或先修第 2 条再探 `toParseInventory` 库存 longest-match。
