// Package config loads and provides WiNotification configuration.
// Author: Hadi Cahyadi <cumulus13@gmail.com>
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all application settings.
type Config struct {
	General  GeneralConfig  `mapstructure:"general"`
	Growl    GrowlConfig    `mapstructure:"growl"`
	Ntfy     NtfyConfig     `mapstructure:"ntfy"`
	RabbitMQ RabbitMQConfig `mapstructure:"rabbitmq"`
	ZeroMQ   ZeroMQConfig   `mapstructure:"zeromq"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Database DatabaseConfig `mapstructure:"database"`
	Toast    ToastConfig    `mapstructure:"toast"`
}

type GeneralConfig struct {
	LogLevel           string   `mapstructure:"log_level"`
	LogFile            string   `mapstructure:"log_file"`
	IconPath           string   `mapstructure:"icon_path"`
	CaptureIntervalMs  int      `mapstructure:"capture_interval_ms"`
	FilterApps         []string `mapstructure:"filter_apps"`
	IgnoreApps         []string `mapstructure:"ignore_apps"`
}

type GrowlConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	AppName  string `mapstructure:"app_name"`
	Icon     string `mapstructure:"icon"`
}

type NtfyConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	ServerURL string `mapstructure:"server_url"`
	Topic     string `mapstructure:"topic"`
	Token     string `mapstructure:"token"`
	IconURL   string `mapstructure:"icon_url"`
	Priority  string `mapstructure:"priority"`
}

type RabbitMQConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	URL          string `mapstructure:"url"`
	Exchange     string `mapstructure:"exchange"`
	ExchangeType string `mapstructure:"exchange_type"`
	RoutingKey   string `mapstructure:"routing_key"`
	Durable      bool   `mapstructure:"durable"`
}

type ZeroMQConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	SocketType string `mapstructure:"socket_type"`
	Bind       string `mapstructure:"bind"`
}

type RedisConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	Host          string `mapstructure:"host"`
	Port          int    `mapstructure:"port"`
	Password      string `mapstructure:"password"`
	DB            int    `mapstructure:"db"`
	KeyPrefix     string `mapstructure:"key_prefix"`
	TTL           int    `mapstructure:"ttl"`
	PubsubChannel string `mapstructure:"pubsub_channel"`
	Publish       bool   `mapstructure:"publish"`
}

type DatabaseConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	Type       string `mapstructure:"type"`
	Host       string `mapstructure:"host"`
	Port       int    `mapstructure:"port"`
	Username   string `mapstructure:"username"`
	Password   string `mapstructure:"password"`
	DBName     string `mapstructure:"dbname"`
	SQLitePath string `mapstructure:"sqlite_path"`
	Params     string `mapstructure:"params"`
}

type ToastConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	AppID    string `mapstructure:"app_id"`
	Duration string `mapstructure:"duration"`
	Audio    string `mapstructure:"audio"`
}

var current *Config

// Load reads config.toml from the given path (or searches standard locations).
func Load(cfgFile string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.winotification")
		v.AddConfigPath("/etc/winotification")
	}

	// Allow env overrides: WINOTIF_NTFY_TOKEN, etc.
	v.SetEnvPrefix("WINOTIF")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		// No config file found — use defaults
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	current = cfg
	return cfg, nil
}

// Get returns the currently loaded config (panics if Load was not called).
func Get() *Config {
	if current == nil {
		panic("config.Load() has not been called")
	}
	return current
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("general.log_level", "info")
	v.SetDefault("general.log_file", "winotification.log")
	v.SetDefault("general.icon_path", "icons/icon.ico")
	v.SetDefault("general.capture_interval_ms", 500)

	v.SetDefault("ntfy.enabled", true)
	v.SetDefault("ntfy.server_url", "https://ntfy.sh")
	v.SetDefault("ntfy.topic", "winotification")
	v.SetDefault("ntfy.priority", "default")

	v.SetDefault("growl.host", "127.0.0.1")
	v.SetDefault("growl.port", 23053)
	v.SetDefault("growl.app_name", "WiNotification")

	v.SetDefault("rabbitmq.exchange_type", "fanout")
	v.SetDefault("rabbitmq.durable", true)

	v.SetDefault("zeromq.socket_type", "pub")
	v.SetDefault("zeromq.bind", "tcp://*:5556")

	v.SetDefault("redis.host", "127.0.0.1")
	v.SetDefault("redis.port", 6379)
	v.SetDefault("redis.key_prefix", "winotif:")
	v.SetDefault("redis.ttl", 86400)
	v.SetDefault("redis.pubsub_channel", "winotification")
	v.SetDefault("redis.publish", true)

	v.SetDefault("database.type", "sqlite")
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.sqlite_path", "winotification.db")

	v.SetDefault("toast.app_id", "WiNotification")
	v.SetDefault("toast.duration", "short")
	v.SetDefault("toast.audio", "ms-winsoundevent:Notification.Default")
}
