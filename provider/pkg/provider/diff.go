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

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// diffDeleteFirst computes a Diff for a resource whose create and delete are
// both keyed by an identity that stays the same across a replacement (or that
// adopt and later restore server-side state). It mirrors the framework's
// default input diff, driven by the same `provider:"replaceOnChanges"` struct
// tags that feed the schema, so the tags remain the single source of truth,
// and sets DeleteBeforeReplace on EVERY response, including the no-change
// ones the engine consults for forced replaces (--replace, --target-replace,
// taint). Under the default create-before-delete order the old resource's
// delete would run after the new create and clobber the state the new
// resource just adopted or created.
func diffDeleteFirst[A any](old, cur A) infer.DiffResponse {
	changed := map[string]pgp.PropertyDiff{}
	collectArgDiffs(reflect.ValueOf(old), reflect.ValueOf(cur), changed)
	return infer.DiffResponse{
		HasChanges:          len(changed) > 0,
		DeleteBeforeReplace: true,
		DetailedDiff:        changed,
	}
}

func collectArgDiffs(old, cur reflect.Value, out map[string]pgp.PropertyDiff) {
	t := old.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			collectArgDiffs(old.Field(i), cur.Field(i), out)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("pulumi"), ",")
		if name == "" || name == "-" || argValuesEqual(old.Field(i), cur.Field(i)) {
			continue
		}
		kind := pgp.Update
		if strings.Contains(f.Tag.Get("provider"), "replaceOnChanges") {
			kind = pgp.UpdateReplace
		}
		out[name] = pgp.PropertyDiff{Kind: kind, InputDiff: true}
	}
}

// argValuesEqual treats nil and empty slices/maps as equal and otherwise
// compares deeply, so nil-versus-set pointers still differ.
func argValuesEqual(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Slice, reflect.Map:
		if a.Len() == 0 && b.Len() == 0 {
			return true
		}
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// diffArgs is the framework's default input diff, driven by the same
// `provider:"replaceOnChanges"` struct tags that feed the schema, for resources
// that need to adjust the prior inputs before comparing.
func diffArgs[A any](old, cur A) infer.DiffResponse {
	changed := map[string]pgp.PropertyDiff{}
	collectArgDiffs(reflect.ValueOf(old), reflect.ValueOf(cur), changed)
	return infer.DiffResponse{HasChanges: len(changed) > 0, DetailedDiff: changed}
}
