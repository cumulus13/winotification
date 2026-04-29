package forwarder

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
)

// NtfyForwarder publishes notifications to an ntfy server (ntfy.sh or self-hosted).
type NtfyForwarder struct {
	cfg    config.NtfyConfig
	log    *logrus.Logger
	client *resty.Client
}

// NewNtfyForwarder returns a configured ntfy forwarder.
func NewNtfyForwarder(cfg config.NtfyConfig, log *logrus.Logger) *NtfyForwarder {
	c := resty.New()
	if cfg.Token != "" {
		c.SetAuthToken(cfg.Token)
	}
	log.Infof("[ntfy] target: %s/%s", cfg.ServerURL, cfg.Topic)
	return &NtfyForwarder{cfg: cfg, log: log, client: c}
}

func (nf *NtfyForwarder) Name() string { return "ntfy" }

func (nf *NtfyForwarder) Forward(ctx context.Context, n *capture.Notification) error {
	title := n.Title
	if title == "" {
		title = n.AppName
	}
	body := n.Body
	if body == "" {
		body = title
		title = n.AppName
	}

	url := strings.TrimRight(nf.cfg.ServerURL, "/") + "/" + nf.cfg.Topic

	req := nf.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "text/plain; charset=utf-8").
		SetHeader("Title", sanitizeHeader(title)).
		SetHeader("Priority", nf.cfg.Priority).
		SetHeader("Tags", "bell,"+sanitizeTag(n.AppName)).
		SetBody(body)

	if nf.cfg.IconURL != "" {
		req.SetHeader("Icon", nf.cfg.IconURL)
	}

	resp, err := req.Post(url)
	if err != nil {
		return fmt.Errorf("ntfy post: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("ntfy non-200: %d %s", resp.StatusCode(), resp.String())
	}
	return nil
}

func (nf *NtfyForwarder) Close() error { return nil }

func sanitizeHeader(s string) string {
	// RFC 7230: header values must not contain newlines
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

func sanitizeTag(s string) string {
	// ntfy tags: lowercase, alphanumeric + underscore
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
