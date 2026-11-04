package provider

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	buildkitev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/buildkite"
	computev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/compute/v1beta"
	githubv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/github/v1beta"
	iamv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/iam/v1beta"
	networkv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/network/v1beta"
	registryv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/registry/v1beta"
	vaultv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/vault/v1beta"
	"buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/stdlib"
	"github.com/blang/semver"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/structpb"
)

// These tests drive every resource through the provider RPC surface against the
// stateful in-memory API in fake_namespace_test.go.

func lcConfig(f *nsFake, region string) map[string]property.Value {
	cfg := map[string]property.Value{
		"token": property.New(testToken).WithSecret(true),
		// "{region}" lets the fake observe which region a compute call went to.
		"computeEndpoint": property.New(f.srv.URL + "/{region}"),
		"iamEndpoint":     property.New(f.srv.URL),
		"globalEndpoint":  property.New(f.srv.URL),
	}
	if region != "" {
		cfg["region"] = property.New(region)
	}
	return cfg
}

func lcServer(t *testing.T, f *nsFake, region string) integration.Server {
	t.Helper()
	clearFallbackEnv(t)
	p, err := New()
	require.NoError(t, err)
	s, err := integration.NewServer(t.Context(), "namespace", semver.MustParse("0.0.1"), integration.WithProvider(p))
	require.NoError(t, err)
	require.NoError(t, s.Configure(pgp.ConfigureRequest{Args: property.NewMap(lcConfig(f, region))}))
	return s
}

// lcEngine builds the provider behind the gRPC surface the engine uses, telling
// it (like a current engine) to send old inputs, which selects the Diff branch
// integration.Server never reaches.
func lcEngine(t *testing.T, f *nsFake, region string) pulumirpc.ResourceProviderServer {
	t.Helper()
	clearFallbackEnv(t)
	p, err := New()
	require.NoError(t, err)
	srv, err := pgp.RawServer("namespace", "0.0.1", p)(nil)
	require.NoError(t, err)
	args := map[string]any{
		"token":           testToken,
		"computeEndpoint": f.srv.URL + "/{region}",
		"iamEndpoint":     f.srv.URL,
		"globalEndpoint":  f.srv.URL,
	}
	if region != "" {
		args["region"] = region
	}
	st, err := structpb.NewStruct(args)
	require.NoError(t, err)
	_, err = srv.Configure(t.Context(), &pulumirpc.ConfigureRequest{
		Args: st, AcceptSecrets: true, SendsOldInputs: true, SendsOldInputsToDelete: true,
	})
	require.NoError(t, err)
	return srv
}

func lcStruct(t *testing.T, m property.Map) *structpb.Struct {
	t.Helper()
	pmap := resource.ToResourcePropertyValue(property.New(m)).ObjectValue()
	st, err := plugin.MarshalProperties(pmap, plugin.MarshalOptions{KeepSecrets: true, KeepUnknowns: true})
	require.NoError(t, err)
	return st
}

// lcEngineDiff runs Diff the way the engine does: olds is the stored state,
// oldInputs the inputs stored with it, news the program's inputs.
func lcEngineDiff(t *testing.T, srv pulumirpc.ResourceProviderServer, typ, id string, state, oldInputs, news property.Map) *pulumirpc.DiffResponse {
	t.Helper()
	resp, err := srv.Diff(t.Context(), &pulumirpc.DiffRequest{
		Id: id, Urn: string(urn(typ, "r")), Olds: lcStruct(t, state), OldInputs: lcStruct(t, oldInputs), News: lcStruct(t, news),
	})
	require.NoError(t, err)
	return resp
}

func lcRequireEngineNoChange(t *testing.T, resp *pulumirpc.DiffResponse, msg string) {
	t.Helper()
	assert.Equal(t, pulumirpc.DiffResponse_DIFF_NONE, resp.Changes, "%s: %v", msg, resp.DetailedDiff)
	assert.Empty(t, resp.Replaces, msg)
}

func lcWith(m property.Map, kv map[string]any) property.Map {
	out := m.AsMap()
	if out == nil {
		out = map[string]property.Value{}
	}
	cp := map[string]property.Value{}
	for k, v := range out {
		cp[k] = v
	}
	for k, v := range kv {
		if v == nil {
			delete(cp, k)
			continue
		}
		cp[k] = pv(v)
	}
	return property.NewMap(cp)
}

func lcSecret(m property.Map, k, v string) property.Map {
	cp := map[string]property.Value{}
	for kk, vv := range m.AsMap() {
		cp[kk] = vv
	}
	cp[k] = property.New(v).WithSecret(true)
	return property.NewMap(cp)
}

func lcCreate(t *testing.T, s integration.Server, typ string, in property.Map) pgp.CreateResponse {
	t.Helper()
	r, err := s.Create(pgp.CreateRequest{Urn: urn(typ, "r"), Properties: in})
	require.NoError(t, err)
	require.NotEmpty(t, r.ID)
	return r
}

func lcDiff(t *testing.T, s integration.Server, typ, id string, state, in property.Map) pgp.DiffResponse {
	t.Helper()
	d, err := s.Diff(pgp.DiffRequest{Urn: urn(typ, "r"), ID: id, State: state, Inputs: in, OldInputs: state})
	require.NoError(t, err)
	return d
}

func lcNoChange(t *testing.T, d pgp.DiffResponse, msg string) {
	t.Helper()
	assert.False(t, d.HasChanges, "%s: unexpected diff %+v", msg, d.DetailedDiff)
}

var (
	lcSchemaOnce sync.Once
	lcSchemaSpec schema.PackageSpec
)

func lcSchema(t *testing.T) schema.PackageSpec {
	t.Helper()
	lcSchemaOnce.Do(func() {
		p, err := New()
		require.NoError(t, err)
		lcSchemaSpec, err = pgp.GetSchema(context.Background(), "namespace", "0.1.0", p)
		require.NoError(t, err)
	})
	return lcSchemaSpec
}

// lcKind requires diff entries for key (the key itself or anything under it).
// With want == pgp.UpdateReplace the property must replace: either the diff
// says so, or the schema flags the top-level input replaceOnChanges (which the
// SDKs hand to the engine; pulumi-go-provider only marks the top-level key in
// the diff itself, so nested paths such as labels.env surface as plain updates).
// With want == pgp.Update the change must update in place (adds and deletes of
// the property count as updates).
func lcKind(t *testing.T, typ string, d pgp.DiffResponse, key string, want pgp.DiffKind) {
	t.Helper()
	require.True(t, d.HasChanges, "expected a change on %s", key)
	top, _, _ := strings.Cut(key, ".")
	flagged := lcSchema(t).Resources["namespace:index:"+typ].InputProperties[top].ReplaceOnChanges
	found := false
	for k, v := range d.DetailedDiff {
		if k != key && !strings.HasPrefix(k, key+".") && !strings.HasPrefix(k, key+"[") {
			continue
		}
		found = true
		replaces := v.Kind == pgp.UpdateReplace || v.Kind == pgp.AddReplace || v.Kind == pgp.DeleteReplace || flagged
		assert.Equal(t, want == pgp.UpdateReplace, replaces, "%s is %s (schema replaceOnChanges=%v), want %s", k, v.Kind, flagged, want)
	}
	require.True(t, found, "no diff on %s: %+v", key, d.DetailedDiff)
}

func lcRead(t *testing.T, s integration.Server, typ, id string, state, inputs property.Map) pgp.ReadResponse {
	t.Helper()
	r, err := s.Read(pgp.ReadRequest{ID: id, Urn: urn(typ, "r"), Properties: state, Inputs: inputs})
	require.NoError(t, err)
	return r
}

// lcImport reads by ID alone, the way pulumi import does.
func lcImport(t *testing.T, s integration.Server, typ, id string) pgp.ReadResponse {
	t.Helper()
	r, err := s.Read(pgp.ReadRequest{ID: id, Urn: urn(typ, "r"), Properties: property.NewMap(nil), Inputs: property.NewMap(nil)})
	require.NoError(t, err)
	require.Equal(t, id, r.ID, "import of %s %s", typ, id)
	return r
}

func lcUpdate(t *testing.T, s integration.Server, typ, id string, state, old, in property.Map) pgp.UpdateResponse {
	t.Helper()
	r, err := s.Update(pgp.UpdateRequest{ID: id, Urn: urn(typ, "r"), State: state, OldInputs: old, Inputs: in})
	require.NoError(t, err)
	return r
}

func lcDelete(t *testing.T, s integration.Server, typ, id string, state property.Map) {
	t.Helper()
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: id, Urn: urn(typ, "r"), Properties: state}))
}

func lcGone(t *testing.T, s integration.Server, typ, id string, state, inputs property.Map) {
	t.Helper()
	r := lcRead(t, s, typ, id, state, inputs)
	assert.Empty(t, r.ID, "%s %s deleted outside Pulumi must read as gone", typ, id)
}

func lcMapOf(m property.Map, k string) property.Map {
	v, ok := m.GetOk(k)
	if !ok || !v.IsMap() {
		return property.NewMap(nil)
	}
	return v.AsMap()
}

func lcHas(m property.Map, k string) bool { _, ok := m.GetOk(k); return ok }

func lcStrOf(m property.Map, path ...string) string {
	cur := m
	for i, p := range path {
		if i == len(path)-1 {
			return str(cur, p)
		}
		cur = lcMapOf(cur, p)
	}
	return ""
}

// ---------------------------------------------------------------------------
// VaultObject
// ---------------------------------------------------------------------------

func TestLifecycle_VaultObject(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "VaultObject"
	in := lcSecret(pm(map[string]any{"description": "db", "labels": map[string]any{"env": "prod"}}), "value", "s3cr3t")

	created := lcCreate(t, s, typ, in)
	id := created.ID
	assert.Equal(t, "v1", str(created.Properties, "version"))
	f.with(func() {
		assert.Equal(t, "s3cr3t", f.vault[id].value)
		assert.Len(t, f.vault[id].md.GetLabels(), 2, "the server attached its own label")
	})
	lcNoChange(t, lcDiff(t, s, typ, id, created.Properties, in), "no-op diff after create")

	// Refresh: the undeclared server label must not become an input.
	read := lcRead(t, s, typ, id, created.Properties, in)
	assert.Equal(t, map[string]property.Value{"env": property.New("prod")}, lcMapOf(read.Inputs, "labels").AsMap())
	assert.Equal(t, "s3cr3t", str(read.Properties, "value"), "value the API cannot return is kept")
	lcNoChange(t, lcDiff(t, s, typ, id, read.Properties, in), "no-op diff after refresh")

	// Update: only the value updates in place, guarded by the version.
	in2 := lcSecret(in, "value", "n3w")
	d := lcDiff(t, s, typ, id, created.Properties, in2)
	lcKind(t, typ, d, "value", pgp.Update)
	assert.NotContains(t, d.DetailedDiff, "labels")
	upd := lcUpdate(t, s, typ, id, created.Properties, in, in2)
	assert.Equal(t, "v2", str(upd.Properties, "version"))
	req := lastReq[*vaultv1.UpdateObjectRequest](f, "UpdateObject")
	assert.Equal(t, "n3w", req.GetNewValue())
	assert.Equal(t, "v1", req.GetIfVersionMatches())
	f.with(func() { assert.Equal(t, "n3w", f.vault[id].value) })
	// A concurrent change makes the guarded update fail instead of overwriting.
	f.with(func() { f.vault[id].md.Version = "v9" })
	_, err := s.Update(pgp.UpdateRequest{ID: id, Urn: urn(typ, "r"), State: upd.Properties, OldInputs: in2, Inputs: lcSecret(in, "value", "again")})
	require.Error(t, err)

	// Every other input replaces.
	for k, v := range map[string]any{"description": "other", "labels": map[string]any{"env": "dev"}, "revealable": true} {
		lcKind(t, typ, lcDiff(t, s, typ, id, created.Properties, lcWith(in, map[string]any{k: v})), k, pgp.UpdateReplace)
	}

	// Drift of a declared label and of the description shows on refresh.
	f.with(func() {
		f.vault[id].md.Description = "changed"
		f.vault[id].md.Labels = []*stdlib.Label{{Name: "env", Value: "staging"}, {Name: systemLabelName, Value: systemLabelValue}}
	})
	drift := lcRead(t, s, typ, id, created.Properties, in)
	assert.Equal(t, "staging", lcStrOf(drift.Inputs, "labels", "env"))
	assert.Equal(t, "changed", str(drift.Inputs, "description"))
	lcKind(t, typ, lcDiff(t, s, typ, id, drift.Properties, in), "labels", pgp.UpdateReplace)

	// An object declared without labels never replaces because of server labels,
	// checked on the path the engine uses (old inputs sent).
	plain := lcSecret(pm(map[string]any{}), "value", "p")
	pc := lcCreate(t, s, typ, plain)
	pr := lcRead(t, s, typ, pc.ID, pc.Properties, plain)
	assert.False(t, lcHas(pr.Inputs, "labels"), "undeclared server labels must not become inputs: %v", pr.Inputs)
	lcNoChange(t, lcDiff(t, s, typ, pc.ID, pr.Properties, plain), "refresh without declared labels")
	eng := lcEngine(t, f, "eu")
	lcRequireEngineNoChange(t, lcEngineDiff(t, eng, typ, pc.ID, pr.Properties, pr.Inputs, plain), "engine path after refresh")

	// Import by ID alone: the server's full view, then no change.
	imp := lcImport(t, s, typ, id)
	assert.Equal(t, "env", firstKey(lcMapOf(imp.Inputs, "labels"), "env"))
	assert.Equal(t, "", str(imp.Inputs, "value"), "a non-revealable value cannot be imported")
	lcNoChange(t, lcDiff(t, s, typ, id, imp.Properties, imp.Inputs), "no-op diff after import")
	// A revealable object imports with its value.
	rev := lcCreate(t, s, typ, lcWith(lcSecret(pm(map[string]any{}), "value", "shown"), map[string]any{"revealable": true}))
	rimp := lcImport(t, s, typ, rev.ID)
	assert.Equal(t, "shown", str(rimp.Inputs, "value"))
	lcNoChange(t, lcDiff(t, s, typ, rev.ID, rimp.Properties, rimp.Inputs), "no-op diff after revealable import")

	// Deleted outside Pulumi, then destroyed: both quiet and idempotent.
	f.with(func() { delete(f.vault, id) })
	lcGone(t, s, typ, id, created.Properties, in)
	lcDelete(t, s, typ, id, created.Properties)
	lcDelete(t, s, typ, id, created.Properties)
	// A genuine delete, then a repeat.
	lcDelete(t, s, typ, pc.ID, pc.Properties)
	lcDelete(t, s, typ, pc.ID, pc.Properties)
	f.with(func() { assert.NotContains(t, f.vault, pc.ID) })
}

func firstKey(m property.Map, k string) string {
	if _, ok := m.GetOk(k); ok {
		return k
	}
	return ""
}

// ---------------------------------------------------------------------------
// Tenant
// ---------------------------------------------------------------------------

func TestLifecycle_Tenant(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "Tenant"
	pol := map[string]any{"id": "p1", "policy": "quota", "value": "10"}
	in := pm(map[string]any{"visibleName": "Acme", "labels": map[string]any{"team": "a"},
		"policies": []any{pol}, "externalAccountId": "ext-1", "creatorId": "me"})

	created := lcCreate(t, s, typ, in)
	id := created.ID
	lcNoChange(t, lcDiff(t, s, typ, id, created.Properties, in), "no-op diff after create")

	// creatorId is not echoed by the API; the declared value must survive refresh.
	read := lcRead(t, s, typ, id, created.Properties, in)
	assert.Equal(t, "me", str(read.Inputs, "creatorId"))
	assert.Equal(t, map[string]property.Value{"team": property.New("a")}, lcMapOf(read.Inputs, "labels").AsMap())
	lcNoChange(t, lcDiff(t, s, typ, id, read.Properties, in), "no-op diff after refresh")

	// Update: every updatable property reaches the API; the immutable two replace.
	in2 := lcWith(in, map[string]any{"visibleName": "Acme 2", "labels": map[string]any{"team": "b"},
		"policies": []any{pol, map[string]any{"id": "p2", "policy": "cap", "value": "1"}}})
	d := lcDiff(t, s, typ, id, created.Properties, in2)
	for _, k := range []string{"visibleName", "labels", "policies"} {
		lcKind(t, typ, d, k, pgp.Update)
	}
	upd := lcUpdate(t, s, typ, id, created.Properties, in, in2)
	req := lastReq[*iamv1.UpdateTenantRequest](f, "UpdateTenant")
	assert.Equal(t, "Acme 2", req.GetVisibleName())
	assert.Equal(t, map[string]string{"team": "b"}, labelsFromProto(req.GetLabels()))
	assert.Len(t, req.GetPolicies(), 2)
	assert.Equal(t, "Acme 2", str(upd.Properties, "visibleName"))
	for k, v := range map[string]any{"externalAccountId": "ext-2", "creatorId": "you"} {
		lcKind(t, typ, lcDiff(t, s, typ, id, created.Properties, lcWith(in, map[string]any{k: v})), k, pgp.UpdateReplace)
	}

	// Drift of the visible name on refresh.
	f.with(func() { f.tenants[0].VisibleName = "Renamed" })
	drift := lcRead(t, s, typ, id, upd.Properties, in2)
	assert.Equal(t, "Renamed", str(drift.Inputs, "visibleName"))
	lcKind(t, typ, lcDiff(t, s, typ, id, drift.Properties, in2), "visibleName", pgp.Update)

	// Declared without labels: server labels never become inputs or a diff,
	// on both Diff paths.
	plain := pm(map[string]any{"visibleName": "Plain"})
	pc := lcCreate(t, s, typ, plain)
	pr := lcRead(t, s, typ, pc.ID, pc.Properties, plain)
	assert.False(t, lcHas(pr.Inputs, "labels"), "%v", pr.Inputs)
	lcNoChange(t, lcDiff(t, s, typ, pc.ID, pr.Properties, plain), "refresh without declared labels")
	eng := lcEngine(t, f, "eu")
	lcRequireEngineNoChange(t, lcEngineDiff(t, eng, typ, pc.ID, pr.Properties, pr.Inputs, plain), "engine path after refresh")

	// Import by ID alone walks the paginated list (page size 2, three tenants).
	third := lcCreate(t, s, typ, pm(map[string]any{"visibleName": "Third", "externalAccountId": "ext-3"}))
	imp := lcImport(t, s, typ, third.ID)
	assert.Equal(t, "Third", str(imp.Inputs, "visibleName"))
	assert.Equal(t, "ext-3", str(imp.Inputs, "externalAccountId"))
	lcNoChange(t, lcDiff(t, s, typ, third.ID, imp.Properties, imp.Inputs), "no-op diff after import")
	assert.GreaterOrEqual(t, f.callCount("ListTenants"), 2)

	// Deleted outside Pulumi; destroy is quiet and idempotent.
	f.with(func() { f.tenants = f.tenants[1:] })
	lcGone(t, s, typ, id, created.Properties, in)
	lcDelete(t, s, typ, id, created.Properties)
	lcDelete(t, s, typ, pc.ID, pc.Properties)
	lcDelete(t, s, typ, pc.ID, pc.Properties)
}

// ---------------------------------------------------------------------------
// Instance
// ---------------------------------------------------------------------------

func fastInstancePolling(t *testing.T) {
	t.Helper()
	prev := instancePollInterval
	instancePollInterval = time.Millisecond
	t.Cleanup(func() { instancePollInterval = prev })
}

func lcInstanceInputs() property.Map {
	return pm(map[string]any{
		"shape":             map[string]any{"virtualCpu": 2, "memoryMegabytes": 4096},
		"documentedPurpose": "ci",
		"deadline":          "2026-11-01T14:00:00Z",
		"labels":            map[string]any{"team": "a"},
	})
}

func TestLifecycle_Instance(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	f.bootDescribes = 2
	s := lcServer(t, f, "eu")
	const typ = "Instance"
	in := lcInstanceInputs()

	created := lcCreate(t, s, typ, in)
	id := created.ID
	assert.Equal(t, "RUNNING", str(created.Properties, "status"), "create waits for the instance")
	assert.Equal(t, "eu", str(created.Properties, "effectiveRegion"))
	assert.False(t, lcHas(created.Properties, "region"), "the declared region input is left as given (unset)")
	f.with(func() { assert.Equal(t, []string{"eu"}, f.regions["CreateInstance"]) })
	lcNoChange(t, lcDiff(t, s, typ, id, created.Properties, in), "no-op diff after create")

	// Refresh: server defaults (machine arch, OS) and undeclared labels must not
	// leak into the inputs, on both Diff paths.
	read := lcRead(t, s, typ, id, created.Properties, in)
	assert.False(t, lcHas(lcMapOf(read.Inputs, "shape"), "machineArch"), "server default must not overwrite the input")
	assert.Equal(t, map[string]property.Value{"team": property.New("a")}, lcMapOf(read.Inputs, "labels").AsMap())
	assert.Equal(t, "2026-11-01T14:00:00Z", str(read.Inputs, "deadline"))
	lcNoChange(t, lcDiff(t, s, typ, id, read.Properties, in), "no-op diff after refresh")
	eng := lcEngine(t, f, "eu")
	lcRequireEngineNoChange(t, lcEngineDiff(t, eng, typ, id, read.Properties, read.Inputs, in), "engine path after refresh")
	bare := pm(map[string]any{"shape": map[string]any{"virtualCpu": 1, "memoryMegabytes": 1024}})
	bc := lcCreate(t, s, typ, bare)
	br := lcRead(t, s, typ, bc.ID, bc.Properties, bare)
	assert.False(t, lcHas(br.Inputs, "labels") || lcHas(br.Inputs, "deadline") || lcHas(br.Inputs, "documentedPurpose"), "%v", br.Inputs)
	lcNoChange(t, lcDiff(t, s, typ, bc.ID, br.Properties, bare), "refresh of an instance declared with defaults")
	lcRequireEngineNoChange(t, lcEngineDiff(t, eng, typ, bc.ID, br.Properties, br.Inputs, bare), "engine path, defaults only")

	// Immutable inputs replace; the deadline updates in place.
	for k, v := range map[string]any{
		"shape":             map[string]any{"virtualCpu": 4, "memoryMegabytes": 4096},
		"documentedPurpose": "other", "labels": map[string]any{"team": "b"}, "interactive": true, "region": "us",
	} {
		lcKind(t, typ, lcDiff(t, s, typ, id, created.Properties, lcWith(in, map[string]any{k: v})), k, pgp.UpdateReplace)
	}
	in2 := lcWith(in, map[string]any{"deadline": "2026-11-01T16:00:00Z"})
	lcKind(t, typ, lcDiff(t, s, typ, id, created.Properties, in2), "deadline", pgp.Update)
	upd := lcUpdate(t, s, typ, id, created.Properties, in, in2)
	assert.Equal(t, "2026-11-01T16:00:00Z", str(upd.Properties, "expiresAt"))
	ext := lastReq[*computev1.ExtendInstanceRequest](f, "ExtendInstance")
	assert.Equal(t, "2026-11-01T16:00:00Z", formatTimestamp(ext.GetNewDeadline()))
	// Shortening is refused.
	_, err := s.Update(pgp.UpdateRequest{ID: id, Urn: urn(typ, "r"), State: upd.Properties, OldInputs: in2,
		Inputs: lcWith(in, map[string]any{"deadline": "2026-11-01T13:00:00Z"})})
	require.Error(t, err)

	// Drift: shape, purpose and deadline changed on the server show on refresh.
	f.with(func() {
		md := f.instances[id].res.Metadata
		md.Shape.VirtualCpu = 8
		md.DocumentedPurpose = "changed"
		md.Deadline = parseTS(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
	})
	drift := lcRead(t, s, typ, id, upd.Properties, in2)
	assert.Equal(t, 8, int(drift.Inputs.Get("shape").AsMap().Get("virtualCpu").AsNumber()))
	assert.Equal(t, "changed", str(drift.Inputs, "documentedPurpose"))
	assert.Equal(t, "2026-11-02T00:00:00Z", str(drift.Inputs, "deadline"))
	lcKind(t, typ, lcDiff(t, s, typ, id, drift.Properties, in2), "shape", pgp.UpdateReplace)

	// Import by ID alone (an instance without containers): region is pinned in
	// effectiveRegion, not invented as an input; a re-run plans nothing.
	imp := lcImport(t, s, typ, bc.ID)
	assert.False(t, lcHas(imp.Inputs, "region"))
	assert.Equal(t, "eu", str(imp.Properties, "effectiveRegion"))
	lcNoChange(t, lcDiff(t, s, typ, bc.ID, imp.Properties, imp.Inputs), "no-op diff after import")

	// Deleted outside Pulumi (destroyed by the platform, or gone): Read drops it
	// and Delete is quiet.
	lcDelete(t, s, typ, bc.ID, bc.Properties)
	lcGone(t, s, typ, bc.ID, bc.Properties, bare)
	f.with(func() { delete(f.instances, id) })
	lcGone(t, s, typ, id, upd.Properties, in2)
	lcDelete(t, s, typ, id, upd.Properties)
	lcDelete(t, s, typ, id, upd.Properties)
}

// The instance must stay reachable when the provider region changes after it
// was created: Read and Delete use the recorded region.
func TestLifecycle_InstanceRegionIsPinnedAtCreate(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	eu := lcServer(t, f, "eu")
	const typ = "Instance"
	in := lcInstanceInputs()
	created := lcCreate(t, eu, typ, in)
	id := created.ID

	us := lcServer(t, f, "us") // same program, provider region changed
	read := lcRead(t, us, typ, id, created.Properties, in)
	require.Equal(t, id, read.ID, "an instance in another region must not be reported as gone")
	assert.Equal(t, "eu", str(read.Properties, "effectiveRegion"))
	f.with(func() { assert.Equal(t, "eu", f.regions["DescribeInstance"][len(f.regions["DescribeInstance"])-1]) })

	lcUpdate(t, us, typ, id, read.Properties, in, lcWith(in, map[string]any{"deadline": "2026-11-01T18:00:00Z"}))
	f.with(func() { assert.Equal(t, "eu", f.regions["ExtendInstance"][0]) })

	lcDelete(t, us, typ, id, read.Properties)
	f.with(func() {
		assert.Equal(t, []string{"eu"}, f.regions["DestroyInstance"], "destroy must target the creation region")
		assert.Equal(t, computev1.InstanceMetadata_DESTROYED, f.instances[id].res.Metadata.Status, "the instance is really destroyed")
	})

	// State written before effectiveRegion existed falls back to the declared
	// resource region.
	legacyIn := lcWith(in, map[string]any{"region": "us"})
	lc := lcCreate(t, us, typ, legacyIn)
	legacyState := lcWith(lc.Properties, map[string]any{"effectiveRegion": nil})
	lr := lcRead(t, eu, typ, lc.ID, legacyState, legacyIn)
	require.Equal(t, lc.ID, lr.ID)
	f.with(func() { assert.Equal(t, "us", f.regions["DescribeInstance"][len(f.regions["DescribeInstance"])-1]) })
}

// A failure after the instance exists must keep it tracked.
func TestLifecycle_InstanceCreateFailureAfterCreateKeepsID(t *testing.T) {
	fastInstancePolling(t)
	f := newNSFake(t)
	f.bootDescribes = 3
	f.failWith("DescribeInstance", &rpcErr{status: 500, code: "internal", msg: "boom"})
	s := lcServer(t, f, "eu")
	r, err := s.Create(pgp.CreateRequest{Urn: urn("Instance", "r"), Properties: lcInstanceInputs()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize")
	assert.NotEmpty(t, r.ID, "the created instance must be tracked so it can be retried or destroyed")
	assert.Equal(t, "eu", str(r.Properties, "effectiveRegion"))
}

// ---------------------------------------------------------------------------
// RevokableToken
// ---------------------------------------------------------------------------

func TestLifecycle_RevokableToken(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "RevokableToken"
	grant := map[string]any{"resourceType": "instance", "resourceId": "", "actions": []any{"list"}}
	in := pm(map[string]any{"name": "ci", "description": "d", "grants": []any{grant}})

	created := lcCreate(t, s, typ, in)
	id := created.ID
	assert.True(t, lcSchema(t).Resources["namespace:index:RevokableToken"].Properties["bearerToken"].Secret, "the bearer token is a secret output")
	assert.Equal(t, "nsct_"+id, created.Properties.Get("bearerToken").AsString())
	lcNoChange(t, lcDiff(t, s, typ, id, created.Properties, in), "no-op diff after create")

	// Refresh maps name, description and grants from the API, keeps the secret,
	// and leaves the server-defaulted scope unset.
	read := lcRead(t, s, typ, id, created.Properties, in)
	assert.Equal(t, "ci", str(read.Inputs, "name"))
	assert.Equal(t, "d", str(read.Inputs, "description"))
	assert.False(t, lcHas(read.Inputs, "scope"), "the server default scope must not become an input")
	assert.Equal(t, "nsct_"+id, read.Properties.Get("bearerToken").AsString())
	lcNoChange(t, lcDiff(t, s, typ, id, read.Properties, in), "no-op diff after refresh")

	// Declared scope round-trips.
	scoped := lcWith(in, map[string]any{"name": "scoped", "scope": "TENANT_MEMBERSHIP_SCOPE"})
	sc := lcCreate(t, s, typ, scoped)
	sr := lcRead(t, s, typ, sc.ID, sc.Properties, scoped)
	assert.Equal(t, "TENANT_MEMBERSHIP_SCOPE", str(sr.Inputs, "scope"))
	lcNoChange(t, lcDiff(t, s, typ, sc.ID, sr.Properties, scoped), "no-op diff with a declared scope")

	// Drift in description, scope and grants shows on refresh.
	f.with(func() {
		for _, tk := range f.tokens {
			if tk.tok.GetTokenId() == sc.ID {
				tk.tok.Description = "changed"
				tk.tok.Scope = iamv1.RevokableToken_TENANT_SCOPE
				tk.tok.Permissions = nil
			}
		}
	})
	drift := lcRead(t, s, typ, sc.ID, sc.Properties, scoped)
	assert.Equal(t, "changed", str(drift.Inputs, "description"))
	assert.Equal(t, "TENANT_SCOPE", str(drift.Inputs, "scope"))
	assert.Equal(t, 0, drift.Inputs.Get("grants").AsArray().Len())
	lcKind(t, typ, lcDiff(t, s, typ, sc.ID, drift.Properties, scoped), "description", pgp.UpdateReplace)

	// Every identity property replaces; only the refresh trigger updates in place.
	for k, v := range map[string]any{
		"name": "other", "description": "x", "expiresAt": "2027-01-01T00:00:00Z", "scope": "TENANT_SCOPE",
		"policies": []any{map[string]any{"policyType": "p", "value": "v"}},
		"grants":   []any{map[string]any{"resourceType": "vault", "resourceId": "", "actions": []any{"get"}}},
	} {
		lcKind(t, typ, lcDiff(t, s, typ, id, created.Properties, lcWith(in, map[string]any{k: v})), k, pgp.UpdateReplace)
	}
	in2 := lcWith(in, map[string]any{"refreshTrigger": "1", "refreshMinimumSeconds": 600})
	lcKind(t, typ, lcDiff(t, s, typ, id, created.Properties, in2), "refreshTrigger", pgp.Update)
	var before time.Time
	f.with(func() { before = f.tokens[0].tok.GetExpiresAt().AsTime() })
	upd := lcUpdate(t, s, typ, id, created.Properties, in, in2)
	rq := lastReq[*iamv1.RefreshRevokableTokenRequest](f, "RefreshRevokableToken")
	assert.Equal(t, id, rq.GetTokenId())
	assert.Equal(t, 600*time.Second, rq.GetMinimumDuration().AsDuration())
	assert.Equal(t, before.Add(600*time.Second).UTC().Format(time.RFC3339), str(upd.Properties, "tokenExpiresAt"))

	// Import is refused: the bearer token cannot be recovered.
	_, err := s.Read(pgp.ReadRequest{ID: id, Urn: urn(typ, "r"), Properties: property.NewMap(nil), Inputs: property.NewMap(nil)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "import")

	// Expired and revoked tokens read as gone so they are recreated; destroy stays quiet.
	f.with(func() { f.tokens[0].tok.State = iamv1.RevokableToken_EXPIRED })
	lcGone(t, s, typ, id, upd.Properties, in2)
	lcDelete(t, s, typ, id, upd.Properties)
	f.with(func() { assert.Equal(t, iamv1.RevokableToken_REVOKED, f.tokens[0].tok.State) })
	lcGone(t, s, typ, id, upd.Properties, in2) // revoked now
	lcDelete(t, s, typ, id, upd.Properties)

	// Deleted outside Pulumi: gone, and destroy (404) is quiet, twice.
	f.with(func() { f.tokens = nil })
	lcGone(t, s, typ, sc.ID, sc.Properties, scoped)
	lcDelete(t, s, typ, sc.ID, sc.Properties)
	lcDelete(t, s, typ, sc.ID, sc.Properties)
}

// ---------------------------------------------------------------------------
// TenantGroup
// ---------------------------------------------------------------------------

func TestLifecycle_TenantGroup(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "TenantGroup"
	in := pm(map[string]any{"name": "gold", "policies": []any{map[string]any{"policy": "quota", "value": "10"}}})

	created := lcCreate(t, s, typ, in)
	assert.Equal(t, "gold", created.ID)
	lcNoChange(t, lcDiff(t, s, typ, "gold", created.Properties, in), "no-op diff after create")

	// Refresh reports enrolled tenants and policy drift.
	f.with(func() {
		f.groups["gold"].EnrolledTenantIds = []string{"t1"}
		f.groups["gold"].Policies = append(f.groups["gold"].Policies, &iamv1.TenantGroupPolicy{Policy: "extra", Value: "1"})
	})
	read := lcRead(t, s, typ, "gold", created.Properties, in)
	assert.Equal(t, 1, read.Properties.Get("enrolledTenantIds").AsArray().Len())
	assert.Equal(t, 2, read.Inputs.Get("policies").AsArray().Len())
	lcKind(t, typ, lcDiff(t, s, typ, "gold", read.Properties, in), "policies", pgp.Update)

	// Update reaches the API; the name replaces.
	in2 := lcWith(in, map[string]any{"policies": []any{map[string]any{"policy": "quota", "value": "20"}}})
	upd := lcUpdate(t, s, typ, "gold", read.Properties, in, in2)
	ens := lastReq[*iamv1.EnsureTenantGroupRequest](f, "EnsureTenantGroup")
	require.Len(t, ens.GetPolicies(), 1)
	assert.Equal(t, "20", ens.GetPolicies()[0].GetValue())
	assert.Equal(t, 1, upd.Properties.Get("policies").AsArray().Len())
	lcKind(t, typ, lcDiff(t, s, typ, "gold", upd.Properties, lcWith(in2, map[string]any{"name": "silver"})), "name", pgp.UpdateReplace)

	// Import by name, then no change.
	imp := lcImport(t, s, typ, "gold")
	lcNoChange(t, lcDiff(t, s, typ, "gold", imp.Properties, imp.Inputs), "no-op diff after import")

	// Deleted outside Pulumi; destroy is quiet and idempotent.
	f.with(func() { delete(f.groups, "gold") })
	lcGone(t, s, typ, "gold", upd.Properties, in2)
	lcDelete(t, s, typ, "gold", upd.Properties)
	lcDelete(t, s, typ, "gold", upd.Properties)
}

// The group exists once EnsureTenantGroup succeeded: a failing read-back must
// leave it tracked.
func TestLifecycle_TenantGroupFollowUpFailureKeepsID(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	in := pm(map[string]any{"name": "gold"})
	f.failWith("DescribeTenantGroup", &rpcErr{status: 500, code: "internal", msg: "boom"})
	r, err := s.Create(pgp.CreateRequest{Urn: urn("TenantGroup", "r"), Properties: in})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize")
	assert.Equal(t, "gold", r.ID)
	f.with(func() { assert.Contains(t, f.groups, "gold", "the group was created") })

	f.failWith("DescribeTenantGroup", nil)
	state := lcCreate(t, s, "TenantGroup", lcWith(in, map[string]any{"name": "silver"}))
	f.failWith("DescribeTenantGroup", &rpcErr{status: 500, code: "internal", msg: "boom"})
	in2 := lcWith(pm(map[string]any{"name": "silver"}), map[string]any{"policies": []any{map[string]any{"policy": "q", "value": "1"}}})
	ur, err := s.Update(pgp.UpdateRequest{ID: "silver", Urn: urn("TenantGroup", "r"), State: state.Properties, OldInputs: pm(map[string]any{"name": "silver"}), Inputs: in2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize")
	assert.Equal(t, 1, ur.Properties.Get("policies").AsArray().Len(), "the applied policies are recorded")
}

// ---------------------------------------------------------------------------
// GithubProfile
// ---------------------------------------------------------------------------

func TestLifecycle_GithubProfile(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "GithubProfile"
	in := lcWith(profileInputs("d"), map[string]any{
		"cacheVolumeSettings":   []any{map[string]any{"volumeSizeGb": 10, "volumeTag": "legacy"}},
		"customRunnerImageSpec": map[string]any{"dockerSpec": map[string]any{"dockerfileContent": "FROM ubuntu"}},
		"rebuildTrigger":        "a",
	})

	created := lcCreate(t, s, typ, in)
	id := created.ID
	assert.Equal(t, 1, int(created.Properties.Get("version").AsNumber()))
	lcNoChange(t, lcDiff(t, s, typ, id, created.Properties, in), "no-op diff after create")
	read := lcRead(t, s, typ, id, created.Properties, in)
	lcNoChange(t, lcDiff(t, s, typ, id, read.Properties, in), "no-op diff after refresh")
	eng := lcEngine(t, f, "eu")
	lcRequireEngineNoChange(t, lcEngineDiff(t, eng, typ, id, read.Properties, read.Inputs, in), "engine path after refresh")

	// Update: every updatable property reaches the API, guarded by the version.
	in2 := lcWith(in, map[string]any{
		"description":          "d2",
		"os":                   "linux",
		"instanceShape":        map[string]any{"virtualCpu": 16, "memoryMegabytes": 32768, "os": "linux", "machineArch": "amd64"},
		"cacheVolumeSettings":  []any{map[string]any{"volumeSizeGb": 20, "volumeTag": "legacy"}},
		"builderMode":          "NO_CACHING",
		"experimentalFeatures": []any{"x"},
		"emoji":                "rocket",
		"accessLevel":          "ACCESS_LEVEL_LIMITED",
		"swapSizeMb":           512,
		"networkPolicy":        map[string]any{"egress": "EGRESS_POLICY_NONE"},
	})
	d := lcDiff(t, s, typ, id, created.Properties, in2)
	for _, k := range []string{"description", "os", "instanceShape", "cacheVolumeSettings", "builderMode", "experimentalFeatures",
		"emoji", "accessLevel", "swapSizeMb", "networkPolicy"} {
		lcKind(t, typ, d, k, pgp.Update)
	}
	upd := lcUpdate(t, s, typ, id, created.Properties, in, in2)
	rq := lastReq[*githubv1.UpdateProfileRequest](f, "UpdateProfile")
	assert.Equal(t, int64(1), rq.GetUpdateVersion())
	sp := rq.GetSpec()
	assert.Equal(t, "d2", sp.GetDescription())
	assert.Equal(t, int32(16), sp.GetInstanceShape().GetVirtualCpu())
	assert.Equal(t, int32(512), sp.GetSwapSizeMb())
	assert.Equal(t, "rocket", sp.GetEmoji())
	assert.Equal(t, 2, int(upd.Properties.Get("version").AsNumber()))
	lcNoChange(t, lcDiff(t, s, typ, id, upd.Properties, in2), "no-op diff after update")
	lcKind(t, typ, lcDiff(t, s, typ, id, upd.Properties, lcWith(in2, map[string]any{"tag": "slow"})), "tag", pgp.UpdateReplace)

	// rebuildTrigger rebuilds the image in the same update.
	in3 := lcWith(in2, map[string]any{"rebuildTrigger": "b"})
	upd3 := lcUpdate(t, s, typ, id, upd.Properties, in2, in3)
	f.with(func() { assert.Equal(t, 1, f.rebuilds[id]) })

	// A failing rebuild comes after UpdateProfile succeeded: the new version is
	// recorded, but the old trigger stays in state so the next up retries.
	f.failWith("RebuildProfileImage", &rpcErr{status: 500, code: "internal", msg: "boom"})
	in4 := lcWith(in3, map[string]any{"rebuildTrigger": "c", "description": "d3"})
	ur, err := s.Update(pgp.UpdateRequest{ID: id, Urn: urn(typ, "r"), State: upd3.Properties, OldInputs: in3, Inputs: in4})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize")
	assert.Equal(t, 4, int(ur.Properties.Get("version").AsNumber()), "the updated version is recorded")
	assert.Equal(t, "d3", str(ur.Properties, "description"))
	assert.Equal(t, "b", str(ur.Properties, "rebuildTrigger"))
	lcKind(t, typ, lcDiff(t, s, typ, id, ur.Properties, in4), "rebuildTrigger", pgp.Update)
	f.failWith("RebuildProfileImage", nil)
	ur2 := lcUpdate(t, s, typ, id, ur.Properties, in3, in4)
	f.with(func() { assert.Equal(t, 2, f.rebuilds[id]) })
	assert.Equal(t, "c", str(ur2.Properties, "rebuildTrigger"))

	// Drift on refresh.
	f.with(func() { f.profiles[0].Spec.Description = "changed" })
	drift := lcRead(t, s, typ, id, ur2.Properties, in4)
	assert.Equal(t, "changed", str(drift.Inputs, "description"))
	lcKind(t, typ, lcDiff(t, s, typ, id, drift.Properties, in4), "description", pgp.Update)

	// Import by ID alone, then no change.
	imp := lcImport(t, s, typ, id)
	assert.Equal(t, "fast", str(imp.Inputs, "tag"))
	lcNoChange(t, lcDiff(t, s, typ, id, imp.Properties, imp.Inputs), "no-op diff after import")

	// Deleted outside Pulumi; destroy quiet and idempotent.
	f.with(func() { f.profiles = nil })
	lcGone(t, s, typ, id, ur2.Properties, in4)
	lcDelete(t, s, typ, id, ur2.Properties)
	lcDelete(t, s, typ, id, ur2.Properties)
}

// ---------------------------------------------------------------------------
// EgressPolicy
// ---------------------------------------------------------------------------

func lcEgressInputs() property.Map {
	return pm(map[string]any{
		"tag": "locked", "description": "d", "mode": "BLOCK", "additionalRulesets": []any{"base"},
		"rules": []any{
			map[string]any{"op": "ALLOW", "matcher": map[string]any{"matchDomains": []any{"example.com"}}},
			map[string]any{"op": "INJECT", "matcher": map[string]any{"matchDomains": []any{"api.example.com"}},
				"inject": map[string]any{"headerName": "X-Key", "value": "s3cr3t"}},
		},
	})
}

func TestLifecycle_EgressPolicy(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "EgressPolicy"
	in := lcEgressInputs()

	created := lcCreate(t, s, typ, in)
	assert.Equal(t, "locked", created.ID)
	assert.False(t, created.Properties.Get("adopted").AsBool())
	lcNoChange(t, lcDiff(t, s, typ, "locked", created.Properties, in), "no-op diff after create")

	// The server redacts the inject value; refresh keeps the declared secret.
	read := lcRead(t, s, typ, "locked", created.Properties, in)
	assert.Equal(t, "s3cr3t", read.Properties.Get("rules").AsArray().Get(1).AsMap().Get("inject").AsMap().Get("value").AsString())
	lcNoChange(t, lcDiff(t, s, typ, "locked", read.Properties, in), "no-op diff after refresh")

	// Update: every updatable property reaches the API; the tag replaces, deleting first.
	in2 := lcWith(in, map[string]any{"description": "d2", "mode": "ADVISORY", "deepPacketInspection": true,
		"additionalRulesets": []any{"base", "more"},
		"rules":              []any{map[string]any{"op": "ALLOW", "matcher": map[string]any{"matchDomains": []any{"other.com"}}}}})
	d := lcDiff(t, s, typ, "locked", created.Properties, in2)
	for _, k := range []string{"description", "mode", "deepPacketInspection", "additionalRulesets", "rules"} {
		lcKind(t, typ, d, k, pgp.Update)
	}
	upd := lcUpdate(t, s, typ, "locked", created.Properties, in, in2)
	rq := lastReq[*networkv1.UpdateEgressPolicyRequest](f, "UpdateEgressPolicy")
	assert.Equal(t, "d2", rq.GetPolicy().GetDescription())
	assert.Equal(t, networkv1.EgressPolicySpec_ADVISORY, rq.GetPolicy().GetSpec().GetMode())
	assert.Equal(t, int64(1), rq.GetMatchRevision())
	assert.Equal(t, 2, int(upd.Properties.Get("revision").AsNumber()))
	rd := lcDiff(t, s, typ, "locked", upd.Properties, lcWith(in2, map[string]any{"tag": "other"}))
	lcKind(t, typ, rd, "tag", pgp.UpdateReplace)
	assert.True(t, rd.DeleteBeforeReplace)

	// Drift on refresh.
	f.with(func() { f.egress["locked"].pol.Description = "changed" })
	drift := lcRead(t, s, typ, "locked", upd.Properties, in2)
	assert.Equal(t, "changed", str(drift.Inputs, "description"))
	lcKind(t, typ, lcDiff(t, s, typ, "locked", drift.Properties, in2), "description", pgp.Update)

	// Import by tag alone (secret values cannot be imported), then no change.
	imp := lcImport(t, s, typ, "locked")
	lcNoChange(t, lcDiff(t, s, typ, "locked", imp.Properties, imp.Inputs), "no-op diff after import")

	// An existing tag is adopted.
	f.with(func() {
		f.egress["pre"] = &nsFakeEgress{rev: 4, pol: &networkv1.EgressPolicy{Tag: "pre", Spec: &networkv1.EgressPolicySpec{Mode: networkv1.EgressPolicySpec_ALLOW_ALL}}}
	})
	ad := lcCreate(t, s, typ, lcWith(in, map[string]any{"tag": "pre"}))
	assert.True(t, ad.Properties.Get("adopted").AsBool())

	// Destroy disables the policy (the API cannot delete it); repeated destroys
	// and destroying one that vanished are quiet.
	lcDelete(t, s, typ, "locked", upd.Properties)
	f.with(func() {
		assert.Equal(t, networkv1.EgressPolicySpec_DISABLED, f.egress["locked"].pol.GetSpec().GetMode())
	})
	f.with(func() { delete(f.egress, "locked") })
	lcGone(t, s, typ, "locked", upd.Properties, in2)
	lcDelete(t, s, typ, "locked", upd.Properties)
}

// ---------------------------------------------------------------------------
// BuildkiteQueue
// ---------------------------------------------------------------------------

func TestLifecycle_BuildkiteQueue(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "BuildkiteQueue"
	f.with(func() {
		f.queues = []*buildkitev1.BuildkiteQueue{{Id: "q-1", QueueName: "ci", Cluster: "c1",
			Settings: &buildkitev1.QueueSettings{EgressPolicyTag: "old"}}}
	})
	in := pm(map[string]any{"queueName": "ci", "egressPolicyTag": "locked",
		"permissions": map[string]any{"permissionsType": "DEFAULT"}})

	// Create adopts the existing queue and records what it had.
	created := lcCreate(t, s, typ, in)
	assert.Equal(t, "q-1", created.ID)
	assert.True(t, created.Properties.Get("priorRecorded").AsBool())
	assert.Equal(t, "old", created.Properties.Get("priorSettings").AsMap().Get("egressPolicyTag").AsString())
	lcNoChange(t, lcDiff(t, s, typ, "q-1", created.Properties, in), "no-op diff after create")
	read := lcRead(t, s, typ, "q-1", created.Properties, in)
	lcNoChange(t, lcDiff(t, s, typ, "q-1", read.Properties, in), "no-op diff after refresh")

	// Update: each updatable setting reaches the API; the queue name replaces.
	in2 := lcWith(in, map[string]any{"egressPolicyTag": "tighter",
		"permissions": map[string]any{"permissionsType": "CUSTOM", "workloadPermissions": []any{
			map[string]any{"resourceType": "instance", "resourceId": "", "actions": []any{"list"}}}},
		"openTelemetrySettings": map[string]any{"otlpEndpoint": "https://otel.example.com",
			"headers": []any{map[string]any{"name": "Authorization", "value": "tok"}}}})
	d := lcDiff(t, s, typ, "q-1", created.Properties, in2)
	for _, k := range []string{"egressPolicyTag", "permissions", "openTelemetrySettings"} {
		lcKind(t, typ, d, k, pgp.Update)
	}
	upd := lcUpdate(t, s, typ, "q-1", created.Properties, in, in2)
	rq := lastReq[*buildkitev1.UpdateQueueRequest](f, "UpdateQueue")
	assert.Equal(t, "tighter", rq.GetSettings().GetEgressPolicyTag())
	assert.Equal(t, "https://otel.example.com", rq.GetSettings().GetOpenTelemetrySettings().GetOtlpEndpoint())
	assert.Len(t, rq.GetSettings().GetPermissions().GetWorkloadPermissions(), 1)
	lcKind(t, typ, lcDiff(t, s, typ, "q-1", upd.Properties, lcWith(in2, map[string]any{"queueName": "other"})), "queueName", pgp.UpdateReplace)
	lcNoChange(t, lcDiff(t, s, typ, "q-1", upd.Properties, in2), "no-op diff after update")

	// Drift on refresh.
	f.with(func() { f.queues[0].Settings.EgressPolicyTag = "changed" })
	drift := lcRead(t, s, typ, "q-1", upd.Properties, in2)
	assert.Equal(t, "changed", str(drift.Inputs, "egressPolicyTag"))
	lcKind(t, typ, lcDiff(t, s, typ, "q-1", drift.Properties, in2), "egressPolicyTag", pgp.Update)

	// Import by queue ID alone, then no change; destroying an imported queue changes nothing.
	imp := lcImport(t, s, typ, "q-1")
	assert.Equal(t, "ci", str(imp.Inputs, "queueName"))
	lcNoChange(t, lcDiff(t, s, typ, "q-1", imp.Properties, imp.Inputs), "no-op diff after import")
	before := f.callCount("UpdateQueue")
	lcDelete(t, s, typ, "q-1", imp.Properties)
	assert.Equal(t, before, f.callCount("UpdateQueue"))

	// Destroy of an adopted queue restores what it had; repeating is harmless.
	lcDelete(t, s, typ, "q-1", upd.Properties)
	f.with(func() { assert.Equal(t, "old", f.queues[0].Settings.GetEgressPolicyTag()) })
	lcDelete(t, s, typ, "q-1", upd.Properties)

	// Deleted outside Pulumi.
	f.with(func() { f.queues = nil })
	lcGone(t, s, typ, "q-1", upd.Properties, in2)
	lcDelete(t, s, typ, "q-1", upd.Properties)
}

// ---------------------------------------------------------------------------
// Registry policies
// ---------------------------------------------------------------------------

const (
	enforce   = "EXPIRATION_ENFORCEMENT_ENFORCE"
	noEnforce = "EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE"
)

func TestLifecycle_RegistryDefaultPolicy(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "RegistryDefaultPolicy"
	f.with(func() {
		f.regDefault = nsFakeRegPolicy{rev: 5, secs: 7200, enf: registryv1.ExpirationEnforcement_EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE}
	})
	in := pm(map[string]any{"defaultExpirationSeconds": 3600, "expirationEnforcement": enforce})

	created := lcCreate(t, s, typ, in)
	assert.Equal(t, "default", created.ID)
	assert.Equal(t, 7200, int(created.Properties.Get("priorExpirationSeconds").AsNumber()))
	assert.Equal(t, noEnforce, str(created.Properties, "priorExpirationEnforcement"))
	lcNoChange(t, lcDiff(t, s, typ, "default", created.Properties, in), "no-op diff after create")
	read := lcRead(t, s, typ, "default", created.Properties, in)
	lcNoChange(t, lcDiff(t, s, typ, "default", read.Properties, in), "no-op diff after refresh")

	// A program declaring only one field is not changed by the other's server value.
	one := pm(map[string]any{"defaultExpirationSeconds": 3600})
	oneRead := lcRead(t, s, typ, "default", created.Properties, one)
	assert.False(t, lcHas(oneRead.Inputs, "expirationEnforcement"), "%v", oneRead.Inputs)
	lcNoChange(t, lcDiff(t, s, typ, "default", oneRead.Properties, one), "refresh with one declared field")

	// Update reaches the API with the state's revision.
	in2 := pm(map[string]any{"defaultExpirationSeconds": 60, "expirationEnforcement": noEnforce})
	d := lcDiff(t, s, typ, "default", created.Properties, in2)
	lcKind(t, typ, d, "defaultExpirationSeconds", pgp.Update)
	lcKind(t, typ, d, "expirationEnforcement", pgp.Update)
	upd := lcUpdate(t, s, typ, "default", created.Properties, in, in2)
	rq := lastReq[*registryv1.UpdateDefaultPolicyRequest](f, "UpdateDefaultPolicy")
	assert.Equal(t, int64(60), rq.GetDefaultExpiration().GetSeconds())
	f.with(func() { assert.Equal(t, int64(60), f.regDefault.secs) })

	// Drift on refresh.
	f.with(func() { f.regDefault.secs = 120; f.regDefault.rev++ })
	drift := lcRead(t, s, typ, "default", upd.Properties, in2)
	assert.Equal(t, 120, int(drift.Inputs.Get("defaultExpirationSeconds").AsNumber()))
	lcKind(t, typ, lcDiff(t, s, typ, "default", drift.Properties, in2), "defaultExpirationSeconds", pgp.Update)

	// Import (any ID reads the singleton), then no change; destroying it changes nothing.
	imp := lcImport(t, s, typ, "default")
	lcNoChange(t, lcDiff(t, s, typ, "default", imp.Properties, imp.Inputs), "no-op diff after import")
	calls := f.callCount("UpdateDefaultPolicy")
	lcDelete(t, s, typ, "default", imp.Properties)
	assert.Equal(t, calls, f.callCount("UpdateDefaultPolicy"))

	// Destroy restores the recorded prior, repeatedly.
	lcDelete(t, s, typ, "default", drift.Properties)
	f.with(func() { assert.Equal(t, int64(7200), f.regDefault.secs) })
	lcDelete(t, s, typ, "default", drift.Properties)
}

func TestLifecycle_RegistryRepositoryPolicy(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "RegistryRepositoryPolicy"
	f.with(func() { f.repos["app"] = &nsFakeRegPolicy{rev: 2, secs: 100} })
	in := pm(map[string]any{"repository": "app", "expirationSeconds": 3600, "expirationEnforcement": enforce})

	created := lcCreate(t, s, typ, in)
	assert.Equal(t, "app", created.ID)
	assert.Equal(t, 100, int(created.Properties.Get("priorExpirationSeconds").AsNumber()))
	assert.False(t, lcHas(created.Properties, "priorExpirationEnforcement"), "an unset prior stays unset")
	lcNoChange(t, lcDiff(t, s, typ, "app", created.Properties, in), "no-op diff after create")
	read := lcRead(t, s, typ, "app", created.Properties, in)
	lcNoChange(t, lcDiff(t, s, typ, "app", read.Properties, in), "no-op diff after refresh")

	in2 := lcWith(in, map[string]any{"expirationSeconds": 60, "expirationEnforcement": noEnforce})
	d := lcDiff(t, s, typ, "app", created.Properties, in2)
	lcKind(t, typ, d, "expirationSeconds", pgp.Update)
	lcKind(t, typ, d, "expirationEnforcement", pgp.Update)
	upd := lcUpdate(t, s, typ, "app", created.Properties, in, in2)
	rq := lastReq[*registryv1.UpdateRepositoryPolicyRequest](f, "UpdateRepositoryPolicy")
	assert.Equal(t, "app", rq.GetRepository())
	assert.Equal(t, int64(60), rq.GetExpiration().GetSeconds())
	lcKind(t, typ, lcDiff(t, s, typ, "app", upd.Properties, lcWith(in2, map[string]any{"repository": "web"})), "repository", pgp.UpdateReplace)

	f.with(func() { f.repos["app"].secs = 5; f.repos["app"].rev++ })
	drift := lcRead(t, s, typ, "app", upd.Properties, in2)
	assert.Equal(t, 5, int(drift.Inputs.Get("expirationSeconds").AsNumber()))
	lcKind(t, typ, lcDiff(t, s, typ, "app", drift.Properties, in2), "expirationSeconds", pgp.Update)

	imp := lcImport(t, s, typ, "app")
	assert.Equal(t, "app", str(imp.Inputs, "repository"))
	lcNoChange(t, lcDiff(t, s, typ, "app", imp.Properties, imp.Inputs), "no-op diff after import")
	lcDelete(t, s, typ, "app", imp.Properties) // imported: nothing recorded to restore
	f.with(func() { assert.Equal(t, int64(5), f.repos["app"].secs) })

	lcDelete(t, s, typ, "app", drift.Properties)
	f.with(func() { assert.Equal(t, int64(100), f.repos["app"].secs, "destroy restores the recorded prior") })
	lcDelete(t, s, typ, "app", drift.Properties)

	f.with(func() { delete(f.repos, "app") })
	lcGone(t, s, typ, "app", upd.Properties, in2)
	lcDelete(t, s, typ, "app", upd.Properties)
}

// The policy has been overwritten once the first update succeeded: a failing
// read-back must keep the resource tracked with its prior recorded, so destroy
// can still restore it.
func TestLifecycle_RegistryPoliciesReadBackFailureKeepsPrior(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	f.with(func() {
		f.regDefault = nsFakeRegPolicy{rev: 1, secs: 7200}
		f.repos["app"] = &nsFakeRegPolicy{rev: 1, secs: 100}
	})

	f.failAfterCalls("GetDefaultPolicy", 1, &rpcErr{status: 500, code: "internal", msg: "boom"})
	r, err := s.Create(pgp.CreateRequest{Urn: urn("RegistryDefaultPolicy", "r"), Properties: pm(map[string]any{"defaultExpirationSeconds": 60})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize")
	assert.Equal(t, "default", r.ID)
	assert.True(t, r.Properties.Get("priorRecorded").AsBool())
	assert.Equal(t, 7200, int(r.Properties.Get("priorExpirationSeconds").AsNumber()))
	f.failAfterCalls("GetDefaultPolicy", 0, nil)
	lcDelete(t, s, "RegistryDefaultPolicy", "default", r.Properties)
	f.with(func() { assert.Equal(t, int64(7200), f.regDefault.secs) })

	f.failAfterCalls("GetRepositoryPolicy", 1, &rpcErr{status: 500, code: "internal", msg: "boom"})
	rr, err := s.Create(pgp.CreateRequest{Urn: urn("RegistryRepositoryPolicy", "r"), Properties: pm(map[string]any{"repository": "app", "expirationSeconds": 60})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize")
	assert.Equal(t, "app", rr.ID)
	assert.True(t, rr.Properties.Get("priorRecorded").AsBool())
	f.failAfterCalls("GetRepositoryPolicy", 0, nil)
	lcDelete(t, s, "RegistryRepositoryPolicy", "app", rr.Properties)
	f.with(func() { assert.Equal(t, int64(100), f.repos["app"].secs) })
}

// ---------------------------------------------------------------------------
// Blueprint
// ---------------------------------------------------------------------------

func TestLifecycle_Blueprint(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	const typ = "Blueprint"
	in := pm(map[string]any{"name": "base", "dockerfileContent": "FROM alpine"})

	created := lcCreate(t, s, typ, in)
	assert.Equal(t, "base", created.ID)
	assert.Equal(t, "bp-base-v1", str(created.Properties, "blueprintId"))
	lcNoChange(t, lcDiff(t, s, typ, "base", created.Properties, in), "no-op diff after create")
	read := lcRead(t, s, typ, "base", created.Properties, in)
	assert.Equal(t, "FROM alpine", str(read.Inputs, "dockerfileContent"))
	lcNoChange(t, lcDiff(t, s, typ, "base", read.Properties, in), "no-op diff after refresh")

	// A spec change updates in place through the upsert: the blueprint never
	// disappears and nothing is removed.
	in2 := pm(map[string]any{"name": "base", "aptBaseImage": "ubuntu:24.04", "aptPackages": []any{"git", "curl"}})
	d := lcDiff(t, s, typ, "base", created.Properties, in2)
	for _, k := range []string{"dockerfileContent", "aptBaseImage", "aptPackages"} {
		lcKind(t, typ, d, k, pgp.Update)
	}
	assert.False(t, d.DeleteBeforeReplace && func() bool {
		for _, v := range d.DetailedDiff {
			if v.Kind == pgp.UpdateReplace {
				return true
			}
		}
		return false
	}())
	upd := lcUpdate(t, s, typ, "base", created.Properties, in, in2)
	assert.Equal(t, "bp-base-v2", str(upd.Properties, "blueprintId"))
	assert.Equal(t, 0, f.callCount("RemoveBlueprint"), "no delete-then-create window")
	f.with(func() {
		assert.Len(t, f.blueprints, 1)
		assert.Equal(t, "ubuntu:24.04", f.blueprints["base"].GetSpec().GetAptBasedImage().GetBaseImage().GetImageRef())
		assert.Equal(t, []string{"git", "curl"}, f.blueprints["base"].GetSpec().GetAptBasedImage().GetAptPackages())
	})
	lcNoChange(t, lcDiff(t, s, typ, "base", upd.Properties, in2), "no-op diff after update")
	lcKind(t, typ, lcDiff(t, s, typ, "base", upd.Properties, lcWith(in2, map[string]any{"name": "other"})), "name", pgp.UpdateReplace)

	// Drift of the spec on refresh.
	f.with(func() { f.blueprints["base"].Spec.GetAptBasedImage().AptPackages = []string{"git"} })
	drift := lcRead(t, s, typ, "base", upd.Properties, in2)
	assert.Equal(t, 1, drift.Inputs.Get("aptPackages").AsArray().Len())
	lcKind(t, typ, lcDiff(t, s, typ, "base", drift.Properties, in2), "aptPackages", pgp.Update)

	// Import by name alone derives the spec; no change afterwards.
	imp := lcImport(t, s, typ, "base")
	assert.Equal(t, "ubuntu:24.04", str(imp.Inputs, "aptBaseImage"))
	lcNoChange(t, lcDiff(t, s, typ, "base", imp.Properties, imp.Inputs), "no-op diff after import")

	// Deleted outside Pulumi; destroy quiet and idempotent.
	f.with(func() { delete(f.blueprints, "base") })
	lcGone(t, s, typ, "base", upd.Properties, in2)
	lcDelete(t, s, typ, "base", upd.Properties)
	lcDelete(t, s, typ, "base", upd.Properties)
}

// ---------------------------------------------------------------------------
// RPC names
// ---------------------------------------------------------------------------

// Every service and method the provider calls must exist in the generated
// descriptors, so an upstream rename fails here instead of during a user's apply.
func TestRPCNamesExistInSchema(t *testing.T) {
	services := map[string]string{
		"vaultService": vaultService, "tenantService": tenantService, "computeService": computeService,
		"githubProfileService": githubProfileService, "githubJobsService": githubJobsService,
		"egressPolicyService": egressPolicyService, "buildkiteQueueService": buildkiteQueueService,
		"registryService": registryService, "tokenService": tokenService, "imageService": imageService,
		"usageService": usageService, "storageService": storageService,
	}
	re := regexp.MustCompile(`\b(\w+Service),\s*"(\w+)"`)
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	seen := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			svc, ok := services[m[1]]
			require.True(t, ok, "%s: service constant %s is not covered by this test", file, m[1])
			d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(svc))
			require.NoError(t, err, "%s: service %s is not in the generated schema", file, svc)
			meth := d.(protoreflect.ServiceDescriptor).Methods().ByName(protoreflect.Name(m[2]))
			assert.NotNil(t, meth, "%s: %s has no method %s", file, svc, m[2])
			seen++
		}
	}
	assert.GreaterOrEqual(t, seen, 40, "the scan should find the provider's RPC calls")
}
