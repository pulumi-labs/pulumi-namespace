// Package version holds the provider version, injected at build time via ldflags.
package version

// Version is injected by the build system via:
//
//	-ldflags "-X github.com/pulumi/autoregistry/lib/providers/namespace/provider/pkg/version.Version=x.y.z"
var Version string
