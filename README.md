# Release Cataloger

Release Cataloger is a lightweight Go-based service designed to fetch, cache, and expose information about a GitHub Project's releases via a REST API.

Data are populated in runtime against Github API, and cached in Memory.

## Features 🚀

- **Centralized Release Information**: Retrieves all release data directly from GitHub, ensuring GitHub remains the single source of truth, eliminating the need for separate databases and/or manual data entry.
- **REST API**: Provides a standardized API interface for seamless integration with various services and CI/CD pipelines.
- **Stateless Design**: The service is designed stateless, simplifying scaling and management.
- **Caching**: Cache release data in memory, serving data from this local cache at all times. A cron job periodically refreshes this cache to ensure data remains up-to-date.
- **Retry Logic**: gracefully handles failures when interacting with the GitHub API.
- **Filtering**: Supports basic filtering of releases by criteria such as RC, drafts, and prerelease versions.
- **Secure and Coordinated Refresh**: Manual cache refresh via a POST request to  route: `/refresh`, with functionality of updating all pods if running under k8s 🚀. Access control is managed at the ALB level.
- **Kubernetes Ready**:
  - Stateless architecture for easy scaling.
  - `/refresh` logic, to propagate to all pods in the deployment
  - `/ready` endpoint for readiness probe
  - Prometheus metrics
  - Configuration through env variables.
  - Containerized

## API Routes 🌐

- `GET /releases`: Fetch the cached list of releases.
    - `include-rc=true|false`: Include release candidates (default: false).
    - `include-draft=true|false`: Include draft releases (default: false).
    - `include-prerelease=true|false`: Include prerelease versions (default: false).
- `POST /refresh`: Manually refresh the releases. Usefull when integrating with a CI pipeline.
- `GET /ready`: Check if the server is ready.
- `GET /metrics`: Expose Prometheus metrics.

## Examples 💡

### Example 1: Fetching Releases

```bash
curl -s -X GET "http://localhost:8080/releases" | jq
```
```json

[...]
```


### Example 2: Fetching Releases Including Prereleases and RC Versions
```bash
curl -s -X GET "http://localhost:8080/releases?include-prerelease=true&include-rc=true" | jq
```
```json

[...]
```


## Metrics 📈

  - `requests_total`: Tracks the total number of API requests handled.
  - `requests_total_github_api`: Tracks the total number of requests made to the GitHub API.
  - `github_api_request_latency_seconds`: Observes the latency of GitHub API requests.
  - `local_server_request_latency_seconds`: Observes the latency of requests to the local server.

## Configuration ⚙️

The following environment variables can be configured:

| Environment Variable | Description                                                                                                                                                 | Default Value |
|----------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------|
| `GITHUB_REPO`      | he GitHub repository in the format owner/repo (e.g., phoinixgrr/release-cataloger).                                                                                       | None   |
| `GITHUB_TOKEN`       | The GitHub token used for authentication. This is optional, but highly recommended to be injected, since without it requests towards GitHub API will be subjected to rate limiting. A GitHub token with readonly repo scope is enough. | None          |
| `VERBOSITY_LEVEL`    | Sets the logging level (e.g., INFO, DEBUG, WARN).                                                                                                           | INFO          |
| `MAX_RETRY_COUNT`    | The maximum number of retry attempts for fetching releases from the GitHub API.                                                                              | 5             |
| `INITIAL_DELAY`      | The initial delay before retrying a failed GitHub API request.                                                                                               | 2s            |
| `CRON_SCHEDULE`      | The cron schedule for refreshing the release data against GitHub API.                                                                                        | @every 24h    |



## Makefile targets 🎯

The Makefile includes the following targets:
```
make help                           to get help
make build                          to build
make release                        to build and release artifacts
make package                        to build, package
make sign                           to sign the artifact and perform verification
make lint                           to lint
make test                           to test
make patch                          to bump patch version (semver)
make minor                          to bump minor version (semver)
make major                          to bump major version (semver)
make package-software               to package the binary
make docker-build                   to build the docker image
make docker-build-single            to build the docker image
make docker-push                    to push the docker image
make docker-sign                    to sign the docker image
make docker-verify                  to verify the docker image
make docker-sbom                    to print a sbom report
make docker-scan                    to print a vulnerability report
make docker-lint                    to lint the Dockerfile
make docker-login                   to login to a container registry
make go-build                       to build binaries
make go-run                         to run locally for development
make go-test                        to run tests
make go-mod-check                   to check go mod files consistency
make go-lint                        to lint go code
make go-doc                         to generate documentation
make check-modules                  Check outdated modules
make github-release                 to publish a release and relevant artifacts to GitHub
```

## ToDo

- Document the API using Swagger
- Better handling of /refresh propagation (master/slave election, state)
- Seperate the code in multiple files
- Tracing?!
- Grafana Dashboard
