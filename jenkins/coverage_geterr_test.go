package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestCovR_GetErrors drives every resource/data source with a request whose Raw
// is the wrong shape (a string where an object is expected), so req.*.Get fails
// and the "bail out after decoding the request" guard at the top of each method
// runs — a branch the happy-path acceptance suite never reaches.
func TestCovR_GetErrors(t *testing.T) {
	ctx := context.Background()
	p := New()
	bad := tftypes.NewValue(tftypes.String, "not-an-object")

	for _, f := range p.Resources(ctx) {
		r := f()
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		s := sr.Schema
		badPlan := tfsdk.Plan{Schema: s, Raw: bad}
		badState := tfsdk.State{Schema: s, Raw: bad}
		emptyState := tfsdk.State{Schema: s}

		run := func(fn func()) {
			defer func() { _ = recover() }()
			fn()
		}
		run(func() {
			r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &resource.CreateResponse{State: emptyState})
		})
		run(func() {
			r.Read(ctx, resource.ReadRequest{State: badState}, &resource.ReadResponse{State: emptyState})
		})
		run(func() {
			r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: badState}, &resource.UpdateResponse{State: emptyState})
		})
		run(func() {
			r.Delete(ctx, resource.DeleteRequest{State: badState}, &resource.DeleteResponse{State: emptyState})
		})
	}

	for _, f := range p.DataSources(ctx) {
		d := f()
		var sr datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sr)
		s := sr.Schema
		func() {
			defer func() { _ = recover() }()
			d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: bad}}, &datasource.ReadResponse{State: tfsdk.State{Schema: s}})
		}()
	}
}
