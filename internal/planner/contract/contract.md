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
      "serves": "<本动作推进的方向：输入「进行中的方向」里的编号（如 D1）；不属于任何方向就不填>",
      "pursues": ["<本动作跟进的线索：输入「待深挖的线索」里的编号（如 L1）>"]
    }
  ],
  "leads": [
    {"id": "<线索编号>", "decision": "attach", "action": "<挂到哪个在办动作：「进行中的工作」里的编号（如 A2）>"},
    {"id": "<线索编号>", "decision": "close", "kind": "<not_worth|out_of_scope|premise_refuted|exhausted|blocked>", "reason": "<关闭的理由>"},
    {"id": "<线索编号>", "decision": "leave"}
  ]
}
```

停止探索时输出 `{"should_continue": false, "reasoning": "..."}`，actions 与 leads 为空。停止探索是对整个任务的结论：你排出的、还没做完的动作（包括执行中的）随之收回，用户指派的动作照常做完；之后图上出现新坐实的结果、新的目标或动作，或任务目标被调整时，会再次请你规划。

- `complexity`：`simple` 简单操作、`moderate` 常规操作、`complex` 需要深入的操作
- `priority`：`critical` 阻塞后续工作、`high` 重要且紧急、`medium` 常规、`low` 可选补充
- `triggered_by` 只引用图中真实存在的 Result ID，非法值会被丢弃

**线索**：输入「待深挖的线索」里的每一条，本轮恰交代一次——被本轮某个动作的 `pursues` 引用，或在 `leads` 里有一条处置，两者只占其一。一条线索本轮只由一个动作跟进（只被一个动作的 `pursues` 引用）；同一方向上的其余动作用 `serves` 归到方向下即可。没有交代的，系统会追问一次。

- `pursues`：这个动作沿该线索推进到结论，线索由它的结局结算。同一陈述的线索在输入里并成一行，引用其中任意一个编号即指这一行上的全部
- `attach`：在办的动作本来就在这条线索上，把线索挂到它上。只记账，不改那个动作；只能挂到在办（待执行或执行中）的动作上。已完成的动作覆盖了这条线索的，改用 `close`
- `close`：`kind` 取 `not_worth`（并不是一条走得通的路，或价值够不上一个动作）、`out_of_scope`（超出任务目标）、`premise_refuted`（前提已被证伪）、`exhausted`（跟进过且已走到头，或已被之前的动作覆盖——理由里引用那个动作）、`blocked`（想跟进但受阻，理由写明受阻于什么）；`reason` 必填
- `leave`：本轮不跟进，线索继续待深挖

**方向**：一个动作只有一个归属。填了 `pursues` 的动作挂在线索下，它的 `serves` 写在这些线索上（改写线索的方向归属，一条线索只改一次）；没有 `pursues` 的动作按 `serves` 挂到方向下，不填则留在任务目标下。`serves` 引用的不是「进行中的方向」里的编号时，按不归属方向处理。
