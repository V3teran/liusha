# Changelog

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
