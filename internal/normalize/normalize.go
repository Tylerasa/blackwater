// Package normalize will canonicalise parsed transactions into a common shape
// (currency in minor units, ISO timestamps, resolved counterparties).
//
// TODO(milestone 4): implement once the spec executor stabilises. The API will
// take a raw map[string]string from spec.Execute and return a Transaction
// struct that group.Group can dedupe on.
package normalize
