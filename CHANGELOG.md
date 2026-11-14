# Changelog

## [Unreleased]

## [0.3.1] - 2026-11-14

### Security

- Security: the Go SDK `go.mod` in the mirror now requires OpenTelemetry `sdk/log` v1.47.0 instead of v0.21.0, fixing GHSA-q7gw-24fr-5p69 (vulnerable >= 0.9.0, < 1.47.0) and clearing the pulumi-labs/pulumi-namespace Dependabot alert once released (fixes #1972). SDK generation change only. Not breaking.

- Security: updated OpenTelemetry (`go.opentelemetry.io/otel`, `sdk`, `trace`, `metric`, `log` and the OTLP trace exporters) from 1.45.0 to 1.47.0, which fixes GHSA-q7gw-24fr-5p69 (CVE-2026-128085, `AttributeValueLengthLimit` bypass in opentelemetry-go, vulnerable 1.33.0 to 1.46.x). Clears the pulumi-labs/pulumi-namespace Dependabot alerts for it once released (part of #1966). Not breaking.

## [0.3.0] - 2026-11-13

### Added

- Lookups `getRegistryImage` (one registry image by repository and tag or digest, from `ContainerRegistryService.GetImage`) and `getGithubJob` (one GitHub Actions job by job ID, from `JobsService.DescribeJob`), the single-object counterparts of `getRegistryImages` and `getGithubJobs`. Both fail with `was not found` for a missing object. Additive, not breaking. `getGithubJob` omits the runner and compute instance details the API returns.

### Fixed

- The tenant-API error hint now covers every tenant RPC the provider sends (pulumi-labs/pulumi-namespace#17). The hint came from a hand-kept list of method names that held RPCs the provider never calls and missed `EnsureTenantGroup`, `DescribeTenantGroup`, `RemoveTenant` and `DescribePolicies`, so a `TenantGroup` create on an account not enabled for tenant management failed with a bare `unauthenticated`. The hint is now decided by the service (`TenantService` and `TenantMembershipService`), so any tenant RPC gets it. Error text only, nothing else changes. Not breaking.

## [0.2.3] - 2026-11-12

### Fixed

- A tenant API call that Namespace answers with HTTP 401 `unauthenticated` now says what is likely wrong instead of only "unauthenticated": the token works for other Namespace APIs but the tenant APIs rejected it, so the account is probably not enabled for tenant management or the token lacks tenant scope (not confirmed, the API does not say). The `rid` is kept; other methods get no hint. Mirror issue https://github.com/pulumi-labs/pulumi-namespace/issues/17 (#1877). Not breaking.
- A resource input wired from another resource's output is no longer rejected at preview (#1832). `InstanceIngress` with `instanceId: ${instance.instanceId}` failed with `must not be empty`, because Check validated the placeholder of a not yet known value. Every resource's Check now skips the validation of values that are unknown at preview (also nested ones) and still validates the known ones, so the ingress, blueprint, queue, token, tenant group and policy resources accept computed inputs. The final Check at apply time is unchanged.

## [0.2.2] - 2026-11-11

### Changed

- API errors that give no reason are now actionable. The tenant APIs (`CreateTenant`, `ListTenants`, `ListTenantGroups` and the rest of `TenantService`) answer a token that is not enabled for tenant management with HTTP 400 `invalid_argument: unspecified message (rid=...)`; the provider now names the likely cause (the token's account may not be enabled for tenant management; the API does not confirm it) rather than leaving only the placeholder, keeps the `rid`, and prints any Connect error `details` the API attaches. `ListTenantGroupsRequest` has no fields at all, so the failure of `getTenantGroups` cannot be a request-shape problem. The request bodies are unchanged. Mirror issues https://github.com/pulumi-labs/pulumi-namespace/issues/12 and https://github.com/pulumi-labs/pulumi-namespace/issues/13 (#1805, #1806).

## [0.2.1] - 2026-11-10

### Changed

- Updated the generated Namespace Cloud API client (`buf.build/gen/go/namespace/cloud/protocolbuffers/go`) from the 2026-11-03 build to the 2026-11-06 build (v1.36.12-20261106090801-6db93b78d1d9.2). No resource, function or schema change.

### Fixed

- `pulumi import` of an object that does not exist (a Blueprint, BuildkiteQueue, EgressPolicy, GithubProfile, Instance, InstanceIngress, RegistryRepositoryPolicy, RevokableToken, SharedImage, Tenant, TenantGroup or VaultObject; getters keep their "was not found" error) now fails with an error instead of exiting 0 with an empty resource in state. Read returned the empty response that only a refresh treats as deleted. A refresh of a deleted object still drops it from state (#1715, same defect as #1712).
- `GithubProfile`: declaring the deprecated `cacheVolumeSettings[].volumeTag` no longer plans an update on the first preview after `pulumi import`. The Namespace API removed the field, it is never sent and an import cannot read it back, so a program that still declares it differed from the imported state. A new diff ignores `volumeTag`; every other input compares as before and nothing sent to the API changes. Mirror issue https://github.com/pulumi-labs/pulumi-namespace/issues/8 (#1691).
- `Instance`: after `pulumi import`, a program that declares `shape.machineArch` and `shape.os` no longer plans a replacement of the running instance. Import still leaves them unset (#1571), so a program that omits them stays clean, but the instance now records what the platform reports in the new outputs `effectiveMachineArch` and `effectiveOs`, and a new diff treats an unset recorded value as equal to the same value declared later. A different declared value still replaces. Mirror issue https://github.com/pulumi-labs/pulumi-namespace/issues/9 (#1677). Additive output properties, not breaking.

## [0.2.0] - 2026-11-04

### Added

- Add `qa.yaml`, the runtime test manifest QA reads: credentials, every resource and function with its test plan and cost, and the regressions to re-check. It is copied to the mirror root and changes no provider behavior.
- Lookup functions so every resource type can be read without managing it: `getVaultObject`, `getTenant` (by tenant ID or external account ID), `getInstance`, `getGithubProfile` (by profile ID or tag), `getTenantGroup`, `getBlueprint`, `getEgressPolicy`, `getBuildkiteQueue` (by queue ID or name), `getRegistryDefaultPolicy`, `getRegistryRepositoryPolicy`, `getRevokableToken` (metadata only, never the bearer value), plus the list functions `getVaultObjects`, `getEgressPolicies`, `getBuildkiteQueues`, `getRevokableTokens`, `getSharedImages` and the read-only lookups `getTenantMembers` and `getTenantPolicies`. A lookup of an object that does not exist fails with `... was not found`.
- New resource `SharedImage` (`ShareImage` / `UnshareImage`) with `getSharedImage` and `getSharedImages`: shares a registry image under a public or partner reference. Import with the shared image ID.
- New resource `InstanceIngress` (`CreateIngress` / `DeleteIngress`) with `getInstanceIngress`: exposes an instance port on an Internet-facing name. Import with `instanceId/ingressId` (or `region/instanceId/ingressId`). Create sends one request and uses the single ingress the API returns even if the server normalised or suffixed the requested name; any other response is resolved by listing the instance's ingresses by name, and fails naming the instance when the ingress cannot be identified. The exact shape of the `CreateIngress` response is unverified against the live API.
- `RevokableToken` can now be imported by token ID. The bearer value cannot be recovered, so an imported token has an empty `bearerToken` (a secret output read by a consumer is an empty string), and destroying an imported token revokes a token other systems may still be using. Previously import was refused.
- `research/coverage/namespace.md`, the coverage inventory of every service of the pinned Namespace API module with a class and reason for each, and the ranked list of what is deferred.

### Changed

- Updated the generated Namespace Cloud protobuf bindings (`buf.build/gen/go/namespace/cloud/protocolbuffers/go`) to the 2026-11-03 build (`v1.36.12-20261103090127-5e7839020ace.2`). Upstream removed the unused `SuspendInstanceRequest` and `WakeInstanceRequest` messages and added `EnsureBlueprintRequest` and `EnsureBlueprintResponse` (compute and private devbox APIs); the provider does not call any of them, so there are no resource or schema changes.
- `go.mod`/`go.sum` moved from `provider/` to the provider's top level (the monorepo module path now ends in `lib/providers/namespace`); the Makefile builds from there. No change to the provider binary, schema or SDKs.

### Fixed

- `VaultObject`, `Tenant` and `Instance`: refreshing a resource declared without `labels` no longer copies labels the platform attaches itself into the inputs. Because `labels` replaces on `VaultObject` and `Instance`, the next `pulumi up` would have replaced them. Only import reports the server's full label set.
- `Instance` records the region it was created in as the new `effectiveRegion` output and uses it for refresh, extend and destroy. Previously a changed provider region or `NSC_REGION` made refresh drop the instance from state and destroy skip the real, still-billed instance. Import no longer sets the `region` input (it used to, which made a program relying on the provider region plan a replacement).
- `Instance` refresh now reports drift in `shape`, `documentedPurpose` and `deadline` instead of echoing state; server-filled defaults of unset inputs (machine arch, OS) never overwrite your input. An instance with no services no longer fails to decode on the next refresh or update.
- `RevokableToken`: an `EXPIRED` token is now treated as gone on refresh (the README always said so) and is recreated; refresh reports drift in `name`, `description`, `scope` (when declared) and `grants`.
- `Blueprint`: refresh reports drift in the whole spec, and a spec change now updates in place by re-calling `CreateBlueprint` (an upsert by name) instead of deleting the blueprint first and creating it again. **BREAKING:** `dockerfileContent`, `aptBaseImage`, `aptBaseBlueprint` and `aptPackages` no longer carry `replaceOnChanges`; changing them is an update, only `name` replaces.
- `TenantGroup`, `GithubProfile`, `RegistryDefaultPolicy`, `RegistryRepositoryPolicy` and `BuildkiteQueue` no longer orphan or lose track of a changed object when a step after the change fails: a failing read-back after `EnsureTenantGroup`, a failing `RebuildProfileImage` after `UpdateProfile` (the old `rebuildTrigger` stays in state so the next up retries), a failing read-back after the registry policy or queue settings were overwritten (the prior values stay recorded so destroy can restore them) now report an initialization failure with the ID and partial state.
- `RegistryDefaultPolicy` refresh no longer copies a server value into an input the program does not declare.
- The RPC name guard referenced in the code comments now exists (`TestRPCNamesExistInSchema`).
- The `computeEndpoint` provider setting accepts a `{region}` placeholder (replaced with the region); without one it behaves as before.
- Provider config environment variable fallbacks now work: `namespace:token` and `namespace:region` are filled from `NSC_TOKEN` and `NSC_REGION` when unset. The fallbacks were previously declared as struct tags, which the provider framework ignores.
- Security: `Read` also marks a secret nested inside an object, an array of objects or a map of objects (`EgressHeader.value`, `EgressInject.value`, `QueueHeader.value` and `QueueValueFrom.static`) as a secret, not only top-level properties (#1599). If the provider schema cannot be loaded during a `Read`, the failure is no longer cached for the life of the process: the next `Read` retries (#1599).
- Security: `pulumi import` and `pulumi refresh` now store the properties this provider marks secret (`VaultObject.value` and `RevokableToken.bearerToken`) as secrets. A `Read` response only kept the secretness of the state it was given, and an import has no state, so a value the provider recovered from the API was written to the state as plaintext. The secret properties are read from the provider's own schema, so every resource is covered. State imported before this fix stays plaintext until its next `pulumi refresh`, which re-encrypts it (#1577).
- A preview no longer invents resource IDs. `Create` during `pulumi preview` returned the resource's logical name (or the placeholder `preview`) as its ID, so `id` was a known, wrong value and a program passing it to a lookup or to another resource ran against a name that does not exist and failed the preview (for example `getWebhook(webhook.id)`). A preview has no real ID now, so `id` stays unknown until the apply. Mirror issue https://github.com/pulumi-labs/pulumi-huggingface/issues/7 (#1590).
- Changing the provider configuration (a rotated token, API key or secret, a new endpoint or region) is an in-place update of the default provider. By default pulumi-go-provider replaced the provider on any config change, and replacing a provider replaces every resource that uses it, so rotating a credential would have deleted and recreated every resource. Each resource's own diff still decides whether it changes. Mirror issue https://github.com/pulumi-labs/pulumi-huggingface/issues/8 (#1591).
- `Instance`, `VaultObject` and `Tenant` import no longer copy the platform's own `system/` labels into `labels`. On `Instance` and `VaultObject` `labels` replaces, so a program that declares no labels planned a replacement of the running instance or the stored secret on the first preview after `pulumi import`; on `Tenant` it planned a spurious update. Your own labels are still imported. This completes the import audit of #1571: every other resource with an import branch (`EgressPolicy`, `Blueprint`, `RegistryDefaultPolicy`, `BuildkiteQueue`, `RevokableToken`, `TenantGroup`, `GithubProfile`) is covered by a test that imports by ID and diffs the program that created it, and none needed a change. A secret egress `inject` value is redacted by the API, so an imported `EgressPolicy` whose program declares one plans a single in-place update, never a replacement. Mirror reports https://github.com/pulumi-labs/pulumi-namespace/issues/4 and https://github.com/pulumi-labs/pulumi-namespace/issues/5.
- `Instance` import no longer copies `shape.machineArch` and `shape.os` into inputs. Because `shape` replaces the instance, a routine `pulumi import` followed by `pulumi up` against a program that did not declare them planned a replacement of the running instance (pulumi-labs/pulumi-namespace#4). `GithubProfile` import no longer copies the platform defaults `builderMode` (`NO_CACHING`) and `instanceShape.os` (`linux`) either, which planned a permanent update (pulumi-labs/pulumi-namespace#5). Values that are not the default are kept. Other resources are not audited yet. Declaring `shape.machineArch` or `shape.os` after an import replaces the instance, as for any instance created without them.
- `GithubProfile`: `os` (a runner image: `ubuntu-20.04`, `ubuntu-22.04`, `ubuntu-24.04`, `ubuntu-24.04-systemd` or `ubuntu-26.04`) and `instanceShape.machineArch` (`amd64` or `arm64`) are required by the API. A profile without them, or with an unsupported value such as `os: linux`, used to preview cleanly and fail with HTTP 400 on create; it now fails at preview with the supported values. The schema is unchanged, the property descriptions are corrected, and the examples declare both (pulumi-labs/pulumi-namespace#3).
- `GithubProfile`, `getGithubProfile`, `getGithubProfiles` and `getGithubJobs` failed with HTTP 404 under the default configuration: the GitHub runner services are not served by `https://global.namespaceapis.com`. They now default to `https://private-api.global.namespaceapis.com`; the other global services (vault, registry, egress policy, blueprints, queues) keep the public host. `globalEndpoint` still overrides both, so the workaround of setting it keeps working (pulumi-labs/pulumi-namespace#2).

### Changed

- All 6 `namespace-cloud-<lang>` templates now use the default provider (no explicit `Provider` resource, no `nscToken` or `region` stack config), so credentials and region come from `pulumi config set namespace:token --secret`, `namespace:region`, `NSC_TOKEN` or `NSC_REGION`.
- Updated the generated Namespace Cloud protobuf bindings (`buf.build/gen/go/namespace/cloud/protocolbuffers/go`) to the 2026-10-30 build (`v1.36.12-20261030150547-dbdea2e9de60.2`). Upstream removed `CacheVolumeSettings.volume_tag` from the GitHub runner profile API; see Deprecated below. Other upstream additions (ingress deletion, devbox usage limits) are not exposed by this provider yet.

### Deprecated

- `GithubProfile` `cacheVolumeSettings[].volumeTag` is now optional, deprecated and ignored: the Namespace API no longer accepts a cache volume tag, so the provider no longer sends it and no longer requires it. Existing programs that set it keep working; remove it at your convenience.

## v0.2.0

### Added

- `namespace-ci-platform-*` examples in TypeScript, Python, Go, C#, Java, and YAML.
- v0.2.0 surface: `GithubProfile`, `EgressPolicy`, `BuildkiteQueue`, `RegistryDefaultPolicy`, `RegistryRepositoryPolicy`, `TenantGroup`, `RevokableToken` (secret bearer output), and `Blueprint` resources; `getGithubProfiles`, `getTenantGroups`, `getBlueprints`, `getRegistryRepositories`, `getRegistryImages`, `getUsage`, `getVolumes`, and `getGithubJobs` functions. `EgressPolicy` destroy disables the policy because the API has no delete.

### Changed

- `EgressPolicy`: removed the `allowedEgressDomains` input (deprecated upstream, only honoured for inline policies at instance creation); use `ALLOW` rules or `additionalRulesets`. v0.2.0 was never published, so this is not a breaking change for released users.
- `RevokableToken`: import is not supported (the bearer secret cannot be recovered); importing fails with a clear error.
- `BuildkiteQueue`: adopting a queue now merges only declared settings, records the prior settings and restores them on destroy.
- `Blueprint`: replacements are delete-before-create because the name is the identity (create-before-delete deleted the new blueprint). `EgressPolicy`, `TenantGroup`, `RegistryDefaultPolicy`, `RegistryRepositoryPolicy` and `BuildkiteQueue` adopt existing server-side state, so they are also delete-before-replace: a forced replace (`--replace`, `--target-replace`, taint) keeps the same key, and the old resource's delete would otherwise run after the new create and undo what it adopted. The delete-first order is requested by the provider; it relies on the Pulumi engine honouring it for forced replaces and is covered by diff tests, not an end-to-end engine run.
- Pagination is shared, rejects repeated cursors, and filtered tenant lists send only the cursor on later pages; oversized responses now fail instead of truncating; endpoint overrides must be https (loopback http allowed).

## v0.1.0

### Added

- Initial `namespace` native provider (v0.1.0) for Namespace Cloud.
- `VaultObject` resource: versioned vault secrets with optimistic-concurrency updates and a refresh that preserves the secret value for non-revealable objects.
- `Instance` resource: ephemeral container instances with readiness polling and in-place deadline extension.
- `Tenant` resource: IAM tenants with labels and policies.
- `getTenants` and `getInstances` functions with cursor pagination.
- Provider configuration `token` (`NSC_TOKEN`), `region` (`NSC_REGION`), and endpoint overrides.
- Examples and `pulumi new` templates in TypeScript, Python, Go, C#, Java, and YAML.
