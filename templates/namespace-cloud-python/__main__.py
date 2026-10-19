import pulumi
import pulumi_namespace as namespace

config = pulumi.Config()

# A versioned secret in the Namespace vault. Only `value` updates in place.
db_password = namespace.VaultObject(
    "db-password",
    value=config.require_secret("dbPassword"),
    description="Database password for the build cache",
    revealable=False,
    labels={"team": "platform"},
)

# An ephemeral container instance. Instances are destroyed at their deadline,
# so treat them as disposable compute for CI jobs, agents, and sandboxes.
sandbox = namespace.Instance(
    "sandbox",
    shape=namespace.InstanceShapeArgs(
        virtual_cpu=2, memory_megabytes=4096, machine_arch="amd64", os="linux"
    ),
    documented_purpose="Agent sandbox",
    containers=[
        namespace.ContainerSpecArgs(
            name="web",
            image_ref="nginx:1.27",
            export_ports=[
                namespace.ContainerPortSpecArgs(name="http", container_port=80, proto="HTTP")
            ],
        )
    ],
)

# An IAM tenant: an isolated account with its own policies.
tenant = namespace.Tenant(
    "customer-a",
    visible_name="Customer A",
    labels={"tier": "free"},
)

pulumi.export("secret_version", db_password.version)
pulumi.export("instance_id", sandbox.instance_id)
pulumi.export("instance_status", sandbox.status)
pulumi.export("ingress_domain", sandbox.ingress_domain)
pulumi.export("tenant_id", tenant.tenant_id)
