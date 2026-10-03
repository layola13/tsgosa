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

> 文件布局（JEV split_many 批准拆分，零语义变更）：`internal/transpile/transpile.go`（管线/作用域/函数/语句）+ `sa_expr.go`（表达式核与调用总线）+ `sa_decl.go`（变量声明）+ `sa_ctrl.go`（控制流与语句）+ `sa_math.go`（Math 内联与别名）+ `sa_str.go`（str 全集）+ `sa_arr.go`（arr 全集）+ `sa_class.go`（class 最小子集）+ `sa_date.go`（Date 最小子集与正则门）+ `sa_map.go`（Map/Set 最小子集）。同包，禁跨文件重复定义；新增领域先问 JEV 落哪件。

- step1：顶层 `function f(): void {}`/`return;` → `@f(): ret`；`(): number/boolean {return lit;}` → `@f() -> i32: ret lit`；值空体缺 return、大声拒；非函数、void 回值、联合注解一律拒（`main_test.go:22-126`；string 返回见 step25）。
- step2：`if/else` → `EXPAND IF_ELSE/IF_TRUE` + `@import "sa_std/control.sal"`；`false` 恒假消死臂；`return c?a:b`（i32 字面臂）→ `EXPAND SELECT`（`main_test.go:128-284`；串臂见 step25 槽汇合）。
- step3-11：i32/bool 表达式核（算术/比较/逻辑/`!`/`++/--`/三元值形/调用传参）、`while`/`for`（legacy br 形，cont 落增量前）/`do-while`/`switch`（legacy 链）/`try-finally`（无 throw 时直跑，含 throw 拒）+ 不可达门。
- step12：i32 数组（字面量 alloc 16 头 + 缓冲逐槽 store、越界归零下标读 join、元素存、`.length` 头+8、`let a=b` 句柄拷贝/传参直传；spread/非 i32 元/缺 init/数组条件·返回位一律拒）。
- step13：`for-of`/`for-in` 索引巡回（绑定数组直传/字面量现场构造，`idx=0`+头+8 len+`slt/br`+`base/mul/add/i32` 读回；for-in 绑下标；`continue→top` 原样不对称；`await`/多声明/pattern/非数组基拒；sci FOR/ARRAY_FOR_EACH 宏形不用，沿 legacy）。
- step14：标号语句（loops/switch/block 绑定，标号 break/continue 查表；块为 break-only；串行复用合法、同名嵌套/未定义/块上 continue/标非常规语句拒；`labels.go:1-158`）。
- step15：`throw` → `panic(2501)`（终结；try 内含 throw 仍拒）+ 空语句 no-op（`saemit.go:153,882-887`）。
- step16：数组解构声明（`const [a,b] = p|[...]` 逐元越界归零 join 绑 i32；空穴跳过；rest/嵌套/对象布局/非数组源/函数值/缺 init/重复名拒；`lowerDestructuringDecl:5414-5461/destructureArray:5309-5333/bindPatternName:5513-5524`）。
- step17：无注解局部推断（数组字面量/句柄走 arr 通道，`true/false`/bool 句柄走 bool，其余 i32 求值；缺 init 绑 i32 零值，const 缺 init/函数值拒；`lowerVarDeclList:1415-1433` 缺省 i32 + 按初值绑定）。
- step18：无注解参数缺省 i32（`lowerFunction:946 ptype := tI32`）。
- step19：无注解返回缺省 void（`lowerFunction:919-923`；有值返回仍拒）。
- step20：`Math.abs(x)` 分支汇合内联（alloc 8 槽 + `sge x,0` + br + 两臂 store + end load + 释放；`lowerMathInline abs:5754-5780` 原样；其余 Math.*/别名调用/元数错拒）。
- step21：`Math.pow(b,e)` 幂循环内联（与 `**` 的 `saLowerPow` 同发射，仅标号前缀 `L_mpow_`；`lowerMathInline pow:5781-5806`；其余 Math.* 仍拒）。
- step22：`Math.floor/ceil/round/trunc(x)` 整数恒等内联（`out = add v, 0`；`lowerMathRounding:5941-5945`；浮点分支在 i32 子集内不可达）。
- step23：`Math.min/max(a,b)` 两元折叠内联（`slt/sgt` + alloc 8 槽 join；`lowerMathMinMax:5650-5687`；spread 切片归约拒）。
- step24（math 一次过收官）：`Math.min/max(...slice)` 切片归约循环（极值初值 + 索引巡回 take/skip；`lowerMathSpreadMinMax:5690-5747`）、`Math.sqrt` 整数二分（`lowerMathSqrt:5841-5891`；浮点早拒）、`Math.log10` 位数循环（`lowerMathLog10:5894-5916`）、`Math.random` 确定性 LCG（`__ts_rand_seed`；`lowerMathRandom:5920-5936`）、`Math.PI/E` 折 3/2（`stdlib.go:126-127`）、`const f = Math.<m>` 别名及链式（`mathAliases:2957-2964`；调用经统一分发；顶层 const 仍由函数外语句门拒）。
- step25（string 一次过收官）：str 种（16 字节 {ptr,len} 句柄；`string` 注解/形参/`-> ptr` 返回/实参形参种导向/无注解字面量推断）+ 文件级 `@const utf8` 常量池（`lowerStringLiteral:2974-2990`）+ `s.length` + `+` 双串拼接（`sa_string_concat` 经 fmt 缓冲读回；混合数值须显式 `String()`）+ `==/!=` 内容相等（`stringContentEq:9146-9166`）+ 串方法全集直调现货（charCodeAt/codePointAt/indexOf/lastIndexOf/startsWith/endsWith/toLower-upper/repeat/padStart-padEnd/replace/replaceAll/includes/charAt/at/trim 系/concat/slice-substring-substr/toString；`lowerStringMethod:7156-7388`；split 需串元数组拒）+ `String(x)`/`String.fromCharCode-fromCodePoint` + 模板字面量（i32/bool 经 `sext+sa_fmt_i64_into`；`lowerTemplate:8823-8847`）+ `console.log`（空格分隔+末尾换行→`sa_print_bytes`；`lowerConsoleLog:7862-7887`）+ `s+=` 拼接；门：tagged 模板/`Number.parseFloat`（f64）/`console.error`（node 插件）/串 switch/串 for-of/串条件一律拒。
- sa_std 复用纪律（回应“直接映射 sa_std、禁造轮子”）：以 `satsgo/internal/saemit/stdlib.go` 投影表为准——凡 `Module: sa_std/*.sai`（string/concat、console.log→print、Map/Set→btree、Date/fs/net…）的特性必须走 `@import` + 符号调用，不得手写；整数系 Math.* 在表中全为 `@inline`（`stdlib.go:115-127`），`sci/sa_std` 侧并无 i32 符号（math.sai 皆 f64、math.sa 皆 u64/i64 宏另带 `!` 释放纪律），故 step20-24 内联即底座复用而非造轮子。
- 底座结论（回应“math 直接映射 sa_std”）：`satsgo/internal/saemit/stdlib.go:115-127` 投影表规定整数系 Math.* 全部 `@inline`，`sci/sa_std` 侧只有 f64 外部函数（`math.sai`）与 u64/i64 宏（`math.sa`，另带 `!` 释放纪律），并无 i32 符号可投；故 step20-23 内联即底座复用。string/vec 同理：`string.sa`/`vec.sa` 为运行时句柄库，薄口尚无 string 类型，待 string 字面量特性时再投影。
- CI 节流：`.github/workflows/ci.yml` 与 `codeql.yml` 触发器改为仅 tag 推送（`push.tags: v*`），main 分支直推/PR/merge_group/定时不再消耗 Action 额度。
- step26（数组方法一次过收官，落 `sa_arr.go`）：push（扩容拷贝）/pop/shift/unshift/fill/sort（数值插入；比较器走 cmp 内联，具名比较器拒）/indexOf-lastIndexOf-includes（扫描）/reverse/slice/at/join（interp 折叠）/copyWithin/toReversed/toSorted/with/toSpliced/concat（含 spread）/`Array.from`（`{length}` 零数组/切片克隆/mapper 内联 map）+ 高阶 forEach-map-filter-find 系-some-every-reduce 系（回调现场内联：形参快照绑定 + return 槽拦截；串回调值/具名回调/形参超量拒）；调用核经成员分发，返回种导向各求值位（i32/arr/str）；下标读基放宽到数组值调用；门：未知成员/元数错一律拒。
- step27（core 顶层收官）：`export function`/`export default function` 修饰擦除直通；interface/type-alias/enum 声明擦除（无码）；`export {}`/`export =` 无码（`lowerModuleDecl:10329-10336`）；type-only import 擦除，值 import 拒（单文件）；整数枚举预扫成表 + `E.M` 折叠（`integerInit:9258-9277`；串/计算初值、未知成员拒）。
- step28（class 最小子集，落 `sa_class.go`）：单类 i32 字段布局（4 字节槽）+ `new`（布局 alloc + 构造 `this.f = param` wiring）+ 方法调用内联（形参快照 + this 指向 + 槽汇合，复用回调机）+ 字段读写（含语句位 `o.f = v`）；门：继承/抽象/静态/访问器/装饰器/私有·计算名/参数属性/字段初值/非 param 右值/具名外回调一律拒。
- step29（date 最小子集 + 正则门，落 `sa_date.go`，JEV 落件 a 97%）：`new Date()`/now/parse/getTime/setter 皆以 date 种（i64 millis）不透明流转，永不截断；串方法（toISOString/toString 系）与模板/console 插值（经 `sa_fmt_i64_into`）直调 `time.sai` 现货；getters 窄化为 i32（分量恒 < 2^31，窄化点唯一）；setters 变异重绑；parse 非法 panic(2503)；getTimezoneOffset 恒 0；门：有参 new、i32 位 millis、toLocale 系、未知成员一律拒。正则：satsgo/sa_plugin_ts 均无 lowering 证据（regex.sai 系无人调用现货），铁律 4 禁原创，字面量/new RegExp/.test 一律大声拒。
- step30（Map/Set 最小子集，落 `sa_map.go`，JEV 落件 a 84%）：`new Map()`/`new Set()` 零参柄直调 btree 后端；Map set/get/has/delete/clear/size,getSize + Set add/has/delete/clear/size 全直调现货（`lowerMapMethod:4726-4803`/`lowerSetMethod:4806-4859`）；键 i32（单元切片）/串直通，值 i32（读回窄化点唯一）；门：有参 new/keys-values-entries（vec 模型超槽）/未知成员/元数错/条件位/`.length` 属性形一律拒。
- step31（typeof 收官，落 `sa_expr.go`/`sa_str.go`）：`typeof v === "undefined"`（任一序、==/===/!=/!==）标识符空检查（eq/ne v,0；`lowerTypeofGuard:156-178`）；其余对静态种折叠 `eq/ne 1, 1` 常量临时量（字面按语法表null方言映 undefined，标识符按作用域种i32/bool/str/arr/map/set/date/inst→number/boolean/string/object，别名/函数→function；`lowerTypeofConstFold:236-283`）；值位具化种类串（`lowerTypeof:9171-9239`）；门：非标识符守卫/未知全局（无 env-probe）/计算值一律拒。
- step32（对象字面量一次过，落 `sa_class.go`）：接口布局预扫（i32 字段；方法/索引签名拒）+ 字面量具化（alloc + 逐域 store；简写读绑定；字面计算键；`layoutOfLiteral:8953-8975`/`objPropName:8984-9007`/`lowerObjectLiteral:9009+`）+ 按键集唯一匹配（0/多皆拒）+ 注解须同名接口 + 实例形参直传（`inst:Pt` 对 `inst:Pt`，预扫拆两遍保序）；读写复用实例通道；门：spread/方法属性/串字段/值位字面量/`new` 接口/错配传参一律拒。
- step34（class 继承一次过，落 `sa_class.go`）：单 extends（基须先声明；多/mixin/未知/环拒）布局追加（父偏移守恒，同名守基偏移）+ 方法拷贝覆写 + 默认派生构造（`recordClassNamed:9819-9828`）+ 自有构造须 `super()`（`ctorCallsSuper:280-317`，声明期查）+ `super(...)` 委托基 wiring（`wireSuperCtorStatement:383-472`）+ `super.m` 同接收者内联/`super.f` 基布局读写（`lowerSuperMethodCall:237-250`/`checkSuperAccess:253-278`）+ abstract 记录并拒 `new` + 接口 extends 展平（`inheritInterfaceLayout:319-376`）。
- step33（Number/数组构造一次过，落 `sa_math.go`+`sa_arr.go`）：`MAX_VALUE`/`MAX_SAFE_INTEGER`→2147483647、`MIN_SAFE_INTEGER`→-2147483648（`stdlib.go:128-130`）；`isInteger(x)` 求值保副作用后折 "1"（`lowerCall:3837-3848`；浮点早拒）；`Array(n)`/`Array(a,b)`/`new Array(n)` 定长零数组与逐元构造（`lowerCall Array:3871-3884`/`newSizedArray:7710-7730`/`lowerNew:8603`）；门：parseFloat/Number(x)（f64）/未知 Number 成员/多元 new Array/spread·回调元一律拒。
- step35（`in`/`??` 一次过，落 `sa_expr.go`）：`in` 布局静态折叠 1/0（`lowerBinary:3135-3177`；品牌检查/动态键拒）+ `??` 空合槽（左非零直通否则右惰性求值，`lowerBinary:3182-3206`；i32 位）；门：串位 `??`/未知布局基一律拒。
- step37（`using` 门收口，落 `sa_decl.go`+`sa_arr.go`）：`using`/`await using` 措辞与封存对齐（`lowerVarDeclList:1395-1398`；同旗）；`for (using …)` 头经声明位同门，`for-of`/`for-in` 头补旗检查（否则析构跳过致静默错义）；回归测试锁死。
- step36（入口合成一次过，落 `transpile.go`）：顶层执行语句聚入生成的 `@main() -> i32`（空帧，缺尾返补 `ret 0`；`entry_top.go:1-152`）；用户 `main` 遇合成改名 `main__user`（定义 + 调用点，`main__user` 已有则拒）；无执行语句零附加（字节一致）；门：顶层变量声明仍拒（模块槽另域）。
- step38（spread 调用展开一次过，落 `sa_expr.go`）：单尾 spread + 定元被调（`resolveSpreadCall:7764-7798`；静态部按位求值，余位越界归零 join 填齐，spread 填位仅 i32）；门：双 spread/非尾/超元/非数组源/非 i32 填充位一律拒。
- step40（存取器一次过，落 `sa_class.go`）：get/set 以内联体记录（静态/计算名/字段重名拒，同类重复拒，继承拷贝覆写；`recordClassNamed:9774-9818`）+ 读内联 getter/写内联 setter（`lowerExpr:8130-8137`；只写读拒）+ super 存取走基 + `in` 判存；门：索引器如前拒。
- step39（`delete`/`await` 对齐，落 `sa_expr.go`）：`delete` 显式拒（静态布局不可删域，`lowerExpr:2742-2745`）；`await v` 值透传（悬挂在内层调用门拒，`lowerExpr:2863-2871`）。
- step41（重载签名擦除，落 `transpile.go`零新文件）：无体声明预扫不注册/发射不落字/入口改名不参与（实现体唯一定义；`saemit.go:642+758`）；孤签名零定义调用点按未知函数诚实拒（`saemit.go:983`）；JS 管线同形擦除（仅实现体落字），本次即底座复用替换。
- step42（未初始化声明补齐，落 `sa_str.go`零新文件）：`let s: string;` 绑空句柄零值（`s = 0`，后赋重绑；`saemit.go:lowerVarDeclList:1405-1434`）；`let x: number;` 既有 `x = 0` 不变；`const` 缺 init 仍拒；数组缺 init 仍按 step12 大声拒。
- step43（解构形参一次过，落 `transpile.go`零新文件）：`{x,y}:Point`/`[a,b]:i32[]` 合成隐藏句柄形参 `__darg`（兄弟名冲突追 `_`；`saemit.go:hiddenDestructuredParam:5347-5371`）+ 体顶按域展开（数组逐元越界归零 join，对象按布局 `load hid+off`；`destructureArray:5309-5333`/`destructureObject:5465-5509`/`drain:5376-5410`）；签名/预扫/调用元数按隐藏名一致（arr/inst 种导向句柄直传）；顶层 `const f=()=>`/`=function` 同形 out-of-line（块/表达式体；`tryTopLevelArrow:1015-1032`/`lowerArrowBinding:1058-1098`；生成器/async 拒）；门：rest/缺省/嵌套/未知域/无布局/局部箭头一律拒。
- step44（逻辑赋值一次过，落 `sa_ctrl.go`+`sa_expr.go`零新文件）：`&&=`/`||=`/`??=` 真短路（目标读一次，RHS 只在赋值臂求值，两臂经 8 字节槽汇合；`saemit.go:lowerLogicAssign:3439-3565`，槽形同 `??`）；语句位全种、值位 i32/bool（串值位无临时量串跟踪，只走语句位）；`L_logas_assign/skip/end` 标签；串以 length 判空、`??=` 保指针判空；门：成员/元素/未知/非i32-bool-str目标、串值位一律拒（模块槽随 modstate 另域）。
- 报告：`subset-report.txt` 逐行 `file:line:col: msg`，有拒绝则 exit 1。

## 4. 工具纪律

- 读/查/改优先 `sa_vm_run`（read_file/read_lines/count_lines/grep_search/edit_file），禁终端 `cat/grep/ls/head/tail` 看代码；`git/diagnostics` 走宿主。
- 有问题问 JEV（`jev_diagnose/jev_choose`），每步完问 `jev_next` 找下个任务，全程由 JEV 控制；禁停下来总结、禁 `askquestions` 式反问。
