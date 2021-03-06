//  Copyright 2018 Google Inc. All Rights Reserved.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

// osconfig_agent interacts with the osconfig api.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/google-osconfig-agent/config"
	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/google-osconfig-agent/inventory"
	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/google-osconfig-agent/logger"
	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/google-osconfig-agent/ospackage"
	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/google-osconfig-agent/ospatch"
	"github.com/GoogleCloudPlatform/compute-image-tools/cli_tools/google-osconfig-agent/tasker"
	"github.com/GoogleCloudPlatform/compute-image-tools/go/packages"
	"github.com/GoogleCloudPlatform/compute-image-tools/go/service"
)

var version string

func init() {
	// We do this here so the -X value doesn't need the full path.
	config.SetVersion(version)

	obtainLock()
}

type logWritter struct{}

func (l *logWritter) Write(b []byte) (int, error) {
	logger.Debug(logger.LogEntry{CallDepth: 3, Message: string(b)})
	return len(b), nil
}

func run(ctx context.Context) {
	ticker := time.NewTicker(config.SvcPollInterval())
	for {
		if err := config.SetConfig(); err != nil {
			logger.Errorf(err.Error())
		}

		// This sets up the patching system to run in the background.
		ospatch.Configure(ctx)

		if config.OSPackageEnabled() {
			ospackage.Run(ctx, config.Instance())
		}

		if config.OSInventoryEnabled() {
			// This should always run after ospackage.SetConfig.
			inventory.Run()
		}

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			logger.Close()
			return
		}
	}
}

var deferredFuncs []func()

func main() {
	flag.Parse()
	ctx := context.Background()

	logger.DeferredFatalFuncs = append(logger.DeferredFatalFuncs, deferredFuncs...)
	defer func() {
		for _, f := range deferredFuncs {
			f()
		}
	}()

	// If this call to SetConfig fails (like a metadata error) we can't continue.
	if err := config.SetConfig(); err != nil {
		logger.Fatalf(err.Error())
	}

	packages.DebugLogger = log.New(&logWritter{}, "", 0)

	logger.Init(ctx, config.ProjectID())
	defer logger.Close()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		select {
		case <-c:
			logger.Fatalf("Ctrl-C caught, shutting down.")
		}
	}()

	switch action := flag.Arg(0); action {
	case "", "run":
		if err := service.Register(ctx, "google_osconfig_agent", "Google OSConfig Agent", "", run, "run"); err != nil {
			logger.Fatalf("service.Register error: %v", err)
		}
	case "noservice":
		run(ctx)
		return
	case "inventory", "osinventory":
		inventory.Run()
		tasker.Close()
		return
	case "ospackage":
		ospackage.Run(ctx, config.Instance())
		tasker.Close()
		return
	case "ospatch":
		ospatch.Run(ctx, make(chan struct{}))
		return
	default:
		logger.Fatalf("Unknown arg %q", action)
	}
}
