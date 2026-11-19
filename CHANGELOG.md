# Changelog

## [0.13.2](https://github.com/V3teran/liusha/compare/v0.13.1...v0.13.2) (2026-11-19)


### 新功能

* **task:** CTF 场景改用简短名称「CTF」 ([10d7039](https://github.com/V3teran/liusha/commit/10d7039c76ed187fdf59233c64be21cdc1444e9a))


### 问题修复

* **exploration:** LLM 限流等瞬时故障不再误判探索动作失败 ([1dea72b](https://github.com/V3teran/liusha/commit/1dea72b2384430feb2bd0fd72a3f44567c7226c5))

## [0.13.1](https://github.com/V3teran/liusha/compare/v0.13.0...v0.13.1) (2026-11-19)


### 新功能

* **sandbox:** 沙盒预装 ncat 与 xxd ([dc7affd](https://github.com/V3teran/liusha/commit/dc7affddc848c91ec6e49de1de3ff98c68a40c11))
* **task:** 下发任务可选择场景，领域指引随场景提供 ([77c03c2](https://github.com/V3teran/liusha/commit/77c03c2fb73114e94b402d8b729682f2e37fb059))
* **tsecbench:** 跑分适配器入库，建任务走 CTF 场景 ([a9db0d7](https://github.com/V3teran/liusha/commit/a9db0d7af61170d071ead12f466398dc66cde29e))


### 问题修复

* **sandbox:** 沙盒中的 nmap 恢复可用 ([d7e9fdd](https://github.com/V3teran/liusha/commit/d7e9fdd0613e7976d6864b31554a5e2dfdb90878))

## [0.13.0](https://github.com/V3teran/liusha/compare/v0.12.0...v0.13.0) (2026-11-17)


### ⚠ 破坏性变更

* 漏洞 API 返回的 repro.domain 改为 repro.kind（取值 web→http、generic→steps），evaluation 证据中 exploit_status_code / exploit_duration_ms 改为 status_code / duration_ms，探索图动作节点不再含 type；存量数据由迁移自动改写，直接读取这些字段的外部调用方需改用新字段名

### 问题修复

* **exploration:** 探索图中的事实不再带优先级等假设字段 ([0081090](https://github.com/V3teran/liusha/commit/0081090ed42f52616eb678571919bcc3809ef616))


### 重构

* 探索引擎核心层与渗透测试领域解耦 ([9bb172f](https://github.com/V3teran/liusha/commit/9bb172f44ff1383a9542e9fbf08a0254f520f6e6))

## [0.12.0](https://github.com/V3teran/liusha/compare/v0.11.0...v0.12.0) (2026-11-17)


### ⚠ 破坏性变更

* **proxy:** `config.yaml` 新增 `proxy.public_addr`，缺这个键会拒绝启动，从新版本的模板复制这一行（保持空值即可），详见 deployments/README.md「从 0.11 升级」。
* **proxy:** 升级后首次启动 proxy 会把旧的「Proxify CA」换成永久有效的「liusha CA」，已信任旧证书的浏览器与设备需要重新安装 `<data_dir>/certs/cacert.pem`。

### 新功能

* **proxy:** 控制台显示代理地址与运行状态，可下载根证书 ([c57329e](https://github.com/V3teran/liusha/commit/c57329e4d8c022564dc7c90fb933bf024d7e076d))


### 问题修复

* **proxy:** 根证书永久有效，到期不再导致代理无法启动 ([665abad](https://github.com/V3teran/liusha/commit/665abada1f4b2b35c70dd651933645e29cbb24df))
* **proxy:** 目标恢复后代理立即可用，不再一直返回 502 ([3c1473f](https://github.com/V3teran/liusha/commit/3c1473fd00f6b50a3eaa9e4e0bb06fe652a7d5a7))

## [0.11.0](https://github.com/V3teran/liusha/compare/v0.10.2...v0.11.0) (2026-11-16)


### ⚠ 破坏性变更

* **config:** 必须提供完整的 `config.yaml`（缺键、未知键拒绝启动），原先 `LIUSHA_<键>` 或覆盖文件里的改动改写进这份文件，详见 `deployments/README.md`「配置文件与 .env 统一」。
* **config:** 配置键改名或删除（`postgres.dsn` → `postgres.url`，`redis.addr` + `redis.password` → `redis.url`，`runner.healthz_addr` → `runner.listen_addr`，`runner.asynq_concurrency` → `runner.concurrency`，`sandbox.ingest_url` → `runner.url`，`proxy.max_*_body_size` → `proxy.max_*_body_bytes` 等），按升级文档逐项改写。
* **config:** 环境变量改名：`LIUSHA_LLM_KEY_SECRET` → `LIUSHA_LLM_ENCRYPTION_KEY`（值必须沿用），`JINA_API_KEY` → `LIUSHA_EMBEDDING_API_KEY`，多镜像 `.env` 的 `POSTGRES_PASSWORD` / `REDIS_PASSWORD` / `HOST_DATA_DIR` / `*_HOST_PORT` 加 `LIUSHA_` 前缀，删除 `API_HOST_PORT`、`LIUSHA_ENV`、`LIUSHA_INSTANCE`。
* **config:** 删除 `setup` 子命令与 LLM 种子，原先从环境变量取密钥的 provider 需在「LLM 配置」页重新填写密钥。
* **config:** 单镜像改用 `--env-file .env` 并挂载 `config.yaml`，原 `/data/secrets` 下的密钥抄进 `.env`（`llm_key.secret` 必须沿用）。
* **config:** MITM 根证书移到 `<data_dir>/certs`（二进制原在 `~/.liusha`，多镜像原为 `proxy-certs`），移过去可沿用已信任的 CA。

### 新功能

* **config:** 配置文件与 .env 统一，三种部署同一套用法 ([7b9091e](https://github.com/V3teran/liusha/commit/7b9091ecae313516d50d9f290a3b92ec8da9e8c5))


### 问题修复

* **ui:** 各页空列表的提示统一，窄屏下也能看到 ([dfcc7d3](https://github.com/V3teran/liusha/commit/dfcc7d30b0d2c3d35146fc0583d74d99c9611769))
* **ui:** 日志页的进程选项只列出有日志的进程 ([547c46e](https://github.com/V3teran/liusha/commit/547c46ef45dac58dd43ecbc7db3094196a7e0917))

## [0.10.2](https://github.com/V3teran/liusha/compare/v0.10.1...v0.10.2) (2026-11-16)


### 新功能

* **deploy:** 对外监听段默认扩到 300 个端口 ([ce62672](https://github.com/V3teran/liusha/commit/ce6267272372135b21791a814664eff96dc2933e))

## [0.10.1](https://github.com/V3teran/liusha/compare/v0.10.0...v0.10.1) (2026-11-16)


### 问题修复

* **conversation:** 下发的测试请求不再偶尔被当成聊天而不建任务 ([1304776](https://github.com/V3teran/liusha/commit/130477632e361f33401934f07e5dc85d092d4b7f))
* **deploy:** 中央仓库不可达时 ysomap 改从镜像取依赖，沙盒构建不再失败 ([5a3da62](https://github.com/V3teran/liusha/commit/5a3da6228356321c1647e481cc6e88690c2e5733))
* **deploy:** 代理按系统 DNS 解析目标，内网与容器内的目标也能抓到流量 ([ad3caa6](https://github.com/V3teran/liusha/commit/ad3caa641b8b578142407edaa6a0b177e7948420))
* **deploy:** 本地构建的镜像带上正确的版本号 ([df5acc9](https://github.com/V3teran/liusha/commit/df5acc949b5f41494745df2b4689532f2c82c99a))
* **deploy:** 经代理的 POST 请求体不再被并发读走 ([a3a9ff1](https://github.com/V3teran/liusha/commit/a3a9ff11aa945ffe4c89ae875dd5a37103e79e35))
* **deploy:** 软件源偶发 502 时构建自动重试，不再整体失败 ([dd28610](https://github.com/V3teran/liusha/commit/dd28610d26602508b19eef5a7363f87d1726c298))
* **sandbox:** Windows 上浏览器请求被拒时如实给出原因 ([4490fb9](https://github.com/V3teran/liusha/commit/4490fb9ced187697974bb7b8deb6af7ecd5ff77e))
* **sandbox:** ysomap 与 RsaCtfTool 能真正运行，构建时校验所有工具入口 ([8631ca4](https://github.com/V3teran/liusha/commit/8631ca4f4a85d3920762f4eee48543aec9a82f48))
* **task:** 续跑与代理捕获的任务不再跑满 30 分钟就被终止 ([da5d8f6](https://github.com/V3teran/liusha/commit/da5d8f6dd7f1af86746804a9ac1a36b88e23f690))

## [0.10.0](https://github.com/V3teran/liusha/compare/v0.9.0...v0.10.0) (2026-11-15)


### ⚠ 破坏性变更

* **finding:** 迁移 0230 会合并同一坐实结果的重复漏洞记录（保留人工处置过的或最早的一条，其余删除），并删除 finding.dedup_key 列；依赖该列的外部查询需改用 result_node_id。
* **httpapi:** GET /traffic/:id 响应 consumed_by[] 的 conv_id 字段改名为 conversation_id，并删除恒为空的 _id 字段。
* **task:** 删除 GET /conversations/:id/usage。状态与暂停改取 GET /tasks/:id（对话所属任务见 GET /conversations/:id 的 task_id），用量改取 GET /tasks/:id/usage；响应字段 duration_ms 更名 elapsed_ms，不再返回 tokens.total、work_ms、running、paused、status、conversation_id、task_id。
* **exploration:** 删除 GET /conversations/:id/exploration/graph 与 GET /conversations/:id/exploration/verifications，改用 GET /tasks/:id/exploration/graph 与 GET /tasks/:id/exploration/verifications（对话的任务 ID 见 GET /conversations/:id 的 task_id）；探索图页链接参数由 ?conversation_id= 改为 ?task=。
* **task:** 任务的 API 字段 `brief` 改名 `goal`（`GET /api/tasks`、`GET /api/tasks/:id`），按字段名读取任务目标的调用方需同步改名；数据库列 task.brief 改名 task.goal、agent_run.input 删除（迁移自动执行），直接查询这两列的外部脚本需同步修改。
* **exploration:** 表 task_control_event 已删除（迁移自动执行）；直接向该表写入事件的外部脚本需改用 `POST /api/tasks/:id/control`（暂停 / 继续 / 停止）。
* **exploration:** 验证审计链接口字段 primitives 改名 repro、evaluation 改名 evidence； exploration_verification 表列 primitives 改名 repro（迁移 0222）。直接读该表或接口的 外部调用方需同步改名。
* **exploration:** 探索图节点字段 blocked_reason 改名 state_reason（迁移 0221 改写存量）。
* **exploration:** 探索图结果节点内容的 summary 改名 statement（迁移 0220 改写存量）。
* **exploration:** 探索图节点不再返回 source_id 字段（迁移 0219 删除存量）。
* **exploration:** 规划输出契约的动作字段 reason 改名 reasoning、删除 metadata；探索图 动作节点内容不再包含 complexity / priority / triggered_by / metadata / origin，规划理由 字段改名 reasoning（迁移 0217 改写存量）。
* **exploration:** 探索图观察节点的内容不再包含 execution_trace（迁移 0215 删除存量）； 事件总线删除 attempt.generated 事件。读取上述字段或订阅该事件的外部调用方需调整。
* **exploration:** 配置项 runner.max_steps_per_task 改名为 runner.max_actions_per_task（环境变量 LIUSHA_RUNNER_MAX_ACTIONS_PER_TASK），含义为 单任务最多执行的动作数；部署覆盖文件或环境变量里用了旧名的需改名。停止原因取值 max_steps_reached 改为 action_budget_reached；时间线终态节点与 agent_run.result 的 报告字段改为 actions / results / hypotheses / stop_reason（迁移 0213 改写存量）。
* **exploration:** 探索图节点不再返回 depends_on 字段，规划输出契约删除 depends_on；迁移 0209 自动清理存量数据，读取该字段的外部调用方需删除相关逻辑。
* **exploration:** 探索图节点不再返回 confidence，改为假设节点上的 verification_status（pending / confirmed / refuted）；观察类别 issue 删除； refute_reason 取值 adjudication_failed 改为 judge_unavailable。升级时迁移 0208 自动改写存量数据，读取上述字段的外部调用方需同步改字段名与取值。
* **deploy:** 镜像 liusha-api / liusha-proxy / liusha-runner / liusha-nginx / liusha-sandbox 停止更新（已发布的保留），改用 ghcr.io/v3teran/liusha；沙盒镜像由部署方本地构建（deployments/sandbox/Dockerfile）。
* **deploy:** compose 改为 docker compose up -d --build（不再叠加 compose.prod.yaml 与 --profile full，沙盒镜像本地构建）；.env 新增必填 LIUSHA_SANDBOX_TOKEN，HOST_WORKSPACE_DIR 不再使用（工作区固定为 ${HOST_DATA_DIR}/workspace）；单镜像改用 PostgreSQL 18，须先用旧镜像导出数据再导入；对外监听段缺省改为 21000-21099。升级步骤见 deployments/README.md「从 0.9 升级」。
* **sandbox:** runner 不再拉起沙盒容器，须以 liusha sandbox 单独运行沙盒服务（compose 中为 sandbox 服务，无需再挂 docker.sock）；sandbox / runner 必须配置 LIUSHA_SANDBOX_TOKEN（api 也应配置，用于查询工具安装状态），并按部署设置 LIUSHA_SANDBOX_URL / LIUSHA_SANDBOX_INGEST_URL；删除配置项 sandbox.default_image 与环境变量 LIUSHA_INGEST_TOKEN、LIUSHA_INGEST_PUBLIC_PORT、SANDBOX_SMOKE。
* **deploy:** liusha-nginx 镜像停止发布；compose 部署改用官方 nginx 镜像挂载 deployments/nginx/nginx.conf（新版 compose 已包含），控制台由 api 直接提供。
* **deploy:** cmd/api、cmd/proxy、cmd/runner、cmd/sandbox-server、cmd/corpus-import 合并为 liusha 子命令；config/config.yaml 移除，改用内置默认 + -config 覆盖文件（只写要改的键）；LIUSHA_API_ADDR 改为 LIUSHA_API_LISTEN_ADDR，LIUSHA_COOKIE_SECURE 改为 LIUSHA_API_COOKIE_SECURE，LIUSHA_INGEST_TOKEN 改为 LIUSHA_SANDBOX_TOKEN，删除 LIUSHA_TOOLS_MANIFEST_PATH 与 LIUSHA_LOG_PROCESS；内置默认不再预置 LLM provider，需在前端「LLM 配置」页接入或在覆盖文件中声明 providers。

### Features

* **conversation:** 对话里发起的 LLM 调用记下所在对话 ([a79fbd8](https://github.com/V3teran/liusha/commit/a79fbd889283c469b256b7ff8b3a3881c6daae49))
* **deploy:** liusha 可作为 Windows 服务运行，密钥文件只对当前用户开放 ([584377b](https://github.com/V3teran/liusha/commit/584377bbbc0eb721f4e2309eeceb02f9e631736f))
* **deploy:** 三种部署形态——二进制、compose、单镜像 ([b804458](https://github.com/V3teran/liusha/commit/b80445800bed6f4f08d5c20a807d627da5d52b65))
* **deploy:** 发布 macOS 二进制（amd64 / arm64） ([67a5bc0](https://github.com/V3teran/liusha/commit/67a5bc0c38bbbeca647e06845a8ae31090823a66))
* **deploy:** 发布 Windows 二进制（amd64 / arm64） ([937e7a5](https://github.com/V3teran/liusha/commit/937e7a588e3ab9cb515dbbbc8206df37f81d44ce))
* **deploy:** 发版产出 liusha 镜像与二进制压缩包 ([2e5e5e4](https://github.com/V3teran/liusha/commit/2e5e5e496f871970279ebd7ad21471ecb5fb77b9))
* **deploy:** 控制台前端编进 liusha，由 api 直接提供 ([690175b](https://github.com/V3teran/liusha/commit/690175b3f411557d38e32413be5a522e1bcec5b1))
* **deploy:** 统一为一个程序 liusha——各组件改为子命令 ([d1be2a7](https://github.com/V3teran/liusha/commit/d1be2a7ca6eeb53d8d8cd0e2aec420a4ec48f272))
* **exploration:** 运行中引导——调整目标、指派动作、否决动作 ([fe7b385](https://github.com/V3teran/liusha/commit/fe7b3855a5981201c2c64b94ed3132bb6ed389e3))
* **finding:** 新增按 ID 取单条漏洞的接口 ([8f3b2b5](https://github.com/V3teran/liusha/commit/8f3b2b5af2fa27b4b9f9859a5caa11b9d3152eeb))
* **finding:** 漏洞台账可按对话限定范围 ([e9b22a4](https://github.com/V3teran/liusha/commit/e9b22a43fb02d4a82ff185303686ecfc706a7ba3))
* **httpapi:** 流量详情的消费任务带上来源与漏洞数 ([3420bf1](https://github.com/V3teran/liusha/commit/3420bf1bab251bb88292866a1bfdc7d68b2272b8))
* **sandbox:** macOS 上按任务标识识别进程，沙盒重启后照样认领 ([6b9b16a](https://github.com/V3teran/liusha/commit/6b9b16a6521d3ccfef19806f1bd5e1ae722227c6))
* **sandbox:** 执行命令的 shell 缺省自动查找，Windows 上找到 Git for Windows ([440e56f](https://github.com/V3teran/liusha/commit/440e56f59ee73fb9a46722f119307d73edee87e6))
* **sandbox:** 沙盒在 Windows 上原生运行，浏览器服务改由沙盒启动 ([ccf9956](https://github.com/V3teran/liusha/commit/ccf9956ac4a1958e02771fd62f9c3c64afed86dd))
* **sandbox:** 沙盒执行与浏览器通道改用全平台同一机制 ([cc45a81](https://github.com/V3teran/liusha/commit/cc45a81964e1b0d081ac221965013669993c9e2d))
* **sandbox:** 沙盒改为常驻服务，runner 不再经宿主 docker 拉起容器 ([7ca87e1](https://github.com/V3teran/liusha/commit/7ca87e194ae468d416129cd86fc8af5a4df7d390))
* **task:** 任务列表可按任务、对话限定范围，批次序号按整个批次计 ([b4ce381](https://github.com/V3teran/liusha/commit/b4ce3817a1a3eea7a4d7002701004cd23c667e82))
* **ui:** 任务详情可调整目标、指派动作，探索图可否决动作，时间线显示引导回执 ([aaeaf50](https://github.com/V3teran/liusha/commit/aaeaf50c977bb50e0e7d7bb325a6aecb44f544a5))
* **ui:** 实体跳转只看被点击元素对应的内容，跳转规则只定义一处 ([df1bfff](https://github.com/V3teran/liusha/commit/df1bfff8c912d2041ac91a4f1351e615a99f1f17))
* **ui:** 漏洞跳到探索图时定位到坐实它的结果节点 ([be31c4c](https://github.com/V3teran/liusha/commit/be31c4c96b61530b066e09d593fc1e5325f628db))


### Bug Fixes

* **conversation:** 一个任务至多一个对话由唯一索引保证 ([c15b0ca](https://github.com/V3teran/liusha/commit/c15b0ca16f7db938e5690e18b1a904de63acba5b))
* **conversation:** 对话列表的计数跟随当前展示的数据，不再把旧总数标成「匹配 N 条」 ([3f6876a](https://github.com/V3teran/liusha/commit/3f6876a60ff487df282e8aceca7a8eea7ba557e0))
* **conversation:** 对话里要执行的消息追加到任务目标，续接已结束任务的指令不再丢失 ([c8c7135](https://github.com/V3teran/liusha/commit/c8c7135cb8e676ae6f7a41418a348ee15b214e0a))
* **deploy:** -env-file 里的日志配置对全部 logger 生效 ([7720a92](https://github.com/V3teran/liusha/commit/7720a925cb3e47c114afa85fb167f8e7a276c219))
* **deploy:** 单镜像不再把初始管理员密码打进日志 ([46ea2c4](https://github.com/V3teran/liusha/commit/46ea2c4e7506887b1394b1f4d0af4106ce63bf2d))
* **deploy:** 单镜像编入真实控制台，全新数据卷首次启动不再失败 ([52045cf](https://github.com/V3teran/liusha/commit/52045cf9a8ad1223f5c44d013477cb508c3eb7e0))
* **deploy:** 控制台日志只在终端里着色 ([06c366d](https://github.com/V3teran/liusha/commit/06c366da005eb2a451e2abd81dbc6fb2e3a42f23))
* **deploy:** 日志缺省写到数据根，不依赖进程工作目录 ([0273a22](https://github.com/V3teran/liusha/commit/0273a22b868e6d3323f6a8b9b5e190ee922bf672))
* **deploy:** 环境变量文件只写各方解析一致的值 ([e3f9fac](https://github.com/V3teran/liusha/commit/e3f9fac61caa52053317243c36585aa3901c7f97))
* **exploration:** 任务图跟随任务状态，所有终态路径都收口开放工作 ([f11bec1](https://github.com/V3teran/liusha/commit/f11bec152d42fcf5df596800c630a9ee20708ea4))
* **exploration:** 优先级只在节点层存一份，严重级结果不再显示成 P2 ([1bad1e1](https://github.com/V3teran/liusha/commit/1bad1e10b7786e937a350fd57a77e22e7f9d7013))
* **exploration:** 删除动作间的执行依赖，被依赖的动作失败不再卡死任务 ([3330f6c](https://github.com/V3teran/liusha/commit/3330f6c381f8fb191d3b389a50806b63bab4245d))
* **exploration:** 动作停摆由执行者看门狗按单个动作判定并真正取消执行 ([9324507](https://github.com/V3teran/liusha/commit/9324507339bcca9b16280357f823534b936c506d))
* **exploration:** 动作内容只留动作本身，规划者看到的失败原因不再是规划理由 ([e6a20a1](https://github.com/V3teran/liusha/commit/e6a20a1edc0e408aea770b3958eececf6c570ec5))
* **exploration:** 动作的「中止」拆为已中止与已否决，被打断的工作不再被当成禁区 ([c2e4e07](https://github.com/V3teran/liusha/commit/c2e4e07d17a72e908a5ed59c642405c79d257087))
* **exploration:** 动作预算只保留一个、从图上计数，报告统计不再依赖可丢事件 ([931aedf](https://github.com/V3teran/liusha/commit/931aedf17f5431c919284aa944c77b752d3960fa))
* **exploration:** 执行者按优先级持续认领动作，不再被一个慢动作挡住整批 ([6fa6f4d](https://github.com/V3teran/liusha/commit/6fa6f4dc92bc13b11bd00437165fd2b4fb47d62c))
* **exploration:** 收敛与重规划改为进程内直接调用，不再因事件丢失挂到超时 ([3b0648c](https://github.com/V3teran/liusha/commit/3b0648c7a1dda41f5eab4b2b2be3d9ad46e2ff2b))
* **exploration:** 每个任务只有一个根目标，执行者每次从图上读当前目标 ([a996e36](https://github.com/V3teran/liusha/commit/a996e36a8f4de23939ae53e8572fabbabb77010b))
* **exploration:** 结果的陈述与假设同名，规划者终于能看到坐实的发现 ([1afcbe2](https://github.com/V3teran/liusha/commit/1afcbe2a1a71b7bbbf5aa69792f5133377900f0e))
* **exploration:** 被打断的动作交还 open 重做，不再记成失败 ([bd375e3](https://github.com/V3teran/liusha/commit/bd375e33797fc1c2493e204958170c53b975e8e2))
* **exploration:** 规划结论以所依据的目标为前置，规划期间目标被改写即整批作废 ([18cba9a](https://github.com/V3teran/liusha/commit/18cba9ad576cc4304acdc66c31be953aa88e559d))
* **exploration:** 解析不出配方的假设直接出圈，不再永远待验证 ([1d2c585](https://github.com/V3teran/liusha/commit/1d2c585954f6d4cc7519f527e9e4d72b6826ddba))
* **exploration:** 重规划请求的理由交给规划者，多个来源的理由不再互相覆盖 ([7352061](https://github.com/V3teran/liusha/commit/73520612f18f1b5dabe213fc9e7dd694d4ac4d26))
* **finding:** 一个坐实结果一条漏洞，同接口上的高危主张不再被低危主张吞掉 ([3ce7581](https://github.com/V3teran/liusha/commit/3ce7581843ea34c282099b01c55b1f1050a33bb0))
* **finding:** 任务下拉的目标摘要放宽到 40 字，保留有区分度的部分 ([414f48c](https://github.com/V3teran/liusha/commit/414f48cb9623ab6b33af3601af0e850e583f76ba))
* **finding:** 任务下拉的选项带创建时间，目标相同的任务可以区分 ([ac55e3b](https://github.com/V3teran/liusha/commit/ac55e3b3dd5458b8a9554cfe9f55788328cac32e))
* **finding:** 任务筛选并入漏洞页筛选栏，选项是有漏洞的任务 ([cfae2c4](https://github.com/V3teran/liusha/commit/cfae2c41150c37b5171f1e9bf80ee0d7383ef8fb))
* **finding:** 任务详情与对话的「漏洞」跳到这个任务的漏洞 ([5fe6c8d](https://github.com/V3teran/liusha/commit/5fe6c8dd9aee49209f66c35bcf7c107bdf31ba67))
* **finding:** 补交付坐实了却没进台账的结果——runner 启动时按结果节点补齐 ([901349e](https://github.com/V3teran/liusha/commit/901349e71f26b129c2c6fd607f8817fbfe5d332e))
* **httpapi:** 尚无日志文件的进程不再让日志页崩溃 ([0e1bccb](https://github.com/V3teran/liusha/commit/0e1bccbf425f3f542b454aedcbfb63ffa1333f4e))
* **httpapi:** 流量详情的消费任务连同对话 ID 一次查出 ([b27f089](https://github.com/V3teran/liusha/commit/b27f0896323d83ceb5a7c3ad8641c1c2dd5a96a5))
* **logx:** zerolog 字段格式在包初始化时设定，消除并发写日志的竞争 ([6e8efe4](https://github.com/V3teran/liusha/commit/6e8efe48d58fc90d2606609bd172465b1d37283b))
* **runner:** 停机时中断在途 run 并等其收尾，任务由恢复监控续跑 ([25fba7c](https://github.com/V3teran/liusha/commit/25fba7c746663ea8c1d334013f89cd19d0e3db3b))
* **runner:** 沙盒持续不可达时任务不再无限重投 ([69aa75d](https://github.com/V3teran/liusha/commit/69aa75db19aa86124788779ebec6a43f4cbb9611))
* **runner:** 被停止的 run 收尾不再写任务行，停止后紧接着续接的任务不会被再终止一次 ([b3b9fd4](https://github.com/V3teran/liusha/commit/b3b9fd48315c3f3afed95f15b52db8b7e90b9a81))
* **sandbox:** 任务释放只按核验过的任务归属结束进程 ([99ec3cf](https://github.com/V3teran/liusha/commit/99ec3cf90bbde5e86d98aebb2f5d16f364db42c0))
* **sandbox:** 命令环境清掉库里声明的 LLM 密钥变量，运行目录按实例区分 ([9b6a9d4](https://github.com/V3teran/liusha/commit/9b6a9d44795fa57d8da5371b1e425d382da26b7c))
* **sandbox:** 沙盒令牌不再交给 agent 命令——浏览器流量经本地套接字回灌 ([dadcfd3](https://github.com/V3teran/liusha/commit/dadcfd32472cf67718c6860134f6845d8c1cd771))
* **sandbox:** 浏览器不加载扩展，冷启动不再因 30 秒时限失败 ([0131455](https://github.com/V3teran/liusha/commit/0131455edf8eedcb87616e387ca8f68676c6cb19))
* **sandbox:** 浏览器回灌流量按 run 归属，不再被全部丢弃 ([074f82f](https://github.com/V3teran/liusha/commit/074f82f35905245496fc5574d9a1d278a41fd370))
* **sandbox:** 浏览器能力如实探测，任务释放不再遗留 chromium ([56a1a36](https://github.com/V3teran/liusha/commit/56a1a36dc68d5186689aa0cb77237ca13fcf8767))
* **sandbox:** 浏览器自愈只作用于出问题的身份 ([ed6a1a2](https://github.com/V3teran/liusha/commit/ed6a1a2aa34b245a4738c512c6c6eda962abbc41))
* **sandbox:** 浏览器身份全程只用身份名，任务隔离改由沙盒按任务目录完成 ([a472a15](https://github.com/V3teran/liusha/commit/a472a155e6d7af054f847018fcabe60ce24534d7))
* **task:** 并行工具调用的结果合进同一条消息，Anthropic 兼容端点不再拒绝 ([7b21122](https://github.com/V3teran/liusha/commit/7b21122d8841be1b2684693a55e36e8841ec652a))
* **task:** 批次名在写入时确定，任务搜索按批次名匹配 ([bc3ba0d](https://github.com/V3teran/liusha/commit/bc3ba0d9d9ea60d1d42b016f01f171a5bdf908c6))
* **task:** 沙盒暂不可达时任务保持进行中，等沙盒恢复后续跑 ([5c75e96](https://github.com/V3teran/liusha/commit/5c75e96609e8d9d8e14c9af9148f809aa23a7149))
* **task:** 自动恢复达上限终止任务时，对话时间线写终止节点 ([ab56650](https://github.com/V3teran/liusha/commit/ab566504827afbe59a11384dbbf2fb5b8c32edd8))
* **ui:** 任务详情与 LLM 调用抽屉的跳转区移到信息区之前 ([1f70788](https://github.com/V3teran/liusha/commit/1f70788037bb2e089104e8fe2ed38c264bee3256))
* **ui:** 列表搜索与筛选选项改到服务端，实体用可搜索的选择框选取 ([a3b6864](https://github.com/V3teran/liusha/commit/a3b6864e54d81f7f1c0396d40a913ad5ce436929))
* **ui:** 可滚动的长文本区块可键盘聚焦 ([93f2726](https://github.com/V3teran/liusha/commit/93f27260da6a9cf828140b849ab68aba4e19cf67))
* **ui:** 对话的任务、漏洞、探索图跳转放回「⋯」菜单，菜单改用 Radix ([692c54f](https://github.com/V3teran/liusha/commit/692c54f140883c243b573c52fe27b95e42dbeebc))
* **ui:** 带底色的行上文字对比度达标，可滚动区域可用键盘聚焦 ([c3e673b](https://github.com/V3teran/liusha/commit/c3e673b008394a365f3704ce0947713e1f6c6819))
* **ui:** 探索图聚焦时非焦点节点退到背景，文字仍满足 AA ([0ea70d7](https://github.com/V3teran/liusha/commit/0ea70d7ace7ed274d700d2f9ee2af17096acb2a6))
* **ui:** 浏览器后退回到应回的地方——打开的条目进 URL，统一历史规则 ([5815bbf](https://github.com/V3teran/liusha/commit/5815bbfa7225ae42f333e7e74b0bc1be29f50335))
* **ui:** 漏洞详情在窄屏占满宽度，不再把左半截推出屏幕 ([07d76df](https://github.com/V3teran/liusha/commit/07d76df0ff7b67844a83b8c02ab8d20c08308613))
* **ui:** 漏洞详情的跳转区移到基本信息之前 ([9cd31d2](https://github.com/V3teran/liusha/commit/9cd31d2b211205151fec7946527798c8cf5ac0e5))
* **ui:** 登录后回到原来要去的页面 ([a2483c8](https://github.com/V3teran/liusha/commit/a2483c8ff48a64c3856d781ee7e02df03882d7a7))
* **ui:** 跳转按钮放在所属元素旁，去掉对话行菜单里的重复入口 ([890aeb2](https://github.com/V3teran/liusha/commit/890aeb23922ac7b4fcd2c8c0842fcc7ddeb42e68))
* **ui:** 选中行上的次要文字对比度达到 AA ([1aae626](https://github.com/V3teran/liusha/commit/1aae626f662adf340a01a9da006bb78704fb7e3b))
* **workspace:** api 停止时关闭工作区目录，测试结束释放打开的文件 ([1e86243](https://github.com/V3teran/liusha/commit/1e8624330fa7f3d6da27f693c47f3578290636ed))
* 搜索词里的 % _ 按字面匹配，任务与工具搜索不再误命中 ([cc33c53](https://github.com/V3teran/liusha/commit/cc33c534f2f97b0ec549442164daebc5aef4eb02))


### Code Refactoring

* **exploration:** 删除没有生产者的控制事件队列，任务行监视无条件启动 ([2fb0f48](https://github.com/V3teran/liusha/commit/2fb0f48d56101b685bceefa481fc7c9294e67e90))
* **exploration:** 执行者与评估者只经图交接，删除执行轨迹与验证事件载荷 ([bac753d](https://github.com/V3teran/liusha/commit/bac753d47355b39a47de8a188912b2fb9701976e))
* **exploration:** 探索图按任务取，不再经对话转一道 ([dd52eea](https://github.com/V3teran/liusha/commit/dd52eea308b71945cebfdb0d6b887a86248ed87a))
* **exploration:** 节点不再记没有读者的 source_id ([3b1299f](https://github.com/V3teran/liusha/commit/3b1299f8557482310190365de0a507e76d908a92))
* **exploration:** 节点的状态原因统一叫 state_reason ([2ca14f6](https://github.com/V3teran/liusha/commit/2ca14f642e36e406db1d85df7da67b4a604285df))
* **exploration:** 观察的「置信度」改为假设专有的验证状态，评估者不再有别名 ([8415993](https://github.com/V3teran/liusha/commit/841599334f1ee74d53026d45eafd3cb88442386a))
* **exploration:** 评估链路一物一名：主张叫 Claim、配方叫 repro、证据叫 evidence ([068c425](https://github.com/V3teran/liusha/commit/068c425397a692ce53e4ef07c79214d8364524de))
* **task:** 任务目标只有一处事实源：task.brief 改为 task.goal，与根目标同事务写入 ([ce9330a](https://github.com/V3teran/liusha/commit/ce9330a4dadde3774e98df9f872ef64a46d04fe9))
* **task:** 状态与用量按任务取，对话不再代理任务数据 ([30a90fc](https://github.com/V3teran/liusha/commit/30a90fce2e9dcb0d6e4162860d80c23da91f4054))

## [0.9.0](https://github.com/V3teran/liusha/compare/v0.8.0...v0.9.0) (2026-11-12)


### Features

* **ui:** 主题首次访问跟随系统深浅偏好 ([e05a808](https://github.com/V3teran/liusha/commit/e05a808c32c125cf85b8d2e7e30cb4437df345f2))


### Bug Fixes

* **ui:** 浅色主题品牌徽记与登录页一致，切换主题即时跟随 ([76a63db](https://github.com/V3teran/liusha/commit/76a63dbc0a8c910439734cb8d295364f6af54293))

## [0.8.0](https://github.com/V3teran/liusha/compare/v0.7.0...v0.8.0) (2026-11-12)


### ⚠ 破坏性变更

* **ui:** 移除 DELETE /credentials/:host 与 DELETE /credentials/:host/sessions（逐个删除身份仍走 DELETE /credentials/:host/usernames/:username）；LLM 审计详情不再解析旧格式历史行（大写键、reasoning_content 等），此类行的正文与思考过程不再结构化展示。
* **sandbox:** 新增迁移 0206（tool 表增加 category_name 列），随 up 自动执行；工具目录在 api 启动同步时回填。GET /tools、GET /tools/:name 响应的工具对象新增 category_name 字段。
* **exploration:** 代理流量 Redis stream 默认键由 flow_events 改为 liusha:traffic_snapshots；升级瞬间旧键中尚未消费的流量不再读取，消费组在新键上自动重建。自定义过 proxy.stream_name 的部署不受影响。
* **httpapi:** 移除 POST /conversations/:id/abort，停止任务改用 POST /tasks/:id/control（{"action":"terminate"}）；新增迁移 0205（仅改 schema 注释），随 up 自动执行。

### Features

* **task:** 定时模板可查看、启停与删除——新建后不再无法停止 ([ff0ab27](https://github.com/V3teran/liusha/commit/ff0ab27e1d1b54293f8e5eb4b812810c1d1cd271))
* **ui:** 主次操作可辨、窄屏主从单栏、时间筛选收为单入口 ([6f2b9d3](https://github.com/V3teran/liusha/commit/6f2b9d3e11188781aadd17c31411c467d834a50c))


### Bug Fixes

* **conversation:** 纯空白消息在边界拒绝，不再建空会话 ([e013b18](https://github.com/V3teran/liusha/commit/e013b18dc960f0d92070517d7b3370e322bcbc97))
* **exploration:** 密钥不随跨源重定向外发、匿名验证真匿名、裁决退出等在途 ([2acd01f](https://github.com/V3teran/liusha/commit/2acd01f62de36a56536243cd741f9306de3eb000))
* **exploration:** 目标不可达计入验证上限，裁决未完成可按原方向重提 ([9798cef](https://github.com/V3teran/liusha/commit/9798cef23f48e6fbcc29a8351b82b4a4e141c578))
* **httpapi:** LLM 审计 Flush 在 ctx 已取消时必定返回错误 ([9820c30](https://github.com/V3teran/liusha/commit/9820c3055ec6cef2217b1724d2ea54889165052e))
* **sandbox:** 任务结束不再误杀共享容器内其他任务的进程 ([0920827](https://github.com/V3teran/liusha/commit/09208274907415a27759d8889ef5f6eb8d319886))
* **sandbox:** 共享容器不被误销毁、并发执行产物各归各位、配置写入串行 ([6646fea](https://github.com/V3teran/liusha/commit/6646fea5370fdf2c5cd1c8a1e22320198405888b))
* **task:** 优雅停机不再终结运行中任务——先排空任务再拆沙盒 ([23716f1](https://github.com/V3teran/liusha/commit/23716f130875108f713b130f74177099a87796fa))
* **task:** 续跑的 run 产出归属自身，不再错记到首个 run ([2c0637a](https://github.com/V3teran/liusha/commit/2c0637a9ea0b7ac19832f17806e9e37b4904c01c))
* **ui:** 对话流式思考道不再对读屏隐藏可聚焦链接 ([5a62bea](https://github.com/V3teran/liusha/commit/5a62bea18e33ed44259767ed6db26e0db1a1f661))
* **ui:** 弹层按钮可点、窄屏账号菜单可用、日志不重复，补齐可访问性 ([964bd04](https://github.com/V3teran/liusha/commit/964bd04daacc413b78c31363e81ed65f378c2840))
* **ui:** 编辑不再被迟到响应覆盖或静默丢弃；删除零调用方的凭证端点与审计旧格式兼容 ([45e3a44](https://github.com/V3teran/liusha/commit/45e3a44d3049bb14e1c9ec0f5eccb8ade7d402e1))


### Code Refactoring

* **exploration:** 命名收敛——一个概念一个名字，跨层同名同义 ([d5f2497](https://github.com/V3teran/liusha/commit/d5f24975fcd0d7d43b49d711da48421b5aa71721))
* **httpapi:** 清除旧架构残留——停止任务只留 control 入口 ([97ce973](https://github.com/V3teran/liusha/commit/97ce973a773a45059948c5104e5ef17c01dfbe70))

## [0.7.0](https://github.com/V3teran/liusha/compare/v0.6.2...v0.7.0) (2026-11-10)


### ⚠ 破坏性变更

* **config:** 移除 POST /models/providers/test 与 /models/providers/list-models，改用 POST /models/providers/diagnose；provider 新增 auth_scheme、output_limit_param 字段（迁移 0203 按协议回填），display_name 改为必填且不区分大小写唯一（迁移 0204 把空名称回填为 Base URL 主机名，同名追加序号）；API 提交空名称返回 400、重名返回 409。迁移随 up 自动执行。
* **exploration:** 新增迁移 0202（exploration_node.state 增加 superseded，exploration_edge.rel 增加 supersedes），升级随 up 自动执行；回滚（down）会删除吸收边、把 superseded 目标改为 aborted。

### Features

* **config:** provider 只认协议不认服务——请求画像、连接诊断与名称必填 ([48810dc](https://github.com/V3teran/liusha/commit/48810dc95d9e5c2219f72b1d8590099a1e8e5910))
* **exploration:** 目标吸收收敛——结果分析经 RevisePlan 原子落图 ([be78de5](https://github.com/V3teran/liusha/commit/be78de5ee2bc37bfb70a86cd6d2d7199690472cd))
* **ui:** 任务控制——暂停/继续合为切换钮，停止改用站内确认弹层 ([20f8b46](https://github.com/V3teran/liusha/commit/20f8b467f11f44bcd83ac4fa82217a2229f24bfe))


### Bug Fixes

* **ui:** 可点击控件统一手型光标 ([7e69b32](https://github.com/V3teran/liusha/commit/7e69b32db7d7d12621f8585029f9187e5d9b4107))

## [0.6.2](https://github.com/V3teran/liusha/compare/v0.6.1...v0.6.2) (2026-11-09)


### Bug Fixes

* **agent:** executor 只归一化带配方的观察；降级推导携带章程与输出契约 ([04eb60b](https://github.com/V3teran/liusha/commit/04eb60b8254994ba863ac27024adcecb007aea25))
* **agent:** 上下文取消引起的错误记 debug，不再在任务结束时刷 error ([2c55b14](https://github.com/V3teran/liusha/commit/2c55b142abd06f27555ddd79a01f6116bef966f0))

## [0.6.1](https://github.com/V3teran/liusha/compare/v0.6.0...v0.6.1) (2026-11-09)


### Bug Fixes

* **agent:** evaluator 轮询跳过在途假设，验证计数只算真正启动的 ([2c4c0ff](https://github.com/V3teran/liusha/commit/2c4c0ff5ac2147a15cf4a0f7e63a0b8a99a994e5))
* **agent:** 模型吐出残缺的工具参数不再拖垮整段 ReAct ([e4559da](https://github.com/V3teran/liusha/commit/e4559da8926db4beb3246321284447247b9f3e62))
* **config:** agent 名单统一经 registry.Allows 判定——修 CLI 工具目录恒为空 ([a5b670e](https://github.com/V3teran/liusha/commit/a5b670e5c304e09b2f2c4c17c0198e729887cdaa))
* **deploy:** 开发脚本的 redis 默认地址对齐 compose，过时注释与代码对齐 ([838c11c](https://github.com/V3teran/liusha/commit/838c11c3697f20214eae6a21516e1cb488edad6f))
* **sandbox:** tools.yaml 去掉重复登记的 trufflehog；清单加载校验工具名 ([a739149](https://github.com/V3teran/liusha/commit/a7391499bf597b87d6fadd64bc18f48968939a82))

## [0.6.0](https://github.com/V3teran/liusha/compare/v0.5.1...v0.6.0) (2026-11-09)


### ⚠ 破坏性变更

* **config:** 需执行迁移 0201（Docker 部署随 up 自动执行，升级前改过的整份提示词归档待处理）； 接口契约变更——/agents/:id → /agents/:code、PUT → PATCH（名单字段为 {on, off}）、 /skills/:code/detail 并入 /skills/:code、/settings/proxy-filter → /settings/proxy_filter； `make reseed` 已移除。

### Features

* **config:** 内置内容分层——出厂值编译进程序，数据库只存用户自定义 ([51785a1](https://github.com/V3teran/liusha/commit/51785a1ddfbff5eee901dbcf0ed3bec73dab9aad))
* **ui:** 智能体、技能与系统设置页改为编辑用户自定义——已自定义标记、对比出厂与恢复默认 ([e609f33](https://github.com/V3teran/liusha/commit/e609f33d16cb75e285209343e55669591150069c))

## [0.5.1](https://github.com/V3teran/liusha/compare/v0.5.0...v0.5.1) (2026-11-09)


### Bug Fixes

* **credential:** 清扫器状态查询失败不再当作任务不存在而误删会话 ([119fe61](https://github.com/V3teran/liusha/commit/119fe6141530bc14d23648c1b0b7b469908db5b4))
* **evaluator:** 删除轮询入口私有的 web→generic 降级，两条裁决入口同口径 ([46728fd](https://github.com/V3teran/liusha/commit/46728fd08169b2ae12bb02bc7402e21bd4d221a2))
* **repro:** 写入侧断言鉴别力与复现门同口径，弱断言交 LLM 裁决官终判 ([5284445](https://github.com/V3teran/liusha/commit/528444561c417b20cce1c10bc806d595d300ff39))

## [0.5.0](https://github.com/V3teran/liusha/compare/v0.4.1...v0.5.0) (2026-11-09)


### Features

* **deploy:** sandbox 镜像随业务镜像同版本发布，compose pull 一并预拉/升级 ([31c9ad0](https://github.com/V3teran/liusha/commit/31c9ad034a2a0fbed54f02c6ca7a89e677180ed8))
* **deploy:** 部署版本绑定到检出的发布 tag——代码与镜像永远同版本 ([ce3c077](https://github.com/V3teran/liusha/commit/ce3c0773376f9d70c737992703e8875f79de1853))


### Bug Fixes

* **bus:** Cancel 改为同步生效，修注销与发布竞态导致已取消订阅仍收到事件 ([df94b2d](https://github.com/V3teran/liusha/commit/df94b2d64b6f56637598e032cbdd41af6bad43f7))
* **ci:** build-sandbox 变更门比较整次推送范围，修多 commit 推送漏构建 ([65ef362](https://github.com/V3teran/liusha/commit/65ef362de5dae68933494467f1b3810447bf46a8))
* **cognition:** 修 Abort 与兜底检查竞态吞掉中止原因 ([2080753](https://github.com/V3teran/liusha/commit/2080753d8d9d6ebe1b951eda8e96b4b1ae50af04))

## [0.4.1](https://github.com/V3teran/liusha/compare/v0.4.0...v0.4.1) (2026-11-09)


### Bug Fixes

* **config:** 启动期不再校验 LLM 密钥就位；校验函数按实际职责更名 ([2b2d2fe](https://github.com/V3teran/liusha/commit/2b2d2fe40afbaa457e66d31bb9e16718bd5707ba))
* **sandbox:** 产物归属判定弃用 mtime 时钟过滤，改目录快照 diff——修 CI flaky ([e52be6c](https://github.com/V3teran/liusha/commit/e52be6cc658defb3fc777ae6cda050f96634db27))

## [0.4.0](https://github.com/V3teran/liusha/compare/v0.3.0...v0.4.0) (2026-11-08)


### Features

* LLM 配置不再强制启动前就绪——三层防线保证「先部署、后配置」全链路自洽 ([ea70e78](https://github.com/V3teran/liusha/commit/ea70e787e8f3752370ef70249906b0241fc84884))


### Bug Fixes

* generic 域文本配方的交付抽取 target——同漏洞跨配方域不再分裂多条台账 ([80dc3ac](https://github.com/V3teran/liusha/commit/80dc3acd3c5d6ffba82a860c950dd09eea3664ec))
* **llm配置:** 数字字段受控输入缺陷——清空回写 0、前导 0 吞吐致「输入 0 开头异常」 ([cea220b](https://github.com/V3teran/liusha/commit/cea220b441642938c12e6353e84f464b58cff500))
* **release-images:** tags 改逗号单行——多行插值折叠的根治 ([b51eb39](https://github.com/V3teran/liusha/commit/b51eb39e23fc2c88e1736cb98f2abc6c347c7dc7))
* **release-images:** 推送凭据换 GHCR_PAT——GITHUB_TOKEN 增量推已有包被拒 ([95fde52](https://github.com/V3teran/liusha/commit/95fde526d6725d368d27aca7c11a893beb087650))

## [0.3.0](https://github.com/V3teran/liusha/compare/v0.2.0...v0.3.0) (2026-11-07)


### Features

* **tasks:** tokens 拆双向 + 控制组三面恒显禁用 + 升级一键校准 ([21fdfdf](https://github.com/V3teran/liusha/commit/21fdfdf7037d8c63817fcf6887946daa0cbc732e))


### Bug Fixes

* **release-images:** 多行 tags 块状注入 + owner 小写 + dispatch 版本入参 ([b45f75e](https://github.com/V3teran/liusha/commit/b45f75ec4d57ef0b9933a58aa7ea506c76e9599b))

## [0.2.0](https://github.com/V3teran/liusha/compare/v0.1.0...v0.2.0) (2026-11-07)


### Features

* **finding:** impact 危害字段贯通——台账补「这洞有多危险」维度 ([aa4b4a6](https://github.com/V3teran/liusha/commit/aa4b4a6557d78cadb81052de8abe549639999076))
* **finding:** 漏洞证据链贯通——复现步骤/机器证据/裁决理由全量可读 ([8654c73](https://github.com/V3teran/liusha/commit/8654c732ea22ec1794ba0f6435dcf493e048c5be))
* **tasks:** 任务列表 tokens 列 + 操作位恒显 + 状态文案去内部术语 ([de81112](https://github.com/V3teran/liusha/commit/de81112e26c954ddd6f4950c54a6f6ab5e93c9df))


### Bug Fixes

* **e2e:** terminate 改走公开 API——旧事件通道重构后无人消费 ([6b2ccc3](https://github.com/V3teran/liusha/commit/6b2ccc332b9a74159dadbb8eca4e8170f119fee2))
* **e2e:** 停机终态断言对齐主动停语义——aborted 才是正确终态 ([4c76d0d](https://github.com/V3teran/liusha/commit/4c76d0d78fa4ebc1fcd78d9ea3ca37f61a73ff9c))
* **tasks:** tokens 列补进 summarySelect/GetSummary——18 列对齐（前次脚本中断致漂移） ([e4f0cab](https://github.com/V3teran/liusha/commit/e4f0cab01e4f584998765789caab73aaab9e36d8))
* **tasks:** tokens 目标序对齐列序（tokens 在 created_at 之前） ([2220d57](https://github.com/V3teran/liusha/commit/2220d57d59814355af64f82a0df96ff0885e479a))
