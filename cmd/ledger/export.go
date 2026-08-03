package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/Tylerasa/blackwater/internal/corpus"
	"github.com/Tylerasa/blackwater/internal/fingerprint"
	"github.com/Tylerasa/blackwater/internal/normalize"
	"github.com/Tylerasa/blackwater/internal/spec"
	"github.com/spf13/cobra"
)

func newExportCmd() *cobra.Command {
	var (
		input     string
		templates string
		allowlist []string
		format    string
		output    string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export matched messages as a normalised ledger",
		Long: `export walks the dump, applies cached specs, normalises matches into a
canonical Transaction shape (money in pesewas + display GHS), and writes
the result as CSV.

Unmatched messages are skipped and summarised at the end so you can decide
whether to generate more specs first.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(cmd.OutOrStderr(), exportOpts{
				input:     input,
				templates: templates,
				allowlist: allowlist,
				format:    format,
				output:    output,
			})
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "path to SMS dump")
	cmd.Flags().StringVar(&templates, "templates", "templates/templates.json", "path to templates cache")
	cmd.Flags().StringSliceVar(&allowlist, "allowlist", nil, "override sender allowlist")
	cmd.Flags().StringVar(&format, "format", "csv", "output format (csv only for now)")
	cmd.Flags().StringVar(&output, "output", "-", "output file path; - for stdout")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

type exportOpts struct {
	input     string
	templates string
	allowlist []string
	format    string
	output    string
}

func runExport(logw io.Writer, o exportOpts) error {
	if o.format != "csv" {
		return fmt.Errorf("unsupported format %q (only csv for now)", o.format)
	}
	store, err := spec.Load(o.templates)
	if err != nil {
		return err
	}
	if store.Len() == 0 {
		return fmt.Errorf("no cached specs; run `ledger generate --input %s` first", o.input)
	}
	it, _, err := corpus.Open(o.input, o.allowlist)
	if err != nil {
		return err
	}

	// Where to write CSV: stdout or file. Log messages go to logw (stderr
	// when writing to stdout, stdout otherwise) so redirecting is clean.
	var w io.Writer
	if o.output == "-" || o.output == "" {
		w = os.Stdout
	} else {
		f, err := os.Create(o.output)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{
		"timestamp", "direction", "amount_pesewas", "amount_ghs", "currency",
		"counterparty", "reference",
		"balance_pesewas", "balance_ghs",
		"fee_pesewas", "fee_ghs",
		"tax_pesewas", "tax_ghs",
		"fingerprint", "sender",
	}); err != nil {
		return err
	}

	var (
		total, matched, exported int
		normalizeErrors          []string
		unmatched                = map[string]int{}
	)
	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		total++
		h, _ := fingerprint.Fingerprint(r.Sender, r.Body)
		s, has := store.Get(h)
		if !has {
			unmatched[h]++
			continue
		}
		fields, err := spec.Execute(s, r.Body)
		if err != nil {
			normalizeErrors = append(normalizeErrors, fmt.Sprintf("exec fp=%s: %v", h[:10], err))
			continue
		}
		matched++
		tx, err := normalize.Normalize(s, fields, r.Date)
		if err != nil {
			normalizeErrors = append(normalizeErrors, fmt.Sprintf("norm fp=%s: %v", h[:10], err))
			continue
		}
		if err := writeRow(cw, tx); err != nil {
			return err
		}
		exported++
	}
	if err := it.Err(); err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}

	// Log summary to stderr so it doesn't pollute the CSV on stdout.
	fmt.Fprintln(logw)
	fmt.Fprintf(logw, "total messages : %d\n", total)
	fmt.Fprintf(logw, "matched        : %d\n", matched)
	fmt.Fprintf(logw, "exported rows  : %d\n", exported)
	fmt.Fprintf(logw, "unmatched      : %d messages across %d fingerprints\n",
		sumMap(unmatched), len(unmatched))
	if len(normalizeErrors) > 0 {
		fmt.Fprintf(logw, "normalize errors: %d\n", len(normalizeErrors))
		for _, e := range normalizeErrors {
			fmt.Fprintln(logw, "  ", e)
		}
	}
	if len(unmatched) > 0 {
		fmt.Fprintln(logw, "unmatched fingerprints (top by frequency):")
		type kv struct{ h string; c int }
		xs := make([]kv, 0, len(unmatched))
		for h, c := range unmatched {
			xs = append(xs, kv{h, c})
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].c > xs[j].c })
		for i, x := range xs {
			if i >= 5 {
				break
			}
			fmt.Fprintf(logw, "  %dx %s\n", x.c, x.h[:10])
		}
	}
	return nil
}

func writeRow(cw *csv.Writer, tx normalize.Transaction) error {
	ts := ""
	if !tx.Timestamp.IsZero() {
		ts = tx.Timestamp.UTC().Format("2006-01-02T15:04:05Z")
	}
	return cw.Write([]string{
		ts,
		string(tx.Direction),
		fmt.Sprintf("%d", tx.Amount),
		tx.AmountGHS(),
		tx.Currency,
		tx.Counterparty,
		tx.Reference,
		fmt.Sprintf("%d", tx.Balance),
		tx.BalanceGHS(),
		fmt.Sprintf("%d", tx.Fee),
		tx.FeeGHS(),
		fmt.Sprintf("%d", tx.Tax),
		tx.TaxGHS(),
		tx.Fingerprint,
		tx.Sender,
	})
}

func sumMap(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
