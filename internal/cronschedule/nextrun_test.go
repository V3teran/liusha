package cronschedule

import (
	"testing"
	"time"
)

func TestNextRun_ComputesNextMinuteBoundary(t *testing.T) {
	from := time.Date(2026, 7, 11, 10, 30, 15, 0, time.UTC)
	got, err := NextRun("* * * * *", from)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	want := time.Date(2026, 7, 11, 10, 31, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNextRun_DailyAtFixedHour(t *testing.T) {
	from := time.Date(2026, 7, 11, 10, 30, 0, 0, time.UTC)
	got, err := NextRun("0 4 * * *", from)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	want := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNextRun_RejectsInvalidExpr(t *testing.T) {
	if _, err := NextRun("not a cron expr", time.Now()); err == nil {
		t.Fatal("非法 cron_expr 应报错")
	}
}
