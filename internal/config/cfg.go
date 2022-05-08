// Copyright (C) 2022 AlgoNode Org.
//
// algonode is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// algonode is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with algonode.  If not, see <https://www.gnu.org/licenses/>.

package config

import (
	"flag"
	"fmt"

	"github.com/algonode/algovnode/internal/algod"
	"github.com/algonode/algovnode/internal/utils"
)

var cfgFile = flag.String("f", "config.jsonc", "config file")

type SteramerConfig struct {
	Algod *algod.AConfig `json:"algod"`
}

var defaultConfig = SteramerConfig{}

// loadConfig loads the configuration from the specified file, merging into the default configuration.
func LoadConfig() (cfg SteramerConfig, err error) {
	flag.Parse()
	cfg = defaultConfig
	err = utils.LoadJSONCFromFile(*cfgFile, &cfg)

	if cfg.Algod == nil {
		return cfg, fmt.Errorf("[CFG] Missing algod config")
	}
	if len(cfg.Algod.Nodes) == 0 {
		return cfg, fmt.Errorf("[CFG] Configure at least one node")
	}
	return cfg, err
}
