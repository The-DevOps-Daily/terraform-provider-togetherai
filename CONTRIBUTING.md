# Contributing

Thanks for helping improve the Terraform provider for Together AI. Read the
[Code of Conduct](CODE_OF_CONDUCT.md) before taking part. Report security
issues through the private form in [SECURITY.md](SECURITY.md).

## Development setup

Install Go 1.26 or newer, as specified in `go.mod`, GNU Make and the Terraform
CLI. Put `go` and `terraform` on your `PATH`. Clone this repository, then run:

```sh
go mod download
make build
make test
```

The Go module is `github.com/The-DevOps-Daily/terraform-provider-togetherai`.
Dependencies must be downloaded before working offline.

The following targets are defined in `GNUmakefile`:

| Target | Purpose |
| --- | --- |
| `make` or `make build` | Build `terraform-provider-togetherai`. |
| `make install` | Install the provider with `go install`. |
| `make test` | Run unit tests and mock API acceptance tests. |
| `make testacc` | Run real-API acceptance tests with `TF_ACC=1`. |
| `make examples` | Run `terraform validate` on every example, then plan them against the mock API. |
| `make mock` | Run the mock API on a local port for manual testing. |
| `make fmt` | Format Go files with `gofmt`. |
| `make vet` | Run `go vet`. |
| `make lint` | Run golangci-lint. |
| `make docs` | Generate provider documentation with tfplugindocs. |

## Unit and mock acceptance tests

Run `make test` without Together AI credentials. The `TestMock*` tests run
Terraform plan, apply, import, refresh and destroy against an in-process mock
of the API in `internal/mockapi`, built from the OpenAPI spec in `testdata/`.
No API key and no network are needed. The real-API `TestAcc*` tests skip
because `TOGETHER_API_KEY` is not set.

When you change the client or a resource, update the mock in the same pull
request so it keeps following the spec: status codes, async states, error
bodies and validation.

## Real-API acceptance tests

Use a dedicated Together AI account or project. These tests create and change
real resources, and some of them bill real money. The README lists the
expected cost of each test. Load the following values through your local
secret manager or environment without committing them:

- `TF_ACC=1` enables real-API tests. `make testacc` sets this for you.
- `TOGETHER_API_KEY` is an API key for the test account. The GPU cluster test
  needs the key of a project admin to read the kubeconfig.
- `TOGETHER_ACC_EXPENSIVE=1` also runs the tests that cost money: a running
  deployment, a shared volume and a GPU cluster.
- `TOGETHER_PROJECT_ID` selects the project when the key sees more than one.
- `TOGETHER_ACC_MODEL_SEARCH`, `TOGETHER_ACC_MODEL`, `TOGETHER_ACC_CONFIG`,
  `TOGETHER_ACC_REGION` and `TOGETHER_ACC_GPU_TYPE` are optional overrides
  described in the README.

With the credentials set, run either command:

```sh
make testacc
# Equivalent command:
TF_ACC=1 go test ./internal/provider/ -run '^TestAcc' -v -count=1 -timeout 120m
```

The tests use random names with the `tfacc-` prefix and destroy what they
create. A failed or interrupted run can leave resources behind. After such a
run, check the Together AI console for `tfacc-` resources, mainly GPU clusters
and shared volumes, and delete them.

The Acceptance workflow runs only through manual dispatch, never on a
schedule or on pull requests. It needs the `TOGETHER_API_KEY` repository
secret and skips the test run without it. Its `expensive` input turns on the
paid tests.

## Documentation

Edit schema descriptions and files under `examples/`, then run `make docs`.
This uses tfplugindocs to regenerate `docs/`. Include the generated files in
your pull request. CI runs the same generation command and checks for changes
in `docs/`. Do not edit generated documentation as the only source of a change.

## Commits and pull requests

- Keep changes focused. Discuss large changes in an issue first.
- Use clear commit messages that describe the change.
- Explain the problem, the resulting behavior and how you tested it in the PR.
- Add or update tests for behavior changes. Run `make fmt`, `make test`,
  `make vet`, `make lint` and `make examples` before requesting review.
- Update examples and regenerate documentation when schemas or usage change.
- Add an entry under the unreleased version in [CHANGELOG.md](CHANGELOG.md)
  for user-visible changes. Use the existing categories and name the affected
  resource or data source.
- Remove secrets from code, fixtures, logs and screenshots. Do not commit
  Terraform state, kubeconfigs or local credentials.

Contributions are covered by the repository's [MPL-2.0 license](LICENSE).
