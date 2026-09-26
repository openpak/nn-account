package grpcv2

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	accountv1 "openpak/nn-account/internal/accountpb"
)

// OnlineMarker is the core's MarkOnline, when the core client has it.
type OnlineMarker interface {
	MarkOnline(ctx context.Context, players []*accountv1.OnlinePlayer) (int32, error)
}

// goAsync runs the core call off the login's path; tests make it synchronous.
var goAsync = func(f func()) { go f() }

// markOnlineLastLog is the unix second of the last logged failure (one a minute at most).
var markOnlineLastLog atomic.Int64

// markOnline tells the core a game server just authenticated this account, which starts its
// playtime (the core only marks a live titled session, so anything else is a no-op there).
// deviceType is the NEX account's console, "wiiu" or "3ds", which is the namespace. It never
// slows or fails the login: fire and forget with a short timeout.
func (s *Server) markOnline(accountID, deviceType string) {
	m, ok := s.core.(OnlineMarker)
	if !ok || accountID == "" {
		return
	}
	ns := "wiiu"
	if deviceType == "3ds" {
		ns = "3ds"
	}
	goAsync(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := m.MarkOnline(ctx, []*accountv1.OnlinePlayer{{AccountId: accountID, Namespace: ns}})
		if err == nil {
			return
		}
		now := time.Now().Unix()
		if last := markOnlineLastLog.Load(); now-last >= 60 && markOnlineLastLog.CompareAndSwap(last, now) {
			log.Printf("grpcv2: mark online (logged at most once a minute): %v", err)
		}
	})
}
