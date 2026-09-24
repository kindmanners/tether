# Contributing to Tether

First off, thanks for taking the time to contribute! ❤️

All types of contributions are encouraged and valued. See the [Table of Contents](#table-of-contents) for different ways to help and details about how this project handles them. Please make sure to read the relevant section before making your contribution. It will make it a lot easier for us maintainers and smooth out the experience for all involved. The community looks forward to your contributions. 🎉

> And if you like the project, but just don't have time to contribute, that's fine. There are other easy ways to support the project and show your appreciation, which we would also be very happy about:
>
> - Star the project
> - Tweet about it
> - Refer this project in your project's readme
> - Mention the project at local meetups and tell your friends/colleagues

<!-- omit in toc -->
## Table of Contents

- [I Have a Question](#i-have-a-question)
  - [I Want To Contribute](#i-want-to-contribute)
  - [Reporting Bugs](#reporting-bugs)
  - [Suggesting Enhancements](#suggesting-enhancements)
  - [Your First Code Contribution](#your-first-code-contribution)
  - [Improving The Documentation](#improving-the-documentation)
- [Styleguides](#styleguides)
  - [Commit Messages](#commit-messages)

## I Have a Question

<!-- Add documentation links here when available. -->

Before you ask a question, it is best to search for existing [Issues](https://github.com/kindmanners/tether/issues) that might help you. In case you have found a suitable issue and still need clarification, you can write your question in this issue. It is also advisable to search the internet for answers first.

If you then still feel the need to ask a question and need clarification, we recommend the following:

- Open an [Issue](https://github.com/kindmanners/tether/issues/new).
- Provide as much context as you can about what you're running into.
- Provide project and platform versions (nodejs, npm, etc), depending on what seems relevant.

We will then take care of the issue as soon as possible.

<!--
You might want to create a separate issue tag for questions and include it in this description. People should then tag their issues accordingly.

Depending on how large the project is, you may want to outsource the questioning, e.g. to Stack Overflow or Gitter. You may add additional contact and information possibilities:
- IRC
- Slack
- Gitter
- Stack Overflow tag
- Blog
- FAQ
- Roadmap
- E-Mail List
- Forum
-->

## I Want To Contribute

> **Legal Notice**
>
> When contributing to this project, you must agree that you have authored 100% of the content, that you have the necessary rights to the content and that the content you contribute may be provided under the project licence.

### Reporting Bugs

<!-- omit in toc -->
#### Before Submitting a Bug Report

A good bug report shouldn't leave others needing to chase you up for more information. Therefore, we ask you to investigate carefully, collect information and describe the issue in detail in your report. Please complete the following steps in advance to help us fix any potential bug as fast as possible.

- Make sure that you are using the latest version.
- Determine if your bug is really a bug and not an error on your side e.g. using incompatible environment components/versions (make sure that you have read the [documentation](README.md). If you are looking for support, you might want to check [this section](#i-have-a-question)).
- To see if other users have experienced (and potentially already solved) the same issue you are having, check if there is not already a bug report existing for your bug or error in the [bug tracker](https://github.com/kindmanners/tether/issues?q=label%3Abug).
- Also make sure to search the internet (including Stack Overflow) to see if users outside of the GitHub community have discussed the issue.
- Collect information about the bug:
  - Stack trace (Traceback)
  - OS, Platform and Version (Windows, Linux, macOS, x86, ARM)
  - Version of the interpreter, compiler, SDK, runtime environment, package manager, depending on what seems relevant.
  - Possibly your input and the output
  - Can you reliably reproduce the issue? And can you also reproduce it with older versions?

<!-- omit in toc -->
#### How Do I Submit a Good Bug Report?

> You must never report security related issues, vulnerabilities or bugs including sensitive information to the issue tracker, or elsewhere in public. Instead sensitive bugs must be sent by email to [maxiefeseymen@gmail.com](mailto:maxiefeseymen@gmail.com).
<!-- You may add a PGP key to allow the messages to be sent encrypted as well. -->

We use GitHub issues to track bugs and errors. If you run into an issue with the project:

- Open an [Issue](https://github.com/kindmanners/tether/issues/new). (Since we can't be sure at this point whether it is a bug or not, we ask you not to talk about a bug yet and not to label the issue.)
- Explain the behavior you would expect and the actual behavior.
- Please provide as much context as possible and describe the *reproduction steps* that someone else can follow to recreate the issue on their own. This usually includes your code. For good bug reports you should isolate the problem and create a reduced test case.
- Provide the information you collected in the previous section.

Once it's filed:

- The project team will label the issue accordingly.
- A team member will try to reproduce the issue with your provided steps. If there are no reproduction steps or no obvious way to reproduce the issue, the team will ask you for those steps and mark the issue as `needs-repro`. Bugs with the `needs-repro` tag will not be addressed until they are reproduced.
- If the team is able to reproduce the issue, it will be marked `needs-fix`, as well as possibly other tags (such as `critical`), and the issue will be left to be [implemented by someone](#your-first-code-contribution).

<!-- You might want to create an issue template for bugs and errors that can be used as a guide and that defines the structure of the information to be included. If you do so, reference it here in the description. -->

### Suggesting Enhancements

This section guides you through submitting an enhancement suggestion for Tether, **including completely new features and minor improvements to existing functionality**. Following these guidelines will help maintainers and the community to understand your suggestion and find related suggestions.

<!-- omit in toc -->
#### Before Submitting an Enhancement

- Make sure that you are using the latest version.
- Read the [documentation](README.md) carefully and find out if the functionality is already covered, maybe by an individual configuration.
- Perform a [search](https://github.com/kindmanners/tether/issues) to see if the enhancement has already been suggested. If it has, add a comment to the existing issue instead of opening a new one.
- Find out whether your idea fits with the scope and aims of the project. It's up to you to make a strong case to convince the project's developers of the merits of this feature. Keep in mind that we want features that will be useful to the majority of our users and not just a small subset. If you're just targeting a minority of users, consider writing an add-on/plugin library.

<!-- omit in toc -->
#### How Do I Submit a Good Enhancement Suggestion?

Enhancement suggestions are tracked as [GitHub issues](https://github.com/kindmanners/tether/issues).

- Use a **clear and descriptive title** for the issue to identify the suggestion.
- Provide a **step-by-step description of the suggested enhancement** in as many details as possible.
- **Describe the current behavior** and **explain which behavior you expected to see instead** and why. At this point you can also tell which alternatives do not work for you.
- You may want to **include screenshots or screen recordings** which help you demonstrate the steps or point out the part which the suggestion is related to. You can use [LICEcap](https://www.cockos.com/licecap/) to record GIFs on macOS and Windows, and the built-in [screen recorder in GNOME](https://help.gnome.org/users/gnome-help/stable/screen-shot-record.html.en) or [SimpleScreenRecorder](https://github.com/MaartenBaert/ssr) on Linux. <!-- this should only be included if the project has a GUI -->
- **Explain why this enhancement would be useful** to most Tether users. You may also want to point out the other projects that solved it better and which could serve as inspiration.

<!-- You might want to create an issue template for enhancement suggestions that can be used as a guide and that defines the structure of the information to be included. If you do so, reference it here in the description. -->

### Your First Code Contribution

We welcome contributions of all sizes! Whether you are fixing a bug, improving test coverage, or implementing a new feature, this guide will help you get your local environment set up quickly.

#### Prerequisites

- **Go** (v1.22+ recommended)
- **Git**
- **golangci-lint** (recommended for local pre-commit checks)

#### Local Environment Setup

1. **Fork and clone the repository:**

   ```bash
   git clone https://github.com/kindmanners/tether.git
   cd tether
   ```

2. **Create a topic branch:**

   Use a descriptive branch name with a standard prefix (`feat/`, `fix/`, `docs/`, or `refactor/`):

   ```bash
   git checkout -b fix/pairing-timeout-handling

   # Download module dependencies.
   go mod download
   ```

### Building & Testing

Before submitting your code, ensure all binaries compile cleanly, tests pass under the race detector, and formatting complies with standard Go guidelines:

```bash

# Build all internal packages and command binaries
go build ./...

# Run unit & integration tests with race detector enabled
go test -v -race ./...

# Check code formatting and run static analysis
gofmt -w -s .
golangci-lint run
```

### Building runnable executables

`go build ./...` checks that every package compiles, but it does not leave
predictably named command executables for a local deployment. Build them
explicitly from the repository root:

```bash
go build -tags production -o bin/tether ./cmd/tether
go build -tags production -o bin/tether-agent ./cmd/tether-agent
```

On Windows, name the outputs with `.exe`:

```powershell
go build -tags 'production,wv2runtime.embed' -ldflags '-H windowsgui' -o .\bin\tether.exe .\cmd\tether
go build -tags 'production,wv2runtime.embed' -ldflags '-H windowsgui' -o .\bin\tether-agent.exe .\cmd\tether-agent
```

For a Windows GPU node, also build llama.cpp's CUDA RPC server. This requires
the NVIDIA CUDA Toolkit, CMake, and Visual Studio 2022 Build Tools with the C++
workload:

```powershell
cd llama.cpp
$cuda = 'C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v13.4'
$env:CUDA_PATH = $cuda
$env:CUDA_PATH_V13_4 = $cuda
$env:CudaToolkitDir = "$cuda\"
$env:PATH = "$cuda\bin;$cuda\bin\x64;$env:PATH"

cmake -S . -B build-rpc-cuda -G 'Visual Studio 17 2022' -A x64 `
  -DGGML_CUDA=ON -DGGML_RPC=ON
cmake --build build-rpc-cuda --config Release --target ggml-rpc-server --parallel 4
```

The output is `build-rpc-cuda\bin\Release\ggml-rpc-server.exe`. Keep its
adjacent DLLs when running it. See the README for Agent configuration, firewall
rules, and a Linux Orchestrator RPC build.

### Improving The Documentation

Clear documentation is just as critical as high-quality code. You can contribute by:

- Go Doc Comments: Ensure all exported functions, types, constants, and packages have clear Go doc comments explaining their purpose and edge cases.

- Architecture & Design Docs: When modifying core behaviors (such as pairing handshakes, TLS trust pinning, or IPC protocols), update the relevant design documents in docs/ alongside your code changes.

- Typo & Clarity Fixes: Correcting typos, ambiguous phrasing, or outdated CLI instructions in README.md or CONTRIBUTING.md is always welcome.

## Styleguides

### Commit Messages

We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification. Structured commit messages make git logs readable and enable automated release note generation.

#### Format

[optional body]

[optional footer(s)]

#### Types

- **`feat`**: A new feature or capability.
- **`fix`**: A bug fix.
- **`docs`**: Documentation-only changes (GoDoc, Markdown files).
- **`refactor`**: Code changes that neither fix a bug nor add a feature (restructuring, cleanup).
- **`perf`**: Code changes that improve execution speed or resource usage.
- **`test`**: Adding missing tests or updating existing test suites.
- **`build`**: Changes affecting the build system, Go toolchain, or external dependencies (`go.mod`).
- **`ci`**: Changes to continuous integration pipelines (`.github/workflows`).
- **`chore`**: Maintenance tasks that don't modify internal src or test files.

#### Scope (Optional)

Specify the subsystem being modified. Common scopes for Tether include:
`pairing`, `tls`, `ipc`, `cli`, `config`, `agent`.

#### Guidelines

1. **Use the Imperative Mood:** Write the summary line as a command: *"add endpoint"* rather than *"added endpoint"* or *"adds endpoint"*.
2. **Keep the First Line Short:** Limit the summary line to 50–72 characters.
3. **No Trailing Period:** Do not end the subject line with a period.
4. **Explain the *Why* in the Body:** Use the commit body to explain *why* the change was made and any trade-offs involved, not *how* (the code diff shows how).
5. **Breaking Changes:** Indicate breaking protocol or API changes by appending `!` after the type/scope, or placing `BREAKING CHANGE:` in the footer:
   `feat(ipc)!: update packet frame encoding format`
6. **Reference Issues:** Link related GitHub issues in the footer:
   `Closes #42` or `Fixes #108`.

#### Example Commit Message

```commit
fix(pairing): increase timeout duration during TLS handshake

High-latency network connections cause pair handshakes to fail prematurely.
This bumps the connection context timeout to 15s and adds exponential backoff.
```

<!-- omit in toc -->

## Attribution

This guide is based on the [contributing.md](https://contributing.md/generator)!

(c) Ataraxia Productions 2026
