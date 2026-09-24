# Contributing to Tether

Thanks for helping improve Tether. Contributions to code, tests, documentation,
and bug reports are all welcome.

## Before you start

- Search the [existing issues](https://github.com/kindmanners/tether/issues) before opening a new one.
- For a bug, include the Tether revision, Go version, operating system, expected
  and actual behaviour, and a small set of reproducible steps.
- Describe feature proposals in an issue before beginning a substantial change,
  so that the proposed behaviour and scope can be discussed.
- Do not post security vulnerabilities or sensitive data in a public issue.
  Report them privately to [maxiefeseymen@gmail.com](mailto:maxiefeseymen@gmail.com).

## Development setup

Tether is a Go project. The required Go toolchain is defined in
[`go.mod`](go.mod); at present it is Go 1.27. Install Git and that Go version,
then fork and clone the repository:

```bash
git clone https://github.com/<your-account>/tether.git
cd tether
git checkout -b fix/short-description
go mod download
```

Use a focused branch name such as `fix/...`, `feat/...`, `docs/...`, or
`refactor/...`. Keep generated binaries out of commits; `bin/` is ignored.

## Build and test

Run these from the repository root before opening a pull request:

```bash
# Format tracked Go source files (Git Bash, macOS, or Linux)
gofmt -w $(git ls-files '*.go')

# Run the test suite and ensure every package builds.
go test ./...
go build ./...
```

On PowerShell, use this formatting command instead:

```powershell
gofmt -w (git ls-files '*.go')
```

Use the race detector for changes involving concurrency, networking, pairing,
or process lifecycle:

```bash
go test -race ./...
```

There is no repository-managed linter configuration. You may run additional
linters locally, but the commands above are the portable project checks.

### Building runnable programs

`go build ./...` is a compilation check. To create named local binaries, use:

```bash
go build -tags production -o bin/tether ./cmd/tether
go build -tags production -o bin/tether-agent ./cmd/tether-agent
go build -o bin/tether-api ./cmd/tether-api
go build -o bin/tether-dashboard ./cmd/tether-dashboard
```

On Windows, build the desktop release applications and API gateway with:

```powershell
.\scripts\build-windows-release.ps1
```

This produces `tether.exe`, `tether-agent.exe`, and `tether-api.exe` in
`bin/`. Keep `tether-api.exe` beside `tether.exe` when packaging an
Orchestrator installation. See the [README](README.md) for runtime setup,
including llama.cpp RPC and GPU-node prerequisites.

## Code and documentation

- Follow standard Go conventions and let `gofmt` make formatting decisions.
- Add or update tests when a change affects behaviour. Tests live beside the
  relevant packages under `internal/` and `cmd/`.
- Keep changes small and scoped. Avoid unrelated reformatting or generated
  binary changes in the same pull request.
- Update the README or inline Go documentation when a user-facing command,
  configuration field, pairing flow, or API behaviour changes. Put longer
  operator and integration guides in `docs/`.

## Pull requests

1. Rebase or merge the current target branch as appropriate, and resolve any
   conflicts locally.
2. Include a clear summary of the problem and solution, plus the checks you
   ran.
3. Link the related issue when there is one. For UI or user-visible behaviour,
   include screenshots or concise reproduction/verification notes where useful.
4. Respond to review feedback with follow-up commits or an explanation of the
   trade-off.

## Commit messages

Recent project history follows [Conventional Commits](https://www.conventionalcommits.org/).
Use an imperative, concise subject with an optional scope:

```text
fix(windows): harden local GPU gateway startup
docs: clarify Windows release build
```

Useful types include `feat`, `fix`, `docs`, `refactor`, `test`, `build`, `ci`,
and `chore`. Explain motivation and compatibility impact in the body when the
subject alone is not enough. Mark breaking changes with `!` or a
`BREAKING CHANGE:` footer.

## License

By contributing, you confirm that you have the right to submit the work and
agree to license your contribution under the repository's
[GNU Affero General Public License v3.0](LICENSE).
