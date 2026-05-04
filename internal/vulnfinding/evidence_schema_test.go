package vulnfinding

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidateEvidence 覆盖 evidence schema 校验的接受 / 拒绝路径。
func TestValidateEvidence(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		evidence  string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "BAC kind with full fields passes",
			kind: "bac.unauthorized_access",
			evidence: `{
				"violating_identities":["anonymous"],
				"responses":[{"identity":"anonymous","status_code":200}]
			}`,
		},
		{
			name: "BAC kind with reasoning passes",
			kind: "bac.horizontal_priv_esc",
			evidence: `{
				"violating_identities":["user1","user2"],
				"responses":[{"identity":"user1","status_code":200}],
				"reasoning":"both users hit Alice's data"
			}`,
		},
		{
			name:      "BAC kind missing violating_identities is rejected",
			kind:      "bac.vertical_priv_esc",
			evidence:  `{"responses":[{"identity":"user1","status_code":200}]}`,
			wantErr:   true,
			errSubstr: "violating_identities",
		},
		{
			name:      "BAC kind empty violating_identities array is rejected",
			kind:      "bac.unauthorized_access",
			evidence:  `{"violating_identities":[],"responses":[{"identity":"u","status_code":200}]}`,
			wantErr:   true,
			errSubstr: "violating_identities",
		},
		{
			name:      "BAC kind missing responses is rejected",
			kind:      "bac.unauthorized_access",
			evidence:  `{"violating_identities":["anonymous"]}`,
			wantErr:   true,
			errSubstr: "responses",
		},
		{
			name:      "BAC kind with empty identity in responses is rejected",
			kind:      "bac.horizontal_priv_esc",
			evidence:  `{"violating_identities":["u1"],"responses":[{"identity":"","status_code":200}]}`,
			wantErr:   true,
			errSubstr: "identity",
		},
		{
			name:      "BAC kind invalid JSON is rejected",
			kind:      "bac.unauthorized_access",
			evidence:  `{not json`,
			wantErr:   true,
			errSubstr: "BAC evidence",
		},
		{
			name:     "non-BAC kind is unconstrained",
			kind:     "ssrf.classic",
			evidence: `{"any_field":"any_value"}`,
		},
		{
			name:     "empty evidence passes for any kind",
			kind:     "bac.unauthorized_access",
			evidence: ``,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEvidence(tc.kind, json.RawMessage(tc.evidence))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got %v", tc.errSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected pass, got %v", err)
			}
		})
	}
}
