//go:build integration

// End-to-end: Go adapter against a real account core subprocess.
// Covers the M2 console journey: register → sign in → NEX token →
// ExchangeNEXTokenForUserData, plus negative checks (PRD §10).
package integration_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/PretendoNetwork/grpc/go/account/v2"

	accountv1 "openpak/nn-account/internal/accountpb"

	"openpak/nn-account/internal/config"
	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/grpcv2"
	"openpak/nn-account/internal/nnas"
	"openpak/nn-account/internal/resolution"
	"openpak/nn-account/internal/store"
	resolutionv1 "openpak/nn-account/proto/resolution/v1"
)

const (
	coreDBURL = "postgres://postgres:test@127.0.0.1:54329/nn_core_test?sslmode=disable"
	// adapter DB lives in the same test postgres instance
)

func resetDB(t *testing.T, url string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(context.Background(), `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	if err != nil {
		t.Fatal(err)
	}
}

// startCore builds and runs the account core subprocess; returns the client.
func startCore(t *testing.T) *coreclient.Client {
	t.Helper()
	resetDB(t, coreDBURL)

	// Resolve the sibling core repo relative to this file.
	_, thisFile, _, _ := runtime.Caller(0)
	coreDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "account")
	bin := filepath.Join(t.TempDir(), "account-core")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "openpak/account/cmd/account")
	build.Dir = coreDir
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build core: %v\n%s", err, out)
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"ACCOUNT_DATABASE_URL="+coreDBURL,
		"ACCOUNT_SESSION_SECRET=0123456789abcdef0123456789abcdef",
		"ACCOUNT_INTERNAL_KEY=core-internal-key-0123456789abcdef0123",
		"ACCOUNT_ENVIRONMENT=development",
		"ACCOUNT_GRPC_ADDR=127.0.0.1:17071",
		"ACCOUNT_HTTP_ADDR=127.0.0.1:17081",
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	// Wait for readiness.
	client := &http.Client{Timeout: time.Second}
	ready := false
	for i := 0; i < 50; i++ {
		resp, err := client.Get("http://127.0.0.1:17081/readyz")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatal("core did not become ready")
	}

	core, err := coreclient.Dial(context.Background(), "127.0.0.1:17071", "core-internal-key-0123456789abcdef0123")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	return core
}

func startAdapter(t *testing.T, core *coreclient.Client) (*nnas.Server, pb.AccountServiceClient, *pgxpool.Pool) {
	t.Helper()
	resetDB(t, strings.Replace(coreDBURL, "nn_core_test", "nn_adapter_test", 1))

	adapterDB := strings.Replace(coreDBURL, "nn_core_test", "nn_adapter_test", 1)
	pool, err := store.Connect(context.Background(), adapterDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	// Seed a game server (friends' game-server id 00003200 per PRD §3).
	_, err = pool.Exec(context.Background(), `INSERT INTO servers
		(game_server_id, access_mode, device, client_id, service_name, title_ids, ip, port, aes_key)
		VALUES ('00003200','prod',1,'test-client-id','friends',ARRAY['0005000010143500'],'127.0.0.1',60000,'0123456789abcdef0123456789abcdef')`)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		CoreAddress: "127.0.0.1:17071",
		CDNBaseURL:  "https://cdn.test",
	}
	srv := nnas.New(pool, core, cfg)

	// gRPC v2 on bufconn.
	lis := bufconn.Listen(1024 * 1024)
	gsrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcv2.Interceptor("adapter-grpc-key-0123456789abcdef")))
	pb.RegisterAccountServiceServer(gsrv, grpcv2.New(pool, core, cfg.CDNBaseURL))
	go gsrv.Serve(lis)
	t.Cleanup(gsrv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, pb.NewAccountServiceClient(conn), pool
}

func TestAdapterConsoleJourney(t *testing.T) {
	core := startCore(t)
	nnasSrv, v2, pool := startAdapter(t, core)

	// --- 1. Console registration (POST /v1/api/people) ---
	form := strings.NewReader(strings.Join([]string{
		"user_id=testplayer1",
		"password=consoleSecret99",
		"email.address=player1@example.com",
		"mii.name=testplayer1",
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
		t.Fatalf("registration failed: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<pid>") {
		t.Fatalf("expected pid in response: %s", rec.Body.String())
	}
	pidStr := between(rec.Body.String(), "<pid>", "</pid>")
	if pidStr == "" {
		t.Fatal("empty pid")
	}
	t.Logf("registered pid %s", pidStr)

	// PNID + NEX account + active core link must exist.
	ctx := context.Background()
	pnid, err := store.GetPNIDByPID(ctx, pool, parseInt(t, pidStr))
	if err != nil {
		t.Fatalf("pnid lookup: %v", err)
	}
	if _, err := store.GetNEXAccountByOwningPID(ctx, pool, pnid.PID); err != nil {
		t.Fatalf("nex account: %v", err)
	}
	link, err := core.GetActiveLink(ctx, "wiiu", pidStr)
	if err != nil || link.GetAccountId() != pnid.AccountID {
		t.Fatalf("core link: %+v %v", link, err)
	}

	// --- 2. Console sign-in (password grant, transformed credential) ---
	transformed := nnas.NintendoPasswordHash("consoleSecret99", pnid.PID)
	oauthXML := postForm(t, nnasSrv, "/v1/api/oauth20/access_token/generate", []string{
		"grant_type=password", "user_id=testplayer1", "password=" + transformed,
	})
	accessToken := between(oauthXML, "<token>", "</token>")
	refreshToken := between(oauthXML, "<refresh_token>", "</refresh_token>")
	if accessToken == "" || refreshToken == "" {
		t.Fatalf("oauth response bad: %s", oauthXML)
	}

	// Wrong credential rejected with 0106.
	bad := postForm(t, nnasSrv, "/v1/api/oauth20/access_token/generate", []string{
		"grant_type=password", "user_id=testplayer1", "password=wrongpassword00",
	})
	if !strings.Contains(bad, "0106") {
		t.Fatalf("expected 0106 for bad credential: %s", bad)
	}

	// --- 3. NEX token issuance (audience-bound) ---
	nexXML := getXML(t, nnasSrv, "/v1/api/provider/nex_token/@me?game_server_id=00003200", accessToken, "0005000010143500")
	nexToken := between(nexXML, "<token>", "</token>")
	nexPassword := between(nexXML, "<nex_password>", "</nex_password>")
	if nexToken == "" || nexPassword == "" {
		t.Fatalf("nex token response bad: %s", nexXML)
	}

	// The NEX token remembers its client: no header is the console, the
	// emulator's X-OpenPak-Client names it.
	res := resolution.New(pool, core)
	// An unknown PID is "not found", not an internal error (it answered 500 until v0.8.3).
	if r, err := res.ResolvePid(context.Background(), &resolutionv1.ResolvePidRequest{Namespace: "wiiu", Pid: 1}); err != nil || r.GetFound() {
		t.Fatalf("unknown pid: %+v %v", r, err)
	}
	if c, err := res.ResolveNexTokenClient(context.Background(), &resolutionv1.ResolveNexTokenClientRequest{Token: nexToken}); err != nil ||
		!c.GetFound() || c.GetClient() != "wiiu" || c.GetPid() != uint32(pnid.PID) {
		t.Fatalf("console token client: %+v %v", c, err)
	}
	cemuReq := httptest.NewRequest("GET", "/v1/api/provider/nex_token/@me?game_server_id=00003200", nil)
	cemuReq.Header.Set("Authorization", "Bearer "+accessToken)
	cemuReq.Header.Set("X-Nintendo-Title-ID", "0005000010143500")
	cemuReq.Header.Set("X-OpenPak-Client", "cemu/2.6")
	cemuRec := httptest.NewRecorder()
	nnasSrv.ServeHTTP(cemuRec, cemuReq)
	cemuToken := between(cemuRec.Body.String(), "<token>", "</token>")
	if cemuRec.Code != 200 || cemuToken == "" {
		t.Fatalf("cemu nex token: %d %s", cemuRec.Code, cemuRec.Body.String())
	}
	if c, err := res.ResolveNexTokenClient(context.Background(), &resolutionv1.ResolveNexTokenClientRequest{Token: cemuToken}); err != nil ||
		!c.GetFound() || c.GetClient() != "cemu" {
		t.Fatalf("cemu token client: %+v %v", c, err)
	}

	// Service token issuance (review P1 #5: the stale access_level helper
	// made this lookup fail with SQLSTATE 42703; asserted end-to-end now).
	svcCode, svcBody := getXMLWithCode(t, nnasSrv, "/v1/api/provider/service_token/@me?client_id=test-client-id", accessToken, "0005000010143500")
	if svcCode != 200 || !strings.Contains(svcBody, "<service_token>") {
		t.Fatalf("service token failed: %d %s", svcCode, svcBody)
	}

	// --- 4. ExchangeNEXTokenForUserData (the friends contract) ---
	gctx := metadata.AppendToOutgoingContext(context.Background(), "X-API-Key", "adapter-grpc-key-0123456789abcdef")
	ex, err := v2.ExchangeNEXTokenForUserData(gctx, &pb.ExchangeNEXTokenForUserDataRequest{
		Token:         nexToken,
		GameServerIds: []string{"00003200"},
	})
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if ex.GetNexAccount().GetPid() != uint32(pnid.PID) || ex.GetTokenInfo().GetTokenType() != pb.TokenType_TOKEN_TYPE_NEX {
		t.Fatalf("unexpected exchange result: %+v", ex.GetNexAccount())
	}

	// Audience mismatch (wrong game server) must be rejected — upstream TODO fix.
	if _, err := v2.ExchangeNEXTokenForUserData(gctx, &pb.ExchangeNEXTokenForUserDataRequest{
		Token:         nexToken,
		GameServerIds: []string{"0000ffff"},
	}); err == nil {
		t.Fatal("expected audience mismatch rejection")
	}

	// Missing API key rejected.
	if _, err := v2.GetNEXPassword(context.Background(), &pb.GetNEXPasswordRequest{Pid: uint32(pnid.PID)}); err == nil {
		t.Fatal("expected unauthenticated GetNEXPassword")
	}

	// GetNEXPassword with API key works and returns the NEX secret.
	np, err := v2.GetNEXPassword(gctx, &pb.GetNEXPasswordRequest{Pid: uint32(pnid.PID)})
	if err != nil || np.GetPassword() != nexPassword {
		t.Fatalf("GetNEXPassword: %+v %v", np, err)
	}

	// --- 5. Fail-closed on core outage ---
	// Point the nnas server at a dead core client briefly.
	deadCore, err := coreclient.Dial(ctx, "127.0.0.1:1", "x") // unreachable
	if err != nil {
		t.Fatal(err)
	}
	closedSrv := nnas.New(pool, deadCore, &config.Config{CDNBaseURL: "https://cdn.test"})
	badOutage := postForm(t, closedSrv, "/v1/api/oauth20/access_token/generate", []string{
		"grant_type=password", "user_id=testplayer1", "password=" + transformed,
	})
	if !strings.Contains(badOutage, "0106") {
		t.Fatalf("expected fail-closed 0106 on core outage: %s", badOutage)
	}
	_ = deadCore.Close()

	// --- 7. Devices echo + mapped_ids + deletion (FR-8 stage one) ---
	devReq := httptest.NewRequest("GET", "/v1/api/people/@me/devices", nil)
	devReq.Header.Set("Authorization", "Bearer "+accessToken)
	for k, v := range map[string]string{
		"X-Nintendo-Device-ID": "DEV123", "Accept-Language": "en",
		"X-Nintendo-Platform-ID": "1", "X-Nintendo-Region": "US",
		"X-Nintendo-Serial-Number": "SER1", "X-Nintendo-System-Version": "5501",
	} {
		devReq.Header.Set(k, v)
	}
	devRec := httptest.NewRecorder()
	nnasSrv.ServeHTTP(devRec, devReq)
	if devRec.Code != 200 || !strings.Contains(devRec.Body.String(), "<status>ACTIVE</status>") {
		t.Fatalf("devices failed: %d %s", devRec.Code, devRec.Body.String())
	}

	mapped := httptest.NewRequest("GET", "/v1/api/admin/mapped_ids?input_type=pid&output_type=user_id&input="+pidStr, nil)
	mappedRec := httptest.NewRecorder()
	nnasSrv.ServeHTTP(mappedRec, mapped)
	if !strings.Contains(mappedRec.Body.String(), "<user_id>testplayer1</user_id>") {
		t.Fatalf("mapped_ids failed: %s", mappedRec.Body.String())
	}

	// --- 6a. Console deletion (FR-8 stage one; allowed from banned too)
	delReq := httptest.NewRequest("POST", "/v1/api/people/@me/deletion", nil)
	delReq.Header.Set("Authorization", "Bearer "+accessToken)
	delRec := httptest.NewRecorder()
	nnasSrv.ServeHTTP(delRec, delReq)
	if delRec.Code != 200 {
		t.Fatalf("deletion failed: %d %s", delRec.Code, delRec.Body.String())
	}
	acctAfter, err := core.GetAccount(ctx, pnid.AccountID)
	if err != nil || acctAfter.GetStatus() != accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING {
		t.Fatalf("expected deletion_pending, got %+v %v", acctAfter, err)
	}

	corePool, err := pgxpool.New(ctx, coreDBURL)
	if err != nil {
		t.Fatal(err)
	}
	defer corePool.Close()
	if _, err := corePool.Exec(ctx, `UPDATE accounts SET status='banned' WHERE id=$1`, pnid.AccountID); err != nil {
		t.Fatal(err)
	}
	banned := postForm(t, nnasSrv, "/v1/api/oauth20/access_token/generate", []string{
		"grant_type=password", "user_id=testplayer1", "password=" + transformed,
	})
	if !strings.Contains(banned, "0108") {
		t.Fatalf("expected 0108 banned: %s", banned)
	}

	// The game servers learn the ban from the NEX credential lookups; the NEX
	// titles map this exact refusal (InvalidArgument, "banned" in the message)
	// to RendezVous::AccountDisabled, so pin it here.
	for name, call := range map[string]func() error{
		"GetNEXPassword": func() error {
			_, err := v2.GetNEXPassword(gctx, &pb.GetNEXPasswordRequest{Pid: uint32(pnid.PID)})
			return err
		},
		"GetNEXData": func() error {
			_, err := v2.GetNEXData(gctx, &pb.GetNEXDataRequest{Pid: uint32(pnid.PID)})
			return err
		},
	} {
		err := call()
		if st := status.Convert(err); st.Code() != codes.InvalidArgument || !strings.Contains(st.Message(), "banned") {
			t.Fatalf("%s for a banned owner: want InvalidArgument \"...banned...\", got %v", name, err)
		}
	}

	// Existing token also fails after ban+deletion: local tokens were
	// revoked at deletion, so 401 (invalid token) is the expected outcome.
	bannedTok, bannedBody := getXMLWithCode(t, nnasSrv, "/v1/api/provider/nex_token/@me?game_server_id=00003200", accessToken, "0005000010143500")
	if bannedTok != http.StatusUnauthorized || !strings.Contains(bannedBody, "0005") {
		t.Fatalf("expected revoked token 401/0005, got %d %s", bannedTok, bannedBody)
	}

	// --- 8. Username availability check (upstream semantics: taken=400/0100,
	// available=empty 200; review P2 #10) ---
	dup, dupBody := getXMLWithCode(t, nnasSrv, "/v1/api/people/testplayer1", "", "")
	if dup != http.StatusBadRequest || !strings.Contains(dupBody, "0100") {
		t.Fatalf("taken username must 400/0100, got %d %s", dup, dupBody)
	}
	free, freeBody := getXMLWithCode(t, nnasSrv, "/v1/api/people/unregistered_name", "", "")
	if free != http.StatusOK || strings.Contains(freeBody, "error") {
		t.Fatalf("available username must be empty 200, got %d %s", free, freeBody)
	}

	// --- 9. XML registration body (console format; review P1 #4) ---
	xmlBody := `<person><user_id>xmluser1</user_id><password>consoleSecret99</password>` +
		`<email><address>xmluser@example.com</address></email>` +
		`<mii><name>xmluser1</mii_name><data>AAAA</data></mii>` +
		`<country>US</country><language>en</language><region>1</region><tz_name>EST5EDT</tz_name></person>`
	xmlBody = strings.Replace(xmlBody, "</mii_name>", "</name>", 1)
	xmlRec := postRaw(t, nnasSrv, "/v1/api/people", xmlBody, "application/xml")
	if !strings.Contains(xmlRec.Body.String(), "<pid>") {
		t.Fatalf("XML registration failed: %d %s", xmlRec.Code, xmlRec.Body.String())
	}

}

func parseInt(t *testing.T, s string) int64 {
	t.Helper()
	var v int64
	for _, c := range s {
		v = v*10 + int64(c-'0')
	}
	return v
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func postForm(t *testing.T, srv http.Handler, path string, fields []string) string {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(strings.Join(fields, "&")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Body.String() + "\x00" + itoa(rec.Code)
}

func getXML(t *testing.T, srv http.Handler, path, bearer, titleID string) string {
	t.Helper()
	code, body := getXMLWithCode(t, srv, path, bearer, titleID)
	if code != 200 {
		t.Fatalf("GET %s: %d %s", path, code, body)
	}
	return body
}

func getXMLStatus(t *testing.T, srv http.Handler, path, bearer, titleID string) int {
	t.Helper()
	code, _ := getXMLWithCode(t, srv, path, bearer, titleID)
	return code
}

func getXMLWithCode(t *testing.T, srv http.Handler, path, bearer, titleID string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Nintendo-Title-ID", titleID)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func getXMLNoAuth(t *testing.T, srv http.Handler, path string) int {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func postRaw(t *testing.T, srv http.Handler, path, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// Review P1 #3: unlinking immediately revokes adapter authorization.
func TestUnlinkRevokesAccess(t *testing.T) {
	core := startCore(t)
	srv, v2, pool := startAdapter(t, core)

	// Register + sign in.
	form := strings.NewReader(strings.Join([]string{
		"user_id=unlinkme", "password=consoleSecret99",
		"email.address=unlinkme@example.com", "mii.name=unlinkme",
		"mii.data=AAAA", "country=US", "language=en", "region=1", "tz_name=EST5EDT",
	}, "&"))
	req := httptest.NewRequest("POST", "/v1/api/people", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("registration failed: %s", rec.Body.String())
	}
	pidStr := between(rec.Body.String(), "<pid>", "</pid>")
	transformed := nnas.NintendoPasswordHash("consoleSecret99", parseInt(t, pidStr))
	oauthXML := postForm(t, srv, "/v1/api/oauth20/access_token/generate",
		[]string{"grant_type=password", "user_id=unlinkme", "password=" + transformed})
	accessToken := between(oauthXML, "<token>", "</token>")
	if accessToken == "" {
		t.Fatalf("login failed: %s", oauthXML)
	}

	// Unlink the console in the core (adapter-side unlink trigger).
	ctx := context.Background()
	pnid, err := store.GetPNIDByPID(ctx, pool, parseInt(t, pidStr))
	if err != nil {
		t.Fatal(err)
	}
	if err := core.UnlinkBySubject(ctx, "wiiu", pidStr); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	// New logins fail (0106) — the active-link check fires before tokens.
	relogin := postForm(t, srv, "/v1/api/oauth20/access_token/generate",
		[]string{"grant_type=password", "user_id=unlinkme", "password=" + transformed})
	if !strings.Contains(relogin, "0106") {
		t.Fatalf("unlinked console must not sign in: %s", relogin)
	}

	// Existing tokens fail on use (bearer paths check the link).
	code, body := getXMLWithCode(t, srv, "/v1/api/people/@me/profile", accessToken, "0005000010143500")
	if code != http.StatusBadRequest {
		t.Fatalf("unlinked token must fail, got %d %s", code, body)
	}

	// GetNEXPassword also refuses unlinked identities.
	gctx := metadata.AppendToOutgoingContext(context.Background(), "X-API-Key", "adapter-grpc-key-0123456789abcdef")
	if _, err := v2.GetNEXPassword(gctx, &pb.GetNEXPasswordRequest{Pid: uint32(pnid.PID)}); err == nil {
		t.Fatal("unlinked identity must not resolve NEX credentials")
	}
}

// Review P1 #7: a token whose audience is not a registered game server is
// rejected even when the caller declares no server list.
func TestExchangeAudienceRegistered(t *testing.T) {
	core := startCore(t)
	srv, v2, pool := startAdapter(t, core)
	ctx := context.Background()

	// Register a user via the adapter's NNAS handler.
	form := strings.NewReader(strings.Join([]string{
		"user_id=audienceuser", "password=consoleSecret99",
		"email.address=audience@example.com", "mii.name=audienceuser",
		"mii.data=AAAA", "country=US", "language=en", "region=1", "tz_name=EST5EDT",
	}, "&"))
	req := httptest.NewRequest("POST", "/v1/api/people", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("registration failed: %s", rec.Body.String())
	}
	pnid, err := store.GetPNIDByUsername(ctx, pool, "audienceuser")
	if err != nil {
		t.Fatal(err)
	}

	// Mint a NEX token bound to an UNREGISTERED game server id directly.
	now := time.Now()
	if err := store.InsertNEXToken(ctx, pool, "rogue-token", "9999abcd", pnid.PID, 0, now, now.Add(time.Hour), "wiiu"); err != nil {
		t.Fatal(err)
	}
	gctx := metadata.AppendToOutgoingContext(ctx, "X-API-Key", "adapter-grpc-key-0123456789abcdef")
	if _, err := v2.ExchangeNEXTokenForUserData(gctx, &pb.ExchangeNEXTokenForUserDataRequest{
		Token: "rogue-token", // no GameServerIds declared
	}); err == nil {
		t.Fatal("unregistered audience must be rejected even with empty list")
	}
	// The registered server still exchanges with an empty list.
	if err := store.InsertNEXToken(ctx, pool, "good-token", "00003200", pnid.PID, 0, now, now.Add(time.Hour), "wiiu"); err != nil {
		t.Fatal(err)
	}
	if _, err := v2.ExchangeNEXTokenForUserData(gctx, &pb.ExchangeNEXTokenForUserDataRequest{
		Token: "good-token",
	}); err != nil {
		t.Fatalf("registered audience with empty list must pass: %v", err)
	}
}
