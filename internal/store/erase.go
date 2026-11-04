package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NilAccountID replaces the owner on an erased PNID row. The row itself stays
// so the PID is never handed out again, but it names nobody.
const NilAccountID = "00000000-0000-0000-0000-000000000000"

// EraseAccount removes everything the adapter holds about a core account
// (card openpak/prds#120): tokens, NEX credentials and friend code, the
// devices linked only to it, and the PNID's name, Mii and secrets. The PNID
// row is kept, anonymised and marked deleted, only so its PID is not reused.
// extraPIDs are PIDs the caller knows from the core's links that have no PNID
// row here. Idempotent: a second run touches no rows. Returns rows affected.
func EraseAccount(ctx context.Context, pool *pgxpool.Pool, accountID string, extraPIDs []int64) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	pids := map[int64]bool{}
	rows, err := tx.Query(ctx, `SELECT pid FROM pnids WHERE account_id=$1`, accountID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			rows.Close()
			return 0, err
		}
		pids[pid] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range extraPIDs {
		pids[p] = true
	}
	list := make([]int64, 0, len(pids))
	for p := range pids {
		list = append(list, p)
	}

	total := 0
	exec := func(sql string, args ...any) error {
		tag, err := tx.Exec(ctx, sql, args...)
		total += int(tag.RowsAffected())
		return err
	}
	if len(list) > 0 {
		for _, stmt := range []string{
			`DELETE FROM oauth_tokens WHERE pid = ANY($1)`,
			`DELETE FROM independent_service_tokens WHERE pid = ANY($1)`,
			`DELETE FROM nex_tokens WHERE pid = ANY($1)`,
			`DELETE FROM basic_auth_cache WHERE pid = ANY($1)`,
			`DELETE FROM nex_accounts WHERE pid = ANY($1) OR owning_pid = ANY($1)`,
			// A console only this account used goes whole (serial, MAC hash, cert hash);
			// one shared with another PID just loses this account's PID.
			`DELETE FROM devices WHERE linked_pids && $1 AND linked_pids <@ $1`,
			`UPDATE devices SET linked_pids = ARRAY(SELECT x FROM unnest(linked_pids) x WHERE NOT (x = ANY($1))) WHERE linked_pids && $1`,
		} {
			if err := exec(stmt, list); err != nil {
				return 0, err
			}
		}
	}
	if err := exec(`UPDATE pnids SET username='deleted-'||pid::text, account_id=$2, mii_name='', mii_data='', mii_hash='',
		password_cache='', gender='', marketing_flag=false, off_device_flag=false, country='US', language='en',
		timezone_name='EST5EDT', console_pw_mode='', deleted=true, updated_at=now() WHERE account_id=$1`, accountID, NilAccountID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return total, nil
}
