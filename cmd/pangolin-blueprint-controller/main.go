package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/hirrrooo/pangolin-blueprint-controller/internal/controller"
)

type options struct {
	output     string
	debounce   time.Duration
	kubeconfig string
	healthAddr string
	logLevel   string
}

func main() {
	options := parseFlags()
	logger, err := newLogger(options.logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, options, logger); err != nil {
		logger.Error("controller stopped", "error", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var value options
	flag.StringVar(&value.output, "output", "/var/run/pangolin/blueprint.yaml", "blueprint output path")
	flag.DurationVar(&value.debounce, "debounce", 750*time.Millisecond, "quiet period before regenerating the blueprint")
	flag.StringVar(&value.kubeconfig, "kubeconfig", "", "kubeconfig path; empty uses in-cluster configuration")
	flag.StringVar(&value.healthAddr, "health-address", ":8080", "health and readiness listen address")
	flag.StringVar(&value.logLevel, "log-level", "info", "debug, info, warn, or error")
	flag.Parse()
	return value
}

func run(ctx context.Context, options options, logger *slog.Logger) error {
	runContext, stop := context.WithCancel(ctx)
	defer stop()
	config, err := kubernetesConfig(options.kubeconfig)
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	factory := informers.NewSharedInformerFactory(client, 0)
	serviceInformer := factory.Core().V1().Services().Informer()
	serviceController, err := controller.New(serviceInformer, options.output, options.debounce, logger)
	if err != nil {
		return err
	}

	server := healthServer(options.healthAddr, serviceController)
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("health server listening", "address", options.healthAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("health server: %w", err)
		}
	}()

	controllerErrors := make(chan error, 1)
	go func() { controllerErrors <- serviceController.Run(runContext) }()

	var runError error
	select {
	case <-ctx.Done():
	case runError = <-controllerErrors:
	case runError = <-serverErrors:
	}
	stop()

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil && runError == nil {
		runError = fmt.Errorf("shut down health server: %w", err)
	}
	return runError
}

func kubernetesConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig: %w", err)
		}
		return config, nil
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	return config, nil
}

func healthServer(address string, serviceController *controller.Controller) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(response http.ResponseWriter, _ *http.Request) {
		if !serviceController.Ready() {
			http.Error(response, "not ready", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusOK)
	})
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

func newLogger(level string) (*slog.Logger, error) {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed})), nil
}
