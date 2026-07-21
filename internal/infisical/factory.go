package infisical

import (
	"context"
	"fmt"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
)

// NewClient constructs an authenticated Infisical client. The context ties the
// client's background token refresh to the caller's lifecycle.
//
// Two machine-identity auth methods are supported, selected by which
// credentials the configuration carries:
//
//   - Token Auth: a pre-issued access token used directly as a bearer token.
//     No login round-trip happens, so a bad token surfaces on first use rather
//     than here.
//   - Universal Auth: a client ID and secret exchanged for a short-lived
//     token, refreshed automatically for the life of the context.
//
// The identity must also be a member of the configured project. Org-level
// access is not sufficient; without a project membership every secret call
// fails with 403 even though authentication succeeded.
func NewClient(ctx context.Context, cfg *config.Config) (infisical.InfisicalClientInterface, error) {
	client := infisical.NewInfisicalClient(ctx, infisical.Config{
		SiteUrl:              cfg.InfisicalSiteURL,
		AutoTokenRefresh:     true,
		CacheExpiryInSeconds: 0,
	})

	if cfg.InfisicalAccessToken != "" {
		client.Auth().SetAccessToken(cfg.InfisicalAccessToken)
		return client, nil
	}

	if _, err := client.Auth().UniversalAuthLogin(cfg.InfisicalClientID, cfg.InfisicalClientSecret); err != nil {
		return nil, fmt.Errorf("universal auth login failed for client ID %s: %w", cfg.InfisicalClientID, err)
	}

	return client, nil
}
