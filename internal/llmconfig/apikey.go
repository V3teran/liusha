package llmconfig

import (
	"fmt"
	"os"
)

// KeyDecrypter 解密 Provider.EncryptedAPIKey（*cryptx.Cipher 自动满足）。
// llmconfig 包不直接依赖 cryptx——避免持久化层认识加密细节，调用方（framework/llm router 装配层、探测适配器）
// 在构造 LLM client 那一刻传入具体实现。
type KeyDecrypter interface {
	Decrypt(sealed []byte) (string, error)
}

// ResolveAPIKey 取一个 provider 可用的 API Key 明文。
// 双路径，前端优先：EncryptedAPIKey 非空（前端直填，AES-GCM 加密落库）→ 解密返回；
// 为空 → 读 os.Getenv(APIKeyEnv)（部署期路径：config.yaml providers 块钉定变量名，.env 放同名 key）。
// 两者皆空 → 明确报错，不静默返回空字符串（避免下游拿空 key 打出请求后才报 401）。
func ResolveAPIKey(p Provider, dec KeyDecrypter) (string, error) {
	if len(p.EncryptedAPIKey) > 0 {
		if dec == nil {
			return "", fmt.Errorf("provider %q: 已加密存储密钥但未配置解密器", p.Code)
		}
		key, err := dec.Decrypt(p.EncryptedAPIKey)
		if err != nil {
			return "", fmt.Errorf("provider %q: 解密密钥失败: %w", p.Code, err)
		}
		return key, nil
	}
	if p.APIKeyEnv != "" {
		if key := os.Getenv(p.APIKeyEnv); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("env %s 为空（provider=%s）", p.APIKeyEnv, p.Code)
	}
	return "", fmt.Errorf("provider %q 未配置密钥（既无加密密钥也无 api_key_env）", p.Code)
}
