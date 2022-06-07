package main

import (
	"os"
	"strings"

	"github.com/algonode/algovnode/internal/config"
	"github.com/sirupsen/logrus"
)

func isRunningInDockerContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

func init() {
	if isRunningInDockerContainer() {
		logrus.SetFormatter(&logrus.JSONFormatter{})
		logrus.SetOutput(os.Stdout)
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{})
		logrus.SetOutput(os.Stderr)
	}
	logrus.SetLevel(logrus.DebugLevel)
}

func setLogging(cfg *config.LoggingCfg) {
	if strings.ToLower(cfg.Format) == "txt" {
		logrus.SetFormatter(&logrus.TextFormatter{})
		logrus.SetOutput(os.Stderr)
	}
	if level, err := logrus.ParseLevel(cfg.Level); err == nil {
		logrus.SetLevel(level)
	} else {
		logrus.Error("Invalid debug level")
	}
}
