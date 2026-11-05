// Package eraseapi serves DELETE /internal/accounts/{account_id}/data, the
// call the website's erasure finaliser makes (card openpak/prds#120).
package eraseapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/profilecore"
	"openpak/nn-account/internal/store"
)

const Service = "nn-account"

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Erase removes the account's adapter data. core may be nil (event path: the
// links are already gone), then only the PNID table decides the PIDs.
func Erase(ctx context.Context, pool *pgxpool.Pool, core *coreclient.Client, accountID string) (int, error) {
	var extra []int64
	if core != nil {
		subjects, err := core.ListLinkSubjects(ctx, accountID)
		if err != nil {
			return 0, err
		}
		for _, s := range subjects {
			if pid, err := strconv.ParseInt(s, 10, 64); err == nil {
				extra = append(extra, pid)
			}
		}
	}
	n, err := store.EraseAccount(ctx, pool, accountID, extra)
	if profilecore.Default != nil {
		profilecore.Default.Invalidate(accountID) // no profile of an erased account stays in memory
	}
	if err != nil {
		return 0, err
	}
	log.Printf("account erased account=%s rows=%d service=%s", accountID, n, Service)
	return n, nil
}

type Handler struct {
	Key   string
	Erase func(ctx context.Context, accountID string) (int, error)
}

func New(key string, pool *pgxpool.Pool, core *coreclient.Client) *Handler {
	return &Handler{Key: key, Erase: func(ctx context.Context, id string) (int, error) {
		return Erase(ctx, pool, core, id)
	}}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Key == "" {
		http.Error(w, `{"error":"erasure not configured"}`, http.StatusServiceUnavailable)
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") ||
		subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(h.Key)) != 1 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	id := r.PathValue("account_id")
	if !uuidRe.MatchString(id) {
		http.Error(w, `{"error":"account_id must be a UUID"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	n, err := h.Erase(ctx, strings.ToLower(id))
	if err != nil {
		log.Printf("erase: account=%s: %v", id, err)
		http.Error(w, `{"error":"erase failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"service": Service, "rows": n})
}
