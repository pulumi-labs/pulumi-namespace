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

package provider_test

import "testing"

func TestReplaceOnChanges_NewResources(t *testing.T) {
	spec := mustSchema(t)
	cases := map[string][]string{
		"namespace:index:GithubProfile":            {"tag"},
		"namespace:index:EgressPolicy":             {"tag"},
		"namespace:index:BuildkiteQueue":           {"queueName"},
		"namespace:index:RegistryRepositoryPolicy": {"repository"},
		"namespace:index:TenantGroup":              {"name"},
		"namespace:index:RevokableToken":           {"name", "description", "expiresAt", "scope", "policies", "grants"},
		"namespace:index:Blueprint":                {"name"},
	}
	for tok, props := range cases {
		res, ok := spec.Resources[tok]
		if !ok {
			t.Fatalf("%s not found in schema", tok)
		}
		for _, p := range props {
			in, ok := res.InputProperties[p]
			if !ok {
				t.Errorf("%s: input %q missing", tok, p)
				continue
			}
			if !in.ReplaceOnChanges {
				t.Errorf("%s.%s must be replaceOnChanges", tok, p)
			}
		}
	}
}

func TestUpdatableInputs_NewResourcesAreNotReplaceOnChanges(t *testing.T) {
	spec := mustSchema(t)
	cases := map[string][]string{
		"namespace:index:GithubProfile":            {"description", "instanceShape", "cacheVolumeSettings", "networkPolicy", "accessLevel", "rebuildTrigger"},
		"namespace:index:EgressPolicy":             {"description", "mode", "rules"},
		"namespace:index:BuildkiteQueue":           {"egressPolicyTag", "permissions"},
		"namespace:index:RegistryDefaultPolicy":    {"defaultExpirationSeconds", "expirationEnforcement"},
		"namespace:index:RegistryRepositoryPolicy": {"expirationSeconds", "expirationEnforcement"},
		"namespace:index:TenantGroup":              {"policies"},
		"namespace:index:Blueprint":                {"dockerfileContent", "aptBaseImage", "aptBaseBlueprint", "aptPackages"},
		"namespace:index:RevokableToken":           {"refreshTrigger", "refreshMinimumSeconds"},
	}
	for tok, props := range cases {
		res, ok := spec.Resources[tok]
		if !ok {
			t.Fatalf("%s not found in schema", tok)
		}
		for _, p := range props {
			in, ok := res.InputProperties[p]
			if !ok {
				t.Errorf("%s: input %q missing", tok, p)
				continue
			}
			if in.ReplaceOnChanges {
				t.Errorf("%s.%s must not be replaceOnChanges: it updates in place", tok, p)
			}
		}
	}
}

func TestSecrets_NewResources(t *testing.T) {
	spec := mustSchema(t)
	tok := spec.Resources["namespace:index:RevokableToken"]
	if !tok.Properties["bearerToken"].Secret {
		t.Error("RevokableToken.bearerToken output must be marked secret")
	}
	// The egress inject value is a literal credential.
	if _, ok := spec.Resources["namespace:index:EgressPolicy"]; !ok {
		t.Fatal("EgressPolicy missing")
	}
}

func TestSchemaHasNewSurface(t *testing.T) {
	spec := mustSchema(t)
	for _, tok := range []string{"GithubProfile", "EgressPolicy", "BuildkiteQueue", "RegistryDefaultPolicy",
		"RegistryRepositoryPolicy", "TenantGroup", "RevokableToken", "Blueprint"} {
		if _, ok := spec.Resources["namespace:index:"+tok]; !ok {
			t.Errorf("resource %s missing", tok)
		}
	}
	for _, tok := range []string{"getGithubProfiles", "getTenantGroups", "getBlueprints", "getRegistryRepositories",
		"getRegistryImages", "getUsage", "getVolumes", "getGithubJobs"} {
		if _, ok := spec.Functions["namespace:index:"+tok]; !ok {
			t.Errorf("function %s missing", tok)
		}
	}
}
