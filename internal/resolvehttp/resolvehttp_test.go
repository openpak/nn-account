package resolvehttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWiiUNNID(t *testing.T) {
	s := &Server{key: "k", wiiuName: func(_ context.Context, id string) (string, int64, bool, error) {
		switch id {
		case "acc-1":
			return "MK8Player", 1800000123, true, nil
		case "boom":
			return "", 0, false, context.DeadlineExceeded
		}
		return "", 0, false, nil
	}}
	do := func(path, key string) (int, string) {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if key != "" {
			r.Header.Set("X-Internal-Key", key)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := do("/internal/wiiu-nnid?account_id=acc-1", "k"); code != 200 || !strings.Contains(body, `"nnid":"MK8Player"`) || !strings.Contains(body, `"pid":1800000123`) {
		t.Errorf("linked: %d %s", code, body)
	}
	if code, _ := do("/internal/wiiu-nnid?account_id=acc-1", ""); code != http.StatusUnauthorized {
		t.Errorf("no key: %d", code)
	}
	if code, _ := do("/internal/wiiu-nnid?account_id=acc-1", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong key: %d", code)
	}
	if code, _ := do("/internal/wiiu-nnid?account_id=nobody", "k"); code != http.StatusNotFound {
		t.Errorf("unlinked: %d", code)
	}
	if code, _ := do("/internal/wiiu-nnid", "k"); code != http.StatusBadRequest {
		t.Errorf("no account: %d", code)
	}
	if code, body := do("/internal/wiiu-nnid?account_id=boom", "k"); code != http.StatusInternalServerError || strings.Contains(body, "Deadline") {
		t.Errorf("error must not leak: %d %s", code, body)
	}
	empty := &Server{wiiuName: s.wiiuName}
	r := httptest.NewRequest(http.MethodGet, "/internal/wiiu-nnid?account_id=acc-1", nil)
	w := httptest.NewRecorder()
	empty.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("empty key must refuse everything: %d", w.Code)
	}
}
