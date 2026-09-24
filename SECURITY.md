# Security Policy

## Supported Versions

Tether is currently under active development. Security fixes are provided for the latest release and the current development version.

| Version                  | Supported          |
| ------------------------ | ------------------ |
| Latest release           | :white_check_mark: |
| `master` development branch | :white_check_mark: |
| Older releases           | :x:                |

Users are encouraged to update to the latest available version before reporting a vulnerability that may already have been fixed.

## Reporting a Vulnerability

Please **do not report security vulnerabilities through public GitHub issues, discussions, or pull requests**.

Instead, please report vulnerabilities using **GitHub Private Vulnerability Reporting** for this repository.

If private vulnerability reporting is unavailable, you may contact the Tether maintainers at:

**maxiefeseymen@gmail.com**

When submitting a report, please include as much relevant information as possible, such as:

- A description of the vulnerability.
- The affected Tether component, such as the Orchestrator, Agent, API, pairing system, or networking layer.
- Steps required to reproduce the issue.
- The potential security impact.
- Relevant logs or configuration details, with passwords, tokens, certificates, and other sensitive information removed.
- A proof of concept, if applicable.
- Any known mitigations or suggested fixes.

## What to Expect

We aim to acknowledge security reports within **7 days**.

After reviewing the report, we may contact you for additional information or clarification.

If the vulnerability is accepted:

- The issue will be investigated privately.
- A fix will be developed and tested where appropriate.
- Security-sensitive details will remain private until a fix or mitigation is available.
- A GitHub Security Advisory or release note may be published once the issue has been resolved.

If the report is determined not to represent a security vulnerability, we will provide an explanation where reasonably possible.

Please allow the maintainers reasonable time to investigate and resolve a vulnerability before publicly disclosing it.

## Scope

Examples of security issues that should be reported privately include:

- Authentication or authorization bypasses.
- Unauthorized access to a Tether Agent or Orchestrator.
- Remote code execution.
- Privilege escalation.
- Weaknesses in Tether's device pairing process.
- mTLS or certificate validation vulnerabilities.
- Exposure of authentication tokens, certificates, configuration data, or other sensitive information.
- Vulnerabilities in Tether's OpenAI-compatible API.
- Network vulnerabilities that unintentionally expose Tether services.
- Security-sensitive command or configuration injection.
- Security issues caused by Tether's interaction with `llama.cpp` or `ggml-rpc`.
- Denial-of-service vulnerabilities with meaningful security impact.

General bugs, crashes, performance problems, installation problems, and feature requests without a security impact should be reported through the normal GitHub issue tracker.

If a vulnerability exists entirely within an upstream dependency such as `llama.cpp` and is not caused or worsened by Tether, it should generally be reported to the upstream project instead.

## Responsible Testing

When investigating a potential vulnerability:

- Only test systems and devices you own or have explicit permission to test.
- Do not access another person's data without authorization.
- Do not intentionally disrupt infrastructure or services.
- Do not perform destructive testing.
- Do not retain or publicly disclose credentials, tokens, certificates, or private data obtained unintentionally.
- Stop testing and report the issue if you gain unintended access to systems or sensitive information.

Thank you for helping keep Tether and its users secure.
