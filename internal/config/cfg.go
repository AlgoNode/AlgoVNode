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
	"errors"
	"flag"

	"github.com/algonode/algovnode/internal/utils"
)

var cfgFile = flag.String("f", "config.jsonc", "config file")

type ANode struct {
	Address  string `json:"address"`
	Token    string `json:"token"`
	Id       string `json:"id"`
	ReqLimit int32  `json:"reqlimit"`
}

type Virtual struct {
	Listen string `json:"listen"`
	Cache  int    `json:"cache"`
	Token  string `json:"token"`
}

type AConfig struct {
	Nodes   []*ANode `json:"nodes"`
	Virtual *Virtual `json:"virtual"`
}

type AlgoVNodeConfig struct {
	Algod *AConfig `json:"algod"`
}

var defaultConfig = AlgoVNodeConfig{
	Algod: &AConfig{
		Virtual: &Virtual{
			Listen: ":18090",
			Cache:  1000,
			Token:  "",
		},
	},
}

// LoadConfig loads the configuration from the specified file, merging into the default configuration.
func LoadConfig() (cfg AlgoVNodeConfig, err error) {
	flag.Parse()
	cfg = defaultConfig
	err = utils.LoadJSONCFromFile(*cfgFile, &cfg)

	if cfg.Algod == nil {
		return cfg, errors.New("Missing algod config")
	}
	if len(cfg.Algod.Nodes) == 0 {
		return cfg, errors.New("Configure at least one node")
	}
	return cfg, err
}
