-- 0154: skill.code 统一为裸名（目录名），与 agent.skills 声明对齐
--
-- 背景：seed 曾把 code 写成 "<category>/<name>"（如 tooling/browser-use），
-- 而 agent.skills 声明与运行时 read_skill 寻址都是裸名（browser-use）。
-- 本次改造后 DB 是 skill 运行时事实源，code 必须与寻址名一致；
-- 分类语义由 category 列继续承载。
--
-- 防御：若已存在同裸名的用户自建行（理论不会发生——前端 skill UI 尚未上线），
-- 该内置前缀行保持原 code 不动（避免 UNIQUE 冲突），由 reseed 重建收敛。

UPDATE skill
SET code = split_part(code, '/', 2), updated_at = now()
WHERE code LIKE '%/%'
  AND NOT EXISTS (
      SELECT 1 FROM skill t
      WHERE t.code = split_part(skill.code, '/', 2)
  );
