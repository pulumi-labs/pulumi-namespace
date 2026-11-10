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

	githubv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/github/v1beta"
	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ---------------------------------------------------------------------------
// GithubProfile resource
// ---------------------------------------------------------------------------

// GithubProfile is a GitHub Actions runner profile: the shape, caches, base
// image and network policy that 'runs-on: nscloud-<tag>' jobs use.
type GithubProfile struct{}

// ProfileInstanceShape is the machine shape of a runner profile.
type ProfileInstanceShape struct {
	VirtualCPU      int     `pulumi:"virtualCpu" json:"virtualCpu"`
	MemoryMegabytes int     `pulumi:"memoryMegabytes" json:"memoryMegabytes"`
	MachineArch     *string `pulumi:"machineArch,optional" json:"machineArch,omitempty"`
	Os              *string `pulumi:"os,optional" json:"os,omitempty"`
}

func (s *ProfileInstanceShape) Annotate(a infer.Annotator) {
	a.Describe(&s.VirtualCPU, "Number of virtual CPUs.")
	a.Describe(&s.MemoryMegabytes, "Memory in megabytes.")
	a.Describe(&s.MachineArch, "Machine architecture: 'amd64' or 'arm64'. Required.")
	a.Describe(&s.Os, "Operating system the platform reports for the shape (for example 'linux'). Not an input vocabulary of its own: choose the runner image with the profile's top-level os.")
}

// ProfileCacheVolume configures one cache volume attached to runners.
type ProfileCacheVolume struct {
	// VolumeTag is deprecated: the Namespace API no longer accepts a volume tag,
	// so it is never sent. It stays an optional input so existing programs keep
	// working, and is round-tripped through state from the declared inputs.
	VolumeTag                 *string  `pulumi:"volumeTag,optional" json:"-"`
	VolumeSizeGb              *int     `pulumi:"volumeSizeGb,optional" json:"volumeSizeGb,omitempty"`
	VolumeReservedSizeGb      *int     `pulumi:"volumeReservedSizeGb,optional" json:"volumeReservedSizeGb,omitempty"`
	BundledContainerCache     *bool    `pulumi:"bundledContainerCache,optional" json:"bundledContainerCache,omitempty"`
	BundledBuildkitCache      *bool    `pulumi:"bundledBuildkitCache,optional" json:"bundledBuildkitCache,omitempty"`
	BundledGitMirror          *bool    `pulumi:"bundledGitMirror,optional" json:"bundledGitMirror,omitempty"`
	BundledRunnerToolsCache   *bool    `pulumi:"bundledRunnerToolsCache,optional" json:"bundledRunnerToolsCache,omitempty"`
	BundledRunnerActionsCache *bool    `pulumi:"bundledRunnerActionsCache,optional" json:"bundledRunnerActionsCache,omitempty"`
	AllowCommitFromBranch     []string `pulumi:"allowCommitFromBranch,optional" json:"allowCommitFromBranch,omitempty"`
}

func (v *ProfileCacheVolume) Annotate(a infer.Annotator) {
	a.Describe(&v.VolumeTag, "Deprecated and ignored: the Namespace API no longer accepts a cache volume tag.")
	a.Deprecate(&v.VolumeTag, "The Namespace API removed the cache volume tag; this value is ignored and no longer sent.")
	a.Describe(&v.VolumeSizeGb, "Size of the cache volume in GB.")
	a.Describe(&v.VolumeReservedSizeGb, "Reserved (pre-provisioned) size of the cache volume in GB.")
	a.Describe(&v.BundledContainerCache, "Bundle a container image cache into the volume.")
	a.Describe(&v.BundledBuildkitCache, "Bundle a BuildKit cache into the volume.")
	a.Describe(&v.BundledGitMirror, "Bundle a git mirror into the volume.")
	a.Describe(&v.BundledRunnerToolsCache, "Bundle the GitHub runner tools cache into the volume.")
	a.Describe(&v.BundledRunnerActionsCache, "Bundle the GitHub Actions cache into the volume.")
	a.Describe(&v.AllowCommitFromBranch, "Branches whose runs may commit changes back to the cache volume.")
}

// ProfileUbuntuSpec builds the custom runner image from the Ubuntu base image.
type ProfileUbuntuSpec struct {
	AptPackage []string `pulumi:"aptPackage,optional" json:"aptPackage,omitempty"`
}

func (s *ProfileUbuntuSpec) Annotate(a infer.Annotator) {
	a.Describe(&s.AptPackage, "APT packages to install on top of the Ubuntu base image.")
}

// ProfileDockerSpec builds the custom runner image from a Dockerfile.
type ProfileDockerSpec struct {
	DockerfileContent string `pulumi:"dockerfileContent" json:"dockerfileContent"`
}

func (s *ProfileDockerSpec) Annotate(a infer.Annotator) {
	a.Describe(&s.DockerfileContent, "Dockerfile content describing the custom runner image.")
}

// ProfileImageSpec describes a custom runner base image.
type ProfileImageSpec struct {
	UbuntuSpec *ProfileUbuntuSpec `pulumi:"ubuntuSpec,optional" json:"ubuntuSpec,omitempty"`
	DockerSpec *ProfileDockerSpec `pulumi:"dockerSpec,optional" json:"dockerSpec,omitempty"`
}

func (s *ProfileImageSpec) Annotate(a infer.Annotator) {
	a.Describe(&s.UbuntuSpec, "Build the image from the Ubuntu base image plus APT packages. Set at most one of ubuntuSpec and dockerSpec.")
	a.Describe(&s.DockerSpec, "Build the image from a Dockerfile. Set at most one of ubuntuSpec and dockerSpec.")
}

// ProfileNetworkPolicy is the egress policy of a runner profile.
type ProfileNetworkPolicy struct {
	Egress                *string  `pulumi:"egress,optional" json:"egress,omitempty"`
	EgressDomainAllowList []string `pulumi:"egressDomainAllowList,optional" json:"egressDomainAllowList,omitempty"`
	Advisory              *bool    `pulumi:"advisory,optional" json:"advisory,omitempty"`
	EgressPolicyTag       *string  `pulumi:"egressPolicyTag,optional" json:"egressPolicyTag,omitempty"`
}

func (n *ProfileNetworkPolicy) Annotate(a infer.Annotator) {
	a.Describe(&n.Egress, "Egress mode: EGRESS_POLICY_NONE, EGRESS_POLICY_DOMAIN_ALLOW_LIST (requires egressDomainAllowList) "+
		"or EGRESS_POLICY_TAG (requires egressPolicyTag, see the EgressPolicy resource).")
	a.Describe(&n.EgressDomainAllowList, "Domains runners may reach when egress is EGRESS_POLICY_DOMAIN_ALLOW_LIST.")
	a.Describe(&n.Advisory, "Only report violations instead of blocking them.")
	a.Describe(&n.EgressPolicyTag, "Tag of an EgressPolicy to apply when egress is EGRESS_POLICY_TAG.")
}

// GithubProfileArgs are the user-supplied inputs for a runner profile.
type GithubProfileArgs struct {
	Tag                   string                `pulumi:"tag" json:"tag" provider:"replaceOnChanges"`
	Description           *string               `pulumi:"description,optional" json:"description,omitempty"`
	Os                    *string               `pulumi:"os,optional" json:"os,omitempty"`
	InstanceShape         ProfileInstanceShape  `pulumi:"instanceShape" json:"instanceShape"`
	CacheVolumeSettings   []ProfileCacheVolume  `pulumi:"cacheVolumeSettings,optional" json:"cacheVolumeSettings,omitempty"`
	BuilderMode           *string               `pulumi:"builderMode,optional" json:"builderMode,omitempty"`
	ExperimentalFeatures  []string              `pulumi:"experimentalFeatures,optional" json:"experimentalFeatures,omitempty"`
	Emoji                 *string               `pulumi:"emoji,optional" json:"emoji,omitempty"`
	CustomRunnerImageSpec *ProfileImageSpec     `pulumi:"customRunnerImageSpec,optional" json:"customRunnerImageSpec,omitempty"`
	NetworkPolicy         *ProfileNetworkPolicy `pulumi:"networkPolicy,optional" json:"networkPolicy,omitempty"`
	AccessLevel           *string               `pulumi:"accessLevel,optional" json:"accessLevel,omitempty"`
	SwapSizeMb            *int                  `pulumi:"swapSizeMb,optional" json:"swapSizeMb,omitempty"`
	RebuildTrigger        *string               `pulumi:"rebuildTrigger,optional" json:"-"`
}

// GithubProfileState is the persisted state of a runner profile.
type GithubProfileState struct {
	GithubProfileArgs

	ProfileID      string  `pulumi:"profileId"`
	Version        int     `pulumi:"version"`
	CreatedAt      string  `pulumi:"createdAt"`
	UpdatedAt      string  `pulumi:"updatedAt"`
	ImageStatus    *string `pulumi:"imageStatus,optional"`
	ImageRef       *string `pulumi:"imageRef,optional"`
	LastBuildError *string `pulumi:"lastBuildError,optional"`
}

func (r *GithubProfile) Annotate(a infer.Annotator) {
	a.Describe(&r, "A GitHub Actions runner profile on Namespace: machine shape, cache volumes, custom base image, "+
		"network policy and access level used by 'runs-on: nscloud-<tag>' jobs. Changing 'tag' replaces the profile; "+
		"everything else updates in place using the profile version for optimistic concurrency. "+
		"Import with the profile ID. Changing 'rebuildTrigger' calls RebuildProfileImage to rebuild the custom base image.")
}

func (a *GithubProfileArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.Tag, "The profile tag used in 'runs-on' labels. Changing it replaces the profile.")
	ann.Describe(&a.Description, "A human-readable description of the profile.")
	ann.Describe(&a.Os, "Runner image of the profile. Required: Namespace accepts ubuntu-20.04, ubuntu-22.04, ubuntu-24.04, ubuntu-24.04-systemd or ubuntu-26.04 (not 'linux'; that is what the platform reports in instanceShape.os).")
	ann.Describe(&a.InstanceShape, "The machine shape of runners using this profile.")
	ann.Describe(&a.CacheVolumeSettings, "Cache volumes attached to runners.")
	ann.Describe(&a.BuilderMode, "Docker builder mode: USE_REMOTE_BUILDER, USE_LOCAL_CACHE or NO_CACHING.")
	ann.Describe(&a.ExperimentalFeatures, "Experimental feature flags enabled on the profile.")
	ann.Describe(&a.Emoji, "An emoji shown next to the profile in the dashboard.")
	ann.Describe(&a.CustomRunnerImageSpec, "A custom base image for runners, built by Namespace.")
	ann.Describe(&a.NetworkPolicy, "Egress network policy applied to runners.")
	ann.Describe(&a.AccessLevel, "Access level: ACCESS_LEVEL_PERMISSIVE, ACCESS_LEVEL_LIMITED or ACCESS_LEVEL_RESTRICTED.")
	ann.Describe(&a.SwapSizeMb, "Swap size in megabytes.")
	ann.Describe(&a.RebuildTrigger, "Any string. Changing it triggers a rebuild of the custom runner image "+
		"(RebuildProfileImage) during update. Requires customRunnerImageSpec.")
}

func (s *GithubProfileState) Annotate(ann infer.Annotator) {
	ann.Describe(&s.ProfileID, "The server-assigned profile identifier.")
	ann.Describe(&s.Version, "The profile version; increments on every update.")
	ann.Describe(&s.CreatedAt, "RFC3339 timestamp of creation.")
	ann.Describe(&s.UpdatedAt, "RFC3339 timestamp of the last update.")
	ann.Describe(&s.ImageStatus, "Build status of the custom runner image: BUILDING, FAILED or READY.")
	ann.Describe(&s.ImageRef, "Reference of the built custom runner image.")
	ann.Describe(&s.LastBuildError, "Error of the last failed image build, if any.")
}

var (
	profileOSes   = []string{"ubuntu-20.04", "ubuntu-22.04", "ubuntu-24.04", "ubuntu-24.04-systemd", "ubuntu-26.04"}
	machineArches = []string{"arm64", "amd64"}
	builderModes  = []string{"USE_REMOTE_BUILDER", "USE_LOCAL_CACHE", "NO_CACHING"}
	accessLevels  = []string{"ACCESS_LEVEL_PERMISSIVE", "ACCESS_LEVEL_LIMITED", "ACCESS_LEVEL_RESTRICTED"}
	egressModes   = []string{"EGRESS_POLICY_NONE", "EGRESS_POLICY_DOMAIN_ALLOW_LIST", "EGRESS_POLICY_TAG"}
)

func (a GithubProfileArgs) validate() []pgp.CheckFailure {
	var f []pgp.CheckFailure
	f = append(f, requireNonEmpty("tag", a.Tag)...)
	if a.InstanceShape.VirtualCPU <= 0 {
		f = append(f, fail("instanceShape.virtualCpu", "must be greater than 0"))
	}
	if a.InstanceShape.MemoryMegabytes <= 0 {
		f = append(f, fail("instanceShape.memoryMegabytes", "must be greater than 0"))
	}
	// The API rejects a profile without these at apply time (#1570), so fail at
	// preview with the supported values.
	f = append(f, reqIn("os", a.Os, profileOSes...)...)
	f = append(f, reqIn("instanceShape.machineArch", a.InstanceShape.MachineArch, machineArches...)...)
	f = append(f, optIn("builderMode", a.BuilderMode, builderModes...)...)
	f = append(f, optIn("accessLevel", a.AccessLevel, accessLevels...)...)
	if s := a.CustomRunnerImageSpec; s != nil && s.UbuntuSpec != nil && s.DockerSpec != nil {
		f = append(f, fail("customRunnerImageSpec", "set at most one of ubuntuSpec and dockerSpec"))
	}
	if a.RebuildTrigger != nil && a.CustomRunnerImageSpec == nil {
		f = append(f, fail("rebuildTrigger", "requires customRunnerImageSpec"))
	}
	if n := a.NetworkPolicy; n != nil {
		f = append(f, optIn("networkPolicy.egress", n.Egress, egressModes...)...)
		if n.Egress != nil {
			switch *n.Egress {
			case "EGRESS_POLICY_TAG":
				if n.EgressPolicyTag == nil || *n.EgressPolicyTag == "" {
					f = append(f, fail("networkPolicy.egressPolicyTag", "is required when egress is EGRESS_POLICY_TAG"))
				}
			case "EGRESS_POLICY_DOMAIN_ALLOW_LIST":
				if len(n.EgressDomainAllowList) == 0 {
					f = append(f, fail("networkPolicy.egressDomainAllowList", "is required when egress is EGRESS_POLICY_DOMAIN_ALLOW_LIST"))
				}
			}
		}
	}
	return f
}

func (r *GithubProfile) Check(
	ctx context.Context, req infer.CheckRequest,
) (infer.CheckResponse[GithubProfileArgs], error) {
	return checkWith(ctx, req, GithubProfileArgs.validate)
}

func profileSpec(in GithubProfileArgs) (*githubv1.RunnerProfileSpec, error) {
	var spec githubv1.RunnerProfileSpec
	if err := toProto(in, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// Values the platform fills in for a profile that does not declare them.
const (
	defaultBuilderMode = "NO_CACHING"
	defaultShapeOS     = "linux"
)

// Diff compares the program with the recorded inputs like the framework's default diff, except that
// cacheVolumeSettings[].volumeTag never counts as a change: the Namespace API removed it, it is never sent
// and an import cannot read it back, so a program that still declares it would otherwise plan an update
// on the first preview after an import (#1691).
func (r *GithubProfile) Diff(
	_ context.Context, req infer.DiffRequest[GithubProfileArgs, GithubProfileState],
) (infer.DiffResponse, error) {
	old, cur := req.State.GithubProfileArgs, req.Inputs
	if len(cur.CacheVolumeSettings) > 0 {
		vols := make([]ProfileCacheVolume, len(cur.CacheVolumeSettings))
		copy(vols, cur.CacheVolumeSettings)
		for i := range vols {
			if i < len(old.CacheVolumeSettings) {
				vols[i].VolumeTag = old.CacheVolumeSettings[i].VolumeTag
			} else {
				vols[i].VolumeTag = nil
			}
		}
		cur.CacheVolumeSettings = vols
	}
	return diffArgs(old, cur), nil
}

// applyProfile copies server-owned fields and, on import (declared == nil), the
// whole spec into state.
func applyProfile(state *GithubProfileState, p *githubv1.RunnerProfileWithStatus, declared *GithubProfileArgs) error {
	var srv GithubProfileArgs
	if err := fromProto(p.GetSpec(), &srv); err != nil {
		return err
	}
	if declared == nil {
		// Import: the program is not known, so drop what the platform fills in
		// itself. A program that declares these values later sees an in-place
		// update at most (#1571).
		if srv.BuilderMode != nil && *srv.BuilderMode == defaultBuilderMode {
			srv.BuilderMode = nil
		}
		if srv.InstanceShape.Os != nil && *srv.InstanceShape.Os == defaultShapeOS {
			srv.InstanceShape.Os = nil
		}
	}
	if declared != nil {
		keepDeclared(*declared, &srv)
		// volumeTag is not part of the API any more, so carry the declared
		// value into state to avoid a perpetual diff.
		for i := range srv.CacheVolumeSettings {
			if i < len(declared.CacheVolumeSettings) {
				srv.CacheVolumeSettings[i].VolumeTag = declared.CacheVolumeSettings[i].VolumeTag
			}
		}
		srv.RebuildTrigger = declared.RebuildTrigger
	}
	state.GithubProfileArgs = srv
	state.ProfileID = p.GetProfileId()
	state.Version = int(p.GetVersion())
	state.CreatedAt = formatTimestamp(p.GetCreatedAt())
	state.UpdatedAt = formatTimestamp(p.GetUpdatedAt())
	state.ImageStatus, state.ImageRef, state.LastBuildError = nil, nil, nil
	if imgs := p.GetStatus().GetCustomRunnerImage(); len(imgs) > 0 {
		st := imgs[0].GetImageStatus()
		state.ImageStatus = emptyToNil(st.GetStatus().String())
		state.ImageRef = emptyToNil(st.GetImageRef())
		state.LastBuildError = emptyToNil(st.GetLastBuildError())
	}
	return nil
}

func (r *GithubProfile) Create(
	ctx context.Context, req infer.CreateRequest[GithubProfileArgs],
) (infer.CreateResponse[GithubProfileState], error) {
	state := GithubProfileState{GithubProfileArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[GithubProfileState]{ID: "", Output: state}, nil
	}
	spec, err := profileSpec(req.Inputs)
	if err != nil {
		return infer.CreateResponse[GithubProfileState]{}, err
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.CreateResponse[GithubProfileState]{}, err
	}
	var res githubv1.CreateProfileResponse
	if err := c.call(ctx, c.endpoints.githubURL(), githubProfileService, "CreateProfile",
		&githubv1.CreateProfileRequest{Spec: spec}, &res); err != nil {
		return infer.CreateResponse[GithubProfileState]{}, fmt.Errorf("create github profile %q: %w", req.Inputs.Tag, err)
	}
	if res.GetProfile() == nil || res.GetProfile().GetProfileId() == "" {
		return infer.CreateResponse[GithubProfileState]{}, fmt.Errorf("create github profile %q: response carried no profile id", req.Inputs.Tag)
	}
	if err := applyProfile(&state, res.GetProfile(), &req.Inputs); err != nil {
		return infer.CreateResponse[GithubProfileState]{}, err
	}
	return infer.CreateResponse[GithubProfileState]{ID: state.ProfileID, Output: state}, nil
}

// findProfile returns the profile with the given ID, or nil when it is gone.
// The API has no single-profile getter, so this lists all profiles.
func findProfile(ctx context.Context, c *nsClient, id string) (*githubv1.RunnerProfileWithStatus, error) {
	ps, err := listProfiles(ctx, c)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.GetProfileId() == id {
			return p, nil
		}
	}
	return nil, nil
}

func listProfiles(ctx context.Context, c *nsClient) ([]*githubv1.RunnerProfileWithStatus, error) {
	var res githubv1.ListProfilesResponse
	if err := c.call(ctx, c.endpoints.githubURL(), githubProfileService, "ListProfiles", &emptypb.Empty{}, &res); err != nil {
		return nil, err
	}
	return res.GetProfiles(), nil
}

func (r *GithubProfile) Read(
	ctx context.Context, req infer.ReadRequest[GithubProfileArgs, GithubProfileState],
) (infer.ReadResponse[GithubProfileArgs, GithubProfileState], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.ReadResponse[GithubProfileArgs, GithubProfileState]{}, err
	}
	p, err := findProfile(ctx, c, req.ID)
	if err != nil {
		return infer.ReadResponse[GithubProfileArgs, GithubProfileState]{}, fmt.Errorf("read github profile %q: %w", req.ID, err)
	}
	if p == nil {
		if err := notFoundOnImport("github profile", req.ID, req.Inputs, req.State); err != nil {
			return infer.ReadResponse[GithubProfileArgs, GithubProfileState]{}, err
		}
		return infer.ReadResponse[GithubProfileArgs, GithubProfileState]{}, nil
	}
	var state GithubProfileState
	var declared *GithubProfileArgs
	if req.Inputs.Tag != "" { // refresh; an import has no prior inputs
		d := req.Inputs
		declared = &d
	}
	if err := applyProfile(&state, p, declared); err != nil {
		return infer.ReadResponse[GithubProfileArgs, GithubProfileState]{}, err
	}
	return infer.ReadResponse[GithubProfileArgs, GithubProfileState]{
		ID: state.ProfileID, Inputs: state.GithubProfileArgs, State: state}, nil
}

func (r *GithubProfile) Update(
	ctx context.Context, req infer.UpdateRequest[GithubProfileArgs, GithubProfileState],
) (infer.UpdateResponse[GithubProfileState], error) {
	state := req.State
	state.GithubProfileArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[GithubProfileState]{Output: state}, nil
	}
	spec, err := profileSpec(req.Inputs)
	if err != nil {
		return infer.UpdateResponse[GithubProfileState]{}, err
	}
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.UpdateResponse[GithubProfileState]{}, err
	}
	var res githubv1.UpdateProfileResponse
	if err := c.call(ctx, c.endpoints.githubURL(), githubProfileService, "UpdateProfile", &githubv1.UpdateProfileRequest{
		ProfileId:     req.ID,
		Spec:          spec,
		UpdateVersion: int64(req.State.Version),
	}, &res); err != nil {
		return infer.UpdateResponse[GithubProfileState]{}, fmt.Errorf("update github profile %q: %w", req.ID, err)
	}
	profile := res.GetProfile()
	trigger, old := derefStr(req.Inputs.RebuildTrigger), derefStr(req.State.RebuildTrigger)
	if trigger != old && req.Inputs.CustomRunnerImageSpec != nil {
		var rb githubv1.RebuildProfileImageResponse
		if err := c.call(ctx, c.endpoints.githubURL(), githubProfileService, "RebuildProfileImage",
			&githubv1.RebuildProfileImageRequest{ProfileId: req.ID}, &rb); err != nil {
			// UpdateProfile already succeeded (and bumped the version), so record
			// that, but keep the previous rebuildTrigger in state so the next up
			// sees the trigger change and retries the rebuild.
			rerr := fmt.Errorf("rebuild image of github profile %q: %w", req.ID, err)
			if profile != nil {
				if aerr := applyProfile(&state, profile, &req.Inputs); aerr == nil {
					state.RebuildTrigger = req.State.RebuildTrigger
					return infer.UpdateResponse[GithubProfileState]{Output: state},
						infer.ResourceInitFailedError{Reasons: []string{rerr.Error()}}
				}
			}
			return infer.UpdateResponse[GithubProfileState]{}, rerr
		}
		if profile != nil && rb.GetStatus() != nil {
			profile.Status = rb.GetStatus()
		}
	}
	if profile == nil {
		return infer.UpdateResponse[GithubProfileState]{}, fmt.Errorf("update github profile %q: response carried no profile", req.ID)
	}
	if err := applyProfile(&state, profile, &req.Inputs); err != nil {
		return infer.UpdateResponse[GithubProfileState]{}, err
	}
	return infer.UpdateResponse[GithubProfileState]{Output: state}, nil
}

func (r *GithubProfile) Delete(
	ctx context.Context, req infer.DeleteRequest[GithubProfileState],
) (infer.DeleteResponse, error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	err = c.call(ctx, c.endpoints.githubURL(), githubProfileService, "DeleteProfile",
		&githubv1.DeleteProfileRequest{ProfileId: req.ID}, nil)
	if err != nil && !isNotFound(err) {
		return infer.DeleteResponse{}, fmt.Errorf("delete github profile %q: %w", req.ID, err)
	}
	return infer.DeleteResponse{}, nil
}

// ---------------------------------------------------------------------------
// getGithubProfiles data source
// ---------------------------------------------------------------------------

type GetGithubProfiles struct{}

type GetGithubProfilesArgs struct{}

type GithubProfileSummary struct {
	ProfileID       string `pulumi:"profileId"`
	Tag             string `pulumi:"tag"`
	Description     string `pulumi:"description"`
	Os              string `pulumi:"os"`
	VirtualCPU      int    `pulumi:"virtualCpu"`
	MemoryMegabytes int    `pulumi:"memoryMegabytes"`
	Version         int    `pulumi:"version"`
	ImageStatus     string `pulumi:"imageStatus"`
	CreatedAt       string `pulumi:"createdAt"`
	UpdatedAt       string `pulumi:"updatedAt"`
}

type GetGithubProfilesResult struct {
	Profiles []GithubProfileSummary `pulumi:"profiles"`
}

func (f *GetGithubProfiles) Annotate(a infer.Annotator) {
	a.Describe(&f, "Lists the GitHub Actions runner profiles of the current tenant.")
}

func (r *GetGithubProfilesResult) Annotate(a infer.Annotator) {
	a.Describe(&r.Profiles, "The runner profiles.")
}

func (s *GithubProfileSummary) Annotate(a infer.Annotator) {
	a.Describe(&s.ProfileID, "Profile identifier.")
	a.Describe(&s.Tag, "Profile tag used in runs-on labels.")
	a.Describe(&s.Description, "Profile description.")
	a.Describe(&s.Os, "Runner operating system.")
	a.Describe(&s.VirtualCPU, "Virtual CPUs of the instance shape.")
	a.Describe(&s.MemoryMegabytes, "Memory of the instance shape in megabytes.")
	a.Describe(&s.Version, "Profile version.")
	a.Describe(&s.ImageStatus, "Custom image build status, empty when the profile has no custom image.")
	a.Describe(&s.CreatedAt, "RFC3339 creation time.")
	a.Describe(&s.UpdatedAt, "RFC3339 last update time.")
}

func (f *GetGithubProfiles) Invoke(
	ctx context.Context, _ infer.FunctionRequest[GetGithubProfilesArgs],
) (infer.FunctionResponse[GetGithubProfilesResult], error) {
	c, err := clientFor(ctx, nil)
	if err != nil {
		return infer.FunctionResponse[GetGithubProfilesResult]{}, err
	}
	ps, err := listProfiles(ctx, c)
	if err != nil {
		return infer.FunctionResponse[GetGithubProfilesResult]{}, fmt.Errorf("list github profiles: %w", err)
	}
	out := GetGithubProfilesResult{Profiles: make([]GithubProfileSummary, 0, len(ps))}
	for _, p := range ps {
		sp := p.GetSpec()
		sum := GithubProfileSummary{
			ProfileID: p.GetProfileId(), Tag: sp.GetTag(), Description: sp.GetDescription(), Os: sp.GetOs(),
			VirtualCPU: int(sp.GetInstanceShape().GetVirtualCpu()), MemoryMegabytes: int(sp.GetInstanceShape().GetMemoryMegabytes()),
			Version: int(p.GetVersion()), CreatedAt: formatTimestamp(p.GetCreatedAt()), UpdatedAt: formatTimestamp(p.GetUpdatedAt()),
		}
		if imgs := p.GetStatus().GetCustomRunnerImage(); len(imgs) > 0 {
			sum.ImageStatus = imgs[0].GetImageStatus().GetStatus().String()
		}
		out.Profiles = append(out.Profiles, sum)
	}
	return infer.FunctionResponse[GetGithubProfilesResult]{Output: out}, nil
}

// ---- getGithubProfile ----

// GetGithubProfile reads one GitHub runner profile.
type GetGithubProfile struct{}

type GetGithubProfileArgs struct {
	ProfileID *string `pulumi:"profileId,optional"`
	Tag       *string `pulumi:"tag,optional"`
}

func (f *GetGithubProfile) Annotate(a infer.Annotator) {
	a.Describe(&f, "Reads one GitHub Actions runner profile, by profile ID or by tag. "+
		"Returns the same fields as the GithubProfile resource reports on import.")
}

func (a *GetGithubProfileArgs) Annotate(ann infer.Annotator) {
	ann.Describe(&a.ProfileID, "The profile ID. Set exactly one of profileId and tag.")
	ann.Describe(&a.Tag, "The profile tag used in 'runs-on' labels. Set exactly one of profileId and tag.")
}

func (f *GetGithubProfile) Invoke(
	ctx context.Context, req infer.FunctionRequest[GetGithubProfileArgs],
) (infer.FunctionResponse[GithubProfileState], error) {
	type resp = infer.FunctionResponse[GithubProfileState]
	id, tag := derefStr(req.Input.ProfileID), derefStr(req.Input.Tag)
	if (id == "") == (tag == "") {
		return resp{}, errExactlyOne("getGithubProfile", "profileId", "tag")
	}
	if id == "" {
		c, err := clientFor(ctx, nil)
		if err != nil {
			return resp{}, err
		}
		ps, err := listProfiles(ctx, c)
		if err != nil {
			return resp{}, fmt.Errorf("list github profiles: %w", err)
		}
		for _, p := range ps {
			if p.GetSpec().GetTag() == tag {
				id = p.GetProfileId()
				break
			}
		}
		if id == "" {
			return resp{}, errLookupNotFound("github profile with tag", tag)
		}
	}
	st, err := readLookup(ctx, "github profile", id, (&GithubProfile{}).Read)
	return resp{Output: st}, err
}
