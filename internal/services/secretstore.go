package services

import (
	infisical "github.com/infisical/go-sdk"
	"github.com/pratiknkulkarni/databasemanager/internal/database"
)

// SecretStore is the narrow port over Infisical that the provisioning
// lifecycle needs. It is consumer-defined (idiomatic Go) so tests can fake it
// with a few lines; the SDK adapter below is the only production
// implementation. All methods are per-key/per-folder primitives — policy
// (fail-closed folder creation, sync ordering, rollback) stays in the
// Provisioner.
type SecretStore interface {
	// ListSecrets returns the key/value pairs stored at secretPath. A path
	// with no secrets yields an empty map, not an error.
	ListSecrets(environment, secretPath string) (map[string]string, error)
	CreateSecret(environment, secretPath string, kv database.SecretKV) error
	UpdateSecret(environment, secretPath string, kv database.SecretKV) error
	DeleteSecret(environment, secretPath, key string) error

	CreateFolder(environment, name string) error
	FolderExists(environment, name string) (bool, error)
	DeleteFolder(environment, name string) error
}

// sdkSecretStore adapts the Infisical SDK client to the SecretStore port.
// The project ID is fixed at construction: it is a property of the configured
// client, not of individual operations.
type sdkSecretStore struct {
	client    infisical.InfisicalClientInterface
	projectID string
}

// NewSecretStore wraps an Infisical client. A nil client yields a nil
// SecretStore (not a typed-nil interface), preserving the "Secrets may be
// nil, provisioning proceeds offline" contract of ProvisionerDeps.
func NewSecretStore(client infisical.InfisicalClientInterface, projectID string) SecretStore {
	if client == nil {
		return nil
	}
	return &sdkSecretStore{client: client, projectID: projectID}
}

func (s *sdkSecretStore) ListSecrets(environment, secretPath string) (map[string]string, error) {
	secrets, err := s.client.Secrets().List(infisical.ListSecretsOptions{
		ProjectID:   s.projectID,
		Environment: environment,
		SecretPath:  secretPath,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		m[secret.SecretKey] = secret.SecretValue
	}
	return m, nil
}

func (s *sdkSecretStore) CreateSecret(environment, secretPath string, kv database.SecretKV) error {
	_, err := s.client.Secrets().Create(infisical.CreateSecretOptions{
		ProjectID:   s.projectID,
		Environment: environment,
		SecretPath:  secretPath,
		SecretKey:   kv.Key,
		SecretValue: kv.Value,
	})
	return err
}

func (s *sdkSecretStore) UpdateSecret(environment, secretPath string, kv database.SecretKV) error {
	_, err := s.client.Secrets().Update(infisical.UpdateSecretOptions{
		ProjectID:      s.projectID,
		Environment:    environment,
		SecretPath:     secretPath,
		SecretKey:      kv.Key,
		NewSecretValue: kv.Value,
	})
	return err
}

func (s *sdkSecretStore) DeleteSecret(environment, secretPath, key string) error {
	_, err := s.client.Secrets().Delete(infisical.DeleteSecretOptions{
		ProjectID:   s.projectID,
		Environment: environment,
		SecretPath:  secretPath,
		SecretKey:   key,
	})
	return err
}

func (s *sdkSecretStore) CreateFolder(environment, name string) error {
	_, err := s.client.Folders().Create(infisical.CreateFolderOptions{
		ProjectID:   s.projectID,
		Environment: environment,
		Name:        name,
	})
	return err
}

func (s *sdkSecretStore) FolderExists(environment, name string) (bool, error) {
	folders, err := s.client.Folders().List(infisical.ListFoldersOptions{
		ProjectID:   s.projectID,
		Environment: environment,
	})
	if err != nil {
		return false, err
	}
	for _, folder := range folders {
		if folder.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (s *sdkSecretStore) DeleteFolder(environment, name string) error {
	_, err := s.client.Folders().Delete(infisical.DeleteFolderOptions{
		ProjectID:   s.projectID,
		Environment: environment,
		FolderName:  name,
		Path:        "/",
	})
	return err
}
