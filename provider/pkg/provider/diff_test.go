package provider

import (
	"reflect"
	"strings"
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// diffCase mutates one input and states, independently of the struct tags,
// whether that change must replace or update in place.
type diffCase struct {
	field   string
	changed any // replacement value for the field; nil means "remove the field"
	replace bool
}

type diffSuite struct {
	typ     string
	args    any
	base    map[string]any
	state   map[string]any // extra output properties
	changes []diffCase
}

func diffSuites() []diffSuite {
	rule := func(op string) []any { return []any{map[string]any{"op": op}} }
	return []diffSuite{
		{
			typ: "Blueprint", args: BlueprintArgs{},
			base: map[string]any{"name": "bp", "dockerfileContent": "FROM alpine", "aptBaseImage": "ubuntu:24.04",
				"aptBaseBlueprint": "other", "aptPackages": []any{"curl"}},
			state: map[string]any{"blueprintId": "b-1", "createdAt": "", "imageRef": "", "buildError": ""},
			changes: []diffCase{
				{"name", "bp2", true}, {"dockerfileContent", "FROM debian", true}, {"dockerfileContent", nil, true},
				{"aptBaseImage", "ubuntu:22.04", true}, {"aptBaseImage", nil, true},
				{"aptBaseBlueprint", "other2", true}, {"aptBaseBlueprint", nil, true},
				{"aptPackages", []any{"git"}, true}, {"aptPackages", nil, true},
			},
		},
		{
			typ: "EgressPolicy", args: EgressPolicyArgs{},
			base: map[string]any{"tag": "t", "description": "d", "mode": "ALLOW", "deepPacketInspection": true,
				"rules": rule("ALLOW"), "additionalRulesets": []any{"a"}},
			state: map[string]any{"revision": 1, "adopted": false},
			changes: []diffCase{
				{"tag", "t2", true}, {"description", "d2", false}, {"description", nil, false},
				{"mode", "DENY", false}, {"deepPacketInspection", false, false}, {"deepPacketInspection", nil, false},
				{"rules", rule("DENY"), false}, {"rules", nil, false},
				{"additionalRulesets", []any{"b"}, false}, {"additionalRulesets", nil, false},
			},
		},
		{
			typ: "TenantGroup", args: TenantGroupArgs{},
			base:  map[string]any{"name": "g", "policies": []any{map[string]any{"policy": "p", "value": "v"}}},
			state: map[string]any{"enrolledTenantIds": []any{}},
			changes: []diffCase{
				{"name", "g2", true},
				{"policies", []any{map[string]any{"policy": "p", "value": "v2"}}, false}, {"policies", nil, false},
			},
		},
		{
			typ: "RegistryDefaultPolicy", args: RegistryDefaultPolicyArgs{},
			base:  map[string]any{"defaultExpirationSeconds": 10, "expirationEnforcement": "WARN"},
			state: map[string]any{"revision": 1},
			changes: []diffCase{
				{"defaultExpirationSeconds", 20, false}, {"defaultExpirationSeconds", nil, false},
				{"expirationEnforcement", "ENFORCE", false}, {"expirationEnforcement", nil, false},
			},
		},
		{
			typ: "RegistryRepositoryPolicy", args: RegistryRepositoryPolicyArgs{},
			base:  map[string]any{"repository": "r", "expirationSeconds": 10, "expirationEnforcement": "WARN"},
			state: map[string]any{"revision": 1},
			changes: []diffCase{
				{"repository", "r2", true},
				{"expirationSeconds", 20, false}, {"expirationSeconds", nil, false},
				{"expirationEnforcement", "ENFORCE", false}, {"expirationEnforcement", nil, false},
			},
		},
		{
			typ: "BuildkiteQueue", args: BuildkiteQueueArgs{},
			base: map[string]any{"queueName": "q", "egressPolicyTag": "t",
				"permissions":           map[string]any{"permissionsType": "X"},
				"openTelemetrySettings": map[string]any{"otlpEndpoint": "e"}},
			state: map[string]any{"queueId": "1", "cluster": "c", "organizationSlug": "o"},
			changes: []diffCase{
				{"queueName", "q2", true},
				{"egressPolicyTag", "t2", false}, {"egressPolicyTag", nil, false},
				{"permissions", map[string]any{"permissionsType": "Y"}, false}, {"permissions", nil, false},
				{"openTelemetrySettings", map[string]any{"otlpEndpoint": "e2"}, false}, {"openTelemetrySettings", nil, false},
			},
		},
	}
}

func withField(base map[string]any, field string, v any) map[string]any {
	out := map[string]any{}
	for k, x := range base {
		out[k] = x
	}
	if v == nil {
		delete(out, field)
	} else {
		out[field] = v
	}
	return out
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range []map[string]any{a, b} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func pulumiFields(t reflect.Type, into map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			pulumiFields(f.Type, into)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("pulumi"), ",")
		if name != "" {
			into[name] = true
		}
	}
}

// TestDiff_EveryInputReplacesOrUpdatesAsDeclared covers every input of every
// delete-before-replace resource: each one must produce the expected diff kind,
// and every diff, including a no-change one, must set DeleteBeforeReplace so a
// forced replace (--replace, taint) deletes the old resource first.
func TestDiff_EveryInputReplacesOrUpdatesAsDeclared(t *testing.T) {
	for _, su := range diffSuites() {
		t.Run(su.typ, func(t *testing.T) {
			s := newServerFor(t, newFakeAPI(t))
			fields := map[string]bool{}
			pulumiFields(reflect.TypeOf(su.args), fields)
			covered := map[string]bool{}
			for _, c := range su.changes {
				covered[c.field] = true
			}
			for f := range fields {
				assert.True(t, covered[f], "input %q has no diff case; add one", f)
			}
			for f := range covered {
				assert.True(t, fields[f], "diff case for unknown input %q", f)
			}
			require.Equal(t, len(fields), len(su.base), "base must set every input")

			state := pm(mergeMaps(su.base, su.state))
			old := pm(su.base)
			diff := func(in map[string]any) pgp.DiffResponse {
				resp, err := s.Diff(pgp.DiffRequest{Urn: urn(su.typ, "x"), ID: "id", State: state, Inputs: pm(in), OldInputs: old})
				require.NoError(t, err)
				return resp
			}
			same := diff(su.base)
			assert.False(t, same.HasChanges)
			assert.True(t, same.DeleteBeforeReplace, "forced replace of unchanged inputs must delete first")
			assert.Empty(t, same.DetailedDiff)
			for _, c := range su.changes {
				resp := diff(withField(su.base, c.field, c.changed))
				want := pgp.Update
				if c.replace {
					want = pgp.UpdateReplace
				}
				assert.True(t, resp.HasChanges, "%s: change not detected", c.field)
				assert.True(t, resp.DeleteBeforeReplace, "%s", c.field)
				require.Contains(t, resp.DetailedDiff, c.field)
				assert.Equal(t, want, resp.DetailedDiff[c.field].Kind, "%s changed=%v", c.field, c.changed)
				assert.Len(t, resp.DetailedDiff, 1, "%s: only the changed input may differ", c.field)
			}
		})
	}
}

func TestDiff_NilAndEmptyCollectionsAreEqual(t *testing.T) {
	a := BlueprintArgs{Name: "x", AptPackages: nil}
	b := BlueprintArgs{Name: "x", AptPackages: []string{}}
	assert.False(t, diffDeleteFirst(a, b).HasChanges)
}
