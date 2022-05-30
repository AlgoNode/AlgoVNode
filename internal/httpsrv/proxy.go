package httpsrv

import (
	"net/http"

	"github.com/olivere/elastic/config"
)

func Listen(cfg *config.Config) {
	http.ListenAndServe()
}
