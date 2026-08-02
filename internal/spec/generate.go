package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GenerateOptions bundles the inputs for one Generate call.
type GenerateOptions struct {
	Fingerprint string
	Skeleton    string
	Sample      string
	Sender      string
	MaxAttempts int    // default 3
	Client      Client // required
}

// Generate asks the LLM for a Spec, validates it against the sample, and
// retries with error feedback on failure. Returns a validated Spec ready to
// hand to Store.Put.
//
// The retry loop is what makes this trustworthy: even if the LLM produces a
// bad regex the first time, we tell it exactly what broke and let it try
// again. Only Specs that pass Validate are ever returned.
func Generate(ctx context.Context, opts GenerateOptions) (Spec, error) {
	if opts.Client == nil {
		return Spec{}, errors.New("spec: Generate needs a Client")
	}
	if opts.Sample == "" {
		return Spec{}, errors.New("spec: Generate needs a sample body")
	}
	attempts := opts.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}

	initial := buildInitialUserMessage(opts.Skeleton, opts.Sample, opts.Sender)
	var lastErr error
	var prevPattern string

	for i := 0; i < attempts; i++ {
		userMsg := initial
		if i > 0 {
			userMsg = buildRetryUserMessage(opts.Skeleton, opts.Sample, opts.Sender, prevPattern, lastErr)
		}
		raw, err := opts.Client.Complete(ctx, systemPrompt, userMsg)
		if err != nil {
			// A transport error is not something the LLM can fix on retry.
			// Bail immediately.
			return Spec{}, fmt.Errorf("spec: client (attempt %d): %w", i+1, err)
		}
		s, parseErr := parseSpecJSON(raw)
		if parseErr != nil {
			lastErr = fmt.Errorf("your previous response was not valid JSON: %v", parseErr)
			prevPattern = extractPatternGuess(raw)
			continue
		}
		s.Fingerprint = opts.Fingerprint
		s.Sender = opts.Sender
		if s.Version == 0 {
			s.Version = 1
		}
		if valErr := Validate(s, opts.Sample); valErr != nil {
			lastErr = valErr
			prevPattern = s.Pattern
			continue
		}
		return s, nil
	}

	return Spec{}, fmt.Errorf("spec: gave up after %d attempts; last error: %v", attempts, lastErr)
}

func buildInitialUserMessage(skeleton, sample, sender string) string {
	var b strings.Builder
	b.WriteString("Skeleton: ")
	b.WriteString(skeleton)
	b.WriteString("\n\nSample: ")
	b.WriteString(sample)
	b.WriteString("\n\nSender: ")
	b.WriteString(sender)
	b.WriteString("\n\nProduce the JSON spec now.")
	return b.String()
}

func buildRetryUserMessage(skeleton, sample, sender, prevPattern string, prevErr error) string {
	var b strings.Builder
	b.WriteString("Your previous attempt failed validation:\n")
	fmt.Fprintf(&b, "  %v\n\n", prevErr)
	if prevPattern != "" {
		b.WriteString("Previous pattern:\n  ")
		b.WriteString(prevPattern)
		b.WriteString("\n\n")
	}
	b.WriteString("Try again. Same inputs:\n\n")
	b.WriteString("Skeleton: ")
	b.WriteString(skeleton)
	b.WriteString("\n\nSample: ")
	b.WriteString(sample)
	b.WriteString("\n\nSender: ")
	b.WriteString(sender)
	b.WriteString("\n\nOutput only the JSON object.")
	return b.String()
}

// parseSpecJSON extracts a Spec from the LLM's response. Being defensive
// about markdown fences and leading prose — Haiku usually returns raw JSON
// but let's not depend on it.
func parseSpecJSON(raw string) (Spec, error) {
	raw = strings.TrimSpace(raw)
	// Strip markdown fences if present.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// Some models front-load text before the JSON. Find the first `{` and
	// trust everything from there to the matched closing brace.
	start := strings.Index(raw, "{")
	if start == -1 {
		return Spec{}, errors.New("no JSON object found in response")
	}
	end := findMatchingBrace(raw, start)
	if end == -1 {
		return Spec{}, errors.New("unbalanced JSON braces in response")
	}
	body := raw[start : end+1]

	var s Spec
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		return Spec{}, fmt.Errorf("unmarshal: %w", err)
	}
	if s.Pattern == "" {
		return Spec{}, errors.New(`missing "pattern" in response`)
	}
	if s.Direction == "" {
		return Spec{}, errors.New(`missing "direction" in response`)
	}
	return s, nil
}

// findMatchingBrace scans from an opening `{` and returns the index of the
// matching `}`, correctly ignoring braces inside strings.
func findMatchingBrace(s string, start int) int {
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// extractPatternGuess pulls out something that looks like a pattern from a
// mangled response, so the retry prompt can still tell the LLM what it just
// tried. Best-effort; empty string is fine.
func extractPatternGuess(raw string) string {
	needle := `"pattern"`
	i := strings.Index(raw, needle)
	if i == -1 {
		return ""
	}
	rest := raw[i+len(needle):]
	q1 := strings.Index(rest, `"`)
	if q1 == -1 {
		return ""
	}
	rest = rest[q1+1:]
	// Find unescaped closing quote.
	esc := false
	for j := 0; j < len(rest); j++ {
		if esc {
			esc = false
			continue
		}
		if rest[j] == '\\' {
			esc = true
			continue
		}
		if rest[j] == '"' {
			return rest[:j]
		}
	}
	return ""
}
