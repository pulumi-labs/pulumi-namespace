package provider

import (
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ghProfileSvc = "/namespace.cloud.github.v1beta.ProfileService/"
	egressSvc    = "/namespace.cloud.network.v1beta.EgressPolicyService/"
	bkSvc        = "/namespace.cloud.buildkite.QueueService/"
	registrySvc  = "/namespace.cloud.registry.v1beta.ContainerRegistryService/"
	tokenSvc     = "/namespace.cloud.iam.v1beta.TokenService/"
	imageSvc     = "/namespace.cloud.image.v1beta.ImageService/"
	usageSvc     = "/namespace.cloud.compute.v1beta.UsageService/"
	storageSvc   = "/namespace.cloud.compute.v1beta.StorageService/"
	ghJobsSvc    = "/namespace.cloud.github.v1beta.JobsService/"
)

// pv converts plain Go values into a property.Value for building test inputs.
func pv(v any) property.Value {
	switch x := v.(type) {
	case string:
		return property.New(x)
	case int:
		return property.New(float64(x))
	case bool:
		return property.New(x)
	case []any:
		out := make([]property.Value, 0, len(x))
		for _, e := range x {
			out = append(out, pv(e))
		}
		return property.New(property.NewArray(out))
	case map[string]any:
		return property.New(pm(x))
	}
	panic("unsupported test value")
}

func pm(m map[string]any) property.Map {
	out := map[string]property.Value{}
	for k, v := range m {
		out[k] = pv(v)
	}
	return property.NewMap(out)
}

func checkFails(t *testing.T, s interface {
	Check(pgp.CheckRequest) (pgp.CheckResponse, error)
}, typ string, in map[string]any) []pgp.CheckFailure {
	t.Helper()
	resp, err := s.Check(pgp.CheckRequest{Urn: urn(typ, "x"), Inputs: pm(in)})
	require.NoError(t, err)
	return resp.Failures
}

// --------------------------------------------------------------------------
// GithubProfile
// --------------------------------------------------------------------------

const profileResp = `{"profileId":"p-1","version":"3","createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-02T10:00:00Z",` +
	`"spec":{"tag":"fast","description":"d","instanceShape":{"virtualCpu":8,"memoryMegabytes":16384,"os":"linux","machineArch":"amd64"},` +
	`"networkPolicy":{"egress":"EGRESS_POLICY_TAG","egressPolicyTag":"locked"}},` +
	`"status":{"customRunnerImage":[{"imageStatus":{"status":"READY","imageRef":"img:1"}}]}}`

func profileInputs(desc string) property.Map {
	return pm(map[string]any{
		"tag":           "fast",
		"description":   desc,
		"instanceShape": map[string]any{"virtualCpu": 8, "memoryMegabytes": 16384, "os": "linux", "machineArch": "amd64"},
		"networkPolicy": map[string]any{"egress": "EGRESS_POLICY_TAG", "egressPolicyTag": "locked"},
	})
}

func TestGithubProfile_Lifecycle(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(ghProfileSvc+"CreateProfile", `{"profile":`+profileResp+`}`)
	f.ok(ghProfileSvc+"UpdateProfile", `{"profile":`+profileResp+`}`)
	f.ok(ghProfileSvc+"ListProfiles", `{"profiles":[`+profileResp+`]}`)
	f.ok(ghProfileSvc+"DeleteProfile", `{}`)
	s := newServerFor(t, f)
	in := profileInputs("d")

	created, err := s.Create(pgp.CreateRequest{Urn: urn("GithubProfile", "p"), Properties: in})
	require.NoError(t, err)
	assert.Equal(t, "p-1", created.ID)
	cr := f.callsTo(ghProfileSvc + "CreateProfile")
	require.Len(t, cr, 1)
	assert.Equal(t, "Bearer "+testToken, cr[0].Auth)
	assert.Equal(t, "fast", cr[0].Body["spec"].(map[string]any)["tag"])

	read, err := s.Read(pgp.ReadRequest{ID: "p-1", Urn: urn("GithubProfile", "p"), Properties: created.Properties, Inputs: in})
	require.NoError(t, err)
	assert.Equal(t, "p-1", read.ID)
	assert.Equal(t, "fast", str(read.Properties, "tag"))

	upd, err := s.Update(pgp.UpdateRequest{ID: "p-1", Urn: urn("GithubProfile", "p"),
		State: created.Properties, Inputs: profileInputs("new"), OldInputs: in})
	require.NoError(t, err)
	assert.NotEmpty(t, upd.Properties)
	up := f.callsTo(ghProfileSvc + "UpdateProfile")
	require.Len(t, up, 1)
	assert.Equal(t, "p-1", up[0].Body["profileId"])
	assert.Equal(t, "3", up[0].Body["updateVersion"], "optimistic concurrency uses the recorded version (int64 as string)")

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "p-1", Urn: urn("GithubProfile", "p"), Properties: upd.Properties}))
	assert.Len(t, f.callsTo(ghProfileSvc+"DeleteProfile"), 1)
}

func ghState() property.Map {
	m := profileInputs("d").AsMap()
	m["profileId"] = pv("p-9")
	m["version"] = pv(1)
	m["createdAt"] = pv("2026-10-01T10:00:00Z")
	m["updatedAt"] = pv("2026-10-01T10:00:00Z")
	return property.NewMap(m)
}

func TestGithubProfile_ReadMissingAndDeleteIdempotent(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(ghProfileSvc+"ListProfiles", `{"profiles":[]}`)
	f.on(ghProfileSvc+"DeleteProfile", func(map[string]any) (int, string) {
		return 404, `{"code":"not_found","message":"gone"}`
	})
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "p-9", Urn: urn("GithubProfile", "p"), Properties: ghState(), Inputs: profileInputs("d")})
	require.NoError(t, err)
	assert.Empty(t, read.ID, "a profile absent from the list means gone")
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "p-9", Urn: urn("GithubProfile", "p"), Properties: ghState()}))
}

func TestGithubProfile_RebuildTriggerCallsRebuild(t *testing.T) {
	withState := func(m property.Map) property.Map {
		o := m.AsMap()
		for k, v := range ghState().AsMap() {
			if _, ok := o[k]; !ok {
				o[k] = v
			}
		}
		return property.NewMap(o)
	}
	f := newFakeAPI(t)
	f.ok(ghProfileSvc+"UpdateProfile", `{"profile":`+profileResp+`}`)
	f.ok(ghProfileSvc+"RebuildProfileImage", `{"status":{"customRunnerImage":[{"imageStatus":{"status":"BUILDING"}}]}}`)
	s := newServerFor(t, f)
	img := map[string]any{"aptPackage": []any{}, "ubuntuSpec": map[string]any{"aptPackage": []any{"jq"}}}
	mk := func(trigger string) property.Map {
		return pm(map[string]any{"tag": "fast", "rebuildTrigger": trigger,
			"instanceShape":         map[string]any{"virtualCpu": 8, "memoryMegabytes": 16384, "os": "linux", "machineArch": "amd64"},
			"customRunnerImageSpec": map[string]any{"ubuntuSpec": img["ubuntuSpec"]}})
	}
	_, err := s.Update(pgp.UpdateRequest{ID: "p-1", Urn: urn("GithubProfile", "p"), State: withState(mk("a")), Inputs: mk("b"), OldInputs: mk("a")})
	require.NoError(t, err)
	assert.Len(t, f.callsTo(ghProfileSvc+"RebuildProfileImage"), 1)
	_, err = s.Update(pgp.UpdateRequest{ID: "p-1", Urn: urn("GithubProfile", "p"), State: withState(mk("b")), Inputs: mk("b"), OldInputs: mk("b")})
	require.NoError(t, err)
	assert.Len(t, f.callsTo(ghProfileSvc+"RebuildProfileImage"), 1, "unchanged trigger must not rebuild")
}

func TestGithubProfile_CheckRejectsBadInput(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	fs := checkFails(t, s, "GithubProfile", map[string]any{"tag": "", "instanceShape": map[string]any{"virtualCpu": 0, "memoryMegabytes": 0}})
	assert.NotEmpty(t, fs)
	fs = checkFails(t, s, "GithubProfile", map[string]any{"tag": "x", "rebuildTrigger": "a",
		"instanceShape": map[string]any{"virtualCpu": 2, "memoryMegabytes": 4096}})
	assert.NotEmpty(t, fs, "rebuildTrigger without customRunnerImageSpec is rejected")
}

// --------------------------------------------------------------------------
// EgressPolicy
// --------------------------------------------------------------------------

func egressInputs(mode string) property.Map {
	return pm(map[string]any{"tag": "locked", "description": "ci", "mode": mode,
		"rules": []any{map[string]any{"op": "ALLOW", "matcher": map[string]any{"matchDomains": []any{"*.example.com"}}}}})
}

func egressGet(rev string) string {
	return `{"revision":"` + rev + `","policy":{"tag":"locked","description":"ci","spec":{"mode":"BLOCK",` +
		`"rules":[{"op":"ALLOW","matcher":{"matchDomains":["*.example.com"]}}]}}}`
}

func TestEgressPolicy_Lifecycle_CreateUpdateDeleteDisables(t *testing.T) {
	f := newFakeAPI(t)
	f.on(egressSvc+"GetEgressPolicy", func(map[string]any) (int, string) {
		return 404, `{"code":"not_found","message":"no such policy"}`
	})
	f.ok(egressSvc+"CreateEgressPolicy", `{"revision":"1"}`)
	f.ok(egressSvc+"UpdateEgressPolicy", `{"revision":"2"}`)
	s := newServerFor(t, f)

	created, err := s.Create(pgp.CreateRequest{Urn: urn("EgressPolicy", "e"), Properties: egressInputs("BLOCK")})
	require.NoError(t, err)
	assert.Equal(t, "locked", created.ID)
	assert.Empty(t, f.callsTo(egressSvc+"UpdateEgressPolicy"))
	cr := f.callsTo(egressSvc + "CreateEgressPolicy")
	require.Len(t, cr, 1)
	spec := cr[0].Body["policy"].(map[string]any)["spec"].(map[string]any)
	assert.Equal(t, "BLOCK", spec["mode"])

	// Update must use the revision recorded in state as matchRevision.
	f.ok(egressSvc+"GetEgressPolicy", egressGet("1"))
	upd, err := s.Update(pgp.UpdateRequest{ID: "locked", Urn: urn("EgressPolicy", "e"),
		State: created.Properties, Inputs: egressInputs("ADVISORY"), OldInputs: egressInputs("BLOCK")})
	require.NoError(t, err)
	assert.NotEmpty(t, upd.Properties)
	up := f.callsTo(egressSvc + "UpdateEgressPolicy")
	require.Len(t, up, 1)
	assert.NotEmpty(t, up[0].Body["matchRevision"])

	// Delete has no API: the policy is switched to DISABLED with no rules.
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "locked", Urn: urn("EgressPolicy", "e"), Properties: upd.Properties}))
	up = f.callsTo(egressSvc + "UpdateEgressPolicy")
	require.Len(t, up, 2)
	pol := up[1].Body["policy"].(map[string]any)
	assert.Equal(t, "locked", pol["tag"])
	dspec, _ := pol["spec"].(map[string]any)
	assert.Equal(t, "DISABLED", dspec["mode"])
	assert.Empty(t, dspec["rules"])
}

func TestEgressPolicy_CreateAdoptsExistingTag(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(egressSvc+"GetEgressPolicy", egressGet("7"))
	f.ok(egressSvc+"UpdateEgressPolicy", `{"revision":"8"}`)
	s := newServerFor(t, f)
	created, err := s.Create(pgp.CreateRequest{Urn: urn("EgressPolicy", "e"), Properties: egressInputs("BLOCK")})
	require.NoError(t, err)
	assert.Equal(t, "locked", created.ID)
	assert.Empty(t, f.callsTo(egressSvc+"CreateEgressPolicy"))
	up := f.callsTo(egressSvc + "UpdateEgressPolicy")
	require.Len(t, up, 1)
	assert.Equal(t, "7", up[0].Body["matchRevision"])
}

func egressState() property.Map {
	m := egressInputs("BLOCK").AsMap()
	m["revision"] = pv(1)
	m["adopted"] = pv(false)
	return property.NewMap(m)
}

func TestEgressPolicy_DeleteNotFoundIsIdempotentAndReadMissing(t *testing.T) {
	f := newFakeAPI(t)
	f.on(egressSvc+"GetEgressPolicy", func(map[string]any) (int, string) { return 404, `{"code":"not_found"}` })
	f.on(egressSvc+"UpdateEgressPolicy", func(map[string]any) (int, string) { return 404, `{"code":"not_found"}` })
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "locked", Urn: urn("EgressPolicy", "e"), Properties: egressState(), Inputs: egressInputs("BLOCK")})
	require.NoError(t, err)
	assert.Empty(t, read.ID)
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "locked", Urn: urn("EgressPolicy", "e"), Properties: egressState()}))
}

func TestEgressPolicy_CheckValidation(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	assert.NotEmpty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "x", "mode": "NOPE"}))
	assert.NotEmpty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "x", "mode": "BLOCK",
		"rules": []any{map[string]any{"op": "PROXY"}}}), "PROXY needs proxy options")
	assert.Empty(t, checkFails(t, s, "EgressPolicy", map[string]any{"tag": "x", "mode": "BLOCK"}))
}

// --------------------------------------------------------------------------
// BuildkiteQueue (adopt-style)
// --------------------------------------------------------------------------

const bkQueue = `{"id":"q-1","queueName":"default","cluster":"c1","organization":{"orgSlug":"acme"},"settings":{"egressPolicyTag":"locked"}}`

func TestBuildkiteQueue_AdoptLifecycle(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(bkSvc+"ListQueues", `{"queues":[`+bkQueue+`]}`)
	f.ok(bkSvc+"UpdateQueue", `{"queue":`+bkQueue+`}`)
	f.ok(bkSvc+"GetQueue", `{"queue":`+bkQueue+`}`)
	s := newServerFor(t, f)
	in := pm(map[string]any{"queueName": "default", "egressPolicyTag": "locked"})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("BuildkiteQueue", "q"), Properties: in})
	require.NoError(t, err)
	assert.Equal(t, "q-1", created.ID)
	up := f.callsTo(bkSvc + "UpdateQueue")
	require.Len(t, up, 1)
	assert.Equal(t, "q-1", up[0].Body["queueId"])
	assert.Equal(t, "locked", up[0].Body["settings"].(map[string]any)["egressPolicyTag"])

	read, err := s.Read(pgp.ReadRequest{ID: "q-1", Urn: urn("BuildkiteQueue", "q"), Properties: created.Properties, Inputs: in})
	require.NoError(t, err)
	assert.Equal(t, "q-1", read.ID)

	// Destroy restores the settings recorded at adoption (here: none).
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "q-1", Urn: urn("BuildkiteQueue", "q"), Properties: created.Properties}))
	assert.Len(t, f.callsTo(bkSvc+"UpdateQueue"), 2, "delete restores the recorded prior")
}

func TestBuildkiteQueue_MissingQueueFailsClearly(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(bkSvc+"ListQueues", `{"queues":[]}`)
	s := newServerFor(t, f)
	_, err := s.Create(pgp.CreateRequest{Urn: urn("BuildkiteQueue", "q"), Properties: pm(map[string]any{"queueName": "nope"})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Empty(t, f.callsTo(bkSvc+"UpdateQueue"))
}

func TestBuildkiteQueue_AmbiguousNameFails(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(bkSvc+"ListQueues", `{"queues":[`+bkQueue+`,`+bkQueue+`]}`)
	s := newServerFor(t, f)
	_, err := s.Create(pgp.CreateRequest{Urn: urn("BuildkiteQueue", "q"), Properties: pm(map[string]any{"queueName": "default"})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

// --------------------------------------------------------------------------
// Registry policies
// --------------------------------------------------------------------------

// Restore-on-destroy is asserted against stateful fakes in review_fixes_test.go
// (TestRegistryDefaultPolicy_DestroyRestoresRecordedPrior and friends).

func TestRegistryRepositoryPolicy_Lifecycle(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(registrySvc+"GetRepositoryPolicy", `{"revision":"2","expiration":"60s","expirationEnforcement":"EXPIRATION_ENFORCEMENT_ENFORCE"}`)
	f.ok(registrySvc+"UpdateRepositoryPolicy", `{}`)
	s := newServerFor(t, f)
	in := pm(map[string]any{"repository": "acme/app", "expirationSeconds": 7200})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("RegistryRepositoryPolicy", "r"), Properties: in})
	require.NoError(t, err)
	assert.Equal(t, "acme/app", created.ID)
	gets := f.callsTo(registrySvc + "GetRepositoryPolicy")
	require.NotEmpty(t, gets)
	assert.Equal(t, "acme/app", gets[0].Body["repository"])
	up := f.callsTo(registrySvc + "UpdateRepositoryPolicy")
	require.Len(t, up, 1)
	assert.Equal(t, "7200s", up[0].Body["expiration"])

	read, err := s.Read(pgp.ReadRequest{ID: "acme/app", Urn: urn("RegistryRepositoryPolicy", "r"), Properties: created.Properties, Inputs: in})
	require.NoError(t, err)
	assert.Equal(t, "acme/app", read.ID)
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "acme/app", Urn: urn("RegistryRepositoryPolicy", "r"), Properties: created.Properties}))
	assert.Len(t, f.callsTo(registrySvc+"UpdateRepositoryPolicy"), 2, "delete restores the prior policy")
}

// --------------------------------------------------------------------------
// TenantGroup
// --------------------------------------------------------------------------

func TestTenantGroup_Lifecycle(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(tenantSvc+"EnsureTenantGroup", `{"name":"ci"}`)
	f.ok(tenantSvc+"DescribeTenantGroup", `{"name":"ci","policies":[{"policy":"max-instances","value":"5"}],"enrolledTenantIds":["t1"]}`)
	f.ok(tenantSvc+"DeleteTenantGroup", `{}`)
	s := newServerFor(t, f)
	in := pm(map[string]any{"name": "ci", "policies": []any{map[string]any{"policy": "max-instances", "value": "5"}}})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("TenantGroup", "g"), Properties: in})
	require.NoError(t, err)
	assert.Equal(t, "ci", created.ID)
	cr := f.callsTo(tenantSvc + "EnsureTenantGroup")
	require.Len(t, cr, 1)
	assert.Equal(t, "ci", cr[0].Body["name"])
	read, err := s.Read(pgp.ReadRequest{ID: "ci", Urn: urn("TenantGroup", "g"), Properties: created.Properties, Inputs: in})
	require.NoError(t, err)
	assert.Equal(t, "ci", read.ID)
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "ci", Urn: urn("TenantGroup", "g"), Properties: created.Properties}))
	assert.Len(t, f.callsTo(tenantSvc+"DeleteTenantGroup"), 1)
}

func TestTenantGroup_ReadNotFound(t *testing.T) {
	f := newFakeAPI(t)
	f.on(tenantSvc+"DescribeTenantGroup", func(map[string]any) (int, string) { return 404, `{"code":"not_found"}` })
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "ci", Urn: urn("TenantGroup", "g"), Properties: pm(map[string]any{"name": "ci"}), Inputs: pm(map[string]any{"name": "ci"})})
	require.NoError(t, err)
	assert.Empty(t, read.ID)
}

func TestGetTenantGroups(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(tenantSvc+"ListTenantGroups", `{"tenantGroups":[{"name":"a"},{"name":"b"}]}`)
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getTenantGroups", Args: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Return.Get("tenantGroups").AsArray().Len())
}

// --------------------------------------------------------------------------
// RevokableToken
// --------------------------------------------------------------------------

const tokResp = `{"tokenId":"tok-1","tenantId":"t1","name":"ci","createdAt":"2026-10-01T10:00:00Z","expiresAt":"2026-11-01T10:00:00Z","state":"ACTIVE"}`

func TestRevokableToken_LifecycleSecretAndRevoke(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(tokenSvc+"CreateRevokableToken", `{"bearerToken":"nstok-SECRET","token":`+tokResp+`}`)
	f.ok(tokenSvc+"RefreshRevokableToken", `{"token":`+tokResp+`}`)
	f.ok(tokenSvc+"ListRevokableTokens", `{"tokens":[`+tokResp+`]}`)
	f.ok(tokenSvc+"RevokeRevokableToken", `{}`)
	s := newServerFor(t, f)
	in := pm(map[string]any{"name": "ci", "scope": "TENANT_SCOPE", "refreshTrigger": "a",
		"grants": []any{map[string]any{"resourceType": "instance", "resourceId": "*", "actions": []any{"read"}}}})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("RevokableToken", "t"), Properties: in})
	require.NoError(t, err)
	assert.Equal(t, "tok-1", created.ID)
	bt, ok := created.Properties.GetOk("bearerToken")
	require.True(t, ok)
	// Output secrecy is applied by the engine from the schema; see
	// TestRevokableTokenBearerIsSecretOutput in schema_test.go.
	assert.Equal(t, "nstok-SECRET", bt.AsString())
	cr := f.callsTo(tokenSvc + "CreateRevokableToken")
	require.Len(t, cr, 1)
	assert.Equal(t, "TENANT_SCOPE", cr[0].Body["scope"])

	// Read must keep the bearer token: the API only returns it at creation.
	read, err := s.Read(pgp.ReadRequest{ID: "tok-1", Urn: urn("RevokableToken", "t"), Properties: created.Properties, Inputs: in})
	require.NoError(t, err)
	assert.Equal(t, "nstok-SECRET", str(read.Properties, "bearerToken"))

	// A changed refreshTrigger refreshes the token in place.
	in2 := pm(map[string]any{"name": "ci", "scope": "TENANT_SCOPE", "refreshTrigger": "b", "refreshMinimumSeconds": 600,
		"grants": []any{map[string]any{"resourceType": "instance", "resourceId": "*", "actions": []any{"read"}}}})
	_, err = s.Update(pgp.UpdateRequest{ID: "tok-1", Urn: urn("RevokableToken", "t"), State: created.Properties, Inputs: in2, OldInputs: in})
	require.NoError(t, err)
	rf := f.callsTo(tokenSvc + "RefreshRevokableToken")
	require.Len(t, rf, 1)
	assert.Equal(t, "tok-1", rf[0].Body["tokenId"])
	assert.Equal(t, "600s", rf[0].Body["minimumDuration"])

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "tok-1", Urn: urn("RevokableToken", "t"), Properties: created.Properties}))
	rv := f.callsTo(tokenSvc + "RevokeRevokableToken")
	require.Len(t, rv, 1)
	assert.Equal(t, "tok-1", rv[0].Body["tokenId"])
}

func TestRevokableToken_RevokedOrExpiredMeansGone(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(tokenSvc+"ListRevokableTokens", `{"tokens":[{"tokenId":"tok-1","name":"ci","state":"REVOKED"}]}`)
	f.on(tokenSvc+"RevokeRevokableToken", func(map[string]any) (int, string) { return 404, `{"code":"not_found"}` })
	s := newServerFor(t, f)
	in := pm(map[string]any{"name": "ci"})
	pmap := pm(map[string]any{"name": "ci", "tokenId": "tok-1", "tenantId": "t1", "tokenState": "ACTIVE",
		"createdAt": "x", "tokenExpiresAt": "y"}).AsMap()
	pmap["bearerToken"] = property.New("x").WithSecret(true)
	props := property.NewMap(pmap)
	read, err := s.Read(pgp.ReadRequest{ID: "tok-1", Urn: urn("RevokableToken", "t"), Properties: props, Inputs: in})
	require.NoError(t, err)
	assert.Empty(t, read.ID)
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "tok-1", Urn: urn("RevokableToken", "t"), Properties: props}))
}

func TestRevokableToken_CheckRejectsBadExpiryAndScope(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	assert.NotEmpty(t, checkFails(t, s, "RevokableToken", map[string]any{"name": "x", "expiresAt": "tomorrow"}))
	assert.NotEmpty(t, checkFails(t, s, "RevokableToken", map[string]any{"name": "x", "scope": "GLOBAL"}))
	assert.Empty(t, checkFails(t, s, "RevokableToken", map[string]any{"name": "x", "expiresAt": "2027-01-01T00:00:00Z"}))
}

func TestRevokableToken_ErrorDoesNotLeakToken(t *testing.T) {
	f := newFakeAPI(t)
	f.on(tokenSvc+"CreateRevokableToken", func(map[string]any) (int, string) {
		return 403, `{"code":"permission_denied","message":"nope"}`
	})
	s := newServerFor(t, f)
	_, err := s.Create(pgp.CreateRequest{Urn: urn("RevokableToken", "t"), Properties: pm(map[string]any{"name": "ci"})})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), testToken)
}

// --------------------------------------------------------------------------
// Blueprint
// --------------------------------------------------------------------------

const bpResp = `{"id":"bp-1","name":"base","createdAt":"2026-10-01T10:00:00Z","spec":{"dockerfile":{"content":"FROM ubuntu"}},"status":{"imageRef":"img:1"}}`

func TestBlueprint_Lifecycle(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(imageSvc+"CreateBlueprint", `{"blueprint":`+bpResp+`}`)
	f.ok(imageSvc+"FetchBlueprint", `{"blueprint":`+bpResp+`}`)
	f.ok(imageSvc+"RemoveBlueprint", `{}`)
	f.ok(imageSvc+"ListBlueprints", `{"blueprints":[`+bpResp+`]}`)
	s := newServerFor(t, f)
	in := pm(map[string]any{"name": "base", "dockerfileContent": "FROM ubuntu"})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("Blueprint", "b"), Properties: in})
	require.NoError(t, err)
	assert.Equal(t, "base", created.ID)
	cr := f.callsTo(imageSvc + "CreateBlueprint")
	require.Len(t, cr, 1)
	assert.Equal(t, "base", cr[0].Body["blueprintName"])
	assert.Equal(t, "FROM ubuntu", cr[0].Body["spec"].(map[string]any)["dockerfile"].(map[string]any)["content"])

	read, err := s.Read(pgp.ReadRequest{ID: "base", Urn: urn("Blueprint", "b"), Properties: created.Properties, Inputs: in})
	require.NoError(t, err)
	assert.Equal(t, "base", read.ID)
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "base", Urn: urn("Blueprint", "b"), Properties: created.Properties}))
	assert.Len(t, f.callsTo(imageSvc+"RemoveBlueprint"), 1)

	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getBlueprints", Args: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Return.Get("blueprints").AsArray().Len())
}

func TestBlueprint_CheckRequiresExactlyOneSource(t *testing.T) {
	s := newServerFor(t, newFakeAPI(t))
	assert.NotEmpty(t, checkFails(t, s, "Blueprint", map[string]any{"name": "b"}))
	assert.NotEmpty(t, checkFails(t, s, "Blueprint", map[string]any{"name": "b", "dockerfileContent": "FROM x", "aptBaseImage": "ubuntu"}))
	assert.Empty(t, checkFails(t, s, "Blueprint", map[string]any{"name": "b", "dockerfileContent": "FROM x"}))
}

// --------------------------------------------------------------------------
// Data sources
// --------------------------------------------------------------------------

func TestGetGithubProfiles(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(ghProfileSvc+"ListProfiles", `{"profiles":[`+profileResp+`]}`)
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getGithubProfiles", Args: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Return.Get("profiles").AsArray().Len())
}

func TestGetRegistryRepositoriesAndImages_Paginate(t *testing.T) {
	f := newFakeAPI(t)
	f.on(registrySvc+"ListRepositories", func(b map[string]any) (int, string) {
		if b["paginationCursor"] == nil {
			return 200, `{"repositories":[{"name":"a"}],"paginationCursor":"Yg=="}`
		}
		return 200, `{"repositories":[{"name":"b"}]}`
	})
	f.on(registrySvc+"ListImages", func(b map[string]any) (int, string) {
		if b["paginationCursor"] == nil {
			return 200, `{"images":[{"repository":"a","digest":"sha256:1","tags":["v1"]}],"paginationCursor":"Yg=="}`
		}
		return 200, `{"images":[{"repository":"a","digest":"sha256:2"}]}`
	})
	s := newServerFor(t, f)
	r, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getRegistryRepositories", Args: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Equal(t, 2, r.Return.Get("repositories").AsArray().Len())
	require.Len(t, f.callsTo(registrySvc+"ListRepositories"), 2)
	assert.Equal(t, "Yg==", f.callsTo(registrySvc + "ListRepositories")[1].Body["paginationCursor"])

	im, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getRegistryImages",
		Args: pm(map[string]any{"repository": "a"})})
	require.NoError(t, err)
	assert.Equal(t, 2, im.Return.Get("images").AsArray().Len())
	first := f.callsTo(registrySvc + "ListImages")[0].Body
	mr := first["matchRepository"].(map[string]any)
	assert.Equal(t, []any{"a"}, mr["values"])
}

func TestGetRegistryRepositories_RepeatedCursorTerminates(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(registrySvc+"ListRepositories", `{"repositories":[{"name":"a"}],"paginationCursor":"Yg=="}`)
	s := newServerFor(t, f)
	_, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getRegistryRepositories", Args: property.NewMap(nil)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repeated pagination cursor")
}

func TestGetUsage(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(usageSvc+"GetUsage", `{"total":{"instanceMinutes":{"unit":"120","wall":"60"},"builds":{"count":"3","unitMinutes":"9","wallMinutes":"4"}},`+
		`"perDay":[{"year":2026,"month":10,"day":1,"total":{"instanceMinutes":{"unit":"120","wall":"60"}}}]}`)
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getUsage",
		Args: pm(map[string]any{"periodStart": "2026-10-01", "periodEnd": "2026-10-08"})})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Return)
	c := f.callsTo(usageSvc + "GetUsage")
	require.Len(t, c, 1)
	assert.Equal(t, map[string]any{"year": float64(2026), "month": float64(10), "day": float64(1)}, c[0].Body["periodStart"])
	_, err = s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getUsage",
		Args: pm(map[string]any{"periodStart": "oct 1", "periodEnd": "2026-10-08"})})
	require.Error(t, err)
}

func TestGetVolumes(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(storageSvc+"ListCacheVolumes", `{"cacheVolume":[{"id":"c1","tag":"go","metadata":{"sizeMb":1024,"state":"STATE_AT_REST"}}]}`)
	f.ok(storageSvc+"ListPersistentVolumes", `{"volumes":[{"id":"v1","tag":"data","sizeMb":2048}]}`)
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getVolumes", Args: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Return.Get("cacheVolumes").AsArray().Len())
	assert.Equal(t, 1, resp.Return.Get("persistentVolumes").AsArray().Len())
}

func TestGetGithubJobs(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(ghJobsSvc+"ListJobs", `{"jobs":[{"jobId":"42","repository":"acme/app","conclusion":"success"}]}`)
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getGithubJobs",
		Args: pm(map[string]any{"repository": "acme/app"})})
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Return.Get("jobs").AsArray().Len())
	c := f.callsTo(ghJobsSvc + "ListJobs")
	require.Len(t, c, 1)
	assert.Equal(t, []any{"acme/app"}, c[0].Body["repository"].(map[string]any)["values"])
}
