// Copyright 2026, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	buildkitev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/buildkite"
	computev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/compute/v1beta"
	githubv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/github/v1beta"
	iamv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/iam/v1beta"
	imagev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/image/v1beta"
	networkv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/network/v1beta"
	registryv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/registry/v1beta"
	vaultv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/vault/v1beta"
	"buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/stdlib"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ---------------------------------------------------------------------------
// nsFake: a stateful in-memory stand-in for the Namespace Cloud Connect API.
//
// It speaks the real wire format (every request and response is the generated
// protobuf message, encoded with protojson) and models the API behaviour the
// provider has to cope with:
//   - the server attaches a system label to vault objects, tenants and instances
//     that the user never declared;
//   - the server fills defaults the user left out (instance machine arch, OS and
//     deadline, token scope);
//   - non-revealable vault objects never return their value, UpdateObject is
//     guarded by the object version;
//   - tenants have no describe call (list only), creatorId is not echoed;
//   - an instance only exists in the region it was created in;
//   - CreateBlueprint upserts by name; profiles and egress policies are guarded
//     by a version / revision; registry policies are guarded by a revision;
//   - Remove/Delete of something that is gone answers not_found;
//   - egress policy secret inject values are redacted on read.
// ---------------------------------------------------------------------------

const systemLabelName, systemLabelValue = "system/managed-by", "namespace"

type rpcErr struct {
	status int
	code   string
	msg    string
}

func (e *rpcErr) Error() string { return e.code + ": " + e.msg }

func errNotFound(format string, a ...any) *rpcErr {
	return &rpcErr{status: 404, code: "not_found", msg: fmt.Sprintf(format, a...)}
}
func errConflict(format string, a ...any) *rpcErr {
	return &rpcErr{status: 400, code: "failed_precondition", msg: fmt.Sprintf(format, a...)}
}
func errExists(format string, a ...any) *rpcErr {
	return &rpcErr{status: 409, code: "already_exists", msg: fmt.Sprintf(format, a...)}
}

type nsFakeVault struct {
	md         *vaultv1.VaultObjectMetadata
	value      string
	revealable bool
	ver        int
}

type nsFakeInstance struct {
	res    *computev1.DescribeInstanceResponse
	region string
	// describesUntilRunning is how many describe calls still answer CREATING.
	describesUntilRunning int
}

type nsFakeToken struct {
	tok    *iamv1.RevokableToken
	bearer string
}

type nsFakeEgress struct {
	pol *networkv1.EgressPolicy
	rev int64
}

type nsFakeRegPolicy struct {
	rev  int64
	secs int64
	enf  registryv1.ExpirationEnforcement
}

type nsFake struct {
	t   *testing.T
	mu  sync.Mutex
	srv *httptest.Server

	handlers map[string]func([]byte) (proto.Message, *rpcErr)
	calls    map[string]int
	reqs     map[string][]proto.Message
	regions  map[string][]string // method -> region path segment of each call
	failing  map[string]*rpcErr  // method -> error to answer instead of handling it
	// failFrom makes calls of a method after the first n fail with the error.
	failFrom map[string]failAfter
	seq      int
	now      time.Time

	// pageSize caps the entries per list page so pagination is exercised.
	pageSize int
	// bootDescribes is how many DescribeInstance calls still answer CREATING
	// for a new instance.
	bootDescribes int

	vault        map[string]*nsFakeVault
	tenants      []*iamv1.Tenant
	tenantPols   map[string][]*iamv1.TenantPolicy
	groups       map[string]*iamv1.DescribeTenantGroupResponse
	tokens       []*nsFakeToken
	instances    map[string]*nsFakeInstance
	profiles     []*githubv1.RunnerProfileWithStatus
	rebuilds     map[string]int
	egress       map[string]*nsFakeEgress
	queues       []*buildkitev1.BuildkiteQueue
	regDefault   nsFakeRegPolicy
	repos        map[string]*nsFakeRegPolicy
	blueprints   map[string]*imagev1.ImageBlueprint
	blueprintVer map[string]int

	sharedImages []*registryv1.SharedImage
	ingresses    map[string][]*computev1.AllocatedIngress // by instance id
	// ingressRename, when set, rewrites the name the server allocates (it normalises or suffixes names).
	ingressRename func(string) string
	// ingressHideResponse makes CreateIngress allocate but return no ingresses.
	ingressHideResponse bool
	members             []*iamv1.TenantMember
	callerPols          []*iamv1.TenantPolicy
}

func newNSFake(t *testing.T) *nsFake {
	t.Helper()
	f := &nsFake{
		t: t, handlers: map[string]func([]byte) (proto.Message, *rpcErr){}, calls: map[string]int{},
		reqs: map[string][]proto.Message{}, regions: map[string][]string{}, failing: map[string]*rpcErr{}, failFrom: map[string]failAfter{},
		now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), pageSize: 2,
		vault: map[string]*nsFakeVault{}, tenantPols: map[string][]*iamv1.TenantPolicy{},
		groups: map[string]*iamv1.DescribeTenantGroupResponse{}, instances: map[string]*nsFakeInstance{},
		rebuilds: map[string]int{}, egress: map[string]*nsFakeEgress{}, repos: map[string]*nsFakeRegPolicy{},
		blueprints: map[string]*imagev1.ImageBlueprint{}, blueprintVer: map[string]int{},
		ingresses:  map[string][]*computev1.AllocatedIngress{},
		regDefault: nsFakeRegPolicy{rev: 1},
	}
	f.register()
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *nsFake) with(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func (f *nsFake) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

// failWith makes every call of method answer err until failWith(method, nil).
func (f *nsFake) failWith(method string, err *rpcErr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.failing, method)
		return
	}
	f.failing[method] = err
}

type failAfter struct {
	n   int
	err *rpcErr
}

// failAfterCalls lets the first n calls of method through and fails the rest
// until failAfterCalls(method, 0, nil).
func (f *nsFake) failAfterCalls(method string, n int, err *rpcErr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.failFrom, method)
		return
	}
	f.failFrom[method] = failAfter{n: f.calls[method] + n, err: err}
}

func (f *nsFake) nextID(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%d", prefix, f.seq)
}

func (f *nsFake) ts() *timestamppb.Timestamp { return timestamppb.New(f.now) }

func (f *nsFake) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	path := strings.TrimPrefix(r.URL.Path, "/")
	region := ""
	if !strings.HasPrefix(path, "namespace.") { // "/{region}/service/Method"
		region, path, _ = strings.Cut(path, "/")
	}
	method := path[strings.LastIndex(path, "/")+1:]
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	writeErr := func(e *rpcErr) {
		w.WriteHeader(e.status)
		b, _ := json.Marshal(map[string]string{"code": e.code, "message": e.msg})
		_, _ = w.Write(b)
	}
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		writeErr(&rpcErr{status: 401, code: "unauthenticated", msg: "bad token"})
		return
	}
	h := f.handlers[path]
	if h == nil || r.Method != http.MethodPost {
		writeErr(errNotFound("no handler for %s", path))
		return
	}
	f.calls[method]++
	f.regions[method] = append(f.regions[method], region)
	if ff, ok := f.failFrom[method]; ok && f.calls[method] > ff.n {
		writeErr(ff.err)
		return
	}
	if e := f.failing[method]; e != nil {
		writeErr(e)
		return
	}
	msg, e := h(raw)
	if e != nil {
		writeErr(e)
		return
	}
	b, err := protojson.Marshal(msg)
	if err != nil {
		f.t.Errorf("marshal %s response: %v", method, err)
		writeErr(&rpcErr{status: 500, code: "internal", msg: err.Error()})
		return
	}
	_, _ = w.Write(b)
}

// reg registers a handler after checking service, method and message types
// against the generated descriptors, so the fake cannot drift from the schema.
func (f *nsFake) reg(service, method string, req func() proto.Message, h func(proto.Message) (proto.Message, *rpcErr)) {
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		f.t.Fatalf("service %s not in schema: %v", service, err)
	}
	m := d.(protoreflect.ServiceDescriptor).Methods().ByName(protoreflect.Name(method))
	if m == nil {
		f.t.Fatalf("method %s/%s not in schema", service, method)
	}
	if got := req().ProtoReflect().Descriptor().FullName(); got != m.Input().FullName() {
		f.t.Fatalf("%s/%s request is %s, schema says %s", service, method, got, m.Input().FullName())
	}
	f.handlers[service+"/"+method] = func(raw []byte) (proto.Message, *rpcErr) {
		in := req()
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := protojson.Unmarshal(raw, in); err != nil {
				return nil, &rpcErr{status: 400, code: "invalid_argument", msg: err.Error()}
			}
		}
		f.reqs[method] = append(f.reqs[method], in)
		return h(in)
	}
}

// lastReq returns the most recent request received for method.
func lastReq[T proto.Message](f *nsFake, method string) T {
	f.mu.Lock()
	defer f.mu.Unlock()
	rs := f.reqs[method]
	if len(rs) == 0 {
		var zero T
		f.t.Fatalf("no %s request recorded", method)
		return zero
	}
	return rs[len(rs)-1].(T)
}

func withSystemLabel(ls []*stdlib.Label) []*stdlib.Label {
	out := append([]*stdlib.Label{}, ls...)
	for _, l := range out {
		if l.GetName() == systemLabelName {
			return out
		}
	}
	return append(out, &stdlib.Label{Name: systemLabelName, Value: systemLabelValue})
}

func empty() proto.Message { return &emptypb.Empty{} }

func (f *nsFake) register() {
	const (
		vault  = "namespace.cloud.vault.v1beta.VaultService"
		tenant = "namespace.cloud.iam.v1beta.TenantService"
		token  = "namespace.cloud.iam.v1beta.TokenService"
		comp   = "namespace.cloud.compute.v1beta.ComputeService"
		prof   = "namespace.cloud.github.v1beta.ProfileService"
		egr    = "namespace.cloud.network.v1beta.EgressPolicyService"
		queue  = "namespace.cloud.buildkite.QueueService"
		regsvc = "namespace.cloud.registry.v1beta.ContainerRegistryService"
		image  = "namespace.cloud.image.v1beta.ImageService"
	)

	// ---- vault ----
	f.reg(vault, "CreateObject", func() proto.Message { return &vaultv1.CreateObjectRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*vaultv1.CreateObjectRequest)
		o := &nsFakeVault{value: in.GetValue(), revealable: in.GetRevealable(), ver: 1, md: &vaultv1.VaultObjectMetadata{
			ObjectId: f.nextID("obj"), Description: in.GetDescription(), Version: "v1", CreatedAt: f.ts(),
			Labels: withSystemLabel(in.GetLabels()),
		}}
		f.vault[o.md.GetObjectId()] = o
		return o.md, nil
	})
	f.reg(vault, "DescribeObject", func() proto.Message { return &vaultv1.DescribeObjectRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		o := f.vault[m.(*vaultv1.DescribeObjectRequest).GetObjectId()]
		if o == nil {
			return nil, errNotFound("object not found")
		}
		res := &vaultv1.DescribeObjectResponse{Metadata: o.md}
		if o.revealable {
			res.Value = o.value
		}
		return res, nil
	})
	f.reg(vault, "UpdateObject", func() proto.Message { return &vaultv1.UpdateObjectRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*vaultv1.UpdateObjectRequest)
		o := f.vault[in.GetObjectId()]
		if o == nil {
			return nil, errNotFound("object not found")
		}
		if in.GetIfVersionMatches() != "" && in.GetIfVersionMatches() != o.md.GetVersion() {
			return nil, errConflict("version %s does not match %s", in.GetIfVersionMatches(), o.md.GetVersion())
		}
		o.ver++
		o.value = in.GetNewValue()
		o.md = proto.Clone(o.md).(*vaultv1.VaultObjectMetadata)
		o.md.Version = fmt.Sprintf("v%d", o.ver)
		return o.md, nil
	})
	f.reg(vault, "DeleteObject", func() proto.Message { return &vaultv1.DeleteObjectRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		id := m.(*vaultv1.DeleteObjectRequest).GetObjectId()
		if f.vault[id] == nil {
			return nil, errNotFound("object not found")
		}
		delete(f.vault, id)
		return empty(), nil
	})

	// ---- tenants ----
	f.reg(tenant, "CreateTenant", func() proto.Message { return &iamv1.CreateTenantRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*iamv1.CreateTenantRequest)
		// creatorId is deprecated and not echoed.
		t := &iamv1.Tenant{Id: f.nextID("tenant"), ExternalAccountId: in.GetExternalAccountId(), CreatedAt: f.ts(),
			VisibleName: in.GetVisibleName(), Labels: withSystemLabel(in.GetLabels())}
		f.tenants = append(f.tenants, t)
		f.tenantPols[t.GetId()] = in.GetPolicies()
		return &iamv1.TenantResponse{Tenant: t}, nil
	})
	f.reg(tenant, "UpdateTenant", func() proto.Message { return &iamv1.UpdateTenantRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*iamv1.UpdateTenantRequest)
		for _, t := range f.tenants {
			if t.GetId() == in.GetTenantId() {
				t.VisibleName = in.GetVisibleName()
				t.Labels = withSystemLabel(in.GetLabels())
				f.tenantPols[t.GetId()] = in.GetPolicies()
				return &iamv1.TenantResponse{Tenant: t}, nil
			}
		}
		return nil, errNotFound("tenant not found")
	})
	f.reg(tenant, "RemoveTenant", func() proto.Message { return &iamv1.TenantReference{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		id := m.(*iamv1.TenantReference).GetTenantId()
		for i, t := range f.tenants {
			if t.GetId() == id {
				f.tenants = append(f.tenants[:i], f.tenants[i+1:]...)
				return empty(), nil
			}
		}
		return nil, errNotFound("tenant not found")
	})
	f.reg(tenant, "ListTenants", func() proto.Message { return &iamv1.ListTenantsRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*iamv1.ListTenantsRequest)
		var matching []*iamv1.Tenant
		for _, t := range f.tenants {
			if in.GetExternalAccountId() != "" && t.GetExternalAccountId() != in.GetExternalAccountId() {
				continue
			}
			matching = append(matching, t)
		}
		start := 0
		if in.GetCursor() != "" {
			fmt.Sscanf(in.GetCursor(), "c%d", &start)
		}
		end := start + f.pageSize
		res := &iamv1.ListTenantsResponse{}
		if end < len(matching) {
			res.Cursor = fmt.Sprintf("c%d", end)
		} else {
			end = len(matching)
		}
		if start > end {
			start = end
		}
		res.Tenants = matching[start:end]
		return res, nil
	})

	// ---- tenant groups ----
	f.reg(tenant, "EnsureTenantGroup", func() proto.Message { return &iamv1.EnsureTenantGroupRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*iamv1.EnsureTenantGroupRequest)
		g := f.groups[in.GetName()]
		if g == nil {
			g = &iamv1.DescribeTenantGroupResponse{Name: in.GetName()}
			f.groups[in.GetName()] = g
		}
		g.Policies = in.GetPolicies()
		return &iamv1.TenantGroupResponse{Name: in.GetName()}, nil
	})
	f.reg(tenant, "DescribeTenantGroup", func() proto.Message { return &iamv1.TenantGroupReference{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		g := f.groups[m.(*iamv1.TenantGroupReference).GetName()]
		if g == nil {
			return nil, errNotFound("tenant group not found")
		}
		return g, nil
	})
	f.reg(tenant, "DeleteTenantGroup", func() proto.Message { return &iamv1.TenantGroupReference{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		n := m.(*iamv1.TenantGroupReference).GetName()
		if f.groups[n] == nil {
			return nil, errNotFound("tenant group not found")
		}
		delete(f.groups, n)
		return empty(), nil
	})
	f.reg(tenant, "ListTenantGroups", func() proto.Message { return &iamv1.ListTenantGroupsRequest{} }, func(proto.Message) (proto.Message, *rpcErr) {
		res := &iamv1.ListTenantGroupsResponse{}
		for _, g := range f.groups {
			res.TenantGroups = append(res.TenantGroups, &iamv1.ListTenantGroupsResponse_TenantGroup{Name: g.GetName(), Policies: g.GetPolicies()})
		}
		return res, nil
	})

	// ---- revokable tokens ----
	f.reg(token, "CreateRevokableToken", func() proto.Message { return &iamv1.CreateRevokableTokenRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*iamv1.CreateRevokableTokenRequest)
		for _, t := range f.tokens {
			if t.tok.GetName() == in.GetName() && t.tok.GetState() == iamv1.RevokableToken_ACTIVE {
				return nil, errExists("token name %q is taken", in.GetName())
			}
		}
		exp := in.GetExpiresAt()
		if exp == nil {
			exp = timestamppb.New(f.now.Add(90 * 24 * time.Hour))
		}
		scope := in.GetScope()
		if scope == iamv1.RevokableToken_SCOPE_UNKNOWN {
			scope = iamv1.RevokableToken_TENANT_SCOPE // server default
		}
		tk := &iamv1.RevokableToken{TokenId: f.nextID("tok"), TenantId: "tenant-self", Name: in.GetName(),
			Description: in.GetDescription(), CreatedAt: f.ts(), ExpiresAt: exp, Scope: scope,
			Permissions: in.GetAccess().GetGrants(), State: iamv1.RevokableToken_ACTIVE}
		ft := &nsFakeToken{tok: tk, bearer: "nsct_" + tk.GetTokenId()}
		f.tokens = append(f.tokens, ft)
		return &iamv1.CreateRevokableTokenResponse{Token: tk, BearerToken: ft.bearer}, nil
	})
	f.reg(token, "ListRevokableTokens", func() proto.Message { return &iamv1.ListRevokableTokensRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*iamv1.ListRevokableTokensRequest)
		var matching []*iamv1.RevokableToken
		for _, t := range f.tokens {
			if t.tok.GetState() == iamv1.RevokableToken_REVOKED && !in.GetIncludeRevoked() {
				continue
			}
			matching = append(matching, t.tok)
		}
		start := 0
		if len(in.GetPaginationCursor()) > 0 {
			fmt.Sscanf(string(in.GetPaginationCursor()), "c%d", &start)
		}
		end := start + f.pageSize
		res := &iamv1.ListRevokableTokensResponse{}
		if end < len(matching) {
			res.PaginationCursor = []byte(fmt.Sprintf("c%d", end))
		} else {
			end = len(matching)
		}
		res.Tokens = matching[start:end]
		return res, nil
	})
	f.reg(token, "RefreshRevokableToken", func() proto.Message { return &iamv1.RefreshRevokableTokenRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*iamv1.RefreshRevokableTokenRequest)
		for _, t := range f.tokens {
			if t.tok.GetTokenId() == in.GetTokenId() {
				if t.tok.GetState() != iamv1.RevokableToken_ACTIVE {
					return nil, errConflict("token is %s", t.tok.GetState())
				}
				d := 24 * time.Hour
				if in.GetMinimumDuration() != nil {
					d = in.GetMinimumDuration().AsDuration()
				}
				t.tok.ExpiresAt = timestamppb.New(t.tok.GetExpiresAt().AsTime().Add(d))
				return &iamv1.RefreshRevokableTokenResponse{Token: t.tok}, nil
			}
		}
		return nil, errNotFound("token not found")
	})
	f.reg(token, "RevokeRevokableToken", func() proto.Message { return &iamv1.RevokeRevokableTokenRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		id := m.(*iamv1.RevokeRevokableTokenRequest).GetTokenId()
		for _, t := range f.tokens {
			if t.tok.GetTokenId() == id {
				t.tok.State = iamv1.RevokableToken_REVOKED
				t.tok.RevokedAt = f.ts()
				return empty(), nil
			}
		}
		return nil, errNotFound("token not found")
	})

	// ---- instances ----
	regionOf := func() string {
		rs := f.regions["CreateInstance"]
		if len(rs) == 0 {
			return ""
		}
		return rs[len(rs)-1]
	}
	f.reg(comp, "CreateInstance", func() proto.Message { return &computev1.CreateInstanceRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*computev1.CreateInstanceRequest)
		shape := proto.Clone(in.GetShape()).(*computev1.InstanceShape)
		if shape.GetMachineArch() == "" {
			shape.MachineArch = "amd64" // server default
		}
		if shape.GetOs() == "" {
			shape.Os = "linux"
		}
		dl := in.GetDeadline()
		if dl == nil {
			dl = timestamppb.New(f.now.Add(time.Hour)) // server default
		}
		id := f.nextID("inst")
		md := &computev1.InstanceMetadata{InstanceId: id, CreatedAt: f.ts(), Deadline: dl, DocumentedPurpose: in.GetDocumentedPurpose(),
			Shape: shape, Status: computev1.InstanceMetadata_RUNNING, IngressDomain: id + ".example.test",
			Labels: withSystemLabel(in.GetLabels())}
		res := &computev1.DescribeInstanceResponse{InstanceUrl: "https://cloud.test/instances/" + id, Metadata: md}
		for i, c := range in.GetContainers() {
			res.Containers = append(res.Containers, &computev1.AllocatedContainer{Id: fmt.Sprintf("c%d", i), Name: c.GetName()})
		}
		fi := &nsFakeInstance{res: res, region: regionOf()}
		f.instances[id] = fi
		if f.bootDescribes > 0 {
			md.Status = computev1.InstanceMetadata_CREATING
			fi.describesUntilRunning = f.bootDescribes
		} else {
			md.ReadyAt = f.ts()
		}
		return res, nil
	})
	inRegion := func(id, method string) (*nsFakeInstance, *rpcErr) {
		rs := f.regions[method]
		region := ""
		if len(rs) > 0 {
			region = rs[len(rs)-1]
		}
		fi := f.instances[id]
		if fi == nil || fi.region != region {
			return nil, errNotFound("instance %s not found", id)
		}
		return fi, nil
	}
	f.reg(comp, "DescribeInstance", func() proto.Message { return &computev1.DescribeInstanceRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		fi, e := inRegion(m.(*computev1.DescribeInstanceRequest).GetInstanceId(), "DescribeInstance")
		if e != nil {
			return nil, e
		}
		if fi.describesUntilRunning > 0 {
			fi.describesUntilRunning--
			if fi.describesUntilRunning == 0 {
				fi.res.Metadata.Status = computev1.InstanceMetadata_RUNNING
				fi.res.Metadata.ReadyAt = f.ts()
			}
		}
		return fi.res, nil
	})
	f.reg(comp, "ExtendInstance", func() proto.Message { return &computev1.ExtendInstanceRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*computev1.ExtendInstanceRequest)
		fi, e := inRegion(in.GetInstanceId(), "ExtendInstance")
		if e != nil {
			return nil, e
		}
		fi.res.Metadata.Deadline = in.GetNewDeadline()
		return &computev1.ExtendInstanceResponse{NewDeadline: in.GetNewDeadline()}, nil
	})
	f.reg(comp, "DestroyInstance", func() proto.Message { return &computev1.DestroyInstanceRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		fi, e := inRegion(m.(*computev1.DestroyInstanceRequest).GetInstanceId(), "DestroyInstance")
		if e != nil {
			return nil, e
		}
		fi.res.Metadata.Status = computev1.InstanceMetadata_DESTROYED
		fi.res.Metadata.DestroyedAt = f.ts()
		return empty(), nil
	})
	f.reg(comp, "ListInstances", func() proto.Message { return &computev1.ListInstancesRequest{} }, func(proto.Message) (proto.Message, *rpcErr) {
		res := &computev1.ListInstancesResponse{}
		for _, fi := range f.instances {
			res.Instances = append(res.Instances, fi.res.GetMetadata())
		}
		return res, nil
	})

	// ---- github runner profiles ----
	f.reg(prof, "CreateProfile", func() proto.Message { return &githubv1.CreateProfileRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*githubv1.CreateProfileRequest)
		for _, p := range f.profiles {
			if p.GetSpec().GetTag() == in.GetSpec().GetTag() {
				return nil, errExists("profile tag %q is taken", in.GetSpec().GetTag())
			}
		}
		p := &githubv1.RunnerProfileWithStatus{ProfileId: f.nextID("prof"), Spec: in.GetSpec(), Version: 1, CreatedAt: f.ts(), UpdatedAt: f.ts()}
		f.profiles = append(f.profiles, p)
		return &githubv1.CreateProfileResponse{Profile: p}, nil
	})
	f.reg(prof, "ListProfiles", func() proto.Message { return &emptypb.Empty{} }, func(proto.Message) (proto.Message, *rpcErr) {
		return &githubv1.ListProfilesResponse{Profiles: f.profiles}, nil
	})
	f.reg(prof, "UpdateProfile", func() proto.Message { return &githubv1.UpdateProfileRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*githubv1.UpdateProfileRequest)
		for _, p := range f.profiles {
			if p.GetProfileId() == in.GetProfileId() {
				if in.GetUpdateVersion() != p.GetVersion() {
					return nil, errConflict("profile version %d does not match %d", in.GetUpdateVersion(), p.GetVersion())
				}
				p.Spec = in.GetSpec()
				p.Version++
				p.UpdatedAt = f.ts()
				return &githubv1.UpdateProfileResponse{Profile: p}, nil
			}
		}
		return nil, errNotFound("profile not found")
	})
	f.reg(prof, "DeleteProfile", func() proto.Message { return &githubv1.DeleteProfileRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		id := m.(*githubv1.DeleteProfileRequest).GetProfileId()
		for i, p := range f.profiles {
			if p.GetProfileId() == id {
				f.profiles = append(f.profiles[:i], f.profiles[i+1:]...)
				return empty(), nil
			}
		}
		return nil, errNotFound("profile not found")
	})
	f.reg(prof, "RebuildProfileImage", func() proto.Message { return &githubv1.RebuildProfileImageRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		id := m.(*githubv1.RebuildProfileImageRequest).GetProfileId()
		for _, p := range f.profiles {
			if p.GetProfileId() == id {
				f.rebuilds[id]++
				return &githubv1.RebuildProfileImageResponse{}, nil
			}
		}
		return nil, errNotFound("profile not found")
	})

	// ---- egress policies ----
	f.reg(egr, "CreateEgressPolicy", func() proto.Message { return &networkv1.CreateEgressPolicyRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		p := m.(*networkv1.CreateEgressPolicyRequest).GetPolicy()
		if f.egress[p.GetTag()] != nil {
			return nil, errExists("egress policy %q exists", p.GetTag())
		}
		f.egress[p.GetTag()] = &nsFakeEgress{pol: p, rev: 1}
		return &networkv1.CreateEgressPolicyResponse{Revision: 1}, nil
	})
	f.reg(egr, "GetEgressPolicy", func() proto.Message { return &networkv1.GetEgressPolicyRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		e := f.egress[m.(*networkv1.GetEgressPolicyRequest).GetTag()]
		if e == nil {
			return nil, errNotFound("egress policy not found")
		}
		return &networkv1.GetEgressPolicyResponse{Policy: redactEgress(e.pol), Revision: e.rev}, nil
	})
	f.reg(egr, "UpdateEgressPolicy", func() proto.Message { return &networkv1.UpdateEgressPolicyRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*networkv1.UpdateEgressPolicyRequest)
		e := f.egress[in.GetPolicy().GetTag()]
		if e == nil {
			return nil, errNotFound("egress policy not found")
		}
		if in.GetMatchRevision() != e.rev {
			return nil, errConflict("revision %d does not match %d", in.GetMatchRevision(), e.rev)
		}
		e.pol = in.GetPolicy()
		e.rev++
		return &networkv1.UpdateEgressPolicyResponse{Revision: e.rev}, nil
	})

	// ---- buildkite queues ----
	f.reg(queue, "ListQueues", func() proto.Message { return &buildkitev1.ListQueuesRequest{} }, func(proto.Message) (proto.Message, *rpcErr) {
		return &buildkitev1.ListQueuesResponse{Queues: f.queues}, nil
	})
	f.reg(queue, "GetQueue", func() proto.Message { return &buildkitev1.GetQueueRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		for _, q := range f.queues {
			if q.GetId() == m.(*buildkitev1.GetQueueRequest).GetQueueId() {
				return &buildkitev1.QueueResponse{Queue: q}, nil
			}
		}
		return nil, errNotFound("queue not found")
	})
	f.reg(queue, "UpdateQueue", func() proto.Message { return &buildkitev1.UpdateQueueRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*buildkitev1.UpdateQueueRequest)
		for _, q := range f.queues {
			if q.GetId() == in.GetQueueId() {
				q.Settings = in.GetSettings() // replaces the whole settings object
				return &buildkitev1.QueueResponse{Queue: q}, nil
			}
		}
		return nil, errNotFound("queue not found")
	})

	// ---- registry policies ----
	f.reg(regsvc, "GetDefaultPolicy", func() proto.Message { return &emptypb.Empty{} }, func(proto.Message) (proto.Message, *rpcErr) {
		p := f.regDefault
		res := &registryv1.GetDefaultPolicyResponse{Revision: p.rev, ExpirationEnforcement: p.enf}
		if p.secs > 0 {
			res.DefaultExpiration = durationpb.New(time.Duration(p.secs) * time.Second)
		}
		return res, nil
	})
	f.reg(regsvc, "UpdateDefaultPolicy", func() proto.Message { return &registryv1.UpdateDefaultPolicyRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*registryv1.UpdateDefaultPolicyRequest)
		if in.GetRevision() != f.regDefault.rev {
			return nil, errConflict("revision %d does not match %d", in.GetRevision(), f.regDefault.rev)
		}
		// The update replaces the whole policy: an omitted field is cleared.
		f.regDefault = nsFakeRegPolicy{rev: f.regDefault.rev + 1, secs: in.GetDefaultExpiration().GetSeconds(), enf: in.GetExpirationEnforcement()}
		return empty(), nil
	})
	f.reg(regsvc, "GetRepositoryPolicy", func() proto.Message { return &registryv1.GetRepositoryPolicyRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		p := f.repos[m.(*registryv1.GetRepositoryPolicyRequest).GetRepository()]
		if p == nil {
			return nil, errNotFound("repository not found")
		}
		res := &registryv1.GetRepositoryPolicyResponse{Revision: p.rev, ExpirationEnforcement: p.enf}
		if p.secs > 0 {
			res.Expiration = durationpb.New(time.Duration(p.secs) * time.Second)
		}
		return res, nil
	})
	f.reg(regsvc, "UpdateRepositoryPolicy", func() proto.Message { return &registryv1.UpdateRepositoryPolicyRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*registryv1.UpdateRepositoryPolicyRequest)
		p := f.repos[in.GetRepository()]
		if p == nil {
			return nil, errNotFound("repository not found")
		}
		if in.GetRevision() != p.rev {
			return nil, errConflict("revision %d does not match %d", in.GetRevision(), p.rev)
		}
		*p = nsFakeRegPolicy{rev: p.rev + 1, secs: in.GetExpiration().GetSeconds(), enf: in.GetExpirationEnforcement()}
		return empty(), nil
	})

	// ---- image blueprints ----
	f.reg(image, "CreateBlueprint", func() proto.Message { return &imagev1.CreateBlueprintRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*imagev1.CreateBlueprintRequest)
		f.blueprintVer[in.GetBlueprintName()]++
		v := f.blueprintVer[in.GetBlueprintName()]
		// An upsert: a new version replaces the current one under the same name.
		b := &imagev1.ImageBlueprint{Id: fmt.Sprintf("bp-%s-v%d", in.GetBlueprintName(), v), Name: in.GetBlueprintName(),
			CreatedAt: f.ts(), Spec: in.GetSpec(), Status: &imagev1.ImageBlueprint_Status{ImageRef: fmt.Sprintf("registry.test/%s:v%d", in.GetBlueprintName(), v)}}
		f.blueprints[in.GetBlueprintName()] = b
		return &imagev1.CreateBlueprintResponse{Blueprint: b}, nil
	})
	f.reg(image, "FetchBlueprint", func() proto.Message { return &imagev1.FetchBlueprintRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		b := f.blueprints[m.(*imagev1.FetchBlueprintRequest).GetBlueprintName()]
		if b == nil {
			return nil, errNotFound("blueprint not found")
		}
		return &imagev1.FetchBlueprintResponse{Blueprint: b}, nil
	})
	f.reg(image, "RemoveBlueprint", func() proto.Message { return &imagev1.RemoveBlueprintRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		n := m.(*imagev1.RemoveBlueprintRequest).GetBlueprintName()
		if f.blueprints[n] == nil {
			return nil, errNotFound("blueprint not found")
		}
		delete(f.blueprints, n)
		return empty(), nil
	})
	f.reg(image, "ListBlueprints", func() proto.Message { return &imagev1.ListBlueprintsRequest{} }, func(proto.Message) (proto.Message, *rpcErr) {
		res := &imagev1.ListBlueprintsResponse{}
		for _, b := range f.blueprints {
			res.Blueprints = append(res.Blueprints, b)
		}
		return res, nil
	})

	// ---- vault list ----
	f.reg(vault, "ListObjects", func() proto.Message { return &vaultv1.ListObjectsRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*vaultv1.ListObjectsRequest)
		var ids []string
		for id := range f.vault {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		res := &vaultv1.ListObjectsResponse{}
	objects:
		for _, id := range ids {
			md := f.vault[id].md
			for _, want := range in.GetLabelFilter() {
				ok := false
				for _, l := range md.GetLabels() {
					ok = ok || (l.GetName() == want.GetName() && l.GetValue() == want.GetValue())
				}
				if !ok {
					continue objects
				}
			}
			res.Objects = append(res.Objects, md)
		}
		return res, nil
	})

	// ---- egress list ----
	f.reg(egr, "ListEgressPolicies", func() proto.Message { return &networkv1.ListEgressPoliciesRequest{} }, func(proto.Message) (proto.Message, *rpcErr) {
		var tags []string
		for tag := range f.egress {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		res := &networkv1.ListEgressPoliciesResponse{}
		for _, tag := range tags {
			e := f.egress[tag]
			res.Policies = append(res.Policies, &networkv1.ListEgressPoliciesResponse_EgressPolicyEntry{Policy: redactEgress(e.pol), Revision: e.rev})
		}
		return res, nil
	})

	// ---- tenant members and the caller's own policies ----
	const membership = "namespace.cloud.iam.v1beta.TenantMembershipService"
	f.reg(membership, "ListTenantMembers", func() proto.Message { return &iamv1.ListTenantMembersRequest{} }, func(proto.Message) (proto.Message, *rpcErr) {
		return &iamv1.ListTenantMembersResponse{Members: f.members}, nil
	})
	f.reg(tenant, "DescribePolicies", func() proto.Message { return &iamv1.DescribePoliciesRequest{} }, func(proto.Message) (proto.Message, *rpcErr) {
		return &iamv1.DescribePoliciesResponse{Policies: f.callerPols, Revision: 7}, nil
	})

	// ---- shared images ----
	f.reg(regsvc, "ShareImage", func() proto.Message { return &registryv1.ShareImageRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*registryv1.ShareImageRequest)
		if in.GetVisibility() == registryv1.Visibility_UNKNOWN_VISIBILITY {
			return nil, &rpcErr{status: 400, code: "invalid_argument", msg: "visibility is required"}
		}
		id := f.nextID("shared")
		ref := "public.nscr.io/" + id
		if in.GetSuffix() != "" {
			ref += "/" + in.GetSuffix()
		}
		s := &registryv1.SharedImage{Id: id, SharedRepository: in.GetRepository(), SharedDigest: in.GetDigest(),
			ExpiresAt: in.GetExpiresAt(), ImageRef: ref, Visibility: in.GetVisibility(), Suffix: in.GetSuffix()}
		f.sharedImages = append(f.sharedImages, s)
		return s, nil
	})
	f.reg(regsvc, "ListSharedImages", func() proto.Message { return &registryv1.ListSharedImagesRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*registryv1.ListSharedImagesRequest)
		res := &registryv1.ListSharedImagesResponse{}
		for _, s := range f.sharedImages {
			if in.GetMatchVisibility() != registryv1.Visibility_UNKNOWN_VISIBILITY && s.GetVisibility() != in.GetMatchVisibility() {
				continue
			}
			res.SharedImages = append(res.SharedImages, s)
		}
		return res, nil
	})
	f.reg(regsvc, "UnshareImage", func() proto.Message { return &registryv1.UnshareImageRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		id := m.(*registryv1.UnshareImageRequest).GetId()
		for i, s := range f.sharedImages {
			if s.GetId() == id {
				f.sharedImages = append(f.sharedImages[:i], f.sharedImages[i+1:]...)
				return empty(), nil
			}
		}
		return nil, errNotFound("shared image not found")
	})

	// ---- instance ingresses ----
	f.reg(comp, "CreateIngress", func() proto.Message { return &computev1.CreateIngressRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*computev1.CreateIngressRequest)
		if _, e := inRegion(in.GetInstanceId(), "CreateIngress"); e != nil {
			return nil, e
		}
		res := &computev1.CreateIngressResponse{}
		for _, r := range in.GetIngresses() {
			if r.GetExportedPortBackend().GetPort() == 0 {
				return nil, &rpcErr{status: 400, code: "invalid_argument", msg: "port is required"}
			}
			name := r.GetName()
			if name == "" {
				name = "wild"
			}
			if f.ingressRename != nil {
				name = f.ingressRename(name)
			}
			a := &computev1.AllocatedIngress{Name: name, Fqdn: name + "-" + in.GetInstanceId() + ".example.test",
				Description: fmt.Sprintf("port %d", r.GetExportedPortBackend().GetPort()), IngressId: f.nextID("ing")}
			f.ingresses[in.GetInstanceId()] = append(f.ingresses[in.GetInstanceId()], a)
			res.AllocatedIngresses = append(res.AllocatedIngresses, a)
		}
		if f.ingressHideResponse {
			res.AllocatedIngresses = nil
		}
		return res, nil
	})
	f.reg(comp, "ListIngresses", func() proto.Message { return &computev1.ListIngressesRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*computev1.ListIngressesRequest)
		if _, e := inRegion(in.GetInstanceId(), "ListIngresses"); e != nil {
			return nil, e
		}
		return &computev1.ListIngressesResponse{AllocatedIngresses: f.ingresses[in.GetInstanceId()]}, nil
	})
	f.reg(comp, "DeleteIngress", func() proto.Message { return &computev1.DeleteIngressRequest{} }, func(m proto.Message) (proto.Message, *rpcErr) {
		in := m.(*computev1.DeleteIngressRequest)
		if _, e := inRegion(in.GetInstanceId(), "DeleteIngress"); e != nil {
			return nil, e
		}
		list := f.ingresses[in.GetInstanceId()]
		for i, a := range list {
			if a.GetIngressId() == in.GetIngressId() {
				f.ingresses[in.GetInstanceId()] = append(list[:i], list[i+1:]...)
				return empty(), nil
			}
		}
		return nil, errNotFound("ingress not found")
	})
}

// redactEgress returns the policy as the server reads it back: secret inject
// values are never echoed.
func redactEgress(p *networkv1.EgressPolicy) *networkv1.EgressPolicy {
	raw, _ := protojson.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if spec, ok := m["spec"].(map[string]any); ok {
		if rules, ok := spec["rules"].([]any); ok {
			for _, r := range rules {
				if inj, ok := r.(map[string]any)["inject"].(map[string]any); ok {
					delete(inj, "value")
					if fe, ok := inj["fromEndpoint"].(map[string]any); ok {
						if hs, ok := fe["headers"].([]any); ok {
							for _, h := range hs {
								delete(h.(map[string]any), "value")
							}
						}
					}
				}
			}
		}
	}
	b, _ := json.Marshal(m)
	var out networkv1.EgressPolicy
	_ = protojson.Unmarshal(b, &out)
	return &out
}
