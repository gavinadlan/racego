-- Profil publik penyelenggara & tampilan event (kalender, arsip, halaman penyelenggara).

ALTER TABLE organizations
    ADD COLUMN description  text NOT NULL DEFAULT '',
    ADD COLUMN city         text NOT NULL DEFAULT '',
    ADD COLUMN instagram    text,
    ADD COLUMN website      text,
    ADD COLUMN theme_color  text NOT NULL DEFAULT '#FF5B23',
    ADD COLUMN logo_url     text;

ALTER TABLE events
    ADD COLUMN tagline        text NOT NULL DEFAULT '',
    ADD COLUMN start_time     text NOT NULL DEFAULT '05:00',  -- jam flag-off, waktu lokal
    ADD COLUMN racepack_info  text NOT NULL DEFAULT '',
    ADD COLUMN theme_color    text,                           -- null = pakai warna penyelenggara
    ADD COLUMN cover_url      text;                           -- poster; null = sampul otomatis

CREATE INDEX events_city_idx ON events (lower(city));
