// Package miicore moves the Wii U / 3DS Mii between this adapter and the
// account core. The Mii belongs to the account and is shared by the Wii, the
// Wii U / 3DS and the Switch, so it lives in the core; the pnids.mii_data
// column is only a landing place for data the core could not take yet.
package miicore

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"strings"

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

// Install makes every PNID this process loads carry the account's core Mii.
// A Mii still sitting in pnids.mii_data (uploaded while the core was
// unreachable, or from before the core held Miis) is moved into the core the
// first time it is seen, and cleared locally once the core has it.
func Install(core *coreclient.Client, pool *pgxpool.Pool) {
	store.MiiOverlay = func(ctx context.Context, p *store.PNID) {
		raw, err := core.GetMii(ctx, p.AccountID)
		switch {
		case err == nil:
			p.MiiData = base64.StdEncoding.EncodeToString(raw)
		case errors.Is(err, coreclient.ErrNoMii):
			if legacy := clean(p.MiiData); legacy != "" && legacy != DefaultMiiData {
				if Adopt(ctx, core, pool, p.PID, p.AccountID, legacy) {
					return
				}
			}
			if clean(p.MiiData) == "" {
				p.MiiData = DefaultMiiData
			}
		default:
			// Core unreachable: serve what this adapter holds rather than fail the sign-in.
			log.Printf("miicore: read Mii of pid %d: %v", p.PID, err)
			if clean(p.MiiData) == "" {
				p.MiiData = DefaultMiiData
			}
		}
	}
}

// Save stores an uploaded Mii (base64 Ver3) as the account's Mii. When the core
// refuses the data (unreadable Mii) or is unreachable, the upload is kept in
// pnids.mii_data so nothing is lost; Save reports whether the core has it.
func Save(ctx context.Context, core *coreclient.Client, pool *pgxpool.Pool, pid int64, accountID, b64 string) (inCore bool) {
	b64 = clean(b64)
	if Adopt(ctx, core, pool, pid, accountID, b64) {
		return true
	}
	if _, err := pool.Exec(ctx, `UPDATE pnids SET mii_data=$2, updated_at=now() WHERE pid=$1`, pid, b64); err != nil {
		log.Printf("miicore: keep Mii of pid %d locally: %v", pid, err)
	}
	return false
}

// Adopt writes b64 into the core and clears the adapter's local copy. It
// returns false (and changes nothing locally) if the core did not take it.
func Adopt(ctx context.Context, core *coreclient.Client, pool *pgxpool.Pool, pid int64, accountID, b64 string) bool {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		log.Printf("miicore: pid %d Mii is not base64: %v", pid, err)
		return false
	}
	if _, err := core.PutMii(ctx, accountID, raw); err != nil {
		if status.Code(err) == codes.InvalidArgument {
			log.Printf("miicore: core rejected the Mii of pid %d: %v", pid, err)
		} else {
			log.Printf("miicore: store Mii of pid %d: %v", pid, err)
		}
		return false
	}
	if _, err := pool.Exec(ctx, `UPDATE pnids SET mii_data='', updated_at=now() WHERE pid=$1`, pid); err != nil {
		log.Printf("miicore: clear local Mii of pid %d: %v", pid, err)
	}
	return true
}

// MigrateAll moves every Mii still held in pnids.mii_data into the core, once
// at startup. Accounts whose core Mii already exists are left alone (the core
// copy may have been changed on another console since).
func MigrateAll(ctx context.Context, core *coreclient.Client, pool *pgxpool.Pool) (moved, kept int) {
	rows, err := pool.Query(ctx, `SELECT pid, account_id::text, mii_data FROM pnids WHERE mii_data <> '' AND mii_data <> $1 AND NOT deleted AND account_id IS NOT NULL`, DefaultMiiData)
	if err != nil {
		log.Printf("miicore: migration query: %v", err)
		return
	}
	type row struct {
		pid       int64
		acct, mii string
	}
	var todo []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.pid, &r.acct, &r.mii) == nil {
			todo = append(todo, r)
		}
	}
	rows.Close()
	for _, r := range todo {
		if _, err := core.GetMii(ctx, r.acct); err == nil {
			// The core already holds this account's Mii: it wins.
			_, _ = pool.Exec(ctx, `UPDATE pnids SET mii_data='' WHERE pid=$1`, r.pid)
			kept++
			continue
		}
		if Adopt(ctx, core, pool, r.pid, r.acct, clean(r.mii)) {
			moved++
		}
	}
	return
}
