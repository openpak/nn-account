// nn-account: OpenPak Wii U/3DS adapter (Go rewrite; PRD M2).
// Port provenance: Pretendo/account f7b1bc2 (AGPL-3.0).
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	pb "github.com/PretendoNetwork/grpc/go/account/v2"
	resolutionv1 "openpak/nn-account/proto/resolution/v1"

	"openpak/nn-account/internal/config"
	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/emulator"
	"openpak/nn-account/internal/grpcv2"
	"openpak/nn-account/internal/nasc"
	"openpak/nn-account/internal/nnas"
	"openpak/nn-account/internal/resolution"
	"openpak/nn-account/internal/resolvehttp"
	"openpak/nn-account/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		return err
	}

	core, err := coreclient.Dial(ctx, cfg.CoreAddress, cfg.CoreInternalKey)
	if err != nil {
		return err
	}
	defer core.Close()

	// Pretendo-compatible account gRPC v2.
	grpcSrv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcv2.Interceptor(cfg.GRPCAPIKey)))
	pb.RegisterAccountServiceServer(grpcSrv, grpcv2.New(pool, core, cfg.CDNBaseURL))
	resolutionv1.RegisterResolutionServer(grpcSrv, resolution.New(pool, core))
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("nn-account", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcSrv, healthSrv)

	grpcListener, err := net.Listen("tcp", cfg.GRPCListenAddr)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	nas := nnas.New(pool, core, cfg)
	nascSrv := nasc.New(pool, core)
	// nnas owns /v1/api/* plus /conntest and /cbvc; nasc owns /ac.
	// Mount nnas at root so all of its patterns resolve.
	mux.Handle("/", nas)
	mux.Handle("/ac", nascSrv)
	mux.Handle("/ac/", nascSrv)
	if cfg.InternalKey != "" {
		// Longest pattern wins: the resolve facade sits beside the emulator
		// surface under the one internal key.
		resolveHTTP := resolvehttp.New(pool, core, cfg.InternalKey)
		mux.Handle("/internal/resolve/", resolveHTTP)
		mux.Handle("/internal/online", resolveHTTP)
		mux.Handle("/internal/", emulator.New(pool, core, cfg.InternalKey))
	}

	httpSrv := &http.Server{
		Addr:              cfg.HTTPListenAddr,
		Handler:           hostRestrict(cfg, mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- grpcSrv.Serve(grpcListener) }()
	go func() { errCh <- httpSrv.ListenAndServe() }()

	// Durable invalidation event consumer: core bans/unlinks/deletions revoke
	// local tokens (PRD §7 consistency rules).
	go eventLoop(ctx, pool, core)
	// Accounts made before NNIDs were published on their links, and any
	// registration whose publish failed, get theirs now. Idempotent.
	go publishNNIDs(ctx, pool, core)

	log.Printf("nn-account adapter listening: grpc=%s http=%s", cfg.GRPCListenAddr, cfg.HTTPListenAddr)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	stop()
	grpcSrv.GracefulStop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// eventLoop polls core invalidation events and applies them locally with
// dedup via the processed_events row (PRD §7: dedup + version ordering).
func eventLoop(ctx context.Context, pool *pgxpool.Pool, core *coreclient.Client) {
	for {
		version, err := store.GetProcessedEventVersion(ctx, pool)
		if err != nil {
			log.Printf("events: read version: %v", err)
			sleepCtx(ctx, 10*time.Second)
			continue
		}
		var lastProcessed int64 = int64(version)
		page, err := core.PollEvents(ctx, uint64(version))
		if err != nil {
			sleepCtx(ctx, 10*time.Second)
			continue
		}
		// Durable retry (review P1 #8): process in order; on the first
		// failure STOP — the cursor stays put so the failed event (and the
		// rest of the page) is re-delivered next tick. Revocations are
		// idempotent, so redelivery is safe.
		failed := false
		for _, ev := range page.GetEvents() {
			var applyErr error
			switch ev.GetType() {
			case "account_banned", "account_delete_started", "account_deleted":
				var pids []int64
				pids, applyErr = store.PIDsForAccount(ctx, pool, ev.GetAccountId())
				if applyErr == nil {
					for _, pid := range pids {
						if applyErr = store.RevokeTokensForPID(ctx, pool, pid); applyErr != nil {
							break
						}
					}
				}
			case "link_unlinked":
				if ev.GetSubjectId() != "" {
					var pid int64
					if pid, applyErr = strconvI(ev.GetSubjectId()); applyErr == nil {
						applyErr = store.RevokeTokensForPID(ctx, pool, pid)
					}
				}
			case "account_unbanned":
				// Nothing to re-arm locally; ban revocations are permanent.
			default:
				log.Printf("events: unknown type %q (advancing)", ev.GetType())
			}
			if applyErr != nil {
				log.Printf("events: apply %s (version %d) failed, will retry: %v",
					ev.GetType(), ev.GetVersion(), applyErr)
				failed = true
				break
			}
			lastProcessed = int64(ev.GetVersion())
		}
		if failed {
			sleepCtx(ctx, 2*time.Second)
			continue
		}
		if len(page.GetEvents()) > 0 {
			if err := store.MarkEventProcessed(ctx, pool, int64(lastProcessed)); err != nil {
				log.Printf("events: mark processed: %v", err)
			}
		}
		sleepCtx(ctx, 5*time.Second)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func strconvI(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

// hostRestrict applies optional hostname allowlisting (upstream
// restrictHostnames behavior; empty list allows all, e.g. development).
func hostRestrict(cfg *config.Config, next http.Handler) http.Handler {
	if len(cfg.AllowedHostnames) == 0 {
		return next
	}
	allowed := map[string]bool{}
	for _, h := range cfg.AllowedHostnames {
		allowed[strings.TrimSpace(h)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		if !allowed[host] {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// publishNNIDs puts every PNID's name on its Wii U link, once at startup.
func publishNNIDs(ctx context.Context, pool *pgxpool.Pool, core *coreclient.Client) {
	names, err := store.ListPNIDNames(ctx, pool)
	if err != nil {
		log.Printf("publish NNIDs: list: %v", err)
		return
	}
	failed := 0
	for _, n := range names {
		if err := core.PublishNNID(ctx, n.PID, n.Username); err != nil {
			failed++
			log.Printf("publish NNIDs: pid %d: %v", n.PID, err)
		}
	}
	log.Printf("publish NNIDs: %d names, %d failed", len(names), failed)
}
