# Namespace Cloud Provider Skills

## Component Overview

| Property | Value |
|---|---|
| Type token prefix | `namespace:index:` |
| Resources | `VaultObject`, `Instance`, `Tenant` |
| Functions | `getTenants`, `getInstances` |
| Upstream API | Namespace Cloud Connect/gRPC API (no Terraform provider exists, so this is a native implementation) |
| Upstream schema | `buf.build/namespace/cloud`, consumed as `buf.build/gen/go/namespace/cloud/protocolbuffers/go` |
| pulumi-go-provider | `v1.3.1` |
| Resource count | 3 |

## Architecture

`pkg/provider/client.go` is a thin net/http client. A unary Connect call is an HTTP POST to `https://<host>/<proto package>.<Service>/<Method>` with `Content-Type: application/json` and `Authorization: Bearer <token>`. Request and response bodies are protobuf-JSON, produced with `protojson` from the generated message types, so the wire format is exactly what the schema says. There is deliberately no connect-go dependency.

Hosts:

| Host | Used for |
|---|---|
| `{region}.compute.namespaceapis.com` | Instances (region such as `us` or `eu`) |
| `iam.namespaceapis.com` | Tenants and tokens |
| `global.namespaceapis.com` | Vault, registry, egress policy, GitHub |

Connect errors arrive as JSON `{code, message}` with an HTTP status. `NotFound` is mapped to "resource is gone" so Read removes it from state and Delete is idempotent.

## Repository Layout

```
lib/providers/namespace/
├── Makefile, version.txt, CHANGELOG.md, README.md, .gitignore
├── examples/namespace-{ts,python,go,csharp,java,yaml}/
├── templates/namespace-cloud-{typescript,python,go,csharp,java,yaml}/
├── skills/SKILLS.md
└── provider/
    ├── go.mod
    ├── cmd/pulumi-resource-namespace/main.go
    └── pkg/{provider,version}/
```

## Common Mistakes

- Treating int64 fields as numbers. The protobuf-JSON mapping sends int64 as strings, which is why `version` is a string output. Use the generated types, never hand-built JSON.
- Expecting the vault value on refresh. DescribeObject only returns `value` for revealable objects. Read must keep the existing secret from state.
- Forgetting that instances are ephemeral. They are destroyed at their deadline, so a stack that outlives it will see the instance disappear on refresh. Set `deadline` deliberately for long-lived work.
- Using the wrong region. Instance calls go to `{region}.compute.namespaceapis.com`, so a resource-level `region` changes the endpoint and replaces the instance.
- Adding a new input without a `provider:"replaceOnChanges"` tag when the API has no update path. See Native Provider Identity Semantics in AGENTS.md.
- Naming a schema type the same as one of its properties. The C# SDK rejects it (this is why helper types are `ContainerSpec`, `ContainerPortSpec`, `InstanceShape`, `TenantPolicy`).

## API Quirks

- `UpdateObject` takes `ifVersionMatches`; the provider sends the version it last saw so a concurrent writer causes a clear error instead of a silent overwrite.
- `ExtendInstance` can only move a deadline later.
- `WaitInstance` is a streaming RPC. The provider polls `DescribeInstance` instead (2 second interval, 10 minute limit).
- `ListInstances` and `ListTenants` paginate with a cursor (bytes for instances, string for tenants).
- Labels are `namespace.stdlib.Label` lists on the wire and maps in the Pulumi schema. Label names are limited to 63 bytes and values to 255.

## Authentication

Provider config `token` or `NSC_TOKEN`. Accepted tokens: revokable (`nsc token create`), development (`nsc auth generate-dev-token`), or an OIDC exchange result. Never hardcode a token in a program; read it from config as a secret.

## Tracking Upstream Changes

The module version string encodes the Buf Schema Registry commit timestamp (`v1.36.12-<yyyymmddhhmmss>-<commit>.N`). To check for drift, run `GOPROXY=https://buf.build/gen/go,https://proxy.golang.org go list -m -versions buf.build/gen/go/namespace/cloud/protocolbuffers/go` and compare with `go.mod`. Bump, rebuild, and let the schema tests and compile errors point at renamed or removed fields. Because there is no Terraform provider, bridging is not an option; new RPCs must be added by hand.

## Test Coverage

- Unit tests with an `httptest` server standing in for the Connect API: URL path, auth header, JSON body shape per RPC, and Connect error mapping including NotFound.
- Mocked lifecycle tests through the pulumi-go-provider test harness for every resource (create, update, read, delete, idempotent delete).
- Schema tests asserting `replaceOnChanges` on every identity-bearing input, with a negative case for in-place inputs.
- Registry metadata test per the Provider Registry Metadata Contract.
- Live API calls are not covered: no credentials are available in CI.

## Adopt-Style and Non-Deletable Resources

- `EgressPolicy`: no delete RPC. Destroy sets mode DISABLED with no rules; the tag remains. Create adopts an existing tag by updating against its current revision.
- `BuildkiteQueue`: create = look up by name then update settings; destroy = state only.
- `RegistryDefaultPolicy` / `RegistryRepositoryPolicy`: prior values recorded at create, restored on destroy.
- `RevokableToken`: `bearerToken` is a secret and only available at creation; never log it. Destroy revokes.

## Resource Coverage

GithubProfile, EgressPolicy, BuildkiteQueue, RegistryDefaultPolicy, RegistryRepositoryPolicy, TenantGroup, RevokableToken, Blueprint, plus data sources getGithubProfiles, getTenantGroups, getBlueprints, getRegistryRepositories, getRegistryImages, getUsage, getVolumes, getGithubJobs. `getBuilds` is skipped because ListBuilds is server-streaming.

## Backlog

Streaming RPCs, action-style services (commands, logs, image sharing), and trust, Tailscale and access-control APIs once they appear in the public schema.
