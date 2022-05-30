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

	"github.com/sirupsen/logrus"
)

func proxyStatus(proxyStatuses *[]int, status int) bool {
	if proxyStatuses == nil {
		return true
	}
	for _, s := range *proxyStatuses {
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

func (node *Node) ProxyHTTP(wr http.ResponseWriter, req *http.Request, proxyStatuses *[]int) (bool, int, error) {
	node.log.WithFields(logrus.Fields{"method": req.Method, "url": req.URL}).Info()
	req.RequestURI = ""
	//Todo: keep-alive ??
	//ratelimiter
	//parallel calls to all catchup
	//constant scanning on all catchup
	//direct query relay nodes for block ranges
	//parallel catchup queries
	resp, err := node.httpClient.Do(req)
	if err != nil {
		http.Error(wr, "Server Error", http.StatusInternalServerError)
		node.log.WithFields(logrus.Fields{"ServeHTTP:": err}).Error()
		return false, http.StatusInternalServerError, err
	}
	defer resp.Body.Close()
	if proxyStatus(proxyStatuses, resp.StatusCode) {
		copyHeader(wr.Header(), resp.Header)
		wr.WriteHeader(resp.StatusCode)
		io.Copy(wr, resp.Body)
		return true, resp.StatusCode, nil
	}
	return false, resp.StatusCode, nil
}
