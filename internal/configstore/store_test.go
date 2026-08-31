package configstore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
	cfgagent "github.com/V3teran/liusha/internal/config/agent"
)

// errNoRows 是底层 store「不存在」的 sentinel，与生产 store 用 %w 包的 pgx.ErrNoRows 一致，
// 保证 isNotFound(errors.Is) 在 upsert 回退路径能识别。
var errNoRows = pgx.ErrNoRows

// fakeScenarios 是带 DB 命中计数的 scenario 底层 store 假实现。
type fakeScenarios struct {
	byCode map[string]cfgscenario.Scenario
	getHit int64 // GetByCode+GetByID 累计打底层次数（读穿透断言用）
}

func (f *fakeScenarios) GetByCode(_ context.Context, code string) (cfgscenario.Scenario, error) {
	atomic.AddInt64(&f.getHit, 1)
	sc, ok := f.byCode[code]
	if !ok {
		return cfgscenario.Scenario{}, pgxErrNoRows()
	}
	return sc, nil
}

func (f *fakeScenarios) GetByID(_ context.Context, id string) (cfgscenario.Scenario, error) {
	atomic.AddInt64(&f.getHit, 1)
	for _, sc := range f.byCode {
		if sc.ID == id {
			return sc, nil
		}
	}
	return cfgscenario.Scenario{}, pgxErrNoRows()
}

func (f *fakeScenarios) Create(_ context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error) {
	sc := cfgscenario.Scenario{ID: "id-" + p.Code, Code: p.Code, Name: p.Name, Instruction: p.Instruction, Engine: p.Engine, SoloExecutorID: p.SoloExecutorID, Enabled: p.Enabled}
	f.byCode[p.Code] = sc
	return sc, nil
}

func (f *fakeScenarios) Update(_ context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error) {
	if _, ok := f.byCode[p.Code]; !ok {
		return cfgscenario.Scenario{}, pgxErrNoRows()
	}
	sc := f.byCode[p.Code]
	sc.Name = p.Name
	sc.Instruction = p.Instruction
	f.byCode[p.Code] = sc
	return sc, nil
}

func (f *fakeScenarios) Delete(_ context.Context, code string) error {
	delete(f.byCode, code)
	return nil
}

func (f *fakeScenarios) List(_ context.Context, _ bool) ([]cfgscenario.Scenario, error) {
	out := make([]cfgscenario.Scenario, 0, len(f.byCode))
	for _, sc := range f.byCode {
		out = append(out, sc)
	}
	return out, nil
}
func (f *fakeScenarios) ListPaged(_ context.Context, _ cfgscenario.ListParams) ([]cfgscenario.Scenario, error) {
	out := make([]cfgscenario.Scenario, 0, len(f.byCode))
	for _, sc := range f.byCode {
		out = append(out, sc)
	}
	return out, nil
}
func (f *fakeScenarios) Count(_ context.Context, _ cfgscenario.ListParams) (int, error) {
	return len(f.byCode), nil
}

// fakeExecutors 是最小 agent 底层 store 假实现（EnabledDomain/planner 测试够用）。
type fakeExecutors struct {
	orch      cfgagent.Agent
	orchHit   int64
	domain    []cfgagent.Agent
	domainHit int64

	complexityByCode map[string]string // code → complexity（缺失=非 agent 路由键，found=false）
	complexityHit    int64             // ComplexityByCode 打底层次数（complexity 读穿透断言用）
	list             []cfgagent.Agent
	listHit          int64             // List 打底层次数（列表读穿透断言用）
	codeByID         map[string]string // id → code：模拟 UpdateComplexity 的 RETURNING colsSelect 回填 Code
}

func (f *fakeExecutors) GetByID(_ context.Context, id string) (cfgagent.Agent, error) {
	return cfgagent.Agent{ID: id}, nil
}
func (f *fakeExecutors) GetByCode(_ context.Context, _ string) (cfgagent.Agent, error) {
	return cfgagent.Agent{}, pgxErrNoRows()
}
func (f *fakeExecutors) ComplexityByCode(_ context.Context, code string) (string, bool, error) {
	atomic.AddInt64(&f.complexityHit, 1)
	complexity, ok := f.complexityByCode[code]
	return complexity, ok, nil
}
func (f *fakeExecutors) Create(_ context.Context, code string, p cfgagent.UpdateParams) (cfgagent.Agent, error) {
	return cfgagent.Agent{ID: "id-" + code, Code: code}, nil
}
func (f *fakeExecutors) Update(_ context.Context, code string, p cfgagent.UpdateParams) (cfgagent.Agent, error) {
	return cfgagent.Agent{ID: "id-" + code, Code: code}, nil
}
func (f *fakeExecutors) UpdateComplexity(_ context.Context, id, complexity string) (cfgagent.Agent, error) {
	// 生产 UpdateComplexity 用 RETURNING colsSelect，返回行含 Code；假实现据 codeByID 回填。
	return cfgagent.Agent{ID: id, Code: f.codeByID[id], Complexity: complexity}, nil
}
func (f *fakeExecutors) Delete(_ context.Context, _ string) error { return nil }
func (f *fakeExecutors) List(_ context.Context, _ bool) ([]cfgagent.Agent, error) {
	atomic.AddInt64(&f.listHit, 1)
	return f.list, nil
}
func (f *fakeExecutors) ListPaged(_ context.Context, _ cfgagent.ListParams) ([]cfgagent.Agent, error) {
	return nil, nil
}
func (f *fakeExecutors) CountList(_ context.Context, _ cfgagent.ListParams) (int, error) {
	return 0, nil
}
func (f *fakeExecutors) ListEnabledDomain(_ context.Context) ([]cfgagent.Agent, error) {
	atomic.AddInt64(&f.domainHit, 1)
	return f.domain, nil
}
func (f *fakeExecutors) GetPlanner(_ context.Context) (cfgagent.Agent, error) {
	atomic.AddInt64(&f.orchHit, 1)
	return f.orch, nil
}
func (f *fakeExecutors) GetExecutor(_ context.Context) (cfgagent.Agent, error) {
	atomic.AddInt64(&f.domainHit, 1)
	if len(f.domain) == 0 {
		return cfgagent.Agent{}, pgxErrNoRows()
	}
	return f.domain[0], nil
}

// newTestStore 用 miniredis + 假底层 store 构造 Store，返回 store、场景假实现、miniredis。
func newTestStore(t *testing.T) (*Store, *fakeScenarios, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fs := &fakeScenarios{byCode: map[string]cfgscenario.Scenario{}}
	s := newWithStores(fs, &fakeExecutors{}, cachestore.New(rdb, 0))
	return s, fs, mr
}

// pgxErrNoRows 返回底层 store「不存在」时用 %w 包的 pgx.ErrNoRows（isNotFound 据此判定）。
func pgxErrNoRows() error { return errNoRows }

// TestReadThrough_RefillHitsDBOnce 验证：首读打 DB，二读命中 L1（DB 计数不增）。
func TestReadThrough_RefillHitsDBOnce(t *testing.T) {
	ctx := context.Background()
	s, fs, _ := newTestStore(t)
	fs.byCode["web"] = cfgscenario.Scenario{ID: "id-web", Code: "web", Engine: cfgscenario.EngineSwarm}

	if _, err := s.ScenarioByCode(ctx, "web"); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := s.ScenarioByCode(ctx, "web"); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if got := atomic.LoadInt64(&fs.getHit); got != 1 {
		t.Fatalf("期望 DB 只打 1 次（二读命中 L1），实际 %d", got)
	}
}

// TestReadThrough_L2HitSkipsDB 验证：清 L1 后读命中 redis L2，不再打 DB。
func TestReadThrough_L2HitSkipsDB(t *testing.T) {
	ctx := context.Background()
	s, fs, _ := newTestStore(t)
	fs.byCode["web"] = cfgscenario.Scenario{ID: "id-web", Code: "web", Engine: cfgscenario.EngineSwarm}

	if _, err := s.ScenarioByCode(ctx, "web"); err != nil { // 回填 L1+L2
		t.Fatalf("warm: %v", err)
	}
	s.cache.DropL1(keyScenarioCode("web"), keyScenarioID("id-web")) // 仅清 L1，保留 L2

	if _, err := s.ScenarioByCode(ctx, "web"); err != nil {
		t.Fatalf("read after L1 evict: %v", err)
	}
	if got := atomic.LoadInt64(&fs.getHit); got != 1 {
		t.Fatalf("期望 DB 仍只 1 次（L2 命中），实际 %d", got)
	}
}

// TestScenarioDualPath_ShareEntry 验证：code 路回填后，id 路读命中缓存不打 DB。
func TestScenarioDualPath_ShareEntry(t *testing.T) {
	ctx := context.Background()
	s, fs, _ := newTestStore(t)
	fs.byCode["web"] = cfgscenario.Scenario{ID: "id-web", Code: "web", Engine: cfgscenario.EngineSwarm}

	if _, err := s.ScenarioByCode(ctx, "web"); err != nil {
		t.Fatalf("code path: %v", err)
	}
	if _, err := s.ScenarioByID(ctx, "id-web"); err != nil {
		t.Fatalf("id path: %v", err)
	}
	if got := atomic.LoadInt64(&fs.getHit); got != 1 {
		t.Fatalf("双路应共享 entry，DB 只 1 次，实际 %d", got)
	}
}

// TestSubscribe_CrossProcessInvalidation 验证：进程 A 写→PUBLISH→进程 B 的 L1 被清、下次读拿新值。
func TestSubscribe_CrossProcessInvalidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// 两个 Store 实例共享 redis，各自持有独立 L1（模拟 api 与 runner 两进程）。
	fsA := &fakeScenarios{byCode: map[string]cfgscenario.Scenario{
		"web": {ID: "id-web", Code: "web", Name: "旧名", Engine: cfgscenario.EngineSwarm},
	}}
	fsB := &fakeScenarios{byCode: fsA.byCode} // 共享底层数据（同一 DB）
	a := newWithStores(fsA, &fakeExecutors{}, cachestore.New(rdb, 0))
	bCache := cachestore.New(rdb, 0)
	b := newWithStores(fsB, &fakeExecutors{}, bCache)

	// 进程 B 起订阅（订阅在共享 cachestore 内核上，一条循环覆盖所有资源）。
	go func() { _ = bCache.Subscribe(ctx) }()
	waitSubscribed(t, mr, 1)

	// B 先读，暖起 B 的 L1。
	if _, err := b.ScenarioByCode(ctx, "web"); err != nil {
		t.Fatalf("B warm: %v", err)
	}

	// A 改名（走 upsert Update），PUBLISH 失效。
	if _, err := a.SaveScenario(ctx, cfgscenario.NewParams{
		Code: "web", Name: "新名", Engine: cfgscenario.EngineSwarm,
	}); err != nil {
		t.Fatalf("A save: %v", err)
	}

	// B 的 L1 应被订阅清除→下次读拿到新名。
	waitForCondition(t, func() bool {
		return !b.cache.L1Has(keyScenarioCode("web"))
	})
	got, err := b.ScenarioByCode(ctx, "web")
	if err != nil {
		t.Fatalf("B reread: %v", err)
	}
	if got.Name != "新名" {
		t.Fatalf("B 应读到新名，实际 %q", got.Name)
	}
}

// Testplanner_SentinelKeyCached 验证：planner 首读打底层、二读命中哨兵键缓存。
func TestPlanner_SentinelKeyCached(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fh := &fakeExecutors{orch: cfgagent.Agent{ID: "id-orch", Code: "planner", Kind: cfgagent.KindPlanner}}
	s := newWithStores(&fakeScenarios{byCode: map[string]cfgscenario.Scenario{}}, fh, cachestore.New(rdb, 0))

	for i := 0; i < 3; i++ {
		if _, err := s.Planner(ctx); err != nil {
			t.Fatalf("planner read #%d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(&fh.orchHit); got != 1 {
		t.Fatalf("期望编排操作员只打底层 1 次，实际 %d", got)
	}
}

// TestEnabledDomainExecutors_SentinelKeyCached 验证：EnabledDomainExecutors 首读打底层、
// 后续命中哨兵键缓存。新架构下只返回1个通用Executor。
func TestEnabledDomainExecutors_SentinelKeyCached(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fh := &fakeExecutors{domain: []cfgagent.Agent{
		{ID: "id-executor", Code: "executor", Kind: cfgagent.KindExecutor},
	}}
	s := newWithStores(&fakeScenarios{byCode: map[string]cfgscenario.Scenario{}}, fh, cachestore.New(rdb, 0))

	for i := 0; i < 3; i++ {
		got, err := s.EnabledDomainExecutors(ctx)
		if err != nil {
			t.Fatalf("enabled domain read #%d: %v", i, err)
		}
		if len(got) != 1 {
			t.Fatalf("期望 1 个执行者，实际 %d", len(got))
		}
	}
	if got := atomic.LoadInt64(&fh.domainHit); got != 1 {
		t.Fatalf("期望执行者池只打底层 1 次，实际 %d", got)
	}
}

// TestUpdateExecutor_InvalidatesSentinels 验证：UpdateExecutor 后两个哨兵键都被清（下次读重打底层）。
func TestUpdateExecutor_InvalidatesSentinels(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fh := &fakeExecutors{
		orch:   cfgagent.Agent{ID: "id-orch", Code: "planner", Kind: cfgagent.KindPlanner},
		domain: []cfgagent.Agent{{ID: "id-recon", Code: "reconnaissance", Kind: cfgagent.KindExecutor}},
	}
	s := newWithStores(&fakeScenarios{byCode: map[string]cfgscenario.Scenario{}}, fh, cachestore.New(rdb, 0))

	// 暖起两个哨兵键。
	if _, err := s.Planner(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnabledDomainExecutors(ctx); err != nil {
		t.Fatal(err)
	}

	// 写一个操作员 → 两个哨兵键应被失效。
	complexityStr := "medium"
	if _, err := s.UpdateExecutor(ctx, "new", cfgagent.UpdateParams{Complexity: &complexityStr}); err != nil {
		t.Fatalf("update executor: %v", err)
	}
	if s.cache.L1Has(keyplanner) {
		t.Fatal("编排哨兵键应被清")
	}
	if s.cache.L1Has(keyExecutor) {
		t.Fatal("executor哨兵键应被清")
	}
}

func strPtr(s string) *string { return &s }

// newAgentTestStore 用 miniredis + 假 agent 底层 store 构造，返回 store 与 agent 假实现。
func newAgentTestStore(t *testing.T, fh *fakeExecutors) *Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newWithStores(&fakeScenarios{byCode: map[string]cfgscenario.Scenario{}}, fh, cachestore.New(rdb, 0))
}

// TestComplexityByCode_Cached 验证：ComplexityByCode 首读打底层、后续命中 complexity 键缓存（热路径路由第一跳）。
func TestComplexityByCode_Cached(t *testing.T) {
	ctx := context.Background()
	fh := &fakeExecutors{complexityByCode: map[string]string{"traffic-analysis": "complex"}}
	s := newAgentTestStore(t, fh)

	for i := 0; i < 3; i++ {
		complexity, found, err := s.ComplexityByCode(ctx, "traffic-analysis")
		if err != nil {
			t.Fatalf("complexity read #%d: %v", i, err)
		}
		if !found || complexity != "complex" {
			t.Fatalf("期望 (complex,true)，实际 (%q,%v)", complexity, found)
		}
	}
	if got := atomic.LoadInt64(&fh.complexityHit); got != 1 {
		t.Fatalf("期望 complexity 只打底层 1 次，实际 %d", got)
	}
}

// TestComplexityByCode_CachesNotFound 验证：found=false（非 agent 路由键，如 inspector）也缓存，
// 后续读不再打底层——省掉这类 key 每次 agent-run 的重复 DB miss。
func TestComplexityByCode_CachesNotFound(t *testing.T) {
	ctx := context.Background()
	fh := &fakeExecutors{complexityByCode: map[string]string{}} // inspector 不在表中
	s := newAgentTestStore(t, fh)

	for i := 0; i < 3; i++ {
		_, found, err := s.ComplexityByCode(ctx, "inspector")
		if err != nil {
			t.Fatalf("complexity read #%d: %v", i, err)
		}
		if found {
			t.Fatalf("inspector 非 executor 路由键，期望 found=false")
		}
	}
	if got := atomic.LoadInt64(&fh.complexityHit); got != 1 {
		t.Fatalf("期望 found=false 也缓存（只打底层 1 次），实际 %d", got)
	}
}

// TestUpdateExecutorComplexity_InvalidatesTierKey 验证：移复杂度档写后 complexity 键被清，下次读拿新档（无脏读）。
func TestUpdateExecutorComplexity_InvalidatesTierKey(t *testing.T) {
	ctx := context.Background()
	fh := &fakeExecutors{
		complexityByCode: map[string]string{"traffic-analysis": "medium"},
		codeByID:   map[string]string{"id-ta": "traffic-analysis"},
	}
	s := newAgentTestStore(t, fh)

	if _, _, err := s.ComplexityByCode(ctx, "traffic-analysis"); err != nil { // 暖起 complexity 键
		t.Fatal(err)
	}
	// UpdateTier 返回行含 code（生产 RETURNING colsSelect），失效 agentKeys 含 complexity 键。
	fh.complexityByCode["traffic-analysis"] = "complex" // 模拟底层已改
	if _, err := s.UpdateExecutorComplexity(ctx, "id-ta", "complex"); err != nil {
		t.Fatalf("update tier: %v", err)
	}
	if s.cache.L1Has(keyExecutorComplexity("traffic-analysis")) {
		t.Fatal("complexity 键应被失效")
	}
	complexity, _, err := s.ComplexityByCode(ctx, "traffic-analysis")
	if err != nil {
		t.Fatalf("reread complexity: %v", err)
	}
	if complexity != "complex" {
		t.Fatalf("移复杂度档后应读到 complex，实际 %q", complexity)
	}
}

// TestUpdateExecutor_InvalidatesComplexityKey 验证：整体更新（第二个 complexity 写入口）也失效 complexity 键。
func TestUpdateExecutor_InvalidatesComplexityKey(t *testing.T) {
	ctx := context.Background()
	fh := &fakeExecutors{complexityByCode: map[string]string{"new": "medium"}}
	s := newAgentTestStore(t, fh)

	if _, _, err := s.ComplexityByCode(ctx, "new"); err != nil { // 暖起 complexity 键
		t.Fatal(err)
	}
	// UpdateExecutor 走更新
	complexityStr := "medium"
	if _, err := s.UpdateExecutor(ctx, "new", cfgagent.UpdateParams{Complexity: &complexityStr}); err != nil {
		t.Fatalf("update executor: %v", err)
	}
	if s.cache.L1Has(keyExecutorComplexity("new")) {
		t.Fatal("UpdateExecutor 也应失效 complexity 键（第二写入口）")
	}
}

// TestListExecutors_CachedAndInvalidated 验证：ListExecutors 走缓存（二读不打底层），写后失效。
func TestListExecutors_CachedAndInvalidated(t *testing.T) {
	ctx := context.Background()
	fh := &fakeExecutors{list: []cfgagent.Agent{{ID: "id-a", Code: "a", Kind: cfgagent.KindExecutor}}}
	s := newAgentTestStore(t, fh)

	if _, err := s.ListExecutors(ctx, false); err != nil {
		t.Fatalf("first list: %v", err)
	}
	if _, err := s.ListExecutors(ctx, false); err != nil {
		t.Fatalf("second list: %v", err)
	}
	if got := atomic.LoadInt64(&fh.listHit); got != 1 {
		t.Fatalf("期望列表只打底层 1 次（二读命中缓存），实际 %d", got)
	}
	// 写一个操作员 → 列表键应被失效。
	complexityStr := "medium"
	if _, err := s.UpdateExecutor(ctx, "b", cfgagent.UpdateParams{Complexity: &complexityStr}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if s.cache.L1Has(keyAgentsList(false)) {
		t.Fatal("executors 列表键应被失效")
	}
	if _, err := s.ListExecutors(ctx, false); err != nil {
		t.Fatalf("relist: %v", err)
	}
	if got := atomic.LoadInt64(&fh.listHit); got != 2 {
		t.Fatalf("失效后重读应再打底层 1 次（共 2），实际 %d", got)
	}
}

// TestListScenarios_CachedAndInvalidated 验证：ListScenarios 走缓存，写后失效。
func TestListScenarios_CachedAndInvalidated(t *testing.T) {
	ctx := context.Background()
	s, fs, _ := newTestStore(t)
	fs.byCode["web"] = cfgscenario.Scenario{ID: "id-web", Code: "web", Engine: cfgscenario.EngineSwarm}

	if _, err := s.ListScenarios(ctx, false); err != nil {
		t.Fatalf("first list: %v", err)
	}
	if s.cache.L1Has(keyScenariosList(false)) == false {
		t.Fatal("首读后 scenarios 列表键应回填 L1")
	}
	if _, err := s.SaveScenario(ctx, cfgscenario.NewParams{Code: "api", Engine: cfgscenario.EngineSwarm}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if s.cache.L1Has(keyScenariosList(false)) {
		t.Fatal("scenarios 列表键应被失效")
	}
}

// waitSubscribed 轮询 miniredis 直到 channel 订阅者数达到 want。
func waitSubscribed(t *testing.T, mr *miniredis.Miniredis, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mr.PubSubChannels("*")) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待订阅者超时")
}

// waitForCondition 轮询 cond 直到为真或超时。
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
