package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// 上游要求等待的时长在一分钟以内，是瞬时限流：等完接着发，与一次普通的退避无异。超过
// 一分钟就是额度窗口（额度按五小时、按天重置）：重试等不来恢复，按退避表重试只会在恢复
// 前耗尽次数，白发一堆注定被拒的请求（实测：额度重置前的 36 分钟内盲目重试了 242 次）。
//
// 它同时是一次调用在额度窗口内静默等待的上限。不再长，是因为调用方拿到错误后会立即
// 重新认领（如执行者交还动作即触发补位），调用层若即刻返回，就成了认领、失败、交还的
// 空转；等满一个周期再交还，循环既慢，期间也不发请求。
const defaultQuotaWindow = time.Minute

// defaultQuotaProbeEvery 是额度窗口内放行探测的间隔。上游告知的恢复时刻只是它的说法：
// 额度可能提前到账，时刻也可能不准。每隔这么久放行一次真实请求，上游已恢复就当场解除
// 等待；没恢复也只多花一个请求。
const defaultQuotaProbeEvery = 5 * time.Minute

// cooldown 登记各账户被上游告知的额度窗口。额度是账户级的状态：一个调用方撞上之后，
// 同一账户的其余调用方照着登记等待即可，不必各自再发一次注定被拒的请求去发现它。
// 由 Router 持有、进程内共享——已构造的 Provider 实例会定期重建，登记不能挂在实例上。
type cooldown struct {
	mu         sync.Mutex
	entries    map[string]*quotaWindowEntry
	window     time.Duration // 额度窗口与瞬时限流的分界，也是一次调用在窗口内静默等待的上限
	probeEvery time.Duration // 窗口内放行探测的间隔
	// holdInWindow 为假时，窗口内的调用不等候、立即交还限流（见 Router.WithoutQuotaHold）。
	// 装配期一次性设置，之后只读。
	holdInWindow bool
}

func newCooldown() *cooldown {
	return &cooldown{
		entries:      make(map[string]*quotaWindowEntry),
		window:       defaultQuotaWindow,
		probeEvery:   defaultQuotaProbeEvery,
		holdInWindow: true,
	}
}

// quotaWindowEntry 是一个账户的一次额度窗口。
type quotaWindowEntry struct {
	until      time.Time // 上游告知的恢复时刻
	cause      error     // 上游那次限流响应
	lastProbed time.Time // 最近一次放行探测的时刻；登记窗口时一并记下，首次探测由此起算
}

// admission 是发请求前的准入结果。
type admission int

const (
	admitFree  admission = iota // 没有窗口，或窗口已到期：照常发
	admitTail                   // 窗口只剩最后一段：等到恢复时刻再发，不必交还外层绕一圈
	admitProbe                  // 窗口内放行的一次探测
	admitHold                   // 窗口内：不发请求
)

// open 登记一次额度窗口，以上游最近一次告知为准。
func (c *cooldown) open(account string, until time.Time, cause error, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[account] = &quotaWindowEntry{until: until, cause: cause, lastProbed: now}
}

// lift 解除一个账户的额度窗口（放行的探测成功了）。
func (c *cooldown) lift(account string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, account)
}

// admit 判定一个账户的一次即将发出的请求能否发出。
func (c *cooldown) admit(account string, now time.Time) (admission, quotaWindowEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[account]
	if !ok || !now.Before(e.until) {
		delete(c.entries, account)
		return admitFree, quotaWindowEntry{}
	}
	snapshot := *e
	switch {
	case e.until.Sub(now) <= c.window:
		return admitTail, snapshot
	case now.Sub(e.lastProbed) >= c.probeEvery:
		// 先占住再放行：同一瞬间到来的其余调用方看到的是刚用过的探测名额，继续等待。
		e.lastProbed = now
		return admitProbe, snapshot
	}
	return admitHold, snapshot
}

// quotaIdentity 是一份额度归属的标识：同一服务地址上的同一把密钥。额度随账户走，不随
// provider 行走——两个 provider 共用一把密钥就是同一份额度，撞上一次就都该等；换了密钥
// 是另一份额度，旧账户的窗口不约束它。取摘要：标识只在进程内存里比对，密钥明文不落到
// 任何会被打印、导出的地方。
func quotaIdentity(baseURL, apiKey string) string {
	sum := sha256.Sum256([]byte(baseURL + "\x00" + apiKey))
	return hex.EncodeToString(sum[:])
}
