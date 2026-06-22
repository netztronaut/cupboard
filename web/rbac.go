package web

// +kubebuilder:rbac:groups=dashboard.netztronaut.de,resources=bookmarkgroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=forecastle.stakater.com,resources=forecastleapps,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tcproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints,verbs=get;list;watch
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch
// +kubebuilder:rbac:groups=traefik.containo.us,resources=ingressroutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=traefik.io,resources=ingressroutes,verbs=get;list;watch
