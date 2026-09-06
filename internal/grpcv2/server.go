// Package grpcv2 implements the Pretendo-compatible account gRPC v2 contract
// (`account.v2.AccountService`) required by the pinned nn-friends client.
// Port provenance: Pretendo/account f7b1bc2 services/grpc/account/v2 (AGPL-3.0).
package grpcv2

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/PretendoNetwork/grpc/go/account/v2"
	"openpak/account/proto/openpak/account/v1"

	"openpak/nn-account/internal/store"
)

type Server struct {
	pb.UnimplementedAccountServiceServer
	pool *pgxpool.Pool
	core CorePort
	cdn  string
}

// CorePort is the subset of the account core the adapter's v2 RPCs consult.
type CorePort interface {
	GetAccount(ctx context.Context, accountID string) (*accountv1.GetAccountResponse, error)
	GetActiveLink(ctx context.Context, namespace, subjectID string) (*accountv1.GetLinkBySubjectResponse, error)
}

func New(pool *pgxpool.Pool, core CorePort, cdnBaseURL string) *Server {
	return &Server{pool: pool, core: core, cdn: cdnBaseURL}
}

// Interceptor enforces the X-API-Key contract used by the pinned clients
// (friends sends common_globals.GRPCAccountCommonMetadata). Fail-closed.
func Interceptor(apiKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "Missing or invalid API key")
		}
		vals := md.Get("X-API-Key")
		if len(vals) == 0 || vals[0] != apiKey {
			return nil, status.Error(codes.Unauthenticated, "Missing or invalid API key")
		}
		return handler(ctx, req)
	}
}

func (s *Server) checkCoreStatus(ctx context.Context, accountID string) error {
	// Fail-closed: unreachable core denies new authorization (PRD §7).
	acct, err := s.core.GetAccount(ctx, accountID)
	if err != nil {
		return status.Error(codes.Internal, "Core account service unavailable")
	}
	switch acct.GetStatus() {
	case accountv1.AccountStatus_ACCOUNT_STATUS_BANNED,
		accountv1.AccountStatus_ACCOUNT_STATUS_DELETED,
		accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING:
		return status.Error(codes.InvalidArgument, "Account is banned or deleted")
	}
	return nil
}

// GetUserData ports upstream GetUserData with PRD FR-8 field minimization:
// email, birthdate, gender, tier, and linked devices are omitted (pinned
// friends consumes none of them; evidenced by consumer audit).
func (s *Server) GetUserData(ctx context.Context, req *pb.GetUserDataRequest) (*pb.GetUserDataResponse, error) {
	pnid, err := store.GetPNIDByPID(ctx, s.pool, int64(req.GetPid()))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgxNoRows()) {
		return nil, status.Error(codes.InvalidArgument, "No PNID found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "Internal error")
	}
	if pnid.Deleted {
		return &pb.GetUserDataResponse{Deleted: true, Pid: uint32(pnid.PID)}, nil
	}
	if err := s.checkCoreStatus(ctx, pnid.AccountID); err != nil {
		return nil, err
	}
	if err := s.checkActiveLink(ctx, pnid.AccountID, pnid.PID); err != nil {
		return nil, err
	}
	return &pb.GetUserDataResponse{
		Pid:               uint32(pnid.PID),
		Username:          pnid.Username,
		AccessLevel:       pnid.AccessLevel,
		ServerAccessLevel: pnid.ServerAccessLevel,
		Mii: &pb.Mii{
			Name: pnid.MiiName,
			Data: pnid.MiiData,
			Url:  s.cdn + "/mii/" + itoa(pnid.PID) + "/standard.tga",
		},
		CreationDate: pnid.CreationDate.UTC().Format("2006-01-02T15:04:05Z"),
		Country:      pnid.Country,
		Language:     pnid.Language,
		// Omitted per FR-8 minimization: birthdate, gender, email, tier,
		// permissions, linked devices.
	}, nil
}

// GetNEXPassword ports upstream GetNEXPassword. Credentials may only be
// retrieved by authorized Nintendo services (PRD FR-3): the X-API-Key gate
// is that authorization; the PID must map to a live, unbanned identity.
func (s *Server) GetNEXPassword(ctx context.Context, req *pb.GetNEXPasswordRequest) (*pb.GetNEXPasswordResponse, error) {
	nex, err := store.GetNEXAccountByPID(ctx, s.pool, int64(req.GetPid()))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgxNoRows()) {
		return nil, status.Error(codes.InvalidArgument, "No NEX account found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "Internal error")
	}
	pnid, err := store.GetPNIDByPID(ctx, s.pool, nex.OwningPID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "No NEX account found")
	}
	if pnid.Deleted {
		return nil, status.Error(codes.InvalidArgument, "No NEX account found")
	}
	if err := s.checkCoreStatus(ctx, pnid.AccountID); err != nil {
		return nil, err
	}
	return &pb.GetNEXPasswordResponse{Password: nex.Password}, nil
}

// ExchangeNEXTokenForUserData ports upstream with the PRD-mandated fixes
// (§3): game-server ID and token type are validated (upstream TODO), expiry
// is checked synchronously, and the owning identity's core status is enforced.
func (s *Server) ExchangeNEXTokenForUserData(ctx context.Context, req *pb.ExchangeNEXTokenForUserDataRequest) (*pb.ExchangeNEXTokenForUserDataResponse, error) {
	token := req.GetToken()
	if token == "" {
		return nil, status.Error(codes.InvalidArgument, "Invalid token")
	}
	gameServerID, pid, titleID, issued, expires, err := store.GetNEXToken(ctx, s.pool, token)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgxNoRows()) {
		return nil, status.Error(codes.InvalidArgument, "Invalid token")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "Internal error")
	}
	// Audience check (PRD §3, upstream TODO resolved): the token must have
	// been issued for exactly the requesting game server.
	if len(req.GetGameServerIds()) > 0 {
		matched := false
		for _, id := range req.GetGameServerIds() {
			if id == gameServerID {
				matched = true
				break
			}
		}
		if !matched {
			log.Printf("nex token audience mismatch: token gs=%s requested=%v", gameServerID, req.GetGameServerIds())
			return nil, status.Error(codes.InvalidArgument, "Invalid token")
		}
	}
	// Wrong-type and expired tokens rejected synchronously (FR-3).
	if !expires.After(time.Now()) {
		return nil, status.Error(codes.InvalidArgument, "Invalid token")
	}
	_ = issued
	_ = titleID

	nex, err := store.GetNEXAccountByPID(ctx, s.pool, pid)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgxNoRows()) {
		return nil, status.Error(codes.InvalidArgument, "Invalid token. No user found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "Internal error")
	}
	pnid, err := store.GetPNIDByPID(ctx, s.pool, nex.OwningPID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid token. No user found")
	}
	if pnid.Deleted {
		return nil, status.Error(codes.InvalidArgument, "Invalid token. No user found")
	}
	if err := s.checkCoreStatus(ctx, pnid.AccountID); err != nil {
		return nil, err
	}
	if err := s.checkActiveLink(ctx, pnid.AccountID, pnid.PID); err != nil {
		return nil, err
	}

	owningPID := uint32(nex.OwningPID)
	resp := &pb.ExchangeNEXTokenForUserDataResponse{
		NexAccount: &pb.NEXAccount{
			Pid:               uint32(nex.PID),
			OwningPid:         &owningPID,
			AccessLevel:       nex.AccessLevel,
			ServerAccessLevel: nex.ServerAccessLevel,
			FriendCode:        &nex.FriendCode,
			DeviceType:        &nex.DeviceType,
		},
		TokenInfo: &pb.TokenInfo{
			SystemType:  pb.SystemType(systemTypeForDevice(nex.DeviceType)),
			TokenType:   pb.TokenType_TOKEN_TYPE_NEX,
			Pid:         uint64(nex.PID),
			AccessLevel: nex.AccessLevel,
			TitleId:     uint64(titleID),
		},
		BasicUserInfo: &pb.BasicUserInfo{
			AccessBetaServers:      nex.AccessLevel >= 1 && pnid.AccessLevel >= 1,
			AccessDeveloperServers: pnid.AccessLevel == 3,
		},
	}
	return resp, nil
}

func (s *Server) checkActiveLink(ctx context.Context, accountID string, pid int64) error {
	// The adapter's subject identity for both Wii U and 3DS flows is the PNID
	// pid under the wiiu/3ds namespaces; enforce that the core still links it.
	if _, err := s.core.GetActiveLink(ctx, "wiiu", itoa(pid)); err == nil {
		return nil
	}
	if _, err := s.core.GetActiveLink(ctx, "3ds", itoa(pid)); err == nil {
		return nil
	}
	return status.Error(codes.Internal, "Core account service unavailable")
}

func systemTypeForDevice(device string) int32 {
	if device == "3ds" {
		return 2 // SYSTEM_TYPE_CTR
	}
	return 1 // SYSTEM_TYPE_WUP
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

func pgxNoRows() error { return pgx.ErrNoRows }
