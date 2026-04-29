module github.com/cumulus13/WiNotification

go 1.25.3

require (

	// GNTP (Growl) — https://github.com/cumulus13/go-gntp
	github.com/cumulus13/go-gntp v1.0.3
	// Systray
	github.com/getlantern/systray v1.2.2

	// Windows notification capture (WinRT / COM)
	github.com/go-ole/go-ole v1.3.0

	// HTTP client (ntfy)
	github.com/go-resty/resty/v2 v2.12.0

	// Toast notifications (Windows)
	github.com/go-toast/toast v0.0.0-20190211030409-01e6764cf0a4

	// UUID
	github.com/google/uuid v1.6.0

	// ZeroMQ
	github.com/pebbe/zmq4 v1.2.10

	// RabbitMQ
	github.com/rabbitmq/amqp091-go v1.9.0

	// Redis
	github.com/redis/go-redis/v9 v9.5.1

	// Logging
	github.com/sirupsen/logrus v1.9.3

	// Config
	github.com/spf13/viper v1.18.2
	golang.org/x/sys v0.18.0
	gorm.io/driver/mysql v1.5.6
	gorm.io/driver/postgres v1.5.7
	gorm.io/driver/sqlite v1.5.5
	gorm.io/driver/sqlserver v1.5.3

	// Database (multi-driver)
	gorm.io/gorm v1.25.8
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/getlantern/context v0.0.0-20190109183933-c447772a6520 // indirect
	github.com/getlantern/errors v0.0.0-20190325191628-abdb3e3e36f7 // indirect
	github.com/getlantern/golog v0.0.0-20190830074920-4ef2e798c2d7 // indirect
	github.com/getlantern/hex v0.0.0-20190417191902-c6586a6fe0b7 // indirect
	github.com/getlantern/hidden v0.0.0-20190325191715-f02dbb02be55 // indirect
	github.com/getlantern/ops v0.0.0-20190325191751-d70cb0d6f85f // indirect
	github.com/go-sql-driver/mysql v1.7.0 // indirect
	github.com/go-stack/stack v1.8.0 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/pgx/v5 v5.4.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mattn/go-sqlite3 v1.14.17 // indirect
	github.com/microsoft/go-mssqldb v1.6.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/nu7hatch/gouuid v0.0.0-20131221200532-179d4d0c4d8d // indirect
	github.com/oxtoacart/bpool v0.0.0-20190530202638-03653db5a59c // indirect
	github.com/pelletier/go-toml/v2 v2.1.0 // indirect
	github.com/sagikazarmark/locafero v0.4.0 // indirect
	github.com/sagikazarmark/slog-shim v0.1.0 // indirect
	github.com/sourcegraph/conc v0.3.0 // indirect
	github.com/spf13/afero v1.11.0 // indirect
	github.com/spf13/cast v1.6.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	go.uber.org/multierr v1.9.0 // indirect
	golang.org/x/crypto v0.21.0 // indirect
	golang.org/x/exp v0.0.0-20230905200255-921286631fa9 // indirect
	golang.org/x/net v0.22.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
