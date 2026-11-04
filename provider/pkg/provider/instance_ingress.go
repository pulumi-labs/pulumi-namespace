package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	computev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/compute/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// InstanceIngress resource
// ---------------------------------------------------------------------------

// InstanceIngress exposes a port of a running instance on an Internet-facing name.
type InstanceIngress struct{}

// IngressMatchRule is one HTTP routing rule of an ingress.
type IngressMatchRule struct {
	// Methods the rule matches; empty matches any method.
	Methods []string `pulumi:"methods,optional"`
	// Path pattern the rule matches; empty matches any path.
	Path *string `pulumi:"path,optional"`
	// DoesNotRequireAuth lets matching requests through without a bearer token.
	DoesNotRequireAuth *bool `pulumi:"doesNotRequireAuth,optional"`
}

func (m *IngressMatchRule) Annotate(a infer.Annotator) {
	a.Describe(&m.Methods, "HTTP methods the rule matches. Empty matches any method.")
	a.Describe(&m.Path, "Path pattern the rule matches. Empty matches any path.")
	a.Describe(&m.DoesNotRequireAuth, "When true, matching requests do not require authentication. By default every request needs a valid bearer token.")
}

type InstanceIngressArgs struct {
	InstanceID       string             `pulumi:"instanceId" provider:"replaceOnChanges"`
	Region           *string            `pulumi:"region,optional" provider:"replaceOnChanges"`
	Name             *string            `pulumi:"name,optional" provider:"replaceOnChanges"`
	Port             int                `pulumi:"port" provider:"replaceOnChanges"`
	BackendIPAddress *string            `pulumi:"backendIpAddress,optional" provider:"replaceOnChanges"`
	HTTPMatchRules   []IngressMatchRule `pulumi:"httpMatchRules,optional" provider:"replaceOnChanges"`
	Wildcard         *bool              `pulumi:"wildcard,optional" provider:"replaceOnChanges"`
}

type InstanceIngressState struct {
	InstanceIngressArgs

	// IngressID is the server-assigned ingress identifier.
	IngressID string `pulumi:"ingressId"`
	// Fqdn is the Internet-facing server name.
	Fqdn string `pulumi:"fqdn"`
	// Description is the server-generated description of the ingress.
	Description string `pulumi:"description"`
	// EffectiveRegion is the region the instance runs in; later calls use it.
	EffectiveRegion *string `pulumi:"effectiveRegion,optional"`
}

func (r *InstanceIngress) Annotate(a infer.Annotator) {
	a.Describe(&r, "An ingress that exposes one port of a Namespace compute instance on an Internet-facing "+
		"server name (CreateIngress / DeleteIngress). The API has no update call, so every input replaces the ingress. "+
		"The API reports only an ingress's name, fqdn and description back, so port, backendIpAddress, httpMatchRules "+
		"and wildcard cannot be refreshed: an imported ingress carries only instanceId and name, and the port and rules "+
		"you declare are accepted without a replacement. Import with 'instanceId/ingressId', or "+
		"'region/instanceId/ingressId' for an instance outside the provider region.")
}

func (a *InstanceIngressArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.InstanceID, "The instance to expose a port of. Changing it replaces the ingress.")
	ann.Describe(&a.Region, "Region the instance runs in, such as 'us' or 'eu'. Defaults to the provider region. Changing it replaces the ingress.")
	ann.Describe(&a.Name, "Name used to generate the Internet-facing server name; lowercase letters, digits, '-' and '_'. "+
		"Required unless 'wildcard' is true. Changing it replaces the ingress.")
	ann.Describe(&a.Port, "The port the instance listens on. Changing it replaces the ingress.")
	ann.Describe(&a.BackendIPAddress, "Optional IP address of the backend, for routing to a non-loopback endpoint reachable from the "+
		"instance such as a Kubernetes Service load balancer IP. Defaults to the instance itself. Changing it replaces the ingress.")
	ann.Describe(&a.HTTPMatchRules, "HTTP routing and authentication rules. Changing them replaces the ingress.")
	ann.Describe(&a.Wildcard, "Routes all first-level subdomains of the instance wildcard domain to this ingress. Requires the instance "+
		"to be created with the wildcard domain enabled. Mutually exclusive with 'name'. Changing it replaces the ingress.")
}

func (s *InstanceIngressState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.IngressID, "The server-assigned ingress identifier.")
	ann.Describe(&s.Fqdn, "The Internet-facing server name of the ingress.")
	ann.Describe(&s.Description, "Server-generated description of the ingress.")
	ann.Describe(&s.EffectiveRegion, "The region the instance runs in. Read and Delete always use this region.")
}

var ingressNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-_]*[a-z0-9])?$`)

func (a InstanceIngressArgs) validate() []pgp.CheckFailure {
	f := requireNonEmpty("instanceId", a.InstanceID)
	if a.Port < 1 || a.Port > 65535 {
		f = append(f, fail("port", "must be between 1 and 65535, got %d", a.Port))
	}
	wild := a.Wildcard != nil && *a.Wildcard
	name := derefStr(a.Name)
	switch {
	case wild && name != "":
		f = append(f, fail("name", "must not be set together with wildcard"))
	case !wild && name == "":
		f = append(f, fail("name", "is required unless wildcard is true"))
	case name != "" && !ingressNameRE.MatchString(name):
		f = append(f, fail("name", "must match %s, got %q", ingressNameRE.String(), name))
	}
	return f
}

func (r *InstanceIngress) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[InstanceIngressArgs], error) {
	return checkWith(ctx, req, InstanceIngressArgs.validate)
}

func ingressID(instanceID, ingressID string) string { return instanceID + "/" + ingressID }

// parseIngressID splits 'instanceId/ingressId' or 'region/instanceId/ingressId'.
func parseIngressID(id string) (region, instanceID, ingress string, err error) {
	parts := strings.Split(id, "/")
	switch len(parts) {
	case 2:
		return "", parts[0], parts[1], nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	}
	return "", "", "", fmt.Errorf("namespace: instance ingress ID %q must be 'instanceId/ingressId' or 'region/instanceId/ingressId'", id)
}

func ingressRegion(s InstanceIngressState) *string {
	if s.EffectiveRegion != nil && *s.EffectiveRegion != "" {
		return s.EffectiveRegion
	}
	return s.Region
}

func ingressRequest(in InstanceIngressArgs) *computev1.IngressRequest {
	req := &computev1.IngressRequest{
		Name: derefStr(in.Name), Wildcard: derefBool(in.Wildcard),
		ExportedPortBackend: &computev1.ExportedPortBackend{Port: int32(in.Port), IpAddress: derefStr(in.BackendIPAddress)}, //nolint:gosec // validated 1..65535
	}
	for _, m := range in.HTTPMatchRules {
		req.HttpMatchRule = append(req.HttpMatchRule, &computev1.HttpMatchRule{
			Match:              &computev1.HttpMatchRule_HttpMatch{Method: m.Methods, Path: derefStr(m.Path)},
			DoesNotRequireAuth: derefBool(m.DoesNotRequireAuth),
		})
	}
	return req
}

func listIngresses(ctx context.Context, c *nsClient, instanceID string) ([]*computev1.AllocatedIngress, error) {
	var res computev1.ListIngressesResponse
	if err := c.call(ctx, c.endpoints.computeURL(c.region), computeService, "ListIngresses",
		&computev1.ListIngressesRequest{InstanceId: instanceID}, &res); err != nil {
		return nil, err
	}
	return res.GetAllocatedIngresses(), nil
}

// identifyCreatedIngress finds the ingress a CreateIngress call allocated. Exactly
// one request is sent, so a response with exactly one ingress is that ingress,
// even when the server normalised or suffixed the requested name. Any other
// count is resolved against the instance's current ingresses by name; when that
// cannot single the ingress out, the error names the instance so the ingress
// that may exist remotely can be found and imported or removed.
func identifyCreatedIngress(ctx context.Context, c *nsClient, in InstanceIngressArgs, returned []*computev1.AllocatedIngress) (*computev1.AllocatedIngress, error) {
	if len(returned) == 1 && returned[0].GetIngressId() != "" {
		return returned[0], nil
	}
	name := derefStr(in.Name)
	pick := func(list []*computev1.AllocatedIngress) *computev1.AllocatedIngress {
		var found *computev1.AllocatedIngress
		for _, a := range list {
			if name == "" || a.GetName() != name || a.GetIngressId() == "" {
				continue
			}
			if found != nil {
				return nil // ambiguous
			}
			found = a
		}
		return found
	}
	if got := pick(returned); got != nil {
		return got, nil
	}
	all, err := listIngresses(ctx, c, in.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("create ingress on instance %q: the API returned %d ingresses and listing the instance's ingresses to identify the new one failed: %w",
			in.InstanceID, len(returned), err)
	}
	if got := pick(all); got != nil {
		return got, nil
	}
	return nil, fmt.Errorf("create ingress on instance %q: the ingress was requested but could not be identified (the API returned %d ingresses and none of the instance's %d ingresses matches name %q uniquely); it may exist on the instance, so check its ingresses and import or delete it before running again",
		in.InstanceID, len(returned), len(all), name)
}

func (r *InstanceIngress) Create(
	ctx context.Context, req infer.CreateRequest[InstanceIngressArgs],
) (infer.CreateResponse[InstanceIngressState], error) {
	in := req.Inputs
	state := InstanceIngressState{InstanceIngressArgs: in}
	if req.DryRun {
		return infer.CreateResponse[InstanceIngressState]{ID: "preview", Output: state}, nil
	}
	c, err := clientFor(ctx, in.Region)
	if err != nil {
		return infer.CreateResponse[InstanceIngressState]{}, err
	}
	var res computev1.CreateIngressResponse
	err = c.call(ctx, c.endpoints.computeURL(c.region), computeService, "CreateIngress",
		&computev1.CreateIngressRequest{InstanceId: in.InstanceID, Ingresses: []*computev1.IngressRequest{ingressRequest(in)}}, &res)
	if err != nil {
		return infer.CreateResponse[InstanceIngressState]{}, fmt.Errorf("create ingress on instance %q: %w", in.InstanceID, err)
	}
	got, err := identifyCreatedIngress(ctx, c, in, res.GetAllocatedIngresses())
	if err != nil {
		return infer.CreateResponse[InstanceIngressState]{}, err
	}
	state.IngressID = got.GetIngressId()
	state.Fqdn = got.GetFqdn()
	state.Description = got.GetDescription()
	state.EffectiveRegion = ptr(c.region)
	return infer.CreateResponse[InstanceIngressState]{ID: ingressID(in.InstanceID, state.IngressID), Output: state}, nil
}

func (r *InstanceIngress) Read(
	ctx context.Context, req infer.ReadRequest[InstanceIngressArgs, InstanceIngressState],
) (infer.ReadResponse[InstanceIngressArgs, InstanceIngressState], error) {
	type resp = infer.ReadResponse[InstanceIngressArgs, InstanceIngressState]
	idRegion, instanceID, ing, err := parseIngressID(req.ID)
	if err != nil {
		return resp{}, err
	}
	imported := req.State.IngressID == ""
	region := ingressRegion(req.State)
	if region == nil && idRegion != "" {
		region = &idRegion
	}
	c, err := clientFor(ctx, region)
	if err != nil {
		return resp{}, err
	}
	all, err := listIngresses(ctx, c, instanceID)
	if isNotFound(err) {
		return resp{}, nil // the instance is gone, and its ingresses with it
	}
	if err != nil {
		return resp{}, fmt.Errorf("read ingress %q: %w", req.ID, err)
	}
	var got *computev1.AllocatedIngress
	for _, a := range all {
		if a.GetIngressId() == ing {
			got = a
			break
		}
	}
	if got == nil {
		return resp{}, nil
	}
	state := req.State
	state.InstanceID = instanceID
	// The ingress id never leaves the ID: rebuild the ID the same way Create does.
	state.IngressID = ing
	state.Fqdn = got.GetFqdn()
	state.Description = got.GetDescription()
	if state.EffectiveRegion == nil || *state.EffectiveRegion == "" {
		state.EffectiveRegion = ptr(c.region)
	}
	// A name is reported back for drift only when one was declared (a wildcard
	// ingress carries a server-chosen name the program never set).
	if imported || state.Name != nil {
		state.Name = emptyToNil(got.GetName())
	}
	return resp{ID: ingressID(instanceID, ing), Inputs: state.InstanceIngressArgs, State: state}, nil
}

// Diff compares inputs with the prior inputs. The API cannot report port,
// backendIpAddress, httpMatchRules or wildcard, so an imported ingress (no
// recorded port) adopts the declared values instead of replacing.
func (r *InstanceIngress) Diff(
	_ context.Context, req infer.DiffRequest[InstanceIngressArgs, InstanceIngressState],
) (infer.DiffResponse, error) {
	old := req.State.InstanceIngressArgs
	if old.Port == 0 {
		old.Port, old.BackendIPAddress, old.HTTPMatchRules, old.Wildcard =
			req.Inputs.Port, req.Inputs.BackendIPAddress, req.Inputs.HTTPMatchRules, req.Inputs.Wildcard
		old.Region = req.Inputs.Region // imported: the region input stays unset until declared
	}
	return diffArgs(old, req.Inputs), nil
}

func (r *InstanceIngress) Delete(
	ctx context.Context, req infer.DeleteRequest[InstanceIngressState],
) (infer.DeleteResponse, error) {
	_, instanceID, ing, err := parseIngressID(req.ID)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	c, err := clientFor(ctx, ingressRegion(req.State))
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.computeURL(c.region), computeService, "DeleteIngress",
		&computev1.DeleteIngressRequest{InstanceId: instanceID, IngressId: ing}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("delete ingress %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// ---------------------------------------------------------------------------
// getInstanceIngress
// ---------------------------------------------------------------------------

// GetInstanceIngress reads the ingresses of an instance.
type GetInstanceIngress struct{}

type GetInstanceIngressArgs struct {
	InstanceID string  `pulumi:"instanceId"`
	IngressID  *string `pulumi:"ingressId,optional"`
	Name       *string `pulumi:"name,optional"`
	Region     *string `pulumi:"region,optional"`
}

type GetInstanceIngressResult struct {
	InstanceID  string `pulumi:"instanceId"`
	IngressID   string `pulumi:"ingressId"`
	Name        string `pulumi:"name"`
	Fqdn        string `pulumi:"fqdn"`
	Description string `pulumi:"description"`
	Region      string `pulumi:"region"`
}

func (f *GetInstanceIngress) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one ingress of an instance, by ingress ID or by name.")
}

func (a *GetInstanceIngressArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.InstanceID, "The instance ID.")
	ann.Describe(&a.IngressID, "The ingress ID. Set exactly one of ingressId and name.")
	ann.Describe(&a.Name, "The ingress name. Set exactly one of ingressId and name.")
	ann.Describe(&a.Region, "Region the instance runs in. Defaults to the provider region.")
}

func (r *GetInstanceIngressResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.InstanceID, "The instance ID.")
	ann.Describe(&r.IngressID, "The ingress ID.")
	ann.Describe(&r.Name, "The ingress name.")
	ann.Describe(&r.Fqdn, "The Internet-facing server name.")
	ann.Describe(&r.Description, "Server-generated description.")
	ann.Describe(&r.Region, "The region the instance was read from.")
}

func (f *GetInstanceIngress) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetInstanceIngressArgs],
) (infer.FunctionResponse[GetInstanceIngressResult], error) {
	type resp = infer.FunctionResponse[GetInstanceIngressResult]
	id, name := derefStr(req.Input.IngressID), derefStr(req.Input.Name)
	if (id == "") == (name == "") {
		return resp{}, errExactlyOne("getInstanceIngress", "ingressId", "name")
	}
	c, err := clientFor(ctx, req.Input.Region)
	if err != nil {
		return resp{}, err
	}
	all, err := listIngresses(ctx, c, req.Input.InstanceID)
	if isNotFound(err) {
		return resp{}, errLookupNotFound("instance", req.Input.InstanceID)
	}
	if err != nil {
		return resp{}, fmt.Errorf("list ingresses of instance %q: %w", req.Input.InstanceID, err)
	}
	for _, a := range all {
		if (id != "" && a.GetIngressId() == id) || (name != "" && a.GetName() == name) {
			return resp{Output: GetInstanceIngressResult{
				InstanceID: req.Input.InstanceID, IngressID: a.GetIngressId(), Name: a.GetName(),
				Fqdn: a.GetFqdn(), Description: a.GetDescription(), Region: c.region,
			}}, nil
		}
	}
	return resp{}, errLookupNotFound("ingress of instance "+req.Input.InstanceID, id+name)
}
