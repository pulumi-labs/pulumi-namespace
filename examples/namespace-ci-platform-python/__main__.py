import pulumi
import pulumi_labs_namespace as namespace

# Block all egress from CI jobs except what the rules allow.
egress = namespace.EgressPolicy(
    "ci-egress",
    tag="ci-locked-down",
    description="Only package registries and GitHub",
    mode="BLOCK",
    rules=[namespace.EgressRuleArgs(
        op="ALLOW",
        matcher=namespace.EgressMatcherArgs(match_domains=["github.com", "*.githubusercontent.com", "registry.npmjs.org"]),
    )],
)

# A GitHub runner profile that uses the egress policy.
profile = namespace.GithubProfile(
    "ci-runner",
    tag="ci-medium",
    instance_shape=namespace.ProfileInstanceShapeArgs(virtual_cpu=4, memory_megabytes=8192),
    network_policy=namespace.ProfileNetworkPolicyArgs(egress="EGRESS_POLICY_TAG", egress_policy_tag=egress.tag),
)

# A tenant group and a revokable token whose bearer value is a secret output.
group = namespace.TenantGroup("ci-tenants", name="ci-tenants")
token = namespace.RevokableToken(
    "ci-token",
    name="ci-automation",
    scope="TENANT_SCOPE",
    grants=[namespace.TokenGrantArgs(resource_type="instance", resource_id="*", actions=["read"])],
)

# Expire images in one repository after 14 days.
policy = namespace.RegistryRepositoryPolicy(
    "ci-images",
    repository="ci/runner",
    expiration_seconds=14 * 24 * 3600,
    expiration_enforcement="EXPIRATION_ENFORCEMENT_ENFORCE",
)

repos = namespace.get_registry_repositories_output()

pulumi.export("profileId", profile.profile_id)
pulumi.export("groupName", group.name)
pulumi.export("tokenId", token.token_id)
pulumi.export("bearerToken", token.bearer_token)
pulumi.export("repositoryNames", repos.repositories.apply(lambda rs: [r.name for r in rs]))
pulumi.export("policyRevision", policy.revision)
