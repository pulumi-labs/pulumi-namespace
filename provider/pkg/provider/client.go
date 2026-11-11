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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// Namespace Cloud API client
//
// Namespace exposes its API as Connect RPC over HTTPS. A unary call is a plain
// HTTP POST to https://<host>/<proto package>.<Service>/<Method> with a JSON
// body in protobuf-JSON encoding. This file is a deliberately thin transport:
// all request and response bodies are the schema-generated protobuf messages
// from buf.build/namespace/cloud, marshalled with protojson, so the wire format
// is exactly what the upstream schema defines (int64 as strings, enums as names,
// timestamps as RFC3339, field names in lowerCamelCase).
// ---------------------------------------------------------------------------

// Fully qualified RPC service names. TestRPCNamesExistInSchema verifies
// each of these (and every method used below) against the generated
// descriptors, so a rename upstream fails a test rather than a user's apply.
const (
	vaultService    = "namespace.cloud.vault.v1beta.VaultService"
	tenantService   = "namespace.cloud.iam.v1beta.TenantService"
	computeService  = "namespace.cloud.compute.v1beta.ComputeService"
	defaultRegion   = "us"
	iamHost         = "https://iam.namespaceapis.com"
	globalHost      = "https://global.namespaceapis.com"
	githubHost      = "https://private-api.global.namespaceapis.com"
	computeHostTmpl = "https://%s.compute.namespaceapis.com"
	userAgent       = "pulumi-namespace-autoregistry"
	maxResponseSize = 32 << 20
)

// APIError is a Connect error: a stable lowercase code plus a message, together
// with the HTTP status the server answered with. Details holds the readable
// form of any Connect error details the server attached (a Connect error body
// may carry them next to the message), and Hint is guidance the provider adds
// when the API gave no usable reason.
type APIError struct {
	Method     string
	HTTPStatus int
	Code       string
	Message    string
	Details    []string
	Hint       string
}

func (e *APIError) Error() string {
	code := e.Code
	if code == "" {
		code = http.StatusText(e.HTTPStatus)
	}
	var msg string
	if e.Message == "" {
		msg = fmt.Sprintf("namespace: %s failed: %s (HTTP %d)", e.Method, code, e.HTTPStatus)
	} else {
		msg = fmt.Sprintf("namespace: %s failed: %s: %s (HTTP %d)", e.Method, code, e.Message, e.HTTPStatus)
	}
	if len(e.Details) > 0 {
		msg += "; details: " + strings.Join(e.Details, "; ")
	}
	if e.Hint != "" {
		msg += ". " + e.Hint
	}
	return msg
}

// tenantMethods are the TenantService RPCs. The API answers them with a
// generic invalid_argument ("unspecified message") for a token whose account is
// not enabled for tenant management, so the error names that likely cause (#1805, #1806).
var tenantMethods = map[string]bool{
	"CreateTenant": true, "GetTenant": true, "ListTenants": true, "DeleteTenant": true,
	"UpdateTenant": true, "ListTenantGroups": true, "CreateTenantGroup": true,
	"GetTenantGroup": true, "UpdateTenantGroup": true, "DeleteTenantGroup": true,
	"ListTenantMembers": true, "ListTenantPolicies": true,
}

// isUnspecified reports whether a Connect error message carries no reason: it
// is empty, or the API's placeholder "unspecified message (rid=...)".
func isUnspecified(msg string) bool {
	msg = strings.TrimSpace(msg)
	return msg == "" || strings.HasPrefix(msg, "unspecified message")
}

// hintFor explains an error whose message gives no reason.
func hintFor(method, code, message string) string {
	if code != "invalid_argument" || !isUnspecified(message) {
		return ""
	}
	if tenantMethods[method] {
		return "The API gave no reason for rejecting the request. For the tenant APIs a likely cause is that the token's " +
			"account is not enabled for tenant management (not confirmed: the API does not say) rather than a malformed " +
			"request: the provider sends only the documented fields of " + method + ". Check that the token was issued " +
			"by an account that can manage tenants, and quote the rid (if the message has one) when asking Namespace support"
	}
	return "The API gave no reason for rejecting the request; quote the rid (if the message has one) when asking Namespace support"
}

// IsNotFound reports whether the API said the target does not exist.
func (e *APIError) IsNotFound() bool {
	return e.Code == "not_found" || (e.Code == "" && e.HTTPStatus == http.StatusNotFound)
}

// isNotFound reports whether err is (or wraps) a Connect not_found error.
func isNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.IsNotFound()
}

// endpoints are the base URLs of the three API hosts. Region is only used to
// derive the compute host when no explicit compute endpoint is configured.
type endpoints struct {
	Compute string // when set, used instead of the regional host; "{region}" is substituted (tests, private deployments)
	IAM     string
	Global  string
}

func (e endpoints) computeURL(region string) string {
	if e.Compute != "" {
		// A "{region}" placeholder in an explicit endpoint is replaced with the
		// region, so tests and private deployments can still route per region.
		return strings.TrimRight(strings.ReplaceAll(e.Compute, "{region}", region), "/")
	}
	return fmt.Sprintf(computeHostTmpl, region)
}

func (e endpoints) iamURL() string {
	if e.IAM != "" {
		return strings.TrimRight(e.IAM, "/")
	}
	return iamHost
}

func (e endpoints) globalURL() string {
	if e.Global != "" {
		return strings.TrimRight(e.Global, "/")
	}
	return globalHost
}

// githubURL is the host of the GitHub runner services (profiles, jobs). They are
// not served by the public global host (it answers 404, #1569), so they have
// their own default; an explicit global endpoint still overrides it, which is
// how tests and private deployments route every global service to one place.
func (e endpoints) githubURL() string {
	if e.Global != "" {
		return strings.TrimRight(e.Global, "/")
	}
	return githubHost
}

// nsClient is a minimal Connect-over-HTTP client for the Namespace Cloud API.
type nsClient struct {
	http      *http.Client
	token     string
	region    string
	endpoints endpoints
	// maxResponse overrides maxResponseSize (tests only).
	maxResponse int64
}

func (c *nsClient) responseCap() int64 {
	if c.maxResponse > 0 {
		return c.maxResponse
	}
	return maxResponseSize
}

func newNSClient(token, region string, ep endpoints) *nsClient {
	if region == "" {
		region = defaultRegion
	}
	return &nsClient{
		http:      &http.Client{Timeout: 60 * time.Second},
		token:     token,
		region:    region,
		endpoints: ep,
	}
}

var (
	marshalOpts   = protojson.MarshalOptions{}
	unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// call performs one unary Connect RPC against baseURL. resp may be nil when the
// caller does not care about the response body (e.g. google.protobuf.Empty).
func (c *nsClient) call(ctx context.Context, baseURL, service, method string, req, resp proto.Message) error {
	body, err := marshalOpts.Marshal(req)
	if err != nil {
		return fmt.Errorf("namespace: encode %s request: %w", method, err)
	}
	url := fmt.Sprintf("%s/%s/%s", baseURL, service, method)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("namespace: build %s request: %w", method, err)
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("Connect-Protocol-Version", "1")
	hr.Header.Set("User-Agent", userAgent)
	hr.Header.Set("Authorization", "Bearer "+c.token)

	hres, err := c.http.Do(hr)
	if err != nil {
		return fmt.Errorf("namespace: %s: %w", method, err)
	}
	defer func() { _ = hres.Body.Close() }()
	// Read one byte past the cap so an oversize body is an explicit error
	// rather than a silently truncated (and then undecodable) response.
	raw, err := io.ReadAll(io.LimitReader(hres.Body, c.responseCap()+1))
	if err != nil {
		return fmt.Errorf("namespace: read %s response: %w", method, err)
	}
	if int64(len(raw)) > c.responseCap() {
		return fmt.Errorf("namespace: %s response exceeds the %d byte limit", method, c.responseCap())
	}
	if hres.StatusCode != http.StatusOK {
		return parseAPIError(method, hres.StatusCode, raw)
	}
	if resp == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := unmarshalOpts.Unmarshal(raw, resp); err != nil {
		return fmt.Errorf("namespace: decode %s response: %w", method, err)
	}
	return nil
}

// parseAPIError decodes a Connect error body {"code": "...", "message": "..."}.
// Non-JSON bodies (proxies, gateways) fall back to the HTTP status alone.
func parseAPIError(method string, status int, raw []byte) *APIError {
	ae := &APIError{Method: method, HTTPStatus: status}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Type  string          `json:"type"`
			Debug json.RawMessage `json:"debug"`
		} `json:"details"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		ae.Code = payload.Code
		ae.Message = payload.Message
		for _, d := range payload.Details {
			dbg := strings.TrimSpace(string(d.Debug))
			switch {
			case dbg != "" && dbg != "null":
				ae.Details = append(ae.Details, strings.TrimSpace(d.Type+" "+dbg))
			case d.Type != "":
				ae.Details = append(ae.Details, d.Type)
			}
		}
	}
	if ae.Code == "" {
		switch status {
		case http.StatusNotFound:
			ae.Code = "not_found"
		case http.StatusUnauthorized:
			ae.Code = "unauthenticated"
		case http.StatusForbidden:
			ae.Code = "permission_denied"
		}
	}
	ae.Hint = hintFor(method, ae.Code, ae.Message)
	return ae
}

// notFoundOnImport reports a missing remote object during an import.
//
// The engine treats an empty ID from Read as "deleted" only on refresh; on import the
// framework accepts the empty response and an empty resource lands in state with exit
// code 0. An import is recognisable by Read arriving with neither prior inputs nor prior
// state, so there a missing object is an error. On refresh it returns nil and the caller
// returns the empty response that drops the resource from state.
func notFoundOnImport[A, S any](kind, id string, inputs A, state S) error {
	if reflect.ValueOf(inputs).IsZero() && reflect.ValueOf(state).IsZero() {
		return &importNotFoundError{kind: kind, id: id}
	}
	return nil
}

// importNotFoundError is the error notFoundOnImport returns. It is a type so that the lookup
// functions, which run Read from the ID alone, can turn it into their own not-found error.
type importNotFoundError struct{ kind, id string }

func (e *importNotFoundError) Error() string {
	return fmt.Sprintf("cannot import %s %q: it does not exist (or the API key cannot see it); check the import ID", e.kind, e.id)
}
