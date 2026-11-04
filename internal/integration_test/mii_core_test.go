//go:build integration

package integration_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"openpak/nn-account/internal/miicore"
	"openpak/nn-account/internal/store"
)

// The Wii U Mii is the account's Mii: uploaded into the core, read back from
// the core, and any copy left in pnids moves across once.
func TestMiiLivesInTheCore(t *testing.T) {
	core := startCore(t)
	srv, _, pool := startAdapter(t, core)
	ctx := context.Background()
	t.Cleanup(func() { store.MiiOverlay = nil })

	// Registered with a Mii the core cannot read: it stays in pnids as a landing place.
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

	if miicore.Save(ctx, core, pool, pnid.PID, pnid.AccountID, "AAAA") {
		t.Fatal("the core must refuse a 3-byte Mii")
	}
	got, _ := store.GetPNIDByPID(ctx, pool, pnid.PID)
	if got.MiiData != "AAAA" {
		t.Fatalf("refused upload must stay local, got %q", got.MiiData)
	}

	// A real upload goes into the core and the local copy is cleared.
	if !miicore.Save(ctx, core, pool, pnid.PID, pnid.AccountID, miicore.DefaultMiiData) {
		t.Fatal("core refused a valid Mii")
	}
	var local string
	if err := pool.QueryRow(ctx, `SELECT mii_data FROM pnids WHERE pid=$1`, pnid.PID).Scan(&local); err != nil || local != "" {
		t.Fatalf("local copy after save: %q %v", local, err)
	}
	raw, err := core.GetMii(ctx, pnid.AccountID)
	if err != nil || base64.StdEncoding.EncodeToString(raw) != miicore.DefaultMiiData {
		t.Fatalf("core Mii: %v", err)
	}
	got, _ = store.GetPNIDByPID(ctx, pool, pnid.PID)
	if got.MiiData != miicore.DefaultMiiData {
		t.Fatalf("PNID must carry the core Mii, got %q", got.MiiData)
	}

	// Startup migration moves a leftover local Mii for an account with no core Mii,
	// and leaves an account whose core Mii exists alone.
	if _, err := pool.Exec(ctx, `UPDATE pnids SET mii_data='AAAA' WHERE pid=$1`, pnid.PID); err != nil {
		t.Fatal(err)
	}
	moved, kept := miicore.MigrateAll(ctx, core, pool)
	if moved != 0 || kept != 1 {
		t.Fatalf("migrate with a core Mii present: moved %d kept %d", moved, kept)
	}
	if err := pool.QueryRow(ctx, `SELECT mii_data FROM pnids WHERE pid=$1`, pnid.PID).Scan(&local); err != nil || local != "" {
		t.Fatalf("stale local copy must be dropped: %q %v", local, err)
	}
}
