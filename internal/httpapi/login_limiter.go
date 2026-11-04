package httpapi

import (
	"sync"
	"time"
)

// login_limiter.go：登录/初始化端点的内存防爆破限流。
//
// 三维度计数，任一维度触发锁定即拒绝：
//   - 组合维度（username+IP，阈值 comboLockFails）：常态防爆破——同一来源对同一账号反复试错；
//   - 账号维度（username，阈值 usernameLockFails）：防轮换来源绕过——分布式/伪造 XFF 轮换
//     时组合维度形同虚设，账号维度兜底，保证对单一账号的尝试速率仍有硬上限；
//   - 来源维度（IP，阈值 ipLockFails）：防轮换用户名绕过——每次凭据校验都烧一次 bcrypt
//     （未知用户名走等时 dummy 比对），单源换着用户名打可无限消耗 CPU；来源维度封住
//     单源请求速率，即封住 CPU 消耗上限。
//
// 账号/来源维度阈值刻意高于组合维度：低阈值会让任何人都能源源不断地把管理员锁在门外
// （锁定 DoS）；高阈值下攻击者每窗口仅获得约 usernameLockFails 次猜测（配合 bcrypt
// cost 12 与最短 8 字节密码，这不构成现实爆破面），而管理员自身误输几乎不可能触顶，
// 多账号/共享出口的正常失败也不会误伤。成功登录清零三个维度。
// api 单进程部署，内存态即全量状态；map 增长由 prune 兜底（键空间含攻击者可控的
// 用户名/IP，不能无界增长）。

const (
	comboLockFails    = 5
	usernameLockFails = 20
	ipLockFails       = 30
	loginLockoutTTL   = 15 * time.Minute
	// pruneThreshold 触发清理的条目数上限（远超正常规模，仅防内存滥用）。
	pruneThreshold = 1024
)

type loginFailState struct {
	count       int
	lastFail    time.Time
	blockedTill time.Time
}

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*loginFailState
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: make(map[string]*loginFailState)}
}

func comboKey(username, ip string) string { return "c:" + username + "|" + ip }
func accountKey(username string) string   { return "u:" + username }
func sourceKey(ip string) string          { return "ip:" + ip }

// allow 报告该 username+IP 当前是否放行（三个维度都未处于锁定期）。
func (l *loginLimiter) allow(username, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.unlocked(comboKey(username, ip)) &&
		l.unlocked(accountKey(username)) &&
		l.unlocked(sourceKey(ip))
}

// fail 记一次凭据失败，三个维度同时累计；各自达到阈值即进入锁定期并重新计数。
func (l *loginLimiter) fail(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bump(comboKey(username, ip), comboLockFails)
	l.bump(accountKey(username), usernameLockFails)
	l.bump(sourceKey(ip), ipLockFails)
	if len(l.fails) > pruneThreshold {
		l.pruneLocked()
	}
}

// retryAfter 返回三个维度中最长的剩余锁定秒数；未锁定返回 0。
func (l *loginLimiter) retryAfter(username, ip string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	remaining := 0
	for _, k := range []string{comboKey(username, ip), accountKey(username), sourceKey(ip)} {
		if st := l.fails[k]; st != nil {
			if d := int(time.Until(st.blockedTill).Seconds()) + 1; d > remaining {
				remaining = d
			}
		}
	}
	return remaining
}

// reset 成功登录后清零三个维度。
func (l *loginLimiter) reset(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, comboKey(username, ip))
	delete(l.fails, accountKey(username))
	delete(l.fails, sourceKey(ip))
}

// unlocked 报告键当前未处于锁定期（无条目视为未锁定）。
func (l *loginLimiter) unlocked(key string) bool {
	st := l.fails[key]
	return st == nil || time.Now().After(st.blockedTill)
}

// bump 累计一次失败并在达阈值时进入锁定期。
func (l *loginLimiter) bump(key string, threshold int) {
	st := l.fails[key]
	if st == nil {
		st = &loginFailState{}
		l.fails[key] = st
	}
	st.count++
	st.lastFail = time.Now()
	if st.count >= threshold {
		st.blockedTill = time.Now().Add(loginLockoutTTL)
		st.count = 0
	}
}

// pruneLocked 清理无记忆价值的条目：既不在锁定期、最近一次失败也已超出锁定窗口。
func (l *loginLimiter) pruneLocked() {
	now := time.Now()
	cutoff := now.Add(-loginLockoutTTL)
	for k, st := range l.fails {
		if st.blockedTill.IsZero() || (st.blockedTill.Before(now) && st.lastFail.Before(cutoff)) {
			delete(l.fails, k)
		}
	}
}
