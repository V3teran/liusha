# liusha

> **免责声明**：本项目仅用于安全研究与学习，严禁使用本项目从事违反所在地区法律的行为，并对自身使用行为承担全部责任，作者对任何滥用不承担任何责任。

## 部署

三种方式任选其一：二进制、单镜像、多镜像。都由两份文件驱动——`config.yaml`（全部配置，
发布压缩包与仓库里各有一份模板）与 `.env`（密钥，由 `.env.example` 复制填写，三种方式共用一份模板）。
配置、沙盒与本地开发的细节见 [deployments/README.md](deployments/README.md)。

### 二进制

自备 PostgreSQL 17+（装好 pgvector 扩展）、Redis，渗透工具装在本机。Linux（如 Kali）：

```bash
VERSION=0.11.0  # x-release-please-version
curl -fLO https://github.com/V3teran/liusha/releases/download/v${VERSION}/liusha_${VERSION}_linux_amd64.tar.gz
tar -xzf liusha_${VERSION}_linux_amd64.tar.gz && cd liusha_${VERSION}_linux_amd64
cp .env.example .env && chmod 600 .env   # 填写密钥，取消注释并填写 LIUSHA_POSTGRES_URL、LIUSHA_REDIS_URL
./liusha serve -env-file .env            # 启动时自动迁移数据库；其余配置在同目录的 config.yaml
```

macOS 用 `liusha_${VERSION}_darwin_arm64.tar.gz`（Intel 机型为 `darwin_amd64`），步骤相同。

Windows（PowerShell；智能体的命令按 sh 语法执行，需安装 Git for Windows——沙盒自动找到它的 sh；
要完整工具集可在 WSL2 的 Kali 里用 Linux 版）：

```powershell
$VERSION = "0.11.0"  # x-release-please-version
Invoke-WebRequest -OutFile liusha.zip "https://github.com/V3teran/liusha/releases/download/v$VERSION/liusha_${VERSION}_windows_amd64.zip"
Expand-Archive liusha.zip .; cd "liusha_${VERSION}_windows_amd64"
Copy-Item .env.example .env   # 填写密钥，取消注释并填写 LIUSHA_POSTGRES_URL、LIUSHA_REDIS_URL
.\liusha.exe serve -env-file .env
```

控制台在 `http://<主机>:8580`，用 `.env` 里的 `LIUSHA_ADMIN_INITIAL_PASSWORD` 以 admin 登录。
作为后台服务运行（systemd / launchd / Windows 服务）见 [deployments/README.md](deployments/README.md#二进制)。

### 单镜像

```bash
git clone --branch v0.11.0 https://github.com/V3teran/liusha.git && cd liusha  # x-release-please-version
make single-build
cp .env.example .env    # 填写密钥与容器部署必填项
mkdir -p ~/data/liusha && cp internal/config/config.yaml ~/data/liusha/   # 配置文件，按需修改
make single-run
```

首次会本地构建沙盒镜像（Kali + 工具），需要较长时间。控制台在 `http://<主机>:50080`。

### 多镜像

```bash
git clone --branch v0.11.0 https://github.com/V3teran/liusha.git && cd liusha  # x-release-please-version
cp .env.example .env    # 填写密钥与容器部署必填项（与单镜像同一组）
mkdir -p ~/data/liusha && cp internal/config/config.yaml ~/data/liusha/   # 配置文件，按需修改
docker compose up -d --build
```

首次会本地构建沙盒镜像（Kali + 工具），需要较长时间。控制台在 `http://<主机>:50080`。

## 升级

数据库迁移随启动自动执行，只进不退；跨版本回滚前先备份数据库。升级后对照新版本的 `config.yaml` 与
`.env.example` 补齐新增的项。

- 二进制：下载新版本替换 `liusha`（Windows 为 `liusha.exe`），重启 `liusha serve`。
- 单镜像：检出新版本后重新 `make single-build`，删除旧容器，以同一数据目录重新运行。
- 多镜像：`git fetch --tags && git checkout vX.Y.Z && docker compose pull && docker compose up -d --build`。

不兼容变更的升级步骤见 [deployments/README.md](deployments/README.md)（本版本：「配置文件与 .env 统一」；
0.9 升级到 0.10：「从 0.9 升级」）。版本列表见 [Releases](https://github.com/V3teran/liusha/releases)。

## 许可证

Copyright 2026 V3teran

本项目以 [Apache License 2.0](LICENSE) 授权：可以自由使用、修改、分发与商用；再分发时须附带许可证，并标明所做的修改。v0.6.2 及之前发布的版本仍按当时的 MIT 许可证授权。发布产物附带的第三方组件声明见其中的 THIRD_PARTY_NOTICES。
