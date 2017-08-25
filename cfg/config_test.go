package cfg

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestNewConfig(t *testing.T) {
	cmd := cobra.Command{}

	config, err := NewConfig(&cmd)
	if err != nil {
		t.Errorf("Error creating config: %v", err)
	}

	if config.IsDryRun() {
		t.Errorf("Dry-run: expected false, got true")
	}
}
