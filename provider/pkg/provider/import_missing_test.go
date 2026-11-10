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
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/stretchr/testify/require"
)

// An import of an ID that does not exist must fail instead of storing an empty resource
// (#1715). A refresh of an object deleted outside Pulumi (prior state present) still drops
// it; the revokable token lifecycle test covers that side.
func TestImportOfMissingObjectFails(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	ids := map[string]string{
		"Blueprint":                "nope",
		"BuildkiteQueue":           "nope",
		"EgressPolicy":             "nope",
		"GithubProfile":            "nope",
		"Instance":                 "nope",
		"InstanceIngress":          "nope/nope",
		"RegistryRepositoryPolicy": "nope",
		"RevokableToken":           "nope",
		"SharedImage":              "nope",
		"Tenant":                   "nope",
		"TenantGroup":              "nope",
		"VaultObject":              "nope",
	}
	for typ, id := range ids {
		_, err := s.Read(pgp.ReadRequest{ID: id, Urn: urn(typ, "gone")})
		require.Error(t, err, typ)
		require.Contains(t, err.Error(), "does not exist", typ)
	}
}
