-- Migration 0136: 为 wm_node 添加乐观锁版本号
--
-- 目的：支持 GraphStore 的乐观锁并发控制
-- 1. 添加 version 字段（BIGINT，每次更新自增）
-- 2. 创建触发器自动更新 version

-- 1. 添加 version 列（如果不存在，默认值为 1）
-- 注意：字段可能已经存在，使用 IF NOT EXISTS
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'wm_node' AND column_name = 'version'
    ) THEN
        ALTER TABLE wm_node ADD COLUMN version BIGINT NOT NULL DEFAULT 1;
    ELSE
        -- 字段已存在，确保类型和默认值正确
        ALTER TABLE wm_node ALTER COLUMN version TYPE BIGINT;
        ALTER TABLE wm_node ALTER COLUMN version SET NOT NULL;
        ALTER TABLE wm_node ALTER COLUMN version SET DEFAULT 1;
    END IF;
END $$;

-- 2. 创建触发器函数：更新时自动递增 version
CREATE OR REPLACE FUNCTION increment_wm_node_version()
RETURNS TRIGGER AS $$
BEGIN
    NEW.version = OLD.version + 1;
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 3. 创建触发器
DROP TRIGGER IF EXISTS trigger_increment_wm_node_version ON wm_node;
CREATE TRIGGER trigger_increment_wm_node_version
    BEFORE UPDATE ON wm_node
    FOR EACH ROW
    EXECUTE FUNCTION increment_wm_node_version();

-- 4. 创建索引（用于乐观锁查询优化）
CREATE INDEX IF NOT EXISTS idx_wm_node_id_version ON wm_node(id, version);

-- 注释
COMMENT ON COLUMN wm_node.version IS '乐观锁版本号，每次更新自增';
COMMENT ON FUNCTION increment_wm_node_version() IS '自动递增 wm_node.version 字段';
