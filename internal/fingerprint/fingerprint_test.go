package fingerprint

import (
	"strings"
	"testing"
)

// TestMasks covers each masking rule in isolation and a few known ordering
// interactions. Where a rule is expected to touch the input, we assert that
// the token appears AND that no fragment of the original variable data leaks
// through — the second half is what actually protects against PII leakage.
func TestMasks(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		mustHave   []string // tokens that must appear
		mustNotHas []string // substrings that must NOT survive masking
	}{
		// amounts
		{
			name:       "amount GHS prefix with decimals",
			body:       "You have received GHS 1,234.56 today",
			mustHave:   []string{amtToken},
			mustNotHas: []string{"1,234", "1234", "GHS"},
		},
		{
			name:       "amount GH¢ prefix",
			body:       "Debit GH¢50.00 completed",
			mustHave:   []string{amtToken},
			mustNotHas: []string{"50.00", "GH¢"},
		},
		{
			name:       "amount suffix form",
			body:       "Fee 2.50 GHS applied",
			mustHave:   []string{amtToken},
			mustNotHas: []string{"2.50"},
		},
		{
			name:       "amount no decimals",
			body:       "Transfer GHS 100 to friend",
			mustHave:   []string{amtToken},
			mustNotHas: []string{"100"},
		},
		{
			name:       "two amounts in one message",
			body:       "Sent GHS 50.00, balance GHS 200.00",
			mustHave:   []string{amtToken},
			mustNotHas: []string{"50.00", "200.00"},
		},

		// phone numbers
		{
			name:       "msisdn local format",
			body:       "Sent to 0244123456 successfully",
			mustHave:   []string{msisdnToken},
			mustNotHas: []string{"0244123456"},
		},
		{
			name:       "msisdn international format",
			body:       "From +233244123456",
			mustHave:   []string{msisdnToken},
			mustNotHas: []string{"+233244123456", "233244123456"},
		},

		// dates
		{
			name:       "date ISO",
			body:       "on 2024-01-15 processed",
			mustHave:   []string{dateToken},
			mustNotHas: []string{"2024-01-15", "2024"},
		},
		{
			name:       "date slash",
			body:       "date 15/01/2024 confirmed",
			mustHave:   []string{dateToken},
			mustNotHas: []string{"15/01/2024"},
		},
		{
			name:       "date month name dash",
			body:       "on 15-Jan-2024",
			mustHave:   []string{dateToken},
			mustNotHas: []string{"15-Jan-2024", "Jan"},
		},
		{
			name:       "date month first",
			body:       "on Jan 15, 2024",
			mustHave:   []string{dateToken},
			mustNotHas: []string{"Jan 15", "2024"},
		},

		// times
		{
			name:       "time HH:MM",
			body:       "at 14:30 today",
			mustHave:   []string{timeToken},
			mustNotHas: []string{"14:30"},
		},
		{
			name:       "time HH:MM:SS",
			body:       "at 14:30:22 today",
			mustHave:   []string{timeToken},
			mustNotHas: []string{"14:30:22"},
		},
		{
			name:       "time with am/pm",
			body:       "at 2:30pm confirmed",
			mustHave:   []string{timeToken},
			mustNotHas: []string{"2:30pm", "2:30"},
		},

		// references
		{
			name:       "reference alphanumeric",
			body:       "Ref ABC12345XY complete",
			mustHave:   []string{refToken},
			mustNotHas: []string{"ABC12345XY"},
		},
		{
			name:       "reference pure digits 8+",
			body:       "Ref 12345678 saved",
			mustHave:   []string{refToken},
			mustNotHas: []string{"12345678"},
		},
		{
			name:       "reference not triggered by short alnum",
			body:       "See TXN123 done",
			mustHave:   []string{}, // TXN123 is 6 chars, below threshold
			mustNotHas: []string{refToken},
		},

		// bare numbers
		{
			name:       "standalone number",
			body:       "Received item 5 of 7",
			mustHave:   []string{numToken},
			mustNotHas: []string{" 5 ", " 7"},
		},

		// names
		{
			name:       "name title case two words",
			body:       "From John Doe now",
			mustHave:   []string{nameToken},
			mustNotHas: []string{"John Doe"},
		},
		{
			name:       "name all caps two words",
			body:       "From JOHN DOE now",
			mustHave:   []string{nameToken},
			mustNotHas: []string{"JOHN DOE"},
		},
		{
			name:       "name three words",
			body:       "From John Middle Doe now",
			mustHave:   []string{nameToken},
			mustNotHas: []string{"John Middle Doe"},
		},
		{
			name:       "single capitalised word is not a name",
			body:       "from John now",
			mustHave:   []string{}, // rule requires 2+ words in a row
			mustNotHas: []string{nameToken},
		},

		// combined realistic message
		{
			name: "realistic MTN debit",
			body: "You have transferred GHS 100.00 to JOHN DOE 0244123456 on 15-Jan-2024 " +
				"at 14:30. Ref ABC12345XY. New balance GHS 500.00. Fee GHS 1.00.",
			mustHave: []string{amtToken, msisdnToken, dateToken, timeToken, refToken, nameToken},
			mustNotHas: []string{
				"100.00", "500.00", "1.00",
				"0244123456",
				"15-Jan-2024",
				"14:30",
				"ABC12345XY",
				"JOHN DOE",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, skeleton := Fingerprint("MTN", tc.body)
			for _, tok := range tc.mustHave {
				if !strings.Contains(skeleton, strings.ToLower(tok)) {
					t.Errorf("expected token %q in skeleton %q", tok, skeleton)
				}
			}
			for _, leak := range tc.mustNotHas {
				if strings.Contains(skeleton, strings.ToLower(leak)) {
					t.Errorf("leak: %q survived in skeleton %q", leak, skeleton)
				}
			}
		})
	}
}

// TestOrderingInteractions pins down the specific ordering decisions in the
// package doc — these are the interactions most likely to regress if someone
// reorders the mask calls.
func TestOrderingInteractions(t *testing.T) {
	t.Run("msisdn wins over ref for 10-digit phone", func(t *testing.T) {
		_, sk := Fingerprint("MTN", "to 0244123456 done")
		if !strings.Contains(sk, "<msisdn>") {
			t.Fatalf("expected msisdn token, got %q", sk)
		}
		if strings.Contains(sk, "<ref>") {
			t.Fatalf("ref should not swallow a phone number, got %q", sk)
		}
	})

	t.Run("amount wins over ref/num for GHS 1,234.56", func(t *testing.T) {
		_, sk := Fingerprint("MTN", "amount GHS 1,234.56 done")
		if !strings.Contains(sk, "<amt>") {
			t.Fatalf("expected amt token, got %q", sk)
		}
		if strings.Contains(sk, "1234") || strings.Contains(sk, "1,234") {
			t.Fatalf("digits leaked past amount mask: %q", sk)
		}
	})

	t.Run("date wins over ref for numeric slash date", func(t *testing.T) {
		_, sk := Fingerprint("MTN", "on 15/01/2024 done")
		if !strings.Contains(sk, "<date>") {
			t.Fatalf("expected date token, got %q", sk)
		}
	})
}

// TestRefRequiresDigit: pure-letter alnum runs of 8+ chars are usually
// English template words (received, Available, MobileMoney), not real refs.
// Requiring a digit lets those words survive. Note: consecutive Title-case
// words like "Transaction Id" still get NAME-masked, but sentence-cased and
// lowercase words survive — this test proves that path.
func TestRefRequiresDigit(t *testing.T) {
	_, sk := Fingerprint("MobileMoney", "Payment received. New balance available. Id 52086662958.")
	if !strings.Contains(sk, "<ref>") {
		t.Fatalf("digit-bearing ref should be masked: %q", sk)
	}
	for _, word := range []string{"received", "available"} {
		if !strings.Contains(sk, word) {
			t.Errorf("pure-letter word %q should survive: %q", word, sk)
		}
	}
}

func TestMSISDNBare233(t *testing.T) {
	_, sk := Fingerprint("MobileMoney", "Reference: JOHN DOE,233509346370,Q")
	if !strings.Contains(sk, "<msisdn>") {
		t.Fatalf("bare 233 phone should be masked as MSISDN: %q", sk)
	}
	if strings.Contains(sk, "233509346370") {
		t.Fatalf("phone digits leaked: %q", sk)
	}
}

// TestFingerprintDeterminism is the whole reason this thing exists: two SMS
// from the same template must hash the same, two from different templates
// must not.
func TestFingerprintDeterminism(t *testing.T) {
	a := "You have received GHS 100.00 from JOHN DOE (0244123456). Balance: GHS 500.00. Ref: ABC12345XY."
	b := "You have received GHS 50.00 from JANE ROE (0201234567). Balance: GHS 250.00. Ref: XYZ98765LM."
	c := "You have transferred GHS 100.00 to JOHN DOE (0244123456). Balance: GHS 400.00. Ref: ABC12345XY."

	ha, ska := Fingerprint("MTN", a)
	hb, skb := Fingerprint("MTN", b)
	hc, _ := Fingerprint("MTN", c)

	if ha != hb {
		t.Errorf("same template must collide:\n  a=%s\n  b=%s\n  ska=%q\n  skb=%q", ha, hb, ska, skb)
	}
	if ha == hc {
		t.Errorf("received != transferred templates must not collide: %s", ha)
	}
}

func TestSenderNormalisation(t *testing.T) {
	h1, _ := Fingerprint("MTN", "You have received GHS 10.00")
	h2, _ := Fingerprint("mtn", "You have received GHS 10.00")
	h3, _ := Fingerprint("  MTN  ", "You have received GHS 10.00")
	if h1 != h2 || h1 != h3 {
		t.Errorf("sender should be case- and space-insensitive: %s %s %s", h1, h2, h3)
	}

	h4, _ := Fingerprint("ECOBANK", "You have received GHS 10.00")
	if h4 == h1 {
		t.Errorf("different senders must produce different fingerprints")
	}
}

func TestEmptyBody(t *testing.T) {
	h, sk := Fingerprint("MTN", "")
	if h == "" {
		t.Errorf("empty body should still produce a hash")
	}
	if sk != "MTN: " {
		t.Errorf("unexpected skeleton for empty body: %q", sk)
	}
}

// TestSkeletonPIIFree is a defence-in-depth check: no matter how weird the
// input, the skeleton must never contain the raw variable tokens we saw
// going in. Extend this list as new bug reports come in.
func TestSkeletonPIIFree(t *testing.T) {
	body := "GHS 999.99 to +233244000000 on 2024-12-31 at 23:59:59 ref DEAD00BEEF from ABENA MENSAH"
	_, sk := Fingerprint("MTN", body)
	for _, leak := range []string{
		"999.99", "244000000", "2024-12-31", "23:59:59", "dead00beef", "abena mensah",
	} {
		if strings.Contains(sk, leak) {
			t.Errorf("PII leak: %q survived: %q", leak, sk)
		}
	}
}
