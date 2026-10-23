package seahorse

import (
	"context"
	"fmt"
)

type historyScopeKey struct{}

// WithHistoryScope binds an application-created opaque execution-view nonce.
// It carries no authority and is never decoded from model tool arguments.
func WithHistoryScope(ctx context.Context, scope string) (context.Context, error) {
	if len(scope) != 32 {
		return nil, fmt.Errorf("seahorse: invalid history scope")
	}
	for _, r := range scope {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return nil, fmt.Errorf("seahorse: invalid history scope")
		}
	}
	return context.WithValue(ctx, historyScopeKey{}, scope), nil
}
func ScopedSession(ctx context.Context, base string) string {
	scope, _ := ctx.Value(historyScopeKey{}).(string)
	if scope == "" {
		return base
	}
	return "execution-view-" + digest([]string{base, scope})
}
