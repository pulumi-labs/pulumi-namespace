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
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tenantRPC is one RPC the provider actually sends to a tenant service.
type tenantRPC struct{ service, method string }

// tenantRPCs lists every tenant RPC the provider sends. TestTenantRPCsMatchSource
// fails when a c.call to a tenant service is added or removed without updating
// it, so the hint tests cannot drift from the code again (#1877).
var tenantRPCs = []tenantRPC{
	{tenantService, "CreateTenant"}, {tenantService, "UpdateTenant"}, {tenantService, "RemoveTenant"},
	{tenantService, "ListTenants"}, {tenantService, "DescribePolicies"},
	{tenantService, "EnsureTenantGroup"}, {tenantService, "DescribeTenantGroup"},
	{tenantService, "DeleteTenantGroup"}, {tenantService, "ListTenantGroups"},
	{tenantMembershipService, "ListTenantMembers"},
}

// Every c.call whose service argument is a tenant service is in tenantRPCs, and vice versa.
func TestTenantRPCsMatchSource(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 5 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "call" {
					return true
				}
				svc, ok := call.Args[2].(*ast.Ident)
				lit, ok2 := call.Args[3].(*ast.BasicLit)
				if !ok || !ok2 || (svc.Name != "tenantService" && svc.Name != "tenantMembershipService") {
					return true
				}
				m, _ := strconv.Unquote(lit.Value)
				found[svc.Name+"/"+m] = true
				return true
			})
		}
	}
	want := map[string]bool{}
	for _, r := range tenantRPCs {
		name := "tenantService"
		if r.service == tenantMembershipService {
			name = "tenantMembershipService"
		}
		want[name+"/"+r.method] = true
	}
	var missing, extra []string
	for k := range found {
		if !want[k] {
			missing = append(missing, k)
		}
	}
	for k := range want {
		if !found[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("tenantRPCs out of date: sent but not listed %v; listed but not sent %v", missing, extra)
	}
}

// The live API answers the tenant RPCs with a placeholder message
// (pulumi-labs/pulumi-namespace#12 and #13). The error must say what that
// usually means instead of leaving only "unspecified message".
func TestParseAPIError_TenantUnspecifiedInvalidArgumentGetsHint(t *testing.T) {
	for _, rpc := range tenantRPCs {
		method := rpc.method
		e := parseAPIError(rpc.service, method, 400, []byte(`{"code":"invalid_argument","message":"unspecified message (rid=abc123)"}`))
		got := e.Error()
		for _, want := range []string{"rid=abc123", "(HTTP 400)", "not enabled for tenant management", method} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: error %q does not contain %q", method, got, want)
			}
		}
	}
}

func TestParseAPIError_EveryTenantMethodGetsHint(t *testing.T) {
	for _, rpc := range tenantRPCs {
		method := rpc.method
		got := parseAPIError(rpc.service, method, 400, []byte(`{"code":"invalid_argument"}`)).Error()
		if !strings.Contains(got, "tenant management") {
			t.Errorf("%s: error %q has no tenant hint", method, got)
		}
	}
}

func TestParseAPIError_EmptyMessageNonTenantGetsGenericHint(t *testing.T) {
	e := parseAPIError(computeService, "DescribeObject", 400, []byte(`{"code":"invalid_argument"}`))
	got := e.Error()
	if !strings.Contains(got, "no reason") || strings.Contains(got, "tenant management") {
		t.Errorf("error %q: want the generic no-reason hint without tenant guidance", got)
	}
}

func TestParseAPIError_RealMessageGetsNoHint(t *testing.T) {
	e := parseAPIError(tenantService, "CreateTenant", 400, []byte(`{"code":"invalid_argument","message":"visible_name too long"}`))
	if e.Hint != "" || strings.Contains(e.Error(), "tenant management") {
		t.Errorf("a message that names the problem must not get a hint: %q", e.Error())
	}
}

func TestParseAPIError_SurfacesConnectDetails(t *testing.T) {
	body := `{"code":"invalid_argument","message":"bad","details":[{"type":"google.rpc.BadRequest","value":"AAA","debug":{"fieldViolations":[{"field":"visible_name"}]}},{"type":"x.Empty"}]}`
	got := parseAPIError(tenantService, "CreateTenant", 400, []byte(body)).Error()
	for _, want := range []string{"details:", "google.rpc.BadRequest", "fieldViolations", "x.Empty"} {
		if !strings.Contains(got, want) {
			t.Errorf("error %q does not contain %q", got, want)
		}
	}
}

// A 401 from a tenant RPC gets the tenant hint even when the token works elsewhere (#1877).
func TestParseAPIError_TenantUnauthenticatedGetsHint(t *testing.T) {
	for _, rpc := range tenantRPCs {
		method := rpc.method
		for _, body := range []string{
			`{"code":"unauthenticated","message":"unauthenticated (rid=r1)"}`,
			``,
		} {
			e := parseAPIError(rpc.service, method, 401, []byte(body))
			got := e.Error()
			for _, want := range []string{"(HTTP 401)", "tenant management", "tenant scope", "not confirmed"} {
				if !strings.Contains(got, want) {
					t.Errorf("%s: error %q does not contain %q", method, got, want)
				}
			}
			if body != "" && !strings.Contains(got, "rid=r1") {
				t.Errorf("%s: error %q lost the rid", method, got)
			}
		}
	}
}

func TestParseAPIError_NonTenantUnauthenticatedGetsNoHint(t *testing.T) {
	e := parseAPIError(computeService, "DescribeObject", 401, []byte(`{"code":"unauthenticated","message":"bad token"}`))
	if e.Hint != "" || strings.Contains(e.Error(), "tenant") {
		t.Errorf("non-tenant 401 must not get tenant guidance: %q", e.Error())
	}
}

// A tenant method name sent to a non-tenant service must not get tenant guidance:
// the hint follows the service.
func TestParseAPIError_TenantHintFollowsService(t *testing.T) {
	e := parseAPIError(computeService, "ListTenants", 401, []byte(`{"code":"unauthenticated"}`))
	if e.Hint != "" {
		t.Errorf("non-tenant service got hint %q", e.Hint)
	}
	e = parseAPIError(tenantService, "SomeFutureTenantRPC", 401, []byte(`{"code":"unauthenticated"}`))
	if !strings.Contains(e.Error(), "tenant management") {
		t.Errorf("new tenant RPC missed the hint: %q", e.Error())
	}
}

// End to end (#1877): a 401 on the RPC TenantGroup create sends (EnsureTenantGroup)
// names the likely cause through the whole provider path.
func TestLifecycle_TenantGroupUnauthenticatedNamesCause(t *testing.T) {
	for _, method := range []string{"EnsureTenantGroup"} {
		f := newNSFake(t)
		s := lcServer(t, f, "eu")
		f.failWith(method, &rpcErr{status: 401, code: "unauthenticated", msg: "unauthenticated"})
		_, err := s.Create(pgp.CreateRequest{Urn: urn("TenantGroup", "r"), Properties: pm(map[string]any{"name": "gold"})})
		require.Error(t, err, method)
		for _, want := range []string{"tenant management", "tenant scope"} {
			assert.Contains(t, err.Error(), want, method)
		}
	}
}
