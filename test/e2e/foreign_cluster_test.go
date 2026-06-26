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

// Package e2e tests the foreign-cluster (fleet) feature end-to-end.
//
// Four k3d clusters are created in each test run:
//   - cupboard-e2e-alpha   ("alpha")
//   - cupboard-e2e-beta    ("beta")
//   - cupboard-e2e-gamma   ("gamma")
//   - cupboard-e2e-delta   ("delta")
//
// All four clusters are arranged in a full mesh: every cluster's cupboard
// instance is configured with fleet entries pointing at all three peers.
//
// Cross-cluster networking is established by connecting each cluster's
// API-server container to every other cluster's Docker network with a DNS
// alias, so that pods inside any cluster can resolve the API-server hostname
// of every other cluster.
//
// Cupboard is deployed to all clusters from dist/install.yaml with auth
// disabled (ENABLE_AUTH=false) so the dashboard API can be called without
// OIDC credentials.
//
// Dashboard HTTP access (:8082) uses kubectl port-forward to a ClusterIP
// Service backed by the manager pod, with an auto-restarting goroutine so
// transient disconnects (e.g. pod restarts, leader-election reloads) are
// transparent to callers.  Go's net/http client is used directly, keeping all
// assertions in the Go testing layer.
//
// Tests cover:
//  1. Basic replication       – an annotated Ingress on beta appears on alpha, gamma, delta
//  2. Annotation gate         – Ingresses without the replicate annotation do NOT replicate
//  3. Source tagging          – links carry "foreign:<endpoint>:ingress" source prefix
//  4. Group placement         – replicated links land in the correct group
//  5. Loop prevention         – a resource on alpha appears once (local) in alpha and
//                               once (foreign) in each of beta/gamma/delta; never echoed
//  6. Isolation               – alpha's own resources are never shown as "foreign" in alpha
//  7. Service replication     – annotated Services replicate the same way as Ingresses
//  8. Graceful degradation    – a cluster's dashboard returns HTTP 200 when one peer is
//                               unreachable; remaining peers' resources still appear
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"netztronaut.de/cupboard/test/utils"
)

// ---- constants ---------------------------------------------------------------

const (
	fcAlpha = "cupboard-e2e-alpha"
	fcBeta  = "cupboard-e2e-beta"
	fcGamma = "cupboard-e2e-gamma"
	fcDelta = "cupboard-e2e-delta"
	fcNS    = "cupboard-system"

	// dashboardServiceName is the ClusterIP Service created in each cluster for
	// port-forwarding to the cupboard dashboard API (port 8082).
	dashboardServiceName = "cupboard-web-test"

	// Cupboard annotations / labels used in test manifests.
	annEnabled   = "cupboard.netztronaut.de/enabled"
	annReplicate = "cupboard.netztronaut.de/replicate"
	annGroup     = "cupboard.netztronaut.de/group"
	annName      = "cupboard.netztronaut.de/name"
	annURL       = "cupboard.netztronaut.de/url"
	labelEnabled = "cupboard.netztronaut.de/enabled"
)

// fcClusters is the ordered list of all four fleet clusters.
var fcClusters = []string{fcAlpha, fcBeta, fcGamma, fcDelta}

// ---- shared state ------------------------------------------------------------

var fc struct {
	// kubeconfig holds the standard (local-server) kubeconfig for each cluster.
	kubeconfig map[string]string
	// fleetKubeconfig holds the merged fleet kubeconfig (internal server URL,
	// insecure TLS) written into each cluster's fleet Secret.
	fleetKubeconfig map[string]string
	// pf holds the auto-restarting port-forwarder for each cluster's dashboard.
	pf     map[string]*portForwarder
	tmpDir string
}

// ---- small types -------------------------------------------------------------

// peerSpec describes one entry in CUPBOARD_FLEET_CLUSTERS.
type peerSpec struct {
	endpoint string // https://k3d-<cluster>-server-0:6443
	context  string // k3d-<cluster>
}

// dashboardResponse is the minimal subset of web.DashboardResponse used by tests.
type dashboardResponse struct {
	Groups []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Links  []struct {
			Name   string `json:"name"`
			URL    string `json:"url"`
			Source string `json:"source"`
		} `json:"links"`
	} `json:"groups"`
}

// ---- port-forward ------------------------------------------------------------

// portForwarder keeps a kubectl port-forward process alive by restarting it
// whenever it exits.  It exposes the dashboard API via a local Go HTTP client.
type portForwarder struct {
	kubeconfig string
	namespace  string
	service    string
	remotePort int
	localPort  int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	client *http.Client
}

// newPortForwarder allocates a free local port and starts the background
// goroutine that keeps kubectl port-forward alive.
func newPortForwarder(kubeconfig, namespace, service string, remotePort int) (*portForwarder, error) {
	localPort, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("finding free port: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pf := &portForwarder{
		kubeconfig: kubeconfig,
		namespace:  namespace,
		service:    service,
		remotePort: remotePort,
		localPort:  localPort,
		ctx:        ctx,
		cancel:     cancel,
		client:     &http.Client{Timeout: 120 * time.Second},
	}
	pf.wg.Add(1)
	go pf.loop()
	return pf, nil
}

// loop restarts kubectl port-forward whenever it exits until the context is
// cancelled.
func (pf *portForwarder) loop() {
	defer pf.wg.Done()
	for {
		cmd := exec.CommandContext(pf.ctx, "kubectl",
			"--kubeconfig", pf.kubeconfig,
			"port-forward",
			"-n", pf.namespace,
			"svc/"+pf.service,
			fmt.Sprintf("%d:%d", pf.localPort, pf.remotePort),
		)
		// Discard output; failures are surfaced via HTTP errors to callers.
		_ = cmd.Run()
		select {
		case <-pf.ctx.Done():
			return
		case <-time.After(2 * time.Second):
			// Brief pause before restart to avoid tight-looping when the pod
			// is not yet ready.
		}
	}
}

// close stops the port-forward goroutine and waits for it to exit.
func (pf *portForwarder) close() {
	pf.cancel()
	pf.wg.Wait()
}

// fetchDashboard calls /api/dashboard via the local port and returns the
// decoded response.
func (pf *portForwarder) fetchDashboard() (dashboardResponse, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/dashboard", pf.localPort)
	resp, err := pf.client.Get(url) //nolint:noctx
	if err != nil {
		return dashboardResponse{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return dashboardResponse{}, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	var dr dashboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return dashboardResponse{}, fmt.Errorf("decode response from %s: %w", url, err)
	}
	return dr, nil
}

// freePort returns an available TCP port on localhost.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// ---- naming helpers ----------------------------------------------------------

func k3dBinary() string { return utils.K3dBinary() }

func serverContainer(cluster string) string { return "k3d-" + cluster + "-server-0" }
func dockerNetwork(cluster string) string   { return "k3d-" + cluster }
func internalAPIEndpoint(cluster string) string {
	return "https://" + serverContainer(cluster) + ":6443"
}
func kubeconfigContext(cluster string) string { return "k3d-" + cluster }

// ---- command helpers ---------------------------------------------------------

func runKubectl(kubeconfig string, args ...string) (string, error) {
	return utils.Run(exec.Command("kubectl",
		append([]string{"--kubeconfig", kubeconfig}, args...)...))
}

func runK3d(args ...string) (string, error) {
	return utils.Run(exec.Command(k3dBinary(), args...))
}

func runDocker(args ...string) (string, error) {
	return utils.Run(exec.Command("docker", args...))
}

// ---- fleet setup helpers -----------------------------------------------------

// prepareFleetKubeconfig writes a merged fleet kubeconfig for targetCluster
// that contains all four cluster contexts with internal server URLs and
// insecure TLS verification disabled.
func prepareFleetKubeconfig(targetCluster, destPath string) error {
	var paths []string
	for _, c := range fcClusters {
		p := filepath.Join(fc.tmpDir, c+"-raw.yaml")
		raw, err := runK3d("kubeconfig", "get", c)
		if err != nil {
			return fmt.Errorf("k3d kubeconfig get %s: %w", c, err)
		}
		if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
			return err
		}
		paths = append(paths, p)
	}

	mergeCmd := exec.Command("kubectl", "config", "view", "--flatten", "--merge")
	mergeCmd.Env = append(os.Environ(), "KUBECONFIG="+strings.Join(paths, ":"))
	merged, err := utils.Run(mergeCmd)
	if err != nil {
		return fmt.Errorf("merging kubeconfigs: %w", err)
	}
	if err := os.WriteFile(destPath, []byte(merged), 0600); err != nil {
		return err
	}

	for _, c := range fcClusters {
		ctx := kubeconfigContext(c)
		if _, err := runKubectl(destPath, "config", "set-cluster", ctx,
			"--server="+internalAPIEndpoint(c), "--kubeconfig="+destPath); err != nil {
			return fmt.Errorf("set-cluster server for %s: %w", c, err)
		}
		if _, err := runKubectl(destPath, "config", "set-cluster", ctx,
			"--insecure-skip-tls-verify=true", "--kubeconfig="+destPath); err != nil {
			return fmt.Errorf("disable TLS for %s: %w", c, err)
		}
	}
	return nil
}

// installFleetConfig creates the fleet-kubeconfig Secret in targetCluster,
// then patches the cupboard Deployment with:
//   - CUPBOARD_FLEET_CLUSTERS (comma-separated endpoint=context pairs)
//   - CUPBOARD_FLEET_KUBECONFIG (/etc/fleet/kubeconfig)
//   - ENABLE_AUTH=false (so the dashboard API is accessible without OIDC)
//   - the fleet-kubeconfig volume + mount
//
// All test resources are created in fcNS (cupboard-system) so the manager's
// default WATCH_NAMESPACE (= metadata.namespace = cupboard-system) covers them.
func installFleetConfig(targetKubeconfig, fleetKubeconfigPath string, peers []peerSpec) error {
	out, err := runKubectl(targetKubeconfig,
		"create", "secret", "generic", "fleet-kubeconfig",
		"--from-file=kubeconfig="+fleetKubeconfigPath,
		"-n", fcNS)
	if err != nil && !strings.Contains(out+err.Error(), "already exists") {
		return fmt.Errorf("creating fleet-kubeconfig secret: %w", err)
	}

	parts := make([]string, len(peers))
	for i, p := range peers {
		parts[i] = p.endpoint + "=" + p.context
	}
	clustersValue := strings.Join(parts, ",")

	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{`+
		`"containers":[{"name":"manager","env":[`+
		`{"name":"CUPBOARD_FLEET_CLUSTERS","value":%q},`+
		`{"name":"CUPBOARD_FLEET_KUBECONFIG","value":"/etc/fleet/kubeconfig"},`+
		`{"name":"ENABLE_AUTH","value":"false"}],`+
		`"volumeMounts":[{"name":"fleet-kubeconfig","mountPath":"/etc/fleet","readOnly":true}]}],`+
		`"volumes":[{"name":"fleet-kubeconfig","secret":{"secretName":"fleet-kubeconfig"}}]}}}}`,
		clustersValue)

	_, err = runKubectl(targetKubeconfig,
		"patch", "deployment", "cupboard-controller-manager",
		"-n", fcNS, "--type=strategic", "--patch", patch)
	return err
}

// createWebService creates a ClusterIP Service forwarding port 8082 to the
// cupboard manager pod.  Used as the target for kubectl port-forward.
func createWebService(kubeconfig string) error {
	svc := fmt.Sprintf(`apiVersion: v1
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
`, dashboardServiceName, fcNS)
	p := filepath.Join(fc.tmpDir, filepath.Base(kubeconfig)+"-svc.yaml")
	if err := os.WriteFile(p, []byte(svc), 0644); err != nil {
		return err
	}
	_, err := runKubectl(kubeconfig, "apply", "-f", p)
	return err
}

// ---- test helpers ------------------------------------------------------------

func fetchDashboard(cluster string) (dashboardResponse, error) {
	return fc.pf[cluster].fetchDashboard()
}

func findLinks(resp dashboardResponse, pred func(group, name, url, source string) bool) []struct {
	Group, Name, URL, Source string
} {
	var out []struct{ Group, Name, URL, Source string }
	for _, g := range resp.Groups {
		for _, l := range g.Links {
			if pred(g.Name, l.Name, l.URL, l.Source) {
				out = append(out, struct{ Group, Name, URL, Source string }{g.Name, l.Name, l.URL, l.Source})
			}
		}
	}
	return out
}

func countLinks(resp dashboardResponse, name string) int {
	return len(findLinks(resp, func(_, n, _, _ string) bool { return n == name }))
}

func writeManifest(name, content string) (string, error) {
	p := filepath.Join(fc.tmpDir, name+".yaml")
	return p, os.WriteFile(p, []byte(content), 0644)
}

func waitForRollout(kubeconfig string) error {
	_, err := runKubectl(kubeconfig,
		"rollout", "status", "deployment/cupboard-controller-manager",
		"-n", fcNS, "--timeout=5m")
	return err
}

// ---- k3d / cert-manager helpers ----------------------------------------------

func loadImageIntoCluster(image, cluster string) error {
	_, err := utils.Run(exec.Command(k3dBinary(), "image", "import", image, "--cluster", cluster))
	return err
}

func installCertManagerForCluster(kubeconfig string) error {
	prev := os.Getenv("KUBECONFIG")
	_ = os.Setenv("KUBECONFIG", kubeconfig)
	defer func() { _ = os.Setenv("KUBECONFIG", prev) }()
	// Wait for the API server to be ready before applying cert-manager.
	// Freshly created k3d clusters can have a brief window where the API server
	// is not yet accepting connections, causing kubectl to fail with EOF.
	for range 30 {
		cmd := exec.Command("kubectl", "get", "nodes", "--request-timeout=5s")
		if _, err := utils.Run(cmd); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return utils.InstallCertManager()
}

// ---- main test suite ---------------------------------------------------------

var _ = Describe("ForeignCluster", Ordered, Label("fleet"), func() {

	BeforeAll(func() {
		var err error
		fc.tmpDir, err = os.MkdirTemp("", "cupboard-fleet-e2e-*")
		Expect(err).NotTo(HaveOccurred())
		// Register tmpDir removal first so it runs last (LIFO).
		DeferCleanup(func() {
			By("cleaning up temp directory")
			_ = os.RemoveAll(fc.tmpDir)
		})

		fc.kubeconfig = make(map[string]string, len(fcClusters))
		fc.fleetKubeconfig = make(map[string]string, len(fcClusters))
		fc.pf = make(map[string]*portForwarder, len(fcClusters))

		// ---- 1. Build image and create all four k3d clusters ------------------
		// Build the manager image before creating clusters so the image exists
		// for import.  DeferCleanup is registered immediately after each
		// successful cluster creation so the cluster is deleted even if
		// BeforeAll fails partway through or the test run is interrupted.

		By("building manager image")
		_, err = utils.Run(exec.Command("make", "docker-build", "IMG="+managerImage))
		Expect(err).NotTo(HaveOccurred(), "Failed to build manager image")

		for _, c := range fcClusters {
			c := c
			By("removing any leftover k3d cluster " + c)
			_, _ = runK3d("cluster", "delete", c)
			By("creating k3d cluster " + c)
			_, err = runK3d("cluster", "create", c)
			Expect(err).NotTo(HaveOccurred(), "cluster %s", c)
			DeferCleanup(func() {
				By("deleting k3d cluster " + c)
				_, _ = runK3d("cluster", "delete", c)
			})
		}

		// ---- 2. Full-mesh Docker network cross-connects ----------------------
		// For every ordered pair (A, B) with A≠B, connect B's API-server
		// container to A's Docker network under B's hostname as a DNS alias.

		By("cross-connecting Docker networks for full-mesh inter-cluster reachability")
		for _, target := range fcClusters {
			for _, guest := range fcClusters {
				if target == guest {
					continue
				}
				_, err = runDocker("network", "connect",
					"--alias", serverContainer(guest),
					dockerNetwork(target),
					serverContainer(guest))
				Expect(err).NotTo(HaveOccurred(),
					"connecting %s to network of %s", guest, target)
			}
		}

		// ---- 3. Build installer and load image into every cluster -----------

		By("building installer manifest")
		_, err = utils.Run(exec.Command("make", "build-installer", "IMG="+managerImage))
		Expect(err).NotTo(HaveOccurred())

		for _, c := range fcClusters {
			By("loading manager image into " + c)
			Expect(loadImageIntoCluster(managerImage, c)).To(Succeed())
		}

		// ---- 4. Store standard kubeconfigs ----------------------------------

		By("writing standard kubeconfigs")
		for _, c := range fcClusters {
			p := filepath.Join(fc.tmpDir, c+".yaml")
			raw, err := runK3d("kubeconfig", "get", c)
			Expect(err).NotTo(HaveOccurred(), "kubeconfig for %s", c)
			Expect(os.WriteFile(p, []byte(raw), 0600)).To(Succeed())
			fc.kubeconfig[c] = p
		}

		// ---- 5. Install cert-manager on every cluster ----------------------

		for _, c := range fcClusters {
			By("installing cert-manager on " + c)
			Expect(installCertManagerForCluster(fc.kubeconfig[c])).To(Succeed())
		}

		// ---- 6. Deploy cupboard to every cluster ---------------------------

		for _, c := range fcClusters {
			By("deploying cupboard to " + c)
			_, err = runKubectl(fc.kubeconfig[c], "apply", "-f", "dist/install.yaml")
			Expect(err).NotTo(HaveOccurred(), "deploy to %s", c)
		}
		for _, c := range fcClusters {
			By("waiting for initial rollout on " + c)
			Expect(waitForRollout(fc.kubeconfig[c])).To(Succeed())
		}

		// ---- 7. Build merged fleet kubeconfigs (all four contexts) ----------

		By("building merged fleet kubeconfigs with internal server addresses")
		for _, c := range fcClusters {
			p := filepath.Join(fc.tmpDir, c+"-fleet.yaml")
			Expect(prepareFleetKubeconfig(c, p)).To(Succeed())
			fc.fleetKubeconfig[c] = p
		}

		// ---- 8. Install fleet config on every cluster ----------------------
		// Each cluster gets CUPBOARD_FLEET_CLUSTERS listing its three peers
		// and ENABLE_AUTH=false so the dashboard API is accessible.

		for _, c := range fcClusters {
			peers := make([]peerSpec, 0, len(fcClusters)-1)
			for _, peer := range fcClusters {
				if peer == c {
					continue
				}
				peers = append(peers, peerSpec{
					endpoint: internalAPIEndpoint(peer),
					context:  kubeconfigContext(peer),
				})
			}
			By(fmt.Sprintf("configuring fleet + disabling auth on %s", c))
			Expect(installFleetConfig(fc.kubeconfig[c], fc.fleetKubeconfig[c], peers)).To(Succeed())
		}
		for _, c := range fcClusters {
			By("waiting for fleet rollout on " + c)
			Expect(waitForRollout(fc.kubeconfig[c])).To(Succeed())
		}

		// ---- 9. Create dashboard ClusterIP Service on every cluster --------

		for _, c := range fcClusters {
			By("creating dashboard service on " + c)
			Expect(createWebService(fc.kubeconfig[c])).To(Succeed())
		}

		// ---- 10. Start auto-restarting port-forwards -----------------------

		for _, c := range fcClusters {
			c := c
			By("starting port-forward for " + c)
			pf, err := newPortForwarder(fc.kubeconfig[c], fcNS, dashboardServiceName, 8082)
			Expect(err).NotTo(HaveOccurred(), "port-forwarder for %s", c)
			fc.pf[c] = pf
			DeferCleanup(func() {
				By("stopping port-forward for " + c)
				pf.close()
			})
		}

		// ---- 11. Wait for dashboards to become reachable -------------------
		// The web server starts after leader election (~30 s); poll each
		// cluster until the dashboard responds with HTTP 200.

		for _, c := range fcClusters {
			cc := c
			By("waiting for dashboard on " + cc)
			Eventually(func() error {
				_, err := fetchDashboard(cc)
				return err
			}, 3*time.Minute, 5*time.Second).Should(Succeed(),
				"dashboard on %s did not become reachable within 3 minutes", cc)
		}
	})

	AfterAll(func() {
		// Resource cleanup (port-forwards, clusters, tmpDir) is handled by
		// DeferCleanup calls registered during BeforeAll.  This block only
		// dumps logs for post-mortem analysis on failure.
		if CurrentSpecReport().Failed() {
			for _, c := range fcClusters {
				if kc := fc.kubeconfig[c]; kc != "" {
					out, err := runKubectl(kc,
						"logs", "-l", "control-plane=controller-manager",
						"-n", fcNS, "--tail=50", "--all-containers")
					if err == nil {
						_, _ = fmt.Fprintf(GinkgoWriter,
							"\n=== %s manager logs (last 50 lines) ===\n%s\n", c, out)
					}
				}
			}
		}
	})

	AfterEach(func() {})

	SetDefaultEventuallyTimeout(3 * time.Minute)
	SetDefaultEventuallyPollingInterval(10 * time.Second)

	// ==========================================================================
	// Basic cross-cluster replication (beta → all)
	// ==========================================================================

	Context("basic cross-cluster replication", Ordered, func() {
		const (
			replicatedName    = "fleet-test-replicated"
			nonReplicatedName = "fleet-test-no-replicate"
			ingressNS         = fcNS
			testGroup         = "fleet-test-group"
		)

		BeforeAll(func() {
			By("creating replicated Ingress on " + fcBeta)
			p, err := writeManifest("beta-ingress-replicated", fmt.Sprintf(`
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  labels:
    %s: "true"
  annotations:
    %s: "%s"
    %s: "Fleet Test App"
    %s: "https://fleet-test.beta.example.com"
    %s: "true"
spec:
  rules:
  - host: fleet-test.beta.example.com
`, replicatedName, ingressNS, labelEnabled, annGroup, testGroup, annName, annURL, annReplicate))
			Expect(err).NotTo(HaveOccurred())
			_, err = runKubectl(fc.kubeconfig[fcBeta], "apply", "-f", p)
			Expect(err).NotTo(HaveOccurred())

			By("creating non-replicated Ingress on " + fcBeta)
			p2, err := writeManifest("beta-ingress-no-replicate", fmt.Sprintf(`
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  labels:
    %s: "true"
  annotations:
    %s: "%s"
    %s: "Fleet Test App (no-replicate)"
    %s: "https://no-replicate.beta.example.com"
spec:
  rules:
  - host: no-replicate.beta.example.com
`, nonReplicatedName, ingressNS, labelEnabled, annGroup, testGroup, annName, annURL))
			Expect(err).NotTo(HaveOccurred())
			_, err = runKubectl(fc.kubeconfig[fcBeta], "apply", "-f", p2)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			_, _ = runKubectl(fc.kubeconfig[fcBeta], "delete", "ingress",
				replicatedName, nonReplicatedName, "-n", ingressNS, "--ignore-not-found")
		})

		for _, consumer := range []string{fcAlpha, fcGamma, fcDelta} {
			consumer := consumer
			It(fmt.Sprintf("shows the replicated Ingress from beta in %s's dashboard", consumer), func() {
				Eventually(func(g Gomega) {
					resp, err := fetchDashboard(consumer)
					g.Expect(err).NotTo(HaveOccurred())
					links := findLinks(resp, func(_, name, _, _ string) bool { return name == "Fleet Test App" })
					g.Expect(links).To(HaveLen(1),
						"expected exactly one 'Fleet Test App' link on %s", consumer)
				}).Should(Succeed())
			})
		}

		It("does NOT show the non-replicated Ingress from beta in alpha", func() {
			resp, err := fetchDashboard(fcAlpha)
			Expect(err).NotTo(HaveOccurred())
			links := findLinks(resp, func(_, name, _, _ string) bool { return name == "Fleet Test App (no-replicate)" })
			Expect(links).To(BeEmpty(), "non-replicated link must NOT appear in alpha")
		})

		It("tags foreign links from beta with the beta endpoint as source prefix", func() {
			expectedPrefix := "foreign:" + internalAPIEndpoint(fcBeta)
			Eventually(func(g Gomega) {
				resp, err := fetchDashboard(fcAlpha)
				g.Expect(err).NotTo(HaveOccurred())
				links := findLinks(resp, func(_, name, _, _ string) bool { return name == "Fleet Test App" })
				g.Expect(links).To(HaveLen(1))
				g.Expect(links[0].Source).To(HavePrefix(expectedPrefix))
			}).Should(Succeed())
		})

		It("places the foreign link in the correct group on alpha", func() {
			Eventually(func(g Gomega) {
				resp, err := fetchDashboard(fcAlpha)
				g.Expect(err).NotTo(HaveOccurred())
				links := findLinks(resp, func(group, name, _, _ string) bool {
					return name == "Fleet Test App" && group == testGroup
				})
				g.Expect(links).To(HaveLen(1), "link must appear under group %q", testGroup)
			}).Should(Succeed())
		})

		It("the replicated Ingress appears locally on beta (not as foreign)", func() {
			Eventually(func(g Gomega) {
				resp, err := fetchDashboard(fcBeta)
				g.Expect(err).NotTo(HaveOccurred())
				links := findLinks(resp, func(_, name, _, _ string) bool { return name == "Fleet Test App" })
				g.Expect(links).To(HaveLen(1), "Fleet Test App must appear exactly once on beta")
				g.Expect(links[0].Source).NotTo(HavePrefix("foreign:"),
					"Fleet Test App must be local on its own cluster")
			}).Should(Succeed())
		})
	})

	// ==========================================================================
	// Loop prevention (full-mesh, 4 clusters)
	// ==========================================================================

	Context("loop prevention (full-mesh, 4 clusters)", Ordered, func() {
		// Each cluster hosts exactly one annotated Ingress unique to it.
		// After settling, every dashboard must contain:
		//   - exactly 1 copy of its own resource (local, not foreign)
		//   - exactly 1 copy of each peer's resource (foreign, from owning cluster)
		// Total per dashboard: 4 links, each appearing exactly once.

		type clusterIngress struct {
			cluster  string
			linkName string
		}

		clusterIngresses := []clusterIngress{
			{fcAlpha, "Loop-Test Alpha"},
			{fcBeta, "Loop-Test Beta"},
			{fcGamma, "Loop-Test Gamma"},
			{fcDelta, "Loop-Test Delta"},
		}

		BeforeAll(func() {
			for _, ci := range clusterIngresses {
				ci := ci
				ingressName := "fleet-loop-" + ci.cluster
				p, err := writeManifest("loop-ingress-"+ci.cluster, fmt.Sprintf(`
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  labels:
    %s: "true"
  annotations:
    %s: "loop-test"
    %s: %q
    %s: "https://loop-%s.example.com"
    %s: "true"
spec:
  rules:
  - host: loop-%s.example.com
`, ingressName, fcNS, labelEnabled, annGroup, annName, ci.linkName, annURL, ci.cluster, annReplicate, ci.cluster))
				Expect(err).NotTo(HaveOccurred())
				_, err = runKubectl(fc.kubeconfig[ci.cluster], "apply", "-f", p)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		AfterAll(func() {
			for _, ci := range clusterIngresses {
				ingressName := "fleet-loop-" + ci.cluster
				_, _ = runKubectl(fc.kubeconfig[ci.cluster], "delete", "ingress",
					ingressName, "-n", fcNS, "--ignore-not-found")
			}
		})

		for _, viewer := range fcClusters {
			viewer := viewer

			It(fmt.Sprintf("%s: own resource is local (not foreign)", viewer), func() {
				ownName := "Loop-Test " + clusterShortName(viewer)
				Eventually(func(g Gomega) {
					resp, err := fetchDashboard(viewer)
					g.Expect(err).NotTo(HaveOccurred())
					links := findLinks(resp, func(_, name, _, _ string) bool { return name == ownName })
					g.Expect(links).To(HaveLen(1), "%s must see its own resource exactly once", viewer)
					g.Expect(links[0].Source).NotTo(HavePrefix("foreign:"),
						"%s must see its own resource as local (not foreign)", viewer)
				}).Should(Succeed())
			})

			for _, owner := range fcClusters {
				if owner == viewer {
					continue
				}
				owner := owner
				ownerName := "Loop-Test " + clusterShortName(owner)
				expectedPrefix := "foreign:" + internalAPIEndpoint(owner)

				It(fmt.Sprintf("%s: sees %s's resource once as foreign (from %s)", viewer, owner, owner), func() {
					Eventually(func(g Gomega) {
						resp, err := fetchDashboard(viewer)
						g.Expect(err).NotTo(HaveOccurred())
						links := findLinks(resp, func(_, name, _, _ string) bool { return name == ownerName })
						g.Expect(links).To(HaveLen(1),
							"%s must see %s exactly once", viewer, ownerName)
						g.Expect(links[0].Source).To(HavePrefix(expectedPrefix))
					}).Should(Succeed())
				})
			}
		}

		It("sustained polling produces stable link counts across all four clusters", func() {
			for poll := 1; poll <= 5; poll++ {
				for _, viewer := range fcClusters {
					resp, err := fetchDashboard(viewer)
					Expect(err).NotTo(HaveOccurred(), "poll %d fetch %s", poll, viewer)
					for _, ci := range clusterIngresses {
						count := countLinks(resp, ci.linkName)
						Expect(count).To(Equal(1),
							"poll %d: %s must see %q exactly once; got %d", poll, viewer, ci.linkName, count)
					}
				}
				time.Sleep(5 * time.Second)
			}
		})
	})

	// ==========================================================================
	// Service replication (gamma → all)
	// ==========================================================================

	Context("Service replication", Ordered, func() {
		const (
			svcName  = "fleet-test-svc"
			svcNS    = fcNS
			svcGroup = "fleet-svc-group"
		)

		BeforeAll(func() {
			By("creating annotated Service on " + fcGamma)
			p, err := writeManifest("gamma-service", fmt.Sprintf(`
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels:
    %s: "true"
  annotations:
    %s: "%s"
    %s: "Fleet Service"
    %s: "https://fleet-svc.gamma.example.com"
    %s: "true"
spec:
  ports:
  - port: 80
    targetPort: 80
  selector:
    app: fleet-test
`, svcName, svcNS, labelEnabled, annGroup, svcGroup, annName, annURL, annReplicate))
			Expect(err).NotTo(HaveOccurred())
			_, err = runKubectl(fc.kubeconfig[fcGamma], "apply", "-f", p)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			_, _ = runKubectl(fc.kubeconfig[fcGamma], "delete", "service", svcName, "-n", svcNS, "--ignore-not-found")
		})

		for _, consumer := range []string{fcAlpha, fcBeta, fcDelta} {
			consumer := consumer
			expectedPrefix := "foreign:" + internalAPIEndpoint(fcGamma)

			It(fmt.Sprintf("annotated Service from gamma appears in %s's dashboard", consumer), func() {
				Eventually(func(g Gomega) {
					resp, err := fetchDashboard(consumer)
					g.Expect(err).NotTo(HaveOccurred())
					links := findLinks(resp, func(_, name, _, _ string) bool { return name == "Fleet Service" })
					g.Expect(links).To(HaveLen(1))
					g.Expect(links[0].Source).To(HavePrefix(expectedPrefix))
				}).Should(Succeed())
			})
		}
	})

	// ==========================================================================
	// Graceful degradation when one peer is unreachable
	// ==========================================================================

	Context("graceful degradation when one peer is unreachable", func() {
		It("alpha's dashboard stays up and shows delta's resources when gamma is paused", func() {
			// Create a resource on delta before pausing gamma, so we can verify
			// that the remaining peers are still reachable.
			p, err := writeManifest("delta-degrade-ingress", fmt.Sprintf(`
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: fleet-degrade-test
  namespace: %s
  labels:
    %s: "true"
  annotations:
    %s: "degrade-test"
    %s: "Degrade Test Delta"
    %s: "https://degrade.delta.example.com"
    %s: "true"
spec:
  rules:
  - host: degrade.delta.example.com
`, fcNS, labelEnabled, annGroup, annName, annURL, annReplicate))
			Expect(err).NotTo(HaveOccurred())
			_, err = runKubectl(fc.kubeconfig[fcDelta], "apply", "-f", p)
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				_, _ = runKubectl(fc.kubeconfig[fcDelta], "delete", "ingress",
					"fleet-degrade-test", "-n", fcNS, "--ignore-not-found")
			}()

			By("waiting for delta resource to appear on alpha before pausing gamma")
			Eventually(func(g Gomega) {
				resp, err := fetchDashboard(fcAlpha)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(countLinks(resp, "Degrade Test Delta")).To(Equal(1))
			}).Should(Succeed())

			By("pausing " + serverContainer(fcGamma) + " to simulate unreachable peer")
			_, err = runDocker("pause", serverContainer(fcGamma))
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				By("unpausing " + serverContainer(fcGamma))
				_, _ = runDocker("unpause", serverContainer(fcGamma))
			}()

			// Alpha must still respond with HTTP 200 and still show delta's resource.
			// Each fetchDashboard call may take ~20s while alpha waits for gamma's TLS
			// timeout, so we use an explicit 5-minute window with 30s polling.
			Eventually(func(g Gomega) {
				resp, err := fetchDashboard(fcAlpha)
				g.Expect(err).NotTo(HaveOccurred(), "alpha dashboard must respond when gamma is paused")
				g.Expect(countLinks(resp, "Degrade Test Delta")).To(Equal(1),
					"delta resources must still appear on alpha when only gamma is unreachable")
			}, "5m", "30s").Should(Succeed())
		})
	})
})

// clusterShortName returns the human-readable suffix of a cluster constant,
// e.g. "cupboard-e2e-alpha" → "Alpha".
func clusterShortName(cluster string) string {
	short := strings.TrimPrefix(cluster, "cupboard-e2e-")
	if len(short) == 0 {
		return cluster
	}
	return strings.ToUpper(short[:1]) + short[1:]
}
