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

func projectDeleteRequest(t *testing.T, r *projectResource, id string) resource.DeleteRequest {
	t.Helper()
	var sResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sResp)

	state := tfsdk.State{Schema: sResp.Schema}
	diags := state.Set(context.Background(), &projectModel{
		ID:          strVal(id),
		Workspace:   strVal("ws"),
		Name:        strVal("storefront"),
		Slug:        strVal("storefront"),
		NetworkSlug: strVal("abc1234"),
	})
	if diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	return resource.DeleteRequest{State: state}
}

func envDeleteRequest(t *testing.T, r *environmentResource, id string) resource.DeleteRequest {
	t.Helper()
	var sResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sResp)

	state := tfsdk.State{Schema: sResp.Schema}
	diags := state.Set(context.Background(), &environmentModel{
		ID:        strVal(id),
		Workspace: strVal("ws"),
		Project:   strVal("storefront"),
		Name:      strVal("production"),
		Slug:      strVal("production"),
	})
	if diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	return resource.DeleteRequest{State: state}
}

func TestProjectDeleteCallsAPI(t *testing.T) {
	h := newDeleteHarness(t, http.StatusOK, `{"success":true}`)
	r := &projectResource{pd: h.providerData()}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), projectDeleteRequest(t, r, "ws/storefront"), &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
	if len(h.paths) != 1 || h.paths[0] != "/v1/workspaces/ws/projects/storefront" {
		t.Errorf("unexpected requests: %v", h.paths)
	}
	if h.methods[0] != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", h.methods[0])
	}
}

// A failed teardown must leave the resource in state. Removing it would strand
// running services that nothing in the config points at any more.
func TestProjectDeleteKeepsStateOnFailure(t *testing.T) {
	h := newDeleteHarness(t, http.StatusInternalServerError, `{"message":"boom"}`)
	r := &projectResource{pd: h.providerData()}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), projectDeleteRequest(t, r, "ws/storefront"), &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic")
	}
}

// Already deleted out of band is the outcome destroy wanted.
func TestProjectDeleteTreats404AsSuccess(t *testing.T) {
	h := newDeleteHarness(t, http.StatusNotFound, `{"message":"not found"}`)
	r := &projectResource{pd: h.providerData()}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), projectDeleteRequest(t, r, "ws/storefront"), &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("404 should not fail destroy: %v", resp.Diagnostics)
	}
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

// Destroying a project and its only environment together must succeed: the env
// is destroyed first, gets refused, and the project teardown then removes it.
// A hard error here would make such a config impossible to destroy at all.
func TestEnvDeleteLastEnvironmentWarnsButSucceeds(t *testing.T) {
	h := newDeleteHarness(t, http.StatusConflict,
		`{"success":false,"message":"ErrLastEnvironment: delete the project instead"}`)
	r := &environmentResource{pd: h.providerData()}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), envDeleteRequest(t, r, "ws/storefront/production"), &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("409 must not fail destroy: %v", resp.Diagnostics)
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("expected exactly 1 warning, got %d", resp.Diagnostics.WarningsCount())
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
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("403 must not produce the last-environment warning: %v", resp.Diagnostics)
	}
}
