package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Tylerasa/blackwater/internal/store"
	"github.com/spf13/cobra"
)

func newSumCmd() *cobra.Command {
	var (
		dbPath string
		since  string
		until  string
	)
	cmd := &cobra.Command{
		Use:   "sum",
		Short: "Show totals grouped by direction",
		Long: `sum queries the ledger DB and prints per-direction totals (count + GHS).
Use --since / --until to bound the window; formats accepted: 2025-02-01 or
2025-02-01T14:00:00Z.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSum(cmd.Context(), cmd.OutOrStdout(), sumOpts{dbPath: dbPath, since: since, until: until})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to SQLite ledger DB")
	cmd.Flags().StringVar(&since, "since", "", "lower bound (inclusive), e.g. 2025-02-01")
	cmd.Flags().StringVar(&until, "until", "", "upper bound (inclusive), e.g. 2025-02-28")
	_ = cmd.MarkFlagRequired("db")
	return cmd
}

type sumOpts struct {
	dbPath string
	since  string
	until  string
}

func runSum(ctx context.Context, w io.Writer, o sumOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
	since, err := parseDateBound(o.since, false)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	until, err := parseDateBound(o.until, true)
	if err != nil {
		return fmt.Errorf("--until: %w", err)
	}

	db, err := store.Open(o.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.SumByDirection(ctx, since, until)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DIRECTION\tCOUNT\tTOTAL_PESEWAS\tTOTAL_GHS")
	fmt.Fprintln(tw, "---------\t-----\t-------------\t---------")
	var totalIn, totalOut int64
	var msgs int
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", r.Direction, r.Count, r.TotalPesewas, ghs(r.TotalPesewas))
		msgs += r.Count
		switch r.Direction {
		case "credit":
			totalIn += r.TotalPesewas
		case "debit", "fee":
			totalOut += r.TotalPesewas
		}
	}
	tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintf(w, "messages: %d\n", msgs)
	fmt.Fprintf(w, "in     : %s GHS\n", ghs(totalIn))
	fmt.Fprintf(w, "out    : %s GHS\n", ghs(totalOut))
	fmt.Fprintf(w, "net    : %s GHS\n", ghs(totalIn-totalOut))
	return nil
}

// parseDateBound accepts empty (zero time), YYYY-MM-DD, or full RFC3339.
// When endOfDay is true and the input is date-only, the returned time is
// bumped to 23:59:59 UTC so `--until 2025-02-28` behaves inclusively.
func parseDateBound(s string, endOfDay bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if endOfDay {
			return t.Add(24*time.Hour - time.Second), nil
		}
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q; try YYYY-MM-DD or RFC3339", s)
}

func ghs(p int64) string {
	sign := ""
	if p < 0 {
		sign = "-"
		p = -p
	}
	return fmt.Sprintf("%s%d.%02d", sign, p/100, p%100)
}
