-- 0089 down: 还原剧本层与列变更（按 FK 依赖顺序重建）。

-- ③ 回滚 hunter.cli_tools
ALTER TABLE hunter DROP COLUMN cli_tools;

-- ② 回滚 scenario.solo_hunter_id
DROP INDEX IF EXISTS scenario_solo_hunter_idx;
ALTER TABLE scenario DROP CONSTRAINT IF EXISTS scenario_solo_hunter_ck;
ALTER TABLE scenario DROP COLUMN solo_hunter_id;

-- ① 重建 playbook / playbook_hunter（DDL 取自 0086）
CREATE TABLE playbook (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code         text        NOT NULL UNIQUE,
    name         text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    enabled      boolean     NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE playbook_hunter (
    playbook_id  uuid  NOT NULL REFERENCES playbook(id) ON DELETE CASCADE,
    hunter_id    uuid  NOT NULL REFERENCES hunter(id)   ON DELETE RESTRICT,
    position     int   NOT NULL DEFAULT 0,
    PRIMARY KEY (playbook_id, hunter_id)
);
CREATE INDEX playbook_hunter_playbook_idx ON playbook_hunter (playbook_id, position);

-- scenario 恢复 playbook_id（NOT NULL 需空库前提）
ALTER TABLE scenario ADD COLUMN playbook_id uuid NOT NULL REFERENCES playbook(id) ON DELETE RESTRICT;
CREATE INDEX scenario_playbook_idx ON scenario (playbook_id);
