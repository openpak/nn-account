package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgconnCommandTag aliases the command tag type for the Querier interface.
type pgconnCommandTag = pgconn.CommandTag

// Querier abstracts *pgxpool.Pool and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconnCommandTag, error)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// GetServerByClientIDFallback resolves a server by client ID with access-
// mode ordering (upstream getServerByClientID: dev sees dev>test>prod).
// The servers schema column is access_mode (review P1 #5: the stale copy of
// this helper queried a dropped access_level column, erroring 42703).
func GetServerByClientIDFallback(ctx context.Context, q Querier, clientID, accessMode string) (*Server, error) {
	for _, mode := range AccessModeOrder(accessMode) {
		s, err := scanServer(q.QueryRow(ctx, `SELECT `+serverCols+` FROM servers WHERE client_id=$1 AND access_mode=$2`, clientID, mode))
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	return nil, pgx.ErrNoRows
}

// QuerierTx abstracts pgx.Tx for transactional multi-statement operations.
type QuerierTx = Querier
