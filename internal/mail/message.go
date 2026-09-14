package mail

import (
	"fmt"
	"mime"
	"strings"
	"time"
)

// Event names. Each is also the suffix of the setting that switches it off
// (mail.notify_<switch>).
const (
	EventReviewRequested = "review.requested"
	EventReviewDecided   = "review.decided"
	EventAppStatus       = "app.status_changed"
	EventTest            = "test"
)

// Event describes one notification kind for the settings screen.
type Event struct {
	Name   string `json:"name"`
	Switch string `json:"switch"`
	Label  string `json:"label"`
	Help   string `json:"help"`
}

// Events is what this service tells people about. The bar for a kind to be
// here is that without the mail somebody loses something or keeps refreshing
// a screen: a reviewer whose turn it is, an owner waiting on a decision, an
// owner whose app an administrator took down. A plain "something changed" is
// not mail.
var Events = []Event{
	{Name: EventReviewRequested, Switch: "review_requested", Label: "검토 요청",
		Help: "앱이 검토 대기에 들어가면 그 단계를 처리할 수 있는 검토자·팀장에게 보냅니다."},
	{Name: EventReviewDecided, Switch: "review_decided", Label: "검토 결과",
		Help: "앱이 승인되거나 반려되면 등록한 사람에게 보냅니다. 중간 단계 승인은 보내지 않습니다."},
	{Name: EventAppStatus, Switch: "app_status", Label: "관리자 상태 변경",
		Help: "관리자가 다른 사람의 앱 게시 상태를 바꾸면 소유자에게 보냅니다."},
}

// compose builds a MIME message. Korean subjects and names are encoded so
// relays and clients that predate UTF-8 headers still show them correctly.
func compose(config Config, message Message) string {
	var builder strings.Builder
	builder.WriteString("From: " + encodeAddress(config.Address()) + "\r\n")
	builder.WriteString("To: " + strings.TrimSpace(message.To) + "\r\n")
	builder.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", headerLine(message.Subject)) + "\r\n")
	builder.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	builder.WriteString("Auto-Submitted: auto-generated\r\n")
	builder.WriteString("X-AppStore-Notification: 1\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(normalizeBody(message.Body))
	return builder.String()
}

func encodeAddress(address string) string {
	open := strings.LastIndex(address, "<")
	if open <= 0 {
		return address
	}
	return mime.QEncoding.Encode("utf-8", strings.TrimSpace(address[:open])) + " " + address[open:]
}

// headerLine keeps a subject to one header line whatever an app name holds.
func headerLine(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")), " ")
}

// normalizeBody uses CRLF line endings. Dot-stuffing is left to the DATA
// writer of net/smtp, which already escapes a leading dot; doing it here as
// well would deliver a doubled dot.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if !strings.HasSuffix(body, "\r\n") {
		body += "\r\n"
	}
	return body
}

// Notification is the content of one event mail before recipients are
// resolved. Reference identifies the record it is about (an app id) so the
// delivery log can be searched by it; the body is never stored.
type Notification struct {
	Event     string
	Subject   string
	Lines     []string
	Reference string
	// Link is a path under mail.base_url, or an absolute URL.
	Link string
}

// Render turns a notification into the message body, appending the link and
// a footer that says why the mail arrived.
func (n Notification) Render(config Config) string {
	lines := append([]string{}, n.Lines...)
	if link := n.absoluteLink(config); link != "" {
		lines = append(lines, "", "바로 열기: "+link)
	}
	lines = append(lines, "", "—", "이 메일은 AppStore 알림 설정에 따라 자동으로 발송되었습니다. 회신은 읽지 않습니다.")
	return strings.Join(lines, "\n")
}

func (n Notification) absoluteLink(config Config) string {
	if strings.HasPrefix(n.Link, "http://") || strings.HasPrefix(n.Link, "https://") {
		return n.Link
	}
	base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if base == "" || n.Link == "" {
		return ""
	}
	return base + "/" + strings.TrimLeft(n.Link, "/")
}

// ReviewRequested tells the people who can decide a review that an app is
// waiting on them.
func ReviewRequested(submitter, appName, appID string, level, levels int) Notification {
	step := ""
	if levels > 1 {
		step = fmt.Sprintf(" (%d/%d단계)", level, levels)
	}
	return Notification{
		Event:     EventReviewRequested,
		Subject:   fmt.Sprintf("[AppStore] '%s' 앱이 검토를 기다립니다%s", appName, step),
		Reference: appID,
		Link:      "/review",
		Lines: []string{
			fmt.Sprintf("%s 님이 등록한 '%s' 앱이 검토 대기에 들어갔습니다%s.", submitter, appName, step),
			"검토 목록에서 승인하거나 반려 사유와 함께 돌려보낼 수 있습니다.",
		},
	}
}

// ReviewDecided tells the submitter how a review ended. Only a final
// decision is worth a mail: a rejection they must act on, or the approval
// that makes the app visible.
func ReviewDecided(reviewer, appName, appID, appSlug string, approved, published bool, reason string) Notification {
	if !approved {
		lines := []string{fmt.Sprintf("%s 님이 '%s' 앱을 반려했습니다.", reviewer, appName)}
		if strings.TrimSpace(reason) != "" {
			lines = append(lines, "", quote(reason))
		}
		lines = append(lines, "", "내 앱에서 지적된 내용을 고쳐 저장하면 다시 검토 대기에 들어갑니다.")
		return Notification{
			Event: EventReviewDecided, Subject: fmt.Sprintf("[AppStore] '%s' 앱이 반려되었습니다", appName),
			Reference: appID, Link: "/my/apps", Lines: lines,
		}
	}
	if published {
		return Notification{
			Event: EventReviewDecided, Subject: fmt.Sprintf("[AppStore] '%s' 앱이 승인되어 게시되었습니다", appName),
			Reference: appID, Link: "/apps/" + appSlug,
			Lines: []string{fmt.Sprintf("%s 님이 '%s' 앱을 승인했습니다. 지금부터 카탈로그에서 볼 수 있습니다.", reviewer, appName)},
		}
	}
	return Notification{
		Event: EventReviewDecided, Subject: fmt.Sprintf("[AppStore] '%s' 앱이 승인되었습니다", appName),
		Reference: appID, Link: "/my/apps",
		Lines: []string{
			fmt.Sprintf("%s 님이 '%s' 앱을 승인했습니다.", reviewer, appName),
			"자동 게시가 꺼져 있어 관리자가 게시 상태로 바꾸면 카탈로그에 실립니다.",
		},
	}
}

// AppStatusChanged tells an owner that an administrator changed where their
// app stands — most often that it was taken out of the catalogue.
func AppStatusChanged(actor, appName, appID, appSlug, fromStatus, toStatus string) Notification {
	link := "/my/apps"
	if toStatus == "published" {
		link = "/apps/" + appSlug
	}
	return Notification{
		Event:     EventAppStatus,
		Subject:   fmt.Sprintf("[AppStore] '%s' 앱 상태가 %s(으)로 바뀌었습니다", appName, StatusLabel(toStatus)),
		Reference: appID,
		Link:      link,
		Lines: []string{
			fmt.Sprintf("%s 님이 '%s' 앱의 상태를 %s에서 %s(으)로 바꿨습니다.", actor, appName, StatusLabel(fromStatus), StatusLabel(toStatus)),
			"문의는 관리자에게 하세요. 이 메일에 회신해도 전달되지 않습니다.",
		},
	}
}

// TestMessage proves the relay works from the settings screen.
func TestMessage() Notification {
	return Notification{
		Event:   EventTest,
		Subject: "[AppStore] SMTP 발송 테스트",
		Lines:   []string{"AppStore 관리자 화면에서 보낸 테스트 메일입니다.", "이 메일을 받았다면 SMTP 설정이 정상입니다."},
	}
}

// StatusLabel is the wording the console uses for an app status.
func StatusLabel(status string) string {
	switch status {
	case "draft":
		return "초안"
	case "pending_review":
		return "검토 대기"
	case "published":
		return "게시됨"
	case "rejected":
		return "반려"
	case "archived":
		return "보관됨"
	}
	return status
}

func quote(body string) string {
	trimmed := strings.TrimSpace(body)
	if len([]rune(trimmed)) > 500 {
		trimmed = string([]rune(trimmed)[:500]) + "…"
	}
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	for index, line := range lines {
		lines[index] = "> " + line
	}
	return strings.Join(lines, "\n")
}
