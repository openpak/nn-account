// Package coreclient is the adapter's client for the account core's internal
// APIs. The adapter never reads core tables directly (PRD §7).
package coreclient

import (
	"context"
	"errors"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	accountv1 "openpak/nn-account/internal/accountpb"
)

type Client struct {
	conn   *grpc.ClientConn
	key    string
	ident  accountv1.IdentityClient
	links  accountv1.LinksClient
	events accountv1.EventsClient
	reg    accountv1.RegistrationClient
	sess   accountv1.SessionsClient
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
		sess:   accountv1.NewSessionsClient(conn),
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

// EnsureLink makes sure the subject has an active link in exactly this
// namespace, creating one for the account if not. GetActiveLink falls back to
// the other family, so it cannot answer this: an account whose NNID was made on
// a Wii U has a "wiiu" link, and signing that identity into Azahar must add the
// "3ds" one too, or the website and the app never see its 3DS friend code.
func (c *Client) EnsureLink(ctx context.Context, namespace, subjectID, accountID string) error {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	link, err := c.links.GetLinkBySubject(rctx, &accountv1.GetLinkBySubjectRequest{
		Namespace: namespace, SubjectId: subjectID,
	})
	if err == nil && link.State == accountv1.LinkState_LINK_STATE_ACTIVE {
		return nil
	}
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	return c.ReserveAndActivateLink(ctx, namespace, subjectID, accountID)
}

// PublishNNID puts the account's Nintendo Network ID on its Wii U link as the
// link's public code (account docs/public-codes.md), so the website and the
// app can show it and people can add each other by it. A link that already
// carries it is left alone, which makes this safe to repeat. The core keys
// lookups on the name's letters and digits, lower-cased, so "Leia.B" and
// "leiab" resolve alike; two names that differ only in punctuation collide
// and the second is refused with AlreadyExists.
func (c *Client) PublishNNID(ctx context.Context, pid int64, username string) error {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	link, err := c.links.GetLinkBySubject(rctx, &accountv1.GetLinkBySubjectRequest{
		Namespace: "wiiu", SubjectId: strconv.FormatInt(pid, 10),
	})
	if err != nil {
		return err
	}
	if link.State != accountv1.LinkState_LINK_STATE_ACTIVE || link.PublicCode == username {
		return nil
	}
	_, err = c.links.SetLinkPublicCode(rctx, &accountv1.SetLinkPublicCodeRequest{
		LinkId: link.LinkId, PublicCode: username,
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

// MarkOnline tells the core these players were just confirmed online by a
// game server, which is where their playtime starts. At most 1000 per call.
func (c *Client) MarkOnline(ctx context.Context, players []*accountv1.OnlinePlayer) (int32, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	resp, err := c.sess.MarkOnline(rctx, &accountv1.MarkOnlineRequest{Players: players})
	if err != nil {
		return 0, err
	}
	return resp.GetMarked(), nil
}
