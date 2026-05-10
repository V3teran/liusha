---
name: jq
category: utility
description: JSON 解析/提取/重构。LLM 完全熟悉操作符——本手册只列项目策略 + 写 finding 红线。配合 curl pipe 用，避免自己 grep JSON 跑偏。
---

# jq 项目特定约束

## 项目策略

- 响应是 HTML/XML/纯文本时 **不要用 jq**——会报 `parse error`，换 `grep`/`sed`/`python3`。
- 大响应防 stdout 8KB tail 被无关字段占满：用 `jq -c '.field'` 流式提单字段，或 `jq '...' | head -c 4096` 限输出长度。
- 探未知 schema 时先 `jq 'keys'` / `jq 'paths' | head -50`，别一次 `jq '.'` 印整段。

## 写 finding 红线

**不要把 jq 输出作为唯一证据**——主证据放原始 curl 响应（含 `HTTP/1.1 <code>` + Content-Type + 完整 body 截段），jq 仅作"快速定位字段"的辅助命令。`evidence` 里同时给 jq 表达式 + 提取结果，方便复核。

## 决策边界（什么时候**不要**用 jq）

- 复杂状态机（拿 token → 用 token → 解析） → `python3` 整段更清晰
- 需要从 jq 输出做条件分支（if/else） → shell 嵌 jq 易出错，换 `python3` + `json.loads`
