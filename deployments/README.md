# 部署

liusha 是一个程序，组件是它的子命令：`api`（控制台与 API）、`proxy`（MITM 代理入口）、
`runner`（任务执行）、`sandbox`（工具执行沙盒）、`migrate`（数据库迁移），`serve` 在同一
进程运行全部组件。三种部署方式只是组件的摆放不同：

| | 二进制 | compose | 单镜像 |
|---|---|---|---|
| 组件 | `liusha serve` 一个进程 | api / proxy / runner / sandbox 各一个容器 | 一个容器内 `liusha serve` |
| PostgreSQL / Redis | 自备 | compose 内置（pg17） | 镜像内置（pg18） |
| 渗透工具 | 本机安装（Linux / macOS / Windows） | 沙盒镜像（本地构建） | 沙盒镜像（本地构建） |
| 对外入口 | :8580 | nginx :50080 | :8580（映射到宿主自定） |
| 数据库迁移 | `setup` 首装；`serve` 启动时 | 一次性的 `migrate` 服务 | `serve` 启动时 |

发布产物只有 Releases 中的二进制压缩包与 `ghcr.io/v3teran/liusha` 镜像；沙盒镜像与单镜像由
部署方本地构建。

## 配置

生效顺序：内置默认（[internal/config/config.yaml](../internal/config/config.yaml)，列出全部键
与说明）→ 覆盖文件（`-config` 或 `LIUSHA_CONFIG`，只写要改的键）→ 环境变量。键 `a.b_c`
对应环境变量 `LIUSHA_A_B_C`，例如 `sandbox.callback_host` → `LIUSHA_SANDBOX_CALLBACK_HOST`。

密钥只经环境变量注入：

| 变量 | 用途 | 需要它的组件 |
|---|---|---|
| `LIUSHA_POSTGRES_DSN` | 数据库连接串 | 除 sandbox 外全部 |
| `LIUSHA_REDIS_ADDR` / `LIUSHA_REDIS_PASSWORD` | Redis | 除 sandbox 外全部 |
| `LIUSHA_LLM_KEY_SECRET` | 加密保存在数据库里的 LLM 密钥（32 字节 hex），生成后不要更改 | api、runner |
| `LIUSHA_SANDBOX_TOKEN` | 沙盒与 runner / api 的共享密钥 | sandbox、runner、api |
| `LIUSHA_ADMIN_INITIAL_PASSWORD` | 首次启动创建 admin 的密码 | api |
| `JINA_API_KEY` | 知识库 embedding（可选） | runner |

LLM 在控制台「LLM 配置」页接入。也可以在覆盖文件里声明 `providers`，空库首次启动时导入一次
（格式见内置默认里 `llm` 段的注释）。

## 沙盒

runner 的工具命令都经沙盒服务执行（`sandbox.url` + `sandbox.token`）。沙盒按任务分目录
（`<工作区>/<批次>/<任务>/`），任务的一次运行结束时结束它遗留的进程与浏览器会话。

- **工具**：工具页标出沙盒未安装的工具，智能体的工具名单里「已允许 · 未安装」的工具运行时
  不可用，装上后自动生效。二进制部署时工具就是本机安装的那些。
- **浏览器**：`drive_browser` 需要装了 browser-use 0.13.x（1.0 起接口不兼容）的 Python
  （`sandbox.browser_python`，沙盒镜像里已配好）。不可用时工具页会说明缺什么。
- **工作区**：api（文件页、附件）与沙盒必须看到同一目录、同一绝对路径（`sandbox.workspace_dir`，
  缺省 `~/data/liusha/workspace`）。compose 以同一路径挂进两个容器。
- **回连**：反弹 shell / 代理 / payload 托管只绑对外监听段（`sandbox.listener_ports`，缺省
  `21000-21099`）中的端口；`sandbox.callback_host` 是目标用来回连的地址。容器部署探测不到宿主
  地址，需要回连时必须填写，并放行该端口段。
- 沙盒执行 agent 编写的任意命令（root 运行）：命令环境里没有 liusha 的密钥，沙盒令牌也不会
  交给命令；但命令能访问沙盒所在机器上的一切。compose 部署时沙盒是独立容器，只挂载任务工作区；
  二进制与单镜像部署时沙盒与平台同机（同容器），命令可以读到平台的数据目录与本机数据库——
  需要隔离时选 compose。沙盒端口不要对外暴露（compose 只绑回环）。

## 二进制

Release 提供 Linux、macOS（tar.gz）与 Windows（zip）的 amd64 / arm64 程序，三者功能一致。

```bash
./liusha setup -postgres-dsn 'postgres://liusha:<密码>@127.0.0.1:5432/liusha' -redis-addr 127.0.0.1:6379
./liusha serve -env-file liusha.env
```

- 数据库需已安装 pgvector（`CREATE EXTENSION vector` 由迁移执行，账号需相应权限）。
- `liusha.env` 是 `KEY=VALUE` 文件，只对当前用户开放（POSIX 0600，Windows 上是只含当前用户、
  SYSTEM、Administrators 的访问控制列表）。其他配置追加 `LIUSHA_<键>` 即可；`setup` 不覆盖已存在的文件。
- 数据缺省在 `~/data/liusha` 下：`workspace`（任务工作区）、`logs`（日志）。
- 智能体的命令按 POSIX sh 语法编写，沙盒用 `sandbox.shell` 执行，缺省自动查找（PATH 中的 `sh`；
  Windows 上再找 Git for Windows）。渗透工具装在本机并加入 PATH，工具页按命令实际看到的 PATH
  标出未安装的工具。
- 浏览器能力：装了 `browser-use==0.13.14` 的 Python（`sandbox.browser_python`，缺省 `python3`，
  Windows 为 `python`；建议用独立 venv）。浏览器内核优先用本机已装的 Chrome / Chromium / Edge
  （Windows 自带 Edge）；都没有时执行一次 `uvx playwright install chromium`（需要 uv），或在首次
  打开浏览器时由它自动拉取。
- 停止时在途任务按中断收尾（至多 `runner.asynq_shutdown_timeout_seconds`，缺省 30 秒），由下次
  启动续跑；托管方的停止等待须长于它。
- 升级：替换程序后重启，`serve` 启动时自动迁移。
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
EnvironmentFile=/opt/liusha/liusha.env
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
      <string>-env-file</string><string>/opt/liusha/liusha.env</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
    <key>ExitTimeOut</key><integer>60</integer>
  </dict></plist>
  ```

  launchd 不继承登录 shell 的 PATH：Homebrew 装的工具需在 `liusha.env` 里写
  `PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"`。

### Windows

两条路线：

- **完整工具集：WSL2。** 在 WSL2 的 Linux 发行版（如 Kali）里运行 Linux 版 `liusha`，工具、
  命令语义与 Linux 完全一致。控制台与代理端口在 Windows 上经 `localhost` 可达。
- **原生：`liusha.exe`。** 需要提供 sh 的环境：安装 Git for Windows 即可（沙盒自动找到它的
  `bin\sh.exe`，装在别处或用 MSYS2 时用 `LIUSHA_SANDBOX_SHELL` 指定）。渗透工具装 Windows 版
  （nmap、sqlmap、nuclei、ffuf 等都有）。

原生运行时可作为 Windows 服务（以管理员身份执行；服务名任取）：

```powershell
sc.exe create liusha binPath= "\"C:\liusha\liusha.exe\" serve -env-file C:\liusha\liusha.env" start= auto obj= ".\liusha-svc" password= "<密码>"
sc.exe failure liusha reset= 86400 actions= restart/5000
sc.exe start liusha
```

`liusha` 由服务控制管理器启动时按服务运行，停止与关机时中断在途任务、收尾后退出。服务账户
用专门的低权限账户；工具与 Python 须对该账户可用（装在系统范围，或装在该账户下），`liusha.env`
也要用该账户执行 `setup` 生成（只有它与管理员可读）。

## compose

```bash
cp .env.example .env    # 填写必填项
docker compose up -d --build
```

数据都在 `HOST_DATA_DIR`（缺省 `~/data/liusha`）下：postgres、redis、workspace、logs、proxy-certs。
宿主端口：控制台 50080、代理 58588；api 58580、沙盒 58591 只绑回环。改端口与其它可选项见
`.env.example`。修改其它配置项时在对应服务的 `environment` 中加 `LIUSHA_<键>`。

## 单镜像

```bash
make single-build       # 先构建沙盒镜像，再以它为底构建 liusha-single:local
docker run -d --name liusha --stop-timeout 60 -p 50080:8580 -p 58588:8588 -v ~/data/liusha/single:/data liusha-single:local
```

一个容器运行 PostgreSQL 18（pgvector）、Redis 与 `liusha serve`，任一进程退出即整体停止，交给
`--restart` 等编排重启。数据都在 `/data`；首次启动生成的密钥存于 `/data/secrets`（同名环境
变量优先），初始管理员 admin 的密码在 `/data/secrets/admin.initial_password`
（`docker exec liusha cat /data/secrets/admin.initial_password`）。`--stop-timeout 60` 留出停机
收尾时间：在途任务按中断收尾（至多 `runner.asynq_shutdown_timeout_seconds`，缺省 30 秒）后
数据库才关闭，Docker 缺省的 10 秒会在收尾中途强杀。需要回连时加
`-e LIUSHA_SANDBOX_CALLBACK_HOST=<宿主地址> -p 21000-21099:21000-21099`（`make single-run` 已含）。
目标机架构不同时构建加 `--platform`；离线分发用 `docker save liusha-single:local | gzip`。

## 本地开发

```bash
make dev-up               # 起 pg + redis + 沙盒容器并迁移（首次构建沙盒镜像较久）
./scripts/dev/run-svc.sh  # 宿主上运行 proxy + api + runner（日志 logs/*.log）
make run-web              # 前端 dev server :5173，/api 代理到 :8580
make dev-down             # 收工（-v 清数据）
```

宿主上的 runner 经 `127.0.0.1:58591` 调用容器里的沙盒，沙盒的浏览器流量回灌到
`host.docker.internal:8590`。

## 从 0.9 升级

0.10 把五个程序合并为 `liusha`，沙盒改为常驻服务，镜像只发布 `ghcr.io/v3teran/liusha`。

**compose**

1. 备份数据库：`docker exec liusha-postgres pg_dump -U liusha -Fc liusha > liusha.dump`。
2. 检出新版本；`compose.prod.yaml` 与 `--profile full` 不再使用，直接 `docker compose up -d --build`。
3. 修改 `.env`：新增 `LIUSHA_SANDBOX_TOKEN`（必填）；`LIUSHA_COOKIE_SECURE` 改名
   `LIUSHA_API_COOKIE_SECURE`；删除 `LIUSHA_INGEST_TOKEN`；`HOST_WORKSPACE_DIR` 不再使用，
   工作区固定为 `${HOST_DATA_DIR}/workspace`，原先自定义了位置的把目录移过去。
4. `config/config.yaml` 已移除：改过的键写进覆盖文件（挂进容器并设 `LIUSHA_CONFIG`），或改用
   `LIUSHA_<键>` 环境变量。内置默认不再预置 GLM provider；已导入数据库的 provider 不受影响，
   其密钥变量（如 `GLM_API_KEY`）保留在 `.env` 即可。
5. 需要回连时在 `.env` 填 `LIUSHA_SANDBOX_CALLBACK_HOST`；对外监听段缺省改为 `21000-21099`。
6. runner 不再挂载 docker.sock，58590 不再对外发布。旧版 runner 在宿主上拉起的沙盒容器不随
   compose 删除，手动清掉（它们占着监听段端口）：`docker rm -f $(docker ps -aq --filter name=liusha-sandbox-)`。
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
