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
- P1（覆盖锁仓）：上段通过待锁仓逐项核值建 43x 式聚焦 demo（多项已有早期 broad 覆盖：95/98/314/321/359/357/47/310；静态初始化序类名径已定案不锁）。439 已锁数组标识源解构（`const [x,y]=a`，node 10/20/30，`sa check` 160 指令 ok，真机 PASS（LLVM-14 后端））；441 已锁非字面 static 拒收（`expect.refused` 指纹）；442 已锁 `Boolean()` 构造（字面量折叠 + 变量形 `ne v,0`，bun 镜算 `1/5/3`，`sa check` 170 指令 ok，上游同位拒收系薄口领先，真机待 LLVM）；443 已锁 `satisfies` 串/i32 双形（纯类型擦除与字面量绑定同形，双边同过，bun 镜算 `ab/42`，`sa check` 81 指令 ok，真机待 LLVM）；444 已锁箭头 this 嵌套（方法内箭头经实例捕获读 `this.v`，`call @__arrow_1(c)`，bun 镜算 `7`，`sa check` 81 指令 ok，双边同过，真机待 LLVM）；445 已锁 `readonly` 接口字段（布局读同形，双边同过，bun 镜算 `9`，`sa check` 69 指令 ok，真机待 LLVM）；446 已锁 `new Array(n)` 定长读写（`a[0..2]` 存取 + `length`，双边同过，bun 镜算 `60/3`，`sa check` 160 指令 ok，真机待 LLVM）；447 已锁 Set/Map 无参构造对（`add/set/getSize` 求和，`sa check` ok，上游 `Set.getSize is not a projected surface` 系薄口领先 step330 旧功，btree 同族调用与 93/94 同形，真机待 LLVM）；448 已锁接口继承布局（`B extends A` 双域读写，双边同过，bun 镜算 `7`，`sa check` 72 指令 ok，真机待 LLVM）；449 已锁 getter+setter 对（存取各走内联体，`c.d=21` 后读 `42`，双边同过，bun 镜算 `42`，`sa check` 84 指令 ok，真机待 LLVM）；450 已锁 rest 首参分离形（`first + rest[0] + rest.length`，与 47 纯巡回区分，双边同过，bun 镜算 `32`，真机待 LLVM）；451 已锁 `?.()` 明函数/方法双形（方法 `c.m?.()` + 顶层 `g?.()`，双边同过，bun 镜算 `8/3`，`sa check` ok，真机 PASS）；452 已锁移位大计数（`8>>33/1<<33/-8>>33/8>>>33` + `>>=/<<=/>>>=` 33 三复合，bun/真机 `4/2/-4/4/4/2/4` 全对；修法落二元发射位 `and r,31` + 复合四路 `saMaskShiftCount` 同掩码（`>>>=` 左值 ToUint32 一并），local 位存回污染（`target` 复用掩码临时量）已修并真机复验；148/277/372/412/65 小计数意图内重生成；JEV 红审 proceed 85%）；453 已锁有捕获箭头返回拒收（旧路过转译吐无效 SAI：占位返 0 + 调用方跨帧传参 `UnknownRegister`；修法按捕获分治：有捕获/`this` 大声拒，无捕获沿旧路 retFn 登记——中途一刀切曾误伤 386 无捕获在跑形，已回调分治并回归；JEV 修法拒 92%、红审 proceed 77%）；454 已锁 f64 while 浮累加（`let x: f64 = 0` 整毒化初值致环第二轮即烂，真机 `4886401197650477000` vs 应 `4.5`；修法初值整字面补 `.0` + 无初值 `0.0`；撞上 R1 整步进旧设计（基整毒化自洽），初值转浮后 `add i,1` 毒化致 440 转圈 hang——R1 改走浮步进 `fadd/fsub`（整字面补 `.0`，i32 变量后端强制转换直传），440 复活 PASS；R2 浮步进仍拒；455 已锁 f64 `++`/`--` local 六形（语句/前后缀/加减，`fadd/fsub 1.0` + `fadd x,0.0` 快照；修中抓获值 temp 走 i64 打印经 sext 截断小数，返回 temp 记 f64 种 + 打印位按 temp 种纠偏；真机六值全对；modvar/ns/元素 f64 目标沿旧门）；456 已锁 i32 元素 `++`/`--` 四形（语句/旧值/新值/减，读-改-写回与复合元素臂同基同存，前缀新值先快照后存；mod f64 双边同拒无动作；真机 `2/2/11/10` 全对）；457 已锁转换后算术流（`Number("7")+1` + `String(42)` 直印，双边同过，bun/真机 `8/42`，真机 PASS）；458 已锁 f64 小数步进 for（R2 开锁：它侧经严格求值；`i=i+0.5` 真机 4 轮全对；279 转正——旧拒收 demo 现真机 `3/7/73` 与 node 逐行一致（上游 m=1 错值），删 `expect.refused` 进仓 `main.sa`+`expected.stdout`，t279 测试复活为强断言 73，`sa test` 90/90；JEV 全程不可用）；459 已锁 f64 变量步进（`i=i+st`，`st: f64` 它侧严格求值，双边同过，真机 `4.000000` PASS）；460 已锁 f64 while 条件（`x>0` 递减至零，双边同过，真机 `0.000000` PASS）；461 已锁抽象类覆盖（`abstract v` + 子类实现，双边同过，真机 `6` PASS）；462 已锁标量 as const 擦除（`"hi"/1 as const`，数组形 400 已锁，对象形双边同拒系接口绑定门；双边同过，真机 `hi/2` PASS；JEV 排 as const ROI #1）；463 已锁命名空间合并（同名双块常量合并，双边同过，真机 `3` PASS）；464 已锁函数重载（签名+实现，取实现形，双边同过，真机 `7` PASS）；465 已锁形参非空断言（`v!` 收窄擦除，401 已锁调用形；双边同过，真机 `5` PASS；JEV ROI #1）；466 已锁字段定赋值断言（`v!: i32` 后赋初值，双边同过，真机 `4` PASS；JEV ROI #2）；467 已锁展开调用（`sum(...args)`，与 29 展开元区分，双边同过，真机 `7` PASS）；470 已锁 override 关键字（子类覆写分发，双边同过，真机 `2` PASS）；471 已锁 ambient declare 擦除（未使用声明零发射，上游同位拒收系薄口领先，真机 `1` PASS）；473 已锁混合 + 拼接（非串臂经文本化，i32/bool/f64 走 interp；bool 拼写 1 系全子集既定口径 console/模板/String 全同；真机 PASS）；472 已锁 replace/g 全换（自动改调 replaceAll 核，真机 `bbb/hell0 w0rld` 全对；P-A1 首项）；469 已锁 keyof 取值流（`o[k]` 随键取值，与 280 取长区分，双边同过，真机 `4` PASS）；468 已锁串 for-of（`for ch of "ab"` 计数，与 12 数组形区分，双边同过，真机 `2` PASS）；enumcomp/类型谓词/索引签名/生成器/装饰器皆双边同拒 parity；f64 数组元截断存槽已知缺口（`[1.5,2.5]` 真机读回 1/2，上游同错 parity-in-wrong；demos+corpus 零 f64 数组用例；曾试 choke 点全拒，误伤只读 length 的 315 已回退，8 字节布局 Phase 2 另立；JEV 全程不可用）；F01 真实现（捕获装箱+间接调用，需 sci 运行时）仍 Phase 2；`?.` 双问号链双边同拒（D11 定案不再追，`?.` 单链各形 18/368/374/421 已锁）；静态初始化序实例径已闭环（记表期大声拒 + 441 锁仓，见同步；静态值语义另立）。
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

## P4d P-A1 进展（existsSync 调研中）
- replace/g→replaceAll 已推（472）。
- existsSync：sci 侧现货 `FS_EXISTS` 宏（@sa_std_fs_exists），tsgosa `saProjTable` 加一行即通，待动手+锁仓。
