# Releasing Holocron

A release is a GitHub Release of the repository `karadimnik/holocron`, made by the workflow `.github/workflows/release.yml` when a tag `vX.Y.Z` is pushed. Nothing else publishes: `build-holocron.sh` stays the developer build. The version rules are in [CHANGELOG.md](../CHANGELOG.md#versioning-policy); decision D24 in the [high-level design](../tasks/holocron/os-independent-hld.md) gives the reasons for the choices below.

**Who releases.** Every version merged to `main` is released; the Operator does not approve each one. Only the advisor cuts release tags, from `main`, after CI is green and `CHANGELOG.md` has the version's section, following this runbook. Executors and their sub-agents never create or push a tag or a release ([AGENTS.md](../AGENTS.md)).

## When a version is cut

Every merge to `main` that changes shipped behaviour gets a version by the [versioning policy](../CHANGELOG.md#versioning-policy). Shipped behaviour is anything that is not documentation, CI or tests only.

| The merge changes | Release |
| --- | --- |
| only documents, CI, tests or test data | none (its changelog lines, if any, wait under `[Unreleased]`) |
| a fix that changes no interface | patch, `X.Y.Z+1` |
| a compatible feature: a new command, flag, configuration key, `status.json` field or HTTP route | minor, `X.Y+1.0` |
| a breaking change to the configuration, the command line, the archive layout, `status.json` (`Schema`) or the HTTP API | major, `X+1.0.0` |

A merge that releases also releases whatever waited under `[Unreleased]`; the highest row of everything included decides the number.

## What a release contains

For version `X.Y.Z` (tag `vX.Y.Z`):

| Asset | What it is |
| --- | --- |
| `holocron_X.Y.Z_windows_amd64.zip` | `holocron.exe`, `LICENSE`, `README.md`, `docs/holocron-quickstart.md` |
| `holocron_X.Y.Z_linux_amd64.tar.gz`, `holocron_X.Y.Z_linux_arm64.tar.gz`, `holocron_X.Y.Z_darwin_arm64.tar.gz` | `holocron` and the same three documents |
| `holocron_X.Y.Z_<os>_<arch>.<ext>.sbom.json` | an SPDX 2.3 JSON software bill of materials of each archive, made by Syft |
| `checksums.txt` | the SHA-256 of every archive and SBOM, in `sha256sum` format |

Every archive and `checksums.txt` also has a build provenance attestation (SLSA provenance signed through Sigstore with the workflow's identity), stored by GitHub and checked with `gh attestation verify` (below). The release notes are the section of [CHANGELOG.md](../CHANGELOG.md) for the version. A tag with a suffix (`v1.1.0-rc.1`) makes a pre-release, which is not marked latest.

The binaries are built by GoReleaser from `.goreleaser.yaml`: `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, `-ldflags "-s -w -X main.version=vX.Y.Z -X holocron/internal/web.version=vX.Y.Z"`, file times set to the commit time, and archive entries with a fixed owner (`root`), mode and time. So `holocron version` prints the tag, as a `build-holocron.sh` build of the tagged commit does. Three snapshot builds of one commit on one machine gave byte-identical archives; the SBOMs differ in their `documentNamespace` (a random UUID SPDX requires to be unique) and `created` time, so their lines in `checksums.txt` differ too. Identical archives across machines are UNVERIFIED.

## Release checklist (the advisor)

1. **Decide the version** with the table above.
2. **Changelog.** On the commission's branch (or a branch of its own), move the entries under `## [Unreleased]` in `CHANGELOG.md` into a new section `## [X.Y.Z] - YYYY-MM-DD` (the release date), leave `## [Unreleased]` empty above it, and update the links at the bottom: `[Unreleased]` compares `vX.Y.Z...HEAD`, and a new `[X.Y.Z]` line compares the previous tag with `vX.Y.Z`. In the same commit, change every version in the README's [Download a release](../README.md#download-a-release) section (`VERSION=`, `$Version =`, the example tag and file name) to `X.Y.Z`: the README ships in every archive, and the Go test `TestReadmePinsReleasedVersion` (`cmd/holocron/releaseversion_test.go`) fails from the moment the changelog has the new section until the README names it, so neither change can go in alone.
3. **Merge** into `main` (`--no-ff`, recorded in [the ledger](../tasks/holocron/ledger.md)) and push `main`.
4. **CI green.** Wait for the [CI](ci.md) run of that `main` commit to pass, `release-check` included (it builds the same archives as a snapshot). Do not tag a red or unfinished run.
5. **Tag** the merge commit from an up-to-date `main`, with an annotated tag, and push only that tag:

   ```bash
   git switch main && git pull --ff-only
   git tag -a vX.Y.Z -m "Holocron vX.Y.Z"
   git push origin vX.Y.Z
   ```

6. **Wait for the `release` run** of the tag to finish green (Actions tab, or `gh run watch`).
7. **Verify the published release** as a user would: download one archive and `checksums.txt` from the release and run the two commands of [Verify a download](#verify-a-download); both must pass.
8. **Record** the release in [the ledger](../tasks/holocron/ledger.md): version, tag, commit, the `release` run, and the result of step 7.

## What the workflow does

One job, `release`, on `ubuntu-latest`. The workflow's token is read-only; only this job gets `contents: write` (create the release), `id-token: write` (sign the attestation) and `attestations: write` (store it). It needs no secret besides the built-in `GITHUB_TOKEN`, and every action is pinned to a commit SHA.

1. **Guard.** Fails unless the tag is `vMAJOR.MINOR.PATCH` with an optional pre-release suffix, the tagged commit is on `origin/main` (`git merge-base --is-ancestor`), and `CHANGELOG.md` has a non-empty `## [X.Y.Z]` section. That section becomes the release notes.
2. **Go** from `go.mod` (`setup-go`, `GOTOOLCHAIN=local`), and **Syft** for the SBOMs.
3. **GoReleaser** (a pinned version) builds the four targets, the archives, the SBOMs and `checksums.txt`, and uploads all of them with the notes to a **draft** release (`release.draft: true`).
4. **Attestation** (`actions/attest`) of the four archives and `checksums.txt`, from the files in `dist/`, while the release is still a draft.
5. **Publish**: `gh release edit vX.Y.Z --draft=false` (with `--latest` unless it is a pre-release). This is the last step, so nothing is public until every step passed.

This order is what [immutable releases](#repository-settings-one-time-the-operator) need: every asset is attached to the draft before the release is published, and publishing locks them.

## When a run fails

- **Before the publish step** the release is at most a draft that only the repository's writers see. If the cause was passing (a network error), re-run the failed job: GoReleaser replaces the draft (`replace_existing_draft`). If the fix needs a change to the repository, delete the draft, leave the tag, and release the fix as the next patch version with its own changelog section.
- **A guard failure** builds nothing. A tag that is not on `main` or has no changelog section is a mistake, and the tag ruleset (below) keeps it from being deleted or moved: skip that version number and tag the next one correctly. Only the Operator can remove such a tag, by setting the ruleset's enforcement to Disabled for the deletion and back to Active.

## Roll back a bad release

Never move or re-push a published tag: users and the attestation name its commit, and the ruleset forbids it.

1. Mark the release: edit its notes on GitHub to say what is wrong and which version replaces it, and mark its changelog heading `## [X.Y.Z] - YYYY-MM-DD [YANKED]`. If it must not be downloaded at all, delete the release (`gh release delete vX.Y.Z --repo karadimnik/holocron`); the tag stays.
2. Fix on `main` and publish a new patch version `X.Y.Z+1` whose changelog section says what was wrong.

## Verify a download

Download the archive and `checksums.txt` from the release into one folder. Then:

```bash
sha256sum --ignore-missing -c checksums.txt
gh attestation verify holocron_X.Y.Z_linux_amd64.tar.gz --repo karadimnik/holocron
```

The first checks the file against the published checksum; the second, with the GitHub CLI `gh`, checks that the file was built by this repository's workflow and is unchanged since. Whether `gh` must be signed in for a public repository is not stated on its manual page. On Windows without `sha256sum`, compare `(Get-FileHash .\holocron_X.Y.Z_windows_amd64.zip -Algorithm SHA256).Hash` with the line in `checksums.txt` (case does not matter). With immutable releases on, `gh release verify-asset vX.Y.Z <file> --repo karadimnik/holocron` also checks the file against GitHub's release attestation (UNVERIFIED here: no immutable release exists yet).

There is no separate cosign signature: the attestation is already a Sigstore signature over the archives and `checksums.txt` by the workflow's identity (D24).

## Repository settings (one time, the Operator)

- **Tag ruleset.** Import [`.github/rulesets/protect-release-tags.json`](../.github/rulesets/protect-release-tags.json): Settings, Rules, Rulesets, New ruleset, Import a ruleset, choose the file, review, Create. It targets `refs/tags/v*` with enforcement Active and no bypass actors, and restricts deletions, restricts updates and blocks force pushes, so a pushed release tag can be neither moved nor deleted. Creating tags stays allowed. Whether rulesets are available to this account's plan for a public repository is UNVERIFIED (the rules page does not say).
- **Release immutability.** Settings, Releases, "Enable release immutability" (GitHub's "Preventing changes to your releases"; the REST equivalent is `PUT /repos/karadimnik/holocron/immutable-releases`, admin access). A published release's assets and tag can no longer be changed, GitHub adds a release attestation, and a deleted release's tag name cannot be reused. It applies only to releases published after it is enabled.
- **Actions permissions**: leave the default workflow token read-only; the workflows ask for what they need per job.
- Branch protection is a separate decision and not part of this process.

## Limitations

- **Windows**: `holocron.exe` is not Authenticode-signed, so SmartScreen may warn when it is first run from a download.
- **macOS**: the binary is not signed or notarized, so Gatekeeper may refuse to open it from a download.
- **Linux and macOS** builds are compiled and tested in CI only; the live system runs on Windows.
- Reproducing a release elsewhere needs the Go version it was built with (`go version -m holocron` prints it) and the GoReleaser version pinned in `release.yml`. Rebuilding a published release and comparing checksums is UNVERIFIED: no release has been made by this pipeline yet.
