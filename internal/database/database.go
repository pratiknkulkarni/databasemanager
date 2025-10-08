package database

import "context"

type ProvisionOptions struct {
	AppName     string
	AppPassword string
	Database    string
	User        string
	Schema      string
}

type Database interface {
	Connect(ctx context.Context) error
	Test(ctx context.Context) error
	Provision(ctx context.Context, provisionOptions ProvisionOptions) error
}
