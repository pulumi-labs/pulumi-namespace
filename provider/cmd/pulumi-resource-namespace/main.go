// Command pulumi-resource-namespace is the entry point for the Namespace Cloud
// Pulumi provider binary. It is invoked by the Pulumi engine and serves the
// provider gRPC protocol over stdin/stdout.
package main

import (
	"context"
	"os"

	"github.com/pulumi/autoregistry/lib/providers/namespace/provider/pkg/provider"
	"github.com/pulumi/autoregistry/lib/providers/namespace/provider/pkg/version"
	pgp "github.com/pulumi/pulumi-go-provider"
)

func main() {
	prov, err := provider.New()
	if err != nil {
		_, _ = os.Stderr.WriteString("failed to construct provider: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := pgp.RunProvider(context.Background(), "namespace", version.Version, prov); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
