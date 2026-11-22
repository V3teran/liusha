# 部署

liusha 是一个程序，组件是它的子命令：`api`（控制台与 API）、`proxy`（MITM 代理入口）、
`runner`（任务执行）、`sandbox`（工具执行沙盒）、`migrate`（数据库迁移与出厂内容同步），`serve` 在同一
进程运行全部组件。三种部署方式只是组件的摆放不同：

| | 二进制 | 单镜像 | 多镜像 |
|---|---|---|---|
| 组件 | `liusha serve` 一个进程 | 一个容器内 `liusha serve` | api / proxy / runner / sandbox 各一个容器 |
| PostgreSQL / Redis | 自备 | 镜像内置（pg18） | 独立容器（pg17） |
| 渗透工具 | 本机安装（Linux / macOS / Windows） | 沙盒镜像（本地构建） | 沙盒镜像（本地构建） |
| 对外入口 | :8580 | :8580（映射到宿主自定） | nginx :50080 |
| 数据库迁移与出厂内容同步 | `serve` 启动时 | `serve` 启动时 | 一次性的 `migrate` 服务 |

发布产物只有 Releases 中的二进制压缩包与 `ghcr.io/v3teran/liusha` 镜像；沙盒镜像与单镜像由
部署方本地构建。

## 配置文件与 .env

每种部署方式都由两份文件驱动，三种方式用的是同一份模板：

- **`config.yaml`**（仓库里是 `internal/config/config.yaml`，发布压缩包与镜像里各附一份）：全部配置项，
  是唯一的配置来源。程序不内置配置，读不到文件、缺键、有不认识的键都拒绝启动；升级后对照新版本补齐
  新增的键。文件只写字面值，密钥项留空。
- **`.env`**（由 `.env.example` 复制填写）：密钥，以及容器部署需要的几个变量。

| | `config.yaml` | `.env` | 数据目录 |
|---|---|---|---|
| 二进制 | 程序所在目录 | 程序所在目录，`-env-file .env` 读入 | `config.yaml` 的 `data_dir`（`~/data/liusha`） |
| 单镜像 | 宿主 `~/data/liusha/config.yaml` → 容器 `/data/config.yaml` | 仓库根，`docker run --env-file .env` | 容器 `/data`（宿主 `~/data/liusha/single`） |
| 多镜像 | 宿主 `${LIUSHA_DATA_DIR}/config.yaml` → 容器 `/data/config.yaml` | 仓库根，`docker compose` 自动读取 | `LIUSHA_DATA_DIR`（`~/data/liusha`），容器内同一路径 |

编辑后重启生效。另行指定配置文件用 `-config <文件>` 或环境变量 `LIUSHA_CONFIG`（容器镜像就是用它
指向 `/data/config.yaml`）。

环境变量一律以 `LIUSHA_` 开头，分两种：

- **名字对上配置键的，覆盖 `config.yaml` 里的值**：键 `a.b_c` 对应 `LIUSHA_A_B_C`（如
  `runner.concurrency` → `LIUSHA_RUNNER_CONCURRENCY`），值为空串时不覆盖。`LIUSHA_CONFIG` 指定配置文件
  本身的位置。
- **其余是部署变量**，给容器与 Docker Compose 用，liusha 不读：`LIUSHA_POSTGRES_PASSWORD`、
  `LIUSHA_REDIS_PASSWORD`（自带数据库的密码，liusha 的连接串由容器按它们拼出）、宿主端口、镜像。
  部署变量不与任何配置键同名。

统一的前缀让 liusha 的变量不与其它软件常用的名字（`REDIS_URL`、`LOG_LEVEL`、`DATA_DIR`……）相撞，
沙盒也只需「去掉全部 `LIUSHA_` 变量」一条规则，就保证 liusha 的密钥不进入命令环境。

`.env` 里的密钥：

| 变量 | 配置键 | 用途 | 需要它的组件 |
|---|---|---|---|
| `LIUSHA_SANDBOX_TOKEN` | `sandbox.token` | 沙盒与 runner / api 的共享密钥 | sandbox、runner、api |
| `LIUSHA_LLM_ENCRYPTION_KEY` | `llm.encryption_key` | 加密数据库里保存的 LLM 密钥（32 字节 hex），生成后不要更改 | api、runner |
| `LIUSHA_ADMIN_INITIAL_PASSWORD` | `admin.initial_password` | 首次启动创建 admin 的密码 | api |
| `LIUSHA_POSTGRES_URL` | `postgres.url` | 二进制：数据库连接串（容器部署由容器拼出） | 除 sandbox 外全部 |
| `LIUSHA_REDIS_URL` | `redis.url` | 二进制：Redis 连接串 `redis://[用户名]:密码@主机:端口/库号`，TLS 用 `rediss://` | 除 sandbox 外全部 |

密钥与密码用 `openssl rand -hex` 生成：只含十六进制字符，放进连接串无需转义。LLM provider 与
检索模型的密钥都在控制台「模型配置」页维护，加密保存在数据库（检索模型不配则知识库只做字面检索）。

**数据目录**（`data_dir`）是持久数据的根：`workspace/`（任务工作区）、`logs/`（日志）、`certs/`
（代理的根证书与私钥）都在其下。单镜像与多镜像的数据库数据也放在同一目录
（`pg/`、`postgres/`、`redis/`）。

## 代理

浏览器把 HTTP 代理指到 proxy，访问目标站的流量会被捕获，分批交给智能体分析。控制台「代理流量」页
（或对话页的「代理捕获」）右上角的「代理接入」给出接入所需的一切：代理是否在运行、最近是否收到请求、
代理地址、根证书下载与各系统的安装步骤。

- **代理地址**（`proxy.public_addr`）：客户端设置代理时填的地址。只写 `:端口` 时主机即控制台所在的
  主机。留空时由 `proxy.listen_addr` 推出，二进制部署不用配置；单镜像与多镜像缺省为
  `:${LIUSHA_PROXY_HOST_PORT}`（宿主上映射出的端口）。控制台与代理不在同一主机（如控制台放在另一台
  反向代理后）时，在 `.env` 写完整地址 `LIUSHA_PROXY_PUBLIC_ADDR=主机:端口`。
- **根证书**：proxy 首次启动时在 `<data_dir>/certs` 生成，永久有效，每个浏览器或设备安装并信任一次
  即可。不信任它，浏览器会拦下所有 HTTPS 站点。证书私钥只在 proxy 本机，控制台只提供证书本身。

## 沙盒

runner 的工具命令都经沙盒服务执行（`sandbox.url` + `sandbox.token`）。沙盒按任务分目录
（`<工作区>/<批次>/<任务>/`），任务的一次运行结束时结束它遗留的进程与浏览器会话。

- **工具**：工具页标出沙盒未安装的工具，智能体的工具名单里「已允许 · 未安装」的工具运行时
  不可用，装上后自动生效。二进制部署时工具就是本机安装的那些。
- **浏览器**：`drive_browser` 需要装了 browser-use 0.13.x（1.0 起接口不兼容）的 Python
  （`sandbox.browser_python`，沙盒镜像里已配好）。不可用时工具页会说明缺什么。
- **工作区**：api（文件页、附件）与沙盒必须看到同一目录、同一绝对路径（`<data_dir>/workspace`）。
  多镜像部署以同一路径挂进两个容器。
- **回连**：反弹 shell / 代理 / payload 托管只绑对外监听段中的端口：二进制取 `config.yaml` 的
  `sandbox.listener_ports`；单镜像与多镜像要按它发布同号端口，在 `.env` 里写定
  `LIUSHA_SANDBOX_LISTENER_PORTS`（同一个配置键，覆盖 `config.yaml`）。
  `sandbox.callback_host`（`config.yaml`）是目标用来回连的地址；容器里探测不到宿主地址，容器部署
  需要回连时必须填写，并放行该端口段。
- 沙盒执行 agent 编写的任意命令（root 运行）：命令环境里没有 liusha 的密钥（`LIUSHA_` 开头的变量与
  数据库密码都不传给命令），沙盒令牌也不会交给命令；但命令能访问沙盒所在机器上的一切。多镜像部署时
  沙盒是独立容器，只挂载任务工作区与日志；二进制与单镜像部署时沙盒与平台同机（同容器），命令可以读到
  平台的数据目录——需要隔离时选多镜像。沙盒端口不要对外暴露（多镜像只绑回环）。

## 二进制

Release 提供 Linux、macOS（tar.gz）与 Windows（zip）的 amd64 / arm64 程序，三者功能一致。
压缩包里程序、`config.yaml` 与 `.env.example` 在同一目录：

```text
liusha            程序（Windows 为 liusha.exe）
config.yaml       配置，按需修改
.env.example      环境变量模板（三种部署方式共用），复制为 .env 填写
```

```bash
cp .env.example .env && chmod 600 .env
# 编辑 .env：填写密钥；取消注释并填写 LIUSHA_POSTGRES_URL、LIUSHA_REDIS_URL
./liusha serve -env-file .env
```

- 数据库需已安装 pgvector（`CREATE EXTENSION vector` 由迁移执行，账号需相应权限）；`serve` 启动时
  自动迁移数据库。
- `liusha` 读自己所在目录的 `config.yaml`（与工作目录无关，以服务方式运行也一样）。`.env` 只在
  `-env-file` 显式给出时读入（已设置的环境变量优先）；systemd 也可用 `EnvironmentFile=` 加载同一文件。
  模板里单镜像 / 多镜像的项默认是注释掉的，二进制不用取消注释。
- 密钥用 `openssl rand -hex 32` 生成；Windows 上没有 openssl 时用 PowerShell：
  `$b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); -join ($b | % { $_.ToString('x2') })`。
  `LIUSHA_LLM_ENCRYPTION_KEY` 生成后不要更改，否则已保存的 LLM 密钥无法解密。
- `.env` 含密钥，只对运行账户开放：POSIX 上 `chmod 600 .env`；Windows 上
  `icacls .env /inheritance:r /grant:r "<运行账户>:F" "SYSTEM:F" "Administrators:F"`。
- 数据在 `data_dir`（模板为 `~/data/liusha`，即运行账户的主目录下）。
- 智能体的命令按 POSIX sh 语法编写，沙盒用 `sandbox.shell` 执行，写空串即在本机查找（PATH 中的
  `sh`；Windows 上再找 Git for Windows）。渗透工具装在本机并加入 PATH，工具页按命令实际看到的 PATH
  标出未安装的工具。
- 浏览器能力：装了 `browser-use==0.13.14` 的 Python（`sandbox.browser_python`，写空串即 `python3`，
  Windows 为 `python`；建议用独立 venv）。浏览器内核优先用本机已装的 Chrome / Chromium / Edge
  （Windows 自带 Edge）；都没有时执行一次 `uvx playwright install chromium`（需要 uv），或在首次
  打开浏览器时由它自动拉取。
- 停止时在途任务按中断收尾（至多 `runner.drain_timeout_seconds`，模板为 30 秒），由下次启动续跑；
  托管方的停止等待须长于它。
- 升级：替换程序，对照新压缩包里的 `config.yaml` 与 `.env.example` 补齐新增的项，重启；`serve`
  启动时自动迁移。
- 沙盒执行智能体编写的任意命令：**用专门的低权限账户运行**，不要用 root / LocalSystem。

### Linux

systemd 托管（`/etc/systemd/system/liusha.service`）：

```ini
[Unit]
Description=liusha
After=network-online.target postgresql.service redis.service

[Service]
User=liusha
ExecStart=/opt/liusha/liusha serve
EnvironmentFile=/opt/liusha/.env
TimeoutStopSec=60
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### macOS

- 依赖可用 Homebrew 安装：`brew install postgresql@17 pgvector redis`，工具同样（`brew install nmap …`）。
- 程序未经 Apple 公证：用浏览器下载的压缩包带隔离属性，解压后执行一次
  `xattr -d com.apple.quarantine liusha`（用 curl 下载则没有）。
- launchd 托管（`~/Library/LaunchAgents/liusha.plist`，`launchctl load` 后随登录启动）：

  ```xml
  <?xml version="1.0" encoding="UTF-8"?>
  <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
  <plist version="1.0"><dict>
    <key>Label</key><string>liusha</string>
    <key>ProgramArguments</key><array>
      <string>/opt/liusha/liusha</string><string>serve</string>
      <string>-env-file</string><string>/opt/liusha/.env</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
    <key>ExitTimeOut</key><integer>60</integer>
  </dict></plist>
  ```

  launchd 不继承登录 shell 的 PATH：Homebrew 装的工具需在 `.env` 里写
  `PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"`。

### Windows

两条路线：

- **完整工具集：WSL2。** 在 WSL2 的 Linux 发行版（如 Kali）里运行 Linux 版 `liusha`，工具、
  命令语义与 Linux 完全一致。控制台与代理端口在 Windows 上经 `localhost` 可达。
- **原生：`liusha.exe`。** 需要提供 sh 的环境：安装 Git for Windows 即可（沙盒自动找到它的
  `bin\sh.exe`，装在别处或用 MSYS2 时在 `config.yaml` 的 `sandbox.shell` 指定）。渗透工具装 Windows 版
  （nmap、sqlmap、nuclei、ffuf 等都有）。ARM 机型（如骁龙 Copilot+ PC）用 `windows_arm64` 程序；
  浏览器能力要装 **x64 版** Python 再装 browser-use——它依赖的 cryptography 不提供 ARM64 安装包，
  ARM64 版 Python 装不上；x64 版由 Windows 自动转译运行，浏览器（Edge）仍是原生 ARM。

原生运行时可作为 Windows 服务（以管理员身份执行；服务名任取）：

```powershell
sc.exe create liusha binPath= "\"C:\liusha\liusha.exe\" serve -env-file C:\liusha\.env" start= auto obj= ".\liusha-svc" password= "<密码>"
sc.exe failure liusha reset= 86400 actions= restart/5000
sc.exe start liusha
```

`liusha` 由服务控制管理器启动时按服务运行，停止与关机时中断在途任务、收尾后退出。服务账户
用专门的低权限账户；工具与 Python 须对该账户可用（装在系统范围，或装在该账户下），`.env`
只对该账户与管理员开放（见上文 `icacls`）。

## 单镜像

```bash
make single-build                                   # 先构建沙盒镜像，再以它为底构建 liusha-single:local
cp .env.example .env                                # 填写密钥与容器部署必填项（与多镜像同一份）
mkdir -p ~/data/liusha && cp internal/config/config.yaml ~/data/liusha/   # 配置文件，按需修改
make single-run                                     # 即下面这条命令（端口与监听段取自 .env，缺省如下）
docker run -d --name liusha-single --stop-timeout 60 --env-file .env \
  -p 50080:8580 -p 58588:8588 -p 21000-21299:21000-21299 -v ~/data/liusha/single:/data \
  --mount type=bind,src=$HOME/data/liusha/config.yaml,dst=/data/config.yaml,readonly liusha-single:local
```

一个容器运行 PostgreSQL 18（pgvector）、Redis 与 `liusha serve`，任一进程退出即整体停止，交给
`--restart` 等编排重启。用法与多镜像一致：

- **`.env`** 与多镜像同一份、同一组变量，缺了必填项容器直接退出并列出缺哪些。内置 PostgreSQL / Redis
  用其中的 `LIUSHA_POSTGRES_PASSWORD` / `LIUSHA_REDIS_PASSWORD`（只监听容器内回环，且一律凭密码连接）。
- **配置文件**：宿主的 `~/data/liusha/config.yaml` 只读挂成容器内的 `/data/config.yaml`，编辑后
  `docker restart liusha-single` 生效；文件不存在时 `docker run` 直接报错。
- **数据**在 `/data`（宿主 `~/data/liusha/single`）。
- **端口**：控制台与代理的宿主端口取 `.env` 的 `LIUSHA_CONSOLE_HOST_PORT` / `LIUSHA_PROXY_HOST_PORT`
  （与多镜像同义，缺省 50080 / 58588）；对外监听段按 `LIUSHA_SANDBOX_LISTENER_PORTS` 用同号端口发布
  （`-p 段:段`）。需要回连时在 `config.yaml` 填 `sandbox.callback_host`。
- `--stop-timeout 60` 留出停机收尾时间：在途任务按中断收尾（至多 `runner.drain_timeout_seconds`，
  模板为 30 秒）后数据库才关闭，Docker 缺省的 10 秒会在收尾中途强杀。

目标机架构不同时构建加 `--platform`；离线分发用 `docker save liusha-single:local | gzip`。

## 多镜像

```bash
cp .env.example .env                                   # 填写密钥与容器部署必填项
mkdir -p ~/data/liusha && cp internal/config/config.yaml ~/data/liusha/  # 配置文件，按需修改
docker compose up -d --build
```

配置文件与数据都在 `LIUSHA_DATA_DIR`（缺省 `~/data/liusha`）下：`config.yaml`、postgres、redis、
workspace、logs、certs。`config.yaml` 挂成各 liusha 容器的 `/data/config.yaml`（只读），编辑宿主上的
文件后 `docker compose restart` 生效；文件不存在时 `docker compose up` 直接报错。数据目录在各容器里
就是 `LIUSHA_DATA_DIR` 这个宿主路径（workspace / logs / certs 以同一路径挂载）。宿主端口：控制台 50080、
代理 58588；沙盒 58591、PostgreSQL 55432、Redis 56379 只绑回环（供宿主上运行的开发进程访问）。改端口、
镜像等可选项见 `.env.example`。

## 本地开发

```bash
cp .env.example .env                                   # 按多镜像填写（密钥与容器部署必填项）
mkdir -p ~/data/liusha && cp internal/config/config.yaml ~/data/liusha/   # 宿主进程与沙盒容器共用这份配置
make dev-up               # 起 pg + redis + 沙盒容器并迁移（首次构建沙盒镜像较久）
./scripts/dev/run-svc.sh  # 宿主上运行 proxy + api + runner（日志在 ~/data/liusha/logs）
make run-web              # 前端 dev server :5173，/api 代理到 :8580
make dev-down             # 收工（-v 清数据）
```

宿主上的组件由 `scripts/dev/env.sh` 按 `.env` 拼出连接串，经宿主端口访问容器；runner 经
`127.0.0.1:58591` 调用容器里的沙盒，沙盒的浏览器流量回灌到 `host.docker.internal:8590`。

## 从 0.11 升级

**所有部署方式**

- `config.yaml` 的 `proxy` 段新增 `public_addr`（见上文「代理」），从新版本的模板复制这一行；缺这个键
  会拒绝启动。单镜像与多镜像保持空值即可（缺省按 `LIUSHA_PROXY_HOST_PORT` 给出）。
- 根证书换为永久有效的「liusha CA」：旧版本生成的「Proxify CA」有效期只有一年，到期后代理无法启动，
  升级后首次启动 proxy 时自动更换（日志里有一条警告）。在「代理接入」下载新证书重新安装，旧证书可以
  从信任列表里删掉。

## 配置文件与 .env 统一

此前配置编在程序里，用 `LIUSHA_<键>` 环境变量或只写改动键的覆盖文件修改；各部署方式的 `.env`
写法也各不相同。现在 `config.yaml` 是必须提供的完整文件，`.env` 三种方式共用一份模板，变量名按上文
「配置文件与 .env」的规则统一。升级时：

**所有部署方式**

- 放入同版本的 `config.yaml`（位置见上文），原先用 `LIUSHA_<键>` 或覆盖文件改过的配置改到这份里。
  原先省略某键、靠程序补默认值的写法不再成立：数量与时长写 0 会拒绝启动，按模板里的取值填写。
- 配置键改名（写在 `config.yaml` 里或用 `LIUSHA_<键>` 覆盖的都要改）：

  | 原键 | 新键 |
  |---|---|
  | `postgres.dsn` | `postgres.url` |
  | `redis.addr` + `redis.password` | `redis.url`（`redis://:密码@主机:端口/0`） |
  | `runner.healthz_addr` | `runner.listen_addr` |
  | `runner.asynq_concurrency` | `runner.concurrency` |
  | `runner.asynq_shutdown_timeout_seconds` | `runner.drain_timeout_seconds` |
  | `sandbox.ingest_url` | `runner.url`（只写基础地址，如 `http://127.0.0.1:8590`） |
  | `proxy.max_request_body_size` / `max_response_body_size` | `proxy.max_request_body_bytes` / `max_response_body_bytes`（系统设置里的自定义由迁移改写） |
  | `llm.invocation.retention_days` | `llm.invocation_retention_days` |
  | `sandbox.workspace_dir`、`proxy.cert_dir`、`LIUSHA_LOG_DIR` | 统一为 `data_dir` 下的 `workspace/`、`certs/`、`logs/` |

- 删除的键（内部约定改为程序常量，写了会因不认识而拒绝启动）：`proxy.stream_name`、
  `proxy.internal_addr`、`ingestor.consumer_group` / `consumer_name` / `read_batch` /
  `read_block_timeout_ms` / `retry_delay_ms` / `recreate_group_delay_ms`、`credential` 整段、
  `runner.queue_agent_weight`、`llm.invocation` 的 `buffer_size` / `batch_size` / `flush_interval_ms` /
  `insert_timeout_sec`；`llm.complexity_routes` 与 `providers`（见下）。
- 新增的键：`data_dir`、`log` 段（取代 `LIUSHA_LOG_*` 与 `LIUSHA_ENV` 的代码内默认）、`admin`、
  `embedding`、`llm.encryption_key`、`runner.url`、`runner.read_header_timeout_seconds`。
- 环境变量改名：`LIUSHA_LLM_KEY_SECRET` → `LIUSHA_LLM_ENCRYPTION_KEY`（值不变）；`JINA_API_KEY` →
  `LIUSHA_EMBEDDING_API_KEY`；`LIUSHA_POSTGRES_DSN` → `LIUSHA_POSTGRES_URL`；`LIUSHA_REDIS_ADDR` +
  `LIUSHA_REDIS_PASSWORD` → `LIUSHA_REDIS_URL`。`LIUSHA_ENV`、`LIUSHA_INSTANCE` 删除（日志格式改
  `log.format`；实例名固定为「进程号@主机名」）。
- **LLM 种子删除**：`config.yaml` 不再声明 provider，LLM 只在控制台「模型配置」页接入。原先靠环境
  变量取密钥的 provider（由种子导入、未在页面上填过密钥）升级后显示「未设置密钥」，在页面上重新填写
  一次即可；对应的环境变量可以删掉。
- **MITM 根证书**移到 `<data_dir>/certs`：二进制部署原在 `~/.liusha`，把其中的 `cacert.pem` /
  `cakey.pem` 移过去即可沿用已信任的 CA（否则启动时生成新 CA，客户端需重新信任）；多镜像原在
  `${LIUSHA_DATA_DIR}/proxy-certs`（原 `HOST_DATA_DIR`），改名为 `certs`。

**二进制**

- `setup` 子命令已删除，`.env` 由压缩包里的 `.env.example` 复制填写；原先 setup 生成的 `liusha.env`
  按上面的改名改写后另存为 `.env`（`mv liusha.env .env`），服务定义里的路径同步改。

**单镜像**

- 改为与多镜像相同的 `.env`（`docker run --env-file .env`），不再在容器里生成密钥。把原来
  `/data/secrets` 下的值抄进 `.env`：`llm_key.secret` → `LIUSHA_LLM_ENCRYPTION_KEY`（**必须沿用**，
  否则已保存的 LLM 密钥无法解密）、`sandbox.token` → `LIUSHA_SANDBOX_TOKEN`、`redis.password` →
  `LIUSHA_REDIS_PASSWORD`、`admin.initial_password` → `LIUSHA_ADMIN_INITIAL_PASSWORD`；
  `LIUSHA_POSTGRES_PASSWORD` 新生成一个（内置 PostgreSQL 改为凭密码连接，启动时按它设定）。确认能正常登录与调用 LLM 后可删除
  `/data/secrets`。
- 运行命令加上配置文件挂载 `--mount type=bind,src=$HOME/data/liusha/config.yaml,dst=/data/config.yaml,readonly`
  与 `--env-file .env`；监听段按 `.env` 的 `LIUSHA_SANDBOX_LISTENER_PORTS` 发布（原先 `-e` 传入的同名
  变量改写进 `.env`）；回连地址改在 `config.yaml` 的 `sandbox.callback_host`。

**多镜像**

- `.env` 改名（值不变）：

  | 原变量 | 新变量 |
  |---|---|
  | `POSTGRES_PASSWORD` / `REDIS_PASSWORD` | `LIUSHA_POSTGRES_PASSWORD` / `LIUSHA_REDIS_PASSWORD` |
  | `LIUSHA_LLM_KEY_SECRET` | `LIUSHA_LLM_ENCRYPTION_KEY` |
  | `JINA_API_KEY` | `LIUSHA_EMBEDDING_API_KEY` |
  | `HOST_DATA_DIR` | `LIUSHA_DATA_DIR` |
  | `NGINX_HOST_PORT` | `LIUSHA_CONSOLE_HOST_PORT` |
  | `PROXY_HOST_PORT` / `SANDBOX_HOST_PORT` / `POSTGRES_HOST_PORT` / `REDIS_HOST_PORT` | 各加 `LIUSHA_` 前缀 |

- `LIUSHA_SANDBOX_LISTENER_PORTS` 现为必填（取消注释，写定对外监听段）。
- `API_HOST_PORT` 删除：api 不再映射到宿主，控制台与 API 一律经 nginx（`LIUSHA_CONSOLE_HOST_PORT`）访问。
- `LIUSHA_SANDBOX_CALLBACK_HOST`、`LIUSHA_API_COOKIE_SECURE`、`LIUSHA_SANDBOX_WORKSPACE_RETENTION_DAYS`
  改到 `config.yaml`（`sandbox.callback_host`、`api.cookie_secure`、`sandbox.workspace_retention_days`）。
- `${LIUSHA_DATA_DIR}/proxy-certs` 改名为 `certs`。

## 从 0.9 升级

跨多个版本升级时按版本顺序依次执行各节；本节的命令按 0.10 当时的形态写。

0.10 把五个程序合并为 `liusha`，沙盒改为常驻服务，镜像只发布 `ghcr.io/v3teran/liusha`。

**多镜像**

1. 备份数据库：`docker exec liusha-postgres pg_dump -U liusha -Fc liusha > liusha.dump`。
2. 检出新版本；`compose.prod.yaml` 与 `--profile full` 不再使用，直接 `docker compose up -d --build`。
3. 修改 `.env`：新增 `LIUSHA_SANDBOX_TOKEN`（必填）；`LIUSHA_COOKIE_SECURE` 改名
   `LIUSHA_API_COOKIE_SECURE`；删除 `LIUSHA_INGEST_TOKEN`；`HOST_WORKSPACE_DIR` 不再使用，
   工作区固定为 `${HOST_DATA_DIR}/workspace`，原先自定义了位置的把目录移过去。
4. `config/config.yaml` 改为 `${HOST_DATA_DIR}/config.yaml`（见上一节）。新的 `config.yaml` 不预置
   GLM provider；已导入数据库的 provider 不受影响，其密钥变量（如 `GLM_API_KEY`）保留在 `.env` 即可。
5. 需要回连时在 `.env` 填 `LIUSHA_SANDBOX_CALLBACK_HOST`；对外监听段缺省改为 `21000-21299`。
6. runner 不再挂载 docker.sock，58590 不再对外发布。旧版 runner 在宿主上拉起的沙盒容器不随
   Docker Compose 删除，手动清掉（它们占着监听段端口）：`docker rm -f $(docker ps -aq --filter name=liusha-sandbox-)`。
   旧镜像 `liusha-api` / `liusha-proxy` / `liusha-runner` / `liusha-nginx` / `liusha-sandbox` 停止更新，可删除。

**单镜像**：数据库从 PostgreSQL 17 升到 18，数据目录不能直接沿用。

```bash
docker exec liusha-single su postgres -c 'pg_dump -p 5432 -Fc liusha' > liusha.dump   # 旧容器导出
docker rm -f liusha-single $(docker ps -aq --filter name=liusha-sandbox-)           # 连同旧版拉起的沙盒容器
mv ~/data/liusha/single/pg ~/data/liusha/single/pg17.bak                            # 其余数据与密钥保留
make single-build
docker run -d --name liusha --stop-timeout 60 -p 50080:8580 -p 58588:8588 -v ~/data/liusha/single:/data liusha-single:local
docker exec -i liusha su postgres -c 'pg_restore -p 5432 --clean --if-exists -d liusha' < liusha.dump
docker restart liusha                                                                 # 启动时迁移到新版本
```

单镜像不再需要 docker.sock 与宿主侧沙盒镜像，端口改为 8580（控制台与 API）、8588（代理）。
数据、管理员账号、已保存的 LLM 密钥（`/data/secrets` 下的密钥沿用）与凭证库都原样保留；确认
无误后可删除 `pg17.bak`。

**漏洞台账（所有形态）**：漏洞改为与坐实它的结果一一对应，启动时的迁移（0230）改写存量，升级前
先备份数据库（二进制部署用 `pg_dump -Fc`）。

- 同一结果的重复记录合并为一条：保留人工处置过的，否则保留最早的，其余删除。
- 旧版按「主机 + 路径 + 方法」去重，同一接口上后坐实的漏洞会被先交付的吞掉；这些漏洞在 runner
  首次启动时自动补进台账（有补交付时日志记「坐实结果补交付完成」及条数）。
- `finding.dedup_key` 列已删除，直接查库的外部脚本改用 `result_node_id`。
