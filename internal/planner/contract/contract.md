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
      "triggered_by": ["<滋生本动作的Result的ID，因果不成立就不填>"],
      "pursues": ["<本动作跟进的线索：输入「待深挖的线索」里的编号（如 L1）>"],
      "direction": "<本动作在哪个进行中的方向下排出：输入「进行中的方向」里的编号（如 D2）；直接为任务目标排出的不填>"
    }
  ],
  "leads": [
    {"id": "<线索编号>", "decision": "close", "kind": "<not_worth|out_of_scope|premise_refuted|exhausted|blocked>", "reason": "<关闭的理由>"},
    {"id": "<线索编号>", "decision": "leave"}
  ]
}
```

停止探索时输出 `{"should_continue": false, "reasoning": "..."}`，actions 与 leads 为空。停止探索是对整个任务的结论：你排出的、还没做完的动作（包括执行中的）随之收回，用户指派的动作照常做完；之后图上出现新坐实的结果、新的目标或动作，或任务目标被调整时，会再次请你规划。

- `complexity`：`simple` 简单操作、`moderate` 常规操作、`complex` 需要深入的操作
- `priority`：`critical` 阻塞后续工作、`high` 重要且紧急、`medium` 常规、`low` 可选补充
- `triggered_by` 只引用图中真实存在的 Result ID，非法值会被丢弃
- `direction`：动作挂在它所属的方向下。只引用「进行中的方向」里的编号，无效的引用被丢弃，动作改挂在任务目标下

**线索**：输入「待深挖的线索」里的每一条，本轮恰交代一次——被本轮某个动作的 `pursues` 引用，或在 `leads` 里有一条处置，两者只占其一。一条线索本轮只由一个动作跟进（只被一个动作的 `pursues` 引用）。没有交代的，系统会追问一次。

- `pursues`：这个动作沿该线索推进到结论，线索由它的结局结算。同一陈述的线索在输入里并成一行，引用其中任意一个编号即指这一行上的全部
- `close`：`kind` 取 `not_worth`（并不是一条走得通的路，或价值够不上一个动作）、`out_of_scope`（超出任务目标）、`premise_refuted`（前提已被证伪）、`exhausted`（跟进过且已走到头，或之前已完成的动作已经做过——理由里引用那个动作）、`blocked`（想跟进但受阻，理由写明受阻于什么）；`reason` 必填
- `leave`：本轮不跟进，线索继续待深挖
