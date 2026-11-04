package provider

import (
	"context"
	"fmt"

	imagev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/image/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// Blueprint resource
// ---------------------------------------------------------------------------

// Blueprint is a named, buildable base image definition.
type Blueprint struct{}

type BlueprintArgs struct {
	Name string `pulumi:"name" provider:"replaceOnChanges"`
	// DockerfileContent builds the image from an inline Dockerfile.
	DockerfileContent *string `pulumi:"dockerfileContent,optional"`
	// AptBaseImage is the base image reference of an apt-based blueprint.
	AptBaseImage *string `pulumi:"aptBaseImage,optional"`
	// AptBaseBlueprint is the name of another blueprint used as the base.
	AptBaseBlueprint *string  `pulumi:"aptBaseBlueprint,optional"`
	AptPackages      []string `pulumi:"aptPackages,optional"`
}

type BlueprintState struct {
	BlueprintArgs

	BlueprintID string `pulumi:"blueprintId"`
	CreatedAt   string `pulumi:"createdAt"`
	ImageRef    string `pulumi:"imageRef"`
	BuildError  string `pulumi:"buildError"`
}

func (r *Blueprint) Annotate(a infer.Annotator) {
	a.Describe(&r, "A Namespace image blueprint: a named base image defined either by an inline Dockerfile or by a "+
		"base image plus apt packages. Changing the spec updates the blueprint in place (CreateBlueprint upserts by "+
		"name, so the blueprint never disappears while it is rebuilt); changing the name replaces it. "+
		"Import with the blueprint name.")
}

func (a *BlueprintArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Name, "Blueprint name. Changing it replaces the blueprint.")
	ann.Describe(&a.DockerfileContent, "Inline Dockerfile. Mutually exclusive with the apt* inputs.")
	ann.Describe(&a.AptBaseImage, "Base image reference for an apt-based blueprint. Mutually exclusive with aptBaseBlueprint.")
	ann.Describe(&a.AptBaseBlueprint, "Name of another blueprint to use as the base of an apt-based blueprint.")
	ann.Describe(&a.AptPackages, "Apt packages installed on top of the base.")
}

func (s *BlueprintState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.BlueprintID, "Server-assigned blueprint ID.")
	ann.Describe(&s.CreatedAt, "RFC3339 creation time.")
	ann.Describe(&s.ImageRef, "Reference of the built image, once available.")
	ann.Describe(&s.BuildError, "Last build error, if any.")
}

// Diff updates spec changes in place and replaces the blueprint when its name
// changes (per the replaceOnChanges tag), deleting the old one first. A
// blueprint is identified by its name (the resource ID), and CreateBlueprint is
// keyed by that name, so under the default create-before-delete order a forced
// replace would upsert the blueprint and the follow-up delete of the "old"
// resource, which has the same name, would remove the one just built.
func (r *Blueprint) Diff(
	_ context.Context, req infer.DiffRequest[BlueprintArgs, BlueprintState],
) (infer.DiffResponse, error) {
	return diffDeleteFirst(req.State.BlueprintArgs, req.Inputs), nil
}

func (r *Blueprint) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[BlueprintArgs], error) {
	return checkWith(ctx, req, func(a BlueprintArgs) []pgp.CheckFailure {
		f := requireNonEmpty("name", a.Name)
		apt := a.AptBaseImage != nil || a.AptBaseBlueprint != nil || len(a.AptPackages) > 0
		switch {
		case a.DockerfileContent != nil && apt:
			f = append(f, fail("dockerfileContent", "cannot be combined with aptBaseImage, aptBaseBlueprint or aptPackages"))
		case a.DockerfileContent == nil && !apt:
			f = append(f, fail("dockerfileContent", "set dockerfileContent, or aptBaseImage/aptBaseBlueprint with aptPackages"))
		case a.DockerfileContent != nil && *a.DockerfileContent == "":
			f = append(f, fail("dockerfileContent", "must not be empty"))
		case apt && (a.AptBaseImage == nil) == (a.AptBaseBlueprint == nil):
			f = append(f, fail("aptBaseImage", "set exactly one of aptBaseImage and aptBaseBlueprint"))
		}
		return f
	})
}

func blueprintSpecBody(in BlueprintArgs) map[string]any {
	if in.DockerfileContent != nil {
		return map[string]any{"dockerfile": map[string]any{"content": *in.DockerfileContent}}
	}
	base := map[string]any{}
	if in.AptBaseImage != nil {
		base["imageRef"] = *in.AptBaseImage
	} else if in.AptBaseBlueprint != nil {
		base["blueprintRef"] = map[string]any{"name": *in.AptBaseBlueprint}
	}
	return map[string]any{"aptBasedImage": map[string]any{"baseImage": base, "aptPackages": in.AptPackages}}
}

func applyBlueprint(state *BlueprintState, b *imagev1.ImageBlueprint) {
	state.BlueprintID = b.GetId()
	state.CreatedAt = formatTimestamp(b.GetCreatedAt())
	state.ImageRef = b.GetStatus().GetImageRef()
	state.BuildError = b.GetStatus().GetError()
}

// upsertBlueprint calls CreateBlueprint, which creates or overwrites the
// blueprint with this name.
func upsertBlueprint(ctx context.Context, c *nsClient, in BlueprintArgs) (*imagev1.ImageBlueprint, error) {
	var pb imagev1.CreateBlueprintRequest
	if err := toProto(map[string]any{"blueprintName": in.Name, "spec": blueprintSpecBody(in)}, &pb); err != nil {
		return nil, err
	}
	var res imagev1.CreateBlueprintResponse
	if err := c.call(ctx, c.endpoints.globalURL(), imageService, "CreateBlueprint", &pb, &res); err != nil {
		return nil, err
	}
	return res.GetBlueprint(), nil
}

func (r *Blueprint) Create(
	ctx context.Context, req infer.CreateRequest[BlueprintArgs],
) (infer.CreateResponse[BlueprintState], error) {
	state := BlueprintState{BlueprintArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[BlueprintState]{ID: req.Inputs.Name, Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[BlueprintState]{}, err
	}
	b, err := upsertBlueprint(ctx, c, req.Inputs)
	if err != nil {
		return infer.CreateResponse[BlueprintState]{}, fmt.Errorf("create blueprint %q: %w", req.Inputs.Name, err)
	}
	applyBlueprint(&state, b)
	return infer.CreateResponse[BlueprintState]{ID: req.Inputs.Name, Output: state}, nil
}

func (r *Blueprint) Update(
	ctx context.Context, req infer.UpdateRequest[BlueprintArgs, BlueprintState],
) (infer.UpdateResponse[BlueprintState], error) {
	state := req.State
	state.BlueprintArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[BlueprintState]{Output: state}, nil
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[BlueprintState]{}, err
	}
	b, err := upsertBlueprint(ctx, c, req.Inputs)
	if err != nil {
		return infer.UpdateResponse[BlueprintState]{}, fmt.Errorf("update blueprint %q: %w", req.ID, err)
	}
	applyBlueprint(&state, b)
	return infer.UpdateResponse[BlueprintState]{Output: state}, nil
}

func fetchBlueprint(ctx context.Context, c *nsClient, name string) (*imagev1.ImageBlueprint, error) {
	var res imagev1.FetchBlueprintResponse
	if err := c.call(ctx, c.endpoints.globalURL(), imageService, "FetchBlueprint",
		&imagev1.FetchBlueprintRequest{BlueprintName: name}, &res); err != nil {
		return nil, err
	}
	return res.GetBlueprint(), nil
}

func (r *Blueprint) Read(
	ctx context.Context, req infer.ReadRequest[BlueprintArgs, BlueprintState],
) (infer.ReadResponse[BlueprintArgs, BlueprintState], error) {
	type resp = infer.ReadResponse[BlueprintArgs, BlueprintState]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	b, err := fetchBlueprint(ctx, c, req.ID)
	if isNotFound(err) || (err == nil && b == nil) {
		return resp{}, nil
	}
	if err != nil {
		return resp{}, fmt.Errorf("read blueprint %q: %w", req.ID, err)
	}
	state := req.State
	state.Name = req.ID
	if spec := b.GetSpec(); spec != nil {
		// Refresh the whole spec from the server, so drift shows and import
		// derives the inputs.
		state.DockerfileContent, state.AptBaseImage, state.AptBaseBlueprint, state.AptPackages = nil, nil, nil, nil
		if d := spec.GetDockerfile(); d != nil {
			state.DockerfileContent = ptr(d.GetContent())
		}
		if a := spec.GetAptBasedImage(); a != nil {
			if pk := a.GetAptPackages(); len(pk) > 0 {
				state.AptPackages = pk
			}
			if v := a.GetBaseImage().GetImageRef(); v != "" {
				state.AptBaseImage = ptr(v)
			}
			if v := a.GetBaseImage().GetBlueprintRef().GetName(); v != "" {
				state.AptBaseBlueprint = ptr(v)
			}
		}
	}
	applyBlueprint(&state, b)
	return resp{ID: req.ID, Inputs: state.BlueprintArgs, State: state}, nil
}

func (r *Blueprint) Delete(
	ctx context.Context, req infer.DeleteRequest[BlueprintState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.globalURL(), imageService, "RemoveBlueprint",
		&imagev1.RemoveBlueprintRequest{BlueprintName: req.ID}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("remove blueprint %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// ---- getBlueprints ----

type GetBlueprints struct{}
type GetBlueprintsArgs struct{}

type BlueprintSummary struct {
	Name        string `pulumi:"name"`
	BlueprintID string `pulumi:"blueprintId"`
	CreatedAt   string `pulumi:"createdAt"`
	ImageRef    string `pulumi:"imageRef"`
}

type GetBlueprintsResult struct {
	Blueprints []BlueprintSummary `pulumi:"blueprints"`
}

func (f *GetBlueprints) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the image blueprints of the current tenant.")
}
func (s *BlueprintSummary) Annotate(a infer.Annotator) {
	a.Describe(&s.Name, "Blueprint name.")
	a.Describe(&s.BlueprintID, "Blueprint ID.")
	a.Describe(&s.CreatedAt, "RFC3339 creation time.")
	a.Describe(&s.ImageRef, "Built image reference, if built.")
}
func (r *GetBlueprintsResult) Annotate(a infer.Annotator) {
	a.Describe(&r.Blueprints, "The blueprints.")
}

func (f *GetBlueprints) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetBlueprintsArgs],
) (infer.FunctionResponse[GetBlueprintsResult], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetBlueprintsResult]{}, err
	}
	out := GetBlueprintsResult{Blueprints: []BlueprintSummary{}}
	err = paginate("ListBlueprints", func(cursor []byte) ([]byte, error) {
		var res imagev1.ListBlueprintsResponse
		if err := c.call(ctx, c.endpoints.globalURL(), imageService, "ListBlueprints",
			&imagev1.ListBlueprintsRequest{PaginationCursor: cursor}, &res); err != nil {
			return nil, err
		}
		for _, b := range res.GetBlueprints() {
			out.Blueprints = append(out.Blueprints, BlueprintSummary{Name: b.GetName(), BlueprintID: b.GetId(),
				CreatedAt: formatTimestamp(b.GetCreatedAt()), ImageRef: b.GetStatus().GetImageRef()})
		}
		return res.GetPaginationCursor(), nil
	})
	if err != nil {
		return infer.FunctionResponse[GetBlueprintsResult]{}, fmt.Errorf("list blueprints: %w", err)
	}
	return infer.FunctionResponse[GetBlueprintsResult]{Output: out}, nil
}
