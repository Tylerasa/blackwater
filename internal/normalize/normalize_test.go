package normalize

import (
	"testing"
	"time"

	"github.com/Tylerasa/blackwater/internal/spec"
)

func TestParsePesewas(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"12.34", 1234, false},
		{"0", 0, false},
		{"0.00", 0, false},
		{"1,234.56", 123456, false},
		{"1234", 123400, false},
		{"GHS 5", 500, false},
		{"GHS3.00", 300, false},
		{"GH¢50.00", 5000, false},
		{"12.5", 1250, false},     // 1-digit fractional padded
		{"12.345", 1234, false},   // 3-digit truncated
		{".50", 50, false},        // no integer part
		{"", 0, true},
		{"abc", 0, true},
		{"-5.00", 0, true},        // negatives rejected
	}
	for _, c := range cases {
		got, err := parsePesewas(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parsePesewas(%q): expected error, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePesewas(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parsePesewas(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPesewasToGHS(t *testing.T) {
	cases := []struct{ in int64; want string }{
		{0, "0.00"},
		{5, "0.05"},
		{50, "0.50"},
		{1234, "12.34"},
		{123456, "1234.56"},
	}
	for _, c := range cases {
		if got := pesewasToGHS(c.in); got != c.want {
			t.Errorf("pesewasToGHS(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCollapseSpaces(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SYLVESTER ASARE  SARPONG", "SYLVESTER ASARE SARPONG"},
		{"  padded  ", "padded"},
		{"one two", "one two"},
		{"tabs\there", "tabs here"},
	}
	for _, c := range cases {
		if got := collapseSpaces(c.in); got != c.want {
			t.Errorf("collapseSpaces(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeMerchantPayment(t *testing.T) {
	s := spec.Spec{
		Fingerprint: "abc",
		Sender:      "MobileMoney",
		Direction:   spec.DirectionDebit,
		Fields: map[string]spec.Field{
			"amount":       {Kind: spec.KindAmount},
			"counterparty": {Kind: spec.KindCounterparty},
			"balance":      {Kind: spec.KindBalance},
			"fee":          {Kind: spec.KindFee},
			"tax":          {Kind: spec.KindOther}, // caught by name-based fallback
			"reference":    {Kind: spec.KindReference},
		},
	}
	fields := map[string]string{
		"amount":       "25.00",
		"counterparty": "JOHN  DOE",
		"balance":      "9.19",
		"fee":          "0.00",
		"tax":          "0.25",
		"reference":    "52780608995",
	}
	tx, err := Normalize(s, fields, "Feb 21, 2025  4:17:13 PM")
	if err != nil {
		t.Fatal(err)
	}
	if tx.Amount != 2500 {
		t.Errorf("amount: got %d, want 2500", tx.Amount)
	}
	if tx.Balance != 919 {
		t.Errorf("balance: got %d, want 919", tx.Balance)
	}
	if tx.Fee != 0 {
		t.Errorf("fee: got %d, want 0", tx.Fee)
	}
	if tx.Tax != 25 {
		t.Errorf("tax: got %d, want 25", tx.Tax)
	}
	if tx.Counterparty != "JOHN DOE" {
		t.Errorf("counterparty not collapsed: %q", tx.Counterparty)
	}
	if tx.Reference != "52780608995" {
		t.Errorf("reference: %q", tx.Reference)
	}
	if tx.Direction != spec.DirectionDebit {
		t.Errorf("direction: %q", tx.Direction)
	}
	if tx.Timestamp.IsZero() {
		t.Error("timestamp should have parsed from corpus date")
	}
	if tx.Timestamp.Hour() != 16 || tx.Timestamp.Minute() != 17 {
		t.Errorf("timestamp: got %v, expected 16:17 local", tx.Timestamp)
	}
}

func TestNormalizeSpecTimestampWins(t *testing.T) {
	// Telecel Push template has a body-embedded date + time. That should
	// win over the corpus date because it's the bank server truth.
	s := spec.Spec{
		Direction: spec.DirectionDebit,
		Fields: map[string]spec.Field{
			"amount": {Kind: spec.KindAmount},
			"date":   {Kind: spec.KindDate},
			"time":   {Kind: spec.KindTime},
		},
	}
	fields := map[string]string{
		"amount": "22.00",
		"date":   "2025-02-19",
		"time":   "20:07:28",
	}
	tx, err := Normalize(s, fields, "Feb 19, 2025  7:20:13 PM")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, 2, 19, 20, 7, 28, 0, time.UTC)
	if !tx.Timestamp.Equal(want) {
		t.Errorf("timestamp: got %v, want %v", tx.Timestamp, want)
	}
}

func TestNormalizeStripsReadAnnotation(t *testing.T) {
	s := spec.Spec{Direction: spec.DirectionDebit}
	tx, err := Normalize(s, map[string]string{}, "Feb 15, 2025 12:18:59 PM (Read by you after 2 hours, 24 minutes, 5 seconds)")
	if err != nil {
		t.Fatal(err)
	}
	if tx.Timestamp.IsZero() {
		t.Fatal("timestamp should parse despite the '(Read by ...)' suffix")
	}
	if tx.Timestamp.Hour() != 12 {
		t.Errorf("timestamp hour: got %d, want 12", tx.Timestamp.Hour())
	}
}

func TestNormalizeDashFee(t *testing.T) {
	// Telecel Push sometimes reports fee/tax as "GHS -" (literal dash).
	// Normalize should not error — just treat it as zero.
	s := spec.Spec{
		Direction: spec.DirectionDebit,
		Fields:    map[string]spec.Field{"fee": {Kind: spec.KindFee}},
	}
	tx, err := Normalize(s, map[string]string{"fee": "GHS -"}, "")
	if err != nil {
		t.Fatalf("dash placeholder should not error: %v", err)
	}
	if tx.Fee != 0 {
		t.Errorf("fee: got %d, want 0", tx.Fee)
	}
}

func TestNormalizeAmountGHSDisplay(t *testing.T) {
	tx := Transaction{Amount: 123456}
	if got := tx.AmountGHS(); got != "1234.56" {
		t.Errorf("AmountGHS: got %q, want %q", got, "1234.56")
	}
}
