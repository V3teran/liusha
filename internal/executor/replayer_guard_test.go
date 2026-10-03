package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/httpreplay"
)

type stubTraffic struct{}

func (stubTraffic) GetInScope(_ context.Context, id int64) (httpreplay.Source, bool, error) {
	return httpreplay.Source{ID: id, Method: "POST", URL: "http://t/login", Body: []byte("id=1&pw=x")}, true, nil
}

// 原样重放（无 modifications）必须被拒——命中只能是页面常态，不可坐实。
func TestReplay_RejectsNoModification(t *testing.T) {
	r := NewReplayer(stubTraffic{})
	sc := 200
	_, err := r.Replay(context.Background(), json.RawMessage(`{"traffic_id":1,"modifications":null,"assert":{"status_code":`+jsonInt(sc)+`}}`))
	if err == nil || !strings.Contains(err.Error(), "原样重放") {
		t.Fatalf("无改写配方应被拒: err=%v", err)
	}
}

func jsonInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
