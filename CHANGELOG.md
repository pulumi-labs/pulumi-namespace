# Changelog

## [Unreleased]

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
