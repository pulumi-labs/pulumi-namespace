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

	buildkitev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/buildkite"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ---------------------------------------------------------------------------
// BuildkiteQueue resource (adopt-style)
// ---------------------------------------------------------------------------

// BuildkiteQueue configures the Namespace settings of an existing Buildkite
// queue. Queues are created and deleted through the Buildkite integration, not
// through the Namespace API, so this resource adopts one.
type BuildkiteQueue struct{}

// QueuePermission grants a workload permission on a resource.
type QueuePermission struct {
	ResourceType string   `pulumi:"resourceType" json:"resourceType"`
	ResourceID   string   `pulumi:"resourceId" json:"resourceId"`
	Actions      []string `pulumi:"actions" json:"actions"`
}

func (p *QueuePermission) Annotate(a infer.Annotator) {
	a.Describe(&p.ResourceType, "Resource type the permission applies to.")
	a.Describe(&p.ResourceID, "Resource ID the permission applies to.")
	a.Describe(&p.Actions, "Allowed actions.")
}

// QueuePermissions is the permission set of the queue's workloads.
type QueuePermissions struct {
	PermissionsType     *string           `pulumi:"permissionsType,optional" json:"permissionsType,omitempty"`
	WorkloadPermissions []QueuePermission `pulumi:"workloadPermissions,optional" json:"workloadPermissions,omitempty"`
}

func (p *QueuePermissions) Annotate(a infer.Annotator) {
	a.Describe(&p.PermissionsType, "DEFAULT or CUSTOM. CUSTOM uses workloadPermissions.")
	a.Describe(&p.WorkloadPermissions, "Permissions granted to workloads when permissionsType is CUSTOM.")
}

// QueueValueFrom resolves a header value.
type QueueValueFrom struct {
	Static       *string `pulumi:"static,optional" json:"static,omitempty" provider:"secret"`
	FromSecretID *string `pulumi:"fromSecretId,optional" json:"fromSecretId,omitempty"`
}

func (v *QueueValueFrom) Annotate(a infer.Annotator) {
	a.Describe(&v.Static, "A literal value. Stored as a Pulumi secret.")
	a.Describe(&v.FromSecretID, "Vault secret ID holding the value.")
}

// QueueHeader is an OTLP export header.
type QueueHeader struct {
	Name      string          `pulumi:"name" json:"name"`
	Value     *string         `pulumi:"value,optional" json:"value,omitempty" provider:"secret"`
	ValueFrom *QueueValueFrom `pulumi:"valueFrom,optional" json:"valueFrom,omitempty"`
}

func (h *QueueHeader) Annotate(a infer.Annotator) {
	a.Describe(&h.Name, "Header name.")
	a.Describe(&h.Value, "Header value. Stored as a Pulumi secret. Prefer valueFrom.fromSecretId.")
	a.Describe(&h.ValueFrom, "Resolve the value from a vault secret or a static string.")
}

// QueueOtel configures OpenTelemetry export for jobs of the queue.
type QueueOtel struct {
	OtlpEndpoint       string            `pulumi:"otlpEndpoint" json:"otlpEndpoint"`
	Headers            []QueueHeader     `pulumi:"headers,optional" json:"headers,omitempty"`
	OtlpProtocol       *string           `pulumi:"otlpProtocol,optional" json:"otlpProtocol,omitempty"`
	ResourceAttributes map[string]string `pulumi:"resourceAttributes,optional" json:"resourceAttributes,omitempty"`
}

func (o *QueueOtel) Annotate(a infer.Annotator) {
	a.Describe(&o.OtlpEndpoint, "OTLP collector endpoint.")
	a.Describe(&o.Headers, "Headers sent with every export request.")
	a.Describe(&o.OtlpProtocol, "OTLP_PROTOCOL_GRPC or OTLP_PROTOCOL_HTTP_PROTOBUF.")
	a.Describe(&o.ResourceAttributes, "OpenTelemetry resource attributes added to exports.")
}

// BuildkiteQueueArgs are the user-supplied inputs.
type BuildkiteQueueArgs struct {
	QueueName             string            `pulumi:"queueName" json:"-" provider:"replaceOnChanges"`
	EgressPolicyTag       *string           `pulumi:"egressPolicyTag,optional" json:"egressPolicyTag,omitempty"`
	Permissions           *QueuePermissions `pulumi:"permissions,optional" json:"permissions,omitempty"`
	OpenTelemetrySettings *QueueOtel        `pulumi:"openTelemetrySettings,optional" json:"openTelemetrySettings,omitempty"`
}

// BuildkiteQueueState is the persisted state.
type BuildkiteQueueState struct {
	BuildkiteQueueArgs

	QueueID          string `pulumi:"queueId"`
	Cluster          string `pulumi:"cluster"`
	OrganizationSlug string `pulumi:"organizationSlug"`
	// PriorRecorded is true once PriorSettings holds the settings found when the
	// queue was adopted (an empty prior is a valid recording).
	PriorRecorded bool `pulumi:"priorRecorded,optional"`
	// PriorSettings are the settings the queue had before this resource adopted it.
	PriorSettings *QueuePriorSettings `pulumi:"priorSettings,optional"`
}

// QueuePriorSettings is the settings snapshot restored on destroy.
type QueuePriorSettings struct {
	EgressPolicyTag       *string           `pulumi:"egressPolicyTag,optional" json:"egressPolicyTag,omitempty"`
	Permissions           *QueuePermissions `pulumi:"permissions,optional" json:"permissions,omitempty"`
	OpenTelemetrySettings *QueueOtel        `pulumi:"openTelemetrySettings,optional" json:"openTelemetrySettings,omitempty"`
}

func (p *QueuePriorSettings) Annotate(a infer.Annotator) {
	a.Describe(&p.EgressPolicyTag, "Egress policy tag the queue had before adoption.")
	a.Describe(&p.Permissions, "Workload permissions the queue had before adoption.")
	a.Describe(&p.OpenTelemetrySettings, "OpenTelemetry settings the queue had before adoption.")
}

func (r *BuildkiteQueue) Annotate(a infer.Annotator) {
	a.Describe(&r, "Namespace settings (egress policy, workload permissions, OpenTelemetry export) of an existing "+
		"Buildkite queue. Adopt-style: the Namespace API can neither create nor delete queues, so create looks the "+
		"queue up by name and applies ONLY the declared settings on top of what the queue already has (the API "+
		"replaces the whole settings object, so undeclared settings are read first and sent back unchanged), "+
		"failing if no such queue exists. The settings found at adoption are recorded in priorSettings and "+
		"destroy restores them. Settings removed from the program later are left as the server has them. "+
		"Header values the server redacts cannot be restored. Import with the queue ID.")
}

func (a *BuildkiteQueueArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.QueueName, "Name of the existing Buildkite queue. Changing it replaces the resource (adopts a different queue).")
	ann.Describe(&a.EgressPolicyTag, "Tag of the EgressPolicy applied to jobs of the queue.")
	ann.Describe(&a.Permissions, "Workload permissions of the queue.")
	ann.Describe(&a.OpenTelemetrySettings, "OpenTelemetry export settings.")
}

func (s *BuildkiteQueueState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.QueueID, "The Namespace queue identifier.")
	ann.Describe(&s.Cluster, "The Buildkite cluster the queue belongs to.")
	ann.Describe(&s.OrganizationSlug, "The Buildkite organization slug.")
	ann.Describe(&s.PriorRecorded, "True once priorSettings holds the settings found at adoption.")
	ann.Describe(&s.PriorSettings, "Settings the queue had before it was adopted; restored on destroy.")
}

func (a BuildkiteQueueArgs) validate() []pgp.CheckFailure {
	var f []pgp.CheckFailure
	if a.QueueName == "" {
		f = append(f, fail("queueName", "must not be empty"))
	}
	if p := a.Permissions; p != nil {
		f = append(f, optIn("permissions.permissionsType", p.PermissionsType, "DEFAULT", "CUSTOM")...)
		if p.PermissionsType != nil && *p.PermissionsType == "DEFAULT" && len(p.WorkloadPermissions) > 0 {
			f = append(f, fail("permissions.workloadPermissions", "only valid with permissionsType CUSTOM"))
		}
	}
	if o := a.OpenTelemetrySettings; o != nil {
		f = append(f, requireNonEmpty("openTelemetrySettings.otlpEndpoint", o.OtlpEndpoint)...)
		f = append(f, optIn("openTelemetrySettings.otlpProtocol", o.OtlpProtocol,
			"OTLP_PROTOCOL_GRPC", "OTLP_PROTOCOL_HTTP_PROTOBUF")...)
	}
	return f
}

func (r *BuildkiteQueue) Check(
	ctx context.Context, req infer.CheckRequest,
) (infer.CheckResponse[BuildkiteQueueArgs], error) {
	return checkWith(ctx, req, BuildkiteQueueArgs.validate)
}

func applyQueue(state *BuildkiteQueueState, q *buildkitev1.BuildkiteQueue, declared *BuildkiteQueueArgs) error {
	var srv BuildkiteQueueArgs
	if err := fromProto(q.GetSettings(), &srv); err != nil {
		return err
	}
	srv.QueueName = q.GetQueueName()
	if declared != nil {
		name := srv.QueueName
		keepDeclared(*declared, &srv)
		srv.QueueName = name
		// The server may redact header values: keep the declared secrets.
		if srv.OpenTelemetrySettings != nil && declared.OpenTelemetrySettings != nil {
			dh := declared.OpenTelemetrySettings.Headers
			for i := range srv.OpenTelemetrySettings.Headers {
				if i < len(dh) && srv.OpenTelemetrySettings.Headers[i].Value == nil {
					srv.OpenTelemetrySettings.Headers[i].Value = dh[i].Value
				}
				if i < len(dh) && srv.OpenTelemetrySettings.Headers[i].ValueFrom != nil && dh[i].ValueFrom != nil &&
					srv.OpenTelemetrySettings.Headers[i].ValueFrom.Static == nil {
					srv.OpenTelemetrySettings.Headers[i].ValueFrom.Static = dh[i].ValueFrom.Static
				}
			}
		}
	}
	state.BuildkiteQueueArgs = srv
	state.QueueID = q.GetId()
	state.Cluster = q.GetCluster()
	state.OrganizationSlug = q.GetOrganization().GetOrgSlug()
	return nil
}

// mergeQueueSettings overlays the declared settings on the queue's current ones:
// UpdateQueue replaces the whole settings object, so anything not declared must
// be sent back as the server has it.
func mergeQueueSettings(cur, in BuildkiteQueueArgs) BuildkiteQueueArgs {
	out := cur
	if in.EgressPolicyTag != nil {
		out.EgressPolicyTag = in.EgressPolicyTag
	}
	if in.Permissions != nil {
		out.Permissions = in.Permissions
	}
	if in.OpenTelemetrySettings != nil {
		out.OpenTelemetrySettings = in.OpenTelemetrySettings
	}
	return out
}

func priorFrom(a BuildkiteQueueArgs) *QueuePriorSettings {
	return &QueuePriorSettings{EgressPolicyTag: a.EgressPolicyTag, Permissions: a.Permissions, OpenTelemetrySettings: a.OpenTelemetrySettings}
}

func currentQueueSettings(ctx context.Context, c *nsClient, id string) (BuildkiteQueueArgs, error) {
	var res buildkitev1.QueueResponse
	if err := c.call(ctx, c.endpoints.globalURL(), buildkiteQueueService, "GetQueue",
		&buildkitev1.GetQueueRequest{QueueId: id}, &res); err != nil {
		return BuildkiteQueueArgs{}, err
	}
	var cur BuildkiteQueueArgs
	return cur, fromProto(res.GetQueue().GetSettings(), &cur)
}

func applyQueueSettings(ctx context.Context, c *nsClient, id string, in BuildkiteQueueArgs) (*buildkitev1.BuildkiteQueue, error) {
	var settings buildkitev1.QueueSettings
	if err := toProto(in, &settings); err != nil {
		return nil, err
	}
	var res buildkitev1.QueueResponse
	if err := c.call(ctx, c.endpoints.globalURL(), buildkiteQueueService, "UpdateQueue",
		&buildkitev1.UpdateQueueRequest{QueueId: id, Settings: &settings}, &res); err != nil {
		return nil, err
	}
	return res.GetQueue(), nil
}

// Diff deletes before replacing: BuildkiteQueue adopts and later restores server-side
// state, so with the default create-before-delete order a forced replace
// (--replace, --target-replace, taint) would run the old resource's delete
// after the new create and undo what the new one adopted.
func (r *BuildkiteQueue) Diff(
	_ context.Context, req infer.DiffRequest[BuildkiteQueueArgs, BuildkiteQueueState],
) (infer.DiffResponse, error) {
	return diffDeleteFirst(req.State.BuildkiteQueueArgs, req.Inputs), nil
}

func (r *BuildkiteQueue) Create(
	ctx context.Context, req infer.CreateRequest[BuildkiteQueueArgs],
) (infer.CreateResponse[BuildkiteQueueState], error) {
	state := BuildkiteQueueState{BuildkiteQueueArgs: req.Inputs}
	name := req.Inputs.QueueName
	if req.DryRun {
		return infer.CreateResponse[BuildkiteQueueState]{ID: "", Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[BuildkiteQueueState]{}, err
	}
	var list buildkitev1.ListQueuesResponse
	if err := c.call(ctx, c.endpoints.globalURL(), buildkiteQueueService, "ListQueues", &emptypb.Empty{}, &list); err != nil {
		return infer.CreateResponse[BuildkiteQueueState]{}, fmt.Errorf("list buildkite queues: %w", err)
	}
	var match *buildkitev1.BuildkiteQueue
	for _, q := range list.GetQueues() {
		if q.GetQueueName() != name {
			continue
		}
		if match != nil {
			return infer.CreateResponse[BuildkiteQueueState]{}, fmt.Errorf(
				"buildkite queue name %q is ambiguous (it exists in several clusters or organizations); import the queue by ID instead", name)
		}
		match = q
	}
	if match == nil {
		return infer.CreateResponse[BuildkiteQueueState]{}, fmt.Errorf(
			"buildkite queue %q does not exist on Namespace: queues cannot be created through the API; create it in "+
				"Buildkite with the Namespace integration first", name)
	}
	var prior BuildkiteQueueArgs
	if err := fromProto(match.GetSettings(), &prior); err != nil {
		return infer.CreateResponse[BuildkiteQueueState]{}, err
	}
	updated, err := applyQueueSettings(ctx, c, match.GetId(), mergeQueueSettings(prior, req.Inputs))
	if err != nil {
		return infer.CreateResponse[BuildkiteQueueState]{}, fmt.Errorf("update buildkite queue %q: %w", name, err)
	}
	if updated == nil || updated.GetId() == "" {
		updated = match
	}
	// The queue's settings have been overwritten: record what to restore first.
	state.PriorRecorded = true
	state.PriorSettings = priorFrom(prior)
	if err := applyQueue(&state, updated, &req.Inputs); err != nil {
		state.QueueID = updated.GetId()
		return infer.CreateResponse[BuildkiteQueueState]{ID: updated.GetId(), Output: state},
			infer.ResourceInitFailedError{Reasons: []string{fmt.Sprintf("read back buildkite queue %q: %v", name, err)}}
	}
	return infer.CreateResponse[BuildkiteQueueState]{ID: state.QueueID, Output: state}, nil
}

func (r *BuildkiteQueue) Read(
	ctx context.Context, req infer.ReadRequest[BuildkiteQueueArgs, BuildkiteQueueState],
) (infer.ReadResponse[BuildkiteQueueArgs, BuildkiteQueueState], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.ReadResponse[BuildkiteQueueArgs, BuildkiteQueueState]{}, err
	}
	var res buildkitev1.QueueResponse
	err = c.call(ctx, c.endpoints.globalURL(), buildkiteQueueService, "GetQueue",
		&buildkitev1.GetQueueRequest{QueueId: req.ID}, &res)
	if isNotFound(err) || (err == nil && res.GetQueue() == nil) {
		if err := notFoundOnImport("buildkite queue", req.ID, req.Inputs, req.State); err != nil {
			return infer.ReadResponse[BuildkiteQueueArgs, BuildkiteQueueState]{}, err
		}
		return infer.ReadResponse[BuildkiteQueueArgs, BuildkiteQueueState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[BuildkiteQueueArgs, BuildkiteQueueState]{}, fmt.Errorf("read buildkite queue %q: %w", req.ID, err)
	}
	state := BuildkiteQueueState{PriorRecorded: req.State.PriorRecorded, PriorSettings: req.State.PriorSettings}
	var declared *BuildkiteQueueArgs
	if req.Inputs.QueueName != "" {
		d := req.Inputs
		declared = &d
	}
	if err := applyQueue(&state, res.GetQueue(), declared); err != nil {
		return infer.ReadResponse[BuildkiteQueueArgs, BuildkiteQueueState]{}, err
	}
	return infer.ReadResponse[BuildkiteQueueArgs, BuildkiteQueueState]{ID: state.QueueID, Inputs: state.BuildkiteQueueArgs, State: state}, nil
}

func (r *BuildkiteQueue) Update(
	ctx context.Context, req infer.UpdateRequest[BuildkiteQueueArgs, BuildkiteQueueState],
) (infer.UpdateResponse[BuildkiteQueueState], error) {
	state := req.State
	state.BuildkiteQueueArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[BuildkiteQueueState]{Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[BuildkiteQueueState]{}, err
	}
	cur, err := currentQueueSettings(ctx, c, req.ID)
	if err != nil {
		return infer.UpdateResponse[BuildkiteQueueState]{}, fmt.Errorf("read buildkite queue %q: %w", req.ID, err)
	}
	updated, err := applyQueueSettings(ctx, c, req.ID, mergeQueueSettings(cur, req.Inputs))
	if err != nil {
		return infer.UpdateResponse[BuildkiteQueueState]{}, fmt.Errorf("update buildkite queue %q: %w", req.ID, err)
	}
	if updated != nil {
		if err := applyQueue(&state, updated, &req.Inputs); err != nil {
			return infer.UpdateResponse[BuildkiteQueueState]{}, err
		}
	}
	return infer.UpdateResponse[BuildkiteQueueState]{Output: state}, nil
}

// Delete restores the settings recorded at adoption. The queue itself is not
// removed: Namespace has no delete for it.
func (r *BuildkiteQueue) Delete(
	ctx context.Context, req infer.DeleteRequest[BuildkiteQueueState],
) (infer.DeleteResponse, error) {
	if !req.State.PriorRecorded {
		return infer.DeleteResponse{}, nil // imported: nothing was recorded to restore
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	var restore BuildkiteQueueArgs
	if p := req.State.PriorSettings; p != nil {
		restore = BuildkiteQueueArgs{EgressPolicyTag: p.EgressPolicyTag, Permissions: p.Permissions, OpenTelemetrySettings: p.OpenTelemetrySettings}
	}
	if _, err := applyQueueSettings(ctx, c, req.ID, restore); err != nil {
		if isNotFound(err) {
			return infer.DeleteResponse{}, nil
		}
		return infer.DeleteResponse{}, fmt.Errorf("restore buildkite queue %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// ---- getBuildkiteQueue / getBuildkiteQueues ----

// BuildkiteQueueInfo is a Buildkite queue's Namespace settings as the API reports them.
type BuildkiteQueueInfo struct {
	QueueID               string            `pulumi:"queueId"`
	QueueName             string            `pulumi:"queueName"`
	Cluster               string            `pulumi:"cluster"`
	OrganizationSlug      string            `pulumi:"organizationSlug"`
	EgressPolicyTag       *string           `pulumi:"egressPolicyTag,optional"`
	Permissions           *QueuePermissions `pulumi:"permissions,optional"`
	OpenTelemetrySettings *QueueOtel        `pulumi:"openTelemetrySettings,optional"`
}

func (q *BuildkiteQueueInfo) Annotate(ann infer.Annotator) {
	ann.Describe(&q.QueueID, "The Namespace queue identifier.")
	ann.Describe(&q.QueueName, "The Buildkite queue name.")
	ann.Describe(&q.Cluster, "The Buildkite cluster the queue belongs to.")
	ann.Describe(&q.OrganizationSlug, "The Buildkite organization slug.")
	ann.Describe(&q.EgressPolicyTag, "Tag of the egress policy applied to jobs of the queue.")
	ann.Describe(&q.Permissions, "Workload permissions of the queue.")
	ann.Describe(&q.OpenTelemetrySettings, "OpenTelemetry export settings. Header values may be redacted by the API.")
}

func queueInfo(q *buildkitev1.BuildkiteQueue) (BuildkiteQueueInfo, error) {
	var st BuildkiteQueueState
	if err := applyQueue(&st, q, nil); err != nil {
		return BuildkiteQueueInfo{}, err
	}
	return BuildkiteQueueInfo{
		QueueID: st.QueueID, QueueName: st.QueueName, Cluster: st.Cluster, OrganizationSlug: st.OrganizationSlug,
		EgressPolicyTag: st.EgressPolicyTag, Permissions: st.Permissions, OpenTelemetrySettings: st.OpenTelemetrySettings,
	}, nil
}

func listQueues(ctx context.Context, c *nsClient) ([]*buildkitev1.BuildkiteQueue, error) {
	var res buildkitev1.ListQueuesResponse
	if err := c.call(ctx, c.endpoints.globalURL(), buildkiteQueueService, "ListQueues", &buildkitev1.ListQueuesRequest{}, &res); err != nil {
		return nil, err
	}
	return res.GetQueues(), nil
}

// GetBuildkiteQueue reads one Buildkite queue.
type GetBuildkiteQueue struct{}

type GetBuildkiteQueueArgs struct {
	QueueID   *string `pulumi:"queueId,optional"`
	QueueName *string `pulumi:"queueName,optional"`
}

func (f *GetBuildkiteQueue) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads the Namespace settings of one Buildkite queue, by queue ID or queue name.")
}

func (a *GetBuildkiteQueueArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.QueueID, "The Namespace queue ID. Set exactly one of queueId and queueName.")
	ann.Describe(&a.QueueName, "The Buildkite queue name. Set exactly one of queueId and queueName.")
}

func (f *GetBuildkiteQueue) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetBuildkiteQueueArgs],
) (infer.FunctionResponse[BuildkiteQueueInfo], error) {
	type resp = infer.FunctionResponse[BuildkiteQueueInfo]
	id, name := derefStr(req.Input.QueueID), derefStr(req.Input.QueueName)
	if (id == "") == (name == "") {
		return resp{}, errExactlyOne("getBuildkiteQueue", "queueId", "queueName")
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	var found *buildkitev1.BuildkiteQueue
	if id != "" {
		var res buildkitev1.QueueResponse
		err := c.call(ctx, c.endpoints.globalURL(), buildkiteQueueService, "GetQueue", &buildkitev1.GetQueueRequest{QueueId: id}, &res)
		if err != nil && !isNotFound(err) {
			return resp{}, fmt.Errorf("read buildkite queue %q: %w", id, err)
		}
		found = res.GetQueue()
	} else {
		qs, err := listQueues(ctx, c)
		if err != nil {
			return resp{}, fmt.Errorf("list buildkite queues: %w", err)
		}
		for _, q := range qs {
			if q.GetQueueName() == name {
				found = q
				break
			}
		}
	}
	if found == nil {
		return resp{}, errLookupNotFound("buildkite queue", id+name)
	}
	info, err := queueInfo(found)
	return resp{Output: info}, err
}

// GetBuildkiteQueues lists Buildkite queues.
type GetBuildkiteQueues struct{}

type GetBuildkiteQueuesArgs struct{}

type GetBuildkiteQueuesResult struct {
	Queues []BuildkiteQueueInfo `pulumi:"queues"`
}

func (f *GetBuildkiteQueues) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the Buildkite queues connected to Namespace, with their Namespace settings.")
}

func (r *GetBuildkiteQueuesResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.Queues, "The Buildkite queues.")
}

func (f *GetBuildkiteQueues) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetBuildkiteQueuesArgs],
) (infer.FunctionResponse[GetBuildkiteQueuesResult], error) {
	type resp = infer.FunctionResponse[GetBuildkiteQueuesResult]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	qs, err := listQueues(ctx, c)
	if err != nil {
		return resp{}, fmt.Errorf("list buildkite queues: %w", err)
	}
	out := GetBuildkiteQueuesResult{Queues: []BuildkiteQueueInfo{}}
	for _, q := range qs {
		info, err := queueInfo(q)
		if err != nil {
			return resp{}, err
		}
		out.Queues = append(out.Queues, info)
	}
	return resp{Output: out}, nil
}
