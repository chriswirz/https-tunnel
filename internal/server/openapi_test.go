package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// The specification is served to clients, rendered by the Swagger page and used
// to generate the TypeScript client, so a malformed document is worth catching
// here rather than in a browser.
func loadSpec(t *testing.T) map[string]any {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(openapiJSON, &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	return spec
}

func TestOpenAPIDocumentIsWellFormed(t *testing.T) {
	spec := loadSpec(t)

	version, _ := spec["openapi"].(string)
	if !strings.HasPrefix(version, "3.") {
		t.Errorf("openapi version is %q, want a 3.x document", version)
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("the document has no paths")
	}
	for path, item := range paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("path %q does not start with a slash", path)
		}
		operations, ok := item.(map[string]any)
		if !ok {
			t.Errorf("path %q has no operations", path)
			continue
		}
		for method, op := range operations {
			if method == "parameters" {
				continue
			}
			operation, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := operation["operationId"]; !ok {
				t.Errorf("%s %s has no operationId, which every generator needs", strings.ToUpper(method), path)
			}
			if _, ok := operation["responses"]; !ok {
				t.Errorf("%s %s documents no responses", strings.ToUpper(method), path)
			}
		}
	}

	if _, ok := spec["servers"].([]any); !ok {
		t.Error("the document has no servers entry")
	}
}

// A $ref that points at nothing renders as an empty box in Swagger UI and fails
// code generation outright, and neither failure says which reference broke.
func TestOpenAPIReferencesResolve(t *testing.T) {
	spec := loadSpec(t)

	var checked int
	var walk func(node any, where string)
	walk = func(node any, where string) {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok {
						t.Errorf("%s: $ref is not a string", where)
						continue
					}
					checked++
					if err := resolveRef(spec, ref); err != nil {
						t.Errorf("%s: %v", where, err)
					}
					continue
				}
				walk(child, where+"/"+key)
			}
		case []any:
			for i, child := range v {
				walk(child, where+"/"+itoa(i))
			}
		}
	}
	walk(spec, "")

	if checked == 0 {
		t.Error("no $ref found, so this test is not checking anything")
	}
	t.Logf("resolved %d references", checked)
}

// resolveRef follows a local JSON pointer such as #/components/schemas/Session.
func resolveRef(spec map[string]any, ref string) error {
	pointer, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return &refError{ref, "only local references are used in this document"}
	}
	node := any(spec)
	for _, part := range strings.Split(pointer, "/") {
		object, ok := node.(map[string]any)
		if !ok {
			return &refError{ref, "part " + part + " is not an object"}
		}
		next, ok := object[part]
		if !ok {
			return &refError{ref, "no such member: " + part}
		}
		node = next
	}
	return nil
}

type refError struct{ ref, why string }

func (e *refError) Error() string { return "$ref " + e.ref + " does not resolve: " + e.why }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for ; i > 0; i /= 10 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
	}
	return string(digits)
}

// Every scheme named in a security requirement has to be declared, or the
// Authorize button in Swagger UI silently offers nothing.
func TestOpenAPISecuritySchemesAreDeclared(t *testing.T) {
	spec := loadSpec(t)

	components, _ := spec["components"].(map[string]any)
	declared, _ := components["securitySchemes"].(map[string]any)
	if len(declared) == 0 {
		t.Fatal("no security schemes are declared")
	}

	check := func(where string, requirements any) {
		list, ok := requirements.([]any)
		if !ok {
			return
		}
		for _, requirement := range list {
			entry, ok := requirement.(map[string]any)
			if !ok {
				continue
			}
			for name := range entry {
				if _, ok := declared[name]; !ok {
					t.Errorf("%s requires security scheme %q, which is not declared", where, name)
				}
			}
		}
	}

	check("the document", spec["security"])
	paths, _ := spec["paths"].(map[string]any)
	for path, item := range paths {
		operations, _ := item.(map[string]any)
		for method, op := range operations {
			operation, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if security, ok := operation["security"]; ok {
				check(strings.ToUpper(method)+" "+path, security)
			}
		}
	}
}
