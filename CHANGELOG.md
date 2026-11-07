# Changelog

## [0.3.1](https://github.com/V3teran/liusha/compare/v0.3.0...v0.3.1) (2026-11-07)


### Bug Fixes

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
