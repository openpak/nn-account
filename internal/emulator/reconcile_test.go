package emulator

import (
	"context"
	"errors"
	"testing"
)

func TestReconcileOnce(t *testing.T) {
	pages := map[string][]string{"": {"a", "b"}, "b": {"c"}}
	next := map[string]string{"": "b", "b": ""}
	page := func(_ context.Context, after string) ([]string, string, error) {
		return pages[after], next[after], nil
	}
	got := map[string]int{}
	ensure := func(_ context.Context, ns, id string) (*Identity, error) {
		got[ns+":"+id]++
		if id == "b" && ns == "wiiu" {
			return nil, errors.New("boom")
		}
		return &Identity{}, nil
	}
	n, failed := reconcileOnce(context.Background(), page, ensure)
	if n != 3 || failed != 1 || len(got) != 6 {
		t.Fatalf("accounts=%d failed=%d calls=%v", n, failed, got)
	}
}
