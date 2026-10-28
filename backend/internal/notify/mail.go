// Package notify mengirim email transaksional (tiket terbit, dll).
package notify

import (
	"fmt"
	"html"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Mailer struct {
	addr, user, pass, from string
}

func NewMailer(addr, user, pass, from string) *Mailer {
	return &Mailer{addr: addr, user: user, pass: pass, from: from}
}

func (m *Mailer) Send(to, subject, htmlBody string) error {
	from, err := mail.ParseAddress(m.from)
	if err != nil {
		return fmt.Errorf("MAIL_FROM tidak valid: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		from.String(), to, mimeSubject(subject), time.Now().Format(time.RFC1123Z), htmlBody)

	var auth smtp.Auth
	if m.user != "" {
		host, _, _ := net.SplitHostPort(m.addr)
		auth = smtp.PlainAuth("", m.user, m.pass, host)
	}
	return smtp.SendMail(m.addr, auth, from.Address, []string{to}, []byte(b.String()))
}

func mimeSubject(s string) string {
	return "=?UTF-8?Q?" + strings.ReplaceAll(qEncode(s), " ", "_") + "?="
}

func qEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 33 && c <= 126 && c != '=' && c != '?' && c != '_' {
			b.WriteByte(c)
		} else if c == ' ' {
			b.WriteByte(' ')
		} else {
			fmt.Fprintf(&b, "=%02X", c)
		}
	}
	return b.String()
}

type TicketLine struct {
	Participant, Category, Bib string
}

// TicketEmail membuat isi email "tiket terbit". QR ditampilkan di halaman Tiket Saya
// agar selalu yang terbaru (dan bisa dibuat dinamis saat hari-H).
func TicketEmail(buyerName, eventName, ticketsURL string, lines []TicketLine) (string, string) {
	var rows strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&rows, `<tr><td style="padding:6px 12px">%s</td><td style="padding:6px 12px">%s</td><td style="padding:6px 12px;font-weight:600">%s</td></tr>`,
			html.EscapeString(l.Participant), html.EscapeString(l.Category), html.EscapeString(l.Bib))
	}
	subject := "Tiket " + eventName + " sudah terbit"
	body := fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:560px">
<h2>Pembayaran berhasil 🎉</h2>
<p>Halo %s, tiket untuk <b>%s</b> sudah terbit.</p>
<table style="border-collapse:collapse;border:1px solid #ddd"><tr style="background:#f5f5f5"><th style="padding:6px 12px;text-align:left">Peserta</th><th style="padding:6px 12px;text-align:left">Kategori</th><th style="padding:6px 12px;text-align:left">BIB</th></tr>%s</table>
<p><a href="%s" style="display:inline-block;margin-top:16px;padding:10px 18px;background:#111;color:#fff;border-radius:8px;text-decoration:none">Lihat QR tiket</a></p>
<p style="color:#666;font-size:13px">Tunjukkan QR beserta KTP saat pengambilan race pack.</p></div>`,
		html.EscapeString(buyerName), html.EscapeString(eventName), rows.String(), html.EscapeString(ticketsURL))
	return subject, body
}
