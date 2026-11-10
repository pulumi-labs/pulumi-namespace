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
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// Tenant resource
// ---------------------------------------------------------------------------

// Tenant is a Namespace IAM tenant: an isolated account that owns its own
// instances, vault objects and policies.
type Tenant struct{}

// TenantPolicy is a policy attached to a tenant.
type TenantPolicy struct {
	// ID identifies the policy within the tenant.
	ID string `pulumi:"id"`
	// Policy is the policy name, such as a quota or capability the tenant is granted.
	Policy string `pulumi:"policy"`
	// Value is the policy value.
	Value string `pulumi:"value"`
	// ExpiresAt optionally makes the policy expire, as an RFC3339 timestamp.
	ExpiresAt *string `pulumi:"expiresAt,optional"`
}

func (p *TenantPolicy) Annotate(a infer.Annotator) {
	a.Describe(&p.ID, "Identifier of the policy within the tenant.")
	a.Describe(&p.Policy, "The policy name.")
	a.Describe(&p.Value, "The policy value.")
	a.Describe(&p.ExpiresAt, "Optional RFC3339 timestamp after which the policy no longer applies.")
}

// TenantArgs are the user-supplied inputs for a tenant.
type TenantArgs struct {
	// VisibleName is the human-readable name shown in the dashboard and CLI.
	VisibleName *string `pulumi:"visibleName,optional"`

	// Labels attached to the tenant. Updating overwrites the whole label set.
	Labels map[string]string `pulumi:"labels,optional"`

	// Policies applied to the tenant.
	Policies []TenantPolicy `pulumi:"policies,optional"`

	// ExternalAccountID links the tenant to an account in your own system. Immutable.
	ExternalAccountID *string `pulumi:"externalAccountId,optional" provider:"replaceOnChanges"`

	// CreatorID is an opaque identifier of the creating user. Immutable.
	CreatorID *string `pulumi:"creatorId,optional" provider:"replaceOnChanges"`
}

// TenantState is the persisted state of a tenant.
type TenantState struct {
	TenantArgs

	// TenantID is the server-assigned tenant identifier.
	TenantID string `pulumi:"tenantId"`

	// CreatedAt is when the tenant was created, as an RFC3339 timestamp.
	CreatedAt string `pulumi:"createdAt"`
}

func (r *Tenant) Annotate(a infer.Annotator) {
	a.Describe(&r, "A Namespace IAM tenant, an isolated account with its own labels and policies. "+
		"Name, labels and policies update in place; external account and creator are immutable.")
}

func (a *TenantArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.VisibleName, "Human-readable tenant name. Updates in place.")
	ann.Describe(&a.Labels, "Labels attached to the tenant. An update overwrites the full label set.")
	ann.Describe(&a.Policies, "Policies applied to the tenant. Some policies can only be set at creation; "+
		"the API does not return policies for other tenants, so they are not refreshed from the server.")
	ann.Describe(&a.ExternalAccountID, "Identifier of the matching account in your own system. "+
		"Changing it replaces the tenant.")
	ann.Describe(&a.CreatorID, "Opaque identifier of the creating user (deprecated upstream). "+
		"Changing it replaces the tenant.")
}

func (s *TenantState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.TenantID, "The server-assigned tenant identifier.")
	ann.Describe(&s.CreatedAt, "RFC3339 timestamp of when the tenant was created.")
}

func policiesToProto(ps []TenantPolicy) ([]*iamv1.TenantPolicy, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	out := make([]*iamv1.TenantPolicy, 0, len(ps))
	for i, p := range ps {
		tp := &iamv1.TenantPolicy{Id: p.ID, Policy: p.Policy, Value: p.Value}
		if p.ExpiresAt != nil && *p.ExpiresAt != "" {
			ts, err := parseRFC3339(fmt.Sprintf("policies[%d].expiresAt", i), *p.ExpiresAt)
			if err != nil {
				return nil, err
			}
			tp.ExpirationPolicy = &iamv1.TenantPolicy_ExpirationPolicy{ExpiresAt: ts}
		}
		out = append(out, tp)
	}
	return out, nil
}

func (r *Tenant) Create(
	ctx context.Context,
	req infer.CreateRequest[TenantArgs],
) (infer.CreateResponse[TenantState], error) {
	in := req.Inputs
	state := TenantState{TenantArgs: in}
	policies, err := policiesToProto(in.Policies)
	if err != nil {
		return infer.CreateResponse[TenantState]{}, err
	}
	if req.DryRun {
		return infer.CreateResponse[TenantState]{ID: "", Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[TenantState]{}, err
	}
	var res iamv1.TenantResponse
	err = c.call(ctx, c.endpoints.iamURL(), tenantService, "CreateTenant", &iamv1.CreateTenantRequest{
		CreatorId:         derefStr(in.CreatorID),
		VisibleName:       derefStr(in.VisibleName),
		Policies:          policies,
		ExternalAccountId: derefStr(in.ExternalAccountID),
		Labels:            labelsToProto(in.Labels),
	}, &res)
	if err != nil {
		return infer.CreateResponse[TenantState]{}, fmt.Errorf("create tenant: %w", err)
	}
	if res.GetTenant().GetId() == "" {
		return infer.CreateResponse[TenantState]{}, fmt.Errorf("create tenant: the API returned no tenant id")
	}
	applyTenant(&state, res.GetTenant())
	return infer.CreateResponse[TenantState]{ID: state.TenantID, Output: state}, nil
}

func (r *Tenant) Read(
	ctx context.Context,
	req infer.ReadRequest[TenantArgs, TenantState],
) (infer.ReadResponse[TenantArgs, TenantState], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.ReadResponse[TenantArgs, TenantState]{}, err
	}
	t, err := findTenant(ctx, c, req.ID)
	if err != nil {
		return infer.ReadResponse[TenantArgs, TenantState]{}, fmt.Errorf("read tenant %q: %w", req.ID, err)
	}
	if t == nil {
		if err := notFoundOnImport("tenant", req.ID, req.Inputs, req.State); err != nil {
			return infer.ReadResponse[TenantArgs, TenantState]{}, err
		}
		return infer.ReadResponse[TenantArgs, TenantState]{}, nil
	}
	state := req.State
	state.VisibleName = emptyToNil(t.GetVisibleName())
	state.Labels = labelsView(req.State.Labels, labelsFromProto(t.GetLabels()), req.State.TenantID == "")
	if v := t.GetExternalAccountId(); v != "" {
		state.ExternalAccountID = &v
	}
	// creatorId is deprecated upstream and may be dropped from responses; keep
	// the declared value unless the server reports one.
	if v := t.GetCreatorId(); v != "" {
		state.CreatorID = &v
	}
	applyTenant(&state, t)
	return infer.ReadResponse[TenantArgs, TenantState]{ID: state.TenantID, Inputs: state.TenantArgs, State: state}, nil
}

func (r *Tenant) Update(
	ctx context.Context,
	req infer.UpdateRequest[TenantArgs, TenantState],
) (infer.UpdateResponse[TenantState], error) {
	state := TenantState{TenantArgs: req.Inputs, TenantID: req.State.TenantID, CreatedAt: req.State.CreatedAt}
	policies, err := policiesToProto(req.Inputs.Policies)
	if err != nil {
		return infer.UpdateResponse[TenantState]{}, err
	}
	if req.DryRun {
		return infer.UpdateResponse[TenantState]{Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[TenantState]{}, err
	}
	var res iamv1.TenantResponse
	err = c.call(ctx, c.endpoints.iamURL(), tenantService, "UpdateTenant", &iamv1.UpdateTenantRequest{
		TenantId:    req.ID,
		VisibleName: derefStr(req.Inputs.VisibleName),
		Policies:    policies,
		Labels:      labelsToProto(req.Inputs.Labels),
	}, &res)
	if err != nil {
		return infer.UpdateResponse[TenantState]{}, fmt.Errorf("update tenant %q: %w", req.ID, err)
	}
	applyTenantIdentity(&state, res.GetTenant())
	return infer.UpdateResponse[TenantState]{Output: state}, nil
}

func (r *Tenant) Delete(
	ctx context.Context,
	req infer.DeleteRequest[TenantState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.iamURL(), tenantService, "RemoveTenant",
		&iamv1.TenantReference{TenantId: req.ID}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("remove tenant %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// applyTenant copies server-owned identity fields onto state.
func applyTenant(state *TenantState, t *iamv1.Tenant) {
	applyTenantIdentity(state, t)
}

func applyTenantIdentity(state *TenantState, t *iamv1.Tenant) {
	if t == nil {
		return
	}
	if id := t.GetId(); id != "" {
		state.TenantID = id
	}
	if ts := formatTimestamp(t.GetCreatedAt()); ts != "" {
		state.CreatedAt = ts
	}
}

// listTenants pages through ListTenants, applying the optional server-side
// filters. The API has no describe-by-id call for tenants, so lookups by ID are
// a filtered walk of this list.
func listTenants(ctx context.Context, c *nsClient, creatorID, externalAccountID string) ([]*iamv1.Tenant, error) {
	var all []*iamv1.Tenant
	err := paginate("ListTenants", func(cursor string) (string, error) {
		req := &iamv1.ListTenantsRequest{Limit: 100}
		if cursor != "" {
			// The API forbids any parameter except limit alongside a cursor:
			// the continuation already carries the original filters.
			req.Cursor = cursor
		} else {
			req.CreatorId = creatorID
			req.ExternalAccountId = externalAccountID
		}
		var res iamv1.ListTenantsResponse
		if err := c.call(ctx, c.endpoints.iamURL(), tenantService, "ListTenants", req, &res); err != nil {
			return "", err
		}
		all = append(all, res.GetTenants()...)
		return res.GetCursor(), nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

func findTenant(ctx context.Context, c *nsClient, id string) (*iamv1.Tenant, error) {
	ts, err := listTenants(ctx, c, "", "")
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.GetId() == id {
			return t, nil
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// getTenants function
// ---------------------------------------------------------------------------

// GetTenants lists IAM tenants.
type GetTenants struct{}

// GetTenantsArgs filters the tenant listing.
type GetTenantsArgs struct {
	// CreatorID restricts the result to tenants created by this identity.
	CreatorID *string `pulumi:"creatorId,optional"`
	// ExternalAccountID restricts the result to tenants linked to this external account.
	ExternalAccountID *string `pulumi:"externalAccountId,optional"`
}

// TenantSummary describes one tenant in a listing.
type TenantSummary struct {
	TenantID          string            `pulumi:"tenantId"`
	VisibleName       string            `pulumi:"visibleName"`
	CreatorID         string            `pulumi:"creatorId"`
	ExternalAccountID string            `pulumi:"externalAccountId"`
	CreatedAt         string            `pulumi:"createdAt"`
	Labels            map[string]string `pulumi:"labels"`
}

// GetTenantsResult is the tenant listing.
type GetTenantsResult struct {
	Tenants []TenantSummary `pulumi:"tenants"`
}

func (f *GetTenants) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the Namespace IAM tenants visible to the configured token, "+
		"optionally filtered by creator or external account.")
}

func (a *GetTenantsArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.CreatorID, "Only return tenants created by this identity.")
	ann.Describe(&a.ExternalAccountID, "Only return tenants linked to this external account.")
}

func (r *GetTenantsResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.Tenants, "The matching tenants.")
}

func (f *GetTenants) Invoke(
	ctx context.Context,
	req infer.FunctionRequest[GetTenantsArgs],
) (infer.FunctionResponse[GetTenantsResult], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetTenantsResult]{}, err
	}
	ts, err := listTenants(ctx, c, derefStr(req.Input.CreatorID), derefStr(req.Input.ExternalAccountID))
	if err != nil {
		return infer.FunctionResponse[GetTenantsResult]{}, fmt.Errorf("list tenants: %w", err)
	}
	out := GetTenantsResult{Tenants: make([]TenantSummary, 0, len(ts))}
	for _, t := range ts {
		out.Tenants = append(out.Tenants, TenantSummary{
			TenantID:          t.GetId(),
			VisibleName:       t.GetVisibleName(),
			CreatorID:         t.GetCreatorId(),
			ExternalAccountID: t.GetExternalAccountId(),
			CreatedAt:         formatTimestamp(t.GetCreatedAt()),
			Labels:            labelsFromProto(t.GetLabels()),
		})
	}
	return infer.FunctionResponse[GetTenantsResult]{Output: out}, nil
}

const tenantMembershipService = "namespace.cloud.iam.v1beta.TenantMembershipService"

// ---- getTenant ----

// GetTenant reads one IAM tenant.
type GetTenant struct{}

type GetTenantArgs struct {
	TenantID          *string `pulumi:"tenantId,optional"`
	ExternalAccountID *string `pulumi:"externalAccountId,optional"`
}

func (f *GetTenant) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one Namespace IAM tenant, by tenant ID or by the external account ID it is linked to. "+
		"Returns the same fields as the Tenant resource reports on import.")
}

func (a *GetTenantArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.TenantID, "The tenant ID. Set exactly one of tenantId and externalAccountId.")
	ann.Describe(&a.ExternalAccountID, "The external account ID the tenant is linked to. Set exactly one of tenantId and externalAccountId.")
}

func (f *GetTenant) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetTenantArgs],
) (infer.FunctionResponse[TenantState], error) {
	type resp = infer.FunctionResponse[TenantState]
	id, ext := derefStr(req.Input.TenantID), derefStr(req.Input.ExternalAccountID)
	if (id == "") == (ext == "") {
		return resp{}, errExactlyOne("getTenant", "tenantId", "externalAccountId")
	}
	if id == "" {
		c, err := clientFor(ctx, nil)
		if err != nil {
			return resp{}, err
		}
		ts, err := listTenants(ctx, c, "", ext)
		if err != nil {
			return resp{}, fmt.Errorf("list tenants: %w", err)
		}
		switch len(ts) {
		case 0:
			return resp{}, errLookupNotFound("tenant with external account", ext)
		case 1:
			id = ts[0].GetId()
		default:
			return resp{}, fmt.Errorf("namespace: %d tenants are linked to external account %q; look one up by tenantId", len(ts), ext)
		}
	}
	st, err := readLookup(ctx, "tenant", id, (&Tenant{}).Read)
	return resp{Output: st}, err
}

// ---- getTenantMembers / getTenantPolicies ----

// GetTenantMembers lists the members of the caller's tenant.
type GetTenantMembers struct{}

type GetTenantMembersArgs struct{}

// TenantMemberInfo is one member of a tenant.
type TenantMemberInfo struct {
	UserID    string `pulumi:"userId"`
	FirstName string `pulumi:"firstName"`
	LastName  string `pulumi:"lastName"`
	Email     string `pulumi:"email"`
	Role      string `pulumi:"role"`
}

type GetTenantMembersResult struct {
	Members []TenantMemberInfo `pulumi:"members"`
}

func (f *GetTenantMembers) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the members of the tenant the token belongs to. The API can list and remove members but not add them.")
}

func (m *TenantMemberInfo) Annotate(ann infer.Annotator) {
	ann.Describe(&m.UserID, "Opaque user ID.")
	ann.Describe(&m.FirstName, "First name.")
	ann.Describe(&m.LastName, "Last name.")
	ann.Describe(&m.Email, "Email address.")
	ann.Describe(&m.Role, "The member's role in the tenant.")
}

func (r *GetTenantMembersResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.Members, "The tenant members.")
}

func (f *GetTenantMembers) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetTenantMembersArgs],
) (infer.FunctionResponse[GetTenantMembersResult], error) {
	type resp = infer.FunctionResponse[GetTenantMembersResult]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	var res iamv1.ListTenantMembersResponse
	if err := c.call(ctx, c.endpoints.iamURL(), tenantMembershipService, "ListTenantMembers",
		&iamv1.ListTenantMembersRequest{}, &res); err != nil {
		return resp{}, fmt.Errorf("list tenant members: %w", err)
	}
	out := GetTenantMembersResult{Members: []TenantMemberInfo{}}
	for _, m := range res.GetMembers() {
		out.Members = append(out.Members, TenantMemberInfo{
			UserID: m.GetUserId(), FirstName: m.GetFirstName(), LastName: m.GetLastName(),
			Email: m.GetEmail(), Role: m.GetRole().String(),
		})
	}
	return resp{Output: out}, nil
}

// GetTenantPolicies reads the policies of the caller's own tenant.
type GetTenantPolicies struct{}

type GetTenantPoliciesArgs struct{}

type GetTenantPoliciesResult struct {
	Policies []TenantPolicy `pulumi:"policies"`
	Revision int            `pulumi:"revision"`
}

func (f *GetTenantPolicies) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads the policies of the tenant the token belongs to.")
}

func (r *GetTenantPoliciesResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.Policies, "The policies of the tenant.")
	ann.Describe(&r.Revision, "Server revision of the policy set.")
}

func (f *GetTenantPolicies) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetTenantPoliciesArgs],
) (infer.FunctionResponse[GetTenantPoliciesResult], error) {
	type resp = infer.FunctionResponse[GetTenantPoliciesResult]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	var res iamv1.DescribePoliciesResponse
	if err := c.call(ctx, c.endpoints.iamURL(), tenantService, "DescribePolicies", &iamv1.DescribePoliciesRequest{}, &res); err != nil {
		return resp{}, fmt.Errorf("read tenant policies: %w", err)
	}
	out := GetTenantPoliciesResult{Policies: []TenantPolicy{}, Revision: int(res.GetRevision())}
	for _, p := range res.GetPolicies() {
		tp := TenantPolicy{ID: p.GetId(), Policy: p.GetPolicy(), Value: p.GetValue()}
		if e := p.GetExpirationPolicy().GetExpiresAt(); e != nil {
			tp.ExpiresAt = ptr(formatTimestamp(e))
		}
		out.Policies = append(out.Policies, tp)
	}
	return resp{Output: out}, nil
}
