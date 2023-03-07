package main

import (
	"context"
	"flag"
	"net/http"

	"github.com/go-faster/errors"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/New-Earth-Lab/flicameraservice/internal/api"
	"github.com/New-Earth-Lab/flicameraservice/internal/app"
	"github.com/New-Earth-Lab/flicameraservice/internal/oas"
	"github.com/lirm/aeron-go/aeron"
)

func main() {
	app.Run(func(ctx context.Context, lg *zap.Logger) error {
		var arg struct {
			Addr          string
			MetricsAddr   string
			AeronUri      string
			AeronStreamId int
		}
		flag.StringVar(&arg.Addr, "addr", "127.0.0.1:8080", "listen address")
		flag.StringVar(&arg.MetricsAddr, "metrics.addr", "127.0.0.1:9090", "metrics listen address")
		flag.StringVar(&arg.AeronUri, "aeron.Uri", "aeron:ipc", "Aeron channel URI")
		flag.IntVar(&arg.AeronStreamId, "aeron.StreamId", 1001, "Aeron stream ID")
		flag.Parse()

		lg.Info("Initializing",
			zap.String("http.addr", arg.Addr),
			zap.String("metrics.addr", arg.MetricsAddr),
			zap.String("aeron.Uri", arg.AeronUri),
			zap.Int("aeron.streamId", arg.AeronStreamId),
		)

		m, err := app.NewMetrics(lg, app.Config{
			Addr: arg.MetricsAddr,
			Name: "api",
		})
		if err != nil {
			return errors.Wrap(err, "metrics")
		}

		oasServer, err := oas.NewServer(api.Handler{},
			oas.WithTracerProvider(m.TracerProvider()),
			oas.WithMeterProvider(m.MeterProvider()),
		)
		if err != nil {
			return errors.Wrap(err, "server init")
		}
		httpServer := http.Server{
			Addr:    arg.Addr,
			Handler: oasServer,
		}

		aeronContext := aeron.NewContext()

		a, err := aeron.Connect(aeronContext)
		if err != nil {
			return errors.Wrap(err, "aeron connect")
		}
		defer a.Close()

		publication, err := a.AddPublication(arg.AeronUri, int32(arg.AeronStreamId))
		if err != nil {
			return errors.Wrap(err, "aeron AddPublication")
		}
		defer publication.Close()

		cam, err := app.NewFliCamera(publication)
		if err != nil {
			return errors.Wrap(err, "flicamera")
		}

		if err := cam.StartCamera(); err != nil {
			return errors.Wrap(err, "flicamera")
		}

		g, ctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			return m.Run(ctx)
		})
		g.Go(func() error {
			<-ctx.Done()
			if err := httpServer.Shutdown(ctx); err != nil {
				return errors.Wrap(err, "http")
			}
			if err := cam.Shutdown(); err != nil {
				return errors.Wrap(err, "flicamera")
			}
			return nil
		})
		g.Go(func() error {
			defer lg.Info("HTTP server stopped")
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				return errors.Wrap(err, "http")
			}
			return nil
		})

		return g.Wait()
	})
}
