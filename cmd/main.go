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

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	dashboardv1alpha1 "netztronaut.de/cupboard/api/dashboard/v1alpha1"
	forecastlev1alpha1 "netztronaut.de/cupboard/api/forecastle/v1alpha1"
	dashboardcontroller "netztronaut.de/cupboard/internal/controller/dashboard"
	webhookdashboardv1alpha1 "netztronaut.de/cupboard/internal/webhook/dashboard/v1alpha1"
	"netztronaut.de/cupboard/web"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(dashboardv1alpha1.AddToScheme(scheme))
	utilruntime.Must(forecastlev1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// setupCacheNamespaces configures the cache to watch specific namespace(s).
// It supports both single namespace ("ns1") and multi-namespace ("ns1,ns2,ns3") formats.
func setupCacheNamespaces(namespaces string) cache.Options {
	defaultNamespaces := make(map[string]cache.Config)
	for ns := range strings.SplitSeq(namespaces, ",") {
		defaultNamespaces[strings.TrimSpace(ns)] = cache.Config{}
	}
	return cache.Options{
		DefaultNamespaces: defaultNamespaces,
	}
}

func setupRuntimeConfig(localTesting bool) (*rest.Config, func(), error) {
	if !localTesting {
		return ctrl.GetConfigOrDie(), func() {}, nil
	}

	// Use kind cluster when local testing is requested.
	cfg, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err == nil {
		// Try to use the kind-cupboard context.
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: clientcmd.RecommendedHomeFile}
		configOverrides := &clientcmd.ConfigOverrides{CurrentContext: "kind-cupboard"}
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		cfg, err = clientConfig.ClientConfig()
		if err == nil {
			setupLog.Info("Using kind cluster (kind-cupboard)")
			return cfg, func() {}, nil
		}
	}

	return nil, nil, errors.New("LOCAL_TESTING=true requires kubeconfig context kind-cupboard")
}

func loadViperConfig(configFile string) (*viper.Viper, error) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	_ = v.BindEnv("watchNamespace", "WATCH_NAMESPACE")
	_ = v.BindEnv("localTesting", "LOCAL_TESTING")
	_ = v.BindEnv("enableWebhooks", "ENABLE_WEBHOOKS")
	_ = v.BindEnv("auth.enabled", "ENABLE_AUTH")
	_ = v.BindEnv("auth.cookieSecret", "CUPBOARD_COOKIE_SECRET")
	_ = v.BindEnv("auth.issuerURL", "OIDC_ISSUER_URL")
	_ = v.BindEnv("auth.clientID", "OIDC_CLIENT_ID")
	_ = v.BindEnv("auth.redirectPath", "OIDC_REDIRECT_PATH")
	_ = v.BindEnv("auth.scopes", "OIDC_SCOPES")
	_ = v.BindEnv("auth.userInfoEndpoint", "OIDC_USERINFO_ENDPOINT")
	_ = v.BindEnv("forecastle.instance", "CUPBOARD_FORECASTLE_INSTANCE")
	_ = v.BindEnv("page.title", "CUPBOARD_PAGE_TITLE")
	_ = v.BindEnv("page.faviconURL", "CUPBOARD_FAVICON_URL")
	_ = v.BindEnv("page.templateSet", "CUPBOARD_TEMPLATE_SET")
	_ = v.BindEnv("page.contentLayout", "CUPBOARD_CONTENT_LAYOUT")
	_ = v.BindEnv("sync.bindAddress", "CUPBOARD_SYNC_BIND_ADDRESS")
	_ = v.BindEnv("sync.tls.ca", "CUPBOARD_SYNC_TLS_CA")
	_ = v.BindEnv("sync.tls.cert", "CUPBOARD_SYNC_TLS_CERT")
	_ = v.BindEnv("sync.tls.key", "CUPBOARD_SYNC_TLS_KEY")
	_ = v.BindEnv("sync.tls.authCert", "CUPBOARD_SYNC_TLS_AUTH_CERT")
	_ = v.BindEnv("sync.tls.authKey", "CUPBOARD_SYNC_TLS_AUTH_KEY")

	if strings.TrimSpace(configFile) == "" {
		configFile = os.Getenv("CUPBOARD_CONFIG")
	}
	if strings.TrimSpace(configFile) != "" {
		v.SetConfigFile(configFile)
		if err := v.ReadInConfig(); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func explicitFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

func resolveStringFlag(config *viper.Viper, setFlags map[string]bool, flagName, configKey, current string) string {
	if setFlags[flagName] {
		return current
	}
	if config.IsSet(configKey) {
		return config.GetString(configKey)
	}
	return current
}

func resolveBoolFlag(config *viper.Viper, setFlags map[string]bool, flagName, configKey string, current bool) bool {
	if setFlags[flagName] {
		return current
	}
	if config.IsSet(configKey) {
		return config.GetBool(configKey)
	}
	return current
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func splitCommaFlag(s string) []string {
	var result []string
	for part := range strings.SplitSeq(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			result = append(result, v)
		}
	}
	return result
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var webAddr string
	var forecastleInstance string
	var secureMetrics bool
	var enableHTTP2 bool
	var configFile string
	enableAuth := true
	var tlsOpts []func(*tls.Config)
	var syncAddr string
	var syncURLsFlag string
	var syncSRVRecordsFlag string
	var syncTLSCA string
	var syncTLSCert string
	var syncTLSKey string
	var syncTLSAuthCert string
	var syncTLSAuthKey string
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&webAddr, "web-bind-address", ":8082", "The address the embedded web interface binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.BoolVar(&enableAuth, "enable-auth", enableAuth,
		"If set, API authentication is enabled. Can also be controlled via ENABLE_AUTH env var.")
	flag.StringVar(&forecastleInstance, "forecastle-instance", "",
		"Forecastle instance name used to filter ForecastleApp resources by spec.instance. Can also be controlled via CUPBOARD_FORECASTLE_INSTANCE env var.")
	flag.StringVar(&configFile, "config", "", "Path to the cupboard configuration file (yaml/json/toml). Can also be set via CUPBOARD_CONFIG.")
	flag.StringVar(&syncAddr, "sync-bind-address", ":8083", "The address the sync endpoint binds to. Leave empty to disable.")
	flag.StringVar(&syncURLsFlag, "sync-urls", "", "Comma-separated list of peer URLs to synchronize with.")
	flag.StringVar(&syncSRVRecordsFlag, "sync-srv-records", "", "Comma-separated list of DNS SRV record names for peer discovery.")
	flag.StringVar(&syncTLSCA, "sync-tls-ca", "", "Path to CA certificate used to verify sync peers (trust store). When set with --sync-tls-cert/key, enables mTLS.")
	flag.StringVar(&syncTLSCert, "sync-tls-cert", "", "Path to TLS certificate for the sync endpoint.")
	flag.StringVar(&syncTLSKey, "sync-tls-key", "", "Path to TLS private key for the sync endpoint.")
	flag.StringVar(&syncTLSAuthCert, "sync-tls-auth-cert", "", "Path to client certificate used when authenticating to sync peers. Defaults to --sync-tls-cert if unset.")
	flag.StringVar(&syncTLSAuthKey, "sync-tls-auth-key", "", "Path to client private key used when authenticating to sync peers. Defaults to --sync-tls-key if unset.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	config, err := loadViperConfig(configFile)
	if err != nil {
		setupLog.Error(err, "Failed to load configuration")
		os.Exit(1)
	}
	setFlags := explicitFlags(flag.CommandLine)
	metricsAddr = resolveStringFlag(config, setFlags, "metrics-bind-address", "metrics.bindAddress", metricsAddr)
	probeAddr = resolveStringFlag(config, setFlags, "health-probe-bind-address", "health.probeBindAddress", probeAddr)
	webAddr = resolveStringFlag(config, setFlags, "web-bind-address", "web.bindAddress", webAddr)
	enableLeaderElection = resolveBoolFlag(config, setFlags, "leader-elect", "leaderElection.enabled", enableLeaderElection)
	secureMetrics = resolveBoolFlag(config, setFlags, "metrics-secure", "metrics.secure", secureMetrics)
	webhookCertPath = resolveStringFlag(config, setFlags, "webhook-cert-path", "webhook.cert.path", webhookCertPath)
	webhookCertName = resolveStringFlag(config, setFlags, "webhook-cert-name", "webhook.cert.name", webhookCertName)
	webhookCertKey = resolveStringFlag(config, setFlags, "webhook-cert-key", "webhook.cert.key", webhookCertKey)
	metricsCertPath = resolveStringFlag(config, setFlags, "metrics-cert-path", "metrics.cert.path", metricsCertPath)
	metricsCertName = resolveStringFlag(config, setFlags, "metrics-cert-name", "metrics.cert.name", metricsCertName)
	metricsCertKey = resolveStringFlag(config, setFlags, "metrics-cert-key", "metrics.cert.key", metricsCertKey)
	enableHTTP2 = resolveBoolFlag(config, setFlags, "enable-http2", "http2.enabled", enableHTTP2)
	enableAuth = resolveBoolFlag(config, setFlags, "enable-auth", "auth.enabled", enableAuth)
	forecastleInstance = resolveStringFlag(config, setFlags, "forecastle-instance", "forecastle.instance", forecastleInstance)
	syncAddr = resolveStringFlag(config, setFlags, "sync-bind-address", "sync.bindAddress", syncAddr)
	syncTLSCA = resolveStringFlag(config, setFlags, "sync-tls-ca", "sync.tls.ca", syncTLSCA)
	syncTLSCert = resolveStringFlag(config, setFlags, "sync-tls-cert", "sync.tls.cert", syncTLSCert)
	syncTLSKey = resolveStringFlag(config, setFlags, "sync-tls-key", "sync.tls.key", syncTLSKey)
	syncTLSAuthCert = resolveStringFlag(config, setFlags, "sync-tls-auth-cert", "sync.tls.authCert", syncTLSAuthCert)
	syncTLSAuthKey = resolveStringFlag(config, setFlags, "sync-tls-auth-key", "sync.tls.authKey", syncTLSAuthKey)

	// URLs and SRV records may be arrays in the config file or comma-separated in flags.
	var syncURLs []string
	if setFlags["sync-urls"] {
		syncURLs = splitCommaFlag(syncURLsFlag)
	} else if config.IsSet("sync.urls") {
		syncURLs = config.GetStringSlice("sync.urls")
	}
	var syncSRVRecords []string
	if setFlags["sync-srv-records"] {
		syncSRVRecords = splitCommaFlag(syncSRVRecordsFlag)
	} else if config.IsSet("sync.srvRecords") {
		syncSRVRecords = config.GetStringSlice("sync.srvRecords")
	}

	localTesting := config.GetBool("localTesting")
	enableWebhooks := true
	if config.IsSet("enableWebhooks") {
		enableWebhooks = config.GetBool("enableWebhooks")
	}
	watchNamespace := strings.TrimSpace(config.GetString("watchNamespace"))
	if watchNamespace == "" {
		setupLog.Error(errors.New("WATCH_NAMESPACE must be set"), "Unable to get WATCH_NAMESPACE, the manager will watch and manage resources in all namespaces")
		os.Exit(1)
	}

	var staticLinks []web.StaticLink
	var linkGroups []web.LinkGroup
	if config.IsSet("linkGroups") {
		if err := config.UnmarshalKey("linkGroups", &linkGroups); err != nil {
			setupLog.Error(err, "Failed to parse linkGroups from configuration")
			os.Exit(1)
		}
	}
	if config.IsSet("dashboard.linkGroups") {
		var dashboardLinkGroups []web.LinkGroup
		if err := config.UnmarshalKey("dashboard.linkGroups", &dashboardLinkGroups); err != nil {
			setupLog.Error(err, "Failed to parse dashboard.linkGroups from configuration")
			os.Exit(1)
		}
		linkGroups = append(linkGroups, dashboardLinkGroups...)
	}
	if config.IsSet("staticLinks") {
		if err := config.UnmarshalKey("staticLinks", &staticLinks); err != nil {
			setupLog.Error(err, "Failed to parse staticLinks from configuration")
			os.Exit(1)
		}
	}
	if config.IsSet("dashboard.staticLinks") {
		var dashboardStaticLinks []web.StaticLink
		if err := config.UnmarshalKey("dashboard.staticLinks", &dashboardStaticLinks); err != nil {
			setupLog.Error(err, "Failed to parse dashboard.staticLinks from configuration")
			os.Exit(1)
		}
		staticLinks = append(staticLinks, dashboardStaticLinks...)
	}

	cfg, stopRuntime, err := setupRuntimeConfig(localTesting)
	if err != nil {
		setupLog.Error(err, "Failed to initialize runtime config")
		os.Exit(1)
	}
	defer stopRuntime()

	if localTesting {
		setupLog.Info("Started local envtest control plane")
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	// Configure manager options for namespace-scoped mode
	mgrOptions := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "7cd7701f.netztronaut.de",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	}

	// Configure cache to watch namespace(s) specified in WATCH_NAMESPACE
	mgrOptions.Cache = setupCacheNamespaces(watchNamespace)
	setupLog.Info("Watching namespace(s)", "namespaces", watchNamespace)

	mgr, err := ctrl.NewManager(cfg, mgrOptions)
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		setupLog.Error(err, "Failed to initialize discovery client")
		os.Exit(1)
	}

	notifier := web.NewDashboardNotifier()
	if err := mgr.Add(notifier); err != nil {
		setupLog.Error(err, "Failed to add dashboard notifier")
		os.Exit(1)
	}
	setupDashboardWatches(mgr, notifier, discoveryClient)

	syncOptions := web.SyncOptions{
		BindAddress: syncAddr,
		URLs:        syncURLs,
		SRVRecords:  syncSRVRecords,
		TLS: web.SyncTLSOptions{
			CA:       syncTLSCA,
			Cert:     syncTLSCert,
			Key:      syncTLSKey,
			AuthCert: syncTLSAuthCert,
			AuthKey:  syncTLSAuthKey,
		},
	}
	var syncClient *web.SyncClient
	if len(syncURLs) > 0 || len(syncSRVRecords) > 0 {
		syncClient, err = web.NewSyncClient(syncOptions)
		if err != nil {
			setupLog.Error(err, "Failed to initialize sync client")
			os.Exit(1)
		}
		if err := mgr.Add(syncClient); err != nil {
			setupLog.Error(err, "Failed to add sync client runnable")
			os.Exit(1)
		}
	}

	webHandler, err := web.NewHandler(mgr.GetClient(), discoveryClient, web.Options{
		Auth: web.AuthOptions{
			Enabled:             enableAuth,
			CookieSecret:        config.GetString("auth.cookieSecret"),
			IssuerURL:           config.GetString("auth.issuerURL"),
			ClientID:            config.GetString("auth.clientID"),
			RedirectPath:        config.GetString("auth.redirectPath"),
			Scopes:              config.GetString("auth.scopes"),
			UserInfoEndpointURL: config.GetString("auth.userInfoEndpoint"),
		},
		Forecastle: web.ForecastleOptions{
			Instance: forecastleInstance,
		},
		LinkGroups:  linkGroups,
		StaticLinks: staticLinks,
		Page: web.PageOptions{
			TemplateSet:   firstNonEmpty(config.GetString("page.templateSet"), config.GetString("web.templateSet")),
			Title:         firstNonEmpty(config.GetString("page.title"), config.GetString("web.title")),
			FaviconURL:    firstNonEmpty(config.GetString("page.faviconURL"), config.GetString("web.faviconURL")),
			ContentLayout: firstNonEmpty(config.GetString("page.contentLayout"), config.GetString("web.contentLayout")),
		},
	}, notifier, syncClient)
	if err != nil {
		setupLog.Error(err, "Failed to initialize embedded web interface")
		os.Exit(1)
	}

	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		server := &http.Server{
			Addr:              webAddr,
			Handler:           webHandler,
			ReadHeaderTimeout: 5 * time.Second,
		}

		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
				setupLog.Error(shutdownErr, "Failed to shutdown embedded web interface")
			}
		}()

		setupLog.Info("Starting embedded web interface", "address", webAddr)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	})); err != nil {
		setupLog.Error(err, "Failed to add embedded web interface runnable")
		os.Exit(1)
	}

	if strings.TrimSpace(syncAddr) != "" {
		syncTLSConfig, syncTLSErr := web.BuildSyncServerTLSConfig(syncOptions.TLS)
		if syncTLSErr != nil {
			setupLog.Error(syncTLSErr, "Failed to build sync server TLS configuration")
			os.Exit(1)
		}
		syncServerHandler := webHandler.NewSyncHandler()
		if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
			server := &http.Server{
				Addr:              syncAddr,
				Handler:           syncServerHandler,
				ReadHeaderTimeout: 5 * time.Second,
				TLSConfig:         syncTLSConfig,
			}
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
					setupLog.Error(shutdownErr, "Failed to shutdown sync endpoint")
				}
			}()
			setupLog.Info("Starting sync endpoint", "address", syncAddr, "tls", syncTLSConfig != nil)
			if syncTLSConfig != nil {
				if serveErr := server.ListenAndServeTLS("", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					return serveErr
				}
			} else {
				if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					return serveErr
				}
			}
			return nil
		})); err != nil {
			setupLog.Error(err, "Failed to add sync endpoint runnable")
			os.Exit(1)
		}
	}

	if err := (&dashboardcontroller.BookmarkGroupReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "dashboard-bookmarkgroup")
		os.Exit(1)
	}

	if err := (&dashboardcontroller.BookmarkReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "dashboard-bookmark")
		os.Exit(1)
	}
	// nolint:goconst
	if enableWebhooks {
		if err := webhookdashboardv1alpha1.SetupBookmarkGroupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "BookmarkGroup")
			os.Exit(1)
		}
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

// setupDashboardWatches registers cache informer event handlers so the notifier fires
// whenever a resource that affects the dashboard changes. Must be called before mgr.Start().
func setupDashboardWatches(mgr ctrl.Manager, notifier *web.DashboardNotifier, dc *discovery.DiscoveryClient) {
	notify := toolscache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { notifier.Notify() },
		UpdateFunc: func(_, _ interface{}) { notifier.Notify() },
		DeleteFunc: func(_ interface{}) { notifier.Notify() },
	}

	addWatch := func(obj ctrlclient.Object) {
		informer, err := mgr.GetCache().GetInformer(context.Background(), obj)
		if err != nil {
			setupLog.Error(err, "Failed to set up dashboard watch", "type", fmt.Sprintf("%T", obj))
			return
		}
		if _, err := informer.AddEventHandler(notify); err != nil {
			setupLog.Error(err, "Failed to add dashboard event handler", "type", fmt.Sprintf("%T", obj))
		}
	}

	// Always-available resources.
	addWatch(&dashboardv1alpha1.BookmarkGroup{})
	addWatch(&networkingv1.Ingress{})
	addWatch(&corev1.Service{})

	resourceAvailable := func(groupVersion, kind string) bool {
		list, err := dc.ServerResourcesForGroupVersion(groupVersion)
		if err != nil {
			return false
		}
		for _, r := range list.APIResources {
			if r.Kind == kind {
				return true
			}
		}
		return false
	}

	// Optional: ForecastleApp.
	if resourceAvailable("forecastle.stakater.com/v1alpha1", "ForecastleApp") {
		addWatch(&forecastlev1alpha1.ForecastleApp{})
	}

	// Optional: HTTPRoute (unstructured, CRD may not exist).
	if resourceAvailable("gateway.networking.k8s.io/v1", "HTTPRoute") {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"})
		addWatch(u)
	}
}
