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
	"errors"
	"fmt"

	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// Single-object lookup functions
//
// Every resource type has a get<Name> function that reads one existing object
// by the same ID the resource imports with. They share the resource's Read
// where Read already reconstructs everything the API reports, so a lookup and
// an import never disagree. Functions return an error naming the object when
// it does not exist, so a program fails at preview instead of continuing with
// an empty value.
// ---------------------------------------------------------------------------

// errLookupNotFound is the error every lookup function returns for a missing object.
func errLookupNotFound(kind, key string) error {
	return fmt.Errorf("namespace: %s %q was not found", kind, key)
}

// errExactlyOne reports a lookup that needs exactly one of two selectors.
func errExactlyOne(fn, a, b string) error {
	return fmt.Errorf("namespace: %s needs exactly one of %q or %q", fn, a, b)
}

// readLookup runs a resource's Read from the ID alone (the import path) and
// returns its state, or a not-found error.
func readLookup[A, S any](
	ctx context.Context, kind, id string,
	read func(context.Context, infer.ReadRequest[A, S]) (infer.ReadResponse[A, S], error),
) (S, error) {
	var zero S
	res, err := read(ctx, infer.ReadRequest[A, S]{ID: id})
	var inf *importNotFoundError
	if errors.As(err, &inf) {
		return zero, errLookupNotFound(kind, id)
	}
	if err != nil {
		return zero, err
	}
	if res.ID == "" {
		return zero, errLookupNotFound(kind, id)
	}
	return res.State, nil
}
