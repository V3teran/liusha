package grounding

import (
	"strings"
	"testing"
)

func TestToRealPixels_Normalized1000(t *testing.T) {
	// DVWA spike test 实测值：viewport 1280×800，username 输入框 doubao 输出 (496, 315)
	// 换算后预期 (634, 252)——实际真坐标 (632, 252)，误差 3px
	x, y, err := ToRealPixels(496, 315, Normalized1000, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	if x != 634 || y != 252 {
		t.Errorf("want (634, 252), got (%d, %d)", x, y)
	}
}

func TestToRealPixels_RealPixels(t *testing.T) {
	x, y, err := ToRealPixels(632, 252, RealPixels, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	if x != 632 || y != 252 {
		t.Errorf("real_pixels should passthrough, got (%d, %d)", x, y)
	}
}

func TestToRealPixels_Normalized100(t *testing.T) {
	// 50% 横 × 1280 = 640，30% 纵 × 800 = 240
	x, y, err := ToRealPixels(50, 30, Normalized100, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	if x != 640 || y != 240 {
		t.Errorf("want (640, 240), got (%d, %d)", x, y)
	}
}

func TestToRealPixels_InvalidViewport(t *testing.T) {
	cases := []struct{ w, h int }{{0, 800}, {1280, 0}, {-1, -1}}
	for _, c := range cases {
		if _, _, err := ToRealPixels(500, 500, Normalized1000, c.w, c.h); err == nil {
			t.Errorf("viewport (%d, %d) should err", c.w, c.h)
		}
	}
}

func TestToRealPixels_UnknownSystem(t *testing.T) {
	if _, _, err := ToRealPixels(500, 500, "weird_scheme", 1280, 800); err == nil {
		t.Fatal("unknown system should err")
	}
}

func TestToRealPixels_OutOfRange(t *testing.T) {
	// LLM 偶尔给超范围坐标（> 1000 / < 0），工具层不裁剪——按比例算，让 browser 层报
	x, y, _ := ToRealPixels(1010, -5, Normalized1000, 1280, 800)
	if x != 1292 || y != -4 {
		t.Errorf("out-of-range should compute proportionally, got (%d, %d)", x, y)
	}
}

func TestDescribe(t *testing.T) {
	if !strings.Contains(Describe(Normalized1000), "0-1000") {
		t.Error("normalized_1000 description missing range")
	}
	if !strings.Contains(Describe(RealPixels), "真实像素") {
		t.Error("real_pixels description missing keyword")
	}
}
