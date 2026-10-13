package provider_test

import (
	"context"
	"strings"
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"

	"github.com/pulumi/autoregistry/lib/providers/namespace/provider/pkg/provider"
)

// ---------------------------------------------------------------------------
// Registry metadata contract
//
// A mocked-construction test cannot catch a missing or fabricated Go SDK
// import path: it never exercises the schema.Metadata defaulting logic that
// pulumi-go-provider's infer.NewProviderBuilder runs when WithGoImportPath is
// left unset. These tests read the actual generated schema, the same
// artifact the Pulumi Registry and `pulumi package add` consult.
// ---------------------------------------------------------------------------

func mustMetadataSchema(t *testing.T) map[string]any {
	t.Helper()
	prov, err := provider.New()
	if err != nil {
		t.Fatalf("provider.New() returned error: %v", err)
	}
	spec, err := pgp.GetSchema(context.Background(), "namespace", "0.1.0", prov)
	if err != nil {
		t.Fatalf("GetSchema failed: %v", err)
	}
	return map[string]any{
		"displayName": spec.DisplayName,
		"publisher":   spec.Publisher,
		"license":     spec.License,
		"homepage":    spec.Homepage,
		"repository":  spec.Repository,
		"keywords":    spec.Keywords,
		"goLanguage":  string(spec.Language["go"]),
	}
}

func TestMetadata_DisplayNameIsHumanBranded(t *testing.T) {
	m := mustMetadataSchema(t)
	if got := m["displayName"].(string); got != "Namespace Cloud" {
		t.Errorf("DisplayName = %q, want %q", got, "Namespace Cloud")
	}
}

func TestMetadata_PublisherIsPulumi(t *testing.T) {
	m := mustMetadataSchema(t)
	if got := m["publisher"].(string); got != "Pulumi" {
		t.Errorf("Publisher = %q, want %q", got, "Pulumi")
	}
}

func TestMetadata_LicenseIsSet(t *testing.T) {
	m := mustMetadataSchema(t)
	if got := m["license"].(string); got != "Apache-2.0" {
		t.Errorf("License = %q, want %q", got, "Apache-2.0")
	}
}

func TestMetadata_HomepageAndRepositoryAreSet(t *testing.T) {
	m := mustMetadataSchema(t)
	if got := m["homepage"].(string); got == "" {
		t.Error("Homepage is empty")
	}
	if got := m["repository"].(string); got == "" {
		t.Error("Repository is empty")
	}
}

func TestMetadata_KeywordsStartWithPulumi(t *testing.T) {
	m := mustMetadataSchema(t)
	kws := m["keywords"].([]string)
	if len(kws) == 0 || kws[0] != "pulumi" {
		t.Errorf("Keywords = %v, want first element %q", kws, "pulumi")
	}
}

// TestMetadata_GoImportPathIsNotFabricated is the regression test for the
// defect this package was fixed for: without WithGoImportPath (and without
// WithRepository/WithNamespace set to values that happen to compute the right
// shape), pulumi-go-provider's infer.NewProviderBuilder fabricates
// "local-package/sdk/go/<pkg>", which is not a resolvable Go module path.
func TestMetadata_GoImportPathIsNotFabricated(t *testing.T) {
	m := mustMetadataSchema(t)
	goLang := m["goLanguage"].(string)
	if strings.Contains(goLang, "local-package") {
		t.Fatalf("Go SDK importBasePath was fabricated by the framework default: %s", goLang)
	}
	const want = "github.com/pulumi/pulumi-namespace/sdk/go/namespace"
	if !strings.Contains(goLang, want) {
		t.Errorf("Go SDK importBasePath = %s, want it to contain %q", goLang, want)
	}
}
