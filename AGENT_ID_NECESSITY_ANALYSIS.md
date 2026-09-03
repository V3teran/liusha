# 🔍 Agent ID 必要性严谨评估

## 核心问题：还需要 agent_id 吗？

---

## 📊 当前架构

```
/liusha/<task_id>/
  ├── profile/              ← Task 共享（浏览器登录态）
  └── <agent_id>/
      ├── workspace/        ← Agent 隔离
      └── output/           ← Agent 隔离
```

**问题**：workspace 和 output 真的需要按 agent_id 隔离吗？

---

## 🔬 场景分析

### 场景 A：单 Task 单 Agent（最常见）
```
Task-123
  └── Planner Agent (UUID: aaa-111)
```

**需要 agent_id 吗？**
- workspace: 只有一个 Agent，不需要隔离
- output: 只有一个 Agent，不需要隔离
- **结论**：❌ 不需要

---

### 场景 B：单 Task 多 Agent（串行执行）
```
Task-123
  ├── Planner Agent (UUID: aaa-111) [完成，已退出]
  └── Exploitation Agent (UUID: bbb-222) [运行中]
```

#### 问题 1：Exploitation 需要看到 Planner 的产出吗？

**实际业务**：
- Planner 产出：扫描报告、截图、抓包文件
- Exploitation 需要：读取 Planner 的发现

**如果隔离**：
```
/liusha/task-123/
  ├── aaa-111/
  │   └── output/
  │       └── scan-report.txt     ← Exploitation 看不到！
  └── bbb-222/
      └── workspace/
```

**如果不隔离**：
```
/liusha/task-123/
  ├── workspace/
  └── output/
      └── scan-report.txt         ← Exploitation 可以看到
```

**结论**：✅ 不隔离更好！Agent 之间需要共享数据

#### 问题 2：文件会冲突吗？

**可能的冲突**：
```
Planner:      wget -O result.html http://target.com/
Exploitation: wget -O result.html http://target.com/page2
# 后者会覆盖前者
```

**但是**：
- Planner 执行完后已经退出
- Exploitation 开始时，Planner 的文件已经生成
- 即使覆盖，也是有意为之（Agent 自己的决策）

**结论**：⚠️ 可能冲突，但不严重

---

### 场景 C：单 Task 多 Agent（并发执行）

```
Task-123
  ├── Planner Agent (UUID: aaa-111) [同时运行]
  └── Exploitation Agent (UUID: bbb-222) [同时运行]
```

#### 问题 1：会并发写同一个文件吗？

**实际情况**：
- Planner 和 Exploitation 通常不会写同名文件
- 即使写，也是不同内容（Planner 扫描报告 vs Exploitation payload）

**如果真的冲突**：
```bash
# Planner
nmap -oN scan.txt target.com

# Exploitation (同时)
nmap -oN scan.txt target.com/admin
# 文件内容混乱！
```

**结论**：⚠️ 并发冲突是问题！

#### 问题 2：目前有并发场景吗？

**检查代码**：
```
# task 表没有 "并发" 字段
# agent 是串行调度的
```

**当前架构**：Task 内的 Agent 是**串行执行**，不是并发

**结论**：✅ 当前不存在并发场景

---

### 场景 D：未来可能的并发

**假设未来支持**：
```
Task-123: 多角度扫描
  ├── PortScan Agent [并发]
  ├── WebScan Agent [并发]
  └── VulnScan Agent [并发]
```

**问题**：三个 Agent 同时写 `scan-result.txt`？

**解决方案 A**：按 agent_id 隔离
```
/liusha/task-123/
  ├── portscan-uuid/output/scan-result.txt
  ├── webscan-uuid/output/scan-result.txt
  └── vulnscan-uuid/output/scan-result.txt
```

**解决方案 B**：Agent 自己命名
```
/liusha/task-123/output/
  ├── portscan-result.txt     ← Agent 自己加前缀
  ├── webscan-result.txt
  └── vulnscan-result.txt
```

**对比**：
- 方案 A：强制隔离，但其他 Agent 看不到彼此的产出
- 方案 B：靠 Agent 自律，但可以共享数据

**结论**：⚠️ 如果未来有并发，方案 A 更安全

---

## 🤔 Workspace 和 Output 的区别

### Workspace（工作目录）

**用途**：命令执行的 cwd
```bash
cd /liusha/task-123/workspace/
nmap -oN result.txt target.com
# result.txt 写到 workspace/
```

**特点**：
- 临时文件、中间产物
- Agent 自己的"草稿纸"
- 通常不需要共享

### Output（产出目录）

**用途**：显式产出（通过 `$OUTPUT_DIR`）
```bash
screenshot -o $OUTPUT_DIR/page.png
mitmproxy -w $OUTPUT_DIR/traffic.har
```

**特点**：
- 正式产出，需要返回给主进程
- 可能需要共享给其他 Agent

---

## 💡 核心矛盾

### 矛盾 A：共享 vs 隔离

**共享的好处**：
- ✅ Agent 之间可以读取彼此的产出
- ✅ 简单（只有 task_id 一层）

**隔离的好处**：
- ✅ 避免文件冲突
- ✅ 清晰（哪个 Agent 产生的）
- ✅ 支持未来并发

### 矛盾 B：当前需求 vs 未来扩展

**当前**：
- 串行执行
- 不存在并发冲突
- **不需要** agent_id

**未来**：
- 可能并发
- 需要隔离
- **需要** agent_id

---

## 🎯 方案对比

### 方案 A：不要 agent_id（最简）

```
/liusha/<task_id>/
  ├── profile/
  ├── workspace/
  └── output/
```

**优点**：
- ⭐⭐⭐⭐⭐ 最简单
- ✅ Agent 之间自动共享数据
- ✅ 当前架构完全够用

**缺点**：
- ❌ 不支持并发
- ❌ 文件可能冲突（串行时不严重）

---

### 方案 B：保留 agent_id（当前方案）

```
/liusha/<task_id>/
  ├── profile/              ← 共享
  └── <agent_id>/
      ├── workspace/        ← 隔离
      └── output/           ← 隔离
```

**优点**：
- ✅ 完全隔离，无冲突
- ✅ 支持未来并发
- ✅ 清晰（产出归属明确）

**缺点**：
- ❌ Agent 之间无法直接共享数据
- ❌ 稍微复杂

---

### 方案 C：混合方案

```
/liusha/<task_id>/
  ├── profile/              ← 共享
  ├── shared/               ← 共享数据（Agent 之间交换）
  │   └── findings/
  └── <agent_id>/
      ├── workspace/        ← 隔离（草稿）
      └── output/           ← 隔离（正式产出）
```

**优点**：
- ✅ 隔离 + 共享兼得
- ✅ 支持并发

**缺点**：
- ❌ 复杂
- ⚠️ Agent 需要知道 shared/ 的存在

---

## 🔍 检查实际使用

### Output 产出如何使用？

#### 代码中的 collectAttachments
```go
// internal/sandbox/server/exec.go
files, err := collectAttachments(outputDir, execStart)
// 扫描 outputDir，返回新增/修改的文件
```

**关键**：产出文件会**返回给主进程**，不是留在 Sandbox 里给其他 Agent 用！

#### 流程
```
1. Planner 执行命令
2. 产出文件写到 output/
3. sandbox-server 扫描 output/，b64 编码
4. 返回给主进程（runner）
5. Exploitation 执行时，从主进程获取数据（通过 World Model）
```

**结论**：✅ Agent 之间不通过文件系统共享，而是通过主进程（World Model）！

---

## ✅ 最终结论

### **不需要 agent_id！**

### 核心理由

1. **Agent 之间不直接读文件**
   - 产出通过主进程（World Model）传递
   - 不需要文件系统级别的共享

2. **当前无并发场景**
   - Task 内 Agent 串行执行
   - 文件冲突风险低

3. **KISS 原则**
   - 最简单的方案：只有 task_id
   - 足够满足当前需求

4. **workspace 是临时的**
   - 命令执行的 cwd
   - 容器销毁时自然清理
   - 不需要按 Agent 追踪

---

## 🏗️ 最终架构（最简方案）

### 目录结构
```
/liusha/<task_id>/
  ├── profile/              ← 浏览器登录态（共享）
  ├── workspace/            ← 命令工作目录（共享，临时）
  └── output/               ← 命令产出（共享，返回主进程）
```

### ExecRequest
```go
type ExecRequest struct {
    TaskID         string `json:"task_id"`
    Command        string `json:"command"`
    TimeoutSeconds int    `json:"timeout_seconds"`
    Tag            string `json:"tag,omitempty"`
}
```

### 环境变量
```go
cmd.Env = append(os.Environ(),
    "LIUSHA_TASK_ID="+req.TaskID,
    "LIUSHA_WORKSPACE="+workspaceDir,  // /liusha/<task_id>/workspace/
    "LIUSHA_OUTPUT="+outputDir,         // /liusha/<task_id>/output/
    "LIUSHA_PROFILE="+profileDir,       // /liusha/<task_id>/profile/
    "HOME="+profileDir,
    "OUTPUT_DIR="+outputDir,            // 向后兼容
)
cmd.Dir = workspaceDir
```

---

## 📊 最终对比

| 维度 | 仅 task_id | task_id + agent_id |
|------|-----------|-------------------|
| 简洁性 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ |
| 当前需求 | ✅ 完全满足 | ✅ 满足 |
| 并发支持 | ❌ 不支持 | ✅ 支持 |
| 数据共享 | ✅ 自动 | ❌ 需手动 |
| 实际使用 | ✅ 通过 World Model | ✅ 通过 World Model |
| 文件冲突 | ⚠️ 可能（但罕见） | ✅ 无 |

**推荐**：⭐ **仅 task_id**

理由：当前架构是串行执行 + World Model 传递数据，agent_id 是过度设计。

