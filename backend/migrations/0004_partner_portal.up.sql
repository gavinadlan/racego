-- Portal mitra: penyelenggara mendaftar sendiri, membuat & menerbitkan event.

-- Nomor BIB = bib_prefix + 4 digit urut, jadi prefix tidak boleh kembar di satu event.
CREATE UNIQUE INDEX ticket_categories_bib_prefix_uniq ON ticket_categories (event_id, bib_prefix);

-- Pembuat org (null untuk data seed); dipakai membatasi jumlah org per akun.
ALTER TABLE organizations ADD COLUMN created_by uuid REFERENCES users(id);
CREATE INDEX organizations_created_by_idx ON organizations (created_by);
