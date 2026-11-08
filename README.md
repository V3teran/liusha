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

必填 4 项：`POSTGRES_PASSWORD`、`REDIS_PASSWORD`、`LIUSHA_LLM_KEY_SECRET`（provider 密钥的加解密密钥）、`LIUSHA_ADMIN_INITIAL_PASSWORD`（首次启动建 admin 用，仅首次生效）。

**模型配置不是启动前提**：出厂 `config/config.yaml` 种子了 glm 一块部署（key 读 `.env` 的 `GLM_API_KEY`，三档推理全路由到它）——填了即可用；**不填也能启动**，任务下发会被拒绝并提示到前端「LLM 配置」页接入。换/加厂商两条路：

- 前端（推荐）：「LLM 配置」页新建 provider（填 base_url / 模型 / 密钥，协议选 OpenAI 兼容或 Anthropic），再在能力路由里为三档绑定；
- 部署期：改 `config.yaml` 的 `providers:` 块 + `.env` 放同名 key（`api_key_env` 钉定变量名），首次启动时自动导入。

公网宿主另必填 `LIUSHA_INGEST_TOKEN`（沙盒回灌 runner 的 Bearer，与沙盒自动共享）；其余可选项见 `.env.example`。

### 升级

`git pull` 后重复上面 pull/up 即可。