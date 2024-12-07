// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package resourcegroup

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/awslabs/kro/api/v1alpha1"
	"github.com/awslabs/kro/internal/graph"
	"github.com/awslabs/kro/internal/metadata"
	kroclient "github.com/awslabs/kro/pkg/client"
	"github.com/awslabs/kro/pkg/dynamiccontroller"
)

//+kubebuilder:rbac:groups=kro.run,resources=resourcegroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kro.run,resources=resourcegroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kro.run,resources=resourcegroups/finalizers,verbs=update

// ResourceGroupReconciler reconciles a ResourceGroup object
type ResourceGroupReconciler struct {
	log        logr.Logger
	rootLogger logr.Logger

	allowCRDDeletion bool

	client.Client
	clientSet  *kroclient.Set
	crdManager kroclient.CRDClient

	metadataLabeler   metadata.Labeler
	rgBuilder         *graph.Builder
	dynamicController *dynamiccontroller.DynamicController
}

func NewResourceGroupReconciler(
	log logr.Logger,
	mgrClient client.Client,
	clientSet *kroclient.Set,
	allowCRDDeletion bool,
	dynamicController *dynamiccontroller.DynamicController,
	builder *graph.Builder,
) *ResourceGroupReconciler {
	crdWrapper := clientSet.CRD(kroclient.CRDWrapperConfig{
		Log: log,
	})
	rgLogger := log.WithName("controller.resourceGroup")

	return &ResourceGroupReconciler{
		rootLogger:        log,
		log:               rgLogger,
		clientSet:         clientSet,
		Client:            mgrClient,
		allowCRDDeletion:  allowCRDDeletion,
		crdManager:        crdWrapper,
		dynamicController: dynamicController,
		metadataLabeler:   metadata.NewKroMetaLabeler("0.1.0", "kro-pod"),
		rgBuilder:         builder,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ResourceGroup{}).
		Complete(reconcile.AsReconciler[*v1alpha1.ResourceGroup](mgr.GetClient(), r))
}

func (r *ResourceGroupReconciler) Reconcile(ctx context.Context, resourcegroup *v1alpha1.ResourceGroup) (ctrl.Result, error) {
	rlog := r.log.WithValues("resourcegroup", types.NamespacedName{Namespace: resourcegroup.Namespace, Name: resourcegroup.Name})
	ctx = log.IntoContext(ctx, rlog)

	if !resourcegroup.DeletionTimestamp.IsZero() {
		rlog.V(1).Info("ResourceGroup is being deleted")
		if err := r.cleanupResourceGroup(ctx, resourcegroup); err != nil {
			return ctrl.Result{}, err
		}

		rlog.V(1).Info("Setting resourcegroup as unmanaged")
		if err := r.setUnmanaged(ctx, resourcegroup); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	rlog.V(1).Info("Setting resource group as managed")
	if err := r.setManaged(ctx, resourcegroup); err != nil {
		return ctrl.Result{}, err
	}

	rlog.V(1).Info("Syncing resourcegroup")
	topologicalOrder, resourcesInformation, reconcileErr := r.reconcileResourceGroup(ctx, resourcegroup)

	rlog.V(1).Info("Setting resourcegroup status")
	if err := r.setResourceGroupStatus(ctx, resourcegroup, topologicalOrder, resourcesInformation, reconcileErr); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
