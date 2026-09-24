# Security policy

`spun` holds a token that can change a live website, so we treat its security as part of the
spun.ink service. City of Code GmbH, the operator of spun.ink, is the manufacturer of this software.

## Supported versions

Security fixes are made on the latest release. Please upgrade before reporting, and tell us the
version you found the problem in (`spun --version`).

## Reporting a vulnerability

Please do **not** open a public issue. Report privately, by either:

- GitHub's private vulnerability reporting: **Security → Report a vulnerability** on
  https://github.com/spun-ink/cli, or
- email to **support@spun.ink** with "Security" in the subject.

Include what you found, how to reproduce it and what an attacker could gain. Never send a real
token; a revoked one or a description is enough.

## What happens next

- We acknowledge a report within **3 working days**.
- We assess it and tell you our view of its severity within **10 working days**.
- We fix confirmed vulnerabilities in a new release and publish a GitHub security advisory. We
  agree the disclosure date with you; we aim for no more than 90 days from the report.
- We credit you in the advisory unless you ask us not to.

A vulnerability in the spun.ink service itself (rather than this client) is welcome through the same
channels.

## Regulatory reporting

We report actively exploited vulnerabilities and severe incidents affecting this software as the EU
Cyber Resilience Act requires.
