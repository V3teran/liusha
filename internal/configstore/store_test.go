package configstore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
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
	sc := cfgscenario.Scenario{ID: "id-" + p.Code, Code: p.Code, Name: p.Name, Instruction: p.Instruction, Engine: p.Engine, PlaybookID: p.PlaybookID, Enabled: p.Enabled}
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

// fakeHunters 是最小 hunter 底层 store 假实现（PlaybookHunters/Orchestrator 测试够用）。
type fakeHunters struct {
	orch    cfghunter.Hunter
	orchHit int64
}

func (f *fakeHunters) GetByID(_ context.Context, id string) (cfghunter.Hunter, error) {
	return cfghunter.Hunter{ID: id}, nil
}
func (f *fakeHunters) GetByCode(_ context.Context, _ string) (cfghunter.Hunter, error) {
	return cfghunter.Hunter{}, pgxErrNoRows()
}
func (f *fakeHunters) Create(_ context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error) {
	return cfghunter.Hunter{ID: "id-" + p.Code, Code: p.Code}, nil
}
func (f *fakeHunters) Update(_ context.Context, _ cfghunter.NewParams) (cfghunter.Hunter, error) {
	return cfghunter.Hunter{}, pgxErrNoRows()
}
func (f *fakeHunters) Delete(_ context.Context, _ string) error { return nil }
func (f *fakeHunters) List(_ context.Context, _ bool) ([]cfghunter.Hunter, error) {
	return nil, nil
}
func (f *fakeHunters) GetOrchestrator(_ context.Context) (cfghunter.Hunter, error) {
	atomic.AddInt64(&f.orchHit, 1)
	return f.orch, nil
}

// fakePlaybooks 是最小 playbook 底层 store 假实现。
type fakePlaybooks struct{}

func (fakePlaybooks) GetByID(_ context.Context, id string) (cfgplaybook.Playbook, error) {
	return cfgplaybook.Playbook{ID: id}, nil
}
func (fakePlaybooks) GetByCode(_ context.Context, _ string) (cfgplaybook.Playbook, error) {
	return cfgplaybook.Playbook{}, pgxErrNoRows()
}
func (fakePlaybooks) Create(_ context.Context, p cfgplaybook.NewParams) (cfgplaybook.Playbook, error) {
	return cfgplaybook.Playbook{ID: "id-" + p.Code, Code: p.Code}, nil
}
func (fakePlaybooks) Update(_ context.Context, _ cfgplaybook.NewParams) (cfgplaybook.Playbook, error) {
	return cfgplaybook.Playbook{}, pgxErrNoRows()
}
func (fakePlaybooks) Delete(_ context.Context, _ string) error { return nil }
func (fakePlaybooks) List(_ context.Context) ([]cfgplaybook.Playbook, error) {
	return nil, nil
}
func (fakePlaybooks) SetHunters(_ context.Context, _ string, _ []cfgplaybook.PlaybookHunter) error {
	return nil
}
func (fakePlaybooks) ListHunters(_ context.Context, _ string) ([]cfghunter.Hunter, error) {
	return nil, nil
}

// newTestStore 用 miniredis + 假底层 store 构造 Store，返回 store、场景假实现、miniredis。
func newTestStore(t *testing.T) (*Store, *fakeScenarios, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fs := &fakeScenarios{byCode: map[string]cfgscenario.Scenario{}}
	s := newWithStores(fs, fakePlaybooks{}, &fakeHunters{}, rdb)
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
	s.l1.del(keyScenarioCode("web"), keyScenarioID("id-web")) // 仅清 L1，保留 L2

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
	a := newWithStores(fsA, fakePlaybooks{}, &fakeHunters{}, rdb)
	b := newWithStores(fsB, fakePlaybooks{}, &fakeHunters{}, rdb)

	// 进程 B 起订阅。
	go func() { _ = b.Subscribe(ctx) }()
	waitSubscribed(t, mr, 1)

	// B 先读，暖起 B 的 L1。
	if _, err := b.ScenarioByCode(ctx, "web"); err != nil {
		t.Fatalf("B warm: %v", err)
	}

	// A 改名（走 upsert Update），PUBLISH 失效。
	if _, err := a.SaveScenario(ctx, cfgscenario.NewParams{
		Code: "web", Name: "新名", Engine: cfgscenario.EngineSwarm, PlaybookID: "id-pb",
	}); err != nil {
		t.Fatalf("A save: %v", err)
	}

	// B 的 L1 应被订阅清除→下次读拿到新名。
	waitForCondition(t, func() bool {
		_, ok := b.l1.get(keyScenarioCode("web"))
		return !ok
	})
	got, err := b.ScenarioByCode(ctx, "web")
	if err != nil {
		t.Fatalf("B reread: %v", err)
	}
	if got.Name != "新名" {
		t.Fatalf("B 应读到新名，实际 %q", got.Name)
	}
}

// TestOrchestrator_SentinelKeyCached 验证：Orchestrator 首读打底层、二读命中哨兵键缓存。
func TestOrchestrator_SentinelKeyCached(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fh := &fakeHunters{orch: cfghunter.Hunter{ID: "id-orch", Code: "orchestrator", Kind: cfghunter.KindOrchestrator}}
	s := newWithStores(&fakeScenarios{byCode: map[string]cfgscenario.Scenario{}}, fakePlaybooks{}, fh, rdb)

	for i := 0; i < 3; i++ {
		if _, err := s.Orchestrator(ctx); err != nil {
			t.Fatalf("orchestrator read #%d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(&fh.orchHit); got != 1 {
		t.Fatalf("期望编排猎手只打底层 1 次，实际 %d", got)
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
