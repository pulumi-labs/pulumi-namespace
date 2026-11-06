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
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// #1590: a preview Create that returns an ID the real Create would not return makes the `id`
// output a known, wrong value, so a program that passes it to an invoke or to another resource runs
// against something that does not exist and fails `pulumi preview`. The real ID is usually assigned
// by the service; a preview then has none, and an empty ID leaves `id` unknown.
//
// This test walks every Create method with an AST (so it does not care how a literal is formatted
// or aligned) and fails for any preview return whose ID is not empty unless the resource is listed
// in previewIDKept below with the exact expression. A listed resource keeps its ID because that ID
// is derived from the inputs alone, through the same expression on the preview path and on every
// real path, so it is the ID the apply will produce.
var previewIDKept = map[string]string{
	"Blueprint":                `req.Inputs.Name`,
	"EgressPolicy":             `req.Inputs.Tag`,
	"RegistryDefaultPolicy":    `"default"`,
	"RegistryRepositoryPolicy": `repo`,
	"TenantGroup":              `req.Inputs.Name`,
}

// previewIDs returns, for every Create method in src, the ID expressions of the CreateResponse
// literals returned from an `if <x>.DryRun { ... }` block, keyed by receiver type.
func previewIDs(t *testing.T, filename string, src []byte) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	require.NoError(t, err, filename)
	render := func(n ast.Node) string {
		var b bytes.Buffer
		require.NoError(t, printer.Fprint(&b, fset, n))
		return strings.Join(strings.Fields(b.String()), " ")
	}
	out := map[string][]string{}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name.Name != "Create" || fd.Body == nil {
			continue
		}
		recv := strings.TrimPrefix(render(fd.Recv.List[0].Type), "*")
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			is, ok := n.(*ast.IfStmt)
			if !ok || !strings.HasSuffix(render(is.Cond), ".DryRun") {
				return true
			}
			ast.Inspect(is.Body, func(m ast.Node) bool {
				cl, ok := m.(*ast.CompositeLit)
				if !ok || !strings.Contains(render(cl.Type), "CreateResponse") {
					return true
				}
				for _, e := range cl.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok && render(kv.Key) == "ID" {
						out[recv] = append(out[recv], render(kv.Value))
					}
				}
				return false
			})
			return false
		})
	}
	return out
}

func TestPreviewCreateDoesNotInventAnID(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	seenKept := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for recv, ids := range previewIDs(t, f, src) {
			for _, id := range ids {
				if id == `""` {
					continue
				}
				want, listed := previewIDKept[recv]
				if !listed || want != id {
					t.Errorf("%s: %s.Create returns the ID %s from a preview. A preview has no real ID: return an empty ID so the id output stays unknown. "+
						"Only an ID derived from the inputs alone, identical on every real path, may be kept, and then it must be listed in previewIDKept.", f, recv, id)
					continue
				}
				seenKept[recv] = true
			}
		}
	}
	var stale []string
	for recv := range previewIDKept {
		if !seenKept[recv] {
			stale = append(stale, recv)
		}
	}
	sort.Strings(stale)
	require.Empty(t, stale, "previewIDKept lists resources whose preview no longer returns that ID; remove them")
}

// The scan has to see every spelling of a literal, or it guards nothing: a single-line literal, a
// multi-line literal gofmt aligns as `ID:     expr,`, a variable, and an expression.
func TestPreviewIDScanSeesEveryLiteralForm(t *testing.T) {
	const sample = `package p

type R struct{}

func (r *R) Create(req Req) (CreateResponse[S], error) {
	if req.DryRun {
		return infer.CreateResponse[S]{ID: req.Name, Output: s}, nil
	}
	return infer.CreateResponse[S]{ID: real, Output: s}, nil
}

type Aligned struct{}

func (r *Aligned) Create(req Req) (CreateResponse[S], error) {
	if req.DryRun {
		return infer.CreateResponse[S]{
			ID:     args.Tenant + "/" + args.Name,
			Output: s,
		}, nil
	}
	return infer.CreateResponse[S]{}, nil
}

type Var struct{}

func (r *Var) Create(req Req) (CreateResponse[S], error) {
	id := mk(args)
	if req.DryRun {
		return infer.CreateResponse[S]{ID: id, Output: s}, nil
	}
	return infer.CreateResponse[S]{}, nil
}

type Empty struct{}

func (r *Empty) Create(req Req) (CreateResponse[S], error) {
	if req.DryRun {
		return infer.CreateResponse[S]{ID: "", Output: s}, nil
	}
	return infer.CreateResponse[S]{}, nil
}
`
	got := previewIDs(t, "sample.go", []byte(sample))
	require.Equal(t, map[string][]string{
		"R":       {"req.Name"},
		"Aligned": {`args.Tenant + "/" + args.Name`},
		"Var":     {"id"},
		"Empty":   {`""`},
	}, got)
}
