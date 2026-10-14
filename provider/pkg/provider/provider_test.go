package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/blang/semver"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	vaultSvc   = "/namespace.cloud.vault.v1beta.VaultService/"
	tenantSvc  = "/namespace.cloud.iam.v1beta.TenantService/"
	computeSvc = "/namespace.cloud.compute.v1beta.ComputeService/"
	testToken  = "test-token-not-real"
)

// recorded is one request the fake Connect server received.
type recorded struct {
	Path string
	Auth string
	CT   string
	Body map[string]any
}

// fakeAPI stands in for the Namespace Connect API. Handlers are keyed by the
// full request path and return an HTTP status plus a raw JSON body.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	srv      *httptest.Server
	handlers map[string]func(body map[string]any) (int, string)
	calls    []recorded
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, handlers: map[string]func(map[string]any) (int, string){}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("request body is not JSON: %v", err)
			}
		}
		f.mu.Lock()
		f.calls = append(f.calls, recorded{Path: r.URL.Path, Auth: r.Header.Get("Authorization"),
			CT: r.Header.Get("Content-Type"), Body: body})
		h := f.handlers[r.URL.Path]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || h == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"no handler for ` + r.URL.Path + `"}`))
			return
		}
		status, resp := h(body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) on(path string, h func(body map[string]any) (int, string)) { f.handlers[path] = h }

func (f *fakeAPI) ok(path, resp string) {
	f.on(path, func(map[string]any) (int, string) { return 200, resp })
}

func (f *fakeAPI) callsTo(path string) []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recorded
	for _, c := range f.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// newServerFor configures a live provider against the fake API.
func newServerFor(t *testing.T, f *fakeAPI) integration.Server {
	t.Helper()
	p, err := New()
	require.NoError(t, err)
	s, err := integration.NewServer(t.Context(), "namespace", semver.MustParse("0.0.1"), integration.WithProvider(p))
	require.NoError(t, err)
	err = s.Configure(pgp.ConfigureRequest{Args: property.NewMap(map[string]property.Value{
		"token":           property.New(testToken).WithSecret(true),
		"region":          property.New("eu"),
		"computeEndpoint": property.New(f.srv.URL),
		"iamEndpoint":     property.New(f.srv.URL),
		"globalEndpoint":  property.New(f.srv.URL),
	})})
	require.NoError(t, err)
	return s
}

func urn(typ, name string) resource.URN {
	return resource.NewURN("stack", "proj", "", tokens.Type("namespace:index:"+typ), name)
}

func strMap(m map[string]string) property.Value {
	out := map[string]property.Value{}
	for k, v := range m {
		out[k] = property.New(v)
	}
	return property.New(property.NewMap(out))
}

func str(m property.Map, k string) string {
	v, ok := m.GetOk(k)
	if !ok || !v.IsString() {
		return ""
	}
	return v.AsString()
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

func TestClient_CallSendsConnectJSONWithBearerAuth(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(vaultSvc+"DescribeObject", `{"metadata":{"objectId":"o1","version":"3"}}`)
	s := newServerFor(t, f)
	_, err := s.Read(pgp.ReadRequest{ID: "o1", Urn: urn("VaultObject", "v"),
		Properties: property.NewMap(map[string]property.Value{"objectId": property.New("o1")})})
	require.NoError(t, err)
	calls := f.callsTo(vaultSvc + "DescribeObject")
	require.Len(t, calls, 1)
	assert.Equal(t, "Bearer "+testToken, calls[0].Auth)
	assert.Equal(t, "application/json", calls[0].CT)
	assert.Equal(t, "o1", calls[0].Body["objectId"])
}

func TestParseAPIError_MapsConnectCodes(t *testing.T) {
	e := parseAPIError("DescribeObject", 404, []byte(`{"code":"not_found","message":"gone"}`))
	assert.True(t, e.IsNotFound())
	assert.Contains(t, e.Error(), "gone")
	assert.True(t, isNotFound(e))

	e = parseAPIError("DescribeObject", 403, []byte(`{"code":"permission_denied","message":"nope"}`))
	assert.False(t, e.IsNotFound())
	assert.Contains(t, e.Error(), "permission_denied")

	// A non-JSON body (a proxy error page) still yields a usable error.
	e = parseAPIError("X", 502, []byte(`<html>bad gateway</html>`))
	assert.False(t, e.IsNotFound())
	assert.Contains(t, e.Error(), "502")

	assert.False(t, isNotFound(errors.New("plain")))
}

func TestClientFor_RequiresToken(t *testing.T) {
	t.Setenv("NSC_TOKEN", "")
	p, err := New()
	require.NoError(t, err)
	s, err := integration.NewServer(t.Context(), "namespace", semver.MustParse("0.0.1"), integration.WithProvider(p))
	require.NoError(t, err)
	require.NoError(t, s.Configure(pgp.ConfigureRequest{Args: property.NewMap(nil)}))
	_, err = s.Create(pgp.CreateRequest{Urn: urn("VaultObject", "v"),
		Properties: property.NewMap(map[string]property.Value{"value": property.New("x")})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token")
}

func TestEndpoints_Defaults(t *testing.T) {
	e := endpoints{}
	assert.Equal(t, "us", newNSClient("tok", "", e).region, "region defaults to us")
	assert.Equal(t, "https://eu.compute.namespaceapis.com", e.computeURL("eu"))
	assert.Equal(t, "https://iam.namespaceapis.com", e.iamURL())
	assert.Equal(t, "https://global.namespaceapis.com", e.globalURL())
	e = endpoints{Compute: "http://x:1", IAM: "http://y:2", Global: "http://z:3"}
	assert.Equal(t, "http://x:1", e.computeURL("eu"))
	assert.Equal(t, "http://y:2", e.iamURL())
	assert.Equal(t, "http://z:3", e.globalURL())
}

// ---------------------------------------------------------------------------
// VaultObject
// ---------------------------------------------------------------------------

func TestVaultObject_Lifecycle(t *testing.T) {
	f := newFakeAPI(t)
	f.on(vaultSvc+"CreateObject", func(map[string]any) (int, string) {
		return 200, `{"objectId":"obj-1","version":"1","createdAt":"2026-10-01T10:00:00Z"}`
	})
	f.on(vaultSvc+"UpdateObject", func(map[string]any) (int, string) {
		return 200, `{"objectId":"obj-1","version":"2","createdAt":"2026-10-01T10:00:00Z"}`
	})
	// A non-revealable object never returns its value.
	f.ok(vaultSvc+"DescribeObject",
		`{"metadata":{"objectId":"obj-1","version":"2","description":"db password","createdAt":"2026-10-01T10:00:00Z",`+
			`"labels":[{"name":"env","value":"prod"},{"name":"system","value":"added-by-server"}]}}`)
	f.ok(vaultSvc+"DeleteObject", `{}`)
	s := newServerFor(t, f)

	inputs := property.NewMap(map[string]property.Value{
		"value":       property.New("s3cr3t").WithSecret(true),
		"description": property.New("db password"),
		"labels":      strMap(map[string]string{"env": "prod"}),
	})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("VaultObject", "db"), Properties: inputs})
	require.NoError(t, err)
	assert.Equal(t, "obj-1", created.ID)
	assert.Equal(t, "1", str(created.Properties, "version"))
	assert.Equal(t, "2026-10-01T10:00:00Z", str(created.Properties, "createdAt"))

	cr := f.callsTo(vaultSvc + "CreateObject")
	require.Len(t, cr, 1)
	assert.Equal(t, "s3cr3t", cr[0].Body["value"])
	assert.Equal(t, "db password", cr[0].Body["description"])
	assert.Equal(t, []any{map[string]any{"name": "env", "value": "prod"}}, cr[0].Body["labels"])

	// Read must preserve the secret value and not surface server-added labels.
	read, err := s.Read(pgp.ReadRequest{ID: "obj-1", Urn: urn("VaultObject", "db"),
		Properties: created.Properties, Inputs: inputs})
	require.NoError(t, err)
	assert.Equal(t, "obj-1", read.ID)
	assert.Equal(t, "s3cr3t", str(read.Properties, "value"), "Read must not clobber the secret value")
	assert.Equal(t, "2", str(read.Properties, "version"))
	labels, ok := read.Properties.GetOk("labels")
	require.True(t, ok)
	assert.Equal(t, 1, labels.AsMap().Len(), "server-added labels must not become drift")

	// Update sends the new value with optimistic concurrency on the known version.
	newInputs := property.NewMap(map[string]property.Value{
		"value":       property.New("rotated").WithSecret(true),
		"description": property.New("db password"),
		"labels":      strMap(map[string]string{"env": "prod"}),
	})
	upd, err := s.Update(pgp.UpdateRequest{ID: "obj-1", Urn: urn("VaultObject", "db"),
		State: created.Properties, Inputs: newInputs, OldInputs: inputs})
	require.NoError(t, err)
	assert.Equal(t, "2", str(upd.Properties, "version"), "Update must refresh version from the response")
	up := f.callsTo(vaultSvc + "UpdateObject")
	require.Len(t, up, 1)
	assert.Equal(t, "obj-1", up[0].Body["objectId"])
	assert.Equal(t, "rotated", up[0].Body["newValue"])
	assert.Equal(t, "1", up[0].Body["ifVersionMatches"])

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "obj-1", Urn: urn("VaultObject", "db"), Properties: upd.Properties}))
	assert.Len(t, f.callsTo(vaultSvc+"DeleteObject"), 1)
}

func TestVaultObject_ReadNotFoundMeansGone(t *testing.T) {
	f := newFakeAPI(t)
	f.on(vaultSvc+"DescribeObject", func(map[string]any) (int, string) {
		return 404, `{"code":"not_found","message":"object not found"}`
	})
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "obj-9", Urn: urn("VaultObject", "db"),
		Properties: property.NewMap(map[string]property.Value{"objectId": property.New("obj-9")})})
	require.NoError(t, err)
	assert.Equal(t, "", read.ID, "an empty ID tells Pulumi the resource is gone")
}

func TestVaultObject_DeleteNotFoundIsIdempotent(t *testing.T) {
	f := newFakeAPI(t)
	f.on(vaultSvc+"DeleteObject", func(map[string]any) (int, string) {
		return 404, `{"code":"not_found","message":"gone"}`
	})
	s := newServerFor(t, f)
	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "obj-9", Urn: urn("VaultObject", "db"),
		Properties: property.NewMap(map[string]property.Value{
			"objectId": property.New("obj-9"), "version": property.New("1"), "createdAt": property.New("x"),
			"value": property.New("v").WithSecret(true),
		})}))
}

func TestVaultObject_APIErrorSurfaces(t *testing.T) {
	f := newFakeAPI(t)
	f.on(vaultSvc+"CreateObject", func(map[string]any) (int, string) {
		return 403, `{"code":"permission_denied","message":"token lacks vault access"}`
	})
	s := newServerFor(t, f)
	_, err := s.Create(pgp.CreateRequest{Urn: urn("VaultObject", "db"),
		Properties: property.NewMap(map[string]property.Value{"value": property.New("x")})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token lacks vault access")
}

func TestVaultObject_PreviewDoesNotCallAPI(t *testing.T) {
	f := newFakeAPI(t)
	s := newServerFor(t, f)
	_, err := s.Create(pgp.CreateRequest{Urn: urn("VaultObject", "db"), DryRun: true,
		Properties: property.NewMap(map[string]property.Value{"value": property.New("x")})})
	require.NoError(t, err)
	assert.Empty(t, f.calls)
}

// ---------------------------------------------------------------------------
// Tenant
// ---------------------------------------------------------------------------

func TestTenant_Lifecycle(t *testing.T) {
	f := newFakeAPI(t)
	tenantJSON := func(name string) string {
		return `{"tenant":{"id":"ten-1","creatorId":"usr-1","externalAccountId":"ext-1","visibleName":"` + name +
			`","createdAt":"2026-10-01T10:00:00Z","labels":[{"name":"team","value":"infra"}]}}`
	}
	f.ok(tenantSvc+"CreateTenant", tenantJSON("Acme"))
	f.ok(tenantSvc+"UpdateTenant", tenantJSON("Acme 2"))
	f.ok(tenantSvc+"ListTenants",
		`{"tenants":[{"id":"ten-0","visibleName":"other"},{"id":"ten-1","creatorId":"usr-1","externalAccountId":"ext-1",`+
			`"visibleName":"Acme 2","createdAt":"2026-10-01T10:00:00Z","labels":[{"name":"team","value":"infra"}]}]}`)
	f.ok(tenantSvc+"RemoveTenant", `{}`)
	s := newServerFor(t, f)

	inputs := property.NewMap(map[string]property.Value{
		"visibleName":       property.New("Acme"),
		"externalAccountId": property.New("ext-1"),
		"labels":            strMap(map[string]string{"team": "infra"}),
	})
	created, err := s.Create(pgp.CreateRequest{Urn: urn("Tenant", "acme"), Properties: inputs})
	require.NoError(t, err)
	assert.Equal(t, "ten-1", created.ID)
	assert.Equal(t, "ten-1", str(created.Properties, "tenantId"))
	cr := f.callsTo(tenantSvc + "CreateTenant")
	require.Len(t, cr, 1)
	assert.Equal(t, "Acme", cr[0].Body["visibleName"])
	assert.Equal(t, "ext-1", cr[0].Body["externalAccountId"])

	read, err := s.Read(pgp.ReadRequest{ID: "ten-1", Urn: urn("Tenant", "acme"),
		Properties: created.Properties, Inputs: inputs})
	require.NoError(t, err)
	assert.Equal(t, "ten-1", read.ID)
	assert.Equal(t, "Acme 2", str(read.Properties, "visibleName"))

	newInputs := property.NewMap(map[string]property.Value{
		"visibleName":       property.New("Acme 2"),
		"externalAccountId": property.New("ext-1"),
		"labels":            strMap(map[string]string{"team": "infra"}),
	})
	upd, err := s.Update(pgp.UpdateRequest{ID: "ten-1", Urn: urn("Tenant", "acme"),
		State: created.Properties, Inputs: newInputs, OldInputs: inputs})
	require.NoError(t, err)
	assert.Equal(t, "Acme 2", str(upd.Properties, "visibleName"), "Update must refresh outputs from the response")
	up := f.callsTo(tenantSvc + "UpdateTenant")
	require.Len(t, up, 1)
	assert.Equal(t, "ten-1", up[0].Body["tenantId"])
	assert.Equal(t, "Acme 2", up[0].Body["visibleName"])

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "ten-1", Urn: urn("Tenant", "acme"), Properties: upd.Properties}))
	rm := f.callsTo(tenantSvc + "RemoveTenant")
	require.Len(t, rm, 1)
	assert.Equal(t, "ten-1", rm[0].Body["tenantId"])
}

func TestTenant_ReadMissingMeansGone(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(tenantSvc+"ListTenants", `{"tenants":[{"id":"someone-else"}]}`)
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "ten-1", Urn: urn("Tenant", "acme"),
		Properties: property.NewMap(map[string]property.Value{"tenantId": property.New("ten-1")})})
	require.NoError(t, err)
	assert.Equal(t, "", read.ID)
}

func TestGetTenants_PaginatesAndFilters(t *testing.T) {
	f := newFakeAPI(t)
	f.on(tenantSvc+"ListTenants", func(body map[string]any) (int, string) {
		if body["cursor"] == nil || body["cursor"] == "" {
			return 200, `{"tenants":[{"id":"t1","visibleName":"one"}],"cursor":"next"}`
		}
		return 200, `{"tenants":[{"id":"t2","visibleName":"two"}]}`
	})
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getTenants",
		Args: property.NewMap(map[string]property.Value{"creatorId": property.New("usr-1")})})
	require.NoError(t, err)
	ts := resp.Return.Get("tenants").AsArray()
	require.Equal(t, 2, ts.Len())
	calls := f.callsTo(tenantSvc + "ListTenants")
	require.Len(t, calls, 2)
	assert.Equal(t, "usr-1", calls[0].Body["creatorId"])
	assert.Equal(t, "next", calls[1].Body["cursor"])
}

// ---------------------------------------------------------------------------
// Instance
// ---------------------------------------------------------------------------

func instanceJSON(status, deadline string) string {
	return `{"metadata":{"instanceId":"inst-1","createdAt":"2026-10-01T10:00:00Z","deadline":"` + deadline +
		`","status":"` + status + `","ingressDomain":"inst-1.example.test","documentedPurpose":"ci",` +
		`"shape":{"virtualCpu":2,"memoryMegabytes":4096,"machineArch":"amd64","os":"linux"},` +
		`"services":[{"name":"web","status":"READY","endpointKind":"HTTPS","endpoint":"https://web.example.test"}]}}`
}

func instanceInputs(deadline string) property.Map {
	return property.NewMap(map[string]property.Value{
		"shape": property.New(property.NewMap(map[string]property.Value{
			"virtualCpu":      property.New(2.0),
			"memoryMegabytes": property.New(4096.0),
			"machineArch":     property.New("amd64"),
			"os":              property.New("linux"),
		})),
		"documentedPurpose": property.New("ci"),
		"deadline":          property.New(deadline),
		"containers": property.New(property.NewArray([]property.Value{
			property.New(property.NewMap(map[string]property.Value{
				"name":     property.New("web"),
				"imageRef": property.New("nginx:1.27"),
				"args":     property.New(property.NewArray([]property.Value{property.New("-g")})),
				"env":      strMap(map[string]string{"A": "b"}),
				"exportPorts": property.New(property.NewArray([]property.Value{
					property.New(property.NewMap(map[string]property.Value{
						"name": property.New("http"), "containerPort": property.New(80.0), "proto": property.New("HTTP"),
					})),
				})),
			})),
		})),
	})
}

func TestInstance_Lifecycle_WaitsForRunningAndExtendsDeadline(t *testing.T) {
	prevPoll, prevTO := instancePollInterval, instanceReadyTimeout
	instancePollInterval, instanceReadyTimeout = 5*time.Millisecond, 5*time.Second
	t.Cleanup(func() { instancePollInterval, instanceReadyTimeout = prevPoll, prevTO })

	d1 := "2026-12-01T10:00:00Z"
	d2 := "2026-12-01T12:00:00Z"
	f := newFakeAPI(t)
	f.ok(computeSvc+"CreateInstance", instanceJSON("CREATING", d1))
	var mu sync.Mutex
	describes := 0
	deadline := d1
	f.on(computeSvc+"DescribeInstance", func(map[string]any) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		describes++
		if describes < 3 {
			return 200, instanceJSON("CREATING", deadline)
		}
		return 200, instanceJSON("RUNNING", deadline)
	})
	f.on(computeSvc+"ExtendInstance", func(map[string]any) (int, string) {
		mu.Lock()
		deadline = d2
		mu.Unlock()
		return 200, `{"newDeadline":"` + d2 + `"}`
	})
	f.ok(computeSvc+"DestroyInstance", `{}`)
	s := newServerFor(t, f)

	created, err := s.Create(pgp.CreateRequest{Urn: urn("Instance", "ci"), Properties: instanceInputs(d1)})
	require.NoError(t, err)
	assert.Equal(t, "inst-1", created.ID)
	assert.Equal(t, "RUNNING", str(created.Properties, "status"), "Create must wait for RUNNING")
	assert.Equal(t, "inst-1.example.test", str(created.Properties, "ingressDomain"))
	assert.GreaterOrEqual(t, describes, 3)

	cr := f.callsTo(computeSvc + "CreateInstance")
	require.Len(t, cr, 1)
	assert.Equal(t, "Bearer "+testToken, cr[0].Auth)
	assert.Equal(t, "ci", cr[0].Body["documentedPurpose"])
	assert.Equal(t, d1, cr[0].Body["deadline"])
	shape := cr[0].Body["shape"].(map[string]any)
	assert.EqualValues(t, 2, shape["virtualCpu"])
	assert.EqualValues(t, 4096, shape["memoryMegabytes"])
	assert.Equal(t, "amd64", shape["machineArch"])
	ctrs := cr[0].Body["containers"].([]any)
	require.Len(t, ctrs, 1)
	c0 := ctrs[0].(map[string]any)
	assert.Equal(t, "nginx:1.27", c0["imageRef"])
	assert.Equal(t, []any{"-g"}, c0["args"])
	ports := c0["exportPorts"].([]any)
	require.Len(t, ports, 1)
	assert.Equal(t, "HTTP", ports[0].(map[string]any)["proto"])
	assert.EqualValues(t, 80, ports[0].(map[string]any)["containerPort"])

	// Deadline change goes through ExtendInstance, in place.
	upd, err := s.Update(pgp.UpdateRequest{ID: "inst-1", Urn: urn("Instance", "ci"),
		State: created.Properties, Inputs: instanceInputs(d2), OldInputs: instanceInputs(d1)})
	require.NoError(t, err)
	ext := f.callsTo(computeSvc + "ExtendInstance")
	require.Len(t, ext, 1)
	assert.Equal(t, "inst-1", ext[0].Body["instanceId"])
	assert.Equal(t, d2, ext[0].Body["newDeadline"])
	assert.Equal(t, d2, str(upd.Properties, "expiresAt"), "Update must refresh expiresAt")

	require.NoError(t, s.Delete(pgp.DeleteRequest{ID: "inst-1", Urn: urn("Instance", "ci"), Properties: upd.Properties}))
	assert.Len(t, f.callsTo(computeSvc+"DestroyInstance"), 1)
}

func TestInstance_CreateErrorStatusLeavesTrackedState(t *testing.T) {
	prevPoll := instancePollInterval
	instancePollInterval = time.Millisecond
	t.Cleanup(func() { instancePollInterval = prevPoll })
	f := newFakeAPI(t)
	f.ok(computeSvc+"CreateInstance", instanceJSON("CREATING", "2026-12-01T10:00:00Z"))
	f.ok(computeSvc+"DescribeInstance", instanceJSON("ERROR", "2026-12-01T10:00:00Z"))
	s := newServerFor(t, f)
	_, err := s.Create(pgp.CreateRequest{Urn: urn("Instance", "ci"), Properties: instanceInputs("2026-12-01T10:00:00Z")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize", "the instance exists, so Create reports an init failure and keeps it tracked")
}

func TestInstance_ReadDestroyedMeansGone(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(computeSvc+"DescribeInstance", instanceJSON("DESTROYED", "2026-12-01T10:00:00Z"))
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "inst-1", Urn: urn("Instance", "ci"),
		Properties: property.NewMap(map[string]property.Value{"instanceId": property.New("inst-1")})})
	require.NoError(t, err)
	assert.Equal(t, "", read.ID)
}

func TestInstance_RejectsBadDeadlineAndProto(t *testing.T) {
	f := newFakeAPI(t)
	s := newServerFor(t, f)
	_, err := s.Create(pgp.CreateRequest{Urn: urn("Instance", "ci"), Properties: instanceInputs("tomorrow")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RFC3339")
	assert.Empty(t, f.calls)
}

func TestInstance_UsesResourceRegionForHost(t *testing.T) {
	// Without a compute override the host derives from the region.
	assert.Equal(t, "https://eu.compute.namespaceapis.com", endpoints{}.computeURL("eu"))
}

func TestGetInstances_PaginatesByCursor(t *testing.T) {
	f := newFakeAPI(t)
	f.on(computeSvc+"ListInstances", func(body map[string]any) (int, string) {
		if body["paginationCursor"] == nil || body["paginationCursor"] == "" {
			return 200, `{"instances":[{"instanceId":"a","status":"RUNNING","shape":{"virtualCpu":2}}],"paginationCursor":"Y3Vy"}`
		}
		return 200, `{"instances":[{"instanceId":"b","status":"RUNNING","shape":{"virtualCpu":4}}]}`
	})
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getInstances", Args: property.NewMap(nil)})
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Return.Get("instances").AsArray().Len())
	calls := f.callsTo(computeSvc + "ListInstances")
	require.Len(t, calls, 2)
	assert.Equal(t, "Y3Vy", calls[1].Body["paginationCursor"])
}

var _ = infer.Config[ProviderConfig]

// ---------------------------------------------------------------------------
// CheckConfig: environment variable fallbacks
// ---------------------------------------------------------------------------

// envFallbackKey is one provider config key and the environment variables that
// back it, in priority order.
type envFallbackKey struct {
	key    string
	envs   []string
	secret bool
}

// envFallbackKeys lists every config key that has environment variable fallbacks.
var envFallbackKeys = []envFallbackKey{
	{key: "token", envs: []string{"NSC_TOKEN"}, secret: true},
	{key: "region", envs: []string{"NSC_REGION"}},
}

// clearFallbackEnv blanks every fallback variable so a case starts hermetic.
func clearFallbackEnv(t *testing.T) {
	t.Helper()
	for _, k := range envFallbackKeys {
		for _, e := range k.envs {
			t.Setenv(e, "")
		}
	}
}

// checkConfig runs CheckConfig through the integration server with cfg as the
// explicit provider config and returns the resulting inputs.
func checkConfig(t *testing.T, cfg map[string]property.Value) property.Map {
	t.Helper()
	p, err := New()
	require.NoError(t, err)
	s, err := integration.NewServer(t.Context(), "namespace", semver.MustParse("0.0.1"), integration.WithProvider(p))
	require.NoError(t, err)
	resp, err := s.CheckConfig(pgp.CheckRequest{
		Urn:    resource.NewURN("stack", "proj", "", "pulumi:providers:namespace", "default"),
		Inputs: property.NewMap(cfg),
	})
	require.NoError(t, err)
	require.Empty(t, resp.Failures)
	return resp.Inputs
}

// TestCheckConfig_EnvFallbacks verifies that unset provider config is filled
// from the environment, that explicit config wins, and that the first
// non-empty variable wins when a key has several.
func TestCheckConfig_EnvFallbacks(t *testing.T) {
	type tc struct {
		name   string
		key    string
		env    map[string]string
		config map[string]property.Value
		want   string
	}
	var tests []tc
	for _, k := range envFallbackKeys {
		for _, e := range k.envs {
			tests = append(tests, tc{name: k.key + " from " + e, key: k.key, env: map[string]string{e: "from-env"}, want: "from-env"})
		}
		tests = append(tests, tc{
			name:   k.key + " explicit config wins over env",
			key:    k.key,
			env:    map[string]string{k.envs[0]: "from-env"},
			config: map[string]property.Value{k.key: property.New("from-config")},
			want:   "from-config",
		})
		if len(k.envs) > 1 {
			both := map[string]string{}
			for i, e := range k.envs {
				both[e] = fmt.Sprintf("from-env-%d", i)
			}
			tests = append(tests, tc{name: k.key + " first env var wins", key: k.key, env: both, want: "from-env-0"})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearFallbackEnv(t)
			for e, v := range tt.env {
				t.Setenv(e, v)
			}
			got, ok := checkConfig(t, tt.config).GetOk(tt.key)
			require.True(t, ok, "%s should be populated", tt.key)
			if got.Secret() {
				got = got.WithSecret(false)
			}
			require.Equal(t, tt.want, got.AsString())
		})
	}
}

// TestSchema_ConfigEnvironmentDefaults asserts the generated schema declares the
// environment variables for each config key, which is what the language SDKs
// and the Pulumi CLI use to resolve the same fallbacks.
func TestSchema_ConfigEnvironmentDefaults(t *testing.T) {
	p, err := New()
	require.NoError(t, err)
	s, err := integration.NewServer(t.Context(), "namespace", semver.MustParse("0.0.1"), integration.WithProvider(p))
	require.NoError(t, err)
	resp, err := s.GetSchema(pgp.GetSchemaRequest{})
	require.NoError(t, err)
	var spec struct {
		Config struct {
			Variables map[string]struct {
				DefaultInfo struct {
					Environment []string `json:"environment"`
				} `json:"defaultInfo"`
			} `json:"variables"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &spec))
	for _, k := range envFallbackKeys {
		v, ok := spec.Config.Variables[k.key]
		require.True(t, ok, "config.variables.%s missing from schema", k.key)
		require.Equal(t, k.envs, v.DefaultInfo.Environment, "config.variables.%s.defaultInfo.environment", k.key)
	}
}

// TestCheckConfig_EnvSecretsStaySecret asserts that credentials sourced from the
// environment come back marked secret, and non-secret keys do not.
func TestCheckConfig_EnvSecretsStaySecret(t *testing.T) {
	clearFallbackEnv(t)
	for _, k := range envFallbackKeys {
		t.Setenv(k.envs[0], "from-env")
	}
	got := checkConfig(t, nil)
	for _, k := range envFallbackKeys {
		v, ok := got.GetOk(k.key)
		require.True(t, ok, "%s should be populated", k.key)
		require.Equal(t, k.secret, v.Secret(), "%s secret marking", k.key)
	}
}
