# Security

The security architecture and current verification limits are documented in [docs/security.md](docs/security.md). Deployment and recovery procedures are in [docs/operations.md](docs/operations.md).

Report suspected vulnerabilities privately to the installation operator or repository maintainer through their established private channel. No public contact address is configured in this local repository. Do not attach passwords, session tokens, TOTP secrets, database URLs, receipt images or household exports to public issues.

Include the application/image version, affected route or operation, reproducible synthetic steps and expected/observed behavior. Preserve relevant request IDs and redact personal information. Rotate compromised credentials and revoke affected sessions with the operator; test recovery in a replacement environment before switching a live installation.

Local scanner results do not constitute certification or guarantee that a deployment is secure. HTTPS/proxy configuration, host patching, secret custody, off-host backup storage and restoration acceptance remain operator responsibilities.
