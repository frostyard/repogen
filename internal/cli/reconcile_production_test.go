package cli

import (
	"context"
	"strings"
	"testing"
)

func TestReconcileProductionRequiresEveryExplicitBoundary(t *testing.T) {
	required := []string{
		"config",
		"config-sha256",
		"policy",
		"policy-sha256",
		"request-sha256",
		"provenance-sha256",
		"credentials-file",
		"signing-key",
	}
	for _, omitted := range required {
		t.Run(omitted, func(t *testing.T) {
			var arguments []string
			for _, name := range required {
				if name != omitted {
					arguments = append(arguments, "--"+name, "fixture")
				}
			}
			command := NewReconcileProductionCmd()
			command.SilenceUsage = true
			command.SilenceErrors = true
			command.SetArgs(arguments)
			err := command.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "--"+omitted+" must be provided explicitly") {
				t.Fatalf("omitting --%s returned %v", omitted, err)
			}
		})
	}
}
