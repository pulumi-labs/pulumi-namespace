package provider

import (
	"testing"

	githubv1 "buf.build/gen/go/namespace/cloud/protocolbuffers/go/proto/namespace/cloud/github/v1beta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Namespace API removed CacheVolumeSettings.volume_tag. volumeTag stays an
// optional, deprecated input: it must not be required, must not be sent, and
// must round-trip through state from the declared inputs.
func TestGithubProfileVolumeTagIsDeprecatedAndIgnored(t *testing.T) {
	tag, arch, osName := "go", "amd64", "linux"
	in := GithubProfileArgs{
		Tag:           "p",
		InstanceShape: ProfileInstanceShape{VirtualCPU: 4, MemoryMegabytes: 8192, MachineArch: &arch, Os: &osName},
		CacheVolumeSettings: []ProfileCacheVolume{
			{VolumeTag: &tag},
			{},
		},
	}
	for _, f := range in.validate() {
		assert.NotContains(t, f.Property, "volumeTag", f.Reason)
	}

	spec, err := profileSpec(in)
	require.NoError(t, err)
	require.Len(t, spec.GetCacheVolumeSettings(), 2)

	var state GithubProfileState
	require.NoError(t, applyProfile(&state, &githubv1.RunnerProfileWithStatus{ProfileId: "id", Spec: spec}, &in))
	require.Len(t, state.CacheVolumeSettings, 2)
	require.NotNil(t, state.CacheVolumeSettings[0].VolumeTag)
	assert.Equal(t, "go", *state.CacheVolumeSettings[0].VolumeTag)
	assert.Nil(t, state.CacheVolumeSettings[1].VolumeTag)
}
