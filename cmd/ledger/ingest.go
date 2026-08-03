package main

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/Tylerasa/blackwater/internal/corpus"
	"github.com/Tylerasa/blackwater/internal/fingerprint"
	"github.com/Tylerasa/blackwater/internal/normalize"
	"github.com/Tylerasa/blackwater/internal/spec"
	"github.com/Tylerasa/blackwater/internal/store"
	"github.com/spf13/cobra"
)

func newIngestCmd() *cobra.Command {
	var (
		input     string
		templates string
		allowlist []string
		dbPath    string
	)
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Parse a dump and insert transactions into the SQLite store",
		Long: `ingest is the pipeline for putting messages into your personal ledger DB:
walk the dump, apply cached specs, normalise each match, and INSERT OR IGNORE
into the store. Re-running on the same dump is safe — the (sender, body) hash
key means duplicates are skipped, not appended.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIngest(cmd.Context(), cmd.OutOrStdout(), ingestOpts{
				input: input, templates: templates, allowlist: allowlist, dbPath: dbPath,
			})
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "path to SMS dump")
	cmd.Flags().StringVar(&templates, "templates", "templates/templates.json", "path to templates cache")
	cmd.Flags().StringSliceVar(&allowlist, "allowlist", nil, "override sender allowlist")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to SQLite ledger DB (will be created)")
	_ = cmd.MarkFlagRequired("input")
	_ = cmd.MarkFlagRequired("db")
	return cmd
}

type ingestOpts struct {
	input     string
	templates string
	allowlist []string
	dbPath    string
}

func runIngest(ctx context.Context, w io.Writer, o ingestOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
	specStore, err := spec.Load(o.templates)
	if err != nil {
		return err
	}
	if specStore.Len() == 0 {
		return fmt.Errorf("no cached specs; run `ledger generate --input %s` first", o.input)
	}

	db, err := store.Open(o.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	it, _, err := corpus.Open(o.input, o.allowlist)
	if err != nil {
		return err
	}

	var (
		total, matched, inserted, skipped int
		normErrors                        []string
		unmatched                         = map[string]int{}
	)
	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		total++
		h, _ := fingerprint.Fingerprint(r.Sender, r.Body)
		sp, has := specStore.Get(h)
		if !has {
			unmatched[h]++
			continue
		}
		fields, err := spec.Execute(sp, r.Body)
		if err != nil {
			normErrors = append(normErrors, fmt.Sprintf("exec fp=%s: %v", h[:10], err))
			continue
		}
		matched++
		tx, err := normalize.Normalize(sp, fields, r.Date)
		if err != nil {
			normErrors = append(normErrors, fmt.Sprintf("norm fp=%s: %v", h[:10], err))
			continue
		}
		res, err := db.Insert(ctx, store.MessageHash(r.Sender, r.Body), tx)
		if err != nil {
			return fmt.Errorf("insert (fp=%s): %w", h[:10], err)
		}
		if res.Inserted {
			inserted++
		} else {
			skipped++
		}
	}
	if err := it.Err(); err != nil {
		return err
	}

	fmt.Fprintf(w, "db             : %s\n", o.dbPath)
	fmt.Fprintf(w, "total messages : %d\n", total)
	fmt.Fprintf(w, "matched        : %d\n", matched)
	fmt.Fprintf(w, "inserted       : %d\n", inserted)
	fmt.Fprintf(w, "already stored : %d\n", skipped)
	fmt.Fprintf(w, "unmatched      : %d messages across %d fingerprints\n",
		sumUnmatched(unmatched), len(unmatched))
	if len(normErrors) > 0 {
		fmt.Fprintf(w, "normalize errs : %d\n", len(normErrors))
		for _, e := range normErrors {
			fmt.Fprintln(w, "  ", e)
		}
	}
	if n, err := db.Count(ctx); err == nil {
		fmt.Fprintf(w, "total in db    : %d\n", n)
	}
	if len(unmatched) > 0 {
		fmt.Fprintln(w, "unmatched fingerprints (top by frequency):")
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
			fmt.Fprintf(w, "  %dx %s\n", x.c, x.h[:10])
		}
	}
	return nil
}

func sumUnmatched(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
