---
title: Namespace Cloud Installation & Configuration
meta_desc: How to install the Pulumi Namespace Cloud provider and its language SDKs, and how to configure the token and region.
layout: package
---

## Installation

The Namespace Cloud provider is available as a package in all Pulumi-supported languages:

* JavaScript/TypeScript: [`@pulumi-labs/namespace`](https://www.npmjs.com/package/@pulumi-labs/namespace)
* Python: [`pulumi-labs-namespace`](https://pypi.org/project/pulumi-labs-namespace/)
* Go: [`github.com/pulumi-labs/pulumi-namespace/sdk/go/namespace`](https://github.com/pulumi-labs/pulumi-namespace)
* .NET: [`Pulumi.Labs.Namespace`](https://www.nuget.org/packages/Pulumi.Labs.Namespace)
* Java: [`com.pulumi.labs:namespace`](https://central.sonatype.com/artifact/com.pulumi.labs/namespace)

In normal usage, installing one of the language SDKs above is enough. The first `pulumi up` automatically downloads the matching provider binary from the URL embedded in the SDK, so no separate step is required.

### Provider Binary (optional)

If you need to install the provider binary explicitly (for example, in an air-gapped environment or to prewarm a CI cache), use the `pulumi plugin` command:

```bash
pulumi plugin install resource namespace <version> --server github://api.github.com/pulumi-labs/pulumi-namespace
```

Replace `<version>` with your desired release (see the [releases page](https://github.com/pulumi-labs/pulumi-namespace/releases)).

## Configuration

The provider needs a Namespace bearer token. Create a revokable token with `nsc token create` (recommended for CI), a short-lived development token with `nsc auth generate-dev-token`, or exchange your CI identity token for a Namespace token. The provider needs no explicit `Provider` resource and no stack config when the token is available as an environment variable:

```bash
export NSC_TOKEN=<token>
export NSC_REGION=eu   # optional, defaults to us
```

You can also set them as provider configuration:

```bash
pulumi config set --secret namespace:token <token>
pulumi config set namespace:region eu
```

Explicit Pulumi config wins over the environment variable. The following keys are available:

| Key | Env var | Default | Description |
|---|---|---|---|
| `token` (secret) | `NSC_TOKEN` | none | Bearer token. |
| `region` | `NSC_REGION` | `us` | Region for instances, used to build `{region}.compute.namespaceapis.com`. |
| `computeEndpoint` | none | derived from `region` | Full base URL of the compute API. A `{region}` placeholder is replaced with the region. For tests and private deployments. |
| `iamEndpoint` | none | `https://iam.namespaceapis.com` | Full base URL of the IAM API. |
| `globalEndpoint` | none | `https://global.namespaceapis.com` (`https://private-api.global.namespaceapis.com` for the GitHub runner services) | Full base URL of the vault and other global services. Overrides both defaults. |

An explicit `namespace` provider resource is only needed to target several accounts or regions in one program.
