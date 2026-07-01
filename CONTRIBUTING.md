# Contributing to outbox-md

Thanks for your interest! This project is in early development.

## Developer Certificate of Origin (DCO)

Every commit must be signed off, certifying you wrote the code or have the right to submit it under the project's license:

```bash
git commit -s -m "feat: your change"
```

This appends a `Signed-off-by: Your Name <you@example.com>` trailer. PRs without sign-off cannot be merged.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`, `build:`. These drive automated releases (below), so the prefix matters:

| Prefix | Version effect |
|---|---|
| `fix: …` | patch (`0.1.1` → `0.1.2`) |
| `feat: …` | minor (`0.1.1` → `0.2.0`) |
| `feat!: …` or a `BREAKING CHANGE:` footer | major |
| `docs:` `chore:` `ci:` `test:` `refactor:` `build:` | no release on their own (still listed in the changelog) |

## Releases

Releases are automated with [release-please](https://github.com/googleapis/release-please) — no manual tagging or hand-written notes:

1. Merge PRs to `main` with conventional-commit messages.
2. release-please opens/updates a rolling **"Release PR"** that bumps the version and updates `CHANGELOG.md`.
3. Merge the Release PR → it tags `vX.Y.Z`, creates a GitHub Release, and the same workflow builds + pushes the multi-arch image to `rajanrauniyar/outbox-md` (`:X.Y.Z`, `:X.Y`, `:latest`).

A manual one-off publish is still available via the **docker-publish** workflow's *Run workflow* button, or by pushing a `v*` tag yourself.

## Running the project

**Backend (Go ≥ 1.25, no CGO):**
```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./...
go vet ./...
```

**Frontend:**
```bash
npm --prefix web ci
npm --prefix web run build
npm --prefix web test
```

## Pull requests

- Keep PRs focused and small.
- Include tests for behavior changes.
- Ensure CI is green and commits are signed off.
- A maintainer reviews and merges; please do not merge your own PRs.
