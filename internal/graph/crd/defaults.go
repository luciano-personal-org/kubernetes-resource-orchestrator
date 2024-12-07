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

package crd

import (
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

var (
	defaultStateType = extv1.JSONSchemaProps{
		Type: "string",
	}
	defaultConditionsType = extv1.JSONSchemaProps{
		Type: "array",
		Items: &extv1.JSONSchemaPropsOrArray{
			Schema: &extv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]extv1.JSONSchemaProps{
					"type": {
						Type: "string", // Boolean maybe?
					},
					"status": {
						Type: "string",
					},
					"reason": {
						Type: "string",
					},
					"message": {
						Type: "string",
					},
					"lastTransitionTime": {
						Type: "string",
					},
					"observedGeneration": {
						Type: "integer",
					},
				},
			},
		},
	}
	// additionalPrinterColumns specifies additional columns returned in Table output.
	// See https://kubernetes.io/docs/reference/using-api/api-concepts/#receiving-resources-as-tables for details.
	// Sample output for `kubectl get clusters`
	//
	// NAME            STATE    SYNCED   AGE
	// testcluster29   ACTIVE   True     22d
	defaultAdditionalPrinterColumns = []extv1.CustomResourceColumnDefinition{
		// ResourceGroup instance state
		{
			Name:        "State",
			Description: "The state of a ResourceGroup instance",
			Priority:    0,
			Type:        "string",
			JSONPath:    ".status.state",
		},
		// ResourceGroup instance AllResourcesReady condition
		{
			Name:        "Synced",
			Description: "Whether a ResourceGroup instance have all it's subresources ready",
			Priority:    0,
			Type:        "string",
			JSONPath:    ".status.conditions[?(@.type==\"InstanceSynced\")].status",
		},
		// ResourceGroup instance age
		{
			Name:        "Age",
			Description: "Age of the resource",
			Priority:    0,
			Type:        "date",
			JSONPath:    ".metadata.creationTimestamp",
		},
	}
)
