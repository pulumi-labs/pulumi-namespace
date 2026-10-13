package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/blang/semver"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureWith(t *testing.T, args map[string]property.Value) error {
	t.Helper()
	p, err := New()
	require.NoError(t, err)
	s, err := integration.NewServer(t.Context(), "namespace", semver.MustParse("0.0.1"), integration.WithProvider(p))
	require.NoError(t, err)
	args["token"] = property.New(testToken).WithSecret(true)
	return s.Configure(pgp.ConfigureRequest{Args: property.NewMap(args)})
}

func TestConfigure_RejectsCleartextEndpoints(t *testing.T) {
	for _, key := range []string{"computeEndpoint", "iamEndpoint", "globalEndpoint"} {
		err := configureWith(t, map[string]property.Value{key: property.New("http://api.example.com")})
		require.Error(t, err, key)
		assert.Contains(t, err.Error(), "cleartext")
	}
	err := configureWith(t, map[string]property.Value{"iamEndpoint": property.New("ftp://x.example.com")})
	require.Error(t, err)
	err = configureWith(t, map[string]property.Value{"iamEndpoint": property.New("not a url")})
	require.Error(t, err)
}

func TestConfigure_AllowsHTTPSAndLoopbackHTTP(t *testing.T) {
	for _, v := range []string{"https://iam.example.com", "http://127.0.0.1:8080", "http://localhost:9", "http://[::1]:80"} {
		require.NoError(t, configureWith(t, map[string]property.Value{"iamEndpoint": property.New(v)}), v)
	}
}

func TestClient_OversizeResponseIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tenants":[],"padding":"` + strings.Repeat("x", 200) + `"}`))
	}))
	defer srv.Close()
	c := newNSClient("t", "us", endpoints{IAM: srv.URL})
	c.maxResponse = 100
	_, err := listTenants(context.Background(), c, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
	// Within the cap the same call works.
	c.maxResponse = 1 << 20
	_, err = listTenants(context.Background(), c, "", "")
	require.NoError(t, err)
}

func TestPaginate_RepeatedCursorAndPageCap(t *testing.T) {
	err := paginate("X", func(cursor string) (string, error) { return "same", nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repeated")
	n := 0
	err = paginate("Y", func(cursor []byte) ([]byte, error) { n++; return []byte{byte(n), byte(n >> 8), 1}, nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not terminate")
	var pages []string
	require.NoError(t, paginate("Z", func(c string) (string, error) {
		pages = append(pages, c)
		if c == "" {
			return "a", nil
		}
		return "", nil
	}))
	assert.Equal(t, []string{"", "a"}, pages)
}

func TestListTenants_CursorPageSendsNoFilters(t *testing.T) {
	f := newFakeAPI(t)
	f.on(tenantSvc+"ListTenants", func(b map[string]any) (int, string) {
		if b["cursor"] == nil {
			return 200, `{"tenants":[{"id":"t1"}],"cursor":"c2"}`
		}
		return 200, `{"tenants":[{"id":"t2"}]}`
	})
	c := newNSClient(testToken, "us", endpoints{IAM: f.srv.URL})
	ts, err := listTenants(context.Background(), c, "creator-1", "ext-1")
	require.NoError(t, err)
	require.Len(t, ts, 2)
	calls := f.callsTo(tenantSvc + "ListTenants")
	require.Len(t, calls, 2)
	assert.Equal(t, "creator-1", calls[0].Body["creatorId"])
	assert.Equal(t, "ext-1", calls[0].Body["externalAccountId"])
	assert.Equal(t, "c2", calls[1].Body["cursor"])
	assert.NotContains(t, calls[1].Body, "creatorId", "proto: no parameters except limit may accompany a cursor")
	assert.NotContains(t, calls[1].Body, "externalAccountId")
}

func TestBlueprint_DiffReplacesDeleteFirst(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	old := pm(map[string]any{"name": "bp", "dockerfileContent": "FROM alpine"})
	state := pm(map[string]any{"name": "bp", "dockerfileContent": "FROM alpine",
		"blueprintId": "b-1", "createdAt": "", "imageRef": "", "buildError": ""})
	diff := func(in property.Map) pgp.DiffResponse {
		resp, err := s.Diff(pgp.DiffRequest{Urn: urn("Blueprint", "bp"), ID: "bp", State: state, Inputs: in, OldInputs: old})
		require.NoError(t, err)
		return resp
	}
	same := diff(old)
	assert.False(t, same.HasChanges)

	// Same name, new content: the create would upsert the same name, so the old
	// resource must be deleted first or its delete would remove the new blueprint.
	changed := diff(pm(map[string]any{"name": "bp", "dockerfileContent": "FROM debian"}))
	assert.True(t, changed.HasChanges)
	assert.True(t, changed.DeleteBeforeReplace, "name-keyed replace must delete before create")
	assert.Equal(t, pgp.UpdateReplace, changed.DetailedDiff["dockerfileContent"].Kind)

	renamed := diff(pm(map[string]any{"name": "bp2", "dockerfileContent": "FROM alpine"}))
	assert.True(t, renamed.DeleteBeforeReplace)
	assert.Contains(t, renamed.DetailedDiff, "name")
}

func instanceStateMap(expires string) property.Map {
	in := instanceInputs(expires)
	m := map[string]property.Value{}
	in.All(func(k string, v property.Value) bool { m[k] = v; return true })
	m["instanceId"] = property.New("inst-1")
	m["createdAt"] = property.New("2026-10-01T10:00:00Z")
	m["readyAt"] = property.New("2026-10-01T10:01:00Z")
	m["expiresAt"] = property.New(expires)
	m["status"] = property.New("RUNNING")
	m["instanceUrl"] = property.New("")
	m["ingressDomain"] = property.New("inst-1.example.test")
	m["services"] = property.New(property.NewArray(nil))
	return property.NewMap(m)
}

func TestInstance_PreviewRejectsShortenedDeadlineLikeApply(t *testing.T) {
	f := newFakeAPI(t)
	s := newServerFor(t, f)
	have, want := "2030-01-02T00:00:00Z", "2030-01-01T00:00:00Z"
	for _, dry := range []bool{true, false} {
		_, err := s.Update(pgp.UpdateRequest{
			ID: "inst-1", Urn: urn("Instance", "i"), DryRun: dry,
			State: instanceStateMap(have), Inputs: instanceInputs(want),
		})
		require.Error(t, err, "dryRun=%v", dry)
		assert.Contains(t, err.Error(), "can only be extended", "dryRun=%v", dry)
	}
	assert.Empty(t, f.callsTo(computeSvc+"ExtendInstance"), "neither preview nor apply may call the API")
}

func TestInstance_ImportPopulatesDeadlineAndRefusesContainers(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(computeSvc+"DescribeInstance", instanceJSON("RUNNING", "2030-01-01T00:00:00Z"))
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "inst-1", Urn: urn("Instance", "i")})
	require.NoError(t, err)
	assert.Equal(t, "2030-01-01T00:00:00Z", str(read.Inputs, "deadline"))
	assert.Equal(t, "ci", str(read.Inputs, "documentedPurpose"))
	assert.Equal(t, "eu", str(read.Inputs, "region"))

	f.ok(computeSvc+"DescribeInstance",
		`{"metadata":{"instanceId":"inst-1","status":"RUNNING","shape":{"virtualCpu":2,"memoryMegabytes":4096}},`+
			`"containers":[{"id":"c1","name":"web"}]}`)
	_, err = s.Read(pgp.ReadRequest{ID: "inst-1", Urn: urn("Instance", "i")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "import")
}

// --- registry policies: stateful fake so restore is observable -------------

// regServer is a stateful stand-in for the registry policy RPCs. Update replaces
// the whole policy (no field mask), exactly as the proto documents, so a field
// omitted from a request is cleared.
type regServer struct {
	mu        sync.Mutex
	rev       int
	secs, enf string // "" means unset
}

func (r *regServer) get(secsKey string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := fmt.Sprintf(`{"revision":"%d"`, r.rev)
	if r.secs != "" {
		b += fmt.Sprintf(`,"%s":"%ss"`, secsKey, r.secs)
	}
	if r.enf != "" {
		b += fmt.Sprintf(`,"expirationEnforcement":"%s"`, r.enf)
	}
	return b + "}"
}

func (r *regServer) put(body map[string]any, secsKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rev++
	r.secs, r.enf = "", ""
	if v, ok := body[secsKey].(string); ok {
		r.secs = strings.TrimSuffix(v, "s")
	}
	if v, ok := body["expirationEnforcement"].(string); ok {
		r.enf = v
	}
}

func regFake(t *testing.T, kind string, srv *regServer) *fakeAPI {
	f := newFakeAPI(t)
	key, getM, putM := "defaultExpiration", "GetDefaultPolicy", "UpdateDefaultPolicy"
	if kind == "repo" {
		key, getM, putM = "expiration", "GetRepositoryPolicy", "UpdateRepositoryPolicy"
	}
	f.on(registrySvc+getM, func(map[string]any) (int, string) { return 200, srv.get(key) })
	f.on(registrySvc+putM, func(b map[string]any) (int, string) { srv.put(b, key); return 200, `{}` })
	return f
}

func TestRegistryDefaultPolicy_DestroyRestoresRecordedPrior(t *testing.T) {
	srv := &regServer{rev: 4, secs: "86400", enf: "EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE"}
	f := regFake(t, "default", srv)
	s := newServerFor(t, f)
	in := pm(map[string]any{"defaultExpirationSeconds": 3600, "expirationEnforcement": "EXPIRATION_ENFORCEMENT_ENFORCE"})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("RegistryDefaultPolicy", "d"), Properties: in})
	require.NoError(t, err)
	require.Equal(t, "3600", srv.secs, "create applied the declared policy")
	assert.Equal(t, "EXPIRATION_ENFORCEMENT_ENFORCE", srv.enf)

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "default", Urn: urn("RegistryDefaultPolicy", "d"), Properties: created.Properties}))
	up := f.callsTo(registrySvc + "UpdateDefaultPolicy")
	require.Len(t, up, 2)
	assert.Equal(t, "86400s", up[1].Body["defaultExpiration"], "restore request carries the recorded prior")
	assert.Equal(t, "EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE", up[1].Body["expirationEnforcement"])
	assert.Equal(t, testToken, strings.TrimPrefix(up[1].Auth, "Bearer "))
	assert.Equal(t, "86400", srv.secs, "server is back to the prior")
	assert.Equal(t, "EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE", srv.enf)
}

func TestRegistryDefaultPolicy_DestroyClearsFieldsThatWereUnset(t *testing.T) {
	// Server reports enforcement UNKNOWN (never set) and no expiration.
	srv := &regServer{rev: 1, enf: "EXPIRATION_ENFORCEMENT_UNKNOWN"}
	f := regFake(t, "default", srv)
	s := newServerFor(t, f)
	in := pm(map[string]any{"defaultExpirationSeconds": 3600, "expirationEnforcement": "EXPIRATION_ENFORCEMENT_ENFORCE"})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("RegistryDefaultPolicy", "d"), Properties: in})
	require.NoError(t, err)
	_, hasPriorEnf := created.Properties.GetOk("priorExpirationEnforcement")
	assert.False(t, hasPriorEnf, "UNKNOWN is recorded as unset, not as a string")
	prc, _ := created.Properties.GetOk("priorRecorded")
	assert.True(t, prc.AsBool())

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "default", Urn: urn("RegistryDefaultPolicy", "d"), Properties: created.Properties}))
	up := f.callsTo(registrySvc + "UpdateDefaultPolicy")
	require.Len(t, up, 2)
	assert.NotContains(t, up[1].Body, "defaultExpiration", "unset prior is restored by omitting the field")
	assert.NotContains(t, up[1].Body, "expirationEnforcement")
	assert.Equal(t, "", srv.secs, "declared expiration must not survive destroy")
	assert.Equal(t, "", srv.enf)
}

func TestRegistryPolicies_DestroyOfImportedResourceIsNoOp(t *testing.T) {
	srv := &regServer{rev: 1, secs: "7200"}
	f := regFake(t, "default", srv)
	s := newServerFor(t, f)
	// An imported resource has no recorded prior (priorRecorded absent).
	state := pm(map[string]any{"defaultExpirationSeconds": 7200, "revision": 1})
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "default", Urn: urn("RegistryDefaultPolicy", "d"), Properties: state}))
	assert.Empty(t, f.callsTo(registrySvc+"UpdateDefaultPolicy"))

	srv2 := &regServer{rev: 1, secs: "7200"}
	f2 := regFake(t, "repo", srv2)
	s2 := newServerFor(t, f2)
	state2 := pm(map[string]any{"repository": "acme/app", "expirationSeconds": 7200, "revision": 1})
	require.NoError(t, s2.Delete(pgp.DeleteRequest{ID: "acme/app", Urn: urn("RegistryRepositoryPolicy", "r"), Properties: state2}))
	assert.Empty(t, f2.callsTo(registrySvc+"UpdateRepositoryPolicy"))
}

func TestRegistryDefaultPolicy_UpdateKeepsUndeclaredAndUsesStateRevision(t *testing.T) {
	srv := &regServer{rev: 5, secs: "3600", enf: "EXPIRATION_ENFORCEMENT_ENFORCE"}
	f := regFake(t, "default", srv)
	s := newServerFor(t, f)
	state := pm(map[string]any{"defaultExpirationSeconds": 3600, "revision": 5, "priorRecorded": true})
	in := pm(map[string]any{"defaultExpirationSeconds": 7200})
	up, err := s.Update(pgp.UpdateRequest{ID: "default", Urn: urn("RegistryDefaultPolicy", "d"), State: state, Inputs: in})
	require.NoError(t, err)
	calls := f.callsTo(registrySvc + "UpdateDefaultPolicy")
	require.Len(t, calls, 1)
	assert.Equal(t, "7200s", calls[0].Body["defaultExpiration"])
	assert.Equal(t, "EXPIRATION_ENFORCEMENT_ENFORCE", calls[0].Body["expirationEnforcement"], "undeclared field is sent as the server has it")
	assert.Equal(t, "5", calls[0].Body["revision"])
	assert.True(t, strings.HasPrefix(calls[0].Auth, "Bearer "))
	rev, _ := up.Properties.GetOk("revision")
	assert.Equal(t, 6.0, rev.AsNumber(), "state carries the new revision")

	// Preview must not call the API.
	n := len(f.calls)
	_, err = s.Update(pgp.UpdateRequest{ID: "default", Urn: urn("RegistryDefaultPolicy", "d"), State: state, Inputs: in, DryRun: true})
	require.NoError(t, err)
	assert.Len(t, f.calls, n)
}

func TestRegistryRepositoryPolicy_RestoreUpdateAndPriorUnset(t *testing.T) {
	// Repository with no override: no expiration, enforcement UNKNOWN.
	srv := &regServer{rev: 2, enf: "EXPIRATION_ENFORCEMENT_UNKNOWN"}
	f := regFake(t, "repo", srv)
	s := newServerFor(t, f)
	in := pm(map[string]any{"repository": "acme/app", "expirationSeconds": 7200})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("RegistryRepositoryPolicy", "r"), Properties: in})
	require.NoError(t, err)
	assert.Equal(t, "7200", srv.secs)

	in2 := pm(map[string]any{"repository": "acme/app", "expirationSeconds": 14400})
	up, err := s.Update(pgp.UpdateRequest{ID: "acme/app", Urn: urn("RegistryRepositoryPolicy", "r"), State: created.Properties, Inputs: in2})
	require.NoError(t, err)
	calls := f.callsTo(registrySvc + "UpdateRepositoryPolicy")
	require.Len(t, calls, 2)
	assert.Equal(t, "14400s", calls[1].Body["expiration"])
	assert.Equal(t, "acme/app", calls[1].Body["repository"])
	assert.Equal(t, "3", calls[1].Body["revision"], "revision from the state recorded after create")
	assert.Equal(t, "14400", srv.secs)

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "acme/app", Urn: urn("RegistryRepositoryPolicy", "r"), Properties: up.Properties}))
	calls = f.callsTo(registrySvc + "UpdateRepositoryPolicy")
	require.Len(t, calls, 3)
	assert.NotContains(t, calls[2].Body, "expiration", "restoring an unset override omits it (default policy applies)")
	assert.Equal(t, "", srv.secs)
}

func TestEnforcementPtr_UnknownIsNil(t *testing.T) {
	assert.Nil(t, enforcementPtr(0))
	p := enforcementPtr(1)
	require.NotNil(t, p)
	assert.Equal(t, "EXPIRATION_ENFORCEMENT_ENFORCE", *p)
}

func TestRegistryPolicies_CheckValidation(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	bad := checkFails(t, s, "RegistryDefaultPolicy", map[string]any{"defaultExpirationSeconds": 0})
	assert.NotEmpty(t, bad)
	assert.NotEmpty(t, checkFails(t, s, "RegistryDefaultPolicy", map[string]any{}), "needs at least one input")
	assert.NotEmpty(t, checkFails(t, s, "RegistryDefaultPolicy", map[string]any{"expirationEnforcement": "NOPE"}))
	assert.Empty(t, checkFails(t, s, "RegistryDefaultPolicy", map[string]any{"defaultExpirationSeconds": 3600}))
	assert.NotEmpty(t, checkFails(t, s, "RegistryRepositoryPolicy", map[string]any{"expirationSeconds": 3600}), "repository required")
	assert.NotEmpty(t, checkFails(t, s, "RegistryRepositoryPolicy", map[string]any{"repository": "a", "expirationSeconds": -1}))
	assert.Empty(t, checkFails(t, s, "RegistryRepositoryPolicy", map[string]any{"repository": "a", "expirationSeconds": 3600}))
}

// bkServer is a stateful fake of one Buildkite queue whose UpdateQueue fully
// replaces the settings, as the API documents.
type bkServer struct {
	mu       sync.Mutex
	settings map[string]any
}

func (b *bkServer) queue() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, _ := json.Marshal(b.settings)
	return `{"id":"q-1","queueName":"default","cluster":"c1","organization":{"orgSlug":"acme"},"settings":` + string(raw) + `}`
}

func bkFake(t *testing.T, srv *bkServer) *fakeAPI {
	f := newFakeAPI(t)
	f.on(bkSvc+"ListQueues", func(map[string]any) (int, string) { return 200, `{"queues":[` + srv.queue() + `]}` })
	f.on(bkSvc+"GetQueue", func(map[string]any) (int, string) { return 200, `{"queue":` + srv.queue() + `}` })
	f.on(bkSvc+"UpdateQueue", func(b map[string]any) (int, string) {
		srv.mu.Lock()
		s, _ := b["settings"].(map[string]any)
		if s == nil {
			s = map[string]any{}
		}
		srv.settings = s
		srv.mu.Unlock()
		return 200, `{"queue":` + srv.queue() + `}`
	})
	return f
}

func TestBuildkiteQueue_AdoptKeepsUndeclaredSettingsAndRestoresPrior(t *testing.T) {
	srv := &bkServer{settings: map[string]any{
		"egressPolicyTag": "locked",
		"permissions":     map[string]any{"permissionsType": "DEFAULT"},
	}}
	f := bkFake(t, srv)
	s := newServerFor(t, f)
	// Declares only permissions: the queue's egress enforcement must survive.
	in := pm(map[string]any{"queueName": "default", "permissions": map[string]any{"permissionsType": "DEFAULT"}})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("BuildkiteQueue", "q"), Properties: in})
	require.NoError(t, err)
	up := f.callsTo(bkSvc + "UpdateQueue")
	require.Len(t, up, 1)
	sent := up[0].Body["settings"].(map[string]any)
	assert.Equal(t, "locked", sent["egressPolicyTag"], "undeclared setting is sent back unchanged")

	// Declare an egress tag, then destroy: the prior ("locked") comes back.
	in2 := pm(map[string]any{"queueName": "default", "egressPolicyTag": "open"})
	upd, err := s.Update(pgp.UpdateRequest{ID: "q-1", Urn: urn("BuildkiteQueue", "q"), State: created.Properties, Inputs: in2})
	require.NoError(t, err)
	assert.Equal(t, testToken[:0]+"open", str(upd.Properties, "egressPolicyTag"))
	assert.Equal(t, "open", srv.settings["egressPolicyTag"])
	assert.NotNil(t, srv.settings["permissions"], "update keeps undeclared permissions")
	assert.Equal(t, "Bearer "+testToken, f.callsTo(bkSvc + "UpdateQueue")[1].Auth)

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "q-1", Urn: urn("BuildkiteQueue", "q"), Properties: upd.Properties}))
	assert.Equal(t, "locked", srv.settings["egressPolicyTag"], "destroy restores the adopted queue's prior settings")
}

func TestBuildkiteQueue_DeleteWithoutRecordedPriorDoesNothing(t *testing.T) {
	srv := &bkServer{settings: map[string]any{"egressPolicyTag": "locked"}}
	f := bkFake(t, srv)
	s := newServerFor(t, f)
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "q-1", Urn: urn("BuildkiteQueue", "q"),
		Properties: pm(map[string]any{"queueName": "default", "queueId": "q-1", "cluster": "c1", "organizationSlug": "acme"})}))
	assert.Empty(t, f.callsTo(bkSvc+"UpdateQueue"))
}

func TestBuildkiteQueue_CheckValidation(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	assert.NotEmpty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": ""}))
	assert.NotEmpty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": "q",
		"permissions": map[string]any{"permissionsType": "BOGUS"}}))
	assert.NotEmpty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": "q",
		"permissions": map[string]any{"permissionsType": "DEFAULT", "workloadPermissions": []any{
			map[string]any{"resourceType": "r", "resourceId": "i", "actions": []any{"a"}}}}}))
	assert.NotEmpty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": "q",
		"openTelemetrySettings": map[string]any{"otlpEndpoint": ""}}))
	assert.Empty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": "q", "egressPolicyTag": "t"}))
}

func TestTenantGroup_CheckAndUpdate(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(tenantSvc+"EnsureTenantGroup", `{"name":"ci"}`)
	f.ok(tenantSvc+"DescribeTenantGroup", `{"name":"ci","policies":[{"policy":"max-instances","value":"9"}],"enrolledTenantIds":["t1"]}`)
	s := newServerFor(t, f)
	assert.NotEmpty(t, checkFails(t, s, "TenantGroup", map[string]any{"name": ""}))
	assert.NotEmpty(t, checkFails(t, s, "TenantGroup", map[string]any{"name": "ci",
		"policies": []any{map[string]any{"policy": "", "value": "1"}}}))
	assert.Empty(t, checkFails(t, s, "TenantGroup", map[string]any{"name": "ci"}))

	in := pm(map[string]any{"name": "ci", "policies": []any{map[string]any{"policy": "max-instances", "value": "9"}}})
	up, err := s.Update(pgp.UpdateRequest{ID: "ci", Urn: urn("TenantGroup", "g"), Inputs: in,
		State: pm(map[string]any{"name": "ci", "enrolledTenantIds": []any{}})})
	require.NoError(t, err)
	calls := f.callsTo(tenantSvc + "EnsureTenantGroup")
	require.Len(t, calls, 1)
	assert.Equal(t, "Bearer "+testToken, calls[0].Auth)
	assert.Equal(t, "ci", calls[0].Body["name"])
	assert.Contains(t, calls[0].Body, "policies")
	assert.Equal(t, "t1", up.Properties.Get("enrolledTenantIds").AsArray().Get(0).AsString())
}

func TestEgressPolicy_ReadCarriesDeclaredSecretAndCheckErrors(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(egressSvc+"GetEgressPolicy", `{"revision":"3","policy":{"tag":"locked","spec":{"mode":"BLOCK","rules":[{"op":"INJECT","inject":{"headerName":"X-Key"}}]}}}`)
	s := newServerFor(t, f)
	in := pm(map[string]any{"tag": "locked", "mode": "BLOCK", "rules": []any{map[string]any{"op": "INJECT",
		"inject": map[string]any{"headerName": "X-Key", "value": "s3cr3t"}}}})
	read, err := s.Read(pgp.ReadRequest{ID: "locked", Urn: urn("EgressPolicy", "e"), Inputs: in, Properties: in})
	require.NoError(t, err)
	rules := read.Properties.Get("rules").AsArray()
	assert.Equal(t, "s3cr3t", rules.Get(0).AsMap().Get("inject").AsMap().Get("value").AsString(),
		"server does not echo the secret; declared value must be kept")
	// import (no declared inputs) must not invent a value
	imp, err := s.Read(pgp.ReadRequest{ID: "locked", Urn: urn("EgressPolicy", "e")})
	require.NoError(t, err)
	assert.Equal(t, "locked", str(imp.Properties, "tag"))

	assert.NotEmpty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "", "mode": "BLOCK"}))
	assert.NotEmpty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "t", "mode": "NOPE"}))
	assert.NotEmpty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "t", "mode": "BLOCK",
		"rules": []any{map[string]any{"op": "PROXY"}}}))
	assert.NotEmpty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "t", "mode": "BLOCK",
		"rules": []any{map[string]any{"op": "INJECT", "inject": map[string]any{"headerName": "H"}}}}))
	assert.Empty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "t", "mode": "BLOCK"}))
}

func TestChecks_RejectBadInputs(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	assert.NotEmpty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": ""}))
	assert.NotEmpty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": "q",
		"permissions": map[string]any{"permissionsType": "BOGUS"}}))
	assert.Empty(t, checkFails(t, s, "BuildkiteQueue", map[string]any{"queueName": "q"}))
	assert.NotEmpty(t, checkFails(t, s, "RegistryDefaultPolicy", map[string]any{}))
	assert.NotEmpty(t, checkFails(t, s, "RegistryDefaultPolicy", map[string]any{"defaultExpirationSeconds": 0}))
	assert.NotEmpty(t, checkFails(t, s, "RegistryDefaultPolicy", map[string]any{"expirationEnforcement": "X"}))
	assert.Empty(t, checkFails(t, s, "RegistryDefaultPolicy", map[string]any{"defaultExpirationSeconds": 20000}))
	assert.NotEmpty(t, checkFails(t, s, "RegistryRepositoryPolicy", map[string]any{"repository": "", "expirationSeconds": 20000}))
	assert.NotEmpty(t, checkFails(t, s, "RegistryRepositoryPolicy", map[string]any{"repository": "r"}))
	assert.Empty(t, checkFails(t, s, "RegistryRepositoryPolicy", map[string]any{"repository": "r", "expirationSeconds": 20000}))
}

func TestRevokableToken_ImportIsRefused(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(tokenSvc+"ListRevokableTokens", `{"tokens":[{"tokenId":"tok-1","name":"n","state":"ACTIVE"}]}`)
	s := newServerFor(t, f)
	_, err := s.Read(pgp.ReadRequest{ID: "tok-1", Urn: urn("RevokableToken", "t")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "import is not supported")
	assert.Empty(t, f.callsTo(tokenSvc+"ListRevokableTokens"), "must fail before touching the API")
}

func TestKeepDeclared_ZeroValuesRoundTrip(t *testing.T) {
	type inner struct{ N *int }
	type args struct {
		Flag  *bool
		Count *int
		Name  *string
		In    *inner
		Other *bool
	}
	f, z, e := false, 0, ""
	prior := args{Flag: &f, Count: &z, Name: &e, In: &inner{N: &z}}
	srv := args{Other: new(bool)} // the API drops zero values; Other was never declared
	*srv.Other = true
	srv.In = &inner{}
	keepDeclared(prior, &srv)
	require.NotNil(t, srv.Flag)
	assert.False(t, *srv.Flag)
	require.NotNil(t, srv.Count)
	assert.Zero(t, *srv.Count)
	require.NotNil(t, srv.Name)
	assert.Equal(t, "", *srv.Name)
	assert.Nil(t, srv.Other, "undeclared server defaults are still hidden")
}

func TestQueueRead_DeclaredZeroValueHasNoDiff(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(bkSvc+"GetQueue", `{"queue":{"id":"q-1","queueName":"default","cluster":"c1","organization":{"orgSlug":"acme"},"settings":{}}}`)
	s := newServerFor(t, f)
	in := pm(map[string]any{"queueName": "default", "egressPolicyTag": ""})
	read, err := s.Read(pgp.ReadRequest{ID: "q-1", Urn: urn("BuildkiteQueue", "q"), Inputs: in, Properties: in})
	require.NoError(t, err)
	assert.Contains(t, read.Inputs.AsMap(), "egressPolicyTag", "declared empty string must survive refresh")
}
