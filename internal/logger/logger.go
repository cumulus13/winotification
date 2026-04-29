// Package logger sets up the application-wide logrus logger.
// Author: Hadi Cahyadi <cumulus13@gmail.com>
package logger

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

// New creates a logrus.Logger with the given level and optional file output.
func New(level, logFile string) *logrus.Logger {
	log := logrus.New()

	lvl, err := logrus.ParseLevel(level)
	if err != nil {
		lvl = logrus.InfoLevel
	}
	log.SetLevel(lvl)
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err == nil {
			log.SetOutput(io.MultiWriter(os.Stdout, f))
		} else {
			log.Warnf("Cannot open log file %s: %v", logFile, err)
		}
	}

	return log
}
