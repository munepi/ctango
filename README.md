# ctango — CTAN, go!

`ctan` is a command-line tool for submitting packages to
[CTAN](https://ctan.org/), in the spirit of `gh` for GitHub.
It packs the archive and the package metadata into a request to the
[CTAN submission API](https://ctan.org/help/submit), so that an upload
no longer requires filling in the web form at <https://ctan.org/upload>.

```console
$ ctan init                 # create ctan.toml (pre-filled from CTAN if the package exists)
$ make ctanzip              # build the archive with your own release process
$ ctan validate             # let CTAN check the submission; nothing is stored
$ ctan upload               # check, validate, confirm, and submit
```

## Installation

```sh
go install github.com/munepi/ctango/cmd/ctan@latest
```

or build from a checkout:

```sh
go build -o ctan ./cmd/ctan
```

## Commands

| Command | Description |
|---|---|
| `ctan init [<pkg>]` | Write `ctan.toml`. If the package is on CTAN, summary, description, authors, license, topics, CTAN path and URLs are taken from the CTAN JSON API. |
| `ctan validate [<archive>]` | Send the submission to `/submit/validate` and show the result. Nothing is stored or published. |
| `ctan upload [<archive>]` | Local checks → `/submit/validate` → summary and confirmation → `/submit/upload`. |
| `ctan fields` | Show the fields the submission API accepts. |
| `ctan pkg view <pkg>` | Show what CTAN currently knows about a package. |

### Flags of `validate` and `upload`

| Flag | Description |
|---|---|
| `-c, --config <file>` | `ctan.toml` to use (default: search the current directory and its parents) |
| `--version <v>` | Package version (default: from `ctan.toml`) |
| `--update auto\|true\|false` | Whether the package already exists on CTAN. `auto` (default) asks the CTAN JSON API. |
| `-a, --announcement <text>` | Announcement for the ctan-ann mailing list. `-a ""` means no announcement. |
| `-A, --announcement-file <file>` | Read the announcement from a file (`-` for standard input) |
| `--note <text>`, `--note-file <file>` | Private note to the CTAN upload managers |
| `--uploader <name>`, `--email <addr>` | Uploader (default: see below) |
| `-n, --dry-run` | Run the local checks and print the fields that would be sent; nothing is sent to the submission service |
| `--json` | Print the server response (with `--dry-run`: the fields) as JSON on standard output |
| `--skip-checks` | Skip the local checks |
| `-y, --yes` | (upload) Do not ask for confirmation |
| `--force` | (upload) Upload even if validation reports errors. Requires `--note` explaining why; see “Forcing an invalid upload” in the API documentation and talk to the CTAN team first. |

When `upload` runs in a terminal and no announcement is given, an editor
(`$CTAN_EDITOR`, `$VISUAL`, `$EDITOR`, or `vi`) is opened for it. Lines
starting with `#` are ignored; an empty text means no announcement.

Without a terminal, `upload` refuses to submit unless `--yes` is given.

## ctan.toml

Keys are named after the fields of the submission API. Values that may
occur several times (`author`, `license`, `topic` and the URL fields)
accept either a string or an array of strings.

```toml
pkg         = "spotxcolor"
ctanPath    = "/macros/latex/contrib/spotxcolor"
summary     = "Modern spot color support for the xcolor package"
description = '''
<p>This package provides robust spot color support for xcolor ...</p>
'''
author      = ["Munehiro Yamamoto"]
license     = ["lppl1.3c"]
topic       = ["colour"]
bugtracker  = "https://github.com/munepi/spotxcolor/issues"
repository  = "https://github.com/munepi/spotxcolor"
archive     = "spotxcolor.zip"     # relative to ctan.toml

[version]
file = "spotxcolor.sty"
# pattern = '...'   # optional regular expression; the first group is the version
```

| Key | API field | Notes |
|---|---|---|
| `pkg` | `pkg` | required |
| `version` | `version` | a string, or a `[version]` table with `file` and optional `pattern`. Without a pattern, `\ProvidesExplPackage{..}{..}{1.8}` and `\ProvidesPackage{..}[date v1.8 ..]` (also `Class`/`File`) are recognized. |
| `author` | `author` | required; several authors are joined with `; ` |
| `ctanPath` | `ctanPath` | required; path below `/tex-archive` |
| `summary` | `summary` | required; one line, ≤ 128 characters |
| `description` | `description` | required; HTML allowed, ≤ 4096 characters |
| `license`, `topic` | same | see <https://ctan.org/license> and <https://ctan.org/topics/cloud> |
| `home`, `bugtracker`, `support`, `repository`, `development` | same | URLs |
| `announcement`, `note` | same | usually given per release with flags instead |
| `uploader`, `email` | same | usually taken from the user configuration instead |
| `archive` | `file` | `.zip`, `.tgz` or `.tar.gz`; overridden by the command argument |

Unknown keys are errors, so typos do not silently drop a value.

### Uploader and email

They are taken from the first of: the `--uploader`/`--email` flags, the
`CTAN_UPLOADER`/`CTAN_EMAIL` environment variables, `ctan.toml`, the
user configuration, and `git config user.name`/`user.email`.

The user configuration is `$CTAN_CONFIG`, `$XDG_CONFIG_HOME/ctan/config.toml`
or `~/.config/ctan/config.toml`:

```toml
[user]
name  = "Jane Doe"
email = "jane@example.org"
```

## Local checks

Before anything is sent, `ctan` checks

* mandatory fields, field lengths, the package name, URLs and the email address;
* that the version differs from the one currently on CTAN, and that
  `ctanPath` matches the current CTAN path (`--update auto` only);
* the archive: a single top-level directory named after the package, a
  README in it, PDF documentation, no generated files (`.aux`, `.log`,
  …, backup files `*~`), no hidden or system files (`.git`, `.DS_Store`,
  `__MACOSX`), and that the archive is not older than a day.

Errors stop the command; warnings are only reported. `--skip-checks`
disables these checks; CTAN still validates the submission.

## Environment

| Variable | Meaning |
|---|---|
| `CTAN_UPLOADER`, `CTAN_EMAIL` | Uploader name and email |
| `CTAN_CONFIG` | Path of the user configuration |
| `CTAN_EDITOR` | Editor for the announcement |
| `CTAN_SUBMIT_URL` | Submission endpoint (default `https://www.ctan.org/submit`), e.g. for a test server |
| `CTAN_API_URL` | JSON API endpoint (default `https://ctan.org/json/2.0`) |
| `NO_COLOR` | Disable colors |

## Exit status

`0` on success, `1` if a check, the validation or the upload failed, or
the upload was cancelled.
