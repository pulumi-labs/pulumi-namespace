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

	buildkitev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/buildkite"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1571 step 3: the audit of every import branch. The earlier import tests diffed the
// imported inputs against themselves, which cannot see a platform-filled value that the
// program never declared. These create a resource from a minimal program, import it by ID
// alone, and diff that program against the imported state, the way the engine does on the
// first preview after `pulumi import`.

// lcImportThenProgram imports id and diffs the program's own inputs against the imported
// state and inputs.
func lcImportThenProgram(t *testing.T, s integration.Server, typ, id string, program property.Map) (pgp.ReadResponse, pgp.DiffResponse) {
	t.Helper()
	imp := lcImport(t, s, typ, id)
	d, err := s.Diff(pgp.DiffRequest{Urn: urn(typ, "r"), ID: id, State: imp.Properties, Inputs: program, OldInputs: imp.Inputs})
	require.NoError(t, err)
	return imp, d
}

// The server attaches a system label to instances, tenants and vault objects that the
// program never declared. Instance and VaultObject labels replace on change, so importing
// the system label as an input would destroy and recreate the resource (a running instance,
// a stored secret) on the first preview.
func TestImport_SystemLabelsStayOutOfInputs(t *testing.T) {
	s := lcServer(t, newNSFake(t), "eu")
	cases := []struct {
		typ     string
		program property.Map
	}{
		{"Instance", pm(map[string]any{"shape": map[string]any{"virtualCpu": 1, "memoryMegabytes": 1024}, "deadline": "2026-11-01T14:00:00Z"})},
		{"VaultObject", lcSecret(pm(map[string]any{}), "value", "v1")},
		{"Tenant", pm(map[string]any{"visibleName": "Plain"})},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			created := lcCreate(t, s, c.typ, c.program)
			imp, d := lcImportThenProgram(t, s, c.typ, created.ID, c.program)
			assert.False(t, lcHas(imp.Inputs, "labels"), "the system label must not become an input: %v", imp.Inputs)
			if c.typ == "VaultObject" {
				// A non-revealable value cannot be read back, so the value itself shows as an
				// update (documented); the labels must not, they would replace the secret.
				assert.NotContains(t, d.DetailedDiff, "labels")
				for k, v := range d.DetailedDiff {
					assert.Equal(t, pgp.Update, v.Kind, "%s must not replace the secret", k)
				}
				return
			}
			assert.False(t, d.HasChanges, "importing %s must not plan a change against the program that created it: %+v", c.typ, d.DetailedDiff)
		})
	}
}

// A user label survives an import; only the platform's own labels are dropped.
func TestImport_KeepsUserLabelsDropsSystemLabels(t *testing.T) {
	s := lcServer(t, newNSFake(t), "eu")
	program := pm(map[string]any{"shape": map[string]any{"virtualCpu": 1, "memoryMegabytes": 1024}, "deadline": "2026-11-01T14:00:00Z", "labels": map[string]any{"env": "ci"}})
	created := lcCreate(t, s, "Instance", program)
	imp, d := lcImportThenProgram(t, s, "Instance", created.ID, program)
	assert.Equal(t, map[string]property.Value{"env": property.New("ci")}, lcMapOf(imp.Inputs, "labels").AsMap())
	assert.False(t, d.HasChanges, "%+v", d.DetailedDiff)
}

// Every other resource whose Read has an import branch: create from a minimal program, import
// by ID alone, and diff the program against the imported state. Fields the server fills in
// (token scope, enforcement defaults, the system label) must not turn into a change.
func TestImport_ThenProgramPlansNoChange(t *testing.T) {
	grant := map[string]any{"resourceType": "instance", "resourceId": "", "actions": []any{"list"}}
	cases := []struct {
		typ     string
		program property.Map
	}{
		{typ: "RevokableToken", program: pm(map[string]any{"name": "ci", "description": "d", "grants": []any{grant}})},
		{typ: "TenantGroup", program: pm(map[string]any{"name": "gold", "policies": []any{map[string]any{"policy": "quota", "value": "10"}}})},
		{typ: "EgressPolicy", program: pm(map[string]any{"tag": "open", "mode": "BLOCK", "description": "d",
			"rules": []any{map[string]any{"op": "ALLOW", "matcher": map[string]any{"matchDomains": []any{"example.com"}}}}})},
		{typ: "Blueprint", program: pm(map[string]any{"name": "base", "dockerfileContent": "FROM alpine"})},
		{typ: "RegistryDefaultPolicy", program: pm(map[string]any{"defaultExpirationSeconds": 3600})},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			f := newNSFake(t)
			s := lcServer(t, f, "eu")
			created := lcCreate(t, s, c.typ, c.program)
			imp, d := lcImportThenProgram(t, s, c.typ, created.ID, c.program)
			assert.False(t, d.HasChanges, "importing %s must not plan a change against the program that created it: %+v (imported inputs %v)", c.typ, d.DetailedDiff, imp.Inputs)
		})
	}
}

// The server redacts the secret inject value of an egress rule, so an imported policy cannot
// carry it: the program that declares it plans one in-place update, never a replacement.
func TestImport_EgressPolicySecretInjectIsAnUpdateNotAReplace(t *testing.T) {
	s := lcServer(t, newNSFake(t), "eu")
	program := lcEgressInputs()
	created := lcCreate(t, s, "EgressPolicy", program)
	_, d := lcImportThenProgram(t, s, "EgressPolicy", created.ID, program)
	require.True(t, d.HasChanges)
	for k, v := range d.DetailedDiff {
		assert.Equal(t, pgp.Update, v.Kind, "%s must update in place", k)
	}
}

// BuildkiteQueue adopts an existing queue, so an import by ID reads the queue's settings.
func TestImport_BuildkiteQueueThenProgramPlansNoChange(t *testing.T) {
	f := newNSFake(t)
	f.with(func() {
		f.queues = []*buildkitev1.BuildkiteQueue{{Id: "q-1", QueueName: "ci", Cluster: "c1",
			Settings: &buildkitev1.QueueSettings{EgressPolicyTag: "old"}}}
	})
	s := lcServer(t, f, "eu")
	program := pm(map[string]any{"queueName": "ci", "egressPolicyTag": "locked",
		"permissions": map[string]any{"permissionsType": "DEFAULT"}})
	created := lcCreate(t, s, "BuildkiteQueue", program)
	_, d := lcImportThenProgram(t, s, "BuildkiteQueue", created.ID, program)
	assert.False(t, d.HasChanges, "%+v", d.DetailedDiff)
}
