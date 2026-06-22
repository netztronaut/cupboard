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

// BookmarkReconciler reconciles a Bookmark object
type BookmarkReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarks/finalizers,verbs=update
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarkgroups,verbs=get;list;watch;create;update;patch

func (r *BookmarkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var bookmark dashboardv1alpha1.Bookmark
	if err := r.Get(ctx, req.NamespacedName, &bookmark); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	groupName := bookmark.Spec.Group

	var group dashboardv1alpha1.BookmarkGroup
	err := r.Get(ctx, client.ObjectKey{
		Namespace: bookmark.Namespace,
		Name:      groupName,
	}, &group)

	if errors.IsNotFound(err) {
		group = dashboardv1alpha1.BookmarkGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      groupName,
				Namespace: bookmark.Namespace,
			},
			Spec: dashboardv1alpha1.BookmarkGroupSpec{
				Replicate: bookmark.Spec.Replicate,
			},
		}
		if err := r.Create(ctx, &group); err != nil {
			log.Error(err, "failed to create BookmarkGroup", "groupName", groupName)
			return ctrl.Result{}, err
		}
		log.Info("Created BookmarkGroup dynamically", "groupName", groupName)
	} else if err != nil {
		log.Error(err, "failed to get BookmarkGroup", "groupName", groupName)
		return ctrl.Result{}, err
	} else if bookmark.Spec.Replicate != group.Spec.Replicate {
		// A Bookmark turning replicate on/off should propagate to its group,
		// but only when this is the sole Bookmark that owns the group.  When
		// other Bookmarks share the same group we leave Replicate as-is —
		// any Bookmark setting it to true takes precedence.
		if bookmark.Spec.Replicate || !anyOtherBookmarkReplicates(ctx, r.Client, bookmark, groupName) {
			group.Spec.Replicate = bookmark.Spec.Replicate
			if err := r.Update(ctx, &group); err != nil {
				log.Error(err, "failed to update BookmarkGroup Replicate", "groupName", groupName)
				return ctrl.Result{}, err
			}
		}
	}

	bookmark.Status.LastSyncedAt = &metav1.Time{Time: time.Now()}
	if err := r.Status().Update(ctx, &bookmark); err != nil {
		log.Error(err, "unable to update Bookmark status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *BookmarkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dashboardv1alpha1.Bookmark{}).
		Named("dashboard-bookmark").
		Complete(r)
}

// anyOtherBookmarkReplicates returns true if any Bookmark other than the given
// one in the same namespace shares the groupName and has Replicate=true.
// Used to avoid lowering Replicate on a group that another Bookmark still wants replicated.
func anyOtherBookmarkReplicates(ctx context.Context, c client.Reader, self dashboardv1alpha1.Bookmark, groupName string) bool {
	var list dashboardv1alpha1.BookmarkList
	if err := c.List(ctx, &list, client.InNamespace(self.Namespace)); err != nil {
		return false
	}
	for _, b := range list.Items {
		if b.Name == self.Name {
			continue
		}
		if b.Spec.Group == groupName && b.Spec.Replicate {
			return true
		}
	}
	return false
}
