package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dashboardv1alpha1 "github.com/netztronaut/cupboard/api/dashboard/v1alpha1"
)

func ResolveURLFromSource(ctx context.Context, c client.Reader, namespace string, source *dashboardv1alpha1.URLSource) (string, error) {
	if source == nil {
		return "", fmt.Errorf("urlFrom must not be nil")
	}

	if source.IngressRef != nil {
		return resolveIngressURL(ctx, c, namespace, source.IngressRef.Name)
	}
	if source.HTTPRouteRef != nil {
		return resolveHTTPRouteURL(ctx, c, namespace, source.HTTPRouteRef.Name)
	}
	if source.ServiceRef != nil {
		return resolveServiceURL(ctx, c, namespace, source.ServiceRef.Name)
	}
	if source.RouteRef != nil {
		return resolveOpenShiftRouteURL(ctx, c, namespace, source.RouteRef.Name)
	}
	if source.IngressRouteRef != nil {
		return resolveTraefikIngressRouteURL(ctx, c, namespace, source.IngressRouteRef.Name)
	}

	return "", fmt.Errorf("urlFrom must set one reference")
}

func resolveIngressURL(ctx context.Context, c client.Reader, namespace, name string) (string, error) {
	var ingress networkingv1.Ingress
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &ingress); err != nil {
		return "", fmt.Errorf("ingressRef %q: %w", name, err)
	}
	for _, rule := range ingress.Spec.Rules {
		if strings.TrimSpace(rule.Host) != "" {
			return "https://" + rule.Host, nil
		}
	}
	return "", fmt.Errorf("ingressRef %q has no host rules", name)
}

func resolveHTTPRouteURL(ctx context.Context, c client.Reader, namespace, name string) (string, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "HTTPRoute",
	})
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, obj); err != nil {
		return "", fmt.Errorf("httpRouteRef %q: %w", name, err)
	}
	hostnames, _, err := unstructured.NestedStringSlice(obj.Object, "spec", "hostnames")
	if err != nil {
		return "", fmt.Errorf("httpRouteRef %q: %w", name, err)
	}
	for _, hostname := range hostnames {
		if strings.TrimSpace(hostname) != "" {
			return "https://" + hostname, nil
		}
	}
	return "", fmt.Errorf("httpRouteRef %q has no hostnames", name)
}

func resolveServiceURL(ctx context.Context, c client.Reader, namespace, name string) (string, error) {
	var svc corev1.Service
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &svc); err != nil {
		return "", fmt.Errorf("serviceRef %q: %w", name, err)
	}
	if len(svc.Spec.Ports) == 0 {
		return "", fmt.Errorf("serviceRef %q has no ports", name)
	}
	scheme := "http"
	if svc.Spec.Ports[0].Port == 443 {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d", scheme, svc.Name, svc.Namespace, svc.Spec.Ports[0].Port), nil
}

func resolveOpenShiftRouteURL(ctx context.Context, c client.Reader, namespace, name string) (string, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "route.openshift.io",
		Version: "v1",
		Kind:    "Route",
	})
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, obj); err != nil {
		return "", fmt.Errorf("routeRef %q: %w", name, err)
	}
	host, found, err := unstructured.NestedString(obj.Object, "spec", "host")
	if err != nil {
		return "", fmt.Errorf("routeRef %q: %w", name, err)
	}
	if !found || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("routeRef %q has no spec.host", name)
	}
	return "https://" + host, nil
}

func resolveTraefikIngressRouteURL(ctx context.Context, c client.Reader, namespace, name string) (string, error) {
	for _, gv := range []schema.GroupVersion{
		{Group: "traefik.containo.us", Version: "v1alpha1"},
		{Group: "traefik.io", Version: "v1alpha1"},
	} {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gv.WithKind("IngressRoute"))
		if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, obj); err == nil {
			routes, found, nestedErr := unstructured.NestedSlice(obj.Object, "spec", "routes")
			if nestedErr != nil {
				return "", fmt.Errorf("ingressRouteRef %q: %w", name, nestedErr)
			}
			if !found || len(routes) == 0 {
				return "", fmt.Errorf("ingressRouteRef %q has no routes", name)
			}
			for _, route := range routes {
				routeMap, ok := route.(map[string]any)
				if !ok {
					continue
				}
				match, _ := routeMap["match"].(string)
				if host := extractHostFromTraefikMatch(match); host != "" {
					return "https://" + host, nil
				}
			}
			return "", fmt.Errorf("ingressRouteRef %q has no parsable host", name)
		}
	}
	return "", fmt.Errorf("ingressRouteRef %q not found", name)
}

func extractHostFromTraefikMatch(match string) string {
	start := strings.Index(match, "Host(`")
	if start < 0 {
		return ""
	}
	start += len("Host(`")
	end := strings.Index(match[start:], "`)")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(match[start : start+end])
}
