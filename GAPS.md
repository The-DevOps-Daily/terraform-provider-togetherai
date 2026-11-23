# What the provider does not cover yet

The Together AI API ([OpenAPI spec](testdata/together-openapi.yaml)) is much
larger than this provider. Version 0.1.0 manages GPU clusters, shared volumes
and dedicated inference endpoints. This page lists what the API supports that
the provider does not manage yet, grouped by area. If you need one of these,
open an issue or a pull request.

Calls that run a model (chat, completions, embeddings, images, audio, video,
rerank, realtime) are out of scope. They are runtime traffic, not
infrastructure, and belong in application code.

## GPU clusters

| API | Endpoints | Possible provider shape |
| --- | --- | --- |
| Cluster add-ons | `/v1/compute/clusters/{cluster_id}/addons` | `togetherai_gpu_cluster_addon` resource |
| Node remediations | `/v1/compute/clusters/{cluster_id}/instances/{instance_id}/remediations` | Operational actions; probably a data source only |
| Passive health check alerts | `/v1/compute/passive_health_check_alerts` | Data source |
| Listing clusters and volumes | `GET /v1/compute/clusters`, `GET /v1/compute/clusters/storage/volumes` | `togetherai_gpu_clusters` and `togetherai_shared_volumes` data sources |

## Dedicated endpoints

| API | Endpoints | Possible provider shape |
| --- | --- | --- |
| LoRA adapters on a deployment | `.../deployments/{deploymentId}/adapters` | `togetherai_deployment_adapter` resource |
| Rollouts (start, pause, resume, cancel, promote) | `.../endpoints/{endpointId}/rollouts` | Resource or action; `togetherai_endpoint_traffic_split` covers manual canary and blue/green today |
| A/B experiments | `.../endpoints/{endpointId}/abExperiments` | `togetherai_ab_experiment` resource |
| Shadow experiments and their targets | `.../endpoints/{endpointId}/shadowExperiments` | `togetherai_shadow_experiment` resource |
| Endpoint analytics and events | `.../endpoints/{id}/analytics`, `.../endpoints/{id}/events` | Data sources |
| Listing endpoints and deployments | `GET /v2/projects/{projectId}/endpoints`, `.../deployments`, `/v2/organizations/{organizationId}/endpoints`, `/v2/public/endpoints` | List data sources |
| Saved configs and placement profiles | `/v2/projects/{projectId}/configs`, `/v2/projects/{projectId}/placement-profiles` | Data sources |
| Single catalog items | `GET /v2/supported-models/{id}`, `GET /v2/public/inference-instance-types/{id}` | The list data sources already cover these |

## Custom models

| API | Endpoints | Possible provider shape |
| --- | --- | --- |
| Project models, files and revisions | `/v2/projects/{projectId}/models` | `togetherai_model` resource |
| Remote model uploads | `/v2/projects/{projectId}/models/uploads` | Resource that waits for the upload |
| Organization models | `/v2/organizations/{organizationId}/models` | Data source |
| FP4 quantization (shaping) jobs | `/v1/shaping` | Job resource |

## Container deployments

| API | Endpoints | Possible provider shape |
| --- | --- | --- |
| Deployments, revisions, rollback and logs | `/v1/deployments` | `togetherai_container_deployment` resource |
| Deployment secrets | `/v1/deployments/secrets` | `togetherai_deployment_secret` resource |
| Deployment storage and volumes | `/v1/deployments/storage` | Resource |

## Training, evaluation and jobs

These are mostly one-off jobs, which fit Terraform less well than long-lived
infrastructure.

| API | Endpoints |
| --- | --- |
| Files | `/v1/files` |
| Fine-tuning | `/v1/fine-tunes` |
| Batches | `/v1/batches` |
| Evaluations | `/v1/evaluation` |
| Reinforcement learning sessions, model resources and checkpoints | `/v1/rl` |
| Queue jobs | `/v1/queue` |

## Account

| API | Endpoints | Possible provider shape |
| --- | --- | --- |
| Billing usage | `/v1/billing/usage` | Data source |
| Current identity | `/v3/whoami` | Data source |
| Voices catalog | `/v1/voices` | Data source |
