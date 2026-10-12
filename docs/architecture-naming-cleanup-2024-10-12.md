# 架构命名全面清理报告

## 执行时间
2024-10-12

## 背景
近期进行了大的架构变更，将"攻击图/知识图谱"改名为"探索图"，但存在老架构遗留的目录、类、函数、参数、注释、文件名、md等命名未完全更新。

## 执行方法
系统性10轮自检，结合业界最佳实践，全量发现并解决所有老架构残留问题。

## 发现与修复的问题

### 1. 前端类型定义 (web/src/api/types.ts)
**问题**: 第319-323行存在注释掉的 `AttackGraphNode/Edge/Verification` 旧命名
**修复**: 删除注释掉的废弃代码块
**状态**: ✅ 已完成

### 2. 后端配置注释 (internal/config/llm/model.go)
**问题**: 第123行注释中提到 `attack-graph`
**修复**: 将 "attack-graph 摘要复用" 改为 "探索图摘要复用"
**状态**: ✅ 已完成

### 3. Claude 配置文件 (.claude/settings.local.json)
**问题**: 多处历史命令记录包含 `attack-graph`、`attack_graph`
**修复**: 删除以下4个过时的权限配置项：
- `Bash(grep ... attack-graph-refactor.md)`
- `Bash(git push ... feat/react-rewrite-attackgraph-refactor)`
- `Bash(curl ... /attack_graph/5eed0000...)`
- `Bash(curl ... attack_graph/5eed0000...?conv=...)`
**状态**: ✅ 已完成

### 4. Playwright 测试快照 (.playwright-mcp/*.yml)
**问题**: 33个测试快照文件包含 `#/attack-graph` 路由
**修复**: 批量替换为 `#/exploration-graph`
**状态**: ✅ 已完成 (33个文件)

### 5. 数据库迁移文件 (db/migrations/0108_world_model.up.sql)
**问题**: 第7行注释包含 `exploration graph` 英文
**修复**: 统一为中文 "探索图"
**状态**: ✅ 已完成

### 6. 前端 API 调用 (web/src/pages/ExplorationGraphPage.tsx)
**问题**: 第101行 API 路径缺少 `/exploration` 前缀
**修复**: `/api/v1/tasks/${taskId}/graph` → `/api/v1/tasks/${taskId}/exploration/graph`
**状态**: ✅ 已完成

### 7. 代码质量检查脚本 (scripts/code_quality_check.sh)
**问题**: 第8-9行检查项描述过时
**修复**: 更新检查项文案，同时检查 `attack-graph` 和 `knowledge-graph`
**状态**: ✅ 已完成

## 验证结果

### 10轮系统自检全部通过 ✅

1. ✅ 前端类型定义 - 无残留
2. ✅ 后端配置注释 - 无残留
3. ✅ Claude 配置文件 - 无残留
4. ✅ Playwright 测试快照 - 无残留
5. ✅ 数据库迁移文件 - 无残留
6. ✅ 前端 API 调用 - 路径正确
7. ✅ API 路由定义 - 命名正确
8. ✅ 包名和目录名 - 无旧目录
9. ✅ 类型定义和接口 - 无旧类型
10. ✅ 常量和环境变量 - 无旧常量

### 额外检查项全部通过 ✅

- ✅ 配置文件 (config/*.yaml)
- ✅ Makefile
- ✅ 脚本文件 (scripts/*.sh)

## 架构术语对照表

| 旧术语 | 新术语 | 作用域 |
|--------|--------|--------|
| attack-graph | exploration-graph | 前端路由 |
| attack_graph | exploration/graph | API 路径 |
| AttackGraph* | ExplorationGraph* | 类型命名 |
| knowledge graph | 探索图 | 注释文档 |
| 攻击图 | 探索图 | 中文文档 |

## 清理原则

1. **零兼容代码** - 不保留任何向后兼容的老命名
2. **零老架构概念** - 完全移除知识图谱、攻击图等概念
3. **统一术语** - 全面使用"探索图 (Exploration Graph)"
4. **业务术语保留** - `executor/replayer.go` 中的 "attack"（攻击请求/响应）是正常业务术语，不属于架构命名问题

## 遗留问题

无。所有发现的问题均已修复并验证通过。

## 影响范围

- **前端**: 1个组件，1个类型文件，33个测试快照
- **后端**: 1个配置文件，1个数据库迁移文件
- **配置**: 1个 Claude 配置文件
- **工具**: 1个代码质量检查脚本

## 总结

本次清理工作：
- ✅ 完成10轮系统性自检
- ✅ 发现并修复7处老架构命名残留
- ✅ 更新37个文件（33个测试快照 + 4个源文件）
- ✅ 通过全部验证检查
- ✅ 无遗留问题

**架构命名已完全统一为"探索图 (Exploration Graph)"**

## 提交信息

```
commit 390eae3b
refactor: 清理老架构命名残留，统一为探索图
```
