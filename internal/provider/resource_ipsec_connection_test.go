package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The settings update endpoint ignores the key, and sending it there would make
// a plain edit look like a rotation. Only Create and RotatePSK carry it.
func TestIPsecConnectionInputOmitsPSK(t *testing.T) {
	m := ipsecConnectionModel{
		Name:          types.StringValue("partner-a"),
		RemoteGateway: types.StringValue("203.0.113.10"),
		RemoteSubnets: listOf(t, "192.168.50.0/24"),
		LocalSubnets:  types.ListNull(types.StringType),
		PSK:           types.StringValue("super-secret"),
	}

	in, diags := m.toInput(context.Background())
	if diags.HasError() {
		t.Fatalf("toInput: %v", diags)
	}
	if in.PSK != "" {
		t.Errorf("PSK = %q, want empty", in.PSK)
	}
	if len(in.RemoteSubnets) != 1 || in.RemoteSubnets[0] != "192.168.50.0/24" {
		t.Errorf("RemoteSubnets = %v", in.RemoteSubnets)
	}
	if in.LocalSubnets != nil {
		t.Errorf("LocalSubnets = %v, want nil for an inherited set", in.LocalSubnets)
	}
}

// An optional field the config never set must stay null when the API reports it
// empty, or every plan shows "" drift on it.
func TestOptionalString(t *testing.T) {
	if got := optionalString(types.StringNull(), ""); !got.IsNull() {
		t.Errorf("unset + empty = %v, want null", got)
	}
	if got := optionalString(types.StringNull(), "peer.example.com"); got.ValueString() != "peer.example.com" {
		t.Errorf("unset + value = %v", got)
	}
	if got := optionalString(types.StringValue("old"), ""); !got.IsNull() && got.ValueString() != "" {
		t.Errorf("set + cleared = %v, want empty string", got)
	}
}

func listOf(t *testing.T, vals ...string) types.List {
	t.Helper()
	list, diags := types.ListValueFrom(context.Background(), types.StringType, vals)
	if diags.HasError() {
		t.Fatalf("building list: %v", diags)
	}
	return list
}
