package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Tylerasa/blackwater/internal/corpus"
	"github.com/Tylerasa/blackwater/internal/fingerprint"
	"github.com/Tylerasa/blackwater/internal/spec"
	"github.com/spf13/cobra"
)

func newGenerateCmd() *cobra.Command {
	var (
		input       string
		templates   string
		allowlist   []string
		limit       int
		dryRun      bool
		yes         bool
		targetFP    string
		maxAttempts int
	)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate parsing specs for unseen fingerprints via Claude",
		Long: `generate finds every fingerprint in the dump that is NOT yet in the
templates cache, picks one sample body per fingerprint, sends the skeleton +
sample to the Claude API, validates the returned spec against the sample, and
saves it to templates.json.

Each unseen fingerprint costs one API call (or a small number if retries are
needed). Templates cached from previous runs are skipped for free.

Requires ANTHROPIC_API_KEY. Model defaults to Claude Haiku 4.5; override
with LEDGER_MODEL if you want Sonnet or Opus.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerate(cmd.Context(), cmd.OutOrStdout(), generateOpts{
				input:       input,
				templates:   templates,
				allowlist:   allowlist,
				limit:       limit,
				dryRun:      dryRun,
				assumeYes:   yes,
				targetFP:    targetFP,
				maxAttempts: maxAttempts,
			})
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "path to SMS dump")
	cmd.Flags().StringVar(&templates, "templates", "templates/templates.json", "path to templates cache")
	cmd.Flags().StringSliceVar(&allowlist, "allowlist", nil, "override sender allowlist")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after generating N specs (0 = no limit)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "generate but do NOT persist to templates.json")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the pre-flight confirmation prompt")
	cmd.Flags().StringVar(&targetFP, "fingerprint", "", "only generate for this specific fingerprint (hash prefix ok)")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 3, "retry limit per template")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

type generateOpts struct {
	input       string
	templates   string
	allowlist   []string
	limit       int
	dryRun      bool
	assumeYes   bool
	targetFP    string
	maxAttempts int
}

// candidate is one unseen fingerprint waiting for a spec.
type candidate struct {
	hash     string
	skeleton string
	sender   string
	sample   string
	count    int
}

func runGenerate(ctx context.Context, w io.Writer, o generateOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
	store, err := spec.Load(o.templates)
	if err != nil {
		return err
	}
	client, err := spec.NewAnthropicClient()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "model: %s\n", client.Model)

	// Walk the dump once, collect one candidate per unseen fingerprint.
	// First sample body per fingerprint wins — deterministic enough for now.
	candidates, total, err := collectCandidates(o.input, o.allowlist, store, o.targetFP)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Fprintf(w, "nothing to do: %d messages, all fingerprints already cached\n", total)
		return nil
	}
	// Highest-frequency first: biggest coverage win per API call.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].count > candidates[j].count })
	if o.limit > 0 && len(candidates) > o.limit {
		candidates = candidates[:o.limit]
	}

	fmt.Fprintf(w, "found %d unseen fingerprints covering %d messages\n",
		len(candidates), sumCounts(candidates))
	fmt.Fprintln(w, "this will send one API call per fingerprint (plus retries on failure).")
	fmt.Fprintln(w, "sample bodies are sent to Claude — see README 'Handling real data'.")
	if o.dryRun {
		fmt.Fprintln(w, "--dry-run set: specs will NOT be persisted.")
	}
	if !o.assumeYes {
		if !confirm(w, "proceed?") {
			fmt.Fprintln(w, "aborted.")
			return nil
		}
	}

	succeeded, failed := 0, 0
	for i, c := range candidates {
		fmt.Fprintf(w, "\n[%d/%d] fp=%s count=%d sender=%s\n",
			i+1, len(candidates), c.hash[:10], c.count, c.sender)
		fmt.Fprintf(w, "  skeleton: %s\n", truncate(c.skeleton, 100))

		s, err := spec.Generate(ctx, spec.GenerateOptions{
			Fingerprint: c.hash,
			Skeleton:    c.skeleton,
			Sample:      c.sample,
			Sender:      c.sender,
			MaxAttempts: o.maxAttempts,
			Client:      client,
		})
		if err != nil {
			failed++
			fmt.Fprintf(w, "  FAILED: %v\n", err)
			continue
		}
		succeeded++
		fields, _ := spec.Execute(s, c.sample)
		printGeneratedSpec(w, s, fields)

		if !o.dryRun {
			store.Put(s)
			if err := store.Save(o.templates); err != nil {
				return fmt.Errorf("save after fp %s: %w", c.hash[:10], err)
			}
		}
	}

	fmt.Fprintf(w, "\ndone: %d succeeded, %d failed. templates cached: %d\n",
		succeeded, failed, store.Len())
	if o.dryRun {
		fmt.Fprintln(w, "(dry-run: nothing written)")
	}
	return nil
}

func collectCandidates(input string, allowlist []string, store *spec.Store, targetFP string) ([]candidate, int, error) {
	it, _, err := corpus.Open(input, allowlist)
	if err != nil {
		return nil, 0, err
	}
	byFP := map[string]*candidate{}
	total := 0
	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		total++
		h, sk := fingerprint.Fingerprint(r.Sender, r.Body)
		if _, cached := store.Get(h); cached {
			continue
		}
		if targetFP != "" && !strings.HasPrefix(h, targetFP) {
			continue
		}
		c, seen := byFP[h]
		if !seen {
			byFP[h] = &candidate{hash: h, skeleton: sk, sender: r.Sender, sample: r.Body, count: 1}
		} else {
			c.count++
		}
	}
	if err := it.Err(); err != nil {
		return nil, total, err
	}
	out := make([]candidate, 0, len(byFP))
	for _, c := range byFP {
		out = append(out, *c)
	}
	return out, total, nil
}

func printGeneratedSpec(w io.Writer, s spec.Spec, fields map[string]string) {
	fmt.Fprintf(w, "  direction: %s\n", s.Direction)
	fmt.Fprintf(w, "  pattern:   %s\n", truncate(s.Pattern, 120))
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "    %-14s = %q\n", k, fields[k])
	}
}

func sumCounts(cs []candidate) int {
	n := 0
	for _, c := range cs {
		n += c.count
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func confirm(w io.Writer, prompt string) bool {
	fmt.Fprintf(w, "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
