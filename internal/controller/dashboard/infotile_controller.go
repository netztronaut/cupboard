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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dashboardv1alpha1 "netztronaut.de/cupboard/api/dashboard/v1alpha1"
)

const defaultInfoTileInterval = 60 * time.Second

// DashboardNotifyFunc is called after a tile's content is updated so the
// dashboard WebSocket clients receive a live-update push.
type DashboardNotifyFunc func()

// InfoTileReconciler reconciles InfoTile objects.
type InfoTileReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
	Notify     DashboardNotifyFunc
}

// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=infotiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=infotiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dashboard.netztronaut.de,namespace=cupboard-system,resources=infotiles/finalizers,verbs=update

func (r *InfoTileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var tile dashboardv1alpha1.InfoTile
	if err := r.Get(ctx, req.NamespacedName, &tile); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	interval := defaultInfoTileInterval
	if tile.Spec.Interval != nil && tile.Spec.Interval.Duration > 0 {
		interval = tile.Spec.Interval.Duration
	}

	content, fetchErr := r.renderContent(ctx, &tile)

	now := metav1.Now()
	tile.Status.LastFetchedAt = &now
	if fetchErr != nil {
		log.Error(fetchErr, "InfoTile fetch/render failed", "tile", req.NamespacedName)
		tile.Status.LastError = fetchErr.Error()
	} else {
		tile.Status.LastError = ""
		if tile.Status.Content != content {
			tile.Status.Content = content
			if r.Notify != nil {
				r.Notify()
			}
		}
	}

	if err := r.Status().Update(ctx, &tile); err != nil {
		log.Error(err, "Unable to update InfoTile status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *InfoTileReconciler) renderContent(ctx context.Context, tile *dashboardv1alpha1.InfoTile) (string, error) {
	respCtx := processorContext{}

	if tile.Spec.HTTPRequest != nil {
		code, body, data, err := r.doHTTPRequest(ctx, tile.Spec.HTTPRequest)
		if err != nil {
			return "", fmt.Errorf("http request: %w", err)
		}
		respCtx.Response = processorResponse{
			Code: code,
			Body: body,
			Data: data,
		}
	}

	if tile.Spec.Processor == "" {
		return respCtx.Response.Body, nil
	}

	tmpl, err := template.New("processor").Funcs(sprig.TxtFuncMap()).Parse(tile.Spec.Processor)
	if err != nil {
		return "", fmt.Errorf("parse processor template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, respCtx); err != nil {
		return "", fmt.Errorf("execute processor template: %w", err)
	}
	return buf.String(), nil
}

func (r *InfoTileReconciler) doHTTPRequest(ctx context.Context, req *dashboardv1alpha1.InfoTileHTTPRequest) (int, string, any, error) {
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	rawURL := req.URL
	if len(req.QueryParams) > 0 {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return 0, "", nil, fmt.Errorf("parse URL %q: %w", rawURL, err)
		}
		q := parsed.Query()
		for _, p := range req.QueryParams {
			q.Add(p.Name, p.Value)
		}
		parsed.RawQuery = q.Encode()
		rawURL = parsed.String()
	}

	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = strings.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return 0, "", nil, fmt.Errorf("build request: %w", err)
	}
	for _, h := range req.Headers {
		httpReq.Header.Add(h.Name, h.Value)
	}

	hc := r.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}

	resp, err := hc.Do(httpReq)
	if err != nil {
		return 0, "", nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", nil, fmt.Errorf("read response body: %w", err)
	}

	body := string(rawBody)
	var data any
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		if err := json.Unmarshal(rawBody, &data); err != nil {
			// non-fatal: data stays nil
			data = nil
		}
	}

	return resp.StatusCode, body, data, nil
}

// processorContext is the template rendering context passed to Processor templates.
type processorContext struct {
	Response processorResponse
}

type processorResponse struct {
	Code int
	Body string
	Data any
}

func (r *InfoTileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dashboardv1alpha1.InfoTile{}).
		Named("dashboard-infotile").
		Complete(r)
}
