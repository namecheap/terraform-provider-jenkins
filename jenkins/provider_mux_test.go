package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-mux/tf5to6server"
	"github.com/hashicorp/terraform-plugin-mux/tf6muxserver"
)

// TestMuxedProviderSchemasMatch guards the invariant that the framework and
// SDKv2 provider config schemas are identical. tf6muxserver rejects mismatched
// provider schemas at GetProviderSchema time, which would break every
// acceptance/integration run — so any drift (e.g. a provider-level attribute
// added to one schema but not the other) must fail here first.
func TestMuxedProviderSchemasMatch(t *testing.T) {
	ctx := context.Background()

	upgraded, err := tf5to6server.UpgradeServer(ctx, Provider().GRPCProvider) //nolint:staticcheck
	if err != nil {
		t.Fatalf("UpgradeServer: %v", err)
	}

	muxServer, err := tf6muxserver.NewMuxServer(ctx,
		providerserver.NewProtocol6(New()),
		func() tfprotov6.ProviderServer { return upgraded },
	)
	if err != nil {
		t.Fatalf("NewMuxServer: %v", err)
	}

	resp, err := muxServer.ProviderServer().GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("mux schema error diagnostic: %s: %s", d.Summary, d.Detail)
		}
	}
}
