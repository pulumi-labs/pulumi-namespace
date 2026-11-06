package com.pulumi.example;

import com.pulumi.Context;
import com.pulumi.Pulumi;
import com.pulumi.labs.namespace.EgressPolicy;
import com.pulumi.labs.namespace.EgressPolicyArgs;
import com.pulumi.labs.namespace.GithubProfile;
import com.pulumi.labs.namespace.GithubProfileArgs;
import com.pulumi.labs.namespace.NamespaceFunctions;
import com.pulumi.labs.namespace.RegistryRepositoryPolicy;
import com.pulumi.labs.namespace.RegistryRepositoryPolicyArgs;
import com.pulumi.labs.namespace.RevokableToken;
import com.pulumi.labs.namespace.RevokableTokenArgs;
import com.pulumi.labs.namespace.TenantGroup;
import com.pulumi.labs.namespace.TenantGroupArgs;
import com.pulumi.labs.namespace.inputs.EgressMatcherArgs;
import com.pulumi.labs.namespace.inputs.GetRegistryRepositoriesArgs;
import com.pulumi.labs.namespace.inputs.EgressRuleArgs;
import com.pulumi.labs.namespace.inputs.ProfileInstanceShapeArgs;
import com.pulumi.labs.namespace.inputs.ProfileNetworkPolicyArgs;
import com.pulumi.labs.namespace.inputs.TokenGrantArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(App::stack);
    }

    public static void stack(Context ctx) {
        // Block all egress from CI jobs except what the rules allow.
        var egress = new EgressPolicy("ci-egress", EgressPolicyArgs.builder()
            .tag("ci-locked-down")
            .description("Only package registries and GitHub")
            .mode("BLOCK")
            .rules(EgressRuleArgs.builder()
                .op("ALLOW")
                .matcher(EgressMatcherArgs.builder()
                    .matchDomains("github.com", "*.githubusercontent.com", "registry.npmjs.org")
                    .build())
                .build())
            .build());

        // A GitHub runner profile that uses the egress policy.
        var profile = new GithubProfile("ci-runner", GithubProfileArgs.builder()
            .tag("ci-medium")
            .os("ubuntu-24.04")
            .instanceShape(ProfileInstanceShapeArgs.builder().virtualCpu(4).memoryMegabytes(8192).machineArch("amd64").build())
            .networkPolicy(ProfileNetworkPolicyArgs.builder()
                .egress("EGRESS_POLICY_TAG")
                .egressPolicyTag(egress.tag())
                .build())
            .build());

        // A tenant group and a revokable token whose bearer value is a secret output.
        var group = new TenantGroup("ci-tenants", TenantGroupArgs.builder().name("ci-tenants").build());
        var token = new RevokableToken("ci-token", RevokableTokenArgs.builder()
            .name("ci-automation")
            .scope("TENANT_SCOPE")
            .grants(TokenGrantArgs.builder()
                .resourceType("instance")
                .resourceId("*")
                .actions("read")
                .build())
            .build());

        // Expire images in one repository after 14 days.
        var policy = new RegistryRepositoryPolicy("ci-images", RegistryRepositoryPolicyArgs.builder()
            .repository("ci/runner")
            .expirationSeconds(14 * 24 * 3600)
            .expirationEnforcement("EXPIRATION_ENFORCEMENT_ENFORCE")
            .build());

        var repos = NamespaceFunctions.getRegistryRepositories(
            GetRegistryRepositoriesArgs.Empty);

        ctx.export("profileId", profile.profileId());
        ctx.export("groupName", group.name());
        ctx.export("tokenId", token.tokenId());
        ctx.export("bearerToken", token.bearerToken());
        ctx.export("repositoryCount", repos.applyValue(r -> r.repositories().size()));
        ctx.export("policyRevision", policy.revision());
    }
}
