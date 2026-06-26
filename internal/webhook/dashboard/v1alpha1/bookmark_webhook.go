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

package v1alpha1

import (
	"context"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dashboardv1alpha1 "netztronaut.de/cupboard/api/dashboard/v1alpha1"
)

// nolint:unused
var bookmarklog = logf.Log.WithName("bookmark-resource")

// SetupBookmarkWebhookWithManager registers the webhook for Bookmark in the manager.
func SetupBookmarkWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &dashboardv1alpha1.Bookmark{}).
		WithValidator(&BookmarkCustomValidator{
			client: mgr.GetClient(),
		}).
		Complete()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: If you want to customise the 'path', use the flags '--defaulting-path' or '--validation-path'.
// +kubebuilder:webhook:path=/validate-dashboard-netztronaut-de-v1alpha1-bookmark,mutating=false,failurePolicy=fail,sideEffects=None,groups=dashboard.netztronaut.de,resources=bookmarks,verbs=create;update,versions=v1alpha1,name=vbookmark-v1alpha1.kb.io,admissionReviewVersions=v1

// BookmarkCustomValidator struct is responsible for validating the Bookmark resource
// when it is created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type BookmarkCustomValidator struct {
	client client.Client //nolint:unused
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *BookmarkCustomValidator) ValidateCreate(ctx context.Context, obj *dashboardv1alpha1.Bookmark) (admission.Warnings, error) {
	bookmarklog.Info("Validating Bookmark creation", "name", obj.Name)
	return v.validateBookmark(ctx, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *BookmarkCustomValidator) ValidateUpdate(ctx context.Context, _ *dashboardv1alpha1.Bookmark, newObj *dashboardv1alpha1.Bookmark) (admission.Warnings, error) {
	bookmarklog.Info("Validating Bookmark update", "name", newObj.Name)
	return v.validateBookmark(ctx, newObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *BookmarkCustomValidator) ValidateDelete(_ context.Context, obj *dashboardv1alpha1.Bookmark) (admission.Warnings, error) {
	bookmarklog.Info("Validating Bookmark deletion", "name", obj.Name)
	return nil, nil
}

func (v *BookmarkCustomValidator) validateBookmark(_ context.Context, bookmark *dashboardv1alpha1.Bookmark) (admission.Warnings, error) {
	hasURL := strings.TrimSpace(bookmark.Spec.URL) != ""
	hasURLFrom := bookmark.Spec.URLFrom != nil

	if hasURL && hasURLFrom {
		return nil, bookmarkValidationError(bookmark, "spec.url and spec.urlFrom are mutually exclusive")
	}
	if !hasURL && !hasURLFrom {
		return nil, bookmarkValidationError(bookmark, "one of spec.url or spec.urlFrom must be set")
	}

	if hasURL {
		if err := validateHTTPURL(bookmark.Spec.URL); err != nil {
			return nil, bookmarkValidationError(bookmark, "spec.url: "+err.Error())
		}
	}

	if err := validateTarget(bookmark.Spec.Target); err != nil {
		return nil, bookmarkValidationError(bookmark, "spec.target: "+err.Error())
	}

	return nil, nil
}

func bookmarkValidationError(obj *dashboardv1alpha1.Bookmark, msg string) error {
	validationErrs := field.ErrorList{
		field.Invalid(field.NewPath("spec"), obj.Spec, msg),
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: dashboardv1alpha1.GroupVersion.Group, Kind: "Bookmark"},
		obj.Name,
		validationErrs,
	)
}
