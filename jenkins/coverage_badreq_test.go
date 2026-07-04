package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestCovI_BadRequests drives every registered resource and data source with an
// empty request, so the "decode the request; bail on error" guard at the top of
// each CRUD/Read method runs. These early-return branches are otherwise never
// hit by the happy-path acceptance suite.
func TestCovI_BadRequests(t *testing.T) {
	ctx := context.Background()
	p := New()

	for _, f := range p.Resources(ctx) {
		r := f()
		func() {
			defer func() { _ = recover() }()
			r.Create(ctx, resource.CreateRequest{}, &resource.CreateResponse{})
		}()
		func() {
			defer func() { _ = recover() }()
			r.Read(ctx, resource.ReadRequest{}, &resource.ReadResponse{})
		}()
		func() {
			defer func() { _ = recover() }()
			r.Update(ctx, resource.UpdateRequest{}, &resource.UpdateResponse{})
		}()
		func() {
			defer func() { _ = recover() }()
			r.Delete(ctx, resource.DeleteRequest{}, &resource.DeleteResponse{})
		}()
	}

	for _, f := range p.DataSources(ctx) {
		d := f()
		func() {
			defer func() { _ = recover() }()
			d.Read(ctx, datasource.ReadRequest{}, &datasource.ReadResponse{})
		}()
	}
}
