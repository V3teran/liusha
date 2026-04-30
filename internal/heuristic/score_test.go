package heuristic

import "testing"

type fakeFlow struct {
	Method  string
	URL     string
	Headers map[string]string
}

func (f fakeFlow) GetMethod() string         { return f.Method }
func (f fakeFlow) GetURL() string            { return f.URL }
func (f fakeFlow) GetHeader(k string) string { return f.Headers[k] }

func TestScore_StaticAsset_Zero(t *testing.T) {
	cases := []string{"/style.css", "/app.js", "/logo.png", "/font.woff2", "/image.gif"}
	for _, u := range cases {
		s := Score(fakeFlow{Method: "GET", URL: u})
		if s != 0 {
			t.Errorf("static %q score=%d want 0", u, s)
		}
	}
}

func TestScore_AdminPath_HighScore(t *testing.T) {
	s := Score(fakeFlow{
		Method:  "GET",
		URL:     "/api/admin/users",
		Headers: map[string]string{"cookie": "session=x"},
	})
	if s < 30 {
		t.Errorf("admin path score=%d want >=30", s)
	}
}

func TestScore_IDORQuery(t *testing.T) {
	s := Score(fakeFlow{
		Method:  "GET",
		URL:     "/api/order?uid=1",
		Headers: map[string]string{"cookie": "session=x"},
	})
	if s < 30 {
		t.Errorf("idor query score=%d want >=30", s)
	}
}

func TestScore_StateChangePost(t *testing.T) {
	s := Score(fakeFlow{
		Method:  "POST",
		URL:     "/api/order/cancel",
		Headers: map[string]string{"cookie": "session=x"},
	})
	if s < 30 {
		t.Errorf("post + cookie score=%d want >=30", s)
	}
}

func TestScore_SimpleProfile_LowScore(t *testing.T) {
	s := Score(fakeFlow{
		Method:  "GET",
		URL:     "/api/profile",
		Headers: map[string]string{"cookie": "session=x"},
	})
	if s < 20 || s >= 30 {
		t.Errorf("profile only score=%d want 20-29", s)
	}
}

func TestThreshold_Default(t *testing.T) {
	if Threshold() != 30 {
		t.Errorf("threshold=%d want 30", Threshold())
	}
}
