package provider

import (
	"context"
	"fmt"

	iamv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/iam/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// RevokableToken resource
// ---------------------------------------------------------------------------

// RevokableToken is a bearer token that can be revoked and refreshed. The
// bearer value is only returned by the API at creation time.
type RevokableToken struct{}

var tokenScopes = []string{"TENANT_SCOPE", "TENANT_MEMBERSHIP_SCOPE"}

// TokenPolicy is a typed policy attached to the token.
type TokenPolicy struct {
	PolicyType string `pulumi:"policyType" json:"policyType"`
	Value      string `pulumi:"value" json:"value"`
}

func (p *TokenPolicy) Annotate(a infer.Annotator) {
	a.Describe(&p.PolicyType, "Policy type.")
	a.Describe(&p.Value, "Policy value.")
}

// TokenGrant is one permission granted to the token.
type TokenGrant struct {
	ResourceType string   `pulumi:"resourceType" json:"resourceType"`
	ResourceID   string   `pulumi:"resourceId" json:"resourceId"`
	Actions      []string `pulumi:"actions" json:"actions"`
}

func (g *TokenGrant) Annotate(a infer.Annotator) {
	a.Describe(&g.ResourceType, "Resource type the grant applies to.")
	a.Describe(&g.ResourceID, "Resource ID the grant applies to.")
	a.Describe(&g.Actions, "Allowed actions.")
}

type RevokableTokenArgs struct {
	Name        string        `pulumi:"name" provider:"replaceOnChanges"`
	Description *string       `pulumi:"description,optional" provider:"replaceOnChanges"`
	ExpiresAt   *string       `pulumi:"expiresAt,optional" provider:"replaceOnChanges"`
	Scope       *string       `pulumi:"scope,optional" provider:"replaceOnChanges"`
	Policies    []TokenPolicy `pulumi:"policies,optional" provider:"replaceOnChanges"`
	Grants      []TokenGrant  `pulumi:"grants,optional" provider:"replaceOnChanges"`

	// RefreshTrigger: changing the value calls RefreshRevokableToken in place.
	RefreshTrigger *string `pulumi:"refreshTrigger,optional"`
	// RefreshMinimumSeconds is the minimum remaining lifetime requested by a refresh.
	RefreshMinimumSeconds *int `pulumi:"refreshMinimumSeconds,optional"`
}

type RevokableTokenState struct {
	RevokableTokenArgs

	TokenID     string `pulumi:"tokenId"`
	TenantID    string `pulumi:"tenantId"`
	BearerToken string `pulumi:"bearerToken" provider:"secret"`
	TokenState  string `pulumi:"tokenState"`
	CreatedAt   string `pulumi:"createdAt"`
	// TokenExpiresAt is the current server-side expiry (it moves on refresh).
	TokenExpiresAt string `pulumi:"tokenExpiresAt"`
}

func (r *RevokableToken) Annotate(a infer.Annotator) {
	a.Describe(&r, "A revokable Namespace bearer token. The bearer value is exposed as the secret output "+
		"'bearerToken' and is only available at creation (it cannot be read back, so tokens "+
		"cannot be imported). All identity-defining inputs replace the token. Changing 'refreshTrigger' extends the token in "+
		"place via RefreshRevokableToken. Destroying revokes the token.")
}

func (a *RevokableTokenArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Name, "Token name. Changing it replaces the token.")
	ann.Describe(&a.Description, "Token description. Changing it replaces the token.")
	ann.Describe(&a.ExpiresAt, "RFC3339 expiry. Changing it replaces the token; use refreshTrigger to extend instead.")
	ann.Describe(&a.Scope, "TENANT_SCOPE or TENANT_MEMBERSHIP_SCOPE. Changing it replaces the token.")
	ann.Describe(&a.Policies, "Typed policies attached to the token. Changing them replaces the token.")
	ann.Describe(&a.Grants, "Permission grants of the token. Changing them replaces the token.")
	ann.Describe(&a.RefreshTrigger, "Arbitrary string; changing it refreshes (extends) the token in place.")
	ann.Describe(&a.RefreshMinimumSeconds, "Minimum remaining lifetime in seconds requested when refreshing.")
}

func (s *RevokableTokenState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.TokenID, "The token identifier.")
	ann.Describe(&s.TenantID, "The tenant the token belongs to.")
	ann.Describe(&s.BearerToken, "The bearer token value. Secret. Only known when this provider created the token.")
	ann.Describe(&s.TokenState, "ACTIVE, REVOKED or EXPIRED.")
	ann.Describe(&s.CreatedAt, "RFC3339 creation time.")
	ann.Describe(&s.TokenExpiresAt, "RFC3339 current expiry; moves forward after a refresh.")
}

func (r *RevokableToken) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[RevokableTokenArgs], error) {
	return checkWith(ctx, req, func(a RevokableTokenArgs) []pgp.CheckFailure {
		f := requireNonEmpty("name", a.Name)
		f = append(f, optIn("scope", a.Scope, tokenScopes...)...)
		if a.ExpiresAt != nil {
			if _, err := parseRFC3339("expiresAt", *a.ExpiresAt); err != nil {
				f = append(f, fail("expiresAt", "must be an RFC3339 timestamp"))
			}
		}
		if a.RefreshMinimumSeconds != nil && *a.RefreshMinimumSeconds <= 0 {
			f = append(f, fail("refreshMinimumSeconds", "must be greater than 0"))
		}
		for i, g := range a.Grants {
			if g.ResourceType == "" {
				f = append(f, fail(fmt.Sprintf("grants[%d].resourceType", i), "must not be empty"))
			}
		}
		return f
	})
}

func tokenCreateBody(in RevokableTokenArgs) map[string]any {
	body := map[string]any{"name": in.Name}
	if in.Description != nil {
		body["description"] = *in.Description
	}
	if in.ExpiresAt != nil {
		body["expiresAt"] = *in.ExpiresAt
	}
	if in.Scope != nil {
		body["scope"] = *in.Scope
	}
	if len(in.Policies) > 0 {
		body["policies"] = in.Policies
	}
	if len(in.Grants) > 0 {
		body["access"] = map[string]any{"grants": in.Grants}
	}
	return body
}

func applyToken(state *RevokableTokenState, t *iamv1.RevokableToken) {
	state.TokenID = t.GetTokenId()
	state.TenantID = t.GetTenantId()
	state.TokenState = t.GetState().String()
	state.CreatedAt = formatTimestamp(t.GetCreatedAt())
	state.TokenExpiresAt = formatTimestamp(t.GetExpiresAt())
}

func (r *RevokableToken) Create(
	ctx context.Context, req infer.CreateRequest[RevokableTokenArgs],
) (infer.CreateResponse[RevokableTokenState], error) {
	state := RevokableTokenState{RevokableTokenArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[RevokableTokenState]{ID: "preview", Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[RevokableTokenState]{}, err
	}
	var pb iamv1.CreateRevokableTokenRequest
	if err := toProto(tokenCreateBody(req.Inputs), &pb); err != nil {
		return infer.CreateResponse[RevokableTokenState]{}, fmt.Errorf("create revokable token: %w", err)
	}
	var res iamv1.CreateRevokableTokenResponse
	if err := c.call(ctx, c.endpoints.iamURL(), tokenService, "CreateRevokableToken", &pb, &res); err != nil {
		return infer.CreateResponse[RevokableTokenState]{}, fmt.Errorf("create revokable token: %w", err)
	}
	applyToken(&state, res.GetToken())
	state.BearerToken = res.GetBearerToken()
	return infer.CreateResponse[RevokableTokenState]{ID: state.TokenID, Output: state}, nil
}

func findToken(ctx context.Context, c *nsClient, id string) (*iamv1.RevokableToken, error) {
	var found *iamv1.RevokableToken
	err := paginate("ListRevokableTokens", func(cursor []byte) ([]byte, error) {
		var res iamv1.ListRevokableTokensResponse
		if err := c.call(ctx, c.endpoints.iamURL(), tokenService, "ListRevokableTokens",
			&iamv1.ListRevokableTokensRequest{PaginationCursor: cursor, MaxEntries: 100, IncludeRevoked: true}, &res); err != nil {
			return nil, err
		}
		for _, t := range res.GetTokens() {
			if t.GetTokenId() == id {
				found = t
				return nil, nil
			}
		}
		return res.GetPaginationCursor(), nil
	})
	return found, err
}

func (r *RevokableToken) Read(
	ctx context.Context, req infer.ReadRequest[RevokableTokenArgs, RevokableTokenState],
) (infer.ReadResponse[RevokableTokenArgs, RevokableTokenState], error) {
	type resp = infer.ReadResponse[RevokableTokenArgs, RevokableTokenState]
	if req.State.TokenID == "" {
		// Import: the bearer secret is only returned by CreateRevokableToken and
		// cannot be recovered. An imported resource would hold no secret, and a
		// later refresh trigger would replace the token while delete revokes the
		// one that was imported, so refuse instead of advertising a broken path.
		return resp{}, fmt.Errorf("import revokable token %q: import is not supported because the bearer "+
			"token is only returned at creation and cannot be recovered; create the token with Pulumi instead", req.ID)
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	t, err := findToken(ctx, c, req.ID)
	if err != nil {
		return resp{}, fmt.Errorf("read revokable token %q: %w", req.ID, err)
	}
	if t == nil || t.GetState() == iamv1.RevokableToken_REVOKED {
		return resp{}, nil // revoked tokens are unusable: report gone so it is recreated
	}
	state := req.State // keeps the secret bearer token and declared inputs
	if state.Name == "" {
		state.Name = t.GetName()
		state.Description = emptyToNil(t.GetDescription())
	}
	applyToken(&state, t)
	return resp{ID: req.ID, Inputs: state.RevokableTokenArgs, State: state}, nil
}

func (r *RevokableToken) Update(
	ctx context.Context, req infer.UpdateRequest[RevokableTokenArgs, RevokableTokenState],
) (infer.UpdateResponse[RevokableTokenState], error) {
	state := req.State
	state.RevokableTokenArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[RevokableTokenState]{Output: state}, nil
	}
	if derefStr(req.Inputs.RefreshTrigger) != derefStr(req.State.RefreshTrigger) {
		c, err := clientFor(ctx, nil)
		if err != nil {
			return infer.UpdateResponse[RevokableTokenState]{}, err
		}
		rq := &iamv1.RefreshRevokableTokenRequest{TokenId: req.ID}
		if req.Inputs.RefreshMinimumSeconds != nil {
			var d iamv1.RefreshRevokableTokenRequest
			if err := toProto(map[string]any{"tokenId": req.ID,
				"minimumDuration": secondsToDuration(*req.Inputs.RefreshMinimumSeconds)}, &d); err != nil {
				return infer.UpdateResponse[RevokableTokenState]{}, err
			}
			rq = &d
		}
		var res iamv1.RefreshRevokableTokenResponse
		if err := c.call(ctx, c.endpoints.iamURL(), tokenService, "RefreshRevokableToken", rq, &res); err != nil {
			return infer.UpdateResponse[RevokableTokenState]{}, fmt.Errorf("refresh revokable token %q: %w", req.ID, err)
		}
		applyToken(&state, res.GetToken())
	}
	return infer.UpdateResponse[RevokableTokenState]{Output: state}, nil
}

func (r *RevokableToken) Delete(
	ctx context.Context, req infer.DeleteRequest[RevokableTokenState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.iamURL(), tokenService, "RevokeRevokableToken",
		&iamv1.RevokeRevokableTokenRequest{TokenId: req.ID}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("revoke revokable token %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}
