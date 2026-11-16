-- 0231：provider 密钥只剩一种来源——前端填写、加密落库。
--
-- 曾有的「环境变量名」路径（api_key_env）只由配置文件里的 LLM 种子写入，种子已删除：它只在空库
-- 时导入一次，与「改配置文件、重启生效」矛盾，且与前端「LLM 配置」页重复。删掉这一列，沙盒也不
-- 再需要连库读取「哪些变量是 LLM 密钥」来清洗命令环境。
--
-- 存量：只靠环境变量取密钥（encrypted_api_key 为空）的 provider 升级后显示「未设置密钥」，
-- 在前端重新填写一次即可；升级须知里写明。

ALTER TABLE public.llm_provider DROP COLUMN api_key_env;

COMMENT ON COLUMN public.llm_provider.encrypted_api_key IS 'AES-256-GCM 密文（nonce||ciphertext），密钥来自配置 llm.encryption_key；为空表示尚未设置密钥';
