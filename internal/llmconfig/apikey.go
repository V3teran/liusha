package llmconfig

import "fmt"

// KeyDecrypter 解密 Provider.EncryptedAPIKey（*cryptx.Cipher 自动满足）。
// llmconfig 包不直接依赖 cryptx——避免持久化层认识加密细节，调用方（framework/llm router 装配层、探测适配器）
// 在构造 LLM client 那一刻传入具体实现。
type KeyDecrypter interface {
	Decrypt(sealed []byte) (string, error)
}

// ResolveAPIKey 取一个 provider 的 API Key 明文：前端填写、加密落库的密文解密后返回。
// 未设置密钥 → 明确报错，不静默返回空字符串（避免下游拿空 key 打出请求后才报 401）。
func ResolveAPIKey(p Provider, dec KeyDecrypter) (string, error) {
	if len(p.EncryptedAPIKey) == 0 {
		return "", fmt.Errorf("provider %q 未设置密钥（在「模型配置」页填写）", p.Code)
	}
	if dec == nil {
		return "", fmt.Errorf("provider %q: 已加密存储密钥但未配置解密器", p.Code)
	}
	key, err := dec.Decrypt(p.EncryptedAPIKey)
	if err != nil {
		return "", fmt.Errorf("provider %q: 解密密钥失败: %w", p.Code, err)
	}
	return key, nil
}
