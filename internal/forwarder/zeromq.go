package forwarder

import (
	"context"
	"encoding/json"
	"fmt"

	zmq "github.com/pebbe/zmq4"
	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/sirupsen/logrus"
)

// ZeroMQForwarder publishes notifications on a ZeroMQ PUB or PUSH socket.
type ZeroMQForwarder struct {
	cfg    config.ZeroMQConfig
	log    *logrus.Logger
	socket *zmq.Socket
}

// NewZeroMQForwarder binds a ZeroMQ socket and returns a ready forwarder.
func NewZeroMQForwarder(cfg config.ZeroMQConfig, log *logrus.Logger) (*ZeroMQForwarder, error) {
	var sockType zmq.Type
	switch cfg.SocketType {
	case "push":
		sockType = zmq.PUSH
	default:
		sockType = zmq.PUB
	}

	sock, err := zmq.NewSocket(sockType)
	if err != nil {
		return nil, fmt.Errorf("zmq new socket: %w", err)
	}

	if err := sock.Bind(cfg.Bind); err != nil {
		sock.Close()
		return nil, fmt.Errorf("zmq bind %s: %w", cfg.Bind, err)
	}

	log.Infof("[zeromq] bound %s socket on %s", cfg.SocketType, cfg.Bind)
	return &ZeroMQForwarder{cfg: cfg, log: log, socket: sock}, nil
}

func (z *ZeroMQForwarder) Name() string { return "zeromq" }

func (z *ZeroMQForwarder) Forward(_ context.Context, n *capture.Notification) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	// For PUB sockets, prefix with topic "winotification "
	msg := "winotification " + string(data)
	_, err = z.socket.Send(msg, 0)
	return err
}

func (z *ZeroMQForwarder) Close() error {
	return z.socket.Close()
}
