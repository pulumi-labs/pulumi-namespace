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
	"fmt"
	"strings"
	"sync"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// secretReads makes Read return the schema's secret properties as secrets.
//
// pulumi-go-provider applies the `provider:"secret"` tags to Create, Update and
// function (Invoke) outputs, but a Read response only keeps the secretness of the
// state it was given. On an import there is no state, so a secret the provider
// recovers (Machine.secretKey, ApiKey.secret) was stored as plaintext (#1564, #1577).
// The secret properties are read from the provider's own schema, so a new secret
// field is covered without a second list.
//
// Nested secrets (#1599): a secret declared inside an object type (for example
// SyncSourceS3.awsSecretAccessKey) is marked too. The schema is walked from each
// top-level property through `$ref`, array items and map values, so a secret nested
// in an object, an array of objects or a map of objects is covered, and a property
// that is secret as a whole stays secret as a whole.
//
// Lookup functions need no wrapper: infer applies the output tags on Invoke, so a
// function output is a secret exactly when its schema says so (checked by
// TestFunctionsMarkEveryResourceSecret).
type secretReads struct {
	mu     sync.Mutex
	schema *secretSchema // set only after a successful load
}

// propSpec is the part of a schema property or type reference that decides secrecy.
type propSpec struct {
	Secret               bool      `json:"secret"`
	Ref                  string    `json:"$ref"`
	Items                *propSpec `json:"items"`
	AdditionalProperties *propSpec `json:"additionalProperties"`
}

type objectSpec struct {
	Properties      map[string]propSpec `json:"properties"`
	InputProperties map[string]propSpec `json:"inputProperties"`
}

type secretSchema struct {
	Resources map[string]objectSpec `json:"resources"`
	Types     map[string]objectSpec `json:"types"`
}

func parseSecretSchema(schema string) (*secretSchema, error) {
	var s secretSchema
	if err := json.Unmarshal([]byte(schema), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// load returns the parsed schema. Only a success is cached: a failure (for example
// a transient error generating the schema) is retried by the next Read instead of
// failing every Read for the life of the process.
func (s *secretReads) load(ctx context.Context, p pgp.Provider) (*secretSchema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schema != nil {
		return s.schema, nil
	}
	resp, err := p.GetSchema(ctx, pgp.GetSchemaRequest{})
	if err != nil {
		return nil, err
	}
	parsed, err := parseSecretSchema(resp.Schema)
	if err != nil {
		return nil, err
	}
	s.schema = parsed
	return parsed, nil
}

// maxSecretDepth bounds the walk through recursive types.
const maxSecretDepth = 16

// markSecrets marks the secret properties of m, as described by props, as secrets.
func markSecrets(m property.Map, props map[string]propSpec, types map[string]objectSpec) property.Map {
	return markProps(m, props, types, 0)
}

func markProps(m property.Map, props map[string]propSpec, types map[string]objectSpec, depth int) property.Map {
	for name, ps := range props {
		if v, ok := m.GetOk(name); ok {
			m = m.Set(name, markValue(v, ps, types, depth))
		}
	}
	return m
}

func markValue(v property.Value, ps propSpec, types map[string]objectSpec, depth int) property.Value {
	if v.IsNull() {
		return v
	}
	if ps.Secret {
		if v.Secret() {
			return v
		}
		return v.WithSecret(true)
	}
	if depth >= maxSecretDepth {
		return v
	}
	var out property.Value
	switch {
	case v.IsMap():
		m := v.AsMap()
		if obj, ok := types[strings.TrimPrefix(ps.Ref, "#/types/")]; ps.Ref != "" && ok {
			m = markProps(m, obj.Properties, types, depth+1)
		} else if ps.AdditionalProperties != nil {
			next := make(map[string]property.Value, m.Len())
			for k, e := range m.All {
				next[k] = markValue(e, *ps.AdditionalProperties, types, depth+1)
			}
			m = property.NewMap(next)
		} else {
			return v
		}
		out = property.New(m)
	case v.IsArray() && ps.Items != nil:
		a := v.AsArray()
		next := make([]property.Value, a.Len())
		for i, e := range a.All {
			next[i] = markValue(e, *ps.Items, types, depth+1)
		}
		out = property.New(property.NewArray(next))
	default:
		return v
	}
	return out.WithSecret(v.Secret()).WithDependencies(v.Dependencies())
}

// wrapReadSecrets returns p with Read marking the schema's secret properties.
func wrapReadSecrets(p pgp.Provider) pgp.Provider {
	read := p.Read
	if read == nil {
		return p
	}
	var s secretReads
	p.Read = func(ctx context.Context, req pgp.ReadRequest) (pgp.ReadResponse, error) {
		resp, err := read(ctx, req)
		if resp.ID == "" {
			return resp, err
		}
		schema, lerr := s.load(ctx, p)
		if lerr != nil {
			// Failing loudly: returning the recovered values unmarked would store
			// them as plaintext in the state.
			return resp, fmt.Errorf("marking secret properties: loading provider schema: %w", lerr)
		}
		r := schema.Resources[string(req.Urn.Type())]
		resp.Properties = markSecrets(resp.Properties, r.Properties, schema.Types)
		resp.Inputs = markSecrets(resp.Inputs, r.InputProperties, schema.Types)
		return resp, err
	}
	return p
}
