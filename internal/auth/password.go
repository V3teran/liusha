// Package auth 提供控制面登录认证：管理员账号（bcrypt 密码哈希）与登录会话
// （随机 token，库中只存其 SHA-256）。HTTP 层的会话 Cookie 签发/校验见 internal/httpapi。
package auth

import (
	"fmt"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost 是密码哈希强度。12 在现代硬件上约数百毫秒一次，对单管理员低频
// 登录无感知成本，对拿到哈希后的离线爆破是实质代价。
const bcryptCost = 12

// HashPassword 生成 bcrypt PHC 哈希（内含盐与 cost，直接落库）。
// 明文长度必须在调用前校验（bcrypt 上限 72 字节，超出即报错）。
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: 生成密码哈希: %w", err)
	}
	return string(h), nil
}

// CheckPassword 校验明文密码是否与哈希匹配；不匹配返回非 nil error。
func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// DummyCompare 在用户不存在时执行一次与真实校验同量级的 bcrypt 比对，
// 使登录失败路径的耗时与用户存在时一致，关闭按响应时间枚举用户名的侧信道。
// 哈希串惰性生成（进程生命周期内一次），不阻塞启动。
func DummyCompare(password string) {
	dummyOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword([]byte("liusha-timing-equalizer"), bcryptCost)
		if err == nil {
			dummyHash = string(h)
		}
	})
	if dummyHash != "" {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
	}
}
