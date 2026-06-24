static/fonts/vt323.woff2 is the "latin" subset of the VT323 typeface by Peter
Hull, distributed via Google Fonts (https://fonts.google.com/specimen/VT323),
licensed under the SIL Open Font License 1.1 (https://openfontlicense.org).
Downloaded from https://fonts.gstatic.com/s/vt323/v18/pxiKyp0ihIEF2isfFJU.woff2
on 2026-06-24.

static/htmx.min.js is htmx 2.0.10 (https://htmx.org), licensed under the
Zero-Clause BSD license. Downloaded from
https://unpkg.com/htmx.org@2.0.10/dist/htmx.min.js on 2026-06-24.

Both are vendored here rather than linked from a CDN so the admin UI has no
runtime network dependency (embedded via Go's embed.FS, per design/09 §1).
