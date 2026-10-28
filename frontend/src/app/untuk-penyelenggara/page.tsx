import type { Metadata } from "next";
import Link from "next/link";
import { btnPrimary, SectionHead } from "@/components/ui";

export const metadata: Metadata = { title: "Untuk penyelenggara" };

const FEATURES = [
  { title: "Kategori, kuota, dan gelombang harga", text: "Atur 5K sampai 42K, kuota per kategori, batas slot per akun, early bird, dan form data peserta." },
  { title: "Tetap jalan saat war tiket", text: "Antrian dan kuota atomik: ribuan pembeli di detik yang sama, tidak ada tiket terjual melebihi kuota." },
  { title: "Pembayaran terkonfirmasi otomatis", text: "VA, QRIS, e-wallet, dan kartu lewat Midtrans. Panitia tidak perlu mengecek bukti transfer satu per satu." },
  { title: "Tiket QR dan nomor BIB", text: "Tiket ber-QR dan nomor BIB terbit otomatis per peserta begitu pembayaran lunas." },
  { title: "Scan race pack dari HP", text: "Staf membuka halaman scan di browser, arahkan kamera ke QR. Scan dobel langsung ketahuan." },
  { title: "Laporan dan pencairan dana", text: "Penjualan per kategori secara real-time, data peserta siap diunduh, saldo dicairkan ke rekening penyelenggara." },
];

const STEPS = [
  ["Daftar sebagai penyelenggara", "Masuk dengan email, lalu isi nama, kota, dan profil singkat komunitas atau perusahaanmu. Kurang dari dua menit."],
  ["Buat event", "Isi info race, unggah poster, atur kategori, harga, kuota, dan jadwal penjualan. Tersimpan sebagai draft dulu."],
  ["Verifikasi", "Tim RaceGo memverifikasi penyelenggara sebelum event pertama bisa terbit. Sambil menunggu, draft tetap bisa disiapkan."],
  ["Terbitkan dan pantau", "Event langsung tampil di kalender. Pantau penjualan, undang staf, dan scan race pack dari dashboard."],
];

export default function ForOrganizersPage() {
  return (
    <div className="space-y-24">
      <section className="max-w-3xl">
        <h1 className="font-display text-[clamp(52px,8vw,96px)] leading-[0.88] font-black text-balance">
          Kamu urus rute dan race day. Pendaftaran biar kami.
        </h1>
        <p className="mt-6 max-w-xl text-lg text-muted">
          RaceGo menangani pendaftaran, pembayaran, tiket, dan pengambilan race pack untuk komunitas, EO, kampus, dan perusahaan yang menggelar race lari.
        </p>
        <div className="mt-8 flex flex-wrap items-center gap-4">
          <Link href="/daftar-mitra" className={btnPrimary}>
            Daftarkan penyelenggara
          </Link>
          <Link href="/penyelenggara" className="text-sm font-semibold underline decoration-hivis decoration-2 underline-offset-4">
            Lihat penyelenggara yang sudah bergabung
          </Link>
        </div>
      </section>

      <section>
        <SectionHead title="Yang kamu dapat" />
        <dl className="grid gap-x-10 gap-y-10 sm:grid-cols-2 lg:grid-cols-3">
          {FEATURES.map((f) => (
            <div key={f.title} className="border-l-4 border-hivis pl-5">
              <dt className="text-lg font-semibold">{f.title}</dt>
              <dd className="mt-2 text-sm text-muted">{f.text}</dd>
            </div>
          ))}
        </dl>
      </section>

      <section className="grid gap-10 rounded-md bg-asphalt p-8 text-white sm:p-12 lg:grid-cols-[1fr_1.4fr]">
        <div>
          <h2 className="font-display text-5xl leading-[0.92] font-extrabold">Biaya</h2>
          <p className="mt-4 text-white/70">Tanpa biaya bulanan dan tanpa biaya setup. Kamu hanya membayar saat tiket terjual.</p>
        </div>
        <div className="grid gap-6 sm:grid-cols-2">
          <div>
            <div className="font-display text-7xl leading-none font-black text-hivis">3%</div>
            <p className="mt-2 text-sm text-white/70">fee platform per tiket terjual, dipotong saat pencairan</p>
          </div>
          <div>
            <div className="font-display text-7xl leading-none font-black">Rp0</div>
            <p className="mt-2 text-sm text-white/70">untuk membuat event, dashboard, dan scanner race pack</p>
          </div>
          <p className="text-xs text-white/50 sm:col-span-2">Biaya payment gateway mengikuti metode bayar (misalnya VA Rp4.440 per transaksi termasuk PPN). Angka di proyek ini adalah contoh.</p>
        </div>
      </section>

      <section>
        <SectionHead title="Cara bergabung" />
        <ol className="grid gap-8 sm:grid-cols-2 lg:grid-cols-4">
          {STEPS.map(([t, d], i) => (
            <li key={t} className="border-t-4 border-asphalt pt-4">
              <span className="font-display text-6xl leading-none font-black text-hivis">{i + 1}</span>
              <h3 className="mt-3 text-lg font-semibold">{t}</h3>
              <p className="mt-1 text-sm text-muted">{d}</p>
            </li>
          ))}
        </ol>
      </section>
    </div>
  );
}
