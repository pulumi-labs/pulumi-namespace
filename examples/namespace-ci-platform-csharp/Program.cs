using Pulumi;
using Pulumi.Namespace;
using Pulumi.Namespace.Inputs;
using System.Collections.Generic;
using System.Linq;

return await Deployment.RunAsync(() =>
{
    var config = new Pulumi.Config();
    var provider = new Provider("namespace", new ProviderArgs
    {
        Token = config.RequireSecret("nscToken"),
        Region = "us",
    });
    var opts = new CustomResourceOptions { Provider = provider };

    // Block all egress from CI jobs except what the rules allow.
    var egress = new EgressPolicy("ci-egress", new EgressPolicyArgs
    {
        Tag = "ci-locked-down",
        Description = "Only package registries and GitHub",
        Mode = "BLOCK",
        Rules =
        {
            new EgressRuleArgs
            {
                Op = "ALLOW",
                Matcher = new EgressMatcherArgs
                {
                    MatchDomains = { "github.com", "*.githubusercontent.com", "registry.npmjs.org" },
                },
            },
        },
    }, opts);

    // A GitHub runner profile that uses the egress policy.
    var profile = new GithubProfile("ci-runner", new GithubProfileArgs
    {
        Tag = "ci-medium",
        InstanceShape = new ProfileInstanceShapeArgs { VirtualCpu = 4, MemoryMegabytes = 8192 },
        NetworkPolicy = new ProfileNetworkPolicyArgs
        {
            Egress = "EGRESS_POLICY_TAG",
            EgressPolicyTag = egress.Tag,
        },
    }, opts);

    // A tenant group and a revokable token whose bearer value is a secret output.
    var group = new TenantGroup("ci-tenants", new TenantGroupArgs { Name = "ci-tenants" }, opts);
    var token = new RevokableToken("ci-token", new RevokableTokenArgs
    {
        Name = "ci-automation",
        Scope = "TENANT_SCOPE",
        Grants =
        {
            new TokenGrantArgs
            {
                ResourceType = "instance",
                ResourceId = "*",
                Actions = { "read" },
            },
        },
    }, opts);

    // Expire images in one repository after 14 days.
    var policy = new RegistryRepositoryPolicy("ci-images", new RegistryRepositoryPolicyArgs
    {
        Repository = "ci/runner",
        ExpirationSeconds = 14 * 24 * 3600,
        ExpirationEnforcement = "EXPIRATION_ENFORCEMENT_ENFORCE",
    }, opts);

    var repos = GetRegistryRepositories.Invoke(new InvokeOutputOptions { Provider = provider });

    return new Dictionary<string, object?>
    {
        ["profileId"] = profile.ProfileId,
        ["groupName"] = group.Name,
        ["tokenId"] = token.TokenId,
        ["bearerToken"] = token.BearerToken,
        ["repositoryNames"] = repos.Apply(r => r.Repositories.Select(x => x.Name).ToList()),
        ["policyRevision"] = policy.Revision,
    };
});
