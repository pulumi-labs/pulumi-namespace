using Pulumi;
using Pulumi.Namespace;
using Pulumi.Namespace.Inputs;
using System.Collections.Generic;

return await Deployment.RunAsync(() =>
{
    var config = new Pulumi.Config();
    var nscToken = config.RequireSecret("nscToken");

    // Explicit provider so the token comes from stack config (a secret), not a
    // hardcoded value. NSC_TOKEN in the environment works too.
    var provider = new Provider("namespace", new ProviderArgs
    {
        Token = nscToken,
        Region = config.Get("region") ?? "us",
    });
    var opts = new CustomResourceOptions { Provider = provider };

    // A versioned secret in the Namespace vault. Only `value` updates in place.
    var dbPassword = new VaultObject("db-password", new VaultObjectArgs
    {
        Value = config.RequireSecret("dbPassword"),
        Description = "Database password for the build cache",
        Revealable = false,
        Labels = { { "team", "platform" } },
    }, opts);

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
    }, opts);

    // An IAM tenant: an isolated account with its own policies.
    var tenant = new Tenant("customer-a", new TenantArgs
    {
        VisibleName = "Customer A",
        Labels = { { "tier", "free" } },
    }, opts);

    return new Dictionary<string, object?>
    {
        ["secretVersion"] = dbPassword.Version,
        ["instanceId"] = sandbox.InstanceId,
        ["instanceStatus"] = sandbox.Status,
        ["ingressDomain"] = sandbox.IngressDomain,
        ["tenantId"] = tenant.TenantId,
    };
});
