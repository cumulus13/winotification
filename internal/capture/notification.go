// Package capture defines the core Notification model and the Windows
// Action Center capture loop using WinRT via go-ole.
//
// Build constraint: windows only.
// Author: Hadi Cahyadi <cumulus13@gmail.com>
package capture

import "time"

// Notification is the canonical representation of a Windows notification
// captured from the Action Center / notification platform.
type Notification struct {
	// Unique ID assigned by WiNotification
	ID string `json:"id" gorm:"primaryKey"`

	// Source application name (e.g. "Microsoft.Teams")
	AppName string `json:"app_name" gorm:"index"`

	// Notification title / summary
	Title string `json:"title"`

	// Full notification body text
	Body string `json:"body"`

	// Tag & group used by the source app
	Tag   string `json:"tag"`
	Group string `json:"group"`

	// Sequence number from the platform
	Sequence uint32 `json:"sequence"`

	// When the notification arrived (UTC)
	ArrivedAt time.Time `json:"arrived_at" gorm:"index"`

	// Raw XML payload from the notification platform
	RawXML string `json:"raw_xml,omitempty" gorm:"-"`

	// Icon data (PNG bytes) extracted from the notification, if any
	IconData []byte `json:"-" gorm:"-"`
}
