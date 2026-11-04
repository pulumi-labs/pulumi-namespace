// Package provider implements the Namespace Cloud Pulumi provider.
//
// Resources:
//   - VaultObject — a versioned secret in the Namespace vault
//   - Tenant      — an IAM tenant: an isolated account with its own policies
//   - Instance    — an ephemeral compute instance (VM or container group)
//
// Functions:
//   - getTenants   — list IAM tenants
//   - getInstances — list compute instances
//
// The provider talks to the Namespace Cloud API (Connect RPC over HTTPS) using
// the schema-generated protobuf messages from buf.build/namespace/cloud. See
// client.go for the transport.
package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// ProviderConfig holds provider-level configuration shared across all resources.
type ProviderConfig struct {
	// Token is the Namespace Cloud bearer token.
	Token *string `pulumi:"token,optional" provider:"secret"`

	// Region selects the regional compute endpoint ({region}.compute.namespaceapis.com).
	Region *string `pulumi:"region,optional"`

	// ComputeEndpoint overrides the regional compute API base URL.
	ComputeEndpoint *string `pulumi:"computeEndpoint,optional"`

	// IamEndpoint overrides the IAM API base URL.
	IamEndpoint *string `pulumi:"iamEndpoint,optional"`

	// GlobalEndpoint overrides the global API base URL (vault, registry, ...).
	GlobalEndpoint *string `pulumi:"globalEndpoint,optional"`
}

// Annotate describes the provider config fields and wires their environment
// variable fallbacks; infer reads env defaults only from SetDefault, not from
// struct tags.
func (c *ProviderConfig) Annotate(a infer.Annotator) {
	a.SetDefault(&c.Token, nil, "NSC_TOKEN")
	a.SetDefault(&c.Region, nil, "NSC_REGION")
	a.Describe(&c.Token, "Namespace Cloud bearer token: a revokable token ('nsc token create'), a development "+
		"token ('nsc auth generate-dev-token'), or the result of an OIDC token exchange. "+
		"Falls back to the NSC_TOKEN environment variable.")
	a.Describe(&c.Region, "Region for compute resources, such as 'us' or 'eu'. Selects the "+
		"{region}.compute.namespaceapis.com endpoint. Defaults to 'us'. Falls back to the NSC_REGION environment variable.")
	a.Describe(&c.ComputeEndpoint, "Full base URL of the compute API. A '{region}' placeholder is replaced with the region; "+
		"without one the URL is used for every region. "+
		"Intended for tests and private deployments. Defaults to https://{region}.compute.namespaceapis.com.")
	a.Describe(&c.IamEndpoint, "Full base URL of the IAM API. Defaults to https://iam.namespaceapis.com.")
	a.Describe(&c.GlobalEndpoint, "Full base URL of the global API (vault, registry, egress policy). "+
		"Defaults to https://global.namespaceapis.com.")
}

// Configure validates the endpoint overrides before any request is made. The
// bearer token travels on every call, so a plain-http endpoint would send it in
// cleartext: https is required, except for loopback hosts (local test servers).
func (c *ProviderConfig) Configure(ctx context.Context) error {
	for name, v := range map[string]*string{
		"computeEndpoint": c.ComputeEndpoint,
		"iamEndpoint":     c.IamEndpoint,
		"globalEndpoint":  c.GlobalEndpoint,
	} {
		if err := validateEndpoint(name, strOr(v, "")); err != nil {
			return err
		}
	}
	return nil
}

func validateEndpoint(name, raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("namespace: %s %q is not a valid absolute URL", name, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
		return fmt.Errorf("namespace: %s %q uses http, which would send the bearer token in cleartext; use https "+
			"(http is only accepted for localhost, 127.0.0.1 and ::1)", name, raw)
	}
	return fmt.Errorf("namespace: %s %q must use https", name, raw)
}

func strOr(p *string, fallback string) string {
	if p == nil || *p == "" {
		return fallback
	}
	return *p
}

// clientFor builds an API client from the provider configuration. regionOverride
// (a resource-level region) wins over the provider region when non-empty.
func clientFor(ctx context.Context, regionOverride *string) (*nsClient, error) {
	cfg := infer.GetConfig[ProviderConfig](ctx)
	token := strOr(cfg.Token, os.Getenv("NSC_TOKEN"))
	if token == "" {
		return nil, fmt.Errorf("namespace: an API token is required: set the provider 'token' " +
			"config (pulumi config set --secret namespace:token ...) or the NSC_TOKEN environment variable")
	}
	region := strOr(regionOverride, strOr(cfg.Region, os.Getenv("NSC_REGION")))
	return newNSClient(token, region, endpoints{
		Compute: strOr(cfg.ComputeEndpoint, ""),
		IAM:     strOr(cfg.IamEndpoint, ""),
		Global:  strOr(cfg.GlobalEndpoint, ""),
	}), nil
}

// New returns the configured Namespace Cloud Pulumi provider.
func New() (pgp.Provider, error) {
	return infer.NewProviderBuilder().
		WithDisplayName("Namespace Cloud").
		WithDescription(
			"Manage Namespace Cloud (namespace.so) resources: ephemeral compute instances, "+
				"vault secrets, IAM tenants and tokens, GitHub runner profiles, egress policy and registry policy, as infrastructure with Pulumi.",
		).
		WithHomepage("https://namespace.so").
		WithRepository("https://github.com/pulumi/autoregistry").
		WithGoImportPath("github.com/pulumi/pulumi-namespace/sdk/go/namespace").
		WithPublisher("Pulumi").
		WithLicense("Apache-2.0").
		WithKeywords("pulumi", "namespace", "namespace-cloud", "ci", "sandbox", "ephemeral-compute", "category/cloud").
		WithModuleMap(map[tokens.ModuleName]tokens.ModuleName{
			"provider": "index",
		}).
		WithConfig(infer.Config(&ProviderConfig{})).
		WithResources(
			infer.Resource[*VaultObject, VaultObjectArgs, VaultObjectState](&VaultObject{}),
			infer.Resource[*Tenant, TenantArgs, TenantState](&Tenant{}),
			infer.Resource[*Instance, InstanceArgs, InstanceState](&Instance{}),
			infer.Resource[*GithubProfile, GithubProfileArgs, GithubProfileState](&GithubProfile{}),
			infer.Resource[*EgressPolicy, EgressPolicyArgs, EgressPolicyState](&EgressPolicy{}),
			infer.Resource[*BuildkiteQueue, BuildkiteQueueArgs, BuildkiteQueueState](&BuildkiteQueue{}),
			infer.Resource[*RegistryDefaultPolicy, RegistryDefaultPolicyArgs, RegistryDefaultPolicyState](&RegistryDefaultPolicy{}),
			infer.Resource[*RegistryRepositoryPolicy, RegistryRepositoryPolicyArgs, RegistryRepositoryPolicyState](&RegistryRepositoryPolicy{}),
			infer.Resource[*TenantGroup, TenantGroupArgs, TenantGroupState](&TenantGroup{}),
			infer.Resource[*RevokableToken, RevokableTokenArgs, RevokableTokenState](&RevokableToken{}),
			infer.Resource[*Blueprint, BlueprintArgs, BlueprintState](&Blueprint{}),
			infer.Resource[*SharedImage, SharedImageArgs, SharedImageState](&SharedImage{}),
			infer.Resource[*InstanceIngress, InstanceIngressArgs, InstanceIngressState](&InstanceIngress{}),
		).
		WithFunctions(
			infer.Function[*GetTenants, GetTenantsArgs, GetTenantsResult](&GetTenants{}),
			infer.Function[*GetInstances, GetInstancesArgs, GetInstancesResult](&GetInstances{}),
			infer.Function[*GetGithubProfiles, GetGithubProfilesArgs, GetGithubProfilesResult](&GetGithubProfiles{}),
			infer.Function[*GetTenantGroups, GetTenantGroupsArgs, GetTenantGroupsResult](&GetTenantGroups{}),
			infer.Function[*GetBlueprints, GetBlueprintsArgs, GetBlueprintsResult](&GetBlueprints{}),
			infer.Function[*GetRegistryRepositories, GetRegistryRepositoriesArgs, GetRegistryRepositoriesResult](&GetRegistryRepositories{}),
			infer.Function[*GetRegistryImages, GetRegistryImagesArgs, GetRegistryImagesResult](&GetRegistryImages{}),
			infer.Function[*GetUsage, GetUsageArgs, GetUsageResult](&GetUsage{}),
			infer.Function[*GetVolumes, GetVolumesArgs, GetVolumesResult](&GetVolumes{}),
			infer.Function[*GetGithubJobs, GetGithubJobsArgs, GetGithubJobsResult](&GetGithubJobs{}),
			infer.Function[*GetVaultObject, GetVaultObjectArgs, GetVaultObjectResult](&GetVaultObject{}),
			infer.Function[*GetVaultObjects, GetVaultObjectsArgs, GetVaultObjectsResult](&GetVaultObjects{}),
			infer.Function[*GetTenant, GetTenantArgs, TenantState](&GetTenant{}),
			infer.Function[*GetInstance, GetInstanceArgs, GetInstanceResult](&GetInstance{}),
			infer.Function[*GetGithubProfile, GetGithubProfileArgs, GithubProfileState](&GetGithubProfile{}),
			infer.Function[*GetTenantGroup, GetTenantGroupArgs, TenantGroupState](&GetTenantGroup{}),
			infer.Function[*GetBlueprint, GetBlueprintArgs, BlueprintState](&GetBlueprint{}),
			infer.Function[*GetEgressPolicy, GetEgressPolicyArgs, EgressPolicyInfo](&GetEgressPolicy{}),
			infer.Function[*GetEgressPolicies, GetEgressPoliciesArgs, GetEgressPoliciesResult](&GetEgressPolicies{}),
			infer.Function[*GetBuildkiteQueue, GetBuildkiteQueueArgs, BuildkiteQueueInfo](&GetBuildkiteQueue{}),
			infer.Function[*GetBuildkiteQueues, GetBuildkiteQueuesArgs, GetBuildkiteQueuesResult](&GetBuildkiteQueues{}),
			infer.Function[*GetRegistryDefaultPolicy, GetRegistryDefaultPolicyArgs, GetRegistryDefaultPolicyResult](&GetRegistryDefaultPolicy{}),
			infer.Function[*GetRegistryRepositoryPolicy, GetRegistryRepositoryPolicyArgs, GetRegistryRepositoryPolicyResult](&GetRegistryRepositoryPolicy{}),
			infer.Function[*GetRevokableToken, GetRevokableTokenArgs, RevokableTokenInfo](&GetRevokableToken{}),
			infer.Function[*GetRevokableTokens, GetRevokableTokensArgs, GetRevokableTokensResult](&GetRevokableTokens{}),
			infer.Function[*GetTenantMembers, GetTenantMembersArgs, GetTenantMembersResult](&GetTenantMembers{}),
			infer.Function[*GetTenantPolicies, GetTenantPoliciesArgs, GetTenantPoliciesResult](&GetTenantPolicies{}),
			infer.Function[*GetSharedImage, GetSharedImageArgs, SharedImageInfo](&GetSharedImage{}),
			infer.Function[*GetSharedImages, GetSharedImagesArgs, GetSharedImagesResult](&GetSharedImages{}),
			infer.Function[*GetInstanceIngress, GetInstanceIngressArgs, GetInstanceIngressResult](&GetInstanceIngress{}),
		).
		Build()
}
