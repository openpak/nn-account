// Package miicore moves the Wii U / 3DS Mii between this adapter and the
// account core. The Mii belongs to the account and is shared by the Wii, the
// Wii U / 3DS and the Switch, so it lives in the core and pnids holds none
// (card #131). An upload the core could not take yet waits in the pending_miis
// queue, one row per PNID, until Retry has delivered it; the queue is empty in
// normal operation and the nightly dedup-check flags a row older than an hour.
package miicore

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/store"
)

// DefaultMiiData is upstream's registration placeholder ("Default"). It is shown
// when an account has no Mii, but is never stored in the core: it is nobody's Mii.
const DefaultMiiData = "AwAAQOlVognnx0GC2/uogAOzuI0n2QAAAEBEAGUAZgBhAHUAbAB0AAAAAAAAAEBAAAAhAQJoRBgmNEYUgRIXaA0AACkAUkhQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGm9"

func clean(b64 string) string { return strings.Join(strings.Fields(b64), "") }

// Install makes every PNID this process loads carry the account's core Mii:
// the core's, else a queued upload that has not reached it yet, else the
// placeholder.
func Install(core *coreclient.Client, pool *pgxpool.Pool) {
	store.MiiOverlay = func(ctx context.Context, p *store.PNID) {
		raw, err := core.GetMii(ctx, p.AccountID)
		if err == nil {
			p.MiiData = base64.StdEncoding.EncodeToString(raw)
			return
		}
		if !errors.Is(err, coreclient.ErrNoMii) {
			// Core unreachable: serve what is queued, else the placeholder, rather than fail the sign-in.
			log.Printf("miicore: read Mii of pid %d: %v", p.PID, err)
		}
		var queued string
		if pool.QueryRow(ctx, `SELECT data FROM pending_miis WHERE pid=$1`, p.PID).Scan(&queued) == nil && clean(queued) != "" {
			p.MiiData = clean(queued)
			return
		}
		p.MiiData = DefaultMiiData
	}
}

// Save stores an uploaded Mii (base64 Ver3) as the account's Mii and reports
// whether the core has it. A core outage queues the upload in pending_miis so
// Retry delivers it; data the core refuses as unreadable is dropped (retrying
// can never help).
func Save(ctx context.Context, core *coreclient.Client, pool *pgxpool.Pool, pid int64, accountID, b64 string) (inCore bool) {
	b64 = clean(b64)
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		log.Printf("miicore: pid %d Mii is not base64: %v", pid, err)
		return false
	}
	if _, err := core.PutMii(ctx, accountID, raw); err != nil {
		if status.Code(err) == codes.InvalidArgument {
			log.Printf("miicore: core rejected the Mii of pid %d: %v", pid, err)
			return false
		}
		log.Printf("miicore: store Mii of pid %d, queued for retry: %v", pid, err)
		if _, qerr := pool.Exec(ctx, `INSERT INTO pending_miis (pid, data) VALUES ($1,$2)
			ON CONFLICT (pid) DO UPDATE SET data=EXCLUDED.data, attempts=0, created_at=now()`, pid, b64); qerr != nil {
			log.Printf("miicore: queue Mii of pid %d: %v", pid, qerr)
		}
		return false
	}
	// The core has the newest upload: an older queued one must not overwrite it later.
	if _, err := pool.Exec(ctx, `DELETE FROM pending_miis WHERE pid=$1`, pid); err != nil {
		log.Printf("miicore: clear queue of pid %d: %v", pid, err)
	}
	return true
}

// Retry delivers every queued upload to the core. A delivered or unreadable
// row is deleted (only if it is still the row read, so a newer upload stays).
func Retry(ctx context.Context, core *coreclient.Client, pool *pgxpool.Pool) (sent, dropped int) {
	rows, err := pool.Query(ctx, `SELECT q.pid, p.account_id::text, q.data FROM pending_miis q
		JOIN pnids p ON p.pid=q.pid WHERE NOT p.deleted AND p.account_id IS NOT NULL`)
	if err != nil {
		log.Printf("miicore: queue query: %v", err)
		return
	}
	type row struct {
		pid        int64
		acct, data string
	}
	var todo []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.pid, &r.acct, &r.data) == nil {
			todo = append(todo, r)
		}
	}
	rows.Close()
	for _, r := range todo {
		raw, err := base64.StdEncoding.DecodeString(clean(r.data))
		if err == nil {
			_, err = core.PutMii(ctx, r.acct, raw)
		}
		switch {
		case err == nil:
			sent++
		case err != nil && (status.Code(err) == codes.InvalidArgument || errors.As(err, new(base64.CorruptInputError))):
			log.Printf("miicore: dropping the queued Mii of pid %d: unreadable", r.pid)
			dropped++
		default:
			log.Printf("miicore: queued Mii of pid %d still undeliverable: %v", r.pid, err)
			_, _ = pool.Exec(ctx, `UPDATE pending_miis SET attempts=attempts+1 WHERE pid=$1`, r.pid)
			continue
		}
		_, _ = pool.Exec(ctx, `DELETE FROM pending_miis WHERE pid=$1 AND data=$2`, r.pid, r.data)
	}
	return
}

// RetryLoop runs Retry every minute until ctx ends; it logs only when something moved.
func RetryLoop(ctx context.Context, core *coreclient.Client, pool *pgxpool.Pool) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		rctx, cancel := context.WithTimeout(ctx, 50*time.Second)
		sent, dropped := Retry(rctx, core, pool)
		cancel()
		if sent+dropped > 0 {
			log.Printf("miicore: queue run: %d delivered to the core, %d dropped as unreadable", sent, dropped)
		}
	}
}
