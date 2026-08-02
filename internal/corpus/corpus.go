// Package corpus streams SMS records from a dump file. Two input formats
// are supported and auto-detected: the SMS Backup & Restore XML export
// (Android community standard) and the plain-text 3-line-block export
// produced by a couple of newer MoMo backup tools.
//
// Design notes:
//   - Streaming, not slicing. Real dumps reach hundreds of MB; loading the
//     whole thing into memory kills the CLI on modest hardware.
//   - The record body must never be logged. Callers can call Fingerprint on
//     each record and print the skeleton, but the raw body is only fit for
//     spec.Execute.
package corpus

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

// Record is one SMS extracted from a dump.
type Record struct {
	Sender string
	Body   string
	Date   string // free-form, preserved as-is; normalisation is milestone 4
}

// Format identifies which parser to use.
type Format int

const (
	FormatUnknown Format = iota
	FormatXML
	FormatText
)

// DefaultAllowlist is a set of sender IDs known to carry MoMo / bank
// notifications in Ghana. Case-insensitive match. Extend as needed.
var DefaultAllowlist = []string{
	"MobileMoney", "MTN", "MTN MoMo", "MoMo",
	"TelecelCash", "Telecel", "VodafoneCash", "Vodafone",
	"GCB", "GCBBank",
	"Absa",
	"Ecobank",
	"Fidelity", "FidelityBank",
	"Stanbic", "StanbicBank",
	"ATMoney", "AirtelTigo",
}

// Iterator walks a dump one record at a time. Callers loop with Next and
// check Err at the end for a scanner error.
type Iterator struct {
	next func() (Record, bool, error)
	err  error
}

// Next returns the next record. ok=false means end-of-stream (check Err).
func (it *Iterator) Next() (Record, bool) {
	if it.err != nil {
		return Record{}, false
	}
	r, ok, err := it.next()
	if err != nil {
		it.err = err
		return Record{}, false
	}
	return r, ok
}

// Err returns the terminal error if any.
func (it *Iterator) Err() error { return it.err }

// Open detects the format of the file and returns an iterator plus the
// detected Format. Detection peeks the first non-blank line: if it starts
// with '<', assume XML; else text.
func Open(path string, allowlist []string) (*Iterator, Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, FormatUnknown, fmt.Errorf("corpus: open %s: %w", path, err)
	}
	// Sniff the first ~4KB to decide format.
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, FormatUnknown, fmt.Errorf("corpus: seek: %w", err)
	}
	format := sniff(head)

	allow := buildAllowSet(allowlist)

	switch format {
	case FormatXML:
		return newXMLIterator(f, allow), FormatXML, nil
	case FormatText:
		return newTextIterator(f, allow), FormatText, nil
	default:
		f.Close()
		return nil, FormatUnknown, fmt.Errorf("corpus: could not detect format for %s", path)
	}
}

func sniff(head []byte) Format {
	for _, b := range head {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '<':
			return FormatXML
		default:
			return FormatText
		}
	}
	return FormatUnknown
}

func buildAllowSet(allowlist []string) map[string]bool {
	if allowlist == nil {
		allowlist = DefaultAllowlist
	}
	m := make(map[string]bool, len(allowlist))
	for _, s := range allowlist {
		m[strings.ToLower(strings.TrimSpace(s))] = true
	}
	return m
}

func senderAllowed(allow map[string]bool, sender string) bool {
	if len(allow) == 0 {
		return true
	}
	return allow[strings.ToLower(strings.TrimSpace(sender))]
}

// ---- XML (SMS Backup & Restore) ----

type xmlSMS struct {
	Address string `xml:"address,attr"`
	Body    string `xml:"body,attr"`
	Date    string `xml:"date,attr"`
}

func newXMLIterator(f *os.File, allow map[string]bool) *Iterator {
	dec := xml.NewDecoder(f)
	it := &Iterator{}
	it.next = func() (Record, bool, error) {
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				f.Close()
				return Record{}, false, nil
			}
			if err != nil {
				f.Close()
				return Record{}, false, fmt.Errorf("corpus: xml: %w", err)
			}
			se, ok := tok.(xml.StartElement)
			if !ok || se.Name.Local != "sms" {
				continue
			}
			var s xmlSMS
			if err := dec.DecodeElement(&s, &se); err != nil {
				return Record{}, false, fmt.Errorf("corpus: xml decode: %w", err)
			}
			if !senderAllowed(allow, s.Address) {
				continue
			}
			return Record{Sender: s.Address, Body: s.Body, Date: s.Date}, true, nil
		}
	}
	return it
}

// ---- Text (3-line-block export) ----
//
// Blocks look like:
//
//   Feb 09, 2025  9:57:48 AM
//   MobileMoney
//   Payment for GHS3.00 to MTN...
//   [blank]
//
// The date header sometimes trails with "(Read by you after ...)" which we
// keep as part of the Date field verbatim — normalisation is milestone 4.
// Bodies may span multiple lines; everything from line 3 to the blank is
// concatenated with a single space.

func newTextIterator(f *os.File, allow map[string]bool) *Iterator {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20) // MoMo lines are long
	it := &Iterator{}

	var block []string
	drain := func() (Record, bool) {
		defer func() { block = nil }()
		if len(block) < 3 {
			return Record{}, false
		}
		sender := strings.TrimSpace(block[1])
		body := strings.TrimSpace(strings.Join(block[2:], " "))
		if body == "" {
			return Record{}, false
		}
		if !senderAllowed(allow, sender) {
			return Record{}, false
		}
		return Record{Sender: sender, Body: body, Date: strings.TrimSpace(block[0])}, true
	}

	it.next = func() (Record, bool, error) {
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(line) == "" {
				if r, ok := drain(); ok {
					return r, true, nil
				}
				continue
			}
			block = append(block, line)
		}
		if err := sc.Err(); err != nil {
			f.Close()
			return Record{}, false, fmt.Errorf("corpus: scan: %w", err)
		}
		// flush trailing block at EOF
		if r, ok := drain(); ok {
			return r, true, nil
		}
		f.Close()
		return Record{}, false, nil
	}
	return it
}
