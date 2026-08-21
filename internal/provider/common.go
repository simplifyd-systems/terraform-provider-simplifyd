package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
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

// ── plan modifiers ────────────────────────────────────────────────────────────

// fillNullChildrenFromState carries state values into a nested block's
// Optional+Computed children when the configuration leaves them null.
//
// Terraform takes a nested object from configuration whole, so a child the
// practitioner never wrote plans as null rather than as "unknown, keep what the
// platform chose" — and on a block that forces replacement, that null is the
// difference between a no-op and destroying a running cluster. Terraform's own
// UseStateForUnknown does not cover this: a child omitted inside a written
// object is null, not unknown.
//
// Values the configuration does set are left exactly as written, so this only
// ever fills in what the platform, not the practitioner, decided.
type fillNullChildrenFromState struct{}

func (fillNullChildrenFromState) Description(_ context.Context) string {
	return "keeps platform-assigned values for nested attributes the configuration leaves unset"
}

func (m fillNullChildrenFromState) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (fillNullChildrenFromState) PlanModifyObject(
	ctx context.Context,
	req planmodifier.ObjectRequest,
	resp *planmodifier.ObjectResponse,
) {
	// Nothing to carry over on create or destroy.
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() ||
		req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}

	state := req.StateValue.Attributes()
	planned := make(map[string]attr.Value, len(req.PlanValue.Attributes()))
	changed := false

	for name, value := range req.PlanValue.Attributes() {
		prior, ok := state[name]
		// A child left out of the configuration arrives here either null or,
		// when it is Optional+Computed, already marked unknown. Both mean "the
		// practitioner did not choose this"; the configuration check is what
		// keeps a genuinely computed value (one referencing another resource)
		// out of this branch.
		if (value.IsNull() || value.IsUnknown()) && ok && !prior.IsNull() &&
			req.ConfigValue.Attributes()[name].IsNull() {
			planned[name] = prior
			changed = true
			continue
		}
		planned[name] = value
	}
	if !changed {
		return
	}

	obj, diags := types.ObjectValue(req.PlanValue.AttributeTypes(ctx), planned)
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		resp.PlanValue = obj
	}
}
