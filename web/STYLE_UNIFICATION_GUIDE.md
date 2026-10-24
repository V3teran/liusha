# 页面风格统一规范

## 🎯 统一基调：专业数据工具风格

### 核心原则
- 简洁、高密度、工具感
- 所有页面使用相同的布局结构
- 统一的间距、圆角、字号系统

---

## 📐 页面布局结构

### 1. 页面头部（所有页面必须统一）

```tsx
<header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
  <div>
    <h1 className="text-base font-semibold text-text">{标题}</h1>
    <p className="mt-0.5 text-sm text-text-secondary">{副标题}</p>
  </div>
  <div className="flex items-center gap-3">
    {右侧操作按钮}
  </div>
</header>
```

**关键点：**
- ✅ 容器：`px-6 py-4`（24px 16px）
- ✅ 标题：`text-base font-semibold`（16px）
- ✅ 副标题：`text-sm text-text-secondary`（14px）
- ❌ 禁止使用：`tac-prompt`、`terminal-prompt`、`text-[15px]`、`text-[12.5px]`

### 2. 筛选栏（有筛选功能的页面）

```tsx
<div className="flex flex-wrap items-center gap-3 border-b border-border bg-surface/50 px-6 py-3">
  {筛选控件}
</div>
```

**关键点：**
- ✅ 容器：`px-6 py-3`（24px 12px）
- ✅ 控件间距：`gap-3`（12px）
- ✅ 背景：`bg-surface/50`（半透明）

### 3. 内容区域

```tsx
<div className="flex-1 overflow-y-auto px-6 py-4">
  {主内容}
</div>
```

**关键点：**
- ✅ 容器：`px-6 py-4`（24px 16px）
- ❌ 禁止使用：`px-5.5`、`py-3.5`、其他奇数值

### 4. 分页控件（有分页的页面）

```tsx
<div className="flex items-center justify-between border-t border-border px-6 py-3">
  <span className="text-sm text-text-secondary">共 X 条</span>
  <div className="flex items-center gap-2">
    {分页按钮}
  </div>
</div>
```

---

## 🎨 统一样式规范

### 圆角系统
- ✅ 小组件（按钮、输入框）：`rounded-lg`（12px）
- ✅ 卡片：`rounded-lg`（12px）
- ✅ 弹窗：`rounded-xl`（16px）
- ❌ 禁止使用：`rounded-md`（8px）混用

### 间距系统
- ✅ 页面级：`px-6`（24px）
- ✅ 组件级：`gap-3`（12px）、`gap-2`（8px）
- ❌ 禁止使用：`px-5.5`、`gap-4.5`、`gap-1.5`

### 字号系统
- ✅ 页面标题：`text-base`（16px）
- ✅ 正文：`text-sm`（14px）
- ✅ 辅助文字：`text-xs`（12px）
- ❌ 禁止使用：`text-[15px]`、`text-[13px]`、`text-[12.5px]`

### 按钮高度
- ✅ 标准按钮：`h-9`（36px）
- ✅ 小按钮：`h-8`（32px）
- ✅ 迷你按钮：`h-7`（28px）

---

## 📝 需要统一的页面清单

### 已统一 ✅
- [x] FindingsPage
- [x] ExplorationGraphPage
- [x] ConversationsPage（已有统一风格）
- [x] LlmAuditPage

### 待统一 ⏳
- [ ] ToolsPage（头部、筛选栏、字号）
- [ ] SystemConfig（头部、字号）
- [ ] ModelPage（tab 样式、字号）
- [ ] CredentialsPage（头部、筛选栏、分页）
- [ ] TrafficPage（头部、字号）
- [ ] AgentAdmin（头部、字号）
- [ ] SkillsPage

---

## 🔧 具体修改映射

### 头部样式映射

| 旧样式 | 新样式 |
|--------|--------|
| `tac-prompt font-mono text-[15px]` | `text-base font-semibold` |
| `terminal-prompt` | `text-base font-semibold` |
| `text-[12.5px] text-muted` | `text-sm text-text-secondary` |
| `px-5.5 py-3` | `px-6 py-3` |
| `gap-4.5` | `gap-3` |

### 输入框样式映射

| 旧样式 | 新样式 |
|--------|--------|
| `h-8 rounded-md` | `h-9 rounded-lg` |
| `text-[12.5px]` | `text-sm` |
| `text-muted` | `text-text-tertiary` |

### 分页按钮样式映射

| 旧样式 | 新样式 |
|--------|--------|
| `h-7 w-7 rounded-md` | `h-7 w-7 rounded-lg` |
| `text-muted` | `text-text-secondary` |

---

## ⚠️ 注意事项

1. **只改样式类名，不改业务逻辑**
2. **保留所有功能代码不变**
3. **保持原有的 className 顺序**
4. **测试每个页面确保无回归**

---

## 📊 统一前后对比

### 示例：ToolsPage 头部

**统一前：**
```tsx
<h1 className="tac-prompt font-mono text-[15px] font-semibold text-text">工具</h1>
<p className="mt-0.5 text-[12.5px] text-muted">{stats.total} 个工具</p>
```

**统一后：**
```tsx
<h1 className="text-base font-semibold text-text">工具</h1>
<p className="mt-0.5 text-sm text-text-secondary">{stats.total} 个工具</p>
```
