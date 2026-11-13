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
	"strconv"

	githubv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/github/v1beta"
	registryv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/registry/v1beta"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// Single-object lookups next to the list data sources in data_sources.go:
// ContainerRegistryService.GetImage and github JobsService.DescribeJob. Neither
// object is managed by a resource, so there is no import ID to share.

// ---- getRegistryImage ----

type GetRegistryImage struct{}

type GetRegistryImageArgs struct {
	Repository string `pulumi:"repository"`
	Reference  string `pulumi:"reference"`
}

type GetRegistryImageResult struct {
	Repository string   `pulumi:"repository"`
	Reference  string   `pulumi:"reference"`
	Digest     string   `pulumi:"digest"`
	Tags       []string `pulumi:"tags"`
	CreatedAt  string   `pulumi:"createdAt"`
	ExpiresAt  string   `pulumi:"expiresAt"`
	TotalBytes string   `pulumi:"totalBytes"`
}

func (f *GetRegistryImage) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one image of the Namespace container registry by tag or digest. "+
		"Fails with \"was not found\" when the image does not exist.")
}
func (a *GetRegistryImageArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Repository, "Repository containing the image.")
	ann.Describe(&a.Reference, "Tag, or digest of the form sha256:<hash>.")
}
func (r *GetRegistryImageResult) Annotate(a infer.Annotator) {
	a.Describe(&r.Repository, "Repository name.")
	a.Describe(&r.Reference, "The tag or digest that was looked up.")
	a.Describe(&r.Digest, "Image digest.")
	a.Describe(&r.Tags, "Tags pointing at the image.")
	a.Describe(&r.CreatedAt, "RFC3339 creation time.")
	a.Describe(&r.ExpiresAt, "RFC3339 expiry time, empty if none.")
	a.Describe(&r.TotalBytes, "Total size in bytes, as a decimal string (int64).")
}

func (f *GetRegistryImage) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetRegistryImageArgs],
) (infer.FunctionResponse[GetRegistryImageResult], error) {
	in := req.Input
	if in.Repository == "" || in.Reference == "" {
		return infer.FunctionResponse[GetRegistryImageResult]{}, fmt.Errorf("namespace: getRegistryImage needs repository and reference")
	}
	key := in.Repository + ":" + in.Reference
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetRegistryImageResult]{}, err
	}
	rq := registryv1.GetImageRequest{Repository: in.Repository, Reference: in.Reference}
	var res registryv1.GetImageResponse
	if err := c.call(ctx, c.endpoints.globalURL(), registryService, "GetImage", &rq, &res); err != nil {
		if isNotFound(err) {
			return infer.FunctionResponse[GetRegistryImageResult]{}, errLookupNotFound("registry image", key)
		}
		return infer.FunctionResponse[GetRegistryImageResult]{}, fmt.Errorf("get registry image: %w", err)
	}
	im := res.GetImage()
	if im == nil {
		return infer.FunctionResponse[GetRegistryImageResult]{}, errLookupNotFound("registry image", key)
	}
	return infer.FunctionResponse[GetRegistryImageResult]{Output: GetRegistryImageResult{
		Repository: im.GetRepository(), Reference: in.Reference, Digest: im.GetDigest(),
		Tags: append([]string{}, im.GetTags()...), CreatedAt: formatTimestamp(im.GetCreatedAt()),
		ExpiresAt: formatTimestamp(im.GetExpiresAt()), TotalBytes: strconv.FormatInt(im.GetSizes().GetTotal(), 10),
	}}, nil
}

// ---- getGithubJob ----

type GetGithubJob struct{}

type GetGithubJobArgs struct {
	JobID string `pulumi:"jobId"`
}

type GetGithubJobResult struct {
	JobID        string `pulumi:"jobId"`
	RunID        string `pulumi:"runId"`
	Repository   string `pulumi:"repository"`
	WorkflowName string `pulumi:"workflowName"`
	JobName      string `pulumi:"jobName"`
	Conclusion   string `pulumi:"conclusion"`
	Ref          string `pulumi:"ref"`
	SenderLogin  string `pulumi:"senderLogin"`
	CreatedAt    string `pulumi:"createdAt"`
	CompletedAt  string `pulumi:"completedAt"`
}

func (f *GetGithubJob) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one GitHub Actions job run on a Namespace runner by its GitHub job ID. "+
		"Fails with \"was not found\" when Namespace has no such job. The runner and compute instance "+
		"details the API also returns are not exposed.")
}
func (a *GetGithubJobArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.JobID, "GitHub job ID, the number in the job URL, as a decimal string.")
}
func (r *GetGithubJobResult) Annotate(a infer.Annotator) {
	a.Describe(&r.JobID, "GitHub job ID.")
	a.Describe(&r.RunID, "GitHub workflow run ID.")
	a.Describe(&r.Repository, "Repository.")
	a.Describe(&r.WorkflowName, "Workflow name.")
	a.Describe(&r.JobName, "Job name.")
	a.Describe(&r.Conclusion, "Job conclusion, empty while running.")
	a.Describe(&r.Ref, "Git ref.")
	a.Describe(&r.SenderLogin, "User that triggered the run.")
	a.Describe(&r.CreatedAt, "RFC3339 creation time.")
	a.Describe(&r.CompletedAt, "RFC3339 completion time.")
}

func (f *GetGithubJob) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetGithubJobArgs],
) (infer.FunctionResponse[GetGithubJobResult], error) {
	id, err := strconv.ParseInt(req.Input.JobID, 10, 64)
	if err != nil || id <= 0 {
		return infer.FunctionResponse[GetGithubJobResult]{}, fmt.Errorf("namespace: jobId %q must be a positive decimal number", req.Input.JobID)
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetGithubJobResult]{}, err
	}
	rq := githubv1.DescribeJobRequest{JobId: id}
	var res githubv1.DescribeJobResponse
	if err := c.call(ctx, c.endpoints.githubURL(), githubJobsService, "DescribeJob", &rq, &res); err != nil {
		if isNotFound(err) {
			return infer.FunctionResponse[GetGithubJobResult]{}, errLookupNotFound("github job", req.Input.JobID)
		}
		return infer.FunctionResponse[GetGithubJobResult]{}, fmt.Errorf("describe github job: %w", err)
	}
	j := res.GetJob()
	if j == nil {
		return infer.FunctionResponse[GetGithubJobResult]{}, errLookupNotFound("github job", req.Input.JobID)
	}
	return infer.FunctionResponse[GetGithubJobResult]{Output: GetGithubJobResult{
		JobID: strconv.FormatInt(j.GetJobId(), 10), RunID: strconv.FormatInt(j.GetRunId(), 10),
		Repository: j.GetRepository(), WorkflowName: j.GetWorkflowName(), JobName: j.GetJobName(),
		Conclusion: j.GetConclusion(), Ref: j.GetRef(), SenderLogin: j.GetSenderLogin(),
		CreatedAt: formatTimestamp(j.GetCreatedAt()), CompletedAt: formatTimestamp(j.GetCompletedAt()),
	}}, nil
}
