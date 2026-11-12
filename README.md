# liusha

> **免责声明**：本项目仅用于安全研究与学习，严禁使用本项目从事违反所在地区法律的行为，并对自身使用行为承担全部责任，作者对任何滥用不承担任何责任。

多 agent 协作的自动化 Web 安全测试平台。只对你有权测试的目标使用。

## 部署

三种方式任选其一，配置、沙盒与本地开发的细节见 [deployments/README.md](deployments/README.md)。

### 二进制

自备 PostgreSQL 17+（装好 pgvector 扩展）、Redis，渗透工具装在本机。Linux（如 Kali）：

```bash
VERSION=0.9.0  # x-release-please-version
curl -fLO https://github.com/V3teran/liusha/releases/download/v${VERSION}/liusha_${VERSION}_linux_amd64.tar.gz
tar -xzf liusha_${VERSION}_linux_amd64.tar.gz && cd liusha_${VERSION}_linux_amd64
./liusha setup -postgres-dsn 'postgres://liusha:<密码>@127.0.0.1:5432/liusha' -redis-addr 127.0.0.1:6379
./liusha serve -env-file liusha.env
```

Windows（PowerShell；另需提供 `sh` 的环境，如 Git for Windows——智能体的命令按 sh 语法执行）：

```powershell
$VERSION = "0.9.0"  # x-release-please-version
Invoke-WebRequest -OutFile liusha.zip "https://github.com/V3teran/liusha/releases/download/v$VERSION/liusha_${VERSION}_windows_amd64.zip"
Expand-Archive liusha.zip .; cd "liusha_${VERSION}_windows_amd64"
.\liusha.exe setup -postgres-dsn "postgres://liusha:<密码>@127.0.0.1:5432/liusha" -redis-addr 127.0.0.1:6379
.\liusha.exe serve -env-file liusha.env
```

`setup` 迁移数据库、生成密钥与初始管理员密码，写入 `liusha.env`。控制台在 `http://<主机>:8580`。
作为后台服务运行（systemd / Windows 服务）见 [deployments/README.md](deployments/README.md#二进制)。

### compose

```bash
git clone --branch v0.9.0 https://github.com/V3teran/liusha.git && cd liusha  # x-release-please-version
cp .env.example .env    # 填写必填项
docker compose up -d --build
```

首次会本地构建沙盒镜像（Kali + 工具），需要较长时间。控制台在 `http://<主机>:50080`。

### 单镜像

```bash
git clone --branch v0.9.0 https://github.com/V3teran/liusha.git && cd liusha  # x-release-please-version
make single-build
docker run -d --name liusha --stop-timeout 60 -p 50080:8580 -p 58588:8588 -v ~/data/liusha/single:/data liusha-single:local
docker exec liusha cat /data/secrets/admin.initial_password   # 初始管理员 admin 的密码
```

## 升级

数据库迁移随启动自动执行，只进不退；跨版本回滚前先备份数据库。

- 二进制：下载新版本替换 `liusha`（Windows 为 `liusha.exe`），重启 `liusha serve`。
- compose：`git fetch --tags && git checkout vX.Y.Z && docker compose pull && docker compose up -d --build`。
- 单镜像：检出新版本后重新 `make single-build`，删除旧容器，以同一数据目录重新运行。

从 0.9 升级到 0.10 有不兼容变更，按 [deployments/README.md](deployments/README.md#从-09-升级) 操作。版本列表见 [Releases](https://github.com/V3teran/liusha/releases)。

## 许可证

Copyright 2026 V3teran

本项目以 [Apache License 2.0](LICENSE) 授权：可以自由使用、修改、分发与商用；再分发时须保留许可证与 [NOTICE](NOTICE) 中的署名，并标明所做的修改。v0.6.2 及之前发布的版本仍按当时的 MIT 许可证授权。发布产物附带的第三方组件声明见其中的 THIRD_PARTY_NOTICES。
