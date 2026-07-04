package infisical

import (
	"context"
	"fmt"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
)

// NewClient constructs an authenticated Infisical client. The context ties the
// client's background token refresh to the caller's lifecycle.
func NewClient(ctx context.Context, cfg *config.Config) (infisical.InfisicalClientInterface, error) {
	client := infisical.NewInfisicalClient(ctx, infisical.Config{
		SiteUrl:              cfg.InfisicalSiteURL,
		AutoTokenRefresh:     true,
		CacheExpiryInSeconds: 0,
	})

	_, err := client.Auth().UniversalAuthLogin(cfg.InfisicalClientID, cfg.InfisicalClientSecret)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return client, nil
}
