// Package coreclient is the adapter's client for the account core's internal
// APIs. The adapter never reads core tables directly (PRD §7).
package coreclient

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	accountv1 "openpak/account/proto/openpak/account/v1"
)

type Client struct {
	conn   *grpc.ClientConn
	key    string
	ident  accountv1.IdentityClient
	links  accountv1.LinksClient
	events accountv1.EventsClient
	reg    accountv1.RegistrationClient
}

func Dial(ctx context.Context, addr, internalKey string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, key: internalKey,
		ident:  accountv1.NewIdentityClient(conn),
		links:  accountv1.NewLinksClient(conn),
		events: accountv1.NewEventsClient(conn),
		reg:    accountv1.NewRegistrationClient(conn),
	}
	return c, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) ctx(parent context.Context) context.Context {
	return metadata.AppendToOutgoingContext(parent, "authorization", "Bearer "+c.key)
}

// VerifyConsolePassword forwards a console-submitted password to the core's
// dedicated verification operation (PRD §7). The adapter never stores or
// verifies console passwords itself.
func (c *Client) VerifyConsolePassword(ctx context.Context, accountID, password, purpose string) (*accountv1.VerifyPasswordResponse, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	return c.ident.VerifyPasswordByID(rctx, &accountv1.VerifyPasswordByIDRequest{
		CallerNamespace: "wiiu", Purpose: purpose, AccountId: accountID, Password: password,
	})
}

// GetAccount returns core status for an account. Never includes secrets.
func (c *Client) GetAccount(ctx context.Context, accountID string) (*accountv1.GetAccountResponse, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	return c.ident.GetAccount(rctx, &accountv1.GetAccountRequest{AccountId: accountID})
}

// GetActiveLink returns the core link for this adapter's subject, requiring
// state ACTIVE. Fail-closed on any error. Probes both adapter namespaces:
// accounts register under "wiiu" via NNAS but may sign in from a 3DS.
func (c *Client) GetActiveLink(ctx context.Context, namespace, subjectID string) (*accountv1.GetLinkBySubjectResponse, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	resp, err := c.links.GetLinkBySubject(rctx, &accountv1.GetLinkBySubjectRequest{
		Namespace: namespace, SubjectId: subjectID,
	})
	if err == nil {
		if resp.State != accountv1.LinkState_LINK_STATE_ACTIVE {
			return nil, errors.New("link not active")
		}
		return resp, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, err
	}
	other := "wiiu"
	if namespace == "wiiu" {
		other = "3ds"
	}
	resp, err = c.links.GetLinkBySubject(rctx, &accountv1.GetLinkBySubjectRequest{
		Namespace: other, SubjectId: subjectID,
	})
	if err != nil {
		return nil, err
	}
	if resp.State != accountv1.LinkState_LINK_STATE_ACTIVE {
		return nil, errors.New("link not active")
	}
	return resp, nil
}

// UnlinkBySubject revokes the link for a subject (registration
// compensation; PRD §7 recovery).
func (c *Client) UnlinkBySubject(ctx context.Context, namespace, subjectID string) error {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	resp, err := c.links.GetLinkBySubject(rctx, &accountv1.GetLinkBySubjectRequest{
		Namespace: namespace, SubjectId: subjectID,
	})
	if err != nil {
		return err
	}
	_, err = c.links.UnlinkLink(rctx, &accountv1.UnlinkLinkRequest{
		LinkId: resp.GetLinkId(), IdempotencyKey: "compensation-" + subjectID,
	})
	return err
}

// ReserveAndActivateLink registers a newly created adapter subject with the
// core's link lifecycle (state pending → active).
func (c *Client) ReserveAndActivateLink(ctx context.Context, namespace, subjectID, accountID string) error {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	res, err := c.links.ReserveLink(rctx, &accountv1.ReserveLinkRequest{
		AccountId: accountID, Namespace: namespace, SubjectId: subjectID,
		IdempotencyKey: "register-" + subjectID,
	})
	if err != nil {
		return err
	}
	_, err = c.links.ActivateLink(rctx, &accountv1.ActivateLinkRequest{
		LinkId: res.LinkId, IdempotencyKey: "register-" + subjectID,
	})
	return err
}

// PollEvents pages through core invalidation events.
func (c *Client) PollEvents(ctx context.Context, sinceVersion uint64) (*accountv1.PollEventsResponse, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 10*time.Second)
	defer cancel()
	return c.events.PollEvents(rctx, &accountv1.PollEventsRequest{SinceVersion: sinceVersion, Limit: 1000})
}

// RegisterAccount delegates account creation to the core (console
// registration; PRD FR-1).
func (c *Client) RegisterAccount(ctx context.Context, namespace, email, password, displayName, country, language string) (*accountv1.RegisterAccountResponse, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 15*time.Second)
	defer cancel()
	return c.reg.RegisterAccount(rctx, &accountv1.RegisterAccountRequest{
		CallerNamespace: namespace, Purpose: "console_registration",
		Email: email, Password: password, DisplayName: displayName,
		Country: country, Language: language,
	})
}

// SetAdapterCredential stores the console-transformed secret in the core's
// adapter credential domain (namespace-scoped).
func (c *Client) SetAdapterCredential(ctx context.Context, namespace, accountID, secret string) error {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	_, err := c.ident.SetAdapterCredential(rctx, &accountv1.SetAdapterCredentialRequest{
		CallerNamespace: namespace, Purpose: "console_credential", AccountId: accountID, Secret: secret,
	})
	return err
}

// VerifyAdapterCredential checks a console-transformed secret. Fail-closed on
// core unavailability (PRD §7).
func (c *Client) VerifyAdapterCredential(ctx context.Context, namespace, accountID, secret string) (*accountv1.VerifyAdapterCredentialResponse, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	return c.ident.VerifyAdapterCredential(rctx, &accountv1.VerifyAdapterCredentialRequest{
		CallerNamespace: namespace, Purpose: "console_login", AccountId: accountID, Secret: secret,
	})
}

// RequestAccountDeletion delegates FR-8 stage one to the core.
func (c *Client) RequestAccountDeletion(ctx context.Context, accountID string) error {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	_, err := c.ident.RequestAccountDeletion(rctx, &accountv1.RequestAccountDeletionRequest{
		CallerNamespace: "wiiu", Purpose: "console_deletion", AccountId: accountID,
	})
	return err
}
