//go:build integration

package integration_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/miicore"
	"openpak/nn-account/internal/store"
)

// The Wii U Mii is the account's Mii: uploaded into the core, read back from the core. pnids never holds one;
// an upload made while the core is down waits in pending_miis and Retry delivers it.
func TestMiiLivesInTheCore(t *testing.T) {
	core := startCore(t)
	srv, _, pool := startAdapter(t, core)
	ctx := context.Background()
	t.Cleanup(func() { store.MiiOverlay = nil })

	form := strings.NewReader(strings.Join([]string{
		"user_id=coremii", "password=consoleSecret99", "email.address=coremii@example.com",
		"mii.name=Coremii", "mii.data=AAAA", "country=US", "language=en", "region=1", "tz_name=EST5EDT",
	}, "&"))
	req := httptest.NewRequest("POST", "/v1/api/people", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("registration: %d %s", rec.Code, rec.Body.String())
	}
	pnid, err := store.GetPNIDByUsername(ctx, pool, "coremii")
	if err != nil {
		t.Fatal(err)
	}
	miicore.Install(core, pool)
	var queued int
	count := func() int {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pending_miis WHERE pid=$1`, pnid.PID).Scan(&queued); err != nil {
			t.Fatal(err)
		}
		return queued
	}

	// A Mii the core cannot read is refused and is not queued (retrying cannot help).
	if miicore.Save(ctx, core, pool, pnid.PID, pnid.AccountID, "AAAA") {
		t.Fatal("the core must refuse a 3-byte Mii")
	}
	if count() != 0 {
		t.Fatal("an unreadable Mii must not be queued")
	}

	// A core outage queues the upload; the console still sees it; Retry delivers it.
	down, err := coreclient.Dial(ctx, "127.0.0.1:1", "x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = down.Close() })
	if miicore.Save(ctx, down, pool, pnid.PID, pnid.AccountID, miicore.DefaultMiiData) {
		t.Fatal("a dead core cannot have taken the Mii")
	}
	if count() != 1 {
		t.Fatal("the upload must wait in pending_miis")
	}
	if sent, dropped := miicore.Retry(ctx, core, pool); sent != 1 || dropped != 0 || count() != 0 {
		t.Fatalf("retry: sent %d dropped %d queued %d", sent, dropped, count())
	}
	raw, err := core.GetMii(ctx, pnid.AccountID)
	if err != nil || base64.StdEncoding.EncodeToString(raw) != miicore.DefaultMiiData {
		t.Fatalf("core Mii: %v", err)
	}
	got, _ := store.GetPNIDByPID(ctx, pool, pnid.PID)
	if got.MiiData != miicore.DefaultMiiData {
		t.Fatalf("PNID must carry the core Mii, got %q", got.MiiData)
	}
}
