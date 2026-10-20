---
name: sa-vm
description: Use packaged native function tools (find_files, list_dir, grep_search, grep_search_async, git_status, git_diff, find_references, find_implementations, file_outline, workspace_outline, repo_map, code_graph, relation_graph, symbol_graph, find_definition, run_host) for fast and secure operations. Plus JEV autonomous decision helpers (jev_choose, jev_checklist, jev_thinking, jev_redteam, jev_rank, jev_scope, jev_dispatch, jev_consistency, jev_done, jev_diagnose, jev_edge_cases, jev_blast_radius, jev_intent, jev_next, jev_compress, jev_progress, jev_memory) for fork decisions, delivery sign-off and risk review. Use when inspecting, reading, writing, editing, patching, tail-reading logs, or searching files and code in the workspace.
---

<!-- generated-by-codex-ultra:sa-vm -->

# 封装函数工具使用指南 (Packaged Function Tools Guide)

【安全守护状态：已开启】
本工作区已开启安全策略与封装原生工具支持。**绝不手写脚本代码（严禁在调用中传递 `source` 或 `language`，只用封装动作）！**
大模型必须直接调用 `sa_vm_run` 传入当前已启用的封装动作参数（`action`），底层将以原生极速毫秒级执行。**【宿主终端管控】**：严禁直接调用宿主系统 Shell、Terminal、Node、Bun、Python 执行脚本或命令，所有执行与文件操作必须通过 `sa_vm_run`。

## 1、软件工程工具效率决策矩阵与反模式拦截 (Tool Efficiency & Anti-Patterns)

面对工作区提供的丰富封装工具，严禁“大炮打蚊子”或使用低能效手段。必须严格遵照以下效率决策矩阵，选出性能最高、上下文开销最小的工具：

### 1. 软件工程工具效率决策矩阵 (Tool Efficiency Decision Matrix)

| 意图与工程场景 | 推荐最高效工具 (Recommended) | 严禁采用的低效/危险反模式 (Forbidden Anti-Pattern) | 能效优势与核心原由 (Rationale) |
| **查符号 / 函数 / 类定义与实现** | `find_definition` (首选) / `symbol_graph` | 严禁 `grep_search` 广域盲搜，严禁全文 `read_file` | AST 单步直达源头定义与实现片段预览，避免大文件扫描与海量无关字符串匹配，节省 90%+ 上下文 |
| **查函数 / 符号调用方与被引用处** | `find_references` (首选) / `relation_graph` | 严禁循环 `grep_search` 或写脚本逐个读文件正则拼凑 | 毫秒级返回全工程精准引用与调用方列表（支持轻量行号索引），极速定位所有受影响处 |
| **快速提取文件 / 工程结构大纲** | `file_outline` (单文件) / `workspace_outline` (工程级) / `code_graph` | 严禁盲目调用 `read_file` 吞入几千行完整源码 | 仅提取类、函数、接口声明骨架与签名，零上下文浪费掌握代码架构 |
| **阅读指定行范围代码** | `read_lines` (指定 start_line / end_line) | 文件 > 500 行严禁 `read_file` 全量读取 | 定向精确获取目标行，永不触发大文件上下文溢出保护截断 |
| **排查错误日志 / 查看文件末尾** | `read_file_tail` (末尾流式直读) | 严禁使用 `read_file` 或终端 `cat` 读整个大日志 | 毫秒级反向读取最后 N 行，极速定位最新崩溃与异常栈 |
| **精确单处代码替换** | `edit_file` (精确定位唯一旧文本) | 严禁手写脚本覆盖，或把数千行全文件用 `write_file` 重写 | 原生带安全备份，保证幂等原子替换，杜绝全文件覆写引发的并发冲突与丢代码 |
| **跨文件批量修改 / 复杂重构** | `apply_patch` (标准 Unified Diff) | 严禁连续调用多次碎片化 `edit_file` | 一次性原子应用多文件差异，自带边界检验与失败安全回滚 |
| **确认文件存在 / 统计行数** | `file_info` / `count_lines` | 严禁为了数行数或判存在而去全量 `read_file` | 底层原生物理磁盘直读（stat/wc），0 Token 消耗，绝对真实零缓存 |
| **查找文件路径 / 遍历目录** | `find_files` / `list_dir` | 严禁在终端运行 `Get-ChildItem -Recurse` 或 `find` | 带 depth 与 max_entries 保护，毫秒级结构化返回，避免终端编码乱码与输出刷屏 |
| **代码语义与架构风险审查** | `jev_redteam` / `jev_blast_radius` | 审查代码严禁只传总结摘要，**必须传真实 git diff 原文** | 细节决定成败，摘要会掩盖潜在副作用（如未捕获异常、竞态），导致裁判失准 |

### 2. 核心避坑三原则 (Core Rules)
1. **能用 AST / 图谱工具解决的（找定义/引用/调用关系），坚决不正则 grep**。
2. **能定向切片或读尾部的（read_lines / read_file_tail），绝不全文 read_file**。
3. **单处修改优先 edit_file，多处/多文件联动改动优先 apply_patch，绝不用 write_file 覆写存量大文件**。

## 2、文件读取与检索工具

### 1. 通配符查找与遍历文件 (find_files)
在工作区中搜索匹配通配符或路径正则的文件列表（彻底对等替代 PowerShell `Get-ChildItem | Where-Object`）：
```json
{
  "action": "find_files",
  "dir": ".",
  "pattern": "*.zig",
  "path_pattern": "sci.*sab|src.*sab"
}
```
- `dir` (可选): 搜索起始目录，默认当前目录 `.`。
- `pattern` (可选): 通配符匹配模式（例如 `*`、`*.ts`、`*.zig`），默认 `*`。
- `path_pattern` (可选): 路径过滤正则表达式或模式（如 `sci.*sab|src.*sab`），对等替代 `Where-Object FullName -Match ...`。
- `max_entries` (可选): 最多返回文件数，默认 100。
- `max_depth` (可选): 搜索最大层级深度，默认 5。
- 分页 (可选): `page` / `page_size` / `offset` / `count_cap`，见上文统一分页约定；分页时返回 `{ page, page_size, total, total_pages, files }` 信封。

> `list_dir` 同样支持 `pattern` / `path_pattern` / `exclude_dirs` / `respect_gitignore`（默认 `true`，自动跳过 `.gitignore` 与 `node_modules/.git/dist/release` 等），输出按文件名排序、截断确定，`stderr` 会注明跳过的忽略项数量。大目录务必带 `depth` + `max_entries` + `type`。
>
> **统一分页约定（搜索/诊断系通用）**：`find_files`、`list_dir`、`grep_search`、`grep_search_async`、`find_references`、`find_implementations`、`workspace_outline`、`repo_map`、`workspace_diagnostics`、`verify_loop`、`read_notes` 均支持 `page`（从 1 开始，兼容 `page_num`/`pageNum`/`p`）+ `page_size`（兼容 `pageSize`/`per_page`/`perPage`；缺省回落到各方法原 limit，如 `max_matches`/`max_results`/`max_entries`/`max_errors`/`max_files`）+ `offset`（显式传则优先于 `page` 推导）+ `count_cap`/`max_total`（分页扫描上限，默认 2000，最高 10000）。不传分页参数时输出与原来完全一致（legacy 格式）；传了则返回分页信封：`page`、`page_size`、`total`、`total_pages`（`ceil(total/page_size)`）、`returned`、`has_next`、`has_prev`、`truncated`。`total` 在 `count_cap` 内精确，超 cap 或超时则 `truncated: true` 且 `has_next: true`。翻页前结果已按 `file:line`（或路径）排序，页之间不重不漏。

### 2. 代码关键字与正则表达式文本搜索 (grep_search)
在工作区文件或指定单文件中快速搜索包含指定文本或正则表达式的行（**原生支持正则表达式**如 `a|b|c`、`\bfoo\b`，彻底对等替代 PowerShell `Select-String`）：
```json
{
  "action": "grep_search",
  "query": "three_await|linear|fallthrough",
  "dir": "src",
  "pattern": "*.ts"
}
```
搜索单个文件日志或构建产物（对等替代 `Select-String -LiteralPath 'file.log'`）：
```json
{
  "action": "grep_search",
  "path": "tests_run.log",
  "query": "FallthroughForbidden|basic blocks must end|fallthrough",
  "max_matches": 10
}
```
- `query` (必填): 搜索关键词或正则表达式（包含 `|`、`\b` 等时自动启用正则）。
- `dir` / `path` (可选): 搜索目录或目标文件路径，默认 `.`。
- `pattern` (可选): 文件通配符过滤，默认 `*`。
- `path_pattern` (可选): 路径正则表达式过滤。
- `is_regex` (可选): 是否显式指定为正则表达式（默认对带正则语法自动识别）。
- `case_sensitive` (可选): 是否大小写敏感（默认 false）。
- `max_matches` (可选): 最多返回匹配项数，默认 50。
- 分页 (可选): `page` / `page_size` / `offset` / `count_cap`，见统一分页约定；分页时返回 `{ page, page_size, total, total_pages, matches }` 信封（`matches` 按 `file:line` 排序）。
- `timeout_ms` / `timeout` (可选): 执行超时限制（默认 15000ms）。支持毫秒（如 10000）或秒数（如 10），超时后安全截断返回，杜绝阻塞。
- `max_file_bytes` (可选): 单文件体积上限，默认 512KB。超限文件与二进制文件自动跳过并在 stderr 注明（专治打包产物/超大 bundle 卡死），可用 `read_lines` 切片改读。
- `exclude_dirs` (可选): 跳过的目录名或相对路径数组（如 `["platform-pkgs", "release"]`）。
- `respect_gitignore` (可选): 是否尊重 workspace 根 `.gitignore` 与内置黑名单（`node_modules/.git/dist/release/target/build` 等），默认 `true`。
- 大库食谱：先 `find_references({ symbol, detail: false })`，无命中再收窄 `dir + pattern`（如 `dir: "apps/server/src", pattern: "*.ts"`）走 grep，勿全盘 `dir: "."` 盲扫。

### 3. 高性能异步并发搜索 (grep_search_async)
在中大型工程中利用异步 I/O 并发扫描文件，支持单关键词或批量多关键词/正则并发检索（批量查找符号或错误排查首选）：
```json
{
  "action": "grep_search_async",
  "queries": ["calculateTotal", "processOrder", "handlePayment"],
  "dir": "src",
  "pattern": "*.ts",
  "concurrency": 16,
  "max_matches": 50,
  "timeout_ms": 15000
}
```
- `query` (可选): 单个搜索关键词或正则表达式。
- `queries` (可选): 批量搜索关键词或正则数组，一次性并发查找多个符号，大幅减少交互轮次。
- `dir` / `path` (可选): 搜索目录或单文件路径，默认 `.`。
- `pattern` (可选): 文件通配符过滤，默认 `*`。
- `path_pattern` (可选): 路径正则表达式过滤。
- `is_regex` (可选): 是否显式指定正则。
- `concurrency` (可选): 异步并发通道数，默认 16（范围 1-64）。
- `max_matches` (可选): 最多返回匹配项数，默认 50。
- 分页 (可选): `page` / `page_size` / `offset` / `count_cap`，见统一分页约定；语义与 `grep_search` 一致。
- `timeout_ms` / `timeout` (可选): 并发超时限制（默认 15000ms）。
- `max_file_bytes` / `exclude_dirs` / `respect_gitignore` (可选): 与 `grep_search` 同语义，大库批量检索必备。

### 4. 查找符号所有引用与定向探索 (find_references)
精确查找目标符号（函数、类、变量、类型等）在工程中的所有定义、模块导入与真实调用点。

#### 1. 极致省 Token 规范与数据格式：
- **路径唯一性**：每个文件路径作为唯一键（Key）在返回对象中仅出现一次，绝对不重复输出文件长路径。
- **显式紧凑枚举**：类型使用带 `e` 前缀的代码（杜绝与列号混淆）：
  - **`e0`** = **`def`**（定义/声明：如 `export function foo`、`const foo =`、`def foo`）
  - **`e1`** = **`imp`**（模块导入：如 `import { foo }`、`require`、`use`）
  - **`e2`** = **`use`**（调用/使用：函数调用、属性读取、JSX 标签使用等）

#### 2. 调用参数说明：
```json
{
  "action": "find_references",
  "symbol": "useThreadRuntimeSelections",
  "detail": false,
  "max_results": 50,
  "max_tokens": 3000,
  "timeout_ms": 15000,
  "candidate_files": [
    "apps/web/src/App.tsx",
    "apps/web/src/components/**/*.tsx"
  ],
  "dir": "apps/web/src",
  "exclude_comments": true,
  "include_declarations": true
}
```
- `symbol` (必填): 目标符号名称（严格基于全词词法边界 `\b` 匹配，绝不误报包含该前缀或后缀的长标识符）。
- `detail` (可选): **代码细节显隐开关**（默认 `false`）。
  - `false`（推荐，极限省 Token）：仅返回如 `"L42:e0"`、`"L156:e1"`、`"L2341:e2"`；
  - `true`：返回完整代码行，格式为 `"L156:e1:import { foo } from ..."`。
- `timeout_ms` / `timeout` (可选): **执行超时上限**（默认 15000ms 即 15 秒）。如果遍历超时，将优雅返回已找到的部分结果并标记 `"timed_out": true, "truncated": true`，杜绝大仓库扫描卡死。
- `max_results` (可选): 最多返回的引用总条数（默认 50，最高 200）。防止全局公共函数几千处调用撑爆上下文，超出时返回 `truncated: true`。
- 分页 (可选): `page` / `page_size` / `offset` / `count_cap`，见统一分页约定；分页时额外返回 `page`、`page_size`、`total_pages`、`has_next`、`has_prev`，`files` 只含当页条目（`find_implementations` 的 `implementations` 列表同步切片）。
- `max_tokens` (可选): Token 预算安全熔断上限（默认 3000），物理级限制返回体积。
- `candidate_files` / `files` (可选): **候选文件或通配路径列表**。大模型可指定怀疑有调用的目标文件，系统直接秒级穿透该批文件，无需全盘耗时扫描。
- `dir` (可选): 未指定候选文件时的搜索根目录，默认当前目录 `.`。
- `pattern` (可选): 文件过滤模式，默认涵盖主流编程语言源文件。
- `exclude_comments` (可选): 是否自动过滤单行注释（`//`、`#`）与块级注释（`/* ... */`），默认 `true`。
- `include_declarations` (可选): 是否在结果中包含定义处自身（`e0`），默认 `true`。

#### 3. 典型返回样例 (当 detail: false)：
```json
{
  "symbol": "useThreadRuntimeSelections",
  "total": 4,
  "returned": 4,
  "truncated": false,
  "types": { "e0": "def", "e1": "imp", "e2": "use" },
  "files": {
    "apps/web/src/app/runtime/useThreadRuntimeSelections.ts": [
      "L42:e0"
    ],
    "apps/web/src/App.tsx": [
      "L156:e1",
      "L2341:e2",
      "L4210:e2"
    ]
  }
}
```
> 大模型可先通过 `detail: false` 快速获取所有引用所在的文件与行号；如需阅读某一处实现，再通过 `read_lines` 切片精准读取，极具成本效益！

### 5. 多语言文件符号大纲提取 (file_outline)
基于工业级 Tree-sitter 语法树引擎，秒级提取任意源文件（Rust, TS/JS, Python, Go, Zig, C/C++ 等）的顶层类、函数、接口、结构体、枚举与方法大纲及行号范围。

#### 1. 极致省 Token 规范：
- 面对大文件（数百至数千行）时，**强烈建议先调用 `file_outline`**，获取文件骨架结构（通常仅数十行文本、几十 Token），避免直接全量读取大文件挤爆上下文。
- 确定目标函数/类的行号范围（如 `[L85-120]`）后，再定向调用 `read_lines` 阅读目标逻辑。

#### 2. 调用参数说明：
```json
{
  "action": "file_outline",
  "path": "src/services/user.rs",
  "detail": false
}
```
- `path` (必填): 目标代码文件相对路径。
- `detail` (可选): 是否包含更深层次内部成员（如枚举项、结构体内部字段等，默认 `false`）。
- `max_tokens` (可选): 输出 Token 上限，默认 4000，超限自动截断并提示翻页（`read_lines` 定向续读）。
- `max_file_bytes` (可选): 单文件体积上限，默认 1MB，超限/二进制文件拒绝并指引切片读取。

### 6. 工程级批量大纲与批量读取 (workspace_outline / read_many)
- `workspace_outline`: 一次调用拿下整个目录的结构地图（gitignore 感知、跳过大文件/二进制、按 `max_tokens` 熔断），大库探索先看地图再定点 `read_lines`。
```json
{
  "action": "workspace_outline",
  "dir": "apps/server/src",
  "pattern": "*.ts",
  "max_files": 20,
  "max_symbols_per_file": 50,
  "max_tokens": 6000,
  "exclude_dirs": ["platform-pkgs", "release"]
}
```
- `dir` (可选): 起始目录，默认 `.`；`pattern` / `path_pattern` (可选): 文件与路径过滤；`max_files` (可选): 最多大纲文件数，默认 20（最高 100）；`max_symbols_per_file` (可选): 单文件最多顶层符号数，默认 50；`max_tokens` (可选): 总 Token 上限，默认 6000。
- 分页 (可选): `page` / `page_size` / `offset` / `count_cap`，见统一分页约定；分页时只 outline 当页候选文件，表头注明 `page X of Y` 与总数。
- `read_many`（批量读取，见上文读取工具章节）可配合地图做定点精读。

### 7. 全局符号与架构图谱 (repo_map)
基于高性能 AST 分析与原生 Glob 文件系统快速构建工程级全量符号地图与依赖图谱：
- **全局上帝视角**：提取全量符号定义（函数、类、结构体、接口）、依赖关系（`depends_on`）与模块边界（`module`），毫秒级感知全局代码拓扑。
- **极速增量索引缓存**：进程内缓存各文件 AST 解析结果，文件未变动时毫秒级瞬时返回；支持 Tree-sitter 并行加速。
- **按需调用方探索**：支持传入 `include_used_by: true` 获取关键符号的轻量调用方清单，辅助跨文件重构定位。

#### 调用参数说明：
```json
{
  "action": "repo_map",
  "dir": "apps/server/src",
  "max_files": 200,
  "max_tokens": 8000,
  "include_used_by": false
}
```
- `dir` (可选): 扫描起始目录，默认 `.`；
- `max_files` (可选): 最大扫描文件数，默认 200（最高 500）；
- `max_tokens` (可选): 总 Token 预算上限，默认 8000，超限自动安全截断；
- 分页 (可选): `page` / `page_size` / `offset` / `count_cap`，见统一分页约定；分页时返回 `total`、`total_pages`、`returned`，`entries` 只含当页文件；
- `include_used_by` (可选): 是否包含符号的轻量级被引用关系分析（默认 false，开启时精准提取被哪些文件使用）。

### 8. 架构拓扑与调用链一次性提取 (code_graph)
基于 Tree-sitter WASM 语义分析，按需在内存中毫秒级提取类继承 (extends)、接口实现 (implements)、成员所有权 (owns)、模块引用 (imports)、函数/方法调用流 (calls) 拓扑。首要价值是替代多次循环 grep_search + read_file 拼凑关系：一次调用即得全局骨架。DOT/py_stub/networkx 只是紧凑文本载体，默认 dot 最省 Token。工具别名支持 `relation_graph` 与 `symbol_graph`。

#### IRON RULE（触发铁律）：
- 当需要理清类继承树、多模块 import 网络或跨函数调用链路时，必须优先调用 code_graph 一步获取拓扑，严禁连续执行多次 grep_search 或循环 read_file 逐个文件拼凑调用关系。
- file_outline 看单文件结构、find_references 看单符号引用；只有 code_graph 能一次给出多跳上下游拓扑。

#### 1. 使用场景：
- 理解类继承体系（如查看子类有哪些父类、实现了哪些接口、包含哪些复合成员）；
- 探查文件或模块的上下游依赖关系（upstream/downstream/both）；
- 追溯函数或类方法的调用拓扑（call_flow，如查看执行路径调用了哪些辅助函数）；
- 重构大型项目前快速获取局部架构骨架（无需先建全量索引或占用硬盘）。

#### 2. 调用参数说明：
```json
{
  "action": "code_graph",
  "target": "CustomEngine",
  "mode": "symbol_hierarchy",
  "depth": 2,
  "format": "dot"
}
```
- `target` (必填): 要分析的符号或模块——类名/函数名/符号名（如 `CustomEngine`、`processOrder`）或模块相对路径（如 `src/index.ts`）。想查“谁继承/调用它”直接传符号名，不要误传成写入目的地路径；同时兼容 `symbol`/`path` 别名。
- `mode` (可选): 
  - `symbol_hierarchy`（默认）：类继承/接口实现/成员所有权拓扑；
  - `module_dependency`（别名 `module_deps`）：模块 import 引用网络；
  - `call_flow`：函数或类方法的上下游调用网络。
- `depth` (可选): 遍历深度，默认 `2`，上限 `5`。
- `direction` (可选): `upstream`（上游依赖）、`downstream`（下游依赖）或 `both`，仅在 `module_dependency` 下有效。
- `format` (可选): 输出格式：`dot`（Graphviz DOT 语言，默认，最省 Token）、`py_stub`（Python 架构骨架存根）或 `networkx`（Python NetworkX 构建脚本）。
- `output_file` (可选): 若需显式保存到磁盘可指定输出路径（默认仅内存计算返回，绝不产生临时文件）。
- `candidate_files` (可选): 限定候选文件路径数组加速精准分析。

#### 3. DOT 边样式说明：
- `extends`: 实线普通箭头 (`->`)；
- `implements`: 虚线箭头 (`style="dashed"`)；
- `owns`: 菱形箭头 (`arrowhead="diamond"`)；
- `calls`: 点状虚线箭头 (`style="dotted"`)；
- `imports`: 实线普通箭头带有 `label="imports"`。

### 9. 精准定义查找与函数体直达 (find_definition)
彻底解决 Agent 为了解一个函数实现而盲目全文 read_file 几百上千行浪费上下文的痛点！
- **单步直达源头**：支持跨文件追溯 import 源头，精准返回目标符号的声明位置、代码行范围（`[Lstart-Lend]`）与完整函数/类签名。
- **内置实现预览**：自动携带函数体/定义体核心实现片段（默认最多 40 行），无需再次调用 read_file 即可秒知其内部结构与实现！

#### 调用参数说明：
```json
{
  "action": "find_definition",
  "symbol": "extractFileOutline",
  "path": "apps/server/src/saVmTool.ts",
  "include_body": true
}
```
- `symbol` (必填): 目标符号/函数/类名。
- `path` (可选): 起始文件。若该文件通过 import 引用目标符号，引擎会自动顺藤摸瓜追踪至源定义文件；若省略，在工作区快速检索。
- `include_body` (可选): 是否包含函数体实现代码（默认 `true`）。

### 10. 只读 Git 状态与增量 (git_status / git_diff)
工作区是否干净、改了什么，优先用原生只读 git 动作（无 shell、argv 白名单、截断保护）；`run_host git` 仅在沙箱拦截开启（`replaceExec`）时拦截高危破坏性命令（`reset --hard`、`clean`、`revert`），其余 clone/checkout/add/commit/pull 均放行；沙箱关闭时终端与 run_host 均完全开放、零拦截。严禁 `python -c subprocess(git)`、`bun -e Bun.spawnSync(git)`、写临时脚本跑 git、直读 `.git/HEAD|config|FETCH_HEAD`：
```json
{ "action": "git_status" }
{ "action": "git_diff" }
{ "action": "git_diff", "staged": true }
{ "action": "git_diff", "stat_only": true }
{ "action": "git_diff", "path": "apps/server/src/miniBridge.ts", "context": 3 }
```
- `git_status`：分支、上游、ahead/behind、staged/unstaged/untracked 分类；`repo` 为仓库内任意路径（默认 cwd），向上找 `.git` 不越 workspace root。
- `git_diff`：未提交增量（默认 unstaged；`staged:true` 看暂存区；`stat_only:true` 只看统计；`path` 限定单文件/目录；`context` 0-10；超限截断并标 `truncated:true`）。
- 收到 `use git_status/git_diff instead` 拒绝时立即改调原生动作，不要换姿势重试。

## 3、批处理并行任务 (parallel，已暂时屏蔽)
**`parallel` 已暂时屏蔽（鸡肋）：一律用同一轮多个 `sa_vm_run` 原生多调用做并发，批量写入用串行 `edit_file/write_file` 或一次 `apply_patch`，不要发 `parallel`（服务端直接 228 打回，`SA_VM_ENABLE_PARALLEL=true` 才可恢复）：

### 1. 混合并发（已降级：优先原生多调用 + grep_search_async）
`run_host --help` + 代码探查请拆成同一轮多个 `sa_vm_run`；同义词检索优先一次 `grep_search_async({queries:[...]})`。`parallel` 混合批仅作兜底：
```json
{
  "concurrency": 4,
  "parallel": [
    { "action": "run_host", "exe": "bun", "args": ["--help"], "head_lines": 20 },
    { "action": "grep_search", "pattern": "*.zig", "query": "LinearAwait" },
    { "action": "list_dir", "path": "src", "depth": 1 }
  ]
}
```
**批处理依赖红线**：同批内严禁出现“写/构建 -> 读取产物”的同批因果依赖（例如同一 `parallel` 数组中包含编译生成 `test.out` 的 `run_host`，同时包含读取 `test.out` 的 `read_file`）。若有因果依赖，必须拆分成前后两次独立的串行批次，同批依赖冲突将被系统直接拦截打回。

## 4、严格准则与安全保障
1. **绝不手写脚本代码**：永远只使用上述已启用的封装动作（`action: "find_files" | "list_dir" | "grep_search" | "grep_search_async" | "git_status" | "git_diff" | "find_references" | "find_implementations" | "file_outline" | "workspace_outline" | "repo_map" | "code_graph" | "relation_graph" | "symbol_graph" | "find_definition" | "run_host"`），绝不手写 TS 源码或传递 `source`。
2. **白名单策略保障**：所有文件读写、补丁应用与网络请求均受到当前对话策略白名单保护，超出范围的路径请求将被自动拒绝。
3. **安全自动备份**：修改或覆盖文件（`apply_patch`、`write_file`、`edit_file`、`delete_file`）前，系统会在底层自动对原文件执行安全备份。
4. **宿主系统工具协同**：当通过系统工具调用 `apply_patch` 或执行读写时，安全守护 Broker 会透明进行权限判定与原子备份，与封装工具无缝协同。
5. **查外部库四步曲**：严禁在终端用 `Get-ChildItem` 盲猜外部绝对路径或深层递归。标准步骤：1. `list_dir({ dir: LIB_ROOT, max_entries: 20 })` -> 2. `find_files({ dir: LIB_ROOT, pattern: 'target.ts' })` -> 3. `grep_search({ path: 'found_path', query: 'key' })` -> 4. `read_lines({ path: 'found_path', start_line: X-20, end_line: X+20 })`。严禁全量读大文件后再在终端过滤。
6. **写-运-读三步曲**：构造测试与运行验证必须分步明确：步骤一 `write_file` 生成测试用例 -> 步骤二 终端单条原生命令运行验证（如 `bun run ./script.ts`，严禁 `;` 链式拼接或 `Out-Host`） -> 步骤三 `read_lines` / `read_file_tail` 结构化查看产物。
7. **文档检索固定食谱**：查找 SDK/模块文档时，先 `find_files` 定位 markdown -> `grep_search` 定位章节行号 -> `read_lines` 切片正文。严禁检索渲染后 HTML 或在终端跑 `Select-String -Context` 产生巨量噪声。
8. **严禁终端排版与静默吞错 (NO FORMAT & NO SILENTLY CONTINUE)**：
   - 严禁在终端运行 `Format-Table`, `Format-List`, `Format-Wide`, `Out-String`，JSON 即最终格式。
   - 严禁在终端使用 `-ErrorAction SilentlyContinue` 或 `2>$null` 掩盖文件错误。检查文件必须使用 `sa_vm_run({ action: 'test_path', path: '...' })`。

## 5、JEV 主动决策助手 (opt-in, advisory only)
大模型自主评估决定何时做选择题，无需等待外部钩子触发；JEV 基于 SystemOne 引擎提供毫秒级高置信度离散裁决，结果作为专家建议辅助思考，最终决策权在你。
- **分叉单选**：`sa_vm_run({ action: 'jev_choose', question: '...', options: { a: '...', b: '...' }, context: '...' })`（options 2~6 项，context 自动截断安全保护）。当面对 2~6 种设计/实现方案分叉、架构选型、降级处理或边界歧义时调用，JEV 返回最优方案与置信度。
- **交付多选自检**：`sa_vm_run({ action: 'jev_checklist', checks: { no_placeholders: '...', error_handling: '...' }, context: '...' })`（checks 1~8 项）。在复杂修改完成交付或提交前，主动列出多项准则并发验证，返回逐项 pass/fail 与总体结论。
- **思考前裁决**：`sa_vm_run({ action: 'jev_thinking', goal: '...', thoughts: { t1: '...', t2: '...' }, context: '...' })`（thoughts 1~4 条）。展开长思考前列出候选思路交 JEV 单选，内置 `__act_now`（已有足够信息则直接行动，避免过度思考）；判走某条就去想，单轮只调 1 次，不连环追问。
- **红队找茬**：`sa_vm_run({ action: 'jev_redteam', target: '...', context: '...' })`。高风险动作（迁移、鉴权、安全边界、上线）前把方案交 JEV 审：返回最可能翻车点（固定 8 类风险）+ proceed / harden_first / stop 裁决；补的是你没列出来的盲区。
- **多项排序**：`sa_vm_run({ action: 'jev_rank', items: { a: '...', b: '...' }, criterion: 'ROI', context: '...' })`（items 2~6 项）。按给定标准排前三，未入围的原序附后；技术债、重构 backlog 按性价比排序用它。
- **范围把关**：`sa_vm_run({ action: 'jev_scope', question: '<task>...', options: { p1: '...' }, context: '...' })`。规划阶段先审体量（合适/拆 2~3/拆多/MVP/合并），给了候选阶段再指出最弱环节；避免 epic 直接开干事后返工。
- **分配前门禁（Task/subagents 必问）**：`sa_vm_run({ action: 'jev_dispatch', question: '<task>...', options: { a: '...' }, context: 'max_parallel=3; 依赖说明', target: 'slots=3' })`（options 2~6 项可省）。并行派 subagents/Task 前必须先过此门：返回 single/parallel/sequential/parallel_batches + 最弱环节 + parallelism + dispatch_plan；按 plan 执行，single 则不派。
- **一致性审计**：`sa_vm_run({ action: 'jev_consistency', plan: '...', constraints: { no_mock: '...' }, context: '...' })`（constraints 1~8 项）。审计划本身是否违反硬约束，逐项 holds / violated / unclear；区别于查证据的 checklist。
- **交付核准**：`sa_vm_run({ action: 'jev_done', user_request: '...', actual_work: '...', evidence: '...' })`。说"做完了"之前先核：原始需求 vs 实际工作 vs 验证证据，判 truly_completed / incomplete_with_todos / unverified_claim / diverged + deliver / keep_working；专治留 TODO 假装交付。
- **根因仲裁**：`sa_vm_run({ action: 'jev_diagnose', error_summary: '...', hypotheses: { race: '...' }, proposed_fix: '...', context: '...' })`（hypotheses 2~4 个）。报错时先判最可能真因，再判拟定修复是治本 root_cause_fix、治标 symptom_patch 还是掩盖 risky_mask；治标前先过这一关。
- **边界筛查**：`sa_vm_run({ action: 'jev_edge_cases', code: '...', context: '...' })`。把实现扔给 JEV，指出 5 类边界（数值/空与缺省/编码符号/重入幂等/中断超时）里最脆弱的 1~2 项，照着补断言，别只测 Happy Path。
- **爆炸半径审查**：`sa_vm_run({ action: 'jev_blast_radius', diff: '...', target_component: '...', context: '...' })`。合入/提交重要 diff 前审副作用（纯内部/api_break/state_leak/perf_cliff）+ safe_to_apply / needs_compat_shim / needs_regression_tests。注意：方案用红队，diff 用 blast。
- **意图分析**：`sa_vm_run({ action: 'jev_intent', question: '<用户原话>...', options: { a: '...' }, context: '...' })`。收到用户输入后先判意图：给了候选解读（options 2~6 项）就单选最可能+判清晰度；不给就按内置 8 类意图分类+判 clear / needs_clarification / ambiguous，并给建议第一步。先过这一关再动手，少返工。
- **续接下一步**：`sa_vm_run({ action: 'jev_next', question: '<刚完成的工作>...', options: { a: '...' }, context: '<剩余 backlog>...' })`。任务完成后别闲着：给了候选后续（options 2~6 项）就挑最佳下一步+判动量；没给就按上下文 backlog 判 continue_now / confirm_first / stop，auto_continue 为 true 时直接开干下一步。
- **门控工具历史压缩**：`sa_vm_run({ action: 'jev_compress', mode: 'subscription' | 'token' | 'auto', threshold: 85 })`。返回四步压缩计划（checklist 门卫 -> save_note 外化要点 -> clear_tool_history(keep>=2，只剪只读类) -> 按需回取）；需同时开启 §6 clear_tool_history 能力。订阅制每 phase 边界剪，Token 制到阈值才剪。
- **任务进度裁决**：`sa_vm_run({ action: 'jev_progress', question: '<任务>...', options: { p1: '...' }, content: '<已完成>...', context: '...' })`（task -> question，phases -> options，completed -> content）。明确任务执行中按需查一次进度：返回进度带（0–25/25–50/50–75/75–99/100）+ on_track / at_risk / blocked / done + 下一步；只在大重构/多阶段手动触发，常态任务别每轮调。
- **记忆分层裁决**：`sa_vm_run({ action: 'jev_memory', content: '<候选记忆>...', context: '<来源任务>...' })`。收尾前判一条事实是否值得进长期记忆：返回 skip（临时状态/一次性输出）/ workspace（仅当前工程复用，save_note scope=workspace）/ global（跨工程通用偏好与约定，save_note scope=global）；判 workspace/global 后必须调 save_note 落盘。
- **节流与自主性**：自主评估必要性，单轮按需调用（通常 1~2 次），超时或网络异常自动返回诊断（fallback: model_decide），绝不循环追问或阻塞流程。各方法可在增强工具对话框第 7 节单独开关；JEV 对话框总开关关闭且未逐项开启时本节隐藏。

## 6、Opencode relay 与 serve 接线 (opencode backend only)
本节仅在 `cliBackend === "opencode"` 时生效；codex/mini/claude/agy/grok/pi 走各自 bridge，本节只读不执行。
- **OPENCODE_CONFIG_CONTENT 注入**（`agentRuntime/opencodeCompatibleProcessManager.ts:126`）：每次拉起 `opencode serve` 由服务端组装 JSON：`permission`（dangerous 为 `{"*":"allow"}`，否则 `{"*":"ask"}`）、`compaction:{auto,prune,reserved:20000}`、`mcp` 双形状（v1 `mcp.sa-vm` + v2 `mcp.servers.sa-vm`，任一 CLI 版本都能解析到 `sa-vm` MCP）、relay 时追加 `provider.codex-relay`（`npm:@ai-sdk/openai-compatible`，`baseURL/apiKey/models`）。
- **codex-relay 中转目标**（`opencodeCompatibleRuntime.ts:411` + `opencodeBridge.ts:108`）：relay 模式按 turn 解析 `resolveRelayTarget`，经回环 `codex-relay` provider 下发 `model:{providerID:"codex-relay"}`；`thread/read` 必须回显 `cliBackend=opencode` 才算路由正确，不可静默跑到 opencode-direct 模型。
- **serve 进程复用红线**（`opencodeCompatibleRuntime.ts:338`）：relay 目标或 SA-VM MCP 注入变化后，旧 serve 进程的 `OPENCODE_CONFIG_CONTENT` 已固化，必须重建 serve；严禁复用旧进程继续跑新 relay。
- **prompt 无 system 字段**（`opencodeBridge.ts:44`）：`prompt_async` 没有独立 system-prompt 字段，线程级 SA-VM developer instructions 按 `dev + "\n\n[User Instruction]\n" + prompt` 前置拼接。
- **CLI 发现**（`opencodeCompatibleProbe.ts:30`）：`OPENCODE_BIN / CODEX_UI_OPENCODE_BIN` > 用户首选 > 缓存最新 > PATH（npm 的 opencode.cmd/exe）> 官方安装目录兜底；模型列表解析 `opencode models --verbose`，首个 `/` 前为 provider。
- **skill 发现位**（opencode 源码 `packages/opencode/src/skill/index.ts:24`）：配置目录下 `{skill,skills}/**/SKILL.md`；本文件落盘到 `.opencode/skills/sa-vm/SKILL.md` 即被 opencode skill 工具自动发现，frontmatter `name: sa-vm` 不可改。
- **冒烟验证**（`scripts/opencode-relay-smoke.mjs`）：`thread/start(cliBackend=opencode)` -> `thread/read` 校验 -> `watch` -> `turn/start`（同 relay 选择，180s 等终态 `turn/completed`）-> `unwatch` -> `thread/delete` -> 关 session，diagnostics 归零（thread/watcher/activeTurnHold/subscribed/retrying/leaseBlocked 全 0）。
