# liusha

> **免责声明**：本项目仅用于安全研究与学习。使用者须遵守所在司法辖区的法律，严禁使用本项目从事违反所在地区法律的行为，并对自身使用行为承担全部责任；作者对任何滥用不承担任何责任。

## 形态

| 服务 | 容器端口 | 宿主端口 | 职责 |
|---|---|---|---|
| nginx | 80 | **50080** | 前端静态 |
| api | 8580 | **58580** | API |
| proxy | 8588 | **58588** | 流量代理|
| runner | 8590 | **58590** | 扫描主进程 |
| postgres | 5432 | **55432** | 主存储 |
| redis | 6379 | **56379** | 队列 / 缓存 |
| sandbox | 8080, 21000-21999 | 21000-21999 | 扫描工具容器，动态拉起 |

## 部署

### 全容器

```bash
git clone https://github.com/V3teran/liusha.git && cd liusha
cp .env.example .env   # 填必填项
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

必填项：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`LIUSHA_LLM_KEY_SECRET`、`LIUSHA_ADMIN_INITIAL_PASSWORD`、`GLM_API_KEY`（key 的 env 名由 `config/config.yaml` 的 `providers:` 各块 `api_key_env` 钉定，出厂仅 glm 一块、三档全路由到它，至少一把否则 api 拒启；换/加厂商 = 改 yaml 块 + `.env` 放同名 key）。公网宿主另必填 `LIUSHA_INGEST_TOKEN`（沙盒回灌 runner 的 Bearer，与沙盒自动共享）；可选项见 `.env.example`。