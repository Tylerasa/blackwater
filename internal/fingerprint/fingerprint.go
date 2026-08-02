// Package fingerprint turns an SMS into a template signature.
//
// The idea: two messages from the same bank template differ only in variable
// tokens (amounts, references, phone numbers, names, dates). If we mask every
// variable token and hash what's left, template-siblings collide and unrelated
// templates don't. That hash is the cache key for a parsing Spec.
//
// Ordering of the masks matters and is the main source of subtle bugs:
//   - amount first, because it is the most specific pattern (has a currency
//     prefix/suffix) — waiting risks its digits being eaten by NUM or REF
//   - msisdn before ref, because a bare 10-digit phone number is a valid
//     alphanumeric run of 8+ chars and would otherwise be swallowed by REF
//   - date and time before ref/num for the same reason
//   - ref before num, because ref is more specific (alnum with letters
//     possible) than a bare digit run
//   - name last, because the substituted tokens (`<AMT>`, `<REF>`, ...) sit
//     inside angle brackets and thus never contribute a word-boundary that
//     could form a spurious two-word Title-case sequence
//
// The skeleton returned alongside the hash is deliberately what we later
// send to the LLM and print in CLI output — so by construction it must
// contain no PII. Every rule below is enforced with that invariant in mind.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

const (
	amtToken    = "<AMT>"
	refToken    = "<REF>"
	msisdnToken = "<MSISDN>"
	dateToken   = "<DATE>"
	timeToken   = "<TIME>"
	numToken    = "<NUM>"
	nameToken   = "<NAME>"
)

var (
	// currency amounts: GHS / GH¢ / Ghc, prefix or suffix, with optional
	// thousands separators and decimals.
	reAmount = regexp.MustCompile(
		`(?i)(?:gh[¢c]|ghs)\s*\d+(?:,\d{3})*(?:\.\d{1,2})?` +
			`|\d+(?:,\d{3})*(?:\.\d{1,2})?\s*(?:gh[¢c]|ghs)`,
	)

	// Ghanaian phone numbers: 0XXXXXXXXX, +233XXXXXXXXX, and bare 233XXXXXXXXX
	// (the E.164 form without the plus, common in reference fields).
	reMSISDN = regexp.MustCompile(`\+233\d{9}\b|\b233\d{9}\b|\b0\d{9}\b`)

	// dates: ISO, slash/dash numeric, and month-name forms in either order.
	reDate = regexp.MustCompile(
		`(?i)\d{4}-\d{2}-\d{2}` +
			`|\d{1,2}[/\-]\d{1,2}[/\-]\d{2,4}` +
			`|\d{1,2}[\s\-](?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*[\s\-,]*\d{2,4}` +
			`|(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\s+\d{1,2},?\s+\d{2,4}`,
	)

	// times: HH:MM optionally with seconds and AM/PM.
	reTime = regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}(?::\d{2})?(?:\s*[ap]m)?\b`)

	// reference / transaction IDs: alphanumeric runs of 8+ chars. The digit
	// check in refMatch below rules out ordinary English words of that length
	// (Transaction, received, Available, Reference, MobileMoney) — a real
	// ref/txn ID from a bank virtually always contains at least one digit.
	reRef = regexp.MustCompile(`\b[A-Za-z0-9]{8,}\b`)

	// standalone digit runs left over after the above.
	reNum = regexp.MustCompile(`\b\d+\b`)

	// two-or-more consecutive words each starting uppercase. Word length >= 2
	// avoids catching lone initials. Matches both Title-case ("John Doe") and
	// ALL-CAPS ("JOHN DOE") because MoMo names arrive in both.
	reName = regexp.MustCompile(`\b[A-Z][A-Za-z]+(?:\s+[A-Z][A-Za-z]+)+\b`)

	reWhitespace = regexp.MustCompile(`\s+`)
)

// mask applies every substitution in the required order and returns the
// result unlowered and uncollapsed — callers do the final normalisation so
// tests can inspect intermediate state if useful.
func mask(body string) string {
	body = reAmount.ReplaceAllString(body, amtToken)
	body = reMSISDN.ReplaceAllString(body, msisdnToken)
	body = reDate.ReplaceAllString(body, dateToken)
	body = reTime.ReplaceAllString(body, timeToken)
	body = reRef.ReplaceAllStringFunc(body, refMatch)
	body = reNum.ReplaceAllString(body, numToken)
	body = reName.ReplaceAllString(body, nameToken)
	return body
}

// refMatch is a post-filter on reRef: only mask alphanumeric runs that
// actually contain a digit, so English template words survive intact.
func refMatch(m string) string {
	for i := 0; i < len(m); i++ {
		if m[i] >= '0' && m[i] <= '9' {
			return refToken
		}
	}
	return m
}

// Fingerprint returns the SHA-256 hex hash and the human-readable skeleton
// for an SMS. The skeleton is the same string that was hashed, so users can
// eyeball what the hash represents without ever seeing raw message bodies.
func Fingerprint(sender, body string) (hash string, skeleton string) {
	senderNorm := strings.ToUpper(strings.TrimSpace(sender))

	masked := mask(body)
	masked = reWhitespace.ReplaceAllString(masked, " ")
	masked = strings.ToLower(strings.TrimSpace(masked))

	skeleton = senderNorm + ": " + masked
	sum := sha256.Sum256([]byte(skeleton))
	return hex.EncodeToString(sum[:]), skeleton
}
