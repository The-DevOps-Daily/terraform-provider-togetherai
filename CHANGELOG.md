# Changelog

## 0.1.0 (November 23, 2026)

Early release. APIs and schema may change between minor versions until 1.0.

FEATURES:

* **New Resource:** `togetherai_gpu_cluster` creates Kubernetes or Slurm GPU clusters, waits until they are `Ready`, scales GPUs in place and exports the admin kubeconfig.
* **New Resource:** `togetherai_shared_volume` creates shared volumes for GPU clusters and grows them in place.
* **New Resource:** `togetherai_endpoint` creates dedicated inference endpoints and changes their name and visibility in place.
* **New Resource:** `togetherai_deployment` deploys a model on an endpoint with a hardware config, autoscaling and an idle timeout. Setting replicas to 0 stops it.
* **New Resource:** `togetherai_endpoint_traffic_split` sets which deployments receive an endpoint's requests and their weights.
* **New Data Source:** `togetherai_gpu_cluster` reads one cluster by ID, with its kubeconfig.
* **New Data Source:** `togetherai_gpu_cluster_regions` lists cluster regions, GPU types and NVIDIA driver versions.
* **New Data Source:** `togetherai_hardware` lists dedicated inference hardware with on-demand prices.
* **New Data Source:** `togetherai_supported_models` lists deployable models with their certified deployment profiles.
* **New Data Source:** `togetherai_projects` lists the projects the API key can access.

NOTES:

* provider: When `project_id` is not set on the resource, the provider or `TOGETHER_PROJECT_ID`, the provider uses the only project the key can see.
* resource/togetherai_shared_volume: `size_tib` can only grow. A smaller value fails at plan time.
* resource/togetherai_deployment: Destroy removes the deployment from the traffic split, stops it and waits until it is stopped, then deletes it.
* resource/togetherai_endpoint_traffic_split: Destroy sets an empty split, so the endpoint stops serving live traffic.
