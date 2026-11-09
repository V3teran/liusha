# 产物落盘（交付约定，必须遵守）

- **交付物与证据文件写 `$OUTPUT_DIR`**（环境变量已注入，绝对路径）：扫描报告（nmap -oA）、PoC 脚本、下载的样本/页面存证、复现材料。操作员在工作空间页直接查看/下载这些文件。
- 每次命令执行后，系统自动收集 `$OUTPUT_DIR` 顶层新增文件（单次 ≤10MB、≤5 个）并在输出尾部给出 `[out/ 产物]` 清单——看到它说明文件已存档成功。
- 临时文件（管道中间产物、工具自带会话目录）放当前目录即可，不要与交付物混放；需要引用产物内容时 `cat "$OUTPUT_DIR/文件名"`。
- **归档判据**：问一句「这个文件之后还要用吗——复现时要用、报告要引用、操作员要拿走？」要，就写 `$OUTPUT_DIR`；只是让命令跑起来的（会话目录、一次性试错脚本），留在当前目录。

# 输出契约（系统据此解析，必须遵守）

**在完成任务后，你必须输出一个标准的 JSON 对象，总结你的执行结果和发现。**

## 标准输出格式

在 ReAct 循环的最后，输出以下 JSON 格式：

```json
{
  "status": "completed",
  "summary": "执行了 3 个测试，发现 1 个高优先级问题和 1 个中优先级问题",
  "observations": [
    {
      "category": "hypothesis",
      "statement": "后台接口无需认证即可访问",
      "reasoning": "未携带任何认证凭据直接访问，返回成功且含完整数据",
      "test_plan": "匿名访问目标接口，对比带凭证请求；确认响应独有数据",
      "confidence": "high",
      "priority": "high",
      "impact": "任何匿名访问者可拉取全部用户数据",
      "suggestion": "为后台接口增加认证与鉴权校验；敏感数据接口默认拒绝匿名访问，并按最小权限放行。",
      "repro": {
        "domain": "web",
        "recipe": {
          "request": {
            "method": "GET",
            "url": "http://target.com/api/data",
            "headers": {},
            "body": ""
          },
          "baseline": {
            "method": "GET",
            "url": "http://target.com/api/public",
            "headers": {},
            "body": ""
          }
        },
        "assert": {"body_contains": "sensitive_data"}
      }
    }
  ]
}
```

## 字段说明

- **status**: "completed" 或 "failed"（任务执行状态）
- **summary**: 简短总结（1-2 句话）
- **observations**: 观察结果列表（可以为空数组）

每个 observation 必须包含 **category**（七个信息域，见下方「分类纪律」）。

**category="issue"（异常与障碍）**——已观察到的坏事实，写入即终态：
- **statement**: 问题陈述（如"发现 .git 目录泄露"、"CORS 配置为 *"）
- **reasoning**: 为什么这是一个问题
- **test_plan**: 如何验证这个问题（可选）
- **confidence**: "low" / "medium" / "high"
- **priority**: "low" / "medium" / "high" / "critical"
- **impact**: 影响说明（一段自然语言：主张为真会发生什么、波及多大）
- **suggestion**: 处理建议（一段自然语言：怎么做可改变这一局面）——issue/hypothesis 必须给，其余类别不带
- **repro**: 可选（用于复现或深度验证）

**category="entity"/"access"/"structure"/"behavior"/"note"（信息域事实）**——环境知识，写入即终态：
- **statement**: 知识陈述（如"目标开放端口 22, 80, 443"、"技术栈为 nginx 1.18.0"）
- **reasoning**: 依据来自哪个实测输出
- **confidence**: "low" / "medium" / "high"
- 通常不带 **repro**、不带 **priority**

**category="hypothesis"（待验证推测）**——需要通过复现配方验证的推测性断言，只有这类会被独立验证并可能成为正式发现：
- **statement**: 清晰的主张陈述
- **reasoning**: 为什么得出这个主张
- **confidence**: "low" / "medium" / "high"
- **priority**: "low" / "medium" / "high" / "critical"
- **impact**: 影响说明（主张为真会发生什么、波及多大）
- **suggestion**: 处理建议（经验证坐实后随发现交付）
- **repro**: 机器可执行的复现配方（见下文）——**必须有**，无配方的主张无从裁决

**定位由配方派生**：交付层从 repro 首请求自动派生 {method,url,path}——
无需另填定位字段；不同参数注入点在配方 URL/query 里天然不同，不会被
交付去重合并。

分类纪律（七个信息域，领域中立词汇，选最贴切的一个）：
- **entity** 用于存在的单个对象及其属性（主机、端点、服务、页面、配置项，及其版本/路径等关键属性）
- **access** 用于触达与运用对象的途径（入口、认证方式、授权范围、可用凭据、会话规则）
- **structure** 用于对象之间的关系与布局（站点地图、网络拓扑、组件依赖、目录层级）
- **behavior** 用于环境受激后的反应与内在机制（输入如何被处理、状态如何迁移、过滤/转义规律）
- **issue** 用于已观察到的异常与障碍（信息泄露、配置弱点、默认配置、报错、阻塞推进的坑）——看一眼就能判定的坏事实
- **hypothesis** 用于待验证推测（SQL 注入、XSS、RCE、认证绕过）——必须带 repro（没有配方会被拒绝落图）
- **note** 用于以上都不合适的杂项（最后手段——先找具体域）
- entity 与 structure 的分界：单个对象的存在与属性记 entity；对象之间怎么连接、怎么组织记 structure
- issue 与 hypothesis 的分界：需要复现证明才能下结论的判断必须走 hypothesis，不许落在 issue 里偷懒
- 不要把纯过程性记录（"访问了某页"、"请求返回 200"）写成 observation——只记对规划有决策价值的结论

**示例**：

```json
{
  "status": "completed",
  "summary": "发现 .git 目录泄露和技术栈信息",
  "observations": [
    {
      "category": "issue",
      "statement": "发现 .git 目录泄露",
      "priority": "medium",
      "reasoning": "通过访问 /.git/config 确认存在完整 git 仓库"
    },
    {
      "category": "entity",
      "statement": "目标使用 nginx 1.18.0 + PHP 7.4",
      "reasoning": "从 Server 响应头和错误页面确认"
    }
  ]
}
```

## 复现配方（repro）格式

### HTTP 验证（domain: "web"）

```json
{
  "domain": "web",
  "recipe": {
    "request": {
      "method": "GET",
      "url": "http://target.com/api/user?id=1' OR '1'='1",
      "headers": {"Authorization": "Bearer token"},
      "body": ""
    },
    "baseline": {
      "method": "GET", 
      "url": "http://target.com/api/user?id=1",
      "headers": {"Authorization": "Bearer token"},
      "body": ""
    }
  },
  "assert": {
    "body_contains": "mysql_error"
  }
}
```

**关键点**：
1. `recipe.request` = 完整验证请求（能触发主张现象）
2. `recipe.baseline` = 对照请求（可选；提供后差分自动生效——断言须命中验证响应且基线不命中）
3. `assert` = 断言验证响应的**独有特征**

### 通用场景（domain: "generic"）

```json
{
  "domain": "generic",
  "recipe": {
    "steps": "1. 运行命令 X\n2. 观察输出 Y\n3. 确认 Z 存在"
  },
  "assert": {
    "description": "执行后观察到敏感信息泄露"
  }
}
```

## 复现配方规则（重要）

1. **自包含**：repro 必须包含所有信息，evaluator 可以独立复现，不依赖任何外部上下文
2. **完整 URL**：必须包含 `http://` 或 `https://`
3. **实测过**：request 必须是你实际执行过并触发主张现象的请求
4. **精确断言**：assert 必须描述验证响应的**独有特征**，不能是基线响应也会有的特征
5. **避免误报**：
   - ❌ 禁止：`"status_code": 200` + 通用页面标题
   - ✅ 正确：`"body_contains": "mysql_error"`（配 recipe.baseline 让基线差分自动生效）

## 示例：正确的输出

```json
{
  "status": "completed",
  "summary": "扫描了 5 个接口，发现 1 个高优先级问题",
  "observations": [
    {
      "category": "hypothesis",
      "statement": "/api/users 接口的 id 参数存在未净化的输入回显",
      "reasoning": "注入特殊字符导致错误响应，回显了内部错误信息",
      "test_plan": "在 id 参数注入特殊字符，观察是否返回异常数据或错误",
      "confidence": "high",
      "priority": "critical",
      "repro": {
        "domain": "web",
        "recipe": {
          "request": {
            "method": "GET",
            "url": "http://target.local/api/users?id=1'",
            "headers": {},
            "body": ""
          },
          "baseline": {
            "method": "GET",
            "url": "http://target.local/api/users?id=1",
            "headers": {},
            "body": ""
          }
        },
        "assert": {
          "body_contains": "error in your syntax"
        }
      }
    }
  ]
}
```

## 示例：没有发现

```json
{
  "status": "completed",
  "summary": "扫描了 10 个接口，未发现明显问题",
  "observations": []
}
```

## 输出时机

在完成所有测试后，作为你的**最后一条消息**，输出标准 JSON 格式。

**重要**：
- 可以用 Markdown 代码块包裹：\`\`\`json ... \`\`\`
- 也可以直接输出 JSON 对象
- 必须确保 JSON 格式正确
