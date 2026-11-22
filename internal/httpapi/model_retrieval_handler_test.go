package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/V3teran/liusha/internal/llmconfig"
)

// fakeRetrievalDiagnoser 记录入参，返回预置结果。
type fakeRetrievalDiagnoser struct {
	gotAPIKey string
	res       RetrievalDiagnosis
	err       error
}

func (f *fakeRetrievalDiagnoser) DiagnoseRetrieval(_ context.Context, apiKey string) (RetrievalDiagnosis, error) {
	f.gotAPIKey = apiKey
	return f.res, f.err
}

// fakeBackfill 记录补齐触发次数。
type fakeBackfill struct{ kicks int }

func (f *fakeBackfill) KickVectorBackfill() { f.kicks++ }

// TestGetRetrieval_Unconfigured：未配置时 key_present=false，模型名只读展示，无 updated_at。
func TestGetRetrieval_Unconfigured(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/models/retrieval", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	r := body["retrieval"].(map[string]any)
	if r["key_present"] != false {
		t.Fatalf("key_present = %v, want false", r["key_present"])
	}
	if r["embedding_model"] == "" || r["rerank_model"] == "" {
		t.Fatalf("模型名应只读展示: %v", r)
	}
	if r["updated_at"] != nil {
		t.Fatalf("未配置不应有 updated_at: %v", r)
	}
}

// TestSaveRetrieval_EncryptsAndKicksBackfill：保存走加密路径落库并触发即时补齐；
// 响应带末 4 位但不回显任何密钥形态。
func TestSaveRetrieval_EncryptsAndKicksBackfill(t *testing.T) {
	fm := &fakeModel{}
	bf := &fakeBackfill{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}, RetrievalBackfill: bf})
	defer srv.Close()

	code, body := doJSON(t, "PUT", srv.URL+"/api/models/retrieval", map[string]string{"api_key": "sk-test-1234"})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	saved := fm.savedRetrieval
	if saved == nil {
		t.Fatal("密钥未落库")
	}
	if string(saved.EncryptedAPIKey) != "sealed:sk-test-1234" {
		t.Fatalf("明文未经加密路径: %q", saved.EncryptedAPIKey)
	}
	if saved.APIKeyLast4 != "1234" {
		t.Fatalf("last4 = %q, want 1234", saved.APIKeyLast4)
	}
	if bf.kicks != 1 {
		t.Fatalf("保存后应触发一次即时补齐, got %d", bf.kicks)
	}
	r := body["retrieval"].(map[string]any)
	if r["key_present"] != true || r["key_last4"] != "1234" {
		t.Fatalf("响应应提示已设置与末 4 位: %v", r)
	}
}

// TestSaveRetrieval_EmptyKeyRejected：空密钥 400（清除走 DELETE），不触发补齐。
func TestSaveRetrieval_EmptyKeyRejected(t *testing.T) {
	fm := &fakeModel{}
	bf := &fakeBackfill{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}, RetrievalBackfill: bf})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/retrieval", map[string]string{"api_key": "  "})
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
	if fm.savedRetrieval != nil || bf.kicks != 0 {
		t.Fatal("空密钥不应落库或触发补齐")
	}
}

// TestClearRetrieval_Idempotent：未配置时清除也成功（幂等）。
func TestClearRetrieval_Idempotent(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}})
	defer srv.Close()

	for i := 0; i < 2; i++ {
		code, _ := doJSON(t, "DELETE", srv.URL+"/api/models/retrieval", nil)
		if code != http.StatusOK {
			t.Fatalf("第 %d 次 status=%d", i+1, code)
		}
	}
}

// TestDiagnoseRetrieval_PassesFormKey：带 api_key 时诊断表单当前值（不要求已保存）。
func TestDiagnoseRetrieval_PassesFormKey(t *testing.T) {
	fd := &fakeRetrievalDiagnoser{res: RetrievalDiagnosis{OK: true}}
	srv := newTestServer(t, Deps{Models: &fakeModel{}, RetrievalDiagnoser: fd})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/retrieval/diagnose", map[string]string{"api_key": "sk-form"})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if fd.gotAPIKey != "sk-form" {
		t.Fatalf("诊断应使用表单当前值, got %q", fd.gotAPIKey)
	}
}

// TestDiagnoseRetrieval_NoKeyAnywhere：未配置且未填 → 400（无法发起）。
func TestDiagnoseRetrieval_NoKeyAnywhere(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}, RetrievalDiagnoser: &fakeRetrievalDiagnoser{}})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/retrieval/diagnose", map[string]string{})
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}

// TestDiagnoseRetrieval_StoredKeyDecryptedByDiagnoser：未填时交给诊断实现读已存密钥，
// 诊断实现拿到的 apiKey 为空串；其 error 透传（500 + 中文原因）。
func TestDiagnoseRetrieval_StoredKeyDecryptedByDiagnoser(t *testing.T) {
	fd := &fakeRetrievalDiagnoser{err: errors.New("检索模型密钥无法解密，请重新填写")}
	srv := newTestServer(t, Deps{Models: &fakeModel{retrieval: llmconfig.RetrievalModel{EncryptedAPIKey: []byte("sealed:x")}}, RetrievalDiagnoser: fd})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/retrieval/diagnose", map[string]string{})
	if code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", code)
	}
	if fd.gotAPIKey != "" {
		t.Fatalf("未填时诊断实现应自己读已存密钥, got %q", fd.gotAPIKey)
	}
}
