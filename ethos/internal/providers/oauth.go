package providers

import (
	"github.com/Sahaj-Tech-ltd/ethos/internal/auth"
)

// ResolveOAuthAPIKey looks up a stored OAuth token for the given provider and
// returns its access token in a form usable as an API key (Bearer header is
// injected by the provider client).
//
// Returns "" if no token is stored or the file is unreadable. The factory
// uses this when ProviderConfig.AuthType == "oauth" and APIKey is empty.
func ResolveOAuthAPIKey(provider string) string {
	tok, err := auth.LoadToken(provider)
	if err != nil || tok == nil {
		return ""
	}
	return tok.AccessToken
}
