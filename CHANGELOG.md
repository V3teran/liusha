# Changelog

## [0.15.1](https://github.com/V3teran/liusha/compare/v0.15.0...v0.15.1) (2026-11-25)


### 问题修复

* **llm:** 改模型配置后立即生效，不再延迟 30 秒 ([ae45d35](https://github.com/V3teran/liusha/commit/ae45d35974be9c9b7fe86749e5388947bc6fd9d8))
* **ui:** 页面出错时显示中文错误页与「重新加载」 ([9c7108b](https://github.com/V3teran/liusha/commit/9c7108b4218b2fd3a0f00ab6fbe8831717c40907))

## [0.15.0](https://github.com/V3teran/liusha/compare/v0.14.1...v0.15.0) (2026-11-25)


### ⚠ 破坏性变更

* **httpapi:** 6 组接口路径改名、5 个列表接口的 `q` 参数改名为 `search`，调用方按新路径与参数名改写（对照表见升级说明）
* **tools:** 配置文件新增 `web_search` 段（`url` / `auth_header` / `api_key`），升级前在 config.yaml 里补上这一段（不用网页检索就三项留空），否则程序缺键拒绝启动
* **tools:** 13 个内置函数工具改名（对照表见升级说明），自己写的章程、技能或场景里按名字引用了工具的，升级后按新名改写
* **config:** `config.yaml` 须新增 `runtime`（`step_llm_timeout_seconds`、`step_tool_timeout_seconds`、`run_tail_bytes`、`tool_output_tail_bytes`）与 `compaction`（四个 `*_context_ratio`）两段共 8 个键，`runner.step_llm_timeout_seconds` 改名为 `runtime.step_llm_timeout_seconds`，缺键或留着旧键都拒绝启动
* **runner:** `runner.run_recovery_timeout_seconds` 不得小于 90，此前设得更小的部署须调高，否则程序拒绝启动
* **runner:** 新增配置键 `runner.action_parallelism`、`runner.verification_parallelism`、`runner.monitor_interval_seconds`，部署方须在 `config.yaml` 的 `runner` 段补上（缺省 4 / 3 / 360），否则程序拒绝启动
* **llm:** 新增配置键 `llm.account_concurrency`，部署方须在 `config.yaml` 的 `llm` 段补上（缺省 4；0 = 不限），否则程序拒绝启动

### 新功能

* **config:** 系统设置的出厂值统一取自 config.yaml ([a32dac2](https://github.com/V3teran/liusha/commit/a32dac2289231855aa5d96b1f21f36eac60f8840))
* **conversation:** 时间线支持多选筛选，胶囊带工具调用数 ([24c9974](https://github.com/V3teran/liusha/commit/24c9974e0f84c3e47e0f33ba62e779b70e0ad0a1))
* **conversation:** 时间线支持按智能体筛选 ([64bb929](https://github.com/V3teran/liusha/commit/64bb929433ad18e6d56fbb0380682e19ef38bc01))
* **exploration:** 探索图新增「只看方向关系」开关 ([fc5fceb](https://github.com/V3teran/liusha/commit/fc5fcebea46d9a1a5c14a74b0bcb505970be049b))
* **runner:** 任务内的并行度与监察间隔可配 ([60656ae](https://github.com/V3teran/liusha/commit/60656ae756ee55f1977090003f3f63ecf8fcc4b8))
* **task:** 任务列表的发现列按严重度分档显示 ([0c360ab](https://github.com/V3teran/liusha/commit/0c360abb15dbd161e9e74f52a1ceb4b9612c963c))
* **task:** 任务详情新增智能体调用情况 ([f883ca3](https://github.com/V3teran/liusha/commit/f883ca3073ef264ad45f32731b8b62334f0c8398))
* **tools:** 新增网页检索工具 web_search ([04b32bb](https://github.com/V3teran/liusha/commit/04b32bba5aaf14596c27b7fdf5d7e1e41e6ff1d8))
* **ui:** 从任务与对话直达它名下的模型日志 ([dc178b3](https://github.com/V3teran/liusha/commit/dc178b39c4345271c134df7e545908b1c6f598a7))
* **ui:** 侧栏显示程序版本 ([35a7644](https://github.com/V3teran/liusha/commit/35a76442a90cc5d833743c68348e6aa76f5d6eb2))
* **ui:** 对话列表可向左收起 ([df3e42c](https://github.com/V3teran/liusha/commit/df3e42c1dd0c4bec8540450a3dc8ac5e40cd7403))
* **ui:** 搜索框回车即时生效，并新增刷新按钮 ([86403de](https://github.com/V3teran/liusha/commit/86403de3affb9a058c34715f518186a9dc987608))


### 问题修复

* **config:** 渗透测试交付标准改为影响准入，加固噪音不再报出 ([c631f08](https://github.com/V3teran/liusha/commit/c631f08921c00267705f3b4acbc7e78fc96d7d38))
* **conversation:** 对话顶栏不再重复任务状态 ([1106bcd](https://github.com/V3teran/liusha/commit/1106bcd78774a58c286c22aea7bb08bebaf11092))
* **conversation:** 对话顶栏的耗时逐秒走 ([c8b69b8](https://github.com/V3teran/liusha/commit/c8b69b8bdd4d7304d54b0567dfc31a78cc19592a))
* **exploration:** 任务在跑时探索图持续自动刷新 ([ffa5b58](https://github.com/V3teran/liusha/commit/ffa5b582bdcc3cdf929c61748b9eada268bf91ce))
* **exploration:** 压缩链的时间预算随单步上限推导 ([a1389f2](https://github.com/V3teran/liusha/commit/a1389f254ca226ee221d0aae3ce1fd6e6930c4ba))
* **exploration:** 规划一轮的预算不再比单次调用还短 ([1d239a8](https://github.com/V3teran/liusha/commit/1d239a8d4e243c741707276b5e629d4987dba129))
* **exploration:** 规划者广度与深度并行，线索露头即深入且不被挤掉 ([d1a57d6](https://github.com/V3teran/liusha/commit/d1a57d6ff2af5c20392e72f5415ab9166443f74d))
* **llm:** 模型账户同时在途的请求数有了上限，不再被限流拖垮 ([e632545](https://github.com/V3teran/liusha/commit/e632545d7cb851dfcb4813c7a499e4eecf9e3332))
* **llm:** 模型额度用尽时不再空转重试，等到恢复再继续 ([7c35a8a](https://github.com/V3teran/liusha/commit/7c35a8ae9affdb644064140dadd88e3f80180baa))
* **llm:** 超时或被取消的调用不再丢失审计记录 ([0444cd8](https://github.com/V3teran/liusha/commit/0444cd85e1e046e6049539c1418b4490bea89f5f))
* **overview:** 概览区块不再被拉高，随内容伸展 ([04a1669](https://github.com/V3teran/liusha/commit/04a1669c79f696d72bfafaf347e705dcb6490b10))
* **runner:** 同时执行的任务数缺省由 10 改为 3 ([ca08f52](https://github.com/V3teran/liusha/commit/ca08f5276c3409c63d04e5c0fd46e81c15e5c792))
* **runner:** 执行长命令的任务不再被误判停摆而重跑 ([6f39d2f](https://github.com/V3teran/liusha/commit/6f39d2f277bb54371ebe635f92a34025b9027027))
* **sandbox:** 旧沙盒镜像读得了新版的配置文件 ([99008b6](https://github.com/V3teran/liusha/commit/99008b6d149ea0c99e850597025667f7d277a55b))
* **ui:** 侧栏品牌行与导航之间不再画分隔线 ([348d5ec](https://github.com/V3teran/liusha/commit/348d5ecf9b7045490f7b4f3ac6a099e50de39937))
* **ui:** 列表的表头与内容同侧对齐 ([5eb6ae3](https://github.com/V3teran/liusha/commit/5eb6ae3c81a491b803cf98e89a2cd36bffe7c4ec))
* **ui:** 日志行与发现类型条的读屏名称对齐屏幕文字 ([5906902](https://github.com/V3teran/liusha/commit/59069027d4e3ee75ea0dfbbe43222a5042d6c074))
* **ui:** 模型日志里「压缩」不再显示为英文 ([bb67f1c](https://github.com/V3teran/liusha/commit/bb67f1c4a82d7fd588f5eb799f3d68481ae6df96))
* **ui:** 模型配置的容量字段补填写引导 ([d1ab9d2](https://github.com/V3teran/liusha/commit/d1ab9d2e8a1096b97e2c3f8299dd870643d50f2d))


### 重构

* **httpapi:** 接口路径与资源命名对齐 ([8eee907](https://github.com/V3teran/liusha/commit/8eee9075ba9fb562db47e198bcc3e3b8e88173eb))
* **tools:** 内置工具名统一成「对象_动词」 ([41b534e](https://github.com/V3teran/liusha/commit/41b534e08a2ef62694859b44bac6a57e4d1275cc))

## [0.14.1](https://github.com/V3teran/liusha/compare/v0.14.0...v0.14.1) (2026-11-22)


### 问题修复

* **config:** CTF 场景补充攻击方法：收敛假设、直取旗标 ([77a22aa](https://github.com/V3teran/liusha/commit/77a22aa0f15461dc119962714b276acb6372c62a))
* **config:** CTF 攻击方法补深度优先纪律：原语走完才换面 ([acb5c08](https://github.com/V3teran/liusha/commit/acb5c08def0a010d42943ffb386ceb75d1c488ad))
* **config:** e2e 清库豁免检索模型配置表 ([7c88427](https://github.com/V3teran/liusha/commit/7c88427a78aa816cc76d9714bb2d5b68d629f181))
* **exploration:** 监察者的停滞判定只认打转的过程证据 ([48847b6](https://github.com/V3teran/liusha/commit/48847b6135211ab3b4926f691accf23a343260d6))
* **llm:** 输出上限缺省提到 16k，规划契约不再被截断 ([2d531e6](https://github.com/V3teran/liusha/commit/2d531e64b0651242821492f940618e2043fc6062))
* **tsecbench:** 任务目标带全题面元信息 ([da2e89a](https://github.com/V3teran/liusha/commit/da2e89a6eb6ef80de526b44002caeef5e58b0ad9))
* **tsecbench:** 单题时限按难度分级 ([12905bf](https://github.com/V3teran/liusha/commit/12905bfdead6717250d3d69a6abe4ffba978d396))
* **tsecbench:** 单题预算改按分值分档；监察者盯假设形成 ([5940e77](https://github.com/V3teran/liusha/commit/5940e77ae0bd37d80f2469ed1c58c42fff44e88e))
* **tsecbench:** 适配下发契约，通关任务交还引擎收敛 ([36b0b5c](https://github.com/V3teran/liusha/commit/36b0b5c716cf9236d0327f99e419bb26a3de85f0))
* **ui:** 深色主题徽记统一为同一枚应用图标 ([f6e69ce](https://github.com/V3teran/liusha/commit/f6e69ce06ed01faa2a13aae6228dbbd188b4faf2))

## [0.14.0](https://github.com/V3teran/liusha/compare/v0.13.5...v0.14.0) (2026-11-22)


### ⚠ 破坏性变更

* **config:** 配置文件的 embedding 段已删除，继续保留会因未知键拒绝启动；升级前从 config.yaml 删掉 embedding 段并从 .env 删掉 LIUSHA_EMBEDDING_API_KEY，启动后到「模型配置」页「检索模型」分区填写密钥（不填则知识库只做字面检索，对话与任务不受影响）
* **llm:** LLM 调用接口不再返回 request_id，调用以 id 标识；调用方改用 id。
* **traffic:** 流量详情不再返回 scheme 与 http_version，二者分别在 url 与 request_raw 里；调用方改从 url 与原始报文读取。
* **conversation:** 对话消息不再返回 id 字段，以 seq 标识消息；角色只有 user / assistant，存量的 system / tool 归入 assistant；调用方改用 seq。
* **dispatch:** POST /tasks 立即下发（cron 为空）时带非空 description 返回 400，批次不再保存描述与下发清单；调用方立即下发时不要带 description。
* **finding:** 漏洞接口不再返回 first_seen_at（与 created_at 恒等），存量严重度规整为 critical / high / medium / low / info 五档小写（词表外的归 medium）；调用方改读 created_at。
* **finding:** GET /findings 与 GET /findings/:id 的响应删除 cwe_id、owasp_category 字段（从未有值），改为 class、class_name、cwe；读取这两个字段的调用方改读 cwe。
* **httpapi:** GET /credentials 由 host 汇总改为登录身份分页列表 {items, total}（参数 search / page / size），GET /credentials/:host 已删除；直接调用凭证接口的脚本改用 GET /credentials?search=<host> 取某个主机的身份。
* **exploration:** 移除 `liusha corpus-import` 命令，Markdown 知识改在控制台「知识库」页导入。

### 新功能

* **audit:** 审计日志可查看 ([5032d21](https://github.com/V3teran/liusha/commit/5032d21b634da36dbffc2fa63dcd9d49b202ede5))
* **config:** 工具调用与流量记录可配留存天数 ([766ef09](https://github.com/V3teran/liusha/commit/766ef09e047527645a2c0bd503eb5277d5d3e56c))
* **config:** 新部署默认同时执行 10 个任务 ([379c3ba](https://github.com/V3teran/liusha/commit/379c3bab588c8de81b60820a9ae6d184cc78bb0a))
* **config:** 检索模型可配置服务地址与模型名 ([66d737b](https://github.com/V3teran/liusha/commit/66d737b4e95b2b9cb7971ef11d9af688a677c5b8))
* **config:** 检索模型密钥改前端配置 ([d845767](https://github.com/V3teran/liusha/commit/d845767051d0f71d9b9f7cb2732a8108b2084c09))
* **conversation:** 对话消息只用序号标识 ([29ff04c](https://github.com/V3teran/liusha/commit/29ff04cfe7a0fd7c48be8a2e116b38adc3861e2f))
* **corpus:** 检索方式标识显示运行降级 ([c1d2362](https://github.com/V3teran/liusha/commit/c1d2362af6b717ebfee4d05b9f610274a79689e0))
* **dispatch:** 新建任务的描述只在定时执行时填写 ([9a118ce](https://github.com/V3teran/liusha/commit/9a118ceee398c9142deaf3a4b5d2d73897957acf))
* **exploration:** 知识库检索按相关度过滤，沉淀的经验不再重复堆积 ([debf9db](https://github.com/V3teran/liusha/commit/debf9dbdb9550277584094abde45ef951d7f4784))
* **finding:** 发现按类型记录，台账可按类型筛选、详情给出 CWE ([6c382bf](https://github.com/V3teran/liusha/commit/6c382bf1864e34345455740dc300b97e00ea4d4d))
* **finding:** 漏洞详情只保留一个发现时间 ([ca5a692](https://github.com/V3teran/liusha/commit/ca5a692cfb3cd12e3a8a744044ac7cb839f88e30))
* **httpapi:** 凭证库的搜索与分页改在服务端完成 ([4abb6f8](https://github.com/V3teran/liusha/commit/4abb6f8a5a76fba18a3981b4ff1408d83148b44a))
* **llm:** LLM 调用详情改以调用编号标识 ([0e6d5e9](https://github.com/V3teran/liusha/commit/0e6d5e91148decc113e398e2ad6583866e6082af))
* **overview:** 模型消耗面板删去最近 24 小时读数 ([0259984](https://github.com/V3teran/liusha/commit/0259984cf4aa3c7f218bbdec67604ad5f97488d4))
* **overview:** 趋势面板改平滑渐变面积图，时间窗可切 7 / 14 / 30 天 ([b01cfa0](https://github.com/V3teran/liusha/commit/b01cfa030b22391cff4154930d5f96da939b8606))
* **task:** 任务详情显示终止原因 ([821f33e](https://github.com/V3teran/liusha/commit/821f33ee266e12341ba9d40cbf4d4a53e8bfd1d7))
* **traffic:** 流量详情不再单列协议与 HTTP 版本 ([2a0a001](https://github.com/V3teran/liusha/commit/2a0a0014a33e522b69675d55b421c50728af6e83))
* **ui:** LLM 配置页更名「模型配置」 ([cac14d2](https://github.com/V3teran/liusha/commit/cac14d2729f2377cf0f90322506d06d3c7bc8724))
* **ui:** 加载失败可重试，操作结果改用站内通知与确认 ([112bb2d](https://github.com/V3teran/liusha/commit/112bb2d5e65339ac3a43e50909fe9d08176d811f))
* **ui:** 新增概览页，一屏看清组件健康与待处理的问题 ([5c27bf2](https://github.com/V3teran/liusha/commit/5c27bf2884e5f55f4c90468df728f0c7ac3eaaa1))
* **ui:** 新增知识库页面，可查看、录入、导入与检索预览 ([4e656e5](https://github.com/V3teran/liusha/commit/4e656e5bcc882f663c9c946c97bf386851bf1059))
* **ui:** 检索模型表单对齐业界交互细节 ([ef5c5c5](https://github.com/V3teran/liusha/commit/ef5c5c5bcf82be5e080272d7a147216d5712aaec))
* **ui:** 界面改用 IBM Plex 字体与小圆角，列表更紧凑 ([f024dfb](https://github.com/V3teran/liusha/commit/f024dfb81e3e4c3c36c7274c18554f335ba3a8a4))
* **ui:** 知识库检索测试成为独立视图，导入前可预览切分结果 ([d4ead8e](https://github.com/V3teran/liusha/commit/d4ead8ee772c37804ab745ed4fbd301fc61b42f0))
* **ui:** 观测组三页更名重排 ([9b6d222](https://github.com/V3teran/liusha/commit/9b6d2225a1e2fda96ad854089ed7844fa5dac51d))
* **ui:** 趋势平滑改 Catmull–Rom 样条，峰顶圆滑过渡 ([a1c2304](https://github.com/V3teran/liusha/commit/a1c230444fe4e8b09c75f492e73694f87ac2a3c2))


### 问题修复

* **audit:** 审计日志不再记录没有发生过的凭证删除 ([bc8565c](https://github.com/V3teran/liusha/commit/bc8565c829a8cbb545affb4a35a253bedef43652))
* **audit:** 工具调用记录里的中文输出不再变成乱码 ([e2c31c8](https://github.com/V3teran/liusha/commit/e2c31c817fd22b2de9f9636b381e68629de80329))
* **conversation:** 重命名时清空标题等于取消，原标题不会丢 ([0613aca](https://github.com/V3teran/liusha/commit/0613aca4608d4caafb88f5f37f599dac2461c0a0))
* **deploy:** 数据库错误日志不再写出查询参数 ([0b5121e](https://github.com/V3teran/liusha/commit/0b5121e554c56037de4fe0105a602c53feafc838))
* **dispatch:** 超长的任务目标在下发时一律拒绝，不再建出任务 ([822c3e5](https://github.com/V3teran/liusha/commit/822c3e55876780606d39ec52fafce586d2f879a4))
* **exploration:** 只有付出过代价的教训才沉淀为经验 ([d51598b](https://github.com/V3teran/liusha/commit/d51598bb001002ecfe47f0c101bc401c6828f65a))
* **exploration:** 查看智能体流量时请求与响应体按原文给出 ([a22142b](https://github.com/V3teran/liusha/commit/a22142b43ece901994113fb4bddc3a41378e0846))
* **exploration:** 沉淀的经验不再用「本题」指代原任务 ([49185e5](https://github.com/V3teran/liusha/commit/49185e5e41ffb04f5f70e78de4a913f1f37d7675))
* **exploration:** 知识库字面检索不再因共有一个常见词召回无关条目 ([d122e5a](https://github.com/V3teran/liusha/commit/d122e5ae250644ed4f282e6984d783bdecffcdc7))
* **exploration:** 知识库检索门槛按标注标定，整合找近似条目不设门槛 ([c3d3198](https://github.com/V3teran/liusha/commit/c3d3198fbd93d198e3e17e1427a8d64e6e440d70))
* **exploration:** 规划者不再在后续轮次重复检索同一方向的知识 ([e4dfbf4](https://github.com/V3teran/liusha/commit/e4dfbf4d4f79c592865841d492f36524df540e08))
* **finding:** 处置发现时严重度只能取五档 ([8af3b67](https://github.com/V3teran/liusha/commit/8af3b6780450e8d927242e4a4d4df1f30de38cc6))
* **httpapi:** 凭证库只接受合法的主机，非法主机直接拒绝 ([1200d34](https://github.com/V3teran/liusha/commit/1200d347f0a7e047e5c3268ffe105e6c935ebde4))
* **httpapi:** 列表的筛选条件写错时提示错误，不再悄悄不筛 ([b668415](https://github.com/V3teran/liusha/commit/b668415d57eceed5ef4df19b223aa89532f728bb))
* **llmconfig:** 检索模型 KeepExistingKey 的 COALESCE 不能写在 VALUES 里 ([4f07e98](https://github.com/V3teran/liusha/commit/4f07e98e11957ce565c8abe1c7235a7c821b7d99))
* **llmconfig:** 检索模型未配置判定改用 errors.Is ([eee8bb4](https://github.com/V3teran/liusha/commit/eee8bb4c152c7db7aa12258dc13faec617c9efd2))
* **sandbox:** 任务开始时不再偶发看不到任何工具 ([34c0676](https://github.com/V3teran/liusha/commit/34c06769263d2b3a54ceda6efa0ccde55ff14040))
* **ui:** 侧栏当前页改用正文主色，深浅主题下都是黑白 ([cc1c28a](https://github.com/V3teran/liusha/commit/cc1c28a80048fd1144d32430ce5c97e10dc29e6d))
* **ui:** 图标粗细统一，弹窗与输入框里的文字不再发虚 ([986862e](https://github.com/V3teran/liusha/commit/986862ed824eb67bb55691bdab196cbebf5a08b6))
* **ui:** 弹层关闭后焦点回到打开它的位置 ([1e9f62f](https://github.com/V3teran/liusha/commit/1e9f62fb6c655cdc068d6f653b2a72ce632fac22))
* **ui:** 所有页面的筛选栏都能一键清空筛选 ([25c1af0](https://github.com/V3teran/liusha/commit/25c1af0b431570d9005211b9e0d0e3c71423a7ee))
* **ui:** 智能体按工作流顺序排列，四张卡片两列对齐 ([ad5fbc0](https://github.com/V3teran/liusha/commit/ad5fbc086c7321488522c11be25320bb41e23d1f))
* **ui:** 每页条数选择框显示的就是实际条数 ([f1dc54d](https://github.com/V3teran/liusha/commit/f1dc54dd86c5f89675be3227de8d275b40e98ea8))
* **ui:** 浅色主题下智能体身份色的文字对比度达标 ([396f0f7](https://github.com/V3teran/liusha/commit/396f0f7670d850bad49ac12bda35405f446d978f))
* **ui:** 知识库的适用范围筛选列出全部场景 ([b832caf](https://github.com/V3teran/liusha/commit/b832cafe8fed897f60ec31ddd279111fc9b84933))
* **ui:** 统一字号阶梯与状态配色，修正多处界面细节 ([482d16a](https://github.com/V3teran/liusha/commit/482d16ab1b14d12c84b771899a5aa9d78ccdca19))
* **ui:** 读屏能分清侧栏与对话列表，表格每列都有列名 ([8f8d6f8](https://github.com/V3teran/liusha/commit/8f8d6f84e33e3e5b98951f13e4236714aee89002))
* **ui:** 趋势曲线在陡峰处越出图顶被裁平 ([c744cf2](https://github.com/V3teran/liusha/commit/c744cf274357017eeae9ad4358c0ff70c14873ea))
* **ui:** 趋势曲线在陡峰处越出图顶被裁平（补齐早退分支的返回结构） ([c498c21](https://github.com/V3teran/liusha/commit/c498c2183bfe45da58f3f48f68378ccca0fc1825))
* **ui:** 趋势曲线的 FC 钳制判据补上段横距归一，切线分段共享 ([3536f7c](https://github.com/V3teran/liusha/commit/3536f7ce4c0200c148a0a41532b358bb057e6cbd))
* **ui:** 配置与表单去掉多余的滚动条和说明文字 ([262ad6b](https://github.com/V3teran/liusha/commit/262ad6b3c8300a6d6c643d2df94c48a0f22e3a63))
* **ui:** 页面标题下不再常驻一行说明文字 ([8529113](https://github.com/V3teran/liusha/commit/852911303ae169c62062a603b4c31a1dfca14455))

## [0.13.5](https://github.com/V3teran/liusha/compare/v0.13.4...v0.13.5) (2026-11-20)


### 问题修复

* **config:** CTF 场景只把 flag 当作可交付的主张 ([f62f8cd](https://github.com/V3teran/liusha/commit/f62f8cd28238b47ad8b97d41cb7c076f0adcab6d))
* **config:** CTF 已有定论的 flag 不再被重复回报 ([1bb4ed4](https://github.com/V3teran/liusha/commit/1bb4ed45bd5353e6bdb84db58648245a4d658311))
* **config:** CTF 的 flag 陈述只含一个 flag ([5bdfd92](https://github.com/V3teran/liusha/commit/5bdfd9292ba293d5d0041ae99f66d1cd0c880275))
* **config:** CTF 的候选 flag 够数时先等验证结论 ([23b8803](https://github.com/V3teran/liusha/commit/23b880393a1653f2ee3085394aca57113d6ff2d2))
* **config:** 渗透测试与 CTF 场景说明更准确 ([e93c4bb](https://github.com/V3teran/liusha/commit/e93c4bb253ae7d3dddb57206c4622e9585cb67af))
* **exploration:** 侦察中的推断不再当作主张送去验证 ([be0e88f](https://github.com/V3teran/liusha/commit/be0e88f225be6fa8936eed82b86ddb2239b09bba))
* **exploration:** 同一主张经不同配方确认不再落成重复的漏洞 ([a2d8715](https://github.com/V3teran/liusha/commit/a2d8715d937b8e6cae46db4e532545e809616ce8))
* **exploration:** 新结果坐实后不再排出重复的动作 ([dbe91bf](https://github.com/V3teran/liusha/commit/dbe91bfb80e936110e6e721b28c10d8aa578d488))
* **exploration:** 监察者不再发出无据或过时的重规划请求 ([d0d8630](https://github.com/V3teran/liusha/commit/d0d86305b62a5e83b4045fe7017742445e1f095c))
* **exploration:** 监察者能看到假设的验证情况 ([ac3d8e9](https://github.com/V3teran/liusha/commit/ac3d8e9c61ee2bcbdc8b3a2288efe9e673a70bea))
* **exploration:** 规划者的知识库检索不再重复记录 ([9a31662](https://github.com/V3teran/liusha/commit/9a31662fce6e30901574fc94471b3de919ffff23))
* **exploration:** 规划者能看到待验证的主张，不再重复取得 ([3bab3a6](https://github.com/V3teran/liusha/commit/3bab3a66587daec181d6fcdbe1bcf7d59d525b66))
* **exploration:** 证据不足的证伪不再让探索方向被放弃 ([0306c5d](https://github.com/V3teran/liusha/commit/0306c5d3ec1e8df62290b8ece399dca9470e0b43))
* **exploration:** 限流后重新取得的主张能重新验证 ([8a93f9d](https://github.com/V3teran/liusha/commit/8a93f9de13bba695e266164d20f79bb6d2e2db56))
* **llm:** 生成较久的非流式调用不再被掐断重试 ([386a6bd](https://github.com/V3teran/liusha/commit/386a6bd701fe937c24df9282083d9f58c5d2cd4d))
* **tsecbench:** 只提交坐实主张里的 flag，靶场启动可重试 ([987d50b](https://github.com/V3teran/liusha/commit/987d50b27e32f555644ab158ae2940814c0f07d6))
* **tsecbench:** 等靶场容器就绪再下发任务 ([d68455a](https://github.com/V3teran/liusha/commit/d68455a696fae3d41769a4d6c6d7062a1dfe8cd2))


### 性能优化

* **exploration:** 执行者不再持有知识库检索 ([50b2540](https://github.com/V3teran/liusha/commit/50b2540fb25fd6d6bc81daaba5a7d06aff301b8c))
* **exploration:** 规划者同一方向只检索一次知识库 ([4f28241](https://github.com/V3teran/liusha/commit/4f282418a972d8fcc07866739eb24e30636d4abf))
* **exploration:** 规划者开局不再自动带入以往任务的经验 ([14ba0a7](https://github.com/V3teran/liusha/commit/14ba0a74c0af4da3dce562219f59c6a6da8f0e4e))

## [0.13.4](https://github.com/V3teran/liusha/compare/v0.13.3...v0.13.4) (2026-11-20)


### 新功能

* **config:** 技能与场景新建不再要求用户填标识符 ([701a6a1](https://github.com/V3teran/liusha/commit/701a6a1c843d6dd3d2747f1d433aa577925d2901))


### 问题修复

* **db:** jsonb 写入边界清洗字符串值中的 NUL ([4c1e886](https://github.com/V3teran/liusha/commit/4c1e88668a8bec5c199399226a1f8b4a610788c3))
* **finding:** 交付与验证解耦，台账缺口由周期补交付收账 ([5bb9283](https://github.com/V3teran/liusha/commit/5bb9283498580475a09ee79b8080f67d3d35efcb))
* **llm:** 流截断归为瞬时故障，取消的流不再误记为截断 ([962cf75](https://github.com/V3teran/liusha/commit/962cf75b8d9a0f1f25da9b3043405a6fb15bae6e))
* **task:** 目标主机派生认裸地址，存量任务回填 ([8fb8dc2](https://github.com/V3teran/liusha/commit/8fb8dc2d6eae7061c77e95c33286808bd547f874))
* **ui:** 探索图节点详情的字段标题与分类值中文化 ([ba9f7c6](https://github.com/V3teran/liusha/commit/ba9f7c62d221c7562a78062275e6133575d541b6))

## [0.13.3](https://github.com/V3teran/liusha/compare/v0.13.2...v0.13.3) (2026-11-20)


### 新功能

* **task:** CTF 的 flag 经复现门坐实，图上与漏洞同级 ([56008eb](https://github.com/V3teran/liusha/commit/56008eb101dbeef9ebfe8cde427235f1e9186278))
* **tsecbench:** 只提交坐实的 flag ([783b8ea](https://github.com/V3teran/liusha/commit/783b8eaddb0b1bae0f363de634c95d2592a58f50))
* **tsecbench:** 平台给出 flag 数量就写进目标 ([3cc9451](https://github.com/V3teran/liusha/commit/3cc9451f3dff1bfef3705683466f7c568c32e2b4))


### 问题修复

* **llm:** 调用点不再压低单次输出上限 ([32186c3](https://github.com/V3teran/liusha/commit/32186c37d70e3fbdbd08fc49de43ac929a5186fa))
* **planner:** 判停即收回在途动作，收敛不再空转 ([9063e93](https://github.com/V3teran/liusha/commit/9063e9313fc987445b37d2ce0436434bd165464a))
* **task:** 停机较久后重启不再终止进行中的任务 ([7be7f2c](https://github.com/V3teran/liusha/commit/7be7f2ce023a1812b5d7a9cb9fb2f59fe6dfbcda))
* **task:** 队列丢失的排队任务自动重新开跑 ([3748531](https://github.com/V3teran/liusha/commit/374853147ac0b212db35665d7b89b0110d649384))
* **ui:** 台账改称「发现」，容纳 flag 与漏洞两类成果 ([d4ca7db](https://github.com/V3teran/liusha/commit/d4ca7dbc213bb051d538ee50e7fc25a14970ea42))
* **ui:** 已绑定任务的对话保留只读的场景展示 ([beaf6a5](https://github.com/V3teran/liusha/commit/beaf6a5e9f48bb8f17117eafe9a9150d9871ccd6))
* **ui:** 聊天回答与任务消息渲染为同一张智能体卡片 ([dbd263e](https://github.com/V3teran/liusha/commit/dbd263ee678b460e7053ce6f92e7dc8a3ffe34d3))

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
