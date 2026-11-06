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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// #1577: an import has no state, so a revealable VaultObject's recovered value
// used to be stored as plaintext.
func TestImport_RevealableVaultObjectValueIsSecret(t *testing.T) {
	s := lcServer(t, newNSFake(t), "eu")
	created := lcCreate(t, s, "VaultObject", lcSecret(pm(map[string]any{"revealable": true}), "value", "s3cr3t"))
	imp := lcRead(t, s, "VaultObject", created.ID, property.NewMap(nil), property.NewMap(nil))
	for kind, m := range map[string]property.Map{"state": imp.Properties, "inputs": imp.Inputs} {
		v, ok := m.GetOk("value")
		require.True(t, ok, "%s has no value", kind)
		assert.Equal(t, "s3cr3t", v.AsString(), kind)
		assert.True(t, v.Secret(), "imported %s value must be secret", kind)
	}
}
