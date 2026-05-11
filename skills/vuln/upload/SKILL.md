---
name: upload
category: web
description: 任意文件上传——上传成功 + 访问能执行（PHP/JSP/ASP webshell 或 polyglot）。验证靠三段证据链：上传请求 + 上传后 URL + 访问该 URL 的响应。
---

# 任意文件上传

## 漏洞本质

应用允许上传**未严格校验类型/内容/路径**的文件，导致：

- **上传 + 执行**：上传可解析后缀（`.php` / `.jsp` / `.aspx`）+ 访问能执行 → **RCE（critical）**
- **上传 + 存为静态**：能上传任意内容但服务器不解析 → 仍可做 stored XSS / 钓鱼载体（high）

## 挖掘方向（思路，不是步骤）

**唯一铁律**：验证文件上传漏洞 = **上传成功 + 访问执行**双重证据。仅看上传响应 200 是漏判——文件可能存到非 web-accessible 目录、被重命名、被二次编码破坏。必须追到"上传后的 URL"+"访问该 URL 的响应"。

**payload 最小化原则**：测试 webshell 用 `<?php echo 'pwn-'.uniqid(); ?>` 这种最小可执行负载，不要塞完整 webshell——服务端常有大小限制 / 二次扫描 / payload 检测。

下面是倾向性建议，不是规则：

- 流量已包含 multipart 上传 → 直接照搬 boundary 改 payload
- 看响应里通常会回显文件名 / URL → **先解析这个 URL** 再决定下一步
- 应用同时返回上传路径和"上传成功"提示 → 高价值场景

## 判定原则（按优先级）

### 1. `.php`/`.jsp`/`.aspx` 直传 + 访问执行 → critical（RCE）

最理想场景。访问上传后 URL 的响应 body 含 webshell payload 的输出（如 `phpinfo` HTML 或自定义 `pwn-<uniqid>`）→ 任意文件上传 → RCE。

### 2. `.php` 直传被拒，但变体通过 → 同样 critical

按以下顺序试扩展名变体直到通过：`.php5` / `.pht` / `.phtml` / `.phar` / `.shtml`（PHP）/ `.jspx` / `.jspf`（JSP）/ `.cer` / `.asa` / `.cdx`（ASP）。大小写变体 `.PhP` / `.PHP` 也常被遗漏。

### 3. 上传成功但访问不执行（存为静态文件）→ high

文件存到 web-accessible 目录但 web 服务器不解析（如配置成只解析特定后缀）。仍可利用：

- Stored XSS（上传 `.html` / `.svg`）
- 钓鱼载体（上传伪装文件）
- 路径污染（上传同名文件覆盖资源）

### 4. 仅能上传真实图片内容（jpg/png 实际像素数据）→ 非漏洞

除非应用使用了有 CVE 的图像库（ImageMagick / GhostScript 等）——属于不同漏洞类，超出本 SKILL 范围。

### 5. `Content-Type: image/jpeg` + body 是 PHP 能绕过类型校验 → 绕过 + 看执行

服务端只看 Content-Type 头不看实际内容是常见弱点。先用这个绕过类型校验，再追"是否能执行"。

## 绕过策略（倾向性建议）

| 绕过类型 | 示例 |
|---|---|
| 扩展名变体 | `.php5` / `.pht` / `.phtml` / `.phar` / `.htaccess` / `.shtml` / `.jspx` / `.cer` |
| 大小写 | `.PhP` / `.PHP` |
| Null byte 截断 | `shell.php%00.jpg`（PHP < 5.3） |
| Double extension | `shell.jpg.php` / `shell.php.jpg` |
| Content-Type 头伪造 | `Content-Type: image/jpeg` + body 是 PHP |
| Magic bytes polyglot | body 开头 `GIF89a;<?php ?>` |
| 路径遍历文件名 | `filename="../../shell.php"`（突破 Web 根目录限制） |
| .htaccess 覆盖 | 上传 `.htaccess` 重新定义 PHP 处理规则 |

## 误报排除

提交 finding 前先排除：

- 上传 200 但后续访问 URL 返回 404（文件未持久化或被立即清理）
- 应用做了二次处理（图片重新编码 / WAF 改写）破坏 payload
- 上传到非 web 路径（如 `/tmp/` / `/uploads/private/`）→ 无法 RCE，但仍可能其他风险
- 文件被存但访问需要鉴权 → 看是否能绕过鉴权

## write_finding 红线

evidence 必须包含**完整三段证据链**：

1. **上传请求**：完整 multipart curl 命令（含 boundary + 实际 payload 内容）
2. **上传响应**：文件名 / 上传后 URL / 服务器返回的提示
3. **访问响应**：用 curl 访问上传后 URL 的响应（含 webshell 输出或 stored XSS reflect 内容）

附加要求：

- 必须列出**使用的绕过手法**（扩展名变体 / Content-Type / polyglot / 路径遍历）
- `repro_cmd` 给一条复合命令：先 curl 上传再 curl 访问，别人 copy 能复现
- 上传的 payload 必须是**最小可证明**版本（如 `<?php echo 'pwn-test'; ?>`），不要塞完整 webshell

summary ≤500 字单行：`Arbitrary File Upload (PHP RCE) in POST /vulnerabilities/upload/ — webshell uploaded + executed via Content-Type bypass`
