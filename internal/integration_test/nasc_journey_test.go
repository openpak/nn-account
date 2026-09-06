//go:build integration

package integration_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/PretendoNetwork/grpc/go/account/v2"
	"google.golang.org/grpc/metadata"

	"openpak/nn-account/internal/nasc"
	"openpak/nn-account/internal/store"
)

var base64Std = base64.StdEncoding

// nenc mirrors the Nintendo base64 alphabet swap.
func nenc(s string) string {
	enc := base64Std.EncodeToString([]byte(s))
	r := strings.NewReplacer("+", ".", "/", "-", "=", "*")
	return r.Replace(enc)
}

func nencBytes(b []byte) string { return nenc(string(b)) }

func postNASC(t *testing.T, srv http.Handler, fields map[string]string) (int, string) {
	t.Helper()
	parts := make([]string, 0, len(fields))
	for k, v := range fields {
		parts = append(parts, k+"="+v)
	}
	req := httptest.NewRequest("POST", "/ac", strings.NewReader(strings.Join(parts, "&")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestNASC3DSJourney(t *testing.T) {
	core := startCore(t)
	nnasSrv, v2, pool := startAdapter(t, core)
	nascSrv := nasc.New(pool, core)

	// Fixture console identity.
	fcdcert := make([]byte, 0x110)
	_, _ = rand.Read(fcdcert)
	csnum := "C12345678"
	macadr := "002709abcdef" // Nintendo OUI 002709
	fcdcertHash := sha256b64(fcdcert)

	// --- 1. Device-only registration (title 0004013000003202 + passwd) ---
	code, body := postNASC(t, nascSrv, map[string]string{
		"action":     nenc("LOGIN"),
		"fcdcert":    nencBytes(fcdcert),
		"csnum":      nenc(csnum),
		"macadr":     nenc(macadr),
		"titleid":    nenc("0004013000003202"),
		"servertype": nenc("prod"),
		"gameid":     nenc("00000000"),
		"passwd":     nenc("3dsNEXsecret42"),
	})
	if code != 200 {
		t.Fatalf("nasc http %d", code)
	}
	// No server exists for the settings title: handler errors, but the
	// provisional NEX identity was created in the middleware (FR-2).
	if !strings.Contains(body, "returncd=") {
		t.Fatalf("bad nasc response: %s", body)
	}
	device, err := store.GetDeviceByCertHash(context.Background(), pool, fcdcertHash)
	if err != nil || len(device.LinkedPIDs) != 1 {
		t.Fatalf("device not registered: %+v %v", device, err)
	}
	nexPID := device.LinkedPIDs[0]
	prov, err := store.GetNEXAccountByPID(context.Background(), pool, nexPID)
	if err != nil || prov.OwningPID != nil || prov.DeviceType != "3ds" {
		t.Fatalf("provisional NEX account wrong: %+v %v", prov, err)
	}
	if prov.FriendCode == "" {
		t.Fatal("friend code missing")
	}

	// --- 2. LOGIN for a real title resolves the server and issues a token ---
	code, body = postNASC(t, nascSrv, map[string]string{
		"action":     nenc("LOGIN"),
		"fcdcert":    nencBytes(fcdcert),
		"csnum":      nenc(csnum),
		"macadr":     nenc(macadr),
		"titleid":    nenc("0005000010143500"),
		"servertype": nenc("prod"),
		"gameid":     nenc("00003200"),
		"userid":     nenc(itoa(int(nexPID))),
		"passwd":     nenc("3dsNEXsecret42"),
		"uidhmac":    nenc("hmac-value"),
	})
	if code != 200 || !strings.Contains(body, "returncd="+nenc("001")) {
		t.Fatalf("LOGIN failed: %d %s", code, body)
	}
	locator := between(body, "locator="+nenc(""), "&")
	if locator == "" {
		t.Fatalf("missing locator: %s", body)
	}
	token := between(body, "token="+nenc(""), "&")
	if token == "" {
		t.Fatalf("missing token: %s", body)
	}

	// --- 3. Exchange the NASC token (device-only identity; no PNID) ---
	gctx := metadata.AppendToOutgoingContext(context.Background(), "X-API-Key", "adapter-grpc-key-0123456789abcdef")
	ex, err := v2.ExchangeNEXTokenForUserData(gctx, &pb.ExchangeNEXTokenForUserDataRequest{
		Token:         token,
		GameServerIds: []string{"00003200"},
	})
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if ex.GetNexAccount().GetPid() != uint32(nexPID) {
		t.Fatalf("wrong nex pid: %d", ex.GetNexAccount().GetPid())
	}
	if ex.GetTokenInfo().GetSystemType() != 2 { // CTR
		t.Fatalf("expected CTR system type, got %v", ex.GetTokenInfo().GetSystemType())
	}
	// Audience mismatch still rejected.
	if _, err := v2.ExchangeNEXTokenForUserData(gctx, &pb.ExchangeNEXTokenForUserDataRequest{
		Token: token, GameServerIds: []string{"0000ffff"},
	}); err == nil {
		t.Fatal("expected audience rejection for NASC token")
	}

	// --- 4. SVCLOC issues a service token ---
	code, body = postNASC(t, nascSrv, map[string]string{
		"action":     nenc("SVCLOC"),
		"fcdcert":    nencBytes(fcdcert),
		"csnum":      nenc(csnum),
		"macadr":     nenc(macadr),
		"titleid":    nenc("0005000010143500"),
		"servertype": nenc("prod"),
		"gameid":     nenc("00003200"),
		"userid":     nenc(itoa(int(nexPID))),
		"uidhmac":    nenc("hmac-value"),
	})
	if code != 200 || !strings.Contains(body, "returncd="+nenc("007")) {
		t.Fatalf("SVCLOC failed: %d %s", code, body)
	}
	if !strings.Contains(body, "servicetoken=") {
		t.Fatalf("missing service token: %s", body)
	}

	// --- 5. Serial mismatch → 150; unknown pid → 102 ---
	code, body = postNASC(t, nascSrv, map[string]string{
		"action":     nenc("LOGIN"),
		"fcdcert":    nencBytes(fcdcert),
		"csnum":      nenc("C99999999"), // different serial, same cert
		"macadr":     nenc(macadr),
		"titleid":    nenc("0005000010143500"),
		"servertype": nenc("prod"),
		"gameid":     nenc("00003200"),
		"userid":     nenc(itoa(int(nexPID))),
	})
	if !strings.Contains(body, "returncd="+nenc("150")) {
		t.Fatalf("expected serial mismatch 150: %d %s", code, body)
	}

	// --- 6. Malformed MAC → null response ---
	code, body = postNASC(t, nascSrv, map[string]string{
		"action":     nenc("LOGIN"),
		"fcdcert":    nencBytes(fcdcert),
		"csnum":      nenc(csnum),
		"macadr":     nenc("aabbccddeeff"), // non-Nintendo OUI
		"titleid":    nenc("0005000010143500"),
		"servertype": nenc("prod"),
		"gameid":     nenc("00003200"),
	})
	if !strings.Contains(body, "returncd="+nenc("null")) {
		t.Fatalf("expected null for bad MAC: %d %s", code, body)
	}

	_ = nnasSrv
}

func sha256b64(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}
