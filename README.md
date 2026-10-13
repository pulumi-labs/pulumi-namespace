# Namespace Cloud Pulumi Provider

Manage [Namespace Cloud](https://namespace.so) as infrastructure with Pulumi, across TypeScript, Python, Go, C#, Java, and YAML.

Namespace is fast, ephemeral compute built for CI runners, preview environments, and increasingly AI agent sandboxes: containers and VMs that start in seconds and clean themselves up at a deadline. It also ships a vault for secrets and an IAM layer of tenants. This provider lets you declare those pieces in code instead of clicking through the console or scripting `nsc`.

There is no Terraform provider for Namespace, so this is a native provider (pulumi-go-provider). It talks to the public Connect/gRPC API over HTTPS using the schema-generated Go types published at `buf.build/namespace/cloud`, so request and response shapes follow the upstream protobuf definitions exactly.

## Installation

```bash
# TypeScript / JavaScript
npm install @pulumi/namespace

# Python
pip install pulumi-namespace

# Go
go get github.com/pulumi/pulumi-namespace/sdk/go/namespace

# .NET
dotnet add package Pulumi.Namespace

# Java (Maven)
# Add com.pulumi:namespace to your pom.xml

# YAML: no package install required
```

## Authentication

The provider needs a Namespace bearer token. Any of these work:

| Token source | How to get it |
|---|---|
| Revokable token (recommended for CI) | `nsc token create` |
| Development token (short lived, local use) | `nsc auth generate-dev-token` |
| OIDC token exchange (keyless CI) | Exchange your CI identity token for a Namespace token |

```bash
# Environment variable
export NSC_TOKEN=...

# Or Pulumi config, stored as a secret
pulumi config set --secret namespace:token ...
```

## Configuration

| Key | Env var | Default | Description |
|---|---|---|---|
| `token` (secret) | `NSC_TOKEN` | none | Bearer token. |
| `region` | `NSC_REGION` | `us` | Region for instances, used to build `{region}.compute.namespaceapis.com`. |
| `computeEndpoint` | none | derived from `region` | Full base URL of the compute API. For tests and private deployments. |
| `iamEndpoint` | none | `https://iam.namespaceapis.com` | Full base URL of the IAM API. |
| `globalEndpoint` | none | `https://global.namespaceapis.com` | Full base URL of the vault and other global services. |

## Resources

### `namespace:index:VaultObject`

A versioned secret in the Namespace vault.

| Input | Type | Notes |
|---|---|---|
| `value` | string (secret) | Required. The only input that updates in place: each change writes a new version, guarded by the current version for optimistic concurrency. |
| `description` | string | Replaces on change. |
| `revealable` | bool | Whether the value can be read back after creation. Cannot change after creation, so it replaces. |
| `labels` | map of string | Replaces on change. |

Outputs: `objectId` (the resource ID), `version`, `createdAt`.

The vault only returns the value for revealable objects. For a non-revealable object, refresh keeps the value from your program and never overwrites it with an empty string.

### `namespace:index:Instance`

An ephemeral container instance on Namespace compute. Instances are destroyed by the platform at their deadline.

| Input | Type | Notes |
|---|---|---|
| `shape` | object | Required. `virtualCpu`, `memoryMegabytes`, optional `machineArch` (`amd64` or `arm64`) and `os`. Replaces on change. |
| `containers` | list | Each has `name`, `imageRef`, optional `args`, `entrypoint`, `env`, and `exportPorts` (`name`, `containerPort`, `proto` of `HTTP`, `TCP`, `H2C`, or `TLS_PASSTHROUGH`). Replaces on change. |
| `documentedPurpose` | string | Free text shown in the console. Replaces on change. |
| `labels` | map of string | Replaces on change. |
| `interactive` | bool | Replaces on change. |
| `region` | string | Overrides the provider region. Replaces on change. |
| `deadline` | RFC 3339 string | The only in-place input. Moving it later calls ExtendInstance. Moving it earlier is rejected with an error, because the API can only extend. |

Outputs: `instanceId`, `createdAt`, `readyAt`, `expiresAt`, `status`, `instanceUrl`, `ingressDomain`, `services` (name, status, endpointKind, endpoint).

Create polls DescribeInstance every 2 seconds for up to 10 minutes until the instance is running. If the instance enters an error state the resource is still recorded so Pulumi can clean it up.

### `namespace:index:Tenant`

An IAM tenant: an isolated account with its own policies.

| Input | Type | Notes |
|---|---|---|
| `visibleName` | string | Updates in place. |
| `labels` | map of string | Updates in place. |
| `policies` | list | Each has `id`, `policy`, `value`, and optional `expiresAt`. Updates in place. |
| `creatorId` | string | Replaces on change. |
| `externalAccountId` | string | Replaces on change. |

Outputs: `tenantId` (the resource ID), `createdAt`.

### `namespace:index:GithubProfile`

A GitHub Actions runner profile (shape, cache volumes, builder mode, custom base image, network policy, access level). Inputs: `tag` (replaces), `description`, `os`, `instanceShape`, `cacheVolumeSettings`, `builderMode`, `experimentalFeatures`, `emoji`, `customRunnerImageSpec`, `networkPolicy`, `accessLevel`, `swapSizeMb`, `rebuildTrigger`. Everything except `tag` updates in place with optimistic concurrency on the profile version. Changing `rebuildTrigger` (requires `customRunnerImageSpec`) calls `RebuildProfileImage`. Outputs: `profileId` (the resource ID, also the import ID), `version`, `createdAt`, `updatedAt`, `imageStatus`, `imageRef`, `lastBuildError`.

### `namespace:index:EgressPolicy`

A named outbound network policy (`mode` DISABLED, ALLOW_ALL, ADVISORY or BLOCK, deep packet inspection, ALLOW/PROXY/INJECT rules, additional rulesets; the deprecated `allowedEgressDomains` is not exposed, use `ALLOW` rules). Keyed by `tag` (replaces). Updates use `matchRevision` for optimistic concurrency. If the tag already exists at create time the policy is adopted and its spec is overwritten (a warning is logged and `adopted` is set). **The API has no delete RPC**, so destroy sets the policy to mode `DISABLED` with no rules and removes it from state; the tag itself remains on Namespace. Inject `value` and endpoint header values are secrets. Import with the tag.

### `namespace:index:BuildkiteQueue`

Namespace settings (`egressPolicyTag`, `permissions`, `openTelemetrySettings`) of an **existing** Buildkite queue. Adopt-style: the API has no create or delete, so create looks the queue up by `queueName` (replaces) and applies the declared settings, failing if the queue is missing or ambiguous. Create records the queue's existing settings (`priorSettings`, `priorRecorded`) and changes only the declared fields; undeclared settings stay as the server has them. Destroy restores the recorded prior settings (an imported queue has no prior, so destroy leaves it as it is). Import with the queue ID.

### `namespace:index:RegistryDefaultPolicy` and `namespace:index:RegistryRepositoryPolicy`

Image expiration policy of the container registry: account-wide default, or per `repository` (replaces). Inputs are the expiration in seconds and the enforcement mode (`EXPIRATION_ENFORCEMENT_ENFORCE` or `EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE`). Adopt-style singletons: the values observed before create are recorded in state (`priorExpirationSeconds`, `priorExpirationEnforcement`) and restored on destroy exactly, including clearing a field that was unset before (`priorRecorded`). Imported policies have no prior and are left as they are on destroy. `UpdateImageLifetime` acts on individual images and is not modeled.

### `namespace:index:TenantGroup`

An IAM tenant group with policies. `name` replaces; `policies` update in place through the idempotent `EnsureTenantGroup`. Output `enrolledTenantIds`. Import with the name.

### `namespace:index:RevokableToken`

A revokable bearer token. The `bearerToken` output is a Pulumi secret and is only returned by the API at creation, so it is preserved in state and never logged. Identity inputs (`name`, `description`, `expiresAt`, `scope`, `policies`, `grants`) replace. Changing `refreshTrigger` calls `RefreshRevokableToken` (optionally with `refreshMinimumSeconds`) to extend the expiry in place. Destroy revokes the token. A token that is revoked or expired is treated as gone on refresh. A token cannot be imported because its bearer value cannot be retrieved.

### `namespace:index:Blueprint`

A custom base image blueprint built from either `dockerfileContent` or an apt-based image (`aptBaseImage` or `aptBaseBlueprint`, plus `aptPackages`). Blueprints are immutable: all inputs replace. Outputs `blueprintId`, `createdAt`, `imageRef`, `buildError`. Import with the name.

## Functions

- `namespace:index:getTenants` lists tenants, optionally filtered by `creatorId` or `externalAccountId`. Pagination is handled for you.
- `namespace:index:getInstances` lists instances in a region, optionally filtered by `labels` and `includeDestroyed`. Pagination is handled for you.
- `getGithubProfiles`, `getTenantGroups`, `getBlueprints`: list the corresponding objects.
- `getRegistryRepositories`, `getRegistryImages` (optional `repository` filter): list registry contents.
- `getUsage`: compute usage between `periodStart` and `periodEnd` (YYYY-MM-DD) with instance and build minutes totals and a per-day breakdown.
- `getVolumes`: cache volumes and persistent volumes in a region.
- `getGithubJobs`: GitHub jobs with optional repository, conclusion and time filters, capped by `maxJobs`.

## Example

```yaml
resources:
  namespace:
    type: pulumi:providers:namespace
    properties:
      token: ${nscToken}
  sandbox:
    type: namespace:index:Instance
    properties:
      shape: { virtualCpu: 2, memoryMegabytes: 4096 }
      containers:
        - name: web
          imageRef: nginx:1.27
          exportPorts:
            - { name: http, containerPort: 80, proto: HTTP }
    options:
      provider: ${namespace}
```

Complete programs in all six languages are in [`examples/`](./examples) (`namespace-*` for the basics, `namespace-ci-platform-*` for a runner profile, egress policy, tenant group, secret-output token, registry policy and a data source), and `pulumi new namespace-cloud-<language>` scaffolds one.

## Semantics worth knowing

- Adopt-style resources (`BuildkiteQueue`, `RegistryDefaultPolicy`, `RegistryRepositoryPolicy`, and `EgressPolicy` when its tag exists) manage settings of something that cannot be created or deleted through the API.
- `EgressPolicy` destroy disables the policy instead of deleting it, because no delete RPC exists.
- Regions: compute APIs use `{region}.compute.namespaceapis.com` (provider `region`, default `us`); IAM, registry, GitHub, egress, image and Buildkite APIs are global.

## Not covered

- `getBuilds`: `ListBuilds` is a server-streaming RPC and does not fit the unary Connect JSON transport used here.
- Trust relationships, Tailscale integration and workspace access controls are documented in nsc docs but are absent from the public schema in the pinned module version.
- Operational RPCs (commands, log streaming, `Build`, image sharing, volume destruction) are actions rather than declarative state.

## Development

```bash
make tidy
make build
make test
make fmt
```

See [`skills/SKILLS.md`](./skills/SKILLS.md) for architecture notes, API quirks, and how to track upstream changes.
