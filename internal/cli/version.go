package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/frostyard/repogen/internal/buildinfo"
	"github.com/spf13/cobra"
)

type versionOutput struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func NewVersionCmd() *cobra.Command {
	var short bool
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the embedded release version and commit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeVersion(cmd.OutOrStdout(), short, outputJSON)
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "Print machine-readable version and commit fields")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Print version identity as JSON")
	return cmd
}

func writeVersion(output io.Writer, short, outputJSON bool) error {
	if short && outputJSON {
		return fmt.Errorf("--short and --json cannot be used together")
	}
	identity := versionOutput{Version: buildinfo.Version, Commit: buildinfo.Commit}
	if outputJSON {
		encoder := json.NewEncoder(output)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(identity)
	}
	if short {
		_, err := fmt.Fprintf(output, "%s %s\n", identity.Version, identity.Commit)
		return err
	}
	_, err := fmt.Fprintf(output, "repogen %s (commit %s)\n", identity.Version, identity.Commit)
	return err
}
