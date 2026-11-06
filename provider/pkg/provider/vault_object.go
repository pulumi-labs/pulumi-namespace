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
	"fmt"

	vaultv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/vault/v1beta"
	"buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/stdlib"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// VaultObject resource
// ---------------------------------------------------------------------------

// VaultObject is a versioned secret in the Namespace vault.
type VaultObject struct{}

// VaultObjectArgs are the user-supplied inputs for a vault object.
type VaultObjectArgs struct {
	// Value is the secret payload. It is the only property that can be changed
	// in place: each change writes a new version of the same object.
	Value string `pulumi:"value" provider:"secret"`

	// Description documents what the object holds. Immutable.
	Description *string `pulumi:"description,optional" provider:"replaceOnChanges"`

	// Revealable controls whether the API returns the value in later
	// DescribeObject calls. Immutable after creation.
	Revealable *bool `pulumi:"revealable,optional" provider:"replaceOnChanges"`

	// Labels are attached to the object and shared by all its versions. Immutable.
	Labels map[string]string `pulumi:"labels,optional" provider:"replaceOnChanges"`
}

// VaultObjectState is the persisted state of a vault object.
type VaultObjectState struct {
	VaultObjectArgs

	// ObjectID is the server-assigned identifier of the object.
	ObjectID string `pulumi:"objectId"`

	// Version is the identifier of the current version of the object.
	Version string `pulumi:"version"`

	// CreatedAt is when the object was created, as an RFC3339 timestamp.
	CreatedAt string `pulumi:"createdAt"`
}

func (r *VaultObject) Annotate(a infer.Annotator) {
	a.Describe(&r, "A versioned secret stored in the Namespace vault. Changing 'value' writes a new "+
		"version in place (guarded by optimistic concurrency); changing any other input replaces the object.")
}

func (a *VaultObjectArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Value, "The secret value. Stored as a Pulumi secret. The only input that updates in place.")
	ann.Describe(&a.Description, "A description of the object. Changing it replaces the object.")
	ann.Describe(&a.Revealable, "Whether the value can be read back from the API after creation. "+
		"Defaults to false. Changing it replaces the object.")
	ann.Describe(&a.Labels, "Labels attached to the object. Changing them replaces the object.")
}

func (s *VaultObjectState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.ObjectID, "The server-assigned object identifier.")
	ann.Describe(&s.Version, "The identifier of the current object version. Changes on every value update.")
	ann.Describe(&s.CreatedAt, "RFC3339 timestamp of when the object was created.")
}

func (r *VaultObject) Create(
	ctx context.Context,
	req infer.CreateRequest[VaultObjectArgs],
) (infer.CreateResponse[VaultObjectState], error) {
	in := req.Inputs
	state := VaultObjectState{VaultObjectArgs: in}
	if req.DryRun {
		return infer.CreateResponse[VaultObjectState]{ID: "", Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[VaultObjectState]{}, err
	}
	var md vaultv1.VaultObjectMetadata
	err = c.call(ctx, c.endpoints.globalURL(), vaultService, "CreateObject", &vaultv1.CreateObjectRequest{
		Description: derefStr(in.Description),
		Value:       in.Value,
		Revealable:  derefBool(in.Revealable),
		Labels:      labelsToProto(in.Labels),
	}, &md)
	if err != nil {
		return infer.CreateResponse[VaultObjectState]{}, fmt.Errorf("create vault object: %w", err)
	}
	applyVaultMetadata(&state, &md)
	return infer.CreateResponse[VaultObjectState]{ID: state.ObjectID, Output: state}, nil
}

func (r *VaultObject) Read(
	ctx context.Context,
	req infer.ReadRequest[VaultObjectArgs, VaultObjectState],
) (infer.ReadResponse[VaultObjectArgs, VaultObjectState], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.ReadResponse[VaultObjectArgs, VaultObjectState]{}, err
	}
	var res vaultv1.DescribeObjectResponse
	err = c.call(ctx, c.endpoints.globalURL(), vaultService, "DescribeObject",
		&vaultv1.DescribeObjectRequest{ObjectId: req.ID}, &res)
	if err != nil {
		if isNotFound(err) {
			return infer.ReadResponse[VaultObjectArgs, VaultObjectState]{}, nil
		}
		return infer.ReadResponse[VaultObjectArgs, VaultObjectState]{}, fmt.Errorf("read vault object %q: %w", req.ID, err)
	}
	md := res.GetMetadata()
	if md.GetDeletedAt() != nil {
		return infer.ReadResponse[VaultObjectArgs, VaultObjectState]{}, nil
	}

	state := req.State
	state.Description = emptyToNil(md.GetDescription())
	state.Labels = labelsView(req.State.Labels, labelsFromProto(md.GetLabels()), req.State.ObjectID == "")
	// The API only returns the value of revealable objects. For everything else
	// keep the value already in state: overwriting it with "" would show a
	// permanent diff and could blank the secret on the next update.
	if res.GetValue() != "" {
		state.Value = res.GetValue()
		state.Revealable = ptr(true)
	}
	applyVaultMetadata(&state, md)
	return infer.ReadResponse[VaultObjectArgs, VaultObjectState]{
		ID:     state.ObjectID,
		Inputs: state.VaultObjectArgs,
		State:  state,
	}, nil
}

func (r *VaultObject) Update(
	ctx context.Context,
	req infer.UpdateRequest[VaultObjectArgs, VaultObjectState],
) (infer.UpdateResponse[VaultObjectState], error) {
	state := VaultObjectState{VaultObjectArgs: req.Inputs}
	state.ObjectID = req.State.ObjectID
	state.Version = req.State.Version
	state.CreatedAt = req.State.CreatedAt
	if req.DryRun {
		return infer.UpdateResponse[VaultObjectState]{Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[VaultObjectState]{}, err
	}
	var md vaultv1.VaultObjectMetadata
	err = c.call(ctx, c.endpoints.globalURL(), vaultService, "UpdateObject", &vaultv1.UpdateObjectRequest{
		ObjectId:         req.ID,
		NewValue:         req.Inputs.Value,
		IfVersionMatches: req.State.Version,
	}, &md)
	if err != nil {
		return infer.UpdateResponse[VaultObjectState]{}, fmt.Errorf("update vault object %q: %w", req.ID, err)
	}
	// Refresh identity and version fields from the mutation response, not from
	// stale state.
	applyVaultMetadata(&state, &md)
	return infer.UpdateResponse[VaultObjectState]{Output: state}, nil
}

func (r *VaultObject) Delete(
	ctx context.Context,
	req infer.DeleteRequest[VaultObjectState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.globalURL(), vaultService, "DeleteObject",
		&vaultv1.DeleteObjectRequest{ObjectId: req.ID}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("delete vault object %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// applyVaultMetadata copies the server-owned fields onto state. Empty fields in
// md leave the existing value in place.
func applyVaultMetadata(state *VaultObjectState, md *vaultv1.VaultObjectMetadata) {
	if id := md.GetObjectId(); id != "" {
		state.ObjectID = id
	}
	if v := md.GetVersion(); v != "" {
		state.Version = v
	}
	if ts := formatTimestamp(md.GetCreatedAt()); ts != "" {
		state.CreatedAt = ts
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefBool(p *bool) bool {
	return p != nil && *p
}

// ---- getVaultObject / getVaultObjects ----

// GetVaultObject reads one vault object.
type GetVaultObject struct{}

type GetVaultObjectArgs struct {
	ObjectID string `pulumi:"objectId"`
}

type GetVaultObjectResult struct {
	ObjectID    string            `pulumi:"objectId"`
	Description string            `pulumi:"description"`
	Labels      map[string]string `pulumi:"labels"`
	Version     string            `pulumi:"version"`
	CreatedAt   string            `pulumi:"createdAt"`
	// Revealable is true when the API returned the value, which it only does for revealable objects.
	Revealable bool    `pulumi:"revealable"`
	Value      *string `pulumi:"value,optional" provider:"secret"`
}

func (f *GetVaultObject) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one Namespace vault object by ID. The secret 'value' is only returned for revealable objects.")
}

func (a *GetVaultObjectArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.ObjectID, "The vault object ID.")
}

func (r *GetVaultObjectResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.ObjectID, "The vault object ID.")
	ann.Describe(&r.Description, "The object description.")
	ann.Describe(&r.Labels, "Labels attached to the object, including system labels.")
	ann.Describe(&r.Version, "The current version identifier.")
	ann.Describe(&r.CreatedAt, "RFC3339 creation time.")
	ann.Describe(&r.Revealable, "True when the API returned the value (only revealable objects expose it).")
	ann.Describe(&r.Value, "The decrypted secret value. Secret. Only set for revealable objects.")
}

func (f *GetVaultObject) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetVaultObjectArgs],
) (infer.FunctionResponse[GetVaultObjectResult], error) {
	type resp = infer.FunctionResponse[GetVaultObjectResult]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	var res vaultv1.DescribeObjectResponse
	err = c.call(ctx, c.endpoints.globalURL(), vaultService, "DescribeObject",
		&vaultv1.DescribeObjectRequest{ObjectId: req.Input.ObjectID}, &res)
	if isNotFound(err) || (err == nil && (res.GetMetadata() == nil || res.GetMetadata().GetDeletedAt() != nil)) {
		return resp{}, errLookupNotFound("vault object", req.Input.ObjectID)
	}
	if err != nil {
		return resp{}, fmt.Errorf("read vault object %q: %w", req.Input.ObjectID, err)
	}
	out := vaultResult(res.GetMetadata())
	if v := res.GetValue(); v != "" {
		out.Value, out.Revealable = &v, true
	}
	return resp{Output: out}, nil
}

func vaultResult(md *vaultv1.VaultObjectMetadata) GetVaultObjectResult {
	labels := labelsFromProto(md.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}
	return GetVaultObjectResult{
		ObjectID: md.GetObjectId(), Description: md.GetDescription(), Labels: labels,
		Version: md.GetVersion(), CreatedAt: formatTimestamp(md.GetCreatedAt()),
	}
}

// GetVaultObjects lists vault objects.
type GetVaultObjects struct{}

type GetVaultObjectsArgs struct {
	// Labels restricts the result to objects carrying all of these labels.
	Labels map[string]string `pulumi:"labels,optional"`
}

// VaultObjectSummary is one vault object in a listing; the value is never listed.
type VaultObjectSummary struct {
	ObjectID    string            `pulumi:"objectId"`
	Description string            `pulumi:"description"`
	Labels      map[string]string `pulumi:"labels"`
	Version     string            `pulumi:"version"`
	CreatedAt   string            `pulumi:"createdAt"`
}

type GetVaultObjectsResult struct {
	Objects []VaultObjectSummary `pulumi:"objects"`
}

func (f *GetVaultObjects) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists Namespace vault objects (metadata only, never values), optionally filtered by labels.")
}

func (a *GetVaultObjectsArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Labels, "Only return objects carrying all of these labels.")
}

func (r *GetVaultObjectsResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.Objects, "The matching vault objects.")
}

func (f *GetVaultObjects) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetVaultObjectsArgs],
) (infer.FunctionResponse[GetVaultObjectsResult], error) {
	type resp = infer.FunctionResponse[GetVaultObjectsResult]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	var filters []*stdlib.LabelFilterEntry
	for _, l := range labelsToProto(req.Input.Labels) {
		filters = append(filters, &stdlib.LabelFilterEntry{Name: l.GetName(), Value: l.GetValue(), Op: stdlib.LabelFilterEntry_EQUAL})
	}
	var res vaultv1.ListObjectsResponse
	if err := c.call(ctx, c.endpoints.globalURL(), vaultService, "ListObjects",
		&vaultv1.ListObjectsRequest{LabelFilter: filters}, &res); err != nil {
		return resp{}, fmt.Errorf("list vault objects: %w", err)
	}
	out := GetVaultObjectsResult{Objects: []VaultObjectSummary{}}
	for _, md := range res.GetObjects() {
		if md.GetDeletedAt() != nil {
			continue
		}
		v := vaultResult(md)
		out.Objects = append(out.Objects, VaultObjectSummary{
			ObjectID: v.ObjectID, Description: v.Description, Labels: v.Labels, Version: v.Version, CreatedAt: v.CreatedAt,
		})
	}
	return resp{Output: out}, nil
}
