# Release Process

Releases are made by merging the release PR. setup-browserbase is published to
`functions.circleci.com`, and each release gets a
[GitHub release](https://github.com/circleci-functions/setup-browserbase/releases) with its notes.

## How it works

On every push to main, after the tests pass:

- **The `release-pr` job** runs `go tool release-pr update`. Once the newest version in
  `CHANGELOG.md` is tagged, it opens or updates the "Release vX.Y.Z" PR from the
  `release/next` branch, which adds a section for the next version to `CHANGELOG.md` with
  GitHub's notes on the PRs merged since the last release. With none merged, the PR is closed.
- **The `publish` job** halts unless the newest version in `CHANGELOG.md` isn't tagged yet,
  that is, unless this push merged its release PR. Then it makes the GitHub release and its
  tag, and publishes that version.

Each changelog entry is a merged PR's title, so write titles plainly, the way commit subjects
are written.

## The version

The version is the newest `## [x.y.z]` heading in `CHANGELOG.md`, published as plain semver
(`v0.2.0`).

A release is a minor bump (0.1.0 → 0.2.0), unless a PR in it has one of these labels:

- `release:major`: a major bump (1.0.0)
- `release:patch`: a patch bump (0.1.1), if every PR in the release has it

Label the PR before it's merged, or relabel it and rerun the latest main build's `release-pr`
job.

Don't edit the release PR's branch: it's rewritten on every push to main.

## Credentials

| Context | Variable | For |
|---|---|---|
| `github-release` | `GITHUB_TOKEN` | The release PR, the GitHub release and its tag |
| `functions-publisher` | `FUNCTIONS_PUBLISHER_ROLE_ARN` | Publishing to the functions bucket |

## If a release fails part way

Rerun the `publish` job. It carries on with a version tagged on the commit it's building, and
the release refuses a version that's already published, so a rerun can't replace one. If it
stops for that reason, the binaries are out and only `function.yaml` or the `release.txt`
pointer is missing: upload those by hand.
