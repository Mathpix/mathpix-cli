# mpx — the Mathpix command-line interface

`mpx` is the Mathpix CLI, organized as `mpx <service> <command>`: each
Mathpix product is a service; its operations are the commands under it. Two services ship today:

- **`scs`** — the Mathpix OCR / document API on api.mathpix.com (sync `/v3` and the async Files API).
- **`pco`** — a Mathpix Private Cloud OCR deployment in your own infrastructure.

```bash
mpx configure                                  # store your app_id and app_key
mpx scs convert paper.pdf paper.mmd            # one document
mpx scs convert paper.pdf paper.docx           # request a conversion format
mpx scs convert ./in/ ./out/ --map pdf:docx,md:docx   # a folder
mpx scs convert report.pdf report.mmd --async --destination s3://acme-docs/out/   # Files API, output to your bucket
mpx scs convert equation.png equation.mmd      # an image (goes through /v3/text)
mpx scs jobs list                              # Files API batch jobs
mpx scs data-sources list                      # buckets registered for the Files API
mpx scs app-token                              # mint a short-lived client token
mpx scs results --pdf                          # past results
mpx scs usage --timespan day                   # usage for billing
mpx scs get PDF_ID                             # processing status
mpx scs download PDF_ID docx -o paper.docx     # fetch one format later

mpx pco --endpoint http://pco.internal:8080 convert paper.pdf paper.mmd   # a deployment
mpx pco --endpoint http://pco.internal:8080 status
mpx pco --endpoint http://pco.internal:8080 jobs list
```

Run `mpx <service> --help` and `mpx <service> <command> --help` for the full list of commands and
options. Webhooks are set per convert with `--webhook-url`, `--webhook-event` and `--webhook-header`,
and managed with `mpx scs webhooks`.

## Install

```bash
curl -fsSL https://mathpix.com/install.sh | sh
```

The script detects your OS and CPU, downloads the matching release, verifies its checksum, installs
`mpx` to `~/.local/bin`, and puts it on your `PATH`. Override the location with `MPX_INSTALL_DIR`; pin
a version with `MPX_VERSION`.

Prefer a package manager? `brew install mathpix/tap/mpx` or `winget install Mathpix.mpx`.

`mpx` is one static binary. You do not need Python, Node, Java, or Docker.

## Configuration

`mpx` reads credentials and settings from two INI files with named profiles.

- `mpx configure` prompts for your `app_id`, `app_key`, endpoint, default output and verbosity, and
  writes them to `~/.mpx/credentials` (secrets, 0600) and `~/.mpx/config`.
- `--profile NAME` (or `MPX_PROFILE`) selects a profile; anything not `default`.
- Every value resolves flag first, then environment, then the profile file:

| setting | flag | environment | file |
|---|---|---|---|
| app_id | `--app-id` | `MATHPIX_APP_ID` | credentials |
| app_key | `--app-key` | `MATHPIX_APP_KEY` | credentials |
| endpoint | `--endpoint` | `MPX_ENDPOINT` | config |
| output | `--output` | `MPX_OUTPUT` | config |
| verbosity | `--verbosity` | `MPX_VERBOSITY` | config |

The endpoint defaults to `https://api.mathpix.com`; point it at `https://eu.api.mathpix.com` for the
EU region or at a private deployment.

## Progress and verbosity

A `convert` that submits a document and polls for the result shows a live progress indicator on the
terminal: an animated spinner with a percent bar and page count as the pages are recognized. It is
drawn only on a terminal, so redirected output and logs stay clean.

`verbosity` is `normal` (the default, show the indicator) or `quiet` (hide it). Set it per command
with `--quiet` (shorthand for `--verbosity quiet`) or `--verbosity quiet`, for the session with
`MPX_VERBOSITY=quiet`, or permanently with `verbosity = quiet` in a profile (via `mpx configure`).

## `mpx scs convert`

- **Single file:** the output path's extension picks the format. `paper.pdf paper.docx` produces a
  Word file; `paper.pdf paper.mmd` produces Mathpix Markdown. A `.md`/`.mmd` source is converted
  through `/v3/converter`; any other document goes through `/v3/pdf`.
- **Folder:** `SRC` and `DST` are directories. `--map inputExt:format[+format]` (comma-separated)
  says which inputs to convert and to what: `--map pdf:docx,md:docx` or `--map pdf:docx+mmd,tiff:mmd`.
  Without `--map`, every supported document is converted to `mmd`. Files convert in parallel
  (`--concurrency`), one progress line each, with a report under `SRC/_mathpix/<run>/report.jsonl`.
  Outputs already present are skipped, so a re-run resumes.

Any API option without a dedicated flag is reachable with `--options-json`:

```bash
mpx scs convert paper.pdf paper.mmd --options-json '{"rm_fonts": true, "include_chemistry": true}'
```

**Bucket output and the Files API (`--async`).** `--async` submits through the Files API
(`/files/v1`) instead of `/v3/pdf`. Use it to write results straight to your own bucket with
`--destination`, or to submit from a registered data source:

```bash
mpx scs convert report.pdf report.mmd --async --destination s3://acme-docs/out/
mpx scs convert s3://acme-docs/in/report.pdf report.mmd --async
```

For many documents at once, use `mpx scs jobs` (the Files API batch endpoint, up to 200,000 in one
call). To convert a local folder, plain `convert` on a directory already runs in parallel.

**Webhooks.** Get notified when a document finishes, on any convert (sync or `--async`):

```bash
mpx scs convert paper.pdf paper.mmd \
  --webhook-url https://hooks.example.com/mathpix \
  --webhook-event file.completed --webhook-event file.error \
  --webhook-header 'Authorization=Bearer TOKEN'
```

## The rest of `mpx scs`

```bash
mpx scs get PDF_ID                              # a document's processing status (--output json for the raw object)
mpx scs download PDF_ID docx -o paper.docx      # fetch one format of a document you already submitted
mpx scs delete PDF_ID                           # permanently delete a document's outputs and input
```

**Batch jobs (Files API).** Convert many documents in one call:

```bash
mpx scs jobs create --uri s3://acme-docs/a.pdf --uri s3://acme-docs/b.pdf --formats docx
mpx scs jobs list
mpx scs jobs get JOB_ID
mpx scs jobs files JOB_ID --status error
mpx scs jobs finalize JOB_ID                    # close to new files so job.completed can fire
```

**Data sources.** Register a bucket so the Files API can read from (and write to) it:

```bash
mpx scs data-sources identities                 # Mathpix's grant identities + your external_id, for the bucket's trust policy
mpx scs data-sources register --config '{"provider":"s3","bucket":"acme-docs","region":"us-east-1","role_arn":"arn:aws:iam::..."}'
mpx scs data-sources list
mpx scs data-sources test ID
mpx scs data-sources delete ID
```

**Webhook signing secret.** Deliveries are signed; manage the secret you verify them against:

```bash
mpx scs webhooks config                         # show (creating on first call) the signing secret
mpx scs webhooks rotate-secret [--force]
mpx scs webhooks test https://hooks.example.com/mathpix   # send one signed test delivery
```

**Account.**

```bash
mpx scs app-token [--expires 300] [--strokes]   # mint a short-lived client token for direct v3/text calls
mpx scs results [--pdf]                          # past image results, or document results with --pdf
mpx scs usage --timespan day --group-by usage_type   # aggregated usage for billing
```

## The `pco` service (Private Cloud OCR)

`pco` drives a Mathpix Private Cloud OCR deployment. It speaks the same document API as `scs` plus
the deployment-only endpoints. Point `--endpoint` at your deployment; a deployment usually needs no
credentials, pass `--token` or the client-certificate flags if your ingress requires them.

```bash
mpx pco --endpoint http://pco.internal:8080 convert paper.pdf paper.mmd --formats docx
mpx pco --endpoint http://pco.internal:8080 convert s3://acme-docs/scans/ --formats md   # a folder job in your bucket
mpx pco --endpoint http://pco.internal:8080 status                 # versions, workers, license, metering
mpx pco --endpoint http://pco.internal:8080 usage --from 2026-09-01 --to 2026-09-30
mpx pco --endpoint http://pco.internal:8080 jobs list
mpx pco --endpoint http://pco.internal:8080 jobs get JOB_ID
mpx pco --endpoint http://pco.internal:8080 jobs retry JOB_ID
mpx pco --endpoint http://pco.internal:8080 jobs cancel JOB_ID
```

Transport flags on the `pco` service: `--token` (bearer), `--ca-cert`, `--client-cert`,
`--client-key`, `--insecure`. `--endpoint` can be set once with `mpx configure` (or `MPX_ENDPOINT`).

Every command has `--help`. Run `mpx <service> --help` and `mpx <service> <command> --help` for the
full list of flags.

## License

MIT. See `LICENSE`.
