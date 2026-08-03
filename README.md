# blackwater

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
cmd/ledger/            cobra CLI (analyse, parse, generate, export, ingest, sum)
internal/fingerprint/  masking + hashing (heart of the design)
internal/corpus/       streaming XML + text + JSON-dir dump readers
internal/spec/         Spec type, Execute, Validate, JSON store, LLM Generate
internal/normalize/    raw captures → canonical Transaction shape
internal/store/        SQLite persistence with idempotent inserts
internal/group/        stub, milestone 4 (dedupe across paired SMS)
ios/                   iOS Share Extension + container app (see ios/README.md)
templates/             templates.json (community-contributed cache)
testdata/              synthetic fixtures — never real SMS
```

## Usage

```sh
# see how many distinct templates your dump contains
ledger analyse --input dump.xml

# ask Claude to write a parsing spec for every unseen fingerprint (needs API key)
ledger generate --input dump.xml

# run cached specs, list matches + unmatched fingerprints
ledger parse --input dump.xml

# export the normalised ledger as CSV for a spreadsheet
ledger export --input dump.xml --output ledger.csv

# persist to a local SQLite ledger DB (idempotent — safe to re-run)
ledger ingest --input dump.xml --db ledger.db

# per-direction totals, optionally bounded by date
ledger sum --db ledger.db
ledger sum --db ledger.db --since 2025-02-01 --until 2025-02-28

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

## Generating specs with Claude

`ledger generate` is the only command that touches the network. It walks
your dump, finds every fingerprint not yet in `templates/templates.json`,
picks one sample body per fingerprint, and asks Claude to produce a
regex-based Spec. The Spec is validated against the sample locally — if the
regex doesn't compile, doesn't match, or misses required fields, the tool
retries with the error fed back to the model. Only validated Specs are
cached.

```sh
export ANTHROPIC_API_KEY=sk-ant-...

# generate for every unseen fingerprint, tallest-frequency first
ledger generate --input dump.xml

# just try the top 5 templates (cost control)
ledger generate --input dump.xml --limit 5

# try one specific fingerprint (hash prefix works)
ledger generate --input dump.xml --fingerprint e4afd3

# see what it would do without persisting
ledger generate --input dump.xml --dry-run

# skip the pre-flight confirmation
ledger generate --input dump.xml --yes
```

**Model.** Defaults to `claude-haiku-4-5-20251001` — fast, cheap (~$1/$5
per Mtok), and easily strong enough for regex generation. Override with
`LEDGER_MODEL=claude-sonnet-4-6` if a template resists Haiku.

**Cost.** One API call per unseen fingerprint (plus retries — worst-case
3 calls). A 25-template dump costs ~$0.01 at Haiku prices. The system
prompt is marked for ephemeral cache, so a batch run pays for it once.

**Privacy.** Generating a spec requires sending Claude one real sample body
per template so it can construct the regex against ground truth. That
message is sent once per template, ever, then the cached Spec parses all
future messages of that shape locally. If you don't want *any* raw body to
leave your machine, don't run `generate` — the `analyse` and `parse`
commands never touch the network.

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

## Persistence

`ledger ingest` writes normalised transactions into a local SQLite file
(driver: `modernc.org/sqlite` — pure Go, no CGO). Idempotency is enforced
by hashing `(sender, body)` into `message_hash`; re-ingesting the same
dump is a no-op, not a source of duplicates.

Schema: one `transactions` table with `message_hash` UNIQUE, indexes on
`timestamp`, `direction`, `counterparty`, and `fingerprint`. Money is
stored as INTEGER pesewas for exact aggregation; timestamps are ISO-8601
UTC strings so ORDER BY works.

No app-level encryption in this milestone — rely on OS-level full-disk
encryption (Android, macOS FileVault, BitLocker) which is on by default
on modern devices. If you need at-rest encryption above that, wrap the DB
file with age or sops before syncing it anywhere.

## iOS client (POC)

The iOS side is a manual-share client: long-press an SMS in Messages →
Share → BlackWater → confirm sender → Save. The message becomes a JSON
file in an iCloud Drive folder that syncs to the Mac. `ledger ingest`
then treats the folder as its input.

Apple does not allow third-party apps to read SMS in the background at
all, on any framework. Share extensions are the ceiling on iOS. For a
personal-use POC this is fine — for a general-audience product, ship
Android instead.

See `ios/README.md` for the Xcode setup, sideload flow, and troubleshooting.

## Roadmap

- **Milestone 1 (done)** — fingerprinter, corpus reader, `analyse` + `parse` CLI.
- **Milestone 2 (done)** — LLM spec generator (`spec.Generate`) with
  validation loop, `ledger generate` CLI.
- **Milestone 3 (done)** — SQLite persistence, `ledger ingest`, `ledger sum`.
- **Milestone 4 (partial done)** — `internal/normalize` + `ledger export`;
  still pending: `internal/group` to dedupe paired debit/credit SMS.
- **iOS POC (done)** — Share Extension + iCloud Drive inbox; corpus
  reader accepts a directory of JSON files.
- **Milestone 5** — Android client (native Kotlin + gomobile bind).
- **Ongoing** — community-contributed specs via PRs to
  `templates/templates.json` (diff-friendly, stable ordering, no PII).
