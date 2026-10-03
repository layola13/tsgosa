# tsgosa/demos — TS 特性可运行展示集（用户进度检查用）

每个目录一个受支持特性：`main.ts`（tsgosa 方言）+ `main.sai`（生成的 SA，同进仓）+ `expected.stdout`（手写期望输出）。
`./run.sh` 跑全流水线并打印进度表：

```
main.ts --tsgo --sa--> main.sai --sa build-exe--> out/demo --run--> diff expected.stdout
```

- `out/`、`*.log` 为生成物（gitignore），`main.sai` 可直接打开看 SA。
- 单测某例：`./run.sh 09_array_methods 11_string_array_iter`
- 依赖：Go（编 tsgo）、sci 的 `sa` 二进制（`SA_BIN` 环境变量可覆盖，sci 下 `zig build -Dllvm=false` 构建）。

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

已知边界（demo 写法已规避，对应台账项）：串三元值仅 return 位可放（声明/赋值位拒，H10）；`?.length` 拒（用 `.length`）；`Color.Green` 成员值未用（用 i32 传枚举）；split 结果不可绑定/测长/迭代（用 join…注：join 可转译但 sci verifier 对两家同拒 PhiStateConflict，本集暂不用 join，待后端侧）；for-in 另有 UseAfterMove 缺口（H11，不在本集）。
