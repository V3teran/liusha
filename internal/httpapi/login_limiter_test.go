package httpapi

import (
	"fmt"
	"testing"
	"time"
)

// TestLoginLimiter_ComboDimension 验证组合维度：同 username+IP 连续 5 次失败锁定该组合，
// 换 IP 即新组合（各自独立计数），成功登录清零。
func TestLoginLimiter_ComboDimension(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < comboLockFails; i++ {
		if !l.allow("admin", "1.1.1.1") {
			t.Fatalf("第 %d 次失败前应放行", i+1)
		}
		l.fail("admin", "1.1.1.1")
	}
	if l.allow("admin", "1.1.1.1") {
		t.Fatal("达阈值后同组合应锁定")
	}
	if l.retryAfter("admin", "1.1.1.1") <= 0 {
		t.Fatal("锁定中 retryAfter 应大于 0")
	}
	if !l.allow("admin", "2.2.2.2") {
		t.Fatal("换源 IP 是新组合，不应被原组合锁定连坐")
	}

	l.reset("admin", "1.1.1.1")
	if !l.allow("admin", "1.1.1.1") {
		t.Fatal("成功登录应清零组合维度")
	}
}

// TestLoginLimiter_AccountDimension 验证账号维度：轮换源 IP 绕过组合维度后，
// 同一账号累计 20 次失败仍会被硬锁定（所有来源一律拒绝）。
func TestLoginLimiter_AccountDimension(t *testing.T) {
	l := newLoginLimiter()
	// 每源 4 次（不触组合阈值 5），5 个源累计 20 = 账号阈值。
	for i := 0; i < 5; i++ {
		ip := fmt.Sprintf("10.0.0.%d", i+1)
		for j := 0; j < 4; j++ {
			if !l.allow("admin", ip) {
				t.Fatalf("组合未达阈值却被拒（ip=%s, 第 %d 次）", ip, j+1)
			}
			l.fail("admin", ip)
		}
	}
	if l.allow("admin", "9.9.9.9") {
		t.Fatal("账号维度达阈值后，任何来源都应被拒")
	}
	if l.allow("admin", "8.8.8.8") {
		t.Fatal("账号锁定对新来源同样生效")
	}
	// 其他账号不受连坐。
	if !l.allow("other", "1.1.1.1") {
		t.Fatal("账号维度只锁目标账号，不得连坐其他账号")
	}
}

// TestLoginLimiter_SourceDimension 验证来源维度：单源轮换用户名打（组合/账号维度
// 键恒新、恒放行）累计 30 次后，该来源任何用户名都被拒——封死未鉴权端点上
// 无限烧 bcrypt CPU 的路径；换源不受连坐。
func TestLoginLimiter_SourceDimension(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < ipLockFails; i++ {
		username := fmt.Sprintf("spray-user-%d", i)
		if !l.allow(username, "6.6.6.6") {
			t.Fatalf("第 %d 个用户名在来源锁定前应放行", i+1)
		}
		l.fail(username, "6.6.6.6")
	}
	if l.allow("brand-new-user", "6.6.6.6") {
		t.Fatal("来源达阈值后，换任何用户名都应被拒")
	}
	if !l.allow("anyone", "7.7.7.7") {
		t.Fatal("来源维度只锁该来源，不得连坐其他来源")
	}
}

// TestLoginLimiter_LockoutExpiry 验证锁定期过后自动恢复（无需后台任务）。
func TestLoginLimiter_LockoutExpiry(t *testing.T) {
	l := newLoginLimiter()
	lf := l.fails[accountKey("admin")] // 未触发前为 nil——直接构造已锁条目验证过期语义
	if lf != nil {
		t.Fatal("未失败过不应有条目")
	}
	l.mu.Lock()
	l.fails[accountKey("admin")] = &loginFailState{blockedTill: time.Now().Add(-time.Second)}
	l.mu.Unlock()
	if !l.allow("admin", "1.1.1.1") {
		t.Fatal("锁定期已过应恢复放行")
	}
}
