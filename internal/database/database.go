package database

import "context"

type ProvisionOptions struct {
	AppName          string
	DatabasePassword string
	DatabaseName     string
	DatabaseUser     string
	DatabaseHostname string
	Schema           string
}

func (po *ProvisionOptions) Validate() error {
	return nil
}

type Database interface {
	Connect(ctx context.Context) error
	Test(ctx context.Context) error
	Provision(ctx context.Context, provisionOptions ProvisionOptions) error
	Delete(ctx context.Context, databaseName, userName string) error
	TestAppConnection(ctx context.Context, credentials map[string]string) error
	Debug()
}
