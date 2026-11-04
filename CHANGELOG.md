# Changelog

## [Unreleased]

### Changed

- Updated the generated Namespace Cloud protobuf bindings (`buf.build/gen/go/namespace/cloud/protocolbuffers/go`) to the 2026-11-03 build (`v1.36.12-20261103090127-5e7839020ace.2`). Upstream removed the unused `SuspendInstanceRequest` and `WakeInstanceRequest` messages and added `EnsureBlueprintRequest` and `EnsureBlueprintResponse` (compute and private devbox APIs); the provider does not call any of them, so there are no resource or schema changes.
- `go.mod`/`go.sum` moved from `provider/` to the provider's top level (module path is now `github.com/pulumi/autoregistry/lib/providers/namespace`); the Makefile builds from there. No change to the provider binary, schema or SDKs.

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
