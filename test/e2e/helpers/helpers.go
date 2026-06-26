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

// Package helpers provides shared types and utilities for the e2e test suites.
package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// ManagerImage is the manager image built and loaded by each suite's BeforeAll.
const ManagerImage = "example.com/cupboard:v0.0.1"

// DashboardResponse is the minimal subset of web.DashboardResponse used by tests.
type DashboardResponse struct {
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

// PortForwarder keeps a kubectl port-forward process alive by restarting it
// whenever it exits.  It exposes the dashboard API via a local Go HTTP client.
type PortForwarder struct {
	Kubeconfig string
	Namespace  string
	Service    string
	RemotePort int
	LocalPort  int
	Client     *http.Client

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPortForwarder allocates a free local port and starts the background
// goroutine that keeps kubectl port-forward alive.
func NewPortForwarder(kubeconfig, namespace, service string, remotePort int) (*PortForwarder, error) {
	localPort, err := FreePort()
	if err != nil {
		return nil, fmt.Errorf("finding free port: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pf := &PortForwarder{
		Kubeconfig: kubeconfig,
		Namespace:  namespace,
		Service:    service,
		RemotePort: remotePort,
		LocalPort:  localPort,
		ctx:        ctx,
		cancel:     cancel,
		Client:     &http.Client{Timeout: 120 * time.Second},
	}
	pf.wg.Add(1)
	go pf.loop()
	return pf, nil
}

func (pf *PortForwarder) loop() {
	defer pf.wg.Done()
	for {
		cmd := exec.CommandContext(pf.ctx, "kubectl",
			"--kubeconfig", pf.Kubeconfig,
			"port-forward",
			"-n", pf.Namespace,
			"svc/"+pf.Service,
			fmt.Sprintf("%d:%d", pf.LocalPort, pf.RemotePort),
		)
		_ = cmd.Run()
		select {
		case <-pf.ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// Close stops the port-forward goroutine and waits for it to exit.
func (pf *PortForwarder) Close() {
	pf.cancel()
	pf.wg.Wait()
}

// FetchDashboard calls /api/dashboard via the local port and returns the decoded response.
func (pf *PortForwarder) FetchDashboard() (DashboardResponse, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/dashboard", pf.LocalPort)
	resp, err := pf.Client.Get(url) //nolint:noctx
	if err != nil {
		return DashboardResponse{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DashboardResponse{}, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	var dr DashboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return DashboardResponse{}, fmt.Errorf("decode response from %s: %w", url, err)
	}
	return dr, nil
}

// FreePort returns an available TCP port on localhost.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}
