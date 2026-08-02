// Command ledger is the CLI entry point for milestone 1. Two subcommands:
//
//	ledger analyse --input dump.[xml|txt]
//	ledger parse   --input dump.[xml|txt]
//
// The goal of analyse is diagnostic: it tells you whether the
// fingerprint-then-cache approach is holding on your dump. The goal of
// parse is to run every known Spec against the corpus and dump matches.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "ledger",
		Short:         "Parse Ghanaian MoMo / bank SMS into a ledger",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newAnalyseCmd(), newParseCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
