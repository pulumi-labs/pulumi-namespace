package provider_test

import (
	"context"
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/pulumi/autoregistry/lib/providers/namespace/provider/pkg/provider"
)

func mustSchema(t *testing.T) schema.PackageSpec {
	t.Helper()
	prov, err := provider.New()
	if err != nil {
		t.Fatalf("provider.New() returned error: %v", err)
	}
	spec, err := pgp.GetSchema(context.Background(), "namespace", "0.1.0", prov)
	if err != nil {
		t.Fatalf("GetSchema failed: %v", err)
	}
	return spec
}

// Identity semantics (AGENTS.md "Native Provider Identity Semantics"): a
// mocked lifecycle test cannot catch a missing replaceOnChanges tag, so these
// tests read the generated schema the engine consults at diff time.
func TestReplaceOnChanges(t *testing.T) {
	spec := mustSchema(t)
	cases := map[string][]string{
		"namespace:index:VaultObject": {"description", "revealable", "labels"},
		"namespace:index:Tenant":      {"externalAccountId", "creatorId"},
		"namespace:index:Instance":    {"shape", "documentedPurpose", "labels", "interactive", "region", "containers"},
	}
	for tok, props := range cases {
		res, ok := spec.Resources[tok]
		if !ok {
			t.Fatalf("%s not found in schema", tok)
		}
		for _, p := range props {
			in, ok := res.InputProperties[p]
			if !ok {
				t.Errorf("%s: input %q missing", tok, p)
				continue
			}
			if !in.ReplaceOnChanges {
				t.Errorf("%s.%s must be replaceOnChanges: the API has no in-place update for it", tok, p)
			}
		}
	}
}

// Negative cases: properties with an in-place update path must NOT force
// replacement, or every edit would destroy and recreate the resource.
func TestUpdatableInputsAreNotReplaceOnChanges(t *testing.T) {
	spec := mustSchema(t)
	cases := map[string][]string{
		"namespace:index:VaultObject": {"value"},
		"namespace:index:Tenant":      {"visibleName", "labels", "policies"},
		"namespace:index:Instance":    {"deadline"},
	}
	for tok, props := range cases {
		res := spec.Resources[tok]
		for _, p := range props {
			in, ok := res.InputProperties[p]
			if !ok {
				t.Errorf("%s: input %q missing", tok, p)
				continue
			}
			if in.ReplaceOnChanges {
				t.Errorf("%s.%s must not be replaceOnChanges: it updates in place", tok, p)
			}
		}
	}
}

func TestSecretsAreMarked(t *testing.T) {
	spec := mustSchema(t)
	if !spec.Resources["namespace:index:VaultObject"].InputProperties["value"].Secret {
		t.Error("VaultObject.value must be marked secret")
	}
	if !spec.Config.Variables["token"].Secret {
		t.Error("provider token must be marked secret")
	}
}

func TestSchemaHasExpectedSurface(t *testing.T) {
	spec := mustSchema(t)
	for _, tok := range []string{"namespace:index:VaultObject", "namespace:index:Tenant", "namespace:index:Instance"} {
		if _, ok := spec.Resources[tok]; !ok {
			t.Errorf("resource %s missing", tok)
		}
	}
	for _, tok := range []string{"namespace:index:getTenants", "namespace:index:getInstances"} {
		if _, ok := spec.Functions[tok]; !ok {
			t.Errorf("function %s missing", tok)
		}
	}
	for _, k := range []string{"token", "region", "computeEndpoint", "iamEndpoint", "globalEndpoint"} {
		if _, ok := spec.Config.Variables[k]; !ok {
			t.Errorf("config %q missing", k)
		}
	}
}

func TestRevokableTokenBearerIsSecretOutput(t *testing.T) {
	spec := mustSchema(t)
	res := spec.Resources["namespace:index:RevokableToken"]
	if !res.Properties["bearerToken"].Secret {
		t.Error("RevokableToken.bearerToken output must be marked secret")
	}
}
