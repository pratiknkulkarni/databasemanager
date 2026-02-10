package config

import (
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	type fields struct {
		Postgres              DatabaseConfig
		MySQL                 DatabaseConfig
		InfisicalProjectID    string
		InfisicalClientID     string
		InfisicalClientSecret string
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "Valid Config (Postgres only)",
			fields: fields{
				InfisicalProjectID:    "proj_123",
				InfisicalClientID:     "client_123",
				InfisicalClientSecret: "secret_123",
				Postgres: DatabaseConfig{
					DatabaseHostname: "localhost",
					DatabasePort:     5432,
					DatabaseUser:     "admin",
					DatabasePassword: "password",
					DatabaseName:     "postgres",
				},
			},
			wantErr: false,
		},
		{
			name: "Missing Infisical Details",
			fields: fields{
				InfisicalProjectID: "",
			},
			wantErr: true,
		},
		{
			name: "Incomplete Database Config",
			fields: fields{
				InfisicalProjectID:    "proj_123",
				InfisicalClientID:     "client_123",
				InfisicalClientSecret: "secret_123",
				Postgres: DatabaseConfig{
					DatabaseHostname: "localhost",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{
				Postgres:              tt.fields.Postgres,
				MySQL:                 tt.fields.MySQL,
				InfisicalProjectID:    tt.fields.InfisicalProjectID,
				InfisicalClientID:     tt.fields.InfisicalClientID,
				InfisicalClientSecret: tt.fields.InfisicalClientSecret,
			}
			if err := c.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
