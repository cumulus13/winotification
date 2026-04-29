//go:build windows

package capture

import (
	"context"
	"encoding/xml"
	"strings"
	"sync"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Engine captures Windows toast/Action-Center notifications via the
// Windows.UI.Notifications.Management.UserNotificationListener WinRT API.
//
// It polls the notification list at a configurable interval because the
// WinRT event subscription model requires a full UWP/MSIX context that a
// plain Win32 process cannot host cleanly. Polling works reliably from
// Windows 10 1709 onward.
type Engine struct {
	intervalMs int
	filterApps map[string]struct{}
	ignoreApps map[string]struct{}
	logger     *logrus.Logger
	out        chan<- *Notification
	seen       map[uint32]struct{} // sequence numbers already dispatched
	mu         sync.Mutex
}

// NewEngine creates a capture engine that writes new notifications to out.
func NewEngine(
	intervalMs int,
	filterApps []string,
	ignoreApps []string,
	out chan<- *Notification,
	log *logrus.Logger,
) *Engine {
	fa := make(map[string]struct{}, len(filterApps))
	for _, a := range filterApps {
		fa[strings.ToLower(a)] = struct{}{}
	}
	ia := make(map[string]struct{}, len(ignoreApps))
	for _, a := range ignoreApps {
		ia[strings.ToLower(a)] = struct{}{}
	}
	return &Engine{
		intervalMs: intervalMs,
		filterApps: fa,
		ignoreApps: ia,
		logger:     log,
		out:        out,
		seen:       make(map[uint32]struct{}),
	}
}

// Run starts the capture loop and blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// Already initialised in this thread is OK (S_FALSE = 1)
		if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 0x00000001 {
			return err
		}
	}
	defer ole.CoUninitialize()

	ticker := time.NewTicker(time.Duration(e.intervalMs) * time.Millisecond)
	defer ticker.Stop()

	e.logger.Info("Capture engine started (polling interval: ", e.intervalMs, "ms)")

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Capture engine stopped")
			return nil
		case <-ticker.C:
			if err := e.poll(); err != nil {
				e.logger.WithError(err).Debug("Poll error (will retry)")
			}
		}
	}
}

// poll reads all current UserNotifications from the WinRT listener.
func (e *Engine) poll() error {
	// Activate UserNotificationListener
	listener, err := e.activateListener()
	if err != nil {
		return err
	}
	defer listener.Release()

	// Check access — the user must have granted notification access
	// to this application (done once via requestAccess on first run).
	accessStatus, err := e.getAccessStatus(listener)
	if err != nil {
		return err
	}
	if accessStatus != 2 { // UserNotificationListenerAccessStatus.Allowed == 2
		e.logger.Warn("Notification listener access not granted (status=", accessStatus, "). Run once as admin or grant in Windows Settings.")
		return nil
	}

	// GetNotifications(NotificationKindToast | NotificationKindTile = 0x3)
	notifications, err := oleutil.CallMethod(listener, "GetNotifications", int32(3))
	if err != nil {
		return err
	}
	defer notifications.Clear()

	notifList := notifications.ToIDispatch()
	if notifList == nil {
		return nil
	}
	defer notifList.Release()

	countV, err := oleutil.GetProperty(notifList, "Size")
	if err != nil {
		return err
	}
	count := int(countV.Val)

	for i := 0; i < count; i++ {
		itemV, err := oleutil.CallMethod(notifList, "GetAt", uint32(i))
		if err != nil {
			continue
		}
		item := itemV.ToIDispatch()
		if item == nil {
			continue
		}

		n, err := e.parseUserNotification(item)
		item.Release()
		if err != nil || n == nil {
			continue
		}

		e.mu.Lock()
		_, seen := e.seen[n.Sequence]
		if !seen {
			e.seen[n.Sequence] = struct{}{}
		}
		e.mu.Unlock()

		if seen {
			continue
		}

		if !e.shouldForward(n) {
			continue
		}

		select {
		case e.out <- n:
		default:
			e.logger.Warn("Notification channel full, dropping: ", n.Title)
		}
	}

	return nil
}

func (e *Engine) activateListener() (*ole.IDispatch, error) {
	unknown, err := oleutil.CreateObject("Windows.UI.Notifications.Management.UserNotificationListener")
	if err != nil {
		// Fallback: GetActivationFactory
		return nil, err
	}
	dispatch, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		return nil, err
	}
	unknown.Release()
	return dispatch, nil
}

func (e *Engine) getAccessStatus(listener *ole.IDispatch) (int, error) {
	v, err := oleutil.GetProperty(listener, "AccessStatus")
	if err != nil {
		return 0, err
	}
	return int(v.Val), nil
}

func (e *Engine) parseUserNotification(item *ole.IDispatch) (*Notification, error) {
	seqV, err := oleutil.GetProperty(item, "Id")
	if err != nil {
		return nil, err
	}
	seq := uint32(seqV.Val)

	// AppInfo
	appInfoV, err := oleutil.GetProperty(item, "AppInfo")
	if err != nil {
		return nil, err
	}
	appInfo := appInfoV.ToIDispatch()
	if appInfo == nil {
		return nil, nil
	}
	defer appInfo.Release()

	appIdV, _ := oleutil.GetProperty(appInfo, "AppUserModelId")
	appName := appIdV.ToString()

	// Notification
	notifV, err := oleutil.GetProperty(item, "Notification")
	if err != nil {
		return nil, err
	}
	notif := notifV.ToIDispatch()
	if notif == nil {
		return nil, nil
	}
	defer notif.Release()

	tagV, _ := oleutil.GetProperty(notif, "Tag")
	groupV, _ := oleutil.GetProperty(notif, "Group")

	// Visual → Content (XML)
	visualV, err := oleutil.GetProperty(notif, "Visual")
	if err != nil {
		return nil, err
	}
	visual := visualV.ToIDispatch()
	if visual == nil {
		return nil, nil
	}
	defer visual.Release()

	bindingV, err := oleutil.CallMethod(visual, "GetBinding", "ToastGeneric")
	if err != nil {
		// Try fallback binding name
		bindingV, err = oleutil.GetProperty(visual, "Content")
		if err != nil {
			return nil, nil
		}
	}
	binding := bindingV.ToIDispatch()
	if binding == nil {
		return nil, nil
	}
	defer binding.Release()

	title, body := extractTitleBody(binding)

	n := &Notification{
		ID:        uuid.New().String(),
		AppName:   appName,
		Title:     title,
		Body:      body,
		Tag:       tagV.ToString(),
		Group:     groupV.ToString(),
		Sequence:  seq,
		ArrivedAt: time.Now().UTC(),
	}
	return n, nil
}

// extractTitleBody tries to pull title and body lines from a ToastBinding IDispatch.
func extractTitleBody(binding *ole.IDispatch) (title, body string) {
	// Try to get the XML payload of the content
	contentV, err := oleutil.GetProperty(binding, "Content")
	if err == nil && contentV != nil {
		xmlDisp := contentV.ToIDispatch()
		if xmlDisp != nil {
			defer xmlDisp.Release()
			xmlStrV, err := oleutil.CallMethod(xmlDisp, "GetXml")
			if err == nil {
				raw := xmlStrV.ToString()
				title, body = parseToastXML(raw)
				return
			}
		}
	}

	// Fallback: enumerate children
	var lines []string
	childrenV, err := oleutil.GetProperty(binding, "Children")
	if err != nil {
		return "", ""
	}
	children := childrenV.ToIDispatch()
	if children == nil {
		return "", ""
	}
	defer children.Release()

	sizeV, _ := oleutil.GetProperty(children, "Size")
	size := int(sizeV.Val)
	for i := 0; i < size; i++ {
		childV, err := oleutil.CallMethod(children, "GetAt", uint32(i))
		if err != nil {
			continue
		}
		child := childV.ToIDispatch()
		if child == nil {
			continue
		}
		textV, err := oleutil.GetProperty(child, "Text")
		child.Release()
		if err == nil {
			lines = append(lines, textV.ToString())
		}
	}
	if len(lines) > 0 {
		title = lines[0]
	}
	if len(lines) > 1 {
		body = strings.Join(lines[1:], "\n")
	}
	return
}

// parseToastXML extracts title and body from a Windows toast XML document.
func parseToastXML(raw string) (title, body string) {
	type Text struct {
		XMLName xml.Name `xml:"text"`
		Value   string   `xml:",chardata"`
	}
	type Binding struct {
		Texts []Text `xml:"text"`
	}
	type Visual struct {
		Binding Binding `xml:"binding"`
	}
	type Toast struct {
		Visual Visual `xml:"visual"`
	}

	var toast Toast
	if err := xml.Unmarshal([]byte(raw), &toast); err != nil {
		return raw, ""
	}
	texts := toast.Visual.Binding.Texts
	if len(texts) > 0 {
		title = texts[0].Value
	}
	if len(texts) > 1 {
		var parts []string
		for _, t := range texts[1:] {
			parts = append(parts, t.Value)
		}
		body = strings.Join(parts, "\n")
	}
	return
}

func (e *Engine) shouldForward(n *Notification) bool {
	lower := strings.ToLower(n.AppName)

	if _, ignored := e.ignoreApps[lower]; ignored {
		return false
	}
	if len(e.filterApps) > 0 {
		_, allowed := e.filterApps[lower]
		return allowed
	}
	return true
}

// RequestAccess requests UserNotificationListener access from Windows.
// Must be called once before the capture loop; shows a system dialog.
func RequestAccess(log *logrus.Logger) error {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 0x00000001 {
			return err
		}
	}
	defer ole.CoUninitialize()

	unknown, err := oleutil.CreateObject("Windows.UI.Notifications.Management.UserNotificationListener")
	if err != nil {
		return err
	}
	dispatch, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		return err
	}
	defer dispatch.Release()
	unknown.Release()

	result, err := oleutil.CallMethod(dispatch, "RequestAccessAsync")
	if err != nil {
		log.WithError(err).Warn("RequestAccessAsync failed")
		return err
	}
	log.Info("Notification access requested (status=", result.Val, ")")
	return nil
}
