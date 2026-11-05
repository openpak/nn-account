package profilecore

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"

	"openpak/nn-account/internal/store"
)

type fakeCore struct {
	acct  map[string]*accountv1.GetAccountResponse
	down  bool
	reads int
}

func (f *fakeCore) GetAccount(_ context.Context, id string) (*accountv1.GetAccountResponse, error) {
	f.reads++
	if f.down {
		return nil, errors.New("down")
	}
	a, ok := f.acct[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return a, nil
}

func (f *fakeCore) UpdatePublicProfile(_ context.Context, r *accountv1.UpdatePublicProfileRequest) (*accountv1.GetAccountResponse, error) {
	if f.down {
		return nil, errors.New("down")
	}
	a := f.acct[r.AccountId]
	n := accountv1.GetAccountResponse{Handle: a.Handle, DisplayName: a.DisplayName, Country: a.Country, Language: a.Language, Timezone: a.Timezone, ProfileVersion: a.ProfileVersion + 1}
	if r.Country != nil {
		n.Country = *r.Country
	}
	if r.Language != nil {
		n.Language = *r.Language
	}
	if r.Timezone != nil {
		n.Timezone = *r.Timezone
	}
	f.acct[r.AccountId] = &n
	return &n, nil
}

func newFake() *fakeCore {
	return &fakeCore{acct: map[string]*accountv1.GetAccountResponse{
		"a1": {Handle: "alice", DisplayName: "Alice Longname", Country: "ie", Language: "pt-BR", Timezone: "Europe/Dublin", ProfileVersion: 3},
	}}
}

func TestApplyCoreWinsAndDefaults(t *testing.T) {
	c := New(newFake())
	p := &store.PNID{PID: 1, AccountID: "a1", Username: "old", Country: "US", Language: "en", TimezoneName: "EST5EDT"}
	c.Apply(context.Background(), p)
	if p.Username != "alice" || p.Country != "IE" || p.Language != "pt" || p.TimezoneName != "Europe/Dublin" || p.MiiName != "Alice Long" {
		t.Fatalf("overlay: %+v", p)
	}
	// no core value, no local value: console defaults
	f := newFake()
	f.acct["a2"] = &accountv1.GetAccountResponse{Handle: "bob"}
	c = New(f)
	q := &store.PNID{PID: 2, AccountID: "a2"}
	c.Apply(context.Background(), q)
	if q.Country != "US" || q.Language != "en" || q.TimezoneName != "EST5EDT" {
		t.Fatalf("defaults: %+v", q)
	}
	// core has none, the local column stays
	q = &store.PNID{PID: 2, AccountID: "a2", Country: "PT"}
	c.Apply(context.Background(), q)
	if q.Country != "PT" {
		t.Fatalf("local fallback lost: %+v", q)
	}
}

func TestCacheTTLStaleAndRefresh(t *testing.T) {
	f := newFake()
	c := New(f)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()
	c.Get(ctx, "a1")
	c.Get(ctx, "a1")
	if f.reads != 1 {
		t.Fatalf("cache not used: %d reads", f.reads)
	}
	now = now.Add(TTL + time.Second)
	f.down = true
	a, stale, err := c.Get(ctx, "a1")
	if err != nil || !stale || a.GetHandle() != "alice" {
		t.Fatalf("stale serve: %v %v", stale, err)
	}
	// event: core back with a newer profile
	f.down = false
	f.acct["a1"] = &accountv1.GetAccountResponse{Handle: "alice", Language: "fr", ProfileVersion: 4}
	c.Refresh(ctx, "a1")
	a, stale, _ = c.Get(ctx, "a1")
	if stale || a.GetLanguage() != "fr" {
		t.Fatal("event did not refresh")
	}
	// an older read never replaces a newer cached one
	f.acct["a1"] = &accountv1.GetAccountResponse{Handle: "alice", Language: "de", ProfileVersion: 2}
	c.Refresh(ctx, "a1")
	a, _, _ = c.Get(ctx, "a1")
	if a.GetLanguage() != "fr" {
		t.Fatal("older profile_version replaced newer")
	}
	c.Invalidate("a1")
	f.down = true
	if _, _, err := c.Get(ctx, "a1"); err == nil {
		t.Fatal("erased account still served from memory")
	}
}

func TestUnreachableWithoutCacheKeepsLocal(t *testing.T) {
	f := newFake()
	f.down = true
	c := New(f)
	p := &store.PNID{PID: 1, AccountID: "a1", Username: "alice", Country: "PT", Language: "pt", TimezoneName: "Europe/Lisbon"}
	c.Apply(context.Background(), p)
	if p.Country != "PT" || p.TimezoneName != "Europe/Lisbon" {
		t.Fatalf("%+v", p)
	}
}

func TestConsoleLanguage(t *testing.T) {
	for in, want := range map[string]string{"pt-BR": "pt", "EN": "en", "": "", "fr_CA": "fr"} {
		if got := ConsoleLanguage(in); got != want {
			t.Fatalf("%q: %q", in, got)
		}
	}
}

// Needs a Postgres (NN_ACCOUNT_TEST_DATABASE_URL); skipped without one.
func TestMigrate(t *testing.T) {
	url := os.Getenv("NN_ACCOUNT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NN_ACCOUNT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := store.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	const a1 = "bbbbbbbb-0000-0000-0000-000000000001"
	const a2 = "bbbbbbbb-0000-0000-0000-000000000002"
	f := &fakeCore{acct: map[string]*accountv1.GetAccountResponse{
		a1: {Handle: "mig1", DisplayName: "Mig One", Country: "GB", Language: "", Timezone: ""},
		a2: {Handle: "mig2", DisplayName: "Mig Two", Country: "", Language: "fr", Timezone: "Europe/Paris"},
	}}
	for _, s := range []string{
		`DELETE FROM pnids WHERE pid IN (1700000011,1700000012)`,
		`INSERT INTO pnids (pid, username, account_id, mii_name, country, language, timezone_name, password_cache, gender) VALUES
			(1700000011,'mig1','` + a1 + `','Mig One','US','en','EST5EDT','cafe','M'),
			(1700000012,'mig2','` + a2 + `','Mig Two','US','en','EST5EDT','beef','F')`,
	} {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	c := New(f)
	for i := 0; i < 2; i++ { // second run is a no-op
		if _, err := c.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	// a1: core was empty for language/timezone -> backfilled, never over a value; core country GB kept
	if f.acct[a1].Language != "en" || f.acct[a1].Timezone != "EST5EDT" || f.acct[a1].Country != "GB" {
		t.Fatalf("a1 core: %+v", f.acct[a1])
	}
	// a2: core values untouched, country empty in core -> local country stays
	if f.acct[a2].Language != "fr" || f.acct[a2].Timezone != "Europe/Paris" {
		t.Fatalf("a2 core overwritten: %+v", f.acct[a2])
	}
	var n int
	q := func(sql string, want int) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: got %d (%v) want %d", sql, n, err, want)
		}
	}
	q(`SELECT count(*) FROM pnids WHERE pid=1700000011 AND country='' AND language='' AND timezone_name='' AND mii_name='' AND username='mig1' AND password_cache='cafe' AND gender='M'`, 1)
	q(`SELECT count(*) FROM pnids WHERE pid=1700000012 AND country='US' AND language='' AND timezone_name='' AND mii_name='' AND password_cache='beef' AND gender='F'`, 1)
	pool.Exec(ctx, `DELETE FROM pnids WHERE pid IN (1700000011,1700000012)`)
}
