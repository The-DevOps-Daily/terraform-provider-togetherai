package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type multipleOfValidator struct{ n int64 }

// multipleOf8 checks a GPU count is a whole number of 8-GPU nodes.
func multipleOf8() validator.Int64 { return multipleOfValidator{n: 8} }

func (v multipleOfValidator) Description(context.Context) string {
	return fmt.Sprintf("value must be a multiple of %d", v.n)
}

func (v multipleOfValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v multipleOfValidator) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueInt64()%v.n != 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid GPU count",
			fmt.Sprintf("%s must be a multiple of %d (one node has 8 GPUs), got %d.", req.Path, v.n, req.ConfigValue.ValueInt64()))
	}
}

type inactiveTimeoutValidator struct{}

// inactiveTimeout checks the deployment idle timeout: 0 (off) or 30-1440.
func inactiveTimeout() validator.Int64 { return inactiveTimeoutValidator{} }

func (inactiveTimeoutValidator) Description(context.Context) string {
	return "value must be 0 (disabled) or between 30 and 1440 minutes"
}

func (v inactiveTimeoutValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (inactiveTimeoutValidator) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	n := req.ConfigValue.ValueInt64()
	if n != 0 && (n < 30 || n > 1440) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid inactive timeout",
			fmt.Sprintf("inactive_timeout must be 0 (disabled) or between 30 and 1440 minutes, got %d.", n))
	}
}

var (
	// volumeNameRe: "Volume names can contain only lowercase letters, digits,
	// hyphens, and dots", up to 44 characters in the console.
	volumeNameRe = regexp.MustCompile(`^[a-z0-9.-]{1,44}$`)
	// durationRe is the protobuf Duration JSON pattern from DE.Autoscaling.
	durationRe = regexp.MustCompile(`^-?(?:0|[1-9][0-9]{0,11})(?:\.[0-9]{1,9})?s$`)
	// modelRe and configRe are the resource-name forms from
	// DE.CreateDeploymentRequest.
	modelRe  = regexp.MustCompile(`^projects/[^/]+/models/[^/]+(/revisions/[^/]+)?$`)
	configRe = regexp.MustCompile(`^projects/[^/]+/configs/[^/]+$`)
)

type rfc3339Validator struct{}

// rfc3339 checks a timestamp parses as RFC 3339.
func rfc3339() validator.String { return rfc3339Validator{} }

func (rfc3339Validator) Description(context.Context) string {
	return "value must be an RFC 3339 timestamp, such as 2026-12-01T00:00:00Z"
}

func (v rfc3339Validator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (rfc3339Validator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timestamp", fmt.Sprintf("%s must be an RFC 3339 timestamp, such as 2026-12-01T00:00:00Z: %s", req.Path, err))
	}
}

// joinCodes renders values as "`a`, `b`" for schema descriptions.
func joinCodes(vals []string) string {
	return strings.Join(vals, "`, `")
}
