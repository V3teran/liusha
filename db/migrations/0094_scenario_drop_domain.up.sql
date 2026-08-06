-- 0094: 删除场景交战域概念——删除 scenario.domain 列。
--   场景不再携带交战域标签；工具装配走智能体 cli_tools 严格白名单，与场景解耦。
ALTER TABLE scenario DROP COLUMN domain;
