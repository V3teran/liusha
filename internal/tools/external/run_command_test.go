package external

import (
	"testing"

	"github.com/V3teran/liusha/internal/sandbox"
)

func TestImageMediaTypeFromName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"shot.png", "image/png"},
		{"SHOT.PNG", "image/png"}, // 大小写不敏感
		{"a.jpg", "image/jpeg"},
		{"a.JPEG", "image/jpeg"},
		{"a.gif", "image/gif"},
		{"a.webp", "image/webp"},
		{"output.pcap", ""}, // 非图片
		{"trace.json", ""},  // 非图片
		{"noext", ""},       // 无扩展名
		{".hidden.png", "image/png"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := imageMediaTypeFromName(c.name)
			if got != c.want {
				t.Fatalf("imageMediaTypeFromName(%q)=%q want %q", c.name, got, c.want)
			}
		})
	}
}

func TestExtractImagesFromFiles_OnlyImages(t *testing.T) {
	files := []sandbox.Attachment{
		{Name: "login.png", B64: "iVBORw0KAAA"},
		{Name: "trace.pcap", B64: "ZHVtcA=="}, // 非图，应跳过
		{Name: "dom.jpg", B64: "/9j/4AAQAAA"},
		{Name: "empty.png", B64: ""}, // 空 base64 跳过
		{Name: "icon.gif", B64: "R0lGODlhAQAB"},
	}
	imgs := extractImagesFromFiles(files)
	if len(imgs) != 3 {
		t.Fatalf("期望 3 张图（跳过 pcap + 空 base64），实际 %d 张: %+v", len(imgs), imgs)
	}
	wantMT := []string{"image/png", "image/jpeg", "image/gif"}
	for i, mt := range wantMT {
		if imgs[i].MediaType != mt {
			t.Errorf("imgs[%d].MediaType=%q want %q", i, imgs[i].MediaType, mt)
		}
		if imgs[i].Base64Data == "" {
			t.Errorf("imgs[%d].Base64Data 不应为空", i)
		}
	}
}

func TestExtractImagesFromFiles_NilAndEmpty(t *testing.T) {
	if got := extractImagesFromFiles(nil); got != nil {
		t.Errorf("nil 输入应返 nil，实际 %+v", got)
	}
	if got := extractImagesFromFiles([]sandbox.Attachment{}); got != nil {
		t.Errorf("空输入应返 nil，实际 %+v", got)
	}
	if got := extractImagesFromFiles([]sandbox.Attachment{{Name: "x.txt", B64: "abc"}}); got != nil {
		t.Errorf("全非图应返 nil，实际 %+v", got)
	}
}
