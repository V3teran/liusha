-- 回滚 0185：恢复 depends_on 列（历史数据已随 up 丢弃，回滚只恢复结构）。
ALTER TABLE finding ADD COLUMN IF NOT EXISTS depends_on uuid[] DEFAULT '{}';
