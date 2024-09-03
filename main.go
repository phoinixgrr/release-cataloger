package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron/v3"
	logrus "github.com/sirupsen/logrus"
	"golang.org/x/mod/semver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultMaxRetryCount = 5
	defaultInitialDelay  = 2 * time.Second
	defaultCronSchedule  = "@every 24h"    // Default cron schedule
	refreshCooldown      = 2 * time.Minute // Cooldown period to prevent refresh loops
)

var (
	releases         []Release
	mu               sync.RWMutex // Use RWMutex for concurrent read/write access
	maxRetryCount    int
	initialDelay     time.Duration
	verbosity        string
	ready            bool // Indicates if the server is ready to serve traffic
	releasesFetched  bool // Indicates if the releases have been fetched successfully
	initialFetchDone bool
	cronSchedule     string
	lastRefreshTime  time.Time // Track the last time a refresh was handled
	githubRepo       string    // GitHub repository name, e.g., "owner/repo"

	// LDFLAGS
	gitVersion      = ""
	buildHash       = ""
	buildTagLatest  = ""
	buildTagCurrent = ""
	gitTreeState    = ""
	buildDate       = ""

	// Prometheus metrics
	requestCounter     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "requests_total", Help: "Total number of API requests handled"}, []string{"endpoint"})
	githubAPICounter   = prometheus.NewCounter(prometheus.CounterOpts{Name: "requests_total_github_api", Help: "Total number of requests made to the GitHub API"})
	githubAPILatency   = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "github_api_request_latency_seconds", Help: "Latency of requests to the GitHub API in seconds", Buckets: prometheus.DefBuckets})
	localServerLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "local_server_request_latency_seconds", Help: "Latency of requests to the local server in seconds", Buckets: prometheus.DefBuckets}, []string{"endpoint"})
)

// Defining Structs to hold GitHub's response on "List Releases"
// https://docs.github.com/en/rest/releases/releases?apiVersion=2022-11-28#list-releases
type Author struct {
	Login             string `json:"login"`
	ID                int    `json:"id"`
	NodeID            string `json:"node_id"`
	AvatarURL         string `json:"avatar_url"`
	GravatarID        string `json:"gravatar_id"`
	URL               string `json:"url"`
	HTMLURL           string `json:"html_url"`
	FollowersURL      string `json:"followers_url"`
	FollowingURL      string `json:"following_url"`
	GistsURL          string `json:"gists_url"`
	StarredURL        string `json:"starred_url"`
	SubscriptionsURL  string `json:"subscriptions_url"`
	OrganizationsURL  string `json:"organizations_url"`
	ReposURL          string `json:"repos_url"`
	EventsURL         string `json:"events_url"`
	ReceivedEventsURL string `json:"received_events_url"`
	Type              string `json:"type"`
	SiteAdmin         bool   `json:"site_admin"`
}

type Asset struct {
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ID                 int    `json:"id"`
	NodeID             string `json:"node_id"`
	Name               string `json:"name"`
	Label              string `json:"label"`
	State              string `json:"state"`
	ContentType        string `json:"content_type"`
	Size               int    `json:"size"`
	DownloadCount      int    `json:"download_count"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
	Uploader           Author `json:"uploader"`
}

type Release struct {
	URL             string  `json:"url"`
	HTMLURL         string  `json:"html_url"`
	AssetsURL       string  `json:"assets_url"`
	UploadURL       string  `json:"upload_url"`
	TarballURL      string  `json:"tarball_url"`
	ZipballURL      string  `json:"zipball_url"`
	ID              int     `json:"id"`
	NodeID          string  `json:"node_id"`
	TagName         string  `json:"tag_name"`
	TargetCommitish string  `json:"target_commitish"`
	Name            string  `json:"name"`
	Body            string  `json:"body"`
	Draft           bool    `json:"draft"`
	Prerelease      bool    `json:"prerelease"`
	CreatedAt       string  `json:"created_at"`
	PublishedAt     string  `json:"published_at"`
	Author          Author  `json:"author"`
	Assets          []Asset `json:"assets"`
}

func init() {
	// Initialize logging with default format
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	// Print build/version information at startup
	logrus.Infof("Build Info:")
	logrus.Infof("Application Information:")
	logrus.Infof("  Git Version      : %s", gitVersion)
	logrus.Infof("  Build Hash       : %s", buildHash)
	logrus.Infof("  Build Tag Latest : %s", buildTagLatest)
	logrus.Infof("  Build Tag Current: %s", buildTagCurrent)
	logrus.Infof("  Git Tree State   : %s", gitTreeState)
	logrus.Infof("  Build Date       : %s", buildDate)

	// Initialize verbosity level
	verbosity = os.Getenv("VERBOSITY_LEVEL")
	if verbosity == "" {
		verbosity = "INFO"
	}
	level, err := logrus.ParseLevel(verbosity)
	if err != nil {
		logrus.WithError(err).Fatal("Invalid verbosity level")
	}
	logrus.SetLevel(level)

	// Initialize maxRetryCount and initialDelay from environment variables
	maxRetryCount = defaultMaxRetryCount
	if val, exists := os.LookupEnv("MAX_RETRY_COUNT"); exists {
		if intVal, err := strconv.Atoi(val); err == nil {
			maxRetryCount = intVal
		}
	}

	initialDelay = defaultInitialDelay
	if val, exists := os.LookupEnv("INITIAL_DELAY"); exists {
		if dur, err := time.ParseDuration(val); err == nil {
			initialDelay = dur
		}
	}

	// Initialize GitHub repository from environment variable
	githubRepo = os.Getenv("GITHUB_REPO")
	if githubRepo == "" {
		logrus.Fatal("GITHUB_REPO environment variable is not set")
	}

	// Initialize cron schedule from environment variable
	cronSchedule = os.Getenv("CRON_SCHEDULE")
	if cronSchedule == "" {
		cronSchedule = defaultCronSchedule
	}

	// Register Prometheus metrics
	prometheus.MustRegister(requestCounter)
	prometheus.MustRegister(githubAPICounter)
	prometheus.MustRegister(githubAPILatency)
	prometheus.MustRegister(localServerLatency)

	// Print all configuration settings
	logrus.Infof("Config Info:")
	logrus.Infof("  VERBOSITY_LEVEL: %s", verbosity)
	logrus.Infof("  MAX_RETRY_COUNT: %d", maxRetryCount)
	logrus.Infof("  INITIAL_DELAY: %s", initialDelay)
	logrus.Infof("  GITHUB_REPO: %s", githubRepo)
	logrus.Infof("  CRON_SCHEDULE: %s", cronSchedule)
	if _, exists := os.LookupEnv("GITHUB_TOKEN"); exists {
		logrus.Infof("  GITHUB_TOKEN: [set]")
	} else {
		logrus.Warn("  GITHUB_TOKEN: [not set]. We may hit rate limiting when accessing GitHub API")
	}
}

// Middleware to measure latency for local server requests
func prometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()
		c.Next()
		latency := time.Since(startTime).Seconds()
		localServerLatency.WithLabelValues(c.FullPath()).Observe(latency)
	}
}

// fetchReleases(). Contacts GitHub API and gets the releases. All of the response of GitHub API is stored in memory in the [Release, Asset, Author] structs.
func fetchReleases() ([]Release, error) {
	var allReleases []Release
	client := &http.Client{}
	token, tokenExists := os.LookupEnv("GITHUB_TOKEN")
	page := 1
	retries := 0

	for {
		req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases?page=%d", githubRepo, page), nil)
		if err != nil {
			return nil, err
		}

		// Set the necessary headers for the request
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		if tokenExists {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		// Increment the GitHub API request counter
		githubAPICounter.Inc()

		startTime := time.Now()
		resp, err := client.Do(req)
		latency := time.Since(startTime).Seconds()
		githubAPILatency.Observe(latency) // Record the latency

		if err != nil {
			logrus.WithError(err).Error("Request failed")
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			// Reset retries counter on successful request
			retries = 0

			var pageReleases []Release
			if err := json.NewDecoder(resp.Body).Decode(&pageReleases); err != nil {
				logrus.WithError(err).Error("Failed to decode JSON")
				return nil, err
			}

			if len(pageReleases) == 0 {
				break
			}

			allReleases = append(allReleases, pageReleases...)
			logrus.WithField("page", page).Info("Fetched page")
			page++
		} else {
			retries++
			logrus.WithField("status", resp.StatusCode).Warn("GitHub API returned non-200 status, retrying...")

			if retries >= maxRetryCount {
				return nil, fmt.Errorf("failed to fetch releases after %d attempts", maxRetryCount)
			}

			// Exponential backoff
			sleepDuration := initialDelay * time.Duration(1<<retries)
			logrus.WithField("retry_in", sleepDuration).Warn("Sleeping before retrying...")
			time.Sleep(sleepDuration)
		}
	}
	return allReleases, nil
}

// updateReleases() initiates a cache update against GitHub API
func updateReleases(isManual bool) {
	logrus.Info("Starting update of releases")

	// Skip initial cache update only during the startup phase
	if !isManual && initialFetchDone {
		logrus.Info("Initial fetch already done, skipping")
		return
	}
	// Fetch releases and store them in a temporary variable
	newReleases, err := fetchReleases()
	if err != nil {
		logrus.WithError(err).Error("Error fetching releases, keeping old cache")
		return
	}

	releases = newReleases
	releasesFetched = true // Mark that releases have been successfully fetched
	initialFetchDone = true
	ready = true // Mark the server as ready to serve
	logrus.Info("Releases updated successfully")
}

// Handling the "/releases" route
// 1. Filters RC, Draft, Prerelease
// 2. Calculates dockerhub links
// 3. Does Semver sorting
func getReleases(c *gin.Context) {

	// Locking the access to the releases slice since we are going to update it.
	mu.RLock()
	defer mu.RUnlock()

	if !releasesFetched {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Releases not fetched yet"})
		return
	}

	requestCounter.WithLabelValues("/releases").Inc()

	includeRC := c.Query("include-rc")
	includeDraft := c.Query("include-draft")
	includePrerelease := c.Query("include-prerelease")

	// Regex to match -rcX suffix
	rcRegex := regexp.MustCompile(`-rc\d+$`)

	filteredReleases := releases

	// Filter releases based on the options provided
	var validReleases []Release
	for _, release := range filteredReleases {
		// Exclude draft releases unless includeDraft is true
		if !release.Draft || includeDraft == "true" {
			// Exclude prereleases unless includePrerelease is true
			if !release.Prerelease || includePrerelease == "true" {
				// Exclude RC releases unless includeRC is true
				if includeRC == "true" || !rcRegex.MatchString(release.TagName) {
					validReleases = append(validReleases, release)
				}
			}
		}
	}

	// Sort the valid releases by version using semver
	sort.Slice(validReleases, func(i, j int) bool {
		return semver.Compare(validReleases[i].TagName, validReleases[j].TagName) > 0
	})

	// Prepare the json response
	type ReleaseInfo struct {
		Version     string `json:"version"`
		GithubURL   string `json:"github_url"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
		PublishedAt string `json:"published_at"`
		Prerelease  bool   `json:"prerelease"`
		Draft       bool   `json:"draft"`
		DockerImage string `json:"docker_image"`
	}

	var releaseInfos []ReleaseInfo
	for _, release := range validReleases {
		// Remove the "v" prefix from the version for Docker image tags
		versionWithoutV := strings.TrimPrefix(release.TagName, "v")

		releaseInfos = append(releaseInfos, ReleaseInfo{
			Version:     release.TagName,
			GithubURL:   release.HTMLURL,
			Description: release.Body,
			CreatedAt:   release.CreatedAt,
			PublishedAt: release.PublishedAt,
			Prerelease:  release.Prerelease, // Populate Prerelease field
			Draft:       release.Draft,      // Populate Draft field
			DockerImage: fmt.Sprintf("%s:%s", githubRepo, versionWithoutV),
		})
	}

	// Sort the releaseInfos by version using semver
	sort.Slice(releaseInfos, func(i, j int) bool {
		return semver.Compare(releaseInfos[i].Version, releaseInfos[j].Version) > 0
	})

	c.JSON(http.StatusOK, releaseInfos)
}

// Handling the "/refresh" route
func refreshReleases(c *gin.Context) {

	// Do not refresh if we have refreshed very recently (length of cooldown)
	if time.Since(lastRefreshTime) < refreshCooldown {
		logrus.Warn("Refresh request ignored due to cooldown period")
		// Returning HTTP 202(HTTP/Accepted), to report that we have already updated in the last 2 minutes (length of cooldown)
		// We are not failing, we just report we have updated very recently
		c.JSON(http.StatusAccepted, gin.H{"status": "Cooldown. please try again later"})
		return
	}

	// We are Go to start refresh!
	lastRefreshTime = time.Now()
	logrus.Info("Manual refresh of releases triggered")
	requestCounter.WithLabelValues("/refresh").Inc()
	updateReleases(true)
	c.JSON(http.StatusOK, gin.H{"status": "Releases refreshed"})

	// Propagate refresh to other pods
	propagateRefreshToOtherPods()

}

// propagateRefreshToOtherPods() Checks if we are running under k8s. If we do, then takes a list of all the pods
// in the deployment, and sends POST /refresh to all. This is to ensure that all pods are refreshed when receiving a /refresh in the ClusterIP/Ingress hitting a single pod.
// Since we are running stateless, the receiving pod can initiate a cascading effect with /refresh being sent in a loop from all the pods in the deployment.
// To tackle this, we have introduced the cooldown period to drop /refresh requests when we have updated recently.
// Since we aim to be stateless, this is a simple way to tackle this when running in HA(replicaset>1)
// without introducing state(k-v storage, NFS, database) or slave/master voting.
func propagateRefreshToOtherPods() {
	logrus.Info("Starting to propagate refresh to other pods")

	config, err := rest.InClusterConfig()
	if err != nil {
		logrus.WithError(err).Warn("Failed to create in-cluster config. Most probably we are not running under k8s")
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logrus.WithError(err).Error("Failed to create Kubernetes client")
		return
	}

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		logrus.Error("POD_NAMESPACE environment variable is not set. This should have been handled by the Helm Chart!")
		return
	}

	podName := os.Getenv("POD_NAME")
	if podName == "" {
		logrus.Error("POD_NAME environment variable is not set. This should have been handled by the Helm Chart!")
		return
	}

	appLabel := os.Getenv("APP_LABEL")
	if appLabel == "" {
		logrus.Error("APP_LABEL environment variable is not set. This should have been handled by the Helm Chart!")
		return
	}

	logrus.Infof("Listing pods in namespace: %s with label: %s", podNamespace, appLabel)
	pods, err := clientset.CoreV1().Pods(podNamespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", appLabel),
	})
	if err != nil {
		logrus.WithError(err).Error("Failed to list pods")
		return
	}

	// Print the list of pods found
	if len(pods.Items) > 0 {
		logrus.Infof("Found %d pods:", len(pods.Items))
		for _, pod := range pods.Items {
			logrus.Infof("Pod Name: %s, Pod IP: %s", pod.Name, pod.Status.PodIP)
		}
	} else {
		logrus.Info("No pods found!")
	}

	if len(pods.Items) > 0 {
		// Define an HTTP client with a timeout to send the actual POST /refresh to the other pods of the deployment
		client := &http.Client{
			Timeout: 20 * time.Second, // Set timeout to 20 seconds
		}
		for _, pod := range pods.Items {
			if pod.Name != podName && pod.Status.PodIP != "" {
				go func(podName string, podIP string) {
					url := fmt.Sprintf("http://%s:8080/refresh", podIP)
					logrus.Infof("Triggering refresh on pod %s at %s", podName, url)
					resp, err := client.Post(url, "application/json", nil)
					if err != nil {
						logrus.WithError(err).Errorf("Failed to trigger refresh on pod %s", podName)
					} else {
						logrus.Infof("Triggered refresh on pod %s successfully", podName)
						resp.Body.Close()
					}
				}(pod.Name, pod.Status.PodIP)
			}
		}
	}
	logrus.Info("Finished propagating refresh to other pods")
}

// Handling the "/ready" route
func readyState(c *gin.Context) {
	if ready {
		c.String(http.StatusOK, "Ready")
	} else {
		c.String(http.StatusServiceUnavailable, "Not Ready")
	}
}

// Handling the "/" route
func rootHandler(c *gin.Context) {
	c.String(http.StatusOK, "release-cataloger")
}

func main() {
	// Set up the API server with default Gin logger
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.Use(prometheusMiddleware()) // Use the middleware to measure local latency

	// Prometheus metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Ready state endpoint
	r.GET("/ready", readyState)

	// API endpoints
	r.GET("/releases", getReleases)
	r.POST("/refresh", refreshReleases)

	// Root endpoint
	r.GET("/", rootHandler)

	// Start the server in a separate goroutine so it's ready to serve /ready immediately
	go func() {
		logrus.Info("Starting the server...")
		if err := r.Run(":8080"); err != nil {
			logrus.Fatal("Server failed to start")
		}
	}()

	// Perform the initial fetch to ensure the server has data to serve
	updateReleases(false)

	// Mark the server as ready once the initial cache refresh against GitHub API is completed
	ready = true

	// Create a new cron instance
	c := cron.New()

	// Schedule the cron to run the refresh job at the specified interval
	_, err := c.AddFunc(cronSchedule, func() {
		updateReleases(true)
	})
	if err != nil {
		logrus.Fatalf("Could not start the scheduled updates: %v", err)
	} else {
		logrus.Infof("Successfully started the scheduled updates with cron schedule: %s", cronSchedule)
	}

	// Start the cron (we do not run the cron on first run)
	c.Start()

	// Block the main thread to keep the server running
	select {}
}
