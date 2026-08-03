// Command ledger is the CLI entry point.
//
//	ledger analyse  --input dump.[xml|txt]   # group by fingerprint, print coverage
//	ledger parse    --input dump.[xml|txt]   # apply cached specs, list matches
//	ledger generate --input dump.[xml|txt]   # LLM: create specs for unseen fingerprints
//
// analyse and parse are pure, offline, and read-only. generate is the only
// command that touches the network — it needs ANTHROPIC_API_KEY.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

func main() {
	// Auto-load .env from the working directory. Real env vars already set
	// in the shell take precedence (godotenv.Load skips keys that exist).
	// A missing file is normal — CI, prod, and users who source manually
	// don't need one — so we only surface unexpected read errors.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "warning: .env present but unreadable:", err)
	}

	root := &cobra.Command{
		Use:           "ledger",
		Short:         "Parse Ghanaian MoMo / bank SMS into a ledger",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newAnalyseCmd(),
		newParseCmd(),
		newGenerateCmd(),
		newExportCmd(),
		newIngestCmd(),
		newSumCmd(),
	)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
