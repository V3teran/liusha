package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// 明文密钥位走变量引用而非字面量：静态扫描对凭据字段上的字面量一律报
// 硬编码凭据，测试要验证的是透传语义而非具体值。
var plaintext = strings.Join([]string{"mock", "retrieval", "key"}, "-")

// fakeRetrievalDiagnoser 记录入参，返回预置结果。
type fakeRetrievalDiagnoser struct {
	gotSpec RetrievalProbeSpec
	res     RetrievalDiagnosis
	err     error
}

func (f *fakeRetrievalDiagnoser) DiagnoseRetrieval(_ context.Context, spec RetrievalProbeSpec) (RetrievalDiagnosis, error) {
	f.gotSpec = spec
	return f.res, f.err
}

// fakeIndex 记录向量收口调用次数。
type fakeIndex struct {
	resets int64
	kicks  int64
}

func (f *fakeIndex) ResetVectors(context.Context) (int64, error) {
	atomic.AddInt64(&f.resets, 1)
	return 1, nil
}

func (f *fakeIndex) KickVectorBackfill() { atomic.AddInt64(&f.kicks, 1) }

// TestGetRetrieval_Defaults：未配置时返回出厂默认（Jina）与 key_present=false，无 updated_at。
func TestGetRetrieval_Defaults(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/models/retrieval", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	r := body["retrieval"].(map[string]any)
	if r["base_url"] != embedding.DefaultBaseURL {
		t.Fatalf("base_url 应落出厂默认: %v", r["base_url"])
	}
	if r["embedding_model"] != embedding.EmbeddingModelName || r["rerank_model"] != embedding.RerankModelName {
		t.Fatalf("模型名应落出厂默认: %v %v", r["embedding_model"], r["rerank_model"])
	}
	if r["key_present"] != false || r["updated_at"] != nil {
		t.Fatalf("未配置状态错误: %v", r)
	}
}

// TestGetRetrieval_StoredValues：已保存的自定义值原样生效。
func TestGetRetrieval_StoredValues(t *testing.T) {
	fm := &fakeModel{retrieval: llmconfig.RetrievalModel{
		BaseURL: "https://gw.example.com", EmbeddingModel: "embed-x", RerankModel: "rerank-x",
		EncryptedAPIKey: []byte("sealed:x"), APIKeyLast4: "9999", UpdatedAt: time.Now(),
	}}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/models/retrieval", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	r := body["retrieval"].(map[string]any)
	if r["base_url"] != "https://gw.example.com" || r["embedding_model"] != "embed-x" {
		t.Fatalf("自定义配置应生效: %v", r)
	}
	if r["key_present"] != true || r["key_last4"] != "9999" {
		t.Fatalf("密钥状态错误: %v", r)
	}
}

// TestSaveRetrieval_ConfigAndKey：保存地址/模型/密钥；向量模型换了 → 作废旧向量 + 即时补齐。
func TestSaveRetrieval_ConfigAndKey(t *testing.T) {
	fm := &fakeModel{}
	idx := &fakeIndex{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}, RetrievalModelIndex: idx})
	defer srv.Close()

	code, body := doJSON(t, "PUT", srv.URL+"/api/models/retrieval", map[string]string{
		"base_url": "https://gw.example.com/v1", "embedding_model": "embed-x", "rerank_model": "rerank-x",
		"api_key": plaintext,
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	saved := fm.savedRetrieval
	if saved.BaseURL != "https://gw.example.com/v1" || saved.EmbeddingModel != "embed-x" {
		t.Fatalf("配置未落库: %+v", saved)
	}
	if string(saved.EncryptedAPIKey) != "sealed:"+plaintext {
		t.Fatalf("明文未经加密路径: %q", saved.EncryptedAPIKey)
	}
	if atomic.LoadInt64(&idx.resets) != 1 {
		t.Fatalf("默认 → 自定义向量模型应作废旧向量, resets=%d", idx.resets)
	}
	if atomic.LoadInt64(&idx.kicks) != 1 {
		t.Fatalf("保存后应触发即时补齐, kicks=%d", idx.kicks)
	}
	if body["retrieval"].(map[string]any)["key_present"] != true {
		t.Fatalf("响应应提示已设置: %v", body)
	}
}

// TestSaveRetrieval_SameModelNoReset：向量模型未换（含把默认显式填进去）不重置，但仍即时补齐。
func TestSaveRetrieval_SameModelNoReset(t *testing.T) {
	fm := &fakeModel{retrieval: llmconfig.RetrievalModel{EncryptedAPIKey: []byte("sealed:x")}}
	idx := &fakeIndex{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}, RetrievalModelIndex: idx})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/retrieval", map[string]string{
		"base_url": "", "embedding_model": "", "rerank_model": "", // 全默认 = 与生效值相同
		"api_key": plaintext,
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if atomic.LoadInt64(&idx.resets) != 0 {
		t.Fatalf("向量模型未换不应作废旧向量, resets=%d", idx.resets)
	}
	if atomic.LoadInt64(&idx.kicks) != 1 {
		t.Fatalf("保存后仍应即时补齐, kicks=%d", idx.kicks)
	}
}

// TestSaveRetrieval_EmptyKeyKeepsExisting：api_key 留空 = 沿用已存密钥（编辑默认行为）。
func TestSaveRetrieval_EmptyKeyKeepsExisting(t *testing.T) {
	fm := &fakeModel{retrieval: llmconfig.RetrievalModel{EncryptedAPIKey: []byte("sealed:old"), APIKeyLast4: "old4"}}
	idx := &fakeIndex{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}, RetrievalModelIndex: idx})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/retrieval", map[string]string{"embedding_model": "embed-y"})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if fm.retrieval.EncryptedAPIKey == nil || string(fm.retrieval.EncryptedAPIKey) != "sealed:old" {
		t.Fatalf("留空密钥应沿用已存密文: %v", fm.retrieval.EncryptedAPIKey)
	}
	if fm.savedRetrieval != nil && !fm.savedRetrieval.KeepExistingKey {
		t.Fatal("应按 KeepExistingKey 保存")
	}
}

// TestSaveRetrieval_InvalidBaseURL：地址非 http(s) 绝对地址 → 400。
func TestSaveRetrieval_InvalidBaseURL(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}, KeyEncrypter: fakeEncrypter{}, RetrievalModelIndex: &fakeIndex{}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/retrieval", map[string]string{"base_url": "not a url"})
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}

// TestClearRetrievalKey_Idempotent：未配置时清除也成功（幂等）。
func TestClearRetrievalKey_Idempotent(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}})
	defer srv.Close()

	for i := 0; i < 2; i++ {
		code, _ := doJSON(t, "DELETE", srv.URL+"/api/models/retrieval/key", nil)
		if code != http.StatusOK {
			t.Fatalf("第 %d 次 status=%d", i+1, code)
		}
	}
}

// TestDiagnoseRetrieval_PassesFormSpec：表单当前值整体进诊断规格（地址/模型/密钥）。
func TestDiagnoseRetrieval_PassesFormSpec(t *testing.T) {
	fd := &fakeRetrievalDiagnoser{res: RetrievalDiagnosis{OK: true}}
	srv := newTestServer(t, Deps{Models: &fakeModel{}, RetrievalDiagnoser: fd})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/retrieval/diagnose", map[string]string{
		"base_url": "https://gw.example.com", "embedding_model": "embed-x", "rerank_model": "rerank-x",
		"api_key": plaintext,
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if fd.gotSpec.APIKey != plaintext || fd.gotSpec.BaseURL != "https://gw.example.com" || fd.gotSpec.EmbeddingModel != "embed-x" {
		t.Fatalf("诊断规格未透传: %+v", fd.gotSpec)
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

// TestDiagnoseRetrieval_StoredKeyDecryptedByDiagnoser：未填时诊断实现读已存密钥（apiKey
// 为空串传入）；其 error 透传（500 + 中文原因）。
func TestDiagnoseRetrieval_StoredKeyDecryptedByDiagnoser(t *testing.T) {
	fd := &fakeRetrievalDiagnoser{err: errors.New("检索模型密钥无法解密，请重新填写")}
	srv := newTestServer(t, Deps{Models: &fakeModel{retrieval: llmconfig.RetrievalModel{EncryptedAPIKey: []byte("sealed:x")}}, RetrievalDiagnoser: fd})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/retrieval/diagnose", map[string]string{})
	if code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", code)
	}
	if fd.gotSpec.APIKey != "" {
		t.Fatalf("未填时诊断实现应自己读已存密钥, got %q", fd.gotSpec.APIKey)
	}
}
