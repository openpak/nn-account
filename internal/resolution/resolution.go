// Package resolution exposes PID → core-account mapping to internal
// consumers (nn-friends), per PRD §7 "Nintendo identity resolution":
// consumers never address the core by PID, and resolution only succeeds
// for links the core has activated.
package resolution

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"openpak/nn-account/internal/coreclient"
	"openpak/nn-account/internal/store"
	resolutionv1 "openpak/nn-account/proto/resolution/v1"
)

type Server struct {
	resolutionv1.UnimplementedResolutionServer
	pool *pgxpool.Pool
	core *coreclient.Client
}

func New(pool *pgxpool.Pool, core *coreclient.Client) *Server {
	return &Server{pool: pool, core: core}
}

func (s *Server) ResolvePid(ctx context.Context, req *resolutionv1.ResolvePidRequest) (*resolutionv1.ResolvePidResponse, error) {
	ns := req.GetNamespace()
	if ns != "wiiu" && ns != "3ds" {
		return nil, status.Error(codes.InvalidArgument, "namespace must be wiiu or 3ds")
	}
	pnid, err := store.GetPNIDByPID(ctx, s.pool, int64(req.GetPid()))
	if errors.Is(err, store.ErrNotFound) {
		return &resolutionv1.ResolvePidResponse{Found: false}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if pnid.Deleted {
		return &resolutionv1.ResolvePidResponse{Found: false}, nil
	}
	// The link must be ACTIVE in the core (fail-closed on core errors).
	// Subjects are registered as the PID string under the creating
	// namespace; probe the requested namespace first, then the other, so
	// cross-family registrations still resolve.
	subject := itoa(pnid.PID)
	if _, err := s.core.GetActiveLink(ctx, ns, subject); err == nil {
		return &resolutionv1.ResolvePidResponse{Found: true, AccountId: pnid.AccountID}, nil
	}
	other := "wiiu"
	if ns == "wiiu" {
		other = "3ds"
	}
	if _, err := s.core.GetActiveLink(ctx, other, subject); err == nil {
		return &resolutionv1.ResolvePidResponse{Found: true, AccountId: pnid.AccountID}, nil
	}
	return &resolutionv1.ResolvePidResponse{Found: false}, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func (s *Server) ResolveAccount(ctx context.Context, req *resolutionv1.ResolveAccountRequest) (*resolutionv1.ResolveAccountResponse, error) {
	ns := req.GetNamespace()
	if ns != "wiiu" && ns != "3ds" {
		return nil, status.Error(codes.InvalidArgument, "namespace must be wiiu or 3ds")
	}
	pnid, err := store.GetPNIDByAccountID(ctx, s.pool, req.GetAccountId())
	if errors.Is(err, store.ErrNotFound) {
		return &resolutionv1.ResolveAccountResponse{Found: false}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &resolutionv1.ResolveAccountResponse{Found: true, Pid: uint32(pnid.PID)}, nil
}
