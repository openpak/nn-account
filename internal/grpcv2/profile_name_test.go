package grpcv2

import (
	"context"
	"errors"
	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	"openpak/nn-account/internal/store"
	"testing"
)

type profileCore struct {
	CorePort
	account *accountv1.GetAccountResponse
	err     error
}

func (c *profileCore) GetAccount(context.Context, string) (*accountv1.GetAccountResponse, error) {
	return c.account, c.err
}

func TestProfileNameFollowsCoreWithoutChangingIdentityOrMiiData(t *testing.T) {
	core := &profileCore{account: &accountv1.GetAccountResponse{Status: accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE, DisplayName: "Dino Hansolo", Handle: "hansolo"}}
	server := &Server{core: core}
	p := &store.PNID{PID: 123, Username: "hansolo", MiiName: "Old", MiiData: "unchanged-binary"}
	for _, name := range []string{"Dino Hansolo", "漢字あいうえおかきくけこ", "Hansolo"} {
		core.account.DisplayName = name
		if err := server.checkCoreStatus(context.Background(), "account", p); err != nil {
			t.Fatal(err)
		}
		if p.MiiName != store.ProfileName(name) || p.Username != "hansolo" || p.PID != 123 || p.MiiData != "unchanged-binary" {
			t.Fatalf("wrong projection: %+v", p)
		}
	}
	core.err = errors.New("unavailable")
	if err := server.checkCoreStatus(context.Background(), "account", p); err == nil {
		t.Fatal("core outage must fail closed")
	}
	core.err = nil
	core.account.Status = accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING
	if err := server.checkCoreStatus(context.Background(), "account", p); err == nil {
		t.Fatal("pending deletion must fail closed")
	}
}
