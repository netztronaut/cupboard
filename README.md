# cupboard
`cupboard` is a namespaced, multigroup Kubernetes operator scaffolded with Kubebuilder and the following plugins:

- `go/v4`
- `helm/v2-alpha`
- `grafana/v1-alpha`
- `autoupdate/v1-alpha`
- `deploy-image/v1-alpha`

The repository module is `github.com/netztronaut/cupboard`, and APIs use the `netztronaut.de` domain.

## Description
The operator binary embeds a React/TypeScript web interface built from `web/` and serves it on `--web-bind-address` (default `:8082`).

The binary includes server-side dashboard template partials. A template set contains:

1. page template
2. header template
3. footer template
4. content template (list or grid layout)
5. group template
6. link template

Built-in template sets:

- `default` (`web/templates/default/`)
- `forecastle` (`web/templates/forecastle/`)

When a template set is loaded, each template file is resolved independently in this order:

1. `templates/<set>/<template-name>.tmpl` on the filesystem next to the manager binary
2. `templates/<set>/<template-name>.tmpl` from the embedded filesystem
3. `templates/default/<template-name>.tmpl` from the embedded filesystem
4. fail if none of those files exist

Icon libraries are embedded and served from local static assets (no external CDN dependency):

- Font Awesome (`fa-*`)
- Lucide (`lucide:<icon-name>`)
- Tabler (`tabler:<icon-name>`)
- Heroicons outline 24px (`hero:<icon-name>`)

The backend API provides:

- OpenAPI at `/api/openapi.json`
- Swagger UI at `/api/docs`
- OIDC/OAuth2 authentication middleware with userinfo lookup
- persistent signed userinfo cookie sessions

Authentication can be toggled with:

- flag `--enable-auth` (default: `true`)
- env `ENABLE_AUTH` (used as flag default)

Runtime configuration can also be loaded from a file via:

- flag `--config=/path/to/cupboard.yaml`
- env `CUPBOARD_CONFIG=/path/to/cupboard.yaml`
- env overrides for page rendering:
  - `CUPBOARD_PAGE_TITLE`
  - `CUPBOARD_FAVICON_URL`
  - `CUPBOARD_TEMPLATE_SET`
  - `CUPBOARD_CONTENT_LAYOUT`

The dashboard data model is grouped bookmarks:

- Groups with names (`BookmarkGroup.spec.name`, fallback `metadata.name`)
- Links with `name`, `url` or `urlFrom`, `target`, `icon`, and optional `groups`
- Optional static links from config file (`dashboard.staticLinks`)
- Optional link group metadata from config (`dashboard.linkGroups`): `priority`, `priorityClass` (`first`/`last`), `displayName`

The operator aggregates dashboard links from:

- `BookmarkGroup` custom resources (`dashboard.netztronaut.de/v1alpha1`)
- Forecastle CRs with 1:1 API compatibility:
  - `group: forecastle.stakater.com`
  - `kind: ForecastleApp`
  - `version: v1alpha1`
- labeled and annotated 3rd party resources (`Ingress`, `HTTPRoute`, `Service`)

For 3rd party resources, set:

- label `cupboard.netztronaut.de/enabled=true`
- annotations:
  - `cupboard.netztronaut.de/group`
  - `cupboard.netztronaut.de/name`
  - `cupboard.netztronaut.de/url` (optional when derivable, e.g. from Ingress/HTTPRoute host)
  - `cupboard.netztronaut.de/target`
  - `cupboard.netztronaut.de/icon` (preferred)
  - `cupboard.netztronaut.de/icon-url` (legacy compatibility)

Validation webhooks are enabled for `BookmarkGroup` and verify links are syntactically valid and reachable on create/update.

### OIDC / PKCE configuration

Set these environment variables for backend/frontend authentication:

- `OIDC_ISSUER_URL` (required)
- `OIDC_CLIENT_ID` (required)
- `OIDC_REDIRECT_PATH` (optional, default `/auth/callback`)
- `OIDC_SCOPES` (optional, default `openid profile email`)
- `OIDC_USERINFO_ENDPOINT` (optional; if omitted, discovered from issuer metadata)
- `CUPBOARD_COOKIE_SECRET` (recommended for production)

The frontend performs the PKCE authorization-code flow and authenticates to backend endpoints using bearer tokens. The backend fetches userinfo and persists it in an HTTP-only signed cookie. If userinfo contains a `groups` claim, the backend stores those groups in the session and uses them to filter links that declare `groups`.

### Configuration file

See `config/cupboard.example.yaml` for a full example. All existing flag/env-based options continue to work, and can now also be set in a viper-backed config file. Static links can be configured in:

```yaml
page:
  templateSet: "default"
  title: "cupboard"
  faviconURL: "/favicon.svg"
  contentLayout: "grid"

dashboard:
  linkGroups:
    - name: "code-devops"
      priority: 20
      priorityClass: "first"
      displayName: "Code & DevOps"
  staticLinks:
    - linkGroup: "code-devops"
      name: "GitHub"
      url: "https://github.com"
      target: "_blank"
      icon: "fa-github"
    - linkGroup: "code-devops"
      name: "Search"
      url: "https://duckduckgo.com"
      target: "_blank"
      icon: "lucide:search"
      groups:
        - "code-internal"
```

`/api/dashboard` returns `groups` plus `linkGroups` metadata with deterministic ordering based on `priorityClass`, `priority`, and name.

For a ready-to-run demo setup, use `config/cupboard.test.yaml` (5 groups / 20 links, auth disabled, envtest enabled):

```sh
CUPBOARD_CONFIG=./config/cupboard.test.yaml WATCH_NAMESPACE=default make run
```

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.
- Node.js and npm (for rebuilding the embedded web interface)

### Run locally

```sh
WATCH_NAMESPACE=default make run
```

Then open `http://localhost:8082` for the embedded web UI.
By default, local `make run` sets `ENABLE_WEBHOOKS=false` to avoid requiring webhook TLS certs; set `ENABLE_WEBHOOKS=true` when running with webhook certificates.
By default, local `make run` also sets `ENABLE_AUTH=false`; set `ENABLE_AUTH=true` (or `--enable-auth=true`) to enable OIDC auth locally.
To run without an external cluster, use `LOCAL_TESTING=true WATCH_NAMESPACE=default make run`; this starts an envtest API server with project CRDs.

The web UI reads aggregated dashboard data from `GET /api/dashboard` and listens on `GET /api/dashboard/updates` (WebSocket). On update pings it refreshes dashboard content client-side, throttled to at most once every 2 seconds.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/cupboard:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/cupboard:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/cupboard:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/cupboard/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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
