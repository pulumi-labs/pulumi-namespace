package provider

import (
	"context"
	"fmt"

	registryv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/registry/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ---------------------------------------------------------------------------
// Registry expiration policies (adopt-style singletons)
//
// The container registry always has a default policy and every repository can
// have a policy of its own. Neither can be created or deleted, only updated, so
// both resources record the values found at create time ("prior") and restore
// them on destroy.
// ---------------------------------------------------------------------------

var enforcements = []string{"EXPIRATION_ENFORCEMENT_ENFORCE", "EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE"}

// regPolicy is the neutral view of a registry policy.
type regPolicy struct {
	Revision    int64
	Seconds     *int
	Enforcement *string
}

// enforcementPtr maps the enum to its name, treating the zero value
// (EXPIRATION_ENFORCEMENT_UNKNOWN, what the API reports for "never set") as nil.
// emptyToNil(enum.String()) cannot do this because UNKNOWN prints as a name.
func enforcementPtr(e registryv1.ExpirationEnforcement) *string {
	if e == registryv1.ExpirationEnforcement_EXPIRATION_ENFORCEMENT_UNKNOWN {
		return nil
	}
	n := e.String()
	return &n
}

func secsPtr(s int64) *int {
	if s == 0 {
		return nil
	}
	v := int(s)
	return &v
}

// policyBody builds the protojson-shaped request body shared by both updates.
func policyBody(expirationField string, p regPolicy) map[string]any {
	body := map[string]any{"revision": p.Revision}
	if p.Seconds != nil {
		body[expirationField] = secondsToDuration(*p.Seconds)
	}
	if p.Enforcement != nil {
		body["expirationEnforcement"] = *p.Enforcement
	}
	return body
}

func getDefaultPolicy(ctx context.Context, c *nsClient) (regPolicy, error) {
	var res registryv1.GetDefaultPolicyResponse
	if err := c.call(ctx, c.endpoints.globalURL(), registryService, "GetDefaultPolicy", &emptypb.Empty{}, &res); err != nil {
		return regPolicy{}, err
	}
	return regPolicy{Revision: res.GetRevision(), Seconds: secsPtr(res.GetDefaultExpiration().GetSeconds()),
		Enforcement: enforcementPtr(res.GetExpirationEnforcement())}, nil
}

func putDefaultPolicy(ctx context.Context, c *nsClient, p regPolicy) error {
	var req registryv1.UpdateDefaultPolicyRequest
	if err := toProto(policyBody("defaultExpiration", p), &req); err != nil {
		return err
	}
	return c.call(ctx, c.endpoints.globalURL(), registryService, "UpdateDefaultPolicy", &req, nil)
}

func getRepoPolicy(ctx context.Context, c *nsClient, repo string) (regPolicy, error) {
	var res registryv1.GetRepositoryPolicyResponse
	if err := c.call(ctx, c.endpoints.globalURL(), registryService, "GetRepositoryPolicy",
		&registryv1.GetRepositoryPolicyRequest{Repository: repo}, &res); err != nil {
		return regPolicy{}, err
	}
	return regPolicy{Revision: res.GetRevision(), Seconds: secsPtr(res.GetExpiration().GetSeconds()),
		Enforcement: enforcementPtr(res.GetExpirationEnforcement())}, nil
}

func putRepoPolicy(ctx context.Context, c *nsClient, repo string, p regPolicy) error {
	body := policyBody("expiration", p)
	body["repository"] = repo
	var req registryv1.UpdateRepositoryPolicyRequest
	if err := toProto(body, &req); err != nil {
		return err
	}
	return c.call(ctx, c.endpoints.globalURL(), registryService, "UpdateRepositoryPolicy", &req, nil)
}

// restoreTo returns the policy that puts the registry back exactly as recorded:
// the recorded values verbatim at the server's current revision. The Update
// RPCs replace the whole policy (the requests carry no field mask), so a field
// that was unset when recorded is simply omitted, which clears it. For a
// repository, an omitted expiration means "use the default policy" per the API
// docs. For the registry default, the API does not document what an omitted
// default expiration means; it is sent omitted because that is what was read.
func restoreTo(cur regPolicy, secs *int, enf *string) regPolicy {
	return regPolicy{Revision: cur.Revision, Seconds: secs, Enforcement: enf}
}

// merge overlays the declared values on cur: unset inputs leave the server
// value alone.
func (p regPolicy) merge(secs *int, enf *string) regPolicy {
	if secs != nil {
		p.Seconds = secs
	}
	if enf != nil {
		p.Enforcement = enf
	}
	return p
}

func validatePolicyInputs(secs *int, secsField string, enf *string) []pgp.CheckFailure {
	var f []pgp.CheckFailure
	if secs != nil && *secs <= 0 {
		f = append(f, fail(secsField, "must be greater than 0"))
	}
	f = append(f, optIn("expirationEnforcement", enf, enforcements...)...)
	if secs == nil && enf == nil {
		f = append(f, fail(secsField, "set at least one of %s and expirationEnforcement", secsField))
	}
	return f
}

// ---- RegistryDefaultPolicy ----

// RegistryDefaultPolicy manages the registry-wide default image expiration.
type RegistryDefaultPolicy struct{}

type RegistryDefaultPolicyArgs struct {
	DefaultExpirationSeconds *int    `pulumi:"defaultExpirationSeconds,optional"`
	ExpirationEnforcement    *string `pulumi:"expirationEnforcement,optional"`
}

type RegistryDefaultPolicyState struct {
	RegistryDefaultPolicyArgs

	Revision                   int     `pulumi:"revision"`
	PriorExpirationSeconds     *int    `pulumi:"priorExpirationSeconds,optional"`
	PriorExpirationEnforcement *string `pulumi:"priorExpirationEnforcement,optional"`
	// PriorRecorded is true once the pre-existing values were captured at create
	// time, including when they were unset. Imported resources have no recorded
	// prior, so destroy leaves their policy alone.
	PriorRecorded bool `pulumi:"priorRecorded,optional"`
}

func (r *RegistryDefaultPolicy) Annotate(a infer.Annotator) {
	a.Describe(&r, "The default expiration policy of the Namespace container registry. Adopt-style singleton: the "+
		"policy always exists, so create records the current values ('prior*' outputs) and applies the declared ones; "+
		"destroy restores the recorded values exactly, clearing any field that was unset before. Unset inputs leave the server value unchanged. Import with the ID 'default'.")
}

func (a *RegistryDefaultPolicyArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.DefaultExpirationSeconds, "Default time in seconds after which pushed images expire.")
	ann.Describe(&a.ExpirationEnforcement, "EXPIRATION_ENFORCEMENT_ENFORCE or EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE.")
}

func (s *RegistryDefaultPolicyState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.Revision, "Server revision of the policy.")
	ann.Describe(&s.PriorExpirationSeconds, "Expiration found before this resource took over; restored on destroy.")
	ann.Describe(&s.PriorExpirationEnforcement, "Enforcement found before this resource took over; restored on destroy. "+
		"Unset means the registry had no enforcement setting, and destroy clears it again.")
	ann.Describe(&s.PriorRecorded, "True when the pre-existing policy was captured at create time. Destroy restores exactly "+
		"that, including clearing fields that were unset. Imported resources have no prior, so destroy leaves the policy as it is.")
}

func (r *RegistryDefaultPolicy) Check(
	ctx context.Context, req infer.CheckRequest,
) (infer.CheckResponse[RegistryDefaultPolicyArgs], error) {
	return checkWith(ctx, req, func(a RegistryDefaultPolicyArgs) []pgp.CheckFailure {
		return validatePolicyInputs(a.DefaultExpirationSeconds, "defaultExpirationSeconds", a.ExpirationEnforcement)
	})
}

// Diff deletes before replacing: RegistryDefaultPolicy adopts and later restores server-side
// state, so with the default create-before-delete order a forced replace
// (--replace, --target-replace, taint) would run the old resource's delete
// after the new create and undo what the new one adopted.
func (r *RegistryDefaultPolicy) Diff(
	_ context.Context, req infer.DiffRequest[RegistryDefaultPolicyArgs, RegistryDefaultPolicyState],
) (infer.DiffResponse, error) {
	return diffDeleteFirst(req.State.RegistryDefaultPolicyArgs, req.Inputs), nil
}

func (r *RegistryDefaultPolicy) Create(
	ctx context.Context, req infer.CreateRequest[RegistryDefaultPolicyArgs],
) (infer.CreateResponse[RegistryDefaultPolicyState], error) {
	state := RegistryDefaultPolicyState{RegistryDefaultPolicyArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[RegistryDefaultPolicyState]{ID: "default", Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[RegistryDefaultPolicyState]{}, err
	}
	prior, err := getDefaultPolicy(ctx, c)
	if err != nil {
		return infer.CreateResponse[RegistryDefaultPolicyState]{}, fmt.Errorf("read registry default policy: %w", err)
	}
	if err := putDefaultPolicy(ctx, c, prior.merge(req.Inputs.DefaultExpirationSeconds, req.Inputs.ExpirationEnforcement)); err != nil {
		return infer.CreateResponse[RegistryDefaultPolicyState]{}, fmt.Errorf("update registry default policy: %w", err)
	}
	// The policy has been overwritten: record the prior values before anything
	// else can fail, or destroy could never restore them.
	state.PriorExpirationSeconds = prior.Seconds
	state.PriorExpirationEnforcement = prior.Enforcement
	state.PriorRecorded = true
	cur, err := getDefaultPolicy(ctx, c)
	if err != nil {
		return infer.CreateResponse[RegistryDefaultPolicyState]{ID: "default", Output: state},
			infer.ResourceInitFailedError{Reasons: []string{fmt.Sprintf("read back registry default policy: %v", err)}}
	}
	state.Revision = int(cur.Revision)
	return infer.CreateResponse[RegistryDefaultPolicyState]{ID: "default", Output: state}, nil
}

func (r *RegistryDefaultPolicy) Read(
	ctx context.Context, req infer.ReadRequest[RegistryDefaultPolicyArgs, RegistryDefaultPolicyState],
) (infer.ReadResponse[RegistryDefaultPolicyArgs, RegistryDefaultPolicyState], error) {
	type resp = infer.ReadResponse[RegistryDefaultPolicyArgs, RegistryDefaultPolicyState]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	cur, err := getDefaultPolicy(ctx, c)
	if isNotFound(err) {
		return resp{}, nil
	}
	if err != nil {
		return resp{}, fmt.Errorf("read registry default policy: %w", err)
	}
	state := req.State
	args := RegistryDefaultPolicyArgs{DefaultExpirationSeconds: cur.Seconds, ExpirationEnforcement: cur.Enforcement}
	// On import there is neither a declared input nor a recorded prior: report
	// the server values. On refresh keep only what the program declared, so a
	// server value for an undeclared field never becomes an input.
	imported := !req.State.PriorRecorded && req.Inputs.DefaultExpirationSeconds == nil && req.Inputs.ExpirationEnforcement == nil
	if req.ID == "default" && !imported {
		keepDeclared(req.Inputs, &args)
	}
	state.RegistryDefaultPolicyArgs = args
	state.Revision = int(cur.Revision)
	return resp{ID: "default", Inputs: args, State: state}, nil
}

func (r *RegistryDefaultPolicy) Update(
	ctx context.Context, req infer.UpdateRequest[RegistryDefaultPolicyArgs, RegistryDefaultPolicyState],
) (infer.UpdateResponse[RegistryDefaultPolicyState], error) {
	state := req.State
	state.RegistryDefaultPolicyArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[RegistryDefaultPolicyState]{Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[RegistryDefaultPolicyState]{}, err
	}
	cur, err := getDefaultPolicy(ctx, c)
	if err != nil {
		return infer.UpdateResponse[RegistryDefaultPolicyState]{}, fmt.Errorf("read registry default policy: %w", err)
	}
	next := cur.merge(req.Inputs.DefaultExpirationSeconds, req.Inputs.ExpirationEnforcement)
	next.Revision = int64(req.State.Revision) // optimistic concurrency against what Pulumi last saw
	if err := putDefaultPolicy(ctx, c, next); err != nil {
		return infer.UpdateResponse[RegistryDefaultPolicyState]{}, fmt.Errorf("update registry default policy: %w", err)
	}
	if after, err := getDefaultPolicy(ctx, c); err == nil {
		state.Revision = int(after.Revision)
	}
	return infer.UpdateResponse[RegistryDefaultPolicyState]{Output: state}, nil
}

func (r *RegistryDefaultPolicy) Delete(
	ctx context.Context, req infer.DeleteRequest[RegistryDefaultPolicyState],
) (infer.DeleteResponse, error) {
	if !req.State.PriorRecorded {
		return infer.DeleteResponse{}, nil // imported: nothing recorded to restore
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	cur, err := getDefaultPolicy(ctx, c)
	if err != nil {
		return infer.DeleteResponse{}, fmt.Errorf("read registry default policy: %w", err)
	}
	restore := restoreTo(cur, req.State.PriorExpirationSeconds, req.State.PriorExpirationEnforcement)
	if err := putDefaultPolicy(ctx, c, restore); err != nil {
		return infer.DeleteResponse{}, fmt.Errorf("restore registry default policy: %w", err)
	}
	return infer.DeleteResponse{}, nil
}

// ---- RegistryRepositoryPolicy ----

// RegistryRepositoryPolicy manages the expiration policy of one repository.
type RegistryRepositoryPolicy struct{}

type RegistryRepositoryPolicyArgs struct {
	Repository            string  `pulumi:"repository" provider:"replaceOnChanges"`
	ExpirationSeconds     *int    `pulumi:"expirationSeconds,optional"`
	ExpirationEnforcement *string `pulumi:"expirationEnforcement,optional"`
}

type RegistryRepositoryPolicyState struct {
	RegistryRepositoryPolicyArgs

	Revision                   int     `pulumi:"revision"`
	PriorExpirationSeconds     *int    `pulumi:"priorExpirationSeconds,optional"`
	PriorExpirationEnforcement *string `pulumi:"priorExpirationEnforcement,optional"`
	// PriorRecorded is true once the pre-existing values were captured at create
	// time, including when they were unset. Imported resources have no recorded
	// prior, so destroy leaves their policy alone.
	PriorRecorded bool `pulumi:"priorRecorded,optional"`
}

func (r *RegistryRepositoryPolicy) Annotate(a infer.Annotator) {
	a.Describe(&r, "The expiration policy of one container registry repository. Adopt-style: create records the "+
		"current policy ('prior*' outputs) and applies the declared one; destroy restores the recorded values exactly, clearing any field that was unset before. "+
		"Changing 'repository' replaces the resource. Import with the repository name.")
}

func (a *RegistryRepositoryPolicyArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Repository, "The repository name. Changing it replaces the resource.")
	ann.Describe(&a.ExpirationSeconds, "Time in seconds after which images in the repository expire.")
	ann.Describe(&a.ExpirationEnforcement, "EXPIRATION_ENFORCEMENT_ENFORCE or EXPIRATION_ENFORCEMENT_DO_NOT_ENFORCE.")
}

func (s *RegistryRepositoryPolicyState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.Revision, "Server revision of the policy.")
	ann.Describe(&s.PriorExpirationSeconds, "Expiration found before this resource took over; restored on destroy.")
	ann.Describe(&s.PriorExpirationEnforcement, "Enforcement found before this resource took over; restored on destroy. "+
		"Unset means the registry had no enforcement setting, and destroy clears it again.")
	ann.Describe(&s.PriorRecorded, "True when the pre-existing policy was captured at create time. Destroy restores exactly "+
		"that, including clearing fields that were unset. Imported resources have no prior, so destroy leaves the policy as it is.")
}

func (r *RegistryRepositoryPolicy) Check(
	ctx context.Context, req infer.CheckRequest,
) (infer.CheckResponse[RegistryRepositoryPolicyArgs], error) {
	return checkWith(ctx, req, func(a RegistryRepositoryPolicyArgs) []pgp.CheckFailure {
		return append(requireNonEmpty("repository", a.Repository),
			validatePolicyInputs(a.ExpirationSeconds, "expirationSeconds", a.ExpirationEnforcement)...)
	})
}

// Diff deletes before replacing: RegistryRepositoryPolicy adopts and later restores server-side
// state, so with the default create-before-delete order a forced replace
// (--replace, --target-replace, taint) would run the old resource's delete
// after the new create and undo what the new one adopted.
func (r *RegistryRepositoryPolicy) Diff(
	_ context.Context, req infer.DiffRequest[RegistryRepositoryPolicyArgs, RegistryRepositoryPolicyState],
) (infer.DiffResponse, error) {
	return diffDeleteFirst(req.State.RegistryRepositoryPolicyArgs, req.Inputs), nil
}

func (r *RegistryRepositoryPolicy) Create(
	ctx context.Context, req infer.CreateRequest[RegistryRepositoryPolicyArgs],
) (infer.CreateResponse[RegistryRepositoryPolicyState], error) {
	state := RegistryRepositoryPolicyState{RegistryRepositoryPolicyArgs: req.Inputs}
	repo := req.Inputs.Repository
	if req.DryRun {
		return infer.CreateResponse[RegistryRepositoryPolicyState]{ID: repo, Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[RegistryRepositoryPolicyState]{}, err
	}
	prior, err := getRepoPolicy(ctx, c, repo)
	if err != nil {
		return infer.CreateResponse[RegistryRepositoryPolicyState]{}, fmt.Errorf("read policy of repository %q: %w", repo, err)
	}
	if err := putRepoPolicy(ctx, c, repo, prior.merge(req.Inputs.ExpirationSeconds, req.Inputs.ExpirationEnforcement)); err != nil {
		return infer.CreateResponse[RegistryRepositoryPolicyState]{}, fmt.Errorf("update policy of repository %q: %w", repo, err)
	}
	state.PriorExpirationSeconds = prior.Seconds
	state.PriorExpirationEnforcement = prior.Enforcement
	state.PriorRecorded = true
	cur, err := getRepoPolicy(ctx, c, repo)
	if err != nil {
		return infer.CreateResponse[RegistryRepositoryPolicyState]{ID: repo, Output: state},
			infer.ResourceInitFailedError{Reasons: []string{fmt.Sprintf("read back policy of repository %q: %v", repo, err)}}
	}
	state.Revision = int(cur.Revision)
	return infer.CreateResponse[RegistryRepositoryPolicyState]{ID: repo, Output: state}, nil
}

func (r *RegistryRepositoryPolicy) Read(
	ctx context.Context, req infer.ReadRequest[RegistryRepositoryPolicyArgs, RegistryRepositoryPolicyState],
) (infer.ReadResponse[RegistryRepositoryPolicyArgs, RegistryRepositoryPolicyState], error) {
	type resp = infer.ReadResponse[RegistryRepositoryPolicyArgs, RegistryRepositoryPolicyState]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	cur, err := getRepoPolicy(ctx, c, req.ID)
	if isNotFound(err) {
		return resp{}, nil
	}
	if err != nil {
		return resp{}, fmt.Errorf("read policy of repository %q: %w", req.ID, err)
	}
	state := req.State
	args := RegistryRepositoryPolicyArgs{Repository: req.ID, ExpirationSeconds: cur.Seconds, ExpirationEnforcement: cur.Enforcement}
	if req.Inputs.Repository != "" {
		keepDeclared(req.Inputs, &args)
	}
	state.RegistryRepositoryPolicyArgs = args
	state.Revision = int(cur.Revision)
	return resp{ID: req.ID, Inputs: args, State: state}, nil
}

func (r *RegistryRepositoryPolicy) Update(
	ctx context.Context, req infer.UpdateRequest[RegistryRepositoryPolicyArgs, RegistryRepositoryPolicyState],
) (infer.UpdateResponse[RegistryRepositoryPolicyState], error) {
	state := req.State
	state.RegistryRepositoryPolicyArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[RegistryRepositoryPolicyState]{Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[RegistryRepositoryPolicyState]{}, err
	}
	cur, err := getRepoPolicy(ctx, c, req.ID)
	if err != nil {
		return infer.UpdateResponse[RegistryRepositoryPolicyState]{}, fmt.Errorf("read policy of repository %q: %w", req.ID, err)
	}
	next := cur.merge(req.Inputs.ExpirationSeconds, req.Inputs.ExpirationEnforcement)
	next.Revision = int64(req.State.Revision)
	if err := putRepoPolicy(ctx, c, req.ID, next); err != nil {
		return infer.UpdateResponse[RegistryRepositoryPolicyState]{}, fmt.Errorf("update policy of repository %q: %w", req.ID, err)
	}
	if after, err := getRepoPolicy(ctx, c, req.ID); err == nil {
		state.Revision = int(after.Revision)
	}
	return infer.UpdateResponse[RegistryRepositoryPolicyState]{Output: state}, nil
}

func (r *RegistryRepositoryPolicy) Delete(
	ctx context.Context, req infer.DeleteRequest[RegistryRepositoryPolicyState],
) (infer.DeleteResponse, error) {
	if !req.State.PriorRecorded {
		return infer.DeleteResponse{}, nil // imported: nothing recorded to restore
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	cur, err := getRepoPolicy(ctx, c, req.ID)
	if isNotFound(err) {
		return infer.DeleteResponse{}, nil
	}
	if err != nil {
		return infer.DeleteResponse{}, fmt.Errorf("read policy of repository %q: %w", req.ID, err)
	}
	if err := putRepoPolicy(ctx, c, req.ID, restoreTo(cur, req.State.PriorExpirationSeconds, req.State.PriorExpirationEnforcement)); err != nil {
		return infer.DeleteResponse{}, fmt.Errorf("restore policy of repository %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}
