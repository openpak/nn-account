package grpcv2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pb "github.com/PretendoNetwork/grpc/go/account/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountv1 "openpak/nn-account/internal/accountpb"
	"openpak/nn-account/internal/store"
)

// fakeCore is the core as GetNEXPassword's marking sees it.
type fakeCore struct {
	CorePort
	mu      sync.Mutex
	players []*accountv1.OnlinePlayer
	err     error
	block   chan struct{} // when set, MarkOnline hangs until closed
	called  chan struct{}
}

func (f *fakeCore) MarkOnline(_ context.Context, p []*accountv1.OnlinePlayer) (int32, error) {
	if f.called != nil {
		close(f.called)
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.players = append(f.players, p...)
	return int32(len(p)), f.err
}

const acct = "11111111-1111-1111-1111-111111111111"

// lookups: pid 1 is a Wii U NEX account, 2 a 3DS one, 3 device-only, anything else unknown.
func lookup(_ context.Context, pid int64) (*store.NEXAccount, string, error) {
	switch pid {
	case 1:
		return &store.NEXAccount{PID: 1, Password: "pw1", DeviceType: "wiiu"}, acct, nil
	case 2:
		return &store.NEXAccount{PID: 2, Password: "pw2", DeviceType: "3ds"}, acct, nil
	case 3:
		return &store.NEXAccount{PID: 3, Password: "pw3", DeviceType: "3ds"}, "", nil
	}
	return nil, "", status.Error(codes.InvalidArgument, "No NEX account found")
}

func TestGetNEXPasswordMarksTheAccountOnline(t *testing.T) {
	orig := goAsync
	goAsync = func(f func()) { f() }
	t.Cleanup(func() { goAsync = orig })

	core := &fakeCore{}
	s := &Server{core: core, nexLookup: lookup}
	ctx := context.Background()
	for pid, want := range map[uint32]string{1: "pw1", 2: "pw2", 3: "pw3"} {
		res, err := s.GetNEXPassword(ctx, &pb.GetNEXPasswordRequest{Pid: pid})
		if err != nil || res.GetPassword() != want {
			t.Fatalf("pid %d: %v %v", pid, res, err)
		}
	}
	if _, err := s.GetNEXPassword(ctx, &pb.GetNEXPasswordRequest{Pid: 9}); err == nil {
		t.Fatal("unknown pid answered")
	}
	// Wii U and 3DS each marked under their own namespace; the device-only and unknown pids not at all.
	if len(core.players) != 2 {
		t.Fatalf("marked %v", core.players)
	}
	got := map[string]bool{}
	for _, p := range core.players {
		if p.GetAccountId() != acct || p.GetTitleId() != "" {
			t.Errorf("sent %v", p)
		}
		got[p.GetNamespace()] = true
	}
	if !got["wiiu"] || !got["3ds"] {
		t.Fatalf("namespaces %v", got)
	}

	// A failing core does not touch the answer.
	core.err = errors.New("down")
	if res, err := s.GetNEXPassword(ctx, &pb.GetNEXPasswordRequest{Pid: 1}); err != nil || res.GetPassword() != "pw1" {
		t.Fatalf("failing core changed the answer: %v %v", res, err)
	}
}

// A hung core never holds up the game server's login.
func TestGetNEXPasswordDoesNotWaitForTheCore(t *testing.T) {
	core := &fakeCore{block: make(chan struct{}), called: make(chan struct{})}
	s := &Server{core: core, nexLookup: lookup}
	start := time.Now()
	if _, err := s.GetNEXPassword(context.Background(), &pb.GetNEXPasswordRequest{Pid: 1}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("GetNEXPassword blocked for %v", d)
	}
	select {
	case <-core.called:
	case <-time.After(2 * time.Second):
		t.Fatal("core never called")
	}
	close(core.block)
}
