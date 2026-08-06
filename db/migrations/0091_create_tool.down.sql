-- 0091 down: 删工具目录表（代码为事实源，删表不丢数据，重启 reconcile 会重建）。
DROP TABLE IF EXISTS tool;
