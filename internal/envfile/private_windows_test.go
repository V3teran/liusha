//go:build windows

package envfile

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// 生成的文件不继承父目录权限，只对当前用户、SYSTEM、Administrators 开放。
func TestCreate_RestrictsToOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liusha.env")
	if err := Create(path, []Section{{Vars: []Var{{Key: "K", Value: "v"}}}}); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("DACL 应受保护（不继承父目录）")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl.AceCount != 3 {
		t.Fatalf("DACL 应只有 3 条授权，得 %d", dacl.AceCount)
	}
}
