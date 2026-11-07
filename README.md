# liusha

> **免责声明**：本项目仅用于已获明确授权的安全测试场景。使用者须遵守所在司法辖区的法律，并对自身使用行为承担全部责任；作者对任何滥用不承担任何责任。

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

必填项：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`LIUSHA_LLM_KEY_SECRET`、`LIUSHA_ADMIN_INITIAL_PASSWORD`、LLM key（与 `config/config.yaml` 的 `providers:` 各块 `api_key_env` 一一对应，可多块多把 key，按档分工）。