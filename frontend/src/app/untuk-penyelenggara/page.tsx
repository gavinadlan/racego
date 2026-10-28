import type { Metadata } from "next";
import Link from "next/link";
import { btnPrimary, SectionHead } from "@/components/ui";

export const metadata: Metadata = { title: "Untuk penyelenggara" };

const FEATURES = [
  { title: "Daftar dan kelola event sendiri", text: "Daftarkan komunitas atau perusahaanmu, buat event, unggah poster, lalu terbitkan sendiri dari dashboard." },
  { title: "Kategori dan kuota", text: "Atur 5K sampai 42K dengan harga, kuota, dan batas slot per akun. Kuota bisa ditambah saat penjualan berjalan." },
  { title: "Tiket QR dan nomor BIB otomatis", text: "Begitu pembayaran lunas lewat Midtrans, tiket ber-QR dan nomor BIB terbit per peserta dan dikirim ke email." },
  { title: "Scanner race pack dan tim staf", text: "Tambahkan staf ke tim. Mereka membuka halaman scan di HP, arahkan kamera ke QR, dan scan dobel langsung ketahuan." },
  { title: "Laporan penjualan", text: "Pantau tiket terjual, nilai penjualan, dan progres pengambilan race pack per event dari dashboard." },
  { title: "Tahan lonjakan", text: "Kuota dipotong secara atomik sehingga tidak pernah terjual melebihi kuota. Untuk event yang diburu ribuan orang, aktifkan antrian virtual: pembeli diundi dan dipanggil bertahap." },
];

const SOON = [
  ["Pencairan dana", "Saldo penjualan dicairkan ke rekening penyelenggara."],
  ["Refund", "Pengembalian dana peserta dari dashboard."],
  ["Kupon dan harga bertahap", "Early bird, kode promo, dan gelombang harga."],
  ["Unduh data peserta", "Ekspor peserta ke CSV/Excel."],
];

const STEPS = [
  ["Daftar sebagai penyelenggara", "Masuk dengan email, lalu isi nama, kota, dan profil singkat komunitas atau perusahaanmu. Kurang dari dua menit."],
  ["Buat event", "Isi info race, unggah poster, atur kategori, harga, kuota, jadwal penjualan, dan antrian virtual bila perlu. Tersimpan sebagai draft dulu."],
  ["Verifikasi", "Tim RaceGo memverifikasi penyelenggara sebelum event pertama bisa terbit. Sambil menunggu, draft tetap bisa disiapkan."],
  ["Terbitkan dan pantau", "Event langsung tampil di kalender. Pantau penjualan, tambah staf, dan scan race pack dari dashboard."],
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

      <section aria-labelledby="segera-hadir">
        <h2 id="segera-hadir" className="font-display text-4xl leading-none font-extrabold sm:text-5xl">
          Segera hadir
        </h2>
        <p className="mt-2 max-w-xl text-muted">Belum tersedia di versi ini. Kami tulis di sini supaya tidak ada yang mengira fiturnya sudah jalan.</p>
        <ul className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {SOON.map(([t, d]) => (
            <li key={t} className="rounded-md border border-dashed border-line p-4">
              <span className="inline-block rounded-full bg-line px-2.5 py-0.5 text-xs font-semibold text-muted">Belum tersedia</span>
              <p className="mt-2 font-semibold">{t}</p>
              <p className="mt-1 text-sm text-muted">{d}</p>
            </li>
          ))}
        </ul>
      </section>

      <section className="grid gap-10 rounded-md bg-asphalt p-8 text-white sm:p-12 lg:grid-cols-[1fr_1.4fr]">
        <div>
          <h2 className="font-display text-5xl leading-[0.92] font-extrabold">Biaya</h2>
          <p className="mt-4 text-white/70">Tanpa biaya bulanan dan tanpa biaya setup. Fee hanya dihitung dari tiket yang terjual.</p>
        </div>
        <div className="grid gap-6 sm:grid-cols-2">
          <div>
            <div className="font-display text-7xl leading-none font-black text-hivis">3%</div>
            <p className="mt-2 text-sm text-white/70">fee platform dari setiap tiket terjual, tercatat di pembukuan per order</p>
          </div>
          <div>
            <div className="font-display text-7xl leading-none font-black">Rp0</div>
            <p className="mt-2 text-sm text-white/70">untuk membuat event, dashboard, dan scanner race pack</p>
          </div>
          <p className="text-xs text-white/50 sm:col-span-2">
            Pencairan dana belum tersedia di versi ini: penjualan tercatat, tetapi belum bisa ditarik ke rekening dari dashboard. Biaya payment gateway mengikuti
            metode bayar (misalnya VA Rp4.440 per transaksi termasuk PPN). Angka di proyek ini adalah contoh.
          </p>
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
