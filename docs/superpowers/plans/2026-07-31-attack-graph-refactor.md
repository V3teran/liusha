# 执行图（Attack Graph）重构计划

## 一、诊断结论

工程手艺不差：纯函数分层（`project.go` 无 IO）、窄接口注入
（`MessageLister` / `FindingLister` / `ConvResolver` / `Summarizer`）、测试覆盖到位。
问题不在代码手艺，在**数据模型的抽象层次选错了一档**。

图节点与 agent 事件是机械的一对一映射：

```
message(event, Kind=reasoning)  → 1 个「想」节点
message(event, Kind=tool_call)  → 1 个「做」节点
edge                            → 时序上的 parent_