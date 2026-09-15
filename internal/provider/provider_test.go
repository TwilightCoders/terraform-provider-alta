package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestProviderServesSchema(t *testing.T) {
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("creating provider server: %v", err)
	}

	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("schema diagnostic: %s: %s", d.Summary, d.Detail)
		}
	}
}

func TestProviderMetadata(t *testing.T) {
	server, err := providerserver.NewProtocol6WithError(New("1.2.3")())()
	if err != nil {
		t.Fatalf("creating provider server: %v", err)
	}

	resp, err := server.GetMetadata(context.Background(), &tfprotov6.GetMetadataRequest{})
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if len(resp.Diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
}
