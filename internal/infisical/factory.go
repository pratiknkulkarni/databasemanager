package infisical

import (
	"context"
	"fmt"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
)

func NewClient(cfg *config.Config) (infisical.InfisicalClientInterface, error) {
	client := infisical.NewInfisicalClient(context.Background(), infisical.Config{
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
