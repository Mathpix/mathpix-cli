# mpx — the Mathpix command-line interface

`mpx` is the Mathpix CLI, organized as `mpx <service> <command>` the way the AWS CLI is. Each
Mathpix product is a service; its operations are the commands under it. Two services ship today:

- **`scs`** — the Mathpix OCR / document API on api.mathpix.com (sync `/v3` and the async Files API).
- **`pco`** — a Mathpix Private Cloud OCR deployment in your own infrastructure.

```bash
mpx configure                                  # store your app_id and app_key
mpx scs convert paper.pdf paper.mmd            # one document
mpx scs convert paper.pdf paper.docx           # request a conversion format
mpx scs convert ./in/ ./out/ --map pdf:docx,md:docx   # a folder
mpx scs convert big.pdf big.mmd --async        # via the Files API (large files, bucket output)
mpx scs text equation.png                      # one image, synchronous
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

**Quick install (macOS, Linux):**

```bash
curl -fsSL https://mathpix.com/install.sh | sh
```

The script detects your OS and CPU, downloads the matching release, verifies its checksum, and puts
`mpx` in `~/.local/bin`. Pin a version with `MPX_VERSION`, change the target with `MPX_INSTALL_DIR`.

**Homebrew:**

```bash
brew install mathpix/tap/mpx
```

**Direct download:** grab the archive for your platform from the releases page and put `mpx` on your
`PATH`. On Windows, use the `.exe` from the releases page or `winget install Mathpix.mpx`.

`mpx` is one static binary. You do not need Python, Node, Java, or Docker.

## Configuration

`mpx` reads credentials and settings the way the AWS CLI does: two INI files with named profiles.

- `mpx configure` prompts for your `app_id`, `app_key`, endpoint and default output, and writes them
  to `~/.mpx/credentials` (secrets, 0600) and `~/.mpx/config`.
- `--profile NAME` (or `MPX_PROFILE`) selects a profile; anything not `default`.
- Every value resolves flag first, then environment, then the profile file:

| setting | flag | environment | file |
|---|---|---|---|
| app_id | `--app-id` | `MATHPIX_APP_ID` | credentials |
| app_key | `--app-key` | `MATHPIX_APP_KEY` | credentials |
| endpoint | `--endpoint` | `MPX_ENDPOINT` | config |
| output | `--output` | `MPX_OUTPUT` | config |

The endpoint defaults to `https://api.mathpix.com`; point it at `https://eu.api.mathpix.com` for the
EU region or at a private deployment.

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

## License

MIT. See `LICENSE`.
