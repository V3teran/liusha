-- 0093: 工具目录去场景挂钩——删除 tool.scenarios 列。
--   工具装配改走智能体 cli_tools 严格白名单（所选即所得），tools.yaml 不再与场景绑定。
ALTER TABLE tool DROP COLUMN scenarios;
