package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

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

// GetServerByClientID resolves a server by client ID, preferring the user's
// access level and falling back to prod (upstream getServerByClientID).
func GetServerByClientIDFallback(ctx context.Context, q Querier, clientID, accessLevel string) (*Server, error) {
	if s, err := scanServer(q.QueryRow(ctx, `SELECT `+serverCols+` FROM servers WHERE client_id=$1 AND access_level=$2`, clientID, accessLevel)); err == nil {
		return s, nil
	}
	return scanServer(q.QueryRow(ctx, `SELECT `+serverCols+` FROM servers WHERE client_id=$1 AND access_level='prod'`, clientID))
}

// QuerierTx abstracts pgx.Tx for transactional multi-statement operations.
type QuerierTx = Querier
