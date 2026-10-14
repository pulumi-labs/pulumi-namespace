import * as pulumi from "@pulumi/pulumi";
import * as namespace from "@pulumi/namespace";

const config = new pulumi.Config();

// A versioned secret in the Namespace vault. Only `value` updates in place.
const dbPassword = new namespace.VaultObject("db-password", {
    value: config.requireSecret("dbPassword"),
    description: "Database password for the build cache",
    revealable: false,
    labels: { team: "platform" },
});

// An ephemeral container instance. Instances are destroyed at their deadline,
// so treat them as disposable compute for CI jobs, agents, and sandboxes.
const sandbox = new namespace.Instance("sandbox", {
    shape: { virtualCpu: 2, memoryMegabytes: 4096, machineArch: "amd64", os: "linux" },
    documentedPurpose: "Agent sandbox",
    containers: [{
        name: "web",
        imageRef: "nginx:1.27",
        exportPorts: [{ name: "http", containerPort: 80, proto: "HTTP" }],
    }],
});

// An IAM tenant: an isolated account with its own policies.
const tenant = new namespace.Tenant("customer-a", {
    visibleName: "Customer A",
    labels: { tier: "free" },
});

export const secretVersion = dbPassword.version;
export const instanceId = sandbox.instanceId;
export const instanceStatus = sandbox.status;
export const ingressDomain = sandbox.ingressDomain;
export const tenantId = tenant.tenantId;
