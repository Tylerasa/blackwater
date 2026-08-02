package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSniff(t *testing.T) {
	if sniff([]byte("   <smses>...")) != FormatXML {
		t.Error("XML head misdetected")
	}
	if sniff([]byte("Feb 09, 2025 9:57:48 AM\n...")) != FormatText {
		t.Error("text head misdetected")
	}
	if sniff([]byte("   \n\t")) != FormatUnknown {
		t.Error("empty head should be unknown")
	}
}

func TestOpenXML(t *testing.T) {
	xmlDump := `<?xml version="1.0"?>
<smses>
  <sms address="MobileMoney" body="Payment for GHS3.00 to MTN" date="1"/>
  <sms address="RandomSpam" body="unrelated" date="2"/>
  <sms address="MTN" body="another" date="3"/>
</smses>`
	p := writeTemp(t, "dump.xml", xmlDump)
	it, format, err := Open(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatXML {
		t.Fatalf("expected FormatXML, got %v", format)
	}
	var got []Record
	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, r)
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 allowlisted records, got %d: %+v", len(got), got)
	}
	if got[0].Sender != "MobileMoney" || got[1].Sender != "MTN" {
		t.Errorf("unexpected senders: %+v", got)
	}
}

func TestOpenText(t *testing.T) {
	textDump := `Feb 09, 2025  9:57:48 AM
MobileMoney
Payment for GHS3.00 to MTN

Feb 10, 2025  9:29:10 PM
RandomSpam
should be dropped

Feb 11, 2025 10:00:00 AM (Read by you after 5 minutes)
Ecobank
Bank credit
of GHS 50.00`
	p := writeTemp(t, "dump.txt", textDump)
	it, format, err := Open(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatText {
		t.Fatalf("expected FormatText, got %v", format)
	}
	var got []Record
	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, r)
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 allowlisted records, got %d: %+v", len(got), got)
	}
	if got[0].Sender != "MobileMoney" {
		t.Errorf("first sender: %q", got[0].Sender)
	}
	if got[1].Sender != "Ecobank" {
		t.Errorf("second sender: %q", got[1].Sender)
	}
	// multi-line body joined with space
	if got[1].Body != "Bank credit of GHS 50.00" {
		t.Errorf("multiline body not joined: %q", got[1].Body)
	}
}

func TestAllowlistOverride(t *testing.T) {
	textDump := `Jan 1, 2025 12:00 AM
CustomBank
hi

Jan 2, 2025 12:00 AM
MobileMoney
default sender
`
	p := writeTemp(t, "dump.txt", textDump)
	// override drops MobileMoney, adds CustomBank
	it, _, err := Open(p, []string{"CustomBank"})
	if err != nil {
		t.Fatal(err)
	}
	var senders []string
	for {
		r, ok := it.Next()
		if !ok {
			break
		}
		senders = append(senders, r.Sender)
	}
	if len(senders) != 1 || senders[0] != "CustomBank" {
		t.Errorf("expected [CustomBank], got %v", senders)
	}
}
