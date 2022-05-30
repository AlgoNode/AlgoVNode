// Copyright (C) 2022 AlgoNode.
//
// algovnode is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// algovnode is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with algovnode.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/algonode/algovnode/internal/algod"
	"github.com/algonode/algovnode/internal/blockcache"
	"github.com/algonode/algovnode/internal/config"
	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)
}

func main() {

	//load config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.WithError(err).Error("Loading config")
		return
	}

	//make us a nice cancellable context
	//set Ctrl-C as the cancell trigger
	ctx, cf := context.WithCancel(context.Background())
	defer cf()
	{
		cancelCh := make(chan os.Signal, 1)
		signal.Notify(cancelCh, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			s := <-cancelCh
			log.Errorf("Stopping algovnode due to %s", s.String())
			cf()
		}()
	}

	cache := blockcache.New(ctx)
	cluster := algod.NewCluster(ctx, cache, cfg)
	cache.SetBlockFetcher(cluster)

	cluster.WaitForFatal(ctx)
}
