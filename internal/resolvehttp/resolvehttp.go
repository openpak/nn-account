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
	"strings"
	"time"

	accountv1 "openpak/nn-account/internal/accountpb"

	"openpak/nn-account/internal/resolution"
	resolutionv1 "openpak/nn-account/proto/resolution/v1"

	"github.com/jackc/pgx/v5/pgxpool"

	"openpak/nn-account/internal/coreclient"
)

type Server struct {
	res *resolution.Server
	key string

	// resolve and mark back POST /internal/online; tests replace them.
	resolve func(ctx context.Context, ns string, pid uint32) (string, error)
	mark    func(ctx context.Context, players []*accountv1.OnlinePlayer) (int32, error)
}

func New(pool *pgxpool.Pool, core *coreclient.Client, key string) *Server {
	s := &Server{res: resolution.New(pool, core), key: key, mark: core.MarkOnline}
	s.resolve = func(ctx context.Context, ns string, pid uint32) (string, error) {
		resp, err := s.res.ResolvePid(ctx, &resolutionv1.ResolvePidRequest{Namespace: ns, Pid: pid})
		if err != nil || !resp.GetFound() {
			return "", err
		}
		return resp.GetAccountId(), nil
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
	case "/internal/online":
		s.online(w, r)
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

// maxOnline matches the core's per-call cap on MarkOnline.
const maxOnline = 1000

// online is a game server's roll call: POST /internal/online
// {"namespace":"wiiu"|"3ds","title_id":"optional","pids":[…]} -> {"marked":N,"unknown":M}.
// Playtime counts only time a game server confirmed the player online, and
// game servers hold no core credentials, so they report here. Each pid is
// resolved exactly as /internal/resolve/pid does; unknown ones are skipped.
func (s *Server) online(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Namespace string   `json:"namespace"`
		TitleID   string   `json:"title_id"`
		PIDs      []uint32 `json:"pids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil ||
		(in.Namespace != "wiiu" && in.Namespace != "3ds") {
		http.Error(w, `{"error":"namespace (wiiu or 3ds) and pids are required"}`, http.StatusBadRequest)
		return
	}
	if len(in.PIDs) > maxOnline {
		http.Error(w, `{"error":"at most 1000 pids per call"}`, http.StatusBadRequest)
		return
	}
	title := strings.ToLower(in.TitleID)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	seen := map[string]bool{}
	players := make([]*accountv1.OnlinePlayer, 0, len(in.PIDs))
	unknown := 0
	for _, pid := range in.PIDs {
		id, err := s.resolve(ctx, in.Namespace, pid)
		if err != nil {
			log.Printf("resolvehttp: online pid %d: %v", pid, err)
		}
		if id == "" {
			unknown++
			continue
		}
		if !seen[id] {
			seen[id] = true
			players = append(players, &accountv1.OnlinePlayer{AccountId: id, Namespace: in.Namespace, TitleId: title})
		}
	}
	var marked int32
	if len(players) > 0 {
		var err error
		if marked, err = s.mark(ctx, players); err != nil {
			log.Printf("resolvehttp: mark online: %v", err)
			http.Error(w, `{"error":"core unavailable"}`, http.StatusBadGateway)
			return
		}
	}
	jsonOut(w, map[string]any{"marked": marked, "unknown": unknown})
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
