# 🔍 Profile 目录共享需求分析

## 问题：profile/ 里的数据是什么？是否需要共享？

---

## 📁 Profile 目录内容

### 1. 浏览器数据
```
profile/
  └── chrome/
      ├── cookies.db           ← 登录态 (Session/Cookies)
      ├── Local Storage/       ← localStorage 数据
      ├── Session Storage/     ← sessionStorage 数据
      ├── IndexedDB/           ← IndexedDB 数据
      └── Cache/               ← 浏览器缓存
```

### 2. 工具配置
```
profile/
  └── config/
      ├── .sqlmaprc           ← sqlmap 配置
      ├── .nuclei/            ← nuclei 配置
      └── other tool configs
```

---

## 🎯 核心问题：是否需要共享？

### 场景 A：Planner 登录 → Exploitation 使用

#### 业务流程
```
1. Planner Agent 打开网站，执行登录操作
   - 浏览器保存 cookies 到 profile/chrome/cookies.db
   - 获取 session token

2. Exploitation Agent 需要利用已登录状态
   - 打开同一网站
   - 期望：直接是登录状态（不需要重新登录）
```

#### 当前设计（每个 Agent 独立 profile）
```
/liusha/
  ├── planner-agent-uuid/
  │   └── profile/
  │       └── chrome/
  │           └── cookies.db    ← Planner 的 cookies
  └── exploitation-agent-uuid/
      └── profile/
          └── chrome/
              └── cookies.db    ← Exploitation 的 cookies（空！）
```

**结果**：❌ Exploitation Agent 看不到 Planner 的登录态！

---

## 🔬 实际业务需求分析

### 需求 1：同一 Task 内的 Agent 共享登录态

#### 为什么需要？
1. **避免重复登录**
   - Planner 已经登录
   - Exploitation 不应该再登录一次

2. **保持 Session 一致性**
   - 某些网站检测多次登录（踢掉旧 session）
   - 需要使用同一个 session

3. **验证码问题**
   - 登录可能需要验证码
   - 无法自动化重复登录

#### 共享粒度
- ✅ 同一 Task 的不同 Agent 应该共享
- ❌ 不同 Task 不应该共享（可能是不同目标）

---

### 需求 2：同一 Assignment 内的 Task 共享登录态？

#### Assignment 的含义
从代码来看：
```go
// 同一 Assignment 共享 Sandbox 容器
sandboxClient, err := h.sandboxMgr.Acquire(ctx, assignmentID)
```

#### 可能的场景
```
Assignment-XYZ
  ├── Task-1: 扫描 example.com
  │   ├── Planner (登录 admin/pass)
  │   └── Exploitation (使用 admin 的 session)
  └── Task-2: 深度测试 example.com
      └── Exploitation (应该使用 Task-1 的登录态吗？)
```

**问题**：
1. Task-2 是新任务，可能需要重新登录
2. 或者，Task-2 可以复用 Task-1 的 session（如果还有效）

---

## 🎯 方案对比

### 方案 A：按 Task 共享（推荐）

#### 目录结构
```
/liusha/
  └── <task_id>/
      └── shared-profile/      ← 所有 Agent 共享
          └── chrome/
              └── cookies.db
      ├── <agent_id_1>/
      │   ├── workspace/
      │   └── output/
      └── <agent_id_2>/
          ├── workspace/
          └── output/
```

#### 实现
```go
agentRoot := filepath.Join(liushaRoot, req.TaskID, req.AgentID)
workspaceDir := filepath.Join(agentRoot, "workspace")
outputDir := filepath.Join(agentRoot, "output")
profileDir := filepath.Join(liushaRoot, req.TaskID, "shared-profile")  // 共享！
```

#### 优点
1. ✅ 同一 Task 的 Agent 共享登录态
2. ✅ 不同 Task 隔离（避免混淆）
3. ✅ 清理简单（删除 task_id 目录）

#### 缺点
1. ⚠️ 需要传递 task_id（但这是必需的）

---

### 方案 B：按 Assignment 共享

#### 目录结构
```
/liusha/
  └── <assignment_id>/
      └── shared-profile/      ← 所有 Task 的所有 Agent 共享
      ├── <task_id_1>/
      │   ├── <agent_id_1>/
      │   └── <agent_id_2>/
      └── <task_id_2>/
          └── <agent_id_3>/
```

#### 优点
1. ✅ 跨 Task 复用登录态（如果需要）
2. ✅ Assignment 级别清理

#### 缺点
1. ❌ 不同 Task 可能是不同目标（混淆）
2. ❌ 复杂度更高

---

### 方案 C：完全隔离（不共享）

#### 目录结构
```
/liusha/
  └── <agent_id>/
      ├── workspace/
      ├── output/
      └── profile/             ← 每个 Agent 独立
```

#### 优点
1. ✅ 最简单
2. ✅ 完全隔离

#### 缺点
1. ❌ 无法共享登录态（每个 Agent 都要重新登录）
2. ❌ 不符合业务需求

---

## 🔍 检查当前代码的实际需求

### browser-svc.py 的注释
```python
"""每身份常驻 Page 路由服务：持有 1 个 BrowserSession，按 HUNTER_ID 把命令路由到各自 tab。

直接寻址：同身份多 agent（每 HUNTER_ID 一个 tab）并发读写不串台
```

**关键信息**：
- "同身份多 agent" → 同一个浏览器 session
- "每 HUNTER_ID 一个 tab" → 不同 tab，但共享 cookies/session

**结论**：确实需要共享 profile！

---

## ✅ 最终建议：方案 A（按 Task 共享）

### 目录结构
```
/liusha/
  └── <task_id>/
      ├── profile/              ← Task 级共享（登录态）
      │   └── chrome/
      │       └── cookies.db
      ├── <agent_id_1>/
      │   ├── workspace/
      │   └── output/
      └── <agent_id_2>/
          ├── workspace/
          └── output/
```

### ExecRequest
```go
type ExecRequest struct {
    TaskID         string `json:"task_id"`     // 必需：用于 profile 共享
    AgentID        string `json:"agent_id"`    // 必需：用于 workspace/output 隔离
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

### 环境变量
```go
cmd.Env = append(os.Environ(),
    "LIUSHA_TASK_ID="+req.TaskID,
    "LIUSHA_AGENT_ID="+req.AgentID,
    "LIUSHA_WORKSPACE="+workspaceDir,      // Agent 独立
    "LIUSHA_OUTPUT="+outputDir,             // Agent 独立
    "LIUSHA_PROFILE="+profileDir,           // Task 共享
    "HOME="+profileDir,                     // Task 共享
)
```

---

## 📊 对比总结

| 维度 | 完全隔离 | Task 共享 | Assignment 共享 |
|------|---------|----------|----------------|
| 登录态共享 | ❌ | ✅ | ✅ |
| 简洁性 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ |
| 业务需求 | ❌ | ✅ | ⚠️ 过度 |
| 清理容易 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ |

---

## 🎯 结论

**需要 task_id！**

原因：
1. ✅ 同一 Task 的多个 Agent 需要共享登录态
2. ✅ profile/ 包含浏览器 cookies/session（必须共享）
3. ✅ 不同 Task 应该隔离（避免混淆）

最终结构：
```
/liusha/<task_id>/
  ├── profile/              ← 共享（登录态）
  └── <agent_id>/
      ├── workspace/        ← 隔离
      └── output/           ← 隔离
```

