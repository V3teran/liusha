# 结果分析输出契约（系统据此解析，必须遵守）

分析新坐实的结果后，**只输出一个 JSON 对象**（不要包裹 markdown）：

```json
{
  "reasoning": "判断过程：哪些方向已达成、当前方向是否穷尽、为什么提出新方向或延续当前方向",
  "completed_objectives": [
    {"id": "<进行中的派生目标ID>", "evidence_ids": ["<本次分析的Result ID>"]}
  ],
  "new_objectives": [
    {
      "description": "新方向",
      "priority": "critical|high|medium|low",
      "triggered_by": ["<本次分析的Result ID>"],
      "supersedes": ["<进行中的派生目标ID>"],
      "reasoning": "为什么需要这个方向；声明了 supersedes 时说明它如何承接被吸收目标的范围"
    }
  ]
}
```

- 两个数组都可以为空，也可以同时有内容
- `completed_objectives`：宣告达成的派生目标。`id` 只能引用「进行中的派生目标」里的 ID，`evidence_ids` 只能引用本次分析的 Result ID；任务目标（根目标）不可宣告达成
- `new_objectives`：新提出的派生目标。`supersedes` 填被它吸收的进行中派生目标 ID（细化、拆分或纠正既有方向；拆分时每个新方向都填）；被吸收目标的范围由新方向承担，不是放弃；任务目标不可被吸收
- 引用了不存在或不可引用的 ID 时，该引用会被剔除
