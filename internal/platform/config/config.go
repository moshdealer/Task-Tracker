package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config содержит все настройки приложения
type Config struct {
	App      App      `mapstructure:"app"`
	HTTP     HTTP     `mapstructure:"http"`
	Database Database `mapstructure:"database"`
	Redis    Redis    `mapstructure:"redis"`
	JWT      JWT      `mapstructure:"jwt"`
	Log      Log      `mapstructure:"log"`
}

type App struct {
	Environment     string        `mapstructure:"environment"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type HTTP struct {
	Host              string        `mapstructure:"host"`
	Port              int           `mapstructure:"port"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
}

// Address возвращает адрес HTTP-сервера в формате host:port
func (h HTTP) Address() string {
	return net.JoinHostPort(h.Host, strconv.Itoa(h.Port))
}

type Database struct {
	URL             string        `mapstructure:"url"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

type Redis struct {
	URL string `mapstructure:"url"`
}

type Log struct {
	Level string `mapstructure:"level"`
}

type JWT struct {
	Secret string        `mapstructure:"secret"`
	TTL    time.Duration `mapstructure:"ttl"`
}

// Load читает конфигурацию из необязательного файла .env и переменных окружения
// Переменные окружения имеют приоритет над значениями из файла
func Load() (Config, error) {
	v := viper.New()
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	setDefaults(v)

	for _, environmentVariable := range environmentVariables {
		if err := v.BindEnv(environmentVariable, environmentVariable); err != nil {
			return Config{}, fmt.Errorf("config: bind %s: %w", environmentVariable, err)
		}
	}
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("config: read .env: %w", err)
	}

	cfg := Config{
		App: App{
			Environment:     strings.ToLower(strings.TrimSpace(v.GetString("APP_ENV"))),
			ShutdownTimeout: v.GetDuration("SHUTDOWN_TIMEOUT"),
		},
		HTTP: HTTP{
			Host:              strings.TrimSpace(v.GetString("HTTP_HOST")),
			Port:              v.GetInt("HTTP_PORT"),
			ReadHeaderTimeout: v.GetDuration("HTTP_READ_HEADER_TIMEOUT"),
			WriteTimeout:      v.GetDuration("HTTP_WRITE_TIMEOUT"),
			IdleTimeout:       v.GetDuration("HTTP_IDLE_TIMEOUT"),
		},
		Database: Database{
			URL:             strings.TrimSpace(v.GetString("DATABASE_URL")),
			MaxOpenConns:    v.GetInt("DB_MAX_OPEN_CONNS"),
			MaxIdleConns:    v.GetInt("DB_MAX_IDLE_CONNS"),
			ConnMaxLifetime: v.GetDuration("DB_CONN_MAX_LIFETIME"),
		},
		Redis: Redis{
			URL: strings.TrimSpace(v.GetString("REDIS_URL")),
		},
		JWT: JWT{
			Secret: strings.TrimSpace(v.GetString("JWT_SECRET")),
			TTL:    v.GetDuration("JWT_TTL"),
		},
		Log: Log{
			Level: strings.ToLower(strings.TrimSpace(v.GetString("LOG_LEVEL"))),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

var environmentVariables = []string{
	"APP_ENV",
	"SHUTDOWN_TIMEOUT",
	"HTTP_HOST",
	"HTTP_PORT",
	"HTTP_READ_HEADER_TIMEOUT",
	"HTTP_WRITE_TIMEOUT",
	"HTTP_IDLE_TIMEOUT",
	"DATABASE_URL",
	"DB_MAX_OPEN_CONNS",
	"DB_MAX_IDLE_CONNS",
	"DB_CONN_MAX_LIFETIME",
	"REDIS_URL",
	"JWT_SECRET",
	"JWT_TTL",
	"LOG_LEVEL",
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("APP_ENV", "local")
	v.SetDefault("SHUTDOWN_TIMEOUT", 10*time.Second)
	v.SetDefault("HTTP_HOST", "0.0.0.0")
	v.SetDefault("HTTP_PORT", 8080)
	v.SetDefault("HTTP_READ_HEADER_TIMEOUT", 5*time.Second)
	v.SetDefault("HTTP_WRITE_TIMEOUT", 10*time.Second)
	v.SetDefault("HTTP_IDLE_TIMEOUT", 60*time.Second)
	v.SetDefault("DB_MAX_OPEN_CONNS", 25)
	v.SetDefault("DB_MAX_IDLE_CONNS", 5)
	v.SetDefault("DB_CONN_MAX_LIFETIME", 30*time.Minute)
	v.SetDefault("JWT_TTL", 24*time.Hour)
	v.SetDefault("LOG_LEVEL", "info")
}

func (c Config) validate() error {
	if c.Database.URL == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if c.JWT.Secret == "" {
		return fmt.Errorf("config: JWT_SECRET is required")
	}
	if c.Redis.URL == "" {
		return fmt.Errorf("config: REDIS_URL is required")
	}
	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		return fmt.Errorf("config: HTTP_PORT must be between 1 and 65535")
	}
	if c.Database.MaxOpenConns < 1 {
		return fmt.Errorf("config: DB_MAX_OPEN_CONNS must be greater than 0")
	}
	if c.Database.MaxIdleConns < 0 || c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		return fmt.Errorf("config: DB_MAX_IDLE_CONNS must be between 0 and DB_MAX_OPEN_CONNS")
	}
	if c.App.ShutdownTimeout <= 0 || c.HTTP.ReadHeaderTimeout <= 0 ||
		c.HTTP.WriteTimeout <= 0 || c.HTTP.IdleTimeout <= 0 ||
		c.Database.ConnMaxLifetime <= 0 || c.JWT.TTL <= 0 {
		return fmt.Errorf("config: timeout and lifetime values must be greater than 0")
	}
	if !oneOf(c.App.Environment, "local", "dev", "stage", "prod", "test") {
		return fmt.Errorf("config: APP_ENV must be one of local, dev, stage, prod, test")
	}
	if !oneOf(c.Log.Level, "debug", "info", "warn", "error") {
		return fmt.Errorf("config: LOG_LEVEL must be one of debug, info, warn, error")
	}

	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
