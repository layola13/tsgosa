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
  方向二选一（需用户拍板）：A 通道（SA 原生重写 zod 子集，224 为雏形）；
  B 通道（program-link + 深类型系统：Record 动态字典 Z1、顶层 const 对象 Z2、
  类运行时/Error 基 Z3）。验收：zod464 通数变化（非首拒消失）。
- [ ] E1-2 纯 `.js` 包：218 锁定双边同拒（`module.exports` 无布局）。
  需模块形状识别（CJS/ESM）+ 无类型 JS 的 `any` 化策略（与 T2/故意分歧政策联动）。
  - [x] E1-2 npm JS 运行时包形状（2026-10-20）：`.d.ts` 契约借种（返回种 +
    形参种）+ CJS 导出形状 + `.mjs`/`.cjs`/`.d.mts`/`.d.cts` 扩展族；demo
    `2222_npm_js_shapes` 真机 42/hi/42。
  - [ ] E1-2 余量：CJS 默认导出（`module.exports = fn`）、`exports` 增量赋值
    多语句、JS 类运行时（`.d.ts` 声明类 ↔ `.js` 实现）、ESM `export default`
    运行时、`.d.ts` 类型侧布局播种入 JS 文件（现仅签名借种）。
  - [ ] E1-3 `package.json` exports / 条件配线：`@zod/source` 已验证跟随；
  通用 exports 映射表未建。验收：fixture 真包 + `--parity` SAME。
  - [x] E1-3a 运行时/类型双解析（2026-10-20）：底座 `NoDtsResolution` 运行时
    口径解 npm 包（`exports` `"types"` 置首 / 顶层 `"types"` 字段不再恒
    Unresolved）；demo `2219_npm_types_first` 真机 42。
  - [ ] E1-3b `.d.ts` 类型单元：types 侧布局播种（interface/class/enum/别名）。
    - [x] 本地/相对 `.d.ts`（2026-10-20）：声明文件入集为类型单元，接口/别名/
      枚举可播种；`declare class` 大声拒（无运行时构造）；demo `2220_dts_types`
      真机 3/2/2。
    - [ ] npm 包 `.d.ts`：同一 spec 双目标（运行时目标 + 类型目标），绑定需查
      类型目标。
  - [ ] E1-3c package.json 入口字段：`main`/`module`/`exports`/`types`/
    `imports`(`#alias`)/`workspaces` + 依赖报告面（peer/optional）。
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
  与 interface 后门无关）。**JEV 首选（67%）但有模型前提**（2026-10-20 核）：
  `sci/sa_std/ts_array.sa` 现只有 word（4 字节）元数组（`@ts_arr_push_word` 等），
  句柄元数组（串元/嵌套）在薄口是「4 字节槽存柄 + 读回零扩展」口径（依赖低址
  堆）；实例元（8 字节指针）要么走同一低址前提，要么在 sci 侧新增宽元原语
  （R 轨道纪律：sci 先落 + harness 验，tsgosa 再改调）。未决前不做（禁静默错码）。
- [ ] E2-1b 接口对象数组之外的集合面：`Map<K, V[]>` 已有；`Record<K, V[]>`、
  嵌套泛型数组（`T[][]`）、对象元数组的派生方法（map/filter 回调元为实例）待摘。
  数组族新原语已就位（sci：`@ts_arr_entries`（对数组）、`@ts_arr_keys`（下标）、
  `@ts_arr_clone_flat`（值浅拷）→ 端口 `arr.entries()`/`keys()`/`values()` 已转正）；
  余 `Object.entries`（键值对 = 串+i32 **混元** → 端口数组模型同元，禁混元，
  须立 pair 专用标记；完整设计见下条 E2-2m）。
- [ ] E2-2 `?.` 组合：参数直取/局部拷贝/链基/调用基/struct 数组元基已通
  （链中 `?.` 已空短路修复，step2223）；余 `?.()` 48×（裸函数可选调用）待摘。
- [ ] E2-2c Set 展开为数组（`[...new Set(xs)]`）：Set 迭代需 sci 侧新符号
  （R 轨道：sci 先落 + harness 验；上游的 Set 迭代是把元素当数组读的错译，
  我方拒收正确，不移植）。
- [ ] E2-2e `o.hasOwnProperty(k)` 已补（E2-7c，demo 2237）；余项：
- [ ] E2-2f `str.localeCompare(b)`：需三路串比较（sci 已有 `@ts_str_compare`；
      端口侧须归一为 JS 的 -1/0/1，禁直接透传原值）。
- [ ] E2-2g `Object.entries(o)` / `arr.entries()`：pair 数组需 **str+i32 混元**
      元素（端口数组模型当前同元：arr/arrStr/arrNest 三种标记皆单一种）→
      需扩「按位元种」或引入 pair 专用标记。（`Object.entries` 已给精确拒因；
      `arr.entries()` 仍落 unknown method。）
- [x] E2-2h `Array.from(<string>)`：**已转正**（E2-2i，sci `6d8369fe` + tsgosa 同批）——
      串源走 `sci/sa_std/ts_string.sa` `@ts_str_split` 零长分隔符分支（sci 侧已修
      死循环：原 `nstart = idx + sl` 在 sl=0 时游标不动）；demo `2239_array_from_string`
      真机 `3/a/c`。余：`"str".split("")` 本身（端口侧仍走数组声明路径的笼统拒因）。
- [x] E2-2h-old `Array.from(<string>)`：串→字符数组（`Array.from` 现支持
      `{length}` 零数组 / 数组克隆 / mapper，串源落
      `array initializer must be literal or array`；`"abc".split("")` 同拒）。
      实现要点（已探明，勿重复踩）：逐字核 `saLowerStrIndexChar` 返**字柄
      （owned）**，而字柄在**循环体内**创建 → 域尾释（函数出口）在体外 →
      verifier `UnknownRegister`（`!t_10 not declared in the current scope`）。
      两条出路待决：①字柄入数组即由数组持有（需明确串元数组的句柄归属：
      现模型为「数组存 4 字节槽（句柄截断）+ 域尾释字柄」，读回靠既有
      arrStr 路径），字句柄须在**循环体内**释（则数组元素悬垂，须核 arrStr
      读路径是否重建 {ptr,len}）；②或先解清串元数组的句柄归属模型再落
      （禁半落地，违铁律 4）。
      另：串源当前在**数组声明路径**（`saLowerArrDecl` 尾）落
      「array initializer must be literal or array」——即 `Array.from` 的
      调用分派未走到 `saLowerArrayFrom`（其串源分支未触发），落精确拒因需
      先定位该分派点（勿在 `saLowerArrayFrom` 内加检查，验不到）。
- [x] E2-2i sci 侧空分隔切分（`@ts_str_split` 零长分隔符）——**已完成**
      （sci `6d8369fe` 已推：`sl==0` 分支按字节切 1 字节段；harness `sa check` ok
      1797 指令 + `"abc"` → 3 元素 exit 3）
- [x] E2-2k sci 侧 `arr.entries()` 对数组原语（`@ts_arr_entries`）——**已完成**
      （sci `d67e0565` 已推 + harness `sa check` ok 1261 指令 + 两元源 → 两对 exit 2；
      tsgosa 侧改调见 AGENTS E2-2k；上一轮记的「解析不过」实为**我的 harness 自身
      写错**（该 sci 文件单独 `sa check` 解析通过 ✓，同形旧符号调用亦 check ok ✓）——
      教训：验证 harness 自身先过一遍（单参调用/半程切分），再判被测符号。）

- [x] 已核验可用（本轮探针，零改码）：`infer`/映射类型/条件类型/模板字面量
      类型（纯类型擦除全通）、私有字段 + getter、`p?.["a"]` 可选计算下标、
      `catch` 无绑定、`as` 断言、`instanceof` 值位折叠、`hasOwnProperty` 方法形、
      `localeCompare`、`Object.keys/values/assign`。
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

- [ ] E5-1 `.not` 全系：`neg` 已贯通全部已支持匹配器（实例/数组/串/零元/
  比较/长度/toThrow/toMatch/toBeNaN，`sa_ctrl.go` 分发核实证）；
  剩余为未支持匹配器（mock/对象系，需对象深模型，冻结，禁硬做）。
- [ ] E5-2 `test.failing` / `concurrent` / `retry`：无 panic 捕获/事件循环底座，
  大声拒已锁（冻结，维持）。
- [x] E5-3 `sa test` 真跑：LLVM-14 后端已通，90 件批量原生复核
  **89 PASS + t289_hash 挂起**（`PackageNotResolved`，node 插件环境项 E9-2，
  转译侧全绿）；`--list` 发现层同步全绿。代码零改动（纯验证项）。

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

- [x] E9-1 LLVM-14 构建依赖：`sa` 原生后端需 `llvm-14-dev`
 （sci 默认 `/usr/lib/llvm-14`）+ `zig build`（默认开 LLVM；
  `-Dllvm=false` 编出的 `sa` 跑不了 `build-exe`，`run.sh:51` 旧提示已改）。
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

- [ ] E2-2m `Object.entries(o)` pair 混元模型（obj 家族最后一块）——**完整设计**
      （可直接施工，无需再探）：
      ①**标记**：saScope 增 `arrPair map[string]bool`（pair 数组句柄 → 真）；
      端口数组模型同元（arr/arrStr/arrNest 三种标记皆单一种），故 pair 需**按位
      分种**：位 0 = 串句柄地址、位 1 = i32 值。
      ②**构造**（`sa_arr.go`，与 `saLowerObjectKeys` 同处）：接收者门同 keys
      （绑定标识符 + `inst:` 布局 + 无存取器）；逐字段（**声明序**）建一个 2 元
      pair 数组：头 `alloc 16`（{ptr=buf, len=2}）+ 缓冲 `alloc 8`（位 0 存键的
      **串句柄地址**（`saLowerStringLiteral` 产出的 16 字节 {ptr,len} 句柄，
      按薄口既有「元素存句柄地址截断入槽、读回零扩展」惯例），位 1 存该字段
      **i32 值**（句柄域 str/arr/inst 沿旧门拒：pair 值槽是 i32 字））；外层数组
      存各 pair 的**地址**（`saLowerArrayPush` 既有 word 推入 ✓）→ 外层即
      arrNest 形（`scope.arrNest[h] = true`）；pair 数组登记 `arrPair[pair句柄]=true`。
      **无循环**：字段数静态已知，可展开落字 → 规避「字句柄在循环体内建、域尾释
      在体外」的 verifier UnknownRegister 坑（step2223 实证）。
      ③**读侧**：`saEvalStr` 的元素臂（`sa_arr.go` 附近 arrStr 判定处）与 i32
      元素读（`saLowerIndexLoadExpr`）各加一处：base 命中 `arrPair` 时——
      下标 0 走**串位**（读出的是串句柄地址，与 arrStr 同形）、下标 1 走
      **i32 位**；其余下标大声拒。
      ④**destructure 拒因**：`for (const [k, v] of Object.entries(o))` 的 k 是
      **串**，端口解构位恒 i32 → 该形态大声拒（`Object.entries pair key is a
      string (destructuring not lowerable)`）；索引形 `e[0][0]` / `e[0][1]` 可用。
      ⑤**验收**：demo（`Object.entries` 索引形：键串 + 值 i32）+ sci 侧无需新
      原语（pair 由端口构造 ✓，与 `@ts_arr_entries` 的 {i,v} 不同：此处位 0 是
      **串**而非下标 ✗ 故不能直接复用那个原语 ✓）。
      ⑥**追加实测（2026-10-20 试做后回退，务必照此施工）**：`es[0][0]` 的**内层
      读基是元素访问表达式**（非标识符 ✗）→ 按「pair 句柄名」查标记取不到 ✗ →
      键槽**读出垃圾地址**（静默错码 ✗ 最坏类，已回退）。正解两条：
      (a) 标记打在**外层 entries 数组**上（`scope.arrPair[entries句柄]=true`，
      语义＝「其元素是 pair」✗ 不再逐 pair 登记）；
      (b) 读侧加 helper `saPairSlot(e, scope) (位次 int, ok bool)`：内层读的
      基须是「entries 数组的**字面量**下标访问」，且内层下标亦为字面量 → 返回
      位次（0=串键 / 1=i32 值）；非字面量下标拒（禁运行时猜位次 ✗）；
      (c) 串位读（`sa_str.go`）与 i32 位读（`sa_arr.go`）各按位次分派：位 0 走
      **先取外层 pair 句柄**（`saLowerIndexLoadExpr` 读外层元素 ✓）**再读位 0**
      （`saLowerCheckedIndex(pairH, 0)` ✓）；位 1 同形读位 1 ✓；
      (d) `saIsStrExpr` 的元素臂按 `saPairSlot` 位次 0 判串 ✓（否则 console.log
      会把键当 i32 打印 ✗）；
      (e) 归属：pair 头/缓冲须 `saOwnTemp` 登记（否则 verifier MemoryLeak ✗
      实锤）；键的串句柄沿 `saLowerStringLiteral` 既有归属 ✓。
      ⑥**门禁**：落 demo（`es.length` / `es[0][0]` 键串 / `es[0][1]` 值 i32）+
      `sa check` + 真机跑分 + 全量 `--check`/`--corpus` + 原生跑分。
