//go:build integration

// End-to-end for the emulator surface (NA-1/NA-1a): the identity Cemu asks
// for must carry a console password cache whose hex form the NNAS oauth20
// grant accepts — the exact login the emulator performs. Covers both identity
// origins: minted here (emulator-born) and registered from a console, where
// the cache is the transformed registration secret the console already holds.
package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openpak/nn-account/internal/emulator"
	"openpak/nn-account/internal/nnas"
	"openpak/nn-account/internal/store"
)

func getIdentity(t *testing.T, srv *emulator.Server, accountID string) emulator.Identity {
	t.Helper()
	req := httptest.NewRequest("GET", "/internal/wiiu/emulator?account_id="+accountID, nil)
	req.Header.Set("X-Internal-Key", "emulator-key-0123456789abcdef012345")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("identity failed: %d %s", rec.Code, rec.Body.String())
	}
	var id emulator.Identity
	if err := json.NewDecoder(rec.Body).Decode(&id); err != nil {
		t.Fatalf("identity decode: %v", err)
	}
	return id
}

func TestEmulatorIdentityLoginJourney(t *testing.T) {
	core := startCore(t)
	nnasSrv, _, pool := startAdapter(t, core)
	emSrv := emulator.New(pool, core, "emulator-key-0123456789abcdef012345")
	ctx := context.Background()

	// Internal key is required.
	noAuth := httptest.NewRequest("GET", "/internal/wiiu/emulator?account_id=x", nil)
	noAuthRec := httptest.NewRecorder()
	emSrv.ServeHTTP(noAuthRec, noAuth)
	if noAuthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without internal key, got %d", noAuthRec.Code)
	}

	// An account born on the emulator surface: minted by the core, unknown to
	// the adapter. Ensure mints PNID, NEX account, link and credential.
	reg, err := core.RegisterAccount(ctx, "wiiu", "emulator-user@example.com", "webPassword77", "emulatoruser", "US", "en")
	if err != nil {
		t.Fatalf("core register: %v", err)
	}
	id := getIdentity(t, emSrv, reg.GetAccountId())
	if id.PID == 0 || id.Username == "" || id.NEXPassword == "" || id.MiiData == "" {
		t.Fatalf("identity incomplete: %+v", id)
	}
	if id.PasswordCache == "" {
		t.Fatal("identity carries no password cache")
	}
	cache, err := base64.StdEncoding.DecodeString(id.PasswordCache)
	if err != nil || len(cache) != 32 {
		t.Fatalf("password cache bad: %q (%v)", id.PasswordCache, err)
	}

	// The NNAS login Cemu performs: password grant with hex(cache), user_id
	// the NNID username, exactly as napi_act sends it.
	oauthXML := postForm(t, nnasSrv, "/v1/api/oauth20/access_token/generate", []string{
		"grant_type=password", "user_id=" + id.Username, "password=" + hex.EncodeToString(cache),
	})
	accessToken := between(oauthXML, "<token>", "</token>")
	if accessToken == "" {
		t.Fatalf("emulator credential did not log in: %s", oauthXML)
	}

	// And the minted identity reaches a NEX game server with that token.
	nexXML := getXML(t, nnasSrv, "/v1/api/provider/nex_token/@me?game_server_id=00003200", accessToken, "0005000010143500")
	if between(nexXML, "<nex_password>", "</nex_password>") != id.NEXPassword {
		t.Fatalf("nex password mismatch: %s", nexXML)
	}

	// Stable across calls: the second Ensure returns the same cache.
	again := getIdentity(t, emSrv, reg.GetAccountId())
	if again.PasswordCache != id.PasswordCache || again.PID != id.PID || again.Username != id.Username {
		t.Fatalf("identity not stable: %+v vs %+v", again, id)
	}

	// An account born on a console: the cache on the row is the transformed
	// registration secret, and the identity hands it back so Cemu and the
	// console share one credential.
	form := strings.NewReader(strings.Join([]string{
		"user_id=consoleplayer",
		"password=consoleSecret99",
		"email.address=console-player@example.com",
		"mii.name=consoleplayer",
		"mii.data=AAAA",
		"country=US",
		"language=en",
		"region=1",
		"tz_name=EST5EDT",
	}, "&"))
	req := httptest.NewRequest("POST", "/v1/api/people", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	nnasSrv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("console registration failed: %d %s", rec.Code, rec.Body.String())
	}
	pnid, err := store.GetPNIDByPID(ctx, pool, parseInt(t, between(rec.Body.String(), "<pid>", "</pid>")))
	if err != nil {
		t.Fatalf("pnid lookup: %v", err)
	}
	consoleID := getIdentity(t, emSrv, pnid.AccountID)
	want := nnas.NintendoPasswordHash("consoleSecret99", pnid.PID)
	if consoleID.PasswordCache == "" {
		t.Fatal("console-registered identity carries no password cache")
	}
	if got, err := base64.StdEncoding.DecodeString(consoleID.PasswordCache); err != nil || hex.EncodeToString(got) != want {
		t.Fatalf("console cache mismatch: %v != %s (%v)", got, want, err)
	}
	oauthXML = postForm(t, nnasSrv, "/v1/api/oauth20/access_token/generate", []string{
		"grant_type=password", "user_id=consoleplayer", "password=" + hex.EncodeToString(mustDecode64(t, consoleID.PasswordCache)),
	})
	if between(oauthXML, "<token>", "</token>") == "" {
		t.Fatalf("console cache did not log in: %s", oauthXML)
	}

	// Unknown accounts answer 404, not a mint. Well-formed UUID, so the core
	// answers NotFound rather than failing on the id format (real callers
	// always pass token-derived ids).
	missing := httptest.NewRequest("GET", "/internal/wiiu/emulator?account_id=00000000-0000-0000-0000-000000000000", nil)
	missing.Header.Set("X-Internal-Key", "emulator-key-0123456789abcdef012345")
	missingRec := httptest.NewRecorder()
	emSrv.ServeHTTP(missingRec, missing)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown account, got %d", missingRec.Code)
	}
}

func mustDecode64(t *testing.T, s string) []byte {
	t.Helper()
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("bad base64 %q: %v", s, err)
	}
	return out
}
