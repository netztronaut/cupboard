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
	"fmt"
	"net/url"

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
// log is for logging in this package.
var bookmarkgrouplog = logf.Log.WithName("bookmarkgroup-resource")

// SetupBookmarkGroupWebhookWithManager registers the webhook for BookmarkGroup in the manager.
func SetupBookmarkGroupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &dashboardv1alpha1.BookmarkGroup{}).
		WithValidator(&BookmarkGroupCustomValidator{
			client: mgr.GetClient(),
		}).
		Complete()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: If you want to customise the 'path', use the flags '--defaulting-path' or '--validation-path'.
// +kubebuilder:webhook:path=/validate-dashboard-netztronaut-de-v1alpha1-bookmarkgroup,mutating=false,failurePolicy=fail,sideEffects=None,groups=dashboard.netztronaut.de,resources=bookmarkgroups,verbs=create;update,versions=v1alpha1,name=vbookmarkgroup-v1alpha1.kb.io,admissionReviewVersions=v1

// BookmarkGroupCustomValidator struct is responsible for validating the BookmarkGroup resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type BookmarkGroupCustomValidator struct {
	client client.Client //nolint:unused
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type BookmarkGroup.
func (v *BookmarkGroupCustomValidator) ValidateCreate(ctx context.Context, obj *dashboardv1alpha1.BookmarkGroup) (admission.Warnings, error) {
	bookmarkgrouplog.Info("Validation for BookmarkGroup upon creation", "name", obj.GetName())
	return nil, v.validateBookmarkGroup(ctx, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type BookmarkGroup.
func (v *BookmarkGroupCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *dashboardv1alpha1.BookmarkGroup) (admission.Warnings, error) {
	bookmarkgrouplog.Info("Validation for BookmarkGroup upon update", "name", newObj.GetName())
	return nil, v.validateBookmarkGroup(ctx, newObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type BookmarkGroup.
func (v *BookmarkGroupCustomValidator) ValidateDelete(_ context.Context, obj *dashboardv1alpha1.BookmarkGroup) (admission.Warnings, error) {
	bookmarkgrouplog.Info("Validation for BookmarkGroup upon deletion", "name", obj.GetName())
	return nil, nil
}

func (v *BookmarkGroupCustomValidator) validateBookmarkGroup(_ context.Context, _ *dashboardv1alpha1.BookmarkGroup) error {
	return nil
}

func validateHTTPURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	return nil
}

func validateTarget(target dashboardv1alpha1.BookmarkLinkTarget) error {
	if target == "" ||
		target == dashboardv1alpha1.BookmarkLinkTargetSelf ||
		target == dashboardv1alpha1.BookmarkLinkTargetBlank ||
		target == dashboardv1alpha1.BookmarkLinkTargetParent ||
		target == dashboardv1alpha1.BookmarkLinkTargetTop {
		return nil
	}
	return fmt.Errorf("unsupported target %q", target)
}

func fieldValidationError(obj *dashboardv1alpha1.BookmarkGroup, msg string) error {
	validationErrs := field.ErrorList{
		field.Invalid(field.NewPath("spec"), obj.Spec, msg),
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: dashboardv1alpha1.GroupVersion.Group, Kind: "BookmarkGroup"},
		obj.Name,
		validationErrs,
	)
}
