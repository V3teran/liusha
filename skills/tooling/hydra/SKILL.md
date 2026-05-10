---
name: hydra
category: auth
description: 弱口令爆破——支持 50+ 协议（http-form/http-basic/http-digest/ssh/ftp/smb/mysql/...）。LLM 已熟悉用法——本手册只列沙箱约束 + web 场景常用 module + 写 finding 红线。
---

# hydra 项目特定约束

## 沙箱环境

- **网络**：访问宿主用 `host.docker.internal`。
- **超时**：默认 16 线程；爆 100k 密码 ~5-10min，依目标响应速度。**先小字典 100-1k 试，命中再扩**。
- **wordlist**：`/opt/SecLists/Passwords/Common-Credentials/10-million-password-list-top-{100,1000,10000}.txt` 渐进式上量；用户名 `/opt/SecLists/Usernames/top-usernames-shortlist.txt`。
- **输出**：默认 stdout 含 `[80][http-post-form] host: ... login: admin password: ...` 格式，`grep '^\['` 提结果。

## 项目策略

### Web 常用 module

- **`http-get-form`** / **`http-post-form`** —— Form 登录爆破（最常见）
  ```sh
  hydra -L users.txt -P passes.txt host.docker.internal http-post-form \
    "/login:user=^USER^&pass=^PASS^:F=Invalid"
  ```
  关键：`F=<失败标识>` 或 `S=<成功标识>`——必须**先 curl 试一个错的**看错误响应里的关键字
- **`http-basic`** / **`http-digest`** —— Basic / Digest auth 头
  ```sh
  hydra -L users.txt -P passes.txt host.docker.internal http-basic
  ```
- **`https-*`** 同理（前缀 https）

### 必加参数

- **`-V`** verbose 看每次尝试（debug 时用，否则 stdout 太大）
- **`-f`** 命中即停（默认会跑完所有组合）
- **`-t 4`** 限 4 线程（防 ban / DoS）
- **`-w 5`** 单请求等 5s（防误判 timeout）

## 写 finding 红线

命中弱口令（hydra 输出 `login: <u> password: <p>`）**直接构成 high finding**。`evidence` 必含：
- 完整 hydra 命令（含 host / module / form 参数 / 字典名）
- 命中的用户名 + 密码（前 3 字符 + `***` 脱敏，原值放 evidence 隐藏字段或私有 channel）
- 复现 curl 命令（用命中凭证登录返 200）
- 影响评估（admin 账号 / 普通账号 / 测试账号）

## 决策边界（什么时候**不要**用 hydra）

- 接口有 captcha / rate-limit → hydra 无法绕过，浪费时间
- 已有合法凭证 → 用 jwt_tool 看 token 漏洞 / 业务越权，不用爆密码
- API token / OAuth flow → hydra 不擅长，python3 + requests 手撸
- 不知道 form 字段名 / 错误标识 → 先 curl 试一个错的看响应再写 hydra 命令
