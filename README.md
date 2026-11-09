# liusha

> **免责声明**：本项目仅用于安全研究与学习，严禁使用本项目从事违反所在地区法律的行为，并对自身使用行为承担全部责任，作者对任何滥用不承担任何责任。

## 部署

### 全容器

```bash
git clone https://github.com/V3teran/liusha.git && cd liusha
cp .env.example .env   # 填必填项
docker compose -f compose.yaml -f compose.prod.yaml --profile full pull
docker compose -f compose.yaml -f compose.prod.yaml --profile full up -d
```

`.env` 必填 4 项（其余选项与模型接入说明见 `.env.example` 内注释，两处不重复维护）：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`LIUSHA_LLM_KEY_SECRET`、`LIUSHA_ADMIN_INITIAL_PASSWORD`。**模型配置不是启动前提**——不填 key 也能启动，任务下发时会提示到前端「LLM 配置」页接入；公网宿主另设 `LIUSHA_INGEST_TOKEN`。

### 升级

`git pull` 后重复上面 pull/up 即可。