package main

import (
	"github.com/pulumi/pulumi-namespace/sdk/go/namespace"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")

		// Explicit provider so the token comes from stack config (a secret),
		// not a hardcoded value. NSC_TOKEN in the environment works too.
		provider, err := namespace.NewProvider(ctx, "namespace", &namespace.ProviderArgs{
			Token:  cfg.RequireSecret("nscToken"),
			Region: pulumi.String("us"),
		})
		if err != nil {
			return err
		}

		// A versioned secret in the Namespace vault. Only `value` updates in place.
		dbPassword, err := namespace.NewVaultObject(ctx, "db-password", &namespace.VaultObjectArgs{
			Value:       cfg.RequireSecret("dbPassword"),
			Description: pulumi.String("Database password for the build cache"),
			Revealable:  pulumi.Bool(false),
			Labels:      pulumi.StringMap{"team": pulumi.String("platform")},
		}, pulumi.Provider(provider))
		if err != nil {
			return err
		}

		// An ephemeral container instance. Instances are destroyed at their
		// deadline, so treat them as disposable compute for CI jobs, agents,
		// and sandboxes.
		sandbox, err := namespace.NewInstance(ctx, "sandbox", &namespace.InstanceArgs{
			Shape: &namespace.InstanceShapeArgs{
				VirtualCpu:      pulumi.Int(2),
				MemoryMegabytes: pulumi.Int(4096),
				MachineArch:     pulumi.String("amd64"),
				Os:              pulumi.String("linux"),
			},
			DocumentedPurpose: pulumi.String("Agent sandbox"),
			Containers: namespace.ContainerSpecArray{
				&namespace.ContainerSpecArgs{
					Name:     pulumi.String("web"),
					ImageRef: pulumi.String("nginx:1.27"),
					ExportPorts: namespace.ContainerPortSpecArray{
						&namespace.ContainerPortSpecArgs{
							Name:          pulumi.String("http"),
							ContainerPort: pulumi.Int(80),
							Proto:         pulumi.String("HTTP"),
						},
					},
				},
			},
		}, pulumi.Provider(provider))
		if err != nil {
			return err
		}

		// An IAM tenant: an isolated account with its own policies.
		tenant, err := namespace.NewTenant(ctx, "customer-a", &namespace.TenantArgs{
			VisibleName: pulumi.String("Customer A"),
			Labels:      pulumi.StringMap{"tier": pulumi.String("free")},
		}, pulumi.Provider(provider))
		if err != nil {
			return err
		}

		ctx.Export("secretVersion", dbPassword.Version)
		ctx.Export("instanceId", sandbox.InstanceId)
		ctx.Export("instanceStatus", sandbox.Status)
		ctx.Export("ingressDomain", sandbox.IngressDomain)
		ctx.Export("tenantId", tenant.TenantId)
		return nil
	})
}
