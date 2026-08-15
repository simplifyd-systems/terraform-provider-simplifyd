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

// resolveScope resolves the scope a resource operates in. Workspace and project
// always come from the token — they are not configurable — so only the
// environment is merged from the resource attribute, then the provider default,
// then the token's own environment scope.
func resolveScope(pd *providerData, env types.String, needEnv bool) (scope, diag.Diagnostics) {
	var diags diag.Diagnostics
	s := scope{
		workspace: pd.workspace,
		project:   pd.project,
		env:       firstNonEmpty(env.ValueString(), pd.env),
	}
	// An env-scoped token may only touch its own environment. Catching the
	// mismatch here turns a mid-apply 403 into a plan-time error.
	if pd.envPinned && !env.IsNull() && env.ValueString() != "" && env.ValueString() != pd.env {
		diags.AddAttributeError(pathRoot("env"), "Environment outside token scope",
			"The API token is scoped to environment "+pd.env+", but this resource sets env to "+
				env.ValueString()+". Use a project-scoped token to manage more than one environment.")
	}
	if needEnv && s.env == "" {
		diags.AddAttributeError(pathRoot("env"), "Missing environment",
			"Set `env` on the resource, a default on the provider block, or use an "+
				"environment-scoped token.")
	}
	return s, diags
}

// services returns a ServicesClient bound to the resolved scope.
func (s scope) services(c *cloud.Client) *cloud.ServicesClient {
	return c.Workspace(s.workspace).Project(s.project).Env(s.env).Services()
}

// envDoc is the shared documentation for the one scope attribute resources
// still expose. Defined once so it cannot drift between resources.
const envDoc = "Environment slug. Defaults to the provider's `env`, or to the " +
	"environment the API token is scoped to. Workspace and project are not " +
	"configurable — they come from the token."

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
