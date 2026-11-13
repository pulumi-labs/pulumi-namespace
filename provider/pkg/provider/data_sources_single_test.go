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
	"testing"

	pgp "github.com/pulumi/pulumi-go-provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRegistryImage(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(registrySvc+"GetImage", `{"image":{"repository":"app","digest":"sha256:abc","tags":["v1","latest"],"sizes":{"total":"2048"}}}`)
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getRegistryImage",
		Args: pm(map[string]any{"repository": "app", "reference": "v1"})})
	require.NoError(t, err)
	assert.Equal(t, "sha256:abc", resp.Return.Get("digest").AsString())
	assert.Equal(t, "2048", resp.Return.Get("totalBytes").AsString())
	assert.Equal(t, 2, resp.Return.Get("tags").AsArray().Len())
	assert.Equal(t, "v1", resp.Return.Get("reference").AsString())
	c := f.callsTo(registrySvc + "GetImage")
	require.Len(t, c, 1)
	assert.Equal(t, "app", c[0].Body["repository"])
	assert.Equal(t, "v1", c[0].Body["reference"])
}

func TestGetRegistryImage_MissingFields(t *testing.T) {
	f := newFakeAPI(t)
	s := newServerFor(t, f)
	_, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getRegistryImage",
		Args: pm(map[string]any{"repository": "app", "reference": ""})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs repository and reference")
	assert.Empty(t, f.callsTo(registrySvc+"GetImage"))
}

func TestGetRegistryImage_EmptyResponseIsNotFound(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(registrySvc+"GetImage", `{}`)
	s := newServerFor(t, f)
	_, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getRegistryImage",
		Args: pm(map[string]any{"repository": "app", "reference": "gone"})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not found")
}

func TestGetGithubJob(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(ghJobsSvc+"DescribeJob", `{"job":{"jobId":"1234567890","runId":"77","repository":"acme/app","jobName":"build","conclusion":"success"}}`)
	s := newServerFor(t, f)
	resp, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getGithubJob",
		Args: pm(map[string]any{"jobId": "1234567890"})})
	require.NoError(t, err)
	assert.Equal(t, "1234567890", resp.Return.Get("jobId").AsString())
	assert.Equal(t, "77", resp.Return.Get("runId").AsString())
	assert.Equal(t, "success", resp.Return.Get("conclusion").AsString())
	c := f.callsTo(ghJobsSvc + "DescribeJob")
	require.Len(t, c, 1)
	assert.Equal(t, "1234567890", c[0].Body["jobId"])
}

func TestGetGithubJob_InvalidAndMissing(t *testing.T) {
	f := newFakeAPI(t)
	f.ok(ghJobsSvc+"DescribeJob", `{}`)
	s := newServerFor(t, f)
	for _, bad := range []string{"abc", "0", "-5", ""} {
		_, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getGithubJob",
			Args: pm(map[string]any{"jobId": bad})})
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "positive decimal number")
	}
	assert.Empty(t, f.callsTo(ghJobsSvc+"DescribeJob"))
	_, err := s.Invoke(pgp.InvokeRequest{Token: "namespace:index:getGithubJob",
		Args: pm(map[string]any{"jobId": "42"})})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not found")
}
