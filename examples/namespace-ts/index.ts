import * as pulumi from "@pulumi/pulumi";
import * as namespace from "@pulumi/namespace";

const config = new pulumi.Config();
const nscToken = config.requireSecret("nscToken");

// Explicit provider so the token comes from stack config (a secret), not a
// hardcoded value. NSC_TOKEN in the environment works too.
const provider = new namespace.Provider("namespace", { token: nscToken, region: "us" });

// A versioned secret in the Namespace vault. Only `value` updates in place.
const dbPassword = new namespace.VaultObject("db-password", {
    value: config.requireSecret("dbPassword"),
    description: "Database password for the build cache",
    revealable: false,
    labels: { team: "platform" },
}, { provider });

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
}, { provider });

// An IAM tenant: an isolated account with its own policies.
const tenant = new namespace.Tenant("customer-a", {
    visibleName: "Customer A",
    labels: { tier: "free" },
}, { provider });

export const secretVersion = dbPassword.version;
export const instanceId = sandbox.instanceId;
export const instanceStatus = sandbox.status;
export const ingressDomain = sandbox.ingressDomain;
export const tenantId = tenant.tenantId;
