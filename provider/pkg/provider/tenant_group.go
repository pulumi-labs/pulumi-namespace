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

	iamv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/iam/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// TenantGroup resource
// ---------------------------------------------------------------------------

// TenantGroup is a named group of tenants that shares a set of policies.
type TenantGroup struct{}

// TenantGroupPolicy is one policy applied to every tenant of the group.
type TenantGroupPolicy struct {
	Policy string `pulumi:"policy" json:"policy"`
	Value  string `pulumi:"value" json:"value"`
}

func (p *TenantGroupPolicy) Annotate(a infer.Annotator) {
	a.Describe(&p.Policy, "Policy name.")
	a.Describe(&p.Value, "Policy value.")
}

type TenantGroupArgs struct {
	Name     string              `pulumi:"name" json:"name" provider:"replaceOnChanges"`
	Policies []TenantGroupPolicy `pulumi:"policies,optional" json:"policies,omitempty"`
}

type TenantGroupState struct {
	TenantGroupArgs

	EnrolledTenantIDs []string `pulumi:"enrolledTenantIds"`
}

func (r *TenantGroup) Annotate(a infer.Annotator) {
	a.Describe(&r, "A Namespace IAM tenant group: a named set of policies applied to the tenants enrolled in it. "+
		"Create uses EnsureTenantGroup, which is idempotent: an existing group with the same name is taken over and "+
		"its policies are overwritten. Changing 'name' replaces the group. Destroying deletes the group. "+
		"Import with the group name.")
}

func (a *TenantGroupArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Name, "The group name. Changing it replaces the group.")
	ann.Describe(&a.Policies, "Policies applied to the group's tenants.")
}

func (s *TenantGroupState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.EnrolledTenantIDs, "IDs of the tenants currently enrolled in the group.")
}

func (r *TenantGroup) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[TenantGroupArgs], error) {
	return checkWith(ctx, req, func(a TenantGroupArgs) []pgp.CheckFailure {
		f := requireNonEmpty("name", a.Name)
		for i, p := range a.Policies {
			if p.Policy == "" {
				f = append(f, fail(fmt.Sprintf("policies[%d].policy", i), "must not be empty"))
			}
		}
		return f
	})
}

func ensureGroup(ctx context.Context, c *nsClient, in TenantGroupArgs) error {
	var req iamv1.EnsureTenantGroupRequest
	if err := toProto(in, &req); err != nil {
		return err
	}
	return c.call(ctx, c.endpoints.iamURL(), tenantService, "EnsureTenantGroup", &req, nil)
}

func describeGroup(ctx context.Context, c *nsClient, name string) (*iamv1.DescribeTenantGroupResponse, error) {
	var res iamv1.DescribeTenantGroupResponse
	if err := c.call(ctx, c.endpoints.iamURL(), tenantService, "DescribeTenantGroup",
		&iamv1.TenantGroupReference{Name: name}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func applyGroup(state *TenantGroupState, d *iamv1.DescribeTenantGroupResponse, declared *TenantGroupArgs) error {
	var srv TenantGroupArgs
	if err := fromProto(d, &srv); err != nil {
		return err
	}
	srv.Name = d.GetName()
	if declared != nil && len(declared.Policies) == 0 {
		srv.Policies = nil
	}
	state.TenantGroupArgs = srv
	state.EnrolledTenantIDs = append([]string{}, d.GetEnrolledTenantIds()...)
	return nil
}

// Diff deletes before replacing: TenantGroup adopts and later restores server-side
// state, so with the default create-before-delete order a forced replace
// (--replace, --target-replace, taint) would run the old resource's delete
// after the new create and undo what the new one adopted.
func (r *TenantGroup) Diff(
	_ context.Context, req infer.DiffRequest[TenantGroupArgs, TenantGroupState],
) (infer.DiffResponse, error) {
	return diffDeleteFirst(req.State.TenantGroupArgs, req.Inputs), nil
}

func (r *TenantGroup) Create(
	ctx context.Context, req infer.CreateRequest[TenantGroupArgs],
) (infer.CreateResponse[TenantGroupState], error) {
	state := TenantGroupState{TenantGroupArgs: req.Inputs, EnrolledTenantIDs: []string{}}
	if req.DryRun {
		return infer.CreateResponse[TenantGroupState]{ID: req.Inputs.Name, Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[TenantGroupState]{}, err
	}
	if err := ensureGroup(ctx, c, req.Inputs); err != nil {
		return infer.CreateResponse[TenantGroupState]{}, fmt.Errorf("ensure tenant group %q: %w", req.Inputs.Name, err)
	}
	// The group exists now: a failing read-back must not orphan it.
	d, err := describeGroup(ctx, c, req.Inputs.Name)
	if err == nil {
		err = applyGroup(&state, d, &req.Inputs)
	}
	if err != nil {
		return infer.CreateResponse[TenantGroupState]{ID: req.Inputs.Name, Output: state},
			infer.ResourceInitFailedError{Reasons: []string{fmt.Sprintf("read back tenant group %q: %v", req.Inputs.Name, err)}}
	}
	return infer.CreateResponse[TenantGroupState]{ID: req.Inputs.Name, Output: state}, nil
}

func (r *TenantGroup) Read(
	ctx context.Context, req infer.ReadRequest[TenantGroupArgs, TenantGroupState],
) (infer.ReadResponse[TenantGroupArgs, TenantGroupState], error) {
	type resp = infer.ReadResponse[TenantGroupArgs, TenantGroupState]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	d, err := describeGroup(ctx, c, req.ID)
	if isNotFound(err) {
		if err := notFoundOnImport("tenant group", req.ID, req.Inputs, req.State); err != nil {
			return resp{}, err
		}
		return resp{}, nil
	}
	if err != nil {
		return resp{}, fmt.Errorf("read tenant group %q: %w", req.ID, err)
	}
	var state TenantGroupState
	var declared *TenantGroupArgs
	if req.Inputs.Name != "" {
		in := req.Inputs
		declared = &in
	}
	if err := applyGroup(&state, d, declared); err != nil {
		return resp{}, err
	}
	state.Name = req.ID
	return resp{ID: req.ID, Inputs: state.TenantGroupArgs, State: state}, nil
}

func (r *TenantGroup) Update(
	ctx context.Context, req infer.UpdateRequest[TenantGroupArgs, TenantGroupState],
) (infer.UpdateResponse[TenantGroupState], error) {
	state := req.State
	state.TenantGroupArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[TenantGroupState]{Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[TenantGroupState]{}, err
	}
	if err := ensureGroup(ctx, c, req.Inputs); err != nil {
		return infer.UpdateResponse[TenantGroupState]{}, fmt.Errorf("update tenant group %q: %w", req.ID, err)
	}
	d, err := describeGroup(ctx, c, req.ID)
	if err == nil {
		err = applyGroup(&state, d, &req.Inputs)
	}
	if err != nil {
		return infer.UpdateResponse[TenantGroupState]{Output: state},
			infer.ResourceInitFailedError{Reasons: []string{fmt.Sprintf("read back tenant group %q: %v", req.ID, err)}}
	}
	return infer.UpdateResponse[TenantGroupState]{Output: state}, nil
}

func (r *TenantGroup) Delete(
	ctx context.Context, req infer.DeleteRequest[TenantGroupState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.iamURL(), tenantService, "DeleteTenantGroup", &iamv1.TenantGroupReference{Name: req.ID}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("delete tenant group %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// ---------------------------------------------------------------------------
// getTenantGroups data source
// ---------------------------------------------------------------------------

type GetTenantGroups struct{}

type GetTenantGroupsArgs struct{}

type TenantGroupSummary struct {
	Name     string              `pulumi:"name"`
	Policies []TenantGroupPolicy `pulumi:"policies"`
}

type GetTenantGroupsResult struct {
	TenantGroups []TenantGroupSummary `pulumi:"tenantGroups"`
}

func (f *GetTenantGroups) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the tenant groups of the current tenant.")
}

func (s *TenantGroupSummary) Annotate(a infer.Annotator) {
	a.Describe(&s.Name, "Group name.")
	a.Describe(&s.Policies, "Policies of the group.")
}

func (r *GetTenantGroupsResult) Annotate(a infer.Annotator) {
	a.Describe(&r.TenantGroups, "The tenant groups.")
}

func (f *GetTenantGroups) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetTenantGroupsArgs],
) (infer.FunctionResponse[GetTenantGroupsResult], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetTenantGroupsResult]{}, err
	}
	var res iamv1.ListTenantGroupsResponse
	if err := c.call(ctx, c.endpoints.iamURL(), tenantService, "ListTenantGroups", &iamv1.ListTenantGroupsRequest{}, &res); err != nil {
		return infer.FunctionResponse[GetTenantGroupsResult]{}, fmt.Errorf("list tenant groups: %w", err)
	}
	out := GetTenantGroupsResult{TenantGroups: make([]TenantGroupSummary, 0, len(res.GetTenantGroups()))}
	for _, g := range res.GetTenantGroups() {
		sum := TenantGroupSummary{Name: g.GetName(), Policies: []TenantGroupPolicy{}}
		for _, p := range g.GetPolicies() {
			sum.Policies = append(sum.Policies, TenantGroupPolicy{Policy: p.GetPolicy(), Value: p.GetValue()})
		}
		out.TenantGroups = append(out.TenantGroups, sum)
	}
	return infer.FunctionResponse[GetTenantGroupsResult]{Output: out}, nil
}

// ---- getTenantGroup ----

// GetTenantGroup reads one tenant group.
type GetTenantGroup struct{}

type GetTenantGroupArgs struct {
	Name string `pulumi:"name"`
}

func (f *GetTenantGroup) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one IAM tenant group by name, including the IDs of the tenants enrolled in it.")
}

func (a *GetTenantGroupArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Name, "The group name.")
}

func (f *GetTenantGroup) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetTenantGroupArgs],
) (infer.FunctionResponse[TenantGroupState], error) {
	st, err := readLookup(ctx, "tenant group", req.Input.Name, (&TenantGroup{}).Read)
	return infer.FunctionResponse[TenantGroupState]{Output: st}, err
}
