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
	"sort"
	"strings"
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A value wired in from another resource is unknown at preview, and infer decodes it to its zero
// value. Check must not judge it (#1832): an InstanceIngress could not be created with
// instanceId: ${instance.instanceId}.
func TestCheck_UnknownInputsAreNotValidated(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	spec := lcSchema(t)
	unknown := property.New(property.Computed)

	var tokens []string
	for tok := range spec.Resources {
		tokens = append(tokens, tok)
	}
	sort.Strings(tokens)
	require.NotEmpty(t, tokens)
	for _, tok := range tokens {
		res := spec.Resources[tok]
		typ := tok[strings.LastIndex(tok, ":")+1:]
		t.Run(typ, func(t *testing.T) {
			// Every input unknown at once.
			all := map[string]property.Value{}
			for name := range res.InputProperties {
				all[name] = unknown
			}
			resp, err := s.Check(pgp.CheckRequest{Urn: urn(typ, "x"), Inputs: property.NewMap(all)})
			require.NoError(t, err)
			assert.Empty(t, resp.Failures, "all inputs unknown")

			// Each input unknown on its own, the rest absent.
			for name := range res.InputProperties {
				resp, err := s.Check(pgp.CheckRequest{Urn: urn(typ, "x"), Inputs: property.NewMap(map[string]property.Value{name: unknown})})
				require.NoError(t, err)
				for _, fl := range resp.Failures {
					assert.NotEqual(t, name, fl.Property, "%s is unknown, so it must not fail", name)
				}
			}
		})
	}
}

// The reported program: an ingress on an Instance of the same program.
func TestInstanceIngress_CheckAcceptsUnknownInstanceId(t *testing.T) {
	f := newNSFake(t)
	s := lcServer(t, f, "eu")
	in := property.NewMap(map[string]property.Value{
		"instanceId": property.New(property.Computed), "port": property.New(80.0), "name": property.New("web"),
	})
	resp, err := s.Check(pgp.CheckRequest{Urn: urn("InstanceIngress", "x"), Inputs: in})
	require.NoError(t, err)
	assert.Empty(t, resp.Failures)
	// Known values are still judged, also next to an unknown one.
	bad := in.Set("port", property.New(0.0))
	resp, err = s.Check(pgp.CheckRequest{Urn: urn("InstanceIngress", "x"), Inputs: bad})
	require.NoError(t, err)
	require.Len(t, resp.Failures, 1)
	assert.Equal(t, "port", resp.Failures[0].Property)
}

// A nested unknown is skipped, its known siblings are still validated.
func TestCheck_NestedUnknownOnlySkipsThatLeaf(t *testing.T) {
	failures := []pgp.CheckFailure{
		{Property: "instanceShape.virtualCpu", Reason: "x"},
		{Property: "instanceShape.memoryMegabytes", Reason: "y"},
		{Property: "tags[1].key", Reason: "z"},
		{Property: "tags[0].key", Reason: "w"},
		{Property: "tags.key", Reason: "named without an index"},
	}
	in := property.NewMap(map[string]property.Value{
		"instanceShape": property.New(property.NewMap(map[string]property.Value{
			"virtualCpu": property.New(property.Computed), "memoryMegabytes": property.New(0.0),
		})),
		"tags": property.New(property.NewArray([]property.Value{
			property.New(property.NewMap(map[string]property.Value{"key": property.New("a")})),
			property.New(property.NewMap(map[string]property.Value{"key": property.New(property.Computed)})),
		})),
	})
	kept := dropUnknownFailures(failures, in)
	// Only the failures on unknown values go: the known sibling and the known element stay.
	var props []string
	for _, f := range kept {
		props = append(props, f.Property)
	}
	assert.ElementsMatch(t, []string{"instanceShape.memoryMegabytes", "tags[0].key"}, props)
	assert.Equal(t, "a.b", stripIndexes("a[0].b"))
}
