# liusha

> **免责声明**：本项目仅用于安全研究与学习。使用者须遵守所在司法辖区的法律，严禁使用本项目从事违反所在地区法律的行为，并对自身使用行为承担全部责任；作者对任何滥用不承担任何责任。

## 部署

### 全容器

```bash
git clone https://github.com/V3teran/liusha.git && cd liusha
cp .env.example .env   # 填必填项
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

必填项：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`LIUSHA_LLM_KEY_SECRET`、`LIUSHA_ADMIN_INITIAL_PASSWORD`、`GLM_API_KEY`（key 的 env 名由 `config/config.yaml` 的 `providers:` 各块 `api_key_env` 钉定，出厂仅 glm 一块、三档全路由到它，至少一把否则 api 拒启；换/加厂商 = 改 yaml 块 + `.env` 放同名 key）。公网宿主另必填 `LIUSHA_INGEST_TOKEN`（沙盒回灌 runner 的 Bearer，与沙盒自动共享）；可选项见 `.env.example`。

### 升级

`git pull` 后重复上面 pull/up 即可——迁移由 `migrate` 一次性服务随 up 自动执行。

仅当升级报「版本对不上/拒跑」时才需要校准数据库台账（把版本号对齐到本仓库当前迁移顶号，即 `db/migrations/` 下最大编号；dirty=true 表示上次迁移中断，须先修复再校准）：

```bash
# 查本仓库当前顶号：ls db/migrations/ | sort | tail -1   （如 0196_baseline.up.sql → 196）
docker exec liusha-postgres psql -U liusha -d liusha -c "UPDATE schema_migrations SET version=<顶号>, dirty=false;"
```