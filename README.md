# liusha

## 形态

- **nginx**（:80）前端静态 + `/api` 反代
- **api**（:8090）控制面
- **proxy**（:8888）流量入口
- **runner** 工作进程（按需拉起沙盒容器 `ghcr.io/v3teran/liusha-sandbox`）
- **postgres** + **redis**

## 部署

### 用户（全容器）

前置：Docker。

```bash
git clone https://github.com/V3teran/liusha.git && cd liusha
cp .env.example .env   # 必填：GLM_API_KEY、LIUSHA_LLM_KEY_SECRET、POSTGRES_PASSWORD、
                       # LIUSHA_ADMIN_INITIAL_PASSWORD（登录初始密码，仅首启建号生效）
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

数据库迁移随 `up` 自动执行（幂等）。打开 http://localhost，`admin` / `.env` 里的初始密码登录；HTTPS 反代后设 `LIUSHA_COOKIE_SECURE=true`。
