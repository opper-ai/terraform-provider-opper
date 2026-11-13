package provider

import (
	"context"
	"errors"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func apiKeyAllowedIPsSchema() schema.SetAttribute {
	return schema.SetAttribute{
		Optional: true, Computed: true, ElementType: types.StringType,
		PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
		Validators:          []validator.Set{setvalidator.SizeAtMost(100), apiKeyAllowedIPsValidator{}},
		MarkdownDescription: "Public IP addresses or CIDR ranges allowed to use the runtime key, up to 100 entries. Omit or set null to preserve an existing key's current policy, including on import. Explicitly configure the policy when replacing a restricted key, including rotation or manual replacement. An explicit empty set clears restrictions. Changes update the existing key without rotating its secret. IPv4 ranges must be /8 or narrower; IPv6 /16 or narrower. Runtime enforcement changes may take up to one minute.",
	}
}

type apiKeyAllowedIPsValidator struct{}

func (apiKeyAllowedIPsValidator) Description(context.Context) string {
	return "must contain public IP addresses or supported CIDR ranges"
}
func (v apiKeyAllowedIPsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (apiKeyAllowedIPsValidator) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	known := []string{}
	for _, element := range req.ConfigValue.Elements() {
		value, ok := element.(types.String)
		if !ok || value.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid allowed_ips", "Each allowed_ips entry must be a non-null public IP address or CIDR range.")
			return
		}
		if !value.IsUnknown() {
			known = append(known, value.ValueString())
		}
	}
	if _, err := client.NormalizeAllowedIPs(known); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid allowed_ips", err.Error())
	}
}

func apiKeyAllowedIPsInput(ctx context.Context, value types.Set) ([]string, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, errors.New("allowed_ips and all its entries must be known before updating the runtime key")
	}
	values := []string{}
	if value.ElementsAs(ctx, &values, false).HasError() {
		return nil, errors.New("allowed_ips must contain known, non-null IP addresses or CIDR ranges")
	}
	return client.NormalizeAllowedIPs(values)
}

// Keep the exact configured spellings when they describe the confirmed remote
// set. Rewriting known Terraform config (for example a masked CIDR) would fail
// apply consistency checks. Imports and actual drift use canonical API values.
func (m *apiKeyModel) setAllowedIPs(ctx context.Context, remote []string) error {
	if remote == nil {
		if !m.AllowedIPs.IsNull() && !m.AllowedIPs.IsUnknown() && len(m.AllowedIPs.Elements()) != 0 {
			return errors.New("Opper did not return allowed_ips metadata; the saved restrictions and key identity have been retained")
		}
		if m.AllowedIPs.IsUnknown() {
			m.AllowedIPs = types.SetNull(types.StringType)
		}
		return nil
	}
	canonical, err := client.NormalizeAllowedIPs(remote)
	if err != nil {
		return errors.New("Opper returned invalid allowed_ips metadata")
	}
	if !m.AllowedIPs.IsUnknown() && !m.AllowedIPs.IsNull() {
		previous, err := apiKeyAllowedIPsInput(ctx, m.AllowedIPs)
		if err == nil && slices.Equal(previous, canonical) {
			return nil
		}
	}
	if len(canonical) == 0 {
		canonical = []string{}
	}
	value, diags := types.SetValueFrom(ctx, types.StringType, canonical)
	if diags.HasError() {
		return errors.New("could not store runtime-key allowed_ips metadata")
	}
	m.AllowedIPs = value
	return nil
}
