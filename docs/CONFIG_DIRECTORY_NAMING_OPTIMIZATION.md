# internal/config 目录命名优化建议

## 🎯 当前命名分析

### **现有目录结构**
```
internal/config/
├── agent/          # Agent 配置
├── llmcfg/         # LLM 配置
├── tool/           # 工具配置
├── settingstore/   # 系统设置
├── configstore/    # 通用三级缓存层
└── seed/           # 统一种子加载
```

---

## 🔍 命名问题分析

### **问题 1：命名风格不一致**

| 目录 | 风格 | 问题 |
|------|------|------|
| `agent/` | ✅ 单一名词 | 清晰 |
| `llmcfg/` | ⚠️ 缩写 + cfg 后缀 | 不一致 |
| `tool/` | ✅ 单一名词 | 清晰 |
| `settingstore/` | ⚠️ 名词 + store 后缀 | 不一致 |
| `configstore/` | ⚠️ 名词 + store 后缀 | 不一致 |
| `seed/` | ✅ 单一名词 | 清晰 |

**核心问题：**
- `agent/`, `tool/`, `seed/` - 单一名词（清晰）
- `llmcfg/` - 缩写（不直观）
- `settingstore/`, `configstore/` - 复合名词（冗余）

---

### **问题 2：职责边界不清**

| 目录 | 当前名称 | 职责 | 混淆点 |
|------|---------|------|--------|
| `settingstore/` | 系统设置存储 | 存储系统配置 | ⚠️ 与 config 重复？ |
| `configstore/` | 配置存储 | 通用缓存层 | ⚠️ 与 config 重复？ |

---

### **问题 3：层次感不强**

```
internal/config/
├── agent/          # 领域配置（业务层）
├── llmcfg/         # 领域配置（业务层）
├── tool/           # 领域配置（业务层）
├── settingstore/   # 领域配置（业务层）
├── configstore/    # 基础设施（缓存层）← 职责不同
└── seed/           # 基础设施（初始化层）← 职责不同
```

**混在一起：**
- 业务配置（agent, llm, tool）
- 基础设施（configstore, seed）

---

## 💡 命名原则（业界最佳实践）

### **1. Go 项目命名规范**

```
✅ 好的命名：
- 简短、单一名词
- 小写、无缩写（除非非常通用）
- 体现职责

❌ 避免：
- 复合名词（xxxstore, xxxconfig）
- 缩写（cfg, mgr, svc）
- 后缀冗余（store, config）
```

**参考：Kubernetes**
```
internal/
├── apis/           # 不是 apiconfig/
├── controller/     # 不是 controllermgr/
├── scheduler/      # 不是 schedulersvc/
└── storage/        # 不是 datastore/
```

---

### **2. 按职责分层**

```
按层次组织：
infrastructure/     # 基础设施
domain/            # 领域模型
application/       # 应用逻辑
```

---

## 🎯 方案 A：最小改动（推荐）

### **优化重点：统一风格、消除缩写**

```
internal/config/
├── agent/          # ✅ 保持
├── llm/            # ✅ llmcfg → llm（去掉 cfg 后缀）
├── tool/           # ✅ 保持
├── setting/        # ✅ settingstore → setting（去掉 store 后缀）
├── cache/          # ✅ configstore → cache（更准确）
└── seed/           # ✅ 保持
```

**改动：**
1. `llmcfg/` → `llm/`
2. `settingstore/` → `setting/`
3. `configstore/` → `cache/`

**理由：**
- ✅ 统一风格：都是单一名词
- ✅ 去除冗余后缀（cfg, store）
- ✅ 更准确：configstore 实际上是缓存层，叫 cache 更清晰
- ✅ 改动最小：只改目录名，不改架构

---

## 🎯 方案 B：按职责分层（更清晰）

### **分离业务配置和基础设施**

```
internal/config/
├── domain/                 # 领域配置（业务层）
│   ├── agent/
│   ├── llm/
│   ├── tool/
│   └── setting/
│
├── infrastructure/         # 基础设施（技术层）
│   ├── cache/             # 三级缓存（原 configstore）
│   └── seed/              # 种子加载
│
└── config.go              # 顶层配置（Viper）
```

**改动：**
1. 创建 `domain/` 子目录
2. 创建 `infrastructure/` 子目录
3. 移动现有目录

**理由：**
- ✅ 职责清晰：业务配置 vs 基础设施
- ✅ 易扩展：新增领域配置放 domain/
- ⚠️ 改动较大：需要更新所有 import 路径

---

## 🎯 方案 C：扁平化 + 统一命名（折中）

### **保持扁平，但统一风格**

```
internal/config/
├── agent/          # Agent 配置
├── llm/            # LLM 配置（原 llmcfg）
├── tool/           # 工具配置
├── setting/        # 系统设置（原 settingstore）
├── cache/          # 三级缓存层（原 configstore）
├── seed/           # 种子加载
└── config.go       # 顶层配置
```

**改动同方案 A，但不分层**

---

## 📊 方案对比

| 方案 | 优点 | 缺点 | 改动量 | 推荐度 |
|------|------|------|--------|--------|
| **A. 最小改动** | ✅ 改动最小<br>✅ 统一风格 | ⚠️ 扁平化，不分层 | 🟢 小 | ⭐⭐⭐⭐⭐ |
| **B. 按职责分层** | ✅ 职责最清晰<br>✅ 易扩展 | ⚠️ 改动较大<br>⚠️ 多一层目录 | 🔴 大 | ⭐⭐⭐ |
| **C. 扁平化统一** | ✅ 统一风格<br>✅ 扁平简单 | ⚠️ 同 A | 🟢 小 | ⭐⭐⭐⭐ |

---

## 🎯 详细分析

### **1. llmcfg → llm**

**当前：**
```
internal/config/llmcfg/
├── model.go
├── store.go
└── apikey.go
```

**优化为：**
```
internal/config/llm/
├── model.go
├── store.go
└── apikey.go
```

**理由：**
- ✅ `llm` 本身已经很清楚是配置
- ✅ 在 `config/` 下，不需要 `cfg` 后缀
- ✅ 更简洁：`config.llm` vs `config.llmcfg`

**对比业界：**
```
Kubernetes:
  internal/apis/       # 不是 apisconfig/
  
Docker:
  config/daemon/       # 不是 daemoncfg/
```

---

### **2. settingstore → setting**

**当前：**
```
internal/config/settingstore/
├── model.go
├── store.go
└── store_test.go
```

**优化为：**
```
internal/config/setting/
├── model.go
├── store.go
└── store_test.go
```

**理由：**
- ✅ `setting` 已经表达是设置
- ✅ 包里的 `store.go` 已经表示存储逻辑
- ✅ 避免冗余：`settingstore.Store` → `setting.Store`

**命名对比：**
```
❌ 冗余：
internal/config/settingstore/store.go
→ settingstore.Store  ← store 出现两次

✅ 简洁：
internal/config/setting/store.go
→ setting.Store  ← 清晰
```

---

### **3. configstore → cache**

**当前：**
```
internal/config/configstore/
├── store.go
└── adapter.go
```

**问题：**
- ⚠️ `configstore` 是什么？配置的存储？
- ⚠️ 实际上是：三级缓存层（内存 → Redis → PostgreSQL）
- ⚠️ 职责：缓存，不是存储

**优化为：**
```
internal/config/cache/
├── store.go        # 缓存存储
└── adapter.go      # 缓存适配器
```

**或者更准确：**
```
internal/config/cache/
├── cache.go        # 缓存主逻辑
├── layer.go        # L1/L2/L3 层
└── adapter.go      # 适配器
```

**理由：**
- ✅ `cache` 更准确描述职责
- ✅ 避免混淆：config 下的 configstore 太绕
- ✅ 对标业界：Redis = cache, not store

**业界参考：**
```
Go-Redis:
  cache/          # 不是 cachestore/

Kubernetes:
  storage/cache/  # cache 表示缓存层
```

---

## ✅ 最终推荐：方案 A

### **重命名清单**

| 当前 | 优化后 | 理由 |
|------|--------|------|
| `llmcfg/` | `llm/` | 去除冗余 cfg 后缀 |
| `settingstore/` | `setting/` | 去除冗余 store 后缀 |
| `configstore/` | `cache/` | 更准确描述职责 |
| `agent/` | `agent/` | ✅ 保持 |
| `tool/` | `tool/` | ✅ 保持 |
| `seed/` | `seed/` | ✅ 保持 |

---

### **优化后的结构**

```
internal/config/
├── agent/          # Agent 配置
│   ├── model.go
│   └── store.go
│
├── llm/            # LLM 配置（原 llmcfg）
│   ├── model.go
│   ├── store.go
│   └── apikey.go
│
├── tool/           # 工具配置
│   ├── model.go
│   ├── store.go
│   └── reconcile.go
│
├── setting/        # 系统设置（原 settingstore）
│   ├── model.go
│   ├── store.go
│   └── store_test.go
│
├── cache/          # 三级缓存层（原 configstore）
│   ├── store.go
│   └── adapter.go
│
├── seed/           # 种子加载
│   ├── seed.go
│   ├── agents/
│   │   ├── planner.md
│   │   ├── executor.md
│   │   └── evaluator.md
│   ├── system.go
│   └── llm.go
│
├── config.go       # 顶层配置（Viper）
└── config_test.go
```

---

### **使用示例对比**

#### **重命名前：**
```go
import (
    "github.com/V3teran/liusha/internal/config/llmcfg"
    "github.com/V3teran/liusha/internal/config/settingstore"
    "github.com/V3teran/liusha/internal/config/configstore"
)

llmConfig := llmcfg.Config{}           // ← cfg 冗余
settings := settingstore.Store{}       // ← store 冗余
cache := configstore.Store{}           // ← config 冗余
```

#### **重命名后：**
```go
import (
    "github.com/V3teran/liusha/internal/config/llm"
    "github.com/V3teran/liusha/internal/config/setting"
    "github.com/V3teran/liusha/internal/config/cache"
)

llmConfig := llm.Config{}              // ✅ 清晰
settings := setting.Store{}            // ✅ 清晰
cache := cache.Store{}                 // ✅ 清晰
```

---

## 🚀 执行计划

### **步骤 1：重命名目录（3分钟）**

```bash
cd internal/config

# 1. llmcfg → llm
git mv llmcfg llm

# 2. settingstore → setting
git mv settingstore setting

# 3. configstore → cache
git mv configstore cache

git status
```

---

### **步骤 2：更新 import 路径（10分钟）**

```bash
# 自动替换所有 import
find . -name "*.go" -type f -exec sed -i '' \
  -e 's|internal/config/llmcfg|internal/config/llm|g' \
  -e 's|internal/config/settingstore|internal/config/setting|g' \
  -e 's|internal/config/configstore|internal/config/cache|g' \
  {} +

# 验证
go build ./...
```

---

### **步骤 3：更新文档注释（5分钟）**

```go
// 更新包注释
// internal/config/cache/store.go
// Package cache 实现通用三级缓存层...（原 configstore）
```

---

### **步骤 4：提交（1分钟）**

```bash
git add .
git commit -m "refactor: 优化 config 目录命名

变更：
- llmcfg → llm（去除冗余 cfg 后缀）
- settingstore → setting（去除冗余 store 后缀）
- configstore → cache（更准确描述职责）

理由：
1. 统一命名风格（单一名词）
2. 去除冗余后缀
3. 提升可读性
"
```

---

## 📊 改动影响评估

| 改动 | 影响范围 | 风险 | 测试 |
|------|---------|------|------|
| 目录重命名 | 🟢 低 | 🟢 低 | go build |
| import 路径 | 🟡 中 | 🟢 低 | go test |
| 包注释 | 🟢 低 | 🟢 低 | - |

**总体评估：🟢 低风险、低改动**

---

## ✅ 总结

### **推荐：方案 A（最小改动）**

**重命名：**
1. ✅ `llmcfg/` → `llm/`
2. ✅ `settingstore/` → `setting/`
3. ✅ `configstore/` → `cache/`

**优点：**
- ✅ 统一命名风格
- ✅ 去除冗余后缀
- ✅ 更准确描述职责
- ✅ 改动最小（20分钟）

**要执行吗？** 🚀
