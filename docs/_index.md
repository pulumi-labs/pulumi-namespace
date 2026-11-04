---
title: Namespace Cloud Provider
meta_desc: Manage Namespace Cloud resources (ephemeral compute instances, vault secrets, IAM tenants and tokens, GitHub runner profiles, egress and registry policy) with Pulumi.
layout: package
---

An experimental Pulumi provider for [Namespace Cloud](https://namespace.so), fast ephemeral compute built for CI runners, preview environments and AI agent sandboxes. It manages instances, vault secrets, IAM tenants and tokens, GitHub runner profiles, egress policy and registry policy as infrastructure.

## Installation

The Namespace Cloud provider is available as a package in all Pulumi-supported languages:

* JavaScript/TypeScript: [`@pulumi-labs/namespace`](https://www.npmjs.com/package/@pulumi-labs/namespace)
* Python: [`pulumi-labs-namespace`](https://pypi.org/project/pulumi-labs-namespace/)
* Go: [`github.com/pulumi-labs/pulumi-namespace/sdk/go/namespace`](https://github.com/pulumi-labs/pulumi-namespace)
* .NET: [`Pulumi.Labs.Namespace`](https://www.nuget.org/packages/Pulumi.Labs.Namespace)
* Java: [`com.pulumi.labs:namespace`](https://central.sonatype.com/artifact/com.pulumi.labs/namespace)

## Example Usage

{{< chooser language "typescript,python,yaml" >}}
{{% choosable language typescript %}}
```typescript
import * as namespace from "@pulumi-labs/namespace";

const tenant = new namespace.Tenant("customer-a", {
    visibleName: "Customer A",
    labels: { tier: "free" },
});

export const tenantId = tenant.tenantId;
```
{{% /choosable %}}
{{% choosable language python %}}
```python
import pulumi
import pulumi_labs_namespace as namespace

tenant = namespace.Tenant(
    "customer-a",
    visible_name="Customer A",
    labels={"tier": "free"},
)

pulumi.export("tenantId", tenant.tenant_id)
```
{{% /choosable %}}
{{% choosable language yaml %}}
```yaml
resources:
  customer-a:
    type: namespace:index:Tenant
    properties:
      visibleName: Customer A
      labels:
        tier: free
outputs:
  tenantId: ${customer-a.tenantId}
```
{{% /choosable %}}
{{< /chooser >}}

For runnable programs in every language, see the [`examples/`](https://github.com/pulumi-labs/pulumi-namespace/tree/main/examples) directory in the provider repository.

## Configuration

The provider works with no explicit `Provider` resource. Set a Namespace bearer token as an environment variable and every resource picks it up. See Installation & Configuration for the other ways to configure the token and the region.
