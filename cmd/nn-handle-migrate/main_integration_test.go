//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	accountv1 "openpak/nn-account/internal/accountpb"
	"openpak/nn-account/internal/store"
)

type migrationCore struct {
	accountv1.UnimplementedIdentityServer
	accounts map[string]*accountv1.GetAccountResponse
}

func (c *migrationCore) CheckHandle(context.Context, *accountv1.CheckHandleRequest) (*accountv1.CheckHandleResponse, error) {
	return &accountv1.CheckHandleResponse{Valid: true}, nil
}
func (c *migrationCore) GetAccount(_ context.Context, req *accountv1.GetAccountRequest) (*accountv1.GetAccountResponse, error) {
	return c.accounts[req.AccountId], nil
}

func TestReviewedNNMigration(t *testing.T) {
	dsn := os.Getenv("NN_HANDLE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires dedicated NN_HANDLE_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := store.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var db string
	if err = pool.QueryRow(ctx, "SELECT current_database()").Scan(&db); err != nil {
		t.Fatal(err)
	}
	if db != "openpak_nn_handle_test_20261018" {
		t.Fatal("refusing to reset a database outside this test's dedicated name")
	}
	if _, err = pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	const a = "00000000-0000-0000-0000-000000000001"
	const b = "00000000-0000-0000-0000-000000000002"
	for i, id := range []string{a, b} {
		pid := int64(1000000001 + i)
		p := &store.PNID{PID: pid, AccountID: id, Username: []string{"oldalpha", "oldbeta"}[i], ServerAccessLevel: "prod", MiiName: "old", MiiData: "unchanged-binary", Country: "US", Language: "en", Region: 1, TimezoneName: "UTC", PasswordCache: "unchanged-cache"}
		if err = store.InsertPNID(ctx, pool, p); err != nil {
			t.Fatal(err)
		}
		if err = store.InsertNEXAccount(ctx, pool, &store.NEXAccount{PID: pid, OwningPID: &pid, Password: "unchanged-nex", ServerAccessLevel: "prod", DeviceType: "wiiu"}); err != nil {
			t.Fatal(err)
		}
	}
	choices := map[string]choice{a: {Handle: "AlphaTest", DisplayName: "Alpha Test User"}, b: {Handle: "BravoTest", DisplayName: "Bravo User"}}
	snapshots := []snapshot{{ID: a, Links: []link{{Namespace: "wiiu", Subject: "1000000001", Name: "oldalpha"}}}, {ID: b, Links: []link{{Namespace: "wiiu", Subject: "1000000002", Name: "oldbeta"}}}}
	fake := &migrationCore{accounts: map[string]*accountv1.GetAccountResponse{}}
	for id, c := range choices {
		fake.accounts[id] = &accountv1.GetAccountResponse{Status: 1, Handle: c.Handle, DisplayName: c.DisplayName}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	accountv1.RegisterIdentityServer(server, fake)
	go server.Serve(listener)
	defer server.Stop()
	dir := t.TempDir()
	bin := filepath.Join(dir, "nn-handle-migrate")
	build := exec.Command("go", "build", "-p", "2", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	write := func(name string, v any) {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("choices.json", choices)
	write("snapshot.json", snapshots)
	run := func(apply, wantSuccess bool) {
		t.Helper()
		args := []string{"-choices", filepath.Join(dir, "choices.json"), "-snapshot", filepath.Join(dir, "snapshot.json")}
		if apply {
			args = append(args, "-apply")
		}
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "NN_ACCOUNT_DATABASE_URL="+dsn, "NN_ACCOUNT_CORE_ADDR="+listener.Addr().String(), "NN_ACCOUNT_CORE_KEY=test-key")
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantSuccess {
			t.Fatalf("apply=%v: %v\n%s", apply, err, out)
		}
	}
	username := func(id string) string {
		t.Helper()
		p, err := store.GetPNIDByAccountID(ctx, pool, id)
		if err != nil {
			t.Fatal(err)
		}
		return p.Username
	}
	run(false, true)
	if username(a) != "oldalpha" {
		t.Fatal("dry-run wrote data")
	}
	if _, err = pool.Exec(ctx, "UPDATE pnids SET username='stale' WHERE account_id=$1", b); err != nil {
		t.Fatal(err)
	}
	run(true, false)
	if username(a) != "oldalpha" {
		t.Fatal("stale second row did not roll back first update")
	}
	if _, err = pool.Exec(ctx, "UPDATE pnids SET username='oldbeta' WHERE account_id=$1", b); err != nil {
		t.Fatal(err)
	}
	fake.accounts[b].DisplayName = "unreviewed"
	run(true, false)
	if username(a) != "oldalpha" {
		t.Fatal("core mismatch did not roll back first update")
	}
	fake.accounts[b].DisplayName = choices[b].DisplayName
	run(true, true)
	run(true, true)
	for i, id := range []string{a, b} {
		p, err := store.GetPNIDByAccountID(ctx, pool, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Username != choices[id].Handle || p.PID != int64(1000000001+i) || p.MiiName != store.ProfileName(choices[id].DisplayName) || p.MiiData != "unchanged-binary" || p.PasswordCache != "unchanged-cache" {
			t.Fatalf("identity changed unexpectedly: %+v", p)
		}
		nex, err := store.GetNEXAccountByPID(ctx, pool, p.PID)
		if err != nil || nex.Password != "unchanged-nex" {
			t.Fatal("NEX credential changed")
		}
	}
}
