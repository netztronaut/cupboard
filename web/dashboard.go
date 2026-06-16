package web

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dashboardv1alpha1 "github.com/netztronaut/cupboard/api/dashboard/v1alpha1"
	forecastlev1alpha1 "github.com/netztronaut/cupboard/api/forecastle/v1alpha1"
	webhookdashboardv1alpha1 "github.com/netztronaut/cupboard/internal/webhook/dashboard/v1alpha1"
)

type dashboardDiscovery interface {
	ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error)
}

type dashboardCollector struct {
	reader      client.Reader
	discovery   dashboardDiscovery
	linkGroups  []LinkGroup
	staticLinks []StaticLink
}

func newDashboardCollector(reader client.Reader, discovery dashboardDiscovery, linkGroups []LinkGroup, staticLinks []StaticLink) *dashboardCollector {
	return &dashboardCollector{reader: reader, discovery: discovery, linkGroups: linkGroups, staticLinks: staticLinks}
}

var setupLog = ctrl.Log.WithName("web").WithName("dashboard")

const (
	labelEnabled = "cupboard.netztronaut.de/enabled"

	annotationGroup   = "cupboard.netztronaut.de/group"
	annotationName    = "cupboard.netztronaut.de/name"
	annotationURL     = "cupboard.netztronaut.de/url"
	annotationTarget  = "cupboard.netztronaut.de/target"
	annotationIcon    = "cupboard.netztronaut.de/icon"
	annotationIconURL = "cupboard.netztronaut.de/icon-url"

	allLinkGroupsWildcard = "\x00all-link-groups"
)

type DashboardResponse struct {
	LinkGroups []DashboardLinkGroup `json:"linkGroups"`
	Groups     []DashboardGroup     `json:"groups"`
}

type DashboardGroup struct {
	Name      string          `json:"name"`
	LinkGroup string          `json:"linkGroup,omitempty"`
	Links     []DashboardLink `json:"links"`
}

type DashboardLinkGroup struct {
	Name          string `json:"name"`
	Priority      int    `json:"priority"`
	PriorityClass string `json:"priorityClass,omitempty"`
	DisplayName   string `json:"displayName"`
}

type DashboardLink struct {
	Name      string   `json:"name"`
	LinkGroup string   `json:"linkGroup,omitempty"`
	URL       string   `json:"url"`
	Target    string   `json:"target,omitempty"`
	Icon      string   `json:"icon,omitempty"`
	Source    string   `json:"source,omitempty"`
	Metadata  string   `json:"metadata,omitempty"`
	Groups    []string `json:"groups,omitempty"`
}

func (c *dashboardCollector) collectDashboard(ctx context.Context, userGroups []string) (DashboardResponse, error) {
	groups := map[string][]DashboardLink{}
	groupDetails := c.initLinkGroups()
	c.collectStaticLinks(groups, groupDetails)

	if err := collectBookmarkGroups(ctx, c.reader, groups, groupDetails); err != nil {
		return DashboardResponse{}, err
	}
	if c.resourceAvailable(ctx, forecastlev1alpha1.GroupVersion, "ForecastleApp") {
		if err := collectForecastleApps(ctx, c.reader, groups, groupDetails); err != nil {
			return DashboardResponse{}, err
		}
	}
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "networking.k8s.io", Version: "v1"}, "Ingress") {
		if err := collectIngresses(ctx, c.reader, groups, groupDetails); err != nil {
			return DashboardResponse{}, err
		}
	}
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1"}, "HTTPRoute") {
		if err := collectHTTPRoutes(ctx, c.reader, groups, groupDetails); err != nil {
			return DashboardResponse{}, err
		}
	}
	if err := collectServices(ctx, c.reader, groups, groupDetails); err != nil {
		return DashboardResponse{}, err
	}

	orderedGroups := sortLinkGroups(groupDetails)
	response := DashboardResponse{
		LinkGroups: orderedGroups,
		Groups:     make([]DashboardGroup, 0, len(orderedGroups)),
	}
	for _, group := range orderedGroups {
		links := filterLinksForGroups(groups[group.Name], userGroups)
		if len(links) == 0 {
			continue
		}
		sort.SliceStable(links, func(i, j int) bool {
			return strings.ToLower(links[i].Name) < strings.ToLower(links[j].Name)
		})
		response.Groups = append(response.Groups, DashboardGroup{
			Name:      group.DisplayName,
			LinkGroup: group.Name,
			Links:     links,
		})
	}
	return response, nil
}

func (c *dashboardCollector) initLinkGroups() map[string]DashboardLinkGroup {
	result := map[string]DashboardLinkGroup{}
	for _, group := range c.linkGroups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		displayName := strings.TrimSpace(group.DisplayName)
		if displayName == "" {
			displayName = name
		}
		result[name] = DashboardLinkGroup{
			Name:          name,
			Priority:      group.Priority,
			PriorityClass: strings.TrimSpace(group.PriorityClass),
			DisplayName:   displayName,
		}
	}
	return result
}

func (c *dashboardCollector) collectStaticLinks(groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) {
	for _, link := range c.staticLinks {
		groupName := resolveLinkGroupName(firstNonEmpty(strings.TrimSpace(link.LinkGroup), strings.TrimSpace(link.Group)), groupDetails)
		name := strings.TrimSpace(link.Name)
		url := strings.TrimSpace(link.URL)
		if groupName == "" || name == "" || url == "" {
			continue
		}
		ensureLinkGroup(groupDetails, groupName)
		groups[groupName] = append(groups[groupName], DashboardLink{
			Name:   name,
			URL:    url,
			Target: defaultTarget(link.Target),
			Icon:   strings.TrimSpace(link.Icon),
			Source: "static",
			Groups: normalizedGroups(link.Groups),
		})
	}
}

func (c *dashboardCollector) logMissingOptionalResources(ctx context.Context) {
	type optionalResource struct {
		groupVersion schema.GroupVersion
		kind         string
	}

	for _, resource := range []optionalResource{
		{groupVersion: forecastlev1alpha1.GroupVersion, kind: "ForecastleApp"},
		{groupVersion: schema.GroupVersion{Group: "networking.k8s.io", Version: "v1"}, kind: "Ingress"},
		{groupVersion: schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1"}, kind: "HTTPRoute"},
	} {
		if !c.resourceAvailable(ctx, resource.groupVersion, resource.kind) {
			setupLog.Info("Skipping optional dashboard resource because it is unavailable", "groupVersion", resource.groupVersion.String(), "kind", resource.kind)
		}
	}
}

func (c *dashboardCollector) resourceAvailable(ctx context.Context, groupVersion schema.GroupVersion, kind string) bool {
	if c.discovery == nil {
		return false
	}
	list, err := c.discovery.ServerResourcesForGroupVersion(groupVersion.String())
	if err != nil {
		return false
	}
	for _, resource := range list.APIResources {
		if resource.Kind == kind {
			return true
		}
	}
	return false
}

func collectBookmarkGroups(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	var list dashboardv1alpha1.BookmarkGroupList
	if err := c.List(ctx, &list); err != nil {
		return err
	}
	for _, item := range list.Items {
		groupName := strings.TrimSpace(item.Spec.Name)
		if groupName == "" {
			groupName = item.Name
		}
		ensureLinkGroup(groupDetails, groupName)
		for _, link := range item.Spec.Links {
			url := strings.TrimSpace(link.URL)
			if link.URLFrom != nil {
				resolved, err := webhookdashboardv1alpha1.ResolveURLFromSource(ctx, c, item.Namespace, link.URLFrom)
				if err == nil {
					url = resolved
				}
			}
			if url == "" {
				continue
			}
			target := string(link.Target)
			if target == "" {
				target = string(dashboardv1alpha1.BookmarkLinkTargetSelf)
			}
			groups[groupName] = append(groups[groupName], DashboardLink{
				Name:   link.Name,
				URL:    url,
				Target: target,
				Icon:   link.Icon,
				Source: "bookmarkgroup",
				Groups: normalizedGroups(link.Groups),
			})
		}
	}
	return nil
}

func collectForecastleApps(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	var list forecastlev1alpha1.ForecastleAppList
	if err := c.List(ctx, &list); err != nil {
		return client.IgnoreNotFound(err)
	}

	for _, item := range list.Items {
		linkName := item.Spec.Name
		groupName := resolveLinkGroupName(item.Spec.Group, groupDetails)
		icon := item.Spec.Icon
		target := "_self"
		if v := item.Spec.Properties["target"]; strings.TrimSpace(v) != "" {
			target = v
		}

		url := item.Spec.URL
		if strings.TrimSpace(url) == "" {
			source := parseForecastleURLSource(item.Spec.URLFrom)
			if source != nil {
				resolved, resolveErr := webhookdashboardv1alpha1.ResolveURLFromSource(ctx, c, item.GetNamespace(), source)
				if resolveErr == nil {
					url = resolved
				}
			}
		}
		if strings.TrimSpace(linkName) == "" || strings.TrimSpace(groupName) == "" || strings.TrimSpace(url) == "" {
			continue
		}
		ensureLinkGroup(groupDetails, groupName)
		groups[groupName] = append(groups[groupName], DashboardLink{
			Name:   linkName,
			URL:    url,
			Target: target,
			Icon:   icon,
			Source: "forecastleapp",
			Groups: normalizedGroups(item.Spec.Groups),
		})
	}
	return nil
}

func parseForecastleURLSource(source *forecastlev1alpha1.URLSource) *dashboardv1alpha1.URLSource {
	if source == nil {
		return nil
	}
	parse := func(ref *forecastlev1alpha1.LocalObjectReference) *dashboardv1alpha1.LocalObjectReference {
		if ref == nil || strings.TrimSpace(ref.Name) == "" {
			return nil
		}
		return &dashboardv1alpha1.LocalObjectReference{Name: ref.Name}
	}
	return &dashboardv1alpha1.URLSource{
		IngressRef:      parse(source.IngressRef),
		RouteRef:        parse(source.RouteRef),
		IngressRouteRef: parse(source.IngressRouteRef),
		HTTPRouteRef:    parse(source.HTTPRouteRef),
	}
}

func resolveLinkGroupName(raw string, groupDetails map[string]DashboardLinkGroup) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ""
	}
	if _, ok := groupDetails[name]; ok {
		return name
	}
	for key, details := range groupDetails {
		if strings.EqualFold(strings.TrimSpace(details.DisplayName), name) || strings.EqualFold(key, name) {
			return key
		}
	}
	return name
}

func collectIngresses(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	var list networkingv1.IngressList
	if err := c.List(ctx, &list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return err
	}
	for _, ing := range list.Items {
		groupName := ing.GetAnnotations()[annotationGroup]
		linkName := ing.GetAnnotations()[annotationName]
		url := ing.GetAnnotations()[annotationURL]
		if strings.TrimSpace(url) == "" {
			for _, rule := range ing.Spec.Rules {
				if strings.TrimSpace(rule.Host) != "" {
					url = "https://" + rule.Host
					break
				}
			}
		}
		if groupName == "" || linkName == "" || url == "" {
			continue
		}
		ensureLinkGroup(groupDetails, groupName)
		groups[groupName] = append(groups[groupName], DashboardLink{
			Name:   linkName,
			URL:    url,
			Target: defaultTarget(ing.GetAnnotations()[annotationTarget]),
			Icon:   firstNonEmpty(ing.GetAnnotations()[annotationIcon], ing.GetAnnotations()[annotationIconURL]),
			Source: "ingress",
		})
	}
	return nil
}

func collectHTTPRoutes(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "HTTPRouteList",
	})
	if err := c.List(ctx, list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return client.IgnoreNotFound(err)
	}
	for _, route := range list.Items {
		annotations := route.GetAnnotations()
		groupName := annotations[annotationGroup]
		linkName := annotations[annotationName]
		url := annotations[annotationURL]
		if strings.TrimSpace(url) == "" {
			if hostnames, found, err := unstructured.NestedStringSlice(route.Object, "spec", "hostnames"); err == nil && found {
				for _, host := range hostnames {
					if strings.TrimSpace(host) != "" {
						url = "https://" + host
						break
					}
				}
			}
		}
		if groupName == "" || linkName == "" || url == "" {
			continue
		}
		ensureLinkGroup(groupDetails, groupName)
		groups[groupName] = append(groups[groupName], DashboardLink{
			Name:   linkName,
			URL:    url,
			Target: defaultTarget(annotations[annotationTarget]),
			Icon:   firstNonEmpty(annotations[annotationIcon], annotations[annotationIconURL]),
			Source: "httproute",
		})
	}
	return nil
}

func collectServices(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	var list corev1.ServiceList
	if err := c.List(ctx, &list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return err
	}
	for _, svc := range list.Items {
		annotations := svc.GetAnnotations()
		groupName := annotations[annotationGroup]
		linkName := annotations[annotationName]
		url := annotations[annotationURL]
		if strings.TrimSpace(url) == "" && len(svc.Spec.Ports) > 0 {
			scheme := "http"
			if svc.Spec.Ports[0].Port == 443 {
				scheme = "https"
			}
			url = fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d", scheme, svc.Name, svc.Namespace, svc.Spec.Ports[0].Port)
		}
		if groupName == "" || linkName == "" || url == "" {
			continue
		}
		ensureLinkGroup(groupDetails, groupName)
		groups[groupName] = append(groups[groupName], DashboardLink{
			Name:   linkName,
			URL:    url,
			Target: defaultTarget(annotations[annotationTarget]),
			Icon:   firstNonEmpty(annotations[annotationIcon], annotations[annotationIconURL]),
			Source: "service",
		})
	}
	return nil
}

func defaultTarget(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return string(dashboardv1alpha1.BookmarkLinkTargetSelf)
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func ensureLinkGroup(groups map[string]DashboardLinkGroup, name string) {
	if _, ok := groups[name]; ok {
		return
	}
	groups[name] = DashboardLinkGroup{
		Name:        name,
		DisplayName: name,
	}
}

func filterLinksForGroups(links []DashboardLink, userGroups []string) []DashboardLink {
	groupSet := map[string]struct{}{}
	for _, group := range userGroups {
		group = strings.TrimSpace(group)
		if group != "" {
			groupSet[group] = struct{}{}
		}
	}
	result := make([]DashboardLink, 0, len(links))
	for _, link := range links {
		if linkAllowedForGroups(link, groupSet) {
			result = append(result, link)
		}
	}
	return result
}

func linkAllowedForGroups(link DashboardLink, userGroups map[string]struct{}) bool {
	if len(link.Groups) == 0 {
		return true
	}
	if _, ok := userGroups[allLinkGroupsWildcard]; ok {
		return true
	}
	for _, requiredGroup := range link.Groups {
		if _, ok := userGroups[requiredGroup]; ok {
			return true
		}
	}
	return false
}

func sortLinkGroups(groups map[string]DashboardLinkGroup) []DashboardLinkGroup {
	result := make([]DashboardLinkGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, group)
	}
	slices.SortStableFunc(result, func(a, b DashboardLinkGroup) int {
		if rankA, rankB := priorityClassRank(a.PriorityClass), priorityClassRank(b.PriorityClass); rankA != rankB {
			return rankA - rankB
		}
		if a.Priority != b.Priority {
			return a.Priority - b.Priority
		}
		if dA, dB := strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName); dA != dB {
			if dA < dB {
				return -1
			}
			return 1
		}
		if nA, nB := strings.ToLower(a.Name), strings.ToLower(b.Name); nA != nB {
			if nA < nB {
				return -1
			}
			return 1
		}
		return 0
	})
	return result
}

func priorityClassRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "first":
		return 0
	case "last":
		return 2
	default:
		return 1
	}
}
