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
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dashboardv1alpha1 "netztronaut.de/cupboard/api/dashboard/v1alpha1"
)

const urlCheckInterval = 5 * time.Minute

// BookmarkReconciler reconciles a Bookmark object
type BookmarkReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
}

// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarks/finalizers,verbs=update
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=bookmarkgroups,verbs=get;list;watch

func (r *BookmarkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var bookmark dashboardv1alpha1.Bookmark
	if err := r.Get(ctx, req.NamespacedName, &bookmark); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check URL reachability if due.
	needsURLCheck := bookmark.Status.LastURLCheckAt == nil ||
		time.Since(bookmark.Status.LastURLCheckAt.Time) >= urlCheckInterval

	if needsURLCheck {
		resolvedURL, reachable, checkErr := r.resolveAndCheckURL(ctx, &bookmark)
		now := metav1.Now()
		bookmark.Status.LastURLCheckAt = &now
		if checkErr != nil {
			reachableVal := false
			bookmark.Status.URLReachable = &reachableVal
			bookmark.Status.URLCheckError = checkErr.Error()
			log.Info("URL not reachable", "url", resolvedURL, "error", checkErr.Error())
		} else {
			bookmark.Status.URLReachable = &reachable
			bookmark.Status.URLCheckError = ""
		}
	}

	bookmark.Status.LastSyncedAt = &metav1.Time{Time: time.Now()}
	if err := r.Status().Update(ctx, &bookmark); err != nil {
		log.Error(err, "Unable to update Bookmark status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: urlCheckInterval}, nil
}

// resolveAndCheckURL resolves the bookmark URL (from spec.url or spec.urlFrom) and checks reachability.
func (r *BookmarkReconciler) resolveAndCheckURL(ctx context.Context, bookmark *dashboardv1alpha1.Bookmark) (string, bool, error) {
	var rawURL string

	if strings.TrimSpace(bookmark.Spec.URL) != "" {
		rawURL = bookmark.Spec.URL
	} else if bookmark.Spec.URLFrom != nil {
		resolved, err := resolveURLFromSource(ctx, r.Client, bookmark.Namespace, bookmark.Spec.URLFrom)
		if err != nil {
			return "", false, err
		}
		rawURL = resolved
	} else {
		return "", false, nil
	}

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, http.NoBody)
	if err != nil {
		return rawURL, false, err
	}
	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode == http.StatusMethodNotAllowed {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
		if reqErr != nil {
			return rawURL, false, reqErr
		}
		resp, err = httpClient.Do(req)
		if err != nil {
			return rawURL, false, err
		}
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode >= http.StatusBadRequest {
		return rawURL, false, &urlCheckError{statusCode: resp.StatusCode}
	}
	return rawURL, true, nil
}

type urlCheckError struct {
	statusCode int
}

func (e *urlCheckError) Error() string {
	return "URL returned HTTP " + http.StatusText(e.statusCode)
}

// resolveURLFromSource resolves a URL from a Kubernetes resource reference.
func resolveURLFromSource(ctx context.Context, c client.Reader, namespace string, source *dashboardv1alpha1.URLSource) (string, error) {
	if source == nil {
		return "", nil
	}
	if source.IngressRef != nil {
		var ingress networkingv1.Ingress
		if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: source.IngressRef.Name}, &ingress); err != nil {
			return "", fmt.Errorf("ingressRef %q: %w", source.IngressRef.Name, err)
		}
		for _, rule := range ingress.Spec.Rules {
			if strings.TrimSpace(rule.Host) != "" {
				return "https://" + rule.Host, nil
			}
		}
		return "", fmt.Errorf("ingressRef %q has no host rules", source.IngressRef.Name)
	}
	// Other sources (HTTPRoute, Service, Route, IngressRoute) are not resolved here.
	// Return empty string — the bookmark will be shown once URL is resolvable.
	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BookmarkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dashboardv1alpha1.Bookmark{}).
		Named("dashboard-bookmark").
		Complete(r)
}
