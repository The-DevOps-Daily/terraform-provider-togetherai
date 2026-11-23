package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func configureResource(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. This is a bug in the provider.", req.ProviderData))
		return nil
	}
	return data
}

func configureDataSource(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. This is a bug in the provider.", req.ProviderData))
		return nil
	}
	return data
}

// errWaitTimeout is wrapped by waitFor when the deadline passes.
var errWaitTimeout = errors.New("timed out")

// waitFor calls check every interval until it reports done, returns an
// error, or ctx ends. status describes the last observed state and goes into
// the timeout error.
func waitFor(ctx context.Context, interval time.Duration, check func(ctx context.Context) (done bool, status string, err error)) error {
	last := ""
	for {
		done, status, err := check(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w waiting (last status %q): %v", errWaitTimeout, last, err)
			}
			return err
		}
		if status != "" {
			last = status
		}
		if done {
			return nil
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("%w waiting (last status %q)", errWaitTimeout, last)
		case <-t.C:
		}
	}
}

// replaceIfStateKnown marks an attribute RequiresReplace unless the resource
// was imported and the state has no value yet. Create-only fields that the API
// does not return are null after an import; adding them to the configuration
// then is an in-place state update, not a replacement. ImportState sets the
// importedKey private state flag and Update clears it, so a field left null
// at create and added later still replaces the resource.
const replaceIfStateKnownDesc = "Changing this value replaces the resource (except right after an import, when the value is not yet known)."

const importedKey = "imported"

var importedValue = []byte("true")

type privateKeyGetter interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

func requiresReplaceUnlessImported(ctx context.Context, stateNull bool, private privateKeyGetter) bool {
	if !stateNull {
		return true
	}
	v, _ := private.GetKey(ctx, importedKey)
	return len(v) == 0
}

func stringReplaceIfStateKnown() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
		resp.RequiresReplace = requiresReplaceUnlessImported(ctx, req.StateValue.IsNull(), req.Private)
	}, replaceIfStateKnownDesc, replaceIfStateKnownDesc)
}

func int64ReplaceIfStateKnown() planmodifier.Int64 {
	return int64planmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.Int64Request, resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
		resp.RequiresReplace = requiresReplaceUnlessImported(ctx, req.StateValue.IsNull(), req.Private)
	}, replaceIfStateKnownDesc, replaceIfStateKnownDesc)
}

func boolReplaceIfStateKnown() planmodifier.Bool {
	return boolplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.BoolRequest, resp *boolplanmodifier.RequiresReplaceIfFuncResponse) {
		resp.RequiresReplace = requiresReplaceUnlessImported(ctx, req.StateValue.IsNull(), req.Private)
	}, replaceIfStateKnownDesc, replaceIfStateKnownDesc)
}

func objectReplaceIfStateKnown() planmodifier.Object {
	return objectplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
		resp.RequiresReplace = requiresReplaceUnlessImported(ctx, req.StateValue.IsNull(), req.Private)
	}, replaceIfStateKnownDesc, replaceIfStateKnownDesc)
}

// strOrNull maps "" to null.
// planReplaces reports whether the plan replaces the resource, by the same
// rules as the attribute plan modifiers: a change to an `always` attribute
// replaces it, and a change to an `unlessImported` one does unless the
// resource was just imported with no value there. Resource ModifyPlan methods
// use it to apply update-only checks to in-place updates only.
func planReplaces(ctx context.Context, req resource.ModifyPlanRequest, always, unlessImported []string) bool {
	var planned, prior map[string]tftypes.Value
	if req.Plan.Raw.As(&planned) != nil || req.State.Raw.As(&prior) != nil {
		return false
	}
	changed := func(a string) bool {
		p := planned[a]
		return p.IsFullyKnown() && !p.Equal(prior[a])
	}
	for _, a := range always {
		if changed(a) {
			return true
		}
	}
	for _, a := range unlessImported {
		if changed(a) && requiresReplaceUnlessImported(ctx, prior[a].IsNull(), req.Private) {
			return true
		}
	}
	return false
}

// markImported sets the import flag that replaceIfStateKnown reads.
func markImported(ctx context.Context, resp *resource.ImportStateResponse) {
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, importedKey, importedValue)...)
	}
}

// clearImported drops the import flag once an apply has reconciled the
// configuration with the imported resource.
func clearImported(ctx context.Context, resp *resource.UpdateResponse) {
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, importedKey, nil)...)
	}
}

func strOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// keepTime returns prior when it names the same instant as api, so a
// different RFC 3339 spelling from the API is not reported as drift.
func keepTime(prior types.String, api string) types.String {
	if api == "" {
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() {
		a, errA := time.Parse(time.RFC3339Nano, prior.ValueString())
		b, errB := time.Parse(time.RFC3339Nano, api)
		if errA == nil && errB == nil && a.Equal(b) {
			return prior
		}
	}
	return types.StringValue(api)
}

// lastSegment returns the part after the last "/".
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// keepName keeps the configured short or qualified name when the API returns
// the qualified form "<project_slug>/.../<name>".
func keepName(prior types.String, apiQualified string) types.String {
	if !prior.IsNull() && !prior.IsUnknown() {
		p := prior.ValueString()
		if p == apiQualified || (!strings.Contains(p, "/") && p == lastSegment(apiQualified)) {
			return prior
		}
		if strings.Contains(p, "/") {
			return types.StringValue(apiQualified)
		}
	}
	return types.StringValue(lastSegment(apiQualified))
}

// splitImportID splits "a/b/c" into parts and checks the count is one of want.
func splitImportID(id string, want ...int) ([]string, error) {
	parts := strings.Split(id, "/")
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("import ID %q has an empty part", id)
		}
	}
	for _, n := range want {
		if len(parts) == n {
			return parts, nil
		}
	}
	return nil, fmt.Errorf("import ID %q has %d parts", id, len(parts))
}

func i64p(v int64) *int64 { return &v }
func boolp(v bool) *bool  { return &v }
