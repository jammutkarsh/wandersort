package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

type Configuration struct {
	ServerPort           string
	Postgres             Postgres
	OutputPath           string
	OTelEnabled          bool
	LogLevel             string
	LogConsole           bool
	LogFile              string
	MaxConcurrentScans   int
	MaxConcurrentHashers int
}

type Postgres struct {
	User     string
	Password string
	Port     string
	Host     string
	DB       string
}

func Load() (*Configuration, error) {
	outputPath := os.Getenv("OUTPUT_PATH")
	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputPath = filepath.Join(home, "WanderSort_Library")
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	pg := Postgres{
		User:     os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
		Port:     os.Getenv("POSTGRES_PORT"),
		Host:     os.Getenv("POSTGRES_HOST"),
		DB:       os.Getenv("POSTGRES_DB"),
	}
	if pg.User == "" || pg.Password == "" || pg.Host == "" || pg.Port == "" || pg.DB == "" {
		return nil, errors.New("config: missing required Postgres credentials (POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_HOST, POSTGRES_PORT, POSTGRES_DB)")
	}

	var maxConcurrentScans int
	if v := os.Getenv("MAX_CONCURRENT_SCANS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrentScans = n
		}
	}

	var maxConcurrentHashers int
	if v := os.Getenv("MAX_CONCURRENT_HASHERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrentHashers = n
		}
	}

	return &Configuration{
		ServerPort:           os.Getenv("PORT"),
		OutputPath:           outputPath,
		OTelEnabled:          os.Getenv("OTEL_ENABLED") == "true",
		LogLevel:             logLevel,
		LogConsole:           true, // console logging is always enabled at minimum
		LogFile:              os.Getenv("LOG_FILE"),
		Postgres:             pg,
		MaxConcurrentScans:   maxConcurrentScans,
		MaxConcurrentHashers: maxConcurrentHashers,
	}, nil
}
