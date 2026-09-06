//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openpak/nn-account/internal/nnas"
	"openpak/nn-account/internal/store"
)

func TestConnTestAndCBVC(t *testing.T) {
	core := startCore(t)
	srv, _, _ := startAdapter(t, core)

	req := httptest.NewRequest("GET", "/conntest", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "This is test.html page") {
		t.Fatalf("conntest failed: %d", rec.Code)
	}
	if rec.Header().Get("X-Organization") != "Nintendo" {
		t.Fatal("missing X-Organization header")
	}

	cbvc := httptest.NewRequest("GET", "/cbvc/CTR/1/US", nil)
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, cbvc)
	if rec2.Code != 200 || rec2.Body.String() != "0" {
		t.Fatalf("cbvc failed: %d %s", rec2.Code, rec2.Body.String())
	}
	_ = core
}

func TestAccountSettingsApplet(t *testing.T) {
	core := startCore(t)
	srv, _, pool := startAdapter(t, core)

	// Register a console account (reuses the journey helpers).
	form := strings.NewReader(strings.Join([]string{
		"user_id=settingsguy", "password=consoleSecret99",
		"email.address=settings@example.com", "mii.name=settingsguy",
		"mii.data=AAAA", "country=US", "language=en", "region=1", "tz_name=EST5EDT",
	}, "&"))
	req := httptest.NewRequest("POST", "/v1/api/people", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("registration failed: %d %s", rec.Code, rec.Body.String())
	}
	pidStr := between(rec.Body.String(), "<pid>", "</pid>")

	// Mint a settings service token for the Wii U settings client.
	now := time.Now()
	err := store.InsertIndependentServiceToken(context.Background(), pool,
		"settings-token-1", nnas.WiiUSettingsClientID(), parseInt(t, pidStr), 0, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// Profile page renders.
	pageReq := httptest.NewRequest("GET", "/v1/api/account_settings/ui/profile", nil)
	pageReq.Header.Set("X-Nintendo-Service-Token", "settings-token-1")
	pageRec := httptest.NewRecorder()
	srv.ServeHTTP(pageRec, pageReq)
	if pageRec.Code != 200 || !strings.Contains(pageRec.Body.String(), "settingsguy") {
		t.Fatalf("settings page failed: %d", pageRec.Code)
	}

	// Update applies adapter-owned fields.
	updReq := httptest.NewRequest("POST", "/v1/api/account_settings/update",
		strings.NewReader("tz_name=PST8PDT&country=US&region=1&mii.name=newname&mii.data=BBBB"))
	updReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updReq.Header.Set("X-Nintendo-Service-Token", "settings-token-1")
	updRec := httptest.NewRecorder()
	srv.ServeHTTP(updRec, updReq)
	if updRec.Code != http.StatusFound || !strings.Contains(updRec.Header().Get("Location"), "notice=saved") {
		t.Fatalf("update failed: %d %s", updRec.Code, updRec.Header().Get("Location"))
	}
	pnid, err := store.GetPNIDByUsername(context.Background(), pool, "settingsguy")
	if err != nil {
		t.Fatal(err)
	}
	if pnid.TimezoneName != "PST8PDT" || pnid.MiiName != "newname" || pnid.MiiData != "BBBB" {
		t.Fatalf("update not applied: %+v", pnid)
	}

	// Bad/expired token → 504 (upstream semantics).
	badReq := httptest.NewRequest("GET", "/v1/api/account_settings/ui/profile", nil)
	badReq.Header.Set("X-Nintendo-Service-Token", "wrong-token")
	badRec := httptest.NewRecorder()
	srv.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 for bad token, got %d", badRec.Code)
	}
}
