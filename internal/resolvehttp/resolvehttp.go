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
	"log"
	"net/http"
	"strconv"
	"time"

	"openpak/nn-account/internal/resolution"
	resolutionv1 "openpak/nn-account/proto/resolution/v1"

	"github.com/jackc/pgx/v5/pgxpool"

	"openpak/nn-account/internal/coreclient"
)

type Server struct {
	res *resolution.Server
	key string
}

func New(pool *pgxpool.Pool, core *coreclient.Client, key string) *Server {
	return &Server{res: resolution.New(pool, core), key: key}
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

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
