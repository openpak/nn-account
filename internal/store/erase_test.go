package store

import (
	"context"
	"os"
	"testing"
)

// Needs a Postgres (NN_ACCOUNT_TEST_DATABASE_URL); skipped without one.
func TestEraseAccount(t *testing.T) {
	url := os.Getenv("NN_ACCOUNT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NN_ACCOUNT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	const a, b = "aaaaaaaa-0000-0000-0000-000000000001", "aaaaaaaa-0000-0000-0000-000000000002"
	for _, s := range []string{
		`DELETE FROM oauth_tokens WHERE pid IN (1700000001,1700000002)`,
		`DELETE FROM devices WHERE fcdcert_hash IN ('erase-a','erase-shared')`,
		`DELETE FROM nex_accounts WHERE pid IN (1700000001,1700000002)`,
		`DELETE FROM pnids WHERE pid IN (1700000001,1700000002)`,
		`INSERT INTO pnids (pid, username, account_id, mii_name, mii_data, password_cache) VALUES
			(1700000001,'erasea1',  '` + a + `','Alice','AAAA','cafe'),
			(1700000002,'eraseb1',  '` + b + `','Bob','BBBB','beef')`,
		`INSERT INTO nex_accounts (pid, owning_pid, password, friend_code) VALUES (1700000001,1700000001,'x','123'),(1700000002,1700000002,'y','456')`,
		`INSERT INTO oauth_tokens VALUES ('h1','c',1700000001,'oauth_access',0,now(),now()),('h2','c',1700000002,'oauth_access',0,now(),now())`,
		`INSERT INTO devices (fcdcert_hash, serial, linked_pids) VALUES ('erase-a','SER1','{1700000001}'),('erase-shared','SER2','{1700000001,1700000002}')`,
	} {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	n, err := EraseAccount(ctx, pool, a, nil)
	if err != nil || n == 0 {
		t.Fatalf("first run: n=%d err=%v", n, err)
	}
	var cnt int
	check := func(q string, want int) {
		t.Helper()
		if err := pool.QueryRow(ctx, q).Scan(&cnt); err != nil || cnt != want {
			t.Fatalf("%s: got %d (%v), want %d", q, cnt, err, want)
		}
	}
	check(`SELECT count(*) FROM pnids WHERE pid=1700000001 AND mii_data='' AND password_cache='' AND account_id='`+NilAccountID+`' AND deleted`, 1)
	check(`SELECT count(*) FROM nex_accounts WHERE pid=1700000001`, 0)
	check(`SELECT count(*) FROM oauth_tokens WHERE pid=1700000001`, 0)
	check(`SELECT count(*) FROM devices WHERE fcdcert_hash='erase-a'`, 0)
	check(`SELECT count(*) FROM devices WHERE fcdcert_hash='erase-shared' AND linked_pids='{1700000002}'`, 1)
	// the other account is untouched
	check(`SELECT count(*) FROM pnids WHERE pid=1700000002 AND mii_data='BBBB' AND username='eraseb1'`, 1)
	check(`SELECT count(*) FROM nex_accounts WHERE pid=1700000002`, 1)
	check(`SELECT count(*) FROM oauth_tokens WHERE pid=1700000002`, 1)
	if n2, err := EraseAccount(ctx, pool, a, nil); err != nil || n2 != 0 {
		t.Fatalf("second run must be 0 rows: n=%d err=%v", n2, err)
	}
}
