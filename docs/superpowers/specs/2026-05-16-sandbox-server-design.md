# Sandbox-Server 架构改造设计

**状态**: 设计中
**日期**: 2026-05-16
**作者**: V3teran
**关联**: hunter agent run 容器执行模型

---

## 背景

当前 liusha 用 `internal/tools/runners/DockerRunner` 处理工具执行：每次 LLM 调 `run_command`
→ `docker run --rm sandbox-image sh -c "<command>"` → 等容器退出 → 收 stdout/stderr。
**容器粒度 = 单次工具调用**。

这个模型对 stateless CLI 工具（sqlmap / curl / nuclei）工作得好——一次命令完成一件事，
容器用完即销毁，状态绝对隔离。但有两个引入新需求时撞墙：

1. **浏览器是 stateful 工具**——cookie / page / DOM / 已渲染状态都在 chrome 进程内存里。
   `docker run --rm` 每次新容器意味着新 chrome 进程，跨多次工具调用无法保持浏览器状态。
2. **二进制产物（截图、pcap 等）**——stdout 是文本流，无法干净返回二进制。

site 模式（下阶段）必然要浏览器爬虫，proxy 模式 hunter 也可能用浏览器复现/验证漏洞。
浏览器能力是基础设施层面的必要扩展。

## 目标

- 浏览器（chrome + browser-use）作为容器内常驻能力，状态跨工具调用保持
- LLM 接口零变化（仍然只有一个 `run_command` 工具）
- 容器粒度变为 **per agent run**（一次 `react.Run` = 一个容器），proxy / site 两种模式对称
- 二进制产物通过协议层附件机制返回（避免 stdout 二进制污染）
- 保留 `run_command.go` 注释的核心哲学："不为每个工具写 wrapper，开放 sh -c + SKILL 手册"

## 非目标（v1 显式不做）

- 不做 site 模式（下阶段做）
- 不做跨主机部署（v1 同宿主机，未来跨主机时改 transport 即可）
- 不做异步 + 轮询（同宿主机同步 HTTP 足够）
- 不用 unix socket（用 TCP，便于未来跨主机升级）
- 不引入 supervisord / browser-use-bridge / `/browser/*` 端点 / 主进程 `browser_*` 具名工具
- 不写 browser-cli SKILL.md（先观察 LLM 表现）

---

## 架构总览

```
┌──────────────────────────── 宿主机 ────────────────────────────┐
│                                                                  │
│  ┌── 主进程容器（cmd/scanner）───────────────┐                   │
│  │                                            │                   │
│  │  hunter agent run                          │                   │
│  │    ├─ SandboxLauncher (spawn / destroy)    │                   │
│  │    └─ SandboxClient (HTTP RPC)             │                   │
│  │                ↓                           │                   │
│  └────────────────│───────────────────────────┘                   │
│                   │ TCP (docker network: liusha-net)              │
│                   │ http://liusha-sandbox-<run_id>:8080            │
│                   ↓                                                │
│  ┌── sandbox 容器（per agent run，all-in-one 镜像）───────────┐  │
│  │                                                              │  │
│  │  sandbox-server (Go, PID 1, :8080)                           │  │
│  │    ├─ POST /exec    (sh -c + OUTPUT_DIR + 附件)               │  │
│  │    └─ GET  /healthz                                           │  │
│  │                                                              │  │
│  │  chrome (browser-cli 首次按需 spawn 的常驻子进程)              │  │
│  │    └─ --remote-debugging-port=9222 (CDP)                      │  │
│  │                                                              │  │
│  │  /usr/local/bin/                                              │  │
│  │    ├─ browser-cli (Python, 包装 browser-use)                 │  │
│  │    ├─ sqlmap / curl / nuclei / hydra / ffuf / ...             │  │
│  │    └─ ...                                                     │  │
│  │                                                              │  │
│  │  /tmp/exec-<uuid>/output/  (sandbox-server 每次 /exec 独立)  │  │
│  │                                                              │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                    │
└────────────────────────────────────────────────────────────────────┘
```

**关键边界**：
- sandbox-server 是 sh 执行器，**不知道 chrome / browser-use 存在**
- chrome 由 browser-cli 按需启动（detached 子进程），与 sandbox-server 解耦
- browser-cli 通过 CDP（chrome 自带远程调试协议）连接 chrome，无独立 IPC

---

## 容器内组件

### sandbox-server（Go）

容器的 PID 1，唯一常驻服务。监听 `:8080`，提供两个端点。

#### POST /exec

**请求**：
```json
{
  "command": "string，sh -c 解析（支持管道 / 重定向 / $env）",
  "timeout_seconds": 1800,
  "tag": "运维标签，[a-z0-9-]{1,32}"
}
```

**处理流程**：
1. 生成 uuid，创建独立目录 `/tmp/exec-<uuid>/output/`（权限 0777）
2. 注入环境变量 `OUTPUT_DIR=/tmp/exec-<uuid>/output`
3. 启动 sh 子进程：`exec.CommandContext(ctx, "sh", "-c", command)`，附带 OUTPUT_DIR
4. 等待命令结束或 ctx 超时（被 SIGKILL）
5. **defer 扫描** `/tmp/exec-<uuid>/output/`：
   - 只扫顶层（不递归）
   - 应用上限：单文件 <= 200KB / 总 <= 1MB / 数量 <= 5
   - 超限：返回元数据 + warning 字段，不返回内容
6. **defer 销毁** `/tmp/exec-<uuid>/`
7. 返回响应（即使命令崩溃也走 defer，半成品产物有诊断价值）

**响应**：
```json
{
  "exit_code": 0,
  "stdout": "完整 stdout（不截尾）",
  "stderr": "完整 stderr（不截尾）",
  "timed_out": false,
  "files": [
    {"name": "screenshot.png", "b64": "iVBOR..."}
  ],
  "warnings": ["file 'big.pcap' exceeded 200KB limit, skipped"]
}
```

**注意**：stdout/stderr 由 sandbox-server 完整返回，**主进程 RunCommand 负责截尾**
（沿用现有 `tailString(s, 8192)` 逻辑，保留这层是因为主进程 LLM context
管理在主进程层最直接，sandbox-server 不该做应用层裁剪）。

#### GET /healthz

返回 `{"status": "ok"}` + HTTP 200。无副作用，供 spawn 后等待容器 ready 用。

#### Max lifetime（兜底自杀）

sandbox-server 启动时记录 `startTime`，后台 goroutine 每分钟检查：
若 `time.Since(startTime) > 4h` 则进程退出（容器随之销毁）。

4 小时是远超任何正常 agent run 时长的安全冗余。**这不是 idle timeout**——
agent 思考期无 /exec 请求不触发自杀，避免误杀。

### chrome（按需启动的子进程）

- 启动方：browser-cli 第一次执行时检测 `:9222` 不通就 spawn
- 启动参数：`chrome --headless --no-sandbox --remote-debugging-port=9222 --user-data-dir=/tmp/chrome-data`
- 启动方式：detached（`setsid` + 重定向 stdio 到 /dev/null），脱离 parent shell
- 生命周期：容器销毁时随 PID 1 死亡被一起回收（不需要主动清理）

### browser-cli（Python 脚本）

- 包装 browser-use 库
- 启动流程：
  1. 检测 chrome 在 `:9222` 是否监听
  2. 不在 → detached spawn chrome，等 CDP ready
  3. `browser-use` 通过 `cdp_url="http://localhost:9222"` connect
  4. 执行 subcommand
  5. 输出到 stdout（文本类）或 `$OUTPUT_DIR/<name>`（二进制类）
  6. Python 进程退出（chrome 不退出）
- subcommand：**按 browser-use 实际能力 1:1 暴露**，不预设清单
- 默认输出位置：`$OUTPUT_DIR`（环境变量由 sandbox-server 注入）

### 镜像 `liusha/pentools:latest`

all-in-one 镜像，重新打。

**保留**（现有 pentest 工具）：
- sqlmap / curl / nuclei / nikto / hydra / ffuf / 其他

**新增**：
- chromium 浏览器二进制
- Python 3 + pip
- browser-use（pip 装；重新 pin 版本，移除现有 playwright 旧版本）
- playwright（被 browser-use 拉入，跑 `playwright install chromium`）
- `browser-cli` 脚本（写到 `/usr/local/bin/`）
- `sandbox-server` Go 二进制

**容器入口**：`CMD ["/usr/local/bin/sandbox-server"]`

---

## 主进程组件

### SandboxClient（接口）

```go
package sandbox

type Client interface {
    Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
    Close() error
}

type ExecRequest struct {
    Command        string
    TimeoutSeconds int
    Tag            string
}

type ExecResult struct {
    ExitCode int
    Stdout   string
    Stderr   string
    TimedOut bool
    Files    []Attachment
    Warnings []string
}

type Attachment struct {
    Name string
    B64  string
}
```

**实现**（`httpClient`）：
- HTTP client 持有 base URL `http://liusha-sandbox-<runID>:8080`
- HTTP timeout 31min（比 MaxTimeoutSeconds 1800s 稍大）
- 单一方法 Exec：POST /exec → 解析响应

### SandboxLauncher（容器生命周期）

```go
package sandbox

type Launcher interface {
    Spawn(ctx context.Context, runID string) (Client, error)
    Destroy(ctx context.Context, runID string) error
}
```

**Spawn 流程**：
1. `docker run -d --network=liusha-net --name=liusha-sandbox-<runID>
   --memory=2g --cpus=2 liusha/pentools:latest`
2. 轮询 `GET http://liusha-sandbox-<runID>:8080/healthz`，最长 30s（硬编码）
3. healthz 200 → 返回 Client 实例
4. 超时 → docker stop + rm + 返回 err

**Destroy 流程**：
1. `docker stop liusha-sandbox-<runID>`（默认 10s SIGTERM 后 SIGKILL）
2. `docker rm liusha-sandbox-<runID>`
3. 错误 log warn 不致命（max lifetime 或下次启动清理会兜底）

**孤儿回收**（worker 启动时一次性）：
- scanner worker 启动入口处调一次 `CleanupOrphans(ctx)`
- 逻辑：`docker ps -a --filter=name=liusha-sandbox- --format=...`
  → 全部 stop + rm
- 不是后台 sweeper，只在进程启动时跑

### RunCommand 改造

**LLM 接口零变化**：
- `Name()` / `ParametersJSON()` / 现有 schema 完全保留
- 仅 `Description()` 加一句：

> 沙箱注入了环境变量 `$OUTPUT_DIR`。命令产生二进制或大文件时写到 `$OUTPUT_DIR/xxx`
> 会作为 base64 附件返回（上限 200KB/文件, 1MB 总量, 5 文件）。文本类输出直接走 stdout 即可。

**内部 Execute 改造**：
```go
// 旧：
res, err := a.Runner.RunAndWait(ctx, spec)

// 新：
res, err := a.Sandbox.Exec(ctx, sandbox.ExecRequest{
    Command:        in.Command,
    TimeoutSeconds: in.Timeout,
    Tag:            sanitizeTag(in.Tag),
})
```

**Result.Output 扩展**：增加 `files` 字段透传 attachments；
现有 `exit_code` / `stdout_tail` / `stderr_tail` / `timed_out` 字段保持。

`stdout_tail` 截尾仍由主进程 RunCommand 做（沿用 `tailString`），sandbox-server
返完整 stdout，截尾在主进程层最贴近 LLM context 管理。

### runners.DockerRunner 删除

旧组件不再被使用，整包删除。需要 grep 确认无其他依赖（cmd/scanner 入口处装配
应同步移除）。

---

## 容器生命周期（per agent run）

**绑定到 `react.Run`**：

```
hunter Builder 被调用
  └─ launcher.Spawn(ctx, runID) -> client          // agent run 开始
       |
  react.Run(ctx, Config{Sandbox: client, ...})
       | 多次 client.Exec(...)
       |
  defer launcher.Destroy(ctx, runID)              // agent run 结束
```

**对称性**：proxy 模式（一条 traffic = 一次 react.Run）和 site 模式（一个 site
task = 一次 react.Run）使用完全相同的容器生命周期机制。

**兜底覆盖**：
- 正常路径：Destroy 主动销毁
- 主进程崩溃：sandbox-server max lifetime 4h 自杀
- 兜底失败：scanner worker 启动时 CleanupOrphans 清理前缀容器

---

## 网络与服务发现

- 一次性创建 docker network：`docker network create liusha-net`
- 所有容器（主进程 + sandbox）加入此 network
- 主进程通过容器名解析：`http://liusha-sandbox-<runID>:8080`
- **不暴露端口到宿主机**（无 `-p` 参数）
- sandbox-server 监听 `:8080`（容器内固定端口）

**未来跨主机**：把 base URL 从容器名换成服务发现（K8s service / consul），其他逻辑零改动。

---

## 关键决策记录

### 决策 1：容器粒度 = per agent run（不是 per engagement）

**讨论范围**：engagement / (engagement, host) / agent run / traffic 四种粒度

**决策**：agent run

**理由**：
- hunter 的状态共享通道是 PG（finding/lesson）+ Redis（notes），**不走容器文件系统**——
  这是项目现有显式设计，没有"长生命周期容器换状态复用"的真实收益
- engagement 粒度（24h）状态污染 + 失败放大 + 跨 host 风险
- agent run 粒度让 proxy / site 完全对称，架构无特例

### 决策 2：浏览器走 wrapper，其他工具走 sh -c

**讨论范围**：纯 sh -c CLI 路线 / wrapper 路线 / 混合

**决策**：单一 `run_command` 工具 + `$OUTPUT_DIR` 附件机制

**理由**：
- LLM 接口只有一个 run_command，沿用现有 `run_command.go` 注释里的 "不写 wrapper" 哲学
- browser-cli 在 sandbox 内部用 sh 调用，对 LLM 仍然是 shell 命令
- 截图二进制通过 OUTPUT_DIR + 附件字段返回，不走 stdout 避开 tail 截尾问题
- 附件机制是通用扩展（非浏览器专用，未来 tcpdump / wget 等都能用）

### 决策 3：sandbox-server 不感知 chrome / browser-use

**讨论范围**：是否让 sandbox-server 主管 chrome 生命周期

**决策**：sandbox-server 只做 sh 执行器，chrome 由 browser-cli 按需启动

**理由**：
- sandbox-server / chrome / browser-use 是三个独立角色，紧耦合会污染抽象
- chrome 生命周期 = 容器生命周期（容器死 chrome 死），无需 server 介入
- browser-cli 内部 spawn chrome 是 detached，PID 1 不感知也不影响

### 决策 4：同步 HTTP，不异步轮询

**讨论范围**：同步长连接 / 异步 + 轮询 / 长轮询 / SSE

**决策**：同步 HTTP，HTTP client timeout = 31min

**理由**：
- v1 同宿主机部署，无中间 LB / NAT，长连接稳定
- 工具最长 30min（`MaxTimeoutSeconds=1800`），无需异步状态恢复
- 异步 + 轮询额外 ~80 行代码，无真实收益（v1 没跨主机需求）
- 未来跨主机：再升级到异步，业务代码改动小

### 决策 5：TCP 而非 unix socket

**讨论范围**：unix socket / TCP

**决策**：TCP（docker network 内）

**理由**：
- 同宿主机 unix socket 更优雅，但跨主机升级要重写 transport
- TCP 升级路径更平滑（换 DNS 即可）
- TCP 调试方便（curl 直接打）
- v1 在 docker network 内，端口不暴露到宿主机，安全等价

### 决策 6：browser-use 而非自写 chromedp 抽象

**讨论范围**：browser-use（Python） / chromedp（Go） / playwright-go

**决策**：browser-use（容器内 Python sidecar）

**理由**：
- browser-use 已实现 LLM 友好的 DOM → 元素索引抽象，自写 chromedp 等于重新发明
- 容器内 Python vs Go 不影响主进程（HTTP RPC 透明）
- 违反"研究优先，能用现成的就别造"原则的话才该警惕

### 决策 7：max lifetime 4h 硬编码，不暴露配置

**讨论范围**：是否做成 yaml 配置项

**决策**：硬编码常量（`maxLifetimeFallback = 4 * time.Hour`），带注释解释

**理由**：
- 项目现有 `run_command.go` 大量 `fallback*` 常量模式表明"偏好硬编码安全默认值"
- 4h 远超任何 agent run 时长，几乎不会触发，无需暴露
- 减少 yaml 配置项膨胀

---

## 验收标准

- [ ] proxy 模式现有所有工具零回归（CI 端到端测试）
- [ ] 浏览器场景跑通（具体场景由 V3teran 定，建议跑通一个含登录的访问流程）
- [ ] 容器生命周期干净：spawn → 多次 /exec → destroy 全程 `docker ps` 无残留
- [ ] 失败路径回收正常：
  - 命令崩溃：半成品文件仍能返回，临时目录销毁
  - 命令超时：SIGKILL 后正常返回 timed_out=true
  - agent run abort：主进程 cancel ctx → 容器 destroy
  - 主进程崩溃模拟：手动 kill scanner → 下次 scanner 启动清理孤儿
- [ ] `internal/tools/runners/DockerRunner` 完全删除（grep 确认无引用）
- [ ] design doc 已 review

---

## 执行顺序

**自底向上，每步独立可测**：

1. **镜像 + sandbox-server + browser-cli**（容器内部独立可跑）
   - Dockerfile 重打
   - sandbox-server Go 项目（`internal/sandbox/server/`）
   - browser-cli Python 脚本（`scripts/browser-cli/`）
   - 容器内手动测试：起容器 → curl /exec / browser-cli screenshot 等

2. **SandboxClient + SandboxLauncher**（主进程容器生命周期）
   - `internal/sandbox/client.go`
   - `internal/sandbox/launcher.go`
   - 单元测试 + 集成测试（mock + 真 docker）

3. **RunCommand 改造 + DockerRunner 删除**
   - `internal/tools/external/run_command.go` 改 Execute
   - 删除 `internal/tools/runners/`
   - cmd/scanner 装配处同步更新
   - hunter Builder 入口处加 Spawn / Destroy

4. **端到端验收**
   - proxy 模式回归
   - 浏览器场景跑通
   - 失败路径覆盖

---

## 测试范围

**sandbox-server 单元测试**：
- /exec 正常流程（含 OUTPUT_DIR 注入 + 文件扫描）
- 附件上限触发
- 命令超时被 SIGKILL
- 命令崩溃半成品返回
- max lifetime 触发自杀（用短常量模拟）

**SandboxClient 集成测试**（含真 docker）：
- Spawn + healthz → Exec → Destroy 全流程
- timeout 场景
- ctx abort 场景

**端到端**：
- proxy 模式回归（现有 e2e）
- 浏览器场景

**显式不测**：
- 容器失联（无证据需要，max lifetime + 启动清理已兜底）

---

## 影响范围

**新增**：
- `internal/sandbox/`（Client / Launcher / shared types）
- `cmd/sandbox-server/`（Go 二进制 main）
- `scripts/browser-cli/`（Python 脚本）
- `deployments/docker/Dockerfile.pentools`（如有现成 Dockerfile 则改）

**修改**：
- `internal/tools/external/run_command.go`（Execute 内部）
- `internal/builder/hunter/*.go`（Builder 入口 spawn / destroy）
- `cmd/scanner/main.go`（装配 SandboxLauncher，启动时 CleanupOrphans）
- `config/*.yaml`（新增 sandbox 镜像名 + network 名两个配置项）

**删除**：
- `internal/tools/runners/`（整包）
