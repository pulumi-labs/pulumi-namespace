package com.pulumi.example;

import com.pulumi.Pulumi;
import com.pulumi.core.Output;
import com.pulumi.namespace.Instance;
import com.pulumi.namespace.InstanceArgs;
import com.pulumi.namespace.Tenant;
import com.pulumi.namespace.TenantArgs;
import com.pulumi.namespace.VaultObject;
import com.pulumi.namespace.VaultObjectArgs;
import com.pulumi.namespace.inputs.ContainerSpecArgs;
import com.pulumi.namespace.inputs.ContainerPortSpecArgs;
import com.pulumi.namespace.inputs.InstanceShapeArgs;

import java.util.Map;

public class App {
    public static void main(String[] args) {
        Pulumi.run(ctx -> {
            var config = ctx.config();

            // A versioned secret in the Namespace vault. Only `value` updates in place.
            var dbPassword = new VaultObject("db-password", VaultObjectArgs.builder()
                .value(config.requireSecret("dbPassword"))
                .description("Database password for the build cache")
                .revealable(false)
                .labels(Map.of("team", "platform"))
                .build());

            // An ephemeral container instance. Instances are destroyed at
            // their deadline, so treat them as disposable compute for CI
            // jobs, agents, and sandboxes.
            var sandbox = new Instance("sandbox", InstanceArgs.builder()
                .shape(InstanceShapeArgs.builder()
                    .virtualCpu(2)
                    .memoryMegabytes(4096)
                    .machineArch("amd64")
                    .os("linux")
                    .build())
                .documentedPurpose("Agent sandbox")
                .containers(ContainerSpecArgs.builder()
                    .name("web")
                    .imageRef("nginx:1.27")
                    .exportPorts(ContainerPortSpecArgs.builder()
                        .name("http")
                        .containerPort(80)
                        .proto("HTTP")
                        .build())
                    .build())
                .build());

            // An IAM tenant: an isolated account with its own policies.
            var tenant = new Tenant("customer-a", TenantArgs.builder()
                .visibleName("Customer A")
                .labels(Map.of("tier", "free"))
                .build());

            ctx.export("secretVersion", dbPassword.version());
            ctx.export("instanceId", sandbox.instanceId());
            ctx.export("instanceStatus", sandbox.status());
            ctx.export("ingressDomain", sandbox.ingressDomain());
            ctx.export("tenantId", tenant.tenantId());
        });
    }
}
