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

type NodeCfg struct {
	Address  string `json:"address"`
	Token    string `json:"token"`
	Id       string `json:"id"`
	ReqLimit int32  `json:"reqlimit"`
}

type HttpCfg struct {
	Listen    string `json:"listen"`
	CertDir   string `json:"certDir"`
	Autocerts bool   `json:"autocerts"`
	Enabled   bool   `json:"enabled"`
	H2C       bool   `json:"h2c"`
}
type HttpsCfg struct {
	Listen  string `json:"listen"`
	Enabled bool   `json:"enabled"`
}

type VirtCfg struct {
	Nodes []*NodeCfg `json:"nodes"`
	Http  *HttpCfg   `json:"http"`
	Https *HttpsCfg  `json:"https"`
	Cache int        `json:"cache"`
	Token string     `json:"token"`
}

type AlgoVNodeConfig struct {
	Virtual *VirtCfg `json:"virtual"`
}

var defaultConfig = AlgoVNodeConfig{
	Virtual: &VirtCfg{
		Http: &HttpCfg{
			Listen:  ":18090",
			Enabled: true,
			H2C:     true,
		},
		Cache: 1000,
		Token: "",
	},
}

// LoadConfig loads the configuration from the specified file, merging into the default configuration.
func LoadConfig() (cfg AlgoVNodeConfig, err error) {
	flag.Parse()
	cfg = defaultConfig
	err = utils.LoadJSONCFromFile(*cfgFile, &cfg)

	if cfg.Virtual == nil {
		return cfg, errors.New("missing algod config")
	}
	if len(cfg.Virtual.Nodes) == 0 {
		return cfg, errors.New("configure at least one node")
	}
	return cfg, err
}
