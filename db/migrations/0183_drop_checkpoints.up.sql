-- 拆除框架层迭代检查点半区。
--
-- 依据（以源码为准）：检查点唯一写入方是 monitor 的 ReAct 循环（每 3 迭代落盘），
-- 全仓无任何读取方——RestoreFromCheckpoint 生产无人设值，Checkpointer.Load/List
-- 从进程入口不可达。而跨进程重启的恢复语义已由探索图承担：action 状态落图，
-- runner 启动时把 running 动作复位（graphstore 恢复路径）并重新认领。
-- 迭代级快照与图级恢复是同一问题的两套机制，保留后者（单一天然源），删除前者。
DROP TABLE IF EXISTS checkpoints;
