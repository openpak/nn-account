// Package resolvehttp is the plain-HTTP face of the Resolution service, for
// internal consumers that speak HTTP and have no gRPC stubs for this adapter
// (the Miiverse bridge is TypeScript; buf codegen for our protos is not part
// of its toolchain). Same rules as the gRPC service: resolution only
// succeeds for identities the adapter actually stands behind.
package resolvehttp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"openpak/nn-account/internal/resolution"
	resolutionv1 "openpak/nn-account/proto/resolution/v1"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/store"
)

type Server struct {
	res *resolution.Server
	key string
	// wiiuName finds the Wii U name of a core account: the NNID and PID, or ok=false when the account has no
	// console identity the adapter stands behind. Replaced in tests.
	wiiuName func(ctx context.Context, accountID string) (nnid string, pid int64, ok bool, err error)
}

func New(pool *pgxpool.Pool, core *coreclient.Client, key string) *Server {
	s := &Server{res: resolution.New(pool, core), key: key}
	s.wiiuName = func(ctx context.Context, accountID string) (string, int64, bool, error) {
		pnid, err := store.GetPNIDByAccountID(ctx, pool, accountID)
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return "", 0, false, nil
		}
		if err != nil {
			return "", 0, false, err
		}
		// A shadow stands for a Switch or phone user on a friend list; it was never a Wii U account.
		if pnid.Deleted || pnid.Shadow || pnid.Username == "" {
			return "", 0, false, nil
		}
		// Same fail-closed rule as ResolvePid: the core must hold an active link for the PID.
		resp, err := s.res.ResolvePid(ctx, &resolutionv1.ResolvePidRequest{Namespace: "wiiu", Pid: uint32(pnid.PID)})
		if err != nil {
			return "", 0, false, err
		}
		if !resp.GetFound() || resp.GetAccountId() != accountID {
			return "", 0, false, nil
		}
		return pnid.Username, pnid.PID, true, nil
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.key == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Key")), []byte(s.key)) != 1 {
		http.Error(w, `{"error":"internal"}`, http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/internal/resolve/pid":
		s.pid(w, r)
	case "/internal/resolve/account":
		s.account(w, r)
	case "/internal/wiiu-nnid":
		s.nnid(w, r)
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func (s *Server) pid(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	pid, err := strconv.ParseUint(r.URL.Query().Get("pid"), 10, 32)
	if err != nil || (ns != "wiiu" && ns != "3ds") {
		http.Error(w, `{"error":"namespace and pid are required"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.res.ResolvePid(ctx, &resolutionv1.ResolvePidRequest{Namespace: ns, Pid: uint32(pid)})
	if err != nil {
		log.Printf("resolvehttp: pid %d: %v", pid, err)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	jsonOut(w, map[string]any{"found": resp.GetFound(), "account_id": resp.GetAccountId()})
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	account := r.URL.Query().Get("account")
	if account == "" || (ns != "wiiu" && ns != "3ds") {
		http.Error(w, `{"error":"namespace and account are required"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := s.res.ResolveAccount(ctx, &resolutionv1.ResolveAccountRequest{Namespace: ns, AccountId: account})
	if err != nil {
		log.Printf("resolvehttp: account %s: %v", account, err)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	jsonOut(w, map[string]any{"found": resp.GetFound(), "pid": resp.GetPid()})
}

// nnid answers GET /internal/wiiu-nnid?account_id=<core account id> with {"nnid": "...", "pid": 123}, or 404 when
// the account has no Wii U name. OpenMaker asks it to match an archived maker's Network ID to a signed-in account.
func (s *Server) nnid(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account_id")
	if account == "" {
		http.Error(w, `{"error":"account_id is required"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	nnid, pid, ok, err := s.wiiuName(ctx, account)
	if err != nil {
		log.Printf("resolvehttp: wiiu-nnid %s: %v", account, err)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, `{"error":"not linked"}`, http.StatusNotFound)
		return
	}
	jsonOut(w, map[string]any{"nnid": nnid, "pid": pid})
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
