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

// Package version holds the provider version, injected at build time via ldflags.
package version

// Version is injected by the build system via:
//
//	-ldflags "-X github.com/pulumi/autoregistry/lib/providers/namespace/provider/pkg/version.Version=x.y.z"
var Version string
