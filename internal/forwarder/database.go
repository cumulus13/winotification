package forwarder

import (
	"context"
	"fmt"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// DBForwarder persists notifications to a relational database via GORM.
type DBForwarder struct {
	cfg config.DatabaseConfig
	log *logrus.Logger
	db  *gorm.DB
}

// dbNotification is the GORM model stored in the database.
type dbNotification struct {
	capture.Notification
}

// NewDBForwarder opens a database connection, runs auto-migration, and
// returns a ready forwarder.
func NewDBForwarder(cfg config.DatabaseConfig, log *logrus.Logger) (*DBForwarder, error) {
	gormCfg := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	}

	var dialector gorm.Dialector

	switch cfg.Type {
	case "sqlite":
		dialector = sqlite.Open(cfg.SQLitePath)

	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=UTC",
			cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)
		if cfg.Params != "" {
			dsn += "&" + cfg.Params
		}
		dialector = mysql.Open(dsn)

	case "postgres":
		dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=UTC",
			cfg.Host, cfg.Username, cfg.Password, cfg.DBName, cfg.Port)
		if cfg.Params != "" {
			dsn += " " + cfg.Params
		}
		dialector = postgres.Open(dsn)

	case "sqlserver":
		dsn := fmt.Sprintf("sqlserver://%s:%s@%s:%d?database=%s",
			cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)
		if cfg.Params != "" {
			dsn += "&" + cfg.Params
		}
		dialector = sqlserver.Open(dsn)

	default:
		return nil, fmt.Errorf("database: unsupported type %q (sqlite|mysql|postgres|sqlserver)", cfg.Type)
	}

	db, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("database open (%s): %w", cfg.Type, err)
	}

	// Auto-migrate creates the table if it doesn't exist.
	if err := db.AutoMigrate(&capture.Notification{}); err != nil {
		return nil, fmt.Errorf("database migrate: %w", err)
	}

	log.Infof("[database] connected (%s)", cfg.Type)
	return &DBForwarder{cfg: cfg, log: log, db: db}, nil
}

func (d *DBForwarder) Name() string { return "database" }

func (d *DBForwarder) Forward(ctx context.Context, n *capture.Notification) error {
	result := d.db.WithContext(ctx).Create(n)
	return result.Error
}

func (d *DBForwarder) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
