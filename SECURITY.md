# Security Policy

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability or accidentally
exposed credential. Use GitHub's private vulnerability reporting feature for
this repository instead.

Include the affected version, operating system, reproduction steps, expected
impact, and any suggested mitigation. Remove API keys, access tokens, personal
paths, conversation contents, and other sensitive data from the report.

You should receive an acknowledgement within seven days. A fix timeline will
depend on severity and complexity. Please allow time for a patch to be released
before publishing details.

Confirmed unresolved vulnerabilities that materially affect released users
will also be summarized in the README with affected versions and a temporary
mitigation. The entry will be removed or marked resolved when a patched release
is available.

## Scope

Security reports are especially useful for issues involving:

- exposure of provider credentials;
- unauthorized access to the local AIX gateway;
- unsafe writes to Claude or Codex configuration;
- loss or unintended modification of conversation data;
- command or path injection;
- insecure update or remote-catalog handling.

Provider availability, upstream model behavior, and unsupported client
versions are normally compatibility issues rather than security issues.
