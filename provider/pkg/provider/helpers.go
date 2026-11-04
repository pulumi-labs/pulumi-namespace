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
	"fmt"
	"sort"
	"time"

	"buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/stdlib"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// labelsToProto converts a label map to the stdlib.Label list the API expects,
// sorted by name so request bodies are deterministic.
func labelsToProto(m map[string]string) []*stdlib.Label {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*stdlib.Label, 0, len(keys))
	for _, k := range keys {
		out = append(out, &stdlib.Label{Name: k, Value: m[k]})
	}
	return out
}

// labelsFromProto converts an API label list to a map.
func labelsFromProto(ls []*stdlib.Label) map[string]string {
	if len(ls) == 0 {
		return nil
	}
	out := make(map[string]string, len(ls))
	for _, l := range ls {
		out[l.GetName()] = l.GetValue()
	}
	return out
}

// labelsView reconciles the labels the server reports with what the user
// declared. The server may attach system labels the user never asked for, and
// reporting those back as inputs would turn them into a perpetual diff (or,
// where labels are replace-on-change, a replacement on the next up).
//
// import is true only when Read runs with an empty state (pulumi import): the
// server's full label set then becomes the inputs. On an ordinary refresh the
// inputs only ever carry the keys the user declared (prior), so a removed or
// changed declared label still surfaces as drift while undeclared server
// labels are ignored. A refresh with no declared labels reports none.
func labelsView(prior, server map[string]string, imported bool) map[string]string {
	if imported {
		if len(server) == 0 {
			return nil
		}
		return server
	}
	if len(prior) == 0 {
		return nil
	}
	out := make(map[string]string, len(prior))
	for k := range prior {
		if v, ok := server[k]; ok {
			out[k] = v
		}
	}
	return out
}

// parseRFC3339 parses an RFC3339 timestamp input into a protobuf Timestamp.
func parseRFC3339(field, s string) (*timestamppb.Timestamp, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC3339 timestamp such as 2026-12-31T23:59:59Z: %w", field, err)
	}
	return timestamppb.New(t), nil
}

// formatTimestamp renders a protobuf Timestamp as RFC3339 (UTC); a nil
// timestamp yields "".
func formatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

func ptr[T any](v T) *T { return &v }

// emptyToNil returns nil for the empty string, otherwise a pointer to it.
func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parseTS converts a time into a protobuf Timestamp.
func parseTS(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
