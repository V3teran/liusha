# 项目约定

## 凭证库（credential vault）

- 凭证存储用 Redis（单一事实源），**禁止任何形式的 TTL/过期机制**——不要 field TTL（HEXPIRE）、不要 key EXPIRE、不要 value 内自管过期时间。
- 凭证键 = `host[:port]`（**端口参与隔离**）：同一主机的不同端口是不同应用/不同源，凭证不跨端口共享。Cookie 协议不隔离端口是浏览器实现细节，凭证库按目标应用（端点）建模。
- `username` 必须是**真实登录用户名**（如 admin）——禁止 LLM 自造代号（admin-low、xxx_r2 之类）；同一账号更新既有条目而非另起新名。
- 凭证生命周期只用**明确事件**管理：任务终态清理、启动期兜底清扫、人工删除（API/UI）。
- 判定「一条凭证该不该删」的依据是事实（所属任务是否已终态），不是猜测（过了多久）。

## 探索图与四 Agent

- 探索图写权限不变式：planner 写 objective/action，executor 写 observation，evaluator 只写 result。
- 生产者（executor）与消费者（evaluator）的 repro 契约以 `internal/repro` 归一化器为唯一权威，两条入口（事件/轮询）行为必须一致。
