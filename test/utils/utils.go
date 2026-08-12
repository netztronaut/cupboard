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

package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" // nolint:revive,staticcheck
)

const (
	certmanagerVersion = "v1.20.2"
	certmanagerURLTmpl = "https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml"

	defaultK3dCluster = "cupboard-test-e2e"
	// K3sImage is the k3s image used for all test clusters.
	K3sImage = "rancher/k3s:v1.36.1-k3s1"
)

func warnError(err error) {
	_, _ = fmt.Fprintf(GinkgoWriter, "warning: %v\n", err)
}

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) (string, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "chdir dir: %q\n", err)
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%q failed with error %q: %w", command, string(output), err)
	}

	return string(output), nil
}

// UninstallCertManager uninstalls the cert manager
func UninstallCertManager() {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion)
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}

	// Delete leftover leases in kube-system (not cleaned by default)
	kubeSystemLeases := []string{
		"cert-manager-cainjector-leader-election",
		"cert-manager-controller",
	}
	for _, lease := range kubeSystemLeases {
		cmd = exec.Command("kubectl", "delete", "lease", lease,
			"-n", "kube-system", "--ignore-not-found", "--force", "--grace-period=0")
		if _, err := Run(cmd); err != nil {
			warnError(err)
		}
	}
}

// InstallCertManager installs the cert manager bundle.
func InstallCertManager() error {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion)
	cmd := exec.Command("kubectl", "apply", "-f", url)
	if _, err := Run(cmd); err != nil {
		return err
	}
	// Wait for cert-manager-webhook to be ready, which can take time if cert-manager
	// was re-installed after uninstalling on a cluster.
	cmd = exec.Command("kubectl", "wait", "deployment.apps/cert-manager-webhook",
		"--for", "condition=Available",
		"--namespace", "cert-manager",
		"--timeout", "5m",
	)
	if _, err := Run(cmd); err != nil {
		return err
	}
	// Also wait for cainjector, which populates the caBundle in the webhook
	// configuration.  Without this the API server cannot verify the webhook TLS
	// certificate and all cert-manager resource creation fails with
	// "x509: certificate signed by unknown authority".
	cmd = exec.Command("kubectl", "wait", "deployment.apps/cert-manager-cainjector",
		"--for", "condition=Available",
		"--namespace", "cert-manager",
		"--timeout", "5m",
	)
	if _, err := Run(cmd); err != nil {
		return err
	}
	// Poll until the ValidatingWebhookConfiguration has a non-empty caBundle,
	// confirming the cainjector has completed CA injection.
	for range 60 {
		cmd = exec.Command("kubectl", "get",
			"validatingwebhookconfiguration/cert-manager-webhook",
			"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}",
		)
		out, err := Run(cmd)
		if err == nil && strings.TrimSpace(out) != "" {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for cert-manager caBundle injection into ValidatingWebhookConfiguration")
}

// IsCertManagerCRDsInstalled checks if any Cert Manager CRDs are installed
// by verifying the existence of key CRDs related to Cert Manager.
func IsCertManagerCRDsInstalled() bool {
	// List of common Cert Manager CRDs
	certManagerCRDs := []string{
		"certificates.cert-manager.io",
		"issuers.cert-manager.io",
		"clusterissuers.cert-manager.io",
		"certificaterequests.cert-manager.io",
		"orders.acme.cert-manager.io",
		"challenges.acme.cert-manager.io",
	}

	// Execute the kubectl command to get all CRDs
	cmd := exec.Command("kubectl", "get", "crds")
	output, err := Run(cmd)
	if err != nil {
		return false
	}

	// Check if any of the Cert Manager CRDs are present
	crdList := GetNonEmptyLines(output)
	for _, crd := range certManagerCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// K3dBinary returns the k3d binary path, honouring the K3D env var.
func K3dBinary() string {
	if v, ok := os.LookupEnv("K3D"); ok && v != "" {
		return v
	}
	return "k3d"
}

// DefaultK3dClusterName returns the test cluster name, honouring K3D_CLUSTER env var.
func DefaultK3dClusterName() string {
	if v, ok := os.LookupEnv("K3D_CLUSTER"); ok && v != "" {
		return v
	}
	return defaultK3dCluster
}

// EnsureK3dCluster creates a fresh k3d cluster with the given name, deleting any
// existing cluster with the same name first to guarantee a clean, reproducible state.
func EnsureK3dCluster(name string) error {
	_, _ = Run(exec.Command(K3dBinary(), "cluster", "delete", name))
	_, err := Run(exec.Command(K3dBinary(), "cluster", "create", name, "--image", K3sImage))
	return err
}

// SetupK3dKubeconfig writes the named cluster's kubeconfig to a temp file, sets
// KUBECONFIG, and returns a cleanup function that removes the file and restores the
// previous KUBECONFIG value.
func SetupK3dKubeconfig(clusterName string) (func(), error) {
	prev := os.Getenv("KUBECONFIG")
	out, err := Run(exec.Command(K3dBinary(), "kubeconfig", "write", clusterName))
	if err != nil {
		return nil, err
	}
	kubeconfigPath := strings.TrimSpace(out)
	if err := os.Setenv("KUBECONFIG", kubeconfigPath); err != nil {
		return nil, err
	}
	return func() {
		if prev == "" {
			_ = os.Unsetenv("KUBECONFIG")
		} else {
			_ = os.Setenv("KUBECONFIG", prev)
		}
		_ = os.Remove(kubeconfigPath)
	}, nil
}

// DeleteK3dCluster deletes a k3d cluster, ignoring errors.
func DeleteK3dCluster(name string) {
	_, _ = Run(exec.Command(K3dBinary(), "cluster", "delete", name))
}

// LoadImageToCluster imports a local Docker image into the k3d test cluster.
func LoadImageToCluster(name string) error {
	cluster := DefaultK3dClusterName()
	cmd := exec.Command(K3dBinary(), "image", "import", name, "--cluster", cluster)
	_, err := Run(cmd)
	return err
}

// DefaultKubeconfig returns the kubeconfig path to use for the active test cluster.
// It honours the KUBECONFIG env var; when unset it falls back to ~/.kube/config.
func DefaultKubeconfig() string {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		return kc
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.kube/config"
}

var forwardedPortPattern = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) ->`)

// PortForwardGet starts a `kubectl port-forward` to podPort on the given pod, issues a
// single HTTP GET to path, and returns the response body. The port-forward is torn down
// before returning, whether or not the request succeeded.
func PortForwardGet(namespace, pod string, podPort int, path string) (string, error) {
	cmd := exec.Command("kubectl", "port-forward", "-n", namespace, "pod/"+pod, fmt.Sprintf(":%d", podPort))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to open port-forward stdout: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start port-forward: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	localPort, err := readForwardedPort(stdout)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", localPort, path))
	if err != nil {
		return "", fmt.Errorf("failed to GET %s via port-forward: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return string(body), fmt.Errorf("unexpected status %d from %s", resp.StatusCode, path)
	}
	return string(body), nil
}

// readForwardedPort scans kubectl port-forward's stdout for the local port it bound to.
func readForwardedPort(r io.Reader) (int, error) {
	type result struct {
		port int
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if m := forwardedPortPattern.FindStringSubmatch(line); m != nil {
				port, err := strconv.Atoi(m[1])
				ch <- result{port: port, err: err}
				return
			}
		}
		ch <- result{err: fmt.Errorf("port-forward exited before reporting a local port: %w", scanner.Err())}
	}()

	select {
	case res := <-ch:
		return res.port, res.err
	case <-time.After(15 * time.Second):
		return 0, fmt.Errorf("timed out waiting for port-forward to report a local port")
	}
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.SplitSeq(output, "\n")
	for element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is by walking up
// the directory tree until it finds a go.mod file.
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, fmt.Errorf("failed to get current working directory: %w", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return wd, fmt.Errorf("could not find project root (go.mod) from %s", wd)
}

// UncommentCode searches for target in the file and remove the comment prefix
// of the target content. The target content may span multiple lines.
func UncommentCode(filename, target, prefix string) error {
	// false positive
	// nolint:gosec
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %q: %w", filename, err)
	}
	strContent := string(content)

	idx := strings.Index(strContent, target)
	if idx < 0 {
		return fmt.Errorf("unable to find the code %q to be uncommented", target)
	}

	out := new(bytes.Buffer)
	_, err = out.Write(content[:idx])
	if err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewBufferString(target))
	if !scanner.Scan() {
		return nil
	}
	for {
		if _, err = out.WriteString(strings.TrimPrefix(scanner.Text(), prefix)); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
		// Avoid writing a newline in case the previous line was the last in target.
		if !scanner.Scan() {
			break
		}
		if _, err = out.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
	}

	if _, err = out.Write(content[idx+len(target):]); err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	// false positive
	// nolint:gosec
	if err = os.WriteFile(filename, out.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %w", filename, err)
	}

	return nil
}
