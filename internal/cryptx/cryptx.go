// Package cryptx 提供落库敏感值（当前：LLM provider API Key）的对称加密。
//
// AES-256-GCM：nonce 随机生成、随密文一起存（GCM 标准做法，nonce 不是秘密），
// 输出格式为 nonce || ciphertext（ciphertext 含 GCM tag）单一 []byte，落 bytea 列。
//
// 密钥来自 LIUSHA_LLM_KEY_SECRET 环境变量（32 字节，hex 编码，同 LIUSHA_STREAM_COOKIE_SECRET
// 的 fail-fast 模式——启动期由 cmd/api 校验非空且长度合法，缺失直接拒启动，不静默退化）。
package cryptx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// KeySize 是 AES-256 密钥字节数；调用方（cmd/api）据此校验 LIUSHA_LLM_KEY_SECRET hex 解码后的长度。
const KeySize = 32

// Cipher 封装一把 AES-256-GCM 密钥，对外只暴露 Encrypt/Decrypt——调用方不接触 nonce 细节。
type Cipher struct {
	gcm cipher.AEAD
}

// NewFromEnv 读 envVar（hex 编码，32 字节）构造 Cipher——三个 main.go 入口
// （cmd/api、cmd/runner、cmd/corpus-import）共用同一套 fail-fast 校验，不各写一份。
// 生成密钥：openssl rand -hex 32。
func NewFromEnv(envVar string) (*Cipher, error) {
	hexKey := os.Getenv(envVar)
	if hexKey == "" {
		return nil, fmt.Errorf("cryptx: env %s 为空", envVar)
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("cryptx: env %s hex 解码失败: %w", envVar, err)
	}
	return New(key)
}

// New 用 32 字节密钥构造 Cipher；密钥长度不对直接报错（调用方应在启动期校验，此处双保险）。
func New(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("cryptx: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cryptx: new AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptx: new GCM: %w", err)
	}
	return &Cipher{gcm: gcm}, nil
}

// Encrypt 加密明文，返回 nonce||ciphertext（单一 []byte，直接落 bytea 列）。
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("cryptx: read nonce: %w", err)
	}
	return c.gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt 解出 Encrypt 产出的 nonce||ciphertext，还原明文。
func (c *Cipher) Decrypt(sealed []byte) (string, error) {
	n := c.gcm.NonceSize()
	if len(sealed) < n {
		return "", fmt.Errorf("cryptx: sealed value too short (%d bytes)", len(sealed))
	}
	nonce, ciphertext := sealed[:n], sealed[n:]
	plaintext, err := c.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("cryptx: decrypt: %w", err)
	}
	return string(plaintext), nil
}
