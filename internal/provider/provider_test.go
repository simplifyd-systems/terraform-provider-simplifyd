package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// TestProviderSchemas builds every schema the provider exposes and fails on any
// diagnostics. Schema errors otherwise only surface at runtime, after a user has
// already installed the provider.
func TestProviderSchemas(t *testing.T) {
	ctx := context.Background()
	p := New("test")()

	var pResp fwprovider.SchemaResponse
	p.Schema(ctx, fwprovider.SchemaRequest{}, &pResp)
	if pResp.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", pResp.Diagnostics)
	}

	for _, newResource := range p.Resources(ctx) {
		r := newResource()

		var mResp resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "simplifyd"}, &mResp)
		if mResp.TypeName == "" {
			t.Errorf("%T: empty type name", r)
		}

		var sResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sResp)
		if sResp.Diagnostics.HasError() {
			t.Errorf("%s schema: %v", mResp.TypeName, sResp.Diagnostics)
			continue
		}
		assertHasID[fwresource.Attribute](t, mResp.TypeName, sResp.Schema.Attributes)

		if _, ok := r.(resource.ResourceWithImportState); !ok {
			t.Errorf("%s: does not implement ImportState", mResp.TypeName)
		}
	}

	for _, newDataSource := range p.DataSources(ctx) {
		d := newDataSource()

		var mResp datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "simplifyd"}, &mResp)

		var sResp datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sResp)
		if sResp.Diagnostics.HasError() {
			t.Errorf("%s schema: %v", mResp.TypeName, sResp.Diagnostics)
			continue
		}
		assertHasID[fwdatasource.Attribute](t, mResp.TypeName, sResp.Schema.Attributes)
	}
}

// Every resource needs a stable `id`, or drift detection and import both break.
func assertHasID[T any](t *testing.T, name string, attrs map[string]T) {
	t.Helper()
	if _, ok := attrs["id"]; !ok {
		t.Errorf("%s: missing required `id` attribute", name)
	}
}

func TestParseID(t *testing.T) {
	for _, tc := range []struct {
		id      string
		n       int
		wantErr bool
	}{
		{"ws/proj/env/svc", 4, false},
		{"ws/proj", 4, true},
		{"ws/proj/env/svc/extra", 4, true},
		{"ws//env/svc", 4, true},
		{"", 2, true},
	} {
		_, err := parseID(tc.id, tc.n)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseID(%q, %d): err=%v, wantErr=%v", tc.id, tc.n, err, tc.wantErr)
		}
	}
}
