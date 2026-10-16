# tsgosa 工程化缺口总纲 TODO（npm / 真包 / 后端大代码）

> 定位：本文件只收**工程化目标**的史诗级缺口（能跑真实 npm 包、后端大代码）。
> 编译器本体进度与逐步台账见 `AGENTS.md`（step ledger）与 `docs/todo.md`（历史探针 log）。
> 基线：main `11043ccc7`（0.17 已合并）；demos 2218 目录 / 有期望 2216；
> 门禁实测（zig 0.17.0 + LLVM-14 + go 1.26.0）：`--check` 2216/2216、
> 原生跑分 2209 PASS + 7 插件环境挂起、`--corpus` 286 agree、
> zod464（31 通过一致 / 零 UP过TN拒 / TN 零 panic）、Go 104 包 + fswatch 全绿。

## E1 真 npm 包链路（最大史诗）

现状（已有）：program-link 可链本地 `.ts` 与子集 `.js`——命名/默认（含匿名默认）
/命名空间（含 star 转运）/star/`export *`/`export-from`/本地名单/环境声明/
类（含跨文件 heritage）/枚举/别名/纯量/类型透传/接口字面量实参；
bare 按需纳入 + package.json 读底座 + symlink 穿透 + `.js` 落点直转；
`export * as` / 默认对象值边等双边同拒已锁定。
证据：demos 209–222、224、233–243、247–260、324–332、337–338 + `--parity` 矩阵。

- [ ] E1-1 版本化注册表：`zod@4.6.5` 本体仍 0%（类/泛型/this 链在子集外）。
  现状：210/220/257/258/259 为 `expect.refused` 锁定，双边同拒，零 panic。
  方向二选一（需用户拍板）：A 通道（SA 原生重写 zod 子集эс，224 为雏形）；
  B 通道（program-link + 深类型系统：Record 动态字典 Z1、顶层 const 对象 Z2、
  类运行时/Error 基 Z3）。验收：zod464 通数变化（非首拒消失）。
- [ ] E1-2 纯 `.js` 包：218 锁定双边同拒（`module.exports` 无布局）。
  需模块形状识别（CJS/ESM）+ 无类型 JS 的 `any` 化策略（与 T2/故意分歧政策联动）。
- [ ] E1-3 `package.json` exports / 条件配线：`@zod/source` 已验证跟随；
  通用 exports 映射表未建。验收：fixture 真包 + `--parity` SAME。
- [ ] E1-4 动态 `require()`/`import()`：现定位拒（step358 `import = require`）。
  需运行时模块表（sci 侧立项）或保持拒收（冻结亦可，需裁决）。
- [ ] E1-5 Z1 Record 动态字典收尾：读侧已通（232-244：`m[k]` 读脱糖、
  字面量具化、形参/返回/方法贯通、串值/接口值/串元）；写侧 `m[k] = v`
  已通（233）；联合键（`Record<"a"|"b",T>`）需注解种设计，另立。
- [ ] E1-6 Z2 顶层 const 对象：JEV 已裁决保持拒（B，无底座新语义风险高于收益）。
  若重议，需 `as const` 只读布局 + 用位折叠设计（A/B 见 step245）。
- [ ] E1-7 Z3 类运行时：`extends Error` / `extends ZodX` 链 / 方法-this 链 +
  闭包捕获，用 program-link 域推进（visit.ts 全篇即此域）。

## E2 后端大代码分水岭（B-backend / B-backend2 实证）

结论原文：纯计算 + 字符串 + 受限控制流可搬；触对象数组 / JSON / 反射 /
Error 体系 / Node 运行时一律不行。逐项：

- [ ] E2-1 接口对象数组 `P[]`：下标/字段/`.length` 全拒（核心堵点，
  与 interface 后门无关）。设计：元素句柄种 + 下标缩放到位。
- [ ] E2-2 `?.` 组合：仅参数直取可用；`p?.x ?? d` / 局部 / 拷贝全拒
  （`== null` 显式判断可用作改写目标）。`?.()` 48× 全拒。
- [ ] E2-3 `instanceof`（234×，含自定义类）：双边同拒，无 vtable/品牌表底座。
- [ ] E2-4 `process.env`（319×）：双边同拒，需宿主注入设计（ambient 槽？裁决）。
- [ ] E2-5 `TextEncoder`（86×）/ `Uint8Array` / `Buffer.from`：双边同拒，
  需 typed-array 句柄模型（史诗）。
- [ ] E2-6 自定义 Error 子类：基类外部声明，无布局。`throw "str"` 亦拒（串 throw 另步）。
- [ ] E2-7 对象反射：`Object.keys(o).length` 已锁（418，已知布局静态折叠）；
  `entries/values` 动态结果、`hasOwnProperty` 动态键仍拒。
- [ ] E2-8 `JSON.parse` 全形：bool 字段已修（391）；数组/嵌套/类型转换器未全。
- [ ] E2-9 Map/Set 迭代：`entries()` 三元组、`for-in` over Map、
  for-of 解构（上游静默错码已实锤，薄口拒收正确；真迭代需条目向量设计）。
- [ ] E2-10 泛型约束取字段：`T extends {x}` 取 `o.x` 拒（泛型不携布局）；
  无约束 `id<T>` 可用。需约束布局携带设计。

## E3 事件循环与异步值（冻结为主，需 Phase-2 立项）

- [ ] E3-1 定时器 6 件套：Phase-2 专用拒因已锁（step55），事件循环无底座。
- [ ] E3-2 `Promise.all`（47×）/ `then` 链 / `Promise.resolve` 值语义：双边同拒。
- [ ] E3-3 生成器/`yield`：双边同拒；async 生成器同拒。
- [ ] E3-4 async 余量：声明/箭头/await 各位已通（363/365）；`for await` 已脱糖（F）；
  剩余为值语义（Promise 值）域，维持同步子集。

## E4 f64 / 数学库尾巴

已有：f64 推断/取负/循环（整步进）/打印/顶层折叠/数组元/条件位/`Number()` 空参/
`Number.parseInt` 旧功。剩余：

- [ ] E4-1 `Math.log/hypot/cbrt/atan2` + `EPSILON`：双边同拒（X-mathrem 定案，
  投影表冻结，无 f64 extern 映射不做）。
- [ ] E4-2 `toFixed` / `toLocaleString`：f64 格式化无底座，冻结。
- [ ] E4-3 f64 循环携带：整步进已走 int（279 转拒收 + 440 新锁）；
  浮步进/浮初值 R2 大声拒；while 体内 f64 赋值零实例，维持。
- [ ] E4-4 T2 故意分歧维持：`const x: i32 = 7.5` 拒收（上游忽略注解系静默错译，
  JEV 100% 定案，不跟）。

## E5 测试框架余量

已有：注册内联（test/describe/it/each/skip/todo/only/sequential/skipIf-runIf）、
hooks 三件 + afterAll 整域缓冲、expect（零元/比较/串/长度/toThrow/toMatch/
toContain 数组形/数组深比/negation）。剩余：

- [ ] E5-1 `.not` 全系：仅 toBe/toEqual/toContain 翻转；其余匹配器 `.not` 待补。
- [ ] E5-2 `test.failing` / `concurrent` / `retry`：无 panic 捕获/事件循环底座，
  大声拒已锁（冻结，维持）。
- [ ] E5-3 `sa test` 真跑：需 LLVM 后端（本机已有 LLVM-14 可跑）；
  `sa_tests/` 91 件 `--list` 层已验，真跑待批量复核。

## E6 诊断抛光 H1b / H9（只影响拒因文本，不影响 verdict）

- [ ] E6-1 H1b 深层罪魁：调用实参求值序、声明初值深层、复杂嵌套内层——
  返回臂/三元臂/if-while-for-do 条件/switch 判别已对齐（402-404），余下为
  求值序差异类（H1b 本体延期）。
- [ ] E6-2 H9 文案：H9a（datenew 尾）/H9b（unknown-fn 体）/H9c（arrc 门 +
  kind 名半数）已落；剩余 kind 号模板 1 处 + symbol 核查序（真通拒分歧，
  需裁决：`const s: symbol = 1` 上游放行 vs 薄口拒，T2 同政策）。

## E7 R3-std 回迁收尾（铁律⑦：std 归 sci/sa_std，tsgosa 只映射）

已回迁：slice/push 系列、sort/scan/reverse/fill/concat/splice 系、串判等/
slice/取字/split/trim/parseInt、pow/sqrt/log10/minmax/rounding/abs-sign-imul/
join/split/replace/match/exec、`@ts_str_compare`。剩余：

- [ ] E7-1 按 `AGENTS.md §6` 回迁 track 逐符号关单（harness 间接验 + 改调 +
  demos 重生成 + 双仓推；冻结清单 §5.1 除外）。
- [ ] E7-2 回调骨架永驻 tsgosa（语法展开，非 std，不搬——已定案，勿重议）。

## E8 架构债（不 blocking 门禁，但越拖越贵）

- [ ] E8-1 `internal/transpile/transpile.go` 5085 行：program-link 域堆积，
  铁律⑥（编排薄层、单次 30 行）已多次为例外放行。拆分需先问 JEV，
  拆分即独立 commit + 单测。
- [ ] E8-2 sci 版本钉死：372 位宽回归教训——门禁应记录 sci commit
 （现只钉分支）；`--check`/原生跑分结果必须附 sci 版本。
- [ ] E8-3 台账合并：`AGENTS.md` step ledger（~400 条）与 `docs/todo.md`
  历史 log 口径不一（如“全机 440/440”系 demo~440 时代快照，当前 2216 例
  以实测为准）。新条目必须标 vintage + 验证命令，旧数不得直接引用。

## E9 环境债（本轮现装现验，须文档化）

- [ ] E9-1 LLVM-14 构建依赖：`sa` 原生后端需 `llvm-14-dev`
 （sci 默认 `/usr/lib/llvm-14`）+ `zig build`（默认开 LLVM；
  `-Dllvm=false` 编出的 `sa` 跑不了 `build-exe`，`run.sh` 注释旧提示待改）。
- [ ] E9-2 node/deno 插件正式安装：manifest 缺进程权限声明，
  `sa plugin install` 拒收（归 `sa_plugin_node` 上游）；`--dev` 可绕过。
  203–208/289 七例原生跑分依赖此项。
- [ ] E9-3 TALG 语料：`../TheAlgorithms-TypeScript` 本环境缺失，
  用户 10-12 令的 TALG 全绿验收不可验，需补克隆 + 矩阵。

## 门禁矩阵（每项 epic 的验收命令）

| 目标 | 命令 | 当前数字 |
|---|---|---|
| 单文件转译零漂移 | `demos/run.sh --check` | 2216/2216 |
| 原生跑分 | `demos/run.sh -j8` | 2209 + 7 插件环境挂起 |
| 上游通拒一致 | `demos/run.sh --corpus` | 286 agree |
| Go 单测 | `go test ./...`（fswatch 走 tmpfs） | 104 包 + fswatch 全绿 |
| zod 矩阵 | 464 双边对跑 | 31 一致 / 零 UP过TN拒 / TN 零 panic |
| 底座 360 用例 | 双边对跑（上次实测 step297/301） | 78 双过 + 26 缺口（需重测标 vintage） |
| sa 有效性 | 全量 `sa check` 扫 main.sai | 上次 241/247（6 插件上下文误报） |

## 非目标 / 冻结（已裁决，不重议）

D1 块重名拒收；D2 泛型单态 i32；D3 f64 零释放；D4 any 联合收窄；
D5 f64 复合按 fadd；D6 联合声明走推断；D7 T2；D8 f64 整循环；
D9 symbol 注解拒收；X-datecmp / X-nanspec / X-statread / X-sortstr /
X-strjoin / X-radix / X-sweep 系上游有病项（薄口拒收正确，不移植）。
