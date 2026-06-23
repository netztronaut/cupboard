package web

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dashboardv1alpha1 "netztronaut.de/cupboard/api/dashboard/v1alpha1"
	forecastlev1alpha1 "netztronaut.de/cupboard/api/forecastle/v1alpha1"
	"netztronaut.de/cupboard/internal/foreigncluster"
	webhookdashboardv1alpha1 "netztronaut.de/cupboard/internal/webhook/dashboard/v1alpha1"
)

type dashboardDiscovery interface {
	ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error)
}

type dashboardCollector struct {
	reader             client.Reader
	discovery          dashboardDiscovery
	forecastleInstance string
	linkGroups         []LinkGroup
	staticLinks        []StaticLink
	syncClient         *SyncClient
	foreignClusters    *foreigncluster.Manager
}

func newDashboardCollector(reader client.Reader, discovery dashboardDiscovery, options Options, syncClient *SyncClient, foreignClusters *foreigncluster.Manager) *dashboardCollector {
	return &dashboardCollector{
		reader:             reader,
		discovery:          discovery,
		forecastleInstance: strings.TrimSpace(options.Forecastle.Instance),
		linkGroups:         options.LinkGroups,
		staticLinks:        options.StaticLinks,
		syncClient:         syncClient,
		foreignClusters:    foreignClusters,
	}
}

var setupLog = ctrl.Log.WithName("web").WithName("dashboard")

const (
	labelEnabled = "cupboard.netztronaut.de/enabled"

	annotationGroup     = "cupboard.netztronaut.de/group"
	annotationName      = "cupboard.netztronaut.de/name"
	annotationURL       = "cupboard.netztronaut.de/url"
	annotationTarget    = "cupboard.netztronaut.de/target"
	annotationIcon      = "cupboard.netztronaut.de/icon"
	annotationIconURL   = "cupboard.netztronaut.de/icon-url"
	annotationReplicate = "cupboard.netztronaut.de/replicate"

	allLinkGroupsWildcard = "\x00all-link-groups"
)

type resourceMeta struct {
	Group     string
	Name      string
	URL       string
	Target    string
	Icon      string
	Replicate bool
}

func resourceMetaFrom(obj metav1.Object) resourceMeta {
	ann := obj.GetAnnotations()
	group := ann[annotationGroup]
	if group == "" {
		group = obj.GetNamespace()
	}
	name := ann[annotationName]
	if name == "" {
		name = obj.GetName()
	}
	return resourceMeta{
		Group:     group,
		Name:      name,
		URL:       ann[annotationURL],
		Target:    defaultTarget(ann[annotationTarget]),
		Icon:      firstNonEmpty(ann[annotationIcon], ann[annotationIconURL]),
		Replicate: ann[annotationReplicate] == "true",
	}
}

type DashboardResponse struct {
	LinkGroups []DashboardLinkGroup `json:"linkGroups"`
	Groups     []DashboardGroup     `json:"groups"`
}

type DashboardGroup struct {
	Name      string              `json:"name"`
	LinkGroup string              `json:"linkGroup,omitempty"`
	Links     []DashboardLink     `json:"links"`
	Tiles     []DashboardInfoTile `json:"tiles,omitempty"`
	Source    string              `json:"source,omitempty"`
}

// DashboardInfoTile is the serialised form of an InfoTile sent to clients.
type DashboardInfoTile struct {
	Name    string `json:"name"`
	Icon    string `json:"icon,omitempty"`
	URL     string `json:"url,omitempty"`
	Target  string `json:"target,omitempty"`
	Source  string `json:"source,omitempty"`
	Content string `json:"content,omitempty"`
	// Replicate is an in-process flag; never serialised.
	Replicate bool `json:"-"`
}

type DashboardLinkGroup struct {
	Name          string `json:"name"`
	Priority      int    `json:"priority"`
	PriorityClass string `json:"priorityClass,omitempty"`
	DisplayName   string `json:"displayName"`
	Source        string `json:"source,omitempty"`
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
	// Replicate is an in-process flag that marks whether this link may be served
	// via the synchronization API.  It is never serialized to JSON.
	Replicate bool `json:"-"`
}

// collectDashboard gathers all dashboard data and returns a filtered response.
// When localOnly is true, remote data from sync peers is excluded (used by the sync server
// itself to prevent re-exporting peer data and avoid sync cycles).
func (c *dashboardCollector) collectDashboard(ctx context.Context, userGroups []string, localOnly bool) (DashboardResponse, error) {
	groups := map[string][]DashboardLink{}
	tiles := map[string][]DashboardInfoTile{}
	groupDetails := c.initLinkGroups()
	c.collectStaticLinks(groups, groupDetails)

	if err := collectBookmarkGroups(ctx, c.reader, groups, groupDetails); err != nil {
		return DashboardResponse{}, err
	}
	if c.resourceAvailable(ctx, forecastlev1alpha1.GroupVersion, "ForecastleApp") {
		if err := c.collectForecastleApps(ctx, groups, groupDetails); err != nil {
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
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1alpha2"}, "TLSRoute") {
		if err := collectTLSRoutes(ctx, c.reader, groups, groupDetails); err != nil {
			return DashboardResponse{}, err
		}
	}
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1alpha2"}, "TCPRoute") {
		if err := collectTCPRoutes(ctx, c.reader, groups, groupDetails); err != nil {
			return DashboardResponse{}, err
		}
	}
	for _, traefikGV := range []schema.GroupVersion{
		{Group: "traefik.io", Version: "v1alpha1"},
		{Group: "traefik.containo.us", Version: "v1alpha1"},
	} {
		if c.resourceAvailable(ctx, traefikGV, "IngressRoute") {
			if err := collectIngressRoutes(ctx, c.reader, traefikGV, groups, groupDetails); err != nil {
				return DashboardResponse{}, err
			}
			break
		}
	}
	if err := collectServices(ctx, c.reader, groups, groupDetails); err != nil {
		return DashboardResponse{}, err
	}
	if err := collectEndpointSlices(ctx, c.reader, groups, groupDetails); err != nil {
		return DashboardResponse{}, err
	}
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "externaldns.k8s.io", Version: "v1alpha1"}, "DNSEndpoint") {
		if err := collectDNSEndpoints(ctx, c.reader, groups, groupDetails); err != nil {
			return DashboardResponse{}, err
		}
	}
	if err := collectInfoTiles(ctx, c.reader, tiles, groupDetails); err != nil {
		return DashboardResponse{}, err
	}

	if !localOnly && c.syncClient != nil {
		c.mergeRemoteData(groups, groupDetails)
	}
	if !localOnly && c.foreignClusters != nil {
		c.mergeForeignClusterData(ctx, groups, groupDetails)
	}

	// In the sync path only expose items explicitly marked for replication.
	if localOnly {
		for name, links := range groups {
			groups[name] = filterLinksForReplication(links)
		}
		for name, ts := range tiles {
			tiles[name] = filterTilesForReplication(ts)
		}
	}

	orderedGroups := sortLinkGroups(groupDetails)
	response := DashboardResponse{
		LinkGroups: orderedGroups,
		Groups:     make([]DashboardGroup, 0, len(orderedGroups)),
	}
	for _, group := range orderedGroups {
		links := filterLinksForGroups(groups[group.Name], userGroups)
		groupTiles := tiles[group.Name]
		if len(links) == 0 && len(groupTiles) == 0 {
			continue
		}
		sort.SliceStable(links, func(i, j int) bool {
			return strings.ToLower(links[i].Name) < strings.ToLower(links[j].Name)
		})
		sort.SliceStable(groupTiles, func(i, j int) bool {
			return strings.ToLower(groupTiles[i].Name) < strings.ToLower(groupTiles[j].Name)
		})
		response.Groups = append(response.Groups, DashboardGroup{
			Name:      group.DisplayName,
			LinkGroup: group.Name,
			Links:     links,
			Tiles:     groupTiles,
			Source:    group.Source,
		})
	}
	return response, nil
}

func (c *dashboardCollector) mergeRemoteData(groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) {
	remoteData := c.syncClient.GetRemoteData()
	for peerURL, remoteResp := range remoteData {
		source := "sync:" + peerURL

		remoteLinkGroups := make(map[string]DashboardLinkGroup, len(remoteResp.LinkGroups))
		for _, lg := range remoteResp.LinkGroups {
			remoteLinkGroups[lg.Name] = lg
		}

		for _, group := range remoteResp.Groups {
			groupName := group.LinkGroup
			if groupName == "" {
				groupName = group.Name
			}
			if _, ok := groupDetails[groupName]; !ok {
				if remoteLG, ok := remoteLinkGroups[groupName]; ok {
					groupDetails[groupName] = DashboardLinkGroup{
						Name:          remoteLG.Name,
						Priority:      remoteLG.Priority,
						PriorityClass: remoteLG.PriorityClass,
						DisplayName:   remoteLG.DisplayName,
						Source:        source,
					}
				} else {
					groupDetails[groupName] = DashboardLinkGroup{
						Name:        groupName,
						DisplayName: groupName,
						Source:      source,
					}
				}
			}
			for _, link := range group.Links {
				link.Source = source
				groups[groupName] = append(groups[groupName], link)
			}
		}
	}
}

// mergeForeignClusterData collects dashboard links from every reachable foreign cluster
// and merges them into the local groups/groupDetails maps.  Errors per-cluster are
// logged as warnings and skipped so a single unavailable cluster never breaks the
// dashboard for the rest.
func (c *dashboardCollector) mergeForeignClusterData(ctx context.Context, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) {
	for _, fc := range c.foreignClusters.ActiveClusters() {
		source := "foreign:" + fc.Name
		tempGroups := map[string][]DashboardLink{}
		tempGroupDetails := map[string]DashboardLinkGroup{}

		mini := &dashboardCollector{
			reader:    fc.Client,
			discovery: fc.Discovery,
		}
		collectForeignLinks(ctx, mini, tempGroups, tempGroupDetails)

		// Merge into main maps, stamping the source.
		for groupName, links := range tempGroups {
			replicatedLinks := filterLinksForReplication(links)
			if len(replicatedLinks) == 0 {
				continue
			}
			if _, ok := groupDetails[groupName]; !ok {
				if detail, ok2 := tempGroupDetails[groupName]; ok2 {
					detail.Source = source
					groupDetails[groupName] = detail
				} else {
					groupDetails[groupName] = DashboardLinkGroup{
						Name:        groupName,
						DisplayName: groupName,
						Source:      source,
					}
				}
			}
			for _, link := range replicatedLinks {
				link.Source = source + ":" + link.Source
				groups[groupName] = append(groups[groupName], link)
			}
		}
	}
}

// collectForeignLinks runs the subset of collect functions that make sense on a
// foreign cluster: it excludes BookmarkGroups and infrastructure-only resources
// (EndpointSlice, DNSEndpoint) while keeping Ingress, Service, HTTPRoute, etc.
func collectForeignLinks(ctx context.Context, c *dashboardCollector, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) {
	if err := collectIngresses(ctx, c.reader, groups, groupDetails); err != nil {
		setupLog.Info("Skipping Ingress collection from foreign cluster", "error", err.Error())
	}
	if err := collectServices(ctx, c.reader, groups, groupDetails); err != nil {
		setupLog.Info("Skipping Service collection from foreign cluster", "error", err.Error())
	}
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1"}, "HTTPRoute") {
		if err := collectHTTPRoutes(ctx, c.reader, groups, groupDetails); err != nil {
			setupLog.Info("Skipping HTTPRoute collection from foreign cluster", "error", err.Error())
		}
	}
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1alpha2"}, "TLSRoute") {
		if err := collectTLSRoutes(ctx, c.reader, groups, groupDetails); err != nil {
			setupLog.Info("Skipping TLSRoute collection from foreign cluster", "error", err.Error())
		}
	}
	if c.resourceAvailable(ctx, schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1alpha2"}, "TCPRoute") {
		if err := collectTCPRoutes(ctx, c.reader, groups, groupDetails); err != nil {
			setupLog.Info("Skipping TCPRoute collection from foreign cluster", "error", err.Error())
		}
	}
	for _, traefikGV := range []schema.GroupVersion{
		{Group: "traefik.io", Version: "v1alpha1"},
		{Group: "traefik.containo.us", Version: "v1alpha1"},
	} {
		if c.resourceAvailable(ctx, traefikGV, "IngressRoute") {
			if err := collectIngressRoutes(ctx, c.reader, traefikGV, groups, groupDetails); err != nil {
				setupLog.Info("Skipping IngressRoute collection from foreign cluster", "error", err.Error())
			}
			break
		}
	}
	if c.resourceAvailable(ctx, forecastlev1alpha1.GroupVersion, "ForecastleApp") {
		if err := c.collectForecastleApps(ctx, groups, groupDetails); err != nil {
			setupLog.Info("Skipping ForecastleApp collection from foreign cluster", "error", err.Error())
		}
	}
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
			Source:        "local",
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
		{groupVersion: schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1alpha2"}, kind: "TLSRoute"},
		{groupVersion: schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1alpha2"}, kind: "TCPRoute"},
		{groupVersion: schema.GroupVersion{Group: "traefik.io", Version: "v1alpha1"}, kind: "IngressRoute"},
		{groupVersion: schema.GroupVersion{Group: "traefik.containo.us", Version: "v1alpha1"}, kind: "IngressRoute"},
		{groupVersion: schema.GroupVersion{Group: "externaldns.k8s.io", Version: "v1alpha1"}, kind: "DNSEndpoint"},
	} {
		if !c.resourceAvailable(ctx, resource.groupVersion, resource.kind) {
			setupLog.Info("Skipping optional dashboard resource because it is unavailable", "groupVersion", resource.groupVersion.String(), "kind", resource.kind)
		}
	}
}

func (c *dashboardCollector) resourceAvailable(ctx context.Context, groupVersion schema.GroupVersion, kind string) bool { //nolint:unparam
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
				Name:      link.Name,
				URL:       url,
				Target:    target,
				Icon:      link.Icon,
				Source:    "bookmarkgroup",
				Groups:    normalizedGroups(link.Groups),
				Replicate: item.Spec.Replicate,
			})
		}
	}
	return nil
}

func (c *dashboardCollector) collectForecastleApps(ctx context.Context, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	var list forecastlev1alpha1.ForecastleAppList
	if err := c.reader.List(ctx, &list); err != nil {
		return client.IgnoreNotFound(err)
	}

	for _, item := range list.Items {
		if c.forecastleInstance != "" && strings.TrimSpace(item.Spec.Instance) != c.forecastleInstance {
			continue
		}
		linkName := item.Spec.Name
		groupName := resolveLinkGroupName(item.Spec.Group, groupDetails)
		icon := item.Spec.Icon
		target := string(dashboardv1alpha1.BookmarkLinkTargetBlank)
		if v := item.Spec.Properties["target"]; strings.TrimSpace(v) != "" {
			target = v
		}

		url := item.Spec.URL
		if strings.TrimSpace(url) == "" {
			source := parseForecastleURLSource(item.Spec.URLFrom)
			if source != nil {
				resolved, resolveErr := webhookdashboardv1alpha1.ResolveURLFromSource(ctx, c.reader, item.GetNamespace(), source)
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
			Name:      linkName,
			URL:       url,
			Target:    target,
			Icon:      icon,
			Source:    "forecastleapp",
			Groups:    normalizedGroups(item.Spec.Groups),
			Replicate: item.GetAnnotations()[annotationReplicate] == "true",
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
		meta := resourceMetaFrom(&ing)
		if meta.URL == "" {
			for _, rule := range ing.Spec.Rules {
				if strings.TrimSpace(rule.Host) != "" {
					meta.URL = "https://" + rule.Host
					break
				}
			}
		}
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "ingress",
			Replicate: meta.Replicate,
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
		meta := resourceMetaFrom(&route)
		if meta.URL == "" {
			if hostnames, found, err := unstructured.NestedStringSlice(route.Object, "spec", "hostnames"); err == nil && found {
				for _, host := range hostnames {
					if strings.TrimSpace(host) != "" {
						meta.URL = "https://" + host
						break
					}
				}
			}
		}
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "httproute",
			Replicate: meta.Replicate,
		})
	}
	return nil
}

func collectTLSRoutes(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1alpha2",
		Kind:    "TLSRouteList",
	})
	if err := c.List(ctx, list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return client.IgnoreNotFound(err)
	}
	for _, route := range list.Items {
		meta := resourceMetaFrom(&route)
		if meta.URL == "" {
			if hostnames, found, err := unstructured.NestedStringSlice(route.Object, "spec", "hostnames"); err == nil && found {
				for _, host := range hostnames {
					if strings.TrimSpace(host) != "" {
						meta.URL = "https://" + host
						break
					}
				}
			}
		}
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "tlsroute",
			Replicate: meta.Replicate,
		})
	}
	return nil
}

func collectTCPRoutes(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1alpha2",
		Kind:    "TCPRouteList",
	})
	if err := c.List(ctx, list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return client.IgnoreNotFound(err)
	}
	for _, route := range list.Items {
		meta := resourceMetaFrom(&route)
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "tcproute",
			Replicate: meta.Replicate,
		})
	}
	return nil
}

func collectIngressRoutes(ctx context.Context, c client.Reader, gv schema.GroupVersion, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    "IngressRouteList",
	})
	if err := c.List(ctx, list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return client.IgnoreNotFound(err)
	}
	for _, route := range list.Items {
		meta := resourceMetaFrom(&route)
		if meta.URL == "" {
			routes, found, _ := unstructured.NestedSlice(route.Object, "spec", "routes")
			if found {
				for _, r := range routes {
					routeMap, ok := r.(map[string]any)
					if !ok {
						continue
					}
					match, _ := routeMap["match"].(string)
					if host := extractHostFromTraefikMatch(match); host != "" {
						meta.URL = "https://" + host
						break
					}
				}
			}
		}
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "ingressroute",
			Replicate: meta.Replicate,
		})
	}
	return nil
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

func collectServices(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	var list corev1.ServiceList
	if err := c.List(ctx, &list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return err
	}
	for _, svc := range list.Items {
		meta := resourceMetaFrom(&svc)
		if meta.URL == "" && len(svc.Spec.Ports) > 0 {
			scheme := "http"
			if svc.Spec.Ports[0].Port == 443 {
				scheme = "https"
			}
			meta.URL = fmt.Sprintf("%s://%s.%s.svc.cluster.local:%d", scheme, svc.Name, svc.Namespace, svc.Spec.Ports[0].Port)
		}
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "service",
			Replicate: meta.Replicate,
		})
	}
	return nil
}

func collectEndpointSlices(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	var list discoveryv1.EndpointSliceList
	if err := c.List(ctx, &list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return err
	}
	for _, eps := range list.Items {
		meta := resourceMetaFrom(&eps)
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "endpointslice",
			Replicate: meta.Replicate,
		})
	}
	return nil
}

func collectDNSEndpoints(ctx context.Context, c client.Reader, groups map[string][]DashboardLink, groupDetails map[string]DashboardLinkGroup) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "externaldns.k8s.io",
		Version: "v1alpha1",
		Kind:    "DNSEndpointList",
	})
	if err := c.List(ctx, list, client.MatchingLabels{labelEnabled: "true"}); err != nil {
		return client.IgnoreNotFound(err)
	}
	for _, ep := range list.Items {
		meta := resourceMetaFrom(&ep)
		if meta.URL == "" {
			if endpoints, found, err := unstructured.NestedSlice(ep.Object, "spec", "endpoints"); err == nil && found {
				for _, item := range endpoints {
					endpointMap, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if dnsName, ok := endpointMap["dnsName"].(string); ok && strings.TrimSpace(dnsName) != "" {
						meta.URL = "https://" + dnsName
						break
					}
				}
			}
		}
		if meta.Group == "" || meta.Name == "" || meta.URL == "" {
			continue
		}
		ensureLinkGroup(groupDetails, meta.Group)
		groups[meta.Group] = append(groups[meta.Group], DashboardLink{
			Name:      meta.Name,
			URL:       meta.URL,
			Target:    meta.Target,
			Icon:      meta.Icon,
			Source:    "dnsendpoint",
			Replicate: meta.Replicate,
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
		Source:      "local",
	}
}

// filterLinksForReplication returns only the links that have Replicate=true.
// Used by the sync endpoint to avoid leaking local-only entries to peers.
func filterLinksForReplication(links []DashboardLink) []DashboardLink {
	out := make([]DashboardLink, 0, len(links))
	for _, l := range links {
		if l.Replicate {
			out = append(out, l)
		}
	}
	return out
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

func collectInfoTiles(ctx context.Context, c client.Reader, tiles map[string][]DashboardInfoTile, groupDetails map[string]DashboardLinkGroup) error {
	var list dashboardv1alpha1.InfoTileList
	if err := c.List(ctx, &list); err != nil {
		return fmt.Errorf("list InfoTiles: %w", err)
	}
	for _, item := range list.Items {
		groupName := strings.TrimSpace(item.Spec.Group)
		if groupName == "" {
			groupName = item.Name
		}
		if _, ok := groupDetails[groupName]; !ok {
			groupDetails[groupName] = DashboardLinkGroup{
				Name:        groupName,
				DisplayName: groupName,
				Source:      "infotile",
			}
		}
		tiles[groupName] = append(tiles[groupName], DashboardInfoTile{
			Name:      item.Spec.Name,
			Icon:      item.Spec.Icon,
			URL:       item.Spec.URL,
			Target:    defaultTarget(item.Spec.Target),
			Source:    item.Spec.Source,
			Content:   item.Status.Content,
			Replicate: item.Spec.Replicate,
		})
	}
	return nil
}

func filterTilesForReplication(ts []DashboardInfoTile) []DashboardInfoTile {
	out := make([]DashboardInfoTile, 0, len(ts))
	for _, t := range ts {
		if t.Replicate {
			out = append(out, t)
		}
	}
	return out
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
