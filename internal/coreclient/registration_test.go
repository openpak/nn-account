package coreclient

import (
	"context"
	"google.golang.org/grpc"
	accountv1 "openpak/nn-account/internal/accountpb"
	"testing"
)

type registrationCapture struct {
	accountv1.RegistrationClient
	request *accountv1.RegisterAccountRequest
}

func (r *registrationCapture) RegisterAccount(_ context.Context, req *accountv1.RegisterAccountRequest, _ ...grpc.CallOption) (*accountv1.RegisterAccountResponse, error) {
	r.request = req
	return &accountv1.RegisterAccountResponse{AccountId: "new-account"}, nil
}
func TestRegistrationKeepsHandleAndDisplayNameSeparate(t *testing.T) {
	reg := &registrationCapture{}
	c := &Client{reg: reg}
	for _, display := range []string{"", "Dino Hansolo"} {
		if _, err := c.RegisterAccount(context.Background(), "wiiu", "test@example.invalid", "password", "hansolo", display, "IE", "en"); err != nil {
			t.Fatal(err)
		}
		if reg.request.Handle != "hansolo" || reg.request.DisplayName != display {
			t.Fatalf("identity fields conflated: %+v", reg.request)
		}
	}
}
