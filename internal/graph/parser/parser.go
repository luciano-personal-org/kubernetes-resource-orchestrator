// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//	http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package parser

import (
	"fmt"
	"strings"

	"k8s.io/kube-openapi/pkg/validation/spec"

	"github.com/awslabs/kro/internal/graph/variable"
)

const (
	xKubernetesPreserveUnknownFields = "x-kubernetes-preserve-unknown-fields"
)

// ParseResource extracts CEL expressions from a resource based on
// the schema. The resource is expected to be a map[string]interface{}.
//
// Note that this function will also validate the resource against the schema
// and return an error if the resource does not match the schema. When CEL
// expressions are found, they are extracted and returned with the expected
// type of the field (inferred from the schema).
func ParseResource(resource map[string]interface{}, resourceSchema *spec.Schema) ([]variable.FieldDescriptor, error) {
	return parseResource(resource, resourceSchema, "")
}

// parseResource is a helper function that recursively extracts CEL expressions
// from a resource. It uses a depthh first search to traverse the resource and
// extract expressions from string fields
func parseResource(resource interface{}, schema *spec.Schema, path string) ([]variable.FieldDescriptor, error) {
	if err := validateSchema(schema, path); err != nil {
		return nil, err
	}

	expectedType := getExpectedType(schema)

	switch field := resource.(type) {
	case map[string]interface{}:
		return parseObject(field, schema, path, expectedType)
	case []interface{}:
		return parseArray(field, schema, path, expectedType)
	case string:
		return parseString(field, schema, path, expectedType)
	case nil:
		return nil, nil
	default:
		return parseScalarTypes(field, schema, path, expectedType)
	}
}

func validateSchema(schema *spec.Schema, path string) error {
	if schema == nil {
		return fmt.Errorf("schema is nil for path %s", path)
	}
	if len(schema.Type) != 1 {
		if len(schema.OneOf) > 0 {
			schema.Type = []string{schema.OneOf[0].Type[0]}
		} else {
			return fmt.Errorf("found schema type that is not a single type: %v", schema.Type)
		}
	}
	return nil
}

func getExpectedType(schema *spec.Schema) string {
	if schema.Type[0] != "" {
		return schema.Type[0]
	}
	if schema.AdditionalProperties != nil && schema.AdditionalProperties.Allows {
		// NOTE(a-hilaly): I don't like the type "any", we might want to change this to "object"
		// in the future; just haven't really thought about it yet.
		// Basically "any" means that the field can be of any type, and we have to check
		// the ExpectedSchema field.
		return "any"
	}
	return ""
}

func parseObject(field map[string]interface{}, schema *spec.Schema, path, expectedType string) ([]variable.FieldDescriptor, error) {
	if expectedType != "object" && (schema.AdditionalProperties == nil || !schema.AdditionalProperties.Allows) {
		return nil, fmt.Errorf("expected object type or AdditionalProperties allowed for path %s, got %v", path, field)
	}

	// Look for vendor schema extensions first
	if len(schema.VendorExtensible.Extensions) > 0 {
		// If the schema has the x-kubernetes-preserve-unknown-fields extension, we should not
		// parse the object and return an empty list of expressions.
		if enabled, ok := schema.VendorExtensible.Extensions[xKubernetesPreserveUnknownFields]; ok && enabled.(bool) {
			return nil, nil
		}
	}

	var expressionsFields []variable.FieldDescriptor
	for fieldName, value := range field {
		fieldSchema, err := getFieldSchema(schema, fieldName)
		if err != nil {
			return nil, fmt.Errorf("error getting field schema for path %s: %v", path+"."+fieldName, err)
		}
		fieldPath := joinPathAndFieldName(path, fieldName)
		fieldExpressions, err := parseResource(value, fieldSchema, fieldPath)
		if err != nil {
			return nil, err
		}
		expressionsFields = append(expressionsFields, fieldExpressions...)
	}
	return expressionsFields, nil
}

func parseArray(field []interface{}, schema *spec.Schema, path, expectedType string) ([]variable.FieldDescriptor, error) {
	if expectedType != "array" {
		return nil, fmt.Errorf("expected array type for path %s, got %v", path, field)
	}

	itemSchema, err := getArrayItemSchema(schema, path)
	if err != nil {
		return nil, err
	}

	var expressionsFields []variable.FieldDescriptor
	for i, item := range field {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		itemExpressions, err := parseResource(item, itemSchema, itemPath)
		if err != nil {
			return nil, err
		}
		expressionsFields = append(expressionsFields, itemExpressions...)
	}
	return expressionsFields, nil
}

func parseString(field string, schema *spec.Schema, path, expectedType string) ([]variable.FieldDescriptor, error) {
	ok, err := isStandaloneExpression(field)
	if err != nil {
		return nil, err
	}
	if ok {
		return []variable.FieldDescriptor{{
			Expressions:          []string{strings.Trim(field, "${}")},
			ExpectedType:         expectedType,
			ExpectedSchema:       schema,
			Path:                 path,
			StandaloneExpression: true,
		}}, nil
	}

	if expectedType != "string" && expectedType != "any" {
		return nil, fmt.Errorf("expected string type or AdditionalProperties for path %s, got %v", path, field)
	}

	expressions, err := extractExpressions(field)
	if err != nil {
		return nil, err
	}
	if len(expressions) > 0 {
		return []variable.FieldDescriptor{{
			Expressions:  expressions,
			ExpectedType: expectedType,
			Path:         path,
		}}, nil
	}
	return nil, nil
}

func parseScalarTypes(field interface{}, _ *spec.Schema, path, expectedType string) ([]variable.FieldDescriptor, error) {
	if expectedType == "any" {
		return nil, nil
	}
	// perform type checks for scalar types
	switch expectedType {
	case "number":
		if _, ok := field.(float64); !ok {
			return nil, fmt.Errorf("expected number type for path %s, got %T", path, field)
		}
	case "integer":
		if !isInteger(field) {
			return nil, fmt.Errorf("expected integer type for path %s, got %T", path, field)
		}
	case "boolean":
		if _, ok := field.(bool); !ok {
			return nil, fmt.Errorf("expected boolean type for path %s, got %T", path, field)
		}
	default:
		return nil, fmt.Errorf("unexpected type for path %s: %T", path, field)
	}
	return nil, nil
}

func getFieldSchema(schema *spec.Schema, field string) (*spec.Schema, error) {
	if schema.Properties != nil {
		if fieldSchema, ok := schema.Properties[field]; ok {
			return &fieldSchema, nil
		}
	}

	if schema.AdditionalProperties != nil {
		if schema.AdditionalProperties.Schema != nil {
			return schema.AdditionalProperties.Schema, nil
		} else if schema.AdditionalProperties.Allows {
			return &spec.Schema{}, nil
		}
	}

	return nil, fmt.Errorf("schema not found for field %s", field)
}

func getArrayItemSchema(schema *spec.Schema, path string) (*spec.Schema, error) {
	if schema.Items != nil && schema.Items.Schema != nil {
		return schema.Items.Schema, nil
	}
	if schema.Items != nil && schema.Items.Schema != nil && len(schema.Items.Schema.Properties) > 0 {
		return &spec.Schema{
			SchemaProps: spec.SchemaProps{
				Type:       []string{"object"},
				Properties: schema.Properties,
			},
		}, nil
	}
	return nil, fmt.Errorf("invalid array schema for path %s: neither Items.Schema nor Properties are defined", path)
}

func isInteger(v interface{}) bool {
	switch v.(type) {
	case int, int64, int32:
		return true
	default:
		return false
	}
}

// joinPathAndField appends a field name to a path. If the fieldName contains
// a dot or is empty, the path will be appended using ["fieldName"] instead of
// .fieldName to avoid ambiguity and simplify parsing back the path.
func joinPathAndFieldName(path, fieldName string) string {
	if fieldName == "" || strings.Contains(fieldName, ".") {
		return fmt.Sprintf("%s[%q]", path, fieldName)
	}
	if path == "" {
		return fieldName
	}
	return fmt.Sprintf("%s.%s", path, fieldName)
}
