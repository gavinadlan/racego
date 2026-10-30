// Package notify mengirim email transaksional (tiket terbit, dll).
package notify

import (
	"crypto/tls"
	"fmt"
	"html"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// sendTimeout membatasi satu pengiriman (dial sampai QUIT); kode OTP dikirim langsung dari request API.
const sendTimeout = 10 * time.Second

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

	// Sama seperti smtp.SendMail, tapi dengan batas waktu agar server SMTP yang macet tidak menggantung request.
	host, _, _ := net.SplitHostPort(m.addr)
	conn, err := net.DialTimeout("tcp", m.addr, sendTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(sendTimeout)); err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if m.user != "" {
		if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write([]byte(b.String())); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
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

// OTPEmail membuat isi email kode masuk. Kode di subjek supaya terbaca dari notifikasi.
func OTPEmail(code string) (string, string) {
	subject := "Kode masuk RaceGo: " + code
	body := fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:560px">
<h2>Kode masuk RaceGo</h2>
<p>Masukkan kode ini di halaman masuk:</p>
<p style="font-size:36px;font-weight:700;letter-spacing:8px;margin:16px 0">%s</p>
<p>Kode berlaku 10 menit dan hanya bisa dipakai sekali.</p>
<p style="color:#666;font-size:13px">Bukan kamu yang meminta kode ini? Abaikan saja email ini; akunmu tetap aman.</p></div>`,
		html.EscapeString(code))
	return subject, body
}

// VerificationEmail: status verifikasi mitra untuk owner. event = submitted | approved | rejected.
// Sengaja TIDAK memuat data sensitif (NIK, nomor rekening, NPWP); hanya nama org, status, dan alasan.
func VerificationEmail(orgName, event, reason, pageURL string) (string, string) {
	org := html.EscapeString(orgName)
	var subject, msg string
	switch event {
	case "submitted":
		subject = "Pengajuan verifikasi " + orgName + " diterima"
		msg = fmt.Sprintf(`<p>Pengajuan verifikasi <b>%s</b> sudah kami terima dan sedang menunggu peninjauan tim RaceGo.</p>
<p>Selama ditinjau, isian dan dokumen tidak bisa diubah. Kami kabari lewat email setelah selesai.</p>`, org)
	case "approved":
		subject = orgName + " sudah terverifikasi"
		msg = fmt.Sprintf(`<p><b>%s</b> sudah terverifikasi. Sekarang kamu bisa menerbitkan event.</p>`, org)
	default:
		subject = "Pengajuan verifikasi " + orgName + " perlu diperbaiki"
		msg = fmt.Sprintf(`<p>Pengajuan verifikasi <b>%s</b> belum bisa kami setujui. Alasannya:</p>
<blockquote style="margin:12px 0;padding:10px 14px;border-left:4px solid #ff5a1f;background:#fff4ef">%s</blockquote>
<p>Buka halaman verifikasi, klik <b>Ajukan ulang</b>, perbaiki, lalu kirim lagi.</p>`, org, strings.ReplaceAll(html.EscapeString(reason), "\n", "<br>"))
	}
	body := fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:560px">
<h2>Verifikasi penyelenggara</h2>
%s
<p><a href="%s" style="display:inline-block;margin-top:16px;padding:10px 18px;background:#111;color:#fff;border-radius:8px;text-decoration:none">Buka halaman verifikasi</a></p></div>`,
		msg, html.EscapeString(pageURL))
	return subject, body
}

// PayoutEmail: status pencairan untuk owner/finance. event = requested | approved | rejected | completed | failed.
// Hanya nama org, jumlah, status, alasan, dan 4 digit akhir rekening (tanpa nomor lengkap).
func PayoutEmail(orgName, event, amount, reason, last4, pageURL string) (string, string) {
	org := html.EscapeString(orgName)
	labels := map[string]string{
		"requested": "diajukan dan menunggu persetujuan tim RaceGo",
		"approved":  "disetujui dan sedang diproses",
		"rejected":  "ditolak; saldo dikembalikan ke saldo tersedia",
		"completed": "selesai ditransfer",
		"failed":    "gagal ditransfer; saldo dikembalikan ke saldo tersedia",
	}
	subject := "Pencairan " + amount + " " + orgName + ": " + map[string]string{
		"requested": "diajukan", "approved": "disetujui", "rejected": "ditolak", "completed": "selesai", "failed": "gagal"}[event]
	msg := fmt.Sprintf(`<p>Pencairan dana <b>%s</b> untuk <b>%s</b> ke rekening ••••%s %s.</p>`,
		html.EscapeString(amount), org, html.EscapeString(last4), labels[event])
	if reason != "" {
		msg += fmt.Sprintf(`<p>Alasan:</p><blockquote style="margin:12px 0;padding:10px 14px;border-left:4px solid #ff5a1f;background:#fff4ef">%s</blockquote>`,
			strings.ReplaceAll(html.EscapeString(reason), "\n", "<br>"))
	}
	return subject, financeBody("Pencairan dana", msg, pageURL, "Buka halaman keuangan")
}

// RefundEmail: status refund. audience "buyer" (pembeli) atau "org" (owner/finance).
// event = requested | approved | rejected | manual_required | completed | failed. Tanpa data sensitif.
func RefundEmail(audience, eventName, orderShort, amount, event, reason, pageURL string) (string, string) {
	ev := html.EscapeString(eventName)
	var subject, msg string
	if audience == "buyer" {
		switch event {
		case "approved":
			subject = "Refund " + eventName + " disetujui"
			msg = fmt.Sprintf(`<p>Refund order #%s untuk <b>%s</b> sebesar <b>%s</b> disetujui. Tiket order ini tidak berlaku lagi. Dana sedang dikembalikan.</p>`,
				html.EscapeString(orderShort), ev, html.EscapeString(amount))
		case "completed":
			subject = "Refund " + eventName + " selesai"
			msg = fmt.Sprintf(`<p>Dana refund order #%s untuk <b>%s</b> sebesar <b>%s</b> sudah dikembalikan.</p>`,
				html.EscapeString(orderShort), ev, html.EscapeString(amount))
		default:
			subject = "Refund " + eventName + " tidak disetujui"
			msg = fmt.Sprintf(`<p>Pengajuan refund order #%s untuk <b>%s</b> tidak disetujui. Tiketmu tetap berlaku.</p>`,
				html.EscapeString(orderShort), ev)
		}
	} else {
		labels := map[string]string{"requested": "diajukan", "approved": "disetujui", "rejected": "ditolak",
			"manual_required": "perlu dikirim manual oleh tim RaceGo", "completed": "selesai", "failed": "gagal dikirim otomatis"}
		subject = "Refund order #" + orderShort + " " + labels[event]
		msg = fmt.Sprintf(`<p>Refund order #%s (<b>%s</b>) sebesar <b>%s</b> %s.</p>`,
			html.EscapeString(orderShort), ev, html.EscapeString(amount), labels[event])
	}
	if reason != "" && event == "rejected" {
		msg += fmt.Sprintf(`<p>Alasan:</p><blockquote style="margin:12px 0;padding:10px 14px;border-left:4px solid #ff5a1f;background:#fff4ef">%s</blockquote>`,
			strings.ReplaceAll(html.EscapeString(reason), "\n", "<br>"))
	}
	return subject, financeBody("Refund", msg, pageURL, "Buka RaceGo")
}

func financeBody(title, msg, pageURL, cta string) string {
	return fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:560px">
<h2>%s</h2>
%s
<p><a href="%s" style="display:inline-block;margin-top:16px;padding:10px 18px;background:#111;color:#fff;border-radius:8px;text-decoration:none">%s</a></p></div>`,
		title, msg, html.EscapeString(pageURL), cta)
}
