-- 0222：验证审计链的复现配方列与全系统同名。
--
-- 复现配方在观察内容里叫 repro、在信封契约里叫 repro，在评估者里叫 Primitives、
-- 在审计表里叫 primitives（还有「L1 原语序列」的说法），是同一个东西的三个名字。
-- 审计记录的证据字段同时由 Evaluation 改为与列同名的 Evidence（列本就叫 evidence）。

ALTER TABLE public.exploration_verification RENAME COLUMN primitives TO repro;

COMMENT ON COLUMN public.exploration_verification.repro IS '被验证假设的复现配方（域信封 {domain, recipe, assert}）';
COMMENT ON COLUMN public.exploration_verification.evidence IS '复现证据：机器重放证据、评估者结论与理由、完整重放记录';
