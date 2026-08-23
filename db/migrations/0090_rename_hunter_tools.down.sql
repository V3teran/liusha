-- 0090 down: 还原 function_tools → tools。
ALTER TABLE agent RENAME COLUMN function_tools TO tools;
