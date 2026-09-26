package resolvehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	accountv1 "openpak/nn-account/internal/accountpb"
)

func onlineServer() (*Server, *[][]*accountv1.OnlinePlayer) {
	calls := &[][]*accountv1.OnlinePlayer{}
	known := map[uint32]string{
		1001: "11111111-1111-1111-1111-111111111111",
		1002: "22222222-2222-2222-2222-222222222222",
	}
	s := &Server{key: "k",
		resolve: func(_ context.Context, _ string, pid uint32) (string, error) { return known[pid], nil },
		mark: func(_ context.Context, p []*accountv1.OnlinePlayer) (int32, error) {
			*calls = append(*calls, p)
			return int32(len(p)), nil
		},
	}
	return s, calls
}

func post(s *Server, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/internal/online", strings.NewReader(body))
	r.Header.Set("X-Internal-Key", key)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestOnlineMarksKnownPIDs(t *testing.T) {
	s, calls := onlineServer()
	body := `{"namespace":"3ds","title_id":"0004000000ABCD00","pids":[1001,1002,1001,9]}`

	if w := post(s, "wrong", body); w.Code != 401 {
		t.Fatalf("bad key answered %d", w.Code)
	}
	w := post(s, "k", body)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got struct{ Marked, Unknown int }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Marked != 2 || got.Unknown != 1 {
		t.Fatalf("got %+v, want marked 2 unknown 1", got)
	}
	if len(*calls) != 1 || len((*calls)[0]) != 2 {
		t.Fatalf("core calls = %v", *calls)
	}
	for _, p := range (*calls)[0] {
		if p.GetNamespace() != "3ds" || p.GetTitleId() != "0004000000abcd00" {
			t.Errorf("player sent as %v", p)
		}
	}
}

func TestOnlineRejectsBadBodies(t *testing.T) {
	s, calls := onlineServer()
	many := make([]string, maxOnline+1)
	for i := range many {
		many[i] = fmt.Sprint(i + 1)
	}
	for name, body := range map[string]string{
		"over cap":      `{"namespace":"wiiu","pids":[` + strings.Join(many, ",") + `]}`,
		"bad namespace": `{"namespace":"switch","pids":[1001]}`,
		"not json":      `{`,
	} {
		if w := post(s, "k", body); w.Code != 400 {
			t.Errorf("%s answered %d", name, w.Code)
		}
	}
	if w := post(s, "k", `{"namespace":"wiiu","pids":[5]}`); w.Code != 200 || len(*calls) != 0 {
		t.Fatalf("unknown-only answered %d, core calls %d", w.Code, len(*calls))
	}
}
