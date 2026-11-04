package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestProjectWebhookURLValidation(t *testing.T) {
	const prefix = "https://hooks.example.com/"
	for _, value := range []string{
		prefix,
		"https://hooks.example.com:443/events?token=private-query-token",
		"https://93.184.216.34/events",
		prefix + strings.Repeat("x", 2048-len(prefix)),
	} {
		var resp validator.StringResponse
		projectWebhookURLValidator{}.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("url"), ConfigValue: types.StringValue(value)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatal("valid HTTPS URL shape was rejected")
		}
	}
	for name, value := range map[string]string{
		"empty": "", "http": "http://hooks.example.com/events", "ftp": "ftp://hooks.example.com/events", "opaque": "https:hooks.example.com/events",
		"missing host": "https:///events", "empty host": "https://:443/events", "credentials": "https://private-query-token:password@hooks.example.com/events",
		"other port": "https://hooks.example.com:8443/events", "fragment": "https://hooks.example.com/events#private-query-token", "empty fragment": "https://hooks.example.com/events#",
		"leading whitespace": " " + prefix, "trailing whitespace": prefix + " ", "path whitespace": prefix + "a b", "control": prefix + "\nprivate-query-token", "backslash": prefix + `a\b`,
		"too long": prefix + strings.Repeat("x", 2049-len(prefix)), "invalid escape": prefix + "%zz",
	} {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			projectWebhookURLValidator{}.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("url"), ConfigValue: types.StringValue(value)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("invalid URL shape was accepted")
			}
			for _, diagnostic := range resp.Diagnostics.Errors() {
				if strings.Contains(diagnostic.Summary()+diagnostic.Detail(), "private-query-token") || strings.Contains(diagnostic.Detail(), "hooks.example.com") {
					t.Fatal("validation diagnostic echoed URL content")
				}
			}
		})
	}
	for _, value := range []types.String{types.StringUnknown(), types.StringNull()} {
		var resp validator.StringResponse
		projectWebhookURLValidator{}.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("url"), ConfigValue: value}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatal("validator rejected a deferred value")
		}
	}
}
