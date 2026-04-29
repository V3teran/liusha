package logx

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
)

func New(component string) zerolog.Logger {
	env := strings.ToLower(os.Getenv("LIUSHA_ENV"))
	return newWith(os.Stdout, env).With().Str("component", component).Logger()
}

func newWith(w io.Writer, env string) zerolog.Logger {
	level := parseLevel(os.Getenv("LIUSHA_LOG_LEVEL"))
	zerolog.SetGlobalLevel(level)
	zerolog.MessageFieldName = "message"
	if env == "development" {
		return zerolog.New(zerolog.ConsoleWriter{Out: w, TimeFormat: "15:04:05"}).
			With().Timestamp().Logger()
	}
	return zerolog.New(w).With().Timestamp().Logger()
}

func parseLevel(s string) zerolog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return zerolog.DebugLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}
