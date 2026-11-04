package eraseapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const acct = "11111111-2222-3333-4444-555555555555"

func TestAuthAndShape(t *testing.T) {
	ran := 0
	do := func(key, header, id string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		mux.Handle("DELETE /internal/accounts/{account_id}/data", &Handler{Key: key, Erase: func(context.Context, string) (int, error) { ran++; return 4, nil }})
		req := httptest.NewRequest("DELETE", "/internal/accounts/"+id+"/data", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	for _, c := range []struct {
		key, header, id string
		want            int
	}{
		{"", "Bearer k", acct, 503},
		{"k", "", acct, 401},
		{"k", "Bearer nope", acct, 401},
		{"k", "Bearer k", "nope", 400},
	} {
		if rr := do(c.key, c.header, c.id); rr.Code != c.want {
			t.Fatalf("%+v: got %d", c, rr.Code)
		}
	}
	if ran != 0 {
		t.Fatal("erase ran without a valid key and id")
	}
	rr := do("k", "Bearer k", acct)
	if rr.Code != 200 || rr.Body.String() != "{\"rows\":4,\"service\":\"nn-account\"}\n" {
		t.Fatalf("%d %q", rr.Code, rr.Body.String())
	}
}
