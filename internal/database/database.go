package database

import "context"

// ProvisionOptions holds the provisioning options when user is trying to provision or create a new database.
type ProvisionOptions struct {
	AppName          string
	DatabasePassword string
	DatabaseName     string
	DatabaseUser     string
	DatabasePort     int
	DatabaseHostname string
	Schema           string
}

// Validate verifies if the provisioning options passed in to the database are correct
func (po *ProvisionOptions) Validate() error {
	return nil
}

// Database interface holds all the methods required for a database to support this application.
// All the new databases should be required to implement these.
type Database interface {
	Connect(ctx context.Context) error
	Test(ctx context.Context) error
	Provision(ctx context.Context, provisionOptions ProvisionOptions) error
	Delete(ctx context.Context, databaseName, userName string) error
	TestAppConnection(ctx context.Context, credentials map[string]string) error
}
