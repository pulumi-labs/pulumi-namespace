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
	"time"

	computev1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/compute/v1beta"
	githubv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/github/v1beta"
	registryv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/registry/v1beta"
	"github.com/pulumi/pulumi-go-provider/infer"
)

// Read-only data sources over the registry, usage, storage and GitHub job APIs.
//
// Not modelled: builder ListBuilds is a server-streaming RPC, which does not
// fit the unary Connect JSON transport used here.

// ---- getRegistryRepositories ----

type GetRegistryRepositories struct{}
type GetRegistryRepositoriesArgs struct{}

type RegistryRepositorySummary struct {
	Name     string `pulumi:"name"`
	LastPush string `pulumi:"lastPush"`
}

type GetRegistryRepositoriesResult struct {
	Repositories []RegistryRepositorySummary `pulumi:"repositories"`
}

func (f *GetRegistryRepositories) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the repositories of the Namespace container registry.")
}
func (s *RegistryRepositorySummary) Annotate(a infer.Annotator) {
	a.Describe(&s.Name, "Repository name.")
	a.Describe(&s.LastPush, "RFC3339 time of the last push, empty if never pushed.")
}
func (r *GetRegistryRepositoriesResult) Annotate(a infer.Annotator) {
	a.Describe(&r.Repositories, "The repositories.")
}

func (f *GetRegistryRepositories) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetRegistryRepositoriesArgs],
) (infer.FunctionResponse[GetRegistryRepositoriesResult], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetRegistryRepositoriesResult]{}, err
	}
	out := GetRegistryRepositoriesResult{Repositories: []RegistryRepositorySummary{}}
	err = paginate("ListRepositories", func(cursor []byte) ([]byte, error) {
		var res registryv1.ListRepositoriesResponse
		if err := c.call(ctx, c.endpoints.globalURL(), registryService, "ListRepositories",
			&registryv1.ListRepositoriesRequest{PaginationCursor: cursor, MaxEntries: 100}, &res); err != nil {
			return nil, err
		}
		for _, r := range res.GetRepositories() {
			out.Repositories = append(out.Repositories, RegistryRepositorySummary{Name: r.GetName(), LastPush: formatTimestamp(r.GetLastPush())})
		}
		return res.GetPaginationCursor(), nil
	})
	if err != nil {
		return infer.FunctionResponse[GetRegistryRepositoriesResult]{}, fmt.Errorf("list registry repositories: %w", err)
	}
	return infer.FunctionResponse[GetRegistryRepositoriesResult]{Output: out}, nil
}

// ---- getRegistryImages ----

type GetRegistryImages struct{}

type GetRegistryImagesArgs struct {
	Repository *string `pulumi:"repository,optional"`
}

type RegistryImageSummary struct {
	Repository string   `pulumi:"repository"`
	Digest     string   `pulumi:"digest"`
	Tags       []string `pulumi:"tags"`
	CreatedAt  string   `pulumi:"createdAt"`
	ExpiresAt  string   `pulumi:"expiresAt"`
	TotalBytes string   `pulumi:"totalBytes"`
}

type GetRegistryImagesResult struct {
	Images []RegistryImageSummary `pulumi:"images"`
}

func (f *GetRegistryImages) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists images in the Namespace container registry, optionally limited to one repository. "+
		"Deleted images are excluded.")
}
func (a *GetRegistryImagesArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Repository, "Only return images of this repository.")
}
func (s *RegistryImageSummary) Annotate(a infer.Annotator) {
	a.Describe(&s.Repository, "Repository name.")
	a.Describe(&s.Digest, "Image digest.")
	a.Describe(&s.Tags, "Tags pointing at the image.")
	a.Describe(&s.CreatedAt, "RFC3339 creation time.")
	a.Describe(&s.ExpiresAt, "RFC3339 expiry time, empty if none.")
	a.Describe(&s.TotalBytes, "Total size in bytes, as a decimal string (int64).")
}
func (r *GetRegistryImagesResult) Annotate(a infer.Annotator) { a.Describe(&r.Images, "The images.") }

func (f *GetRegistryImages) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetRegistryImagesArgs],
) (infer.FunctionResponse[GetRegistryImagesResult], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetRegistryImagesResult]{}, err
	}
	out := GetRegistryImagesResult{Images: []RegistryImageSummary{}}
	err = paginate("ListImages", func(cursor []byte) ([]byte, error) {
		body := map[string]any{"deletionFilter": "EXCLUDE_DELETED", "maxEntries": "100"}
		if len(cursor) > 0 {
			body["paginationCursor"] = cursor
		}
		if req.Input.Repository != nil && *req.Input.Repository != "" {
			body["matchRepository"] = map[string]any{"values": []string{*req.Input.Repository}, "op": "IS_ANY_OF"}
		}
		var rq registryv1.ListImagesRequest
		if err := toProto(body, &rq); err != nil {
			return nil, err
		}
		var res registryv1.ListImagesResponse
		if err := c.call(ctx, c.endpoints.globalURL(), registryService, "ListImages", &rq, &res); err != nil {
			return nil, err
		}
		for _, im := range res.GetImages() {
			out.Images = append(out.Images, RegistryImageSummary{
				Repository: im.GetRepository(), Digest: im.GetDigest(), Tags: append([]string{}, im.GetTags()...),
				CreatedAt: formatTimestamp(im.GetCreatedAt()), ExpiresAt: formatTimestamp(im.GetExpiresAt()),
				TotalBytes: strconv.FormatInt(im.GetSizes().GetTotal(), 10),
			})
		}
		return res.GetPaginationCursor(), nil
	})
	if err != nil {
		return infer.FunctionResponse[GetRegistryImagesResult]{}, fmt.Errorf("list registry images: %w", err)
	}
	return infer.FunctionResponse[GetRegistryImagesResult]{Output: out}, nil
}

// ---- getUsage ----

type GetUsage struct{}

type GetUsageArgs struct {
	// PeriodStart and PeriodEnd are calendar dates (YYYY-MM-DD).
	PeriodStart string  `pulumi:"periodStart"`
	PeriodEnd   string  `pulumi:"periodEnd"`
	Region      *string `pulumi:"region,optional"`
}

type UsageDay struct {
	Date                string `pulumi:"date"`
	InstanceUnitMinutes string `pulumi:"instanceUnitMinutes"`
	InstanceWallMinutes string `pulumi:"instanceWallMinutes"`
	BuildCount          string `pulumi:"buildCount"`
}

type GetUsageResult struct {
	InstanceUnitMinutes string     `pulumi:"instanceUnitMinutes"`
	InstanceWallMinutes string     `pulumi:"instanceWallMinutes"`
	BuildCount          string     `pulumi:"buildCount"`
	BuildUnitMinutes    string     `pulumi:"buildUnitMinutes"`
	BuildWallMinutes    string     `pulumi:"buildWallMinutes"`
	PerDay              []UsageDay `pulumi:"perDay"`
}

func (f *GetUsage) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads compute usage totals and a per-day breakdown for a date range. Counters are int64 values "+
		"returned as decimal strings.")
}
func (a *GetUsageArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.PeriodStart, "First day of the range, YYYY-MM-DD.")
	ann.Describe(&a.PeriodEnd, "Last day of the range, YYYY-MM-DD.")
	ann.Describe(&a.Region, "Compute region to query. Defaults to the provider region.")
}
func (d *UsageDay) Annotate(a infer.Annotator) {
	a.Describe(&d.Date, "Day, YYYY-MM-DD.")
	a.Describe(&d.InstanceUnitMinutes, "Instance unit-minutes (shape-weighted).")
	a.Describe(&d.InstanceWallMinutes, "Instance wall-clock minutes.")
	a.Describe(&d.BuildCount, "Number of builds.")
}
func (r *GetUsageResult) Annotate(a infer.Annotator) {
	a.Describe(&r.InstanceUnitMinutes, "Total instance unit-minutes.")
	a.Describe(&r.InstanceWallMinutes, "Total instance wall-clock minutes.")
	a.Describe(&r.BuildCount, "Total number of builds.")
	a.Describe(&r.BuildUnitMinutes, "Total build unit-minutes.")
	a.Describe(&r.BuildWallMinutes, "Total build wall-clock minutes.")
	a.Describe(&r.PerDay, "Per-day usage.")
}

func dateBody(field, v string) (map[string]any, error) {
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, fmt.Errorf("%s must be a YYYY-MM-DD date: %w", field, err)
	}
	return map[string]any{"year": t.Year(), "month": int(t.Month()), "day": t.Day()}, nil
}

func (f *GetUsage) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetUsageArgs],
) (infer.FunctionResponse[GetUsageResult], error) {
	start, err := dateBody("periodStart", req.Input.PeriodStart)
	if err != nil {
		return infer.FunctionResponse[GetUsageResult]{}, err
	}
	end, err := dateBody("periodEnd", req.Input.PeriodEnd)
	if err != nil {
		return infer.FunctionResponse[GetUsageResult]{}, err
	}
	var rq computev1.GetUsageRequest
	if err := toProto(map[string]any{"periodStart": start, "periodEnd": end}, &rq); err != nil {
		return infer.FunctionResponse[GetUsageResult]{}, err
	}
	c, err := clientFor(ctx, req.Input.Region)
	if err != nil {
		return infer.FunctionResponse[GetUsageResult]{}, err
	}
	var res computev1.GetUsageResponse
	if err := c.call(ctx, c.endpoints.computeURL(c.region), usageService, "GetUsage", &rq, &res); err != nil {
		return infer.FunctionResponse[GetUsageResult]{}, fmt.Errorf("get usage: %w", err)
	}
	i64 := func(v int64) string { return strconv.FormatInt(v, 10) }
	t := res.GetTotal()
	out := GetUsageResult{
		InstanceUnitMinutes: i64(t.GetInstanceMinutes().GetUnit()), InstanceWallMinutes: i64(t.GetInstanceMinutes().GetWall()),
		BuildCount: i64(t.GetBuilds().GetCount()), BuildUnitMinutes: i64(t.GetBuilds().GetUnitMinutes()),
		BuildWallMinutes: i64(t.GetBuilds().GetWallMinutes()), PerDay: []UsageDay{},
	}
	for _, d := range res.GetPerDay() {
		u := d.GetTotal()
		out.PerDay = append(out.PerDay, UsageDay{
			Date:                fmt.Sprintf("%04d-%02d-%02d", d.GetYear(), d.GetMonth(), d.GetDay()),
			InstanceUnitMinutes: i64(u.GetInstanceMinutes().GetUnit()), InstanceWallMinutes: i64(u.GetInstanceMinutes().GetWall()),
			BuildCount: i64(u.GetBuilds().GetCount()),
		})
	}
	return infer.FunctionResponse[GetUsageResult]{Output: out}, nil
}

// ---- getVolumes ----

type GetVolumes struct{}

type GetVolumesArgs struct {
	Region *string `pulumi:"region,optional"`
}

type CacheVolumeSummary struct {
	ID         string `pulumi:"id"`
	Tag        string `pulumi:"tag"`
	SizeMb     int    `pulumi:"sizeMb"`
	State      string `pulumi:"state"`
	AttachedTo string `pulumi:"attachedTo"`
	CreatedAt  string `pulumi:"createdAt"`
}

type PersistentVolumeSummary struct {
	ID         string `pulumi:"id"`
	Tag        string `pulumi:"tag"`
	SizeMb     int    `pulumi:"sizeMb"`
	AttachedTo string `pulumi:"attachedTo"`
	Site       string `pulumi:"site"`
	CreatedAt  string `pulumi:"createdAt"`
}

type GetVolumesResult struct {
	CacheVolumes      []CacheVolumeSummary      `pulumi:"cacheVolumes"`
	PersistentVolumes []PersistentVolumeSummary `pulumi:"persistentVolumes"`
}

func (f *GetVolumes) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists cache volumes and persistent volumes in a compute region.")
}
func (a *GetVolumesArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Region, "Compute region to query. Defaults to the provider region.")
}
func (s *CacheVolumeSummary) Annotate(a infer.Annotator) {
	a.Describe(&s.ID, "Volume ID.")
	a.Describe(&s.Tag, "Cache tag.")
	a.Describe(&s.SizeMb, "Size in MB.")
	a.Describe(&s.State, "Volume state, for example STATE_IN_USE.")
	a.Describe(&s.AttachedTo, "Instance the volume is attached to, if any.")
	a.Describe(&s.CreatedAt, "RFC3339 creation time.")
}
func (s *PersistentVolumeSummary) Annotate(a infer.Annotator) {
	a.Describe(&s.ID, "Volume ID.")
	a.Describe(&s.Tag, "Volume tag.")
	a.Describe(&s.SizeMb, "Size in MB.")
	a.Describe(&s.AttachedTo, "Instance the volume is attached to, if any.")
	a.Describe(&s.Site, "Site holding the volume.")
	a.Describe(&s.CreatedAt, "RFC3339 creation time.")
}
func (r *GetVolumesResult) Annotate(a infer.Annotator) {
	a.Describe(&r.CacheVolumes, "Cache volumes.")
	a.Describe(&r.PersistentVolumes, "Persistent volumes.")
}

func (f *GetVolumes) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetVolumesArgs],
) (infer.FunctionResponse[GetVolumesResult], error) {
	c, err := clientFor(ctx, req.Input.Region)
	if err != nil {
		return infer.FunctionResponse[GetVolumesResult]{}, err
	}
	base := c.endpoints.computeURL(c.region)
	out := GetVolumesResult{CacheVolumes: []CacheVolumeSummary{}, PersistentVolumes: []PersistentVolumeSummary{}}
	var cres computev1.ListCacheVolumesResponse
	if err := c.call(ctx, base, storageService, "ListCacheVolumes", &computev1.ListCacheVolumesRequest{}, &cres); err != nil {
		return infer.FunctionResponse[GetVolumesResult]{}, fmt.Errorf("list cache volumes: %w", err)
	}
	for _, v := range cres.GetCacheVolume() {
		out.CacheVolumes = append(out.CacheVolumes, CacheVolumeSummary{
			ID: v.GetId(), Tag: v.GetTag(), SizeMb: int(v.GetMetadata().GetSizeMb()), State: v.GetMetadata().GetState().String(),
			AttachedTo: v.GetAttachment().GetAttachedTo(), CreatedAt: formatTimestamp(v.GetMetadata().GetCreatedAt()),
		})
	}
	err = paginate("ListPersistentVolumes", func(cursor []byte) ([]byte, error) {
		var res computev1.ListPersistentVolumesResponse
		if err := c.call(ctx, base, storageService, "ListPersistentVolumes",
			&computev1.ListPersistentVolumesRequest{PaginationCursor: cursor, MaxEntries: 100}, &res); err != nil {
			return nil, err
		}
		for _, v := range res.GetVolumes() {
			if v.GetDestroyedAt() != nil {
				continue
			}
			out.PersistentVolumes = append(out.PersistentVolumes, PersistentVolumeSummary{
				ID: v.GetId(), Tag: v.GetTag(), SizeMb: int(v.GetSizeMb()), AttachedTo: v.GetAttachedTo(),
				Site: v.GetSite(), CreatedAt: formatTimestamp(v.GetCreatedAt()),
			})
		}
		return res.GetPaginationCursor(), nil
	})
	if err != nil {
		return infer.FunctionResponse[GetVolumesResult]{}, fmt.Errorf("list persistent volumes: %w", err)
	}
	return infer.FunctionResponse[GetVolumesResult]{Output: out}, nil
}

// ---- getGithubJobs ----

type GetGithubJobs struct{}

type GetGithubJobsArgs struct {
	Repository *string `pulumi:"repository,optional"`
	Conclusion *string `pulumi:"conclusion,optional"`
	After      *string `pulumi:"after,optional"`
	Before     *string `pulumi:"before,optional"`
	// MaxJobs caps the number of returned jobs (default 500).
	MaxJobs *int `pulumi:"maxJobs,optional"`
}

type GithubJobSummary struct {
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

type GetGithubJobsResult struct {
	Jobs []GithubJobSummary `pulumi:"jobs"`
}

func (f *GetGithubJobs) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists GitHub Actions jobs run on Namespace runners, optionally filtered. Job and run IDs are "+
		"int64 values returned as decimal strings.")
}
func (a *GetGithubJobsArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Repository, "Only jobs of this repository (owner/name).")
	ann.Describe(&a.Conclusion, "Only jobs with this conclusion, for example success or failure.")
	ann.Describe(&a.After, "Only jobs after this RFC3339 time.")
	ann.Describe(&a.Before, "Only jobs before this RFC3339 time.")
	ann.Describe(&a.MaxJobs, "Maximum jobs to return. Defaults to 500.")
}
func (s *GithubJobSummary) Annotate(a infer.Annotator) {
	a.Describe(&s.JobID, "GitHub job ID.")
	a.Describe(&s.RunID, "GitHub workflow run ID.")
	a.Describe(&s.Repository, "Repository.")
	a.Describe(&s.WorkflowName, "Workflow name.")
	a.Describe(&s.JobName, "Job name.")
	a.Describe(&s.Conclusion, "Job conclusion, empty while running.")
	a.Describe(&s.Ref, "Git ref.")
	a.Describe(&s.SenderLogin, "User that triggered the run.")
	a.Describe(&s.CreatedAt, "RFC3339 creation time.")
	a.Describe(&s.CompletedAt, "RFC3339 completion time.")
}
func (r *GetGithubJobsResult) Annotate(a infer.Annotator) { a.Describe(&r.Jobs, "The jobs.") }

func (f *GetGithubJobs) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetGithubJobsArgs],
) (infer.FunctionResponse[GetGithubJobsResult], error) {
	in := req.Input
	limit := 500
	if in.MaxJobs != nil {
		if *in.MaxJobs <= 0 {
			return infer.FunctionResponse[GetGithubJobsResult]{}, fmt.Errorf("maxJobs must be greater than 0")
		}
		limit = *in.MaxJobs
	}
	tr := map[string]any{}
	for k, v := range map[string]*string{"after": in.After, "before": in.Before} {
		if v != nil && *v != "" {
			if _, err := parseRFC3339(k, *v); err != nil {
				return infer.FunctionResponse[GetGithubJobsResult]{}, err
			}
			tr[k] = *v
		}
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetGithubJobsResult]{}, err
	}
	out := GetGithubJobsResult{Jobs: []GithubJobSummary{}}
	err = paginate("ListJobs", func(cursor []byte) ([]byte, error) {
		body := map[string]any{"maxEntries": "100"}
		if len(cursor) > 0 {
			body["paginationCursor"] = cursor
		}
		if len(tr) > 0 {
			body["timeRange"] = tr
		}
		if in.Repository != nil && *in.Repository != "" {
			body["repository"] = map[string]any{"values": []string{*in.Repository}, "op": "IS_ANY_OF"}
		}
		if in.Conclusion != nil && *in.Conclusion != "" {
			body["conclusion"] = map[string]any{"values": []string{*in.Conclusion}, "op": "IS_ANY_OF"}
		}
		var rq githubv1.ListJobsRequest
		if err := toProto(body, &rq); err != nil {
			return nil, err
		}
		var res githubv1.ListJobsResponse
		if err := c.call(ctx, c.endpoints.globalURL(), githubJobsService, "ListJobs", &rq, &res); err != nil {
			return nil, err
		}
		for _, j := range res.GetJobs() {
			if len(out.Jobs) >= limit {
				return nil, nil
			}
			out.Jobs = append(out.Jobs, GithubJobSummary{
				JobID: strconv.FormatInt(j.GetJobId(), 10), RunID: strconv.FormatInt(j.GetRunId(), 10),
				Repository: j.GetRepository(), WorkflowName: j.GetWorkflowName(), JobName: j.GetJobName(),
				Conclusion: j.GetConclusion(), Ref: j.GetRef(), SenderLogin: j.GetSenderLogin(),
				CreatedAt: formatTimestamp(j.GetCreatedAt()), CompletedAt: formatTimestamp(j.GetCompletedAt()),
			})
		}
		if len(out.Jobs) >= limit {
			return nil, nil
		}
		return res.GetPaginationCursor(), nil
	})
	if err != nil {
		return infer.FunctionResponse[GetGithubJobsResult]{}, fmt.Errorf("list github jobs: %w", err)
	}
	return infer.FunctionResponse[GetGithubJobsResult]{Output: out}, nil
}
