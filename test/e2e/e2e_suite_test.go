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

package e2e

import (
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	// managerImage is the manager image built and loaded by each suite's BeforeAll.
	managerImage = "example.com/cupboard:v0.0.1"
)

// TestE2E runs the e2e test suite to validate the solution in an isolated environment.
// Each suite (Manager, Lifecycle, ForeignCluster) manages its own cluster lifecycle,
// so any label-filtered subset can be run directly with go test -tags=e2e:
//
//	go test -tags=e2e ./test/e2e/                                  # all suites
//	go test -tags=e2e ./test/e2e/ --ginkgo.label-filter=lifecycle  # lifecycle only
//	go test -tags=e2e ./test/e2e/ --ginkgo.label-filter=fleet      # fleet only
//
// To use a pre-provisioned cluster, set KUBECONFIG before running; cluster
// creation and deletion are skipped automatically.
//
// To enable kubectl kuberc (use custom kubectl configurations), set: KUBECTL_KUBERC=true
// By default, kuberc is disabled to ensure consistent test behavior across different environments.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting cupboard e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	configureKubectlKubeRC()
})

// configureKubectlKubeRC disables kubectl kuberc by default for test isolation.
// To enable kuberc, set: KUBECTL_KUBERC=true
func configureKubectlKubeRC() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		By("disabling kubectl kuberc for test isolation")
		err := os.Setenv("KUBECTL_KUBERC", "false")
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to disable kubectl kuberc")
		_, _ = fmt.Fprintf(GinkgoWriter,
			"kubectl kuberc disabled for consistent test behavior (override with KUBECTL_KUBERC=true)\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "kubectl kuberc enabled (KUBECTL_KUBERC=true)\n")
	}
}
