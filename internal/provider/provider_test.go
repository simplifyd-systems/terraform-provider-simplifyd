package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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

// The provider block is deliberately tiny: a token, and optionally an
// environment. api_url is not configurable — the endpoint is fixed — and
// workspace/project come from the token, so offering them would only let a
// config contradict the credential it authenticates with.
func TestProviderSchemaOnlyExposesTokenAndEnv(t *testing.T) {
	var pResp fwprovider.SchemaResponse
	New("test")().Schema(context.Background(), fwprovider.SchemaRequest{}, &pResp)

	want := map[string]bool{"api_token": true, "env": true}
	for name := range pResp.Schema.Attributes {
		if !want[name] {
			t.Errorf("unexpected provider attribute %q", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("missing provider attribute %q", name)
	}
}

// Resources must not offer workspace or project either, for the same reason.
func TestResourcesDoNotExposeWorkspaceOrProject(t *testing.T) {
	ctx := context.Background()
	p := New("test")()

	for _, newResource := range p.Resources(ctx) {
		r := newResource()
		var mResp resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "simplifyd"}, &mResp)

		var sResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sResp)
		for _, banned := range []string{"workspace", "project"} {
			if _, ok := sResp.Schema.Attributes[banned]; ok {
				t.Errorf("%s: exposes %q, which comes from the token", mResp.TypeName, banned)
			}
		}
	}
}

func TestResolveScope(t *testing.T) {
	projectScoped := &providerData{workspace: "ws", project: "proj", env: "staging"}
	envScoped := &providerData{workspace: "ws", project: "proj", env: "production", envPinned: true}

	t.Run("resource env wins over provider default", func(t *testing.T) {
		s, diags := resolveScope(projectScoped, types.StringValue("qa"), true)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if s.env != "qa" || s.workspace != "ws" || s.project != "proj" {
			t.Errorf("got %+v", s)
		}
	})

	t.Run("token env is used when nothing is set", func(t *testing.T) {
		s, diags := resolveScope(envScoped, types.StringNull(), true)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if s.env != "production" {
			t.Errorf("env = %q, want production", s.env)
		}
	})

	// Silently retargeting the token's own environment would apply changes to an
	// environment the configuration does not name.
	t.Run("env outside a pinned token's scope is an error", func(t *testing.T) {
		_, diags := resolveScope(envScoped, types.StringValue("staging"), true)
		if !diags.HasError() {
			t.Error("expected an error for an env the token is not scoped to")
		}
	})

	t.Run("missing env is an error when one is required", func(t *testing.T) {
		_, diags := resolveScope(&providerData{workspace: "ws", project: "proj"}, types.StringNull(), true)
		if !diags.HasError() {
			t.Error("expected an error for a missing env")
		}
	})
}

// A personal JWT must be refused before any request is made, so the failure is
// a clear diagnostic rather than a confusing 401 partway through an apply.
func TestConfigureRejectsNonProjectTokens(t *testing.T) {
	t.Setenv("SIMPLIFYD_API_TOKEN", "eyJhbGciOiJSUzI1NiJ9.notatoken.sig")

	ctx := context.Background()
	p := New("test")()
	var sResp fwprovider.SchemaResponse
	p.Schema(ctx, fwprovider.SchemaRequest{}, &sResp)

	// An empty config: the token comes from the environment variable above.
	cfg := tfsdk.Config{
		Schema: sResp.Schema,
		Raw: tftypes.NewValue(sResp.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
			"api_token": tftypes.NewValue(tftypes.String, nil),
			"env":       tftypes.NewValue(tftypes.String, nil),
		}),
	}

	var resp fwprovider.ConfigureResponse
	p.Configure(ctx, fwprovider.ConfigureRequest{Config: cfg}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a JWT to be rejected")
	}
	if resp.ResourceData != nil {
		t.Error("a rejected credential must not produce provider data")
	}
}
