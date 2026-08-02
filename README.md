# momo-ledger

Offline-first expense tracker that turns Ghanaian mobile-money and bank SMS
into a structured transaction ledger. Milestone 1 covers the parsing
foundation: a fingerprinter and a diagnostic CLI. LLM-driven spec
generation, Postgres storage, and the Android client land in later
milestones.

## Design in one paragraph

The design deliberately avoids calling an LLM per message. Each SMS is
normalised into a **fingerprint** — variable tokens (amounts, phone numbers,
transaction IDs, names, dates, times) are masked and the remaining
"skeleton" is SHA-256 hashed. Two messages from the same bank template
produce the same fingerprint. That hash keys a cached parsing **Spec** — a
declarative regex with named capture groups. At runtime, parsing is pure
Go, deterministic, and never leaks message bodies. The LLM only ever sees
skeletons (no PII) and only for fingerprints not yet in the cache.

## Packages

```
cmd/ledger/            cobra CLI (analyse, parse)
internal/fingerprint/  masking + hashing (heart of the design)
internal/corpus/       streaming XML + text dump readers
internal/spec/         Spec type, Execute, Validate, JSON store
internal/normalize/    stub, milestone 4
internal/group/        stub, milestone 4
templates/             templates.json (community-contributed cache)
testdata/              synthetic fixtures — never real SMS
```

## Usage

```sh
# see how many distinct templates your dump contains
ledger analyse --input dump.xml

# run cached specs, list matches + unmatched fingerprints
ledger parse --input dump.xml

# override the sender allowlist
ledger analyse --input dump.xml --allowlist MobileMoney,Ecobank
```

Both commands auto-detect the input format:
- **SMS Backup & Restore XML** — `<smses><sms address=... body=... /></smses>`
- **Plain-text 3-line-block** — date line / sender / body / blank

The `analyse` command is the diagnostic view. It groups messages by
fingerprint, sorts by frequency, shows whether each has a cached Spec, and
reports coverage. Use it to decide which templates are worth writing Specs
for (highest count = biggest payoff).

## Exporting SMS

**Android (recommended)** — install
[SMS Backup & Restore](https://play.google.com/store/apps/details?id=com.riteshsahu.SMSBackupRestore).
Open it, Backup → include SMS only, choose "Backup to local device", copy
the resulting XML file off the phone (USB or cloud). Feed it to `ledger
analyse --input <file>`.

**Plain-text exports** from other backup tools also work — the CLI
auto-detects them.

## Development

```sh
make test          # unit + fixture tests
make lint          # go vet + gofmt check
make build         # binary at bin/ledger
make analyse INPUT=/path/to/dump.xml
```

## Handling real data

- **Never commit real SMS.** `testdata/*.xml` and `*.dump` are gitignored.
  Real dumps stay outside the repo (or in `/tmp`).
- **Fixtures are synthetic.** `testdata/fixtures.json` contains obviously
  fake names (`JOHN DOE`), zeroed phones (`0244000000`), and dummy
  transaction IDs. The templates match real-world MoMo shapes.
- **Skeletons are PII-free by construction.** Every regex in
  `internal/fingerprint` is asserted against a leak check
  (`TestSkeletonPIIFree`). If you extend the masking rules, extend that
  test too.

## Design limitations (known)

The fingerprint rules trade absolute precision for simplicity. Known
over/under-masking:

- **Titlecase-pair words like `Transaction Id` get NAME-masked.** Doesn't
  hurt determinism (same template still collides) but makes skeletons
  chattier. Fixable with a template-word allowlist if it becomes
  annoying.
- **Single-letter reference codes (`Reference: Q` vs `Reference: W`) cause
  over-splitting.** Two messages that differ only by that letter fingerprint
  differently. Punted to a later pass — accept slightly more templates in
  the cache for now.
- **Business names in Titlecase (e.g. `Ecobank Ghana`) get masked as
  `<NAME>`.** Same reasoning as above; the rule spec asked for Title-case
  pair masking, and disambiguating merchant vs. human names is out of
  scope for milestone 1.

## Roadmap

- **Milestone 2** — LLM spec generator (`spec.Generate`) with validation
  loop, community templates PRs.
- **Milestone 3** — Postgres persistence, per-user encryption.
- **Milestone 4** — normalisation + dedupe (`internal/normalize`, `internal/group`).
- **Milestone 5** — Android client.
