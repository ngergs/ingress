package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/ngergs/ingress/server"
	"github.com/ngergs/ingress/state"
	"github.com/rs/zerolog/log"

	websrv "github.com/ngergs/websrv/server"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	setup()
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", filepath.Join(os.Getenv("HOME"), ".kube", "config"))
		if err != nil {
			log.Fatal().Err(err).Msg("eror reading in cluster config")
		}
	}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ingressStateManager := state.New(ctx, config, "nginx-kubeip")

	time.Sleep(time.Duration(5) * time.Second)

	errChan := make(chan error)
	server.Start(ingressStateManager, *httpPort, *httpsPort, errChan, hstsConfig,
		websrv.Optional(websrv.AccessLog(), *accessLog),
		websrv.RequestID())
	if *health {
		go func() {
			healthServer := websrv.Build(*healthPort,
				websrv.HealthCheckHandler(),
				websrv.Optional(websrv.AccessLog(), *healthAccessLog),
			)
			log.Info().Msgf("Starting healthcheck server on port %d", *healthPort)
			errChan <- healthServer.ListenAndServe()
		}()
	}
	for err := range errChan {
		log.Fatal().Err(err).Msg("Error starting server: %v")
	}

}
