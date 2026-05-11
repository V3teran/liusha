---
name: jwt_tool
category: auth
description: JWT 攻击瑞士军刀——解码/伪造/None 算法/算法混淆/key confusion/弱 secret 暴破。任何 Authorization=Bearer 流量都先喂它一遍。
---

# jwt_tool 项目特定约束

## 沙箱环境

- **运行时**：python3 + git clone 到 `/opt/jwt_tool`，通过 `/usr/local/bin/jwt_tool` 包装脚本调用。
- **网络**：本地解码/伪造无需网；`-I` 模式发请求验证用 e2e 灌入的真实 host:port。
- **超时**：基础解码瞬时；`-C` 弱 secret 爆破依字典大小（10k 词典 ~30s）。
- **wordlist**：`/opt/SecLists/Passwords/jwt.secrets.list`（如不存在用 `/opt/SecLists/Passwords/Common-Credentials/10k-most-common.txt`）。

## 项目策略

### 5 大经典 JWT 攻击

1. **解码**（一切的开始）：
   ```sh
   jwt_tool eyJhbGc...                    # 直接解出 header/payload
   ```
2. **None 算法绕过**（CVE-2015-9235 类）：
   ```sh
   jwt_tool <token> -X a                  # alg=none 攻击
   ```
3. **HS256 弱 secret 暴破**：
   ```sh
   jwt_tool <token> -C -d /opt/SecLists/Passwords/jwt.secrets.list
   ```
4. **算法混淆 RS256 → HS256**（拿 public key 当 secret）：
   ```sh
   jwt_tool <token> -X k -pk pubkey.pem   # key confusion
   ```
5. **Key confusion 综合**：
   ```sh
   jwt_tool <token> -M at                 # All Tests 跑全套
   ```

### 实战流程

- **`-T` 快速分析**：解码 + 检查常见漏洞模式（标记 weak claim / 已过期等）
- **`-S hs256 -p <secret>`** + **`-I -ru <claim>=<val>`**：用已知 secret 重新签发改 claim 的 token
- **`-Q <jti>`**：检查 jti 重放保护

## 写 finding 红线

发现 JWT 漏洞（None 接受 / 弱 secret 命中 / 算法混淆成功 / 关键 claim 可改）**直接构成 high finding**。`evidence` 必含：
- 原 token（前 30 字符 + `...`）
- 解码后的 header + payload
- 攻击命令（`jwt_tool ... -X a/-C/-X k`）
- 伪造 token（前 30 字符 + `...`）
- 用伪造 token 调 API 返成功的 curl 命令 + 响应

## 决策边界（什么时候**不要**用 jwt_tool）

- token 不是 JWT 格式（`eyJ` 开头才是 base64 JWT）→ 是其他格式（PASETO/session）就不行
- 流量没有 token → 先用 hydra 爆登录拿 token
- 已知 secret 强（如 32 字节随机）→ 跳过 `-C` 暴破，直接看 None / 算法混淆
