package provider

import (
	"strings"
	"testing"
	"time"

	buildkitev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/buildkite"
	computev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/compute/v1beta"
	iamv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/iam/v1beta"
	registryv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/registry/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// These tests drive every lookup function and the SharedImage and
// InstanceIngress resources against the stateful fake in fake_namespace_test.go.

func lcFnOutputs(fn schema.FunctionSpec) map[string]schema.PropertySpec {
	if fn.ReturnType != nil && fn.ReturnType.ObjectTypeSpec != nil {
		return fn.ReturnType.ObjectTypeSpec.Properties
	}
	if fn.Outputs != nil {
		return fn.Outputs.Properties
	}
	return nil
}

func lcInvoke(t *testing.T, s integration.Server, fn string, args map[string]any) (property.Map, error) {
	t.Helper()
	r, err := s.Invoke(pgp.InvokeRequest{Token: tokens.Type("namespace:index:" + fn), Args: pm(args)})
	if err != nil {
		return property.Map{}, err
	}
	return r.Return, nil
}

func lcInvokeOK(t *testing.T, s integration.Server, fn string, args map[string]any) property.Map {
	t.Helper()
	m, err := lcInvoke(t, s, fn, args)
	require.NoError(t, err, fn)
	return m
}

// requireNotFound requires the clear 'was not found' error naming the object.
func requireNotFound(t *testing.T, s integration.Server, fn string, args map[string]any, what string) {
	t.Helper()
	_, err := lcInvoke(t, s, fn, args)
	require.Error(t, err, fn)
	assert.Contains(t, err.Error(), "was not found", fn)
	assert.Contains(t, err.Error(), what, fn)
}

func TestLookup_VaultObject(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	plain := lcCreate(t, s, "VaultObject", lcSecret(pm(map[string]any{"description": "p", "labels": map[string]any{"env": "prod"}}), "value", "v1"))
	reveal := lcCreate(t, s, "VaultObject", lcSecret(pm(map[string]any{"description": "r", "revealable": true,
		"labels": map[string]any{"env": "dev"}}), "value", "open"))

	got := lcInvokeOK(t, s, "getVaultObject", map[string]any{"objectId": plain.ID})
	assert.Equal(t, plain.ID, str(got, "objectId"))
	assert.Equal(t, "p", str(got, "description"))
	assert.Equal(t, "v1", str(got, "version"))
	assert.False(t, lcHas(got, "value"), "a non-revealable value is never returned")
	assert.False(t, got.Get("revealable").AsBool())
	assert.Equal(t, "prod", lcMapOf(got, "labels").Get("env").AsString())

	got = lcInvokeOK(t, s, "getVaultObject", map[string]any{"objectId": reveal.ID})
	assert.Equal(t, "open", str(got, "value"))
	assert.True(t, got.Get("value").Secret(), "the value is a secret")
	assert.True(t, lcFnOutputs(lcSchema(t).Functions["namespace:index:getVaultObject"])["value"].Secret)

	requireNotFound(t, s, "getVaultObject", map[string]any{"objectId": "obj-missing"}, "obj-missing")
	lcDelete(t, s, "VaultObject", plain.ID, plain.Properties)
	f.with(func() { f.vault[plain.ID] = nil; delete(f.vault, plain.ID) })
	requireNotFound(t, s, "getVaultObject", map[string]any{"objectId": plain.ID}, plain.ID)

	// getVaultObjects filters by label and never lists values.
	all := lcInvokeOK(t, s, "getVaultObjects", nil)
	assert.Equal(t, 1, all.Get("objects").AsArray().Len())
	other := lcCreate(t, s, "VaultObject", lcSecret(pm(map[string]any{"labels": map[string]any{"env": "prod"}}), "value", "z"))
	prod := lcInvokeOK(t, s, "getVaultObjects", map[string]any{"labels": map[string]any{"env": "prod"}})
	require.Equal(t, 1, prod.Get("objects").AsArray().Len())
	assert.Equal(t, other.ID, str(prod.Get("objects").AsArray().Get(0).AsMap(), "objectId"))
	assert.False(t, lcHas(prod.Get("objects").AsArray().Get(0).AsMap(), "value"))
}

func TestLookup_Tenant(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	a := lcCreate(t, s, "Tenant", pm(map[string]any{"visibleName": "A", "externalAccountId": "ext-a"}))
	lcCreate(t, s, "Tenant", pm(map[string]any{"visibleName": "B", "externalAccountId": "ext-b"}))
	lcCreate(t, s, "Tenant", pm(map[string]any{"visibleName": "B2", "externalAccountId": "ext-b"}))

	got := lcInvokeOK(t, s, "getTenant", map[string]any{"tenantId": a.ID})
	assert.Equal(t, "A", str(got, "visibleName"))
	assert.Equal(t, "ext-a", str(got, "externalAccountId"))
	got = lcInvokeOK(t, s, "getTenant", map[string]any{"externalAccountId": "ext-a"})
	assert.Equal(t, a.ID, str(got, "tenantId"))

	requireNotFound(t, s, "getTenant", map[string]any{"tenantId": "tenant-nope"}, "tenant-nope")
	requireNotFound(t, s, "getTenant", map[string]any{"externalAccountId": "ext-none"}, "ext-none")
	_, err := lcInvoke(t, s, "getTenant", map[string]any{"externalAccountId": "ext-b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "look one up by tenantId", "an ambiguous external account is an error")
	for _, args := range []map[string]any{nil, {"tenantId": a.ID, "externalAccountId": "ext-a"}} {
		_, err := lcInvoke(t, s, "getTenant", args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one of")
	}
}

func TestLookup_Instance(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	in := lcWith(lcInstanceInputs(), map[string]any{"containers": []any{map[string]any{"name": "web", "imageRef": "nginx"}}})
	created := lcCreate(t, s, "Instance", in)
	id := created.ID

	got := lcInvokeOK(t, s, "getInstance", map[string]any{"instanceId": id})
	assert.Equal(t, id, str(got, "instanceId"))
	assert.Equal(t, "eu", str(got, "region"))
	assert.Equal(t, "RUNNING", str(got, "status"))
	assert.Equal(t, "ci", str(got, "documentedPurpose"))
	assert.Equal(t, 2, int(lcMapOf(got, "shape").Get("virtualCpu").AsNumber()))
	assert.Equal(t, 1, int(got.Get("containerCount").AsNumber()), "an instance with containers is readable, unlike import")
	assert.Equal(t, "a", lcMapOf(got, "labels").Get("team").AsString())

	// Another region does not see the instance; an explicit region does.
	_, err := lcInvoke(t, s, "getInstance", map[string]any{"instanceId": id, "region": "us"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not found")
	got = lcInvokeOK(t, s, "getInstance", map[string]any{"instanceId": id, "region": "eu"})
	assert.Equal(t, id, str(got, "instanceId"))

	lcDelete(t, s, "Instance", id, created.Properties)
	requireNotFound(t, s, "getInstance", map[string]any{"instanceId": id}, id)
}

func TestLookup_GithubProfile(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	created := lcCreate(t, s, "GithubProfile", profileInputs("d"))

	for _, args := range []map[string]any{{"profileId": created.ID}, {"tag": "fast"}} {
		got := lcInvokeOK(t, s, "getGithubProfile", args)
		assert.Equal(t, created.ID, str(got, "profileId"))
		assert.Equal(t, "fast", str(got, "tag"))
		assert.Equal(t, "d", str(got, "description"))
	}
	requireNotFound(t, s, "getGithubProfile", map[string]any{"tag": "none"}, "none")
	requireNotFound(t, s, "getGithubProfile", map[string]any{"profileId": "p-none"}, "p-none")
	_, err := lcInvoke(t, s, "getGithubProfile", map[string]any{"profileId": created.ID, "tag": "fast"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one of")
}

func TestLookup_TenantGroupAndBlueprint(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	lcCreate(t, s, "TenantGroup", pm(map[string]any{"name": "gold", "policies": []any{map[string]any{"policy": "quota", "value": "10"}}}))
	f.with(func() { f.groups["gold"].EnrolledTenantIds = []string{"t1", "t2"} })
	got := lcInvokeOK(t, s, "getTenantGroup", map[string]any{"name": "gold"})
	assert.Equal(t, 2, got.Get("enrolledTenantIds").AsArray().Len())
	assert.Equal(t, 1, got.Get("policies").AsArray().Len())
	requireNotFound(t, s, "getTenantGroup", map[string]any{"name": "tin"}, "tin")

	lcCreate(t, s, "Blueprint", pm(map[string]any{"name": "base", "dockerfileContent": "FROM alpine"}))
	bp := lcInvokeOK(t, s, "getBlueprint", map[string]any{"name": "base"})
	assert.Equal(t, "FROM alpine", str(bp, "dockerfileContent"))
	assert.Equal(t, "bp-base-v1", str(bp, "blueprintId"))
	requireNotFound(t, s, "getBlueprint", map[string]any{"name": "none"}, "none")
}

func TestLookup_EgressPolicy(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	lcCreate(t, s, "EgressPolicy", lcEgressInputs())
	lcCreate(t, s, "EgressPolicy", lcWith(lcEgressInputs(), map[string]any{"tag": "another"}))

	got := lcInvokeOK(t, s, "getEgressPolicy", map[string]any{"tag": "locked"})
	assert.Equal(t, "locked", str(got, "tag"))
	assert.Equal(t, "BLOCK", str(got, "mode"))
	assert.Equal(t, 2, got.Get("rules").AsArray().Len())
	assert.Equal(t, 1, got.Get("additionalRulesets").AsArray().Len())
	assert.False(t, lcHas(lcMapOf(got.Get("rules").AsArray().Get(1).AsMap(), "inject"), "value"), "the API redacts inject secrets")
	requireNotFound(t, s, "getEgressPolicy", map[string]any{"tag": "none"}, "none")

	all := lcInvokeOK(t, s, "getEgressPolicies", nil)
	require.Equal(t, 2, all.Get("policies").AsArray().Len())
	assert.Equal(t, "another", str(all.Get("policies").AsArray().Get(0).AsMap(), "tag"))
}

func TestLookup_BuildkiteQueue(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	f.with(func() {
		f.queues = []*buildkitev1.BuildkiteQueue{
			{Id: "q-1", QueueName: "ci", Cluster: "c1", Settings: &buildkitev1.QueueSettings{EgressPolicyTag: "locked"}},
			{Id: "q-2", QueueName: "deploy", Cluster: "c1"},
		}
	})
	for _, args := range []map[string]any{{"queueId": "q-1"}, {"queueName": "ci"}} {
		got := lcInvokeOK(t, s, "getBuildkiteQueue", args)
		assert.Equal(t, "q-1", str(got, "queueId"))
		assert.Equal(t, "ci", str(got, "queueName"))
		assert.Equal(t, "c1", str(got, "cluster"))
		assert.Equal(t, "locked", str(got, "egressPolicyTag"))
	}
	requireNotFound(t, s, "getBuildkiteQueue", map[string]any{"queueName": "none"}, "none")
	requireNotFound(t, s, "getBuildkiteQueue", map[string]any{"queueId": "q-none"}, "q-none")
	_, err := lcInvoke(t, s, "getBuildkiteQueue", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one of")
	all := lcInvokeOK(t, s, "getBuildkiteQueues", nil)
	assert.Equal(t, 2, all.Get("queues").AsArray().Len())
}

func TestLookup_RegistryPolicies(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	f.with(func() {
		f.regDefault = nsFakeRegPolicy{rev: 5, secs: 7200, enf: registryv1.ExpirationEnforcement_EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE}
		f.repos["app"] = &nsFakeRegPolicy{rev: 2, secs: 100}
	})
	d := lcInvokeOK(t, s, "getRegistryDefaultPolicy", nil)
	assert.Equal(t, 7200, int(d.Get("defaultExpirationSeconds").AsNumber()))
	assert.Equal(t, noEnforce, str(d, "expirationEnforcement"))
	assert.Equal(t, 5, int(d.Get("revision").AsNumber()))

	r := lcInvokeOK(t, s, "getRegistryRepositoryPolicy", map[string]any{"repository": "app"})
	assert.Equal(t, 100, int(r.Get("expirationSeconds").AsNumber()))
	assert.Equal(t, 2, int(r.Get("revision").AsNumber()))
	assert.False(t, lcHas(r, "expirationEnforcement"), "an unset enforcement stays unset")
	requireNotFound(t, s, "getRegistryRepositoryPolicy", map[string]any{"repository": "none"}, "none")
}

func TestLookup_RevokableToken(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	grant := map[string]any{"resourceType": "instance", "resourceId": "", "actions": []any{"list"}}
	live := lcCreate(t, s, "RevokableToken", pm(map[string]any{"name": "ci", "description": "d", "grants": []any{grant}}))
	dead := lcCreate(t, s, "RevokableToken", pm(map[string]any{"name": "old"}))
	lcDelete(t, s, "RevokableToken", dead.ID, dead.Properties)

	got := lcInvokeOK(t, s, "getRevokableToken", map[string]any{"tokenId": live.ID})
	assert.Equal(t, "ci", str(got, "name"))
	assert.Equal(t, "ACTIVE", str(got, "tokenState"))
	assert.Equal(t, 1, got.Get("grants").AsArray().Len())
	assert.False(t, lcHas(got, "bearerToken"))
	revoked := lcInvokeOK(t, s, "getRevokableToken", map[string]any{"tokenId": dead.ID})
	assert.Equal(t, "REVOKED", str(revoked, "tokenState"), "a lookup reports revoked tokens instead of hiding them")
	requireNotFound(t, s, "getRevokableToken", map[string]any{"tokenId": "tok-none"}, "tok-none")

	assert.Equal(t, 1, lcInvokeOK(t, s, "getRevokableTokens", nil).Get("tokens").AsArray().Len())
	assert.Equal(t, 2, lcInvokeOK(t, s, "getRevokableTokens", map[string]any{"includeRevoked": true}).Get("tokens").AsArray().Len())
}

// Import works from the token ID alone: metadata only, no bearer value, and the
// program's declared inputs then diff clean.
func TestLifecycle_RevokableTokenImport(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "RevokableToken"
	grant := map[string]any{"resourceType": "instance", "resourceId": "", "actions": []any{"list"}}
	in := pm(map[string]any{"name": "ci", "description": "d", "grants": []any{grant}})
	created := lcCreate(t, s, typ, in)

	imp := lcImport(t, s, typ, created.ID)
	assert.Equal(t, "ci", str(imp.Inputs, "name"))
	assert.Equal(t, "d", str(imp.Inputs, "description"))
	assert.Equal(t, 1, imp.Inputs.Get("grants").AsArray().Len())
	assert.Equal(t, "", str(imp.Properties, "bearerToken"), "the bearer value cannot be recovered")
	assert.Equal(t, "ACTIVE", str(imp.Properties, "tokenState"))
	lcNoChange(t, lcDiff(t, s, typ, created.ID, imp.Properties, imp.Inputs), "no-op diff after import")
	lcNoChange(t, lcDiff(t, s, typ, created.ID, imp.Properties, in), "declared inputs match the imported token")

	// A token without grants imports without inventing any.
	bare := lcCreate(t, s, typ, pm(map[string]any{"name": "bare"}))
	bi := lcImport(t, s, typ, bare.ID)
	assert.False(t, lcHas(bi.Inputs, "grants"))
	lcNoChange(t, lcDiff(t, s, typ, bare.ID, bi.Properties, pm(map[string]any{"name": "bare"})), "no-op diff after import without grants")

	// Revoked and expired tokens still read as gone on import.
	f.with(func() { f.tokens[0].tok.State = iamv1.RevokableToken_EXPIRED })
	r, err := s.Read(pgp.ReadRequest{ID: created.ID, Urn: urn(typ, "r"), Properties: property.NewMap(nil), Inputs: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Empty(t, r.ID)
}

func TestLookup_TenantMembersAndPolicies(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	f.with(func() {
		f.members = []*iamv1.TenantMember{{UserId: "u1", FirstName: "Ada", LastName: "L", Email: "ada@example.test", Role: iamv1.TenantMemberRole_TENANT_MEMBER_ROLE_ADMIN}}
		f.callerPols = []*iamv1.TenantPolicy{
			{Id: "p1", Policy: "quota", Value: "5"},
			{Id: "p2", ExpirationPolicy: &iamv1.TenantPolicy_ExpirationPolicy{ExpiresAt: timestamppb.New(f.now.Add(time.Hour))}},
		}
	})
	m := lcInvokeOK(t, s, "getTenantMembers", nil).Get("members").AsArray()
	require.Equal(t, 1, m.Len())
	assert.Equal(t, "ada@example.test", str(m.Get(0).AsMap(), "email"))
	assert.Equal(t, "TENANT_MEMBER_ROLE_ADMIN", str(m.Get(0).AsMap(), "role"))

	p := lcInvokeOK(t, s, "getTenantPolicies", nil)
	assert.Equal(t, 7, int(p.Get("revision").AsNumber()))
	require.Equal(t, 2, p.Get("policies").AsArray().Len())
	assert.Equal(t, "quota", str(p.Get("policies").AsArray().Get(0).AsMap(), "policy"))
	assert.Equal(t, "2026-11-01T13:00:00Z", str(p.Get("policies").AsArray().Get(1).AsMap(), "expiresAt"))
}

// ---------------------------------------------------------------------------
// SharedImage
// ---------------------------------------------------------------------------

func lcSharedInputs() property.Map {
	return pm(map[string]any{"repository": "app", "digest": "sha256:abc", "visibility": "PUBLIC", "suffix": "v1",
		"expiresAt": "2026-12-31T23:59:59Z"})
}

func TestLifecycle_SharedImage(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "SharedImage"
	in := lcSharedInputs()

	created := lcCreate(t, s, typ, in)
	id := created.ID
	assert.Equal(t, "public.nscr.io/"+id+"/v1", str(created.Properties, "imageRef"))
	assert.Equal(t, id, str(created.Properties, "sharedImageId"))
	rq := lastReq[*registryv1.ShareImageRequest](f, "ShareImage")
	assert.Equal(t, "app", rq.GetRepository())
	assert.Equal(t, registryv1.Visibility_PUBLIC, rq.GetVisibility())
	assert.Equal(t, "v1", rq.GetSuffix())
	lcNoChange(t, lcDiff(t, s, typ, id, created.Properties, in), "no-op diff after create")

	// Refresh reports the server values; an equal instant in another format is not drift.
	read := lcRead(t, s, typ, id, created.Properties, lcWith(in, map[string]any{"expiresAt": "2026-12-31T15:59:59-08:00"}))
	assert.Equal(t, "2026-12-31T15:59:59-08:00", str(read.Inputs, "expiresAt"))
	lcNoChange(t, lcDiff(t, s, typ, id, read.Properties, lcWith(in, map[string]any{"expiresAt": "2026-12-31T15:59:59-08:00"})), "equal instant")

	eng := lcEngine(t, f, "eu")
	fresh := lcRead(t, s, typ, id, created.Properties, in)
	lcRequireEngineNoChange(t, lcEngineDiff(t, eng, typ, id, fresh.Properties, fresh.Inputs, in), "engine path after refresh")

	// The share cannot be updated: every input replaces it.
	for k, v := range map[string]any{"repository": "other", "digest": "sha256:def", "visibility": "PARTNER", "suffix": "v2",
		"expiresAt": "2027-01-01T00:00:00Z"} {
		lcKind(t, typ, lcDiff(t, s, typ, id, created.Properties, lcWith(in, map[string]any{k: v})), k, pgp.UpdateReplace)
	}

	// Drift: the server expires the share earlier.
	f.with(func() { f.sharedImages[0].ExpiresAt = timestamppb.New(f.now.Add(time.Hour)) })
	drift := lcRead(t, s, typ, id, created.Properties, in)
	assert.Equal(t, "2026-11-01T13:00:00Z", str(drift.Inputs, "expiresAt"))
	lcKind(t, typ, lcDiff(t, s, typ, id, drift.Properties, in), "expiresAt", pgp.UpdateReplace)

	// Import by ID alone, then no change against the imported inputs.
	imp := lcImport(t, s, typ, id)
	assert.Equal(t, "app", str(imp.Inputs, "repository"))
	assert.Equal(t, "PUBLIC", str(imp.Inputs, "visibility"))
	assert.Equal(t, "v1", str(imp.Inputs, "suffix"))
	lcNoChange(t, lcDiff(t, s, typ, id, imp.Properties, imp.Inputs), "no-op diff after import")

	// Lookups.
	got := lcInvokeOK(t, s, "getSharedImage", map[string]any{"sharedImageId": id})
	assert.Equal(t, "app", str(got, "repository"))
	assert.Equal(t, "public.nscr.io/"+id+"/v1", str(got, "imageRef"))
	requireNotFound(t, s, "getSharedImage", map[string]any{"sharedImageId": "shared-none"}, "shared-none")
	lcCreate(t, s, typ, pm(map[string]any{"repository": "web", "digest": "sha256:1", "visibility": "PARTNER"}))
	assert.Equal(t, 2, lcInvokeOK(t, s, "getSharedImages", nil).Get("sharedImages").AsArray().Len())
	assert.Equal(t, 1, lcInvokeOK(t, s, "getSharedImages", map[string]any{"visibility": "PARTNER"}).Get("sharedImages").AsArray().Len())
	assert.Equal(t, 1, lcInvokeOK(t, s, "getSharedImages", map[string]any{"repository": "app"}).Get("sharedImages").AsArray().Len())
	_, err := lcInvoke(t, s, "getSharedImages", map[string]any{"visibility": "SECRET"})
	require.Error(t, err)

	// Deleted outside Pulumi; destroy is quiet and idempotent.
	f.with(func() { f.sharedImages = nil })
	lcGone(t, s, typ, id, created.Properties, in)
	lcDelete(t, s, typ, id, created.Properties)
	lcDelete(t, s, typ, id, created.Properties)

	// Destroy unshares.
	again := lcCreate(t, s, typ, in)
	lcDelete(t, s, typ, again.ID, again.Properties)
	f.with(func() { assert.Empty(t, f.sharedImages) })
}

func TestSharedImage_Check(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	bad := checkFails(t, s, "SharedImage", map[string]any{"repository": "", "digest": "", "visibility": "SECRET", "expiresAt": "tomorrow"})
	props := map[string]bool{}
	for _, c := range bad {
		props[c.Property] = true
	}
	for _, p := range []string{"repository", "digest", "visibility", "expiresAt"} {
		assert.True(t, props[p], "%s must be rejected", p)
	}
	assert.Empty(t, checkFails(t, s, "SharedImage", map[string]any{"repository": "a", "digest": "d", "visibility": "PARTNER"}))
}

// ---------------------------------------------------------------------------
// InstanceIngress
// ---------------------------------------------------------------------------

func TestLifecycle_InstanceIngress(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "InstanceIngress"
	inst := lcCreate(t, s, "Instance", lcInstanceInputs())
	in := pm(map[string]any{"instanceId": inst.ID, "name": "web", "port": 8080,
		"httpMatchRules": []any{map[string]any{"methods": []any{"GET"}, "path": "/health", "doesNotRequireAuth": true}}})

	created := lcCreate(t, s, typ, in)
	ingID := str(created.Properties, "ingressId")
	assert.Equal(t, inst.ID+"/"+ingID, created.ID)
	assert.Equal(t, "web-"+inst.ID+".example.test", str(created.Properties, "fqdn"))
	assert.Equal(t, "eu", str(created.Properties, "effectiveRegion"))
	rq := lastReq[*computev1.CreateIngressRequest](f, "CreateIngress")
	assert.Equal(t, inst.ID, rq.GetInstanceId())
	require.Len(t, rq.GetIngresses(), 1)
	assert.Equal(t, int32(8080), rq.GetIngresses()[0].GetExportedPortBackend().GetPort())
	assert.Equal(t, "web", rq.GetIngresses()[0].GetName())
	require.Len(t, rq.GetIngresses()[0].GetHttpMatchRule(), 1)
	assert.Equal(t, []string{"GET"}, rq.GetIngresses()[0].GetHttpMatchRule()[0].GetMatch().GetMethod())
	assert.True(t, rq.GetIngresses()[0].GetHttpMatchRule()[0].GetDoesNotRequireAuth())
	lcNoChange(t, lcDiff(t, s, typ, created.ID, created.Properties, in), "no-op diff after create")

	// Refresh keeps what the API cannot report and reports name drift.
	read := lcRead(t, s, typ, created.ID, created.Properties, in)
	require.Equal(t, created.ID, read.ID)
	assert.Equal(t, 8080, int(read.Inputs.Get("port").AsNumber()))
	lcNoChange(t, lcDiff(t, s, typ, created.ID, read.Properties, in), "no-op diff after refresh")

	eng := lcEngine(t, f, "eu")
	lcRequireEngineNoChange(t, lcEngineDiff(t, eng, typ, created.ID, read.Properties, read.Inputs, in), "engine path after refresh")

	// No update call: every input replaces the ingress.
	for k, v := range map[string]any{"instanceId": "inst-x", "name": "api", "port": 9090, "backendIpAddress": "10.0.0.1",
		"wildcard": true, "region": "us", "httpMatchRules": []any{}} {
		lcKind(t, typ, lcDiff(t, s, typ, created.ID, created.Properties, lcWith(in, map[string]any{k: v})), k, pgp.UpdateReplace)
	}

	// Import from 'instanceId/ingressId' adopts the declared port and rules
	// instead of replacing, and from 'region/instanceId/ingressId' too.
	imp := lcImport(t, s, typ, created.ID)
	assert.Equal(t, inst.ID, str(imp.Inputs, "instanceId"))
	assert.Equal(t, "web", str(imp.Inputs, "name"))
	lcNoChange(t, lcDiff(t, s, typ, created.ID, imp.Properties, imp.Inputs), "no-op diff after import")
	lcNoChange(t, lcDiff(t, s, typ, created.ID, imp.Properties, in), "declared port and rules are accepted after import")
	lcKind(t, typ, lcDiff(t, s, typ, created.ID, imp.Properties, lcWith(in, map[string]any{"name": "other"})), "name", pgp.UpdateReplace)
	us := lcServer(t, f, "us") // provider region differs from the instance's region
	_, err := us.Read(pgp.ReadRequest{ID: created.ID, Urn: urn(typ, "r"), Properties: property.NewMap(nil), Inputs: property.NewMap(nil)})
	require.NoError(t, err)
	r3, err := us.Read(pgp.ReadRequest{ID: "eu/" + created.ID, Urn: urn(typ, "r"), Properties: property.NewMap(nil), Inputs: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Equal(t, inst.ID+"/"+ingID, r3.ID, "the region prefix is not part of the resource ID")
	assert.Equal(t, "eu", str(r3.Properties, "effectiveRegion"))

	// State written in eu stays readable and deletable from a us provider.
	rd := lcRead(t, us, typ, created.ID, created.Properties, in)
	assert.Equal(t, created.ID, rd.ID)

	// Lookup.
	got := lcInvokeOK(t, s, "getInstanceIngress", map[string]any{"instanceId": inst.ID, "name": "web"})
	assert.Equal(t, ingID, str(got, "ingressId"))
	assert.Equal(t, "eu", str(got, "region"))
	got = lcInvokeOK(t, s, "getInstanceIngress", map[string]any{"instanceId": inst.ID, "ingressId": ingID})
	assert.Equal(t, "web", str(got, "name"))
	requireNotFound(t, s, "getInstanceIngress", map[string]any{"instanceId": inst.ID, "name": "nope"}, "nope")
	requireNotFound(t, s, "getInstanceIngress", map[string]any{"instanceId": "inst-none", "name": "web"}, "inst-none")
	_, err = lcInvoke(t, s, "getInstanceIngress", map[string]any{"instanceId": inst.ID})
	require.Error(t, err)

	// Deleted outside Pulumi; destroy is quiet and idempotent. The instance
	// disappearing takes its ingresses with it.
	f.with(func() { f.ingresses[inst.ID] = nil })
	lcGone(t, s, typ, created.ID, created.Properties, in)
	lcDelete(t, s, typ, created.ID, created.Properties)
	lcDelete(t, s, typ, created.ID, created.Properties)
	again := lcCreate(t, s, typ, in)
	f.with(func() { delete(f.instances, inst.ID) })
	lcGone(t, s, typ, again.ID, again.Properties, in)
	lcDelete(t, s, typ, again.ID, again.Properties)
}

// The server may normalise or suffix the requested name. One request is sent,
// so the single ingress it returns is ours, and the ID must not be lost.
func TestLifecycle_InstanceIngressServerRenamesTheName(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	inst := lcCreate(t, s, "Instance", lcInstanceInputs())
	f.with(func() { f.ingressRename = func(n string) string { return n + "-x7k2" } })
	c := lcCreate(t, s, "InstanceIngress", pm(map[string]any{"instanceId": inst.ID, "name": "web", "port": 8080}))
	ingID := str(c.Properties, "ingressId")
	assert.Equal(t, inst.ID+"/"+ingID, c.ID)
	f.with(func() {
		require.Len(t, f.ingresses[inst.ID], 1)
		assert.Equal(t, f.ingresses[inst.ID][0].GetIngressId(), ingID)
		assert.Equal(t, "web-x7k2", f.ingresses[inst.ID][0].GetName())
	})
	assert.Equal(t, 1, f.calls["CreateIngress"], "exactly one create request")
}

// When the response carries no ingress the instance's ingresses are listed and
// matched by name; if that cannot identify it the error names the instance.
func TestLifecycle_InstanceIngressIdentifiedByListing(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	inst := lcCreate(t, s, "Instance", lcInstanceInputs())
	f.with(func() { f.ingressHideResponse = true })

	c := lcCreate(t, s, "InstanceIngress", pm(map[string]any{"instanceId": inst.ID, "name": "web", "port": 8080}))
	f.with(func() {
		require.Len(t, f.ingresses[inst.ID], 1)
		assert.Equal(t, inst.ID+"/"+f.ingresses[inst.ID][0].GetIngressId(), c.ID)
	})
	assert.Equal(t, 1, f.calls["ListIngresses"])

	// A renamed ingress cannot be matched by listing: fail, naming the instance.
	f.with(func() { f.ingressRename = func(n string) string { return n + "-x7k2" } })
	_, err := s.Create(pgp.CreateRequest{Urn: urn("InstanceIngress", "r2"),
		Properties: pm(map[string]any{"instanceId": inst.ID, "name": "api", "port": 9090})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), inst.ID)
	assert.Contains(t, err.Error(), "could not be identified")

	// A wildcard ingress has no name to match by.
	_, err = s.Create(pgp.CreateRequest{Urn: urn("InstanceIngress", "r3"),
		Properties: pm(map[string]any{"instanceId": inst.ID, "wildcard": true, "port": 80})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), inst.ID)
}

func TestLifecycle_InstanceIngressDestroyRemoves(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	inst := lcCreate(t, s, "Instance", lcInstanceInputs())
	in := pm(map[string]any{"instanceId": inst.ID, "wildcard": true, "port": 80})
	c := lcCreate(t, s, "InstanceIngress", in)
	f.with(func() { assert.Len(t, f.ingresses[inst.ID], 1) })
	lcDelete(t, s, "InstanceIngress", c.ID, c.Properties)
	f.with(func() { assert.Empty(t, f.ingresses[inst.ID]) })
	rq := lastReq[*computev1.DeleteIngressRequest](f, "DeleteIngress")
	assert.Equal(t, inst.ID, rq.GetInstanceId())
	assert.True(t, strings.HasPrefix(rq.GetIngressId(), "ing-"))
}

func TestInstanceIngress_Check(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	failsOn := func(in map[string]any) map[string]bool {
		out := map[string]bool{}
		for _, c := range checkFails(t, s, "InstanceIngress", in) {
			out[c.Property] = true
		}
		return out
	}
	assert.True(t, failsOn(map[string]any{"instanceId": "", "name": "web", "port": 80})["instanceId"])
	assert.True(t, failsOn(map[string]any{"instanceId": "i", "name": "web", "port": 0})["port"])
	assert.True(t, failsOn(map[string]any{"instanceId": "i", "name": "web", "port": 70000})["port"])
	assert.True(t, failsOn(map[string]any{"instanceId": "i", "port": 80})["name"], "a name or wildcard is required")
	assert.True(t, failsOn(map[string]any{"instanceId": "i", "name": "web", "wildcard": true, "port": 80})["name"])
	assert.True(t, failsOn(map[string]any{"instanceId": "i", "name": "Web!", "port": 80})["name"])
	assert.Empty(t, failsOn(map[string]any{"instanceId": "i", "wildcard": true, "port": 80}))
	assert.Empty(t, failsOn(map[string]any{"instanceId": "i", "name": "web-1", "port": 443}))
}

// ---------------------------------------------------------------------------
// Schema contract for the lookup functions
// ---------------------------------------------------------------------------

// Every lookup is registered and none has a top-level 'id' (the schema generator
// reserves it) or an input named 'version'.
func TestSchema_LookupFunctionsContract(t *testing.T) {
	spec := lcSchema(t)
	for _, name := range []string{
		"getVaultObject", "getVaultObjects", "getTenant", "getInstance", "getGithubProfile", "getTenantGroup", "getBlueprint",
		"getEgressPolicy", "getEgressPolicies", "getBuildkiteQueue", "getBuildkiteQueues", "getRegistryDefaultPolicy",
		"getRegistryRepositoryPolicy", "getRevokableToken", "getRevokableTokens", "getTenantMembers", "getTenantPolicies",
		"getSharedImage", "getSharedImages", "getInstanceIngress",
	} {
		fn, ok := spec.Functions["namespace:index:"+name]
		require.True(t, ok, "%s must be registered", name)
		assert.NotEmpty(t, fn.Description, "%s needs a description", name)
		if fn.Inputs != nil {
			assert.NotContains(t, fn.Inputs.Properties, "id", name)
			assert.NotContains(t, fn.Inputs.Properties, "version", name)
		}
		out := lcFnOutputs(fn)
		assert.NotEmpty(t, out, "%s must have outputs", name)
		assert.NotContains(t, out, "id", name)
		assert.NotContains(t, out, "bearerToken", name)
	}
	for tok, r := range spec.Resources {
		assert.NotContains(t, r.InputProperties, "version", tok)
	}
	for _, typ := range []string{"SharedImage", "InstanceIngress"} {
		assert.Contains(t, spec.Resources, "namespace:index:"+typ)
	}
}
