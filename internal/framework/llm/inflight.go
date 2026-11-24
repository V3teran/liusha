package llm

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// inflightWaitMax 是调用方没有给出截止时间时，一次调用为在途名额最多排队这么久。
// 运行期的调用都带截止时间，那时按预算算（见 waitWithin）；这里只是兜底。
const inflightWaitMax = time.Minute

// ErrInflightBusy 表示这次调用拿不到账户的在途名额、没有请求发出。它不是上游的响应，
// 是本地的节流：上游没有拒绝任何请求，只是没有名额可发。
var ErrInflightBusy = errors.New("账户在途名额已满")

// inflight 限制各账户同时在途的请求数。限流与在途请求数强相关：实测同一账户在途
// 4 个以内时限流比例接近零，5 个起陡增，6 个时过半——限流不随任务数走，随在途数走，
// 所以上限设在账户上，而不是设在任务或 agent 上。由 Router 持有、进程内共享，按
// 额度归属识别账户（与 cooldown 同一口径）：同一账户的各 provider、各任务、各 agent
// 共用一份名额。
//
// 名额只在一个请求真实在途期间被占用：退避、等待额度窗口、等待名额都不占。一次
// 调用里没有嵌套的调用（工具不调模型），不存在「占着名额等别的名额」。
type inflight struct {
	mu       sync.Mutex
	limit    int           // 0 = 不限
	maxWait  time.Duration // 没有截止时间的调用的排队上限
	accounts map[string]*accountInflight
	// failFast 为真时名额满了不排队、立即交还（见 Router.FailFast）。装配期一次性设置。
	failFast bool
}

func newInflight(limit int) *inflight {
	return &inflight{limit: limit, maxWait: inflightWaitMax, accounts: make(map[string]*accountInflight)}
}

// accountInflight 是一个账户当前占用的名额与排队的调用。排队的调用分两条队列，各按
// 先来先得；名额空出时先给优先的调用，没有才轮到普通的（见 Request.Priority）。
type accountInflight struct {
	active   int
	priority list.List // *inflightWaiter
	ordinary list.List // *inflightWaiter
}

// inflightWaiter 是一个排队中的调用。queue 指向它所在的那条队列；被放行或撤出后为 nil。
type inflightWaiter struct {
	granted chan struct{}
	queue   *list.List
	elem    *list.Element
}

// acquire 为一次即将发出的请求占一个名额，返回归还函数（重复调用只归还一次）。
// priority 为真时排在普通调用之前。排不到时返回包装了 ErrInflightBusy 的错误；进来时
// 调用方的 ctx 已经结束则原样交还它的错误（排队中途结束的，由 ctx.Done 那一支交还）。
// 两种情况都没有名额被占。
func (i *inflight) acquire(ctx context.Context, account string, priority bool) (func(), error) {
	if i.limit <= 0 {
		return func() {}, nil
	}

	i.mu.Lock()
	// 预算已经用尽的调用发不出去，也不该去占名额。如实交还调用方的错误：预算用尽是
	// 调用方的事，调用方据此处置（如规划者据此放大下一轮预算）；报成「名额已满」会让
	// 同一个原因因这一刻有没有空位而得到两种错误。
	if err := ctx.Err(); err != nil {
		i.mu.Unlock()
		return nil, err
	}
	s := i.accounts[account]
	if s == nil {
		s = &accountInflight{}
		i.accounts[account] = s
	}
	// 有空位就直接发：预算只约束排队，不约束本来就不需要等的请求。
	if s.active < i.limit {
		s.active++
		i.mu.Unlock()
		return i.releaser(account), nil
	}
	wait := time.Duration(0)
	if !i.failFast {
		wait = i.waitWithin(ctx)
	}
	if wait <= 0 {
		i.forgetIfIdle(account, s)
		i.mu.Unlock()
		return nil, i.busy()
	}
	w := &inflightWaiter{granted: make(chan struct{})}
	w.queue = &s.ordinary
	if priority {
		w.queue = &s.priority
	}
	w.elem = w.queue.PushBack(w)
	i.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-w.granted:
	case <-ctx.Done():
		return i.giveUp(account, w, ctx.Err())
	case <-timer.C:
		return i.giveUp(account, w, i.busy())
	}
	return i.releaser(account), nil
}

// waitWithin 是本次调用最多排队多久：有截止时间就用剩余预算的一半。排队值得等——
// 等满再放弃要付双重代价：调用方交还动作后从头重做，而且重新排队就丢掉了先来后到的
// 位置；所以尽量等到，只留另一半预算给请求本身（实测单次请求的中位耗时 15 秒、
// p99 156 秒，缺省单步看门狗 300 秒时留给请求的 150 秒覆盖 98.8%）。没有截止时间
// 的调用没有预算可分，按内置上限。
func (i *inflight) waitWithin(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline) / 2
	}
	return i.maxWait
}

// giveUp 是排队未果的收尾，交还 cause。放弃可能与放行同时发生：仍在队列里就撤出；
// 已被放行则名额已在它名下、撤不出来——如实返回它的归还函数，交由调用方归还。
func (i *inflight) giveUp(account string, w *inflightWaiter, cause error) (func(), error) {
	i.mu.Lock()
	if w.elem != nil {
		s := i.accounts[account]
		w.queue.Remove(w.elem)
		w.elem, w.queue = nil, nil
		i.forgetIfIdle(account, s)
		i.mu.Unlock()
		return nil, cause
	}
	i.mu.Unlock()
	return i.releaser(account), nil
}

// release 归还一个名额：有人在排队就把名额直接转给下一位（先优先、后普通，不经过
// 空闲），否则记为空出。
func (i *inflight) release(account string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.accounts[account]
	front := s.priority.Front()
	if front == nil {
		front = s.ordinary.Front()
	}
	if front != nil {
		w := front.Value.(*inflightWaiter)
		w.queue.Remove(front)
		w.elem, w.queue = nil, nil
		close(w.granted)
		return
	}
	s.active--
	i.forgetIfIdle(account, s)
}

// queued 返回 account 上正在排队的调用数（优先与普通合计）。调用方持锁。
func (s *accountInflight) queued() int { return s.priority.Len() + s.ordinary.Len() }

// forgetIfIdle 清掉既没占用也没排队的账户条目。调用方持锁。
func (i *inflight) forgetIfIdle(account string, s *accountInflight) {
	if s.active == 0 && s.queued() == 0 {
		delete(i.accounts, account)
	}
}

// releaser 返回只归还一次的归还函数。
func (i *inflight) releaser(account string) func() {
	var once sync.Once
	return func() { once.Do(func() { i.release(account) }) }
}

// busy 是拿不到名额时交还的错误。对话侧不等待、也走这里，所以不说「排过了队」。
func (i *inflight) busy() error {
	return fmt.Errorf("%w（上限 %d）：本次调用没有发出，同一账户的并发上限见 llm.account_concurrency", ErrInflightBusy, i.limit)
}
