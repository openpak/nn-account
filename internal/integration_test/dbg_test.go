//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"openpak/nn-account/internal/store"
)

func TestTokenLookupDebug(t *testing.T) {
	pool, err := store.Connect(context.Background(), "postgres://postgres:test@127.0.0.1:54329/nn_adapter_test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pnid, err := store.GetPNIDByUsername(context.Background(), pool, "testplayer1")
	if err != nil {
		t.Skipf("no pnid: %v", err)
	}
	raw := "deadbeef"
	now := time.Now()
	if err := store.InsertOAuthToken(context.Background(), pool, raw, "cid", pnid.PID, "oauth_access", 0, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPNIDByAccessToken(context.Background(), pool, raw)
	fmt.Printf("lookup: %+v err=%v\n", got, err)
}
