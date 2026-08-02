// Package group will collapse duplicate transactions that appear across
// multiple SMS (e.g. an MTN debit + an Ecobank credit for the same transfer)
// into a single ledger entry.
//
// TODO(milestone 4): implement once normalize is in place. Grouping keys will
// come from normalize.Transaction (amount + timestamp window + counterparty).
package group
