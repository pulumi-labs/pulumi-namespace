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

	p "github.com/pulumi/pulumi-go-provider"
)

// New returns the configured provider, with provider configuration changes made in place and
// the schema's secret properties marked as secrets on Read (#1577).
func New() (p.Provider, error) {
	prov, err := newProvider()
	if err != nil {
		return prov, err
	}
	return wrapReadSecrets(withInPlaceConfigDiff(prov)), nil
}

// withInPlaceConfigDiff makes every change to the provider configuration an in-place update. By
// default infer replaces the default provider on any config change (pulumi-go-provider#409), and
// replacing a provider replaces every resource that uses it, so rotating a credential or moving an
// endpoint would delete and recreate every resource (#1591). A changed credential or endpoint only
// changes how the next call is made; each resource's own Diff still decides whether it changes.
//
// It keeps infer's diff (which fields differ, secrets, unknowns, the ignored version input) and only
// turns each replace into the matching add, delete or update. It does not decode the config, so the
// provider's live configuration is never touched by a diff.
func withInPlaceConfigDiff(prov p.Provider) p.Provider {
	inner := prov.DiffConfig
	if inner == nil {
		return prov
	}
	prov.DiffConfig = func(ctx context.Context, req p.DiffRequest) (p.DiffResponse, error) {
		resp, err := inner(ctx, req)
		if err != nil {
			return resp, err
		}
		resp.DeleteBeforeReplace = false
		for k, d := range resp.DetailedDiff {
			switch d.Kind {
			case p.AddReplace:
				d.Kind = p.Add
			case p.DeleteReplace:
				d.Kind = p.Delete
			case p.UpdateReplace:
				d.Kind = p.Update
			}
			resp.DetailedDiff[k] = d
		}
		return resp, nil
	}
	return prov
}
