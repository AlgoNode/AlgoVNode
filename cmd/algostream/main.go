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
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/algonode/algovnode/internal/algod"
	"github.com/algonode/algovnode/internal/config"
)

func main() {

	//load config
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!ERR][_MAIN] loading config: %s\n", err)
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
			<-cancelCh
			fmt.Fprintf(os.Stderr, "[!ERR][_MAIN] stopping streamer.\n")
			cf()
		}()
	}

	//spawn a block stream fetcher that never fails
	//	blocks, status, err := algod.AlgoVNode(ctx, cfg.Algod)
	_, _, err = algod.AlgoVNode(ctx, cfg.Algod)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!ERR][_MAIN] error getting algod stream: %s\n", err)
		return
	}

	//Wait for the end of the Algoverse
	<-ctx.Done()

}
