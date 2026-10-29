package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeLog 落一份 JSON lines 日志文件（行内容与写入端 zerolog 的字段形态一致）。
func writeLog(t *testing.T, dir, process string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	var b []byte
	for _, l := range lines {
		b = append(b, []byte(l+"\n")...)
	}
	if err := os.WriteFile(filepath.Join(dir, process+".log"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func logLine(ts string, level, service, instance, caller, msg string) string {
	return fmt.Sprintf(`{"time":"%s","level":"%s","service":"%s","instance":"%s","caller":"%s","message":"%s"}`,
		ts, level, service, instance, caller, msg)
}

func newTestReader(dir string) *Reader {
	r := NewReader(dir)
	r.scanWindow = 512 // 收小窗口驱动截窗逻辑
	r.instanceWindow = 256
	return r
}

func TestReadLogsInitial(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "api", []string{
		logLine("2026-01-01T10:00:01Z", "info", "api", "1@h", "httpapi/a.go:1", "第一条"),
		logLine("2026-01-01T10:00:02Z", "error", "api", "1@h", "httpapi/b.go:2", "第二条"),
		`{"time":"2026-01-01T10:00:03Z","level":"error","service":"api","instance":"1@h","message":"带错误","error":"connect refused","task_id":"t-1"}`,
		"非 JSON 行应被跳过",
		`{"半截`,
	})
	r := newTestReader(dir)
	res, err := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("应取到 3 条（跳过坏行），got %d", len(res.Entries))
	}
	last := res.Entries[2]
	if last.Error != "connect refused" {
		t.Errorf("error 字段未解析: %+v", last)
	}
	if last.Fields["task_id"] != "t-1" {
		t.Errorf("业务键应收进 fields: %+v", last.Fields)
	}
	if res.Cursors["api"] <= last.Offset {
		t.Errorf("游标应越过最后一行行首: cursor=%d offset=%d", res.Cursors["api"], last.Offset)
	}
	if len(res.Reset) != 0 || len(res.Truncated) != 0 {
		t.Errorf("冷读不该有 reset/truncated: %+v", res)
	}
}

func TestReadLogsIncrementalCursor(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "runner", []string{
		logLine("2026-01-01T10:00:01Z", "info", "runner", "1@h", "c/a.go:1", "旧一条"),
	})
	r := newTestReader(dir)
	first, err := r.ReadLogs(ReadQuery{Processes: []string{"runner"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}

	// 追加两行后带游标读：只返回增量
	f, _ := os.OpenFile(filepath.Join(dir, "runner.log"), os.O_APPEND|os.O_WRONLY, 0o600)
	newLines := []string{
		logLine("2026-01-01T10:00:02Z", "warn", "runner", "1@h", "c/b.go:2", "新一条"),
		logLine("2026-01-01T10:00:03Z", "info", "runner", "1@h", "c/c.go:3", "更新一条"),
	}
	for _, l := range newLines {
		f.WriteString(l + "\n")
	}
	f.Close()

	res, err := r.ReadLogs(ReadQuery{Processes: []string{"runner"}, Limit: 100, Cursors: first.Cursors})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("增量应只含 2 条新行，got %d", len(res.Entries))
	}
	if res.Entries[0].Message != "新一条" || res.Entries[1].Message != "更新一条" {
		t.Errorf("增量内容不符: %+v", res.Entries)
	}
}

func TestReadLogsPartialTrailingLine(t *testing.T) {
	dir := t.TempDir()
	r := newTestReader(dir)
	writeLog(t, dir, "api", []string{
		logLine("2026-01-01T10:00:01Z", "info", "api", "1@h", "a.go:1", "完整行"),
	})
	// 模拟写了一半的尾行（无 \n）
	path := filepath.Join(dir, "api.log")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"time":"2026-01-01T10:00:02Z","level":"info"`)
	f.Close()

	res, err := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("半截尾行不该被返回，got %d 条", len(res.Entries))
	}
	pending := res.Cursors["api"]

	// 写完整后（补上剩余字节 + \n）带游标读：半截行应被完整取回
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`,"message":"迟到行"}` + "\n")
	f.Close()

	res2, err := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Limit: 100, Cursors: map[string]int64{"api": pending}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Entries) != 1 || res2.Entries[0].Message != "迟到行" {
		t.Errorf("补全后的行应被读回: %+v", res2.Entries)
	}
}

func TestReadLogsRotationReset(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "proxy", []string{
		logLine("2026-01-01T10:00:01Z", "info", "proxy", "1@h", "p/a.go:1", "轮转前的长内容xxxxxxxxxxxxxxxx"),
		logLine("2026-01-01T10:00:02Z", "info", "proxy", "1@h", "p/b.go:2", "轮转前第二条xxxxxxxxxxxxxxxx"),
	})
	r := newTestReader(dir)
	first, err := r.ReadLogs(ReadQuery{Processes: []string{"proxy"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}

	// 轮转：文件被截短重写
	writeLog(t, dir, "proxy", []string{
		logLine("2026-01-01T11:00:00Z", "info", "proxy", "1@h", "p/c.go:3", "轮转后"),
	})
	res, err := r.ReadLogs(ReadQuery{Processes: []string{"proxy"}, Limit: 100, Cursors: first.Cursors})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reset) != 1 || res.Reset[0] != "proxy" {
		t.Errorf("文件变短应置 reset: %+v", res.Reset)
	}
	if len(res.Entries) != 1 || res.Entries[0].Message != "轮转后" {
		t.Errorf("reset 后应返回整段新内容: %+v", res.Entries)
	}
}

func TestReadLogsScanWindowTruncation(t *testing.T) {
	dir := t.TempDir()
	// scanWindow=512：写多行长内容，让窗口只盖住尾部若干行
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, logLine(
			time.Date(2026, 1, 1, 10, 0, i, 0, time.UTC).Format(time.RFC3339Nano),
			"info", "api", "1@h", "a.go:1", fmt.Sprintf("第%d条内容填充填充填充填充填充填充", i)))
	}
	writeLog(t, dir, "api", lines)
	r := newTestReader(dir)

	res, err := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) == 0 || len(res.Entries) >= 20 {
		t.Fatalf("窗口应只覆盖尾部若干行，got %d", len(res.Entries))
	}
	if len(res.Truncated) != 1 || res.Truncated[0] != "api" {
		t.Errorf("窗口外有行应置 truncated: %+v", res.Truncated)
	}
	// 窗口内的行必须是最新的那些
	lastMsg := res.Entries[len(res.Entries)-1].Message
	if lastMsg != "第19条内容填充填充填充填充填充填充" {
		t.Errorf("窗口内最后一行应是最新行: %s", lastMsg)
	}
}

func TestReadLogsLimitKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, logLine(
			time.Date(2026, 1, 1, 10, 0, i, 0, time.UTC).Format(time.RFC3339Nano),
			"info", "api", "1@h", "a.go:1", fmt.Sprintf("行%d", i)))
	}
	writeLog(t, dir, "api", lines)
	r := newTestReader(dir)

	res, err := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("limit=3 应保留 3 条, got %d", len(res.Entries))
	}
	want := []string{"行7", "行8", "行9"}
	for i, w := range want {
		if res.Entries[i].Message != w {
			t.Errorf("应保留最新 3 条: [%d]=%s, want %s", i, res.Entries[i].Message, w)
		}
	}
	if len(res.Truncated) != 1 {
		t.Errorf("超限丢弃应置 truncated: %+v", res.Truncated)
	}
}

func TestReadLogsFilters(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "api", []string{
		logLine("2026-01-01T10:00:01Z", "info", "api", "1@h", "a.go:1", "GET /healthz 200"),
		logLine("2026-01-01T10:00:02Z", "error", "api", "1@h", "b.go:2", "connect refused"),
		logLine("2026-01-01T10:00:03Z", "info", "api", "2@other", "c.go:3", "另一个实例"),
		`{"time":"2026-01-01T10:00:04Z","level":"info","service":"api","instance":"1@h","message":"普通消息","task_id":"task-abc"}`,
	})
	r := newTestReader(dir)

	byLevel, _ := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Level: "error", Limit: 100})
	if len(byLevel.Entries) != 1 || byLevel.Entries[0].Level != "error" {
		t.Errorf("level 筛选不符: %+v", byLevel.Entries)
	}

	byInstance, _ := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Instance: "2@other", Limit: 100})
	if len(byInstance.Entries) != 1 || byInstance.Entries[0].Instance != "2@other" {
		t.Errorf("instance 筛选不符: %+v", byInstance.Entries)
	}

	bySearch, _ := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Search: "HEALTHZ", Limit: 100})
	if len(bySearch.Entries) != 1 {
		t.Errorf("search 大小写不敏感匹配失败: %+v", bySearch.Entries)
	}

	// 业务键（fields）参与搜索
	byField, _ := r.ReadLogs(ReadQuery{Processes: []string{"api"}, Search: "task-abc", Limit: 100})
	if len(byField.Entries) != 1 {
		t.Errorf("fields 应参与搜索: %+v", byField.Entries)
	}
}

func TestReadLogsCrossProcessMerge(t *testing.T) {
	dir := t.TempDir()
	// 故意让 proxy 的行时间更晚，验证归并排序与文件读取顺序无关
	writeLog(t, dir, "api", []string{
		logLine("2026-01-01T10:00:01Z", "info", "api", "1@h", "a.go:1", "api 早上"),
	})
	writeLog(t, dir, "proxy", []string{
		logLine("2026-01-01T10:00:02Z", "info", "proxy", "1@h", "p.go:1", "proxy 晚些"),
	})
	r := newTestReader(dir)
	res, err := r.ReadLogs(ReadQuery{Limit: 100}) // 空 Processes = 全部
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("应读到 2 条, got %d", len(res.Entries))
	}
	if res.Entries[0].Service != "api" || res.Entries[1].Service != "proxy" {
		t.Errorf("应按时间升序归并: %+v", res.Entries)
	}
	if _, ok := res.Cursors["runner"]; !ok {
		t.Errorf("视图域全量进程都应有游标（文件缺失=0）: %+v", res.Cursors)
	}
}

func TestProcessesInventory(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "api", []string{
		logLine("2026-01-01T10:00:01Z", "info", "api", "72661@hohehoe.local", "a.go:1", "x"),
		logLine("2026-01-01T10:00:02Z", "info", "api", "72661@hohehoe.local", "a.go:2", "y"),
		logLine("2026-01-01T10:00:03Z", "info", "api", "8912@docker-host-2", "a.go:3", "z"),
	})
	r := newTestReader(dir)
	ps := r.LogProcesses()
	if len(ps) != len(ViewProcesses) {
		t.Fatalf("应枚举全部视图域进程, got %d", len(ps))
	}
	byName := map[string]ProcessInfo{}
	for _, p := range ps {
		byName[p.Name] = p
	}
	api := byName["api"]
	if !api.Exists || api.SizeBytes == 0 {
		t.Errorf("api 文件应存在: %+v", api)
	}
	if len(api.Instances) != 2 || api.Instances[0] != "72661@hohehoe.local" || api.Instances[1] != "8912@docker-host-2" {
		t.Errorf("实例枚举不符（去重+首现序）: %+v", api.Instances)
	}
	if byName["runner"].Exists {
		t.Error("未写的 runner 文件应 exists=false")
	}
}
