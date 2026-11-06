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
	"reflect"
	"strings"
	"testing"

	"github.com/blang/semver"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/require"
)

// #1591: infer replaces the default provider on any config change, and replacing the provider
// replaces every resource that uses it. A rotated credential must be an in-place update: for every
// config field, changing it (or setting or clearing it) is never a replace.
func TestProviderConfigChangeIsNeverAReplace(t *testing.T) {
	prov, err := New()
	require.NoError(t, err)
	s, err := integration.NewServer(t.Context(), "huggingface", semver.MustParse("0.0.1"), integration.WithProvider(prov))
	require.NoError(t, err)

	ct := reflect.TypeOf(&ProviderConfig{})
	for ct.Kind() == reflect.Ptr {
		ct = ct.Elem()
	}
	var keys []string
	for i := 0; i < ct.NumField(); i++ {
		if k, _, _ := strings.Cut(ct.Field(i).Tag.Get("pulumi"), ","); k != "" && k != "-" {
			keys = append(keys, k)
		}
	}
	require.NotEmpty(t, keys)

	diff := func(old, cur map[string]property.Value) pgp.DiffResponse {
		d, err := s.DiffConfig(pgp.DiffRequest{State: property.NewMap(old), Inputs: property.NewMap(cur), OldInputs: property.NewMap(old)})
		require.NoError(t, err)
		return d
	}
	for _, k := range keys {
		t.Run(k, func(t *testing.T) {
			a, b := map[string]property.Value{k: property.New("a")}, map[string]property.Value{k: property.New("b")}
			d := diff(a, b)
			require.True(t, d.HasChanges, "a changed %s is a change", k)
			require.Contains(t, d.DetailedDiff, k)
			require.Equal(t, pgp.Update, d.DetailedDiff[k].Kind, "a changed %s updates in place", k)
			require.False(t, d.DeleteBeforeReplace)

			for name, d := range map[string]pgp.DiffResponse{"set": diff(map[string]property.Value{}, b), "clear": diff(a, map[string]property.Value{})} {
				require.True(t, d.HasChanges, name)
				for prop, pd := range d.DetailedDiff {
					require.NotContains(t, string(pd.Kind), "replace", "%s of %s must not replace", name, prop)
				}
			}
			require.False(t, diff(a, a).HasChanges, "an unchanged %s is no change", k)
		})
	}
	// The engine's version input never counts.
	withVersion := map[string]property.Value{keys[0]: property.New("a"), "version": property.New("1.0.0")}
	require.False(t, diff(map[string]property.Value{keys[0]: property.New("a")}, withVersion).HasChanges)
}
