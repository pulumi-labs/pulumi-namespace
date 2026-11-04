package main

import (
	"github.com/pulumi-labs/pulumi-namespace/sdk/go/namespace"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Block all egress from CI jobs except what the rules allow.
		egress, err := namespace.NewEgressPolicy(ctx, "ci-egress", &namespace.EgressPolicyArgs{
			Tag:         pulumi.String("ci-locked-down"),
			Description: pulumi.String("Only package registries and GitHub"),
			Mode:        pulumi.String("BLOCK"),
			Rules: namespace.EgressRuleArray{&namespace.EgressRuleArgs{
				Op: pulumi.String("ALLOW"),
				Matcher: &namespace.EgressMatcherArgs{
					MatchDomains: pulumi.StringArray{
						pulumi.String("github.com"),
						pulumi.String("*.githubusercontent.com"),
						pulumi.String("registry.npmjs.org"),
					},
				},
			}},
		})
		if err != nil {
			return err
		}

		// A GitHub runner profile that uses the egress policy.
		profile, err := namespace.NewGithubProfile(ctx, "ci-runner", &namespace.GithubProfileArgs{
			Tag: pulumi.String("ci-medium"),
			InstanceShape: &namespace.ProfileInstanceShapeArgs{
				VirtualCpu:      pulumi.Int(4),
				MemoryMegabytes: pulumi.Int(8192),
			},
			NetworkPolicy: &namespace.ProfileNetworkPolicyArgs{
				Egress:          pulumi.String("EGRESS_POLICY_TAG"),
				EgressPolicyTag: egress.Tag,
			},
		})
		if err != nil {
			return err
		}

		// A tenant group and a revokable token whose bearer value is a secret output.
		group, err := namespace.NewTenantGroup(ctx, "ci-tenants", &namespace.TenantGroupArgs{
			Name: pulumi.String("ci-tenants"),
		})
		if err != nil {
			return err
		}
		token, err := namespace.NewRevokableToken(ctx, "ci-token", &namespace.RevokableTokenArgs{
			Name:  pulumi.String("ci-automation"),
			Scope: pulumi.String("TENANT_SCOPE"),
			Grants: namespace.TokenGrantArray{&namespace.TokenGrantArgs{
				ResourceType: pulumi.String("instance"),
				ResourceId:   pulumi.String("*"),
				Actions:      pulumi.StringArray{pulumi.String("read")},
			}},
		})
		if err != nil {
			return err
		}

		// Expire images in one repository after 14 days.
		policy, err := namespace.NewRegistryRepositoryPolicy(ctx, "ci-images", &namespace.RegistryRepositoryPolicyArgs{
			Repository:            pulumi.String("ci/runner"),
			ExpirationSeconds:     pulumi.Int(14 * 24 * 3600),
			ExpirationEnforcement: pulumi.String("EXPIRATION_ENFORCEMENT_ENFORCE"),
		})
		if err != nil {
			return err
		}

		repos := namespace.GetRegistryRepositoriesOutput(ctx, namespace.GetRegistryRepositoriesOutputArgs{})

		ctx.Export("profileId", profile.ProfileId)
		ctx.Export("groupName", group.Name)
		ctx.Export("tokenId", token.TokenId)
		ctx.Export("bearerToken", token.BearerToken)
		ctx.Export("repositories", repos.Repositories())
		ctx.Export("policyRevision", policy.Revision)
		return nil
	})
}
