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
	"math"
	"time"

	computev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/compute/v1beta"
	"buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/stdlib"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// Instance resource
// ---------------------------------------------------------------------------

// Instance is an ephemeral Namespace compute instance: a VM that runs a set of
// containers and is destroyed automatically at its deadline.
type Instance struct{}

// InstanceShape is the hardware shape of an instance.
type InstanceShape struct {
	// VirtualCPU is the number of virtual CPUs.
	VirtualCPU int `pulumi:"virtualCpu"`
	// MemoryMegabytes is the amount of memory in megabytes.
	MemoryMegabytes int `pulumi:"memoryMegabytes"`
	// MachineArch is the CPU architecture, such as "amd64" or "arm64".
	MachineArch *string `pulumi:"machineArch,optional"`
	// OS is the operating system, such as "linux".
	OS *string `pulumi:"os,optional"`
}

func (s *InstanceShape) Annotate(a infer.Annotator) {
	a.Describe(&s.VirtualCPU, "Number of virtual CPUs.")
	a.Describe(&s.MemoryMegabytes, "Memory in megabytes.")
	a.Describe(&s.MachineArch, "CPU architecture, such as 'amd64' or 'arm64'. Defaults to the platform default.")
	a.Describe(&s.OS, "Operating system, such as 'linux'. Defaults to the platform default.")
}

// ContainerPortSpec exports a container port through the instance ingress.
type ContainerPortSpec struct {
	// Name identifies the exported port.
	Name *string `pulumi:"name,optional"`
	// ContainerPort is the port the container listens on.
	ContainerPort int `pulumi:"containerPort"`
	// Proto is the protocol: HTTP, TCP, H2C or TLS_PASSTHROUGH.
	Proto *string `pulumi:"proto,optional"`
}

func (p *ContainerPortSpec) Annotate(a infer.Annotator) {
	a.Describe(&p.Name, "Name of the exported port.")
	a.Describe(&p.ContainerPort, "Port the container listens on.")
	a.Describe(&p.Proto, "Protocol: one of HTTP, TCP, H2C, TLS_PASSTHROUGH.")
}

// ContainerSpec describes one container started on the instance.
type ContainerSpec struct {
	// Name is the container name, unique within the instance.
	Name string `pulumi:"name"`
	// ImageRef is the container image reference.
	ImageRef string `pulumi:"imageRef"`
	// Args override the image command arguments.
	Args []string `pulumi:"args,optional"`
	// Entrypoint overrides the image entrypoint.
	Entrypoint []string `pulumi:"entrypoint,optional"`
	// Env are plain environment variables. Use a vault object for secrets.
	Env map[string]string `pulumi:"env,optional"`
	// ExportPorts are the ports exposed through the instance ingress.
	ExportPorts []ContainerPortSpec `pulumi:"exportPorts,optional"`
}

func (c *ContainerSpec) Annotate(a infer.Annotator) {
	a.Describe(&c.Name, "Container name, unique within the instance.")
	a.Describe(&c.ImageRef, "Container image reference, such as 'nginx:1.27'.")
	a.Describe(&c.Args, "Arguments passed to the container, overriding the image command.")
	a.Describe(&c.Entrypoint, "Overrides the image entrypoint.")
	a.Describe(&c.Env, "Environment variables. These are stored in state in plain text; "+
		"do not put secrets here.")
	a.Describe(&c.ExportPorts, "Ports exported through the instance ingress.")
}

// InstanceArgs are the user-supplied inputs for an instance.
type InstanceArgs struct {
	// Shape is the hardware shape. Immutable.
	Shape InstanceShape `pulumi:"shape" provider:"replaceOnChanges"`

	// DocumentedPurpose is a free-form note on why the instance exists. Immutable.
	DocumentedPurpose *string `pulumi:"documentedPurpose,optional" provider:"replaceOnChanges"`

	// Labels attached to the instance. Immutable.
	Labels map[string]string `pulumi:"labels,optional" provider:"replaceOnChanges"`

	// Deadline is when the instance is destroyed automatically, as RFC3339.
	// It can be moved later in place.
	Deadline *string `pulumi:"deadline,optional"`

	// Interactive enables interactive access to the instance. Immutable.
	Interactive *bool `pulumi:"interactive,optional" provider:"replaceOnChanges"`

	// Region overrides the provider region for this instance. Immutable.
	Region *string `pulumi:"region,optional" provider:"replaceOnChanges"`

	// Containers started on the instance. Immutable.
	Containers []ContainerSpec `pulumi:"containers,optional" provider:"replaceOnChanges"`
}

// InstanceServiceState describes a service endpoint reported by the instance.
type InstanceServiceState struct {
	Name         string `pulumi:"name"`
	Status       string `pulumi:"status"`
	EndpointKind string `pulumi:"endpointKind"`
	Endpoint     string `pulumi:"endpoint"`
}

// InstanceState is the persisted state of an instance.
type InstanceState struct {
	InstanceArgs

	// InstanceID is the server-assigned instance identifier.
	InstanceID string `pulumi:"instanceId"`
	// CreatedAt is when the instance was created, as RFC3339.
	CreatedAt string `pulumi:"createdAt"`
	// ReadyAt is when the instance became ready, as RFC3339.
	ReadyAt string `pulumi:"readyAt"`
	// ExpiresAt is the deadline the server is enforcing, as RFC3339.
	ExpiresAt string `pulumi:"expiresAt"`
	// Status is the lifecycle status: PENDING, CREATING, RUNNING, ...
	Status string `pulumi:"status"`
	// InstanceURL is the dashboard URL of the instance.
	InstanceURL string `pulumi:"instanceUrl"`
	// IngressDomain is the domain under which exported ports are served.
	IngressDomain string `pulumi:"ingressDomain"`
	// Services lists the endpoints exposed by the instance.
	Services []InstanceServiceState `pulumi:"services"`
	// EffectiveRegion is the region the instance was created in. Read, Update
	// and Delete talk to this region, so changing the provider region or
	// NSC_REGION later cannot orphan the instance.
	EffectiveRegion *string `pulumi:"effectiveRegion,optional"`
}

// regionForState is the region Read, Update and Delete must talk to: the one
// recorded at creation, then the declared region (state written before
// effectiveRegion existed), then the provider default.
func regionForState(s InstanceState) *string {
	if s.EffectiveRegion != nil && *s.EffectiveRegion != "" {
		return s.EffectiveRegion
	}
	return s.Region
}

func (r *Instance) Annotate(a infer.Annotator) {
	a.Describe(&r, "An ephemeral Namespace compute instance. Instances are destroyed automatically at "+
		"their deadline; the deadline can be extended in place, every other input replaces the instance.")
}

func (a *InstanceArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Shape, "Hardware shape of the instance. Changing it replaces the instance.")
	ann.Describe(&a.DocumentedPurpose, "Free-form note on why the instance exists. Changing it replaces the instance.")
	ann.Describe(&a.Labels, "Labels attached to the instance. Changing them replaces the instance.")
	ann.Describe(&a.Deadline, "RFC3339 timestamp at which the instance is destroyed automatically. "+
		"Can only be moved later, in place (ExtendInstance).")
	ann.Describe(&a.Interactive, "Whether the instance allows interactive access. Changing it replaces the instance.")
	ann.Describe(&a.Region, "Region to create the instance in, such as 'us' or 'eu'. "+
		"Defaults to the provider region. Changing it replaces the instance.")
	ann.Describe(&a.Containers, "Containers to run. Changing them replaces the instance.")
}

func (s *InstanceState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.InstanceID, "The server-assigned instance identifier.")
	ann.Describe(&s.CreatedAt, "RFC3339 timestamp of when the instance was created.")
	ann.Describe(&s.ReadyAt, "RFC3339 timestamp of when the instance became ready.")
	ann.Describe(&s.ExpiresAt, "RFC3339 deadline currently enforced by the platform.")
	ann.Describe(&s.Status, "Lifecycle status, such as RUNNING.")
	ann.Describe(&s.InstanceURL, "Dashboard URL of the instance.")
	ann.Describe(&s.IngressDomain, "Domain under which exported ports are served.")
	ann.Describe(&s.Services, "Service endpoints reported by the instance.")
	ann.Describe(&s.EffectiveRegion, "The region the instance actually runs in (the resource region, else the provider "+
		"region at creation time). Later operations always use this region.")
}

// Timing knobs for readiness polling. Variables so tests can shorten them.
var (
	instanceReadyTimeout = 10 * time.Minute
	instancePollInterval = 2 * time.Second
)

func toInt32(field string, v int) (int32, error) {
	if v < 0 || v > math.MaxInt32 {
		return 0, fmt.Errorf("%s must be between 0 and %d, got %d", field, math.MaxInt32, v)
	}
	return int32(v), nil
}

func buildCreateInstanceRequest(in InstanceArgs) (*computev1.CreateInstanceRequest, error) {
	cpu, err := toInt32("shape.virtualCpu", in.Shape.VirtualCPU)
	if err != nil {
		return nil, err
	}
	mem, err := toInt32("shape.memoryMegabytes", in.Shape.MemoryMegabytes)
	if err != nil {
		return nil, err
	}
	if cpu == 0 || mem == 0 {
		return nil, fmt.Errorf("shape.virtualCpu and shape.memoryMegabytes must be greater than zero")
	}
	req := &computev1.CreateInstanceRequest{
		Shape: &computev1.InstanceShape{
			VirtualCpu:      cpu,
			MemoryMegabytes: mem,
			MachineArch:     derefStr(in.Shape.MachineArch),
			Os:              derefStr(in.Shape.OS),
		},
		DocumentedPurpose: derefStr(in.DocumentedPurpose),
		Labels:            labelsToProto(in.Labels),
		Interactive:       derefBool(in.Interactive),
		Region:            derefStr(in.Region),
	}
	if in.Deadline != nil && *in.Deadline != "" {
		ts, err := parseRFC3339("deadline", *in.Deadline)
		if err != nil {
			return nil, err
		}
		req.Deadline = ts
	}
	for i, c := range in.Containers {
		cr := &computev1.ContainerRequest{
			Name:        c.Name,
			ImageRef:    c.ImageRef,
			Args:        c.Args,
			Entrypoint:  c.Entrypoint,
			Environment: c.Env,
		}
		for j, p := range c.ExportPorts {
			port, err := toInt32(fmt.Sprintf("containers[%d].exportPorts[%d].containerPort", i, j), p.ContainerPort)
			if err != nil {
				return nil, err
			}
			cp := &computev1.ContainerPort{ContainerPort: port, Name: derefStr(p.Name)}
			if p.Proto != nil && *p.Proto != "" {
				v, ok := computev1.ContainerPort_Proto_value[*p.Proto]
				if !ok || v == 0 {
					return nil, fmt.Errorf("containers[%d].exportPorts[%d].proto %q is not one of HTTP, TCP, H2C, TLS_PASSTHROUGH",
						i, j, *p.Proto)
				}
				cp.Proto = computev1.ContainerPort_Proto(v)
			}
			cr.ExportPorts = append(cr.ExportPorts, cp)
		}
		req.Containers = append(req.Containers, cr)
	}
	return req, nil
}

func (r *Instance) Create(
	ctx context.Context,
	req infer.CreateRequest[InstanceArgs],
) (infer.CreateResponse[InstanceState], error) {
	state := InstanceState{InstanceArgs: req.Inputs}
	apiReq, err := buildCreateInstanceRequest(req.Inputs)
	if err != nil {
		return infer.CreateResponse[InstanceState]{}, err
	}
	if req.DryRun {
		return infer.CreateResponse[InstanceState]{ID: "preview", Output: state}, nil
	}
	c, err := clientFor(ctx, req.Inputs.Region)
	if err != nil {
		return infer.CreateResponse[InstanceState]{}, err
	}
	state.EffectiveRegion = ptr(c.region)
	var res computev1.DescribeInstanceResponse
	if err := c.call(ctx, c.endpoints.computeURL(c.region), computeService, "CreateInstance", apiReq, &res); err != nil {
		return infer.CreateResponse[InstanceState]{}, fmt.Errorf("create instance: %w", err)
	}
	id := res.GetMetadata().GetInstanceId()
	if id == "" {
		return infer.CreateResponse[InstanceState]{}, fmt.Errorf("create instance: the API returned no instance id")
	}
	applyInstance(&state, &res)

	final, err := waitInstanceReady(ctx, c, id, &res)
	if final != nil {
		applyInstance(&state, final)
	}
	if err != nil {
		// The instance exists: record it so Pulumi can retry or destroy it.
		return infer.CreateResponse[InstanceState]{ID: id, Output: state},
			infer.ResourceInitFailedError{Reasons: []string{err.Error()}}
	}
	return infer.CreateResponse[InstanceState]{ID: id, Output: state}, nil
}

// waitInstanceReady polls DescribeInstance until the instance is RUNNING. It
// polls the unary RPC rather than the streaming WaitInstance RPC so it works
// over plain request/response HTTP. last is the most recent known description.
func waitInstanceReady(
	ctx context.Context, c *nsClient, id string, last *computev1.DescribeInstanceResponse,
) (*computev1.DescribeInstanceResponse, error) {
	deadline := time.Now().Add(instanceReadyTimeout)
	cur := last
	for {
		switch st := cur.GetMetadata().GetStatus(); st {
		case computev1.InstanceMetadata_RUNNING:
			return cur, nil
		case computev1.InstanceMetadata_ERROR, computev1.InstanceMetadata_DESTROYED, computev1.InstanceMetadata_DESTROYING:
			return cur, fmt.Errorf("instance %s entered status %s%s", id, st, shutdownDetail(cur))
		}
		if time.Now().After(deadline) {
			return cur, fmt.Errorf("instance %s was not RUNNING after %s (last status %s)",
				id, instanceReadyTimeout, cur.GetMetadata().GetStatus())
		}
		select {
		case <-ctx.Done():
			return cur, ctx.Err()
		case <-time.After(instancePollInterval):
		}
		var res computev1.DescribeInstanceResponse
		err := c.call(ctx, c.endpoints.computeURL(c.region), computeService, "DescribeInstance",
			&computev1.DescribeInstanceRequest{InstanceId: id}, &res)
		if err != nil {
			return cur, fmt.Errorf("waiting for instance %s: %w", id, err)
		}
		cur = &res
	}
}

func shutdownDetail(res *computev1.DescribeInstanceResponse) string {
	for _, s := range res.GetShutdownReasons() {
		if s.GetErrorMessage() != "" {
			return ": " + s.GetErrorMessage()
		}
	}
	return ""
}

func (r *Instance) Read(
	ctx context.Context,
	req infer.ReadRequest[InstanceArgs, InstanceState],
) (infer.ReadResponse[InstanceArgs, InstanceState], error) {
	type resp = infer.ReadResponse[InstanceArgs, InstanceState]
	imported := req.State.InstanceID == ""
	c, err := clientFor(ctx, regionForState(req.State))
	if err != nil {
		return resp{}, err
	}
	var res computev1.DescribeInstanceResponse
	err = c.call(ctx, c.endpoints.computeURL(c.region), computeService, "DescribeInstance",
		&computev1.DescribeInstanceRequest{InstanceId: req.ID}, &res)
	if err != nil {
		if isNotFound(err) {
			return resp{}, nil
		}
		return resp{}, fmt.Errorf("read instance %q: %w", req.ID, err)
	}
	md := res.GetMetadata()
	if md.GetStatus() == computev1.InstanceMetadata_DESTROYED || md.GetDestroyedAt() != nil {
		// Deadline reached or destroyed out of band: the resource is gone.
		return resp{}, nil
	}
	state := req.State
	if state.EffectiveRegion == nil || *state.EffectiveRegion == "" {
		// Import, or state written before effectiveRegion existed: remember
		// the region the instance was read from.
		state.EffectiveRegion = ptr(c.region)
	}
	if imported {
		// Import: reconstruct what the API reports. The region input stays
		// unset so a program that relies on the provider region does not see
		// a replacement; effectiveRegion pins the region instead.
		if len(res.GetContainers()) > 0 {
			// DescribeInstance reports only container ids, names and ports, not the
			// image, args, entrypoint or env. A state without them would diff
			// against the program and replace the instance on the next up, so
			// refuse rather than import something that is wrong by construction.
			return resp{}, fmt.Errorf(
				"import instance %q: it runs %d container(s) and the API does not report their image, args, "+
					"entrypoint or env, so the imported state would be incomplete and the next update would "+
					"replace the instance; import is only supported for instances without containers",
				req.ID, len(res.GetContainers()))
		}
		state.Shape = InstanceShape{
			VirtualCPU:      int(md.GetShape().GetVirtualCpu()),
			MemoryMegabytes: int(md.GetShape().GetMemoryMegabytes()),
			MachineArch:     emptyToNil(md.GetShape().GetMachineArch()),
			OS:              emptyToNil(md.GetShape().GetOs()),
		}
		state.DocumentedPurpose = emptyToNil(md.GetDocumentedPurpose())
		state.Deadline = emptyToNil(formatTimestamp(md.GetDeadline()))
		state.Labels = labelsView(nil, labelsFromProto(md.GetLabels()), true)
	} else {
		// Refresh: report drift in everything the API reports, but never turn a
		// server-filled default of an optional input into a declared value.
		state.Shape.VirtualCPU = int(md.GetShape().GetVirtualCpu())
		state.Shape.MemoryMegabytes = int(md.GetShape().GetMemoryMegabytes())
		if state.Shape.MachineArch != nil {
			state.Shape.MachineArch = ptr(md.GetShape().GetMachineArch())
		}
		if state.Shape.OS != nil {
			state.Shape.OS = ptr(md.GetShape().GetOs())
		}
		if state.DocumentedPurpose != nil || md.GetDocumentedPurpose() != "" {
			state.DocumentedPurpose = ptr(md.GetDocumentedPurpose())
		}
		if state.Deadline != nil {
			// Keep the declared text while it denotes the enforced deadline, so
			// formatting differences are not drift; a different server deadline is.
			declared, perr := time.Parse(time.RFC3339, *state.Deadline)
			enforced := md.GetDeadline().AsTime()
			if md.GetDeadline() != nil && (perr != nil || declared.Sub(enforced).Abs() > time.Second) {
				state.Deadline = ptr(formatTimestamp(md.GetDeadline()))
			}
		}
		state.Labels = labelsView(req.State.Labels, labelsFromProto(md.GetLabels()), false)
	}
	applyInstance(&state, &res)
	return resp{ID: req.ID, Inputs: state.InstanceArgs, State: state}, nil
}

func errShortenDeadline(want, have time.Time) error {
	return fmt.Errorf(
		"deadline %s is earlier than the current deadline %s: instance deadlines can only be extended; "+
			"to shorten the lifetime, replace the instance",
		want.UTC().Format(time.RFC3339), have.UTC().Format(time.RFC3339))
}

func (r *Instance) Update(
	ctx context.Context,
	req infer.UpdateRequest[InstanceArgs, InstanceState],
) (infer.UpdateResponse[InstanceState], error) {
	state := req.State
	state.InstanceArgs = req.Inputs
	var want time.Time
	if req.Inputs.Deadline != nil && *req.Inputs.Deadline != "" {
		ts, err := parseRFC3339("deadline", *req.Inputs.Deadline)
		if err != nil {
			return infer.UpdateResponse[InstanceState]{}, err
		}
		want = ts.AsTime()
	}
	// Validate before the DryRun return so a preview fails exactly when the
	// apply would, instead of passing preview and failing during up.
	if !want.IsZero() && req.State.ExpiresAt != "" {
		if have, perr := time.Parse(time.RFC3339, req.State.ExpiresAt); perr == nil && want.Before(have.Add(-time.Second)) {
			return infer.UpdateResponse[InstanceState]{}, errShortenDeadline(want, have)
		}
	}
	if req.DryRun {
		return infer.UpdateResponse[InstanceState]{Output: state}, nil
	}
	c, err := clientFor(ctx, regionForState(req.State))
	if err != nil {
		return infer.UpdateResponse[InstanceState]{}, err
	}
	base := c.endpoints.computeURL(c.region)

	if !want.IsZero() {
		var have time.Time
		if req.State.ExpiresAt != "" {
			have, _ = time.Parse(time.RFC3339, req.State.ExpiresAt)
		}
		switch {
		case !have.IsZero() && want.Before(have.Add(-time.Second)):
			// Unreachable after the preview check above; kept as a guard.
			return infer.UpdateResponse[InstanceState]{}, errShortenDeadline(want, have)
		case have.IsZero() || want.After(have.Add(time.Second)):
			var ext computev1.ExtendInstanceResponse
			err := c.call(ctx, base, computeService, "ExtendInstance", &computev1.ExtendInstanceRequest{
				InstanceId:  req.ID,
				NewDeadline: parseTS(want),
			}, &ext)
			if err != nil {
				return infer.UpdateResponse[InstanceState]{}, fmt.Errorf("extend instance %q: %w", req.ID, err)
			}
			if ts := formatTimestamp(ext.GetNewDeadline()); ts != "" {
				state.ExpiresAt = ts
			}
		}
	}

	// Refresh outputs from the server, and finish a readiness wait that an
	// earlier failed create left unfinished.
	var res computev1.DescribeInstanceResponse
	if err := c.call(ctx, base, computeService, "DescribeInstance",
		&computev1.DescribeInstanceRequest{InstanceId: req.ID}, &res); err != nil {
		return infer.UpdateResponse[InstanceState]{}, fmt.Errorf("describe instance %q: %w", req.ID, err)
	}
	final, werr := waitInstanceReady(ctx, c, req.ID, &res)
	if final != nil {
		applyInstance(&state, final)
	}
	if werr != nil {
		return infer.UpdateResponse[InstanceState]{Output: state},
			infer.ResourceInitFailedError{Reasons: []string{werr.Error()}}
	}
	return infer.UpdateResponse[InstanceState]{Output: state}, nil
}

func (r *Instance) Delete(
	ctx context.Context,
	req infer.DeleteRequest[InstanceState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, regionForState(req.State))
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.computeURL(c.region), computeService, "DestroyInstance",
		&computev1.DestroyInstanceRequest{InstanceId: req.ID, Reason: "destroyed by Pulumi"}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("destroy instance %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// applyInstance copies the server-owned fields of a description onto state.
func applyInstance(state *InstanceState, res *computev1.DescribeInstanceResponse) {
	md := res.GetMetadata()
	if id := md.GetInstanceId(); id != "" {
		state.InstanceID = id
	}
	state.CreatedAt = formatTimestamp(md.GetCreatedAt())
	state.ReadyAt = formatTimestamp(md.GetReadyAt())
	state.ExpiresAt = formatTimestamp(md.GetDeadline())
	state.Status = md.GetStatus().String()
	state.InstanceURL = res.GetInstanceUrl()
	state.IngressDomain = md.GetIngressDomain()
	// Never nil: a nil slice is encoded as absent and then fails to decode as a
	// required output on the next Read, Update or Delete.
	state.Services = []InstanceServiceState{}
	for _, s := range md.GetServices() {
		state.Services = append(state.Services, InstanceServiceState{
			Name:         s.GetName(),
			Status:       s.GetStatus().String(),
			EndpointKind: s.GetEndpointKind().String(),
			Endpoint:     s.GetEndpoint(),
		})
	}
}

// ---------------------------------------------------------------------------
// getInstances function
// ---------------------------------------------------------------------------

// GetInstances lists compute instances.
type GetInstances struct{}

// GetInstancesArgs filters the instance listing.
type GetInstancesArgs struct {
	// Region selects the regional compute endpoint. Defaults to the provider region.
	Region *string `pulumi:"region,optional"`
	// Labels restricts the result to instances carrying all of these labels.
	Labels map[string]string `pulumi:"labels,optional"`
	// IncludeDestroyed also returns instances that have already finished.
	IncludeDestroyed *bool `pulumi:"includeDestroyed,optional"`
}

// InstanceSummary describes one instance in a listing.
type InstanceSummary struct {
	InstanceID        string            `pulumi:"instanceId"`
	Status            string            `pulumi:"status"`
	CreatedAt         string            `pulumi:"createdAt"`
	ExpiresAt         string            `pulumi:"expiresAt"`
	DocumentedPurpose string            `pulumi:"documentedPurpose"`
	VirtualCPU        int               `pulumi:"virtualCpu"`
	MemoryMegabytes   int               `pulumi:"memoryMegabytes"`
	MachineArch       string            `pulumi:"machineArch"`
	IngressDomain     string            `pulumi:"ingressDomain"`
	Labels            map[string]string `pulumi:"labels"`
}

// GetInstancesResult is the instance listing.
type GetInstancesResult struct {
	Instances []InstanceSummary `pulumi:"instances"`
}

func (f *GetInstances) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists Namespace compute instances in a region, optionally filtered by labels.")
}

func (a *GetInstancesArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Region, "Region to list, such as 'us' or 'eu'. Defaults to the provider region.")
	ann.Describe(&a.Labels, "Only return instances carrying all of these labels.")
	ann.Describe(&a.IncludeDestroyed, "Also return instances that have already finished. Defaults to false.")
}

func (r *GetInstancesResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.Instances, "The matching instances.")
}

func (f *GetInstances) Invoke(
	ctx context.Context,
	req infer.FunctionRequest[GetInstancesArgs],
) (infer.FunctionResponse[GetInstancesResult], error) {
	c, err := clientFor(ctx, req.Input.Region)
	if err != nil {
		return infer.FunctionResponse[GetInstancesResult]{}, err
	}
	base := c.endpoints.computeURL(c.region)
	var filters []*stdlib.LabelFilterEntry
	for _, l := range labelsToProto(req.Input.Labels) {
		filters = append(filters, &stdlib.LabelFilterEntry{Name: l.GetName(), Value: l.GetValue(), Op: stdlib.LabelFilterEntry_EQUAL})
	}
	out := GetInstancesResult{Instances: []InstanceSummary{}}
	err = paginate("ListInstances", func(cursor []byte) ([]byte, error) {
		var res computev1.ListInstancesResponse
		err := c.call(ctx, base, computeService, "ListInstances", &computev1.ListInstancesRequest{
			PaginationCursor:    cursor,
			MaxEntries:          100,
			IncludeCompleteRuns: derefBool(req.Input.IncludeDestroyed),
			LabelFilter:         filters,
		}, &res)
		if err != nil {
			return nil, fmt.Errorf("list instances: %w", err)
		}
		for _, m := range res.GetInstances() {
			out.Instances = append(out.Instances, InstanceSummary{
				InstanceID:        m.GetInstanceId(),
				Status:            m.GetStatus().String(),
				CreatedAt:         formatTimestamp(m.GetCreatedAt()),
				ExpiresAt:         formatTimestamp(m.GetDeadline()),
				DocumentedPurpose: m.GetDocumentedPurpose(),
				VirtualCPU:        int(m.GetShape().GetVirtualCpu()),
				MemoryMegabytes:   int(m.GetShape().GetMemoryMegabytes()),
				MachineArch:       m.GetShape().GetMachineArch(),
				IngressDomain:     m.GetIngressDomain(),
				Labels:            labelsFromProto(m.GetLabels()),
			})
		}
		return res.GetPaginationCursor(), nil
	})
	if err != nil {
		return infer.FunctionResponse[GetInstancesResult]{}, err
	}
	return infer.FunctionResponse[GetInstancesResult]{Output: out}, nil
}

// ---- getInstance ----

// GetInstance reads one compute instance.
type GetInstance struct{}

type GetInstanceArgs struct {
	InstanceID string  `pulumi:"instanceId"`
	Region     *string `pulumi:"region,optional"`
}

type GetInstanceResult struct {
	InstanceID        string                 `pulumi:"instanceId"`
	Region            string                 `pulumi:"region"`
	Status            string                 `pulumi:"status"`
	CreatedAt         string                 `pulumi:"createdAt"`
	ReadyAt           string                 `pulumi:"readyAt"`
	ExpiresAt         string                 `pulumi:"expiresAt"`
	DocumentedPurpose string                 `pulumi:"documentedPurpose"`
	Shape             InstanceShape          `pulumi:"shape"`
	Labels            map[string]string      `pulumi:"labels"`
	InstanceURL       string                 `pulumi:"instanceUrl"`
	IngressDomain     string                 `pulumi:"ingressDomain"`
	Services          []InstanceServiceState `pulumi:"services"`
	ContainerCount    int                    `pulumi:"containerCount"`
}

func (f *GetInstance) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one Namespace compute instance by ID, including instances that run containers. "+
		"Fails when the instance does not exist or has been destroyed.")
}

func (a *GetInstanceArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.InstanceID, "The instance ID.")
	ann.Describe(&a.Region, "Region the instance runs in, such as 'us' or 'eu'. Defaults to the provider region.")
}

func (r *GetInstanceResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.InstanceID, "The instance ID.")
	ann.Describe(&r.Region, "The region the instance was read from.")
	ann.Describe(&r.Status, "Lifecycle status, such as RUNNING.")
	ann.Describe(&r.CreatedAt, "RFC3339 creation time.")
	ann.Describe(&r.ReadyAt, "RFC3339 time the instance became ready.")
	ann.Describe(&r.ExpiresAt, "RFC3339 deadline currently enforced.")
	ann.Describe(&r.DocumentedPurpose, "The free-form purpose note.")
	ann.Describe(&r.Shape, "The hardware shape.")
	ann.Describe(&r.Labels, "Labels attached to the instance, including system labels.")
	ann.Describe(&r.InstanceURL, "Dashboard URL of the instance.")
	ann.Describe(&r.IngressDomain, "Domain under which exported ports are served.")
	ann.Describe(&r.Services, "Service endpoints reported by the instance.")
	ann.Describe(&r.ContainerCount, "Number of containers running on the instance.")
}

func (f *GetInstance) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetInstanceArgs],
) (infer.FunctionResponse[GetInstanceResult], error) {
	type resp = infer.FunctionResponse[GetInstanceResult]
	c, err := clientFor(ctx, req.Input.Region)
	if err != nil {
		return resp{}, err
	}
	var res computev1.DescribeInstanceResponse
	err = c.call(ctx, c.endpoints.computeURL(c.region), computeService, "DescribeInstance",
		&computev1.DescribeInstanceRequest{InstanceId: req.Input.InstanceID}, &res)
	if isNotFound(err) {
		return resp{}, errLookupNotFound("instance", req.Input.InstanceID)
	}
	if err != nil {
		return resp{}, fmt.Errorf("read instance %q: %w", req.Input.InstanceID, err)
	}
	md := res.GetMetadata()
	if md.GetStatus() == computev1.InstanceMetadata_DESTROYED || md.GetDestroyedAt() != nil {
		return resp{}, errLookupNotFound("instance", req.Input.InstanceID)
	}
	var st InstanceState
	applyInstance(&st, &res)
	labels := labelsFromProto(md.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}
	return resp{Output: GetInstanceResult{
		InstanceID: req.Input.InstanceID, Region: c.region, Status: st.Status, CreatedAt: st.CreatedAt,
		ReadyAt: st.ReadyAt, ExpiresAt: st.ExpiresAt, DocumentedPurpose: md.GetDocumentedPurpose(),
		Shape: InstanceShape{
			VirtualCPU: int(md.GetShape().GetVirtualCpu()), MemoryMegabytes: int(md.GetShape().GetMemoryMegabytes()),
			MachineArch: emptyToNil(md.GetShape().GetMachineArch()), OS: emptyToNil(md.GetShape().GetOs()),
		},
		Labels: labels, InstanceURL: st.InstanceURL, IngressDomain: st.IngressDomain,
		Services: st.Services, ContainerCount: len(res.GetContainers()),
	}}, nil
}
