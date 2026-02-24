package config

import (
	"errors"
	"os"
	"strconv"
)

type Configuration struct {
	ServerPort         string   `koanf:"port"`
	Postgres           Postgres `koanf:"postgres"`
	OutputPath         string   `koanf:"output_path"`
	OTelEnabled        bool     `koanf:"otel_enabled"`
	LogLevel           string   `koanf:"log_level"`
	LogConsole         bool     `koanf:"log_console"`
	LogFile            string   `koanf:"log_file"`
	MaxConcurrentScans int      `koanf:"max_concurrent_scans"`
}

type Postgres struct {
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Port     string `koanf:"port"`
	Host     string `koanf:"host"`
	DB       string `koanf:"db"`
}

func Load() (*Configuration, error) {
	outputPath := os.Getenv("OUTPUT_PATH")
	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputPath = home + "/WanderSort_Library"
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

	maxConcurrentScans := 5
	if v := os.Getenv("MAX_CONCURRENT_SCANS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrentScans = n
		}
	}

	return &Configuration{
		ServerPort:         os.Getenv("PORT"),
		OutputPath:         outputPath,
		OTelEnabled:        os.Getenv("OTEL_ENABLED") == "true",
		LogLevel:           logLevel,
		LogConsole:         true, // console logging is always enabled at minimum
		LogFile:            os.Getenv("LOG_FILE"),
		Postgres:           pg,
		MaxConcurrentScans: maxConcurrentScans,
	}, nil
}
