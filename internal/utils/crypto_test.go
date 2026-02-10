package utils

import (
	"encoding/hex"
	"testing"
)

func TestGenerateRandomPassword(t *testing.T) {
	tests := []struct {
		name    string
		length  int
		wantErr bool
	}{
		{"Standard Length", 16, false},
		{"Short Length", 4, false},
		{"Long Length", 64, false},
		{"Zero Length", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateRandomPassword(tt.length)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateRandomPassword() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			expectedLen := tt.length * 2
			if len(got) != expectedLen {
				t.Errorf("GenerateRandomPassword() length = %d, want %d", len(got), expectedLen)
			}

			if _, err := hex.DecodeString(got); err != nil {
				t.Errorf("GenerateRandomPassword() returned invalid hex: %v", err)
			}
		})
	}
}
