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
	"time"

	registryv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/registry/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// ---------------------------------------------------------------------------
// SharedImage resource
// ---------------------------------------------------------------------------

// SharedImage shares one registry image under a public or partner image reference.
type SharedImage struct{}

var sharedImageVisibilities = []string{"PUBLIC", "PARTNER"}

type SharedImageArgs struct {
	Repository string  `pulumi:"repository" provider:"replaceOnChanges"`
	Digest     string  `pulumi:"digest" provider:"replaceOnChanges"`
	Visibility string  `pulumi:"visibility" provider:"replaceOnChanges"`
	Suffix     *string `pulumi:"suffix,optional" provider:"replaceOnChanges"`
	ExpiresAt  *string `pulumi:"expiresAt,optional" provider:"replaceOnChanges"`
}

type SharedImageState struct {
	SharedImageArgs

	SharedImageID string `pulumi:"sharedImageId"`
	ImageRef      string `pulumi:"imageRef"`
}

func (r *SharedImage) Annotate(a infer.Annotator) {
	a.Describe(&r, "A container registry image shared under a public or partner image reference "+
		"(ShareImage / UnshareImage). The API has no update call, so every input replaces the share; "+
		"destroying unshares the image. Import with the shared image ID.")
}

func (a *SharedImageArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Repository, "The registry repository that contains the image. Changing it replaces the share.")
	ann.Describe(&a.Digest, "The digest of the image to share, such as 'sha256:...'. Changing it replaces the share.")
	ann.Describe(&a.Visibility, "Who can pull the shared image: PUBLIC or PARTNER. Changing it replaces the share.")
	ann.Describe(&a.Suffix, "Optional suffix that makes the image reference more distinct: public.nscr.io/<id>/<suffix>. "+
		"Changing it replaces the share.")
	ann.Describe(&a.ExpiresAt, "RFC3339 time at which the share expires. Unset shares the image indefinitely. "+
		"Changing it replaces the share.")
}

func (s *SharedImageState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.SharedImageID, "The unique identifier of the shared image.")
	ann.Describe(&s.ImageRef, "The image reference other tenants pull the shared image from.")
}

func (r *SharedImage) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[SharedImageArgs], error) {
	return checkWith(ctx, req, func(a SharedImageArgs) []pgp.CheckFailure {
		f := requireNonEmpty("repository", a.Repository)
		f = append(f, requireNonEmpty("digest", a.Digest)...)
		f = append(f, failIfNotIn("visibility", a.Visibility, sharedImageVisibilities...)...)
		if a.ExpiresAt != nil {
			if _, err := time.Parse(time.RFC3339, *a.ExpiresAt); err != nil {
				f = append(f, fail("expiresAt", "must be an RFC3339 timestamp such as 2026-12-31T23:59:59Z"))
			}
		}
		return f
	})
}

func sharedImageVisibility(name string) registryv1.Visibility {
	return registryv1.Visibility(registryv1.Visibility_value[name])
}

// applySharedImage records what the API reports. declared (nil on import) keeps
// the user's text for expiresAt while it denotes the same instant, so a
// formatting difference is not drift and, with replaceOnChanges, not a replace.
func applySharedImage(state *SharedImageState, s *registryv1.SharedImage, declared *SharedImageArgs) {
	state.SharedImageID = s.GetId()
	state.ImageRef = s.GetImageRef()
	state.Repository = s.GetSharedRepository()
	state.Digest = s.GetSharedDigest()
	state.Visibility = s.GetVisibility().String()
	state.Suffix = emptyToNil(s.GetSuffix())
	srv := emptyToNil(formatTimestamp(s.GetExpiresAt()))
	state.ExpiresAt = srv
	if declared != nil && declared.ExpiresAt != nil && srv != nil {
		d, err := time.Parse(time.RFC3339, *declared.ExpiresAt)
		if err == nil && d.Sub(s.GetExpiresAt().AsTime()).Abs() <= time.Second {
			state.ExpiresAt = declared.ExpiresAt
		}
	}
}

func listSharedImages(ctx context.Context, c *nsClient, req *registryv1.ListSharedImagesRequest) ([]*registryv1.SharedImage, error) {
	var res registryv1.ListSharedImagesResponse
	if err := c.call(ctx, c.endpoints.globalURL(), registryService, "ListSharedImages", req, &res); err != nil {
		return nil, err
	}
	return res.GetSharedImages(), nil
}

func findSharedImage(ctx context.Context, c *nsClient, id string) (*registryv1.SharedImage, error) {
	all, err := listSharedImages(ctx, c, &registryv1.ListSharedImagesRequest{})
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if s.GetId() == id {
			return s, nil
		}
	}
	return nil, nil
}

func (r *SharedImage) Create(
	ctx context.Context, req infer.CreateRequest[SharedImageArgs],
) (infer.CreateResponse[SharedImageState], error) {
	in := req.Inputs
	state := SharedImageState{SharedImageArgs: in}
	if req.DryRun {
		return infer.CreateResponse[SharedImageState]{ID: "", Output: state}, nil
	}
	body := &registryv1.ShareImageRequest{
		Repository: in.Repository, Digest: in.Digest, Visibility: sharedImageVisibility(in.Visibility), Suffix: derefStr(in.Suffix),
	}
	if in.ExpiresAt != nil {
		ts, err := parseRFC3339("expiresAt", *in.ExpiresAt)
		if err != nil {
			return infer.CreateResponse[SharedImageState]{}, err
		}
		body.ExpiresAt = ts
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[SharedImageState]{}, err
	}
	var res registryv1.SharedImage
	if err := c.call(ctx, c.endpoints.globalURL(), registryService, "ShareImage", body, &res); err != nil {
		return infer.CreateResponse[SharedImageState]{}, fmt.Errorf("share image %s@%s: %w", in.Repository, in.Digest, err)
	}
	if res.GetId() == "" {
		return infer.CreateResponse[SharedImageState]{}, fmt.Errorf("share image %s@%s: the API returned no shared image id", in.Repository, in.Digest)
	}
	applySharedImage(&state, &res, &in)
	return infer.CreateResponse[SharedImageState]{ID: state.SharedImageID, Output: state}, nil
}

func (r *SharedImage) Read(
	ctx context.Context, req infer.ReadRequest[SharedImageArgs, SharedImageState],
) (infer.ReadResponse[SharedImageArgs, SharedImageState], error) {
	type resp = infer.ReadResponse[SharedImageArgs, SharedImageState]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	s, err := findSharedImage(ctx, c, req.ID)
	if err != nil {
		return resp{}, fmt.Errorf("read shared image %q: %w", req.ID, err)
	}
	if s == nil {
		if err := notFoundOnImport("shared image", req.ID, req.Inputs, req.State); err != nil {
			return resp{}, err
		}
		return resp{}, nil
	}
	var state SharedImageState
	var declared *SharedImageArgs
	if req.Inputs.Repository != "" { // refresh; an import has no prior inputs
		d := req.Inputs
		declared = &d
	}
	applySharedImage(&state, s, declared)
	return resp{ID: req.ID, Inputs: state.SharedImageArgs, State: state}, nil
}

func (r *SharedImage) Delete(
	ctx context.Context, req infer.DeleteRequest[SharedImageState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.globalURL(), registryService, "UnshareImage", &registryv1.UnshareImageRequest{Id: req.ID}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("unshare image %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// ---------------------------------------------------------------------------
// getSharedImage / getSharedImages
// ---------------------------------------------------------------------------

// SharedImageInfo is a shared image as the API reports it.
type SharedImageInfo struct {
	SharedImageID string  `pulumi:"sharedImageId"`
	Repository    string  `pulumi:"repository"`
	Digest        string  `pulumi:"digest"`
	Visibility    string  `pulumi:"visibility"`
	Suffix        string  `pulumi:"suffix"`
	ImageRef      string  `pulumi:"imageRef"`
	ExpiresAt     *string `pulumi:"expiresAt,optional"`
}

func (s *SharedImageInfo) Annotate(ann infer.Annotator) {
	ann.Describe(&s.SharedImageID, "The shared image identifier.")
	ann.Describe(&s.Repository, "The repository that contains the shared image.")
	ann.Describe(&s.Digest, "The digest of the shared image.")
	ann.Describe(&s.Visibility, "PUBLIC or PARTNER.")
	ann.Describe(&s.Suffix, "The suffix given when the image was shared, if any.")
	ann.Describe(&s.ImageRef, "The image reference the shared image is pulled from.")
	ann.Describe(&s.ExpiresAt, "RFC3339 expiry. Unset for images shared indefinitely.")
}

func sharedImageInfo(s *registryv1.SharedImage) SharedImageInfo {
	return SharedImageInfo{
		SharedImageID: s.GetId(), Repository: s.GetSharedRepository(), Digest: s.GetSharedDigest(),
		Visibility: s.GetVisibility().String(), Suffix: s.GetSuffix(), ImageRef: s.GetImageRef(),
		ExpiresAt: emptyToNil(formatTimestamp(s.GetExpiresAt())),
	}
}

// GetSharedImage reads one shared image.
type GetSharedImage struct{}

type GetSharedImageArgs struct {
	SharedImageID string `pulumi:"sharedImageId"`
}

func (f *GetSharedImage) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one shared registry image by its shared image ID.")
}

func (a *GetSharedImageArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.SharedImageID, "The shared image ID.")
}

func (f *GetSharedImage) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetSharedImageArgs],
) (infer.FunctionResponse[SharedImageInfo], error) {
	type resp = infer.FunctionResponse[SharedImageInfo]
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	s, err := findSharedImage(ctx, c, req.Input.SharedImageID)
	if err != nil {
		return resp{}, fmt.Errorf("read shared image %q: %w", req.Input.SharedImageID, err)
	}
	if s == nil {
		return resp{}, errLookupNotFound("shared image", req.Input.SharedImageID)
	}
	return resp{Output: sharedImageInfo(s)}, nil
}

// GetSharedImages lists shared images.
type GetSharedImages struct{}

type GetSharedImagesArgs struct {
	// Repository restricts the result to images of this repository.
	Repository *string `pulumi:"repository,optional"`
	// Visibility restricts the result to PUBLIC or PARTNER shares.
	Visibility *string `pulumi:"visibility,optional"`
}

type GetSharedImagesResult struct {
	SharedImages []SharedImageInfo `pulumi:"sharedImages"`
}

func (f *GetSharedImages) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the shared registry images of the current tenant.")
}

func (a *GetSharedImagesArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Repository, "Only return images of this repository.")
	ann.Describe(&a.Visibility, "Only return PUBLIC or PARTNER shares.")
}

func (r *GetSharedImagesResult) Annotate(ann infer.Annotator) {
	ann.Describe(&r.SharedImages, "The shared images.")
}

func (f *GetSharedImages) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetSharedImagesArgs],
) (infer.FunctionResponse[GetSharedImagesResult], error) {
	type resp = infer.FunctionResponse[GetSharedImagesResult]
	if v := req.Input.Visibility; v != nil {
		if f := failIfNotIn("visibility", *v, sharedImageVisibilities...); len(f) > 0 {
			return resp{}, fmt.Errorf("namespace: getSharedImages: %s", f[0].Reason)
		}
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return resp{}, err
	}
	all, err := listSharedImages(ctx, c, &registryv1.ListSharedImagesRequest{
		MatchVisibility: sharedImageVisibility(derefStr(req.Input.Visibility)),
	})
	if err != nil {
		return resp{}, fmt.Errorf("list shared images: %w", err)
	}
	out := GetSharedImagesResult{SharedImages: []SharedImageInfo{}}
	for _, s := range all {
		if repo := derefStr(req.Input.Repository); repo != "" && s.GetSharedRepository() != repo {
			continue
		}
		out.SharedImages = append(out.SharedImages, sharedImageInfo(s))
	}
	return resp{Output: out}, nil
}
