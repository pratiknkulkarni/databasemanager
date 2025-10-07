package database

import "context"

type Database interface {
	Connect(ctx context.Context) error
	Test(ctx context.Context) error
	Provision(ctx context.Context, appName, appPassword string) error
}
