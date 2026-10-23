-- 收紧边关系枚举到实际生产集：generates / depends_on / triggers。
-- 删除的四个值（confirms / refutes / belongs_to / enables）自引入起零生产者、
-- 零存量行——语义由 triggers（发现滋生动作/新方向）与 depends_on（动作间执行
-- 依赖）完整承担，保留只会成为无兑现的混淆源。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge ADD CONSTRAINT ck_exploration_edge_rel
    CHECK (rel = ANY (ARRAY['generates'::text, 'depends_on'::text, 'triggers'::text]));
