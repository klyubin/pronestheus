package pkg

import (
	"errors"
	"net/http"
	"os"

	"golang.org/x/oauth2"

	"github.com/go-kit/kit/log"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"pronestheus/pkg/collectors/nest"
	"pronestheus/pkg/collectors/nestapp"
	"pronestheus/pkg/collectors/weather"

	"github.com/prometheus/client_golang/prometheus"
)

// ExporterConfig contains configuration for the Exporter.
type ExporterConfig struct {
	ListenAddr            *string
	MetricsPath           *string
	Timeout               *int
	NestURL               *string
	NestOAuthClientID     *string
	NestOAuthClientSecret *string
	NestOAuthToken        *oauth2.Token // Only used to mock a dummy token in tests
	NestProjectID         *string
	NestRefreshToken      *string
	NestLabelSpaceToDash  *bool
	WeatherLocation       *string
	WeatherURL            *string
	WeatherToken          *string
	NestGoogleAuthURL     *string
	NestGoogleAuthCookies *string
}

// Exporter is a Prometheus exporter.
type Exporter struct {
	logger      log.Logger
	listenAddr  string
	metricsPath string
}

var logger log.Logger

// NewExporter creates a Prometheus exporter using the ExporterConfig and registers the collectors.
func NewExporter(cfg *ExporterConfig) (*Exporter, error) {
	logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	logger = log.With(logger, "ts", log.DefaultTimestampUTC)

	if err := registerNestCollector(cfg); err != nil {
		return nil, err
	}

	if err := registerWeatherCollector(cfg); err != nil {
		return nil, err
	}

	if err := registerNestAppCollector(cfg); err != nil {
		return nil, err
	}

	return &Exporter{
		logger:      logger,
		listenAddr:  *cfg.ListenAddr,
		metricsPath: *cfg.MetricsPath,
	}, nil
}

// Run starts the exporter server and listens for incoming scraping requests.
func (e *Exporter) Run() error {
	e.logger.Log("level", "debug", "msg", "Started ProNestheus - Nest Thermostat Prometheus Exporter")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>ProNestheus</title></head>
			<body>
			<h1>ProNestheus - Nest Thermostat Prometheus Exporter</h1>
			<p><a href="` + e.metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})

	http.Handle(e.metricsPath, promhttp.Handler())
	return http.ListenAndServe(e.listenAddr, nil)
}

func registerNestCollector(cfg *ExporterConfig) error {
	replaceSpacesWithDashesInLabel := false
	if cfg.NestLabelSpaceToDash != nil {
		replaceSpacesWithDashesInLabel = *cfg.NestLabelSpaceToDash
	}
	nestConfig := nest.Config{
		Logger:                         logger,
		Timeout:                        *cfg.Timeout,
		APIURL:                         *cfg.NestURL,
		OAuthClientID:                  *cfg.NestOAuthClientID,
		OAuthClientSecret:              *cfg.NestOAuthClientSecret,
		RefreshToken:                   *cfg.NestRefreshToken,
		ProjectID:                      *cfg.NestProjectID,
		OAuthToken:                     cfg.NestOAuthToken,
		ReplaceSpacesWithDashesInLabel: replaceSpacesWithDashesInLabel,
	}

	nestCollector, err := nest.New(nestConfig)
	if err != nil {
		return err
	}

	return prometheus.Register(nestCollector)
}

func registerWeatherCollector(cfg *ExporterConfig) error {
	// Don't create weather collector if WeatherToken is empty.
	if *cfg.WeatherToken == "" {
		return nil
	}

	weatherConfig := weather.Config{
		Logger:        logger,
		Timeout:       *cfg.Timeout,
		APIURL:        *cfg.WeatherURL,
		APIToken:      *cfg.WeatherToken,
		APILocationID: *cfg.WeatherLocation,
	}

	weatherCollector, err := weather.New(weatherConfig)
	if err != nil {
		return err
	}

	return prometheus.Register(weatherCollector)
}

func registerNestAppCollector(cfg *ExporterConfig) error {
	if cfg.NestGoogleAuthURL == nil || *cfg.NestGoogleAuthURL == "" {
		if cfg.NestGoogleAuthCookies != nil && *cfg.NestGoogleAuthCookies != "" {
			return errors.New("Cookies for Nest app provided, but the Google authentication URL not provided")
		}
		// This feature is not enabled
		return nil
	} else if cfg.NestGoogleAuthCookies == nil || *cfg.NestGoogleAuthCookies == "" {
		return errors.New("Google auth URL for the Nest app provided, but no cookies provided")
	}

	config := nestapp.Config{
		Logger:      logger,
		Timeout:     *cfg.Timeout,
		AuthURL:     *cfg.NestGoogleAuthURL,
		AuthCookies: *cfg.NestGoogleAuthCookies,
	}

	collector, err := nestapp.New(config)
	if err != nil {
		return err
	}

	return prometheus.Register(collector)
}
