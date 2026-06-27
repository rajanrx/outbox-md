# Security Policy

## Reporting a Vulnerability

**Please do not open public issues for security vulnerabilities.**

Report privately to the maintainer at **rajanrauniyar@gmail.com**. Include a description, reproduction steps, and impact. We aim to acknowledge within a few days.

## Threat model notes

outbox-md is local-first and ships **no credentials** and **no embedded model**. Be aware that a running instance:

- **reads and writes Markdown files** in the folder it is pointed at;
- **exposes an MCP endpoint** that any connected agent can use to read documents and propose changes.

Run it only against folders and agents you trust. Do not expose the container's port to untrusted networks.
