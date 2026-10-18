// nn-handle-migrate applies the reviewed core handle to existing PNIDs. It is
// an offline alpha migration tool, never a user rename endpoint.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/store"
)

type choice struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
}
type link struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Subject   string `json:"subject"`
}
type snapshot struct {
	ID    string `json:"id"`
	Links []link `json:"links"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	planPath := flag.String("choices", "", "owner-reviewed private choices JSON")
	snapPath := flag.String("snapshot", "", "reviewed core snapshot JSON")
	apply := flag.Bool("apply", false, "write canonical NNIDs; requires stopped nn-account/backfills and migrated core")
	flag.Parse()
	var choices map[string]choice
	if err := readJSON(*planPath, &choices); err != nil {
		return err
	}
	var snapshots []snapshot
	if err := readJSON(*snapPath, &snapshots); err != nil {
		return err
	}
	if len(choices) == 0 || len(choices) != len(snapshots) {
		return fmt.Errorf("choices and snapshot must cover the same reviewed accounts")
	}
	old := map[string]link{}
	seen := map[string]bool{}
	for _, a := range snapshots {
		if seen[a.ID] {
			return fmt.Errorf("duplicate reviewed account")
		}
		seen[a.ID] = true
		if _, ok := choices[a.ID]; !ok {
			return fmt.Errorf("snapshot account has no owner choice")
		}
		for _, l := range a.Links {
			if l.Namespace == "wiiu" {
				if _, ok := old[a.ID]; ok {
					return fmt.Errorf("duplicate reviewed Wii U link")
				}
				old[a.ID] = l
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	core, err := coreclient.Dial(ctx, os.Getenv("NN_ACCOUNT_CORE_ADDR"), os.Getenv("NN_ACCOUNT_CORE_KEY"))
	if err != nil {
		return fmt.Errorf("core client unavailable")
	}
	defer core.Close()
	pool, err := store.Connect(ctx, os.Getenv("NN_ACCOUNT_DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("adapter database unavailable")
	}
	defer pool.Close()
	opts := pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}
	if *apply {
		opts = pgx.TxOptions{}
	}
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if *apply {
		if _, err = tx.Exec(ctx, `LOCK TABLE pnids IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
	}
	ids := make([]string, 0, len(choices))
	for id := range choices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := choices[id]
		before, ok := old[id]
		if !ok {
			return fmt.Errorf("reviewed account has no Wii U link")
		}
		pid, err := strconv.ParseInt(before.Subject, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid reviewed PID")
		}
		var current string
		var deleted bool
		if err = tx.QueryRow(ctx, `SELECT username,deleted FROM pnids WHERE account_id=$1 AND pid=$2`, id, pid).Scan(&current, &deleted); err != nil {
			return fmt.Errorf("reviewed PNID changed")
		}
		if deleted || (current != before.Name && current != c.Handle) {
			return fmt.Errorf("PNID changed since review")
		}
		var taken bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pnids WHERE lower(username)=lower($1) AND account_id <> $2)`, c.Handle, id).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("reviewed NNID is reserved by another local identity")
		}
		checked, err := core.CheckHandle(ctx, c.Handle)
		if err != nil || !checked.GetValid() {
			return fmt.Errorf("core rejected reviewed handle")
		}
		if !*apply {
			continue
		}
		acct, err := core.GetAccount(ctx, id)
		if err != nil || acct.GetStatus() != 1 || acct.GetHandle() != c.Handle || acct.GetDisplayName() != c.DisplayName {
			return fmt.Errorf("core must own the exact reviewed handle and display name before adapter migration")
		}
		mii := store.ProfileName(acct.GetDisplayName())
		if _, err = tx.Exec(ctx, `UPDATE pnids SET username=$3,mii_name=$4,updated_at=now() WHERE account_id=$1 AND pid=$2`, id, pid, acct.GetHandle(), mii); err != nil {
			return fmt.Errorf("canonical NNID migration failed: %w", err)
		}
	}
	if !*apply {
		fmt.Printf("Validated %d reviewed PNIDs; no writes.\n", len(ids))
		return nil
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("Applied %d canonical NNIDs; PIDs, NEX accounts and credentials preserved.\n", len(ids))
	return nil
}
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
