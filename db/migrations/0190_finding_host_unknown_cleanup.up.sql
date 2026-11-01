-- 历史数据清理：host 列的 "unknown" 占位符退役（deriveHost 回退链升级后
-- 抽不到线索落空串）。空 = 诚实的「无此维度」，占位符是伪装的数据。
UPDATE finding SET host = '' WHERE host = 'unknown';
