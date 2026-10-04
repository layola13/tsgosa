# tsgosa/demos — TS 特性可运行展示集（用户进度检查用）

每个目录一个受支持特性：`main.ts`（tsgosa 方言）+ `main.sai`（生成的 SA，同进仓）+ `expected.stdout`（手写期望输出）。
`./run.sh` 跑全流水线并打印进度表（默认并行，`-j1` 回串行，结果一致）：

```
main.ts --tsgo --sa--> main.sai --sa build-exe--> out/demo --run--> diff expected.stdout
```

- `out/`、`*.log` 为生成物（gitignore），`main.sai` 可直接打开看 SA。
- 单测某例：`./run.sh 09_array_methods 11_string_array_iter`
- `./run.sh --check`：只重生成 `.sai` 并与进仓版逐字节比对（**`.sai` 禁止手改，只能由编译器出**；提交前/CI 自证；不匹配即 FAIL）。
- `./run.sh --corpus`：对 sa_plugin_ts/demos 语料跑上游/薄口通拒差分（零分歧即过；286 例约 2 秒）。
- 依赖：Go（编 tsgo）、sci 的 `sa` 二进制（`SA_BIN` 环境变量可覆盖，sci 下 `zig build -Dllvm=false` 构建）。

## 铁律：`.sai` 是编译产物
- `main.sai` 由 `tsgo --sa` 逐例生成后进仓，**绝不手改**（ts→sa 是编译出来的）。
- `./run.sh --check` 40/40 SA-CLEAN 即自证：进仓版与现编译器输出逐字节一致、可复现。
- 编译器改了发射，`./run.sh` 重生成后 `git diff` 只应出现发射意图内的 `.sai` 变化；意外漂移用 `--check` 定位。

## 特性覆盖表（与 AGENTS.md step 对应）

| demo | 特性 | step |
|---|---|---|
| 01_hello | console.log 字符串 | 2 |
| 02_arith | 四则/取模/优先级/负数 | 5 |
| 03_if_else | if/else 函数 | 2 |
| 04_while_sum | while 累加 | 2 |
| 05_for_sum | for/if/取模累加 | 7 |
| 06_ternary | return 位三元值 | 2/84 |
| 07_functions | 函数+递归 | 3 |
| 08_arrays | 字面量/读写/length | 12 |
| 09_array_methods | push/map/filter/forEach | 26 |
| 10_strings | 串方法（length/拼接/indexOf/slice） | 25 |
| 11_string_array_iter | 串数组 forEach/map 元绑定 | H8/139 |
| 12_for_of | for-of 字面量巡回 | 13 |
| 13_struct | 接口布局字面量/读写 | 89-102 |
| 14_class | 类构造/字段/方法/this | 21 |
| 15_enum_switch | 枚举+i32 switch | 142 |
| 16_template | 模板插值 | 8 |
| 17_bool_logic | &&/!/!= 布尔值打印 | 7 |
| 18_optional | `?.[]`/`??`/`.length` | 28/281 |
| 19_destructure | 数组解构+接口对象读 | 16 |
| 20_console | 多参 console.log | 6/260 |

## 第二批（21–40）

| demo | 特性 | step/台账 |
|---|---|---|
| 21_compound | 复合赋值全家 | 7 |
| 22_f64_cmp | f64 比较→bool 打印 | 128/135 |
| 23_switch_plain | i32 switch | 67 |
| 24_find_some_every | find/findIndex/some/every | 26 |
| 25_reduce | reduce/reduceRight | 26 |
| 26_sort | sort/toSorted | 26 |
| 27_slice_concat | slice/concat | 26 |
| 28_push_unshift | push/unshift/fill/with | H8b/140 |
| 29_spread_elem | 数组 spread 字面量 | 120 |
| 30_closure | 箭头值调用 | 111 |
| 31_math | abs/floor | 20-24 |
| 32_string_pred | startsWith/includes/endsWith | 25 |
| 33_nested_struct | 嵌套接口布局 | 91 |
| 34_generic_fn | 泛型函数擦除 | 134 |
| 35_map_getset | Map set/get/has | 30 |
| 36_date_tz | getTimezoneOffset 恒 0 | 29 |
| 37_interp_calc | 模板内表达式 | 25 |
| 38_nested_loops | 嵌套 for+continue | 7 |
| 39_index_find | indexOf/lastIndexOf/includes/at | 26 |
| 40_array_from | Array.from 切片克隆 | 26 |

已知边界（续）：f64 只比不打（`console.log(f64)` 沿旧门）；`Map.size` 属性形拒（用 get/has）；Date millis 不进 i32；`a[0]` 元素直打两家同出句柄数（parity，绑定串元素仍拒）；串+数 `+` 两家同走指针加（SAI 逐行一致，parity）；`==` 内容相等两家同漏（verifier 侧，parity）；sqrt 二分环两家同被 verifier 拒（后端侧，parity）。

## 第三批（41–60）

| demo | 特性 | step/台账 |
|---|---|---|
| 41_inheritance | 类继承+基字段 | 34 |
| 42_static_method | 静态方法调用 | 69 |
| 43_pop_shift | pop/shift/reverse | 26 |
| 44_to_reversed | toReversed 非变异 | 26 |
| 45_char_at | charAt 取字 | 25 |
| 46_char_code | charCodeAt 取码 | 25 |
| 47_rest_params | rest 形参打包 | 121 |
| 48_default_params | 缺省参数回放 | 71 |
| 49_fib | 递归 fibonacci | 3 |
| 50_logic_assign | `||=`/`&&=` 短路赋值 | 44 |
| 51_nullish | null `??` 缺省 | 35 |
| 52_typeof_guard | typeof 空守卫 | 31 |
| 53_do_while | do-while | 3-11 |
| 54_try_finally | try/finally 直跑 | 3-11/54 |
| 55_labels | 标号 continue | 14 |
| 56_obj_spread | 对象 spread 复制 | 46 |
| 57_str_num | String()/Math.max-min | 25/20-24 |
| 58_at_index | at 下标读 | 26 |
| 59_class_fields | 字段初值忽略+方法 | 28（初值忽略） |
| 60_in_operator | `in` 布局折叠 | 35 |

已知边界（续2）：字段初值忽略（两家同形，读出 alloc 语义值）；switch 无 fallthrough（两家同形，臂独立）；flatMap/flat 绑定拒（两家同拒）；`s += s = ` 串重绑两家皆不通（上游段错误，H13）；trim/repeat 运行时段错误（上游 trim 连解析错，H14）；57 原 `s+=` 已换 String/Math 可跑形。

## 第四批（61–80）

| demo | 特性 | step/台账 |
|---|---|---|
| 61_spread_call | spread 实参展开调用 | 38 |
| 62_pow | `**` 幂 | 21 |
| 63_minmax_spread | Math.max/min spread 归约 | 24 |
| 64_slice_negative | 负下标 slice 钳位 | 26 |
| 65_bit_ops | 位运算/移位 | 5/129 |
| 66_nested_arrays | 嵌套数组下标读 | 80 |
| 67_multi_concat | 多元 `+` 拼接 | 25 |
| 68_gcd | Euclid 辗转相除 | 7 |
| 69_prime | 素数判定循环 | 7 |
| 70_fizzbuzz | else-if 链（endif 修复回归） | step145 |
| 71_nested_if | 嵌套 if/else | step145 |
| 72_early_return | 卫语句早返 | 2 |
| 73_bubble_sort | 冒泡排序 | 7/26 |
| 74_max_loop | 循环求 max | 7 |
| 75_count_char | charAt 计数 | 25 |
| 76_sum_avg | 求和取整平均 | 7 |
| 77_2d_sum | 嵌套字面量直巡求和 | 83 |
| 78_fib_iter | 迭代 fibonacci | 3 |
| 79_copy_loop | 数组拷贝循环 | 12 |
| 80_range_sum | 1..100 求和 | 7 |

已知边界（续3）：绑定嵌套数组 for-of 行未记 arr（字面量直巡可，H15）；`run.sh` 对 tsgo 二进制加新鲜度检查（旧二进制曾致误报）。

已知边界（demo 写法已规避，对应台账项）：串三元值仅 return 位可放（声明/赋值位拒，H10）；`?.length` 拒（用 `.length`）；`Color.Green` 成员值未用（用 i32 传枚举）；split 结果不可绑定/测长/迭代（用 join…注：join 可转译但 sci verifier 对两家同拒 PhiStateConflict，本集暂不用 join，待后端侧）；for-in 另有 UseAfterMove 缺口（H11，不在本集）。

## 第五批（81–100）

| demo | 特性 | step/台账 |
|---|---|---|
| 81_reverse | reverse 原地反转 | 26 |
| 82_last_index | lastIndexOf/indexOf | 26 |
| 83_copy_within | copyWithin 块拷贝 | 26 |
| 84_to_sorted | toSorted 非破坏排序 | 26 |
| 85_with_method | with 下标替换拷贝 | 26 |
| 86_to_spliced | toSpliced 切片删除拷贝 | 26 |
| 87_substring | substring/slice 取子串 | 25 |
| 88_str_eq | 形参串 `==` 内容相等 | 25 |
| 89_math_round | ceil/round/trunc+PI/E | 20-24 |
| 90_num_check | isInteger+MAX/MIN_SAFE | 33 |
| 91_includes | 数组/串 includes | 26/25 |
| 92_push_ret | push 返回新长 | 26 |
| 93_map_ops | Map getSize/delete | 30 |
| 94_set_ops | Set 函数内 add/has | 30 |
| 95_getter_setter | 存取器读写 | 40/73 |
| 96_private_field | 私有字段读写 | 63 |
| 97_catch_value | throwing-try 函数内取值 | 54 |
| 98_static_inherit | 静态字段/方法继承 | 48/69 |
| 99_async_await | async/await 同步解包 | 55/137 |
| 100_date_get | Date 取值函数内 | 29 |

已知边界（续4）：toLowerCase/toUpperCase/replace/padStart-padEnd 两家同段错误（上游同形，串大小写/替换系记 H14 同族）；`s.concat` 方法两家同段错误（上游段错误，记 H14 同族；`+` 拼接可用）；`const s` 串 `==` 入口 leak（形参形可用）；Map `.size` 属性形拒（用 `getSize()`）；Set/`Date`/throwing-try 句柄在 `@main` 入口 leak（函数内可用，H12 入口释放 asymmetry）；`m.set` 语句位须后无 `.size` 误读（沿既有门）。

## 第六批（101–120）

| demo | 特性 | step/台账 |
|---|---|---|
| 101_bsearch | for 线性查找 | 7 |
| 102_insert_sort | 插入排序 | 7 |
| 103_palindrome | for 回文判定 | 25 |
| 104_override | 方法覆写 | 34 |
| 105_nested_tpl | 模板多插值算式 | 25 |
| 106_flags | 位或/与/异或标志 | 129 |
| 107_reduce_right | reduceRight 逆归约 | 26 |
| 108_find_index | findIndex 谓词 | 26 |
| 109_closure | 捕获形参箭头 | 111 |
| 110_while_break | `while(1)`+break/continue | step147 物化修复 |
| 111_switch_fn | 函数内 switch 返回 | 67 |
| 112_pick_max | 取大分支 | 2 |
| 113_chainwrite | 嵌套链写 | 93 |
| 114_compose | 高阶链函数内 | 26 |
| 115_stridx | indexOf/lastIndexOf/charAt | 25 |
| 116_minloop | 循环求 min | 7 |
| 117_dedup | 去重 indexOf+push | 26 |
| 118_prefix | 前缀和就地写 | 12 |
| 119_gcd3 | 辗转相除 while | 7 |
| 120_lcm | 递归 gcd 求 lcm | 7 |

已知边界（续5）：helper 内 while 多重绑两家同 RegisterRedefinition（101/103 原形，记 H16；for 形可用）；rest+缺省混合短调拒（沿 step71 门）；`while(1)` 薄口曾落裸 `br 1`（step147 已修）；map/filter 链须函数内（H12 同族）。

## 第七批（121–140）

| demo | 特性 | step/台账 |
|---|---|---|
| 121_second_max | 次大值分支 | 2 |
| 122_rotate | slice+concat 轮转 | 26 |
| 123_merge | 归并+尾部补齐 | 7 |
| 124_slug | 串长/首字/下标 | 25 |
| 125_fact | 递归阶乘 | 7 |
| 126_clamp | min/max 嵌套钳位 | 23 |
| 127_sorted | every 有序判定 | 26 |
| 128_sieve | 埃氏筛（非常量步进） | step148 增量释放修复 |
| 129_range | 区间求和 | 7 |
| 130_vowels | 元音计数+形参串== | 25 |
| 131_super | super 方法调用 | 34 |
| 132_map_count | Map 读改写计数 | 30 |
| 133_area | 接口形参求值 | 32 |
| 134_gcd_loop | while 辗转相除 | 7 |
| 135_pow_loop | 循环乘方 | 7 |
| 136_concat_all | concat 合并 | 26 |
| 137_tri | 三角数 | 7 |
| 138_diag | 嵌套下标对角线 | 80 |
| 139_wordlen | 空格下标 | 25 |
| 140_tally | filter 偶数统计 | 26 |

已知边界（续6）：串 `+=` 累加重绑记 H13（124 改只读形）；构造缺省短调 new 侧元数门（沿 step71）；`for(j..;j+=i)` 与 `j=j+i` 同经增量释放修复。

## 第八批（141–160）

| demo | 特性 | step/台账 |
|---|---|---|
| 141_sum_sq | 平方和 | 7 |
| 142_dot | 点积 | 7 |
| 143_mat_add | 矩阵加双循环 | 80 |
| 144_transpose | 2x2 转置构造 | 80 |
| 145_strcmp | 串字典序 | 25 |
| 146_mode | 众数双循环 | 7 |
| 147_median | toSorted 中位数 | 26 |
| 148_bit_count | 位计数 while | 129 |
| 149_super_args | super 传参构造 | 34 |
| 150_multi_default | 多缺省短调 | 71 |
| 151_obj_param | 接口形参 | 32 |
| 152_enum_calc | 枚举比较 | 27 |
| 153_ternary_chain | 三元链 | 84 |
| 154_rest_first | 首参+rest | 121 |
| 155_collatz | 考拉兹 while | 7 |
| 156_destructure_call | 数组解构 | 16 |
| 157_label_nested | 标号 continue 外层 | 14 |
| 158_do_sum | do-while 求和 | 3-11 |
| 159_gcd_sum | gcd 累加 | 7 |
| 160_pow2 | `**` 幂表 | 21 |

已知边界（续7）：函数体内嵌套函数声明拒（沿既有门）；bool switch 判别式拒（串 switch 有，bool 另立）；本批零 Go 改动。

## 第九批（161–180）

| demo | 特性 | step/台账 |
|---|---|---|
| 161_concat3 | 三元 concat | 26 |
| 162_at_neg | at 负下标 | 26 |
| 163_slice_str_neg | 串负 slice | 25 |
| 164_num_sep | 大数除法 | 7 |
| 165_chain | 链式赋值 | step150  rebinding 修复 |
| 166_splice_do | 单参 slice | 26 |
| 167_map_clear | Map clear | 30 |
| 168_set_year | Date setter 变异 | step150 setter 修复 |
| 169_neg_idx | 尾下标读 | 12 |
| 170_iife | 箭头直接调用 | 111 |
| 171_greet_cls | 串字段方法返回 | 57 |
| 172_sum_2d | 二维求和 | 77 |
| 173_fib_loop | 滚动 fib | 78 |
| 174_count_even | for-of 偶数计数 | 13 |
| 175_min3 | 三元取小 | 7 |
| 176_pow_sum | 幂求和 | 21 |
| 177_str_walk | 码点累加 | 25 |
| 178_obj_sum | sort+slice 分步 | 26 |
| 179_div_mod | 整除取模 | 7 |
| 180_leap | 闰年判定 | 7 |

已知边界（续8）：fill 三元拒（单参门）；前后缀自增值位两家同 UseAfterMove（记 H17）；逗号表达式拒（门）；链式 slice `.length` 拒（分步）；`Date.getTime` i64 拒（门）。

## 第十批（181–200）·收尾

| demo | 特性 | step/台账 |
|---|---|---|
| 181_gcd_all | 多组 gcd | 7 |
| 182_lcm_all | lcm 组合 | 7 |
| 183_prime_upto | 30 内素数计数 | 69 |
| 184_matrix_mul | 2x2 矩阵乘对角 | 80 |
| 185_str_join2 | 串数组 for-of 判定 | 139 |
| 186_max3 | min/max 嵌套 | 23 |
| 187_binary | 二进制累积 | 129 |
| 188_select | 变量下标读 | 12 |
| 189_nest_if3 | 三层符号判定 | 7 |
| 190_sort_desc | reverse 降序 | 26 |
| 191_fizz20 | 20 内整除计数 | 7 |
| 192_sum_even | filter+for-of 求和 | 26 |
| 193_map_dbl | map 翻倍 | 26 |
| 194_class_pair | 双字段类 | 28 |
| 195_swap | 交换 | 7 |
| 196_str_len_sum | 串形参长和 | 57 |
| 197_and_or | 逻辑值打印 | 17 |
| 198_while_sum2 | 倒序求和 | 7 |
| 199_for_step2 | 步进 2 求和 | 66 |
| 200_finale | 递归 fib+排序综合 | 7/26 |

已知边界（续9）：串数组元素直打两家同出句柄数（185 改 for-of 判定形）；本批零 Go 改动。本轮另将 step150 的 44 个旧 sai 释放行一并进仓（纯 +71 行 `!`，值流零变）。

## 收尾（200/200）

- `run.sh` 200/200 PASS，`run.sh --check` 200/200 SA-CLEAN（sai 禁止手改）。
- 286 差分门禁：286 一致、零分歧。
- `go build ./...` + `testrunner` + `go vet transpile/` 全绿；gofmt 仅旧 4 文件。

## 补批（201–202，H 缺口回归）

| demo | 特性 | step/台账 |
|---|---|---|
| 201_for_in | for-in 下标巡回求和 | step153（H11 快照修复） |
| 202_nested_forof | 嵌套数组标识符巡回 | step153（H15 行绑 arr） |

## 补批（203–205，node 插件投影）

| demo | 特性 | step/台账 |
|---|---|---|
| 203_node_path | path 归一/取目录/扩展名/绝对判定/拼接 | step156（node.sai 复用） |
| 204_node_str | querystring 编解码 + punycode 往返 | step156（node.sai 复用） |
| 205_node_buf | Buffer.concat + randomBytes 定长 | step156（node.sai 复用） |

插件 demo 须带 `sa.mod`（`require_plugin node @0.1.0 abi 1`），run.sh 有该文件时自动加 `--project-root` 供 bare node.sai 解析。已知边界（续10）：Buffer.byteLength 需 u64（子集无此种，大声拒）；console.timeEnd 值位需 f64 调用（通用 f64 值缺口）；Deno 未动。
