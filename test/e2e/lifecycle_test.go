//go:build e2e
// +build e2e

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

// Package e2e tests the full resource lifecycle end-to-end.
//
// The suite deploys the cupboard manager (auth disabled, watching all namespaces)
// together with:
//   - a mock HTTP server (nginx) that returns HTTP 200
//   - Traefik v3 CRDs (IngressRoute)
//   - Gateway API experimental CRDs (HTTPRoute v1, GRPCRoute v1, TLSRoute v1, TCPRoute v1alpha2)
//
// Tests exercise:
//  1. Bookmark → API group named after spec.group
//  2. BookmarkGroup with spec.name → group display name comes from spec.name
//  3. Same as (2) but in a different namespace
//  4. Multiple bookmarks across multiple groups
//  5. Bookmark with unreachable URL → status reflects the failure; not returned by API
//  6. Annotated routing resources → all appear in the dashboard API
package e2e

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"netztronaut.de/cupboard/test/utils"
)

// ---- constants ----------------------------------------------------------------

const (
	// lcNS is the manager namespace (and the namespace for most test resources).
	lcNS = "cupboard-system"
	// lcAltNS is a second namespace used in Test 3 to verify cross-namespace watching.
	lcAltNS = "lc-alt"
	// lcMockNS hosts the mock HTTP server (no restricted pod-security policy).
	lcMockNS = "lc-mock"
	// lcWebSvc is the ClusterIP service used to port-forward to the dashboard API.
	lcWebSvc = "cupboard-web-lc"
	// lcMockSvc is the ClusterIP service of the mock HTTP server.
	lcMockSvc     = "mock-http-server"
	lcMockSvcPort = 80
)

// lcMockURL is the in-cluster URL of the mock HTTP server.
var lcMockURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/", lcMockSvc, lcMockNS, lcMockSvcPort)

// ---- Gateway API and Traefik CRD URLs ----------------------------------------
const (
	// Gateway API experimental channel includes HTTPRoute (v1), GRPCRoute (v1), TLSRoute (v1), TCPRoute (v1alpha2).
	gatewayAPICRDURL = "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/experimental-install.yaml"
	// Traefik v3 CRDs (IngressRoute lives here).
	traefikCRDURL = "https://raw.githubusercontent.com/traefik/traefik-helm-chart/v34.5.0/traefik/crds/traefik.io_ingressroutes.yaml"
)

// ---- shared state ------------------------------------------------------------

var lc struct {
	pf *portForwarder
}

// ---- main test suite ---------------------------------------------------------

var _ = Describe("Lifecycle", Ordered, Label("lifecycle"), func() {

	BeforeAll(func() {
		// ---- 1. Create manager namespace with restricted pod-security ----------
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", lcNS)
		_, _ = utils.Run(cmd) // ignore error if it already exists
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", lcNS,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace")

		DeferCleanup(func() {
			By("deleting manager namespace")
			_, _ = utils.Run(exec.Command("kubectl", "delete", "ns", lcNS, "--ignore-not-found", "--timeout=60s"))
		})

		// ---- 2. Create alternate and mock namespaces -------------------------
		By("creating alternate namespace " + lcAltNS)
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", lcAltNS))
		DeferCleanup(func() {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "ns", lcAltNS, "--ignore-not-found", "--timeout=60s"))
		})

		By("creating mock server namespace " + lcMockNS)
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", lcMockNS))
		DeferCleanup(func() {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "ns", lcMockNS, "--ignore-not-found", "--timeout=60s"))
		})

		// ---- 3. Install CRDs and deploy the manager -------------------------
		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")
		DeferCleanup(func() {
			_, _ = utils.Run(exec.Command("make", "uninstall"))
		})

		// Wait for CRDs to be fully established in the API server before
		// deploying the manager.  Without this, the manager's informers
		// may time out trying to list resources whose CRD endpoint is not
		// yet served, causing a crash-loop.
		By("waiting for cupboard CRDs to be established")
		cmd = exec.Command("kubectl", "wait", "--for=condition=Established",
			"crd/bookmarks.dashboard.netztronaut.de",
			"crd/bookmarkgroups.dashboard.netztronaut.de",
			"crd/infotiles.dashboard.netztronaut.de",
			"--timeout=60s",
		)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Cupboard CRDs did not become established")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
		DeferCleanup(func() {
			_, _ = utils.Run(exec.Command("make", "undeploy"))
		})

		// The default deployment uses a namespace-scoped RoleBinding.
		// When WATCH_NAMESPACE="" the manager uses a cluster-scoped cache
		// and needs cluster-wide LIST/WATCH/UPDATE for the dashboard CRDs.
		// Apply a dedicated ClusterRole+ClusterRoleBinding and clean up.
		By("creating cluster-wide RBAC for lifecycle tests")
		clusterRBACManifest := fmt.Sprintf(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cupboard-lifecycle-cluster-role
rules:
- apiGroups: ["dashboard.netztronaut.de"]
  resources:
  - bookmarks
  - bookmarkgroups
  - infotiles
  - bookmarks/status
  - bookmarkgroups/status
  - infotiles/status
  - bookmarks/finalizers
  - bookmarkgroups/finalizers
  - infotiles/finalizers
  verbs: ["create","delete","get","list","patch","update","watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: cupboard-lifecycle-cluster-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cupboard-lifecycle-cluster-role
subjects:
- kind: ServiceAccount
  name: cupboard-controller-manager
  namespace: %s
`, lcNS)
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(clusterRBACManifest)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create cluster-wide RBAC")
		DeferCleanup(func() {
			_ = exec.Command("kubectl", "delete", "clusterrolebinding",
				"cupboard-lifecycle-cluster-binding", "--ignore-not-found").Run()
			_ = exec.Command("kubectl", "delete", "clusterrole",
				"cupboard-lifecycle-cluster-role", "--ignore-not-found").Run()
			// Also remove the incorrect CRB created in previous step (if any).
			_ = exec.Command("kubectl", "delete", "clusterrolebinding",
				"cupboard-lifecycle-cluster-watch", "--ignore-not-found").Run()
		})

		// ---- 4. Patch: disable auth and watch all namespaces ----------------
		// ENABLE_AUTH=false makes the dashboard API accessible without OIDC.
		// WATCH_NAMESPACE="" tells controller-runtime to watch all namespaces.
		By("patching deployment: ENABLE_AUTH=false, WATCH_NAMESPACE=<all>")
		cmd = exec.Command("kubectl", "set", "env",
			"deployment/cupboard-controller-manager",
			"-n", lcNS,
			"ENABLE_AUTH=false",
			"WATCH_NAMESPACE=",
		)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to patch deployment env")

		By("waiting for rollout after env patch")
		cmd = exec.Command("kubectl", "rollout", "status",
			"deployment/cupboard-controller-manager",
			"-n", lcNS, "--timeout=3m")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Deployment rollout timed out")

		// ---- 5. Install Traefik and Gateway API CRDs ------------------------
		By("installing Traefik IngressRoute CRD")
		cmd = exec.Command("kubectl", "apply", "-f", traefikCRDURL, "--server-side=true", "--force-conflicts")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install Traefik CRDs")

		By("installing Gateway API experimental CRDs")
		cmd = exec.Command("kubectl", "apply", "-f", gatewayAPICRDURL, "--server-side=true", "--force-conflicts")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install Gateway API CRDs")

		// ---- 6. Deploy mock HTTP server (nginx:alpine) in lcMockNS ----------
		By("deploying mock HTTP server")
		mockServerManifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: server
        image: nginx:alpine
        ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    app: %s
  ports:
  - port: %d
    targetPort: 80
`, lcMockSvc, lcMockNS,
			lcMockSvc, lcMockSvc,
			lcMockSvc, lcMockNS, lcMockSvc, lcMockSvcPort)
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(mockServerManifest)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy mock server")

		By("waiting for mock HTTP server to be ready")
		cmd = exec.Command("kubectl", "rollout", "status",
			"deployment/"+lcMockSvc, "-n", lcMockNS, "--timeout=2m")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Mock server deployment timed out")

		// ---- 7. Create a ClusterIP service to port-forward the dashboard ----
		By("creating dashboard port-forward service")
		webSvcManifest := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    control-plane: controller-manager
  ports:
  - port: 8082
    targetPort: 8082
`, lcWebSvc, lcNS)
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(webSvcManifest)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create dashboard service")

		// ---- 8. Start port-forwarder -----------------------------------------
		By("starting port-forwarder for dashboard API")
		// Use the default kubeconfig (managed by the outer k3d setup).
		kubeconfig := utils.DefaultKubeconfig()
		pf, pfErr := newPortForwarder(kubeconfig, lcNS, lcWebSvc, 8082)
		Expect(pfErr).NotTo(HaveOccurred(), "Failed to start port-forwarder")
		// Use a short per-request timeout so the Eventually below can retry
		// many times within its window (the default 120s timeout would
		// allow only ~1 attempt in a 3-minute window).
		pf.client = &http.Client{Timeout: 5 * time.Second}
		lc.pf = pf
		DeferCleanup(func() {
			By("stopping port-forwarder")
			lc.pf.close()
		})

		// ---- 9. Wait for the dashboard API to become available --------------
		By("waiting for the dashboard API to respond")
		Eventually(func() error {
			_, err := lc.pf.fetchDashboard()
			return err
		}, 5*time.Minute, 3*time.Second).Should(Succeed(), "Dashboard API not available")
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(2 * time.Second)

	// ---- helpers -------------------------------------------------------------

	// fetch calls the dashboard API and returns the response.
	fetch := func(g Gomega) dashboardResponse {
		dr, err := lc.pf.fetchDashboard()
		g.Expect(err).NotTo(HaveOccurred())
		return dr
	}

	// groupNames returns the names of all groups in the response.
	groupNames := func(dr dashboardResponse) []string {
		names := make([]string, 0, len(dr.Groups))
		for _, g := range dr.Groups {
			names = append(names, g.Name)
		}
		return names
	}

	// linksInGroup returns the link names in a named group.
	linksInGroup := func(dr dashboardResponse, groupName string) []string {
		for _, g := range dr.Groups {
			if g.Name == groupName {
				names := make([]string, 0, len(g.Links))
				for _, l := range g.Links {
					names = append(names, l.Name)
				}
				return names
			}
		}
		return nil
	}

	// linkSource returns the source field of a link in a named group.
	linkSource := func(dr dashboardResponse, groupName, linkName string) string {
		for _, g := range dr.Groups {
			if g.Name != groupName {
				continue
			}
			for _, l := range g.Links {
				if l.Name == linkName {
					return l.Source
				}
			}
		}
		return ""
	}

	// applyManifest applies a YAML manifest via stdin.
	applyManifest := func(manifest string) error {
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		return err
	}

	// cleanupResource deletes a Kubernetes resource, ignoring not-found errors.
	cleanupResource := func(kind, name, ns string) {
		_, _ = utils.Run(exec.Command("kubectl", "delete", kind, name, "-n", ns, "--ignore-not-found"))
	}

	// ---- Test 1: Bookmark group name = spec.group (no BookmarkGroup) ----------

	Context("Test 1: single Bookmark without BookmarkGroup", func() {
		const bmName = "lc-bm-1"
		const groupID = "my-app"

		BeforeEach(func() {
			By("creating Bookmark " + bmName)
			Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: Bookmark
metadata:
  name: %s
  namespace: %s
spec:
  name: My App
  group: %s
  url: %s
`, bmName, lcNS, groupID, lcMockURL))).To(Succeed())
		})

		AfterEach(func() {
			cleanupResource("bookmark", bmName, lcNS)
		})

		It("exposes the bookmark and names the group after spec.group", func() {
			By("waiting for URL check to mark bookmark reachable")
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "bookmark", bmName, "-n", lcNS,
					"-o", "jsonpath={.status.urlReachable}"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("true"), "bookmark URL not yet marked reachable")
			}).Should(Succeed())

			By("verifying the dashboard API returns the bookmark in the correct group")
			Eventually(func(g Gomega) {
				dr := fetch(g)
				g.Expect(groupNames(dr)).To(ContainElement(groupID), "group %q not in dashboard", groupID)
				g.Expect(linksInGroup(dr, groupID)).To(ContainElement("My App"), "link not in group")
				g.Expect(linkSource(dr, groupID, "My App")).To(Equal("bookmark"))
			}).Should(Succeed())
		})
	})

	// ---- Test 2: BookmarkGroup.spec.name becomes the display name -------------

	Context("Test 2: BookmarkGroup with spec.name overrides display name", func() {
		// spec.name must be a valid k8s identifier (no spaces).
		// Bookmarks route by spec.group, which the dashboard uses as the group key.
		// BookmarkGroup.spec.name overrides the group key to be spec.name instead
		// of metadata.name.
		const bgName = "lc-bg-2"
		const bgSpecName = "lc-bg-2-display" // valid k8s name, different from bgName
		const bmName = "lc-bm-2"

		BeforeEach(func() {
			By("creating BookmarkGroup " + bgName + " with spec.name=" + bgSpecName)
			Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: BookmarkGroup
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
`, bgName, lcNS, bgSpecName))).To(Succeed())

			// Bookmark.spec.group must equal BookmarkGroup.spec.name so that
			// the dashboard places the link under the BookmarkGroup.spec.name key.
			By("creating Bookmark with spec.group matching BookmarkGroup.spec.name")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: Bookmark
metadata:
  name: %s
  namespace: %s
spec:
  name: My Fancy App
  group: %s
  url: %s
`, bmName, lcNS, bgSpecName, lcMockURL))).To(Succeed())
		})

		AfterEach(func() {
			cleanupResource("bookmark", bmName, lcNS)
			cleanupResource("bookmarkgroup", bgName, lcNS)
			// The controller may auto-create a BookmarkGroup for the spec.group name.
			cleanupResource("bookmarkgroup", bgSpecName, lcNS)
		})

		It("names the group after BookmarkGroup.spec.name, not metadata.name", func() {
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "bookmark", bmName, "-n", lcNS,
					"-o", "jsonpath={.status.urlReachable}"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("true"))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				dr := fetch(g)
				// The group is keyed by spec.name, not metadata.name.
				g.Expect(groupNames(dr)).To(ContainElement(bgSpecName),
					"expected group named %q, got groups: %v", bgSpecName, groupNames(dr))
				g.Expect(linksInGroup(dr, bgSpecName)).To(ContainElement("My Fancy App"))
				// metadata.name must NOT appear as a separate group key.
				g.Expect(groupNames(dr)).NotTo(ContainElement(bgName),
					"group should use spec.name, not metadata.name")
			}).Should(Succeed())
		})
	})

	// ---- Test 3: BookmarkGroup and Bookmark in a different namespace ----------

	Context("Test 3: BookmarkGroup and Bookmark in alternate namespace", func() {
		const bgName = "lc-bg-3"
		const bmName = "lc-bm-3"

		BeforeEach(func() {
			By("creating BookmarkGroup in " + lcAltNS)
			Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: BookmarkGroup
metadata:
  name: %s
  namespace: %s
spec: {}
`, bgName, lcAltNS))).To(Succeed())

			By("creating Bookmark in " + lcAltNS)
			Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: Bookmark
metadata:
  name: %s
  namespace: %s
spec:
  name: Alt App
  group: %s
  url: %s
`, bmName, lcAltNS, bgName, lcMockURL))).To(Succeed())
		})

		AfterEach(func() {
			cleanupResource("bookmark", bmName, lcAltNS)
			cleanupResource("bookmarkgroup", bgName, lcAltNS)
		})

		It("collects resources from the alternate namespace", func() {
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "bookmark", bmName, "-n", lcAltNS,
					"-o", "jsonpath={.status.urlReachable}"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("true"))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				dr := fetch(g)
				g.Expect(groupNames(dr)).To(ContainElement(bgName),
					"expected group %q from alt namespace; groups: %v", bgName, groupNames(dr))
				g.Expect(linksInGroup(dr, bgName)).To(ContainElement("Alt App"))
			}).Should(Succeed())
		})
	})

	// ---- Test 4: Multiple bookmarks across multiple groups --------------------

	Context("Test 4: multiple bookmarks across multiple groups", func() {
		type bm struct{ name, group, displayName string }
		bookmarks := []bm{
			{"lc-bm-4a", "lc-group-alpha", "Alpha App"},
			{"lc-bm-4b", "lc-group-alpha", "Beta App"},
			{"lc-bm-4c", "lc-group-gamma", "Gamma App"},
			{"lc-bm-4d", "lc-group-delta", "Delta App"},
		}

		BeforeEach(func() {
			for _, b := range bookmarks {
				By("creating Bookmark " + b.name)
				Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: Bookmark
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  group: %s
  url: %s
`, b.name, lcNS, b.displayName, b.group, lcMockURL))).To(Succeed())
			}
		})

		AfterEach(func() {
			for _, b := range bookmarks {
				cleanupResource("bookmark", b.name, lcNS)
			}
		})

		It("returns all bookmarks in their respective groups", func() {
			// Wait for all bookmarks to become reachable.
			for _, b := range bookmarks {
				b := b
				Eventually(func(g Gomega) {
					out, err := utils.Run(exec.Command("kubectl", "get", "bookmark", b.name, "-n", lcNS,
						"-o", "jsonpath={.status.urlReachable}"))
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(out).To(Equal("true"), "bookmark %s not yet reachable", b.name)
				}).Should(Succeed())
			}

			Eventually(func(g Gomega) {
				dr := fetch(g)
				g.Expect(groupNames(dr)).To(ContainElement("lc-group-alpha"))
				g.Expect(groupNames(dr)).To(ContainElement("lc-group-gamma"))
				g.Expect(groupNames(dr)).To(ContainElement("lc-group-delta"))
				g.Expect(linksInGroup(dr, "lc-group-alpha")).To(ConsistOf("Alpha App", "Beta App"))
				g.Expect(linksInGroup(dr, "lc-group-gamma")).To(ConsistOf("Gamma App"))
				g.Expect(linksInGroup(dr, "lc-group-delta")).To(ConsistOf("Delta App"))
			}).Should(Succeed())
		})
	})

	// ---- Test 5: Bookmark with unreachable URL --------------------------------

	Context("Test 5: Bookmark with non-working URL", func() {
		const bmBadName = "lc-bm-bad"
		const bmGoodName = "lc-bm-good"
		const badURL = "http://this-host-does-not-exist.invalid/path"
		const groupBad = "bad-url-group"
		const groupGood = "good-url-group"

		BeforeEach(func() {
			By("creating Bookmark with unreachable URL")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: Bookmark
metadata:
  name: %s
  namespace: %s
spec:
  name: Bad Bookmark
  group: %s
  url: %s
`, bmBadName, lcNS, groupBad, badURL))).To(Succeed())

			By("creating Bookmark with reachable URL (reference point)")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: dashboard.netztronaut.de/v1alpha1
kind: Bookmark
metadata:
  name: %s
  namespace: %s
spec:
  name: Good Bookmark
  group: %s
  url: %s
`, bmGoodName, lcNS, groupGood, lcMockURL))).To(Succeed())
		})

		AfterEach(func() {
			cleanupResource("bookmark", bmBadName, lcNS)
			cleanupResource("bookmark", bmGoodName, lcNS)
		})

		It("sets status.urlReachable=false for bad URL and omits it from the dashboard", func() {
			By("waiting for the bad bookmark to be reconciled")
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "bookmark", bmBadName, "-n", lcNS,
					"-o", "jsonpath={.status.urlReachable}"))
				g.Expect(err).NotTo(HaveOccurred())
				// Status must be set (either true or false) – not empty.
				g.Expect(out).NotTo(BeEmpty(), "status.urlReachable not yet set")
			}).Should(Succeed())

			By("checking status.urlReachable is false")
			out, err := utils.Run(exec.Command("kubectl", "get", "bookmark", bmBadName, "-n", lcNS,
				"-o", "jsonpath={.status.urlReachable}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal("false"), "expected unreachable URL to be marked false")

			By("checking status.urlCheckError is set")
			out, err = utils.Run(exec.Command("kubectl", "get", "bookmark", bmBadName, "-n", lcNS,
				"-o", "jsonpath={.status.urlCheckError}"))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).NotTo(BeEmpty(), "expected urlCheckError to be populated")

			By("waiting for the good bookmark to become reachable (ensures reconciler ran)")
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "bookmark", bmGoodName, "-n", lcNS,
					"-o", "jsonpath={.status.urlReachable}"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("true"))
			}).Should(Succeed())

			By("verifying the bad bookmark does NOT appear in the dashboard API")
			Eventually(func(g Gomega) {
				dr := fetch(g)
				// Good bookmark must be present.
				g.Expect(groupNames(dr)).To(ContainElement(groupGood))
				g.Expect(linksInGroup(dr, groupGood)).To(ContainElement("Good Bookmark"))
				// Bad bookmark's group must not appear at all.
				g.Expect(groupNames(dr)).NotTo(ContainElement(groupBad),
					"unreachable bookmark group should not appear in dashboard")
			}).Should(Succeed())
		})
	})

	// ---- Test 6: Annotated routing resources ---------------------------------

	Context("Test 6: annotated routing resources appear in dashboard", func() {
		const routeGroup = "routing-test"

		const ingressName = "lc-ingress"
		const ingressRouteName = "lc-ingressroute"
		const svcName = "lc-service"
		const httpRouteName = "lc-httproute"
		const grpcRouteName = "lc-grpcroute"
		const tlsRouteName = "lc-tlsroute"
		const tcpRouteName = "lc-tcproute"

		BeforeEach(func() {
			// Ingress – URL inferred from spec.rules[].host
			By("creating Ingress")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  labels:
    cupboard.netztronaut.de/enabled: "true"
  annotations:
    cupboard.netztronaut.de/group: %s
    cupboard.netztronaut.de/name: My Ingress
spec:
  rules:
  - host: ingress.example.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: dummy
            port:
              number: 80
`, ingressName, lcNS, routeGroup))).To(Succeed())

			// IngressRoute (Traefik) – URL inferred from spec.routes[].match Host(...)
			By("creating IngressRoute")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: %s
  namespace: %s
  labels:
    cupboard.netztronaut.de/enabled: "true"
  annotations:
    cupboard.netztronaut.de/group: %s
    cupboard.netztronaut.de/name: My IngressRoute
spec:
  routes:
  - match: Host(%[4]sroute.example.local%[4]s)
    kind: Rule
    services:
    - name: dummy
      port: 80
`, ingressRouteName, lcNS, routeGroup, "`"))).To(Succeed())

			// Service – URL via explicit annotation
			By("creating Service")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels:
    cupboard.netztronaut.de/enabled: "true"
  annotations:
    cupboard.netztronaut.de/group: %s
    cupboard.netztronaut.de/name: My Service
    cupboard.netztronaut.de/url: http://service.example.local/
spec:
  selector:
    app: dummy
  ports:
  - port: 80
`, svcName, lcNS, routeGroup))).To(Succeed())

			// HTTPRoute (Gateway API v1) – URL inferred from spec.hostnames
			By("creating HTTPRoute")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: %s
  namespace: %s
  labels:
    cupboard.netztronaut.de/enabled: "true"
  annotations:
    cupboard.netztronaut.de/group: %s
    cupboard.netztronaut.de/name: My HTTPRoute
spec:
  hostnames:
  - httproute.example.local
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /
`, httpRouteName, lcNS, routeGroup))).To(Succeed())

			// GRPCRoute (Gateway API v1) – URL inferred from spec.hostnames
			By("creating GRPCRoute")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: GRPCRoute
metadata:
  name: %s
  namespace: %s
  labels:
    cupboard.netztronaut.de/enabled: "true"
  annotations:
    cupboard.netztronaut.de/group: %s
    cupboard.netztronaut.de/name: My GRPCRoute
spec:
  hostnames:
  - grpcroute.example.local
  rules:
  - matches:
    - method:
        type: Exact
        service: example.ExampleService
`, grpcRouteName, lcNS, routeGroup))).To(Succeed())

			// TLSRoute (Gateway API v1) – URL inferred from spec.hostnames
			By("creating TLSRoute")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: TLSRoute
metadata:
  name: %s
  namespace: %s
  labels:
    cupboard.netztronaut.de/enabled: "true"
  annotations:
    cupboard.netztronaut.de/group: %s
    cupboard.netztronaut.de/name: My TLSRoute
spec:
  hostnames:
  - tlsroute.example.local
  rules:
  - backendRefs:
    - name: dummy
      port: 443
`, tlsRouteName, lcNS, routeGroup))).To(Succeed())

			// TCPRoute (Gateway API v1alpha2) – URL must be explicit (no hostnames in spec)
			By("creating TCPRoute")
			Expect(applyManifest(fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: %s
  namespace: %s
  labels:
    cupboard.netztronaut.de/enabled: "true"
  annotations:
    cupboard.netztronaut.de/group: %s
    cupboard.netztronaut.de/name: My TCPRoute
    cupboard.netztronaut.de/url: tcp://tcproute.example.local:9000
spec:
  rules:
  - backendRefs:
    - name: dummy
      port: 9000
`, tcpRouteName, lcNS, routeGroup))).To(Succeed())
		})

		AfterEach(func() {
			cleanupResource("ingress", ingressName, lcNS)
			cleanupResource("ingressroute.traefik.io", ingressRouteName, lcNS)
			cleanupResource("service", svcName, lcNS)
			cleanupResource("httproute.gateway.networking.k8s.io", httpRouteName, lcNS)
			cleanupResource("grpcroute.gateway.networking.k8s.io", grpcRouteName, lcNS)
			cleanupResource("tlsroute.gateway.networking.k8s.io", tlsRouteName, lcNS)
			cleanupResource("tcproute.gateway.networking.k8s.io", tcpRouteName, lcNS)
		})

		It("returns all routing resources in the dashboard under the correct group", func() {
			Eventually(func(g Gomega) {
				dr := fetch(g)
				g.Expect(groupNames(dr)).To(ContainElement(routeGroup),
					"group %q not found; groups: %v", routeGroup, groupNames(dr))
				names := linksInGroup(dr, routeGroup)
				g.Expect(names).To(ContainElement("My Ingress"), "Ingress missing")
				g.Expect(names).To(ContainElement("My IngressRoute"), "IngressRoute missing")
				g.Expect(names).To(ContainElement("My Service"), "Service missing")
				g.Expect(names).To(ContainElement("My HTTPRoute"), "HTTPRoute missing")
				g.Expect(names).To(ContainElement("My GRPCRoute"), "GRPCRoute missing")
				g.Expect(names).To(ContainElement("My TLSRoute"), "TLSRoute missing")
				g.Expect(names).To(ContainElement("My TCPRoute"), "TCPRoute missing")
			}, 3*time.Minute, 3*time.Second).Should(Succeed())

			By("verifying link sources are set correctly")
			dr, err := lc.pf.fetchDashboard()
			Expect(err).NotTo(HaveOccurred())
			Expect(linkSource(dr, routeGroup, "My Ingress")).To(Equal("ingress"))
			Expect(linkSource(dr, routeGroup, "My IngressRoute")).To(Equal("ingressroute"))
			Expect(linkSource(dr, routeGroup, "My HTTPRoute")).To(Equal("httproute"))
			Expect(linkSource(dr, routeGroup, "My GRPCRoute")).To(Equal("grpcroute"))
			Expect(linkSource(dr, routeGroup, "My TLSRoute")).To(Equal("tlsroute"))
			Expect(linkSource(dr, routeGroup, "My TCPRoute")).To(Equal("tcproute"))
			// Service source is "service" or "endpointslice" depending on how discovered.
			svcSrc := linkSource(dr, routeGroup, "My Service")
			Expect(svcSrc).To(Or(Equal("service"), Equal("endpointslice")))
		})
	})
})
