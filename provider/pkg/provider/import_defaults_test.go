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
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1571: an import copied platform-filled defaults into inputs, so the first
// preview against the program that never declared them planned a change (a
// replacement for Instance, whose shape replaces). The tests diff the
// program's own inputs against the imported state, which the earlier import
// tests never did.

func TestInstance_ImportKeepsPlatformDefaultsOutOfInputs(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(computeSvc+"DescribeInstance", instanceJSON("RUNNING", "2030-01-01T00:00:00Z"))
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "inst-1", Urn: urn("Instance", "i")})
	require.NoError(t, err)

	shape := read.Inputs.Get("shape").AsMap()
	_, hasArch := shape.GetOk("machineArch")
	_, hasOS := shape.GetOk("os")
	assert.False(t, hasArch, "machineArch is filled in by the platform")
	assert.False(t, hasOS, "os is filled in by the platform")

	// The program declares only cpu and memory: no replacement, no change.
	program := property.NewMap(map[string]property.Value{
		"shape": property.New(property.NewMap(map[string]property.Value{
			"virtualCpu": property.New(2.0), "memoryMegabytes": property.New(4096.0),
		})),
		"documentedPurpose": property.New("ci"),
		"deadline":          property.New("2030-01-01T00:00:00Z"),
	})
	d, err := s.Diff(pgp.DiffRequest{Urn: urn("Instance", "i"), ID: "inst-1", State: read.Properties, Inputs: program, OldInputs: read.Inputs})
	require.NoError(t, err)
	lcNoChange(t, d, "importing an instance must not plan a change against the program that created it")
}

func TestGithubProfile_ImportKeepsPlatformDefaultsOutOfInputs(t *testing.T) {
	f := newFakeAPI(t)
	withDefaults := `{"profileId":"p-1","version":"3","createdAt":"2026-10-01T10:00:00Z","updatedAt":"2026-10-02T10:00:00Z",` +
		`"spec":{"tag":"fast","description":"d","builderMode":"NO_CACHING","instanceShape":{"virtualCpu":8,"memoryMegabytes":16384,"os":"linux","machineArch":"amd64"},` +
		`"networkPolicy":{"egress":"EGRESS_POLICY_TAG","egressPolicyTag":"locked"}}}`
	f.ok(ghProfileSvc+"ListProfiles", `{"profiles":[`+withDefaults+`]}`)
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "p-1", Urn: urn("GithubProfile", "p")})
	require.NoError(t, err)
	_, hasMode := read.Inputs.GetOk("builderMode")
	assert.False(t, hasMode, "builderMode NO_CACHING is the platform default")
	_, hasOS := read.Inputs.Get("instanceShape").AsMap().GetOk("os")
	assert.False(t, hasOS, "instanceShape.os linux is the platform default")

	program := pm(map[string]any{
		"tag": "fast", "description": "d",
		"instanceShape": map[string]any{"virtualCpu": 8, "memoryMegabytes": 16384, "machineArch": "amd64"},
		"networkPolicy": map[string]any{"egress": "EGRESS_POLICY_TAG", "egressPolicyTag": "locked"},
	})
	d, err := s.Diff(pgp.DiffRequest{Urn: urn("GithubProfile", "p"), ID: "p-1", State: read.Properties, Inputs: program, OldInputs: read.Inputs})
	require.NoError(t, err)
	lcNoChange(t, d, "importing a profile must not plan a change against the program that created it")

	// A value that is not the default is kept.
	custom := `{"profileId":"p-1","version":"3","spec":{"tag":"fast","builderMode":"USE_LOCAL_CACHE","instanceShape":{"virtualCpu":8,"memoryMegabytes":16384,"os":"windows"}}}`
	f.ok(ghProfileSvc+"ListProfiles", `{"profiles":[`+custom+`]}`)
	read, err = s.Read(pgp.ReadRequest{ID: "p-1", Urn: urn("GithubProfile", "p")})
	require.NoError(t, err)
	assert.Equal(t, "USE_LOCAL_CACHE", str(read.Inputs, "builderMode"))
	assert.Equal(t, "windows", str(read.Inputs.Get("instanceShape").AsMap(), "os"))
}

// #1677, the inverse of #1571: a program that DECLARES shape.machineArch and
// shape.os with the values the platform reports must plan no change after an
// import, and a different declared value must still replace.
func TestInstance_ImportThenProgramDeclaringPlatformShapePlansNoChange(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(computeSvc+"DescribeInstance", instanceJSON("RUNNING", "2030-01-01T00:00:00Z"))
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "inst-1", Urn: urn("Instance", "i")})
	require.NoError(t, err)
	require.Equal(t, "amd64", read.Properties.Get("effectiveMachineArch").AsString())
	require.Equal(t, "linux", read.Properties.Get("effectiveOs").AsString())

	program := func(arch, osName any) property.Map {
		shape := map[string]property.Value{"virtualCpu": property.New(2.0), "memoryMegabytes": property.New(4096.0)}
		if arch != nil {
			shape["machineArch"] = property.New(arch.(string))
		}
		if osName != nil {
			shape["os"] = property.New(osName.(string))
		}
		return property.NewMap(map[string]property.Value{
			"shape": property.New(property.NewMap(shape)), "documentedPurpose": property.New("ci"),
			"deadline": property.New("2030-01-01T00:00:00Z"),
		})
	}
	diff := func(in property.Map) pgp.DiffResponse {
		d, err := s.Diff(pgp.DiffRequest{Urn: urn("Instance", "i"), ID: "inst-1", State: read.Properties, Inputs: in, OldInputs: read.Inputs})
		require.NoError(t, err)
		return d
	}
	// All four declared-or-not combinations of the program that created it.
	for _, tc := range []struct{ arch, os any }{{nil, nil}, {"amd64", nil}, {nil, "linux"}, {"amd64", "linux"}} {
		lcNoChange(t, diff(program(tc.arch, tc.os)), "declaring the platform's own shape values is no change")
	}
	// A different value still replaces.
	for _, tc := range []struct{ arch, os any }{{"arm64", nil}, {nil, "windows"}, {"amd64", "windows"}} {
		d := diff(program(tc.arch, tc.os))
		require.True(t, d.HasChanges)
		require.Equal(t, pgp.UpdateReplace, d.DetailedDiff["shape"].Kind)
	}
}

// #1691: cacheVolumeSettings[].volumeTag is deprecated, ignored and never sent, and an import cannot read it
// back, so a program that still declares it must plan no change after an import.
func TestGithubProfile_ImportThenProgramWithVolumeTagPlansNoChange(t *testing.T) {
	f := newFakeAPI(t)
	profile := `{"profileId":"p-1","version":"3","spec":{"tag":"fast","instanceShape":{"virtualCpu":2,"memoryMegabytes":4096,"os":"linux","machineArch":"amd64"},` +
		`"cacheVolumeSettings":[{"volumeSizeGb":20}]}}`
	f.ok(ghProfileSvc+"ListProfiles", `{"profiles":[`+profile+`]}`)
	s := newServerFor(t, f)
	read, err := s.Read(pgp.ReadRequest{ID: "p-1", Urn: urn("GithubProfile", "p")})
	require.NoError(t, err)

	program := func(tag, size any) property.Map {
		vol := map[string]any{"volumeSizeGb": size}
		if tag != nil {
			vol["volumeTag"] = tag
		}
		return pm(map[string]any{
			"tag": "fast", "instanceShape": map[string]any{"virtualCpu": 2, "memoryMegabytes": 4096, "machineArch": "amd64"},
			"cacheVolumeSettings": []any{vol},
		})
	}
	diff := func(in property.Map) pgp.DiffResponse {
		d, err := s.Diff(pgp.DiffRequest{Urn: urn("GithubProfile", "p"), ID: "p-1", State: read.Properties, Inputs: in, OldInputs: read.Inputs})
		require.NoError(t, err)
		return d
	}
	lcNoChange(t, diff(program("ignored-tag", 20)), "a declared volumeTag is ignored")
	lcNoChange(t, diff(program(nil, 20)), "omitting volumeTag is clean")
	// Real differences still show.
	require.True(t, diff(program("ignored-tag", 40)).HasChanges)
}
