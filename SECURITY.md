# Security policy

## Supported versions

Security fixes target the latest minor release series. Use its latest patch
release. Older minor series are not supported.

## Report a vulnerability

Report security issues privately through GitHub's
[Report a vulnerability form](https://github.com/The-DevOps-Daily/terraform-provider-togetherai/security/advisories/new).
This uses private security advisories. Do not open a public issue for a
vulnerability.

Include:

- The provider and Terraform versions, and your operating system.
- A description of the issue and its possible impact.
- Steps to reproduce it, with a small configuration if useful.
- Relevant logs or error messages with secrets removed.
- Any suggested fix or workaround.

The DevOps Daily maintainers aim to acknowledge reports within 3 working days.
We will use the private advisory to discuss findings, a fix and disclosure.

## Scope and credentials

This policy covers this Terraform provider and its repository. Issues in the
Together AI service should be reported through Together AI's own security channels.

Never paste API keys or kubeconfigs in issues. Remove credentials, cluster
access details and other sensitive data from configurations, logs and state
before sharing them, including in private reports. Terraform state holds the
cluster kubeconfig in plain text, so never share state. If a key has been
exposed, revoke it in the Together AI dashboard.

This project is maintained by [DevOps Daily](https://devops-daily.com).
