# Terraform Provider for Together AI

[![Test](https://github.com/The-DevOps-Daily/terraform-provider-togetherai/actions/workflows/test.yml/badge.svg)](https://github.com/The-DevOps-Daily/terraform-provider-togetherai/actions/workflows/test.yml)
[![License: MPL-2.0](https://img.shields.io/badge/license-MPL--2.0-blue.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](go.mod)
[![Terraform Registry](https://img.shields.io/badge/Terraform-Registry-844FBA.svg)](https://registry.terraform.io/providers/The-DevOps-Daily/togetherai/latest)

A Terraform provider for [Together AI](https://www.together.ai) infrastructure:
GPU clusters with shared storage, and dedicated inference endpoints with their
deployments and traffic split. It is built on the
[Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework)
and the public [Together AI OpenAPI spec](https://docs.together.ai/openapi.yaml).

This is a community provider. It is not affiliated with or endorsed by
Together AI.

> **Early release (v0.1.0).** APIs and schema may change between minor
> versions until 1.0. Pin a version (`~> 0.1`) and read the
> [changelog](CHANGELOG.md) before you upgrade.

## What it manages

| Type | Name | API |
| --- | --- | --- |
| Resource | `togetherai_gpu_cluster` | GPU clusters (Kubernetes or Slurm): create and wait until `Ready`, scale GPUs in place, export the admin kubeconfig, delete. |
| Resource | `togetherai_shared_volume` | Shared volumes for GPU clusters: create, grow in place, delete. |
| Resource | `togetherai_endpoint` | Dedicated inference endpoints: create, rename, change visibility, delete. |
| Resource | `togetherai_deployment` | A model deployment on an endpoint: hardware config, autoscaling, idle timeout. Create and replica changes wait for the target state. Set replicas to 0 to stop it. |
| Resource | `togetherai_endpoint_traffic_split` | Which deployments get an endpoint's requests, and their weights (canary and blue/green). |
| Data source | `togetherai_gpu_cluster` | One cluster by ID, with its kubeconfig. |
| Data source | `togetherai_gpu_cluster_regions` | Cluster regions, GPU types and NVIDIA driver versions. |
| Data source | `togetherai_hardware` | Dedicated inference hardware with on-demand prices. |
| Data source | `togetherai_supported_models` | Models you can deploy, with their certified deployment profiles. |
| Data source | `togetherai_projects` | Projects the API key can access. |

All resources support `terraform import`. A resource deleted outside
Terraform is removed from the state on the next refresh and planned again.

## Quick start

```hcl
terraform {
  required_providers {
    togetherai = {
      source  = "The-DevOps-Daily/togetherai"
      version = "~> 0.1"
    }
  }
}

# Reads TOGETHER_API_KEY from the environment.
provider "togetherai" {}

data "togetherai_supported_models" "gemma" {
  search  = "gemma-4-E4B-it"
  product = "PRODUCT_DEDICATED"
}

locals {
  profile = data.togetherai_supported_models.gemma.models[0].deployment_profiles[0]
}

resource "togetherai_endpoint" "chat" {
  name = "chat"
}

resource "togetherai_deployment" "gemma" {
  endpoint_id = togetherai_endpoint.chat.id
  name        = "gemma"
  model       = local.profile.model
  config      = local.profile.config

  autoscaling = {
    min_replicas = 1
    max_replicas = 2
  }
}

resource "togetherai_endpoint_traffic_split" "chat" {
  endpoint_id = togetherai_endpoint.chat.id
  routes      = [{ deployment_id = togetherai_deployment.gemma.id, weight = 1 }]
}

# Send requests with model = this name.
output "model_name" {
  value = togetherai_endpoint.chat.qualified_name
}
```

Two complete examples:

- [`examples/dedicated-endpoint`](examples/dedicated-endpoint): an endpoint,
  an autoscaled deployment and its traffic split.
- [`examples/gpu-cluster-kubernetes`](examples/gpu-cluster-kubernetes): the
  headline use case. One `terraform apply` creates an on-demand GPU cluster
  with a shared volume, waits until it is `Ready`, and runs an `nvidia-smi`
  Job on it through the `kubernetes` provider. `terraform destroy` removes
  everything.

Every resource and data source has an example under [`examples/`](examples)
and a page under [`docs/`](docs).

## Configuration

| Argument | Environment variable | Default |
| --- | --- | --- |
| `api_key` | `TOGETHER_API_KEY` | none (sensitive) |
| `base_url` | `TOGETHER_BASE_URL` | `https://api.together.ai` |
| `project_id` | `TOGETHER_PROJECT_ID` | the only project the key can see |

Endpoints and deployments live in a project. Set `project_id` on the resource,
on the provider, or leave both unset when the key can see exactly one project.

The API key is checked only when a resource or data source calls the API, so
`terraform validate` works without one. GPU cluster kubeconfigs are only
returned to the key of a project admin.

### Behaviour notes

- Requests that get a 429 or a 5xx are retried with backoff (honouring
  `Retry-After`). Creates are retried only on 429, because a failed create may
  have happened. Quota errors are not retried.
- API errors are shown with the method, path, status and the API's own error
  type, code and message.
- `togetherai_shared_volume.size_tib` can only grow. A smaller value fails at
  plan time.
- Destroying a `togetherai_deployment` first removes it from the traffic
  split, stops it and waits until it is stopped, then deletes it. This is the
  order the API requires.
- Destroying a `togetherai_endpoint_traffic_split` sets an empty split, so the
  endpoint stops serving live traffic.

## Cost

GPU clusters, shared volumes and running deployments bill real money. Prices
from [together.ai/pricing](https://www.together.ai/pricing) (checked
2026-11-20, on-demand):

| What | Price |
| --- | --- |
| GPU cluster, H100 | $3.99 per GPU hour (8 x H100 = about $32 per hour) |
| GPU cluster, H200 / B200 | $5.99 / $9.29 per GPU hour |
| Shared volume | $0.16 per GiB per month (1 TiB = about $164 per month, $0.22 per hour) |
| Dedicated inference, H100 / H200 / B200 | $5.49 / $5.99 / $9.29 per GPU hour, billed per minute while a replica runs |

An endpoint on its own, and a deployment stopped at 0 replicas, do not bill.

## Development

Requires Go 1.26 or newer. The mock acceptance tests also need the
`terraform` CLI on `PATH` (or `TF_ACC_TERRAFORM_PATH`).

```sh
make build      # build the provider binary
make test       # unit tests + mock acceptance tests (no key, no network)
make examples   # terraform validate every example, then plan them against the mock API
make lint       # golangci-lint
make docs       # regenerate docs/ with tfplugindocs
make mock       # run the mock API on a local port for manual testing
```

### Tests against the mock API (no key)

`make test` runs the client unit tests and the `TestMock*` tests. The
`TestMock*` tests run the real `terraform` binary (plan, apply, import,
refresh, destroy) against an in-process mock of the Together AI API in
[`internal/mockapi`](internal/mockapi). The mock follows the spec in
[`testdata/`](testdata): status codes, async states (`WaitingForControlPlaneNodes` to `Ready`,
`DEPLOYMENT_STATE_PENDING` to `DEPLOYMENT_STATE_READY`), 404s, validation
errors and the API's error bodies. Every resource has create, update, import,
drift (deleted out of band) and destroy coverage. CI runs them on every push.

### Acceptance tests against the real API

The `TestAcc*` tests use a real Together AI account. They skip unless
`TF_ACC=1` and `TOGETHER_API_KEY` are set, use random `tfacc-` names and
destroy what they create. The tests that cost money skip unless
`TOGETHER_ACC_EXPENSIVE=1` is also set.

| Test | Needs | Creates | Approximate cost |
| --- | --- | --- | --- |
| `TestAccDataSources` | key | nothing (reads only) | $0 |
| `TestAccEndpoint` | key | one endpoint, no deployment | $0 |
| `TestAccDeploymentStopped` | key | an endpoint and a deployment at 0 replicas | $0 (no replica runs) |
| `TestAccDeploymentRunning` | `TOGETHER_ACC_EXPENSIVE=1` | one replica of a small catalog model until it is ready, then stops it | about $1 to $3 (one H100 replica at $5.49 per hour for 10 to 30 minutes; more if the chosen profile uses more GPUs) |
| `TestAccSharedVolume` | `TOGETHER_ACC_EXPENSIVE=1` | a 1 TiB volume, grown to 2 TiB, for a few minutes | under $0.10 (2 TiB at $0.16 per GiB per month is about $0.45 per hour) |
| `TestAccGPUCluster` | `TOGETHER_ACC_EXPENSIVE=1` | an 8 x H100 on-demand Kubernetes cluster with a 1 TiB volume, until it is `Ready`, then destroys it | about $16 to $50 (8 x $3.99 = $31.92 per hour; provisioning takes 10 to 60 minutes and the timeout allows 90) |

Free tests only:

```sh
export TOGETHER_API_KEY=...
make testacc
```

All tests, including the paid ones:

```sh
export TOGETHER_API_KEY=...
TOGETHER_ACC_EXPENSIVE=1 make testacc
```

Optional settings:

| Variable | Use |
| --- | --- |
| `TOGETHER_PROJECT_ID` | project for endpoints and deployments, when the key sees more than one |
| `TOGETHER_ACC_MODEL_SEARCH` | catalog search for the deployment tests (default `gemma-4-E4B-it`) |
| `TOGETHER_ACC_MODEL`, `TOGETHER_ACC_CONFIG` | a fixed model and deployment config instead of the catalog profile |
| `TOGETHER_ACC_REGION` | region for the volume and cluster tests |
| `TOGETHER_ACC_GPU_TYPE` | GPU type for the cluster test (default `H100_SXM`) |

If a test fails half way, check the console for leftover `tfacc-` resources,
mainly GPU clusters and volumes, and delete them.

## What is not covered yet

[`GAPS.md`](GAPS.md) lists the parts of the Together AI API that the provider
does not manage yet, such as fine-tuning, batches, model uploads, adapters,
rollouts and experiments. Issues and pull requests are welcome.

## Maintained by DevOps Daily

This project is maintained by [DevOps Daily](https://devops-daily.com).
Read more in the [DevOps Daily posts](https://devops-daily.com/posts).

<!-- announcement post link -->

See the [contribution guide](CONTRIBUTING.md), [security policy](SECURITY.md)
and [changelog](CHANGELOG.md).

## License

[MPL-2.0](LICENSE)
