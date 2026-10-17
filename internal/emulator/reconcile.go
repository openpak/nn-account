package emulator

import (
	"context"
	"log"
	"time"
)

// Namespaces every active core account must be linked in, used or not.
var provisionNamespaces = []string{"wiiu", "3ds"}

// Reconcile gives every active core account an identity and an active link in
// both families, at startup and then every interval. Ensure is idempotent, so
// a pass over accounts that are already provisioned changes nothing.
func (s *Server) Reconcile(ctx context.Context, interval time.Duration) {
	for {
		reconcileOnce(ctx, s.core.AccountPage, s.Ensure)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func reconcileOnce(ctx context.Context,
	page func(context.Context, string) ([]string, string, error),
	ensure func(context.Context, string, string) (*Identity, error)) (accounts, failed int) {
	after := ""
	for ctx.Err() == nil {
		ids, next, err := page(ctx, after)
		if err != nil {
			log.Printf("provision: list accounts: %v", err)
			failed++
			break
		}
		for _, id := range ids {
			accounts++
			for _, ns := range provisionNamespaces {
				if _, err := ensure(ctx, ns, id); err != nil && err != errNoSuchAccount {
					failed++
				}
			}
		}
		if next == "" || len(ids) == 0 {
			break
		}
		after = next
	}
	log.Printf("provision: %d accounts, %d failures", accounts, failed)
	return
}
