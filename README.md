# liusha

## 形态

- **nginx**（容器 :80 → 宿主 30080）前端静态 + `/api` 反代
- **api**（:8090）控制面
- **proxy**（容器 :8888 → 宿主 38888）流量入口
- **runner** 工作进程（按需拉起沙盒容器 `ghcr.io/v3teran/liusha-sandbox`）
- **postgres** + **redis**

## 部署

### 用户（全容器）

前置：Docker。

```bash
git clone https://github.com/V3teran/liusha.git && cd liusha
cp .env.example .env   # 必填：POSTGRES_PASSWORD、REDIS_PASSWORD、LLM key（与 config.yaml
                       # providers: 各块的 api_key_env 一一对应，可多把分档）、
                       # LIUSHA_LLM_KEY_SECRET、LIUSHA_ADMIN_INITIAL_PASSWORD（登录初始密码）
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

数据库迁移随 `up` 自动执行（幂等）。宿主端口一律「3+容器端口」（前端 30080 / 代理 38888 / 回灌 39594 / pg 35432 / redis 36379 / api 38090 仅回环）。打开 http://localhost:30080，`admin` / `.env` 里的初始密码登录；HTTPS 反代后设 `LIUSHA_COOKIE_SECURE=true`。
