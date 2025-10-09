# mdexec

Execute runnable code blocks from Markdown.

This README doubles as a demo: you can clone, build, and even use mdexec to rebuild itself from the fenced code blocks below. It’s a tiny “chicken-and-egg” showcase: once you have a binary, it can orchestrate building a fresh binary via Markdown.

## What is mdexec?

mdexec parses Markdown files, discovers runnable code fences, and lets you preview, plan, and execute them safely. It understands headings as a hierarchy, supports task metadata (name, deps, tags, dir, shell, timeout, continue), scores risk via simple policies, and can run tasks locally or inside a container.

### Features

- Discover and run code fences from Markdown (by tag or include-all)
- Task metadata: `name`, `deps`, `tags`, `dir`, `shell`, `timeout`, `continue`
- Build an execution plan honoring dependencies
- Policy-based risk scoring and allow levels
- Optional containerization (Docker/Podman) with sandbox defaults
- Interactive TUI with live tail and full-output modal (press `o`)
- Variables: replace `{{KEY}}` from `--var KEY=VALUE` or environment

### Supported languages

Out of the box (via simple runners):

- bash, sh
- python, node, deno, ruby, perl, php
- go (go run), pwsh (PowerShell), make (Makefile)

Whitelist is controlled by `policy/mdexec.policy.yaml`.

## TL;DR

- Clone the repo
- Build from source (Go 1.22+)
- Optional: install to your PATH
- Use mdexec to run tasks defined in Markdown files

```bash mdexec name=print-hello tags=demo
echo "Hello from mdexec!"
```

Tip: Files named `*.nomarker.md` are treated as include-all for whitelisted languages, so code fences do not need the `mdexec` marker in that mode.

## Fetch the repository

```bash mdexec name=clone-repo tags=setup
# Clone the repository (read-only)
git clone https://github.com/SimonWaldherr/mdexec.git
cd mdexec
```

If you already have the repository, you can update:

```bash mdexec name=update-repo tags=setup
# Update local checkout
git -C mdexec pull --ff-only || true
```

## Build the binary from source

```bash mdexec name=build-from-source tags=build deps=update-repo
echo "Building mdexec from source"
# Build a local binary at ./bin/mdexec
mkdir -p ./bin
go build -o ./bin/mdexec ./cmd/mdexec/main.go
# Show where the binary is
ls -alh ./bin/mdexec || true
```

Alternatively, install mdexec into your PATH using Go tools:

```bash mdexec name=go-install tags=build
# Installs into GOPATH/bin or GOBIN
go install github.com/SimonWaldherr/mdexec/cmd/mdexec@latest
```

## Run sample tasks

There are sample files under `samples/` demonstrating both mdexec-tagged and nomarker modes.

```bash mdexec name=list-sample tags=demo
# List tasks from the complex sample
./bin/mdexec list samples/README.complex.md || mdexec list samples/README.complex.md
```

```bash mdexec name=run-sample tags=demo deps=list-sample
# Run tasks (dry-run by default). Use --yes to execute.
./bin/mdexec run samples/README.complex.md || mdexec run samples/README.complex.md
```

## Usage

mdexec provides subcommands to list, show, plan, run, and a TUI for interactive use.

```
mdexec list <markdown>           # list runnable tasks
mdexec show <markdown> --name X  # print the code for task X
mdexec plan <markdown> [--graph out.dot]
mdexec run  <markdown> [--yes]   # by default dry-run; pass --yes to execute
mdexec tui  <markdown>           # interactive TUI (press r to run, o to open output)
mdexec config <markdown>         # print policy/config that would be used
```

Common filters and options:

- `--under "A/B"` limit to tasks under a heading path
- `--lang LANG`, `--tags a,b`, `--names a,b`, `--match REGEX`
- `--timeout SECS` set default timeout; `timeout=...` per task overrides
- `--include-all` consider all whitelisted code fences even without the `mdexec` tag
- `--tail N` control TUI tail line count

Working directory: tasks run from the directory of the markdown file by default. If a task sets `dir=...` and it’s relative, it’s resolved relative to the markdown file’s directory.

### Containerization

Run inside a container to avoid installing runtimes locally:

- `--use-container` to prefer containers
- `--engine docker|podman` and/or `--container <image>` to choose runtime/image
- `--pass-env KEY` passes selected environment variables through (default includes `CI`, `HOME`)

Defaults are in `policy/mdexec.policy.yaml` under `default_sandbox`.

### Policy & safety

See `policy/mdexec.policy.yaml` for whitelist and risk patterns. mdexec scores code and maps to risk levels (low/medium/high). Control allow level with `--allow low|medium|high`. If a task exceeds allowed risk, mdexec will block it unless you explicitly raise `--allow` (and pass `--yes` to execute).

### Variables

Use `--var KEY=VALUE` to set variables. mdexec replaces `{{KEY}}` in code and also exports them into the process environment. If a variable isn’t provided via `--var`, mdexec falls back to the current environment variable of the same name if set.

### Code fence metadata cheat sheet

Add `mdexec` to the code fence info string to mark it runnable, then add key/value pairs:

````
```bash mdexec name=task-a deps=task-b,t1 tags=setup,db dir="services/api" shell="/bin/bash" timeout=120 continue=true
# your script here
```
````

## Self-hosting demo: mdexec building mdexec

Once you have a working `./bin/mdexec` (or `mdexec` in PATH), you can use the tasks below to rebuild a fresh binary. This demonstrates mdexec orchestrating its own build steps via Markdown.

```bash mdexec name=self-clean tags=self
echo "Cleaning previous build artifacts"
rm -f ./bin/mdexec || true
```

```bash mdexec name=self-build tags=self deps=self-clean
# Build a fresh binary
./bin/mdexec list README.md > /dev/null || mdexec list README.md > /dev/null
GOFLAGS="" go build -o ./bin/mdexec ./cmd/mdexec/main.go
```

```bash mdexec name=self-test tags=self deps=self-build
# Quick smoke tests
./bin/mdexec --help || mdexec --help
./bin/mdexec list samples/README.complex.md | head -n 5 || mdexec list samples/README.complex.md | head -n 5
```

```bash mdexec name=self-install tags=self deps=self-build
# Optional: install to PATH
GOBIN=$(go env GOPATH)/bin go install ./cmd/mdexec
command -v mdexec
```

## UI/TUI

Try the interactive TUI (press 'r' to run, 'o' to open full output):

```bash mdexec name=tui-demo tags=ui
./bin/mdexec tui samples/README.complex.md || mdexec tui samples/README.complex.md
```

## Policy and safety

See `policy/mdexec.policy.yaml` for default language whitelist and risk patterns. Use `--allow` to raise allowed risk level and `--yes` to execute (otherwise dry-run).

```bash mdexec name=policy-show tags=policy
./bin/mdexec config samples/README.complex.md || mdexec config samples/README.complex.md
```

## Notes

- Requires Go 1.22+ to build.
- By default, `run` is dry-run unless you pass `--yes`.
- For files named `*.nomarker.md`, mdexec automatically treats all whitelisted code fences as runnable.
- Tasks run by default from the directory of the markdown file, so relative paths are resolved as you’d expect.

## Troubleshooting

- "Missing runtime" warnings: either install the runtime (e.g., `python3`) or run with `--use-container`.
- Nothing shows in TUI output for long logs: use `o` to open the full output modal. Adjust `--tail N` for inline tail.
- Tasks aren’t found: ensure your code fences include `mdexec` (unless using `--include-all` or a `*.nomarker.md` file), and that the language is whitelisted in policy.
- Blocked by policy: raise `--allow` to a higher level or adjust the policy file if appropriate.

## Contributing

Issues and PRs are welcome. Please include a minimal repro Markdown when filing bugs. For significant changes, discuss via an issue first. Running `go build -o ./bin/mdexec ./cmd/mdexec/main.go` should succeed; aim to keep the linter and tests green.

## License

This project is licensed under the terms of the LICENSE file in this repository.
