// Package profilecore keeps the profile fields of a Wii U / 3DS account in the
// account core, where they belong: the NNID (the core handle), the account name,
// country, language and time zone are the same person on every network. The
// adapter only caches them, for five minutes, and refreshes the cache when the
// core announces a profile_updated event. The pnids columns of the same names are
// a landing place for data the core could not take yet and a fallback while the
// core is unreachable; Migrate empties them once the core holds the value.
package profilecore

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"

	"openpak/nn-account/internal/store"
)

// TTL is how long a core profile is trusted without a profile_updated event.
const TTL = 5 * time.Minute

// Console defaults, used only when neither the core nor the local column has a value.
const (
	DefaultCountry  = "US"
	DefaultLanguage = "en"
	DefaultTimezone = "EST5EDT"
)

// Core is what the adapter needs from the account core.
type Core interface {
	GetAccount(ctx context.Context, accountID string) (*accountv1.GetAccountResponse, error)
	UpdatePublicProfile(ctx context.Context, req *accountv1.UpdatePublicProfileRequest) (*accountv1.GetAccountResponse, error)
}

type entry struct {
	acct    *accountv1.GetAccountResponse
	fetched time.Time
}

// Cache is the in-process profile cache.
type Cache struct {
	core Core
	now  func() time.Time
	mu   sync.Mutex
	m    map[string]entry
}

func New(core Core) *Cache {
	return &Cache{core: core, now: time.Now, m: map[string]entry{}}
}

// Get returns the core account, from the cache while fresh. When the core is
// unreachable the last cached value is served (stale=true); with none, err.
func (c *Cache) Get(ctx context.Context, accountID string) (acct *accountv1.GetAccountResponse, stale bool, err error) {
	c.mu.Lock()
	e, ok := c.m[accountID]
	c.mu.Unlock()
	if ok && c.now().Sub(e.fetched) < TTL {
		return e.acct, false, nil
	}
	a, err := c.core.GetAccount(ctx, accountID)
	if err != nil {
		if ok {
			return e.acct, true, nil
		}
		return nil, false, err
	}
	c.put(accountID, a)
	return a, false, nil
}

func (c *Cache) put(accountID string, a *accountv1.GetAccountResponse) {
	c.mu.Lock()
	c.m[accountID] = entry{acct: a, fetched: c.now()}
	c.mu.Unlock()
}

// Invalidate forgets an account (erasure, or a failed refresh).
func (c *Cache) Invalidate(accountID string) {
	c.mu.Lock()
	delete(c.m, accountID)
	c.mu.Unlock()
}

// Refresh handles a profile_updated event: it re-reads the account and keeps
// the copy unless the cached one is newer (profile_version).
func (c *Cache) Refresh(ctx context.Context, accountID string) {
	a, err := c.core.GetAccount(ctx, accountID)
	if err != nil {
		c.Invalidate(accountID)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[accountID]; ok && e.acct.GetProfileVersion() > a.GetProfileVersion() {
		return
	}
	c.m[accountID] = entry{acct: a, fetched: c.now()}
}

// ConsoleLanguage is the language tag a console expects: the primary subtag ("pt-BR" is "pt").
func ConsoleLanguage(tag string) string {
	tag = strings.TrimSpace(tag)
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i]
	}
	return strings.ToLower(tag)
}

// Apply overlays the core profile onto a loaded PNID. Core values win when
// present; otherwise the local column stays; console defaults fill what is left.
func (c *Cache) Apply(ctx context.Context, p *store.PNID) {
	if p.AccountID != "" {
		a, stale, err := c.Get(ctx, p.AccountID)
		switch {
		case err != nil:
			log.Printf("profilecore: read account of pid %d: %v", p.PID, err)
		default:
			_ = stale
			if h := a.GetHandle(); h != "" {
				p.Username = h
			}
			if n := a.GetDisplayName(); n != "" {
				p.MiiName = store.ProfileName(n)
			}
			if v := strings.ToUpper(strings.TrimSpace(a.GetCountry())); v != "" {
				p.Country = v
			}
			if v := ConsoleLanguage(a.GetLanguage()); v != "" {
				p.Language = v
			}
			if v := a.GetTimezone(); v != "" {
				p.TimezoneName = v
			}
		}
	}
	if p.Country == "" {
		p.Country = DefaultCountry
	}
	if p.Language == "" {
		p.Language = DefaultLanguage
	}
	if p.TimezoneName == "" {
		p.TimezoneName = DefaultTimezone
	}
}

// Install makes every PNID this process loads carry the core profile.
func Install(c *Cache) {
	Default = c
	store.ProfileOverlay = c.Apply
}

// Default is the cache Install registered.
var Default *Cache

// SaveUpdates sends the profile keys of a column-update map (country, language,
// timezone_name) to the core and writes the remaining keys to pnids. Without an
// installed cache everything is written locally.
func SaveUpdates(ctx context.Context, pool *pgxpool.Pool, accountID string, pid int64, updates map[string]any) error {
	rest := map[string]any{}
	var f Fields
	for k, v := range updates {
		sv, _ := v.(string)
		switch k {
		case "country":
			f.Country = sv
		case "language":
			f.Language = sv
		case "timezone_name":
			f.Timezone = sv
		default:
			rest[k] = v
		}
	}
	if Default == nil {
		return store.UpdatePNIDSettings(ctx, pool, pid, updates)
	}
	if _, err := Default.Save(ctx, pool, accountID, pid, f); err != nil {
		return err
	}
	return store.UpdatePNIDSettings(ctx, pool, pid, rest)
}

// Fields is a console profile edit; an empty field is left alone.
type Fields struct{ Country, Language, Timezone string }

func (f Fields) empty() bool { return f.Country == "" && f.Language == "" && f.Timezone == "" }

// Save sends a console profile edit to the core. If the core refuses or is
// unreachable the edit lands in the pnids columns instead (and Migrate moves it
// later); Save reports whether the core has it.
func (c *Cache) Save(ctx context.Context, pool *pgxpool.Pool, accountID string, pid int64, f Fields) (inCore bool, err error) {
	if f.empty() {
		return true, nil
	}
	req := &accountv1.UpdatePublicProfileRequest{AccountId: accountID}
	if f.Country != "" {
		v := strings.ToUpper(f.Country)
		req.Country = &v
	}
	if f.Language != "" {
		req.Language = &f.Language
	}
	if f.Timezone != "" {
		req.Timezone = &f.Timezone
	}
	a, cerr := c.core.UpdatePublicProfile(ctx, req)
	if cerr == nil {
		c.put(accountID, a)
		return true, nil
	}
	if status.Code(cerr) == codes.InvalidArgument {
		log.Printf("profilecore: core rejected the profile edit of pid %d: %v", pid, cerr)
	} else {
		log.Printf("profilecore: send profile edit of pid %d: %v", pid, cerr)
	}
	updates := map[string]any{}
	if f.Country != "" {
		updates["country"] = strings.ToUpper(f.Country)
	}
	if f.Language != "" {
		updates["language"] = f.Language
	}
	if f.Timezone != "" {
		updates["timezone_name"] = f.Timezone
	}
	return false, store.UpdatePNIDSettings(ctx, pool, pid, updates)
}

// MigrateStats are the counts of one migration run.
type MigrateStats struct {
	Rows, Pushed, Cleared int
}

type pending struct {
	pid                int64
	acct               string
	miiName, country   string
	language, timezone string
}

// Migrate is the idempotent one-shot: for every live PNID it pushes a local
// language and time zone to the core where the core has none (never over a
// core value), then empties the local copies that the core holds, after
// reading the account back. Country is never pushed: the local value is the
// console's registration default, not a choice; the core's ISO-2 country wins
// and the local one is only emptied when the core has one. The NNID column
// stays: it is the lookup key for NNID sign-in.
func (c *Cache) Migrate(ctx context.Context, pool *pgxpool.Pool) (MigrateStats, error) {
	var st MigrateStats
	rows, err := pool.Query(ctx, `SELECT pid, account_id::text, mii_name, country, language, timezone_name FROM pnids
		WHERE NOT deleted AND account_id IS NOT NULL AND account_id <> $1
		AND (mii_name<>'' OR country<>'' OR language<>'' OR timezone_name<>'')`, store.NilAccountID)
	if err != nil {
		return st, err
	}
	var todo []pending
	for rows.Next() {
		var r pending
		if err := rows.Scan(&r.pid, &r.acct, &r.miiName, &r.country, &r.language, &r.timezone); err != nil {
			rows.Close()
			return st, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	st.Rows = len(todo)
	for _, r := range todo {
		a, err := c.core.GetAccount(ctx, r.acct)
		if err != nil {
			continue
		}
		req := &accountv1.UpdatePublicProfileRequest{AccountId: r.acct}
		push := false
		// The exact default pair is what older registrations stored when the
		// console sent nothing: it is not a choice, never push it.
		defPair := r.language == DefaultLanguage && r.timezone == DefaultTimezone
		if !defPair && a.GetLanguage() == "" && r.language != "" {
			req.Language = &r.language
			push = true
		}
		if !defPair && a.GetTimezone() == "" && r.timezone != "" {
			req.Timezone = &r.timezone
			push = true
		}
		if push {
			if _, err := c.core.UpdatePublicProfile(ctx, req); err != nil {
				log.Printf("profilecore: migrate pid %d: core did not take the profile: %v", r.pid, err)
			} else {
				st.Pushed++
			}
		}
		// Verified read-back, never the cache.
		b, err := c.core.GetAccount(ctx, r.acct)
		if err != nil {
			continue
		}
		c.put(r.acct, b)
		sets := []string{}
		if b.GetDisplayName() != "" && r.miiName != "" {
			sets = append(sets, "mii_name=''")
		}
		if b.GetCountry() != "" && r.country != "" {
			sets = append(sets, "country=''")
		}
		if (defPair || b.GetLanguage() != "") && r.language != "" {
			sets = append(sets, "language=''")
		}
		if (defPair || b.GetTimezone() != "") && r.timezone != "" {
			sets = append(sets, "timezone_name=''")
		}
		if len(sets) == 0 {
			continue
		}
		// Guarded by the values read above: a console edit that landed in between stays.
		_, err = pool.Exec(ctx, `UPDATE pnids SET `+strings.Join(sets, ", ")+`, updated_at=now()
			WHERE pid=$1 AND mii_name=$2 AND country=$3 AND language=$4 AND timezone_name=$5`,
			r.pid, r.miiName, r.country, r.language, r.timezone)
		if err != nil {
			log.Printf("profilecore: migrate pid %d: clear local copies: %v", r.pid, err)
			continue
		}
		st.Cleared++
	}
	return st, nil
}
