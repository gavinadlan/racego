// Package migrations menyimpan file SQL migrasi yang ditanam ke dalam binary.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
