-- 0090: agent.tools 更名 function_tools，与 cli_tools 语义对称、消除歧义。
--   · function_tools：内置函数工具（进程内原生函数，run_command/write_finding… 的 code 列表）
--   · cli_tools     ：外置 CLI 工具白名单（tools.yaml 名字）
-- 二者是两套工具体系；旧 tools 命名与 cli_tools 区分不清，故全链路统一。
ALTER TABLE agent RENAME COLUMN tools TO function_tools;

COMMENT ON COLUMN agent.function_tools IS '内置函数工具集（进程内原生函数 code 列表），与外置 cli_tools 分列';
