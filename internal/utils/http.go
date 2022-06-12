package utils

import (
	"errors"

	"github.com/labstack/echo/v4"
)

//TODO: tune this per endpoint
var Proxy404 = []int{200, 204, 400, 404}
var Proxy200 = []int{200, 204, 400}
var ProxyALL []int = nil

func JsonError(c echo.Context, status int, errStr string) error {
	c.JSON(status, map[string]string{"message": errStr})
	return errors.New(errStr)
}
