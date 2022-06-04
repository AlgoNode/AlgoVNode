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

package algod

import (
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
)

func proxyStatus(proxyStatuses []int, status int) bool {
	if proxyStatuses == nil {
		return true
	}
	for _, s := range proxyStatuses {
		if s == status {
			return true
		}
	}
	return false
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func (node *Node) ProxyHTTP(c echo.Context, proxyStatuses []int) (bool, int, error) {

	//TODO stream response body instead of buffering
	oreq := c.Request()
	res := c.Response()

	node.log.WithFields(logrus.Fields{"method": oreq.Method, "url": oreq.URL}).Info()

	//Todo: keep-alive ??
	//ratelimiter
	//timeout
	//sandboxed timeout
	//parallel calls to all catchup
	//constant scanning on all catchup
	//direct query relay nodes for block ranges
	//parallel catchup queries
	var bodyReader io.Reader
	url := node.cfg.Address + oreq.URL.Path
	req, err := http.NewRequestWithContext(oreq.Context(), oreq.Method, url, bodyReader)
	if err != nil {
		return false, http.StatusBadGateway, err
	}

	req.Header.Set(echo.HeaderXRealIP, c.RealIP())
	if req.Header.Get(echo.HeaderXForwardedProto) == "" {
		req.Header.Set(echo.HeaderXForwardedProto, c.Scheme())
	}
	if len(node.cfg.Token) > 0 {
		req.Header.Set("X-Algod-API-Token", node.cfg.Token)
	}

	resp, err := node.httpClient.Do(req)
	if err != nil {
		http.Error(res, "Server Error", http.StatusInternalServerError)
		node.log.WithFields(logrus.Fields{"ServeHTTP:": err}).Error()
		return false, http.StatusInternalServerError, err
	}
	defer resp.Body.Close()
	if proxyStatus(proxyStatuses, resp.StatusCode) {
		//copyHeader(res.Header(), resp.Header)
		res.Header().Set("X-AVN-NodeID", node.cfg.Id)
		res.WriteHeader(resp.StatusCode)
		node.log.WithFields(logrus.Fields{"status:": resp.StatusCode, "path": oreq.URL.Path}).Debug()
		io.Copy(res, resp.Body)
		return true, resp.StatusCode, nil
	}
	return false, resp.StatusCode, nil
}
