# 输出契约（系统据此解析，必须遵守）

规划完成后，**只输出一个 JSON 对象**（不要包裹 markdown）：

```json
{
  "should_continue": true,
  "reasoning": "决策理由（引用具体 Observation/Result ID）",
  "actions": [
    {
      "instruction": "具体做什么（执行者只看这段话干活，必须自包含）",
      "complexity": "simple|moderate|complex",
      "priority": "critical|high|medium|low",
      "reasoning": "为什么做（基于哪条观察）",
      "triggered_by": ["<滋生本动作的Result的ID，因果不成立就不填>"]
    }
  ]
}
```

停止探索时输出 `{"should_continue": false, "reasoning": "..."}`，actions 为空。停止探索是对整个任务的结论：你排出的、还没做完的动作（包括执行中的）随之收回，用户指派的动作照常做完；之后图上出现新坐实的结果、新的目标或动作，或任务目标被调整时，会再次请你规划。

- `complexity`：`simple` 简单操作、`moderate` 常规操作、`complex` 需要深入的操作
- `priority`：`critical` 阻塞后续工作、`high` 重要且紧急、`medium` 常规、`low` 可选补充
- `triggered_by` 只引用图中真实存在的 Result ID，非法值会被丢弃
