// Package spec defines the declarative message-parsing spec, an executor that
// runs a spec against an SMS body, and the JSON store used to cache specs
// keyed by fingerprint.
//
// A Spec is what an LLM produces once per fingerprint (see Generate) and what
// the runtime applies deterministically thereafter. LLM output that fails
// Validate must never reach the cache.
package spec

import (
	"errors"
	"fmt"
	"regexp"
)

// Direction describes what a message says happened to the account balance.
type Direction string

const (
	DirectionDebit   Direction = "debit"
	DirectionCredit  Direction = "credit"
	DirectionFee     Direction = "fee"
	DirectionBalance Direction = "balance"
)

// Kind is the semantic category of a captured field. It tells downstream
// normalisation how to interpret the raw string.
type Kind string

const (
	KindAmount       Kind = "amount"
	KindCurrency     Kind = "currency"
	KindReference    Kind = "reference"
	KindMSISDN       Kind = "msisdn"
	KindCounterparty Kind = "counterparty"
	KindDate         Kind = "date"
	KindTime         Kind = "time"
	KindBalance      Kind = "balance"
	KindFee          Kind = "fee"
	KindOther        Kind = "other"
)

// Field describes one named capture group.
type Field struct {
	Kind     Kind   `json:"kind"`
	Optional bool   `json:"optional,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Spec is a declarative message template. Pattern is a Go regexp with named
// capture groups; every non-optional key in Fields must appear as a group
// name in Pattern.
type Spec struct {
	Fingerprint string           `json:"fingerprint"`
	Sender      string           `json:"sender"`
	Pattern     string           `json:"pattern"`
	Fields      map[string]Field `json:"fields"`
	Direction   Direction        `json:"direction"`
	Version     int              `json:"version"`
}

// ErrNotImplemented is returned by Generate until the LLM path lands.
var ErrNotImplemented = errors.New("spec: Generate not implemented (milestone 3)")

// ErrNoMatch means the spec's pattern did not match the body at all.
var ErrNoMatch = errors.New("spec: pattern did not match body")

// Execute compiles the spec's pattern and returns the named captures for the
// given SMS body. Group names not declared in Fields are still returned; the
// caller can decide whether that's a problem.
func Execute(s Spec, body string) (map[string]string, error) {
	re, err := regexp.Compile(s.Pattern)
	if err != nil {
		return nil, fmt.Errorf("spec %s: compile pattern: %w", s.Fingerprint, err)
	}
	m := re.FindStringSubmatch(body)
	if m == nil {
		return nil, ErrNoMatch
	}
	out := make(map[string]string, len(re.SubexpNames()))
	for i, name := range re.SubexpNames() {
		if i == 0 || name == "" {
			continue
		}
		out[name] = m[i]
	}
	return out, nil
}

// Validate asserts the spec compiles, matches the sample, and extracts a
// non-empty value for every required field. A spec that fails validation
// must never be cached.
func Validate(s Spec, sample string) error {
	if s.Pattern == "" {
		return errors.New("spec: empty pattern")
	}
	re, err := regexp.Compile(s.Pattern)
	if err != nil {
		return fmt.Errorf("spec: compile pattern: %w", err)
	}
	declared := map[string]bool{}
	for _, name := range re.SubexpNames() {
		if name != "" {
			declared[name] = true
		}
	}
	for name, f := range s.Fields {
		if !f.Optional && !declared[name] {
			return fmt.Errorf("spec: required field %q has no capture group in pattern", name)
		}
	}
	got, err := Execute(s, sample)
	if err != nil {
		return fmt.Errorf("spec: sample did not match: %w", err)
	}
	for name, f := range s.Fields {
		if f.Optional {
			continue
		}
		if got[name] == "" {
			return fmt.Errorf("spec: required field %q extracted empty from sample", name)
		}
	}
	return nil
}

// Generate is the LLM-backed spec generator. Stubbed until milestone 3.
func Generate(skeleton, sample string) (Spec, error) {
	return Spec{}, ErrNotImplemented
}
