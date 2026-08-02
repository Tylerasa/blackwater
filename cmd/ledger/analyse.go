package main

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/Tylerasa/blackwater/internal/corpus"
	"github.com/Tylerasa/blackwater/internal/fingerprint"
	"github.com/Tylerasa/blackwater/internal/spec"
	"github.com/spf13/cobra"
)

func newAnalyseCmd() *cobra.Command {
	var (
		input      string
		templates  string
		allowlist  []string
		showAll    bool
		skelWidth  int
		minSamples int
	)
	cmd := &cobra.Command{
		Use:   "analyse",
		Short: "Group messages by fingerprint and report coverage",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAnalyse(cmd.OutOrStdout(), input, templates, allowlist, showAll, skelWidth, minSamples)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "path to SMS dump (XML or text)")
	cmd.Flags().StringVar(&templates, "templates", "templates/templates.json", "path to templates cache")
	cmd.Flags().StringSliceVar(&allowlist, "allowlist", nil, "override sender allowlist (comma-separated)")
	cmd.Flags().BoolVar(&showAll, "all", false, "show every fingerprint (default: skip singletons)")
	cmd.Flags().IntVar(&skelWidth, "skel-width", 100, "truncate skeleton to this many chars")
	cmd.Flags().IntVar(&minSamples, "min-samples", 1, "only show fingerprints with at least this many occurrences")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

// bucket accumulates counts and remembers one representative skeleton per fp.
type bucket struct {
	count    int
	sender   string
	skeleton string
	hasSpec  bool
}

func runAnalyse(w io.Writer, input, templatesPath string, allowlist []string, showAll bool, skelWidth, minSamples int) error {
	store, err := spec.Load(templatesPath)
	if err != nil {
		return err
	}

	it, format, err := corpus.Open(input, allowlist)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "input: %s (format=%s)\n", input, formatName(format))

	buckets := map[string]*bucket{}
	total := 0
	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		total++
		h, sk := fingerprint.Fingerprint(r.Sender, r.Body)
		b, ok := buckets[h]
		if !ok {
			_, has := store.Get(h)
			b = &bucket{sender: r.Sender, skeleton: sk, hasSpec: has}
			buckets[h] = b
		}
		b.count++
	}
	if err := it.Err(); err != nil {
		return err
	}

	// sort by count descending
	type row struct {
		hash string
		b    *bucket
	}
	rows := make([]row, 0, len(buckets))
	covered := 0
	for h, b := range buckets {
		rows = append(rows, row{h, b})
		if b.hasSpec {
			covered += b.count
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].b.count != rows[j].b.count {
			return rows[i].b.count > rows[j].b.count
		}
		return rows[i].hash < rows[j].hash
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COUNT\tSPEC\tSENDER\tHASH\tSKELETON")
	fmt.Fprintln(tw, "-----\t----\t------\t----\t--------")
	shown := 0
	for _, r := range rows {
		if r.b.count < minSamples {
			continue
		}
		if !showAll && r.b.count < 2 {
			// singletons are usually one-off marketing messages or
			// promo pushes — hide them unless --all is set.
			continue
		}
		skel := r.b.skeleton
		if skelWidth > 0 && len(skel) > skelWidth {
			skel = skel[:skelWidth-3] + "..."
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			r.b.count, specMark(r.b.hasSpec), r.b.sender, r.hash[:10], skel)
		shown++
	}
	tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary\n")
	fmt.Fprintf(w, "  total messages       : %d\n", total)
	fmt.Fprintf(w, "  distinct fingerprints: %d\n", len(buckets))
	fmt.Fprintf(w, "  templates cached     : %d\n", store.Len())
	if total > 0 {
		fmt.Fprintf(w, "  coverage by count    : %d/%d (%.1f%%)\n",
			covered, total, 100*float64(covered)/float64(total))
	}
	if shown < len(rows) {
		fmt.Fprintf(w, "  rows hidden          : %d (raise --min-samples or pass --all)\n", len(rows)-shown)
	}
	return nil
}

func specMark(has bool) string {
	if has {
		return "yes"
	}
	return "no"
}

func formatName(f corpus.Format) string {
	switch f {
	case corpus.FormatXML:
		return "xml"
	case corpus.FormatText:
		return "text"
	default:
		return "unknown"
	}
}
