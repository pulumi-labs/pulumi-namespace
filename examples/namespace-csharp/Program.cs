using Pulumi;
using Pulumi.Labs.Namespace;
using Pulumi.Labs.Namespace.Inputs;
using System.Collections.Generic;

return await Deployment.RunAsync(() =>
{
    var config = new Pulumi.Config();

    // A versioned secret in the Namespace vault. Only `value` updates in place.
    var dbPassword = new VaultObject("db-password", new VaultObjectArgs
    {
        Value = config.RequireSecret("dbPassword"),
        Description = "Database password for the build cache",
        Revealable = false,
        Labels = { { "team", "platform" } },
    });

    // An ephemeral container instance. Instances are destroyed at their
    // deadline, so treat them as disposable compute for CI jobs, agents, and
    // sandboxes.
    var sandbox = new Instance("sandbox", new InstanceArgs
    {
        Shape = new InstanceShapeArgs
        {
            VirtualCpu = 2,
            MemoryMegabytes = 4096,
            MachineArch = "amd64",
            Os = "linux",
        },
        DocumentedPurpose = "Agent sandbox",
        Containers =
        {
            new ContainerSpecArgs
            {
                Name = "web",
                ImageRef = "nginx:1.27",
                ExportPorts =
                {
                    new ContainerPortSpecArgs { Name = "http", ContainerPort = 80, Proto = "HTTP" },
                },
            },
        },
    });

    // An IAM tenant: an isolated account with its own policies.
    var tenant = new Tenant("customer-a", new TenantArgs
    {
        VisibleName = "Customer A",
        Labels = { { "tier", "free" } },
    });

    return new Dictionary<string, object?>
    {
        ["secretVersion"] = dbPassword.Version,
        ["instanceId"] = sandbox.InstanceId,
        ["instanceStatus"] = sandbox.Status,
        ["ingressDomain"] = sandbox.IngressDomain,
        ["tenantId"] = tenant.TenantId,
    };
});
