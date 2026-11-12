# Changelog

## [0.9.0](https://github.com/V3teran/liusha/compare/v0.8.0...v0.9.0) (2026-11-12)


### Features

* **ui:** 主题首次访问跟随系统深浅偏好 ([e05a808](https://github.com/V3teran/liusha/commit/e05a808c32c125cf85b8d2e7e30cb4437df345f2))


### Bug Fixes

* **ui:** 浅色主题品牌徽记与登录页一致，切换主题即时跟随 ([76a63db](https://github.com/V3teran/liusha/commit/76a63dbc0a8c910439734cb8d295364f6af54293))

## [0.8.0](https://github.com/V3teran/liusha/compare/v0.7.0...v0.8.0) (2026-11-12)


### ⚠ BREAKING CHANGES

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


### ⚠ BREAKING CHANGES

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


### ⚠ BREAKING CHANGES

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
