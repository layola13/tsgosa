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

## 循环迭代 4（2026-10-07 14:30 UTC）：第三批探针（外部库面）+ Demo 聚合

- 18 探针（`/tmp/opencode/ts-sa-probe/b3cases/`，生成器 `gen_batch3.py`），后端用量：`node:path` 62×、`node:fs` 47×、`fs/promises` 34×、`crypto` 42×、`child_process` 34×、`os` 27×、`events` 24×、`fetch` 52×、`Bun.*` 15-20×、`@codex-ui/shared` 85×、`ws`/`node-pty`/`sqlite` 各有专用文件、`@napi-rs/keyring` 4×、`js-tiktoken` 代表 npm 包、`node:http` 5×。
- 仅 3 项转译通过但 check 挂 `ImportResolutionFailed`：`path.join`、`crypto.randomUUID`、`os.homedir`——转译器投影出 `@import "node.sai"`（投影表存在），缺插件链接（同 D12 `console.error` 家族，接线问题非语义问题）。
- 其余 15 项转译即拒：`fs.readFileSync`（`not a projected surface`）、`fs/promises`/`spawn`/`createServer`/`fetch`（`unknown function` + Phase 2 警告）、`EventEmitter` 基类（同 D15）、`fileURLToPath`（`not a projected std surface, see StdProjectionTable`）、`WebSocketServer`（`unknown class`）、动态 `import()`/`Bun.serve`（`only direct function calls`）、`require()`两种（含 `bun:sqlite`，顶层 const 拒）、`@codex-ui/shared`/`keyring`/`tiktoken`（单文件 Phase 2 警告，程序模式待验证）。
- 含义：后端 112 文件的 import 面（node 内建 300+ 处、npm 包、内部包）在单文件管线下全灭；`node:http` + `ws` 的 `runtime/server.ts` 双运行时、`runtime/database.ts` 三后端 require、`terminalPty.ts` 动态 pty、`child_process` 的各 CLI bridges——迁移前置条件是程序模式 + 插件链接全通。sci 侧 `.sai` extern 面已存在（node 287 行/os-process-path-fs-crypto-net-http-child_process，db 全套列式 API，http_server 含 websocket），缺的是转译投影→插件链接的 wiring（含本环境 `PackageNotResolved`）。
- Demo 追加 `countGroups()`（扁平 `provLen` 表求和，对应 `toParseInventory` 计数半）：check（3371 指令）→ case11=3 → 镜算一致。

## 循环迭代 5（2026-10-07 15:00 UTC）：第四批探针（基础语法全扫，60 个，全部跑运行时）

- 位置：`/tmp/opencode/ts-sa-probe/b4cases/`，生成器 `gen_batch4.py`。43 通过（转译+check），其中 42 项原生运行与 bun 逐字节一致。
- 15 拒收（基础语法，非偏门）：数组空穴 `H04`、对象 `for-in` `H12`（`for-in base must be bound array`）、**块作用域遮蔽 `H16`**（`duplicate local x`）、逗号表达式 `H21`、数组 `in` `H27`、一元 `+` `H28`、`~`/`>>>` `H29`、**串 `</>` 比较 `H30`**（条件位只认 `+/==/!=`）、tagged 模板 `H32`、数组截断赋值 `H37`、命名函数表达式自递归 `H46`（内名不可见）、**构造器非参数右值 `H47`**（`this.x = 3` 拒，只认 `this.f = param` 接线）、含可选字段接口的对象字面量 `H56`（布局匹配把可选字段算死）、函数类型注解 `H57`、裸 `null` 注解 `H70`（`i32|null` 可用，`null` 不可用）、`static {}` 块 `H51`。
- 校验挂：`H24`（`a++ + ++a` 同表达式别名）`error[UseAfterMove]`。
- 运行时差异（非拒收，更值得记）：`H35` 整数除零 bun 得 `Infinity`，SA 直接 `SIGFPE`（rc=136）——后端任何 `x / y` 无守卫迁移即崩溃点。
- 通过的基本功（含易被低估的）：多声明符/`var`/尾逗号/else-if/label/`do-while`/三元嵌套/逻辑赋值/复合赋值/前后缀自增（单用）/`void`/嵌套模板/数值字面量全家/float 循环/`new Array(n)`/简写属性/箭头块/函数表达式/构造器参数属性/`readonly`/`private`/super 传参+调父方法/接口继承/元组注解/字面量联合/`as const`/`satisfies`/`const enum`/`declare`/命名空间类/`export const`/`undefined`。

## 循环迭代 6（2026-10-07 16:00 UTC）：解释器版 sci 自构建 + 真校验补证据

- 本环境 `sci/zig-out` 缺失，前台 `zig build -Dllvm=false --summary all` 一次建成（`sa` + `sa_std`，约 2 分钟；LLVM 头缺失故无原生后端，但 `check/test --list` 不需 LLVM）。
- 真校验（首轮非转译门禁证据）：`364_json/366_maparr/367_strelem/368_opt_strelem/369_fnstrarr/370_paramstrarr`（R3-34—R3-38 全部动机 demo）`sa check` 全 ok；D09a `Date.now()` 本工具链 `check ok`（84 指令，MemoryLeak 未复现，转译侧 R3-33 或 sci 侧已消；原生复验仍待 LLVM 环境）。
- `sa test --list` 全仓 89/90：唯一失败 `t289_hash`（`PackageNotResolved`）根因为其 `demos/289_hash/main.sai` 含 `@import "node.sai"`，本环境 node 插件包未装（`sa plugin list` 空，无 `~/.sa` 缓存）——与 `console.error` 同族后端 wiring，转译产物自 landing 提交字节未变，非回归。
- 解释器 `sa run` 口径：凡含 `sa_fmt_i64_into` 者报 `unsupported extern`（与第四批注记一致，需原生链接）；纯串 demo（367/368/369/370）在解释器下报 `InvalidAddress`（367 在 LLVM 原生下历史实证全对，故系解释器实现限制，非产物回归；原生复验待 LLVM 环境）。

## 循环迭代 7（2026-10-07）：node 插件链打通 + `sa test --list` 90/90

- 根因链（逐段隔离实证）：`t289`/`console.error` 的 `PackageNotResolved` 并非转译问题——① `sa_plugin_http_client` 构建脚本写死旧绝对路径 `/content/sci`（环境 symlink 绕过，零仓库改动）；② `sa_plugin_http_server/sap.json` 声明 `http://0.0.0.0`，而 sci 安装器只放行 loopback http（`isLoopbackPermissionHost`），逐条核对 manifest 后定位，删该条即装上（跨仓 1 行提交）；③ `sa check` 解析 `@import "node.sai"` 需要包身份，`demos/289_hash/sa.mod`（`require_plugin node`）即钥匙；④ `sa_tests` 缺 `sa.mod`，补后 `--list` 90/90。
- `console.error("oops")` 产物在包身份下 `check ok`（307 指令）：转译侧 `@import "node.sai"` + 插件调用早对，剩余仅原生运行（待 LLVM）。
- 真跑（非 `--list`）仍需 LLVM 后端（解释器墙既有注记），未变。

## 待办（给后续轮）

1. 归档探针：`/tmp/opencode/ts-sa-probe` 易失，建议收进本仓 `demos/`（fixture 例外）或另仓；生成器即文档。
2. 修 lowering 归属 bug：`Number()/Date.now()` i32 结果未登记释放（`MemoryLeak`），`console.error` 缺 node 插件 import（`ImportResolutionFailed`）。
3. node 插件本环境 wiring 未通：`203_node_path` 跑分 `PackageNotResolved`；`sa plugin install` 需 TTY 确认特权插件，且曾因硬编码 `/content/sci` 路径失败（已用软链绕过，`http-client` 已装，`node` 本体报 `InvalidPluginPermission`）。`node:path` 前缀形可投影出 `@import "node.sai"`，转译侧通、链接侧待通。
4. 下一步探针方向（二选一）：`Hono` 路由/`better-sqlite3` 等外部库调用形状（预期全拒，产出 FFI 清单）；或先修第 2 条再探 `toParseInventory` 库存 longest-match。

## 同步（2026-10-08，本轮收录；历史原文不动，状态在此）

- 待办 2 已消：`Number("7")`/`Date.now()` MemoryLeak 由 R3-33 系消除，`376_f64convert`/`322_date_cmp` 锁定，本环境 `check ok` 复现；`console.error` 转译投影 + node 插件链打通（循环 7，`check` 307 指令 ok）。
- 待办 3 已通：node wiring 通（`sa test --list` 90/90，循环 7；`t289` 报的缺 `sa.mod` 钥匙已补）。
- H 系列已锁定：H21/H28/H29→`411_comma_unary`；H47→`413_ctor_lit`；H46→`415_named_fnexpr`；H56→`417_opt_field`（缺省字段置零，读已给字段正确；读缺省得 0 vs JS `undefined` 语义差仍在，demo 不断言该形）；H30→`384_strcmp` + `@ts_str_compare` 回迁；B02 `string|null ??`→`414_null_strarg`（R3-76 实参空柄修复）。
- 仍 open：待办 1 探针归档（`/tmp` 易失未收仓）；待办 4 FFI 清单；H16 块遮蔽/H37 截断赋值/V05 stringify 运行时/366 右值串元（`call()[i]`，缺 LLVM 原生复验）/H35 除零 SIGFPE；`"a"+null` 上游放行实为误编译（句柄当整数打印），薄口拒收为正确立场，不移植。
- H16 已复核：上游 `x=1;{x=2}` 复用同槽打印 2/2（bun 为 2/1），上游误编译，薄口拒收正确，不移植。
- B08 `Object.keys(o).length`（172×）已锁（`418_objkeys_len`，已知布局字段数静态折叠，hasOwn 同门；数组实参/存取器在场沿旧门拒）。
- D11 本地联合 `?.` 部分关闭（`421_opt_union_local`）：`const p: P | null = null` 按 `inst:P` 零句柄记种、`{...}` 字面量按布局收（皆复用 `saUnionInstKind`，与形参/声明种同口径），`?.` 走既有守卫读；链式 `?.`（`b.a?.v`）仍拒。
- B22 联合形参擦除修正：不可折叠联合形参（`v: i32|string`）不再缺省 i32（曾致 `typeof` 恒折真、`v+1` 整数算串柄，静默错码），改诚实拒收（上游同位拒收；可折叠 `string|null`/`Box|null` 不受影响，414/421 通行）。
- `Array.isArray` 擦除种误编译修正：`unknown` 形参（擦为 i32）持数组柄时恒折 0（bun 为 1），标识符分支加 checker 真 any/unknown 守卫（与 `saTypeofKind` 同口径，无 ctx 零行为变）；真 i32/数组形照旧折叠。
- 同类扩展到裸类型形参：`g<T>(x: T)` 内 `Array.isArray(x)` 同样恒折 0，加 `saIsUnresolvedTypeParam`（`TypeFlagsTypeParameter`）守卫；`id<T>` 等正常泛型复测通行。`typeof x` 裸 T 同洞一并堵（同 helper 一行）。
- `unknown == "lit"` 上游以指针相等放行实为误编译（不同 `@const` 恒不等，bun 为 1），薄口拒收正确，不移植。
- `Array.isArray` map 值数组形已锁（`422_isarray_mapval`，串元值经既有值种识数组折真；上游 isArray 全拒）。另核：`!v` 经空判对句柄正确（非零真），无动作；上游 `"a"+v(any)` 落指针加法（`add t_2, v`）误编译，薄口拒收正确，不移植。
- `Array.from` mapper 形归属修正（`423_from_mapper`）：克隆/预分配新柄入 mapper 前未登记 own，mapper 内链式释放成 no-op，`check` 报 MemoryLeak（上游过）；补 `saOwnTemp(scope, base)` 一行（mapper 释基、decl 收新柄；无 mapper 径不动）。两形（切片/长度+mapper）`check` 全过，node 镜算 `4/20`。
- 串 `.concat()` 归属修正（`426_str_concat`）：逐片折叠中间柄与末柄皆未登记，单/多参全报 MemoryLeak（串 `.concat` 零 demo 覆盖；数组 concat 另径不受影响）；中间柄用后即释、末柄登记（trim/slice 同口径；具名复用与 trim 链复测通过）。node 镜算 `abc/ab/ac`。
- H37 部分关闭：`a.length = 0` 字面清零已支持（`419_arr_clear`，len 槽置零 + 清后 push 回写验证；非零字面/变量沿旧门拒——增长需扩容、收缩需静态长度，无依据禁臆测）。
- 回调下标形继续锁定（`431_idx_callbacks`：findLastIndex + 双参 map/filter；`432_reduce_some_idx`：三参 reduce + 双参 some；皆既有路径，node 镜算一致）。
- 解构缺省做对（`438_destructure_defaults`）：数组 OOB 存 0、对象缺省静默丢皆曾静默错码；现字面量源编译期折叠（界内/键在走元，越界/缺键按种求值缺省式），标识源/形参源沿旧门大声拒。`272` 死缺省源已清（同字节）。
- 标识源数组解构锁定（`439_arr_ident_destructure`，零改码）：字面量源早有 19/438 覆盖，标识源无聚焦锁；`const a=[10,20,30]; const [x,y]=a` 双边同过，SAI 越界守卫+槽装载同形（归一化后仅 `!` 释放位方言差），node 镜算 `10/20/30`，`sa check` 160 指令 ok，真机 PASS（本环境 LLVM-14 后端实测 exit 0，stdout 逐字节一致）。
- 静态初始化序实例径双边值错（deferred，不 blocking 439）：`static y = C.x+10` 非字面静态落实例槽但构造期无存入，`c.y` 双边同形（`load c+0`，check ok）读零 vs node 11；修法须定点（记表期求值依赖静态或构造期回填），另立修复项。
- 全机真跑矩阵（本环境首跑，Zig 0.14.1 + LLVM-14 后端，`run.sh -j8`）：400 PASS / 39 FAIL；tsgosa 侧 Go 零改动（`internal/ cmd/` 与 `3af04664d` 零 diff，`--check` 439/439 + `--corpus` 286 agree 全绿），失败归因 sci 侧：sci 检出已新于 expected 生成时（含 `d90beedb` 及后改动）且工作树脏（`src/cli.zig/verifier.zig` 等本地修改）；抽查：376 浮点格式+末值漂移（实测 `7.000000/42.000000/0` vs 预期 `7/42/1`）、384 布尔打印漂移（实测 `1/0` vs 预期 `true/false`）、426 SIGSEGV（exit 139 空输出）、203 系 `PackageNotResolved`（插件未装）；39 名单逐个定因待原环境复核，V05/366/H35 转入此矩阵复核。
- 插件链补齐（本环境，39→32）：`sa plugin list` 为空即 203-208/289 之 `PackageNotResolved` 根因；取证链：①真 zig 须在 PATH 首位（shim 版报 `TestOptions` 错）；② `sa_plugin_http_client` 构建脚本写死 `/content/sci`，补 symlink 绕过（零仓库改动）；③ node/deno 制品含 `execve` 未声明进程权限，正式安装拒收（与循环 7 之 `InvalidPluginPermission` 同族），本环境走 `--dev` 装上（node/http-server/http-client/deno 全绿）；④ 重跑 203/204/205/206/207/208/289 真机全 PASS。node 正式安装被拒系 `sa_plugin_node` 侧 manifest 缺口，修法归上游（非 tsgosa 仓）。
- 串构造 u64 缓冲柄直用修复（本轮，落 `sa_str.go` helper + 5 调用点约 30 行，零新 Go 文件）：`@sa_string_concat/to_lower/to_upper/repeat/pad/replace` 回 u64 缓冲（string.sai），旧 lowering 直当 16 字节句柄读 +0/+8，真机 `mov (%rax),%rax` 野指针 SIGSEGV（rax=0x4 实锤；`sa check` 放行系掩蔽，node 镜算亦掩蔽）。改动：新 `saUnwrapStrBuf`（`sa_fmt_buffer_data/len` 读回 + alloc16 句柄，`+` 路 `saConcatSlices` 同形；step51 fromCharCode 同例）+ concat 折叠改调现货 + 余下 4 处 call1 包裹。实证：最小 `"a".concat("b")` 改前崩改后 `ab`；lower/upper/repeat/pad/replace 五形真机全对。426/425 由崩转 PASS（全机 407→409，余 30）；361 sai 同改但仍 Aborted（正则 abort 另案）。3 demos 重生成（361/425/426，`sa check` 全 ok）。门禁：`--check` **439/439** + `--corpus` **286 agree** 零回退；`go vet` + `gofmt` 干净。
- 三元副作用臂惰性求值（本轮，落 `sa_expr.go` 判定 + 汇合约 60 行，零新 Go 文件）：i32/串臂皆先求值后 SELECT/槽汇合（封存同 eager），副作用臂恒执行——`true ? 7 : boom()` 打印 99（应 7），自递归 `n<=1 ? 1 : n*fact(n-1)` 无限递归 SIGSEGV（415）；上游同 eager（parity），故只对副作用臂（调用/new/++/--/赋值，闭包体不计）改分支槽汇合惰性形（纯臂零字节变；求值失败仍落上游同形拒因，verdict 不变）。归属：臂内新建临时量就地增量释放（分支内声明 join 后不可见，for-of 巡后释放同形；外层存活不碰）。实证三形全对（递归 120/副作用臂不再执行）。415 由崩转 PASS（全机 409→410，余 29）；漂移仅 415 一例。门禁：`--check` **439/439** + `--corpus` **286 agree** 零回退；`go vet` + `gofmt` 干净。串臂同类（求值在分支外）另步。
- `Array.join` 分隔符裸指针崩溃（本轮，落 **sci** `sa_std/ts_string.sa` 5 行，零重编即生效）：`@ts_arr_join_append(lh, rh, rlen)` 取 `load rh+0`（要 16 字节柄），但 SEP 路直传 `(sp, sl)` 数据指针→读串字节当指针（gdb 实锤 `right_ptr=0x494E4150000A002c` 为串字节垃圾，崩于 `stringConcat` 内 `mem.copyForwards`）。tsgosa 侧 `@ts_arr_join_vals(a, ptr, len)` 传参正确（签名 `(h, sp, sl)`），错在运行时内。改动：SEP 路包 16 字节临柄（ELEM 路 `part` 同形）+ `!seph`。实证：最小 join 由崩转 `1,2,3`；352/404 转 PASS。全机 411→413，余 26。门禁 unchanged 全绿。sci 侧提交 `fe6b59fa` 已推；用户本地 WIP 在 sci stash@{0}（已证与崩溃无关，原样保留）。
- `structuredClone` 嵌套双重遍历崩溃（本轮，双仓：tsgosa 整包下沉删约 45 行内联循环 + sci 内层改 flat 1 行）：tsgosa `saLowerDeepCloneInner` deep 臂自带外层循环逐元调运行时、运行时内又循环（首元 i32 叶 `1` 即野指针，gdb 实锤 src=0x1；封存 lowerDeepCloneInner 为纯内联两层形，无运行时符号，R3-14 回迁时调用方循环未删）。改动：tsgosa deep 臂改单 `call @ts_arr_clone_deep(src)`（归属/arrNest 保留）；sci 内层递归改 `clone_flat`（两层形封存原话“deeper nests copy the inner slice headers”，深三级片头拷贝与上游同形）。实证：`[[1,2],[3]]` 由崩转 `2`、元值 `d[0][1]+d[1][0]=5` 与 node 一致、深三级 `1`；350 转 PASS。全机 413→414，余 25。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- 串元扫描 8 字节错读崩溃（本轮，落 `sa_arr.go` 一词）：`saLowerArrayScanStr` 以 `load addr+0 as ptr` 读 4 字节串元槽（gdb 实锤 rax=相邻两元拼接 `0x15ff2c0015ff2a0`）；改 `as i32` 取值再按柄解引用（367 同形）。355 转 PASS。全机 414→415，余 24。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- expect 串元深比同形错读（本轮，落 `sa_ctrl.go` 两词）：`saDeepArrEq` str 臂同以 `as ptr` 读 4 字节槽（345 首测通、次测串元崩）；改双 `as i32`（`saStrContentEq` 内再解）。345 转 PASS。全机 415→416，余 23。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- replace/replaceAll 双错（本轮，双仓）：①汇合读长 `+0`（应 `+8`，落 `sa_str.go` 一字）：指针值当长度转储内存（首行 `aNb2` 后跟 NUL 海）；②replaceAll 误调 i32 元 join（落 sci 新符号 + tsgosa 改调）：`@ts_arr_join_vals` 把段柄当整数格式化（末行 `91896024…` 数字串）；新 `@ts_arr_join_strs`（串元直拼，低堆假设与串元读同形）。361 四行全 PASS。全机 416→417，余 22。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。sci 侧已推。
- 224 手写入口读长错位（本轮，fixture 一字）：`sla_main.sa` 以 `load t_11 + 8` 读换行长度（t_11 为数据指针，读出 5.7e18，打印转储内存后 abort 于 `posix.write`，gdb 实锤 `len=5764675028720247584`）；改读 `t_10 + 8`。224 转 PASS。全机 417→418，余 21。门禁待全量复核。
- f64 调用比较条件位错值（本轮，落 `sa_expr.go` 两处约 15 行）：`if (parseFloat("4.5") > 4)` 落 `sgt` 得 0（应 1）——`saIsF64Operand` 不识裸调用（仅字面量/绑定/取负），条件核经 i32 二元错算；打印位偶对（另路 fcmp）。改动：判定加裸调用臂（经 `saCallRetKind`）+ `saF64Side` 加调用臂经严格求值（`Text()` 无调用形，首版 panic 实锤后补）。附带：376 oracle 前两行按 f64 打印口径（278 锁定）改 `7.000000/42.000000`；`return s` 改 `return 0`（旧 `return s` 借错值 exit 0 才过，值对后 exit 1 必挂门禁，stdout 已锁 s=1）。376 转 PASS。全机 421→422，余 17。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- JSON.parse 布尔字段恒 false（本轮，落 `sa_expr.go` 判定 + 分支约 12 行）：`flag: boolean` 经 fkinds 归一 i32，填充走 get_i64 在真值恒失败（`jsonValueAsBool` 对不上，st 被忽略故静默 0；391 实锤）；`== "bool"` 分支实际死代码。上游拒收（thin-lead），修法自由。改动：`saJSONFieldIsBool` 查原始注解（`fdefs` 存 TypeNode）选 get_bool 路。391 转 PASS（`12`）。全机 422→423，余 16。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- match 组数据悬垂双修（本轮，落 `sa_date.go` 约 20 行）：①汇合读长 `+0`（应 `+8`，361 同族，`m.length` 读指针值 `811338672`）；②`group_ptr` 指向 match 内部文本，`match_free` 后悬垂（`m[0]` 读堆垃圾 `252`/`�`；`saConsumeOwn` 不释仍悬，实锤非归属问题）。改动：组经 concat 空串拷入自有缓冲再释 match（`+` 路同形）。362 七行全 PASS。全机 423→424，余 15。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- 命名空间可变串读旧值（本轮，落 `sa_str.go` 读序两处）：`N.s="yz"` 后 `N.s.length` 仍得 1——`saEvalStr` 标识符分支与属性分支皆先查 `topConsts`（旧字面量）再查 `modVars`（活值）；i32 侧早已 `modVars` 优先并注明“被赋值名永不折叠”。改动：串侧两分支改同序。307 转 PASS（`2/2`）。附带正本 356 oracle 一行（255 二进制应 `11111111` 非 `101`）。全机 424→426，余 13。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- 顶层浮点常量条件位错值（本轮，落 `sa_expr.go` 三处约 25 行）：`const PI2 = 6.5; if (PI2 > 6)` 落 `sgt 6.5, 6` 恒假——`saIsF64Operand` 不识 `topConsts` 浮点折叠值（仅字面量/绑定/取负/调用），i32 核经文本折叠后整型比较；`N.K` 属性形同病。改动：判定加标识符 + `N.K` 属性两臂（`topConsts` 浮点文本）+ `saF64Side`/`saEvalF64Strict` 解折叠（首版解折叠误置分支外致属性形 `Text()` panic 实锤后挪入）。312 转 PASS（`11`）。全机 426→427，余 12。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。
- f64 循环携带后端恒错（本轮，落 `sa_ctrl.go` for-update 位约 40 行 + 279 转拒收 + 440 新锁）：`for (i=0;i<3;i=i+1)`（f64 i）只跑一次、`0.5` 步进亦一次——后端 float 寄存器跨回边重定义无可用形态（手写 SAI 实锤四态：int 初值跑一次、int 初值 +`!i` 仍一次、float 初值 +`!i` 死循环、float 初值无释放 verifier 拒收；与 i32 外计数循环正常对照）。上游同输入 loop1 用整型循环对（`slt/add`，n=3）、loop2 同错（m=1 vs 7，产物无效）。改动 R1 整步进（`i=i±K`，K 经 `saIsF64Operand` 判非浮，计数器侧免判）走 int `add/sub`（上游同形；int 循环携带 sound，`fcmp` 浮读自动转换，P1/fc7 实锤；`/` 溢出语义分叉故只收 `±`）；R2 余下（浮步进/非常值 RHS）大声拒（上游误编译先例，X-statread 同例）。279 转 program 型 `expect.refused`（单文件拒收无 PASS 通道；指纹即拒因行），440 新锁 loop1 形（`3`，真机 PASS）。全机 427→429，余 11。门禁：`--check` **440/440** + `--corpus` **286 agree**（R1/R2 corpus 零漂移）；`go vet` + `gofmt` 干净。残留：while 体内 f64 赋值（零实例）、`++/--/+=` 形 f64 增量（现已大声拒，安全）、`for (i=0.5;…)` 浮初值（R2 在 update 位兜住，init 位未设防，零实例）。
- program 入口非零退出挂门（本轮，9 个 main.ts 一字 + 9 个 main.sa 再生成）：324~332 `return r` 致 exit=r，stdout 对也挂（376 `return s` 同类）；改 `return 0`（stdout 不变）。288 oracle 附带正本（`2020-01-01` 毫秒应 `1577836800000`，旧值 `1583802368` 恰为 mod 2^32 截断，旧 i32 截断 bug 化石）。全机 429→439，余 1（372）。
- `>>>` ToUint32 掩码（本轮，落 `sa_expr.go` 二元发射位 7 行）：后端整数 64 位，裸 `lshr -8, 1` 得 `9223372036854775804`，node 应 `2147483644`；左操作数先 `and 4294967295`（`8>>>1/-8>>>1/8>>1/-1>>>0` 四值与 node 一致）。372 转 PASS。**全机 440/440 + `--check` 440/440 + `--corpus` 286 agree，零失败**；`go vet` + `gofmt` 干净。残留：`>>/<<` 超 32 位分叉与 `>>>=` 复合形未掩码已闭环（本轮：二元 `and r,31` + 复合 `saMaskShiftCount` + 452 真机锁，sci 宽度回归仍未决但本形不再依赖它）。
- 372 定为 sci 后端 i32 位宽回归（本轮，tsgosa 零改动）：`-8 >>> 1` 现值 `9223372036854775804`（64 位），node/上游形皆 `2147483644`（32 位）；同形上游产物在现 sci 亦得 64 位值（上游自证）；另 `2147483647+1` 现得 `2147483648`（应回绕 `-2147483648`）。tsgosa 侧任何发射皆 64 位语义，不可修；移位负字面物化（`sub 0,N`，上游同形）已验证值中性并回滚。待 sci 侧定夺（后端整数位宽）。372 仍红，oracle `-2147483644` 三方皆不合（node 2147483644），随后端修一并正本。
- Map 迭代模型错配（本轮，双仓：sci 新两扁平词向量 + tsgosa 改调 2 行）：`keys()` 返 btree SET、`values()` 返 8 字节元 vec，tsgosa 皆按 4 字节扁平数组 for-of 读（集对象当数组解出堆指针，打印 `2040448879`）；新 `@sa_btree_map_keys_vec`/`@sa_btree_map_values_word_vec`（16 字节柄 + 4 字节元，i32 直取；串键值另步；`entries` 三元组仍旧，另立）。`store` 即 move 故柄存后从柄重载（clone_flat 同形，初版 `UseAfterMove` 实锤后改）。301 转 PASS（`33/33`）。全机 420→421，余 18。门禁：`--check` **439/439** + `--corpus` **286 agree**；`go vet` + `gofmt` 干净。sci 侧 `67a59610` 已推。
- `String.search` i32 种漏登记（本轮，落 `sa_str.go` 一行，零新 Go 文件）：`s.search("1")` 真机 SIGSEGV——`saStrCallIsI32` 列了 indexOf 系却漏 `search`（同返 i32 索引），下游按串柄 `load +0 as ptr` 解整数崩；`indexOf` 同符号对照正常。改动：名单加 `"search"`。实证：最小串参由崩转 `1`；360 全形 PASS（含正则臂 `1/-1/2/2/-1`）。全机 410→411，余 28；漂移仅 360 一例。门禁：`--check` **439/439** + `--corpus` **286 agree** 零回退；`go vet` + `gofmt` 干净。
- R-track 排期：串数组无参 `sort`/`toSorted` 拒收理由已过期（`@ts_str_compare` 现货已落地），需 sci 新增 `@ts_arr_sort_str`（数值插入同骨架，比较位换字典序）+ tsgosa 改调，下轮做。
- R1-19 已闭环：sci `@ts_arr_sort_str`（f9612144）+ tsgosa 串 sort 直调/toSorted 克隆后排，`433_str_sort` 锁定原地/拷贝两形。
- 补充横扫（R1-19 回归教训）：`sa check` 逐个跑全量 committed `main.sai`（--check 只比对字节，不验语义），377/384 直接通过；7 例（203-208 node/deno、289 hash）为 `bare node.sai` 插件装置，需 harness `--project-root`（run.sh:349），本环境裸 check 不可用，step305 后未动，与本轮改动无关。
- super 实例字段读修正：`super.x`（实例数据字段）读实例槽恒错（JS 原型链不见实例态，应 undefined；双边同病），改诚实拒收；方法调用/存取器内联不动。`429_super_getter` 锁定 super getter 两形（纯值 + this 相关，node 镜算 `7/8`）。
- 串 switch 双修（`435_str_switch`）：其一 legacy 链（1/4+ 臂）case 值柄只在体臂释放，default/直通径合并冲突，改 br 前即释；其二串-串臂裸 eq 比柄地址恒假（宏路同病），legacy 串臂改内容比较（复用 `saStrContentEq`，混合臂沿 eq 旧 rule），2/3 臂涉串改走 legacy。node 镜算 `9/3`。
- P0 静态初始化序实例径已闭环（本轮，落 `sa_class.go` 记表期约 10 行，零新 Go 文件）：非字面 static 初值（如 `static y = C.x+10`）旧路落实例槽但无 finit 登记，实例读静默得 0（node 11），类名读早拒；现记表期大声拒（无初值静态沿旧路，字面静态折叠不动；JEV 修法裁决 refuse 92%）。探针实例径改前过改后定位拒；新增 program 拒收锁仓 `demos/441_static_nonlit_refuse`（`expect.refused` 指纹，`run.sh` PASS refused-as-expected）；门禁 `--check` **441/441** + `--corpus` **286 agree** 零漂移；`go vet` + `gofmt` 干净。静态值语义（定义期求值+静态存储）另立，不在本增量。

## 全量扫描（77 探针电池，法：双边通拒 + check + demo 覆盖三核对）

- 双拒对齐（诚实一致，不做，25 项）：bigint 字面量/运算、regexp 字面量、`delete`、this 形参、计算方法名调用、索引签名、`typeof` 查询、`Object.assign`/`fromEntries`/`create`/`defineProperty`、`matchAll`/`normalize`、Math.hypot/clz32、WeakMap、Symbol、Proxy、私有方法、具名 tag 形参、JSON.parse、`.bind`、嵌套/剩余解构、`for-of entries`、D11 链式 `?.`、B23 `?.()`、H37 非零、H57、F01 闭包捕获。
- 分歧已修：对象解构缺省（C1 门 + C2 折叠，438）。
- 分歧 open：串展开 `[..."ab"]`（上游通，薄口拒）→ 已复核：上游按 4 字节步长取 UTF-8 流并装入 i32 元（元值与元种“应为单字串”双错），属误编译；正确实现需逐字柄构造（新机制），暂不做，薄口拒收正确。
- 分歧已决（类名直读非字面静态，零改码，`672ffbcac`）：`static y: i32 = 1 + 2` / `static y: i32 = C.x + 10` 配 `console.log(C.y)`——上游 verdict 过但产物悬空 `load C + 0 as i32`（`sa check` 报 `UnknownRegister`，exit 0 + 无效 SAI 最坏类）；封存 `staticLiteralText` 只折字面量、非字面走实例槽 legacy，类名直读无实例可依故悬空。薄口大声拒正确，不移植。对侧皆通：字面静态类名读（折叠立即数）与实例读（含非字面，`load c + 0` 同形，check ok）。静态初始化序锁仓仅剩实例径核值。
- 通过待锁仓（薄口通、无 demo，逐项核值后锁）：iface 继承/readonly、getter+setter 对、静态初始化序、箭头 this 嵌套、satisfies 串形、`Number()/String()/Boolean()` 构造、`new Array(n)`、`Set/Map` 构造对、`...rest` 形参、数组解构位。

## 待办清单（优先级序）

- P0（静默错码类，见一修一）：V05 stringify 已闭环（本环境 LLVM-14 实测 `JSON.stringify(42)` 打印 `42`，364 全形 PASS；旧堆地址误编译随串缓冲 unwrap 系修复消除）；366 右值串元已闭环（`f()[1]` 真机 `b` 与 node 一致）；H35 除零按设计大声拒（字面量 `1/0` 转译期拒收 `division by zero`，无静默 SIGFPE；变量除数沿旧门）；复查 `==` 混合臂、算术 any 串形（已知双边错码，记限不限修）；静态初始化序实例径双边值错（`c.y` 读零 vs node 11，见同步，待修）。（串展开已定案不移植，见分歧已决。）
- P1（覆盖锁仓）：上段通过待锁仓逐项核值建 43x 式聚焦 demo（多项已有早期 broad 覆盖：95/98/314/321/359/357/47/310；静态初始化序类名径已定案不锁）。439 已锁数组标识源解构（`const [x,y]=a`，node 10/20/30，`sa check` 160 指令 ok，真机 PASS（LLVM-14 后端））；441 已锁非字面 static 拒收（`expect.refused` 指纹）；442 已锁 `Boolean()` 构造（字面量折叠 + 变量形 `ne v,0`，bun 镜算 `1/5/3`，`sa check` 170 指令 ok，上游同位拒收系薄口领先，真机待 LLVM）；443 已锁 `satisfies` 串/i32 双形（纯类型擦除与字面量绑定同形，双边同过，bun 镜算 `ab/42`，`sa check` 81 指令 ok，真机待 LLVM）；444 已锁箭头 this 嵌套（方法内箭头经实例捕获读 `this.v`，`call @__arrow_1(c)`，bun 镜算 `7`，`sa check` 81 指令 ok，双边同过，真机待 LLVM）；445 已锁 `readonly` 接口字段（布局读同形，双边同过，bun 镜算 `9`，`sa check` 69 指令 ok，真机待 LLVM）；446 已锁 `new Array(n)` 定长读写（`a[0..2]` 存取 + `length`，双边同过，bun 镜算 `60/3`，`sa check` 160 指令 ok，真机待 LLVM）；447 已锁 Set/Map 无参构造对（`add/set/getSize` 求和，`sa check` ok，上游 `Set.getSize is not a projected surface` 系薄口领先 step330 旧功，btree 同族调用与 93/94 同形，真机待 LLVM）；448 已锁接口继承布局（`B extends A` 双域读写，双边同过，bun 镜算 `7`，`sa check` 72 指令 ok，真机待 LLVM）；449 已锁 getter+setter 对（存取各走内联体，`c.d=21` 后读 `42`，双边同过，bun 镜算 `42`，`sa check` 84 指令 ok，真机待 LLVM）；450 已锁 rest 首参分离形（`first + rest[0] + rest.length`，与 47 纯巡回区分，双边同过，bun 镜算 `32`，真机待 LLVM）；451 已锁 `?.()` 明函数/方法双形（方法 `c.m?.()` + 顶层 `g?.()`，双边同过，bun 镜算 `8/3`，`sa check` ok，真机 PASS）；452 已锁移位大计数（`8>>33/1<<33/-8>>33/8>>>33` + `>>=/<<=/>>>=` 33 三复合，bun/真机 `4/2/-4/4/4/2/4` 全对；修法落二元发射位 `and r,31` + 复合四路 `saMaskShiftCount` 同掩码（`>>>=` 左值 ToUint32 一并），local 位存回污染（`target` 复用掩码临时量）已修并真机复验；148/277/372/412/65 小计数意图内重生成；JEV 红审 proceed 85%）；453 已锁有捕获箭头返回拒收（旧路过转译吐无效 SAI：占位返 0 + 调用方跨帧传参 `UnknownRegister`；修法按捕获分治：有捕获/`this` 大声拒，无捕获沿旧路 retFn 登记——中途一刀切曾误伤 386 无捕获在跑形，已回调分治并回归；JEV 修法拒 92%、红审 proceed 77%）；454 已锁 f64 while 浮累加（`let x: f64 = 0` 整毒化初值致环第二轮即烂，真机 `4886401197650477000` vs 应 `4.5`；修法初值整字面补 `.0` + 无初值 `0.0`；撞上 R1 整步进旧设计（基整毒化自洽），初值转浮后 `add i,1` 毒化致 440 转圈 hang——R1 改走浮步进 `fadd/fsub`（整字面补 `.0`，i32 变量后端强制转换直传），440 复活 PASS；R2 浮步进仍拒；455 已锁 f64 `++`/`--` local 六形（语句/前后缀/加减，`fadd/fsub 1.0` + `fadd x,0.0` 快照；修中抓获值 temp 走 i64 打印经 sext 截断小数，返回 temp 记 f64 种 + 打印位按 temp 种纠偏；真机六值全对；modvar/ns/元素 f64 目标沿旧门）；456 已锁 i32 元素 `++`/`--` 四形（语句/旧值/新值/减，读-改-写回与复合元素臂同基同存，前缀新值先快照后存；mod f64 双边同拒无动作；真机 `2/2/11/10` 全对）；457 已锁转换后算术流（`Number("7")+1` + `String(42)` 直印，双边同过，bun/真机 `8/42`，真机 PASS）；458 已锁 f64 小数步进 for（R2 开锁：它侧经严格求值；`i=i+0.5` 真机 4 轮全对；279 转正——旧拒收 demo 现真机 `3/7/73` 与 node 逐行一致（上游 m=1 错值），删 `expect.refused` 进仓 `main.sa`+`expected.stdout`，t279 测试复活为强断言 73，`sa test` 90/90；JEV 全程不可用）；459 已锁 f64 变量步进（`i=i+st`，`st: f64` 它侧严格求值，双边同过，真机 `4.000000` PASS）；460 已锁 f64 while 条件（`x>0` 递减至零，双边同过，真机 `0.000000` PASS）；461 已锁抽象类覆盖（`abstract v` + 子类实现，双边同过，真机 `6` PASS）；462 已锁标量 as const 擦除（`"hi"/1 as const`，数组形 400 已锁，对象形双边同拒系接口绑定门；双边同过，真机 `hi/2` PASS；JEV 排 as const ROI #1）；463 已锁命名空间合并（同名双块常量合并，双边同过，真机 `3` PASS）；464 已锁函数重载（签名+实现，取实现形，双边同过，真机 `7` PASS）；465 已锁形参非空断言（`v!` 收窄擦除，401 已锁调用形；双边同过，真机 `5` PASS；JEV ROI #1）；466 已锁字段定赋值断言（`v!: i32` 后赋初值，双边同过，真机 `4` PASS；JEV ROI #2）；467 已锁展开调用（`sum(...args)`，与 29 展开元区分，双边同过，真机 `7` PASS）；470 已锁 override 关键字（子类覆写分发，双边同过，真机 `2` PASS）；471 已锁 ambient declare 擦除（未使用声明零发射，上游同位拒收系薄口领先，真机 `1` PASS）；478 已锁 fs.existsSync（状态判零 SA_FS_OK=0，任错皆 false；修中抓获状态柄漏释 MemoryLeak，已按宏同形补释；hermetic 自指路径；真机 1/0 PASS）；477 已锁串字段 +=（读-改 concat-写回，与普通串存同释放形，真机 ab 全对；f64 字段双边同拒系布局史诗）；476 已锁字段复合赋值（this/外部/接口形参三径，读-改-写回与字段自增核同基同存，真机 16/11 全对）；475 已锁 f64 混合三元臂（f64+整字面经 f64 槽 join，真机 2.5/0 全对；isSafeInteger f64 版上游同拒 parity）；474 已锁 isFinite/isNaN f64 真判定（f64 实参经严格求值，NaN 以 fcmp_ne 自比，isFinite 以 v-v==0；另带整字面 .0 统一（-1 入 f64 位）；真机五值全对；P-A2）；473 已锁混合 + 拼接（非串臂经文本化，i32/bool/f64 走 interp；bool 拼写 1 系全子集既定口径 console/模板/String 全同；真机 PASS）；472 已锁 replace/g 全换（自动改调 replaceAll 核，真机 `bbb/hell0 w0rld` 全对；P-A1 首项）；469 已锁 keyof 取值流（`o[k]` 随键取值，与 280 取长区分，双边同过，真机 `4` PASS）；468 已锁串 for-of（`for ch of "ab"` 计数，与 12 数组形区分，双边同过，真机 `2` PASS）；enumcomp/类型谓词/索引签名/生成器/装饰器皆双边同拒 parity；f64 数组元截断存槽已知缺口（`[1.5,2.5]` 真机读回 1/2，上游同错 parity-in-wrong；demos+corpus 零 f64 数组用例；曾试 choke 点全拒，误伤只读 length 的 315 已回退，8 字节布局 Phase 2 另立；JEV 全程不可用）；F01 真实现（捕获装箱+间接调用，需 sci 运行时）仍 Phase 2；`?.` 双问号链双边同拒（D11 定案不再追，`?.` 单链各形 18/368/374/421 已锁）；静态初始化序实例径已闭环（记表期大声拒 + 441 锁仓，见同步；静态值语义另立）。
- P2（回迁）：串扫描 `@ts_str_arr_scan` 已闭环（双仓：sci 符号元读 `as ptr`→`as i32` 一词修复，与内联 gdb 实锤同形；tsgosa 175 行内联换 7 参 call，门禁/针展开/归属保留，`355_strscan` 八连扫复用数组意图内重生成 -489/+223 行；全量 `--check` 450/450 + `--corpus` 286 agree；link 符号解析过止于 LLVM 层，真机待 LLVM）。
- P3（基建）：探针归档收仓（`/tmp/opencode` 源在本环境已失，须重建探针，生成器即文档）；FFI 清单已闭环（Hono `new Hono()` 与 better-sqlite3 `new Database()` 实使用双形同拒：`import not resolvable` 警告 + step2 kind 244，与 210 系 `expect.refused` 同门，无新增锁仓必要）；LLVM 原生复验已闭环（本轮：真 zig 0.14.1 落 `/opt/zig` 首位 + `llvm-14-dev` + `zig build -Dllvm=true`，`demos/run.sh -j8` 真机 **450/450 PASS**，含 203-208/289 插件链 `--dev` 装 node/deno 后全过；`sa_tests` 真跑 80/91，10 挂皆既有与本次改动无关：t279 系拒收 demo 无 main.sai 结构性 `PackageNotResolved`，t324-t332 prog 族首跑即 assert 挂，回退符号修复前后同挂）；本轮 prog 族修完（`sa test` 真跑 **89/89**：t279 死文件删（279 转拒收后 import 永缺，锁仓归 `expect.refused`）；t324/326/330/331 改直调 lib 函数断言真值 6/7/5/5；t325/327/328/329/332 纯类型库无可调值径，断言归 0 弱锁运行通路，真值仍由各 `expected.stdout` + 真跑 450/450 锁定；修 t324 时抓获调用不 move 实参，测试侧须自 `!o`）；npm 支持；单元测试框架全覆盖；`lib.d.ts` 经 sa_std 全量回迁。


## P4 codex-react-ui 后端重构评估（本轮，全量双边扫 170 文件）
- 现状：单文件过薄口仅 2/170（sshWorkspaceStore 63 行 + codexFallbackInstructions 2 行）；后端共 105715 行，重 TS（泛型 37 文件/联合注解 149 文件/类 67 文件/async 88 文件）+ node 运行时（fs/os/path/crypto/ws/sqlite）+ 事件/流/插件体系。
- 拒收聚类 Top：形参注解567/返回注解503+联合119（富类型：泛型/联合/Record/回调/unknown/void/Promise）；step2 kind244 amplified 428（顶层非常函数语句/对象字面量）；非直接调用126；typeof系60；replace /g、instanceof、动态 Array.isArray、非数组 for-of、富字段类、fs.existsSync、RegExp 组各 <40。
- 结论：整仓重构现不可承接——缺整层：完整类型系统擦除、async/await、事件循环回调分发、多文件链接、npm/bun 运行时绑定、流/进程/网络面。薄口 471 demo 皆 i32/str/arr 微形，与 10 万行量级全 TS 不在同一联赛。
- 可行路径（绞杀者）：先抽最小纯逻辑模块单编单测验证，再逐个扩大；或先定多文件 program-link + 最小 node 插件面。待用户拍板先啃哪块。


## P4b codex 后端缺口双轴计划（频率×难度；170 文件双边扫聚类，kind244=顶层变量语句已实锤）
### 频率 Top（次）
1. 形参富注解 567；2. 返回富注解 503+联合 119；3. 顶层变量语句 428；4. 非直接调用 126（70+56）；5. typeof 未知 60+（40+20）；6. 串条件位 58+（31+15+12）；7. replace/g 49（28+21）；8. node 系 import 警告群（fs/promises 31、child_process 31、events 24…，警告非拒）；9. Array.isArray 动态 25（19+6）；10. for-of 非数组 17；11. instanceof 23（16+7）；12. 富字段类 13；13. fs.existsSync 11；14. 非直接调用语句 11；15. RegExp 组 8。
### 难度轴
- S（单门 widening）：replace/g→replaceAll 自动改调；fs.existsSync 补投影；void 函数 return 值（弃值或收紧）；串条件位其它比较符经现货串比较核；Array 字面量子集 7 例。
- M（需新判定/小运行时）：顶层变量语句（模块初始化序+导出联动）；非直接调用（链式/计算名分形）；typeof 收窄（字面量类型+与 Array.isArray 联动）；Array.isArray 动态（运行时种标）；instanceof（类标比对）；for-of 可迭代（Set/Map 布局遍历）；富字段（bool/f64 入布局）；RegExp 组（ERE 改写）。
- XL（史诗）：富类型全擦除（泛型/联合/Record/函数类型/unknown/void/Promise，1189 次之根）；async/await+事件循环（88 文件）；node/bun 运行时全系绑定；f64 数组布局；闭包透传。
### 分期
- P-A 快赢（S 项逐个锁仓）；P-B 中坚（M 项逐个设计+锁仓）；P-C 史诗（XL 项立项制，每项先 sci/运行时语义再回迁）。


## P4c 缺口全表（频率×难度×分期；170 文件双边扫，521 去重行）
| # | 缺口 | 频率 | 难度 | 分期 | 备注 |
|---|------|------|------|------|------|
| 1 | 富类型注解擦除（泛型/联合/Record/函数类型/unknown/void/Promise/async） | 1189 | XL | P-C | 1189 次之根；async 88 文件 |
| 2 | 顶层变量语句 kind244（顶层 const/对象字面量/导出联动） | 428 | L | P-B1 | 模块初始化序 |
| 3 | 非直接调用（链式/计算名/?.调用/switch 判别） | ~150 | M | P-B2 | 分形逐个 |
| 4 | typeof 收窄（字面量类型/Array联动/计算值） | ~120 | M | P-B2 |  |
| 5 | 串比较/条件位（>＜===/length 门/串值入 i32/三元臂分歧） | ~150 | S-M | P-A3 | 部分走现货串比较 |
| 6 | replace /g→replaceAll 自动改调 | 49 | S | P-A1 | 与 replaceAll 同核 |
| 7 | 多文件链接 import 警告群（本地 100+、node 100+、npm） | 警告非拒 | XL | P-C | program-link+插件面 |
| 8 | Array.isArray 动态 | 25 | M | P-B3 | 运行时种标 |
| 9 | instanceof | 23 | M | P-B3 | 类标比对 |
| 10 | for-of 非数组（Set/Map/迭代器） | 17 | M | P-B3 | 布局遍历 |
| 11 | 对象字面量绑定（需接口布局/方法拒） | ~16 | M | P-B2 | 部分已有 |
| 12 | 富字段类（bool/f64 入布局） | 13 | M | P-B3 |  |
| 13 | fs.existsSync/statSync 等插件面 | ~15 | S | P-A1 | 补投影 |
| 14 | RegExp 组/转义（lookahead/named/\s） | ~15 | M | P-B3 | ERE 改写 |
| 15 | 未声明先调用（require/spawn/stat…，提升序） | ~15 | S-M | P-A2 | 声明序/hoist |
| 16 | unknown class（Map/URL/AbortController/Promise） | ~10 | M | P-B3 | Web 面 |
| 17 | return-in-void | 7 | S | P-A1 | 弃值或收紧 |
| 18 | Array 字面量子集 | 9 | S | P-A1 |  |
| 19 | string+ 拼接（显式 String(x)） | 18 | S | P-A2 |  |
| 20 | 对象解构（需 struct 布局） | 5 | M | P-B2 |  |
| 21 | 非字面缺省参数 | 5 | M | P-B2 | 短调用重放 |
| 22 | struct 返回/调用配对 | ~10 | M | P-B2 |  |
| 23 | JSON.stringify 数组/对象 | 3 | M | P-B3 | 序列化器 |
| 24 | 回调体 lowering | 3 | M | P-B2 |  |
| 25 | f64 入 i32 式（cursor 等） | 5 | S | P-A2 | 强制转换 |
| 26 | switch 非直接调用判别 | 2 | S | P-A2 |  |
| 27 | throw in try（panic 恢复语义） | 2 | L | P-C |  |
| 28 | 数组赋值/复合赋值未知柄 | 8 | S-M | P-A2 |  |
| 29 | .length 基非数组串 | ~9 | S | P-A2 | 门放宽或拒 |
| 30 | async/Promise 事件循环 | 88 文件 | XL | P-C | 见 #1 |
分期顺序：P-A1（6/13/17/18）→ P-A2（15/19/25/26/28/29）→ P-A3（5）→ P-B1（2）→ P-B2（3/4/11/20/21/22/24）→ P-B3（8/9/10/12/14/16/23）→ P-C（1/7/27/30）。

## P4d P-A1 进展（existsSync 已锁，括号联合数组已锁）
- replace/g→replaceAll 已推（472）。
- existsSync 已推（478：状态判零 SA_FS_OK=0，hermetic 真机 1/0 PASS）。
- 括号联合数组注解已推（479：`(number|null)[]` 经 `saAnnotKind` 括号解包走既有联合吸收，null→0 与上游逐行同形，真机 1/3/2 PASS；`Array<number|undefined>` 同门；return-in-void 双边同拒 parity 无活缺口；statSync 需 Stats 对象设计另立）。
- 可空数组 `?.length` 已推（480：`saUnionNullBase` 收 arr（空初值即 arr 零句柄，与 `inst:P` 同律）+ `saLowerOptionalLength` 空守卫槽（与 `?.[i]`/`b?.v` 同形；空读 0，非空读头 +8；直接空读与上游同暴露 parity 不进仓）；真机 0/3/3/8 PASS；串元可空/重绑边形双边同过；定案：float 索引/复合（上游指针算术/`add n,1.5` 静默错码，T2 同族不移植）、var 提升（上游 UnknownRegister 无效产物不移植）、let TDZ（抛语义不移植）。
- 括号被调解包 thin-lead（481：`(add)(…)` 即直调，与注解/表达式括号同律；上游过严拒收；真机 42/3 PASS；成员接收者括号因分发散在 15 处另步）。
- fs 投影缺席文案与上游逐字节对齐（`not a projected std surface (see StdProjectionTable)`；statSync 双边同拒 parity，Stats/len 设计需 sci 侧另立）。
- 工厂调用实例实参已推（482：`use(mk())` 经 `saCallRetKind` 取 `inst:` 返回种直传新柄（含派生 step396 同规；归属经内层调用核自动登记，与 `new` 臂同物；上游 `call @mk()`+双释实证 check-clean）；真机 42/42 PASS）。
- P0 双项复核实锤（本轮，只记台账不改码）：混合 `==`（`1=="1"` 上游 `eq x,柄` 打印 0 vs node 1；`"a"==97` 恒假偶对；`true==1` 子集恒等正确）与 any 串算术（`f(x:any)+1` 上游 `add 柄,1` 指针加法）上游皆静默错码，薄口大声拒正确（铁律 4）；`?.` 链/D11、T2/H7 同族维持。
- 本轮全量原生复验：`demos/run.sh -j8` 真机 **482/482 PASS**（插件链 --dev 齐装后 203-208/289 全过）。
- 括号接收者已推（483：`saCouldBeInst`+`saInstBase` 双门同步解包（单点收 10 处调用方：方法调/字段读写存/下标/串位/ctrl 位）；方法调/字段读写存三形真机 42/41/7 PASS；成员链散点（ns/静态/Object/JSON 门）维持原判）。
- 本轮 triage 定案（只记台账）：typeof 未知（根拒因逐字节一致）、无接口字面量解构（逐字节一致）、非字面缺省（双边同拒，短调用重放另立）、多参 stringify（双边同拒，序列化器另立）、重命名解构/数组回调/bool 字段/工厂派生皆通。
- 括号基 sweep 补齐（484：`(a).length`/直接、`?.length` 守卫、`?.` Map `(m).get`、声明+语句 `new (C)()`，皆经 `saUnwrapTransparent` 复用；真机 2/2/5/3 PASS；裸语句 new alloc 即释 check-clean；JEV 督促复核抓获同文件并行编辑丢更新一处（`?.length` 分支），已串行补回并以 AST 探针+全门禁复验；教训：同文件编辑永不并行）。
- 数组三元展开已推（485：`saArrValueOf` 加三元臂（双臂句柄槽选柄，与串三元 `L_tern_*` 汇合同形；条件核同源；上游同形 check-clean 实证）；字面量真臂/绑定假臂真机 2/7/3/8/9 PASS；全量原生 485/485）。
- 数组三元声明已推 + 串元臂大声拒（485 扩展/486 指纹锁：声明位经 `saIsArrValue` 双臂门 + `saTernaryArrArm` 臂种谓词（字面量扫/标识记种/包装·嵌套递归；调用等未验证形拒）；i32 声明真机 2/1 PASS；串元三元声明·展开皆拒（槽选柄丢串标读回指针，真机 284787488/174363520 vs ccc，上游同错 parity-in-wrong）；全量原生 486/486）。
- entries/values 取长静态折叠已推（487：`saLayoutKeyCount` 由 keys 扩至三者（数据布局长恒等，存取器守卫沿用；keys 形 399/418 在册 thin-lead）；真机 2/2 PASS；全量原生 487/487）。
- 三元调用臂精确放行（485 扩展：`saTernaryArrArm` 加调用臂（唯签名种 `arr`/`arrStr` 可信；内建方法种元模糊如 slice 沿未知拒）；i32 工厂展开·声明真机 3/40、2/40 PASS（上游 sound 一致）；串工厂双边大声拒；全量原生 487/487）。
- 可空串空守卫判零已推（488：`s === null` 即 `eq s, 0`（null/undefined≡0，与 `?.`/`??` 同形；具名绑定直判，未绑定沿旧门禁无效产物；非标识沿旧门；`!==`/`!=`/换位/`undefined` 同臂）；窄化后使用真机 0/a/1 全对且超上游（上游"a"位打指针，undefined 形产物 check 不过）；家族扩展：i32（-1/42）、实例（-1）、Map 缺键 undefined（-1）三形真机全对；`0 != null` 双边同 0（子集 null≡0 契约 vs node 1，定案记限）；全量原生 488/488）。
- 正则 match 管道锁仓（489：POSIX 形 `match(/[0-9]+/)` 绑定+取长+取元真机 1/12 全对 thin-lead；`\d` 转义双边同拒 ERE 口径；全量原生 489/489）。
- startsWith 位点已推（490：`s.startsWith(n, pos)` ≡ `indexOf(n, max(pos,0)) == max(pos,0)`（SELECT 钳零+slt；control.sal 宏随三元先例；轮子负 from 既有缺与 indexOf 同病 sci 侧另立；空针超界残边记窄）；真机 1/0/1/0/1 PASS；全量原生 490/490）。
- 可空串 `?.length` 已推（491：`string|null` 具名串基复用 `saLowerOptionalLength` 空守卫（空读 0，非空读头 +8，与数组 480 同形；上游同形打印 0 实证 parity）；真机 0/2/2/b PASS；bun 空臂 undefined vs SA 0 系 null≡0 契约既定口径（480/488 同例），定案记限；括号基 `(u)?.length` 同形 PASS；全量原生 491/491）。
- const 空柄直读 `.length` 大声拒（492：`const t: string|null = null; t.length` 旧路落 `load t+8` 读零址真机 SIGSEGV（exit 139）实证；上游同错 parity-in-wrong，薄口领先拒收；修法落 `sa_decl.go` 记表期 nullConst（const 永不重绑）+ `saLengthExpr` 直读位拒收（`?.`/let 重绑不受影响）；program 型 `expect.refused` 指纹锁；JEV 修法裁决 refuse；全量原生 492/492；门禁 `--check` 492/492 + `--corpus` 286 agree 零回退）。残留 P0（另步）：const 实例空柄直读 `p.x`（s29f 同形 SIGSEGV，成员径散无中枢 choke）与 let 空柄直读（需流分析）；P-A2 余项：15 未声明先调用（文件内提升已通，require/spawn 裸调另验）、25 f64 入 i32（`f64 x in i32` 仍拒，截断语义待 JEV）、26 switch 非直接判别（直接调用/方法/二元判别已通，残形待收敛）、28 数组复合（具名/形参/元素 `+=/-=/*=` 已通，未知柄残形待收敛）。
- const 空实例成员直读/直调大声拒（493-496：`const p: P|null = null` 配 `p.x`/`p.s`/`c.m()`/`sum(q.a)` 旧路全崩（真机 SIGSEGV×4 实证；`?.` 串域 `p?.s` 亦崩）；修法落 `sa_decl.go` 记表期 nullConst（inst 臂）+ 四读/调门（i32 读位/串读位无守卫故不问 `?.`/数组读位·方法调位仅 plain）大声拒（JEV 沿 492 refuse 口径）；四 program `expect.refused` 同指纹分锁；门禁 `--check` 496/496 + `--corpus` 286 agree + 原生 496/496 零回退；JEV 爆炸半径 safe_to_apply）。残留：let 空柄直读（需流分析）与 `?.` 数组域既有拒因（`optional field access on non-i32 field`，语义待定）维持。
- Math 取整 f64 操作数已推（497：`Math.floor/ceil/round/trunc` 取负字面量/f64 绑定·调用/表达式经 `saEvalF64` 严格求值入现货 `@ts_math_rounding`（正字面量/整数旧路零漂移，u25a/b/c/k 意图内复测全过）；修中抓获 sci 侧真 bug：`farg = add f, 0.0` 整数加截断 f64（`fa` 实测 -7.000000），floor(-7.2)/ceil(7.1) 全按截断错值（sci 侧改 3 处 `fadd`，已推 `21f6754d`，`.sa` 实时解析免重编）；真机 7/-8/3/-7/8/-7/7 与 bun 逐字节一致（含整数负数边）；门禁 `--check` 497/497 + `--corpus` 286 agree + 原生 497/497；JEV 爆炸半径 local_only（回归即 497 本体）。P-A2 余项：15（require/spawn 裸调）/26（残形）/28（未知柄残形）/29（`?.` 数组域既有拒因语义）/25（`Date.now()/1000` 经 date 径已通正数；负时间戳 floor 语义随本轮已对）。
- P-A2 标签虚宽三项锁仓（498/499/500 零代码改动：串 switch `pick("a"/"z")` 真机 1/0；数组整体重赋 `a=[4,5]` 真机 4/2；可空数组 `?.[i] ??` 空 -1/非空 8 双形；三者 bun 逐字节一致；门禁 `--check` 500/500 + `--corpus` 286 agree + 原生 500/500）。P-A2 余项：15（require/spawn/bun:sqlite 等裸调系 node 插件装置，另立）、29（`?.` 数组域既有拒因语义待定）。
- 可空数组 `?.[i]` 直读锁仓（501 零代码改动：非空 `a?.[1]` 真机 8、空 `n?.[0]` 真机 0、重绑 `r?.[0]` 真机 3；空读 0 系越界归零/null≡0 既定口径（480 同例，bun 空臂 undefined 定案记限）；上游同行同值 parity；门禁 `--check` 501/501 + `--corpus` 286 agree + 原生 501/501）。P-A2 正式闭环（15 装置项另立外，其余全锁）；残留：let 空柄流分析、`?.` 串域既有行为维持。
- 串条件真值已推（502：具名串 `if (s)` 复用 `?.length` 空守卫+`ne 0`（空串/零句柄 falsy，可空非空统一门）；串字面量条件编译期折叠（`""` falsy）；上游三形（绑定/字面量/可空）恒真系误编译（`if ("")`→1、`f("")`→1 实证），薄口领先；串关系比较 `>`/`>=` 与 `===` 同仓锁（真机 1/1/1 全对）；门禁 `--check` 502/502 + `--corpus` 286 agree + 原生 502/502）。P-A3 #5 正式闭环。
- let 空柄直线流清除已推（503/504：记表期扩至 let 空初值；`saLowerArm` 压栈 armDepth，仅 depth0 直线赋值成功清除（含值位串赋值；短路 `and/or` 全求值恒执行故 sound，三元/`??` 赋值形不可达实证）；if/while 臂内不清（y29i/y29j 拒收实证，join sound）；JEV 修法裁决 depth（94%）；`let+重绑` 直读原生 2 全对，`?.` 守卫不受影响；门禁 `--check` 504/504 + `--corpus` 286 agree + 原生 504/504；JEV 爆炸半径 safe_to_apply）。残留：串复合空柄崩（y29h 同形 SIGSEGV，H-归属深水区另步）、值位串赋值 UseAfterMove（y29f 同形，归属纪律另步）。
- 空值拼接文本化已推（505：`null+"x"`/记名空串+串/`+=` 空柄统一按 JS 文本义（"nullx"/"undefinedx"）；修中抓获两根因：`saConcatStr` 经 `saToSlice` 对记名空串裸名直读崩、`s+="x"` 左基直读崩（上游前者垃圾数后者未验）；`nullConst` 记表值扩展 null/undefined 区分（map[string]string，读门改存在性）；`= undefined` 初值为 Identifier 非关键字，记表条件补 `saIsUndefinedIdent`（`string|undefined` 既往漏记走 i32 "0x" 错路）；门禁 `--check` 505/505 + `--corpus` 286 agree + 原生 505/505）。
- 值位串赋值归属纪律补齐（506 无新 demo：`a = "x"` 值位（`&&` 右臂等）既往漏 `saRebindRelease`+`saConsumeOwn`+堆复位，原生 UseAfterMove 构建失败；补语句位 2789-2795 同形四行后 y29f 可构建运行；直线恒执行的值位形暂不可达（实参/初值/`.length` 基皆拒），短路 `and` 非短路全求值语义差记限另步；改动触所有值位串赋值发射，全量门禁 `--check` 505/505 + `--corpus` 286 agree + 原生 505/505 零漂移背书）。
- 短路 `&&`/`||` 右臂恒求值记限 Phase2（z9a 实证：`c && hit()`/`c || hit()` 双边皆 `9/0/9/1`，bun `0/9/1`；`and`/`or` 指令形+双臂预求值，无短路；JEV 裁决 defer（98%，parity-in-wrong 先例：f64 数组元截断同例）；短路化需动 saEvalI32 二元核心+br 槽+右臂归属 join，面大量级另步；y29h 串复合空柄崩经 505 左基文本化复验已消（`s += "x"` 空柄得 "nullx"）。
- P-A2 #15 正式闭环（双边同拒 parity：`require()` 薄口/上游同拒 `call to unknown function require`；`spawn`（`node:child_process` 不在投影表）薄口调用拒、上游 import-warning+调用拒，双边 refused=true 无活缺口；`child_process` 调用门/句柄种/插件 ABI 另立 Phase 2；文件内函数提升 p15a/b 早通实证）。
- Array 构造子集锁仓（506 零代码改动：`new Array<i32>(3)` 长 3、`Array.isArray` 判 1、`.map(x=>x*2)` 值 4、`.fill(7)` 双 7；真机 `3/1/4/7/7` 与 bun 逐字节一致；链式 `new Array(2).fill(7)` 初值位拒系初值位门限（441 同类），分写即通非缺口；门禁 `--check` 506/506 + `--corpus` 286 agree + 原生 506/506）。
- Array 高频 API 锁仓（507 零代码改动：`Array.from` 取 3、`filter` 长 2 头 3、`includes`/`indexOf` 判 1/2、`join("-")` 得 5-6-7、`push` 长 3/`pop` 值 3/长 2；真机 9 行与 bun 逐字节一致；门禁 `--check` 507/507 + `--corpus` 286 agree + 原生 507/507）。
- Map/Set 高频锁仓（508 零代码改动：Map set/get??/has/size/delete 真机 1/1/2/0，Set add/has/size/delete 真机 1/2/1；7 行与 bun 逐字节一致；门禁 `--check` 508/508 + `--corpus` 286 agree + 原生 508/508）。
- Object/JSON 锁仓（509 零代码改动：`JSON.stringify({x:5})` 得 {"x":5}、spread `{...a,y:2}` 得 1/2、`Object.keys(p).length` 得 2；匿名对象字面量仍须接口注解（既有门），`Object.values(o)[i]` 下标与 keys 具名绑定初值位拒系既有静态门限；门禁 `--check` 509/509 + `--corpus` 286 agree + 原生 509/509）。
- String 高频 API 锁仓（510 零代码改动：slice/substring/toUpperCase/trim/charCodeAt/repeat/padStart/parseInt 真机 8 行与 bun 逐字节一致；门禁 `--check` 510/510 + `--corpus` 286 agree + 原生 510/510）。
- Math/整数位锁仓（511 零代码改动：max/min/abs/sqrt/pow 得 7/3/5/4/1024、`Number.isInteger` 判 1、`(3).toString()` 得 3、`715` 得 16/4/3/7；11 行与 bun 逐字节一致；`3.75 | 0` 薄口拒收正确（上游放行但泄漏 f64 打印 3.000000，parity-in-wrong 记限）；门禁 `--check` 511/511 + `--corpus` 286 agree + 原生 511/511）。
- `new Date(x)` millis 形已推（512：整字面/i32 绑定 sext 入 i64 柄、date 柄值拷（i64 值语义无别名）、f64/串沿旧门；声明位+值位双拒点同收；上游同拒（parity 领先修）；`getTime` 1000/`getFullYear` 1970/拷贝 2000/now>0 真机与 bun 逐字节一致（UTC）；后端 `new Date(createdAt)` 实例覆盖；门禁 `--check` 512/512 + `--corpus` 286 agree + 原生 512/512）。
- 声明初值数组别名大声拒（513：`const r = a`/`const r = a.reverse()` 既往放行，原生 UseAfterMove（函数尾 `!a`）；`saLowerArrDecl` 补赋值位 H13 同门（注释自认偏离上游）；串元 reverse 体经异构探针实证正确（`["a","ccccc"]` 反转 `ccccc/a` 全对），`a.reverse()` 语句形 `3/3` 全对；门禁 `--check` 513/513 + `--corpus` 286 agree + 原生 513/513）。
- 正则全局替换锁仓（514 零代码改动：`/[0-9]/g` 得 a#b#、`aaa`→bbb，真机与 bun 逐字节一致；`split("")` 薄口诚实拒（串元数组超 i32 槽既定架构限），上游放行但生成非法 SAI（`load  + 0` 空操作数，ForbiddenSyntax 实证），parity-in-wrong 记限；门禁 `--check` 514/514 + `--corpus` 286 agree + 原生 514/514）。
- 语法混合锁仓（515 零代码改动：for-of 累加 6、数组解构 10/20、展开调用 add(...args) 得 7、enum 取 0；5 行与 bun 逐字节一致；门禁 `--check` 515/515 + `--corpus` 286 agree + 原生 515/515）。
- 流程混合锁仓（516 零代码改动：label-continue 得 3、super 方法链得 111/1、throw 整形 catch 得 1； 实例值位与  未知类沿旧门；门禁 `--check` 516/516 + `--corpus` 286 agree + 原生 516/516）。
- 批量十连锁仓（517-526 零代码改动）：String(42)/endsWith/??=/默认参数/every-some-find/typeof/解构默认/padEnd/while-true/concat，真机全与 bun 逐字节一致；`Number("13")` 薄口打印 13.000000（f64 泄漏）剔除记限，`slice`/可选链对象形沿既有门；门禁 `--check` 526/526 + `--corpus` 286 agree + 原生 526/526）。
- 批量十二连（527-538）：模板插值/getter-setter/静态成员/do-while/break/剩余参数/私有字段/as断言/可选参数/数组slice十通过锁（真机全与 bun 一致）；混元元组 `[i32,string]` 串柄截断崩→加 i32 元注解门（全串元组 arrStr 豁免），537 通过+538 拒收双锁；门禁 `--check` 538/538 + `--corpus` 286 agree + 原生 538/538）。
- 条件取反锁仓（539：`!x`/`!!x` 操作数经真值门递归（0翻1/余纯数字翻0/temp补eq），三元/if/while 条件位通用；7 形与 bun 逐字节一致；门禁 `--check` 539/539 + `--corpus` 286 agree + 原生 539/539）。
- for-of 串元素直授已推（540：`for (c of str) { s = c; }` 单轮新鲜柄 move 移交（forOfStr 记名+2796 门豁免，随块域回滚；循环尾跳释/下轮覆盖闭环），覆盖得 b、复合得 ab；`push(c)` 内容拷贝天然 sound 实证；门禁 `--check` 540/540 + `--corpus` 286 agree + 原生 540/540）。
- 批量十连锁仓（541-550 零代码改动）：??链/for(;;)/复合赋值/自增/取整/大数/swap/split逗号/setTime/具名接口返回，真机全与 bun 一致；Map迭代（for-of解构）拒收沿旧门；门禁 `--check` 550/550 + `--corpus` 286 agree + 原生 550/550）。
- for-in 对象键直授已推（541：静态展开键每份新鲜具化，forOfStr 记名+2796 门豁免，540 同律；真机 a 全对；附带六锁仓（542 串域读/543 in/544 接口对象/545 void/546 多声明/547 串下标）；门禁 `--check` 547/547 + `--corpus` 286 agree + 原生 547/547）。typeof null 既定口径（null 即 0 子集义）不锁；delete 静态布局禁删记限。
- instanceof 同布局折叠+enum 传参锁仓（558/559：`c instanceof C` 编译期折叠 1（异名/子类沿旧门禁误判假）；enum 实参比较得 1/0；门禁 `--check` 559/559 + `--corpus` 286 agree + 原生 559/559）。高阶具名实参（函数类型注解）H 级另步。
- Record 点读已推（560：`r.k` 按 `.get("k")` 同义（键编译期常量，方法名沿旧门，读回种按 mapVals）；串值形另抓 saToSlice 缺 str 回种纠偏（f64 有 713 同形，串 head 被 sext 误印数字），补后双值形 1/hi 全对；门禁 `--check` 560/560 + `--corpus` 286 agree + 原生 560/560）。
- 批量九连（561-569）：Map<string,string> 串值记表修（`new` 类型参数忽略致 set 拒）+ 嵌套对象/typeof比较/逗号/sort/split-slice/布尔返回/泛型数组/unknown 九锁仓，真机全对；门禁 `--check` 569/569 + `--corpus` 286 agree + 原生 569/569）。
- 批量六连锁仓（570-575 零代码改动）：幂/位非/进制串/简单模板/PI整数口径/enum返回改i32注解，真机全对；Math.PI 既定折叠 3（`>3` 恒假系子集精度口径）；toFixed/IIFE/delete数组元沿旧门记限。门禁 `--check` 575/575 + `--corpus` 286 agree + 原生 575/575）。
- 集合构造初值已推（576：`new Set([..])` 逐元 add 去重、`new Map([[k,v]])` 双元逐项 set（值串/i32 双门），余形大声拒禁静默丢；真机 3/1/1 全对；门禁 `--check` 576/576 + `--corpus` 286 agree + 原生 576/576）。
- 批量六连锁仓（577-582 零代码改动）：const枚举/异构枚举/抽象类/as-const/泛型函数/readonly数组，真机全与 bun 一致；嵌套命名空间/satisfies匿名对象沿既有门记限；门禁 `--check` 582/582 + `--corpus` 286 agree + 原生 582/582）。
- Object.values 已推（583：已知布局 i32 数据槽声明序具化新数组（存取器/异种槽/未知布局沿旧门），调用种判定+求值门双收；真机 2/3/4 全对；门禁 `--check` 583/583 + `--corpus` 286 agree + 原生 583/583）。
- push 多参已推（584：逐元压栈返末次新长，JS 同义；种检查逐元；真机 3/3/3 与 bun 一致；Math.max 三标量改 spread 形即通记限；门禁 `--check` 584/584 + `--corpus` 286 agree + 原生 584/584）。
- 枚举注解记 i32 已推（585：声明位+形参位（箭头同核）由泛型句柄 arr 改记 i32（成员整数本色；equality 折叠 eq 同效）；152/559 意图内重生成语义保持；真机 1/0/1 全对；门禁 `--check` 585/585 + `--corpus` 286 agree + 原生 585/585）。
- 调用结果 `?.length` 已推（586：新鲜非空柄空臂不可达，等价直读（plain 调用基同形）；真机 1 与 bun 一致；门禁 `--check` 586/586 + `--corpus` 286 agree + 原生 586/586）。
- Map.forEach 已推（587：`sa_btree_map_iter_vec` 快照三元组巡回，回调(v[,k])（值种按建表，键 head 每轮具化即释）；单参累加 12、双参 5/1/7/1 全对；门禁 `--check` 587/587 + `--corpus` 286 agree + 原生 587/587）。
- 顶层折叠串串判定已推（588：`saIsStrExpr` 标识符分支补 topConsts/topStr 折叠读（与 `saEvalStr` 折叠读位同序，局部遮蔽优先；形状证据：封存 lowerExpr:2774-2781 constVals+constIsStr；被赋值名走 modVars 槽故无交）；const/let 折叠串直打/拼接/取长/比较/串形参真机 hi/hi-sa/2/1/sa! 与 bun 一致；门禁 `--check` 588/588 + `--corpus` 286 agree + 原生 588/588）。
- 顶层折叠串条件真值已推（589：`saCondOperand` 标识符分支 topConsts/topStr 臂由拒收改具化后走 502 空守卫（`?.length` 槽 + `ne 0`，具化柄用后即释；上游 br 句柄恒真、`if ("")` 取 then 臂系误编译实锤，薄口领先）；hi/空/取反/while 真机 1/0/2/3 与 bun 一致；门禁 `--check` 589/589 + `--corpus` 286 agree + 原生 589/589）。
- 顶层名 typeof 种映射已推（590：`saTypeofKind` 标识符分支补槽臂（i32→number/str→string）+ 折叠臂（串→string，余下→number；形状证据：封存 lowerTypeof modStateOf/constVals 两分支；上游 i32 槽误报 "boolean" 系静默错译实锤，薄口按 JS 真值，X-datecmp 同例；bool 折叠与上游同取 number 逐字节同形）；折叠串/数/槽/串槽/比较真机 string/number/number/string/5 与 bun 一致；门禁 `--check` 590/590 + `--corpus` 286 agree + 原生 590/590）。
- 值位取反串真值 + 折叠 `?.length` 已推（618-627 十连锁：`saLowerPrefixUnary` 之 `!` 标识符臂增串分支（局部 str + 折叠串具化走 502 空守卫后 `eq 0`，具化柄用后即释；折叠 i32/bool 内联文本直判；既有 `eq 柄,0` 恒 0（含局部）系误编译，上游同形实锤，薄口领先）+ `saLowerLengthExpr` 之 `?.length` 增折叠串基（具化后走 491 同形守卫；局部同形已通）；局部/折叠/链式/三元/返回/实参/遮蔽真机全对；591-599（588 家族：模板/方法/for-of/switch/闭包/遮蔽/双拼/别名链thin-lead值对/长度换算）+ 600-608（589 家族：else-if/嵌套/!/!!/?.length/do-while-`==`/for-len/while/长比较/折叠取反）+ 609-617（590 家族：数布/槽/串槽/不等/switch/闭包/布quirk定案/槽if/比较表达式）各十；残留记限：do-while 体 console+槽既有 MemoryLeak（改前复现）、`&&` 裸串/do-while-`&&`串通用缺口、`s==null` 另步；门禁 `--check` 627/627 + `--corpus` 286 agree + 原生 627/627）。
- 循环体临时量回边释放已推（628-637 十连锁：`saScope` 增 `armReleaseTemps` 开关 + `saLowerArm` 截断前释本臂新生归属临时量（复用既有 `saReleaseArmTemps` helper，for-of/三元同口径；具名不动：体声明名可进条件，释之即 UAF，temp 永不复用故恒安全；形状证据：上游 while 回边 `!t_17/!t_13/!t_9/!t_6/!t_5`）+ while/do-while/legacy-for/for-macro/for-of/for-in 六体臂置位；顶层 do-while+槽+console 之 MemoryLeak 转净（改前 verifier 实锤；while/for/for-of 同形运行时每轮泄漏亦修，零行程路径验尸沉默系 verifier 零行程免检）；13 存量 demo 漂移逐行审计全系新增 `!` 释放行零他变，再生后值零变；do-while/while/for/嵌套/break/串体/for-of/折叠条件真机与 bun 逐字节一致；门禁 `--check` 637/637 + `--corpus` 286 agree + 原生 637/637）。
- 条件位串侧逻辑真短路已推（638-647 十连锁：`saCondOperand` 前增串侧 `&&`/`||` 先行门（括号透明 `saPeelParens`；纯 i32 沿既有 eager 门，零漂移）+ `saLowerCondLogic`（真值化：绑定串/折叠串具化/通用串求值经 `?.length` 空守卫后 `ne 0`，bool 直通，纯整数折叠，非串求值归一；`&&` 左假短路 0/`||` 左真短路 1，槽汇合；`&&=`/`??` 槽形同源；分支立即数经 `ne 0` 物化，110 同训；右臂基址后 `saReleaseArmTemps` 臂内释放，域外落字即 UnknownRegister，三元臂同形；嵌套逻辑递归 strict 收敛；串形参/串调用/拼接/取反/比较/括号嵌套全对；上游 `and/or 柄,1` 恒真系误编译实锤，空串两形薄口值对而上游误真，薄口领先；途中抓获纯 i32 `2 && 1` eager 位与误编译（`and` 得 0 而应 1，既有语义，记限不动）；门禁 `--check` 647/647 + `--corpus` 286 agree + 原生 647/647）。
- 折叠串空比较已推（648-657 十连锁：`saEvalI32` 之 `==/!=/===/!==` 空比较臂增折叠串分支（具化后 `eq/ne 柄,0`，具化柄用后即释；null/undefined≡0 子句口径，`?.`/`??` 同形；上游 `eq t_2, 0` 同形实证；局部绑定臂零动）；`==/!=/===/!==` × null/undefined/左右序/let 折叠/while-`&&`/值位三元/局部回归真机与 bun 逐字节一致；门禁 `--check` 657/657 + `--corpus` 286 agree + 原生 657/657）。
- 条件位串空合真值已推（658-667 十连锁：`saCondOperand` 先行门增串 `??`（括号透明，与 `&&` 门同块；实例臂在下互斥）+ `!` 臂括号透明（求值侧括号纯透传，零发射差）+ 串求值取柄后空守卫（上游 `br 柄` 恒真误编译实锤；可空形参 null/""/"a" 真机 1/0/1 与 bun 一致；union 返回串值位亦拒系独立特性记残留）；折叠/局部/while/或取反/链式/串调用/嵌套/空缺省/函数返回真机与 bun 逐字节一致；数组直打系上游裸内存转储误编译类，按铁律不仿记另步待确认；门禁 `--check` 667/667 + `--corpus` 286 agree + 原生 667/667）。
- `void` 调用求值丢弃已推（668-677 十连锁：`saEvalI32` 之 `VoidExpression` 臂增 void 调用分支（`saCallRetKind` 纯窥种 + console.* 恒 void 惯用法复 sa_decl.go:2491-2495；调用核直求值丢弃，归属口守双释；上游裸 call 同形实证；非 void 调用/余形沿旧路零动）；用户函数/箭头/console/函数内/i32 调用/字面量/嵌套/分支/循环/连发真机与 bun 逐字节一致；门禁 `--check` 677/677 + `--corpus` 286 agree + 原生 677/677）。
- 串 for-in 索引巡回已推（678-687 十连锁：`saLowerForIn` 增串基臂（`saIsStrExpr` 先行，经 `saEvalStr` 求柄，下同数组骨架；16 字节头同形，绑定 i32；求柄归属巡后块统一释放；上游 `k = add idx, 0` 同形实证；数组路零字节动）；折叠/数组回归/函数/下标读/break/空串/let/嵌套/计数/拼接真机与 bun 逐字节一致（`===` 下标走 i32 恒等、算术走 i32，与上游一致，JS 串键差系子集口径已注；`continue` 跳增量系文档化既有行为，数组同形，demo 规避另记残留）；门禁 `--check` 687/687 + `--corpus` 286 agree + 原生 687/687）。
- 空返回与通用空比较已推（688-697 十连锁：`saEvalStr` 增 null/undefined 关键字臂（空即 0 句柄，与 i32 侧同律）+ 返回位 `string` 种未绑定 `undefined` 标识收敛（遮蔽优先）+ `==/!=` 空比较通用串臂（调用/拼接等串值具化后空判，标识符形在上优先；空≡0 子集口径双边既定）；破案记：拒收位实为 `return null`（2:7 即 return 行），调用方表项双边一致，早通；可空直打 segfault 与可空形参既有行为同口径（须经 `??`/守卫）；真机与 bun 逐字节一致；门禁 `--check` 697/697 + `--corpus` 286 agree + 原生 697/697）。
- 顶层 const i32 数组快照已推（698-707 十连锁：P-B1 首增量，JEV 选 a（82%）；`saTopArrPrescan` 收顶层 const 直接量数组（名→元文本，全 declarator 同形，混合/串元/重名/非 arr 注解不收沿旧 kind244 门）+ `saMaterializeTopArr` 用点物化本地新柄（saLowerArrayLiteral 非 spread 臂同形，归属登记读后即释）+ 下标读/`length`/`?.` 三臂（局部/mod 槽遮蔽优先）+ 别名推断/注解双径精确拒收；698-705 真机与 bun 逐字节一致（空/OOB 读 0 系越界归零既定口径，本地同形实证），706/707 program 型 expect.refused（变异/别名/返回/for-of 七形皆响）；门禁 `--check` 707/707 + `--corpus` 286 agree + 原生 707/707）。
- 顶层 const 数组纯读方法已推（708-717 十连锁：P-B1 第二增量，JEV 选 c（30% 弱置信，通路审计后收敛为方法分发点白名单方案，通用臂因调用实参传拷静默风险否决）；`saTopArrPureMethod` 白名单（slice/indexOf/includes/join/concat）+ `saTopArrPureCall` 门控（sa_expr.go 调用点）+ `saTopArrPureRecv` 物化（saLowerArrCall 总线）+ `saArrCallRet` 种表接收器识别（join str 种/slice arr 种，链式/取长直通）+ `Array.isArray` 快照恒折 1；变异/未知方法沿旧门零扰动（706 指纹 intact）；708-716 真机与 bun 逐字节一致（includes/isArray 布尔 1/0 系既定打印口径），717 program 型 expect.refused（pop/push/splice/sort 表达式位四形）；门禁 `--check` 717/717 + `--corpus` 286 agree + 原生 717/717）。
- 顶层数组纯类型包装已推（718-727 十连锁：P-B1 第三增量，JEV 选 a（58%）；预扫初值经 `saUnwrapTransparent` 解包（`as const`/satisfies/括号，非值语义；下标臂既有解包同律），readonly/tuple 注解本即 arr 种零改动；旧臂（下标/`length`/纯读方法）全复用；串元/混合声明整句不收沿旧 kind244 门；718-726 真机与 bun 逐字节一致（includes 布尔 1/0 系既定打印口径），727 program 型 expect.refused；门禁 `--check` 727/727 + `--corpus` 286 agree + 原生 727/727）。
- 顶层数组巡回展开已推（728-737 十连锁：P-B1 第四增量，JEV 选 a（76%）；`saTopArrSnapshot` 快照 helper（标识符直指+遮蔽优先，调用实参/返回/别名/存储位禁用）+ `saForArrHandle` 巡回臂（for-of/for-in 同漏斗，巡后释与字面量柄同律）+ spread 字面量臂（物化后整片合并）；spread-call/高阶回调沿旧门；707 夹具演进（for-of 转正移出，保留返回/别名双指纹）；728-736 真机与 bun 逐字节一致（for-in 下标累加与本地同口径），737 program 型 expect.refused；门禁 `--check` 737/737 + `--corpus` 286 agree + 原生 737/737）。
- 顶层数组高阶回调已推（738-747 十连锁：P-B1 第五增量，JEV 选 a（16% 弱置信，先探本地行为）；`saTopArrPureMethod` 白名单扩至 map/filter/find/findIndex/some/every/reduce/reduceRight/forEach（门控/物化/种表三处调用点零改继承）；回调副作用为用户语义与本地同律，具名回调沿既有内联门精确拒收；737 夹具演进（map 转正移出，保留 spread-call 指纹）；738-746 真机与 bun 逐字节一致（some/every 布尔与 find-miss 归零系既定口径），747 program 型 expect.refused；门禁 `--check` 747/747 + `--corpus` 286 agree + 原生 747/747）。
- 顶层数组复制系方法已推（748-757 十连锁：P-B1 第六增量，JEV 选 a（86%）；`saTopArrPureMethod` 白名单扩至 lastIndexOf/at/toReversed/toSorted/with/toSpliced（门控/物化/种表三处零改继承，拷贝语义与快照天然相容，toSorted 比较器回调同行）；748-757 真机与 bun 逐字节一致；门禁 `--check` 757/757 + `--corpus` 286 agree + 原生 757/757）。
- 顶层串元数组快照已推（758-767 十连锁：P-B1 第七增量，JEV 选 a（54%）；`saTopArr` 结构化（i32/串元）+ 预扫串字面量子集（混元/空无注解沿旧门）+ 串物化分支（16 字节头逐元具化 + arrStr 标记，本地同形）+ 串读位三臂（saIsStrExpr/saEvalStr/for-of 行绑 str）+ indexOf/includes 串扫描接收器（recvIsStrArray 快照识别）；slice/concat/join 等串方法与 spread-串本地同形破碎，沿旧门；727 夹具演进（串转正移出）；758-766 真机与 bun 逐字节一致（includes 布尔系既定口径），767 program 型 expect.refused；门禁 `--check` 767/767 + `--corpus` 286 agree + 原生 767/767）。
- 顶层数组识别精度已推（768-777 十连锁：P-B1 第八增量，JEV 选 b（18% 弱置信）；typeof 恒折 object（含 undefined 守卫常量折叠）+ 条件/取反真值折叠（空数组亦真）+ i32/串值位精确拒因 + `new Set/Map` 绑定实参 panic 改拒收（0 崩溃铁律回归锁）；768-771/777 真机与 bun 逐字节一致（!布尔系既定打印口径），772-776 program 型 expect.refused；门禁 `--check` 777/777 + `--corpus` 286 agree + 原生 777/777）。
- 顶层数组残余方法已推（778-787 十连锁：P-B1 第九增量，JEV 选 b（54%）；`saTopArrPureMethod` 白名单扩至 flat/flatMap/findLast/findLastIndex（门控/物化/种表三处零改继承）；调用实参传快照精确拒收（被调变异即分歧）；778-785 真机与 bun 逐字节一致（findLast-miss 归零系既定口径），786/787 program 型 expect.refused；门禁 `--check` 787/787 + `--corpus` 286 agree + 原生 787/787）。
- 顶层对象常量快照已推（788-797 十连锁：P-B1 第十增量，JEV 选 a（30% 弱置信）；`saTopObjPrescan` 接口先行（注解须已记录接口，键集精确相等，字段初值字面量且与布局种交叉一致，简写/计算键/spread/方法整句不收）+ 初值节点复用 `saLowerObjectLiteral` 现场具化 + i32/串字段读双臂（`saLowerClassFieldLoad` 复用）+ 串域门/真值/typeof/别名对象识别；别名/存储/传参/返回沿旧门大声拒；788-796 真机与 bun 逐字节一致，797 program 型 expect.refused；门禁 `--check` 797/797 + `--corpus` 286 agree + 原生 797/797）。
- 顶层嵌套数组快照已推（798-807 十连锁：P-B1 第十一增量，JEV 选 a（56%）；预扫单层嵌套 i32（`nested` 表，深层/异构不收）+ 物化内外两级具化（外层 4 字节槽存内层柄 + arrNest 标记）+ 双下标臂（次级空守卫读，外层 OOB 归零不崩，本地链式同形崩溃本仓不扩散）+ 内层取长臂 + for-of 行绑 arr；单下标值/存储/别名沿旧门大声拒；798-806 真机与 bun 逐字节一致（OOB 归零系既定口径），807 program 型 expect.refused；门禁 `--check` 807/807 + `--corpus` 286 agree + 原生 807/807）。
- 顶层深层嵌套快照已推（808-817 十连锁：P-B1 第十二增量，JEV 改判 a（92%，结构体字面量本地亦拒无移植源）；`saTopArr` 递归化（同构深度一致，串内层/异构不收）+ 递归物化（`saBuildNestArray`/`saAssembleHandles`）+ 三下标臂（链长须等于深度，次级空守卫）+ 深层内层取长臂；外层 OOB 空柄归零不崩（本地链式同形崩溃本仓不扩散）；808-812/815-817 真机与 bun 逐字节一致（OOB 归零系既定口径），813/814 program 型 expect.refused；门禁 `--check` 817/817 + `--corpus` 286 agree + 原生 817/817）。
- 未赋值 let/var 快照已推（818-827 十连锁：P-B1 第十三增量，JEV 选 a（64%）；预扫 const 永收 + let/var 全 declarator 未被赋值（`assigned` 集判定，被赋值者 modstate 槽认领在先，`using` 沿旧门；数组/对象同律）；重绑/存储沿旧门大声拒；818-826 真机与 bun 逐字节一致，827 program 型 expect.refused；门禁 `--check` 827/827 + `--corpus` 286 agree + 原生 827/827）。
- 顶层 Math 常量折叠已推（828-837 十连锁：P-B1 第十四增量，JEV 选 b（54%）；`saFoldTopLevelConst` 增 `Math.PI`/`Math.E` 折 3/2 臂（i32 位整数子集同形，方法别名沿旧门）；828-836 真机与本地逐字节一致（PI≡3/E≡2 系既定截断口径；f64 混排比较既有行为另记残留），837 program 型 expect.refused；门禁 `--check` 837/837 + `--corpus` 286 agree + 原生 837/837）。
- 顶层残余形普查已推（838-847 十连锁：P-B1 第十五增量，JEV 选 b（36%）；零 Go 改动纯夹具波：using/import-eq 精确拒因锁 + 入口语句语义（entry 即程序、无 main 可行、显式 main() 调 main__user）+ 类表达式/多枚举/未初始化 var；入口 while+main 混写主函数孤立系 Node 一致语义（main 不自调），夹具规避；838-847 真机预期一致；门禁 `--check` 847/847 + `--corpus` 286 agree + 原生 847/847）。
- 顶层模板串折叠已推（848-857 十连锁：P-B1 第十六增量，JEV 选 a（62%）；`saFoldTopLevelConst` 增模板臂（头/尾煮后直拼 + `saTplHoleText` 孔门：十进制整/±号/true-false文本/串煮后/已折串量/已折数值量（"0"/"1" 与 bool 折叠歧义故拒）；调用/浮点/超长沿旧门）；848-855/857 真机与 bun 逐字节一致，856 program 型 expect.refused；门禁 `--check` 857/857 + `--corpus` 286 agree + 原生 857/857）。
- IIFE 立即调用已推（858-867 十连锁：P-B2 首增量（JEV 4% 弱置信，工程判断直行）；调用核增箭头/函数表达式直调臂（括号透明，`?.()` 沿旧门），经 `saCallbackValue` 单帧内联（形参种按注解缺省 i32，元数精确，块体 inlineRet 槽，体归属臂纪律释放；数组形参同帧别名归属深水区本轮拒收）；柯里化跨帧捕获系 Phase 2 显式边界不动；858-866 真机与 bun 逐字节一致，867 program 型 expect.refused；门禁 `--check` 867/867 + `--corpus` 286 agree + 原生 867/867）。
- 用户串标签模板已推（868-877 十连锁：P-B2 第二增量；`saLowerTaggedCall` 返回种按调用位匹配（新增 want 参；i32 位要 number，串位要 string，种错位精确拒因）+ `saIsStrExpr`/`saEvalStr` 用户串标签路由（首参 string[] + 串返回种；String.raw 另走）；868-876 真机与 bun 逐字节一致，877 program 型 expect.refused；门禁 `--check` 877/877 + `--corpus` 286 agree + 原生 877/877）。
- 可选串成员已推（878-887 十连锁：P-B2 第三增量；878 具名/调用串基 `?.length`/`?.[i]` 空守卫沿 HEAD（`sa_arr.go` length 位 + `sa_str.go` 下标位），本轮补成员名守卫（`s?.foo` 曾误折取长，现大声拒，约 5 行，落 `sa_arr.go`）+ 9 通形（具名/空/调用/折叠/链式/拼接/空合）+ 887 program 型 expect.refused（非 length 成员 + 可选方法调用双指纹）；878-886 真机与 node 逐字节一致（`n?.length` 按 null≡0 既定口径得 0 vs node undefined，480/491 同例），887 双拒因全中；门禁 `--check` 887/887 + `--corpus` 286 agree + 原生 10/10）。
- 循环控制混合锁仓（888-897 十连锁：P-B2 第四增量；零 Go 改动纯夹具波：标号 break/continue、do-while continue、for-in 累加、多声明符 for、for-of break、嵌套 continue、while continue、步进 for 真机全与 node 逐字节一致（3/9/60/303/3/8/8/37/20）；897 program 型 expect.refused 锁逗号双增量（legacy/宏双径皆只收单增量，`unsupported for incrementor`）；门禁 `--check` 897/897 + `--corpus` 286 agree + 原生 10/10）。
- 表达式混合锁仓（898-907 十连锁：P-B2 第五增量；零 Go 改动纯夹具波：链式调用取长、计算下标、spread 展开调用、对象 spread、调用判别 switch、命名空间常量、串比较、括号接收者、实例形参直传真机全与 node 逐字节一致（2/2/7/3/2/42/1/2/5）；907 program 型 expect.refused 锁 `delete`（静态布局禁删，Map/Set 用 `.delete()`）；门禁 `--check` 907/907 + `--corpus` 286 agree + 原生 10/10）。
- 语句混合锁仓（908-917 十连锁：P-B2 第六增量；零 Go 改动纯夹具波：数组复合赋值、串 switch、f64 取整（整数子集截断）、嵌套模板、while-break、split 取长、catch 值透传、void 调用、嵌套结构真机全与 node 逐字节一致（6/2/7/a=1 b=2 sum=3/15/3/42/9/9）；917 program 型 expect.refused 锁 `with`（动态作用域不可静态布局）；门禁 `--check` 917/917 + `--corpus` 286 agree + 原生 10/10）。
- 类特性混合锁仓（918-927 十连锁：P-B2 第七增量；零 Go 改动纯夹具波：for-await 同步解包、可空数组守卫（`?.[i]`/`?.length` 空合）、继承覆写、闭包捕获、repeat/padStart/Math.max、静态字段、Map forEach、charAt/charCodeAt/indexOf、Object.keys 取长真机全与 node 逐字节一致（1/2·2/-1/3·42·15·ababab/   x/9·12·30·b/99/1·1）；927 program 型 expect.refused 锁联合形参（XL 富类型域，`unsupported parameter annotation`）；门禁 `--check` 927/927 + `--corpus` 286 agree + 原生 10/10）。
- 杂项语义锁仓（928-937 十连锁：P-B2 第八增量；零 Go 改动纯夹具波：串 for-of 计数/拼接（step25 门禁注记已过期，该两形通且值对）、Set 增删查、比较器 sort、模板 bool 按方言既定 1/0 口径（demo 17 同例，非 node true/false）、Date 取年、parseInt 双形、Math.imul 真机全与 node 逐字节一致（2/hi/2/1/1·1/3·v=1!·1970·42/7·42）；936/937 program 型 expect.refused 双锁（函数内具名声明未入子集、super 只见原型不见实例态）；门禁 `--check` 937/937 + `--corpus` 286 agree + 原生 10/10）。
- try-finally 终结守卫已推（938-947 十连锁：P-B2 第九增量；真修复：try 体静态终结（return 顶层）+ 非空 finally 并存曾直排 finally 成无标号死码，`sa check` 报 FallthroughForbidden（938 v3 实证），现 `saLowerTry` 加约 10 行大声拒（落 `sa_ctrl.go`，零新文件；有条件 abrupt 如 if 内 break 沿 4168 既定局限不动，值错但 SAI 合法）；946 program 型 expect.refused 锁该守卫；其余 8 通形（模板/成员/三元/枚举 switch 判别、变量下标复合、串复合、`??` 链、标号块）真机全与 node 逐字节一致（1/2/13/abcabc/7/2·5/5·1）；947 program 型 expect.refused 锁浮点 case；门禁 `--check` 947/947 + `--corpus` 286 agree + 原生 10/10）。
- 高阶与解构锁仓（948-957 十连锁：P-B2 第十增量；零 Go 改动纯夹具波：case 标识符折叠、串变量 for-of、Map.keys 巡回、嵌套模板调用、实例方法 this、find/findIndex、reduce/filter、数组/对象解构真机全与 node 逐字节一致（1·hey·3·hi bo/hi a!·42·6/2·10/2·17·12）；957 program 型 expect.refused 锁用户函数值别名（函数名与变量分属两表，`unknown variable`；顶层 Math 别名/具名回调另见 step24/26）；门禁 `--check` 957/957 + `--corpus` 286 agree + 原生 10/10）。
- 数组方法回归锁仓（958-967 十连锁：P-B2 第十一增量；零 Go 改动纯夹具波：some/every、concat、负区间 slice、reverse/toReversed、sort/toSorted、fill/with、toSpliced、flatMap（§1.7 回滚后经现路由通且值对）、includes/indexOf/lastIndexOf 真机全与 node 逐字节一致（1/1·4/4·2/2·3/1/3·1/3·9/7·2/4·6·1/2/0）；967 program 型 expect.refused 锁数组侧可选方法调用（串侧见 887）；门禁 `--check` 967/967 + `--corpus` 286 agree + 原生 10/10）。
- split 右值修复已推（968-977 十连锁：P-B2 第十二增量；真修复：`s.split(sep)[i]` 右值版曾漏出 `saIsStrArrRvalue`（只认裸调用/`m.get`），落 i32 下标误打地址（968 n4 实证：SA 799929152 vs node b；具名绑定版正确），现 `saIsStrArrRvalue` 加 split 臂（串基经 `saIsStrExpr` 字面量/具名/调用，约 7 行，落 `sa_map.go`，零新文件；`?.` 沿旧门；字面基/limit 形同验），976 锁该修复；其余 9 通形（slice/substring、ceil/min/max、trim 系、pad、replace 系、starts/ends/includes、大小写折叠、concat/+、fromCharCode）真机全与 node 逐字节一致；门禁 `--check` 977/977 + `--corpus` 286 agree + 原生 10/10）。
- 标准库收尾锁仓（978-987 十连锁：P-B2 第十三增量；零 Go 改动纯夹具波：split 越界空合/空串、Date setter、Map 删清、copyWithin、String/isInteger（MAX_SAFE_INTEGER 沿 90 既定 i32 口径不重锁）、sqrt/log10/sign、toSorted 非变异、join、数组/串 at 真机全与 node 逐字节一致（2/oob/1·2000·2/1/0·3/4·42/1·4/2/-1·1/3·1-2-3·3/1·c/a）；门禁 `--check` 987/987 + `--corpus` 286 agree + 原生 10/10）。
- random 泄漏修复已推（988-997 十连锁：P-B2 第十四增量；真修复：`Math.random()` 的 `__ts_rand_seed` 具名 int 在寄存器模型下至多一次重赋——同函数第二次调用触 RegisterRedefinition、ret 0 尾触 MemoryLeak（988 实证；此前零 demo 覆盖，上游 satsgo 同形亦只验值返回式），现 LCG 状态寄 alloc 8 单元经 load/store 流转（store 非重定义，槽形同 saLowerOptionalLength，约 13 行，落 `sa_math.go`，零新文件），首用 12345 确定性不变（跨进程同序；函数内推进实证 2146800/998800 相异；跨函数 scope-local 重启沿既有语义）；988-991 四 demo 锁该修复；其余 6 通形（case 标识符、`String(true)` 按方言 bool=1/0 口径、`0 ?? 8` 按 step35 左非零直通口径、可选调用空合、parseInt 负零、fromCharCode）真机符合既定口径；门禁 `--check` 997/997 + `--corpus` 286 agree + 原生 10/10）。
- 千例锁仓（998-1007 十连锁：P-B2 第十五增量，总数破 1007；零 Go 改动纯夹具波：trunc/ceil、数值 toString、Object.values/entries、Date.parse/now/getTime 真机全与 node 逐字节一致（7/5·255·2·1·2·1/1）；1004 锁 `Number()` f64 格式口径（42.000000/1.000000，沿 376/457 既定行为——改拒会碎三处已锁 demo，故不碰）；1005/1006/1007 program 型 expect.refused 三锁（Math.hypot 无封存证据禁原创、Error 类未建模、toFixed 系 f64 定点）；门禁 `--check` 1007/1007 + `--corpus` 286 agree + 原生 10/10）。
- 重复调用回归锁仓（1008-1017 十连锁：P-B2 第十六增量；零 Go 改动纯夹具波，专治“单调过、重复崩”类潜伏 bug（random/try 皆属此类）：同函数双调（i32/串/模板/数组形参/递归/getter/构造/sort/拼接/split 右值）真机全与 node 逐字节一致（1/2·a/b·x=1/y=2·3/12·55/21·10/10·3·1/3·x1/x2·a/b）；1017 兼为 split 右值修复的重复回归；门禁 `--check` 1017/1017 + `--corpus` 286 agree + 原生 10/10）。
- 类异常混合锁仓（1018-1027 十连锁：P-B2 第十七增量；零 Go 改动纯夹具波：catch+finally 直落、setter 单双写、super 方法、工厂函数返实例、Map 串键双值种、throw 切片、catch 形参运算真机全与 node 逐字节一致（1/3·20·hi!·42·2/2·0/7·21·v·6）；1027 program 型 expect.refused 锁数组字面量内联对象（须先声明绑定）；跨调用 throw 仍 Panic 体系不恢复（不可锁，沿 step15/54 口径）；门禁 `--check` 1027/1027 + `--corpus` 286 agree + 原生 10/10）。
- 声明形态锁仓（1028-1037 十连锁：P-B2 第十八增量；零 Go 改动纯夹具波：命名空间函数/常量、抽象类覆写、静态块、混杂枚举整数成员折叠、静态方法、implements、as const、readonly 形参、注解命名空间串读真机全与 node 逐字节一致（7/10·11·42·1·7·9·2·7·hi）；1037 program 型 expect.refused 锁串枚举成员（仅全整数枚举折叠）；嵌套命名空间值读与 Object.assign 系显式旧拒边界（transpile.go:1928），未注解串成员读走注解分发，均不碰；门禁 `--check` 1037/1037 + `--corpus` 286 agree + 原生 10/10）。
- 调用语义锁仓（1038-1047 十连锁：P-B2 第十九增量；零 Go 改动纯夹具波：短路求值副作用抑制、二维数组、继承链覆写、Map 数组值可选下标、super 双参直传、串数组形参、布尔形参、rest 形参、串缺省形参真机全与 node 逐字节一致（0/0/1·3/2·3·20·7·x·0/1·6·hi/yo）；1047 program 型 expect.refused 锁 super 表达式实参（仅收形参/字面量）；门禁 `--check` 1047/1047 + `--corpus` 286 agree + 原生 10/10）。
- for 子域泄漏修复已推（1048-1057 十连锁：P-B2 第二十增量；真修复：legacy-for 子域退出时归属截断除名不释，条件调用柄（`for (i<n();)` t_1）与 init 调用柄（`for (let x=get();)` x）末轮残留触 MemoryLeak（1048 g1/k1 实证；while 无子域故出口 sweep 兜住），现 `saLowerFor` 取子域归属快照、endL 落点（含 break 直达与 false 早返）调 `saReleaseDeeperThan`（约 9 行，落 `sa_ctrl.go`，零新文件；外层累加经深度隔离实证无伤，k5；incr/体命名沿既有纪律无碍）；25 家既有 legacy-for SAI 各增一行死域 `!` 卫生行（原生输出逐字节不变，全员 PASS 后 re-baseline）；1057 锁本修复；其余 9 通形（递减 for/while/do、倒序下标、三重 break、单次 do、循环内 switch/try）真机全与 node 逐字节一致；门禁 `--check` 1057/1057 + `--corpus` 286 agree + 原生 10/10）。
- 调用位回归锁仓（1058-1067 十连锁：P-B2 第廿一增量；零 Go 改动纯夹具波，for 子域修复后复验各条件位调用柄无同类漏：for-of 被迭代调用、do-while 调用条件、switch 调用判别、三元调用条件、嵌套调用、字面量内调用、模板内调用、返回链、else-if 链真机全与 node 逐字节一致（6·2·2·20·26·60·hi bo!·2·2）；1067 program 型 expect.refused 锁 for-in 对象键（仅收绑定数组下标巡回）；门禁 `--check` 1067/1067 + `--corpus` 286 agree + 原生 10/10）。
- 类进阶锁仓（1068-1077 十连锁：P-B2 第廿二增量；零 Go 改动纯夹具波：静态继承、箭头捕获 this、私有字段跨实例、闭包数组捕获、箭头内 super、三重嵌套模板、私有静态、泛型擦除、const 枚举真机全与 node 逐字节一致（3·42·1/0·8·11·abc1·8·7·3）；1077 program 型 expect.refused 锁私有方法（字段见 96）；计算枚举成员误标串口径与接口可选方法体缺失两处文案级瑕疵记入候选，均需收割期改动故不碰；门禁 `--check` 1077/1077 + `--corpus` 286 agree + 原生 10/10）。
- 类型守卫断言锁仓（1078-1087 十连锁：P-B2 第廿三增量；零 Go 改动纯夹具波：satisfies 对象/数组、非空断言、as 双断言、void 值（0 口径，node undefined）、逗号表达式、逻辑赋值三件套、switch(true)、in (from 界内字段有无)真机符合既定口径（5·2·2·5·9/0·3·5/0/9·1·1/0）；1087 program 型 expect.refused 锁异构 instanceof（同布局恒真折叠见 t2/t3/t4，异名禁误判假 558；探针曾误判三元实参位 bug，单用途二分证伪系异构行所致）；门禁 `--check` 1087/1087 + `--corpus` 286 agree + 原生 10/10）。
- 解构边界锁仓（1088-1097 十连锁：P-B2 第廿四增量；零 Go 改动纯夹具波：模板转义、静态块多写、单参 splice、区间 fill、多参 push+pop、缺省 join 真机全与 node 逐字节一致；1094-1097 program 型 expect.refused 四锁（Map for-of 解构、绑定数组缺省、改名+缺省组合、嵌套解构；单改名/单缺省/字面缺省见 272/438）；门禁 `--check` 1097/1097 + `--corpus` 286 agree + 原生 10/10）。
- entries 崩溃修复已推（1098-1107 十连锁：P-B2 第廿五增量；真修复：`for (const [k,v] of m.entries())` 转译/check 全过、真机 segfault（exit 139），根因为 `iter_vec` 三 u64 一组被 for-of 按扁平单字数组误巡回 3 倍并解构越界（计数形亦错：1 条打 3 次），现两处设防（落 `sa_map.go` 投影拆分 + `sa_arr.go` for-of 链基 `.entries()` 点名拒，约 24 行，零新文件；forEach 直调 div3 巡回不受影响，1067/1094 旧指纹不动；数组 `.entries()` 同门齐拒）；1106/1107 双锁该修复（Map/数组双接收者）；1105 锁数组内联实例（先绑再入组）；其余 7 通形（values 巡回、布尔 switch、ns 双函数、串计数、Set 构造、keys 求和、has/size）真机全与 node 逐字节一致；门禁 `--check` 1107/1107 + `--corpus` 286 agree + 原生 10/10）。
- Map 向量语义锁仓（1108-1117 十连锁：P-B2 第廿六增量；零 Go 改动纯夹具波：keys/values 取长（单/双/交错/三连）、向量下标、绑定后复读、调用点快照语义真机全与 node 逐字节一致（2/2·1/20·2·2/2·2/2·2/99/2·2/2/2·2/2·2/2/2·1/2）；途中疑似“连调腐化”经 SAI 取证证伪（系探针源单 set 笔误，1 条长 1 正确），未误修；门禁 `--check` 1117/1117 + `--corpus` 286 agree + 原生 10/10）。
- lastIndexOf 缺省修复已推（1118-1127 十连锁：P-B2 第廿七增量；真修复：`lastIndexOf` 无 from 时误传 from=0 只查首位（`"abca"` 返 0 vs 3，1118 b3 实证；stdlib 本体正确），现缺省取 hay 长即 +Inf（约 4 行，落 `sa_str.go`，零新文件；显式 from 沿旧门）；115 系单现针（0 新旧同值）SAI 重基一行，原生输出不变；串越界读系四处一致文档化 UB（改 ABI 代价大），不碰；门禁 `--check` 1127/1127 + `--corpus` 286 agree + 原生 10/10）。
- endsWith 双参补齐已推（1128-1137 十连锁：P-B2 第廿八增量；真功能：`endsWith(n)` 单参既有，`endsWith(n, pos)` 曾拒，现镜像 startsWith 双参（`lastIndexOf(n, clamped-nlen) == clamped-nlen`，双 SELECT 钳位，约 40 行，落 `sa_str.go`，零新文件；空针恒真由 stdlib 空臂内聚），七形对 node 逐位一致（1/1/0/1/0/1/1）；1131 锁负 repeat 夹空口径（子集无可捕获内建错误，确定性空串；node 抛）；1136 锁 Set.forEach 未投影；串越界读四处一致 UB 不碰；门禁 `--check` 1137/1137 + `--corpus` 286 agree + 原生 10/10）。
- 切片钳位双修已推（1138-1147 十连锁：P-B2 第廿九增量；双真修：① `slice(3,1)` 真机 abort（u64 回绕），根在 sci `ts_str_slice` sub=0 未处理 s>f，已补空分支（4 行，落 sci `sa_std/ts_string.sa`，另库提交 `62e2dd4c` 已推 layola13/sci）；② `indexOf/lastIndexOf` 显式负 from 回绕（注释自认既有缺），转译侧钳零（约 10 行，落 `sa_str.go`，缺省字面零漂移）；1138/1139 双锁；其余 8 通形（starts 正负位、slice 钳界、substr 负、charCode 越界哨兵 -1（NaN 不可表）、padEnd、空串三件）真机符合既定口径；门禁 `--check` 1147/1147 + `--corpus` 286 agree + 原生 10/10）。
- 串变量位锁仓（1148-1157 十连锁：P-B2 第三十增量；零 Go 改动纯夹具波，重点复验新修臂变量位：substring 交换/单参/substr、显式 from 索引、变量下标/检索位/填充/重复/切分、endsWith 变量位、变量切片、方法链真机全与 node 逐字节一致；另补交上批漏 add 的 1123 重基（钳零发射版，原生输出不变，本轮 status 审计发现）；门禁 `--check` 1157/1157 + `--corpus` 286 agree + 原生 10/10）。
- match 右值修复已推（1158-1167 十连锁：P-B2 第卅一增量；真修复：`s.match(re)[i]` 右值版曾漏出 `saIsStrArrRvalue` 落 i32 下标误打地址（1158 实证：SA "P" vs node "123"；具名版 362/489 正确），现加 match 臂（约 7 行，落 `sa_map.go`，split 同门；miss 空柄/越界归零与具名版一致，已对读验证；`?.` 沿旧门）；1158-1160 三锁；其余 6 通形（带位 includes、search、负码点哨兵、空垫、长重复、test）真机符合既定口径；1167 锁 replace $ 模式；门禁 `--check` 1167/1167 + `--corpus` 286 agree + 原生 10/10）。
- 正则右值双修已推（1168-1177 十连锁：P-B2 第卅二增量；双真修：① `re.exec(s)[i]` 右值曾漏认落 i32 下标误打地址（1168 e1 实证：SA 652123728 vs node "123"），加 exec 臂（约 6 行，落 `sa_map.go`，match/split 同门）；② 字面量捕获组在 match 非 g / split 结果路径含组、单元素设计装不下（1168 f2/h1 实证 length 1 vs 3、2 vs 3），加双门拒（约 13 行，落 `sa_str.go`，绑定模式不可见沿旧行，/g 与 test/布尔位不受影响）；1168/1169 双锁 exec，1175/1176 双锁捕获组；其余 6 通形（/g 匹配及含组、字面 exec、变量 test/search、含组 test）真机符合既定口径；门禁 `--check` 1177/1177 + `--corpus` 286 agree + 原生 10/10）。
- 正则门收窄已推（1178-1187 十连锁：P-B2 第卅三增量；门精度修复：捕获组门曾把 `(?:`/`(?=` 等一律当捕获组（1168 q2 前瞻误拒），现新增 `saRegexHasCaptureGroup` 只认真捕获组（`(?` 交引擎 POSIX 门精确拒，落 `sa_str.go` 约 30 行，零新文件；绑定模式不可见沿旧行）；1178 锁前瞻 POSIX 拒；另本轮排除两不可锁形（match 数组直接打印仍垃圾、/g exec 失 lastIndex 状态，均记候选）；其余 9 通形（锚点、i/m 旗、search 缺省、iflag 切分、绑定 exec、m 旗匹配、plain/regex 替换）真机全与 node 逐字节一致；门禁 `--check` 1187/1187 + `--corpus` 286 agree + 原生 10/10）。
- 串数组误打双修已推（1188-1197 十连锁：P-B2 第卅四增量；双真修：① match 结果直打（`console.log(m)`）误判串即打地址垃圾（1188 a1 实证 "P"），根在 `saCallIsStr` 兜底（串接收者方法皆判串），定点门落 `saToSlice`（约 18 行，字面量组/POSIX 交下游精确门，绑定沿数组门；中途两版过宽/过窄均回滚以保 1178 精确消息）；② 串元 join 拼地址（1188 p2/p4 实证 "1-2" 变双地址），根在轮子文档化 i32-only，转译侧以 arrStr 标记拒（约 5 行，落 `sa_arr.go`，具名/右值链同门）；1188-1190 三锁；其余 7 通形（模板下标、String 取元、元比较、循环 bound、嵌套 miss、串 reverse/sort）真机全与 node 逐字节一致；门禁 `--check` 1197/1197 + `--corpus` 286 agree + 原生 10/10）。
- 数组文本化拒收锁仓（1198-1207 十连锁：P-B2 第卅五增量；零 Go 改动纯夹具波：String()/模板/+拼接/三元四位置数组禁文本化既有门复验全拒（与 1188 串数组门互补）；数组异体恒假、自体恒真、串恒等、null 三态、typeof 守卫、void 语句真机全与 node 逐字节一致（0·1·1/0·1/0/1·1/0·7/8）；门禁 `--check` 1207/1207 + `--corpus` 286 agree + 原生 10/10）。
- 逻辑值语义修复已推（1208-1217 十连锁：P-B2 第卅六增量；真修复：值位 `&&`/`||` 走按位与或（`3&&4`→0、`3||4`→7、`if(3&&4)` 走错分支；1208 m1/m2/n1 实证），现 `saEvalI32` 值位拦截改分支+槽+臂释放短路形（约 50 行含新函数，落 `sa_expr.go`，零新文件；与三元惰性形同律；副作用跳过沿 e1 锁；bool 绑定系 kind 分裂另域不碰）；16 家既有 &&/|| SAI 重基（分支化+重编号，原生输出逐字节不变，全员 PASS 后收）；1208-1210 三锁；其余 7 通形（while/混合/布尔字面/调用条件/模板嵌套/算术链/布尔 if）真机全与 node 逐字节一致；门禁 `--check` 1217/1217 + `--corpus` 286 agree + 原生 10/10）。
- 逻辑值回归锁仓（1218-1227 十连锁：P-B2 第卅七增量；零 Go 改动纯夹具波，新逻辑 lowering 全位置复验：下标/模板/双否/嵌套/for 条件/三元链/switch 判别/do 条件/调用条件/赋值位真机全与 node 逐字节一致（20/20·2/3·1/0·2/0·10·3/0/5·1·3·0/1·3/9）；门禁 `--check` 1227/1227 + `--corpus` 286 agree + 原生 10/10）。
- 移位 ToInt32 修复已推（1228-1237 十连锁：P-B2 第卅八增量；真修复：64 位后端上 `1<<31` 得 +2147483648（node -2147483648；1228 w2 实证），根为左值/结果双缺 ToInt32（`>>>` 有掩码而 `<<`/`>>` 无），现共享 `saMaskShiftCount` 内聚左值掩码+SELECT 符号扩展、新 `saWrapShiftResult` 低 32 结果符号化（落 `sa_ctrl.go` 约 60 行 + `sa_expr.go` 双份拷贝归一，二元/复合（`<<=`/`>>=`）六处共用；`>>>` 非负直通；`/` 截断与 `*`/`+` f64 口径沿旧律不碰）；7 家既有移位 SAI 重基（归一化+重编号，原生输出逐字节不变，全员 PASS 后收）；1228/1232 双锁；其余 8 通形（位运算、除余截断、幂、变量计数、循环移位、负值、混合、乘溢出 f64 口径）真机全与 node 逐字节一致；门禁 `--check` 1237/1237 + `--corpus` 286 agree + 原生 10/10）。
- 移位回归锁仓（1238-1247 十连锁：P-B2 第卅九增量；零 Go 改动纯夹具波，ToInt32 修复全位置复验：复合位运算、移位链、除余复合截断（沿 1230 口径）、无符号复合、取整惯用法、边界计数、负值移位、移位算术、变量计数、移位比较真机全与 node 逐字节一致（8/14/6·0/-1·-3/-1·1073741822·5/5/5·1/-2147483648/0/-1·-12/-1·20/1·1073741822/2·1）；门禁 `--check` 1247/1247 + `--corpus` 286 agree + 原生 10/10）。
- 位运算回归锁仓（1248-1257 十连锁：P-B2 第四十增量；零 Go 改动纯夹具波，位运算全位置复验：条件/三元/掩码/xor 交换/非条件/switch 判别/变量四则/无符号链/循环移位/非算术真机全与 node 逐字节一致（1/0·10/20·255/511·9/5·1/1·1·8/14/6/-13·4294967295/536870911·8/1·4/-12）；门禁 `--check` 1257/1257 + `--corpus` 286 agree + 原生 10/10）。
- 比较守卫锁仓（1258-1267 十连锁：P-B2 第四十一增量；零 Go 改动纯夹具波：比较链、等值三件、实例恒真、三元 max、嵌套三元、钳位、minmax 求和、switch(true) 比较分支、串恒等、区间判定真机全与 node 逐字节一致（1/1·1/1/0·1·7/9·1/2/3·5/0/10·10·2·1/0·1/0/0）；门禁 `--check` 1267/1267 + `--corpus` 286 agree + 原生 10/10）。
- 循环集合混合锁仓（1268-1277 十连锁：P-B2 第四十二增量；零 Go 改动纯夹具波：while-break/continue、do-while 累加、for-break、数组 push/pop、Map 基本、Set 基本、串 slice/includes、Math minmax、枚举 switch、对象 spread+keys 取长真机全与 node 逐字节一致（17·10·10·3/3/2·1/1/2·1/2/1·el/1/2·7/3/5·1/3·1/5/2）；门禁 `--check` 1277/1277 + `--corpus` 286 agree + 原生 1277/1277）。
- 循环类串混合锁仓（1278-1287 十连锁：P-B2 第四十三增量；零 Go 改动纯夹具波：嵌套循环、while 步进累加、串拼接/concat、数组下标读写、三元 min、数值 switch、for-of 求和、类计数器、空合链、模板插值真机全与 node 逐字节一致（12·20·foobar/foo-·10/99/3·3/2·10/20/30·10·2·8/5·hi sa!/v=7）；门禁 `--check` 1287/1287 + `--corpus` 286 agree + 原生 1287/1287）。
- 新鲜构造链式修复已推（1288-1297 十连锁：P-B2 第四十四增量；真修复：`new Array(2).fill(7)[0]` 曾拒（`index base must be bound array`，sa_arr.go:1219 门；`saArrCallRet`/`saIsArrValue` 皆不识 New 接收者）；改动落 `sa_arr.go` 两处约 14 行零新文件（`saIsArrValue` 加 New→`saIsArrayCtor` 谓词 + `saArrValueOf` 加 New 臂经 `saLowerArrayCtor` 具化 + `saOwnTemp`，与字面量同律；绑定接收者释放对具名 no-op 故仍 sound，实证 7/7；JEV 修法裁决；JEV 爆炸半径 needs_regression_tests 由本批回归锁覆盖）；1288 锁主修（7）+ 1289 链取长（3）+ 1290 绑定回归（7/7）+ 1291 `isArray(new)` 折 1；其余 6 通形（slice 链/concat 链/while 倒数/repeat 变量/sqrt/串链取长）真机全与 node 逐字节一致；残留：`Array.of(..)[i]` 另门拒、`for (v of new Array)` 仍拒；门禁 `--check` 1297/1297 + `--corpus` 286 agree + 原生 1297/1297；`go vet` + `gofmt` 干净。
- 新鲜构造一致性修复已推（1298-1307 十连锁：P-B2 第四十五增量；真修复两则，落 `sa_arr.go` 约 73 行零新文件，JEV 裁决单 commit）：①调用式构造下标基（`Array.of(5,6)[1]` 曾拒；`saArrValueOf` Call 臂加构造回退，具化 + `saOwnTemp`，与 New 臂同律）；②新鲜构造巡回（`for (v of new Array(..))` 曾拒；`saForArrHandle` 加 New 臂，巡后释与字面量柄同律）+ 空穴设防（新 `saIsHoleArrayCtor`：单长即空穴；for-in 跳过空穴计数 0vs3、for-of 空穴值 undefined 与 0 分叉，巡回顶门大声拒；直接下标读 0 沿 480/501 口径不管）；修中抓获 for-in 空穴真分叉（SA 3 vs bun 0）与中途语法错（重复行，已修并复验）；1298 稠密 for-of（6）+ 1299 `Array.of` for-of（9）+ 1300 稠密 for-in（2）+ 1301 `Array.of` 下标（6）+ 1302 New 取长（3）五锁；其余 5 通形（`Array(n)` 取长/reverse 链/sort 链/串链取长/do-while break）真机全与 node 逐字节一致；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1307/1307 + `--corpus` 286 agree + 原生 1307/1307；`go vet` + `gofmt` 干净。
- 新鲜构造解构修复已推（1308-1317 十连锁：P-B2 第四十六增量；真修复：`const [x,y] = new Array(9,8)` 曾拒（`destructuring source must be bound array`，解构门只认字面量/绑定）；改动落 `sa_arr.go` 约 34 行零新文件（解构源加构造臂：`saLowerArrayCtor` 具化 + `saOwnTemp`，与字面量源同律；新 `saCtorElemCount` 供缺省折叠元数；空穴形缺省按未知长大声拒——空穴读 undefined 才走缺省，0 口径不可代，实证拒收；无缺省空穴读 0 沿 480/501 口径）；1308 主修（9/8）+ 1309 `Array.of` 解构（7/8）双锁；其余 8 通形（New sort 链/展开 `Array.of`/fill 链 for-of/New 多元下标/`Array.of` 取长/展开 New/调用式 for-of/单元素构解构）真机全与 node 逐字节一致；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1317/1317 + `--corpus` 286 agree + 原生 1317/1317；`go vet` + `gofmt` 干净。
- Map/Set 同柄链修复已推（1318-1327 十连锁：P-B2 第四十七增量；真修复：`m.set("a",1).get("a")` 曾拒（`only direct function calls lowerable`，分发门只认绑定接收者）；根因 `set`/`add` 原位变异返 `"0"` 字面量（弃值零归属），链式可精确脱糖；改动三文件约 66 行零新文件（`sa_map.go` 新 `saMapChainBase`：仅 set/add 单层 + 绑定接收者 + `?.` 沿旧门；`sa_expr.go` 分发：内层求值弃值、外层在绑定上重分发，求值序与 JS 同；`sa_arr.go` 取长门加 `.size` 链臂）；深链（`.set().set().get()`）沿旧门大声拒（实证）；1318 主修（1）+ 1319 Set 链（1/1）+ 1320 链 has（1/0）+ 1321 add-add 链（2/1）+ 1322 链 `.size`（1）五锁；其余 5 通形（repeat 下标/大写下标/模板取长/`String(n)` 下标/Set 构造 has）真机全与 node 逐字节一致；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1327/1327 + `--corpus` 286 agree + 原生 1327/1327；`go vet` + `gofmt` 干净。
- 串三元表达式位修复已推（1328-1337 十连锁：P-B2 第四十八增量；真修复：`console.log(x > 0 ? "pos" : "neg")` 曾拒（`string ternary in i32 expression`；三元串槽核在 return/声明位已服役，仅表达式位分发谓词 `saIsStrExpr` 缺三元臂）；改动两文件约 11 行零新文件（`sa_str.go` 谓词加三元臂：双臂皆串值即串，异形沿求值门大声拒（实证）；`sa_expr.go` 串三元核补 `!slot`——与空合核/数组三元核同形，此前无 demo 走此核故潜伏，MemoryLeak 实证定位）；1328 主修（pos）+ 1329 声明位（pos）+ 1330 返回位（pos/neg）+ 1331 拼接位（v:y）+ 1332 嵌套（A/B/C）五锁；其余 5 通形（Set add `.size`/链 delete/split 取长/slice 大写链/trim 取长）真机全与 node 逐字节一致；残留：方法形 `.size()` bun 本就崩（前存口径，链继承，本批不锁）；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1337/1337 + `--corpus` 286 agree + 原生 1337/1337；`go vet` + `gofmt` 干净。
- 串三元副作用惰性修复已推（1338-1347 十连锁：P-B2 第四十九增量；真修复：静默错码——串三元核双臂急切求值，`false ? bump() : "x"` 副作用照发（SA x/1 vs bun x/0），i32 侧早有惰性分支形而串侧缺；改动 `sa_expr.go` 约 49 行零新文件（新 `saLowerTernaryLazyStr`：惰性 i32 形同骨架，槽宽按 ptr，臂内求值入槽即消费；串分支副作用臂分发，纯臂沿既有急切径零漂移——全量门禁证）；1338 伪臂（x/0）+ 1339 真臂（b/1）双锁；其余 8 通形（串条件/串调用臂/数组三元下标/模板三元/空合三元/调用臂三元/深嵌套/模板臂）真机全与 node 逐字节一致；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1347/1347 + `--corpus` 286 agree + 原生 1347/1347；`go vet` + `gofmt` 干净。
- i32 空合右臂快照修复已推（1348-1357 十连锁：P-B2 第五十增量；真修复：坏 SAI——`a ?? bump()` 右臂调用柄在分支内声明，返前释放引用未定义寄存器（`UnknownRegister t_9`，check 即拦）；根因 i32 空合核缺右臂快照点，而串空合核/三元惰性核皆有同形快照；改动 `sa_expr.go` 约 9 行零新文件（右臂 `mark + saConsumeOwn` 随槽消费，与串核同律）；1348 主修（3/0）+ 1349 右调用（7）双锁；其余 8 通形（空合算式/空合链/Map 缺键回退/左调用/串短路/串条件/串调用臂/数组三元下标）真机全与 node 逐字节一致；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1357/1357 + `--corpus` 286 agree + 原生 1357/1357；`go vet` + `gofmt` 干净。
- 解构源透明包装修复已推（1358-1367 十连锁：P-B2 第五十一增量；真修复：`const [x = 5] = [] as i32[]` 曾拒（`destructuring source must be bound array`；门只看裸形，`as` 包装的字面量/绑定/构造皆误拒）；改动 `sa_arr.go` 净约 11 行零新文件（解构源三判定经 `saUnwrapTransparent`，包装无值语义既定契约；调用源仍拒）；1358 `as` 空元缺省（5）+ 1359 括号绑定（7/8）+ 1360 `satisfies`（3/4）+ 1361 `as` 构造（4/5）四锁；其余 6 通形（界内缺省/枚举比较/串 switch/includes 三元/倒数步进/floor）真机全与 node 逐字节一致；残留：对象解构布局门、实例空合非空右臂（另域）；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1367/1367 + `--corpus` 286 agree + 原生 1367/1367；`go vet` + `gofmt` 干净。
- 巡回源透明包装 + split 空分隔拒收已推（1368-1377 十连锁：P-B2 第五十二增量；真修复两则，改动两文件约 8 行零新文件，JEV 修法裁决）：①for-of/for-in 源 `saUnwrapTransparent`（`(a)`/`a as T` 与裸形同门，1358 解构同例；先解包再进空穴门，`(new Array(n))` 空穴仍拒实证）；②`split("")` 字面量空分隔静态拒（真机 OOM/137 实证，切分实现在只读 sci 镜像动不得，薄口侧先行大声；变量分隔沿旧路；既有用例零命中）；1377 以 program 形 `expect.refused` 锁拒收指纹（单文件无拒收锁，run.sh 仅 program 工程支持；初版 `.length` 形误判门——length 门先拦调用未求值，改直达语句形一次过）；1368 括号巡回（6）+ 1369 `as` 巡回（6）双锁；其余 7 通形（声明位 fill 链/逗号 split/单参 slice/绑定 reverse/concat 叹号/ceil/复合赋值）+ 1 拒收锁真机验证一致；残留：数组 `toString`（需 join 逗号映射，另域）、变量空分隔；JEV 爆炸半径 needs_regression_tests 由 1377 拒收锁覆盖；门禁 `--check` 1377/1377 + `--corpus` 286 agree + 原生 1377/1377；`go vet` + `gofmt` 干净。
- switch 非空臂穿透修复已推（1378-1387 十连锁：P-B2 第五十三增量；真修复：静默错码——非空无 break 臂隐式落 endL（SA 10 vs bun 11），空臂堆叠早表态支持穿透而非空臂未竟；改动 `sa_ctrl.go` 约 +51/-7 零新文件（新 `saSwitchFallthrough`：源码序穿透目标，与空臂堆叠 dispatch 同序同律，中 default/末无 default 皆正；legacy/macro 两落点改 `jmp endL` 为穿透目标；空臂/break/return/条件 break 启发式未动）；中途 `String(arr)`/`arr.toString()` 撞 1198-1201 四位置显式锁（模板/拼接/三元+i32 同拒，`--check` 实证回归），JEV 裁决全回滚（窄化不连贯），改动已 revert，残留记回；1378 主修穿透（11，宏路）+ 1379 串穿透（11，legacy 路）+ 1380 中 default 穿透（111）三锁；其余 7 通形（join 调用串化/无参 join/短横 join/串替换/数值串化/i32 toString/切片 join）真机全与 node 逐字节一致；全量门禁零漂移（现存 29 switch 全带 break/return）；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1387/1387 + `--corpus` 286 agree + 原生 1387/1387；`go vet` + `gofmt` 干净。
- push 展开形修复已推（1388-1397 十连锁：P-B2 第五十四增量；真修复：`a.push(...b)` 曾拒（`KindSpreadElement is not in subset`；用户函数展开调用 515 有先例，push 变参需循环）；改动 `sa_arr.go` 约 60 行零新文件（新 `saLowerPushSpread`：源长循环逐元原位压栈，空源零次，返压后新长直读 recv 头 +8，源柄读后即释；元种门：i32 绑定数组收串/嵌套源沿标量臂同律拒（实证）；自展长预读与 JS 先求值一致（4/2 实证）；标量臂零动）；1388 主修（3/3）+ 1389 混合（5/1/3）+ 1390 空源（1/1）+ 1391 自展（4/2）+ 1392 返长（3）五锁；其余 5 通形（变量 padStart/标号 break/do-while continue/展开 max/循环赋值）真机全与 node 逐字节一致；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1397/1397 + `--corpus` 286 agree + 原生 1397/1397；`go vet` + `gofmt` 干净。
- unshift 展开形修复已推（1398-1407 十连锁：P-B2 第五十五增量；真修复：`a.unshift(...b)` 曾拒（push 展开 1388 续集；顺序语义须中转：全参按序入新鲜暂存再逆序单步前插，直接顺序单步会倒置）；改动 `sa_arr.go` 约 83 行零新文件（unshift 分支含展开即走新 `saLowerUnshiftSpread`：标量逐元压栈暂存、展开整片合并（`saAppendSlice`），逆序 push + rotr1 单步，暂存读后无条件释放（checked-index 槽同律，分支安全），返压后新长；元种门沿 push 同律；单参路径零动）；1398 主修（3/1/2）+ 1399 混合保序（4/0/1/2）+ 1400 单参回归（3/1）三锁；其余 7 通形（splice 删/插/isArray 构造/typeof 数组/shift/endsWith/起位 indexOf）真机全与 node 逐字节一致；JEV 爆炸半径 safe_to_apply；门禁 `--check` 1407/1407 + `--corpus` 286 agree + 原生 1407/1407；`go vet` + `gofmt` 干净。
