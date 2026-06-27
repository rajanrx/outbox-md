# Contributing to outbox-md

Thanks for your interest! This project is in early development.

## Developer Certificate of Origin (DCO)

Every commit must be signed off, certifying you wrote the code or have the right to submit it under the project's license:

```bash
git commit -s -m "feat: your change"
```

This appends a `Signed-off-by: Your Name <you@example.com>` trailer. PRs without sign-off cannot be merged.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`, `build:`.

## Running the project

**Backend (Go ≥ 1.23, no CGO):**
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
