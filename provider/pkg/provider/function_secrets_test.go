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
	"fmt"
	"strings"
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	presource "github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/require"
)

type secretAuditProp struct {
	Secret bool `json:"secret"`
}

type secretAuditSpec struct {
	Resources map[string]struct {
		Properties map[string]secretAuditProp `json:"properties"`
	} `json:"resources"`
	Functions map[string]struct {
		Outputs struct {
			Properties map[string]secretAuditProp `json:"properties"`
		} `json:"outputs"`
	} `json:"functions"`
}

func loadSecretAuditSchema(t *testing.T) (string, secretAuditSpec) {
	t.Helper()
	prov, err := newProvider()
	require.NoError(t, err)
	resp, err := readSecretsServer(t, prov).GetSchema(pgp.GetSchemaRequest{})
	require.NoError(t, err)
	var spec secretAuditSpec
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &spec))
	return resp.Schema, spec
}

// #1599: pulumi-go-provider applies `provider:"secret"` tags to function outputs on
// Invoke (infer applySecrets, v1.3.1), so a function output is returned as a secret
// exactly when its schema marks it secret. A lookup function (get<Name>) returns the
// same fields as the resource <Name>, so every secret resource output must be secret
// on the function too, or a credential read through the lookup lands in state as
// plaintext.
func TestFunctionsMarkEveryResourceSecret(t *testing.T) {
	_, spec := loadSecretAuditSchema(t)
	require.NotEmpty(t, spec.Functions, "the schema declares no functions")
	var checked int
	for ftok, f := range spec.Functions {
		i := strings.LastIndex(ftok, ":")
		name := ftok[i+1:]
		if !strings.HasPrefix(name, "get") || len(name) == 3 {
			continue
		}
		rtok := ftok[:i+1] + strings.ToUpper(name[3:4]) + name[4:]
		r, ok := spec.Resources[rtok]
		if !ok {
			continue
		}
		for prop, rp := range r.Properties {
			fp, ok := f.Outputs.Properties[prop]
			if !ok || !rp.Secret {
				continue
			}
			checked++
			if !fp.Secret {
				t.Errorf("%s output %q is secret on %s but not on the function", ftok, prop, rtok)
			}
		}
	}
	t.Logf("checked %d secret resource outputs against their lookup functions", checked)
}

// Credential-like function outputs that no resource mirrors must be reviewed by hand:
// the allowlist names the ones that are verified not to be credentials.
func TestFunctionOutputsWithCredentialNamesAreSecret(t *testing.T) {
	_, spec := loadSecretAuditSchema(t)
	for ftok, f := range spec.Functions {
		for prop, p := range f.Outputs.Properties {
			if p.Secret || !credentialLikeName(prop) {
				continue
			}
			if _, ok := notCredentialFunctionOutputs[ftok+"#"+prop]; !ok {
				t.Errorf("%s output %q looks like a credential but is not secret: mark it `provider:\"secret\"` or add it to notCredentialFunctionOutputs with a reason", ftok, prop)
			}
		}
	}
}

func credentialLikeName(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"password", "secret", "token", "key", "credential", "private"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// notCredentialFunctionOutputs maps "<function token>#<output>" to why that output is
// not a credential, despite its name.
var notCredentialFunctionOutputs = map[string]string{
	"namespace:index:getRevokableToken#tokenExpiresAt": "an identifier, name, timestamp or metadata, never the credential itself",
	"namespace:index:getRevokableToken#tokenId":        "an identifier, name, timestamp or metadata, never the credential itself",
	"namespace:index:getRevokableToken#tokenState":     "an identifier, name, timestamp or metadata, never the credential itself",
	"namespace:index:getRevokableTokens#tokens":        "an identifier, name, timestamp or metadata, never the credential itself",
}

// #1599: a secret declared inside an object type must come back secret from Read,
// wherever the type is used (object, array of objects, map of objects). The value is
// built from the schema: every property of a type is filled in, then the result is
// compared with what the schema says.
func TestReadMarksNestedSchemaSecrets(t *testing.T) {
	prov, err := newProvider()
	require.NoError(t, err)
	resp, err := readSecretsServer(t, prov).GetSchema(pgp.GetSchemaRequest{})
	require.NoError(t, err)
	schema, err := parseSecretSchema(resp.Schema)
	require.NoError(t, err)

	var nested int
	for tok, r := range schema.Resources {
		for _, kind := range []struct {
			name  string
			props map[string]propSpec
		}{{"output", r.Properties}, {"input", r.InputProperties}} {
			for name, ps := range kind.props {
				if ps.Secret || !hasNestedSecret(ps, schema.Types, 0) {
					continue
				}
				nested++
				sample := sampleValue(ps, schema.Types, 0)
				marked := markSecrets(property.NewMap(map[string]property.Value{name: sample}), map[string]propSpec{name: ps}, schema.Types)
				assertNestedSecrets(t, tok+" "+kind.name+" "+name, marked.Get(name), ps, schema.Types, 0)
			}
		}
	}
	t.Logf("checked %d properties with a nested secret", nested)
}

func hasNestedSecret(ps propSpec, types map[string]objectSpec, depth int) bool {
	if depth > maxSecretDepth {
		return false
	}
	if ps.Secret {
		return true
	}
	if ps.Items != nil && hasNestedSecret(*ps.Items, types, depth+1) {
		return true
	}
	if ps.AdditionalProperties != nil && hasNestedSecret(*ps.AdditionalProperties, types, depth+1) {
		return true
	}
	if obj, ok := types[strings.TrimPrefix(ps.Ref, "#/types/")]; ps.Ref != "" && ok {
		for _, p := range obj.Properties {
			if hasNestedSecret(p, types, depth+1) {
				return true
			}
		}
	}
	return false
}

func sampleValue(ps propSpec, types map[string]objectSpec, depth int) property.Value {
	if depth > 4 {
		return property.New("v")
	}
	switch {
	case ps.Items != nil:
		return property.New(property.NewArray([]property.Value{sampleValue(*ps.Items, types, depth+1)}))
	case ps.AdditionalProperties != nil:
		return property.New(property.NewMap(map[string]property.Value{"k": sampleValue(*ps.AdditionalProperties, types, depth+1)}))
	}
	if obj, ok := types[strings.TrimPrefix(ps.Ref, "#/types/")]; ps.Ref != "" && ok && len(obj.Properties) > 0 {
		m := map[string]property.Value{}
		for name, p := range obj.Properties {
			m[name] = sampleValue(p, types, depth+1)
		}
		return property.New(property.NewMap(m))
	}
	return property.New("v")
}

func assertNestedSecrets(t *testing.T, where string, v property.Value, ps propSpec, types map[string]objectSpec, depth int) {
	t.Helper()
	if ps.Secret {
		require.True(t, v.Secret(), "%s should be secret", where)
		return
	}
	require.False(t, v.Secret(), "%s should not be secret", where)
	switch {
	case v.IsArray() && ps.Items != nil:
		for i, e := range v.AsArray().All {
			assertNestedSecrets(t, fmt.Sprintf("%s[%d]", where, i), e, *ps.Items, types, depth+1)
		}
	case v.IsMap() && ps.AdditionalProperties != nil:
		for k, e := range v.AsMap().All {
			assertNestedSecrets(t, where+"."+k, e, *ps.AdditionalProperties, types, depth+1)
		}
	case v.IsMap():
		if obj, ok := types[strings.TrimPrefix(ps.Ref, "#/types/")]; ps.Ref != "" && ok {
			for name, p := range obj.Properties {
				e, ok := v.AsMap().GetOk(name)
				require.True(t, ok, "%s.%s missing", where, name)
				assertNestedSecrets(t, where+"."+name, e, p, types, depth+1)
			}
		}
	}
}

func TestMarkSecretsNestedShapes(t *testing.T) {
	types := map[string]objectSpec{
		"p:index:Inner": {Properties: map[string]propSpec{"key": {Secret: true}, "name": {}}},
		"p:index:Outer": {Properties: map[string]propSpec{"inner": {Ref: "#/types/p:index:Inner"}, "list": {Items: &propSpec{Ref: "#/types/p:index:Inner"}}}},
	}
	inner := func() property.Value {
		return property.New(property.NewMap(map[string]property.Value{"key": property.New("k"), "name": property.New("n")}))
	}
	props := map[string]propSpec{
		"obj":  {Ref: "#/types/p:index:Outer"},
		"list": {Items: &propSpec{Ref: "#/types/p:index:Inner"}},
		"byId": {AdditionalProperties: &propSpec{Ref: "#/types/p:index:Inner"}},
		"all":  {Ref: "#/types/p:index:Inner", Secret: true},
	}
	m := property.NewMap(map[string]property.Value{
		"obj": property.New(property.NewMap(map[string]property.Value{
			"inner": inner(),
			"list":  property.New(property.NewArray([]property.Value{inner()})),
		})),
		"list": property.New(property.NewArray([]property.Value{inner(), inner()})),
		"byId": property.New(property.NewMap(map[string]property.Value{"a": inner()})),
		"all":  inner(),
		"keep": property.New("plain"),
	})
	got := markSecrets(m, props, types)
	secretKey := func(v property.Value) bool { return v.AsMap().Get("key").Secret() && !v.AsMap().Get("name").Secret() }
	obj := got.Get("obj").AsMap()
	require.True(t, secretKey(obj.Get("inner")))
	require.True(t, secretKey(obj.Get("list").AsArray().Get(0)))
	require.True(t, secretKey(got.Get("list").AsArray().Get(0)))
	require.True(t, secretKey(got.Get("list").AsArray().Get(1)))
	require.True(t, secretKey(got.Get("byId").AsMap().Get("a")))
	require.True(t, got.Get("all").Secret())
	require.False(t, got.Get("keep").Secret())
	require.False(t, got.Get("obj").Secret())
}

// A schema load error is not cached: the next Read retries and succeeds.
func TestReadRetriesSchemaLoadAfterError(t *testing.T) {
	prov, err := newProvider()
	require.NoError(t, err)
	real := prov.GetSchema
	var calls int
	prov.GetSchema = func(ctx context.Context, req pgp.GetSchemaRequest) (pgp.GetSchemaResponse, error) {
		calls++
		if calls == 1 {
			return pgp.GetSchemaResponse{}, errors.New("transient")
		}
		return real(ctx, req)
	}
	prov.Read = func(context.Context, pgp.ReadRequest) (pgp.ReadResponse, error) {
		return pgp.ReadResponse{ID: "id-1", Properties: property.NewMap(map[string]property.Value{"a": property.New("x")})}, nil
	}
	s := readSecretsServer(t, wrapReadSecrets(prov))
	req := pgp.ReadRequest{ID: "id-1", Urn: presource.NewURN("stack", "proj", "", tokens.Type("pkg:index:Thing"), "name")}
	_, err = s.Read(req)
	require.ErrorContains(t, err, "loading provider schema")
	_, err = s.Read(req)
	require.NoError(t, err)
}
