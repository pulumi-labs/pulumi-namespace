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

	networkv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/network/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// EgressPolicy resource
// ---------------------------------------------------------------------------

// EgressPolicy is a named egress (outbound network) policy that runner
// profiles and Buildkite queues reference by tag.
//
// The API has no delete: destroying the resource sets the policy to the
// non-enforcing DISABLED mode with every rule removed. The tag itself remains
// on the Namespace side.
type EgressPolicy struct{}

// EgressMatcher selects the destinations a rule applies to.
type EgressMatcher struct {
	MatchDomains []string `pulumi:"matchDomains,optional" json:"matchDomains,omitempty"`
}

func (m *EgressMatcher) Annotate(a infer.Annotator) {
	a.Describe(&m.MatchDomains, "Domains the rule matches. A leading wildcard such as '*.example.com' is allowed.")
}

// EgressProxy routes matching traffic through another domain.
type EgressProxy struct {
	ViaDomain string `pulumi:"viaDomain" json:"viaDomain"`
}

func (p *EgressProxy) Annotate(a infer.Annotator) {
	a.Describe(&p.ViaDomain, "The domain traffic is proxied through.")
}

// EgressHeader is an HTTP header of an endpoint call.
type EgressHeader struct {
	Name  string  `pulumi:"name" json:"name"`
	Value *string `pulumi:"value,optional" json:"value,omitempty" provider:"secret"`
}

func (h *EgressHeader) Annotate(a infer.Annotator) {
	a.Describe(&h.Name, "Header name.")
	a.Describe(&h.Value, "Header value. Stored as a Pulumi secret.")
}

// EgressFromEndpoint fetches the injected value from an HTTP endpoint.
type EgressFromEndpoint struct {
	URL     string         `pulumi:"url" json:"url"`
	Headers []EgressHeader `pulumi:"headers,optional" json:"headers,omitempty"`
}

func (e *EgressFromEndpoint) Annotate(a infer.Annotator) {
	a.Describe(&e.URL, "Endpoint URL the value is fetched from.")
	a.Describe(&e.Headers, "Headers sent to the endpoint.")
}

// EgressInject injects a header into matching outbound requests.
type EgressInject struct {
	HeaderName       string              `pulumi:"headerName" json:"headerName"`
	FromSecretID     *string             `pulumi:"fromSecretId,optional" json:"fromSecretId,omitempty"`
	Value            *string             `pulumi:"value,optional" json:"value,omitempty" provider:"secret"`
	FromEndpoint     *EgressFromEndpoint `pulumi:"fromEndpoint,optional" json:"fromEndpoint,omitempty"`
	ResolutionPolicy *string             `pulumi:"resolutionPolicy,optional" json:"resolutionPolicy,omitempty"`
}

func (i *EgressInject) Annotate(a infer.Annotator) {
	a.Describe(&i.HeaderName, "Name of the header to inject.")
	a.Describe(&i.FromSecretID, "Vault secret ID whose value is injected. Set exactly one of fromSecretId, value and fromEndpoint.")
	a.Describe(&i.Value, "A literal value to inject. Stored as a Pulumi secret. Prefer fromSecretId.")
	a.Describe(&i.FromEndpoint, "An endpoint the value is fetched from.")
	a.Describe(&i.ResolutionPolicy, "When the value is resolved: RESOLUTION_POLICY_ONCE or RESOLUTION_POLICY_WHEN_STALE.")
}

// EgressRule is one rule of a policy.
type EgressRule struct {
	Op      string         `pulumi:"op" json:"op"`
	Matcher *EgressMatcher `pulumi:"matcher,optional" json:"matcher,omitempty"`
	Proxy   *EgressProxy   `pulumi:"proxy,optional" json:"proxy,omitempty"`
	Inject  *EgressInject  `pulumi:"inject,optional" json:"inject,omitempty"`
}

func (r *EgressRule) Annotate(a infer.Annotator) {
	a.Describe(&r.Op, "Rule operation: ALLOW, PROXY (requires proxy) or INJECT (requires inject).")
	a.Describe(&r.Matcher, "Destinations the rule applies to.")
	a.Describe(&r.Proxy, "Proxy options, for op PROXY.")
	a.Describe(&r.Inject, "Header injection options, for op INJECT.")
}

// EgressPolicyArgs are the user-supplied inputs of an egress policy.
type EgressPolicyArgs struct {
	Tag                  string       `pulumi:"tag" json:"tag" provider:"replaceOnChanges"`
	Description          *string      `pulumi:"description,optional" json:"description,omitempty"`
	Mode                 string       `pulumi:"mode" json:"mode"`
	DeepPacketInspection *bool        `pulumi:"deepPacketInspection,optional" json:"deepPacketInspection,omitempty"`
	Rules                []EgressRule `pulumi:"rules,optional" json:"rules,omitempty"`
	AdditionalRulesets   []string     `pulumi:"additionalRulesets,optional" json:"additionalRulesets,omitempty"`
}

// EgressPolicyState is the persisted state of an egress policy.
type EgressPolicyState struct {
	EgressPolicyArgs

	// Revision is the server revision used for optimistic concurrency.
	Revision int `pulumi:"revision"`
	// Adopted is true when the tag already existed at create time.
	Adopted bool `pulumi:"adopted"`
}

func (r *EgressPolicy) Annotate(a infer.Annotator) {
	a.Describe(&r, "A named egress policy (outbound network rules) referenced by tag from GithubProfile and "+
		"BuildkiteQueue. Updates use the server revision for optimistic concurrency. If the tag already exists "+
		"at create time the policy is adopted and overwritten with the declared spec ('adopted' is set to true). "+
		"The Namespace API has no delete for egress policies: destroying this resource sets the policy to mode "+
		"DISABLED with all rules, rulesets and allowed domains cleared, and removes it from Pulumi state; the tag "+
		"itself remains on Namespace. Import with the policy tag.")
}

func (a *EgressPolicyArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Tag, "The policy tag other resources reference. Changing it replaces the policy.")
	ann.Describe(&a.Description, "A description of the policy.")
	ann.Describe(&a.Mode, "Enforcement mode: DISABLED, ALLOW_ALL, ADVISORY (report only) or BLOCK.")
	ann.Describe(&a.DeepPacketInspection, "Enable deep packet inspection (required for header injection).")
	ann.Describe(&a.Rules, "Ordered rules of the policy.")
	ann.Describe(&a.AdditionalRulesets, "Names of predefined rulesets to include.")
}

func (s *EgressPolicyState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.Revision, "The server revision of the policy; changes on every update.")
	ann.Describe(&s.Adopted, "True when the policy tag already existed at create time and was taken over.")
}

func (a EgressPolicyArgs) validate() []pgp.CheckFailure {
	var f []pgp.CheckFailure
	f = append(f, requireNonEmpty("tag", a.Tag)...)
	f = append(f, failIfNotIn("mode", a.Mode, "DISABLED", "ALLOW_ALL", "ADVISORY", "BLOCK")...)
	for i, r := range a.Rules {
		p := fmt.Sprintf("rules[%d]", i)
		f = append(f, failIfNotIn(p+".op", r.Op, "ALLOW", "PROXY", "INJECT")...)
		switch r.Op {
		case "PROXY":
			if r.Proxy == nil || r.Proxy.ViaDomain == "" {
				f = append(f, fail(p+".proxy", "op PROXY requires proxy.viaDomain"))
			}
		case "INJECT":
			if r.Inject == nil || r.Inject.HeaderName == "" {
				f = append(f, fail(p+".inject", "op INJECT requires inject.headerName"))
				continue
			}
			n := 0
			if r.Inject.FromSecretID != nil {
				n++
			}
			if r.Inject.Value != nil {
				n++
			}
			if r.Inject.FromEndpoint != nil {
				n++
			}
			if n != 1 {
				f = append(f, fail(p+".inject", "set exactly one of fromSecretId, value and fromEndpoint"))
			}
			f = append(f, optIn(p+".inject.resolutionPolicy", r.Inject.ResolutionPolicy,
				"RESOLUTION_POLICY_ONCE", "RESOLUTION_POLICY_WHEN_STALE")...)
		}
	}
	return f
}

func (r *EgressPolicy) Check(
	ctx context.Context, req infer.CheckRequest,
) (infer.CheckResponse[EgressPolicyArgs], error) {
	return checkWith(ctx, req, EgressPolicyArgs.validate)
}

// egressPolicyMessage builds the API policy. ALLOW rules carry an empty
// 'allow' options message, which has no Pulumi-side fields.
func egressPolicyMessage(a EgressPolicyArgs) (*networkv1.EgressPolicy, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	var spec map[string]any
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, err
	}
	// tag and description live on the policy, not in its spec.
	delete(spec, "tag")
	delete(spec, "description")
	if rules, ok := spec["rules"].([]any); ok {
		for _, r := range rules {
			if rm, ok := r.(map[string]any); ok && rm["op"] == "ALLOW" {
				rm["allow"] = map[string]any{}
			}
		}
	}
	var out networkv1.EgressPolicy
	if err := toProto(map[string]any{"tag": a.Tag, "description": derefStr(a.Description), "spec": spec}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// applyEgress reads a server policy into state. declared is nil on import.
// Secret values the server does not echo back are carried over from the
// declared inputs so Read never clobbers them.
func applyEgress(state *EgressPolicyState, p *networkv1.EgressPolicy, rev int64, declared *EgressPolicyArgs) error {
	var srv EgressPolicyArgs
	if err := fromProto(p.GetSpec(), &srv); err != nil {
		return err
	}
	srv.Tag = p.GetTag()
	srv.Description = emptyToNil(p.GetDescription())
	if declared != nil {
		keepDeclared(*declared, &srv)
		for i := range srv.Rules {
			if i >= len(declared.Rules) || srv.Rules[i].Inject == nil || declared.Rules[i].Inject == nil {
				continue
			}
			d, s := declared.Rules[i].Inject, srv.Rules[i].Inject
			if s.Value == nil && d.Value != nil {
				s.Value = d.Value
			}
			if s.FromEndpoint != nil && d.FromEndpoint != nil {
				for j := range s.FromEndpoint.Headers {
					if j < len(d.FromEndpoint.Headers) && s.FromEndpoint.Headers[j].Value == nil {
						s.FromEndpoint.Headers[j].Value = d.FromEndpoint.Headers[j].Value
					}
				}
			}
		}
	}
	state.EgressPolicyArgs = srv
	state.Revision = int(rev)
	return nil
}

func getEgress(ctx context.Context, c *nsClient, tag string) (*networkv1.GetEgressPolicyResponse, error) {
	var res networkv1.GetEgressPolicyResponse
	if err := c.call(ctx, c.endpoints.globalURL(), egressPolicyService, "GetEgressPolicy",
		&networkv1.GetEgressPolicyRequest{Tag: tag}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// Diff deletes before replacing: EgressPolicy adopts and later restores server-side
// state, so with the default create-before-delete order a forced replace
// (--replace, --target-replace, taint) would run the old resource's delete
// after the new create and undo what the new one adopted.
func (r *EgressPolicy) Diff(
	_ context.Context, req infer.DiffRequest[EgressPolicyArgs, EgressPolicyState],
) (infer.DiffResponse, error) {
	return diffDeleteFirst(req.State.EgressPolicyArgs, req.Inputs), nil
}

func (r *EgressPolicy) Create(
	ctx context.Context, req infer.CreateRequest[EgressPolicyArgs],
) (infer.CreateResponse[EgressPolicyState], error) {
	state := EgressPolicyState{EgressPolicyArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[EgressPolicyState]{ID: req.Inputs.Tag, Output: state}, nil
	}
	pol, err := egressPolicyMessage(req.Inputs)
	if err != nil {
		return infer.CreateResponse[EgressPolicyState]{}, err
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[EgressPolicyState]{}, err
	}
	tag := req.Inputs.Tag
	existing, err := getEgress(ctx, c, tag)
	switch {
	case err == nil:
		// The tag exists: adopt it by updating against its current revision.
		pgp.GetLogger(ctx).Warningf("egress policy %q already exists; adopting it and overwriting its spec", tag)
		var up networkv1.UpdateEgressPolicyResponse
		if err := c.call(ctx, c.endpoints.globalURL(), egressPolicyService, "UpdateEgressPolicy",
			&networkv1.UpdateEgressPolicyRequest{Policy: pol, MatchRevision: existing.GetRevision()}, &up); err != nil {
			return infer.CreateResponse[EgressPolicyState]{}, fmt.Errorf("adopt egress policy %q: %w", tag, err)
		}
		state.Adopted = true
		state.Revision = int(up.GetRevision())
	case isNotFound(err):
		var cr networkv1.CreateEgressPolicyResponse
		if err := c.call(ctx, c.endpoints.globalURL(), egressPolicyService, "CreateEgressPolicy",
			&networkv1.CreateEgressPolicyRequest{Policy: pol}, &cr); err != nil {
			return infer.CreateResponse[EgressPolicyState]{}, fmt.Errorf("create egress policy %q: %w", tag, err)
		}
		state.Revision = int(cr.GetRevision())
	default:
		return infer.CreateResponse[EgressPolicyState]{}, fmt.Errorf("look up egress policy %q: %w", tag, err)
	}
	return infer.CreateResponse[EgressPolicyState]{ID: tag, Output: state}, nil
}

func (r *EgressPolicy) Read(
	ctx context.Context, req infer.ReadRequest[EgressPolicyArgs, EgressPolicyState],
) (infer.ReadResponse[EgressPolicyArgs, EgressPolicyState], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.ReadResponse[EgressPolicyArgs, EgressPolicyState]{}, err
	}
	res, err := getEgress(ctx, c, req.ID)
	if isNotFound(err) {
		if err := notFoundOnImport("egress policy", req.ID, req.Inputs, req.State); err != nil {
			return infer.ReadResponse[EgressPolicyArgs, EgressPolicyState]{}, err
		}
		return infer.ReadResponse[EgressPolicyArgs, EgressPolicyState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[EgressPolicyArgs, EgressPolicyState]{}, fmt.Errorf("read egress policy %q: %w", req.ID, err)
	}
	state := EgressPolicyState{Adopted: req.State.Adopted}
	var declared *EgressPolicyArgs
	if req.Inputs.Tag != "" {
		d := req.Inputs
		declared = &d
	}
	if err := applyEgress(&state, res.GetPolicy(), res.GetRevision(), declared); err != nil {
		return infer.ReadResponse[EgressPolicyArgs, EgressPolicyState]{}, err
	}
	state.Tag = req.ID
	return infer.ReadResponse[EgressPolicyArgs, EgressPolicyState]{ID: req.ID, Inputs: state.EgressPolicyArgs, State: state}, nil
}

func (r *EgressPolicy) Update(
	ctx context.Context, req infer.UpdateRequest[EgressPolicyArgs, EgressPolicyState],
) (infer.UpdateResponse[EgressPolicyState], error) {
	state := req.State
	state.EgressPolicyArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[EgressPolicyState]{Output: state}, nil
	}
	pol, err := egressPolicyMessage(req.Inputs)
	if err != nil {
		return infer.UpdateResponse[EgressPolicyState]{}, err
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[EgressPolicyState]{}, err
	}
	var up networkv1.UpdateEgressPolicyResponse
	if err := c.call(ctx, c.endpoints.globalURL(), egressPolicyService, "UpdateEgressPolicy",
		&networkv1.UpdateEgressPolicyRequest{Policy: pol, MatchRevision: int64(req.State.Revision)}, &up); err != nil {
		return infer.UpdateResponse[EgressPolicyState]{}, fmt.Errorf("update egress policy %q: %w", req.ID, err)
	}
	state.Revision = int(up.GetRevision())
	return infer.UpdateResponse[EgressPolicyState]{Output: state}, nil
}

// Delete neutralizes the policy: the API cannot delete one.
func (r *EgressPolicy) Delete(
	ctx context.Context, req infer.DeleteRequest[EgressPolicyState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	cur, err := getEgress(ctx, c, req.ID)
	if isNotFound(err) {
		return infer.DeleteResponse{}, nil
	}
	if err != nil {
		return infer.DeleteResponse{}, fmt.Errorf("delete egress policy %q: %w", req.ID, err)
	}
	neutral := &networkv1.EgressPolicy{
		Tag:         req.ID,
		Description: cur.GetPolicy().GetDescription(),
		Spec:        &networkv1.EgressPolicySpec{Mode: networkv1.EgressPolicySpec_DISABLED},
	}
	if err := c.call(ctx, c.endpoints.globalURL(), egressPolicyService, "UpdateEgressPolicy",
		&networkv1.UpdateEgressPolicyRequest{Policy: neutral, MatchRevision: cur.GetRevision()}, nil); err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("disable egress policy %q on destroy: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// ---- getEgressPolicy / getEgressPolicies ----

const egressListMethod = "ListEgressPolicies"

// EgressPolicyInfo is an egress policy as the API reports it.
type EgressPolicyInfo struct {
	Tag                  string       `pulumi:"tag"`
	Description          *string      `pulumi:"description,optional"`
	Mode                 string       `pulumi:"mode"`
	DeepPacketInspection *bool        `pulumi:"deepPacketInspection,optional"`
	Rules                []EgressRule `pulumi:"rules"`
	AdditionalRulesets   []string     `pulumi:"additionalRulesets"`
	Revision             int          `pulumi:"revision"`
}

func (e *EgressPolicyInfo) Annotate(ann infer.Annotator) {
	ann.Describe(&e.Tag, "The policy tag.")
	ann.Describe(&e.Description, "The policy description.")
	ann.Describe(&e.Mode, "Enforcement mode: DISABLED, ALLOW_ALL, ADVISORY or BLOCK.")
	ann.Describe(&e.DeepPacketInspection, "Whether deep packet inspection is enabled.")
	ann.Describe(&e.Rules, "Ordered rules of the policy. Secret inject values are redacted by the API.")
	ann.Describe(&e.AdditionalRulesets, "Names of predefined rulesets included.")
	ann.Describe(&e.Revision, "The server revision of the policy.")
}

func egressInfo(p *networkv1.EgressPolicy, rev int64) (EgressPolicyInfo, error) {
	var st EgressPolicyState
	if err := applyEgress(&st, p, rev, nil); err != nil {
		return EgressPolicyInfo{}, err
	}
	a := st.EgressPolicyArgs
	info := EgressPolicyInfo{
		Tag: a.Tag, Description: a.Description, Mode: a.Mode, DeepPacketInspection: a.DeepPacketInspection,
		Rules: a.Rules, AdditionalRulesets: a.AdditionalRulesets, Revision: st.Revision,
	}
	if info.Rules == nil {
		info.Rules = []EgressRule{}
	}
	if info.AdditionalRulesets == nil {
		info.AdditionalRulesets = []string{}
	}
	return info, nil
}

// GetEgressPolicy reads one egress policy.
type GetEgressPolicy struct{}

type GetEgressPolicyArgs struct {
	Tag string `pulumi:"tag"`
}

func (f *GetEgressPolicy) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one egress policy by tag.")
}

func (a *GetEgressPolicyArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Tag, "The policy tag.")
}

func (f *GetEgressPolicy) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetEgressPolicyArgs],
) (infer.FunctionResponse[EgressPolicyInfo], error) {
	type resp = infer.FunctionResponse[EgressPolicyInfo]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	res, err := getEgress(ctx, c, req.Input.Tag)
	if isNotFound(err) || (err == nil && res.GetPolicy() == nil) {
		return resp{}, errLookupNotFound("egress policy", req.Input.Tag)
	}
	if err != nil {
		return resp{}, fmt.Errorf("read egress policy %q: %w", req.Input.Tag, err)
	}
	info, err := egressInfo(res.GetPolicy(), res.GetRevision())
	return resp{Output: info}, err
}

// GetEgressPolicies lists egress policies.
type GetEgressPolicies struct{}

type GetEgressPoliciesArgs struct{}

type GetEgressPoliciesResult struct {
	Policies []EgressPolicyInfo `pulumi:"policies"`
}

func (f *GetEgressPolicies) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the egress policies of the current tenant.")
}

func (r *GetEgressPoliciesResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.Policies, "The egress policies.")
}

func (f *GetEgressPolicies) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetEgressPoliciesArgs],
) (infer.FunctionResponse[GetEgressPoliciesResult], error) {
	type resp = infer.FunctionResponse[GetEgressPoliciesResult]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	var res networkv1.ListEgressPoliciesResponse
	if err := c.call(ctx, c.endpoints.globalURL(), egressPolicyService, egressListMethod,
		&networkv1.ListEgressPoliciesRequest{}, &res); err != nil {
		return resp{}, fmt.Errorf("list egress policies: %w", err)
	}
	out := GetEgressPoliciesResult{Policies: []EgressPolicyInfo{}}
	for _, e := range res.GetPolicies() {
		info, err := egressInfo(e.GetPolicy(), e.GetRevision())
		if err != nil {
			return resp{}, err
		}
		out.Policies = append(out.Policies, info)
	}
	return resp{Output: out}, nil
}
