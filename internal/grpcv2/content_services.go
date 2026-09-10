package grpcv2

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	pb "github.com/PretendoNetwork/grpc/go/account/v2"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"openpak/nn-account/internal/store"
)

// The RPCs the content services (BOSS, Juxtaposition) consume, ported from
// upstream's v2 handlers. Field minimization as in GetUserData: no email,
// birthdate, gender or tier; permission flags are all false because OpenPak
// moderation lives in the core, not in PNID bits.

// GetNEXData returns the NEX account bound to a PID (BOSS: task ownership).
func (s *Server) GetNEXData(ctx context.Context, req *pb.GetNEXDataRequest) (*pb.GetNEXDataResponse, error) {
	nexAccount, err := store.GetNEXAccountByPID(ctx, s.pool, int64(req.GetPid()))
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.InvalidArgument, "No NEX account found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &pb.GetNEXDataResponse{
		Pid:               uint32(nexAccount.PID),
		Password:          nexAccount.Password,
		OwningPid:         uint32(nexAccount.OwningPIDOrSelf()),
		AccessLevel:       nexAccount.AccessLevel,
		ServerAccessLevel: nexAccount.ServerAccessLevel,
		FriendCode:        nexAccount.FriendCode,
		DeviceType:        deviceTypeEnum(nexAccount.DeviceType),
	}, nil
}

// ExchangeTokenForUserData resolves an OAuth access token to its PNID
// (BOSS and the Miiverse applet authenticate console sessions with it).
func (s *Server) ExchangeTokenForUserData(ctx context.Context, req *pb.ExchangeTokenForUserDataRequest) (*pb.ExchangeTokenForUserDataResponse, error) {
	if req.GetToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "Invalid token")
	}
	pnid, err := store.GetPNIDByAccessToken(ctx, s.pool, req.GetToken())
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.InvalidArgument, "Invalid token")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if err := s.checkCoreStatus(ctx, pnid.AccountID); err != nil {
		return nil, err
	}
	return &pb.ExchangeTokenForUserDataResponse{
		Deleted:           pnid.Deleted,
		Pid:               uint32(pnid.PID),
		Username:          pnid.Username,
		AccessLevel:       pnid.AccessLevel,
		ServerAccessLevel: pnid.ServerAccessLevel,
		Mii:               s.mii(pnid),
		CreationDate:      pnid.CreationDate.UTC().Format("2006-01-02T15:04:05Z"),
		Country:           pnid.Country,
		Language:          pnid.Language,
		Permissions:       &pb.PNIDPermissionFlags{},
	}, nil
}

// ValidateIndependentServiceToken checks a game-server-minted service token
// (60 bytes hex: 28-byte body, HMAC-SHA256 with the server's AES key).
func (s *Server) ValidateIndependentServiceToken(ctx context.Context, req *pb.ValidateIndependentServiceTokenRequest) (*pb.ValidateIndependentServiceTokenResponse, error) {
	if len(req.GetClientIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "No server identification data sent")
	}
	invalid := &pb.ValidateIndependentServiceTokenResponse{IsValid: false}
	server := s.serverForClientIDs(ctx, req.GetClientIds())
	if server == nil {
		return invalid, nil
	}
	token, err := hex.DecodeString(req.GetToken())
	if err != nil || len(token) != 60 {
		return invalid, nil
	}
	mac := hmac.New(sha256.New, []byte(server.AESKey))
	mac.Write(token[:28])
	if !hmac.Equal(token[28:], mac.Sum(nil)) {
		return invalid, nil
	}
	pid := int64(uint32(token[0])<<24 | uint32(token[1])<<16 | uint32(token[2])<<8 | uint32(token[3]))
	nexAccount, err := store.GetNEXAccountByPID(ctx, s.pool, pid)
	if err != nil {
		return invalid, nil
	}
	level := nexAccount.AccessLevel
	if pnid, err := store.GetPNIDByPID(ctx, s.pool, pid); err == nil {
		level = pnid.AccessLevel
	}
	return &pb.ValidateIndependentServiceTokenResponse{IsValid: true, BasicUserInfo: basicUserInfo(level)}, nil
}

// ExchangeIndependentServiceTokenForUserData resolves a service token the
// adapter issued (NNAS /provider/service_token) to the PNID and NEX account
// behind it: what Juxtaposition and BOSS call on every console request.
func (s *Server) ExchangeIndependentServiceTokenForUserData(ctx context.Context, req *pb.ExchangeIndependentServiceTokenForUserDataRequest) (*pb.ExchangeIndependentServiceTokenForUserDataResponse, error) {
	var (
		clientID string
		pid      int64
		titleID  int64
		issued   time.Time
		expires  time.Time
	)
	err := s.pool.QueryRow(ctx, `SELECT client_id, pid, title_id, issued_at, expires_at FROM independent_service_tokens WHERE token_hash=$1`,
		store.HashToken(req.GetToken())).Scan(&clientID, &pid, &titleID, &issued, &expires)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && expires.Before(time.Now())) {
		return nil, status.Error(codes.InvalidArgument, "Invalid token")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if ids := req.GetClientIds(); len(ids) > 0 && !contains(ids, clientID) {
		return nil, status.Error(codes.InvalidArgument, "Invalid token")
	}
	nexAccount, err := store.GetNEXAccountByPID(ctx, s.pool, pid)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid token. No user found")
	}
	systemType := pb.SystemType_SYSTEM_TYPE_WUP
	if nexAccount.DeviceType == "3ds" {
		systemType = pb.SystemType_SYSTEM_TYPE_CTR
	}
	resp := &pb.ExchangeIndependentServiceTokenForUserDataResponse{
		NexAccount: &pb.NEXAccount{
			Pid:               uint32(nexAccount.PID),
			AccessLevel:       nexAccount.AccessLevel,
			ServerAccessLevel: nexAccount.ServerAccessLevel,
			FriendCode:        &nexAccount.FriendCode,
			DeviceType:        &nexAccount.DeviceType,
		},
		TokenInfo: &pb.TokenInfo{
			SystemType:  systemType,
			TokenType:   pb.TokenType_TOKEN_TYPE_INDEPENDENT_SERVICE,
			Pid:         uint64(pid),
			AccessLevel: nexAccount.AccessLevel,
			TitleId:     uint64(titleID),
			IssueTime:   timestamppb.New(issued),
			ExpireTime:  timestamppb.New(expires),
		},
		BasicUserInfo: basicUserInfo(nexAccount.AccessLevel),
	}
	if nexAccount.OwningPID != nil {
		owning := uint32(*nexAccount.OwningPID)
		resp.NexAccount.OwningPid = &owning
	}
	pnid, err := store.GetPNIDByPID(ctx, s.pool, nexAccount.OwningPIDOrSelf())
	if err == nil {
		if err := s.checkCoreStatus(ctx, pnid.AccountID); err != nil {
			return nil, err
		}
		resp.Pnid = &pb.GetPNIDResponse{
			Deleted:           pnid.Deleted,
			Pid:               uint32(pnid.PID),
			Username:          pnid.Username,
			AccessLevel:       pnid.AccessLevel,
			ServerAccessLevel: pnid.ServerAccessLevel,
			Mii:               s.mii(pnid),
			CreationDate:      pnid.CreationDate.UTC().Format("2006-01-02T15:04:05Z"),
			Country:           pnid.Country,
			Language:          pnid.Language,
			Permissions:       &pb.PNIDPermissionFlags{},
		}
		resp.TokenInfo.AccessLevel = pnid.AccessLevel
		resp.BasicUserInfo = basicUserInfo(pnid.AccessLevel)
	}
	return resp, nil
}

func (s *Server) mii(p *store.PNID) *pb.Mii {
	return &pb.Mii{Name: p.MiiName, Data: p.MiiData, Url: s.cdn + "/mii/" + itoa(p.PID) + "/standard.tga"}
}

func (s *Server) serverForClientIDs(ctx context.Context, clientIDs []string) *store.Server {
	for _, id := range clientIDs {
		for _, mode := range []string{"prod", "test", "dev"} {
			if srv, err := store.GetServerByClientIDFallback(ctx, s.pool, id, mode); err == nil && srv != nil {
				return srv
			}
		}
	}
	return nil
}

func basicUserInfo(level int32) *pb.BasicUserInfo {
	return &pb.BasicUserInfo{AccessBetaServers: level >= 1 && level <= 3, AccessDeveloperServers: level == 3}
}

func deviceTypeEnum(t string) pb.DeviceType {
	switch t {
	case "3ds":
		return pb.DeviceType_DEVICE_TYPE_CTR
	case "wiiu":
		return pb.DeviceType_DEVICE_TYPE_WUP
	}
	return pb.DeviceType_DEVICE_TYPE_UNSPECIFIED
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
