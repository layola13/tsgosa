---
name: jev
description: Use ONLY when the user asks for JEV judgment, adjudication, triage, ranking, or delivery review. Provides 17 structured decision actions via sa_vm_run (jev_choose, jev_checklist, jev_thinking, jev_redteam, jev_rank, jev_scope, jev_dispatch, jev_consistency, jev_done, jev_diagnose, jev_edge_cases, jev_blast_radius, jev_intent, jev_next, jev_compress, jev_progress, jev_memory). Do not use for plain file read/write/search; use sa-vm instead.
---

<!-- generated-by-codex-ultra:jev -->

# JEV Decision Skill

All actions run through `sa_vm_run({ action: "jev_*" })` (in Claude Code: `mcp__sa-vm__sa_vm_run`).
Each action is stateless, returns JSON, and never touches workspace files.

## Bridge-safe calling convention (REQUIRED)

The `sa_vm_run` bridge only forwards DECLARED params
(`question/options/checks/content/context/target/...`).
Canonical names like `goal/thoughts/items/task/phases/plan/code/diff`
are dropped in transit, so ALWAYS use the alias-safe shapes below.
`nativeJev*` in `apps/server/src/saVmJev.ts` maps aliases to canonical
semantics (`goal<=question`, `thoughts/items/phases/hypotheses<=options`,
`task/plan<=question`, `constraints<=checks`, `code/diff<=content`,
`target_component<=target`, `error_summary<=question`).

### 1. jev_choose
```json
{ "action": "jev_choose", "question": "...", "options": { "a": "...", "b": "..." }, "context": "..." }
```

### 2. jev_checklist
```json
{ "action": "jev_checklist", "checks": { "no_placeholders": "...", "error_handling": "..." }, "context": "..." }
```

### 3. jev_thinking (goal -> question, thoughts -> options)
```json
{ "action": "jev_thinking", "question": "<goal>...", "options": { "t1": "...", "t2": "..." }, "context": "..." }
```

### 4. jev_redteam
```json
{ "action": "jev_redteam", "target": "...", "context": "..." }
```

### 5. jev_rank (items -> options, criterion -> question)
```json
{ "action": "jev_rank", "question": "ROI", "options": { "a": "...", "b": "..." }, "context": "..." }
```

### 6. jev_scope (task -> question, phases -> options)
```json
{ "action": "jev_scope", "question": "<task>...", "options": { "p1": "...", "p2": "..." }, "context": "..." }
```

### 7. jev_dispatch (task -> question, subtasks -> options; REQUIRED before Task/subagents fan-out)
```json
{ "action": "jev_dispatch", "question": "<task>...", "options": { "a": "...", "b": "..." }, "context": "max_parallel=3; 依赖说明", "target": "slots=3" }
```

### 8. jev_consistency (plan -> question, constraints -> checks)
```json
{ "action": "jev_consistency", "question": "<plan>...", "checks": { "no_mock": "..." }, "context": "..." }
```

### 9. jev_done (pack the triple into question (bridge drops every other jev_done field))
```json
{ "action": "jev_done", "question": "原始需求:<request>\n实际工作:<work>\n证据:<evidence>" }
```

### 10. jev_diagnose (error_summary -> question, hypotheses -> options)
```json
{ "action": "jev_diagnose", "question": "<error>...", "options": { "h1": "...", "h2": "..." }, "context": "..." }
```

### 11. jev_edge_cases (code -> content)
```json
{ "action": "jev_edge_cases", "content": "<code under review>...", "context": "..." }
```

### 12. jev_blast_radius (diff -> content, target_component -> target)
```json
{ "action": "jev_blast_radius", "content": "<diff>...", "target": "<component>...", "context": "..." }
```

### 13. jev_intent (user input -> question, interpretations -> options (optional))
```json
{ "action": "jev_intent", "question": "<用户原话>...", "options": { "a": "...", "b": "..." }, "context": "..." }
```

### 14. jev_next (completed work -> question, candidates -> options (optional))
```json
{ "action": "jev_next", "question": "<刚完成的工作>...", "options": { "a": "...", "b": "..." }, "context": "<剩余 backlog>..." }
```

### 15. jev_compress (gated tool-history compression plan (checklist -> save_note -> clear_tool_history))
```json
{ "action": "jev_compress", "mode": "subscription | token | auto", "threshold": 85 }
```

### 16. jev_progress (task -> question, phases -> options, completed -> content)
```json
{ "action": "jev_progress", "question": "<任务>...", "options": { "p1": "..." }, "content": "<已完成>...", "context": "..." }
```

### 17. jev_memory (candidate -> content, context -> context; verdict: skip | workspace | global)
```json
{ "action": "jev_memory", "content": "<候选记忆>...", "context": "<来源任务>..." }
```

WARNING: never pass `diff`/`patch` field names to `jev_blast_radius`
through the bridge — they get misrouted to the `apply_patch`
validator (`invalid patch boundaries`). Always use `content`+`target`.

## Rules

1. JEV never edits files; it only returns a verdict. Apply the verdict with sa-vm actions.
2. If `sa_vm_run` rejects `jev_*` with unknown-action, report it as missing backend registration; do not retry with another shape.
3. Keep `context` short (goal + constraints + evidence). Do not paste whole files.
4. CODE REVIEW MUST SEND git diff, NEVER A SUMMARY. When asking JEV to review
   code (`jev_redteam`, `jev_blast_radius`, `jev_edge_cases`,
   `jev_consistency`), paste the actual `git_diff` output (or the exact
   code under review) into `content`/`context`. A condensed paraphrase
   hides the details the verdict depends on — verified 2026-09-21, the same
   fix got different risk confidences from a summary vs the full diff.
   Summaries are for humans; JEV gets the raw diff.
