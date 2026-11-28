-- 上下文窗口与单次最大输出的缺省取当前旗舰模型的官方上限（1M / 128K），与代码里唯一的出处
-- （llmconfig.DefaultMaxTokens / DefaultContextWindow）同值——internal/dbtest 的迁移测试对照两处。
-- 此前的 4096 是旧值：推理模型的思考计入输出，规划者一次输出的思考加 JSON 轻易超过它，
-- 实测一个按它建起的 provider 上 19% 的规划输出被截断。
--
-- 存量为 0（未设置）的行落成缺省：配置入口已把 0 落成缺省值，库里不再有「未设置」的行，
-- 各级缓存读到的都是确定的正数。已填的值不动——那是各 provider 的具体配置。
ALTER TABLE llm_provider ALTER COLUMN max_tokens SET DEFAULT 131072;
ALTER TABLE llm_provider ALTER COLUMN context_window SET DEFAULT 1048576;

UPDATE llm_provider SET max_tokens = 131072 WHERE max_tokens = 0;
