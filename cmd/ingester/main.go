package main

import (
	"fmt"
	"github.com/Clymene-project/Clymene/cmd/docs"
	"github.com/Clymene-project/Clymene/cmd/flags"
	"github.com/Clymene-project/Clymene/cmd/ingester/app"
	"github.com/Clymene-project/Clymene/cmd/ingester/app/builder"
	"github.com/Clymene-project/Clymene/pkg/config"
	"github.com/Clymene-project/Clymene/pkg/version"
	"github.com/Clymene-project/Clymene/plugin/storage"
	"github.com/Clymene-project/Clymene/ports"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/uber/jaeger-lib/metrics"
	"go.uber.org/zap"
	"io"
	"log"
	"os"
)

var (
	// Version is set during binary building (git revision number)
	Version string
	// BuildTime is set during binary building
	BuildTime string
)

const (
	ClymeneIngesterName = "Clymene-ingester"
)

func main() {
	svc := flags.NewService(ports.Ingester)
	svc.NoStorage = true

	storageFactory, err := storage.NewFactory(storage.FactoryConfigFromEnvAndCLI(os.Stderr))
	if err != nil {
		log.Fatalf("Cannot initialize storage factory: %v", err)
	}

	v := viper.New()
	command := &cobra.Command{
		Use:   ClymeneIngesterName,
		Short: ClymeneIngesterName + " consumes from Kafka and send to db.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := svc.Start(v); err != nil {
				return err
			}
			logger := svc.Logger

			logger.Info("start....", zap.String("component name", ClymeneIngesterName))
			logger.Info("build info", zap.String("version", Version), zap.String("build_time", BuildTime))

			baseFactory := svc.MetricsFactory.Namespace(metrics.NSOptions{Name: "clymene"})
			metricsFactory := baseFactory.Namespace(metrics.NSOptions{Name: "ingester"})

			storageFactory.InitFromViper(v)
			if err := storageFactory.Initialize(baseFactory, logger); err != nil {
				logger.Fatal("Failed to init storage factory", zap.Error(err))
			}

			metricWriter, err := storageFactory.CreateWriter()
			if err != nil {
				logger.Fatal("Failed to create metric writer", zap.Error(err))
			}

			options := app.Options{}
			options.InitFromViper(v) // default encode is protobuf
			consumer, err := builder.CreateConsumer(
				logger.With(zap.String("component", "consumer")),
				metricsFactory,
				metricWriter,
				options,
			)
			if err != nil {
				logger.Fatal("Unable to create consumer", zap.Error(err))
			}
			consumer.Start()

			svc.RunAndThen(func() {
				if err := options.TLS.Close(); err != nil {
					logger.Error("Failed to close TLS certificates watcher", zap.Error(err))
				}
				if err = consumer.Close(); err != nil {
					logger.Error("Failed to close consumer", zap.Error(err))
				}
				if closer, ok := metricWriter.(io.Closer); ok {
					err := closer.Close()
					if err != nil {
						logger.Error("Failed to close metrics writer", zap.Error(err))
					}
				}
				if err := storageFactory.Close(); err != nil {
					logger.Error("Failed to close storage factory", zap.Error(err))
				}
			})
			return nil
		},
	}

	command.AddCommand(version.Command())
	command.AddCommand(docs.Command(v))

	config.AddFlags(
		v,
		command,
		svc.AddFlags,
		storageFactory.AddPipelineFlags,
		app.AddFlags,
	)

	if err := command.Execute(); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}