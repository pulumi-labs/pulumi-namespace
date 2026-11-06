import * as namespace from "@pulumi-labs/namespace";

// Block all egress from CI jobs except what the rules allow.
const egress = new namespace.EgressPolicy("ci-egress", {
    tag: "ci-locked-down",
    description: "Only package registries and GitHub",
    mode: "BLOCK",
    rules: [{ op: "ALLOW", matcher: { matchDomains: ["github.com", "*.githubusercontent.com", "registry.npmjs.org"] } }],
});

// A GitHub runner profile that uses the egress policy.
const profile = new namespace.GithubProfile("ci-runner", {
    tag: "ci-medium",
    os: "ubuntu-24.04",
    instanceShape: { virtualCpu: 4, memoryMegabytes: 8192, machineArch: "amd64" },
    networkPolicy: { egress: "EGRESS_POLICY_TAG", egressPolicyTag: egress.tag },
});

// A tenant group and a revokable token whose bearer value is a secret output.
const group = new namespace.TenantGroup("ci-tenants", { name: "ci-tenants" });
const token = new namespace.RevokableToken("ci-token", {
    name: "ci-automation",
    scope: "TENANT_SCOPE",
    grants: [{ resourceType: "instance", resourceId: "*", actions: ["read"] }],
});

// Expire images in one repository after 14 days.
const policy = new namespace.RegistryRepositoryPolicy("ci-images", {
    repository: "ci/runner",
    expirationSeconds: 14 * 24 * 3600,
    expirationEnforcement: "EXPIRATION_ENFORCEMENT_ENFORCE",
});

const repos = namespace.getRegistryRepositoriesOutput();

export const profileId = profile.profileId;
export const groupName = group.name;
export const tokenId = token.tokenId;
export const bearerToken = token.bearerToken;
export const repositoryNames = repos.repositories.apply(rs => rs.map(r => r.name));
export const policyRevision = policy.revision;
