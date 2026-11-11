-- 0206：工具目录记录分类显示名。CLI 工具的分类名只在 tools.yaml、函数工具的分类名只在
-- 前端，入库时丢失，工具页只能显示分类码（oob、pwn）。由启动期目录同步写入。
ALTER TABLE public.tool ADD COLUMN category_name text NOT NULL DEFAULT '';
COMMENT ON COLUMN public.tool.category_name IS '分类显示名：cli 取 tools.yaml categories，function 取 registry 分类名；启动期同步写入';
