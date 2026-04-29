package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/sirupsen/logrus"
)

// RabbitMQForwarder publishes notifications to a RabbitMQ exchange.
type RabbitMQForwarder struct {
	cfg  config.RabbitMQConfig
	log  *logrus.Logger
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewRabbitMQForwarder connects to RabbitMQ, declares the exchange, and
// returns a ready forwarder.
func NewRabbitMQForwarder(cfg config.RabbitMQConfig, log *logrus.Logger) (*RabbitMQForwarder, error) {
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq channel: %w", err)
	}

	if err := ch.ExchangeDeclare(
		cfg.Exchange,
		cfg.ExchangeType,
		cfg.Durable,
		false, false, false, nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("rabbitmq declare exchange: %w", err)
	}

	log.Infof("[rabbitmq] connected to %s, exchange=%s", cfg.URL, cfg.Exchange)
	return &RabbitMQForwarder{cfg: cfg, log: log, conn: conn, ch: ch}, nil
}

func (r *RabbitMQForwarder) Name() string { return "rabbitmq" }

func (r *RabbitMQForwarder) Forward(ctx context.Context, n *capture.Notification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}

	return r.ch.PublishWithContext(ctx,
		r.cfg.Exchange,
		r.cfg.RoutingKey,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
}

func (r *RabbitMQForwarder) Close() error {
	if r.ch != nil {
		r.ch.Close()
	}
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}
