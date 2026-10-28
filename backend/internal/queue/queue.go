// Package queue adalah antrian virtual (waiting room) untuk event yang mengaktifkannya.
//
// Alur: lobi dibuka QUEUE_LOBBY_MINUTES sebelum penjualan → peserta lobi mendapat skor acak →
// pada jam buka urutan = urutan skor → worker memanggil peserta dari depan antrian sesuai
// kapasitas → peserta yang dipanggil memegang pass bertanda tangan untuk reservasi.
// Semua key satu event memakai hash tag {ev:<id>} (aman untuk Redis Cluster).
package queue

import (
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"math/big"
	mrand "math/rand/v2"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/checkout"
	"github.com/gavinadlan/racego/backend/internal/config"
)

var (
	//go:embed lua/join.lua
	joinSrc string
	//go:embed lua/status.lua
	statusSrc string
	//go:embed lua/admit.lua
	admitSrc string
	//go:embed lua/release.lua
	releaseSrc string

	joinScript    = redis.NewScript(joinSrc)
	statusScript  = redis.NewScript(statusSrc)
	admitScript   = redis.NewScript(admitSrc)
	releaseScript = redis.NewScript(releaseSrc)
)

// Status akhir antrian.
const (
	StateLobby      = "lobby"
	StateWaiting    = "waiting"
	StateAdmitted   = "admitted"
	StateSoldOut    = "sold_out"
	StateClosed     = "closed"
	StateNotInQueue = "not_in_queue"
)

const (
	finalTTL       = 10 * time.Minute // antrian event yang habis/tutup dibersihkan setelah ini
	lobbyScoreBits = 52
)

// Event adalah data event yang dibutuhkan antrian (dibaca dari Postgres, di-cache pemanggil).
type Event struct {
	ID          string
	SaleOpenAt  time.Time
	SaleCloseAt time.Time
	Closed      bool // status 'closed' (mitra menutup penjualan)
	CategoryIDs []string
}

func (e Event) ended(now time.Time) bool { return e.Closed || !now.Before(e.SaleCloseAt) }

type keys struct{ base string }

func keysFor(eventID string) keys { return keys{base: "{ev:" + eventID + "}"} }
func (k keys) queue() string      { return k.base + ":queue" }
func (k keys) seen() string       { return k.base + ":seen" }
func (k keys) active() string     { return k.base + ":active" }
func (k keys) opened() string     { return k.base + ":opened" }
func (k keys) meta() string       { return k.base + ":meta" }
func (k keys) final() string      { return k.base + ":final" }
func (k keys) all() []string {
	return []string{k.queue(), k.seen(), k.active(), k.opened(), k.meta(), k.final()}
}
func (k keys) statusKeys() []string {
	return []string{k.queue(), k.seen(), k.active(), k.meta(), k.final()}
}
func (k keys) releaseKeys() []string { return []string{k.queue(), k.seen(), k.active()} }

// State adalah jawaban GET /queue/me.
type State struct {
	State         string     `json:"state"`
	Position      *int       `json:"position"` // 1 = paling depan; null di lobi (urutan belum dibekukan)
	Ahead         *int       `json:"ahead"`
	QueueSize     int        `json:"queue_size"`
	ETASeconds    *int       `json:"eta_seconds"` // null bila laju panggil belum terukur
	Pass          string     `json:"pass,omitempty"`
	PassExpiresAt *time.Time `json:"pass_expires_at,omitempty"`
	PollAfterMs   int        `json:"poll_after_ms"` // 0 = tidak perlu polling lagi
	ServerTime    time.Time  `json:"server_time"`
	LobbyOpensAt  time.Time  `json:"lobby_opens_at"`
	SaleOpenAt    time.Time  `json:"sale_open_at"`
}

type Queue struct {
	rdb    *redis.Client
	inv    *checkout.Inventory
	signer *PassSigner
	cfg    config.QueueConfig
	jitter func() float64 // 0..1, diganti di tes
}

func New(rdb *redis.Client, inv *checkout.Inventory, signer *PassSigner, cfg config.QueueConfig) *Queue {
	return &Queue{rdb: rdb, inv: inv, signer: signer, cfg: cfg, jitter: mrand.Float64}
}

// LobbyOpensAt: lobi dibuka QUEUE_LOBBY_MINUTES sebelum penjualan.
func (q *Queue) LobbyOpensAt(ev Event) time.Time {
	return ev.SaleOpenAt.Add(-time.Duration(q.cfg.LobbyMinutes) * time.Minute)
}

// Open: antrian menerima peserta (lobi atau penjualan berjalan).
func (q *Queue) Open(ev Event, now time.Time) bool {
	return !now.Before(q.LobbyOpensAt(ev)) && !ev.ended(now)
}

func lobbyScore() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<lobbyScoreBits))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}

// Join memasukkan user ke antrian (atau mengembalikan posisinya bila sudah ada).
// created=false bila user sudah di antrian atau sedang memegang pass.
func (q *Queue) Join(ctx context.Context, ev Event, userID string, now time.Time) (State, bool, error) {
	score, err := lobbyScore()
	if err != nil {
		return State{}, false, err
	}
	k := keysFor(ev.ID)
	n, err := joinScript.Run(ctx, q.rdb, []string{k.queue(), k.seen(), k.active(), k.opened()},
		userID, score, now.UnixMilli(), ev.SaleOpenAt.UnixMilli()).Int()
	if err != nil {
		return State{}, false, err
	}
	st, err := q.Status(ctx, ev, userID, now)
	return st, n == 1, err
}

// Status membaca posisi user sekaligus memperbarui tanda hidupnya.
func (q *Queue) Status(ctx context.Context, ev Event, userID string, now time.Time) (State, error) {
	st := State{ServerTime: now, LobbyOpensAt: q.LobbyOpensAt(ev), SaleOpenAt: ev.SaleOpenAt}
	if ev.ended(now) {
		st.State = StateClosed
		return st, nil
	}
	res, err := statusScript.Run(ctx, q.rdb, keysFor(ev.ID).statusKeys(), userID, now.UnixMilli()).Slice()
	if err != nil {
		return st, err
	}
	switch toInt(res[0]) {
	case 3:
		st.State, _ = res[1].(string)
	case 2:
		exp := time.UnixMilli(toInt(res[1]))
		st.State = StateAdmitted
		st.Pass = q.signer.Sign(ev.ID, userID, exp)
		st.PassExpiresAt = &exp
		st.PollAfterMs = q.poll(5000)
	case 1:
		rank, size := int(toInt(res[1])), int(toInt(res[2]))
		st.QueueSize = size
		if now.Before(ev.SaleOpenAt) {
			st.State = StateLobby
			st.PollAfterMs = lobbyPoll(ev.SaleOpenAt.Sub(now), q.jitter())
			break
		}
		pos := rank + 1
		st.State, st.Position, st.Ahead = StateWaiting, &pos, &rank
		if rate, _ := strconv.ParseFloat(fmt.Sprint(res[3]), 64); rate > 0 {
			eta := int(math.Ceil(float64(rank) / rate))
			st.ETASeconds = &eta
		}
		st.PollAfterMs = q.poll(waitingPollBase(rank))
	default:
		st.State = StateNotInQueue
		st.QueueSize = int(toInt(res[1]))
	}
	return st, nil
}

// waitingPollBase: dekat depan antrian 2,5 detik, makin jauh makin jarang, maksimal 15 detik.
func waitingPollBase(ahead int) int { return min(2500+ahead*10, 15000) }

// poll menambah jitter ±20% supaya ribuan klien tidak menembak di milidetik yang sama.
func (q *Queue) poll(base int) int { return int(float64(base) * (0.8 + 0.4*q.jitter())) }

// lobbyPoll: di lobi cukup jarang; menjelang jam buka, polling pertama setelah buka disebar
// 0–5 detik agar seluruh lobi tidak masuk serempak.
func lobbyPoll(untilOpen time.Duration, jitter float64) int {
	ms := int(untilOpen.Milliseconds())
	if ms > 15000 {
		return int(15000 * (0.8 + 0.4*jitter))
	}
	return max(ms, 0) + 500 + int(4500*jitter)
}

// Leave mengeluarkan user dari antrian dan mencabut pass-nya.
func (q *Queue) Leave(ctx context.Context, eventID, userID string) error {
	return releaseScript.Run(ctx, q.rdb, keysFor(eventID).releaseKeys(), userID).Err()
}

// ReleasePass mengembalikan slot kapasitas (reservasi berhasil). Posisi antrian sudah tidak ada.
func (q *Queue) ReleasePass(ctx context.Context, eventID, userID string) error {
	return q.rdb.ZRem(ctx, keysFor(eventID).active(), userID).Err()
}

// PassValid: user masih memegang pass aktif (belum dilepas, belum kedaluwarsa).
func (q *Queue) PassValid(ctx context.Context, eventID, userID string, now time.Time) (bool, error) {
	exp, err := q.rdb.ZScore(ctx, keysFor(eventID).active(), userID).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return err == nil && int64(exp) > now.UnixMilli(), err
}

// Check dipakai checkout.Reserve: pass sah, cocok dengan event & user, dan masih aktif.
func (q *Queue) Check(ctx context.Context, eventID, userID, token string, now time.Time) error {
	if token == "" {
		return checkout.ErrQueueRequired
	}
	switch err := q.signer.Verify(token, eventID, userID, now); {
	case errors.Is(err, errPassExpired):
		return checkout.ErrQueuePassExpired
	case err != nil:
		return checkout.ErrQueueRequired
	}
	ok, err := q.PassValid(ctx, eventID, userID, now)
	if err != nil {
		return err
	}
	if !ok {
		return checkout.ErrQueueRequired // dicabut: sudah dipakai, keluar antrian, atau data antrian hilang
	}
	return nil
}

// Admit memanggil peserta dari depan antrian (dijalankan worker tiap detik).
func (q *Queue) Admit(ctx context.Context, ev Event, now time.Time) (int, error) {
	r, err := q.admit(ctx, ev, now)
	return len(r.users), err
}

type admitResult struct {
	users   []string
	dropped int
	active  int
}

func (q *Queue) admit(ctx context.Context, ev Event, now time.Time) (admitResult, error) {
	k := keysFor(ev.ID)
	res, err := admitScript.Run(ctx, q.rdb, []string{k.queue(), k.seen(), k.active(), k.meta(), k.opened()},
		now.UnixMilli(), q.cfg.AdmitPerSec, q.cfg.MaxActive, q.cfg.PassTTL.Milliseconds(),
		q.cfg.HeartbeatTTL.Milliseconds(), ev.SaleOpenAt.UnixMilli()).Slice()
	if err != nil {
		return admitResult{}, err
	}
	out := admitResult{dropped: int(toInt(res[1])), active: int(toInt(res[2]))}
	for _, v := range res[3:] {
		out.users = append(out.users, v.(string))
	}
	return out, nil
}

// Tick adalah satu putaran worker untuk satu event: tutup antrian event yang selesai/habis,
// atau panggil peserta berikutnya. Saat stok 0 tapi masih ada hold (bisa kembali bila tidak
// dibayar), pemanggilan ditahan: tidak ada gunanya memberi pass untuk kuota kosong.
func (q *Queue) Tick(ctx context.Context, ev Event, now time.Time) (admitted int, err error) {
	if ev.ended(now) {
		return 0, q.finish(ctx, ev.ID, StateClosed)
	}
	if now.Before(ev.SaleOpenAt) {
		return 0, nil
	}
	stock, holds, loaded, err := q.inv.Totals(ctx, ev.CategoryIDs)
	if err != nil {
		return 0, err
	}
	if loaded && stock == 0 {
		if holds == 0 {
			return 0, q.finish(ctx, ev.ID, StateSoldOut)
		}
		return 0, nil
	}
	k := keysFor(ev.ID)
	// Kuota kembali (mis. mitra menambah kuota) setelah dinyatakan habis: buka lagi antriannya.
	if v, err := q.rdb.Get(ctx, k.final()).Result(); err == nil && v == StateSoldOut {
		if _, err := q.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Del(ctx, k.final())
			for _, key := range k.all() {
				p.Persist(ctx, key)
			}
			return nil
		}); err != nil {
			return 0, err
		}
	}
	return q.Admit(ctx, ev, now)
}

// finish menandai status akhir; semua key antrian event itu terhapus sendiri setelah finalTTL.
func (q *Queue) finish(ctx context.Context, eventID, state string) error {
	k := keysFor(eventID)
	set, err := q.rdb.SetNX(ctx, k.final(), state, finalTTL).Result()
	if err != nil || !set {
		return err
	}
	_, err = q.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, key := range k.all() {
			p.Expire(ctx, key, finalTTL)
		}
		return nil
	})
	return err
}

// Summary untuk dashboard mitra.
type Summary struct {
	QueueSize     int    `json:"queue_size"`
	Active        int    `json:"active"`
	AdmittedTotal int    `json:"admitted_total"`
	Final         string `json:"final,omitempty"` // sold_out | closed
}

func (q *Queue) Summary(ctx context.Context, eventID string, now time.Time) (Summary, error) {
	k := keysFor(eventID)
	var size, active *redis.IntCmd
	var total, final *redis.StringCmd
	_, err := q.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		size = p.ZCard(ctx, k.queue())
		active = p.ZCount(ctx, k.active(), strconv.FormatInt(now.UnixMilli()+1, 10), "+inf")
		total = p.HGet(ctx, k.meta(), "admitted_total")
		final = p.Get(ctx, k.final())
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return Summary{}, err
	}
	n, _ := total.Int()
	return Summary{QueueSize: int(size.Val()), Active: int(active.Val()), AdmittedTotal: n, Final: final.Val()}, nil
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case string: // skor ZSET dikirim Redis sebagai teks angka
		f, _ := strconv.ParseFloat(x, 64)
		return int64(f)
	}
	return 0
}
