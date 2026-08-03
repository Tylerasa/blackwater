// Package normalize converts raw regex captures from spec.Execute into a
// canonical Transaction shape suitable for a ledger export or a downstream
// grouping/dedupe step.
//
// The design principle: normalisation is lossy on purpose. We drop the
// template-specific field names ("counterparty" vs "recipient" vs "payee")
// and settle on one canonical set. If you ever need the exact raw values
// again, they're preserved in Transaction.RawFields.
package normalize

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tylerasa/blackwater/internal/spec"
)

// Transaction is the canonical shape every normalised SMS reduces to.
// Money values are stored as int64 in minor units (GHS pesewas). Zero
// values mean "not present in the message", not "zero pesewas" — if that
// ambiguity ever matters, check RawFields for the raw string.
type Transaction struct {
	Fingerprint  string
	Sender       string
	Timestamp    time.Time
	Direction    spec.Direction
	Amount       int64 // pesewas
	Currency     string
	Counterparty string
	Reference    string
	Balance      int64 // pesewas; 0 = not captured
	Fee          int64
	Tax          int64
	RawFields    map[string]string // audit trail of what came out of spec.Execute
}

// Amounts helpers: turn pesewas back to a display string like "12.34".
func (t Transaction) AmountGHS() string  { return pesewasToGHS(t.Amount) }
func (t Transaction) BalanceGHS() string { return pesewasToGHS(t.Balance) }
func (t Transaction) FeeGHS() string     { return pesewasToGHS(t.Fee) }
func (t Transaction) TaxGHS() string     { return pesewasToGHS(t.Tax) }

// Normalize walks spec.Fields, converts each captured value based on its
// Kind, and returns a Transaction. smsDate is the raw date string from the
// corpus Record — used as a fallback timestamp when the message body
// itself does not carry a date/time.
//
// Errors on fields we CANNOT recover from (e.g. unparseable amount for a
// required field). Missing optional fields are silently zeroed.
func Normalize(s spec.Spec, fields map[string]string, smsDate string) (Transaction, error) {
	t := Transaction{
		Fingerprint: s.Fingerprint,
		Sender:      s.Sender,
		Direction:   s.Direction,
		Currency:    "GHS",
		RawFields:   fields,
	}

	if ts, ok := pickTimestamp(s, fields, smsDate); ok {
		t.Timestamp = ts
	}

	for name, field := range s.Fields {
		raw := strings.TrimSpace(fields[name])
		if raw == "" {
			continue
		}
		switch field.Kind {
		case spec.KindAmount:
			v, err := parsePesewas(raw)
			if err != nil {
				return Transaction{}, fmt.Errorf("normalize: field %q amount: %w", name, err)
			}
			t.Amount = v
		case spec.KindBalance:
			v, err := parsePesewas(raw)
			if err != nil {
				return Transaction{}, fmt.Errorf("normalize: field %q balance: %w", name, err)
			}
			t.Balance = v
		case spec.KindFee:
			// Fees like "GHS -" or "-" show up in Telecel Push messages.
			// Non-numeric fee = zero, not a hard error.
			if isDashPlaceholder(raw) {
				break
			}
			v, err := parsePesewas(raw)
			if err != nil {
				return Transaction{}, fmt.Errorf("normalize: field %q fee: %w", name, err)
			}
			t.Fee = v
		case spec.KindCounterparty:
			t.Counterparty = collapseSpaces(raw)
		case spec.KindReference:
			if t.Reference == "" {
				t.Reference = raw
			}
		case spec.KindCurrency:
			t.Currency = strings.ToUpper(raw)
		}
		// Templates often name a "tax" field with kind:other/fee. Catch
		// it by group name so it lands in the right column.
		if t.Tax == 0 && strings.Contains(strings.ToLower(name), "tax") {
			if v, err := parsePesewas(raw); err == nil {
				t.Tax = v
			}
		}
	}

	return t, nil
}

// parsePesewas takes a MoMo-style money string ("12.34", "1,234.56", "0",
// "GHS 5") and returns pesewas (integer minor units). Rejects negative
// numbers and non-numeric strings.
func parsePesewas(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"GHS", "GH¢", "GHC", "Ghc"} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")

	if s == "" {
		return 0, errors.New("empty")
	}

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" && !hasFrac {
		return 0, fmt.Errorf("no digits in %q", s)
	}
	if intPart == "" {
		intPart = "0"
	}

	intVal, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad integer part %q: %w", intPart, err)
	}
	if intVal < 0 {
		return 0, fmt.Errorf("negative amount %q not supported", s)
	}
	var fracVal int64
	if hasFrac {
		switch {
		case len(fracPart) == 0:
			fracVal = 0
		case len(fracPart) == 1:
			fracVal, err = strconv.ParseInt(fracPart+"0", 10, 64)
		case len(fracPart) == 2:
			fracVal, err = strconv.ParseInt(fracPart, 10, 64)
		default:
			// truncate silently; MoMo does not send sub-pesewa precision.
			fracVal, err = strconv.ParseInt(fracPart[:2], 10, 64)
		}
		if err != nil {
			return 0, fmt.Errorf("bad fractional part %q: %w", fracPart, err)
		}
	}
	return intVal*100 + fracVal, nil
}

func pesewasToGHS(p int64) string {
	sign := ""
	if p < 0 {
		sign = "-"
		p = -p
	}
	whole := p / 100
	frac := p % 100
	return fmt.Sprintf("%s%d.%02d", sign, whole, frac)
}

func isDashPlaceholder(s string) bool {
	s = strings.TrimSpace(s)
	return s == "-" || s == "GHS -" || s == "GHS-"
}

func collapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range strings.TrimSpace(s) {
		if r == ' ' || r == '\t' {
			if prevSpace {
				continue
			}
			prevSpace = true
			b.WriteRune(' ')
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// pickTimestamp returns the best available timestamp for a matched message.
// Priority: (1) spec-captured date + time, (2) spec-captured date alone,
// (3) parsed smsDate from the corpus record.
func pickTimestamp(s spec.Spec, fields map[string]string, smsDate string) (time.Time, bool) {
	var dateStr, timeStr string
	for name, f := range s.Fields {
		switch f.Kind {
		case spec.KindDate:
			if v := strings.TrimSpace(fields[name]); v != "" && dateStr == "" {
				dateStr = v
			}
		case spec.KindTime:
			if v := strings.TrimSpace(fields[name]); v != "" && timeStr == "" {
				timeStr = v
			}
		}
	}
	if dateStr != "" {
		if timeStr != "" {
			if t, ok := tryFormats(dateStr+" "+timeStr, specDateTimeFormats); ok {
				return t, true
			}
		}
		if t, ok := tryFormats(dateStr, specDateFormats); ok {
			return t, true
		}
	}
	if smsDate != "" {
		if idx := strings.Index(smsDate, " (Read"); idx > 0 {
			smsDate = smsDate[:idx]
		}
		if t, ok := tryFormats(strings.TrimSpace(smsDate), corpusDateFormats); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func tryFormats(s string, layouts []string) (time.Time, bool) {
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

var (
	specDateTimeFormats = []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"02/01/2006 15:04:05",
		"02/01/2006 15:04",
		"02-Jan-2006 15:04:05",
		"02-Jan-2006 15:04",
	}
	specDateFormats = []string{
		"2006-01-02",
		"02/01/2006",
		"02-Jan-2006",
		"Jan 02, 2006",
	}
	// Corpus text export uses forms like
	//   "Feb 09, 2025  9:57:48 AM"    (two spaces before hour, single-digit hour)
	//   "Feb 15, 2025 12:18:59 PM"    (single space, two-digit hour)
	corpusDateFormats = []string{
		"Jan 02, 2006  3:04:05 PM",
		"Jan 02, 2006 3:04:05 PM",
		"Jan 02, 2006 15:04:05",
	}
)
