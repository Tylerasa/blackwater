package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/Tylerasa/blackwater/internal/corpus"
	"github.com/Tylerasa/blackwater/internal/fingerprint"
	"github.com/Tylerasa/blackwater/internal/spec"
	"github.com/spf13/cobra"
)

func newParseCmd() *cobra.Command {
	var (
		input        string
		templatesArg string
		allowlist    []string
	)
	cmd := &cobra.Command{
		Use:   "parse",
		Short: "Run cached specs against a dump and print matches / unmatched",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runParse(cmd.OutOrStdout(), input, templatesArg, allowlist)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "path to SMS dump")
	cmd.Flags().StringVar(&templatesArg, "templates", "templates/templates.json", "path to templates cache")
	cmd.Flags().StringSliceVar(&allowlist, "allowlist", nil, "override sender allowlist")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func runParse(w io.Writer, input, templatesPath string, allowlist []string) error {
	store, err := spec.Load(templatesPath)
	if err != nil {
		return err
	}
	if store.Len() == 0 {
		fmt.Fprintln(w, "note: templates cache is empty — every message will be unmatched.")
	}
	it, _, err := corpus.Open(input, allowlist)
	if err != nil {
		return err
	}

	matched := 0
	total := 0
	unmatched := map[string]int{} // fingerprint -> count
	unmatchedSkel := map[string]string{}

	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		total++
		h, sk := fingerprint.Fingerprint(r.Sender, r.Body)
		s, has := store.Get(h)
		if !has {
			unmatched[h]++
			unmatchedSkel[h] = sk
			continue
		}
		fields, err := spec.Execute(s, r.Body)
		if err != nil {
			fmt.Fprintf(w, "match-error fp=%s: %v (skeleton=%s)\n", h[:10], err, sk)
			continue
		}
		matched++
		printMatch(w, h, s, fields)
	}
	if err := it.Err(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "matched   : %d/%d\n", matched, total)
	fmt.Fprintf(w, "unmatched : %d fingerprints across %d messages\n", len(unmatched), total-matched)
	if len(unmatched) > 0 {
		// sort unmatched by count desc
		type kv struct {
			h string
			c int
		}
		xs := make([]kv, 0, len(unmatched))
		for h, c := range unmatched {
			xs = append(xs, kv{h, c})
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].c > xs[j].c })
		fmt.Fprintln(w, "\nunmatched fingerprints (top by frequency):")
		for _, x := range xs {
			sk := unmatchedSkel[x.h]
			if len(sk) > 120 {
				sk = sk[:117] + "..."
			}
			fmt.Fprintf(w, "  %dx  %s  %s\n", x.c, x.h[:10], sk)
		}
	}
	return nil
}

func printMatch(w io.Writer, h string, s spec.Spec, fields map[string]string) {
	// stable key order for readability
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(w, "match  fp=%s  dir=%s", h[:10], s.Direction)
	for _, k := range keys {
		fmt.Fprintf(w, "  %s=%q", k, fields[k])
	}
	fmt.Fprintln(w)
}
