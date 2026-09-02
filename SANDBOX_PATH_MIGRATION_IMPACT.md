# 🔍 Sandbox 路径变更影响分析

## 问题：镜像中的工具是否知道路径变更？

---

## ✅ 好消息：影响很小！

### 核心原因
**所有工具都通过环境变量访问路径，没有硬编码**

---

## 📊 完整影响分析

### 1️⃣ 环境变量使用情况

#### A. sandbox-server 设置的环境变量（旧）
```go
cmd.Env = append(os.Environ(),
    "OUTPUT_DIR="+outputDir,              // /tmp/sandbox-output/<executor_id>/
    "HUNTER_ID="+req.ExecutorID,          // executor_id
)
cmd.Dir = workdir                         // /workspace/<executor_id>/
```

#### B. 新架构的环境变量
```go
cmd.Env = append(os.Environ(),
    "LIUSHA_WORKSPACE="+workspaceDir,     // /liusha/<assignment>/<agent>/workspace/
    "LIUSHA_OUTPUT="+outputDir,           // /liusha/<assignment>/<agent>/output/
    "LIUSHA_PROFILE="+profileDir,         // /liusha/<assignment>/<agent>/profile/
    "LIUSHA_AGENT_ID="+req.AgentID,       // agent_id
    "HOME="+profileDir,                   // 浏览器使用
    "OUTPUT_DIR="+outputDir,              // 向后兼容
)
cmd.Dir = workspaceDir
```

### 2️⃣ 工具如何访问路径

#### A. 命令行工具（nmap, sqlmap, nuclei 等）
✅ **无影响** - 它们使用 `cwd`（工作目录），由 `cmd.Dir` 设置

```bash
# 工具命令示例
nmap -oN output.txt target.com
# 输出到：$PWD/output.txt
# 新架构：/liusha/<assignment>/<agent>/workspace/output.txt
```

#### B. 需要输出文件的工具
✅ **无影响** - 使用 `$OUTPUT_DIR` 环境变量

```bash
# 示例：screenshot 工具
screenshot -o $OUTPUT_DIR/page.png
# 新架构：/liusha/<assignment>/<agent>/output/page.png
```

#### C. Python 脚本

##### `mitm-capture.py` - 流量抓包
```python
HUNTER_ID = os.getenv("LIUSHA_HUNTER_ID", "").strip()
```

⚠️ **需要修改**：
```python
# 旧：LIUSHA_HUNTER_ID
# 新：LIUSHA_AGENT_ID
AGENT_ID = os.getenv("LIUSHA_AGENT_ID", "").strip()
```

##### `browser-svc.py` - 浏览器服务
```python
INGEST_URL = os.getenv("LIUSHA_INGEST_URL", "").strip()
INGEST_TOKEN = os.getenv("LIUSHA_INGEST_TOKEN", "").strip()
```

✅ **无影响** - 不使用路径相关环境变量

#### D. browser-use wrapper
```bash
SOCK = f"/tmp/browser-svc-{IDENTITY}.sock"
LOG = f"/tmp/browser-svc-{IDENTITY}.log"
```

✅ **无影响** - 使用 `/tmp/` 的临时 socket，与 `/liusha/` 无关

---

## 🔧 需要修改的地方

### 1. mitm-capture.py

#### 当前代码
```python
HUNTER_ID = os.getenv("LIUSHA_HUNTER_ID", "").strip()
```

#### 修改为
```python
AGENT_ID = os.getenv("LIUSHA_AGENT_ID", "").strip()
```

#### 完整修改
```python
# Line 30
- HUNTER_ID = os.getenv("LIUSHA_HUNTER_ID", "").strip()
+ AGENT_ID = os.getenv("LIUSHA_AGENT_ID", "").strip()

# Line 97
- self._enabled = bool(INGEST_URL) and bool(HUNTER_ID)
+ self._enabled = bool(INGEST_URL) and bool(AGENT_ID)

# Line 138
- "agent_id": HUNTER_ID,
+ "agent_id": AGENT_ID,
```

### 2. sandbox-server

#### 当前代码
```go
cmd.Env = append(os.Environ(),
    "OUTPUT_DIR="+outputDir,
    "HUNTER_ID="+req.ExecutorID,
)
```

#### 修改为
```go
cmd.Env = append(os.Environ(),
    "LIUSHA_WORKSPACE="+workspaceDir,
    "LIUSHA_OUTPUT="+outputDir,
    "LIUSHA_PROFILE="+profileDir,
    "LIUSHA_AGENT_ID="+req.AgentID,
    "HOME="+profileDir,
    "OUTPUT_DIR="+outputDir,  // 向后兼容，可选
)
```

---

## 📋 完整迁移清单

### 代码修改
- [x] `internal/sandbox/server/exec.go` - 更新环境变量
- [x] `deployments/tool-images/pentools/mitm-capture.py` - HUNTER_ID → AGENT_ID

### 向后兼容
- [x] 保留 `OUTPUT_DIR` 环境变量
- [ ] 可选：保留 `LIUSHA_HUNTER_ID` 别名（不推荐）

### 测试验证
- [ ] run_command 输出文件位置正确
- [ ] screenshot 保存到 output 目录
- [ ] 浏览器 cookies 保存到 profile 目录
- [ ] mitm-capture 正常工作
- [ ] 工具配置文件正确保存

---

## ✅ 结论

### 影响范围：**非常小**

1. **99% 的工具无影响**
   - 命令行工具使用 cwd（由 cmd.Dir 控制）
   - 输出文件使用 $OUTPUT_DIR（我们会设置）

2. **只需修改 2 个地方**
   - `mitm-capture.py` - 环境变量重命名
   - `sandbox-server/exec.go` - 环境变量设置

3. **无硬编码路径**
   - 所有工具通过环境变量访问
   - 路径变更对工具透明

### 迁移风险：**低**

- ✅ 环境变量驱动的设计非常好
- ✅ 向后兼容性容易保证
- ✅ 测试覆盖简单

---

## 🚀 建议的实施顺序

1. **Phase 1**: 更新 sandbox-server 环境变量
2. **Phase 2**: 修改 mitm-capture.py
3. **Phase 3**: 测试验证所有工具
4. **Phase 4**: 删除向后兼容的环境变量（可选）

---

## 📝 环境变量对照表

| 用途 | 旧变量 | 新变量 | 状态 |
|------|--------|--------|------|
| 工作目录 | `$PWD` (cmd.Dir) | `$LIUSHA_WORKSPACE` | 透明 |
| 输出目录 | `$OUTPUT_DIR` | `$LIUSHA_OUTPUT` + `$OUTPUT_DIR` | 兼容 |
| 用户主目录 | `$HOME` | `$HOME` (新路径) | 透明 |
| Agent ID | `$HUNTER_ID` | `$LIUSHA_AGENT_ID` | 需改 |
| Assignment ID | - | `$LIUSHA_ASSIGNMENT_ID` | 新增 |
| Profile 目录 | - | `$LIUSHA_PROFILE` | 新增 |

