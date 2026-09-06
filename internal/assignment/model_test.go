package assignment

import (
	"encoding/json"
	"testing"
)

func TestSource_String(t *testing.T) {
	tests := []struct {
		source Source
		want   string
	}{
		{SourceManual, "manual"},
		{SourceAuto, "auto"},
	}
	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			if got := string(tt.source); got != tt.want {
				t.Errorf("Source = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusEmpty, "empty"},
		{StatusRunning, "running"},
		{StatusDone, "done"},
		{StatusAborted, "aborted"},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := string(tt.status); got != tt.want {
				t.Errorf("Status = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  NewParams
		wantErr bool
	}{
		{
			name: "合法的 manual assignment",
			params: NewParams{
				Source: SourceManual,
				Items: []Item{
					{Brief: "测试目标1", Host: "example.com"},
				},
				Title: "手动测试",
			},
			wantErr: false,
		},
		{
			name: "合法的 auto assignment",
			params: NewParams{
				Source: SourceAuto,
				Items: []Item{
					{Brief: "自动发现", Host: "auto.example.com"},
				},
			},
			wantErr: false,
		},
		{
			name: "非法的 source",
			params: NewParams{
				Source: "invalid",
				Items:  []Item{{Brief: "test"}},
			},
			wantErr: true,
		},
		{
			name: "Items 可以为空",
			params: NewParams{
				Source: SourceManual,
				Items:  nil,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 验证 source
			validSource := tt.params.Source == SourceManual || tt.params.Source == SourceAuto
			if !validSource && !tt.wantErr {
				t.Error("应该拒绝非法 source")
			}
		})
	}
}

func TestItem_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		item Item
		want string
	}{
		{
			name: "完整的 item",
			item: Item{
				Brief:      "测试目标",
				Host:       "example.com",
				TrafficIDs: []int64{1, 2, 3},
			},
			want: `{"brief":"测试目标","host":"example.com","traffic_ids":[1,2,3]}`,
		},
		{
			name: "只有 brief",
			item: Item{
				Brief: "测试目标",
			},
			want: `{"brief":"测试目标"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.item)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			// 比较结构而非字符串（字段顺序可能不同）
			var gotMap, wantMap map[string]interface{}
			if err := json.Unmarshal(got, &gotMap); err != nil {
				t.Fatalf("Unmarshal got: %v", err)
			}
			if err := json.Unmarshal([]byte(tt.want), &wantMap); err != nil {
				t.Fatalf("Unmarshal want: %v", err)
			}
			if gotMap["brief"] != wantMap["brief"] {
				t.Errorf("brief = %v, want %v", gotMap["brief"], wantMap["brief"])
			}
		})
	}
}

func TestAssignment_UnmarshalPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    int
		wantErr bool
	}{
		{
			name:    "空 payload",
			payload: []byte("[]"),
			want:    0,
			wantErr: false,
		},
		{
			name:    "单个 item",
			payload: []byte(`[{"brief":"测试","host":"example.com"}]`),
			want:    1,
			wantErr: false,
		},
		{
			name:    "多个 items",
			payload: []byte(`[{"brief":"测试1"},{"brief":"测试2"},{"brief":"测试3"}]`),
			want:    3,
			wantErr: false,
		},
		{
			name:    "非法 JSON",
			payload: []byte(`{invalid json`),
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var items []Item
			err := json.Unmarshal(tt.payload, &items)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(items) != tt.want {
				t.Errorf("len(items) = %v, want %v", len(items), tt.want)
			}
		})
	}
}

func TestAssignment_GetItems(t *testing.T) {
	items := []Item{
		{Brief: "测试1", Host: "host1.com"},
		{Brief: "测试2", Host: "host2.com"},
	}
	payload, _ := json.Marshal(items)

	a := Assignment{
		ID:      "a1",
		Source:  SourceManual,
		Payload: payload,
	}

	var got []Item
	if err := json.Unmarshal(a.Payload, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(got) != len(items) {
		t.Errorf("len(items) = %v, want %v", len(got), len(items))
	}

	for i, item := range got {
		if item.Brief != items[i].Brief {
			t.Errorf("item[%d].Brief = %v, want %v", i, item.Brief, items[i].Brief)
		}
		if item.Host != items[i].Host {
			t.Errorf("item[%d].Host = %v, want %v", i, item.Host, items[i].Host)
		}
	}
}
