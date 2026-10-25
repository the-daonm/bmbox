package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// outputFormat is --output of the commands that report state.
var outputFormat string

func addOutputFlag(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "output format: table or json")
}

// jsonOutput reports whether --output asks for JSON, rejecting unknown
// formats.
func jsonOutput() (bool, error) {
	switch outputFormat {
	case "table", "":
		return false, nil
	case "json":
		return true, nil
	}
	return false, fmt.Errorf("--output: unknown format %q (want table or json)", outputFormat)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
