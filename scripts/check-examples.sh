#!/bin/sh
# Build the provider, then run terraform init + validate on every example.
# With MOCK=1, also start the mock API and run terraform plan on the examples
# that need no input, so data source reads and plan-time checks run too.
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
mock_pid=""
cleanup() {
  if [ -n "$mock_pid" ]; then kill "$mock_pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

mkdir -p "$work/bin"
(cd "$root" && go build -o "$work/bin/terraform-provider-togetherai" .)
cat > "$work/dev.tfrc" <<TFRC
provider_installation {
  dev_overrides {
    "the-devops-daily/togetherai" = "$work/bin"
  }
  direct {}
}
TFRC
export TF_CLI_CONFIG_FILE="$work/dev.tfrc" CHECKPOINT_DISABLE=1 TF_IN_AUTOMATION=1
cp -r "$root/examples" "$work/examples"
cd "$work/examples"

for dir in resources/* data-sources/*; do
  cp provider/provider.tf "$dir/zz_provider.tf"
done

for dir in provider dedicated-endpoint gpu-cluster-kubernetes resources/* data-sources/*; do
  echo "== validate $dir"
  (cd "$dir" && terraform init -backend=false -input=false >/dev/null && terraform validate -no-color >/dev/null) || {
    (cd "$dir" && terraform validate -no-color) || true
    exit 1
  }
done

if [ "${MOCK:-}" = "1" ]; then
  (cd "$root" && go build -o "$work/mockserver" ./tools/mockserver)
  "$work/mockserver" > "$work/mock.url" 2>/dev/null &
  mock_pid=$!
  i=0
  while [ ! -s "$work/mock.url" ] && [ $i -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
  TOGETHER_BASE_URL="$(head -1 "$work/mock.url")"
  export TOGETHER_BASE_URL TOGETHER_API_KEY=mock-api-key TF_VAR_project_slug=mock-project
  for dir in dedicated-endpoint resources/togetherai_endpoint resources/togetherai_deployment \
    resources/togetherai_endpoint_traffic_split resources/togetherai_shared_volume \
    resources/togetherai_gpu_cluster data-sources/togetherai_gpu_cluster_regions \
    data-sources/togetherai_hardware data-sources/togetherai_projects \
    data-sources/togetherai_supported_models; do
    echo "== plan $dir (mock API)"
    (cd "$dir" && terraform plan -input=false -no-color >/dev/null) || {
      (cd "$dir" && terraform plan -input=false -no-color) || true
      exit 1
    }
  done
fi
echo "All examples OK"
