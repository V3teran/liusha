# 产物落盘（交付约定，必须遵守）

- **交付物与证据文件写 `$OUTPUT_DIR`**（环境变量已注入，绝对路径）：工具生成的报告、编写的脚本、下载的原始材料、复现材料。操作员在工作空间页直接查看/下载这些文件。
- 每次命令执行后，系统自动收集 `$OUTPUT_DIR` 顶层新增文件（单次 ≤10MB、≤5 个）并在输出尾部给出 `[out/ 产物]` 清单——看到它说明文件已存档成功。
- 临时文件（管道中间产物、工具自带会话目录）放当前目录即可，不要与交付物混放；需要引用产物内容时 `cat "$OUTPUT_DIR/文件名"`。
- **归档判据**：问一句「这个文件之后还要用吗——复现时要用、报告要引用、操作员要拿走？」要，就写 `$OUTPUT_DIR`；只是让命令跑起来的（会话目录、一次性试错脚本），留在当前目录。

# 输出契约（系统据此解析，必须遵守）

执行结束时，作为**最后一条消息**输出一个 JSON 对象（可以包在 ```json 代码块里），系统解析后把观察落进探索图：

```json
{
  "status": "completed",
  "summary": "一两句话的执行总结",
  "observations": [
    {
      "category": "hypothesis",
      "statement": "清晰、具体的主张",
      "reasoning": "依据来自哪个实测输出",
      "priority": "high",
      "impact": "主张为真会发生什么、波及多大",
      "suggestion": "处理建议",
      "repro": {"kind": "steps", "recipe": {"steps": "..."}, "assert": {"description": "..."}}
    },
    {
      "category": "access",
      "statement": "admin/password 能登进后台，左侧有 8 个没测过的模块",
      "reasoning": "依据来自哪个实测输出",
      "lead": {
        "next": "用这个会话把其余 8 个模块各测一遍",
        "question": "security=low 下这些模块是否同样可利用",
        "potential": "每多一个可利用的模块就多一条可坐实的主张"
      }
    }
  ]
}
```

- **status**：`completed` 或 `failed`
- **summary**：简短总结
- **observations**：观察列表，可以为空数组；每条必须有 **category**

## 观察分类

| category | 含义 | 生命周期 |
|---|---|---|
| `hypothesis` | 可交付的主张：任务要交付的那类结论（什么算可交付由任务场景规定） | 写入即待验证，经复现门独立验证，坐实后成为交付的结果 |
| `entity` | 存在的单个对象及其属性（名称、版本、位置等） | 写入即终态 |
| `access` | 触达与运用对象的途径（入口、认证方式、授权范围、可用凭据、会话规则） | 写入即终态 |
| `structure` | 对象之间的关系与布局（层级、拓扑、依赖） | 写入即终态 |
| `behavior` | 环境受激后的反应与内在机制（输入如何被处理、状态如何迁移、限制与拦截） | 写入即终态 |
| `note` | 以上都不合适的杂项（最后手段） | 写入即终态 |

- **hypothesis** 必须带 `statement`、`reasoning`、`priority`（`low` / `medium` / `high` / `critical`）、`impact`、`suggestion` 与 `repro`；没有配方的假设无从验证，会被降级为 note
- 事实类（entity / access / structure / behavior / note）带 `statement` 与 `reasoning`，可另带 `lead`；不带 repro、priority、impact、suggestion
- `lead` 是线索，可选：这条事实揭示了一个意外发现的方向——值得认领动作沿它推进、有希望带来重大进展（什么样的发现配得上线索见章程）。`next` 是沿它再走一步具体做什么，`question` 是走完要弄清的结论，`potential` 是走通了能换来什么；三样都写才是线索，缺任何一样按普通事实写入。仅事实类可带，hypothesis 不带
- 分界：可交付的主张写 hypothesis，事实只陈述环境是什么样、怎么反应；单个对象的存在与属性记 entity，对象之间怎么连接、怎么组织记 structure
- 纯过程记录（「执行了某命令」「请求返回成功」）不是观察

## 复现配方（repro）

配方是信封 `{kind, recipe, assert}`：`kind` 说明配方的形状，评估者按它复现。配方必须**自包含**——评估者没有你的上下文，只凭配方就能独立复现；并且必须是你**实测过**、确实触发了主张现象的。

### kind = "http"：一个 HTTP 请求

复现通道原样重发请求，按断言判定；可带一个对照请求。

```json
{
  "kind": "http",
  "recipe": {
    "request":  {"method": "GET", "url": "http://host.local/items/42", "headers": {}, "body": ""},
    "baseline": {"method": "GET", "url": "http://host.local/items/41", "headers": {}, "body": ""}
  },
  "assert": {"body_contains": ["\"owner\":"]}
}
```

- `recipe.request`：完整的验证请求，四个字段齐全（`headers` 可为 `{}`、`body` 可为 `""`），`url` 带 `http://` 或 `https://`
- `recipe.baseline`（可选）：对照请求。提供后差分自动生效——断言须命中验证响应、且对照响应不命中
- `assert`：验证响应的**独有特征**，至少一条，全部满足才算复现：
  - `status_code`：响应码等于此值
  - `body_contains` / `body_not_contains`：响应体含全部子串 / 不含任一子串（子串至少 4 个字符）
  - `header_contains`：响应头存在并含子串（子串为空即只要求该头存在）
  - `header_not_contains`：响应头不存在或不含子串（子串为空即要求该头不存在）
  - `min_duration_ms`：响应耗时至少 N 毫秒
- 只描述目标自身某种属性、没有触发输入的主张，不带 baseline，断言直接描述该属性
- 单独的 `status_code`、页面常态内容（通用标题、导航文字）在任何响应里都会出现，不是独有特征

### kind = "steps"：自然语言步骤

没有机器复现通道：评估者按步骤自主执行取证，对照判据判定。

```json
{
  "kind": "steps",
  "recipe": {"steps": "1. 执行操作 A\n2. 观察输出 B\n3. 确认 C 出现"},
  "assert": {"description": "执行步骤后观察到 C，且不做操作 A 时不会出现"}
}
```

- `steps`：照着就能从头复现的完整步骤
- `assert.description`：坐实判据——观察到什么才算主张成立，以及正常情况下为什么不会出现
