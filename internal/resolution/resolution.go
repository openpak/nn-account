// Package resolution exposes PID → core-account mapping to internal
// consumers (nn-friends), per PRD §7 "Nintendo identity resolution":
// consumers never address the core by PID, and resolution only succeeds
// for links the core has activated.
package resolution

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	accountv1 "openpak/nn-account/internal/accountpb"

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
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return &resolutionv1.ResolvePidResponse{Found: false}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if pnid.Deleted {
		return &resolutionv1.ResolvePidResponse{Found: false}, nil
	}
	if pnid.Shadow {
		// A shadow has no console link by definition; it stands for the core
		// account as long as that account is not gone.
		if acct, err := s.core.GetAccount(ctx, pnid.AccountID); err != nil ||
			acct.GetStatus() == accountv1.AccountStatus_ACCOUNT_STATUS_DELETED ||
			acct.GetStatus() == accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING {
			return &resolutionv1.ResolvePidResponse{Found: false}, nil
		}
		return &resolutionv1.ResolvePidResponse{Found: true, AccountId: pnid.AccountID}, nil
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
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		// No console of this family: mint the account's shadow PNID so a Wii U or
		// 3DS friend list can show a Switch or phone user by PID, name and Mii.
		pnid, err = s.mintShadow(ctx, req.GetAccountId())
		if errors.Is(err, errNoSuchAccount) {
			return &resolutionv1.ResolveAccountResponse{Found: false}, nil
		}
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &resolutionv1.ResolveAccountResponse{Found: true, Pid: uint32(pnid.PID)}, nil
}

var errNoSuchAccount = errors.New("resolution: no such core account")

// defaultMiiData is upstream's registration default ("Default"), base64 FFLStoreData.
const defaultMiiData = "AwAAQOlVognnx0GC2/uogAOzuI0n2QAAAEBEAGUAZgBhAHUAbAB0AAAAAAAAAEBAAAAhAQJoRBgmNEYUgRIXaA0AACkAUkhQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGm9"

func (s *Server) mintShadow(ctx context.Context, accountID string) (*store.PNID, error) {
	acct, err := s.core.GetAccount(ctx, accountID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, errNoSuchAccount
		}
		return nil, err
	}
	switch acct.GetStatus() {
	case accountv1.AccountStatus_ACCOUNT_STATUS_DELETED, accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING:
		return nil, errNoSuchAccount
	}
	pid, err := store.GeneratePID(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	miiName := acct.GetDisplayName()
	if r := []rune(miiName); len(r) > 10 {
		miiName = string(r[:10])
	}
	if miiName == "" {
		miiName = "OpenPak"
	}
	p := &store.PNID{PID: pid, AccountID: accountID, ServerAccessLevel: "prod", MiiName: miiName,
		MiiData: defaultMiiData, Country: "US", Language: "en", Region: 1, TimezoneName: "EST5EDT", Shadow: true}
	// The NNID shown next to the name: the display name where it fits the NNID
	// alphabet and is free, otherwise a generated one. Two shadows can race for
	// the same account; the loser reads the winner's row.
	for _, username := range []string{nnidFromDisplayName(acct.GetDisplayName()), "op" + itoa(pid)} {
		if username == "" {
			continue
		}
		p.Username = username
		err = store.InsertPNID(ctx, s.pool, p)
		if err == nil {
			return p, nil
		}
		if !store.IsUniqueViolation(err) {
			return nil, err
		}
	}
	if existing, e := store.GetPNIDByAccountID(ctx, s.pool, accountID); e == nil {
		return existing, nil
	}
	return nil, err
}

// nnidFromDisplayName keeps only the NNID alphabet (letters, digits, - _ .) and
// requires the 6..16 length NNAS enforces; "" when the name does not qualify.
func nnidFromDisplayName(name string) string {
	var b []byte
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b = append(b, byte(r))
		}
	}
	if len(b) < 6 || len(b) > 16 {
		return ""
	}
	return string(b)
}

// ResolveNexTokenClient reads back the client a NEX token was issued to and
// the OS it runs on (package clientid). The caller has already had the token validated through
// ExchangeNEXTokenForUserData; this only names where it came from.
func (s *Server) ResolveNexTokenClient(ctx context.Context, req *resolutionv1.ResolveNexTokenClientRequest) (*resolutionv1.ResolveNexTokenClientResponse, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}
	client, os, pid, err := store.GetNEXTokenClient(ctx, s.pool, req.GetToken())
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		return &resolutionv1.ResolveNexTokenClientResponse{Found: false}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &resolutionv1.ResolveNexTokenClientResponse{Found: true, Client: client, Os: os, Pid: uint32(pid)}, nil
}
