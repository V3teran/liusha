# 结果分析输出契约（系统据此解析，必须遵守）

分析新坐实的结果后，**只输出一个 JSON 对象**（不要包裹 markdown）：

```json
{
  "reasoning": "判断过程：这些结果揭示了什么、要不要提出新方向、为什么",
  "new_objectives": [
    {
      "description": "新方向",
      "priority": "critical|high|medium|low",
      "triggered_by": ["<本次分析的Result ID>"],
      "reasoning": "为什么需要这个方向"
    }
  ]
}
```

- `new_objectives` 可以为空：没有要提出的新方向时就是空的
- `triggered_by` 只引用本次分析的 Result ID，非法值会被丢弃
