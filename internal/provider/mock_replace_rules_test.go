package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/mockapi"
)

// A create-only field left out at create and added later replaces the
// resource, and update-only checks (no shrinking) do not block a replacement.
func TestMockSharedVolumeReplaceRules(t *testing.T) {
	_, provider := newMock(t)
	cfg := func(name string, size int, extra string) string {
		return provider + fmt.Sprintf(`
resource "togetherai_shared_volume" "data" {
  name     = %q
  size_tib = %d
  region   = "us-central-8"
  %s
}
`, name, size, extra)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg("tf-rules", 2, "")},
			{
				Config: cfg("tf-rules", 2, "is_lifecycle_independent = true"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_shared_volume.data", plancheck.ResourceActionDestroyBeforeCreate),
				}},
			},
			{
				// Rename and shrink: a new volume, so the shrink check does not apply.
				Config: cfg("tf-rules-2", 1, "is_lifecycle_independent = true"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_shared_volume.data", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.TestCheckResourceAttr("togetherai_shared_volume.data", "size_tib", "1"),
			},
		},
	})
}

// After an import, create-only fields the API does not return are filled in
// place. Once an apply has run, changing them replaces the resource again.
func TestMockSharedVolumeImportThenConfigure(t *testing.T) {
	m, provider := newMock(t)
	v, err := client.New(mockapi.APIKey, m.URL(), "").CreateVolume(context.Background(), client.VolumeCreateRequest{VolumeName: "tf-imported", SizeTiB: 1, Region: "us-central-8"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(extra string) string {
		return provider + fmt.Sprintf(`
import {
  to = togetherai_shared_volume.data
  id = %q
}

resource "togetherai_shared_volume" "data" {
  name     = "tf-imported"
  size_tib = 1
  region   = "us-central-8"
  %s
}
`, v.VolumeID, extra)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_shared_volume.data", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("togetherai_shared_volume.data", "id", v.VolumeID),
			},
			{
				Config: cfg("is_lifecycle_independent = true"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_shared_volume.data", plancheck.ResourceActionDestroyBeforeCreate),
				}},
			},
		},
	})
}
