---
name: arjun
category: discovery
description: HTTP 隐藏参数发现——爆 25k+ 参数名 wordlist 找接口里没文档化但实际生效的参数。挖隐藏的 admin/debug/role/test 类管理面参数神器；但误报多，慎用。
---

# arjun 项目特定约束

## 沙箱环境

- **网络**：访问宿主用 `host.docker.internal`。
- **超时**：单 endpoint 跑 25k 参数 ~2-5min，跨 GET/POST/JSON 三种位置，权衡 react step 预算。
- **输出**：`-oJ /tmp/arjun.json` JSON 输出 + `-q` quiet；不加 -q 进度条占 stdout。

## 项目策略

- **基本调用**：`arjun -u http://host.docker.internal:4280/api/x -m GET -oJ /tmp/arjun.json -q`
- **`-m GET/POST/JSON/XML`**：默认 GET，要测多种位置时分别跑；POST/JSON 命中率比 GET 更高（管理面常用 POST）
- **`-w <wordlist>`**：默认内置 25k；要更激进可用 `/opt/SecLists/Discovery/Web-Content/burp-parameter-names.txt`
- **`--passive`**：从 wayback machine 拉历史参数 —— 公网目标有用，本地靶场无意义
- **慎用条件**：流量已含 5+ 参数 → 先把已知参数挖透；接口返 401/403 → 先解决鉴权

## 写 finding 红线

arjun 找到的"隐藏参数"**单独不构成 finding**——必须用 curl 复跑确认参数生效（响应有差异）+ 喂给 sqlmap/dalfox 测漏 **才能**产 finding。常见高价值参数：`debug`/`admin`/`role`/`test`/`mode=dev`/`_method`。

## 决策边界（什么时候**不要**用 arjun）

- LLM 凭常识能想到的 10 个高价值参数（admin/debug/role/test/format/verbose/_method/proxy/url）→ 直接用 curl 试，10s 比 arjun 5min 快
- 流量本身参数复杂（5+ 参数）→ 先挖已知参数
- 接口返非 2xx → 先解决鉴权/路径问题
- 单纯找注入 → 直接 sqlmap/dalfox 拿现有参数测，不需先发现新参数
