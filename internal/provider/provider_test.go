package provider

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/mockapi"
)

var testProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"togetherai": providerserver.NewProtocol6WithError(New("test")()),
}

// skipWithoutTerraform skips plan/apply tests when no Terraform CLI is
// available. CI installs one with hashicorp/setup-terraform.
func skipWithoutTerraform(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC_TERRAFORM_PATH") != "" {
		return
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform CLI not found on PATH; skipping plan/apply test against the mock API")
	}
}

// newMock starts the mock API and returns it with a provider block that
// points at it. Polling runs every few milliseconds instead of 15 seconds.
func newMock(t *testing.T) (*mockapi.Server, string) {
	t.Helper()
	skipWithoutTerraform(t)
	m := mockapi.New()
	t.Cleanup(m.Close)
	t.Setenv("TOGETHER_POLL_INTERVAL", "5ms")
	t.Setenv("TOGETHER_API_KEY", "")
	t.Setenv("TOGETHER_BASE_URL", "")
	t.Setenv("TOGETHER_PROJECT_ID", "")
	return m, fmt.Sprintf(`
provider "togetherai" {
  api_key  = %q
  base_url = %q
}
`, mockapi.APIKey, m.URL())
}

func TestProviderSchemaValid(t *testing.T) {
	// providerserver validates the schema of every resource and data source
	// when it serves GetProviderSchema.
	srv, err := testProtoV6ProviderFactories["togetherai"]()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		t.Errorf("%s: %s: %s", d.Severity, d.Summary, d.Detail)
	}
	for _, name := range []string{"togetherai_gpu_cluster", "togetherai_shared_volume", "togetherai_endpoint", "togetherai_endpoint_traffic_split", "togetherai_deployment"} {
		if _, ok := resp.ResourceSchemas[name]; !ok {
			t.Errorf("missing resource %s", name)
		}
	}
	for _, name := range []string{"togetherai_gpu_cluster", "togetherai_gpu_cluster_regions", "togetherai_hardware", "togetherai_supported_models", "togetherai_projects"} {
		if _, ok := resp.DataSourceSchemas[name]; !ok {
			t.Errorf("missing data source %s", name)
		}
	}
}
