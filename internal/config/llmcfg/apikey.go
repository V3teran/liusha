package llmcfg

import (
	"fmt"
	"os"
)

// KeyDecrypter 解密 Provider.EncryptedAPIKey（*cryptx.Cipher 自动满足）。
// llmcfg 包不直接依赖 cryptx——避免持久化层认识加密细节，调用方（internal/llm、internal/provider）
// 在构造 LLM client 那一刻传入具体实现。
type KeyDecrypter interface {
	Decrypt(sealed []byte) (string, error)
}

// ResolveAPIKey 取一个 provider 可用的 API Key 明文。
// 双路径（migration 0103）：EncryptedAPIKey 非空 → 解密返回（当前事实源）；
// 为空 → 回退 os.Getenv(APIKeyEnv)（旧数据兼容，未通过前端重新保存密钥的 provider 仍可用）。
// 两者皆空 → 明确报错，不静默返回空字符串（避免下游拿空 key 打出请求后才报 401）。
func ResolveAPIKey(p Provider, dec KeyDecrypter) (string, error) {
	if len(p.EncryptedAPIKey) > 0 {
		if dec == nil {
			return "", fmt.Errorf("provider %q: 已加密存储密钥但未配置解密器", p.Key)
		}
		key, err := dec.Decrypt(p.EncryptedAPIKey)
		if err != nil {
			return "", fmt.Errorf("provider %q: 解密密钥失败: %w", p.Key, err)
		}
		return key, nil
	}
	if p.APIKeyEnv != "" {
		if key := os.Getenv(p.APIKeyEnv); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("env %s 为空（provider=%s）", p.APIKeyEnv, p.Key)
	}
	return "", fmt.Errorf("provider %q 未配置密钥（既无加密密钥也无 api_key_env）", p.Key)
}
