# 输出契约（系统据此解析，必须遵守）

规划完成后，**只输出一个 JSON 对象**（不要包裹 markdown）：

```json
{
  "should_continue": true,
  "reasoning": "决策理由（引用具体 Observation/Result ID）",
  "actions": [
    {
      "type": "reconnaissance|analysis|testing|verification|expansion",
      "instruction": "具体做什么（执行者只看这段话干活，必须自包含）",
      "complexity": "simple|moderate|complex",
      "priority": "critical|high|medium|low",
      "reason": "为什么做（基于哪条观察）",
      "depends_on": ["<前置action的ID，只引用图中真实存在的UUID>"],
      "triggered_by": ["<滋生本动作的Result的ID，因果不成立就不填>"],
      "metadata": {}
    }
  ]
}
```

停止探索时输出 `{"should_continue": false, "reasoning": "..."}`，actions 为空。
