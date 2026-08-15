package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

func pathRoot(name string) path.Path { return path.Root(name) }

// scope is the resolved workspace/project/env triple a resource operates in.
type scope struct {
	workspace string
	project   string
	env       string
}

// resolveScope merges per-resource scope attributes with the provider defaults.
// Every resource in this provider accepts optional workspace/project/env
// attributes so a single config can span more than one project.
func resolveScope(pd *providerData, ws, proj, env types.String, needEnv bool) (scope, diag.Diagnostics) {
	var diags diag.Diagnostics
	s := scope{
		workspace: firstNonEmpty(ws.ValueString(), pd.workspace),
		project:   firstNonEmpty(proj.ValueString(), pd.project),
		env:       firstNonEmpty(env.ValueString(), pd.env),
	}
	if s.workspace == "" {
		diags.AddAttributeError(pathRoot("workspace"), "Missing workspace",
			"Set `workspace` on the resource or a default on the provider block.")
	}
	if s.project == "" {
		diags.AddAttributeError(pathRoot("project"), "Missing project",
			"Set `project` on the resource or a default on the provider block.")
	}
	if needEnv && s.env == "" {
		diags.AddAttributeError(pathRoot("env"), "Missing environment",
			"Set `env` on the resource or a default on the provider block.")
	}
	return s, diags
}

// services returns a ServicesClient bound to the resolved scope.
func (s scope) services(c *cloud.Client) *cloud.ServicesClient {
	return c.Workspace(s.workspace).Project(s.project).Env(s.env).Services()
}

// scopeAttrs are the shared optional scope attributes mixed into each resource
// schema. Defined once so the attribute names and docs never drift apart.
const (
	workspaceDoc = "Workspace slug. Defaults to the provider's `workspace`."
	projectDoc   = "Project slug. Defaults to the provider's `project`."
	envDoc       = "Environment slug. Defaults to the provider's `env`."
)

// ── composite IDs ─────────────────────────────────────────────────────────────
//
// Simplifyd identifies resources by slug, which is only unique within its
// parent scope. Terraform needs one opaque string, so IDs are the full path.
// This is also what `terraform import` accepts.

func makeID(parts ...string) string { return strings.Join(parts, "/") }

func parseID(id string, n int) ([]string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != n {
		return nil, fmt.Errorf("expected %d path segments, got %d", n, len(parts))
	}
	for i, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("segment %d is empty", i+1)
		}
	}
	return parts, nil
}

// gone reports whether err means the remote object no longer exists, in which
// case the caller should drop it from state rather than fail the plan.
func gone(err error) bool { return cloud.IsNotFound(err) }
