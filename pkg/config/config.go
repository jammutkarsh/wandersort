package config

import "os"

type Configuration struct {
	ServerPort string   `koanf:"port"`
	Postgres   Postgres `koanf:"postgres"`
	OutputPath string   `koanf:"output_path"`
}

type Postgres struct {
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Port     string `koanf:"port"`
	Host     string `koanf:"host"`
	DB       string `koanf:"db"`
}

func Load() *Configuration {
	outputPath := os.Getenv("OUTPUT_PATH")
	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputPath = home + "/WanderSort_Library"
	}
	return &Configuration{
		ServerPort: os.Getenv("PORT"),
		OutputPath: outputPath,
		Postgres: Postgres{
			User:     os.Getenv("POSTGRES_USER"),
			Password: os.Getenv("POSTGRES_PASSWORD"),
			Port:     os.Getenv("POSTGRES_PORT"),
			Host:     os.Getenv("POSTGRES_HOST"),
			DB:       os.Getenv("POSTGRES_DB"),
		},
	}
}
