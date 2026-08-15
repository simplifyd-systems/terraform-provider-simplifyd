package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

func strVal(s string) types.String { return types.StringValue(s) }

// deleteHarness builds the state a Delete call reads from, wired to a stub API.
// Delete is the one CRUD method with no return value to assert on, so the tests
// assert on the request the provider made and on the diagnostics it produced.
type deleteHarness struct {
	server  *httptest.Server
	methods []string
	paths   []string
}

func newDeleteHarness(t *testing.T, status int, body string) *deleteHarness {
	t.Helper()
	h := &deleteHarness{}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.methods = append(h.methods, r.Method)
		h.paths = append(h.paths, r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *deleteHarness) providerData() *providerData {
	return &providerData{client: cloud.NewClient(cloud.WithBaseURL(h.server.URL))}
}

func envDeleteRequest(t *testing.T, r *environmentResource, id string) resource.DeleteRequest {
	t.Helper()
	var sResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sResp)

	state := tfsdk.State{Schema: sResp.Schema}
	diags := state.Set(context.Background(), &environmentModel{
		ID:   strVal(id),
		Name: strVal("production"),
		Slug: strVal("production"),
	})
	if diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	return resource.DeleteRequest{State: state}
}

func TestEnvDeleteCallsAPI(t *testing.T) {
	h := newDeleteHarness(t, http.StatusOK, `{"success":true}`)
	r := &environmentResource{pd: h.providerData()}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), envDeleteRequest(t, r, "ws/storefront/production"), &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
	if want := "/v1/workspaces/ws/projects/storefront/envs/production"; h.paths[0] != want {
		t.Errorf("path = %s, want %s", h.paths[0], want)
	}
}

// The last environment in a project cannot be deleted, and the provider cannot
// delete the project either — that is above a project token's authority. So the
// 409 is a real failure and must surface as one rather than a warning.
func TestEnvDeleteLastEnvironmentIsAnError(t *testing.T) {
	h := newDeleteHarness(t, http.StatusConflict,
		`{"success":false,"message":"ErrLastEnvironment: delete the project instead"}`)
	r := &environmentResource{pd: h.providerData()}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), envDeleteRequest(t, r, "ws/storefront/production"), &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for the last-environment 409")
	}
}

// A 403 is not the last-environment case and must not be swallowed as one.
func TestEnvDeleteForbiddenIsAnError(t *testing.T) {
	h := newDeleteHarness(t, http.StatusForbidden,
		`{"message":"only a workspace owner can delete an environment"}`)
	r := &environmentResource{pd: h.providerData()}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), envDeleteRequest(t, r, "ws/storefront/production"), &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for 403")
	}
}
