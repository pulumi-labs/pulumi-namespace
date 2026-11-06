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

	"github.com/stretchr/testify/assert"
)

// #1569: the GitHub runner services are not served by the public global host
// (it answers 404), so with no endpoint configured they use their own host,
// the other global services keep theirs, and an explicit global endpoint still
// routes everything to one place.
func TestEndpoints_GithubServicesHaveTheirOwnDefaultHost(t *testing.T) {
	var def endpoints
	assert.Equal(t, "https://private-api.global.namespaceapis.com", def.githubURL())
	assert.Equal(t, "https://global.namespaceapis.com", def.globalURL())

	set := endpoints{Global: "http://127.0.0.1:1234/"}
	assert.Equal(t, "http://127.0.0.1:1234", set.githubURL())
	assert.Equal(t, "http://127.0.0.1:1234", set.globalURL())
}
