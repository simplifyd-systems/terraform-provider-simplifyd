package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var kafkaObjectTypes = map[string]attr.Type{
	"mode":     types.StringType,
	"brokers":  types.Int64Type,
	"replicas": types.Int64Type,
}

func kafkaObject(t *testing.T, mode attr.Value, brokers attr.Value, replicas attr.Value) types.Object {
	t.Helper()
	obj, diags := types.ObjectValue(kafkaObjectTypes, map[string]attr.Value{
		"mode":     mode,
		"brokers":  brokers,
		"replicas": replicas,
	})
	if diags.HasError() {
		t.Fatalf("building object: %v", diags)
	}
	return obj
}

// A block that forces replacement must not read an omitted child as a change:
// on a Kafka service that would destroy a running cluster over a broker count
// the practitioner never wrote.
func TestFillNullChildrenFromState(t *testing.T) {
	ctx := context.Background()
	state := kafkaObject(t, types.StringValue("standalone"), types.Int64Value(1), types.Int64Value(1))

	for _, tc := range []struct {
		name         string
		config       types.Object
		plan         types.Object
		wantBrokers  attr.Value
		wantModified bool
	}{
		{
			name:         "omitted child is filled from state",
			config:       kafkaObject(t, types.StringValue("standalone"), types.Int64Null(), types.Int64Null()),
			plan:         kafkaObject(t, types.StringValue("standalone"), types.Int64Unknown(), types.Int64Unknown()),
			wantBrokers:  types.Int64Value(1),
			wantModified: true,
		},
		{
			name:         "configured child is left alone",
			config:       kafkaObject(t, types.StringValue("cluster"), types.Int64Value(3), types.Int64Null()),
			plan:         kafkaObject(t, types.StringValue("cluster"), types.Int64Value(3), types.Int64Unknown()),
			wantBrokers:  types.Int64Value(3),
			wantModified: true,
		},
		{
			name:        "nothing to fill",
			config:      kafkaObject(t, types.StringValue("cluster"), types.Int64Value(3), types.Int64Value(3)),
			plan:        kafkaObject(t, types.StringValue("cluster"), types.Int64Value(3), types.Int64Value(3)),
			wantBrokers: types.Int64Value(3),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := planmodifier.ObjectResponse{PlanValue: tc.plan}
			fillNullChildrenFromState{}.PlanModifyObject(ctx, planmodifier.ObjectRequest{
				StateValue:  state,
				ConfigValue: tc.config,
				PlanValue:   tc.plan,
			}, &resp)

			if resp.Diagnostics.HasError() {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if got := resp.PlanValue.Attributes()["brokers"]; !got.Equal(tc.wantBrokers) {
				t.Errorf("brokers = %v, want %v", got, tc.wantBrokers)
			}
			if tc.wantModified && resp.PlanValue.Attributes()["replicas"].IsUnknown() {
				t.Errorf("replicas left unknown; state value should have been carried over")
			}
		})
	}
}

// On create there is no prior state to carry anything over from, and the
// modifier must leave the plan exactly as Terraform built it.
func TestFillNullChildrenFromStateOnCreate(t *testing.T) {
	plan := kafkaObject(t, types.StringValue("standalone"), types.Int64Unknown(), types.Int64Unknown())
	resp := planmodifier.ObjectResponse{PlanValue: plan}

	fillNullChildrenFromState{}.PlanModifyObject(context.Background(), planmodifier.ObjectRequest{
		StateValue:  types.ObjectNull(kafkaObjectTypes),
		ConfigValue: kafkaObject(t, types.StringValue("standalone"), types.Int64Null(), types.Int64Null()),
		PlanValue:   plan,
	}, &resp)

	if !resp.PlanValue.Equal(plan) {
		t.Errorf("plan changed on create: %v", resp.PlanValue)
	}
}
