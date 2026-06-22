/*
Copyright 2026 steigr <me@stei.gr>.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dashboard

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dashboardv1alpha1 "netztronaut.de/cupboard/api/dashboard/v1alpha1"
)

// BookmarkGroupReconciler reconciles a BookmarkGroup object
type BookmarkGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarkgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarkgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarkgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarks,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BookmarkGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var group dashboardv1alpha1.BookmarkGroup
	if err := r.Get(ctx, req.NamespacedName, &group); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	group.Status.LinkCount = int32(len(group.Spec.Links))
	now := metav1.NewTime(time.Now())
	group.Status.LastSyncedAt = &now
	if err := r.Status().Update(ctx, &group); err != nil {
		log.Error(err, "unable to update BookmarkGroup status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BookmarkGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dashboardv1alpha1.BookmarkGroup{}).
		Named("dashboard-bookmarkgroup").
		Complete(r)
}
