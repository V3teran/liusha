# liusha

## 形态

| 服务 | 容器端口 | 宿主端口 | 职责 |
|---|---|---|---|
| nginx | 80 | **30080** | 前端静态 + `/api` 反代 |
| api | 8580 | 38580（仅回环） | 控制面 |
| proxy | 8588 | **38588** | 流量入口（浏览器代理指这里） |
| runner | 8590 | **38590** | 工作进程（回灌端点；按需拉起沙盒容器 `ghcr.io/v3teran/liusha-sandbox`） |
| postgres | 5432 | **35432** | 主存储 |
| redis | 6379 | **36379** | 队列 / 缓存 / 凭证库（强制密码） |

宿主端口一律「3 + 容器端口」（不足五位补 0）。api 仅绑回环——唯一入口是 nginx。

## 部署

### 用户（全容器）

前置：Docker。

```bash
git clone https://github.com/V3teran/liusha.git && cd liusha
cp .env.example .env   # 填必填项（模板内有逐项说明）
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

必填项：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`LIUSHA_LLM_KEY_SECRET`、`LIUSHA_ADMIN_INITIAL_PASSWORD`（登录初始密码，仅首启建号生效）、LLM key（与 `config/config.yaml` 的 `providers:` 各块 `api_key_env` 一一对应，可多块多把 key，按档分工——写法见该文件注释）。

数据库迁移随 `up` 自动执行（幂等）。打开 http://localhost:30080，`admin` / 初始密码登录；HTTPS 反代后设 `LIUSHA_COOKIE_SECURE=true`。

### 维护者

```bash
make prod-build   # 本地构建六镜像（单架构=本机架构，供本机栈用）
make publish      # 构建并推送业务四镜像（amd64+arm64 双架构）——用户 pull 自动取本机
                  # 架构；需 docker login ghcr.io，改代码后须 publish
make migrate      # 手动迁移（host 直跑形态）
```
