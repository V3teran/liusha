# 🏗️ Sandbox 目录结构新架构设计

## 设计原则

1. **统一前缀**：所有目录使用同一根目录
2. **清晰层次**：`/liusha/<assignment_id>/<agent_id>/<功能目录>/`
3. **语义明确**：目录名直观表达用途
4. **完全隔离**：每个 agent 独立空间
5. **无遗留**：彻底删除 hunter 概念

---

## 新架构方案

### 目录结构

```
/liusha/                                    ← 统一根目录
└── <assignment_id>/                        ← Sandbox 容器生命周期
    └── <agent_id>/                         ← Agent 实例 (UUID)
        ├── workspace/                      ← 工作目录 (cwd)
        │   ├── temp/                       ← 临时文件
        │   └── scripts/                    ← 脚本
        ├── output/                         ← 命令产出
        │   ├── screenshots/                ← 截图
        │   ├── traffic/                    ← 流量抓包
        │   └── reports/                    ← 扫描报告
        └── profile/                        ← 用户配置和状态
            ├── chrome/                     ← 浏览器数据
            │   ├── cookies.db
            │   └── sessions/
            └── config/                     ← 工具配置
                ├── .sqlmaprc
                └── .nuclei/
```

### 完整路径示例

```
/liusha/8ebde604-f49f-48aa-8970-6baa423fb1d4/480a665e-e47c-4712-a1a8-2eeda74a2e35/workspace/
/liusha/8ebde604-f49f-48aa-8970-6baa423fb1d4/480a665e-e47c-4712-a1a8-2eeda74a2e35/output/
/liusha/8ebde604-f49f-48aa-8970-6baa423fb1d4/480a665e-e47c-4712-a1a8-2eeda74a2e35/profile/
```

---

## 代码实现

### 1. 常量定义

```go
// internal/sandbox/server/exec.go

const (
    liushaRoot = "/liusha"
)

func buildAgentDir(assignmentID, agentID string) string {
    return filepath.Join(liushaRoot, assignmentID, agentID)
}
```

### 2. 目录初始化

```go
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
    var req sandbox.ExecRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, http.StatusBadRequest, "decode request: %v", err)
        return
    }
    
    // 验证
    if req.AssignmentID == "" {
        writeError(w, http.StatusBadRequest, "assignment_id required")
        return
    }
    if req.AgentID == "" {
        writeError(w, http.StatusBadRequest, "agent_id required")
        return
    }
    if !isPathSafe(req.AssignmentID) || !isPathSafe(req.AgentID) {
        writeError(w, http.StatusBadRequest, "invalid id format")
        return
    }
    
    // 构建目录
    agentRoot := buildAgentDir(req.AssignmentID, req.AgentID)
    workspaceDir := filepath.Join(agentRoot, "workspace")
    outputDir := filepath.Join(agentRoot, "output")
    profileDir := filepath.Join(agentRoot, "profile")
    
    // 创建所有必要目录
    for _, dir := range []string{workspaceDir, outputDir, profileDir} {
        if err := os.MkdirAll(dir, 0o777); err != nil {
            writeError(w, http.StatusInternalServerError, "mkdir %s: %v", dir, err)
            return
        }
    }
    
    // 设置环境变量和工作目录
    cmd := exec.CommandContext(cmdCtx, "sh", "-c", req.Command)
    cmd.Dir = workspaceDir
    cmd.Env = append(os.Environ(),
        "LIUSHA_ROOT="+liushaRoot,
        "LIUSHA_ASSIGNMENT_ID="+req.AssignmentID,
        "LIUSHA_AGENT_ID="+req.AgentID,
        "LIUSHA_WORKSPACE="+workspaceDir,
        "LIUSHA_OUTPUT="+outputDir,
        "LIUSHA_PROFILE="+profileDir,
        "HOME="+profileDir,                    // 浏览器使用
        "OUTPUT_DIR="+outputDir,               // 兼容旧脚本（可选）
    )
    
    // ... 执行命令
}
```

### 3. ExecRequest 结构更新

```go
// internal/sandbox/types.go

type ExecRequest struct {
    AssignmentID   string `json:"assignment_id"`
    AgentID        string `json:"agent_id"`
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

### 4. 工具注册层更新

```go
// internal/tools/sandbox.go

func (t *runCommandTool) Execute(ctx context.Context, argsJSON []byte) (registry.ToolResult, error) {
    // ...
    
    result, err := t.deps.Sandbox.Exec(ctx, sandbox.ExecRequest{
        AssignmentID:   t.deps.AssignmentID,   // 新增
        AgentID:        t.deps.AgentID,         // 重命名
        Command:        args.Command,
        TimeoutSeconds: timeout,
        Tag:            args.Tag,
    })
    
    // ...
}
```

### 5. Deps 结构更新

```go
// internal/tools/registry.go (或对应文件)

type Deps struct {
    TaskID         string
    AssignmentID   string    // 新增
    AgentID        string    // 重命名（原 ExecutorID）
    Host           string
    Sandbox        sandbox.Client
    // ... 其他依赖
}
```

### 6. Handler 层更新

```go
// cmd/runner/handler_run.go

func (h handler) handleCognition(ctx context.Context, p worker.Payload, brief string) error {
    // ...
    
    var assignmentID string
    if tk, err := h.tasks.GetByID(ctx, taskID); err == nil {
        assignmentID = tk.AssignmentID
    }
    
    // ...
    
    tools.RegisterAll(reg, tools.Deps{
        TaskID:       taskID,
        AssignmentID: assignmentID,          // 新增
        AgentID:      p.AgentID,             // 重命名
        Host:         virtualHost,
        Sandbox:      sandboxClient,
        // ...
    })
    
    // ...
}
```

---

## 环境变量说明

| 变量名 | 值 | 用途 |
|--------|-----|------|
| `LIUSHA_ROOT` | `/liusha` | 根目录 |
| `LIUSHA_ASSIGNMENT_ID` | UUID | Assignment ID |
| `LIUSHA_AGENT_ID` | UUID | Agent ID |
| `LIUSHA_WORKSPACE` | `/liusha/<assignment>/<agent>/workspace` | 工作目录 |
| `LIUSHA_OUTPUT` | `/liusha/<assignment>/<agent>/output` | 产出目录 |
| `LIUSHA_PROFILE` | `/liusha/<assignment>/<agent>/profile` | 配置目录 |
| `HOME` | 同 `LIUSHA_PROFILE` | 浏览器和工具使用 |
| `OUTPUT_DIR` | 同 `LIUSHA_OUTPUT` | 向后兼容（可选） |

---

## 优势分析

### ✅ 统一性
- 所有目录使用同一根 `/liusha/`
- 层次清晰：assignment → agent → 功能目录

### ✅ 可维护性
```bash
# 清理一个 Assignment 的所有数据
rm -rf /liusha/<assignment_id>/

# 清理一个 Agent 的数据
rm -rf /liusha/<assignment_id>/<agent_id>/

# 查看所有 Agent
ls /liusha/<assignment_id>/
```

### ✅ 调试友好
```bash
# 进入容器
docker exec -it sandbox-xxx bash

# 查看某个 Agent 的工作目录
cd /liusha/8ebde604.../480a665e.../workspace/

# 查看产出
ls /liusha/8ebde604.../480a665e.../output/

# 查看浏览器 cookies
ls /liusha/8ebde604.../480a665e.../profile/chrome/
```

### ✅ 扩展性
```
/liusha/<assignment>/<agent>/
    ├── workspace/       ← 当前有
    ├── output/          ← 当前有
    ├── profile/         ← 当前有
    ├── logs/            ← 未来可加：Agent 级别日志
    ├── cache/           ← 未来可加：缓存目录
    └── metrics/         ← 未来可加：性能指标
```

### ✅ 安全性
- 每个 Agent 完全隔离
- 无法访问其他 Agent 的数据
- Assignment 级别也隔离

---

## 迁移检查清单

### 代码修改
- [ ] `internal/sandbox/types.go` - 更新 ExecRequest
- [ ] `internal/sandbox/server/exec.go` - 更新目录逻辑
- [ ] `internal/tools/sandbox.go` - 更新工具调用
- [ ] `internal/tools/registry.go` - 更新 Deps 结构
- [ ] `cmd/runner/handler_run.go` - 更新 handler (handleCognition, handleSolo)
- [ ] `cmd/runner/handler_run.go` - 更新 toolRecordInterceptor
- [ ] `internal/worker/handler.go` - 更新 Payload（如果需要）

### 全局重命名
- [ ] `ExecutorID` → `AgentID` (所有文件)
- [ ] `executor_id` → `agent_id` (JSON 标签)
- [ ] `HUNTER_ID` → `LIUSHA_AGENT_ID` (环境变量)
- [ ] `/home/hunter` → `/liusha/.../profile` (路径)

### 删除遗留
- [ ] 删除所有 `hunter` 相关代码
- [ ] 删除 `HUNTER_ID` 环境变量
- [ ] 更新所有注释中的旧概念

### 测试验证
- [ ] 编译通过
- [ ] run_command 执行成功
- [ ] 浏览器工具正常工作
- [ ] 文件隔离正确
- [ ] Assignment 共享容器正常

---

## 🚀 实施计划

### Phase 1: 核心修改（不破坏编译）
1. 更新 ExecRequest 结构（添加 AssignmentID，重命名为 AgentID）
2. 更新 sandbox server 目录逻辑
3. 更新 Deps 结构

### Phase 2: 全局重命名
1. ExecutorID → AgentID（所有代码）
2. 更新所有调用方

### Phase 3: 测试和部署
1. 本地测试
2. 构建新镜像
3. 部署验证

---

## 📊 对比总结

| 维度 | 旧架构 | 新架构 |
|------|--------|--------|
| 根目录 | 分散（/tmp, /workspace, /home） | 统一（/liusha） |
| 命名 | hunter, executor | 清晰（assignment, agent） |
| 隔离 | 部分共享 | 完全隔离 |
| 层次 | 混乱 | 清晰（3层） |
| 扩展性 | 差 | 好 |
| 调试性 | 难 | 易 |

