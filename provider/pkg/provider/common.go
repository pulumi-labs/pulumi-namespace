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
	"reflect"
	"strings"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Fully qualified RPC service names added after the first release. Each is
// verified against the generated descriptors by TestRPCNamesExistInSchema.
const (
	githubProfileService  = "namespace.cloud.github.v1beta.ProfileService"
	githubJobsService     = "namespace.cloud.github.v1beta.JobsService"
	egressPolicyService   = "namespace.cloud.network.v1beta.EgressPolicyService"
	buildkiteQueueService = "namespace.cloud.buildkite.QueueService"
	registryService       = "namespace.cloud.registry.v1beta.ContainerRegistryService"
	tokenService          = "namespace.cloud.iam.v1beta.TokenService"
	imageService          = "namespace.cloud.image.v1beta.ImageService"
	usageService          = "namespace.cloud.compute.v1beta.UsageService"
	storageService        = "namespace.cloud.compute.v1beta.StorageService"
)

// maxPagesCommon bounds every pagination loop so a server that keeps returning
// a cursor cannot hang a deployment.
const maxPagesCommon = 1000

// paginate is the single cursor-pagination driver for every list RPC. fetch
// receives the current cursor (the zero value for the first page) and returns
// the next cursor; an empty cursor ends the walk. A cursor that repeats, or more
// than maxPagesCommon pages, is an error instead of an endless loop. The cursor
// is a string for IAM and bytes for the other services, hence the generic.
func paginate[C ~string | ~[]byte](method string, fetch func(cursor C) (C, error)) error {
	var cursor C
	seen := map[string]bool{}
	for page := 0; page < maxPagesCommon; page++ {
		next, err := fetch(cursor)
		if err != nil {
			return err
		}
		if len(next) == 0 {
			return nil
		}
		if seen[string(next)] {
			return fmt.Errorf("namespace: %s returned a repeated pagination cursor", method)
		}
		seen[string(next)] = true
		cursor = next
	}
	return fmt.Errorf("namespace: %s did not terminate after %d pages", method, maxPagesCommon)
}

// toProto converts a Go value with protojson-compatible json tags into the
// generated protobuf message dst. Unknown fields are an error so a typo in a
// json tag fails loudly (and in tests) instead of silently dropping data.
func toProto(src any, dst proto.Message) error {
	b, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("namespace: encode request: %w", err)
	}
	if err := (protojson.UnmarshalOptions{}).Unmarshal(b, dst); err != nil {
		return fmt.Errorf("namespace: build %T: %w", dst, err)
	}
	return nil
}

// fromProto decodes a protobuf message into a Go value via its protojson form.
func fromProto(src proto.Message, dst any) error {
	if src == nil || !src.ProtoReflect().IsValid() {
		return nil
	}
	b, err := protojson.Marshal(src)
	if err != nil {
		return fmt.Errorf("namespace: encode %T: %w", src, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("namespace: decode %T: %w", src, err)
	}
	return nil
}

// keepDeclared clears, in srv, every optional (pointer, slice, map) field that
// prior leaves unset, recursing into declared structs and slices of structs.
// Resources use it in Read so server-side defaults for properties the user never
// declared do not show up as a perpetual diff, while a declared property that
// drifted on the server still does.
func keepDeclared[T any](prior T, srv *T) {
	keepDeclaredV(reflect.ValueOf(prior), reflect.ValueOf(srv).Elem())
}

func keepDeclaredV(p, s reflect.Value) {
	if p.Kind() != reflect.Struct || s.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < p.NumField(); i++ {
		pf, sf := p.Field(i), s.Field(i)
		if !sf.CanSet() {
			continue
		}
		switch pf.Kind() {
		case reflect.Ptr:
			if pf.IsNil() {
				sf.Set(reflect.Zero(sf.Type()))
			} else if sf.IsNil() && pf.Elem().Kind() != reflect.Struct && pf.Elem().IsZero() {
				// protojson omits zero values (false, 0, ""), so the server view
				// reads back as nil for something the user explicitly declared.
				// Keep the declared zero so it round-trips without a diff.
				sf.Set(pf)
			} else if pf.Elem().Kind() == reflect.Struct && !sf.IsNil() {
				keepDeclaredV(pf.Elem(), sf.Elem())
			}
		case reflect.Slice, reflect.Map:
			if pf.Len() == 0 {
				sf.Set(reflect.Zero(sf.Type()))
			} else if pf.Kind() == reflect.Slice && pf.Type().Elem().Kind() == reflect.Struct {
				for j := 0; j < pf.Len() && j < sf.Len(); j++ {
					keepDeclaredV(pf.Index(j), sf.Index(j))
				}
			}
		case reflect.Struct:
			keepDeclaredV(pf, sf)
		}
	}
}

// checkWith is the shared Check implementation: default decoding plus a
// validator that reports failures per property.
func checkWith[I any](
	ctx context.Context, req infer.CheckRequest, validate func(I) []pgp.CheckFailure,
) (infer.CheckResponse[I], error) {
	args, failures, err := infer.DefaultCheck[I](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[I]{}, err
	}
	failures = append(failures, dropUnknownFailures(validate(args), req.NewInputs)...)
	return infer.CheckResponse[I]{Inputs: args, Failures: failures}, nil
}

// unknownPaths lists the property paths ("a.b", "tags[1].key") that hold a preview-time
// unknown anywhere at or below them. infer decodes an unknown into its zero value, so a
// validator that looked at it would reject a program that wires an output of another
// resource into an input (instanceId: ${instance.instanceId}), which is the normal way to
// use most resources here (#1832).
func unknownPaths(v property.Value, path string, out map[string]bool) {
	switch {
	case v.IsComputed():
		out[path] = true
	case v.IsMap():
		for k, c := range v.AsMap().AsMap() {
			p := k
			if path != "" {
				p = path + "." + k
			}
			unknownPaths(c, p, out)
		}
	case v.IsArray():
		for i, c := range v.AsArray().AsSlice() {
			unknownPaths(c, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// pathCovers reports whether a is b or one of its ancestors (on a path boundary).
func pathCovers(a, b string) bool {
	return b == a || strings.HasPrefix(b, a+".") || strings.HasPrefix(b, a+"[")
}

// dropUnknownFailures removes the failures that are about a value which is not known yet:
// a failure on a property that is unknown, sits below an unknown, or contains one (a list
// whose element is computed cannot be judged as a whole). Known values are still validated
// in full, also their siblings (tags[0].key next to an unknown tags[1].key), and the
// apply-time Check sees every value. A failure named without an index ("tags.key") is
// compared without indexes, which can only skip more.
func dropUnknownFailures(failures []pgp.CheckFailure, inputs property.Map) []pgp.CheckFailure {
	if len(failures) == 0 {
		return failures
	}
	unknown := map[string]bool{}
	unknownPaths(property.New(inputs), "", unknown)
	delete(unknown, "")
	if len(unknown) == 0 {
		return failures
	}
	kept := failures[:0:0]
	for _, f := range failures {
		hit := false
		for u := range unknown {
			prop, uu := f.Property, u
			if !strings.Contains(prop, "[") {
				uu = stripIndexes(u)
			}
			if pathCovers(uu, prop) || pathCovers(prop, uu) {
				hit = true
				break
			}
		}
		if !hit {
			kept = append(kept, f)
		}
	}
	return kept
}

// stripIndexes turns "a[0].b" into "a.b".
func stripIndexes(p string) string {
	var b strings.Builder
	depth := 0
	for _, r := range p {
		switch {
		case r == '[':
			depth++
		case r == ']':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fail(property, format string, a ...any) pgp.CheckFailure {
	return pgp.CheckFailure{Property: property, Reason: fmt.Sprintf(format, a...)}
}

// failIfNotIn reports a failure unless v is one of allowed.
func failIfNotIn(property, v string, allowed ...string) []pgp.CheckFailure {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return []pgp.CheckFailure{fail(property, "must be one of %s, got %q", strings.Join(allowed, ", "), v)}
}

// optIn validates an optional enum-valued string.
func optIn(property string, v *string, allowed ...string) []pgp.CheckFailure {
	if v == nil {
		return nil
	}
	return failIfNotIn(property, *v, allowed...)
}

// reqIn is optIn for a property the API requires: a missing value fails too.
func reqIn(property string, v *string, allowed ...string) []pgp.CheckFailure {
	if v == nil || *v == "" {
		return []pgp.CheckFailure{fail(property, "is required; supported values: %s", strings.Join(allowed, ", "))}
	}
	return failIfNotIn(property, *v, allowed...)
}

func requireNonEmpty(property, v string) []pgp.CheckFailure {
	if strings.TrimSpace(v) == "" {
		return []pgp.CheckFailure{fail(property, "must not be empty")}
	}
	return nil
}

// secondsToDuration renders whole seconds as a protojson Duration string.
func secondsToDuration(s int) string { return fmt.Sprintf("%ds", s) }

// optional pointer helpers for read paths.
func intPtr32(v int32) *int {
	if v == 0 {
		return nil
	}
	x := int(v)
	return &x
}
