# testdata

`together-openapi.yaml` is the Together AI OpenAPI spec the provider and its
mock API were built from.

- Source: https://docs.together.ai/openapi.yaml (the file says it is synced
  from `togethercomputer/openapi`)
- Downloaded: 2026-11-20
- SHA-256: `6566a4414ba8259e5124e80c58ad4ccdfd98707cd814ba8164aacebcab7b67a7`
- License: MIT, as stated in the spec's `info.license` block

The provider uses these operations:

| Operation | Used by |
| - | - |
| `GPUClusterService_Create/Get/Update/Delete` | `togetherai_gpu_cluster`, data source `togetherai_gpu_cluster` |
| `RegionService_List` | data source `togetherai_gpu_cluster_regions` |
| `SharedVolumeService_Create/Get/Update/Delete` | `togetherai_shared_volume` |
| `EndpointService_CreateEndpoint/GetEndpoint/UpdateEndpoint/DeleteEndpoint` | `togetherai_endpoint`, `togetherai_endpoint_traffic_split` |
| `DeploymentService_CreateDeployment/GetDeployment/UpdateDeployment/DeleteDeployment` | `togetherai_deployment` |
| `InstanceTypeService_ListInferenceInstanceTypes` | data source `togetherai_hardware` |
| `SupportedModelsService_ListSupportedModels` | data source `togetherai_supported_models` |
| `listProjects` | data source `togetherai_projects`, default project lookup |
