package provider

import (
	"context"
	"fmt"

	vaultv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/vault/v1beta"
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
		return infer.CreateResponse[VaultObjectState]{ID: "preview", Output: state}, nil
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
