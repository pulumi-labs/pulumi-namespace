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
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/blang/semver"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	presource "github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/require"
)

type readSecretsSpec struct {
	Resources map[string]struct {
		Properties map[string]struct {
			Secret bool `json:"secret"`
		} `json:"properties"`
		InputProperties map[string]struct {
			Secret bool `json:"secret"`
		} `json:"inputProperties"`
	} `json:"resources"`
}

// readSecretsServer serves prov, so a Read runs with the run info the engine
// gives it (the schema is generated from inside Read).
func readSecretsServer(t *testing.T, prov pgp.Provider) integration.Server {
	t.Helper()
	s, err := integration.NewServer(t.Context(), "namespace", semver.MustParse("0.0.1"), integration.WithProvider(prov))
	require.NoError(t, err)
	return s
}

func loadReadSecretsSpec(t *testing.T, s integration.Server) readSecretsSpec {
	t.Helper()
	resp, err := s.GetSchema(pgp.GetSchemaRequest{})
	require.NoError(t, err)
	var spec readSecretsSpec
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &spec))
	return spec
}

// #1577: a Read response keeps only the secretness it was given, and an import
// has no state, so every schema-secret property of every resource must be
// marked on the way out, inputs and outputs, and nothing else.
func TestReadMarksEverySchemaSecret(t *testing.T) {
	prov, err := newProvider()
	require.NoError(t, err)
	spec := loadReadSecretsSpec(t, readSecretsServer(t, prov))

	plain := func(names map[string]bool) property.Map {
		m := map[string]property.Value{"plainProp": property.New("visible")}
		for name := range names {
			m[name] = property.New("recovered-" + name)
		}
		return property.NewMap(m)
	}
	var secrets int
	for tok, r := range spec.Resources {
		outs, ins := map[string]bool{}, map[string]bool{}
		for n, p := range r.Properties {
			outs[n] = p.Secret
		}
		for n, p := range r.InputProperties {
			ins[n] = p.Secret
		}
		stub := prov
		stub.Read = func(context.Context, pgp.ReadRequest) (pgp.ReadResponse, error) {
			return pgp.ReadResponse{ID: "id-1", Properties: plain(outs), Inputs: plain(ins)}, nil
		}
		got, err := readSecretsServer(t, wrapReadSecrets(stub)).Read(pgp.ReadRequest{
			ID:  "id-1",
			Urn: presource.NewURN("stack", "proj", "", tokens.Type(tok), "name"),
		})
		require.NoError(t, err, tok)
		check := func(kind string, m property.Map, want map[string]bool) {
			for name, isSecret := range want {
				v, ok := m.GetOk(name)
				require.True(t, ok, "%s %s.%s missing", kind, tok, name)
				require.Equal(t, isSecret, v.Secret(), "%s %s.%s secret=%v", kind, tok, name, isSecret)
				if isSecret {
					secrets++
				}
			}
			require.False(t, m.Get("plainProp").Secret(), "%s %s.plainProp", kind, tok)
		}
		check("output", got.Properties, outs)
		check("input", got.Inputs, ins)
	}
	require.Positive(t, secrets, "the schema declares no secret property")
}

// A schema that cannot be loaded must fail the Read, not return plaintext.
func TestReadFailsLoudlyWithoutSchema(t *testing.T) {
	prov, err := newProvider()
	require.NoError(t, err)
	prov.GetSchema = func(context.Context, pgp.GetSchemaRequest) (pgp.GetSchemaResponse, error) {
		return pgp.GetSchemaResponse{}, errors.New("no schema")
	}
	prov.Read = func(context.Context, pgp.ReadRequest) (pgp.ReadResponse, error) {
		return pgp.ReadResponse{ID: "id-1", Properties: property.NewMap(map[string]property.Value{"a": property.New("x")})}, nil
	}
	_, err = readSecretsServer(t, wrapReadSecrets(prov)).Read(pgp.ReadRequest{
		ID:  "id-1",
		Urn: presource.NewURN("stack", "proj", "", tokens.Type("pkg:index:Thing"), "name"),
	})
	require.ErrorContains(t, err, "loading provider schema")
}

// A resource that no longer exists (empty ID) passes straight through.
func TestReadMissingResourcePassesThrough(t *testing.T) {
	prov, err := newProvider()
	require.NoError(t, err)
	prov.Read = func(context.Context, pgp.ReadRequest) (pgp.ReadResponse, error) {
		return pgp.ReadResponse{}, nil
	}
	got, err := readSecretsServer(t, wrapReadSecrets(prov)).Read(pgp.ReadRequest{})
	require.NoError(t, err)
	require.Empty(t, got.ID)
}

func TestMarkSecrets(t *testing.T) {
	secret := map[string]propSpec{"a": {Secret: true}, "b": {}}
	m := property.NewMap(map[string]property.Value{"a": property.New("x"), "b": property.New("y"), "c": property.New("z")})
	got := markSecrets(m, secret, nil)
	require.True(t, got.Get("a").Secret())
	require.False(t, got.Get("b").Secret())
	require.False(t, got.Get("c").Secret())
	// Null values are left alone.
	n := markSecrets(property.NewMap(map[string]property.Value{"a": property.New(property.Null)}), secret, nil)
	require.False(t, n.Get("a").Secret())
}
