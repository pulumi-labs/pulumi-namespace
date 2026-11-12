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
	"strings"
	"testing"
)

// The live API answers the tenant RPCs with a placeholder message
// (pulumi-labs/pulumi-namespace#12 and #13). The error must say what that
// usually means instead of leaving only "unspecified message".
func TestParseAPIError_TenantUnspecifiedInvalidArgumentGetsHint(t *testing.T) {
	for _, method := range []string{"CreateTenant", "ListTenants", "ListTenantGroups"} {
		e := parseAPIError(method, 400, []byte(`{"code":"invalid_argument","message":"unspecified message (rid=abc123)"}`))
		got := e.Error()
		for _, want := range []string{"rid=abc123", "(HTTP 400)", "not enabled for tenant management", method} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: error %q does not contain %q", method, got, want)
			}
		}
	}
}

func TestParseAPIError_EveryTenantMethodGetsHint(t *testing.T) {
	for method := range tenantMethods {
		got := parseAPIError(method, 400, []byte(`{"code":"invalid_argument"}`)).Error()
		if !strings.Contains(got, "tenant management") {
			t.Errorf("%s: error %q has no tenant hint", method, got)
		}
	}
}

func TestParseAPIError_EmptyMessageNonTenantGetsGenericHint(t *testing.T) {
	e := parseAPIError("DescribeObject", 400, []byte(`{"code":"invalid_argument"}`))
	got := e.Error()
	if !strings.Contains(got, "no reason") || strings.Contains(got, "tenant management") {
		t.Errorf("error %q: want the generic no-reason hint without tenant guidance", got)
	}
}

func TestParseAPIError_RealMessageGetsNoHint(t *testing.T) {
	e := parseAPIError("CreateTenant", 400, []byte(`{"code":"invalid_argument","message":"visible_name too long"}`))
	if e.Hint != "" || strings.Contains(e.Error(), "tenant management") {
		t.Errorf("a message that names the problem must not get a hint: %q", e.Error())
	}
}

func TestParseAPIError_SurfacesConnectDetails(t *testing.T) {
	body := `{"code":"invalid_argument","message":"bad","details":[{"type":"google.rpc.BadRequest","value":"AAA","debug":{"fieldViolations":[{"field":"visible_name"}]}},{"type":"x.Empty"}]}`
	got := parseAPIError("CreateTenant", 400, []byte(body)).Error()
	for _, want := range []string{"details:", "google.rpc.BadRequest", "fieldViolations", "x.Empty"} {
		if !strings.Contains(got, want) {
			t.Errorf("error %q does not contain %q", got, want)
		}
	}
}

// A 401 from a tenant RPC gets the tenant hint even when the token works elsewhere (#1877).
func TestParseAPIError_TenantUnauthenticatedGetsHint(t *testing.T) {
	for method := range tenantMethods {
		for _, body := range []string{
			`{"code":"unauthenticated","message":"unauthenticated (rid=r1)"}`,
			``,
		} {
			e := parseAPIError(method, 401, []byte(body))
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
	e := parseAPIError("DescribeObject", 401, []byte(`{"code":"unauthenticated","message":"bad token"}`))
	if e.Hint != "" || strings.Contains(e.Error(), "tenant") {
		t.Errorf("non-tenant 401 must not get tenant guidance: %q", e.Error())
	}
}
